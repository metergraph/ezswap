package term

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

// What the TypeScript paint(true, truecolor) wrote around "x", per style.
var tsTruecolor = map[string]string{
	"bold": "\x1b[1mx\x1b[0m", "dim": "\x1b[2mx\x1b[0m", "italic": "\x1b[3mx\x1b[0m",
	"underline": "\x1b[4mx\x1b[0m", "reverse": "\x1b[7mx\x1b[0m",
	"prod": "\x1b[38;2;177;177;176mx\x1b[0m", "treat": "\x1b[38;2;96;172;152mx\x1b[0m",
	"floor": "\x1b[38;2;97;96;96mx\x1b[0m", "muted": "\x1b[2mx\x1b[0m",
	"line": "\x1b[38;2;97;96;96mx\x1b[0m", "brand": "\x1b[1;38;2;96;172;152mx\x1b[0m",
	"emph":      "\x1b[1mx\x1b[0m",
	"highlight": "\x1b[1;38;2;17;17;17;48;2;199;240;106mx\x1b[0m",
	"tab":       "\x1b[1;38;2;255;255;255;48;2;24;97;78mx\x1b[0m",
	"select":    "\x1b[38;2;255;255;255;48;2;24;97;78mx\x1b[0m",
	"bar":       "\x1b[38;2;240;239;238;48;2;53;53;52mx\x1b[0m",
}

// And with the 256-colour palette.
var ts256 = map[string]string{
	"prod": "\x1b[38;5;249mx\x1b[0m", "treat": "\x1b[38;5;72mx\x1b[0m", "floor": "\x1b[38;5;59mx\x1b[0m",
	"line": "\x1b[38;5;59mx\x1b[0m", "brand": "\x1b[1;38;5;72mx\x1b[0m",
	"highlight": "\x1b[1;38;5;233;48;5;191mx\x1b[0m", "tab": "\x1b[1;38;5;231;48;5;23mx\x1b[0m",
	"select": "\x1b[38;5;231;48;5;23mx\x1b[0m", "bar": "\x1b[38;5;255;48;5;236mx\x1b[0m",
}

func styles(p Paint) map[string]func(string) string {
	return map[string]func(string) string{
		"bold": p.Bold, "dim": p.Dim, "italic": p.Italic, "underline": p.Underline, "reverse": p.Reverse,
		"prod": p.Prod, "treat": p.Treat, "floor": p.Floor, "muted": p.Muted, "line": p.Line,
		"brand": p.Brand, "emph": p.Emph, "highlight": p.Highlight, "tab": p.Tab, "select": p.Select, "bar": p.Bar,
	}
}

var sgrOnly = regexp.MustCompile(`^\x1b\[[0-9;]*m$`)
var anyEscape = regexp.MustCompile(`\x1b[^m]*m?`)

// The one place Lip Gloss writes a different (equivalent) sequence: it sets
// underline twice, as on and as the single style.
const lipglossUnderline = "\x1b[4;4mx\x1b[0m"

func TestPaletteMatchesTheTypeScriptInTruecolorAnd256(t *testing.T) {
	tsTruecolor["underline"] = lipglossUnderline
	for name, fn := range styles(NewPaint(colorprofile.TrueColor)) {
		eq(t, fn("x"), tsTruecolor[name], "truecolor", name)
	}
	for name, fn := range styles(NewPaint(colorprofile.ANSI256)) {
		want, ok := ts256[name]
		if !ok {
			want = tsTruecolor[name] // attribute-only styles do not change
		}
		eq(t, fn("x"), want, "256", name)
	}
	eq(t, Nearest256(Design.Green300), 72)
	eq(t, Nearest256(Design.Lime700), 191)
	eq(t, Nearest256(Design.Grey600), 233)
	eq(t, Nearest256(Design.White), 231)
}

func TestEveryProfileWritesOnlySGR(t *testing.T) {
	for _, prof := range []colorprofile.Profile{colorprofile.TrueColor, colorprofile.ANSI256, colorprofile.ANSI, colorprofile.ASCII} {
		pp := NewPaint(prof)
		if !pp.Color {
			t.Errorf("%v: colour should be on", prof)
		}
		for name, fn := range styles(pp) {
			out := fn("a\nb\tc")
			for _, e := range anyEscape.FindAllString(out, -1) {
				if !sgrOnly.MatchString(e) {
					t.Errorf("%v %s: %q", prof, name, e)
				}
			}
			eq(t, StripAnsi(out), "a\nb\tc", "text untouched", prof.String(), name)
		}
	}
	// ASCII: attributes, no hues.
	eq(t, NewPaint(colorprofile.ASCII).Treat("x"), "x")
	eq(t, NewPaint(colorprofile.ASCII).Bold("x"), "\x1b[1mx\x1b[0m")
	// 16 colours: a basic colour code.
	if got := NewPaint(colorprofile.ANSI).Treat("x"); !regexp.MustCompile(`^\x1b\[(3[0-7]|9[0-7])mx\x1b\[0m$`).MatchString(got) {
		t.Errorf("16-colour treat: %q", got)
	}
}

