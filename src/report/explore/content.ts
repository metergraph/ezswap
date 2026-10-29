// What each explorer tab and the call detail show, as lines at a width. Pure:
// the same data, width and style always give the same lines, which is what
// lets the reducer clamp scrolling against them.

import { formatCount } from "../../core/index.ts";
import type { Role } from "../term/ansi.ts";
import { DIVIDER, box, rule } from "../term/box.ts";
import { fit, truncate, width, wrap } from "../term/width.ts";
import type { ArmOutput, CaseItem } from "../model.ts";
import { type Ctx, MAX_WIDTH, fieldsPanel, makeCtx, notReplayablePanel, reportLines } from "../terminal.ts";
import type { ExplorerData } from "./state.ts";

export function contentCtx(columns: number, color: boolean, ascii: boolean): Ctx {
  return makeCtx({ width: Math.min(columns, MAX_WIDTH), color, ascii });
}

const memo = new WeakMap<ExplorerData, Map<string, string[]>>();

function cached(data: ExplorerData, key: string, build: () => string[]): string[] {
  let map = memo.get(data);
  if (!map) {
    map = new Map();
    memo.set(data, map);
  }
  const hit = map.get(key);
  if (hit) return hit;
  const lines = build();
  map.set(key, lines);
  return lines;
}

const keyOf = (ctx: Ctx) => `${ctx.w}|${ctx.p.color ? 1 : 0}|${ctx.ascii ? 1 : 0}`;

/** A scrollable tab's lines. The Calls tab is a list the screen draws itself, so it has none here. */
export function tabLines(data: ExplorerData, tab: number, ctx: Ctx): string[] {
  return cached(data, `tab${tab}|${keyOf(ctx)}`, () => {
    switch (tab) {
      case 0:
        return reportLines(ctx, data.model, data.link, null);
      case 1:
        return fieldsTab(ctx, data);
      case 2:
        return floorTab(ctx, data);
      case 4:
        return notReplayableTab(ctx, data);
      default:
        return [];
    }
  });
}

function fieldsTab(ctx: Ctx, data: ExplorerData): string[] {
  const lines = fieldsPanel(ctx, data.model, { sentences: true, changes: true });
  const floor = data.model.floor;
  if (floor?.fields.some((f) => f.mismatches.length)) {
    lines.push("");
    lines.push(...floorMismatches(ctx, data));
  }
  return lines;
}

function floorMismatches(ctx: Ctx, data: ExplorerData): string[] {
  const { p } = ctx;
  const floor = data.model.floor;
  if (!floor) return [];
  const inner = ctx.w - 4;
  const lines: string[] = [];
  const arrow = ctx.ascii ? "->" : "→";
  floor.fields.forEach((f, i) => {
    if (i > 0) lines.push("");
    lines.push(`${p.bold(f.path.replace(/^\$\.?/, ""))}  ${p.floor(f.text ?? "—")}`);
    if (!f.mismatches.length) {
      lines.push(p.muted("  A1 and A2 agreed on every call."));
      return;
    }
    const idW = Math.min(24, Math.max(...f.mismatches.map((m) => width(m.traceId))));
    for (const m of f.mismatches) lines.push(truncate(`  ${fit(m.traceId, idW)}  ${p.floor(m.a1)} ${p.muted(arrow)} ${p.floor(m.a2)}`, inner, ctx.ell));
  });
  return box(lines, { width: ctx.w, chars: ctx.chars, paint: p, title: "Where production disagreed with itself", note: "A1 then A2", ellipsis: ctx.ell });
}

