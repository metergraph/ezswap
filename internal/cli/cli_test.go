package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/metergraph/replay/internal/types"
)

const (
	key      = "sk-or-test-not-a-real-key-4242"
	otherKey = "sk-openai-test-not-a-real-key-7777"
)

var now = func() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) }

// ---------------------------------------------------------------------------
// Trace files (test/run-helpers.ts)

type traceOptions struct{ truncated bool }

func traceLine(t *testing.T, n int, o traceOptions) string {
	t.Helper()
	text, _ := json.Marshal(map[string]any{"category": "bug", "n": n})
	line, err := json.Marshal(map[string]any{
		"schema":   "metergraph.replay-trace/v1",
		"id":       fmt.Sprintf("call-%d", n),
		"trace_id": fmt.Sprintf("trace-%d", n),
		"provider": "openai",
		"request": map[string]any{
			"model":           "gpt-4.1-mini",
			"system":          "Label the ticket for example.com as JSON.",
			"messages":        []any{map[string]any{"role": "user", "parts": []any{map[string]any{"type": "text", "content": fmt.Sprintf("Ticket %d: the dashboard is slow.", n)}}}},
			"response_format": map[string]any{"type": "json_object"},
			"params":          map[string]any{"temperature": 0.2, "max_tokens": 200},
		},
		"response": map[string]any{
			"text":          string(text),
			"finish_reason": "stop",
			"usage":         map[string]any{"input_tokens": 40, "output_tokens": 12},
			"latency_ms":    800,
		},
		"meta": map[string]any{"ts": time.Date(2026, 10, 1, 0, n, 0, 0, time.UTC).Format("2006-01-02T15:04:05.000Z"), "truncated": o.truncated, "missing": []any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

func writeTraces(t *testing.T, count int, options func(n int) traceOptions) string {
	t.Helper()
	if options == nil {
		options = func(int) traceOptions { return traceOptions{} }
	}
	lines := make([]string, 0, count)
	for n := 1; n <= count; n++ {
		lines = append(lines, traceLine(t, n, options(n)))
	}
	path := filepath.Join(t.TempDir(), "traces.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---------------------------------------------------------------------------
// A fake endpoint: every provider request goes to an httptest server.

type endpoint struct {
	mu      sync.Mutex
	fetches int
	bodies  []string
	reply   func() (int, string)
	server  *httptest.Server
}

func chatReply(content string, cost *float64) string {
	usage := map[string]any{"prompt_tokens": 50, "completion_tokens": 10}
	if cost != nil {
		usage["cost"] = *cost
	}
	body, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
		"usage":   usage,
	})
	return string(body)
}

func okReply(cost *float64) func() (int, string) {
	return func() (int, string) { return 200, chatReply(`{"category":"bug"}`, cost) }
}

func newEndpoint(t *testing.T, reply func() (int, string)) *endpoint {
	e := &endpoint{reply: reply}
	e.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		e.mu.Lock()
		e.fetches++
		e.bodies = append(e.bodies, string(body))
		reply := e.reply
		e.mu.Unlock()
		status, text := reply()
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, text)
	}))
	t.Cleanup(e.server.Close)
	return e
}

// client sends every request, whatever its host, to the fake endpoint.
func (e *endpoint) client() *http.Client {
	target, _ := url.Parse(e.server.URL)
	return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// ---------------------------------------------------------------------------

type captured struct {
	code    int
	stdout  string
	stderr  string
	fetches int
	bodies  []string
}

type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// cli runs the CLI in-process: no terminal, OPENROUTER_API_KEY set, a fixed
// clock, a stop signal that never fires. change adjusts the IO.
func cli(t *testing.T, args []string, change func(*IO), reply func() (int, string)) captured {
	t.Helper()
	if reply == nil {
		reply = okReply(nil)
	}
	e := newEndpoint(t, reply)
	var stdout, stderr lockedBuffer
	o := IO{
		Stdout:     &stdout,
		Stderr:     &stderr,
		Env:        map[string]string{"OPENROUTER_API_KEY": key},
		HTTPClient: e.client(),
		Now:        now,
		Signal:     context.Background(),
	}
	if change != nil {
		change(&o)
	}
	code := Main(args, o)
	e.mu.Lock()
	defer e.mu.Unlock()
	return captured{code: code, stdout: stdout.String(), stderr: stderr.String(), fetches: e.fetches, bodies: append([]string{}, e.bodies...)}
}

func noEnv(o *IO) { o.Env = map[string]string{} }

var models = []string{"--models", "google/gemini-2.5-flash-lite"}

func args(parts ...any) []string {
	var out []string
	for _, p := range parts {
		switch v := p.(type) {
		case string:
			out = append(out, v)
		case []string:
			out = append(out, v...)
		}
	}
	return out
}

func decode(t *testing.T, text string) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 1 {
		t.Fatalf("not one JSON line:\n%s", text)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &out); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	return out
}

