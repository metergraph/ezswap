package run

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metergraph/ezswap/internal/pricing"
	"github.com/metergraph/ezswap/internal/providers"
	"github.com/metergraph/ezswap/internal/types"
)

const at = "2026-10-28T12:00:00.000Z"

var nowTime = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func now() time.Time { return nowTime }

func loadTestPricing(t *testing.T) *pricing.CatalogPricing {
	t.Helper()
	prices, err := pricing.LoadPricing(pricing.LoadPricingOptions{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return prices
}

func baseOptions(t *testing.T, file string) PlanOptions {
	return PlanOptions{
		File:          file,
		Treatments:    []TreatmentInput{{Model: "google/gemini-2.5-flash-lite", Endpoint: "openrouter"}},
		FloorEndpoint: "openrouter",
		Pricing:       loadTestPricing(t),
		At:            at,
	}
}

func plan(t *testing.T, count int, edit func(*PlanOptions)) *RunPlan {
	t.Helper()
	options := baseOptions(t, writeTraces(t, t.TempDir(), count, nil))
	if edit != nil {
		edit(&options)
	}
	p, err := PlanRun(options)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func productionInputOf(item types.Case) *float64 {
	if item.Production.Usage == nil {
		return nil
	}
	return item.Production.Usage.InputTokens
}

// caseBound is what one case reserves: every arm's upper bound.
func caseBound(t *testing.T, p *RunPlan, prices types.Pricing, item types.Case) float64 {
	t.Helper()
	total := 0.0
	for _, treatment := range p.Treatments {
		total += *pricing.UpperBoundUSD(pricing.TreatmentCall(item, treatment), prices, productionInputOf(item), at)
	}
	if p.FloorEndpoint != "" {
		model := pricing.FloorModel(item, prices, at)
		total += 2 * *pricing.UpperBoundUSD(pricing.FloorCall(item, *model), prices, productionInputOf(item), at)
	}
	return total
}

func execute(t *testing.T, p *RunPlan, options ExecuteOptions) *types.RunFile {
	t.Helper()
	if options.Now == nil {
		options.Now = now
	}
	run, err := ExecuteRun(context.Background(), p, options)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func ids(cs []types.Case) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func equal(t *testing.T, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func errorMatches(t *testing.T, err error, fragment string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), fragment) {
		t.Fatalf("want an error with %q, got %v", fragment, err)
	}
}

func TestPlanRunSamplesTheLatestReplayableCasesOnePerTraceAndKeepsTheUnreplayableOnes(t *testing.T) {
	dir := t.TempDir()
	// Calls 1-10; 7 and 8 share a trace; 9 is cut short; 10 searched the web.
	file := writeTraces(t, dir, 10, func(n int) traceOptions {
		var o traceOptions
		if n == 7 || n == 8 {
			o.traceID = str("shared")
		}
		o.truncated = n == 9
		o.search = n == 10
		return o
	})
	prices := loadTestPricing(t)
	treatments := []TreatmentInput{{Model: "google/gemini-2.5-flash-lite", Endpoint: "openrouter"}}
	p, err := PlanRun(PlanOptions{File: file, Sample: 3, Treatments: treatments, Pricing: prices, At: at})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, p.File.Calls, 10)
	equal(t, p.File.Traces, 9)
	equal(t, p.Input.File, "traces.jsonl")
	equal(t, p.Input.Adapter, "ezswap-jsonl")
	equal(t, ids(p.Cases), []string{"call-5", "call-6", "call-8", "call-9", "call-10"})
	equal(t, p.Estimate.Replayable, 3)
	equal(t, p.Estimate.NotReplayable, map[string]int{"content_truncated": 1, "web_search_not_replayed": 1})

	allowed, err := PlanRun(PlanOptions{File: file, Sample: 3, AllowNoSearch: true, Treatments: treatments, Pricing: prices, At: at})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, ids(allowed.Cases), []string{"call-6", "call-8", "call-9", "call-10"})
}

func TestSampleCasesOrdersByTimeWhenEveryCaseHasOne(t *testing.T) {
	cases := []types.Case{
		{ID: "b", TS: str("2026-10-02T00:00:00Z")},
		{ID: "a", TS: str("2026-10-01T00:00:00Z")},
		{ID: "c", TS: str("2026-10-03T00:00:00Z")},
	}
	equal(t, ids(SampleCases(cases, 2)), []string{"b", "c"})
}

func TestPlanRunRejectsASampleAboveTheMaximumAndTooManyTrackedFields(t *testing.T) {
	options := baseOptions(t, writeTraces(t, t.TempDir(), 2, nil))
	options.Sample = 201
	_, err := PlanRun(options)
	errorMatches(t, err, "from 1 to 200")
	var planErr *PlanError
	if !errorsAs(err, &planErr) {
		t.Fatalf("want a *PlanError, got %T", err)
	}

	options.Sample = 0
	for i := range 11 {
		options.TrackedFields = append(options.TrackedFields, fmt.Sprintf("$.f%d", i))
	}
	_, err = PlanRun(options)
	errorMatches(t, err, "at most 10")
}

func errorsAs(err error, target **PlanError) bool {
	e, ok := err.(*PlanError)
	if ok {
		*target = e
	}
	return ok
}

func TestBuildTreatmentsNumbersTreatmentsAndKeepsLabelsUnique(t *testing.T) {
	treatments := BuildTreatments([]TreatmentInput{
		{Model: "openai/gpt-5-mini", Endpoint: "openrouter"},
		{Model: "openai/gpt-5-mini", Endpoint: "openai"},
		{Model: "openai/gpt-5-mini", Endpoint: "openai", Settings: types.TreatmentSettings{Temperature: num(0.2)}},
	})
	var gotIDs []string
	labels := map[string]bool{}
	for _, treatment := range treatments {
		gotIDs = append(gotIDs, treatment.ID)
		labels[treatment.Label] = true
	}
	equal(t, gotIDs, []string{"t1", "t2", "t3"})
	equal(t, len(labels), 3)
	equal(t, *treatments[2].Settings.Temperature, 0.2)
}

func TestParseModelsReadsEndpointPrefixesAndPerModelSettings(t *testing.T) {
	vercel, err := ParseEndpoint("vercel")
	if err != nil {
		t.Fatal(err)
	}
	specs, err := ParseModels(
		"openrouter:anthropic/claude-haiku-4.5,openai:gpt-5-mini@temperature=0.2,max_tokens=500,google/gemini-2.5-flash",
		vercel,
	)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		model, endpoint string
		settings        types.TreatmentSettings
	}
	var got []row
	for _, s := range specs {
		got = append(got, row{s.Model, s.Endpoint.Key, s.Settings})
	}
	equal(t, got, []row{
		{"anthropic/claude-haiku-4.5", "openrouter", types.TreatmentSettings{}},
		{"openai/gpt-5-mini", "openai", types.TreatmentSettings{Temperature: num(0.2), MaxTokens: num(500)}},
		{"google/gemini-2.5-flash", "vercel", types.TreatmentSettings{}},
	})
	openrouter, _ := ParseEndpoint("openrouter")
	_, err = ParseModels("x@top_k=3", openrouter)
	errorMatches(t, err, "Settings are")
	local, err := ParseEndpoint("http://localhost:8000/v1/")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, []any{local.Key, local.BaseURL, local.Free}, []any{"localhost:8000", "http://localhost:8000/v1", true})
	remote, _ := ParseEndpoint("https://llm.example.com/v1")
	equal(t, remote.Free, false)
	_, err = ParseEndpoint("https://user:secret@llm.example.com/v1")
	errorMatches(t, err, "environment variable")
}

