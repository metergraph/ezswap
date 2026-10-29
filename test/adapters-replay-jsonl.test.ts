import { test } from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { readTraces } from "../src/adapters/index.ts";
import { prepareCase } from "../src/cases/index.ts";

const fixture = fileURLToPath(new URL("./fixtures/replay.jsonl", import.meta.url));

test("replay-jsonl: maps request, response, usage and meta", async () => {
  const calls = await readTraces(fixture);
  assert.equal(calls.length, 4);
  const [first] = calls;
  assert.ok(first);
  assert.equal(first.source, "replay-jsonl");
  assert.equal(first.id, "rp-1");
  assert.equal(first.traceId, "tr-1");
  assert.equal(first.ts, "2026-03-01T10:00:00.000Z");
  assert.equal(first.provider, "openai");
  assert.equal(first.model, "gpt-4o-mini");
  assert.equal(first.truncated, false);
  assert.deepEqual(first.missing, []);
  assert.equal(first.response.text, "A calm blue mug for slow mornings.");
  assert.equal(first.response.finishReason, "stop");
  assert.deepEqual(first.response.usage, { inputTokens: 31, outputTokens: 9, cachedInputTokens: 0, reasoningTokens: null });
  assert.equal(first.response.costUsd, 0.0000101);
  assert.equal(first.response.latencyMs, 812);

  const kase = prepareCase(first);
  assert.equal(kase.notReplayable, null);
  assert.equal(kase.system, "You write short product blurbs for example.com.");
  assert.deepEqual(kase.messages, [{ role: "user", content: "Write a blurb for a blue mug." }]);
  assert.deepEqual(kase.params, { temperature: 0.3, maxTokens: 200, topP: 0.9, stop: ["\n\n"], reasoningEffort: null });
});

test("replay-jsonl: forced tool and tool-call output", async () => {
  const [, second] = await readTraces(fixture);
  assert.ok(second);
  assert.deepEqual(second.missing, ["response_format"]);
  const kase = prepareCase(second);
  assert.equal(kase.notReplayable, null);
  assert.equal(kase.forcedTool?.name, "record_city");
  assert.equal(kase.production.output, '{"city":"Lyon"}');
  assert.equal(kase.fidelity, "partial");
});

test("replay-jsonl: meta.truncated and a verbatim raw body", async () => {
  const calls = await readTraces(fixture);
  assert.equal(calls[2]?.truncated, true);
  assert.equal(prepareCase(calls[2]!).notReplayable, "content_truncated");
  const fourth = calls[3]!;
  assert.equal(fourth.ts, "2026-03-01T10:03:00.000Z", "epoch seconds");
  const kase = prepareCase(fourth);
  assert.deepEqual(kase.messages, [{ role: "user", content: "Hello from a verbatim body" }]);
  assert.equal(kase.params.temperature, 1);
});

test("replay-jsonl: limit stops early", async () => {
  assert.equal((await readTraces(fixture, { limit: 2 })).length, 2);
});
