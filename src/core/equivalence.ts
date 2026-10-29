// Per treatment, what reads together when deciding a swap: each tracked
// field's shift, cost and latency against the control over the same cases,
// and how many cases that is. The same block hosted Metergraph returns;
// conformance/experiment_equivalence*.json pin it. Shown, never judged.

import { type FieldComparison, type Loose, SPREAD_MIN, fieldComparisons } from "./fields.ts";
import { formatCount, formatMs, formatPercent, formatSigned, formatUsd } from "./format.ts";
import {
  REPLAYED_CONTROL_LABEL,
  builtInRows,
  controlSummary,
  fieldSentence,
  middleHalvesOverlap,
  sameKeysCell,
  shiftSentence,
  statDelta,
} from "./stats.ts";
import type { FieldKind } from "./view-types.ts";

/** A plain note when a treatment returned on too few cases for a range. */
export function sampleNote(n: number | null | undefined): string | null {
  if (n == null || n >= SPREAD_MIN) return null;
  if (n === 0) return "No case has returned on both sides yet.";
  return `Only ${formatCount(n)} ${n === 1 ? "case" : "cases"}, too few for a range (a range needs ${SPREAD_MIN}).`;
}

export interface Against {
  value: number | null;
  control: number | null;
  delta: string | null;
  sentence: string;
}

const AGAINST_CONTROL: Record<string, { format: (v: number | null) => string; what: string; says: (v: string) => string }> = {
  cost_per_call_usd: { format: formatUsd, what: "cost per call", says: (v) => `costs ${v} a call` },
  latency_ms_p50: { format: formatMs, what: "latency", says: (v) => `takes a median ${v}` },
};

/** One built-in stat of an arm against the control over the cases the arm returned on. */
export function againstControl(experiment: Loose, arm: Loose, key: "cost_per_call_usd" | "latency_ms_p50"): Against {
  const { format, what, says } = AGAINST_CONTROL[key]!;
  const matched = arm?.control_summary;
  const value = arm?.summary?.[key] ?? null;
  const base = matched ? (matched[key] ?? null) : (controlSummary(experiment)?.[key] ?? null);
  const cases = matched ? (matched.cases ?? null) : (controlSummary(experiment)?.cases ?? null);
  const delta = statDelta(key, value, base);
  let sentence: string;
  if (value == null || base == null) {
    sentence = `${arm?.label} has no ${what} to compare with control yet.`;
  } else {
    const over = cases == null ? "" : ` over the same ${cases} ${cases === 1 ? "case" : "cases"}`;
    const shown = delta == null ? "" : ` (${delta === "same" ? "the same" : delta})`;
    sentence = `${arm.label} ${says(format(value))} against ${format(base)} for control${over}${shown}.`;
  }
  return { value, control: base, delta, sentence };
}

export interface FloorSummary {
  kind: FieldKind;
  pairs: number;
  matched: number | null;
  match_rate: number | null;
  mean_overlap: number | null;
  min_difference: number | null;
  max_difference: number | null;
}

/** The control against itself on one field, from the view's noise floor: aggregates only. */
function floorSummary(spread: Loose, kind: FieldKind): FloorSummary | null {
  if (!spread || !spread.pairs || spread.kind !== kind) return null;
  return {
    kind: spread.kind,
    pairs: spread.pairs,
    matched: spread.matched ?? null,
    match_rate: spread.match_rate ?? null,
    mean_overlap: spread.mean_overlap ?? null,
    min_difference: spread.min_difference ?? null,
    max_difference: spread.max_difference ?? null,
  };
}

// floorClause reads the page's floor shape; this is the summary in it.
function asFloor(summary: FloorSummary | null): Loose {
  if (!summary) return null;
  return {
    kind: summary.kind,
    pairs: summary.pairs,
    matched: summary.matched,
    overlap: summary.mean_overlap,
    diffBox: summary.min_difference == null || summary.max_difference == null ? null : { min: summary.min_difference, max: summary.max_difference },
  };
}

export interface FieldShift {
  path: string;
  kind: FieldKind;
  pairs: number;
  missing: number;
  matched: number | null;
  rate: number | null;
  overlap: number | null;
  median_difference: number | null;
  middle_halves_overlap: boolean | null;
  sentence: string;
  noise_floor: FloorSummary | null;
}

