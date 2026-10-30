// Writes internal/report/testdata/model/*.json: the TypeScript report model
// (src/report/model.ts) for the demo run and test/fixtures/report-run.json,
// which the Go port is compared with field by field.
//
//   node --experimental-strip-types scripts/report-model-fixtures.ts
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { reportModel } from "../src/report/model.ts";
import type { RunFile } from "../src/types.ts";

const root = new URL("../", import.meta.url);
const out = new URL("internal/report/testdata/model/", root);
mkdirSync(out, { recursive: true });
const read = (path: string): RunFile => JSON.parse(readFileSync(new URL(path, root), "utf8"));

const demo = read("demo/run.json");
const fixture = read("test/fixtures/report-run.json");
// Without its noise floor or traffic.
const bare = structuredClone(fixture);
bare.settings.noiseFloor = false;
bare.replays = bare.replays.filter((r) => !r.arm.startsWith("floor_"));
delete bare.input.traffic;
// A source that recorded no durations.
const untimed = structuredClone(demo);
for (const c of untimed.cases) c.production.latencyMs = 0;

const runs: [string, RunFile, Parameters<typeof reportModel>[1]][] = [
  ["demo", demo, {}],
  ["demo-labels", demo, { labels: true }],
  ["fixture", fixture, {}],
  ["fixture-labels-monthly", fixture, { labels: true, monthlyCalls: 1000 }],
  ["fixture-bare", bare, { labels: true }],
  ["demo-untimed", untimed, {}],
];
for (const [name, run, options] of runs) {
  const model = reportModel(run, options);
  writeFileSync(new URL(`${name}.json`, out), `${JSON.stringify({ ...model, view: undefined }, null, 1)}\n`);
}
