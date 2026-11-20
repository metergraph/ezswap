package flow

import (
	"strings"
	"testing"

	"github.com/metergraph/ezswap/internal/report/term"
)

func samplePlan() Plan {
	parts := []PartRow{
		{Name: "route-ticket", Source: "name", Cases: 48, Compared: "2 fields"},
		{Name: "draft-reply", Source: "name", Cases: 48, Compared: "free text", FreeText: true},
	}
	p := NewPlan("ezswap-out/traces.jsonl", "step 2 of 3", parts, Choice{Models: []string{"anthropic/claude-haiku-5.5", "openai/gpt-5.4-nano"}, Endpoint: "vercel", PerPart: 40})
	p.Width = 90
	p.Preview = Preview{Calls: 400, ExpectedUSD: 0.44, UpperUSD: 0.92, BudgetUSD: 5}
	return p
}

func keys(p Plan, ks ...string) (Plan, bool) {
	changed := false
	for _, k := range ks {
		var c bool
		p, c = ReducePlan(p, k)
		changed = changed || c
	}
	return p, changed
}

func TestPlanStartsOnTheModels(t *testing.T) {
	p := samplePlan()
	if r := p.rows()[p.Cursor]; r.kind != rowModel {
		t.Fatalf("cursor on %d", r.kind)
	}
	if len(p.Choice.Parts) != 2 || !p.Ready() {
		t.Fatalf("%+v ready %v", p.Choice, p.Ready())
	}
	frame := strings.Join(PlanFrame(p, term.PlainPaint(), false), "\n")
	for _, want := range []string{"step 2 of 3", "Analyze", "PARTS", "route-ticket", "48 cases, 2 fields", "MODELS TO TRY", "anthropic/claude-haiku-5.5", "Endpoint", "vercel", "Cases per part", "40", "Judge free text", "Run the map", "400 calls. Expected $0.44, at most $0.92. Budget $5.00."} {
		if !strings.Contains(frame, want) {
			t.Errorf("frame lacks %q:\n%s", want, frame)
		}
	}
}

func TestPlanChoices(t *testing.T) {
	p := samplePlan()
	// Untick the first model.
	p, changed := keys(p, " ")
	if !changed || len(p.Choice.Models) != 1 || p.Choice.Models[0] != "openai/gpt-5.4-nano" {
		t.Fatalf("models %v changed %v", p.Choice.Models, changed)
	}
	// Untick both: not ready.
	p, _ = keys(p, "down", " ")
	if p.Ready() {
		t.Error("ready with no model")
	}
	// Add a model by name.
	p, changed = keys(p, "down", "enter", "o", "p", "e", "n", "a", "i", "/", "x", "backspace", "m", "enter")
	if !changed || p.Typing || p.AllModels[2] != "openai/m" || len(p.Choice.Models) != 1 {
		t.Fatalf("added %v chosen %v typing %v", p.AllModels, p.Choice.Models, p.Typing)
	}
	// Endpoint cycles; cases per part stay between 10 and 200.
	p, _ = keys(p, "down", "right")
	if p.Choice.Endpoint != "openai" {
		t.Errorf("endpoint %s", p.Choice.Endpoint)
	}
	p, _ = keys(p, "left", "left")
	if p.Choice.Endpoint != "openrouter" {
		t.Errorf("endpoint %s", p.Choice.Endpoint)
	}
	p, _ = keys(p, "down")
	for i := 0; i < 30; i++ {
		p, _ = keys(p, "right")
	}
	if p.Choice.PerPart != 200 {
		t.Errorf("per part %d", p.Choice.PerPart)
	}
	// The judge row exists while a free-text part is chosen.
	p, _ = keys(p, "down", " ")
	if !p.Choice.Judge {
		t.Error("judge not toggled")
	}
	p, _ = keys(p, "home", "down", " ")
	if len(p.Choice.Parts) != 1 {
		t.Fatalf("parts %v", p.Choice.Parts)
	}
	for _, r := range p.rows() {
		if r.kind == rowJudge {
			t.Error("judge row without a free-text part")
		}
	}
	// r runs from anywhere once ready; q cancels.
	if done, _ := keys(p, "r"); !done.Done {
		t.Error("r did not run")
	}
	p.Preview.Err = "Unknown model."
	if done, _ := keys(p, "r"); done.Done {
		t.Error("ran with an error in the estimate")
	}
	if c, _ := keys(p, "q"); !c.Cancel {
		t.Error("q did not cancel")
	}
}

func TestPlanFrameFitsAndCleans(t *testing.T) {
	p := samplePlan()
	p.AllModels = append(p.AllModels, "evil\x1b]0;pwned\x07/model")
	p.Preview.Unpriced = []string{"evil\x1b]0;pwned\x07/model"}
	for _, ascii := range []bool{false, true} {
		for _, l := range PlanFrame(p, term.PlainPaint(), ascii) {
			if term.Width(l) > 90 {
				t.Errorf("line wider than 90: %q", l)
			}
			if strings.Contains(l, "\x1b]") || strings.Contains(l, "\x07") {
				t.Fatal("an escape reached the frame")
			}
			if ascii {
				for _, r := range l {
					if r > 0x7e {
						t.Fatalf("ASCII frame holds %q", l)
					}
				}
			}
		}
	}
}

func TestMenu(t *testing.T) {
	m := NewMenu("step 1 of 3", "Start", []string{"What would you like to do?"}, []Item{
		{Title: "Open a finished map", Disabled: "(no run.json here)"},
		{Title: "Record my app", Detail: "Start a local proxy."},
		{Title: "Map a trace file", Prompt: "Trace file:", Value: "ezswap-out/traces.jsonl"},
	})
	m.Width = 90
	if m.Cursor != 1 {
		t.Fatalf("cursor %d", m.Cursor)
	}
	if up := ReduceMenu(m, "up"); up.Cursor != 1 {
		t.Error("moved onto a disabled item")
	}
	m = ReduceMenu(m, "down")
	m = ReduceMenu(m, "enter")
	if !m.Editing || m.Value != "ezswap-out/traces.jsonl" || m.Chosen != -1 {
		t.Fatalf("%+v", m)
	}
	for _, k := range []string{"backspace", "backspace", "backspace", "backspace", "backspace", "x", "t"} {
		m = ReduceMenu(m, k)
	}
	m = ReduceMenu(m, "enter")
	if m.Chosen != 2 || m.Value != "ezswap-out/traces.xt" {
		t.Fatalf("chosen %d value %q", m.Chosen, m.Value)
	}
	n := NewMenu("", "Start", nil, m.Items)
	if n = ReduceMenu(n, "2"); n.Chosen != 1 {
		t.Errorf("a number did not choose: %d", n.Chosen)
	}
	if n = ReduceMenu(NewMenu("", "x", nil, m.Items), "1"); n.Chosen != -1 {
		t.Error("a number chose a disabled item")
	}
	m.Width = 90
	frame := strings.Join(MenuFrame(m, term.PlainPaint(), true), "\n")
	for _, want := range []string{"step 1 of 3", "Start", "1  Open a finished map  (no run.json here)", "> 3  Map a trace file"} {
		if !strings.Contains(frame, want) {
			t.Errorf("menu lacks %q:\n%s", want, frame)
		}
	}
}
