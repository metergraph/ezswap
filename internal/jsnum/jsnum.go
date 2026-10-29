// Package jsnum reproduces JavaScript's number formatting and rounding
// exactly. The report's text, the conformance fixtures and the golden files
// were all written by JavaScript, so a figure has to come out the same digit
// for digit. Go's strconv and math.Round differ on ties and on when to switch
// to exponent notation; use these instead wherever a number becomes text.
package jsnum

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Round is Math.round: halves round toward +Infinity (-2.5 -> -2).
func Round(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	f := math.Floor(x)
	if x-f >= 0.5 {
		return f + 1
	}
	return f
}

// shortest returns the shortest round-trip decimal digits of |x| (no leading
// zeros) and n, the position of the decimal point: |x| = 0.digits * 10^n.
func shortest(x float64) (string, int) {
	s := strconv.FormatFloat(math.Abs(x), 'e', -1, 64) // d.ddde±XX
	mant, exp, _ := strings.Cut(s, "e")
	e, _ := strconv.Atoi(exp)
	digits := strings.Replace(mant, ".", "", 1)
	return digits, e + 1
}

// String is String(x) / Number.prototype.toString().
func String(x float64) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case math.IsInf(x, 1):
		return "Infinity"
	case math.IsInf(x, -1):
		return "-Infinity"
	case x == 0:
		return "0"
	}
	sign := ""
	if x < 0 {
		sign = "-"
	}
	digits, n := shortest(x)
	k := len(digits)
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	e := n - 1
	es := "+"
	if e < 0 {
		es = "-"
		e = -e
	}
	m := digits[:1]
	if k > 1 {
		m += "." + digits[1:]
	}
	return sign + m + "e" + es + strconv.Itoa(e)
}

var ten = big.NewInt(10)

func pow10(n int) *big.Int { return new(big.Int).Exp(ten, big.NewInt(int64(n)), nil) }

// roundHalfUp rounds a non-negative rational to an integer, ties up.
func roundHalfUp(r *big.Rat) *big.Int {
	twice := new(big.Rat).Mul(r, big.NewRat(2, 1))
	twice.Add(twice, big.NewRat(1, 1)) // 2r + 1
	num := new(big.Int).Mul(twice.Num(), big.NewInt(1))
	den := new(big.Int).Mul(twice.Denom(), big.NewInt(2))
	return num.Quo(num, den) // floor((2r+1)/2) = floor(r + 1/2)
}

// ToFixed is x.toFixed(digits), digits 0..100.
func ToFixed(x float64, digits int) string {
	if math.IsNaN(x) {
		return "NaN"
	}
	if math.Abs(x) >= 1e21 || math.IsInf(x, 0) {
		return String(x)
	}
	sign := ""
	if x < 0 {
		sign = "-"
	}
	r := new(big.Rat).SetFloat64(math.Abs(x))
	r.Mul(r, new(big.Rat).SetInt(pow10(digits)))
	n := roundHalfUp(r).String()
	if digits == 0 {
		return sign + n
	}
	if len(n) <= digits {
		n = strings.Repeat("0", digits-len(n)+1) + n
	}
	return sign + n[:len(n)-digits] + "." + n[len(n)-digits:]
}

// significant returns the precision-digit integer m and exponent e with
// m * 10^(e-precision+1) closest to |x| (ties to the larger m).
func significant(x float64, precision int) (string, int) {
	_, n := shortest(x)
	e := n - 1
	abs := new(big.Rat).SetFloat64(math.Abs(x))
	for {
		scale := e - precision + 1
		r := new(big.Rat).Set(abs)
		if scale >= 0 {
			r.Quo(r, new(big.Rat).SetInt(pow10(scale)))
		} else {
			r.Mul(r, new(big.Rat).SetInt(pow10(-scale)))
		}
		m := roundHalfUp(r)
		if m.Cmp(pow10(precision)) >= 0 {
			e++
			continue
		}
		if m.Cmp(pow10(precision-1)) < 0 {
			e--
			continue
		}
		return m.String(), e
	}
}

func expSuffix(e int) string {
	if e < 0 {
		return "e-" + strconv.Itoa(-e)
	}
	return "e+" + strconv.Itoa(e)
}

// ToPrecision is x.toPrecision(precision), precision 1..100.
func ToPrecision(x float64, precision int) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return String(x)
	}
	sign := ""
	if x < 0 {
		sign = "-"
	}
	if x == 0 {
		if precision == 1 {
			return "0"
		}
		return "0." + strings.Repeat("0", precision-1)
	}
	m, e := significant(x, precision)
	if e < -6 || e >= precision {
		out := m[:1]
		if precision > 1 {
			out += "." + m[1:]
		}
		return sign + out + expSuffix(e)
	}
	if e == precision-1 {
		return sign + m
	}
	if e >= 0 {
		return sign + m[:e+1] + "." + m[e+1:]
	}
	return sign + "0." + strings.Repeat("0", -(e+1)) + m
}

// ToExponential is x.toExponential(fractionDigits), fractionDigits 0..100.
func ToExponential(x float64, fractionDigits int) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return String(x)
	}
	sign := ""
	if x < 0 {
		sign = "-"
	}
	if x == 0 {
		out := "0"
		if fractionDigits > 0 {
			out += "." + strings.Repeat("0", fractionDigits)
		}
		return sign + out + "e+0"
	}
	m, e := significant(x, fractionDigits+1)
	out := m[:1]
	if fractionDigits > 0 {
		out += "." + m[1:]
	}
	return sign + out + expSuffix(e)
}

// ToExponentialShortest is x.toExponential() with no argument: as many
// digits as it takes to read back as x.
func ToExponentialShortest(x float64) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return String(x)
	}
	sign := ""
	if x < 0 {
		sign = "-"
	}
	if x == 0 {
		return "0e+0"
	}
	digits, n := shortest(x)
	out := digits[:1]
	if len(digits) > 1 {
		out += "." + digits[1:]
	}
	return sign + out + expSuffix(n-1)
}

// GroupInt is Math.round(x).toLocaleString("en-US") for a finite x: digits
// grouped by commas.
func GroupInt(x float64) string {
	s := ToFixed(Round(x), 0)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	if s == "0" {
		sign = "" // toLocaleString drops the sign of -0
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return sign + b.String()
}

// IsInteger is Number.isInteger.
func IsInteger(x float64) bool {
	return !math.IsInf(x, 0) && !math.IsNaN(x) && x == math.Trunc(x)
}
