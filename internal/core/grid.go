package core

// Ranges and the treatments grid: a seeded percentile bootstrap for every
// figure, models as rows by setting variants as columns, and the cost
// against quality frontier. JavaScript semantics; the ranges match
// Metergraph's hosted figures (conformance/page.json). A range says how far
// a figure could move with other calls from the same traffic; it is a range,
// never a verdict.

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/metergraph/replay/internal/jsnum"
)

// Seeded is seeded: a small deterministic generator (mulberry32), so a range
// is the same on every render. A seed always yields the same
// sequence, so a run's ranges are reproducible.
func Seeded(seed float64) func() float64 {
	a := toUint32(seed)
	return func() float64 {
		a += 0x6d2b79f5
		t := a
		t = (t ^ (t >> 15)) * (t | 1)
		t ^= t + (t^(t>>7))*(t|61)
		return float64(t^(t>>14)) / 4294967296
	}
}

// toUint32 is `x >>> 0`.
func toUint32(x float64) uint32 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	t := math.Trunc(x)
	m := math.Mod(t, 4294967296)
	if m < 0 {
		m += 4294967296
	}
	return uint32(m)
}

func gridMean(values []float64) float64 { return sumOf(values) / float64(len(values)) }

func gridMedian(values []float64) float64 { return quantileOf(sortNumbers(values), 0.5) }

// Resamples is RESAMPLES; IntervalLevel is INTERVAL_LEVEL.
const (
	Resamples     = 400
	IntervalLevel = 0.9
)

// Interval is a bootstrap range.
type Interval struct {
	Low   float64 `json:"low"`
	High  float64 `json:"high"`
	N     int     `json:"n"`
	Level float64 `json:"level"`
}

// BootstrapOptions are bootstrapInterval's options; zero values are the defaults.
type BootstrapOptions struct {
	Resamples int
	Level     float64
	Seed      *float64
}

// BootstrapInterval is bootstrapInterval: a percentile bootstrap, the
// statistic ("mean" or "median", the default) on Resamples draws with
// replacement, its 5th and 95th percentile. Nil below two values.
func BootstrapInterval(values []any, statistic string, options BootstrapOptions) *Interval {
	resamples := options.Resamples
	if resamples == 0 {
		resamples = Resamples
	}
	level := options.Level
	if level == 0 {
		level = IntervalLevel
	}
	seed := 1.0
	if options.Seed != nil {
		seed = *options.Seed
	}
	nums := finiteNumbers(values)
	n := len(nums)
	if n < 2 {
		return nil
	}
	stat := gridMedian
	if statistic == "mean" {
		stat = gridMean
	}
	random := Seeded(seed + float64(n))
	draws := make([]float64, 0, resamples)
	sample := make([]float64, n)
	for r := 0; r < resamples; r++ {
		for i := 0; i < n; i++ {
			sample[i] = nums[int(math.Floor(random()*float64(n)))]
		}
		draws = append(draws, stat(sample))
	}
	draws = sortNumbers(draws)
	tail := (1 - level) / 2
	return &Interval{Low: quantileOf(draws, tail), High: quantileOf(draws, 1-tail), N: n, Level: level}
}

// --- Metrics per cell -----------------------------------------------------------

// GridMetric is what a cell can show. Better is "lower", "higher" or ""
// (a numeric shift has no direction); ControlSource is "production",
// "replay" or "" (none).
type GridMetric struct {
	Key           string
	Label         string
	Statistic     string
	Format        func(any) string
	Better        string
	ControlSource string
	Field         string
	Values        func(label string) []any
	Control       func() []any
}

func armResult(c any, label string) any {
	result := at(c, "arms", label)
	if truthy(result) && !truthy(at(result, "error")) {
		return result
	}
	return nil
}

// GridTreatmentLabels is gridTreatmentLabels: the treatments a grid shows,
// every arm but the replayed control.
func GridTreatmentLabels(experiment any) []string {
	var out []string
	for _, arm := range list(at(toLoose(experiment), "arms")) {
		if l, _ := get(arm, "label"); l != ReplayedControlLabel && !truthy(at(arm, "replayed_control")) {
			out = append(out, armLabel(arm))
		}
	}
	return out
}

