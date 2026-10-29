import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import type { PriceQuery } from "../src/types.ts";
import {
  bundledCatalog,
  countsCacheReadInInput,
  freePricing,
  loadPricing,
  parseInstant,
  type CatalogDocument,
} from "../src/pricing/catalog.ts";
import { dec, unitsHalfUp, unitsUp } from "../src/pricing/decimal.ts";

interface ConformanceCase {
  name: string;
  query: PriceQuery;
  expected: {
    canonicalModel: string | null;
    priceId: string | null;
    costUsd: string | null;
    status: string;
    reasons: string[];
  };
}

const fixture = JSON.parse(readFileSync(new URL("../conformance/pricing.json", import.meta.url), "utf8")) as {
  catalog: { commit: string; version: string };
  cases: ConformanceCase[];
};

const pricing = loadPricing();

test("the conformance fixture was generated from the bundled catalog", () => {
  assert.equal(fixture.catalog.commit, bundledCatalog.source.commit);
  assert.equal(fixture.catalog.version, bundledCatalog.version);
  assert.ok(fixture.cases.length > 100);
});

for (const item of fixture.cases) {
  test(`pricing conformance: ${item.name}`, () => {
    const quote = pricing.quote(item.query);
    assert.equal(quote.canonicalModel, item.expected.canonicalModel);
    assert.equal(quote.priceId, item.expected.priceId);
    assert.equal(quote.status, item.expected.status);
    assert.deepEqual(quote.reasons, item.expected.reasons);
    assert.equal(quote.costUsd, item.expected.costUsd === null ? null : Number(item.expected.costUsd));
  });
}

const base = { cachedInputTokens: null, inputIncludesCacheRead: null, at: "2026-10-20T12:00:00Z" };

test("the catalog version names the source commit", () => {
  assert.match(pricing.catalogVersion, /^metergraph-core prices\.yaml \d{4}-\d{2}-\d{2} \([0-9a-f]{12}\)$/);
  assert.match(pricing.source.commit, /^[0-9a-f]{40}$/);
  assert.equal(pricing.source.license, "Apache-2.0");
});

test("price is the quote's cost, null when unpriced", () => {
  assert.equal(pricing.price({ ...base, model: "openai/gpt-6-luna", inputTokens: 100_000, outputTokens: 0 }), 0.01);
  assert.equal(pricing.price({ ...base, model: "example/unknown", inputTokens: 1, outputTokens: 1 }), null);
});

test("knows a gateway id and a bare alias, not an unknown model", () => {
  assert.equal(pricing.knows("openai/gpt-6-luna"), true);
  assert.equal(pricing.knows("gpt-6-luna"), true);
  assert.equal(pricing.knows("example/unknown"), false);
});

test("a query without a time prices at the injected clock", () => {
  const before = loadPricing({ now: () => new Date("2026-01-01T00:00:00Z") });
  const after = loadPricing({ now: () => new Date("2026-10-20T00:00:00Z") });
  const query = { model: "gpt-6-luna", inputTokens: 1000, outputTokens: 1000, cachedInputTokens: null, inputIncludesCacheRead: null };
  assert.equal(before.price(query), null);
  assert.equal(after.price(query), 0.0006);
  assert.equal(before.knows("gpt-6-luna"), false);
});

test("reasoning tokens bill as output: there is no separate rate", () => {
  const plain = pricing.price({ ...base, model: "openai/gpt-6-sol", inputTokens: 100, outputTokens: 1000 });
  assert.equal(plain, 0.0102);
});

test("resolveProduction follows the hosted spellings", () => {
  assert.equal(pricing.resolveProduction("anthropic", "claude-sonnet-5-5", base.at), "anthropic/claude-sonnet-5.5");
  assert.equal(pricing.resolveProduction("openai", "gpt-6-luna", base.at), "openai/gpt-6-luna");
  assert.equal(pricing.resolveProduction("vercel", "openai/gpt-6-sol", base.at), "openai/gpt-6-sol");
  assert.equal(pricing.resolveProduction("openai", "openai/gpt-6-sol", base.at), "openai/gpt-6-sol");
  assert.equal(pricing.resolveProduction(null, "anthropic/claude-sonnet-5.5", base.at), "anthropic/claude-sonnet-5.5");
  assert.equal(pricing.resolveProduction(null, "gpt-6-luna", base.at), "openai/gpt-6-luna");
  assert.equal(pricing.resolveProduction("example", "nothing", base.at), null);
  assert.equal(pricing.resolveProduction("openai", null, base.at), null);
});

