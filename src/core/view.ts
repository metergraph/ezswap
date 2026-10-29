// The experiment view built from a run file: the same shape Metergraph's
// hosted product uses, so every statistic and sentence in core reads a local
// run exactly as it reads a hosted one. The control is
// each case's recorded production output; the arms are the run's
// treatments; the noise floor is production's model replayed twice.

import type { Case, Replay, RunFile, Treatment } from "../types.ts";
import { type FloorCase, type FloorInput, controlOutcome, noiseFloorView } from "./noise-floor.ts";
import { parseJsonPath } from "./paths.ts";
import { fieldValue, outputFacts, summarize } from "./summary.ts";
import type {
  ArmView,
  CaseView,
  ControlRecord,
  ExperimentView,
  Outcome,
  ResultView,
} from "./view-types.ts";

export const FLOOR_ARMS = ["floor_a1", "floor_a2"] as const;

/**
 * A replay as the statistics read it, or undefined for one that never ran
 * (skipped when the run stopped), which counts as no result rather than an
 * error. Facts are taken from the full output with the tracked fields.
 */
export function replayOutcome(replay: Replay | undefined, trackedFields: readonly string[]): Outcome | undefined {
  if (!replay || replay.status === "skipped") return undefined;
  const error = replay.status === "ok" ? null : replay.error?.code || "failed";
  return {
    output: replay.output,
    error,
    cost_usd: replay.costUsd,
    cost_search_usd: null,
    search_units: null,
    latency_ms: replay.latencyMs,
    finish_reason: replay.finishReason,
    output_tokens: replay.usage?.outputTokens ?? null,
    input_tokens: replay.usage?.inputTokens ?? null,
    cache_read_tokens: replay.usage?.cachedInputTokens ?? null,
    // input_includes_cache_read is left out, which reads as true: replies come through an
    // OpenAI-compatible endpoint, whose input count includes cached tokens.
    refusal: replay.refusal,
    facts: error ? null : outputFacts(replay.output, trackedFields),
    temperature_dropped: replay.flags.includes("temperature_dropped"),
    response_format_dropped: replay.flags.includes("response_format_dropped"),
    output_truncated: false,
  };
}

/** Production's call as the case's control. Its cache convention is not recorded, so it is unknown. */
export function controlRecord(c: Case, trackedFields: readonly string[]): ControlRecord {
  const p = c.production;
  return {
    output: p.output,
    cost_usd: p.costUsd,
    cost_search_usd: null,
    latency_ms: p.latencyMs,
    finish_reason: p.finishReason,
    output_tokens: p.usage?.outputTokens ?? null,
    input_tokens: p.usage?.inputTokens ?? null,
    cache_read_tokens: p.usage?.cachedInputTokens ?? null,
    input_includes_cache_read: null,
    refusal: p.refusal,
    facts: outputFacts(p.output, trackedFields),
    output_truncated: false,
    model: c.model,
    provider: c.provider,
    settings: caseSettings(c),
  };
}

function caseSettings(c: Case): Record<string, unknown> {
  const params = c.params;
  const settings: Record<string, unknown> = {};
  if (params.temperature != null) settings.temperature = params.temperature;
  if (params.maxTokens != null) settings.max_tokens = params.maxTokens;
  if (params.reasoningEffort != null) settings.reasoning_effort = params.reasoningEffort;
  return settings;
}

function treatmentSettings(t: Treatment): Record<string, unknown> {
  const settings: Record<string, unknown> = {};
  if (t.settings.temperature != null) settings.temperature = t.settings.temperature;
  if (t.settings.maxTokens != null) settings.max_tokens = t.settings.maxTokens;
  if (t.settings.reasoningEffort != null) settings.reasoning_effort = t.settings.reasoningEffort;
  return settings;
}

function resultView(outcome: Outcome): ResultView {
  return {
    output: outcome.output ?? null,
    cost_usd: outcome.cost_usd ?? null,
    cost_search_usd: outcome.cost_search_usd ?? null,
    search_units: outcome.search_units ?? null,
    latency_ms: outcome.latency_ms ?? null,
    finish_reason: outcome.finish_reason ?? null,
    output_tokens: outcome.output_tokens ?? null,
    error: outcome.error ?? null,
    temperature_dropped: Boolean(outcome.temperature_dropped),
    temperature_ignored_with_reasoning: false,
    response_format_dropped: Boolean(outcome.response_format_dropped),
    output_truncated: Boolean(outcome.output_truncated),
    refusal: Boolean(outcome.refusal),
  };
}

function mostCommon(values: readonly (string | null)[]): string | null {
  const counts = new Map<string, number>();
  for (const v of values) if (v) counts.set(v, (counts.get(v) ?? 0) + 1);
  let best: string | null = null;
  let bestCount = 0;
  for (const [v, n] of counts) {
    if (n > bestCount) {
      best = v;
      bestCount = n;
    }
  }
  return best;
}

/**
 * The experiment view of a run. Only replayable cases are statistics'
 * cases; every sampled case is listed. `includeCases: false` leaves out
 * `cases` and `fields`, and the floor's per-case lists.
 */
