package report

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/metergraph/ezswap/internal/report/term"
	"github.com/metergraph/ezswap/internal/types"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata/golden")

func golden(t *testing.T, name, actual string) {
	t.Helper()
	file := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(actual), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/report -update to write it)", err)
	}
	expected := string(data)
	if actual != expected {
		a, e := strings.Split(actual, "\n"), strings.Split(expected, "\n")
		for i := range max(len(a), len(e)) {
			var al, el string
			if i < len(a) {
				al = a[i]
			}
			if i < len(e) {
				el = e[i]
			}
			if al != el {
				t.Fatalf("%s differs at line %d:\n  expected: %q\n  actual:   %q\nRun go test ./internal/report -update to accept the new output.", name, i+1, el, al)
			}
		}
	}
}

// A URL or a command alone on its line stays whole so it can be copied; the terminal may wrap it.
var linkLine = regexp.MustCompile(`^(?:https://\S+|ezswap report \S+)$`)

func checkWidth(t *testing.T, label, out string, w int) {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if linkLine.MatchString(term.StripAnsi(line)) {
			continue
		}
		if term.Width(line) > w {
			t.Errorf("%s: %d columns wider than %d: %q", label, term.Width(line), w, term.StripAnsi(line))
		}
	}
}

func TestGoldenDemo(t *testing.T) {
	m := mustModel(t, demoRun(t), Options{})
	for _, w := range []int{80, 120} {
		for _, ascii := range []bool{false, true} {
			name := fmt.Sprintf("demo-%d-%s.txt", w, map[bool]string{true: "ascii", false: "unicode"}[ascii])
			t.Run(name, func(t *testing.T) {
				out := Render(m, RenderOptions{Width: w, ASCII: ascii, Explore: "ezswap-out/run.json"})
				checkWidth(t, name, out, w)
				golden(t, name, out)
			})
		}
	}
}

// Edge-case runs.

func stopped(run *types.RunFile, why string) *types.RunFile {
	out := *run
	out.Stopped = &why
	return &out
}

// withoutTreatments is the run with no treatments: only the noise floor ran.
func withoutTreatments(run *types.RunFile) *types.RunFile {
	out := *run
	out.Treatments = nil
	out.Replays = nil
	for _, r := range run.Replays {
		if strings.HasPrefix(r.Arm, "floor_") {
			out.Replays = append(out.Replays, r)
		}
	}
	return &out
}

// allFailed is the run with every treatment call failed.
func allFailed(run *types.RunFile) *types.RunFile {
	out := *run
	out.Replays = make([]types.Replay, len(run.Replays))
	copy(out.Replays, run.Replays)
	for i := range out.Replays {
		r := &out.Replays[i]
		if strings.HasPrefix(r.Arm, "floor_") {
			continue
		}
		r.Status = "failed"
		r.Output = nil
		r.CostUSD = nil
		r.Error = &types.ReplayError{Code: "timeout", Message: "no reply in 60 s"}
	}
	return &out
}

// nothingReplayed is the run with every case not replayable.
func nothingReplayed(run *types.RunFile) *types.RunFile {
	out := *run
	out.Cases = make([]types.Case, len(run.Cases))
	copy(out.Cases, run.Cases)
	reason := types.ContentUnavailable
	for i := range out.Cases {
		out.Cases[i].NotReplayable = &reason
	}
	out.Replays = nil
	return &out
}

func edgeRuns(t *testing.T) map[string]*types.RunFile {
	return map[string]*types.RunFile{
		"demo":               demoRun(t),
		"fixture":            fixtureRun(t),
		"bare":               bare(fixtureRun(t)),
		"untimed-bare":       untimed(bare(demoRun(t))),
		"stopped":            stopped(demoRun(t), "budget"),
		"interrupted":        stopped(fixtureRun(t), "interrupted"),
		"no-treatments":      withoutTreatments(demoRun(t)),
		"all-failed":         allFailed(fixtureRun(t)),
		"nothing-replayed":   nothingReplayed(demoRun(t)),
		"hostile":            hostileRun(t),
		"treatments-galore":  manyTreatments(t),
		"no-floor-no-tracks": noTracks(bare(demoRun(t))),
	}
}

