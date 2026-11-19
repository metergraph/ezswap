package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/metergraph/replay/demo"
	"github.com/metergraph/replay/internal/core"
	"github.com/metergraph/replay/internal/explore"
	"github.com/metergraph/replay/internal/parts"
	"github.com/metergraph/replay/internal/pricing"
	"github.com/metergraph/replay/internal/providers"
	"github.com/metergraph/replay/internal/report"
	"github.com/metergraph/replay/internal/report/term"
	"github.com/metergraph/replay/internal/run"
	"github.com/metergraph/replay/internal/tui"
	"github.com/metergraph/replay/internal/types"
)

// ---------------------------------------------------------------------------
// Options

var wholeNumber = regexp.MustCompile(`^\d+$`)

func intOption(value string, given bool, name string, lo, hi, fallback int) (int, error) {
	if !given {
		return fallback, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if !wholeNumber.MatchString(strings.TrimSpace(value)) || err != nil || n < lo || n > hi {
		return 0, usagef(fmt.Sprintf("--%s must be a whole number from %d to %d.", name, lo, hi))
	}
	return n, nil
}

var dollars = regexp.MustCompile(`^\d+(\.\d+)?$`)

func budgetOption(value string, given bool, fallback float64) (float64, error) {
	if !given {
		return fallback, nil
	}
	text := strings.TrimPrefix(strings.TrimSpace(value), "$")
	n, err := strconv.ParseFloat(text, 64)
	if !dollars.MatchString(text) || err != nil || math.IsInf(n, 0) {
		return 0, usagef("--budget must be an amount in dollars, such as 2.00.")
	}
	return n, nil
}

func trackedOption(list []string) ([]string, error) {
	var tracked []string
	for _, value := range list {
		for _, part := range strings.Split(value, ",") {
			if p := strings.TrimSpace(part); p != "" {
				tracked = append(tracked, p)
			}
		}
	}
	if len(tracked) > run.MaxTrackedFields {
		return nil, usagef(fmt.Sprintf("Track at most %d fields.", run.MaxTrackedFields))
	}
	var unique []string
	seen := map[string]bool{}
	for _, path := range tracked {
		if _, err := core.ParseJSONPath(path); err != nil {
			return nil, usagef(err.Error())
		}
		if !seen[path] {
			seen[path] = true
			unique = append(unique, path)
		}
	}
	return unique, nil
}

func monthlyCallsOption(value string, given bool) (*float64, error) {
	if !given {
		return nil, nil
	}
	text := strings.NewReplacer("_", "", ",", "").Replace(strings.TrimSpace(value))
	n, err := strconv.ParseInt(text, 10, 64)
	if !wholeNumber.MatchString(text) || err != nil || n < 1 || n > 1<<53-1 {
		return nil, usagef("--monthly-calls must be a whole number of calls, such as 250000.")
	}
	f := float64(n)
	return &f, nil
}

// reportOptions are what the report is built with.
func (s *session) reportOptions(tracked []string) (report.Options, error) {
	monthly, err := monthlyCallsOption(s.v.str("monthly-calls"))
	if err != nil {
		return report.Options{}, err
	}
	o := report.Options{Labels: s.v.bool("labels"), MonthlyCalls: monthly}
	if len(tracked) > 0 {
		o.Tracked = tracked
	}
	return o, nil
}

func pricingHelpURL(p *pricing.CatalogPricing) string {
	return p.Source.Repository + "/blob/main/" + p.Source.Path
}

func isoTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// relativeBelow is path relative to dir when it is below dir, else path.
func relativeBelow(dir, path string) string {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return path
	}
	return rel
}

// ---------------------------------------------------------------------------
// Setup: the plan, the endpoints it uses and the price catalog

type setupResult struct {
	pricing   *pricing.CatalogPricing
	endpoints []run.EndpointSpec
	plan      *run.RunPlan
	tracked   []string
	// Map plans: the names file, what it held, and what happened to it.
	namesPath  string
	names      map[string]string
	namesState string
}

