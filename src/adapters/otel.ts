// OpenTelemetry GenAI spans: an OTLP JSON export (resourceSpans, scopeSpans,
// spans) or one span per line. Reads the current `gen_ai.input.messages` /
// `gen_ai.output.messages` attributes and the older indexed
// `gen_ai.prompt.N.*` / `gen_ai.completion.N.*` form. Content capture is
// opt-in in most SDKs: a span without it has no request, and cases reports
// it as content_unavailable.

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
  num,
  usageOf,
  type Adapter,
  type Json,
  type RecordContext,
} from "./common.ts";
import { capturedResponse, otelOutput, type ParsedResponse } from "./response.ts";

type Attributes = Record<string, unknown>;

/** One OTLP AnyValue to a plain value. */
function anyValue(value: unknown): unknown {
  if (!isObject(value)) return value;
  if ("stringValue" in value) return value["stringValue"];
  if ("intValue" in value) return num(value["intValue"]);
  if ("doubleValue" in value) return num(value["doubleValue"]);
  if ("boolValue" in value) return value["boolValue"];
  if ("bytesValue" in value) return value["bytesValue"];
  if (isObject(value["arrayValue"])) {
    const values = value["arrayValue"]["values"];
    return Array.isArray(values) ? values.map(anyValue) : [];
  }
  if (isObject(value["kvlistValue"])) {
    const values = value["kvlistValue"]["values"];
    return Array.isArray(values) ? attributeMap(values) : {};
  }
  return value;
}

/** OTLP `[{key, value}]` attributes, or an already flat map, to a flat map. */
function attributeMap(attributes: unknown): Attributes {
  if (Array.isArray(attributes)) {
    const out: Attributes = {};
    for (const entry of attributes) {
      if (isObject(entry) && typeof entry["key"] === "string") out[entry["key"]] = anyValue(entry["value"]);
    }
    return out;
  }
  return isObject(attributes) ? { ...attributes } : {};
}

function isSpan(record: Json): boolean {
  return "attributes" in record && ("spanId" in record || "span_id" in record || isObject(record["context"]) || "name" in record);
}

function detect(record: unknown): boolean {
  if (!isObject(record)) return false;
  if (Array.isArray(record["resourceSpans"]) || Array.isArray(record["resource_spans"])) return true;
  if (!isSpan(record)) return false;
  return Object.keys(attributeMap(record["attributes"])).some((key) => key.startsWith("gen_ai.") || key.startsWith("llm."));
}

/** Spans in an OTLP export, or the record itself when it is one span. */
function spansOf(record: Json): Json[] {
  const resourceSpans = record["resourceSpans"] ?? record["resource_spans"];
  if (!Array.isArray(resourceSpans)) return [record];
  const spans: Json[] = [];
  for (const resource of resourceSpans) {
    if (!isObject(resource)) continue;
    const scopes = resource["scopeSpans"] ?? resource["scope_spans"] ?? resource["instrumentationLibrarySpans"];
    for (const scope of Array.isArray(scopes) ? scopes : []) {
      if (!isObject(scope)) continue;
      for (const span of Array.isArray(scope["spans"]) ? scope["spans"] : []) {
        if (isObject(span)) spans.push(span);
      }
    }
  }
  return spans;
}

const SKIPPED_OPERATIONS = new Set(["embeddings", "execute_tool", "invoke_agent", "create_agent"]);

function isLlmSpan(attributes: Attributes): boolean {
  const operation = attributes["gen_ai.operation.name"];
  if (typeof operation === "string" && SKIPPED_OPERATIONS.has(operation)) return false;
  return (
    "gen_ai.request.model" in attributes ||
    "gen_ai.input.messages" in attributes ||
    "gen_ai.prompt.0.content" in attributes ||
    "gen_ai.prompt.0.role" in attributes ||
    (typeof operation === "string" && ["chat", "text_completion", "generate_content"].includes(operation))
  );
}

/** A JSON-valued attribute. A string that does not parse was most likely cut at a length limit. */
function jsonAttribute(attributes: Attributes, key: string): { value: unknown; cut: boolean } {
  const raw = attributes[key];
  if (raw === undefined || raw === null) return { value: undefined, cut: false };
  if (typeof raw !== "string") return { value: raw, cut: false };
  try {
    return { value: JSON.parse(raw), cut: false };
  } catch {
    return { value: undefined, cut: true };
  }
}

