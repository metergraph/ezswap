// The price catalog and cost function.
//
// The catalog is metergraph-core's public prices.yaml, converted to JSON at
// build time (scripts/build-prices.mjs). Resolution follows metergraph-core:
// an alias is looked up by (provider, model) and is effective-dated and
// region-pinned; the price is the row effective at the call's time for the
// alias's model and channel; cache reads come out of the input count where the
// publisher reports them inside it; long-context and off-peak multipliers
// apply to every rate. A gateway model id ("creator/model") is looked up under
// its creator, as the hosted product prices replays.
//
// Reasoning tokens bill as output: they are inside the output count and the
// catalog has no separate rate for them.

import type { PriceQuery, Pricing } from "../types.ts";
import catalogDocument from "./prices.json" with { type: "json" };
import {
  type Dec,
  ONE,
  ZERO,
  add,
  dec,
  int,
  mul,
  shift,
  unitsHalfUp,
  unitsToNumber,
} from "./decimal.ts";

// ---------------------------------------------------------------------------
// Catalog document

export interface CatalogDocument {
  source: { repository: string; path: string; commit: string; license: string };
  version: string;
  currency: string;
  pricing_verified_at: string | null;
  models: CatalogModel[];
}

export interface CatalogModel {
  canonical_id: string;
  publisher: string | null;
  aliases: CatalogAlias[];
  prices: CatalogPrice[];
}

export interface CatalogAlias {
  provider: string;
  alias: string;
  channel: string;
  rules?: Record<string, unknown>;
  effective_from?: string;
  effective_to?: string;
  price_region?: string[];
}

export interface CatalogPrice {
  channel: string;
  region: string;
  effective_from: string;
  effective_to?: string;
  input_per_mtok?: string;
  output_per_mtok?: string;
  cache_read_per_mtok?: string;
  cache_write_5m_per_mtok?: string;
  cache_write_1h_per_mtok?: string;
  batch_input_per_mtok?: string;
  batch_output_per_mtok?: string;
  rules?: Record<string, unknown>;
  source_url: string;
}

// ---------------------------------------------------------------------------
// Resolution tables, as metergraph-core defines them

const PROVIDER_ALIASES: Record<string, string> = {
  "amazon-bedrock": "bedrock",
  aws: "bedrock",
  "aws-bedrock": "bedrock",
  gemini: "google",
  "google-genai": "google",
  moonshot: "moonshotai",
  perplexity: "perplexity-ai",
  xai: "x-ai",
};

// The loader indexes a bedrock alias under these spellings too.
const PROVIDER_SYNONYMS: Record<string, string[]> = { bedrock: ["aws-bedrock", "aws"] };

// Publishers that report cache reads inside the input count, on these channels.
const INPUT_INCLUDES_CACHE_READ_PAIRS = new Set([
  "openai\u0000openai-api",
  "google\u0000google-api",
  "google\u0000google-vertex-ai",
  "deepseek\u0000deepseek-api",
  "xai\u0000xai-api",
]);

// A gateway names models "creator/model" and is priced under the creator.
const ROUTER_PROVIDERS = new Set(["gateway", "vercel", "vercel-ai-gateway"]);

const BEDROCK_CHANNEL = "aws-bedrock";
const BEDROCK_CHANNELS = new Set([BEDROCK_CHANNEL, "aws-bedrock-geo"]);
const BEDROCK_VERSION_SUFFIX = /-v\d+(?::\d+)?$/;
const BEDROCK_GLOBAL_PREFIX = "global.";
const GLOBAL_REGION = "global";
const MILLION_PLACES = 6;
const COST_PLACES = 8;

interface Alias {
  canonicalId: string;
  channel: string;
  rules: Record<string, unknown>;
  publisher: string | null;
  from: number | null;
  to: number | null;
  priceRegion: string[];
}

interface Price {
  id: string;
  modelId: string;
  channel: string;
  region: string;
  input: Dec | null;
  output: Dec | null;
  cacheRead: Dec | null;
  rules: Record<string, unknown>;
  from: number;
  to: number | null;
  publisher: string | null;
}

export interface Quote {
  canonicalModel: string | null;
  priceId: string | null;
  /** Rounded to 1e-8 USD, as metergraph-core rounds. Null when unpriced. */
  costUsd: number | null;
  status: "priced" | "partial" | "unpriced";
  reasons: string[];
}

