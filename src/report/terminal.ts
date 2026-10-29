// The terminal report: panels drawn from the report model, width-aware, in
// Unicode or plain ASCII, with or without colour. Every captured string is
// cleaned of control characters before it is laid out (see term/sanitize.ts),
// and every figure and sentence comes from model.ts. Presentation, never a
// verdict: colour marks which arm a figure belongs to, not whether it is good.

import type { RunFile } from "../types.ts";
import { formatCount, formatMs } from "../core/index.ts";
import { type Paint, type Role, paint, roleColor } from "./term/ansi.ts";
import { agreementBar, bigText, hbar, histogram, paintSegments, rangeBar, sparkline } from "./term/bars.ts";
import { type Charset, box, charset } from "./term/box.ts";
import { type ColumnSpec, has, layout, offsetOf, renderRow } from "./term/layout.ts";
import { cleanDeep, cleanLine, toAscii } from "./term/sanitize.ts";
import { fit, truncate, width, wrap } from "./term/width.ts";
import { hostedLink } from "./link.ts";
import { type CaseItem, type CostBar, type FieldPanel, type LabelCell, NOISE_FLOOR_DOC, type ReportModel, SETUP_COMMAND, reportModel } from "./model.ts";

export const MIN_WIDTH = 40;
export const MAX_WIDTH = 140;
/** Calls the static report lists under "Calls that differ". */
export const DIFFER_LIMIT = 5;
/** What a clickable hosted link shows instead of the long URL. */
const HOSTED_LABEL = "metergraph.dev/replay";

export interface TerminalOptions {
  /** Columns to fill; clamped to 40..140. */
  width?: number;
  color?: boolean;
  /** Plain ASCII: no box drawing, no block characters, captured text mapped to ASCII. */
  ascii?: boolean;
  tracked?: string[];
  labels?: boolean;
  monthlyCalls?: number | null;
  /** The run file's path as the user would type it, for the hint to explore it. */
  explore?: string | null;
}

/** Everything a panel needs to draw. */
export interface Ctx {
  w: number;
  p: Paint;
  ascii: boolean;
  chars: Charset;
  ell: string;
  /** Emit OSC 8 hyperlinks: only on a colour terminal, where escape sequences are wanted. */
  links: boolean;
}

export function makeCtx(options: { width?: number; color?: boolean; ascii?: boolean }): Ctx {
  const ascii = Boolean(options.ascii);
  return {
    w: Math.max(MIN_WIDTH, Math.min(MAX_WIDTH, Math.floor(options.width ?? 100))),
    p: paint(Boolean(options.color)),
    ascii,
    chars: charset(ascii),
    ell: ascii ? "~" : "…",
    links: Boolean(options.color) && !ascii,
  };
}

/** Text that opens `url` when clicked, in terminals that support OSC 8 (others show the text). */
function hyperlink(url: string, text: string): string {
  return `\x1b]8;;${url}\x1b\\${text}\x1b]8;;\x1b\\`;
}

/** The report model with every string cleaned for the terminal (and made ASCII when asked). */
export function terminalModel(run: RunFile, options: TerminalOptions = {}): ReportModel {
  const model = reportModel(run, {
    ...(options.tracked ? { tracked: options.tracked } : {}),
    ...(options.labels ? { labels: true } : {}),
    ...(options.monthlyCalls != null ? { monthlyCalls: options.monthlyCalls } : {}),
  });
  return cleanDeep(model, Boolean(options.ascii));
}

// ---------------------------------------------------------------------------
// Small pieces

const g = (ctx: Ctx, unicode: string, ascii: string) => (ctx.ascii ? ascii : unicode);
/** Keeps a figure and its unit on one line when wrapped. */
const NBSP = "\u00a0";

