package mapview

// The map explorer's state and its pure reducer: Reduce(state, key) returns
// the next state and never touches the terminal.

import (
	"github.com/metergraph/replay/internal/report"
)

// Views, from the outside in.
const (
	ViewGrid = iota
	ViewCell
	ViewCase
)

// Key is a key as the reducer reads it: "up", "enter", "q", ...
type Key = string

// State is everything the explorer shows. Reduce returns a new one.
type State struct {
	Data *Data
	View int
	// The selected treatment and part (an index into Data.Parts).
	Row, Col int
	// The highlighted case in the cell's list.
	Cursor int
	// The cell's list shows every case, not only the changed ones.
	All bool
	// How far the case view is scrolled, in lines.
	Scroll int
	Help   bool
	Quit   bool
	// Set when o asks for the HTML page; the program opens it and clears it.
	OpenPage bool
	Columns  int
	Rows     int
}

// NewState is the explorer on its grid, the first beyond-noise cell
// selected when there is one.
func NewState(d *Data, columns, rows int) State {
	s := State{Data: d, Columns: max(1, columns), Rows: max(1, rows)}
	for ti := range d.Map.Treatments {
		for pi, part := range d.Parts {
			if part.Treatments[ti].Result == report.ResultBeyondNoise {
				s.Row, s.Col = ti, pi
				return s
			}
		}
	}
	return s
}

// Cell is the selected cell's figures.
func (s State) Cell() report.MapTreatment {
	return s.Data.Parts[s.Col].Treatments[s.Row]
}

// Entry is one row of a cell's case list.
type Entry struct {
	Case *Case
	// "changed", "same", or why there is no comparison ("cut off", "failed",
	// "not paired", ...).
	Status string
	// The first change, "path: from -> to", or "" when nothing changed.
	Change string
}

// Entries is the selected cell's case list, changed cases first, filtered
// to the changed ones unless All.
func (s State) Entries() []Entry {
	part := s.Data.Parts[s.Col]
	mt := s.Cell()
	arm := s.Data.Map.Treatments[s.Row].ID
	changed := map[string]report.MapChange{}
	for _, ch := range mt.Changes {
		changed[ch.CaseID] = ch
	}
	var first, rest []Entry
	for _, c := range s.Data.Cases[part.Name] {
		reply, ok := c.Arms[arm]
		e := Entry{Case: c}
		if ch, isChanged := changed[c.ID]; isChanged {
			e.Status = "changed"
			if ch.Path == report.JudgedPath {
				e.Change = ch.To
			} else {
				e.Change = report.ShortPath(ch.Path) + ": " + ch.From + " -> " + ch.To
			}
			first = append(first, e)
			continue
		}
		switch {
		case !ok:
			e.Status = "no reply"
		case reply.Problem != "" && reply.Problem != "cut off":
			e.Status = reply.Problem
		case reply.Problem == "cut off":
			e.Status = "cut off"
		default:
			if _, rerun := c.Arms["floor_a1"]; !rerun {
				e.Status = "not paired"
			} else {
				e.Status = "same"
			}
		}
		rest = append(rest, e)
	}
	if !s.All && len(first) > 0 {
		return first
	}
	return append(first, rest...)
}

// Reduce is the state after a key.
func Reduce(s State, key Key) State {
	if s.Help {
		if key == "?" || key == "escape" || key == "q" || key == "enter" {
			s.Help = false
		}
		if key == "ctrl-c" {
			s.Quit = true
		}
		return s
	}
	switch key {
	case "ctrl-c":
		s.Quit = true
		return s
	case "?":
		s.Help = true
		return s
	case "o":
		if s.Data.HTMLPath != "" {
			s.OpenPage = true
		}
		return s
	}
	switch s.View {
	case ViewGrid:
		return reduceGrid(s, key)
	case ViewCell:
		return reduceCell(s, key)
	default:
		return reduceCase(s, key)
	}
}

func reduceGrid(s State, key Key) State {
	rows, cols := len(s.Data.Map.Treatments), len(s.Data.Parts)
	switch key {
	case "q", "escape":
		s.Quit = true
	case "up", "k":
		s.Row = max(0, s.Row-1)
	case "down", "j":
		s.Row = min(rows-1, s.Row+1)
	case "left", "h", "backtab":
		s.Col = max(0, s.Col-1)
	case "right", "l", "tab":
		s.Col = min(cols-1, s.Col+1)
	case "enter", " ":
		s.View, s.Cursor, s.All, s.Scroll = ViewCell, 0, false, 0
	}
	return s
}

func reduceCell(s State, key Key) State {
	n := len(s.Entries())
	page := max(1, s.Rows/2)
	switch key {
	case "q":
		s.Quit = true
	case "escape", "backspace", "left", "h":
		s.View = ViewGrid
	case "up", "k":
		s.Cursor = max(0, s.Cursor-1)
	case "down", "j":
		s.Cursor = min(max(0, n-1), s.Cursor+1)
	case "pageup":
		s.Cursor = max(0, s.Cursor-page)
	case "pagedown":
		s.Cursor = min(max(0, n-1), s.Cursor+page)
	case "home":
		s.Cursor = 0
	case "end":
		s.Cursor = max(0, n-1)
	case "a", "tab":
		s.All, s.Cursor = !s.All, 0
	case "enter", "right", "l", " ":
		if n > 0 {
			s.View, s.Scroll = ViewCase, 0
		}
	}
	return s
}

func reduceCase(s State, key Key) State {
	n := len(s.Entries())
	page := max(1, s.Rows-4)
	switch key {
	case "q":
		s.Quit = true
	case "escape", "backspace", "h":
		s.View = ViewCell
	case "up", "k":
		s.Scroll = max(0, s.Scroll-1)
	case "down", "j":
		s.Scroll++
	case "pageup":
		s.Scroll = max(0, s.Scroll-page)
	case "pagedown", " ":
		s.Scroll += page
	case "home":
		s.Scroll = 0
	case "end":
		s.Scroll = 1 << 20
	case "left", "p":
		if s.Cursor > 0 {
			s.Cursor, s.Scroll = s.Cursor-1, 0
		}
	case "right", "n":
		if s.Cursor < n-1 {
			s.Cursor, s.Scroll = s.Cursor+1, 0
		}
	}
	return s
}

// Resize is the state at a new terminal size.
func Resize(s State, columns, rows int) State {
	s.Columns, s.Rows = max(1, columns), max(1, rows)
	return s
}
