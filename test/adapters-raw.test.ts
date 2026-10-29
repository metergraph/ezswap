import { test } from "node:test";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { readTraces } from "../src/adapters/index.ts";
import { parseResponseBody } from "../src/adapters/response.ts";
import { prepareCase } from "../src/cases/index.ts";

const path = (name: string) => fileURLToPath(new URL(`./fixtures/${name}`, import.meta.url));

test("raw pairs: OpenAI chat, Responses, Anthropic and Gemini", async () => {
  const calls = await readTraces(path("raw-pairs.jsonl"));
  assert.equal(calls.length, 4);
  assert.ok(calls.every((c) => c.source === "raw"));

  const [chat, responses, anthropic, gemini] = calls as [any, any, any, any];
  assert.equal(chat.id, "raw-chat");
  assert.equal(chat.provider, "openai");
  assert.equal(chat.model, "gpt-4o-mini");
  assert.equal(chat.response.text, '{"label":"positive"}');
  assert.equal(chat.response.finishReason, "stop");
  assert.equal(chat.response.latencyMs, 640);
  assert.equal(chat.response.costUsd, 0.00002);
  assert.deepEqual(chat.response.usage, { inputTokens: 22, outputTokens: 6, cachedInputTokens: 0, reasoningTokens: 0 });
  assert.deepEqual(prepareCase(chat).responseFormat, { type: "json_object" });

  assert.equal(responses.id, "resp_1");
  assert.equal(responses.provider, "openai");
  assert.equal(responses.ts, "2026-03-02T09:01:00.000Z");
  assert.equal(responses.response.text, "4");
  assert.equal(responses.response.finishReason, "completed");
  assert.equal(responses.response.usage.reasoningTokens, 38);
  assert.equal(prepareCase(responses).params.reasoningEffort, "low");

  assert.equal(anthropic.provider, "anthropic");
  assert.equal(anthropic.response.text, "Red.");
  assert.equal(anthropic.response.finishReason, "end_turn");
  assert.equal(prepareCase(anthropic).system, "Be concise.");

  assert.equal(gemini.provider, "google");
  assert.equal(gemini.response.text, "Hi!");
  assert.equal(gemini.response.usage.inputTokens, 3);
  assert.equal(prepareCase(gemini).params.maxTokens, 10);
  for (const c of calls) assert.equal(prepareCase(c).notReplayable, null, c.id);
});

test("raw: Helicone export with bodies, and the placeholder without them", async () => {
  const [withBody, placeholder] = (await readTraces(path("helicone.jsonl"))) as [any, any];
  assert.equal(withBody.id, "hc-1");
  assert.equal(withBody.traceId, "session-1");
  assert.equal(withBody.provider, "openai");
  assert.equal(withBody.ts, "2026-03-03T08:00:00.000Z");
  assert.equal(withBody.response.text, "Lima.");
  assert.equal(withBody.response.latencyMs, 950);
  assert.equal(withBody.response.costUsd, 0.0000042);
  assert.deepEqual(withBody.response.usage, { inputTokens: 12, outputTokens: 4, cachedInputTokens: 2, reasoningTokens: null });
  assert.equal(prepareCase(withBody).notReplayable, null);

  assert.equal(placeholder.request, null);
  assert.deepEqual(placeholder.missing, ["request_body", "response_body"]);
  assert.equal(placeholder.response.text, null);
  assert.equal(placeholder.response.costUsd, 0.00008);
  assert.equal(prepareCase(placeholder).notReplayable, "content_unavailable");
});

test("raw: LiteLLM StandardLoggingPayload, plain and redacted", async () => {
  const [plain, redacted] = (await readTraces(path("litellm.jsonl"))) as [any, any];
  assert.equal(plain.id, "ll-1");
  assert.equal(plain.traceId, "ll-trace-1");
  assert.equal(plain.provider, "openai");
  assert.equal(plain.model, "gpt-4o-mini");
  assert.equal(plain.ts, "2026-03-03T09:00:00.250Z");
  assert.equal(plain.response.latencyMs, 750);
  assert.equal(plain.response.costUsd, 0.0000155);
  assert.equal(plain.response.text, "The cat sat.");
  const kase = prepareCase(plain);
  assert.equal(kase.notReplayable, null);
  assert.equal(kase.system, "You fix typos.");
  assert.deepEqual(kase.params.stop, ["\n"]);
  assert.equal(kase.params.maxTokens, 30);
  assert.ok(kase.missing.includes("tools_not_offered"), "tools from model_parameters are seen");

  assert.equal(redacted.request, null);
  assert.deepEqual(redacted.missing, ["messages", "response"]);
  assert.equal(redacted.response.text, null);
  assert.deepEqual(redacted.response.usage, { inputTokens: 40, outputTokens: 12, cachedInputTokens: null, reasoningTokens: null });
  assert.equal(prepareCase(redacted).notReplayable, "content_unavailable");
});

test("response bodies: refusals and tool calls", () => {
  const refusal = parseResponseBody({
    choices: [{ message: { role: "assistant", content: null, refusal: "I can't help with that." }, finish_reason: "stop" }],
  });
  assert.equal(refusal.refusal, true);
  assert.equal(refusal.text, null);

  const tool = parseResponseBody({
    choices: [{ message: { role: "assistant", content: null, tool_calls: [{ id: "1", function: { name: "f", arguments: "{}" } }] }, finish_reason: "tool_calls" }],
  });
  assert.equal(tool.toolCalls?.length, 1);

  const anthropic = parseResponseBody({ type: "message", role: "assistant", content: [{ type: "tool_use", id: "t", name: "f", input: { a: 1 } }], stop_reason: "refusal" });
  assert.equal(anthropic.refusal, true);
  assert.equal(anthropic.toolCalls?.length, 1);

  const incomplete = parseResponseBody({ object: "response", status: "incomplete", incomplete_details: { reason: "max_output_tokens" }, output: [] });
  assert.equal(incomplete.finishReason, "max_output_tokens");

  const geminiSafety = parseResponseBody({ candidates: [{ content: { parts: [] }, finishReason: "SAFETY" }] });
  assert.equal(geminiSafety.refusal, true);

  assert.equal(parseResponseBody("plain text").text, "plain text");
  assert.equal(parseResponseBody({ something: "else" }).shape, null);
});
