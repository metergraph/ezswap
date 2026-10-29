import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { decodeSetup, hostedLink, renderHtml, renderTable, reportModel, scriptJson } from "../src/report/index.ts";
import type { RunFile } from "../src/types.ts";

// A synthetic run: 24 support-ticket calls at example.com, two treatments,
// a noise floor, hostile strings in a prompt and in outputs.
const fixture: RunFile = JSON.parse(readFileSync(new URL("./fixtures/report-run.json", import.meta.url), "utf8"));
const clone = (): RunFile => structuredClone(fixture);

const HOSTILE = `</script><script>alert("x")</script><img src=x onerror="alert('y')"> &amp; ' " <!-- \u2028`;

function hostileRun(): RunFile {
  const run = clone();
  run.input.file = `traces</title><script>alert(1)</script>.jsonl`;
  run.catalog.version = HOSTILE;
  run.treatments[0]!.label = `Mini"><script>alert(2)</script>`;
  run.treatments[0]!.model = `openai/<b>mini</b>`;
  run.cases[0]!.inputPreview = HOSTILE;
  run.cases[0]!.production.output = HOSTILE;
  const replay = run.replays.find((r) => r.caseId === run.cases[0]!.id && r.arm === "t1")!;
  replay.output = HOSTILE;
  const failed = run.replays.find((r) => r.status === "failed")!;
  failed.error = { code: "timeout", message: HOSTILE };
  return run;
}

function dataBlock(html: string): unknown {
  const m = html.match(/<script type="application\/json" id="report-data">([\s\S]*?)<\/script>/);
  assert.ok(m, "the data block is present");
  return JSON.parse(m[1] as string);
}

test("hostile captured strings never reach the markup", () => {
  const run = hostileRun();
  const html = renderHtml(run, { labels: true });
  // Exactly the two script elements the report writes, and no element a capture could have opened.
  assert.equal(html.match(/<script/gi)?.length, 2);
  assert.equal(html.match(/<\/script/gi)?.length, 2);
  assert.doesNotMatch(html, /<img/i);
  assert.doesNotMatch(html, /<b>/i);
  assert.doesNotMatch(html, /<script>alert/);
  assert.doesNotMatch(html, /<!--/);
  assert.ok(!html.includes("\u2028"), "no raw line separator inside the script");
  // The title is the one captured string outside the data block, and it is escaped.
  assert.match(html, /<title>Replay report: gpt-5\.4<\/title>/);
  // The strings survive intact as data.
  const data = dataBlock(html) as { cases: { input: string; production: { output: string }; arms: { output: string; error: string | null }[] }[]; columns: { label: string }[]; meta: { value: string }[] };
  assert.equal(data.cases[0]?.input, HOSTILE);
  assert.equal(data.cases[0]?.production.output, HOSTILE);
  assert.equal(data.cases[0]?.arms[0]?.output, HOSTILE);
  assert.equal(data.columns[0]?.label, run.treatments[0]!.label);
  assert.ok(data.meta.some((m) => m.value === HOSTILE));
  assert.ok(data.cases.some((c) => c.arms.some((a) => a.error?.includes(HOSTILE))));
});

