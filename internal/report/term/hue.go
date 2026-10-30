package term

import (
	"image/color"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// TreatmentHues are the colours treatments take in order (A, B, C, ...): the
// brand green first, then hues clear of the design system's alert colours, so
// a colour names a treatment and never reads as good or bad.
var TreatmentHues = []string{Design.Green300, Design.Lime700, "#7FA7E0", "#E3B25B", "#B79CE6", "#5FC4D0"}

type hueKey struct {
	profile colorprofile.Profile
	hex     string
}

var hueOpeners sync.Map // hueKey -> string

// Hue is text in a foreground colour (a "#rrggbb" hex), for this paint's
// colour profile; the identity when colour is off. Like every other style it
// writes SGR sequences only.
func (p Paint) Hue(hex, text string) string {
	if !p.Color {
		return text
	}
	key := hueKey{p.Profile, hex}
	open, ok := hueOpeners.Load(key)
	if !ok {
		var c color.Color
		switch {
		case p.Profile >= colorprofile.TrueColor:
			c = lipgloss.Color(hex)
		case p.Profile == colorprofile.ANSI256:
			c = lipgloss.ANSIColor(Nearest256(hex))
		case p.Profile == colorprofile.ANSI:
			c = p.Profile.Convert(lipgloss.Color(hex))
		}
		st := lipgloss.NewStyle()
		if c != nil {
			st = st.Foreground(c)
		}
		open, _ = hueOpeners.LoadOrStore(key, opener(st))
	}
	return sgr(open.(string), text)
}
