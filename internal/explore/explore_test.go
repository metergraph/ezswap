package explore

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/metergraph/replay/demo"
	"github.com/metergraph/replay/internal/report/term"
	"github.com/metergraph/replay/internal/types"
)

func demoRun(t *testing.T) *types.RunFile {
	t.Helper()
	run, err := demo.Run()
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func demoData(t *testing.T) *Data {
	t.Helper()
	d, err := NewData(demoRun(t), Options{Name: "replay-out/run.json"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func press(s State, keys ...string) State {
	for _, k := range keys {
		s = Reduce(s, k)
	}
	return s
}

func repeat(key string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = key
	}
	return out
}

func TestTabsSwitchAndWrap(t *testing.T) {
	d := demoData(t)
	s := NewState(d, 80, 24, 0)
	s = Reduce(s, "right")
	if s.Tab != 1 {
		t.Fatalf("right: tab %d", s.Tab)
	}
	s = press(s, "left", "left")
	if s.Tab != len(Tabs)-1 {
		t.Fatalf("left from the first tab wraps to the last: %d", s.Tab)
	}
	for i, key := range []string{"1", "2", "3", "4", "5"} {
		if got := Reduce(s, key).Tab; got != i {
			t.Fatalf("%s: tab %d", key, got)
		}
	}
	if Reduce(NewState(d, 80, 24, 0), "tab").Tab != 1 {
		t.Fatal("tab moves right")
	}
}

func TestScrollingIsClamped(t *testing.T) {
	d := demoData(t)
	s := NewState(d, 80, 24, 1) // Fields: longer than a screen
	s = press(s, "down", "j", "down")
	if s.Scroll[1] != 3 {
		t.Fatalf("scroll %d", s.Scroll[1])
	}
	s = press(s, "up", "k")
	if s.Scroll[1] != 1 {
		t.Fatalf("scroll %d", s.Scroll[1])
	}
	end := Reduce(s, "end")
	most := end.Scroll[1]
	if most < 10 {
		t.Fatalf("the fields tab is longer than a screen: %d", most)
	}
	if Reduce(end, "down").Scroll[1] != most || Reduce(end, "G").Scroll[1] != most {
		t.Fatal("never past the end")
	}
	if Reduce(end, "home").Scroll[1] != 0 || Reduce(end, "g").Scroll[1] != 0 {
		t.Fatal("home goes to the top")
	}
	if Reduce(NewState(d, 80, 24, 1), "up").Scroll[1] != 0 {
		t.Fatal("never above the top")
	}
	if got := Reduce(NewState(d, 80, 24, 1), " ").Scroll[1]; got != min(most, BodyHeight(24)) {
		t.Fatalf("space pages down: %d", got)
	}
	// The Summary tab fits a tall terminal: nothing to scroll.
	if Reduce(NewState(d, 120, 80, 0), "down").Scroll[0] != 0 {
		t.Fatal("a tab that fits does not scroll")
	}
}

func TestCallList(t *testing.T) {
	d := demoData(t)
	s := NewState(d, 80, 24, CallsTab)
	s = press(s, "down", "j")
	if s.Cursor != 2 {
		t.Fatalf("cursor %d", s.Cursor)
	}
	s = Reduce(s, "up")
	if s.Cursor != 1 {
		t.Fatalf("cursor %d", s.Cursor)
	}
	s = Reduce(s, "end")
	if s.Cursor != len(d.Clean.Cases)-1 {
		t.Fatalf("end: %d", s.Cursor)
	}
	if Reduce(s, "down").Cursor != s.Cursor {
		t.Fatal("clamped at the last call")
	}
	s = Reduce(s, "home")
	if s.Cursor != 0 {
		t.Fatal("home")
	}
	s = Reduce(s, "pagedown")
	if s.Cursor <= 10 {
		t.Fatalf("pagedown: %d", s.Cursor)
	}
	s = Reduce(s, "pageup")
	if s.Cursor != 0 {
		t.Fatal("pageup")
	}
	s = press(s, "down", "enter")
	if s.Detail == nil || *s.Detail != (Detail{Index: 1}) {
		t.Fatalf("enter opens the call: %+v", s.Detail)
	}
	s = press(s, "down", "down")
	if s.Detail.Scroll != 2 {
		t.Fatalf("arrows scroll the open call: %d", s.Detail.Scroll)
	}
	s = Reduce(s, "n")
	if *s.Detail != (Detail{Index: 2}) || s.Cursor != 2 {
		t.Fatalf("n: %+v %d", s.Detail, s.Cursor)
	}
	s = Reduce(s, "p")
	if s.Detail.Index != 1 {
		t.Fatal("p")
	}
	if Reduce(s, "escape").Detail != nil || Reduce(s, "backspace").Detail != nil {
		t.Fatal("esc and backspace close the call")
	}
	if Reduce(s, "right").Detail != nil {
		t.Fatal("switching tabs closes the call")
	}
	long := press(NewState(d, 80, 10, CallsTab), repeat("down", 15)...)
	if long.Scroll[CallsTab] <= 0 {
		t.Fatal("the list follows the cursor")
	}
}

func TestDifferAndFilter(t *testing.T) {
	d := demoData(t)
	s := NewState(d, 80, 24, CallsTab)
	all := len(VisibleCalls(s))
	s = press(s, "end", "d")
	if !s.OnlyDiffer || s.Cursor != 0 {
		t.Fatal("d shows only calls that differ")
	}
	differ := VisibleCalls(s)
	if len(differ) == 0 || len(differ) >= all {
		t.Fatalf("differ %d of %d", len(differ), all)
	}
	for _, i := range differ {
		if !d.Clean.Cases[i].Differs {
			t.Fatal("a listed call does not differ")
		}
	}
	s = Reduce(s, "d")
	if len(VisibleCalls(s)) != all {
		t.Fatal("d again shows all")
	}

	s = Reduce(s, "/")
	if !s.Typing {
		t.Fatal("/ starts the filter")
	}
	s = press(s, "D", "N", "S")
	if s.Filter != "DNS" {
		t.Fatalf("filter %q", s.Filter)
	}
	if Reduce(s, "q").Quit {
		t.Fatal("q types while filtering")
	}
	matches := VisibleCalls(s)
	if len(matches) < 1 || len(matches) >= all {
		t.Fatalf("matches %d", len(matches))
	}
	s = Reduce(s, "backspace")
	if s.Filter != "DN" {
		t.Fatal("backspace")
	}
	s = press(s, "S", "enter")
	if s.Typing || s.Filter != "DNS" {
		t.Fatal("enter keeps the filter")
	}
	s = Reduce(s, "escape")
	if s.Filter != "" {
		t.Fatal("esc clears")
	}
	s = press(s, "/", "x", "escape")
	if s.Typing || s.Filter != "" {
		t.Fatal("esc while typing clears the filter")
	}
	jumped := Reduce(NewState(d, 80, 24, 0), "d")
	if jumped.Tab != CallsTab || !jumped.OnlyDiffer {
		t.Fatal("d jumps to the call list")
	}
	if !Reduce(NewState(d, 80, 24, 1), "/").Typing {
		t.Fatal("/ jumps to the call list")
	}
}

func TestHelpAndQuit(t *testing.T) {
	d := demoData(t)
	s := Reduce(NewState(d, 80, 24, 0), "?")
	if !s.Help {
		t.Fatal("? opens help")
	}
	closed := Reduce(s, "down")
	if closed.Help || closed.Scroll[0] != 0 {
		t.Fatal("any key closes help and does nothing else")
	}
	if !Reduce(s, "q").Quit || !Reduce(NewState(d, 80, 24, 0), "q").Quit || !Reduce(NewState(d, 80, 24, 0), "ctrl-c").Quit {
		t.Fatal("q and Ctrl-C quit")
	}
	if !press(NewState(d, 80, 24, CallsTab), "/", "ctrl-c").Quit {
		t.Fatal("Ctrl-C quits even while typing")
	}
	idle := NewState(d, 80, 24, 0)
	if fmt.Sprint(Reduce(idle, "z")) != fmt.Sprint(idle) {
		t.Fatal("an unbound key changes nothing")
	}
}

func TestResizeKeepsPositionsValid(t *testing.T) {
	d := demoData(t)
	s := press(NewState(d, 120, 40, 1), "end")
	s = Resize(s, 80, 24)
	if s.Columns != 80 {
		t.Fatal("columns")
	}
	if s.Scroll[1] > Reduce(s, "end").Scroll[1] {
		t.Fatal("scroll past the end after a resize")
	}
	list := Resize(press(NewState(d, 80, 40, CallsTab), "end"), 80, 8)
	top := list.Scroll[CallsTab]
	if list.Cursor < top || list.Cursor >= top+ListHeight(8) {
		t.Fatal("the cursor stays on screen")
	}
}

func frame(t *testing.T, s State, columns, rows int, paint term.Paint, ascii bool) []string {
	t.Helper()
	lines := ScreenLines(s, Screen{Columns: columns, Rows: rows, Paint: paint, ASCII: ascii})
	if len(lines) != rows {
		t.Fatalf("%d lines for %d rows", len(lines), rows)
	}
	for _, l := range lines {
		if term.Width(l) != columns {
			t.Fatalf("not %d wide (%d): %q", columns, term.Width(l), l)
		}
	}
	return lines
}

var colour = term.NewPaint(colorprofile.ANSI256)

func TestEveryTabAndTheDetailRender(t *testing.T) {
	d := demoData(t)
	plain := term.PlainPaint()
	differing := -1
	for i, c := range d.Clean.Cases {
		if c.Differs {
			differing = i
			break
		}
	}
	for _, size := range [][2]int{{80, 24}, {120, 40}} {
		columns, rows := size[0], size[1]
		t.Run(fmt.Sprintf("%dx%d", columns, rows), func(t *testing.T) {
			expect := []*regexp.Regexp{
				regexp.MustCompile(`COST`),
				regexp.MustCompile(`Tracked fields`),
				regexp.MustCompile(`Noise floor`),
				regexp.MustCompile(`ticket-0001`),
				regexp.MustCompile(`Content cut short`),
			}
			for tab := range Tabs {
				lines := frame(t, NewState(d, columns, rows, tab), columns, rows, plain, false)
				text := strings.Join(lines, "\n")
				if !strings.Contains(lines[0], "replay") {
					t.Fatalf("tab bar: %q", lines[0])
				}
				if !expect[tab].MatchString(text) {
					t.Fatalf("%s:\n%s", Tabs[tab], text)
				}
				last := lines[len(lines)-1]
				if !strings.Contains(last, "replay-out/run.json") || !strings.Contains(last, Tabs[tab]) {
					t.Fatalf("status bar: %q", last)
				}
				if strings.Contains(text, "\x1b") {
					t.Fatal("no escapes without colour")
				}
			}
			open := press(NewState(d, columns, rows, CallsTab), append(repeat("down", differing), "enter")...)
			lines := frame(t, open, columns, rows, plain, false)
			text := strings.Join(lines, "\n")
			if !strings.Contains(text, "Input") || !strings.Contains(text, "system") {
				t.Fatalf("detail:\n%s", text)
			}
			if !regexp.MustCompile(`Calls › ticket-`).MatchString(lines[len(lines)-1]) {
				t.Fatalf("status bar: %q", lines[len(lines)-1])
			}
			scrolled := strings.Join(frame(t, Reduce(open, "pagedown"), columns, rows, plain, false), "\n") +
				strings.Join(frame(t, press(open, "pagedown", "pagedown"), columns, rows, plain, false), "\n")
			if !strings.Contains(scrolled, "production (gpt-4.1-mini)") {
				t.Fatalf("outputs:\n%s", scrolled)
			}
			if !strings.Contains(scrolled, "▌ ") {
				t.Fatal("a changed field is marked")
			}
			help := strings.Join(frame(t, Reduce(NewState(d, columns, rows, 0), "?"), columns, rows, plain, false), "\n")
			if !strings.Contains(help, "Keys") || !strings.Contains(help, "Any key closes this.") {
				t.Fatalf("help:\n%s", help)
			}
			// Colour and ASCII keep every line the same width.
			frame(t, open, columns, rows, colour, false)
			ascii := strings.Join(frame(t, open, columns, rows, plain, true), "\n")
			if !regexp.MustCompile(`^[\x20-\x7e\n]*$`).MatchString(ascii) {
				t.Fatal("ASCII frame has other characters")
			}
		})
	}
}

func TestHelpShowsTheNotes(t *testing.T) {
	d := demoData(t)
	tall := strings.Join(frame(t, Reduce(NewState(d, 120, 80, 0), "?"), 120, 80, term.PlainPaint(), false), "\n")
	if !strings.Contains(tall, "replayed twice (A1 and A2)") {
		t.Fatalf("the notes are in the help:\n%s", tall)
	}
	short := strings.Join(frame(t, Reduce(NewState(d, 80, 24, 0), "?"), 80, 24, term.PlainPaint(), false), "\n")
	if !strings.Contains(short, "--explain") {
		t.Fatalf("a short screen points at --explain:\n%s", short)
	}
}

func TestSideBySideWhenWide(t *testing.T) {
	d := demoData(t)
	differing := 0
	for i, c := range d.Clean.Cases {
		if c.Differs {
			differing = i
			break
		}
	}
	open := func(columns int) State {
		return press(NewState(d, columns, 200, CallsTab), append(repeat("down", differing), "enter")...)
	}
	find := func(lines []string, s string) string {
		for _, l := range lines {
			if strings.Contains(l, s) {
				return l
			}
		}
		return ""
	}
	wide := find(frame(t, open(140), 140, 200, term.PlainPaint(), false), "production (gpt-4.1-mini)")
	if !strings.Contains(wide, "gemini-2.5-flash-lite") {
		t.Fatalf("two panes share a row at 140 columns: %q", wide)
	}
	narrow := frame(t, open(60), 60, 200, term.PlainPaint(), false)
	if strings.Contains(find(narrow, "production (gpt-4.1-mini)"), "gemini") {
		t.Fatal("one pane per row at 60 columns")
	}
	if find(narrow, "gemini-2.5-flash-lite") == "" {
		t.Fatal("the treatment pane is drawn")
	}
}

func TestHostileTextIsNeutralized(t *testing.T) {
	run := demoRun(t)
	c := &run.Cases[0]
	c.Messages = []types.Message{{Role: "user", Content: "hi \x1b[2J\x1b]0;owned\x07 there"}}
	for i := range run.Replays {
		r := &run.Replays[i]
		if r.CaseID == c.ID && r.Arm == "t1" {
			out := "plain text \x1b[31mred\x1b[0m \u009b2J"
			r.Output = &out
		}
	}
	d, err := NewData(run, Options{Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	s := Reduce(NewState(d, 120, 60, CallsTab), "enter")
	escape := regexp.MustCompile("\x1b[\\s\\S]?[^m]*m?")
	for _, p := range []term.Paint{term.PlainPaint(), colour} {
		text := strings.Join(ScreenLines(s, Screen{Columns: 120, Rows: 60, Paint: p}), "\n")
		for _, e := range escape.FindAllString(text, -1) {
			if !regexp.MustCompile(`^\x1b\[[0-9;]*m$`).MatchString(e) {
				t.Fatalf("an escape that is not a style: %q", e)
			}
		}
		if strings.ContainsAny(text, "\x07\u009b") {
			t.Fatal("BEL or CSI survived")
		}
		if !p.Color && !strings.Contains(text, "^[[2J") {
			t.Fatal("the escape is shown in caret notation")
		}
	}
}

// ---------------------------------------------------------------------------
// The Bubble Tea model

func key(code rune, text string, mod ...tea.KeyMod) tea.KeyPressMsg {
	k := tea.Key{Code: code, Text: text}
	for _, m := range mod {
		k.Mod |= m
	}
	return tea.KeyPressMsg(k)
}

func update(t *testing.T, m Model, msgs ...tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		var next tea.Model
		next, cmd = m.Update(msg)
		m = next.(Model)
	}
	return m, cmd
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestKeyNames(t *testing.T) {
	cases := []struct {
		msg  tea.KeyPressMsg
		want []string
	}{
		{key(tea.KeyUp, ""), []string{"up"}},
		{key(tea.KeyPgDown, ""), []string{"pagedown"}},
		{key(tea.KeyHome, ""), []string{"home"}},
		{key(tea.KeyEnter, ""), []string{"enter"}},
		{key(tea.KeyEscape, ""), []string{"escape"}},
		{key(tea.KeyBackspace, ""), []string{"backspace"}},
		{key(tea.KeyTab, ""), []string{"tab"}},
		{key(tea.KeyTab, "", tea.ModShift), []string{"left"}},
		{key(tea.KeySpace, " "), []string{" "}},
		{key('c', "", tea.ModCtrl), []string{"ctrl-c"}},
		{key('d', "", tea.ModCtrl), []string{"q"}},
		{key('q', "q"), []string{"q"}},
		{key('G', "G"), []string{"G"}},
		{key('é', "é"), []string{"é"}},
		{key(tea.KeyF5, ""), nil},
	}
	for _, c := range cases {
		if got := KeyNames(c.msg); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%v: %q, want %q", c.msg, got, c.want)
		}
	}
}

func TestModelDrivesTheExplorer(t *testing.T) {
	d := demoData(t)
	m := NewModel(d, term.PlainPaint(), 80, 24, 0)
	if !m.View().AltScreen {
		t.Fatal("the explorer uses the alternate screen")
	}
	if !strings.Contains(m.View().Content, "COST") {
		t.Fatalf("opens on the summary:\n%s", m.View().Content)
	}
	m, cmd := update(t, m, key(tea.KeyRight, ""))
	if cmd != nil || m.State.Tab != 1 || !strings.Contains(m.View().Content, "Tracked fields") {
		t.Fatal("right shows the fields")
	}
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if lines := strings.Split(m.View().Content, "\n"); len(lines) != 40 || term.Width(lines[0]) != 120 {
		t.Fatalf("a resize redraws at the new size: %d lines", len(lines))
	}
	m, _ = update(t, m, key('4', "4"), key(tea.KeyEnter, ""))
	if m.State.Detail == nil || !strings.Contains(m.View().Content, "Calls › ticket-") {
		t.Fatal("4 then enter opens a call")
	}
	m, cmd = update(t, m, key('q', "q"))
	if !isQuit(cmd) || m.Reason != ReasonQuit {
		t.Fatal("q quits")
	}
	m = NewModel(d, term.PlainPaint(), 80, 24, CallsTab)
	m, cmd = update(t, m, key('/', "/"), key('c', "", tea.ModCtrl))
	if !isQuit(cmd) {
		t.Fatal("Ctrl-C quits while typing")
	}
	m, cmd = update(t, NewModel(d, term.PlainPaint(), 80, 24, 0), SignalMsg{Name: ReasonSIGTERM})
	if !isQuit(cmd) || m.Reason != ReasonSIGTERM {
		t.Fatal("a signal ends it with its name")
	}
	typed, _ := update(t, NewModel(d, term.PlainPaint(), 80, 24, CallsTab), key('/', "/"), tea.PasteMsg{Content: "DNS"})
	if typed.State.Filter != "DNS" {
		t.Fatalf("a paste types into the filter: %q", typed.State.Filter)
	}
}

func TestColourAndNoColour(t *testing.T) {
	d := demoData(t)
	coloured := NewModel(d, colour, 100, 30, 0).View().Content
	if !strings.Contains(coloured, "\x1b[") {
		t.Fatal("colour on")
	}
	plain := NewModel(d, term.PlainPaint(), 100, 30, 0).View().Content
	if strings.Contains(plain, "\x1b") {
		t.Fatal("colour off writes no escape")
	}
	if term.StripAnsi(coloured) == "" || len(strings.Split(coloured, "\n")) != 30 {
		t.Fatal("a full frame")
	}
}
