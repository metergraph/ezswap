package report

// The terminal report, answer first: a one-line header, one comparison table
// with the models as columns, the calls that changed, and a pointer to the
// caveats. No boxes; blank lines separate the sections. Colour marks which
// model a figure belongs to and whether it moved further than production
// moves against itself, never whether it is good: there are no red or green
// verdicts. Every captured string is cleaned of control characters before it
// is laid out, and every figure comes from the model.

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/metergraph/ezswap/internal/core"
	"github.com/metergraph/ezswap/internal/report/term"
	"github.com/metergraph/ezswap/internal/types"
)

const (
	// MinWidth and MaxWidth clamp the columns the report fills.
	MinWidth = 40
	MaxWidth = 140
	// DefaultWidth is the width when none is known.
	DefaultWidth = 100
	// ChangeLimit is the lines "What changed" lists before it says how many more there are.
	ChangeLimit = 8
	// ExplainHint is where the footer sends a reader for the caveats.
	ExplainHint = "run with --explain"
)

// RenderOptions say how to draw the report.
type RenderOptions struct {
	// Columns to fill: clamped to MinWidth..MaxWidth, DefaultWidth when 0.
	Width int
	// Colour: term.NewPaint(profile), or the zero value (term.PlainPaint()) for none.
	Paint term.Paint
	// Plain ASCII: no Unicode glyphs, captured text mapped to ASCII.
	ASCII bool
	// The run file's path as the user would type it, for the hint to explore
	// it; no hint when empty.
	Explore string
	// Lines "What changed" lists; ChangeLimit when 0.
	ChangeLimit int
	// The note under a capped change list; "+N more, see the explorer" when nil.
	MoreHint func(more int) string
	// Where the footer sends a reader for the caveats; ExplainHint when empty.
	Help string
}

// ReportOptions are RenderReport's options: what to build and how to draw it.
type ReportOptions struct {
	Options
	RenderOptions
}

// RenderReport builds the model from the run and renders the printed report.
func RenderReport(run *types.RunFile, options ReportOptions) (string, error) {
	m, err := Build(run, options.Options)
	if err != nil {
		return "", err
	}
	return Render(m, options.RenderOptions), nil
}

type ctx struct {
	w     int
	p     term.Paint
	ascii bool
	ell   string
	opts  RenderOptions
}

func newCtx(o RenderOptions) ctx {
	w := o.Width
	if w == 0 {
		w = DefaultWidth
	}
	w = max(MinWidth, min(MaxWidth, w))
	ell := term.Ellipsis
	if o.ASCII {
		ell = "~"
	}
	return ctx{w: w, p: o.Paint, ascii: o.ASCII, ell: ell, opts: o}
}

func (c ctx) g(unicode, ascii string) string {
	if c.ascii {
		return ascii
	}
	return unicode
}

// clean is captured or model text made safe for one terminal line (and ASCII when asked).
func (c ctx) clean(s string) string {
	s = term.CleanLine(s)
	if c.ascii {
		s = term.ToAscii(s)
	}
	return s
}

// hue is treatment i's colour.
func (c ctx) hue(i int, text string) string {
	return c.p.Hue(term.TreatmentHues[i%len(term.TreatmentHues)], text)
}

// Letter is treatment i's letter: A, B, ... Z, then AA, AB, ...
func Letter(i int) string {
	if i < 26 {
		return string(rune('A' + i))
	}
	return Letter(i/26-1) + Letter(i%26)
}

func finish(c ctx, lines []string) string {
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	text := strings.Join(lines, "\n")
	if c.ascii {
		text = term.ToAscii(text)
	}
	return text
}

// Render is the printed report: header, summary and footer, ending in a
// newline.
func Render(m *Model, o RenderOptions) string {
	c := newCtx(o)
	var lines []string
	lines = append(lines, header(c, m)...)
	lines = append(lines, "")
	lines = append(lines, summaryLines(c, m)...)
	lines = append(lines, "")
	lines = append(lines, footer(c, m)...)
	return finish(c, lines) + "\n"
}

// RenderSummary is the comparison table, the calls that changed and the
// not-replayable line: the block the explorer shows as its Summary tab. No
// trailing newline.
func RenderSummary(m *Model, o RenderOptions) string {
	c := newCtx(o)
	return finish(c, summaryLines(c, m))
}

