package report

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/metergraph/replay/demo"
	"github.com/metergraph/replay/internal/parts"
	"github.com/metergraph/replay/internal/report/term"
	"github.com/metergraph/replay/internal/types"
)

func demoMap(t *testing.T, options MapOptions) *Map {
	t.Helper()
	run, err := demo.MapRun()
	if err != nil {
		t.Fatal(err)
	}
	m, err := BuildMap(run, options)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func partNamed(t *testing.T, m *Map, name string) MapPart {
	t.Helper()
	for _, part := range m.Parts {
		if part.Name == name {
			return part
		}
	}
	t.Fatalf("no part %q", name)
	return MapPart{}
}

// The bundled demo's counts are the ones the documentation and the talk show.
func TestDemoMapCounts(t *testing.T) {
	m := demoMap(t, MapOptions{})
	if len(m.Parts) != 5 || m.PerPart != 20 {
		t.Fatalf("%d parts, %d per part", len(m.Parts), m.PerPart)
	}
	type want struct {
		result                               string
		paired, changed, rerun, itself, b, c int
		reason                               string
	}
	for name, w := range map[string]want{
		"extract-order":    {ResultBeyondNoise, 20, 9, 1, 1, 9, 1, ""},
		"route-to-team":    {ResultWithinNoise, 20, 4, 3, 3, 4, 3, ""},
		"classify-ticket":  {ResultWithinNoise, 20, 2, 3, 2, 2, 3, ""},
		"summarise-thread": {ResultTooFewCalls, 6, 0, 0, 0, 0, 0, "6 paired, needs 10"},
		"draft-reply":      {ResultNotComparable, 20, 0, 0, 0, 0, 0, parts.ReasonFreeText},
	} {
		got := partNamed(t, m, name).Treatments[0]
		reason := ""
		if got.Reason != nil {
			reason = *got.Reason
		}
		if got.Result != w.result || got.Paired != w.paired || got.Changed != w.changed || got.Rerun != w.rerun ||
			got.Itself != w.itself || got.OnlyTreatment != w.b || got.OnlyRerun != w.c || reason != w.reason {
			t.Errorf("%s: %+v", name, got)
		}
		if got.CostPerCallChange == nil || *got.CostPerCallChange >= 0 || got.LatencyChange == nil || *got.LatencyChange >= 0 {
			t.Errorf("%s: the cheaper, faster treatment should show lower cost and latency", name)
		}
	}
	order := partNamed(t, m, "extract-order")
	if order.Source != parts.SourceName || partNamed(t, m, "draft-reply").Source != parts.SourceFingerprint {
		t.Fatal("sources")
	}
	last := order.Treatments[0].Changes[len(order.Treatments[0].Changes)-1]
	if last.To != "reply not JSON" {
		t.Fatalf("an unreadable reply counts as changed and says why: %+v", last)
	}
}

func TestMapGolden(t *testing.T) {
	m := demoMap(t, MapOptions{})
	golden(t, "map-100-unicode.txt", RenderMap(m, RenderOptions{Width: 100}))
	golden(t, "map-80-ascii.txt", RenderMap(m, RenderOptions{Width: 80, ASCII: true}))
	ascii := RenderMap(m, RenderOptions{Width: 80, ASCII: true})
	for _, r := range ascii {
		if r > 0x7e {
			t.Fatalf("non-ASCII %q in the ASCII map", r)
		}
	}
	if strings.Contains(RenderMap(m, RenderOptions{Width: 100}), "\u2014") && !strings.Contains(ascii, "-") {
		t.Fatal("the missing-value marker has no ASCII form")
	}
}

func TestMapPrintsNoPromptText(t *testing.T) {
	run, _ := demo.MapRun()
	text := RenderMap(demoMap(t, MapOptions{}), RenderOptions{Width: 100})
	for _, kase := range run.Cases {
		if kase.System != nil && strings.Contains(text, (*kase.System)[:30]) {
			t.Fatal("the map prints a system prompt")
		}
		if strings.Contains(text, kase.Messages[len(kase.Messages)-1].Content[:30]) {
			t.Fatal("the map prints an input")
		}
	}
}

func TestMapRenameMergeAndOnly(t *testing.T) {
	merged := demoMap(t, MapOptions{Rename: []string{"route-to-team=triage", "classify-ticket=triage"}})
	triage := partNamed(t, merged, "triage")
	if len(merged.Parts) != 4 || len(triage.Keys) != 2 || triage.Sampled != 40 || triage.Treatments[0].Changed != 6 {
		t.Fatalf("merge: %d parts, %+v", len(merged.Parts), triage)
	}
	only := demoMap(t, MapOptions{Only: []string{"extract-order", "name:route-to-team"}})
	if len(only.Parts) != 2 {
		t.Fatalf("only: %d parts", len(only.Parts))
	}
	run, _ := demo.MapRun()
	if _, err := BuildMap(run, MapOptions{Only: []string{"nope"}}); err == nil {
		t.Fatal("unknown --only")
	}
}

func TestMapNeedsAMapRun(t *testing.T) {
	run, _ := demo.Run()
	if _, err := BuildMap(run, MapOptions{}); err != ErrNotMapRun {
		t.Fatalf("an ordinary run has no parts: %v", err)
	}
	if IsMapRun(run) {
		t.Fatal("an ordinary run is not a map run")
	}
}

func TestMapJSON(t *testing.T) {
	data, err := MapJSON(demoMap(t, MapOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Parts []struct {
			Name       string   `json:"name"`
			Keys       []string `json:"keys"`
			Paths      []string `json:"paths"`
			Treatments []struct {
				Result string  `json:"result"`
				Reason *string `json:"reason"`
			} `json:"treatments"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Parts) != 5 || decoded.Parts[0].Treatments[0].Result == "" || decoded.Parts[0].Paths == nil {
		t.Fatalf("%s", data)
	}
}

// --- hand-built runs --------------------------------------------------------

type row struct{ control, treatment, a1, a2 string }

const skip = "<none>"

func str(s string) *string { return &s }

// runOf is a one-part map run: one row per case. skip leaves a replay out;
// "FAIL" makes it a failed call.
func runOf(rows []row, settings func(*types.RunSettings)) *types.RunFile {
	perPart := 20
	run := &types.RunFile{
		Settings:   types.RunSettings{Sample: perPart, PerPart: &perPart, NoiseFloor: true, BudgetUSD: 5},
		Treatments: []types.Treatment{{ID: "t1", Label: "candidate", Model: "candidate"}},
		Input:      types.RunInput{File: "traces.jsonl"},
	}
	if settings != nil {
		settings(&run.Settings)
	}
	for i, r := range rows {
		id := fmt.Sprintf("case-%02d", i)
		control := r.control
		run.Cases = append(run.Cases, types.Case{
			ID: id, PartKey: "name:part", PartName: "part", TS: str("2026-11-10T10:00:00.000Z"),
			Production: types.CaseOutput{Output: &control},
		})
		for arm, output := range map[string]string{"t1": r.treatment, types.ArmFloorA1: r.a1, types.ArmFloorA2: r.a2} {
			if output == skip {
				continue
			}
			replay := types.Replay{CaseID: id, Arm: arm, Status: "ok", Output: str(output)}
			if output == "FAIL" {
				replay.Status, replay.Output = "failed", nil
			}
			cost, latency := 0.001, 500.0
			if arm == "t1" {
				cost, latency = 0.0005, 250
			}
			replay.CostUSD, replay.LatencyMs = &cost, &latency
			run.Replays = append(run.Replays, replay)
		}
	}
	return run
}

func same(n int, output string) []row {
	rows := make([]row, n)
	for i := range rows {
		rows[i] = row{output, output, output, output}
	}
	return rows
}

func only(t *testing.T, run *types.RunFile) (MapPart, MapTreatment) {
	t.Helper()
	m, err := BuildMap(run, MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return m.Parts[0], m.Parts[0].Treatments[0]
}

func TestLabels(t *testing.T) {
	a, b := `{"label":"a","n":1}`, `{"label":"b","n":1}`
	rows := same(20, a)
	for i := 0; i < 5; i++ {
		rows[i].treatment = b
	}
	if _, got := only(t, runOf(rows, nil)); got.Result != ResultBeyondNoise || got.Changed != 5 || got.Rerun != 0 {
		t.Fatalf("5 against 0: %+v", got)
	}
	rows[4].treatment = a
	if _, got := only(t, runOf(rows, nil)); got.Result != ResultWithinNoise || got.Changed != 4 {
		t.Fatalf("4 against 0: %+v", got)
	}
	if _, got := only(t, runOf(same(9, a), nil)); got.Result != ResultTooFewCalls || *got.Reason != "9 paired, needs 10" {
		t.Fatalf("9 cases: %+v", got)
	}
	if _, got := only(t, runOf(same(20, "plain text"), nil)); got.Result != ResultNotComparable || *got.Reason != parts.ReasonFreeText {
		t.Fatalf("free text: %+v", got)
	}
	noFloor := runOf(rows, func(s *types.RunSettings) { s.NoiseFloor = false })
	if _, got := only(t, noFloor); got.Result != ResultNotComparable || *got.Reason != ReasonNoFloor {
		t.Fatalf("no floor: %+v", got)
	}
}

// The label sets the treatment against production's rerun, both measured
// against the recording, so a recording that has gone stale raises both and
// does not read as the treatment's doing.
func TestStaleRecordingDoesNotBlameTheTreatment(t *testing.T) {
	old, current := `{"label":"a","n":1}`, `{"label":"b","n":1}`
	rows := same(20, old)
	for i := 0; i < 12; i++ {
		rows[i] = row{old, current, current, current}
	}
	_, got := only(t, runOf(rows, nil))
	if got.Result != ResultWithinNoise || got.Changed != 12 || got.Rerun != 12 || got.Itself != 0 {
		t.Fatalf("%+v", got)
	}
	notes := strings.Join(got.Notes, "\n")
	if !strings.Contains(notes, "no longer reproduces its recording") || !strings.Contains(notes, "can hardly exceed") {
		t.Fatalf("the stale and noisy notes: %q", notes)
	}
}

func TestCaseRules(t *testing.T) {
	a := `{"label":"a","n":1}`
	rows := same(20, a)
	rows[0].treatment = "I cannot answer that."            // not JSON: changed
	rows[1].treatment = `{"label":"a","n":"1"}`            // the text "1" is not the number 1
	rows[2].treatment = `{"label":"a","n":1,"extra":true}` // an extra field is ignored
	rows[3].treatment = `{"label":" a ","n":1}`            // spaces around text are ignored
	rows[4].treatment = `{"label":"a"}`                    // a missing field: changed
	rows[5].treatment = `{"label":"a","n":null}`           // null reads as missing: changed
	rows[6] = row{`{"other":true}`, a, a, a}               // the recording has no compared field: left out
	rows[7] = row{`{"label":"a"}`, `{"label":"a","n":9}`, `{"label":"a"}`, `{"label":"a"}`}
	rows[8].treatment = "FAIL"
	rows[9].a1 = skip
	part, got := only(t, runOf(rows, nil))
	if part.LeftOut != 1 || got.Failed != 1 || got.Paired != 17 {
		t.Fatalf("left out %d, failed %d, paired %d", part.LeftOut, got.Failed, got.Paired)
	}
	if got.Changed != 4 {
		t.Fatalf("changed %d: %+v", got.Changed, got.Changes)
	}
	if got.Changes[0].To != "reply not JSON" || got.Changes[1].Path != "$.n" || got.Changes[1].To != `"1"` || got.Changes[2].To != "(no value)" {
		t.Fatalf("%+v", got.Changes)
	}
}

func TestListsAreComparedInOrder(t *testing.T) {
	a := `{"items":[{"sku":"A","qty":1},{"sku":"B","qty":2}]}`
	swapped := `{"items":[{"sku":"A","qty":2},{"sku":"B","qty":1}]}`
	rows := same(20, a)
	for i := 0; i < 6; i++ {
		rows[i].treatment = swapped
	}
	part, got := only(t, runOf(rows, nil))
	if strings.Join(part.Paths, " ") != "$.items[].qty $.items[].sku" && strings.Join(part.Paths, " ") != "$.items[].sku $.items[].qty" {
		t.Fatalf("paths %v", part.Paths)
	}
	if got.Changed != 6 || got.Result != ResultBeyondNoise {
		t.Fatalf("two lines swapping quantities is a change: %+v", got)
	}
}

func TestIgnoreAndRefusedAndCutOff(t *testing.T) {
	a := `{"label":"a","id":"x-1"}`
	rows := same(20, a)
	for i := range rows {
		rows[i].control = fmt.Sprintf(`{"label":"a","id":"x-%d"}`, i)
		rows[i].treatment = fmt.Sprintf(`{"label":"a","id":"y-%d"}`, i)
		rows[i].a1 = fmt.Sprintf(`{"label":"a","id":"z-%d"}`, i)
		rows[i].a2 = rows[i].a1
	}
	_, noisy := only(t, runOf(rows, nil))
	if noisy.Rerun != 20 || !strings.Contains(strings.Join(noisy.Notes, " "), "--ignore '$.id'") {
		t.Fatalf("a generated id raises the baseline and the note names it: %+v", noisy)
	}
	ignored := runOf(rows, func(s *types.RunSettings) { s.IgnoredFields = []string{"$.id"} })
	part, quiet := only(t, ignored)
	if strings.Join(part.Paths, " ") != "$.label" || quiet.Changed != 0 || quiet.Rerun != 0 {
		t.Fatalf("--ignore: %v %+v", part.Paths, quiet)
	}
	run := runOf(same(20, `{"label":"a","n":1}`), nil)
	length := "length"
	for i := range run.Replays {
		r := &run.Replays[i]
		if r.Arm != "t1" {
			continue
		}
		switch r.CaseID {
		case "case-00":
			r.Refusal, r.Output = true, str("I can't help with that.")
		case "case-01":
			r.FinishReason, r.Output = &length, str(`{"label":"a","n"`)
		}
	}
	_, got := only(t, run)
	if got.Changed != 2 || got.Changes[0].To != "refused" || got.Changes[1].To != "cut off" {
		t.Fatalf("%+v", got.Changes)
	}
}

func TestMapRowOrderTruncationAndSeveralTreatments(t *testing.T) {
	a, b := `{"label":"a","n":1}`, `{"label":"b","n":1}`
	perPart := 20
	run := &types.RunFile{
		Settings:   types.RunSettings{Sample: 20, PerPart: &perPart, NoiseFloor: true, BudgetUSD: 5},
		Treatments: []types.Treatment{{ID: "t1", Label: "one"}, {ID: "t2", Label: "two"}},
		Input:      types.RunInput{File: "traces.jsonl"},
	}
	add := func(name string, changed1, changed2 int) {
		for i := 0; i < 20; i++ {
			id := fmt.Sprintf("%s-%02d", name, i)
			run.Cases = append(run.Cases, types.Case{ID: id, PartKey: "name:" + name, PartName: name, Production: types.CaseOutput{Output: str(a)}})
			out1, out2 := a, a
			if i < changed1 {
				out1 = b
			}
			if i < changed2 {
				out2 = b
			}
			for arm, output := range map[string]string{"t1": out1, "t2": out2, types.ArmFloorA1: a, types.ArmFloorA2: a} {
				run.Replays = append(run.Replays, types.Replay{CaseID: id, Arm: arm, Status: "ok", Output: str(output)})
			}
		}
	}
	add("quiet", 0, 8)
	add("a-very-long-part-name-indeed", 9, 0)
	add("loud", 6, 0)
	m, err := BuildMap(run, MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	text := term.StripAnsi(RenderMap(m, RenderOptions{Width: 100, ASCII: true}))
	first, second, ok := strings.Cut(text, "treatment B two")
	if !ok || !strings.Contains(first, "treatment A one") {
		t.Fatalf("one block per treatment:\n%s", text)
	}
	long, loud, quiet := strings.Index(first, "a-very-long-par~"), strings.Index(first, "\nloud "), strings.Index(first, "\nquiet ")
	if long < 0 || !(long < loud && loud < quiet) {
		t.Fatalf("beyond noise first, largest change first, long names cut:\n%s", first)
	}
	if !strings.Contains(first, "COST/CALL") || !strings.Contains(first, " - ") {
		t.Fatalf("an unknown cost prints a marker:\n%s", first)
	}
	if strings.Index(second, "\nquiet ") > strings.Index(second, "\nloud ") {
		t.Fatalf("each treatment has its own row order:\n%s", second)
	}
	if strings.Count(first, "and 1 more") != 1 {
		t.Fatalf("the change list is capped at %d:\n%s", ChangeLimit, first)
	}
}

func TestStoppedRunLabelsFromFinishedCases(t *testing.T) {
	rows := same(20, `{"label":"a","n":1}`)
	for i := 12; i < 20; i++ {
		rows[i].treatment, rows[i].a1, rows[i].a2 = skip, skip, skip
	}
	run := runOf(rows, nil)
	stopped := "budget"
	run.Stopped = &stopped
	m, _ := BuildMap(run, MapOptions{})
	if got := m.Parts[0].Treatments[0]; got.Paired != 12 || got.Result != ResultWithinNoise {
		t.Fatalf("%+v", got)
	}
	if !strings.Contains(RenderMap(m, RenderOptions{Width: 100}), "stopped early (budget)") {
		t.Fatal("the map says the run stopped early")
	}
}

func TestObjectsCompareWithoutKeyOrderAndDatesAreChecked(t *testing.T) {
	var a, b, c any
	json.Unmarshal([]byte(`{"x":1,"y":[1,2]}`), &a)
	json.Unmarshal([]byte(`{"y":[1,2],"x":1}`), &b)
	json.Unmarshal([]byte(`{"y":[2,1],"x":1}`), &c)
	if !sameValue(a, b) || sameValue(a, c) || sameValue(a, `{"x":1,"y":[1,2]}`) {
		t.Fatal("objects: same keys and values in any order, lists in order, never equal to text")
	}
	run := runOf(same(10, `{"label":"a","n":1}`), nil)
	bad := "\x1b]0;title\x07 not a date"
	run.Cases[0].TS = &bad
	m, _ := BuildMap(run, MapOptions{})
	if text := RenderMap(m, RenderOptions{Width: 100}); strings.Contains(text, "\x1b]0") || m.From != "2026-11-10" {
		t.Fatalf("a timestamp that is not a date reached the header: %q", m.From)
	}
}

func TestCutOffNoteSaysWhenReasoningTookTheCap(t *testing.T) {
	run := runOf(same(20, `{"label":"a"}`), nil)
	length := "length"
	thought, plain, out := 190.0, 20.0, 200.0
	for i := range run.Replays {
		r := &run.Replays[i]
		if r.Arm != "t1" {
			continue
		}
		switch r.CaseID {
		case "case-00", "case-01", "case-02":
			r.FinishReason, r.Output = &length, str("")
			r.Usage = &types.Usage{OutputTokens: &out, ReasoningTokens: &thought}
		case "case-03":
			r.FinishReason, r.Output = &length, str(`{"label":`)
			r.Usage = &types.Usage{OutputTokens: &out, ReasoningTokens: &plain}
		}
	}
	part, got := only(t, run)
	if got.CutOff != 4 || got.CutOffReasoning != 3 {
		t.Fatalf("cut off %d, reasoning %d", got.CutOff, got.CutOffReasoning)
	}
	want := "part: 4 of 20 replies cut off at the output cap, 3 of them mostly reasoning. Try --max-tokens or --reasoning-effort."
	if note := cutOffNote(part.Name, got); note != want {
		t.Errorf("note %q", note)
	}
	got.CutOffReasoning = 0
	if note := cutOffNote(part.Name, got); !strings.HasSuffix(note, "cap. Try --max-tokens.") {
		t.Errorf("note without reasoning %q", note)
	}
}
