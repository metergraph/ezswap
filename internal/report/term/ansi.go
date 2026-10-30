package term

// ANSI styles and the report's palette, from the Metergraph design system.
// Colour marks what a row is, never whether it is good: production is the
// neutral grey the product gives its control, every treatment shares the
// brand green, the noise floor is a darker grey. A difference beyond noise is
// emphasis (dark ink on the brand lime), never the system's alert colours.
//
// Colours are 24-bit where the terminal says it supports them (COLORTERM),
// else the nearest of the 256 standard terminal colours, else the 16 basic
// ones when that is all the terminal claims. Each style is a Lip Gloss style;
// colour-profile detection and downsampling come from colorprofile.

import (
	"image/color"
	"regexp"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// Reset is SGR 0: every attribute off.
const Reset = "\x1b[0m"

// Design holds the Metergraph design system colours the report uses.
var Design = struct {
	Green300, Green500, Green700, Lime700             string
	Grey50, Grey300, Grey400, Grey500, Grey600, White string
}{
	Green300: "#60AC98",
	Green500: "#468171",
	Green700: "#18614E",
	Lime700:  "#C7F06A",
	Grey50:   "#F0EFEE",
	Grey300:  "#B1B1B0",
	Grey400:  "#616060",
	Grey500:  "#353534",
	Grey600:  "#111111",
	White:    "#FFFFFF",
}

// AlertColors are the design system's verdict colours. The report never uses them.
var AlertColors = struct{ Good, Bad string }{Good: "#0EAA45", Bad: "#D04526"}

type rgbTriple [3]int

func rgb(hex string) rgbTriple {
	n, _ := strconv.ParseInt(hex[1:], 16, 64)
	return rgbTriple{int(n>>16) & 255, int(n>>8) & 255, int(n) & 255}
}

var cube = [6]int{0, 95, 135, 175, 215, 255}

// Nearest256 is the nearest colour in the xterm 256-colour palette (the 6x6x6 cube and the grey ramp).
func Nearest256(hex string) int {
	c := rgb(hex)
	r, g, b := c[0], c[1], c[2]
	distance := func(x rgbTriple) int {
		return (x[0]-r)*(x[0]-r) + (x[1]-g)*(x[1]-g) + (x[2]-b)*(x[2]-b)
	}
	abs := func(v int) int {
		if v < 0 {
			return -v
		}
		return v
	}
	step := func(v int) int {
		best := 0
		for i, c := range cube {
			if abs(c-v) < abs(cube[best]-v) {
				best = i
			}
		}
		return best
	}
	ri, gi, bi := step(r), step(g), step(b)
	bestIndex := 16 + 36*ri + 6*gi + bi
	bestD := distance(rgbTriple{cube[ri], cube[gi], cube[bi]})
	for i := 0; i < 24; i++ {
		v := 8 + 10*i
		if d := distance(rgbTriple{v, v, v}); d < bestD {
			bestIndex, bestD = 232+i, d
		}
	}
	return bestIndex
}

var truecolorRe = regexp.MustCompile(`(?i)^(truecolor|24bit)$`)

// SupportsTruecolor reports whether the terminal advertises 24-bit colour
// (COLORTERM). env is in os.Environ form ("KEY=value").
func SupportsTruecolor(env []string) bool {
	return truecolorRe.MatchString(lookupEnv(env, "COLORTERM"))
}

func lookupEnv(env []string, key string) string {
	value := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			value = v // the last assignment wins, as in a process environment
		}
	}
	return value
}

// ColorMode is the --color / --no-color choice the CLI was given.
type ColorMode int

const (
	// ColorAuto colours a terminal unless NO_COLOR is set or TERM is dumb.
	ColorAuto ColorMode = iota
	// ColorAlways is --color: colour even when piped or NO_COLOR is set.
	ColorAlways
	// ColorNever is --no-color.
	ColorNever
)

