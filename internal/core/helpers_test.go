package core

// Shared test helpers: fixtures read with JavaScript's JSON.parse rules, and
// a deep comparison of JSON forms (a value compared as
// `JSON.parse(JSON.stringify(x))` would be).

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/metergraph/ezswap/internal/types"
)

// fixture reads conformance/<name> as loose JSON.
func fixture(t *testing.T, name string) any {
	t.Helper()
	data, err := os.ReadFile("../../conformance/" + name)
	if err != nil {
		t.Fatal(err)
	}
	v, err := ParseJSON(string(data))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return v
}

// plain is a value's JSON form as encoding/json decodes it: map[string]any,
// []any, float64, string, bool, nil.
func plain(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// firstDifference names where two JSON forms first differ, or "".
func firstDifference(path string, a, b any) string {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: got %s, want %s", path, short(a), short(b))
		}
		keys := map[string]bool{}
		for k := range x {
			keys[k] = true
		}
		for k := range y {
			keys[k] = true
		}
		names := make([]string, 0, len(keys))
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			xv, xok := x[k]
			yv, yok := y[k]
			if xok != yok {
				return fmt.Sprintf("%s.%s: present %v, want present %v (got %s, want %s)", path, k, xok, yok, short(xv), short(yv))
			}
			if d := firstDifference(path+"."+k, xv, yv); d != "" {
				return d
			}
		}
		return ""
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return fmt.Sprintf("%s: got %s, want %s", path, short(a), short(b))
		}
		for i := range x {
			if d := firstDifference(fmt.Sprintf("%s[%d]", path, i), x[i], y[i]); d != "" {
				return d
			}
		}
		return ""
	}
	if !reflect.DeepEqual(a, b) {
		return fmt.Sprintf("%s: got %s, want %s", path, short(a), short(b))
	}
	return ""
}

func short(v any) string {
	data, _ := json.Marshal(v)
	s := string(data)
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// sameJSON fails the test when got's and want's JSON forms differ.
func sameJSON(t *testing.T, got, want any, what string) {
	t.Helper()
	if d := firstDifference("", plain(t, got), plain(t, want)); d != "" {
		t.Errorf("%s: %s", what, d)
	}
}

// loadRun is the synthetic run conformance/page.json was generated from.
func loadRun(t *testing.T) *types.RunFile {
	t.Helper()
	data, err := os.ReadFile("../../conformance/page.json")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Run types.RunFile `json:"run"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatal(err)
	}
	return &page.Run
}

func mustBuild(t *testing.T, run *types.RunFile, options BuildViewOptions) *ExperimentView {
	t.Helper()
	view, err := BuildView(run, options)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func str(v any) string { s, _ := v.(string); return s }
