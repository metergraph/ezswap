package adapters

import (
	"testing"

	"github.com/metergraph/replay/internal/types"
)

func TestReplayJSONLMapsRequestResponseUsageAndMeta(t *testing.T) {
	calls := read(t, fixture("replay.jsonl"))
	equal(t, len(calls), 4)
	first := calls[0]
	equal(t, first.Source, "replay-jsonl")
	equal(t, first.ID, "rp-1")
	equal(t, str(first.TraceID), "tr-1")
	equal(t, str(first.TS), "2026-03-01T10:00:00.000Z")
	equal(t, str(first.Provider), "openai")
	equal(t, str(first.Model), "gpt-4o-mini")
	equal(t, first.Truncated, false)
	equal(t, first.Missing, []string{})
	equal(t, str(first.Response.Text), "A calm blue mug for slow mornings.")
	equal(t, str(first.Response.FinishReason), "stop")
	equal(t, usage(first.Response.Usage), usageOf(31.0, 9.0, 0.0, nil))
	equal(t, num(first.Response.CostUSD), 0.0000101)
	equal(t, num(first.Response.LatencyMs), 812.0)

	kase := prepare(first)
	equal(t, reason(kase), "")
	equal(t, str(kase.System), "You write short product blurbs for example.com.")
	equal(t, kase.Messages, []types.Message{{Role: "user", Content: "Write a blurb for a blue mug."}})
	equal(t, params(kase), map[string]any{"temperature": 0.3, "maxTokens": 200.0, "topP": 0.9, "stop": []string{"\n\n"}, "reasoningEffort": nil})
}

func TestReplayJSONLForcedToolAndToolCallOutput(t *testing.T) {
	second := read(t, fixture("replay.jsonl"))[1]
	equal(t, second.Missing, []string{"response_format"})
	kase := prepare(second)
	equal(t, reason(kase), "")
	if kase.ForcedTool == nil || kase.ForcedTool.Name != "record_city" {
		t.Errorf("forced tool %#v", kase.ForcedTool)
	}
	equal(t, str(kase.Production.Output), `{"city":"Lyon"}`)
	equal(t, kase.Fidelity, "partial")
}

func TestReplayJSONLMetaTruncatedAndAVerbatimRawBody(t *testing.T) {
	calls := read(t, fixture("replay.jsonl"))
	equal(t, calls[2].Truncated, true)
	equal(t, reason(prepare(calls[2])), "content_truncated")
	fourth := calls[3]
	equal(t, str(fourth.TS), "2026-03-01T10:03:00.000Z", "epoch seconds")
	kase := prepare(fourth)
	equal(t, kase.Messages, []types.Message{{Role: "user", Content: "Hello from a verbatim body"}})
	equal(t, num(kase.Params.Temperature), 1.0)
}

func TestReplayJSONLLimitStopsEarly(t *testing.T) {
	limit := 2
	equal(t, len(read(t, fixture("replay.jsonl"), ReadTracesOptions{Limit: &limit})), 2)
}