// DetectProfile decides how much colour to write. isTTY says whether stdout
// is a terminal; env is in os.Environ form.
//
// Whether to colour at all follows the CLI: --no-color wins, then --color,
// then a terminal that is not TERM=dumb and has no (non-empty) NO_COLOR. How
// many colours: 24-bit when COLORTERM says truecolor or 24bit; otherwise what
// colorprofile reads from TERM and friends (256 or 16); when nothing in the
// environment says, the 256-colour palette.
// NoTTY means no styling at all.
func DetectProfile(isTTY bool, env []string, mode ColorMode) colorprofile.Profile {
	switch mode {
	case ColorNever:
		return colorprofile.NoTTY
	case ColorAuto:
		if !isTTY || lookupEnv(env, "TERM") == "dumb" || lookupEnv(env, "NO_COLOR") != "" {
			return colorprofile.NoTTY
		}
	}
	if SupportsTruecolor(env) {
		return colorprofile.TrueColor
	}
	// NO_COLOR already decided on/off above; colorprofile would otherwise
	// turn it into "attributes only".
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		if !strings.HasPrefix(kv, "NO_COLOR=") {
			filtered = append(filtered, kv)
		}
	}
	if p := colorprofile.Env(filtered); p >= colorprofile.ANSI {
		return p
	}
	return colorprofile.ANSI256
}

// Paint styles report text. With Color false every method is the identity,
// so uncoloured output is exactly the text given. Methods are safe to pass as
// func(string) string values (p.Treat, p.Floor, ...).
type Paint struct {
	// Color is false when nothing is styled.
	Color bool
	// Profile is the colour profile the styles were built for.
	Profile colorprofile.Profile
	open    [numSlots]string
}

type slot int

const (
	slotBold slot = iota
	slotDim
	slotItalic
	slotUnderline
	slotReverse
	slotProd
	slotTreat
	slotFloor
	slotMuted
	slotLine
	slotBrand
	slotEmph
	slotHighlight
	slotTab
	slotSelect
	slotBar
	numSlots
)

// NewPaint is a painter for a colour profile: real styles for ANSI, ANSI256
// and TrueColor, attributes without hues for ASCII, the identity for NoTTY
// (and Unknown).
func NewPaint(profile colorprofile.Profile) Paint {
	if profile <= colorprofile.NoTTY {
		return Paint{Color: false, Profile: colorprofile.NoTTY}
	}
	col := func(hex string) color.Color {
		switch {
		case profile >= colorprofile.TrueColor:
			return lipgloss.Color(hex)
		case profile == colorprofile.ANSI256:
			return lipgloss.ANSIColor(Nearest256(hex))
		case profile == colorprofile.ANSI:
			return profile.Convert(lipgloss.Color(hex))
		}
		return nil
	}
	s := lipgloss.NewStyle
	fg := func(st lipgloss.Style, hex string) lipgloss.Style {
		if c := col(hex); c != nil {
			return st.Foreground(c)
		}
		return st
	}
	bg := func(st lipgloss.Style, hex string) lipgloss.Style {
		if c := col(hex); c != nil {
			return st.Background(c)
		}
		return st
	}
	styles := [numSlots]lipgloss.Style{
		slotBold:      s().Bold(true),
		slotDim:       s().Faint(true),
		slotItalic:    s().Italic(true),
		slotUnderline: s().Underline(true),
		slotReverse:   s().Reverse(true),
		slotProd:      fg(s(), Design.Grey300),
		slotTreat:     fg(s(), Design.Green300),
		slotFloor:     fg(s(), Design.Grey400),
		// Secondary copy uses the terminal's own dim, so it reads on dark and light backgrounds alike.
		slotMuted:     s().Faint(true),
		slotLine:      fg(s(), Design.Grey400),
		slotBrand:     fg(s().Bold(true), Design.Green300),
		slotEmph:      s().Bold(true),
		slotHighlight: bg(fg(s().Bold(true), Design.Grey600), Design.Lime700),
		slotTab:       bg(fg(s().Bold(true), Design.White), Design.Green700),
		slotSelect:    bg(fg(s(), Design.White), Design.Green700),
		slotBar:       bg(fg(s(), Design.Grey50), Design.Grey500),
	}
	p := Paint{Color: true, Profile: profile}
	for i, st := range styles {
		p.open[i] = opener(st)
	}
	return p
}

