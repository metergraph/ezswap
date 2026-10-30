package explore

// What each tab and the call detail show, as lines at a width. Pure: the
// same data, width and style always give the same lines, which is what lets
// the reducer clamp scrolling against them.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/metergraph/replay/internal/core"
	"github.com/metergraph/replay/internal/report"
	"github.com/metergraph/replay/internal/report/term"
)

func keyOf(c ctx) string {
	return fmt.Sprintf("%d|%d|%t", c.w, c.p.Profile, c.ascii)
}

// changeLimit is how many changed fields the Summary tab lists before
// pointing at the Calls tab.
const changeLimit = 12

// tabLines are a scrollable tab's lines. The Calls tab is a list the screen
// draws itself, so it has none here.
func tabLines(d *Data, tab int, c ctx) []string {
	return d.cached("tab"+strconv.Itoa(tab)+"|"+keyOf(c), func() []string {
		switch tab {
		case 0:
			return summaryTab(c, d)
		case 1:
			return fieldsTab(c, d)
		case 2:
			return floorTab(c, d)
		case 4:
			return notReplayableTab(c, d)
		}
		return []string{}
	})
}

func summaryTab(c ctx, d *Data) []string {
	text := report.RenderSummary(d.Model, report.RenderOptions{
		Width:       c.w,
		Paint:       c.p,
		ASCII:       c.ascii,
		ChangeLimit: changeLimit,
		MoreHint: func(more int) string {
			return "+" + core.FormatCount(more) + " more on the Calls tab (press 4)"
		},
	})
	lines := strings.Split(text, "\n")
	lines = append(lines, "", c.p.Muted("Keep this running on live traffic:"))
	if c.p.Color && !c.ascii {
		lines = append(lines, c.p.Underline(term.Hyperlink(d.Link, report.HostedLabel)))
	} else {
		// Whole, over as many lines as it takes, so it can still be copied.
		for _, l := range term.Wrap(d.Link, c.w) {
			lines = append(lines, c.p.Underline(l))
		}
	}
	return lines
}

func fieldsTab(c ctx, d *Data) []string {
	lines := fieldsPanel(c, d.Clean, true, true)
	if floor := d.Clean.Floor; floor != nil {
		for _, f := range floor.Fields {
			if len(f.Mismatches) > 0 {
				lines = append(lines, "")
				lines = append(lines, floorMismatches(c, d)...)
				break
			}
		}
	}
	return lines
}

func floorMismatches(c ctx, d *Data) []string {
	p := c.p
	floor := d.Clean.Floor
	if floor == nil {
		return nil
	}
	inner := c.inner()
	var lines []string
	arrow := c.g("→", "->")
	for i, f := range floor.Fields {
		if i > 0 {
			lines = append(lines, "")
		}
		text := core.MissingValue
		if f.Text != nil {
			text = *f.Text
		}
		lines = append(lines, p.Bold(stripPathDollar(f.Path))+"  "+p.Floor(text))
		if len(f.Mismatches) == 0 {
			lines = append(lines, p.Muted("  A1 and A2 agreed on every call."))
			continue
		}
		idW := 0
		for _, m := range f.Mismatches {
			idW = max(idW, term.Width(m.TraceID))
		}
		idW = min(24, idW)
		for _, m := range f.Mismatches {
			lines = append(lines, term.Truncate("  "+term.Fit(m.TraceID, idW, term.AlignLeft)+"  "+p.Floor(m.A1)+" "+p.Muted(arrow)+" "+p.Floor(m.A2), inner, c.ell))
		}
	}
	return term.Box(lines, term.BoxOptions{Width: c.w, Chars: c.chars, Paint: p, Title: "Where production disagreed with itself", Note: "A1 then A2", Ellipsis: c.ell})
}

// stripPathDollar is path.replace(/^\$\.?/, ""): unlike stripPath, "$" alone becomes "".
func stripPathDollar(path string) string {
	return strings.TrimPrefix(strings.TrimPrefix(path, "$"), ".")
}

