package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/metergraph/ezswap/internal/types"
)

const testKey = "sk-test-not-a-real-key-0000"

func str(s string) *string   { return &s }
func num(v float64) *float64 { return &v }
func strOrNil(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func call(edit func(*types.ReplayCall)) types.ReplayCall {
	c := types.ReplayCall{
		Model:  "openai/gpt-6-luna",
		System: str("Be brief."),
		Messages: []types.Message{
			{Role: "user", Content: "Hi"},
			{Role: "assistant", Content: "Hello"},
			{Role: "user", Content: "Capital of France?"},
		},
		MaxTokens: 256,
	}
	if edit != nil {
		edit(&c)
	}
	return c
}

// answer is a scripted reply: a response, a transport error, or a function of the request.
type answer struct {
	status  int
	body    []byte
	reader  io.Reader
	headers map[string]string
	err     error
	fn      func(*http.Request) (*http.Response, error)
}

func ok(body any) answer {
	data, _ := json.Marshal(body)
	return answer{status: 200, body: data, headers: map[string]string{"content-type": "application/json"}}
}

func okReply() answer { return ok(reply(str("Paris"), nil, "stop")) }

func status(code int, body string, headers map[string]string) answer {
	return answer{status: code, body: []byte(body), headers: headers}
}

func raw(code int, body []byte) answer { return answer{status: code, body: body} }

func fails(err error) answer { return answer{err: err} }

func reply(content *string, extra map[string]any, finish string) map[string]any {
	message := map[string]any{"role": "assistant", "content": nil}
	if content != nil {
		message["content"] = *content
	}
	for k, v := range extra {
		message[k] = v
	}
	return map[string]any{
		"choices": []any{map[string]any{"message": message, "finish_reason": finish}},
		"usage": map[string]any{
			"prompt_tokens":             120,
			"completion_tokens":         30,
			"prompt_tokens_details":     map[string]any{"cached_tokens": 100},
			"completion_tokens_details": map[string]any{"reasoning_tokens": 12},
		},
	}
}

type sent struct {
	url     string
	headers http.Header
	body    map[string]any
}

// script is a fake transport answering from a script, recording every request.
type script struct {
	mu      sync.Mutex
	answers []answer
	sent    []sent
	raw     []string
}

func newScript(answers ...answer) *script { return &script{answers: answers} }

func (s *script) RoundTrip(request *http.Request) (*http.Response, error) {
	data, _ := io.ReadAll(request.Body)
	var body map[string]any
	_ = json.Unmarshal(data, &body)
	s.mu.Lock()
	s.sent = append(s.sent, sent{url: request.URL.String(), headers: request.Header.Clone(), body: body})
	s.raw = append(s.raw, string(data))
	if len(s.answers) == 0 {
		s.mu.Unlock()
		return nil, errors.New("unexpected request")
	}
	next := s.answers[0]
	s.answers = s.answers[1:]
	s.mu.Unlock()
	if next.fn != nil {
		return next.fn(request)
	}
	if next.err != nil {
		return nil, next.err
	}
	header := http.Header{}
	for k, v := range next.headers {
		header.Set(k, v)
	}
	reader := next.reader
	if reader == nil {
		reader = strings.NewReader(string(next.body))
	}
	return &http.Response{StatusCode: next.status, Header: header, Body: io.NopCloser(reader), Request: request}, nil
}

func (s *script) client() *http.Client { return &http.Client{Transport: s} }

type harness struct {
	instance *OpenAICompatibleProvider
	sleeps   []float64
}

func provider(t *testing.T, transport http.RoundTripper, edit func(*ProviderOptions)) *harness {
	t.Helper()
	h := &harness{}
	clock := 0.0
	options := ProviderOptions{
		Preset: PresetVercel,
		Env: map[string]string{
			"AI_GATEWAY_API_KEY": testKey, "OPENAI_API_KEY": testKey, "OPENROUTER_API_KEY": testKey,
			"LITELLM_API_KEY": testKey, "LITELLM_BASE_URL": "https://litellm.example.com/",
		},
		HTTPClient: &http.Client{Transport: transport},
		Sleep: func(_ context.Context, ms float64) {
			h.sleeps = append(h.sleeps, ms)
			clock += ms
		},
		Now: func() float64 { clock += 10; return clock },
	}
	if edit != nil {
		edit(&options)
	}
	instance, err := CreateProvider(options)
	if err != nil {
		t.Fatal(err)
	}
	h.instance = instance
	return h
}

func complete(h *harness, c types.ReplayCall) types.ReplyOutcome {
	return h.instance.Complete(context.Background(), c)
}

func connectionError(errno syscall.Errno) error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: errno}}
}