func floorA1(c any) any {
	result := at(c, "noise_floor", "a1")
	if truthy(result) && !truthy(at(result, "error")) {
		return result
	}
	return nil
}

// GridMetricsOptions are gridMetrics' options.
type GridMetricsOptions struct {
	Tracked       []string
	FloorAsReplay bool
}

// GridMetrics is gridMetrics: what a cell can show, cost per call (a mean),
// latency (a median, with the control's only from a replayed control
// column, never production's recording), and a reading per tracked field.
// With FloorAsReplay, a run without that column reads the noise floor's
// first replay (A1) as the replayed control's latency.
func GridMetrics(experiment any, options GridMetricsOptions) []GridMetric {
	experiment = toLoose(experiment)
	var cases []any
	for _, c := range list(at(experiment, "cases")) {
		if replayedCase(c) {
			cases = append(cases, c)
		}
	}
	labels := GridTreatmentLabels(experiment)
	column := false
	for _, arm := range list(at(experiment, "arms")) {
		if l, _ := get(arm, "label"); l == ReplayedControlLabel || truthy(at(arm, "replayed_control")) {
			column = true
		}
	}
	someA1 := false
	for _, c := range cases {
		if floorA1(c) != nil {
			someA1 = true
		}
	}
	fromFloor := !column && options.FloorAsReplay && truthy(at(experiment, "noise_floor")) && someA1
	replayed := column || fromFloor
	replayLatency := func(c any) any {
		if fromFloor {
			return at(floorA1(c), "latency_ms")
		}
		return at(armResult(c, ReplayedControlLabel), "latency_ms")
	}
	perCase := func(read func(c any) any) []any {
		out := make([]any, len(cases))
		for i, c := range cases {
			out[i] = read(c)
		}
		return out
	}
	controlSource := ""
	if replayed {
		controlSource = "replay"
	}
	metrics := []GridMetric{
		{
			Key: "cost_per_call_usd", Label: "Cost per call", Statistic: "mean", Format: FormatUSD, Better: "lower", ControlSource: "production",
			Values: func(label string) []any {
				return perCase(func(c any) any { return at(armResult(c, label), "cost_usd") })
			},
			Control: func() []any { return perCase(func(c any) any { return at(c, "control", "cost_usd") }) },
		},
		{
			Key: "latency_ms_p50", Label: "Latency", Statistic: "median", Format: FormatMs, Better: "lower", ControlSource: controlSource,
			Values: func(label string) []any {
				return perCase(func(c any) any { return at(armResult(c, label), "latency_ms") })
			},
			Control: func() []any {
				if replayed {
					return perCase(replayLatency)
				}
				return []any{}
			},
		},
	}
	fields := at(experiment, "fields")
	for _, path := range options.Tracked {
		kind, _ := FieldComparisons(cases, path, labels, fields)
		pairs := func(label string) []FieldPair { return FieldPairs(cases, path, label, fields) }
		none := func() []any { return []any{} }
		read := func(score func(p FieldPair) float64) func(string) []any {
			return func(label string) []any {
				ps := pairs(label)
				out := make([]any, len(ps))
				for i, p := range ps {
					out[i] = score(p)
				}
				return out
			}
		}
		switch kind {
		case KindNumber:
			metrics = append(metrics, GridMetric{
				Key: "field:" + path, Label: path + " shift", Statistic: "median", Format: func(v any) string { return FormatSigned(v, nil) }, Field: path,
				Values: read(func(p FieldPair) float64 { return jsNumber(p.Arm) - jsNumber(p.Control) }), Control: none,
			})
		case KindList:
			metrics = append(metrics, GridMetric{
				Key: "field:" + path, Label: path + " overlap", Statistic: "mean", Format: FormatPercent, Better: "higher", Field: path,
				Values: read(func(p FieldPair) float64 { return Overlap(p.Control, p.Arm) }), Control: none,
			})
		case KindMixed:
		default:
			metrics = append(metrics, GridMetric{
				Key: "field:" + path, Label: path + " matches", Statistic: "mean", Format: FormatPercent, Better: "higher", Field: path,
				Values: read(func(p FieldPair) float64 {
					if trimmedEqual(p.Control, p.Arm) {
						return 1
					}
					return 0
				}), Control: none,
			})
		}
	}
	return metrics
}

