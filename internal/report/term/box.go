package term

// Boxes and panels: a border around lines that are fitted to the inner width,
// so a box is always exactly as wide as asked and never wraps.
//
// Drawn by hand rather than with Lip Gloss borders so the uncoloured output
// is byte for byte stable (the golden files pin it).

import "strings"

// Charset is the glyphs a box is drawn with.
type Charset struct {
	TL, TR, BL, BR, H, V string
	// T-junctions for a divider inside a box.
	LT, RT string
}

var (
	Rounded     = Charset{TL: "╭", TR: "╮", BL: "╰", BR: "╯", H: "─", V: "│", LT: "├", RT: "┤"}
	Double      = Charset{TL: "╔", TR: "╗", BL: "╚", BR: "╝", H: "═", V: "║", LT: "╟", RT: "╢"}
	ASCIIBox    = Charset{TL: "+", TR: "+", BL: "+", BR: "+", H: "-", V: "|", LT: "+", RT: "+"}
	ASCIIDouble = Charset{TL: "+", TR: "+", BL: "+", BR: "+", H: "=", V: "|", LT: "+", RT: "+"}
)

// BoxStyle picks the rounded or double border.
type BoxStyle string

const (
	StyleRounded BoxStyle = "rounded"
	StyleDouble  BoxStyle = "double"
)

// CharsetFor is the charset for ASCII or Unicode output; style is optional
// (rounded by default).
func CharsetFor(ascii bool, style ...BoxStyle) Charset {
	double := len(style) > 0 && style[0] == StyleDouble
	if ascii {
		if double {
			return ASCIIDouble
		}
		return ASCIIBox
	}
	if double {
		return Double
	}
	return Rounded
}

// BoxOptions configures Box.
type BoxOptions struct {
	Width int
	Chars Charset
	Paint Paint
	// Shown in the top border at the left.
	Title string
	// Shown in the top border at the right, muted.
	Note string
	// Columns of space inside each side; nil means 1.
	Padding *int
	// Style for the border; nil means the muted line colour.
	Border func(string) string
	// What a cut line ends with; empty means "…".
	Ellipsis string
}

// Divider is the divider line inside a box: pass it as a content line to draw ├────┤.
const Divider = "\x00divider"

// Box draws lines inside a border exactly options.Width columns wide.
func Box(lines []string, options BoxOptions) []string {
	c, p := options.Chars, options.Paint
	total := max(4, options.Width)
	pad := 1
	if options.Padding != nil {
		pad = *options.Padding
	}
	inner := total - 2 - pad*2
	border := options.Border
	if border == nil {
		border = p.Line
	}
	ellipsis := options.Ellipsis
	if ellipsis == "" {
		ellipsis = Ellipsis
	}

	var top string
	used := 1
	if options.Title != "" {
		room := total - 6
		title := Truncate(options.Title, max(1, room), ellipsis)
		top = border(c.TL+c.H+" ") + p.Bold(title) + border(" ")
		used = 3 + Width(title) + 1
	} else {
		top = border(c.TL)
	}
	noteText := ""
	if options.Note != "" {
		if room := total - used - 5; room >= 8 {
			noteText = Truncate(options.Note, room, ellipsis)
		}
	}
	fillWidth := total - used - 1
	if noteText != "" {
		fillWidth -= Width(noteText) + 3
	}
	top += border(strings.Repeat(c.H, max(0, fillWidth)))
	if noteText != "" {
		top += border(" ") + p.Muted(noteText) + border(" "+c.H)
	}
	top += border(c.TR)

	out := []string{top}
	side := border(c.V)
	space := strings.Repeat(" ", max(0, pad))
	for _, line := range lines {
		if line == Divider {
			out = append(out, border(c.LT+strings.Repeat(c.H, total-2)+c.RT))
			continue
		}
		out = append(out, side+space+Fit(line, inner, AlignLeft, ellipsis)+space+side)
	}
	out = append(out, border(c.BL+strings.Repeat(c.H, total-2)+c.BR))
	return out
}

// Rule is a horizontal rule cols wide, optionally with a label: "── Label ──────".
func Rule(cols int, chars Charset, p Paint, label ...string) string {
	if len(label) == 0 || label[0] == "" {
		return p.Line(strings.Repeat(chars.H, max(0, cols)))
	}
	text := Truncate(label[0], max(1, cols-4))
	return p.Line(strings.Repeat(chars.H, 2)) + " " + p.Bold(text) + " " + p.Line(strings.Repeat(chars.H, max(0, cols-Width(text)-4)))
}
