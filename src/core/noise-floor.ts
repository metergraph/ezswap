// The noise floor: production's model replayed twice on the same cases (A1
// and A2), and how far the two replays differ on every built-in statistic and
// tracked field. A port of Metergraph's Python reference;
// conformance/noise_floor.json pins it. Presentation, never a verdict.

import { codePointLength, pyEqual, pyJsonDumps, pyQuantile, pyRound, pySorted, pyStr, pyStrip, pySum, pyTruthy, isPlainObject } from "./py.ts";
import { factsOf, fieldValue, isCutOff, isEmptyOrRefused, keysComplete, summarize } from "./summary.ts";
import type {
  ControlRecord,
  DifferenceSpread,
  FieldKind,
  FloorField,
  MatchSpread,
  NoiseFloorView,
  NoiseMatchStat,
  NoiseNumberStat,
  Outcome,
  OverlapSpread,
  Spread,
} from "./view-types.ts";

export const NOISE_REPLAYS = 2;
export const NOISE_NUMBER_STATS: readonly NoiseNumberStat[] = ["cost_usd", "latency_ms", "output_tokens", "output_chars"];
export const NOISE_MATCH_STATS: readonly NoiseMatchStat[] = ["json_parse", "same_keys", "empty_or_refused", "cut_off", "output"];

// What a field's values are, which decides how two sides are compared on it.
export const FIELD_LABEL_MAX_DISTINCT = 12;
export const FIELD_SHORT_TEXT_MAX = 120;

/** One case as the floor reads it. */
export interface FloorCase {
  position: number;
  trace_id: string;
  status: string;
  control: ControlRecord | null;
}

/** What `noiseFloorView` reads: the cases, the floor's replays by case and replay, the tracked fields. */
export interface FloorInput {
  /** The floor's plan, or null when the run has no floor. */
  noise_floor: { replays?: number | null; gateway_models?: string[] | null } | null;
  cases: FloorCase[];
  noise_results: { position: number; replay: number; outcome: Outcome }[];
  tracked_fields: string[] | null;
}

function isNumber(value: unknown): value is number {
  return typeof value === "number";
}

/** A value as the page's String() writes it, so a label compares alike on both. */
function jsString(value: unknown): string {
  if (value === true) return "true";
  if (value === false) return "false";
  return pyStr(value);
}

/** The field kind as the reference's statistics decide it (string length in code points). */
export function fieldKindPy(values: readonly unknown[]): FieldKind {
  const present = values.filter((v) => v !== null && v !== undefined);
  if (!present.length) return "empty";
  if (present.every((v) => isNumber(v) && Number.isFinite(v))) return "number";
  if (present.every(Array.isArray)) return "list";
  if (present.every((v) => typeof v === "boolean")) return "label";
  if (present.every((v) => typeof v === "string" || typeof v === "boolean")) {
    const texts = present.map(jsString);
    const distinct = new Set(texts).size;
    if (
      texts.every((t) => codePointLength(t) <= 40) &&
      distinct <= Math.max(FIELD_LABEL_MAX_DISTINCT, 2) &&
      distinct < present.length
    ) {
      return "label";
    }
    return texts.every((t) => codePointLength(t) <= FIELD_SHORT_TEXT_MAX) ? "text" : "long_text";
  }
  return "mixed";
}

function listItems(value: readonly unknown[]): Set<string> {
  return new Set(
    value.map((item) =>
      isPlainObject(item) || Array.isArray(item) ? pyJsonDumps(item, { sortKeys: true, compact: true }) : jsString(item),
    ),
  );
}

/** Shared items over all distinct items (1 when both lists are empty). */
export function itemOverlap(a: readonly unknown[], b: readonly unknown[]): number {
  const left = listItems(a);
  const right = listItems(b);
  if (!left.size && !right.size) return 1;
  let shared = 0;
  for (const item of left) if (right.has(item)) shared += 1;
  return shared / new Set([...left, ...right]).size;
}

type Pair<T> = [traceId: string, a1: T, a2: T];

