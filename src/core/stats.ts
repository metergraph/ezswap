// The built-in stat rows, spreads, monthly projection, the noise floor's
// readings and the one sentence a tracked field may carry. JavaScript
// semantics; matches Metergraph's hosted figures (conformance/page.json).
// Nothing here grades a treatment or says whether a swap is safe.

import {
  type BoxStats,
  type FieldComparison,
  type FieldPair,
  type Loose,
  SPREAD_MIN,
  boxStats,
  fieldComparisons,
  fieldKind,
  mean,
  outputText,
  pairedReading,
  parseOutput,
  quantile,
  replayedIndexes,
  sideValue,
} from "./fields.ts";
import { formatCachedShare, formatCount, formatMs, formatPercent, formatSigned, formatUsd } from "./format.ts";
import { isEmptyOrRefused } from "./summary.ts";
import type { FieldKind } from "./view-types.ts";

/** A column labelled this is production's model replayed as a paid arm: the baseline for cost and latency. */
export const REPLAYED_CONTROL_LABEL = "Control, replayed";

/**
 * The control's summary: the view's own, or, when it has none, what the
 * cases' production calls tell. A reply counts as empty or refused on the
 * refusal flag, a refusal finish reason, or blank text.
 */
export function controlSummary(experiment: Loose): Loose {
  if (experiment?.control?.summary) return experiment.control.summary;
  const cases = (experiment?.cases || []).filter((c: Loose) => c.control);
  if (!cases.length) return null;
  const parses = cases.map((c: Loose) => parseOutput(c.control.output).ok);
  return {
    cases: cases.length,
    errors: 0,
    cost_per_call_usd: mean(cases.map((c: Loose) => c.control.cost_usd)),
    model_cost_per_call_usd: mean(
      cases.map((c: Loose) => (c.control.cost_search_usd == null || c.control.cost_usd == null ? null : c.control.cost_usd - c.control.cost_search_usd)),
    ),
    search_cost_per_call_usd: mean(cases.map((c: Loose) => c.control.cost_search_usd)),
    latency_ms_p50: boxStats(cases.map((c: Loose) => c.control.latency_ms))?.median ?? null,
    output_tokens_p50: boxStats(cases.map((c: Loose) => c.control.output_tokens))?.median ?? null,
    json_parse_rate: parses.filter(Boolean).length / cases.length,
    same_keys_rate: null,
    empty_or_refused_rate:
      cases.filter((c: Loose) => isEmptyOrRefused(c.control.output, c.control.finish_reason, Boolean(c.control.refusal))).length / cases.length,
    cut_off_rate: null,
  };
}

export interface StatRowDef {
  key: string;
  label: string;
  format: (value: number | null | undefined) => string;
}

export const STAT_ROWS: StatRowDef[] = [
  { key: "cost_per_call_usd", label: "Cost per call", format: formatUsd },
  // The cost split: shown when any column has search fees.
  { key: "model_cost_per_call_usd", label: "Model cost per call", format: formatUsd },
  { key: "search_cost_per_call_usd", label: "Search fees per call", format: formatUsd },
  { key: "cached_input_share", label: "Input tokens from cache", format: formatCachedShare },
  { key: "latency_ms_p50", label: "Latency, median", format: formatMs },
  { key: "output_tokens_p50", label: "Output tokens, median", format: formatCount },
  { key: "output_chars_p50", label: "Output length, median characters", format: formatCount },
  { key: "json_parse_rate", label: "JSON parses", format: formatPercent },
  { key: "same_keys_rate", label: "Same keys as control", format: formatPercent },
  { key: "empty_or_refused_rate", label: "Empty or refused", format: formatPercent },
  { key: "cut_off_rate", label: "Cut off at token limit", format: formatPercent },
  { key: "errors", label: "Errors", format: formatCount },
];

/** The stats read in two groups: what a call costs and how long it takes, then the output's shape. */
export const STAT_GROUPS = [
  {
    title: "Cost and speed",
    keys: ["monthly_cost_usd", "cost_per_call_usd", "model_cost_per_call_usd", "search_cost_per_call_usd", "cached_input_share", "latency_ms_p50", "output_tokens_p50", "output_chars_p50"],
  },
  { title: "Output shape", keys: ["json_parse_rate", "same_keys_rate", "empty_or_refused_rate", "cut_off_rate", "errors"] },
];