// The URL normalization WHATWG does and net/url
// does not, and the spec errors.
func TestParseEndpointNormalizesLikeWHATWG(t *testing.T) {
	for _, c := range []struct{ spec, key, base string }{
		{"https://LLM.Example.com:443/v1//", "llm.example.com", "https://llm.example.com/v1"},
		{"http://127.0.0.1:80", "127.0.0.1", "http://127.0.0.1"},
		{"http://[::1]:8000/v1", "[::1]:8000", "http://[::1]:8000/v1"},
		{"http://app.localhost:3000/v1?x=1#y", "app.localhost:3000", "http://app.localhost:3000/v1"},
	} {
		spec, err := ParseEndpoint(c.spec)
		if err != nil {
			t.Fatal(err)
		}
		equal(t, []string{spec.Key, spec.BaseURL}, []string{c.key, c.base})
	}
	appLocal, _ := ParseEndpoint("http://app.localhost:3000")
	equal(t, appLocal.Free, true)
	ollama, _ := ParseEndpoint(" ollama ")
	equal(t, []any{ollama.Key, ollama.Free}, []any{"ollama", true})
	_, err := ParseEndpoint("nonsense")
	errorMatches(t, err, `"nonsense" is not an endpoint. Use a preset (openai, openrouter, vercel, litellm, ollama)`)
	_, err = ParseEndpoint("ftp://example.com")
	errorMatches(t, err, "must start with http:// or https://")
	_, err = ParseModels(" , ", appLocal)
	errorMatches(t, err, "--models names no model.")
	_, err = ParseModels("a b", appLocal)
	errorMatches(t, err, `"a b" does not name a model.`)
	_, err = ParseTemperature("2.5")
	errorMatches(t, err, `Temperature must be a number from 0 to 2, not "2.5".`)
	_, err = ParseMaxTokens("1.5")
	errorMatches(t, err, "Max tokens must be a whole number")
	effort, err := ParseEffort(" HIGH ")
	if err != nil {
		t.Fatal(err)
	}
	equal(t, effort, "high")
	_, err = ParseEffort("max")
	errorMatches(t, err, "Reasoning effort must be one of none, minimal, low, medium, high, xhigh.")
	if _, ok := err.(*SpecError); !ok {
		t.Fatalf("want a *SpecError, got %T", err)
	}
}

