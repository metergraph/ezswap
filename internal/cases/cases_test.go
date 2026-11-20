package cases

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/metergraph/ezswap/internal/cases/jsval"
	"github.com/metergraph/ezswap/internal/types"
)

// j decodes a JSON literal into ordered values, the way a test's object
// literal reads (key order kept).
func j(t *testing.T, text string) any {
	t.Helper()
	value, err := jsval.Parse(text)
	if err != nil {
		t.Fatalf("bad JSON literal %s: %v", text, err)
	}
	return value
}

func f(v float64) *float64 { return &v }
func s(v string) *string   { return &v }

func response(edit func(*types.CapturedResponse)) types.CapturedResponse {
	r := types.CapturedResponse{
		Text:         s("Production answer."),
		FinishReason: s("stop"),
		Usage:        &types.Usage{InputTokens: f(10), OutputTokens: f(5)},
		CostUSD:      f(0.0001),
		LatencyMs:    f(420),
	}
	if edit != nil {
		edit(&r)
	}
	return r
}

func captured(request any, edit ...func(*types.CapturedCall)) types.CapturedCall {
	c := types.CapturedCall{
		ID:       "call-1",
		TraceID:  s("trace-1"),
		TS:       s("2026-01-01T00:00:00.000Z"),
		Source:   "raw",
		Provider: s("openai"),
		Model:    s("gpt-4o-mini"),
		Request:  request,
		Response: response(nil),
		Missing:  []string{},
	}
	for _, e := range edit {
		e(&c)
	}
	return c
}

func prepare(request any, edit ...func(*types.CapturedCall)) types.Case {
	return PrepareCase(captured(request, edit...), PrepareCaseOptions{})
}

func reason(k types.Case) string {
	if k.NotReplayable == nil {
		return ""
	}
	return string(*k.NotReplayable)
}

func str(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func num(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func equal(t *testing.T, got, want any, label ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", strings.Join(label, " "), got, want)
	}
}

func params(k types.Case) map[string]any {
	var stop any
	if k.Params.Stop != nil {
		stop = k.Params.Stop
	}
	return map[string]any{
		"temperature":     num(k.Params.Temperature),
		"maxTokens":       num(k.Params.MaxTokens),
		"topP":            num(k.Params.TopP),
		"stop":            stop,
		"reasoningEffort": str(k.Params.ReasoningEffort),
	}
}

func msgs(pairs ...string) []types.Message {
	out := []types.Message{}
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, types.Message{Role: pairs[i], Content: pairs[i+1]})
	}
	return out
}

func TestOpenAIChatSystemAndDeveloperTurnsFoldSettingsCarry(t *testing.T) {
	kase := prepare(j(t, `{
		"model": "gpt-4o-mini",
		"messages": [
			{"role": "system", "content": "You are terse."},
			{"role": "developer", "content": "Answer in English."},
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "Hi."},
			{"role": "user", "content": [{"type": "text", "text": "Summarize"}, {"type": "text", "text": "this."}]}
		],
		"temperature": 0.2,
		"top_p": 0.9,
		"stop": "END",
		"max_completion_tokens": 900,
		"reasoning_effort": "low"
	}`))
	equal(t, kase.NotReplayable, (*types.NotReplayableReason)(nil))
	equal(t, str(kase.System), "You are terse.\n\nAnswer in English.")
	equal(t, kase.Messages, msgs("user", "Hello", "assistant", "Hi.", "user", "Summarize\nthis."))
	equal(t, params(kase), map[string]any{"temperature": 0.2, "maxTokens": 900.0, "topP": 0.9, "stop": []string{"END"}, "reasoningEffort": "low"})
	equal(t, str(kase.InputPreview), "Summarize this.")
	equal(t, kase.Fidelity, "full")
	equal(t, str(kase.Production.Output), "Production answer.")
	equal(t, str(kase.Production.FinishReason), "stop")
}

func TestRecordedLatencyOfZeroReadsAsNotRecorded(t *testing.T) {
	zero := prepare(j(t, `{"messages": [{"role": "user", "content": "x"}]}`), func(c *types.CapturedCall) {
		c.Response = response(func(r *types.CapturedResponse) { r.LatencyMs = f(0) })
	})
	equal(t, num(zero.Production.LatencyMs), nil)
	timed := prepare(j(t, `{"messages": [{"role": "user", "content": "x"}]}`))
	equal(t, num(timed.Production.LatencyMs), 420.0)
}

