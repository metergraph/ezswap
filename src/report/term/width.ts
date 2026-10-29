// Display width of terminal text: ANSI sequences take no columns, East Asian
// wide characters and emoji take two, combining marks take none. Plus the
// width-aware helpers built on it: truncation with an ellipsis, padding and
// word wrap.

const ANSI_RE = /\x1b\[[0-?]*[ -\/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g;
const ANSI_AT_START = /^(?:\x1b\[[0-?]*[ -\/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\))/;
const PRINTABLE_ASCII = /^[\x20-\x7e]*$/;

export function stripAnsi(text: string): string {
  return text.replace(ANSI_RE, "");
}

// Inclusive ranges of code points a terminal draws two columns wide.
const WIDE: [number, number][] = [
  [0x1100, 0x115f],
  [0x231a, 0x231b],
  [0x2329, 0x232a],
  [0x23e9, 0x23ec],
  [0x23f0, 0x23f0],
  [0x23f3, 0x23f3],
  [0x25fd, 0x25fe],
  [0x2614, 0x2615],
  [0x2648, 0x2653],
  [0x267f, 0x267f],
  [0x2693, 0x2693],
  [0x26a1, 0x26a1],
  [0x26aa, 0x26ab],
  [0x26bd, 0x26be],
  [0x26c4, 0x26c5],
  [0x26ce, 0x26ce],
  [0x26d4, 0x26d4],
  [0x26ea, 0x26ea],
  [0x26f2, 0x26f3],
  [0x26f5, 0x26f5],
  [0x26fa, 0x26fa],
  [0x26fd, 0x26fd],
  [0x2705, 0x2705],
  [0x270a, 0x270b],
  [0x2728, 0x2728],
  [0x274c, 0x274c],
  [0x274e, 0x274e],
  [0x2753, 0x2755],
  [0x2757, 0x2757],
  [0x2795, 0x2797],
  [0x27b0, 0x27b0],
  [0x27bf, 0x27bf],
  [0x2b1b, 0x2b1c],
  [0x2b50, 0x2b50],
  [0x2b55, 0x2b55],
  [0x2e80, 0x303e],
  [0x3041, 0x33ff],
  [0x3400, 0x4dbf],
  [0x4e00, 0x9fff],
  [0xa000, 0xa4cf],
  [0xa960, 0xa97f],
  [0xac00, 0xd7a3],
  [0xf900, 0xfaff],
  [0xfe10, 0xfe19],
  [0xfe30, 0xfe6f],
  [0xff00, 0xff60],
  [0xffe0, 0xffe6],
  [0x16fe0, 0x16fe4],
  [0x17000, 0x18cff],
  [0x1b000, 0x1b2ff],
  [0x1f004, 0x1f004],
  [0x1f0cf, 0x1f0cf],
  [0x1f18e, 0x1f18e],
  [0x1f191, 0x1f19a],
  [0x1f200, 0x1f251],
  [0x1f300, 0x1f320],
  [0x1f32d, 0x1f335],
  [0x1f337, 0x1f37c],
  [0x1f37e, 0x1f393],
  [0x1f3a0, 0x1f3ca],
  [0x1f3cf, 0x1f3d3],
  [0x1f3e0, 0x1f3f0],
  [0x1f3f4, 0x1f3f4],
  [0x1f3f8, 0x1f43e],
  [0x1f440, 0x1f440],
  [0x1f442, 0x1f4fc],
  [0x1f4ff, 0x1f53d],
  [0x1f54b, 0x1f54e],
  [0x1f550, 0x1f567],
  [0x1f57a, 0x1f57a],
  [0x1f595, 0x1f596],
  [0x1f5a4, 0x1f5a4],
  [0x1f5fb, 0x1f64f],
  [0x1f680, 0x1f6c5],
  [0x1f6cc, 0x1f6cc],
  [0x1f6d0, 0x1f6d2],
  [0x1f6d5, 0x1f6d7],
  [0x1f6dc, 0x1f6df],
  [0x1f6eb, 0x1f6ec],
  [0x1f6f4, 0x1f6fc],
  [0x1f7e0, 0x1f7eb],
  [0x1f7f0, 0x1f7f0],
  [0x1f90c, 0x1f93a],
  [0x1f93c, 0x1f945],
  [0x1f947, 0x1f9ff],
  [0x1fa70, 0x1faff],
  [0x20000, 0x2fffd],
  [0x30000, 0x3fffd],
];

function inRanges(cp: number, ranges: [number, number][]): boolean {
  let lo = 0;
  let hi = ranges.length - 1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    const [a, b] = ranges[mid] as [number, number];
    if (cp < a) hi = mid - 1;
    else if (cp > b) lo = mid + 1;
    else return true;
  }
  return false;
}

const ZERO_WIDTH = /^[\p{Mn}\p{Me}\p{Cf}​-‏︀-️]$/u;

/** Columns one code point takes: 0, 1 or 2. Controls count 0; they are neutralized before they get here. */
export function codePointWidth(cp: number): number {
  if (cp < 0x20 || (cp >= 0x7f && cp < 0xa0)) return 0;
  if (cp < 0x300) return 1;
  if (ZERO_WIDTH.test(String.fromCodePoint(cp))) return 0;
  if (cp >= 0xe0100 && cp <= 0xe01ef) return 0;
  return inRanges(cp, WIDE) ? 2 : 1;
}

const segmenter = new Intl.Segmenter("en", { granularity: "grapheme" });

/** Columns one grapheme cluster takes: its first visible code point's, two for an emoji presentation. */
function clusterWidth(cluster: string): number {
  let width = 0;
  for (const ch of cluster) {
    const w = codePointWidth(ch.codePointAt(0) as number);
    if (w > 0) {
      width = w;
      break;
    }
  }
  if (width === 1 && cluster.includes("️")) return 2;
  return width;
}

function graphemes(text: string): string[] {
  if (PRINTABLE_ASCII.test(text)) return text.split("");
  return Array.from(segmenter.segment(text), (s) => s.segment);
}

/** Visible width in columns, ignoring ANSI sequences. */
export function width(text: string): number {
  if (PRINTABLE_ASCII.test(text)) return text.length;
  const plain = stripAnsi(text);
  if (PRINTABLE_ASCII.test(plain)) return plain.length;
  let total = 0;
  for (const g of graphemes(plain)) total += clusterWidth(g);
  return total;
}

/**
 * Cut text to at most `max` columns, ending with `ellipsis` when cut. ANSI
 * sequences pass through, and a style left open is reset at the cut.
 */
export function truncate(text: string, max: number, ellipsis = "…"): string {
  if (max <= 0) return "";
  if (width(text) <= max) return text;
  const room = Math.max(0, max - width(ellipsis));
  let out = "";
  let used = 0;
  let styled = false;
  let rest = text;
  while (rest.length) {
    const ansi = rest.match(ANSI_AT_START);
    if (ansi) {
      out += ansi[0];
      styled = true;
      rest = rest.slice(ansi[0].length);
      continue;
    }
    const next = rest.search(/\x1b/);
    const chunk = next === -1 ? rest : next === 0 ? rest.slice(0, 1) : rest.slice(0, next);
    rest = rest.slice(chunk.length);
    let stop = false;
    for (const g of graphemes(chunk)) {
      const w = clusterWidth(g);
      if (used + w > room) {
        stop = true;
        break;
      }
      out += g;
      used += w;
    }
    if (stop) break;
  }
  const tail = width(ellipsis) <= max ? ellipsis : "";
  return out + (styled ? "\x1b[0m" : "") + tail;
}

/** Pad (or cut) to exactly `cols` columns. */
export function fit(text: string, cols: number, align: "left" | "right" | "center" = "left", ellipsis = "…"): string {
  const cut = truncate(text, cols, ellipsis);
  const gap = Math.max(0, cols - width(cut));
  if (align === "right") return " ".repeat(gap) + cut;
  if (align === "center") return " ".repeat(Math.floor(gap / 2)) + cut + " ".repeat(gap - Math.floor(gap / 2));
  return cut + " ".repeat(gap);
}

/** Pad on the right to at least `cols` columns; never cuts. */
export function padEnd(text: string, cols: number): string {
  return text + " ".repeat(Math.max(0, cols - width(text)));
}

export function padStart(text: string, cols: number): string {
  return " ".repeat(Math.max(0, cols - width(text))) + text;
}

/**
 * Word-wrap plain text (no ANSI) to `cols` columns. Words longer than a line
 * are broken. Existing newlines start new lines; leading indentation on each
 * source line is kept on its continuation lines.
 */
export function wrap(text: string, cols: number): string[] {
  const limit = Math.max(1, cols);
  const out: string[] = [];
  for (const source of text.split("\n")) {
    const indent = (source.match(/^ */)?.[0] ?? "").slice(0, Math.max(0, limit - 1));
    const words = source.slice(indent.length).split(/ +/).filter((w) => w.length);
    if (!words.length) {
      out.push("");
      continue;
    }
    let line = indent;
    let lineWidth = indent.length;
    const push = () => {
      out.push(line.replace(/ +$/, ""));
      line = indent;
      lineWidth = indent.length;
    };
    for (let word of words) {
      let w = width(word);
      const sep = lineWidth > indent.length ? 1 : 0;
      if (lineWidth + sep + w <= limit) {
        line += (sep ? " " : "") + word;
        lineWidth += sep + w;
        continue;
      }
      if (lineWidth > indent.length) push();
      while (w > limit - indent.length) {
        const head = truncate(word, limit - indent.length, "");
        out.push(indent + head);
        word = word.slice(head.length);
        w = width(word);
      }
      if (word) {
        line = indent + word;
        lineWidth = indent.length + w;
      }
    }
    if (lineWidth > indent.length) push();
  }
  return out;
}
