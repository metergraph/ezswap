// Raw provider request and response pairs (OpenAI chat or Responses,
// Anthropic Messages, Gemini), and two logs that reduce to them with a field
// map: Helicone request exports and LiteLLM StandardLoggingPayload callback
// logs.

package adapters

import (
	"github.com/metergraph/replay/internal/cases/jsval"
	"github.com/metergraph/replay/internal/types"
)

// Helicone's stand-in body when an export ran without `includeBody`.
func heliconePlaceholder(body any) bool {
	return jsval.Nullish(body) || body == "" || (IsObject(body) && has(body, "helicone_message"))
}

// LiteLLM writes this string in place of redacted content.
const litellmRedacted = "redacted-by-litellm"

func isHelicone(record any) bool {
	return has(record, "request_body") || (has(record, "request_id") && has(record, "response_status")) || has(record, "signed_body_url")
}

func isLitellm(record any) bool {
	return has(record, "model_parameters") &&
		(has(record, "call_type") || has(record, "custom_llm_provider") || has(record, "response_cost"))
}

func detectRaw(record any) bool {
	if !IsObject(record) {
		return false
	}
	return isHelicone(record) || isLitellm(record) || (has(record, "request") && has(record, "response"))
}

func helicone(record any, context RecordContext) types.CapturedCall {
	missing := []string{}
	requestBody := ParseJSON(get(record, "request_body"))
	responseBody := ParseJSON(get(record, "response_body"))
	var request any
	if !heliconePlaceholder(requestBody) {
		request = requestBody
	}
	if request == nil {
		AddMissing(&missing, "request_body")
	}
	responseMissing := heliconePlaceholder(responseBody)
	if responseMissing {
		AddMissing(&missing, "response_body")
	}
	var body any
	if !responseMissing {
		body = responseBody
	}
	parsed := ParseResponseBody(body)
	var properties any = map[string]any{}
	if p := get(record, "request_properties"); IsObject(p) {
		properties = p
	} else if p := get(record, "properties"); IsObject(p) {
		properties = p
	}
	var requestModel any
	if IsObject(request) {
		requestModel = get(request, "model")
	}
	return newCall(types.CapturedCall{
		ID:       *orDefault(FirstStr(get(record, "request_id"), get(record, "response_id")), ptr(HashID(context.Raw))),
		TraceID:  FirstStr(get(properties, "Helicone-Session-Id")),
		TS:       IsoTime(get(record, "request_created_at")),
		Source:   "raw",
		Provider: orDefault(NormalizeProvider(get(record, "provider")), ProviderFromShape(request, parsed)),
		Model:    FirstStr(get(record, "model"), get(record, "request_model"), get(record, "response_model"), requestModel),
		Request:  request,
		Response: CapturedResponse(parsed, ResponseExtra{
			Usage: UsageOf(types.Usage{
				InputTokens:       FirstNum(get(record, "prompt_tokens")),
				OutputTokens:      FirstNum(get(record, "completion_tokens")),
				CachedInputTokens: FirstNum(get(record, "prompt_cache_read_tokens")),
				ReasoningTokens:   FirstNum(get(record, "reasoning_tokens")),
			}),
			CostUSD:   FirstNum(get(record, "cost"), get(record, "costUSD")),
			LatencyMs: FirstNum(get(record, "delay_ms"), get(record, "latency")),
		}),
		Missing: missing,
	})
}

// spread copies an object's properties onto out, as `{...out, ...value}`
// does; anything but an object adds nothing.
func spread(out *jsval.Object, value any) {
	if !IsObject(value) {
		return
	}
	for _, key := range jsval.Keys(value) {
		out.Set(key, get(value, key))
	}
}

