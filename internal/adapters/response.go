// Provider response bodies (OpenAI chat and Responses, Anthropic Messages,
// Gemini, OpenTelemetry output messages) to the parts of CapturedResponse
// they carry.

package adapters

import (
	"slices"
	"strings"

	"github.com/metergraph/ezswap/internal/cases/jsval"
	"github.com/metergraph/ezswap/internal/types"
)

// ParsedResponse is what a response body carries. ToolCalls hold the body's
// own values: ordered objects when the body came from the reader, so a tool
// call's arguments keep their source key order.
type ParsedResponse struct {
	Text         *string
	ToolCalls    []any
	FinishReason *string
	Refusal      bool
	Usage        *types.Usage
	// Which shape was recognized, or "" when none was: "openai_chat" |
	// "openai_responses" | "anthropic" | "gemini" | "otel" | "text".
	Shape string
}

var refusalReasons = []string{"refusal", "content_filter", "safety"}

func isRefusalReason(reason *string) bool {
	return reason != nil && slices.Contains(refusalReasons, *reason)
}

func joined(texts []string) *string {
	if len(texts) == 0 {
		return nil
	}
	out := strings.Join(texts, "\n")
	return &out
}

func nonEmpty(list []any) []any {
	if len(list) == 0 {
		return nil
	}
	return list
}

func objectOr(value any) any {
	if jsval.IsObject(value) {
		return value
	}
	return map[string]any{}
}

func listOf(value any) []any {
	list, _ := value.([]any)
	return list
}

func blockTexts(content any) []string {
	if s, ok := content.(string); ok {
		return []string{s}
	}
	list, ok := content.([]any)
	if !ok {
		return nil
	}
	texts := []string{}
	for _, block := range list {
		if s, ok := block.(string); ok {
			texts = append(texts, s)
		} else if text, ok := get(block, "text").(string); ok && IsObject(block) && get(block, "type") != "refusal" {
			texts = append(texts, text)
		}
	}
	return texts
}

func openaiChatUsage(usage any) *types.Usage {
	if !IsObject(usage) {
		return nil
	}
	prompt := objectOr(get(usage, "prompt_tokens_details"))
	completion := objectOr(get(usage, "completion_tokens_details"))
	return UsageOf(types.Usage{
		InputTokens:       FirstNum(get(usage, "prompt_tokens"), get(usage, "input_tokens")),
		OutputTokens:      FirstNum(get(usage, "completion_tokens"), get(usage, "output_tokens")),
		CachedInputTokens: FirstNum(get(prompt, "cached_tokens"), get(usage, "cache_read_input_tokens")),
		ReasoningTokens:   FirstNum(get(completion, "reasoning_tokens")),
	})
}

func responsesUsage(usage any) *types.Usage {
	if !IsObject(usage) {
		return nil
	}
	input := objectOr(get(usage, "input_tokens_details"))
	output := objectOr(get(usage, "output_tokens_details"))
	return UsageOf(types.Usage{
		InputTokens:       FirstNum(get(usage, "input_tokens")),
		OutputTokens:      FirstNum(get(usage, "output_tokens")),
		CachedInputTokens: FirstNum(get(input, "cached_tokens")),
		ReasoningTokens:   FirstNum(get(output, "reasoning_tokens")),
	})
}

func anthropicUsage(usage any) *types.Usage {
	if !IsObject(usage) {
		return nil
	}
	return UsageOf(types.Usage{
		InputTokens:       FirstNum(get(usage, "input_tokens")),
		OutputTokens:      FirstNum(get(usage, "output_tokens")),
		CachedInputTokens: FirstNum(get(usage, "cache_read_input_tokens")),
	})
}

func geminiUsage(usage any) *types.Usage {
	if !IsObject(usage) {
		return nil
	}
	return UsageOf(types.Usage{
		InputTokens:       FirstNum(get(usage, "promptTokenCount")),
		OutputTokens:      FirstNum(get(usage, "candidatesTokenCount")),
		CachedInputTokens: FirstNum(get(usage, "cachedContentTokenCount")),
		ReasoningTokens:   FirstNum(get(usage, "thoughtsTokenCount")),
	})
}

