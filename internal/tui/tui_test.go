package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/metergraph/ezswap/internal/report/term"
	"github.com/metergraph/ezswap/internal/run"
	"github.com/metergraph/ezswap/internal/types"
)

func keyPress(code rune, text string, mod ...tea.KeyMod) tea.KeyPressMsg {
	k := tea.Key{Code: code, Text: text}
	for _, m := range mod {
		k.Mod |= m
	}
	return tea.KeyPressMsg(k)
}

func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestConfirm(t *testing.T) {
	q := "Spend up to $0.10 (about $0.05 expected)? [y/N] "
	ask := Confirm{Question: q, Paint: term.PlainPaint()}
	if v := ask.View().Content; !strings.HasPrefix(v, q) || strings.Contains(v, "\x1b") {
		t.Fatalf("asks: %q", v)
	}
	cases := []struct {
		msg tea.Msg
		yes bool
	}{
		{keyPress('y', "y"), true},
		{keyPress('Y', "Y"), true},
		{keyPress('n', "n"), false},
		{keyPress(tea.KeyEnter, ""), false},
		{keyPress(tea.KeyEscape, ""), false},
		{keyPress('c', "", tea.ModCtrl), false},
		{keyPress('d', "", tea.ModCtrl), false},
		{SignalMsg{Name: "SIGINT"}, false},
	}
	for _, c := range cases {
		next, cmd := ask.Update(c.msg)
		m := next.(Confirm)
		if !m.Answered || m.Yes != c.yes || !quits(cmd) {
			t.Fatalf("%v: answered %v yes %v", c.msg, m.Answered, m.Yes)
		}
		want := "no"
		if c.yes {
			want = "yes"
		}
		if !strings.HasSuffix(m.View().Content, want+"\n") {
			t.Fatalf("shows the answer: %q", m.View().Content)
		}
	}
	next, cmd := ask.Update(keyPress('x', "x"))
	if next.(Confirm).Answered || cmd != nil {
		t.Fatal("another key does not answer")
	}
	// Once answered, nothing changes it.
	yes, _ := ask.Update(keyPress('y', "y"))
	after, _ := yes.Update(keyPress('n', "n"))
	if !after.(Confirm).Yes {
		t.Fatal("the answer stands")
	}
}

func planOf(replayable int, floor bool) *run.RunPlan {
	model := "gpt-4.1-mini"
	plan := &run.RunPlan{
		Treatments: []types.Treatment{
			{ID: "t1", Label: "google/gemini-2.5-flash-lite", Model: "google/gemini-2.5-flash-lite", Endpoint: "openrouter"},
			{ID: "t2", Label: "openai/gpt-5-mini", Model: "openai/gpt-5-mini", Endpoint: "openrouter"},
		},
		Settings: types.RunSettings{BudgetUSD: 1},
		Estimate: types.Estimate{Replayable: replayable},
		Cases:    []types.Case{{ID: "c1", Model: &model}},
	}
	if floor {
		plan.FloorEndpoint = "openrouter"
	}
	return plan
}

func send(m Progress, msgs ...tea.Msg) Progress {
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(Progress)
	}
	return m
}

func finished(arm, status, code string, done, total int, spent float64) EventMsg {
	r := &types.Replay{Arm: arm, Status: status}
	if code != "" {
		r.Error = &types.ReplayError{Code: code, Message: "x"}
	}
	return EventMsg{Event: run.Event{Kind: run.EventCallFinished, Arm: arm, Replay: r, Progress: run.Progress{Done: done, Total: total, SpentUSD: spent, BudgetUSD: 1, InFlight: 2}}}
}

