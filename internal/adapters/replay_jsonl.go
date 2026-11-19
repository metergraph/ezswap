// Metergraph Replay JSONL: the documented trace format, one call per line.
// See docs/trace-format.md.

package adapters

import (
	"github.com/metergraph/replay/internal/cases/jsval"
	"github.com/metergraph/replay/internal/types"
)

const ReplayTraceSchema = "metergraph.replay-trace/v1"

func detectReplayJSONL(record any) bool {
	if !IsObject(record) {
		return false
	}
	if get(record, "schema") == ReplayTraceSchema {
		return true
	}
	request := get(record, "request")
	return IsObject(get(record, "meta")) &&
		IsObject(request) &&
		(has(request, "messages") || has(request, "raw") || has(request, "params"))
}

func parseReplayResponse(response any) ParsedResponse {
	var parsed ParsedResponse
	if messages, ok := get(response, "messages").([]any); ok {
		parsed = OtelOutput(messages)
	}
	if text, ok := get(response, "text").(string); ok {
		parsed.Text = &text
	}
	if calls, ok := get(response, "tool_calls").([]any); ok && len(calls) > 0 {
		parsed.ToolCalls = calls
	}
	if finish := FirstStr(get(response, "finish_reason")); finish != nil {
		parsed.FinishReason = finish
	}
	if refusal, ok := get(response, "refusal").(bool); ok {
		parsed.Refusal = refusal
	}
	usage := objectOr(get(response, "usage"))
	parsed.Usage = UsageOf(types.Usage{
		InputTokens:       FirstNum(get(usage, "input_tokens")),
		OutputTokens:      FirstNum(get(usage, "output_tokens")),
		CachedInputTokens: FirstNum(get(usage, "cached_input_tokens")),
		ReasoningTokens:   FirstNum(get(usage, "reasoning_tokens")),
	})
	return parsed
}

// The body cases parses: the normalized request, or the verbatim one when
// only it is present.
func replayRequestBody(request any) any {
	if raw := get(request, "raw"); !has(request, "messages") && !jsval.Nullish(raw) {
		return raw
	}
	normalized := jsval.ToOrdered(request)
	rest := jsval.NewObject()
	for _, key := range normalized.Keys() {
		if key != "raw" {
			value, _ := normalized.Get(key)
			rest.Set(key, value)
		}
	}
	return rest
}

func readReplayJSONL(record any, context RecordContext) []types.CapturedCall {
	if !IsObject(record) {
		return nil
	}
	var request any
	if r := get(record, "request"); IsObject(r) {
		request = r
	}
	response := objectOr(get(record, "response"))
	meta := objectOr(get(record, "meta"))
	missing := []string{}
	if list, ok := get(meta, "missing").([]any); ok {
		for _, name := range list {
			if s, ok := name.(string); ok {
				missing = append(missing, s)
			}
		}
	}
	if request == nil {
		AddMissing(&missing, "request")
	}
	if !IsObject(get(record, "response")) {
		AddMissing(&missing, "response")
	}
	var raw any = map[string]any{}
	if request != nil && IsObject(get(request, "raw")) {
		raw = get(request, "raw")
	}
	var body any
	if request != nil {
		body = replayRequestBody(request)
	}
	return []types.CapturedCall{
		newCall(types.CapturedCall{
			ID:       *orDefault(FirstStr(get(record, "id")), ptr(HashID(context.Raw))),
			TraceID:  FirstStr(get(record, "trace_id"), get(meta, "trace_id")),
			TS:       IsoTime(nullishOr(get(meta, "ts"), get(record, "ts"))),
			Source:   "replay-jsonl",
			Provider: NormalizeProvider(get(record, "provider")),
			Model:    FirstStr(get(request, "model"), get(raw, "model")),
			Request:  body,
			Response: CapturedResponse(parseReplayResponse(response), ResponseExtra{
				CostUSD:   FirstNum(get(response, "cost_usd")),
				LatencyMs: FirstNum(get(response, "latency_ms")),
			}),
			Truncated: get(meta, "truncated") == true,
			Missing:   missing,
			Part:      orDefault(partFrom(context, record, meta), CleanPart(get(record, "part"))),
		}),
	}
}

// ReplayJSONL reads the Metergraph Replay JSONL format.
var ReplayJSONL Adapter = adapter{name: "replay-jsonl", detect: detectReplayJSONL, read: readReplayJSONL}
