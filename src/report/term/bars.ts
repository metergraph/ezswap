// Bars drawn with text: horizontal bars to an eighth of a column, an
// agreement bar with the noise floor ticked on the same scale, range bars on
// a shared axis, sparklines, and big three-row numerals for the one figure
// worth reading from across the room.

const EIGHTHS = ["", "▏", "▎", "▍", "▌", "▋", "▊", "▉"];
const FULL = "█";

function clamp01(v: number): number {
  return Number.isFinite(v) ? Math.min(1, Math.max(0, v)) : 0;
}

/**
 * A bar `cols` columns long for `value` of `max`, as filled cells only (no
 * padding). Unicode resolves an eighth of a column; ASCII a whole one. A
 * positive value never renders empty.
 */
export function hbar(value: number, max: number, cols: number, ascii: boolean): string {
  if (!(cols > 0) || !(max > 0) || !(value > 0)) return "";
  const share = clamp01(value / max);
  if (ascii) {
    const n = Math.max(1, Math.round(share * cols));
    return "#".repeat(n);
  }
  const eighths = Math.max(1, Math.round(share * cols * 8));
  const full = Math.floor(eighths / 8);
  return FULL.repeat(full) + (EIGHTHS[eighths % 8] as string);
}

/** Cell index (0..cols-1) a share of the scale falls in. */
export function position(share: number, cols: number): number {
  return Math.min(cols - 1, Math.max(0, Math.round(clamp01(share) * (cols - 1))));
}

export interface Segment {
  text: string;
  kind: "fill" | "track" | "tick" | "range" | "mark";
}

/**
 * An agreement bar: `value` (0..1) filled, the rest a dotted track, and the
 * noise floor's own level ticked on the same scale.
 */
export function agreementBar(value: number | null, floor: number | null, cols: number, ascii: boolean): Segment[] {
  const fill = value == null ? "" : hbar(value, 1, cols, ascii);
  const cells = Array.from(fill);
  const kinds: Segment["kind"][] = cells.map(() => "fill");
  const track = ascii ? "." : "·";
  while (cells.length < cols) {
    cells.push(track);
    kinds.push("track");
  }
  if (floor != null && Number.isFinite(floor)) {
    // Ticks sit at the right edge of the cell the floor falls in, so 100% is the last cell.
    const at = Math.min(cols - 1, Math.max(0, Math.ceil(clamp01(floor) * cols) - 1));
    cells[at] = ascii ? "|" : kinds[at] === "fill" ? "┃" : "┆";
    kinds[at] = "tick";
  }
  return merge(cells, kinds);
}

/**
 * A range bar on an axis from `min` to `max`: a track, the range from `lo` to
 * `hi` drawn heavier, the median as a dot.
 */
export function rangeBar(lo: number | null, mid: number | null, hi: number | null, min: number, max: number, cols: number, ascii: boolean): Segment[] {
  const span = max - min || 1;
  const share = (v: number) => (v - min) / span;
  const cells: string[] = Array.from({ length: cols }, () => (ascii ? "-" : "─"));
  const kinds: Segment["kind"][] = cells.map(() => "track");
  if (lo != null && hi != null) {
    const a = position(share(lo), cols);
    const b = position(share(hi), cols);
    for (let i = Math.min(a, b); i <= Math.max(a, b); i++) {
      cells[i] = ascii ? "=" : "━";
      kinds[i] = "range";
    }
  }
  if (mid != null) {
    const m = position(share(mid), cols);
    cells[m] = ascii ? "o" : "●";
    kinds[m] = "mark";
  }
  return merge(cells, kinds);
}

function merge(cells: string[], kinds: Segment["kind"][]): Segment[] {
  const out: Segment[] = [];
  cells.forEach((text, i) => {
    const kind = kinds[i] as Segment["kind"];
    const last = out.at(-1);
    if (last && last.kind === kind) last.text += text;
    else out.push({ text, kind });
  });
  return out;
}

/** Join segments, styling each by kind. */
export function paintSegments(segments: Segment[], style: Partial<Record<Segment["kind"], (t: string) => string>>): string {
  return segments.map((s) => (style[s.kind] ?? ((t: string) => t))(s.text)).join("");
}

const SPARKS = ["▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"];
const ASCII_SPARKS = [" ", ".", ":", "-", "=", "+", "*", "#"];

/** One character per value, scaled to the largest. Zero counts draw the lowest mark (Unicode) or a space (ASCII). */
export function sparkline(values: number[], ascii: boolean): string {
  const marks = ascii ? ASCII_SPARKS : SPARKS;
  const max = Math.max(0, ...values.filter((v) => Number.isFinite(v)));
  return values
    .map((v) => {
      if (!(max > 0) || !(v > 0)) return ascii ? " " : (marks[0] as string);
      return marks[Math.min(marks.length - 1, Math.max(ascii ? 1 : 0, Math.round((v / max) * (marks.length - 1))))] as string;
    })
    .join("");
}

/** Counts of `values` in `bins` equal bins from `min` to `max`. */
export function histogram(values: number[], bins: number, min: number, max: number): number[] {
  const counts = Array.from({ length: Math.max(1, bins) }, () => 0);
  const span = max - min || 1;
  for (const v of values) {
    if (!Number.isFinite(v)) continue;
    const i = Math.min(counts.length - 1, Math.max(0, Math.floor(((v - min) / span) * counts.length)));
    counts[i] = (counts[i] as number) + 1;
  }
  return counts;
}

// Three-row numerals from box-drawing pieces.
const BIG: Record<string, [string, string, string]> = {
  "0": ["┏━┓", "┃ ┃", "┗━┛"],
  "1": ["╺┓ ", " ┃ ", "╺┻╸"],
  "2": ["╺━┓", "┏━┛", "┗━╸"],
  "3": ["╺━┓", " ━┫", "╺━┛"],
  "4": ["╻ ╻", "┗━┫", "  ╹"],
  "5": ["┏━╸", "┗━┓", "╺━┛"],
  "6": ["┏━╸", "┣━┓", "┗━┛"],
  "7": ["╺━┓", "  ┃", "  ╹"],
  "8": ["┏━┓", "┣━┫", "┗━┛"],
  "9": ["┏━┓", "┗━┫", "╺━┛"],
  $: ["┏╋╸", "┗╋┓", "╺╋┛"],
  ".": [" ", " ", "╹"],
  ",": [" ", " ", "┛"],
  "%": ["╻ ╱", " ╱ ", "╱ ╹"],
  " ": [" ", " ", " "],
};

/** `text` as three rows of big numerals, or null when it holds a character the font lacks. */
export function bigText(text: string): [string, string, string] | null {
  const rows: [string, string, string] = ["", "", ""];
  for (const ch of text) {
    const glyph = BIG[ch];
    if (!glyph) return null;
    for (let i = 0; i < 3; i++) rows[i] += (rows[i] ? " " : "") + glyph[i];
  }
  return rows;
}
