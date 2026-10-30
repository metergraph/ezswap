package pricing

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/metergraph/replay/internal/types"
)

type estimateFixture struct {
	At         string            `json:"at"`
	Cases      []types.Case      `json:"cases"`
	Treatments []types.Treatment `json:"treatments"`
	Expected   struct {
		ExpectedUSD   string `json:"expectedUsd"`
		UpperBoundUSD string `json:"upperBoundUsd"`
		PerArm        map[string]struct {
			ExpectedUSD   float64 `json:"expectedUsd"`
			UpperBoundUSD string  `json:"upperBoundUsd"`
		} `json:"perArm"`
		Floor struct {
			Models                 []string `json:"models"`
			ExpectedUSD            float64  `json:"expectedUsd"`
			UpperBoundUSDPerReplay string   `json:"upperBoundUsdPerReplay"`
		} `json:"floor"`
		Calls []struct {
			Arm           string   `json:"arm"`
			Case          string   `json:"case"`
			Model         string   `json:"model"`
			MaxTokens     float64  `json:"maxTokens"`
			InputBound    *float64 `json:"inputBound"`
			UpperBoundUSD string   `json:"upperBoundUsd"`
		} `json:"calls"`
	} `json:"expected"`
}

func number(t *testing.T, text string) float64 {
	t.Helper()
	n, err := strconv.ParseFloat(text, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func loadEstimateFixture(t *testing.T) estimateFixture {
	var fixture estimateFixture
	readJSON(t, "../../conformance/estimate.json", &fixture)
	return fixture
}

func productionInput(item types.Case) *float64 {
	if item.Production.Usage == nil {
		return nil
	}
	return item.Production.Usage.InputTokens
}

func TestEstimateConformanceTotals(t *testing.T) {
	fixture := loadEstimateFixture(t)
	pricing := mustLoad(t, LoadPricingOptions{})
	estimate := EstimateRun(fixture.Cases, fixture.Treatments, EstimateOptions{NoiseFloor: true, At: fixture.At}, pricing)
	if estimate.Replayable != 4 {
		t.Errorf("replayable %d", estimate.Replayable)
	}
	if !reflect.DeepEqual(estimate.NotReplayable, map[string]int{"tool_history": 1}) {
		t.Errorf("notReplayable %v", estimate.NotReplayable)
	}
	if estimate.ExpectedUSD != number(t, fixture.Expected.ExpectedUSD) {
		t.Errorf("expectedUsd %v, want %s", estimate.ExpectedUSD, fixture.Expected.ExpectedUSD)
	}
	if estimate.UpperBoundUSD != number(t, fixture.Expected.UpperBoundUSD) {
		t.Errorf("upperBoundUsd %v, want %s", estimate.UpperBoundUSD, fixture.Expected.UpperBoundUSD)
	}
	for arm, expected := range fixture.Expected.PerArm {
		got, ok := estimate.PerArm[arm]
		if !ok || got.ExpectedUSD != expected.ExpectedUSD || got.UpperBoundUSD != number(t, expected.UpperBoundUSD) {
			t.Errorf("%s: %+v, want %+v", arm, got, expected)
		}
	}
	for _, arm := range []string{types.ArmFloorA1, types.ArmFloorA2} {
		got := estimate.PerArm[arm]
		if got.ExpectedUSD != fixture.Expected.Floor.ExpectedUSD/2 || got.UpperBoundUSD != number(t, fixture.Expected.Floor.UpperBoundUSDPerReplay) {
			t.Errorf("%s: %+v", arm, got)
		}
	}
	if len(estimate.Unpriced) != 0 || estimate.Unpriced == nil {
		t.Errorf("unpriced %#v", estimate.Unpriced)
	}
}

func TestEstimateConformanceFloorModels(t *testing.T) {
	fixture := loadEstimateFixture(t)
	pricing := mustLoad(t, LoadPricingOptions{})
	var models []string
	for _, item := range fixture.Cases {
		if item.NotReplayable == nil {
			models = append(models, strOrNil(FloorModel(item, pricing, fixture.At)))
		}
	}
	if !reflect.DeepEqual(models, fixture.Expected.Floor.Models) {
		t.Errorf("floor models %v, want %v", models, fixture.Expected.Floor.Models)
	}
}

func TestEstimateConformanceCallUpperBounds(t *testing.T) {
	fixture := loadEstimateFixture(t)
	pricing := mustLoad(t, LoadPricingOptions{})
	byID := map[string]types.Case{}
	floorModels := map[string]string{}
	index := 0
	for _, item := range fixture.Cases {
		byID[item.ID] = item
		if item.NotReplayable == nil {
			floorModels[item.ID] = fixture.Expected.Floor.Models[index]
			index++
		}
	}
	for _, expected := range fixture.Expected.Calls {
		label := expected.Arm + "/" + expected.Case
		item := byID[expected.Case]
		var call types.ReplayCall
		if expected.Arm == "floor" {
			call = FloorCall(item, floorModels[item.ID])
		} else {
			for _, treatment := range fixture.Treatments {
				if treatment.ID == expected.Arm {
					call = TreatmentCall(item, treatment)
				}
			}
		}
		if call.Model != expected.Model {
			t.Errorf("%s: model %s, want %s", label, call.Model, expected.Model)
		}
		if call.MaxTokens != expected.MaxTokens {
			t.Errorf("%s: maxTokens %v, want %v", label, call.MaxTokens, expected.MaxTokens)
		}
		if expected.InputBound != nil {
			if got := ReservationInputTokens(call, productionInput(item)); got != *expected.InputBound {
				t.Errorf("%s: input bound %v, want %v", label, got, *expected.InputBound)
			}
		}
		got := UpperBoundUSD(call, pricing, productionInput(item), fixture.At)
		if got == nil || *got != number(t, expected.UpperBoundUSD) {
			t.Errorf("%s: upper bound %s, want %s", label, costOrNil(got), expected.UpperBoundUSD)
		}
	}
}

// ---------------------------------------------------------------------------

func f(v float64) *float64 { return &v }

func makeCase(edit func(*types.Case)) types.Case {
	item := types.Case{
		ID:       "case-1",
		Source:   "replay-jsonl",
		Provider: str("openai"),
		Model:    str("gpt-6-luna"),
		Messages: []types.Message{{Role: "user", Content: "hello"}},
		Production: types.CaseOutput{
			Output:       str("hi"),
			FinishReason: str("stop"),
			Usage:        &types.Usage{InputTokens: f(1000), OutputTokens: f(100)},
		},
		Fidelity: "full",
		Missing:  []string{},
	}
	if edit != nil {
		edit(&item)
	}
	return item
}

func makeTreatment(edit func(*types.Treatment)) types.Treatment {
	treatment := types.Treatment{ID: "t1", Label: "t1", Model: "openai/gpt-6-luna", Endpoint: "vercel"}
	if edit != nil {
		edit(&treatment)
	}
	return treatment
}

func TestCapSent(t *testing.T) {
	cases := []struct {
		treatment, production *float64
		want                  float64
	}{
		{f(500), f(2000), 500},
		{nil, f(2000), 2000},
		{nil, nil, 4096},
		{f(0), f(0), 4096},
	}
	for _, c := range cases {
		if got := CapSent(c.treatment, c.production); got != c.want {
			t.Errorf("CapSent = %v, want %v", got, c.want)
		}
	}
}

func TestAssumedOutputTokens(t *testing.T) {
	cases := []struct {
		cap        float64
		production *float64
		want       float64
	}{
		{4096, f(100), 2048},
		{4096, f(3000), 3000},
		{1000, f(3000), 1000},
		{600, nil, 512},
		{300, nil, 300},
		{4095, f(0), 2047},
	}
	for _, c := range cases {
		if got := AssumedOutputTokens(c.cap, c.production); got != c.want {
			t.Errorf("AssumedOutputTokens(%v) = %v, want %v", c.cap, got, c.want)
		}
	}
}

func TestInputBoundCountsBytesAndFraming(t *testing.T) {
	item := makeCase(func(c *types.Case) {
		c.System = str("abc")
		c.Messages = []types.Message{{Role: "user", Content: "é"}}
	})
	call := TreatmentCall(item, makeTreatment(nil))
	// 3 + 2 bytes, 16 * (1 + 1) framing, 64 base.
	if got := ReservationInputTokens(call, nil); got != 3+2+32+64 {
		t.Errorf("bound %v", got)
	}
	if got := ReservationInputTokens(call, f(5000)); got != 5000 {
		t.Errorf("floored bound %v", got)
	}
}

func TestPythonJSON(t *testing.T) {
	cases := []struct {
		value any
		want  string
	}{
		{map[string]any{"a": []any{1.0, 2.5, "é"}, "b": nil, "c": true}, "{\"a\": [1, 2.5, \"\\u00e9\"], \"b\": null, \"c\": true}"},
		{map[string]any{}, "{}"},
		{[]any{}, "[]"},
		{"line\nbreak\t\"q\"\\", `"line\nbreak\t\"q\"\\"`},
		{"\u0001", `"\u0001"`},
		{"😀", "\"\\ud83d\\ude00\""},
		{0.00001, "1e-05"},
		{1e21, "1e+21"},
		{0.5, "0.5"},
	}
	for _, c := range cases {
		if got := PythonJSON(c.value); got != c.want {
			t.Errorf("PythonJSON(%#v) = %s, want %s", c.value, got, c.want)
		}
	}
}

func TestPythonJSONReadsRawJSONInItsOwnOrder(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		// Source spacing does not matter; Python's separators do.
		{"{\n  \"zeta\":1,\"alpha\" : [true,null],\"mid\":\"\u00e9\"\n}", "{\"zeta\": 1, \"alpha\": [true, null], \"mid\": \"\\u00e9\"}"},
		// JSON.parse keeps a repeated key once, where it first appeared, with its last value.
		{`{"a": 1, "b": 2, "a": 3}`, `{"a": 3, "b": 2}`},
		{`null`, `null`},
	}
	for _, c := range cases {
		if got := PythonJSON(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("PythonJSON(%s) = %s, want %s", c.raw, got, c.want)
		}
	}
	if got := PythonJSON(json.RawMessage(nil)); got != "null" {
		t.Errorf("nil raw: %s", got)
	}
}

func TestReservationSkipsNullResponseFormat(t *testing.T) {
	call := TreatmentCall(makeCase(nil), makeTreatment(nil))
	without := ReservationInputTokens(call, nil)
	call.ResponseFormat = json.RawMessage(`null`)
	if got := ReservationInputTokens(call, nil); got != without {
		t.Errorf("null response format counted: %v vs %v", got, without)
	}
	call.ResponseFormat = json.RawMessage(`{"type":"json_object"}`)
	if got := ReservationInputTokens(call, nil); got != without+float64(len(`{"type": "json_object"}`)) {
		t.Errorf("json_object bound %v", got)
	}
}

func TestUnpriceableTreatmentIsReported(t *testing.T) {
	pricing := mustLoad(t, LoadPricingOptions{})
	unknown := makeTreatment(func(tr *types.Treatment) { tr.Model = "example/unknown" })
	estimate := EstimateRun([]types.Case{makeCase(nil)}, []types.Treatment{unknown}, EstimateOptions{At: "2026-10-20"}, pricing)
	if !reflect.DeepEqual(estimate.Unpriced, []string{"example/unknown"}) {
		t.Errorf("unpriced %v", estimate.Unpriced)
	}
	if estimate.PerArm["t1"] != (types.ArmEstimate{}) {
		t.Errorf("t1 %+v", estimate.PerArm["t1"])
	}
	if got := UpperBoundUSD(TreatmentCall(makeCase(nil), unknown), pricing, nil, ""); got != nil {
		t.Errorf("bound %s", costOrNil(got))
	}
}

func TestUnpriceableProductionModelLeavesFloorUnpriced(t *testing.T) {
	pricing := mustLoad(t, LoadPricingOptions{})
	item := makeCase(func(c *types.Case) { c.Provider, c.Model = str("example"), str("mystery") })
	estimate := EstimateRun([]types.Case{item}, nil, EstimateOptions{NoiseFloor: true, At: "2026-10-20"}, pricing)
	if !reflect.DeepEqual(estimate.Unpriced, []string{"example/mystery"}) || estimate.ExpectedUSD != 0 {
		t.Errorf("estimate %+v", estimate)
	}
}

func TestCaseWithoutProductionModelReportedForFloor(t *testing.T) {
	pricing := mustLoad(t, LoadPricingOptions{})
	item := makeCase(func(c *types.Case) { c.Provider, c.Model = nil, nil })
	estimate := EstimateRun([]types.Case{item}, nil, EstimateOptions{NoiseFloor: true, At: "2026-10-20"}, pricing)
	if !reflect.DeepEqual(estimate.Unpriced, []string{"(production model not recorded)"}) {
		t.Errorf("unpriced %v", estimate.Unpriced)
	}
}

func TestOllamaPresetIsFree(t *testing.T) {
	pricing := mustLoad(t, LoadPricingOptions{})
	cases := []types.Case{makeCase(nil)}
	free := EstimateRun(cases, []types.Treatment{makeTreatment(func(tr *types.Treatment) { tr.Endpoint, tr.Model = "ollama", "llama3.2" })},
		EstimateOptions{At: "2026-10-20"}, pricing)
	if free.PerArm["t1"] != (types.ArmEstimate{}) || len(free.Unpriced) != 0 {
		t.Errorf("free %+v", free)
	}
	custom := EstimateRun(cases, []types.Treatment{makeTreatment(func(tr *types.Treatment) { tr.Endpoint, tr.Model = "localhost:8000", "llama3.2" })},
		EstimateOptions{At: "2026-10-20", IsFree: func(tr types.Treatment) bool { return strings.HasPrefix(tr.Endpoint, "localhost") }}, pricing)
	if len(custom.Unpriced) != 0 {
		t.Errorf("custom %+v", custom)
	}
}

func TestMarginRoundsEachArmUp(t *testing.T) {
	pricing := mustLoad(t, LoadPricingOptions{})
	// 1000 in, 2048 out on gpt-6-luna: 0.0001 + 0.001024 = 0.001124, x1.25 = 0.001405 -> 0.0015.
	estimate := EstimateRun([]types.Case{makeCase(nil)}, []types.Treatment{makeTreatment(nil)}, EstimateOptions{At: "2026-10-20"}, pricing)
	if estimate.PerArm["t1"].ExpectedUSD != 0.0015 || estimate.ExpectedUSD != 0.0015 {
		t.Errorf("estimate %+v", estimate)
	}
}

func TestNotReplayableCountedByReason(t *testing.T) {
	pricing := mustLoad(t, LoadPricingOptions{})
	reason := func(id string, r types.NotReplayableReason) types.Case {
		return makeCase(func(c *types.Case) { c.ID, c.NotReplayable = id, &r })
	}
	cases := []types.Case{reason("a", types.ContentTruncated), reason("b", types.ContentTruncated), reason("c", types.NoMessages)}
	estimate := EstimateRun(cases, []types.Treatment{makeTreatment(nil)}, EstimateOptions{NoiseFloor: true, At: "2026-10-20"}, pricing)
	if estimate.Replayable != 0 || !reflect.DeepEqual(estimate.NotReplayable, map[string]int{"content_truncated": 2, "no_messages": 1}) ||
		estimate.ExpectedUSD != 0 || estimate.UpperBoundUSD != 0 {
		t.Errorf("estimate %+v", estimate)
	}
}

type plainPricing struct{}

func (plainPricing) CatalogVersion() string          { return "x" }
func (plainPricing) Price(types.PriceQuery) *float64 { return f(1) }
func (plainPricing) Knows(string) bool               { return true }

func TestFloorModelFallsBackWithoutResolver(t *testing.T) {
	cases := []struct {
		provider *string
		model    string
		want     string
	}{
		{str("Anthropic"), "claude-x", "anthropic/claude-x"},
		{nil, "claude-x", "claude-x"},
		{str("openai"), "openai/gpt-x", "openai/gpt-x"},
	}
	for _, c := range cases {
		item := makeCase(func(item *types.Case) { item.Provider, item.Model = c.provider, str(c.model) })
		if got := strOrNil(FloorModel(item, plainPricing{}, "")); got != c.want {
			t.Errorf("FloorModel(%s, %s) = %s", strOrNil(c.provider), c.model, got)
		}
	}
}

func TestTreatmentCallCarriesProductionSettings(t *testing.T) {
	item := makeCase(func(c *types.Case) {
		c.Params = types.CallParams{Temperature: f(0.7), MaxTokens: f(300), TopP: f(0.9), Stop: []string{"END"}, ReasoningEffort: str("low")}
	})
	call := TreatmentCall(item, makeTreatment(func(tr *types.Treatment) {
		tr.Settings = types.TreatmentSettings{Temperature: f(0), ReasoningEffort: str("high")}
	}))
	if got := fmt.Sprintf("%v %v %v %v %v", *call.Temperature, call.MaxTokens, *call.TopP, call.Stop, *call.ReasoningEffort); got != "0 300 0.9 [END] high" {
		t.Errorf("call %s", got)
	}
}
