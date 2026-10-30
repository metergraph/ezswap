// Package providers is an OpenAI-compatible chat-completions client for replays.
package providers

// One request per replay. The client retries only what cannot have been
// billed (transport.go), resends once without a temperature the model refuses
// (and remembers that model), resends once without a response_format the
// endpoint refuses by name, and reads the reply into the replay's terms. Keys
// are read from the environment once and never appear in an error or output.

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/metergraph/replay/internal/types"
)

const DefaultConcurrency = 4

type PresetName string

const (
	PresetOpenAI     PresetName = "openai"
	PresetOpenRouter PresetName = "openrouter"
	PresetVercel     PresetName = "vercel"
	PresetLiteLLM    PresetName = "litellm"
	PresetOllama     PresetName = "ollama"
)

// PresetNames is the presets in the order the TypeScript lists them.
var PresetNames = []PresetName{PresetOpenAI, PresetOpenRouter, PresetVercel, PresetLiteLLM, PresetOllama}

// Preset is a known endpoint. An empty string stands for the TypeScript's null.
type Preset struct {
	// Base URL, or the environment variable that holds it.
	BaseURL    string
	BaseURLEnv string
	// Environment variable holding the key; empty for an endpoint without one.
	APIKeyEnv string
	// A model prefix this endpoint does not take, e.g. "openai/".
	StripPrefix    string
	MaxTokensField string // "max_tokens" | "max_completion_tokens"
	// Request fields the preset adds.
	Extra map[string]any
	// Runs on the user's machine and costs nothing.
	Free bool
}

var Presets = map[PresetName]Preset{
	PresetOpenAI: {
		BaseURL:        "https://api.openai.com/v1",
		APIKeyEnv:      "OPENAI_API_KEY",
		StripPrefix:    "openai/",
		MaxTokensField: "max_completion_tokens",
		Extra:          map[string]any{},
	},
	PresetOpenRouter: {
		BaseURL:        "https://openrouter.ai/api/v1",
		APIKeyEnv:      "OPENROUTER_API_KEY",
		MaxTokensField: "max_tokens",
		// Ask OpenRouter to report what it charged in usage.cost.
		Extra: map[string]any{"usage": map[string]any{"include": true}},
	},
	PresetVercel: {
		BaseURL:        "https://ai-gateway.vercel.sh/v1",
		APIKeyEnv:      "AI_GATEWAY_API_KEY",
		MaxTokensField: "max_tokens",
		Extra:          map[string]any{},
	},
	PresetLiteLLM: {
		BaseURLEnv:     "LITELLM_BASE_URL",
		APIKeyEnv:      "LITELLM_API_KEY",
		MaxTokensField: "max_tokens",
		Extra:          map[string]any{},
	},
	PresetOllama: {
		BaseURL:        "http://localhost:11434/v1",
		MaxTokensField: "max_tokens",
		Extra:          map[string]any{},
		Free:           true,
	},
}

func IsPreset(name string) bool {
	_, ok := Presets[PresetName(name)]
	return ok
}

// IsFreeEndpoint says a treatment's endpoint runs locally for free (the Ollama preset).
func IsFreeEndpoint(endpoint string) bool {
	return IsPreset(endpoint) && Presets[PresetName(endpoint)].Free
}

type ProviderOptions struct {
	// Empty for a custom endpoint.
	Preset PresetName
	// A custom endpoint's base URL, or an override of the preset's. Empty: none.
	BaseURL string
	// Environment variable holding the key. Overrides the preset's when not
	// nil; a pointer to "" means none.
	APIKeyEnv *string
	// Default: a client with no proxy and no client-wide timeout.
	HTTPClient *http.Client
	// Concurrency 0 means DefaultConcurrency.
	Concurrency int
	// Where keys and base URLs are read. Nil: the process environment.
	Env map[string]string
	// Timeout of one request. 0 means RequestTimeoutMs.
	Timeout time.Duration
	// Treat a custom endpoint as free (a local vLLM server, say). Nil: the preset's.
	Free *bool
	// For tests: waits between retries, and the clock for latency (milliseconds).
	Sleep func(ctx context.Context, ms float64)
	Now   func() float64
}

// OpenAICompatibleProvider is a types.Provider over a chat-completions endpoint.
type OpenAICompatibleProvider struct {
	name    string
	baseURL string
	free    bool
	url     string
	headers map[string]string
	preset  *Preset
	client  *http.Client
	limiter *Limiter
	sleep   func(ctx context.Context, ms float64)
	now     func() float64
	timeout time.Duration
	mu      sync.Mutex
	refused map[string]bool
}

var _ types.Provider = (*OpenAICompatibleProvider)(nil)

func (p *OpenAICompatibleProvider) Name() string { return p.name }

// BaseURL is the base URL, without any credential.
func (p *OpenAICompatibleProvider) BaseURL() string { return p.baseURL }

func (p *OpenAICompatibleProvider) Free() bool { return p.free }

