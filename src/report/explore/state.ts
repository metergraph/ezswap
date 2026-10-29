// The explorer's state and its pure reducer: reduce(state, key) returns the
// next state and never touches the terminal. Scroll positions are clamped
// against the same lines the screen draws, so the reducer and the renderer
// cannot disagree about where a page ends.

import type { RunFile } from "../../types.ts";
import { hostedLink } from "../link.ts";
import type { CaseItem, ReportModel } from "../model.ts";
import { terminalModel } from "../terminal.ts";
import { contentCtx, detailLines, tabLines } from "./content.ts";

export const TABS = ["Summary", "Fields", "Noise floor", "Calls", "Not replayable"] as const;
export const CALLS_TAB = 3;

/** What the explorer shows, prepared once per run. */
export interface ExplorerData {
  model: ReportModel;
  /** The hosted link for the Summary footer. */
  link: string;
  /** The run's name for the status bar, such as replay-out/run.json. */
  name: string;
  /** The model was made ASCII-only. */
  ascii: boolean;
}

export interface ExplorerOptions {
  tracked?: string[];
  labels?: boolean;
  monthlyCalls?: number | null;
  ascii?: boolean;
  name?: string;
}

export function explorerData(run: RunFile, options: ExplorerOptions = {}): ExplorerData {
  return {
    model: terminalModel(run, {
      ...(options.tracked ? { tracked: options.tracked } : {}),
      ...(options.labels ? { labels: true } : {}),
      ...(options.monthlyCalls != null ? { monthlyCalls: options.monthlyCalls } : {}),
      ascii: Boolean(options.ascii),
    }),
    link: hostedLink(run, "cli"),
    name: options.name ?? "run.json",
    ascii: Boolean(options.ascii),
  };
}

export interface ExplorerState {
  readonly data: ExplorerData;
  /** Index into TABS. */
  tab: number;
  /** Scroll offset per tab, in lines (the Calls tab scrolls its list). */
  scroll: number[];
  /** The highlighted row of the call list, an index into the filtered list. */
  cursor: number;
  onlyDiffer: boolean;
  filter: string;
  /** Typing into the filter. */
  typing: boolean;
  /** An open call: its index in model.cases, and how far it is scrolled. */
  detail: { index: number; scroll: number } | null;
  help: boolean;
  quit: boolean;
  columns: number;
  rows: number;
}

export function initialState(data: ExplorerData, size: { columns: number; rows: number }, tab = 0): ExplorerState {
  return {
    data,
    tab,
    scroll: TABS.map(() => 0),
    cursor: 0,
    onlyDiffer: false,
    filter: "",
    typing: false,
    detail: null,
    help: false,
    quit: false,
    columns: Math.max(1, size.columns),
    rows: Math.max(1, size.rows),
  };
}

/** Rows between the tab bar and the status bar. */
export function bodyHeight(state: { rows: number }): number {
  return Math.max(1, state.rows - 3);
}

/** Rows the call list has for calls, below its column header. */
export function listHeight(state: { rows: number }): number {
  return Math.max(1, bodyHeight(state) - 2);
}

function haystack(c: CaseItem): string {
  return [c.traceId, c.id, c.input ?? "", c.reason ?? "", c.production.output ?? "", ...c.arms.flatMap((a) => [a.output ?? "", a.error ?? ""])]
    .join("\n")
    .toLowerCase();
}

/** Indexes into model.cases that the call list shows, after "only differ" and the text filter. */
export function visibleCalls(state: Pick<ExplorerState, "data" | "onlyDiffer" | "filter">): number[] {
  const needle = state.filter.trim().toLowerCase();
  const out: number[] = [];
  state.data.model.cases.forEach((c, i) => {
    if (state.onlyDiffer && !c.differs) return;
    if (needle && !haystack(c).includes(needle)) return;
    out.push(i);
  });
  return out;
}

/** Lines a scrollable tab or the open call takes at this width (drawn without colour; colour never changes a line count). */
function countLines(state: ExplorerState): number {
  return tabLines(state.data, state.tab, contentCtx(state.columns, false, state.data.ascii)).length;
}

function countDetail(state: ExplorerState): number {
  if (!state.detail) return 0;
  return detailLines(state.data, state.detail.index, contentCtx(state.columns, false, state.data.ascii)).length;
}

