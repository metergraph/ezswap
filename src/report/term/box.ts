// Boxes and panels: a border around lines that are fitted to the inner width,
// so a box is always exactly as wide as asked and never wraps.

import type { Paint } from "./ansi.ts";
import { fit, truncate, width } from "./width.ts";

export interface Charset {
  tl: string;
  tr: string;
  bl: string;
  br: string;
  h: string;
  v: string;
  /** T-junctions for a divider inside a box. */
  lt: string;
  rt: string;
}

export const ROUNDED: Charset = { tl: "╭", tr: "╮", bl: "╰", br: "╯", h: "─", v: "│", lt: "├", rt: "┤" };
export const DOUBLE: Charset = { tl: "╔", tr: "╗", bl: "╚", br: "╝", h: "═", v: "║", lt: "╟", rt: "╢" };
export const ASCII_BOX: Charset = { tl: "+", tr: "+", bl: "+", br: "+", h: "-", v: "|", lt: "+", rt: "+" };
export const ASCII_DOUBLE: Charset = { tl: "+", tr: "+", bl: "+", br: "+", h: "=", v: "|", lt: "+", rt: "+" };

export function charset(ascii: boolean, style: "rounded" | "double" = "rounded"): Charset {
  if (ascii) return style === "double" ? ASCII_DOUBLE : ASCII_BOX;
  return style === "double" ? DOUBLE : ROUNDED;
}

export interface BoxOptions {
  width: number;
  chars: Charset;
  paint: Paint;
  /** Shown in the top border at the left. */
  title?: string;
  /** Shown in the top border at the right, muted. */
  note?: string;
  /** Columns of space inside each side (default 1). */
  padding?: number;
  /** Style for the border; default the muted line colour. */
  border?: (text: string) => string;
  ellipsis?: string;
}

/** The divider line inside a box: pass it as a content line to draw ├────┤. */
export const DIVIDER = "\u0000divider";

/** Draw `lines` inside a border exactly `options.width` columns wide. */
export function box(lines: string[], options: BoxOptions): string[] {
  const { chars: c, paint: p } = options;
  const total = Math.max(4, options.width);
  const pad = options.padding ?? 1;
  const inner = total - 2 - pad * 2;
  const border = options.border ?? p.line;
  const ellipsis = options.ellipsis ?? "…";

  let top = c.tl;
  let used = 1;
  if (options.title) {
    const room = total - 6;
    const title = truncate(options.title, Math.max(1, room), ellipsis);
    top = `${border(`${c.tl}${c.h} `)}${p.bold(title)}${border(" ")}`;
    used = 3 + width(title) + 1;
  } else {
    top = border(c.tl);
  }
  let noteText = "";
  if (options.note) {
    const room = total - used - 5;
    if (room >= 8) noteText = truncate(options.note, room, ellipsis);
  }
  const fillWidth = total - used - 1 - (noteText ? width(noteText) + 3 : 0);
  top += border(c.h.repeat(Math.max(0, fillWidth)));
  if (noteText) top += `${border(" ")}${p.muted(noteText)}${border(` ${c.h}`)}`;
  top += border(c.tr);

  const out = [top];
  const side = border(c.v);
  const space = " ".repeat(pad);
  for (const line of lines) {
    if (line === DIVIDER) {
      out.push(border(c.lt + c.h.repeat(total - 2) + c.rt));
      continue;
    }
    out.push(`${side}${space}${fit(line, inner, "left", ellipsis)}${space}${side}`);
  }
  out.push(border(c.bl + c.h.repeat(total - 2) + c.br));
  return out;
}

/** A horizontal rule `cols` wide, optionally with a label: "── Label ──────". */
export function rule(cols: number, chars: Charset, p: Paint, label?: string): string {
  if (!label) return p.line(chars.h.repeat(Math.max(0, cols)));
  const text = truncate(label, Math.max(1, cols - 4));
  return `${p.line(chars.h.repeat(2))} ${p.bold(text)} ${p.line(chars.h.repeat(Math.max(0, cols - width(text) - 4)))}`;
}
