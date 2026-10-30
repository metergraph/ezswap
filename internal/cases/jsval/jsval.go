// Package jsval reproduces the JavaScript value semantics the cases and
// adapters packages were written against: JSON.parse with object key order
// kept, JSON.stringify, truthiness, String(), and the string functions whose
// Go counterparts differ (trim, \s, toLowerCase, UTF-16 lengths, Number()).
//
// Decoded JSON is plain Go values (map[string]any, []any, string, float64,
// bool, nil) everywhere outside the adapters. Parse returns *Object for
// objects instead, so that JSON.stringify and Object.keys see the source's
// key order; the accessors here (IsObject, Get, Has, Keys) accept either
// representation, and Plain turns an ordered tree into plain values.
package jsval

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/metergraph/replay/internal/jsnum"
)

// undefinedType is the type of Undefined.
type undefinedType struct{}

// Undefined stands for a JavaScript undefined held as a value (an object
// property that exists but holds undefined). Plain turns it into nil;
// Stringify leaves it out of objects and writes null for it in arrays.
var Undefined any = undefinedType{}

// MarshalJSON writes undefined as null, as JSON.stringify does in an array.
func (undefinedType) MarshalJSON() ([]byte, error) { return []byte("null"), nil }

// Nullish is `value === null || value === undefined`: the values `??` skips.
func Nullish(value any) bool {
	return value == nil || value == Undefined
}

// ---------------------------------------------------------------------------
// Ordered objects

// Object is a JSON object that keeps its key order the way a JavaScript
// object does: insertion order, a reassigned key keeping its place.
type Object struct {
	keys   []string
	values map[string]any
}

// NewObject returns an empty ordered object.
func NewObject() *Object {
	return &Object{values: map[string]any{}}
}

// Set assigns a property, appending the key when it is new.
func (o *Object) Set(key string, value any) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

// Get returns a property and whether the object has it.
func (o *Object) Get(key string) (any, bool) {
	value, ok := o.values[key]
	return value, ok
}

// Len is the number of properties.
func (o *Object) Len() int { return len(o.keys) }

// Keys are Object.keys(o): array-index keys ascending, then the rest in
// insertion order.
func (o *Object) Keys() []string {
	return jsKeyOrder(o.keys)
}

// MarshalJSON writes the object as JSON.stringify does: source key order
// (array indices first), nested ordered values in order, numbers as
// JavaScript writes them and no HTML escaping. encoding/json re-escapes
// <, > and & in a Marshaler's output unless the Encoder has
// SetEscapeHTML(false); use Stringify, or such an Encoder, for exact text.
func (o *Object) MarshalJSON() ([]byte, error) {
	return []byte(Stringify(o)), nil
}

// Copy is `{...o}`: a shallow copy.
func (o *Object) Copy() *Object {
	out := &Object{keys: append([]string(nil), o.keys...), values: make(map[string]any, len(o.values))}
	for key, value := range o.values {
		out.values[key] = value
	}
	return out
}

// IsArrayIndex reports whether a property key is a canonical array index
// (0 to 2^32 - 2), which JavaScript orders before every other key.
func IsArrayIndex(key string) bool {
	if key == "" || len(key) > 10 {
		return false
	}
	if key != "0" && key[0] == '0' {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return false
		}
	}
	n, err := strconv.ParseUint(key, 10, 64)
	return err == nil && n <= 4294967294
}

// jsKeyOrder stably moves array-index keys to the front, ascending.
func jsKeyOrder(keys []string) []string {
	var indices, rest []string
	for _, key := range keys {
		if IsArrayIndex(key) {
			indices = append(indices, key)
		} else {
			rest = append(rest, key)
		}
	}
	if len(indices) == 0 {
		return append([]string(nil), keys...)
	}
	sort.SliceStable(indices, func(i, j int) bool {
		a, _ := strconv.ParseUint(indices[i], 10, 64)
		b, _ := strconv.ParseUint(indices[j], 10, 64)
		return a < b
	})
	return append(indices, rest...)
}

// IsObject is `typeof value === "object" && value !== null &&
// !Array.isArray(value)` for decoded JSON: a map or an ordered object.
func IsObject(value any) bool {
	switch value.(type) {
	case map[string]any, *Object:
		return true
	}
	return false
}

// Get is value[key] for an object (nil when absent or not an object).
func Get(value any, key string) any {
	switch o := value.(type) {
	case map[string]any:
		return o[key]
	case *Object:
		v, _ := o.Get(key)
		return v
	}
	return nil
}

// Has is `key in value` for an object.
func Has(value any, key string) bool {
	switch o := value.(type) {
	case map[string]any:
		_, ok := o[key]
		return ok
	case *Object:
		_, ok := o.Get(key)
		return ok
	}
	return false
}