func (s *session) setup(file string, mapMode bool) (*setupResult, error) {
	models, _ := s.v.str("models")
	if models == "" {
		return nil, usagef("Name the models to replay on with --models.")
	}
	tracked, err := trackedOption(s.v.list("track"))
	if err != nil {
		return nil, err
	}
	var global types.TreatmentSettings
	if value, ok := s.v.str("temperature"); ok {
		t, err := run.ParseTemperature(value)
		if err != nil {
			return nil, err
		}
		global.Temperature = &t
	}
	if value, ok := s.v.str("max-tokens"); ok {
		n, err := run.ParseMaxTokens(value)
		if err != nil {
			return nil, err
		}
		global.MaxTokens = &n
	}
	if value, ok := s.v.str("reasoning-effort"); ok {
		e, err := run.ParseEffort(value)
		if err != nil {
			return nil, err
		}
		global.ReasoningEffort = &e
	}
	endpointName, ok := s.v.str("endpoint")
	if !ok {
		endpointName = string(run.DefaultEndpoint)
	}
	defaultEndpoint, err := run.ParseEndpoint(endpointName)
	if err != nil {
		return nil, err
	}
	floorEndpoint := defaultEndpoint
	if name, _ := s.v.str("floor-endpoint"); name != "" {
		if floorEndpoint, err = run.ParseEndpoint(name); err != nil {
			return nil, err
		}
	}
	specs, err := run.ParseModels(models, defaultEndpoint)
	if err != nil {
		return nil, err
	}
	noiseFloor := !s.v.bool("no-noise-floor")
	var endpoints []run.EndpointSpec
	index := map[string]int{}
	all := make([]run.EndpointSpec, 0, len(specs)+1)
	for _, m := range specs {
		all = append(all, m.Endpoint)
	}
	if noiseFloor {
		all = append(all, floorEndpoint)
	}
	for _, spec := range all {
		if i, known := index[spec.Key]; known {
			if endpoints[i].BaseURL != spec.BaseURL {
				return nil, usagef("Two endpoints share the host " + spec.Key + "; use one base URL per host.")
			}
			endpoints[i] = spec
			continue
		}
		index[spec.Key] = len(endpoints)
		endpoints = append(endpoints, spec)
	}
	prices, err := pricing.LoadPricing(pricing.LoadPricingOptions{Now: s.io.Now})
	if err != nil {
		return nil, err
	}
	sampleText, sampleGiven := s.v.str("sample")
	sample, err := intOption(sampleText, sampleGiven, "sample", 1, run.MaxSample, run.DefaultSample)
	if err != nil {
		return nil, err
	}
	defaultBudget := run.DefaultBudgetUSD
	if mapMode {
		defaultBudget = run.DefaultMapBudgetUSD
	}
	budgetText, budgetGiven := s.v.str("budget")
	budget, err := budgetOption(budgetText, budgetGiven, defaultBudget)
	if err != nil {
		return nil, err
	}
	treatments := make([]run.TreatmentInput, len(specs))
	for i, m := range specs {
		settings := m.Settings
		if settings.Temperature == nil {
			settings.Temperature = global.Temperature
		}
		if settings.MaxTokens == nil {
			settings.MaxTokens = global.MaxTokens
		}
		if settings.ReasoningEffort == nil {
			settings.ReasoningEffort = global.ReasoningEffort
		}
		treatments[i] = run.TreatmentInput{Model: m.Model, Endpoint: m.Endpoint.Key, Settings: settings}
	}
	adapter, _ := s.v.str("adapter")
	options := run.PlanOptions{
		File:          file,
		Adapter:       adapter,
		Sample:        sample,
		Treatments:    treatments,
		NoiseFloor:    &noiseFloor,
		FloorEndpoint: floorEndpoint.Key,
		TrackedFields: tracked,
		BudgetUSD:     &budget,
		AllowNoSearch: s.v.bool("allow-no-search"),
		Pricing:       prices,
		IsFreeEndpoint: func(key string) bool {
			if i, ok := index[key]; ok {
				return endpoints[i].Free
			}
			return false
		},
	}
	if s.io.Now != nil {
		options.At = isoTime(s.io.Now())
	}
	result := &setupResult{pricing: prices, endpoints: endpoints, tracked: tracked}
	if mapMode {
		if err := s.mapPlanOptions(file, &options, result); err != nil {
			return nil, err
		}
	}
	plan, err := run.PlanRun(options)
	if err != nil {
		var usage *parts.UsageError
		if errors.As(err, &usage) {
			return nil, usagef(usage.Message)
		}
		if errors.Is(err, parts.ErrPartsNotFound) {
			return nil, &cliError{ExitUsage, "parts_not_found", err.Error()}
		}
		return nil, err
	}
	if len(plan.Estimate.Unpriced) > 0 {
		if again, err := s.lookUpPrices(prices, plan.Estimate.Unpriced, options); err != nil {
			return nil, err
		} else if again != nil {
			plan = again
		}
	}
	result.plan = plan
	if mapMode {
		s.writeNames(result)
	}
	return result, nil
}