/** `prefix.0.role`, `prefix.0.content`, `prefix.1.role`... grouped by index. */
function indexed(attributes: Attributes, prefix: string): Map<number, Attributes> {
  const groups = new Map<number, Attributes>();
  const pattern = new RegExp(`^${prefix.replace(/\./g, "\\.")}\\.(\\d+)\\.(.+)$`);
  for (const [key, value] of Object.entries(attributes)) {
    const match = pattern.exec(key);
    if (!match) continue;
    const index = Number(match[1]);
    const group = groups.get(index) ?? {};
    group[match[2] as string] = value;
    groups.set(index, group);
  }
  return new Map([...groups.entries()].sort(([a], [b]) => a - b));
}

/** Content that an instrumentation stored as a JSON list of blocks stays a list. */
function legacyContent(value: unknown): unknown {
  if (typeof value === "string" && value.trimStart().startsWith("[")) {
    try {
      const parsed = JSON.parse(value);
      if (Array.isArray(parsed) && parsed.every((block) => isObject(block) && "type" in block)) return parsed;
    } catch {
      // Plain text that happens to start with a bracket.
    }
  }
  return value;
}

function legacyToolCalls(group: Attributes): unknown[] {
  const calls = indexed(group, "tool_calls");
  return [...calls.values()].map((tool) => ({
    id: tool["id"] ?? null,
    name: tool["name"] ?? null,
    arguments: tool["arguments"] ?? null,
  }));
}

function legacyMessages(attributes: Attributes): unknown[] | null {
  const groups = indexed(attributes, "gen_ai.prompt");
  if (groups.size === 0) return null;
  return [...groups.values()].map((group) => {
    const message: Json = { role: group["role"] ?? "user", content: legacyContent(group["content"] ?? null) };
    const toolCalls = legacyToolCalls(group);
    if (toolCalls.length > 0) message["tool_calls"] = toolCalls;
    if (group["tool_call_id"] !== undefined) message["role"] = "tool";
    return message;
  });
}

function legacyOutput(attributes: Attributes): ParsedResponse | null {
  const groups = indexed(attributes, "gen_ai.completion");
  if (groups.size === 0) return null;
  const messages = [...groups.values()].map((group) => {
    const message: Json = {
      role: group["role"] ?? "assistant",
      content: legacyContent(group["content"] ?? null),
      finish_reason: group["finish_reason"] ?? null,
    };
    const toolCalls = legacyToolCalls(group);
    if (toolCalls.length > 0) message["tool_calls"] = toolCalls;
    return message;
  });
  return otelOutput(messages);
}

function legacyTools(attributes: Attributes): unknown[] | null {
  const groups = indexed(attributes, "llm.request.functions");
  if (groups.size === 0) return null;
  return [...groups.values()].map((fn) => {
    let parameters: unknown = fn["parameters"];
    if (typeof parameters === "string") {
      try {
        parameters = JSON.parse(parameters);
      } catch {
        parameters = undefined;
      }
    }
    return { type: "function", name: fn["name"] ?? null, description: fn["description"] ?? null, parameters };
  });
}

function stopSequences(value: unknown): string[] | null {
  if (typeof value === "string") return [value];
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : null;
}

