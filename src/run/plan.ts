// Plan a run: read the trace file, sample the latest cases, name the
// treatments and estimate the spend. No provider call.

import { basename } from "node:path";
import { readTraces } from "../adapters/index.ts";
import { prepareCase } from "../cases/index.ts";
import { estimateRun } from "../pricing/estimate.ts";
import type { Case, Estimate, Pricing, RunFile, Treatment, TreatmentSettings } from "../types.ts";

export const DEFAULT_SAMPLE = 20;
export const MAX_SAMPLE = 200;
export const MAX_TRACKED_FIELDS = 10;
export const DEFAULT_BUDGET_USD = 1;

export interface TreatmentInput {
  model: string;
  /** The endpoint's key: a preset name or a base URL's host. */
  endpoint: string;
  settings?: Partial<TreatmentSettings>;
  label?: string;
}

export interface PlanOptions {
  /** Trace file path. Only its base name is recorded. */
  file: string;
  adapter?: string;
  sample?: number;
  treatments: TreatmentInput[];
  noiseFloor?: boolean;
  /** Endpoint key the noise floor runs on. Default: the first treatment's. */
  floorEndpoint?: string;
  trackedFields?: string[];
  budgetUsd?: number;
  allowNoSearch?: boolean;
  pricing: Pricing;
  /** Endpoints that cost nothing (local servers). */
  isFreeEndpoint?: (endpoint: string) => boolean;
  /** ISO time to price at. Default now. */
  at?: string;
}

export interface RunPlan {
  input: { file: string; adapter: string };
  /** What the file held, before sampling. */
  file: { calls: number; traces: number; skippedLines: number };
  /** The sampled cases, replayable or not, oldest first. */
  cases: Case[];
  treatments: Treatment[];
  /** Endpoint key of the noise floor, or null without one. */
  floorEndpoint: string | null;
  freeEndpoints: string[];
  settings: RunFile["settings"];
  estimate: Estimate;
  catalogVersion: string;
}

export class PlanError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PlanError";
  }
}

function settingsSummary(settings: TreatmentSettings): string {
  const parts: string[] = [];
  if (settings.temperature !== null) parts.push(`temperature=${settings.temperature}`);
  if (settings.maxTokens !== null) parts.push(`max_tokens=${settings.maxTokens}`);
  if (settings.reasoningEffort !== null) parts.push(`reasoning_effort=${settings.reasoningEffort}`);
  return parts.join(",");
}

/** Treatments t1, t2, ... with labels that are unique within the run. */
export function buildTreatments(inputs: TreatmentInput[]): Treatment[] {
  const treatments: Treatment[] = inputs.map((input, i) => ({
    id: `t${i + 1}`,
    label: input.label?.trim() || input.model,
    model: input.model,
    endpoint: input.endpoint,
    settings: {
      temperature: input.settings?.temperature ?? null,
      maxTokens: input.settings?.maxTokens ?? null,
      reasoningEffort: input.settings?.reasoningEffort ?? null,
    },
  }));
  const count = (label: string) => treatments.filter((t) => t.label === label).length;
  for (const t of treatments) {
    if (count(t.label) > 1 && !inputs[Number(t.id.slice(1)) - 1]?.label) {
      const summary = settingsSummary(t.settings);
      const sameEndpoint = treatments.every((other) => other.label !== t.label || other.endpoint === t.endpoint);
      if (!sameEndpoint) t.label = `${t.label} (${t.endpoint})`;
      else if (summary) t.label = `${t.label} @${summary}`;
    }
  }
  const seen = new Map<string, number>();
  for (const t of treatments) {
    const n = (seen.get(t.label) ?? 0) + 1;
    seen.set(t.label, n);
    if (n > 1) t.label = `${t.label} #${n}`;
  }
  return treatments;
}

function timeOf(item: Case): number | null {
  if (!item.ts) return null;
  const t = Date.parse(item.ts);
  return Number.isNaN(t) ? null : t;
}

/**
 * The latest `size` replayable cases, one per trace id where calls have one
 * (the trace's latest replayable call, else its latest call), oldest first.
 * The cases that cannot be replayed among them are kept, so the report says
 * what was left out and why. Cases are ordered by time when every one has a
 * time, else by their order in the file.
 */
