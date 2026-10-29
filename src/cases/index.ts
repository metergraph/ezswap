// CapturedCall to Case: what a replay sends, production's output to compare
// against, and the reason a call cannot be replayed when it cannot.

import type { CapturedCall, Case, CaseOutput, Message, NotReplayableReason } from "../types.ts";
import { isObject, NotReplayable, requestMessages } from "./messages.ts";
import {
  capturedParams,
  choiceForcesTool,
  forcedTool,
  offeredTools,
  responseFormat,
  webSearchSeen,
} from "./request.ts";

export { MAX_INPUT_BYTES, NotReplayable, requestMessages } from "./messages.ts";
export {
  capturedParams,
  forcedTool,
  MAX_MAX_TOKENS,
  MIN_MAX_TOKENS,
  productionMaxTokens,
  responseFormat,
  webSearchSeen,
} from "./request.ts";

export const INPUT_PREVIEW_CHARS = 280;

export interface PrepareCaseOptions {
  /**
   * Replay calls whose production request searched the web, without search.
   * Off by default: such a call is not replayable ("web_search_not_replayed").
   */
  allowNoSearch?: boolean;
}

/** Added to `missing` when production offered tools the replay leaves out. */
export const TOOLS_NOT_OFFERED = "tools_not_offered";

function decodeJson(value: unknown): unknown {
  if (typeof value !== "string") return value;
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
}

/** A forced tool's answer: the arguments of the one tool call, when they are a JSON object. */
export function singleToolCallOutput(toolCalls: unknown): string | null {
  toolCalls = decodeJson(toolCalls);
  if (!Array.isArray(toolCalls) || toolCalls.length !== 1 || !isObject(toolCalls[0])) return null;
  const call = toolCalls[0];
  // Normalized {arguments}, OpenAI chat {function: {arguments}}, Anthropic tool_use {input},
  // Gemini functionCall {args}.
  const fn = isObject(call["function"]) ? call["function"] : isObject(call["functionCall"]) ? call["functionCall"] : null;
  const args = call["arguments"] ?? call["input"] ?? call["args"] ?? fn?.["arguments"] ?? fn?.["args"];
  if (isObject(args)) return JSON.stringify(args);
  if (typeof args === "string") {
    const parsed = parseOutputJson(args);
    if (isObject(parsed)) return args;
  }
  return null;
}

/** Parse an output as JSON, accepting one surrounding Markdown code fence. */
function parseOutputJson(output: string): unknown {
  let text = output.trim();
  const fenced = /^```[A-Za-z0-9_-]*\s*\n([\s\S]*?)\n?```$/.exec(text);
  if (fenced?.[1] !== undefined) text = fenced[1].trim();
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

/** Short preview of the last non-blank user turn. */
export function inputPreview(messages: Message[]): string | null {
  for (let i = messages.length - 1; i >= 0; i--) {
    const message = messages[i];
    if (message?.role === "user" && message.content.trim()) {
      return message.content.split(/\s+/).filter(Boolean).join(" ").slice(0, INPUT_PREVIEW_CHARS);
    }
  }
  return null;
}

function productionOutput(call: CapturedCall): CaseOutput {
  const response = call.response;
  let output = response.text;
  if (output === null || !output.trim()) {
    // A forced tool call writes no text: its arguments are the answer.
    output = singleToolCallOutput(response.toolCalls) ?? output;
  }
  const finishReason = typeof response.finishReason === "string" && response.finishReason.trim()
    ? response.finishReason.trim()
    : null;
  return {
    output,
    finishReason,
    refusal: response.refusal,
    usage: response.usage,
    costUsd: response.costUsd,
    // No model answers in 0 ms: a zero means the source logged no duration
    // (Langfuse writes endTime = startTime when a call was not timed).
    latencyMs: response.latencyMs !== null && response.latencyMs > 0 ? response.latencyMs : null,
  };
}

/** Read one captured call into a Case, replayable or with the reason it is not. */
export function prepareCase(call: CapturedCall, options: PrepareCaseOptions = {}): Case {
  const payload = decodeJson(call.request);
  const missing = [...call.missing];
  const webSearch = webSearchSeen(payload);
  const kase: Case = {
    id: call.id,
    traceId: call.traceId,
    ts: call.ts,
    source: call.source,
    provider: call.provider,
    model: call.model,
    system: null,
    messages: [],
    responseFormat: null,
    forcedTool: null,
    params: capturedParams(payload),
    webSearch,
    production: productionOutput(call),
    inputPreview: null,
    fidelity: "full",
    missing,
    notReplayable: null,
  };
  const finish = (reason: NotReplayableReason | null): Case => {
    kase.notReplayable = reason;
    kase.fidelity = missing.length > 0 ? "partial" : "full";
    return kase;
  };

  if (call.request === null || call.request === undefined) return finish("content_unavailable");
  if (call.truncated) return finish("content_truncated");
  try {
    const { system, messages } = requestMessages(payload);
    kase.system = system;
    kase.messages = messages;
  } catch (error) {
    if (error instanceof NotReplayable) return finish(error.reason);
    return finish("unparseable_request");
  }
  kase.inputPreview = inputPreview(kase.messages);
  kase.responseFormat = responseFormat(payload);
  kase.forcedTool = forcedTool(payload);

  const tools = offeredTools(payload);
  if (kase.forcedTool === null && tools.length > 0) {
    // A forced choice among several tools, or of a tool not offered alone,
    // cannot be reproduced by a replay that offers none.
    if (choiceForcesTool(payload)) return finish("tool_definitions_not_offered");
    // The model was free to answer in text; the replay offers no tools, and
    // the case is marked in `missing`.
    if (!missing.includes(TOOLS_NOT_OFFERED)) missing.push(TOOLS_NOT_OFFERED);
  }
  if (webSearch && !options.allowNoSearch) return finish("web_search_not_replayed");
  return finish(null);
}
