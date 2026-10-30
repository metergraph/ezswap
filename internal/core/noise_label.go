package core

// Within-noise labels (opt-in): for each statistic and tracked field, is a
// treatment's gap from the control larger than the control's gap from
// itself (A2 against A1) on the same cases? A reading aid that says whether a
// difference stands out from replay-to-replay variation. It is never a
// verdict that a model is safe to switch to. docs/noise-floor.md describes
// the method.

import (
	"math"
	"strings"
)

// LabelMinCases is LABEL_MIN_CASES: below this many paired cases a label
// would be mostly noise itself.
const LabelMinCases = SpreadMin

// NoiseLabelValue is "within_noise", "beyond_noise", "too_few_calls" or "not_comparable".
type NoiseLabelValue = string

// NoiseLabel is one statistic's or tracked field's within-noise label.
type NoiseLabel struct {
	// A built-in statistic's key, or `field:<path>`.
	Key   string          `json:"key"`
	Label NoiseLabelValue `json:"label"`
	// Which way the treatment moved, for beyond_noise: "higher" or "lower"
	// for numbers and rates, "different" for values with no order.
	Direction *string `json:"direction"`
	// How the gap is measured: "median_abs_difference", "disagreement_rate" or "mean_non_overlap".
	Measure *string `json:"measure"`
	// Paired cases: the treatment, its baseline and both floor replays all have a value.
	Cases int `json:"cases"`
	// The treatment's gap from its baseline over those cases.
	Gap *float64 `json:"gap"`
	// The floor's gap (A2 against A1) over the same cases.
	FloorGap *float64 `json:"floor_gap"`
	// What the treatment is read against: "production", or "replayed_control" (the floor's first replay).
	Against string `json:"against"`
}

type pairedCase struct {
	control, treatment, a1, a2 any
	index                      int
}

// pairedCases is the replayed cases where the treatment and both floor replays returned.
func pairedCases(view any, armLabel string) []pairedCase {
	cases := list(at(view, "cases"))
	indexes := ReplayedIndexes(cases)
	var out []pairedCase
	for i, c := range cases {
		if !replayedCase(c) {
			continue
		}
		treatment := at(c, "arms", armLabel)
		a1 := at(c, "noise_floor", "a1")
		a2 := at(c, "noise_floor", "a2")
		control := at(c, "control")
		if !truthy(control) || !truthy(treatment) || truthy(at(treatment, "error")) || !truthy(a1) || truthy(at(a1, "error")) || !truthy(a2) || truthy(at(a2, "error")) {
			continue
		}
		out = append(out, pairedCase{control, treatment, a1, a2, indexes[i]})
	}
	return out
}

func labelMedian(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := sortNumbers(values)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return ptr(sorted[mid])
	}
	return ptr((sorted[mid-1] + sorted[mid]) / 2)
}

func decide(key, against, measure string, n int, gap, floorGap *float64, direction string) NoiseLabel {
	l := NoiseLabel{Key: key, Measure: ptr(measure), Cases: n, Gap: gap, FloorGap: floorGap, Against: against}
	if n < LabelMinCases || gap == nil || floorGap == nil {
		l.Label = "too_few_calls"
		return l
	}
	if *gap <= *floorGap {
		l.Label = "within_noise"
		return l
	}
	l.Label = "beyond_noise"
	l.Direction = ptr(direction)
	return l
}

type numberRow struct{ t, c, a1, a2 float64 }

// numberLabel reads numbers: the median size of the per-case difference,
// treatment against baseline and A2 against A1.
func numberLabel(key, against string, rows []numberRow) NoiseLabel {
	gaps := make([]float64, len(rows))
	floorGaps := make([]float64, len(rows))
	shifts := make([]float64, len(rows))
	for i, r := range rows {
		gaps[i] = math.Abs(r.t - r.c)
		floorGaps[i] = math.Abs(r.a2 - r.a1)
		shifts[i] = r.t - r.c
	}
	shift := labelMedian(shifts)
	direction := "different"
	if shift != nil && *shift != 0 {
		if *shift > 0 {
			direction = "higher"
		} else {
			direction = "lower"
		}
	}
	return decide(key, against, "median_abs_difference", len(rows), labelMedian(gaps), labelMedian(floorGaps), direction)
}