func dnsError(notFound bool) error {
	return &net.DNSError{Err: "lookup failed", Name: "example.invalid", IsNotFound: notFound, IsTemporary: !notFound}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "timed out" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return false }

func flagsEqual(t *testing.T, label string, got []types.ReplayFlag, want ...types.ReplayFlag) {
	t.Helper()
	if want == nil {
		want = []types.ReplayFlag{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: flags %v, want %v", label, got, want)
	}
}

func errorCode(outcome types.ReplyOutcome) string {
	if outcome.Error == nil {
		return "<nil>"
	}
	return outcome.Error.Code
}

// ---------------------------------------------------------------------------
// Presets and configuration

func TestPresetsNameEndpointKeyAndModelHandling(t *testing.T) {
	if Presets[PresetOpenAI].BaseURL != "https://api.openai.com/v1" ||
		Presets[PresetOpenRouter].APIKeyEnv != "OPENROUTER_API_KEY" ||
		Presets[PresetVercel].BaseURL != "https://ai-gateway.vercel.sh/v1" ||
		Presets[PresetLiteLLM].BaseURLEnv != "LITELLM_BASE_URL" ||
		Presets[PresetOllama].APIKeyEnv != "" {
		t.Error("preset table")
	}
	if !IsFreeEndpoint("ollama") || IsFreeEndpoint("vercel") || IsFreeEndpoint("localhost:11434") {
		t.Error("free endpoints")
	}
}

func TestOpenAIPresetStripsPrefixAndSendsMaxCompletionTokens(t *testing.T) {
	s := newScript(okReply())
	h := provider(t, s, func(o *ProviderOptions) { o.Preset = PresetOpenAI })
	outcome := complete(h, call(nil))
	if outcome.Status != "ok" {
		t.Fatalf("outcome %+v", outcome)
	}
	first := s.sent[0]
	if first.url != "https://api.openai.com/v1/chat/completions" || first.body["model"] != "gpt-6-luna" ||
		first.body["max_completion_tokens"] != 256.0 || first.body["max_tokens"] != nil {
		t.Errorf("sent %+v", first)
	}
	if first.headers.Get("authorization") != "Bearer "+testKey || h.instance.Name() != "openai" {
		t.Error("authorization or name")
	}
}

func TestGatewayPresetsKeepCreatorModel(t *testing.T) {
	for _, preset := range []PresetName{PresetVercel, PresetOpenRouter, PresetLiteLLM} {
		s := newScript(okReply())
		h := provider(t, s, func(o *ProviderOptions) { o.Preset = preset })
		complete(h, call(nil))
		if s.sent[0].body["model"] != "openai/gpt-6-luna" || s.sent[0].body["max_tokens"] != 256.0 {
			t.Errorf("%s: %+v", preset, s.sent[0].body)
		}
	}
}

func TestOpenRouterUsageAndLiteLLMBaseURL(t *testing.T) {
	openrouter := newScript(okReply())
	complete(provider(t, openrouter, func(o *ProviderOptions) { o.Preset = PresetOpenRouter }), call(nil))
	if !reflect.DeepEqual(openrouter.sent[0].body["usage"], map[string]any{"include": true}) ||
		openrouter.sent[0].url != "https://openrouter.ai/api/v1/chat/completions" {
		t.Errorf("openrouter %+v", openrouter.sent[0])
	}
	litellm := newScript(okReply())
	h := provider(t, litellm, func(o *ProviderOptions) { o.Preset = PresetLiteLLM })
	complete(h, call(nil))
	if litellm.sent[0].url != "https://litellm.example.com/chat/completions" || h.instance.BaseURL() != "https://litellm.example.com" {
		t.Errorf("litellm %s %s", litellm.sent[0].url, h.instance.BaseURL())
	}
}

func TestOllamaNeedsNoKey(t *testing.T) {
	s := newScript(okReply())
	instance, err := CreateProvider(ProviderOptions{Preset: PresetOllama, Env: map[string]string{}, HTTPClient: s.client()})
	if err != nil {
		t.Fatal(err)
	}
	instance.Complete(context.Background(), call(func(c *types.ReplayCall) { c.Model = "llama3.2" }))
	if s.sent[0].url != "http://localhost:11434/v1/chat/completions" || s.sent[0].headers.Get("authorization") != "" || !instance.Free() {
		t.Errorf("ollama %+v", s.sent[0])
	}
}

func TestCustomEndpointNamedByHost(t *testing.T) {
	s := newScript(okReply())
	instance, err := CreateProvider(ProviderOptions{
		BaseURL: "https://llm.example.com/v1/", APIKeyEnv: str("EXAMPLE_KEY"), Env: map[string]string{"EXAMPLE_KEY": testKey}, HTTPClient: s.client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	instance.Complete(context.Background(), call(nil))
	if s.sent[0].url != "https://llm.example.com/v1/chat/completions" || instance.Name() != "llm.example.com" || instance.Free() {
		t.Errorf("custom %s %s", s.sent[0].url, instance.Name())
	}
	yes := true
	keyless, err := CreateProvider(ProviderOptions{BaseURL: "http://127.0.0.1:8000/v1", Env: map[string]string{}, HTTPClient: newScript(okReply()).client(), Free: &yes})
	if err != nil || !keyless.Free() {
		t.Errorf("keyless %v", err)
	}
}

func TestMissingKeyOrBaseURLFailsNamingVariableOnly(t *testing.T) {
	_, err := CreateProvider(ProviderOptions{Preset: PresetOpenAI, Env: map[string]string{}})
	var config *ProviderConfigError
	if !errors.As(err, &config) || config.Code != "credential_missing" || config.Message != "OPENAI_API_KEY is not set" {
		t.Errorf("openai: %v", err)
	}
	cases := []struct {
		options ProviderOptions
		want    string
	}{
		{ProviderOptions{Preset: PresetLiteLLM, Env: map[string]string{"LITELLM_API_KEY": testKey}}, "LITELLM_BASE_URL is not set"},
		{ProviderOptions{Env: map[string]string{}}, "needs a base URL"},
		{ProviderOptions{BaseURL: "not a url", Env: map[string]string{}}, "not a valid URL"},
		{ProviderOptions{BaseURL: "https://user:secret@example.com/v1", Env: map[string]string{}}, "environment variable"},
	}
	for _, c := range cases {
		if _, err := CreateProvider(c.options); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v", c.options, err)
		}
	}
}

// ---------------------------------------------------------------------------
// The request

func asJSON(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	_ = json.Unmarshal(data, &decoded)
	return decoded
}

func TestPayloadPutsSystemFirstAndCarriesEverySetField(t *testing.T) {
	body := BuildPayload(call(func(c *types.ReplayCall) {
		c.Temperature = num(0.3)
		c.TopP = num(0.9)
		c.Stop = []string{"END"}
		c.ReasoningEffort = str("high")
		c.ResponseFormat = json.RawMessage(`{"type": "json_object"}`)
		c.ForcedTool = &types.ForcedTool{Name: "record", Description: str("Record it"), Parameters: json.RawMessage(`{"type": "object"}`)}
	}), PayloadOptions{Temperature: true, ResponseFormat: true})
	want := map[string]any{
		"model": "openai/gpt-6-luna",
		"messages": []any{
			map[string]any{"role": "system", "content": "Be brief."},
			map[string]any{"role": "user", "content": "Hi"},
			map[string]any{"role": "assistant", "content": "Hello"},
			map[string]any{"role": "user", "content": "Capital of France?"},
		},
		"max_tokens":       256,
		"temperature":      0.3,
		"top_p":            0.9,
		"stop":             []any{"END"},
		"reasoning_effort": "high",
		"response_format":  map[string]any{"type": "json_object"},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "record", "parameters": map[string]any{"type": "object"}, "strict": false, "description": "Record it",
		}}},
		"tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": "record"}},
	}
	if got, expected := asJSON(t, body), asJSON(t, want); !reflect.DeepEqual(got, expected) {
		t.Errorf("payload\n%v\nwant\n%v", got, expected)
	}
}

