package flow

import (
	"strings"

	"github.com/metergraph/replay/internal/report/term"
)

// Item is one choice in a menu. An item with a Prompt asks for a value
// (a file path, a URL) before it is chosen, starting from Value.
type Item struct {
	Title  string
	Detail string
	Prompt string
	Value  string
	// Not available, and why: shown muted, cannot be chosen.
	Disabled string
}

// Menu is a list of choices on its own screen.
type Menu struct {
	Step  string
	Title string
	Intro []string
	Items []Item
	// The highlighted item, and the chosen one (-1 until chosen).
	Cursor int
	Chosen int
	// Typing the chosen item's value.
	Editing bool
	Value   string
	Cancel  bool
	Width   int
}

// NewMenu is a menu with its first available item highlighted.
func NewMenu(step, title string, intro []string, items []Item) Menu {
	m := Menu{Step: step, Title: title, Intro: intro, Items: items, Chosen: -1}
	for i, it := range items {
		if it.Disabled == "" {
			m.Cursor = i
			break
		}
	}
	return m
}

// ReduceMenu is the menu after a key.
func ReduceMenu(m Menu, key string) Menu {
	if m.Editing {
		switch key {
		case "enter":
			if strings.TrimSpace(m.Value) != "" {
				m.Editing, m.Chosen = false, m.Cursor
			}
		case "escape":
			m.Editing = false
		case "backspace":
			if r := []rune(m.Value); len(r) > 0 {
				m.Value = string(r[:len(r)-1])
			}
		case "ctrl-c":
			m.Cancel = true
		default:
			if len([]rune(key)) == 1 {
				m.Value += key
			}
		}
		return m
	}
	switch key {
	case "ctrl-c", "q", "escape":
		m.Cancel = true
	case "up", "k":
		m.Cursor = m.next(-1)
	case "down", "j", "tab":
		m.Cursor = m.next(1)
	case "enter", " ", "right", "l":
		it := m.Items[m.Cursor]
		if it.Disabled != "" {
			break
		}
		if it.Prompt != "" {
			m.Editing, m.Value = true, it.Value
			break
		}
		m.Chosen, m.Value = m.Cursor, it.Value
	default:
		// 1 to 9 choose an item directly.
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			if i := int(key[0] - '1'); i < len(m.Items) && m.Items[i].Disabled == "" {
				m.Cursor = i
				return ReduceMenu(m, "enter")
			}
		}
	}
	return m
}

// next is the nearest available item in a direction, or the cursor.
func (m Menu) next(step int) int {
	for i := m.Cursor + step; i >= 0 && i < len(m.Items); i += step {
		if m.Items[i].Disabled == "" {
			return i
		}
	}
	return m.Cursor
}

// MenuFrame is the menu as lines, each fitted to the width.
func MenuFrame(m Menu, paint term.Paint, ascii bool) []string {
	w := max(40, m.Width)
	lines := header(m.Step, m.Title, paint, w, ascii)
	for _, l := range m.Intro {
		lines = append(lines, term.Wrap(l, w-2)...)
	}
	if len(m.Intro) > 0 {
		lines = append(lines, "")
	}
	pointer := "›"
	if ascii {
		pointer = ">"
	}
	for i, it := range m.Items {
		mark := "  "
		if i == m.Cursor {
			mark = paint.Bold(pointer) + " "
		}
		number := paint.Muted(string(rune('1'+i)) + "  ")
		title := term.CleanLine(it.Title)
		switch {
		case it.Disabled != "":
			lines = append(lines, mark+number+paint.Muted(title+"  "+term.CleanLine(it.Disabled)))
			continue
		case i == m.Cursor:
			title = paint.Bold(title)
		}
		lines = append(lines, mark+number+title)
		if it.Detail != "" {
			for _, d := range term.Wrap(term.CleanLine(it.Detail), w-8) {
				lines = append(lines, "       "+paint.Muted(d))
			}
		}
		if m.Editing && i == m.Cursor {
			caret := "▏"
			if ascii {
				caret = "_"
			}
			lines = append(lines, "       "+term.CleanLine(it.Prompt)+" "+term.CleanLine(m.Value)+paint.Bold(caret))
		}
	}
	hint := "↑↓ move   enter choose   q quit"
	if m.Editing {
		hint = "type, then enter   esc back"
	}
	if ascii {
		hint = strings.ReplaceAll(hint, "↑↓", "up, down")
	}
	lines = append(lines, "", paint.Muted(hint))
	return fit(lines, w, ascii)
}
