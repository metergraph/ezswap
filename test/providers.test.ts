import { test } from "node:test";
import assert from "node:assert/strict";
import type { ReplayCall } from "../src/types.ts";
import {
  MAX_RESPONSE_BYTES,
  PRESETS,
  ProviderConfigError,
  buildPayload,
  createLimiter,
  createProvider,
  forcedToolRequest,
  isCutOff,
  isFreeEndpoint,
  isRefusalReason,
  type FetchLike,
  type ProviderOptions,
} from "../src/providers/index.ts";
import { neverSent, retryAfterMs } from "../src/providers/transport.ts";

const KEY = "sk-test-not-a-real-key-0000";

function call(overrides: Partial<ReplayCall> = {}): ReplayCall {
  return {
    model: "openai/gpt-6-luna",
    system: "Be brief.",
    messages: [
      { role: "user", content: "Hi" },
      { role: "assistant", content: "Hello" },
      { role: "user", content: "Capital of France?" },
    ],
    maxTokens: 256,
    temperature: null,
    topP: null,
    stop: null,
    reasoningEffort: null,
    responseFormat: null,
    forcedTool: null,
    ...overrides,
  };
}

function ok(body: unknown = reply("Paris"), init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" }, ...init });
}

function status(code: number, body = "", headers: Record<string, string> = {}): Response {
  return new Response(body, { status: code, headers });
}

function reply(content: string | null, extra: Record<string, unknown> = {}, finish = "stop") {
  return {
    choices: [{ message: { role: "assistant", content, ...extra }, finish_reason: finish }],
    usage: {
      prompt_tokens: 120,
      completion_tokens: 30,
      prompt_tokens_details: { cached_tokens: 100 },
      completion_tokens_details: { reasoning_tokens: 12 },
    },
  };
}

interface Sent {
  url: string;
  headers: Record<string, string>;
  body: Record<string, unknown>;
}

/** A fake fetch answering from a script, recording every request. */
function script(...answers: Array<Response | Error | ((sent: Sent) => Response | Error)>) {
  const sent: Sent[] = [];
  const fetch: FetchLike = async (url, init) => {
    const entry: Sent = {
      url,
      headers: init.headers as Record<string, string>,
      body: JSON.parse(String(init.body)) as Record<string, unknown>,
    };
    sent.push(entry);
    const next = answers.shift();
    if (next === undefined) throw new Error("unexpected request");
    const answer = typeof next === "function" ? next(entry) : next;
    if (answer instanceof Error) throw answer;
    return answer;
  };
  return { fetch, sent };
}

function provider(fetch: FetchLike, options: Partial<ProviderOptions> = {}) {
  const sleeps: number[] = [];
  let clock = 0;
  const instance = createProvider({
    preset: "vercel",
    env: { AI_GATEWAY_API_KEY: KEY, OPENAI_API_KEY: KEY, OPENROUTER_API_KEY: KEY, LITELLM_API_KEY: KEY, LITELLM_BASE_URL: "https://litellm.example.com/" },
    fetch,
    sleep: async (ms) => {
      sleeps.push(ms);
      clock += ms;
    },
    now: () => (clock += 10),
    ...options,
  });
  return { instance, sleeps };
}

function connectionError(code: string): Error {
  return Object.assign(new TypeError("fetch failed"), { cause: Object.assign(new Error(code), { code }) });
}

// ---------------------------------------------------------------------------
// Presets and configuration

test("presets name their endpoint, key variable and model handling", () => {
  assert.equal(PRESETS.openai.baseUrl, "https://api.openai.com/v1");
  assert.equal(PRESETS.openrouter.apiKeyEnv, "OPENROUTER_API_KEY");
  assert.equal(PRESETS.vercel.baseUrl, "https://ai-gateway.vercel.sh/v1");
  assert.equal(PRESETS.litellm.baseUrlEnv, "LITELLM_BASE_URL");
  assert.equal(PRESETS.ollama.apiKeyEnv, null);
  assert.equal(isFreeEndpoint("ollama"), true);
  assert.equal(isFreeEndpoint("vercel"), false);
  assert.equal(isFreeEndpoint("localhost:11434"), false);
});

