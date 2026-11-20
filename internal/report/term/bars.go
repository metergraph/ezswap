package term

// Bars drawn with text: horizontal bars to an eighth of a column, an
// agreement bar with the noise floor ticked on the same scale, range bars on
// a shared axis, sparklines, and big three-row numerals for the one figure
// worth reading from across the room.

import (
	"math"
	"strings"

	"github.com/metergraph/ezswap/internal/jsnum"
)

var eighths = [8]string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}

const full = "█"

func clamp01(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Min(1, math.Max(0, v))
}

// Hbar is a bar cols columns long for value of max, as filled cells only (no
// padding). Unicode resolves an eighth of a column; ASCII a whole one. A
// positive value never renders empty.
func Hbar(value, max float64, cols int, ascii bool) string {
	if !(cols > 0) || !(max > 0) || !(value > 0) {
		return ""
	}
	share := clamp01(value / max)
	if ascii {
		n := int(math.Max(1, jsnum.Round(share*float64(cols))))
		return strings.Repeat("#", n)
	}
	e := int(math.Max(1, jsnum.Round(share*float64(cols)*8)))
	return strings.Repeat(full, e/8) + eighths[e%8]
}

// Position is the cell index (0..cols-1) a share of the scale falls in.
func Position(share float64, cols int) int {
	return min(cols-1, max(0, int(jsnum.Round(clamp01(share)*float64(cols-1)))))
}

// SegmentKind is what part of a bar a segment draws.
type SegmentKind string

const (
	KindFill  SegmentKind = "fill"
	KindTrack SegmentKind = "track"
	KindTick  SegmentKind = "tick"
	KindRange SegmentKind = "range"
	KindMark  SegmentKind = "mark"
)

// Segment is a run of cells of one kind.
type Segment struct {
	Text string
	Kind SegmentKind
}

// AgreementBar is an agreement bar: value (0..1) filled, the rest a dotted
// track, and the noise floor's own level ticked on the same scale. A nil
// value draws no fill; a nil floor no tick.
func AgreementBar(value, floor *float64, cols int, ascii bool) []Segment {
	if cols <= 0 {
		return []Segment{}
	}
	fill := ""
	if value != nil {
		fill = Hbar(*value, 1, cols, ascii)
	}
	var cells []string
	var kinds []SegmentKind
	for _, r := range fill {
		cells = append(cells, string(r))
		kinds = append(kinds, KindFill)
	}
	track := "·"
	if ascii {
		track = "."
	}
	for len(cells) < cols {
		cells = append(cells, track)
		kinds = append(kinds, KindTrack)
	}
	if floor != nil && !math.IsNaN(*floor) && !math.IsInf(*floor, 0) {
		// Ticks sit at the right edge of the cell the floor falls in, so 100% is the last cell.
		at := min(cols-1, max(0, int(math.Ceil(clamp01(*floor)*float64(cols)))-1))
		switch {
		case ascii:
			cells[at] = "|"
		case kinds[at] == KindFill:
			cells[at] = "┃"
		default:
			cells[at] = "┆"
		}
		kinds[at] = KindTick
	}
	return merge(cells, kinds)
}

// RangeBar is a range bar on an axis from min to max: a track, the range
// from lo to hi drawn heavier, the median as a dot. A nil lo or hi draws no
// range; a nil mid no dot.
func RangeBar(lo, mid, hi *float64, min, max float64, cols int, ascii bool) []Segment {
	if cols <= 0 {
		return []Segment{}
	}
	span := max - min
	if span == 0 || math.IsNaN(span) {
		span = 1
	}
	share := func(v float64) float64 { return (v - min) / span }
	cells := make([]string, cols)
	kinds := make([]SegmentKind, cols)
	for i := range cells {
		cells[i] = "─"
		if ascii {
			cells[i] = "-"
		}
		kinds[i] = KindTrack
	}
	if lo != nil && hi != nil {
		a := Position(share(*lo), cols)
		b := Position(share(*hi), cols)
		for i := minInt(a, b); i <= maxInt(a, b); i++ {
			cells[i] = "━"
			if ascii {
				cells[i] = "="
			}
			kinds[i] = KindRange
		}
	}
	if mid != nil {
		m := Position(share(*mid), cols)
		cells[m] = "●"
		if ascii {
			cells[m] = "o"
		}
		kinds[m] = KindMark
	}
	return merge(cells, kinds)
}

