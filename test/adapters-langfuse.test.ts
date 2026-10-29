import { test } from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { detectAdapter, readTraces } from "../src/adapters/index.ts";
import { prepareCase } from "../src/cases/index.ts";

const path = (name: string) => fileURLToPath(new URL(`./fixtures/${name}`, import.meta.url));

test("langfuse: GENERATION observations only", async () => {
  const calls = await readTraces(path("langfuse.jsonl"));
  assert.deepEqual(calls.map((c) => c.id), ["lf-1", "lf-3", "lf-4"]);
  assert.ok(calls.every((c) => c.source === "langfuse"));
});

test("langfuse: input with tools, string model parameters, usage and cost", async () => {
  const [first] = await readTraces(path("langfuse.jsonl"));
  assert.ok(first);
  assert.equal(first.traceId, "lf-trace-1");
  assert.equal(first.ts, "2026-03-04T12:00:00.000Z");
  assert.equal(first.provider, "openai");
  assert.equal(first.model, "gpt-4o-mini");
  assert.deepEqual(first.missing, [], "tools were logged");
  assert.equal(first.response.text, "shipping");
  assert.equal(first.response.latencyMs, 1250);
  assert.equal(first.response.costUsd, 0.0000072);
  assert.deepEqual(first.response.usage, { inputTokens: 40, outputTokens: 2, cachedInputTokens: 0, reasoningTokens: null });
  const kase = prepareCase(first);
  assert.equal(kase.notReplayable, null);
  assert.equal(kase.system, "You tag support tickets.");
  assert.deepEqual(kase.params, { temperature: 0.4, maxTokens: 150, topP: 1, stop: null, reasoningEffort: null });
  assert.deepEqual(kase.missing, ["tools_not_offered"]);
});

test("langfuse: bare message input marks tools missing; JSON output is the answer", async () => {
  const [, third] = await readTraces(path("langfuse.jsonl"));
  assert.ok(third);
  assert.deepEqual(third.missing, ["tools"]);
  assert.equal(third.provider, "anthropic");
  assert.equal(third.response.text, '{"items":["bread","cheese"]}');
  assert.equal(third.response.costUsd, 0.00004);
  assert.deepEqual(third.response.usage, { inputTokens: 11, outputTokens: 9, cachedInputTokens: null, reasoningTokens: null });
  const kase = prepareCase(third);
  assert.equal(kase.notReplayable, null);
  assert.equal(kase.fidelity, "partial");
});

test("langfuse: a truncated preview is refused", async () => {
  const calls = await readTraces(path("langfuse.jsonl"));
  const last = calls[2]!;
  assert.equal(last.truncated, true);
  assert.equal(last.response.latencyMs, null, "no end time");
  assert.equal(prepareCase(last).notReplayable, "content_truncated");
});

test("langfuse: a JSON array export", async () => {
  const calls = await readTraces(path("langfuse-array.json"));
  assert.deepEqual(calls.map((c) => c.id), ["lfa-1", "lfa-3"]);
  assert.equal(prepareCase(calls[0]!).messages[0]?.content, "Spell 'example' backwards.");
});

test("langfuse: detection", () => {
  assert.equal(detectAdapter({ id: "x", traceId: "t", type: "GENERATION" })?.name, "langfuse");
  assert.equal(detectAdapter({ id: "x", modelParameters: {} })?.name, "langfuse");
});