export interface CatalogPricing extends Pricing {
  /** Cost with the catalog's reasons, for reports and conformance. */
  quote(query: PriceQuery): Quote;
  /**
   * The canonical "creator/model" id a production call's model resolves to,
   * trying the spellings the hosted product tries, or null when the catalog
   * cannot price it. The noise floor replays and prices this id.
   */
  resolveProduction(provider: string | null, model: string | null, at?: string): string | null;
  /** Source repository, file and commit of the catalog. */
  readonly source: CatalogDocument["source"];
}

export interface LoadPricingOptions {
  /** Deployment region for region-pinned rows. Default "global". */
  region?: string;
  /** A catalog document in the prices.json shape. Default: the bundled one. */
  document?: CatalogDocument;
  /** Clock for queries without `at`. */
  now?: () => Date;
}

// ---------------------------------------------------------------------------
// Dates

/** Milliseconds since the epoch; a value without a zone is UTC, as in Python. */
export function parseInstant(value: string): number {
  const text = value.trim();
  const hasZone = /(?:z|[+-]\d{2}:?\d{2})$/i.test(text);
  const iso = /^\d{4}-\d{2}-\d{2}$/.test(text)
    ? `${text}T00:00:00Z`
    : hasZone
      ? text
      : `${text.replace(" ", "T")}Z`;
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) throw new RangeError(`not an ISO date or time: ${value}`);
  return ms;
}

function windowsOverlap(a: Alias, b: Alias): boolean {
  return (
    (a.to === null || b.from === null || b.from < a.to) && (b.to === null || a.from === null || a.from < b.to)
  );
}

function inEffect(from: number | null, to: number | null, at: number): boolean {
  return (from === null || from <= at) && (to === null || at < to);
}

// ---------------------------------------------------------------------------
// Rules

export function normalizeProvider(provider: string): string {
  const key = provider.trim().toLowerCase();
  return PROVIDER_ALIASES[key] ?? key;
}

/** Whether cache reads come out of billable input for this row. */
export function countsCacheReadInInput(
  publisher: string | null,
  channel: string | null,
  rules: Record<string, unknown>,
): boolean {
  const stated = rules["input_includes_cache_read"];
  if (stated !== undefined && stated !== null) return Boolean(stated);
  if (typeof publisher !== "string" || typeof channel !== "string") return false;
  return INPUT_INCLUDES_CACHE_READ_PAIRS.has(`${publisher.trim().toLowerCase()}\u0000${channel.trim().toLowerCase()}`);
}

function tokens(value: unknown): number | null {
  if (value === null || value === undefined || typeof value === "boolean") return null;
  const number = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(number)) return null;
  const whole = Math.trunc(number);
  return whole >= 0 ? whole : null;
}

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

/** The discount a row's provider applies outside its published peak hours. */
function offPeakMultiplier(rules: Record<string, unknown>, at: number): Dec {
  const discount = record(rules["off_peak_discount"]);
  const windows = Array.isArray(discount["peak_hours_utc"]) ? (discount["peak_hours_utc"] as unknown[]) : [];
  const multiplier = dec(discount["multiplier"]);
  if (windows.length === 0 || multiplier === null) return ONE;
  const moment = new Date(at);
  const weekday = (moment.getUTCDay() + 6) % 7; // Monday 0, as Python's weekday()
  if (discount["peak_weekdays_only"] && weekday >= 5) return multiplier;
  const hour = moment.getUTCHours();
  for (const window of windows) {
    if (!Array.isArray(window) || window.length !== 2) continue;
    const start = tokens(window[0]);
    const end = tokens(window[1]);
    if (start === null || end === null) continue;
    if (start <= hour && hour < end) return ONE;
  }
  return multiplier;
}

function bedrockFallbackIds(modelKey: string): Array<[string, boolean]> {
  const unversioned = modelKey.replace(BEDROCK_VERSION_SUFFIX, "");
  const candidates: Array<[string, boolean]> = [];
  if (unversioned !== modelKey) candidates.push([unversioned, false]);
  if (modelKey.startsWith(BEDROCK_GLOBAL_PREFIX)) {
    for (const name of new Set([modelKey, unversioned])) {
      const bare = name.slice(BEDROCK_GLOBAL_PREFIX.length);
      if (bare) candidates.push([bare, true]);
    }
  }
  return candidates;
}

function crossesPriceRegion(alias: Alias): boolean {
  return alias.priceRegion.length > 0 && !(alias.priceRegion.length === 1 && alias.priceRegion[0] === GLOBAL_REGION);
}

// ---------------------------------------------------------------------------
// The snapshot

