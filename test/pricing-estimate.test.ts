import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import type { Case, Pricing, ReplayCall, Treatment } from "../src/types.ts";
import { loadPricing } from "../src/pricing/catalog.ts";
import {
  assumedOutputTokens,
  capSent,
  estimateRun,
  floorCall,
  floorModel,
  pythonJson,
  reservationInputTokens,
  treatmentCall,
  upperBoundUsd,
} from "../src/pricing/estimate.ts";

const fixture = JSON.parse(readFileSync(new URL("../conformance/estimate.json", import.meta.url), "utf8")) as {
  at: string;
  cases: Case[];
  treatments: Treatment[];
  expected: {
    expectedUsd: string;
    upperBoundUsd: string;
    perArm: Record<string, { expectedUsd: number; upperBoundUsd: string }>;
    floor: { models: string[]; expectedUsd: number; upperBoundUsdPerReplay: string };
    calls: Array<{ arm: string; case: string; model: string; maxTokens: number; inputBound?: number; upperBoundUsd: string }>;
  };
};

const pricing = loadPricing();
const at = fixture.at;

test("estimate conformance: totals and per-arm shares match the hosted estimate", () => {
  const estimate = estimateRun(fixture.cases, fixture.treatments, { noiseFloor: true, at }, pricing);
  assert.equal(estimate.replayable, 4);
  assert.deepEqual(estimate.notReplayable, { tool_history: 1 });
  assert.equal(estimate.expectedUsd, Number(fixture.expected.expectedUsd));
  assert.equal(estimate.upperBoundUsd, Number(fixture.expected.upperBoundUsd));
  for (const [arm, expected] of Object.entries(fixture.expected.perArm)) {
    assert.equal(estimate.perArm[arm]?.expectedUsd, expected.expectedUsd, arm);
    assert.equal(estimate.perArm[arm]?.upperBoundUsd, Number(expected.upperBoundUsd), arm);
  }
  for (const arm of ["floor_a1", "floor_a2"]) {
    assert.equal(estimate.perArm[arm]?.expectedUsd, fixture.expected.floor.expectedUsd / 2);
    assert.equal(estimate.perArm[arm]?.upperBoundUsd, Number(fixture.expected.floor.upperBoundUsdPerReplay));
  }
  assert.deepEqual(estimate.unpriced, []);
});

test("estimate conformance: the noise floor replays production's resolved model", () => {
  const replayable = fixture.cases.filter((item) => item.notReplayable === null);
  assert.deepEqual(
    replayable.map((item) => floorModel(item, pricing, at)),
    fixture.expected.floor.models,
  );
});

test("estimate conformance: every call's upper bound matches the hosted figures", () => {
  const byId = new Map(fixture.cases.map((item) => [item.id, item]));
  const floorModels = new Map(
    fixture.cases.filter((item) => item.notReplayable === null).map((item, index) => [item.id, fixture.expected.floor.models[index]!]),
  );
  for (const expected of fixture.expected.calls) {
    const item = byId.get(expected.case)!;
    const call =
      expected.arm === "floor"
        ? floorCall(item, floorModels.get(item.id)!)
        : treatmentCall(item, fixture.treatments.find((t) => t.id === expected.arm)!);
    assert.equal(call.model, expected.model);
    assert.equal(call.maxTokens, expected.maxTokens, `${expected.arm}/${expected.case}`);
    if (expected.inputBound !== undefined) {
      assert.equal(
        reservationInputTokens(call, item.production.usage?.inputTokens ?? null),
        expected.inputBound,
        `${expected.arm}/${expected.case}`,
      );
    }
    assert.equal(
      upperBoundUsd(call, pricing, item.production.usage?.inputTokens ?? null, at),
      Number(expected.upperBoundUsd),
      `${expected.arm}/${expected.case}`,
    );
  }
});

// ---------------------------------------------------------------------------

function makeCase(overrides: Partial<Case> = {}): Case {
  return {
    id: "case-1",
    traceId: null,
    ts: null,
    source: "replay-jsonl",
    provider: "openai",
    model: "gpt-6-luna",
    system: null,
    messages: [{ role: "user", content: "hello" }],
    responseFormat: null,
    forcedTool: null,
    params: { temperature: null, maxTokens: null, topP: null, stop: null, reasoningEffort: null },
    webSearch: false,
    production: {
      output: "hi",
      finishReason: "stop",
      refusal: false,
      usage: { inputTokens: 1000, outputTokens: 100, cachedInputTokens: null, reasoningTokens: null },
      costUsd: null,
      latencyMs: null,
    },
    inputPreview: null,
    fidelity: "full",
    missing: [],
    notReplayable: null,
    ...overrides,
  };
}

function makeTreatment(overrides: Partial<Treatment> = {}): Treatment {
  return {
    id: "t1",
    label: "t1",
    model: "openai/gpt-6-luna",
    endpoint: "vercel",
    settings: { temperature: null, maxTokens: null, reasoningEffort: null },
    ...overrides,
  };
}

test("the cap sent is the treatment's, else production's, else 4096", () => {
  assert.equal(capSent(500, 2000), 500);
  assert.equal(capSent(null, 2000), 2000);
  assert.equal(capSent(null, null), 4096);
  assert.equal(capSent(0, 0), 4096);
});

test("assumed output is max(production or 512, half the cap), never above the cap", () => {
  assert.equal(assumedOutputTokens(4096, 100), 2048);
  assert.equal(assumedOutputTokens(4096, 3000), 3000);
  assert.equal(assumedOutputTokens(1000, 3000), 1000);
  assert.equal(assumedOutputTokens(600, null), 512);
  assert.equal(assumedOutputTokens(300, null), 300);
  assert.equal(assumedOutputTokens(4095, 0), 2047);
});

