// What a run is expected to cost, and the most it can spend.
//
// The estimate is the hosted product's: each replayable case priced at
// production's input count (or 2000) and an assumed output (the larger of
// production's output, or 512, and half the cap the replay is sent with, never
// above that cap), times a 1.25 margin, rounded up to $0.0001 per arm. The
// noise floor adds two replays per case on production's model at production's
// settings.
//
// The upper bound is what each call reserves before it is sent: one token per
// UTF-8 byte of everything sent (system, messages, the response_format and the
// forced tool, as JSON), framing overhead per message, and the full max_tokens,
// since reasoning bills as output inside that cap. Production's own input
// count, from another tokenizer, is only ever a floor.

import type { ArmId, Case, Estimate, NotReplayableReason, Pricing, ReplayCall, Treatment } from "../types.ts";
import { forcedToolRequest } from "../providers/payload.ts";
import { isFreeEndpoint } from "../providers/index.ts";
import { dec, mul, numberToUnits, unitsToNumber, unitsUp } from "./decimal.ts";
import type { CatalogPricing } from "./catalog.ts";

export const DEFAULT_MAX_TOKENS = 4096;
export const FALLBACK_INPUT_TOKENS = 2000;
export const FALLBACK_OUTPUT_TOKENS = 512;
export const ESTIMATE_MARGIN = "1.25";
export const NOISE_REPLAYS = 2;
export const RESERVATION_TOKENS_PER_MESSAGE = 16;
export const RESERVATION_BASE_TOKENS = 64;
const COST_PLACES = 8;
const ESTIMATE_PLACES = 4;

export interface EstimateOptions {
  noiseFloor: boolean;
  /** ISO time to price at. Default now. */
  at?: string;
  /** A treatment that costs nothing. Default: the Ollama preset. */
  isFree?: (treatment: Treatment) => boolean;
}

/** The max_tokens a replay is sent with: the treatment's, else production's cap, else 4096. */
export function capSent(treatmentMaxTokens: number | null, productionMaxTokens: number | null): number {
  return treatmentMaxTokens || productionMaxTokens || DEFAULT_MAX_TOKENS;
}

/**
 * The output an estimate assumes for one replay: the larger of production's
 * output and half the cap, never above the cap. A treatment often writes more
 * than production did (a reasoning model thinks, and that bills as output).
 */
export function assumedOutputTokens(cap: number, productionOutputTokens: number | null): number {
  const control = productionOutputTokens || FALLBACK_OUTPUT_TOKENS;
  return Math.min(Math.max(control, Math.floor(cap / 2)), cap);
}

/** The call a treatment sends for one case. */
export function treatmentCall(item: Case, treatment: Treatment): ReplayCall {
  return {
    model: treatment.model,
    system: item.system,
    messages: item.messages,
    maxTokens: capSent(treatment.settings.maxTokens, item.params.maxTokens),
    temperature: treatment.settings.temperature ?? item.params.temperature,
    topP: item.params.topP,
    stop: item.params.stop,
    reasoningEffort: treatment.settings.reasoningEffort ?? item.params.reasoningEffort,
    responseFormat: item.responseFormat,
    forcedTool: item.forcedTool,
  };
}

/** A noise-floor call: production's model at production's settings. */
export function floorCall(item: Case, model: string): ReplayCall {
  return {
    model,
    system: item.system,
    messages: item.messages,
    maxTokens: capSent(null, item.params.maxTokens),
    temperature: item.params.temperature,
    topP: item.params.topP,
    stop: item.params.stop,
    reasoningEffort: item.params.reasoningEffort,
    responseFormat: item.responseFormat,
    forcedTool: item.forcedTool,
  };
}

/**
 * The model id the noise floor replays production on: the catalog's
 * canonical id when it can resolve production's model, else the model as
 * captured (with its provider as creator when it has no slash). Null without
 * a model.
 */