func TestConsecutiveSameRoleTurnsMergeEmptyTurnsDrop(t *testing.T) {
	kase := prepare(j(t, `{"messages": [{"role": "user", "content": "one"}, {"role": "assistant", "content": ""}, {"role": "user", "content": "two"}]}`))
	equal(t, kase.Messages, msgs("user", "one\n\ntwo"))
}

func TestOpenAIResponsesInstructionsStringInputTextFormatTranslated(t *testing.T) {
	kase := prepare(j(t, `{
		"model": "gpt-5-mini",
		"instructions": "Extract fields.",
		"input": "Invoice 42 from example.com",
		"max_output_tokens": 64000,
		"reasoning": {"effort": "medium"},
		"text": {"format": {"type": "json_schema", "name": "invoice", "schema": {"type": "object"}, "strict": true}}
	}`))
	equal(t, reason(kase), "")
	equal(t, str(kase.System), "Extract fields.")
	equal(t, kase.Messages, msgs("user", "Invoice 42 from example.com"))
	equal(t, num(kase.Params.MaxTokens), 32000.0, "clamped")
	equal(t, str(kase.Params.ReasoningEffort), "medium")
	equal(t, string(kase.ResponseFormat), `{"type":"json_schema","json_schema":{"name":"invoice","schema":{"type":"object"},"strict":true}}`)
}

func TestResponsesTextFormatWithoutStrictLeavesStrictOutJSONObjectPasses(t *testing.T) {
	schema := prepare(j(t, `{"input": "x", "text": {"format": {"type": "json_schema", "schema": {"type": "object"}}}}`))
	equal(t, string(schema.ResponseFormat), `{"type":"json_schema","json_schema":{"name":"output","schema":{"type":"object"}}}`)
	object := prepare(j(t, `{"input": "x", "text": {"format": {"type": "json_object"}}}`))
	equal(t, string(object.ResponseFormat), `{"type":"json_object"}`)
	chat := prepare(j(t, `{"messages": [{"role": "user", "content": "x"}], "response_format": {"type": "json_object"}}`))
	equal(t, string(chat.ResponseFormat), `{"type":"json_object"}`)
	text := prepare(j(t, `{"messages": [{"role": "user", "content": "x"}], "response_format": {"type": "text"}}`))
	equal(t, text.ResponseFormat, json.RawMessage(nil))
}

func TestResponsesInputItemsWithTypedParts(t *testing.T) {
	kase := prepare(j(t, `{"input": [{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "Classify this."}]}]}`))
	equal(t, kase.Messages, msgs("user", "Classify this."))
}

func TestAnthropicSystemBlocksAndMessages(t *testing.T) {
	kase := prepare(j(t, `{
		"model": "claude-sonnet-4-5",
		"system": [{"type": "text", "text": "Be kind.", "cache_control": {"type": "ephemeral"}}],
		"messages": [{"role": "user", "content": [{"type": "text", "text": "Hi"}]}],
		"max_tokens": 1024,
		"stop_sequences": ["\n\nHuman:"]
	}`))
	equal(t, str(kase.System), "Be kind.")
	equal(t, num(kase.Params.MaxTokens), 1024.0)
	equal(t, kase.Params.Stop, []string{"\n\nHuman:"})
}

func TestGeminiContentsSystemInstructionAndConfig(t *testing.T) {
	kase := prepare(j(t, `{
		"contents": [
			{"role": "user", "parts": [{"text": "Hi"}]},
			{"role": "model", "parts": [{"text": "Hello"}]},
			{"role": "user", "parts": [{"text": "Translate to French."}]}
		],
		"config": {
			"systemInstruction": {"parts": [{"text": "Translator."}]},
			"maxOutputTokens": 256,
			"temperature": 0,
			"topP": 0.5,
			"stopSequences": ["STOP"]
		}
	}`))
	equal(t, reason(kase), "")
	equal(t, str(kase.System), "Translator.")
	roles := []string{}
	for _, m := range kase.Messages {
		roles = append(roles, m.Role)
	}
	equal(t, roles, []string{"user", "assistant", "user"})
	equal(t, params(kase), map[string]any{"temperature": 0.0, "maxTokens": 256.0, "topP": 0.5, "stop": []string{"STOP"}, "reasoningEffort": nil})
}

