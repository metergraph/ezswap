package adapters

import (
	"testing"
	"time"
)

// Expected values are what `new Date(s).toISOString()` printed in Node with
// TZ=America/Los_Angeles ("" for an invalid date).
func TestParseDateMatchesV8(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no time zone database")
	}
	saved := time.Local
	time.Local = la
	defer func() { time.Local = saved }()
	for _, tc := range []struct{ in, want string }{
		{"2026-03-04", "2026-03-04T00:00:00.000Z"},
		{"2026-03-04T12:30:00Z", "2026-03-04T12:30:00.000Z"},
		{"2026-03-04T12:30:00.123456Z", "2026-03-04T12:30:00.123Z"},
		{"2026-03-04 12:30:00", "2026-03-04T20:30:00.000Z"},
		{"2026-03-04T12:30", "2026-03-04T20:30:00.000Z"},
		{"2026-02-30", "2026-03-02T00:00:00.000Z"},
		{"2026-03-04T24:00:00Z", "2026-03-05T00:00:00.000Z"},
		{"2026-03-04T12:30:00Z ", ""},
		{"+012026-03-04T00:00:00Z", "+012026-03-04T00:00:00.000Z"},
		{"-000000-01-01T00:00:00Z", ""},
		{"Mar 4 2026", "2026-03-04T08:00:00.000Z"},
		{"Wed, 04 Mar 2026 12:30:00 GMT", "2026-03-04T12:30:00.000Z"},
		{"Wed Mar 04 2026 12:30:00 GMT-0800 (Pacific Standard Time)", "2026-03-04T20:30:00.000Z"},
		{"Mar 4 2026 +0530", ""},
		{"Mar 4 2026 10:00 PM", "2026-03-05T06:00:00.000Z"},
		{"Mar 4", "2001-03-04T08:00:00.000Z"},
		{"3/4/2026", "2026-03-04T08:00:00.000Z"},
		{"2026/03/04 10:00", "2026-03-04T18:00:00.000Z"},
		{"2026-03-08 02:30", "2026-03-08T10:30:00.000Z"}, // in the spring-forward gap
		{"1772528400", ""},
		{"", ""},
	} {
		got := ""
		if ms, ok := parseDate(tc.in); ok {
			got = isoString(ms)
		}
		if got != tc.want {
			t.Errorf("parseDate(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsoTimeFromEpochUnits(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want any
	}{
		{"1772528400", "2026-03-03T09:00:00.000Z"},
		{1772528400.25, "2026-03-03T09:00:00.250Z"},
		{"1772618400000000000", "2026-03-04T10:00:00.000Z"},
		{1772528400123.0, "2026-03-03T09:00:00.123Z"},
		{0.0, nil},
		{-5.0, nil},
		{true, nil},
	} {
		got := IsoTime(tc.in)
		var g any
		if got != nil {
			g = *got
		}
		if g != tc.want {
			t.Errorf("IsoTime(%v) = %v, want %v", tc.in, g, tc.want)
		}
	}
}
