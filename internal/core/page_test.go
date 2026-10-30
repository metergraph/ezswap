package core

// conformance/page.json pins the view statistics (stat rows, spreads, floor
// readings, sentences, the summary table, grid ranges, the field tree) to
// Metergraph's hosted figures for the same views. A few parts exist only in
// the hosted product, and the comparison leaves exactly those out: the
// cached-input footnote, the monthly note's wording, and the judge and
// similarity columns.

import (
	"testing"
)

var hostedOnly = map[string]bool{"embedding_similarity": true, "grade_mean": true}

func withoutHostedOnly(expected map[string]any) map[string]any {
	for _, key := range []string{"stat_rows", "stat_rows_monthly"} {
		for _, r := range expected[key].([]any) {
			row := r.(map[string]any)
			if row["key"] == "cached_input_share" {
				delete(row, "note")
			}
			if note, ok := row["note"].(map[string]any); ok && row["key"] == "monthly_cost_usd" {
				delete(note, "text")
			}
		}
	}
	if summary, ok := expected["equivalence_summary"].(map[string]any); ok {
		var columns []any
		for _, c := range summary["columns"].([]any) {
			if !hostedOnly[c.(map[string]any)["key"].(string)] {
				columns = append(columns, c)
			}
		}
		summary["columns"] = columns
		floored := false
		for _, r := range summary["rows"].([]any) {
			cells := r.(map[string]any)["cells"].(map[string]any)
			for key := range hostedOnly {
				delete(cells, key)
			}
			// Whether any kept column carries a floor, as the summary itself decides it.
			for _, cell := range cells {
				if _, ok := cell.(map[string]any)["floor"]; ok {
					floored = true
				}
			}
		}
		summary["floored"] = floored
	}
	grid := expected["grid"].(map[string]any)
	for key := range hostedOnly {
		delete(grid, key)
	}
	return expected
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func page(t *testing.T, experiment any) map[string]any {
	var tracked []string
	for _, p := range list(at(experiment, "tracked_fields")) {
		tracked = append(tracked, str(p))
	}
	var labels []string
	for _, a := range list(at(experiment, "arms")) {
		labels = append(labels, str(at(a, "label")))
	}
	floors := NoiseFloors(experiment, tracked)
	fields := map[string]any{}
	for _, path := range tracked {
		kind, comparisons := FieldComparisons(at(experiment, "cases"), path, labels, at(experiment, "fields"))
		floorKind := kind
		if kind == KindMixed {
			floorKind = ""
		}
		floor := FloorField(experiment, path, floorKind)
		sentences := []string{}
		for i := range comparisons {
			sentences = append(sentences, FieldSentence(&comparisons[i], floor))
		}
		fields[path] = map[string]any{"kind": kind, "comparisons": comparisons, "floor": floor, "sentences": sentences}
	}
	grid := map[string]any{}
	for _, metric := range GridMetrics(experiment, GridMetricsOptions{Tracked: tracked}) {
		var frontier []any
		for _, p := range Frontier(FrontierPoints(experiment, metric, nil), metric.Better) {
			frontier = append(frontier, p.Label)
		}
		if frontier == nil {
			frontier = []any{}
		}
		grid[metric.Key] = map[string]any{
			"better":    nullable(metric.Better),
			"statistic": metric.Statistic,
			"readings":  GridReadingsOf(experiment, metric),
			"frontier":  frontier,
		}
	}
	root := FieldTree(at(experiment, "cases"), tracked)
	rows := StatRowsOf(experiment, StatRowsOptions{})
	monthly := plain(t, StatRowsOf(experiment, StatRowsOptions{Volume: &MonthlyVolume{Calls: 12000, SpendUSD: ptr(30.0)}}))
	for _, r := range monthly.([]any) {
		row := r.(map[string]any)
		if note, ok := row["note"].(map[string]any); ok && row["key"] == "monthly_cost_usd" {
			delete(note, "text")
		}
	}
	focus := tracked
	if len(focus) > 1 {
		focus = focus[:1]
	}
	var summary any
	if s := EquivalenceSummary(experiment, tracked, floors); s != nil {
		summary = s
	}
	var layout any
	if l := GridLayoutOf(experiment); l != nil {
		layout = l
	}
	return plain(t, map[string]any{
		"stat_rows":           rows,
		"stat_rows_monthly":   monthly,
		"stat_spreads":        StatSpreads(experiment),
		"noise_floors":        floors,
		"fields":              fields,
		"equivalence_summary": summary,
		"grid_layout":         layout,
		"grid":                grid,
		"field_tree":          root,
		"initial_open":        InitialOpen(root, focus),
		"suggested_fields":    SuggestedFields(at(experiment, "cases"), 0),
		"control_summary":     ControlSummary(experiment),
	}).(map[string]any)
}

func TestViewStatisticsMatchHostedFigures(t *testing.T) {
	f := fixture(t, "page.json")
	views := at(f, "views")
	for _, name := range keysOf(views) {
		t.Run(name, func(t *testing.T) {
			view := at(views, name, "view")
			actual := page(t, view)
			want := withoutHostedOnly(plain(t, at(views, name, "expected")).(map[string]any))
			for key, value := range want {
				if d := firstDifference(key, actual[key], value); d != "" {
					t.Errorf("%s", d)
				}
			}
		})
	}
}

func TestBuildViewStillBuildsThePageView(t *testing.T) {
	f := fixture(t, "page.json")
	sameJSON(t, mustBuild(t, loadRun(t), BuildViewOptions{}), at(f, "views", "replay_run", "view"), "buildView")
}

func TestBootstrapRangesAreSeededAndMatch(t *testing.T) {
	f := fixture(t, "page.json")
	cases := list(at(f, "bootstrap"))
	if len(cases) == 0 {
		t.Fatal("no bootstrap cases")
	}
	for _, c := range cases {
		got := BootstrapInterval(list(at(c, "values")), str(at(c, "statistic")), BootstrapOptions{})
		var value any
		if got != nil {
			value = got
		}
		sameJSON(t, value, at(c, "expected"), Stringify(at(c, "values"), ""))
	}
}
