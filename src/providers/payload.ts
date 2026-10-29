// The OpenAI chat-completions request for one replay, and the reading of its reply.

import type { ForcedTool, ReplayCall, Usage } from "../types.ts";

export interface PayloadOptions {
  /** Send the call's temperature. False after the model refused it. */
  temperature: boolean;
  /** Send the call's response_format. False after the endpoint refused it. */
  responseFormat: boolean;
  /** The model id as this endpoint takes it. Default: call.model. */
  model?: string;
  /** "max_tokens" (most endpoints) or "max_completion_tokens" (OpenAI). */
  maxTokensField?: "max_tokens" | "max_completion_tokens";
  /** Fields the preset adds, never in place of the call's own. */
  extra?: Record<string, unknown>;
}

/**
 * A forced tool as a function tool and a forced choice. The schema goes as
 * strict: false, since a schema one provider accepts another's strict mode
 * may reject. The key order is fixed: the upper bound measures this JSON, and
 * conformance/estimate.json pins the bytes.
 */
export function forcedToolRequest(tool: ForcedTool): { tool: Record<string, unknown>; tool_choice: Record<string, unknown> } {
  const fn: Record<string, unknown> = { name: tool.name, parameters: tool.parameters, strict: false };
  if (typeof tool.description === "string") fn["description"] = tool.description;
  return {
    tool: { type: "function", function: fn },
    tool_choice: { type: "function", function: { name: tool.name } },
  };
}

export function buildPayload(call: ReplayCall, options: PayloadOptions): Record<string, unknown> {
  const messages: Array<{ role: string; content: string }> = [];
  if (call.system) messages.push({ role: "system", content: call.system });
  for (const message of call.messages) messages.push({ role: message.role, content: message.content });
  const body: Record<string, unknown> = {
    model: options.model ?? call.model,
    messages,
    [options.maxTokensField ?? "max_tokens"]: call.maxTokens,
  };
  if (options.temperature && call.temperature !== null) body["temperature"] = call.temperature;
  if (call.topP !== null) body["top_p"] = call.topP;
  if (call.stop !== null && call.stop.length > 0) body["stop"] = [...call.stop];
  if (call.reasoningEffort) body["reasoning_effort"] = call.reasoningEffort;
  if (options.responseFormat && call.responseFormat) body["response_format"] = structuredClone(call.responseFormat);
  if (call.forcedTool) {
    const forced = forcedToolRequest(call.forcedTool);
    body["tools"] = [forced.tool];
    body["tool_choice"] = forced.tool_choice;
  }
  for (const [key, value] of Object.entries(options.extra ?? {})) {
    if (!(key in body)) body[key] = value;
  }
  return body;
}

// ---------------------------------------------------------------------------
// The reply

const CUT_OFF = new Set(["length", "max_tokens", "max_output_tokens"]);
const REFUSAL = new Set(["content_filter", "refusal", "safety"]);

/** The model stopped at its output cap: whatever it wrote is unfinished. */
export function isCutOff(finishReason: string | null): boolean {
  return finishReason !== null && CUT_OFF.has(finishReason.toLowerCase());
}

/** A finish reason that says the model or a filter refused. */
export function isRefusalReason(finishReason: string | null): boolean {
  return finishReason !== null && REFUSAL.has(finishReason.toLowerCase());
}

export interface ParsedReply {
  output: string | null;
  fromToolCall: boolean;
  finishReason: string | null;
  refusal: boolean;
  usage: Usage | null;
  reportedCostUsd: number | null;
}

function object(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function count(value: unknown): number | null {
  return typeof value === "number" && Number.isInteger(value) && value >= 0 ? value : null;
}

/** The arguments of the reply's first tool call, as text, when there is one. */
export function toolCallArguments(message: Record<string, unknown>): string | null {
  const calls = message["tool_calls"];
  if (!Array.isArray(calls) || calls.length === 0) return null;
  const first = calls[0];
  if (first === null || typeof first !== "object" || Array.isArray(first)) return null;
  const fn = (first as Record<string, unknown>)["function"];
  if (fn === null || typeof fn !== "object" || Array.isArray(fn)) return null;
  const args = (fn as Record<string, unknown>)["arguments"];
  if (typeof args === "string") return args;
  if (args !== null && typeof args === "object" && !Array.isArray(args)) return JSON.stringify(args);
  return null;
}

/** Usage in the replay's terms. prompt_tokens includes cached tokens. */
export function readUsage(value: unknown): Usage | null {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return null;
  const usage = value as Record<string, unknown>;
  const prompt = object(usage["prompt_tokens_details"]);
  const completion = object(usage["completion_tokens_details"]);
  return {
    inputTokens: count(usage["prompt_tokens"]),
    outputTokens: count(usage["completion_tokens"]),
    cachedInputTokens: count(prompt["cached_tokens"]),
    reasoningTokens: count(completion["reasoning_tokens"]),
  };
}

export function parseReply(body: Record<string, unknown>): ParsedReply {
  const choices = Array.isArray(body["choices"]) ? body["choices"] : [];
  const first = object(choices[0]);
  const message = object(first["message"]);
  let content: unknown = message["content"];
  let fromToolCall = false;
  const args = toolCallArguments(message);
  if (args !== null && !(typeof content === "string" && content.trim())) {
    // A forced tool's answer is its arguments; "tool_calls" is then a normal stop.
    content = args;
    fromToolCall = true;
  }
  const finishReason = typeof first["finish_reason"] === "string" ? first["finish_reason"] : null;
  const refusalField = message["refusal"];
  const usageValue = body["usage"];
  const reported = object(usageValue)["cost"];
  return {
    output: typeof content === "string" ? content : null,
    fromToolCall,
    finishReason,
    refusal: Boolean(refusalField) || isRefusalReason(finishReason),
    usage: readUsage(usageValue),
    reportedCostUsd: typeof reported === "number" && Number.isFinite(reported) && reported >= 0 ? reported : null,
  };
}
