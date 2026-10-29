// conformance/page.json pins the view statistics (stat rows, spreads, floor
// readings, sentences, the summary table, grid ranges, the field tree) to
// Metergraph's hosted figures for the same views. A few parts exist only in
// the hosted product, and the comparison leaves exactly those out: the
// cached-input footnote, the monthly note's wording, and the judge and
// similarity columns.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import {
  bootstrapInterval,
  buildView,
  controlSummary,
  equivalenceSummary,
  fieldComparisons,
  fieldSentence,
  fieldTree,
  floorField,
  frontier,
  frontierPoints,
  gridLayout,
  gridMetrics,
  gridReadings,
  noiseFloors,
  statRows,
  statSpreads,
  suggestedFields,
} from "../src/core/index.ts";
import { initialOpen } from "../src/core/field-tree.ts";

const fixture = JSON.parse(readFileSync(new URL("../conformance/page.json", import.meta.url), "utf8"));
const json = (value: unknown) => JSON.parse(JSON.stringify(value));
const HOSTED_ONLY = new Set(["embedding_similarity", "grade_mean"]);

function withoutHostedOnly(expected: any) {
  const e = structuredClone(expected);
  for (const key of ["stat_rows", "stat_rows_monthly"]) {
    for (const row of e[key]) {
      if (row.key === "cached_input_share") delete row.note;
      if (row.key === "monthly_cost_usd" && row.note) delete row.note.text;
    }
  }
  if (e.equivalence_summary) {
    e.equivalence_summary.columns = e.equivalence_summary.columns.filter((c: any) => !HOSTED_ONLY.has(c.key));
    for (const row of e.equivalence_summary.rows) for (const key of HOSTED_ONLY) delete row.cells[key];
    // Whether any kept column carries a floor, as the summary itself decides it.
    e.equivalence_summary.floored = e.equivalence_summary.rows.some((row: any) => Object.values(row.cells).some((cell: any) => cell.floor));
  }
  for (const key of HOSTED_ONLY) delete e.grid[key];
  return e;
}

function page(experiment: any) {
  const tracked: string[] = experiment.tracked_fields || [];
  const labels = (experiment.arms || []).map((a: any) => a.label);
  const floors = noiseFloors(experiment, { tracked });
  const fields = Object.fromEntries(
    tracked.map((path) => {
      const { kind, comparisons } = fieldComparisons(experiment.cases, path, labels, experiment.fields);
      const floor = floorField(experiment, path, kind === "mixed" ? null : kind);
      return [path, { kind, comparisons, floor, sentences: comparisons.map((c) => fieldSentence(c, floor)) }];
    }),
  );
  const grid: Record<string, unknown> = {};
  for (const metric of gridMetrics(experiment, { tracked })) {
    grid[metric.key] = {
      better: metric.better,
      statistic: metric.statistic,
      readings: gridReadings(experiment, metric),
      frontier: frontier(frontierPoints(experiment, metric), metric.better).map((p) => p.label),
    };
  }
  const root = fieldTree(experiment.cases, tracked);
  const rows = statRows(experiment);
  const monthly = statRows(experiment, { volume: { calls: 12000, spendUsd: 30 } });
  for (const row of monthly) if (row.key === "monthly_cost_usd" && row.note) delete (row.note as any).text;
  return {
    stat_rows: rows,
    stat_rows_monthly: monthly,
    stat_spreads: statSpreads(experiment),
    noise_floors: floors,
    fields,
    equivalence_summary: equivalenceSummary(experiment, { tracked, floors }),
    grid_layout: gridLayout(experiment),
    grid,
    field_tree: root,
    initial_open: [...initialOpen(root, tracked.slice(0, 1))],
    suggested_fields: suggestedFields(experiment.cases),
    control_summary: controlSummary(experiment),
  };
}

for (const [name, { view, expected }] of Object.entries(fixture.views) as [string, any][]) {
  test(`view statistics match the hosted figures: ${name}`, () => {
    const actual = json(page(view));
    const want = withoutHostedOnly(expected);
    for (const key of Object.keys(want)) assert.deepEqual(actual[key], want[key], key);
  });
}

test("buildView still builds the view conformance/page.json was generated from", () => {
  assert.deepEqual(json(buildView(fixture.run)), fixture.views.replay_run.view);
});

test("bootstrap ranges are seeded and match the hosted figures", () => {
  for (const c of fixture.bootstrap) assert.deepEqual(bootstrapInterval(c.values, c.statistic), c.expected, JSON.stringify(c.values));
});
