package cli

import (
	"strconv"

	"github.com/metergraph/replay/internal/jsnum"
	"github.com/metergraph/replay/internal/run"
)

// Help is what --help prints.
var Help = `metergraph-replay: replay your production LLM calls on other models, and read
what changes against a noise floor (production's model replayed twice).

Usage
  metergraph-replay demo [--out DIR] [--explore]
  metergraph-replay demo --map
  metergraph-replay record [--upstream URL] [--port N] [--out DIR]
  metergraph-replay estimate <traces> --models LIST [--map] [options]
  metergraph-replay map <traces> --models LIST [options]
  metergraph-replay run <traces> --models LIST [options]
  metergraph-replay report <run.json> [--no-interactive] [--explain]

Commands
  demo       Print the report of bundled synthetic traces. No keys, no network.
             With --map, print the map of a bundled five-part app instead.
  record     Run a local proxy that forwards your app's model calls to one
             provider and writes each call to <out>/traces.jsonl.
  estimate   Count replayable cases and price the run. Makes no provider call.
             With --map, list the parts of the app and price a map.
  map        Replay a sample of every part of the app and print which parts
             change beyond what a rerun of production's own model does.
  run        Estimate, confirm, replay, write run.json and print the report.
  report     Explore a run.json in the terminal: tabs for fields, the noise
             floor and every call. Prints the report when not interactive.
             Prints the map for a run that map made.

The map (map, estimate --map)
  A part is a place in your app that calls a model. It is named by a "part"
  field in the trace file, by the X-Replay-Part header when recording, or by
  --part-key; calls with no name are grouped by a fingerprint of the system
  prompt, forced tool and response schema.
  --per-part N             Latest N replayable cases of each part (default
                           ` + strconv.Itoa(run.DefaultPerPart) + `, from ` + strconv.Itoa(run.MinPerPart) + ` to ` + strconv.Itoa(run.MaxPerPart) + `). Takes the place of --sample.
  --only NAME              Keep these parts, by name or key. Repeat for more.
  --name KEY=NAME          Rename a part for this command. KEY is a part's key
                           or current name; two parts with one name merge.
  --part-key KEY           The key that holds the part name in your records:
                           a top-level or metadata key, a span attribute, or
                           span.name.
  --ignore PATH            Leave a field out of the comparison, such as a
                           generated id: $.request_id
  Names are kept in replay-parts.json beside the trace file. Edit a name
  there to rename a part; give two parts one name to merge them.

Recording (record)
  --upstream URL           Where calls are forwarded (default
                           https://api.openai.com). https, or http to this
                           machine.
  --port N                 Port on 127.0.0.1 (default 8787; 0 picks a free one).
  Point your app's base URL at http://127.0.0.1:8787/v1. Your API key passes
  through to the provider and is never written or shown.

Models and endpoints
  --models LIST            Comma-separated models, as the catalog names them
                           (creator/model). Prefix an endpoint preset to send
                           one elsewhere: openai:gpt-5-mini. Per-model settings
                           follow an @: model@temperature=0.2,max_tokens=500
  --endpoint NAME|URL      Endpoint for models without a prefix (default
                           ` + string(run.DefaultEndpoint) + `). Presets: openai, openrouter, vercel,
                           litellm, ollama; or any OpenAI-compatible base URL.
  --floor-endpoint NAME|URL  Endpoint for the noise floor (default: --endpoint).
  --api-key-env NAME       Variable holding the key for a base URL endpoint.
  --temperature N          Temperature for every model (default: production's).
  --max-tokens N           Max tokens for every model (default: production's).
  --reasoning-effort E     none, minimal, low, medium, high or xhigh.

Keys are read from the environment: OPENAI_API_KEY, OPENROUTER_API_KEY,
AI_GATEWAY_API_KEY, LITELLM_API_KEY (with LITELLM_BASE_URL). Ollama needs none.

Sampling and statistics
  --sample N               Latest N replayable cases, one per trace (default
                           ` + strconv.Itoa(run.DefaultSample) + `, max ` + strconv.Itoa(run.MaxSample) + `).
  --track PATH             A JSON field to compare, such as $.category.
                           Repeat for more (max ` + strconv.Itoa(run.MaxTrackedFields) + `).
  --no-noise-floor         Skip the two replays of production's model.
  --labels                 Show within-noise labels in the report.
  --allow-no-search        Replay calls that searched the web, without search.
  --adapter NAME           Trace format: replay-jsonl, raw, langfuse, otel
                           (default: detected).

Spend
  --budget USD             Most the run may spend (default ` + jsnum.ToFixed(run.DefaultBudgetUSD, 2) + `, or ` + jsnum.ToFixed(run.DefaultMapBudgetUSD, 2) + `
                           for map). Each call
                           reserves its maximum before it is sent; the run stops
                           when the next case does not fit.
  --monthly-calls N        Calls a month the workload makes, for the monthly cost
                           projection (default: estimated from the file's time span).
  --allow-unpriced         Run models the price catalog does not know.
  --no-price-lookup        Do not ask Metergraph's Pricing API for models the
                           bundled catalog cannot price (only model names are sent).
  --yes                    Skip the spend confirmation (needed without a terminal).
  --concurrency N          Calls in flight at once (default ` + strconv.Itoa(run.DefaultRunConcurrency) + `).

Output
  --out DIR                Where run.json goes, and record's traces.jsonl
                           (default ./replay-out).
  --explore                demo: open the explorer instead of printing the report.
  --no-interactive         Print instead of drawing: no explorer for report, and
                           plain progress for run. Also the default when CI is set.
  --explain                Print the notes on reading the report after it.
  --ascii                  Plain ASCII, no box drawing (default when stdout is not
                           a terminal or TERM is dumb).
  --color, --no-color      Colour on or off (default: on in a terminal, unless
                           NO_COLOR is set).
  --json                   Print one JSON object instead of text.
  --help, --version

Exit codes
  0  done
  1  any other error
  2  usage: a bad option, value or file
  3  no replayable cases in the sample
  4  refused: over the budget, unpriced models, or no confirmation
  5  endpoint configuration, such as a missing key
  6  interrupted, or stopped early (budget or a call above its reservation)

Nothing leaves this machine except the provider calls a run makes, and the
calls record forwards for your app. record listens on 127.0.0.1 only.

Metergraph runs this comparison continuously on live traffic: https://www.metergraph.dev
`
