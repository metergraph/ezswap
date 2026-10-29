// Raw provider request and response pairs (OpenAI chat or Responses,
// Anthropic Messages, Gemini), and two logs that reduce to them with a field
// map: Helicone request exports and LiteLLM StandardLoggingPayload callback
// logs.

import type { CapturedCall } from "../types.ts";
import {
  addMissing,
  call,
  durationMs,
  firstNum,
  firstStr,
  hashId,
  isObject,
  isoTime,
  normalizeProvider,
  parseJson,
  usageOf,
  type Adapter,
  type Json,
  type RecordContext,
} from "./common.ts";
import { capturedResponse, parseResponseBody, providerFromShape } from "./response.ts";

/** Helicone's stand-in body when an export ran without `includeBody`. */
function heliconePlaceholder(body: unknown): boolean {
  return body === undefined || body === null || body === "" || (isObject(body) && "helicone_message" in body);
}

/** LiteLLM writes this string in place of redacted content. */
const LITELLM_REDACTED = "redacted-by-litellm";

function isHelicone(record: Json): boolean {
  return "request_body" in record || ("request_id" in record && "response_status" in record) || "signed_body_url" in record;
}

function isLitellm(record: Json): boolean {
  return (
    "model_parameters" in record &&
    ("call_type" in record || "custom_llm_provider" in record || "response_cost" in record)
  );
}

function detect(record: unknown): boolean {
  if (!isObject(record)) return false;
  return isHelicone(record) || isLitellm(record) || ("request" in record && "response" in record);
}

function helicone(record: Json, context: RecordContext): CapturedCall {
  const missing: string[] = [];
  const requestBody = parseJson(record["request_body"]);
  const responseBody = parseJson(record["response_body"]);
  const request = heliconePlaceholder(requestBody) ? null : requestBody;
  if (request === null) addMissing(missing, "request_body");
  const responseMissing = heliconePlaceholder(responseBody);
  if (responseMissing) addMissing(missing, "response_body");
  const parsed = parseResponseBody(responseMissing ? null : responseBody);
  const properties = isObject(record["request_properties"])
    ? record["request_properties"]
    : isObject(record["properties"])
      ? record["properties"]
      : {};
  return call({
    id: firstStr(record["request_id"], record["response_id"]) ?? hashId(context.raw),
    traceId: firstStr(properties["Helicone-Session-Id"]),
    ts: isoTime(record["request_created_at"]),
    source: "raw",
    provider: normalizeProvider(record["provider"]) ?? providerFromShape(request, parsed),
    model: firstStr(record["model"], record["request_model"], record["response_model"], isObject(request) ? request["model"] : null),
    request,
    response: capturedResponse(parsed, {
      usage: usageOf({
        inputTokens: firstNum(record["prompt_tokens"]),
        outputTokens: firstNum(record["completion_tokens"]),
        cachedInputTokens: firstNum(record["prompt_cache_read_tokens"]),
        reasoningTokens: firstNum(record["reasoning_tokens"]),
      }),
      costUsd: firstNum(record["cost"], record["costUSD"]),
      latencyMs: firstNum(record["delay_ms"], record["latency"]),
    }),
    missing,
  });
}

function litellm(record: Json, context: RecordContext): CapturedCall {
  const missing: string[] = [];
  const messages = parseJson(record["messages"]);
  const redacted =
    messages === undefined ||
    messages === null ||
    messages === LITELLM_REDACTED ||
    (Array.isArray(messages) &&
      messages.some((m) => isObject(m) && m["content"] === LITELLM_REDACTED));
  const parameters = isObject(record["model_parameters"]) ? record["model_parameters"] : {};
  let request: unknown = null;
  if (!redacted) {
    // model_parameters is the request minus its content (messages, prompt, input, system).
    request = typeof messages === "string"
      ? { model: record["model"], prompt: messages, ...parameters }
      : isObject(messages)
        ? { model: record["model"], ...parameters, ...messages }
        : { model: record["model"], ...parameters, messages };
  } else {
    addMissing(missing, "messages");
  }
  const responseBody = parseJson(record["response"]);
  const responseRedacted = responseBody === LITELLM_REDACTED || responseBody === null || responseBody === undefined;
  if (responseRedacted) addMissing(missing, "response");
  const parsed = parseResponseBody(responseRedacted ? null : responseBody);
  const responseSeconds = firstNum(record["response_time"]);
  const latency = durationMs(record["startTime"], record["endTime"]) ??
    (responseSeconds !== null ? responseSeconds * 1000 : null);
  return call({
    id: firstStr(record["id"], record["litellm_call_id"]) ?? hashId(context.raw),
    traceId: firstStr(record["trace_id"]),
    ts: isoTime(record["startTime"]),
    source: "raw",
    provider: normalizeProvider(record["custom_llm_provider"]) ?? providerFromShape(request, parsed),
    model: firstStr(record["model"]),
    request,
    response: capturedResponse(parsed, {
      usage: usageOf({
        inputTokens: firstNum(record["prompt_tokens"]),
        outputTokens: firstNum(record["completion_tokens"]),
      }),
      costUsd: firstNum(record["response_cost"]),
      latencyMs: latency,
    }),
    missing,
  });
}

function generic(record: Json, context: RecordContext): CapturedCall {
  const missing: string[] = [];
  const request = parseJson(record["request"]) ?? null;
  if (request === null) addMissing(missing, "request");
  const response = parseJson(record["response"]);
  if (response === null || response === undefined) addMissing(missing, "response");
  const parsed = parseResponseBody(response);
  const responseObject = isObject(response) ? response : {};
  return call({
    id: firstStr(record["id"], responseObject["id"]) ?? hashId(context.raw),
    traceId: firstStr(record["trace_id"], record["traceId"]),
    ts: isoTime(record["ts"] ?? record["timestamp"] ?? record["created_at"] ?? responseObject["created"] ?? responseObject["created_at"]),
    source: "raw",
    provider: normalizeProvider(record["provider"]) ?? providerFromShape(request, parsed),
    model: firstStr(isObject(request) ? request["model"] : null, responseObject["model"], record["model"]),
    request,
    response: capturedResponse(parsed, {
      costUsd: firstNum(record["cost_usd"], record["cost"]),
      latencyMs: firstNum(record["latency_ms"]) ?? durationMs(record["start_time"], record["end_time"]),
    }),
    truncated: record["truncated"] === true,
    missing,
  });
}

function read(record: unknown, context: RecordContext): CapturedCall[] {
  if (!isObject(record)) return [];
  if (isLitellm(record)) return [litellm(record, context)];
  if (isHelicone(record)) return [helicone(record, context)];
  return [generic(record, context)];
}

export const raw: Adapter = { name: "raw", detect, read };
