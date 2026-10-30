package adapters

import (
	"testing"
)

func TestLangfuseGenerationObservationsOnly(t *testing.T) {
	calls := read(t, fixture("langfuse.jsonl"))
	equal(t, ids(calls), []string{"lf-1", "lf-3", "lf-4"})
	for _, c := range calls {
		equal(t, c.Source, "langfuse")
	}
}

func TestLangfuseInputWithToolsStringModelParametersUsageAndCost(t *testing.T) {
	first := read(t, fixture("langfuse.jsonl"))[0]
	equal(t, str(first.TraceID), "lf-trace-1")
	equal(t, str(first.TS), "2026-03-04T12:00:00.000Z")
	equal(t, str(first.Provider), "openai")
	equal(t, str(first.Model), "gpt-4o-mini")
	equal(t, first.Missing, []string{}, "tools were logged")
	equal(t, str(first.Response.Text), "shipping")
	equal(t, num(first.Response.LatencyMs), 1250.0)
	equal(t, num(first.Response.CostUSD), 0.0000072)
	equal(t, usage(first.Response.Usage), usageOf(40.0, 2.0, 0.0, nil))
	kase := prepare(first)
	equal(t, reason(kase), "")
	equal(t, str(kase.System), "You tag support tickets.")
	equal(t, params(kase), map[string]any{"temperature": 0.4, "maxTokens": 150.0, "topP": 1.0, "stop": nil, "reasoningEffort": nil})
	equal(t, kase.Missing, []string{"tools_not_offered"})
}

func TestLangfuseBareMessageInputMarksToolsMissingJSONOutputIsTheAnswer(t *testing.T) {
	third := read(t, fixture("langfuse.jsonl"))[1]
	equal(t, third.Missing, []string{"tools"})
	equal(t, str(third.Provider), "anthropic")
	equal(t, str(third.Response.Text), `{"items":["bread","cheese"]}`)
	equal(t, num(third.Response.CostUSD), 0.00004)
	equal(t, usage(third.Response.Usage), usageOf(11.0, 9.0, nil, nil))
	kase := prepare(third)
	equal(t, reason(kase), "")
	equal(t, kase.Fidelity, "partial")
}

func TestLangfuseATruncatedPreviewIsRefused(t *testing.T) {
	last := read(t, fixture("langfuse.jsonl"))[2]
	equal(t, last.Truncated, true)
	equal(t, num(last.Response.LatencyMs), nil, "no end time")
	equal(t, reason(prepare(last)), "content_truncated")
}

func TestLangfuseAJSONArrayExport(t *testing.T) {
	calls := read(t, fixture("langfuse-array.json"))
	equal(t, ids(calls), []string{"lfa-1", "lfa-3"})
	equal(t, prepare(calls[0]).Messages[0].Content, "Spell 'example' backwards.")
}

func TestLangfuseDetection(t *testing.T) {
	equal(t, detected(j(t, `{"id": "x", "traceId": "t", "type": "GENERATION"}`)), "langfuse")
	equal(t, detected(j(t, `{"id": "x", "modelParameters": {}}`)), "langfuse")
}
