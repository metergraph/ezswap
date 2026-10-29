import { test } from "node:test";
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { readFileSync } from "node:fs";
import { ENTER, LEAVE, frameOutput, runExplorer } from "../src/report/explore/run.ts";
import { screenLines } from "../src/report/explore/screen.ts";
import { CALLS_TAB, type ExplorerState, TABS, bodyHeight, explorerData, initialState, parseKeys, reduce, resize, visibleCalls } from "../src/report/explore/state.ts";
import { width } from "../src/report/term/width.ts";
import type { RunFile } from "../src/types.ts";

const demo: RunFile = JSON.parse(readFileSync(new URL("../demo/run.json", import.meta.url), "utf8"));
const data = explorerData(demo, { name: "replay-out/run.json" });
const start = (columns = 80, rows = 24, tab = 0) => initialState(data, { columns, rows }, tab);
const press = (state: ExplorerState, ...keys: string[]) => keys.reduce(reduce, state);

test("parseKeys names arrows, paging, editing keys and characters", () => {
  assert.deepEqual(parseKeys("\x1b[A\x1b[B\x1b[C\x1b[D\x1bOA"), ["up", "down", "right", "left", "up"]);
  assert.deepEqual(parseKeys("\x1b[5~\x1b[6~\x1b[H\x1b[F\x1b[1~\x1b[4~"), ["pageup", "pagedown", "home", "end", "home", "end"]);
  assert.deepEqual(parseKeys("\r\x7f\x1b\t\x03"), ["enter", "backspace", "escape", "tab", "ctrl-c"]);
  assert.deepEqual(parseKeys("q/jk?é"), ["q", "/", "j", "k", "?", "é"]);
  assert.deepEqual(parseKeys("\x1b[200~x"), ["x"], "an unknown sequence is dropped");
});

test("tabs: arrows and digits switch, and wrap around", () => {
  let s = start();
  assert.equal(s.tab, 0);
  s = reduce(s, "right");
  assert.equal(s.tab, 1);
  s = reduce(s, "left");
  s = reduce(s, "left");
  assert.equal(s.tab, TABS.length - 1, "left from the first tab wraps to the last");
  for (const [key, tab] of [["1", 0], ["2", 1], ["3", 2], ["4", 3], ["5", 4]] as const) assert.equal(reduce(s, key).tab, tab);
  assert.equal(reduce(start(), "tab").tab, 1);
});

test("scrolling a tab is clamped to its content", () => {
  let s = start(80, 24, 0);
  s = press(s, "down", "j", "down");
  assert.equal(s.scroll[0], 3);
  s = press(s, "up", "k");
  assert.equal(s.scroll[0], 1);
  s = reduce(s, "pagedown");
  assert.equal(s.scroll[0], 1 + bodyHeight(s));
  s = reduce(s, "pageup");
  assert.equal(s.scroll[0], 1);
  s = reduce(s, "end");
  const max = s.scroll[0] as number;
  assert.ok(max > 50, "the summary is longer than a screen");
  assert.equal(reduce(s, "down").scroll[0], max, "never past the end");
  assert.equal(reduce(s, "G").scroll[0], max);
  assert.equal(reduce(s, "home").scroll[0], 0);
  assert.equal(reduce(s, "g").scroll[0], 0);
  assert.equal(reduce(start(), "up").scroll[0], 0, "never above the top");
  assert.equal(reduce(start(), " ").scroll[0], bodyHeight(start()), "space pages down");
});

