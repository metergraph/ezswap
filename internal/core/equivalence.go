package core

// Per treatment, what reads together when deciding a swap: each tracked
// field's shift, cost and latency against the control over the same cases,
// and how many cases that is. Matches Metergraph's hosted figures;
// conformance/experiment_equivalence*.json pin it. Shown, never judged.

import (
	"math"
)

// SampleNote is sampleNote: a plain note when a treatment returned on too
// few cases for a range. Nil otherwise.
func SampleNote(n any) *string {
	v := toLoose(n)
	if v == nil || jsNumber(v) >= SpreadMin {
		return nil
	}
	if strictEqual(v, 0.0) {
		return ptr("No case has returned on both sides yet.")
	}
	word := "cases"
	if strictEqual(v, 1.0) {
		word = "case"
	}
	return ptr("Only " + FormatCount(v) + " " + word + ", too few for a range (a range needs 10).")
}

// Against is one built-in stat of an arm against its baseline.
type Against struct {
	Value    *float64 `json:"value"`
	Control  *float64 `json:"control"`
	Delta    *string  `json:"delta"`
	Sentence string   `json:"sentence"`
}

type againstDef struct {
	format func(any) string
	what   string
	says   func(string) string
}

var againstControlDefs = map[string]againstDef{
	"cost_per_call_usd": {FormatUSD, "cost per call", func(v string) string { return "costs " + v + " a call" }},
	"latency_ms_p50":    {FormatMs, "latency", func(v string) string { return "takes a median " + v }},
}

// templateText is a value in a template literal: `${x}`, "undefined" when missing.
func templateText(v any, present bool) string {
	if !present {
		return "undefined"
	}
	return jsString(v)
}

func labelText(arm any) string {
	v, ok := get(arm, "label")
	return templateText(v, ok)
}

// AgainstControl is againstControl: one built-in stat of an arm against the
// control over the cases the arm returned on. Key is "cost_per_call_usd" or
// "latency_ms_p50".
func AgainstControl(experiment, arm any, key string) Against {
	experiment, arm = toLoose(experiment), toLoose(arm)
	def := againstControlDefs[key]
	matched := at(arm, "control_summary")
	value := at(arm, "summary", key)
	var base, cases any
	if truthy(matched) {
		base, cases = at(matched, key), at(matched, "cases")
	} else {
		control := ControlSummary(experiment)
		base, cases = at(control, key), at(control, "cases")
	}
	delta := StatDelta(key, value, base, "")
	var sentence string
	if value == nil || base == nil {
		sentence = labelText(arm) + " has no " + def.what + " to compare with control yet."
	} else {
		over := ""
		if cases != nil {
			word := "cases"
			if f, _ := num(cases); f == 1 {
				word = "case"
			}
			over = " over the same " + jsString(cases) + " " + word
		}
		shown := ""
		if delta != nil {
			d := *delta
			if d == "same" {
				d = "the same"
			}
			shown = " (" + d + ")"
		}
		sentence = labelText(arm) + " " + def.says(def.format(value)) + " against " + def.format(base) + " for control" + over + shown + "."
	}
	return Against{Value: numPtr(value), Control: numPtr(base), Delta: delta, Sentence: sentence}
}

// FloorSummary is the control against itself on one field, from the view's
// noise floor: aggregates only.
type FloorSummary struct {
	Kind          FieldKind `json:"kind"`
	Pairs         float64   `json:"pairs"`
	Matched       *float64  `json:"matched"`
	MatchRate     *float64  `json:"match_rate"`
	MeanOverlap   *float64  `json:"mean_overlap"`
	MinDifference *float64  `json:"min_difference"`
	MaxDifference *float64  `json:"max_difference"`
}

func floorSummary(spread any, kind FieldKind) *FloorSummary {
	pairs, _ := get(spread, "pairs")
	k, _ := get(spread, "kind")
	if !truthy(spread) || !truthy(pairs) || k != string(kind) {
		return nil
	}
	return &FloorSummary{
		Kind:          kind,
		Pairs:         jsNumber(pairs),
		Matched:       numPtr(at(spread, "matched")),
		MatchRate:     numPtr(at(spread, "match_rate")),
		MeanOverlap:   numPtr(at(spread, "mean_overlap")),
		MinDifference: numPtr(at(spread, "min_difference")),
		MaxDifference: numPtr(at(spread, "max_difference")),
	}
}

