package mapview

import (
	"os"
	"strings"
	"testing"

	"github.com/metergraph/ezswap/demo"
	"github.com/metergraph/ezswap/internal/report"
	"github.com/metergraph/ezswap/internal/report/term"
	"github.com/metergraph/ezswap/internal/types"
)

func demoData(t *testing.T) *Data {
	t.Helper()
	run, err := demo.MapRun()
	if err != nil {
		t.Fatal(err)
	}
	m, err := report.BuildMap(run, report.MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return NewData(run, m, Options{Name: "run.json", HTMLPath: "map.html"})
}

func frame(s State) string {
	return Render(s, Screen{Columns: 100, Rows: 30, Paint: term.PlainPaint()})
}

func press(s State, keys ...string) State {
	for _, k := range keys {
		s = Reduce(s, k)
	}
	return s
}

func TestPrintFrames(t *testing.T) {
	if os.Getenv("MAPVIEW_PRINT") == "" {
		t.Skip()
	}
	s := NewState(demoData(t), 100, 30)
	t.Log("\n" + frame(s))
	s = press(s, "enter")
	t.Log("\n" + frame(s))
	s = press(s, "enter")
	t.Log("\n" + frame(s))
}

func TestGridOpensOnTheFirstBeyondNoiseCell(t *testing.T) {
	s := NewState(demoData(t), 100, 30)
	if s.Cell().Result != report.ResultBeyondNoise || s.Data.Parts[s.Col].Name != "extract-order" {
		t.Fatalf("selected %s %s", s.Data.Parts[s.Col].Name, s.Cell().Result)
	}
	text := frame(s)
	for _, want := range []string{"ezswap map", "Each model on each part", "extract-order", "beyond 9/20", "A openai/gpt-5.4-nano on extract-order: beyond noise, 9 of 20 changed against 1"} {
		if !strings.Contains(text, want) {
			t.Errorf("grid lacks %q:\n%s", want, text)
		}
	}
	lines := strings.Split(text, "\n")
	if len(lines) != 30 {
		t.Fatalf("%d lines", len(lines))
	}
	for i, l := range lines {
		if term.Width(l) > 100 {
			t.Errorf("line %d is %d wide", i, term.Width(l))
		}
	}
}

func TestCellAndCaseViews(t *testing.T) {
	s := press(NewState(demoData(t), 100, 30), "enter")
	if s.View != ViewCell || len(s.Entries()) != 9 {
		t.Fatalf("view %d, %d entries", s.View, len(s.Entries()))
	}
	text := frame(s)
	for _, want := range []string{"extract-order", "changed 9 of 20", "production rerun 1 of 20", "CHANGED CASES (9)", "quantity: 5 -> 6"} {
		if !strings.Contains(text, want) {
			t.Errorf("cell lacks %q:\n%s", want, text)
		}
	}
	all := press(s, "a")
	if len(all.Entries()) != 20 || !strings.Contains(frame(all), "ALL CASES (20)") {
		t.Errorf("all cases: %d", len(all.Entries()))
	}
	c := press(s, "enter")
	if c.View != ViewCase {
		t.Fatal("enter did not open the case")
	}
	text = frame(c)
	for _, want := range []string{"case 1 of 9", "REQUEST", "recorded", "A openai/gpt-5.4-nano"} {
		if !strings.Contains(text, want) {
			t.Errorf("case lacks %q:\n%s", want, text)
		}
	}
	// Stacked at 100 columns: the rerun is further down.
	if bottom := frame(press(c, "end")); !strings.Contains(bottom, "production rerun") {
		t.Errorf("the rerun is not below:\n%s", bottom)
	}
	// Side by side on a wide terminal: all three replies on one screen.
	wide := Render(c, Screen{Columns: 140, Rows: 40, Paint: term.PlainPaint()})
	for _, want := range []string{"recorded", "A openai/gpt-5.4-nano", "production rerun", `"quantity": 6`} {
		if !strings.Contains(wide, want) {
			t.Errorf("side by side lacks %q:\n%s", want, wide)
		}
	}
	next := press(c, "right")
	if next.Cursor != 1 || !strings.Contains(frame(next), "case 2 of 9") {
		t.Errorf("right did not step to the next case")
	}
	back := press(c, "escape", "escape")
	if back.View != ViewGrid {
		t.Errorf("escape twice did not return to the grid: %d", back.View)
	}
	if q := press(back, "q"); !q.Quit {
		t.Error("q did not quit")
	}
}

func TestFreeTextCellAndNarrowScreen(t *testing.T) {
	d := demoData(t)
	s := NewState(d, 60, 20)
	for s.Data.Parts[s.Col].Name != "draft-reply" {
		s = press(s, "right")
	}
	text := Render(s, Screen{Columns: 60, Rows: 20, Paint: term.PlainPaint(), ASCII: true})
	if !strings.Contains(text, "draft-reply") || !strings.Contains(text, "not comparable") {
		t.Errorf("free text cell:\n%s", text)
	}
	for i, l := range strings.Split(text, "\n") {
		if term.Width(l) > 60 {
			t.Errorf("line %d is %d wide: %q", i, term.Width(l), l)
		}
		for _, r := range l {
			if r > 0x7e {
				t.Fatalf("ASCII frame holds %q", l)
			}
		}
	}
	// A cell with nothing changed opens on all its cases.
	c := press(s, "enter")
	if len(c.Entries()) == 0 {
		t.Error("an unchanged cell lists no cases")
	}
}

func TestHelpAndOpenPage(t *testing.T) {
	s := press(NewState(demoData(t), 100, 30), "?")
	if !s.Help || !strings.Contains(frame(s), "paired sign test") {
		t.Fatal("help did not open")
	}
	s = press(s, "escape", "o")
	if s.Help || !s.OpenPage {
		t.Errorf("help %v, open %v", s.Help, s.OpenPage)
	}
}

func TestRunFileTextCannotDriveTheTerminal(t *testing.T) {
	run, err := demo.MapRun()
	if err != nil {
		t.Fatal(err)
	}
	evil := "\x1b]0;pwned\x07evil"
	for i := range run.Replays {
		r := &run.Replays[i]
		if r.Arm == "t1" {
			r.Status = "failed"
			r.Output = nil
			r.Error = &types.ReplayError{Code: evil, Message: evil}
		}
	}
	for i := range run.Cases {
		run.Cases[i].ID = run.Cases[i].ID + evil
	}
	m, err := report.BuildMap(run, report.MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range m.Parts {
		for j := range m.Parts[i].Treatments {
			mt := &m.Parts[i].Treatments[j]
			reason := evil
			mt.Reason = &reason
			for k := range mt.Paths {
				mt.Paths[k].Path = "$." + evil
			}
		}
	}
	d := NewData(run, m, Options{})
	for _, cases := range d.Cases {
		for _, c := range cases {
			c.Request = evil
		}
	}
	s := NewState(d, 100, 30)
	frames := []string{frame(s), frame(press(s, "enter")), frame(press(s, "enter", "a")), frame(press(s, "enter", "a", "enter"))}
	for i, text := range frames {
		if strings.Contains(text, "\x1b]") || strings.Contains(text, "\x07") {
			t.Fatalf("frame %d: an escape reached the frame", i)
		}
	}
}
