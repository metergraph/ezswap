package report

import (
	"strings"
	"testing"

	"github.com/metergraph/ezswap/internal/types"
)

func summaryMap() *Map {
	reason := "free text"
	cost := -0.5
	return &Map{
		File: "traces.jsonl", PerPart: 20, BudgetUSD: 5,
		Treatments: []types.Treatment{{ID: "t1", Label: "examplelab/small"}, {ID: "t2", Label: "examplelab/thinker"}},
		Parts: []MapPart{
			{Name: "draft-reply", Treatments: []MapTreatment{
				{Result: ResultNotComparable, Reason: &reason, CostPerCallChange: &cost},
				{Result: ResultNotComparable, Reason: &reason, CutOff: 3, CutOffReasoning: 3},
			}},
			{Name: "classify", Paths: []string{"$.label"}, Treatments: []MapTreatment{
				{Result: ResultBeyondNoise, Changed: 9, Paired: 20, CostPerCallChange: &cost},
				{Result: ResultWithinNoise, Changed: 1, Rerun: 1, Paired: 20},
			}},
		},
	}
}

func TestSummaryShowsEveryTreatmentOnEveryPart(t *testing.T) {
	text := RenderMap(summaryMap(), RenderOptions{Width: 80, ASCII: true})
	start := strings.Index(text, "SUMMARY")
	end := strings.Index(text, "treatment A")
	if start < 0 || end < start {
		t.Fatalf("no summary before the treatment blocks:\n%s", text)
	}
	summary := text[start:end]
	flat := strings.Join(strings.Fields(summary), " ")
	for _, want := range []string{
		"classify        draft-reply     COST/CALL",
		"A examplelab/small   beyond 9/20     free text       -50%",
		"B examplelab/thinker within 1/20     free text*",
		"rerun changed up to 1 per part",
		"*: replies cut off",
	} {
		if !strings.Contains(summary, want) && !strings.Contains(flat, want) {
			t.Errorf("summary lacks %q:\n%s", want, summary)
		}
	}
	one := summaryMap()
	one.Treatments = one.Treatments[:1]
	for i := range one.Parts {
		one.Parts[i].Treatments = one.Parts[i].Treatments[:1]
	}
	if strings.Contains(RenderMap(one, RenderOptions{Width: 80}), "SUMMARY") {
		t.Error("a map with one treatment printed a summary")
	}
}