func summaryLines(c ctx, m *Model) []string {
	lines := table(c, m)
	lines = append(lines, "")
	lines = append(lines, changes(c, m)...)
	if nr := notReplayableLine(c, m); nr != nil {
		lines = append(lines, "")
		lines = append(lines, nr...)
	}
	return lines
}

// ---------------------------------------------------------------------------
// Header

func header(c ctx, m *Model) []string {
	p := c.p
	run := m.Run
	sample := m.View.Sample
	prod := c.clean(shortProduction(m))
	calls := "calls"
	if sample.Cases == 1 {
		calls = "call"
	}
	what := core.FormatCount(sample.Replayed) + " of " + core.FormatCount(sample.Cases) + " " + prod + " " + calls + " replayed"
	if sample.Replayed == sample.Cases {
		what = core.FormatCount(sample.Cases) + " " + prod + " " + calls + " replayed"
	}
	source := c.clean(run.Input.File)
	date := ""
	if d, ok := parseDate(run.CreatedAt); ok {
		date = pad2Date(d)
		date = date[:strings.Index(date, ",")]
	}
	spent := "spent " + core.FormatUSD(run.SpentUSD)
	budget := spent + " of " + core.FormatUSD(run.Settings.BudgetUSD)
	sep := c.g(" · ", " | ")
	join := func(parts ...string) string {
		var kept []string
		for _, s := range parts {
			if s != "" {
				kept = append(kept, s)
			}
		}
		return strings.Join(kept, sep)
	}
	candidates := [][]string{
		{source, date, budget},
		{source, date, spent},
		{date, spent},
		{spent},
		{},
	}
	line := ""
	for _, rest := range candidates {
		if term.Width(what)+term.Width(sep)+term.Width(join(rest...)) <= c.w || len(rest) == 0 {
			line = p.Bold(term.Truncate(what, c.w, c.ell))
			if r := join(rest...); r != "" {
				line += p.Muted(sep + r)
			}
			break
		}
	}
	lines := []string{line}
	if m.Stopped != nil {
		for _, l := range term.Wrap(*m.Stopped, c.w) {
			lines = append(lines, p.Emph(l))
		}
	}
	return lines
}

func shortProduction(m *Model) string {
	if m.View != nil && m.View.Control.Model == nil {
		return "production"
	}
	if s := core.ShortModel(m.ProductionModel); s != "" {
		return s
	}
	return m.ProductionModel
}

// ---------------------------------------------------------------------------
// The comparison table

// figure is one cell of the table.
type figure struct {
	text string
	// The same as production's figure: drawn dim.
	same bool
	// Further from production than production is from itself: drawn in the
	// treatment's colour, bold, and marked.
	beyond bool
}

type tableRow struct {
	group  string // a group label row when set
	label  string
	prod   figure
	itself figure
	arms   []figure
}

// BeyondMark marks a figure further from production than production is from itself.
const BeyondMark = "*"

func stripPath(path string) string {
	s := strings.TrimPrefix(path, "$")
	s = strings.TrimPrefix(s, ".")
	if s == "" {
		return path
	}
	return s
}

func trimMo(s *string) string {
	if s == nil {
		return core.MissingValue
	}
	return strings.TrimSuffix(*s, "/mo")
}

// beyond is whether treatment i's label on key says beyond noise.
func beyond(m *Model, i int, key string) bool {
	if i >= len(m.Noise) {
		return false
	}
	l := findLabel(m.Noise[i], key)
	return l != nil && l.Label == "beyond_noise"
}

func (m *Model) row(key string) *Row {
	for i := range m.Rows {
		if m.Rows[i].Key == key {
			return &m.Rows[i]
		}
	}
	return nil
}

func (m *Model) costOf(role ArmRole) []CostBar {
	var out []CostBar
	for _, b := range m.Cost {
		if b.Role == role {
			out = append(out, b)
		}
	}
	return out
}

func (m *Model) latencyOf(role ArmRole) []LatencyRow {
	var out []LatencyRow
	for _, r := range m.Latency {
		if r.Role == role {
			out = append(out, r)
		}
	}
	return out
}