// TemperatureRefused is the models that refused an explicit temperature, for
// this provider's lifetime. The map is a copy.
func (p *OpenAICompatibleProvider) TemperatureRefused() map[string]bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]bool, len(p.refused))
	for model := range p.refused {
		out[model] = true
	}
	return out
}

func (p *OpenAICompatibleProvider) hasRefused(model string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.refused[model]
}

func (p *OpenAICompatibleProvider) refuse(model string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refused[model] = true
}

// ProviderConfigError says the endpoint is misconfigured: a missing key or
// base URL. Says which variable, never its value.
type ProviderConfigError struct {
	Code    string
	Message string
}

func (e *ProviderConfigError) Error() string { return e.Message }

func configError(code, message string) error {
	return &ProviderConfigError{Code: code, Message: message}
}

// parseURL stands in for new URL(): an absolute URL with a scheme, and a
// host for the special schemes.
func parseURL(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return nil, false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "ws", "wss", "ftp":
		if parsed.Host == "" {
			return nil, false
		}
	}
	return parsed, true
}

// urlHost is URL.host: the host lower-cased, with the scheme's default port dropped.
func urlHost(parsed *url.URL) string {
	host := strings.ToLower(parsed.Host)
	port := parsed.Port()
	defaults := map[string]string{"http": "80", "https": "443", "ws": "80", "wss": "443", "ftp": "21"}
	if port != "" && defaults[strings.ToLower(parsed.Scheme)] == port {
		host = strings.TrimSuffix(host, ":"+port)
	}
	return host
}

func endpointName(preset PresetName, baseURL string) string {
	if preset != "" {
		return string(preset)
	}
	parsed, ok := parseURL(baseURL)
	if !ok {
		return "custom"
	}
	return urlHost(parsed)
}

var trailingSlashes = regexp.MustCompile(`/+$`)

func CreateProvider(options ProviderOptions) (*OpenAICompatibleProvider, error) {
	lookup := func(name string) (string, bool) {
		if options.Env != nil {
			value, ok := options.Env[name]
			return value, ok
		}
		return os.LookupEnv(name)
	}
	var preset *Preset
	if options.Preset != "" {
		found, ok := Presets[options.Preset]
		if !ok {
			return nil, configError("unknown_preset", "unknown endpoint preset")
		}
		preset = &found
	}

	baseURLEnv := ""
	if preset != nil {
		baseURLEnv = preset.BaseURLEnv
	}
	baseURL := options.BaseURL
	if baseURL == "" && preset != nil {
		baseURL = preset.BaseURL
	}
	if baseURL == "" && baseURLEnv != "" {
		if value, ok := lookup(baseURLEnv); ok {
			baseURL = jsTrim(value)
		}
	}
	baseURL = trailingSlashes.ReplaceAllString(baseURL, "")
	if baseURL == "" {
		if baseURLEnv != "" {
			return nil, configError("base_url_missing", baseURLEnv+" is not set")
		}
		return nil, configError("base_url_missing", "a custom endpoint needs a base URL")
	}
	parsed, ok := parseURL(baseURL)
	if !ok {
		return nil, configError("base_url_invalid", "the endpoint base URL is not a valid URL")
	}
	if parsed.User != nil {
		password, _ := parsed.User.Password()
		if parsed.User.Username() != "" || password != "" {
			return nil, configError("base_url_invalid", "put the key in an environment variable, not in the base URL")
		}
	}

	apiKeyEnv := ""
	if options.APIKeyEnv != nil {
		apiKeyEnv = *options.APIKeyEnv
	} else if preset != nil {
		apiKeyEnv = preset.APIKeyEnv
	}
	key := ""
	if apiKeyEnv != "" {
		if value, ok := lookup(apiKeyEnv); ok {
			key = jsTrim(value)
		}
		if key == "" {
			return nil, configError("credential_missing", apiKeyEnv+" is not set")
		}
	}

	concurrency := options.Concurrency
	if concurrency == 0 {
		concurrency = DefaultConcurrency
	}
	limiter, err := CreateLimiter(concurrency)
	if err != nil {
		return nil, err
	}
	client := options.HTTPClient
	if client == nil {
		client = DefaultClient()
	}
	sleep := options.Sleep
	if sleep == nil {
		sleep = func(ctx context.Context, ms float64) {
			timer := time.NewTimer(time.Duration(ms * float64(time.Millisecond)))
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
			}
		}
	}
	now := options.Now
	if now == nil {
		start := time.Now()
		now = func() float64 { return float64(time.Since(start)) / float64(time.Millisecond) }
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = RequestTimeoutMs * time.Millisecond
	}
	headers := map[string]string{}
	if key != "" {
		headers["authorization"] = "Bearer " + key
	}
	free := false
	if options.Free != nil {
		free = *options.Free
	} else if preset != nil {
		free = preset.Free
	}

	return &OpenAICompatibleProvider{
		name:    endpointName(options.Preset, baseURL),
		baseURL: baseURL,
		free:    free,
		url:     baseURL + "/chat/completions",
		headers: headers,
		preset:  preset,
		client:  client,
		limiter: limiter,
		sleep:   sleep,
		now:     now,
		timeout: timeout,
		refused: map[string]bool{},
	}, nil
}

