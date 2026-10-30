package pricing

// JavaScript-exact helpers the catalog and the estimate need: String.prototype.trim,
// Number(value), truthiness, and Array.prototype.sort's default order.

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/metergraph/replay/internal/jsnum"
)

// isJSSpace is a WhiteSpace or LineTerminator code point as trim() strips it.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

var jsDecimalLiteral = regexp.MustCompile(`^[+-]?(?:Infinity|(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?)$`)

// jsStringToNumber is Number(string).
func jsStringToNumber(s string) float64 {
	text := jsTrim(s)
	if text == "" {
		return 0
	}
	if len(text) > 2 && text[0] == '0' {
		base := 0
		switch text[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			digits := text[2:]
			value := 0.0
			for _, c := range digits {
				d := strings.IndexRune("0123456789abcdef", c|0x20)
				if d < 0 || d >= base {
					return math.NaN()
				}
				value = value*float64(base) + float64(d)
			}
			return value
		}
	}
	if !jsDecimalLiteral.MatchString(text) {
		return math.NaN()
	}
	switch strings.TrimLeft(text, "+-") {
	case "Infinity":
		if text[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	// Out of range, ParseFloat returns ±Inf or ±0 with its error, as Number does.
	value, _ := strconv.ParseFloat(text, 64)
	return value
}

// jsToString is String(value) for a decoded JSON value.
func jsToString(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
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
			if item != nil {
				parts[i] = jsToString(item)
			}
		}
		return strings.Join(parts, ",")
	default:
		return "[object Object]"
	}
}

// jsToNumber is Number(value) for a decoded JSON value.
func jsToNumber(value any) float64 {
	switch v := value.(type) {
	case nil:
		return 0
	case bool:
		if v {
			return 1
		}
		return 0
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		return jsStringToNumber(v)
	case []any:
		return jsStringToNumber(jsToString(v))
	default:
		return math.NaN()
	}
}

// truthy is Boolean(value) for a decoded JSON value.
func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0 && !math.IsNaN(v)
	case int:
		return v != 0
	case string:
		return v != ""
	default:
		return true
	}
}

// utf16Less is the default sort order of Array.prototype.sort for strings:
// by UTF-16 code unit.
func utf16Less(a, b string) bool {
	x, y := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] < y[i]
		}
	}
	return len(x) < len(y)
}
