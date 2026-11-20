package cli

// The guided workflow: ezswap with no command on an interactive
// terminal. It runs, in order: a start menu; for a recording, the
// upstream's menu and the record dashboard; the plan of the map, with a
// live estimate; then map itself (the spend confirmation, the progress
// view, the printed map and the map explorer). Every step is also a
// command of its own.

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/metergraph/ezswap/demo"
	"github.com/metergraph/ezswap/internal/flow"
	"github.com/metergraph/ezswap/internal/judge"
	"github.com/metergraph/ezswap/internal/parts"
	"github.com/metergraph/ezswap/internal/report"
)

// Models the plan offers, unticked, when the settings file names none.
var suggestedModels = []string{
	"anthropic/claude-haiku-5.5",
	"openai/gpt-5.4-nano",
	"openai/gpt-5.4-mini",
	"google/gemini-3.8-flash",
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
	stepStart  = "step 1 of 4"
	stepRecord = "step 2 of 4"
	stepPlan   = "step 3 of 4"
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
	open := flow.Item{Title: "Open a finished map", Detail: "Explore a run.json that map wrote.", Prompt: "Run file:", Value: runFile}
	if !exists(filepath.Join(s.io.Cwd, runFile)) {
		open.Value = ""
	}
	start := flow.NewMenu(stepStart, "What would you like to do?", []string{
		"ezswap records the model calls your app makes, replays them on other models and twice on production's own, and maps where answers change beyond that noise.",
	}, []flow.Item{
		{Title: "Record my app's model calls", Detail: "Run a local proxy your app sends its calls through. Then map them."},
		{Title: "Map a trace file", Detail: "A file record wrote, or Langfuse, OpenTelemetry or raw provider logs.", Prompt: "Trace file:", Value: traces},
		open,
		{Title: "Try the demo", Detail: "The map of a synthetic app. No keys, no network."},
	})
	chosen, err := flow.RunMenu(start, o)
	if err != nil || chosen.Cancel {
		return done, err
	}
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
	case 2:
		return s.guidedOpen(strings.TrimSpace(chosen.Value))
	default:
		r, err := demo.MapRun()
		if err != nil {
			return outcome{}, err
		}
		m, err := report.BuildMap(r, report.MapOptions{})
		if err != nil {
			return outcome{}, err
		}
		code, _, err := s.exploreMap(r, m, "demo")
		return outcome{code, done.json}, err
	}
	return s.guidedPlan(file, o)
}

// guidedRecord asks for the upstream and records, fresh, until the person
// stops it. It returns the trace file, or "" when nothing was recorded.
func (s *session) guidedRecord(o flow.Options) (string, error) {
	menu, err := flow.RunMenu(flow.NewMenu(stepRecord, "Where does your app send its model calls?", []string{
		"ezswap forwards each call there unchanged and writes a copy to ezswap-out/traces.jsonl. Your app's key passes through and is never written. A previous recording is kept beside the new one.",
	}, upstreams), o)
	if err != nil || menu.Cancel {
		return "", err
	}
	upstream := strings.TrimSpace(menu.Value)
	s.v.strings["upstream"] = upstream
	s.v.bools["fresh"] = true
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
	// The models to start from: the settings file's, or suggestions.
	chosen := commaList([]string{func() string { m, _ := s.v.str("models"); return m }()})
	offered := append([]string(nil), chosen...)
	if len(chosen) == 0 {
		offered = append(offered, suggestedModels...)
	}
	// The parts, from a plan on any one model: parts do not depend on it.
	probe := s.quiet(withChoice(s.v, flow.Choice{Models: offered[:1], Endpoint: endpointOf(s.v), PerPart: perPartOf(s.v)}, nil))
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
	plan := flow.NewPlan(file, stepPlan, rows, start)
	plan.AllModels = offered
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