// lookUpPrices asks Metergraph's Pricing API for models the bundled catalog
// cannot price, and plans again when it priced any. Only model names are
// sent. A failure is a note, not an error: the models stay unpriced.
func (s *session) lookUpPrices(prices *pricing.CatalogPricing, unpriced []string, options run.PlanOptions) (*run.RunPlan, error) {
	if s.io.PricingURL == "" || s.v.bool("no-price-lookup") {
		return nil, nil
	}
	ctx := s.io.Signal
	if ctx == nil {
		ctx = context.Background()
	}
	found, err := prices.LookUp(ctx, unpriced, pricing.LookupOptions{BaseURL: s.io.PricingURL, CacheDir: s.io.CacheDir})
	if err != nil {
		s.say("Could not look up prices for " + strings.Join(unpriced, ", ") + ": " + err.Error() + ".")
	}
	if len(found.Found) == 0 {
		return nil, nil
	}
	s.say("Prices from Metergraph's Pricing API: " + strings.Join(found.Found, ", ") + ". Only model names were sent. Turn this off with --no-price-lookup.")
	return run.PlanRun(options)
}

func (s *session) concurrency() (int, error) {
	text, given := s.v.str("concurrency")
	return intOption(text, given, "concurrency", 1, 64, run.DefaultRunConcurrency)
}

func (s *session) createProviders(endpoints []run.EndpointSpec) (map[string]types.Provider, error) {
	concurrency, err := s.concurrency()
	if err != nil {
		return nil, err
	}
	out := map[string]types.Provider{}
	for _, spec := range endpoints {
		free := spec.Free
		o := providers.ProviderOptions{
			Preset:      spec.Preset,
			HTTPClient:  s.io.HTTPClient,
			Env:         s.io.Env,
			Free:        &free,
			Concurrency: concurrency,
		}
		if spec.BaseURL != "" {
			o.BaseURL = spec.BaseURL
			keyEnv, _ := s.v.str("api-key-env")
			o.APIKeyEnv = &keyEnv
		}
		provider, err := providers.CreateProvider(o)
		if err != nil {
			return nil, err
		}
		out[spec.Key] = provider
	}
	return out, nil
}

// missingKeys are notes for endpoints a run could not use yet. Creating a
// provider only reads its configuration; it sends nothing.
func (s *session) missingKeys(endpoints []run.EndpointSpec) ([]string, error) {
	notes := []string{}
	for _, spec := range endpoints {
		if _, err := s.createProviders([]run.EndpointSpec{spec}); err != nil {
			if pce, ok := err.(*providers.ProviderConfigError); ok {
				notes = append(notes, pce.Message+"; run will need it.")
				continue
			}
			return nil, err
		}
	}
	return notes, nil
}

// ---------------------------------------------------------------------------
// estimate

func (s *session) estimate(file string) (outcome, error) {
	mapMode := s.v.bool("map")
	if err := s.checkOptions("estimate", mapMode); err != nil {
		return outcome{}, err
	}
	setup, err := s.setup(file, mapMode)
	if err != nil {
		return outcome{}, err
	}
	plan := setup.plan
	if mapMode {
		return s.estimateMap(setup)
	}
	s.say(explainEstimate(plan, explainOptions{pricingHelpURL: pricingHelpURL(setup.pricing), allowUnpriced: s.v.bool("allow-unpriced")}))
	notes, err := s.missingKeys(setup.endpoints)
	if err != nil {
		return outcome{}, err
	}
	for _, note := range notes {
		s.say("Note: " + note)
	}
	exitCode := ExitOK
	if plan.Estimate.Replayable == 0 {
		exitCode = ExitNoReplayableCases
		s.say("No case in the sample can be replayed, so there is nothing to run.")
	}
	return outcome{exitCode, object{
		{"command", "estimate"},
		{"input", plan.Input},
		{"file", object{{"calls", plan.File.Calls}, {"traces", plan.File.Traces}, {"skippedLines", plan.File.SkippedLines}}},
		{"sampled", len(plan.Cases)},
		{"treatments", plan.Treatments},
		{"noiseFloor", plan.Settings.NoiseFloor},
		{"budgetUsd", plan.Settings.BudgetUSD},
		{"estimate", plan.Estimate},
		{"overBudget", plan.Estimate.ExpectedUSD > plan.Settings.BudgetUSD},
		{"notes", notes},
	}}, nil
}

