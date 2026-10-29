// Langfuse observation export (JSONL or a JSON array), GENERATION
// observations only. The batch export carries full input and output; UI and
// API previews cut values with a "...[truncated]" marker and are refused.

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
  num,
  parseJson,
  usageOf,
  type Adapter,
  type Json,
  type RecordContext,
} from "./common.ts";
import { capturedResponse, parseResponseBody } from "./response.ts";

const TRUNCATION_MARKER = "...[truncated]";

const OBSERVATION_TYPES = new Set([
  "GENERATION",
  "SPAN",
  "EVENT",
  "AGENT",
  "TOOL",
  "CHAIN",
  "RETRIEVER",
  "EVALUATOR",
  "EMBEDDING",
  "GUARDRAIL",
]);

function detect(record: unknown): boolean {
  if (!isObject(record)) return false;
  const typed = typeof record["type"] === "string" && OBSERVATION_TYPES.has(record["type"]);
  const traced = "traceId" in record || "trace_id" in record;
  return (typed && traced) || "modelParameters" in record || ("model_parameters" in record && "trace_id" in record && "type" in record);
}

/** Model parameters Langfuse may store as strings, read back as numbers. */
const NUMERIC_PARAMETERS = [
  "temperature",
  "top_p",
  "max_tokens",
  "max_completion_tokens",
  "max_output_tokens",
  "maxOutputTokens",
  "maxTokens",
];

function modelParameters(value: unknown): Json {
  const parameters = parseJson(value);
  if (!isObject(parameters)) return {};
  const out: Json = {};
  for (const [key, raw] of Object.entries(parameters)) {
    if (NUMERIC_PARAMETERS.includes(key)) {
      const n = num(raw);
      if (n !== null) out[key] = n;
    } else if (key === "response_format" || key === "tool_choice" || key === "stop" || key === "tools") {
      out[key] = parseJson(raw);
    } else if (key === "reasoning_effort" && typeof raw === "string") {
      out[key] = raw;
    }
  }
  return out;
}

function inferProvider(model: string | null): string | null {
  if (!model) return null;
  const name = model.toLowerCase();
  if (name.startsWith("claude") || name.includes("anthropic")) return "anthropic";
  if (name.startsWith("gemini") || name.includes("google/")) return "google";
  if (/^(gpt|o[1-9]|chatgpt|text-|davinci|openai\/)/.test(name)) return "openai";
  return null;
}

function usage(record: Json) {
  const details = isObject(record["usageDetails"]) ? record["usageDetails"] : isObject(record["usage_details"]) ? record["usage_details"] : {};
  const legacy = isObject(record["usage"]) ? record["usage"] : {};
  return usageOf({
    inputTokens: firstNum(details["input"], legacy["input"], record["inputUsage"], record["promptTokens"], legacy["promptTokens"]),
    outputTokens: firstNum(details["output"], legacy["output"], record["outputUsage"], record["completionTokens"], legacy["completionTokens"]),
    cachedInputTokens: firstNum(
      details["input_cached_tokens"],
      details["cache_read_input_tokens"],
      details["input_cache_read"],
      details["cached_tokens"],
    ),
    reasoningTokens: firstNum(details["output_reasoning_tokens"], details["reasoning_tokens"], details["output_reasoning"]),
  });
}

function cost(record: Json): number | null {
  const details = isObject(record["costDetails"]) ? record["costDetails"] : isObject(record["cost_details"]) ? record["cost_details"] : {};
  return firstNum(details["total"], record["totalCost"], record["total_cost"], record["calculatedTotalCost"]);
}

function read(record: unknown, context: RecordContext): CapturedCall[] {
  if (!isObject(record) || record["type"] !== "GENERATION") return [];
  const missing: string[] = [];
  const input = parseJson(record["input"]);
  const output = parseJson(record["output"]);
  const truncated =
    JSON.stringify(input ?? null).includes(TRUNCATION_MARKER) ||
    JSON.stringify(output ?? null).includes(TRUNCATION_MARKER);
  const model = firstStr(record["model"], record["providedModelName"], record["provided_model_name"]);
  const parameters = modelParameters(record["modelParameters"] ?? record["model_parameters"]);

  let request: unknown = null;
  if (input === null || input === undefined || input === "") {
    addMissing(missing, "input");
  } else {
    // The OpenAI integration logs {messages, tools} when tools were offered,
    // else the bare message list or prompt.
    const content = isObject(input) ? input : Array.isArray(input) ? { messages: input } : { prompt: input };
    request = { model, ...parameters, ...content };
    if (!("tools" in content) && !("functions" in content) && !("tools" in parameters)) addMissing(missing, "tools");
  }

  let parsed = parseResponseBody(output ?? null);
  if (parsed.shape === null && output !== null && output !== undefined) {
    // A structured answer Langfuse stored as parsed JSON: its JSON is the output.
    parsed = { ...parsed, text: JSON.stringify(output), shape: "text" };
  }
  if (output === null || output === undefined) addMissing(missing, "output");

  const startTime = record["startTime"] ?? record["start_time"];
  const endTime = record["endTime"] ?? record["end_time"];
  return [
    call({
      id: firstStr(record["id"]) ?? hashId(context.raw),
      traceId: firstStr(record["traceId"], record["trace_id"]),
      ts: isoTime(startTime),
      source: "langfuse",
      provider: inferProvider(model),
      model,
      request,
      response: capturedResponse(parsed, {
        usage: usage(record),
        costUsd: cost(record),
        latencyMs: durationMs(startTime, endTime),
      }),
      truncated,
      missing,
    }),
  ];
}

export const langfuse: Adapter = { name: "langfuse", detect, read };
