package cli

// The map commands: `map`, `estimate --map`, `report` on a map run and
// `demo --map`.

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/metergraph/replay/demo"
	"github.com/metergraph/replay/internal/cases"
	"github.com/metergraph/replay/internal/core"
	"github.com/metergraph/replay/internal/parts"
	"github.com/metergraph/replay/internal/report"
	"github.com/metergraph/replay/internal/report/term"
	"github.com/metergraph/replay/internal/run"
	"github.com/metergraph/replay/internal/types"
)

// Options that only some commands take. A command not listed for an option
// refuses it, so a typo such as `run --per-part 30` does not pass silently.
var (
	mapPlanOptions = []string{"per-part", "part-key", "ignore"}
	mapViewOptions = []string{"only", "name"}
	recordOptions  = []string{"upstream", "port"}
	runOnlyOptions = []string{
		"models", "endpoint", "floor-endpoint", "api-key-env", "sample", "track", "temperature", "max-tokens",
		"reasoning-effort", "budget", "monthly-calls", "no-noise-floor", "labels", "allow-no-search",
		"allow-unpriced", "adapter", "explore", "explain", "yes", "concurrency",
	}
)

func (s *session) given(name string) bool {
	if _, ok := s.v.str(name); ok {
		return true
	}
	return s.v.bool(name) || len(s.v.list(name)) > 0
}

// checkOptions refuses options the command does not take. mapMode is whether
// the command is working on a map.
func (s *session) checkOptions(command string, mapMode bool) error {
	refuse := func(names []string, why string) error {
		for _, name := range names {
			if s.given(name) {
				return usagef("--" + name + " " + why)
			}
		}
		return nil
	}
	if command != "record" {
		if err := refuse(recordOptions, "is for record."); err != nil {
			return err
		}
	}
	switch command {
	case "record":
		if err := refuse(runOnlyOptions, "is not for record: it forwards calls and replays nothing."); err != nil {
			return err
		}
		return refuse(append(append([]string{"map"}, mapPlanOptions...), mapViewOptions...), "is not for record.")
	case "run":
		if err := refuse([]string{"map"}, "is for estimate, report and demo. To make a map, use the map command."); err != nil {
			return err
		}
	case "map":
		if err := refuse([]string{"map"}, "is not needed: map always makes a map."); err != nil {
			return err
		}
		if err := refuse([]string{"sample"}, "is for run. A map samples each part: use --per-part."); err != nil {
			return err
		}
		if err := refuse([]string{"labels", "explore"}, "is not for map."); err != nil {
			return err
		}
	case "demo":
		if mapMode {
			if err := refuse([]string{"explore"}, "is not for demo --map, which prints the map."); err != nil {
				return err
			}
			if err := refuse([]string{"out"}, "is not for demo --map, which writes nothing."); err != nil {
				return err
			}
		}
		return refuse(append(append([]string{}, mapPlanOptions...), mapViewOptions...), "is not for demo.")
	case "report":
		if err := refuse(mapPlanOptions, "is set when the map is made, not when it is printed."); err != nil {
			return err
		}
	}
	if !mapMode {
		return refuse(append(append([]string{}, mapPlanOptions...), mapViewOptions...), "is for a map: use the map command, or estimate --map.")
	}
	if command == "estimate" || command == "map" {
		if s.given("sample") {
			return usagef("--sample is for run. A map samples each part: use --per-part.")
		}
	}
	return nil
}

func overBudgetHint(mapMode bool) string {
	if mapMode {
		return "Raise --budget, lower --per-part, or keep fewer parts with --only."
	}
	return "Raise --budget or lower --sample."
}

// ignoredOption is the --ignore paths: the same syntax the outputs' own
// paths have, such as $.items[].sku.
func ignoredOption(list []string) ([]string, error) {
	var out []string
	for _, value := range list {
		for _, part := range strings.Split(value, ",") {
			path := strings.TrimSpace(part)
			if path == "" {
				continue
			}
			if _, ok := core.ParsePath(path); !ok || !strings.HasPrefix(path, "$") {
				return nil, usagef("--ignore takes a path such as $.sku, not " + path + ".")
			}
			if !slices.Contains(out, path) {
				out = append(out, path)
			}
		}
	}
	return out, nil
}

