package run

// Replay a planned run: every replayable case on every treatment, and twice
// on production's model for the noise floor.
//
// Budget: each call reserves its upper bound before it is sent (free
// endpoints reserve nothing), and the run admits a case only when all of the
// case's calls fit in the budget next to what is spent and still reserved.
// So a budget that runs out drops whole cases, never one column. A case that
// does not fit waits for calls in flight to settle; when nothing is in flight
// and it still does not fit, the run stops ("budget"). A call that cost more
// than it reserved means the price catalog is wrong for that model on that
// endpoint (a provider charging more than its listed price, say): the run
// multiplies the model's later reservations by what the overrun shows, with
// headroom, and records it in Repriced. If the model overruns again after
// maxRaises raises, or a call costs more than maxOverrun times its
// reservation, the run stops at once ("reservation_exceeded"). A stop
// signal ends it the same way ("interrupted"), after the calls in flight
// finish.
//
// Cost of a call: what the endpoint reported, else the catalog price of the
// usage it returned, else its reservation (a reply with no usable count, or a
// failure that may have reached the model). Free endpoints cost zero.
//
// Concurrency: one goroutine per call in flight runs only the provider call
// and hands its outcome back. Everything else (admission, reservations,
// settling, recording, progress and events) happens on the goroutine that
// called ExecuteRun, one outcome at a time. So the callbacks are never called concurrently.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/metergraph/ezswap/internal/jsnum"
	"github.com/metergraph/ezswap/internal/pricing"
	"github.com/metergraph/ezswap/internal/types"
)

const DefaultRunConcurrency = 4

// FloorArms are the two noise-floor replays, in the order they run.
var FloorArms = [2]string{types.ArmFloorA1, types.ArmFloorA2}

const epsilon = 1e-9

// Why a run ended early (RunFile.Stopped).
const (
	StopBudget              = "budget"
	StopInterrupted         = "interrupted"
	StopReservationExceeded = "reservation_exceeded"
)

// How often a model's reservations may be raised before an overrun stops the
// run, the headroom each raise adds, and the overrun past which the run stops
// at once.
const (
	maxRaises       = 2
	reserveHeadroom = 1.25
	maxOverrun      = 4.0
)

// Where a replay's cost came from (Replay.CostSource).
const (
	CostSourceEndpoint    = "endpoint"
	CostSourceCatalog     = "catalog"
	CostSourceReservation = "reservation"
	CostSourceFree        = "free"
)

type Progress struct {
	// Replays recorded (settled or skipped) out of every replay the run holds.
	Done  int
	Total int
	// Rounded to $0.00000001.
	SpentUSD    float64
	ReservedUSD float64
	// The budget the run admits cases against.
	BudgetUSD float64
	// Calls sent and not yet settled.
	InFlight int
}

// EventKind names what just happened in a run.
type EventKind string

const (
	// A case fit the budget: its calls are reserved and queued.
	EventCaseStarted EventKind = "case_started"
	// A call was sent.
	EventCallStarted EventKind = "call_started"
	// A replay was recorded: a call that settled, or one that was skipped.
	// Every replay in the run file gets exactly one.
	EventCallFinished EventKind = "call_finished"
	// Every arm of a case is recorded.
	EventCaseFinished EventKind = "case_finished"
	// The run will send no more calls (or a stronger reason replaced the
	// first one). Calls in flight still settle after it.
	EventStopped EventKind = "stopped"
	// The run is over; the run file is about to be returned.
	EventFinished EventKind = "finished"
)

// Event is one step of a run, for a live view. Fields that do not apply to
// the kind are zero.
type Event struct {
	Kind   EventKind
	CaseID string
	// Call events: the arm, the model sent and the endpoint key. Model is
	// empty for a replay skipped before it had a call.
	Arm      string
	Model    string
	Endpoint string
	// call_started: what the call reserved, nil when it cannot be priced.
	ReserveUSD *float64
	// call_finished: a copy of the recorded replay (status, cost, cost
	// source, latency, error).
	Replay *types.Replay
	// stopped and finished: why the run stopped, nil when it did not.
	Stopped *string
	// The run's totals after this event.
	Progress Progress
}

