// Package report renders a run for the terminal: the report model (every
// figure and sentence the report shows, as plain data) and the answer-first
// text report drawn from it. The explorer reads the same model.
//
// Every figure and sentence comes
// from core, read with the noise floor's first replay (A1) as the replayed
// control. Presentation, never a verdict.
package report

import (
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/metergraph/ezswap/internal/core"
	"github.com/metergraph/ezswap/internal/jsnum"
	"github.com/metergraph/ezswap/internal/types"
)

// NoiseFloorDoc explains the noise floor.
const NoiseFloorDoc = "https://github.com/metergraph/ezswap/blob/main/docs/noise-floor.md"

// SummaryStats are the built-in statistics the summary shows, in order.
var SummaryStats = []string{
	"monthly_cost_usd",
	"cost_per_call_usd",
	"latency_ms_p50",
	"output_chars_p50",
	"json_parse_rate",
	"same_keys_rate",
	"empty_or_refused_rate",
	"cut_off_rate",
	"errors",
}

// Statistics read against production's model replayed (A1) rather than its recorded call.
var replayRead = map[string]bool{"monthly_cost_usd": true, "cost_per_call_usd": true, "latency_ms_p50": true}

var labelWords = map[string]string{
	"within_noise":   "within noise",
	"beyond_noise":   "beyond noise",
	"too_few_calls":  "too few calls",
	"not_comparable": "not comparable",
}

// LabelCell is a within-noise label as the report shows it. Tone is
// "within", "beyond" or "none".
type LabelCell struct {
	Text  string `json:"text"`
	Tone  string `json:"tone"`
	Title string `json:"title"`
}

// Cell is one treatment's figure on one row.
type Cell struct {
	Text string `json:"text"`
	// The shift against production (or its replay), in core's words.
	Delta *string `json:"delta"`
	// Production's own gap on this figure, in core's words.
	Floor *string    `json:"floor"`
	Title *string    `json:"title"`
	Label *LabelCell `json:"label"`
}

// Row is one statistic or tracked field across the treatments. Kind is
// "stat" or "field".
type Row struct {
	Key            string  `json:"key"`
	Label          string  `json:"label"`
	Sublabel       *string `json:"sublabel"`
	Kind           string  `json:"kind"`
	Production     string  `json:"production"`
	ProductionNote *string `json:"productionNote"`
	// Production against itself (A2 against A1), the row's noise floor.
	Floor *string `json:"floor"`
	Cells []Cell  `json:"cells"`
	// Per treatment: the full sentence, for fields.
	Sentences []string `json:"sentences"`
	Note      *string  `json:"note"`
}

// Column is one treatment.
type Column struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Model    string `json:"model"`
	Settings string `json:"settings"`
	Endpoint string `json:"endpoint"`
	// Cases this treatment returned a result on.
	N    *float64 `json:"n"`
	Note *string  `json:"note"`
}

// ChartPoint is one arm on a frontier chart.
type ChartPoint struct {
	Label      string   `json:"label"`
	X          float64  `json:"x"`
	XLow       *float64 `json:"xLow"`
	XHigh      *float64 `json:"xHigh"`
	XText      string   `json:"xText"`
	Y          float64  `json:"y"`
	YLow       *float64 `json:"yLow"`
	YHigh      *float64 `json:"yHigh"`
	YText      string   `json:"yText"`
	N          int      `json:"n"`
	Control    bool     `json:"control"`
	OnFrontier bool     `json:"onFrontier"`
}

// ChartFloor is production against itself on a chart's metric, drawn as a reference line.
type ChartFloor struct {
	Y    float64 `json:"y"`
	Text string  `json:"text"`
}

// FrontierChart is cost against one metric. Better is "lower", "higher" or
// nil; Format is "usd", "ms", "percent" or "signed".
type FrontierChart struct {
	Key      string       `json:"key"`
	Label    string       `json:"label"`
	Better   *string      `json:"better"`
	Format   string       `json:"format"`
	Points   []ChartPoint `json:"points"`
	Floor    *ChartFloor  `json:"floor"`
	Frontier []string     `json:"frontier"`
}

// FieldChange is one tracked field whose value on an arm differs from production's on a case.
type FieldChange struct {
	Path string `json:"path"`
	From string `json:"from"`
	To   string `json:"to"`
}

// ArmOutput is one arm's result on one case. Status is "ok", "failed" or "missing".
type ArmOutput struct {
	Label        string   `json:"label"`
	Status       string   `json:"status"`
	Output       *string  `json:"output"`
	Pretty       *string  `json:"pretty"`
	Error        *string  `json:"error"`
	Flags        []string `json:"flags"`
	Latency      string   `json:"latency"`
	Cost         string   `json:"cost"`
	FinishReason *string  `json:"finishReason"`
	Differs      bool     `json:"differs"`
	// Tracked fields whose value differs from production's, read as the field's statistics read it.
	Changes []FieldChange `json:"changes"`
}

// CaseItem is one sampled case in the case browser. Status is "replayed"
// or "not_replayable".
type CaseItem struct {
	ID      string  `json:"id"`
	TraceID string  `json:"traceId"`
	Status  string  `json:"status"`
	Reason  *string `json:"reason"`
	Input   *string `json:"input"`
	// The request as replayed: folded system turns, then the text turns.
	System      *string         `json:"system"`
	Messages    []types.Message `json:"messages"`
	Limitations []string        `json:"limitations"`
	Production  ArmOutput       `json:"production"`
	Arms        []ArmOutput     `json:"arms"`
	Floor       []ArmOutput     `json:"floor"`
	Differs     bool            `json:"differs"`
}

// ArmRole is which arm a figure belongs to: production (its replay when
// there is a floor), a treatment, or production against itself.
type ArmRole = string

const (
	RoleProduction ArmRole = "production"
	RoleTreatment  ArmRole = "treatment"
	RoleFloor      ArmRole = "floor"
)

// CostBar is one arm's cost.
type CostBar struct {
	Role  ArmRole `json:"role"`
	Label string  `json:"label"`
	// Short name for narrow columns.
	Name    string   `json:"name"`
	Model   string   `json:"model"`
	PerCall *float64 `json:"perCall"`
	Text    string   `json:"text"`
	// The change against production's replayed model, in core's words.
	Delta *string `json:"delta"`
	// Production's recorded figure, beside its replay.
	Note         *string    `json:"note"`
	Monthly      *float64   `json:"monthly"`
	MonthlyText  *string    `json:"monthlyText"`
	MonthlyDelta *string    `json:"monthlyDelta"`
	Badge        *LabelCell `json:"badge"`
}