// Production's recorded latency is timed by whatever logged the call, and its
// cost reflects production's own caching: neither is measured the way a
// replay is. So cost, cache share and latency are read against the control
// replayed the same way when the run has it, and latency is never read
// against production's recording.
const REPLAY_BASELINE_KEYS = new Set(["cost_per_call_usd", "model_cost_per_call_usd", "search_cost_per_call_usd", "cached_input_share", "latency_ms_p50"]);
const COST_SPLIT_KEYS = new Set(["model_cost_per_call_usd", "search_cost_per_call_usd"]);

/** Whether the cost split has anything to show: a column whose calls billed search fees. */
export function costSplitShown(experiment: Loose): boolean {
  const arms = experiment?.arms || [];
  const summaries = [controlSummary(experiment), ...arms.map((arm: Loose) => arm.summary)];
  return summaries.some((s) => s?.search_cost_per_call_usd > 0);
}

/**
 * Where the replayed control comes from. By default only a paid "Control,
 * replayed" column counts (the behaviour conformance/page.json pins). A replay
 * run measures the same thing as the noise floor's first replay, A1, so
 * `floorAsReplay: true` reads A1 as the replayed control when the run has no
 * such column, as `equivalence` already does.
 */
export interface BaselineOptions {
  floorAsReplay?: boolean;
}

export function replayBaseline(experiment: Loose, { floorAsReplay = false }: BaselineOptions = {}): Loose {
  const arm = (experiment?.arms || []).find((a: Loose) => a.label === REPLAYED_CONTROL_LABEL);
  if (arm?.summary) return arm;
  const a1 = floorAsReplay ? noiseFloor(experiment)?.a1 : null;
  return a1 ? { label: "Noise floor A1", summary: a1, floor: true } : null;
}

/** A shift against a baseline in words: points for rates, a ratio otherwise. Null when there is none to give. */
export function statDelta(key: string, value: number | null, base: number | null, against = "control"): string | null {
  if (value == null || base == null) return null;
  if (key === "errors" || key === "same_keys_rate") return null;
  if (key.endsWith("_rate") || key === "cached_input_share") {
    const points = Math.round((value - base) * 100);
    return points === 0 ? "same" : `${points > 0 ? "+" : "−"}${Math.abs(points)} pts`;
  }
  if (base === 0) return null;
  const ratio = value / base;
  if (Math.abs(ratio - 1) < 0.005) return "same";
  return ratio >= 1 ? `${ratio.toFixed(ratio >= 10 ? 0 : 1)}× ${against}` : `${Math.round((1 - ratio) * 100)}% lower`;
}

/**
 * Same keys is counted only where the control returned a whole JSON object,
 * which can be a few of the cases. Then the figure says how many it is over
 * ("0 of 1"), so one case never reads as a rate for the run.
 */
export function sameKeysCell(summary: Loose): { text: string; sub?: string; title: string } | null {
  const rate = summary?.same_keys_rate ?? null;
  const compared = summary?.same_keys_compared ?? null;
  if (rate == null) return null;
  const title = "Outputs whose JSON has the same top-level keys as the control’s, over the traces where the control returned a JSON object";
  if (compared == null || summary?.cases == null || compared >= summary.cases) return { text: formatPercent(rate), title };
  const kept = Math.round(rate * compared);
  return { text: `${formatCount(kept)} of ${formatCount(compared)}`, sub: `${compared === 1 ? "trace" : "traces"} with JSON`, title };
}

function narrowerBase(matched: Loose, control: Loose): boolean {
  return Boolean(matched) && matched.cases != null && matched.cases !== control?.cases;
}

/**
 * The control's own gap on a built-in stat: its second replay against its
 * first, in the same terms a treatment's gap is given.
 */
export function floorStat(row: StatRowDef, floor: Loose): { text?: string; delta: string; title: string } | null {
  const value = floor.summary?.[row.key] ?? null;
  const base = floor.control_summary?.[row.key] ?? null;
  const title = `The control's model replayed twice on ${formatCount(floor.cases)} cases, the second replay against the first`;
  if (row.key === "same_keys_rate") return value == null ? null : { text: row.format(value), delta: "against itself", title };
  const delta = statDelta(row.key, value, base, "itself");
  if (!delta) return null;
  return { delta: delta.endsWith("itself") ? delta : `${delta} against itself`, title };
}

export interface StatCell {
  label: string;
  text: string;
  delta: string | null;
  title?: string;
  sameCases?: number;
  sameBase?: string;
}

