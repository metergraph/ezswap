import { test } from "node:test";
import assert from "node:assert/strict";
import { paint } from "../src/report/term/ansi.ts";
import { agreementBar, bigText, hbar, histogram, paintSegments, rangeBar, sparkline } from "../src/report/term/bars.ts";
import { ASCII_BOX, DIVIDER, ROUNDED, box, rule } from "../src/report/term/box.ts";
import { layout, renderRow } from "../src/report/term/layout.ts";
import { clean, cleanDeep, cleanLine, toAscii } from "../src/report/term/sanitize.ts";
import { fit, padStart, stripAnsi, truncate, width, wrap } from "../src/report/term/width.ts";

const p = paint(true);

test("width ignores ANSI and counts wide and combining characters", () => {
  assert.equal(width("hello"), 5);
  assert.equal(width(p.bold(p.treat("hello"))), 5);
  assert.equal(width("\x1b]8;;https://example.com\x07link\x1b]8;;\x07"), 4);
  assert.equal(width("日本語"), 6);
  assert.equal(width("한국"), 4);
  assert.equal(width("ｈｉ"), 4);
  assert.equal(width("é"), 1, "combining acute");
  assert.equal(width("🙂"), 2);
  assert.equal(width("👩‍💻"), 2, "a ZWJ sequence is one cluster");
  assert.equal(width("❤️"), 2, "emoji presentation selector");
  assert.equal(width("█▏─│╭"), 5);
  assert.equal(width(""), 0);
  assert.equal(stripAnsi(p.reverse("x")), "x");
});

test("truncate cuts to a width with an ellipsis and keeps styles closed", () => {
  assert.equal(truncate("abcdef", 4), "abc…");
  assert.equal(truncate("abcdef", 4, "~"), "abc~");
  assert.equal(truncate("abc", 3), "abc");
  assert.equal(truncate("abc", 0), "");
  assert.equal(truncate("日本語です", 5), "日本…");
  assert.equal(width(truncate("日本語です", 4)), 3, "never splits a wide character");
  const styled = truncate(p.bold("abcdef"), 4);
  assert.equal(stripAnsi(styled), "abc…");
  assert.ok(styled.endsWith("\x1b[0m…"), "an open style is reset at the cut");
  assert.equal(fit("ab", 4, "right"), "  ab");
  assert.equal(fit("ab", 5, "center"), " ab  ");
  assert.equal(fit("abcdef", 4), "abc…");
  assert.equal(padStart(p.bold("ab"), 4).startsWith("  "), true);
});

test("wrap breaks on spaces, breaks long words and keeps indentation", () => {
  assert.deepEqual(wrap("the quick brown fox jumps", 10), ["the quick", "brown fox", "jumps"]);
  assert.deepEqual(wrap("abcdefghij", 4), ["abcd", "efgh", "ij"]);
  assert.deepEqual(wrap("  indented text that wraps", 12), ["  indented", "  text that", "  wraps"]);
  assert.deepEqual(wrap("one\n\ntwo", 10), ["one", "", "two"]);
  for (const line of wrap("日本語の文章をここで折り返します 長い", 7)) assert.ok(width(line) <= 7, line);
});

test("hbar resolves eighths in Unicode and whole cells in ASCII", () => {
  assert.equal(hbar(1, 1, 8, false), "████████");
  assert.equal(hbar(0.5, 1, 8, false), "████");
  assert.equal(hbar(1, 16, 2, false), "▏", "one eighth");
  assert.equal(hbar(0.5625, 1, 8, false), "████▌");
  assert.equal(hbar(0.0001, 1, 8, false), "▏", "a positive value never renders empty");
  assert.equal(hbar(0, 1, 8, false), "");
  assert.equal(hbar(0.5, 1, 8, true), "####");
  assert.equal(hbar(2, 1, 4, true), "####", "clamped to the width");
  assert.equal(hbar(1, 0, 4, false), "");
});

test("agreement bars tick the floor on the same scale", () => {
  const plain = (s: ReturnType<typeof agreementBar>) => s.map((x) => x.text).join("");
  assert.equal(plain(agreementBar(0.5, 1, 10, false)), "█████····┆");
  assert.equal(plain(agreementBar(1, 1, 10, false)), "█████████┃", "a tick inside the fill is heavy");
  assert.equal(plain(agreementBar(0.5, 0.3, 10, true)), "##|##.....");
  assert.equal(plain(agreementBar(null, 0.5, 4, true)), ".|..");
  for (const ascii of [true, false]) assert.equal(width(plain(agreementBar(0.37, 0.81, 23, ascii))), 23);
  const painted = paintSegments(agreementBar(0.5, 1, 10, false), { fill: p.treat, tick: p.floor });
  assert.equal(stripAnsi(painted), "█████····┆");
});