type valueRow struct{ t, c, a1, a2 any }

// matchLabel reads values compared for equality: the share of cases where
// the two sides disagree.
func matchLabel(key string, rows []valueRow, same func(x, y any) bool, ordered bool) NoiseLabel {
	n := len(rows)
	var gap, floorGap *float64
	if n > 0 {
		g, f := 0, 0
		for _, r := range rows {
			if !same(r.t, r.c) {
				g++
			}
			if !same(r.a2, r.a1) {
				f++
			}
		}
		gap = ptr(float64(g) / float64(n))
		floorGap = ptr(float64(f) / float64(n))
	}
	direction := "different"
	if ordered {
		shift := 0
		for _, r := range rows {
			if r.t == true {
				shift++
			}
			if r.c == true {
				shift--
			}
		}
		if shift > 0 {
			direction = "higher"
		} else if shift < 0 {
			direction = "lower"
		}
	}
	return decide(key, "production", "disagreement_rate", n, gap, floorGap, direction)
}

func presentRow(r valueRow) bool { return r.t != nil && r.c != nil && r.a1 != nil && r.a2 != nil }

type numberStatDef struct {
	key     string
	read    func(r any) any
	against string
}

var numberStatDefs = []numberStatDef{
	{"cost_per_call_usd", func(r any) any { return at(r, "cost_usd") }, "replayed_control"},
	{"latency_ms_p50", func(r any) any { return at(r, "latency_ms") }, "replayed_control"},
	{"output_tokens_p50", func(r any) any { return at(r, "output_tokens") }, "production"},
	{"output_chars_p50", func(r any) any {
		if truthy(at(r, "output_truncated")) {
			return nil
		}
		s, _ := at(r, "output").(string)
		return float64(codePointLength(s))
	}, "production"},
}

type matchStatDef struct {
	key     string
	read    func(r any) any
	ordered bool
}

var matchStatDefs = []matchStatDef{
	{"json_parse_rate", func(r any) any { _, ok := ParseOutputJSON(at(r, "output")); return ok }, true},
	{"same_keys_rate", func(r any) any {
		if truthy(at(r, "output_truncated")) {
			return nil
		}
		facts := OutputFacts(at(r, "output"), nil)
		if !keysComplete(facts) {
			return nil
		}
		var keys []string
		for _, k := range list(at(facts, "keys")) {
			keys = append(keys, jsString(k))
		}
		quoted := make([]string, 0, len(keys))
		for _, k := range pySorted(keys) {
			quoted = append(quoted, jsonQuote(k))
		}
		return "[" + strings.Join(quoted, ",") + "]"
	}, false},
	{"empty_or_refused_rate", func(r any) any {
		return IsEmptyOrRefused(at(r, "output"), at(r, "finish_reason"), truthy(at(r, "refusal")))
	}, true},
	{"cut_off_rate", func(r any) any { return IsCutOff(at(r, "finish_reason")) }, true},
}

// statLabels is the labels for every built-in statistic, for one treatment.
func statLabels(paired []pairedCase) []NoiseLabel {
	var labels []NoiseLabel
	for _, def := range numberStatDefs {
		var rows []numberRow
		for _, p := range paired {
			baseline := p.control
			if def.against == "replayed_control" {
				baseline = p.a1
			}
			t, okT := finiteNum(def.read(p.treatment))
			c, okC := finiteNum(def.read(baseline))
			a1, ok1 := finiteNum(def.read(p.a1))
			a2, ok2 := finiteNum(def.read(p.a2))
			if okT && okC && ok1 && ok2 {
				rows = append(rows, numberRow{t, c, a1, a2})
			}
		}
		labels = append(labels, numberLabel(def.key, def.against, rows))
	}
	for _, def := range matchStatDefs {
		var rows []valueRow
		for _, p := range paired {
			r := valueRow{def.read(p.treatment), def.read(p.control), def.read(p.a1), def.read(p.a2)}
			if presentRow(r) {
				rows = append(rows, r)
			}
		}
		labels = append(labels, matchLabel(def.key, rows, strictEqual, def.ordered))
	}
	return labels
}