// ---------------------------------------------------------------------------
// run

// ask asks the spend question: on the terminal with Bubble Tea when stderr
// is one, else by reading a line from stdin.
func (s *session) ask(question string) (bool, error) {
	if s.io.Confirm != nil {
		return s.io.Confirm(question)
	}
	if s.io.StderrTTY && !s.dumb() {
		profile := term.DetectProfile(true, s.environ(), s.colorMode())
		return tui.Ask(question, tui.AskOptions{Input: s.io.Stdin, Output: s.io.Stderr, Env: s.environ(), Profile: profile, Signals: true})
	}
	io.WriteString(s.io.Stderr, question)
	line, err := bufio.NewReader(s.io.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	return yesAnswer.MatchString(strings.TrimSpace(line)), nil
}

var yesAnswer = regexp.MustCompile(`(?i)^y(es)?$`)

// interrupts are the run's two stops, and how to let go of them.
type interrupts struct {
	soft, hard         context.Context
	stopSoft, stopHard context.CancelFunc
	dispose            func()
	handlersInstalled  bool
}

// installInterrupt: the first Ctrl-C stops new calls, the second cancels
// the calls in flight. With IO.Signal set, those contexts are the stops and
// no handler is installed.
func (s *session) installInterrupt(view bool) *interrupts {
	softParent, hardParent := context.Background(), context.Background()
	if s.io.Signal != nil {
		softParent = s.io.Signal
		if s.io.Hard != nil {
			hardParent = s.io.Hard
		}
	}
	in := &interrupts{dispose: func() {}}
	in.soft, in.stopSoft = context.WithCancel(softParent)
	in.hard, in.stopHard = context.WithCancel(hardParent)
	if s.io.Signal != nil || view {
		// Tests own the stops; the live view handles Ctrl-C itself.
		return in
	}
	in.handlersInstalled = true
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-ch:
				if sig == syscall.SIGTERM || in.soft.Err() != nil {
					in.stopSoft()
					in.stopHard()
					continue
				}
				in.stopSoft()
				s.say("\nStopping: finishing the calls in flight, then writing what exists. Press Ctrl-C again to quit now.")
			case <-done:
				return
			}
		}
	}()
	in.dispose = func() {
		signal.Stop(ch)
		close(done)
	}
	return in
}

