package report

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/metergraph/replay/internal/core"
	"github.com/metergraph/replay/internal/report/term"
)

const (
	mapPartWidth  = 16
	mapValueWidth = 24
	// MapFooter is the last line of every map.
	MapFooter = "This shows where outputs change. It does not say a change is better or worse."
	// MapRule names the label's rule, which is not the one the report's
	// per-field labels use.
	MapRule = "CHANGED: cases where the treatment's answer differs from the recorded one. RERUN: the same for a fresh run of production's own model. A part is beyond noise when CHANGED exceeds RERUN by a paired sign test at 5%."
)

var mapLabels = map[string]string{
	ResultBeyondNoise:   "beyond noise",
	ResultWithinNoise:   "within noise",
	ResultTooFewCalls:   "too few calls",
	ResultNotComparable: "not comparable",
}

var mapRank = map[string]int{ResultBeyondNoise: 0, ResultWithinNoise: 1, ResultTooFewCalls: 2, ResultNotComparable: 3}

// percentChange is a relative change as the map prints it: -60%, +12%, 0%.
func percentChange(c ctx, value *float64) string {
	if value == nil {
		return c.g("—", "-")
	}
	rounded := math.Round(*value * 100)
	switch {
	case rounded > 0:
		return fmt.Sprintf("+%.0f%%", rounded)
	case rounded < 0:
		return fmt.Sprintf("-%.0f%%", -rounded)
	}
	return "0%"
}

func (c ctx) cut(text string, width int) string {
	return term.Truncate(c.clean(text), width, "~")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// RenderMap is the printed map: one block per treatment, one row per part.
func RenderMap(m *Map, o RenderOptions) string {
	c := newCtx(o)
	p := c.p
	sep := c.g(" · ", " | ")
	var lines []string
	head := []string{fmt.Sprintf("%d %s", len(m.Parts), plural(len(m.Parts), "part", "parts")), c.clean(m.File)}
	if m.From != "" {
		if m.From == m.To {
			head = append(head, m.From)
		} else {
			head = append(head, m.From+" to "+m.To)
		}
	}
	head = append(head, fmt.Sprintf("%d cases per part", m.PerPart))
	lines = append(lines, p.Bold(strings.Join(head, sep)))
	lines = append(lines, p.Muted("spent "+core.FormatUSD(m.SpentUSD)+" of "+core.FormatUSD(m.BudgetUSD)))
	if m.Stopped != nil {
		lines = append(lines, "The run stopped early ("+c.clean(strings.ReplaceAll(*m.Stopped, "_", " "))+"). Parts are labelled from the cases that finished.")
	}

	if len(m.Treatments) >= 2 {
		lines = append(lines, summaryBlock(c, m)...)
	}
	for ti, treatment := range m.Treatments {
		lines = append(lines, "")
		lines = append(lines, c.hue(ti, "treatment "+Letter(ti)+" "+c.clean(treatment.Label)))
		lines = append(lines, "")
		lines = append(lines, p.Muted(mapRow("PART", "LABEL", "CHANGED", "RERUN", "COST/CALL", "LATENCY")))

		order := make([]int, len(m.Parts))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool {
			x, y := m.Parts[order[a]], m.Parts[order[b]]
			tx, ty := x.Treatments[ti], y.Treatments[ti]
			if mapRank[tx.Result] != mapRank[ty.Result] {
				return mapRank[tx.Result] < mapRank[ty.Result]
			}
			if tx.Changed != ty.Changed {
				return tx.Changed > ty.Changed
			}
			return x.Name < y.Name
		})
		var notes []string
		var blocks [][]string
		for _, i := range order {
			part := m.Parts[i]
			t := part.Treatments[ti]
			name := term.PadEnd(c.cut(part.Name, mapPartWidth), mapPartWidth)
			label := term.PadEnd(mapLabels[t.Result], 15)
			cost, latency := percentChange(c, t.CostPerCallChange), percentChange(c, t.LatencyChange)
			var row string
			switch t.Result {
			case ResultBeyondNoise, ResultWithinNoise:
				of := fmt.Sprintf(" of %d", t.Paired)
				row = name + " " + label + " " + term.PadEnd(fmt.Sprintf("%d%s", t.Changed, of), 10) +
					term.PadEnd(fmt.Sprintf("%d%s", t.Rerun, of), 10) + term.PadEnd(cost, 11) + latency
				if t.Result == ResultBeyondNoise {
					row = name + " " + p.Emph(label) + row[len(name)+1+len(label):]
				}
			default:
				reason := ""
				if t.Reason != nil {
					reason = *t.Reason
				}
				row = name + " " + p.Muted(label) + " " + term.PadEnd(c.cut(reason, 19), 20) + term.PadEnd(cost, 11) + latency
			}
			lines = append(lines, row)
			for _, note := range t.Notes {
				notes = append(notes, c.clean(note))
			}
			if part.LeftOut > 0 {
				notes = append(notes, fmt.Sprintf("%s: left out %d %s whose recorded answer has no compared field.",
					c.clean(part.Name), part.LeftOut, plural(part.LeftOut, "case", "cases")))
			}
			if t.Failed > 0 {
				notes = append(notes, fmt.Sprintf("%s: %d treatment %s failed.", c.clean(part.Name), t.Failed, plural(t.Failed, "call", "calls")))
			}
			if t.CutOff > 0 {
				notes = append(notes, CutOffNote(c.clean(part.Name), t))
			}
			if (t.Result == ResultBeyondNoise || t.Result == ResultWithinNoise) && t.Changed > 0 &&
				(t.Result == ResultBeyondNoise || t.Changed > t.Rerun) {
				blocks = append(blocks, changeBlock(c, part, t))
			}
		}
		if len(blocks) > 0 {
			lines = append(lines, "", p.Muted("WHAT CHANGED"))
			for _, block := range blocks {
				lines = append(lines, block...)
			}
		}
		if len(notes) > 0 {
			lines = append(lines, "")
			for _, note := range notes {
				lines = append(lines, term.Wrap(note, c.w)...)
			}
		}
	}
	lines = append(lines, "")
	if note := JudgedNote(m); note != "" {
		for _, line := range term.Wrap(c.clean(note), c.w) {
			lines = append(lines, p.Muted(line))
		}
	}
	for _, line := range term.Wrap(MapRule, c.w) {
		lines = append(lines, p.Muted(line))
	}
	lines = append(lines, MapFooter)
	return finish(c, lines) + "\n"
}

