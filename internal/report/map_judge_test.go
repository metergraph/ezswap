package report

import (
	"strings"
	"testing"

	"github.com/metergraph/replay/demo"
	"github.com/metergraph/replay/internal/parts"
	"github.com/metergraph/replay/internal/types"
)

// judgedDemo is the bundled demo run with verdicts on its free-text part:
// the treatment differs from the recording in the first `changed` cases,
// production's rerun in the first `rerun`, and case 0 is unsure. Cases at or
// past `judged` have no verdict.
func judgedDemo(t *testing.T, changed, rerun, judged int) *types.RunFile {
	t.Helper()
	run, err := demo.MapRun()
	if err != nil {
		t.Fatal(err)
	}
	free, err := FreeTextCases(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(free) != 20 || free[0].PartName == "" {
		t.Fatalf("%d free-text cases", len(free))
	}
	p := func(yes bool) float64 {
		if yes {
			return 0.9
		}
		return 0.1
	}
	record := &types.JudgeRecord{Model: "examplelab/judge", Question: "same?"}
	for i, kase := range free {
		if i >= judged {
			break
		}
		record.Judgements = append(record.Judgements,
			types.Judgement{CaseID: kase.ID, Arm: run.Treatments[0].ID, Against: types.JudgedAgainstRecording, PDifferent: p(i < changed), Unsure: i == 0},
			types.Judgement{CaseID: kase.ID, Arm: types.ArmFloorA1, Against: types.JudgedAgainstRecording, PDifferent: p(i < rerun)},
			types.Judgement{CaseID: kase.ID, Arm: types.ArmFloorA2, Against: types.ArmFloorA1, PDifferent: p(i < rerun)},
		)
	}
	run.Judge = record
	return run
}

func TestJudgedFreeTextPartIsLabelled(t *testing.T) {
	m, err := BuildMap(judgedDemo(t, 12, 2, 20), MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := partNamed(t, m, "draft-reply").Treatments[0]
	if !got.Judged || got.Result != ResultBeyondNoise || got.Paired != 20 || got.Changed != 12 || got.Rerun != 2 ||
		got.OnlyTreatment != 10 || got.OnlyRerun != 0 || got.Itself != 2 || got.Unsure != 1 || got.Reason != nil {
		t.Fatalf("%+v", got)
	}
	if len(got.Changes) != 12 || got.Changes[0].Path != JudgedPath || got.Changes[0].To != "judged different (90%), unsure" {
		t.Fatalf("changes %+v", got.Changes)
	}
	// The JSON parts are untouched by the judge.
	if order := partNamed(t, m, "extract-order").Treatments[0]; order.Judged || order.Changed != 9 {
		t.Fatalf("extract-order %+v", order)
	}
	text := RenderMap(m, RenderOptions{Width: 100, ASCII: true})
	for _, want := range []string{
		"draft-reply      beyond noise    12 of 20  2 of 20",
		"JUDGED: draft-reply is free text, compared by examplelab/judge.",
		"judged different (90%), unsure",
		"draft-reply: the judge's verdict changed with the order of the two replies in 1 of 20 cases",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the map lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "—") {
		t.Error("an em dash in the map")
	}
}

// A crafted run file cannot put terminal escapes into the printed map.
func TestJudgeModelIsCleaned(t *testing.T) {
	run := judgedDemo(t, 12, 2, 20)
	run.Judge.Model = "evil\x1b]0;pwned\x07/judge"
	// Two treatments, so the summary block (which names the judge) prints.
	second := run.Treatments[0]
	second.ID, second.Label = "t-second", "examplelab/second"
	run.Treatments = append(run.Treatments, second)
	m, _ := BuildMap(run, MapOptions{})
	if JudgedNote(m) == "" {
		t.Fatal("no judged part")
	}
	if text := RenderMap(m, RenderOptions{Width: 100}); strings.Contains(text, "\x1b]") || strings.Contains(text, "\x07") {
		t.Fatalf("an escape reached the map:\n%q", text)
	}
}

func TestUnjudgedCasesDropOut(t *testing.T) {
	m, _ := BuildMap(judgedDemo(t, 3, 0, 8), MapOptions{})
	got := partNamed(t, m, "draft-reply").Treatments[0]
	if got.Paired != 8 || got.Result != ResultTooFewCalls {
		t.Fatalf("%+v", got)
	}
}

func TestNoisyRerunIsNamed(t *testing.T) {
	m, _ := BuildMap(judgedDemo(t, 12, 11, 20), MapOptions{})
	got := partNamed(t, m, "draft-reply").Treatments[0]
	if got.Result != ResultWithinNoise || len(got.Notes) == 0 || !strings.Contains(strings.Join(got.Notes, " "), "in 11 of 20 cases. A treatment can hardly exceed that.") {
		t.Fatalf("%+v", got)
	}
}

func TestWithoutJudgeFreeTextIsNotComparable(t *testing.T) {
	m := demoMap(t, MapOptions{})
	got := partNamed(t, m, "draft-reply").Treatments[0]
	if got.Judged || got.Result != ResultNotComparable || *got.Reason != parts.ReasonFreeText || JudgedNote(m) != "" {
		t.Fatalf("%+v", got)
	}
}
