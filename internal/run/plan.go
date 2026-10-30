package run

// Plan a run: read the trace file, sample the latest cases, name the
// treatments and estimate the spend. No provider call.

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/metergraph/replay/internal/adapters"
	"github.com/metergraph/replay/internal/cases"
	"github.com/metergraph/replay/internal/cases/jsval"
	"github.com/metergraph/replay/internal/jsnum"
	"github.com/metergraph/replay/internal/pricing"
	"github.com/metergraph/replay/internal/types"
)

const (
	DefaultSample    = 20
	MaxSample        = 200
	MaxTrackedFields = 10
	DefaultBudgetUSD = 1.0
)

type TreatmentInput struct {
	Model string
	// The endpoint's key: a preset name or a base URL's host.
	Endpoint string
	// Nil fields are unset.
	Settings types.TreatmentSettings
	// Empty: the model.
	Label string
}

type PlanOptions struct {
	// Trace file path. Only its base name is recorded.
	File string
	// Adapter name; empty detects it.
	Adapter string
	// Zero: DefaultSample.
	Sample     int
	Treatments []TreatmentInput
	// Nil: on.
	NoiseFloor *bool
	// Endpoint key the noise floor runs on. Empty: the first treatment's.
	FloorEndpoint string
	TrackedFields []string
	// Nil: DefaultBudgetUSD. Zero is a budget (only free endpoints fit it).
	BudgetUSD     *float64
	AllowNoSearch bool
	Pricing       types.Pricing
	// Endpoints that cost nothing (local servers). Nil: none.
	IsFreeEndpoint func(endpoint string) bool
	// ISO time to price at. Empty: now.
	At string
}

// FileCounts is what the file held, before sampling.
type FileCounts struct {
	Calls        int
	Traces       int
	SkippedLines int
}

type RunPlan struct {
	Input types.RunInput
	// What the file held, before sampling.
	File FileCounts
	// The sampled cases, replayable or not, oldest first.
	Cases      []types.Case
	Treatments []types.Treatment
	// Endpoint key of the noise floor, or empty without one.
	FloorEndpoint  string
	FreeEndpoints  []string
	Settings       types.RunSettings
	Estimate       types.Estimate
	CatalogVersion string
}

// PlanError is a plan option out of range.
type PlanError struct{ Message string }

func (e *PlanError) Error() string { return e.Message }

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

func settingsSummary(settings types.TreatmentSettings) string {
	var parts []string
	if settings.Temperature != nil {
		parts = append(parts, "temperature="+jsnum.String(*settings.Temperature))
	}
	if settings.MaxTokens != nil {
		parts = append(parts, "max_tokens="+jsnum.String(*settings.MaxTokens))
	}
	if settings.ReasoningEffort != nil {
		parts = append(parts, "reasoning_effort="+*settings.ReasoningEffort)
	}
	return strings.Join(parts, ",")
}

// BuildTreatments names treatments t1, t2, ... with labels that are unique
// within the run.
func BuildTreatments(inputs []TreatmentInput) []types.Treatment {
	treatments := make([]types.Treatment, len(inputs))
	for i, input := range inputs {
		label := jsval.Trim(input.Label)
		if label == "" {
			label = input.Model
		}
		treatments[i] = types.Treatment{
			ID:       fmt.Sprintf("t%d", i+1),
			Label:    label,
			Model:    input.Model,
			Endpoint: input.Endpoint,
			Settings: types.TreatmentSettings{
				Temperature:     input.Settings.Temperature,
				MaxTokens:       input.Settings.MaxTokens,
				ReasoningEffort: input.Settings.ReasoningEffort,
			},
		}
	}
	count := func(label string) int {
		n := 0
		for _, t := range treatments {
			if t.Label == label {
				n++
			}
		}
		return n
	}
	for i := range treatments {
		t := &treatments[i]
		if count(t.Label) > 1 && inputs[i].Label == "" {
			summary := settingsSummary(t.Settings)
			sameEndpoint := true
			for _, other := range treatments {
				if other.Label == t.Label && other.Endpoint != t.Endpoint {
					sameEndpoint = false
				}
			}
			if !sameEndpoint {
				t.Label = fmt.Sprintf("%s (%s)", t.Label, t.Endpoint)
			} else if summary != "" {
				t.Label = fmt.Sprintf("%s @%s", t.Label, summary)
			}
		}
	}
	seen := map[string]int{}
	for i := range treatments {
		t := &treatments[i]
		seen[t.Label]++
		if n := seen[t.Label]; n > 1 {
			t.Label = fmt.Sprintf("%s #%d", t.Label, n)
		}
	}
	return treatments
}

