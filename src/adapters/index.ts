// Trace file readers: a JSONL file (one record per line), a JSON array, or a
// single JSON document such as an OTLP export, read into CapturedCall[].

import { createReadStream } from "node:fs";
import { open, readFile } from "node:fs/promises";
import { createInterface } from "node:readline";
import type { CapturedCall } from "../types.ts";
import type { Adapter, RecordContext } from "./common.ts";
import { langfuse } from "./langfuse.ts";
import { otel } from "./otel.ts";
import { raw } from "./raw.ts";
import { replayJsonl } from "./replay-jsonl.ts";

export type { Adapter, RecordContext } from "./common.ts";
export { REPLAY_TRACE_SCHEMA } from "./replay-jsonl.ts";
export { langfuse, otel, raw, replayJsonl };

/** Detection order matters: the most specific shapes first. */
export const ADAPTERS: readonly Adapter[] = [otel, langfuse, replayJsonl, raw];

export type AdapterName = "replay-jsonl" | "raw" | "langfuse" | "otel";

export function getAdapter(name: string): Adapter {
  const adapter = ADAPTERS.find((candidate) => candidate.name === name);
  if (!adapter) {
    throw new Error(`Unknown trace format "${name}". Use one of: ${ADAPTERS.map((a) => a.name).join(", ")}.`);
  }
  return adapter;
}

/** The adapter whose format the first record of a file looks like, or null. */
export function detectAdapter(firstRecord: unknown): Adapter | null {
  return ADAPTERS.find((adapter) => adapter.detect(firstRecord)) ?? null;
}

export interface ReadTracesOptions {
  /** Adapter name; detected from the first record when omitted. */
  adapter?: string;
  /** Stop after this many calls. */
  limit?: number;
  /** Called for each line that is not JSON or that the adapter could not read. */
  onSkip?: (line: number, reason: string) => void;
}

interface SourceRecord {
  record: unknown;
  context: RecordContext;
}

async function firstChar(path: string): Promise<string | null> {
  const handle = await open(path, "r");
  try {
    const buffer = Buffer.alloc(4096);
    let position = 0;
    for (;;) {
      const { bytesRead } = await handle.read(buffer, 0, buffer.length, position);
      if (bytesRead === 0) return null;
      const text = buffer.subarray(0, bytesRead).toString("utf8").replace(/^﻿/, "");
      const match = /\S/.exec(text);
      if (match) return match[0];
      position += bytesRead;
    }
  } finally {
    await handle.close();
  }
}

async function* wholeDocument(path: string): AsyncGenerator<SourceRecord> {
  const text = (await readFile(path, "utf8")).replace(/^﻿/, "");
  const document: unknown = JSON.parse(text);
  if (Array.isArray(document)) {
    for (let i = 0; i < document.length; i++) {
      yield { record: document[i], context: { line: i + 1, raw: JSON.stringify(document[i]) } };
    }
  } else {
    yield { record: document, context: { line: 1, raw: text } };
  }
}

/**
 * Records from a file. A file that starts with `[` is one JSON array. Any
 * other is read line by line; if its first line is not JSON on its own, the
 * file is one pretty-printed document (an OTLP export, say).
 */
async function* records(path: string, onSkip?: ReadTracesOptions["onSkip"]): AsyncGenerator<SourceRecord> {
  const start = await firstChar(path);
  if (start === null) return;
  if (start === "[") {
    yield* wholeDocument(path);
    return;
  }
  const lines = createInterface({ input: createReadStream(path, { encoding: "utf8" }), crlfDelay: Infinity });
  let line = 0;
  let seenRecord = false;
  try {
    for await (const text of lines) {
      line++;
      const trimmed = text.replace(/^﻿/, "").trim();
      if (!trimmed) continue;
      let record: unknown;
      try {
        record = JSON.parse(trimmed);
      } catch {
        if (!seenRecord) {
          lines.close();
          yield* wholeDocument(path);
          return;
        }
        onSkip?.(line, "not valid JSON");
        continue;
      }
      seenRecord = true;
      yield { record, context: { line, raw: trimmed } };
    }
  } finally {
    lines.close();
  }
}

/** Stream calls from a trace file. */
export async function* iterateTraces(path: string, options: ReadTracesOptions = {}): AsyncGenerator<CapturedCall> {
  let adapter: Adapter | null = options.adapter ? getAdapter(options.adapter) : null;
  const limit = options.limit ?? Infinity;
  let count = 0;
  if (limit <= 0) return;
  for await (const { record, context } of records(path, options.onSkip)) {
    if (adapter === null) {
      adapter = detectAdapter(record);
      if (adapter === null) {
        throw new Error(
          `Could not tell the trace format from the first record of ${path}. ` +
            `Pass the format explicitly: ${ADAPTERS.map((a) => a.name).join(", ")}.`,
        );
      }
    }
    let calls: CapturedCall[];
    try {
      calls = adapter.read(record, context);
    } catch (error) {
      options.onSkip?.(context.line, error instanceof Error ? error.message : String(error));
      continue;
    }
    for (const captured of calls) {
      yield captured;
      if (++count >= limit) return;
    }
  }
}

/** Read every call (up to `limit`) from a trace file. */
export async function readTraces(path: string, options: ReadTracesOptions = {}): Promise<CapturedCall[]> {
  const calls: CapturedCall[] = [];
  for await (const captured of iterateTraces(path, options)) calls.push(captured);
  return calls;
}
