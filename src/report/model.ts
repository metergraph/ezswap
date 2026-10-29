// What the report shows, as plain data: every figure and sentence comes from
// core, read with the noise floor's first replay (A1) as the replayed control.
// The terminal report and the explorer render this; neither computes a
// statistic of its own. Presentation, never a verdict.

import type { Message, NotReplayableReason, RunFile } from "../types.ts";
import {
  type ExperimentView,
  type NoiseLabel,
  type Summary,
  buildView,
  equivalence,
  equivalenceSummary,
  fieldComparisons,
  fieldSentence,
  floorField,
  formatCount,
  formatMs,
  formatPercent,
  formatUsd,
  frontier,
  frontierPoints,
  gridMetrics,
  noiseFloors,
  noiseLabels,
  formatSigned,
  parseOutput,
  quantile,
  statRows,
} from "../core/index.ts";
import { KIND_LABELS, type Loose, fieldPairs, overlap, replayedIndexes } from "../core/fields.ts";
import { settingsPhrase, shortModel } from "../core/grid.ts";

export const NOISE_FLOOR_DOC = "https://github.com/metergraph/replay/blob/main/docs/noise-floor.md";
export const SETUP_COMMAND = "npx --yes metergraph-cli@latest setup";

/** The built-in statistics the summary shows, in order. */
export const SUMMARY_STATS = [
  "monthly_cost_usd",
  "cost_per_call_usd",
  "latency_ms_p50",
  "output_chars_p50",
  "json_parse_rate",
  "same_keys_rate",
  "empty_or_refused_rate",
  "cut_off_rate",
  "errors",
] as const;

/** Statistics read against production's model replayed (A1) rather than its recorded call. */
const REPLAY_READ = new Set(["monthly_cost_usd", "cost_per_call_usd", "latency_ms_p50"]);

const LABEL_WORDS: Record<NoiseLabel["label"], string> = {
  within_noise: "within noise",
  beyond_noise: "beyond noise",
  too_few_calls: "too few calls",
  not_comparable: "not comparable",
};

export interface LabelCell {
  text: string;
  tone: "within" | "beyond" | "none";
  title: string;
}

export interface Cell {
  text: string;
  /** The shift against production (or its replay), in core's words. */
  delta: string | null;
  /** Production's own gap on this figure, in core's words. */
  floor: string | null;
  title: string | null;
  label: LabelCell | null;
}

export interface Row {
  key: string;
  label: string;
  sublabel: string | null;
  kind: "stat" | "field";
  production: string;
  productionNote: string | null;
  /** Production against itself (A2 against A1), the row's noise floor. */
  floor: string | null;
  cells: Cell[];
  /** Per treatment: the full sentence, for fields. */
  sentences: string[] | null;
  note: string | null;
}

export interface Column {
  id: string;
  label: string;
  model: string;
  settings: string;
  endpoint: string;
  /** Cases this treatment returned a result on. */
  n: number | null;
  note: string | null;
}

export interface FrontierChart {
  key: string;
  label: string;
  better: "lower" | "higher" | null;
  format: "usd" | "ms" | "percent" | "signed";
  points: {
    label: string;
    x: number;
    xLow: number | null;
    xHigh: number | null;
    xText: string;
    y: number;
    yLow: number | null;
    yHigh: number | null;
    yText: string;
    n: number;
    control: boolean;
    onFrontier: boolean;
  }[];
  /** Production against itself on this metric, drawn as a reference line. */
  floor: { y: number; text: string } | null;
  frontier: string[];
}

/** One tracked field whose value on an arm differs from production's on a case. */
export interface FieldChange {
  path: string;
  from: string;
  to: string;
}

export interface ArmOutput {
  label: string;
  status: "ok" | "failed" | "missing";
  output: string | null;
  pretty: string | null;
  error: string | null;
  flags: string[];
  latency: string;
  cost: string;
  finishReason: string | null;
  differs: boolean;
  /** Tracked fields whose value differs from production's, read as the field's statistics read it. */
  changes: FieldChange[];
}

export interface CaseItem {
  id: string;
  traceId: string;
  status: "replayed" | "not_replayable";
  reason: string | null;
  input: string | null;
  /** The request as replayed: folded system turns, then the text turns. */
  system: string | null;
  messages: Message[];
  limitations: string[];
  production: ArmOutput;
  arms: ArmOutput[];
  floor: ArmOutput[];
  differs: boolean;
}

/** Which arm a figure belongs to: production (its replay when there is a floor), a treatment, or production against itself. */
export type ArmRole = "production" | "treatment" | "floor";