// manyTreatments is the fixture with five treatments, more than fit beside each other at 80 columns.
func manyTreatments(t *testing.T) *types.RunFile {
	run := fixtureRun(t)
	out := *run
	out.Treatments = append([]types.Treatment(nil), run.Treatments...)
	out.Replays = append([]types.Replay(nil), run.Replays...)
	for k := 3; k <= 5; k++ {
		base := run.Treatments[k%2]
		id := "t" + strconv.Itoa(k)
		base.ID = id
		base.Label = "anthropic/claude-haiku-4.5-variant-" + strconv.Itoa(k)
		base.Model = base.Label
		out.Treatments = append(out.Treatments, base)
		for _, r := range run.Replays {
			if r.Arm == run.Treatments[k%2].ID {
				r.Arm = id
				out.Replays = append(out.Replays, r)
			}
		}
	}
	return &out
}

func noTracks(run *types.RunFile) *types.RunFile {
	out := *run
	out.Settings.TrackedFields = nil
	return &out
}

func hostileRun(t *testing.T) *types.RunFile {
	run := fixtureRun(t)
	evil := "\x1b[2J\x1b[H\x1b]0;owned\x07\u009b31m\r\u202e"
	run.Input.File = "traces" + evil + ".jsonl"
	run.Catalog.Version = "v" + evil
	run.Treatments[0].Label = "Mini" + evil
	for i := range run.Cases {
		if run.Cases[i].NotReplayable == nil {
			preview := "ticket " + evil + " " + strings.Repeat("long line ", 400)
			run.Cases[i].InputPreview = &preview
			for j := range run.Replays {
				if run.Replays[j].CaseID == run.Cases[i].ID && run.Replays[j].Arm == "t1" {
					out := `{"category":"bug` + strings.ReplaceAll(strconv.Quote(evil), `"`, "") + `","priority":"low","tags":[]}`
					run.Replays[j].Output = &out
				}
			}
			break
		}
	}
	for j := range run.Replays {
		if run.Replays[j].Status == "failed" {
			run.Replays[j].Error = &types.ReplayError{Code: "timeout", Message: evil}
		}
	}
	return run
}

func TestNoLineWiderThanTheTerminal(t *testing.T) {
	for name, run := range edgeRuns(t) {
		m := mustModel(t, run, Options{Labels: true})
		for w := 40; w <= 140; w += 4 {
			for _, ascii := range []bool{false, true} {
				for _, color := range []bool{false, true} {
					p := term.PlainPaint()
					if color {
						p = term.NewPaint(colorprofile.TrueColor)
					}
					out := Render(m, RenderOptions{Width: w, ASCII: ascii, Paint: p, Explore: "ezswap-out/run.json"})
					checkWidth(t, fmt.Sprintf("%s %d ascii=%v color=%v", name, w, ascii, color), out, w)
				}
			}
		}
	}
	// Wider terminals get at most MaxWidth.
	out := Render(mustModel(t, manyTreatments(t), Options{}), RenderOptions{Width: 300})
	checkWidth(t, "300", out, MaxWidth)
}

func TestNarrowWidthWrapsTreatmentsIntoGroups(t *testing.T) {
	m := mustModel(t, demoRun(t), Options{})
	out := RenderSummary(m, RenderOptions{Width: 60})
	checkWidth(t, "60", out, 60)
	if len(regexp.MustCompile(`(?m)^ +production`).FindAllString(out, -1)) != 2 {
		t.Errorf("want two column groups at 60 columns:\n%s", out)
	}
	for _, want := range []string{"A gemini", "B gpt-5.4-nano"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q at 60 columns", want)
		}
	}
	// Five treatments at 80 columns: grouped, never wider.
	many := Render(mustModel(t, manyTreatments(t), Options{}), RenderOptions{Width: 80})
	checkWidth(t, "many", many, 80)
	for _, letter := range []string{"A ", "B ", "C ", "D ", "E "} {
		if !strings.Contains(many, letter) {
			t.Errorf("no column %q", letter)
		}
	}
}

func rowByLabel(t *testing.T, rows []tableRow, label string) tableRow {
	t.Helper()
	for _, r := range rows {
		if r.label == label {
			return r
		}
	}
	t.Fatalf("no row %q", label)
	return tableRow{}
}

