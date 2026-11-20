package mapview

// One full frame of the map explorer, every line exactly as wide as the
// terminal. Pure: the terminal is never touched here. Captured text is
// cleaned before it is drawn, so a reply cannot drive the terminal.

import (
	"fmt"
	"strings"

	"github.com/metergraph/replay/internal/core"
	"github.com/metergraph/replay/internal/report"
	"github.com/metergraph/replay/internal/report/term"
)

// Colours of the labels, as the HTML page draws them in its dark theme.
const (
	beyondHue = "#f08a60"
	withinHue = "#7cc9a0"
	accentHue = "#8aa8ff"
)

// Screen is how to draw a frame.
type Screen struct {
	Columns int
	Rows    int
	Paint   term.Paint
	ASCII   bool
}

type ctx struct {
	p     term.Paint
	w     int
	ascii bool
}

func (c ctx) g(fancy, plain string) string {
	if c.ascii {
		return plain
	}
	return fancy
}

func (c ctx) cut(text string, width int) string {
	return term.Truncate(term.CleanLine(text), max(1, width), c.g("…", "~"))
}

// ScreenLines is the frame as lines, one per row.
func ScreenLines(s State, o Screen) []string {
	columns, rows := max(20, o.Columns), max(6, o.Rows)
	s.Columns, s.Rows = columns, rows
	c := ctx{p: o.Paint, w: columns, ascii: o.ASCII}
	height := rows - 3
	var body []string
	switch s.View {
	case ViewGrid:
		body = gridLines(s, c)
	case ViewCell:
		body = cellLines(s, c, height)
	default:
		body = scrolled(caseLines(s, c), s.Scroll, height)
	}
	lines := []string{header(s, c), c.p.Line(strings.Repeat(c.g("─", "-"), columns))}
	lines = append(lines, body...)
	for len(lines) < rows-1 {
		lines = append(lines, "")
	}
	lines = append(lines[:rows-1], footer(s, c))
	if s.Help {
		lines = withHelp(lines, c)
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if o.ASCII {
			l = term.ToAscii(l)
		}
		out[i] = term.Fit(l, columns, term.AlignLeft, c.g("…", "~"))
	}
	return out
}

// Render is the frame joined by newlines.
func Render(s State, o Screen) string {
	return strings.Join(ScreenLines(s, o), "\n")
}

func scrolled(lines []string, at, height int) []string {
	start := min(max(0, at), max(0, len(lines)-height))
	return lines[start:min(len(lines), start+height)]
}

func header(s State, c ctx) string {
	m := s.Data.Map
	logo := c.p.Brand(c.g("▁▃▅▇", "::")) + " " + c.p.Bold("replay map")
	facts := []string{
		fmt.Sprintf("%d %s", len(m.Parts), plural(len(m.Parts), "part", "parts")),
		term.CleanLine(m.File),
		fmt.Sprintf("%d cases per part", m.PerPart),
		"spent " + core.FormatUSD(m.SpentUSD),
	}
	return logo + "  " + c.p.Muted(strings.Join(facts, c.g(" · ", " | ")))
}