func TestGenerationConfigAndInferenceConfigSpellingsOfTheOutputCap(t *testing.T) {
	equal(t, num(prepare(j(t, `{"contents": "hi", "generationConfig": {"maxOutputTokens": 77}}`)).Params.MaxTokens), 77.0)
	equal(t, num(prepare(j(t, `{"messages": [{"role": "user", "content": "hi"}], "inferenceConfig": {"maxTokens": 88}}`)).Params.MaxTokens), 88.0)
	equal(t, num(prepare(j(t, `{"messages": [{"role": "user", "content": "hi"}], "max_tokens": 0}`)).Params.MaxTokens), nil)
	equal(t, num(prepare(j(t, `{"messages": [{"role": "user", "content": "hi"}], "max_tokens": 1.5}`)).Params.MaxTokens), nil)
	equal(t, num(prepare(j(t, `{"messages": [{"role": "user", "content": "hi"}], "max_tokens": true}`)).Params.MaxTokens), nil)
}

func TestOpenTelemetryMessagesWithTypedPartsAndSystemInstructions(t *testing.T) {
	kase := prepare(j(t, `{
		"system": [{"type": "text", "content": "System from OTel."}],
		"messages": [{"role": "user", "parts": [{"type": "text", "content": "Question?"}]}],
		"params": {"temperature": 0.7, "max_tokens": 300, "top_p": 1, "stop": ["###"]}
	}`))
	equal(t, reason(kase), "")
	equal(t, str(kase.System), "System from OTel.")
	equal(t, kase.Messages, msgs("user", "Question?"))
	equal(t, params(kase), map[string]any{"temperature": 0.7, "maxTokens": 300.0, "topP": 1.0, "stop": []string{"###"}, "reasoningEffort": nil})
}

func TestBarePromptStringAsTextOrAsJSONString(t *testing.T) {
	equal(t, prepare("Just a prompt").Messages, msgs("user", "Just a prompt"))
	equal(t, prepare(`{"messages":[{"role":"user","content":"encoded"}]}`).Messages, msgs("user", "encoded"))
}

// ---------------------------------------------------------------------------
// Not replayable

func TestContentUnavailableWhenThereIsNoRequest(t *testing.T) {
	kase := prepare(nil)
	equal(t, reason(kase), "content_unavailable")
	equal(t, str(kase.Production.Output), "Production answer.", "production output is still read")
}

func TestContentTruncatedWhenTheSourceSaysTheContentWasCut(t *testing.T) {
	kase := prepare(j(t, `{"messages": [{"role": "user", "content": "x"}]}`), func(c *types.CapturedCall) { c.Truncated = true })
	equal(t, reason(kase), "content_truncated")
}

func TestToolHistoryForEverySpellingOfAToolTurn(t *testing.T) {
	histories := []string{
		`{"messages": [{"role": "user", "content": "x"}, {"role": "assistant", "tool_calls": [{"id": "1"}]}, {"role": "tool", "content": "r"}, {"role": "user", "content": "y"}]}`,
		`{"messages": [{"role": "function", "content": "r"}, {"role": "user", "content": "y"}]}`,
		`{"messages": [{"role": "assistant", "function_call": {"name": "f"}}, {"role": "user", "content": "y"}]}`,
		`{"messages": [{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "1", "content": "r"}]}]}`,
		`{"messages": [{"role": "assistant", "content": [{"type": "tool_use", "id": "1", "name": "f", "input": {}}]}, {"role": "user", "content": "y"}]}`,
		`{"input": [{"type": "function_call", "name": "f", "arguments": "{}"}, {"type": "function_call_output", "output": "r"}]}`,
		`{"contents": [{"role": "model", "parts": [{"functionCall": {"name": "f", "args": {}}}]}, {"role": "user", "parts": [{"text": "y"}]}]}`,
		`{"contents": [{"role": "user", "parts": [{"functionResponse": {"name": "f", "response": {}}}]}]}`,
		`{"messages": [{"role": "assistant", "parts": [{"type": "tool_call", "id": "1", "name": "f", "arguments": {}}]}, {"role": "user", "content": "y"}]}`,
		`{"messages": [{"role": "tool", "parts": [{"type": "tool_call_response", "id": "1", "response": "r"}]}, {"role": "user", "content": "y"}]}`,
	}
	for _, request := range histories {
		equal(t, reason(prepare(j(t, request))), "tool_history", request)
	}
}