// timeOf is Date.parse of a string time, or false.
func timeOf(ts *string) (float64, bool) {
	if ts == nil || *ts == "" {
		return 0, false
	}
	return adapters.DateParse(*ts)
}

// SampleCases is the latest `size` replayable cases, one per trace id where
// calls have one (the trace's latest replayable call, else its latest call),
// oldest first. The cases that cannot be replayed among them are kept, so the
// report says what was left out and why. Cases are ordered by time when every
// one has a time, else by their order in the file.
func SampleCases(all []types.Case, size int) []types.Case {
	type entry struct {
		item  types.Case
		index int
		time  float64
		timed bool
	}
	ordered := make([]entry, len(all))
	everyTimed := true
	for i, item := range all {
		t, ok := timeOf(item.TS)
		ordered[i] = entry{item: item, index: i, time: t, timed: ok}
		everyTimed = everyTimed && ok
	}
	if everyTimed {
		sort.SliceStable(ordered, func(a, b int) bool {
			if ordered[a].time != ordered[b].time {
				return ordered[a].time < ordered[b].time
			}
			return ordered[a].index < ordered[b].index
		})
	}
	// One representative per trace: its latest replayable call, else its latest call.
	type ranked struct {
		item types.Case
		rank int
	}
	groups := map[string]ranked{}
	var keys []string
	for rank, e := range ordered {
		key := fmt.Sprintf("call:%d", e.index)
		if e.item.TraceID != nil && *e.item.TraceID != "" {
			key = "trace:" + *e.item.TraceID
		}
		current, ok := groups[key]
		if !ok {
			keys = append(keys, key)
		}
		replayable := e.item.NotReplayable == nil
		if !ok || replayable || current.item.NotReplayable != nil {
			groups[key] = ranked{item: e.item, rank: rank}
		}
	}
	representatives := make([]ranked, 0, len(keys))
	for _, key := range keys {
		representatives = append(representatives, groups[key])
	}
	sort.SliceStable(representatives, func(a, b int) bool { return representatives[a].rank > representatives[b].rank })
	var picked []ranked
	replayable, notReplayable := 0, 0
	for _, e := range representatives {
		if replayable >= size {
			break
		}
		if e.item.NotReplayable == nil {
			replayable++
			picked = append(picked, e)
		} else if notReplayable < size {
			notReplayable++
			picked = append(picked, e)
		}
	}
	sort.SliceStable(picked, func(a, b int) bool { return picked[a].rank < picked[b].rank })
	out := make([]types.Case, len(picked))
	for i, e := range picked {
		out[i] = e.item
	}
	return out
}

// TrafficOf is how many calls the file holds and the time they span, for the
// report's monthly projection. Nil when no call has a time.
func TrafficOf(timestamps []*string) *types.Traffic {
	var from, to float64
	seen := false
	for _, ts := range timestamps {
		t, ok := timeOf(ts)
		if !ok {
			continue
		}
		if !seen || t < from {
			from = t
		}
		if !seen || t > to {
			to = t
		}
		seen = true
	}
	if !seen {
		return nil
	}
	fromISO, toISO := adapters.ISOString(from), adapters.ISOString(to)
	return &types.Traffic{Calls: len(timestamps), From: &fromISO, To: &toISO}
}

