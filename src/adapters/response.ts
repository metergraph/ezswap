// Provider response bodies (OpenAI chat and Responses, Anthropic Messages,
// Gemini, OpenTelemetry output messages) to the parts of CapturedResponse
// they carry.

import type { CapturedResponse, Usage } from "../types.ts";
import { emptyResponse, firstNum, isObject, parseJson, str, usageOf, type Json } from "./common.ts";

export interface ParsedResponse {
  text: string | null;
  toolCalls: unknown[] | null;
  finishReason: string | null;
  refusal: boolean;
  usage: Usage | null;
  /** Which shape was recognized, or null when none was. */
  shape: "openai_chat" | "openai_responses" | "anthropic" | "gemini" | "otel" | "text" | null;
}

const REFUSAL_REASONS = new Set(["refusal", "content_filter", "safety"]);

function joined(texts: string[]): string | null {
  return texts.length > 0 ? texts.join("\n") : null;
}

function nonEmpty<T>(list: T[]): T[] | null {
  return list.length > 0 ? list : null;
}

function blockTexts(content: unknown): string[] {
  if (typeof content === "string") return [content];
  if (!Array.isArray(content)) return [];
  const texts: string[] = [];
  for (const block of content) {
    if (typeof block === "string") texts.push(block);
    else if (isObject(block) && typeof block["text"] === "string" && block["type"] !== "refusal") {
      texts.push(block["text"]);
    }
  }
  return texts;
}

function openaiChatUsage(usage: unknown): Usage | null {
  if (!isObject(usage)) return null;
  const prompt = isObject(usage["prompt_tokens_details"]) ? usage["prompt_tokens_details"] : {};
  const completion = isObject(usage["completion_tokens_details"]) ? usage["completion_tokens_details"] : {};
  return usageOf({
    inputTokens: firstNum(usage["prompt_tokens"], usage["input_tokens"]),
    outputTokens: firstNum(usage["completion_tokens"], usage["output_tokens"]),
    cachedInputTokens: firstNum(prompt["cached_tokens"], usage["cache_read_input_tokens"]),
    reasoningTokens: firstNum(completion["reasoning_tokens"]),
  });
}

function responsesUsage(usage: unknown): Usage | null {
  if (!isObject(usage)) return null;
  const input = isObject(usage["input_tokens_details"]) ? usage["input_tokens_details"] : {};
  const output = isObject(usage["output_tokens_details"]) ? usage["output_tokens_details"] : {};
  return usageOf({
    inputTokens: firstNum(usage["input_tokens"]),
    outputTokens: firstNum(usage["output_tokens"]),
    cachedInputTokens: firstNum(input["cached_tokens"]),
    reasoningTokens: firstNum(output["reasoning_tokens"]),
  });
}

function anthropicUsage(usage: unknown): Usage | null {
  if (!isObject(usage)) return null;
  return usageOf({
    inputTokens: firstNum(usage["input_tokens"]),
    outputTokens: firstNum(usage["output_tokens"]),
    cachedInputTokens: firstNum(usage["cache_read_input_tokens"]),
  });
}

function geminiUsage(usage: unknown): Usage | null {
  if (!isObject(usage)) return null;
  return usageOf({
    inputTokens: firstNum(usage["promptTokenCount"]),
    outputTokens: firstNum(usage["candidatesTokenCount"]),
    cachedInputTokens: firstNum(usage["cachedContentTokenCount"]),
    reasoningTokens: firstNum(usage["thoughtsTokenCount"]),
  });
}

function openaiChat(body: Json): ParsedResponse {
  const choices = body["choices"] as unknown[];
  const choice = isObject(choices[0]) ? choices[0] : {};
  const message = isObject(choice["message"]) ? choice["message"] : {};
  const toolCalls = Array.isArray(message["tool_calls"]) ? message["tool_calls"] : [];
  const finishReason = str(choice["finish_reason"]);
  const refusal = Boolean(str(message["refusal"])) || REFUSAL_REASONS.has(finishReason ?? "");
  return {
    text: joined(blockTexts(message["content"])),
    toolCalls: nonEmpty(toolCalls),
    finishReason,
    refusal,
    usage: openaiChatUsage(body["usage"]),
    shape: "openai_chat",
  };
}

function openaiResponses(body: Json): ParsedResponse {
  const texts: string[] = [];
  const toolCalls: unknown[] = [];
  let refusal = false;
  for (const item of Array.isArray(body["output"]) ? body["output"] : []) {
    if (!isObject(item)) continue;
    if (item["type"] === "message") {
      for (const part of Array.isArray(item["content"]) ? item["content"] : []) {
        if (!isObject(part)) continue;
        if (part["type"] === "refusal") refusal = true;
        else if (typeof part["text"] === "string") texts.push(part["text"]);
      }
    } else if (item["type"] === "function_call" || item["type"] === "custom_tool_call") {
      toolCalls.push(item);
    }
  }
  const text = typeof body["output_text"] === "string" ? body["output_text"] : joined(texts);
  const incomplete = isObject(body["incomplete_details"]) ? str(body["incomplete_details"]["reason"]) : null;
  return {
    text,
    toolCalls: nonEmpty(toolCalls),
    finishReason: incomplete ?? str(body["status"]),
    refusal,
    usage: responsesUsage(body["usage"]),
    shape: "openai_responses",
  };
}