func floorTab(c ctx, d *Data) []string {
	p := c.p
	floor := d.Clean.Floor
	inner := c.inner()
	if floor == nil {
		var lines []string
		for _, l := range term.Wrap("This run has no noise floor: it was made with --no-noise-floor, so there is no reading of how much production differs from itself. Run again without that option to measure it.", inner) {
			lines = append(lines, p.Muted(l))
		}
		return c.panel("Noise floor", lines, "")
	}
	var lines []string
	for i, t := range floor.Text {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, term.Wrap(t, inner)...)
	}
	lines = append(lines, "")
	lines = append(lines, p.Muted(core.FormatCount(floor.Cases)+" calls replayed twice on "+floor.Model+", for "+floor.Spent+"."))
	lines = append(lines, term.Divider)
	labelW, a1W, a2W := 0, 2, 2
	for _, s := range floor.Stats {
		labelW = max(labelW, term.Width(s.Label))
		a1W = max(a1W, term.Width(s.A1))
		a2W = max(a2W, term.Width(s.A2))
	}
	lines = append(lines, term.Fit("", labelW, term.AlignLeft)+"  "+term.Fit(p.Floor("A1"), a1W, term.AlignRight)+"  "+term.Fit(p.Floor("A2"), a2W, term.AlignRight)+"  "+p.Floor("gap"))
	for _, s := range floor.Stats {
		lines = append(lines, term.Fit(s.Label, labelW, term.AlignLeft)+"  "+term.Fit(s.A1, a1W, term.AlignRight)+"  "+term.Fit(s.A2, a2W, term.AlignRight)+"  "+p.Floor(s.Gap))
	}
	out := c.panel("Noise floor", lines, "production against itself")
	if len(floor.Fields) > 0 {
		out = append(out, "")
		out = append(out, floorMismatches(c, d)...)
	}
	return out
}

var spaces = regexp.MustCompile(`\s+`)

func oneLine(text string) string {
	return strings.TrimSpace(spaces.ReplaceAllString(text, " "))
}

func strOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

func notReplayableTab(c ctx, d *Data) []string {
	p := c.p
	m := d.Clean
	summary := notReplayablePanel(c, m)
	if summary == nil {
		return c.panel("Not replayable", []string{p.Muted("Every sampled call was replayed.")}, "")
	}
	var calls []report.CaseItem
	idW := 0
	for _, cs := range m.Cases {
		if cs.Status == "not_replayable" {
			calls = append(calls, cs)
			idW = max(idW, term.Width(cs.TraceID))
		}
	}
	idW = min(24, idW)
	inner := c.inner()
	var lines []string
	for _, cs := range calls {
		lines = append(lines, term.Truncate(term.Fit(p.Bold(cs.TraceID), idW, term.AlignLeft)+"  "+term.Fit(strOr(cs.Reason, core.MissingValue), 24, term.AlignLeft, c.ell)+"  "+p.Muted(oneLine(strOr(cs.Input, ""))), inner, c.ell))
	}
	out := append(summary, "")
	return append(out, c.panel("Calls not replayed", lines, "enter one from the Calls tab")...)
}

// ---------------------------------------------------------------------------
// The call detail

const minPane = 36

var lastKeyRe = regexp.MustCompile(`(?:\.|\[["']?)([^.[\]"']+)["']?\]?$`)

// lastKey is the key a JSON path names last: "$.a.b" to "b".
func lastKey(path string) string {
	if m := lastKeyRe.FindStringSubmatch(path); m != nil {
		return m[1]
	}
	return path
}

type pane struct {
	out     report.ArmOutput
	role    term.Role
	title   string
	changed []string
}

func paneLines(c ctx, out report.ArmOutput, role term.Role, cols int, changed []string) []string {
	p := c.p
	inner := cols - 4
	var lines []string
	meta := strings.Join([]string{"finish " + strOr(out.FinishReason, core.MissingValue), out.Cost, out.Latency}, c.g(" · ", " | "))
	lines = append(lines, p.Muted(term.Truncate(meta, inner, c.ell)))
	for _, f := range out.Flags {
		for _, l := range term.Wrap(f, inner) {
			lines = append(lines, p.Emph(l))
		}
	}
	if out.Error != nil {
		for _, l := range term.Wrap("error: "+*out.Error, inner) {
			lines = append(lines, p.Emph(l))
		}
	}
	lines = append(lines, "")
	mark := c.g("▌", ">")
	color := term.RoleColor(p, role)
	body := out.Output
	if out.Pretty != nil {
		body = out.Pretty
	}
	if body == nil || *body == "" {
		text := "No output"
		if out.Status == "missing" {
			text = strOr(out.Error, "No result")
		}
		return append(lines, p.Muted(text))
	}
	var keyPattern *regexp.Regexp
	if len(changed) > 0 {
		quoted := make([]string, len(changed))
		for i, k := range changed {
			quoted[i] = regexp.QuoteMeta(k)
		}
		keyPattern = regexp.MustCompile(`^\s*"(` + strings.Join(quoted, "|") + `)":`)
	}
	for _, source := range strings.Split(*body, "\n") {
		hit := keyPattern != nil && out.Pretty != nil && keyPattern.MatchString(source)
		for _, piece := range term.Wrap(source, inner-2) {
			if hit {
				lines = append(lines, color(mark)+" "+p.Bold(piece))
			} else {
				lines = append(lines, "  "+piece)
			}
		}
	}
	return lines
}