test("the openai preset strips openai/ and sends max_completion_tokens with the key", async () => {
  const { fetch, sent } = script(ok());
  const { instance } = provider(fetch, { preset: "openai" });
  const outcome = await instance.complete(call());
  assert.equal(outcome.status, "ok");
  assert.equal(sent[0]!.url, "https://api.openai.com/v1/chat/completions");
  assert.equal(sent[0]!.body["model"], "gpt-6-luna");
  assert.equal(sent[0]!.body["max_completion_tokens"], 256);
  assert.equal(sent[0]!.body["max_tokens"], undefined);
  assert.equal(sent[0]!.headers["authorization"], `Bearer ${KEY}`);
  assert.equal(instance.name, "openai");
});

test("gateway presets keep creator/model and send max_tokens", async () => {
  for (const preset of ["vercel", "openrouter", "litellm"] as const) {
    const { fetch, sent } = script(ok());
    const { instance } = provider(fetch, { preset });
    await instance.complete(call());
    assert.equal(sent[0]!.body["model"], "openai/gpt-6-luna", preset);
    assert.equal(sent[0]!.body["max_tokens"], 256, preset);
  }
});

test("openrouter asks for the charged amount; litellm reads its base URL from the environment", async () => {
  const openrouter = script(ok());
  await provider(openrouter.fetch, { preset: "openrouter" }).instance.complete(call());
  assert.deepEqual(openrouter.sent[0]!.body["usage"], { include: true });
  assert.equal(openrouter.sent[0]!.url, "https://openrouter.ai/api/v1/chat/completions");
  const litellm = script(ok());
  const { instance } = provider(litellm.fetch, { preset: "litellm" });
  await instance.complete(call());
  assert.equal(litellm.sent[0]!.url, "https://litellm.example.com/chat/completions");
  assert.equal(instance.baseUrl, "https://litellm.example.com");
});

test("ollama needs no key, is free, and sends no authorization", async () => {
  const { fetch, sent } = script(ok());
  const instance = createProvider({ preset: "ollama", env: {}, fetch });
  await instance.complete(call({ model: "llama3.2" }));
  assert.equal(sent[0]!.url, "http://localhost:11434/v1/chat/completions");
  assert.equal(sent[0]!.headers["authorization"], undefined);
  assert.equal(instance.free, true);
});

test("a custom endpoint takes a base URL and key variable, named by its host", async () => {
  const { fetch, sent } = script(ok());
  const instance = createProvider({ baseUrl: "https://llm.example.com/v1/", apiKeyEnv: "EXAMPLE_KEY", env: { EXAMPLE_KEY: KEY }, fetch });
  await instance.complete(call());
  assert.equal(sent[0]!.url, "https://llm.example.com/v1/chat/completions");
  assert.equal(instance.name, "llm.example.com");
  assert.equal(instance.free, false);
  const keyless = createProvider({ baseUrl: "http://127.0.0.1:8000/v1", env: {}, fetch: script(ok()).fetch, free: true });
  assert.equal(keyless.free, true);
});

test("a missing key or base URL fails at creation, naming the variable only", () => {
  assert.throws(
    () => createProvider({ preset: "openai", env: {} }),
    (error: unknown) => error instanceof ProviderConfigError && error.code === "credential_missing" && error.message === "OPENAI_API_KEY is not set",
  );
  assert.throws(() => createProvider({ preset: "litellm", env: { LITELLM_API_KEY: KEY } }), /LITELLM_BASE_URL is not set/);
  assert.throws(() => createProvider({ env: {} }), /needs a base URL/);
  assert.throws(() => createProvider({ baseUrl: "not a url", env: {} }), /not a valid URL/);
  assert.throws(() => createProvider({ baseUrl: "https://user:secret@example.com/v1", env: {} }), /environment variable/);
});

// ---------------------------------------------------------------------------
// The request

test("the payload puts the system first and carries every set field", () => {
  const body = buildPayload(
    call({
      temperature: 0.3,
      topP: 0.9,
      stop: ["END"],
      reasoningEffort: "high",
      responseFormat: { type: "json_object" },
      forcedTool: { name: "record", description: "Record it", parameters: { type: "object" } },
    }),
    { temperature: true, responseFormat: true },
  );
  assert.deepEqual(body, {
    model: "openai/gpt-6-luna",
    messages: [
      { role: "system", content: "Be brief." },
      { role: "user", content: "Hi" },
      { role: "assistant", content: "Hello" },
      { role: "user", content: "Capital of France?" },
    ],
    max_tokens: 256,
    temperature: 0.3,
    top_p: 0.9,
    stop: ["END"],
    reasoning_effort: "high",
    response_format: { type: "json_object" },
    tools: [{ type: "function", function: { name: "record", parameters: { type: "object" }, strict: false, description: "Record it" } }],
    tool_choice: { type: "function", function: { name: "record" } },
  });
});

