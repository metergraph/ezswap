package report

import "math"

// PairedLevel is the chance of a false "beyond noise" the paired test allows
// for one part when the treatment behaves like production.
const PairedLevel = 0.05

// PairedTest is a one-sided paired sign test. b is the cases that changed
// under the treatment and not under production's rerun; c is the reverse.
// Were the treatment no different from production, a case would be as likely
// to fall in b as in c. The test passes when b is larger than c and the
// chance of b or more out of b+c at even odds is at most PairedLevel.
// Passing: 5 against 0, 7 against 1, 9 against 2, 10 against 3.
func PairedTest(b, c int) bool {
	if b <= c || b <= 0 || c < 0 {
		return false
	}
	return binomialTail(b, b+c) <= PairedLevel
}

// binomialTail is the chance of k or more heads in n fair coin flips.
func binomialTail(k, n int) float64 {
	total := 0.0
	for i := k; i <= n; i++ {
		lg, _ := math.Lgamma(float64(n + 1))
		la, _ := math.Lgamma(float64(i + 1))
		lb, _ := math.Lgamma(float64(n - i + 1))
		total += math.Exp(lg - la - lb - float64(n)*math.Ln2)
	}
	return total
}