func errorCode(t *testing.T, text string) string {
	t.Helper()
	return decode(t, text)["error"].(map[string]any)["code"].(string)
}

func match(t *testing.T, pattern, text string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("no match for %s in:\n%s", pattern, text)
	}
}

func noMatch(t *testing.T, pattern, text string) {
	t.Helper()
	if regexp.MustCompile(pattern).MatchString(text) {
		t.Fatalf("unexpected match for %s in:\n%s", pattern, text)
	}
}

func TestHelpVersionAndUsageErrors(t *testing.T) {
	help := cli(t, []string{"--help"}, nil, nil)
	if help.code != ExitOK || help.stdout != Help {
		t.Fatalf("--help: %d", help.code)
	}
	for _, code := range []string{"0", "1", "2", "3", "4", "5", "6"} {
		match(t, `(?m)^  `+code+`  `, Help)
	}
	noMatch(t, "—", Help)
	version := cli(t, []string{"--version"}, nil, nil)
	if version.code != ExitOK || version.stdout != Version+"\n" {
		t.Fatalf("--version: %q", version.stdout)
	}
	if v := cli(t, []string{"-v", "--json"}, nil, nil); decode(t, v.stdout)["version"] != Version {
		t.Fatal("--version --json")
	}
	bad := cli(t, []string{"run", "x.jsonl", "--frobnicate"}, nil, nil)
	if bad.code != ExitUsage {
		t.Fatalf("a bad option: %d", bad.code)
	}
	match(t, `^metergraph-replay: Unknown option '--frobnicate'`, bad.stderr)
	match(t, `Run metergraph-replay --help for usage.\n$`, bad.stderr)
	if none := cli(t, nil, nil, nil); none.code != ExitUsage || none.stdout != Help {
		t.Fatal("no command prints the help and exits 2")
	}
	if unknown := cli(t, []string{"fly"}, nil, nil); unknown.code != ExitUsage {
		t.Fatal("an unknown command")
	}
	if h := cli(t, []string{"help"}, nil, nil); h.code != ExitOK {
		t.Fatal("help is a command")
	}
}

func TestOptionParsing(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"estimate", "x", "--models"}, "Option '--models <value>' argument missing"},
		{[]string{"estimate", "x", "--models", "--labels"}, "Option '--models' argument is ambiguous."},
		{[]string{"demo", "--json=1"}, "Option '--json' does not take an argument"},
		{[]string{"demo", "-yx"}, "Unknown option '-x'."},
		{[]string{"estimate", "a", "b"}, "estimate takes one trace file."},
		{[]string{"demo", "file"}, "demo takes no file."},
		{[]string{"report"}, "report takes one run.json."},
	}
	for _, c := range cases {
		out := cli(t, c.argv, nil, nil)
		if out.code != ExitUsage || !strings.Contains(out.stderr, c.want) {
			t.Fatalf("%v: %d %q", c.argv, out.code, out.stderr)
		}
	}
	file := writeTraces(t, 1, nil)
	for _, c := range []struct {
		flag, value, want string
	}{
		{"--sample", "0", "--sample must be a whole number from 1 to 200."},
		{"--sample", "201", "--sample must be a whole number from 1 to 200."},
		{"--sample", "2.5", "--sample must be a whole number from 1 to 200."},
		{"--concurrency", "0", "--concurrency must be a whole number from 1 to 64."},
		{"--budget", "lots", "--budget must be an amount in dollars, such as 2.00."},
		{"--temperature", "9", "Temperature must be a number from 0 to 2"},
		{"--track", "$.[", ""},
	} {
		out := cli(t, args("estimate", file, models, c.flag, c.value), nil, nil)
		if out.code != ExitUsage || !strings.Contains(out.stderr, c.want) {
			t.Fatalf("%s %s: %d %q", c.flag, c.value, out.code, out.stderr)
		}
	}
	// --name=value and a dollar sign on the budget.
	ok := cli(t, args("estimate", file, "--models=google/gemini-2.5-flash-lite", "--budget=$2.50", "--sample=1"), nil, nil)
	if ok.code != ExitOK {
		t.Fatalf("--name=value: %d %s", ok.code, ok.stderr)
	}
	match(t, `Budget: \$2\.50\.`, ok.stdout)
	if out := cli(t, args("estimate", file), nil, nil); out.code != ExitUsage || !strings.Contains(out.stderr, "Name the models to replay on with --models.") {
		t.Fatal("--models is required")
	}
}

