package cli

import (
	"encoding/json"
	"fmt"
	"github.com/metergraph/replay/internal/mapview"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/metergraph/replay/internal/types"
)

type partSpec struct {
	// The "part" field, or "" for none.
	name   string
	system string
	count  int
	// Extra top-level fields on every record.
	extra map[string]any
}

// writePartTraces writes a trace file whose calls come from several parts.
// Every recorded answer is {"category": "bug", "n": <call number>}.
func writePartTraces(t *testing.T, specs ...partSpec) string {
	t.Helper()
	var lines []string
	n := 0
	for _, spec := range specs {
		for i := 0; i < spec.count; i++ {
			n++
			system := spec.system
			if system == "" {
				system = "Label the ticket for " + spec.name + " as JSON."
			}
			text, _ := json.Marshal(map[string]any{"category": "bug", "n": n})
			record := map[string]any{
				"schema":   "metergraph.replay-trace/v1",
				"id":       fmt.Sprintf("call-%d", n),
				"trace_id": fmt.Sprintf("trace-%d", n),
				"provider": "openai",
				"request": map[string]any{
					"model":           "gpt-4.1-mini",
					"system":          strings.ReplaceAll(system, "{n}", fmt.Sprintf("customer %c%c", 'a'+n%26, 'a'+n/26)),
					"messages":        []any{map[string]any{"role": "user", "parts": []any{map[string]any{"type": "text", "content": fmt.Sprintf("Ticket %d: the dashboard is slow.", n)}}}},
					"response_format": map[string]any{"type": "json_object"},
					"params":          map[string]any{"temperature": 0.2, "max_tokens": 200},
				},
				"response": map[string]any{"text": string(text), "finish_reason": "stop", "usage": map[string]any{"input_tokens": 40, "output_tokens": 12}, "latency_ms": 800},
				"meta":     map[string]any{"ts": time.Date(2026, 10, 1, 0, 0, n, 0, time.UTC).Format("2006-01-02T15:04:05.000Z"), "truncated": false, "missing": []any{}},
			}
			if spec.name != "" {
				record["part"] = spec.name
			}
			for key, value := range spec.extra {
				record[key] = value
			}
			line, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, string(line))
		}
	}
	path := filepath.Join(t.TempDir(), "traces.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	return path
}

func twoParts(t *testing.T) string {
	return writePartTraces(t, partSpec{name: "classify", count: 12}, partSpec{name: "route", count: 11})
}

func namesOf(t *testing.T, traces string) map[string]string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join(filepath.Dir(traces), "replay-parts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Parts map[string]string `json:"parts"`
	}
	if err := json.Unmarshal(text, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Parts
}

func TestEstimateMapListsPartsWritesNamesAndCallsNothing(t *testing.T) {
	file := twoParts(t)
	result := cli(t, args("estimate", file, "--map", models), nil, nil)
	if result.code != ExitOK || result.fetches != 0 {
		t.Fatalf("%d fetches %d %s", result.code, result.fetches, result.stderr)
	}
	match(t, `2 parts \| traces\.jsonl \| 1 treatment \| 20 cases per part`, result.stdout)
	match(t, `classify +name +12 +2 fields +\$`, result.stdout)
	match(t, `route +name +11 +2 fields +\$`, result.stdout)
	match(t, `69 calls in all`, result.stdout)
	match(t, `Budget \$5\.00\.`, result.stdout)
	match(t, `Names file: \S*replay-parts\.json \(updated\)`, result.stdout)
	if names := namesOf(t, file); names["name:classify"] != "classify" || names["name:route"] != "route" {
		t.Fatalf("names file %v", names)
	}
	again := cli(t, args("estimate", file, "--map", models), nil, nil)
	match(t, `replay-parts\.json \(unchanged\)`, again.stdout)
}

func TestEstimateMapJSON(t *testing.T) {
	file := twoParts(t)
	result := cli(t, args("estimate", file, "--map", models, "--per-part", "10", "--json"), nil, nil)
	out := decode(t, result.stdout)
	parts := out["parts"].([]any)
	first := parts[0].(map[string]any)
	if len(parts) != 2 || first["name"] != "classify" || first["sampled"] != 10.0 || first["available"] != 12.0 ||
		first["compared"] != true || first["reason"] != nil || first["source"] != "name" {
		t.Fatalf("%v", first)
	}
	if out["perPart"] != 10.0 || out["sampled"] != 20.0 || out["namesFile"].(map[string]any)["state"] != "updated" {
		t.Fatalf("%v", out)
	}
}