func openaiChat(body any) ParsedResponse {
	choices := listOf(get(body, "choices"))
	choice := objectOr(choices[0])
	message := objectOr(get(choice, "message"))
	toolCalls := listOf(get(message, "tool_calls"))
	finishReason := Str(get(choice, "finish_reason"))
	refusal := Str(get(message, "refusal")) != nil || isRefusalReason(finishReason)
	return ParsedResponse{
		Text:         joined(blockTexts(get(message, "content"))),
		ToolCalls:    nonEmpty(toolCalls),
		FinishReason: finishReason,
		Refusal:      refusal,
		Usage:        openaiChatUsage(get(body, "usage")),
		Shape:        "openai_chat",
	}
}

func openaiResponses(body any) ParsedResponse {
	texts := []string{}
	toolCalls := []any{}
	refusal := false
	for _, item := range listOf(get(body, "output")) {
		if !IsObject(item) {
			continue
		}
		switch get(item, "type") {
		case "message":
			for _, part := range listOf(get(item, "content")) {
				if !IsObject(part) {
					continue
				}
				if get(part, "type") == "refusal" {
					refusal = true
				} else if text, ok := get(part, "text").(string); ok {
					texts = append(texts, text)
				}
			}
		case "function_call", "custom_tool_call":
			toolCalls = append(toolCalls, item)
		}
	}
	text := joined(texts)
	if s, ok := get(body, "output_text").(string); ok {
		text = &s
	}
	var incomplete *string
	if details := get(body, "incomplete_details"); IsObject(details) {
		incomplete = Str(get(details, "reason"))
	}
	return ParsedResponse{
		Text:         text,
		ToolCalls:    nonEmpty(toolCalls),
		FinishReason: orDefault(incomplete, Str(get(body, "status"))),
		Refusal:      refusal,
		Usage:        responsesUsage(get(body, "usage")),
		Shape:        "openai_responses",
	}
}

func anthropic(body any) ParsedResponse {
	texts := []string{}
	toolCalls := []any{}
	for _, block := range listOf(get(body, "content")) {
		if !IsObject(block) {
			continue
		}
		if text, ok := get(block, "text").(string); ok && get(block, "type") == "text" {
			texts = append(texts, text)
		} else if get(block, "type") == "tool_use" {
			toolCalls = append(toolCalls, block)
		}
	}
	finishReason := Str(get(body, "stop_reason"))
	return ParsedResponse{
		Text:         joined(texts),
		ToolCalls:    nonEmpty(toolCalls),
		FinishReason: finishReason,
		Refusal:      finishReason != nil && *finishReason == "refusal",
		Usage:        anthropicUsage(get(body, "usage")),
		Shape:        "anthropic",
	}
}

var geminiRefusals = []string{"SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII"}

func gemini(body any) ParsedResponse {
	candidates := listOf(get(body, "candidates"))
	candidate := objectOr(candidates[0])
	content := objectOr(get(candidate, "content"))
	texts := []string{}
	toolCalls := []any{}
	for _, part := range listOf(get(content, "parts")) {
		if !IsObject(part) || get(part, "thought") == true {
			continue
		}
		if text, ok := get(part, "text").(string); ok {
			texts = append(texts, text)
		} else if call := get(part, "functionCall"); IsObject(call) {
			toolCalls = append(toolCalls, call)
		}
	}
	finishReason := Str(get(candidate, "finishReason"))
	return ParsedResponse{
		Text:         joined(texts),
		ToolCalls:    nonEmpty(toolCalls),
		FinishReason: finishReason,
		Refusal:      finishReason != nil && slices.Contains(geminiRefusals, *finishReason),
		Usage:        geminiUsage(get(body, "usageMetadata")),
		Shape:        "gemini",
	}
}

