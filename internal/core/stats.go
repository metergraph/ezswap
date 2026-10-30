package core

// The built-in stat rows, spreads, monthly projection, the noise floor's
// readings and the one sentence a tracked field may carry. JavaScript
// semantics; matches Metergraph's hosted figures (conformance/page.json).
// Nothing here grades a treatment or says whether a swap is safe.

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/metergraph/replay/internal/jsnum"
)

// ReplayedControlLabel is REPLAYED_CONTROL_LABEL: a column labelled this is
// production's model replayed as a paid arm, the baseline for cost and latency.
const ReplayedControlLabel = "Control, replayed"

// ControlSummary is controlSummary: the control's summary, the view's own,
// or, when it has none, what the cases' production calls tell. A reply
// counts as empty or refused on the refusal flag, a refusal finish reason,
// or blank text. Nil when there is neither. The result is loose: the view's
// own summary as it is, or an object in the summary's keys.
func ControlSummary(experiment any) any {
	experiment = toLoose(experiment)
	if s := at(experiment, "control", "summary"); truthy(s) {
		return s
	}
	var cases []any
	for _, c := range list(at(experiment, "cases")) {
		if truthy(at(c, "control")) {
			cases = append(cases, c)
		}
	}
	if len(cases) == 0 {
		return nil
	}
	column := func(read func(control any) any) []any {
		out := make([]any, len(cases))
		for i, c := range cases {
			out[i] = read(at(c, "control"))
		}
		return out
	}
	field := func(key string) func(any) any {
		return func(control any) any { v, _ := get(control, key); return v }
	}
	median := func(values []any) any {
		if b := BoxStatsOf(values); b != nil {
			return b.Median
		}
		return nil
	}
	parses, empty := 0, 0
	for _, c := range cases {
		control := at(c, "control")
		output, _ := get(control, "output")
		if ParseOutput(output).OK {
			parses++
		}
		finish, _ := get(control, "finish_reason")
		refusal, _ := get(control, "refusal")
		if IsEmptyOrRefused(output, finish, truthy(refusal)) {
			empty++
		}
	}
	n := float64(len(cases))
	return obj(
		"cases", n,
		"errors", 0.0,
		"cost_per_call_usd", toLoose(Mean(column(field("cost_usd")))),
		"model_cost_per_call_usd", toLoose(Mean(column(func(control any) any {
			cost, _ := get(control, "cost_usd")
			fee, _ := get(control, "cost_search_usd")
			if fee == nil || cost == nil {
				return nil
			}
			return jsNumber(cost) - jsNumber(fee)
		}))),
		"search_cost_per_call_usd", toLoose(Mean(column(field("cost_search_usd")))),
		"latency_ms_p50", median(column(field("latency_ms"))),
		"output_tokens_p50", median(column(field("output_tokens"))),
		"json_parse_rate", float64(parses)/n,
		"same_keys_rate", nil,
		"empty_or_refused_rate", float64(empty)/n,
		"cut_off_rate", nil,
	)
}

// StatRowDef is one built-in stat: its summary key, its label and how its
// figure prints.
type StatRowDef struct {
	Key    string
	Label  string
	Format func(any) string
}

// StatRowDefs is STAT_ROWS: the built-in stats in the order the table shows them.
var StatRowDefs = []StatRowDef{
	{"cost_per_call_usd", "Cost per call", FormatUSD},
	// The cost split: shown when any column has search fees.
	{"model_cost_per_call_usd", "Model cost per call", FormatUSD},
	{"search_cost_per_call_usd", "Search fees per call", FormatUSD},
	{"cached_input_share", "Input tokens from cache", FormatCachedShare},
	{"latency_ms_p50", "Latency, median", FormatMs},
	{"output_tokens_p50", "Output tokens, median", FormatCount},
	{"output_chars_p50", "Output length, median characters", FormatCount},
	{"json_parse_rate", "JSON parses", FormatPercent},
	{"same_keys_rate", "Same keys as control", FormatPercent},
	{"empty_or_refused_rate", "Empty or refused", FormatPercent},
	{"cut_off_rate", "Cut off at token limit", FormatPercent},
	{"errors", "Errors", FormatCount},
}

// StatGroup is one group of stats.
type StatGroup struct {
	Title string
	Keys  []string
}

