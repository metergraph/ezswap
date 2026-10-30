package term

// Ported from test/term.test.ts.

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

var p = NewPaint(colorprofile.TrueColor)

func f(v float64) *float64 { return &v }

func eq[T any](t *testing.T, got, want T, msg ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", strings.Join(msg, " "), got, want)
	}
}

func TestWidthIgnoresAnsiAndCountsWideAndCombining(t *testing.T) {
	eq(t, Width("hello"), 5)
	eq(t, Width(p.Bold(p.Treat("hello"))), 5)
	eq(t, Width("\x1b]8;;https://example.com\x07link\x1b]8;;\x07"), 4)
	eq(t, Width("日本語"), 6)
	eq(t, Width("한국"), 4)
	eq(t, Width("ｈｉ"), 4)
	eq(t, Width("é"), 1, "combining acute")
	eq(t, Width("🙂"), 2)
	eq(t, Width("👩‍💻"), 2, "a ZWJ sequence is one cluster")
	eq(t, Width("❤️"), 2, "emoji presentation selector")
	eq(t, Width("█▏─│╭"), 5)
	eq(t, Width(""), 0)
	eq(t, StripAnsi(p.Reverse("x")), "x")
}

func TestTruncateCutsWithEllipsisAndKeepsStylesClosed(t *testing.T) {
	eq(t, Truncate("abcdef", 4), "abc…")
	eq(t, Truncate("abcdef", 4, "~"), "abc~")
	eq(t, Truncate("abc", 3), "abc")
	eq(t, Truncate("abc", 0), "")
	eq(t, Truncate("日本語です", 5), "日本…")
	eq(t, Width(Truncate("日本語です", 4)), 3, "never splits a wide character")
	styled := Truncate(p.Bold("abcdef"), 4)
	eq(t, StripAnsi(styled), "abc…")
	if !strings.HasSuffix(styled, "\x1b[0m…") {
		t.Errorf("an open style is reset at the cut: %q", styled)
	}
	eq(t, Fit("ab", 4, AlignRight), "  ab")
	eq(t, Fit("ab", 5, AlignCenter), " ab  ")
	eq(t, Fit("abcdef", 4, AlignLeft), "abc…")
	eq(t, strings.HasPrefix(PadStart(p.Bold("ab"), 4), "  "), true)
}

func TestWrapBreaksOnSpacesBreaksLongWordsKeepsIndentation(t *testing.T) {
	eq(t, Wrap("the quick brown fox jumps", 10), []string{"the quick", "brown fox", "jumps"})
	eq(t, Wrap("abcdefghij", 4), []string{"abcd", "efgh", "ij"})
	eq(t, Wrap("  indented text that wraps", 12), []string{"  indented", "  text that", "  wraps"})
	eq(t, Wrap("one\n\ntwo", 10), []string{"one", "", "two"})
	for _, line := range Wrap("日本語の文章をここで折り返します 長い", 7) {
		if Width(line) > 7 {
			t.Errorf("too wide: %q", line)
		}
	}
}

func TestHbarResolvesEighthsInUnicodeAndWholeCellsInASCII(t *testing.T) {
	eq(t, Hbar(1, 1, 8, false), "████████")
	eq(t, Hbar(0.5, 1, 8, false), "████")
	eq(t, Hbar(1, 16, 2, false), "▏", "one eighth")
	eq(t, Hbar(0.5625, 1, 8, false), "████▌")
	eq(t, Hbar(0.0001, 1, 8, false), "▏", "a positive value never renders empty")
	eq(t, Hbar(0, 1, 8, false), "")
	eq(t, Hbar(0.5, 1, 8, true), "####")
	eq(t, Hbar(2, 1, 4, true), "####", "clamped to the width")
	eq(t, Hbar(1, 0, 4, false), "")
}

func plain(s []Segment) string {
	var b strings.Builder
	for _, x := range s {
		b.WriteString(x.Text)
	}
	return b.String()
}

func TestAgreementBarsTickTheFloorOnTheSameScale(t *testing.T) {
	eq(t, plain(AgreementBar(f(0.5), f(1), 10, false)), "█████····┆")
	eq(t, plain(AgreementBar(f(1), f(1), 10, false)), "█████████┃", "a tick inside the fill is heavy")
	eq(t, plain(AgreementBar(f(0.5), f(0.3), 10, true)), "##|##.....")
	eq(t, plain(AgreementBar(nil, f(0.5), 4, true)), ".|..")
	for _, ascii := range []bool{true, false} {
		eq(t, Width(plain(AgreementBar(f(0.37), f(0.81), 23, ascii))), 23)
	}
	painted := PaintSegments(AgreementBar(f(0.5), f(1), 10, false), map[SegmentKind]func(string) string{KindFill: p.Treat, KindTick: p.Floor})
	eq(t, StripAnsi(painted), "█████····┆")
}

func TestRangeBarsPlaceRangeAndMedianOnASharedAxis(t *testing.T) {
	eq(t, plain(RangeBar(f(2), f(5), f(8), 0, 10, 11, false)), "──━━━●━━━──")
	eq(t, plain(RangeBar(f(2), f(5), f(8), 0, 10, 11, true)), "--===o===--")
	eq(t, plain(RangeBar(nil, f(0), nil, 0, 10, 5, true)), "o----")
	eq(t, Width(plain(RangeBar(f(-5), f(100), f(200), 0, 10, 9, false))), 9, "clamped to the axis")
}

