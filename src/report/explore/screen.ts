// renderScreen(state, options): one full frame of the explorer as text, every
// line exactly `columns` wide, so a redraw overwrites the whole screen with
// one write and nothing flickers. Pure: the terminal is never touched here.

import { formatCount } from "../../core/index.ts";
import { box } from "../term/box.ts";
import { toAscii } from "../term/sanitize.ts";
import { fit, truncate, width } from "../term/width.ts";
import { type Ctx } from "../terminal.ts";
import { contentCtx, detailLines, tabLines } from "./content.ts";
import { CALLS_TAB, type ExplorerState, TABS, bodyHeight, listHeight, visibleCalls } from "./state.ts";

export interface ScreenOptions {
  columns: number;
  rows: number;
  color: boolean;
  ascii: boolean;
}

/** The frame as lines joined by "\n". */
export function renderScreen(state: ExplorerState, options: ScreenOptions): string {
  return screenLines(state, options).join("\n");
}

export function screenLines(state: ExplorerState, options: ScreenOptions): string[] {
  const columns = Math.max(1, options.columns);
  const rows = Math.max(3, options.rows);
  const sized = { ...state, columns, rows };
  const ctx = contentCtx(columns, options.color, options.ascii);
  const height = bodyHeight(sized);
  const body = state.detail ? detailBody(sized, ctx, height) : state.tab === CALLS_TAB ? callList(sized, ctx, height) : scrolled(tabLines(state.data, state.tab, ctx), state.scroll[state.tab] ?? 0, height);
  const lines = [tabBar(sized, ctx), ctx.p.line((options.ascii ? "-" : "─").repeat(columns)), ...body];
  while (lines.length < rows - 1) lines.push("");
  lines.length = rows - 1;
  lines.push(statusBar(sized, ctx));
  let out = lines.map((l, i) => (i === rows - 1 ? l : fit(l, columns, "left", ctx.ell)));
  if (state.help) out = overlay(out, helpBox(ctx), columns);
  if (options.ascii) out = out.map(toAscii);
  return out;
}

function scrolled(lines: string[], at: number, height: number): string[] {
  const max = Math.max(0, lines.length - height);
  const start = Math.min(Math.max(0, at), max);
  return lines.slice(start, start + height);
}

function tabBar(state: ExplorerState, ctx: Ctx): string {
  const { p } = ctx;
  const logo = `${p.brand(ctx.ascii ? "::" : "▁▃▅▇")} ${p.bold("replay")}`;
  const full = TABS.map((t, i) => `${i + 1} ${t}`);
  const short = TABS.map((t, i) => `${i + 1} ${t.split(" ")[0]}`);
  const bare = TABS.map((_, i) => `${i + 1}`);
  for (const names of [full, short, bare]) {
    const tabs = names.map((t, i) => (i === state.tab ? p.tab(` ${t} `) : p.muted(` ${t} `)));
    const line = ` ${logo}  ${tabs.join(" ")}`;
    if (width(line) <= state.columns) return line;
  }
  return truncate(` ${logo}`, state.columns, ctx.ell);
}

function position(state: ExplorerState, ctx: Ctx): string {
  if (state.detail) {
    const list = visibleCalls(state);
    const at = list.indexOf(state.detail.index);
    const lines = detailLines(state.data, state.detail.index, contentCtx(state.columns, false, ctx.ascii)).length;
    const call = at >= 0 ? `call ${at + 1} of ${list.length}` : "call";
    return `${call}, ${percent(state.detail.scroll, lines, bodyHeight(state))}`;
  }
  if (state.tab === CALLS_TAB) {
    const list = visibleCalls(state);
    const total = state.data.model.cases.length;
    const shown = list.length === total ? `${formatCount(total)} calls` : `${formatCount(list.length)} of ${formatCount(total)} calls`;
    return list.length ? `${state.cursor + 1}/${list.length}, ${shown}` : shown;
  }
  const lines = tabLines(state.data, state.tab, contentCtx(state.columns, false, ctx.ascii)).length;
  return percent(state.scroll[state.tab] ?? 0, lines, bodyHeight(state));
}

function percent(at: number, lines: number, height: number): string {
  if (lines <= height) return "all";
  const max = lines - height;
  if (at <= 0) return "top";
  if (at >= max) return "end";
  return `${Math.round((at / max) * 100)}%`;
}

function hints(state: ExplorerState): string[] {
  if (state.typing) return ["type to filter", "enter done", "esc clear"];
  if (state.detail) return ["↑↓ scroll", "n/p next call", "esc back", "? help", "q quit"];
  if (state.tab === CALLS_TAB) return ["↑↓ move", "enter open", "d differ", "/ filter", "←→ tabs", "? help", "q quit"];
  return ["↑↓ scroll", "←→ tabs", "? help", "q quit"];
}