test("the input bound counts bytes, framing, and is floored at production's count", () => {
  const call: ReplayCall = treatmentCall(makeCase({ system: "abc", messages: [{ role: "user", content: "é" }] }), makeTreatment());
  // 3 + 2 bytes, 16 * (1 + 1) framing, 64 base.
  assert.equal(reservationInputTokens(call), 3 + 2 + 32 + 64);
  assert.equal(reservationInputTokens(call, 5000), 5000);
});

test("JSON is measured as Python's json.dumps writes it", () => {
  assert.equal(pythonJson({ a: [1, 2.5, "é"], b: null, c: true }), '{"a": [1, 2.5, "\\u00e9"], "b": null, "c": true}');
  assert.equal(pythonJson({}), "{}");
  assert.equal(pythonJson([]), "[]");
  assert.equal(pythonJson("line\nbreak\t\"q\"\\"), '"line\\nbreak\\t\\"q\\"\\\\"');
  assert.equal(pythonJson("\u0001"), '"\\u0001"');
  assert.equal(pythonJson("😀"), '"\\ud83d\\ude00"');
  assert.equal(pythonJson(0.00001), "1e-05");
  assert.equal(pythonJson(1e21), "1e+21");
  assert.equal(pythonJson(0.5), "0.5");
});

test("an unpriceable treatment is reported, not counted", () => {
  const estimate = estimateRun([makeCase()], [makeTreatment({ model: "example/unknown" })], { noiseFloor: false, at: "2026-10-20" }, pricing);
  assert.deepEqual(estimate.unpriced, ["example/unknown"]);
  assert.deepEqual(estimate.perArm["t1"], { expectedUsd: 0, upperBoundUsd: 0 });
  assert.equal(upperBoundUsd(treatmentCall(makeCase(), makeTreatment({ model: "example/unknown" })), pricing), null);
});

test("an unpriceable production model leaves the floor unpriced for that case", () => {
  const estimate = estimateRun([makeCase({ provider: "example", model: "mystery" })], [], { noiseFloor: true, at: "2026-10-20" }, pricing);
  assert.deepEqual(estimate.unpriced, ["example/mystery"]);
  assert.equal(estimate.expectedUsd, 0);
});

test("a case without a production model is reported for the floor", () => {
  const estimate = estimateRun([makeCase({ provider: null, model: null })], [], { noiseFloor: true, at: "2026-10-20" }, pricing);
  assert.deepEqual(estimate.unpriced, ["(production model not recorded)"]);
});

test("the Ollama preset is free; other endpoints are priced", () => {
  const cases = [makeCase()];
  const free = estimateRun(cases, [makeTreatment({ endpoint: "ollama", model: "llama3.2" })], { noiseFloor: false, at: "2026-10-20" }, pricing);
  assert.deepEqual(free.perArm["t1"], { expectedUsd: 0, upperBoundUsd: 0 });
  assert.deepEqual(free.unpriced, []);
  const custom = estimateRun(cases, [makeTreatment({ endpoint: "localhost:8000", model: "llama3.2" })], {
    noiseFloor: false,
    at: "2026-10-20",
    isFree: (t) => t.endpoint.startsWith("localhost"),
  }, pricing);
  assert.deepEqual(custom.unpriced, []);
});

test("the margin rounds each arm up to $0.0001", () => {
  // 1000 in, 2048 out on gpt-6-luna: 0.0001 + 0.001024 = 0.001124, x1.25 = 0.001405 -> 0.0015.
  const estimate = estimateRun([makeCase()], [makeTreatment()], { noiseFloor: false, at: "2026-10-20" }, pricing);
  assert.equal(estimate.perArm["t1"]?.expectedUsd, 0.0015);
  assert.equal(estimate.expectedUsd, 0.0015);
});

test("not-replayable cases are counted by reason and never priced", () => {
  const cases = [
    makeCase({ id: "a", notReplayable: "content_truncated" }),
    makeCase({ id: "b", notReplayable: "content_truncated" }),
    makeCase({ id: "c", notReplayable: "no_messages" }),
  ];
  const estimate = estimateRun(cases, [makeTreatment()], { noiseFloor: true, at: "2026-10-20" }, pricing);
  assert.equal(estimate.replayable, 0);
  assert.deepEqual(estimate.notReplayable, { content_truncated: 2, no_messages: 1 });
  assert.equal(estimate.expectedUsd, 0);
  assert.equal(estimate.upperBoundUsd, 0);
});

test("floorModel falls back to provider/model with a pricing that cannot resolve", () => {
  const plain: Pricing = { catalogVersion: "x", price: () => 1, knows: () => true };
  assert.equal(floorModel(makeCase({ provider: "Anthropic", model: "claude-x" }), plain), "anthropic/claude-x");
  assert.equal(floorModel(makeCase({ provider: null, model: "claude-x" }), plain), "claude-x");
  assert.equal(floorModel(makeCase({ provider: "openai", model: "openai/gpt-x" }), plain), "openai/gpt-x");
});

test("a treatment's call carries production's settings where the treatment sets none", () => {
  const item = makeCase({
    params: { temperature: 0.7, maxTokens: 300, topP: 0.9, stop: ["END"], reasoningEffort: "low" },
  });
  const call = treatmentCall(item, makeTreatment({ settings: { temperature: 0, maxTokens: null, reasoningEffort: "high" } }));
  assert.equal(call.temperature, 0);
  assert.equal(call.maxTokens, 300);
  assert.equal(call.topP, 0.9);
  assert.deepEqual(call.stop, ["END"]);
  assert.equal(call.reasoningEffort, "high");
});