/** A2 minus A1 per case, and the size of those differences. */
export function differenceSpread(pairs: readonly Pair<number>[]): DifferenceSpread {
  const differences = pairs.map(([trace_id, a1, a2]) => ({ trace_id, a1, a2, difference: a2 - a1 }));
  const sizes = differences.map((d) => Math.abs(d.difference));
  const signed = differences.map((d) => d.difference);
  return {
    comparison: "difference",
    pairs: pairs.length,
    median_abs_difference: pyQuantile(sizes, 0.5),
    p90_abs_difference: pyQuantile(sizes, 0.9),
    max_abs_difference: sizes.length ? Math.max(...sizes) : null,
    // The signed range of A2 minus A1: what a treatment's own shift is read against.
    min_difference: signed.length ? Math.min(...signed) : null,
    max_difference: signed.length ? Math.max(...signed) : null,
    differences,
  };
}

/** How often A1 and A2 give the same value. */
export function matchSpread(pairs: readonly Pair<unknown>[]): MatchSpread {
  const matched = pairs.filter(([, a1, a2]) => pyEqual(a1, a2)).length;
  return {
    comparison: "match_rate",
    pairs: pairs.length,
    matched,
    match_rate: pairs.length ? pyRound(matched / pairs.length, 4) : null,
    mismatches: pairs.filter(([, a1, a2]) => !pyEqual(a1, a2)).map(([traceId]) => traceId),
  };
}

/** Item overlap between A1's list and A2's, per case. */
export function overlapSpread(pairs: readonly Pair<unknown[]>[]): OverlapSpread {
  const overlaps = pairs.map(([trace_id, a1, a2]) => ({ trace_id, overlap: pyRound(itemOverlap(a1, a2), 4) }));
  const scores = overlaps.map((o) => o.overlap);
  return {
    comparison: "overlap",
    pairs: pairs.length,
    mean_overlap: scores.length ? pyRound(pySum(scores) / scores.length, 4) : null,
    min_overlap: scores.length ? Math.min(...scores) : null,
    overlaps,
  };
}

/** The control as an outcome. Not in the reference: production's refusal flag is kept. */
export function controlOutcome(control: ControlRecord | null | undefined): Outcome {
  const c = control ?? {};
  return {
    output: c.output ?? null,
    cost_usd: c.cost_usd ?? null,
    cost_search_usd: c.cost_search_usd ?? null,
    latency_ms: c.latency_ms ?? null,
    finish_reason: c.finish_reason ?? null,
    output_tokens: c.output_tokens ?? null,
    input_tokens: c.input_tokens ?? null,
    cache_read_tokens: c.cache_read_tokens ?? null,
    input_includes_cache_read: c.input_includes_cache_read ?? null,
    facts: c.facts ?? null,
    refusal: pyTruthy(c.refusal),
    error: null,
  };
}

/** One built-in statistic of one output, or null when it has none. */
export function statOf(outcome: Outcome, key: NoiseNumberStat | NoiseMatchStat): unknown {
  if (key === "cost_usd" || key === "latency_ms" || key === "output_tokens") {
    const value = outcome[key];
    return isNumber(value) ? value : null;
  }
  const facts = factsOf(outcome);
  if (key === "output_chars") return facts.chars;
  if (key === "json_parse") return pyTruthy(facts.json_ok);
  if (key === "same_keys") return keysComplete(facts) ? pySorted(facts.keys ?? []) : null;
  if (key === "empty_or_refused") return isEmptyOrRefused(outcome.output, outcome.finish_reason, pyTruthy(outcome.refusal));
  if (key === "cut_off") return isCutOff(outcome.finish_reason);
  // A stored output cut at the bound is not the whole text.
  return pyTruthy(outcome.output_truncated) || outcome.output == null ? null : pyStrip(outcome.output);
}

const PER_CASE = ["differences", "mismatches", "overlaps", "a1", "a2"] as const;

/**
 * How far production's model differs from itself on the same cases: the
 * spread between its two replays (A1 and A2) for every built-in statistic and
 * tracked field, over the replayed cases where both replies came back. Null
 * without a noise floor. `includeCases: false` keeps the aggregates and leaves
 * out every per-case list.
 */
