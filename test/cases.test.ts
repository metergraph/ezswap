import { test } from "node:test";
import assert from "node:assert/strict";
import { prepareCase, singleToolCallOutput, TOOLS_NOT_OFFERED } from "../src/cases/index.ts";
import type { CapturedCall, CapturedResponse } from "../src/types.ts";

function response(partial: Partial<CapturedResponse> = {}): CapturedResponse {
  return {
    text: "Production answer.",
    toolCalls: null,
    finishReason: "stop",
    refusal: false,
    usage: { inputTokens: 10, outputTokens: 5, cachedInputTokens: null, reasoningTokens: null },
    costUsd: 0.0001,
    latencyMs: 420,
    ...partial,
  };
}

function captured(request: unknown, partial: Partial<CapturedCall> = {}): CapturedCall {
  return {
    id: "call-1",
    traceId: "trace-1",
    ts: "2026-01-01T00:00:00.000Z",
    source: "raw",
    provider: "openai",
    model: "gpt-4o-mini",
    request,
    response: response(),
    truncated: false,
    missing: [],
    ...partial,
  };
}

const user = (content: unknown) => ({ role: "user", content });

test("OpenAI chat: system and developer turns fold, settings carry", () => {
  const kase = prepareCase(
    captured({
      model: "gpt-4o-mini",
      messages: [
        { role: "system", content: "You are terse." },
        { role: "developer", content: "Answer in English." },
        user("Hello"),
        { role: "assistant", content: "Hi." },
        user([{ type: "text", text: "Summarize" }, { type: "text", text: "this." }]),
      ],
      temperature: 0.2,
      top_p: 0.9,
      stop: "END",
      max_completion_tokens: 900,
      reasoning_effort: "low",
    }),
  );
  assert.equal(kase.notReplayable, null);
  assert.equal(kase.system, "You are terse.\n\nAnswer in English.");
  assert.deepEqual(kase.messages, [
    { role: "user", content: "Hello" },
    { role: "assistant", content: "Hi." },
    { role: "user", content: "Summarize\nthis." },
  ]);
  assert.deepEqual(kase.params, { temperature: 0.2, maxTokens: 900, topP: 0.9, stop: ["END"], reasoningEffort: "low" });
  assert.equal(kase.inputPreview, "Summarize this.");
  assert.equal(kase.fidelity, "full");
  assert.equal(kase.production.output, "Production answer.");
  assert.equal(kase.production.finishReason, "stop");
});

test("a recorded latency of 0 ms reads as not recorded", () => {
  const zero = prepareCase(captured({ messages: [user("x")] }, { response: response({ latencyMs: 0 }) }));
  assert.equal(zero.production.latencyMs, null);
  const timed = prepareCase(captured({ messages: [user("x")] }));
  assert.equal(timed.production.latencyMs, 420);
});

test("consecutive same-role turns merge, empty turns drop", () => {
  const kase = prepareCase(captured({ messages: [user("one"), { role: "assistant", content: "" }, user("two")] }));
  assert.deepEqual(kase.messages, [{ role: "user", content: "one\n\ntwo" }]);
});

test("OpenAI Responses: instructions, string input, text.format translated", () => {
  const kase = prepareCase(
    captured({
      model: "gpt-5-mini",
      instructions: "Extract fields.",
      input: "Invoice 42 from example.com",
      max_output_tokens: 64000,
      reasoning: { effort: "medium" },
      text: { format: { type: "json_schema", name: "invoice", schema: { type: "object" }, strict: true } },
    }),
  );
  assert.equal(kase.notReplayable, null);
  assert.equal(kase.system, "Extract fields.");
  assert.deepEqual(kase.messages, [{ role: "user", content: "Invoice 42 from example.com" }]);
  assert.equal(kase.params.maxTokens, 32000, "clamped");
  assert.equal(kase.params.reasoningEffort, "medium");
  assert.deepEqual(kase.responseFormat, {
    type: "json_schema",
    json_schema: { name: "invoice", schema: { type: "object" }, strict: true },
  });
});

test("Responses text.format without strict leaves strict out; json_object passes", () => {
  const schema = prepareCase(captured({ input: "x", text: { format: { type: "json_schema", schema: { type: "object" } } } }));
  assert.deepEqual(schema.responseFormat, { type: "json_schema", json_schema: { name: "output", schema: { type: "object" } } });
  const object = prepareCase(captured({ input: "x", text: { format: { type: "json_object" } } }));
  assert.deepEqual(object.responseFormat, { type: "json_object" });
  const chat = prepareCase(captured({ messages: [user("x")], response_format: { type: "json_object" } }));
  assert.deepEqual(chat.responseFormat, { type: "json_object" });
  const text = prepareCase(captured({ messages: [user("x")], response_format: { type: "text" } }));
  assert.equal(text.responseFormat, null);
});

