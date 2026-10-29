import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { loadPricing } from "../src/pricing/catalog.ts";
import { floorCall, floorModel, treatmentCall, upperBoundUsd } from "../src/pricing/estimate.ts";
import {
  buildTreatments,
  executeRun,
  parseEndpoint,
  parseModels,
  planRun,
  sampleCases,
  writeRun,
  type RunPlan,
} from "../src/run/index.ts";
import type { Case } from "../src/types.ts";
import { failedOutcome, fakeProvider, okOutcome, tempDir, writeTraces } from "./run-helpers.ts";

const AT = "2026-10-28T12:00:00.000Z";
const NOW = () => new Date(AT);
const pricing = loadPricing({ now: NOW });

async function plan(count: number, overrides: Partial<Parameters<typeof planRun>[0]> = {}): Promise<RunPlan> {
  const file = writeTraces(tempDir(), count);
  return planRun({
    file,
    treatments: [{ model: "google/gemini-2.5-flash-lite", endpoint: "openrouter" }],
    floorEndpoint: "openrouter",
    pricing,
    at: AT,
    ...overrides,
  });
}

/** What one case reserves: every arm's upper bound. */
function caseBound(p: RunPlan, item: Case): number {
  let total = 0;
  for (const t of p.treatments) total += upperBoundUsd(treatmentCall(item, t), pricing, item.production.usage?.inputTokens, AT)!;
  if (p.floorEndpoint) {
    const model = floorModel(item, pricing, AT)!;
    total += 2 * upperBoundUsd(floorCall(item, model), pricing, item.production.usage?.inputTokens, AT)!;
  }
  return total;
}

test("planRun samples the latest replayable cases, one per trace, and keeps the unreplayable ones among them", async () => {
  const dir = tempDir();
  // Calls 1-10; 7 and 8 share a trace; 9 is cut short; 10 searched the web.
  const file = writeTraces(dir, 10, (n) => ({
    ...(n === 7 || n === 8 ? { traceId: "shared" } : {}),
    ...(n === 9 ? { truncated: true } : {}),
    ...(n === 10 ? { search: true } : {}),
  }));
  const p = await planRun({
    file,
    sample: 3,
    treatments: [{ model: "google/gemini-2.5-flash-lite", endpoint: "openrouter" }],
    pricing,
    at: AT,
  });
  assert.equal(p.file.calls, 10);
  assert.equal(p.file.traces, 9);
  assert.equal(p.input.file, "traces.jsonl");
  assert.equal(p.input.adapter, "replay-jsonl");
  assert.deepEqual(
    p.cases.map((c) => c.id),
    ["call-5", "call-6", "call-8", "call-9", "call-10"],
  );
  assert.equal(p.estimate.replayable, 3);
  assert.deepEqual(p.estimate.notReplayable, { content_truncated: 1, web_search_not_replayed: 1 });

  const allowed = await planRun({
    file,
    sample: 3,
    allowNoSearch: true,
    treatments: [{ model: "google/gemini-2.5-flash-lite", endpoint: "openrouter" }],
    pricing,
    at: AT,
  });
  assert.deepEqual(allowed.cases.map((c) => c.id), ["call-6", "call-8", "call-9", "call-10"]);
});

test("sampleCases orders by time when every case has one", () => {
  const base = { notReplayable: null, traceId: null } as unknown as Case;
  const cases = [
    { ...base, id: "b", ts: "2026-10-02T00:00:00Z" },
    { ...base, id: "a", ts: "2026-10-01T00:00:00Z" },
    { ...base, id: "c", ts: "2026-10-03T00:00:00Z" },
  ] as Case[];
  assert.deepEqual(sampleCases(cases, 2).map((c) => c.id), ["b", "c"]);
});

test("planRun rejects a sample above the maximum and too many tracked fields", async () => {
  await assert.rejects(plan(2, { sample: 201 }), /from 1 to 200/);
  await assert.rejects(plan(2, { trackedFields: Array.from({ length: 11 }, (_, i) => `$.f${i}`) }), /at most 10/);
});

test("buildTreatments numbers treatments and keeps labels unique", () => {
  const treatments = buildTreatments([
    { model: "openai/gpt-5-mini", endpoint: "openrouter" },
    { model: "openai/gpt-5-mini", endpoint: "openai" },
    { model: "openai/gpt-5-mini", endpoint: "openai", settings: { temperature: 0.2 } },
  ]);
  assert.deepEqual(treatments.map((t) => t.id), ["t1", "t2", "t3"]);
  assert.equal(new Set(treatments.map((t) => t.label)).size, 3);
  assert.equal(treatments[2]!.settings.temperature, 0.2);
});