// asFloor is the summary in the shape FloorClause reads ({kind, pairs,
// matched, overlap, diffBox}).
func asFloor(summary *FloorSummary) *FloorReading {
	if summary == nil {
		return nil
	}
	f := &FloorReading{Kind: summary.Kind, Pairs: int(summary.Pairs)}
	if summary.Matched != nil {
		f.Matched = ptr(int(*summary.Matched))
	}
	f.Overlap = summary.MeanOverlap
	if summary.MinDifference != nil && summary.MaxDifference != nil {
		f.DiffBox = &BoxStats{Min: *summary.MinDifference, Max: *summary.MaxDifference}
	}
	return f
}

// FieldShift is one tracked field's shift for one treatment.
type FieldShift struct {
	Path                string        `json:"path"`
	Kind                FieldKind     `json:"kind"`
	Pairs               int           `json:"pairs"`
	Missing             int           `json:"missing"`
	Matched             *int          `json:"matched"`
	Rate                *float64      `json:"rate"`
	Overlap             *float64      `json:"overlap"`
	MedianDifference    *float64      `json:"median_difference"`
	MiddleHalvesOverlap *bool         `json:"middle_halves_overlap"`
	Sentence            string        `json:"sentence"`
	NoiseFloor          *FloorSummary `json:"noise_floor"`
}

func fieldSummary(comparison *FieldComparison, floor *FloorSummary) FieldShift {
	number := comparison.Kind == KindNumber && comparison.Pairs > 0
	s := FieldShift{
		Path:       comparison.Path,
		Kind:       comparison.Kind,
		Pairs:      comparison.Pairs,
		Missing:    comparison.Missing,
		Matched:    comparison.Matched,
		Rate:       comparison.Rate,
		Overlap:    comparison.Overlap,
		Sentence:   FieldSentence(comparison, asFloor(floor)),
		NoiseFloor: floor,
	}
	if number {
		if comparison.DiffBox != nil {
			s.MedianDifference = ptr(comparison.DiffBox.Median)
		}
		s.MiddleHalvesOverlap = ptr(MiddleHalvesOverlap(comparison.ControlBox, comparison.ArmBox))
	}
	return s
}

// replaySummary is the control replayed the same way as a treatment: a paid
// column, else the noise floor's first replay.
func replaySummary(experiment any) any {
	for _, a := range list(at(experiment, "arms")) {
		if l, _ := get(a, "label"); l == ReplayedControlLabel && truthy(at(a, "summary")) {
			return at(a, "summary")
		}
	}
	if a1 := at(experiment, "noise_floor", "a1"); truthy(a1) {
		return a1
	}
	return nil
}

// ReplayVia is what the control was replayed through, as the replay
// sentences name it. "gateway" is the hosted wording
// (conformance/experiment_equivalence_floor.json); a local run replays
// through the user's own "endpoint".
type ReplayVia string

const (
	ViaGateway  ReplayVia = "gateway"
	ViaEndpoint ReplayVia = "endpoint"
)

func againstReplay(arm any, key string, replay any, via ReplayVia) Against {
	def := againstControlDefs[key]
	value := at(arm, "summary", key)
	base := at(replay, key)
	delta := StatDelta(key, value, base, "replay")
	var sentence string
	if value == nil || base == nil {
		sentence = labelText(arm) + " has no " + def.what + " to compare with the replayed control yet."
	} else {
		shown := ""
		if delta != nil {
			d := *delta
			if d == "same" {
				d = "the same"
			}
			shown = " (" + d + ")"
		}
		sentence = labelText(arm) + " " + def.says(def.format(value)) + " against " + def.format(base) + " for the control replayed through the same " + string(via) + shown + "."
	}
	return Against{Value: numPtr(value), Control: numPtr(base), Delta: delta, Sentence: sentence}
}