func TestUnsupportedContentForNonTextParts(t *testing.T) {
	requests := []string{
		`{"messages": [{"role": "user", "content": [{"type": "image_url", "image_url": {"url": "https://example.com/a.png"}}]}]}`,
		`{"messages": [{"role": "user", "content": [{"type": "image", "source": {"type": "base64", "data": "AAAA"}}]}]}`,
		`{"input": [{"role": "user", "content": [{"type": "input_image", "image_url": "https://example.com/a.png"}]}]}`,
		`{"contents": [{"role": "user", "parts": [{"inlineData": {"mimeType": "image/png", "data": "AAAA"}}]}]}`,
		`{"messages": [{"role": "user", "parts": [{"type": "blob", "modality": "image", "content": "AAAA"}]}]}`,
	}
	for _, request := range requests {
		equal(t, reason(prepare(j(t, request))), "unsupported_content", request)
	}
}

func TestNoMessagesWhenNothingButASystemPromptOrBlanks(t *testing.T) {
	equal(t, reason(prepare(j(t, `{"messages": [{"role": "system", "content": "S"}]}`))), "no_messages")
	equal(t, reason(prepare(j(t, `{"messages": []}`))), "no_messages")
	equal(t, reason(prepare("   ")), "no_messages")
	equal(t, reason(prepare(j(t, `{"model": "m"}`))), "no_messages")
}

func TestLastTurnNotUserWhenTheConversationEndsWithTheAssistant(t *testing.T) {
	kase := prepare(j(t, `{"messages": [{"role": "user", "content": "x"}, {"role": "assistant", "content": "prefill"}]}`))
	equal(t, reason(kase), "last_turn_not_user")
}

func TestInputTooLargeOver64KiBOfSystemPlusMessages(t *testing.T) {
	half := strings.Repeat("a", 33*1024)
	request := map[string]any{"system": half, "messages": []any{map[string]any{"role": "user", "content": half}}}
	equal(t, reason(prepare(request)), "input_too_large")
	request = map[string]any{"messages": []any{map[string]any{"role": "user", "content": strings.Repeat("a", 60*1024)}}}
	equal(t, reason(prepare(request)), "")
}

func TestUnparseableRequestForShapesTheParserCannotRead(t *testing.T) {
	requests := []string{
		`42`,
		`{"messages": "not a list but messages key"}`,
		`{"messages": [42]}`,
		`{"messages": [{"role": "critic", "content": "x"}]}`,
		`{"messages": [{"role": "user", "content": {"nested": true}}]}`,
		`{"contents": [{"role": "user", "parts": "x"}]}`,
	}
	for _, request := range requests {
		equal(t, reason(prepare(j(t, request))), "unparseable_request", request)
	}
}

// ---------------------------------------------------------------------------
// Tools

const weather = `{"type": "function", "function": {"name": "get_weather", "description": "Weather", "parameters": {"type": "object", "properties": {}}}}`

func withUser(t *testing.T, content string, extra string) any {
	request := j(t, extra).(*jsval.Object)
	request.Set("messages", []any{map[string]any{"role": "user", "content": content}})
	return request
}

func TestForcedToolTheFourSpellings(t *testing.T) {
	schema := `{"type": "object", "properties": {"city": {"type": "string"}}}`
	forms := []string{
		`{"tools": [` + weather + `], "tool_choice": "required"}`,
		`{"tools": [` + weather + `], "tool_choice": {"type": "function", "function": {"name": "get_weather"}}}`,
		`{"tools": [{"type": "function", "name": "get_weather", "parameters": ` + schema + `}], "tool_choice": {"type": "function", "name": "get_weather"}}`,
		`{"tools": [{"name": "get_weather", "input_schema": ` + schema + `}], "tool_choice": {"type": "tool", "name": "get_weather"}}`,
		`{"tools": [{"name": "get_weather", "input_schema": ` + schema + `}], "tool_choice": {"type": "any"}}`,
	}
	for _, form := range forms {
		kase := prepare(withUser(t, "Weather in Paris?", form))
		equal(t, reason(kase), "", form)
		if kase.ForcedTool == nil || kase.ForcedTool.Name != "get_weather" {
			t.Errorf("%s: forced tool %#v", form, kase.ForcedTool)
		}
		if slices.Contains(kase.Missing, ToolsNotOffered) {
			t.Errorf("%s: tools_not_offered in missing", form)
		}
	}
}

