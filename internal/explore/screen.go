package explore

// ScreenLines: one full frame of the explorer, every line exactly as wide
// as the terminal, so a redraw replaces the whole screen. Pure: the
// terminal is never touched here.

import (
	"strconv"
	"strings"

	"github.com/metergraph/ezswap/internal/core"
	"github.com/metergraph/ezswap/internal/report/term"
)

// Screen is how to draw a frame.
type Screen struct {
	Columns int
	Rows    int
	Paint   term.Paint
	ASCII   bool
}

// ScreenLines is the frame as lines, one per row.
func ScreenLines(s State, o Screen) []string {
	columns := max(1, o.Columns)
	rows := max(3, o.Rows)
	s.Columns, s.Rows = columns, rows
	c := contentCtx(columns, o.Paint, o.ASCII)
	height := BodyHeight(rows)
	var body []string
	switch {
	case s.Detail != nil:
		body = scrolled(detailLines(s.Data, s.Detail.Index, c), s.Detail.Scroll, height)
	case s.Tab == CallsTab:
		body = callList(s, c, height)
	default:
		body = scrolled(tabLines(s.Data, s.Tab, c), s.Scroll[s.Tab], height)
	}
	lines := []string{tabBar(s, c), c.p.Line(strings.Repeat(c.g("─", "-"), columns))}
	lines = append(lines, body...)
	for len(lines) < rows-1 {
		lines = append(lines, "")
	}
	lines = lines[:rows-1]
	lines = append(lines, statusBar(s, c))
	out := make([]string, len(lines))
	for i, l := range lines {
		if i == rows-1 {
			out[i] = l
		} else {
			out[i] = term.Fit(l, columns, term.AlignLeft, c.ell)
		}
	}
	if s.Help {
		out = overlay(out, helpPanel(s, c, rows-3), columns)
	}
	if o.ASCII {
		for i, l := range out {
			out[i] = term.ToAscii(l)
		}
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

func tabBar(s State, c ctx) string {
	p := c.p
	logo := p.Brand(c.g("▁▃▅▇", "::")) + " " + p.Bold("ezswap")
	names := [3][]string{}
	for i, t := range Tabs {
		n := strconv.Itoa(i + 1)
		names[0] = append(names[0], n+" "+t)
		names[1] = append(names[1], n+" "+strings.Split(t, " ")[0])
		names[2] = append(names[2], n)
	}
	for _, set := range names {
		tabs := make([]string, len(set))
		for i, t := range set {
			if i == s.Tab {
				tabs[i] = p.Tab(" " + t + " ")
			} else {
				tabs[i] = p.Muted(" " + t + " ")
			}
		}
		line := " " + logo + "  " + strings.Join(tabs, " ")
		if term.Width(line) <= s.Columns {
			return line
		}
	}
	return term.Truncate(" "+logo, s.Columns, c.ell)
}

func percent(at, lines, height int) string {
	if lines <= height {
		return "all"
	}
	most := lines - height
	switch {
	case at <= 0:
		return "top"
	case at >= most:
		return "end"
	}
	return strconv.Itoa(int(float64(at)/float64(most)*100+0.5)) + "%"
}

func position(s State, c ctx) string {
	plain := contentCtx(s.Columns, term.PlainPaint(), c.ascii)
	if s.Detail != nil {
		list := VisibleCalls(s)
		at := -1
		for i, v := range list {
			if v == s.Detail.Index {
				at = i
			}
		}
		lines := len(detailLines(s.Data, s.Detail.Index, plain))
		call := "call"
		if at >= 0 {
			call = "call " + strconv.Itoa(at+1) + " of " + strconv.Itoa(len(list))
		}
		return call + ", " + percent(s.Detail.Scroll, lines, BodyHeight(s.Rows))
	}
	if s.Tab == CallsTab {
		list := VisibleCalls(s)
		total := len(s.Data.Clean.Cases)
		shown := core.FormatCount(total) + " calls"
		if len(list) != total {
			shown = core.FormatCount(len(list)) + " of " + core.FormatCount(total) + " calls"
		}
		if len(list) > 0 {
			return strconv.Itoa(s.Cursor+1) + "/" + strconv.Itoa(len(list)) + ", " + shown
		}
		return shown
	}
	return percent(s.Scroll[s.Tab], len(tabLines(s.Data, s.Tab, plain)), BodyHeight(s.Rows))
}

func hints(s State) []string {
	switch {
	case s.Typing:
		return []string{"type to filter", "enter done", "esc clear"}
	case s.Detail != nil:
		return []string{"↑↓ scroll", "n/p next call", "esc back", "? help", "q quit"}
	case s.Tab == CallsTab:
		return []string{"↑↓ move", "enter open", "d differ", "/ filter", "←→ tabs", "? help", "q quit"}
	}
	return []string{"↑↓ scroll", "←→ tabs", "? help", "q quit"}
}

func statusBar(s State, c ctx) string {
	tab := Tabs[s.Tab]
	where := tab
	if s.Detail != nil {
		id := ""
		if s.Detail.Index < len(s.Data.Clean.Cases) {
			id = s.Data.Clean.Cases[s.Detail.Index].TraceID
		}
		where = tab + " › " + id
	}
	var flags []string
	if s.OnlyDiffer {
		flags = append(flags, "only calls that differ")
	}
	if s.Filter != "" || s.Typing {
		cursor := ""
		if s.Typing {
			cursor = "_"
		}
		flags = append(flags, "filter: "+term.CleanLine(s.Filter)+cursor)
	}
	left := " " + s.Data.Name + "  " + where + "  " + position(s, c)
	if len(flags) > 0 {
		left += "  [" + strings.Join(flags, ", ") + "]"
	}
	left += " "
	keys := hints(s)
	right := ""
	for n := len(keys); n > 0; n-- {
		text := " " + strings.Join(keys[:n], "  ") + " "
		if term.Width(left)+term.Width(text) <= s.Columns {
			right = text
			break
		}
	}
	gap := max(0, s.Columns-term.Width(left)-term.Width(right))
	line := term.Truncate(left+strings.Repeat(" ", gap)+right, s.Columns, c.ell)
	return c.p.Bar(term.Fit(line, s.Columns, term.AlignLeft))
}

func callList(s State, c ctx, height int) []string {
	p := c.p
	cases := s.Data.Clean.Cases
	list := VisibleCalls(s)
	cols := min(s.Columns, 160)
	idW := 8
	for _, cs := range cases {
		idW = max(idW, term.Width(cs.TraceID))
	}
	idW = min(22, idW)
	const statusW = 18
	mark := func(on bool) string {
		if on {
			return p.Treat(c.g("●", "*"))
		}
		return p.Muted(c.g("·", "."))
	}
	lines := []string{
		p.Muted("    " + term.Fit("trace", idW, term.AlignLeft) + "  " + term.Fit("status", statusW, term.AlignLeft) + "  input"),
		p.Line(strings.Repeat(c.g("─", "-"), cols)),
	}
	if len(list) == 0 {
		if s.Filter != "" {
			lines = append(lines, p.Muted("  No call matches \""+term.CleanLine(s.Filter)+"\"."))
		} else {
			lines = append(lines, p.Muted("  No call differs from production."))
		}
		return lines
	}
	top := s.Scroll[CallsTab]
	for i := top; i < min(len(list), top+ListHeight(s.Rows)); i++ {
		cs := cases[list[i]]
		status := strOr(cs.Reason, "not replayable")
		if cs.Status == "replayed" {
			status = "same"
			if cs.Differs {
				status = "differs"
			}
		}
		selected := i == s.Cursor
		pointer := " "
		if selected {
			pointer = c.g("›", ">")
		}
		var statusText string
		switch {
		case cs.Status != "replayed":
			statusText = p.Floor(status)
		case cs.Differs:
			statusText = p.Treat(status)
		default:
			statusText = p.Muted(status)
		}
		id := cs.TraceID
		if selected {
			id = p.Bold(id)
		}
		input := oneLine(strOr(cs.Input, ""))
		row := pointer + " " + mark(cs.Differs) + " " + term.Fit(id, idW, term.AlignLeft, c.ell) + "  " + term.Fit(statusText, statusW, term.AlignLeft, c.ell) + "  " + input
		fitted := term.Fit(row, cols, term.AlignLeft, c.ell)
		if selected {
			fitted = p.Select(fitted)
		}
		lines = append(lines, fitted)
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return lines
}

var helpKeys = [][2]string{
	{"← → or 1-5", "switch tabs"},
	{"↑ ↓  j k", "move or scroll"},
	{"PgUp PgDn  space", "a page"},
	{"g G  Home End", "first, last"},
	{"enter", "open the call"},
	{"esc  backspace", "back"},
	{"n p", "next, previous call"},
	{"d", "only calls that differ"},
	{"/", "filter calls by text"},
	{"?", "this help"},
	{"q", "quit"},
}

// helpPanel is the keys, then the notes (report.Caveats), in a box at most
// room rows tall.
func helpPanel(s State, c ctx, room int) []string {
	p := c.p
	keyW := 0
	for _, kv := range helpKeys {
		keyW = max(keyW, term.Width(kv[0]))
	}
	w := min(c.w, 80)
	inner := w - 4
	var lines []string
	for _, kv := range helpKeys {
		lines = append(lines, p.Bold(term.Fit(kv[0], keyW, term.AlignLeft))+"  "+kv[1])
	}
	// Borders, the divider, and the closing line with the blank before it.
	budget := max(0, room-5-len(lines))
	more := term.Wrap("More notes: ezswap report "+s.Data.Name+" --explain", inner)
	var notes []string
	for i, note := range s.Data.Caveats {
		paragraph := term.Wrap(term.CleanLine(note), inner)
		if i > 0 {
			paragraph = append([]string{""}, paragraph...)
		}
		// Whole paragraphs only, leaving room to say where the rest is.
		rest := 0
		if i < len(s.Data.Caveats)-1 {
			rest = 1 + len(more)
		}
		if len(notes)+len(paragraph)+rest > budget {
			if len(notes)+1+len(more) <= budget {
				if len(notes) > 0 {
					notes = append(notes, "")
				}
				notes = append(notes, more...)
			}
			break
		}
		for _, l := range paragraph {
			notes = append(notes, p.Muted(l))
		}
	}
	closing := p.Muted("Any key closes this.")
	note := ""
	if len(notes) > 0 {
		note = "and notes"
		lines = append(lines, term.Divider)
		lines = append(lines, notes...)
	}
	lines = append(lines, "", closing)
	return term.Box(lines, term.BoxOptions{Width: w, Chars: c.chars, Paint: p, Title: "Keys", Note: note, Ellipsis: c.ell})
}

// overlay draws panel over the middle of lines.
func overlay(lines, panel []string, columns int) []string {
	out := append([]string{}, lines...)
	top := max(2, (len(lines)-len(panel))/2)
	w := 0
	for _, l := range panel {
		w = max(w, term.Width(l))
	}
	left := max(0, (columns-w)/2)
	for i, l := range panel {
		row := top + i
		if row >= len(out)-1 {
			break
		}
		out[row] = term.Fit(strings.Repeat(" ", left)+l, columns, term.AlignLeft)
	}
	return out
}