// fieldLabel is the label for one tracked field, read with the same kind and
// values the field's sentence uses.
func fieldLabel(view any, paired []pairedCase, path, armLabel string) NoiseLabel {
	var labels []string
	for _, arm := range list(at(view, "arms")) {
		labels = append(labels, jsString(at(arm, "label")))
	}
	kind, _ := FieldComparisons(at(view, "cases"), path, labels, at(view, "fields"))
	floorSeries := at(view, "noise_floor", "fields", path)
	var rows []valueRow
	for _, p := range paired {
		r := valueRow{
			t:  SideValue(p.treatment, path, at(view, "fields", path, armLabel), p.index),
			c:  SideValue(p.control, path, at(view, "fields", path, "control"), p.index),
			a1: SideValue(p.a1, path, at(floorSeries, "a1"), p.index),
			a2: SideValue(p.a2, path, at(floorSeries, "a2"), p.index),
		}
		if presentRow(r) {
			rows = append(rows, r)
		}
	}
	key := "field:" + path
	switch kind {
	case KindNumber:
		var numeric []numberRow
		for _, r := range rows {
			t, okT := num(r.t)
			c, okC := num(r.c)
			a1, ok1 := num(r.a1)
			a2, ok2 := num(r.a2)
			if okT && okC && ok1 && ok2 {
				numeric = append(numeric, numberRow{t, c, a1, a2})
			}
		}
		return numberLabel(key, "production", numeric)
	case KindLabel, KindText, KindLongText:
		return matchLabel(key, rows, trimmedEqual, false)
	case KindList:
		var lists []valueRow
		for _, r := range rows {
			if isArray(r.t) && isArray(r.c) && isArray(r.a1) && isArray(r.a2) {
				lists = append(lists, r)
			}
		}
		n := len(lists)
		var gap, floorGap *float64
		if n > 0 {
			g, f := 0.0, 0.0
			for _, r := range lists {
				g += 1 - Overlap(r.t, r.c)
			}
			for _, r := range lists {
				f += 1 - Overlap(r.a2, r.a1)
			}
			gap, floorGap = ptr(g/float64(n)), ptr(f/float64(n))
		}
		return decide(key, "production", "mean_non_overlap", n, gap, floorGap, "different")
	}
	return NoiseLabel{Key: key, Label: "not_comparable", Cases: len(rows), Against: "production"}
}

// NoiseLabels is one treatment's within-noise labels.
type NoiseLabels struct {
	Arm    string       `json:"arm"`
	Stats  []NoiseLabel `json:"stats"`
	Fields []NoiseLabel `json:"fields"`
}

// NoiseLabelsOf is noiseLabels: the within-noise label for every built-in
// statistic and tracked field, for one treatment. A nil tracked is the
// view's tracked fields. Nil when the run has no noise floor.
func NoiseLabelsOf(view any, armLabel string, tracked []string) *NoiseLabels {
	view = toLoose(view)
	if !truthy(at(view, "noise_floor")) {
		return nil
	}
	paired := pairedCases(view, armLabel)
	paths := tracked
	if paths == nil {
		for _, p := range list(at(view, "tracked_fields")) {
			paths = append(paths, jsString(p))
		}
	}
	out := &NoiseLabels{Arm: armLabel, Stats: statLabels(paired), Fields: []NoiseLabel{}}
	for _, path := range paths {
		out.Fields = append(out.Fields, fieldLabel(view, paired, path, armLabel))
	}
	return out
}

// NoiseLabelFor is noiseLabel: one label, by a built-in statistic's key (as
// the summary names it) or `field:<path>`. Nil when there is none.
func NoiseLabelFor(view any, armLabel, key string) *NoiseLabel {
	tracked := []string{}
	if path, ok := strings.CutPrefix(key, "field:"); ok {
		tracked = []string{path}
	}
	all := NoiseLabelsOf(view, armLabel, tracked)
	if all == nil {
		return nil
	}
	for _, l := range append(append([]NoiseLabel{}, all.Stats...), all.Fields...) {
		if l.Key == key {
			return &l
		}
	}
	return nil
}
