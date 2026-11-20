package providers

// The OpenAI chat-completions request for one replay, and the reading of its reply.

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"

	"github.com/metergraph/ezswap/internal/types"
)

type PayloadOptions struct {
	// Send the call's temperature. False after the model refused it.
	Temperature bool
	// Send the call's response_format. False after the endpoint refused it.
	ResponseFormat bool
	// The model id as this endpoint takes it. Default ("" here): call.Model.
	Model string
	// "max_tokens" (most endpoints) or "max_completion_tokens" (OpenAI). Default "max_tokens".
	MaxTokensField string
	// Fields the preset adds, never in place of the call's own.
	Extra map[string]any
}

// ForcedToolRequestBody is a forced tool as a function tool and a forced choice.
type ForcedToolRequestBody struct {
	Tool       map[string]any `json:"tool"`
	ToolChoice map[string]any `json:"tool_choice"`
}

// ForcedToolRequest is a forced tool as a function tool and a forced choice.
// The schema goes as strict: false, since a schema one provider accepts
// another's strict mode may reject. The key order is fixed: the upper bound
// measures this JSON, and conformance/estimate.json pins the bytes. (A Go map
// has no key order, so the wrapper's keys come out sorted; the bound measures
// only the length, which order does not change. The schema itself is raw JSON
// and keeps its own order.)
func ForcedToolRequest(tool types.ForcedTool) ForcedToolRequestBody {
	// The parameters go verbatim, in the schema's own key order; nil is JSON null.
	parameters := json.RawMessage("null")
	if len(bytes.TrimSpace(tool.Parameters)) > 0 {
		parameters = cloneJSON(tool.Parameters)
	}
	fn := map[string]any{"name": tool.Name, "parameters": parameters, "strict": false}
	if tool.Description != nil {
		fn["description"] = *tool.Description
	}
	return ForcedToolRequestBody{
		Tool:       map[string]any{"type": "function", "function": fn},
		ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": tool.Name}},
	}
}

func BuildPayload(call types.ReplayCall, options PayloadOptions) map[string]any {
	messages := make([]any, 0, len(call.Messages)+1)
	if call.System != nil && *call.System != "" {
		messages = append(messages, map[string]any{"role": "system", "content": *call.System})
	}
	for _, message := range call.Messages {
		messages = append(messages, map[string]any{"role": message.Role, "content": message.Content})
	}
	model := options.Model
	if model == "" {
		model = call.Model
	}
	maxTokensField := options.MaxTokensField
	if maxTokensField == "" {
		maxTokensField = "max_tokens"
	}
	body := map[string]any{
		"model":        model,
		"messages":     messages,
		maxTokensField: call.MaxTokens,
	}
	if options.Temperature && call.Temperature != nil {
		body["temperature"] = *call.Temperature
	}
	if call.TopP != nil {
		body["top_p"] = *call.TopP
	}
	if len(call.Stop) > 0 {
		body["stop"] = append([]string(nil), call.Stop...)
	}
	if call.ReasoningEffort != nil && *call.ReasoningEffort != "" {
		body["reasoning_effort"] = *call.ReasoningEffort
	}
	if options.ResponseFormat && HasJSON(call.ResponseFormat) {
		body["response_format"] = cloneJSON(call.ResponseFormat)
	}
	if call.ForcedTool != nil {
		forced := ForcedToolRequest(*call.ForcedTool)
		body["tools"] = []any{forced.Tool}
		body["tool_choice"] = forced.ToolChoice
	}
	for key, value := range options.Extra {
		if _, taken := body[key]; !taken {
			body[key] = value
		}
	}
	return body
}

// HasJSON says a raw JSON value is there: not nil, empty or a JSON null.
// Any JSON object or array counts, empty or not.
func HasJSON(raw json.RawMessage) bool {
	text := bytes.TrimSpace(raw)
	return len(text) > 0 && !bytes.Equal(text, []byte("null"))
}

// cloneJSON is structuredClone for raw JSON: a copy of the bytes, so the
// request body never shares the case's buffer. It embeds as-is, keeping the
// source's key order.
func cloneJSON(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}

// ---------------------------------------------------------------------------
// The reply

var cutOff = map[string]bool{"length": true, "max_tokens": true, "max_output_tokens": true}
var refusal = map[string]bool{"content_filter": true, "refusal": true, "safety": true}

// IsCutOff says the model stopped at its output cap: whatever it wrote is unfinished.
func IsCutOff(finishReason *string) bool {
	return finishReason != nil && cutOff[strings.ToLower(*finishReason)]
}