test("the call list: move, page, open, back, next and previous", () => {
  let s = start(80, 24, CALLS_TAB);
  s = press(s, "down", "j");
  assert.equal(s.cursor, 2);
  s = reduce(s, "up");
  assert.equal(s.cursor, 1);
  s = reduce(s, "end");
  assert.equal(s.cursor, data.model.cases.length - 1);
  assert.equal(reduce(s, "down").cursor, s.cursor, "clamped at the last call");
  s = reduce(s, "home");
  assert.equal(s.cursor, 0);
  s = reduce(s, "pagedown");
  assert.ok(s.cursor > 10);
  s = reduce(s, "pageup");
  assert.equal(s.cursor, 0);
  s = press(s, "down", "enter");
  assert.deepEqual(s.detail, { index: 1, scroll: 0 });
  s = press(s, "down", "down");
  assert.equal(s.detail?.scroll, 2, "arrows scroll the open call");
  s = reduce(s, "n");
  assert.deepEqual(s.detail, { index: 2, scroll: 0 });
  assert.equal(s.cursor, 2);
  s = reduce(s, "p");
  assert.equal(s.detail?.index, 1);
  assert.equal(reduce(s, "escape").detail, null);
  assert.equal(reduce(s, "backspace").detail, null);
  const tabbed = reduce(s, "right");
  assert.equal(tabbed.detail, null, "switching tabs closes the call");
  // The list follows the cursor.
  let long = start(80, 10, CALLS_TAB);
  long = press(long, ...Array.from({ length: 15 }, () => "down"));
  assert.ok((long.scroll[CALLS_TAB] as number) > 0);
});

test("d shows only calls that differ; / filters by text; esc clears", () => {
  let s = start(80, 24, CALLS_TAB);
  const all = visibleCalls(s).length;
  s = press(s, "end", "d");
  assert.equal(s.onlyDiffer, true);
  assert.equal(s.cursor, 0);
  const differ = visibleCalls(s);
  assert.ok(differ.length > 0 && differ.length < all);
  assert.ok(differ.every((i) => data.model.cases[i]?.differs));
  s = reduce(s, "d");
  assert.equal(visibleCalls(s).length, all);

  s = reduce(s, "/");
  assert.equal(s.typing, true);
  s = press(s, "D", "N", "S");
  assert.equal(s.filter, "DNS");
  assert.equal(reduce(s, "q").quit, false, "q types while filtering");
  const matches = visibleCalls(s);
  assert.ok(matches.length >= 1 && matches.length < all);
  s = reduce(s, "backspace");
  assert.equal(s.filter, "DN");
  s = reduce(s, "S");
  s = reduce(s, "enter");
  assert.equal(s.typing, false);
  assert.equal(s.filter, "DNS");
  s = reduce(s, "escape");
  assert.equal(s.filter, "");
  s = press(s, "/", "x", "escape");
  assert.equal(s.typing, false);
  assert.equal(s.filter, "", "esc while typing clears the filter");
  // From another tab, d and / jump to the call list.
  const jumped = reduce(start(80, 24, 0), "d");
  assert.equal(jumped.tab, CALLS_TAB);
  assert.equal(jumped.onlyDiffer, true);
  assert.equal(reduce(start(80, 24, 1), "/").typing, true);
});

test("? opens help, any key closes it; q and Ctrl-C quit", () => {
  const s = reduce(start(), "?");
  assert.equal(s.help, true);
  const closed = reduce(s, "down");
  assert.equal(closed.help, false);
  assert.equal(closed.scroll[0], 0, "the closing key does nothing else");
  assert.equal(reduce(s, "q").quit, true);
  assert.equal(reduce(start(), "q").quit, true);
  assert.equal(reduce(start(), "ctrl-c").quit, true);
  assert.equal(press(start(80, 24, CALLS_TAB), "/", "ctrl-c").quit, true, "Ctrl-C quits even while typing");
  const idle = start();
  assert.equal(reduce(idle, "z"), idle, "an unbound key changes nothing");
});

test("resize keeps positions valid", () => {
  let s = press(start(120, 40, 0), "end");
  s = resize(s, 80, 24);
  assert.equal(s.columns, 80);
  const max = reduce(s, "end").scroll[0];
  assert.ok((s.scroll[0] as number) <= (max as number));
  let list = press(start(80, 40, CALLS_TAB), "end");
  list = resize(list, 80, 8);
  const top = list.scroll[CALLS_TAB] as number;
  assert.ok(list.cursor >= top && list.cursor < top + 5, "the cursor stays on screen");
});

