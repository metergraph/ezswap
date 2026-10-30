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
  metergraph-replay estimate <traces> --models LIST [options]
  metergraph-replay run <traces> --models LIST [options]
  metergraph-replay report <run.json> [--no-interactive] [--explain]

Commands
  demo       Print the report of bundled synthetic traces. No keys, no network.
  estimate   Count replayable cases and price the run. Makes no provider call.
  run        Estimate, confirm, replay, write run.json and print the report.
  report     Explore a run.json in the terminal: tabs for fields, the noise
             floor and every call. Prints the report when not interactive.

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
  --budget USD             Most the run may spend (default ` + jsnum.ToFixed(run.DefaultBudgetUSD, 2) + `). Each call
                           reserves its maximum before it is sent; the run stops
                           when the next case does not fit.
  --monthly-calls N        Calls a month the workload makes, for the monthly cost
                           projection (default: estimated from the file's time span).
  --allow-unpriced         Run models the price catalog does not know.
  --yes                    Skip the spend confirmation (needed without a terminal).
  --concurrency N          Calls in flight at once (default ` + strconv.Itoa(run.DefaultRunConcurrency) + `).

Output
  --out DIR                Where run.json goes (default ./replay-out).
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

Nothing leaves this machine except the provider calls a run makes.
`