func TestExecuteRunReplaysEveryCaseOnEveryTreatmentAndTwiceForTheFloorPricedFromTheCatalog(t *testing.T) {
	prices := loadTestPricing(t)
	p := plan(t, 3, nil)
	provider := newFakeProvider(nil)
	run := execute(t, p, ExecuteOptions{Providers: map[string]types.Provider{"openrouter": provider}, Pricing: prices})
	equal(t, len(run.Replays), 3*3)
	equal(t, []string{run.Replays[0].Arm, run.Replays[1].Arm, run.Replays[2].Arm}, []string{"t1", "floor_a1", "floor_a2"})
	calls := provider.Calls()
	equal(t, len(calls), 9)
	// Calls go out in order with concurrency, but which of the first case's
	// floor calls the provider saw second is not fixed: check the set.
	floorCalls := 0
	for _, call := range calls {
		if call.Model == "openai/gpt-4.1-mini" {
			floorCalls++
		}
	}
	equal(t, floorCalls, 6)
	sum := 0.0
	for _, r := range run.Replays {
		if r.Status != "ok" || r.CostSource == nil || *r.CostSource != "catalog" || r.CostUSD == nil || *r.CostUSD <= 0 {
			t.Fatalf("replay %+v", r)
		}
		sum += *r.CostUSD
	}
	if math.Abs(run.SpentUSD-sum) >= 1e-8 {
		t.Fatalf("spent %v, sum %v", run.SpentUSD, sum)
	}
	if run.Stopped != nil {
		t.Fatalf("stopped %v", *run.Stopped)
	}
	equal(t, run.CreatedAt, at)
	equal(t, run.Schema, "metergraph.replay-run/v1")
}

func TestExecuteRunSendsCallsInPlanOrderOneAtATime(t *testing.T) {
	prices := loadTestPricing(t)
	p := plan(t, 2, nil)
	provider := newFakeProvider(nil)
	execute(t, p, ExecuteOptions{Providers: map[string]types.Provider{"openrouter": provider}, Pricing: prices, Concurrency: 1})
	var models []string
	for _, call := range provider.Calls() {
		models = append(models, call.Model)
	}
	equal(t, models[1], "openai/gpt-4.1-mini")
	equal(t, models[0], "google/gemini-2.5-flash-lite")
}

func costOf(r types.Replay) []any {
	var source any
	if r.CostSource != nil {
		source = *r.CostSource
	}
	var cost any
	if r.CostUSD != nil {
		cost = *r.CostUSD
	}
	return []any{source, cost}
}

