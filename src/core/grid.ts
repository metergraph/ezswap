// Ranges and the treatments grid: a seeded percentile bootstrap for every
// figure, models as rows by setting variants as columns, and the cost
// against quality frontier. JavaScript semantics; the ranges match
// Metergraph's hosted figures (conformance/page.json). A range says how far
// a figure could move with other calls from the same traffic; it is a range,
// never a verdict.

import { type Loose, fieldComparisons, fieldPairs, overlap, quantile } from "./fields.ts";
import { formatCount, formatMs, formatPercent, formatSigned, formatUsd } from "./format.ts";
import { REPLAYED_CONTROL_LABEL } from "./stats.ts";

/** A small deterministic generator (mulberry32), so a range is the same on every render. */
export function seeded(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function mean(values: readonly number[]): number | null {
  return values.length ? values.reduce((s, v) => s + v, 0) / values.length : null;
}

export function median(values: readonly number[]): number | null {
  return quantile([...values].sort((a, b) => a - b), 0.5);
}

export const RESAMPLES = 400;
export const INTERVAL_LEVEL = 0.9;

export interface Interval {
  low: number;
  high: number;
  n: number;
  level: number;
}

/**
 * A percentile bootstrap: the statistic (mean or median) on `resamples`
 * draws with replacement, its 5th and 95th percentile. Null below two values.
 */
export function bootstrapInterval(
  values: readonly unknown[] | null | undefined,
  statistic: "mean" | "median" = "median",
  { resamples = RESAMPLES, level = INTERVAL_LEVEL, seed = 1 }: { resamples?: number; level?: number; seed?: number } = {},
): Interval | null {
  const nums = (values || []).filter((v) => typeof v === "number" && Number.isFinite(v)) as number[];
  const n = nums.length;
  if (n < 2) return null;
  const stat = statistic === "mean" ? mean : median;
  const random = seeded(seed + n);
  const draws: number[] = [];
  for (let r = 0; r < resamples; r += 1) {
    const sample: number[] = [];
    for (let i = 0; i < n; i += 1) sample.push(nums[Math.floor(random() * n)] as number);
    draws.push(stat(sample) as number);
  }
  draws.sort((a, b) => a - b);
  const tail = (1 - level) / 2;
  return { low: quantile(draws, tail) as number, high: quantile(draws, 1 - tail) as number, n, level };
}

// --- Metrics per cell -----------------------------------------------------------

export interface GridMetric {
  key: string;
  label: string;
  statistic: "mean" | "median";
  format: (value: number | null | undefined) => string;
  /** Which way is up for the frontier; a numeric shift has no direction. */
  better: "lower" | "higher" | null;
  controlSource?: "production" | "replay" | null;
  field?: string;
  values: (label: string) => unknown[];
  control: () => unknown[];
}

function armResult(c: Loose, label: string): Loose {
  const result = c.arms?.[label];
  return result && !result.error ? result : null;
}

/** The treatments a grid shows: every arm but the replayed control. */
export function gridTreatmentLabels(experiment: Loose): string[] {
  return (experiment?.arms || [])
    .filter((arm: Loose) => arm.label !== REPLAYED_CONTROL_LABEL && !arm.replayed_control)
    .map((arm: Loose) => arm.label);
}

function floorA1(c: Loose): Loose {
  const result = c.noise_floor?.a1;
  return result && !result.error ? result : null;
}

/**
 * What a cell can show: cost per call (a mean), latency (a median, with the
 * control's only from a replayed control column, never production's
 * recording), and a reading per tracked field. With `floorAsReplay`, a run
 * without that column reads the noise floor's first replay (A1) as the
 * replayed control's latency.
 */
export function gridMetrics(
  experiment: Loose,
  { tracked = [], floorAsReplay = false }: { tracked?: string[]; floorAsReplay?: boolean } = {},
): GridMetric[] {
  const cases = (experiment?.cases || []).filter((c: Loose) => c.status == null || c.status === "replayed");
  const labels = gridTreatmentLabels(experiment);
  const column = (experiment?.arms || []).some((arm: Loose) => arm.label === REPLAYED_CONTROL_LABEL || arm.replayed_control);
  const fromFloor = !column && floorAsReplay && Boolean(experiment?.noise_floor) && cases.some((c: Loose) => floorA1(c));
  const replayed = column || fromFloor;
  const replayLatency = (c: Loose) => (fromFloor ? floorA1(c) : armResult(c, REPLAYED_CONTROL_LABEL))?.latency_ms;
  const metrics: GridMetric[] = [
    {
      key: "cost_per_call_usd", label: "Cost per call", statistic: "mean", format: formatUsd, better: "lower", controlSource: "production",
      values: (label) => cases.map((c: Loose) => armResult(c, label)?.cost_usd),
      control: () => cases.map((c: Loose) => c.control?.cost_usd),
    },
    {
      key: "latency_ms_p50", label: "Latency", statistic: "median", format: formatMs, better: "lower", controlSource: replayed ? "replay" : null,
      values: (label) => cases.map((c: Loose) => armResult(c, label)?.latency_ms),
      control: () => (replayed ? cases.map(replayLatency) : []),
    },
  ];
  for (const path of tracked) {
    const { kind } = fieldComparisons(cases, path, labels, experiment?.fields);
    const pairs = (label: string) => fieldPairs(cases, path, label, experiment?.fields);
    if (kind === "number") {
      metrics.push({
        key: `field:${path}`, label: `${path} shift`, statistic: "median", format: (v) => formatSigned(v), better: null, field: path,
        values: (label) => pairs(label).map((p) => (p.arm as number) - (p.control as number)), control: () => [],
      });
    } else if (kind === "list") {
      metrics.push({
        key: `field:${path}`, label: `${path} overlap`, statistic: "mean", format: formatPercent, better: "higher", field: path,
        values: (label) => pairs(label).map((p) => overlap(p.control, p.arm)), control: () => [],
      });
    } else if (kind !== "mixed") {
      metrics.push({
        key: `field:${path}`, label: `${path} matches`, statistic: "mean", format: formatPercent, better: "higher", field: path,
        values: (label) => pairs(label).map((p) => (String(p.control).trim() === String(p.arm).trim() ? 1 : 0)), control: () => [],
      });
    }
  }
  return metrics;
}

export interface CellReading {
  value: number | null;
  interval: Interval | null;
  n: number;
  text: string;
  rangeText: string | null;
  note: string | null;
  title?: string;
}

/** One cell's reading on one metric: the figure, its range and N, and the words for them. */
export function cellReading(values: readonly unknown[] | null | undefined, metric: Pick<GridMetric, "statistic" | "format" | "label">): CellReading {
  const nums = (values || []).filter((v) => typeof v === "number" && Number.isFinite(v)) as number[];
  const n = nums.length;
  if (!n) return { value: null, interval: null, n: 0, text: "—", rangeText: null, note: "No calls returned" };
  const value = (metric.statistic === "mean" ? mean(nums) : median(nums)) as number;
  const interval = bootstrapInterval(nums, metric.statistic);
  const calls = `${formatCount(n)} ${n === 1 ? "call" : "calls"}`;
  return {
    value,
    interval,
    n,
    text: metric.format(value),
    rangeText: interval ? `${metric.format(interval.low)} to ${metric.format(interval.high)}` : null,
    note: interval ? null : `${calls}, no range`,
    title: interval
      ? `${metric.label}: ${metric.format(value)}, the ${metric.statistic} of ${calls}. ${Math.round(interval.level * 100)}% bootstrap range ${metric.format(interval.low)} to ${metric.format(interval.high)}.`
      : `${metric.label}: ${metric.format(value)} from ${calls}; a range needs two.`,
  };
}

// --- The grid -------------------------------------------------------------------

/** The part of a model id people say out loud: the id after its last slash. */
export function shortModel(id: string | null | undefined): string {
  if (!id) return "";
  const text = String(id);
  return text.slice(text.lastIndexOf("/") + 1);
}

/** A treatment's settings in words: "temperature 0.2, 2,048 max tokens, low reasoning", or "Default settings". */
export function settingsPhrase(settings: Loose): string {
  const s = settings || {};
  const parts: string[] = [];
  if (s.temperature != null) parts.push(`temperature ${s.temperature}`);
  if (s.max_tokens != null) parts.push(`${Number(s.max_tokens).toLocaleString("en-US")} max tokens`);
  if (s.reasoning_effort != null) parts.push(`${s.reasoning_effort} reasoning`);
  return parts.length ? parts.join(", ") : "Default settings";
}

function canonical(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort().map((k) => [k, canonical((value as Record<string, unknown>)[k])]));
  }
  return value;
}

