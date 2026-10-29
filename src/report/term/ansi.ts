// ANSI styles and the report's palette. Colour marks what a row is, never
// whether it is good: production has one colour, every treatment shares the
// accent, the noise floor is muted. A difference beyond noise is shown with
// emphasis (bold, reverse video), not with green or red.

export const RESET = "\x1b[0m";

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
  /** The selected row in the explorer. */
  select(text: string): string;
  /** The status bar and the active tab. */
  bar(text: string): string;
}

const identity = (text: string) => text;

/** A painter: real ANSI styles when `color`, the identity otherwise. */
export function paint(color: boolean): Paint {
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
    prod: (t) => sgr("38;5;81", t),
    treat: (t) => sgr("38;5;141", t),
    floor: (t) => sgr("38;5;246", t),
    muted: (t) => sgr("38;5;244", t),
    line: (t) => sgr("38;5;240", t),
    brand: (t) => sgr("1;38;5;141", t),
    emph: (t) => sgr("1", t),
    select: (t) => sgr("48;5;237", t),
    bar: (t) => sgr("38;5;252;48;5;236", t),
  };
}

/** Which palette slot a row's arm takes. */
export type Role = "production" | "treatment" | "floor";

export function roleColor(p: Paint, role: Role): (text: string) => string {
  return role === "production" ? p.prod : role === "treatment" ? p.treat : p.floor;
}