// CellReading is one cell's reading on one metric: the figure, its range and
// N, and the words for them.
type CellReading struct {
	Value     *float64  `json:"value"`
	Interval  *Interval `json:"interval"`
	N         int       `json:"n"`
	Text      string    `json:"text"`
	RangeText *string   `json:"rangeText"`
	Note      *string   `json:"note"`
	Title     *string   `json:"title,omitempty"`
}

// CellReadingOf is cellReading.
func CellReadingOf(values []any, metric GridMetric) CellReading {
	nums := finiteNumbers(values)
	n := len(nums)
	if n == 0 {
		return CellReading{Text: MissingValue, Note: ptr("No calls returned")}
	}
	var value float64
	if metric.Statistic == "mean" {
		value = gridMean(nums)
	} else {
		value = gridMedian(nums)
	}
	interval := BootstrapInterval(floats(nums), metric.Statistic, BootstrapOptions{})
	word := "calls"
	if n == 1 {
		word = "call"
	}
	calls := FormatCount(n) + " " + word
	r := CellReading{Value: &value, Interval: interval, N: n, Text: metric.Format(value)}
	if interval != nil {
		r.RangeText = ptr(metric.Format(interval.Low) + " to " + metric.Format(interval.High))
		r.Title = ptr(metric.Label + ": " + metric.Format(value) + ", the " + metric.Statistic + " of " + calls + ". " +
			jsnum.String(jsnum.Round(float64(interval.Level*100))) + "% bootstrap range " + metric.Format(interval.Low) + " to " + metric.Format(interval.High) + ".")
	} else {
		r.Note = ptr(calls + ", no range")
		r.Title = ptr(metric.Label + ": " + metric.Format(value) + " from " + calls + "; a range needs two.")
	}
	return r
}

// --- The grid -------------------------------------------------------------------

// ShortModel is shortModel: the part of a model id people say out loud, the
// id after its last slash.
func ShortModel(id any) string {
	id = toLoose(id)
	if !truthy(id) {
		return ""
	}
	text := jsString(id)
	return text[strings.LastIndex(text, "/")+1:]
}

// SettingsPhrase is settingsPhrase: a treatment's settings in words,
// "temperature 0.2, 2,048 max tokens, low reasoning", or "Default settings".
func SettingsPhrase(settings any) string {
	s := toLoose(settings)
	var parts []string
	if v := at(s, "temperature"); v != nil {
		parts = append(parts, "temperature "+jsString(v))
	}
	if v := at(s, "max_tokens"); v != nil {
		parts = append(parts, localeString(jsNumber(v))+" max tokens")
	}
	if v := at(s, "reasoning_effort"); v != nil {
		parts = append(parts, jsString(v)+" reasoning")
	}
	if len(parts) == 0 {
		return "Default settings"
	}
	return strings.Join(parts, ", ")
}

func canonical(value any) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = canonical(item)
		}
		return out
	case *Object, map[string]any:
		keys := keysOf(v)
		slices.SortStableFunc(keys, func(a, b string) int {
			if utf16Less(a, b) {
				return -1
			}
			if utf16Less(b, a) {
				return 1
			}
			return 0
		})
		o := NewObject()
		for _, k := range keys {
			x, _ := get(v, k)
			o.Set(k, canonical(x))
		}
		return o
	}
	return value
}

// GridRow is one model row of the grid.
type GridRow struct {
	Model    string  `json:"model"`
	Label    string  `json:"label"`
	Provider *string `json:"provider"`
}