// StatGroups is STAT_GROUPS: the stats read in two groups, what a call costs
// and how long it takes, then the output's shape.
var StatGroups = []StatGroup{
	{"Cost and speed", []string{"monthly_cost_usd", "cost_per_call_usd", "model_cost_per_call_usd", "search_cost_per_call_usd", "cached_input_share", "latency_ms_p50", "output_tokens_p50", "output_chars_p50"}},
	{"Output shape", []string{"json_parse_rate", "same_keys_rate", "empty_or_refused_rate", "cut_off_rate", "errors"}},
}

// Production's recorded latency is timed by whatever logged the call, and its
// cost reflects production's own caching: neither is measured the way a
// replay is. So cost, cache share and latency are read against the control
// replayed the same way when the run has it, and latency is never read
// against production's recording.
var replayBaselineKeys = map[string]bool{"cost_per_call_usd": true, "model_cost_per_call_usd": true, "search_cost_per_call_usd": true, "cached_input_share": true, "latency_ms_p50": true}
var costSplitKeys = map[string]bool{"model_cost_per_call_usd": true, "search_cost_per_call_usd": true}

// CostSplitShown is costSplitShown: whether the cost split has anything to
// show, a column whose calls billed search fees.
func CostSplitShown(experiment any) bool {
	experiment = toLoose(experiment)
	summaries := []any{ControlSummary(experiment)}
	for _, arm := range list(at(experiment, "arms")) {
		summaries = append(summaries, at(arm, "summary"))
	}
	for _, s := range summaries {
		if f, ok := num(at(s, "search_cost_per_call_usd")); ok && f > 0 {
			return true
		}
	}
	return false
}

// BaselineOptions say where the replayed control comes from. By default only
// a paid "Control, replayed" column counts (the behaviour
// conformance/page.json pins). A replay run measures the same thing as the
// noise floor's first replay, A1, so FloorAsReplay reads A1 as the replayed
// control when the run has no such column, as Equivalence already does.
type BaselineOptions struct {
	FloorAsReplay bool
}

// ReplayBaseline is replayBaseline: the replayed control's arm, or, with
// FloorAsReplay, the noise floor's A1 as one; nil when there is none.
func ReplayBaseline(experiment any, options BaselineOptions) any {
	experiment = toLoose(experiment)
	for _, a := range list(at(experiment, "arms")) {
		if label, _ := get(a, "label"); label == ReplayedControlLabel {
			if truthy(at(a, "summary")) {
				return a
			}
			break
		}
	}
	if !options.FloorAsReplay {
		return nil
	}
	if a1 := at(noiseFloor(experiment), "a1"); truthy(a1) {
		return obj("label", "Noise floor A1", "summary", a1, "floor", true)
	}
	return nil
}

// StatDelta is statDelta: a shift against a baseline in words, points for
// rates, a ratio otherwise. Nil when there is none to give. Value and base
// are numbers (float64, *float64, int) or nil; against is what a ratio is
// "×" of ("control" when empty).
func StatDelta(key string, value, base any, against string) *string {
	if against == "" {
		against = "control"
	}
	v, b := toLoose(value), toLoose(base)
	if v == nil || b == nil {
		return nil
	}
	if key == "errors" || key == "same_keys_rate" {
		return nil
	}
	x, y := jsNumber(v), jsNumber(b)
	if strings.HasSuffix(key, "_rate") || key == "cached_input_share" {
		points := jsnum.Round(float64((x - y) * 100))
		if points == 0 {
			return ptr("same")
		}
		sign := "+"
		if !(points > 0) {
			sign = "−"
		}
		return ptr(sign + jsnum.String(math.Abs(points)) + " pts")
	}
	if y == 0 {
		return nil
	}
	ratio := x / y
	if math.Abs(ratio-1) < 0.005 {
		return ptr("same")
	}
	if ratio >= 1 {
		digits := 1
		if ratio >= 10 {
			digits = 0
		}
		return ptr(jsnum.ToFixed(ratio, digits) + "× " + against)
	}
	return ptr(jsnum.String(jsnum.Round(float64((1-ratio)*100))) + "% lower")
}

// SameKeysCell is the same-keys figure, and over how many it is.
type SameKeysCell struct {
	Text  string  `json:"text"`
	Sub   *string `json:"sub,omitempty"`
	Title string  `json:"title"`
}

