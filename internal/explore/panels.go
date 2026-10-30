package explore

// The boxed panels the explorer's tabs are built from: the tracked fields
// and the reasons calls were not replayed. They come from an earlier
// layout of the printed report, which it no longer uses.

import (
	"math"
	"regexp"
	"strings"

	"github.com/metergraph/replay/internal/core"
	"github.com/metergraph/replay/internal/report"
	"github.com/metergraph/replay/internal/report/term"
)

// ctx is everything a panel needs to draw.
type ctx struct {
	w     int
	p     term.Paint
	ascii bool
	chars term.Charset
	ell   string
}

func newCtx(width int, p term.Paint, ascii bool) ctx {
	ell := term.Ellipsis
	if ascii {
		ell = "~"
	}
	return ctx{
		w:     max(report.MinWidth, min(report.MaxWidth, width)),
		p:     p,
		ascii: ascii,
		chars: term.CharsetFor(ascii),
		ell:   ell,
	}
}

// contentCtx is the context the tabs draw in at a terminal width.
func contentCtx(columns int, p term.Paint, ascii bool) ctx {
	return newCtx(min(columns, report.MaxWidth), p, ascii)
}

func (c ctx) g(unicode, ascii string) string {
	if c.ascii {
		return ascii
	}
	return unicode
}

func (c ctx) inner() int { return c.w - 4 }

func (c ctx) panel(title string, lines []string, note string) []string {
	return term.Box(lines, term.BoxOptions{Width: c.w, Chars: c.chars, Paint: c.p, Title: title, Note: note, Ellipsis: c.ell})
}

func (c ctx) muteWrap(text string, cols, indent int) []string {
	var out []string
	for _, l := range term.Wrap(text, max(8, cols-indent)) {
		out = append(out, strings.Repeat(" ", indent)+c.p.Muted(l))
	}
	return out
}

func stripPath(path string) string {
	s := strings.TrimPrefix(path, "$")
	s = strings.TrimPrefix(s, ".")
	if s == "" {
		return path
	}
	return s
}

var (
	higherSuffix = regexp.MustCompile(`, higher$`)
	lowerSuffix  = regexp.MustCompile(`, lower$`)
)

// badge is a within-noise label: brackets keep the same width with and without colour.
func (c ctx) badge(label *report.LabelCell) string {
	if label == nil {
		return ""
	}
	text := label.Text
	if !c.ascii {
		text = higherSuffix.ReplaceAllString(text, " ↑")
		text = lowerSuffix.ReplaceAllString(text, " ↓")
	}
	if label.Tone == "beyond" {
		if c.p.Color {
			return c.p.Highlight(" " + text + " ")
		}
		return "[" + text + "]"
	}
	return c.p.Muted("(" + text + ")")
}

func fieldsPanel(c ctx, m *report.Model, sentences, changes bool) []string {
	inner := c.inner()
	var lines []string
	if len(m.Fields) == 0 {
		lines = append(lines, c.muteWrap("No tracked fields. Pass --track $.field (for example --track $.category) to compare a JSON field with production's.", inner, 0)...)
		return c.panel("Tracked fields", lines, "")
	}
	for i, f := range m.Fields {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, fieldBlock(c, f, inner, sentences, changes)...)
	}
	note := ""
	if m.HasFloor {
		note = c.g("┆", "|") + " marks production's agreement with itself"
	}
	return c.panel("Tracked fields", lines, note)
}