func TestPayloadLeavesOutUnsetOrDropped(t *testing.T) {
	body := BuildPayload(call(func(c *types.ReplayCall) {
		c.System = nil
		c.Temperature = num(0.3)
		c.Stop = []string{}
		c.ResponseFormat = json.RawMessage(`{"type": "json_object"}`)
	}), PayloadOptions{Extra: map[string]any{"model": "ignored", "usage": map[string]any{"include": true}}})
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"max_tokens", "messages", "model", "usage"}) || body["model"] != "openai/gpt-6-luna" || len(body["messages"].([]any)) != 3 {
		t.Errorf("payload %v", body)
	}
}

func TestPayloadKeepsSchemaPropertyOrder(t *testing.T) {
	schema := `{"type": "object", "properties": {"zeta": {"type": "string"}, "alpha": {"type": "integer"}, "mid": {"type": "boolean"}}, "required": ["zeta", "alpha"]}`
	format := `{"type": "json_schema", "json_schema": {"name": "answer", "strict": true, "schema": ` + schema + `}}`
	c := call(func(c *types.ReplayCall) {
		c.ResponseFormat = json.RawMessage(format)
		c.ForcedTool = &types.ForcedTool{Name: "record", Parameters: json.RawMessage(schema)}
	})
	data, err := json.Marshal(BuildPayload(c, PayloadOptions{ResponseFormat: true}))
	if err != nil {
		t.Fatal(err)
	}
	compact := func(text string) string {
		var b bytes.Buffer
		if err := json.Compact(&b, []byte(text)); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	body := string(data)
	if !strings.Contains(body, `"response_format":`+compact(format)) {
		t.Errorf("response_format not verbatim: %s", body)
	}
	if !strings.Contains(body, `"parameters":`+compact(schema)) {
		t.Errorf("parameters not verbatim: %s", body)
	}
	// The body on the wire, through the provider, carries the same order.
	s := newScript(okReply())
	h := provider(t, s, nil)
	complete(h, c)
	if !strings.Contains(s.raw[0], `"properties":{"zeta":{"type":"string"},"alpha":{"type":"integer"},"mid":{"type":"boolean"}}`) {
		t.Errorf("wire body lost the order: %s", s.raw[0])
	}
	// Dropping the response format leaves the case's bytes untouched.
	if string(c.ResponseFormat) != format {
		t.Error("the case's response format changed")
	}
}

func TestPayloadOmitsNullResponseFormat(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(``), json.RawMessage(` null `)} {
		body := BuildPayload(call(func(c *types.ReplayCall) { c.ResponseFormat = raw }), PayloadOptions{ResponseFormat: true})
		if _, has := body["response_format"]; has {
			t.Errorf("%q: response_format sent", raw)
		}
	}
}