func TestGeminiForcedFunctionOneDeclarationWithModeANY(t *testing.T) {
	kase := prepare(j(t, `{
		"contents": [{"role": "user", "parts": [{"text": "Extract."}]}],
		"tools": [{"functionDeclarations": [{"name": "extract", "parameters": {"type": "object"}}]}],
		"toolConfig": {"functionCallingConfig": {"mode": "ANY"}}
	}`))
	equal(t, reason(kase), "")
	equal(t, kase.ForcedTool, &types.ForcedTool{Name: "extract", Parameters: json.RawMessage(`{"type":"object"}`)})
}

func TestToolDefinitionsNotOfferedWhenAForcedChoiceSitsBesideOtherTools(t *testing.T) {
	other := `{"type": "function", "function": {"name": "lookup", "parameters": {"type": "object"}}}`
	requests := []string{
		`{"tools": [` + weather + `, ` + other + `], "tool_choice": "required"}`,
		`{"tools": [` + weather + `, ` + other + `], "tool_choice": {"type": "function", "function": {"name": "get_weather"}}}`,
		`{"tools": [` + weather + `], "tool_choice": {"type": "function", "function": {"name": "lookup"}}}`,
		`{"tools": [{"name": "a", "input_schema": {}}, {"name": "b", "input_schema": {}}], "tool_choice": {"type": "any"}}`,
	}
	for _, request := range requests {
		kase := prepare(withUser(t, "x", request))
		equal(t, reason(kase), "tool_definitions_not_offered", request)
		equal(t, kase.ForcedTool, (*types.ForcedTool)(nil), request)
	}
}

func TestToolsTheModelWasFreeToSkipReplayableMarkedInMissing(t *testing.T) {
	kase := prepare(withUser(t, "x", `{"tools": [`+weather+`], "tool_choice": "auto"}`))
	equal(t, reason(kase), "")
	equal(t, kase.ForcedTool, (*types.ForcedTool)(nil))
	equal(t, kase.Fidelity, "partial")
	if !slices.Contains(kase.Missing, ToolsNotOffered) {
		t.Errorf("missing %v lacks tools_not_offered", kase.Missing)
	}
}

func TestProductionOutputFromASingleToolCallsArguments(t *testing.T) {
	kase := prepare(withUser(t, "x", `{"tools": [`+weather+`], "tool_choice": "required"}`), func(c *types.CapturedCall) {
		c.Response = response(func(r *types.CapturedResponse) {
			r.Text = nil
			r.ToolCalls = j(t, `[{"id": "c1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"Paris\"}"}}]`).([]any)
		})
	})
	equal(t, str(kase.Production.Output), `{"city":"Paris"}`)
	equal(t, str(SingleToolCallOutput(j(t, `[{"type": "tool_use", "name": "f", "input": {"a": 1}}]`))), `{"a":1}`)
	equal(t, str(SingleToolCallOutput(j(t, `[{"name": "f", "args": {"b": 2}}]`))), `{"b":2}`)
	equal(t, str(SingleToolCallOutput(j(t, `[{"arguments": "`+"```json\\n{\\\"c\\\":3}\\n```"+`"}]`))), "```json\n{\"c\":3}\n```")
	equal(t, str(SingleToolCallOutput(j(t, `[{"arguments": "[1,2]"}]`))), nil, "not an object")
	equal(t, str(SingleToolCallOutput(j(t, `[{"arguments": "{}"}, {"arguments": "{}"}]`))), nil, "two calls")
}

// ---------------------------------------------------------------------------
// Source key order

func TestResponseFormatKeepsSourceKeyOrder(t *testing.T) {
	chat := prepare(j(t, `{"messages": [{"role": "user", "content": "x"}], "response_format": {"type": "json_schema", "json_schema": {"strict": true, "schema": {"type": "object", "properties": {"z": {"type": "string"}, "a": {"type": "number"}}, "required": ["z", "a"]}, "name": "out"}}}`))
	equal(t, string(chat.ResponseFormat), `{"type":"json_schema","json_schema":{"strict":true,"schema":{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"number"}},"required":["z","a"]},"name":"out"}}`)
	// Translated from text.format: the chat shape's own keys in a fixed order, the schema as captured.
	responses := prepare(j(t, `{"input": "x", "text": {"format": {"strict": false, "schema": {"properties": {"z": {}, "a": {}}, "type": "object"}, "description": "d", "type": "json_schema", "name": "n"}}}`))
	equal(t, string(responses.ResponseFormat), `{"type":"json_schema","json_schema":{"name":"n","schema":{"properties":{"z":{},"a":{}},"type":"object"},"description":"d","strict":false}}`)
}

