// Package flow is the guided workflow's own screens: the start menu, the
// recorder's setup and the plan of a map. Each is a pure state and reducer
// with a frame, run as its own Bubble Tea program; the CLI runs them in
// order with the record dashboard, the spend confirmation, the progress
// view and the map explorer.
package flow

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/metergraph/replay/internal/report/term"
)

// Endpoints the plan can send to, in the order the plan cycles through.
var Endpoints = []string{"openrouter", "vercel", "openai", "litellm", "ollama"}

// Choice is what the plan screen sets up: the options of a map.
type Choice struct {
	Models   []string
	Endpoint string
	// The parts to map; all of them when it holds every part.
	Parts   []string
	PerPart int
	Judge   bool
}

// PartRow is one part as the estimate sees it.
type PartRow struct {
	Name     string
	Source   string
	Cases    int
	Compared string
	FreeText bool
	USD      float64
}

// Preview is the estimate of a Choice.
type Preview struct {
	Parts       []PartRow
	Calls       int
	ExpectedUSD float64
	UpperUSD    float64
	BudgetUSD   float64
	// Models the catalog cannot price, keys the endpoint needs and lacks,
	// and an error that stops the plan (a bad model name, say).
	Unpriced []string
	Missing  []string
	Err      string
}

// Row kinds of the plan screen, top to bottom.
const (
	rowPart = iota
	rowModel
	rowAddModel
	rowEndpoint
	rowPerPart
	rowJudge
	rowRun
)

type row struct {
	kind  int
	index int
}

// Plan is the plan screen's state.
type Plan struct {
	File   string
	Step   string
	Choice Choice
	// Every part and model on offer; Choice holds the ones chosen.
	AllParts  []PartRow
	AllModels []string
	Preview   Preview
	// The estimate is being worked out for this revision.
	Pending  bool
	Revision int
	Cursor   int
	// Typing a model name into the add row.
	Typing bool
	Draft  string
	Done   bool
	Cancel bool
	Width  int
	Height int
}

// NewPlan is the plan screen for a trace file, from the parts the file
// holds and the choice to start from.
func NewPlan(file, step string, parts []PartRow, start Choice) Plan {
	p := Plan{File: file, Step: step, AllParts: parts, Choice: start, AllModels: append([]string(nil), start.Models...)}
	if len(p.Choice.Parts) == 0 {
		for _, part := range parts {
			p.Choice.Parts = append(p.Choice.Parts, part.Name)
		}
	}
	if p.Choice.PerPart == 0 {
		p.Choice.PerPart = 20
	}
	if p.Choice.Endpoint == "" {
		p.Choice.Endpoint = Endpoints[0]
	}
	p.Cursor = p.firstRow(rowModel)
	return p
}

func (p Plan) rows() []row {
	var rows []row
	for i := range p.AllParts {
		rows = append(rows, row{rowPart, i})
	}
	for i := range p.AllModels {
		rows = append(rows, row{rowModel, i})
	}
	rows = append(rows, row{rowAddModel, 0}, row{rowEndpoint, 0}, row{rowPerPart, 0})
	if p.hasFreeText() {
		rows = append(rows, row{rowJudge, 0})
	}
	return append(rows, row{rowRun, 0})
}

func (p Plan) firstRow(kind int) int {
	for i, r := range p.rows() {
		if r.kind == kind {
			return i
		}
	}
	return 0
}

func (p Plan) hasFreeText() bool {
	for _, part := range p.AllParts {
		if part.FreeText && slices.Contains(p.Choice.Parts, part.Name) {
			return true
		}
	}
	return false
}

// Ready is whether the plan can run: a model, a part, no error.
func (p Plan) Ready() bool {
	return len(p.Choice.Models) > 0 && len(p.Choice.Parts) > 0 && p.Preview.Err == "" && !p.Pending
}

// Changed reports whether a key changed the choice, so the estimate must be
// worked out again.
func (p Plan) changed(before Choice) bool {
	return !slices.Equal(before.Models, p.Choice.Models) || before.Endpoint != p.Choice.Endpoint ||
		!slices.Equal(before.Parts, p.Choice.Parts) || before.PerPart != p.Choice.PerPart || before.Judge != p.Choice.Judge
}

