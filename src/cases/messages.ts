// Turns a captured request body into a system prompt and plain text turns, or
// refuses it with the reason a replay could not reproduce it faithfully.
//
// Accepted shapes: OpenAI chat `messages`, OpenAI Responses `input` and
// `instructions`, Anthropic `system` and `messages`, Gemini `contents` and
// `systemInstruction`, OpenTelemetry GenAI messages (`role` plus typed
// `parts`), a bare message list, and a bare prompt string.

import type { Message, NotReplayableReason } from "../types.ts";

/** The whole composed input (system plus messages) a replay may send. */
export const MAX_INPUT_BYTES = 64 * 1024;

export class NotReplayable extends Error {
  readonly reason: NotReplayableReason;
  constructor(reason: NotReplayableReason, message: string) {
    super(message);
    this.reason = reason;
  }
}

/** Block types that carry tool calls or tool results. */
const TOOL_BLOCK_TYPES = new Set([
  "tool_use",
  "tool_result",
  "server_tool_use",
  "web_search_tool_result",
  "mcp_tool_use",
  "mcp_tool_result",
  "function_call",
  "function_call_output",
  "custom_tool_call",
  "custom_tool_call_output",
  "computer_call",
  "computer_call_output",
  "web_search_call",
  "file_search_call",
  "code_interpreter_call",
  "local_shell_call",
  "local_shell_call_output",
  "mcp_call",
  "tool_call",
  "tool_call_response",
]);

const TEXT_BLOCK_TYPES = new Set([undefined, null, "text", "input_text", "output_text"]);

const GEMINI_TOOL_PARTS = ["functionCall", "function_call", "functionResponse", "function_response"];

type Json = Record<string, unknown>;

export function isObject(value: unknown): value is Json {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function blockLabel(kind: unknown): string {
  return typeof kind === "string" && kind.length > 0 && kind.length <= 40 && /^[A-Za-z0-9_]+$/.test(kind)
    ? kind
    : "unknown";
}

function toolHistory(): NotReplayable {
  return new NotReplayable(
    "tool_history",
    "the request contains tool calls or tool results, which a replay cannot reproduce",
  );
}

function malformed(message: string): NotReplayable {
  return new NotReplayable("unparseable_request", message);
}

/** Text of one message's content, refusing anything a replay would distort. */
function contentText(content: unknown): string | null {
  if (content === null || content === undefined) return null;
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) {
    throw malformed("a message has content that is neither text nor a list of blocks");
  }
  const texts: string[] = [];
  for (const item of content) {
    if (typeof item === "string") {
      texts.push(item);
      continue;
    }
    if (!isObject(item)) throw malformed("a message has an unreadable content block");
    const kind = item["type"];
    if (typeof kind === "string" && TOOL_BLOCK_TYPES.has(kind)) throw toolHistory();
    if (TEXT_BLOCK_TYPES.has(kind as string | undefined | null)) {
      // OpenAI and Anthropic spell the text `text`; OpenTelemetry spells it `content`.
      const text = typeof item["text"] === "string" ? item["text"] : item["content"];
      if (typeof text === "string") {
        texts.push(text);
        continue;
      }
    }
    throw new NotReplayable(
      "unsupported_content",
      `a replay cannot reproduce ${blockLabel(kind)} content blocks`,
    );
  }
  return texts.join("\n");
}

function geminiText(parts: unknown): string {
  if (!Array.isArray(parts)) throw malformed("a Gemini message has unreadable parts");
  const texts: string[] = [];
  for (const part of parts) {
    if (!isObject(part)) throw malformed("a Gemini message has an unreadable part");
    if (GEMINI_TOOL_PARTS.some((key) => key in part)) throw toolHistory();
    if (typeof part["text"] !== "string") {
      throw new NotReplayable("unsupported_content", "a replay can only reproduce text Gemini parts");
    }
    texts.push(part["text"]);
  }
  return texts.join("\n");
}

function geminiSystem(value: unknown): string {
  if (typeof value === "string") return value;
  if (isObject(value) && "parts" in value) return geminiText(value["parts"]);
  if (Array.isArray(value)) return geminiText(value);
  throw new NotReplayable("unsupported_content", "a replay cannot read the Gemini system instruction");
}

function join(first: string | null, second: string | null): string | null {
  if (!second) return first;
  return first ? `${first}\n\n${second}` : second;
}

export interface ParsedMessages {
  system: string | null;
  messages: Message[];
}

