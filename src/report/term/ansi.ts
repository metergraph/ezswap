// ANSI styles and the report's palette, from the Metergraph design system.
// Colour marks what a row is, never whether it is good: production is the
// neutral grey the product gives its control, every treatment shares the
// brand green, the noise floor is a darker grey. A difference beyond noise is
// emphasis (dark ink on the brand lime), never the system's alert colours.
//
// Colours are 24-bit where the terminal says it supports them (COLORTERM),
// else the nearest of the 256 standard terminal colours.

export const RESET = "\x1b[0m";

/** Metergraph design system colours the report uses. */
export const DESIGN = Object.freeze({
  green300: "#60AC98",
  green500: "#468171",
  green700: "#18614E",
  lime700: "#C7F06A",
  grey50: "#F0EFEE",
  grey300: "#B1B1B0",
  grey400: "#616060",
  grey500: "#353534",
  grey600: "#111111",
  white: "#FFFFFF",
});

/** The design system's verdict colours. The report never uses them. */
export const ALERT_COLORS = Object.freeze({ good: "#0EAA45", bad: "#D04526" });

type Rgb = [number, number, number];

function rgb(hex: string): Rgb {
  const n = Number.parseInt(hex.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

const CUBE = [0, 95, 135, 175, 215, 255];

/** The nearest colour in the xterm 256-colour palette (the 6x6x6 cube and the grey ramp). */
export function nearest256(hex: string): number {
  const [r, g, b] = rgb(hex);
  const distance = (c: Rgb) => (c[0] - r) ** 2 + (c[1] - g) ** 2 + (c[2] - b) ** 2;
  const step = (v: number) => CUBE.reduce((best, c, i) => (Math.abs(c - v) < Math.abs(CUBE[best]! - v) ? i : best), 0);
  const [ri, gi, bi] = [step(r), step(g), step(b)];
  const cube: Rgb = [CUBE[ri]!, CUBE[gi]!, CUBE[bi]!];
  let best = { index: 16 + 36 * ri + 6 * gi + bi, d: distance(cube) };
  for (let i = 0; i < 24; i++) {
    const v = 8 + 10 * i;
    const d = distance([v, v, v]);
    if (d < best.d) best = { index: 232 + i, d };
  }
  return best.index;
}

/** Does the terminal advertise 24-bit colour? */
export function supportsTruecolor(env: Record<string, string | undefined> = process.env): boolean {
  return /^(truecolor|24bit)$/i.test(env["COLORTERM"] ?? "");
}

function fg(hex: string, truecolor: boolean): string {
  return truecolor ? `38;2;${rgb(hex).join(";")}` : `38;5;${nearest256(hex)}`;
}

function bg(hex: string, truecolor: boolean): string {
  return truecolor ? `48;2;${rgb(hex).join(";")}` : `48;5;${nearest256(hex)}`;
}

/** Wrap text in a style, reopening it after any reset inside so nested styles keep the outer one. */
function sgr(open: string, text: string): string {
  if (!text) return text;
  const start = `\x1b[${open}m`;
  return `${start}${text.split(RESET).join(RESET + start)}${RESET}`;
}

export interface Paint {
  readonly color: boolean;
  bold(text: string): string;
  dim(text: string): string;
  italic(text: string): string;
  underline(text: string): string;
  reverse(text: string): string;
  /** Production's colour. */
  prod(text: string): string;
  /** The accent every treatment shares. */
  treat(text: string): string;
  /** The noise floor: production against itself. */
  floor(text: string): string;
  /** Secondary copy: labels, units, rules. */
  muted(text: string): string;
  /** Box borders and rules. */
  line(text: string): string;
  /** The wordmark and headings. */
  brand(text: string): string;
  /** Emphasis for a magnitude beyond noise: bold, no hue. */
  emph(text: string): string;
  /** The brand lime badge: a label such as "beyond noise". */
  highlight(text: string): string;
  /** The explorer's active tab. */
  tab(text: string): string;
  /** The selected row in the explorer. */
  select(text: string): string;
  /** The status bar and the active tab. */
  bar(text: string): string;
}

const identity = (text: string) => text;

/**
 * A painter: real ANSI styles when `color`, the identity otherwise.
 * `truecolor` defaults to what the terminal advertises.
 */
export function paint(color: boolean, truecolor: boolean = supportsTruecolor()): Paint {
  if (!color) {
    return {
      color: false,
      bold: identity,
      dim: identity,
      italic: identity,
      underline: identity,
      reverse: identity,
      prod: identity,
      treat: identity,
      floor: identity,
      muted: identity,
      line: identity,
      brand: identity,
      emph: identity,
      highlight: identity,
      tab: identity,
      select: identity,
      bar: identity,
    };
  }
  return {
    color: true,
    bold: (t) => sgr("1", t),
    dim: (t) => sgr("2", t),
    italic: (t) => sgr("3", t),
    underline: (t) => sgr("4", t),
    reverse: (t) => sgr("7", t),
    prod: (t) => sgr(fg(DESIGN.grey300, truecolor), t),
    treat: (t) => sgr(fg(DESIGN.green300, truecolor), t),
    floor: (t) => sgr(fg(DESIGN.grey400, truecolor), t),
    // Secondary copy uses the terminal's own dim, so it reads on dark and light backgrounds alike.
    muted: (t) => sgr("2", t),
    line: (t) => sgr(fg(DESIGN.grey400, truecolor), t),
    brand: (t) => sgr(`1;${fg(DESIGN.green300, truecolor)}`, t),
    emph: (t) => sgr("1", t),
    highlight: (t) => sgr(`1;${fg(DESIGN.grey600, truecolor)};${bg(DESIGN.lime700, truecolor)}`, t),
    tab: (t) => sgr(`1;${fg(DESIGN.white, truecolor)};${bg(DESIGN.green700, truecolor)}`, t),
    select: (t) => sgr(`${fg(DESIGN.white, truecolor)};${bg(DESIGN.green700, truecolor)}`, t),
    bar: (t) => sgr(`${fg(DESIGN.grey50, truecolor)};${bg(DESIGN.grey500, truecolor)}`, t),
  };
}

/** Which palette slot a row's arm takes. */
export type Role = "production" | "treatment" | "floor";

export function roleColor(p: Paint, role: Role): (text: string) => string {
  return role === "production" ? p.prod : role === "treatment" ? p.treat : p.floor;
}