export function sampleCases(all: Case[], size: number): Case[] {
  const ordered = all.map((item, index) => ({ item, index, time: timeOf(item) }));
  if (ordered.every((entry) => entry.time !== null)) {
    ordered.sort((a, b) => a.time! - b.time! || a.index - b.index);
  }
  // One representative per trace: its latest replayable call, else its latest call.
  const groups = new Map<string, { item: Case; rank: number }>();
  ordered.forEach((entry, rank) => {
    const key = entry.item.traceId ? `trace:${entry.item.traceId}` : `call:${entry.index}`;
    const current = groups.get(key);
    const replayable = entry.item.notReplayable === null;
    if (!current || replayable || current.item.notReplayable !== null) {
      groups.set(key, { item: entry.item, rank });
    }
  });
  const representatives = [...groups.values()].sort((a, b) => b.rank - a.rank);
  const picked: { item: Case; rank: number }[] = [];
  let replayable = 0;
  let notReplayable = 0;
  for (const entry of representatives) {
    if (replayable >= size) break;
    if (entry.item.notReplayable === null) {
      replayable++;
      picked.push(entry);
    } else if (notReplayable < size) {
      notReplayable++;
      picked.push(entry);
    }
  }
  return picked.sort((a, b) => a.rank - b.rank).map((entry) => entry.item);
}

export async function planRun(options: PlanOptions): Promise<RunPlan> {
  const sample = options.sample ?? DEFAULT_SAMPLE;
  if (!Number.isInteger(sample) || sample < 1 || sample > MAX_SAMPLE) {
    throw new PlanError(`The sample must be a whole number from 1 to ${MAX_SAMPLE}.`);
  }
  const tracked = options.trackedFields ?? [];
  if (tracked.length > MAX_TRACKED_FIELDS) {
    throw new PlanError(`Track at most ${MAX_TRACKED_FIELDS} fields.`);
  }
  if (options.treatments.length === 0) throw new PlanError("Name at least one model to replay on.");
  const budgetUsd = options.budgetUsd ?? DEFAULT_BUDGET_USD;
  if (!Number.isFinite(budgetUsd) || budgetUsd < 0) throw new PlanError("The budget must be a positive amount.");

  let skippedLines = 0;
  const calls = await readTraces(options.file, {
    ...(options.adapter ? { adapter: options.adapter } : {}),
    onSkip: () => {
      skippedLines++;
    },
  });
  const all = calls.map((call) => prepareCase(call, { allowNoSearch: options.allowNoSearch ?? false }));
  const traces = new Set(calls.map((call, i) => call.traceId ?? `call:${i}`)).size;
  const cases = sampleCases(all, sample);

  const treatments = buildTreatments(options.treatments);
  const isFree = options.isFreeEndpoint ?? (() => false);
  const noiseFloor = options.noiseFloor ?? true;
  const floorEndpoint = noiseFloor ? (options.floorEndpoint ?? treatments[0]!.endpoint) : null;
  const endpoints = new Set([...treatments.map((t) => t.endpoint), ...(floorEndpoint ? [floorEndpoint] : [])]);
  const freeEndpoints = [...endpoints].filter(isFree).sort();

  // A noise floor on a local endpoint costs nothing and needs no price.
  const freeFloor = floorEndpoint !== null && isFree(floorEndpoint);
  const estimate = estimateRun(
    cases,
    treatments,
    {
      noiseFloor: noiseFloor && !freeFloor,
      isFree: (t) => isFree(t.endpoint),
      ...(options.at !== undefined ? { at: options.at } : {}),
    },
    options.pricing,
  );
  if (freeFloor) {
    for (const arm of ["floor_a1", "floor_a2"] as const) estimate.perArm[arm] = { expectedUsd: 0, upperBoundUsd: 0 };
  }

  return {
    input: { file: basename(options.file), adapter: calls[0]?.source ?? options.adapter ?? "unknown" },
    file: { calls: calls.length, traces, skippedLines },
    cases,
    treatments,
    floorEndpoint,
    freeEndpoints,
    settings: { sample, trackedFields: [...tracked], noiseFloor, budgetUsd },
    estimate,
    catalogVersion: options.pricing.catalogVersion,
  };
}
