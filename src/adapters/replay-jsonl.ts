// Metergraph Replay JSONL: the documented trace format, one call per line.
// See docs/trace-format.md.

import type { CapturedCall } from "../types.ts";
import {
  addMissing,
  call,
  firstNum,
  firstStr,
  hashId,
  isObject,
  isoTime,
  normalizeProvider,
  usageOf,
  type Adapter,
  type Json,
  type RecordContext,
} from "./common.ts";
import { capturedResponse, otelOutput, type ParsedResponse } from "./response.ts";

export const REPLAY_TRACE_SCHEMA = "metergraph.replay-trace/v1";

function detect(record: unknown): boolean {
  if (!isObject(record)) return false;
  if (record["schema"] === REPLAY_TRACE_SCHEMA) return true;
  const request = record["request"];
  return (
    isObject(record["meta"]) &&
    isObject(request) &&
    ("messages" in request || "raw" in request || "params" in request)
  );
}

function parseResponse(response: Json): ParsedResponse {
  const parsed: ParsedResponse = Array.isArray(response["messages"])
    ? otelOutput(response["messages"])
    : { text: null, toolCalls: null, finishReason: null, refusal: false, usage: null, shape: null };
  if (typeof response["text"] === "string") parsed.text = response["text"];
  if (Array.isArray(response["tool_calls"]) && response["tool_calls"].length > 0) {
    parsed.toolCalls = response["tool_calls"];
  }
  const finish = firstStr(response["finish_reason"]);
  if (finish !== null) parsed.finishReason = finish;
  if (typeof response["refusal"] === "boolean") parsed.refusal = response["refusal"];
  const usage = isObject(response["usage"]) ? response["usage"] : {};
  parsed.usage = usageOf({
    inputTokens: firstNum(usage["input_tokens"]),
    outputTokens: firstNum(usage["output_tokens"]),
    cachedInputTokens: firstNum(usage["cached_input_tokens"]),
    reasoningTokens: firstNum(usage["reasoning_tokens"]),
  });
  return parsed;
}

/** The body cases parses: the normalized request, or the verbatim one when only it is present. */
function requestBody(request: Json): unknown {
  if (!("messages" in request) && request["raw"] !== undefined && request["raw"] !== null) {
    return request["raw"];
  }
  const { raw: _raw, ...normalized } = request;
  return normalized;
}

function read(record: unknown, context: RecordContext): CapturedCall[] {
  if (!isObject(record)) return [];
  const request = isObject(record["request"]) ? record["request"] : null;
  const response = isObject(record["response"]) ? record["response"] : {};
  const meta = isObject(record["meta"]) ? record["meta"] : {};
  const missing = Array.isArray(meta["missing"])
    ? meta["missing"].filter((name): name is string => typeof name === "string")
    : [];
  if (request === null) addMissing(missing, "request");
  if (!isObject(record["response"])) addMissing(missing, "response");
  const raw = request && isObject(request["raw"]) ? request["raw"] : {};
  return [
    call({
      id: firstStr(record["id"]) ?? hashId(context.raw),
      traceId: firstStr(record["trace_id"], meta["trace_id"]),
      ts: isoTime(meta["ts"] ?? record["ts"]),
      source: "replay-jsonl",
      provider: normalizeProvider(record["provider"]),
      model: firstStr(request?.["model"], raw["model"]),
      request: request === null ? null : requestBody(request),
      response: capturedResponse(parseResponse(response), {
        costUsd: firstNum(response["cost_usd"]),
        latencyMs: firstNum(response["latency_ms"]),
      }),
      truncated: meta["truncated"] === true,
      missing,
    }),
  ];
}

export const replayJsonl: Adapter = { name: "replay-jsonl", detect, read };