// SameKeysCellOf is sameKeysCell. Same keys is counted only where the
// control returned a whole JSON object, which can be a few of the cases.
// Then the figure says how many it is over ("0 of 1"), so one case never
// reads as a rate for the run.
func SameKeysCellOf(summary any) *SameKeysCell {
	summary = toLoose(summary)
	rateV := at(summary, "same_keys_rate")
	compared := at(summary, "same_keys_compared")
	if rateV == nil {
		return nil
	}
	title := "Outputs whose JSON has the same top-level keys as the control’s, over the traces where the control returned a JSON object"
	cases := at(summary, "cases")
	if compared == nil || cases == nil || jsNumber(compared) >= jsNumber(cases) {
		return &SameKeysCell{Text: FormatPercent(rateV), Title: title}
	}
	kept := jsnum.Round(float64(jsNumber(rateV) * jsNumber(compared)))
	sub := "traces with JSON"
	if jsNumber(compared) == 1 {
		sub = "trace with JSON"
	}
	return &SameKeysCell{Text: FormatCount(kept) + " of " + FormatCount(compared), Sub: &sub, Title: title}
}

func narrowerBase(matched, control any) bool {
	if !truthy(matched) {
		return false
	}
	cases, _ := get(matched, "cases")
	other, present := get(control, "cases")
	if cases == nil {
		return false
	}
	if !present {
		return true
	}
	return !strictEqual(cases, other)
}

// FloorStat is the control's own gap on a built-in stat.
type FloorStat struct {
	Text  *string `json:"text,omitempty"`
	Delta string  `json:"delta"`
	Title string  `json:"title"`
}

// FloorStatOf is floorStat: the control's own gap on a built-in stat, its
// second replay against its first, in the same terms a treatment's gap is
// given. Nil when there is none.
func FloorStatOf(row StatRowDef, floor any) *FloorStat {
	floor = toLoose(floor)
	value := at(floor, "summary", row.Key)
	base := at(floor, "control_summary", row.Key)
	cases, _ := get(floor, "cases")
	title := "The control's model replayed twice on " + FormatCount(cases) + " cases, the second replay against the first"
	if row.Key == "same_keys_rate" {
		if value == nil {
			return nil
		}
		return &FloorStat{Text: ptr(row.Format(value)), Delta: "against itself", Title: title}
	}
	delta := StatDelta(row.Key, value, base, "itself")
	if delta == nil || *delta == "" {
		return nil
	}
	text := *delta
	if !strings.HasSuffix(text, "itself") {
		text += " against itself"
	}
	return &FloorStat{Delta: text, Title: title}
}

// StatCell is one arm's figure on one stat row. SameCases and SameBase are
// set when the arm is read over other cases than the control column: those
// cases and the control's figure over them.
type StatCell struct {
	Label     string   `json:"label"`
	Text      string   `json:"text"`
	Delta     *string  `json:"delta"`
	Title     *string  `json:"title,omitempty"`
	SameCases *float64 `json:"sameCases,omitempty"`
	SameBase  *string  `json:"sameBase,omitempty"`
}

// MonthlyRowNote is what the monthly row says about the projection.
type MonthlyRowNote struct {
	Text    string `json:"text"`
	Typical *bool  `json:"typical"`
}

// StatRow is one built-in stat across the columns. Floor is there when the
// run has a noise floor (nil when the floor has no reading for this stat);
// Sublabel and Note belong to the monthly row only.
type StatRow struct {
	Key      string
	Label    string
	Sublabel *string
	Control  string
	Floor    *FloorStat
	Arms     []StatCell
	Note     *MonthlyRowNote

	HasFloor bool
	Monthly  bool
}

// MarshalJSON writes the row in the page's shape.
func (r StatRow) MarshalJSON() ([]byte, error) {
	o := obj("key", r.Key, "label", r.Label)
	if r.Monthly {
		o.Set("sublabel", toLoose(r.Sublabel))
	}
	o.Set("control", r.Control)
	if r.HasFloor {
		o.Set("floor", r.Floor)
	}
	arms := r.Arms
	if arms == nil {
		arms = []StatCell{}
	}
	o.Set("arms", arms)
	if r.Monthly {
		o.Set("note", r.Note)
	}
	return o.MarshalJSON()
}

func armLabel(arm any) string { return jsString(at(arm, "label")) }