func (p *OpenAICompatibleProvider) endpointModel(model string) string {
	if p.preset == nil || p.preset.StripPrefix == "" {
		return model
	}
	prefix := p.preset.StripPrefix
	if len(model) >= len(prefix) && strings.ToLower(model[:len(prefix)]) == prefix {
		return model[len(prefix):]
	}
	return model
}

// flagSet is a Set of flags: each once, in the order first added.
type flagSet struct{ flags []types.ReplayFlag }

func (s *flagSet) add(flag types.ReplayFlag) {
	for _, f := range s.flags {
		if f == flag {
			return
		}
	}
	s.flags = append(s.flags, flag)
}

func (s *flagSet) list() []types.ReplayFlag {
	return append(make([]types.ReplayFlag, 0, len(s.flags)), s.flags...)
}

// Complete sends one replay, at most Concurrency at once. The context
// cancels it: before it is sent, or while it is in flight.
func (p *OpenAICompatibleProvider) Complete(ctx context.Context, call types.ReplayCall) types.ReplyOutcome {
	var outcome types.ReplyOutcome
	_ = p.limiter.Run(func() error {
		outcome = p.complete(ctx, call)
		return nil
	})
	return outcome
}

func (p *OpenAICompatibleProvider) complete(ctx context.Context, call types.ReplayCall) types.ReplyOutcome {
	flags := &flagSet{}
	maxTokensField := "max_tokens"
	extra := map[string]any{}
	if p.preset != nil {
		maxTokensField = p.preset.MaxTokensField
		extra = p.preset.Extra
	}
	send := func(withTemperature, withResponseFormat bool) (PostResult, error) {
		body := BuildPayload(call, PayloadOptions{
			Temperature:    withTemperature,
			ResponseFormat: withResponseFormat,
			Model:          p.endpointModel(call.Model),
			MaxTokensField: maxTokensField,
			Extra:          extra,
		})
		return Post(ctx, p.url, body, PostOptions{
			Client:  p.client,
			Headers: p.headers,
			Timeout: p.timeout,
			Sleep:   p.sleep,
			Now:     p.now,
			OnRetry: func() { flags.add(types.FlagRetried) },
		})
	}

	// A temperature the model refused is dropped once and remembered.
	sendWithFallback := func(withResponseFormat bool) (PostResult, error) {
		if call.Temperature == nil {
			return send(false, withResponseFormat)
		}
		if p.hasRefused(call.Model) {
			flags.add(types.FlagTemperatureDropped)
			return send(false, withResponseFormat)
		}
		result, err := send(true, withResponseFormat)
		var failed *CallFailed
		if err == nil || !errors.As(err, &failed) || failed.Code != CodeTemperatureUnsupported {
			return result, err
		}
		p.refuse(call.Model)
		flags.add(types.FlagTemperatureDropped)
		return send(false, withResponseFormat)
	}

	result, err := sendWithFallback(true)
	if err != nil {
		// A refused request was never billed. When the refusal names the
		// response format the call carried, try once without it.
		var failed *CallFailed
		if !errors.As(err, &failed) || failed.Code != CodeResponseFormatUnsupported || call.ResponseFormat == nil {
			return failure(err, flags)
		}
		flags.add(types.FlagResponseFormatDropped)
		result, err = sendWithFallback(false)
		if err != nil {
			return failure(err, flags)
		}
	}
	body := result.Body
	choices, isList := body["choices"].([]any)
	if _, hasError := body["error"]; (!isList || len(choices) == 0) && hasError {
		// Some gateways answer 200 with an error object and no choice.
		return failure(callFailed(CodeProviderError, "the endpoint returned an error instead of a reply", true, 0), flags)
	}
	reply := parseReply(body, result.Raw)
	latency := result.LatencyMs
	return types.ReplyOutcome{
		Status:          "ok",
		Output:          reply.Output,
		FinishReason:    reply.FinishReason,
		Refusal:         reply.Refusal,
		Usage:           reply.Usage,
		ReportedCostUSD: reply.ReportedCostUSD,
		LatencyMs:       &latency,
		Flags:           flags.list(),
		Error:           nil,
	}
}

func failure(err error, flags *flagSet) types.ReplyOutcome {
	code := string(CodeProviderError)
	message := "the call failed"
	var failed *CallFailed
	if errors.As(err, &failed) {
		code = string(failed.Code)
		message = failed.Message
		// A refusal named a setting this call did not carry: an ordinary refusal.
		if failed.Code == CodeTemperatureUnsupported || failed.Code == CodeResponseFormatUnsupported {
			code = string(CodeProviderRefused)
		}
		if failed.MaybeBilled {
			flags.add(types.FlagMaybeBilled)
		}
	} else {
		flags.add(types.FlagMaybeBilled)
	}
	return types.ReplyOutcome{
		Status: "failed",
		Flags:  flags.list(),
		Error:  &types.ReplayError{Code: code, Message: message},
	}
}