// LatencyRow is one arm's latency.
type LatencyRow struct {
	Role  ArmRole `json:"role"`
	Label string  `json:"label"`
	Name  string  `json:"name"`
	// Calls with a latency.
	N     int        `json:"n"`
	P10   *float64   `json:"p10"`
	P50   *float64   `json:"p50"`
	P90   *float64   `json:"p90"`
	Text  string     `json:"text"`
	Delta *string    `json:"delta"`
	Badge *LabelCell `json:"badge"`
	// Every call's latency, for a distribution.
	Values []float64 `json:"values"`
}

// Range is the middle half of a per-call shift.
type Range struct {
	Lo  float64 `json:"lo"`
	Mid float64 `json:"mid"`
	Hi  float64 `json:"hi"`
}

// LabelChange is how often production's label became this arm's.
type LabelChange struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

// FieldArm is one treatment on one tracked field.
type FieldArm struct {
	Label string `json:"label"`
	Name  string `json:"name"`
	// Agreement (0..1) for labels, text and lists; the median shift for numbers.
	Value     *float64   `json:"value"`
	ValueText string     `json:"valueText"`
	Detail    string     `json:"detail"`
	Sentence  string     `json:"sentence"`
	Badge     *LabelCell `json:"badge"`
	// Numbers: the middle half of the per-call shift.
	Range *Range `json:"range"`
	// Labels: how production's label became this arm's, where it changed, most common first.
	Changes []LabelChange `json:"changes"`
}

// FieldFloor is production against itself on one tracked field.
type FieldFloor struct {
	Value *float64 `json:"value"`
	Text  *string  `json:"text"`
	Low   *float64 `json:"low"`
	High  *float64 `json:"high"`
}

// FieldPanel is one tracked field across the treatments. Scale is "share"
// (agreement from 0 to 1), "shift" (a signed per-call difference) or "none".
type FieldPanel struct {
	Path      string      `json:"path"`
	Kind      string      `json:"kind"`
	KindLabel string      `json:"kindLabel"`
	Scale     string      `json:"scale"`
	Floor     *FieldFloor `json:"floor"`
	Arms      []FieldArm  `json:"arms"`
}

// FloorStatRow is one built-in statistic on the floor: A1 beside A2 and their gap.
type FloorStatRow struct {
	Label string `json:"label"`
	A1    string `json:"a1"`
	A2    string `json:"a2"`
	Gap   string `json:"gap"`
}

// FloorMismatch is one call where A1 and A2 disagreed on a field.
type FloorMismatch struct {
	TraceID string `json:"traceId"`
	A1      string `json:"a1"`
	A2      string `json:"a2"`
}

// FloorFieldRow is one tracked field on the floor.
type FloorFieldRow struct {
	Path       string          `json:"path"`
	Text       *string         `json:"text"`
	Mismatches []FloorMismatch `json:"mismatches"`
}

// FloorPanel is production against itself.
type FloorPanel struct {
	Cases  int             `json:"cases"`
	Model  string          `json:"model"`
	Spent  string          `json:"spent"`
	Text   []string        `json:"text"`
	Stats  []FloorStatRow  `json:"stats"`
	Fields []FloorFieldRow `json:"fields"`
}

// MetaItem is one line of the run's metadata.
type MetaItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// NotReplayable is one reason calls could not be replayed.
type NotReplayable struct {
	Reason string `json:"reason"`
	Title  string `json:"title"`
	Count  int    `json:"count"`
	Fix    string `json:"fix"`
}

// Model is everything the report shows.
type Model struct {
	Headline        string          `json:"headline"`
	Subhead         string          `json:"subhead"`
	Meta            []MetaItem      `json:"meta"`
	Stopped         *string         `json:"stopped"`
	ProductionModel string          `json:"productionModel"`
	Columns         []Column        `json:"columns"`
	Rows            []Row           `json:"rows"`
	HasFloor        bool            `json:"hasFloor"`
	Floor           *FloorPanel     `json:"floor"`
	Labels          bool            `json:"labels"`
	Charts          []FrontierChart `json:"charts"`
	Cases           []CaseItem      `json:"cases"`
	NotReplayable   []NotReplayable `json:"notReplayable"`
	// Calls a month the projection uses, or nil when unknown.
	MonthlyCalls *float64     `json:"monthlyCalls"`
	Cost         []CostBar    `json:"cost"`
	Latency      []LatencyRow `json:"latency"`
	Fields       []FieldPanel `json:"fields"`
	// Production's recorded median latency, which is not compared. Nil when
	// the run has no floor or the source recorded no durations (a median <= 0).
	RecordedLatency *string `json:"recordedLatency"`

	// Not in the frozen model fixtures. Noise holds each treatment's within-noise
	// labels whether or not Labels is on (nil without a floor), so the
	// summary can mark figures beyond production's own variation.
	Noise []*core.NoiseLabels `json:"-"`
	// The run and its view, for the header and the explorer.
	Run  *types.RunFile       `json:"-"`
	View *core.ExperimentView `json:"-"`
}

// Options are Build's options.
type Options struct {
	// Tracked JSON field paths to show; nil means the run's tracked fields.
	Tracked []string
	// Show within-noise labels (opt-in).
	Labels bool
	// Calls a month to project a monthly cost from; nil reads the run's traffic.
	MonthlyCalls *float64
}

// MonthlyCalls is the calls a month: the volume the user gave
// (--monthly-calls), else the run's input traffic (calls a day over the
// file's span, times 30). Nil when unknown.
func MonthlyCalls(run *types.RunFile) *float64 {
	if g := run.Settings.MonthlyCalls; g != nil && !math.IsNaN(*g) && !math.IsInf(*g, 0) && *g > 0 {
		return ptr(jsnum.Round(*g))
	}
	traffic := run.Input.Traffic
	if traffic == nil || traffic.Calls <= 0 || traffic.From == nil || traffic.To == nil || *traffic.From == "" || *traffic.To == "" {
		return nil
	}
	from, ok1 := parseDate(*traffic.From)
	to, ok2 := parseDate(*traffic.To)
	if !ok1 || !ok2 {
		return nil
	}
	days := float64(to.Sub(from).Milliseconds()) / 86_400_000
	if days < 1 {
		return nil
	}
	return ptr(jsnum.Round(float64(traffic.Calls) / days * 30))
}

type reasonText struct{ title, fix string }