// ReducePlan is the plan after a key. It reports whether the choice
// changed.
func ReducePlan(p Plan, key string) (Plan, bool) {
	before := Choice{Models: append([]string(nil), p.Choice.Models...), Endpoint: p.Choice.Endpoint,
		Parts: append([]string(nil), p.Choice.Parts...), PerPart: p.Choice.PerPart, Judge: p.Choice.Judge}
	if p.Typing {
		switch key {
		case "enter":
			name := strings.TrimSpace(p.Draft)
			if name != "" && !slices.Contains(p.AllModels, name) {
				p.AllModels = append(p.AllModels, name)
				p.Choice.Models = append(p.Choice.Models, name)
				// A row was added above the cursor: stay on the add row.
				p.Cursor++
			}
			p.Typing, p.Draft = false, ""
		case "escape":
			p.Typing, p.Draft = false, ""
		case "backspace":
			if r := []rune(p.Draft); len(r) > 0 {
				p.Draft = string(r[:len(r)-1])
			}
		case "ctrl-c":
			p.Cancel = true
		default:
			if len([]rune(key)) == 1 && key != " " {
				p.Draft += key
			}
		}
		return p, p.changed(before)
	}
	rows := p.rows()
	p.Cursor = min(p.Cursor, len(rows)-1)
	current := rows[p.Cursor]
	switch key {
	case "ctrl-c", "q", "escape":
		p.Cancel = true
	case "up", "k":
		p.Cursor = max(0, p.Cursor-1)
	case "down", "j", "tab":
		p.Cursor = min(len(rows)-1, p.Cursor+1)
	case "home":
		p.Cursor = 0
	case "end":
		p.Cursor = len(rows) - 1
	case " ", "x", "enter", "left", "right", "h", "l", "-", "+":
		p = p.act(current, key)
	case "r":
		if p.Ready() {
			p.Done = true
		}
	}
	return p, p.changed(before)
}

func (p Plan) act(r row, key string) Plan {
	toggle := key == " " || key == "x" || key == "enter"
	switch r.kind {
	case rowPart:
		if toggle {
			p.Choice.Parts = toggled(p.Choice.Parts, p.AllParts[r.index].Name, partOrder(p.AllParts))
		}
	case rowModel:
		if toggle {
			p.Choice.Models = toggled(p.Choice.Models, p.AllModels[r.index], p.AllModels)
		}
	case rowAddModel:
		if key == "enter" || key == " " {
			p.Typing = true
		}
	case rowEndpoint:
		i := slices.Index(Endpoints, p.Choice.Endpoint)
		switch key {
		case "left", "h", "-":
			i = (i - 1 + len(Endpoints)) % len(Endpoints)
		default:
			i = (i + 1) % len(Endpoints)
		}
		p.Choice.Endpoint = Endpoints[i]
	case rowPerPart:
		switch key {
		case "left", "h", "-":
			p.Choice.PerPart = max(10, p.Choice.PerPart-10)
		case "right", "l", "+", " ", "enter":
			p.Choice.PerPart = min(200, p.Choice.PerPart+10)
		}
	case rowJudge:
		if toggle || key == "left" || key == "right" {
			p.Choice.Judge = !p.Choice.Judge
		}
	case rowRun:
		if key == "enter" && p.Ready() {
			p.Done = true
		}
	}
	return p
}

func partOrder(parts []PartRow) []string {
	out := make([]string, len(parts))
	for i, part := range parts {
		out[i] = part.Name
	}
	return out
}

// toggled adds or removes a name, keeping the order of all.
func toggled(chosen []string, name string, all []string) []string {
	on := map[string]bool{}
	for _, c := range chosen {
		on[c] = true
	}
	on[name] = !on[name]
	var out []string
	for _, n := range all {
		if on[n] {
			out = append(out, n)
		}
	}
	return out
}