function anthropic(body: Json): ParsedResponse {
  const content = Array.isArray(body["content"]) ? body["content"] : [];
  const texts: string[] = [];
  const toolCalls: unknown[] = [];
  for (const block of content) {
    if (!isObject(block)) continue;
    if (block["type"] === "text" && typeof block["text"] === "string") texts.push(block["text"]);
    else if (block["type"] === "tool_use") toolCalls.push(block);
  }
  const finishReason = str(body["stop_reason"]);
  return {
    text: joined(texts),
    toolCalls: nonEmpty(toolCalls),
    finishReason,
    refusal: finishReason === "refusal",
    usage: anthropicUsage(body["usage"]),
    shape: "anthropic",
  };
}

function gemini(body: Json): ParsedResponse {
  const candidates = body["candidates"] as unknown[];
  const candidate = isObject(candidates[0]) ? candidates[0] : {};
  const content = isObject(candidate["content"]) ? candidate["content"] : {};
  const texts: string[] = [];
  const toolCalls: unknown[] = [];
  for (const part of Array.isArray(content["parts"]) ? content["parts"] : []) {
    if (!isObject(part) || part["thought"] === true) continue;
    if (typeof part["text"] === "string") texts.push(part["text"]);
    else if (isObject(part["functionCall"])) toolCalls.push(part["functionCall"]);
  }
  const finishReason = str(candidate["finishReason"]);
  return {
    text: joined(texts),
    toolCalls: nonEmpty(toolCalls),
    finishReason,
    refusal: ["SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII"].includes(finishReason ?? ""),
    usage: geminiUsage(body["usageMetadata"]),
    shape: "gemini",
  };
}

/** OpenTelemetry GenAI output messages: [{role, parts: [...], finish_reason}]. */
export function otelOutput(messages: unknown[]): ParsedResponse {
  const texts: string[] = [];
  const toolCalls: unknown[] = [];
  let finishReason: string | null = null;
  for (const message of messages) {
    if (!isObject(message)) continue;
    if (message["role"] !== undefined && message["role"] !== "assistant") continue;
    finishReason ??= str(message["finish_reason"]);
    const parts = Array.isArray(message["parts"]) ? message["parts"] : null;
    if (parts === null) {
      texts.push(...blockTexts(message["content"]));
      if (Array.isArray(message["tool_calls"])) toolCalls.push(...message["tool_calls"]);
      continue;
    }
    for (const part of parts) {
      if (!isObject(part)) continue;
      const type = part["type"] ?? "text";
      if (type === "text") {
        const text = part["content"] ?? part["text"];
        if (typeof text === "string") texts.push(text);
      } else if (type === "tool_call") {
        toolCalls.push(part);
      }
    }
  }
  return {
    text: joined(texts),
    toolCalls: nonEmpty(toolCalls),
    finishReason,
    refusal: REFUSAL_REASONS.has(finishReason ?? ""),
    usage: null,
    shape: "otel",
  };
}

/** Recognize and read a provider response body. Unknown shapes read as nothing. */
export function parseResponseBody(value: unknown): ParsedResponse {
  const body = parseJson(value);
  if (typeof body === "string") {
    return { text: body, toolCalls: null, finishReason: null, refusal: false, usage: null, shape: "text" };
  }
  if (Array.isArray(body)) return otelOutput(body);
  if (isObject(body)) {
    if (Array.isArray(body["choices"]) && body["choices"].length > 0) return openaiChat(body);
    if (body["object"] === "response" || (Array.isArray(body["output"]) && "status" in body)) {
      return openaiResponses(body);
    }
    if (Array.isArray(body["candidates"]) && body["candidates"].length > 0) return gemini(body);
    if ((body["type"] === "message" || body["role"] === "assistant") && Array.isArray(body["content"])) {
      return anthropic(body);
    }
    if (body["role"] === "assistant" || Array.isArray(body["parts"])) return otelOutput([body]);
  }
  return { text: null, toolCalls: null, finishReason: null, refusal: false, usage: null, shape: null };
}

/** A CapturedResponse from a parsed body plus the figures the record keeps beside it. */
export function capturedResponse(
  parsed: ParsedResponse,
  extra: { usage?: Usage | null; costUsd?: number | null; latencyMs?: number | null } = {},
): CapturedResponse {
  return {
    ...emptyResponse(),
    text: parsed.text,
    toolCalls: parsed.toolCalls,
    finishReason: parsed.finishReason,
    refusal: parsed.refusal,
    usage: mergeUsage(parsed.usage, extra.usage ?? null),
    costUsd: extra.costUsd ?? null,
    latencyMs: extra.latencyMs ?? null,
  };
}

/** Field by field: the body's figures first, the record's beside it second. */
export function mergeUsage(primary: Usage | null, secondary: Usage | null): Usage | null {
  if (primary === null) return secondary;
  if (secondary === null) return primary;
  return usageOf({
    inputTokens: primary.inputTokens ?? secondary.inputTokens,
    outputTokens: primary.outputTokens ?? secondary.outputTokens,
    cachedInputTokens: primary.cachedInputTokens ?? secondary.cachedInputTokens,
    reasoningTokens: primary.reasoningTokens ?? secondary.reasoningTokens,
  });
}

/** Provider implied by a request or response body shape, when the record does not say. */
export function providerFromShape(request: unknown, parsed: ParsedResponse): string | null {
  if (parsed.shape === "openai_chat" || parsed.shape === "openai_responses") return "openai";
  if (parsed.shape === "anthropic") return "anthropic";
  if (parsed.shape === "gemini") return "google";
  if (isObject(request)) {
    if (Array.isArray(request["contents"])) return "google";
    if ("anthropic_version" in request) return "anthropic";
  }
  return null;
}