// PlainPaint is the identity painter: colour off.
func PlainPaint() Paint { return NewPaint(colorprofile.NoTTY) }

// opener is the SGR sequence a Lip Gloss style opens with. Rendering through
// Lip Gloss directly would pad multi-line text, convert tabs and, for
// underline, style each rune on its own (splitting any escape sequence
// inside); the report only wants the style's SGR around its text, so take
// the opening sequence once and apply it with sgr.
func opener(st lipgloss.Style) string {
	out := st.Render("x")
	open, rest, ok := strings.Cut(out, "x")
	if !ok || (rest != Reset && rest != "\x1b[m") {
		return ""
	}
	return open
}

// sgr wraps text in a style, reopening it after any reset inside so nested
// styles keep the outer one.
func sgr(open, text string) string {
	if text == "" || open == "" {
		return text
	}
	return open + strings.ReplaceAll(text, Reset, Reset+open) + Reset
}

func (p Paint) apply(s slot, text string) string {
	if !p.Color {
		return text
	}
	return sgr(p.open[s], text)
}

func (p Paint) Bold(text string) string      { return p.apply(slotBold, text) }
func (p Paint) Dim(text string) string       { return p.apply(slotDim, text) }
func (p Paint) Italic(text string) string    { return p.apply(slotItalic, text) }
func (p Paint) Underline(text string) string { return p.apply(slotUnderline, text) }
func (p Paint) Reverse(text string) string   { return p.apply(slotReverse, text) }

// Prod is production's colour.
func (p Paint) Prod(text string) string { return p.apply(slotProd, text) }

// Treat is the accent every treatment shares.
func (p Paint) Treat(text string) string { return p.apply(slotTreat, text) }

// Floor is the noise floor: production against itself.
func (p Paint) Floor(text string) string { return p.apply(slotFloor, text) }

// Muted is secondary copy: labels, units, rules.
func (p Paint) Muted(text string) string { return p.apply(slotMuted, text) }

// Line is box borders and rules.
func (p Paint) Line(text string) string { return p.apply(slotLine, text) }

// Brand is the wordmark and headings.
func (p Paint) Brand(text string) string { return p.apply(slotBrand, text) }

// Emph is emphasis for a magnitude beyond noise: bold, no hue.
func (p Paint) Emph(text string) string { return p.apply(slotEmph, text) }

// Highlight is the brand lime badge: a label such as "beyond noise".
func (p Paint) Highlight(text string) string { return p.apply(slotHighlight, text) }

// Tab is the explorer's active tab.
func (p Paint) Tab(text string) string { return p.apply(slotTab, text) }

// Select is the selected row in the explorer.
func (p Paint) Select(text string) string { return p.apply(slotSelect, text) }

// Bar is the status bar and the active tab.
func (p Paint) Bar(text string) string { return p.apply(slotBar, text) }

// Role is which palette slot a row's arm takes.
type Role string

const (
	RoleProduction Role = "production"
	RoleTreatment  Role = "treatment"
	RoleFloor      Role = "floor"
)

// RoleColor is the style for a role.
func RoleColor(p Paint, role Role) func(string) string {
	switch role {
	case RoleProduction:
		return p.Prod
	case RoleTreatment:
		return p.Treat
	}
	return p.Floor
}

// Hyperlink is text that opens url when clicked, in terminals that support
// OSC 8 (others show the text). Terminated with ST (ESC \). Emit it only when colour is on and not in ASCII mode,
// and only for a URL the report built itself (the hosted link): never for
// captured text.
func Hyperlink(url, text string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}
