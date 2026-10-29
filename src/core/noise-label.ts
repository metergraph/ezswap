// Within-noise labels (opt-in): for each statistic and tracked field, is a
// treatment's gap from the control larger than the control's gap from
// itself (A2 against A1) on the same cases? A reading aid that says whether a
// difference stands out from replay-to-replay variation. It is never a
// verdict that a model is safe to switch to. docs/noise-floor.md describes
// the method.

import { type Loose, SPREAD_MIN, fieldComparisons, overlap, replayedIndexes, sideValue } from "./fields.ts";
import { codePointLength, pySorted } from "./py.ts";
import { isCutOff, isEmptyOrRefused, keysComplete, outputFacts, parseOutputJson } from "./summary.ts";
import type { ExperimentView } from "./view-types.ts";

/** Below this many paired cases a label would be mostly noise itself. */
export const LABEL_MIN_CASES = SPREAD_MIN;

export type NoiseLabelValue = "within_noise" | "beyond_noise" | "too_few_calls" | "not_comparable";

export interface NoiseLabel {
  /** A built-in statistic's key, or `field:<path>`. */
  key: string;
  label: NoiseLabelValue;
  /**
   * Which way the treatment moved, for beyond_noise: "higher" or "lower"
   * for numbers and rates, "different" for values with no order.
   */
  direction: "higher" | "lower" | "different" | null;
  /** How the gap is measured. */
  measure: "median_abs_difference" | "disagreement_rate" | "mean_non_overlap" | null;
  /** Paired cases: the treatment, its baseline and both floor replays all have a value. */
  cases: number;
  /** The treatment's gap from its baseline over those cases. */
  gap: number | null;
  /** The floor's gap (A2 against A1) over the same cases. */
  floor_gap: number | null;
  /** What the treatment is read against: production's output, or the floor's first replay. */
  against: "production" | "replayed_control";
}

type Side = "treatment" | "control" | "a1" | "a2";
type Values = Record<Side, unknown>;

interface PairedCase {
  control: Loose;
  treatment: Loose;
  a1: Loose;
  a2: Loose;
  index: number;
}

/** Replayed cases where the treatment and both floor replays returned. */
function pairedCases(view: Loose, armLabel: string): PairedCase[] {
  const cases: Loose[] = view?.cases || [];
  const indexes = replayedIndexes(cases);
  const out: PairedCase[] = [];
  cases.forEach((c, i) => {
    if (!(c.status == null || c.status === "replayed")) return;
    const treatment = c.arms?.[armLabel];
    const a1 = c.noise_floor?.a1;
    const a2 = c.noise_floor?.a2;
    if (!c.control || !treatment || treatment.error || !a1 || a1.error || !a2 || a2.error) return;
    out.push({ control: c.control, treatment, a1, a2, index: indexes[i] as number });
  });
  return out;
}

function median(values: number[]): number | null {
  if (!values.length) return null;
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? (sorted[mid] as number) : ((sorted[mid - 1] as number) + (sorted[mid] as number)) / 2;
}

function decide(
  key: string,
  against: NoiseLabel["against"],
  measure: NonNullable<NoiseLabel["measure"]>,
  n: number,
  gap: number | null,
  floorGap: number | null,
  direction: NoiseLabel["direction"],
): NoiseLabel {
  const base = { key, measure, cases: n, gap, floor_gap: floorGap, against };
  if (n < LABEL_MIN_CASES || gap == null || floorGap == null) return { ...base, label: "too_few_calls", direction: null };
  if (gap <= floorGap) return { ...base, label: "within_noise", direction: null };
  return { ...base, label: "beyond_noise", direction };
}

/** Numbers: the median size of the per-case difference, treatment against baseline and A2 against A1. */
function numberLabel(key: string, against: NoiseLabel["against"], rows: { t: number; c: number; a1: number; a2: number }[]): NoiseLabel {
  const gap = median(rows.map((r) => Math.abs(r.t - r.c)));
  const floorGap = median(rows.map((r) => Math.abs(r.a2 - r.a1)));
  const shift = median(rows.map((r) => r.t - r.c));
  const direction = shift == null || shift === 0 ? "different" : shift > 0 ? "higher" : "lower";
  return decide(key, against, "median_abs_difference", rows.length, gap, floorGap, direction);
}

/** Values compared for equality: the share of cases where the two sides disagree. */
function matchLabel(
  key: string,
  rows: { t: unknown; c: unknown; a1: unknown; a2: unknown }[],
  same: (x: unknown, y: unknown) => boolean,
  ordered: boolean,
): NoiseLabel {
  const n = rows.length;
  const gap = n ? rows.filter((r) => !same(r.t, r.c)).length / n : null;
  const floorGap = n ? rows.filter((r) => !same(r.a2, r.a1)).length / n : null;
  let direction: NoiseLabel["direction"] = "different";
  if (ordered) {
    const shift = rows.filter((r) => r.t === true).length - rows.filter((r) => r.c === true).length;
    direction = shift > 0 ? "higher" : shift < 0 ? "lower" : "different";
  }
  return decide(key, "production", "disagreement_rate", n, gap, floorGap, direction);
}

function present(values: Values): boolean {
  return values.treatment != null && values.control != null && values.a1 != null && values.a2 != null;
}

const NUMBER_STATS: Record<string, { key: string; read: (r: Loose) => unknown; against: NoiseLabel["against"] }> = {
  cost_usd: { key: "cost_per_call_usd", read: (r) => r.cost_usd, against: "replayed_control" },
  latency_ms: { key: "latency_ms_p50", read: (r) => r.latency_ms, against: "replayed_control" },
  output_tokens: { key: "output_tokens_p50", read: (r) => r.output_tokens, against: "production" },
  output_chars: { key: "output_chars_p50", read: (r) => (r.output_truncated ? null : codePointLength(r.output ?? "")), against: "production" },
};