// run is `run`, and `map` when mapMode: the same estimate, confirmation,
// replay and run.json, planned part by part and printed as the map.
func (s *session) run(file string, mapMode bool) (outcome, error) {
	command := "run"
	if mapMode {
		command = "map"
	}
	if err := s.checkOptions(command, mapMode); err != nil {
		return outcome{}, err
	}
	judgeKey := ""
	if s.v.bool("judge") {
		key, err := s.judgeKey("map")
		if err != nil {
			return outcome{}, err
		}
		judgeKey = key
	}
	setup, err := s.setup(file, mapMode)
	if err != nil {
		return outcome{}, err
	}
	plan := setup.plan
	help := pricingHelpURL(setup.pricing)
	if mapMode {
		s.say(s.explainMapEstimate(setup))
	} else {
		s.say(explainEstimate(plan, explainOptions{pricingHelpURL: help, allowUnpriced: s.v.bool("allow-unpriced")}))
	}
	if plan.Estimate.Replayable == 0 {
		if mapMode {
			return outcome{}, &cliError{ExitNoReplayableCases, "no_replayable_cases", "No case in any part can be replayed."}
		}
		return outcome{}, &cliError{ExitNoReplayableCases, "no_replayable_cases", "No case in the sample can be replayed."}
	}
	if len(plan.Estimate.Unpriced) > 0 && !s.v.bool("allow-unpriced") {
		return outcome{}, &cliError{ExitRefused, "unpriced_models", "The catalog has no price for " + strings.Join(plan.Estimate.Unpriced, ", ") + ", so the run cannot be budgeted. Pass --allow-unpriced to run anyway, or add the price: " + help}
	}
	if plan.Estimate.ExpectedUSD > plan.Settings.BudgetUSD {
		return outcome{}, &cliError{ExitRefused, "over_budget", "The expected spend (" + usd(plan.Estimate.ExpectedUSD) + ") is over the budget (" + usd(plan.Settings.BudgetUSD) + "). " + overBudgetHint(mapMode)}
	}
	providerMap, err := s.createProviders(setup.endpoints)
	if err != nil {
		return outcome{}, err
	}

	ceiling := math.Min(plan.Estimate.UpperBoundUSD, plan.Settings.BudgetUSD)
	if !s.v.bool("yes") {
		if !s.io.StdinTTY {
			return outcome{}, &cliError{ExitRefused, "confirmation_required", "Refusing to spend without a confirmation. Pass --yes to run without a terminal."}
		}
		yes, err := s.ask("Spend up to " + usd(ceiling) + " (about " + usd(plan.Estimate.ExpectedUSD) + " expected)? [y/N] ")
		if err != nil {
			return outcome{}, err
		}
		if !yes {
			return outcome{}, &cliError{ExitRefused, "not_confirmed", "Nothing was sent."}
		}
	}

	concurrency, err := s.concurrency()
	if err != nil {
		return outcome{}, err
	}
	showProgress := s.io.StderrTTY && !s.asJSON
	view := showProgress && s.interactive()
	interrupt := s.installInterrupt(view)
	options := run.ExecuteOptions{
		Providers:   providerMap,
		Pricing:     setup.pricing,
		Hard:        interrupt.hard,
		Concurrency: concurrency,
		Now:         s.io.Now,
		ToolVersion: Version,
	}
	var result *types.RunFile
	if view {
		display := s.display()
		width := s.io.Columns
		if width <= 0 {
			width = display.width
		}
		result, err = tui.RunWithProgress(plan, tui.RunOptions{
			Input:   s.io.Stdin,
			Output:  s.io.Stderr,
			Env:     s.environ(),
			Profile: term.DetectProfile(true, s.environ(), s.colorMode()),
			ASCII:   s.v.bool("ascii") || s.dumb(),
			Width:   width,
			Signals: s.io.Signal == nil,
			Now:     s.io.Now,
		}, interrupt.stopSoft, interrupt.stopHard, func(onEvent func(run.Event)) (*types.RunFile, error) {
			o := options
			o.OnEvent = onEvent
			return run.ExecuteRun(interrupt.soft, plan, o)
		})
	} else {
		if showProgress {
			options.OnProgress = func(p run.Progress) {
				fmt.Fprintf(s.io.Stderr, "\r  %d/%d calls, spent %s   ", p.Done, p.Total, usd(p.SpentUSD))
			}
		}
		result, err = run.ExecuteRun(interrupt.soft, plan, options)
		if showProgress {
			io.WriteString(s.io.Stderr, "\n")
		}
	}
	var judgeFields object
	if err == nil && judgeKey != "" && result.Stopped != nil {
		s.say("The run stopped early, so nothing was judged. Judge what finished with: metergraph-replay judge <run.json>")
	}
	if err == nil && judgeKey != "" && result.Stopped == nil {
		s.say("")
		judgedPairs, jerr := s.judgeRun(interrupt.soft, result, judgeKey)
		if jerr != nil {
			err = jerr
		}
		judgeFields = object{{"judge", judgedPairs.fields()}}
	}
	interrupt.dispose()
	interrupt.stopSoft()
	interrupt.stopHard()
	if err != nil {
		return outcome{}, err
	}

	monthly, err := monthlyCallsOption(s.v.str("monthly-calls"))
	if err != nil {
		return outcome{}, err
	}
	if monthly != nil {
		result.Settings.MonthlyCalls = monthly
	}
	out, given := s.v.str("out")
	if !given {
		out = "replay-out"
	}
	written, err := run.WriteRun(out, result)
	if err != nil {
		return outcome{}, err
	}
	s.say("")
	s.say(explainOutcome(result))
	s.say("Wrote " + written.RunPath)
	s.say("")
	shown := s.shownPath(written.RunPath)
	var mapFields object
	if mapMode {
		m, err := report.BuildMap(result, report.MapOptions{})
		if err != nil {
			return outcome{}, err
		}
		s.printMap(m)
		s.say("")
		s.say("Print it again: metergraph-replay report " + shown)
		mapFields = object{{"parts", m.Parts}}
		pageField, err := s.writePage(result, m, nil)
		if err != nil {
			return outcome{}, err
		}
		mapFields = append(mapFields, pageField...)
	} else {
		if err := s.printReport(result, setup.tracked, shown, "add --explain to the report command below"); err != nil {
			return outcome{}, err
		}
		pageField, err := s.writePage(result, nil, setup.tracked)
		if err != nil {
			return outcome{}, err
		}
		mapFields = pageField
	}

	counts := countReplays(result)
	exitCode := ExitOK
	if result.Stopped != nil {
		exitCode = ExitStoppedEarly
	} else if counts.ok == 0 && counts.failed > 0 {
		exitCode = ExitError
	}
	var stopped any
	if result.Stopped != nil {
		stopped = *result.Stopped
	}
	return outcome{exitCode, append(object{
		{"command", command},
		{"input", plan.Input},
		{"estimate", plan.Estimate},
		{"runFile", written.RunPath},
		{"stopped", stopped},
		{"spentUsd", result.SpentUSD},
		{"budgetUsd", result.Settings.BudgetUSD},
		{"replays", object{{"ok", counts.ok}, {"failed", counts.failed}, {"skipped", counts.skipped}}},
	}, append(judgeFields, mapFields...)...)}, nil
}

