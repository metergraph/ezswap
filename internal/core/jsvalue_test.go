package core

// The JavaScript semantics core emulates, checked directly: the fixtures
// only reach them through the statistics.

import (
	"math"
	"testing"
)

func TestParseJSONKeepsJavaScriptKeyOrder(t *testing.T) {
	v, err := ParseJSON(`{"b":1,"2":2,"a":3,"1":4,"b":5,"01":6}`)
	if err != nil {
		t.Fatal(err)
	}
	o := v.(*Object)
	// Index keys first, ascending; then insertion order; a repeated key keeps its place and takes the last value.
	if got := Stringify(o, ""); got != `{"1":4,"2":2,"b":5,"a":3,"01":6}` {
		t.Errorf("order = %s", got)
	}
}

func TestParseJSONRejectsWhatJSONParseRejects(t *testing.T) {
	for _, text := range []string{"NaN", "Infinity", "01", "1.", ".5", "[1,]", `{"a":1,}`, "'a'", "\"a\tb\"", "\u00a01", "-"} {
		if _, err := ParseJSON(text); err == nil {
			t.Errorf("%q parsed", text)
		}
	}
	v, err := ParseJSON("1e400")
	if err != nil || !math.IsInf(v.(float64), 1) {
		t.Errorf("1e400 = %v, %v", v, err)
	}
	v, _ = ParseJSON(`"🙂 \ud83d"`)
	if v != "🙂 \ufffd" {
		t.Errorf("surrogates = %q", v)
	}
}

func TestStringifyMatchesJSONStringify(t *testing.T) {
	v, _ := ParseJSON(`{"a":[1,2.5,{"b":null}],"c":{},"d":[],"e":"x\u0001\n\u2028"}`)
	want := "{\n  \"a\": [\n    1,\n    2.5,\n    {\n      \"b\": null\n    }\n  ],\n  \"c\": {},\n  \"d\": [],\n  \"e\": \"x\\u0001\\n\u2028\"\n}"
	if got := Stringify(v, "  "); got != want {
		t.Errorf("got %s", got)
	}
	if got := Stringify([]any{math.NaN(), math.Inf(1), 1e21, 1e-7}, ""); got != "[null,null,1e+21,1e-7]" {
		t.Errorf("numbers = %s", got)
	}
}

func TestJSStringAndTrim(t *testing.T) {
	v, _ := ParseJSON(`[1,[2,null],{"a":1},true,null]`)
	if got := jsString(v); got != "1,2,,[object Object],true," {
		t.Errorf("String(array) = %q", got)
	}
	if got := jsTrim("\ufeff\u00a0 x \u3000"); got != "x" {
		t.Errorf("trim = %q", got)
	}
	if got := pyStrip("\ufeff x \u0085\u001c"); got != "\ufeff x" {
		t.Errorf("strip = %q", got)
	}
}

func TestLocaleString(t *testing.T) {
	cases := map[float64]string{0: "0", 1024: "1,024", 1234567: "1,234,567", -1234.5: "-1,234.5", 0.12345: "0.123", 1.0005: "1.001", 999.9996: "1,000", 1e21: "1,000,000,000,000,000,000,000"}
	for x, want := range cases {
		if got := localeString(x); got != want {
			t.Errorf("localeString(%v) = %q, want %q", x, got, want)
		}
	}
}

func TestUTF16Lengths(t *testing.T) {
	if utf16Len("a🙂") != 3 || codePointLength("a🙂") != 2 {
		t.Error("lengths")
	}
	if got := utf16Slice("🙂🙂", 3); got != "🙂\ufffd" {
		t.Errorf("slice = %q", got)
	}
}

func TestPathReaders(t *testing.T) {
	// Python's \w after the first character takes any letter; the first must be ASCII.
	tokens, err := ParseJSONPath(`$.a.b[0]["odd key"]['x'][-1].xé1`)
	if err != nil || len(tokens) != 7 || tokens[3].Key != "odd key" || tokens[4].Key != "x" || tokens[6].Key != "xé1" || !tokens[5].IsIndex || tokens[5].Index != -1 {
		t.Errorf("tokens = %+v, %v", tokens, err)
	}
	for _, bad := range []string{"$..deep", "$[*]", "$", "", "$.é1", `$["a]`} {
		if _, err := ParseJSONPath(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	v, _ := ParseJSON(`{"items":[{"id":1},{"id":2},{"other":3}],"a":{"b":null}}`)
	got, ok := ValueAt(v, "$.items[].id")
	if !ok || Stringify(got, "") != "[1,2]" {
		t.Errorf("items[].id = %v", got)
	}
	if got, ok := ValueAt(v, "a.b"); !ok || got != nil {
		t.Errorf("a.b = %v, %v", got, ok)
	}
	if _, ok := ValueAt(v, "$.a.c"); ok {
		t.Error("$.a.c found")
	}
	if paths := JSONPaths(v, "$"); Stringify(paths, "") != `["$.items[].id","$.items[].other","$.a.b"]` {
		t.Errorf("paths = %v", paths)
	}
}

func TestSeededMatchesMulberry32(t *testing.T) {
	// mulberry32(1)'s first draws, as JavaScript computes them.
	random := Seeded(1)
	want := []float64{0.6270739405881613, 0.002735721180215478, 0.5274470399599522}
	for i, w := range want {
		if got := random(); got != w {
			t.Errorf("draw %d = %v, want %v", i, got, w)
		}
	}
}

func TestFormatUSDBelowOneCent(t *testing.T) {
	if got := FormatUSD(ptr(0.000123456)); got != "$0.00012" {
		t.Errorf("got %s", got)
	}
	if got := FormatUSD((*float64)(nil)); got != MissingValue {
		t.Errorf("nil = %s", got)
	}
}