func TestCostSources(t *testing.T) {
	// Endpoint-reported, reservation for a maybe-billed failure, zero for a
	// failure that was not billed.
	prices := loadTestPricing(t)
	noFloor := false
	p := plan(t, 1, func(o *PlanOptions) { o.NoiseFloor = &noFloor })
	item := p.Cases[0]
	reserve := *pricing.UpperBoundUSD(pricing.TreatmentCall(item, p.Treatments[0]), prices, productionInputOf(item), at)
	with := func(respond func(types.ReplayCall, int) types.ReplyOutcome) *types.RunFile {
		return execute(t, p, ExecuteOptions{Providers: map[string]types.Provider{"openrouter": newFakeProvider(respond)}, Pricing: prices})
	}

	reported := with(func(types.ReplayCall, int) types.ReplyOutcome {
		return okOutcome(outcomeOverrides{reportedCostUSD: num(0.00001)})
	})
	equal(t, costOf(reported.Replays[0]), []any{"endpoint", 0.00001})

	billed := with(func(types.ReplayCall, int) types.ReplyOutcome { return failedOutcome(true) })
	equal(t, costOf(billed.Replays[0]), []any{"reservation", math.Round(reserve*1e8) / 1e8})
	if billed.Stopped != nil {
		t.Fatalf("stopped %v", *billed.Stopped)
	}

	refused := with(func(types.ReplayCall, int) types.ReplyOutcome { return failedOutcome(false) })
	equal(t, costOf(refused.Replays[0]), []any{nil, 0.0})

	noUsage := with(func(types.ReplayCall, int) types.ReplyOutcome { return okOutcome(outcomeOverrides{noUsage: true}) })
	equal(t, *noUsage.Replays[0].CostSource, "reservation")
}

func TestAFreeEndpointCostsNothingAndReservesNothingSoAZeroBudgetStillRuns(t *testing.T) {
	prices := loadTestPricing(t)
	zero := 0.0
	p := plan(t, 2, func(o *PlanOptions) {
		o.Treatments = []TreatmentInput{{Model: "llama3.2", Endpoint: "ollama"}}
		o.FloorEndpoint = "ollama"
		o.IsFreeEndpoint = func(e string) bool { return e == "ollama" }
		o.BudgetUSD = &zero
	})
	equal(t, p.Estimate.ExpectedUSD, 0.0)
	equal(t, p.Estimate.Unpriced, []string{})
	run := execute(t, p, ExecuteOptions{Providers: map[string]types.Provider{"ollama": newFakeProvider(nil)}, Pricing: prices})
	equal(t, len(run.Replays), 6)
	for _, r := range run.Replays {
		equal(t, costOf(r), []any{"free", 0.0})
	}
	if run.Stopped != nil {
		t.Fatalf("stopped %v", *run.Stopped)
	}
}

func TestTheBudgetStopsTheRunBetweenWholeCasesAndIsNeverExceeded(t *testing.T) {
	prices := loadTestPricing(t)
	p := plan(t, 5, nil)
	bound := caseBound(t, p, prices, p.Cases[0])
	// Every call costs its full reservation, so exactly two cases fit.
	budget := bound * 2.5
	provider := newFakeProvider(func(types.ReplayCall, int) types.ReplyOutcome { return failedOutcome(true) })
	run := execute(t, p, ExecuteOptions{Providers: map[string]types.Provider{"openrouter": provider}, Pricing: prices, BudgetUSD: &budget})
	equal(t, *run.Stopped, "budget")
	equal(t, len(provider.Calls()), 6)
	if run.SpentUSD > budget {
		t.Fatalf("spent %v over %v", run.SpentUSD, budget)
	}
	byCase := map[string]map[string]bool{}
	for _, r := range run.Replays {
		if byCase[r.CaseID] == nil {
			byCase[r.CaseID] = map[string]bool{}
		}
		byCase[r.CaseID][r.Status] = true
	}
	// No case is half replayed.
	for caseID, statuses := range byCase {
		if len(statuses) != 1 {
			t.Fatalf("case %s has statuses %v", caseID, statuses)
		}
	}
	skippedCount := 0
	for _, r := range run.Replays {
		if r.Status == "skipped" {
			skippedCount++
			equal(t, r.Error.Code, "budget_exhausted")
		}
	}
	equal(t, skippedCount, 9)
	equal(t, run.Settings.BudgetUSD, budget)
}