function frame(state: ExplorerState, columns: number, rows: number, options: { color?: boolean; ascii?: boolean } = {}) {
  const lines = screenLines(state, { columns, rows, color: Boolean(options.color), ascii: Boolean(options.ascii) });
  assert.equal(lines.length, rows, "one line per row");
  for (const line of lines) assert.equal(width(line), columns, `exactly ${columns} wide: ${line}`);
  return lines;
}

for (const [columns, rows] of [
  [80, 24],
  [120, 40],
] as const) {
  test(`every tab and the call detail render at ${columns}x${rows}`, () => {
    const expect: Record<number, RegExp> = {
      0: /metergraph replay/,
      1: /Tracked fields/,
      2: /Noise floor/,
      3: /ticket-0001/,
      4: /Content cut short/,
    };
    for (let tab = 0; tab < TABS.length; tab++) {
      const lines = frame(start(columns, rows, tab), columns, rows);
      const text = lines.join("\n");
      assert.match(lines[0] as string, /replay/);
      assert.match(text, expect[tab] as RegExp, TABS[tab]);
      assert.match(lines.at(-1) as string, /replay-out\/run\.json/, "the status bar names the run");
      assert.match(lines.at(-1) as string, new RegExp(TABS[tab] as string));
      assert.doesNotMatch(text, /\x1b/);
    }
    const differing = data.model.cases.findIndex((c) => c.differs);
    const open = press(start(columns, rows, CALLS_TAB), ...Array.from({ length: differing }, () => "down"), "enter");
    const lines = frame(open, columns, rows);
    const text = lines.join("\n");
    assert.match(text, /Input/);
    assert.match(text, /system/);
    assert.match(lines.at(-1) as string, /Calls › ticket-/);
    // Scroll to the outputs: production beside each arm, changed fields marked.
    const scrolled = frame(press(open, "pagedown"), columns, rows).join("\n") + frame(press(open, "pagedown", "pagedown"), columns, rows).join("\n");
    assert.match(scrolled, /production \(gpt-4\.1-mini\)/);
    assert.match(scrolled, /▌ /, "a changed field is marked");
    // Help over any screen.
    assert.match(frame(reduce(start(columns, rows), "?"), columns, rows).join("\n"), /Keys/);
    // Colour and ASCII keep every line the same width.
    frame(open, columns, rows, { color: true });
    const ascii = frame(open, columns, rows, { ascii: true });
    assert.match(ascii.join("\n"), /^[\x20-\x7e\n]*$/);
  });
}

test("side by side when wide, stacked when narrow", () => {
  const differing = data.model.cases.findIndex((c) => c.differs);
  const open = (columns: number) => press(start(columns, 200, CALLS_TAB), ...Array.from({ length: differing }, () => "down"), "enter");
  const sideBySide = frame(open(140), 140, 200).find((l) => l.includes("production (gpt-4.1-mini)")) as string;
  assert.match(sideBySide, /gemini-2\.5-flash-lite/, "two panes share a row at 140 columns");
  const narrow = frame(open(60), 60, 200);
  const row = narrow.find((l) => l.includes("production (gpt-4.1-mini)")) as string;
  assert.doesNotMatch(row, /gemini/, "one pane per row at 60 columns");
  assert.ok(narrow.some((l) => /gemini-2\.5-flash-lite/.test(l)));
});

