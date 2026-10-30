package core

// JavaScript's JSON values, as the TypeScript version read them. The view
// and every output core parses are JSON, and JavaScript objects keep their
// keys in a defined order (array-index keys first, ascending, then the rest
// in insertion order) that a Go map does not. Several figures depend on that
// order (list items compared by their JSON text, the paths an output holds),
// so parsed objects are an *Object, not a map.
//
// A loose value is one of: nil (null), bool, float64, string, []any, *Object
// or map[string]any (read with sorted keys, as Go's encoding/json gives it).
// Anything else a function is handed (a typed view, a slice of typed cases)
// goes through its JSON encoding first; see toLoose.

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/metergraph/replay/internal/jsnum"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// Loose is a JSON value read the way the TypeScript version reads its
// `Loose` (any) inputs: a view from the hosted product, one built from a run
// file, and partial test inputs all fit.
type Loose = any

// Object is a JSON object with JavaScript's own property order.
type Object struct {
	keys   []string
	values map[string]any
}

// NewObject returns an empty object.
func NewObject() *Object { return &Object{values: map[string]any{}} }

// arrayIndex reports whether key is a canonical array index ("0" to
// "4294967294"), which JavaScript orders before every other key.
func arrayIndex(key string) (uint64, bool) {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	n, err := strconv.ParseUint(key, 10, 64)
	if err != nil || n >= 4294967295 {
		return 0, false
	}
	return n, true
}

// Set adds or replaces a property. A replaced property keeps its place.
func (o *Object) Set(key string, value any) *Object {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
		if _, index := arrayIndex(key); index {
			// Keep index keys first, ascending; the rest keep insertion order.
			sort.SliceStable(o.keys, func(i, j int) bool {
				a, ai := arrayIndex(o.keys[i])
				b, bi := arrayIndex(o.keys[j])
				if ai && bi {
					return a < b
				}
				return ai && !bi
			})
		}
	}
	o.values[key] = value
	return o
}

// Get is `o[key]` for an own property.
func (o *Object) Get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.values[key]
	return v, ok
}

// Delete removes a property.
func (o *Object) Delete(key string) {
	if _, ok := o.values[key]; !ok {
		return
	}
	delete(o.values, key)
	o.keys = slices.DeleteFunc(o.keys, func(k string) bool { return k == key })
}

// Keys is Object.keys(o).
func (o *Object) Keys() []string {
	if o == nil {
		return nil
	}
	return slices.Clone(o.keys)
}

// Len is the number of own properties.
func (o *Object) Len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

// Clone is a shallow copy.
func (o *Object) Clone() *Object {
	c := NewObject()
	for _, k := range o.keys {
		c.Set(k, o.values[k])
	}
	return c
}

// MarshalJSON writes the object as JSON.stringify does.
func (o *Object) MarshalJSON() ([]byte, error) {
	if o == nil {
		return []byte("null"), nil
	}
	return []byte(Stringify(o, "")), nil
}

// UnmarshalJSON reads an object with JavaScript's key order.
func (o *Object) UnmarshalJSON(data []byte) error {
	v, err := ParseJSON(string(data))
	if err != nil {
		return err
	}
	parsed, ok := v.(*Object)
	if !ok {
		return errors.New("core: JSON value is not an object")
	}
	*o = *parsed
	return nil
}

// obj builds an object from key, value pairs.
func obj(pairs ...any) *Object {
	o := NewObject()
	for i := 0; i+1 < len(pairs); i += 2 {
		o.Set(pairs[i].(string), pairs[i+1])
	}
	return o
}

// ---------------------------------------------------------------------------
// JSON.parse

type jsonParser struct {
	s   string
	pos int
}

var errJSON = errors.New("core: invalid JSON")

// ParseJSON is JSON.parse: JSON's own whitespace, no NaN or Infinity, a
// number too large for a double reads as Infinity, a duplicate key keeps its
// first place and its last value. A lone surrogate escape reads as U+FFFD.
func ParseJSON(text string) (any, error) {
	p := &jsonParser{s: text}
	p.space()
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos != len(p.s) {
		return nil, errJSON
	}
	return v, nil
}