/** A within-noise label as a badge: brackets keep the same width with and without colour. */
export function badge(ctx: Ctx, label: LabelCell | null): string {
  if (!label) return "";
  let text = label.text;
  if (!ctx.ascii) text = text.replace(/, higher$/, " ↑").replace(/, lower$/, " ↓");
  const { p } = ctx;
  if (label.tone === "beyond") return p.color ? p.highlight(` ${text} `) : `[${text}]`;
  return p.muted(`(${text})`);
}

function roleName(ctx: Ctx, role: Role, text: string): string {
  const color = roleColor(ctx.p, role);
  return role === "treatment" ? color(text) : role === "production" ? ctx.p.bold(color(text)) : color(text);
}

/** Strip the reference from core's floor wording: "same against itself" to "same". */
function itself(text: string | null): string {
  if (!text) return "—";
  return text.replace(/ against itself$/, "").replace(/ itself$/, "");
}

function stripPath(path: string): string {
  return path.replace(/^\$\.?/, "") || path;
}

function panel(ctx: Ctx, title: string, lines: string[], note?: string): string[] {
  return box(lines, { width: ctx.w, chars: ctx.chars, paint: ctx.p, title, ...(note ? { note } : {}), ellipsis: ctx.ell });
}

/** Inner width of a panel. */
const innerOf = (ctx: Ctx) => ctx.w - 4;

function muteWrap(ctx: Ctx, text: string, cols: number, indent = 0): string[] {
  return wrap(text, Math.max(8, cols - indent)).map((l) => " ".repeat(indent) + ctx.p.muted(l));
}

// ---------------------------------------------------------------------------
// Panels

export function headerPanel(ctx: Ctx, model: ReportModel): string[] {
  const { p } = ctx;
  const inner = innerOf(ctx);
  const logo = g(ctx, "▁▃▅▇", "::");
  const mark = `${p.brand(logo)} ${p.bold("metergraph")} ${p.treat("replay")}`;
  const created = model.meta.find((m) => m.label === "Created")?.value ?? "";
  const right = p.muted(created);
  const top = width(mark) + 2 + width(right) <= inner ? mark + " ".repeat(inner - width(mark) - width(right)) + right : mark;
  const lines = [top, ""];
  for (const l of wrap(model.headline, inner)) lines.push(p.bold(l));
  for (const l of wrap(model.subhead, inner)) lines.push(l);
  lines.push("");
  const sample = model.view.sample;
  const items: [string, string][] = [
    ["sample", `${formatCount(sample.replayed)} of ${formatCount(sample.cases)} replayed`],
    ["source", model.meta.find((m) => m.label === "Source")?.value ?? "—"],
    ["production", model.productionModel],
    ["spent", (model.meta.find((m) => m.label === "Spent")?.value ?? "—").replace(/ budget$/, "")],
    ["prices", model.meta.find((m) => m.label === "Price catalog")?.value ?? "—"],
  ];
  const sep = p.line(g(ctx, "  ·  ", "  |  "));
  let line = "";
  for (const [label, value] of items) {
    const item = truncate(`${p.muted(label)} ${value}`, inner, ctx.ell);
    if (line && width(line) + width(sep) + width(item) > inner) {
      lines.push(line);
      line = item;
    } else line = line ? line + sep + item : item;
  }
  if (line) lines.push(line);
  if (model.stopped) {
    lines.push("");
    for (const l of wrap(model.stopped, inner)) lines.push(p.emph(l));
  }
  return box(lines, { width: ctx.w, chars: ctx.chars, paint: ctx.p, ellipsis: ctx.ell });
}

/** The figure to print big: the cheapest treatment's monthly cost, else its cost per call. */
function featured(model: ReportModel): { bar: CostBar; big: string; monthly: boolean } | null {
  const treatments = model.cost.filter((c) => c.role === "treatment" && c.perCall != null);
  if (!treatments.length) return null;
  const bar = [...treatments].sort((a, b) => (a.perCall as number) - (b.perCall as number))[0] as CostBar;
  if (bar.monthlyText) return { bar, big: bar.monthlyText.replace(/\/mo$/, ""), monthly: true };
  return { bar, big: bar.text, monthly: false };
}