/** System prompt and turns as captured, before merging and the size rules. */
function capturedMessages(payload: unknown): ParsedMessages {
  if (typeof payload === "string") {
    return { system: null, messages: payload.trim() ? [{ role: "user", content: payload }] : [] };
  }
  if (Array.isArray(payload)) payload = { messages: payload };
  if (!isObject(payload)) {
    throw malformed("the request is not an object, a message list, or text");
  }
  let system: string | null = null;
  for (const key of ["system", "system_instructions", "instructions"]) {
    const value = payload[key];
    if (value !== null && value !== undefined) system = join(system, contentText(value));
  }
  const config = isObject(payload["config"]) ? payload["config"] : {};
  for (const container of [payload, config]) {
    for (const key of ["system_instruction", "systemInstruction"]) {
      const value = container[key];
      if (value !== null && value !== undefined) system = join(system, geminiSystem(value));
    }
  }
  const messages: Message[] = [];
  const contents = payload["contents"];
  if (contents !== undefined && !("messages" in payload)) {
    if (typeof contents === "string") {
      return { system, messages: contents.trim() ? [{ role: "user", content: contents }] : [] };
    }
    if (!Array.isArray(contents)) throw malformed("the Gemini contents are not a list");
    for (const item of contents) {
      if (!isObject(item)) throw malformed("a Gemini message is not an object");
      const role = item["role"] === "model" ? "assistant" : "user";
      const text = geminiText(item["parts"]);
      if (text) messages.push({ role, content: text });
    }
    return { system, messages };
  }
  let raw: unknown = "messages" in payload ? payload["messages"] : payload["input"];
  if ((raw === undefined || raw === null) && typeof payload["prompt"] === "string") raw = payload["prompt"];
  if (typeof raw === "string" && !("messages" in payload)) {
    // Responses `input` and completion `prompt` may be a bare string.
    return { system, messages: raw.trim() ? [{ role: "user", content: raw }] : [] };
  }
  if (raw === undefined || raw === null) return { system, messages };
  if (!Array.isArray(raw)) throw malformed("the request messages are not a list");
  for (const item of raw) {
    if (!isObject(item)) throw malformed("a message is not an object");
    const kind = item["type"];
    const toolCalls = item["tool_calls"];
    if (
      (typeof kind === "string" && TOOL_BLOCK_TYPES.has(kind)) ||
      (Array.isArray(toolCalls) ? toolCalls.length > 0 : Boolean(toolCalls)) ||
      Boolean(item["function_call"])
    ) {
      throw toolHistory();
    }
    const role = String(item["role"] ?? "user").toLowerCase();
    if (role === "tool" || role === "function") throw toolHistory();
    // OpenTelemetry GenAI messages carry typed `parts` instead of `content`.
    const text = contentText("parts" in item && !("content" in item) ? item["parts"] : item["content"]);
    if (role === "system" || role === "developer") {
      system = join(system, text);
    } else if (role === "user" || role === "assistant") {
      if (text) messages.push({ role, content: text });
    } else {
      throw malformed(`a message has an unsupported role ${blockLabel(role)}`);
    }
  }
  return { system, messages };
}

function byteLength(text: string): number {
  return Buffer.byteLength(text, "utf8");
}

/**
 * The system prompt and turns a replay sends. Same-role turns merge, the last
 * turn must be the user's and the whole input must fit in 64 KiB.
 * Throws NotReplayable otherwise.
 */
export function requestMessages(payload: unknown): ParsedMessages {
  const { system, messages } = capturedMessages(payload);
  if (messages.length === 0) {
    throw new NotReplayable("no_messages", "the request has no text turns to replay");
  }
  // Dropping an empty turn can leave two turns from one role in a row, which
  // providers reject; merge them so the conversation keeps its order.
  const merged: Message[] = [];
  for (const message of messages) {
    const last = merged[merged.length - 1];
    if (last && last.role === message.role) {
      merged[merged.length - 1] = { role: message.role, content: `${last.content}\n\n${message.content}` };
    } else {
      merged.push({ ...message });
    }
  }
  if (merged[merged.length - 1]?.role !== "user") {
    throw new NotReplayable("last_turn_not_user", "the request ends with an assistant turn");
  }
  const size = byteLength(system ?? "") + merged.reduce((sum, m) => sum + byteLength(m.content), 0);
  if (size > MAX_INPUT_BYTES) {
    throw new NotReplayable("input_too_large", `the replay input exceeds ${MAX_INPUT_BYTES / 1024} KiB`);
  }
  return { system, messages: merged };
}
