// Package term holds the terminal report's drawing primitives: the palette
// and styles (ansi.go), display width, truncation and wrapping (width.go),
// the sanitizer for captured text (sanitize.go), text bars and sparklines
// (bars.go), boxes and rules (box.go) and column layout (layout.go).
//
// With colour off every primitive writes plain text, byte for byte what the
// TypeScript version wrote, so the golden files under test/golden/ hold for
// both. With colour on the only escape sequences are SGR (ESC [ ... m) and,
// from Hyperlink, the one OSC 8 link the report writes itself; captured text
// must go through Clean (or CleanDeep / CleanLine) before it is drawn, so it
// can never carry escapes of its own.
//
// # Making a styled context
//
// The renderer keeps what every panel needs in one context (Ctx in
// terminal.ts). Build its parts like this:
//
//	// Colour: decide the profile once, from the --color / --no-color flag,
//	// whether stdout is a terminal, and the environment.
//	profile := term.DetectProfile(isTTY, os.Environ(), term.ColorAuto)
//	p := term.NewPaint(profile) // term.PlainPaint() for colour off
//
//	// ASCII mode: no box drawing or block characters, "~" for a cut.
//	chars := term.CharsetFor(ascii)                  // or term.CharsetFor(ascii, term.StyleDouble)
//	ell := term.Ellipsis
//	if ascii {
//		ell = "~"
//	}
//
//	// Width: the caller clamps the terminal's columns (40..140 for the
//	// report) and passes it to Box, Rule, LayoutColumns, Fit and Wrap.
//
//	// Hyperlinks: only on a colour terminal, where escape sequences are
//	// wanted, and never in ASCII mode.
//	links := p.Color && !ascii
//	if links {
//		text = term.Hyperlink(url, text)
//	}
//
// In tests, term.NewPaint(colorprofile.TrueColor) is the TypeScript's
// paint(true) on a truecolor terminal, and term.PlainPaint() its paint(false).
package term