export function costPanel(ctx: Ctx, model: ReportModel): string[] {
  const { p } = ctx;
  const inner = innerOf(ctx);
  const lines: string[] = [];
  const prod = model.cost.find((c) => c.role === "production");
  const feature = featured(model);
  if (feature) {
    const { bar, big, monthly } = feature;
    const reference = monthly ? prod?.monthlyText : prod?.text;
    const caption = [
      p.bold(`${monthly ? "a month" : "per call"} on ${p.treat(bar.name)}`),
      reference ? `${p.muted("vs")} ${reference} ${p.muted(`on ${model.hasFloor ? `${prod?.name} replayed` : (prod?.name ?? "production")}`)}` : "",
      p.muted(
        monthly && model.monthlyCalls
          ? `${formatCount(model.monthlyCalls)} calls a month at the sample's cost per call`
          : "pass --monthly-calls N to project a month",
      ),
    ];
    const rows = ctx.ascii ? null : bigText(big);
    if (rows && width(rows[0]) + 4 + 30 <= inner) {
      const pad = "  ";
      for (let i = 0; i < 3; i++) lines.push(`${pad}${p.treat(rows[i] as string)}    ${caption[i]}`);
    } else {
      const indent = " ".repeat(width(big) + 2);
      lines.push(`${p.bold(p.treat(big))}  ${caption[0]}`);
      if (caption[1]) lines.push(indent + caption[1]);
      lines.push(indent + caption[2]);
    }
    lines.push("");
  }

  const max = Math.max(0, ...model.cost.map((c) => c.perCall ?? 0));
  const cells = model.cost.map((c) => ({
    name: roleName(ctx, c.role, c.role === "production" ? "production" : c.role === "floor" ? "prod vs itself" : c.name),
    bar: "",
    cost: c.role === "floor" ? p.floor(c.text) : c.text,
    delta: c.role === "floor" ? p.floor(itself(c.delta)) : c.delta ? c.delta : "",
    monthly: c.monthlyText ? (c.role === "treatment" ? p.bold(c.monthlyText) : c.monthlyText) : "",
    saving: c.monthlyDelta ? p.muted(c.monthlyDelta) : "",
    badge: badge(ctx, c.badge),
  }));
  const specs: ColumnSpec[] = [
    { id: "name", max: 26 },
    { id: "bar", flex: true, min: 6, max: 40 },
    { id: "cost", align: "right" },
    { id: "delta" },
    { id: "monthly", align: "right", drop: 1 },
    { id: "saving", drop: 2 },
    { id: "badge", drop: 3 },
  ];
  const l = layout(specs, cells, inner);
  const barWidth = l.columns.find((c) => c.id === "bar")?.width ?? 10;
  model.cost.forEach((c, i) => {
    const filled = hbar(c.perCall ?? 0, max, barWidth, ctx.ascii);
    const color = roleColor(p, c.role);
    (cells[i] as Record<string, string>).bar = color(filled);
    lines.push(renderRow(l, cells[i] as Record<string, string>, ctx.ell));
  });
  lines.push("");
  const notes: string[] = [];
  if (prod && model.hasFloor) {
    const recorded = prod.note?.replace(/ recorded$/, "");
    notes.push(`production here is ${prod.name} replayed the same way as the treatments.${recorded ? ` Its recorded calls cost ${recorded} each.` : ""}`);
  }
  if (!has(l, "monthly") && model.monthlyCalls) notes.push("Monthly projections are not shown at this width.");
  const beyond = model.cost.filter((c) => c.badge?.tone === "beyond");
  if (!has(l, "badge") && beyond.length) notes.push(`Beyond noise: ${beyond.map((c) => `${c.name} (${c.badge?.text.replace(/^beyond noise,? ?/, "") || "different"})`).join(", ")}.`);
  if (!model.monthlyCalls) notes.push("No monthly projection: the run does not record the workload's call volume. Pass --monthly-calls N.");
  for (const n of notes) lines.push(...muteWrap(ctx, n, inner));
  return panel(ctx, "Cost", lines, model.hasFloor ? `per call, against ${prod?.name ?? "production"} replayed` : "per call");
}

