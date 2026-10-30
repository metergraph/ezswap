package core

// Tracked-field comparisons over the experiment view: what kind of values a
// field holds, and how one side reads against another on it (per-call
// differences for numbers, match rate for labels and text, item overlap for
// lists). JavaScript semantics throughout; the numbers match Metergraph's
// hosted figures exactly (conformance/page.json). Shown, never judged.
//
// These functions read the view loosely: a view from the hosted product, one
// built from a run file, and partial test inputs all fit.

import (
	"math"
	"regexp"
	"slices"
	"strings"
)

// SpreadMin is SPREAD_MIN: below this many values a 10th-to-90th percentile
// range is mostly noise.
const SpreadMin = 10

const jsWS = `\t\n\x{0b}\x{0c}\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// A model's JSON often comes fenced in markdown: one surrounding fence of any language, then JSON.
var jsFence = regexp.MustCompile("^(?s)```[A-Za-z0-9_-]*[" + jsWS + "]*\n(.*?)\n?```[" + jsWS + "]*$")

// Parsed is an output read as JSON.
type Parsed struct {
	OK    bool
	Value any
}

// ParseOutput is parseOutput: an output parsed as JSON, trimming with
// JavaScript whitespace rules. An object or array is taken as already parsed.
func ParseOutput(output any) Parsed {
	output = toLoose(output)
	if output == nil {
		return Parsed{}
	}
	switch output.(type) {
	case *Object, map[string]any, []any:
		return Parsed{OK: true, Value: output}
	}
	text := jsTrim(jsString(output))
	if text == "" {
		return Parsed{}
	}
	if m := jsFence.FindStringSubmatch(text); m != nil {
		text = jsTrim(m[1])
	}
	v, err := ParseJSON(text)
	if err != nil {
		return Parsed{}
	}
	return Parsed{OK: true, Value: v}
}

// OutputText is outputText: the output as text, pretty JSON for a value.
func OutputText(output any) string {
	output = toLoose(output)
	if output == nil {
		return ""
	}
	if s, ok := output.(string); ok {
		return s
	}
	return Stringify(output, "  ")
}

// DisplayOutput is displayOutput: pretty JSON when it parses, the verbatim
// string when it does not.
func DisplayOutput(output any) string {
	parsed := ParseOutput(output)
	if parsed.OK {
		return Stringify(parsed.Value, "  ")
	}
	return OutputText(output)
}

// PathCount is a path and how many outputs carry it.
type PathCount struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

func caseOutputs(c any) []any {
	outputs := []any{at(c, "control", "output")}
	arms, _ := get(c, "arms")
	for _, a := range valuesOf(arms) {
		outputs = append(outputs, at(a, "output"))
	}
	return outputs
}

// AvailablePaths is availablePaths: every path seen in any control or arm
// output, with how many outputs carry it, most common first.
func AvailablePaths(cases any) []PathCount {
	counts := map[string]int{}
	var order []string
	for _, c := range list(toLoose(cases)) {
		for _, output := range caseOutputs(c) {
			parsed := ParseOutput(output)
			if !parsed.OK {
				continue
			}
			seen := map[string]bool{}
			for _, path := range JSONPaths(parsed.Value, "$") {
				if seen[path] {
					continue
				}
				seen[path] = true
				if _, ok := counts[path]; !ok {
					order = append(order, path)
				}
				counts[path]++
			}
		}
	}
	out := make([]PathCount, len(order))
	for i, path := range order {
		out[i] = PathCount{path, counts[path]}
	}
	slices.SortStableFunc(out, func(a, b PathCount) int {
		if d := b.Count - a.Count; d != 0 {
			return d
		}
		return localeCompare(a.Path, b.Path)
	})
	return out
}

// PickerPaths is pickerPaths: the paths seen in the outputs, plus any the run tracked.
func PickerPaths(cases any, tracked []string) []PathCount {
	seen := AvailablePaths(cases)
	known := map[string]bool{}
	for _, p := range seen {
		known[p.Path] = true
	}
	for _, path := range tracked {
		if !known[path] {
			seen = append(seen, PathCount{path, 0})
		}
	}
	return seen
}

const (
	labelMaxDistinct = 12
	shortTextMax     = 120
)

// FieldKindOf is fieldKind: what a field's values are, which decides how
// arms are compared on it (lengths in UTF-16 units). Nil values are missing.
func FieldKindOf(values []any) FieldKind {
	var present []any
	for _, v := range values {
		if v != nil {
			present = append(present, v)
		}
	}
	if len(present) == 0 {
		return KindEmpty
	}
	every := func(test func(any) bool) bool {
		for _, v := range present {
			if !test(v) {
				return false
			}
		}
		return true
	}
	if every(func(v any) bool { _, ok := finiteNum(v); return ok }) {
		return KindNumber
	}
	if every(isArray) {
		return KindList
	}
	if every(func(v any) bool { _, ok := v.(bool); return ok }) {
		return KindLabel
	}
	if every(func(v any) bool {
		switch v.(type) {
		case string, bool:
			return true
		}
		return false
	}) {
		distinct := map[string]bool{}
		short := true
		within := true
		for _, v := range present {
			s := jsString(v)
			distinct[s] = true
			n := utf16Len(s)
			if n > 40 {
				short = false
			}
			if n > shortTextMax {
				within = false
			}
		}
		if short && len(distinct) <= max(labelMaxDistinct, 2) && len(distinct) < len(present) {
			return KindLabel
		}
		if within {
			return KindText
		}
		return KindLongText
	}
	return KindMixed
}

// KindLabels name each kind for the page.
var KindLabels = map[FieldKind]string{
	KindNumber:   "Number",
	KindLabel:    "Label",
	KindList:     "List",
	KindText:     "Short text",
	KindLongText: "Long text",
	KindMixed:    "Mixed",
	KindEmpty:    "No values",
}

func replayedCase(c any) bool {
	status, _ := get(c, "status")
	return status == nil || status == "replayed"
}

// ReplayedIndexes is replayedIndexes: each case's position among the
// replayed cases, the order `fields` series follow; -1 otherwise.
func ReplayedIndexes(cases any) []int {
	items := list(toLoose(cases))
	out := make([]int, len(items))
	next := 0
	for i, c := range items {
		if replayedCase(c) {
			out[i] = next
			next++
		} else {
			out[i] = -1
		}
	}
	return out
}

// SideValue is sideValue: one side's value on a field, nil when it has none.
// A stored output cut at the bound is a prefix that no longer parses, so a
// shortened one takes the series value, which was read from the full output.
// Null counts as missing, never as zero.
func SideValue(result any, path string, series any, index int) any {
	var value any
	if items, isList := series.([]any); truthy(at(result, "output_truncated")) && isList && index >= 0 {
		if index < len(items) {
			value = items[index]
		}
	} else {
		parsed := ParseOutput(at(result, "output"))
		if parsed.OK {
			value, _ = ValueAt(parsed.Value, path)
		}
	}
	return value
}

func seriesOf(fields any, path, side string) any {
	return at(fields, path, side)
}

// ControlValues is controlValues: the control's value on a field, per case.
func ControlValues(cases any, path string, fields any) []any {
	items := list(toLoose(cases))
	fields = toLoose(fields)
	indexes := ReplayedIndexes(items)
	out := make([]any, len(items))
	for i, c := range items {
		out[i] = SideValue(at(c, "control"), path, seriesOf(fields, path, "control"), indexes[i])
	}
	return out
}

// ArmValues is armValues: one arm's value on a field, per case.
func ArmValues(cases any, path, label string, fields any) []any {
	items := list(toLoose(cases))
	fields = toLoose(fields)
	indexes := ReplayedIndexes(items)
	out := make([]any, len(items))
	for i, c := range items {
		out[i] = SideValue(at(c, "arms", label), path, seriesOf(fields, path, label), indexes[i])
	}
	return out
}

// SuggestedFields is suggestedFields: fields worth pre-highlighting, present
// in at least half the control outputs, of a kind with a comparison. A limit
// of 0 is the default, 3.
func SuggestedFields(cases any, limit int) []string {
	if limit == 0 {
		limit = 3
	}
	items := list(toLoose(cases))
	if len(items) == 0 {
		return []string{}
	}
	half := (len(items) + 1) / 2
	order := []FieldKind{KindLabel, KindNumber, KindList, KindText}
	type candidate struct {
		path string
		kind FieldKind
	}
	var kept []candidate
	for _, p := range AvailablePaths(items) {
		values := ControlValues(items, p.Path, nil)
		n := 0
		for _, v := range values {
			if v != nil {
				n++
			}
		}
		if n < half {
			continue
		}
		kind := FieldKindOf(values)
		if slices.Contains(order, kind) {
			kept = append(kept, candidate{p.Path, kind})
		}
	}
	slices.SortStableFunc(kept, func(a, b candidate) int {
		return slices.Index(order, a.kind) - slices.Index(order, b.kind)
	})
	out := []string{}
	for i, c := range kept {
		if i >= limit {
			break
		}
		out = append(out, c.path)
	}
	return out
}

// --- Statistics ---------------------------------------------------------------

func quantileOf(sorted []float64, q float64) float64 {
	pos := float64(float64(len(sorted)-1) * q)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return sorted[lo] + float64((sorted[hi]-sorted[lo])*(pos-float64(lo)))
}

// Quantile is quantile: the linear-interpolated quantile of sorted values,
// nil for none.
func Quantile(sorted []float64, q float64) *float64 {
	if len(sorted) == 0 {
		return nil
	}
	return ptr(quantileOf(sorted, q))
}

// BoxStats is a five-number summary.
type BoxStats struct {
	N      int     `json:"n"`
	Min    float64 `json:"min"`
	Q1     float64 `json:"q1"`
	Median float64 `json:"median"`
	Q3     float64 `json:"q3"`
	Max    float64 `json:"max"`
}

func finiteNumbers(values []any) []float64 {
	var out []float64
	for _, v := range values {
		if f, ok := finiteNum(v); ok {
			out = append(out, f)
		}
	}
	return out
}

func floats(values []float64) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// BoxStatsOf is boxStats: the five numbers of the finite numbers among
// values, nil for none.
func BoxStatsOf(values []any) *BoxStats {
	sorted := sortNumbers(finiteNumbers(values))
	if len(sorted) == 0 {
		return nil
	}
	return &BoxStats{
		N:      len(sorted),
		Min:    sorted[0],
		Q1:     quantileOf(sorted, 0.25),
		Median: quantileOf(sorted, 0.5),
		Q3:     quantileOf(sorted, 0.75),
		Max:    sorted[len(sorted)-1],
	}
}

// Mean is mean: the mean of the finite numbers among values, nil for none.
func Mean(values []any) *float64 {
	nums := finiteNumbers(values)
	if len(nums) == 0 {
		return nil
	}
	return ptr(sumOf(nums) / float64(len(nums)))
}

// sumOf is `values.reduce((s, v) => s + v, 0)`.
func sumOf(values []float64) float64 {
	s := 0.0
	for _, v := range values {
		s += v
	}
	return s
}

func listItems(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, len(items))
	for i, v := range items {
		switch v.(type) {
		case nil, *Object, map[string]any, []any:
			out[i] = Stringify(v, "")
		default:
			out[i] = jsString(v)
		}
	}
	return out
}

func stringSet(items []string) (map[string]bool, []string) {
	set := map[string]bool{}
	var order []string
	for _, s := range items {
		if !set[s] {
			set[s] = true
			order = append(order, s)
		}
	}
	return set, order
}

// Overlap is overlap: shared list items over all distinct items (1 when both are empty).
func Overlap(a, b any) float64 {
	_, left := stringSet(listItems(toLoose(a)))
	right, _ := stringSet(listItems(toLoose(b)))
	if len(left) == 0 && len(right) == 0 {
		return 1
	}
	shared := 0
	for _, item := range left {
		if right[item] {
			shared++
		}
	}
	return float64(shared) / float64(len(left)+len(right)-shared)
}

// FieldPair is one case's value on each side.
type FieldPair struct {
	TraceID any `json:"trace_id"`
	Control any `json:"control"`
	Arm     any `json:"arm"`
}

// FieldPairs is fieldPairs: one arm's values beside the control's, per case,
// over the cases both have a value on.
func FieldPairs(cases any, path, label string, fields any) []FieldPair {
	items := list(toLoose(cases))
	control := ControlValues(items, path, fields)
	arm := ArmValues(items, path, label, fields)
	out := []FieldPair{}
	for i, c := range items {
		if control[i] != nil && arm[i] != nil {
			out = append(out, FieldPair{at(c, "trace_id"), control[i], arm[i]})
		}
	}
	return out
}

// LabelTally is how often one control label became one arm label.
type LabelTally struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
	Same  bool   `json:"same"`
}

// LabelTallyOf is labelTally: how often each control label became each arm
// label, most common first.
func LabelTallyOf(pairs []FieldPair) []LabelTally {
	counts := map[string]int{}
	var order []string
	for _, p := range pairs {
		key := jsString(p.Control) + "\x00" + jsString(p.Arm)
		if _, ok := counts[key]; !ok {
			order = append(order, key)
		}
		counts[key]++
	}
	out := make([]LabelTally, len(order))
	for i, key := range order {
		parts := strings.Split(key, "\x00")
		out[i] = LabelTally{From: parts[0], To: parts[1], Count: counts[key], Same: parts[0] == parts[1]}
	}
	slices.SortStableFunc(out, func(a, b LabelTally) int {
		if d := b.Count - a.Count; d != 0 {
			return d
		}
		return localeCompare(a.From, b.From)
	})
	return out
}

// Diff is one case's arm minus control on a number.
type Diff struct {
	TraceID any     `json:"trace_id"`
	Diff    float64 `json:"diff"`
}

// PairedReading is what one side reads as against another over the cases
// both have a value on. Which fields it has follows the kind it was read
// with: DiffBox and Diffs for a number; Matched, Rate and Tally (labels
// only) for labels and text; Overlap and OverlapBox for lists; none for a
// mixed or empty field.
type PairedReading struct {
	DiffBox    *BoxStats
	Diffs      []Diff
	Matched    *int
	Rate       *float64
	Tally      []LabelTally
	Overlap    *float64
	OverlapBox *BoxStats

	kind FieldKind
}

// set writes the reading's fields as its kind has them.
func (r *PairedReading) set(o *Object) {
	switch r.kind {
	case KindNumber:
		o.Set("diffBox", r.DiffBox)
		o.Set("diffs", r.Diffs)
	case KindLabel, KindText, KindLongText:
		o.Set("matched", toLoose(r.Matched))
		o.Set("rate", toLoose(r.Rate))
		if r.Tally == nil {
			o.Set("tally", nil)
		} else {
			o.Set("tally", r.Tally)
		}
	case KindList:
		o.Set("overlap", toLoose(r.Overlap))
		o.Set("overlapBox", r.OverlapBox)
	}
}

// MarshalJSON writes the reading's fields.
func (r PairedReading) MarshalJSON() ([]byte, error) {
	o := NewObject()
	r.set(o)
	return o.MarshalJSON()
}

func trimmedEqual(a, b any) bool { return jsTrim(jsString(a)) == jsTrim(jsString(b)) }

// PairedReadingOf is pairedReading: what one side reads as against another
// over the cases both have a value on. Treatments and the noise floor share
// it, so the two readings are always measured the same way.
func PairedReadingOf(pairs []FieldPair, kind FieldKind) PairedReading {
	r := PairedReading{kind: kind}
	switch kind {
	case KindNumber:
		diffs := make([]any, len(pairs))
		r.Diffs = make([]Diff, len(pairs))
		for i, p := range pairs {
			d := jsNumber(p.Arm) - jsNumber(p.Control)
			diffs[i] = d
			r.Diffs[i] = Diff{p.TraceID, d}
		}
		r.DiffBox = BoxStatsOf(diffs)
	case KindLabel, KindText, KindLongText:
		same := 0
		for _, p := range pairs {
			if trimmedEqual(p.Control, p.Arm) {
				same++
			}
		}
		r.Matched = ptr(same)
		if len(pairs) > 0 {
			r.Rate = ptr(float64(same) / float64(len(pairs)))
		}
		if kind == KindLabel {
			r.Tally = LabelTallyOf(pairs)
		}
	case KindList:
		scores := make([]any, len(pairs))
		for i, p := range pairs {
			scores[i] = Overlap(p.Control, p.Arm)
		}
		r.Overlap = Mean(scores)
		r.OverlapBox = BoxStatsOf(scores)
	}
	return r
}

// FieldComparison is one arm against the control on one field. ControlBox
// and ArmBox are there for a number only.
type FieldComparison struct {
	PairedReading
	Path       string
	Label      string
	Kind       FieldKind
	Pairs      int
	Missing    int
	ControlBox *BoxStats
	ArmBox     *BoxStats
}

// MarshalJSON writes the comparison in the page's shape.
func (c FieldComparison) MarshalJSON() ([]byte, error) {
	o := obj("path", c.Path, "label", c.Label, "kind", string(c.Kind), "pairs", float64(c.Pairs), "missing", float64(c.Missing))
	if c.Kind == KindNumber {
		o.Set("controlBox", c.ControlBox)
		o.Set("armBox", c.ArmBox)
	}
	c.PairedReading.set(o)
	return o.MarshalJSON()
}

// CompareField is compareField: one arm against the control on one field.
// An empty sharedKind reads the kind from the values.
func CompareField(cases any, path, label string, fields any, sharedKind FieldKind) FieldComparison {
	items := list(toLoose(cases))
	fields = toLoose(fields)
	control := ControlValues(items, path, fields)
	arm := ArmValues(items, path, label, fields)
	kind := sharedKind
	if kind == "" {
		kind = FieldKindOf(append(slices.Clone(control), arm...))
	}
	pairs := FieldPairs(items, path, label, fields)
	c := FieldComparison{Path: path, Label: label, Kind: kind, Pairs: len(pairs), Missing: len(items) - len(pairs)}
	if kind == KindNumber {
		c.ControlBox = BoxStatsOf(control)
		c.ArmBox = BoxStatsOf(arm)
	}
	c.PairedReading = PairedReadingOf(pairs, kind)
	return c
}

// FieldComparisons is fieldComparisons: every arm on one field, judged by
// one kind read from the control and all the arms together, so the order of
// the arms cannot change how any of them is shown. When the series disagree
// on type, each arm falls back to its own kind and the field as a whole
// reads as mixed.
func FieldComparisons(cases any, path string, labels []string, fields any) (FieldKind, []FieldComparison) {
	items := list(toLoose(cases))
	fields = toLoose(fields)
	all := ControlValues(items, path, fields)
	for _, label := range labels {
		all = append(all, ArmValues(items, path, label, fields)...)
	}
	shared := FieldKindOf(all)
	comparisons := make([]FieldComparison, len(labels))
	for i, label := range labels {
		if shared != KindMixed {
			comparisons[i] = CompareField(items, path, label, fields, shared)
		} else {
			comparisons[i] = CompareField(items, path, label, fields, "")
		}
	}
	return shared, comparisons
}