// BuiltInRows is builtInRows: every built-in stat row, without the monthly row.
func BuiltInRows(experiment any, baseline BaselineOptions) []StatRow {
	experiment = toLoose(experiment)
	control := ControlSummary(experiment)
	arms := list(at(experiment, "arms"))
	floor := noiseFloor(experiment)
	replay := ReplayBaseline(experiment, baseline)
	split := CostSplitShown(experiment)
	var rows []StatRow
	for _, row := range StatRowDefs {
		if !split && costSplitKeys[row.Key] {
			continue
		}
		base := at(control, row.Key)
		r := StatRow{Key: row.Key, Label: row.Label, Control: row.Format(base), Arms: []StatCell{}}
		if floor != nil {
			r.HasFloor = true
			r.Floor = FloorStatOf(row, floor)
		}
		// An arm is compared with the control over the cases it has results
		// for; when that covers other cases than the control column, the cell
		// carries those cases and the control's figure over them.
		for _, arm := range arms {
			value := at(arm, "summary", row.Key)
			cell := StatCell{Label: armLabel(arm), Text: row.Format(value)}
			switch {
			case replayBaselineKeys[row.Key] && replay != nil && !strictEqual(arm, replay):
				cell.Delta = StatDelta(row.Key, value, at(replay, "summary", row.Key), "replay")
			case row.Key == "latency_ms_p50":
			case row.Key == "same_keys_rate":
				if c := SameKeysCellOf(at(arm, "summary")); c != nil {
					cell.Text = c.Text
					cell.Delta = c.Sub
					cell.Title = ptr(c.Title)
				}
			default:
				matched := at(arm, "control_summary")
				own := base
				if truthy(matched) {
					own = at(matched, row.Key)
				}
				cell.Delta = StatDelta(row.Key, value, own, "")
				if cell.Delta != nil && *cell.Delta != "" && narrowerBase(matched, control) {
					cell.SameCases = numPtr(at(matched, "cases"))
					cell.SameBase = ptr(row.Format(own))
				}
			}
			r.Arms = append(r.Arms, cell)
		}
		rows = append(rows, r)
	}
	return rows
}

// StatRowsOptions are statRows' options: a monthly volume for the monthly
// row, and where the replayed control comes from.
type StatRowsOptions struct {
	Volume *MonthlyVolume
	BaselineOptions
}

// StatRowsOf is statRows: one row per built-in stat, the control's value,
// then each arm's value and its ratio or difference against the control
// where both exist. Given a monthly volume, a monthly cost row follows cost
// per call.
func StatRowsOf(experiment any, options StatRowsOptions) []StatRow {
	experiment = toLoose(experiment)
	rows := BuiltInRows(experiment, options.BaselineOptions)
	if monthly := MonthlyRow(experiment, options.Volume, options.BaselineOptions); monthly != nil {
		i := slices.IndexFunc(rows, func(r StatRow) bool { return r.Key == "cost_per_call_usd" }) + 1
		rows = slices.Insert(rows, i, *monthly)
	}
	return rows
}

// --- Monthly cost -------------------------------------------------------------

// MonthlyVolume is the calls a month, and optionally what they cost, that
// the projection multiplies by.
type MonthlyVolume struct {
	Calls    float64
	SpendUSD *float64
	// The sample is a slice of the volume (filtered by label).
	Labelled bool
}

func (v *MonthlyVolume) calls() (float64, bool) {
	if v == nil || math.IsNaN(v.Calls) || math.IsInf(v.Calls, 0) || v.Calls <= 0 {
		return 0, false
	}
	return v.Calls, true
}

// MonthlyDelta is monthlyDelta.
func MonthlyDelta(value, base *float64) *string {
	if value == nil || base == nil {
		return nil
	}
	diff := *value - *base
	if *base != 0 && math.Abs(diff / *base) < 0.005 {
		return ptr("same")
	}
	if diff < 0 {
		return ptr("saves " + FormatUSD(-diff) + "/mo")
	}
	return ptr("adds " + FormatUSD(diff) + "/mo")
}

// MeanRange is meanRange: the range the sample's mean cost per call could
// plausibly take, about two standard errors either side. Nil below two values.
func MeanRange(values []any) *[2]float64 {
	nums := finiteNumbers(values)
	if len(nums) < 2 {
		return nil
	}
	n := float64(len(nums))
	m := sumOf(nums) / n
	squares := 0.0
	for _, v := range nums {
		squares += float64((v - m) * (v - m))
	}
	sd := math.Sqrt(squares / (n - 1))
	half := math.Max(float64(2*sd)/math.Sqrt(n), float64(m*0.005))
	return &[2]float64{m - half, m + half}
}

// MonthlyNote is what the monthly figures rest on (Basis), and whether the
// control's projection matches what the calls actually cost (Text).
type MonthlyNote struct {
	Basis   string
	Text    *string
	Typical *bool
}