export interface CostBar {
  role: ArmRole;
  label: string;
  /** Short name for narrow columns. */
  name: string;
  model: string;
  perCall: number | null;
  text: string;
  /** The change against production's replayed model, in core's words. */
  delta: string | null;
  /** Production's recorded figure, beside its replay. */
  note: string | null;
  monthly: number | null;
  monthlyText: string | null;
  monthlyDelta: string | null;
  badge: LabelCell | null;
}

export interface LatencyRow {
  role: ArmRole;
  label: string;
  name: string;
  /** Calls with a latency. */
  n: number;
  p10: number | null;
  p50: number | null;
  p90: number | null;
  text: string;
  delta: string | null;
  badge: LabelCell | null;
  /** Every call's latency, for a distribution. */
  values: number[];
}

export interface FieldArm {
  label: string;
  name: string;
  /** Agreement (0..1) for labels, text and lists; the median shift for numbers. */
  value: number | null;
  valueText: string;
  detail: string;
  sentence: string;
  badge: LabelCell | null;
  /** Numbers: the middle half of the per-call shift. */
  range: { lo: number; mid: number; hi: number } | null;
  /** Labels: how production's label became this arm's, where it changed, most common first. */
  changes: { from: string; to: string; count: number }[];
}

export interface FieldPanel {
  path: string;
  kind: string;
  kindLabel: string;
  /** "share": agreement from 0 to 1; "shift": a signed per-call difference. */
  scale: "share" | "shift" | "none";
  floor: { value: number | null; text: string | null; low: number | null; high: number | null } | null;
  arms: FieldArm[];
}

export interface FloorPanel {
  cases: number;
  model: string;
  spent: string;
  text: string[];
  /** Built-in statistics, A1 beside A2 and their gap. */
  stats: { label: string; a1: string; a2: string; gap: string }[];
  /** Tracked fields: agreement and the calls where A1 and A2 disagreed. */
  fields: { path: string; text: string | null; mismatches: { traceId: string; a1: string; a2: string }[] }[];
}

export interface ReportModel {
  headline: string;
  subhead: string;
  meta: { label: string; value: string }[];
  stopped: string | null;
  productionModel: string;
  columns: Column[];
  rows: Row[];
  hasFloor: boolean;
  floor: FloorPanel | null;
  labels: boolean;
  charts: FrontierChart[];
  cases: CaseItem[];
  notReplayable: { reason: string; title: string; count: number; fix: string }[];
  /** Calls a month the projection uses, or null when unknown. */
  monthlyCalls: number | null;
  cost: CostBar[];
  latency: LatencyRow[];
  fields: FieldPanel[];
  /** Production's recorded median latency, which is not compared. */
  recordedLatency: string | null;
  view: ExperimentView;
}

export interface ModelOptions {
  tracked?: string[];
  labels?: boolean;
  /** Calls a month to project a monthly cost from; else read from the run's traffic. */
  monthlyCalls?: number | null;
}

/**
 * Calls a month: the volume the user gave (`--monthly-calls`), else the run's
 * input traffic (calls a day over the file's span, times 30). Null when unknown.
 */
export function monthlyCalls(run: RunFile): number | null {
  const given = run.settings.monthlyCalls;
  if (typeof given === "number" && Number.isFinite(given) && given > 0) return Math.round(given);
  const traffic = run.input.traffic;
  if (!traffic || !(traffic.calls > 0) || !traffic.from || !traffic.to) return null;
  const days = (Date.parse(traffic.to) - Date.parse(traffic.from)) / 86_400_000;
  if (!Number.isFinite(days) || days < 1) return null;
  return Math.round((traffic.calls / days) * 30);
}