const clamp = (v: number, lo: number, hi: number) => Math.min(Math.max(v, lo), Math.max(lo, hi));

function withScroll(state: ExplorerState, tab: number, value: number): ExplorerState {
  const scroll = [...state.scroll];
  const max = Math.max(0, countLines({ ...state, tab }) - bodyHeight(state));
  scroll[tab] = clamp(value, 0, max);
  return { ...state, scroll };
}

/** Keep the cursor in range and on screen. */
function settleList(state: ExplorerState): ExplorerState {
  const n = visibleCalls(state).length;
  const cursor = clamp(state.cursor, 0, n - 1);
  const page = listHeight(state);
  let top = state.scroll[CALLS_TAB] ?? 0;
  if (cursor < top) top = cursor;
  if (cursor >= top + page) top = cursor - page + 1;
  top = clamp(top, 0, n - page);
  const scroll = [...state.scroll];
  scroll[CALLS_TAB] = top;
  return { ...state, cursor, scroll };
}

function scrollDetail(state: ExplorerState, value: number): ExplorerState {
  if (!state.detail) return state;
  const max = Math.max(0, countDetail(state) - bodyHeight(state));
  return { ...state, detail: { ...state.detail, scroll: clamp(value, 0, max) } };
}

function switchTab(state: ExplorerState, tab: number): ExplorerState {
  const next = ((tab % TABS.length) + TABS.length) % TABS.length;
  return { ...state, tab: next, detail: null, typing: false };
}

/** A key, as parseKeys names it: "up", "pagedown", "enter", "escape", "ctrl-c", or one printable character. */
export type Key = string;

export function reduce(state: ExplorerState, key: Key): ExplorerState {
  if (state.quit) return state;
  if (key === "ctrl-c") return { ...state, quit: true };

  if (state.typing) {
    if (key === "enter" || key === "tab") return settleList({ ...state, typing: false });
    if (key === "escape") return settleList({ ...state, typing: false, filter: "", cursor: 0 });
    if (key === "backspace") {
      if (!state.filter) return { ...state, typing: false };
      return settleList({ ...state, filter: state.filter.slice(0, -1), cursor: 0 });
    }
    if (key === "up" || key === "down") return reduce({ ...state, typing: false }, key);
    if ([...key].length === 1 && key >= " ") return settleList({ ...state, filter: state.filter + key, cursor: 0 });
    return state;
  }

  if (state.help) {
    if (key === "q") return { ...state, quit: true };
    return { ...state, help: false };
  }
  if (key === "q") return { ...state, quit: true };
  if (key === "?") return { ...state, help: true };
  if (key === "left") return switchTab(state, state.tab - 1);
  if (key === "right" || key === "tab") return switchTab(state, state.tab + 1);
  if (/^[1-5]$/.test(key)) return switchTab(state, Number(key) - 1);

  const page = bodyHeight(state);
  if (state.detail) {
    const at = state.detail.scroll;
    switch (key) {
      case "escape":
      case "backspace":
        return settleList({ ...state, detail: null });
      case "up":
      case "k":
        return scrollDetail(state, at - 1);
      case "down":
      case "j":
        return scrollDetail(state, at + 1);
      case "pageup":
        return scrollDetail(state, at - page);
      case "pagedown":
      case " ":
        return scrollDetail(state, at + page);
      case "home":
      case "g":
        return scrollDetail(state, 0);
      case "end":
      case "G":
        return scrollDetail(state, Number.MAX_SAFE_INTEGER);
      case "n":
      case "p": {
        const list = visibleCalls(state);
        const here = list.indexOf(state.detail.index);
        const next = list[clamp(here + (key === "n" ? 1 : -1), 0, list.length - 1)];
        if (next === undefined) return state;
        return { ...state, cursor: list.indexOf(next), detail: { index: next, scroll: 0 } };
      }
      default:
        return state;
    }
  }

  if (state.tab === CALLS_TAB) {
    const list = visibleCalls(state);
    const lpage = listHeight(state);
    switch (key) {
      case "up":
      case "k":
        return settleList({ ...state, cursor: state.cursor - 1 });
      case "down":
      case "j":
        return settleList({ ...state, cursor: state.cursor + 1 });
      case "pageup":
        return settleList({ ...state, cursor: state.cursor - lpage });
      case "pagedown":
      case " ":
        return settleList({ ...state, cursor: state.cursor + lpage });
      case "home":
      case "g":
        return settleList({ ...state, cursor: 0 });
      case "end":
      case "G":
        return settleList({ ...state, cursor: list.length - 1 });
      case "enter": {
        const index = list[state.cursor];
        return index === undefined ? state : { ...state, detail: { index, scroll: 0 } };
      }
      case "d":
        return settleList({ ...state, onlyDiffer: !state.onlyDiffer, cursor: 0 });
      case "/":
        return { ...state, typing: true };
      case "escape":
        return state.filter ? settleList({ ...state, filter: "", cursor: 0 }) : state;
      default:
        return state;
    }
  }

  const at = state.scroll[state.tab] ?? 0;
  switch (key) {
    case "up":
    case "k":
      return withScroll(state, state.tab, at - 1);
    case "down":
    case "j":
      return withScroll(state, state.tab, at + 1);
    case "pageup":
      return withScroll(state, state.tab, at - page);
    case "pagedown":
    case " ":
      return withScroll(state, state.tab, at + page);
    case "home":
    case "g":
      return withScroll(state, state.tab, 0);
    case "end":
    case "G":
      return withScroll(state, state.tab, Number.MAX_SAFE_INTEGER);
    case "d":
    case "/":
      // The call list's keys jump to it from any tab.
      return reduce(switchTab(state, CALLS_TAB), key);
    default:
      return state;
  }
}