// MonthlyNoteOf is monthlyNote.
func MonthlyNoteOf(experiment any, volume *MonthlyVolume, controlPerCall *float64) *MonthlyNote {
	calls, ok := volume.calls()
	if !ok {
		return nil
	}
	basis := FormatCount(calls) + " calls in 30 days"
	if volume.Labelled {
		basis += ", all labels"
	}
	spend := volume.SpendUSD
	if controlPerCall == nil || spend == nil || math.IsNaN(*spend) || math.IsInf(*spend, 0) {
		return &MonthlyNote{Basis: basis}
	}
	var costs []any
	for _, c := range list(at(toLoose(experiment), "cases")) {
		costs = append(costs, at(c, "control", "cost_usd"))
	}
	r := MeanRange(costs)
	if r == nil {
		return &MonthlyNote{Basis: basis}
	}
	projected := float64(*controlPerCall * calls)
	average := *spend / calls
	if average >= r[0] && average <= r[1] {
		return &MonthlyNote{
			Basis:   basis,
			Text:    ptr("The control's " + FormatUSD(projected) + " is in line with the " + FormatUSD(*spend) + " these calls actually cost."),
			Typical: ptr(true),
		}
	}
	direction := "more"
	if *controlPerCall < average {
		direction = "less"
	}
	return &MonthlyNote{
		Basis:   basis,
		Text:    ptr("The sampled calls cost " + direction + " than the average call (" + FormatUSD(*controlPerCall) + " against " + FormatUSD(average) + "), so compare the treatments with each other, not with your bill."),
		Typical: ptr(false),
	}
}

// MonthlyRow is monthlyRow: each arm's measured cost per call times the
// calls a month. Nil with no volume: never estimated.
func MonthlyRow(experiment any, volume *MonthlyVolume, baseline BaselineOptions) *StatRow {
	calls, ok := volume.calls()
	if !ok {
		return nil
	}
	experiment = toLoose(experiment)
	control := ControlSummary(experiment)
	base := numPtr(at(control, "cost_per_call_usd"))
	monthly := func(perCall any) *float64 {
		if perCall == nil {
			return nil
		}
		return ptr(float64(jsNumber(perCall) * calls))
	}
	note := MonthlyNoteOf(experiment, volume, base)
	replay := ReplayBaseline(experiment, baseline)
	row := &StatRow{Key: "monthly_cost_usd", Label: "Monthly cost", Monthly: true, Arms: []StatCell{}}
	if note != nil {
		row.Sublabel = ptr(note.Basis)
		if note.Text != nil && *note.Text != "" {
			row.Note = &MonthlyRowNote{Text: *note.Text, Typical: note.Typical}
		}
	}
	row.Control = FormatUSD(monthly(toLoose(base)))
	for _, arm := range list(at(experiment, "arms")) {
		value := monthly(at(arm, "summary", "cost_per_call_usd"))
		cell := StatCell{Label: armLabel(arm), Text: FormatUSD(value)}
		if replay != nil && !strictEqual(arm, replay) {
			cell.Delta = MonthlyDelta(value, monthly(at(replay, "summary", "cost_per_call_usd")))
		} else {
			matched := at(arm, "control_summary")
			ownPerCall := toLoose(base)
			if truthy(matched) {
				ownPerCall = at(matched, "cost_per_call_usd")
			}
			own := monthly(ownPerCall)
			cell.Delta = MonthlyDelta(value, own)
			if cell.Delta != nil && *cell.Delta != "" && *cell.Delta != "same" && narrowerBase(matched, control) {
				cell.SameCases = numPtr(at(matched, "cases"))
				cell.SameBase = ptr(FormatUSD(own))
			}
		}
		row.Arms = append(row.Arms, cell)
	}
	return row
}

// --- Spread -------------------------------------------------------------------

// outputChars is the output's length in code points; unknown for a stored prefix.
func outputChars(result any) any {
	if truthy(at(result, "output_truncated")) {
		return nil
	}
	return float64(codePointLength(OutputText(at(result, "output"))))
}

type spreadRowDef struct {
	key    string
	value  func(result any) any
	center string
	format func(any) string
}

// The tick sits at the figure the row prints: cost per call is a mean (a bill is a sum), the others medians.
var spreadRows = []spreadRowDef{
	{"cost_per_call_usd", func(r any) any { return at(r, "cost_usd") }, "mean", FormatUSD},
	{"latency_ms_p50", func(r any) any { return at(r, "latency_ms") }, "median", FormatMs},
	{"output_tokens_p50", func(r any) any { return at(r, "output_tokens") }, "median", FormatCount},
	{"output_chars_p50", outputChars, "median", FormatCount},
}