func TestForcedToolParametersKeepSourceKeyOrder(t *testing.T) {
	kase := prepare(withUser(t, "x", `{"tools": [{"name": "f", "input_schema": {"type": "object", "properties": {"z": {"type": "string"}, "a": {"type": "integer"}}, "required": ["z"]}}], "tool_choice": {"type": "any"}}`))
	if kase.ForcedTool == nil {
		t.Fatal("no forced tool")
	}
	equal(t, string(kase.ForcedTool.Parameters), `{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"integer"}},"required":["z"]}`)
}

func TestProductionOutputKeepsSourceKeyOrderForObjectArguments(t *testing.T) {
	kase := prepare(withUser(t, "x", `{"tools": [`+weather+`], "tool_choice": "required"}`), func(c *types.CapturedCall) {
		c.Response = response(func(r *types.CapturedResponse) {
			r.Text = nil
			r.ToolCalls = j(t, `[{"type": "tool_use", "name": "f", "input": {"z": 1, "a": {"y": 2, "b": 3}, "2": 0, "1": 9}}]`).([]any)
		})
	})
	// JSON.stringify order: array-index keys ascending, then insertion order.
	equal(t, str(kase.Production.Output), `{"1":9,"2":0,"z":1,"a":{"y":2,"b":3}}`)
}

func TestSingleToolCallOutputKeepsKeyOrderFromAJSONString(t *testing.T) {
	// Tool calls captured as a JSON string keep their source key order.
	equal(t, str(SingleToolCallOutput(`[{"input": {"z": 1, "a": 2, "1": 3}}]`)), `{"1":3,"z":1,"a":2}`)
}

// ---------------------------------------------------------------------------
// Web search

func TestWebSearchFlaggedAndNotReplayedUnlessAllowed(t *testing.T) {
	searches := []string{
		`{"messages": [{"role": "user", "content": "news?"}], "web_search_options": {"search_context_size": "low"}}`,
		`{"input": "news?", "tools": [{"type": "web_search_preview"}]}`,
		`{"messages": [{"role": "user", "content": "news?"}], "tools": [{"type": "web_search_20250305", "name": "web_search"}]}`,
		`{"contents": "news?", "tools": [{"googleSearch": {}}]}`,
		`{"contents": "news?", "config": {"tools": [{"google_search": {}}]}}`,
	}
	for _, request := range searches {
		blocked := prepare(j(t, request))
		equal(t, blocked.WebSearch, true, request)
		equal(t, reason(blocked), "web_search_not_replayed", request)
		allowed := PrepareCase(captured(j(t, request)), PrepareCaseOptions{AllowNoSearch: true})
		equal(t, allowed.WebSearch, true, request)
		equal(t, reason(allowed), "", request)
		if slices.Contains(allowed.Missing, ToolsNotOffered) {
			t.Errorf("%s: a search tool is not an offered tool", request)
		}
	}
	equal(t, prepare(j(t, `{"messages": [{"role": "user", "content": "x"}]}`)).WebSearch, false)
}

func TestMissingFieldsFromTheAdapterGradeTheCasePartial(t *testing.T) {
	kase := prepare(j(t, `{"messages": [{"role": "user", "content": "x"}]}`), func(c *types.CapturedCall) { c.Missing = []string{"tools"} })
	equal(t, kase.Fidelity, "partial")
	equal(t, kase.Missing, []string{"tools"})
}

func TestPreviewCollapsesWhitespaceAndCapsAt280Characters(t *testing.T) {
	kase := prepare(map[string]any{"messages": []any{map[string]any{"role": "user", "content": "  a\n\n b  " + strings.Repeat("c", 400)}}})
	if kase.InputPreview == nil || !strings.HasPrefix(*kase.InputPreview, "a b c") {
		t.Errorf("preview %v", str(kase.InputPreview))
	}
	equal(t, jsval.UTF16Len(*kase.InputPreview), 280)
}
