package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/metergraph/replay/internal/judge"
	"github.com/metergraph/replay/internal/types"
)

const gatewayKey = "vck-test-not-a-real-key-5151"

// fakeGatewayJudge says two replies differ when their text differs.
func fakeGatewayJudge(t *testing.T, status int, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "Bearer "+gatewayKey {
			t.Errorf("auth header %q", r.Header.Get("Authorization"))
		}
		if strings.Contains(string(body), key) {
			t.Error("the provider key went to the judge")
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		var request struct {
			Input string `json:"input"`
		}
		json.Unmarshal(body, &request)
		if !strings.Contains(request.Input, "Reply customers") || !strings.Contains(request.Input, "user: Ticket") {
			t.Errorf("the judge was not given the request: %s", request.Input)
		}
		one := request.Input[strings.Index(request.Input, "Reply 1:\n")+9 : strings.Index(request.Input, "\n\nReply 2:")]
		two := request.Input[strings.Index(request.Input, "Reply 2:\n")+9:]
		p := "0.1"
		if one != two {
			p = "0.9"
		}
		io.WriteString(w, `{"answers":[{"type":"choice","name":"same","probabilities":[{"value":"L1","probability":0.5},{"value":"L2","probability":`+p+`}]}],"provider_metadata":{"gateway":{"cost":"0.00002"}}}`)
	}))
	t.Cleanup(server.Close)
	return server
}

// freeTextTraces is one part of 12 calls whose recorded answers are prose.
func freeTextTraces(t *testing.T) string {
	return writePartTraces(t, partSpec{name: "reply", system: "Reply customers in plain text.", count: 12, extra: map[string]any{
		"response": map[string]any{"text": "Your order ships tomorrow.", "finish_reason": "stop", "usage": map[string]any{"input_tokens": 40, "output_tokens": 12}, "latency_ms": 800},
	}})
}

func withJudge(server *httptest.Server) func(*IO) {
	return func(o *IO) {
		o.Env[judge.KeyEnv] = gatewayKey
		o.JudgeURL = server.URL
	}
}

func TestMapJudgesFreeTextParts(t *testing.T) {
	var calls atomic.Int32
	server := fakeGatewayJudge(t, http.StatusOK, &calls)
	file := freeTextTraces(t)
	out := t.TempDir()
	result := cli(t, args("map", file, models, "--yes", "--out", out, "--judge"), withJudge(server), nil)
	if result.code != ExitOK {
		t.Fatalf("%d %s\n%s", result.code, result.stderr, result.stdout)
	}
	// 12 cases: the treatment and the first rerun against the recording, the
	// second rerun against the first; each asked in both orders.
	if calls.Load() != 72 {
		t.Fatalf("%d judge calls", calls.Load())
	}
	match(t, `Judging 36 pairs of free-text replies with typesafe-ai/jev, each in both orders\.`, result.stdout)
	match(t, `Judged 36 of 36 pairs`, result.stdout)
	// Every reply ({"category":"bug"}) differs from the prose recording, and
	// the two reruns agree.
	match(t, `reply +within noise +12 of 12 +12 of 12`, result.stdout)
	match(t, `JUDGED: reply is free text, compared by typesafe-ai/jev\.`, result.stdout)

	runPath := filepath.Join(out, "run.json")
	text, _ := os.ReadFile(runPath)
	if strings.Contains(string(text), gatewayKey) || strings.Contains(result.stdout, gatewayKey) {
		t.Fatal("the gateway key leaked")
	}
	var r types.RunFile
	json.Unmarshal(text, &r)
	if r.Judge == nil || r.Judge.Model != "typesafe-ai/jev" || len(r.Judge.Judgements) != 36 || r.Judge.CostUSD < 0.00143 {
		t.Fatalf("judge record %+v", r.Judge)
	}

	// Printing it again, or judging again, asks the judge nothing.
	again := cli(t, args("report", runPath), nil, nil)
	match(t, `reply +within noise +12 of 12`, again.stdout)
	rejudged := cli(t, args("judge", runPath), withJudge(server), nil)
	if rejudged.code != ExitOK || calls.Load() != 72 {
		t.Fatalf("%d, %d calls\n%s", rejudged.code, calls.Load(), rejudged.stderr)
	}
	match(t, `Nothing to judge`, rejudged.stdout)
}

func TestJudgeCommandJudgesAnExistingMap(t *testing.T) {
	var calls atomic.Int32
	server := fakeGatewayJudge(t, http.StatusOK, &calls)
	file := freeTextTraces(t)
	out := t.TempDir()
	if made := cli(t, args("map", file, models, "--yes", "--out", out), nil, nil); made.code != ExitOK || calls.Load() != 0 {
		t.Fatalf("%d, %d judge calls without --judge", made.code, calls.Load())
	}
	runPath := filepath.Join(out, "run.json")
	before := cli(t, args("report", runPath), nil, nil)
	match(t, `reply +not comparable +free text`, before.stdout)

	result := cli(t, args("judge", runPath, "--json"), withJudge(server), nil)
	if result.code != ExitOK || calls.Load() != 72 {
		t.Fatalf("%d, %d calls\n%s", result.code, calls.Load(), result.stderr)
	}
	doc := decode(t, result.stdout)
	if j := doc["judge"].(map[string]any); j["pairs"] != float64(36) || j["failed"] != float64(0) {
		t.Fatalf("%v", doc)
	}
	after := cli(t, args("report", runPath), nil, nil)
	match(t, `reply +within noise +12 of 12`, after.stdout)
}

func TestJudgeRefusals(t *testing.T) {
	var calls atomic.Int32
	server := fakeGatewayJudge(t, http.StatusOK, &calls)
	file := freeTextTraces(t)
	// --judge is for map only.
	if out := cli(t, args("run", file, models, "--yes", "--judge"), withJudge(server), nil); out.code != ExitUsage || !strings.Contains(out.stderr, "--judge is for map") {
		t.Errorf("run --judge: %d %s", out.code, out.stderr)
	}
	// Without the key, map refuses before spending anything.
	out := cli(t, args("map", file, models, "--yes", "--judge"), func(o *IO) { o.JudgeURL = server.URL }, nil)
	if out.code != ExitRefused || out.fetches != 0 || !strings.Contains(out.stderr, "AI_GATEWAY_API_KEY") {
		t.Errorf("no key: %d, %d fetches, %s", out.code, out.fetches, out.stderr)
	}
	// judge only reads map runs.
	dir := t.TempDir()
	cli(t, args("demo", "--out", dir), nil, nil)
	if out := cli(t, args("judge", filepath.Join(dir, "run.json")), withJudge(server), nil); out.code != ExitUsage || !strings.Contains(out.stderr, "not made by map") {
		t.Errorf("plain run: %d %s", out.code, out.stderr)
	}
	if calls.Load() != 0 {
		t.Errorf("%d judge calls", calls.Load())
	}
}

func TestJudgeFailureKeepsTheRun(t *testing.T) {
	var calls atomic.Int32
	server := fakeGatewayJudge(t, http.StatusUnauthorized, &calls)
	file := freeTextTraces(t)
	out := t.TempDir()
	result := cli(t, args("map", file, models, "--yes", "--out", out, "--judge"), withJudge(server), nil)
	if result.code != ExitOK {
		t.Fatalf("%d %s", result.code, result.stderr)
	}
	match(t, `The judge could not answer for 36 \(the judge answered HTTP 401\); their cases are left out`, result.stdout)
	match(t, `reply +too few calls +0 paired`, result.stdout)
	if _, err := os.Stat(filepath.Join(out, "run.json")); err != nil {
		t.Fatal("the run was not written")
	}
}
