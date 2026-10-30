package term

// Display width of terminal text: ANSI sequences take no columns, East Asian
// wide characters and emoji take two, combining marks take none. Plus the
// width-aware helpers built on it: truncation with an ellipsis, padding and
// word wrap.
//
// Grapheme clusters come from x/ansi (its uax29 segmenter follows the same
// extended grapheme cluster rules as Intl.Segmenter, including Indic
// conjuncts, which uniseg 0.4 splits). A cluster's width is computed here
// rather than taken from a width library, from the same table the
// TypeScript used, so every layout comes out column for column the same.

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

var (
	ansiRe      = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	ansiAtStart = regexp.MustCompile(`^(?:\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\))`)
)

func printableASCII(text string) bool {
	for i := 0; i < len(text); i++ {
		if c := text[i]; c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

// StripAnsi removes CSI and OSC sequences.
func StripAnsi(text string) string {
	return ansiRe.ReplaceAllString(text, "")
}

// Inclusive ranges of code points a terminal draws two columns wide.
var wide = [][2]rune{
	{0x1100, 0x115f},
	{0x231a, 0x231b},
	{0x2329, 0x232a},
	{0x23e9, 0x23ec},
	{0x23f0, 0x23f0},
	{0x23f3, 0x23f3},
	{0x25fd, 0x25fe},
	{0x2614, 0x2615},
	{0x2648, 0x2653},
	{0x267f, 0x267f},
	{0x2693, 0x2693},
	{0x26a1, 0x26a1},
	{0x26aa, 0x26ab},
	{0x26bd, 0x26be},
	{0x26c4, 0x26c5},
	{0x26ce, 0x26ce},
	{0x26d4, 0x26d4},
	{0x26ea, 0x26ea},
	{0x26f2, 0x26f3},
	{0x26f5, 0x26f5},
	{0x26fa, 0x26fa},
	{0x26fd, 0x26fd},
	{0x2705, 0x2705},
	{0x270a, 0x270b},
	{0x2728, 0x2728},
	{0x274c, 0x274c},
	{0x274e, 0x274e},
	{0x2753, 0x2755},
	{0x2757, 0x2757},
	{0x2795, 0x2797},
	{0x27b0, 0x27b0},
	{0x27bf, 0x27bf},
	{0x2b1b, 0x2b1c},
	{0x2b50, 0x2b50},
	{0x2b55, 0x2b55},
	{0x2e80, 0x303e},
	{0x3041, 0x33ff},
	{0x3400, 0x4dbf},
	{0x4e00, 0x9fff},
	{0xa000, 0xa4cf},
	{0xa960, 0xa97f},
	{0xac00, 0xd7a3},
	{0xf900, 0xfaff},
	{0xfe10, 0xfe19},
	{0xfe30, 0xfe6f},
	{0xff00, 0xff60},
	{0xffe0, 0xffe6},
	{0x16fe0, 0x16fe4},
	{0x17000, 0x18cff},
	{0x1b000, 0x1b2ff},
	{0x1f004, 0x1f004},
	{0x1f0cf, 0x1f0cf},
	{0x1f18e, 0x1f18e},
	{0x1f191, 0x1f19a},
	{0x1f200, 0x1f251},
	{0x1f300, 0x1f320},
	{0x1f32d, 0x1f335},
	{0x1f337, 0x1f37c},
	{0x1f37e, 0x1f393},
	{0x1f3a0, 0x1f3ca},
	{0x1f3cf, 0x1f3d3},
	{0x1f3e0, 0x1f3f0},
	{0x1f3f4, 0x1f3f4},
	{0x1f3f8, 0x1f43e},
	{0x1f440, 0x1f440},
	{0x1f442, 0x1f4fc},
	{0x1f4ff, 0x1f53d},
	{0x1f54b, 0x1f54e},
	{0x1f550, 0x1f567},
	{0x1f57a, 0x1f57a},
	{0x1f595, 0x1f596},
	{0x1f5a4, 0x1f5a4},
	{0x1f5fb, 0x1f64f},
	{0x1f680, 0x1f6c5},
	{0x1f6cc, 0x1f6cc},
	{0x1f6d0, 0x1f6d2},
	{0x1f6d5, 0x1f6d7},
	{0x1f6dc, 0x1f6df},
	{0x1f6eb, 0x1f6ec},
	{0x1f6f4, 0x1f6fc},
	{0x1f7e0, 0x1f7eb},
	{0x1f7f0, 0x1f7f0},
	{0x1f90c, 0x1f93a},
	{0x1f93c, 0x1f945},
	{0x1f947, 0x1f9ff},
	{0x1fa70, 0x1faff},
	{0x20000, 0x2fffd},
	{0x30000, 0x3fffd},
}

func inRanges(cp rune, ranges [][2]rune) bool {
	lo, hi := 0, len(ranges)-1
	for lo <= hi {
		mid := (lo + hi) >> 1
		a, b := ranges[mid][0], ranges[mid][1]
		switch {
		case cp < a:
			hi = mid - 1
		case cp > b:
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}

// Nonspacing and enclosing marks, format characters, the zero-width spaces
// and joiners (U+200B..U+200F) and the variation selectors (U+FE00..U+FE0F).
func zeroWidth(cp rune) bool {
	return unicode.In(cp, unicode.Mn, unicode.Me, unicode.Cf) ||
		(cp >= 0x200b && cp <= 0x200f) || (cp >= 0xfe00 && cp <= 0xfe0f)
}

// CodePointWidth is the columns one code point takes: 0, 1 or 2. Controls
// count 0; they are neutralized before they get here.
func CodePointWidth(cp rune) int {
	if cp < 0x20 || (cp >= 0x7f && cp < 0xa0) {
		return 0
	}
	if cp < 0x300 {
		return 1
	}
	if zeroWidth(cp) {
		return 0
	}
	if cp >= 0xe0100 && cp <= 0xe01ef {
		return 0
	}
	if inRanges(cp, wide) {
		return 2
	}
	return 1
}

// clusterWidth is the columns one grapheme cluster takes: its first visible
// code point's, two for an emoji presentation.
func clusterWidth(cluster string) int {
	w := 0
	for _, r := range cluster {
		if cw := CodePointWidth(r); cw > 0 {
			w = cw
			break
		}
	}
	if w == 1 && strings.ContainsRune(cluster, 0xfe0f) {
		return 2
	}
	return w
}

func graphemes(text string) []string {
	if printableASCII(text) {
		out := make([]string, len(text))
		for i := range len(text) {
			out[i] = text[i : i+1]
		}
		return out
	}
	var out []string
	for len(text) > 0 {
		cluster, _ := ansi.FirstGraphemeCluster(text, ansi.GraphemeWidth)
		if cluster == "" {
			// Never stall on input the segmenter cannot split.
			cluster = text[:1]
		}
		out = append(out, cluster)
		text = text[len(cluster):]
	}
	return out
}

// Width is the visible width in columns, ignoring ANSI sequences.
func Width(text string) int {
	if printableASCII(text) {
		return len(text)
	}
	plain := StripAnsi(text)
	if printableASCII(plain) {
		return len(plain)
	}
	total := 0
	for _, g := range graphemes(plain) {
		total += clusterWidth(g)
	}
	return total
}

// Ellipsis is what Truncate and Fit end a cut with unless told otherwise.
const Ellipsis = "…"

func ellipsisOr(ellipsis []string) string {
	if len(ellipsis) > 0 {
		return ellipsis[0]
	}
	return Ellipsis
}

// Truncate cuts text to at most max columns, ending with the ellipsis (by
// default "…"; pass one to override, "" for none) when cut. ANSI sequences
// pass through, and a style left open is reset at the cut.
func Truncate(text string, max int, ellipsis ...string) string {
	ell := ellipsisOr(ellipsis)
	if max <= 0 {
		return ""
	}
	if Width(text) <= max {
		return text
	}
	room := max - Width(ell)
	if room < 0 {
		room = 0
	}
	var out strings.Builder
	used := 0
	styled := false
	rest := text
	for len(rest) > 0 {
		if loc := ansiAtStart.FindStringIndex(rest); loc != nil {
			out.WriteString(rest[:loc[1]])
			styled = true
			rest = rest[loc[1]:]
			continue
		}
		var chunk string
		switch next := strings.IndexByte(rest, 0x1b); next {
		case -1:
			chunk = rest
		case 0:
			chunk = rest[:1]
		default:
			chunk = rest[:next]
		}
		rest = rest[len(chunk):]
		stop := false
		for _, g := range graphemes(chunk) {
			w := clusterWidth(g)
			if used+w > room {
				stop = true
				break
			}
			out.WriteString(g)
			used += w
		}
		if stop {
			break
		}
	}
	if styled {
		out.WriteString(Reset)
	}
	if Width(ell) <= max {
		out.WriteString(ell)
	}
	return out.String()
}

// Align is where Fit places text in its columns.
type Align string

const (
	AlignLeft   Align = "left"
	AlignRight  Align = "right"
	AlignCenter Align = "center"
)

// Fit pads (or cuts) text to exactly cols columns. An empty align is left.
// The optional ellipsis is as for Truncate.
func Fit(text string, cols int, align Align, ellipsis ...string) string {
	cut := Truncate(text, cols, ellipsisOr(ellipsis))
	gap := cols - Width(cut)
	if gap < 0 {
		gap = 0
	}
	switch align {
	case AlignRight:
		return strings.Repeat(" ", gap) + cut
	case AlignCenter:
		return strings.Repeat(" ", gap/2) + cut + strings.Repeat(" ", gap-gap/2)
	}
	return cut + strings.Repeat(" ", gap)
}

// PadEnd pads on the right to at least cols columns; never cuts.
func PadEnd(text string, cols int) string {
	return text + strings.Repeat(" ", max(0, cols-Width(text)))
}

// PadStart pads on the left to at least cols columns; never cuts.
func PadStart(text string, cols int) string {
	return strings.Repeat(" ", max(0, cols-Width(text))) + text
}

// Wrap word-wraps plain text (no ANSI) to cols columns. Words longer than a
// line are broken. Existing newlines start new lines; leading indentation on
// each source line is kept on its continuation lines.
func Wrap(text string, cols int) []string {
	limit := max(1, cols)
	out := []string{}
	for _, source := range strings.Split(text, "\n") {
		lead := len(source) - len(strings.TrimLeft(source, " "))
		indent := source[:min(lead, max(0, limit-1))]
		var words []string
		for _, w := range strings.Split(source[len(indent):], " ") {
			if w != "" {
				words = append(words, w)
			}
		}
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := indent
		lineWidth := len(indent)
		push := func() {
			out = append(out, strings.TrimRight(line, " "))
			line = indent
			lineWidth = len(indent)
		}
		for _, word := range words {
			w := Width(word)
			sep := 0
			if lineWidth > len(indent) {
				sep = 1
			}
			if lineWidth+sep+w <= limit {
				if sep == 1 {
					line += " "
				}
				line += word
				lineWidth += sep + w
				continue
			}
			if lineWidth > len(indent) {
				push()
			}
			for w > limit-len(indent) {
				head := Truncate(word, limit-len(indent), "")
				if head == "" {
					// A cluster wider than the whole line (a wide character
					// in one column): take it alone rather than loop forever.
					head = graphemes(word)[0]
				}
				out = append(out, indent+head)
				word = word[len(head):]
				w = Width(word)
			}
			if word != "" {
				line = indent + word
				lineWidth = len(indent) + w
			}
		}
		if lineWidth > len(indent) {
			push()
		}
	}
	return out
}
