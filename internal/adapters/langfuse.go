// Langfuse observation export (JSONL or a JSON array), GENERATION
// observations only. The batch export carries full input and output; UI and
// API previews cut values with a "...[truncated]" marker and are refused.

package adapters

import (
	"regexp"
	"slices"
	"strings"

	"github.com/metergraph/replay/internal/cases/jsval"
	"github.com/metergraph/replay/internal/types"
)

const truncationMarker = "...[truncated]"

var observationTypes = []string{
	"GENERATION",
	"SPAN",
	"EVENT",
	"AGENT",
	"TOOL",
	"CHAIN",
	"RETRIEVER",
	"EVALUATOR",
	"EMBEDDING",
	"GUARDRAIL",
}

func detectLangfuse(record any) bool {
	if !IsObject(record) {
		return false
	}
	kind, ok := get(record, "type").(string)
	typed := ok && slices.Contains(observationTypes, kind)
	traced := has(record, "traceId") || has(record, "trace_id")
	return (typed && traced) || has(record, "modelParameters") ||
		(has(record, "model_parameters") && has(record, "trace_id") && has(record, "type"))
}

// Model parameters Langfuse may store as strings, read back as numbers.
var numericParameters = []string{
	"temperature",
	"top_p",
	"max_tokens",
	"max_completion_tokens",
	"max_output_tokens",
	"maxOutputTokens",
	"maxTokens",
}

func modelParameters(value any) *jsval.Object {
	parameters := ParseJSON(value)
	out := jsval.NewObject()
	if !IsObject(parameters) {
		return out
	}
	for _, key := range jsval.Keys(parameters) {
		raw := get(parameters, key)
		switch {
		case slices.Contains(numericParameters, key):
			if n := Num(raw); n != nil {
				out.Set(key, *n)
			}
		case key == "response_format" || key == "tool_choice" || key == "stop" || key == "tools":
			out.Set(key, ParseJSON(raw))
		case key == "reasoning_effort":
			if s, ok := raw.(string); ok {
				out.Set(key, s)
			}
		}
	}
	return out
}

var openaiModel = regexp.MustCompile(`^(gpt|o[1-9]|chatgpt|text-|davinci|openai/)`)

func inferProvider(model *string) *string {
	if model == nil || *model == "" {
		return nil
	}
	name := jsval.ToLower(*model)
	if strings.HasPrefix(name, "claude") || strings.Contains(name, "anthropic") {
		return ptr("anthropic")
	}
	if strings.HasPrefix(name, "gemini") || strings.Contains(name, "google/") {
		return ptr("google")
	}
	if openaiModel.MatchString(name) {
		return ptr("openai")
	}
	return nil
}

func langfuseUsage(record any) *types.Usage {
	var details any = map[string]any{}
	if d := get(record, "usageDetails"); IsObject(d) {
		details = d
	} else if d := get(record, "usage_details"); IsObject(d) {
		details = d
	}
	legacy := objectOr(get(record, "usage"))
	return UsageOf(types.Usage{
		InputTokens:  FirstNum(get(details, "input"), get(legacy, "input"), get(record, "inputUsage"), get(record, "promptTokens"), get(legacy, "promptTokens")),
		OutputTokens: FirstNum(get(details, "output"), get(legacy, "output"), get(record, "outputUsage"), get(record, "completionTokens"), get(legacy, "completionTokens")),
		CachedInputTokens: FirstNum(
			get(details, "input_cached_tokens"),
			get(details, "cache_read_input_tokens"),
			get(details, "input_cache_read"),
			get(details, "cached_tokens"),
		),
		ReasoningTokens: FirstNum(get(details, "output_reasoning_tokens"), get(details, "reasoning_tokens"), get(details, "output_reasoning")),
	})
}

func langfuseCost(record any) *float64 {
	var details any = map[string]any{}
	if d := get(record, "costDetails"); IsObject(d) {
		details = d
	} else if d := get(record, "cost_details"); IsObject(d) {
		details = d
	}
	return FirstNum(get(details, "total"), get(record, "totalCost"), get(record, "total_cost"), get(record, "calculatedTotalCost"))
}

func readLangfuse(record any, context RecordContext) []types.CapturedCall {
	if !IsObject(record) || get(record, "type") != "GENERATION" {
		return nil
	}
	missing := []string{}
	input := ParseJSON(get(record, "input"))
	output := ParseJSON(get(record, "output"))
	truncated := strings.Contains(jsval.Stringify(nullishOr(input, nil)), truncationMarker) ||
		strings.Contains(jsval.Stringify(nullishOr(output, nil)), truncationMarker)
	model := FirstStr(get(record, "model"), get(record, "providedModelName"), get(record, "provided_model_name"))
	parameters := modelParameters(nullishOr(get(record, "modelParameters"), get(record, "model_parameters")))

	var request any
	if jsval.Nullish(input) || input == "" {
		AddMissing(&missing, "input")
	} else {
		// The OpenAI integration logs {messages, tools} when tools were offered,
		// else the bare message list or prompt.
		var content any
		if IsObject(input) {
			content = input
		} else if list, ok := input.([]any); ok {
			content = map[string]any{"messages": list}
		} else {
			content = map[string]any{"prompt": input}
		}
		out := jsval.NewObject()
		if model != nil {
			out.Set("model", *model)
		} else {
			out.Set("model", nil)
		}
		spread(out, parameters)
		spread(out, content)
		request = out
		if !has(content, "tools") && !has(content, "functions") && !has(parameters, "tools") {
			AddMissing(&missing, "tools")
		}
	}

	parsed := ParseResponseBody(nullishOr(output, nil))
	if parsed.Shape == "" && !jsval.Nullish(output) {
		// A structured answer Langfuse stored as parsed JSON: its JSON is the output.
		text := jsval.Stringify(output)
		parsed.Text = &text
		parsed.Shape = "text"
	}
	if jsval.Nullish(output) {
		AddMissing(&missing, "output")
	}

	startTime := nullishOr(get(record, "startTime"), get(record, "start_time"))
	endTime := nullishOr(get(record, "endTime"), get(record, "end_time"))
	return []types.CapturedCall{
		newCall(types.CapturedCall{
			ID:       *orDefault(FirstStr(get(record, "id")), ptr(HashID(context.Raw))),
			TraceID:  FirstStr(get(record, "traceId"), get(record, "trace_id")),
			TS:       IsoTime(startTime),
			Source:   "langfuse",
			Provider: inferProvider(model),
			Model:    model,
			Request:  request,
			Response: CapturedResponse(parsed, ResponseExtra{
				Usage:     langfuseUsage(record),
				CostUSD:   langfuseCost(record),
				LatencyMs: DurationMs(startTime, endTime),
			}),
			Truncated: truncated,
			Missing:   missing,
			Part:      langfusePart(record, context),
		}),
	}
}

// langfusePart is the part name under the developer's key: in the
// observation's metadata, or the observation name when the key is "name".
// The name is an instrumentation default unless the developer set it, so it
// is read only when asked for.
func langfusePart(record any, context RecordContext) *string {
	if context.PartKey == "" {
		return nil
	}
	if part := partFrom(context, ParseJSON(get(record, "metadata"))); part != nil {
		return part
	}
	if context.PartKey == "name" {
		return CleanPart(get(record, "name"))
	}
	return nil
}

// Langfuse reads a Langfuse observation export.
var Langfuse Adapter = adapter{name: "langfuse", detect: detectLangfuse, read: readLangfuse}
