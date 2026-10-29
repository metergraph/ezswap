package jsnum

import "testing"

// Expected values were produced by Node.js.
func TestString(t *testing.T) {
	cases := map[float64]string{
		0: "0", 1: "1", -1.5: "-1.5", 0.30000000000000004: "0.30000000000000004", 1e21: "1e+21",
		123456789012345680000: "123456789012345680000", 1e-7: "1e-7", 0.000001: "0.000001",
		1.5e-10: "1.5e-10", 250000: "250000", 9e-7: "9e-7", 0.0000085: "0.0000085",
	}
	for x, want := range cases {
		if got := String(x); got != want {
			t.Errorf("String(%v) = %q, want %q", x, got, want)
		}
	}
}

func TestRound(t *testing.T) {
	cases := [][2]float64{{2.5, 3}, {-2.5, -2}, {0.49999999999999994, 0}, {-0.5, 0}, {1.4, 1}, {-1.6, -2}}
	for _, c := range cases {
		if got := Round(c[0]); got != c[1] {
			t.Errorf("Round(%v) = %v, want %v", c[0], got, c[1])
		}
	}
}

func TestToFixed(t *testing.T) {
	cases := []struct {
		x    float64
		d    int
		want string
	}{
		{0.5, 0, "1"}, {2.5, 0, "3"}, {1.005, 2, "1.00"}, {1.45, 1, "1.4"}, {0.125, 2, "0.13"},
		{-0.0001, 2, "-0.00"}, {0, 2, "0.00"}, {123.456, 1, "123.5"}, {0.000061, 6, "0.000061"},
		{1e21, 2, "1e+21"}, {-1.5, 0, "-2"}, {8.54, 2, "8.54"},
	}
	for _, c := range cases {
		if got := ToFixed(c.x, c.d); got != c.want {
			t.Errorf("ToFixed(%v, %d) = %q, want %q", c.x, c.d, got, c.want)
		}
	}
}

func TestToPrecision(t *testing.T) {
	cases := []struct {
		x    float64
		p    int
		want string
	}{
		{0.000034, 2, "0.000034"}, {123.456, 2, "1.2e+2"}, {99.96, 3, "100"}, {9.995, 3, "9.99"},
		{0.00000012, 2, "1.2e-7"}, {1, 3, "1.00"}, {0, 2, "0.0"}, {35.47, 4, "35.47"}, {-0.00125, 2, "-0.0013"},
		{0.0000048, 2, "0.0000048"}, {1234.5, 4, "1235"},
	}
	for _, c := range cases {
		if got := ToPrecision(c.x, c.p); got != c.want {
			t.Errorf("ToPrecision(%v, %d) = %q, want %q", c.x, c.p, got, c.want)
		}
	}
}

func TestToExponential(t *testing.T) {
	if got := ToExponential(123.456, 2); got != "1.23e+2" {
		t.Errorf("got %q", got)
	}
	if got := ToExponential(0.00015, 0); got != "1e-4" {
		t.Errorf("got %q", got)
	}
	cases := map[float64]string{0.0000025: "2.5e-6", 1e21: "1e+21", 12: "1.2e+1", 0: "0e+0", 0.30000000000000004: "3.0000000000000004e-1"}
	for x, want := range cases {
		if got := ToExponentialShortest(x); got != want {
			t.Errorf("ToExponentialShortest(%v) = %q, want %q", x, got, want)
		}
	}
}

func TestGroupInt(t *testing.T) {
	cases := map[float64]string{0: "0", 999: "999", 1000: "1,000", 250000: "250,000", 1234567.6: "1,234,568", -4096: "-4,096"}
	for x, want := range cases {
		if got := GroupInt(x); got != want {
			t.Errorf("GroupInt(%v) = %q, want %q", x, got, want)
		}
	}
}