function statusBar(state: ExplorerState, ctx: Ctx): string {
  const { p } = ctx;
  const tab = TABS[state.tab] as string;
  const where = state.detail ? `${tab} › ${state.data.model.cases[state.detail.index]?.traceId ?? ""}` : tab;
  const flags: string[] = [];
  if (state.onlyDiffer) flags.push("only calls that differ");
  if (state.filter || state.typing) flags.push(`filter: ${state.filter}${state.typing ? "_" : ""}`);
  const left = ` ${state.data.name}  ${where}  ${position(state, ctx)}${flags.length ? `  [${flags.join(", ")}]` : ""} `;
  const keys = hints(state);
  let right = "";
  for (let n = keys.length; n > 0; n--) {
    const text = ` ${keys.slice(0, n).join("  ")} `;
    if (width(left) + width(text) <= state.columns) {
      right = text;
      break;
    }
  }
  const gap = Math.max(0, state.columns - width(left) - width(right));
  const line = truncate(left + " ".repeat(gap) + right, state.columns, ctx.ell);
  return p.bar(fit(line, state.columns));
}

function callList(state: ExplorerState, ctx: Ctx, height: number): string[] {
  const { p } = ctx;
  const model = state.data.model;
  const list = visibleCalls(state);
  const cols = Math.min(state.columns, 160);
  const idW = Math.min(22, Math.max(8, ...model.cases.map((c) => width(c.traceId))));
  const statusW = 18;
  const lines: string[] = [];
  const mark = (on: boolean) => (on ? p.treat(ctx.ascii ? "*" : "●") : p.muted(ctx.ascii ? "." : "·"));
  lines.push(p.muted(`    ${fit("trace", idW)}  ${fit("status", statusW)}  input`));
  lines.push(p.line((ctx.ascii ? "-" : "─").repeat(cols)));
  if (!list.length) {
    lines.push(p.muted(state.filter ? `  No call matches "${state.filter}".` : "  No call differs from production."));
    return lines;
  }
  const top = state.scroll[CALLS_TAB] ?? 0;
  for (let i = top; i < Math.min(list.length, top + listHeight(state)); i++) {
    const c = model.cases[list[i] as number];
    if (!c) continue;
    const status = c.status === "replayed" ? (c.differs ? "differs" : "same") : (c.reason ?? "not replayable");
    const selected = i === state.cursor;
    const pointer = selected ? (ctx.ascii ? ">" : "›") : " ";
    const statusText = c.status === "replayed" ? (c.differs ? p.treat(status) : p.muted(status)) : p.floor(status);
    const input = (c.input ?? "").replace(/\s+/g, " ").trim();
    const row = `${pointer} ${mark(c.differs)} ${fit(selected ? p.bold(c.traceId) : c.traceId, idW, "left", ctx.ell)}  ${fit(statusText, statusW, "left", ctx.ell)}  ${input}`;
    const fitted = fit(row, cols, "left", ctx.ell);
    lines.push(selected ? p.select(fitted) : fitted);
  }
  return lines.slice(0, height);
}

function detailBody(state: ExplorerState, ctx: Ctx, height: number): string[] {
  if (!state.detail) return [];
  return scrolled(detailLines(state.data, state.detail.index, ctx), state.detail.scroll, height);
}

const HELP: [string, string][] = [
  ["← → or 1-5", "switch tabs"],
  ["↑ ↓  j k", "move or scroll"],
  ["PgUp PgDn  space", "a page"],
  ["g G  Home End", "first, last"],
  ["enter", "open the call"],
  ["esc  backspace", "back"],
  ["n p", "next, previous call"],
  ["d", "only calls that differ"],
  ["/", "filter calls by text"],
  ["?", "this help"],
  ["q", "quit"],
];

function helpBox(ctx: Ctx): string[] {
  const keyW = Math.max(...HELP.map(([k]) => width(k)));
  const lines = HELP.map(([k, v]) => `${ctx.p.bold(fit(k, keyW))}  ${v}`);
  lines.push("");
  lines.push(ctx.p.muted("Any key closes this."));
  const w = Math.min(ctx.w, keyW + 2 + Math.max(...HELP.map(([, v]) => width(v))) + 4);
  return box(lines, { width: w, chars: ctx.chars, paint: ctx.p, title: "Keys", ellipsis: ctx.ell });
}

/** Draw `panel` over the middle of `lines`. */
function overlay(lines: string[], panel: string[], columns: number): string[] {
  const out = [...lines];
  const top = Math.max(2, Math.floor((lines.length - panel.length) / 2));
  const w = Math.max(...panel.map((l) => width(l)));
  const left = Math.max(0, Math.floor((columns - w) / 2));
  panel.forEach((l, i) => {
    const row = top + i;
    if (row >= out.length - 1) return;
    out[row] = fit(" ".repeat(left) + l, columns);
  });
  return out;
}
