package report

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/metergraph/ezswap/demo"
	"github.com/metergraph/ezswap/internal/core"
	"github.com/metergraph/ezswap/internal/types"
)

// The fixtures: the demo's recorded run (synthetic support tickets) and
// testdata/report-run.json, a synthetic run of 24 support-ticket calls
// at example.com with two treatments, a noise floor, a failure and hostile
// strings.

func demoRun(t testing.TB) *types.RunFile {
	t.Helper()
	run, err := demo.Run()
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func fixtureRun(t testing.TB) *types.RunFile {
	t.Helper()
	data, err := os.ReadFile("testdata/report-run.json")
	if err != nil {
		t.Fatal(err)
	}
	var run types.RunFile
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	return &run
}

// bare is the run without its noise floor or traffic.
func bare(run *types.RunFile) *types.RunFile {
	out := *run
	out.Settings.NoiseFloor = false
	out.Replays = nil
	for _, r := range run.Replays {
		if !strings.HasPrefix(r.Arm, "floor_") {
			out.Replays = append(out.Replays, r)
		}
	}
	input := run.Input
	input.Traffic = nil
	out.Input = input
	return &out
}

// untimed is the run with every recorded production latency 0.
func untimed(run *types.RunFile) *types.RunFile {
	out := *run
	out.Cases = make([]types.Case, len(run.Cases))
	copy(out.Cases, run.Cases)
	for i := range out.Cases {
		out.Cases[i].Production.LatencyMs = new(float64)
	}
	return &out
}

func mustModel(t testing.TB, run *types.RunFile, options Options) *Model {
	t.Helper()
	m, err := Build(run, options)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestModelMatchesFrozenFixtures compares the model, field by field, with
// testdata/model: frozen test data, written by the earlier TypeScript
// implementation's report model for the same runs. Do not regenerate them
// from this code; a difference is a change in what the report says.
func TestModelMatchesFrozenFixtures(t *testing.T) {
	thousand := 1000.0
	cases := []struct {
		name    string
		run     *types.RunFile
		options Options
	}{
		{"demo", demoRun(t), Options{}},
		{"demo-labels", demoRun(t), Options{Labels: true}},
		{"fixture", fixtureRun(t), Options{}},
		{"fixture-labels-monthly", fixtureRun(t), Options{Labels: true, MonthlyCalls: &thousand}},
		{"fixture-bare", bare(fixtureRun(t)), Options{Labels: true}},
		{"demo-untimed", untimed(demoRun(t)), Options{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/model/" + tc.name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var want any
			if err := json.Unmarshal(data, &want); err != nil {
				t.Fatal(err)
			}
			got := roundTrip(t, mustModel(t, tc.run, tc.options))
			diff(t, "model", want, got)
		})
	}
}

func roundTrip(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// diff reports the first few paths where want and got differ.
func diff(t *testing.T, path string, want, got any) {
	t.Helper()
	var found []string
	var walk func(path string, want, got any)
	walk = func(path string, want, got any) {
		if len(found) >= 10 {
			return
		}
		switch w := want.(type) {
		case map[string]any:
			g, ok := got.(map[string]any)
			if !ok {
				found = append(found, fmt.Sprintf("%s: want object, got %v", path, got))
				return
			}
			for k, wv := range w {
				gv, ok := g[k]
				if !ok {
					found = append(found, fmt.Sprintf("%s.%s: missing (want %v)", path, k, wv))
					continue
				}
				walk(path+"."+k, wv, gv)
			}
			for k := range g {
				if _, ok := w[k]; !ok {
					found = append(found, fmt.Sprintf("%s.%s: unexpected %v", path, k, g[k]))
				}
			}
		case []any:
			g, ok := got.([]any)
			if !ok || len(g) != len(w) {
				found = append(found, fmt.Sprintf("%s: want %d items, got %v", path, len(w), got))
				return
			}
			for i := range w {
				walk(fmt.Sprintf("%s[%d]", path, i), w[i], g[i])
			}
		default:
			if !reflect.DeepEqual(want, got) {
				found = append(found, fmt.Sprintf("%s: want %#v, got %#v", path, want, got))
			}
		}
	}
	walk(path, want, got)
	for _, f := range found {
		t.Error(f)
	}
}

// Report model behaviour.

func TestLatencyReadAgainstFloorAndNoVerdict(t *testing.T) {
	m := mustModel(t, fixtureRun(t), Options{Labels: true})
	var latency *Row
	for i := range m.Rows {
		if m.Rows[i].Key == "latency_ms_p50" {
			latency = &m.Rows[i]
		}
	}
	if latency == nil {
		t.Fatal("no latency row")
	}
	for _, c := range latency.Cells {
		if c.Delta == nil || c.Floor == nil {
			t.Errorf("latency cell without a delta or floor: %+v", c)
		}
	}
	if !strings.HasSuffix(m.Headline, "?") {
		t.Errorf("headline %q is not a question", m.Headline)
	}
	rendered := Render(m, RenderOptions{Width: 120}) + strings.Join(Caveats(m), "\n")
	m.View = nil
	data, _ := json.Marshal(m)
	copyText := string(data) + rendered
	if regexp.MustCompile(`(?i)\b(you should switch|we recommend|recommended model|switch now)\b`).MatchString(copyText) {
		t.Error("the report states a verdict")
	}
	safe := regexp.MustCompile(`(?i)\b(safe|good enough)\b`)
	negated := regexp.MustCompile(`(?i)\b(neither|nothing|not|never)\b`)
	for _, sentence := range regexp.MustCompile(`[.!?]\s+`).Split(copyText, -1) {
		if safe.MatchString(sentence) && !negated.MatchString(sentence) {
			t.Errorf("sentence says safe without a negation: %q", sentence)
		}
	}
	// No em dash in copy; a bare one marks a missing value in a cell.
	forEachString(m, func(s string) {
		if strings.Contains(s, "—") && strings.TrimSpace(s) != "—" {
			t.Errorf("em dash in copy: %q", s)
		}
	})
}

// forEachString calls f on every string in the model (as JSON), skipping captured outputs.
func forEachString(m *Model, f func(string)) {
	data, _ := json.Marshal(m)
	var v any
	_ = json.Unmarshal(data, &v)
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			f(t)
		case []any:
			for _, x := range t {
				walk(x)
			}
		case map[string]any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	walk(v)
}

func TestReportNamesEndpointNeverGateway(t *testing.T) {
	m := mustModel(t, fixtureRun(t), Options{Labels: true})
	var out strings.Builder
	for _, w := range []int{80, 120} {
		out.WriteString(Render(m, RenderOptions{Width: w}))
	}
	view := m.View
	m.View = nil
	data, _ := json.Marshal(m)
	out.Write(data)
	if strings.Contains(strings.ToLower(out.String()), "gateway") {
		t.Error("the report mentions a gateway")
	}
	for _, e := range core.Equivalence(view, core.ViaEndpoint).Treatments {
		if !strings.Contains(e.Latency.Sentence, "same endpoint") {
			t.Errorf("latency sentence %q", e.Latency.Sentence)
		}
	}
}

func TestLabelsAreOptIn(t *testing.T) {
	off := mustModel(t, fixtureRun(t), Options{})
	for _, r := range off.Rows {
		for _, c := range r.Cells {
			if c.Label != nil {
				t.Fatalf("label without --labels on %s", r.Key)
			}
		}
	}
	on := mustModel(t, fixtureRun(t), Options{Labels: true})
	found := false
	for _, r := range on.Rows {
		for _, c := range r.Cells {
			if c.Label != nil && (c.Label.Tone == "within" || c.Label.Tone == "beyond") {
				found = true
			}
		}
	}
	if !found {
		t.Error("no within or beyond label with labels on")
	}
	// The summary reads the labels either way.
	if off.Noise == nil {
		t.Error("the model carries no noise labels for the summary")
	}
}

func TestRunWithoutFloorOrTrafficStillRenders(t *testing.T) {
	run := bare(fixtureRun(t))
	m := mustModel(t, run, Options{Labels: true})
	if m.Floor != nil || m.Labels {
		t.Error("floor or labels without a floor")
	}
	if m.Rows[0].Key != "monthly_cost_usd" || m.Rows[0].Production != "—" {
		t.Errorf("first row %s %q", m.Rows[0].Key, m.Rows[0].Production)
	}
	out := Render(m, RenderOptions{Width: 80, ASCII: true})
	if !strings.Contains(out, "production") {
		t.Error("no production column")
	}
	if strings.Contains(out, "itself") {
		t.Error("an itself column without a floor")
	}
	caveats := strings.Join(Caveats(m), "\n")
	if !strings.Contains(caveats, "no noise floor") || !strings.Contains(caveats, "--monthly-calls N") {
		t.Errorf("caveats: %s", caveats)
	}
}

func TestMonthlyProjectionUsesTrafficOrVolume(t *testing.T) {
	m := mustModel(t, fixtureRun(t), Options{})
	if s := strOr(m.Rows[0].Sublabel, ""); !strings.Contains(s, "18,400 calls in 30 days") {
		t.Errorf("sublabel %q", s)
	}
	thousand := 1000.0
	explicit := mustModel(t, fixtureRun(t), Options{MonthlyCalls: &thousand})
	if s := strOr(explicit.Rows[0].Sublabel, ""); !strings.HasPrefix(s, "1,000 calls in 30 days") {
		t.Errorf("sublabel %q", s)
	}
}

func TestNotReplayableCountsReasons(t *testing.T) {
	m := mustModel(t, fixtureRun(t), Options{})
	var got [][2]any
	for _, r := range m.NotReplayable {
		got = append(got, [2]any{r.Reason, r.Count})
		if len(r.Fix) <= 20 || strings.Contains(r.Fix, "—") {
			t.Errorf("fix %q", r.Fix)
		}
	}
	want := [][2]any{{"tool_history", 2}, {"content_truncated", 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("not replayable %v", got)
	}
}

func TestCaseFilterMarksDifferingTreatments(t *testing.T) {
	m := mustModel(t, fixtureRun(t), Options{})
	var failedDiffers, someSame bool
	for _, c := range m.Cases {
		for _, a := range c.Arms {
			if a.Status == "failed" {
				failedDiffers = c.Differs
			}
		}
		if c.Status == "replayed" && !c.Differs {
			someSame = true
		}
		if c.Status == "not_replayable" && (c.Differs || len(c.Arms) != 0) {
			t.Errorf("not-replayable case %s differs or has arms", c.ID)
		}
	}
	if !failedDiffers {
		t.Error("a failure does not count as differing")
	}
	if !someSame {
		t.Error("every replayed case differs")
	}
}

func TestRecordedLatencyZeroIsNotRecorded(t *testing.T) {
	if m := mustModel(t, demoRun(t), Options{}); m.RecordedLatency == nil || *m.RecordedLatency != "932 ms" {
		t.Errorf("recorded latency %v", m.RecordedLatency)
	}
	m := mustModel(t, untimed(demoRun(t)), Options{})
	if m.RecordedLatency != nil {
		t.Errorf("recorded latency %q from a source that timed nothing", *m.RecordedLatency)
	}
	caveats := strings.Join(Caveats(m), " ")
	if !strings.Contains(caveats, "production here is its model replayed from this machine.") || strings.Contains(caveats, "Its recorded median") {
		t.Errorf("caveats: %s", caveats)
	}
	if !strings.Contains(strings.Join(Caveats(mustModel(t, demoRun(t), Options{})), " "), "Its recorded median (932 ms) is not compared") {
		t.Error("the recorded median is not stated")
	}
}
