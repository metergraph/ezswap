package core

// A replay run has no paid "Control, replayed" column; its noise floor's
// first replay (A1) is the control replayed the same way. With
// FloorAsReplay, the stat rows and the grid read latency (and cost) against
// A1, as Equivalence does. The default reads only a paid column, the
// behaviour conformance/page.json pins.

import (
	"testing"
)

func findRow(rows []StatRow, key string) *StatRow {
	for i := range rows {
		if rows[i].Key == key {
			return &rows[i]
		}
	}
	return nil
}

func findMetric(metrics []GridMetric, key string) *GridMetric {
	for i := range metrics {
		if metrics[i].Key == key {
			return &metrics[i]
		}
	}
	return nil
}

func sameText(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func TestByDefaultLatencyIsNotComparedOnReplayRun(t *testing.T) {
	view := mustBuild(t, loadRun(t), BuildViewOptions{})
	latency := findRow(StatRowsOf(view, StatRowsOptions{}), "latency_ms_p50")
	if latency == nil {
		t.Fatal("no latency row")
	}
	for _, cell := range latency.Arms {
		if cell.Delta != nil {
			t.Errorf("%s delta = %s", cell.Label, *cell.Delta)
		}
	}
	if ReplayBaseline(view, BaselineOptions{}) != nil {
		t.Error("a replay baseline without a paid column")
	}
}

func TestFloorAsReplayReadsAgainstFloorsFirstReplay(t *testing.T) {
	view := mustBuild(t, loadRun(t), BuildViewOptions{})
	a1 := view.NoiseFloor.A1
	rows := StatRowsOf(view, StatRowsOptions{BaselineOptions: BaselineOptions{FloorAsReplay: true}})
	latency := findRow(rows, "latency_ms_p50")
	cost := findRow(rows, "cost_per_call_usd")
	if latency == nil || cost == nil {
		t.Fatal("missing rows")
	}
	for i, arm := range view.Arms {
		if want := StatDelta("latency_ms_p50", arm.Summary.LatencyMsP50, a1.LatencyMsP50, "replay"); !sameText(latency.Arms[i].Delta, want) {
			t.Errorf("%s latency delta = %v, want %v", arm.Label, latency.Arms[i].Delta, want)
		}
		if want := StatDelta("cost_per_call_usd", arm.Summary.CostPerCallUSD, a1.CostPerCallUSD, "replay"); !sameText(cost.Arms[i].Delta, want) {
			t.Errorf("%s cost delta = %v, want %v", arm.Label, cost.Arms[i].Delta, want)
		}
		if latency.Arms[i].Delta == nil {
			t.Errorf("%s has no latency delta", arm.Label)
		}
	}
	// The same baseline equivalence uses.
	mini := Equivalence(view, "").Treatments[0]
	if !sameText(mini.Latency.Delta, latency.Arms[0].Delta) {
		t.Errorf("equivalence latency delta = %v", mini.Latency.Delta)
	}
}

func TestFloorAsReplayGivesGridReplayedControlLatency(t *testing.T) {
	view := mustBuild(t, loadRun(t), BuildViewOptions{})
	p := findMetric(GridMetrics(view, GridMetricsOptions{}), "latency_ms_p50")
	if p == nil || p.ControlSource != "" || len(p.Control()) != 0 {
		t.Errorf("plain latency metric = %+v", p)
	}
	metric := findMetric(GridMetrics(view, GridMetricsOptions{FloorAsReplay: true}), "latency_ms_p50")
	if metric == nil || metric.ControlSource != "replay" {
		t.Fatalf("latency metric = %+v", metric)
	}
	control := GridReadingsOf(view, *metric).Control
	if control == nil || *control.Value != *view.NoiseFloor.A1.LatencyMsP50 {
		t.Errorf("control reading = %+v", control)
	}
}

func TestWithoutFloorFloorAsReplayChangesNothing(t *testing.T) {
	view := mustBuild(t, withoutFloor(loadRun(t)), BuildViewOptions{})
	sameJSON(t, StatRowsOf(view, StatRowsOptions{BaselineOptions: BaselineOptions{FloorAsReplay: true}}), StatRowsOf(view, StatRowsOptions{}), "stat rows")
	if m := GridMetrics(view, GridMetricsOptions{FloorAsReplay: true}); m[1].ControlSource != "" {
		t.Errorf("control source = %q", m[1].ControlSource)
	}
}