func TestNamesFileRenamesAndMerges(t *testing.T) {
	file := twoParts(t)
	names := filepath.Join(filepath.Dir(file), "replay-parts.json")
	os.WriteFile(names, []byte(`{"schema":"metergraph.replay-parts/v1","parts":{"name:classify":"triage","name:route":"triage","name:gone":"x"}}`), 0o644)
	result := cli(t, args("estimate", file, "--map", models), nil, nil)
	match(t, `1 part \|`, result.stdout)
	match(t, `triage +name +20 `, result.stdout)
	match(t, `Names for parts this file does not have were ignored: name:gone\.`, result.stdout)
	flagged := cli(t, args("estimate", file, "--map", models, "--name", "name:route=route-again", "--only", "route-again"), nil, nil)
	match(t, `route-again +name +11 `, flagged.stdout)
	noMatch(t, `triage`, flagged.stdout)
	if got := namesOf(t, file); got["name:route"] != "triage" {
		t.Fatalf("--name is for one command and does not change the file: %v", got)
	}
}

func TestNamesFileInvalidIsUsageError(t *testing.T) {
	file := twoParts(t)
	os.WriteFile(filepath.Join(filepath.Dir(file), "replay-parts.json"), []byte("{"), 0o644)
	result := cli(t, args("estimate", file, "--map", models), nil, nil)
	if result.code != ExitUsage {
		t.Fatalf("%d", result.code)
	}
	match(t, `replay-parts\.json is not valid JSON`, result.stderr)
}

func TestUnwritableDirectoryStillRuns(t *testing.T) {
	if os.Geteuid() == 0 || os.PathSeparator == '\\' {
		t.Skip("needs a directory the user cannot write")
	}
	file := twoParts(t)
	dir := filepath.Dir(file)
	os.Chmod(dir, 0o555)
	defer os.Chmod(dir, 0o755)
	result := cli(t, args("estimate", file, "--map", models), nil, nil)
	if result.code != ExitOK {
		t.Fatalf("%d %s", result.code, result.stderr)
	}
	match(t, `not written: the directory cannot be written`, result.stdout)
}

func TestPartOptionUsageErrors(t *testing.T) {
	file := twoParts(t)
	for name, c := range map[string]struct {
		args    []string
		pattern string
	}{
		"TestOnlyUnknownIsUsageError":    {args("estimate", file, "--map", models, "--only", "nope"), `--only nope matches no part\. The parts are: classify, route\.`},
		"TestNameUnknownKeyIsUsageError": {args("estimate", file, "--map", models, "--name", "nope=x"), `--name nope matches no part`},
		"TestNameBadFormIsUsageError":    {args("estimate", file, "--map", models, "--name", "classify"), `--name takes KEY=NAME`},
		"TestMapRejectsSample":           {args("map", file, models, "--sample", "5"), `--sample is for run`},
		"TestPerPartRejectedByRun":       {args("run", file, models, "--per-part", "30"), `--per-part is for a map`},
		"TestPerPartRejectedByEstimate":  {args("estimate", file, models, "--per-part", "30"), `--per-part is for a map`},
		"TestPerPartRange":               {args("estimate", file, "--map", models, "--per-part", "5"), `--per-part must be a whole number from 10 to 200`},
		"TestDemoMapRejectsExplore":      {args("demo", "--map", "--explore"), `--explore is not for demo --map`},
		"TestRecordRejectsModels":        {args("record", models), `--models is not for record`},
		"TestUpstreamOnlyForRecord":      {args("run", file, models, "--upstream", "https://example.com"), `--upstream is for record`},
		"TestIgnoreNeedsAPath":           {args("map", file, models, "--ignore", "sku"), `--ignore takes a path such as \$\.sku`},
		"TestRunRejectsMapFlag":          {args("run", file, models, "--map"), `use the map command`},
	} {
		result := cli(t, c.args, nil, nil)
		if result.code != ExitUsage || result.fetches != 0 {
			t.Errorf("%s: exit %d, fetches %d: %s", name, result.code, result.fetches, result.stderr)
			continue
		}
		match(t, c.pattern, result.stderr)
	}
}

func TestMapRefusesSingletonFingerprints(t *testing.T) {
	file := writePartTraces(t,
		partSpec{system: "Help {n} with their ticket.", count: 6},
		partSpec{name: "classify", count: 12},
	)
	result := cli(t, args("map", file, models, "--yes", "--json"), nil, nil)
	if result.code != ExitUsage || result.fetches != 0 || errorCode(t, result.stdout) != "parts_not_found" {
		t.Fatalf("%d %s", result.code, result.stdout)
	}
	named := cli(t, args("estimate", file, "--map", models, "--only", "classify"), nil, nil)
	if named.code != ExitOK {
		t.Fatalf("naming explicit parts in --only skips the refusal: %s", named.stderr)
	}
}