function fieldSummary(comparison: FieldComparison, floor: FloorSummary | null = null): FieldShift {
  const number = comparison.kind === "number" && comparison.pairs > 0;
  return {
    path: comparison.path,
    kind: comparison.kind,
    pairs: comparison.pairs,
    missing: comparison.missing,
    matched: comparison.matched ?? null,
    rate: comparison.rate ?? null,
    overlap: comparison.overlap ?? null,
    median_difference: number ? (comparison.diffBox as { median: number }).median : null,
    middle_halves_overlap: number ? middleHalvesOverlap(comparison) : null,
    sentence: fieldSentence(comparison, asFloor(floor)),
    noise_floor: floor,
  };
}

/** The control replayed the same way as a treatment: a paid column, else the noise floor's first replay. */
function replaySummary(experiment: Loose): Loose {
  const arm = (experiment?.arms || []).find((a: Loose) => a.label === REPLAYED_CONTROL_LABEL && a.summary);
  return arm ? arm.summary : experiment?.noise_floor?.a1 || null;
}

function againstReplay(arm: Loose, key: "cost_per_call_usd" | "latency_ms_p50", replay: Loose): Against {
  const { format, what, says } = AGAINST_CONTROL[key]!;
  const value = arm?.summary?.[key] ?? null;
  const base = replay?.[key] ?? null;
  const delta = statDelta(key, value, base, "replay");
  const sentence =
    value == null || base == null
      ? `${arm?.label} has no ${what} to compare with the replayed control yet.`
      : `${arm.label} ${says(format(value))} against ${format(base)} for the control replayed through the same gateway${delta == null ? "" : ` (${delta === "same" ? "the same" : delta})`}.`;
  return { value, control: base, delta, sentence };
}

function unreplayedLatency(arm: Loose): Against {
  const value = arm?.summary?.latency_ms_p50 ?? null;
  const sentence =
    value == null
      ? `${arm?.label} has no latency yet.`
      : `${arm.label} takes a median ${formatMs(value)}. Production's recorded latency is not timed the way a replay is, so it is not compared; replay the control to compare latency.`;
  return { value, control: null, delta: null, sentence };
}

export interface TreatmentEquivalence {
  label: string;
  cases: number | null;
  sample_note: string | null;
  cost: Against;
  latency: Against;
  fields: FieldShift[];
  similarity: { median: number | null; n: number | null; control_median: number | null } | null;
}

/**
 * Per treatment: cost and latency against the control replayed the same way
 * (or, without one, cost against production and latency alone), and each
 * tracked field's shift with the noise floor's gap beside it.
 */
export function equivalence(experiment: Loose): { treatments: TreatmentEquivalence[] } {
  const cases = experiment?.cases || [];
  const arms = experiment?.arms || [];
  const labels: string[] = arms.map((arm: Loose) => arm.label);
  const tracked: string[] = experiment?.tracked_fields || [];
  const byPath = tracked.map((path) => fieldComparisons(cases, path, labels, experiment?.fields).comparisons);
  const floorFields = experiment?.noise_floor?.fields || {};
  const replay = replaySummary(experiment);
  const similarity = experiment?.similarity?.columns || {};
  const controlSimilarity = similarity.control || {};
  const treatments: TreatmentEquivalence[] = [];
  arms.forEach((arm: Loose, i: number) => {
    // The replayed control is the baseline, not a treatment.
    if (arm.label === REPLAYED_CONTROL_LABEL) return;
    const n = arm.control_summary?.cases ?? null;
    const column = similarity[arm.label] || {};
    treatments.push({
      label: arm.label,
      cases: n,
      sample_note: sampleNote(n),
      cost: replay ? againstReplay(arm, "cost_per_call_usd", replay) : againstControl(experiment, arm, "cost_per_call_usd"),
      latency: replay ? againstReplay(arm, "latency_ms_p50", replay) : unreplayedLatency(arm),
      fields: byPath.map((comparisons, j) => {
        const comparison = comparisons[i] as FieldComparison;
        const kind = comparison.kind;
        return fieldSummary(comparison, kind === "mixed" ? null : floorSummary(floorFields[tracked[j] as string], kind));
      }),
      similarity: column.n ? { median: column.median ?? null, n: column.n ?? null, control_median: controlSimilarity.median ?? null } : null,
    });
  });
  return { treatments };
}

// --- The summary table ----------------------------------------------------------

/** Below this many cases with a result a range is mostly noise, so the row says so. */
export const SUMMARY_N_MIN = SPREAD_MIN;

export function fewTracesNote(n: number | null): string | null {
  if (n == null || n >= SUMMARY_N_MIN) return null;
  return `${n === 1 ? "1 trace" : `${formatCount(n)} traces`} returned a result. Ranges need at least ${SUMMARY_N_MIN}, so none are drawn for this treatment.`;
}

