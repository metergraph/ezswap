package adapters

import (
	"slices"
	"testing"

	"github.com/metergraph/replay/internal/types"
)

func TestOtelAnOTLPJSONExportKeepsLLMSpansOnly(t *testing.T) {
	calls := read(t, fixture("otel-export.json"))
	equal(t, ids(calls), []string{"eee19b7ec3c1b174", "aaa19b7ec3c1b175", "bbb19b7ec3c1b176"})
	for _, c := range calls {
		equal(t, c.Source, "otel")
		equal(t, str(c.TraceID), "5b8efff798038103d269b633813fc60c")
	}
}

func TestOtelCurrentInputAndOutputMessagesAttributes(t *testing.T) {
	current := read(t, fixture("otel-export.json"))[0]
	equal(t, str(current.Provider), "openai")
	equal(t, str(current.Model), "gpt-4o-mini")
	equal(t, str(current.TS), "2026-03-04T10:00:00.000Z")
	equal(t, num(current.Response.LatencyMs), 900.0)
	equal(t, str(current.Response.Text), "May or September.")
	equal(t, str(current.Response.FinishReason), "stop")
	equal(t, usage(current.Response.Usage), usageOf(25.0, 5.0, 0.0, nil))
	equal(t, num(current.Response.CostUSD), nil)
	equal(t, current.Missing, []string{"tools", "response_format"})
	kase := prepare(current)
	equal(t, reason(kase), "")
	equal(t, str(kase.System), "You answer travel questions.")
	equal(t, kase.Messages, []types.Message{{Role: "user", Content: "Best month to visit Lisbon?"}})
	equal(t, params(kase), map[string]any{"temperature": 0.2, "maxTokens": 120.0, "topP": 0.95, "stop": []string{"END"}, "reasoningEffort": nil})
	equal(t, kase.Fidelity, "partial")
}

func TestOtelLegacyIndexedFormAndTheStructuredOutputSchema(t *testing.T) {
	legacy := read(t, fixture("otel-export.json"))[1]
	equal(t, str(legacy.Provider), "openai", "from gen_ai.system")
	equal(t, str(legacy.Response.Text), `{"fruits":["apple","pear"]}`)
	equal(t, str(legacy.Response.FinishReason), "stop")
	equal(t, num(legacy.Response.Usage.InputTokens), 30.0)
	equal(t, legacy.Missing, []string{"tool_choice"})
	kase := prepare(legacy)
	equal(t, reason(kase), "")
	equal(t, str(kase.System), "Return JSON.")
	equal(t, kase.Messages, []types.Message{{Role: "user", Content: "List two fruits."}})
	equal(t, string(kase.ResponseFormat), `{"type":"json_schema","json_schema":{"name":"output","schema":{"type":"object","properties":{"fruits":{"type":"array"}}}}}`)
	if !slices.Contains(kase.Missing, "tools_not_offered") {
		t.Errorf("llm.request.functions are offered tools: %v", kase.Missing)
	}
}

func TestOtelASpanWithoutCapturedContentHasNoRequest(t *testing.T) {
	bare := read(t, fixture("otel-export.json"))[2]
	equal(t, bare.Request, nil)
	if !slices.Contains(bare.Missing, "input_messages") || !slices.Contains(bare.Missing, "output_messages") {
		t.Errorf("missing %v", bare.Missing)
	}
	equal(t, num(bare.Response.Usage.InputTokens), 40.0)
	equal(t, reason(prepare(bare)), "content_unavailable")
}

func TestOtelOneSpanPerLineToolHistoryAndACutAttribute(t *testing.T) {
	calls := read(t, fixture("otel-spans.jsonl"))
	tools, cut := calls[0], calls[1]
	equal(t, str(tools.Provider), "anthropic")
	equal(t, tools.Missing, []string{"tool_choice", "response_format"})
	equal(t, str(tools.Response.Text), "It is sunny.")
	equal(t, reason(prepare(tools)), "tool_history")

	equal(t, cut.ID, "0x9a8b")
	equal(t, str(cut.TraceID), "0xe1f2")
	equal(t, str(cut.TS), "2026-03-04T12:30:00.000Z")
	equal(t, num(cut.Response.LatencyMs), 500.0)
	equal(t, cut.Truncated, true)
	equal(t, reason(prepare(cut)), "content_truncated")
}

func TestOtelDetection(t *testing.T) {
	equal(t, detected(j(t, `{"resourceSpans": []}`)), "otel")
	equal(t, detected(j(t, `{"spanId": "1", "attributes": [{"key": "gen_ai.request.model", "value": {"stringValue": "m"}}]}`)), "otel")
	equal(t, detected(j(t, `{"spanId": "1", "attributes": [{"key": "http.method", "value": {"stringValue": "GET"}}]}`)), nil)
}
