package core

// The synthetic run conformance/page.json was generated from: 16 cases (one not
// replayable), treatments "Mini" (t1) and "Flash" (t2, two failures and one
// skipped), and a noise floor.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/metergraph/ezswap/internal/jsnum"
	"github.com/metergraph/ezswap/internal/types"
)

func withoutFloor(run *types.RunFile) *types.RunFile {
	plain := *run
	plain.Settings.NoiseFloor = false
	plain.Replays = nil
	for _, r := range run.Replays {
		if !strings.HasPrefix(r.Arm, "floor_") {
			plain.Replays = append(plain.Replays, r)
		}
	}
	return &plain
}

func TestOnlyReplayableCasesCount(t *testing.T) {
	view := mustBuild(t, loadRun(t), BuildViewOptions{})
	if view.Sample != (Sample{Size: 16, Cases: 16, Replayed: 15, NotReplayable: 1}) {
		t.Errorf("sample = %+v", view.Sample)
	}
	if view.Control.Summary.Cases != 15 {
		t.Errorf("control cases = %d", view.Control.Summary.Cases)
	}
	if len(view.Cases) != 16 {
		t.Errorf("cases = %d", len(view.Cases))
	}
	for _, c := range view.Cases {
		if c.Status == "not_replayable" && (c.Reason == nil || *c.Reason != "tool_history") {
			t.Errorf("reason = %v", c.Reason)
		}
	}
	if n := len(view.Fields["$.priority"]["control"]); n != 15 {
		t.Errorf("priority control series = %d", n)
	}
}

func TestFailedReplayIsErrorSkippedIsNoResult(t *testing.T) {
	view := mustBuild(t, loadRun(t), BuildViewOptions{})
	var flash *ArmView
	for i := range view.Arms {
		if view.Arms[i].Label == "Flash" {
			flash = &view.Arms[i]
		}
	}
	if flash == nil {
		t.Fatal("no Flash arm")
	}
	if flash.Summary.Cases != 14 || flash.Summary.Errors != 2 {
		t.Errorf("Flash cases %d errors %d", flash.Summary.Cases, flash.Summary.Errors)
	}
	// The control over the same cases the arm returned on.
	if flash.ControlSummary.Cases != 12 {
		t.Errorf("Flash control cases = %d", flash.ControlSummary.Cases)
	}
	if flash.Summary.TemperatureDropped != 1 {
		t.Errorf("temperature dropped = %d", flash.Summary.TemperatureDropped)
	}
	sameJSON(t, flash.Settings, obj("temperature", 0.0, "max_tokens", 1024.0, "reasoning_effort", "low"), "settings")
}

func TestControlRefusalCountsAsEmptyOrRefused(t *testing.T) {
	view := mustBuild(t, loadRun(t), BuildViewOptions{})
	want := jsnum.Round(1.0/15*1e4) / 1e4
	if s := view.Control.Summary; s.EmptyOrRefusedRate == nil || *s.EmptyOrRefusedRate != want {
		t.Errorf("empty or refused = %v", s.EmptyOrRefusedRate)
	}
	if s := view.Control.Summary; s.CutOffRate == nil || *s.CutOffRate != want {
		t.Errorf("cut off = %v", s.CutOffRate)
	}
	if view.Control.Model == nil || *view.Control.Model != "gpt-5.4" {
		t.Errorf("control model = %v", view.Control.Model)
	}
}

func TestNoiseFloorPairsCasesWhereBothReturned(t *testing.T) {
	view := mustBuild(t, loadRun(t), BuildViewOptions{})
	nf := view.NoiseFloor
	if nf == nil {
		t.Fatal("no noise floor")
	}
	if nf.Cases != 15 {
		t.Errorf("floor cases = %d", nf.Cases)
	}
	if len(nf.GatewayModels) != 1 || nf.GatewayModels[0] != "gpt-5.4" {
		t.Errorf("gateway models = %v", nf.GatewayModels)
	}
	if nf.Fields["$.category"].Kind != KindLabel {
		t.Errorf("category kind = %s", nf.Fields["$.category"].Kind)
	}
	if c := view.Cases[0]; c.NoiseFloor == nil || c.NoiseFloor.A1 == nil || c.NoiseFloor.A2 == nil {
		t.Error("case 0 lacks its floor replays")
	}
}