test("the payload leaves out what is unset or dropped", () => {
  const body = buildPayload(call({ system: null, temperature: 0.3, stop: [], responseFormat: { type: "json_object" } }), {
    temperature: false,
    responseFormat: false,
    extra: { model: "ignored", usage: { include: true } },
  });
  assert.deepEqual(Object.keys(body), ["model", "messages", "max_tokens", "usage"]);
  assert.equal(body["model"], "openai/gpt-6-luna");
  assert.equal((body["messages"] as unknown[]).length, 3);
});

test("a forced tool without a description has none", () => {
  const forced = forcedToolRequest({ name: "f", description: null, parameters: {} });
  assert.deepEqual(Object.keys(forced.tool["function"] as object), ["name", "parameters", "strict"]);
});

// ---------------------------------------------------------------------------
// The reply

test("usage, reasoning, cache and reported cost are normalized", async () => {
  const body = reply("Paris");
  (body.usage as Record<string, unknown>)["cost"] = 0.00042;
  const { instance } = provider(script(ok(body)).fetch);
  const outcome = await instance.complete(call());
  assert.deepEqual(outcome, {
    status: "ok",
    output: "Paris",
    finishReason: "stop",
    refusal: false,
    usage: { inputTokens: 120, outputTokens: 30, cachedInputTokens: 100, reasoningTokens: 12 },
    reportedCostUsd: 0.00042,
    latencyMs: 10,
    flags: [],
    error: null,
  });
});

test("missing or odd usage reads as null fields; a negative or non-number cost is ignored", async () => {
  const { instance } = provider(
    script(
      ok({ choices: [{ message: { content: "x" }, finish_reason: "stop" }] }),
      ok({ choices: [{ message: { content: "x" } }], usage: { prompt_tokens: -1, completion_tokens: 2.5, cost: -1 } }),
      ok({ choices: [{ message: { content: "x" } }], usage: { prompt_tokens: 5, cost: "0.1" } }),
    ).fetch,
  );
  const none = await instance.complete(call());
  assert.equal(none.usage, null);
  assert.equal(none.reportedCostUsd, null);
  const odd = await instance.complete(call());
  assert.deepEqual(odd.usage, { inputTokens: null, outputTokens: null, cachedInputTokens: null, reasoningTokens: null });
  assert.equal(odd.reportedCostUsd, null);
  assert.equal(odd.finishReason, null);
  assert.equal((await instance.complete(call())).reportedCostUsd, null);
});

test("an empty text with a tool call answers with the first call's arguments", async () => {
  const toolCall = (args: unknown) => ({ tool_calls: [{ type: "function", function: { name: "record", arguments: args } }] });
  const { instance } = provider(
    script(
      ok(reply(null, toolCall('{"priority": 2}'), "tool_calls")),
      ok(reply("  ", toolCall({ priority: 3 }), "tool_calls")),
      ok(reply("text wins", toolCall("{}"), "tool_calls")),
      ok(reply(null, { tool_calls: [{ function: { arguments: 7 } }] })),
    ).fetch,
  );
  assert.equal((await instance.complete(call())).output, '{"priority": 2}');
  assert.equal((await instance.complete(call())).output, '{"priority":3}');
  assert.equal((await instance.complete(call())).output, "text wins");
  assert.equal((await instance.complete(call())).output, null);
});

test("refusals: a refusal field or a refusing finish reason", async () => {
  const { instance } = provider(
    script(
      ok(reply(null, { refusal: "I can't help with that." })),
      ok(reply("", {}, "content_filter")),
      ok(reply("", {}, "safety")),
      ok(reply("", {}, "refusal")),
      ok(reply("fine", { refusal: null })),
    ).fetch,
  );
  for (let i = 0; i < 4; i += 1) assert.equal((await instance.complete(call())).refusal, true, String(i));
  assert.equal((await instance.complete(call())).refusal, false);
});

test("cut-off and refusal finish reasons", () => {
  for (const reason of ["length", "max_tokens", "max_output_tokens", "LENGTH"]) assert.equal(isCutOff(reason), true);
  for (const reason of ["stop", "tool_calls", null]) assert.equal(isCutOff(reason), false);
  for (const reason of ["content_filter", "refusal", "safety"]) assert.equal(isRefusalReason(reason), true);
  assert.equal(isRefusalReason("stop"), false);
  assert.equal(isRefusalReason(null), false);
});