func (p *jsonParser) space() {
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *jsonParser) value() (any, error) {
	if p.pos >= len(p.s) {
		return nil, errJSON
	}
	switch c := p.s[p.pos]; {
	case c == '{':
		p.pos++
		o := NewObject()
		p.space()
		if p.pos < len(p.s) && p.s[p.pos] == '}' {
			p.pos++
			return o, nil
		}
		for {
			p.space()
			if p.pos >= len(p.s) || p.s[p.pos] != '"' {
				return nil, errJSON
			}
			key, err := p.str()
			if err != nil {
				return nil, err
			}
			p.space()
			if p.pos >= len(p.s) || p.s[p.pos] != ':' {
				return nil, errJSON
			}
			p.pos++
			p.space()
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			o.Set(key, v)
			p.space()
			if p.pos >= len(p.s) {
				return nil, errJSON
			}
			if p.s[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.s[p.pos] == '}' {
				p.pos++
				return o, nil
			}
			return nil, errJSON
		}
	case c == '[':
		p.pos++
		list := []any{}
		p.space()
		if p.pos < len(p.s) && p.s[p.pos] == ']' {
			p.pos++
			return list, nil
		}
		for {
			p.space()
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			list = append(list, v)
			p.space()
			if p.pos >= len(p.s) {
				return nil, errJSON
			}
			if p.s[p.pos] == ',' {
				p.pos++
				continue
			}
			if p.s[p.pos] == ']' {
				p.pos++
				return list, nil
			}
			return nil, errJSON
		}
	case c == '"':
		return p.str()
	case c == 't':
		return p.word("true", true)
	case c == 'f':
		return p.word("false", false)
	case c == 'n':
		return p.word("null", nil)
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	}
	return nil, errJSON
}

func (p *jsonParser) word(w string, v any) (any, error) {
	if strings.HasPrefix(p.s[p.pos:], w) {
		p.pos += len(w)
		return v, nil
	}
	return nil, errJSON
}

func (p *jsonParser) number() (any, error) {
	start := p.pos
	if p.s[p.pos] == '-' {
		p.pos++
	}
	digits := func() int {
		n := 0
		for p.pos < len(p.s) && p.s[p.pos] >= '0' && p.s[p.pos] <= '9' {
			p.pos++
			n++
		}
		return n
	}
	if p.pos >= len(p.s) {
		return nil, errJSON
	}
	if p.s[p.pos] == '0' {
		p.pos++
	} else if digits() == 0 {
		return nil, errJSON
	}
	if p.pos < len(p.s) && p.s[p.pos] == '.' {
		p.pos++
		if digits() == 0 {
			return nil, errJSON
		}
	}
	if p.pos < len(p.s) && (p.s[p.pos] == 'e' || p.s[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.s) && (p.s[p.pos] == '+' || p.s[p.pos] == '-') {
			p.pos++
		}
		if digits() == 0 {
			return nil, errJSON
		}
	}
	f, err := strconv.ParseFloat(p.s[start:p.pos], 64)
	if err != nil {
		var numErr *strconv.NumError
		if !errors.As(err, &numErr) || numErr.Err != strconv.ErrRange {
			return nil, errJSON
		}
	}
	return f, nil
}

func hexValue(s string) (rune, bool) {
	if len(s) < 4 {
		return 0, false
	}
	n, err := strconv.ParseUint(s[:4], 16, 32)
	if err != nil {
		return 0, false
	}
	return rune(n), true
}

func (p *jsonParser) str() (string, error) {
	p.pos++ // the opening quote
	var b strings.Builder
	for {
		if p.pos >= len(p.s) {
			return "", errJSON
		}
		c := p.s[p.pos]
		switch {
		case c == '"':
			p.pos++
			return b.String(), nil
		case c < 0x20:
			return "", errJSON
		case c == '\\':
			p.pos++
			if p.pos >= len(p.s) {
				return "", errJSON
			}
			e := p.s[p.pos]
			p.pos++
			switch e {
			case '"', '\\', '/':
				b.WriteByte(e)
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				r, ok := hexValue(p.s[p.pos:])
				if !ok {
					return "", errJSON
				}
				p.pos += 4
				if utf16.IsSurrogate(r) && r < 0xdc00 && strings.HasPrefix(p.s[p.pos:], "\\u") {
					if r2, ok := hexValue(p.s[p.pos+2:]); ok && r2 >= 0xdc00 && r2 <= 0xdfff {
						p.pos += 6
						b.WriteRune(utf16.DecodeRune(r, r2))
						continue
					}
				}
				if utf16.IsSurrogate(r) {
					r = utf8.RuneError
				}
				b.WriteRune(r)
			default:
				return "", errJSON
			}
		default:
			r, size := utf8.DecodeRuneInString(p.s[p.pos:])
			b.WriteRune(r)
			p.pos += size
		}
	}
}

// ---------------------------------------------------------------------------
// JSON.stringify

// Stringify is JSON.stringify(value, null, indent) for a loose value
// (indent "" is the compact form). NaN and the infinities are null. A typed
// Go value inside is written by encoding/json.
func Stringify(value any, indent string) string {
	var b strings.Builder
	stringifyTo(&b, value, indent, "")
	return b.String()
}

func jsQuote(b *strings.Builder, s string) {
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
				b.WriteByte("0123456789abcdef"[r>>4])
				b.WriteByte("0123456789abcdef"[r&0xf])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// jsonQuote is JSON.stringify of a string.
func jsonQuote(s string) string {
	var b strings.Builder
	jsQuote(&b, s)
	return b.String()
}

func stringifyTo(b *strings.Builder, value any, indent, current string) {
	inner := current + indent
	open := func(c byte, empty bool) {
		b.WriteByte(c)
		if indent != "" && !empty {
			b.WriteByte('\n')
			b.WriteString(inner)
		}
	}
	sep := func() {
		b.WriteByte(',')
		if indent != "" {
			b.WriteByte('\n')
			b.WriteString(inner)
		}
	}
	closing := func(c byte, empty bool) {
		if indent != "" && !empty {
			b.WriteByte('\n')
			b.WriteString(current)
		}
		b.WriteByte(c)
	}
	switch v := value.(type) {
	case nil:
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
		b.WriteString(strconv.Itoa(v))
	case string:
		jsQuote(b, v)
	case []any:
		open('[', len(v) == 0)
		for i, item := range v {
			if i > 0 {
				sep()
			}
			stringifyTo(b, item, indent, inner)
		}
		closing(']', len(v) == 0)
	case []string:
		open('[', len(v) == 0)
		for i, item := range v {
			if i > 0 {
				sep()
			}
			jsQuote(b, item)
		}
		closing(']', len(v) == 0)
	case *Object:
		if v == nil {
			b.WriteString("null")
			return
		}
		open('{', len(v.keys) == 0)
		for i, k := range v.keys {
			if i > 0 {
				sep()
			}
			jsQuote(b, k)
			b.WriteByte(':')
			if indent != "" {
				b.WriteByte(' ')
			}
			stringifyTo(b, v.values[k], indent, inner)
		}
		closing('}', len(v.keys) == 0)
	case map[string]any:
		keys := sortedKeys(v)
		open('{', len(keys) == 0)
		for i, k := range keys {
			if i > 0 {
				sep()
			}
			jsQuote(b, k)
			b.WriteByte(':')
			if indent != "" {
				b.WriteByte(' ')
			}
			stringifyTo(b, v[k], indent, inner)
		}
		closing('}', len(keys) == 0)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			b.WriteString("null")
			return
		}
		if indent != "" {
			var out bytes.Buffer
			if json.Indent(&out, data, current, indent) == nil {
				data = out.Bytes()
			}
		}
		b.Write(data)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---------------------------------------------------------------------------
// Reading loose values

// ToLoose turns any value into a loose one: loose values as they are, typed
// values (a view, a slice of cases) through their JSON encoding. Every
// function that reads a view does this itself; a caller that makes many
// calls on one typed view can convert it once and pass the result. It
// panics on a value with no JSON form (a NaN in a typed float field).
func ToLoose(v any) any { return toLoose(v) }

func toLoose(v any) any {
	switch t := v.(type) {
	case nil, bool, float64, string, []any, *Object, map[string]any:
		return v
	case int:
		return float64(t)
	case *float64:
		if t == nil {
			return nil
		}
		return *t
	case *string:
		if t == nil {
			return nil
		}
		return *t
	}
	rv := reflect.ValueOf(v)
	if (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Slice || rv.Kind() == reflect.Map) && rv.IsNil() {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		panic("core: value has no JSON form: " + err.Error())
	}
	parsed, err := ParseJSON(string(data))
	if err != nil {
		panic("core: value has no JSON form: " + err.Error())
	}
	return parsed
}

// get is `v?.[key]`: the own property, and whether it is there.
func get(v any, key string) (any, bool) {
	switch t := v.(type) {
	case *Object:
		return t.Get(key)
	case map[string]any:
		x, ok := t[key]
		return x, ok
	}
	return nil, false
}

// at is `v?.a?.b...`, nil when any step is missing.
func at(v any, keys ...string) any {
	for _, k := range keys {
		v, _ = get(v, k)
		if v == nil {
			return nil
		}
	}
	return v
}

// has is `Object.hasOwn(v, key)`.
func has(v any, key string) bool {
	_, ok := get(v, key)
	return ok
}

// keysOf is Object.keys for a loose object, nil otherwise.
func keysOf(v any) []string {
	switch t := v.(type) {
	case *Object:
		return t.Keys()
	case map[string]any:
		return sortedKeys(t)
	}
	return nil
}

// valuesOf is Object.values for a loose object (`|| {}` included).
func valuesOf(v any) []any {
	keys := keysOf(v)
	out := make([]any, len(keys))
	for i, k := range keys {
		out[i], _ = get(v, k)
	}
	return out
}

// list is `v || []` for an array.
func list(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

// isArray is Array.isArray.
func isArray(v any) bool {
	_, ok := v.([]any)
	return ok
}

// num is `typeof v === "number"`.
func num(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

// finiteNum is `typeof v === "number" && Number.isFinite(v)`.
func finiteNum(v any) (float64, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// numPtr is a loose number as *float64: nil unless it is a number.
func numPtr(v any) *float64 {
	if f, ok := v.(float64); ok {
		return &f
	}
	return nil
}

// truthy is JavaScript truthiness.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0 && !math.IsNaN(t)
	case string:
		return t != ""
	}
	return true
}

// jsNumber is Number(v).
func jsNumber(v any) float64 {
	switch t := v.(type) {
	case nil:
		return 0
	case bool:
		if t {
			return 1
		}
		return 0
	case float64:
		return t
	case string:
		s := jsTrim(t)
		if s == "" {
			return 0
		}
		switch s {
		case "Infinity", "+Infinity":
			return math.Inf(1)
		case "-Infinity":
			return math.Inf(-1)
		}
		if strings.ContainsAny(s, "xXoObB") {
			if n, err := strconv.ParseInt(s, 0, 64); err == nil && !strings.HasPrefix(s, "-") && !strings.HasPrefix(s, "+") {
				return float64(n)
			}
			return math.NaN()
		}
		if strings.ContainsAny(s, "_iInN") {
			return math.NaN()
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			var numErr *strconv.NumError
			if errors.As(err, &numErr) && numErr.Err == strconv.ErrRange {
				return f
			}
			return math.NaN()
		}
		return f
	case []any:
		return jsNumber(jsString(t))
	}
	return math.NaN()
}

// jsString is String(v).
func jsString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return jsnum.String(t)
	case string:
		return t
	case []any:
		parts := make([]string, len(t))
		for i, item := range t {
			if item != nil {
				parts[i] = jsString(item)
			}
		}
		return strings.Join(parts, ",")
	case *Object, map[string]any:
		return "[object Object]"
	}
	return jsString(toLoose(v))
}

// strictEqual is `a === b` on loose values: objects and arrays by identity.
func strictEqual(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool:
		y, ok := b.(bool)
		return ok && x == y
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case string:
		y, ok := b.(string)
		return ok && x == y
	case *Object:
		y, ok := b.(*Object)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		return ok && len(x) == len(y) && (len(x) == 0 && reflect.ValueOf(x).Pointer() == reflect.ValueOf(y).Pointer() || len(x) > 0 && &x[0] == &y[0])
	}
	return false
}

// ---------------------------------------------------------------------------
// JavaScript strings

// isJSSpace is JavaScript's WhiteSpace and LineTerminator (`\s`, trim).
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// utf16Len is a string's .length: UTF-16 code units.
func utf16Len(s string) int {
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

// utf16Slice is s.slice(0, n) in UTF-16 units. A surrogate pair cut in half
// leaves a lone surrogate in JavaScript; Go strings hold U+FFFD there.
func utf16Slice(s string, n int) string {
	units := utf16.Encode([]rune(s))
	if n < len(units) {
		units = units[:n]
	}
	return string(utf16.Decode(units))
}

// utf16Less is JavaScript's default sort order: by UTF-16 code unit.
func utf16Less(a, b string) bool {
	x := utf16.Encode([]rune(a))
	y := utf16.Encode([]rune(b))
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return len(x) < len(y)
}

// jsToLower is String.prototype.toLowerCase, including the one
// context-free special casing Go's simple mapping lacks (U+0130).
func jsToLower(s string) string {
	if !strings.ContainsRune(s, 0x130) {
		return strings.ToLower(s)
	}
	return strings.ToLower(strings.ReplaceAll(s, "İ", "i̇"))
}

var (
	collatorMu sync.Mutex
	collator   = collate.New(language.English)
)

// localeCompare is a.localeCompare(b): ICU collation, not code units.
func localeCompare(a, b string) int {
	collatorMu.Lock()
	defer collatorMu.Unlock()
	return collator.CompareString(a, b)
}

// sortNumbers is `[...values].sort((a, b) => a - b)`: stable, NaN compares as equal.
func sortNumbers(values []float64) []float64 {
	out := slices.Clone(values)
	slices.SortStableFunc(out, func(a, b float64) int {
		d := a - b
		switch {
		case d < 0:
			return -1
		case d > 0:
			return 1
		}
		return 0
	})
	return out
}

func ptr[T any](v T) *T { return &v }
