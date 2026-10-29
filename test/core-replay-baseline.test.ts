// A replay run has no paid "Control, replayed" column; its noise floor's
// first replay (A1) is the control replayed the same way. With
// `floorAsReplay`, the stat rows and the grid read latency (and cost) against
// A1, as equivalence does. The default stays the hosted page's behaviour,
// which conformance/page.json pins.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { buildView, equivalence, gridMetrics, gridReadings, replayBaseline, statDelta, statRows } from "../src/core/index.ts";
import type { RunFile } from "../src/types.ts";

const run: RunFile = JSON.parse(readFileSync(new URL("../conformance/page.json", import.meta.url), "utf8")).run;

test("by default latency is not compared on a replay run", () => {
  const view = buildView(run);
  const latency = statRows(view).find((row) => row.key === "latency_ms_p50");
  assert.ok(latency);
  assert.ok(latency.arms.every((cell) => cell.delta === null));
  assert.equal(replayBaseline(view), null);
});

test("floorAsReplay reads latency and cost against the floor's first replay", () => {
  const view = buildView(run);
  const a1 = view.noise_floor?.a1;
  assert.ok(a1);
  const rows = statRows(view, { floorAsReplay: true });
  const latency = rows.find((row) => row.key === "latency_ms_p50");
  const cost = rows.find((row) => row.key === "cost_per_call_usd");
  assert.ok(latency && cost);
  view.arms.forEach((arm, i) => {
    assert.equal(latency.arms[i]?.delta, statDelta("latency_ms_p50", arm.summary.latency_ms_p50, a1.latency_ms_p50, "replay"));
    assert.equal(cost.arms[i]?.delta, statDelta("cost_per_call_usd", arm.summary.cost_per_call_usd, a1.cost_per_call_usd, "replay"));
    assert.notEqual(latency.arms[i]?.delta, null);
  });
  // The same baseline equivalence uses.
  const [mini] = equivalence(view).treatments;
  assert.equal(mini?.latency.delta, latency.arms[0]?.delta);
});

test("floorAsReplay gives the grid a replayed control's latency from A1", () => {
  const view = buildView(run);
  const plain = gridMetrics(view).find((m) => m.key === "latency_ms_p50");
  assert.equal(plain?.controlSource, null);
  assert.deepEqual(plain?.control(), []);
  const metric = gridMetrics(view, { floorAsReplay: true }).find((m) => m.key === "latency_ms_p50");
  assert.ok(metric);
  assert.equal(metric.controlSource, "replay");
  const { control } = gridReadings(view, metric);
  assert.ok(control);
  assert.equal(control.value, view.noise_floor?.a1.latency_ms_p50);
});

test("without a floor, floorAsReplay changes nothing", () => {
  const plain: RunFile = { ...run, settings: { ...run.settings, noiseFloor: false }, replays: run.replays.filter((r) => !r.arm.startsWith("floor_")) };
  const view = buildView(plain);
  assert.deepEqual(statRows(view, { floorAsReplay: true }), statRows(view));
  assert.equal(gridMetrics(view, { floorAsReplay: true })[1]?.controlSource, null);
});
