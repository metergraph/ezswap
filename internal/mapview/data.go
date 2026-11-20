// Package mapview is the interactive map: a grid of every model on every
// part, a cell's figures and cases, and one case's replies side by side.
// Like the run explorer, it is a pure state and reducer (state.go), a pure
// frame (screen.go) and a thin Bubble Tea layer (program.go).
package mapview

import (
	"strings"

	"github.com/metergraph/ezswap/internal/report"
	"github.com/metergraph/ezswap/internal/report/term"
	"github.com/metergraph/ezswap/internal/types"
)

// Reply is one answer to a case: the recording, a treatment or a rerun.
type Reply struct {
	Label string
	Text  string
	// Why there is no usable text, or "": "failed: ...", "cut off", "refused".
	Problem   string
	CostUSD   *float64
	LatencyMs *float64
}

// Case is one sampled call, with every reply to it.
type Case struct {
	ID      string
	Request string
	// The last message, for one-line previews.
	Preview  string
	Recorded Reply
	// By arm: treatment ids, types.ArmFloorA1, types.ArmFloorA2.
	Arms map[string]Reply
}

// Data is everything the map explorer shows, worked out once.
type Data struct {
	Map *report.Map
	// The parts in the order the grid shows them: compared parts first.
	Parts []report.MapPart
	// By part name, in the run's order.
	Cases map[string][]*Case
	// Where the HTML page was written, or "".
	HTMLPath string
	// The run file as shown, for the header.
	Name  string
	ASCII bool
	// The guided workflow's step, shown after the title, or "".
	Step string
}

// Options say what NewData shows.
type Options struct {
	HTMLPath string
	Name     string
	ASCII    bool
	Step     string
}

// NewData is the explorer's data for a map run.
func NewData(run *types.RunFile, m *report.Map, o Options) *Data {
	d := &Data{Map: m, Parts: report.SummaryParts(m), Cases: map[string][]*Case{}, HTMLPath: o.HTMLPath, Name: o.Name, ASCII: o.ASCII, Step: o.Step}
	partOf := map[string]string{}
	for _, part := range m.Parts {
		for _, key := range part.Keys {
			partOf[key] = part.Name
		}
	}
	replays := map[string]map[string]*types.Replay{}
	for i := range run.Replays {
		r := &run.Replays[i]
		if replays[r.CaseID] == nil {
			replays[r.CaseID] = map[string]*types.Replay{}
		}
		replays[r.CaseID][r.Arm] = r
	}
	labels := map[string]string{types.ArmFloorA1: "production rerun", types.ArmFloorA2: "second rerun"}
	for i, t := range run.Treatments {
		labels[t.ID] = report.Letter(i) + " " + t.Label
	}
	for _, kase := range run.Cases {
		name, ok := partOf[kase.PartKey]
		if !ok || kase.NotReplayable != nil {
			continue
		}
		c := &Case{ID: kase.ID, Request: requestText(kase), Preview: lastMessage(kase), Arms: map[string]Reply{}}
		c.Recorded = Reply{Label: "recorded", Text: deref(kase.Production.Output), Problem: problem("ok", kase.Production.FinishReason, kase.Production.Refusal, nil)}
		for arm, r := range replays[kase.ID] {
			var message *string
			if r.Error != nil {
				text := r.Error.Code
				if r.Error.Message != "" {
					text += ": " + r.Error.Message
				}
				message = &text
			}
			c.Arms[arm] = Reply{
				Label: term.CleanLine(labels[arm]), Text: deref(r.Output),
				// The error text is from the run file: cleaned like every
				// other captured string.
				Problem: term.CleanLine(problem(r.Status, r.FinishReason, r.Refusal, message)),
				CostUSD: r.CostUSD, LatencyMs: r.LatencyMs,
			}
		}
		d.Cases[name] = append(d.Cases[name], c)
	}
	return d
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func problem(status string, finish *string, refusal bool, message *string) string {
	switch {
	case status == "skipped":
		return "not sent"
	case status != "ok":
		if message != nil {
			return "failed: " + *message
		}
		return "failed"
	case refusal:
		return "refused"
	case finish != nil && (*finish == "length" || *finish == "max_tokens"):
		return "cut off"
	}
	return ""
}

func requestText(kase types.Case) string {
	var b strings.Builder
	if kase.System != nil && strings.TrimSpace(*kase.System) != "" {
		b.WriteString("[system]\n" + *kase.System + "\n\n")
	}
	for i, m := range kase.Messages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("[" + m.Role + "]\n" + m.Content)
	}
	return b.String()
}

func lastMessage(kase types.Case) string {
	if len(kase.Messages) == 0 {
		return ""
	}
	return strings.Join(strings.Fields(kase.Messages[len(kase.Messages)-1].Content), " ")
}
