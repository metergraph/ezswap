package pricing

// Exact decimal arithmetic for prices, as Python's Decimal computes it, so a
// cost matches Metergraph's hosted figures (conformance/pricing.json). A value is n / 10^s.

import (
	"math"
	"math/big"
	"regexp"
	"strconv"

	"github.com/metergraph/replay/internal/jsnum"
)

// Decimal is n / 10^s; Dec parses one.
type Decimal struct {
	N *big.Int
	S int
}

var decimalPattern = regexp.MustCompile(`(?i)^([+-]?)(\d*)(?:\.(\d*))?(?:e([+-]?\d+))?$`)

var bigTen = big.NewInt(10)

func pow10(n int) *big.Int { return new(big.Int).Exp(bigTen, big.NewInt(int64(n)), nil) }

// Dec parses a decimal string or a finite number. Nil when it is not one.
func Dec(value any) *Decimal {
	var text string
	switch v := value.(type) {
	case nil, bool:
		return nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
		text = jsnum.String(v)
	case int:
		text = strconv.Itoa(v)
	case int64:
		text = strconv.FormatInt(v, 10)
	case *big.Int:
		if v == nil {
			return nil
		}
		text = v.String()
	case string:
		text = v
	default:
		return nil
	}
	match := decimalPattern.FindStringSubmatch(jsTrim(text))
	if match == nil {
		return nil
	}
	sign, whole, fraction, exponent := match[1], match[2], match[3], match[4]
	if exponent == "" {
		exponent = "0"
	}
	if whole == "" && fraction == "" {
		return nil
	}
	if whole == "" {
		whole = "0"
	}
	n, ok := new(big.Int).SetString(whole+fraction, 10)
	if !ok {
		return nil
	}
	e, err := strconv.Atoi(exponent)
	if err != nil {
		return nil
	}
	s := len(fraction) - e
	if s < 0 {
		n.Mul(n, pow10(-s))
		s = 0
	}
	if sign == "-" {
		n.Neg(n)
	}
	return &Decimal{N: n, S: s}
}

var (
	Zero = Decimal{N: big.NewInt(0), S: 0}
	One  = Decimal{N: big.NewInt(1), S: 0}
)

// Int is a whole number as a Decimal. A float64 count converts exactly, as
// BigInt(number) does for an integer.
func Int(value float64) Decimal {
	n, _ := new(big.Float).SetFloat64(value).Int(nil)
	return Decimal{N: n, S: 0}
}

func Mul(a, b Decimal) Decimal {
	return Decimal{N: new(big.Int).Mul(a.N, b.N), S: a.S + b.S}
}

func Add(a, b Decimal) Decimal {
	if a.S == b.S {
		return Decimal{N: new(big.Int).Add(a.N, b.N), S: a.S}
	}
	if a.S < b.S {
		n := new(big.Int).Mul(a.N, pow10(b.S-a.S))
		return Decimal{N: n.Add(n, b.N), S: b.S}
	}
	n := new(big.Int).Mul(b.N, pow10(a.S-b.S))
	return Decimal{N: n.Add(a.N, n), S: a.S}
}

// Shift divides by 10^places, exactly.
func Shift(a Decimal, places int) Decimal {
	return Decimal{N: a.N, S: a.S + places}
}

// UnitsHalfUp is units of 10^-places, rounded half away from zero (Decimal ROUND_HALF_UP).
func UnitsHalfUp(a Decimal, places int) *big.Int {
	if a.S <= places {
		return new(big.Int).Mul(a.N, pow10(places-a.S))
	}
	divisor := pow10(a.S - places)
	negative := a.N.Sign() < 0
	magnitude := new(big.Int).Abs(a.N)
	quotient, remainder := new(big.Int).QuoRem(magnitude, divisor, new(big.Int))
	if remainder.Mul(remainder, big.NewInt(2)).Cmp(divisor) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if negative {
		quotient.Neg(quotient)
	}
	return quotient
}

// UnitsUp is units of 10^-places, rounded away from zero (Decimal ROUND_UP).
func UnitsUp(a Decimal, places int) *big.Int {
	if a.S <= places {
		return new(big.Int).Mul(a.N, pow10(places-a.S))
	}
	divisor := pow10(a.S - places)
	negative := a.N.Sign() < 0
	magnitude := new(big.Int).Abs(a.N)
	quotient, remainder := new(big.Int).QuoRem(magnitude, divisor, new(big.Int))
	if remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if negative {
		quotient.Neg(quotient)
	}
	return quotient
}

// UnitsToNumber is a count of 10^-places units as a number (exact below 2^53 units).
func UnitsToNumber(units *big.Int, places int) float64 {
	// Number(bigint) rounds to nearest, ties to even, as big.Float does.
	f, _ := new(big.Float).SetInt(units).Float64()
	return f / math.Pow10(places)
}

// NumberToUnits turns a number that was a count of 10^-places units back to that count.
func NumberToUnits(value float64, places int) *big.Int {
	n, _ := new(big.Float).SetFloat64(jsnum.Round(value * math.Pow10(places))).Int(nil)
	return n
}

func Compare(a, b Decimal) int {
	return Add(a, Decimal{N: new(big.Int).Neg(b.N), S: b.S}).N.Sign()
}