type ExecuteOptions struct {
	// One provider per endpoint key the plan names.
	Providers map[string]types.Provider
	Pricing   types.Pricing
	// Default: the plan's budget.
	BudgetUSD *float64
	// Cancelling it cancels calls in flight too, and stops the run as the
	// context passed to ExecuteRun does. Nil: calls in flight always finish.
	Hard        context.Context
	Concurrency int
	// Called after each settled call and once at the end.
	OnProgress func(Progress)
	// Called for every event, in order, on the goroutine running ExecuteRun.
	// It holds up the run while it runs: hand the event off rather than
	// draw in it.
	OnEvent func(Event)
	// Clock for createdAt and for pricing.
	Now         func() time.Time
	ToolVersion string
}

// ExecuteError is a run that cannot start.
type ExecuteError struct{ Message string }

func (e *ExecuteError) Error() string { return e.Message }

type item struct {
	item     *types.Case
	arm      string
	endpoint string
	call     *types.ReplayCall
	// Nil when the call cannot be priced (allowed unpriced) or never runs.
	reserve *float64
	// The model's reservation multiplier when this call was admitted.
	scale float64
	free  bool
	// Set when the replay is known before any call: why it is skipped.
	skip string
}

func round8(value float64) float64 {
	rounded := jsnum.Round(value*1e8) / 1e8
	if rounded == 0 {
		return 0 // never -0, which JSON.stringify writes as 0
	}
	return rounded
}

func ptr[T any](v T) *T { return &v }

var skipMessages = map[string]string{
	"budget_exhausted":     "not sent: the budget could not cover this case",
	"interrupted":          "not sent: the run was interrupted",
	"reservation_exceeded": "not sent: a call cost more than its reservation, so the run stopped",
	"floor_model_unknown":  "not sent: production's model is not recorded for this case",
}

func skipped(caseID, arm, code string) types.Replay {
	return types.Replay{
		CaseID: caseID,
		Arm:    arm,
		Status: "skipped",
		Flags:  []types.ReplayFlag{},
		Error:  &types.ReplayError{Code: code, Message: skipMessages[code]},
	}
}

// SettledCost is what a settled call cost, and where the figure came from.
type SettledCost struct {
	CostUSD    *float64
	CostSource *string
}

// SettleCost is what a settled call cost, and where the figure came from. An
// empty at prices at now.
func SettleCost(outcome types.ReplyOutcome, call types.ReplayCall, reserve *float64, free bool, prices types.Pricing, at string) SettledCost {
	if free {
		return SettledCost{CostUSD: ptr(0.0), CostSource: ptr(CostSourceFree)}
	}
	byReservation := func() SettledCost {
		if reserve == nil {
			return SettledCost{}
		}
		return SettledCost{CostUSD: ptr(*reserve), CostSource: ptr(CostSourceReservation)}
	}
	if outcome.Status == "ok" {
		if outcome.ReportedCostUSD != nil && finite(*outcome.ReportedCostUSD) {
			return SettledCost{CostUSD: ptr(*outcome.ReportedCostUSD), CostSource: ptr(CostSourceEndpoint)}
		}
		usage := outcome.Usage
		if usage != nil && usage.InputTokens != nil && usage.OutputTokens != nil {
			priced := prices.Price(types.PriceQuery{
				Model:             call.Model,
				InputTokens:       *usage.InputTokens,
				OutputTokens:      *usage.OutputTokens,
				CachedInputTokens: usage.CachedInputTokens,
				// An OpenAI-compatible endpoint's prompt count includes cached tokens.
				InputIncludesCacheRead: ptr(true),
				At:                     at,
			})
			if priced != nil {
				return SettledCost{CostUSD: priced, CostSource: ptr(CostSourceCatalog)}
			}
		}
		return byReservation()
	}
	for _, flag := range outcome.Flags {
		if flag == types.FlagMaybeBilled {
			return byReservation()
		}
	}
	return SettledCost{CostUSD: ptr(0.0)}
}

func crashed() types.ReplyOutcome {
	return types.ReplyOutcome{
		Status: "failed",
		Flags:  []types.ReplayFlag{types.FlagMaybeBilled},
		Error:  &types.ReplayError{Code: "provider_error", Message: "the call failed unexpectedly"},
	}
}

// isoTime is Date.prototype.toISOString.
func isoTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

type settled struct {
	entry   *item
	outcome types.ReplyOutcome
}

