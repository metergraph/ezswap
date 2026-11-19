package report

import (
	"fmt"
	"strings"

	"github.com/metergraph/replay/internal/report/term"
)

const (
	summaryCellWidth  = 16
	summaryModelWidth = 30
	summaryCostWidth  = 10
)

// SummaryParts is the summary's column order: parts with a comparison first,
// then the rest, each in the map's order.
func SummaryParts(m *Map) []MapPart {
	var compared, rest []MapPart
	for _, part := range m.Parts {
		if len(part.Paths) > 0 {
			compared = append(compared, part)
		} else {
			rest = append(rest, part)
		}
	}
	return append(compared, rest...)
}

// SummaryCost is treatment ti's mean cost-per-call change across the parts
// that have one, or nil.
func SummaryCost(m *Map, ti int) *float64 {
	sum, n := 0.0, 0
	for _, part := range m.Parts {
		if v := part.Treatments[ti].CostPerCallChange; v != nil {
			sum += *v
			n++
		}
	}
	if n == 0 {
		return nil
	}
	mean := sum / float64(n)
	return &mean
}

// SummaryCell is one treatment on one part, as the summary shows it:
// "beyond 11/20", "within 0/20", "too few", "free text". CutOff marks a
// treatment with replies cut off at the output cap on this part.
func SummaryCell(t MapTreatment) (text string, cutOff bool) {
	switch t.Result {
	case ResultBeyondNoise:
		text = fmt.Sprintf("beyond %d/%d", t.Changed, t.Paired)
	case ResultWithinNoise:
		text = fmt.Sprintf("within %d/%d", t.Changed, t.Paired)
	case ResultTooFewCalls:
		text = "too few"
	default:
		text = "not compared"
		if t.Reason != nil && strings.Contains(*t.Reason, "free text") {
			text = "free text"
		}
	}
	return text, t.CutOff > 0
}

// summaryBlock is the map's first block when it has two or more
// treatments: one row per treatment, one column per part, so a part that
// moves under every model and one that moves under none show at a glance.
// Parts that do not fit the width go on a second table.
func summaryBlock(c ctx, m *Map) []string {
	p := c.p
	modelWidth := 0
	for ti, t := range m.Treatments {
		modelWidth = max(modelWidth, len(Letter(ti))+1+len([]rune(c.clean(t.Label))))
	}
	modelWidth = min(modelWidth, summaryModelWidth)
	ordered := SummaryParts(m)
	perRow := max(1, (c.w-modelWidth-1-summaryCostWidth)/summaryCellWidth)
	lines := []string{"", p.Bold("SUMMARY")}
	anyCut := false
	for start := 0; start < len(ordered); start += perRow {
		parts := ordered[start:min(start+perRow, len(ordered))]
		last := start+perRow >= len(ordered)
		head := term.PadEnd("", modelWidth) + " "
		for _, part := range parts {
			head += term.PadEnd(c.cut(part.Name, summaryCellWidth-1), summaryCellWidth)
		}
		if last {
			head += "COST/CALL"
		}
		lines = append(lines, "", p.Muted(strings.TrimRight(head, " ")))
		for ti, t := range m.Treatments {
			row := c.hue(ti, term.PadEnd(c.cut(Letter(ti)+" "+t.Label, modelWidth), modelWidth)) + " "
			for _, part := range parts {
				text, cut := SummaryCell(part.Treatments[ti])
				if cut {
					text += "*"
					anyCut = true
				}
				cell := term.PadEnd(text, summaryCellWidth)
				switch part.Treatments[ti].Result {
				case ResultBeyondNoise:
					cell = p.Emph(cell)
				case ResultTooFewCalls, ResultNotComparable:
					cell = p.Muted(cell)
				}
				row += cell
			}
			if last {
				row += percentChange(c, SummaryCost(m, ti))
			}
			lines = append(lines, strings.TrimRight(row, " "))
		}
	}
	lines = append(lines, "")
	key := "beyond/within N/M: N of M paired answers changed, and whether that exceeds a fresh run of production's model."
	if rerun := maxRerun(m); rerun == 0 {
		key += " Production's own rerun changed none."
	} else {
		key += fmt.Sprintf(" Production's own rerun changed up to %d per part.", rerun)
	}
	key += " COST/CALL: the mean change in cost per call across the parts."
	if anyCut {
		key += " *: replies cut off at the output cap (see the notes below)."
	}
	if JudgedNote(m) != "" {
		key += " Free-text parts were compared by " + m.JudgeModel + " (see JUDGED below)."
	}
	lines = append(lines, term.Wrap(p.Muted(key), c.w)...)
	return lines
}

func maxRerun(m *Map) int {
	most := 0
	for _, part := range m.Parts {
		for _, t := range part.Treatments {
			if t.Result == ResultBeyondNoise || t.Result == ResultWithinNoise {
				most = max(most, t.Rerun)
			}
		}
	}
	return most
}