export function floorModel(item: Case, pricing: Pricing, at?: string): string | null {
  const resolver = (pricing as Partial<CatalogPricing>).resolveProduction;
  if (typeof resolver === "function") {
    const resolved = resolver.call(pricing, item.provider, item.model, at);
    if (resolved !== null) return resolved;
  }
  const model = item.model?.trim();
  if (!model) return null;
  if (model.includes("/") || !item.provider) return model;
  return `${item.provider.trim().toLowerCase()}/${model}`;
}

// ---------------------------------------------------------------------------
// JSON as the hosted product measures it (Python's json.dumps defaults:
// ", " and ": " separators, non-ASCII escaped), so a bound here is the bound
// there.

function pyString(value: string): string {
  let out = '"';
  for (let i = 0; i < value.length; i += 1) {
    const code = value.charCodeAt(i);
    const char = value[i];
    if (char === '"') out += '\\"';
    else if (char === "\\") out += "\\\\";
    else if (char === "\n") out += "\\n";
    else if (char === "\r") out += "\\r";
    else if (char === "\t") out += "\\t";
    else if (char === "\b") out += "\\b";
    else if (char === "\f") out += "\\f";
    else if (code < 0x20 || code > 0x7e) {
      out += code === 0x7f ? char : `\\u${code.toString(16).padStart(4, "0")}`;
    } else out += char;
  }
  return out + '"';
}

function pyNumber(value: number): string {
  if (!Number.isFinite(value)) return value > 0 ? "Infinity" : value < 0 ? "-Infinity" : "NaN";
  if (Number.isInteger(value) && Math.abs(value) < 1e16) return String(value);
  const magnitude = Math.abs(value);
  if (magnitude >= 1e16 || magnitude < 1e-4) {
    const [mantissa, exponent = "0"] = value.toExponential().split("e");
    const sign = exponent.startsWith("-") ? "-" : "+";
    const digits = exponent.replace(/^[+-]/, "").padStart(2, "0");
    return `${mantissa}e${sign}${digits}`;
  }
  return String(value);
}

export function pythonJson(value: unknown): string {
  if (value === null || value === undefined) return "null";
  if (value === true) return "true";
  if (value === false) return "false";
  if (typeof value === "number") return pyNumber(value);
  if (typeof value === "string") return pyString(value);
  if (Array.isArray(value)) return `[${value.map(pythonJson).join(", ")}]`;
  if (typeof value === "object") {
    const entries = Object.entries(value as Record<string, unknown>).filter(([, item]) => item !== undefined);
    return `{${entries.map(([key, item]) => `${pyString(key)}: ${pythonJson(item)}`).join(", ")}}`;
  }
  return pyString(String(value));
}

const utf8 = new TextEncoder();

/** A true upper bound on the input tokens of the request this call sends. */
export function reservationInputTokens(call: ReplayCall, productionInputTokens?: number | null): number {
  let bytes = utf8.encode(call.system ?? "").byteLength;
  for (const message of call.messages) bytes += utf8.encode(message.content).byteLength;
  if (call.responseFormat) bytes += pythonJson(call.responseFormat).length;
  if (call.forcedTool) bytes += pythonJson(forcedToolRequest(call.forcedTool)).length;
  const bound = bytes + RESERVATION_TOKENS_PER_MESSAGE * (call.messages.length + 1) + RESERVATION_BASE_TOKENS;
  return Math.max(bound, productionInputTokens ?? 0);
}

/**
 * What one call reserves before it is sent: the input bound and the full
 * max_tokens, priced. Null when the catalog cannot price the model; such a
 * call cannot be budgeted.
 */
export function upperBoundUsd(
  call: ReplayCall,
  pricing: Pricing,
  productionInputTokens?: number | null,
  at?: string,
): number | null {
  return pricing.price({
    model: call.model,
    inputTokens: reservationInputTokens(call, productionInputTokens),
    outputTokens: call.maxTokens,
    cachedInputTokens: null,
    inputIncludesCacheRead: null,
    ...(at !== undefined ? { at } : {}),
  });
}

// ---------------------------------------------------------------------------
// The run's estimate

const MARGIN = dec(ESTIMATE_MARGIN)!;