// tableRows are the table's rows, grouped: cost, quality, speed.
func tableRows(c ctx, m *Model) []tableRow {
	n := len(m.Columns)
	nf := m.View.NoiseFloor
	treatments := m.costOf(RoleTreatment)
	prodCost := m.costOf(RoleProduction)
	floorCost := m.costOf(RoleFloor)
	build := func(label, key, prod, itself string, arm func(i int) string) tableRow {
		r := tableRow{label: label, prod: figure{text: prod}, itself: figure{text: itself, same: itself == prod && prod != core.MissingValue}}
		for i := range n {
			text := arm(i)
			r.arms = append(r.arms, figure{
				text:   text,
				same:   text == prod && prod != core.MissingValue,
				beyond: key != "" && text != core.MissingValue && beyond(m, i, key),
			})
		}
		return r
	}
	var rows []tableRow

	// Cost.
	rows = append(rows, tableRow{group: "COST"})
	if m.MonthlyCalls != nil && m.row("monthly_cost_usd") != nil && m.row("monthly_cost_usd").Production != core.MissingValue {
		prod, itself := core.MissingValue, core.MissingValue
		if len(prodCost) > 0 {
			prod = trimMo(prodCost[0].MonthlyText)
		}
		if len(floorCost) > 0 && floorCost[0].PerCall != nil {
			itself = core.FormatUSD(*floorCost[0].PerCall * *m.MonthlyCalls)
		}
		rows = append(rows, build("per month", "cost_per_call_usd", prod, itself, func(i int) string {
			if i < len(treatments) {
				return trimMo(treatments[i].MonthlyText)
			}
			return core.MissingValue
		}))
	}
	{
		prod, itself := core.MissingValue, core.MissingValue
		if len(prodCost) > 0 {
			prod = prodCost[0].Text
		}
		if len(floorCost) > 0 {
			itself = floorCost[0].Text
		}
		rows = append(rows, build("per call", "cost_per_call_usd", prod, itself, func(i int) string {
			if i < len(treatments) {
				return treatments[i].Text
			}
			return core.MissingValue
		}))
	}

	// Quality.
	rows = append(rows, tableRow{group: "QUALITY"})
	for _, f := range m.Fields {
		name := c.clean(stripPath(f.Path))
		label := name + " agrees"
		itself := core.MissingValue
		switch f.Scale {
		case "shift":
			label = name + " shift"
			if f.Floor != nil && f.Floor.Value != nil {
				itself = core.FormatSigned(*f.Floor.Value, nil)
			}
		case "share":
			if f.Floor != nil && f.Floor.Value != nil {
				itself = core.FormatPercent(*f.Floor.Value)
			}
		}
		rows = append(rows, build(label, "field:"+f.Path, core.MissingValue, itself, func(i int) string {
			if i < len(f.Arms) {
				if f.Scale == "none" {
					return core.MissingValue
				}
				return f.Arms[i].ValueText
			}
			return core.MissingValue
		}))
	}
	stat := func(label, key, noiseKey string, floorRead func(s core.Summary) string) {
		row := m.row(key)
		if row == nil {
			return
		}
		itself := core.MissingValue
		if nf != nil {
			itself = floorRead(nf.A2)
		}
		rows = append(rows, build(label, noiseKey, row.Production, itself, func(i int) string {
			if i < len(row.Cells) {
				return row.Cells[i].Text
			}
			return core.MissingValue
		}))
	}
	stat("JSON parses", "json_parse_rate", "json_parse_rate", func(s core.Summary) string { return core.FormatPercent(s.JSONParseRate) })
	stat("cut off", "cut_off_rate", "cut_off_rate", func(s core.Summary) string { return core.FormatPercent(s.CutOffRate) })
	// Errors only when a treatment had any.
	if row := m.row("errors"); row != nil {
		for _, cell := range row.Cells {
			if cell.Text != "0" && cell.Text != core.MissingValue {
				stat("errors", "errors", "", func(s core.Summary) string { return core.FormatCount(s.Errors) })
				break
			}
		}
	}

	// Speed.
	rows = append(rows, tableRow{group: "SPEED"})
	{
		prod, itself := core.MissingValue, core.MissingValue
		if l := m.latencyOf(RoleProduction); len(l) > 0 {
			// Production's recorded median when there is no floor: a median of
			// 0 ms or less means the source recorded no durations.
			if m.HasFloor || (l[0].P50 != nil && *l[0].P50 > 0) {
				prod = l[0].Text
			}
		}
		if l := m.latencyOf(RoleFloor); len(l) > 0 {
			itself = l[0].Text
		}
		arms := m.latencyOf(RoleTreatment)
		rows = append(rows, build("median latency", "latency_ms_p50", prod, itself, func(i int) string {
			if i < len(arms) {
				return arms[i].Text
			}
			return core.MissingValue
		}))
	}
	return rows
}

