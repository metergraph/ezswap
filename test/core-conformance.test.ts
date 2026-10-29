// The fixtures in conformance/ pin core to Metergraph's hosted figures. A
// failure here means core moved a number, never that the fixture needs
// editing.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  cachedInput,
  equivalence,
  fieldKindPy,
  formatCount,
  formatMs,
  formatPercent,
  formatSigned,
  formatUsd,
  isCutOff,
  isEmptyOrRefused,
  itemOverlap,
  noiseFloorView,
  outputFacts,
  parseOutputJson,
  sampleNote,
  summarize,
} from "../src/core/index.ts";

function fixture(name: string) {
  return JSON.parse(readFileSync(new URL(`../conformance/${name}`, import.meta.url), "utf8"));
}

test("equivalence reproduces the hosted block for a run without a noise floor", () => {
  const f = fixture("experiment_equivalence.json");
  assert.deepEqual(equivalence(f.view), f.equivalence);
});

test("equivalence reproduces the hosted block with a noise floor and similarity", () => {
  const f = fixture("experiment_equivalence_floor.json");
  assert.deepEqual(equivalence(f.view), f.equivalence);
});

test("formatters match the hosted figures", () => {
  const f = fixture("experiment_equivalence.json");
  const formats: Record<string, (v: never) => string | null> = {
    usd: formatUsd,
    ms: formatMs,
    signed: (v) => formatSigned(v),
    percent: formatPercent,
    count: formatCount,
    sample_note: sampleNote,
  };
  for (const [name, format] of Object.entries(formats)) {
    for (const [value, text] of f.formats[name]) assert.equal(format(value as never), text, `${name}(${value})`);
  }
});

const summary = fixture("summarize.json");

test("parse_output_json: one fence, NaN and Infinity rejected, Python whitespace", () => {
  for (const c of summary.parse_output_json) {
    const [value, ok] = parseOutputJson(c.output);
    assert.equal(ok, c.ok, JSON.stringify(c.output));
    assert.deepEqual(ok ? value : null, c.value, JSON.stringify(c.output));
  }
});

test("is_cut_off and is_empty_or_refused", () => {
  for (const c of summary.is_cut_off) assert.equal(isCutOff(c.finish_reason), c.expected, JSON.stringify(c.finish_reason));
  for (const c of summary.is_empty_or_refused) {
    assert.equal(isEmptyOrRefused(c.output, c.finish_reason, c.refusal), c.expected, JSON.stringify(c));
  }
});

test("cached_input", () => {
  for (const c of summary.cached_input) assert.deepEqual(cachedInput(c.outcome), c.expected, JSON.stringify(c.outcome));
});

test("output_facts, with every bound", () => {
  for (const c of summary.output_facts) {
    assert.deepEqual(outputFacts(c.output, c.tracked_fields), c.expected, String(c.output).slice(0, 60));
  }
});

for (const s of summary.summarize) {
  test(`summarize: ${s.name}`, () => {
    assert.deepEqual(summarize(s.outcomes, s.controls), s.expected);
  });
}

const floor = fixture("noise_floor.json");

test("field_kind under Python semantics", () => {
  for (const c of floor.field_kind) assert.equal(fieldKindPy(c.values), c.expected, JSON.stringify(c.values).slice(0, 80));
});

test("item_overlap", () => {
  for (const c of floor.item_overlap) assert.equal(itemOverlap(c.a, c.b), c.expected, JSON.stringify([c.a, c.b]));
});

for (const s of floor.noise_floor_view) {
  test(`noise_floor_view: ${s.name}`, () => {
    assert.deepEqual(noiseFloorView(s.experiment, { includeCases: s.include_cases }), s.expected);
  });
}