export interface GridLayout {
  rows: { model: string; label: string; provider: string | null }[];
  columns: { key: string; label: string; settings: Record<string, unknown> }[];
  /** Arm labels at `<row>:<column>`. */
  cells: Record<string, string[]>;
  arms: string[];
}

/**
 * Models as rows and setting variants as columns, read off the arms: a row
 * per model through an endpoint in the order it first appears, a column per
 * distinct settings, and at each crossing every arm that sits there. Null
 * with no treatments.
 */
export function gridLayout(experiment: Loose): GridLayout | null {
  const arms = (experiment?.arms || []).filter((arm: Loose) => arm.label !== REPLAYED_CONTROL_LABEL && !arm.replayed_control);
  if (!arms.length) return null;
  const rows: GridLayout["rows"] = [];
  const columns: GridLayout["columns"] = [];
  const cells: Record<string, string[]> = {};
  for (const arm of arms) {
    const model = arm.model || "";
    const provider = arm.provider || null;
    let row = rows.find((r) => r.model === model && r.provider === provider);
    if (!row) {
      row = { model, label: shortModel(model) || "—", provider };
      rows.push(row);
    }
    const key = JSON.stringify(canonical(arm.settings || {}));
    let column = columns.find((c) => c.key === key);
    if (!column) {
      column = { key, label: settingsPhrase(arm.settings), settings: arm.settings || {} };
      columns.push(column);
    }
    (cells[`${rows.indexOf(row)}:${columns.indexOf(column)}`] ||= []).push(arm.label);
  }
  // Rows that share a model are told apart by their endpoint.
  for (const row of rows) {
    if (rows.some((other) => other !== row && other.model === row.model)) row.label = `${row.label} via ${row.provider || "unknown provider"}`;
  }
  return { rows, columns, cells, arms: arms.map((arm: Loose) => arm.label) };
}