// OtelOutput reads OpenTelemetry GenAI output messages:
// [{role, parts: [...], finish_reason}].
func OtelOutput(messages []any) ParsedResponse {
	texts := []string{}
	toolCalls := []any{}
	var finishReason *string
	for _, message := range messages {
		if !IsObject(message) {
			continue
		}
		if role := get(message, "role"); has(message, "role") && role != jsval.Undefined && role != "assistant" {
			continue
		}
		if finishReason == nil {
			finishReason = Str(get(message, "finish_reason"))
		}
		parts, ok := get(message, "parts").([]any)
		if !ok {
			texts = append(texts, blockTexts(get(message, "content"))...)
			if calls, ok := get(message, "tool_calls").([]any); ok {
				toolCalls = append(toolCalls, calls...)
			}
			continue
		}
		for _, part := range parts {
			if !IsObject(part) {
				continue
			}
			kind := nullishOr(get(part, "type"), "text")
			if kind == "text" {
				text := nullishOr(get(part, "content"), get(part, "text"))
				if s, ok := text.(string); ok {
					texts = append(texts, s)
				}
			} else if kind == "tool_call" {
				toolCalls = append(toolCalls, part)
			}
		}
	}
	return ParsedResponse{
		Text:         joined(texts),
		ToolCalls:    nonEmpty(toolCalls),
		FinishReason: finishReason,
		Refusal:      isRefusalReason(finishReason),
		Shape:        "otel",
	}
}

// ParseResponseBody recognizes and reads a provider response body. Unknown
// shapes read as nothing.
func ParseResponseBody(value any) ParsedResponse {
	body := ParseJSON(value)
	if s, ok := body.(string); ok {
		return ParsedResponse{Text: &s, Shape: "text"}
	}
	if list, ok := body.([]any); ok {
		return OtelOutput(list)
	}
	if IsObject(body) {
		if choices, ok := get(body, "choices").([]any); ok && len(choices) > 0 {
			return openaiChat(body)
		}
		if _, ok := get(body, "output").([]any); get(body, "object") == "response" || (ok && has(body, "status")) {
			return openaiResponses(body)
		}
		if candidates, ok := get(body, "candidates").([]any); ok && len(candidates) > 0 {
			return gemini(body)
		}
		if _, ok := get(body, "content").([]any); ok && (get(body, "type") == "message" || get(body, "role") == "assistant") {
			return anthropic(body)
		}
		if _, ok := get(body, "parts").([]any); get(body, "role") == "assistant" || ok {
			return OtelOutput([]any{body})
		}
	}
	return ParsedResponse{}
}

// ResponseExtra holds the figures a record keeps beside the body.
type ResponseExtra struct {
	Usage     *types.Usage
	CostUSD   *float64
	LatencyMs *float64
}

// CapturedResponse is a CapturedResponse from a parsed body plus the figures
// the record keeps beside it.
func CapturedResponse(parsed ParsedResponse, extra ResponseExtra) types.CapturedResponse {
	response := EmptyResponse()
	response.Text = parsed.Text
	response.ToolCalls = parsed.ToolCalls
	response.FinishReason = parsed.FinishReason
	response.Refusal = parsed.Refusal
	response.Usage = MergeUsage(parsed.Usage, extra.Usage)
	response.CostUSD = extra.CostUSD
	response.LatencyMs = extra.LatencyMs
	return response
}

// MergeUsage merges field by field: the body's figures first, the record's
// beside it second.
func MergeUsage(primary, secondary *types.Usage) *types.Usage {
	if primary == nil {
		return secondary
	}
	if secondary == nil {
		return primary
	}
	return UsageOf(types.Usage{
		InputTokens:       orDefault(primary.InputTokens, secondary.InputTokens),
		OutputTokens:      orDefault(primary.OutputTokens, secondary.OutputTokens),
		CachedInputTokens: orDefault(primary.CachedInputTokens, secondary.CachedInputTokens),
		ReasoningTokens:   orDefault(primary.ReasoningTokens, secondary.ReasoningTokens),
	})
}

// ProviderFromShape is the provider implied by a request or response body
// shape, when the record does not say.
func ProviderFromShape(request any, parsed ParsedResponse) *string {
	switch parsed.Shape {
	case "openai_chat", "openai_responses":
		return ptr("openai")
	case "anthropic":
		return ptr("anthropic")
	case "gemini":
		return ptr("google")
	}
	if IsObject(request) {
		if _, ok := get(request, "contents").([]any); ok {
			return ptr("google")
		}
		if has(request, "anthropic_version") {
			return ptr("anthropic")
		}
	}
	return nil
}
