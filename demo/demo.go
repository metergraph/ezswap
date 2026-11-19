// Package demo is the bundled demo: synthetic support-ticket traces and a
// recorded run over them, made by cmd/build-demo with a deterministic fake
// provider, so `metergraph-replay demo` needs no keys and no network.
//
// Every ticket, name and address is invented; addresses use example.com.
package demo

import (
	_ "embed"
	"encoding/json"

	"github.com/metergraph/replay/internal/types"
)

// RunJSON is demo/run.json as committed.
//
//go:embed run.json
var RunJSON []byte

// TracesJSONL is demo/traces.jsonl as committed: the trace file the run
// replayed.
//
//go:embed traces.jsonl
var TracesJSONL []byte

// Run is the recorded run, decoded afresh on every call so a caller may
// change it (the demo command sets display options on it, say).
func Run() (*types.RunFile, error) {
	var run types.RunFile
	if err := json.Unmarshal(RunJSON, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// MapRunJSON is demo/map-run.json as committed: a map run over a made-up
// support desk with five parts.
//
//go:embed map-run.json
var MapRunJSON []byte

// MapTracesJSONL is demo/map-traces.jsonl as committed: the trace file that
// run replayed.
//
//go:embed map-traces.jsonl
var MapTracesJSONL []byte

// MapRun is the recorded map run, decoded afresh on every call.
func MapRun() (*types.RunFile, error) {
	var run types.RunFile
	if err := json.Unmarshal(MapRunJSON, &run); err != nil {
		return nil, err
	}
	return &run, nil
}