const SIGNALS: [string, string][] = [
  ["json_parse_rate", "JSON parses"],
  ["same_keys_rate", "Same keys"],
  ["empty_or_refused_rate", "Empty or refused"],
  ["cut_off_rate", "Cut off"],
  ["output_chars_p50", "Output length"],
  ["errors", "Errors"],
];

export function signalsPanel(ctx: Ctx, model: ReportModel): string[] {
  const { p } = ctx;
  const inner = innerOf(ctx);
  const rows = SIGNALS.map(([key, label]) => ({ key, label, row: model.rows.find((r) => r.key === key) })).filter((r) => r.row);
  const labelW = Math.max(...rows.map((r) => width(r.label)));
  const prodW = Math.max(width("production"), ...rows.map((r) => width(r.row?.production ?? "")));
  const floorW = model.hasFloor ? Math.max(width("vs itself"), ...rows.map((r) => width(itself(r.row?.floor ?? null)))) : 0;
  const valueW = Math.max(4, ...rows.flatMap((r) => r.row?.cells.map((c) => width(c.text)) ?? []));
  const deltaW = Math.max(0, ...rows.flatMap((r) => r.row?.cells.map((c) => width(c.delta ?? "")) ?? []));
  const badgeW = model.labels ? Math.max(0, ...rows.flatMap((r) => r.row?.cells.map((c) => width(badge(ctx, c.label))) ?? [])) : 0;
  const fixed = labelW + 2 + prodW + (floorW ? 2 + floorW : 0);
  const columns = model.columns;
  const longest = Math.max(0, ...model.cost.filter((c) => c.role === "treatment").map((c) => width(c.name)));
  const content = Math.max(12, valueW + 1 + deltaW + (badgeW ? 1 + badgeW : 0));
  const share = Math.floor((inner - fixed) / Math.max(1, columns.length)) - 2;
  const armW = Math.max(content, Math.min(longest, share));
  const perChunk = Math.max(1, Math.floor((inner - fixed) / (armW + 2)));
  const lines: string[] = [];
  for (let start = 0; start < Math.max(1, columns.length); start += perChunk) {
    const chunk = columns.slice(start, start + perChunk).map((col, k) => ({ col, i: start + k }));
    const cellW = chunk.length === 1 ? Math.min(armW + 8, inner - fixed - 2) : armW;
    const head = [fit("", labelW), fit(p.bold(p.prod("production")), prodW, "right")];
    const names = model.cost.filter((c) => c.role === "treatment");
    for (const { i } of chunk) head.push(fit(p.treat(names[i]?.name ?? ""), cellW, "left", ctx.ell));
    if (floorW) head.push(fit(p.floor("vs itself"), floorW, "right"));
    if (start > 0) lines.push("");
    lines.push(head.join("  ").replace(/ +$/, ""));
    lines.push(p.line(g(ctx, "─", "-").repeat(inner)));
    for (const { key, label, row } of rows) {
      if (!row) continue;
      const parts = [fit(label, labelW), fit(row.production, prodW, "right")];
      for (const { i } of chunk) {
        const cell = row.cells[i];
        if (!cell) {
          parts.push(fit("", cellW));
          continue;
        }
        const beyond = model.labels && cell.label?.tone === "beyond";
        const value = beyond ? p.emph(cell.text) : cell.text;
        const delta = cell.delta && cell.delta !== "same" ? cell.delta : cell.delta === "same" ? p.muted("same") : "";
        let text = `${fit(value, valueW, "right")} ${fit(delta, deltaW)}`;
        if (badgeW) text += ` ${badge(ctx, key === "errors" ? null : cell.label)}`;
        parts.push(fit(text, cellW, "left", ctx.ell));
      }
      if (floorW) parts.push(fit(p.floor(itself(row.floor)), floorW, "right"));
      lines.push(parts.join("  ").replace(/ +$/, ""));
    }
  }
  return panel(ctx, "Output signals", lines, "treatments against production");
}

