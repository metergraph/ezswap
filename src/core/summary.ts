// Built-in statistics for one arm or for the control: cost, latency, output
// length, JSON parse rate, same top-level keys, empty or refused, cut off, and
// the share of input read from cache. Python semantics; matches Metergraph's
// hosted figures, which conformance/summarize.json pins.

import { MISSING, extract, parseJsonPath } from "./paths.ts";
import {
  PY_WS_CLASS,
  codePointLength,
  codePointSlice,
  isPlainObject,
  pyJsonBytes,
  pyMedian,
  pyRound,
  pySorted,
  pyStrip,
  pySum,
  pyTruthy,
} from "./py.ts";
import type { Facts, Outcome, Summary } from "./view-types.ts";

/** Finish reasons that mean the reply hit its token limit. */
export const CUT_OFF_REASONS: ReadonlySet<string> = new Set(["length", "max_tokens", "max_output_tokens"]);
/** Finish reasons that mean the provider refused or filtered the reply. */
export const REFUSAL_REASONS: ReadonlySet<string> = new Set(["content_filter", "refusal", "safety"]);

// What is kept of an output's structure once the output itself is bounded.
export const MAX_FACT_KEYS = 200;
export const MAX_FACT_KEY_CHARS = 256;
export const MAX_FACT_FIELD_BYTES = 4 * 1024;
/** A hard bound on the serialized facts of one output, whatever its shape. */
export const MAX_FACTS_BYTES = 32 * 1024;

const FENCE = new RegExp(`^\`\`\`[A-Za-z0-9_-]*${PY_WS_CLASS}*\\n([\\s\\S]*?)\\n?\`\`\`${PY_WS_CLASS}*$`);

/**
 * Parse an output as JSON, accepting one surrounding Markdown code fence.
 * NaN, Infinity and -Infinity are not JSON, so an output using them does not parse.
 */
export function parseOutputJson(output: unknown): [unknown, boolean] {
  if (typeof output !== "string") return [null, false];
  let text = pyStrip(output);
  if (!text) return [null, false];
  const fenced = FENCE.exec(text);
  if (fenced) text = pyStrip(fenced[1] as string);
  try {
    return [JSON.parse(text), true];
  } catch {
    return [null, false];
  }
}

/** A tracked field's value in one output, or null when it does not parse or has no such field. */
export function extractField(output: unknown, path: string): unknown {
  const [parsed, ok] = parseOutputJson(output);
  if (!ok) return null;
  const found = extract(parsed, parseJsonPath(path));
  return found === MISSING ? null : found;
}

/**
 * What the statistics need from one output, taken from the full text, and
 * bounded whatever the output holds: at most MAX_FACT_KEYS keys of at most
 * MAX_FACT_KEY_CHARS each, and at most MAX_FACTS_BYTES serialized. Over that,
 * the field values go first and then the keys; `truncated` says so.
 */
export function outputFacts(output: string | null | undefined, trackedFields: readonly string[] = []): Facts {
  const [value, ok] = parseOutputJson(output);
  const isObject = ok && isPlainObject(value);
  let fields: Record<string, unknown> = {};
  if (ok) {
    for (const path of trackedFields) {
      const found = extract(value, parseJsonPath(path));
      fields[path] = found === MISSING || pyJsonBytes(found) > MAX_FACT_FIELD_BYTES ? null : found;
    }
  }
  let keys: string[] | null = null;
  let truncated = false;
  if (isObject) {
    const names = pySorted(Object.keys(value as object));
    truncated = names.length > MAX_FACT_KEYS || names.some((name) => codePointLength(name) > MAX_FACT_KEY_CHARS);
    keys = pySorted(new Set(names.map((name) => codePointSlice(name, MAX_FACT_KEY_CHARS)))).slice(0, MAX_FACT_KEYS);
  }
  const facts: Facts = {
    json_ok: ok,
    keys,
    chars: codePointLength(output ?? ""),
    fields,
    truncated,
    keys_complete: isObject && !truncated,
  };
  if (pyJsonBytes(facts) > MAX_FACTS_BYTES) {
    fields = Object.fromEntries(Object.keys(fields).map((path) => [path, null]));
    facts.fields = fields;
    facts.truncated = true;
  }
  if (pyJsonBytes(facts) > MAX_FACTS_BYTES) {
    facts.fields = {};
    facts.keys = null;
    facts.keys_complete = false;
  }
  return facts;
}

/** Stored facts when there are any, else worked out from the stored output. */
export function factsOf(outcome: Outcome | string | null | undefined): Facts {
  if (isPlainObject(outcome)) {
    const stored = (outcome as Outcome).facts;
    return pyTruthy(stored) ? (stored as Facts) : outputFacts((outcome as Outcome).output);
  }
  return outputFacts(typeof outcome === "string" ? outcome : null);
}

/** One tracked field's value in one outcome: from its stored facts when they hold it. */
export function fieldValue(outcome: Outcome | null | undefined, path: string): unknown {
  const record = outcome ?? {};
  const facts = record.facts;
  if (pyTruthy(facts) && facts && Object.hasOwn(facts.fields ?? {}, path)) return (facts.fields as Record<string, unknown>)[path];
  return extractField(record.output, path);
}

export function isCutOff(finishReason: unknown): boolean {
  return typeof finishReason === "string" && CUT_OFF_REASONS.has(pyStrip(finishReason).toLowerCase());
}