// PlanFrame is the plan screen as lines, each fitted to the width.
func PlanFrame(p Plan, paint term.Paint, ascii bool) []string {
	w := max(40, p.Width)
	g := func(fancy, plain string) string {
		if ascii {
			return plain
		}
		return fancy
	}
	box := func(on bool) string {
		if on {
			return g("◉", "[x]")
		}
		return g("○", "[ ]")
	}
	lines := header(p.Step, "Plan the map", paint, w, ascii)
	lines = append(lines, paint.Muted("Trace file ")+term.CleanLine(p.File))
	rows := p.rows()
	cursorAt := func(i int) string {
		if i == p.Cursor {
			return paint.Bold(g("›", ">")) + " "
		}
		return "  "
	}
	section := ""
	for i, r := range rows {
		title := map[int]string{rowPart: "PARTS", rowModel: "MODELS TO TRY", rowAddModel: "MODELS TO TRY", rowEndpoint: "SETTINGS", rowPerPart: "SETTINGS", rowJudge: "SETTINGS", rowRun: ""}[r.kind]
		if title != section {
			if title != "" {
				lines = append(lines, "", paint.Muted(title))
			} else {
				lines = append(lines, "")
			}
			section = title
		}
		var text string
		switch r.kind {
		case rowPart:
			part := p.AllParts[r.index]
			on := slices.Contains(p.Choice.Parts, part.Name)
			detail := fmt.Sprintf("%d cases, %s", part.Cases, part.Compared)
			for _, pr := range p.Preview.Parts {
				if pr.Name == part.Name && on {
					detail += ", " + usd(pr.USD)
				}
			}
			text = box(on) + " " + term.PadEnd(term.Truncate(term.CleanLine(part.Name), 22, g("…", "~")), 22) + "  " + paint.Muted(term.CleanLine(detail))
		case rowModel:
			name := p.AllModels[r.index]
			text = box(slices.Contains(p.Choice.Models, name)) + " " + term.CleanLine(name)
			if slices.Contains(p.Preview.Unpriced, name) {
				text += "  " + paint.Hue("#f08a60", "no price")
			}
		case rowAddModel:
			if p.Typing {
				text = "+ " + term.CleanLine(p.Draft) + paint.Bold(g("▏", "_")) + paint.Muted("   enter adds, esc cancels")
			} else {
				text = paint.Muted("+ add a model (creator/model, as the catalog names it)")
			}
		case rowEndpoint:
			text = term.PadEnd("Endpoint", 18) + g("‹ ", "< ") + paint.Bold(p.Choice.Endpoint) + g(" ›", " >")
		case rowPerPart:
			text = term.PadEnd("Cases per part", 18) + g("‹ ", "< ") + paint.Bold(strconv.Itoa(p.Choice.PerPart)) + g(" ›", " >")
		case rowJudge:
			text = term.PadEnd("Judge free text", 18) + box(p.Choice.Judge) + paint.Muted("  typesafe-ai/jev compares free-text replies; they are sent to it")
		case rowRun:
			label := "Run the map"
			if p.Ready() {
				text = paint.Bold(paint.Hue("#8aa8ff", label)) + paint.Muted("   enter, or r from anywhere")
			} else {
				text = paint.Muted(label + "   choose at least one model and one part")
			}
		}
		lines = append(lines, cursorAt(i)+text)
	}
	lines = append(lines, "", paint.Muted("ESTIMATE"))
	pv := p.Preview
	switch {
	case p.Pending:
		lines = append(lines, paint.Muted("Working it out..."))
	case pv.Err != "":
		lines = append(lines, paint.Hue("#f08a60", term.CleanLine(pv.Err)))
	default:
		lines = append(lines, fmt.Sprintf("%d calls. Expected %s, at most %s. Budget %s.", pv.Calls, usd(pv.ExpectedUSD), usd(pv.UpperUSD), usd(pv.BudgetUSD)))
		if len(pv.Unpriced) > 0 {
			lines = append(lines, paint.Hue("#f08a60", "No price for "+term.CleanLine(strings.Join(pv.Unpriced, ", "))+": the map will refuse them."))
		}
		for _, m := range pv.Missing {
			lines = append(lines, paint.Hue("#f08a60", term.CleanLine(m)))
		}
	}
	lines = append(lines, "", paint.Muted(g("↑↓", "up, down")+" move   space choose   "+g("←→", "left, right")+" change   r run   q quit"))
	return fit(lines, w, ascii)
}

func usd(v float64) string {
	if v > 0 && v < 0.01 {
		return fmt.Sprintf("$%.4f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

// header is the workflow's title lines: the step and its name.
func header(step, name string, paint term.Paint, width int, ascii bool) []string {
	logo := "▁▃▅▇"
	if ascii {
		logo = "::"
	}
	title := paint.Brand(logo) + " " + paint.Bold("replay")
	if step != "" {
		title += "  " + paint.Muted(step)
	}
	title += "  " + paint.Bold(name)
	rule := strings.Repeat("─", width)
	if ascii {
		rule = strings.Repeat("-", width)
	}
	return []string{title, paint.Line(rule)}
}

func fit(lines []string, width int, ascii bool) []string {
	ell := "…"
	if ascii {
		ell = "~"
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if ascii {
			l = term.ToAscii(l)
		}
		out[i] = term.Fit(l, width, term.AlignLeft, ell)
	}
	return out
}
