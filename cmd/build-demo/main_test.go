package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/metergraph/replay/demo"
	"github.com/metergraph/replay/internal/types"
)

// The committed demo is what this command builds: the same trace file byte
// for byte, and a run.json that decodes to the same run. (The bytes of
// run.json differ only in the estimate's perArm key order: a Go map writes
// its keys sorted.)
func TestBuildReproducesTheCommittedDemo(t *testing.T) {
	dir := t.TempDir()
	if _, err := build(dir); err != nil {
		t.Fatal(err)
	}
	traces, err := os.ReadFile(filepath.Join(dir, "traces.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(traces, demo.TracesJSONL) {
		t.Fatal("traces.jsonl differs from demo/traces.jsonl")
	}
	text, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var built types.RunFile
	if err := json.Unmarshal(text, &built); err != nil {
		t.Fatal(err)
	}
	committed, err := demo.Run()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&built, committed) {
		t.Fatal("run.json does not decode to the committed demo run")
	}
	var a, b any
	_ = json.Unmarshal(text, &a)
	_ = json.Unmarshal(demo.RunJSON, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("run.json is not the same JSON as demo/run.json")
	}
}

// The committed map demo is what this command builds, in the same way.
func TestBuildReproducesTheCommittedMapDemo(t *testing.T) {
	dir := t.TempDir()
	if _, err := buildMap(dir); err != nil {
		t.Fatal(err)
	}
	traces, err := os.ReadFile(filepath.Join(dir, "map-traces.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(traces, demo.MapTracesJSONL) {
		t.Fatal("map-traces.jsonl differs from demo/map-traces.jsonl")
	}
	text, err := os.ReadFile(filepath.Join(dir, "map-run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var built types.RunFile
	if err := json.Unmarshal(text, &built); err != nil {
		t.Fatal(err)
	}
	committed, err := demo.MapRun()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&built, committed) {
		t.Fatal("map-run.json does not decode to the committed map run")
	}
	names := map[string]bool{}
	for _, kase := range committed.Cases {
		names[kase.PartName] = true
	}
	for _, name := range []string{"extract-order", "route-to-team", "classify-ticket", "summarise-thread", "draft-reply"} {
		if !names[name] {
			t.Fatalf("the map demo has no part %q", name)
		}
	}
}