var reasonTexts = map[string]reasonText{
	"content_unavailable":          {"No content captured", "The trace carries no prompt or response text. Turn on content capture in your tracing setup and export again."},
	"content_truncated":            {"Content cut short", "The exporter cut the content, so the replay would send a different prompt. Raise the exporter's attribute or payload size limit and export again."},
	"tool_history":                 {"Earlier tool calls in the conversation", "A replay cannot rerun the tools that answered earlier turns. Sample calls that start from a user message, before any tool runs."},
	"unsupported_content":          {"Images, audio or files", "Only text turns replay in this version. Calls with text-only messages replay as they are."},
	"no_messages":                  {"No messages", "The request has no text turns to send. Check that the adapter matches the way the file was exported."},
	"last_turn_not_user":           {"Last turn is not from the user", "The request ends with an assistant turn, so there is nothing to answer. Export the request exactly as it was sent."},
	"input_too_large":              {"Input too large", "The input is larger than a replay sends (64 KiB). Sample shorter calls."},
	"tool_definitions_not_offered": {"Forced choice among tools", "Production forced a choice among several tools, which a replay that offers no tools cannot reproduce. Calls that force a single tool, or offer none, replay."},
	"web_search_not_replayed":      {"Web search", "Production searched the web on these calls and a replay does not. Calls without web search replay."},
	"unparseable_request":          {"Request could not be read", "The request body is not in a shape any adapter reads. See docs/trace-format.md for the accepted formats."},
}

var flagTexts = map[string]string{
	"temperature_dropped":     "temperature not accepted, sent without it",
	"response_format_dropped": "response format not accepted, sent without it",
	"maybe_billed":            "may have been billed though it failed",
	"retried":                 "retried",
}

var months = []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}

// parseDate reads the ISO dates Date.parse reads in practice.
func parseDate(iso string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, iso); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func utc(iso string) string {
	d, ok := parseDate(iso)
	if !ok {
		return iso
	}
	return pad2Date(d)
}

func pad2Date(d time.Time) string {
	return jsnum.String(float64(d.Day())) + " " + months[d.Month()-1] + " " + jsnum.String(float64(d.Year())) + ", " +
		d.Format("15:04") + " UTC"
}

func labelCell(label *core.NoiseLabel) *LabelCell {
	if label == nil {
		return nil
	}
	words := labelWords[label.Label]
	text := words
	if label.Label == "beyond_noise" && label.Direction != nil && *label.Direction != "" && *label.Direction != "different" {
		text = words + ", " + *label.Direction
	}
	tone := "none"
	switch label.Label {
	case "within_noise":
		tone = "within"
	case "beyond_noise":
		tone = "beyond"
	}
	var title string
	if label.Label == "too_few_calls" {
		title = core.FormatCount(label.Cases) + " paired cases; a label needs 10."
	} else {
		title = "Over " + core.FormatCount(label.Cases) + " paired cases. The treatment's gap is compared with production's gap from itself; neither says which is better."
	}
	return &LabelCell{Text: text, Tone: tone, Title: title}
}

func prettyOutput(output *string) *string {
	if output == nil {
		return nil
	}
	parsed := core.ParseOutput(*output)
	if !parsed.OK || parsed.Value == nil {
		return nil
	}
	switch parsed.Value.(type) {
	case *core.Object, []any, map[string]any:
		return ptr(core.Stringify(parsed.Value, "  "))
	}
	return nil
}

// jsString is String(value) for a loose value.
func jsString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return jsnum.String(t)
	case string:
		return t
	case []any:
		parts := make([]string, len(t))
		for i, item := range t {
			if item != nil {
				parts[i] = jsString(item)
			}
		}
		return strings.Join(parts, ",")
	case *core.Object, map[string]any:
		return "[object Object]"
	}
	return jsString(core.ToLoose(v))
}

// valuesDiffer is whether one arm's value on one field differs from
// production's, read the way the field's statistics read it.
func valuesDiffer(kind core.FieldKind, control, arm any) bool {
	switch kind {
	case core.KindNumber:
		a, ok1 := control.(float64)
		b, ok2 := arm.(float64)
		if ok1 && ok2 {
			return a != b
		}
		return true
	case core.KindList:
		return core.Overlap(control, arm) < 1
	}
	return strings.TrimSpace(jsString(control)) != strings.TrimSpace(jsString(arm))
}

// showValue is a field value as text: strings as they are, anything else as JSON.
func showValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return core.Stringify(value, "")
}

// shortNames are short names for arms: a label that is just the model id
// loses its creator prefix ("google/gemini-2.5-flash-lite" to
// "gemini-2.5-flash-lite"), unless two arms would then share a name.
func shortNames(arms []core.ArmView) []string {
	short := make([]string, len(arms))
	for i, a := range arms {
		short[i] = a.Label
		if a.Label == a.Model {
			if s := core.ShortModel(a.Model); s != "" {
				short[i] = s
			}
		}
	}
	out := make([]string, len(arms))
	for i, name := range short {
		if slices.Index(short, name) != lastIndex(short, name) {
			out[i] = arms[i].Label
		} else {
			out[i] = name
		}
	}
	return out
}

func lastIndex(list []string, s string) int {
	for i := len(list) - 1; i >= 0; i-- {
		if list[i] == s {
			return i
		}
	}
	return -1
}

func spread(values []float64) (p10, p50, p90 *float64) {
	sorted := slices.Clone(values)
	sort.Float64s(sorted)
	return core.Quantile(sorted, 0.1), core.Quantile(sorted, 0.5), core.Quantile(sorted, 0.9)
}

func ptr[T any](v T) *T { return &v }

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func strOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

func get(v any, keys ...string) any {
	for _, k := range keys {
		o, ok := v.(*core.Object)
		if !ok {
			return nil
		}
		v, _ = o.Get(k)
	}
	return v
}

// result is the parts of a case result the case browser reads: an arm's
// ResultView or production's CaseControlView.
type result struct {
	output       *string
	latency      *float64
	cost         *float64
	finishReason *string
	err          *string
	refusal      bool
}

func fromResult(r *core.ResultView) *result {
	if r == nil {
		return nil
	}
	return &result{r.Output, r.LatencyMs, r.CostUSD, r.FinishReason, r.Error, r.Refusal}
}

var cutOffReasons = map[string]bool{"length": true, "max_tokens": true, "max_output_tokens": true}

