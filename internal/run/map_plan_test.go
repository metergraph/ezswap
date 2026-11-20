package run

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/metergraph/ezswap/internal/parts"
	"github.com/metergraph/ezswap/internal/types"
)

const mapTraces = "../../demo/map-traces.jsonl"

func mapPlan(t *testing.T, file string, edit func(*PlanOptions)) *RunPlan {
	t.Helper()
	options := baseOptions(t, file)
	options.Map = true
	if edit != nil {
		edit(&options)
	}
	p, err := PlanRun(options)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMapPlanSamplesPerPartAndOrdersCasesPartByPart(t *testing.T) {
	p := mapPlan(t, mapTraces, func(o *PlanOptions) { o.PerPart = 12 })
	if len(p.Parts) != 5 || len(p.AllParts) != 5 {
		t.Fatalf("%d parts", len(p.Parts))
	}
	if *p.Settings.PerPart != 12 || p.Settings.Sample != 12 || p.Settings.BudgetUSD != DefaultMapBudgetUSD {
		t.Fatalf("settings %+v", p.Settings)
	}
	total := 0
	for i, part := range p.Parts {
		want := 12
		if part.Available == 6 {
			want = 6
			if part.Reason != parts.ReasonTooFew || i != len(p.Parts)-1 {
				t.Fatalf("the small part comes last and says why it gets no label: %+v", part)
			}
		}
		if part.Sampled != want {
			t.Fatalf("%s sampled %d", part.Name, part.Sampled)
		}
		for _, kase := range p.Cases[total : total+part.Sampled] {
			if kase.PartName != part.Name || kase.NotReplayable != nil {
				t.Fatalf("case %s is in %q, expected %q", kase.ID, kase.PartName, part.Name)
			}
		}
		total += part.Sampled
	}
	if total != len(p.Cases) || total != 54 {
		t.Fatalf("%d cases", total)
	}
}

func TestMapPlanEstimateIsOneCallAndPartsAddUp(t *testing.T) {
	p := mapPlan(t, mapTraces, nil)
	if p.Estimate.Replayable != 86 {
		t.Fatalf("replayable %d", p.Estimate.Replayable)
	}
	sum := 0.0
	for _, part := range p.Parts {
		if part.Estimate.Replayable != part.Sampled || part.Estimate.ExpectedUSD <= 0 {
			t.Fatalf("%s estimate %+v", part.Name, part.Estimate)
		}
		sum += part.Estimate.ExpectedUSD
	}
	if sum < p.Estimate.ExpectedUSD-1e-9 || sum > p.Estimate.ExpectedUSD*1.05 {
		t.Fatalf("parts sum to %v, the plan to %v", sum, p.Estimate.ExpectedUSD)
	}
	if math.IsNaN(sum) {
		t.Fatal("NaN")
	}
}

func TestMapPlanComparedPathsAndReasons(t *testing.T) {
	p := mapPlan(t, mapTraces, func(o *PlanOptions) {
		o.TrackedFields = []string{"$.order_id"}
		o.IgnoredFields = []string{"$.sku"}
	})
	byName := map[string]PartPlan{}
	for _, part := range p.Parts {
		byName[part.Name] = part
	}
	if got := strings.Join(byName["extract-order"].Paths, " "); got != "$.order_id $.quantity" {
		t.Fatalf("tracked first, ignored left out: %q", got)
	}
	if got := strings.Join(byName["route-to-team"].Paths, " "); got != "$.order_id $.team $.urgent" && got != "$.order_id $.urgent $.team" {
		t.Fatalf("route-to-team: %q", got)
	}
	free := 0
	for _, part := range p.Parts {
		if part.Reason == parts.ReasonFreeText {
			free++
		}
	}
	if free != 1 {
		t.Fatalf("one free-text part, got %d", free)
	}
	if strings.Join(p.Settings.IgnoredFields, " ") != "$.sku" {
		t.Fatalf("ignored fields are stored: %v", p.Settings.IgnoredFields)
	}
}

func TestMapPlanOnlyNamesAndRename(t *testing.T) {
	p := mapPlan(t, mapTraces, func(o *PlanOptions) {
		o.Only = []string{"orders", "name:route-to-team"}
		o.Rename = []string{"extract-order=orders"}
	})
	if len(p.Parts) != 2 || p.Parts[0].Name != "orders" || p.Parts[1].Name != "route-to-team" {
		t.Fatalf("%+v", p.Parts)
	}
	if len(p.AllParts) != 5 || len(p.Cases) != 40 {
		t.Fatalf("every part is kept for the names file: %d parts, %d cases", len(p.AllParts), len(p.Cases))
	}
	options := baseOptions(t, mapTraces)
	options.Map, options.Only = true, []string{"nope"}
	var usage *parts.UsageError
	if _, err := PlanRun(options); !errors.As(err, &usage) {
		t.Fatalf("unknown --only: %v", err)
	}
	options.Only, options.PerPart = nil, 5
	var planError *PlanError
	if _, err := PlanRun(options); !errors.As(err, &planError) {
		t.Fatalf("--per-part below the minimum: %v", err)
	}
}

func TestToolsNotOfferedAndUnreplayableCasesAreLeftOutAndCounted(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "traces.jsonl")
	var lines []string
	for i := 0; i < 12; i++ {
		lines = append(lines, `{"id":"ok-`+string(rune('a'+i))+`","part":"plain","request":{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hi `+string(rune('a'+i))+`"}]},"response":{"choices":[{"message":{"role":"assistant","content":"{\"a\":1}"},"finish_reason":"stop"}]}}`)
	}
	lines = append(lines,
		`{"id":"tools","part":"plain","request":{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Weather?"}],"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}},{"type":"function","function":{"name":"news","parameters":{"type":"object"}}}]},"response":{"choices":[{"message":{"role":"assistant","content":"Sunny."},"finish_reason":"stop"}]}}`,
		`{"id":"cut","part":"plain","truncated":true,"request":{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hi"}]},"response":{"choices":[{"message":{"role":"assistant","content":"x"}}]}}`,
	)
	os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	p := mapPlan(t, file, nil)
	if len(p.Cases) != 12 || p.LeftOut["tools_not_offered"] != 1 || p.LeftOut[string(types.ContentTruncated)] != 1 {
		t.Fatalf("%d cases, left out %v", len(p.Cases), p.LeftOut)
	}
	if p.File.Calls != 14 {
		t.Fatalf("the file's own count is unchanged: %d", p.File.Calls)
	}
}