// SpreadKeys is SPREAD_KEYS: the stats a spread is drawn for.
var SpreadKeys = []string{"cost_per_call_usd", "latency_ms_p50", "output_tokens_p50", "output_chars_p50"}

// SpreadRange is a 10th to 90th percentile range and its centre.
type SpreadRange struct {
	Low    float64 `json:"low"`
	High   float64 `json:"high"`
	Center float64 `json:"center"`
}

// SpreadSide is one side's spread on one row.
type SpreadSide struct {
	N     int          `json:"n"`
	Range *SpreadRange `json:"range"`
	Text  *string      `json:"text"`
	Note  *string      `json:"note"`
}

// SpreadOf is spreadOf: the 10th to 90th percentile of the finite numbers
// among values, centred on their mean or median.
func SpreadOf(values []any, center string, format func(any) string) SpreadSide {
	sorted := sortNumbers(finiteNumbers(values))
	n := len(sorted)
	if n < SpreadMin {
		side := SpreadSide{N: n}
		if n > 0 {
			word := "calls"
			if n == 1 {
				word = "call"
			}
			side.Note = ptr("Only " + FormatCount(n) + " " + word + ", too few for a range")
		}
		return side
	}
	low := quantileOf(sorted, 0.1)
	high := quantileOf(sorted, 0.9)
	mid := quantileOf(sorted, 0.5)
	if center == "mean" {
		mid = sumOf(sorted) / float64(n)
	}
	return SpreadSide{N: n, Range: &SpreadRange{low, high, mid}, Text: ptr(format(low) + " to " + format(high))}
}

// Scale is a shared axis.
type Scale struct {
	Lo float64 `json:"lo"`
	Hi float64 `json:"hi"`
}

// StatSpread is one spread row across the columns.
type StatSpread struct {
	Control   SpreadSide            `json:"control"`
	Arms      map[string]SpreadSide `json:"arms"`
	Scale     *Scale                `json:"scale"`
	ScaleText *string               `json:"scaleText"`
}

// StatSpreads is statSpreads: each spread row's 10th to 90th percentile per
// side, over the replayed cases: the control's production calls, and each
// arm's calls that returned. Every side in a row shares one scale from zero
// to the row's highest value.
func StatSpreads(experiment any) map[string]StatSpread {
	experiment = toLoose(experiment)
	var cases []any
	for _, c := range list(at(experiment, "cases")) {
		if replayedCase(c) {
			cases = append(cases, c)
		}
	}
	var labels []string
	for _, arm := range list(at(experiment, "arms")) {
		labels = append(labels, armLabel(arm))
	}
	var controls []any
	for _, c := range cases {
		if control := at(c, "control"); truthy(control) {
			controls = append(controls, control)
		}
	}
	results := map[string][]any{}
	for _, l := range labels {
		var rs []any
		for _, c := range cases {
			if r := at(c, "arms", l); truthy(r) && !truthy(at(r, "error")) {
				rs = append(rs, r)
			}
		}
		results[l] = rs
	}
	out := map[string]StatSpread{}
	for _, row := range spreadRows {
		read := func(items []any) []any {
			values := make([]any, len(items))
			for i, r := range items {
				values[i] = row.value(r)
			}
			return values
		}
		control := SpreadOf(read(controls), row.center, row.format)
		arms := map[string]SpreadSide{}
		for _, l := range labels {
			arms[l] = SpreadOf(read(results[l]), row.center, row.format)
		}
		drawn := []SpreadSide{control}
		for _, side := range arms {
			drawn = append(drawn, side)
		}
		hi := 0.0
		anyDrawn := false
		for _, side := range drawn {
			if side.Range != nil {
				anyDrawn = true
				hi = jsMax(hi, side.Range.High, side.Range.Center)
			}
		}
		s := StatSpread{Control: control, Arms: arms}
		if anyDrawn {
			h := hi
			if !truthy(h) {
				h = 1
			}
			s.Scale = &Scale{0, h}
		}
		if hi > 0 {
			s.ScaleText = ptr("Bars from zero to " + row.format(hi))
		}
		out[row.key] = s
	}
	return out
}

// jsMax is Math.max: NaN wins, +0 beats -0.
func jsMax(values ...float64) float64 {
	m := math.Inf(-1)
	for _, v := range values {
		m = math.Max(m, v)
	}
	return m
}

// jsMin is Math.min.
func jsMin(values ...float64) float64 {
	m := math.Inf(1)
	for _, v := range values {
		m = math.Min(m, v)
	}
	return m
}

// --- Noise floor --------------------------------------------------------------

