// Production settings read from a captured request body: sampling parameters,
// the output cap, structured output, a forced tool and web search.

import type { CallParams, ForcedTool } from "../types.ts";
import { isObject } from "./messages.ts";

type Json = Record<string, unknown>;

export const MIN_MAX_TOKENS = 1;
export const MAX_MAX_TOKENS = 32000;
/** Captured settings could carry anything; only short strings are kept. */
const MAX_SETTING_CHARS = 64;

/** Containers a provider may nest its generation settings in. */
const SETTING_CONTAINERS = ["config", "generationConfig", "generation_config", "inferenceConfig", "params"];

function containers(payload: Json): Json[] {
  const found = [payload];
  for (const key of SETTING_CONTAINERS) {
    const value = payload[key];
    if (isObject(value)) found.push(value);
  }
  return found;
}

function finiteNumber(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function first<T>(payload: Json, keys: string[], read: (value: unknown) => T | null): T | null {
  for (const container of containers(payload)) {
    for (const key of keys) {
      const value = read(container[key]);
      if (value !== null) return value;
    }
  }
  return null;
}

/** Production's output cap from any of its spellings, bounded as a treatment's would be. */
export function productionMaxTokens(payload: unknown): number | null {
  if (!isObject(payload)) return null;
  const value = first(
    payload,
    ["max_tokens", "max_output_tokens", "max_completion_tokens", "maxOutputTokens", "maxTokens"],
    finiteNumber,
  );
  if (value === null || !Number.isInteger(value) || value < MIN_MAX_TOKENS) return null;
  return Math.min(value, MAX_MAX_TOKENS);
}

function stopSequences(value: unknown): string[] | null {
  if (typeof value === "string") return value ? [value] : null;
  if (Array.isArray(value)) {
    const stops = value.filter((item): item is string => typeof item === "string" && item.length > 0);
    return stops.length > 0 ? stops : null;
  }
  return null;
}

function shortString(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 && value.length <= MAX_SETTING_CHARS ? value : null;
}

function reasoningEffort(payload: Json): string | null {
  for (const container of containers(payload)) {
    const direct = shortString(container["reasoning_effort"]);
    if (direct !== null) return direct;
    const reasoning = container["reasoning"];
    const effort = isObject(reasoning) ? shortString(reasoning["effort"]) : null;
    if (effort !== null) return effort;
  }
  return null;
}

/** The production settings a replay carries, stop and top_p included. */
export function capturedParams(payload: unknown): CallParams {
  if (!isObject(payload)) {
    return { temperature: null, maxTokens: null, topP: null, stop: null, reasoningEffort: null };
  }
  return {
    temperature: first(payload, ["temperature"], finiteNumber),
    maxTokens: productionMaxTokens(payload),
    topP: first(payload, ["top_p", "topP"], finiteNumber),
    stop: first(payload, ["stop", "stop_sequences", "stopSequences"], stopSequences),
    reasoningEffort: reasoningEffort(payload),
  };
}

/**
 * The structured output production asked for, as a chat-completions
 * `response_format`: a top-level one, or a Responses API `text.format` (flat:
 * name, description, schema, strict beside type) translated to the nested
 * chat shape.
 */
export function responseFormat(payload: unknown): Json | null {
  if (!isObject(payload)) return null;
  const value = payload["response_format"];
  if (isObject(value) && (value["type"] === "json_object" || value["type"] === "json_schema")) return value;
  const text = payload["text"];
  const format = isObject(text) ? text["format"] : null;
  if (!isObject(format)) return null;
  if (format["type"] === "json_object") return { type: "json_object" };
  if (format["type"] === "json_schema" && isObject(format["schema"])) {
    const name = format["name"];
    const jsonSchema: Json = {
      name: typeof name === "string" && name ? name : "output",
      schema: format["schema"],
    };
    if (typeof format["description"] === "string") jsonSchema["description"] = format["description"];
    // Only when production set it: the default differs between the two APIs.
    if (typeof format["strict"] === "boolean") jsonSchema["strict"] = format["strict"];
    return { type: "json_schema", json_schema: jsonSchema };
  }
  return null;
}

/** Gemini names its search grounding tool by one of these keys. */
const GOOGLE_SEARCH_TOOLS = ["google_search", "googleSearch", "google_search_retrieval", "googleSearchRetrieval"];

function isWebSearchTool(tool: unknown): boolean {
  if (!isObject(tool)) return false;
  return String(tool["type"] ?? "").startsWith("web_search") || GOOGLE_SEARCH_TOOLS.some((key) => key in tool);
}

function toolLists(payload: Json): unknown[][] {
  const lists: unknown[][] = [];
  if (Array.isArray(payload["tools"])) lists.push(payload["tools"]);
  const config = payload["config"];
  if (isObject(config) && Array.isArray(config["tools"])) lists.push(config["tools"]);
  return lists;
}

/**
 * Did production search the web? Read from chat completions'
 * `web_search_options` or a built-in search tool (OpenAI Responses
 * `web_search`/`web_search_preview`, Anthropic `web_search_*`, Gemini's
 * `google_search` grounding).
 */
export function webSearchSeen(payload: unknown): boolean {
  if (!isObject(payload)) return false;
  if (isObject(payload["web_search_options"])) return true;
  return toolLists(payload).some((tools) => tools.some(isWebSearchTool));
}

/** Tools production offered that a replay could call: everything but web search. */
export function offeredTools(payload: unknown): unknown[] {
  if (!isObject(payload)) return [];
  return toolLists(payload).flat().filter((tool) => !isWebSearchTool(tool));
}

/** Does the tool choice make the model call a tool, rather than leave it free to? */
export function choiceForcesTool(payload: unknown): boolean {
  if (!isObject(payload)) return false;
  const choice = payload["tool_choice"];
  if (choice === "required" || choice === "any") return true;
  if (isObject(choice) && ["tool", "any", "function"].includes(String(choice["type"]))) return true;
  // Gemini: toolConfig.functionCallingConfig.mode ANY.
  for (const container of [payload, isObject(payload["config"]) ? payload["config"] : {}]) {
    const config = container["toolConfig"] ?? container["tool_config"];
    const calling = isObject(config) ? (config["functionCallingConfig"] ?? config["function_calling_config"]) : null;
    if (isObject(calling) && String(calling["mode"]).toUpperCase() === "ANY") return true;
  }
  return false;
}

/** Gemini: one function declaration and function calling mode ANY. */
function geminiForcedTool(payload: Json, declarations: unknown[]): ForcedTool | null {
  const declaration = declarations[0];
  if (declarations.length !== 1 || !isObject(declaration)) return null;
  const { name, description } = declaration;
  const schema = declaration["parameters"] ?? declaration["parametersJsonSchema"];
  if (typeof name !== "string" || !name || !isObject(schema) || !choiceForcesTool(payload)) return null;
  return { name, description: typeof description === "string" ? description : null, parameters: schema };
}

/**
 * A request that offered exactly one tool and forced the model to call it.
 * Anything else (several tools, a choice the model makes, no tools) is null:
 * an agent loop a replay cannot reproduce.
 */
export function forcedTool(payload: unknown): ForcedTool | null {
  if (!isObject(payload)) return null;
  const tools = payload["tools"];
  const choice = payload["tool_choice"];
  if (!Array.isArray(tools) || tools.length !== 1 || !isObject(tools[0])) return null;
  const tool = tools[0];
  const declarations = tool["functionDeclarations"] ?? tool["function_declarations"];
  if (Array.isArray(declarations)) return geminiForcedTool(payload, declarations);
  let name: unknown;
  let description: unknown;
  let schema: unknown;
  if (isObject(tool["function"])) {
    // OpenAI chat: {type: function, function: {name, description, parameters}}
    ({ name, description, parameters: schema } = tool["function"]);
  } else if (tool["type"] === "function") {
    // OpenAI Responses and OpenTelemetry: {type: function, name, description, parameters}
    ({ name, description, parameters: schema } = tool);
  } else {
    // Anthropic: {name, description, input_schema}
    ({ name, description, input_schema: schema } = tool);
  }
  if (typeof name !== "string" || !name || !isObject(schema)) return null;
  const forced =
    choice === "required" ||
    (isObject(choice) &&
      ((choice["type"] === "tool" && choice["name"] === name) ||
        choice["type"] === "any" ||
        (choice["type"] === "function" &&
          ((isObject(choice["function"]) && choice["function"]["name"] === name) || choice["name"] === name))));
  if (!forced) return null;
  return { name, description: typeof description === "string" ? description : null, parameters: schema };
}
