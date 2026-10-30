package core

// The fixtures in conformance/ pin core to Metergraph's hosted figures. A
// failure here means core moved a number, never that the fixture needs
// editing.

import (
	"testing"
)

func TestEquivalenceWithoutNoiseFloor(t *testing.T) {
	f := fixture(t, "experiment_equivalence.json")
	sameJSON(t, Equivalence(at(f, "view"), ""), at(f, "equivalence"), "equivalence")
}

func TestEquivalenceWithNoiseFloorAndSimilarity(t *testing.T) {
	f := fixture(t, "experiment_equivalence_floor.json")
	sameJSON(t, Equivalence(at(f, "view"), ""), at(f, "equivalence"), "equivalence")
}

func TestFormattersMatchHostedFigures(t *testing.T) {
	f := fixture(t, "experiment_equivalence.json")
	formats := map[string]func(any) any{
		"usd":         func(v any) any { return FormatUSD(v) },
		"ms":          func(v any) any { return FormatMs(v) },
		"signed":      func(v any) any { return FormatSigned(v, nil) },
		"percent":     func(v any) any { return FormatPercent(v) },
		"count":       func(v any) any { return FormatCount(v) },
		"sample_note": func(v any) any { return toLoose(SampleNote(v)) },
	}
	for name, format := range formats {
		cases := list(at(f, "formats", name))
		if len(cases) == 0 {
			t.Fatalf("no %s cases", name)
		}
		for _, c := range cases {
			pair := list(c)
			if got := format(pair[0]); got != pair[1] {
				t.Errorf("%s(%v) = %#v, want %#v", name, pair[0], got, pair[1])
			}
		}
	}
}

func TestParseOutputJSON(t *testing.T) {
	summary := fixture(t, "summarize.json")
	for _, c := range list(at(summary, "parse_output_json")) {
		output, _ := get(c, "output")
		value, ok := ParseOutputJSON(output)
		if ok != at(c, "ok") {
			t.Errorf("%s: ok = %v", Stringify(output, ""), ok)
		}
		if !ok {
			value = nil
		}
		sameJSON(t, value, at(c, "value"), Stringify(output, ""))
	}
}

func TestIsCutOffAndIsEmptyOrRefused(t *testing.T) {
	summary := fixture(t, "summarize.json")
	for _, c := range list(at(summary, "is_cut_off")) {
		if got := IsCutOff(at(c, "finish_reason")); got != at(c, "expected") {
			t.Errorf("isCutOff(%s) = %v", Stringify(at(c, "finish_reason"), ""), got)
		}
	}
	for _, c := range list(at(summary, "is_empty_or_refused")) {
		if got := IsEmptyOrRefused(at(c, "output"), at(c, "finish_reason"), truthy(at(c, "refusal"))); got != at(c, "expected") {
			t.Errorf("isEmptyOrRefused(%s) = %v", Stringify(c, ""), got)
		}
	}
}

func TestCachedInput(t *testing.T) {
	summary := fixture(t, "summarize.json")
	for _, c := range list(at(summary, "cached_input")) {
		sameJSON(t, CachedInput(at(c, "outcome")), at(c, "expected"), Stringify(at(c, "outcome"), ""))
	}
}

func TestOutputFactsWithEveryBound(t *testing.T) {
	summary := fixture(t, "summarize.json")
	for _, c := range list(at(summary, "output_facts")) {
		var tracked []string
		for _, p := range list(at(c, "tracked_fields")) {
			tracked = append(tracked, str(p))
		}
		what := jsString(at(c, "output"))
		if len(what) > 60 {
			what = what[:60]
		}
		sameJSON(t, OutputFacts(at(c, "output"), tracked), at(c, "expected"), what)
	}
}

func TestSummarize(t *testing.T) {
	summary := fixture(t, "summarize.json")
	for _, s := range list(at(summary, "summarize")) {
		t.Run(str(at(s, "name")), func(t *testing.T) {
			controls, _ := get(s, "controls")
			sameJSON(t, Summarize(list(at(s, "outcomes")), list(controls)), at(s, "expected"), "summary")
		})
	}
}

func TestFieldKindUnderPythonSemantics(t *testing.T) {
	floor := fixture(t, "noise_floor.json")
	for _, c := range list(at(floor, "field_kind")) {
		values := list(at(c, "values"))
		if got := FieldKindPy(values); string(got) != at(c, "expected") {
			t.Errorf("fieldKindPy(%.80s) = %s, want %v", Stringify(values, ""), got, at(c, "expected"))
		}
	}
}

func TestItemOverlap(t *testing.T) {
	floor := fixture(t, "noise_floor.json")
	for _, c := range list(at(floor, "item_overlap")) {
		a, b := list(at(c, "a")), list(at(c, "b"))
		if got := ItemOverlap(a, b); got != at(c, "expected") {
			t.Errorf("itemOverlap(%s) = %v, want %v", Stringify([]any{a, b}, ""), got, at(c, "expected"))
		}
	}
}

func TestNoiseFloorView(t *testing.T) {
	floor := fixture(t, "noise_floor.json")
	for _, s := range list(at(floor, "noise_floor_view")) {
		t.Run(str(at(s, "name")), func(t *testing.T) {
			include, present := get(s, "include_cases")
			includeCases := !present || truthy(include)
			sameJSON(t, NoiseFloorView(at(s, "experiment"), includeCases), at(s, "expected"), "noise floor")
		})
	}
}