// IsRefusalReason is a finish reason that says the model or a filter refused.
func IsRefusalReason(finishReason *string) bool {
	return finishReason != nil && refusal[strings.ToLower(*finishReason)]
}

type ParsedReply struct {
	Output          *string
	FromToolCall    bool
	FinishReason    *string
	Refusal         bool
	Usage           *types.Usage
	ReportedCostUSD *float64
}

func object(value any) map[string]any {
	if m, ok := value.(map[string]any); ok && m != nil {
		return m
	}
	return map[string]any{}
}

func count(value any) *float64 {
	if n, ok := value.(float64); ok && !math.IsInf(n, 0) && n == math.Trunc(n) && n >= 0 {
		return &n
	}
	return nil
}

// ToolCallArguments is the arguments of the reply's first tool call, as
// text, when there is one. Object arguments are written as JSON.stringify
// writes them; a decoded Go map has lost the reply's key order, so here the
// keys come out sorted. The provider keeps the reply's own order (see
// parseReplyOrdered).
func ToolCallArguments(message map[string]any) *string {
	args, ok := firstToolArguments(message)
	if !ok {
		return nil
	}
	switch v := args.(type) {
	case string:
		return &v
	case map[string]any:
		text := jsonStringify(v)
		return &text
	}
	return nil
}

func firstToolArguments(message map[string]any) (any, bool) {
	calls, ok := message["tool_calls"].([]any)
	if !ok || len(calls) == 0 {
		return nil, false
	}
	first, ok := calls[0].(map[string]any)
	if !ok || first == nil {
		return nil, false
	}
	fn, ok := first["function"].(map[string]any)
	if !ok || fn == nil {
		return nil, false
	}
	args, present := fn["arguments"]
	return args, present
}

// ReadUsage is usage in the replay's terms. prompt_tokens includes cached tokens.
func ReadUsage(value any) *types.Usage {
	usage, ok := value.(map[string]any)
	if !ok || usage == nil {
		return nil
	}
	prompt := object(usage["prompt_tokens_details"])
	completion := object(usage["completion_tokens_details"])
	return &types.Usage{
		InputTokens:       count(usage["prompt_tokens"]),
		OutputTokens:      count(usage["completion_tokens"]),
		CachedInputTokens: count(prompt["cached_tokens"]),
		ReasoningTokens:   count(completion["reasoning_tokens"]),
	}
}

func ParseReply(body map[string]any) ParsedReply {
	return parseReply(body, nil)
}

// parseReply reads a reply. raw, when given, is the reply's bytes, so tool
// arguments sent as an object come out in the reply's own key order, as
// JSON.stringify writes them after JSON.parse.
func parseReply(body map[string]any, raw []byte) ParsedReply {
	choices, _ := body["choices"].([]any)
	var firstChoice any
	if len(choices) > 0 {
		firstChoice = choices[0]
	}
	first := object(firstChoice)
	message := object(first["message"])
	content := message["content"]
	fromToolCall := false
	args := ToolCallArguments(message)
	if args != nil && raw != nil {
		if _, isObject := mustArguments(message).(map[string]any); isObject {
			if ordered, ok := orderedArguments(raw); ok {
				args = &ordered
			}
		}
	}
	text, isString := content.(string)
	if args != nil && !(isString && jsTrim(text) != "") {
		// A forced tool's answer is its arguments; "tool_calls" is then a normal stop.
		content = *args
		fromToolCall = true
	}
	var finishReason *string
	if reason, ok := first["finish_reason"].(string); ok {
		finishReason = &reason
	}
	usageValue := body["usage"]
	var reportedCost *float64
	if reported, ok := object(usageValue)["cost"].(float64); ok && !math.IsInf(reported, 0) && !math.IsNaN(reported) && reported >= 0 {
		reportedCost = &reported
	}
	var output *string
	if s, ok := content.(string); ok {
		output = &s
	}
	return ParsedReply{
		Output:          output,
		FromToolCall:    fromToolCall,
		FinishReason:    finishReason,
		Refusal:         truthy(message["refusal"]) || IsRefusalReason(finishReason),
		Usage:           ReadUsage(usageValue),
		ReportedCostUSD: reportedCost,
	}
}

func mustArguments(message map[string]any) any {
	args, _ := firstToolArguments(message)
	return args
}

// truthy is Boolean(value) for a decoded JSON value.
func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0 && !math.IsNaN(v)
	case string:
		return v != ""
	default:
		return true
	}
}