// Build is everything the report shows, from the run.
func Build(run *types.RunFile, options Options) (*Model, error) {
	view, err := core.BuildView(run, core.BuildViewOptions{})
	if err != nil {
		return nil, err
	}
	lv := core.ToLoose(view)
	lcases := get(lv, "cases")
	lfields := get(lv, "fields")

	tracked := options.Tracked
	if tracked == nil {
		tracked = view.TrackedFields
	}
	if tracked == nil {
		tracked = []string{}
	}
	nf := view.NoiseFloor
	hasFloor := nf != nil
	labelsOn := options.Labels && hasFloor
	arms := view.Arms
	armLabels := make([]string, len(arms))
	for i, a := range arms {
		armLabels[i] = a.Label
	}
	calls := options.MonthlyCalls
	if calls == nil {
		calls = MonthlyCalls(run)
	}
	var volume *core.MonthlyVolume
	if calls != nil && *calls != 0 && !math.IsNaN(*calls) {
		volume = &core.MonthlyVolume{Calls: *calls}
	}
	var a1 *core.Summary
	if nf != nil {
		a1 = &nf.A1
	}
	productionModel := strOr(view.Control.Model, "production's model")

	stats := core.StatRowsOf(lv, core.StatRowsOptions{Volume: volume, BaselineOptions: core.BaselineOptions{FloorAsReplay: true}})
	byKey := map[string]*core.StatRow{}
	for i := range stats {
		byKey[stats[i].Key] = &stats[i]
	}
	floors := core.NoiseFloors(lv, tracked)
	summary := core.EquivalenceSummary(lv, tracked, floors)
	withTracked := lv.(*core.Object).Clone()
	trackedLoose := make([]any, len(tracked))
	for i, p := range tracked {
		trackedLoose[i] = p
	}
	withTracked.Set("tracked_fields", trackedLoose)
	eq := core.Equivalence(withTracked, core.ViaEndpoint).Treatments

	var noise []*core.NoiseLabels
	if hasFloor {
		noise = make([]*core.NoiseLabels, len(arms))
		for i, arm := range arms {
			noise[i] = core.NoiseLabelsOf(lv, arm.Label, tracked)
		}
	}
	labelFor := func(i int, key string) *LabelCell {
		if !labelsOn {
			return nil
		}
		return labelCell(findLabel(noise[i], key))
	}
	summaryRow := func(i int) *core.SummaryRow {
		if summary == nil {
			return nil
		}
		for j := range summary.Rows {
			if summary.Rows[j].Index == i {
				return &summary.Rows[j]
			}
		}
		return nil
	}

	var rows []Row
	for _, key := range SummaryStats {
		row := byKey[key]
		if row == nil {
			continue
		}
		replay := replayRead[key] && hasFloor
		var productionNote *string
		if replay && a1 != nil {
			var replayed string
			switch key {
			case "latency_ms_p50":
				replayed = core.FormatMs(a1.LatencyMsP50)
			case "monthly_cost_usd":
				if a1.CostPerCallUSD == nil || calls == nil || *calls == 0 {
					replayed = core.FormatUSD(nil)
				} else {
					replayed = core.FormatUSD(*a1.CostPerCallUSD * *calls)
				}
			default:
				replayed = core.FormatUSD(a1.CostPerCallUSD)
			}
			productionNote = ptr(replayed + " replayed")
		}
		var floorText *string
		if row.Floor != nil {
			if row.Floor.Text != nil && *row.Floor.Text != "" {
				floorText = ptr(*row.Floor.Text + " " + row.Floor.Delta)
			} else {
				floorText = ptr(row.Floor.Delta)
			}
		}
		var sublabel *string
		switch {
		case key == "monthly_cost_usd":
			s := ""
			if row.Sublabel != nil && *row.Sublabel != "" {
				s = *row.Sublabel + ", "
			}
			sublabel = ptr(s + "projected from the sample's cost per call")
		case replay:
			sublabel = ptr("against production's model replayed")
		case key == "latency_ms_p50":
			sublabel = ptr("not compared with production's recorded latency")
		}
		production := row.Control
		if key == "latency_ms_p50" && hasFloor {
			production = row.Control + " recorded"
		}
		cells := make([]Cell, len(row.Arms))
		for i, cell := range row.Arms {
			var title *string
			if cell.SameCases != nil && *cell.SameCases != 0 {
				title = ptr("Over the " + core.FormatCount(*cell.SameCases) + " cases this treatment returned on, production's figure is " + strOr(cell.SameBase, "undefined") + ".")
			} else {
				title = cell.Title
			}
			var label *LabelCell
			if key != "monthly_cost_usd" && key != "errors" {
				label = labelFor(i, key)
			}
			cells[i] = Cell{Text: cell.Text, Delta: cell.Delta, Floor: floorText, Title: title, Label: label}
		}
		var note *string
		if row.Note != nil {
			note = ptr(row.Note.Text)
		}
		rows = append(rows, Row{
			Key: key, Label: row.Label, Sublabel: sublabel, Kind: "stat",
			Production: production, ProductionNote: productionNote, Floor: floorText,
			Cells: cells, Note: note,
		})
	}
	if byKey["monthly_cost_usd"] == nil {
		cells := make([]Cell, len(arms))
		for i := range cells {
			cells[i] = Cell{Text: core.MissingValue}
		}
		rows = append([]Row{{
			Key: "monthly_cost_usd", Label: "Monthly cost", Sublabel: ptr("needs your call volume"), Kind: "stat",
			Production: core.MissingValue, Cells: cells,
			Note: ptr("The run does not record how many calls the traced workload makes, so no monthly cost is projected."),
		}}, rows...)
	}

	for _, path := range tracked {
		kind, comparisons := core.FieldComparisons(lcases, path, armLabels, lfields)
		floorKind := kind
		if kind == core.KindMixed {
			floorKind = ""
		}
		floor := core.FloorField(lv, path, floorKind)
		var floorText *string
		if n := floors["field:"+path]; n != nil {
			floorText = ptr(n.Text)
		}
		cells := make([]Cell, len(arms))
		for i := range arms {
			text := core.MissingValue
			var title *string
			if row := summaryRow(i); row != nil {
				if cell, ok := row.Cells["field:"+path]; ok {
					text = cell.Text
					if cell.Sub != nil && *cell.Sub != "" {
						text += " " + *cell.Sub
					}
					title = cell.Title
				}
			}
			cells[i] = Cell{Text: text, Floor: floorText, Title: title, Label: labelFor(i, "field:"+path)}
		}
		sentences := make([]string, len(comparisons))
		for i := range comparisons {
			sentences[i] = core.FieldSentence(&comparisons[i], floor)
		}
		rows = append(rows, Row{
			Key: "field:" + path, Label: path, Sublabel: ptr(core.KindLabels[kind] + " field"), Kind: "field",
			Production: core.MissingValue, Floor: floorText, Cells: cells, Sentences: sentences,
		})
	}

	columns := make([]Column, len(arms))
	for i, arm := range arms {
		var t *core.TreatmentEquivalence
		for j := range eq {
			if eq[j].Label == arm.Label {
				t = &eq[j]
				break
			}
		}
		col := Column{ID: arm.ID, Label: arm.Label, Model: arm.Model, Settings: core.SettingsPhrase(arm.Settings), Endpoint: arm.Provider}
		if row := summaryRow(i); row != nil {
			col.N = row.N
			col.Note = row.Note
		}
		if col.Note == nil && t != nil {
			col.Note = t.SampleNote
		}
		columns[i] = col
	}

	charts := buildCharts(lv, tracked, a1, nf)
	caseItems := buildCases(run, view, lcases, lfields, tracked, armLabels)

	// Not-replayable reasons, most common first (ties in order of appearance).
	var notReplayable []NotReplayable
	for _, c := range view.Cases {
		if c.Status != "not_replayable" || c.Reason == nil || *c.Reason == "" {
			continue
		}
		at := slices.IndexFunc(notReplayable, func(r NotReplayable) bool { return r.Reason == *c.Reason })
		if at >= 0 {
			notReplayable[at].Count++
			continue
		}
		text, ok := reasonTexts[*c.Reason]
		if !ok {
			text = reasonText{*c.Reason, "This call could not be replayed."}
		}
		notReplayable = append(notReplayable, NotReplayable{Reason: *c.Reason, Title: text.title, Count: 1, Fix: text.fix})
	}
	sort.SliceStable(notReplayable, func(i, j int) bool { return notReplayable[i].Count > notReplayable[j].Count })
	if notReplayable == nil {
		notReplayable = []NotReplayable{}
	}

	// The headline: a question built from the data, never an answer.
	costRow := byKey["cost_per_call_usd"]
	type cheap struct {
		arm   core.ArmView
		delta *string
		cost  float64
	}
	var cheaper []cheap
	for i, arm := range arms {
		if arm.Summary.CostPerCallUSD == nil {
			continue
		}
		var delta *string
		if costRow != nil && i < len(costRow.Arms) {
			delta = costRow.Arms[i].Delta
		}
		cheaper = append(cheaper, cheap{arm, delta, *arm.Summary.CostPerCallUSD})
	}
	sort.SliceStable(cheaper, func(i, j int) bool { return cheaper[i].cost < cheaper[j].cost })
	baseCost := view.Control.Summary.CostPerCallUSD
	if a1 != nil && a1.CostPerCallUSD != nil {
		baseCost = a1.CostPerCallUSD
	}
	anyCheaper := len(cheaper) > 0 && baseCost != nil && cheaper[0].cost < *baseCost
	prod := core.ShortModel(productionModel)
	if prod == "" {
		prod = productionModel
	}
	headline := "What would change if " + prod + "'s calls ran on another model?"
	if anyCheaper {
		headline = "Would a cheaper model have worked for " + prod + "'s calls?"
	}
	callWord := "calls"
	if view.Sample.Replayed == 1 {
		callWord = "call"
	}
	parts := []string{core.FormatCount(view.Sample.Replayed) + " production " + callWord + " replayed on " + joinAnd(armLabels) + "."}
	if anyCheaper && cheaper[0].delta != nil && strings.HasSuffix(*cheaper[0].delta, "% lower") {
		c := cheaper[0]
		less := strings.TrimSuffix(*c.delta, " lower") + " less"
		short := core.ShortModel(c.arm.Model)
		name := c.arm.Label + " (" + short + ")"
		if c.arm.Label == c.arm.Model || c.arm.Label == short {
			name = short
			if name == "" {
				name = c.arm.Label
			}
		}
		same := ""
		if hasFloor {
			same = " replayed the same way"
		}
		parts = append(parts, name+" costs "+less+" per call than "+prod+same+".")
	}
	if hasFloor {
		parts = append(parts, "Every difference below sits beside how much "+prod+" differs from itself.")
	} else {
		parts = append(parts, "This run has no noise floor, so differences are shown without production's own variation.")
	}

	meta := []MetaItem{
		{"Sample", core.FormatCount(view.Sample.Cases) + " calls, " + core.FormatCount(view.Sample.Replayed) + " replayed"},
		{"Source", run.Input.Adapter + ", " + run.Input.File},
		{"Production model", productionModel},
		{"Created", utc(run.CreatedAt)},
		{"Price catalog", run.Catalog.Version},
		{"Spent", core.FormatUSD(run.SpentUSD) + " of " + core.FormatUSD(run.Settings.BudgetUSD) + " budget"},
	}
	var stopped *string
	if run.Stopped != nil {
		switch *run.Stopped {
		case "budget":
			stopped = ptr("The run stopped at its budget, so some calls were not replayed.")
		case "interrupted":
			stopped = ptr("The run was interrupted, so some calls were not replayed.")
		case "reservation_exceeded":
			stopped = ptr("The run stopped when a call could have cost more than the budget left, so some calls were not replayed.")
		}
	}

	rowOf := func(key string) *Row {
		for i := range rows {
			if rows[i].Key == key {
				return &rows[i]
			}
		}
		return nil
	}

	var replayedCases []core.CaseView
	for _, c := range view.Cases {
		if c.Status == "replayed" {
			replayedCases = append(replayedCases, c)
		}
	}
	var floorPanel *FloorPanel
	if nf != nil {
		type statText struct {
			key, label string
			read       func(s core.Summary) string
		}
		defs := []statText{
			{"cost_per_call_usd", "Cost per call", func(s core.Summary) string { return core.FormatUSD(s.CostPerCallUSD) }},
			{"latency_ms_p50", "Latency, median", func(s core.Summary) string { return core.FormatMs(s.LatencyMsP50) }},
			{"output_chars_p50", "Output length, median", func(s core.Summary) string { return core.FormatCount(s.OutputCharsP50) }},
			{"json_parse_rate", "JSON parses", func(s core.Summary) string { return core.FormatPercent(s.JSONParseRate) }},
			{"empty_or_refused_rate", "Empty or refused", func(s core.Summary) string { return core.FormatPercent(s.EmptyOrRefusedRate) }},
			{"cut_off_rate", "Cut off at token limit", func(s core.Summary) string { return core.FormatPercent(s.CutOffRate) }},
		}
		floorStats := make([]FloorStatRow, 0, len(defs))
		for _, d := range defs {
			gap := core.MissingValue
			if r := rowOf(d.key); r != nil && r.Floor != nil {
				gap = *r.Floor
			}
			floorStats = append(floorStats, FloorStatRow{Label: d.label, A1: d.read(nf.A1), A2: d.read(nf.A2), Gap: gap})
		}
		floorFields := make([]FloorFieldRow, 0, len(tracked))
		for _, path := range tracked {
			series := nf.Fields[path]
			mismatches := []FloorMismatch{}
			if series != nil && series.Comparison == "match_rate" {
				for _, id := range series.Mismatches {
					traceID := jsString(id)
					i := slices.IndexFunc(replayedCases, func(c core.CaseView) bool { return c.TraceID == traceID })
					mismatches = append(mismatches, FloorMismatch{TraceID: traceID, A1: seriesValue(series.A1, i), A2: seriesValue(series.A2, i)})
				}
			}
			var text *string
			if n := floors["field:"+path]; n != nil {
				text = ptr(n.Text)
			}
			floorFields = append(floorFields, FloorFieldRow{Path: path, Text: text, Mismatches: mismatches})
		}
		floorPanel = &FloorPanel{
			Cases: nf.Cases,
			Model: prod,
			Spent: core.FormatUSD(nf.SpentUSD),
			Text: []string{
				"Each replayable call was sent to " + prod + " twice more (A1 and A2), at production's settings, through the same endpoint as the treatments. Whatever separates A2 from A1 is chance, so it is the variation production shows on its own.",
				"Read a treatment's difference beside that gap: a difference no larger than production's own is one a rerun of production would also show. Neither figure says which output is better.",
			},
			Stats:  floorStats,
			Fields: floorFields,
		}
	}

	// Bars: cost and latency per arm, production first and production against itself last.
	armNames := shortNames(arms)
	costStat := rowOf("cost_per_call_usd")
	monthlyStat := rowOf("monthly_cost_usd")
	latencyStat := rowOf("latency_ms_p50")
	strip := func(text *string) *string {
		if text == nil || *text == "" {
			return nil
		}
		s := *text
		for _, suffix := range []string{" replay", " control"} {
			if strings.HasSuffix(s, suffix) {
				s = strings.TrimSuffix(s, suffix)
				break
			}
		}
		return &s
	}
	perMonth := func(perCall *float64) *float64 {
		if perCall == nil || calls == nil || *calls == 0 {
			return nil
		}
		return ptr(*perCall * *calls)
	}
	prodPerCall := view.Control.Summary.CostPerCallUSD
	if hasFloor {
		prodPerCall = a1.CostPerCallUSD
	}
	prodLabel := prod
	if hasFloor {
		prodLabel = prod + ", replayed"
	}
	prodBar := CostBar{
		Role: RoleProduction, Label: prodLabel, Name: prod, Model: productionModel,
		PerCall: prodPerCall, Text: core.FormatUSD(prodPerCall), Monthly: perMonth(prodPerCall),
	}
	if hasFloor && costStat != nil && view.Control.Summary.CostPerCallUSD != nil {
		prodBar.Note = ptr(costStat.Production + " recorded")
	}
	if m := perMonth(prodPerCall); m != nil {
		prodBar.MonthlyText = ptr(core.FormatUSD(*m) + "/mo")
	}
	costBars := []CostBar{prodBar}
	for i, arm := range arms {
		perCall := arm.Summary.CostPerCallUSD
		bar := CostBar{Role: RoleTreatment, Label: arm.Label, Name: armNames[i], Model: arm.Model, PerCall: perCall, Monthly: perMonth(perCall)}
		if costStat != nil && i < len(costStat.Cells) {
			bar.Text = costStat.Cells[i].Text
			bar.Delta = strip(costStat.Cells[i].Delta)
			bar.Badge = costStat.Cells[i].Label
		} else {
			bar.Text = core.FormatUSD(perCall)
		}
		if monthlyStat != nil && i < len(monthlyStat.Cells) {
			cell := monthlyStat.Cells[i]
			if cell.Text != core.MissingValue {
				bar.MonthlyText = ptr(cell.Text + "/mo")
			}
			bar.MonthlyDelta = cell.Delta
		}
		costBars = append(costBars, bar)
	}
	if nf != nil {
		perCall := nf.Summary.CostPerCallUSD
		bar := CostBar{Role: RoleFloor, Label: "production vs itself", Name: "vs itself", Model: productionModel, PerCall: perCall, Text: core.FormatUSD(perCall)}
		if costStat != nil {
			bar.Delta = costStat.Floor
		}
		costBars = append(costBars, bar)
	}

	latencies := func(pick func(c core.CaseView) *core.ResultView) []float64 {
		out := []float64{}
		for _, c := range replayedCases {
			r := pick(c)
			if r == nil || (r.Error != nil && *r.Error != "") || r.LatencyMs == nil || math.IsNaN(*r.LatencyMs) || math.IsInf(*r.LatencyMs, 0) {
				continue
			}
			out = append(out, *r.LatencyMs)
		}
		return out
	}
	latencyRow := func(role ArmRole, label, name string, values []float64, median *float64, delta *string, badge *LabelCell) LatencyRow {
		p10, _, p90 := spread(values)
		return LatencyRow{Role: role, Label: label, Name: name, N: len(values), P10: p10, P50: median, P90: p90, Text: core.FormatMs(median), Delta: delta, Badge: badge, Values: values}
	}
	floorA := func(pick func(f *core.CaseFloor) *core.ResultView) func(c core.CaseView) *core.ResultView {
		return func(c core.CaseView) *core.ResultView {
			if c.NoiseFloor == nil {
				return nil
			}
			return pick(c.NoiseFloor)
		}
	}
	var latency []LatencyRow
	if hasFloor {
		latency = append(latency, latencyRow(RoleProduction, prod+", replayed", prod, latencies(floorA(func(f *core.CaseFloor) *core.ResultView { return f.A1 })), a1.LatencyMsP50, nil, nil))
	} else {
		latency = append(latency, latencyRow(RoleProduction, prod, prod, latencies(func(c core.CaseView) *core.ResultView {
			ctl := c.Control
			return &core.ResultView{LatencyMs: ctl.LatencyMs}
		}), view.Control.Summary.LatencyMsP50, nil, nil))
	}
	for i, arm := range arms {
		var delta *string
		var badge *LabelCell
		if latencyStat != nil && i < len(latencyStat.Cells) {
			delta = strip(latencyStat.Cells[i].Delta)
			badge = latencyStat.Cells[i].Label
		}
		label := arm.Label
		latency = append(latency, latencyRow(RoleTreatment, arm.Label, armNames[i], latencies(func(c core.CaseView) *core.ResultView {
			r, ok := c.Arms[label]
			if !ok {
				return nil
			}
			return &r
		}), arm.Summary.LatencyMsP50, delta, badge))
	}
	if nf != nil {
		var delta *string
		if latencyStat != nil {
			delta = latencyStat.Floor
		}
		latency = append(latency, latencyRow(RoleFloor, "production vs itself", "vs itself", latencies(floorA(func(f *core.CaseFloor) *core.ResultView { return f.A2 })), nf.A2.LatencyMsP50, delta, nil))
	}

	// Tracked fields: agreement per arm on one scale with production's agreement with itself.
	fieldPanels := make([]FieldPanel, 0, len(tracked))
	for _, path := range tracked {
		kind, comparisons := core.FieldComparisons(lcases, path, armLabels, lfields)
		floorKind := kind
		if kind == core.KindMixed {
			floorKind = ""
		}
		f := core.FloorField(lv, path, floorKind)
		statRow := rowOf("field:" + path)
		scale := "none"
		switch kind {
		case core.KindNumber:
			scale = "shift"
		case core.KindLabel, core.KindText, core.KindLongText, core.KindList:
			scale = "share"
		}
		panel := FieldPanel{Path: path, Kind: string(kind), KindLabel: core.KindLabels[kind], Scale: scale}
		if f != nil {
			ff := &FieldFloor{}
			if scale == "shift" {
				if f.DiffBox != nil {
					ff.Value = ptr(f.DiffBox.Median)
				}
			} else {
				ff.Value = share(f.Kind, f.PairedReading)
			}
			if n := floors["field:"+path]; n != nil {
				ff.Text = ptr(n.Text)
			}
			if f.DiffBox != nil {
				ff.Low = ptr(f.DiffBox.Min)
				ff.High = ptr(f.DiffBox.Max)
			}
			panel.Floor = ff
		}
		panel.Arms = make([]FieldArm, len(comparisons))
		for i := range comparisons {
			c := &comparisons[i]
			box := c.DiffBox
			var value *float64
			if scale == "shift" {
				if box != nil {
					value = ptr(box.Median)
				}
			} else {
				value = share(c.Kind, c.PairedReading)
			}
			valueText := core.FormatPercent(value)
			if scale == "shift" {
				valueText = core.FormatSigned(value, nil)
			}
			arm := FieldArm{Label: c.Label, Name: armNames[i], Value: value, ValueText: valueText, Detail: core.MissingValue, Changes: []LabelChange{}}
			if statRow != nil && i < len(statRow.Cells) {
				arm.Detail = statRow.Cells[i].Text
				arm.Badge = statRow.Cells[i].Label
			}
			if statRow != nil && i < len(statRow.Sentences) {
				arm.Sentence = statRow.Sentences[i]
			} else {
				arm.Sentence = core.FieldSentence(c, f)
			}
			if box != nil {
				arm.Range = &Range{Lo: box.Q1, Mid: box.Median, Hi: box.Q3}
			}
			for _, t := range c.Tally {
				if !t.Same {
					arm.Changes = append(arm.Changes, LabelChange{From: t.From, To: t.To, Count: t.Count})
				}
			}
			panel.Arms[i] = arm
		}
		fieldPanels = append(fieldPanels, panel)
	}

	// A recorded median of 0 ms (or less) means the source logged no durations.
	var recordedLatency *string
	if hasFloor && latencyStat != nil && view.Control.Summary.LatencyMsP50 != nil && *view.Control.Summary.LatencyMsP50 > 0 {
		recordedLatency = ptr(strings.TrimSuffix(latencyStat.Production, " recorded"))
	}

	return &Model{
		Headline:        headline,
		Subhead:         strings.Join(parts, " "),
		Meta:            meta,
		Stopped:         stopped,
		ProductionModel: productionModel,
		Columns:         columns,
		Rows:            rows,
		HasFloor:        hasFloor,
		Floor:           floorPanel,
		Labels:          labelsOn,
		Charts:          charts,
		Cases:           caseItems,
		NotReplayable:   notReplayable,
		MonthlyCalls:    calls,
		Cost:            costBars,
		Latency:         latency,
		Fields:          fieldPanels,
		RecordedLatency: recordedLatency,
		Noise:           noise,
		Run:             run,
		View:            view,
	}, nil
}