func unreplayedLatency(arm any) Against {
	value := at(arm, "summary", "latency_ms_p50")
	sentence := labelText(arm) + " has no latency yet."
	if value != nil {
		sentence = labelText(arm) + " takes a median " + FormatMs(value) + ". Production's recorded latency is not timed the way a replay is, so it is not compared; replay the control to compare latency."
	}
	return Against{Value: numPtr(value), Sentence: sentence}
}

// Similarity is a hosted similarity column; replay computes none.
type Similarity struct {
	Median        *float64 `json:"median"`
	N             *float64 `json:"n"`
	ControlMedian *float64 `json:"control_median"`
}

// TreatmentEquivalence is one treatment's equivalence block.
type TreatmentEquivalence struct {
	Label      string       `json:"label"`
	Cases      *float64     `json:"cases"`
	SampleNote *string      `json:"sample_note"`
	Cost       Against      `json:"cost"`
	Latency    Against      `json:"latency"`
	Fields     []FieldShift `json:"fields"`
	Similarity *Similarity  `json:"similarity"`
}

// EquivalenceResult is equivalence's result.
type EquivalenceResult struct {
	Treatments []TreatmentEquivalence `json:"treatments"`
}

// Equivalence is equivalence: per treatment, cost and latency against the
// control replayed the same way (or, without one, cost against production
// and latency alone), and each tracked field's shift with the noise floor's
// gap beside it. An empty via is "gateway".
func Equivalence(experiment any, via ReplayVia) EquivalenceResult {
	if via == "" {
		via = ViaGateway
	}
	experiment = toLoose(experiment)
	cases := at(experiment, "cases")
	arms := list(at(experiment, "arms"))
	labels := make([]string, len(arms))
	for i, arm := range arms {
		labels[i] = armLabel(arm)
	}
	var tracked []string
	for _, p := range list(at(experiment, "tracked_fields")) {
		tracked = append(tracked, jsString(p))
	}
	byPath := make([][]FieldComparison, len(tracked))
	for j, path := range tracked {
		_, byPath[j] = FieldComparisons(cases, path, labels, at(experiment, "fields"))
	}
	floorFields := at(experiment, "noise_floor", "fields")
	replay := replaySummary(experiment)
	similarity := at(experiment, "similarity", "columns")
	controlSimilarity := at(similarity, "control")
	treatments := []TreatmentEquivalence{}
	for i, arm := range arms {
		// The replayed control is the baseline, not a treatment.
		if l, _ := get(arm, "label"); l == ReplayedControlLabel {
			continue
		}
		n := at(arm, "control_summary", "cases")
		column := at(similarity, labels[i])
		t := TreatmentEquivalence{
			Label:      labels[i],
			Cases:      numPtr(n),
			SampleNote: SampleNote(n),
			Fields:     []FieldShift{},
		}
		if replay != nil {
			t.Cost = againstReplay(arm, "cost_per_call_usd", replay, via)
			t.Latency = againstReplay(arm, "latency_ms_p50", replay, via)
		} else {
			t.Cost = AgainstControl(experiment, arm, "cost_per_call_usd")
			t.Latency = unreplayedLatency(arm)
		}
		for j, comparisons := range byPath {
			comparison := comparisons[i]
			var floor *FloorSummary
			if comparison.Kind != KindMixed {
				spread, _ := get(floorFields, tracked[j])
				floor = floorSummary(spread, comparison.Kind)
			}
			t.Fields = append(t.Fields, fieldSummary(&comparison, floor))
		}
		if truthy(at(column, "n")) {
			t.Similarity = &Similarity{numPtr(at(column, "median")), numPtr(at(column, "n")), numPtr(at(controlSimilarity, "median"))}
		}
		treatments = append(treatments, t)
	}
	return EquivalenceResult{Treatments: treatments}
}

// --- The summary table ----------------------------------------------------------

// SummaryNMin is SUMMARY_N_MIN: below this many cases with a result a range
// is mostly noise, so the row says so.
const SummaryNMin = SpreadMin

// FewTracesNote is fewTracesNote.
func FewTracesNote(n *float64) *string {
	if n == nil || *n >= SummaryNMin {
		return nil
	}
	traces := FormatCount(*n) + " traces"
	if *n == 1 {
		traces = "1 trace"
	}
	return ptr(traces + " returned a result. Ranges need at least 10, so none are drawn for this treatment.")
}