// printReport prints the report (and, with --explain, its notes). help is
// where the footer sends a reader for the notes when they are not printed.
func (s *session) printReport(r *types.RunFile, tracked []string, shown, help string) error {
	if s.asJSON {
		return nil
	}
	ro, err := s.reportOptions(tracked)
	if err != nil {
		return err
	}
	m, err := report.Build(r, ro)
	if err != nil {
		return err
	}
	d := s.display()
	explain := s.v.bool("explain")
	if explain {
		help = "below"
	}
	s.say(strings.TrimRight(report.Render(m, report.RenderOptions{Width: d.width, Paint: d.paint, ASCII: d.ascii, Explore: shown, Help: help}), " \n\t"))
	if explain {
		s.say("")
		s.say(explainNotes(report.Caveats(m), d.width, d.paint, d.ascii))
	}
	return nil
}

// ---------------------------------------------------------------------------
// report and demo

func readRunFile(path string) (*types.RunFile, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, usagef("Could not read " + path + ".")
	}
	if !json.Valid(text) {
		return nil, usagef(path + " is not JSON.")
	}
	var head struct {
		Schema any `json:"schema"`
	}
	notRun := usagef(path + " is not a metergraph-replay run file.")
	if json.Unmarshal(text, &head) != nil || head.Schema != types.RunSchema {
		return nil, notRun
	}
	var r types.RunFile
	if err := json.Unmarshal(text, &r); err != nil {
		return nil, notRun
	}
	return &r, nil
}

// explore opens the explorer; the exit code says how it ended.
func (s *session) explore(r *types.RunFile, tracked []string, name string) (int, error) {
	ro, err := s.reportOptions(tracked)
	if err != nil {
		return 0, err
	}
	d := s.display()
	o := ExploreOptions{
		Name:         name,
		Color:        d.paint.Color,
		Profile:      d.profile,
		ASCII:        d.ascii,
		Tracked:      ro.Tracked,
		Labels:       ro.Labels,
		MonthlyCalls: ro.MonthlyCalls,
	}
	var reason string
	if s.io.Explore != nil {
		reason, err = s.io.Explore(r, o)
	} else {
		reason, err = s.defaultExplore(r, o)
	}
	if err != nil {
		return 0, err
	}
	switch reason {
	case explore.ReasonSIGINT:
		return ExitSIGINT, nil
	case explore.ReasonSIGTERM:
		return ExitSIGTERM, nil
	}
	return ExitOK, nil
}

func (s *session) defaultExplore(r *types.RunFile, o ExploreOptions) (string, error) {
	data, err := explore.NewData(r, explore.Options{Tracked: o.Tracked, Labels: o.Labels, MonthlyCalls: o.MonthlyCalls, ASCII: o.ASCII, Name: o.Name})
	if err != nil {
		return "", err
	}
	columns, rows := s.io.Columns, s.io.Rows
	if columns <= 0 {
		columns = 80
	}
	if rows <= 0 {
		rows = 24
	}
	return explore.Run(context.Background(), data, explore.RunOptions{
		Input:   s.io.Stdin,
		Output:  s.io.Stdout,
		Env:     s.environ(),
		Profile: o.Profile,
		Columns: columns,
		Rows:    rows,
		Tab:     o.Tab,
		Signals: true,
	})
}