function tinyCatalog(): CatalogDocument {
  return {
    source: { repository: "https://example.com/catalog", path: "prices.yaml", commit: "0".repeat(40), license: "Apache-2.0" },
    version: "2026-01-01",
    currency: "USD",
    pricing_verified_at: "2026-01-01",
    models: [
      {
        canonical_id: "example/model-a",
        publisher: "example",
        aliases: [
          { provider: "example", alias: "example/model-a", channel: "example-api" },
          { provider: "unknown", alias: "model-a", channel: "example-api" },
          { provider: "bedrock", alias: "example.model-a", channel: "aws-bedrock", price_region: ["us-east-1"] },
          { provider: "bedrock", alias: "global.example.model-a", channel: "aws-bedrock", price_region: ["global"] },
          { provider: "unknown", alias: "reused", channel: "example-api", effective_to: "2026-06-01" },
        ],
        prices: [
          { channel: "example-api", region: "global", effective_from: "2026-01-01", input_per_mtok: "1", output_per_mtok: "2", source_url: "https://example.com" },
          { channel: "aws-bedrock", region: "global", effective_from: "2026-01-01", input_per_mtok: "3", output_per_mtok: "4", source_url: "https://example.com" },
          { channel: "aws-bedrock", region: "us-east-1", effective_from: "2026-01-01", input_per_mtok: "5", output_per_mtok: "6", source_url: "https://example.com" },
        ],
      },
      {
        canonical_id: "example/model-b",
        publisher: "example",
        aliases: [
          { provider: "unknown", alias: "reused", channel: "example-api", effective_from: "2026-06-01" },
          { provider: "example", alias: "example/model-b", channel: "example-api" },
        ],
        prices: [
          { channel: "example-api", region: "global", effective_from: "2026-01-01", output_per_mtok: "8", source_url: "https://example.com" },
        ],
      },
    ],
  };
}

test("a reused spelling resolves to the model it named at the time", () => {
  const local = loadPricing({ document: tinyCatalog() });
  const query = { model: "reused", inputTokens: 0, outputTokens: 1_000_000, cachedInputTokens: null, inputIncludesCacheRead: null };
  assert.equal(local.quote({ ...query, at: "2026-05-31T23:59:59Z" }).canonicalModel, "example/model-a");
  assert.equal(local.quote({ ...query, at: "2026-06-01" }).canonicalModel, "example/model-b");
});

test("a missing input rate makes the cost partial, not unpriced", () => {
  const local = loadPricing({ document: tinyCatalog() });
  const quote = local.quote({ model: "example/model-b", inputTokens: 10, outputTokens: 10, cachedInputTokens: null, inputIncludesCacheRead: null, at: "2026-02-01" });
  assert.equal(quote.status, "partial");
  assert.deepEqual(quote.reasons, ["input_rate_unavailable"]);
  assert.equal(quote.costUsd, 0.00008);
});

test("region-pinned aliases price on the deployment region's row", () => {
  const query = { model: "model-a", inputTokens: 1_000_000, outputTokens: 0, cachedInputTokens: null, inputIncludesCacheRead: null, at: "2026-02-01" };
  // Bare aliases go through "unknown"; reach the bedrock aliases through a document copy.
  const document = tinyCatalog();
  document.models[0]!.aliases.push(
    { provider: "unknown", alias: "regional", channel: "aws-bedrock", price_region: ["us-east-1"] },
  );
  const east = loadPricing({ document, region: "us-east-1" });
  const global = loadPricing({ document });
  assert.equal(east.price({ ...query, model: "regional" }), 5);
  assert.equal(global.price({ ...query, model: "regional" }), null);
  assert.equal(global.price(query), 1);
});

test("overlapping alias windows are refused at load", () => {
  const document = tinyCatalog();
  document.models[1]!.aliases.push({ provider: "unknown", alias: "model-a", channel: "example-api" });
  assert.throws(() => loadPricing({ document }), /overlapping alias windows/);
});

test("the cache-read rule: a stated rule wins, else the publisher and channel", () => {
  assert.equal(countsCacheReadInInput("anthropic", "anthropic-api", {}), false);
  assert.equal(countsCacheReadInInput("openai", "openai-api", {}), true);
  assert.equal(countsCacheReadInInput("OpenAI", " OPENAI-API ", {}), true);
  assert.equal(countsCacheReadInInput("anthropic", "vercel-ai-gateway", { input_includes_cache_read: true }), true);
  assert.equal(countsCacheReadInInput("openai", "openai-api", { input_includes_cache_read: false }), false);
  assert.equal(countsCacheReadInInput(null, "openai-api", {}), false);
});

test("times without a zone are UTC; offsets are honoured", () => {
  assert.equal(parseInstant("2026-10-01"), Date.UTC(2026, 9, 1));
  assert.equal(parseInstant("2026-10-01T08:30:00"), Date.UTC(2026, 9, 1, 8, 30));
  assert.equal(parseInstant("2026-10-01T08:30:00+02:00"), Date.UTC(2026, 9, 1, 6, 30));
  assert.throws(() => parseInstant("yesterday"), RangeError);
});

test("free pricing knows every model and charges nothing", () => {
  const free = freePricing();
  assert.equal(free.knows("llama3.2"), true);
  assert.equal(free.price({ model: "llama3.2", inputTokens: 10, outputTokens: 10, cachedInputTokens: null, inputIncludesCacheRead: null }), 0);
});

test("decimal rounding matches Decimal's ROUND_HALF_UP and ROUND_UP", () => {
  assert.equal(unitsHalfUp(dec("0.000000005")!, 8), 1n);
  assert.equal(unitsHalfUp(dec("0.0000000049")!, 8), 0n);
  assert.equal(unitsHalfUp(dec("-0.000000005")!, 8), -1n);
  assert.equal(unitsUp(dec("0.00010001")!, 4), 2n);
  assert.equal(unitsUp(dec("0.0001")!, 4), 1n);
  assert.deepEqual(dec("1e-7"), { n: 1n, s: 7 });
  assert.deepEqual(dec("2.5E3"), { n: 2500n, s: 0 });
  assert.equal(dec("abc"), null);
  assert.equal(dec(true), null);
  assert.equal(dec(Number.NaN), null);
});