export function noiseFloorView(input: FloorInput, { includeCases = true }: { includeCases?: boolean } = {}): NoiseFloorView | null {
  const plan = input.noise_floor;
  if (!pyTruthy(plan) || !plan) return null;
  const noise = new Map<string, Outcome>();
  for (const r of input.noise_results ?? []) {
    const key = `${r.position}:${r.replay}`;
    if (!noise.has(key)) noise.set(key, r.outcome);
  }
  const at = (c: FloorCase, replay: number) => noise.get(`${c.position}:${replay}`);
  const failed = (o: Outcome | undefined) => pyTruthy((pyTruthy(o) ? o : { error: "missing" })?.error);

  const replayed = input.cases.filter((c) => c.status === "replayed");
  const a1 = replayed.filter((c) => at(c, 1) !== undefined).map((c) => at(c, 1) as Outcome);
  const a2 = replayed.filter((c) => at(c, 2) !== undefined).map((c) => at(c, 2) as Outcome);
  const both: [string, Outcome, Outcome][] = replayed
    .filter((c) => !failed(at(c, 1)) && !failed(at(c, 2)))
    .map((c) => [c.trace_id, at(c, 1) as Outcome, at(c, 2) as Outcome]);
  const controlsA1 = replayed.filter((c) => at(c, 1) !== undefined).map((c) => controlOutcome(c.control));
  const controlsA2 = replayed.filter((c) => at(c, 2) !== undefined).map((c) => controlOutcome(c.control));

  const stats: Record<string, Spread> = {};
  for (const key of NOISE_NUMBER_STATS) {
    const pairs = both.map(([t, x, y]) => [t, statOf(x, key), statOf(y, key)] as Pair<unknown>);
    stats[key] = differenceSpread(pairs.filter(([, x, y]) => x != null && y != null) as Pair<number>[]);
  }
  for (const key of NOISE_MATCH_STATS) {
    const pairs = both.map(([t, x, y]) => [t, statOf(x, key), statOf(y, key)] as Pair<unknown>);
    stats[key] = matchSpread(pairs.filter(([, x, y]) => x != null && y != null));
  }

  const fields: Record<string, FloorField> = {};
  for (const path of input.tracked_fields ?? []) {
    const values = both.map(([t, x, y]) => [t, fieldValue(x, path), fieldValue(y, path)] as Pair<unknown>);
    const kind = fieldKindPy([
      ...replayed.map((c) => fieldValue(controlOutcome(c.control), path)),
      ...values.flatMap(([, x, y]) => [x, y]),
    ]);
    const present = values.filter(([, x, y]) => x != null && y != null);
    let spread: Spread;
    if (kind === "number") {
      spread = differenceSpread(present.filter(([, x, y]) => isNumber(x) && isNumber(y)) as Pair<number>[]);
    } else if (kind === "list") {
      spread = overlapSpread(present.filter(([, x, y]) => Array.isArray(x) && Array.isArray(y)) as Pair<unknown[]>[]);
    } else if (kind === "label" || kind === "text" || kind === "long_text") {
      spread = matchSpread(present.map(([t, x, y]) => [t, pyStrip(jsString(x)), pyStrip(jsString(y))]));
    } else {
      spread = { comparison: null, pairs: present.length };
    }
    fields[path] = {
      kind,
      missing: values.length - present.length,
      ...spread,
      // Each replay's raw values, aligned to the replayed cases; null where that replay has none.
      a1: replayed.map((c) => fieldValue(at(c, 1), path)),
      a2: replayed.map((c) => fieldValue(at(c, 2), path)),
    };
  }
  if (!includeCases) {
    for (const spread of [...Object.values(stats), ...Object.values(fields)]) {
      for (const key of PER_CASE) delete (spread as unknown as Record<string, unknown>)[key];
    }
  }

  const spent = pySum([...a1, ...a2].map((o) => (pyTruthy(o.cost_usd) ? Number(o.cost_usd) : 0)));
  return {
    replays: plan.replays === undefined ? NOISE_REPLAYS : plan.replays,
    gateway_models: pySorted(new Set(plan.gateway_models ?? [])),
    cases: both.length,
    errors: {
      a1: a1.filter((o) => pyTruthy(o.error)).length,
      a2: a2.filter((o) => pyTruthy(o.error)).length,
    },
    a1: summarize(a1, controlsA1),
    a2: summarize(a2, controlsA2),
    summary: summarize(
      both.map(([, , y]) => y),
      both.map(([, x]) => x),
    ),
    control_summary: summarize(both.map(([, x]) => x)),
    spent_usd: pyRound(spent, 8),
    stats: stats as NoiseFloorView["stats"],
    fields,
  };
}