test("parseModels reads endpoint prefixes and per-model settings", () => {
  const specs = parseModels(
    "openrouter:anthropic/claude-haiku-4.5,openai:gpt-5-mini@temperature=0.2,max_tokens=500,google/gemini-2.5-flash",
    parseEndpoint("vercel"),
  );
  assert.deepEqual(
    specs.map((s) => [s.model, s.endpoint.key, s.settings]),
    [
      ["anthropic/claude-haiku-4.5", "openrouter", {}],
      ["openai/gpt-5-mini", "openai", { temperature: 0.2, maxTokens: 500 }],
      ["google/gemini-2.5-flash", "vercel", {}],
    ],
  );
  assert.throws(() => parseModels("x@top_k=3", parseEndpoint("openrouter")), /Settings are/);
  const local = parseEndpoint("http://localhost:8000/v1/");
  assert.deepEqual([local.key, local.baseUrl, local.free], ["localhost:8000", "http://localhost:8000/v1", true]);
  assert.equal(parseEndpoint("https://llm.example.com/v1").free, false);
  assert.throws(() => parseEndpoint("https://user:secret@llm.example.com/v1"), /environment variable/);
});

test("executeRun replays every case on every treatment and twice for the floor, priced from the catalog", async () => {
  const p = await plan(3);
  const provider = fakeProvider();
  const run = await executeRun(p, { providers: { openrouter: provider }, pricing, now: NOW });
  assert.equal(run.replays.length, 3 * 3);
  assert.deepEqual(run.replays.slice(0, 3).map((r) => r.arm), ["t1", "floor_a1", "floor_a2"]);
  assert.equal(provider.calls.length, 9);
  assert.equal(provider.calls[1]!.model, "openai/gpt-4.1-mini");
  assert.ok(run.replays.every((r) => r.status === "ok" && r.costSource === "catalog" && r.costUsd! > 0));
  const sum = run.replays.reduce((total, r) => total + r.costUsd!, 0);
  assert.ok(Math.abs(run.spentUsd - sum) < 1e-8);
  assert.equal(run.stopped, null);
  assert.equal(run.createdAt, AT);
  assert.equal(run.schema, "metergraph.replay-run/v1");
});

test("cost sources: endpoint-reported, reservation for a maybe-billed failure, zero for a failure that was not billed", async () => {
  const p = await plan(1, { noiseFloor: false });
  const item = p.cases[0]!;
  const reserve = upperBoundUsd(treatmentCall(item, p.treatments[0]!), pricing, item.production.usage?.inputTokens, AT)!;

  const reported = await executeRun(p, {
    providers: { openrouter: fakeProvider(() => okOutcome({ reportedCostUsd: 0.00001 })) },
    pricing,
    now: NOW,
  });
  assert.deepEqual([reported.replays[0]!.costSource, reported.replays[0]!.costUsd], ["endpoint", 0.00001]);

  const billed = await executeRun(p, { providers: { openrouter: fakeProvider(() => failedOutcome(true)) }, pricing, now: NOW });
  assert.deepEqual([billed.replays[0]!.costSource, billed.replays[0]!.costUsd], ["reservation", Math.round(reserve * 1e8) / 1e8]);
  assert.equal(billed.stopped, null);

  const refused = await executeRun(p, { providers: { openrouter: fakeProvider(() => failedOutcome(false)) }, pricing, now: NOW });
  assert.deepEqual([refused.replays[0]!.costSource, refused.replays[0]!.costUsd], [null, 0]);

  const noUsage = await executeRun(p, {
    providers: { openrouter: fakeProvider(() => okOutcome({ usage: null })) },
    pricing,
    now: NOW,
  });
  assert.equal(noUsage.replays[0]!.costSource, "reservation");
});

test("a free endpoint costs nothing and reserves nothing, so a zero budget still runs", async () => {
  const p = await plan(2, {
    treatments: [{ model: "llama3.2", endpoint: "ollama" }],
    floorEndpoint: "ollama",
    isFreeEndpoint: (e) => e === "ollama",
    budgetUsd: 0,
  });
  assert.equal(p.estimate.expectedUsd, 0);
  assert.deepEqual(p.estimate.unpriced, []);
  const run = await executeRun(p, { providers: { ollama: fakeProvider() }, pricing, now: NOW });
  assert.equal(run.replays.length, 6);
  assert.ok(run.replays.every((r) => r.costSource === "free" && r.costUsd === 0));
  assert.equal(run.stopped, null);
});

