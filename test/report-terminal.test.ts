import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { renderReport } from "../src/report/index.ts";
import { stripAnsi, width } from "../src/report/term/width.ts";
import type { RunFile } from "../src/types.ts";

// The demo's recorded run (synthetic support tickets) and a second synthetic
// fixture with a failure, three tracked fields and two short labels.
const demo: RunFile = JSON.parse(readFileSync(new URL("../demo/run.json", import.meta.url), "utf8"));
const fixture: RunFile = JSON.parse(readFileSync(new URL("./fixtures/report-run.json", import.meta.url), "utf8"));

const GOLDEN = new URL("./golden/", import.meta.url);
const UPDATE = process.env["UPDATE_GOLDEN"] === "1";

function golden(name: string, actual: string): void {
  const file = new URL(name, GOLDEN);
  if (UPDATE || !existsSync(file)) {
    mkdirSync(GOLDEN, { recursive: true });
    writeFileSync(file, actual, "utf8");
    if (!UPDATE) assert.fail(`wrote missing golden ${name}; run again to compare`);
    return;
  }
  const expected = readFileSync(file, "utf8");
  if (actual !== expected) {
    const a = actual.split("\n");
    const e = expected.split("\n");
    const at = a.findIndex((line, i) => line !== e[i]);
    assert.fail(`${name} differs at line ${at + 1}:\n  expected: ${JSON.stringify(e[at])}\n  actual:   ${JSON.stringify(a[at])}\nRun UPDATE_GOLDEN=1 npm test to accept the new output.`);
  }
}

for (const w of [80, 120]) {
  for (const ascii of [true, false]) {
    const name = `demo-${w}-${ascii ? "ascii" : "unicode"}.txt`;
    test(`golden: the demo report at ${w} columns, ${ascii ? "ASCII" : "Unicode"}, no colour`, () => {
      golden(name, renderReport(demo, { width: w, ascii, color: false, explore: "replay-out/run.json" }));
    });
  }
}

test("golden: the demo report with within-noise labels at 120 columns", () => {
  golden("demo-120-unicode-labels.txt", renderReport(demo, { width: 120, ascii: false, color: false, labels: true, explore: "replay-out/run.json" }));
});

// A URL or a command alone on its line stays whole so it can be copied; the terminal may wrap it.
const LINK = /^ +(?:https:\/\/\S+|metergraph-replay report \S+)$/;

test("no line is wider than the terminal, at any width, in either character set", () => {
  for (const run of [demo, fixture]) {
    for (let w = 40; w <= 140; w += 7) {
      for (const ascii of [true, false]) {
        for (const labels of [true, false]) {
          const out = renderReport(run, { width: w, ascii, labels, color: true, explore: "replay-out/run.json" });
          for (const line of out.split("\n")) {
            if (LINK.test(stripAnsi(line))) continue;
            assert.ok(width(line) <= w, `${w} ${ascii ? "ascii" : "unicode"}: ${stripAnsi(line)}`);
          }
        }
      }
    }
  }
  // Wider terminals get the 140-column layout, never a box wider than that.
  const wide = renderReport(demo, { width: 300, ascii: false });
  assert.equal(Math.max(...wide.split("\n").filter((l) => !LINK.test(l)).map((l) => width(l))), 140);
});

test("ASCII mode prints only printable ASCII; colour adds only SGR sequences", () => {
  const ascii = renderReport(fixture, { width: 100, ascii: true, labels: true });
  assert.match(ascii, /^[\x20-\x7e\n]*$/);
  const colored = renderReport(fixture, { width: 100, ascii: false, labels: true, color: true });
  const escapes = colored.match(/\x1b[^m]*m?/g) ?? [];
  assert.ok(escapes.length > 0);
  for (const e of escapes) assert.match(e, /^\x1b\[[0-9;]*m$/, JSON.stringify(e));
  assert.doesNotMatch(renderReport(fixture, { width: 100, color: false }), /\x1b/);
});

test("the report has every panel, top to bottom", () => {
  const out = renderReport(demo, { width: 120, ascii: false, explore: "replay-out/run.json" });
  const order = [
    "metergraph replay",
    "Would a cheaper model",
    "╭─ Cost",
    "╭─ Output signals",
    "╭─ Tracked fields",
    "╭─ Latency",
    "╭─ Calls that differ",
    "╭─ Not replayable",
    "Production vs itself:",
    "Keep this comparison running on live traffic",
    "npx --yes metergraph-cli@latest setup",
    "utm_medium=cli",
    "metergraph-replay report replay-out/run.json",
  ];
  let at = -1;
  for (const needle of order) {
    const next = out.indexOf(needle, at + 1);
    assert.ok(next > at, `${needle} after the previous panel`);
    at = next;
  }
  assert.match(out, /\$8\.54\/mo/);
  assert.match(out, /76% lower/);
  assert.match(out, /category: \S+ → \S+/);
  assert.doesNotMatch(out, /\S—|—\S/, "no em dash in copy; a bare one marks a missing value");
});

test("colour marks arms, not verdicts: no green or red", () => {
  const colored = renderReport(demo, { width: 120, color: true, labels: true });
  // 16-colour green and red, and the 256-colour greens and reds.
  assert.doesNotMatch(colored, /\x1b\[(?:[0-9;]*;)?(?:31|32|91|92)m/);
  assert.doesNotMatch(colored, /38;5;(?:2|9|10|28|34|40|46|82|118|160|196|124)m/);
});

test("hostile captured text is neutralized before it reaches the terminal", () => {
  const run: RunFile = structuredClone(fixture);
  const evil = "\x1b[2J\x1b[H\x1b]0;owned\x07\x9b31m\r‮";
  run.input.file = `traces${evil}.jsonl`;
  run.catalog.version = `v${evil}`;
  run.treatments[0]!.label = `Mini${evil}`;
  const first = run.cases.find((c) => !c.notReplayable)!;
  first.inputPreview = `ticket ${evil} ${"long line ".repeat(400)}`;
  const replay = run.replays.find((r) => r.caseId === first.id && r.arm === "t1")!;
  replay.output = JSON.stringify({ category: `bug${evil}`, priority: "low", tags: [] });
  const failed = run.replays.find((r) => r.status === "failed")!;
  failed.error = { code: "timeout", message: evil };
  for (const color of [false, true]) {
    for (const ascii of [false, true]) {
      const out = renderReport(run, { width: 100, color, ascii, labels: true, explore: `out${evil}/run.json` });
      // Only the report's own SGR styles may appear, and none without colour.
      const escapes = out.match(/\x1b[\s\S]?[^m]*m?/g) ?? [];
      for (const e of escapes) assert.match(e, /^\x1b\[[0-9;]*m$/, JSON.stringify(e));
      if (!color) assert.doesNotMatch(out, /\x1b/);
      assert.doesNotMatch(out, /[\x00-\x09\x0b-\x1a\x1c-\x1f\x7f-\x9f‮]/);
      assert.match(out, /\^\[\[2J/, "shown as caret notation");
      for (const line of out.split("\n")) if (!LINK.test(stripAnsi(line))) assert.ok(width(line) <= 100);
    }
  }
});