func TestForcedToolWithoutDescriptionHasNone(t *testing.T) {
	forced := ForcedToolRequest(types.ForcedTool{Name: "f", Parameters: json.RawMessage(`{}`)})
	fn := forced.Tool["function"].(map[string]any)
	if _, has := fn["description"]; has || len(fn) != 3 {
		t.Errorf("function %v", fn)
	}
}

// ---------------------------------------------------------------------------
// The reply

func TestUsageReasoningCacheAndReportedCostNormalized(t *testing.T) {
	body := reply(str("Paris"), nil, "stop")
	body["usage"].(map[string]any)["cost"] = 0.00042
	outcome := complete(provider(t, newScript(ok(body)), nil), call(nil))
	want := types.ReplyOutcome{
		Status:          "ok",
		Output:          str("Paris"),
		FinishReason:    str("stop"),
		Usage:           &types.Usage{InputTokens: num(120), OutputTokens: num(30), CachedInputTokens: num(100), ReasoningTokens: num(12)},
		ReportedCostUSD: num(0.00042),
		LatencyMs:       num(10),
		Flags:           []types.ReplayFlag{},
	}
	if !reflect.DeepEqual(outcome, want) {
		t.Errorf("outcome %s", mustJSON(outcome))
	}
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func TestOddUsageReadsAsNilFields(t *testing.T) {
	h := provider(t, newScript(
		ok(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "x"}, "finish_reason": "stop"}}}),
		ok(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "x"}}}, "usage": map[string]any{"prompt_tokens": -1, "completion_tokens": 2.5, "cost": -1}}),
		ok(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "x"}}}, "usage": map[string]any{"prompt_tokens": 5, "cost": "0.1"}}),
	), nil)
	none := complete(h, call(nil))
	if none.Usage != nil || none.ReportedCostUSD != nil {
		t.Errorf("none %s", mustJSON(none))
	}
	odd := complete(h, call(nil))
	if !reflect.DeepEqual(odd.Usage, &types.Usage{}) || odd.ReportedCostUSD != nil || odd.FinishReason != nil {
		t.Errorf("odd %s", mustJSON(odd))
	}
	if complete(h, call(nil)).ReportedCostUSD != nil {
		t.Error("string cost read")
	}
}

func TestEmptyTextWithToolCallAnswersWithArguments(t *testing.T) {
	toolCall := func(args any) map[string]any {
		return map[string]any{"tool_calls": []any{map[string]any{"type": "function", "function": map[string]any{"name": "record", "arguments": args}}}}
	}
	h := provider(t, newScript(
		ok(reply(nil, toolCall(`{"priority": 2}`), "tool_calls")),
		ok(reply(str("  "), toolCall(map[string]any{"priority": 3}), "tool_calls")),
		ok(reply(str("text wins"), toolCall("{}"), "tool_calls")),
		ok(reply(nil, map[string]any{"tool_calls": []any{map[string]any{"function": map[string]any{"arguments": 7}}}}, "stop")),
		// Object arguments keep the reply's own key order, as JSON.stringify does.
		raw(200, []byte(`{"choices":[{"message":{"content":null,"tool_calls":[{"function":{"arguments":{"b":1,"a":[1.50,"é\n"],"2":true,"1":null}}}]}}]}`)),
	), nil)
	for i, want := range []string{`{"priority": 2}`, `{"priority":3}`, "text wins", "<nil>", `{"1":null,"2":true,"b":1,"a":[1.5,"é\n"]}`} {
		if got := strOrNil(complete(h, call(nil)).Output); got != want {
			t.Errorf("%d: output %s, want %s", i, got, want)
		}
	}
}