/** Refused (by flag or finish reason), or no text at all. */
export function isEmptyOrRefused(output: unknown, finishReason: unknown, refusal = false): boolean {
  if (refusal) return true;
  if (typeof finishReason === "string" && REFUSAL_REASONS.has(pyStrip(finishReason).toLowerCase())) return true;
  return output == null || (typeof output === "string" && !pyStrip(output));
}

function median(values: number[]): number | null {
  return pyMedian(values);
}

function rate(numerator: number, denominator: number): number | null {
  return denominator ? pyRound(numerator / denominator, 4) : null;
}

export function keysComplete(facts: Facts): boolean {
  if (Object.hasOwn(facts, "keys_complete") && facts.keys_complete !== undefined) return pyTruthy(facts.keys_complete);
  return facts.keys != null && !pyTruthy(facts.truncated);
}

function tokenCount(value: unknown): number | null {
  return typeof value === "number" && Number.isInteger(value) && value >= 0 ? value : null;
}

/**
 * [input tokens read from cache, all input tokens] for one call, or null
 * when it reported no cache count or its input total cannot be placed. A
 * cache count above the input total cannot have been included in it.
 */
export function cachedInput(outcome: Outcome): [number, number] | null {
  const cached = tokenCount(outcome.cache_read_tokens);
  const reported = tokenCount(outcome.input_tokens);
  if (cached === null || reported === null) return null;
  if (cached > reported) return [cached, reported + cached];
  const includes = outcome.input_includes_cache_read === undefined ? true : outcome.input_includes_cache_read;
  if (cached === 0 || includes === true) return [cached, reported];
  if (includes === false) return [cached, reported + cached];
  return null;
}

/**
 * Built-in statistics for one arm (or the control). `controls` are the
 * control of the same cases, aligned, for the same-keys comparison. Rates are
 * over the calls that returned (errors are counted, not averaged in);
 * same_keys_rate is over the cases whose control output is a JSON object;
 * cached_input_share is over the calls that reported a cache count, weighted
 * by their input tokens, and null when none did, never zero.
 */
export function summarize(
  outcomes: readonly Outcome[],
  controls: readonly (Outcome | string | null)[] | null = null,
): Summary {
  const succeeded = outcomes.filter((o) => !pyTruthy(o.error));
  const costs = succeeded.filter((o) => o.cost_usd != null).map((o) => Number(o.cost_usd));
  const fees = succeeded.filter((o) => o.cost_search_usd != null).map((o) => Number(o.cost_search_usd));
  const modelCosts = succeeded
    .filter((o) => o.cost_usd != null && o.cost_search_usd != null)
    .map((o) => Number(o.cost_usd) - Number(o.cost_search_usd));
  const latencies = succeeded.filter((o) => o.latency_ms != null).map((o) => Number(o.latency_ms));
  const tokens = succeeded.filter((o) => o.output_tokens != null).map((o) => Number(o.output_tokens));
  const facts = succeeded.map((o) => factsOf(o));
  const cache = succeeded.map(cachedInput).filter((pair): pair is [number, number] => pair !== null);
  const cacheInputTotal = cache.reduce((s, [, total]) => s + total, 0);

  let sameKeys = 0;
  let comparable = 0;
  if (controls !== null) {
    if (controls.length !== outcomes.length) throw new Error("summarize: outcomes and controls must be aligned");
    outcomes.forEach((outcome, i) => {
      const controlFacts = factsOf(controls[i] ?? null);
      if (!keysComplete(controlFacts) || pyTruthy(outcome.error)) return;
      const outcomeFacts = factsOf(outcome);
      // An object whose key set was cut: not comparable, and not a mismatch.
      if (outcomeFacts.keys != null && !keysComplete(outcomeFacts)) return;
      // Its keys were too large to keep at all.
      if (outcomeFacts.keys == null && pyTruthy(outcomeFacts.truncated)) return;
      comparable += 1;
      const keys = outcomeFacts.keys;
      if (keys != null && sameSet(keys, controlFacts.keys ?? [])) sameKeys += 1;
    });
  }

  const mean8 = (values: number[]) => (values.length ? pyRound(pySum(values) / values.length, 8) : null);
  return {
    cases: outcomes.length,
    errors: outcomes.length - succeeded.length,
    cost_per_call_usd: mean8(costs),
    model_cost_per_call_usd: mean8(modelCosts),
    search_cost_per_call_usd: mean8(fees),
    latency_ms_p50: median(latencies),
    output_tokens_p50: median(tokens),
    output_chars_p50: median(facts.map((f) => f.chars)),
    json_parse_rate: rate(facts.filter((f) => f.json_ok).length, succeeded.length),
    same_keys_rate: controls !== null ? rate(sameKeys, comparable) : null,
    same_keys_compared: controls !== null ? comparable : null,
    empty_or_refused_rate: rate(
      succeeded.filter((o) => isEmptyOrRefused(o.output, o.finish_reason, pyTruthy(o.refusal))).length,
      succeeded.length,
    ),
    cut_off_rate: rate(succeeded.filter((o) => isCutOff(o.finish_reason)).length, succeeded.length),
    cached_input_share: cacheInputTotal ? pyRound(cache.reduce((s, [cached]) => s + cached, 0) / cacheInputTotal, 4) : null,
    cached_input_calls: cache.length,
    temperature_dropped: outcomes.filter((o) => pyTruthy(o.temperature_dropped)).length,
    response_format_dropped: outcomes.filter((o) => pyTruthy(o.response_format_dropped)).length,
  };
}

function sameSet(a: readonly string[], b: readonly string[]): boolean {
  const left = new Set(a);
  const right = new Set(b);
  return left.size === right.size && [...left].every((k) => right.has(k));
}