function readSpan(span: Json, resource: Attributes, context: RecordContext): CapturedCall | null {
  const attributes = { ...resource, ...attributeMap(span["attributes"]) };
  if (!isLlmSpan(attributes)) return null;
  const missing: string[] = [];
  let truncated = false;

  const input = jsonAttribute(attributes, "gen_ai.input.messages");
  const system = jsonAttribute(attributes, "gen_ai.system_instructions");
  const definitions = jsonAttribute(attributes, "gen_ai.tool.definitions");
  const schema = jsonAttribute(attributes, "gen_ai.request.structured_output_schema");
  truncated = input.cut || system.cut || definitions.cut;

  const messages = Array.isArray(input.value) ? input.value : legacyMessages(attributes);
  const tools = Array.isArray(definitions.value) ? definitions.value : legacyTools(attributes);
  const model = firstStr(attributes["gen_ai.request.model"], attributes["gen_ai.response.model"]);

  let request: Json | null = null;
  if (messages === null && !input.cut) {
    addMissing(missing, "input_messages");
  } else {
    const params: Json = {
      temperature: num(attributes["gen_ai.request.temperature"]),
      max_tokens: firstNum(attributes["gen_ai.request.max_tokens"], attributes["gen_ai.request.max_completion_tokens"]),
      top_p: num(attributes["gen_ai.request.top_p"]),
      stop: stopSequences(attributes["gen_ai.request.stop_sequences"]),
      seed: num(attributes["gen_ai.request.seed"]),
    };
    request = { model, messages: messages ?? [], params };
    if (system.value !== undefined) request["system"] = system.value;
    if (tools !== null) request["tools"] = tools;
    if (isObject(schema.value)) {
      request["response_format"] = { type: "json_schema", json_schema: { name: "output", schema: schema.value } };
    }
  }
  if (tools === null) addMissing(missing, "tools");
  else addMissing(missing, "tool_choice");
  if (!isObject(schema.value)) addMissing(missing, "response_format");

  const output = jsonAttribute(attributes, "gen_ai.output.messages");
  truncated = truncated || output.cut;
  let parsed: ParsedResponse | null = Array.isArray(output.value) ? otelOutput(output.value) : legacyOutput(attributes);
  if (parsed === null) {
    addMissing(missing, "output_messages");
    parsed = { text: null, toolCalls: null, finishReason: null, refusal: false, usage: null, shape: null };
  }
  const reasons = attributes["gen_ai.response.finish_reasons"];
  const finish = Array.isArray(reasons) ? firstStr(...reasons) : firstStr(reasons);
  if (finish !== null) parsed.finishReason = finish;

  const spanContext = isObject(span["context"]) ? span["context"] : {};
  const spanId = firstStr(span["spanId"], span["span_id"], spanContext["span_id"]);
  const start = span["startTimeUnixNano"] ?? span["start_time_unix_nano"] ?? span["start_time"];
  const end = span["endTimeUnixNano"] ?? span["end_time_unix_nano"] ?? span["end_time"];
  return call({
    id: spanId ?? hashId(`${context.raw}#${context.line}`),
    traceId: firstStr(span["traceId"], span["trace_id"], spanContext["trace_id"]),
    ts: isoTime(start),
    source: "otel",
    provider: normalizeProvider(firstStr(attributes["gen_ai.provider.name"], attributes["gen_ai.system"])),
    model,
    request,
    response: capturedResponse(parsed, {
      usage: usageOf({
        inputTokens: firstNum(attributes["gen_ai.usage.input_tokens"], attributes["gen_ai.usage.prompt_tokens"]),
        outputTokens: firstNum(attributes["gen_ai.usage.output_tokens"], attributes["gen_ai.usage.completion_tokens"]),
        cachedInputTokens: firstNum(
          attributes["gen_ai.usage.cache_read.input_tokens"],
          attributes["gen_ai.usage.cache_read_input_tokens"],
        ),
        reasoningTokens: firstNum(
          attributes["gen_ai.usage.reasoning.output_tokens"],
          attributes["gen_ai.usage.reasoning_tokens"],
        ),
      }),
      latencyMs: durationMs(start, end),
    }),
    truncated,
    missing,
  });
}

function read(record: unknown, context: RecordContext): CapturedCall[] {
  if (!isObject(record)) return [];
  const resourceSpans = record["resourceSpans"] ?? record["resource_spans"];
  const calls: CapturedCall[] = [];
  if (!Array.isArray(resourceSpans)) {
    const one = readSpan(record, {}, context);
    return one ? [one] : [];
  }
  for (const resource of resourceSpans) {
    if (!isObject(resource)) continue;
    const resourceAttributes = isObject(resource["resource"]) ? attributeMap(resource["resource"]["attributes"]) : {};
    // Resource attributes describe the service, not the call: keep only GenAI ones.
    const genAiResource = Object.fromEntries(
      Object.entries(resourceAttributes).filter(([key]) => key.startsWith("gen_ai.")),
    );
    for (const span of spansOf({ resourceSpans: [resource] })) {
      const one = readSpan(span, genAiResource, context);
      if (one) calls.push(one);
    }
  }
  return calls;
}

export const otel: Adapter = { name: "otel", detect, read };
