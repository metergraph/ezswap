// Replay a planned run: every replayable case on every treatment, and twice
// on production's model for the noise floor.
//
// Budget: each call reserves its upper bound before it is sent (free
// endpoints reserve nothing), and the run admits a case only when all of the
// case's calls fit in the budget next to what is spent and still reserved.
// So a budget that runs out drops whole cases, never one column. A case that
// does not fit waits for calls in flight to settle; when nothing is in flight
// and it still does not fit, the run stops ("budget"). A call that cost more
// than it reserved stops the run at once ("reservation_exceeded"): a bound
// that was wrong once may be wrong again. A stop signal ends it the same way
// ("interrupted"), after the calls in flight finish.
//
// Cost of a call: what the endpoint reported, else the catalog price of the
// usage it returned, else its reservation (a reply with no usable count, or a
// failure that may have reached the model). Free endpoints cost zero.

import { floorCall, floorModel, treatmentCall, upperBoundUsd } from "../pricing/estimate.ts";
import type { ArmId, Case, Pricing, Provider, Replay, ReplayCall, ReplyOutcome, RunFile } from "../types.ts";
import type { RunPlan } from "./plan.ts";

export const DEFAULT_RUN_CONCURRENCY = 4;
export const FLOOR_ARMS = ["floor_a1", "floor_a2"] as const;
const EPSILON = 1e-9;

export interface Progress {
  done: number;
  total: number;
  spentUsd: number;
  reservedUsd: number;
}

export interface ExecuteOptions {
  /** One provider per endpoint key the plan names. */
  providers: Record<string, Provider>;
  pricing: Pricing;
  /** Default: the plan's budget. */
  budgetUsd?: number;
  /** Aborting stops new calls; calls in flight finish and are recorded. */
  signal?: AbortSignal;
  /** Aborting cancels calls in flight too. */
  hardSignal?: AbortSignal;
  concurrency?: number;
  onProgress?: (progress: Progress) => void;
  /** Clock for createdAt and for pricing. */
  now?: () => Date;
  toolVersion?: string;
}

export class ExecuteError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ExecuteError";
  }
}

interface Item {
  item: Case;
  arm: ArmId;
  endpoint: string;
  call: ReplayCall | null;
  /** Null when the call cannot be priced (allowed unpriced) or never runs. */
  reserve: number | null;
  free: boolean;
  /** Set when the replay is known before any call: why it is skipped. */
  skip: string | null;
}

function round8(value: number): number {
  return Math.round(value * 1e8) / 1e8;
}

function skipped(item: Case, arm: ArmId, code: string, message: string): Replay {
  return {
    caseId: item.id,
    arm,
    status: "skipped",
    output: null,
    finishReason: null,
    refusal: false,
    usage: null,
    costUsd: null,
    costSource: null,
    latencyMs: null,
    flags: [],
    error: { code, message },
  };
}

const SKIP_MESSAGES: Record<string, string> = {
  budget_exhausted: "not sent: the budget could not cover this case",
  interrupted: "not sent: the run was interrupted",
  reservation_exceeded: "not sent: a call cost more than its reservation, so the run stopped",
  floor_model_unknown: "not sent: production's model is not recorded for this case",
};

/** What a settled call cost, and where the figure came from. */
export function settleCost(
  outcome: ReplyOutcome,
  call: ReplayCall,
  reserve: number | null,
  free: boolean,
  pricing: Pricing,
  at?: string,
): { costUsd: number | null; costSource: Replay["costSource"] } {
  if (free) return { costUsd: 0, costSource: "free" };
  if (outcome.status === "ok") {
    if (outcome.reportedCostUsd !== null && Number.isFinite(outcome.reportedCostUsd)) {
      return { costUsd: outcome.reportedCostUsd, costSource: "endpoint" };
    }
    const usage = outcome.usage;
    if (usage && usage.inputTokens !== null && usage.outputTokens !== null) {
      const priced = pricing.price({
        model: call.model,
        inputTokens: usage.inputTokens,
        outputTokens: usage.outputTokens,
        cachedInputTokens: usage.cachedInputTokens,
        // An OpenAI-compatible endpoint's prompt count includes cached tokens.
        inputIncludesCacheRead: true,
        ...(at !== undefined ? { at } : {}),
      });
      if (priced !== null) return { costUsd: priced, costSource: "catalog" };
    }
    return reserve === null ? { costUsd: null, costSource: null } : { costUsd: reserve, costSource: "reservation" };
  }
  if (outcome.flags.includes("maybe_billed")) {
    return reserve === null ? { costUsd: null, costSource: null } : { costUsd: reserve, costSource: "reservation" };
  }
  return { costUsd: 0, costSource: null };
}

