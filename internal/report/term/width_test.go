package term

// Widths, cuts and wraps for the characters the report draws or may be
// handed, with the values the TypeScript (Intl.Segmenter and its width
// table) computed for the same input.

import "testing"

func TestWidthMatchesTheTypeScript(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"日本語のテキスト", 16},
		{"한국어 문장입니다", 17},
		{"ｆｕｌｌｗｉｄｔｈ", 18},
		{"é ä ñ", 5},
		{"🙂 👩‍💻 ❤️ 🇯🇵 👍🏽", 13},
		{"█▏▎▍▌▋▊▉ ─│╭╮╰╯├┤ ━┃┆●·", 23},
		{"┏━┓┃ ┃┗━┛╺┓╹╻╱═║╔╗╚╝╟╢", 22},
		{"▁▂▃▄▅▆▇█", 8},
		{"​‍zero️", 5},
		{"a\u00a0b\u3000c\ufeffd  e", 10},
		{"ⅰⅱ ¼ ☃ ⌚ ⏰ ♈ ⚡ ✅ ⭐ 〜 ㄱ ꀀ 가 豈 ︐ ﹰ ｡ ￠ 𠀀 𖿠", 55},
		// An Indic conjunct is one cluster (Unicode 15.1, GB9c), as in Intl.Segmenter.
		{"กิ क्ष אָ", 5},
		{"\x1b[1mbold\x1b[0m plain \x1b[38;2;1;2;3mcol\x1b[0m", 14},
	}
	for _, c := range cases {
		eq(t, Width(c.text), c.want, c.text)
	}
	eq(t, CodePointWidth(0x1b), 0)
	eq(t, CodePointWidth(0x9b), 0)
	eq(t, CodePointWidth(0x0301), 0)
	eq(t, CodePointWidth('─'), 1)
	eq(t, CodePointWidth('█'), 1)
	eq(t, CodePointWidth('中'), 2)
	eq(t, CodePointWidth(0x1f642), 2)
	eq(t, CodePointWidth(0xe0100), 0)
}

func TestTruncateNeverSplitsAClusterAndMatchesTheTypeScript(t *testing.T) {
	emoji := "🙂 👩‍💻 ❤️ 🇯🇵 👍🏽"
	eq(t, Truncate(emoji, 2), "…")
	eq(t, Truncate(emoji, 2, ""), "🙂")
	eq(t, Truncate(emoji, 3), "🙂…")
	eq(t, Truncate(emoji, 5, ""), "🙂 👩‍💻")
	eq(t, Truncate(emoji, 8, "~"), "🙂 👩‍💻 ~")
	eq(t, Truncate("ééé", 2), "é…")
	eq(t, Truncate("\x1b[1mbold\x1b[0m plain", 6), "\x1b[1mbold\x1b[0m \x1b[0m…")
	eq(t, Truncate("abc", 1, "……"), "", "an ellipsis wider than the room is dropped")
}

func TestWrapMatchesTheTypeScript(t *testing.T) {
	cjk := "日本語のテキスト"
	eq(t, Wrap(cjk, 2), []string{"日", "本", "語", "の", "テ", "キ", "ス", "ト"})
	eq(t, Wrap(cjk, 5), []string{"日本", "語の", "テキ", "スト"})
	eq(t, Wrap(cjk, 10), []string{"日本語のテ", "キスト"})
	emoji := "🙂 👩‍💻 ❤️ 🇯🇵 👍🏽"
	eq(t, Wrap(emoji, 5), []string{"🙂 👩‍💻", "❤️ 🇯🇵", "👍🏽"})
	eq(t, Wrap("", 10), []string{""})
	eq(t, Wrap("a  b", 10), []string{"a b"})
}

func TestWrapOfAWideCharacterInOneColumnEnds(t *testing.T) {
	// The TypeScript loops forever here; the port takes the cluster alone.
	eq(t, Wrap("日本", 1), []string{"日", "本"})
}

func TestToAsciiAndCleanLineMatchTheTypeScript(t *testing.T) {
	eq(t, ToAscii("−5 × 2 → … ‘q’ “d” ← ↑ ↓ ≈ · › ✓ 😀 é"), `-5 x 2 -> ... 'q' "d" <- ^ v ~ - > ? ?? ?`)
	eq(t, ToAscii("🙂 👩‍💻"), "?? ?????", "astral characters are two UTF-16 units, so two marks")
	eq(t, ToAscii("█─│╭·"), "????-")
	eq(t, ToAscii("a\x1b[1mb\nc\td\u00a0e"), "a\x1b[1mb\nc?d e")
	eq(t, CleanLine("a\u00a0b\u3000c\ufeffd  e"), "a b c d e")
	eq(t, CleanLine("  \n  "), "")
	eq(t, Clean("tab\there\r\nline\x00\x7f\u009b‮⁦x y"), "tab  here\nline^@^?<9B><U+202E><U+2066>x\ny")
	eq(t, Clean("a\rb\r\n\r\nc "), "a\nb\n\nc\n")
}

func TestCleanDeepWalksMapsPointersAndInterfaces(t *testing.T) {
	type node struct {
		Name  string
		Next  *node
		Tags  map[string]string
		Any   any
		Arr   [2]string
		Bytes []byte
		n     string
	}
	shared := &node{Name: "\x1b[2J"}
	in := node{Name: "€\x07", Next: shared, Tags: map[string]string{"\x1bk": "\x1bv"}, Any: []any{"\x07", 3.5, true, nil}, Arr: [2]string{"\x00", "ok"}, Bytes: []byte{0x1b}, n: "\x1b"}
	in.Next.Next = shared // a cycle
	out := CleanDeep(in, true)
	eq(t, out.Name, "?^G")
	eq(t, out.Next.Name, "^[[2J")
	if out.Next.Next != out.Next {
		t.Error("a cycle stays a cycle")
	}
	eq(t, out.Tags, map[string]string{"\x1bk": "^[v"}, "keys untouched, values cleaned")
	eq(t, out.Any, any([]any{"^G", 3.5, true, nil}))
	eq(t, out.Arr, [2]string{"^@", "ok"})
	eq(t, out.Bytes, []byte{0x1b}, "bytes are not text")
	eq(t, out.n, "\x1b", "unexported fields are copied, not cleaned")
	eq(t, in.Name, "€\x07", "the input is untouched")
	eq(t, CleanDeep("\x1b", false), "^[")
	eq(t, CleanDeep[*node](nil, false), (*node)(nil))
}
