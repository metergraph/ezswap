package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/metergraph/ezswap/internal/telemetry"
)

// collector is a stand-in for where events are sent.
type collector struct {
	mu     sync.Mutex
	bodies []string
	server *httptest.Server
}

func newCollector(t *testing.T) *collector {
	t.Helper()
	c := &collector{}
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.bodies = append(c.bodies, string(body))
		c.mu.Unlock()
	}))
	t.Cleanup(c.server.Close)
	return c
}

func (c *collector) sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.bodies...)
}

// reporting is an IO that could report: a key, a URL and a settings directory.
func (c *collector) reporting(dir string, more func(*IO)) func(*IO) {
	return func(o *IO) {
		o.ConfigDir, o.TelemetryURL, o.TelemetryKey = dir, c.server.URL, "test-key"
		if more != nil {
			more(o)
		}
	}
}

func TestNothingIsReportedUntilTelemetryIsTurnedOn(t *testing.T) {
	c := newCollector(t)
	dir := t.TempDir()
	file := writeTraces(t, 3, nil)
	estimate := args("estimate", file, "--models=google/gemini-2.5-flash-lite")
	if out := cli(t, estimate, c.reporting(dir, nil), nil); out.code != ExitOK {
		t.Fatalf("%d %s", out.code, out.stderr)
	}
	if got := c.sent(); len(got) != 0 {
		t.Fatalf("reported before any answer: %v", got)
	}
	// Answered no: still nothing.
	if _, err := telemetry.Answer(dir, false, now()); err != nil {
		t.Fatal(err)
	}
	cli(t, estimate, c.reporting(dir, nil), nil)
	if got := c.sent(); len(got) != 0 {
		t.Fatalf("reported after a no: %v", got)
	}
}