test("a 200 carrying an error and no choice is a failure that may have billed", async () => {
  const { instance } = provider(script(ok({ error: { message: "upstream failed" } })).fetch);
  const outcome = await instance.complete(call());
  assert.equal(outcome.status, "failed");
  assert.equal(outcome.error?.code, "provider_error");
  assert.deepEqual(outcome.flags, ["maybe_billed"]);
});

// ---------------------------------------------------------------------------
// Retries

test("429 is retried with a backoff and flagged; the latency is the answering request's", async () => {
  const { fetch, sent } = script(status(429), status(429), ok());
  const { instance, sleeps } = provider(fetch);
  const outcome = await instance.complete(call());
  assert.equal(outcome.status, "ok");
  assert.equal(sent.length, 3);
  assert.deepEqual(sleeps, [2000, 4000]);
  assert.deepEqual(outcome.flags, ["retried"]);
  assert.equal(outcome.latencyMs, 10);
});

test("Retry-After is honoured, in seconds, as a date, or in milliseconds, and capped", async () => {
  const { fetch } = script(status(429, "", { "retry-after": "7" }), status(529, "", { "retry-after-ms": "1500" }), ok());
  const { instance, sleeps } = provider(fetch);
  assert.equal((await instance.complete(call())).status, "ok");
  assert.deepEqual(sleeps, [7000, 1500]);
  const capped = provider(script(status(429, "", { "retry-after": "3600" }), ok()).fetch);
  await capped.instance.complete(call());
  assert.deepEqual(capped.sleeps, [60_000]);
  const now = Date.UTC(2026, 9, 20, 12, 0, 0);
  assert.equal(retryAfterMs(new Headers({ "retry-after": new Date(now + 5000).toUTCString() }), now), 5000);
  assert.equal(retryAfterMs(new Headers({ "retry-after": new Date(now - 5000).toUTCString() }), now), 0);
  assert.equal(retryAfterMs(new Headers({ "retry-after": "soon" }), now), null);
  assert.equal(retryAfterMs(new Headers(), now), null);
});

test("429 three times fails as rate limited, not billed", async () => {
  const { instance } = provider(script(status(429), status(429), status(429)).fetch);
  const outcome = await instance.complete(call());
  assert.equal(outcome.status, "failed");
  assert.equal(outcome.error?.code, "provider_rate_limited");
  assert.deepEqual(outcome.flags, ["retried"]);
});

test("529 is overloaded: retried, then failed without billing", async () => {
  const { instance } = provider(script(status(529), status(529), status(529)).fetch);
  const outcome = await instance.complete(call());
  assert.equal(outcome.error?.code, "provider_overloaded");
  assert.equal(outcome.flags.includes("maybe_billed"), false);
});

test("a refused connection or unresolved name is retried; three fail as unreachable", async () => {
  const recovered = provider(script(connectionError("ECONNREFUSED"), connectionError("ENOTFOUND"), ok()).fetch);
  const outcome = await recovered.instance.complete(call());
  assert.equal(outcome.status, "ok");
  assert.deepEqual(outcome.flags, ["retried"]);
  const failed = provider(script(connectionError("EAI_AGAIN"), connectionError("ECONNREFUSED"), connectionError("ECONNREFUSED")).fetch);
  const down = await failed.instance.complete(call());
  assert.equal(down.error?.code, "provider_unreachable");
  assert.equal(down.flags.includes("maybe_billed"), false);
});

test("never-sent covers grouped connection errors only when every one was refused", () => {
  const grouped = (codes: string[]) =>
    Object.assign(new TypeError("fetch failed"), { cause: { errors: codes.map((code) => ({ code })) } });
  assert.equal(neverSent(grouped(["ECONNREFUSED", "ECONNREFUSED"])), true);
  assert.equal(neverSent(grouped(["ECONNREFUSED", "ETIMEDOUT"])), false);
  assert.equal(neverSent(connectionError("ECONNRESET")), false);
  assert.equal(neverSent(null), false);
});