func (s *session) report(file string) (outcome, error) {
	if _, given := s.v.str("out"); given {
		return outcome{}, usagef("report reads a run file and writes none, so it takes no --out. To save a page, use --html.")
	}
	r, err := readRunFile(file)
	if err != nil {
		return outcome{}, err
	}
	tracked, err := trackedOption(s.v.list("track"))
	if err != nil {
		return outcome{}, err
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		abs = file
	}
	fields := object{{"command", "report"}, {"runFile", abs}}
	if err := s.checkOptions("report", s.v.bool("map") || report.IsMapRun(r)); err != nil {
		return outcome{}, err
	}
	if s.v.bool("map") || report.IsMapRun(r) {
		// A map run is read part by part: its cases are a sample of each part,
		// not of the file, so the whole-run report and the explorer do not apply.
		m, err := report.BuildMap(r, report.MapOptions{Rename: s.v.list("name"), Only: commaList(s.v.list("only"))})
		if err != nil {
			var usage *parts.UsageError
			if errors.As(err, &usage) || errors.Is(err, report.ErrNotMapRun) {
				return outcome{}, usagef(err.Error())
			}
			return outcome{}, err
		}
		s.printMap(m)
		pageField, err := s.writePage(r, m, nil)
		if err != nil {
			return outcome{}, err
		}
		return outcome{ExitOK, append(append(fields, field{"parts", m.Parts}), pageField...)}, nil
	}
	if path, err := s.htmlPath(); err != nil {
		return outcome{}, err
	} else if path != "" {
		// Writing the page replaces opening the explorer.
		pageField, err := s.writePage(r, nil, tracked)
		if err != nil {
			return outcome{}, err
		}
		return outcome{ExitOK, append(fields, pageField...)}, nil
	}
	if !s.asJSON && !s.v.bool("explain") && s.interactive() {
		code, err := s.explore(r, tracked, s.shownPath(abs))
		if err != nil {
			return outcome{}, err
		}
		return outcome{code, fields}, nil
	}
	if err := s.printReport(r, tracked, s.shownPath(abs), ""); err != nil {
		return outcome{}, err
	}
	return outcome{ExitOK, fields}, nil
}

func (s *session) demo() (outcome, error) {
	if err := s.checkOptions("demo", s.v.bool("map")); err != nil {
		return outcome{}, err
	}
	if s.v.bool("map") {
		return s.demoMap()
	}
	r, err := demo.Run()
	if err != nil {
		return outcome{}, err
	}
	tracked, err := trackedOption(s.v.list("track"))
	if err != nil {
		return outcome{}, err
	}
	// Check every option before anything is written.
	if _, err := monthlyCallsOption(s.v.str("monthly-calls")); err != nil {
		return outcome{}, err
	}
	out, given := s.v.str("out")
	if !given {
		out = "replay-out"
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return outcome{}, err
	}
	if err := os.MkdirAll(out, 0o777); err != nil {
		return outcome{}, err
	}
	runPath := filepath.Join(out, "run.json")
	if err := os.WriteFile(runPath, demo.RunJSON, 0o666); err != nil {
		return outcome{}, err
	}
	fields := object{{"command", "demo"}, {"runFile", runPath}}
	if s.v.bool("explore") && !s.asJSON && s.interactive() {
		code, err := s.explore(r, tracked, s.shownPath(runPath))
		if err != nil {
			return outcome{}, err
		}
		return outcome{code, fields}, nil
	}
	s.say("A replay of synthetic support-ticket traces, recorded so it needs no keys and no network.")
	s.say("Try it on your own traces:")
	s.say("  metergraph-replay estimate traces.jsonl --models openai/gpt-5-mini,anthropic/claude-haiku-4.5")
	s.say("")
	if err := s.printReport(r, tracked, s.shownPath(runPath), ""); err != nil {
		return outcome{}, err
	}
	pageField, err := s.writePage(r, nil, tracked)
	if err != nil {
		return outcome{}, err
	}
	return outcome{ExitOK, append(fields, pageField...)}, nil
}