test("the page script renders captured text with textContent only", () => {
  const html = renderHtml(fixture);
  const script = html.slice(html.lastIndexOf("<script>"));
  assert.doesNotMatch(script, /innerHTML|outerHTML|insertAdjacentHTML|document\.write|eval\(|new Function/);
});

test("scriptJson leaves nothing that can close or comment a script", () => {
  const out = scriptJson({ a: "</script><!-- & > \u2028 \u2029" });
  assert.doesNotMatch(out, /[<>&\u2028\u2029]/);
  assert.deepEqual(JSON.parse(out), { a: "</script><!-- & > \u2028 \u2029" });
});

test("the report makes no network requests and links only where it should", () => {
  const html = renderHtml(fixture, { labels: true });
  assert.match(html, /Content-Security-Policy" content="default-src 'none'/);
  const markup = html.replace(/<script type="application\/json" id="report-data">[\s\S]*?<\/script>/, "");
  assert.doesNotMatch(markup, /<link\b|@import|url\(|\bsrc=|\bfetch\(|XMLHttpRequest|WebSocket|sendBeacon/i);
  const urls = html.match(/https?:\/\/[^\s"'<>\\)]+/g) ?? [];
  assert.ok(urls.length > 0);
  const allowed = [
    "https://github.com/metergraph/replay/",
    "https://www.metergraph.dev/",
    // The SVG namespace name: an identifier, never fetched.
    "http://www.w3.org/2000/svg",
  ];
  for (const url of urls) assert.ok(allowed.some((prefix) => url.startsWith(prefix)), `unexpected URL ${url}`);
  assert.ok(html.includes("https://github.com/metergraph/replay/blob/main/docs/noise-floor.md"));
  assert.ok(html.includes("npx --yes metergraph-cli@latest setup"));
  assert.equal((dataBlock(html) as { links: { hosted: string } }).links.hosted, hostedLink(fixture, "report"));
});

test("light and dark tokens are both defined", () => {
  const html = renderHtml(fixture);
  assert.match(html, /@media \(prefers-color-scheme: dark\)/);
  assert.match(html, /<meta name="color-scheme" content="light dark">/);
  for (const token of ["--bg", "--surface", "--text", "--muted", "--line", "--accent", "--floor"]) {
    assert.equal(html.match(new RegExp(`${token}:`, "g"))?.length, 2, token);
  }
  assert.match(html, /<meta name="viewport" content="width=device-width, initial-scale=1">/);
});

test("the report reads latency against the floor's first replay, and states no verdict", () => {
  const model = reportModel(fixture, { labels: true });
  const latency = model.rows.find((r) => r.key === "latency_ms_p50");
  assert.ok(latency?.cells.every((c) => c.delta && c.floor));
  assert.match(model.headline, /\?$/);
  const html = renderHtml(fixture, { labels: true });
  const copy = JSON.stringify(dataBlock(html)) + html;
  assert.doesNotMatch(copy, /\b(you should switch|we recommend|recommended model|switch now)\b/i);
  // "Safe" and "good enough" appear only to say that nothing here says so.
  for (const sentence of copy.split(/(?<=[.!?])\s+/)) {
    if (/\b(safe|good enough)\b/i.test(sentence)) assert.match(sentence, /\b(neither|nothing|not|never)\b/i, sentence);
  }
  // No em dash in copy; a bare one marks a missing value in a cell.
  const strings: string[] = [];
  const walk = (v: unknown) => {
    if (typeof v === "string") strings.push(v);
    else if (v && typeof v === "object") Object.values(v).forEach(walk);
  };
  walk(dataBlock(html));
  for (const s of strings) if (s.includes("—")) assert.equal(s.trim(), "—", s);
});

test("labels are opt-in", () => {
  const off = reportModel(fixture);
  assert.ok(off.rows.every((r) => r.cells.every((c) => c.label === null)));
  const on = reportModel(fixture, { labels: true });
  assert.ok(on.rows.some((r) => r.cells.some((c) => c.label?.tone === "within" || c.label?.tone === "beyond")));
});

test("a run without a noise floor or traffic still renders", () => {
  const run = clone();
  run.settings.noiseFloor = false;
  run.replays = run.replays.filter((r) => !r.arm.startsWith("floor_"));
  delete run.input.traffic;
  const model = reportModel(run, { labels: true });
  assert.equal(model.floor, null);
  assert.equal(model.labels, false);
  assert.equal(model.rows[0]?.key, "monthly_cost_usd");
  assert.equal(model.rows[0]?.production, "—");
  const html = renderHtml(run);
  assert.ok(dataBlock(html));
  assert.match(renderTable(run, { width: 80, ascii: true }), /production/);
});

test("the monthly projection uses the traffic rate, or an explicit volume", () => {
  const model = reportModel(fixture);
  assert.match(model.rows[0]?.sublabel ?? "", /18,400 calls in 30 days/);
  const explicit = reportModel(fixture, { monthlyCalls: 1000 });
  assert.match(explicit.rows[0]?.sublabel ?? "", /^1,000 calls in 30 days/);
});

test("the not-replayable summary counts reasons and says how to fix each", () => {
  const model = reportModel(fixture);
  assert.deepEqual(
    model.notReplayable.map((r) => [r.reason, r.count]),
    [
      ["tool_history", 2],
      ["content_truncated", 1],
    ],
  );
  assert.ok(model.notReplayable.every((r) => r.fix.length > 20 && !r.fix.includes("—")));
});

test("the case filter marks treatments that differ from production", () => {
  const model = reportModel(fixture);
  const failed = model.cases.find((c) => c.arms.some((a) => a.status === "failed"));
  assert.ok(failed?.differs, "a failure counts as differing");
  assert.ok(model.cases.some((c) => c.status === "replayed" && !c.differs));
  assert.ok(model.cases.filter((c) => c.status === "not_replayable").every((c) => !c.differs && c.arms.length === 0));
});

const ANSI = /\x1b\[[0-9;]*m/g;

test("the table never exceeds the width", () => {
  for (let width = 40; width <= 160; width += 3) {
    for (const ascii of [true, false]) {
      const out = renderTable(fixture, { width, ascii, labels: true, color: true });
      for (const line of out.split("\n")) assert.ok([...line.replace(ANSI, "")].length <= width, `${width}: ${line}`);
    }
  }
});

test("the table has production, each treatment and the floor last", () => {
  const out = renderTable(fixture, { width: 140, ascii: true });
  const rows = out.split("\n").map((l) => l.split(/\s{2,}/)[0]);
  const names = rows.filter((n) => ["production", "Mini", "Flash", "production vs itself"].includes(n as string));
  assert.deepEqual(names, ["production", "Mini", "Flash", "production vs itself"]);
  assert.match(out, /category/);
  assert.match(out, /cost\/call/);
});

test("ASCII mode prints only ASCII, and colour only when asked", () => {
  const ascii = renderTable(fixture, { width: 120, ascii: true, labels: true });
  assert.match(ascii, /^[\x20-\x7e\n]*$/);
  assert.doesNotMatch(ascii, ANSI);
  assert.match(renderTable(fixture, { width: 120, ascii: true, color: true }), ANSI);
  assert.match(renderTable(fixture, { width: 120, ascii: false }), /Δ cost/);
});

test("the hosted link carries UTM parameters and only the setup, in the fragment", () => {
  const run = hostileRun();
  (run.treatments[1] as unknown as Record<string, unknown>).apiKey = "sk-test-not-a-key";
  run.treatments[1]!.endpoint = "https://gateway.example.com/v1";
  const link = hostedLink(run, "cli");
  const url = new URL(link);
  assert.equal(url.origin + url.pathname, "https://www.metergraph.dev/replay");
  assert.deepEqual([...url.searchParams.keys()], ["utm_source", "utm_medium", "utm_campaign"]);
  assert.equal(url.searchParams.get("utm_source"), "replay");
  assert.equal(url.searchParams.get("utm_medium"), "cli");
  assert.equal(url.searchParams.get("utm_campaign"), run.tool.version);
  assert.match(url.hash, /^#setup=[A-Za-z0-9_-]+$/);
  const setup = decodeSetup(url.hash);
  assert.deepEqual(Object.keys(setup).sort(), ["sample", "trackedFields", "treatments", "v"]);
  assert.equal(setup.v, 1);
  assert.equal(setup.sample, run.settings.sample);
  assert.deepEqual(setup.trackedFields, run.settings.trackedFields);
  assert.deepEqual(
    setup.treatments,
    run.treatments.map((t) => ({ model: t.model, settings: t.settings })),
  );
  for (const t of setup.treatments) assert.deepEqual(Object.keys(t).sort(), ["model", "settings"]);
  const decoded = JSON.stringify(setup);
  for (const leak of ["sk-test", "gateway.example.com", "example.com", "alert", "Ticket", run.treatments[0]!.label, run.input.file]) {
    assert.ok(!decoded.includes(leak) && !link.includes(leak), `link carries ${leak}`);
  }
  for (const c of run.cases) {
    if (c.production.output) assert.ok(!decoded.includes(c.production.output));
  }
});
