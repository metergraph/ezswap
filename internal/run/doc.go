// Package run is the run: plan (read, sample, estimate), execute (reserve,
// replay, settle) and write (run.json).
//
//	PlanRun(options)                   read the trace file, sample, name treatments, estimate
//	ExecuteRun(ctx, plan, options)     replay within the budget; ctx is the stop signal
//	WriteRun(dir, run)                 dir/run.json
//	ParseEndpoint / ParseModels        what --endpoint, --floor-endpoint and --models name
//	ParseTemperature / ParseMaxTokens / ParseEffort
//	BuildTreatments / SampleCases / TrafficOf / SettleCost
//
// ExecuteRun reports progress through
// ExecuteOptions.OnProgress and, for a live
// view, ExecuteOptions.OnEvent: every case admitted, call sent and replay
// recorded, and the stop, in order and never concurrently.
package run