func TestColourOffIsTheIdentity(t *testing.T) {
	for _, pp := range []Paint{PlainPaint(), NewPaint(colorprofile.NoTTY), NewPaint(colorprofile.Unknown)} {
		if pp.Color {
			t.Error("colour should be off")
		}
		for name, fn := range styles(pp) {
			eq(t, fn("a\x1b[0mb"), "a\x1b[0mb", name)
		}
	}
}

func TestNestedStylesReopenTheOuterAfterAnInnerReset(t *testing.T) {
	// As the TypeScript wrote it.
	eq(t, p.Bold(p.Treat("a")+"b"), "\x1b[1m\x1b[38;2;96;172;152ma\x1b[0m\x1b[1mb\x1b[0m")
	eq(t, p.Bold(""), "")
	eq(t, RoleColor(p, RoleProduction)("x"), p.Prod("x"))
	eq(t, RoleColor(p, RoleTreatment)("x"), p.Treat("x"))
	eq(t, RoleColor(p, RoleFloor)("x"), p.Floor("x"))
}

func TestCleanedTextCannotInjectEscapesThroughPaint(t *testing.T) {
	hostile := "\x1b[2J\x1b]0;title\x07\x1b]8;;https://evil.example.com\x1b\\x\x1b]8;;\x1b\\\u009b2J\x1bc\xff\x9b"
	for _, prof := range []colorprofile.Profile{colorprofile.TrueColor, colorprofile.ANSI256, colorprofile.NoTTY} {
		pp := NewPaint(prof)
		for name, fn := range styles(pp) {
			out := fn(Clean(hostile))
			for _, e := range anyEscape.FindAllString(out, -1) {
				if !sgrOnly.MatchString(e) {
					t.Errorf("%v %s: escape %q survived", prof, name, e)
				}
			}
			if controls.MatchString(StripAnsi(out)) {
				t.Errorf("%v %s: control survived in %q", prof, name, out)
			}
		}
	}
	eq(t, Clean("\xff\x9b"), "��", "bytes that are not UTF-8 become U+FFFD")
}

func TestHyperlinkIsOSC8WithST(t *testing.T) {
	url := "https://www.metergraph.dev/replay?utm_source=replay#setup=abc"
	link := Hyperlink(url, "metergraph.dev/replay")
	eq(t, link, "\x1b]8;;"+url+"\x1b\\metergraph.dev/replay\x1b]8;;\x1b\\")
	eq(t, Width(link), len("metergraph.dev/replay"))
	// As in the TypeScript, a cut stops before the closing OSC 8: the report
	// never cuts its link (the link line is exempt from the width limit).
	eq(t, Truncate(link, 10), "\x1b]8;;"+url+"\x1b\\metergrap\x1b[0m…")
	if !strings.HasPrefix(link, "\x1b]8;;") {
		t.Error("opens with OSC 8")
	}
}

func env(kv ...string) []string { return kv }

func TestDetectProfile(t *testing.T) {
	cases := []struct {
		name string
		tty  bool
		env  []string
		mode ColorMode
		want colorprofile.Profile
	}{
		{"no-color wins", true, env("TERM=xterm-256color", "COLORTERM=truecolor"), ColorNever, colorprofile.NoTTY},
		{"piped", false, env("TERM=xterm-256color"), ColorAuto, colorprofile.NoTTY},
		{"dumb", true, env("TERM=dumb"), ColorAuto, colorprofile.NoTTY},
		{"NO_COLOR", true, env("TERM=xterm-256color", "NO_COLOR=1"), ColorAuto, colorprofile.NoTTY},
		{"empty NO_COLOR is unset", true, env("TERM=xterm-256color", "NO_COLOR="), ColorAuto, colorprofile.ANSI256},
		{"COLORTERM truecolor", true, env("TERM=xterm-256color", "COLORTERM=truecolor"), ColorAuto, colorprofile.TrueColor},
		{"COLORTERM 24bit", true, env("TERM=screen", "COLORTERM=24BIT"), ColorAuto, colorprofile.TrueColor},
		{"256 from TERM", true, env("TERM=xterm-256color"), ColorAuto, colorprofile.ANSI256},
		{"16 from TERM", true, env("TERM=xterm"), ColorAuto, colorprofile.ANSI},
		{"--color, piped, NO_COLOR", false, env("TERM=xterm-256color", "NO_COLOR=1"), ColorAlways, colorprofile.ANSI256},
		{"--color, no TERM: the TypeScript's 256", false, env(), ColorAlways, colorprofile.ANSI256},
		{"--color, dumb", false, env("TERM=dumb"), ColorAlways, colorprofile.ANSI256},
	}
	for _, c := range cases {
		eq(t, DetectProfile(c.tty, c.env, c.mode), c.want, c.name)
	}
	eq(t, SupportsTruecolor(env("COLORTERM=truecolor")), true)
	eq(t, SupportsTruecolor(env("COLORTERM=yes")), false)
}