test("Responses input items with typed parts", () => {
  const kase = prepareCase(
    captured({
      input: [{ type: "message", role: "user", content: [{ type: "input_text", text: "Classify this." }] }],
    }),
  );
  assert.deepEqual(kase.messages, [{ role: "user", content: "Classify this." }]);
});

test("Anthropic: system blocks and messages", () => {
  const kase = prepareCase(
    captured({
      model: "claude-sonnet-4-5",
      system: [{ type: "text", text: "Be kind.", cache_control: { type: "ephemeral" } }],
      messages: [user([{ type: "text", text: "Hi" }])],
      max_tokens: 1024,
      stop_sequences: ["\n\nHuman:"],
    }),
  );
  assert.equal(kase.system, "Be kind.");
  assert.equal(kase.params.maxTokens, 1024);
  assert.deepEqual(kase.params.stop, ["\n\nHuman:"]);
});

test("Gemini: contents, systemInstruction and config", () => {
  const kase = prepareCase(
    captured({
      contents: [
        { role: "user", parts: [{ text: "Hi" }] },
        { role: "model", parts: [{ text: "Hello" }] },
        { role: "user", parts: [{ text: "Translate to French." }] },
      ],
      config: {
        systemInstruction: { parts: [{ text: "Translator." }] },
        maxOutputTokens: 256,
        temperature: 0,
        topP: 0.5,
        stopSequences: ["STOP"],
      },
    }),
  );
  assert.equal(kase.notReplayable, null);
  assert.equal(kase.system, "Translator.");
  assert.deepEqual(kase.messages.map((m) => m.role), ["user", "assistant", "user"]);
  assert.deepEqual(kase.params, { temperature: 0, maxTokens: 256, topP: 0.5, stop: ["STOP"], reasoningEffort: null });
});

test("generationConfig and inferenceConfig spellings of the output cap", () => {
  assert.equal(prepareCase(captured({ contents: "hi", generationConfig: { maxOutputTokens: 77 } })).params.maxTokens, 77);
  assert.equal(prepareCase(captured({ messages: [user("hi")], inferenceConfig: { maxTokens: 88 } })).params.maxTokens, 88);
  assert.equal(prepareCase(captured({ messages: [user("hi")], max_tokens: 0 })).params.maxTokens, null);
  assert.equal(prepareCase(captured({ messages: [user("hi")], max_tokens: 1.5 })).params.maxTokens, null);
  assert.equal(prepareCase(captured({ messages: [user("hi")], max_tokens: true })).params.maxTokens, null);
});

test("OpenTelemetry messages with typed parts and system instructions", () => {
  const kase = prepareCase(
    captured({
      system: [{ type: "text", content: "System from OTel." }],
      messages: [{ role: "user", parts: [{ type: "text", content: "Question?" }] }],
      params: { temperature: 0.7, max_tokens: 300, top_p: 1, stop: ["###"] },
    }),
  );
  assert.equal(kase.notReplayable, null);
  assert.equal(kase.system, "System from OTel.");
  assert.deepEqual(kase.messages, [{ role: "user", content: "Question?" }]);
  assert.deepEqual(kase.params, { temperature: 0.7, maxTokens: 300, topP: 1, stop: ["###"], reasoningEffort: null });
});

test("a bare prompt string, as text or as a JSON string", () => {
  assert.deepEqual(prepareCase(captured("Just a prompt")).messages, [{ role: "user", content: "Just a prompt" }]);
  assert.deepEqual(prepareCase(captured(JSON.stringify({ messages: [user("encoded")] }))).messages, [
    { role: "user", content: "encoded" },
  ]);
});

// ---------------------------------------------------------------------------
// Not replayable

test("content_unavailable when there is no request", () => {
  const kase = prepareCase(captured(null));
  assert.equal(kase.notReplayable, "content_unavailable");
  assert.equal(kase.production.output, "Production answer.", "production output is still read");
});

test("content_truncated when the source says the content was cut", () => {
  assert.equal(prepareCase(captured({ messages: [user("x")] }, { truncated: true })).notReplayable, "content_truncated");
});