test("hostile output text is neutralized in the explorer", () => {
  const run: RunFile = structuredClone(demo);
  const c = run.cases[0]!;
  c.messages = [{ role: "user", content: "hi \x1b[2J\x1b]0;owned\x07 there" }];
  const replay = run.replays.find((r) => r.caseId === c.id && r.arm === "t1")!;
  replay.output = "plain text \x1b[31mred\x1b[0m \x9b2J";
  const hostile = explorerData(run, { name: "x" });
  const s = reduce(initialState(hostile, { columns: 120, rows: 60 }, CALLS_TAB), "enter");
  for (const color of [false, true]) {
    const text = screenLines(s, { columns: 120, rows: 60, color, ascii: false }).join("\n");
    for (const e of text.match(/\x1b[\s\S]?[^m]*m?/g) ?? []) assert.match(e, /^\x1b\[[0-9;]*m$/, JSON.stringify(e));
    assert.doesNotMatch(text, /[\x07\x9b]/);
    if (!color) assert.match(text, /\^\[\[2J/);
  }
});

// ---------------------------------------------------------------------------
// The terminal owner

class FakeInput extends EventEmitter {
  isTTY = true;
  raw: boolean[] = [];
  paused = false;
  setRawMode(on: boolean) {
    this.raw.push(on);
  }
  setEncoding() {}
  resume() {
    this.paused = false;
  }
  pause() {
    this.paused = true;
  }
}

class FakeOutput extends EventEmitter {
  columns = 80;
  rows = 24;
  writes: string[] = [];
  write(text: string) {
    this.writes.push(text);
    return true;
  }
}

function harness(beforeFrame?: (s: ExplorerState) => void) {
  const stdin = new FakeInput();
  const stdout = new FakeOutput();
  const proc = new EventEmitter();
  const done = runExplorer(demo, { stdin, stdout, process: proc, color: false, ascii: false, ...(beforeFrame ? { beforeFrame } : {}) }, { name: "replay-out/run.json" });
  return { stdin, stdout, proc, done };
}

function assertRestored(h: ReturnType<typeof harness>) {
  assert.equal(h.stdout.writes[0], ENTER, "entered the alternate screen first");
  assert.equal(h.stdout.writes.at(-1), LEAVE, "left it last");
  assert.deepEqual(h.stdin.raw, [true, false], "raw mode on, then off");
  assert.equal(h.stdin.paused, true);
  assert.equal(h.stdin.listenerCount("data"), 0);
  assert.equal(h.stdout.listenerCount("resize"), 0);
  for (const event of ["SIGINT", "SIGTERM", "uncaughtException", "unhandledRejection"]) assert.equal(h.proc.listenerCount(event), 0, event);
}

test("the explorer draws each frame in one write and quits on q", async () => {
  const h = harness();
  assert.equal(h.stdout.writes.length, 2, "enter, then the first frame");
  assert.ok((h.stdout.writes[1] as string).startsWith("\x1b[H"));
  h.stdin.emit("data", "\x1b[C");
  assert.equal(h.stdout.writes.length, 3, "one write per key");
  assert.match(h.stdout.writes[2] as string, /Tracked fields/);
  h.stdout.columns = 120;
  h.stdout.rows = 40;
  h.stdout.emit("resize");
  assert.equal(h.stdout.writes.length, 4, "a resize redraws");
  assert.equal((h.stdout.writes[3] as string).split("\x1b[").filter((s) => /^\d+;1H/.test(s)).length, 40);
  h.stdin.emit("data", "q");
  const result = await h.done;
  assert.equal(result.reason, "quit");
  assertRestored(h);
});

test("the terminal is restored when drawing throws", async () => {
  let frames = 0;
  const h = harness(() => {
    if (++frames === 2) throw new Error("boom");
  });
  h.stdin.emit("data", "j");
  await assert.rejects(h.done, /boom/);
  assertRestored(h);
});

test("the terminal is restored on SIGTERM, SIGINT and an uncaught exception", async () => {
  const term = harness();
  term.proc.emit("SIGTERM");
  assert.equal((await term.done).reason, "SIGTERM");
  assertRestored(term);

  const int = harness();
  int.proc.emit("SIGINT");
  assert.equal((await int.done).reason, "SIGINT");
  assertRestored(int);

  const fatal = harness();
  fatal.proc.emit("uncaughtException", new Error("elsewhere"));
  await assert.rejects(fatal.done, /elsewhere/);
  assertRestored(fatal);
});

test("frameOutput places every row explicitly", () => {
  const out = frameOutput("ab\ncd");
  assert.equal(out, "\x1b[H\x1b[1;1Hab\x1b[0m\x1b[2;1Hcd\x1b[0m");
});