// GridColumn is one settings column of the grid.
type GridColumn struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Settings any    `json:"settings"`
}

// GridLayout is the grid: models as rows, setting variants as columns.
type GridLayout struct {
	Rows    []*GridRow    `json:"rows"`
	Columns []*GridColumn `json:"columns"`
	// Arm labels at "<row>:<column>".
	Cells map[string][]string `json:"cells"`
	Arms  []string            `json:"arms"`
}

// GridLayoutOf is gridLayout: models as rows and setting variants as
// columns, read off the arms, a row per model through an endpoint in the
// order it first appears, a column per distinct settings, and at each
// crossing every arm that sits there. Nil with no treatments.
func GridLayoutOf(experiment any) *GridLayout {
	var arms []any
	for _, arm := range list(at(toLoose(experiment), "arms")) {
		if l, _ := get(arm, "label"); l != ReplayedControlLabel && !truthy(at(arm, "replayed_control")) {
			arms = append(arms, arm)
		}
	}
	if len(arms) == 0 {
		return nil
	}
	layout := &GridLayout{Rows: []*GridRow{}, Columns: []*GridColumn{}, Cells: map[string][]string{}, Arms: []string{}}
	for _, arm := range arms {
		model := ""
		if m := at(arm, "model"); truthy(m) {
			model = jsString(m)
		}
		var provider *string
		if p := at(arm, "provider"); truthy(p) {
			provider = ptr(jsString(p))
		}
		rowIndex := slices.IndexFunc(layout.Rows, func(r *GridRow) bool {
			return r.Model == model && ((r.Provider == nil && provider == nil) || (r.Provider != nil && provider != nil && *r.Provider == *provider))
		})
		if rowIndex < 0 {
			l := ShortModel(model)
			if l == "" {
				l = MissingValue
			}
			layout.Rows = append(layout.Rows, &GridRow{Model: model, Label: l, Provider: provider})
			rowIndex = len(layout.Rows) - 1
		}
		settings := at(arm, "settings")
		if !truthy(settings) {
			settings = NewObject()
		}
		key := Stringify(canonical(settings), "")
		columnIndex := slices.IndexFunc(layout.Columns, func(c *GridColumn) bool { return c.Key == key })
		if columnIndex < 0 {
			layout.Columns = append(layout.Columns, &GridColumn{Key: key, Label: SettingsPhrase(at(arm, "settings")), Settings: settings})
			columnIndex = len(layout.Columns) - 1
		}
		cell := strconv.Itoa(rowIndex) + ":" + strconv.Itoa(columnIndex)
		layout.Cells[cell] = append(layout.Cells[cell], armLabel(arm))
		layout.Arms = append(layout.Arms, armLabel(arm))
	}
	// Rows that share a model are told apart by their endpoint.
	for _, row := range layout.Rows {
		for _, other := range layout.Rows {
			if other != row && other.Model == row.Model {
				via := "unknown provider"
				if row.Provider != nil && *row.Provider != "" {
					via = *row.Provider
				}
				row.Label = row.Label + " via " + via
				break
			}
		}
	}
	return layout
}

// GridReadings is every cell's reading on one metric, the control's when it
// has one, and one shared scale.
type GridReadings struct {
	Readings map[string]CellReading `json:"readings"`
	Control  *CellReading           `json:"control"`
	Scale    *Scale                 `json:"scale"`
}

// GridReadingsOf is gridReadings.
func GridReadingsOf(experiment any, metric GridMetric) GridReadings {
	labels := GridTreatmentLabels(experiment)
	readings := map[string]CellReading{}
	var order []string
	for _, l := range labels {
		if _, ok := readings[l]; !ok {
			order = append(order, l)
		}
		readings[l] = CellReadingOf(metric.Values(l), metric)
	}
	var control *CellReading
	if metric.Field == "" {
		c := CellReadingOf(metric.Control(), metric)
		control = &c
	}
	var drawn []CellReading
	for _, l := range order {
		if readings[l].Value != nil {
			drawn = append(drawn, readings[l])
		}
	}
	if control != nil && control.Value != nil {
		drawn = append(drawn, *control)
	}
	lo, hi := 0.0, 0.0
	for _, r := range drawn {
		value := *r.Value
		low, high := value, value
		if r.Interval != nil {
			low, high = r.Interval.Low, r.Interval.High
		}
		lo = jsMin(lo, value, low, high)
		hi = jsMax(hi, value, low, high)
	}
	out := GridReadings{Readings: readings}
	if control != nil && control.N > 0 {
		out.Control = control
	}
	if len(drawn) > 0 {
		h := lo + 1
		if hi > lo {
			h = hi
		}
		out.Scale = &Scale{Lo: lo, Hi: h}
	}
	return out
}