func footer(s State, c ctx) string {
	var keys []string
	switch s.View {
	case ViewGrid:
		keys = []string{c.g("←↑↓→", "arrows") + " move", "enter open", "q quit"}
	case ViewCell:
		keys = []string{c.g("↑↓", "up, down") + " choose", "enter open", "a all cases", "esc back", "q quit"}
	default:
		keys = []string{c.g("↑↓", "up, down") + " scroll", c.g("←→", "left, right") + " previous, next", "esc back", "q quit"}
	}
	if s.Data.HTMLPath != "" {
		keys = append(keys[:len(keys)-1], "o open page", keys[len(keys)-1])
	}
	keys = append(keys, "? help")
	return c.p.Muted(" " + strings.Join(keys, "   "))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// cellText is a grid cell: "beyond 9/40", "within 2/40", "too few", ...
func cellText(t report.MapTreatment) string {
	text, cut := report.SummaryCell(t)
	if cut {
		text += "*"
	}
	return text
}

func paintResult(c ctx, result, text string) string {
	switch result {
	case report.ResultBeyondNoise:
		return c.p.Bold(c.p.Hue(beyondHue, text))
	case report.ResultWithinNoise:
		return c.p.Hue(withinHue, text)
	}
	return c.p.Muted(text)
}

func gridLines(s State, c ctx) []string {
	d := s.Data
	m := d.Map
	modelW := 10
	for ti, t := range m.Treatments {
		modelW = max(modelW, term.Width(report.Letter(ti)+" "+term.CleanLine(t.Label))+2)
	}
	modelW = min(modelW, 34)
	costW := 10
	cellW := 15
	n := len(d.Parts)
	if n > 0 {
		cellW = max(13, min(20, (c.w-modelW-costW-4)/n))
	}
	// The parts that fit, scrolled to keep the selected one in view.
	fit := max(1, (c.w-modelW-costW-4)/cellW)
	first := min(max(0, s.Col-fit+1), max(0, n-fit))
	last := min(n, first+fit)
	more := func(cond bool, mark string) string {
		if cond {
			return c.p.Muted(mark)
		}
		return " "
	}
	lines := []string{"", " " + c.p.Bold("Each model on each part") + c.p.Muted(". Choose a cell to see the cases behind it."), ""}
	head := " " + term.PadEnd("", modelW) + more(first > 0, c.g("‹", "<"))
	for _, part := range d.Parts[first:last] {
		head += term.PadEnd(c.cut(part.Name, cellW-2), cellW)
	}
	lines = append(lines, c.p.Muted(head)+more(last < n, c.g("›", ">"))+c.p.Muted("COST/CALL"))
	for ti, t := range m.Treatments {
		row := " " + term.PadEnd(c.cut(report.Letter(ti)+" "+t.Label, modelW-2), modelW) + " "
		for pi := first; pi < last; pi++ {
			part := d.Parts[pi]
			mt := part.Treatments[ti]
			text := term.PadEnd(cellText(mt), cellW-2)
			if ti == s.Row && pi == s.Col {
				if c.p.Color {
					row += c.p.Reverse(" " + text + " ")
				} else {
					row += "[" + text + "]"
				}
				continue
			}
			row += " " + paintResult(c, mt.Result, text) + " "
		}
		row += " " + percent(report.SummaryCost(m, ti))
		lines = append(lines, row)
	}
	lines = append(lines, "")
	// The selected cell in a sentence.
	mt := s.Cell()
	part := d.Parts[s.Col]
	t := m.Treatments[s.Row]
	what := report.Letter(s.Row) + " " + term.CleanLine(t.Label) + " on " + term.CleanLine(part.Name) + ": "
	switch mt.Result {
	case report.ResultBeyondNoise, report.ResultWithinNoise:
		what += fmt.Sprintf("%s, %d of %d changed against %d on production's rerun. Cost per call %s.",
			labelName(mt.Result), mt.Changed, mt.Paired, mt.Rerun, percent(mt.CostPerCallChange))
	default:
		reason := ""
		if mt.Reason != nil {
			reason = ": " + *mt.Reason
		}
		what += labelName(mt.Result) + reason + "."
	}
	for _, l := range term.Wrap(what, c.w-2) {
		lines = append(lines, " "+l)
	}
	lines = append(lines, "")
	notes := []string{}
	if note := report.JudgedNote(m); note != "" {
		notes = append(notes, note)
	}
	notes = append(notes, report.MapRule, report.MapFooter)
	for _, n := range notes {
		for _, l := range term.Wrap(term.CleanLine(n), c.w-2) {
			lines = append(lines, " "+c.p.Muted(l))
		}
	}
	if anyCut(d) {
		lines = append(lines, " "+c.p.Muted("*: replies cut off at the output cap. Open the cell to see whether reasoning used the cap."))
	}
	return lines
}

func anyCut(d *Data) bool {
	for _, part := range d.Parts {
		for _, t := range part.Treatments {
			if t.CutOff > 0 {
				return true
			}
		}
	}
	return false
}

func labelName(result string) string {
	switch result {
	case report.ResultBeyondNoise:
		return "beyond noise"
	case report.ResultWithinNoise:
		return "within noise"
	case report.ResultTooFewCalls:
		return "too few calls"
	}
	return "not comparable"
}

func percent(v *float64) string {
	if v == nil {
		return "-"
	}
	p := *v * 100
	switch {
	case p >= 0.5:
		return fmt.Sprintf("+%.0f%%", p)
	case p <= -0.5:
		return fmt.Sprintf("-%.0f%%", -p)
	}
	return "0%"
}

func cellTitle(s State, c ctx) []string {
	part := s.Data.Parts[s.Col]
	t := s.Data.Map.Treatments[s.Row]
	return []string{"", " " + c.p.Bold(term.CleanLine(part.Name)) + "  " + c.p.Muted(report.Letter(s.Row)+" "+term.CleanLine(t.Label)+" against the recording")}
}

func cellLines(s State, c ctx, height int) []string {
	mt := s.Cell()
	lines := cellTitle(s, c)
	figures := []string{paintResult(c, mt.Result, labelName(mt.Result))}
	if mt.Result == report.ResultBeyondNoise || mt.Result == report.ResultWithinNoise {
		figures = append(figures,
			fmt.Sprintf("changed %d of %d", mt.Changed, mt.Paired),
			fmt.Sprintf("production rerun %d of %d", mt.Rerun, mt.Paired))
	} else if mt.Reason != nil {
		figures = append(figures, term.CleanLine(*mt.Reason))
	}
	figures = append(figures, "cost per call "+percent(mt.CostPerCallChange), "latency "+percent(mt.LatencyChange))
	if mt.CutOff > 0 {
		figures = append(figures, fmt.Sprintf("cut off %d of %d", mt.CutOff, mt.Paired))
	}
	if mt.Judged && mt.Unsure > 0 {
		figures = append(figures, fmt.Sprintf("judge unsure %d", mt.Unsure))
	}
	lines = append(lines, " "+strings.Join(figures, c.p.Muted(c.g("  ·  ", "  |  "))), "")
	var fields []string
	for _, pc := range mt.Paths {
		if pc.Changed > 0 || pc.Rerun > 0 {
			fields = append(fields, fmt.Sprintf("%s changed %d, rerun %d", report.ShortPath(pc.Path), pc.Changed, pc.Rerun))
		}
	}
	if len(fields) > 0 {
		lines = append(lines, " "+c.p.Muted("FIELDS")+"  "+strings.Join(fields, c.p.Muted(c.g("  ·  ", "  |  "))), "")
	}
	notes := append([]string(nil), mt.Notes...)
	if mt.CutOff > 0 {
		notes = append(notes, report.CutOffNote(s.Data.Parts[s.Col].Name, mt))
	}
	for _, n := range notes {
		for i, l := range term.Wrap(term.CleanLine(n), c.w-4) {
			bullet := c.g("• ", "- ")
			if i > 0 {
				bullet = "  "
			}
			lines = append(lines, " "+c.p.Muted(bullet+l))
		}
	}
	if len(notes) > 0 {
		lines = append(lines, "")
	}
	entries := s.Entries()
	which := "changed cases"
	if s.All {
		which = "all cases"
	}
	lines = append(lines, " "+c.p.Muted(fmt.Sprintf("%s (%d)", strings.ToUpper(which), len(entries))))
	if len(entries) == 0 {
		return append(lines, " "+c.p.Muted("No cases. Press a to show all cases."))
	}
	room := max(3, height-len(lines))
	start := min(max(0, s.Cursor-room/2), max(0, len(entries)-room))
	idW := 0
	for _, e := range entries {
		idW = max(idW, term.Width(e.Case.ID))
	}
	idW = min(idW, 20)
	for i := start; i < min(len(entries), start+room); i++ {
		e := entries[i]
		status := term.PadEnd(e.Status, 10)
		switch e.Status {
		case "changed":
			status = c.p.Hue(beyondHue, status)
		case "same":
			status = c.p.Hue(withinHue, status)
		default:
			status = c.p.Muted(status)
		}
		text := e.Change
		if text == "" {
			text = e.Case.Preview
		}
		rest := term.PadEnd(c.cut(e.Case.ID, idW), idW) + "  " + c.cut(text, c.w-idW-20)
		row := " " + status + " " + rest
		if i == s.Cursor {
			if c.p.Color {
				row = " " + c.p.Reverse(term.PadEnd(term.PadEnd(e.Status, 10)+" "+rest, c.w-3))
			} else {
				row = ">" + term.PadEnd(e.Status, 10) + " " + rest
			}
		}
		lines = append(lines, row)
	}
	return lines
}

func caseLines(s State, c ctx) []string {
	entries := s.Entries()
	if len(entries) == 0 {
		return cellTitle(s, c)
	}
	e := entries[min(s.Cursor, len(entries)-1)]
	arm := s.Data.Map.Treatments[s.Row].ID
	lines := cellTitle(s, c)
	lines = append(lines, " "+c.p.Muted(fmt.Sprintf("case %d of %d  ", min(s.Cursor, len(entries)-1)+1, len(entries)))+term.CleanLine(e.Case.ID)+"  "+statusText(c, e.Status))
	if e.Change != "" {
		lines = append(lines, " "+c.p.Hue(beyondHue, c.cut(e.Change, c.w-2)))
	}
	lines = append(lines, "", " "+c.p.Muted("REQUEST"))
	for _, l := range wrapBlock(e.Case.Request, c.w-4) {
		lines = append(lines, "   "+l)
	}
	replies := []Reply{e.Case.Recorded}
	if r, ok := e.Case.Arms[arm]; ok {
		replies = append(replies, r)
	} else {
		replies = append(replies, Reply{Label: report.Letter(s.Row) + " " + s.Data.Map.Treatments[s.Row].Label, Problem: "no reply"})
	}
	if r, ok := e.Case.Arms["floor_a1"]; ok {
		replies = append(replies, r)
	}
	lines = append(lines, "")
	if c.w >= 110 {
		return append(lines, sideBySide(replies, c)...)
	}
	for _, r := range replies {
		lines = append(lines, " "+replyHead(r, c))
		for _, l := range replyBody(r, c.w-4) {
			lines = append(lines, "   "+l)
		}
		lines = append(lines, "")
	}
	return lines
}

func statusText(c ctx, status string) string {
	switch status {
	case "changed":
		return c.p.Hue(beyondHue, status)
	case "same":
		return c.p.Hue(withinHue, status)
	}
	return c.p.Muted(status)
}

func replyHead(r Reply, c ctx) string {
	head := c.p.Bold(term.CleanLine(r.Label))
	var facts []string
	if r.LatencyMs != nil {
		facts = append(facts, fmt.Sprintf("%.1f s", *r.LatencyMs/1000))
	}
	if r.CostUSD != nil {
		facts = append(facts, core.FormatUSD(*r.CostUSD))
	}
	if len(facts) > 0 {
		head += "  " + c.p.Muted(strings.Join(facts, " · "))
	}
	if r.Problem != "" {
		head += "  " + c.p.Hue(beyondHue, r.Problem)
	}
	return head
}

func replyBody(r Reply, width int) []string {
	if strings.TrimSpace(r.Text) == "" {
		return []string{"(no text)"}
	}
	return wrapBlock(pretty(r.Text), width)
}

// pretty indents a JSON reply so its fields line up; other text is kept.
func pretty(text string) string {
	parsed := core.ParseOutput(text)
	if !parsed.OK {
		return text
	}
	return core.Stringify(parsed.Value, "  ")
}

// wrapBlock wraps each line of a text, cleaned, keeping its line breaks.
func wrapBlock(text string, width int) []string {
	var out []string
	for _, line := range strings.Split(term.Clean(text), "\n") {
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, term.Wrap(line, max(10, width))...)
	}
	return out
}