// Keys is Object.keys(value) for an object. A plain map has lost its source
// order, so its non-index keys come back sorted.
func Keys(value any) []string {
	switch o := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(o))
		for key := range o {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return jsKeyOrder(keys)
	case *Object:
		return o.Keys()
	}
	return nil
}

// ToOrdered is `{...value}` as an ordered object: a shallow copy of a map or
// an ordered object, an empty object for anything else.
func ToOrdered(value any) *Object {
	switch o := value.(type) {
	case *Object:
		return o.Copy()
	case map[string]any:
		out := NewObject()
		for _, key := range Keys(o) {
			out.Set(key, o[key])
		}
		return out
	}
	return NewObject()
}

// Plain turns a value that may hold ordered objects or Undefined into plain
// decoded JSON: map[string]any, []any, string, float64, bool and nil.
func Plain(value any) any {
	switch v := value.(type) {
	case *Object:
		out := make(map[string]any, len(v.keys))
		for _, key := range v.keys {
			out[key] = Plain(v.values[key])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = Plain(item)
		}
		return out
	case []any:
		if v == nil {
			return []any(nil)
		}
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = Plain(item)
		}
		return out
	case undefinedType:
		return nil
	}
	return value
}

// ---------------------------------------------------------------------------
// JSON.parse

var errTrailing = errors.New("unexpected data after the JSON value")

// Parse is JSON.parse, objects as *Object so their key order survives. A
// repeated key keeps its first position and its last value, as in
// JavaScript. Numbers past float64's range read as ±Infinity.
func Parse(text string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	value, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			err = errTrailing
		}
		return nil, err
	}
	return value, nil
}

// ParsePlain is JSON.parse into plain decoded JSON.
func ParsePlain(text string) (any, error) {
	value, err := Parse(text)
	if err != nil {
		return nil, err
	}
	return Plain(value), nil
}

func parseValue(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		if err == io.EOF {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return parseToken(dec, token)
}

func parseToken(dec *json.Decoder, token json.Token) (any, error) {
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			object := NewObject()
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("object key is not a string")
				}
				value, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				object.Set(key, value)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return object, nil
		case '[':
			list := []any{}
			for dec.More() {
				value, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				list = append(list, value)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return list, nil
		}
		return nil, errors.New("unexpected delimiter")
	case json.Number:
		f, err := strconv.ParseFloat(string(t), 64)
		if err != nil {
			var numErr *strconv.NumError
			if !errors.As(err, &numErr) || numErr.Err != strconv.ErrRange {
				return nil, err
			}
		}
		return f, nil
	case string, bool, nil:
		return t, nil
	}
	return nil, errors.New("unexpected token")
}

// ---------------------------------------------------------------------------
// JSON.stringify

// Stringify is JSON.stringify(value) with no replacer or indent: the source
// key order for ordered objects (array indices first), sorted keys for plain
// maps, numbers as JavaScript writes them, no HTML escaping.
func Stringify(value any) string {
	var b strings.Builder
	writeJSON(&b, value)
	return b.String()
}

func writeJSON(b *strings.Builder, value any) {
	switch v := value.(type) {
	case nil:
		b.WriteString("null")
	case undefinedType:
		// Only reached in arrays: objects skip undefined members.
		b.WriteString("null")
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			b.WriteString("null")
		} else {
			b.WriteString(jsnum.String(v))
		}
	case int:
		b.WriteString(jsnum.String(float64(v)))
	case int64:
		b.WriteString(jsnum.String(float64(v)))
	case string:
		writeString(b, v)
	case []any:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, item)
		}
		b.WriteByte(']')
	case []string:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(b, item)
		}
		b.WriteByte(']')
	case map[string]any:
		writeObject(b, Keys(v), func(key string) any { return v[key] })
	case *Object:
		writeObject(b, v.Keys(), func(key string) any { return v.values[key] })
	default:
		// Not a decoded JSON value; fall back to encoding/json.
		data, err := json.Marshal(v)
		if err != nil {
			b.WriteString("null")
			return
		}
		b.Write(data)
	}
}

