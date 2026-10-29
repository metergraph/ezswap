// Shared test helpers for the runner and the CLI: synthetic trace files and
// fake providers. Not a test file itself.

import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { Provider, ReplayCall, ReplyOutcome } from "../src/types.ts";

export function tempDir(): string {
  return mkdtempSync(join(tmpdir(), "replay-test-"));
}

export interface TraceOptions {
  traceId?: string | null;
  ts?: string | null;
  truncated?: boolean;
  search?: boolean;
  model?: string;
}

export function traceLine(n: number, options: TraceOptions = {}): string {
  const request: Record<string, unknown> = {
    model: options.model ?? "gpt-4.1-mini",
    system: "Label the ticket for example.com as JSON.",
    messages: [{ role: "user", parts: [{ type: "text", content: `Ticket ${n}: the dashboard is slow.` }] }],
    response_format: { type: "json_object" },
    params: { temperature: 0.2, max_tokens: 200 },
  };
  if (options.search) request["web_search_options"] = {};
  return JSON.stringify({
    schema: "metergraph.replay-trace/v1",
    id: `call-${n}`,
    trace_id: options.traceId === undefined ? `trace-${n}` : options.traceId,
    provider: "openai",
    request,
    response: {
      text: JSON.stringify({ category: "bug", n }),
      finish_reason: "stop",
      usage: { input_tokens: 40, output_tokens: 12 },
      latency_ms: 800,
    },
    meta: {
      ts: options.ts === undefined ? new Date(Date.UTC(2026, 9, 1, 0, n)).toISOString() : options.ts,
      truncated: options.truncated ?? false,
      missing: [],
    },
  });
}

/** Write a trace file of `count` calls; `options(n)` varies each one. */
export function writeTraces(dir: string, count: number, options: (n: number) => TraceOptions = () => ({})): string {
  const path = join(dir, "traces.jsonl");
  const lines: string[] = [];
  for (let n = 1; n <= count; n++) lines.push(traceLine(n, options(n)));
  writeFileSync(path, `${lines.join("\n")}\n`);
  return path;
}

export function okOutcome(overrides: Partial<ReplyOutcome> = {}): ReplyOutcome {
  return {
    status: "ok",
    output: '{"category":"bug"}',
    finishReason: "stop",
    refusal: false,
    usage: { inputTokens: 50, outputTokens: 10, cachedInputTokens: 0, reasoningTokens: null },
    reportedCostUsd: null,
    latencyMs: 100,
    flags: [],
    error: null,
    ...overrides,
  };
}

export function failedOutcome(maybeBilled: boolean): ReplyOutcome {
  return {
    status: "failed",
    output: null,
    finishReason: null,
    refusal: false,
    usage: null,
    reportedCostUsd: null,
    latencyMs: null,
    flags: maybeBilled ? ["maybe_billed"] : [],
    error: { code: maybeBilled ? "provider_timeout" : "provider_refused", message: "fake failure" },
  };
}

export interface FakeProvider extends Provider {
  calls: ReplayCall[];
}

export function fakeProvider(
  respond: (call: ReplayCall, index: number) => ReplyOutcome | Promise<ReplyOutcome> = () => okOutcome(),
): FakeProvider {
  const calls: ReplayCall[] = [];
  return {
    name: "fake",
    calls,
    async complete(call) {
      const index = calls.push(call) - 1;
      await new Promise((resolve) => setImmediate(resolve));
      return respond(call, index);
    },
  };
}

/** An OpenAI chat completion body. */
export function chatReply(content: string, extra: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    choices: [{ message: { role: "assistant", content }, finish_reason: "stop" }],
    usage: { prompt_tokens: 50, completion_tokens: 10, ...extra },
  };
}