func TestACaseWaitsForCallsInFlightToSettleBeforeTheBudgetStopsTheRun(t *testing.T) {
	prices := loadTestPricing(t)
	p := plan(t, 4, nil)
	budget := caseBound(t, p, prices, p.Cases[0]) * 1.5
	// Reservations hold two cases at once, but each settles far below them.
	run := execute(t, p, ExecuteOptions{
		Providers:   map[string]types.Provider{"openrouter": newFakeProvider(nil)},
		Pricing:     prices,
		BudgetUSD:   &budget,
		Concurrency: 4,
	})
	if run.Stopped != nil {
		t.Fatalf("stopped %v", *run.Stopped)
	}
	for _, r := range run.Replays {
		equal(t, r.Status, "ok")
	}
}

func TestACallIsNeverSentWhenItsCaseDoesNotFitTheBudget(t *testing.T) {
	prices := loadTestPricing(t)
	p := plan(t, 2, nil)
	provider := newFakeProvider(nil)
	budget := caseBound(t, p, prices, p.Cases[0]) / 2
	run := execute(t, p, ExecuteOptions{Providers: map[string]types.Provider{"openrouter": provider}, Pricing: prices, BudgetUSD: &budget})
	equal(t, len(provider.Calls()), 0)
	equal(t, *run.Stopped, "budget")
	equal(t, run.SpentUSD, 0.0)
}

func TestACallThatCostsMoreThanItsReservationStopsTheRun(t *testing.T) {
	prices := loadTestPricing(t)
	p := plan(t, 3, nil)
	provider := newFakeProvider(func(types.ReplayCall, int) types.ReplyOutcome {
		return okOutcome(outcomeOverrides{reportedCostUSD: num(5)})
	})
	budget := 100.0
	run := execute(t, p, ExecuteOptions{Providers: map[string]types.Provider{"openrouter": provider}, Pricing: prices, BudgetUSD: &budget, Concurrency: 1})
	equal(t, *run.Stopped, "reservation_exceeded")
	equal(t, len(provider.Calls()), 1)
	ok := 0
	for _, r := range run.Replays {
		switch r.Status {
		case "ok":
			ok++
		case "skipped":
			equal(t, r.Error.Code, "reservation_exceeded")
		}
	}
	equal(t, ok, 1)
}

// billedAt reports each call's cost as factor times what it reserved, as an
// endpoint does when the catalog lists a lower price than it charges.
func billedAt(factor func(call int) float64) (*fakeProvider, func(Event)) {
	var reserve float64
	provider := newFakeProvider(func(_ types.ReplayCall, i int) types.ReplyOutcome {
		return okOutcome(outcomeOverrides{reportedCostUSD: num(reserve * factor(i))})
	})
	return provider, func(e Event) {
		if e.Kind == EventCallStarted && e.ReserveUSD != nil {
			reserve = *e.ReserveUSD
		}
	}
}

func TestAModelBilledAboveTheCatalogRaisesItsReservations(t *testing.T) {
	prices := loadTestPricing(t)
	p := plan(t, 3, nil)
	// The treatment's first call is billed at twice its reservation; every
	// later call costs what the raised reservation expects.
	provider, onEvent := billedAt(func(i int) float64 {
		if i == 0 {
			return 2
		}
		return 0.5
	})
	budget := 100.0
	run := execute(t, p, ExecuteOptions{Providers: map[string]types.Provider{"openrouter": provider}, Pricing: prices, BudgetUSD: &budget, Concurrency: 1, OnEvent: onEvent})
	if run.Stopped != nil {
		t.Fatalf("stopped: %s", *run.Stopped)
	}
	for _, r := range run.Replays {
		equal(t, r.Status, "ok")
	}
	if len(run.Repriced) != 1 || run.Repriced[0].Model != p.Treatments[0].Model || run.Repriced[0].Factor != 2.5 {
		t.Fatalf("repriced %+v", run.Repriced)
	}
}