export interface SummaryCell {
  text: string;
  sub?: string;
  delta?: string | null;
  title?: string;
  floor?: { text: string; title: string };
}

/** One tracked field's shift for one arm, in the fewest words that keep the field's own reading. */
function fieldShift(c: FieldComparison | undefined): SummaryCell {
  if (!c || !c.pairs) return { text: "—", title: "No case has a value on both sides yet." };
  const title = shiftSentence(c);
  if (c.kind === "number") return { text: formatSigned(c.diffBox?.median), sub: "median", title };
  if (c.kind === "label") return { text: `${formatCount(c.matched)} of ${formatCount(c.pairs)}`, sub: "same label", title };
  if (c.kind === "text" || c.kind === "long_text") return { text: `${formatCount(c.matched)} of ${formatCount(c.pairs)}`, sub: "exact match", title };
  if (c.kind === "list") return { text: formatPercent(c.overlap), sub: "items shared", title };
  return { text: "—", title };
}

function sameAsControl(delta: string | null | undefined): string | null {
  return delta === "same" ? "same as control" : delta || null;
}

/** How many of the replayed cases gave this arm a result. */
function armN(arm: Loose): number | null {
  const s = arm?.summary;
  if (s?.cases == null) return null;
  return Math.max(0, s.cases - (s.errors || 0));
}

/**
 * The summary at the top of the results: one row per treatment, one figure
 * per thing a swap is decided on, each the treatment's shift from the control
 * and, when given, the control's shift from itself (`floors`, from
 * noiseFloors). Cost and latency close the row. Every cell names the detail it
 * summarises (`target`). The hosted judge and similarity columns are not in v1.
 */
export function equivalenceSummary(
  experiment: Loose,
  { tracked = [], floors = {} }: { tracked?: string[]; floors?: Record<string, { text: string; title: string }> } = {},
): Loose {
  const arms = experiment?.arms || [];
  const treatments = arms.filter((arm: Loose) => !arm.replayed_control);
  if (!treatments.length) return null;
  const labels: string[] = arms.map((arm: Loose) => arm.label);
  const cases = experiment.cases || [];
  const builtIn = Object.fromEntries(builtInRows(experiment).map((row) => [row.key, row]));
  const hasKeys = treatments.some((arm: Loose) => arm.summary?.same_keys_rate != null);
  const fields = tracked.map((path) => ({ path, ...fieldComparisons(cases, path, labels, experiment.fields) }));

  const columns = [
    ...fields.map(({ path }) => ({ key: `field:${path}`, label: path, mono: true, target: { field: path } })),
    hasKeys && { key: "same_keys_rate", label: "JSON keys kept", target: { stat: "same_keys_rate" } },
    { key: "output_chars_p50", label: "Output length", target: { stat: "output_chars_p50" } },
    { key: "cost_per_call_usd", label: "Cost per call", target: { stat: "cost_per_call_usd" }, group: "cost" },
    { key: "latency_ms_p50", label: "Latency", target: { stat: "latency_ms_p50" }, group: "cost" },
  ].filter(Boolean) as { key: string; label: string }[];

  const rows = arms
    .map((arm: Loose, i: number) => {
      const cells: Record<string, SummaryCell> = {};
      for (const { path, comparisons } of fields) cells[`field:${path}`] = fieldShift(comparisons[i]);
      const builtInCell = (key: string): SummaryCell => {
        const cell = builtIn[key]?.arms?.[i];
        return cell ? { text: cell.text, delta: sameAsControl(cell.delta) } : { text: "—" };
      };
      if (hasKeys) cells.same_keys_rate = sameKeysCell(arm.summary) || { text: "—" };
      cells.output_chars_p50 = { ...builtInCell("output_chars_p50"), title: "Median output length in characters" };
      cells.cost_per_call_usd = { ...builtInCell("cost_per_call_usd"), title: "Mean cost per call" };
      cells.latency_ms_p50 = { ...builtInCell("latency_ms_p50"), title: "Median latency" };
      for (const column of columns) {
        const floor = floors?.[column.key];
        const cell = cells[column.key];
        if (floor && cell) cell.floor = floor;
      }
      const n = armN(arm);
      return { label: arm.label, model: arm.model, index: i, n, note: fewTracesNote(n), cells };
    })
    .filter((_row: Loose, i: number) => !arms[i].replayed_control);

  return { columns, rows, floored: columns.some((column) => floors?.[column.key]) };
}