func TestRefusals(t *testing.T) {
	h := provider(t, newScript(
		ok(reply(nil, map[string]any{"refusal": "I can't help with that."}, "stop")),
		ok(reply(str(""), nil, "content_filter")),
		ok(reply(str(""), nil, "safety")),
		ok(reply(str(""), nil, "refusal")),
		ok(reply(str("fine"), map[string]any{"refusal": nil}, "stop")),
	), nil)
	for i := 0; i < 4; i++ {
		if !complete(h, call(nil)).Refusal {
			t.Errorf("%d: not a refusal", i)
		}
	}
	if complete(h, call(nil)).Refusal {
		t.Error("fine is a refusal")
	}
}

func TestCutOffAndRefusalFinishReasons(t *testing.T) {
	for _, reason := range []string{"length", "max_tokens", "max_output_tokens", "LENGTH"} {
		if !IsCutOff(&reason) {
			t.Errorf("%s not cut off", reason)
		}
	}
	for _, reason := range []*string{str("stop"), str("tool_calls"), nil} {
		if IsCutOff(reason) {
			t.Errorf("%s cut off", strOrNil(reason))
		}
	}
	for _, reason := range []string{"content_filter", "refusal", "safety"} {
		if !IsRefusalReason(&reason) {
			t.Errorf("%s not a refusal", reason)
		}
	}
	if IsRefusalReason(str("stop")) || IsRefusalReason(nil) {
		t.Error("refusal reasons")
	}
}

func TestOKCarryingErrorAndNoChoiceIsFailure(t *testing.T) {
	outcome := complete(provider(t, newScript(ok(map[string]any{"error": map[string]any{"message": "upstream failed"}})), nil), call(nil))
	if outcome.Status != "failed" || errorCode(outcome) != "provider_error" {
		t.Errorf("outcome %s", mustJSON(outcome))
	}
	flagsEqual(t, "error body", outcome.Flags, types.FlagMaybeBilled)
}

// ---------------------------------------------------------------------------
// Retries

func TestRateLimitRetriedWithBackoff(t *testing.T) {
	s := newScript(status(429, "", nil), status(429, "", nil), okReply())
	h := provider(t, s, nil)
	outcome := complete(h, call(nil))
	if outcome.Status != "ok" || len(s.sent) != 3 || !reflect.DeepEqual(h.sleeps, []float64{2000, 4000}) || *outcome.LatencyMs != 10 {
		t.Errorf("outcome %s sleeps %v", mustJSON(outcome), h.sleeps)
	}
	flagsEqual(t, "429", outcome.Flags, types.FlagRetried)
}

func TestRetryAfterHonoured(t *testing.T) {
	h := provider(t, newScript(status(429, "", map[string]string{"retry-after": "7"}), status(529, "", map[string]string{"retry-after-ms": "1500"}), okReply()), nil)
	if complete(h, call(nil)).Status != "ok" || !reflect.DeepEqual(h.sleeps, []float64{7000, 1500}) {
		t.Errorf("sleeps %v", h.sleeps)
	}
	capped := provider(t, newScript(status(429, "", map[string]string{"retry-after": "3600"}), okReply()), nil)
	complete(capped, call(nil))
	if !reflect.DeepEqual(capped.sleeps, []float64{60_000}) {
		t.Errorf("capped %v", capped.sleeps)
	}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	header := func(value string) http.Header { return http.Header{"Retry-After": {value}} }
	nowMs := float64(now.UnixMilli())
	if got := RetryAfterMs(header(now.Add(5*time.Second).Format(http.TimeFormat)), nowMs); got == nil || *got != 5000 {
		t.Errorf("date ahead %v", got)
	}
	if got := RetryAfterMs(header(now.Add(-5*time.Second).Format(http.TimeFormat)), nowMs); got == nil || *got != 0 {
		t.Errorf("date behind %v", got)
	}
	if RetryAfterMs(header("soon"), nowMs) != nil || RetryAfterMs(http.Header{}, nowMs) != nil {
		t.Error("absent or unreadable")
	}
}

func TestRateLimitedThreeTimesFails(t *testing.T) {
	outcome := complete(provider(t, newScript(status(429, "", nil), status(429, "", nil), status(429, "", nil)), nil), call(nil))
	if outcome.Status != "failed" || errorCode(outcome) != "provider_rate_limited" {
		t.Errorf("outcome %s", mustJSON(outcome))
	}
	flagsEqual(t, "429x3", outcome.Flags, types.FlagRetried)
}