func TestAModelThatKeepsOverrunningStopsTheRun(t *testing.T) {
	prices := loadTestPricing(t)
	p := plan(t, 6, nil)
	provider, onEvent := billedAt(func(int) float64 { return 3 })
	budget := 100.0
	run := execute(t, p, ExecuteOptions{Providers: map[string]types.Provider{"openrouter": provider}, Pricing: prices, BudgetUSD: &budget, Concurrency: 1, OnEvent: onEvent})
	if run.Stopped == nil || *run.Stopped != "reservation_exceeded" {
		t.Fatalf("stopped %v", run.Stopped)
	}
	// Raised twice (3 x 1.25 each time), then stopped on the third overrun.
	if len(run.Repriced) == 0 || run.Repriced[0].Factor != 14.06 {
		t.Fatalf("repriced %+v", run.Repriced)
	}
}

func TestAnUpperBoundIgnoresOffPeakDiscounts(t *testing.T) {
	prices, err := pricing.LoadPricing(pricing.LoadPricingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// DeepSeek's gateway row halves its price outside peak hours: a Saturday
	// is off-peak all day.
	query := types.PriceQuery{Model: "deepseek/deepseek-v4.1-flash", InputTokens: 100, OutputTokens: 400, At: "2026-11-21T12:00:00Z"}
	discounted := prices.Price(query)
	query.FullRate = true
	full := prices.Price(query)
	if discounted == nil || full == nil || *full != *discounted*2 {
		t.Fatalf("discounted %v, full %v", discounted, full)
	}
	call := types.ReplayCall{Model: "deepseek/deepseek-v4.1-flash", MaxTokens: 400}
	bound := pricing.UpperBoundUSD(call, prices, num(100), "2026-11-21T12:00:00Z")
	if bound == nil || *bound < *full {
		t.Fatalf("bound %v below the full rate %v", bound, *full)
	}
}

func TestAnInterruptLetsCallsInFlightFinishAndRecordsWhatExists(t *testing.T) {
	prices := loadTestPricing(t)
	p := plan(t, 4, nil)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	// Calls in flight get the hard context, not the stop signal: they finish.
	var cancelledInFlight atomic.Bool
	provider := &fakeProvider{}
	provider.respond = func(_ types.ReplayCall, index int) types.ReplyOutcome {
		if index == 0 {
			stop()
		}
		return okOutcome(outcomeOverrides{})
	}
	wrapped := providerFunc(func(callCtx context.Context, call types.ReplayCall) types.ReplyOutcome {
		outcome := provider.Complete(callCtx, call)
		if callCtx.Err() != nil {
			cancelledInFlight.Store(true)
		}
		return outcome
	})
	run, err := ExecuteRun(ctx, p, ExecuteOptions{
		Providers:   map[string]types.Provider{"openrouter": wrapped},
		Pricing:     prices,
		Concurrency: 2,
		Now:         now,
	})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, *run.Stopped, "interrupted")
	ok := 0
	for _, r := range run.Replays {
		switch r.Status {
		case "ok":
			ok++
		case "skipped":
			equal(t, r.Error.Code, "interrupted")
		}
	}
	// Every call in flight when the interrupt came was recorded.
	equal(t, ok, len(provider.Calls()))
	if ok < 1 || ok > 2 {
		t.Fatalf("%d calls recorded", ok)
	}
	equal(t, len(run.Replays), 12)
	if cancelledInFlight.Load() {
		t.Fatal("a call in flight saw the stop signal")
	}
}

func TestAHardStopCancelsCallsInFlight(t *testing.T) {
	prices := loadTestPricing(t)
	p := plan(t, 2, nil)
	hard, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 8)
	blocking := providerFunc(func(callCtx context.Context, call types.ReplayCall) types.ReplyOutcome {
		started <- struct{}{}
		<-callCtx.Done()
		return types.ReplyOutcome{Status: "failed", Flags: []types.ReplayFlag{}, Error: &types.ReplayError{Code: "aborted", Message: "aborted"}}
	})
	go func() {
		<-started
		cancel()
	}()
	run, err := ExecuteRun(context.Background(), p, ExecuteOptions{
		Providers: map[string]types.Provider{"openrouter": blocking},
		Pricing:   prices,
		Hard:      hard,
		Now:       now,
	})
	if err != nil {
		t.Fatal(err)
	}
	equal(t, *run.Stopped, "interrupted")
	equal(t, len(run.Replays), 6)
}