func panes(c ctx, items []pane) []string {
	if len(items) == 0 {
		return nil
	}
	p := c.p
	perRow := max(1, min(len(items), (c.w+1)/(minPane+1)))
	var lines []string
	for start := 0; start < len(items); start += perRow {
		row := items[start:min(len(items), start+perRow)]
		cols := (c.w - (len(row) - 1)) / len(row)
		contents := make([][]string, len(row))
		height := 0
		for k, it := range row {
			contents[k] = paneLines(c, it.out, it.role, cols, it.changed)
			height = max(height, len(contents[k]))
		}
		boxes := make([][]string, len(row))
		for k, it := range row {
			content := contents[k]
			for len(content) < height {
				content = append(content, "")
			}
			color := term.RoleColor(p, it.role)
			status := ""
			switch {
			case it.out.Status == "failed":
				status = "failed"
			case it.out.Status == "missing":
				status = "no result"
			case it.out.Differs:
				status = "differs"
			}
			boxes[k] = term.Box(content, term.BoxOptions{Width: cols, Chars: c.chars, Paint: p, Title: color(it.title), Note: status, Ellipsis: c.ell})
		}
		if start > 0 {
			lines = append(lines, "")
		}
		for i := 0; i < height+2; i++ {
			parts := make([]string, len(boxes))
			for k, b := range boxes {
				if i < len(b) {
					parts[k] = b[i]
				}
			}
			lines = append(lines, strings.Join(parts, " "))
		}
	}
	return lines
}

// detailLines are everything about one call: its input, then production's
// output beside every arm's.
func detailLines(d *Data, index int, c ctx) []string {
	return d.cached("detail"+strconv.Itoa(index)+"|"+keyOf(c), func() []string {
		if index < 0 || index >= len(d.Clean.Cases) {
			return []string{}
		}
		return detail(c, d, d.Clean.Cases[index])
	})
}

func treatmentNames(m *report.Model) []string {
	var names []string
	for _, b := range m.Cost {
		if b.Role == report.RoleTreatment {
			names = append(names, b.Name)
		}
	}
	return names
}

func detail(c ctx, d *Data, cs report.CaseItem) []string {
	p := c.p
	m := d.Clean
	var lines []string
	status := "not replayable: " + strOr(cs.Reason, core.MissingValue)
	if cs.Status == "replayed" {
		status = "replayed"
		if cs.Differs {
			status = "replayed, differs from production"
		}
	}
	lines = append(lines, p.Bold(cs.TraceID)+"  "+p.Muted(status))
	for _, l := range cs.Limitations {
		lines = append(lines, p.Muted("note: "+l))
	}
	lines = append(lines, "", term.Rule(c.w, c.chars, p, "Input"))
	if cs.System != nil && *cs.System != "" {
		lines = append(lines, p.Muted("system"))
		for _, l := range term.Wrap(*cs.System, c.w-2) {
			lines = append(lines, "  "+p.Muted(l))
		}
	}
	switch {
	case len(cs.Messages) > 0:
		for _, msg := range cs.Messages {
			lines = append(lines, p.Bold(msg.Role))
			for _, l := range term.Wrap(msg.Content, c.w-2) {
				lines = append(lines, "  "+l)
			}
		}
	case cs.Input != nil && *cs.Input != "":
		for _, l := range term.Wrap(*cs.Input, c.w-2) {
			lines = append(lines, "  "+l)
		}
	default:
		lines = append(lines, p.Muted("  The trace carries no input text."))
	}
	lines = append(lines, "", term.Rule(c.w, c.chars, p, "Outputs"))
	names := treatmentNames(m)
	nameOf := func(i int, a report.ArmOutput) string {
		if i < len(names) {
			return names[i]
		}
		return a.Label
	}
	var all []string
	for _, a := range cs.Arms {
		for _, ch := range a.Changes {
			all = append(all, lastKey(ch.Path))
		}
	}
	items := []pane{{out: cs.Production, role: term.RoleProduction, title: "production (" + m.ProductionModel + ")", changed: all}}
	for i, a := range cs.Arms {
		var changed []string
		for _, ch := range a.Changes {
			changed = append(changed, lastKey(ch.Path))
		}
		items = append(items, pane{out: a, role: term.RoleTreatment, title: nameOf(i, a), changed: changed})
	}
	lines = append(lines, panes(c, items)...)
	var changes []string
	for i, a := range cs.Arms {
		for _, ch := range a.Changes {
			changes = append(changes, nameOf(i, a)+"  "+stripPathDollar(ch.Path)+": "+ch.From+" "+c.g("→", "->")+" "+ch.To)
		}
	}
	if len(changes) > 0 {
		lines = append(lines, "")
		for _, ch := range changes {
			lines = append(lines, term.Truncate(p.Treat(c.g("▌", ">"))+" "+ch, c.w, c.ell))
		}
	}
	if len(cs.Floor) > 0 {
		lines = append(lines, "", term.Rule(c.w, c.chars, p, "Noise floor replays"))
		var floor []pane
		for _, f := range cs.Floor {
			floor = append(floor, pane{out: f, role: term.RoleFloor, title: f.Label})
		}
		lines = append(lines, panes(c, floor)...)
	}
	return lines
}
