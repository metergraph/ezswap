package term

// Captured text (prompts, outputs, errors, labels, file names) is untrusted:
// a model can return an escape sequence that clears the screen, moves the
// cursor, retitles the window or rewrites what the report shows. Every string
// the terminal report prints passes through Clean first, which turns C0 and
// C1 controls into visible caret notation (ESC becomes "^["), normalizes line
// breaks and shows bidirectional overrides as code points, so the only
// escape sequences on screen are the ones the report writes itself.
//
// Go strings can hold bytes that are not UTF-8 (JavaScript strings cannot);
// each such byte becomes U+FFFD first, so a raw 0x9B can never reach a
// terminal that reads it as an 8-bit CSI.

import (
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

func bidi(r rune) bool {
	return r == 0x061c || r == 0x200e || r == 0x200f ||
		(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}

func caret(cp rune) string {
	if cp == 0x7f {
		return "^?"
	}
	if cp < 0x20 {
		return "^" + string(rune(cp+0x40))
	}
	return fmt.Sprintf("<%02X>", cp)
}

// Clean neutralizes controls. Newlines survive (CR LF, CR, U+2028 and U+2029
// become "\n"); tabs become two spaces; every other C0 or C1 control, and
// DEL, becomes caret notation.
func Clean(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		i += size
		switch {
		case r == '\r':
			if i < len(text) && text[i] == '\n' {
				i++
			}
			b.WriteByte('\n')
		case r == 0x2028 || r == 0x2029:
			b.WriteByte('\n')
		case r == '\t':
			b.WriteString("  ")
		case r == '\n':
			b.WriteByte('\n')
		case r < 0x20 || (r >= 0x7f && r <= 0x9f):
			b.WriteString(caret(r))
		case bidi(r):
			fmt.Fprintf(&b, "<U+%X>", r)
		default:
			// An invalid byte decodes as U+FFFD (RuneError), written as such.
			b.WriteRune(r)
		}
	}
	return b.String()
}

// jsSpace is JavaScript's \s: the characters String.prototype.trim removes.
func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// CleanLine is Clean, then one line: runs of whitespace (newlines included)
// become one space.
func CleanLine(text string) string {
	c := Clean(text)
	var b strings.Builder
	b.Grow(len(c))
	space := false
	for _, r := range c {
		if jsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// ToAscii keeps printable ASCII only: the report's own glyphs mapped to
// look-alikes, anything else "?". As when it matched UTF-16 code units, a character outside the Basic Multilingual Plane (most emoji)
// becomes "??". ESC and newlines pass through.
func ToAscii(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch r {
		case '−', '–', '—':
			b.WriteByte('-')
		case '×':
			b.WriteByte('x')
		case '…':
			b.WriteString("...")
		case '‘', '’':
			b.WriteByte('\'')
		case '“', '”':
			b.WriteByte('"')
		case '→':
			b.WriteString("->")
		case '←':
			b.WriteString("<-")
		case '↑':
			b.WriteByte('^')
		case '↓':
			b.WriteByte('v')
		case '≈':
			b.WriteByte('~')
		case '·':
			b.WriteByte('-')
		case '›':
			b.WriteByte('>')
		case 0xa0:
			b.WriteByte(' ')
		default:
			switch {
			case r == 0x1b || r == '\n' || (r >= 0x20 && r <= 0x7e):
				b.WriteRune(r)
			case r > 0xffff:
				b.WriteString("??")
			default:
				b.WriteByte('?')
			}
		}
	}
	return b.String()
}

// CleanDeep is a copy of value with every string cleaned (and made ASCII
// when asked). Numbers, booleans and structure are untouched, so figures
// read the same. It walks structs (exported fields), pointers, slices,
// arrays, interfaces and map values; map keys are left as they are. Shared or cyclic pointers stay shared in the
// copy.
func CleanDeep[T any](value T, ascii bool) T {
	c := &deepCleaner{ascii: ascii, seen: map[seenKey]reflect.Value{}}
	v := reflect.ValueOf(&value).Elem()
	out := reflect.New(v.Type()).Elem()
	out.Set(c.walk(v))
	return out.Interface().(T)
}

type deepCleaner struct {
	ascii bool
	seen  map[seenKey]reflect.Value
}

// A struct and its first field share an address, so a pointer is known by
// its type as well.
type seenKey struct {
	t reflect.Type
	p uintptr
}

func (c *deepCleaner) str(s string) string {
	s = Clean(s)
	if c.ascii {
		s = ToAscii(s)
	}
	return s
}

func (c *deepCleaner) walk(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.String:
		out := reflect.New(v.Type()).Elem()
		out.SetString(c.str(v.String()))
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		key := seenKey{v.Type(), v.Pointer()}
		if known, ok := c.seen[key]; ok {
			return known
		}
		out := reflect.New(v.Type().Elem())
		c.seen[key] = out
		out.Elem().Set(c.walk(v.Elem()))
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(c.walk(v.Elem()))
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(c.walk(v.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := range v.Len() {
			out.Index(i).Set(c.walk(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), c.walk(iter.Value()))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v) // unexported fields are copied as they are
		for i := range v.NumField() {
			if out.Field(i).CanSet() {
				out.Field(i).Set(c.walk(v.Field(i)))
			}
		}
		return out
	}
	return v
}