func sideBySide(replies []Reply, c ctx) []string {
	n := len(replies)
	gap := 3
	colW := (c.w - 2 - gap*(n-1)) / n
	cols := make([][]string, n)
	heads := make([]string, n)
	tallest := 0
	for i, r := range replies {
		heads[i] = replyHead(r, c)
		cols[i] = replyBody(r, colW)
		tallest = max(tallest, len(cols[i]))
	}
	var lines []string
	row := " "
	for i, h := range heads {
		row += term.Fit(h, colW, term.AlignLeft, c.g("…", "~"))
		if i < n-1 {
			row += strings.Repeat(" ", gap)
		}
	}
	lines = append(lines, row, " "+c.p.Line(strings.Repeat(c.g("─", "-"), c.w-2)))
	for k := 0; k < tallest; k++ {
		row := " "
		for i := range cols {
			cell := ""
			if k < len(cols[i]) {
				cell = cols[i][k]
			}
			row += term.PadEnd(cell, colW)
			if i < n-1 {
				row += strings.Repeat(" ", gap)
			}
		}
		lines = append(lines, row)
	}
	return lines
}

var helpText = []string{
	"The map compares each model's answers with the recorded ones, part by",
	"part, against how often production's own model changes its answer on a",
	"rerun. A cell is beyond noise when the model changes more than that, by",
	"a paired sign test at 5%.",
	"",
	"Grid      arrows or h j k l move, enter opens a cell",
	"Cell      up and down choose a case, enter opens it, a shows every case",
	"Case      up and down scroll, left and right step through the cases",
	"Anywhere  esc goes back, o opens the HTML page, q quits",
}

func withHelp(lines []string, c ctx) []string {
	inner := 0
	for _, l := range helpText {
		inner = max(inner, term.Width(l))
	}
	box := term.Box(helpText, term.BoxOptions{Width: min(c.w-2, inner+4), Title: "help", Chars: term.CharsetFor(c.ascii), Paint: c.p})
	top := max(2, (len(lines)-len(box))/2)
	left := max(0, (c.w-term.Width(box[0]))/2)
	for i, b := range box {
		if top+i < len(lines)-1 {
			lines[top+i] = strings.Repeat(" ", left) + b
		}
	}
	return lines
}