interface Usage {
  inputTokens: number | null;
  outputTokens: number | null;
  cachedInputTokens: number;
  inputIncludesCacheRead: boolean | null;
}

class Snapshot {
  readonly aliases = new Map<string, Alias[]>();
  readonly prices = new Map<string, Price[]>();
  readonly region: string;

  constructor(document: CatalogDocument, region: string) {
    this.region = region.trim().toLowerCase();
    for (const entry of document.models) {
      const canonical = String(entry.canonical_id);
      const publisher = entry.publisher ? entry.publisher.trim().toLowerCase() : null;
      for (const raw of entry.aliases ?? []) {
        const provider = raw.provider.toLowerCase();
        const name = raw.alias.toLowerCase();
        const alias: Alias = {
          canonicalId: canonical,
          channel: raw.channel,
          rules: record(raw.rules),
          publisher,
          from: raw.effective_from ? parseInstant(raw.effective_from) : null,
          to: raw.effective_to ? parseInstant(raw.effective_to) : null,
          priceRegion: [...new Set((raw.price_region ?? []).map((r) => r.trim().toLowerCase()))],
        };
        for (const key of [provider, ...(PROVIDER_SYNONYMS[provider] ?? [])]) {
          const id = `${key}\u0000${name}`;
          const list = this.aliases.get(id) ?? [];
          if (list.some((other) => windowsOverlap(alias, other))) {
            throw new Error(`${canonical}: overlapping alias windows for ${key}/${name}`);
          }
          list.push(alias);
          this.aliases.set(id, list);
        }
      }
      for (const raw of entry.prices ?? []) {
        const from = parseInstant(raw.effective_from);
        const price: Price = {
          id: `${canonical}:${raw.channel}:${raw.region}:${new Date(from).toISOString().slice(0, 10)}`,
          modelId: canonical,
          channel: raw.channel,
          region: raw.region || GLOBAL_REGION,
          input: dec(raw.input_per_mtok),
          output: dec(raw.output_per_mtok),
          cacheRead: dec(raw.cache_read_per_mtok),
          rules: record(raw.rules),
          from,
          to: raw.effective_to ? parseInstant(raw.effective_to) : null,
          publisher,
        };
        const id = `${canonical}\u0000${raw.channel}`;
        const list = this.prices.get(id) ?? [];
        list.push(price);
        this.prices.set(id, list);
      }
    }
    for (const list of this.prices.values()) list.sort((a, b) => b.from - a.from);
  }

  private aliasFor(candidates: Alias[], at: number): Alias | null {
    const effective = candidates.filter((alias) => inEffect(alias.from, alias.to, at));
    for (const region of new Set([this.region, GLOBAL_REGION])) {
      for (const alias of effective) if (alias.priceRegion.includes(region)) return alias;
    }
    for (const alias of effective) if (alias.priceRegion.length === 0) return alias;
    return null;
  }

  private priceRegions(alias: Alias): string[] {
    if (alias.priceRegion.length === 0) return [...new Set([this.region, "*", GLOBAL_REGION])];
    for (const region of [this.region, GLOBAL_REGION]) if (alias.priceRegion.includes(region)) return [region];
    return [];
  }

  private priceFor(alias: Alias, at: number): Price | null {
    const candidates = this.prices.get(`${alias.canonicalId}\u0000${alias.channel}`) ?? [];
    for (const region of this.priceRegions(alias)) {
      for (const price of candidates) {
        if (price.region.toLowerCase() !== region) continue;
        if (inEffect(price.from, price.to, at)) return price;
      }
    }
    return null;
  }

  /**
   * metergraph-core's CatalogSnapshot.cost for an already-normalized provider
   * key: alias, bedrock fallbacks, effective price, then the token cost.
   */
  cost(providerKey: string, modelKey: string, at: number, usage: Usage): Quote {
    let candidates = this.aliases.get(`${providerKey}\u0000${modelKey}`) ?? [];
    let aliasKnown = candidates.length > 0;
    let alias = this.aliasFor(candidates, at);
    if (alias === null && providerKey === "bedrock") {
      for (const [candidate, droppedGlobal] of bedrockFallbackIds(modelKey)) {
        candidates = this.aliases.get(`${providerKey}\u0000${candidate}`) ?? [];
        aliasKnown = aliasKnown || candidates.length > 0;
        const found = this.aliasFor(candidates, at);
        if (found === null || !BEDROCK_CHANNELS.has(found.channel)) continue;
        if (droppedGlobal && (found.channel !== BEDROCK_CHANNEL || crossesPriceRegion(found))) continue;
        alias = found;
        break;
      }
    }
    if (alias === null) {
      return unpriced(null, aliasKnown ? "no_effective_alias" : "unknown_model");
    }
    const price = this.priceFor(alias, at);
    if (price === null) return unpriced(alias.canonicalId, "no_effective_price");
    return priceTokens(alias, price, { ...price.rules, ...alias.rules }, at, usage);
  }
}

