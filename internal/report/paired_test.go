package report

import (
	"math/rand"
	"testing"
)

func TestPairedTestTable(t *testing.T) {
	pass := [][2]int{{5, 0}, {7, 1}, {9, 2}, {10, 3}, {9, 1}}
	fail := [][2]int{{4, 0}, {6, 1}, {8, 2}, {9, 3}, {4, 3}, {2, 3}, {0, 0}, {0, 5}, {3, 3}}
	for _, c := range pass {
		if !PairedTest(c[0], c[1]) {
			t.Errorf("%d against %d should pass", c[0], c[1])
		}
	}
	for _, c := range fail {
		if PairedTest(c[0], c[1]) {
			t.Errorf("%d against %d should not pass", c[0], c[1])
		}
	}
}

// A treatment that behaves exactly like production must read beyond noise in
// at most 5% of parts, whatever the noise level and whether or not the
// recording has drifted (drift raises both counts alike).
func TestPairedTestFalseRate(t *testing.T) {
	random := rand.New(rand.NewSource(20261202))
	for _, noise := range []float64{0.05, 0.15, 0.40} {
		for _, drift := range []float64{0, 0.3} {
			beyond := 0
			const parts, cases = 10000, 20
			for p := 0; p < parts; p++ {
				b, c := 0, 0
				for i := 0; i < cases; i++ {
					drifted := random.Float64() < drift
					treatment := drifted || random.Float64() < noise
					rerun := drifted || random.Float64() < noise
					switch {
					case treatment && !rerun:
						b++
					case rerun && !treatment:
						c++
					}
				}
				if PairedTest(b, c) {
					beyond++
				}
			}
			if rate := float64(beyond) / parts; rate > PairedLevel {
				t.Errorf("noise %.2f drift %.1f: beyond noise in %.1f%% of parts", noise, drift, rate*100)
			}
		}
	}
}
