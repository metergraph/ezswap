// core: pure statistics over a run file. No I/O.
//
//   buildView(run)            the experiment view every function below reads
//   equivalence(view)         per treatment: cost, latency, each tracked field's shift
//   statRows(view)            the built-in stat table, with the floor's own gap
//   noiseLabels(view, label)  opt-in within-noise labels
//
// Presentation, never a verdict: nothing here says a model is good enough to switch to.

export type * from "./view-types.ts";

export { buildView, controlRecord, replayOutcome } from "./view.ts";

export {
  CUT_OFF_REASONS,
  REFUSAL_REASONS,
  cachedInput,
  extractField,
  factsOf,
  fieldValue,
  isCutOff,
  isEmptyOrRefused,
  outputFacts,
  parseOutputJson,
  summarize,
} from "./summary.ts";

export { type FloorInput, controlOutcome, fieldKindPy, itemOverlap, noiseFloorView } from "./noise-floor.ts";

export { parseJsonPath, parsePath, valueAt, jsonPaths } from "./paths.ts";

export {
  SPREAD_MIN,
  type FieldComparison,
  boxStats,
  compareField,
  fieldComparisons,
  fieldKind,
  fieldPairs,
  overlap,
  pairedReading,
  parseOutput,
  quantile,
  suggestedFields,
} from "./fields.ts";

export {
  FLOOR_LABEL,
  STAT_GROUPS,
  STAT_ROWS,
  type MonthlyVolume,
  type StatRow,
  controlSummary,
  fieldSentence,
  floorClause,
  floorField,
  floorStat,
  monthlyNote,
  monthlyRow,
  noiseFloors,
  sameKeysCell,
  shiftSentence,
  statDelta,
  statRows,
  statSpreads,
} from "./stats.ts";

export { type TreatmentEquivalence, equivalence, equivalenceSummary, fewTracesNote, sampleNote } from "./equivalence.ts";

export {
  INTERVAL_LEVEL,
  RESAMPLES,
  bootstrapInterval,
  cellReading,
  frontier,
  frontierPoints,
  gridLayout,
  gridMetrics,
  gridReadings,
} from "./grid.ts";

export { type FieldNode, fieldTree } from "./field-tree.ts";

export { formatCachedShare, formatCount, formatMs, formatNumber, formatPercent, formatSigned, formatUsd } from "./format.ts";

export { LABEL_MIN_CASES, type NoiseLabel, noiseLabel, noiseLabels } from "./noise-label.ts";