function floorTab(ctx: Ctx, data: ExplorerData): string[] {
  const { p } = ctx;
  const floor = data.model.floor;
  const inner = ctx.w - 4;
  if (!floor) {
    return box(wrap("This run has no noise floor: it was made with --no-noise-floor, so there is no reading of how much production differs from itself. Run again without that option to measure it.", inner).map((l) => p.muted(l)), {
      width: ctx.w,
      chars: ctx.chars,
      paint: p,
      title: "Noise floor",
      ellipsis: ctx.ell,
    });
  }
  const lines: string[] = [];
  floor.text.forEach((t, i) => {
    if (i > 0) lines.push("");
    lines.push(...wrap(t, inner));
  });
  lines.push("");
  lines.push(p.muted(`${formatCount(floor.cases)} calls replayed twice on ${floor.model}, for ${floor.spent}.`));
  lines.push(DIVIDER);
  const labelW = Math.max(...floor.stats.map((s) => width(s.label)));
  const a1W = Math.max(2, ...floor.stats.map((s) => width(s.a1)));
  const a2W = Math.max(2, ...floor.stats.map((s) => width(s.a2)));
  lines.push(`${fit("", labelW)}  ${fit(p.floor("A1"), a1W, "right")}  ${fit(p.floor("A2"), a2W, "right")}  ${p.floor("gap")}`);
  for (const s of floor.stats) lines.push(`${fit(s.label, labelW)}  ${fit(s.a1, a1W, "right")}  ${fit(s.a2, a2W, "right")}  ${p.floor(s.gap)}`);
  const out = box(lines, { width: ctx.w, chars: ctx.chars, paint: p, title: "Noise floor", note: "production against itself", ellipsis: ctx.ell });
  if (floor.fields.length) {
    out.push("");
    out.push(...floorMismatches(ctx, data));
  }
  return out;
}

function notReplayableTab(ctx: Ctx, data: ExplorerData): string[] {
  const { p } = ctx;
  const model = data.model;
  const summary = notReplayablePanel(ctx, model);
  if (!summary) return box([p.muted("Every sampled call was replayed.")], { width: ctx.w, chars: ctx.chars, paint: p, title: "Not replayable", ellipsis: ctx.ell });
  const calls = model.cases.filter((c) => c.status === "not_replayable");
  const inner = ctx.w - 4;
  const idW = Math.min(24, Math.max(...calls.map((c) => width(c.traceId))));
  const lines = calls.map((c) => truncate(`${fit(p.bold(c.traceId), idW)}  ${fit(c.reason ?? "—", 24, "left", ctx.ell)}  ${p.muted(oneLine(c.input ?? ""))}`, inner, ctx.ell));
  return [...summary, "", ...box(lines, { width: ctx.w, chars: ctx.chars, paint: p, title: "Calls not replayed", note: "enter one from the Calls tab", ellipsis: ctx.ell })];
}

const oneLine = (text: string) => text.replace(/\s+/g, " ").trim();

// ---------------------------------------------------------------------------
// The call detail

const MIN_PANE = 36;

