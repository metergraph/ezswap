// Column layout: fit cells into a width, dropping optional columns (highest
// `drop` first) and giving what is left to one flexible column.

import { fit, width } from "./width.ts";

export interface ColumnSpec {
  id: string;
  align?: "left" | "right";
  /** Fixed columns size to their widest cell, capped here. */
  max?: number;
  /** At least this wide. */
  min?: number;
  /** Optional columns drop first, highest number first; 0 never drops. */
  drop?: number;
  /** The one column that takes the space left, between min and max. */
  flex?: boolean;
}

export interface Layout {
  columns: (ColumnSpec & { width: number })[];
  gap: number;
}

/** Decide column widths for these rows within `available` columns. */
export function layout(specs: ColumnSpec[], rows: Record<string, string>[], available: number, gap = 2): Layout {
  const natural = (spec: ColumnSpec) => {
    const widest = Math.max(0, ...rows.map((r) => width(r[spec.id] ?? "")));
    return Math.min(spec.max ?? Infinity, Math.max(spec.min ?? 0, widest));
  };
  let active = specs.filter((s) => s.flex || rows.some((r) => (r[s.id] ?? "") !== ""));
  const need = (set: ColumnSpec[]) =>
    set.reduce((sum, s) => sum + (s.flex ? (s.min ?? 4) : natural(s)), 0) + gap * Math.max(0, set.length - 1);
  while (need(active) > available) {
    const droppable = active.filter((s) => (s.drop ?? 0) > 0).sort((a, b) => (b.drop ?? 0) - (a.drop ?? 0));
    if (!droppable.length) break;
    active = active.filter((s) => s !== droppable[0]);
  }
  const fixed = active.filter((s) => !s.flex).reduce((sum, s) => sum + natural(s), 0) + gap * Math.max(0, active.length - 1);
  let spare = Math.max(0, available - fixed);
  const columns = active.map((s) => {
    if (!s.flex) return { ...s, width: natural(s) };
    const w = Math.max(s.min ?? 4, Math.min(s.max ?? spare, spare));
    spare -= w;
    return { ...s, width: w };
  });
  // Still too wide (a very narrow terminal): narrow the widest fixed columns.
  let total = columns.reduce((sum, c) => sum + c.width, 0) + gap * Math.max(0, columns.length - 1);
  while (total > available) {
    const widest = [...columns].sort((a, b) => b.width - a.width)[0];
    if (!widest || widest.width <= 1) break;
    widest.width -= 1;
    total -= 1;
  }
  return { columns, gap };
}

/** Render one row with a layout. Cells may carry ANSI styles. */
export function renderRow(l: Layout, cells: Record<string, string>, ellipsis = "…"): string {
  return l.columns
    .map((c) => fit(cells[c.id] ?? "", c.width, c.align ?? "left", ellipsis))
    .join(" ".repeat(l.gap))
    .replace(/ +$/, "");
}

export function has(l: Layout, id: string): boolean {
  return l.columns.some((c) => c.id === id);
}

export function widthOf(l: Layout, id: string): number {
  return l.columns.find((c) => c.id === id)?.width ?? 0;
}

/** Columns from the left edge to where column `id` starts. */
export function offsetOf(l: Layout, id: string): number {
  let x = 0;
  for (const c of l.columns) {
    if (c.id === id) return x;
    x += c.width + l.gap;
  }
  return x;
}
