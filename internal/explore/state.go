package explore

// The explorer's state and its pure reducer: Reduce(state, key) returns the
// next state and never touches the terminal. Scroll positions are clamped
// against the same lines the screen draws, so the reducer and the renderer
// cannot disagree about where a page ends.

import (
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/metergraph/replay/internal/report"
	"github.com/metergraph/replay/internal/report/term"
)

// Tabs are the explorer's tabs, in order.
var Tabs = []string{"Summary", "Fields", "Noise floor", "Calls", "Not replayable"}

// CallsTab is the index of the call list.
const CallsTab = 3

// Detail is an open call: its index in the model's cases, and how far it is scrolled.
type Detail struct {
	Index  int
	Scroll int
}

// State is everything the explorer shows. It is a value: Reduce returns a
// new one and never changes the one it is given.
type State struct {
	Data *Data
	// Index into Tabs.
	Tab int
	// Scroll offset per tab, in lines (the Calls tab scrolls its list).
	Scroll []int
	// The highlighted row of the call list, an index into the filtered list.
	Cursor     int
	OnlyDiffer bool
	Filter     string
	// Typing into the filter.
	Typing bool
	Detail *Detail
	Help   bool
	Quit   bool
	// Columns and rows of the terminal.
	Columns int
	Rows    int
}

// NewState is the explorer at its start, on a tab.
func NewState(data *Data, columns, rows, tab int) State {
	return State{
		Data:    data,
		Tab:     max(0, min(len(Tabs)-1, tab)),
		Scroll:  make([]int, len(Tabs)),
		Columns: max(1, columns),
		Rows:    max(1, rows),
	}
}

// clone copies the slices a reducer step may change.
func (s State) clone() State {
	s.Scroll = slices.Clone(s.Scroll)
	if s.Detail != nil {
		d := *s.Detail
		s.Detail = &d
	}
	return s
}

// BodyHeight is the rows between the tab bar and the status bar.
func BodyHeight(rows int) int { return max(1, rows-3) }

// ListHeight is the rows the call list has for calls, below its column header.
func ListHeight(rows int) int { return max(1, BodyHeight(rows)-2) }

