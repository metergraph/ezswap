package run

// Plan a run: read the trace file, sample the latest cases, name the
// treatments and estimate the spend. No provider call.

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/metergraph/ezswap/internal/adapters"
	"github.com/metergraph/ezswap/internal/cases"
	"github.com/metergraph/ezswap/internal/cases/jsval"
	"github.com/metergraph/ezswap/internal/jsnum"
	"github.com/metergraph/ezswap/internal/parts"
	"github.com/metergraph/ezswap/internal/pricing"
	"github.com/metergraph/ezswap/internal/types"
)

const (
	DefaultSample    = 20
	MaxSample        = 200
	MaxTrackedFields = 10
	DefaultBudgetUSD = 1.0

	// A map run samples each part on its own.
	DefaultPerPart      = 20
	MinPerPart          = 10
	MaxPerPart          = 200
	DefaultMapBudgetUSD = 5.0
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

	// Map plans. Map selects one; the fields after it apply only then.
	Map bool
	// Cases sampled per part. Zero: DefaultPerPart.
	PerPart int
	// Parts to keep, by name or key. Empty: all.
	Only []string
	// Part key to name, from the names file.
	Names map[string]string
	// --name flags, "KEY=NAME", applied over Names.
	Rename []string
	// The key that holds the part name in the source's records.
	PartKey string
	// Paths left out of the comparison.
	IgnoredFields []string
}

// PartPlan is one part of a map plan.
type PartPlan struct {
	Keys []string
	Name string
	// "name", "fingerprint" or "mixed".
	Source string
	// Replayable cases the part holds in the file, and how many were sampled.
	Available int
	Sampled   int
	// The paths the part is compared on.
	Paths []string
	// Why the part will get no comparison, or empty: parts.ReasonFreeText,
	// parts.ReasonNoField or parts.ReasonTooFew.
	Reason string
	// This part alone, for display. Each is rounded up, so they can sum to
	// slightly more than the plan's estimate.
	Estimate types.Estimate
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
	// The sampled cases, replayable or not, oldest first. A map plan holds
	// replayable cases only, part by part in the order of Parts, so a run
	// that stops at its budget has finished the largest parts.
	Cases      []types.Case
	Treatments []types.Treatment
	// Endpoint key of the noise floor, or empty without one.
	FloorEndpoint  string
	FreeEndpoints  []string
	Settings       types.RunSettings
	Estimate       types.Estimate
	CatalogVersion string

	// Map plans only. Parts is the parts that will run, largest first.
	Parts []PartPlan
	// Every part the file holds, selected or not, for the names file.
	AllParts []parts.Part
	// What the names passed in did not match.
	PartNotes []string
	// Cases the map leaves out before sampling, by reason: ezswap's
	// not-replayable reasons, and "tools_not_offered" for calls whose tools a
	// replay would strip.
	LeftOut map[string]int
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
	if options.Map {
		sample = options.PerPart
		if sample == 0 {
			sample = DefaultPerPart
		}
		if sample < MinPerPart || sample > MaxPerPart {
			return nil, &PlanError{Message: fmt.Sprintf("--per-part must be a whole number from %d to %d.", MinPerPart, MaxPerPart)}
		}
	}
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
	if options.Map {
		budgetUSD = DefaultMapBudgetUSD
	}
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
		PartKey: options.PartKey,
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
	var sampled []types.Case
	var mapParts []mapPart
	var allParts []parts.Part
	var partNotes []string
	leftOut := map[string]int{}
	if options.Map {
		var err error
		mapParts, allParts, partNotes, leftOut, err = planParts(all, sample, options)
		if err != nil {
			return nil, err
		}
		for _, part := range mapParts {
			sampled = append(sampled, part.cases...)
		}
	} else {
		sampled = SampleCases(all, sample)
	}

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
	estimateOf := func(cases []types.Case) types.Estimate {
		estimate := pricing.EstimateRun(cases, treatments, pricing.EstimateOptions{
			NoiseFloor: noiseFloor && !freeFloor,
			IsFree:     func(t types.Treatment) bool { return isFree(t.Endpoint) },
			At:         options.At,
		}, options.Pricing)
		if freeFloor {
			for _, arm := range FloorArms {
				estimate.PerArm[arm] = types.ArmEstimate{ExpectedUSD: 0, UpperBoundUSD: 0}
			}
		}
		return estimate
	}
	estimate := estimateOf(sampled)
	var partPlans []PartPlan
	for _, part := range mapParts {
		part.plan.Estimate = estimateOf(part.cases)
		partPlans = append(partPlans, part.plan)
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
	settings := types.RunSettings{Sample: sample, TrackedFields: append([]string{}, tracked...), NoiseFloor: noiseFloor, BudgetUSD: budgetUSD}
	if options.Map {
		settings.PerPart = &sample
		settings.IgnoredFields = append([]string(nil), options.IgnoredFields...)
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
		Settings:       settings,
		Estimate:       estimate,
		CatalogVersion: options.Pricing.CatalogVersion(),
		Parts:          partPlans,
		AllParts:       allParts,
		PartNotes:      partNotes,
		LeftOut:        leftOut,
	}, nil
}

type mapPart struct {
	plan  PartPlan
	cases []types.Case
}

// planParts groups the file's replayable cases into parts, keeps the ones
// asked for, and samples each. Cases that cannot be replayed, and cases whose
// tools a replay would strip, are left out and counted.
func planParts(all []types.Case, perPart int, options PlanOptions) ([]mapPart, []parts.Part, []string, map[string]int, error) {
	leftOut := map[string]int{}
	var eligible []types.Case
	for _, kase := range all {
		switch {
		case kase.NotReplayable != nil:
			leftOut[string(*kase.NotReplayable)]++
		case slices.Contains(kase.Missing, cases.ToolsNotOffered):
			leftOut[cases.ToolsNotOffered]++
		default:
			eligible = append(eligible, kase)
		}
	}
	names := map[string]string{}
	for key, name := range options.Names {
		names[key] = name
	}
	allParts, notes := parts.Assign(eligible, names)
	if len(options.Rename) > 0 {
		renamed, err := parts.Rename(allParts, options.Rename)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		for key, name := range renamed {
			names[key] = name
		}
		allParts, _ = parts.Assign(eligible, names)
	}
	selected, err := parts.Select(allParts, options.Only)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := parts.CheckFingerprints(selected); err != nil {
		return nil, nil, nil, nil, err
	}
	out := make([]mapPart, 0, len(selected))
	for _, part := range selected {
		members := make([]types.Case, len(part.Cases))
		for i, index := range part.Cases {
			members[i] = eligible[index]
		}
		sampled := SampleCases(members, perPart)
		outputs := make([]*string, len(sampled))
		for i, kase := range sampled {
			outputs[i] = kase.Production.Output
		}
		paths, reason := parts.ComparedPaths(outputs, options.TrackedFields, options.IgnoredFields)
		if reason == "" && len(sampled) < parts.MinCases {
			reason = parts.ReasonTooFew
		}
		out = append(out, mapPart{
			plan: PartPlan{
				Keys: part.Keys, Name: part.Name, Source: part.Source,
				Available: len(members), Sampled: len(sampled), Paths: paths, Reason: reason,
			},
			cases: sampled,
		})
	}
	return out, allParts, notes, leftOut, nil
}