test("range bars place the range and the median on a shared axis", () => {
  const plain = (s: ReturnType<typeof rangeBar>) => s.map((x) => x.text).join("");
  assert.equal(plain(rangeBar(2, 5, 8, 0, 10, 11, false)), "──━━━●━━━──");
  assert.equal(plain(rangeBar(2, 5, 8, 0, 10, 11, true)), "--===o===--");
  assert.equal(plain(rangeBar(null, 0, null, 0, 10, 5, true)), "o----");
  assert.equal(width(plain(rangeBar(-5, 100, 200, 0, 10, 9, false))), 9, "clamped to the axis");
});

test("sparklines and histograms", () => {
  assert.equal(sparkline([0, 1, 2, 4, 8], false), "▁▂▃▅█");
  assert.equal(sparkline([0, 4, 8], true), " =#");
  assert.equal(sparkline([], false), "");
  assert.deepEqual(histogram([0, 1, 2, 9, 10], 2, 0, 10), [3, 2]);
});

test("big numerals cover what a cost prints, and refuse anything else", () => {
  const rows = bigText("$8.54");
  assert.ok(rows);
  assert.equal(rows.length, 3);
  assert.equal(new Set(rows.map((r) => width(r))).size, 1, "the three rows are one width");
  assert.equal(bigText("$1,234 50%")?.length, 3);
  assert.equal(bigText("abc"), null);
});

test("boxes are exactly as wide as asked, in Unicode and ASCII", () => {
  for (const chars of [ROUNDED, ASCII_BOX]) {
    for (const w of [10, 40, 80]) {
      const lines = box(["short", "a line much longer than the box can hold, which is cut", DIVIDER, p.bold("styled")], {
        width: w,
        chars,
        paint: p,
        title: "A title that is long",
        note: "a note",
      });
      for (const line of lines) assert.equal(width(line), w, line);
    }
  }
  const unicode = box(["hi"], { width: 12, chars: ROUNDED, paint: paint(false), title: "T" });
  assert.deepEqual(unicode, ["╭─ T ──────╮", "│ hi       │", "╰──────────╯"]);
  const ascii = box(["hi"], { width: 12, chars: ASCII_BOX, paint: paint(false), title: "T", note: "n" });
  assert.deepEqual(ascii, ["+- T ------+", "| hi       |", "+----------+"], "a note is dropped when it does not fit");
  assert.match(box([DIVIDER], { width: 6, chars: ROUNDED, paint: paint(false) })[1] as string, /^├────┤$/);
  assert.equal(width(rule(30, ROUNDED, p, "Input")), 30);
});

test("layout drops optional columns first and gives the rest to the flexible one", () => {
  const rows = [{ a: "name", bar: "", c: "12345", opt: "optional text" }];
  const wide = layout([{ id: "a" }, { id: "bar", flex: true, min: 4, max: 10 }, { id: "c", align: "right" }, { id: "opt", drop: 1 }], rows, 60);
  assert.deepEqual(wide.columns.map((c) => [c.id, c.width]), [["a", 4], ["bar", 10], ["c", 5], ["opt", 13]]);
  const narrow = layout([{ id: "a" }, { id: "bar", flex: true, min: 4 }, { id: "c", align: "right" }, { id: "opt", drop: 1 }], rows, 20);
  assert.deepEqual(narrow.columns.map((c) => c.id), ["a", "bar", "c"]);
  assert.ok(width(renderRow(narrow, rows[0] as Record<string, string>)) <= 20);
});

test("captured controls are neutralized: no escape sequence survives", () => {
  const hostile = "a\x1b[2Jb\x1b]0;pwned\x07c\x9b31md\re\r\nf‮g\x00h\x7fi\tj k";
  const out = clean(hostile);
  assert.doesNotMatch(out, /[\x00-\x08\x0b-\x1f\x7f-\x9f]/);
  assert.equal(out, "a^[[2Jb^[]0;pwned^Gc<9B>31md\ne\nf<U+202E>g^@h^?i  j\nk");
  assert.equal(cleanLine("one\n two\x1b[A"), "one two^[[A");
  const deep = cleanDeep({ a: ["\x1b[31mred"], n: 3, b: { c: "\x07" } });
  assert.deepEqual(deep, { a: ["^[[31mred"], n: 3, b: { c: "^G" } });
  assert.equal(toAscii("−5 pts × 2 → … ✓"), "-5 pts x 2 -> ... ?");
});