func TestProgressCountsCallsSpendAndFailures(t *testing.T) {
	start := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	now := start
	soft, hard := 0, 0
	m := NewProgress(planOf(3, true), ProgressOptions{
		Paint: term.PlainPaint(), Width: 100, Now: func() time.Time { return now },
		Soft: func() { soft++ }, Hard: func() { hard++ },
	})
	if got := m.Rows(); len(got) != 3 || got[2] != [3]string{"itself (gpt-4.1-mini, twice)", "0", "6"} {
		t.Fatalf("rows: %v", got)
	}
	frame := m.Frame()
	for _, want := range []string{"Replaying", "0 of 12 calls", "Spent $0.00 of $1.00", "A google/gemini-2.5-flash-lite", "0 of 3", "Ctrl-C stops new calls"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("missing %q:\n%s", want, frame)
		}
	}
	now = start.Add(75 * time.Second)
	m = send(m,
		finished("t1", "ok", "", 1, 12, 0.002),
		finished("floor_a1", "ok", "", 2, 12, 0.003),
		finished("t2", "failed", "provider_rate_limited", 3, 12, 0.003),
		tickMsg(now),
	)
	frame = m.Frame()
	for _, want := range []string{"3 of 12 calls", " 25%", "1:15", "Spent $0.0030 of $1.00", "2 calls in flight", "1 failed", "Failures: provider_rate_limited (1).", "1 of 6"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("missing %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "\x1b") {
		t.Fatal("no colour, no escapes")
	}

	// The first Ctrl-C stops new calls; the second cancels the ones in flight.
	m = send(m, keyPress('c', "", tea.ModCtrl))
	if soft != 1 || hard != 0 || !m.Stopping() || !strings.Contains(m.Frame(), "Stopping: finishing the calls in flight") {
		t.Fatalf("first Ctrl-C: soft %d hard %d\n%s", soft, hard, m.Frame())
	}
	next, cmd := m.Update(keyPress('c', "", tea.ModCtrl))
	m = next.(Progress)
	if hard != 1 || !m.Cancelling() || quits(cmd) || !strings.Contains(m.Frame(), "cancelling the calls in flight") {
		t.Fatalf("second Ctrl-C: hard %d", hard)
	}
	m = send(m, keyPress('c', "", tea.ModCtrl))
	if hard != 1 {
		t.Fatal("cancel once")
	}
	next, cmd = m.Update(DoneMsg{})
	if !quits(cmd) || next.(Progress).View().Content != "" {
		t.Fatal("the run's end ends the view and leaves nothing behind")
	}
}

func TestProgressSignals(t *testing.T) {
	soft, hard := 0, 0
	base := NewProgress(planOf(2, false), ProgressOptions{Paint: term.PlainPaint(), Soft: func() { soft++ }, Hard: func() { hard++ }})
	if len(base.Rows()) != 2 {
		t.Fatal("no floor row without a floor")
	}
	m := send(base, SignalMsg{Name: "SIGINT"})
	if soft != 1 || hard != 0 || !m.Stopping() {
		t.Fatal("SIGINT is the first Ctrl-C")
	}
	send(base, SignalMsg{Name: "SIGTERM"})
	if hard != 1 {
		t.Fatal("SIGTERM cancels everything")
	}
	stopped := run.StopBudget
	m = send(base, EventMsg{Event: run.Event{Kind: run.EventStopped, Stopped: &stopped, Progress: run.Progress{Total: 4, BudgetUSD: 1}}})
	if !strings.Contains(m.Frame(), "Stopped early: the budget could not cover the next case") {
		t.Fatalf("a stop is shown:\n%s", m.Frame())
	}
}

func TestProgressASCIIAndColour(t *testing.T) {
	ascii := NewProgress(planOf(2, true), ProgressOptions{Paint: term.PlainPaint(), ASCII: true, Width: 60})
	ascii = send(ascii, finished("t1", "ok", "", 3, 8, 0.01))
	frame := ascii.Frame()
	for _, l := range strings.Split(frame, "\n") {
		if term.Width(l) > 60 {
			t.Fatalf("wider than the terminal: %q", l)
		}
		for _, r := range l {
			if r > 0x7e {
				t.Fatalf("not ASCII: %q", l)
			}
		}
	}
	if !strings.Contains(frame, "#") {
		t.Fatalf("an ASCII bar:\n%s", frame)
	}
	colour := NewProgress(planOf(2, true), ProgressOptions{Paint: term.NewPaint(colorprofile.TrueColor), Width: 80})
	if !strings.Contains(colour.Frame(), "\x1b[") {
		t.Fatal("colour on a colour terminal")
	}
}

func TestRunWithProgressDeliversEveryEvent(t *testing.T) {
	// No terminal: the view renders into a buffer and the run's result comes back.
	var out strings.Builder
	soft, cancelSoft := context.WithCancel(context.Background())
	defer cancelSoft()
	want := &types.RunFile{SpentUSD: 0.5}
	got, err := RunWithProgress(planOf(1, false), RunOptions{Input: strings.NewReader(""), Output: &out, Profile: colorprofile.NoTTY, Width: 80}, cancelSoft, func() {}, func(onEvent func(run.Event)) (*types.RunFile, error) {
		for i := 1; i <= 2; i++ {
			onEvent(run.Event{Kind: run.EventCallFinished, Arm: "t1", Replay: &types.Replay{Status: "ok"}, Progress: run.Progress{Done: i, Total: 2, BudgetUSD: 1}})
		}
		return want, nil
	})
	if err != nil || got != want {
		t.Fatalf("result %v %v", got, err)
	}
	if soft.Err() != nil {
		t.Fatal("nothing interrupted the run")
	}
}