func TestAPanickingProviderIsRecordedAsAMaybeBilledFailure(t *testing.T) {
	prices := loadTestPricing(t)
	noFloor := false
	p := plan(t, 1, func(o *PlanOptions) { o.NoiseFloor = &noFloor })
	run := execute(t, p, ExecuteOptions{
		Providers: map[string]types.Provider{"openrouter": providerFunc(func(context.Context, types.ReplayCall) types.ReplyOutcome { panic("boom") })},
		Pricing:   prices,
	})
	r := run.Replays[0]
	equal(t, []any{r.Status, r.Error.Code, *r.CostSource, r.Flags}, []any{"failed", "provider_error", "reservation", []types.ReplayFlag{types.FlagMaybeBilled}})
}

func TestAProviderIsNeededForEveryEndpointThePlanNames(t *testing.T) {
	p := plan(t, 1, nil)
	_, err := ExecuteRun(context.Background(), p, ExecuteOptions{Providers: map[string]types.Provider{}, Pricing: loadTestPricing(t)})
	errorMatches(t, err, "No provider")
	if _, ok := err.(*ExecuteError); !ok {
		t.Fatalf("want an *ExecuteError, got %T", err)
	}
}

func TestEventsAndProgressFollowTheRun(t *testing.T) {
	prices := loadTestPricing(t)
	p := plan(t, 3, nil)
	bound := caseBound(t, p, prices, p.Cases[0])
	budget := bound * 2.5
	var events []Event
	var progress []Progress
	run := execute(t, p, ExecuteOptions{
		Providers:  map[string]types.Provider{"openrouter": newFakeProvider(func(types.ReplayCall, int) types.ReplyOutcome { return failedOutcome(true) })},
		Pricing:    prices,
		BudgetUSD:  &budget,
		OnEvent:    func(e Event) { events = append(events, e) },
		OnProgress: func(pr Progress) { progress = append(progress, pr) },
	})
	count := map[EventKind]int{}
	for _, e := range events {
		count[e.Kind]++
		if e.Progress.SpentUSD > budget || e.Progress.SpentUSD+e.Progress.ReservedUSD > budget+1e-9 {
			t.Fatalf("event %+v over budget", e)
		}
		equal(t, e.Progress.BudgetUSD, budget)
	}
	equal(t, count, map[EventKind]int{
		EventCaseStarted: 2, EventCallStarted: 6, EventCallFinished: 9, EventCaseFinished: 3,
		EventStopped: 1, EventFinished: 1,
	})
	last := events[len(events)-1]
	equal(t, last.Kind, EventFinished)
	equal(t, *last.Stopped, "budget")
	equal(t, last.Progress.Done, last.Progress.Total)
	equal(t, last.Progress.SpentUSD, run.SpentUSD)
	equal(t, last.Progress.InFlight, 0)
	// One progress per settled call, and one at the end.
	equal(t, len(progress), 7)
	for _, e := range events {
		if e.Kind == EventCallFinished && e.Replay.Status != "skipped" {
			equal(t, *e.Replay.CostSource, "reservation")
			if e.Model == "" || e.Endpoint != "openrouter" {
				t.Fatalf("call event %+v", e)
			}
		}
	}
}

func TestWriteRunWritesRunJSONAndNothingElse(t *testing.T) {
	prices := loadTestPricing(t)
	noFloor := false
	p := plan(t, 1, func(o *PlanOptions) { o.NoiseFloor = &noFloor })
	run := execute(t, p, ExecuteOptions{Providers: map[string]types.Provider{"openrouter": newFakeProvider(nil)}, Pricing: prices})
	dir := t.TempDir()
	written, err := WriteRun(dir, run)
	if err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(written.RunPath)
	if err != nil {
		t.Fatal(err)
	}
	var back types.RunFile
	if err := json.Unmarshal(text, &back); err != nil {
		t.Fatal(err)
	}
	// Compare as JSON: raw schemas come back indented.
	equal(t, plainJSON(t, mustMarshal(t, &back)), plainJSON(t, mustMarshal(t, run)))
	entries, _ := os.ReadDir(dir)
	equal(t, len(entries), 1)
	equal(t, entries[0].Name(), "run.json")
	if !strings.HasSuffix(string(text), "}\n") || !strings.HasPrefix(string(text), "{\n  \"schema\": ") {
		t.Fatalf("not JSON.stringify(run, null, 2) form: %q", text[:40])
	}
	// Schemas keep production's key order through the indent.
	if !strings.Contains(string(text), "\"responseFormat\": {\n        \"type\": \"json_object\"\n      }") {
		t.Fatalf("responseFormat not written in place:\n%s", text)
	}
}