// armNames are each treatment's short name, for its column heading.
func armNames(m *Model) []string {
	treatments := m.costOf(RoleTreatment)
	names := make([]string, len(m.Columns))
	for i, col := range m.Columns {
		names[i] = col.Label
		if i < len(treatments) {
			names[i] = treatments[i].Name
		}
	}
	return names
}

func table(c ctx, m *Model) []string {
	p := c.p
	rows := tableRows(c, m)
	names := armNames(m)
	for i := range names {
		names[i] = c.clean(names[i])
	}
	anyBeyond := false
	for _, r := range rows {
		for _, f := range r.arms {
			anyBeyond = anyBeyond || f.beyond
		}
	}
	mark := 0
	if anyBeyond {
		mark = term.Width(BeyondMark)
	}

	// Widths: the label column, production, itself, then each treatment.
	naturalLabel := 0
	for _, r := range rows {
		if r.group == "" {
			naturalLabel = max(naturalLabel, 2+term.Width(r.label))
		}
	}
	naturalLabel = min(naturalLabel, 24)
	valueW := func(head string, pick func(r tableRow) string) int {
		w := term.Width(head)
		for _, r := range rows {
			if r.group == "" {
				w = max(w, term.Width(pick(r)))
			}
		}
		return w
	}
	type armCol struct {
		i     int
		min   int // the widest figure, or the letter and a few characters of the name
		full  int // the widest figure, or the whole heading
		width int
	}
	cols := make([]armCol, len(names))
	for i, name := range names {
		figW := valueW("", func(r tableRow) string {
			if i < len(r.arms) {
				return r.arms[i].text
			}
			return ""
		})
		least := max(figW, len(Letter(i))+1+min(term.Width(name), 6)) + mark
		full := max(figW, len(Letter(i))+1+term.Width(name)) + mark
		cols[i] = armCol{i: i, min: least, full: full}
	}
	needArm := 0
	if len(cols) > 0 {
		needArm = cols[0].min
	}

	// The roomiest layout in which the fixed columns leave space for at least
	// one treatment: then a shorter heading for production, a shorter label
	// column, and a narrower gap.
	var (
		sp, labelW, prodW, itselfW, fixed int
		prodHead                          string
	)
	fixedOf := func() int {
		f := labelW + sp + prodW
		if m.HasFloor {
			f += sp + itselfW
		}
		return f
	}
	for _, cfg := range []struct {
		gap      int
		head     string
		minLabel int
	}{{2, "production", naturalLabel}, {2, "prod", naturalLabel}, {2, "prod", 10}, {1, "prod", 8}, {1, "prod", 4}} {
		sp, prodHead = cfg.gap, cfg.head
		prodW = valueW(prodHead, func(r tableRow) string { return r.prod.text })
		itselfW = 0
		if m.HasFloor {
			itselfW = valueW("itself", func(r tableRow) string { return r.itself.text })
		}
		labelW = naturalLabel
		fixed = fixedOf()
		if over := fixed + sp + needArm - c.w; over > 0 {
			labelW = max(cfg.minLabel, labelW-over)
			fixed = fixedOf()
		}
		if fixed+sp+needArm <= c.w {
			break
		}
	}
	// Whatever is left of the line goes to the treatments, at least a column each.
	if fixed+sp+1 > c.w {
		labelW = max(1, labelW-(fixed+sp+1-c.w))
		fixed = fixedOf()
	}

	// Groups of treatments that fit beside the fixed columns; each group is drawn as its own table.
	var groups [][]armCol
	room := c.w - fixed
	for start := 0; start < len(cols); {
		end := start
		used := 0
		for end < len(cols) && used+sp+cols[end].min <= room {
			used += sp + cols[end].min
			end++
		}
		if end == start {
			// One treatment always goes in, cut to fit.
			end = start + 1
			used = room
			cols[start].min = room - sp
		}
		group := append([]armCol(nil), cols[start:end]...)
		// Share what is left over with the headings that are cut, one column at a time.
		spare := room - used
		for k := range group {
			group[k].width = group[k].min
		}
		for spare > 0 {
			grew := false
			for k := range group {
				if spare > 0 && group[k].width < group[k].full {
					group[k].width++
					spare--
					grew = true
				}
			}
			if !grew {
				break
			}
		}
		groups = append(groups, group)
		start = end
	}
	if len(groups) == 0 {
		groups = [][]armCol{nil}
	}
	gapText := strings.Repeat(" ", sp)
	figureW := func(col armCol) int { return max(1, col.width-mark) }

	var lines []string
	for gi, group := range groups {
		if gi > 0 {
			lines = append(lines, "")
		}
		head := []string{term.Fit("", labelW, term.AlignLeft), term.Fit(p.Bold(p.Prod(prodHead)), prodW, term.AlignRight)}
		if m.HasFloor {
			head = append(head, term.Fit(p.Floor("itself"), itselfW, term.AlignRight))
		}
		for _, col := range group {
			letter := Letter(col.i)
			nameW := figureW(col) - len(letter) - 1
			cell := c.hue(col.i, p.Bold(letter))
			if nameW > 0 {
				cell += " " + c.hue(col.i, term.Truncate(names[col.i], nameW, c.ell))
			}
			head = append(head, term.Fit(cell, figureW(col), term.AlignRight, c.ell)+strings.Repeat(" ", mark))
		}
		lines = append(lines, strings.Join(head, gapText))
		for _, r := range rows {
			if r.group != "" {
				lines = append(lines, p.Muted(r.group))
				continue
			}
			parts := []string{term.Fit("  "+r.label, labelW, term.AlignLeft, c.ell), term.Fit(r.prod.text, prodW, term.AlignRight)}
			if m.HasFloor {
				text := r.itself.text
				if r.itself.same {
					text = p.Muted(text)
				}
				parts = append(parts, term.Fit(text, itselfW, term.AlignRight))
			}
			for _, col := range group {
				f := figure{text: core.MissingValue}
				if col.i < len(r.arms) {
					f = r.arms[col.i]
				}
				text := f.text
				switch {
				case f.beyond:
					text = p.Bold(c.hue(col.i, text))
				case f.same:
					text = p.Muted(text)
				}
				suffix := strings.Repeat(" ", mark)
				if f.beyond {
					suffix = c.hue(col.i, BeyondMark)
				}
				parts = append(parts, term.Fit(text, figureW(col), term.AlignRight, c.ell)+suffix)
			}
			lines = append(lines, strings.Join(parts, gapText))
		}
	}
	if anyBeyond {
		for _, l := range term.Wrap(BeyondMark+" further from production than production is from itself", c.w) {
			lines = append(lines, p.Muted(l))
		}
	}
	return lines
}

