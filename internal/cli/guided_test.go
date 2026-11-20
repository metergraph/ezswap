package cli

import (
	"strings"
	"testing"

	"github.com/metergraph/ezswap/internal/flow"
)

func TestPlanChoiceBecomesMapOptions(t *testing.T) {
	base := values{strings: map[string]string{"budget": "3"}, lists: map[string][]string{"only": {"old"}}, bools: map[string]bool{}}
	all := []string{"route-ticket", "draft-reply"}
	v := withChoice(base, flow.Choice{Models: []string{"a/one", "b/two"}, Endpoint: "vercel", PerPart: 40, Parts: all, Judge: true}, all)
	if v.strings["models"] != "a/one,b/two" || v.strings["endpoint"] != "vercel" || v.strings["per-part"] != "40" || !v.bools["judge"] || v.strings["budget"] != "3" {
		t.Fatalf("%+v", v)
	}
	if _, only := v.lists["only"]; only {
		t.Error("every part chosen, yet --only is set")
	}
	some := withChoice(base, flow.Choice{Models: []string{"a/one"}, Endpoint: "openai", PerPart: 20, Parts: []string{"draft-reply"}}, all)
	if strings.Join(some.lists["only"], ",") != "draft-reply" || some.bools["judge"] {
		t.Fatalf("%+v", some)
	}
	// The base is not changed.
	if base.strings["models"] != "" || strings.Join(base.lists["only"], ",") != "old" {
		t.Errorf("base changed: %+v", base)
	}
	if endpointOf(values{strings: map[string]string{"endpoint": "https://example.com/v1"}}) != "openrouter" || endpointOf(v) != "vercel" {
		t.Error("endpointOf")
	}
	if perPartOf(values{strings: map[string]string{"per-part": "500"}}) != 20 || perPartOf(v) != 40 {
		t.Error("perPartOf")
	}
}

func TestGuidedStepsAreNumberedOfThree(t *testing.T) {
	for i, step := range []string{stepRecord, stepAnalyze, stepMap} {
		if want := "step " + string(rune('1'+i)) + " of 3"; step != want {
			t.Errorf("step %d is %q, want %q", i+1, step, want)
		}
	}
}

func TestNoCommandWithoutATerminalPrintsHelp(t *testing.T) {
	out := cli(t, nil, nil, nil)
	if out.code != ExitUsage || out.stdout != Help {
		t.Fatalf("%d %q", out.code, out.stdout[:min(80, len(out.stdout))])
	}
	// --json never starts the guided flow, even on a terminal.
	asJSON := cli(t, args("--json"), func(o *IO) { o.StdinTTY, o.StdoutTTY = true, true }, nil)
	if asJSON.code != ExitUsage || !strings.Contains(asJSON.stdout, `"command":"help"`) {
		t.Fatalf("%d %s", asJSON.code, asJSON.stdout)
	}
}
