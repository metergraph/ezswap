// Tracked-field comparisons over the experiment view: what kind of values a
// field holds, and how one side reads against another on it (per-call
// differences for numbers, match rate for labels and text, item overlap for
// lists). Extracted from hosted Metergraph's page and kept to its JavaScript
// semantics, so the numbers match it exactly. Shown, never judged.

import { jsonPaths, valueAt } from "./paths.ts";
import type { FieldKind } from "./view-types.ts";

// The page functions read the view loosely, as the page does: a hosted view
// and one built from a run file both fit, and so do partial test inputs.
export type Loose = any;

/** Below this many values a 10th-to-90th percentile range is mostly noise. */
export const SPREAD_MIN = 10;

// A model's JSON often comes fenced in markdown: one surrounding fence of any language, then JSON.
const FENCE = /^```[A-Za-z0-9_-]*\s*\n([\s\S]*?)\n?```\s*$/;

/** Parse an output the way the page does (JavaScript whitespace rules). */
export function parseOutput(output: unknown): { ok: boolean; value: unknown } {
  if (output == null) return { ok: false, value: undefined };
  if (typeof output === "object") return { ok: true, value: output };
  let text = String(output).trim();
  if (!text) return { ok: false, value: undefined };
  const fenced = text.match(FENCE);
  if (fenced) text = (fenced[1] as string).trim();
  try {
    return { ok: true, value: JSON.parse(text) };
  } catch {
    return { ok: false, value: undefined };
  }
}

export function outputText(output: unknown): string {
  if (output == null) return "";
  return typeof output === "string" ? output : JSON.stringify(output, null, 2);
}

/** Pretty JSON when it parses, the verbatim string when it does not. */
export function displayOutput(output: unknown): string {
  const parsed = parseOutput(output);
  return parsed.ok ? JSON.stringify(parsed.value, null, 2) : outputText(output);
}

/** Every path seen in any control or arm output, with how many outputs carry it. */
export function availablePaths(cases: Loose[] | null | undefined): { path: string; count: number }[] {
  const counts = new Map<string, number>();
  for (const c of cases || []) {
    const outputs = [c.control?.output, ...Object.values(c.arms || {}).map((a: Loose) => a?.output)];
    for (const output of outputs) {
      const parsed = parseOutput(output);
      if (!parsed.ok) continue;
      for (const path of new Set(jsonPaths(parsed.value))) counts.set(path, (counts.get(path) || 0) + 1);
    }
  }
  return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([path, count]) => ({ path, count }));
}

/** The paths seen in the outputs, plus any the run tracked. */
export function pickerPaths(cases: Loose[] | null | undefined, tracked: string[] = []): { path: string; count: number }[] {
  const seen = availablePaths(cases);
  const known = new Set(seen.map((p) => p.path));
  return [...seen, ...tracked.filter((path) => !known.has(path)).map((path) => ({ path, count: 0 }))];
}

const LABEL_MAX_DISTINCT = 12;
const SHORT_TEXT_MAX = 120;

/** What a field's values are, which decides how arms are compared on it (lengths in UTF-16 units). */
export function fieldKind(values: readonly unknown[]): FieldKind {
  const present = values.filter((v) => v !== undefined && v !== null);
  if (!present.length) return "empty";
  if (present.every((v) => typeof v === "number" && Number.isFinite(v))) return "number";
  if (present.every(Array.isArray)) return "list";
  if (present.every((v) => typeof v === "boolean")) return "label";
  if (present.every((v) => typeof v === "string" || typeof v === "boolean")) {
    const distinct = new Set(present.map(String));
    const short = present.every((v) => String(v).length <= 40);
    if (short && distinct.size <= Math.max(LABEL_MAX_DISTINCT, 2) && distinct.size < present.length) return "label";
    if (present.every((v) => String(v).length <= SHORT_TEXT_MAX)) return "text";
    return "long_text";
  }
  return "mixed";
}

export const KIND_LABELS: Record<FieldKind, string> = {
  number: "Number",
  label: "Label",
  list: "List",
  text: "Short text",
  long_text: "Long text",
  mixed: "Mixed",
  empty: "No values",
};