function crashed(): ReplyOutcome {
  return {
    status: "failed",
    output: null,
    finishReason: null,
    refusal: false,
    usage: null,
    reportedCostUsd: null,
    latencyMs: null,
    flags: ["maybe_billed"],
    error: { code: "provider_error", message: "the call failed unexpectedly" },
  };
}

export async function executeRun(plan: RunPlan, options: ExecuteOptions): Promise<RunFile> {
  const now = options.now ?? (() => new Date());
  const createdAt = now().toISOString();
  const at = createdAt;
  const budget = options.budgetUsd ?? plan.settings.budgetUsd;
  const concurrency = Math.max(1, options.concurrency ?? DEFAULT_RUN_CONCURRENCY);
  const pricing = options.pricing;
  const free = new Set(plan.freeEndpoints);

  const endpoints = new Set(plan.treatments.map((t) => t.endpoint));
  if (plan.floorEndpoint) endpoints.add(plan.floorEndpoint);
  for (const endpoint of endpoints) {
    if (!options.providers[endpoint]) throw new ExecuteError(`No provider for the endpoint "${endpoint}".`);
  }

  const armOrder: ArmId[] = [...plan.treatments.map((t) => t.id), ...(plan.floorEndpoint ? FLOOR_ARMS : [])];
  const replayable = plan.cases.filter((c) => c.notReplayable === null);

  const itemsFor = (item: Case): Item[] => {
    const items: Item[] = [];
    for (const t of plan.treatments) {
      const call = treatmentCall(item, t);
      const isFree = free.has(t.endpoint);
      const reserve = isFree ? 0 : upperBoundUsd(call, pricing, item.production.usage?.inputTokens ?? null, at);
      items.push({ item, arm: t.id, endpoint: t.endpoint, call, reserve, free: isFree, skip: null });
    }
    if (plan.floorEndpoint) {
      const endpoint = plan.floorEndpoint;
      const isFree = free.has(endpoint);
      const model = floorModel(item, pricing, at);
      for (const arm of FLOOR_ARMS) {
        if (model === null) {
          items.push({ item, arm, endpoint, call: null, reserve: null, free: isFree, skip: "floor_model_unknown" });
          continue;
        }
        const call = floorCall(item, model);
        const reserve = isFree ? 0 : upperBoundUsd(call, pricing, item.production.usage?.inputTokens ?? null, at);
        items.push({ item, arm, endpoint, call, reserve, free: isFree, skip: null });
      }
    }
    return items;
  };

  const results = new Map<string, Replay>();
  const keyOf = (caseId: string, arm: ArmId) => `${caseId}\u0000${arm}`;
  const total = replayable.length * armOrder.length;
  let done = 0;
  let spent = 0;
  let reserved = 0;
  let stopped: RunFile["stopped"] = null;
  const stop = (reason: NonNullable<RunFile["stopped"]>) => {
    const rank = { budget: 1, interrupted: 2, reservation_exceeded: 3 } as const;
    if (stopped === null || rank[reason] > rank[stopped]) stopped = reason;
  };
  const progress = () =>
    options.onProgress?.({ done, total, spentUsd: round8(spent), reservedUsd: round8(reserved) });

  const queue = [...replayable];
  const pending: Item[] = [];
  const inFlight = new Set<Promise<void>>();
  let admitting = true;

  const record = (replay: Replay) => {
    results.set(keyOf(replay.caseId, replay.arm), replay);
    done++;
  };

  const start = (entry: Item) => {
    const call = entry.call!;
    const provider = options.providers[entry.endpoint]!;
    const task = (async () => {
      let outcome: ReplyOutcome;
      try {
        outcome = await provider.complete(call, options.hardSignal);
      } catch {
        // A provider never throws for a failed call; anything else may have reached the model.
        outcome = crashed();
      }
      const { costUsd, costSource } = settleCost(outcome, call, entry.reserve, entry.free, pricing, at);
      reserved -= entry.reserve ?? 0;
      spent += costUsd ?? 0;
      record({
        caseId: entry.item.id,
        arm: entry.arm,
        status: outcome.status,
        output: outcome.output,
        finishReason: outcome.finishReason,
        refusal: outcome.refusal,
        usage: outcome.usage,
        costUsd: costUsd === null ? null : round8(costUsd),
        costSource,
        latencyMs: outcome.latencyMs,
        flags: [...outcome.flags],
        error: outcome.error,
      });
      if (entry.reserve !== null && costUsd !== null && costUsd > entry.reserve + EPSILON) {
        stop("reservation_exceeded");
      }
      progress();
    })();
    const tracked = task.finally(() => inFlight.delete(tracked));
    inFlight.add(tracked);
  };

  for (;;) {
    while (inFlight.size < concurrency) {
      if (options.signal?.aborted && (pending.length > 0 || queue.length > 0)) stop("interrupted");
      if (stopped === "interrupted" || stopped === "reservation_exceeded") break;
      const next = pending.shift();
      if (next) {
        start(next);
        continue;
      }
      if (!admitting) break;
      const kase = queue[0];
      if (!kase) break;
      const items = itemsFor(kase);
      const need = items.reduce((sum, entry) => sum + (entry.skip ? 0 : (entry.reserve ?? 0)), 0);
      if (spent + reserved + need > budget + EPSILON) {
        // Calls in flight may settle below what they hold: wait for them first.
        if (inFlight.size > 0) break;
        stop("budget");
        admitting = false;
        break;
      }
      queue.shift();
      reserved += need;
      for (const entry of items) {
        if (entry.skip) record(skipped(entry.item, entry.arm, entry.skip, SKIP_MESSAGES[entry.skip]!));
        else pending.push(entry);
      }
    }
    if (inFlight.size === 0) break;
    await Promise.race(inFlight);
  }

  // Whatever was not sent is recorded as skipped, with the reason.
  const reason: NonNullable<RunFile["stopped"]> = (stopped as RunFile["stopped"]) ?? "interrupted";
  const code = reason === "budget" ? "budget_exhausted" : reason;
  for (const entry of pending) {
    reserved -= entry.reserve ?? 0;
    record(skipped(entry.item, entry.arm, code, SKIP_MESSAGES[code]!));
  }
  for (const kase of queue) {
    for (const arm of armOrder) record(skipped(kase, arm, code, SKIP_MESSAGES[code]!));
  }
  progress();

  const replays: Replay[] = [];
  for (const kase of replayable) {
    for (const arm of armOrder) {
      const replay = results.get(keyOf(kase.id, arm));
      if (replay) replays.push(replay);
    }
  }

  return {
    schema: "metergraph.replay-run/v1",
    createdAt,
    tool: { name: "metergraph-replay", version: options.toolVersion ?? "0.0.0" },
    input: { ...plan.input },
    catalog: { version: plan.catalogVersion },
    settings: { ...plan.settings, trackedFields: [...plan.settings.trackedFields], budgetUsd: budget },
    treatments: plan.treatments.map((t) => ({ ...t, settings: { ...t.settings } })),
    cases: plan.cases,
    replays,
    estimate: plan.estimate,
    spentUsd: round8(spent),
    stopped,
  };
}
