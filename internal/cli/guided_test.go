package cli

import (
	"strings"
	"testing"

	"github.com/metergraph/ezswap/internal/flow"
	"github.com/metergraph/ezswap/internal/pricing"
	"github.com/metergraph/ezswap/internal/recommend"
	"github.com/metergraph/ezswap/internal/types"
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

func TestObjectivesFromTheBundledListAndCatalog(t *testing.T) {
	prices, err := pricing.LoadPricing(pricing.LoadPricingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	list := recommend.Bundled()
	// Every model ezswap suggests has a price, or a map would refuse it.
	for _, m := range list.Models {
		if prices.Price(types.PriceQuery{Model: m.ID, InputTokens: typicalInput, OutputTokens: typicalOutput}) == nil {
			t.Errorf("the catalog has no price for %s", m.ID)
		}
	}
	provider, captured := "anthropic", "claude-sonnet-5-5"
	production := productionModel([]types.Case{{Provider: &provider, Model: &captured}, {Provider: &provider, Model: &captured}, {}}, prices)
	if production != "anthropic/claude-sonnet-5.5" {
		t.Fatalf("production resolved to %q", production)
	}
	got := objectives(list, production, prices)
	if len(got) != 3 || got[0].Key != recommend.Cost || got[1].Key != recommend.Quality || got[2].Key != recommend.Latest {
		t.Fatalf("%+v", got)
	}
	cost := func(model string) float64 {
		return *prices.Price(types.PriceQuery{Model: model, InputTokens: typicalInput, OutputTokens: typicalOutput})
	}
	for _, o := range got {
		if len(o.Models) == 0 || len(o.Models) > suggested {
			t.Errorf("%s suggests %v", o.Key, o.Models)
		}
		for _, m := range o.Models {
			switch {
			case m == production:
				t.Errorf("%s suggests production's own model", o.Key)
			case o.Key == recommend.Cost && cost(m) >= cost(production):
				t.Errorf("cost suggests %s, which is not cheaper", m)
			case o.Key == recommend.Quality && cost(m) <= cost(production):
				t.Errorf("quality suggests %s, which is not costlier", m)
			}
		}
	}
	if !got[1].Judge || got[0].Judge || !strings.Contains(got[0].Detail, production) {
		t.Errorf("%+v", got)
	}
	// A model the catalog does not know: the whole list is ranked.
	unknown := objectives(list, "acme/own-model", prices)
	if len(unknown[0].Models) == 0 || strings.Contains(unknown[0].Detail, "less than") {
		t.Errorf("%+v", unknown[0])
	}
}