const REASON_TEXT: Record<NotReplayableReason, { title: string; fix: string }> = {
  content_unavailable: {
    title: "No content captured",
    fix: "The trace carries no prompt or response text. Turn on content capture in your tracing setup and export again.",
  },
  content_truncated: {
    title: "Content cut short",
    fix: "The exporter cut the content, so the replay would send a different prompt. Raise the exporter's attribute or payload size limit and export again.",
  },
  tool_history: {
    title: "Earlier tool calls in the conversation",
    fix: "A replay cannot rerun the tools that answered earlier turns. Sample calls that start from a user message, before any tool runs.",
  },
  unsupported_content: {
    title: "Images, audio or files",
    fix: "Only text turns replay in this version. Calls with text-only messages replay as they are.",
  },
  no_messages: {
    title: "No messages",
    fix: "The request has no text turns to send. Check that the adapter matches the way the file was exported.",
  },
  last_turn_not_user: {
    title: "Last turn is not from the user",
    fix: "The request ends with an assistant turn, so there is nothing to answer. Export the request exactly as it was sent.",
  },
  input_too_large: {
    title: "Input too large",
    fix: "The input is larger than a replay sends (64 KiB). Sample shorter calls.",
  },
  tool_definitions_not_offered: {
    title: "Forced choice among tools",
    fix: "Production forced a choice among several tools, which a replay that offers no tools cannot reproduce. Calls that force a single tool, or offer none, replay.",
  },
  web_search_not_replayed: {
    title: "Web search",
    fix: "Production searched the web on these calls and a replay does not. Calls without web search replay.",
  },
  unparseable_request: {
    title: "Request could not be read",
    fix: "The request body is not in a shape any adapter reads. See docs/trace-format.md for the accepted formats.",
  },
};

const FLAG_TEXT: Record<string, string> = {
  temperature_dropped: "temperature not accepted, sent without it",
  response_format_dropped: "response format not accepted, sent without it",
  maybe_billed: "may have been billed though it failed",
  retried: "retried",
};