func mapRow(part, label, changed, rerun, cost, latency string) string {
	return term.PadEnd(part, mapPartWidth) + " " + term.PadEnd(label, 15) + " " +
		term.PadEnd(changed, 10) + term.PadEnd(rerun, 10) + term.PadEnd(cost, 11) + latency
}

// changeBlock is one part's lines under WHAT CHANGED: its per-path counts,
// then the cases that changed. No prompt text and no whole reply is printed.
func changeBlock(c ctx, part MapPart, t MapTreatment) []string {
	p := c.p
	lines := []string{p.Bold(c.clean(part.Name))}
	width := 0
	for _, path := range t.Paths {
		width = max(width, term.Width(c.cut(ShortPath(path.Path), mapValueWidth)))
	}
	for _, change := range t.Changes {
		width = max(width, term.Width(c.cut(ShortPath(change.Path), mapValueWidth)))
	}
	for _, path := range t.Paths {
		if path.Changed == 0 && path.Rerun == 0 {
			continue
		}
		lines = append(lines, "  "+term.PadEnd(c.cut(ShortPath(path.Path), mapValueWidth), width)+
			p.Muted(fmt.Sprintf("  changed %d, rerun %d", path.Changed, path.Rerun)))
	}
	limit := c.opts.ChangeLimit
	if limit == 0 {
		limit = ChangeLimit
	}
	ids := 0
	for i, change := range t.Changes {
		if i >= limit {
			break
		}
		ids = max(ids, term.Width(c.cut(change.CaseID, 18)))
	}
	for i, change := range t.Changes {
		if i >= limit {
			lines = append(lines, p.Muted(fmt.Sprintf("  and %d more", len(t.Changes)-limit)))
			break
		}
		if t.Judged {
			lines = append(lines, "  "+p.Muted(term.PadEnd(c.cut(change.CaseID, 18), ids))+"  "+c.cut(change.To, 40))
			continue
		}
		lines = append(lines, "  "+p.Muted(term.PadEnd(c.cut(change.CaseID, 18), ids))+"  "+
			term.PadEnd(c.cut(ShortPath(change.Path), mapValueWidth), width)+"  "+
			c.cut(change.From, mapValueWidth)+" -> "+c.cut(change.To, mapValueWidth))
	}
	return lines
}

// JudgedNote names the free-text parts a judge compared, and what its
// CHANGED means, or "" when no part was judged.
func JudgedNote(m *Map) string {
	var names []string
	for _, part := range m.Parts {
		for _, t := range part.Treatments {
			if t.Judged {
				names = append(names, part.Name)
				break
			}
		}
	}
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("JUDGED: %s %s free text, compared by %s. CHANGED counts replies it judged to give different facts, commitments or next steps from the recorded one, asked in both orders; RERUN is judged the same way.",
		strings.Join(names, ", "), plural(len(names), "is", "are"), m.JudgeModel)
}

// MapJSON is the map as --json prints it.
func MapJSON(m *Map) ([]byte, error) {
	return json.Marshal(m)
}

// CutOffNote says how many replies hit the output cap and, when reasoning
// took most of it, which options give the model room.
func CutOffNote(name string, t MapTreatment) string {
	note := fmt.Sprintf("%s: %d of %d replies cut off at the output cap", name, t.CutOff, t.Paired)
	if t.CutOffReasoning == 0 {
		return note + ". Try --max-tokens."
	}
	return note + fmt.Sprintf(", %d of them mostly reasoning. Try --max-tokens or --reasoning-effort.", t.CutOffReasoning)
}