func TestOverloadedRetriedThenFailedWithoutBilling(t *testing.T) {
	outcome := complete(provider(t, newScript(status(529, "", nil), status(529, "", nil), status(529, "", nil)), nil), call(nil))
	if errorCode(outcome) != "provider_overloaded" {
		t.Errorf("outcome %s", mustJSON(outcome))
	}
	flagsEqual(t, "529x3", outcome.Flags, types.FlagRetried)
}

func TestRefusedConnectionOrUnresolvedNameRetried(t *testing.T) {
	recovered := complete(provider(t, newScript(fails(connectionError(syscall.ECONNREFUSED)), fails(dnsError(true)), okReply()), nil), call(nil))
	if recovered.Status != "ok" {
		t.Errorf("recovered %s", mustJSON(recovered))
	}
	flagsEqual(t, "recovered", recovered.Flags, types.FlagRetried)
	down := complete(provider(t, newScript(fails(dnsError(false)), fails(connectionError(syscall.ECONNREFUSED)), fails(connectionError(syscall.ECONNREFUSED))), nil), call(nil))
	if errorCode(down) != "provider_unreachable" {
		t.Errorf("down %s", mustJSON(down))
	}
	flagsEqual(t, "down", down.Flags, types.FlagRetried)
}

func TestNeverSentCoversGroupedErrorsOnlyWhenEveryOneRefused(t *testing.T) {
	grouped := func(errnos ...syscall.Errno) error {
		errs := make([]error, len(errnos))
		for i, errno := range errnos {
			errs[i] = connectionError(errno)
		}
		return &net.OpError{Op: "dial", Err: errors.Join(errs...)}
	}
	if !NeverSent(grouped(syscall.ECONNREFUSED, syscall.ECONNREFUSED)) {
		t.Error("all refused")
	}
	if NeverSent(grouped(syscall.ECONNREFUSED, syscall.ETIMEDOUT)) {
		t.Error("one timed out")
	}
	if NeverSent(connectionError(syscall.ECONNRESET)) || NeverSent(nil) {
		t.Error("reset or nil")
	}
}

func TestResetTimeoutOr5xxFailsAtOnceAndMayHaveBilled(t *testing.T) {
	cases := []struct {
		answer answer
		code   string
	}{
		{fails(connectionError(syscall.ECONNRESET)), "provider_unreachable"},
		{fails(timeoutError{}), "provider_timeout"},
		{status(500, "", nil), "provider_unavailable"},
		{status(503, "", nil), "provider_unavailable"},
	}
	for _, c := range cases {
		s := newScript(c.answer)
		outcome := complete(provider(t, s, nil), call(nil))
		if outcome.Status != "failed" || errorCode(outcome) != c.code || len(s.sent) != 1 {
			t.Errorf("%s: outcome %s", c.code, mustJSON(outcome))
		}
		flagsEqual(t, c.code, outcome.Flags, types.FlagMaybeBilled)
	}
}

// hang blocks until the request's context ends.
type hang struct{ started func() }

func (h hang) RoundTrip(request *http.Request) (*http.Response, error) {
	if h.started != nil {
		h.started()
	}
	<-request.Context().Done()
	return nil, request.Context().Err()
}

