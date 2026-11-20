package core

// Python semantics the per-arm summary and the noise floor depend on. Their
// figures must match Metergraph's hosted figures to the last bit
// (conformance/summarize.json, conformance/noise_floor.json), so where Python
// and JavaScript disagree on rounding, summation, whitespace or string length,
// these follow Python.

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/metergraph/ezswap/internal/jsnum"
)

// pyWS is the class body of Python's `str.isspace()` characters, which
// `str.strip()` and `\s` use.
const pyWS = `\t\n\x{0b}\x{0c}\r\x{1c}-\x{1f} \x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`

// pyWSClass is pyWS as a regular expression character class.
const pyWSClass = "[" + pyWS + "]"

func isPySpace(r rune) bool {
	switch r {
	case '\t', '\n', 0x0b, 0x0c, '\r', ' ', 0x85, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
		return true
	}
	return (r >= 0x1c && r <= 0x1f) || (r >= 0x2000 && r <= 0x200a)
}

// pyStrip is `str.strip()`: unlike String.prototype.trim, keeps U+FEFF and
// strips U+001C..U+001F and U+0085.
func pyStrip(text string) string { return strings.TrimFunc(text, isPySpace) }

// pyRound is `round(x, digits)`: correctly rounded, exact ties to even.
func pyRound(x float64, digits int) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) >= 1e21 {
		return x
	}
	exact := jsnum.ToFixed(math.Abs(x), 100)
	point := strings.IndexByte(exact, '.')
	rest := exact[point+1+digits:]
	tie := rest != "" && rest[0] == '5' && strings.Trim(rest[1:], "0") == ""
	if tie {
		end := point
		if digits > 0 {
			end = point + 1 + digits
		}
		kept := exact[:end]
		last := kept[len(kept)-1] - '0'
		if last%2 == 0 {
			sign := ""
			if x < 0 {
				sign = "-"
			}
			return parseNumber(sign + kept)
		}
	}
	return parseNumber(jsnum.ToFixed(x, digits))
}

// parseNumber is Number(text) for a decimal the code itself wrote.
func parseNumber(text string) float64 {
	f, _ := strconv.ParseFloat(text, 64)
	return f
}

// pySum is `sum()` over floats. Python 3.12 and later add floats with
// Neumaier's compensated summation, so `sum([0.1] * 10)` is exactly 1.0.
func pySum(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0 + values[0]
	compensation := 0.0
	for _, x := range values[1:] {
		t := total + x
		if math.Abs(total) >= math.Abs(x) {
			compensation += total - t + x
		} else {
			compensation += x - t + total
		}
		total = t
	}
	if compensation != 0 && !math.IsNaN(compensation) && !math.IsInf(compensation, 0) {
		total += compensation
	}
	return total
}

// pyMedian is `statistics.median`: the middle value, or the mean of the two
// middle ones. Nil for no values.
func pyMedian(values []float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	sorted := sortNumbers(values)
	n := len(sorted)
	mid := n / 2
	if n%2 == 1 {
		return ptr(sorted[mid])
	}
	return ptr((sorted[mid-1] + sorted[mid]) / 2)
}

// pyQuantile is a linear-interpolated (inclusive) quantile.
func pyQuantile(values []float64, q float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	ordered := sortNumbers(values)
	position := float64(float64(len(ordered)-1) * q)
	low := int(math.Trunc(position))
	high := min(low+1, len(ordered)-1)
	lo := ordered[low]
	hi := ordered[high]
	return ptr(lo + float64((hi-lo)*(position-float64(low))))
}

// codePointLength is `len(text)`: code points, not UTF-16 units.
func codePointLength(text string) int { return utf8.RuneCountInString(text) }

// codePointSlice is `text[:n]` in code points.
func codePointSlice(text string, n int) string {
	i := 0
	for pos := range text {
		if i >= n {
			return text[:pos]
		}
		i++
	}
	return text
}

// pySorted is Python's string order: by code point (UTF-8 byte order),
// where JavaScript compares UTF-16 units.
func pySorted(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}

// pyTruthy is Python truthiness: empty strings, lists and objects are false.
func pyTruthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0 && !math.IsNaN(v)
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case *Object:
		return v.Len() > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}

// isPlainObject is a JSON object (not an array, not null).
func isPlainObject(value any) bool {
	switch v := value.(type) {
	case *Object:
		return v != nil
	case map[string]any:
		return v != nil
	}
	return false
}

// pyNumberText is a number as Python would print it. A JSON number arrives
// here without Python's int/float distinction, so an integral value below
// 1e16 is taken for an int (as JSON integers are) and everything else for a
// float.
func pyNumberText(x float64) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case math.IsInf(x, 1):
		return "Infinity"
	case math.IsInf(x, -1):
		return "-Infinity"
	}
	if jsnum.IsInteger(x) && math.Abs(x) < 1e16 {
		if x == 0 && math.Signbit(x) {
			return "-0.0"
		}
		return jsnum.String(x)
	}
	return pyFloatRepr(x)
}