func TestEstimateExplainsAndCallsNothing(t *testing.T) {
	file := writeTraces(t, 4, func(n int) traceOptions { return traceOptions{truncated: n == 4} })
	out := cli(t, args("estimate", file, models), nil, nil)
	if out.code != ExitOK || out.fetches != 0 {
		t.Fatalf("code %d fetches %d", out.code, out.fetches)
	}
	match(t, `3 can be replayed, 1 cannot`, out.stdout)
	match(t, `1 because the captured content was cut short`, out.stdout)
	match(t, `Expected spend: about \$`, out.stdout)
	match(t, `Most it can spend: \$`, out.stdout)
	noMatch(t, "—", out.stdout)
}

func TestEstimateJSON(t *testing.T) {
	file := writeTraces(t, 3, nil)
	out := cli(t, args("estimate", file, models, "--json", "--endpoint", "openai"), noEnv, nil)
	if out.code != ExitOK || out.stderr != "" {
		t.Fatalf("code %d stderr %q", out.code, out.stderr)
	}
	j := decode(t, out.stdout)
	if j["ok"] != true || j["command"] != "estimate" || j["estimate"].(map[string]any)["replayable"] != 3.0 {
		t.Fatalf("%v", j)
	}
	if fmt.Sprint(j["notes"]) != "[OPENAI_API_KEY is not set; run will need it.]" {
		t.Fatalf("notes %v", j["notes"])
	}
	if !strings.HasPrefix(out.stdout, `{"ok":true,"exitCode":0,"command":"estimate",`) {
		t.Fatalf("keys in order: %s", out.stdout[:60])
	}
}

func TestNoReplayableCasesExits3(t *testing.T) {
	file := writeTraces(t, 2, func(int) traceOptions { return traceOptions{truncated: true} })
	if out := cli(t, args("estimate", file, models), nil, nil); out.code != ExitNoReplayableCases {
		t.Fatalf("estimate: %d", out.code)
	}
	out := cli(t, args("run", file, models, "--yes", "--json"), nil, nil)
	if out.code != ExitNoReplayableCases || errorCode(t, out.stdout) != "no_replayable_cases" || out.fetches != 0 {
		t.Fatalf("run: %d %s", out.code, out.stdout)
	}
}

func TestRunRefusesWithoutATerminalOrYes(t *testing.T) {
	file := writeTraces(t, 2, nil)
	out := cli(t, args("run", file, models, "--out", t.TempDir()), nil, nil)
	if out.code != ExitRefused || out.fetches != 0 {
		t.Fatalf("%d %d", out.code, out.fetches)
	}
	match(t, `--yes`, out.stderr)
}

func TestRunAsksOnATerminalAndANoSendsNothing(t *testing.T) {
	file := writeTraces(t, 2, nil)
	asked := ""
	out := cli(t, args("run", file, models, "--out", t.TempDir()), func(o *IO) {
		o.StdinTTY = true
		o.Confirm = func(question string) (bool, error) {
			asked = question
			return false, nil
		}
	}, nil)
	match(t, `^Spend up to \$\S+ \(about \$\S+ expected\)\? \[y/N\] $`, asked)
	if out.code != ExitRefused || out.fetches != 0 {
		t.Fatalf("%d %d", out.code, out.fetches)
	}
	match(t, `Nothing was sent\.`, out.stderr)
}