func TestFiguresEqualToProductionAreDimmed(t *testing.T) {
	c := newCtx(RenderOptions{Width: 80})
	rows := tableRows(c, mustModel(t, demoRun(t), Options{}))
	perCall := rowByLabel(t, rows, "per call")
	if !perCall.itself.same || perCall.arms[0].same || perCall.arms[1].same {
		t.Errorf("per call: %+v", perCall)
	}
	parses := rowByLabel(t, rows, "JSON parses")
	if !parses.itself.same || !parses.arms[0].same || parses.arms[1].same {
		t.Errorf("JSON parses: %+v", parses)
	}
	// A missing production figure dims nothing.
	agrees := rowByLabel(t, rows, "category agrees")
	if agrees.itself.same || agrees.arms[0].same || agrees.arms[1].same {
		t.Errorf("category agrees: %+v", agrees)
	}
	// Dim is SGR 2 (faint): the treatment figure equal to production's is drawn faint.
	p := term.NewPaint(colorprofile.TrueColor)
	out := RenderSummary(mustModel(t, demoRun(t), Options{}), RenderOptions{Width: 120, Paint: p})
	if !strings.Contains(out, p.Muted("100%")) {
		t.Error("an equal figure is not dimmed")
	}
}

func TestOnlyDifferencesBeyondTheFloorAreEmphasized(t *testing.T) {
	m := mustModel(t, demoRun(t), Options{})
	c := newCtx(RenderOptions{Width: 80})
	rows := tableRows(c, m)
	// Gemini's cost is beyond the floor; its priority agreement (85%, production's own 85%) is not.
	if !rowByLabel(t, rows, "per call").arms[0].beyond {
		t.Error("cost beyond the floor is not emphasized")
	}
	if rowByLabel(t, rows, "priority agrees").arms[0].beyond {
		t.Error("a difference within the floor is emphasized")
	}
	// Emphasis is the treatment's own colour, bold, and the mark.
	p := term.NewPaint(colorprofile.TrueColor)
	out := RenderSummary(m, RenderOptions{Width: 120, Paint: p})
	if !strings.Contains(out, p.Bold(p.Hue(term.TreatmentHues[0], "$0.000034"))) {
		t.Error("gemini's cost is not in its colour")
	}
	if !strings.Contains(out, p.Bold(p.Hue(term.TreatmentHues[1], "$0.00035"))) {
		t.Error("gpt-5.4-nano's cost is not in its colour")
	}
	plain := RenderSummary(m, RenderOptions{Width: 120})
	if !strings.Contains(plain, "$0.000034*") || strings.Contains(plain, "85%*") {
		t.Errorf("marks:\n%s", plain)
	}
	if !strings.Contains(plain, "* further from production than production is from itself") {
		t.Error("no legend for the mark")
	}
	// No floor, nothing to be beyond.
	none := RenderSummary(mustModel(t, bare(fixtureRun(t)), Options{}), RenderOptions{Width: 120})
	if strings.Contains(none, BeyondMark) {
		t.Errorf("a mark without a floor:\n%s", none)
	}
}

func TestColourMarksModelsNotVerdicts(t *testing.T) {
	m := mustModel(t, demoRun(t), Options{Labels: true})
	red := regexp.MustCompile(`\x1b\[(?:[0-9;]*;)?(?:31|32|91|92)m`)
	red256 := regexp.MustCompile(`38;5;(?:2|9|10|28|34|40|46|82|118|160|196|124)m`)
	for _, profile := range []colorprofile.Profile{colorprofile.ANSI256, colorprofile.TrueColor} {
		p := term.NewPaint(profile)
		out := Render(m, RenderOptions{Width: 120, Paint: p})
		if red.MatchString(out) || red256.MatchString(out) {
			t.Errorf("profile %v: red or green SGR", profile)
		}
		for _, hex := range []string{term.AlertColors.Good, term.AlertColors.Bad} {
			n, _ := strconv.ParseInt(hex[1:], 16, 64)
			code := fmt.Sprintf("5;%dm", term.Nearest256(hex))
			if profile == colorprofile.TrueColor {
				code = fmt.Sprintf("2;%d;%d;%dm", n>>16&255, n>>8&255, n&255)
			}
			if strings.Contains(out, "38;"+code) || strings.Contains(out, "48;"+code) {
				t.Errorf("alert colour %s used", hex)
			}
		}
		for i := range 2 {
			if !strings.Contains(out, p.Hue(term.TreatmentHues[i], "x")[:8]) {
				t.Errorf("treatment %d's colour is not used", i)
			}
		}
	}
	for _, word := range []string{"better", "worse", "recommend", "should switch"} {
		if strings.Contains(strings.ToLower(Render(m, RenderOptions{Width: 120})), word) {
			t.Errorf("verdict word %q", word)
		}
	}
}