// ---------------------------------------------------------------------------
// What changed

type change struct {
	id     string
	arm    int
	field  string
	from   string
	to     string
	plain  string // a change with no from and to: an error, a flag
	hasArw bool
}

// Changes are the lines "What changed" lists, every one of them: per
// differing call and treatment, each tracked field that changed, then an
// error, then a refusal or a cut-off, or "output differs" when nothing else
// says how.
func changeList(m *Model) []change {
	var out []change
	for _, cs := range m.Cases {
		if !cs.Differs {
			continue
		}
		for i, arm := range cs.Arms {
			if !arm.Differs {
				continue
			}
			before := len(out)
			for _, ch := range arm.Changes {
				out = append(out, change{id: cs.TraceID, arm: i, field: stripPath(ch.Path), from: ch.From, to: ch.To, hasArw: true})
			}
			if arm.Error != nil {
				out = append(out, change{id: cs.TraceID, arm: i, field: "error", plain: *arm.Error})
			}
			for _, f := range arm.Flags {
				if len(out) == before || strings.HasPrefix(f, "cut off") || strings.Contains(f, "refusal") {
					out = append(out, change{id: cs.TraceID, arm: i, plain: f})
				}
			}
			if len(out) == before {
				out = append(out, change{id: cs.TraceID, arm: i, plain: "output differs"})
			}
		}
	}
	return out
}