func haystack(c report.CaseItem) string {
	parts := []string{c.TraceID, c.ID, strOr(c.Input, ""), strOr(c.Reason, ""), strOr(c.Production.Output, "")}
	for _, a := range c.Arms {
		parts = append(parts, strOr(a.Output, ""), strOr(a.Error, ""))
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}

// VisibleCalls are the indexes into the model's cases that the call list
// shows, after "only differ" and the text filter.
func VisibleCalls(s State) []int {
	needle := strings.ToLower(strings.TrimSpace(s.Filter))
	out := []int{}
	for i, c := range s.Data.Clean.Cases {
		if s.OnlyDiffer && !c.Differs {
			continue
		}
		if needle != "" && !strings.Contains(haystack(c), needle) {
			continue
		}
		out = append(out, i)
	}
	return out
}

// Lines a scrollable tab or the open call takes at this width, drawn
// without colour (colour never changes a line count).
func countLines(s State, tab int) int {
	return len(tabLines(s.Data, tab, contentCtx(s.Columns, term.PlainPaint(), s.Data.ASCII)))
}

func countDetail(s State) int {
	if s.Detail == nil {
		return 0
	}
	return len(detailLines(s.Data, s.Detail.Index, contentCtx(s.Columns, term.PlainPaint(), s.Data.ASCII)))
}

func clamp(v, lo, hi int) int { return min(max(v, lo), max(lo, hi)) }

func withScroll(s State, tab, value int) State {
	s = s.clone()
	s.Scroll[tab] = clamp(value, 0, max(0, countLines(s, tab)-BodyHeight(s.Rows)))
	return s
}

// settleList keeps the cursor in range and on screen.
func settleList(s State) State {
	s = s.clone()
	n := len(VisibleCalls(s))
	s.Cursor = clamp(s.Cursor, 0, n-1)
	page := ListHeight(s.Rows)
	top := s.Scroll[CallsTab]
	if s.Cursor < top {
		top = s.Cursor
	}
	if s.Cursor >= top+page {
		top = s.Cursor - page + 1
	}
	s.Scroll[CallsTab] = clamp(top, 0, n-page)
	return s
}

func scrollDetail(s State, value int) State {
	if s.Detail == nil {
		return s
	}
	s = s.clone()
	s.Detail.Scroll = clamp(value, 0, max(0, countDetail(s)-BodyHeight(s.Rows)))
	return s
}

func switchTab(s State, tab int) State {
	s = s.clone()
	n := len(Tabs)
	s.Tab = ((tab % n) + n) % n
	s.Detail = nil
	s.Typing = false
	return s
}

// The farthest any scroll goes; clamped to the content.
const farEnd = math.MaxInt32

// Key names what Reduce reads: "up", "down", "left", "right", "pageup",
// "pagedown", "home", "end", "enter", "escape", "backspace", "tab",
// "ctrl-c", or one printable character.
type Key = string

// Reduce is the state after one key.
func Reduce(s State, key Key) State {
	if s.Quit {
		return s
	}
	if key == "ctrl-c" {
		s = s.clone()
		s.Quit = true
		return s
	}

	if s.Typing {
		switch key {
		case "enter", "tab":
			s = s.clone()
			s.Typing = false
			return settleList(s)
		case "escape":
			s = s.clone()
			s.Typing, s.Filter, s.Cursor = false, "", 0
			return settleList(s)
		case "backspace":
			s = s.clone()
			if s.Filter == "" {
				s.Typing = false
				return s
			}
			_, size := utf8.DecodeLastRuneInString(s.Filter)
			s.Filter, s.Cursor = s.Filter[:len(s.Filter)-size], 0
			return settleList(s)
		case "up", "down":
			s = s.clone()
			s.Typing = false
			return Reduce(s, key)
		}
		if utf8.RuneCountInString(key) == 1 && key >= " " {
			s = s.clone()
			s.Filter, s.Cursor = s.Filter+key, 0
			return settleList(s)
		}
		return s
	}

	if s.Help {
		s = s.clone()
		if key == "q" {
			s.Quit = true
			return s
		}
		s.Help = false
		return s
	}
	switch key {
	case "q":
		s = s.clone()
		s.Quit = true
		return s
	case "?":
		s = s.clone()
		s.Help = true
		return s
	case "left":
		return switchTab(s, s.Tab-1)
	case "right", "tab":
		return switchTab(s, s.Tab+1)
	case "1", "2", "3", "4", "5":
		return switchTab(s, int(key[0]-'1'))
	}

	page := BodyHeight(s.Rows)
	if s.Detail != nil {
		at := s.Detail.Scroll
		switch key {
		case "escape", "backspace":
			s = s.clone()
			s.Detail = nil
			return settleList(s)
		case "up", "k":
			return scrollDetail(s, at-1)
		case "down", "j":
			return scrollDetail(s, at+1)
		case "pageup":
			return scrollDetail(s, at-page)
		case "pagedown", " ":
			return scrollDetail(s, at+page)
		case "home", "g":
			return scrollDetail(s, 0)
		case "end", "G":
			return scrollDetail(s, farEnd)
		case "n", "p":
			list := VisibleCalls(s)
			if len(list) == 0 {
				return s
			}
			here := slices.Index(list, s.Detail.Index)
			step := 1
			if key == "p" {
				step = -1
			}
			at := clamp(here+step, 0, len(list)-1)
			s = s.clone()
			s.Cursor = at
			s.Detail = &Detail{Index: list[at]}
			return s
		}
		return s
	}

	if s.Tab == CallsTab {
		list := VisibleCalls(s)
		lpage := ListHeight(s.Rows)
		move := func(cursor int) State {
			n := s.clone()
			n.Cursor = cursor
			return settleList(n)
		}
		switch key {
		case "up", "k":
			return move(s.Cursor - 1)
		case "down", "j":
			return move(s.Cursor + 1)
		case "pageup":
			return move(s.Cursor - lpage)
		case "pagedown", " ":
			return move(s.Cursor + lpage)
		case "home", "g":
			return move(0)
		case "end", "G":
			return move(len(list) - 1)
		case "enter":
			if s.Cursor < 0 || s.Cursor >= len(list) {
				return s
			}
			s = s.clone()
			s.Detail = &Detail{Index: list[s.Cursor]}
			return s
		case "d":
			s = s.clone()
			s.OnlyDiffer, s.Cursor = !s.OnlyDiffer, 0
			return settleList(s)
		case "/":
			s = s.clone()
			s.Typing = true
			return s
		case "escape":
			if s.Filter == "" {
				return s
			}
			s = s.clone()
			s.Filter, s.Cursor = "", 0
			return settleList(s)
		}
		return s
	}

	at := s.Scroll[s.Tab]
	switch key {
	case "up", "k":
		return withScroll(s, s.Tab, at-1)
	case "down", "j":
		return withScroll(s, s.Tab, at+1)
	case "pageup":
		return withScroll(s, s.Tab, at-page)
	case "pagedown", " ":
		return withScroll(s, s.Tab, at+page)
	case "home", "g":
		return withScroll(s, s.Tab, 0)
	case "end", "G":
		return withScroll(s, s.Tab, farEnd)
	case "d", "/":
		// The call list's keys jump to it from any tab.
		return Reduce(switchTab(s, CallsTab), key)
	}
	return s
}

// Resize is the state at a new terminal size, every position kept valid.
func Resize(s State, columns, rows int) State {
	s = s.clone()
	s.Columns, s.Rows = max(1, columns), max(1, rows)
	for tab := range Tabs {
		if tab != CallsTab {
			s = withScroll(s, tab, s.Scroll[tab])
		}
	}
	s = settleList(s)
	if s.Detail != nil {
		s = scrollDetail(s, s.Detail.Scroll)
	}
	return s
}