func PlanRun(options PlanOptions) (*RunPlan, error) {
	sample := options.Sample
	if sample == 0 {
		sample = DefaultSample
	}
	if sample < 1 || sample > MaxSample {
		return nil, &PlanError{Message: fmt.Sprintf("The sample must be a whole number from 1 to %d.", MaxSample)}
	}
	tracked := options.TrackedFields
	if len(tracked) > MaxTrackedFields {
		return nil, &PlanError{Message: fmt.Sprintf("Track at most %d fields.", MaxTrackedFields)}
	}
	if len(options.Treatments) == 0 {
		return nil, &PlanError{Message: "Name at least one model to replay on."}
	}
	budgetUSD := DefaultBudgetUSD
	if options.BudgetUSD != nil {
		budgetUSD = *options.BudgetUSD
	}
	if !finite(budgetUSD) || budgetUSD < 0 {
		return nil, &PlanError{Message: "The budget must be a positive amount."}
	}

	skippedLines := 0
	calls, err := adapters.ReadTraces(options.File, adapters.ReadTracesOptions{
		Adapter: options.Adapter,
		OnSkip:  func(int, string) { skippedLines++ },
	})
	if err != nil {
		return nil, err
	}
	all := make([]types.Case, len(calls))
	traceKeys := map[string]bool{}
	timestamps := make([]*string, len(calls))
	for i, call := range calls {
		all[i] = cases.PrepareCase(call, cases.PrepareCaseOptions{AllowNoSearch: options.AllowNoSearch})
		if call.TraceID != nil {
			traceKeys[*call.TraceID] = true
		} else {
			traceKeys[fmt.Sprintf("call:%d", i)] = true
		}
		timestamps[i] = call.TS
	}
	sampled := SampleCases(all, sample)

	treatments := BuildTreatments(options.Treatments)
	isFree := options.IsFreeEndpoint
	if isFree == nil {
		isFree = func(string) bool { return false }
	}
	noiseFloor := options.NoiseFloor == nil || *options.NoiseFloor
	floorEndpoint := ""
	if noiseFloor {
		floorEndpoint = options.FloorEndpoint
		if floorEndpoint == "" {
			floorEndpoint = treatments[0].Endpoint
		}
	}
	endpoints := map[string]bool{}
	for _, t := range treatments {
		endpoints[t.Endpoint] = true
	}
	if floorEndpoint != "" {
		endpoints[floorEndpoint] = true
	}
	freeEndpoints := []string{}
	for endpoint := range endpoints {
		if isFree(endpoint) {
			freeEndpoints = append(freeEndpoints, endpoint)
		}
	}
	sort.Strings(freeEndpoints)

	// A noise floor on a local endpoint costs nothing and needs no price.
	freeFloor := floorEndpoint != "" && isFree(floorEndpoint)
	estimate := pricing.EstimateRun(sampled, treatments, pricing.EstimateOptions{
		NoiseFloor: noiseFloor && !freeFloor,
		IsFree:     func(t types.Treatment) bool { return isFree(t.Endpoint) },
		At:         options.At,
	}, options.Pricing)
	if freeFloor {
		for _, arm := range FloorArms {
			estimate.PerArm[arm] = types.ArmEstimate{ExpectedUSD: 0, UpperBoundUSD: 0}
		}
	}

	adapter := options.Adapter
	if len(calls) > 0 {
		adapter = calls[0].Source
	}
	if adapter == "" {
		adapter = "unknown"
	}
	if sampled == nil {
		sampled = []types.Case{}
	}
	return &RunPlan{
		Input: types.RunInput{
			File:    filepath.Base(options.File),
			Adapter: adapter,
			Traffic: TrafficOf(timestamps),
		},
		File:           FileCounts{Calls: len(calls), Traces: len(traceKeys), SkippedLines: skippedLines},
		Cases:          sampled,
		Treatments:     treatments,
		FloorEndpoint:  floorEndpoint,
		FreeEndpoints:  freeEndpoints,
		Settings:       types.RunSettings{Sample: sample, TrackedFields: append([]string{}, tracked...), NoiseFloor: noiseFloor, BudgetUSD: budgetUSD},
		Estimate:       estimate,
		CatalogVersion: options.Pricing.CatalogVersion(),
	}, nil
}
