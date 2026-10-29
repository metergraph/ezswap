import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { buildView, equivalence, noiseLabel, noiseLabels, summarize } from "../src/core/index.ts";
import type { RunFile } from "../src/types.ts";

// The synthetic run the page fixture was generated from: 16 cases (one not
// replayable), treatments "Mini" (t1) and "Flash" (t2, two failures and one
// skipped), and a noise floor.
const run: RunFile = JSON.parse(readFileSync(new URL("../conformance/page.json", import.meta.url), "utf8")).run;

test("only replayable cases count, and every sampled case is listed", () => {
  const view = buildView(run);
  assert.deepEqual(view.sample, { size: 16, cases: 16, replayed: 15, not_replayable: 1 });
  assert.equal(view.control.summary.cases, 15);
  assert.equal(view.cases?.length, 16);
  assert.equal(view.cases?.find((c) => c.status === "not_replayable")?.reason, "tool_history");
  assert.equal(view.fields?.["$.priority"]?.control?.length, 15);
});

test("a failed replay is an error; a skipped one is no result at all", () => {
  const view = buildView(run);
  const flash = view.arms.find((a) => a.label === "Flash");
  assert.ok(flash);
  assert.equal(flash.summary.cases, 14);
  assert.equal(flash.summary.errors, 2);
  // The control over the same cases the arm returned on.
  assert.equal(flash.control_summary.cases, 12);
  assert.equal(flash.summary.temperature_dropped, 1);
  assert.deepEqual(flash.settings, { temperature: 0, max_tokens: 1024, reasoning_effort: "low" });
});

test("the control's refusal flag counts as empty or refused", () => {
  const view = buildView(run);
  assert.equal(view.control.summary.empty_or_refused_rate, Math.round((1 / 15) * 1e4) / 1e4);
  assert.equal(view.control.summary.cut_off_rate, Math.round((1 / 15) * 1e4) / 1e4);
  assert.equal(view.control.model, "gpt-5.4");
});

test("the noise floor pairs the cases where both replays returned", () => {
  const view = buildView(run);
  assert.ok(view.noise_floor);
  assert.equal(view.noise_floor.cases, 15);
  assert.deepEqual(view.noise_floor.gateway_models, ["gpt-5.4"]);
  assert.equal(view.noise_floor.fields["$.category"]?.kind, "label");
  assert.ok(view.cases?.[0]?.noise_floor?.a1 && view.cases[0].noise_floor.a2);
});

test("a run without a floor has a null noise floor", () => {
  const plain: RunFile = {
    ...run,
    settings: { ...run.settings, noiseFloor: false },
    replays: run.replays.filter((r) => !r.arm.startsWith("floor_")),
  };
  const view = buildView(plain);
  assert.equal(view.noise_floor, null);
  assert.equal(view.cases?.[0]?.noise_floor, undefined);
  const [mini] = equivalence(view).treatments;
  assert.equal(mini?.latency.control, null);
  assert.match(mini?.cost.sentence ?? "", /for control over the same 15 cases/);
  assert.equal(noiseLabels(view, "Mini"), null);
});

test("without cases, the view keeps its statistics and drops per-case detail", () => {
  const view = buildView(run, { includeCases: false });
  assert.equal(view.cases, null);
  assert.equal(view.fields, null);
  assert.equal(view.noise_floor?.stats.latency_ms.differences, undefined);
  assert.equal(view.arms[0]?.summary.cases, 15);
});

test("equivalence reads cost and latency against the floor's first replay", () => {
  const view = buildView(run);
  const [mini] = equivalence(view).treatments;
  assert.ok(mini);
  assert.equal(mini.latency.control, view.noise_floor?.a1.latency_ms_p50);
  assert.match(mini.latency.sentence, /for the control replayed through the same gateway/);
  assert.equal(mini.fields.length, 4);
});

test("a tracked path no statistic can read is refused", () => {
  assert.throws(() => buildView({ ...run, settings: { ...run.settings, trackedFields: ["$..deep"] } }), /not a supported JSON path/);
});

test("summarize refuses outcomes and controls that are not aligned", () => {
  assert.throws(() => summarize([{ output: "x" }], []), /aligned/);
});

test("within-noise labels: beyond, within, and their direction", () => {
  const view = buildView(run);
  const labels = noiseLabels(view, "Mini");
  assert.ok(labels);
  const byKey = Object.fromEntries([...labels.stats, ...labels.fields].map((l) => [l.key, l]));
  assert.equal(byKey.latency_ms_p50?.label, "beyond_noise");
  assert.equal(byKey.latency_ms_p50?.direction, "lower");
  assert.equal(byKey.latency_ms_p50?.against, "replayed_control");
  assert.equal(byKey.cost_per_call_usd?.direction, "lower");
  // Every output keeps production's keys (production's one non-JSON reply is not compared).
  assert.equal(byKey.same_keys_rate?.label, "within_noise");
  assert.equal(byKey.same_keys_rate?.gap, 0);
  assert.equal(byKey.same_keys_rate?.cases, 14);
  // Production's one refusal did not parse; Mini's reply there does.
  assert.equal(byKey.json_parse_rate?.label, "beyond_noise");
  assert.equal(byKey.json_parse_rate?.direction, "higher");
  assert.equal(byKey["field:$.category"]?.label, "beyond_noise");
  assert.equal(byKey["field:$.category"]?.direction, "different");
  assert.equal(byKey["field:$.category"]?.cases, 14);
  assert.ok((byKey["field:$.category"]?.gap ?? 0) > (byKey["field:$.category"]?.floor_gap ?? 1));
  // Mini moves priority on a third of the cases by 1; the floor moves it on two cases by 1.
  assert.equal(byKey["field:$.priority"]?.measure, "median_abs_difference");
  assert.equal(noiseLabel(view, "Mini", "field:$.summary")?.label, "beyond_noise");
});

test("within-noise labels: a treatment no further from the control than the floor is within noise", () => {
  // A treatment that returns exactly the floor's second replay differs from the first by the floor's own gap.
  const twin: RunFile = {
    ...run,
    treatments: [...run.treatments, { ...run.treatments[0]!, id: "t3", label: "Twin" }],
    replays: [...run.replays, ...run.replays.filter((r) => r.arm === "floor_a2").map((r) => ({ ...r, arm: "t3" }))],
  };
  const view = buildView(twin);
  assert.equal(noiseLabel(view, "Twin", "latency_ms_p50")?.label, "within_noise");
  assert.equal(noiseLabel(view, "Twin", "cost_per_call_usd")?.label, "within_noise");
});

test("within-noise labels: too few paired cases", () => {
  const ids = new Set(run.cases.slice(0, 6).map((c) => c.id));
  const small: RunFile = { ...run, cases: run.cases.filter((c) => ids.has(c.id)), replays: run.replays.filter((r) => ids.has(r.caseId)) };
  const label = noiseLabel(buildView(small), "Mini", "latency_ms_p50");
  assert.equal(label?.label, "too_few_calls");
  assert.equal(label?.direction, null);
  assert.equal(label?.cases, 6);
});