func TestAnOptedInCommandReportsOnlyPublicNames(t *testing.T) {
	c := newCollector(t)
	dir := t.TempDir()
	settings, err := telemetry.Answer(dir, true, now())
	if err != nil {
		t.Fatal(err)
	}
	file := writeTraces(t, 3, nil)
	out := cli(t, args("estimate", file, "--models=google/gemini-2.5-flash-lite,acme/private-finetune-7", "--allow-unpriced"), c.reporting(dir, nil), nil)
	got := c.sent()
	if len(got) != 1 {
		t.Fatalf("%d events, want 1 (exit %d: %s)", len(got), out.code, out.stderr)
	}
	body := got[0]
	for _, private := range []string{"acme", "finetune", file, "example.com", "Ticket", key} {
		if strings.Contains(body, private) {
			t.Errorf("the event holds %q: %s", private, body)
		}
	}
	var event struct {
		Key        string         `json:"api_key"`
		ID         string         `json:"distinct_id"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal([]byte(body), &event); err != nil {
		t.Fatal(err)
	}
	p := event.Properties
	if event.Key != "test-key" || event.ID != settings.ID || p["command"] != "estimate" || p["cases"] != float64(3) {
		t.Errorf("%s", body)
	}
	models, _ := json.Marshal(p["models"])
	if string(models) != `["google/gemini-2.5-flash-lite","other"]` {
		t.Errorf("models: %s", models)
	}
}

func TestAFailedCommandReportsItsErrorCodeNotItsMessage(t *testing.T) {
	c := newCollector(t)
	dir := t.TempDir()
	telemetry.Answer(dir, true, now())
	out := cli(t, args("estimate", "secret-customer.jsonl", "--models=google/gemini-2.5-flash-lite"), c.reporting(dir, nil), nil)
	got := c.sent()
	if out.code != ExitUsage || len(got) != 1 {
		t.Fatalf("%d, %d events", out.code, len(got))
	}
	if strings.Contains(got[0], "secret-customer") || !strings.Contains(got[0], `"error":"file_not_found"`) {
		t.Errorf("%s", got[0])
	}
}

func TestTheEnvironmentAndAnUnreachableCollectorKeepACommandQuiet(t *testing.T) {
	c := newCollector(t)
	dir := t.TempDir()
	telemetry.Answer(dir, true, now())
	file := writeTraces(t, 3, nil)
	estimate := args("estimate", file, "--models=google/gemini-2.5-flash-lite")
	for _, name := range []string{"DO_NOT_TRACK", "CI"} {
		cli(t, estimate, c.reporting(dir, func(o *IO) { o.Env[name] = "1" }), nil)
	}
	cli(t, estimate, c.reporting(dir, func(o *IO) { o.Env["EZSWAP_TELEMETRY"] = "0" }), nil)
	// Commands that are not work: help, --version and telemetry itself.
	cli(t, args("--version"), c.reporting(dir, nil), nil)
	cli(t, args("--help"), c.reporting(dir, nil), nil)
	cli(t, args("telemetry"), c.reporting(dir, nil), nil)
	if got := c.sent(); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	// Nowhere to send: the command's output and exit code are unchanged.
	plain := cli(t, estimate, nil, nil)
	down := cli(t, estimate, func(o *IO) { o.ConfigDir, o.TelemetryURL, o.TelemetryKey = dir, "http://127.0.0.1:1/", "test-key" }, nil)
	if down.code != plain.code || down.stdout != plain.stdout || down.stderr != plain.stderr {
		t.Errorf("an unreachable collector changed the command:\n%s\n%s", down.stdout, down.stderr)
	}
}

func TestTelemetryCommandTurnsItOnAndOff(t *testing.T) {
	c := newCollector(t)
	dir := t.TempDir()
	with := c.reporting(dir, nil)
	if out := cli(t, args("telemetry"), with, nil); out.code != ExitOK || !strings.Contains(out.stdout, "Telemetry is off.") {
		t.Fatalf("%d %s", out.code, out.stdout)
	}
	on := cli(t, args("telemetry", "on", "--json"), with, nil)
	if j := decode(t, on.stdout); j["enabled"] != true || j["sending"] != true {
		t.Fatalf("%s", on.stdout)
	}
	if s, answered := telemetry.Load(dir); !answered || !s.Enabled {
		t.Fatalf("%+v", s)
	}
	// On, but this build has no key: said plainly, and nothing is sent.
	noKey := cli(t, args("telemetry"), func(o *IO) { o.ConfigDir = dir }, nil)
	if !strings.Contains(noKey.stdout, "nowhere to send") {
		t.Errorf("%s", noKey.stdout)
	}
	blocked := cli(t, args("telemetry"), c.reporting(dir, func(o *IO) { o.Env["DO_NOT_TRACK"] = "1" }), nil)
	if !strings.Contains(blocked.stdout, "DO_NOT_TRACK is set") {
		t.Errorf("%s", blocked.stdout)
	}
	off := cli(t, args("telemetry", "off"), with, nil)
	if s, _ := telemetry.Load(dir); s.Enabled || s.ID != "" || !strings.Contains(off.stdout, "Telemetry is off.") {
		t.Fatalf("%+v %s", s, off.stdout)
	}
	if out := cli(t, args("telemetry", "maybe"), with, nil); out.code != ExitUsage {
		t.Errorf("%d", out.code)
	}
	if out := cli(t, args("telemetry", "on"), nil, nil); out.code != ExitError || !strings.Contains(out.stderr, "stays off") {
		t.Errorf("%d %s", out.code, out.stderr)
	}
	if strings.Contains(strings.Join(c.sent(), ""), "telemetry") {
		t.Error("the telemetry command reported itself")
	}
}

// A pricing service can price a private name. That makes it runnable, not
// public: the event still calls it other.
func TestAModelOnlyAPricingServicePricesIsNotNamed(t *testing.T) {
	c := newCollector(t)
	dir := t.TempDir()
	if _, err := telemetry.Answer(dir, true, now()); err != nil {
		t.Fatal(err)
	}
	var asked atomic.Int32
	api := pricingAPI(t, &asked)
	file := writeTraces(t, 3, nil)
	out := cli(t, args("estimate", file, "--models=examplelab/model-9"), c.reporting(dir, func(o *IO) {
		o.PricingURL, o.CacheDir = api.URL, t.TempDir()
	}), nil)
	got := c.sent()
	if out.code != ExitOK || asked.Load() != 1 || len(got) != 1 {
		t.Fatalf("exit %d, %d lookups, %d events: %s", out.code, asked.Load(), len(got), out.stderr)
	}
	if strings.Contains(got[0], "examplelab") || !strings.Contains(got[0], `"models":["other"]`) {
		t.Errorf("the event names a model the bundled catalog does not know: %s", got[0])
	}
}

// An opt-out that cannot be saved must not say telemetry is off.
func TestAFailedOptOutSaysTelemetryIsStillOn(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("needs a file the test cannot write")
	}
	c := newCollector(t)
	dir := t.TempDir()
	if _, err := telemetry.Answer(dir, true, now()); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, telemetry.File)
	if err := os.Chmod(file, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(file, 0o600) })
	out := cli(t, args("telemetry", "off"), c.reporting(dir, nil), nil)
	if out.code == ExitOK || !strings.Contains(out.stderr, "still on") || !strings.Contains(out.stderr, "EZSWAP_TELEMETRY=0") {
		t.Errorf("%d %s", out.code, out.stderr)
	}
}