// noiseFloor is the view's noise floor, or nil when the run did not measure one.
func noiseFloor(experiment any) any {
	floor := at(toLoose(experiment), "noise_floor")
	switch floor.(type) {
	case *Object, map[string]any, []any:
		return floor
	}
	return nil
}

// FloorLabel is FLOOR_LABEL.
const FloorLabel = "Control vs itself"

// FloorReading is the control against itself on one field, read the way a
// treatment is read against the control.
type FloorReading struct {
	PairedReading
	Path  string
	Label string
	Kind  FieldKind
	Pairs int
}

// MarshalJSON writes the reading in the page's shape.
func (f FloorReading) MarshalJSON() ([]byte, error) {
	o := obj("path", f.Path, "label", f.Label, "kind", string(f.Kind), "pairs", float64(f.Pairs))
	f.PairedReading.set(o)
	return o.MarshalJSON()
}

// FloorField is floorField: the control against itself on one field, the
// second replay read against the first, with the same reading a treatment
// gets against the control. An empty kind reads the kind from the values.
// Nil when the run has no floor or no case has both replays' values.
func FloorField(experiment any, path string, kind FieldKind) *FloorReading {
	experiment = toLoose(experiment)
	floor := noiseFloor(experiment)
	if floor == nil {
		return nil
	}
	cases := list(at(experiment, "cases"))
	indexes := ReplayedIndexes(cases)
	series := at(floor, "fields", path)
	var pairs []FieldPair
	for i, c := range cases {
		replays := at(c, "noise_floor")
		if !truthy(replays) || truthy(at(replays, "a1", "error")) || truthy(at(replays, "a2", "error")) {
			continue
		}
		p := FieldPair{
			TraceID: at(c, "trace_id"),
			Control: SideValue(at(replays, "a1"), path, at(series, "a1"), indexes[i]),
			Arm:     SideValue(at(replays, "a2"), path, at(series, "a2"), indexes[i]),
		}
		if p.Control != nil && p.Arm != nil {
			pairs = append(pairs, p)
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	own := kind
	if own == "" {
		var values []any
		for _, p := range pairs {
			values = append(values, p.Control, p.Arm)
		}
		own = FieldKindOf(values)
	}
	return &FloorReading{Path: path, Label: FloorLabel, Kind: own, Pairs: len(pairs), PairedReading: PairedReadingOf(pairs, own)}
}

// FloorRange is floorRange: the control's per-call difference from itself,
// both ends to one precision.
func FloorRange(min, max float64) string {
	abs := math.Max(math.Abs(min), math.Abs(max))
	digits := 2
	if abs >= 100 {
		digits = 0
	} else if abs >= 10 {
		digits = 1
	}
	fixed := func(v any) string { f, _ := finite(v); return jsnum.ToFixed(f, digits) }
	return FormatSigned(min, fixed) + " to " + FormatSigned(max, fixed)
}

// FloorFieldText is floorFieldText: a floor reading in a few words, nil
// when it has none.
func FloorFieldText(f *FloorReading) *string {
	switch f.Kind {
	case KindNumber:
		if f.DiffBox == nil {
			return nil
		}
		return ptr(FloorRange(f.DiffBox.Min, f.DiffBox.Max))
	case KindLabel:
		return ptr(FormatPercent(f.Rate) + " agreement")
	case KindText, KindLongText:
		return ptr(FormatPercent(f.Rate) + " exact match")
	case KindList:
		return ptr(FormatPercent(f.Overlap) + " item overlap")
	}
	return nil
}

// FloorNote is one metric's noise floor in words.
type FloorNote struct {
	Text  string `json:"text"`
	Title string `json:"title"`
}

// NoiseFloors is noiseFloors: every floor the run measured, one per metric,
// to show beside each treatment's figure: `field:<path>` for each tracked
// field, and some built-in stats by key. A metric with nothing measured is
// left out.
func NoiseFloors(experiment any, tracked []string) map[string]*FloorNote {
	experiment = toLoose(experiment)
	out := map[string]*FloorNote{}
	floor := noiseFloor(experiment)
	if floor == nil {
		return out
	}
	title := func(n any) string {
		word := "traces"
		if f, _ := finite(n); f == 1 {
			word = "trace"
		}
		return "Control against its own replay over " + FormatCount(n) + " " + word
	}
	var labels []string
	for _, arm := range list(at(experiment, "arms")) {
		labels = append(labels, armLabel(arm))
	}
	for _, path := range tracked {
		kind, _ := FieldComparisons(at(experiment, "cases"), path, labels, at(experiment, "fields"))
		if kind == KindMixed {
			kind = ""
		}
		f := FloorField(experiment, path, kind)
		if f == nil {
			continue
		}
		if text := FloorFieldText(f); text != nil && *text != "" {
			out["field:"+path] = &FloorNote{*text, title(f.Pairs)}
		}
	}
	for _, row := range StatRowDefs {
		switch row.Key {
		case "same_keys_rate", "output_chars_p50", "cost_per_call_usd", "latency_ms_p50":
		default:
			continue
		}
		if stat := FloorStatOf(row, floor); stat != nil {
			text := stat.Delta
			if stat.Text != nil && *stat.Text != "" {
				text = *stat.Text + " " + stat.Delta
			}
			cases, _ := get(floor, "cases")
			out[row.Key] = &FloorNote{text, title(cases)}
		}
	}
	return out
}

// FloorClause is floorClause: the clause the shift sentence adds for the
// floor, in the treatment's own terms; empty when nothing compares.
func FloorClause(floor *FloorReading, kind FieldKind) string {
	if floor == nil || floor.Pairs == 0 || floor.Kind != kind {
		return ""
	}
	switch kind {
	case KindNumber:
		if d := floor.DiffBox; d != nil {
			return "the control differs from itself by " + FloorRange(d.Min, d.Max)
		}
		return ""
	case KindLabel:
		return "the control gives the same label as itself on " + intText(floor.Matched) + " of " + strconv.Itoa(floor.Pairs)
	case KindText, KindLongText:
		return "the control matches itself exactly on " + intText(floor.Matched) + " of " + strconv.Itoa(floor.Pairs)
	case KindList:
		return "the control shares " + FormatPercent(floor.Overlap) + " with itself"
	}
	return ""
}

// intText is a count in a template literal: "null" when there is none.
func intText(n *int) string {
	if n == nil {
		return "null"
	}
	return strconv.Itoa(*n)
}

// ShiftSentence is shiftSentence: the one sentence a tracked field may
// carry, shift and overlap, never a verdict. With a noise floor it names the
// control's own gap beside the treatment's instead of the overlap of the two
// middle halves.
func ShiftSentence(comparison *FieldComparison, floor *FloorReading) string {
	if comparison == nil || comparison.Pairs == 0 {
		return "No case has a value on both sides yet."
	}
	l, kind, pairs := comparison.Label, comparison.Kind, strconv.Itoa(comparison.Pairs)
	clause := FloorClause(floor, kind)
	switch kind {
	case KindNumber:
		var median any
		if comparison.DiffBox != nil {
			median = comparison.DiffBox.Median
		}
		if clause != "" {
			return l + " differs from control by a median " + FormatSigned(median, nil) + " per call across " + pairs + " cases; " + clause + "."
		}
		overlaps := "do not overlap"
		if MiddleHalvesOverlap(comparison.ControlBox, comparison.ArmBox) {
			overlaps = "overlap"
		}
		return l + " differs from control by a median " + FormatSigned(median, nil) + " per call across " + pairs + " cases; the middle halves of the two " + overlaps + "."
	}
	tail := "."
	if clause != "" {
		tail = "; " + clause + "."
	}
	switch kind {
	case KindLabel:
		return l + " gives the same label as control on " + intText(comparison.Matched) + " of " + pairs + " cases" + tail
	case KindText, KindLongText:
		return l + " matches control exactly on " + intText(comparison.Matched) + " of " + pairs + " cases" + tail
	case KindList:
		return l + " shares " + FormatPercent(comparison.Overlap) + " of list items with control on average across " + pairs + " cases" + tail
	}
	return l + " returns values of more than one type here, so they are listed per case only."
}

// MiddleHalvesOverlap is middleHalvesOverlap: whether the control's and the
// arm's interquartile ranges meet.
func MiddleHalvesOverlap(control, arm *BoxStats) bool {
	return control != nil && arm != nil && arm.Q1 <= control.Q3 && control.Q1 <= arm.Q3
}

// FieldSentence is fieldSentence: the shift sentence, then how many cases
// had no value on one side.
func FieldSentence(comparison *FieldComparison, floor *FloorReading) string {
	missing := ""
	if comparison != nil && comparison.Missing > 0 {
		s := "s"
		if comparison.Missing == 1 {
			s = ""
		}
		missing = " " + strconv.Itoa(comparison.Missing) + " case" + s + " had no value on one side."
	}
	return ShiftSentence(comparison, floor) + missing
}