func TestRunWithoutFloorHasNullNoiseFloor(t *testing.T) {
	view := mustBuild(t, withoutFloor(loadRun(t)), BuildViewOptions{})
	if view.NoiseFloor != nil {
		t.Error("noise floor is not nil")
	}
	if view.Cases[0].NoiseFloor != nil {
		t.Error("case 0 has a floor")
	}
	mini := Equivalence(view, "").Treatments[0]
	if mini.Latency.Control != nil {
		t.Errorf("latency control = %v", *mini.Latency.Control)
	}
	if !strings.Contains(mini.Cost.Sentence, "for control over the same 15 cases") {
		t.Errorf("cost sentence = %q", mini.Cost.Sentence)
	}
	if NoiseLabelsOf(view, "Mini", nil) != nil {
		t.Error("labels without a floor")
	}
}

func TestWithoutCasesViewKeepsStatistics(t *testing.T) {
	view := mustBuild(t, loadRun(t), BuildViewOptions{ExcludeCases: true})
	if view.Cases != nil || view.Fields != nil {
		t.Error("cases or fields kept")
	}
	if view.NoiseFloor.Stats["latency_ms"].Differences != nil {
		t.Error("per-case differences kept")
	}
	got := plain(t, view).(map[string]any)
	if _, ok := got["noise_floor"].(map[string]any)["stats"].(map[string]any)["latency_ms"].(map[string]any)["differences"]; ok {
		t.Error("differences written")
	}
	if view.Arms[0].Summary.Cases != 15 {
		t.Errorf("arm cases = %d", view.Arms[0].Summary.Cases)
	}
}

func TestEquivalenceReadsAgainstFloorsFirstReplay(t *testing.T) {
	view := mustBuild(t, loadRun(t), BuildViewOptions{})
	mini := Equivalence(view, "").Treatments[0]
	if mini.Latency.Control == nil || *mini.Latency.Control != *view.NoiseFloor.A1.LatencyMsP50 {
		t.Errorf("latency control = %v", mini.Latency.Control)
	}
	if !strings.Contains(mini.Latency.Sentence, "for the control replayed through the same gateway") {
		t.Errorf("latency sentence = %q", mini.Latency.Sentence)
	}
	if len(mini.Fields) != 4 {
		t.Errorf("fields = %d", len(mini.Fields))
	}
}

func TestEquivalenceNamesEndpointWhenAsked(t *testing.T) {
	view := mustBuild(t, loadRun(t), BuildViewOptions{})
	local := Equivalence(view, ViaEndpoint).Treatments[0]
	for _, s := range []string{local.Latency.Sentence, local.Cost.Sentence} {
		if !strings.Contains(s, "for the control replayed through the same endpoint") {
			t.Errorf("sentence = %q", s)
		}
		if strings.Contains(s, "gateway") {
			t.Errorf("sentence names the gateway: %q", s)
		}
	}
}

func TestUnreadableTrackedPathIsRefused(t *testing.T) {
	run := *loadRun(t)
	run.Settings.TrackedFields = []string{"$..deep"}
	_, err := BuildView(&run, BuildViewOptions{})
	if err == nil || !regexp.MustCompile(`not a supported JSON path`).MatchString(err.Error()) {
		t.Errorf("err = %v", err)
	}
}

func TestSummarizeRefusesMisalignedControls(t *testing.T) {
	defer func() {
		r := recover()
		if s, _ := r.(string); !strings.Contains(s, "aligned") {
			t.Errorf("recovered %v", r)
		}
	}()
	Summarize([]any{obj("output", "x")}, []any{})
	t.Error("no panic")
}

