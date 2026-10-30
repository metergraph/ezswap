package main

import (
	"bytes"
	"os"
	"testing"
)

// testdata/edge-cases.json is what the original JavaScript build script
// (the npm yaml package and JSON.stringify) wrote for edge-cases.yaml, kept
// as frozen test data: merge keys over a sequence and an alias, explicit keys
// overriding merged ones, array-index keys, hex, octal, exponent and
// infinite numbers, escapes, a falsy region, a flattened price_region and a
// null publisher.
func TestRenderMatchesTheJavaScriptBuild(t *testing.T) {
	yaml, err := os.ReadFile("testdata/edge-cases.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/edge-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	got, n, err := render(yaml, "0000000000000000000000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("models = %d, want 2", n)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("render differs from testdata/edge-cases.json:\n%s", got)
	}
}