// mapPlanOptions fills in the plan options of a map and reads the names file.
func (s *session) mapPlanOptions(file string, options *run.PlanOptions, result *setupResult) error {
	perPartText, perPartGiven := s.v.str("per-part")
	perPart, err := intOption(perPartText, perPartGiven, "per-part", run.MinPerPart, run.MaxPerPart, run.DefaultPerPart)
	if err != nil {
		return err
	}
	ignored, err := ignoredOption(s.v.list("ignore"))
	if err != nil {
		return err
	}
	result.namesPath = parts.NamesPath(file)
	names, err := parts.ReadNames(result.namesPath)
	if err != nil {
		return usagef(err.Error())
	}
	result.names = names
	partKey, _ := s.v.str("part-key")
	options.Map = true
	options.PerPart = perPart
	options.Sample = 0
	options.Only = commaList(s.v.list("only"))
	options.Names = names
	options.Rename = s.v.list("name")
	options.PartKey = strings.TrimSpace(partKey)
	options.IgnoredFields = ignored
	return nil
}

// writeNames adds the file's new part keys to the names file, once, after
// parts are assigned. A directory that cannot be written does not stop the
// command.
func (s *session) writeNames(setup *setupResult) {
	changed, err := parts.WriteNames(setup.namesPath, setup.names, setup.plan.AllParts)
	switch {
	case err != nil:
		setup.namesState = "not written: the directory cannot be written"
	case changed:
		setup.namesState = "updated"
	default:
		setup.namesState = "unchanged"
	}
}

func comparedText(part run.PartPlan) string {
	if part.Reason != "" {
		return "no: " + part.Reason
	}
	if len(part.Paths) == 1 {
		return "1 field"
	}
	return fmt.Sprintf("%d fields", len(part.Paths))
}

// explainMapEstimate is the estimate of a map: one row per part, with how it
// was named, what it will be compared on and what it will cost.
func (s *session) explainMapEstimate(setup *setupResult) string {
	plan := setup.plan
	d := s.display()
	sep := " | "
	if !d.ascii {
		sep = " · "
	}
	clean := func(text string) string {
		text = term.CleanLine(text)
		if d.ascii {
			text = term.ToAscii(text)
		}
		return text
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("Read %s in %s from %s (%s format).", plural(plan.File.Calls, "call"), plural(plan.File.Traces, "trace"), plan.Input.File, plan.Input.Adapter))
	if plan.File.SkippedLines > 0 {
		lines = append(lines, fmt.Sprintf("Skipped %s that could not be read.", plural(plan.File.SkippedLines, "line")))
	}
	if len(plan.LeftOut) > 0 {
		reasonsLeft := make([]string, 0, len(plan.LeftOut))
		for reason := range plan.LeftOut {
			reasonsLeft = append(reasonsLeft, reason)
		}
		sort.Strings(reasonsLeft)
		total := 0
		for _, n := range plan.LeftOut {
			total += n
		}
		lines = append(lines, fmt.Sprintf("Left out %s that a replay cannot reproduce:", plural(total, "call")))
		for _, reason := range reasonsLeft {
			text, ok := reasons[types.NotReplayableReason(reason)]
			if reason == cases.ToolsNotOffered {
				text, ok = "production offered tools a replay does not", true
			}
			if !ok {
				text = reason
			}
			lines = append(lines, fmt.Sprintf("  %d because %s", plan.LeftOut[reason], text))
		}
	}
	for _, note := range plan.PartNotes {
		lines = append(lines, clean(note))
	}
	lines = append(lines, "")
	lines = append(lines, d.paint.Bold(strings.Join([]string{
		plural(len(plan.Parts), "part"), clean(plan.Input.File), plural(len(plan.Treatments), "treatment"),
		fmt.Sprintf("%d cases per part", plan.Settings.Sample),
	}, sep)))
	lines = append(lines, "")
	row := func(part, from, count, compared, expected string) string {
		return term.PadEnd(term.Truncate(part, 16, "~"), 16) + " " + term.PadEnd(from, 12) + term.PadStart(count, 5) + "  " +
			term.PadEnd(compared, 24) + term.PadStart(expected, 9)
	}
	lines = append(lines, d.paint.Muted(row("PART", "FROM", "CASES", "COMPARED", "EXPECTED")))
	for _, part := range plan.Parts {
		lines = append(lines, row(clean(part.Name), part.Source, fmt.Sprint(part.Sampled), comparedText(part), usd(part.Estimate.ExpectedUSD)))
	}
	lines = append(lines, "")
	estimate := plan.Estimate
	perCase := len(plan.Treatments)
	if plan.FloorEndpoint != "" {
		perCase += 2
	}
	lines = append(lines, fmt.Sprintf("%s in all: each case runs on %s%s.", plural(estimate.Replayable*perCase, "call"),
		plural(len(plan.Treatments), "model"), map[bool]string{true: ", plus twice on production's model", false: ""}[plan.FloorEndpoint != ""]))
	lines = append(lines, "Expected "+usd(estimate.ExpectedUSD)+", at most "+usd(estimate.UpperBoundUSD)+". Budget "+usd(plan.Settings.BudgetUSD)+".")
	if estimate.ExpectedUSD > plan.Settings.BudgetUSD {
		lines = append(lines, "The expected spend is over the budget, so map will refuse. "+overBudgetHint(true))
	} else if estimate.UpperBoundUSD > plan.Settings.BudgetUSD {
		lines = append(lines, "The run reserves each call's maximum before sending it and stops at the budget, largest parts first.")
	}
	if len(estimate.Unpriced) > 0 {
		lines = append(lines, "No price for: "+strings.Join(estimate.Unpriced, ", ")+". Their calls are not in the estimate.")
		if !s.v.bool("allow-unpriced") {
			lines = append(lines, "map refuses them unless you pass --allow-unpriced. Add a price: "+pricingHelpURL(setup.pricing))
		}
	}
	lines = append(lines, "Names file: "+s.shownPath(setup.namesPath)+" ("+setup.namesState+"). Edit a name there to rename a part; give two parts one name to merge them.")
	return strings.Join(lines, "\n")
}

