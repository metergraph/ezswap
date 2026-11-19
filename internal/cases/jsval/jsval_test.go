package jsval

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestStringifyKeepsSourceOrderWithIndexKeysFirst(t *testing.T) {
	value, err := Parse(`{"b":1,"2":2,"1":3,"4294967295":4,"4294967294":5," ":"<&>\u0001\"\\"}`)
	if err != nil {
		t.Fatal(err)
	}
	// What JSON.stringify(JSON.parse(...)) prints in Node.
	want := "{\"1\":3,\"2\":2,\"4294967294\":5,\"b\":1,\"4294967295\":4,\" \":\"<&>\\u0001\\\"\\\\\"}"
	if got := Stringify(value); got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestParseRepeatedKeyKeepsFirstPositionAndLastValue(t *testing.T) {
	value, _ := Parse(`{"a":1,"b":2,"a":3}`)
	if got := Stringify(value); got != `{"a":3,"b":2}` {
		t.Errorf("got %s", got)
	}
}

func TestParseRejectsTrailingData(t *testing.T) {
	for _, text := range []string{`{} x`, `{}{}`, `1 2`, ``, `   `} {
		if _, err := Parse(text); err == nil {
			t.Errorf("Parse(%q) should fail", text)
		}
	}
	if v, err := Parse("1e400"); err != nil || !math.IsInf(v.(float64), 1) {
		t.Errorf("1e400 reads as Infinity: %v %v", v, err)
	}
	if got := Stringify([]any{math.Inf(1), 1e21, 1e-7, math.Copysign(0, -1), 0.1}); got != "[null,1e+21,1e-7,0,0.1]" {
		t.Errorf("numbers: %s", got)
	}
}

func TestToString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{[]any{"As", "sistant"}, "As,sistant"},
		{[]any{nil, []any{1.0, []any{2.0}}, map[string]any{}}, ",1,2,[object Object]"},
		{nil, "null"},
		{Undefined, "undefined"},
		{true, "true"},
		{1e21, "1e+21"},
	}
	for _, tc := range cases {
		if got := ToString(tc.in); got != tc.want {
			t.Errorf("ToString(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStringToNumber(t *testing.T) {
	cases := map[string]float64{" 0x1A ": 26, "1e3": 1000, "+.5": 0.5, "": 0, "5.": 5, "0o17": 15, "0b11": 3}
	for in, want := range cases {
		if got := StringToNumber(in); got != want {
			t.Errorf("Number(%q) = %v, want %v", in, got, want)
		}
	}
	for _, in := range []string{"-0b11", "1_0", "inf", "0x", "1e", "abc", "NaN"} {
		if got := StringToNumber(in); !math.IsNaN(got) {
			t.Errorf("Number(%q) = %v, want NaN", in, got)
		}
	}
	if !math.IsInf(StringToNumber("-Infinity"), -1) {
		t.Error("Number(-Infinity)")
	}
}

func TestStringHelpers(t *testing.T) {
	if got := Trim("\ufeff  a b 　"); got != "a b" {
		t.Errorf("Trim: %q", got)
	}
	if got := Trim("\u0085a"); got != "\u0085a" {
		t.Errorf("U+0085 is not JavaScript white space: %q", got)
	}
	if got := ToLower("ASSİSTANT"); got != "assi̇stant" {
		t.Errorf("ToLower: %q", got)
	}
	if got := UTF16Len("a😀"); got != 3 {
		t.Errorf("UTF16Len: %d", got)
	}
	if got := SliceUTF16("ab😀c", 3); got != "ab�" {
		t.Errorf("SliceUTF16 through a pair: %q", got)
	}
	if got := SliceUTF16("ab😀c", 4); got != "ab😀" {
		t.Errorf("SliceUTF16: %q", got)
	}
}

func TestDecodeUTF8ReplacesMaximalSubparts(t *testing.T) {
	// Node: Buffer.from([0x61,0xE2,0x82,0x62,0xFF,0xF0,0x9F,0x98]).toString() is "a�b��".
	if got := DecodeUTF8([]byte{0x61, 0xE2, 0x82, 0x62, 0xFF, 0xF0, 0x9F, 0x98}); got != "a�b��" {
		t.Errorf("got %q", got)
	}
}

func TestMarshalJSONIsStringify(t *testing.T) {
	value, _ := Parse(`{"z":{"b":1e21,"a":[{"y":0.1,"x":"<&>"}]},"a":-0,"3":null}`)
	object := value.(*Object)
	object.Set("u", Undefined)
	want := `{"3":null,"z":{"b":1e+21,"a":[{"y":0.1,"x":"<&>"}]},"a":0}`
	data, err := object.MarshalJSON()
	if err != nil || string(data) != want {
		t.Errorf("MarshalJSON = %s, %v; want %s", data, err, want)
	}
	// Nested in plain values and marshalled by encoding/json without HTML escaping.
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]any{"v": []any{object, Undefined}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(b.String()); got != `{"v":[`+want+`,null]}` {
		t.Errorf("encoding/json: %s", got)
	}
}