/** Each case's position among the replayed cases, the order `fields` series follow; -1 otherwise. */
export function replayedIndexes(cases: Loose[]): number[] {
  let next = 0;
  return cases.map((c) => (c.status == null || c.status === "replayed" ? next++ : -1));
}

/**
 * One side's value on a field. A stored output cut at the bound is a prefix
 * that no longer parses, so a shortened one takes the series value, which was
 * read from the full output. Null counts as missing, never as zero.
 */
export function sideValue(result: Loose, path: string, series: unknown[] | undefined, index: number): unknown {
  let value: unknown;
  if (result?.output_truncated && Array.isArray(series) && index >= 0) {
    value = series[index];
  } else {
    const parsed = parseOutput(result?.output);
    value = parsed.ok ? valueAt(parsed.value, path) : undefined;
  }
  return value === null ? undefined : value;
}

export function controlValues(cases: Loose[], path: string, fields?: Loose): unknown[] {
  const indexes = replayedIndexes(cases);
  return cases.map((c, i) => sideValue(c.control, path, fields?.[path]?.control, indexes[i] as number));
}

export function armValues(cases: Loose[], path: string, label: string, fields?: Loose): unknown[] {
  const indexes = replayedIndexes(cases);
  return cases.map((c, i) => sideValue(c.arms?.[label], path, fields?.[path]?.[label], indexes[i] as number));
}

/** Fields worth pre-highlighting: present in at least half the control outputs, of a kind with a comparison. */
export function suggestedFields(cases: Loose[] | null | undefined, limit = 3): string[] {
  const list = cases || [];
  if (!list.length) return [];
  const half = Math.ceil(list.length / 2);
  const order = ["label", "number", "list", "text"];
  return availablePaths(list)
    .map(({ path }) => ({ path, values: controlValues(list, path) }))
    .filter(({ values }) => values.filter((v) => v !== undefined).length >= half)
    .map(({ path, values }) => ({ path, kind: fieldKind(values) }))
    .filter(({ kind }) => order.includes(kind))
    .sort((a, b) => order.indexOf(a.kind) - order.indexOf(b.kind))
    .slice(0, limit)
    .map(({ path }) => path);
}

// --- Statistics ---------------------------------------------------------------

export function quantile(sorted: readonly number[], q: number): number | null {
  if (!sorted.length) return null;
  const pos = (sorted.length - 1) * q;
  const lo = Math.floor(pos);
  const hi = Math.ceil(pos);
  return (sorted[lo] as number) + ((sorted[hi] as number) - (sorted[lo] as number)) * (pos - lo);
}

export interface BoxStats {
  n: number;
  min: number;
  q1: number;
  median: number;
  q3: number;
  max: number;
}

export function boxStats(values: readonly unknown[]): BoxStats | null {
  const sorted = (values.filter((v) => typeof v === "number" && Number.isFinite(v)) as number[]).sort((a, b) => a - b);
  if (!sorted.length) return null;
  return {
    n: sorted.length,
    min: sorted[0] as number,
    q1: quantile(sorted, 0.25) as number,
    median: quantile(sorted, 0.5) as number,
    q3: quantile(sorted, 0.75) as number,
    max: sorted[sorted.length - 1] as number,
  };
}

export function mean(values: readonly unknown[]): number | null {
  const nums = values.filter((v) => typeof v === "number" && Number.isFinite(v)) as number[];
  return nums.length ? nums.reduce((s, v) => s + v, 0) / nums.length : null;
}

function listItems(value: unknown): string[] {
  return Array.isArray(value) ? value.map((v) => (typeof v === "object" ? JSON.stringify(v) : String(v))) : [];
}

/** Shared list items over all distinct items (1 when both are empty). */
export function overlap(a: unknown, b: unknown): number {
  const left = new Set(listItems(a));
  const right = new Set(listItems(b));
  if (!left.size && !right.size) return 1;
  let shared = 0;
  for (const item of left) if (right.has(item)) shared += 1;
  return shared / (left.size + right.size - shared);
}

export interface FieldPair {
  trace_id: string;
  control: unknown;
  arm: unknown;
}