// ExecuteRun replays the plan. Cancelling ctx stops new calls; calls in flight
// finish and are recorded, and the run file holds what exists, with Stopped
// "interrupted". The error is only ever an *ExecuteError, before any call.
func ExecuteRun(ctx context.Context, plan *RunPlan, options ExecuteOptions) (*types.RunFile, error) {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	createdAt := isoTime(now())
	at := createdAt
	budget := plan.Settings.BudgetUSD
	if options.BudgetUSD != nil {
		budget = *options.BudgetUSD
	}
	concurrency := options.Concurrency
	if concurrency == 0 {
		concurrency = DefaultRunConcurrency
	}
	concurrency = max(1, concurrency)
	prices := options.Pricing
	hard := options.Hard
	if hard == nil {
		hard = context.Background()
	}
	free := map[string]bool{}
	for _, endpoint := range plan.FreeEndpoints {
		free[endpoint] = true
	}

	var endpoints []string
	seenEndpoint := map[string]bool{}
	for _, t := range plan.Treatments {
		if !seenEndpoint[t.Endpoint] {
			seenEndpoint[t.Endpoint] = true
			endpoints = append(endpoints, t.Endpoint)
		}
	}
	if plan.FloorEndpoint != "" && !seenEndpoint[plan.FloorEndpoint] {
		endpoints = append(endpoints, plan.FloorEndpoint)
	}
	for _, endpoint := range endpoints {
		if options.Providers[endpoint] == nil {
			return nil, &ExecuteError{Message: fmt.Sprintf(`No provider for the endpoint "%s".`, endpoint)}
		}
	}

	var armOrder []string
	for _, t := range plan.Treatments {
		armOrder = append(armOrder, t.ID)
	}
	if plan.FloorEndpoint != "" {
		armOrder = append(armOrder, FloorArms[:]...)
	}
	var replayable []*types.Case
	for i := range plan.Cases {
		if plan.Cases[i].NotReplayable == nil {
			replayable = append(replayable, &plan.Cases[i])
		}
	}

	// A model's reservations are multiplied by its scale once it has cost
	// more than the catalog said: by endpoint and model.
	scales := map[string]float64{}
	raises := map[string]int{}
	var repriced []string
	scaleKey := func(endpoint, model string) string { return endpoint + "\x00" + model }
	scaleOf := func(endpoint, model string) float64 {
		if s, ok := scales[scaleKey(endpoint, model)]; ok {
			return s
		}
		return 1
	}
	scaled := func(reserve *float64, endpoint, model string) (*float64, float64) {
		s := scaleOf(endpoint, model)
		if reserve == nil {
			return nil, s
		}
		return ptr(*reserve * s), s
	}

	productionInput := func(c *types.Case) *float64 {
		if c.Production.Usage == nil {
			return nil
		}
		return c.Production.Usage.InputTokens
	}
	itemsFor := func(c *types.Case) []*item {
		var items []*item
		for _, t := range plan.Treatments {
			call := pricing.TreatmentCall(*c, t)
			isFree := free[t.Endpoint]
			reserve, scale := ptr(0.0), 1.0
			if !isFree {
				reserve, scale = scaled(pricing.UpperBoundUSD(call, prices, productionInput(c), at), t.Endpoint, call.Model)
			}
			items = append(items, &item{item: c, arm: t.ID, endpoint: t.Endpoint, call: &call, reserve: reserve, scale: scale, free: isFree})
		}
		if plan.FloorEndpoint != "" {
			endpoint := plan.FloorEndpoint
			isFree := free[endpoint]
			model := pricing.FloorModel(*c, prices, at)
			if isFree && c.Model != nil && strings.TrimSpace(*c.Model) != "" {
				// A local server names models its own way: send the model as
				// production named it, not the catalog's creator/model id.
				local := strings.TrimSpace(*c.Model)
				model = &local
			}
			for _, arm := range FloorArms {
				if model == nil {
					items = append(items, &item{item: c, arm: arm, endpoint: endpoint, free: isFree, skip: "floor_model_unknown"})
					continue
				}
				call := pricing.FloorCall(*c, *model)
				reserve, scale := ptr(0.0), 1.0
				if !isFree {
					reserve, scale = scaled(pricing.UpperBoundUSD(call, prices, productionInput(c), at), endpoint, call.Model)
				}
				items = append(items, &item{item: c, arm: arm, endpoint: endpoint, call: &call, reserve: reserve, scale: scale, free: isFree})
			}
		}
		return items
	}

	type key struct{ caseID, arm string }
	results := map[key]types.Replay{}
	recordedPerCase := map[string]int{}
	total := len(replayable) * len(armOrder)
	done := 0
	spent := 0.0
	reserved := 0.0
	inFlight := 0
	var stopped *string

	progress := func() Progress {
		return Progress{
			Done: done, Total: total, SpentUSD: round8(spent), ReservedUSD: round8(reserved),
			BudgetUSD: budget, InFlight: inFlight,
		}
	}
	emit := func(event Event) {
		if options.OnEvent == nil {
			return
		}
		event.Progress = progress()
		if event.Kind == EventStopped || event.Kind == EventFinished {
			if stopped != nil {
				event.Stopped = ptr(*stopped)
			}
		}
		options.OnEvent(event)
	}
	reportProgress := func() {
		if options.OnProgress != nil {
			options.OnProgress(progress())
		}
	}
	rank := map[string]int{StopBudget: 1, StopInterrupted: 2, StopReservationExceeded: 3}
	stop := func(reason string) {
		if stopped == nil || rank[reason] > rank[*stopped] {
			stopped = ptr(reason)
			emit(Event{Kind: EventStopped})
		}
	}
	stoppedHard := func() bool {
		return stopped != nil && (*stopped == StopInterrupted || *stopped == StopReservationExceeded)
	}
	interrupted := func() bool { return ctx.Err() != nil || hard.Err() != nil }

	record := func(replay types.Replay, model, endpoint string) {
		results[key{replay.CaseID, replay.Arm}] = replay
		done++
		recordedPerCase[replay.CaseID]++
		copied := replay
		emit(Event{Kind: EventCallFinished, CaseID: replay.CaseID, Arm: replay.Arm, Model: model, Endpoint: endpoint, Replay: &copied})
		if recordedPerCase[replay.CaseID] == len(armOrder) {
			emit(Event{Kind: EventCaseFinished, CaseID: replay.CaseID})
		}
	}
	modelOf := func(entry *item) string {
		if entry.call == nil {
			return ""
		}
		return entry.call.Model
	}

	queue := append([]*types.Case{}, replayable...)
	var pending []*item
	outcomes := make(chan settled, concurrency)
	admitting := true

	start := func(entry *item) {
		call := *entry.call
		provider := options.Providers[entry.endpoint]
		inFlight++
		emit(Event{Kind: EventCallStarted, CaseID: entry.item.ID, Arm: entry.arm, Model: call.Model, Endpoint: entry.endpoint, ReserveUSD: entry.reserve})
		go func() {
			var outcome types.ReplyOutcome
			func() {
				defer func() {
					// A provider never panics for a failed call; anything else may have reached the model.
					if recover() != nil {
						outcome = crashed()
					}
				}()
				outcome = provider.Complete(hard, call)
			}()
			outcomes <- settled{entry: entry, outcome: outcome}
		}()
	}

	settle := func(result settled) {
		entry, outcome := result.entry, result.outcome
		inFlight--
		cost := SettleCost(outcome, *entry.call, entry.reserve, entry.free, prices, at)
		if entry.reserve != nil {
			reserved -= *entry.reserve
		}
		if cost.CostUSD != nil {
			spent += *cost.CostUSD
		}
		var costUSD *float64
		if cost.CostUSD != nil {
			costUSD = ptr(round8(*cost.CostUSD))
		}
		flags := append([]types.ReplayFlag{}, outcome.Flags...)
		record(types.Replay{
			CaseID:       entry.item.ID,
			Arm:          entry.arm,
			Status:       outcome.Status,
			Output:       outcome.Output,
			FinishReason: outcome.FinishReason,
			Refusal:      outcome.Refusal,
			Usage:        outcome.Usage,
			CostUSD:      costUSD,
			CostSource:   cost.CostSource,
			LatencyMs:    outcome.LatencyMs,
			Flags:        flags,
			Error:        outcome.Error,
		}, entry.call.Model, entry.endpoint)
		if entry.reserve != nil && cost.CostUSD != nil && *cost.CostUSD > *entry.reserve+epsilon {
			k := scaleKey(entry.endpoint, entry.call.Model)
			current := scaleOf(entry.endpoint, entry.call.Model)
			switch {
			case *entry.reserve <= 0 || *cost.CostUSD > *entry.reserve*maxOverrun:
				// Far beyond the catalog: something other than a price is wrong.
				stop(StopReservationExceeded)
			case entry.scale < current:
				// Admitted before the last raise, which already covers it.
			case raises[k] >= maxRaises:
				stop(StopReservationExceeded)
			default:
				next := entry.scale * (*cost.CostUSD / *entry.reserve) * reserveHeadroom
				if next > current {
					if raises[k] == 0 {
						repriced = append(repriced, k)
					}
					scales[k] = next
					raises[k]++
				}
			}
		}
		reportProgress()
	}

	for {
		for inFlight < concurrency {
			if interrupted() && (len(pending) > 0 || len(queue) > 0) {
				stop(StopInterrupted)
			}
			if stoppedHard() {
				break
			}
			if len(pending) > 0 {
				next := pending[0]
				pending = pending[1:]
				start(next)
				continue
			}
			if !admitting || len(queue) == 0 {
				break
			}
			c := queue[0]
			items := itemsFor(c)
			need := 0.0
			for _, entry := range items {
				if entry.skip == "" && entry.reserve != nil {
					need += *entry.reserve
				}
			}
			if spent+reserved+need > budget+epsilon {
				// Calls in flight may settle below what they hold: wait for them first.
				if inFlight > 0 {
					break
				}
				stop(StopBudget)
				admitting = false
				break
			}
			queue = queue[1:]
			reserved += need
			emit(Event{Kind: EventCaseStarted, CaseID: c.ID})
			for _, entry := range items {
				if entry.skip != "" {
					record(skipped(entry.item.ID, entry.arm, entry.skip), "", entry.endpoint)
				} else {
					pending = append(pending, entry)
				}
			}
		}
		if inFlight == 0 {
			break
		}
		settle(<-outcomes)
	}

	// Whatever was not sent is recorded as skipped, with the reason.
	reason := StopInterrupted
	if stopped != nil {
		reason = *stopped
	}
	code := reason
	if reason == StopBudget {
		code = "budget_exhausted"
	}
	for _, entry := range pending {
		if entry.reserve != nil {
			reserved -= *entry.reserve
		}
		record(skipped(entry.item.ID, entry.arm, code), modelOf(entry), entry.endpoint)
	}
	for _, c := range queue {
		for _, arm := range armOrder {
			record(skipped(c.ID, arm, code), "", "")
		}
	}
	reportProgress()

	replays := []types.Replay{}
	for _, c := range replayable {
		for _, arm := range armOrder {
			if replay, ok := results[key{c.ID, arm}]; ok {
				replays = append(replays, replay)
			}
		}
	}

	toolVersion := options.ToolVersion
	if toolVersion == "" {
		toolVersion = "0.0.0"
	}
	treatments := make([]types.Treatment, len(plan.Treatments))
	copy(treatments, plan.Treatments)
	settings := plan.Settings
	settings.TrackedFields = append([]string{}, plan.Settings.TrackedFields...)
	settings.BudgetUSD = budget
	input := plan.Input
	cases := plan.Cases
	if cases == nil {
		cases = []types.Case{}
	}
	run := &types.RunFile{
		Schema:     types.RunSchema,
		CreatedAt:  createdAt,
		Tool:       types.Tool{Name: "ezswap", Version: toolVersion},
		Input:      input,
		Catalog:    types.Catalog{Version: plan.CatalogVersion},
		Settings:   settings,
		Treatments: treatments,
		Cases:      cases,
		Replays:    replays,
		Estimate:   plan.Estimate,
		SpentUSD:   round8(spent),
		Stopped:    stopped,
	}
	for _, k := range repriced {
		endpoint, model, _ := strings.Cut(k, "\x00")
		run.Repriced = append(run.Repriced, types.Repricing{Model: model, Endpoint: endpoint, Factor: jsnum.Round(scales[k]*100) / 100})
	}
	emit(Event{Kind: EventFinished})
	return run, nil
}