func TestChangeListIsCapped(t *testing.T) {
	m := mustModel(t, fixtureRun(t), Options{})
	all := changeList(m)
	if len(all) <= ChangeLimit {
		t.Fatalf("the fixture has only %d changes", len(all))
	}
	out := RenderSummary(m, RenderOptions{Width: 100})
	section := out[strings.Index(out, "WHAT CHANGED"):]
	section = section[:strings.Index(section, "\n\n")]
	lines := strings.Split(section, "\n")
	if len(lines) != 1+ChangeLimit+1 {
		t.Errorf("want heading, %d changes and the overflow note, got %d lines:\n%s", ChangeLimit, len(lines), section)
	}
	want := fmt.Sprintf("  +%d more, see the explorer", len(all)-ChangeLimit)
	if lines[len(lines)-1] != want {
		t.Errorf("overflow note %q, want %q", lines[len(lines)-1], want)
	}
	// The explorer says where the rest are; a higher limit shows them all.
	custom := RenderSummary(m, RenderOptions{Width: 100, ChangeLimit: 3, MoreHint: func(n int) string { return fmt.Sprintf("+%d more on the Calls tab", n) }})
	if !strings.Contains(custom, fmt.Sprintf("+%d more on the Calls tab", len(all)-3)) {
		t.Errorf("custom hint:\n%s", custom)
	}
	full := RenderSummary(m, RenderOptions{Width: 100, ChangeLimit: 1000})
	if strings.Contains(full, "more, see the explorer") {
		t.Error("an overflow note under a list that fits")
	}
	// A failure is listed as an error, with its message.
	var failure bool
	for _, ch := range all {
		failure = failure || (ch.field == "error" && strings.HasPrefix(ch.plain, "timeout"))
	}
	if !failure {
		t.Error("the failed call is not in the change list")
	}
	// ASCII uses ->, Unicode the arrow.
	if !strings.Contains(RenderSummary(m, RenderOptions{Width: 100}), " → ") || !strings.Contains(RenderSummary(m, RenderOptions{Width: 100, ASCII: true}), " -> ") {
		t.Error("arrow")
	}
}

func TestNothingChanged(t *testing.T) {
	out := RenderSummary(mustModel(t, withoutTreatments(demoRun(t)), Options{}), RenderOptions{Width: 80})
	if !strings.Contains(out, "Nothing: no replayed call differs") {
		t.Errorf("no-treatment summary:\n%s", out)
	}
	out = RenderSummary(mustModel(t, nothingReplayed(demoRun(t)), Options{}), RenderOptions{Width: 80})
	if !strings.Contains(out, "Nothing was replayed.") || !strings.Contains(out, "22 calls not replayable: no content captured.") {
		t.Errorf("nothing-replayed summary:\n%s", out)
	}
}

func TestNotReplayableLine(t *testing.T) {
	demo := RenderSummary(mustModel(t, demoRun(t), Options{}), RenderOptions{Width: 120})
	if !strings.Contains(demo, "\n2 calls not replayable, most often: content cut short (1).") {
		t.Errorf("demo:\n%s", demo)
	}
	fixture := RenderSummary(mustModel(t, fixtureRun(t), Options{}), RenderOptions{Width: 120})
	if !strings.Contains(fixture, "3 calls not replayable, most often: earlier tool calls in the conversation (2).") {
		t.Errorf("fixture:\n%s", fixture)
	}
	run := demoRun(t)
	for i := range run.Cases {
		run.Cases[i].NotReplayable = nil
	}
	if out := RenderSummary(mustModel(t, run, Options{}), RenderOptions{Width: 120}); strings.Contains(out, "not replayable") {
		t.Errorf("a not-replayable line with every call replayable:\n%s", out)
	}
}