export function fieldsPanel(ctx: Ctx, model: ReportModel, options: { sentences?: boolean; changes?: boolean } = {}): string[] {
  const { p } = ctx;
  const inner = innerOf(ctx);
  const lines: string[] = [];
  if (!model.fields.length) {
    lines.push(...muteWrap(ctx, "No tracked fields. Pass --track $.field (for example --track $.category) to compare a JSON field with production's.", inner));
    return panel(ctx, "Tracked fields", lines);
  }
  model.fields.forEach((field, fi) => {
    if (fi > 0) lines.push("");
    lines.push(...fieldBlock(ctx, field, inner, options));
  });
  const tick = g(ctx, "┆", "|");
  return panel(ctx, "Tracked fields", lines, model.hasFloor ? `${tick} marks production's agreement with itself` : undefined);
}

function fieldBlock(ctx: Ctx, field: FieldPanel, inner: number, options: { sentences?: boolean; changes?: boolean }): string[] {
  const { p } = ctx;
  const lines: string[] = [];
  const title = `${p.bold(stripPath(field.path))}  ${p.muted(`${field.kindLabel.toLowerCase()} field`)}`;
  const floorText = field.floor?.text ? `${p.floor(g(ctx, "┆", "|"))} ${p.floor(`vs itself ${field.floor.text}`)}` : "";
  lines.push(floorText && width(title) + 2 + width(floorText) <= inner ? title + " ".repeat(inner - width(title) - width(floorText)) + floorText : title);
  if (floorText && !lines.at(-1)?.includes("vs itself")) lines.push(floorText);
  const cells = field.arms.map((arm) => ({
    name: p.treat(arm.name),
    bar: "",
    value: arm.badge?.tone === "beyond" ? p.emph(arm.valueText) : arm.valueText,
    detail: p.muted(arm.detail),
    badge: badge(ctx, arm.badge),
  }));
  const rowsForLayout = [...cells];
  if (field.scale === "shift" && field.floor) rowsForLayout.push({ name: p.floor("vs itself"), bar: "", value: "", detail: "", badge: "" });
  const l = layout(
    [
      { id: "name", max: 26 },
      { id: "bar", flex: true, min: 16, max: 36 },
      { id: "value", align: "right" },
      { id: "detail", drop: 2 },
      { id: "badge", drop: 1 },
    ],
    rowsForLayout,
    inner,
  );
  const barW = l.columns.find((c) => c.id === "bar")?.width ?? 10;
  const barX = offsetOf(l, "bar");
  let axis = 1;
  if (field.scale === "shift") {
    const ends = field.arms.flatMap((a) => (a.range ? [a.range.lo, a.range.hi] : []));
    if (field.floor?.low != null && field.floor.high != null) ends.push(field.floor.low, field.floor.high);
    axis = Math.max(1e-9, ...ends.map((v) => Math.abs(v)));
  }
  field.arms.forEach((arm, i) => {
    const cell = cells[i] as Record<string, string>;
    if (field.scale === "share") {
      cell.bar = paintSegments(agreementBar(arm.value, field.floor?.value ?? null, barW, ctx.ascii), { fill: p.treat, track: p.line, tick: (t) => p.bold(p.floor(t)) });
    } else if (field.scale === "shift" && arm.range) {
      cell.bar = paintSegments(rangeBar(arm.range.lo, arm.range.mid, arm.range.hi, -axis, axis, barW, ctx.ascii), { range: p.treat, mark: p.treat, track: p.line });
    }
    lines.push(renderRow(l, cell, ctx.ell));
    if (options.changes && arm.changes.length) {
      const text = arm.changes.map((c) => `${c.from} ${g(ctx, "→", "->")} ${c.to} ${g(ctx, "×", "x")}${c.count}`).join(", ");
      lines.push(...wrap(`changed: ${text}`, inner - barX).map((t) => " ".repeat(barX) + p.muted(t)));
    }
    if (options.sentences !== false) lines.push(...muteWrap(ctx, arm.sentence, inner, barX));
  });
  if (field.scale === "shift" && field.floor && field.floor.low != null && field.floor.high != null) {
    const bar = paintSegments(rangeBar(field.floor.low, field.floor.value, field.floor.high, -axis, axis, barW, ctx.ascii), { range: p.floor, mark: p.floor, track: p.line });
    lines.push(renderRow(l, { name: p.floor("vs itself"), bar, value: p.floor(field.floor.text ?? "") }, ctx.ell));
  }
  return lines;
}

