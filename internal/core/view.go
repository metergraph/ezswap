package core

// The experiment view built from a run file: the same shape Metergraph's
// hosted product uses, so every statistic and sentence in core reads a local
// run exactly as it reads a hosted one. The control is
// each case's recorded production output; the arms are the run's
// treatments; the noise floor is production's model replayed twice.

import (
	"slices"

	"github.com/metergraph/replay/internal/types"
)

// FloorArms is FLOOR_ARMS: the noise floor's two replays.
var FloorArms = []string{types.ArmFloorA1, types.ArmFloorA2}

func strOrNil(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func numOrNil(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func usageField(u *types.Usage, read func(*types.Usage) *float64) any {
	if u == nil {
		return nil
	}
	return numOrNil(read(u))
}

func outputTokens(u *types.Usage) *float64 { return u.OutputTokens }
func inputTokens(u *types.Usage) *float64  { return u.InputTokens }
func cachedTokens(u *types.Usage) *float64 { return u.CachedInputTokens }

// ReplayOutcome is replayOutcome: a replay as the statistics read it, or
// nil for one that never ran (skipped when the run stopped), which counts as
// no result rather than an error. Facts are taken from the full output with
// the tracked fields.
func ReplayOutcome(replay *types.Replay, trackedFields []string) *Object {
	if replay == nil || replay.Status == "skipped" {
		return nil
	}
	var errorCode any
	if replay.Status != "ok" {
		errorCode = "failed"
		if replay.Error != nil && replay.Error.Code != "" {
			errorCode = replay.Error.Code
		}
	}
	var facts any
	if errorCode == nil {
		facts = OutputFacts(strOrNil(replay.Output), trackedFields)
	}
	return obj(
		"output", strOrNil(replay.Output),
		"error", errorCode,
		"cost_usd", numOrNil(replay.CostUSD),
		"cost_search_usd", nil,
		"search_units", nil,
		"latency_ms", numOrNil(replay.LatencyMs),
		"finish_reason", strOrNil(replay.FinishReason),
		"output_tokens", usageField(replay.Usage, outputTokens),
		"input_tokens", usageField(replay.Usage, inputTokens),
		"cache_read_tokens", usageField(replay.Usage, cachedTokens),
		// input_includes_cache_read is left out, which reads as true: replies come through an
		// OpenAI-compatible endpoint, whose input count includes cached tokens.
		"refusal", replay.Refusal,
		"facts", facts,
		"temperature_dropped", slices.Contains(replay.Flags, types.FlagTemperatureDropped),
		"response_format_dropped", slices.Contains(replay.Flags, types.FlagResponseFormatDropped),
		"output_truncated", false,
	)
}

// ControlRecord is controlRecord: production's call as the case's control.
// Its cache convention is not recorded, so it is unknown.
func ControlRecord(c *types.Case, trackedFields []string) *Object {
	p := c.Production
	return obj(
		"output", strOrNil(p.Output),
		"cost_usd", numOrNil(p.CostUSD),
		"cost_search_usd", nil,
		"latency_ms", numOrNil(p.LatencyMs),
		"finish_reason", strOrNil(p.FinishReason),
		"output_tokens", usageField(p.Usage, outputTokens),
		"input_tokens", usageField(p.Usage, inputTokens),
		"cache_read_tokens", usageField(p.Usage, cachedTokens),
		"input_includes_cache_read", nil,
		"refusal", p.Refusal,
		"facts", OutputFacts(strOrNil(p.Output), trackedFields),
		"output_truncated", false,
		"model", strOrNil(c.Model),
		"provider", strOrNil(c.Provider),
		"settings", caseSettings(c),
	)
}

func caseSettings(c *types.Case) *Object {
	settings := NewObject()
	if c.Params.Temperature != nil {
		settings.Set("temperature", *c.Params.Temperature)
	}
	if c.Params.MaxTokens != nil {
		settings.Set("max_tokens", *c.Params.MaxTokens)
	}
	if c.Params.ReasoningEffort != nil {
		settings.Set("reasoning_effort", *c.Params.ReasoningEffort)
	}
	return settings
}

func treatmentSettings(t *types.Treatment) *Object {
	settings := NewObject()
	if t.Settings.Temperature != nil {
		settings.Set("temperature", *t.Settings.Temperature)
	}
	if t.Settings.MaxTokens != nil {
		settings.Set("max_tokens", *t.Settings.MaxTokens)
	}
	if t.Settings.ReasoningEffort != nil {
		settings.Set("reasoning_effort", *t.Settings.ReasoningEffort)
	}
	return settings
}

func looseString(v any) *string {
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

func resultView(outcome *Object) ResultView {
	field := func(key string) any { v, _ := outcome.Get(key); return v }
	return ResultView{
		Output:                          looseString(field("output")),
		CostUSD:                         numPtr(field("cost_usd")),
		CostSearchUSD:                   numPtr(field("cost_search_usd")),
		SearchUnits:                     numPtr(field("search_units")),
		LatencyMs:                       numPtr(field("latency_ms")),
		FinishReason:                    looseString(field("finish_reason")),
		OutputTokens:                    numPtr(field("output_tokens")),
		Error:                           looseString(field("error")),
		TemperatureDropped:              truthy(field("temperature_dropped")),
		TemperatureIgnoredWithReasoning: false,
		ResponseFormatDropped:           truthy(field("response_format_dropped")),
		OutputTruncated:                 truthy(field("output_truncated")),
		Refusal:                         truthy(field("refusal")),
	}
}

func mostCommon(values []*string) *string {
	counts := map[string]int{}
	var order []string
	for _, v := range values {
		if v != nil && *v != "" {
			if _, ok := counts[*v]; !ok {
				order = append(order, *v)
			}
			counts[*v]++
		}
	}
	var best *string
	bestCount := 0
	for _, v := range order {
		if counts[v] > bestCount {
			best = ptr(v)
			bestCount = counts[v]
		}
	}
	return best
}

// BuildViewOptions are buildView's options.
type BuildViewOptions struct {
	// ExcludeCases leaves out `cases` and `fields`, and the floor's per-case
	// lists.
	ExcludeCases bool
}

// BuildView is buildView: the experiment view of a run. Only replayable
// cases are statistics' cases; every sampled case is listed. It refuses a
// tracked path no statistic can read.
func BuildView(run *types.RunFile, options BuildViewOptions) (*ExperimentView, error) {
	includeCases := !options.ExcludeCases
	tracked := run.Settings.TrackedFields
	if tracked == nil {
		tracked = []string{}
	}
	for _, path := range tracked {
		if _, err := ParseJSONPath(path); err != nil { // no statistic can read it
			return nil, err
		}
	}

	type replayKey struct{ caseID, arm string }
	replays := map[replayKey]*types.Replay{}
	for i := range run.Replays {
		r := &run.Replays[i]
		key := replayKey{r.CaseID, r.Arm}
		if _, ok := replays[key]; !ok {
			replays[key] = r
		}
	}
	outcomes := map[replayKey]*Object{}
	outcomeOf := func(caseID, arm string) *Object {
		key := replayKey{caseID, arm}
		if o, ok := outcomes[key]; ok {
			return o
		}
		o := ReplayOutcome(replays[key], tracked)
		outcomes[key] = o
		return o
	}

	var replayed []*types.Case
	records := map[string]*Object{}
	for i := range run.Cases {
		c := &run.Cases[i]
		if c.NotReplayable == nil {
			replayed = append(replayed, c)
		}
		records[c.ID] = ControlRecord(c, tracked)
	}
	controlOf := func(c *types.Case) *Object { return records[c.ID] }
	controls := make([]any, len(replayed))
	for i, c := range replayed {
		controls[i] = ControlOutcome(controlOf(c))
	}

	arms := []ArmView{}
	for i := range run.Treatments {
		t := &run.Treatments[i]
		ran := []any{}
		aligned := []any{}
		for _, c := range replayed {
			if o := outcomeOf(c.ID, t.ID); o != nil {
				ran = append(ran, o)
				aligned = append(aligned, ControlOutcome(controlOf(c)))
			}
		}
		summary := Summarize(ran, aligned)
		summary.TemperatureIgnoredWithReasoning = ptr(0)
		// The control over exactly the cases this arm returned on, so the two compare.
		returned := []any{}
		for j, control := range aligned {
			if e, _ := get(ran[j], "error"); !truthy(e) {
				returned = append(returned, control)
			}
		}
		arms = append(arms, ArmView{
			ID:             t.ID,
			Label:          t.Label,
			Model:          t.Model,
			Provider:       t.Endpoint,
			Settings:       treatmentSettings(t),
			Summary:        summary,
			ControlSummary: Summarize(returned, nil),
		})
	}

	var fields map[string]map[string][]any
	if includeCases {
		fields = map[string]map[string][]any{}
		for _, path := range tracked {
			series := map[string][]any{}
			values := make([]any, len(controls))
			for i, control := range controls {
				values[i] = FieldValue(control, path)
			}
			series["control"] = values
			for i := range run.Treatments {
				t := &run.Treatments[i]
				values := make([]any, len(replayed))
				for j, c := range replayed {
					values[j] = FieldValue(nilOutcome(outcomeOf(c.ID, t.ID)), path)
				}
				series[t.Label] = values
			}
			fields[path] = series
		}
	}

	hasFloor := run.Settings.NoiseFloor
	for _, r := range run.Replays {
		if r.Arm == types.ArmFloorA1 || r.Arm == types.ArmFloorA2 {
			hasFloor = true
		}
	}
	var cases []CaseView
	if includeCases {
		cases = []CaseView{}
		for i := range run.Cases {
			c := &run.Cases[i]
			control := controlOf(c)
			field := func(key string) any { v, _ := control.Get(key); return v }
			armViews := map[string]ResultView{}
			for j := range run.Treatments {
				t := &run.Treatments[j]
				if o := outcomeOf(c.ID, t.ID); o != nil {
					armViews[t.Label] = resultView(o)
				}
			}
			traceID := c.ID
			if c.TraceID != nil {
				traceID = *c.TraceID
			}
			status := "replayed"
			if c.NotReplayable != nil {
				status = "not_replayable"
			}
			var reason *string
			if c.NotReplayable != nil {
				reason = ptr(string(*c.NotReplayable))
			}
			view := CaseView{
				TraceID:      traceID,
				CaseID:       c.ID,
				Status:       status,
				Reason:       reason,
				InputPreview: c.InputPreview,
				Limitations:  append([]string{}, c.Missing...),
				Control: CaseControlView{
					Output:       looseString(field("output")),
					CostUSD:      numPtr(field("cost_usd")),
					LatencyMs:    numPtr(field("latency_ms")),
					FinishReason: looseString(field("finish_reason")),
					OutputTokens: numPtr(field("output_tokens")),
					Refusal:      truthy(field("refusal")),
				},
				Arms: armViews,
			}
			if hasFloor {
				floor := &CaseFloor{}
				if a1 := outcomeOf(c.ID, types.ArmFloorA1); a1 != nil {
					floor.A1 = ptr(resultView(a1))
				}
				if a2 := outcomeOf(c.ID, types.ArmFloorA2); a2 != nil {
					floor.A2 = ptr(resultView(a2))
				}
				view.NoiseFloor = floor
			}
			cases = append(cases, view)
		}
	}

	floorCases := make([]any, len(run.Cases))
	results := []any{}
	for position := range run.Cases {
		c := &run.Cases[position]
		traceID := c.ID
		if c.TraceID != nil {
			traceID = *c.TraceID
		}
		status := "replayed"
		if c.NotReplayable != nil {
			status = "not_replayable"
		}
		floorCases[position] = obj("position", float64(position), "trace_id", traceID, "status", status, "control", controlOf(c))
		for i, arm := range FloorArms {
			if o := outcomeOf(c.ID, arm); o != nil {
				results = append(results, obj("position", float64(position), "replay", float64(i+1), "outcome", o))
			}
		}
	}
	var plan any
	if hasFloor {
		models := []any{}
		for _, c := range replayed {
			if outcomeOf(c.ID, types.ArmFloorA1) != nil || outcomeOf(c.ID, types.ArmFloorA2) != nil {
				if c.Model != nil && *c.Model != "" {
					models = append(models, *c.Model)
				}
			}
		}
		plan = obj("replays", float64(len(FloorArms)), "gateway_models", models)
	}
	trackedLoose := make([]any, len(tracked))
	for i, p := range tracked {
		trackedLoose[i] = p
	}
	floorInput := obj("noise_floor", plan, "cases", floorCases, "noise_results", results, "tracked_fields", trackedLoose)

	modelsSeen := NewObject()
	for _, c := range run.Cases {
		if c.Model != nil && *c.Model != "" {
			n, _ := modelsSeen.Get(*c.Model)
			count, _ := n.(float64)
			modelsSeen.Set(*c.Model, count+1)
		}
	}
	settingsSeen := NewObject()
	for _, c := range replayed {
		settings := caseSettings(c)
		for _, key := range settings.Keys() {
			value, _ := settings.Get(key)
			existing, _ := settingsSeen.Get(key)
			values, _ := existing.([]any)
			if values == nil {
				values = []any{}
			}
			if !slices.ContainsFunc(values, func(v any) bool { return sameValueZero(v, value) }) {
				values = append(values, value)
			}
			settingsSeen.Set(key, values)
		}
	}

	models := make([]*string, len(replayed))
	providers := make([]*string, len(replayed))
	for i, c := range replayed {
		models[i], providers[i] = c.Model, c.Provider
	}
	return &ExperimentView{
		Status:  "done",
		Stopped: run.Stopped,
		Sample: Sample{
			Size:          run.Settings.Sample,
			Cases:         len(run.Cases),
			Replayed:      len(replayed),
			NotReplayable: len(run.Cases) - len(replayed),
		},
		TrackedFields: append([]string{}, tracked...),
		EstimateUSD:   run.Estimate.ExpectedUSD,
		BudgetUSD:     run.Settings.BudgetUSD,
		SpentUSD:      run.SpentUSD,
		CreatedAt:     run.CreatedAt,
		Control: ControlView{
			Model:        mostCommon(models),
			Provider:     mostCommon(providers),
			SettingsSeen: settingsSeen,
			ModelsSeen:   modelsSeen,
			// Same keys compares a treatment with the control, so the control has none.
			Summary: Summarize(controls, nil),
		},
		Arms:       arms,
		Fields:     fields,
		Cases:      cases,
		NoiseFloor: NoiseFloorView(floorInput, includeCases),
	}, nil
}

// nilOutcome keeps a missing outcome a nil interface rather than a nil *Object.
func nilOutcome(o *Object) any {
	if o == nil {
		return nil
	}
	return o
}

// sameValueZero is Array.prototype.includes' equality.
func sameValueZero(a, b any) bool {
	x, xn := a.(float64)
	y, yn := b.(float64)
	if xn && yn && x != x && y != y {
		return true
	}
	return strictEqual(a, b)
}
