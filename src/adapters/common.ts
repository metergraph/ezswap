// Small helpers every adapter shares.

import { createHash } from "node:crypto";
import type { CapturedCall, CapturedResponse, Usage } from "../types.ts";

export type Json = Record<string, unknown>;

export function isObject(value: unknown): value is Json {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function str(value: unknown): string | null {
  return typeof value === "string" && value.length > 0 ? value : null;
}

/** A finite number, also from a numeric string (OTLP sends int64 as a string). */
export function num(value: unknown): number | null {
  if (typeof value === "number") return Number.isFinite(value) ? value : null;
  if (typeof value === "string" && value.trim() !== "" && Number.isFinite(Number(value))) return Number(value);
  return null;
}

export function firstNum(...values: unknown[]): number | null {
  for (const value of values) {
    const n = num(value);
    if (n !== null) return n;
  }
  return null;
}

export function firstStr(...values: unknown[]): string | null {
  for (const value of values) {
    const s = str(value);
    if (s !== null) return s;
  }
  return null;
}

/** JSON.parse that returns `fallback` instead of throwing. */
export function parseJson(value: unknown, fallback: unknown = value): unknown {
  if (typeof value !== "string") return value;
  try {
    return JSON.parse(value);
  } catch {
    return fallback;
  }
}

/** Short stable id for a record that has none of its own. */
export function hashId(text: string): string {
  return createHash("sha256").update(text).digest("hex").slice(0, 16);
}

/** An ISO 8601 time from an ISO string, epoch seconds, epoch milliseconds or epoch nanoseconds. */
export function isoTime(value: unknown): string | null {
  if (typeof value === "string" && value && !/^\d+(\.\d+)?$/.test(value)) {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? null : date.toISOString();
  }
  const n = num(value);
  if (n === null || n <= 0) return null;
  const ms = epochMs(n);
  const date = new Date(ms);
  return Number.isNaN(date.getTime()) ? null : date.toISOString();
}

/** Milliseconds since the epoch from seconds, milliseconds, microseconds or nanoseconds. */
export function epochMs(n: number): number {
  if (n > 1e17) return n / 1e6; // nanoseconds
  if (n > 1e14) return n / 1e3; // microseconds
  if (n > 1e11) return n; // milliseconds
  return n * 1000; // seconds
}

/** Wall time between two timestamps in any epoch unit or ISO form, or null. */
export function durationMs(start: unknown, end: unknown): number | null {
  const a = timeMs(start);
  const b = timeMs(end);
  if (a === null || b === null || b < a) return null;
  return Math.round((b - a) * 1000) / 1000;
}

function timeMs(value: unknown): number | null {
  if (typeof value === "string" && value && !/^\d+(\.\d+)?$/.test(value)) {
    const t = new Date(value).getTime();
    return Number.isNaN(t) ? null : t;
  }
  const n = num(value);
  return n === null || n <= 0 ? null : epochMs(n);
}

export function usageOf(parts: Partial<Usage>): Usage | null {
  const usage: Usage = {
    inputTokens: parts.inputTokens ?? null,
    outputTokens: parts.outputTokens ?? null,
    cachedInputTokens: parts.cachedInputTokens ?? null,
    reasoningTokens: parts.reasoningTokens ?? null,
  };
  return Object.values(usage).every((value) => value === null) ? null : usage;
}

export function emptyResponse(): CapturedResponse {
  return {
    text: null,
    toolCalls: null,
    finishReason: null,
    refusal: false,
    usage: null,
    costUsd: null,
    latencyMs: null,
  };
}

/** Lower-case provider name, with common aliases folded. */
export function normalizeProvider(value: unknown): string | null {
  const name = str(value)?.trim().toLowerCase();
  if (!name) return null;
  if (name === "gcp.gemini" || name === "gcp.vertex_ai" || name === "gemini" || name === "vertex_ai" || name === "vertex") {
    return "google";
  }
  if (name === "aws.bedrock") return "bedrock";
  if (name === "azure.ai.openai" || name === "azure.ai.inference") return "azure";
  return name;
}

export function addMissing(missing: string[], ...names: string[]): void {
  for (const name of names) if (!missing.includes(name)) missing.push(name);
}

export function call(partial: Partial<CapturedCall> & Pick<CapturedCall, "id" | "source">): CapturedCall {
  return {
    traceId: null,
    ts: null,
    provider: null,
    model: null,
    request: null,
    response: emptyResponse(),
    truncated: false,
    missing: [],
    ...partial,
  };
}

/** Context an adapter gets for each record. */
export interface RecordContext {
  /** 1-based line number, or array index plus one. */
  line: number;
  /** The record's source text, for hashing an id. */
  raw: string;
}

export interface Adapter {
  readonly name: string;
  /** Does this record look like this adapter's format? */
  detect(record: unknown): boolean;
  /** One record to zero or more calls (an OTLP export holds many spans). */
  read(record: unknown, context: RecordContext): CapturedCall[];
}
