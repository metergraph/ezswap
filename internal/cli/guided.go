package cli

// The guided workflow: ezswap with no command on an interactive
// terminal. It has three steps. Record: the upstream's menu and the record
// dashboard. Analyze: the parts found in the traces, the models to try and
// a live estimate. Map: the spend confirmation, the progress view, the
// printed map and the map explorer. A start menu chooses the step to begin
// at. Every step is also a command of its own.

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/metergraph/ezswap/internal/flow"
	"github.com/metergraph/ezswap/internal/judge"
	"github.com/metergraph/ezswap/internal/parts"
	"github.com/metergraph/ezswap/internal/pricing"
	"github.com/metergraph/ezswap/internal/recommend"
	"github.com/metergraph/ezswap/internal/report"
	"github.com/metergraph/ezswap/internal/types"
)

// How many models an objective suggests.
const suggested = 3

// A typical call, for comparing what models cost: three input tokens to
// each output token.
const typicalInput, typicalOutput = 3000, 1000

// objectives is the plan's objectives for traces recorded on production,
// each with the models suggested for it. Prices are the catalog's.
func objectives(list recommend.List, production string, prices *pricing.CatalogPricing) []flow.Objective {
	price := func(model string) (float64, bool) {
		if model == "" {
			return 0, false
		}
		usd := prices.Price(types.PriceQuery{Model: model, InputTokens: typicalInput, OutputTokens: typicalOutput})
		if usd == nil {
			return 0, false
		}
		return *usd, true
	}
	_, known := price(production)
	than := "the model in the traces"
	if production != "" {
		than = production
	}
	var out []flow.Objective
	for _, key := range recommend.Objectives {
		o := flow.Objective{Key: key, Models: recommend.Suggest(list, key, production, price, suggested)}
		switch key {
		case recommend.Cost:
			o.Label, o.Detail = "Reduce cost", "models that cost less than "+than
			if !known {
				o.Detail = "the cheapest models on ezswap's list"
			}
		case recommend.Quality:
			o.Label, o.Detail, o.Judge = "Improve quality", "models that cost more than "+than, true
			if !known {
				o.Detail = "the costliest models on ezswap's list"
			}
		case recommend.Latest:
			o.Label, o.Detail = "Try the latest models", "the newest model from each provider"
		}
		if len(o.Models) == 0 {
			o.Detail = "ezswap's list has no such model for " + than
		}
		out = append(out, o)
	}
	return out
}

// productionModel is the model most of the plan's cases were recorded on,
// the first seen winning a tie, or "". It is the catalog's name for the
// model when the catalog knows it, and the name as captured otherwise.
func productionModel(cases []types.Case, prices *pricing.CatalogPricing) string {
	count := map[string]int{}
	best := ""
	for _, c := range cases {
		if c.Model == nil || *c.Model == "" {
			continue
		}
		name := *c.Model
		if resolved := prices.ResolveProduction(c.Provider, c.Model, ""); resolved != nil {
			name = *resolved
		}
		count[name]++
		if best == "" || count[name] > count[best] {
			best = name
		}
	}
	return best
}

// Upstreams the recorder offers.
var upstreams = []flow.Item{
	{Title: "OpenAI", Detail: "https://api.openai.com", Value: "https://api.openai.com"},
	{Title: "Vercel AI Gateway", Detail: "https://ai-gateway.vercel.sh", Value: "https://ai-gateway.vercel.sh"},
	{Title: "OpenRouter", Detail: "https://openrouter.ai/api", Value: "https://openrouter.ai/api"},
	{Title: "Anthropic", Detail: "https://api.anthropic.com", Value: "https://api.anthropic.com"},
	{Title: "Ollama on this machine", Detail: "http://localhost:11434", Value: "http://localhost:11434"},
	{Title: "Another provider", Prompt: "Base URL:", Value: "https://"},
}

const (
	stepRecord  = "step 1 of 3"
	stepAnalyze = "step 2 of 3"
	stepMap     = "step 3 of 3"
)