function unpriced(canonicalModel: string | null, reason: string): Quote {
  return { canonicalModel, priceId: null, costUsd: null, status: "unpriced", reasons: [reason] };
}

function priceTokens(alias: Alias, price: Price, rules: Record<string, unknown>, at: number, usage: Usage): Quote {
  const reasons: string[] = [];
  let inputCount = tokens(usage.inputTokens);
  let outputCount = tokens(usage.outputTokens);
  const cacheRead = tokens(usage.cachedInputTokens) ?? 0;
  if (inputCount === null) {
    reasons.push("missing_input_tokens");
    inputCount = 0;
  }
  if (outputCount === null) {
    reasons.push("missing_output_tokens");
    outputCount = 0;
  }

  const rowIncludes = countsCacheReadInInput(price.publisher, price.channel, rules);
  // A caller that knows how its count was taken restates it in the row's
  // convention, so the row's own arithmetic (and its long-context
  // threshold) applies as metergraph-core applies it.
  if (usage.inputIncludesCacheRead !== null && usage.inputIncludesCacheRead !== rowIncludes) {
    inputCount = usage.inputIncludesCacheRead ? Math.max(0, inputCount - cacheRead) : inputCount + cacheRead;
  }

  let deducted = 0;
  if (rowIncludes) {
    if (cacheRead > inputCount) {
      reasons.push("cache_read_exceeds_input");
      deducted = inputCount;
    } else {
      deducted += cacheRead;
    }
  }
  // Replays record no cache writes, so input_includes_cache_write deducts nothing.
  const billableInput = inputCount - deducted;

  let inputMultiplier: Dec = ONE;
  let outputMultiplier: Dec = ONE;
  const longContext = record(rules["long_context"]);
  const threshold = tokens(longContext["threshold"]);
  if (threshold !== null && inputCount > threshold) {
    inputMultiplier = nonZero(dec(longContext["input_multiplier"]));
    outputMultiplier = nonZero(dec(longContext["output_multiplier"]));
  }
  const offPeak = offPeakMultiplier(rules, at);
  inputMultiplier = mul(inputMultiplier, offPeak);
  outputMultiplier = mul(outputMultiplier, offPeak);

  let total: Dec = ZERO;
  if (price.input === null) {
    if (billableInput) reasons.push("input_rate_unavailable");
  } else {
    total = add(total, shift(mul(mul(int(billableInput), price.input), inputMultiplier), MILLION_PLACES));
  }
  if (price.output === null) {
    if (outputCount) reasons.push("output_rate_unavailable");
  } else {
    total = add(total, shift(mul(mul(int(outputCount), price.output), outputMultiplier), MILLION_PLACES));
  }
  if (cacheRead) {
    if (price.cacheRead === null) {
      reasons.push("cache_read_rate_unavailable");
    } else {
      total = add(total, shift(mul(mul(int(cacheRead), price.cacheRead), inputMultiplier), MILLION_PLACES));
    }
  }
  if (rules["uncaptured_fees"]) reasons.push("uncaptured_fees");

  const unique = [...new Set(reasons)];
  return {
    canonicalModel: alias.canonicalId,
    priceId: price.id,
    costUsd: unitsToNumber(unitsHalfUp(total, COST_PLACES), COST_PLACES),
    status: unique.length ? "partial" : "priced",
    reasons: unique,
  };
}

// Python's `_decimal(x) or Decimal("1")`: a zero or missing multiplier is one.
function nonZero(value: Dec | null): Dec {
  return value === null || value.n === 0n ? ONE : value;
}

// ---------------------------------------------------------------------------
// The public surface

export const bundledCatalog = catalogDocument as CatalogDocument;

/** The bundled catalog's version for the report footer: commit and date. */
export function catalogVersion(document: CatalogDocument = bundledCatalog): string {
  return `metergraph-core prices.yaml ${document.version} (${document.source.commit.slice(0, 12)})`;
}