func TestMissingTrafficHasNoMonthlyRow(t *testing.T) {
	run := demoRun(t)
	run.Input.Traffic = nil
	run.Settings.MonthlyCalls = nil
	m := mustModel(t, run, Options{})
	out := Render(m, RenderOptions{Width: 100})
	if strings.Contains(out, "per month") {
		t.Errorf("a monthly row without traffic:\n%s", out)
	}
	if !strings.Contains(strings.Join(Caveats(m), "\n"), "No monthly projection") {
		t.Error("the caveats do not say why there is no monthly cost")
	}
	calls := 1000.0
	if out := Render(mustModel(t, run, Options{MonthlyCalls: &calls}), RenderOptions{Width: 100}); !strings.Contains(out, "per month") {
		t.Error("no monthly row with --monthly-calls")
	}
}

func TestMissingFloor(t *testing.T) {
	m := mustModel(t, bare(fixtureRun(t)), Options{})
	out := Render(m, RenderOptions{Width: 100})
	if strings.Contains(out, "itself") && !strings.Contains(out, "No noise floor") {
		t.Errorf("an itself column without a floor:\n%s", out)
	}
	head := strings.Split(out, "\n")[2]
	if strings.Contains(head, "itself") || !strings.Contains(head, "production") {
		t.Errorf("column headings %q", head)
	}
	if !strings.Contains(out, "No noise floor in this run") {
		t.Error("the footer does not say there is no floor")
	}
}

func TestRecordedLatencyZeroIsNotShown(t *testing.T) {
	// Without a floor production's column is its recorded calls: an untimed source shows no median.
	c := newCtx(RenderOptions{Width: 100})
	rows := tableRows(c, mustModel(t, untimed(bare(demoRun(t))), Options{}))
	if got := rowByLabel(t, rows, "median latency").prod.text; got != "—" {
		t.Errorf("untimed production latency %q", got)
	}
	rows = tableRows(c, mustModel(t, bare(demoRun(t)), Options{}))
	if got := rowByLabel(t, rows, "median latency").prod.text; got != "932 ms" {
		t.Errorf("recorded production latency %q", got)
	}
}

func TestStoppedRunHeader(t *testing.T) {
	for why, want := range map[string]string{
		"budget":               "The run stopped at its budget, so some calls were not replayed.",
		"interrupted":          "The run was interrupted, so some calls were not replayed.",
		"reservation_exceeded": "The run stopped when a call could have cost more than the budget left",
	} {
		out := Render(mustModel(t, stopped(demoRun(t), why), Options{}), RenderOptions{Width: 140})
		lines := strings.Split(out, "\n")
		if !strings.HasPrefix(lines[1], want) {
			t.Errorf("%s: second line %q", why, lines[1])
		}
	}
	if line := strings.Split(Render(mustModel(t, demoRun(t), Options{}), RenderOptions{Width: 80}), "\n")[1]; line != "" {
		t.Errorf("a finished run's second line is %q", line)
	}
}

func TestHeader(t *testing.T) {
	m := mustModel(t, demoRun(t), Options{})
	for w, want := range map[int]string{
		120: "20 of 22 gpt-4.1-mini calls replayed · traces.jsonl · 28 Oct 2026 · spent $0.013 of $1.00",
		80:  "20 of 22 gpt-4.1-mini calls replayed · traces.jsonl · 28 Oct 2026 · spent $0.013",
		60:  "20 of 22 gpt-4.1-mini calls replayed · spent $0.013",
		40:  "20 of 22 gpt-4.1-mini calls replayed",
	} {
		if got := strings.Split(Render(m, RenderOptions{Width: w}), "\n")[0]; got != want {
			t.Errorf("%d: %q", w, got)
		}
	}
}

func TestZeroTreatmentsAndAllFailed(t *testing.T) {
	none := Render(mustModel(t, withoutTreatments(demoRun(t)), Options{}), RenderOptions{Width: 80})
	if !strings.Contains(none, "production") || !strings.Contains(none, "itself") || strings.Contains(none, "A ") && strings.Contains(none, " A gemini") {
		t.Errorf("no treatments:\n%s", none)
	}
	failed := Render(mustModel(t, allFailed(fixtureRun(t)), Options{}), RenderOptions{Width: 100})
	errors := ""
	for _, l := range strings.Split(failed, "\n") {
		if strings.HasPrefix(l, "  errors") {
			errors = l
		}
	}
	if errors == "" || !strings.Contains(failed, "A  error  timeout: no reply in 60 s") {
		t.Errorf("all failed:\n%s", failed)
	}
}