// FrontierPoint is one cell as a point: cost per call across, the chosen metric up.
type FrontierPoint struct {
	Label   string      `json:"label"`
	X       CellReading `json:"x"`
	Y       CellReading `json:"y"`
	Control bool        `json:"control"`
}

// FrontierPoints is frontierPoints: every cell as a point, cost per call
// across, the chosen metric up. A nil cost is the cost metric GridMetrics
// gives first.
func FrontierPoints(experiment any, metric GridMetric, cost *GridMetric) []FrontierPoint {
	experiment = toLoose(experiment)
	costMetric := cost
	if costMetric == nil {
		costMetric = &GridMetrics(experiment, GridMetricsOptions{})[0]
	}
	costs := GridReadingsOf(experiment, *costMetric)
	ups := GridReadingsOf(experiment, metric)
	var points []FrontierPoint
	for _, l := range GridTreatmentLabels(experiment) {
		points = append(points, FrontierPoint{Label: l, X: costs.Readings[l], Y: ups.Readings[l]})
	}
	if costs.Control != nil && ups.Control != nil {
		points = append(points, FrontierPoint{Label: "Control", X: *costs.Control, Y: *ups.Control, Control: true})
	}
	out := []FrontierPoint{}
	for _, p := range points {
		if p.X.Value != nil && p.Y.Value != nil {
			out = append(out, p)
		}
	}
	return out
}

// Frontier is frontier: the points no other point is at once cheaper than
// and at least as far up (or down, where lower is the way), the cost against
// quality frontier, cheapest first. A fact about the points, not a
// recommendation. An empty better has no frontier.
func Frontier(points []FrontierPoint, better string) []FrontierPoint {
	if better == "" {
		return []FrontierPoint{}
	}
	up := func(p FrontierPoint) float64 {
		if better == "higher" {
			return *p.Y.Value
		}
		return -*p.Y.Value
	}
	x := func(p FrontierPoint) float64 { return *p.X.Value }
	kept := []FrontierPoint{}
	for i, p := range points {
		dominated := false
		for j, q := range points {
			if j != i && x(q) <= x(p) && up(q) >= up(p) && (x(q) < x(p) || up(q) > up(p)) {
				dominated = true
				break
			}
		}
		if !dominated {
			kept = append(kept, p)
		}
	}
	slices.SortStableFunc(kept, func(a, b FrontierPoint) int {
		d := x(a) - x(b)
		switch {
		case d < 0:
			return -1
		case d > 0:
			return 1
		}
		return 0
	})
	return kept
}

// CellSummary is cellSummary: what a cell is in words, for a table cell's
// accessible name. An empty armLabel names the cell by its row and column.
func CellSummary(rowLabel, columnLabel, armLabel string, reading *CellReading) string {
	text := ""
	if reading != nil {
		text = reading.Text
	}
	if text == "" {
		text = MissingValue
	}
	parts := []string{text}
	if reading != nil && reading.RangeText != nil && *reading.RangeText != "" {
		parts = append(parts, "range "+*reading.RangeText)
	}
	if reading != nil && reading.Note != nil && *reading.Note != "" {
		parts = append(parts, *reading.Note)
	}
	name := armLabel
	if name == "" {
		name = rowLabel + ", " + columnLabel
	}
	return name + ": " + strings.Join(parts, ", ")
}