export function loadPricing(options: LoadPricingOptions = {}): CatalogPricing {
  const document = options.document ?? bundledCatalog;
  const snapshot = new Snapshot(document, options.region ?? GLOBAL_REGION);
  const now = options.now ?? (() => new Date());
  const instant = (at: string | undefined): number => (at === undefined ? now().getTime() : parseInstant(at));

  // The hosted lookup: a router provider resolves to the creator in
  // "creator/model", spelled as written; then the creator's folded spelling,
  // which metergraph-core indexes some creators under.
  const lookup = (provider: string, model: string, at: number, usage: Usage): Quote => {
    let providerKey = normalizeProvider(provider);
    const modelKey = model.trim().toLowerCase();
    const attempts: string[] = [];
    if (ROUTER_PROVIDERS.has(providerKey)) {
      const slash = modelKey.indexOf("/");
      if (slash > 0) providerKey = modelKey.slice(0, slash);
      attempts.push(providerKey, normalizeProvider(providerKey));
    } else {
      attempts.push(providerKey);
    }
    let quote = unpriced(null, "unknown_model");
    for (const key of new Set(attempts)) {
      quote = snapshot.cost(key, modelKey, at, usage);
      if (quote.reasons[0] !== "unknown_model") break;
    }
    return quote;
  };

  // A replay's model: "creator/model" through the gateway rule, or a bare
  // id under the catalog's provider-less aliases.
  const quoteModel = (model: string, at: number, usage: Usage): Quote =>
    lookup(model.includes("/") ? "gateway" : "unknown", model, at, usage);

  const probe: Usage = { inputTokens: 1, outputTokens: 1, cachedInputTokens: 0, inputIncludesCacheRead: null };
  const priceable = (provider: string, model: string, at: number): string | null => {
    const quote = lookup(provider, model, at, probe);
    return quote.status !== "unpriced" && quote.costUsd !== null ? quote.canonicalModel : null;
  };

  const quote = (query: PriceQuery): Quote =>
    quoteModel(query.model, instant(query.at), {
      inputTokens: query.inputTokens,
      outputTokens: query.outputTokens,
      cachedInputTokens: query.cachedInputTokens ?? 0,
      inputIncludesCacheRead: query.inputIncludesCacheRead,
    });

  return {
    catalogVersion: catalogVersion(document),
    source: document.source,
    quote,
    price(query) {
      return quote(query).costUsd;
    },
    knows(model) {
      const quote = quoteModel(model, instant(undefined), probe);
      return quote.status !== "unpriced";
    },
    resolveProduction(provider, model, at) {
      const when = instant(at);
      const raw = String(model ?? "").trim();
      if (!raw) return null;
      // As the hosted product resolves production's model for the floor:
      // the captured provider (a router folded to the creator) with the bare
      // model, then the provider and model as captured.
      const providerKey = String(provider ?? "").trim().toLowerCase();
      const slash = raw.indexOf("/");
      const bare = slash >= 0 && raw.slice(slash + 1) ? raw.slice(slash + 1) : raw;
      let captured = normalizeCaptured(providerKey);
      if ((ROUTER_PROVIDERS.has(captured) || !captured) && slash > 0) captured = raw.slice(0, slash).toLowerCase();
      const attempts: Array<[string, string]> = [[captured, bare]];
      if (providerKey) attempts.push([providerKey, raw]);
      for (const [candidateProvider, candidateModel] of attempts) {
        if (!candidateProvider || !candidateModel) continue;
        const candidates: Array<[string, string]> = [];
        if (candidateModel.includes("/")) candidates.push(["gateway", candidateModel]);
        candidates.push([candidateProvider, candidateModel]);
        if (!candidateModel.includes("/")) candidates.push(["gateway", `${candidateProvider}/${candidateModel}`]);
        for (const [p, m] of candidates) {
          const canonical = priceable(p, m, when);
          if (canonical !== null) return canonical;
        }
      }
      // A capture without a provider: the catalog's provider-less aliases.
      if (!providerKey && slash < 0) return priceable("unknown", raw, when);
      return null;
    },
  };
}

function normalizeCaptured(provider: string): string {
  const folded: Record<string, string> = { "amazon-bedrock": "bedrock", aws: "bedrock", "aws-bedrock": "bedrock" };
  return folded[provider] ?? provider;
}

/**
 * Pricing for a local endpoint (Ollama and similar): every model is known and
 * costs nothing.
 */
export function freePricing(): Pricing {
  return { catalogVersion: "local (free)", price: () => 0, knows: () => true };
}