// SummaryCell is one figure in the summary table. HasDelta says whether the
// cell carries a delta at all (Delta may still be nil).
type SummaryCell struct {
	Text     string
	Sub      *string
	Delta    *string
	HasDelta bool
	Title    *string
	Floor    *FloorNote
}

// MarshalJSON writes the cell in the page's shape.
func (c SummaryCell) MarshalJSON() ([]byte, error) {
	o := obj("text", c.Text)
	if c.Sub != nil {
		o.Set("sub", *c.Sub)
	}
	if c.HasDelta {
		o.Set("delta", toLoose(c.Delta))
	}
	if c.Title != nil {
		o.Set("title", *c.Title)
	}
	if c.Floor != nil {
		o.Set("floor", c.Floor)
	}
	return o.MarshalJSON()
}

// fieldShift is one tracked field's shift for one arm, in the fewest words
// that keep the field's own reading.
func fieldShift(c *FieldComparison) SummaryCell {
	if c == nil || c.Pairs == 0 {
		return SummaryCell{Text: MissingValue, Title: ptr("No case has a value on both sides yet.")}
	}
	title := ptr(ShiftSentence(c, nil))
	switch c.Kind {
	case KindNumber:
		var median any
		if c.DiffBox != nil {
			median = c.DiffBox.Median
		}
		return SummaryCell{Text: FormatSigned(median, nil), Sub: ptr("median"), Title: title}
	case KindLabel:
		return SummaryCell{Text: FormatCount(c.Matched) + " of " + FormatCount(c.Pairs), Sub: ptr("same label"), Title: title}
	case KindText, KindLongText:
		return SummaryCell{Text: FormatCount(c.Matched) + " of " + FormatCount(c.Pairs), Sub: ptr("exact match"), Title: title}
	case KindList:
		return SummaryCell{Text: FormatPercent(c.Overlap), Sub: ptr("items shared"), Title: title}
	}
	return SummaryCell{Text: MissingValue, Title: title}
}

func sameAsControl(delta *string) *string {
	if delta == nil || *delta == "" {
		return nil
	}
	if *delta == "same" {
		return ptr("same as control")
	}
	return delta
}

// armN is how many of the replayed cases gave this arm a result.
func armN(arm any) *float64 {
	s := at(arm, "summary")
	cases := at(s, "cases")
	if cases == nil {
		return nil
	}
	errors := 0.0
	if e := at(s, "errors"); truthy(e) {
		errors = jsNumber(e)
	}
	return ptr(math.Max(0, jsNumber(cases)-errors))
}

// SummaryTarget names the detail a summary column summarises.
type SummaryTarget struct {
	Field string `json:"field,omitempty"`
	Stat  string `json:"stat,omitempty"`
}

// SummaryColumn is one column of the summary table.
type SummaryColumn struct {
	Key    string        `json:"key"`
	Label  string        `json:"label"`
	Mono   bool          `json:"mono,omitempty"`
	Target SummaryTarget `json:"target"`
	Group  string        `json:"group,omitempty"`
}

// SummaryRow is one treatment's row.
type SummaryRow struct {
	Label string                 `json:"label"`
	Model any                    `json:"model"`
	Index int                    `json:"index"`
	N     *float64               `json:"n"`
	Note  *string                `json:"note"`
	Cells map[string]SummaryCell `json:"cells"`
}

// EquivalenceSummaryTable is the summary at the top of the results.
type EquivalenceSummaryTable struct {
	Columns []SummaryColumn `json:"columns"`
	Rows    []SummaryRow    `json:"rows"`
	Floored bool            `json:"floored"`
}