test("tool_history for every spelling of a tool turn", () => {
  const histories: unknown[] = [
    { messages: [user("x"), { role: "assistant", tool_calls: [{ id: "1" }] }, { role: "tool", content: "r" }, user("y")] },
    { messages: [{ role: "function", content: "r" }, user("y")] },
    { messages: [{ role: "assistant", function_call: { name: "f" } }, user("y")] },
    { messages: [user([{ type: "tool_result", tool_use_id: "1", content: "r" }])] },
    { messages: [{ role: "assistant", content: [{ type: "tool_use", id: "1", name: "f", input: {} }] }, user("y")] },
    { input: [{ type: "function_call", name: "f", arguments: "{}" }, { type: "function_call_output", output: "r" }] },
    { contents: [{ role: "model", parts: [{ functionCall: { name: "f", args: {} } }] }, { role: "user", parts: [{ text: "y" }] }] },
    { contents: [{ role: "user", parts: [{ functionResponse: { name: "f", response: {} } }] }] },
    { messages: [{ role: "assistant", parts: [{ type: "tool_call", id: "1", name: "f", arguments: {} }] }, user("y")] },
    { messages: [{ role: "tool", parts: [{ type: "tool_call_response", id: "1", response: "r" }] }, user("y")] },
  ];
  for (const request of histories) {
    assert.equal(prepareCase(captured(request)).notReplayable, "tool_history", JSON.stringify(request));
  }
});

test("unsupported_content for non-text parts", () => {
  const requests: unknown[] = [
    { messages: [user([{ type: "image_url", image_url: { url: "https://example.com/a.png" } }])] },
    { messages: [user([{ type: "image", source: { type: "base64", data: "AAAA" } }])] },
    { input: [{ role: "user", content: [{ type: "input_image", image_url: "https://example.com/a.png" }] }] },
    { contents: [{ role: "user", parts: [{ inlineData: { mimeType: "image/png", data: "AAAA" } }] }] },
    { messages: [{ role: "user", parts: [{ type: "blob", modality: "image", content: "AAAA" }] }] },
  ];
  for (const request of requests) {
    assert.equal(prepareCase(captured(request)).notReplayable, "unsupported_content", JSON.stringify(request));
  }
});

test("no_messages when nothing but a system prompt or blanks", () => {
  assert.equal(prepareCase(captured({ messages: [{ role: "system", content: "S" }] })).notReplayable, "no_messages");
  assert.equal(prepareCase(captured({ messages: [] })).notReplayable, "no_messages");
  assert.equal(prepareCase(captured("   ")).notReplayable, "no_messages");
  assert.equal(prepareCase(captured({ model: "m" })).notReplayable, "no_messages");
});

test("last_turn_not_user when the conversation ends with the assistant", () => {
  assert.equal(
    prepareCase(captured({ messages: [user("x"), { role: "assistant", content: "prefill" }] })).notReplayable,
    "last_turn_not_user",
  );
});

test("input_too_large over 64 KiB of system plus messages", () => {
  const half = "a".repeat(33 * 1024);
  assert.equal(prepareCase(captured({ system: half, messages: [user(half)] })).notReplayable, "input_too_large");
  assert.equal(prepareCase(captured({ messages: [user("a".repeat(60 * 1024))] })).notReplayable, null);
});

test("unparseable_request for shapes the parser cannot read", () => {
  const requests: unknown[] = [
    42,
    { messages: "not a list but messages key" },
    { messages: [42] },
    { messages: [{ role: "critic", content: "x" }] },
    { messages: [user({ nested: true })] },
    { contents: [{ role: "user", parts: "x" }] },
  ];
  for (const request of requests) {
    assert.equal(prepareCase(captured(request)).notReplayable, "unparseable_request", JSON.stringify(request));
  }
});

// ---------------------------------------------------------------------------
// Tools

const weather = {
  type: "function",
  function: { name: "get_weather", description: "Weather", parameters: { type: "object", properties: {} } },
};

test("forced tool: the four spellings", () => {
  const schema = { type: "object", properties: { city: { type: "string" } } };
  const forms: unknown[] = [
    { tools: [weather], tool_choice: "required" },
    { tools: [weather], tool_choice: { type: "function", function: { name: "get_weather" } } },
    { tools: [{ type: "function", name: "get_weather", parameters: schema }], tool_choice: { type: "function", name: "get_weather" } },
    { tools: [{ name: "get_weather", input_schema: schema }], tool_choice: { type: "tool", name: "get_weather" } },
    { tools: [{ name: "get_weather", input_schema: schema }], tool_choice: { type: "any" } },
  ];
  for (const form of forms) {
    const kase = prepareCase(captured({ messages: [user("Weather in Paris?")], ...(form as object) }));
    assert.equal(kase.notReplayable, null, JSON.stringify(form));
    assert.equal(kase.forcedTool?.name, "get_weather");
    assert.ok(!kase.missing.includes(TOOLS_NOT_OFFERED));
  }
});

