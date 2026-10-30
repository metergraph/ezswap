package demo

import (
	"bytes"
	"testing"
)

func TestRunDecodesTheCommittedDemo(t *testing.T) {
	run, err := Run()
	if err != nil {
		t.Fatal(err)
	}
	if run.Schema != "metergraph.replay-run/v1" || len(run.Cases) != 22 || len(run.Replays) != 80 || run.Stopped != nil {
		t.Fatalf("unexpected demo run: %s, %d cases, %d replays", run.Schema, len(run.Cases), len(run.Replays))
	}
	if run.Settings.MonthlyCalls == nil || *run.Settings.MonthlyCalls != 250_000 {
		t.Fatal("the demo's assumed monthly volume is missing")
	}
	// Each call decodes afresh.
	again, _ := Run()
	again.Cases = nil
	if len(run.Cases) != 22 {
		t.Fatal("Run shares its result between calls")
	}
	if n := bytes.Count(TracesJSONL, []byte("\n")); n != 22 {
		t.Fatalf("traces.jsonl has %d lines", n)
	}
}