func TestMapRefusesOverBudgetWithoutProviderCall(t *testing.T) {
	result := cli(t, args("map", twoParts(t), models, "--yes", "--budget", "0.000001", "--json"), nil, nil)
	if result.code != ExitRefused || result.fetches != 0 || errorCode(t, result.stdout) != "over_budget" {
		t.Fatalf("%d %s", result.code, result.stdout)
	}
	text := cli(t, args("map", twoParts(t), models, "--yes", "--budget", "0.000001"), nil, nil)
	match(t, `lower --per-part, or keep fewer parts with --only`, text.stderr)
	match(t, `classify +name +12 `, text.stdout)
}

func TestMapNeedsYesWithoutTerminal(t *testing.T) {
	result := cli(t, args("map", twoParts(t), models, "--json"), nil, nil)
	if result.code != ExitRefused || result.fetches != 0 || errorCode(t, result.stdout) != "confirmation_required" {
		t.Fatalf("%d %s", result.code, result.stdout)
	}
}

func TestMapNoReplayableCases(t *testing.T) {
	file := writeTraces(t, 3, func(int) traceOptions { return traceOptions{truncated: true} })
	result := cli(t, args("map", file, models, "--yes", "--json"), nil, nil)
	if result.code != ExitNoReplayableCases || result.fetches != 0 || errorCode(t, result.stdout) != "no_replayable_cases" {
		t.Fatalf("%d %s", result.code, result.stdout)
	}
	text := cli(t, args("estimate", file, "--map", models), nil, nil)
	match(t, `Left out 3 calls that a replay cannot reproduce:\n  3 because the captured content was cut short`, text.stdout)
}

func TestMapReplaysWritesAndPrintsTheMap(t *testing.T) {
	file := twoParts(t)
	out := t.TempDir()
	cost := 0.00002
	result := cli(t, args("map", file, models, "--yes", "--out", out, "--ignore", "$.n"), nil, okReply(&cost))
	if result.code != ExitOK {
		t.Fatalf("%d %s", result.code, result.stderr)
	}
	if result.fetches != 69 {
		t.Fatalf("every sampled case runs on the treatment and twice on the floor: %d fetches", result.fetches)
	}
	runPath := filepath.Join(out, "run.json")
	runText, err := os.ReadFile(runPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(runText), key) || strings.Contains(result.stdout, key) {
		t.Fatal("the key leaked")
	}
	var r types.RunFile
	if err := json.Unmarshal(runText, &r); err != nil {
		t.Fatal(err)
	}
	if r.Settings.PerPart == nil || *r.Settings.PerPart != 20 || fmt.Sprint(r.Settings.IgnoredFields) != "[$.n]" || r.Settings.BudgetUSD != 5 {
		t.Fatalf("settings %+v", r.Settings)
	}
	if len(r.Cases) != 23 || r.Cases[0].PartKey != "name:classify" || r.Cases[0].PartName != "classify" || r.Cases[22].PartKey != "name:route" {
		t.Fatalf("cases part by part, largest part first: %d %q %q", len(r.Cases), r.Cases[0].PartKey, r.Cases[22].PartKey)
	}
	match(t, `PART +LABEL +CHANGED +RERUN +COST/CALL +LATENCY`, result.stdout)
	match(t, `classify +within noise +0 of 12 +0 of 12`, result.stdout)
	match(t, `route +within noise +0 of 11 +0 of 11`, result.stdout)
	match(t, `Print it again: metergraph-replay report \S*run\.json`, result.stdout)
	if names := namesOf(t, file); len(names) != 2 {
		t.Fatalf("map writes the names file: %v", names)
	}

	// report prints the same map from run.json, without the names file.
	os.Remove(filepath.Join(filepath.Dir(file), "replay-parts.json"))
	again := cli(t, args("report", runPath), nil, nil)
	if again.code != ExitOK || again.fetches != 0 {
		t.Fatalf("%d %s", again.code, again.stderr)
	}
	match(t, `classify +within noise +0 of 12`, again.stdout)
	renamed := cli(t, args("report", runPath, "--map", "--name", "classify=classify-ticket", "--only", "classify-ticket"), nil, nil)
	match(t, `classify-ticket  within noise`, renamed.stdout)
	noMatch(t, `route`, renamed.stdout)
	asJSON := decode(t, cli(t, args("report", runPath, "--json"), nil, nil).stdout)
	if parts := asJSON["parts"].([]any); len(parts) != 2 || parts[0].(map[string]any)["treatments"].([]any)[0].(map[string]any)["result"] != "within_noise" {
		t.Fatalf("%v", asJSON)
	}
	// On a terminal, the map is printed and then the map explorer opens on it.
	var explored *mapview.Data
	onTerminal := cli(t, args("report", runPath), func(o *IO) {
		o.StdinTTY, o.StdoutTTY = true, true
		o.ExploreMap = func(d *mapview.Data) (string, error) { explored = d; return mapview.ReasonQuit, nil }
	}, nil)
	match(t, `PART +LABEL`, onTerminal.stdout)
	if onTerminal.code != ExitOK || explored == nil || len(explored.Parts) != 2 || filepath.Base(explored.HTMLPath) != "map.html" {
		t.Fatalf("explorer %+v, exit %d", explored, onTerminal.code)
	}
	// Without a terminal, or with --no-interactive or --json, it does not open.
	explored = nil
	cli(t, args("report", runPath, "--no-interactive"), func(o *IO) {
		o.StdinTTY, o.StdoutTTY = true, true
		o.ExploreMap = func(d *mapview.Data) (string, error) { explored = d; return mapview.ReasonQuit, nil }
	}, nil)
	if explored != nil {
		t.Error("--no-interactive opened the explorer")
	}
}