test("Gemini forced function: one declaration with mode ANY", () => {
  const kase = prepareCase(
    captured({
      contents: [{ role: "user", parts: [{ text: "Extract." }] }],
      tools: [{ functionDeclarations: [{ name: "extract", parameters: { type: "object" } }] }],
      toolConfig: { functionCallingConfig: { mode: "ANY" } },
    }),
  );
  assert.equal(kase.notReplayable, null);
  assert.deepEqual(kase.forcedTool, { name: "extract", description: null, parameters: { type: "object" } });
});

test("tool_definitions_not_offered when a forced choice sits beside other tools", () => {
  const other = { type: "function", function: { name: "lookup", parameters: { type: "object" } } };
  const requests: unknown[] = [
    { tools: [weather, other], tool_choice: "required" },
    { tools: [weather, other], tool_choice: { type: "function", function: { name: "get_weather" } } },
    { tools: [weather], tool_choice: { type: "function", function: { name: "lookup" } } },
    { tools: [{ name: "a", input_schema: {} }, { name: "b", input_schema: {} }], tool_choice: { type: "any" } },
  ];
  for (const request of requests) {
    const kase = prepareCase(captured({ messages: [user("x")], ...(request as object) }));
    assert.equal(kase.notReplayable, "tool_definitions_not_offered", JSON.stringify(request));
    assert.equal(kase.forcedTool, null);
  }
});

test("tools the model was free to skip: replayable, marked in missing", () => {
  const kase = prepareCase(captured({ messages: [user("x")], tools: [weather], tool_choice: "auto" }));
  assert.equal(kase.notReplayable, null);
  assert.equal(kase.forcedTool, null);
  assert.equal(kase.fidelity, "partial");
  assert.ok(kase.missing.includes(TOOLS_NOT_OFFERED));
});

test("production output from a single tool call's arguments", () => {
  const kase = prepareCase(
    captured(
      { messages: [user("x")], tools: [weather], tool_choice: "required" },
      {
        response: response({
          text: null,
          toolCalls: [{ id: "c1", type: "function", function: { name: "get_weather", arguments: '{"city":"Paris"}' } }],
        }),
      },
    ),
  );
  assert.equal(kase.production.output, '{"city":"Paris"}');
  assert.equal(singleToolCallOutput([{ type: "tool_use", name: "f", input: { a: 1 } }]), '{"a":1}');
  assert.equal(singleToolCallOutput([{ name: "f", args: { b: 2 } }]), '{"b":2}');
  assert.equal(singleToolCallOutput([{ arguments: "```json\n{\"c\":3}\n```" }]), "```json\n{\"c\":3}\n```");
  assert.equal(singleToolCallOutput([{ arguments: "[1,2]" }]), null, "not an object");
  assert.equal(singleToolCallOutput([{ arguments: "{}" }, { arguments: "{}" }]), null, "two calls");
});

// ---------------------------------------------------------------------------
// Web search

test("web search: flagged and not replayed unless allowed", () => {
  const searches: unknown[] = [
    { messages: [user("news?")], web_search_options: { search_context_size: "low" } },
    { input: "news?", tools: [{ type: "web_search_preview" }] },
    { messages: [user("news?")], tools: [{ type: "web_search_20250305", name: "web_search" }] },
    { contents: "news?", tools: [{ googleSearch: {} }] },
    { contents: "news?", config: { tools: [{ google_search: {} }] } },
  ];
  for (const request of searches) {
    const blocked = prepareCase(captured(request));
    assert.equal(blocked.webSearch, true, JSON.stringify(request));
    assert.equal(blocked.notReplayable, "web_search_not_replayed");
    const allowed = prepareCase(captured(request), { allowNoSearch: true });
    assert.equal(allowed.webSearch, true);
    assert.equal(allowed.notReplayable, null, JSON.stringify(request));
    assert.ok(!allowed.missing.includes(TOOLS_NOT_OFFERED), "a search tool is not an offered tool");
  }
  assert.equal(prepareCase(captured({ messages: [user("x")] })).webSearch, false);
});

test("missing fields from the adapter grade the case partial", () => {
  const kase = prepareCase(captured({ messages: [user("x")] }, { missing: ["tools"] }));
  assert.equal(kase.fidelity, "partial");
  assert.deepEqual(kase.missing, ["tools"]);
});

test("preview collapses whitespace and caps at 280 characters", () => {
  const kase = prepareCase(captured({ messages: [user(`  a\n\n b  ${"c".repeat(400)}`)] }));
  assert.equal(kase.inputPreview?.startsWith("a b c"), true);
  assert.equal(kase.inputPreview?.length, 280);
});
