package term

import (
	"regexp"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestHue(t *testing.T) {
	if got := PlainPaint().Hue(TreatmentHues[0], "x"); got != "x" {
		t.Errorf("colour off: %q", got)
	}
	sgr := regexp.MustCompile(`^\x1b\[[0-9;]*mx\x1b\[0m$`)
	for _, profile := range []colorprofile.Profile{colorprofile.ANSI, colorprofile.ANSI256, colorprofile.TrueColor} {
		p := NewPaint(profile)
		for _, hex := range TreatmentHues {
			if got := p.Hue(hex, "x"); !sgr.MatchString(got) {
				t.Errorf("%v %s: %q", profile, hex, got)
			}
		}
	}
	if got := NewPaint(colorprofile.TrueColor).Hue("#7FA7E0", "x"); got != "\x1b[38;2;127;167;224mx\x1b[0m" {
		t.Errorf("truecolor: %q", got)
	}
	// Distinct treatments get distinct colours.
	seen := map[string]bool{}
	for _, hex := range TreatmentHues {
		seen[NewPaint(colorprofile.ANSI256).Hue(hex, "x")] = true
	}
	if len(seen) != len(TreatmentHues) {
		t.Errorf("only %d distinct 256-colour hues", len(seen))
	}
}