/** The top-level key a JSON path names last: "$.a.b" to "b". */
function lastKey(path: string): string {
  const m = path.match(/(?:\.|\[["']?)([^.[\]"']+)["']?\]?$/);
  return m ? (m[1] as string) : path;
}

function paneLines(ctx: Ctx, out: ArmOutput, role: Role, cols: number, changed: Set<string>): string[] {
  const { p } = ctx;
  const inner = cols - 4;
  const lines: string[] = [];
  const meta = [`finish ${out.finishReason ?? "—"}`, out.cost, out.latency].join(ctx.ascii ? " | " : " · ");
  lines.push(p.muted(truncate(meta, inner, ctx.ell)));
  for (const f of out.flags) lines.push(...wrap(f, inner).map((l) => p.emph(l)));
  if (out.error) lines.push(...wrap(`error: ${out.error}`, inner).map((l) => p.emph(l)));
  lines.push("");
  const mark = ctx.ascii ? ">" : "▌";
  const color = role === "production" ? p.prod : role === "treatment" ? p.treat : p.floor;
  const body = out.pretty ?? out.output;
  if (body == null || body === "") {
    lines.push(p.muted(out.status === "missing" ? (out.error ?? "No result") : "No output"));
    return lines;
  }
  const keyPattern = changed.size ? new RegExp(`^\\s*"(${[...changed].map((k) => k.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|")})":`) : null;
  for (const source of body.split("\n")) {
    const hit = Boolean(keyPattern && out.pretty && keyPattern.test(source));
    for (const piece of wrap(source, inner - 2)) lines.push(hit ? `${color(mark)} ${p.bold(piece)}` : `  ${piece}`);
  }
  return lines;
}

function panes(ctx: Ctx, items: { out: ArmOutput; role: Role; title: string; changed: Set<string> }[]): string[] {
  if (!items.length) return [];
  const { p } = ctx;
  const perRow = Math.max(1, Math.min(items.length, Math.floor((ctx.w + 1) / (MIN_PANE + 1))));
  const lines: string[] = [];
  for (let start = 0; start < items.length; start += perRow) {
    const row = items.slice(start, start + perRow);
    const cols = Math.floor((ctx.w - (row.length - 1)) / row.length);
    const contents = row.map((it) => paneLines(ctx, it.out, it.role, cols, it.changed));
    const height = Math.max(...contents.map((c) => c.length));
    const boxes = row.map((it, k) => {
      const content = contents[k] as string[];
      while (content.length < height) content.push("");
      const color = it.role === "production" ? p.prod : it.role === "treatment" ? p.treat : p.floor;
      const status = it.out.status === "failed" ? "failed" : it.out.status === "missing" ? "no result" : it.out.differs ? "differs" : "";
      return box(content, { width: cols, chars: ctx.chars, paint: p, title: color(it.title), ...(status ? { note: status } : {}), ellipsis: ctx.ell });
    });
    if (start > 0) lines.push("");
    for (let i = 0; i < height + 2; i++) lines.push(boxes.map((b) => b[i] ?? "").join(" "));
  }
  return lines;
}

/** Everything about one call: its input, then production's output beside every arm's. */
export function detailLines(data: ExplorerData, index: number, ctx: Ctx): string[] {
  return cached(data, `detail${index}|${keyOf(ctx)}`, () => {
    const c = data.model.cases[index];
    return c ? detail(ctx, data, c) : [];
  });
}

function detail(ctx: Ctx, data: ExplorerData, c: CaseItem): string[] {
  const { p } = ctx;
  const model = data.model;
  const lines: string[] = [];
  const status = c.status === "replayed" ? (c.differs ? "replayed, differs from production" : "replayed") : `not replayable: ${c.reason ?? "—"}`;
  lines.push(`${p.bold(c.traceId)}  ${p.muted(status)}`);
  for (const l of c.limitations) lines.push(p.muted(`note: ${l}`));
  lines.push("");
  lines.push(rule(ctx.w, ctx.chars, p, "Input"));
  if (c.system) {
    lines.push(p.muted("system"));
    for (const l of wrap(c.system, ctx.w - 2)) lines.push(`  ${p.muted(l)}`);
  }
  if (c.messages.length) {
    for (const m of c.messages) {
      lines.push(p.bold(m.role));
      for (const l of wrap(m.content, ctx.w - 2)) lines.push(`  ${l}`);
    }
  } else if (c.input) {
    for (const l of wrap(c.input, ctx.w - 2)) lines.push(`  ${l}`);
  } else {
    lines.push(p.muted("  The trace carries no input text."));
  }
  lines.push("");
  lines.push(rule(ctx.w, ctx.chars, p, "Outputs"));
  const names = model.cost.filter((x) => x.role === "treatment").map((x) => x.name);
  const changedAll = new Set(c.arms.flatMap((a) => a.changes.map((ch) => lastKey(ch.path))));
  const items: { out: ArmOutput; role: Role; title: string; changed: Set<string> }[] = [
    { out: c.production, role: "production", title: `production (${model.productionModel})`, changed: changedAll },
    ...c.arms.map((a, i) => ({ out: a, role: "treatment" as Role, title: names[i] ?? a.label, changed: new Set(a.changes.map((ch) => lastKey(ch.path))) })),
  ];
  lines.push(...panes(ctx, items));
  const changes = c.arms.flatMap((a, i) => a.changes.map((ch) => `${names[i] ?? a.label}  ${ch.path.replace(/^\$\.?/, "")}: ${ch.from} ${ctx.ascii ? "->" : "→"} ${ch.to}`));
  if (changes.length) {
    lines.push("");
    for (const ch of changes) lines.push(truncate(`${p.treat(ctx.ascii ? ">" : "▌")} ${ch}`, ctx.w, ctx.ell));
  }
  if (c.floor.length) {
    lines.push("");
    lines.push(rule(ctx.w, ctx.chars, p, "Noise floor replays"));
    lines.push(...panes(ctx, c.floor.map((f) => ({ out: f, role: "floor" as Role, title: f.label, changed: new Set<string>() }))));
  }
  return lines;
}