/** Costs in 1e-8 units, times the margin, rounded up to $0.0001, in 1e-8 units. */
function withMargin(units: bigint): bigint {
  const scaled = mul({ n: units, s: COST_PLACES }, MARGIN);
  return unitsUp(scaled, ESTIMATE_PLACES) * 10n ** BigInt(COST_PLACES - ESTIMATE_PLACES);
}

function usd(units: bigint): number {
  return unitsToNumber(units, COST_PLACES);
}

export function estimateRun(
  cases: Case[],
  treatments: Treatment[],
  options: EstimateOptions,
  pricing: Pricing,
): Estimate {
  const isFree = options.isFree ?? ((treatment: Treatment) => isFreeEndpoint(treatment.endpoint));
  const at = options.at;
  const replayable = cases.filter((item) => item.notReplayable === null);
  const notReplayable: Partial<Record<NotReplayableReason, number>> = {};
  for (const item of cases) {
    if (item.notReplayable !== null) notReplayable[item.notReplayable] = (notReplayable[item.notReplayable] ?? 0) + 1;
  }
  const unpriced = new Set<string>();
  const perArm: Record<ArmId, { expectedUsd: number; upperBoundUsd: number }> = {};
  let expected = 0n;
  let upper = 0n;

  const priceUnits = (model: string, inputTokens: number, outputTokens: number): bigint | null => {
    const cost = pricing.price({
      model,
      inputTokens,
      outputTokens,
      cachedInputTokens: null,
      inputIncludesCacheRead: null,
      ...(at !== undefined ? { at } : {}),
    });
    return cost === null ? null : numberToUnits(cost, COST_PLACES);
  };
  const boundUnits = (call: ReplayCall, productionInput: number | null): bigint | null => {
    const bound = upperBoundUsd(call, pricing, productionInput, at);
    return bound === null ? null : numberToUnits(bound, COST_PLACES);
  };

  for (const treatment of treatments) {
    let armExpected = 0n;
    let armUpper = 0n;
    if (!isFree(treatment)) {
      for (const item of replayable) {
        const usage = item.production.usage;
        const call = treatmentCall(item, treatment);
        const cost = priceUnits(
          treatment.model,
          usage?.inputTokens || FALLBACK_INPUT_TOKENS,
          assumedOutputTokens(call.maxTokens, usage?.outputTokens ?? null),
        );
        const bound = boundUnits(call, usage?.inputTokens ?? null);
        if (cost === null || bound === null) {
          unpriced.add(treatment.model);
          continue;
        }
        armExpected += cost;
        armUpper += bound;
      }
      armExpected = withMargin(armExpected);
    }
    perArm[treatment.id] = { expectedUsd: usd(armExpected), upperBoundUsd: usd(armUpper) };
    expected += armExpected;
    upper += armUpper;
  }

  if (options.noiseFloor) {
    let floorExpected = 0n;
    let floorUpper = 0n;
    for (const item of replayable) {
      const model = floorModel(item, pricing, at);
      if (model === null) {
        unpriced.add("(production model not recorded)");
        continue;
      }
      const usage = item.production.usage;
      const call = floorCall(item, model);
      const cost = priceUnits(
        model,
        usage?.inputTokens || FALLBACK_INPUT_TOKENS,
        assumedOutputTokens(call.maxTokens, usage?.outputTokens ?? null),
      );
      const bound = boundUnits(call, usage?.inputTokens ?? null);
      if (cost === null || bound === null) {
        unpriced.add(model);
        continue;
      }
      floorExpected += cost * BigInt(NOISE_REPLAYS);
      floorUpper += bound;
    }
    // The hosted floor share: both replays, with one margin and one rounding.
    const share = withMargin(floorExpected);
    for (const arm of ["floor_a1", "floor_a2"] as const) {
      perArm[arm] = { expectedUsd: usd(share) / NOISE_REPLAYS, upperBoundUsd: usd(floorUpper) };
    }
    expected += share;
    upper += floorUpper * BigInt(NOISE_REPLAYS);
  }

  return {
    replayable: replayable.length,
    notReplayable,
    expectedUsd: usd(expected),
    upperBoundUsd: usd(upper),
    perArm,
    unpriced: [...unpriced].sort(),
  };
}