func TestRunAsksOnStdinWithoutAColourTerminal(t *testing.T) {
	// stdin is a terminal but stderr is not: the question is a plain line, the answer a line.
	file := writeTraces(t, 1, nil)
	for _, answer := range []string{"yes\n", "Y\n"} {
		out := cli(t, args("run", file, models, "--out", t.TempDir(), "--no-noise-floor"), func(o *IO) {
			o.StdinTTY = true
			o.Stdin = strings.NewReader(answer)
		}, nil)
		if out.code != ExitOK || out.fetches != 1 {
			t.Fatalf("%q: %d %d %s", answer, out.code, out.fetches, out.stderr)
		}
		match(t, `^Spend up to `, out.stderr)
	}
	out := cli(t, args("run", file, models, "--out", t.TempDir()), func(o *IO) {
		o.StdinTTY = true
		o.Stdin = strings.NewReader("\n")
	}, nil)
	if out.code != ExitRefused || out.fetches != 0 {
		t.Fatal("no is the default")
	}
}

func TestRunRefusesOverBudgetAndUnpriced(t *testing.T) {
	file := writeTraces(t, 3, nil)
	over := cli(t, args("run", file, models, "--budget", "0", "--yes", "--json"), nil, nil)
	if over.code != ExitRefused || errorCode(t, over.stdout) != "over_budget" {
		t.Fatalf("%d %s", over.code, over.stdout)
	}
	unpriced := cli(t, args("run", file, "--models", "example/unknown-model", "--yes", "--json"), nil, nil)
	if unpriced.code != ExitRefused || errorCode(t, unpriced.stdout) != "unpriced_models" {
		t.Fatalf("%d %s", unpriced.code, unpriced.stdout)
	}
	if over.fetches+unpriced.fetches != 0 {
		t.Fatal("nothing sent")
	}
}

func TestMissingKeyExits5AndNeverPrintsAKey(t *testing.T) {
	file := writeTraces(t, 2, nil)
	out := cli(t, args("run", file, "--models", "openai:gpt-5-mini,google/gemini-2.5-flash-lite", "--yes"), func(o *IO) {
		o.Env = map[string]string{"OPENAI_API_KEY": otherKey}
	}, nil)
	if out.code != ExitProviderConfig || out.fetches != 0 {
		t.Fatalf("%d %d", out.code, out.fetches)
	}
	match(t, `OPENROUTER_API_KEY is not set`, out.stderr)
	for _, text := range []string{out.stdout, out.stderr} {
		if strings.Contains(text, otherKey) || strings.Contains(text, key) {
			t.Fatal("a key was printed")
		}
	}
	j := cli(t, args("run", file, models, "--yes", "--json"), noEnv, nil)
	if j.code != ExitProviderConfig || errorCode(t, j.stdout) != "credential_missing" {
		t.Fatalf("%d %s", j.code, j.stdout)
	}
}

