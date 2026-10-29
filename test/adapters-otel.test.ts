import { test } from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { detectAdapter, readTraces } from "../src/adapters/index.ts";
import { prepareCase } from "../src/cases/index.ts";

const path = (name: string) => fileURLToPath(new URL(`./fixtures/${name}`, import.meta.url));

test("otel: an OTLP JSON export keeps LLM spans only", async () => {
  const calls = await readTraces(path("otel-export.json"));
  assert.deepEqual(calls.map((c) => c.id), ["eee19b7ec3c1b174", "aaa19b7ec3c1b175", "bbb19b7ec3c1b176"]);
  assert.ok(calls.every((c) => c.source === "otel" && c.traceId === "5b8efff798038103d269b633813fc60c"));
});

test("otel: current gen_ai.input.messages / output.messages attributes", async () => {
  const [current] = await readTraces(path("otel-export.json"));
  assert.ok(current);
  assert.equal(current.provider, "openai");
  assert.equal(current.model, "gpt-4o-mini");
  assert.equal(current.ts, "2026-03-04T10:00:00.000Z");
  assert.equal(current.response.latencyMs, 900);
  assert.equal(current.response.text, "May or September.");
  assert.equal(current.response.finishReason, "stop");
  assert.deepEqual(current.response.usage, { inputTokens: 25, outputTokens: 5, cachedInputTokens: 0, reasoningTokens: null });
  assert.equal(current.response.costUsd, null);
  assert.deepEqual(current.missing, ["tools", "response_format"]);
  const kase = prepareCase(current);
  assert.equal(kase.notReplayable, null);
  assert.equal(kase.system, "You answer travel questions.");
  assert.deepEqual(kase.messages, [{ role: "user", content: "Best month to visit Lisbon?" }]);
  assert.deepEqual(kase.params, { temperature: 0.2, maxTokens: 120, topP: 0.95, stop: ["END"], reasoningEffort: null });
  assert.equal(kase.fidelity, "partial");
});

test("otel: legacy gen_ai.prompt.N / completion.N and the structured output schema", async () => {
  const [, legacy] = await readTraces(path("otel-export.json"));
  assert.ok(legacy);
  assert.equal(legacy.provider, "openai", "from gen_ai.system");
  assert.equal(legacy.response.text, '{"fruits":["apple","pear"]}');
  assert.equal(legacy.response.finishReason, "stop");
  assert.equal(legacy.response.usage?.inputTokens, 30);
  assert.deepEqual(legacy.missing, ["tool_choice"]);
  const kase = prepareCase(legacy);
  assert.equal(kase.notReplayable, null);
  assert.equal(kase.system, "Return JSON.");
  assert.deepEqual(kase.messages, [{ role: "user", content: "List two fruits." }]);
  assert.deepEqual(kase.responseFormat, {
    type: "json_schema",
    json_schema: { name: "output", schema: { type: "object", properties: { fruits: { type: "array" } } } },
  });
  assert.ok(kase.missing.includes("tools_not_offered"), "llm.request.functions are offered tools");
});

test("otel: a span without captured content has no request", async () => {
  const [, , bare] = await readTraces(path("otel-export.json"));
  assert.ok(bare);
  assert.equal(bare.request, null);
  assert.ok(bare.missing.includes("input_messages"));
  assert.ok(bare.missing.includes("output_messages"));
  assert.equal(bare.response.usage?.inputTokens, 40);
  assert.equal(prepareCase(bare).notReplayable, "content_unavailable");
});

test("otel: one span per line, tool history and a cut attribute", async () => {
  const [tools, cut] = await readTraces(path("otel-spans.jsonl"));
  assert.ok(tools && cut);
  assert.equal(tools.provider, "anthropic");
  assert.deepEqual(tools.missing, ["tool_choice", "response_format"]);
  assert.equal(tools.response.text, "It is sunny.");
  assert.equal(prepareCase(tools).notReplayable, "tool_history");

  assert.equal(cut.id, "0x9a8b");
  assert.equal(cut.traceId, "0xe1f2");
  assert.equal(cut.ts, "2026-03-04T12:30:00.000Z");
  assert.equal(cut.response.latencyMs, 500);
  assert.equal(cut.truncated, true);
  assert.equal(prepareCase(cut).notReplayable, "content_truncated");
});

test("otel: detection", () => {
  assert.equal(detectAdapter({ resourceSpans: [] })?.name, "otel");
  assert.equal(
    detectAdapter({ spanId: "1", attributes: [{ key: "gen_ai.request.model", value: { stringValue: "m" } }] })?.name,
    "otel",
  );
  assert.equal(detectAdapter({ spanId: "1", attributes: [{ key: "http.method", value: { stringValue: "GET" } }] }), null);
});
