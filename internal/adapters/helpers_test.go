package adapters

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/metergraph/ezswap/internal/cases"
	"github.com/metergraph/ezswap/internal/cases/jsval"
	"github.com/metergraph/ezswap/internal/types"
)

func fixture(name string) string {
	return filepath.Join("testdata", name)
}

func read(t *testing.T, path string, options ...ReadTracesOptions) []types.CapturedCall {
	t.Helper()
	var opts ReadTracesOptions
	if len(options) > 0 {
		opts = options[0]
	}
	calls, err := ReadTraces(path, opts)
	if err != nil {
		t.Fatalf("ReadTraces(%s): %v", path, err)
	}
	return calls
}

func prepare(c types.CapturedCall) types.Case {
	return cases.PrepareCase(c, cases.PrepareCaseOptions{})
}

func j(t *testing.T, text string) any {
	t.Helper()
	value, err := jsval.Parse(text)
	if err != nil {
		t.Fatalf("bad JSON literal %s: %v", text, err)
	}
	return value
}

func equal(t *testing.T, got, want any, label ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", strings.Join(label, " "), got, want)
	}
}

func str(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func num(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func reason(k types.Case) string {
	if k.NotReplayable == nil {
		return ""
	}
	return string(*k.NotReplayable)
}

func usage(u *types.Usage) any {
	if u == nil {
		return nil
	}
	return map[string]any{
		"inputTokens":       num(u.InputTokens),
		"outputTokens":      num(u.OutputTokens),
		"cachedInputTokens": num(u.CachedInputTokens),
		"reasoningTokens":   num(u.ReasoningTokens),
	}
}

func usageOf(input, output, cached, reasoning any) map[string]any {
	return map[string]any{"inputTokens": input, "outputTokens": output, "cachedInputTokens": cached, "reasoningTokens": reasoning}
}

func params(k types.Case) map[string]any {
	var stop any
	if k.Params.Stop != nil {
		stop = k.Params.Stop
	}
	return map[string]any{
		"temperature":     num(k.Params.Temperature),
		"maxTokens":       num(k.Params.MaxTokens),
		"topP":            num(k.Params.TopP),
		"stop":            stop,
		"reasoningEffort": str(k.Params.ReasoningEffort),
	}
}

func ids(calls []types.CapturedCall) []string {
	out := []string{}
	for _, c := range calls {
		out = append(out, c.ID)
	}
	return out
}

func detected(record any) any {
	a := DetectAdapter(record)
	if a == nil {
		return nil
	}
	return a.Name()
}

func temp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