func TestRunReplaysWritesAndPrintsTheReport(t *testing.T) {
	file := writeTraces(t, 2, nil)
	out := t.TempDir()
	cost := 0.00002
	result := cli(t, args("run", file, models, "--yes", "--out", out, "--track", "$.category"), nil, okReply(&cost))
	if result.code != ExitOK {
		t.Fatalf("%d %s", result.code, result.stderr)
	}
	if result.fetches != 6 {
		t.Fatalf("fetches %d", result.fetches)
	}
	runText, err := os.ReadFile(filepath.Join(out, "run.json"))
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
	if len(r.Replays) != 6 {
		t.Fatalf("replays %d", len(r.Replays))
	}
	for _, replay := range r.Replays {
		if replay.CostSource == nil || *replay.CostSource != "endpoint" {
			t.Fatal("cost from the endpoint")
		}
	}
	if fmt.Sprint(r.Settings.TrackedFields) != "[$.category]" {
		t.Fatalf("tracked %v", r.Settings.TrackedFields)
	}
	if r.Tool.Version != Version {
		t.Fatalf("the run records the version: %q", r.Tool.Version)
	}
	if _, err := os.Stat(filepath.Join(out, "report.html")); err == nil {
		t.Fatal("no HTML report")
	}
	match(t, `Keep this running on live traffic:`, result.stdout)
	match(t, `(?m)^https://www\.metergraph\.dev/replay\?\S*utm_medium=cli&utm_campaign=`+Version, result.stdout)
	match(t, `Explore every call:\s+metergraph-replay report \S*run\.json`, result.stdout)
	match(t, `Notes: add --explain to the report command\s+below\.`, result.stdout)
	match(t, `^[\x20-\x7e\n]*$`, result.stdout)
	var body map[string]any
	json.Unmarshal([]byte(result.bodies[0]), &body)
	if body["temperature"] != 0.2 {
		t.Fatalf("production's settings travel with the replay: %v", body["temperature"])
	}
}

func TestRunSettingsApplyToEveryTreatment(t *testing.T) {
	file := writeTraces(t, 1, nil)
	result := cli(t, args("run", file, models, "--temperature", "0.7", "--max-tokens", "64", "--no-noise-floor", "--yes", "--out", t.TempDir(), "--json"), nil, nil)
	if result.code != ExitOK || result.fetches != 1 {
		t.Fatalf("%d %d %s", result.code, result.fetches, result.stdout)
	}
	var body map[string]any
	json.Unmarshal([]byte(result.bodies[0]), &body)
	if body["temperature"] != 0.7 || body["max_tokens"] != 64.0 {
		t.Fatalf("%v", body)
	}
	if fmt.Sprint(decode(t, result.stdout)["replays"]) != "map[failed:0 ok:1 skipped:0]" {
		t.Fatal(result.stdout)
	}
}

func TestRunStoppedEarlyByTheBudgetExits6(t *testing.T) {
	file := writeTraces(t, 5, nil)
	out := t.TempDir()
	cost := 0.5
	result := cli(t, args("run", file, models, "--yes", "--out", out, "--json", "--concurrency", "1"), nil, okReply(&cost))
	if result.code != ExitStoppedEarly || decode(t, result.stdout)["stopped"] != "reservation_exceeded" || result.fetches != 1 {
		t.Fatalf("%d %d %s", result.code, result.fetches, result.stdout)
	}
	if _, err := os.Stat(filepath.Join(out, "run.json")); err != nil {
		t.Fatal("run.json written")
	}
}