func TestSparklinesAndHistograms(t *testing.T) {
	eq(t, Sparkline([]float64{0, 1, 2, 4, 8}, false), "▁▂▃▅█")
	eq(t, Sparkline([]float64{0, 4, 8}, true), " =#")
	eq(t, Sparkline(nil, false), "")
	eq(t, Histogram([]float64{0, 1, 2, 9, 10}, 2, 0, 10), []int{3, 2})
}

func TestBigNumeralsCoverWhatACostPrintsAndRefuseAnythingElse(t *testing.T) {
	rows, ok := BigText("$8.54")
	if !ok {
		t.Fatal("no rows")
	}
	if Width(rows[0]) != Width(rows[1]) || Width(rows[1]) != Width(rows[2]) {
		t.Errorf("the three rows are one width: %q", rows)
	}
	if _, ok := BigText("$1,234 50%"); !ok {
		t.Error("$1,234 50% has glyphs")
	}
	if _, ok := BigText("abc"); ok {
		t.Error("abc has no glyphs")
	}
}

func TestBoxesAreExactlyAsWideAsAsked(t *testing.T) {
	for _, chars := range []Charset{Rounded, ASCIIBox} {
		for _, w := range []int{10, 40, 80} {
			lines := Box([]string{"short", "a line much longer than the box can hold, which is cut", Divider, p.Bold("styled")}, BoxOptions{
				Width: w, Chars: chars, Paint: p, Title: "A title that is long", Note: "a note",
			})
			for _, line := range lines {
				if Width(line) != w {
					t.Errorf("width %d != %d: %q", Width(line), w, line)
				}
			}
		}
	}
	unicode := Box([]string{"hi"}, BoxOptions{Width: 12, Chars: Rounded, Paint: PlainPaint(), Title: "T"})
	eq(t, unicode, []string{"╭─ T ──────╮", "│ hi       │", "╰──────────╯"})
	ascii := Box([]string{"hi"}, BoxOptions{Width: 12, Chars: ASCIIBox, Paint: PlainPaint(), Title: "T", Note: "n"})
	eq(t, ascii, []string{"+- T ------+", "| hi       |", "+----------+"}, "a note is dropped when it does not fit")
	eq(t, Box([]string{Divider}, BoxOptions{Width: 6, Chars: Rounded, Paint: PlainPaint()})[1], "├────┤")
	eq(t, Width(Rule(30, Rounded, p, "Input")), 30)
}

func TestLayoutDropsOptionalColumnsFirstAndGivesTheRestToTheFlexibleOne(t *testing.T) {
	rows := []map[string]string{{"a": "name", "bar": "", "c": "12345", "opt": "optional text"}}
	wide := LayoutColumns([]ColumnSpec{{ID: "a"}, {ID: "bar", Flex: true, Min: Cols(4), Max: Cols(10)}, {ID: "c", Align: AlignRight}, {ID: "opt", Drop: 1}}, rows, 60)
	var got [][2]any
	for _, c := range wide.Columns {
		got = append(got, [2]any{c.ID, c.Width})
	}
	eq(t, got, [][2]any{{"a", 4}, {"bar", 10}, {"c", 5}, {"opt", 13}})
	narrow := LayoutColumns([]ColumnSpec{{ID: "a"}, {ID: "bar", Flex: true, Min: Cols(4)}, {ID: "c", Align: AlignRight}, {ID: "opt", Drop: 1}}, rows, 20)
	var ids []string
	for _, c := range narrow.Columns {
		ids = append(ids, c.ID)
	}
	eq(t, ids, []string{"a", "bar", "c"})
	if w := Width(RenderRow(narrow, rows[0])); w > 20 {
		t.Errorf("row is %d wide", w)
	}
	eq(t, Has(narrow, "opt"), false)
	eq(t, WidthOf(narrow, "c"), 5)
	eq(t, OffsetOf(narrow, "c"), 4+2+WidthOf(narrow, "bar")+2)
}

var controls = regexp.MustCompile("[\x00-\x08\x0b-\x1f\x7f]|[\u0080-\u009f]")

func TestCapturedControlsAreNeutralized(t *testing.T) {
	hostile := "a\x1b[2Jb\x1b]0;pwned\x07c\u009b31md\re\r\nf‮g\x00h\x7fi\tj k"
	out := Clean(hostile)
	if controls.MatchString(out) {
		t.Errorf("a control survived: %q", out)
	}
	eq(t, out, "a^[[2Jb^[]0;pwned^Gc<9B>31md\ne\nf<U+202E>g^@h^?i  j\nk")
	eq(t, CleanLine("one\n two\x1b[A"), "one two^[[A")
	type inner struct{ C string }
	type outer struct {
		A []string
		N int
		B inner
	}
	deep := CleanDeep(outer{A: []string{"\x1b[31mred"}, N: 3, B: inner{C: "\x07"}}, false)
	eq(t, deep, outer{A: []string{"^[[31mred"}, N: 3, B: inner{C: "^G"}})
	eq(t, ToAscii("−5 pts × 2 → … ✓"), "-5 pts x 2 -> ... ?")
}