export interface StatRow {
  key: string;
  label: string;
  sublabel?: string | null;
  control: string;
  floor?: { text?: string; delta: string; title: string } | null;
  arms: StatCell[];
  note?: { text: string; typical: boolean | null } | null;
}

export function builtInRows(experiment: Loose, baseline: BaselineOptions = {}): StatRow[] {
  const control = controlSummary(experiment);
  const arms = experiment?.arms || [];
  const floor = noiseFloor(experiment);
  const replay = replayBaseline(experiment, baseline);
  const split = costSplitShown(experiment);
  return STAT_ROWS.filter((row) => split || !COST_SPLIT_KEYS.has(row.key)).map((row) => {
    const base = control?.[row.key] ?? null;
    return {
      key: row.key,
      label: row.label,
      control: row.format(base),
      ...(floor ? { floor: floorStat(row, floor) } : {}),
      // An arm is compared with the control over the cases it has results
      // for; when that covers other cases than the control column, the cell
      // carries those cases and the control's figure over them.
      arms: arms.map((arm: Loose): StatCell => {
        const value = arm.summary?.[row.key] ?? null;
        if (REPLAY_BASELINE_KEYS.has(row.key) && replay && arm !== replay) {
          return { label: arm.label, text: row.format(value), delta: statDelta(row.key, value, replay.summary[row.key] ?? null, "replay") };
        }
        if (row.key === "latency_ms_p50") return { label: arm.label, text: row.format(value), delta: null };
        if (row.key === "same_keys_rate") {
          const cell = sameKeysCell(arm.summary);
          return { label: arm.label, text: cell ? cell.text : row.format(value), delta: cell?.sub ?? null, ...(cell ? { title: cell.title } : {}) };
        }
        const matched = arm.control_summary;
        const own = matched ? (matched[row.key] ?? null) : base;
        const delta = statDelta(row.key, value, own);
        const narrower = Boolean(delta) && narrowerBase(matched, control);
        return { label: arm.label, text: row.format(value), delta, ...(narrower ? { sameCases: matched.cases, sameBase: row.format(own) } : {}) };
      }),
    };
  });
}

/**
 * One row per built-in stat: the control's value, then each arm's value and
 * its ratio or difference against the control where both exist. Given a
 * monthly volume, a monthly cost row follows cost per call.
 */
export function statRows(
  experiment: Loose,
  { volume = null, floorAsReplay = false }: { volume?: MonthlyVolume | null } & BaselineOptions = {},
): StatRow[] {
  const rows = builtInRows(experiment, { floorAsReplay });
  const monthly = monthlyRow(experiment, volume, { floorAsReplay });
  if (monthly) rows.splice(rows.findIndex((row) => row.key === "cost_per_call_usd") + 1, 0, monthly);
  return rows;
}

// --- Monthly cost -------------------------------------------------------------

/** Calls a month, and optionally what they cost, that the projection multiplies by. */
export interface MonthlyVolume {
  calls: number;
  spendUsd?: number | null;
  /** The sample is a slice of the volume (filtered by label). */
  labelled?: boolean;
}

export function monthlyDelta(value: number | null, base: number | null): string | null {
  if (value == null || base == null) return null;
  const diff = value - base;
  if (base !== 0 && Math.abs(diff / base) < 0.005) return "same";
  return diff < 0 ? `saves ${formatUsd(-diff)}/mo` : `adds ${formatUsd(diff)}/mo`;
}

/** The range the sample's mean cost per call could plausibly take: about two standard errors either side. */
export function meanRange(values: readonly unknown[]): { low: number; high: number } | null {
  const nums = values.filter((v) => typeof v === "number" && Number.isFinite(v)) as number[];
  if (nums.length < 2) return null;
  const m = nums.reduce((s, v) => s + v, 0) / nums.length;
  const sd = Math.sqrt(nums.reduce((s, v) => s + (v - m) ** 2, 0) / (nums.length - 1));
  const half = Math.max((2 * sd) / Math.sqrt(nums.length), m * 0.005);
  return { low: m - half, high: m + half };
}

/**
 * What the monthly figures rest on (`basis`), and whether the control's
 * projection matches what the calls actually cost (`text`).
 */