export function latencyPanel(ctx: Ctx, model: ReportModel): string[] {
  const { p } = ctx;
  const inner = innerOf(ctx);
  const rows = model.latency.filter((r) => r.n > 0 || r.p50 != null);
  const lines: string[] = [];
  if (!rows.length) {
    lines.push(p.muted("No latencies recorded."));
    return panel(ctx, "Latency", lines);
  }
  const top = Math.max(1, ...rows.map((r) => Math.max(r.p90 ?? 0, r.p50 ?? 0)));
  const axisMax = niceCeil(top * 1.05);
  const cells = rows.map((r) => ({
    name: roleName(ctx, r.role, r.role === "production" ? "production" : r.role === "floor" ? "prod vs itself" : r.name),
    bar: "",
    median: r.role === "floor" ? p.floor(r.text) : r.text,
    delta: r.role === "floor" ? p.floor(itself(r.delta)) : (r.delta ?? ""),
    spread: "",
    badge: badge(ctx, r.badge),
  }));
  const sparkW = 12;
  for (const c of cells) c.spread = "x".repeat(sparkW);
  const l = layout(
    [
      { id: "name", max: 26 },
      { id: "bar", flex: true, min: 16, max: 44 },
      { id: "median", align: "right" },
      { id: "delta" },
      ...(ctx.ascii ? [] : [{ id: "spread", drop: 2 }]),
      { id: "badge", drop: 1 },
    ],
    cells,
    inner,
  );
  const barW = l.columns.find((c) => c.id === "bar")?.width ?? 10;
  rows.forEach((r, i) => {
    const color = roleColor(p, r.role);
    const cell = cells[i] as Record<string, string>;
    cell.bar = paintSegments(rangeBar(r.p10, r.p50, r.p90, 0, axisMax, barW, ctx.ascii), { range: color, mark: (t) => p.bold(color(t)), track: p.line });
    cell.spread = p.muted(sparkline(histogram(r.values, sparkW, 0, axisMax), ctx.ascii));
    lines.push(renderRow(l, cell, ctx.ell));
  });
  // The shared axis under the bars.
  const x = offsetOf(l, "bar");
  const left = formatMs(0);
  const mid = formatMs(axisMax / 2);
  const right = formatMs(axisMax);
  if (barW >= width(left) + width(mid) + width(right) + 4) {
    const axis = Array.from({ length: barW }, () => " ");
    const place = (text: string, at: number) => {
      const start = Math.max(0, Math.min(barW - text.length, at));
      for (let k = 0; k < text.length; k++) axis[start + k] = text[k] as string;
    };
    place(left, 0);
    place(mid, Math.round((barW - 1) / 2 - mid.length / 2));
    place(right, barW - right.length);
    lines.push(" ".repeat(x) + p.muted(axis.join("")));
  }
  lines.push("");
  const legend = [`${g(ctx, "●", "o")} median`, `${g(ctx, "━━", "==")} 10th to 90th percentile`];
  if (has(l, "spread")) legend.push("▁▃▇ calls by latency");
  lines.push(p.muted(legend.join("   ")));
  if (model.hasFloor) {
    const recorded = model.recordedLatency
      ? ` Its recorded median (${model.recordedLatency.replace(/ /g, NBSP)}) is not compared: it ran at another time, over another network.`
      : "";
    lines.push(...muteWrap(ctx, `production here is its model replayed from this machine.${recorded}`, inner));
  }
  return panel(ctx, "Latency", lines, model.hasFloor ? "median, against production replayed" : "median");
}

