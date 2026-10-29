#!/usr/bin/env node
// Convert metergraph-core's public price catalog (prices.yaml, Apache-2.0) to
// src/pricing/prices.json, the catalog this package ships.
//
// Usage: node scripts/build-prices.mjs <path to a clone of the metergraph repository>
//
// The header records the source repository, the file's path in it and the
// commit that last changed the file, so a report can say which prices it used.
// Rates are kept as decimal strings: the cost function does exact decimal
// arithmetic, as metergraph-core does.

import { execFileSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "yaml";

const SOURCE_REPO = "https://github.com/PioneerSquareLabs/metergraph";
const SOURCE_PATH = "core/src/metergraph_core/data/prices.yaml";
const RATE_FIELDS = [
  "input_per_mtok",
  "output_per_mtok",
  "cache_read_per_mtok",
  "cache_write_5m_per_mtok",
  "cache_write_1h_per_mtok",
  "batch_input_per_mtok",
  "batch_output_per_mtok",
];

const clone = process.argv[2];
if (!clone) {
  console.error("Usage: node scripts/build-prices.mjs <metergraph clone>");
  process.exit(2);
}

const git = (...args) => execFileSync("git", ["-C", clone, ...args], { encoding: "utf8" }).trim();
const commit = git("log", "-1", "--format=%H", "--", SOURCE_PATH);
if (!/^[0-9a-f]{40}$/.test(commit)) {
  console.error(`No commit found for ${SOURCE_PATH} in the clone`);
  process.exit(1);
}
if (git("status", "--porcelain", "--", SOURCE_PATH) !== "") {
  console.error(`${SOURCE_PATH} has uncommitted changes in the clone; build from a committed file`);
  process.exit(1);
}

// prices.yaml uses YAML 1.1 merge keys (`<<: *anchor`), as PyYAML reads them.
const document = parse(readFileSync(join(clone, SOURCE_PATH), "utf8"), { merge: true });

function text(value) {
  if (value === null || value === undefined) return null;
  if (value instanceof Date) return value.toISOString().slice(0, 10);
  return String(value);
}

function rate(value) {
  if (value === null || value === undefined) return null;
  if (typeof value !== "number" && typeof value !== "string") {
    throw new Error(`unexpected rate ${JSON.stringify(value)}`);
  }
  return String(value);
}

function compact(object) {
  return Object.fromEntries(Object.entries(object).filter(([, value]) => value !== null && value !== undefined));
}

const models = document.models.map((entry) => ({
  canonical_id: String(entry.canonical_id),
  publisher: entry.publisher ? String(entry.publisher).trim().toLowerCase() : null,
  aliases: (entry.aliases ?? []).map((alias) =>
    compact({
      provider: String(alias.provider),
      alias: String(alias.alias),
      channel: String(alias.channel),
      rules: alias.rules ?? null,
      effective_from: text(alias.effective_from),
      effective_to: text(alias.effective_to),
      price_region:
        alias.price_region === undefined || alias.price_region === null
          ? null
          : [alias.price_region].flat().map(String),
    }),
  ),
  prices: (entry.prices ?? []).map((price) =>
    compact({
      channel: String(price.channel),
      region: price.region ? String(price.region) : "global",
      effective_from: text(price.effective_from),
      effective_to: text(price.effective_to),
      ...Object.fromEntries(RATE_FIELDS.map((field) => [field, rate(price[field])])),
      rules: price.rules ?? null,
      source_url: String(price.source_url),
    }),
  ),
}));

const output = {
  source: {
    repository: SOURCE_REPO,
    path: SOURCE_PATH,
    commit,
    license: "Apache-2.0",
  },
  version: String(document.version),
  currency: String(document.currency),
  pricing_verified_at: text(document.pricing_verified_at),
  models,
};

const target = join(dirname(fileURLToPath(import.meta.url)), "..", "src", "pricing", "prices.json");
writeFileSync(target, JSON.stringify(output, null, 1) + "\n");
console.log(`Wrote ${models.length} models from ${commit.slice(0, 12)} to src/pricing/prices.json`);