func (s *session) estimateMap(setup *setupResult) (outcome, error) {
	plan := setup.plan
	s.say(s.explainMapEstimate(setup))
	notes, err := s.missingKeys(setup.endpoints)
	if err != nil {
		return outcome{}, err
	}
	for i, note := range notes {
		notes[i] = strings.Replace(note, "; run will need it.", "; map will need it.", 1)
		s.say("Note: " + notes[i])
	}
	exitCode := ExitOK
	if plan.Estimate.Replayable == 0 {
		exitCode = ExitNoReplayableCases
		s.say("No case in any part can be replayed, so there is nothing to run.")
	}
	partsJSON := make([]object, len(plan.Parts))
	for i, part := range plan.Parts {
		var reason any
		if part.Reason != "" {
			reason = part.Reason
		}
		paths := part.Paths
		if paths == nil {
			paths = []string{}
		}
		partsJSON[i] = object{
			{"keys", part.Keys}, {"name", part.Name}, {"source", part.Source}, {"available", part.Available},
			{"sampled", part.Sampled}, {"paths", paths}, {"compared", part.Reason == ""}, {"reason", reason},
			{"expectedUsd", part.Estimate.ExpectedUSD},
		}
	}
	return outcome{exitCode, object{
		{"command", "estimate"},
		{"input", plan.Input},
		{"file", object{{"calls", plan.File.Calls}, {"traces", plan.File.Traces}, {"skippedLines", plan.File.SkippedLines}}},
		{"sampled", len(plan.Cases)},
		{"perPart", plan.Settings.Sample},
		{"leftOut", plan.LeftOut},
		{"treatments", plan.Treatments},
		{"noiseFloor", plan.Settings.NoiseFloor},
		{"budgetUsd", plan.Settings.BudgetUSD},
		{"estimate", plan.Estimate},
		{"overBudget", plan.Estimate.ExpectedUSD > plan.Settings.BudgetUSD},
		{"parts", partsJSON},
		{"namesFile", object{{"path", setup.namesPath}, {"state", setup.namesState}}},
		{"notes", notes},
	}}, nil
}

// printMap prints the map on stdout.
func (s *session) printMap(m *report.Map) {
	d := s.display()
	s.say(strings.TrimRight(report.RenderMap(m, report.RenderOptions{Width: d.width, Paint: d.paint, ASCII: d.ascii}), " \n\t"))
}

// demoMap prints the map of the bundled synthetic run. It reads nothing from
// the network and writes no file.
func (s *session) demoMap() (outcome, error) {
	r, err := demo.MapRun()
	if err != nil {
		return outcome{}, err
	}
	m, err := report.BuildMap(r, report.MapOptions{})
	if err != nil {
		return outcome{}, err
	}
	s.say("A map of a synthetic support desk with five parts, recorded so it needs no keys and no network.")
	s.say("Make one from your own traces:")
	s.say("  metergraph-replay map traces.jsonl --models openai/gpt-5-mini")
	s.say("")
	s.printMap(m)
	return outcome{ExitOK, object{{"command", "demo"}, {"parts", m.Parts}}}, nil
}

// commaList splits repeated, comma-separated option values.
func commaList(values []string) []string {
	var out []string
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}