func TestEncodeRunMatchesJSONStringify(t *testing.T) {
	run := &types.RunFile{
		Schema:     types.RunSchema,
		Treatments: []types.Treatment{},
		Cases: []types.Case{{
			ID:             "a",
			System:         str("<b>&</b>   \\u2028"),
			Messages:       []types.Message{},
			ResponseFormat: json.RawMessage(`{"z":1,"a":{"y":[],"b":{}}}`),
			Missing:        []string{},
		}},
		Replays: []types.Replay{},
	}
	text, err := EncodeRun(run)
	if err != nil {
		t.Fatal(err)
	}
	s := string(text)
	for _, want := range []string{
		`"system": "<b>&</b> ` + " " + ` \\u2028"`,
		"\"responseFormat\": {\n        \"z\": 1,\n        \"a\": {\n          \"y\": [],\n          \"b\": {}\n        }\n      }",
		`"stopped": null` + "\n}\n",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
}

// No key ever reaches run.json: a real provider reads the key from its
// environment, sends it, and the run file names only the endpoint's host.
func TestRunJSONHoldsNoKey(t *testing.T) {
	const secret = "sk-test-0123456789abcdef-never-written"
	var sawKey atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+secret {
			sawKey.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"category\":\"bug\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":50,"completion_tokens":10}}`)
	}))
	defer server.Close()

	endpoint, err := ParseEndpoint(server.URL + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	keyEnv := "EZSWAP_TEST_KEY"
	provider, err := providers.CreateProvider(providers.ProviderOptions{
		BaseURL:   endpoint.BaseURL,
		APIKeyEnv: &keyEnv,
		Env:       map[string]string{keyEnv: secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	prices := loadTestPricing(t)
	p := plan(t, 2, func(o *PlanOptions) {
		o.Treatments = []TreatmentInput{{Model: "openai/gpt-5-mini", Endpoint: endpoint.Key}}
		o.FloorEndpoint = endpoint.Key
		o.IsFreeEndpoint = func(e string) bool { return e == endpoint.Key && endpoint.Free }
	})
	run := execute(t, p, ExecuteOptions{Providers: map[string]types.Provider{endpoint.Key: provider}, Pricing: prices})
	written, err := WriteRun(t.TempDir(), run)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := os.ReadFile(written.RunPath)
	if !sawKey.Load() {
		t.Fatal("the provider never sent the key")
	}
	for _, r := range run.Replays {
		equal(t, r.Status, "ok")
	}
	if strings.Contains(string(text), secret) || strings.Contains(string(text), "sk-test") {
		t.Fatal("run.json holds the key")
	}
	if !strings.Contains(string(text), `"endpoint": "`+endpoint.Key+`"`) {
		t.Fatal("run.json does not name the endpoint host")
	}
}

func TestTrafficOfSpansTheFilesTimedCallsAndIgnoresUntimedOnes(t *testing.T) {
	traffic := TrafficOf([]*string{str("2026-10-02T00:00:00Z"), nil, str("2026-10-01T00:00:00Z"), str("not a time")})
	equal(t, []any{traffic.Calls, *traffic.From, *traffic.To}, []any{4, "2026-10-01T00:00:00.000Z", "2026-10-02T00:00:00.000Z"})
	if TrafficOf([]*string{nil}) != nil {
		t.Fatal("want nil traffic")
	}
}

type providerFunc func(ctx context.Context, call types.ReplayCall) types.ReplyOutcome

func (f providerFunc) Name() string { return "func" }
func (f providerFunc) Complete(ctx context.Context, call types.ReplayCall) types.ReplyOutcome {
	return f(ctx, call)
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func plainJSON(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