func TestReportMapNeedsParts(t *testing.T) {
	out := t.TempDir()
	if result := cli(t, args("demo", "--out", out), nil, nil); result.code != ExitOK {
		t.Fatal(result.stderr)
	}
	result := cli(t, args("report", filepath.Join(out, "run.json"), "--map"), nil, nil)
	if result.code != ExitUsage {
		t.Fatalf("%d", result.code)
	}
	match(t, `This run has no parts`, result.stderr)
}

func TestBudgetStopLabelsFromFinishedCases(t *testing.T) {
	cost := 0.004
	out := t.TempDir()
	result := cli(t, args("map", twoParts(t), models, "--yes", "--out", out, "--budget", "0.05", "--concurrency", "1"), nil, okReply(&cost))
	if result.code != ExitStoppedEarly {
		t.Fatalf("%d %s\n%s", result.code, result.stderr, result.stdout)
	}
	match(t, `The run stopped early \((budget|reservation exceeded)\)\. Parts are labelled from the cases that finished\.`, result.stdout)
	match(t, `classify +too few calls +\d+ paired, needs 10`, result.stdout)
}

func TestDemoMapMakesNoNetworkCallAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	result := cli(t, args("demo", "--map"), func(o *IO) { o.Cwd = dir }, nil)
	if result.code != ExitOK || result.fetches != 0 {
		t.Fatalf("%d fetches %d %s", result.code, result.fetches, result.stderr)
	}
	match(t, `extract-order +beyond noise +9 of 20 +1 of 20`, result.stdout)
	match(t, `route-to-team +within noise +4 of 20 +3 of 20`, result.stdout)
	match(t, `summarise-thread too few calls +6 paired, needs 10`, result.stdout)
	match(t, `draft-reply +not comparable +free text`, result.stdout)
	match(t, `^[\x20-\x7e\n]*$`, result.stdout)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("demo --map wrote %v", entries)
	}
	wd, _ := os.Getwd()
	if _, err := os.Stat(filepath.Join(wd, "replay-out")); err == nil {
		t.Fatal("demo --map wrote replay-out")
	}
	if strings.Contains(result.stdout, "—") {
		t.Fatal("an em dash in the output")
	}
}

func TestPartKeyReadsTheDevelopersField(t *testing.T) {
	file := writePartTraces(t,
		partSpec{system: "Shared prompt.", count: 10, extra: map[string]any{"meta": map[string]any{"ts": "2026-10-01T00:00:00.000Z", "feature": "summarise"}}},
		partSpec{system: "Shared prompt.", count: 10, extra: map[string]any{"meta": map[string]any{"ts": "2026-10-02T00:00:00.000Z", "feature": "translate"}}},
	)
	plain := cli(t, args("estimate", file, "--map", models), nil, nil)
	match(t, `1 part \|`, plain.stdout)
	match(t, `part-[0-9a-f]{6} +fingerprint +20 `, plain.stdout)
	keyed := cli(t, args("estimate", file, "--map", models, "--part-key", "feature"), nil, nil)
	match(t, `2 parts \|`, keyed.stdout)
	match(t, `summarise +name +10 `, keyed.stdout)
	match(t, `translate +name +10 `, keyed.stdout)
}

func TestHelpNamesTheNewCommands(t *testing.T) {
	result := cli(t, args("--help"), nil, nil)
	for _, word := range []string{"map <traces>", "record [", "--per-part", "--only", "--name", "--part-key", "--ignore", "--upstream", "--port", "demo --map", "replay-parts.json"} {
		if !strings.Contains(result.stdout, word) {
			t.Errorf("help does not mention %q", word)
		}
	}
}