func findLabel(set *core.NoiseLabels, key string) *core.NoiseLabel {
	if set == nil {
		return nil
	}
	for _, list := range [][]core.NoiseLabel{set.Stats, set.Fields} {
		for i := range list {
			if list[i].Key == key {
				return &list[i]
			}
		}
	}
	return nil
}

func share(kind core.FieldKind, r core.PairedReading) *float64 {
	finite := func(p *float64) *float64 {
		if p == nil || math.IsNaN(*p) || math.IsInf(*p, 0) {
			return nil
		}
		return p
	}
	switch kind {
	case core.KindList:
		return finite(r.Overlap)
	case core.KindLabel, core.KindText, core.KindLongText:
		return finite(r.Rate)
	}
	return nil
}

func seriesValue(series []any, i int) string {
	if i < 0 || i >= len(series) {
		return core.MissingValue
	}
	return showValue(series[i])
}

func joinAnd(names []string) string {
	if len(names) <= 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// buildCharts is the cost against quality frontier, one chart per metric the grid reads.
func buildCharts(lv any, tracked []string, a1 *core.Summary, nf *core.NoiseFloor) []FrontierChart {
	charts := []FrontierChart{}
	metrics := core.GridMetrics(lv, core.GridMetricsOptions{Tracked: tracked, FloorAsReplay: true})
	if len(metrics) == 0 {
		return charts
	}
	cost := &metrics[0]
	formatOf := func(key, label string) string {
		switch {
		case key == "cost_per_call_usd":
			return "usd"
		case key == "latency_ms_p50":
			return "ms"
		case strings.HasSuffix(label, "shift"):
			return "signed"
		}
		return "percent"
	}
	// Tracked fields first: they say most about the outputs; latency last.
	var ordered []core.GridMetric
	if len(metrics) > 2 {
		ordered = append(ordered, metrics[2:]...)
	}
	if len(metrics) > 1 {
		ordered = append(ordered, metrics[1])
	}
	for _, metric := range ordered {
		points := core.FrontierPoints(lv, metric, cost)
		if len(points) == 0 {
			continue
		}
		var kept []string
		for _, p := range core.Frontier(points, metric.Better) {
			kept = append(kept, p.Label)
		}
		var floorLine *ChartFloor
		if metric.Field != "" {
			if f := core.FloorField(lv, metric.Field, ""); f != nil && metric.Better != "" {
				y := f.Rate
				if f.Kind == core.KindList {
					y = f.Overlap
				}
				if y != nil {
					floorLine = &ChartFloor{Y: *y, Text: "Production vs itself: " + core.FormatPercent(*y)}
				}
			}
		}
		var better *string
		if metric.Better != "" {
			better = ptr(metric.Better)
		}
		chart := FrontierChart{Key: metric.Key, Label: metric.Label, Better: better, Format: formatOf(metric.Key, metric.Label), Floor: floorLine, Frontier: []string{}}
		hasControl := false
		for _, p := range points {
			label := p.Label
			if p.Control {
				label = "Production, replayed"
				hasControl = true
			}
			pt := ChartPoint{
				Label: label, X: deref(p.X.Value), XText: p.X.Text, Y: deref(p.Y.Value), YText: p.Y.Text, N: p.Y.N,
				Control: p.Control, OnFrontier: slices.Contains(kept, p.Label),
			}
			if p.X.Interval != nil {
				pt.XLow, pt.XHigh = ptr(p.X.Interval.Low), ptr(p.X.Interval.High)
			}
			if p.Y.Interval != nil {
				pt.YLow, pt.YHigh = ptr(p.Y.Interval.Low), ptr(p.Y.Interval.High)
			}
			chart.Points = append(chart.Points, pt)
		}
		for _, l := range kept {
			if l == "Control" {
				l = "Production, replayed"
			}
			chart.Frontier = append(chart.Frontier, l)
		}
		// A field has no control reading of its own, so production sits where its replay costs and agrees with itself.
		if floorLine != nil && a1 != nil && a1.CostPerCallUSD != nil && !hasControl {
			n := 0
			if nf != nil {
				n = nf.Cases
			}
			chart.Points = append(chart.Points, ChartPoint{
				Label: "Production, replayed", X: *a1.CostPerCallUSD, XText: core.FormatUSD(a1.CostPerCallUSD),
				Y: floorLine.Y, YText: core.FormatPercent(floorLine.Y) + " against itself", N: n, Control: true,
			})
		}
		charts = append(charts, chart)
	}
	return charts
}

// buildCases is the case browser.
func buildCases(run *types.RunFile, view *core.ExperimentView, lcases, lfields any, tracked, armLabels []string) []CaseItem {
	kinds := map[string]core.FieldKind{}
	for _, path := range tracked {
		kinds[path], _ = core.FieldComparisons(lcases, path, armLabels, lfields)
	}
	differing := map[string]map[string]bool{}
	changesOf := map[string][]FieldChange{}
	for _, label := range armLabels {
		set := map[string]bool{}
		for _, path := range tracked {
			kind := kinds[path]
			if kind == core.KindMixed || kind == core.KindEmpty {
				continue
			}
			for _, p := range core.FieldPairs(lcases, path, label, lfields) {
				if !valuesDiffer(kind, p.Control, p.Arm) {
					continue
				}
				id := jsString(p.TraceID)
				set[id] = true
				key := id + "\x00" + label
				changesOf[key] = append(changesOf[key], FieldChange{Path: path, From: showValue(p.Control), To: showValue(p.Arm)})
			}
		}
		differing[label] = set
	}
	replayByID := map[string]*types.Replay{}
	for i := range run.Replays {
		r := &run.Replays[i]
		replayByID[r.CaseID+"\x00"+r.Arm] = r
	}
	caseByID := map[string]*types.Case{}
	for i := range run.Cases {
		if _, seen := caseByID[run.Cases[i].ID]; !seen {
			caseByID[run.Cases[i].ID] = &run.Cases[i]
		}
	}
	items := make([]CaseItem, 0, len(view.Cases))
	for _, c := range view.Cases {
		original := caseByID[c.CaseID]
		treatment := func(arm string) bool { return arm != "control" && !strings.HasPrefix(arm, "floor_") }
		output := func(label, arm string, res *result) ArmOutput {
			replay := replayByID[c.CaseID+"\x00"+arm]
			if res == nil {
				msg := "No result"
				if replay != nil && replay.Status == "skipped" {
					msg = "Not run: the run stopped first"
				}
				return ArmOutput{Label: label, Status: "missing", Error: &msg, Flags: []string{}, Latency: core.MissingValue, Cost: core.MissingValue, Changes: []FieldChange{}}
			}
			flags := []string{}
			if replay != nil {
				for _, f := range replay.Flags {
					if text, ok := flagTexts[string(f)]; ok {
						flags = append(flags, text)
					} else {
						flags = append(flags, string(f))
					}
				}
			}
			if res.refusal {
				flags = append(flags, "flagged as a refusal")
			}
			if res.finishReason != nil && cutOffReasons[*res.finishReason] {
				flags = append(flags, "cut off at the token limit")
			}
			var errText *string
			if res.err != nil && *res.err != "" {
				e := *res.err
				if replay != nil && replay.Error != nil && replay.Error.Message != "" {
					e += ": " + replay.Error.Message
				}
				errText = &e
			}
			differs := false
			if treatment(arm) {
				switch {
				case errText != nil || res.refusal:
					differs = true
				case len(tracked) > 0:
					differs = differing[label][c.TraceID]
				default:
					differs = strings.TrimSpace(strOr(res.output, "")) != strings.TrimSpace(strOr(c.Control.Output, ""))
				}
			}
			status := "ok"
			if errText != nil {
				status = "failed"
			}
			changes := []FieldChange{}
			if treatment(arm) {
				if list := changesOf[c.TraceID+"\x00"+label]; list != nil {
					changes = list
				}
			}
			var finish *string
			if res.finishReason != nil && *res.finishReason != "" {
				finish = res.finishReason
			}
			return ArmOutput{
				Label: label, Status: status, Output: res.output, Pretty: prettyOutput(res.output), Error: errText,
				Flags: flags, Latency: core.FormatMs(res.latency), Cost: core.FormatUSD(res.cost),
				FinishReason: finish, Differs: differs, Changes: changes,
			}
		}
		ctl := c.Control
		production := output("Production", "control", &result{output: ctl.Output, latency: ctl.LatencyMs, cost: ctl.CostUSD, finishReason: ctl.FinishReason, refusal: ctl.Refusal})
		production.Flags = []string{}
		if ctl.Refusal {
			production.Flags = append(production.Flags, "flagged as a refusal")
		}
		armOutputs := []ArmOutput{}
		if c.Status == "replayed" {
			for _, arm := range view.Arms {
				var res *result
				if r, ok := c.Arms[arm.Label]; ok {
					res = fromResult(&r)
				}
				armOutputs = append(armOutputs, output(arm.Label, arm.ID, res))
			}
		}
		floorOutputs := []ArmOutput{}
		if c.Status == "replayed" && c.NoiseFloor != nil {
			floorOutputs = append(floorOutputs,
				output("A1, production's model replayed", "floor_a1", fromResult(c.NoiseFloor.A1)),
				output("A2, replayed again", "floor_a2", fromResult(c.NoiseFloor.A2)))
		}
		var reason *string
		if c.Reason != nil && *c.Reason != "" {
			if text, ok := reasonTexts[*c.Reason]; ok {
				reason = ptr(text.title)
			} else {
				reason = ptr(*c.Reason)
			}
		}
		input := c.InputPreview
		if input == nil && original != nil && len(original.Messages) > 0 {
			input = ptr(original.Messages[len(original.Messages)-1].Content)
		}
		var system *string
		messages := []types.Message{}
		if original != nil {
			system = original.System
			if original.Messages != nil {
				messages = original.Messages
			}
		}
		limitations := c.Limitations
		if limitations == nil {
			limitations = []string{}
		}
		differs := false
		for _, a := range armOutputs {
			differs = differs || a.Differs
		}
		items = append(items, CaseItem{
			ID: c.CaseID, TraceID: c.TraceID, Status: c.Status, Reason: reason, Input: input,
			System: system, Messages: messages, Limitations: limitations,
			Production: production, Arms: armOutputs, Floor: floorOutputs, Differs: differs,
		})
	}
	return items
}
