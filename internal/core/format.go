package core

// Formatters for the figures the report prints. They match Metergraph's
// hosted figures, pinned by conformance/experiment_equivalence.json. A bare
// "—" marks a missing value.
//
// Each takes a number as float64, *float64, int or *int, or a loose value;
// anything that is not a finite number prints as MissingValue.

import (
	"math"
	"strconv"
	"strings"

	"github.com/metergraph/replay/internal/jsnum"
)

// MissingValue marks a missing figure.
const MissingValue = "—"

// finite reads a number of any of the accepted shapes.
func finite(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, !math.IsNaN(v) && !math.IsInf(v, 0)
	case *float64:
		if v == nil {
			return 0, false
		}
		return finite(*v)
	case int:
		return float64(v), true
	case *int:
		if v == nil {
			return 0, false
		}
		return float64(*v), true
	}
	return 0, false
}

// localeString is x.toLocaleString("en-US"): digits grouped by commas and at
// most three fraction digits, rounded half away from zero on the shortest
// decimal that reads back as x, as ICU does.
func localeString(x float64) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case math.IsInf(x, 1):
		return "∞"
	case math.IsInf(x, -1):
		return "-∞"
	}
	if jsnum.IsInteger(x) && math.Abs(x) < 1e21 {
		return jsnum.GroupInt(x)
	}
	sign := ""
	if x < 0 {
		sign = "-"
	}
	// The shortest digits, written out in full.
	text := strconv.FormatFloat(math.Abs(x), 'f', -1, 64)
	whole, fraction, _ := strings.Cut(text, ".")
	if len(fraction) > 3 {
		up := fraction[3] >= '5'
		fraction = fraction[:3]
		if up {
			digits := []byte(whole + fraction)
			i := len(digits) - 1
			for ; i >= 0; i-- {
				if digits[i] == '9' {
					digits[i] = '0'
					continue
				}
				digits[i]++
				break
			}
			s := string(digits)
			if i < 0 {
				s = "1" + s
			}
			whole, fraction = s[:len(s)-3], s[len(s)-3:]
		}
	}
	fraction = strings.TrimRight(fraction, "0")
	if whole == "" {
		whole = "0"
	}
	var b strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	out := b.String()
	if fraction != "" {
		out += "." + fraction
	}
	if out == "0" {
		sign = ""
	}
	return sign + out
}

// FormatUSD is formatUsd.
func FormatUSD(value any) string {
	v, ok := finite(value)
	if !ok {
		return MissingValue
	}
	switch {
	case v == 0:
		return "$0"
	case v >= 100:
		return "$" + localeString(jsnum.Round(v))
	case v >= 1:
		return "$" + jsnum.ToFixed(v, 2)
	case v >= 0.01:
		return "$" + jsnum.ToFixed(v, 3)
	}
	return "$" + jsnum.String(parseNumber(jsnum.ToPrecision(v, 2)))
}

// FormatMs is formatMs.
func FormatMs(value any) string {
	v, ok := finite(value)
	if !ok {
		return MissingValue
	}
	switch {
	case v >= 10000:
		return jsnum.ToFixed(v/1000, 0) + " s"
	case v >= 1000:
		return jsnum.ToFixed(v/1000, 1) + " s"
	}
	return jsnum.String(jsnum.Round(v)) + " ms"
}

// FormatPercent is formatPercent.
func FormatPercent(value any) string {
	v, ok := finite(value)
	if !ok {
		return MissingValue
	}
	return jsnum.String(jsnum.Round(float64(v*100))) + "%"
}

// FormatCount is formatCount.
func FormatCount(value any) string {
	v, ok := finite(value)
	if !ok {
		return MissingValue
	}
	return localeString(jsnum.Round(v))
}

// FormatNumber is formatNumber.
func FormatNumber(value any) string {
	v, ok := finite(value)
	if !ok {
		return MissingValue
	}
	if jsnum.IsInteger(v) {
		return localeString(v)
	}
	abs := math.Abs(v)
	digits := 2
	if abs >= 100 {
		digits = 0
	} else if abs >= 10 {
		digits = 1
	}
	return jsnum.ToFixed(v, digits)
}

// FormatSigned is formatSigned: a signed figure with a true minus sign
// (U+2212). A nil format is FormatNumber.
func FormatSigned(value any, format func(any) string) string {
	if format == nil {
		format = FormatNumber
	}
	v, ok := finite(value)
	if !ok {
		return MissingValue
	}
	if v == 0 {
		return format(0.0)
	}
	sign := "+"
	if v < 0 {
		sign = "−"
	}
	return sign + format(math.Abs(v))
}

// FormatCachedShare is formatCachedShare: a call whose reply carried no
// cache count is not reported, never 0%.
func FormatCachedShare(value any) string {
	if _, ok := finite(value); !ok {
		return "not reported"
	}
	return FormatPercent(value)
}
