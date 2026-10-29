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

// The one hyperlink the report may emit: the hosted link (OSC 8 open, then close).
const OWN_LINK = /\x1b\]8;;(?:https:\/\/www\.metergraph\.dev\/replay\?[A-Za-z0-9_=&.%-]*#setup=[A-Za-z0-9_-]*)?\x1b\\/g;

/** Remove the report's own hyperlink, checking it opens and closes exactly once. */
function withoutOwnLink(out: string, expected: boolean): string {
  const found = out.match(OWN_LINK) ?? [];
  assert.equal(found.length, expected ? 2 : 0, "the hosted link is the only hyperlink");
  return out.replace(OWN_LINK, "");
}

test("ASCII mode prints only printable ASCII; colour adds only SGR sequences and the hosted link", () => {
  const ascii = renderReport(fixture, { width: 100, ascii: true, labels: true });
  assert.match(ascii, /^[\x20-\x7e\n]*$/);
  const colored = withoutOwnLink(renderReport(fixture, { width: 100, ascii: false, labels: true, color: true }), true);
  const escapes = colored.match(/\x1b[^m]*m?/g) ?? [];
  assert.ok(escapes.length > 0);
  for (const e of escapes) assert.match(e, /^\x1b\[[0-9;]*m$/, JSON.stringify(e));
  assert.doesNotMatch(renderReport(fixture, { width: 100, color: false }), /\x1b/);
});

test("the cost note states production's recorded cost only when there is one", () => {
  const flat = (run: RunFile) => renderReport(run, { width: 120, color: false }).replace(/\s+/g, " ");
  assert.match(flat(fixture), /Its recorded calls cost \$\S+ each\./);
  const unpriced: RunFile = structuredClone(fixture);
  for (const c of unpriced.cases) c.production.costUsd = null;
  const text = flat(unpriced);
  assert.match(text, /replayed the same way as the treatments\./);
  assert.doesNotMatch(text, /Its recorded calls cost/);
  assert.doesNotMatch(text, /cost — each/);
});

test("production's recorded latency is stated only when the source timed its calls", () => {
  const flat = (run: RunFile) => renderReport(run, { width: 120, color: false }).replace(/\s+/g, " ");
  assert.match(flat(demo), /Its recorded median \(\d+ ms\) is not compared/);
  const untimed: RunFile = structuredClone(demo);
  for (const c of untimed.cases) c.production.latencyMs = 0;
  const text = flat(untimed);
  assert.match(text, /production here is its model replayed from this machine\./);
  assert.doesNotMatch(text, /Its recorded median/);
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

test("colour marks arms, not verdicts: design system colours, never its alert colours", async () => {
  const { ALERT_COLORS, DESIGN, nearest256 } = await import("../src/report/term/ansi.ts");
  const before = process.env["COLORTERM"];
  try {
    for (const truecolor of [false, true]) {
      if (truecolor) process.env["COLORTERM"] = "truecolor";
      else delete process.env["COLORTERM"];
      const colored = renderReport(demo, { width: 120, color: true, labels: true });
      // 16-colour green and red, and the 256-colour greens and reds.
      assert.doesNotMatch(colored, /\x1b\[(?:[0-9;]*;)?(?:31|32|91|92)m/);
      assert.doesNotMatch(colored, /38;5;(?:2|9|10|28|34|40|46|82|118|160|196|124)m/);
      for (const hex of Object.values(ALERT_COLORS)) {
        const n = Number.parseInt(hex.slice(1), 16);
        const code = truecolor ? `2;${(n >> 16) & 255};${(n >> 8) & 255};${n & 255}m` : `5;${nearest256(hex)}m`;
        assert.ok(!colored.includes(`38;${code}`) && !colored.includes(`48;${code}`), `alert colour ${hex} is never used`);
      }
      // Treatments carry the brand green.
      const green = Number.parseInt(DESIGN.green300.slice(1), 16);
      const brand = truecolor ? `38;2;${(green >> 16) & 255};${(green >> 8) & 255};${green & 255}m` : `38;5;${nearest256(DESIGN.green300)}m`;
      assert.ok(colored.includes(brand), "treatments are drawn in the brand green");
    }
  } finally {
    if (before === undefined) delete process.env["COLORTERM"];
    else process.env["COLORTERM"] = before;
  }
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
      const rendered = renderReport(run, { width: 100, color, ascii, labels: true, explore: `out${evil}/run.json` });
      // Only the report's own SGR styles and hosted link may appear, and none without colour.
      const out = withoutOwnLink(rendered, color && !ascii);
      const escapes = out.match(/\x1b[\s\S]?[^m]*m?/g) ?? [];
      for (const e of escapes) assert.match(e, /^\x1b\[[0-9;]*m$/, JSON.stringify(e));
      if (!color) assert.doesNotMatch(out, /\x1b/);
      assert.doesNotMatch(out, /[\x00-\x09\x0b-\x1a\x1c-\x1f\x7f-\x9f‮]/);
      assert.match(out, /\^\[\[2J/, "shown as caret notation");
      for (const line of out.split("\n")) if (!LINK.test(stripAnsi(line))) assert.ok(width(line) <= 100);
    }
  }
});