function niceCeil(v: number): number {
  if (!(v > 0)) return 1;
  const pow = 10 ** Math.floor(Math.log10(v));
  for (const step of [1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) if (step * pow >= v) return step * pow;
  return 10 * pow;
}

/** The lines describing how one call differs, per treatment that differs. */
export function caseDiffLines(ctx: Ctx, model: ReportModel, c: CaseItem, inner: number, nameW: number): string[] {
  const { p } = ctx;
  const lines: string[] = [];
  const names = model.cost.filter((x) => x.role === "treatment");
  c.arms.forEach((arm, i) => {
    if (!arm.differs) return;
    const name = fit(p.treat(names[i]?.name ?? arm.label), nameW, "left", ctx.ell);
    const parts: string[] = [];
    for (const ch of arm.changes) parts.push(`${stripPath(ch.path)}: ${p.prod(ch.from)} ${p.muted(g(ctx, "→", "->"))} ${p.treat(ch.to)}`);
    if (arm.error) parts.push(`error: ${arm.error}`);
    for (const f of arm.flags) if (!parts.length || f.startsWith("cut off") || f.includes("refusal")) parts.push(f);
    if (!parts.length) parts.push("output differs");
    const room = inner - nameW - 4;
    const text = parts.join(p.muted("; "));
    lines.push(`  ${name}  ${truncate(text, room, ctx.ell)}`);
  });
  return lines;
}

export function differPanel(ctx: Ctx, model: ReportModel, limit = DIFFER_LIMIT): string[] {
  const { p } = ctx;
  const inner = innerOf(ctx);
  const differ = model.cases.filter((c) => c.differs);
  const lines: string[] = [];
  if (!differ.length) {
    lines.push(p.muted("No replayed call differs from production on the tracked fields, errors or refusals."));
    return panel(ctx, "Calls that differ", lines);
  }
  const nameW = Math.min(26, Math.max(...model.cost.filter((c) => c.role === "treatment").map((c) => width(c.name))));
  differ.slice(0, limit).forEach((c, i) => {
    if (i > 0) lines.push("");
    const id = p.bold(c.traceId);
    const input = c.input ? cleanOneLine(c.input) : "";
    lines.push(`${id}  ${p.muted(truncate(input, Math.max(8, inner - width(c.traceId) - 2), ctx.ell))}`);
    lines.push(...caseDiffLines(ctx, model, c, inner, nameW));
  });
  const shown = Math.min(limit, differ.length);
  const replayed = model.cases.filter((c) => c.status === "replayed").length;
  return panel(ctx, "Calls that differ", lines, `${shown} of ${differ.length} differing, of ${replayed} replayed`);
}

function cleanOneLine(text: string): string {
  return text.replace(/\s+/g, " ").trim();
}

export function notReplayablePanel(ctx: Ctx, model: ReportModel): string[] | null {
  if (!model.notReplayable.length) return null;
  const { p } = ctx;
  const inner = innerOf(ctx);
  const countW = Math.max(...model.notReplayable.map((r) => width(formatCount(r.count))));
  const lines: string[] = [];
  model.notReplayable.forEach((r, i) => {
    if (i > 0) lines.push("");
    lines.push(`${fit(formatCount(r.count), countW, "right")}  ${p.bold(r.title)}`);
    lines.push(...muteWrap(ctx, r.fix, inner, countW + 2));
  });
  const total = model.notReplayable.reduce((s, r) => s + r.count, 0);
  return panel(ctx, "Not replayable", lines, `${formatCount(total)} of ${formatCount(model.view.sample.cases)} sampled calls`);
}

export function footer(ctx: Ctx, model: ReportModel, link: string, explore: string | null | undefined): string[] {
  const { p } = ctx;
  const lines: string[] = [];
  const floorNote = model.floor
    ? `Production vs itself: ${model.floor.model} replayed twice (A1 and A2) on the same ${formatCount(model.floor.cases)} calls. The gap between the two is what a rerun of production changes on its own; read every difference beside it. Neither figure says which output is better.`
    : "This run has no noise floor, so differences are shown without production's own variation.";
  for (const l of wrap(floorNote, ctx.w - 2)) lines.push(` ${p.muted(l)}`);
  if (width(NOISE_FLOOR_DOC) + 12 <= ctx.w) lines.push(` ${p.muted("Read more:")} ${p.underline(NOISE_FLOOR_DOC)}`);
  else lines.push(` ${p.muted("Read more:")}`, ` ${p.underline(NOISE_FLOOR_DOC)}`);
  lines.push("");
  const inner = ctx.w - 4;
  const cta = [
    p.bold("Keep this comparison running on live traffic"),
    "",
    `${p.muted("Set it up in one command:")}  ${p.bold(p.treat(SETUP_COMMAND))}`,
  ];
  if (ctx.links) {
    // Clickable: a short label inside the box instead of the long URL.
    cta.push(`${p.muted("Or open it with this run's setup:")}  ${p.underline(hyperlink(link, HOSTED_LABEL))}`);
    cta.push(...wrap("The link carries the setup only: models, settings, tracked fields and sample size.", inner).map((l) => p.muted(l)));
  } else {
    cta.push(...wrap("Or open the link below. It carries the run's setup only: models, settings, tracked fields and sample size.", inner).map((l) => p.muted(l)));
  }
  lines.push(...box(cta, { width: ctx.w, chars: charset(ctx.ascii, "double"), paint: p, border: p.treat, ellipsis: ctx.ell }));
  // Without hyperlinks the link stays whole on its own line so it can be copied; the terminal may wrap it.
  if (!ctx.links) lines.push(` ${p.underline(link)}`);
  if (explore) {
    lines.push("");
    const command = `metergraph-replay report ${cleanLine(explore)}`;
    const lead = "Explore every call:";
    if (width(lead) + width(command) + 3 <= ctx.w) lines.push(` ${p.muted(lead)}  ${p.bold(command)}`);
    else lines.push(` ${p.muted(lead)}`, ` ${p.bold(command)}`);
  }
  return lines;
}

/** Every panel of the static report, top to bottom. */
export function reportLines(ctx: Ctx, model: ReportModel, link: string, explore?: string | null): string[] {
  const sections = [
    headerPanel(ctx, model),
    costPanel(ctx, model),
    signalsPanel(ctx, model),
    // The bars carry the figures; the sentences live in the explorer's Fields tab.
    fieldsPanel(ctx, model, { sentences: false }),
    latencyPanel(ctx, model),
    differPanel(ctx, model),
    notReplayablePanel(ctx, model),
    footer(ctx, model, link, explore),
  ].filter((s): s is string[] => Boolean(s));
  const out: string[] = [];
  sections.forEach((s, i) => {
    if (i > 0) out.push("");
    out.push(...s);
  });
  return out;
}

/** The static report as one string, ending in a newline. */
export function renderReport(run: RunFile, options: TerminalOptions = {}): string {
  const ctx = makeCtx(options);
  const model = terminalModel(run, options);
  const text = reportLines(ctx, model, hostedLink(run, "cli"), options.explore ?? null)
    .map((l) => l.replace(/ +$/, ""))
    .join("\n");
  return `${ctx.ascii ? toAscii(text) : text}\n`;
}