export function buildView(run: RunFile, { includeCases = true }: { includeCases?: boolean } = {}): ExperimentView {
  const tracked = run.settings.trackedFields ?? [];
  for (const path of tracked) parseJsonPath(path); // throws on a path no statistic can read

  const replays = new Map<string, Replay>();
  for (const r of run.replays) {
    const key = `${r.caseId}\u0000${r.arm}`;
    if (!replays.has(key)) replays.set(key, r);
  }
  const outcomeOf = (caseId: string, arm: string) => replayOutcome(replays.get(`${caseId}\u0000${arm}`), tracked);

  const replayed = run.cases.filter((c) => c.notReplayable === null);
  const records = new Map(run.cases.map((c) => [c.id, controlRecord(c, tracked)]));
  const controlOf = (c: Case) => records.get(c.id) as ControlRecord;
  const controls = replayed.map((c) => controlOutcome(controlOf(c)));

  const arms: ArmView[] = run.treatments.map((t) => {
    const ran = replayed.filter((c) => outcomeOf(c.id, t.id) !== undefined);
    const outcomes = ran.map((c) => outcomeOf(c.id, t.id) as Outcome);
    const aligned = ran.map((c) => controlOutcome(controlOf(c)));
    const summary = { ...summarize(outcomes, aligned), temperature_ignored_with_reasoning: 0 };
    // The control over exactly the cases this arm returned on, so the two compare.
    const controlSummary = summarize(aligned.filter((_, i) => !outcomes[i]?.error));
    return {
      id: t.id,
      label: t.label,
      model: t.model,
      provider: t.endpoint,
      settings: treatmentSettings(t),
      temperature_ignored_with_reasoning: false,
      web_search_fees_missing: false,
      summary,
      control_summary: controlSummary,
    };
  });

  let fields: ExperimentView["fields"] = null;
  if (includeCases) {
    fields = {};
    for (const path of tracked) {
      const series: Record<string, unknown[]> = { control: controls.map((control) => fieldValue(control, path)) };
      for (const t of run.treatments) series[t.label] = replayed.map((c) => fieldValue(outcomeOf(c.id, t.id), path));
      fields[path] = series;
    }
  }

  const hasFloor = run.settings.noiseFloor || run.replays.some((r) => r.arm === "floor_a1" || r.arm === "floor_a2");
  let cases: CaseView[] | null = null;
  if (includeCases) {
    cases = run.cases.map((c) => {
      const control = controlOf(c);
      const armViews: Record<string, ResultView> = {};
      for (const t of run.treatments) {
        const outcome = outcomeOf(c.id, t.id);
        if (outcome) armViews[t.label] = resultView(outcome);
      }
      const view: CaseView = {
        trace_id: c.traceId ?? c.id,
        case_id: c.id,
        status: c.notReplayable === null ? "replayed" : "not_replayable",
        reason: c.notReplayable,
        input_preview: c.inputPreview,
        limitations: [...c.missing],
        control: {
          output: control.output ?? null,
          cost_usd: control.cost_usd ?? null,
          cost_search_usd: null,
          latency_ms: control.latency_ms ?? null,
          finish_reason: control.finish_reason ?? null,
          output_tokens: control.output_tokens ?? null,
          output_truncated: false,
          refusal: Boolean(control.refusal),
        },
        arms: armViews,
      };
      if (hasFloor) {
        const floor: NonNullable<CaseView["noise_floor"]> = {};
        const a1 = outcomeOf(c.id, "floor_a1");
        const a2 = outcomeOf(c.id, "floor_a2");
        if (a1) floor.a1 = resultView(a1);
        if (a2) floor.a2 = resultView(a2);
        view.noise_floor = floor;
      }
      return view;
    });
  }

  const floorCases: FloorCase[] = run.cases.map((c, position) => ({
    position,
    trace_id: c.traceId ?? c.id,
    status: c.notReplayable === null ? "replayed" : "not_replayable",
    control: controlOf(c),
  }));
  const floorInput: FloorInput = {
    noise_floor: hasFloor
      ? { replays: FLOOR_ARMS.length, gateway_models: replayed.filter((c) => outcomeOf(c.id, "floor_a1") || outcomeOf(c.id, "floor_a2")).map((c) => c.model).filter((m): m is string => Boolean(m)) }
      : null,
    cases: floorCases,
    noise_results: run.cases.flatMap((c, position) =>
      FLOOR_ARMS.flatMap((arm, i) => {
        const outcome = outcomeOf(c.id, arm);
        return outcome ? [{ position, replay: i + 1, outcome }] : [];
      }),
    ),
    tracked_fields: tracked,
  };

  const modelsSeen: Record<string, number> = {};
  for (const c of run.cases) if (c.model) modelsSeen[c.model] = (modelsSeen[c.model] ?? 0) + 1;
  const settingsSeen: Record<string, unknown[]> = {};
  for (const c of replayed) {
    for (const [key, value] of Object.entries(caseSettings(c))) {
      const values = (settingsSeen[key] ??= []);
      if (!values.includes(value)) values.push(value);
    }
  }

  return {
    status: "done",
    stopped: run.stopped,
    sample: {
      size: run.settings.sample,
      cases: run.cases.length,
      replayed: replayed.length,
      not_replayable: run.cases.length - replayed.length,
    },
    tracked_fields: [...tracked],
    estimate_usd: run.estimate.expectedUsd,
    budget_usd: run.settings.budgetUsd,
    spent_usd: run.spentUsd,
    created_at: run.createdAt,
    control: {
      model: mostCommon(replayed.map((c) => c.model)),
      provider: mostCommon(replayed.map((c) => c.provider)),
      settings_seen: settingsSeen,
      models_seen: modelsSeen,
      // Same keys compares a treatment with the control, so the control has none.
      summary: summarize(controls),
    },
    arms,
    fields,
    cases,
    judge: null,
    noise_floor: noiseFloorView(floorInput, { includeCases }),
    similarity: null,
  };
}