/** One arm's values beside the control's, per case, over the cases both have a value on. */
export function fieldPairs(cases: Loose[] | null | undefined, path: string, label: string, fields?: Loose): FieldPair[] {
  const list = cases || [];
  const control = controlValues(list, path, fields);
  const arm = armValues(list, path, label, fields);
  return list
    .map((c, i) => ({ trace_id: c.trace_id, control: control[i], arm: arm[i] }))
    .filter((p) => p.control !== undefined && p.arm !== undefined);
}

export interface LabelTally {
  from: string;
  to: string;
  count: number;
  same: boolean;
}

/** How often each control label became each arm label, most common first. */
export function labelTally(pairs: readonly FieldPair[]): LabelTally[] {
  const counts = new Map<string, number>();
  for (const p of pairs) {
    const key = `${p.control}\u0000${p.arm}`;
    counts.set(key, (counts.get(key) || 0) + 1);
  }
  return [...counts.entries()]
    .map(([key, count]) => {
      const [from, to] = key.split("\u0000") as [string, string];
      return { from, to, count, same: from === to };
    })
    .sort((a, b) => b.count - a.count || a.from.localeCompare(b.from));
}

export interface PairedReading {
  diffBox?: BoxStats | null;
  diffs?: { trace_id: string; diff: number }[];
  matched?: number;
  rate?: number | null;
  tally?: LabelTally[] | null;
  overlap?: number | null;
  overlapBox?: BoxStats | null;
}

/**
 * What one side reads as against another over the cases both have a value
 * on. Treatments and the noise floor share it, so the two readings are always
 * measured the same way.
 */
export function pairedReading(pairs: readonly FieldPair[], kind: FieldKind): PairedReading {
  if (kind === "number") {
    return {
      diffBox: boxStats(pairs.map((p) => (p.arm as number) - (p.control as number))),
      diffs: pairs.map((p) => ({ trace_id: p.trace_id, diff: (p.arm as number) - (p.control as number) })),
    };
  }
  if (kind === "label" || kind === "text" || kind === "long_text") {
    const same = pairs.filter((p) => String(p.control).trim() === String(p.arm).trim()).length;
    const rate = pairs.length ? same / pairs.length : null;
    const tally = kind === "label" ? labelTally(pairs) : null;
    return { matched: same, rate, tally };
  }
  if (kind === "list") {
    const scores = pairs.map((p) => overlap(p.control, p.arm));
    return { overlap: mean(scores), overlapBox: boxStats(scores) };
  }
  return {};
}

export interface FieldComparison extends PairedReading {
  path: string;
  label: string;
  kind: FieldKind;
  pairs: number;
  missing: number;
  controlBox?: BoxStats | null;
  armBox?: BoxStats | null;
}

/** One arm against the control on one field. */
export function compareField(
  cases: Loose[] | null | undefined,
  path: string,
  label: string,
  fields?: Loose,
  sharedKind?: FieldKind,
): FieldComparison {
  const list = cases || [];
  const control = controlValues(list, path, fields);
  const arm = armValues(list, path, label, fields);
  const kind = sharedKind || fieldKind([...control, ...arm]);
  const pairs = fieldPairs(list, path, label, fields);
  const result = { path, label, kind, pairs: pairs.length, missing: list.length - pairs.length };
  if (kind === "number") return { ...result, controlBox: boxStats(control), armBox: boxStats(arm), ...pairedReading(pairs, kind) };
  return { ...result, ...pairedReading(pairs, kind) };
}

/**
 * Every arm on one field, judged by one kind read from the control and all
 * the arms together, so the order of the arms cannot change how any of them
 * is shown. When the series disagree on type, each arm falls back to its own
 * kind and the field as a whole reads as mixed.
 */
export function fieldComparisons(
  cases: Loose[] | null | undefined,
  path: string,
  labels: string[],
  fields?: Loose,
): { kind: FieldKind; comparisons: FieldComparison[] } {
  const list = cases || [];
  const all = [...controlValues(list, path, fields), ...labels.flatMap((label) => armValues(list, path, label, fields))];
  const shared = fieldKind(all);
  if (shared !== "mixed") return { kind: shared, comparisons: labels.map((label) => compareField(list, path, label, fields, shared)) };
  return { kind: "mixed", comparisons: labels.map((label) => compareField(list, path, label, fields)) };
}