// The builtins are shadowed by RangeBar's min and max parameters.
func minInt(a, b int) int { return min(a, b) }
func maxInt(a, b int) int { return max(a, b) }

func merge(cells []string, kinds []SegmentKind) []Segment {
	out := []Segment{}
	for i, text := range cells {
		if n := len(out); n > 0 && out[n-1].Kind == kinds[i] {
			out[n-1].Text += text
		} else {
			out = append(out, Segment{Text: text, Kind: kinds[i]})
		}
	}
	return out
}

// PaintSegments joins segments, styling each by kind (unstyled when the kind
// has no style).
func PaintSegments(segments []Segment, style map[SegmentKind]func(string) string) string {
	var b strings.Builder
	for _, s := range segments {
		if f := style[s.Kind]; f != nil {
			b.WriteString(f(s.Text))
		} else {
			b.WriteString(s.Text)
		}
	}
	return b.String()
}

var (
	sparks      = []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	asciiSparks = []string{" ", ".", ":", "-", "=", "+", "*", "#"}
)

// Sparkline is one character per value, scaled to the largest. Zero counts
// draw the lowest mark (Unicode) or a space (ASCII).
func Sparkline(values []float64, ascii bool) string {
	marks := sparks
	lowest := 0.0
	if ascii {
		marks = asciiSparks
		lowest = 1
	}
	top := 0.0
	for _, v := range values {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			top = math.Max(top, v)
		}
	}
	var b strings.Builder
	for _, v := range values {
		if !(top > 0) || !(v > 0) {
			if ascii {
				b.WriteString(" ")
			} else {
				b.WriteString(marks[0])
			}
			continue
		}
		last := float64(len(marks) - 1)
		i := math.Min(last, math.Max(lowest, jsnum.Round((v/top)*last)))
		b.WriteString(marks[int(i)])
	}
	return b.String()
}

// Histogram is the counts of values in bins equal bins from min to max.
func Histogram(values []float64, bins int, min, max float64) []int {
	counts := make([]int, maxInt(1, bins))
	span := max - min
	if span == 0 || math.IsNaN(span) {
		span = 1
	}
	n := float64(len(counts))
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		i := math.Min(n-1, math.Max(0, math.Floor(((v-min)/span)*n)))
		if math.IsNaN(i) {
			continue
		}
		counts[int(i)]++
	}
	return counts
}

// Three-row numerals from box-drawing pieces.
var big = map[rune][3]string{
	'0': {"┏━┓", "┃ ┃", "┗━┛"},
	'1': {"╺┓ ", " ┃ ", "╺┻╸"},
	'2': {"╺━┓", "┏━┛", "┗━╸"},
	'3': {"╺━┓", " ━┫", "╺━┛"},
	'4': {"╻ ╻", "┗━┫", "  ╹"},
	'5': {"┏━╸", "┗━┓", "╺━┛"},
	'6': {"┏━╸", "┣━┓", "┗━┛"},
	'7': {"╺━┓", "  ┃", "  ╹"},
	'8': {"┏━┓", "┣━┫", "┗━┛"},
	'9': {"┏━┓", "┗━┫", "╺━┛"},
	'$': {"┏╋╸", "┗╋┓", "╺╋┛"},
	'.': {" ", " ", "╹"},
	',': {" ", " ", "┛"},
	'%': {"╻ ╱", " ╱ ", "╱ ╹"},
	' ': {" ", " ", " "},
}

// BigText is text as three rows of big numerals; ok is false when it holds a
// character the font lacks.
func BigText(text string) (rows [3]string, ok bool) {
	for _, ch := range text {
		glyph, found := big[ch]
		if !found {
			return [3]string{}, false
		}
		for i := range 3 {
			if rows[i] != "" {
				rows[i] += " "
			}
			rows[i] += glyph[i]
		}
	}
	return rows, true
}