const MATCH_STATS: Record<string, { key: string; read: (r: Loose) => unknown; ordered: boolean }> = {
  json_parse: { key: "json_parse_rate", read: (r) => parseOutputJson(r.output)[1], ordered: true },
  same_keys: {
    key: "same_keys_rate",
    read: (r) => {
      if (r.output_truncated) return null;
      const facts = outputFacts(r.output);
      return keysComplete(facts) ? JSON.stringify(pySorted(facts.keys ?? [])) : null;
    },
    ordered: false,
  },
  empty_or_refused: { key: "empty_or_refused_rate", read: (r) => isEmptyOrRefused(r.output, r.finish_reason, Boolean(r.refusal)), ordered: true },
  cut_off: { key: "cut_off_rate", read: (r) => isCutOff(r.finish_reason), ordered: true },
};

/** Labels for every built-in statistic, for one treatment. */
function statLabels(paired: PairedCase[]): NoiseLabel[] {
  const labels: NoiseLabel[] = [];
  for (const { key, read, against } of Object.values(NUMBER_STATS)) {
    const rows = paired
      .map((p) => {
        const baseline = against === "replayed_control" ? p.a1 : p.control;
        return { treatment: read(p.treatment), control: read(baseline), a1: read(p.a1), a2: read(p.a2) };
      })
      .filter((v) => present(v) && [v.treatment, v.control, v.a1, v.a2].every((x) => typeof x === "number" && Number.isFinite(x)))
      .map((v) => ({ t: v.treatment as number, c: v.control as number, a1: v.a1 as number, a2: v.a2 as number }));
    labels.push(numberLabel(key, against, rows));
  }
  for (const { key, read, ordered } of Object.values(MATCH_STATS)) {
    const rows = paired
      .map((p) => ({ treatment: read(p.treatment), control: read(p.control), a1: read(p.a1), a2: read(p.a2) }))
      .filter(present)
      .map((v) => ({ t: v.treatment, c: v.control, a1: v.a1, a2: v.a2 }));
    labels.push(matchLabel(key, rows, (x, y) => x === y, ordered));
  }
  return labels;
}

/** The label for one tracked field, read with the same kind and values the field's sentence uses. */
function fieldLabel(view: Loose, paired: PairedCase[], path: string, armLabel: string): NoiseLabel {
  const labels = (view?.arms || []).map((arm: Loose) => arm.label);
  const { kind } = fieldComparisons(view?.cases || [], path, labels, view?.fields);
  const floorSeries = view?.noise_floor?.fields?.[path];
  const rows = paired
    .map((p) => ({
      treatment: sideValue(p.treatment, path, view?.fields?.[path]?.[armLabel], p.index),
      control: sideValue(p.control, path, view?.fields?.[path]?.control, p.index),
      a1: sideValue(p.a1, path, floorSeries?.a1, p.index),
      a2: sideValue(p.a2, path, floorSeries?.a2, p.index),
    }))
    .filter(present);
  const key = `field:${path}`;
  if (kind === "number") {
    const numeric = rows.filter((v) => [v.treatment, v.control, v.a1, v.a2].every((x) => typeof x === "number"));
    return numberLabel(key, "production", numeric.map((v) => ({ t: v.treatment as number, c: v.control as number, a1: v.a1 as number, a2: v.a2 as number })));
  }
  if (kind === "label" || kind === "text" || kind === "long_text") {
    const same = (x: unknown, y: unknown) => String(x).trim() === String(y).trim();
    return matchLabel(key, rows.map((v) => ({ t: v.treatment, c: v.control, a1: v.a1, a2: v.a2 })), same, false);
  }
  if (kind === "list") {
    const lists = rows.filter((v) => [v.treatment, v.control, v.a1, v.a2].every(Array.isArray));
    const n = lists.length;
    const gap = n ? lists.reduce((s, v) => s + (1 - overlap(v.treatment, v.control)), 0) / n : null;
    const floorGap = n ? lists.reduce((s, v) => s + (1 - overlap(v.a2, v.a1)), 0) / n : null;
    return decide(key, "production", "mean_non_overlap", n, gap, floorGap, "different");
  }
  return { key, label: "not_comparable", direction: null, measure: null, cases: rows.length, gap: null, floor_gap: null, against: "production" };
}

/**
 * The within-noise label for every built-in statistic and tracked field, for
 * one treatment. Null when the run has no noise floor.
 */
export function noiseLabels(
  view: ExperimentView | Loose,
  armLabel: string,
  { tracked }: { tracked?: string[] } = {},
): { arm: string; stats: NoiseLabel[]; fields: NoiseLabel[] } | null {
  if (!view?.noise_floor) return null;
  const paired = pairedCases(view, armLabel);
  const paths: string[] = tracked ?? view.tracked_fields ?? [];
  return {
    arm: armLabel,
    stats: statLabels(paired),
    fields: paths.map((path) => fieldLabel(view, paired, path, armLabel)),
  };
}

/** One label: a built-in statistic's key (as the summary names it) or `field:<path>`. */
export function noiseLabel(view: ExperimentView | Loose, armLabel: string, key: string): NoiseLabel | null {
  const all = noiseLabels(view, armLabel, key.startsWith("field:") ? { tracked: [key.slice("field:".length)] } : { tracked: [] });
  if (!all) return null;
  return [...all.stats, ...all.fields].find((label) => label.key === key) ?? null;
}