func fieldBlock(c ctx, field report.FieldPanel, inner int, sentences, changes bool) []string {
	p := c.p
	var lines []string
	title := p.Bold(stripPath(field.Path)) + "  " + p.Muted(strings.ToLower(field.KindLabel)+" field")
	floorText := ""
	if field.Floor != nil && field.Floor.Text != nil && *field.Floor.Text != "" {
		floorText = p.Floor(c.g("┆", "|")) + " " + p.Floor("vs itself "+*field.Floor.Text)
	}
	if floorText != "" && term.Width(title)+2+term.Width(floorText) <= inner {
		lines = append(lines, title+strings.Repeat(" ", inner-term.Width(title)-term.Width(floorText))+floorText)
	} else {
		lines = append(lines, title)
		if floorText != "" {
			lines = append(lines, floorText)
		}
	}
	cells := make([]map[string]string, len(field.Arms))
	for i, arm := range field.Arms {
		value := arm.ValueText
		if arm.Badge != nil && arm.Badge.Tone == "beyond" {
			value = p.Emph(value)
		}
		cells[i] = map[string]string{
			"name":   p.Treat(arm.Name),
			"bar":    "",
			"value":  value,
			"detail": p.Muted(arm.Detail),
			"badge":  c.badge(arm.Badge),
		}
	}
	rows := append([]map[string]string{}, cells...)
	if field.Scale == "shift" && field.Floor != nil {
		rows = append(rows, map[string]string{"name": p.Floor("vs itself"), "bar": "", "value": "", "detail": "", "badge": ""})
	}
	l := term.LayoutColumns([]term.ColumnSpec{
		{ID: "name", Max: term.Cols(26)},
		{ID: "bar", Flex: true, Min: term.Cols(16), Max: term.Cols(36)},
		{ID: "value", Align: term.AlignRight},
		{ID: "detail", Drop: 2},
		{ID: "badge", Drop: 1},
	}, rows, inner)
	barW := 10
	if term.Has(l, "bar") {
		barW = term.WidthOf(l, "bar")
	}
	barX := term.OffsetOf(l, "bar")
	axis := 1.0
	if field.Scale == "shift" {
		axis = 1e-9
		for _, a := range field.Arms {
			if a.Range != nil {
				axis = math.Max(axis, math.Max(math.Abs(a.Range.Lo), math.Abs(a.Range.Hi)))
			}
		}
		if field.Floor != nil && field.Floor.Low != nil && field.Floor.High != nil {
			axis = math.Max(axis, math.Max(math.Abs(*field.Floor.Low), math.Abs(*field.Floor.High)))
		}
	}
	for i, arm := range field.Arms {
		cell := cells[i]
		switch {
		case field.Scale == "share":
			var floor *float64
			if field.Floor != nil {
				floor = field.Floor.Value
			}
			cell["bar"] = term.PaintSegments(term.AgreementBar(arm.Value, floor, barW, c.ascii), map[term.SegmentKind]func(string) string{
				term.KindFill:  p.Treat,
				term.KindTrack: p.Line,
				term.KindTick:  func(t string) string { return p.Bold(p.Floor(t)) },
			})
		case field.Scale == "shift" && arm.Range != nil:
			lo, mid, hi := arm.Range.Lo, arm.Range.Mid, arm.Range.Hi
			cell["bar"] = term.PaintSegments(term.RangeBar(&lo, &mid, &hi, -axis, axis, barW, c.ascii), map[term.SegmentKind]func(string) string{
				term.KindRange: p.Treat,
				term.KindMark:  p.Treat,
				term.KindTrack: p.Line,
			})
		}
		lines = append(lines, term.RenderRow(l, cell, c.ell))
		if changes && len(arm.Changes) > 0 {
			parts := make([]string, len(arm.Changes))
			for k, ch := range arm.Changes {
				parts[k] = ch.From + " " + c.g("→", "->") + " " + ch.To + " " + c.g("×", "x") + core.FormatCount(ch.Count)
			}
			for _, t := range term.Wrap("changed: "+strings.Join(parts, ", "), inner-barX) {
				lines = append(lines, strings.Repeat(" ", barX)+p.Muted(t))
			}
		}
		if sentences {
			lines = append(lines, c.muteWrap(arm.Sentence, inner, barX)...)
		}
	}
	if field.Scale == "shift" && field.Floor != nil && field.Floor.Low != nil && field.Floor.High != nil {
		bar := term.PaintSegments(term.RangeBar(field.Floor.Low, field.Floor.Value, field.Floor.High, -axis, axis, barW, c.ascii), map[term.SegmentKind]func(string) string{
			term.KindRange: p.Floor,
			term.KindMark:  p.Floor,
			term.KindTrack: p.Line,
		})
		text := ""
		if field.Floor.Text != nil {
			text = *field.Floor.Text
		}
		lines = append(lines, term.RenderRow(l, map[string]string{"name": p.Floor("vs itself"), "bar": bar, "value": p.Floor(text)}, c.ell))
	}
	return lines
}

// notReplayablePanel is nil when every sampled call was replayed.
func notReplayablePanel(c ctx, m *report.Model) []string {
	if len(m.NotReplayable) == 0 {
		return nil
	}
	p := c.p
	countW := 0
	total := 0
	for _, r := range m.NotReplayable {
		countW = max(countW, term.Width(core.FormatCount(r.Count)))
		total += r.Count
	}
	var lines []string
	for i, r := range m.NotReplayable {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, term.Fit(core.FormatCount(r.Count), countW, term.AlignRight)+"  "+p.Bold(r.Title))
		lines = append(lines, c.muteWrap(r.Fix, c.inner(), countW+2)...)
	}
	sampled := 0
	if m.View != nil {
		sampled = m.View.Sample.Cases
	}
	return c.panel("Not replayable", lines, core.FormatCount(total)+" of "+core.FormatCount(sampled)+" sampled calls")
}