func changes(c ctx, m *Model) []string {
	p := c.p
	lines := []string{p.Muted("WHAT CHANGED")}
	all := changeList(m)
	if len(all) == 0 {
		text := "Nothing: no replayed call differs from production on the tracked fields, errors or refusals."
		if m.View.Sample.Replayed == 0 {
			text = "Nothing was replayed."
		}
		for _, l := range term.Wrap(text, c.w-2) {
			lines = append(lines, "  "+p.Muted(l))
		}
		return lines
	}
	limit := c.opts.ChangeLimit
	if limit <= 0 {
		limit = ChangeLimit
	}
	shown := all
	if len(all) > limit {
		shown = all[:limit]
	}
	idW, fieldW := 0, 0
	for _, ch := range shown {
		idW = max(idW, term.Width(c.clean(ch.id)))
		fieldW = max(fieldW, term.Width(c.clean(ch.field)))
	}
	idW = min(idW, max(12, c.w/4))
	fieldW = min(fieldW, max(8, c.w/5))
	letterW := len(Letter(len(m.Columns) - 1))
	arrow := c.g(" → ", " -> ")
	for _, ch := range shown {
		lead := "  " + term.Fit(c.clean(ch.id), idW, term.AlignLeft, c.ell) + "  " +
			term.Fit(c.hue(ch.arm, p.Bold(Letter(ch.arm))), letterW, term.AlignLeft) + "  "
		room := c.w - term.Width(lead)
		var rest string
		switch {
		case ch.hasArw:
			field := term.Fit(c.clean(ch.field), fieldW, term.AlignLeft, c.ell) + "  "
			room -= term.Width(field)
			from, to := c.clean(ch.from), c.clean(ch.to)
			each := max(4, (room-term.Width(arrow))/2)
			// Give the side that needs less its room, the other the rest.
			if term.Width(from) <= each {
				to = term.Truncate(to, max(4, room-term.Width(arrow)-term.Width(from)), c.ell)
			} else if term.Width(to) <= each {
				from = term.Truncate(from, max(4, room-term.Width(arrow)-term.Width(to)), c.ell)
			} else {
				from, to = term.Truncate(from, each, c.ell), term.Truncate(to, each, c.ell)
			}
			rest = field + p.Prod(from) + p.Muted(arrow) + c.hue(ch.arm, to)
		case ch.field != "":
			field := term.Fit(c.clean(ch.field), fieldW, term.AlignLeft, c.ell) + "  "
			rest = field + c.clean(ch.plain)
		default:
			rest = c.clean(ch.plain)
		}
		lines = append(lines, term.Truncate(lead+rest, c.w, c.ell))
	}
	if more := len(all) - len(shown); more > 0 {
		note := "+" + core.FormatCount(more) + " more, see the explorer"
		if c.opts.MoreHint != nil {
			note = c.opts.MoreHint(more)
		}
		lines = append(lines, "  "+p.Muted(term.Truncate(c.clean(note), c.w-2, c.ell)))
	}
	return lines
}

// ---------------------------------------------------------------------------
// Not replayable

func lowerFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError || size == len(s) && size == 0 {
		return s
	}
	// Keep an acronym ("JSON ...") as it is.
	if next, _ := utf8.DecodeRuneInString(s[size:]); unicode.IsUpper(next) {
		return s
	}
	return string(unicode.ToLower(r)) + s[size:]
}