// pyFloatRepr is `repr(float)`: shortest round-trip digits, exponent form
// below 1e-4 or from 1e16.
func pyFloatRepr(x float64) string {
	if math.IsNaN(x) {
		return "nan"
	}
	if math.IsInf(x, 0) {
		if x > 0 {
			return "inf"
		}
		return "-inf"
	}
	if x == 0 {
		if math.Signbit(x) {
			return "-0.0"
		}
		return "0.0"
	}
	sign := ""
	if x < 0 {
		sign = "-"
	}
	mantissa, exponentText, _ := strings.Cut(jsnum.ToExponentialShortest(math.Abs(x)), "e")
	exponent, _ := strconv.Atoi(exponentText)
	digits := strings.Replace(mantissa, ".", "", 1)
	if exponent < -4 || exponent >= 16 {
		body := digits
		if len(digits) > 1 {
			body = digits[:1] + "." + digits[1:]
		}
		abs := exponent
		if abs < 0 {
			abs = -abs
		}
		e := strconv.Itoa(abs)
		if abs < 10 {
			e = "0" + e
		}
		es := "+"
		if exponent < 0 {
			es = "-"
		}
		return sign + body + "e" + es + e
	}
	if exponent < 0 {
		return sign + "0." + strings.Repeat("0", -exponent-1) + digits
	}
	whole := digits
	if len(whole) > exponent+1 {
		whole = whole[:exponent+1]
	} else {
		whole += strings.Repeat("0", exponent+1-len(whole))
	}
	fraction := ""
	if len(digits) > exponent+1 {
		fraction = digits[exponent+1:]
	}
	if fraction == "" {
		fraction = "0"
	}
	return sign + whole + "." + fraction
}

// pyStr is `str(value)` for a JSON value: None, True and False are spelt as
// Python spells them.
func pyStr(value any) string {
	switch v := value.(type) {
	case nil:
		return "None"
	case bool:
		if v {
			return "True"
		}
		return "False"
	case float64:
		if jsnum.IsInteger(v) && math.Abs(v) < 1e16 {
			return jsnum.String(v)
		}
		return pyFloatRepr(v)
	case string:
		return v
	}
	return pyJSONDumps(value, false, false)
}

func pyJSONString(b *strings.Builder, text string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for _, unit := range utf16.Encode([]rune(text)) {
		switch unit {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if unit < 0x20 || unit > 0x7e {
				// ensure_ascii: every non-ASCII UTF-16 unit as \uXXXX (DEL stays as is).
				if unit == 0x7f {
					b.WriteByte(0x7f)
				} else {
					b.WriteString(`\u`)
					b.WriteByte(hex[unit>>12&0xf])
					b.WriteByte(hex[unit>>8&0xf])
					b.WriteByte(hex[unit>>4&0xf])
					b.WriteByte(hex[unit&0xf])
				}
			} else {
				b.WriteByte(byte(unit))
			}
		}
	}
	b.WriteByte('"')
}

// pyJSONDumps is `json.dumps(value)` with ensure_ascii, used to bound and to
// compare values. `compact` is `separators=(",", ":")`.
func pyJSONDumps(value any, sortKeys, compact bool) string {
	item, pair := ", ", ": "
	if compact {
		item, pair = ",", ":"
	}
	var b strings.Builder
	var dump func(v any)
	dump = func(v any) {
		switch t := v.(type) {
		case nil:
			b.WriteString("null")
		case bool:
			if t {
				b.WriteString("true")
			} else {
				b.WriteString("false")
			}
		case float64:
			b.WriteString(pyNumberText(t))
		case int:
			b.WriteString(strconv.Itoa(t))
		case string:
			pyJSONString(&b, t)
		case []any:
			b.WriteByte('[')
			for i, x := range t {
				if i > 0 {
					b.WriteString(item)
				}
				dump(x)
			}
			b.WriteByte(']')
		case []string:
			b.WriteByte('[')
			for i, x := range t {
				if i > 0 {
					b.WriteString(item)
				}
				pyJSONString(&b, x)
			}
			b.WriteByte(']')
		case *Object, map[string]any:
			keys := keysOf(t)
			if sortKeys {
				keys = pySorted(keys)
			}
			b.WriteByte('{')
			for i, k := range keys {
				if i > 0 {
					b.WriteString(item)
				}
				pyJSONString(&b, k)
				b.WriteString(pair)
				x, _ := get(t, k)
				dump(x)
			}
			b.WriteByte('}')
		default:
			dump(toLoose(t))
		}
	}
	dump(value)
	return b.String()
}

// pyJSONBytes is `len(json.dumps(value).encode("utf-8"))`: the output is
// ASCII, so its length.
func pyJSONBytes(value any) int { return len(pyJSONDumps(value, false, false)) }

// pyEqual is Python `==` on the values the statistics compare: scalars, and
// tuples of them.
func pyEqual(a, b any) bool {
	x, xa := a.([]any)
	y, ya := b.([]any)
	if xa && ya {
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if !pyEqual(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return strictEqual(a, b)
}