func TestOrdinaryPlanIsUnchangedByParts(t *testing.T) {
	p := plan(t, 5, nil)
	if len(p.Parts) != 0 || p.Settings.PerPart != nil || p.Settings.BudgetUSD != DefaultBudgetUSD {
		t.Fatalf("%+v", p.Settings)
	}
	for _, kase := range p.Cases {
		if kase.PartKey != "" || kase.PartName != "" {
			t.Fatal("an ordinary run does not assign parts")
		}
	}
}

// A local endpoint takes the model as production named it. The catalog's
// creator/model id ("openai/qwen2.5:3b") is not a name a local server knows.
func TestFloorOnALocalEndpointSendsTheCapturedModelName(t *testing.T) {
	file := filepath.Join(t.TempDir(), "traces.jsonl")
	os.WriteFile(file, []byte(`{"id":"a","provider":"openai","request":{"model":"qwen2.5:3b","messages":[{"role":"user","content":"Hi"}]},"response":{"choices":[{"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}]}}`+"\n"), 0o644)
	options := baseOptions(t, file)
	options.Treatments = []TreatmentInput{{Model: "qwen2.5-coder:7b", Endpoint: "ollama"}}
	options.FloorEndpoint = "ollama"
	options.IsFreeEndpoint = func(string) bool { return true }
	p, err := PlanRun(options)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var models []string
	provider := fakeModels{name: "ollama", seen: func(model string) {
		mu.Lock()
		models = append(models, model)
		mu.Unlock()
	}}
	if _, err := ExecuteRun(context.Background(), p, ExecuteOptions{
		Providers: map[string]types.Provider{"ollama": provider}, Pricing: options.Pricing, Now: now, Concurrency: 1,
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(models)
	if strings.Join(models, " ") != "qwen2.5-coder:7b qwen2.5:3b qwen2.5:3b" {
		t.Fatalf("models sent: %v", models)
	}
}

type fakeModels struct {
	name string
	seen func(model string)
}

func (f fakeModels) Name() string { return f.name }

func (f fakeModels) Complete(_ context.Context, call types.ReplayCall) types.ReplyOutcome {
	f.seen(call.Model)
	text, finish := "Hello", "stop"
	return types.ReplyOutcome{Status: "ok", Output: &text, FinishReason: &finish, Flags: []types.ReplayFlag{}}
}