func notReplayableLine(c ctx, m *Model) []string {
	if len(m.NotReplayable) == 0 {
		return nil
	}
	total := 0
	for _, r := range m.NotReplayable {
		total += r.Count
	}
	calls := "calls"
	if total == 1 {
		calls = "call"
	}
	top := m.NotReplayable[0]
	text := core.FormatCount(total) + " " + calls + " not replayable: " + lowerFirst(c.clean(top.Title)) + "."
	if len(m.NotReplayable) > 1 {
		text = core.FormatCount(total) + " " + calls + " not replayable, most often: " + lowerFirst(c.clean(top.Title)) + " (" + core.FormatCount(top.Count) + ")."
	}
	var lines []string
	for _, l := range term.Wrap(text, c.w) {
		lines = append(lines, c.p.Muted(l))
	}
	return lines
}

// ---------------------------------------------------------------------------
// Footer

func footer(c ctx, m *Model) []string {
	p := c.p
	help := c.opts.Help
	if help == "" {
		help = ExplainHint
	}
	lead := "No noise floor in this run: differences are shown without production's own variation."
	if m.HasFloor {
		lead = "itself: " + c.clean(shortProduction(m)) + " replayed again, the noise floor."
	}
	var lines []string
	for _, l := range term.Wrap(lead+" Notes: "+help+".", c.w) {
		lines = append(lines, p.Muted(l))
	}
	if c.opts.Explore != "" {
		lead := "Explore every call:"
		command := "ezswap report " + c.clean(c.opts.Explore)
		if term.Width(lead)+1+term.Width(command) <= c.w {
			lines = append(lines, p.Muted(lead)+" "+p.Bold(command))
		} else {
			// A command alone on its line stays whole so it can be copied.
			lines = append(lines, p.Muted(lead), p.Bold(command))
		}
	}
	return lines
}

// ---------------------------------------------------------------------------
// Caveats

// Caveats are the notes the report leaves out of its body, as plain
// sentences, one paragraph each: how to read the itself column, what
// production's figures are, how cost is estimated and why calls were not
// replayed. The CLI prints them for --explain and the explorer under ?. Captured text in them is cleaned to one
// line each but not made ASCII.
func Caveats(m *Model) []string {
	clean := term.CleanLine
	prod := clean(shortProduction(m))
	var out []string
	for i, col := range m.Columns {
		text := Letter(i) + ": " + clean(col.Label)
		if col.Label != col.Model {
			text += " (" + clean(col.Model) + ")"
		}
		text += ", " + lowerFirst(col.Settings) + ", through " + clean(col.Endpoint) + "."
		if col.Note != nil && *col.Note != "" {
			text += " " + clean(*col.Note)
		}
		out = append(out, text)
	}
	if m.Floor != nil {
		out = append(out,
			"itself: "+prod+" replayed twice (A1 and A2) on the same "+core.FormatCount(m.Floor.Cases)+" calls. The gap between the two is what a rerun of production changes on its own; read every difference beside it. A figure marked "+BeyondMark+" is further from production than production is from itself. Neither figure says which output is better. Read more: "+NoiseFloorDoc,
		)
		note := "production here is " + prod + " replayed the same way as the treatments, for cost and latency."
		if bars := m.costOf(RoleProduction); len(bars) > 0 && bars[0].Note != nil {
			note += " Its recorded calls cost " + strings.TrimSuffix(*bars[0].Note, " recorded") + " each."
		}
		out = append(out, note)
		latency := "production here is its model replayed from this machine."
		if m.RecordedLatency != nil {
			latency += " Its recorded median (" + *m.RecordedLatency + ") is not compared: it ran at another time, over another network."
		}
		out = append(out, latency)
	} else {
		out = append(out, "This run has no noise floor, so differences are shown without production's own variation.")
	}
	if row := m.row("monthly_cost_usd"); row != nil && m.MonthlyCalls != nil && row.Production != core.MissingValue {
		out = append(out, "Per month: "+strOr(row.Sublabel, "")+".")
	} else {
		out = append(out, "No monthly projection: the run does not record the workload's call volume. Pass --monthly-calls N.")
	}
	out = append(out, "Cost per call is priced from each call's token counts with "+clean(m.Run.Catalog.Version)+".")
	for _, r := range m.NotReplayable {
		out = append(out, "Not replayable, "+lowerFirst(clean(r.Title))+" ("+core.FormatCount(r.Count)+"): "+r.Fix)
	}
	return out
}