func (s *session) flowOptions() flow.Options {
	d := s.display()
	rows := s.io.Rows
	if rows <= 0 {
		rows = 30
	}
	return flow.Options{Input: s.io.Stdin, Output: s.io.Stdout, Env: s.environ(), Profile: d.profile, ASCII: d.ascii, Width: d.width, Height: rows}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (s *session) guided() (outcome, error) {
	done := outcome{ExitOK, object{{"command", "guided"}}}
	o := s.flowOptions()
	traces := filepath.Join("ezswap-out", "traces.jsonl")
	runFile := filepath.Join("ezswap-out", "run.json")
	open := flow.Item{Title: "Open a finished map", Detail: "Step 3. Explore a run.json that an earlier map wrote.", Prompt: "Run file:", Value: runFile}
	if !exists(filepath.Join(s.io.Cwd, runFile)) {
		open.Value = ""
	}
	start := flow.NewMenu("", "Where would you like to start?", []string{
		"ezswap works in three steps. Record the model calls your app makes. Analyze them: the call sites found, the models to try and the price. Map where answers change on those models, beyond what production's own model changes when it is replayed twice.",
	}, []flow.Item{
		{Title: "Record my app's model calls", Detail: "Step 1. Run a local proxy your app sends its calls through."},
		{Title: "Analyze a trace file", Detail: "Step 2. A file record wrote, or Langfuse, OpenTelemetry or raw provider logs.", Prompt: "Trace file:", Value: traces},
		open,
	})
	if quit, err := s.askTelemetry(o); err != nil || quit {
		return done, err
	}
	chosen, err := flow.RunMenu(start, o)
	if err != nil || chosen.Cancel {
		return done, err
	}
	s.event.Start = []string{"record", "analyze", "open"}[min(chosen.Chosen, 2)]
	file := ""
	switch chosen.Chosen {
	case 0:
		recorded, err := s.guidedRecord(o)
		if err != nil || recorded == "" {
			return done, err
		}
		file = recorded
	case 1:
		file = strings.TrimSpace(chosen.Value)
	default:
		s.step = stepMap
		return s.guidedOpen(strings.TrimSpace(chosen.Value))
	}
	return s.guidedPlan(file, o)
}

// guidedRecord asks for the upstream and records, fresh, until the person
// stops it. It returns the trace file, or "" when nothing was recorded.
func (s *session) guidedRecord(o flow.Options) (string, error) {
	menu, err := flow.RunMenu(flow.NewMenu(stepRecord, "Record: where does your app send its model calls?", []string{
		"ezswap forwards each call there unchanged and writes a copy to ezswap-out/traces.jsonl. Your app's key passes through and is never written. A previous recording is kept beside the new one.",
	}, upstreams), o)
	if err != nil || menu.Cancel {
		return "", err
	}
	upstream := strings.TrimSpace(menu.Value)
	s.v.strings["upstream"] = upstream
	s.v.bools["fresh"] = true
	s.step = stepRecord
	if _, err := s.record(); err != nil {
		return "", err
	}
	out, _ := s.v.str("out")
	if out == "" {
		out = "ezswap-out"
	}
	file := filepath.Join(out, "traces.jsonl")
	if info, err := os.Stat(filepath.Join(s.io.Cwd, file)); err != nil || info.Size() == 0 {
		s.say("Nothing was recorded, so there is nothing to map.")
		return "", nil
	}
	return file, nil
}

// guidedOpen opens a finished run: the map explorer for a map, the run
// explorer for a whole-file run.
func (s *session) guidedOpen(path string) (outcome, error) {
	r, err := readRunFile(path)
	if err != nil {
		return outcome{}, err
	}
	if !report.IsMapRun(r) {
		code, err := s.explore(r, nil, s.shownPath(path))
		return outcome{code, object{{"command", "guided"}}}, err
	}
	m, err := report.BuildMap(r, report.MapOptions{})
	if err != nil {
		return outcome{}, err
	}
	code, _, err := s.exploreMap(r, m, path)
	return outcome{code, object{{"command", "guided"}}}, err
}

// quiet is a copy of the session that prints nothing, for working out an
// estimate behind the plan screen.
func (s *session) quiet(v values) *session {
	o := s.io
	o.Stdout, o.Stderr = io.Discard, io.Discard
	return &session{io: o, v: v}
}

func (v values) clone() values {
	out := values{strings: map[string]string{}, lists: map[string][]string{}, bools: map[string]bool{}}
	for k, x := range v.strings {
		out.strings[k] = x
	}
	for k, x := range v.lists {
		out.lists[k] = append([]string(nil), x...)
	}
	for k, x := range v.bools {
		out.bools[k] = x
	}
	return out
}

// withChoice is the options with a plan's choice applied.
func withChoice(v values, c flow.Choice, all []string) values {
	v = v.clone()
	v.strings["models"] = strings.Join(c.Models, ",")
	v.strings["endpoint"] = c.Endpoint
	v.strings["per-part"] = strconv.Itoa(c.PerPart)
	delete(v.lists, "only")
	if len(c.Parts) < len(all) {
		v.lists["only"] = append([]string(nil), c.Parts...)
	}
	v.bools["judge"] = c.Judge
	return v
}

func (s *session) guidedPlan(file string, o flow.Options) (outcome, error) {
	if _, _, err := s.applyConfig("map"); err != nil {
		return outcome{}, err
	}
	// The models to start from: the settings file's. The objective adds
	// its suggestions, ticked when the settings file names none.
	chosen := commaList([]string{func() string { m, _ := s.v.str("models"); return m }()})
	list := recommend.Bundled()
	// The parts, from a plan on any one model: parts do not depend on it.
	probeModel := list.Models[0].ID
	if len(chosen) > 0 {
		probeModel = chosen[0]
	}
	probe := s.quiet(withChoice(s.v, flow.Choice{Models: []string{probeModel}, Endpoint: endpointOf(s.v), PerPart: perPartOf(s.v)}, nil))
	setup, err := probe.setup(file, true)
	if err != nil {
		return outcome{}, err
	}
	var rows []flow.PartRow
	var all []string
	for _, part := range setup.plan.Parts {
		rows = append(rows, flow.PartRow{Name: part.Name, Source: part.Source, Cases: part.Available, Compared: comparedText(part, true), FreeText: part.Reason == parts.ReasonFreeText})
		all = append(all, part.Name)
	}
	if len(rows) == 0 {
		return outcome{}, usagef(file + " holds no call a map can replay.")
	}
	start := flow.Choice{Models: chosen, Endpoint: endpointOf(s.v), PerPart: perPartOf(s.v), Judge: s.v.bool("judge")}
	if only := commaList(s.v.list("only")); len(only) > 0 {
		start.Parts = only
	}
	plan := flow.NewPlan(file, stepAnalyze, rows, start).
		WithObjectives(objectives(list, productionModel(setup.plan.Cases, setup.pricing), setup.pricing), chosen, len(chosen) == 0, "")
	estimate := func(c flow.Choice) flow.Preview {
		if len(c.Models) == 0 || len(c.Parts) == 0 {
			return flow.Preview{Err: "Choose at least one model and one part."}
		}
		q := s.quiet(withChoice(s.v, c, all))
		setup, err := q.setup(file, true)
		if err != nil {
			var ce *cliError
			var ue *usageError
			message := err.Error()
			if errors.As(err, &ce) {
				message = ce.message
			} else if errors.As(err, &ue) {
				message = ue.message
			}
			return flow.Preview{Err: message}
		}
		p := setup.plan
		perCase := len(p.Treatments)
		if p.FloorEndpoint != "" {
			perCase += 2
		}
		preview := flow.Preview{
			Calls: p.Estimate.Replayable * perCase, ExpectedUSD: p.Estimate.ExpectedUSD, UpperUSD: p.Estimate.UpperBoundUSD,
			BudgetUSD: p.Settings.BudgetUSD, Unpriced: p.Estimate.Unpriced,
		}
		for _, part := range p.Parts {
			preview.Parts = append(preview.Parts, flow.PartRow{Name: part.Name, USD: part.Estimate.ExpectedUSD})
		}
		if notes, err := q.missingKeys(setup.endpoints); err == nil {
			for _, n := range notes {
				preview.Missing = append(preview.Missing, strings.Replace(n, "; run will need it.", ": the map will need it.", 1))
			}
		}
		if c.Judge && strings.TrimSpace(s.env(judge.KeyEnv)) == "" {
			preview.Missing = append(preview.Missing, "The judge needs "+judge.KeyEnv+".")
		}
		return preview
	}
	final, err := flow.RunPlan(plan, estimate, o)
	if err != nil || final.Cancel || !final.Done {
		return outcome{ExitOK, object{{"command", "guided"}}}, err
	}
	s.v = withChoice(s.v, final.Choice, all)
	s.event.Objective = final.Choice.Objective
	s.step = stepMap
	return s.run(file, true)
}

func endpointOf(v values) string {
	if e, ok := v.str("endpoint"); ok && slices.Contains(flow.Endpoints, e) {
		return e
	}
	return flow.Endpoints[0]
}

func perPartOf(v values) int {
	if text, ok := v.str("per-part"); ok {
		if n, err := strconv.Atoi(text); err == nil && n >= 10 && n <= 200 {
			return n
		}
	}
	return 20
}