export function monthlyNote(
  experiment: Loose,
  volume: MonthlyVolume | null | undefined,
  controlPerCall: number | null,
): { basis: string; text: string | null; typical: boolean | null } | null {
  const calls = volume?.calls;
  if (calls == null || !Number.isFinite(calls) || calls <= 0) return null;
  const basis = `${formatCount(calls)} calls in 30 days${volume?.labelled ? ", all labels" : ""}`;
  const spend = volume?.spendUsd;
  if (controlPerCall == null || spend == null || !Number.isFinite(spend)) return { basis, text: null, typical: null };
  const range = meanRange((experiment?.cases || []).map((c: Loose) => c.control?.cost_usd));
  if (!range) return { basis, text: null, typical: null };
  const projected = controlPerCall * calls;
  const average = spend / calls;
  if (average >= range.low && average <= range.high) {
    return { basis, text: `The control's ${formatUsd(projected)} is in line with the ${formatUsd(spend)} these calls actually cost.`, typical: true };
  }
  return {
    basis,
    text: `The sampled calls cost ${controlPerCall < average ? "less" : "more"} than the average call (${formatUsd(controlPerCall)} against ${formatUsd(average)}), so compare the treatments with each other, not with your bill.`,
    typical: false,
  };
}

/** Each arm's measured cost per call times the calls a month. Null with no volume: never estimated. */
export function monthlyRow(experiment: Loose, volume: MonthlyVolume | null | undefined, baseline: BaselineOptions = {}): StatRow | null {
  const calls = volume?.calls;
  if (calls == null || !Number.isFinite(calls) || calls <= 0) return null;
  const control = controlSummary(experiment);
  const base = control?.cost_per_call_usd ?? null;
  const monthly = (perCall: number | null) => (perCall == null ? null : perCall * calls);
  const note = monthlyNote(experiment, volume, base);
  const replay = replayBaseline(experiment, baseline);
  return {
    key: "monthly_cost_usd",
    label: "Monthly cost",
    sublabel: note?.basis ?? null,
    control: formatUsd(monthly(base)),
    arms: (experiment?.arms || []).map((arm: Loose): StatCell => {
      const value = monthly(arm.summary?.cost_per_call_usd ?? null);
      if (replay && arm !== replay) {
        return { label: arm.label, text: formatUsd(value), delta: monthlyDelta(value, monthly(replay.summary.cost_per_call_usd ?? null)) };
      }
      const matched = arm.control_summary;
      const own = monthly(matched ? (matched.cost_per_call_usd ?? null) : base);
      const delta = monthlyDelta(value, own);
      const narrower = Boolean(delta) && delta !== "same" && narrowerBase(matched, control);
      return { label: arm.label, text: formatUsd(value), delta, ...(narrower ? { sameCases: matched.cases, sameBase: formatUsd(own) } : {}) };
    }),
    note: note?.text ? { text: note.text, typical: note.typical } : null,
  };
}

// --- Spread -------------------------------------------------------------------

/** Output length in code points; unknown for a stored prefix. */
function outputChars(result: Loose): number | undefined {
  if (result?.output_truncated) return undefined;
  return [...outputText(result?.output)].length;
}

interface SpreadRowDef {
  key: string;
  value: (result: Loose) => unknown;
  center: "mean" | "median";
  format: (value: number | null | undefined) => string;
}

// The tick sits at the figure the row prints: cost per call is a mean (a bill is a sum), the others medians.
const SPREAD_ROWS: SpreadRowDef[] = [
  { key: "cost_per_call_usd", value: (r) => r.cost_usd, center: "mean", format: formatUsd },
  { key: "latency_ms_p50", value: (r) => r.latency_ms, center: "median", format: formatMs },
  { key: "output_tokens_p50", value: (r) => r.output_tokens, center: "median", format: formatCount },
  { key: "output_chars_p50", value: outputChars, center: "median", format: formatCount },
];

export const SPREAD_KEYS = SPREAD_ROWS.map((row) => row.key);

export interface SpreadSide {
  n: number;
  range: { low: number; high: number; center: number } | null;
  text: string | null;
  note: string | null;
}

export function spreadOf(values: readonly unknown[], { center, format }: Pick<SpreadRowDef, "center" | "format">): SpreadSide {
  const sorted = (values.filter((v) => typeof v === "number" && Number.isFinite(v)) as number[]).sort((a, b) => a - b);
  const n = sorted.length;
  if (n < SPREAD_MIN) {
    return { n, range: null, text: null, note: n ? `Only ${formatCount(n)} ${n === 1 ? "call" : "calls"}, too few for a range` : null };
  }
  const low = quantile(sorted, 0.1) as number;
  const high = quantile(sorted, 0.9) as number;
  const mid = (center === "mean" ? mean(sorted) : quantile(sorted, 0.5)) as number;
  return { n, range: { low, high, center: mid }, text: `${format(low)} to ${format(high)}`, note: null };
}