func TestRequestTimeoutAbortsHungRequest(t *testing.T) {
	instance, err := CreateProvider(ProviderOptions{Preset: PresetOllama, Env: map[string]string{}, HTTPClient: &http.Client{Transport: hang{}}, Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	outcome := instance.Complete(context.Background(), call(nil))
	if errorCode(outcome) != "provider_timeout" {
		t.Errorf("outcome %s", mustJSON(outcome))
	}
	flagsEqual(t, "timeout", outcome.Flags, types.FlagMaybeBilled)
}

func TestCallersAbortCancelsCall(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	h := provider(t, newScript(), nil)
	if outcome := h.instance.Complete(cancelled, call(nil)); errorCode(outcome) != "aborted" {
		t.Errorf("before: %s", mustJSON(outcome))
	}
	inflight, abort := context.WithCancel(context.Background())
	defer abort()
	transport := hang{started: func() { time.AfterFunc(5*time.Millisecond, abort) }}
	live, err := CreateProvider(ProviderOptions{Preset: PresetOllama, Env: map[string]string{}, HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	outcome := live.Complete(inflight, call(nil))
	if errorCode(outcome) != "aborted" {
		t.Errorf("in flight: %s", mustJSON(outcome))
	}
	flagsEqual(t, "in flight", outcome.Flags, types.FlagMaybeBilled)
}

// cutOffReader yields a partial reply, then fails.
type cutOffReader struct{ done bool }

func (r *cutOffReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, `{"choices":`), nil
	}
	return 0, errors.New("socket closed")
}

func TestUnreadableOversizedOrNonObjectReplyMayHaveBilled(t *testing.T) {
	cases := []struct {
		answer answer
		code   string
	}{
		{raw(200, []byte("not json")), "provider_malformed"},
		{raw(200, []byte("[1,2]")), "provider_malformed"},
		{raw(200, []byte{0xff, 0xfe}), "provider_malformed"},
		{raw(200, make([]byte, MaxResponseBytes+1)), "provider_malformed"},
		{answer{status: 200, reader: &cutOffReader{}}, "provider_unreachable"},
	}
	for i, c := range cases {
		outcome := complete(provider(t, newScript(c.answer), nil), call(nil))
		if errorCode(outcome) != c.code {
			t.Errorf("%d: outcome %s", i, mustJSON(outcome))
		}
		flagsEqual(t, strconv.Itoa(i), outcome.Flags, types.FlagMaybeBilled)
	}
}

func TestOther4xxAreRefusalsNotRetriedNotBilledNotEchoed(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 422} {
		s := newScript(status(code, `{"error":"bad thing with `+testKey+`"}`, nil))
		outcome := complete(provider(t, s, nil), call(nil))
		if errorCode(outcome) != "provider_refused" || outcome.Error.Message != "the endpoint answered HTTP "+strconv.Itoa(code) || len(s.sent) != 1 {
			t.Errorf("%d: outcome %s", code, mustJSON(outcome))
		}
		flagsEqual(t, strconv.Itoa(code), outcome.Flags)
	}
}

// ---------------------------------------------------------------------------
// Fallbacks

func TestTemperatureRefusalResentOnceAndRemembered(t *testing.T) {
	s := newScript(status(400, `{"error":{"message":"Unsupported value: 'temperature' does not support 0.2"}}`, nil), okReply(), okReply())
	h := provider(t, s, nil)
	withTemperature := call(func(c *types.ReplayCall) { c.Temperature = num(0.2) })
	first := complete(h, withTemperature)
	if first.Status != "ok" || s.sent[0].body["temperature"] != 0.2 {
		t.Errorf("first %s", mustJSON(first))
	}
	flagsEqual(t, "first", first.Flags, types.FlagTemperatureDropped)
	if _, has := s.sent[1].body["temperature"]; has {
		t.Error("resent with temperature")
	}
	if !h.instance.TemperatureRefused()["openai/gpt-6-luna"] {
		t.Error("not remembered")
	}
	second := complete(h, withTemperature)
	flagsEqual(t, "second", second.Flags, types.FlagTemperatureDropped)
	if _, has := s.sent[2].body["temperature"]; has || len(s.sent) != 3 {
		t.Error("second sent temperature")
	}
}

func TestTemperatureRefusalWithoutTemperatureIsOrdinaryRefusal(t *testing.T) {
	outcome := complete(provider(t, newScript(status(400, "temperature is weird", nil)), nil), call(nil))
	if errorCode(outcome) != "provider_refused" {
		t.Errorf("outcome %s", mustJSON(outcome))
	}
}

func TestTemperatureRefusedTwiceFailsAsRefused(t *testing.T) {
	outcome := complete(provider(t, newScript(status(400, "temperature", nil), status(400, "temperature", nil)), nil),
		call(func(c *types.ReplayCall) { c.Temperature = num(1) }))
	if errorCode(outcome) != "provider_refused" {
		t.Errorf("outcome %s", mustJSON(outcome))
	}
	flagsEqual(t, "twice", outcome.Flags, types.FlagTemperatureDropped)
}

func TestResponseFormatRefusalDropsItOnce(t *testing.T) {
	for _, detail := range []string{"response_format is not supported", "Invalid json_schema", "This model does not support JSON schema"} {
		s := newScript(status(400, detail, nil), okReply())
		outcome := complete(provider(t, s, nil), call(func(c *types.ReplayCall) { c.ResponseFormat = json.RawMessage(`{"type": "json_schema"}`) }))
		if outcome.Status != "ok" {
			t.Errorf("%s: %s", detail, mustJSON(outcome))
		}
		flagsEqual(t, detail, outcome.Flags, types.FlagResponseFormatDropped)
		_, first := s.sent[0].body["response_format"]
		_, second := s.sent[1].body["response_format"]
		if !first || second {
			t.Errorf("%s: response_format sent %v then %v", detail, first, second)
		}
	}
}

func TestRefusalNotNamingResponseFormatKeepsItAndFails(t *testing.T) {
	s := newScript(status(400, "context length exceeded", nil))
	outcome := complete(provider(t, s, nil), call(func(c *types.ReplayCall) { c.ResponseFormat = json.RawMessage(`{"type": "json_schema"}`) }))
	if errorCode(outcome) != "provider_refused" || len(s.sent) != 1 {
		t.Errorf("outcome %s", mustJSON(outcome))
	}
	auth := complete(provider(t, newScript(status(401, "invalid key for response_format", nil)), nil),
		call(func(c *types.ReplayCall) { c.ResponseFormat = json.RawMessage(`{"type": "json_object"}`) }))
	if errorCode(auth) != "provider_refused" {
		t.Errorf("auth %s", mustJSON(auth))
	}
}

func TestResponseFormatRefusalWithoutOneIsOrdinaryRefusal(t *testing.T) {
	if outcome := complete(provider(t, newScript(status(422, "response_format invalid", nil)), nil), call(nil)); errorCode(outcome) != "provider_refused" {
		t.Errorf("outcome %s", mustJSON(outcome))
	}
}

func TestBothFallbacksCompose(t *testing.T) {
	s := newScript(status(400, "temperature unsupported", nil), status(400, "response_format unsupported", nil), okReply())
	outcome := complete(provider(t, s, nil), call(func(c *types.ReplayCall) {
		c.Temperature = num(0.5)
		c.ResponseFormat = json.RawMessage(`{"type": "json_object"}`)
	}))
	flags := append([]types.ReplayFlag(nil), outcome.Flags...)
	sort.Slice(flags, func(i, j int) bool { return flags[i] < flags[j] })
	if outcome.Status != "ok" || len(s.sent) != 3 {
		t.Errorf("outcome %s", mustJSON(outcome))
	}
	flagsEqual(t, "both", flags, types.FlagResponseFormatDropped, types.FlagTemperatureDropped)
	_, temperature := s.sent[2].body["temperature"]
	_, format := s.sent[2].body["response_format"]
	if temperature || format {
		t.Error("third request still carries a dropped field")
	}
}

// ---------------------------------------------------------------------------
// Keys and concurrency

func TestNoOutcomeOrErrorCarriesTheKey(t *testing.T) {
	answers := []answer{status(400, testKey, nil), status(500, testKey, nil), fails(connectionError(syscall.ECONNRESET)), ok(map[string]any{"error": testKey})}
	for i, a := range answers {
		outcome := complete(provider(t, newScript(a), nil), call(nil))
		if strings.Contains(mustJSON(outcome), testKey) {
			t.Errorf("%d: outcome carries the key", i)
		}
	}
	_, err := CreateProvider(ProviderOptions{Preset: PresetOpenAI, Env: map[string]string{"OPENAI_API_KEY": "   "}})
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("blank key: %v", err)
	}
}