var sgrOnly = regexp.MustCompile(`^\x1b\[[0-9;]*m$`)

func TestASCIIAndColourEscapes(t *testing.T) {
	m := mustModel(t, fixtureRun(t), Options{Labels: true})
	ascii := Render(m, RenderOptions{Width: 100, ASCII: true})
	if !regexp.MustCompile(`^[\x20-\x7e\n]*$`).MatchString(ascii) {
		t.Error("ASCII mode prints something other than printable ASCII")
	}
	colored := Render(m, RenderOptions{Width: 100, Paint: term.NewPaint(colorprofile.TrueColor)})
	escapes := regexp.MustCompile(`\x1b[^m]*m?`).FindAllString(colored, -1)
	if len(escapes) == 0 {
		t.Error("no colour")
	}
	for _, e := range escapes {
		if !sgrOnly.MatchString(e) {
			t.Errorf("escape %q", e)
		}
	}
	if strings.Contains(Render(m, RenderOptions{Width: 100}), "\x1b") {
		t.Error("an escape without colour")
	}
}

func TestHostileTextIsNeutralized(t *testing.T) {
	m := mustModel(t, hostileRun(t), Options{Labels: true})
	for _, color := range []bool{false, true} {
		for _, ascii := range []bool{false, true} {
			p := term.PlainPaint()
			if color {
				p = term.NewPaint(colorprofile.TrueColor)
			}
			out := Render(m, RenderOptions{Width: 100, Paint: p, ASCII: ascii, Explore: "out\x1b[2J/run.json", ChangeLimit: 100})
			for _, e := range regexp.MustCompile(`\x1b[\s\S]?[^m]*m?`).FindAllString(out, -1) {
				if !sgrOnly.MatchString(e) {
					t.Errorf("color=%v ascii=%v: escape %q", color, ascii, e)
				}
			}
			if !color && strings.Contains(out, "\x1b") {
				t.Error("an escape without colour")
			}
			if regexp.MustCompile("[\x00-\x09\x0b-\x1a\x1c-\x1f\x7f\u0080-\u009f\u202e]").MatchString(out) {
				t.Errorf("color=%v ascii=%v: a control character got through", color, ascii)
			}
			if !strings.Contains(out, "^[[2J") {
				t.Error("captured escapes are not shown as caret notation")
			}
			checkWidth(t, "hostile", out, 100)
		}
	}
	for _, c := range Caveats(m) {
		if strings.ContainsAny(c, "\x1b\x07\r\u009b") {
			t.Errorf("caveat carries a control: %q", c)
		}
	}
}

func TestCopyHasNoEmDash(t *testing.T) {
	for name, run := range edgeRuns(t) {
		if name == "hostile" {
			continue
		}
		m := mustModel(t, run, Options{})
		text := Render(m, RenderOptions{Width: 120}) + strings.Join(Caveats(m), "\n")
		if regexp.MustCompile(`\S—|—\S`).MatchString(text) {
			t.Errorf("%s: an em dash in copy", name)
		}
	}
}

func TestLetters(t *testing.T) {
	for i, want := range map[int]string{0: "A", 1: "B", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA"} {
		if got := Letter(i); got != want {
			t.Errorf("Letter(%d) = %q, want %q", i, got, want)
		}
	}
}

func TestRenderReportBuildsAndRenders(t *testing.T) {
	out, err := RenderReport(demoRun(t), ReportOptions{RenderOptions: RenderOptions{Width: 80}})
	if err != nil {
		t.Fatal(err)
	}
	if out != Render(mustModel(t, demoRun(t), Options{}), RenderOptions{Width: 80}) {
		t.Error("RenderReport differs from Build then Render")
	}
	if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
		t.Error("the report does not end in exactly one newline")
	}
	if summary := RenderSummary(mustModel(t, demoRun(t), Options{}), RenderOptions{Width: 80}); !strings.Contains(out, summary) {
		t.Error("the printed report does not hold the summary block")
	}
}