/** The terminal changed size: keep every position valid. */
export function resize(state: ExplorerState, columns: number, rows: number): ExplorerState {
  let next: ExplorerState = { ...state, columns: Math.max(1, columns), rows: Math.max(1, rows) };
  for (let tab = 0; tab < TABS.length; tab++) if (tab !== CALLS_TAB) next = withScroll(next, tab, next.scroll[tab] ?? 0);
  next = settleList(next);
  if (next.detail) next = scrollDetail(next, next.detail.scroll);
  return next;
}

const KEYS: [string, Key][] = [
  ["\x1b[A", "up"],
  ["\x1b[B", "down"],
  ["\x1b[C", "right"],
  ["\x1b[D", "left"],
  ["\x1bOA", "up"],
  ["\x1bOB", "down"],
  ["\x1bOC", "right"],
  ["\x1bOD", "left"],
  ["\x1b[5~", "pageup"],
  ["\x1b[6~", "pagedown"],
  ["\x1b[H", "home"],
  ["\x1b[F", "end"],
  ["\x1bOH", "home"],
  ["\x1bOF", "end"],
  ["\x1b[1~", "home"],
  ["\x1b[4~", "end"],
  ["\x1b[7~", "home"],
  ["\x1b[8~", "end"],
  ["\x1b[Z", "left"],
];

/** Split raw terminal input into named keys. Unknown escape sequences are dropped. */
export function parseKeys(input: string): Key[] {
  const keys: Key[] = [];
  let i = 0;
  while (i < input.length) {
    const rest = input.slice(i);
    const known = KEYS.find(([seq]) => rest.startsWith(seq));
    if (known) {
      keys.push(known[1]);
      i += known[0].length;
      continue;
    }
    if (rest.startsWith("\x1b[") || rest.startsWith("\x1bO")) {
      // An unknown CSI or SS3 sequence: skip to its final byte.
      const m = rest.match(/^\x1b(?:\[[0-?]*[ -\/]*[@-~]|O.)/);
      i += m ? m[0].length : 1;
      continue;
    }
    const ch = String.fromCodePoint(rest.codePointAt(0) as number);
    i += ch.length;
    if (ch === "\x1b") keys.push("escape");
    else if (ch === "\r" || ch === "\n") keys.push("enter");
    else if (ch === "\x7f" || ch === "\b") keys.push("backspace");
    else if (ch === "\t") keys.push("tab");
    else if (ch === "\x03") keys.push("ctrl-c");
    else if (ch === "\x04") keys.push("q");
    else if (ch >= " ") keys.push(ch);
  }
  return keys;
}
