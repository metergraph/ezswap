// The terminal table: one row for production, one per treatment, and the
// noise floor last ("production vs itself"). Every figure comes from core;
// this module only lays them out within a width.

import type { RunFile } from "../types.ts";
import { fieldComparisons, floorField, formatMs, formatPercent, formatSigned, formatUsd } from "../core/index.ts";
import type { FieldComparison } from "../core/fields.ts";
import { type ReportModel, reportModel } from "./model.ts";

export interface TableRenderOptions {
  tracked?: string[];
  labels?: boolean;
  monthlyCalls?: number | null;
  width?: number;
  color?: boolean;
  /** Plain ASCII only. Defaults to true when stdout is not a terminal. */
  ascii?: boolean;
}

const MAX_FIELDS = 3;
const GAP = 2;

interface Col {
  key: string;
  head: string;
  /** Lower drops first when the table is too wide; 0 never drops. */
  keep: number;
  align: "left" | "right";
}

interface Line {
  cells: Record<string, string>;
  /** Keys of cells that are beyond noise, when labels are on. */
  beyond: Set<string>;
  style: "head" | "production" | "treatment" | "floor";
}

function agreement(c: FieldComparison | null | undefined): string {
  if (!c || !c.pairs) return "—";
  if (c.kind === "label" || c.kind === "text" || c.kind === "long_text") return formatPercent(c.rate);
  if (c.kind === "list") return formatPercent(c.overlap);
  if (c.kind === "number") return formatSigned(c.diffBox?.median);
  return "—";
}

function floorAgreement(f: ReturnType<typeof floorField>): string {
  if (!f) return "—";
  if (f.kind === "label" || f.kind === "text" || f.kind === "long_text") return formatPercent(f.rate);
  if (f.kind === "list") return formatPercent(f.overlap);
  if (f.kind === "number") return formatSigned(f.diffBox?.median);
  return "—";
}

/** Shorten a field path from the left, keeping its end: "$.a.b.category" to "…category". */
function shortPath(path: string, max: number, ascii: boolean): string {
  const tail = path.replace(/^\$\.?/, "");
  if (tail.length <= max) return tail;
  return (ascii ? "~" : "…") + tail.slice(tail.length - max + 1);
}

function clip(text: string, max: number, ascii: boolean): string {
  if (text.length <= max) return text;
  if (max <= 1) return text.slice(0, max);
  return text.slice(0, max - 1) + (ascii ? "~" : "…");
}

const ASCII_MAP: [RegExp, string][] = [
  [/−/g, "-"],
  [/—/g, "-"],
  [/×/g, "x"],
  [/…/g, "~"],
  [/’/g, "'"],
  [/[^\x20-\x7e]/g, "?"],
];

function toAscii(text: string): string {
  let out = text;
  for (const [re, to] of ASCII_MAP) out = out.replace(re, to);
  return out;
}

function pad(text: string, width: number, align: "left" | "right"): string {
  const n = [...text].length;
  if (n >= width) return text;
  return align === "right" ? " ".repeat(width - n) + text : text + " ".repeat(width - n);
}

const ANSI = { bold: "\x1b[1m", dim: "\x1b[2m", yellow: "\x1b[33m", reset: "\x1b[0m" };