/**
 * Each spread row's 10th to 90th percentile per side, over the replayed
 * cases: the control's production calls, and each arm's calls that returned.
 * Every side in a row shares one scale from zero to the row's highest value.
 */
export function statSpreads(experiment: Loose): Record<string, { control: SpreadSide; arms: Record<string, SpreadSide>; scale: { lo: number; hi: number } | null; scaleText: string | null }> {
  const cases = (experiment?.cases || []).filter((c: Loose) => c.status == null || c.status === "replayed");
  const labels: string[] = (experiment?.arms || []).map((arm: Loose) => arm.label);
  const controls = cases.map((c: Loose) => c.control).filter(Boolean);
  const results = Object.fromEntries(labels.map((label) => [label, cases.map((c: Loose) => c.arms?.[label]).filter((r: Loose) => r && !r.error)]));
  return Object.fromEntries(
    SPREAD_ROWS.map((row) => {
      const control = spreadOf(controls.map(row.value), row);
      const arms = Object.fromEntries(labels.map((label) => [label, spreadOf(results[label].map(row.value), row)]));
      const drawn = [control, ...Object.values(arms)].filter((side) => side.range);
      const hi = Math.max(0, ...drawn.flatMap((side) => [side.range!.high, side.range!.center]));
      const scale = drawn.length ? { lo: 0, hi: hi || 1 } : null;
      return [row.key, { control, arms, scale, scaleText: hi > 0 ? `Bars from zero to ${row.format(hi)}` : null }];
    }),
  );
}

// --- Noise floor --------------------------------------------------------------

/** The view's noise floor, or null when the run did not measure one. */
export function noiseFloor(experiment: Loose): Loose {
  const floor = experiment?.noise_floor;
  return floor && typeof floor === "object" ? floor : null;
}

export const FLOOR_LABEL = "Control vs itself";

export interface FloorReading extends ReturnType<typeof pairedReading> {
  path: string;
  label: string;
  kind: FieldKind;
  pairs: number;
}

/**
 * The control against itself on one field: the second replay read against
 * the first, with the same reading a treatment gets against the control.
 * Null when the run has no floor or no case has both replays' values.
 */
export function floorField(experiment: Loose, path: string, kind?: FieldKind | null): FloorReading | null {
  const floor = noiseFloor(experiment);
  if (!floor) return null;
  const cases = experiment?.cases || [];
  const indexes = replayedIndexes(cases);
  const series = floor.fields?.[path];
  const pairs = cases
    .map((c: Loose, i: number): FieldPair | null => {
      const replays = c.noise_floor;
      if (!replays || replays.a1?.error || replays.a2?.error) return null;
      return {
        trace_id: c.trace_id,
        control: sideValue(replays.a1, path, series?.a1, indexes[i] as number),
        arm: sideValue(replays.a2, path, series?.a2, indexes[i] as number),
      };
    })
    .filter((p: FieldPair | null): p is FieldPair => Boolean(p) && p!.control !== undefined && p!.arm !== undefined);
  if (!pairs.length) return null;
  const own = kind || fieldKind(pairs.flatMap((p: FieldPair) => [p.control, p.arm]));
  return { path, label: FLOOR_LABEL, kind: own, pairs: pairs.length, ...pairedReading(pairs, own) };
}

/** The control's per-call difference from itself, both ends to one precision. */
export function floorRange(d: { min: number; max: number }): string {
  const abs = Math.max(Math.abs(d.min), Math.abs(d.max));
  const fixed = (v: number) => v.toFixed(abs >= 100 ? 0 : abs >= 10 ? 1 : 2);
  return `${formatSigned(d.min, fixed)} to ${formatSigned(d.max, fixed)}`;
}

export function floorFieldText(f: Loose): string | null {
  if (f.kind === "number") return f.diffBox ? floorRange(f.diffBox) : null;
  if (f.kind === "label") return `${formatPercent(f.rate)} agreement`;
  if (f.kind === "text" || f.kind === "long_text") return `${formatPercent(f.rate)} exact match`;
  if (f.kind === "list") return `${formatPercent(f.overlap)} item overlap`;
  return null;
}