// EquivalenceSummary is equivalenceSummary: the summary at the top of the
// results, one row per treatment, one figure per thing a swap is decided on,
// each the treatment's shift from the control and, when given, the
// control's shift from itself (`floors`, from NoiseFloors). Cost and latency
// close the row. Every cell names the detail it summarises (`target`).
// There are no judge or similarity columns. Nil with no treatments.
func EquivalenceSummary(experiment any, tracked []string, floors map[string]*FloorNote) *EquivalenceSummaryTable {
	experiment = toLoose(experiment)
	arms := list(at(experiment, "arms"))
	var treatments []any
	for _, arm := range arms {
		if !truthy(at(arm, "replayed_control")) {
			treatments = append(treatments, arm)
		}
	}
	if len(treatments) == 0 {
		return nil
	}
	labels := make([]string, len(arms))
	for i, arm := range arms {
		labels[i] = armLabel(arm)
	}
	cases := at(experiment, "cases")
	builtIn := map[string]StatRow{}
	for _, row := range BuiltInRows(experiment, BaselineOptions{}) {
		builtIn[row.Key] = row
	}
	hasKeys := false
	for _, arm := range treatments {
		if at(arm, "summary", "same_keys_rate") != nil {
			hasKeys = true
		}
	}
	type fieldSet struct {
		path        string
		comparisons []FieldComparison
	}
	fields := make([]fieldSet, len(tracked))
	for i, path := range tracked {
		_, comparisons := FieldComparisons(cases, path, labels, at(experiment, "fields"))
		fields[i] = fieldSet{path, comparisons}
	}

	columns := []SummaryColumn{}
	for _, f := range fields {
		columns = append(columns, SummaryColumn{Key: "field:" + f.path, Label: f.path, Mono: true, Target: SummaryTarget{Field: f.path}})
	}
	if hasKeys {
		columns = append(columns, SummaryColumn{Key: "same_keys_rate", Label: "JSON keys kept", Target: SummaryTarget{Stat: "same_keys_rate"}})
	}
	columns = append(columns,
		SummaryColumn{Key: "output_chars_p50", Label: "Output length", Target: SummaryTarget{Stat: "output_chars_p50"}},
		SummaryColumn{Key: "cost_per_call_usd", Label: "Cost per call", Target: SummaryTarget{Stat: "cost_per_call_usd"}, Group: "cost"},
		SummaryColumn{Key: "latency_ms_p50", Label: "Latency", Target: SummaryTarget{Stat: "latency_ms_p50"}, Group: "cost"},
	)

	rows := []SummaryRow{}
	for i, arm := range arms {
		if truthy(at(arm, "replayed_control")) {
			continue
		}
		cells := map[string]SummaryCell{}
		for _, f := range fields {
			var c *FieldComparison
			if i < len(f.comparisons) {
				c = &f.comparisons[i]
			}
			cells["field:"+f.path] = fieldShift(c)
		}
		builtInCell := func(key string) SummaryCell {
			if row, ok := builtIn[key]; ok && i < len(row.Arms) {
				cell := row.Arms[i]
				return SummaryCell{Text: cell.Text, Delta: sameAsControl(cell.Delta), HasDelta: true}
			}
			return SummaryCell{Text: MissingValue}
		}
		if hasKeys {
			if c := SameKeysCellOf(at(arm, "summary")); c != nil {
				cells["same_keys_rate"] = SummaryCell{Text: c.Text, Sub: c.Sub, Title: ptr(c.Title)}
			} else {
				cells["same_keys_rate"] = SummaryCell{Text: MissingValue}
			}
		}
		withTitle := func(c SummaryCell, title string) SummaryCell { c.Title = ptr(title); return c }
		cells["output_chars_p50"] = withTitle(builtInCell("output_chars_p50"), "Median output length in characters")
		cells["cost_per_call_usd"] = withTitle(builtInCell("cost_per_call_usd"), "Mean cost per call")
		cells["latency_ms_p50"] = withTitle(builtInCell("latency_ms_p50"), "Median latency")
		for _, column := range columns {
			if floor := floors[column.Key]; floor != nil {
				if cell, ok := cells[column.Key]; ok {
					cell.Floor = floor
					cells[column.Key] = cell
				}
			}
		}
		n := armN(arm)
		model, _ := get(arm, "model")
		rows = append(rows, SummaryRow{Label: labels[i], Model: model, Index: i, N: n, Note: FewTracesNote(n), Cells: cells})
	}
	floored := false
	for _, column := range columns {
		if floors[column.Key] != nil {
			floored = true
		}
	}
	return &EquivalenceSummaryTable{Columns: columns, Rows: rows, Floored: floored}
}