/** Every cell's reading on one metric, the control's when it has one, and one shared scale. */
export function gridReadings(experiment: Loose, metric: GridMetric): {
  readings: Record<string, CellReading>;
  control: CellReading | null;
  scale: { lo: number; hi: number } | null;
} {
  const labels = gridTreatmentLabels(experiment);
  const readings = Object.fromEntries(labels.map((label) => [label, cellReading(metric.values(label), metric)]));
  const control = metric.field ? null : cellReading(metric.control(), metric);
  const drawn = [...Object.values(readings), control].filter((r): r is CellReading => Boolean(r) && r!.value != null);
  const extent = drawn.flatMap((r) => [r.value as number, r.interval?.low ?? (r.value as number), r.interval?.high ?? (r.value as number)]);
  const lo = Math.min(0, ...extent);
  const hi = Math.max(0, ...extent);
  return { readings, control: control && control.n ? control : null, scale: drawn.length ? { lo, hi: hi > lo ? hi : lo + 1 } : null };
}

export interface FrontierPoint {
  label: string;
  x: CellReading;
  y: CellReading;
  control: boolean;
}

/** Every cell as a point: cost per call across, the chosen metric up. */
export function frontierPoints(experiment: Loose, metric: GridMetric, { cost = null }: { cost?: GridMetric | null } = {}): FrontierPoint[] {
  const costMetric = cost || (gridMetrics(experiment)[0] as GridMetric);
  const costs = gridReadings(experiment, costMetric);
  const ups = gridReadings(experiment, metric);
  const labels = gridTreatmentLabels(experiment);
  const points: FrontierPoint[] = labels.map((label) => ({ label, x: costs.readings[label]!, y: ups.readings[label]!, control: false }));
  if (costs.control && ups.control) points.push({ label: "Control", x: costs.control, y: ups.control, control: true });
  return points.filter((p) => p.x.value != null && p.y.value != null);
}

/**
 * The points no other point is at once cheaper than and at least as far up
 * (or down, where lower is the way): the cost against quality frontier,
 * cheapest first. A fact about the points, not a recommendation.
 */
export function frontier(points: readonly FrontierPoint[], better: "lower" | "higher" | null): FrontierPoint[] {
  if (!better) return [];
  const up = (p: FrontierPoint) => (better === "higher" ? (p.y.value as number) : -(p.y.value as number));
  const x = (p: FrontierPoint) => p.x.value as number;
  const kept = points.filter((p) => !points.some((q) => q !== p && x(q) <= x(p) && up(q) >= up(p) && (x(q) < x(p) || up(q) > up(p))));
  return kept.sort((a, b) => x(a) - x(b));
}

/** What a cell is in words, for a table cell's accessible name. */
export function cellSummary(rowLabel: string, columnLabel: string, armLabel: string | null, reading: CellReading | null): string {
  const parts = [reading?.text || "—"];
  if (reading?.rangeText) parts.push(`range ${reading.rangeText}`);
  if (reading?.note) parts.push(reading.note);
  return `${armLabel || `${rowLabel}, ${columnLabel}`}: ${parts.join(", ")}`;
}