/**
 * Every floor the run measured, one per metric, to show beside each
 * treatment's figure: `field:<path>` for each tracked field, and some
 * built-in stats by key. A metric with nothing measured is left out.
 */
export function noiseFloors(experiment: Loose, { tracked = [] }: { tracked?: string[] } = {}): Record<string, { text: string; title: string }> {
  const floor = noiseFloor(experiment);
  if (!floor) return {};
  const title = (n: number) => `Control against its own replay over ${formatCount(n)} ${n === 1 ? "trace" : "traces"}`;
  const out: Record<string, { text: string; title: string }> = {};
  const labels = (experiment?.arms || []).map((arm: Loose) => arm.label);
  for (const path of tracked) {
    const { kind } = fieldComparisons(experiment?.cases || [], path, labels, experiment?.fields);
    const f = floorField(experiment, path, kind === "mixed" ? null : kind);
    const text = f && floorFieldText(f);
    if (f && text) out[`field:${path}`] = { text, title: title(f.pairs) };
  }
  for (const row of STAT_ROWS) {
    if (!["same_keys_rate", "output_chars_p50", "cost_per_call_usd", "latency_ms_p50"].includes(row.key)) continue;
    const stat = floorStat(row, floor);
    if (stat) out[row.key] = { text: stat.text ? `${stat.text} ${stat.delta}` : stat.delta, title: title(floor.cases) };
  }
  return out;
}

/** The clause the shift sentence adds for the floor, in the treatment's own terms; empty when nothing compares. */
export function floorClause(floor: Loose, kind: FieldKind): string {
  if (!floor || !floor.pairs || floor.kind !== kind) return "";
  if (kind === "number") {
    const d = floor.diffBox;
    return d ? `the control differs from itself by ${floorRange(d)}` : "";
  }
  if (kind === "label") return `the control gives the same label as itself on ${floor.matched} of ${floor.pairs}`;
  if (kind === "text" || kind === "long_text") return `the control matches itself exactly on ${floor.matched} of ${floor.pairs}`;
  if (kind === "list") return `the control shares ${formatPercent(floor.overlap)} with itself`;
  return "";
}

/**
 * The one sentence a tracked field may carry: shift and overlap, never a
 * verdict. With a noise floor it names the control's own gap beside the
 * treatment's instead of the overlap of the two middle halves.
 */
export function shiftSentence(comparison: Loose, floor: Loose = null): string {
  if (!comparison || !comparison.pairs) return "No case has a value on both sides yet.";
  const { label, kind, pairs } = comparison;
  const clause = floorClause(floor, kind);
  if (kind === "number") {
    const d = comparison.diffBox;
    if (clause) return `${label} differs from control by a median ${formatSigned(d.median)} per call across ${pairs} cases; ${clause}.`;
    const overlaps = middleHalvesOverlap(comparison);
    return `${label} differs from control by a median ${formatSigned(d.median)} per call across ${pairs} cases; the middle halves of the two ${overlaps ? "overlap" : "do not overlap"}.`;
  }
  const tail = clause ? `; ${clause}.` : ".";
  if (kind === "label") return `${label} gives the same label as control on ${comparison.matched} of ${pairs} cases${tail}`;
  if (kind === "text" || kind === "long_text") return `${label} matches control exactly on ${comparison.matched} of ${pairs} cases${tail}`;
  if (kind === "list") return `${label} shares ${formatPercent(comparison.overlap)} of list items with control on average across ${pairs} cases${tail}`;
  return `${label} returns values of more than one type here, so they are listed per case only.`;
}

/** Whether the control's and the arm's interquartile ranges meet. */
export function middleHalvesOverlap(comparison: { controlBox?: BoxStats | null; armBox?: BoxStats | null }): boolean {
  const c = comparison.controlBox;
  const a = comparison.armBox;
  return Boolean(c && a && a.q1 <= c.q3 && c.q1 <= a.q3);
}

/** The shift sentence, then how many cases had no value on one side. */
export function fieldSentence(comparison: FieldComparison | Loose, floor?: Loose): string {
  const missing =
    comparison?.missing > 0 ? ` ${comparison.missing} case${comparison.missing === 1 ? "" : "s"} had no value on one side.` : "";
  return `${shiftSentence(comparison, floor)}${missing}`;
}