func TestInterruptedRunWritesWhatExists(t *testing.T) {
	file := writeTraces(t, 3, nil)
	out := t.TempDir()
	stop, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := cli(t, args("run", file, models, "--yes", "--out", out, "--json", "--concurrency", "1"), func(o *IO) { o.Signal = stop }, func() (int, string) {
		cancel()
		return 200, chatReply(`{"category":"bug"}`, nil)
	})
	if result.code != ExitStoppedEarly || decode(t, result.stdout)["stopped"] != "interrupted" {
		t.Fatalf("%d %s", result.code, result.stdout)
	}
	var r types.RunFile
	text, _ := os.ReadFile(filepath.Join(out, "run.json"))
	json.Unmarshal(text, &r)
	ok := 0
	for _, replay := range r.Replays {
		if replay.Status == "ok" {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("one answered, %d", ok)
	}
}

func TestRunProgressLineOnATerminal(t *testing.T) {
	// stderr is a terminal, stdin is not: the plain progress line, no ANSI.
	file := writeTraces(t, 1, nil)
	result := cli(t, args("run", file, models, "--yes", "--out", t.TempDir(), "--no-noise-floor"), func(o *IO) { o.StderrTTY = true }, nil)
	if result.code != ExitOK {
		t.Fatal(result.stderr)
	}
	if result.stderr != "\r  1/1 calls, spent $0.0000   \n" && !strings.HasPrefix(result.stderr, "\r  1/1 calls, spent ") {
		t.Fatalf("progress %q", result.stderr)
	}
	noMatch(t, "\x1b", result.stderr)
}

func TestRunExplain(t *testing.T) {
	file := writeTraces(t, 1, nil)
	result := cli(t, args("run", file, models, "--yes", "--out", t.TempDir(), "--explain"), nil, nil)
	if result.code != ExitOK {
		t.Fatal(result.stderr)
	}
	match(t, `Notes: below\.`, result.stdout)
	match(t, `(?m)^NOTES$`, result.stdout)
	match(t, `itself: gpt-4\.1-mini replayed twice`, result.stdout)
}

func TestDemoAndReport(t *testing.T) {
	out := t.TempDir()
	d := cli(t, args("demo", "--out", out, "--json"), noEnv, nil)
	if d.code != ExitOK || d.fetches != 0 {
		t.Fatalf("%d %d", d.code, d.fetches)
	}
	j := decode(t, d.stdout)
	runFile := j["runFile"].(string)
	text, err := os.ReadFile(runFile)
	if err != nil {
		t.Fatal(err)
	}
	var r types.RunFile
	json.Unmarshal(text, &r)
	if r.Schema != types.RunSchema || len(r.Replays) == 0 {
		t.Fatal("the demo run")
	}
	match(t, `utm_medium=cli`, j["hostedLink"].(string))

	plain := cli(t, args("demo", "--out", out), noEnv, nil)
	match(t, `gpt-4\.1-mini calls replayed`, plain.stdout)
	match(t, `Explore every call:\s+metergraph-replay report \S*run\.json`, plain.stdout)
	match(t, `Notes: run with --explain\.`, plain.stdout)
	noMatch(t, "\x1b", plain.stdout)

	report := cli(t, args("report", runFile), nil, nil)
	if report.code != ExitOK {
		t.Fatal(report.stderr)
	}
	match(t, `(?m)^COST$`, report.stdout)
	match(t, `^[\x20-\x7e\n]*$`, report.stdout)
	explained := cli(t, args("report", runFile, "--explain"), nil, nil)
	match(t, `(?m)^NOTES$`, explained.stdout)
	match(t, `Notes: below\.`, explained.stdout)
	if missing := cli(t, args("report", filepath.Join(out, "nope.json")), nil, nil); missing.code != ExitUsage {
		t.Fatal("a missing run file")
	}
	if withOut := cli(t, args("report", runFile, "--out", out), nil, nil); withOut.code != ExitUsage {
		t.Fatal("report takes no --out")
	}
	notJSON := filepath.Join(out, "bad.json")
	os.WriteFile(notJSON, []byte("{"), 0o666)
	if bad := cli(t, args("report", notJSON), nil, nil); !strings.Contains(bad.stderr, "is not JSON.") {
		t.Fatal(bad.stderr)
	}
	os.WriteFile(notJSON, []byte("[]"), 0o666)
	if bad := cli(t, args("report", notJSON), nil, nil); !strings.Contains(bad.stderr, "is not a metergraph-replay run file.") {
		t.Fatal(bad.stderr)
	}
}

func TestReportOpensTheExplorerOnATerminal(t *testing.T) {
	out := t.TempDir()
	runFile := decode(t, cli(t, args("demo", "--out", out, "--json"), noEnv, nil).stdout)["runFile"].(string)
	var opened []ExploreOptions
	reason := "quit"
	var printed lockedBuffer
	tty := func(env map[string]string) func(*IO) {
		return func(o *IO) {
			o.Stdout = &printed
			o.StdoutTTY, o.StdinTTY = true, true
			o.Columns = 100
			o.Env = env
			o.Explore = func(_ *types.RunFile, eo ExploreOptions) (string, error) {
				opened = append(opened, eo)
				return reason, nil
			}
		}
	}
	if code := cli(t, args("report", runFile), tty(map[string]string{}), nil).code; code != ExitOK {
		t.Fatal(code)
	}
	if len(opened) != 1 || !strings.HasSuffix(opened[0].Name, "run.json") || !opened[0].Color || opened[0].ASCII {
		t.Fatalf("%+v", opened)
	}
	cli(t, args("report", runFile, "--no-color", "--ascii"), tty(map[string]string{}), nil)
	if opened[1].Color || !opened[1].ASCII {
		t.Fatal("--no-color --ascii")
	}
	cli(t, args("report", runFile), tty(map[string]string{"NO_COLOR": "1"}), nil)
	if opened[2].Color {
		t.Fatal("NO_COLOR turns colour off")
	}

	printed = lockedBuffer{}
	if code := cli(t, args("report", runFile, "--no-interactive"), tty(map[string]string{}), nil).code; code != ExitOK || len(opened) != 3 {
		t.Fatal("--no-interactive never opens the explorer")
	}
	match(t, "\x1b\\[", printed.String())
	match(t, `(?m)^COST`, unstyled(printed.String()))
	match(t, `·`, printed.String())

	cli(t, args("report", runFile), tty(map[string]string{"CI": "true"}), nil)
	cli(t, args("report", runFile), tty(map[string]string{"TERM": "dumb"}), nil)
	if len(opened) != 3 {
		t.Fatal("CI and a dumb terminal print instead")
	}
	if code := cli(t, args("demo", "--out", out, "--explore"), tty(map[string]string{}), nil).code; code != ExitOK || len(opened) != 4 {
		t.Fatal("demo --explore opens the explorer")
	}
	reason = "SIGTERM"
	if code := cli(t, args("report", runFile), tty(map[string]string{}), nil).code; code != ExitSIGTERM {
		t.Fatalf("SIGTERM: %d", code)
	}
	reason = "SIGINT"
	if code := cli(t, args("report", runFile), tty(map[string]string{}), nil).code; code != ExitSIGINT {
		t.Fatalf("SIGINT: %d", code)
	}
}

// unstyled strips styles.
func unstyled(s string) string {
	return regexp.MustCompile("\x1b\\[[0-9;]*m|\x1b\\]8;;[^\x1b]*\x1b\\\\").ReplaceAllString(s, "")
}

func TestMissingTraceFileIsAUsageError(t *testing.T) {
	out := cli(t, args("estimate", filepath.Join(t.TempDir(), "missing.jsonl"), models, "--json"), nil, nil)
	if out.code != ExitUsage || errorCode(t, out.stdout) != "file_not_found" {
		t.Fatalf("%d %s", out.code, out.stdout)
	}
}

func TestMonthlyCallsMustBeAWholeNumber(t *testing.T) {
	out := cli(t, args("demo", "--monthly-calls", "lots", "--out", t.TempDir()), noEnv, nil)
	if out.code != ExitUsage {
		t.Fatal(out.code)
	}
	match(t, `--monthly-calls must be a whole number`, out.stderr)
	ok := cli(t, args("demo", "--monthly-calls", "1_000,000", "--out", t.TempDir()), noEnv, nil)
	if ok.code != ExitOK {
		t.Fatal(ok.stderr)
	}
}

func TestShownPath(t *testing.T) {
	if got := relativeBelow("/a/b", "/a/b/c/run.json"); got != filepath.Join("c", "run.json") {
		t.Fatal(got)
	}
	if got := relativeBelow("/a/b", "/a/x/run.json"); got != "/a/x/run.json" {
		t.Fatal(got)
	}
}

func TestRunDrawsTheLiveViewOnATerminal(t *testing.T) {
	// Every stream a terminal: the run goes through the progress view (drawn
	// on stderr, here a buffer; the run ends before its first frame), and the
	// run, its file and its report are the same.
	file := writeTraces(t, 2, nil)
	out := t.TempDir()
	result := cli(t, args("run", file, models, "--yes", "--out", out), func(o *IO) {
		o.StdinTTY, o.StdoutTTY, o.StderrTTY = true, true, true
		o.Columns = 100
	}, nil)
	if result.code != ExitOK || result.fetches != 6 {
		t.Fatalf("%d %d %s", result.code, result.fetches, result.stderr)
	}
	match(t, `Replayed 6 calls: 6 answered, 0 failed\.`, result.stdout)
	if _, err := os.Stat(filepath.Join(out, "run.json")); err != nil {
		t.Fatal(err)
	}
}