test("the budget stops the run between whole cases and is never exceeded", async () => {
  const p = await plan(5);
  const bound = caseBound(p, p.cases[0]!);
  // Every call costs its full reservation, so exactly two cases fit.
  const budgetUsd = bound * 2.5;
  const provider = fakeProvider(() => failedOutcome(true));
  const run = await executeRun(p, { providers: { openrouter: provider }, pricing, budgetUsd, now: NOW });
  assert.equal(run.stopped, "budget");
  assert.equal(provider.calls.length, 6);
  assert.ok(run.spentUsd <= budgetUsd);
  const byCase = new Map<string, Set<string>>();
  for (const r of run.replays) {
    const statuses = byCase.get(r.caseId) ?? new Set();
    statuses.add(r.status);
    byCase.set(r.caseId, statuses);
  }
  // No case is half replayed.
  for (const statuses of byCase.values()) assert.equal(statuses.size, 1);
  const skipped = run.replays.filter((r) => r.status === "skipped");
  assert.equal(skipped.length, 9);
  assert.ok(skipped.every((r) => r.error?.code === "budget_exhausted"));
  assert.equal(run.settings.budgetUsd, budgetUsd);
});

test("a case waits for calls in flight to settle before the budget stops the run", async () => {
  const p = await plan(4);
  const bound = caseBound(p, p.cases[0]!);
  // Reservations hold two cases at once, but each settles far below them.
  const run = await executeRun(p, {
    providers: { openrouter: fakeProvider() },
    pricing,
    budgetUsd: bound * 1.5,
    concurrency: 4,
    now: NOW,
  });
  assert.equal(run.stopped, null);
  assert.ok(run.replays.every((r) => r.status === "ok"));
});

test("a call is never sent when its case does not fit the budget", async () => {
  const p = await plan(2);
  const provider = fakeProvider();
  const run = await executeRun(p, {
    providers: { openrouter: provider },
    pricing,
    budgetUsd: caseBound(p, p.cases[0]!) / 2,
    now: NOW,
  });
  assert.equal(provider.calls.length, 0);
  assert.equal(run.stopped, "budget");
  assert.equal(run.spentUsd, 0);
});

test("a call that costs more than its reservation stops the run", async () => {
  const p = await plan(3);
  const provider = fakeProvider(() => okOutcome({ reportedCostUsd: 5 }));
  const run = await executeRun(p, { providers: { openrouter: provider }, pricing, budgetUsd: 100, concurrency: 1, now: NOW });
  assert.equal(run.stopped, "reservation_exceeded");
  assert.equal(provider.calls.length, 1);
  assert.equal(run.replays.filter((r) => r.status === "ok").length, 1);
  assert.ok(run.replays.filter((r) => r.status === "skipped").every((r) => r.error?.code === "reservation_exceeded"));
});

test("an interrupt lets calls in flight finish and records what exists", async () => {
  const p = await plan(4);
  const stop = new AbortController();
  const provider = fakeProvider((_, index) => {
    if (index === 0) stop.abort();
    return okOutcome();
  });
  const run = await executeRun(p, {
    providers: { openrouter: provider },
    pricing,
    signal: stop.signal,
    concurrency: 2,
    now: NOW,
  });
  assert.equal(run.stopped, "interrupted");
  const ok = run.replays.filter((r) => r.status === "ok");
  // Both calls in flight when the interrupt came were recorded.
  assert.equal(ok.length, provider.calls.length);
  assert.ok(ok.length >= 1 && ok.length <= 2);
  assert.equal(run.replays.length, 12);
  assert.ok(run.replays.filter((r) => r.status === "skipped").every((r) => r.error?.code === "interrupted"));
});

test("a provider is needed for every endpoint the plan names", async () => {
  const p = await plan(1);
  await assert.rejects(executeRun(p, { providers: {}, pricing }), /No provider/);
});

test("writeRun writes run.json and nothing else", async () => {
  const p = await plan(1, { noiseFloor: false });
  const run = await executeRun(p, { providers: { openrouter: fakeProvider() }, pricing, now: NOW });
  const dir = tempDir();
  const written = await writeRun(dir, run);
  const back = JSON.parse(readFileSync(written.runPath, "utf8"));
  assert.deepEqual(back, JSON.parse(JSON.stringify(run)));
  assert.deepEqual(readdirSync(dir), ["run.json"]);
});

test("trafficOf spans the file's timed calls and ignores untimed ones", async () => {
  const { trafficOf } = await import("../src/run/plan.ts");
  assert.deepEqual(
    trafficOf([{ ts: "2026-10-02T00:00:00Z" }, { ts: null }, { ts: "2026-10-01T00:00:00Z" }, { ts: "not a time" }]),
    { calls: 4, from: "2026-10-01T00:00:00.000Z", to: "2026-10-02T00:00:00.000Z" },
  );
  assert.equal(trafficOf([{ ts: null }]), null);
});