test("a reset, a timeout or a 5xx fails at once and may have billed", async () => {
  for (const [answer, code] of [
    [connectionError("ECONNRESET"), "provider_unreachable"],
    [Object.assign(new Error("timed out"), { name: "TimeoutError" }), "provider_timeout"],
    [status(500), "provider_unavailable"],
    [status(503), "provider_unavailable"],
  ] as const) {
    const { fetch, sent } = script(answer);
    const outcome = await provider(fetch).instance.complete(call());
    assert.equal(outcome.status, "failed");
    assert.equal(outcome.error?.code, code);
    assert.deepEqual(outcome.flags, ["maybe_billed"]);
    assert.equal(sent.length, 1);
  }
});

test("the request timeout aborts a hung request as a timeout", async () => {
  const hang: FetchLike = (_url, init) =>
    new Promise((_resolve, reject) => {
      init.signal?.addEventListener("abort", () => reject(init.signal?.reason));
    });
  const instance = createProvider({ preset: "ollama", env: {}, fetch: hang, timeoutMs: 20 });
  const outcome = await instance.complete(call());
  assert.equal(outcome.error?.code, "provider_timeout");
  assert.deepEqual(outcome.flags, ["maybe_billed"]);
});

test("a caller's abort cancels the call", async () => {
  const controller = new AbortController();
  controller.abort();
  const { instance } = provider(script().fetch);
  const outcome = await instance.complete(call(), controller.signal);
  assert.equal(outcome.error?.code, "aborted");
  const inflight = new AbortController();
  const hang: FetchLike = (_url, init) =>
    new Promise((_resolve, reject) => {
      init.signal?.addEventListener("abort", () => reject(init.signal?.reason));
      setTimeout(() => inflight.abort(), 5);
    });
  const live = createProvider({ preset: "ollama", env: {}, fetch: hang });
  const cancelled = await live.complete(call(), inflight.signal);
  assert.equal(cancelled.error?.code, "aborted");
  assert.deepEqual(cancelled.flags, ["maybe_billed"]);
});

test("an unreadable, oversized or non-object reply may have billed", async () => {
  const huge = new Response(new Uint8Array(MAX_RESPONSE_BYTES + 1), { status: 200 });
  const cutOff = new Response(
    new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('{"choices":'));
        controller.error(new Error("socket closed"));
      },
    }),
    { status: 200 },
  );
  for (const [answer, code] of [
    [new Response("not json", { status: 200 }), "provider_malformed"],
    [new Response("[1,2]", { status: 200 }), "provider_malformed"],
    [new Response(new Uint8Array([0xff, 0xfe]), { status: 200 }), "provider_malformed"],
    [huge, "provider_malformed"],
    [cutOff, "provider_unreachable"],
  ] as const) {
    const outcome = await provider(script(answer).fetch).instance.complete(call());
    assert.equal(outcome.error?.code, code);
    assert.deepEqual(outcome.flags, ["maybe_billed"]);
  }
});

test("other 4xx are refusals: not retried, not billed, and the body is never echoed", async () => {
  for (const code of [400, 401, 403, 404, 422]) {
    const { fetch, sent } = script(status(code, `{"error":"bad thing with ${KEY}"}`));
    const outcome = await provider(fetch).instance.complete(call());
    assert.equal(outcome.error?.code, "provider_refused");
    assert.equal(outcome.error?.message, `the endpoint answered HTTP ${code}`);
    assert.deepEqual(outcome.flags, []);
    assert.equal(sent.length, 1);
  }
});

// ---------------------------------------------------------------------------
// Fallbacks

test("a 400 naming temperature is resent once without it, and the model is remembered", async () => {
  const { fetch, sent } = script(
    status(400, '{"error":{"message":"Unsupported value: \'temperature\' does not support 0.2"}}'),
    ok(),
    ok(),
  );
  const { instance } = provider(fetch);
  const first = await instance.complete(call({ temperature: 0.2 }));
  assert.equal(first.status, "ok");
  assert.deepEqual(first.flags, ["temperature_dropped"]);
  assert.equal(sent[0]!.body["temperature"], 0.2);
  assert.equal("temperature" in sent[1]!.body, false);
  assert.ok(instance.temperatureRefused.has("openai/gpt-6-luna"));
  const second = await instance.complete(call({ temperature: 0.2 }));
  assert.deepEqual(second.flags, ["temperature_dropped"]);
  assert.equal(sent.length, 3);
  assert.equal("temperature" in sent[2]!.body, false);
});

test("a temperature refusal for a call that sent none is an ordinary refusal", async () => {
  const { instance } = provider(script(status(400, "temperature is weird")).fetch);
  const outcome = await instance.complete(call());
  assert.equal(outcome.error?.code, "provider_refused");
});