// counting answers every request after a short wait, tracking the peak in flight.
type counting struct{ active, peak atomic.Int32 }

func (c *counting) RoundTrip(request *http.Request) (*http.Response, error) {
	n := c.active.Add(1)
	for {
		peak := c.peak.Load()
		if n <= peak || c.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
	c.active.Add(-1)
	data, _ := json.Marshal(reply(str("Paris"), nil, "stop"))
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(data))), Request: request}, nil
}

func TestProviderRunsAtMostConcurrencyRequests(t *testing.T) {
	run := func(concurrency, calls int) int32 {
		transport := &counting{}
		instance, err := CreateProvider(ProviderOptions{Preset: PresetOllama, Env: map[string]string{}, HTTPClient: &http.Client{Transport: transport}, Concurrency: concurrency})
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		outcomes := make([]types.ReplyOutcome, calls)
		for i := range outcomes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				outcomes[i] = instance.Complete(context.Background(), call(nil))
			}()
		}
		wg.Wait()
		for _, outcome := range outcomes {
			if outcome.Status != "ok" {
				t.Errorf("outcome %s", mustJSON(outcome))
			}
		}
		return transport.peak.Load()
	}
	if peak := run(2, 7); peak != 2 {
		t.Errorf("peak %d, want 2", peak)
	}
	if peak := run(0, 10); peak != 4 {
		t.Errorf("default peak %d, want 4", peak)
	}
}

func TestLimiterReleasesOnFailureAndRefusesBadLimit(t *testing.T) {
	limiter, err := CreateLimiter(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := limiter.Run(func() error { return errors.New("boom") }); err == nil || err.Error() != "boom" {
		t.Errorf("err %v", err)
	}
	result := 0
	_ = limiter.Run(func() error { result = 5; return nil })
	if result != 5 || limiter.Active() != 0 || limiter.Waiting() != 0 {
		t.Errorf("result %d active %d waiting %d", result, limiter.Active(), limiter.Waiting())
	}
	if _, err := CreateLimiter(0); !errors.Is(err, ErrConcurrency) {
		t.Errorf("limit 0: %v", err)
	}
}