export function tableFromModel(model: ReportModel, options: TableRenderOptions = {}): string {
  const width = Math.max(40, Math.floor(options.width ?? 100));
  const ascii = options.ascii ?? !process.stdout?.isTTY;
  const color = Boolean(options.color);
  const view = model.view;
  const tracked = (options.tracked ?? view.tracked_fields).slice(0, MAX_FIELDS);
  const labels = view.arms.map((a) => a.label);
  const rows = new Map(model.rows.map((r) => [r.key, r]));
  const stat = (key: string) => rows.get(key);
  const fields = tracked.map((path) => ({ path, ...fieldComparisons(view.cases ?? [], path, labels, view.fields) }));

  const cols: Col[] = [
    { key: "name", head: "", keep: 0, align: "left" },
    { key: "cost", head: "cost/call", keep: 0, align: "right" },
    { key: "dcost", head: ascii ? "d cost" : "Δ cost", keep: 0, align: "right" },
    { key: "latency", head: "latency", keep: 0, align: "right" },
    { key: "json", head: "JSON ok", keep: 5, align: "right" },
    { key: "keys", head: "same keys", keep: 4, align: "right" },
    { key: "refused", head: "refused", keep: 3, align: "right" },
    { key: "cutoff", head: "cut off", keep: 2, align: "right" },
    ...fields.map((f, i) => ({ key: `field:${f.path}`, head: f.path, keep: 1 - i * 0.1, align: "right" as const })),
  ];

  const lines: Line[] = [];
  const prodCells: Record<string, string> = {
    name: "production",
    cost: stat("cost_per_call_usd")?.production ?? "—",
    dcost: "",
    latency: (stat("latency_ms_p50")?.production ?? "—").replace(/ recorded$/, ""),
    json: stat("json_parse_rate")?.production ?? "—",
    keys: "",
    refused: stat("empty_or_refused_rate")?.production ?? "—",
    cutoff: stat("cut_off_rate")?.production ?? "—",
  };
  for (const f of fields) prodCells[`field:${f.path}`] = "";
  lines.push({ cells: prodCells, beyond: new Set(), style: "production" });

  model.columns.forEach((column, i) => {
    const cell = (key: string) => stat(key)?.cells[i];
    const beyond = new Set<string>();
    const mark = (colKey: string, statKey: string) => {
      if (model.labels && cell(statKey)?.label?.tone === "beyond") beyond.add(colKey);
    };
    const cells: Record<string, string> = {
      name: column.label,
      cost: cell("cost_per_call_usd")?.text ?? "—",
      dcost: cell("cost_per_call_usd")?.delta?.replace(/ (replay|control)$/, "") ?? "—",
      latency: cell("latency_ms_p50")?.text ?? "—",
      json: cell("json_parse_rate")?.text ?? "—",
      keys: cell("same_keys_rate")?.text ?? "—",
      refused: cell("empty_or_refused_rate")?.text ?? "—",
      cutoff: cell("cut_off_rate")?.text ?? "—",
    };
    mark("dcost", "cost_per_call_usd");
    mark("latency", "latency_ms_p50");
    mark("json", "json_parse_rate");
    mark("keys", "same_keys_rate");
    mark("refused", "empty_or_refused_rate");
    mark("cutoff", "cut_off_rate");
    for (const f of fields) {
      cells[`field:${f.path}`] = agreement(f.comparisons[i]);
      mark(`field:${f.path}`, `field:${f.path}`);
    }
    lines.push({ cells, beyond, style: "treatment" });
  });

  const floor = view.noise_floor;
  if (floor) {
    const s = floor.summary;
    const cells: Record<string, string> = {
      name: "production vs itself",
      cost: formatUsd(s.cost_per_call_usd),
      dcost: (stat("cost_per_call_usd")?.floor ?? "—").replace(/ against itself$/, "").replace(/ itself$/, ""),
      latency: formatMs(s.latency_ms_p50),
      json: formatPercent(s.json_parse_rate),
      keys: formatPercent(s.same_keys_rate),
      refused: formatPercent(s.empty_or_refused_rate),
      cutoff: formatPercent(s.cut_off_rate),
    };
    for (const f of fields) cells[`field:${f.path}`] = floorAgreement(floorField(view, f.path, f.kind === "mixed" ? null : f.kind));
    lines.push({ cells, beyond: new Set(), style: "floor" });
  }

  const clean = (text: string) => (ascii ? toAscii(text) : text);
  for (const line of lines) for (const k of Object.keys(line.cells)) line.cells[k] = clean(line.cells[k] as string);

  // Fit: drop the lowest-kept columns, then narrow field headers and the name column.
  let active = [...cols];
  const markWidth = model.labels ? 1 : 0;
  const widthOf = (col: Col) => {
    const head = [...clean(col.head)].length;
    const body = Math.max(0, ...lines.map((l) => [...(l.cells[col.key] ?? "")].length + (col.key !== "name" && l.beyond.has(col.key) ? markWidth : 0)));
    return Math.max(head, body);
  };
  const total = (set: Col[], widths: Map<string, number>) => set.reduce((s, c) => s + (widths.get(c.key) ?? 0), 0) + GAP * (set.length - 1);
  let widths = new Map(active.map((c) => [c.key, widthOf(c)]));
  // Field headers are paths: shorten them to their values' width (at least 8) before dropping anything.
  for (const col of active) {
    if (!col.key.startsWith("field:")) continue;
    const body = Math.max(0, ...lines.map((l) => [...(l.cells[col.key] ?? "")].length));
    const w = Math.max(8, body);
    col.head = shortPath(col.head, w, ascii);
    widths.set(col.key, Math.max(body, [...col.head].length));
  }
  const nameNeeded = widths.get("name") ?? 10;
  // Keep row names whole (up to 20 columns) before giving up a column.
  const nameMin = Math.min(nameNeeded, 20);
  while (total(active, new Map([...widths, ["name", Math.min(nameNeeded, nameMin)]])) > width) {
    const droppable = active.filter((c) => c.keep > 0).sort((a, b) => a.keep - b.keep);
    if (!droppable.length) break;
    const drop = droppable[0] as Col;
    active = active.filter((c) => c !== drop);
  }
  const spare = width - total(active, new Map([...widths, ["name", 0]]));
  widths.set("name", Math.max(1, Math.min(nameNeeded, spare)));
  widths = new Map(widths);
  // Last resort at very small widths: narrow every column evenly.
  while (total(active, widths) > width) {
    const widest = [...active].sort((a, b) => (widths.get(b.key) ?? 0) - (widths.get(a.key) ?? 0))[0] as Col;
    widths.set(widest.key, Math.max(1, (widths.get(widest.key) ?? 1) - 1));
    if ((widths.get(widest.key) ?? 0) <= 1) break;
  }

  const style = (text: string, kind: Line["style"]) => {
    if (!color) return text;
    if (kind === "head") return ANSI.bold + text + ANSI.reset;
    if (kind === "floor") return ANSI.dim + text + ANSI.reset;
    return text;
  };
  const renderCell = (line: Line, col: Col): string => {
    const w = widths.get(col.key) ?? 0;
    const raw = line.cells[col.key] ?? "";
    const flagged = col.key !== "name" && line.beyond.has(col.key);
    const text = clip(raw, flagged ? Math.max(0, w - 1) : w, ascii) + (flagged ? "*" : "");
    const padded = pad(text, w, col.align);
    if (flagged && color) return padded.replace(/\S.*$/, (m) => ANSI.yellow + m + ANSI.reset);
    return padded;
  };
  const out: string[] = [];
  const headLine: Line = { cells: Object.fromEntries(active.map((c) => [c.key, clean(c.head)])), beyond: new Set(), style: "head" };
  const join = (parts: string[]) => parts.join(" ".repeat(GAP)).replace(/\s+$/, "");
  out.push(style(join(active.map((c) => renderCell(headLine, c))), "head"));
  out.push((ascii ? "-" : "─").repeat(Math.min(width, total(active, widths))));
  for (const line of lines) {
    if (line.style === "floor") out.push((ascii ? "-" : "─").repeat(Math.min(width, total(active, widths))));
    out.push(style(join(active.map((c) => renderCell(line, c))), line.style));
  }

  const notes: string[] = [];
  if (floor) notes.push(`${ascii ? "d cost" : "Δ cost"} and latency are read against production's model replayed (A1); production's recorded latency is not compared.`);
  if (fields.length) notes.push("Field columns: agreement with production (labels and text: share identical; lists: items shared; numbers: median shift).");
  const dropped = cols.filter((c) => !active.includes(c)).map((c) => c.head);
  if (dropped.length) notes.push(`Not shown at this width: ${dropped.map((d) => clean(d)).join(", ")}.`);
  if (model.labels) notes.push("* beyond noise: the gap from production is larger than production's gap from itself. Not a verdict.");
  for (const note of notes) out.push(...wrap(clean(note), width));
  return out.join("\n") + "\n";
}

function wrap(text: string, width: number): string[] {
  const words = text.split(" ");
  const lines: string[] = [];
  let line = "";
  for (const word of words) {
    const w = word.length > width ? word.slice(0, width) : word;
    if (!line) line = w;
    else if (line.length + 1 + w.length <= width) line += ` ${w}`;
    else {
      lines.push(line);
      line = w;
    }
  }
  if (line) lines.push(line);
  return lines;
}

export function renderTerminalTable(run: RunFile, options: TableRenderOptions = {}): string {
  return tableFromModel(reportModel(run, options), options);
}
