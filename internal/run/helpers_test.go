package run

// Shared test helpers for the runner: synthetic trace files and fake
// providers.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/metergraph/ezswap/internal/types"
)

type traceOptions struct {
	// Nil: "trace-<n>".
	traceID *string
	// A null trace id.
	noTraceID bool
	// Nil: a minute past midnight UTC, 1 October 2026, per call.
	ts        *string
	truncated bool
	search    bool
	model     string
}

func traceLine(t *testing.T, n int, options traceOptions) string {
	t.Helper()
	model := options.model
	if model == "" {
		model = "gpt-4.1-mini"
	}
	request := map[string]any{
		"model":           model,
		"system":          "Label the ticket for example.com as JSON.",
		"messages":        []any{map[string]any{"role": "user", "parts": []any{map[string]any{"type": "text", "content": fmt.Sprintf("Ticket %d: the dashboard is slow.", n)}}}},
		"response_format": map[string]any{"type": "json_object"},
		"params":          map[string]any{"temperature": 0.2, "max_tokens": 200},
	}
	if options.search {
		request["web_search_options"] = map[string]any{}
	}
	var traceID any = fmt.Sprintf("trace-%d", n)
	if options.traceID != nil {
		traceID = *options.traceID
	}
	if options.noTraceID {
		traceID = nil
	}
	var ts any = time.Date(2026, 10, 1, 0, n, 0, 0, time.UTC).Format("2006-01-02T15:04:05.000Z")
	if options.ts != nil {
		ts = *options.ts
	}
	text, _ := json.Marshal(map[string]any{"category": "bug", "n": n})
	line, err := json.Marshal(map[string]any{
		"schema":   "metergraph.replay-trace/v1",
		"id":       fmt.Sprintf("call-%d", n),
		"trace_id": traceID,
		"provider": "openai",
		"request":  request,
		"response": map[string]any{
			"text":          string(text),
			"finish_reason": "stop",
			"usage":         map[string]any{"input_tokens": 40, "output_tokens": 12},
			"latency_ms":    800,
		},
		"meta": map[string]any{"ts": ts, "truncated": options.truncated, "missing": []any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(line)
}

// writeTraces writes a trace file of count calls; options(n) varies each one.
func writeTraces(t *testing.T, dir string, count int, options func(n int) traceOptions) string {
	t.Helper()
	if options == nil {
		options = func(int) traceOptions { return traceOptions{} }
	}
	path := filepath.Join(dir, "traces.jsonl")
	lines := make([]string, 0, count)
	for n := 1; n <= count; n++ {
		lines = append(lines, traceLine(t, n, options(n)))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	return path
}

func num(v float64) *float64 { return &v }
func str(v string) *string   { return &v }

type outcomeOverrides struct {
	reportedCostUSD *float64
	noUsage         bool
}

func okOutcome(overrides outcomeOverrides) types.ReplyOutcome {
	outcome := types.ReplyOutcome{
		Status:       "ok",
		Output:       str(`{"category":"bug"}`),
		FinishReason: str("stop"),
		Usage:        &types.Usage{InputTokens: num(50), OutputTokens: num(10), CachedInputTokens: num(0)},
		LatencyMs:    num(100),
		Flags:        []types.ReplayFlag{},
	}
	if overrides.reportedCostUSD != nil {
		outcome.ReportedCostUSD = overrides.reportedCostUSD
	}
	if overrides.noUsage {
		outcome.Usage = nil
	}
	return outcome
}

func failedOutcome(maybeBilled bool) types.ReplyOutcome {
	flags := []types.ReplayFlag{}
	code := "provider_refused"
	if maybeBilled {
		flags = []types.ReplayFlag{types.FlagMaybeBilled}
		code = "provider_timeout"
	}
	return types.ReplyOutcome{
		Status: "failed",
		Flags:  flags,
		Error:  &types.ReplayError{Code: code, Message: "fake failure"},
	}
}

// fakeProvider records every call and answers with respond. Safe for
// concurrent calls, as a real provider is.
type fakeProvider struct {
	mu      sync.Mutex
	calls   []types.ReplayCall
	respond func(call types.ReplayCall, index int) types.ReplyOutcome
}

func newFakeProvider(respond func(call types.ReplayCall, index int) types.ReplyOutcome) *fakeProvider {
	if respond == nil {
		respond = func(types.ReplayCall, int) types.ReplyOutcome { return okOutcome(outcomeOverrides{}) }
	}
	return &fakeProvider{respond: respond}
}

func (p *fakeProvider) Name() string { return "fake" }

func (p *fakeProvider) Complete(_ context.Context, call types.ReplayCall) types.ReplyOutcome {
	p.mu.Lock()
	p.calls = append(p.calls, call)
	index := len(p.calls) - 1
	p.mu.Unlock()
	// Let other work run before answering.
	runtime.Gosched()
	return p.respond(call, index)
}

func (p *fakeProvider) Calls() []types.ReplayCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]types.ReplayCall{}, p.calls...)
}