test("a temperature refused twice fails as refused", async () => {
  const { instance } = provider(script(status(400, "temperature"), status(400, "temperature")).fetch);
  const outcome = await instance.complete(call({ temperature: 1 }));
  assert.equal(outcome.error?.code, "provider_refused");
  assert.deepEqual(outcome.flags, ["temperature_dropped"]);
});

test("a 4xx naming the response format drops it once and flags the case", async () => {
  for (const detail of ["response_format is not supported", "Invalid json_schema", "This model does not support JSON schema"]) {
    const { fetch, sent } = script(status(400, detail), ok());
    const outcome = await provider(fetch).instance.complete(call({ responseFormat: { type: "json_schema" } }));
    assert.equal(outcome.status, "ok", detail);
    assert.deepEqual(outcome.flags, ["response_format_dropped"]);
    assert.ok("response_format" in sent[0]!.body);
    assert.equal("response_format" in sent[1]!.body, false);
  }
});

test("a 4xx that does not name the response format keeps it and fails", async () => {
  const { fetch, sent } = script(status(400, "context length exceeded"));
  const outcome = await provider(fetch).instance.complete(call({ responseFormat: { type: "json_schema" } }));
  assert.equal(outcome.error?.code, "provider_refused");
  assert.equal(sent.length, 1);
  const auth = script(status(401, "invalid key for response_format"));
  assert.equal((await provider(auth.fetch).instance.complete(call({ responseFormat: { type: "json_object" } }))).error?.code, "provider_refused");
});

test("a response-format refusal on a call without one is an ordinary refusal", async () => {
  const { instance } = provider(script(status(422, "response_format invalid")).fetch);
  assert.equal((await instance.complete(call())).error?.code, "provider_refused");
});

test("both fallbacks compose: temperature first, then the response format", async () => {
  const { fetch, sent } = script(status(400, "temperature unsupported"), status(400, "response_format unsupported"), ok());
  const outcome = await provider(fetch).instance.complete(call({ temperature: 0.5, responseFormat: { type: "json_object" } }));
  assert.equal(outcome.status, "ok");
  assert.deepEqual(outcome.flags.sort(), ["response_format_dropped", "temperature_dropped"]);
  assert.equal(sent.length, 3);
  assert.equal("temperature" in sent[2]!.body, false);
  assert.equal("response_format" in sent[2]!.body, false);
});

// ---------------------------------------------------------------------------
// Keys and concurrency

test("no outcome or error carries the key", async () => {
  const answers = [status(400, KEY), status(500, KEY), connectionError("ECONNRESET"), ok({ error: KEY })];
  for (const answer of answers) {
    const outcome = await provider(script(answer).fetch).instance.complete(call());
    assert.equal(JSON.stringify(outcome).includes(KEY), false);
  }
  try {
    createProvider({ preset: "openai", env: { OPENAI_API_KEY: "   " } });
  } catch (error) {
    assert.equal(String(error).includes("OPENAI_API_KEY"), true);
  }
});

test("the provider runs at most `concurrency` requests at once", async () => {
  let active = 0;
  let peak = 0;
  const fetch: FetchLike = async () => {
    active += 1;
    peak = Math.max(peak, active);
    await new Promise((resolve) => setTimeout(resolve, 5));
    active -= 1;
    return ok();
  };
  const instance = createProvider({ preset: "ollama", env: {}, fetch, concurrency: 2 });
  const outcomes = await Promise.all(Array.from({ length: 7 }, () => instance.complete(call())));
  assert.equal(peak, 2);
  assert.ok(outcomes.every((outcome) => outcome.status === "ok"));
  const defaults = createProvider({ preset: "ollama", env: {}, fetch });
  active = 0;
  peak = 0;
  await Promise.all(Array.from({ length: 10 }, () => defaults.complete(call())));
  assert.equal(peak, 4);
});

test("the limiter releases on failure and refuses a bad limit", async () => {
  const limiter = createLimiter(1);
  await assert.rejects(limiter.run(async () => Promise.reject(new Error("boom"))), /boom/);
  assert.equal(await limiter.run(async () => 5), 5);
  assert.equal(limiter.active, 0);
  assert.equal(limiter.waiting, 0);
  assert.throws(() => createLimiter(0), RangeError);
  assert.throws(() => createLimiter(1.5), RangeError);
});
