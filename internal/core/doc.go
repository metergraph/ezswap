// Package core is pure statistics over a run file. No I/O.
//
//	BuildView(run, opts)              the experiment view every function below reads
//	Equivalence(view, via)            per treatment: cost, latency, each tracked field's shift
//	EquivalenceSummary(view, ...)     the summary table at the top of the results
//	StatRowsOf(view, opts)            the built-in stat table, with the floor's own gap
//	StatSpreads(view)                 10th to 90th percentile ranges per stat
//	NoiseFloors(view, tracked)        the floor beside each metric, in words
//	FieldComparisons(cases, ...)      every arm against the control on one tracked field
//	FloorField(view, path, kind)      the control against itself on one field
//	FieldSentence(comparison, floor)  the one sentence a tracked field may carry
//	GridMetrics / GridReadingsOf / FrontierPoints / Frontier / GridLayoutOf
//	                                  the treatments grid and the cost against quality frontier
//	BootstrapInterval(values, stat)   seeded percentile bootstrap ranges
//	FieldTree / InitialOpen           the tree of fields a run could track
//	NoiseLabelsOf / NoiseLabelFor     opt-in within-noise labels
//	Summarize, NoiseFloorView         the per-arm summary and the noise floor, as the view holds them
//	FormatUSD, FormatMs, FormatPercent, FormatCount, FormatNumber, FormatSigned, FormatCachedShare
//
// Presentation, never a verdict: nothing here says a model is good enough to
// switch to.
//
// The conformance fixtures in
// conformance/ pin its figures and sentences to Metergraph's hosted product.
// Where a function and a
// type would share a name, the function takes an "Of" or "For" suffix
// (BoxStatsOf, CellReadingOf, GridLayoutOf, GridReadingsOf, LabelTallyOf,
// PairedReadingOf, SameKeysCellOf, FloorStatOf, MonthlyNoteOf, StatRowsOf,
// BranchStateOf, NoiseLabelsOf, NoiseLabelFor); fieldKind is FieldKindOf,
// STAT_ROWS is StatRowDefs, and the NoiseFloorView type is NoiseFloor.
//
// The statistics read their inputs loosely:
// a view from the hosted product, one built by BuildView, and partial inputs
// all fit. Such a parameter is `any` and takes a loose JSON value (see
// Object and ParseJSON) or any typed value with a JSON form, such as an
// *ExperimentView or a []CaseView, which is read through its JSON encoding
// (ToLoose does that conversion once, for a caller making many calls).
// Numbers that may be missing are *float64 (nil is null); text that may be
// missing is *string.
package core