func writeObject(b *strings.Builder, keys []string, get func(string) any) {
	b.WriteByte('{')
	first := true
	for _, key := range keys {
		value := get(key)
		if value == Undefined {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		writeString(b, key)
		b.WriteByte(':')
		writeJSON(b, value)
	}
	b.WriteByte('}')
}

const hexDigits = "0123456789abcdef"

func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				b.WriteByte(hexDigits[r>>4])
				b.WriteByte(hexDigits[r&0xF])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// ---------------------------------------------------------------------------
// Values

// Truthy is JavaScript truthiness for a decoded JSON value.
func Truthy(value any) bool {
	switch v := value.(type) {
	case nil, undefinedType:
		return false
	case bool:
		return v
	case float64:
		return v != 0 && !math.IsNaN(v)
	case string:
		return v != ""
	}
	return true
}

// ToString is String(value) for a decoded JSON value.
func ToString(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case undefinedType:
		return "undefined"
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return jsnum.String(v)
	case string:
		return v
	case []any:
		parts := make([]string, len(v))
		for i, item := range v {
			if !Nullish(item) {
				parts[i] = ToString(item)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// ---------------------------------------------------------------------------
// Strings

// IsSpace is membership of JavaScript's \s (WhiteSpace plus LineTerminator),
// the set String.prototype.trim strips.
func IsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// SpaceClass is JavaScript's \s as an RE2 character class body.
const SpaceClass = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// Trim is String.prototype.trim.
func Trim(s string) string {
	return strings.TrimFunc(s, IsSpace)
}

// TrimStart is String.prototype.trimStart.
func TrimStart(s string) string {
	return strings.TrimLeftFunc(s, IsSpace)
}

// SplitSpace is `s.split(/\s+/).filter(Boolean)`.
func SplitSpace(s string) []string {
	return strings.FieldsFunc(s, IsSpace)
}

// UTF16Len is a string's .length: UTF-16 code units.
func UTF16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// SliceUTF16 is s.slice(0, n) counted in UTF-16 code units. A cut through a
// surrogate pair leaves a lone surrogate in JavaScript, which a Go string
// cannot hold: it becomes U+FFFD, keeping the length.
func SliceUTF16(s string, n int) string {
	units := 0
	for i, r := range s {
		width := 1
		if r >= 0x10000 {
			width = 2
		}
		if units+width > n {
			if units < n {
				return s[:i] + "\uFFFD"
			}
			return s[:i]
		}
		units += width
	}
	return s
}

// ToLower is String.prototype.toLowerCase: Go's simple case mapping plus the
// one unconditional special mapping JavaScript applies (U+0130 to "i̇"). Final
// sigma is not reproduced; it only changes non-ASCII text.
func ToLower(s string) string {
	if !strings.ContainsRune(s, 0x130) {
		return strings.ToLower(s)
	}
	var b strings.Builder
	for _, r := range s {
		if r == 0x130 {
			b.WriteString("i\u0307")
		} else {
			b.WriteString(strings.ToLower(string(r)))
		}
	}
	return b.String()
}

var decimalLiteral = regexp.MustCompile(`^[+-]?(?:(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?|Infinity)$`)
var radixLiteral = regexp.MustCompile(`^0(?:([xX])([0-9a-fA-F]+)|([oO])([0-7]+)|([bB])([01]+))$`)

// StringToNumber is Number(s) for a string: NaN when it is not a number.
func StringToNumber(s string) float64 {
	s = Trim(s)
	if s == "" {
		return 0
	}
	if decimalLiteral.MatchString(s) {
		switch strings.TrimLeft(s, "+-") {
		case "Infinity":
			if s[0] == '-' {
				return math.Inf(-1)
			}
			return math.Inf(1)
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			var numErr *strconv.NumError
			if !errors.As(err, &numErr) || numErr.Err != strconv.ErrRange {
				return math.NaN()
			}
		}
		return f
	}
	if m := radixLiteral.FindStringSubmatch(s); m != nil {
		base, digits := 16, m[2]
		if m[3] != "" {
			base, digits = 8, m[4]
		} else if m[5] != "" {
			base, digits = 2, m[6]
		}
		n, ok := new(big.Int).SetString(digits, base)
		if !ok {
			return math.NaN()
		}
		f, _ := new(big.Float).SetInt(n).Float64()
		return f
	}
	return math.NaN()
}

// DecodeUTF8 decodes bytes as UTF-8 the way Node's decoder does (WHATWG):
// each maximal ill-formed subsequence becomes one U+FFFD.
func DecodeUTF8(data []byte) string {
	if utf8.Valid(data) {
		return string(data)
	}
	var b strings.Builder
	b.Grow(len(data))
	for i := 0; i < len(data); {
		c := data[i]
		if c < 0x80 {
			b.WriteByte(c)
			i++
			continue
		}
		var need int
		lower, upper := byte(0x80), byte(0xBF)
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c >= 0xE0 && c <= 0xEF:
			need = 2
			if c == 0xE0 {
				lower = 0xA0
			} else if c == 0xED {
				upper = 0x9F
			}
		case c >= 0xF0 && c <= 0xF4:
			need = 3
			if c == 0xF0 {
				lower = 0x90
			} else if c == 0xF4 {
				upper = 0x8F
			}
		default:
			b.WriteRune(utf8.RuneError)
			i++
			continue
		}
		j := i + 1
		ok := true
		for k := 0; k < need; k++ {
			if j >= len(data) || data[j] < lower || data[j] > upper {
				ok = false
				break
			}
			lower, upper = 0x80, 0xBF
			j++
		}
		if !ok {
			b.WriteRune(utf8.RuneError)
			i = j
			continue
		}
		b.Write(data[i:j])
		i = j
	}
	return b.String()
}