func litellm(record any, context RecordContext) types.CapturedCall {
	missing := []string{}
	messages := ParseJSON(get(record, "messages"))
	redacted := jsval.Nullish(messages) || messages == litellmRedacted
	if list, ok := messages.([]any); ok && !redacted {
		for _, m := range list {
			if IsObject(m) && get(m, "content") == litellmRedacted {
				redacted = true
				break
			}
		}
	}
	parameters := objectOr(get(record, "model_parameters"))
	var request any
	if !redacted {
		// model_parameters is the request minus its content (messages, prompt, input, system).
		out := jsval.NewObject()
		setDefined(out, "model", record, "model")
		if s, ok := messages.(string); ok {
			out.Set("prompt", s)
			spread(out, parameters)
		} else if IsObject(messages) {
			spread(out, parameters)
			spread(out, messages)
		} else {
			spread(out, parameters)
			out.Set("messages", messages)
		}
		request = out
	} else {
		AddMissing(&missing, "messages")
	}
	responseBody := ParseJSON(get(record, "response"))
	responseRedacted := responseBody == litellmRedacted || jsval.Nullish(responseBody)
	if responseRedacted {
		AddMissing(&missing, "response")
	}
	var body any
	if !responseRedacted {
		body = responseBody
	}
	parsed := ParseResponseBody(body)
	responseSeconds := FirstNum(get(record, "response_time"))
	latency := DurationMs(get(record, "startTime"), get(record, "endTime"))
	if latency == nil && responseSeconds != nil {
		latency = ptr(*responseSeconds * 1000)
	}
	return newCall(types.CapturedCall{
		ID:       *orDefault(FirstStr(get(record, "id"), get(record, "litellm_call_id")), ptr(HashID(context.Raw))),
		TraceID:  FirstStr(get(record, "trace_id")),
		TS:       IsoTime(get(record, "startTime")),
		Source:   "raw",
		Provider: orDefault(NormalizeProvider(get(record, "custom_llm_provider")), ProviderFromShape(request, parsed)),
		Model:    FirstStr(get(record, "model")),
		Request:  request,
		Response: CapturedResponse(parsed, ResponseExtra{
			Usage: UsageOf(types.Usage{
				InputTokens:  FirstNum(get(record, "prompt_tokens")),
				OutputTokens: FirstNum(get(record, "completion_tokens")),
			}),
			CostUSD:   FirstNum(get(record, "response_cost")),
			LatencyMs: latency,
		}),
		Missing: missing,
	})
}

// setDefined is `out[key] = from[fromKey]`, the property kept (as undefined)
// when from lacks it, as an object literal keeps it.
func setDefined(out *jsval.Object, key string, from any, fromKey string) {
	if has(from, fromKey) {
		out.Set(key, get(from, fromKey))
	} else {
		out.Set(key, jsval.Undefined)
	}
}

func generic(record any, context RecordContext) types.CapturedCall {
	missing := []string{}
	request := ParseJSON(get(record, "request"))
	if request == jsval.Undefined {
		request = nil
	}
	if request == nil {
		AddMissing(&missing, "request")
	}
	response := ParseJSON(get(record, "response"))
	if jsval.Nullish(response) {
		AddMissing(&missing, "response")
	}
	parsed := ParseResponseBody(response)
	responseObject := objectOr(response)
	var requestModel any
	if IsObject(request) {
		requestModel = get(request, "model")
	}
	ts := nullishOr(get(record, "ts"), nullishOr(get(record, "timestamp"), nullishOr(get(record, "created_at"),
		nullishOr(get(responseObject, "created"), get(responseObject, "created_at")))))
	return newCall(types.CapturedCall{
		ID:       *orDefault(FirstStr(get(record, "id"), get(responseObject, "id")), ptr(HashID(context.Raw))),
		TraceID:  FirstStr(get(record, "trace_id"), get(record, "traceId")),
		TS:       IsoTime(ts),
		Source:   "raw",
		Provider: orDefault(NormalizeProvider(get(record, "provider")), ProviderFromShape(request, parsed)),
		Model:    FirstStr(requestModel, get(responseObject, "model"), get(record, "model")),
		Request:  request,
		Response: CapturedResponse(parsed, ResponseExtra{
			CostUSD:   FirstNum(get(record, "cost_usd"), get(record, "cost")),
			LatencyMs: orDefault(FirstNum(get(record, "latency_ms")), DurationMs(get(record, "start_time"), get(record, "end_time"))),
		}),
		Truncated: get(record, "truncated") == true,
		Missing:   missing,
	})
}

func readRaw(record any, context RecordContext) []types.CapturedCall {
	if !IsObject(record) {
		return nil
	}
	if isLitellm(record) {
		return []types.CapturedCall{litellm(record, context)}
	}
	if isHelicone(record) {
		return []types.CapturedCall{helicone(record, context)}
	}
	return []types.CapturedCall{generic(record, context)}
}

// Raw reads raw provider pairs, Helicone exports and LiteLLM logs.
var Raw Adapter = adapter{name: "raw", detect: detectRaw, read: readRaw}
