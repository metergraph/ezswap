package adapters

import (
	"slices"
	"testing"
)

func TestRawPairsOpenAIChatResponsesAnthropicAndGemini(t *testing.T) {
	calls := read(t, fixture("raw-pairs.jsonl"))
	equal(t, len(calls), 4)
	for _, c := range calls {
		equal(t, c.Source, "raw")
	}
	chat, responses, anthropic, gemini := calls[0], calls[1], calls[2], calls[3]
	equal(t, chat.ID, "raw-chat")
	equal(t, str(chat.Provider), "openai")
	equal(t, str(chat.Model), "gpt-4o-mini")
	equal(t, str(chat.Response.Text), `{"label":"positive"}`)
	equal(t, str(chat.Response.FinishReason), "stop")
	equal(t, num(chat.Response.LatencyMs), 640.0)
	equal(t, num(chat.Response.CostUSD), 0.00002)
	equal(t, usage(chat.Response.Usage), usageOf(22.0, 6.0, 0.0, 0.0))
	equal(t, string(prepare(chat).ResponseFormat), `{"type":"json_object"}`)

	equal(t, responses.ID, "resp_1")
	equal(t, str(responses.Provider), "openai")
	equal(t, str(responses.TS), "2026-03-02T09:01:00.000Z")
	equal(t, str(responses.Response.Text), "4")
	equal(t, str(responses.Response.FinishReason), "completed")
	equal(t, num(responses.Response.Usage.ReasoningTokens), 38.0)
	equal(t, str(prepare(responses).Params.ReasoningEffort), "low")

	equal(t, str(anthropic.Provider), "anthropic")
	equal(t, str(anthropic.Response.Text), "Red.")
	equal(t, str(anthropic.Response.FinishReason), "end_turn")
	equal(t, str(prepare(anthropic).System), "Be concise.")

	equal(t, str(gemini.Provider), "google")
	equal(t, str(gemini.Response.Text), "Hi!")
	equal(t, num(gemini.Response.Usage.InputTokens), 3.0)
	equal(t, num(prepare(gemini).Params.MaxTokens), 10.0)
	for _, c := range calls {
		equal(t, reason(prepare(c)), "", c.ID)
	}
}

func TestRawHeliconeExportWithBodiesAndThePlaceholderWithoutThem(t *testing.T) {
	calls := read(t, fixture("helicone.jsonl"))
	withBody, placeholder := calls[0], calls[1]
	equal(t, withBody.ID, "hc-1")
	equal(t, str(withBody.TraceID), "session-1")
	equal(t, str(withBody.Provider), "openai")
	equal(t, str(withBody.TS), "2026-03-03T08:00:00.000Z")
	equal(t, str(withBody.Response.Text), "Lima.")
	equal(t, num(withBody.Response.LatencyMs), 950.0)
	equal(t, num(withBody.Response.CostUSD), 0.0000042)
	equal(t, usage(withBody.Response.Usage), usageOf(12.0, 4.0, 2.0, nil))
	equal(t, reason(prepare(withBody)), "")

	equal(t, placeholder.Request, nil)
	equal(t, placeholder.Missing, []string{"request_body", "response_body"})
	equal(t, str(placeholder.Response.Text), nil)
	equal(t, num(placeholder.Response.CostUSD), 0.00008)
	equal(t, reason(prepare(placeholder)), "content_unavailable")
}

func TestRawLiteLLMStandardLoggingPayloadPlainAndRedacted(t *testing.T) {
	calls := read(t, fixture("litellm.jsonl"))
	plain, redacted := calls[0], calls[1]
	equal(t, plain.ID, "ll-1")
	equal(t, str(plain.TraceID), "ll-trace-1")
	equal(t, str(plain.Provider), "openai")
	equal(t, str(plain.Model), "gpt-4o-mini")
	equal(t, str(plain.TS), "2026-03-03T09:00:00.250Z")
	equal(t, num(plain.Response.LatencyMs), 750.0)
	equal(t, num(plain.Response.CostUSD), 0.0000155)
	equal(t, str(plain.Response.Text), "The cat sat.")
	kase := prepare(plain)
	equal(t, reason(kase), "")
	equal(t, str(kase.System), "You fix typos.")
	equal(t, kase.Params.Stop, []string{"\n"})
	equal(t, num(kase.Params.MaxTokens), 30.0)
	if !slices.Contains(kase.Missing, "tools_not_offered") {
		t.Errorf("tools from model_parameters are seen: %v", kase.Missing)
	}

	equal(t, redacted.Request, nil)
	equal(t, redacted.Missing, []string{"messages", "response"})
	equal(t, str(redacted.Response.Text), nil)
	equal(t, usage(redacted.Response.Usage), usageOf(40.0, 12.0, nil, nil))
	equal(t, reason(prepare(redacted)), "content_unavailable")
}

func TestResponseBodiesRefusalsAndToolCalls(t *testing.T) {
	refusal := ParseResponseBody(j(t, `{"choices": [{"message": {"role": "assistant", "content": null, "refusal": "I can't help with that."}, "finish_reason": "stop"}]}`))
	equal(t, refusal.Refusal, true)
	equal(t, str(refusal.Text), nil)

	tool := ParseResponseBody(j(t, `{"choices": [{"message": {"role": "assistant", "content": null, "tool_calls": [{"id": "1", "function": {"name": "f", "arguments": "{}"}}]}, "finish_reason": "tool_calls"}]}`))
	equal(t, len(tool.ToolCalls), 1)

	anthropic := ParseResponseBody(j(t, `{"type": "message", "role": "assistant", "content": [{"type": "tool_use", "id": "t", "name": "f", "input": {"a": 1}}], "stop_reason": "refusal"}`))
	equal(t, anthropic.Refusal, true)
	equal(t, len(anthropic.ToolCalls), 1)

	incomplete := ParseResponseBody(j(t, `{"object": "response", "status": "incomplete", "incomplete_details": {"reason": "max_output_tokens"}, "output": []}`))
	equal(t, str(incomplete.FinishReason), "max_output_tokens")

	geminiSafety := ParseResponseBody(j(t, `{"candidates": [{"content": {"parts": []}, "finishReason": "SAFETY"}]}`))
	equal(t, geminiSafety.Refusal, true)

	equal(t, str(ParseResponseBody("plain text").Text), "plain text")
	equal(t, ParseResponseBody(j(t, `{"something": "else"}`)).Shape, "")
}