func TestWithinNoiseLabelsBeyondWithinAndDirection(t *testing.T) {
	view := mustBuild(t, loadRun(t), BuildViewOptions{})
	labels := NoiseLabelsOf(view, "Mini", nil)
	if labels == nil {
		t.Fatal("no labels")
	}
	byKey := map[string]NoiseLabel{}
	for _, l := range append(append([]NoiseLabel{}, labels.Stats...), labels.Fields...) {
		byKey[l.Key] = l
	}
	deref := func(s *string) string {
		if s == nil {
			return "<nil>"
		}
		return *s
	}
	check := func(what string, got, want any) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %v, want %v", what, got, want)
		}
	}
	check("latency label", byKey["latency_ms_p50"].Label, "beyond_noise")
	check("latency direction", deref(byKey["latency_ms_p50"].Direction), "lower")
	check("latency against", byKey["latency_ms_p50"].Against, "replayed_control")
	check("cost direction", deref(byKey["cost_per_call_usd"].Direction), "lower")
	// Every output keeps production's keys (production's one non-JSON reply is not compared).
	check("same keys label", byKey["same_keys_rate"].Label, "within_noise")
	if g := byKey["same_keys_rate"].Gap; g == nil || *g != 0 {
		t.Errorf("same keys gap = %v", g)
	}
	check("same keys cases", byKey["same_keys_rate"].Cases, 14)
	// Production's one refusal did not parse; Mini's reply there does.
	check("json label", byKey["json_parse_rate"].Label, "beyond_noise")
	check("json direction", deref(byKey["json_parse_rate"].Direction), "higher")
	category := byKey["field:$.category"]
	check("category label", category.Label, "beyond_noise")
	check("category direction", deref(category.Direction), "different")
	check("category cases", category.Cases, 14)
	gap, floorGap := 0.0, 1.0
	if category.Gap != nil {
		gap = *category.Gap
	}
	if category.FloorGap != nil {
		floorGap = *category.FloorGap
	}
	if !(gap > floorGap) {
		t.Errorf("category gap %v not above floor gap %v", gap, floorGap)
	}
	// Mini moves priority on a third of the cases by 1; the floor moves it on two cases by 1.
	check("priority measure", deref(byKey["field:$.priority"].Measure), "median_abs_difference")
	if l := NoiseLabelFor(view, "Mini", "field:$.summary"); l == nil || l.Label != "beyond_noise" {
		t.Errorf("summary label = %+v", l)
	}
}

func TestTreatmentNoFurtherThanFloorIsWithinNoise(t *testing.T) {
	// A treatment that returns exactly the floor's second replay differs from the first by the floor's own gap.
	run := *loadRun(t)
	twin := run.Treatments[0]
	twin.ID, twin.Label = "t3", "Twin"
	run.Treatments = append(append([]types.Treatment{}, run.Treatments...), twin)
	run.Replays = append([]types.Replay{}, run.Replays...)
	for _, r := range loadRun(t).Replays {
		if r.Arm == "floor_a2" {
			r.Arm = "t3"
			run.Replays = append(run.Replays, r)
		}
	}
	view := mustBuild(t, &run, BuildViewOptions{})
	for _, key := range []string{"latency_ms_p50", "cost_per_call_usd"} {
		if l := NoiseLabelFor(view, "Twin", key); l == nil || l.Label != "within_noise" {
			t.Errorf("%s label = %+v", key, l)
		}
	}
}

func TestTooFewPairedCases(t *testing.T) {
	run := *loadRun(t)
	ids := map[string]bool{}
	for _, c := range run.Cases[:6] {
		ids[c.ID] = true
	}
	small := run
	small.Cases = run.Cases[:6]
	small.Replays = nil
	for _, r := range run.Replays {
		if ids[r.CaseID] {
			small.Replays = append(small.Replays, r)
		}
	}
	l := NoiseLabelFor(mustBuild(t, &small, BuildViewOptions{}), "Mini", "latency_ms_p50")
	if l == nil || l.Label != "too_few_calls" || l.Direction != nil || l.Cases != 6 {
		t.Errorf("label = %+v", l)
	}
}