function utc(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getUTCDate()} ${months[d.getUTCMonth()]} ${d.getUTCFullYear()}, ${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())} UTC`;
}

function labelCell(label: NoiseLabel | undefined): LabelCell | null {
  if (!label) return null;
  const words = LABEL_WORDS[label.label];
  const text = label.label === "beyond_noise" && label.direction && label.direction !== "different" ? `${words}, ${label.direction}` : words;
  const tone = label.label === "within_noise" ? "within" : label.label === "beyond_noise" ? "beyond" : "none";
  const title =
    label.label === "too_few_calls"
      ? `${formatCount(label.cases)} paired cases; a label needs 10.`
      : `Over ${formatCount(label.cases)} paired cases. The treatment's gap is compared with production's gap from itself; neither says which is better.`;
  return { text, tone, title };
}

function pretty(output: string | null): string | null {
  if (output == null) return null;
  const parsed = parseOutput(output);
  if (!parsed.ok || parsed.value === null || typeof parsed.value !== "object") return null;
  return JSON.stringify(parsed.value, null, 2);
}

/** Whether one arm's value on one field differs from production's, read the way the field's statistics read it. */
function valuesDiffer(kind: string, control: unknown, arm: unknown): boolean {
  if (kind === "number") return control !== arm;
  if (kind === "list") return overlap(control, arm) < 1;
  return String(control).trim() !== String(arm).trim();
}

/** A field value as text: strings as they are, anything else as JSON. */
function showValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (value === undefined) return "—";
  return JSON.stringify(value) ?? String(value);
}

/**
 * Short names for arms: a label that is just the model id loses its
 * creator prefix ("google/gemini-2.5-flash-lite" to "gemini-2.5-flash-lite"),
 * unless two arms would then share a name.
 */
function shortNames(arms: { label: string; model: string }[]): string[] {
  const short = arms.map((a) => (a.label === a.model ? shortModel(a.model) || a.label : a.label));
  return short.map((name, i) => (short.indexOf(name) !== short.lastIndexOf(name) ? (arms[i] as { label: string }).label : name));
}

function spread(values: number[]): { p10: number | null; p50: number | null; p90: number | null } {
  const sorted = [...values].sort((a, b) => a - b);
  return { p10: quantile(sorted, 0.1), p50: quantile(sorted, 0.5), p90: quantile(sorted, 0.9) };
}

const finiteNumber = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v);

/** Everything the report shows, from the run. */
export function reportModel(run: RunFile, options: ModelOptions = {}): ReportModel {
  const view = buildView(run);
  const tracked = options.tracked ?? view.tracked_fields;
  const labelsOn = Boolean(options.labels) && Boolean(view.noise_floor);
  const arms = view.arms;
  const armLabels = arms.map((a) => a.label);
  const calls = options.monthlyCalls ?? monthlyCalls(run);
  const volume = calls ? { calls } : null;
  const hasFloor = Boolean(view.noise_floor);
  const a1 = view.noise_floor?.a1 ?? null;
  const productionModel = view.control.model ?? "production's model";

  const stats = statRows(view, { volume, floorAsReplay: true });
  const byKey = new Map(stats.map((row) => [row.key, row]));
  const floors = noiseFloors(view, { tracked });
  const summary = equivalenceSummary(view, { tracked, floors });
  const eq = equivalence({ ...view, tracked_fields: tracked }, { via: "endpoint" }).treatments;
  const labelSets = labelsOn ? arms.map((arm) => noiseLabels(view, arm.label, { tracked })) : [];
  const labelFor = (i: number, key: string) => {
    const set = labelSets[i];
    return set ? labelCell([...set.stats, ...set.fields].find((l) => l.key === key)) : null;
  };

  const rows: Row[] = [];
  for (const key of SUMMARY_STATS) {
    const row = byKey.get(key);
    if (!row) continue;
    const replayRead = REPLAY_READ.has(key) && hasFloor;
    let productionNote: string | null = null;
    if (replayRead && a1) {
      const perCall = a1.cost_per_call_usd;
      const replayed =
        key === "latency_ms_p50"
          ? formatMs(a1.latency_ms_p50)
          : formatUsd(key === "monthly_cost_usd" ? (perCall == null || !calls ? null : perCall * calls) : perCall);
      productionNote = `${replayed} replayed`;
    }
    const floorText = row.floor ? (row.floor.text ? `${row.floor.text} ${row.floor.delta}` : row.floor.delta) : null;
    rows.push({
      key,
      label: row.label,
      sublabel:
        key === "monthly_cost_usd"
          ? `${row.sublabel ?? ""}${row.sublabel ? ", " : ""}projected from the sample's cost per call`
          : replayRead
            ? "against production's model replayed"
            : key === "latency_ms_p50"
              ? "not compared with production's recorded latency"
              : null,
      kind: "stat",
      production: key === "latency_ms_p50" && hasFloor ? `${row.control} recorded` : row.control,
      productionNote,
      floor: floorText,
      cells: row.arms.map((cell, i) => ({
        text: cell.text,
        delta: cell.delta === "same" ? "same" : cell.delta,
        floor: floorText,
        title: cell.sameCases ? `Over the ${formatCount(cell.sameCases)} cases this treatment returned on, production's figure is ${cell.sameBase}.` : (cell.title ?? null),
        label: key === "monthly_cost_usd" || key === "errors" ? null : labelFor(i, key),
      })),
      sentences: null,
      note: row.note?.text ?? null,
    });
  }
  if (!byKey.has("monthly_cost_usd")) {
    rows.unshift({
      key: "monthly_cost_usd",
      label: "Monthly cost",
      sublabel: "needs your call volume",
      kind: "stat",
      production: "—",
      productionNote: null,
      floor: null,
      cells: arms.map(() => ({ text: "—", delta: null, floor: null, title: null, label: null })),
      sentences: null,
      note: "The run does not record how many calls the traced workload makes, so no monthly cost is projected.",
    });
  }

  for (const path of tracked) {
    const { kind, comparisons } = fieldComparisons(view.cases ?? [], path, armLabels, view.fields);
    const floor = floorField(view, path, kind === "mixed" ? null : kind);
    const floorText = floors[`field:${path}`]?.text ?? null;
    rows.push({
      key: `field:${path}`,
      label: path,
      sublabel: `${KIND_LABELS[kind]} field`,
      kind: "field",
      production: "—",
      productionNote: null,
      floor: floorText,
      cells: arms.map((_, i) => {
        const cell = summary?.rows?.find((r: Loose) => r.index === i)?.cells?.[`field:${path}`];
        return {
          text: cell ? `${cell.text}${cell.sub ? ` ${cell.sub}` : ""}` : "—",
          delta: null,
          floor: floorText,
          title: cell?.title ?? null,
          label: labelFor(i, `field:${path}`),
        };
      }),
      sentences: comparisons.map((c) => fieldSentence(c, floor)),
      note: null,
    });
  }

  const columns: Column[] = arms.map((arm, i) => {
    const t = eq.find((e) => e.label === arm.label);
    const row = summary?.rows?.find((r: Loose) => r.index === i);
    return {
      id: arm.id,
      label: arm.label,
      model: arm.model,
      settings: settingsPhrase(arm.settings),
      endpoint: arm.provider,
      n: row?.n ?? null,
      note: row?.note ?? t?.sample_note ?? null,
    };
  });

  // The cost against quality frontier, one chart per metric the grid reads.
  const charts: FrontierChart[] = [];
  const metrics = gridMetrics(view, { tracked, floorAsReplay: true });
  const cost = metrics[0];
  const formatOf = (key: string, label: string): FrontierChart["format"] =>
    key === "cost_per_call_usd" ? "usd" : key === "latency_ms_p50" ? "ms" : label.endsWith("shift") ? "signed" : "percent";
  // Tracked fields first: they say most about the outputs; latency last.
  const ordered = [...metrics.slice(2), ...metrics.slice(1, 2)];
  for (const metric of ordered) {
    const points = frontierPoints(view, metric, { cost: cost ?? null });
    if (!points.length) continue;
    const kept = frontier(points, metric.better).map((p) => p.label);
    let floorLine: FrontierChart["floor"] = null;
    if (metric.field) {
      const f = floorField(view, metric.field);
      const y = f?.kind === "list" ? f.overlap : f?.rate;
      if (f && typeof y === "number" && metric.better) floorLine = { y, text: `Production vs itself: ${formatPercent(y)}` };
    }
    charts.push({
      key: metric.key,
      label: metric.label,
      better: metric.better,
      format: formatOf(metric.key, metric.label),
      points: points.map((p) => ({
        label: p.control ? "Production, replayed" : p.label,
        x: p.x.value as number,
        xLow: p.x.interval?.low ?? null,
        xHigh: p.x.interval?.high ?? null,
        xText: p.x.text,
        y: p.y.value as number,
        yLow: p.y.interval?.low ?? null,
        yHigh: p.y.interval?.high ?? null,
        yText: p.y.text,
        n: p.y.n,
        control: p.control,
        onFrontier: kept.includes(p.label),
      })),
      floor: floorLine,
      frontier: kept.map((l) => (l === "Control" ? "Production, replayed" : l)),
    });
    // A field has no control reading of its own, so production sits where its replay costs and agrees with itself.
    const chart = charts.at(-1) as FrontierChart;
    if (floorLine && a1?.cost_per_call_usd != null && !chart.points.some((p) => p.control)) {
      chart.points.push({
        label: "Production, replayed",
        x: a1.cost_per_call_usd,
        xLow: null,
        xHigh: null,
        xText: formatUsd(a1.cost_per_call_usd),
        y: floorLine.y,
        yLow: null,
        yHigh: null,
        yText: `${formatPercent(floorLine.y)} against itself`,
        n: view.noise_floor?.cases ?? 0,
        control: true,
        onFrontier: false,
      });
    }
  }

  // The case browser.
  const kinds = new Map(tracked.map((path) => [path, fieldComparisons(view.cases ?? [], path, armLabels, view.fields).kind]));
  const differing = new Map<string, Set<string>>();
  const changesOf = new Map<string, FieldChange[]>();
  for (const label of armLabels) {
    const set = new Set<string>();
    for (const path of tracked) {
      const kind = kinds.get(path) as string;
      if (kind === "mixed" || kind === "empty") continue;
      for (const p of fieldPairs(view.cases ?? [], path, label, view.fields)) {
        if (!valuesDiffer(kind, p.control, p.arm)) continue;
        set.add(p.trace_id);
        const key = `${p.trace_id}\u0000${label}`;
        const list = changesOf.get(key) ?? [];
        list.push({ path, from: showValue(p.control), to: showValue(p.arm) });
        changesOf.set(key, list);
      }
    }
    differing.set(label, set);
  }
  const replayById = new Map(run.replays.map((r) => [`${r.caseId}\u0000${r.arm}`, r]));
  const caseItems: CaseItem[] = (view.cases ?? []).map((c) => {
    const original = run.cases.find((k) => k.id === c.case_id);
    const output = (label: string, arm: string, result: Loose): ArmOutput => {
      const replay = replayById.get(`${c.case_id}\u0000${arm}`);
      if (!result) return { label, status: "missing", output: null, pretty: null, error: replay?.status === "skipped" ? "Not run: the run stopped first" : "No result", flags: [], latency: "—", cost: "—", finishReason: null, differs: false, changes: [] };
      const flags = (replay?.flags ?? []).map((f) => FLAG_TEXT[f] ?? f);
      if (result.refusal) flags.push("flagged as a refusal");
      if (result.finish_reason && ["length", "max_tokens", "max_output_tokens"].includes(result.finish_reason)) flags.push("cut off at the token limit");
      const error = result.error ? `${result.error}${replay?.error?.message ? `: ${replay.error.message}` : ""}` : null;
      let differs = false;
      if (arm !== "control" && !arm.startsWith("floor_")) {
        if (error || result.refusal) differs = true;
        else if (tracked.length) differs = differing.get(label)?.has(c.trace_id) ?? false;
        else differs = String(result.output ?? "").trim() !== String(c.control.output ?? "").trim();
      }
      return {
        label,
        status: error ? "failed" : "ok",
        output: result.output ?? null,
        pretty: pretty(result.output ?? null),
        error,
        flags,
        latency: formatMs(result.latency_ms),
        cost: formatUsd(result.cost_usd),
        finishReason: result.finish_reason ?? null,
        differs,
        changes: arm !== "control" && !arm.startsWith("floor_") ? (changesOf.get(`${c.trace_id}\u0000${label}`) ?? []) : [],
      };
    };
    const production = output("Production", "control", c.control);
    production.flags = [];
    if (c.control.refusal) production.flags.push("flagged as a refusal");
    const armOutputs = c.status === "replayed" ? arms.map((arm) => output(arm.label, arm.id, c.arms[arm.label])) : [];
    const floorOutputs =
      c.status === "replayed" && c.noise_floor
        ? [output("A1, production's model replayed", "floor_a1", c.noise_floor.a1), output("A2, replayed again", "floor_a2", c.noise_floor.a2)]
        : [];
    return {
      id: c.case_id,
      traceId: c.trace_id,
      status: c.status,
      reason: c.reason ? (REASON_TEXT[c.reason as NotReplayableReason]?.title ?? c.reason) : null,
      input: c.input_preview ?? original?.messages.at(-1)?.content ?? null,
      system: original?.system ?? null,
      messages: original?.messages ?? [],
      limitations: c.limitations,
      production,
      arms: armOutputs,
      floor: floorOutputs,
      differs: armOutputs.some((a) => a.differs),
    };
  });

  const reasonCounts = new Map<string, number>();
  for (const c of view.cases ?? []) if (c.status === "not_replayable" && c.reason) reasonCounts.set(c.reason, (reasonCounts.get(c.reason) ?? 0) + 1);
  const notReplayable = [...reasonCounts]
    .sort((x, y) => y[1] - x[1])
    .map(([reason, count]) => {
      const text = REASON_TEXT[reason as NotReplayableReason];
      return { reason, title: text?.title ?? reason, count, fix: text?.fix ?? "This call could not be replayed." };
    });

  // The headline: a question built from the data, never an answer.
  const costRow = byKey.get("cost_per_call_usd");
  const cheaper = arms
    .map((arm, i) => ({ arm, delta: costRow?.arms[i]?.delta ?? null, cost: arm.summary.cost_per_call_usd }))
    .filter((x) => x.cost != null)
    .sort((x, y) => (x.cost as number) - (y.cost as number));
  const baseCost = a1?.cost_per_call_usd ?? view.control.summary.cost_per_call_usd;
  const cheapest = cheaper[0];
  const anyCheaper = cheapest && baseCost != null && (cheapest.cost as number) < baseCost;
  const prod = shortModel(productionModel) || productionModel;
  const headline = anyCheaper ? `Would a cheaper model have worked for ${prod}'s calls?` : `What would change if ${prod}'s calls ran on another model?`;
  const names = arms.map((a) => a.label);
  const list = names.length <= 1 ? names.join("") : `${names.slice(0, -1).join(", ")} and ${names.at(-1)}`;
  const parts = [`${formatCount(view.sample.replayed)} production ${view.sample.replayed === 1 ? "call" : "calls"} replayed on ${list}.`];
  if (anyCheaper && cheapest?.delta?.endsWith("% lower")) {
    const less = cheapest.delta.replace(/ lower$/, " less");
    const short = shortModel(cheapest.arm.model);
    const name = cheapest.arm.label === cheapest.arm.model || cheapest.arm.label === short ? short || cheapest.arm.label : `${cheapest.arm.label} (${short})`;
    parts.push(`${name} costs ${less} per call than ${prod}${hasFloor ? " replayed the same way" : ""}.`);
  }
  parts.push(hasFloor ? `Every difference below sits beside how much ${prod} differs from itself.` : "This run has no noise floor, so differences are shown without production's own variation.");

  const meta = [
    { label: "Sample", value: `${formatCount(view.sample.cases)} calls, ${formatCount(view.sample.replayed)} replayed` },
    { label: "Source", value: `${run.input.adapter}, ${run.input.file}` },
    { label: "Production model", value: productionModel },
    { label: "Created", value: utc(run.createdAt) },
    { label: "Price catalog", value: run.catalog.version },
    { label: "Spent", value: `${formatUsd(run.spentUsd)} of ${formatUsd(run.settings.budgetUsd)} budget` },
  ];
  const stopped =
    run.stopped === "budget"
      ? "The run stopped at its budget, so some calls were not replayed."
      : run.stopped === "interrupted"
        ? "The run was interrupted, so some calls were not replayed."
        : run.stopped === "reservation_exceeded"
          ? "The run stopped when a call could have cost more than the budget left, so some calls were not replayed."
          : null;

  const nf = view.noise_floor;
  const replayedCases = (view.cases ?? []).filter((c) => c.status === "replayed");
  const floorStats: FloorPanel["stats"] = [];
  if (nf) {
    const statText: [string, string, (s: Summary) => string][] = [
      ["cost_per_call_usd", "Cost per call", (s) => formatUsd(s.cost_per_call_usd)],
      ["latency_ms_p50", "Latency, median", (s) => formatMs(s.latency_ms_p50)],
      ["output_chars_p50", "Output length, median", (s) => formatCount(s.output_chars_p50)],
      ["json_parse_rate", "JSON parses", (s) => formatPercent(s.json_parse_rate)],
      ["empty_or_refused_rate", "Empty or refused", (s) => formatPercent(s.empty_or_refused_rate)],
      ["cut_off_rate", "Cut off at token limit", (s) => formatPercent(s.cut_off_rate)],
    ];
    for (const [key, label, read] of statText) {
      const row = rows.find((r) => r.key === key);
      floorStats.push({ label, a1: read(nf.a1), a2: read(nf.a2), gap: row?.floor ?? "—" });
    }
  }
  const floorFields: FloorPanel["fields"] = nf
    ? tracked.map((path) => {
        const series = nf.fields?.[path];
        const mismatches =
          series && series.comparison === "match_rate"
            ? (series.mismatches ?? []).map((traceId) => {
                const i = replayedCases.findIndex((c) => c.trace_id === traceId);
                return { traceId, a1: showValue(series.a1?.[i]), a2: showValue(series.a2?.[i]) };
              })
            : [];
        return { path, text: floors[`field:${path}`]?.text ?? null, mismatches };
      })
    : [];
  const floorPanel: FloorPanel | null = nf
    ? {
        cases: nf.cases,
        model: prod,
        spent: formatUsd(nf.spent_usd),
        text: [
          `Each replayable call was sent to ${prod} twice more (A1 and A2), at production's settings, through the same endpoint as the treatments. Whatever separates A2 from A1 is chance, so it is the variation production shows on its own.`,
          `Read a treatment's difference beside that gap: a difference no larger than production's own is one a rerun of production would also show. Neither figure says which output is better.`,
        ],
        stats: floorStats,
        fields: floorFields,
      }
    : null;

  // Bars: cost and latency per arm, production first and production against itself last.
  const armNames = shortNames(arms);
  const rowOf = (key: string) => rows.find((r) => r.key === key);
  const costStat = rowOf("cost_per_call_usd");
  const monthlyStat = rowOf("monthly_cost_usd");
  const latencyStat = rowOf("latency_ms_p50");
  const strip = (text: string | null | undefined) => (text ? text.replace(/ (replay|control)$/, "") : null);
  const perMonth = (perCall: number | null) => (perCall != null && calls ? perCall * calls : null);
  const prodPerCall = hasFloor ? (a1?.cost_per_call_usd ?? null) : view.control.summary.cost_per_call_usd;
  const costBars: CostBar[] = [
    {
      role: "production",
      label: hasFloor ? `${prod}, replayed` : prod,
      name: prod,
      model: productionModel,
      perCall: prodPerCall,
      text: formatUsd(prodPerCall),
      delta: null,
      note: hasFloor && costStat && view.control.summary.cost_per_call_usd != null ? `${costStat.production} recorded` : null,
      monthly: perMonth(prodPerCall),
      monthlyText: perMonth(prodPerCall) == null ? null : `${formatUsd(perMonth(prodPerCall))}/mo`,
      monthlyDelta: null,
      badge: null,
    },
    ...arms.map((arm, i): CostBar => {
      const perCall = arm.summary.cost_per_call_usd;
      const monthlyCell = monthlyStat?.cells[i];
      return {
        role: "treatment",
        label: arm.label,
        name: armNames[i] as string,
        model: arm.model,
        perCall,
        text: costStat?.cells[i]?.text ?? formatUsd(perCall),
        delta: strip(costStat?.cells[i]?.delta),
        note: null,
        monthly: perMonth(perCall),
        monthlyText: monthlyCell && monthlyCell.text !== "—" ? `${monthlyCell.text}/mo` : null,
        monthlyDelta: monthlyCell?.delta ?? null,
        badge: costStat?.cells[i]?.label ?? null,
      };
    }),
  ];
  if (nf) {
    const perCall = nf.summary.cost_per_call_usd;
    costBars.push({
      role: "floor",
      label: "production vs itself",
      name: "vs itself",
      model: productionModel,
      perCall,
      text: formatUsd(perCall),
      delta: costStat?.floor ?? null,
      note: null,
      monthly: null,
      monthlyText: null,
      monthlyDelta: null,
      badge: null,
    });
  }

  const latencies = (pick: (c: (typeof replayedCases)[number]) => Loose): number[] =>
    replayedCases
      .map(pick)
      .filter((r) => r && !r.error && finiteNumber(r.latency_ms))
      .map((r) => r.latency_ms as number);
  const latencyRow = (role: ArmRole, label: string, name: string, values: number[], median: number | null, delta: string | null, badge: LabelCell | null): LatencyRow => {
    const s = spread(values);
    return { role, label, name, n: values.length, p10: s.p10, p50: median, p90: s.p90, text: formatMs(median), delta, badge, values };
  };
  const latency: LatencyRow[] = [
    hasFloor
      ? latencyRow("production", `${prod}, replayed`, prod, latencies((c) => c.noise_floor?.a1), a1?.latency_ms_p50 ?? null, null, null)
      : latencyRow("production", prod, prod, latencies((c) => c.control), view.control.summary.latency_ms_p50, null, null),
    ...arms.map((arm, i) =>
      latencyRow("treatment", arm.label, armNames[i] as string, latencies((c) => c.arms[arm.label]), arm.summary.latency_ms_p50, strip(latencyStat?.cells[i]?.delta), latencyStat?.cells[i]?.label ?? null),
    ),
  ];
  if (nf) latency.push(latencyRow("floor", "production vs itself", "vs itself", latencies((c) => c.noise_floor?.a2), nf.a2.latency_ms_p50, latencyStat?.floor ?? null, null));

  // Tracked fields: agreement per arm on one scale with production's agreement with itself.
  const fieldPanels: FieldPanel[] = tracked.map((path) => {
    const { kind, comparisons } = fieldComparisons(view.cases ?? [], path, armLabels, view.fields);
    const f = floorField(view, path, kind === "mixed" ? null : kind);
    const statRow = rowOf(`field:${path}`);
    const share = (x: Loose): number | null => {
      if (!x) return null;
      if (x.kind === "list") return finiteNumber(x.overlap) ? x.overlap : null;
      if (x.kind === "label" || x.kind === "text" || x.kind === "long_text") return finiteNumber(x.rate) ? x.rate : null;
      return null;
    };
    const scale: FieldPanel["scale"] = kind === "number" ? "shift" : ["label", "text", "long_text", "list"].includes(kind) ? "share" : "none";
    return {
      path,
      kind,
      kindLabel: KIND_LABELS[kind],
      scale,
      floor: f
        ? {
            value: scale === "shift" ? (f.diffBox?.median ?? null) : share(f),
            text: floors[`field:${path}`]?.text ?? null,
            low: f.diffBox?.min ?? null,
            high: f.diffBox?.max ?? null,
          }
        : null,
      arms: comparisons.map((c, i): FieldArm => {
        const box = c.diffBox ?? null;
        const value = scale === "shift" ? (box?.median ?? null) : share(c);
        return {
          label: c.label,
          name: armNames[i] as string,
          value,
          valueText: scale === "shift" ? formatSigned(value) : formatPercent(value),
          detail: statRow?.cells[i]?.text ?? "—",
          sentence: statRow?.sentences?.[i] ?? fieldSentence(c, f),
          badge: statRow?.cells[i]?.label ?? null,
          range: box ? { lo: box.q1, mid: box.median, hi: box.q3 } : null,
          changes: (c.tally ?? []).filter((t) => !t.same).map((t) => ({ from: t.from, to: t.to, count: t.count })),
        };
      }),
    };
  });

  return {
    headline,
    subhead: parts.join(" "),
    meta,
    stopped,
    productionModel,
    columns,
    rows,
    hasFloor,
    floor: floorPanel,
    labels: labelsOn,
    charts,
    cases: caseItems,
    notReplayable,
    monthlyCalls: calls,
    cost: costBars,
    latency,
    fields: fieldPanels,
    recordedLatency: hasFloor && latencyStat ? latencyStat.production.replace(/ recorded$/, "") : null,
    view,
  };
}
