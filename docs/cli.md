# Command line

```sh
npx metergraph-replay demo
npx metergraph-replay estimate traces.jsonl --models openai/gpt-5-mini,anthropic/claude-haiku-4.5
npx metergraph-replay run traces.jsonl --models openai/gpt-5-mini,anthropic/claude-haiku-4.5 --budget 2.00
npx metergraph-replay report replay-out/run.json
```

| Command | What it does |
|---|---|
| `demo` | Prints the report of bundled synthetic traces and recorded replies. No keys, no network. `--explore` opens the explorer instead. |
| `estimate <traces>` | Reads the traces, says how many cases can be replayed and why the rest cannot, and prices the run. Makes no provider call. |
| `run <traces>` | Estimates, asks before spending, replays every case on every model plus the noise floor, then writes `run.json` and prints the report. |
| `report <run.json>` | Opens the interactive explorer on an existing run. Without an interactive terminal, or with `--no-interactive`, it prints the report instead. |

The report lives in the terminal; nothing else is written besides `run.json`.

## Models and endpoints

Models are named as the price catalog names them, `creator/model`. Every
model goes to an OpenAI-compatible endpoint you bring:

| Preset | Key variable | Notes |
|---|---|---|
| `openrouter` (default) | `OPENROUTER_API_KEY` | Reports what it charged, which the run uses. |
| `openai` | `OPENAI_API_KEY` | A bare model id is read as an OpenAI model. |
| `vercel` | `AI_GATEWAY_API_KEY` | Vercel AI Gateway. |
| `litellm` | `LITELLM_API_KEY`, `LITELLM_BASE_URL` | Your LiteLLM proxy. |
| `ollama` | none | Local, free. |

`--endpoint` sets the endpoint for every model without a prefix. It takes a
preset or a base URL (`http://localhost:8000/v1`); a base URL on this machine
is free, and `--api-key-env NAME` names the variable that holds its key.
Prefix a model with a preset to send it elsewhere:

```sh
--models openrouter:anthropic/claude-haiku-4.5,openai:gpt-5-mini
```

The noise floor replays production's model at production's settings on
`--floor-endpoint` (default: the same as `--endpoint`).

Settings: `--temperature`, `--max-tokens` and `--reasoning-effort` apply to
every model; without them a replay uses production's. One model can carry its
own after an `@`:

```sh
--models openai/gpt-5-mini@temperature=0.2,max_tokens=500,google/gemini-2.5-flash
```

Keys are read from the environment and never written to `run.json`, the
report or the terminal. A run records an endpoint by its preset name or host.

## Options

| Option | Default | |
|---|---|---|
| `--sample N` | 20 | The latest N replayable cases, one per trace id (max 200). Cases that cannot be replayed among them are kept, with the reason. |
| `--track PATH` | none | A JSON field to compare with production, such as `$.category`. Repeat it, up to 10. |
| `--budget USD` | 1.00 | The most the run may spend. |
| `--monthly-calls N` | from the file | Calls a month the workload makes, for the monthly cost projection. Without it, the projection uses the file's calls a day when the file spans at least a day. |
| `--no-noise-floor` | off | Skip the two replays of production's model. |
| `--labels` | off | Within-noise labels in the report. |
| `--allow-no-search` | off | Replay calls that searched the web, without search. |
| `--allow-unpriced` | off | Run models the price catalog does not know. |
| `--adapter NAME` | detected | `replay-jsonl`, `raw`, `langfuse` or `otel`. See [trace formats](trace-format.md). |
| `--out DIR` | `./replay-out` | Where `run.json` goes (`demo` and `run`). |
| `--explore` | off | `demo`: open the explorer instead of printing the report. |
| `--no-interactive` | off | `report`: print the report instead of opening the explorer. |
| `--ascii` | detected | Plain ASCII: no box drawing or block characters. On by default when stdout is not a terminal or `TERM` is `dumb`. |
| `--color`, `--no-color` | detected | Colour on or off. On by default in a terminal, unless `NO_COLOR` is set. |
| `--concurrency N` | 4 | Calls in flight at once. |
| `--yes` | off | Skip the spend confirmation. Without a terminal, `run` refuses without it. |
| `--json` | off | Print one JSON object on stdout and nothing else. |

## The report

`run` and `demo` print the report; `report <run.json>` explores it. The
report fits the terminal's width (80 to 140 columns; wider terminals get the
140-column layout) and never wraps a box. From the top:

1. The headline question, its one-sentence answer, and the run's sample,
   source, production model, spend and price catalog.
2. **Cost**: a bar per arm scaled to cost per call, the change against
   production's model replayed, and the monthly projection when the call
   volume is known.
3. **Output signals**: JSON parses, same keys, empty or refused, cut off,
   output length and errors, each with its change and production's own gap.
4. **Tracked fields**: agreement with production per arm, with production's
   agreement with itself ticked on the same scale (`┆`), and core's sentence.
5. **Latency**: the median and the 10th to 90th percentile per arm on one axis.
6. **Calls that differ**: up to five, with the changed field values.
7. **Not replayable**: counts by reason, with how to fix each.
8. What the noise floor means, and the link to keep the comparison running
   on live traffic.

With `--labels`, figures carry badges: `(within noise)`, `[beyond noise ↑]`,
`(too few calls)`. Colour marks which arm a figure belongs to (production,
the treatments, production against itself); it never marks a figure as good
or bad.

Captured text (prompts, outputs, errors) is shown with control characters
made visible: an escape sequence in a model's output prints as `^[[2J`
instead of clearing the screen.

### The explorer

| Key | |
|---|---|
| `←` `→`, `1` to `5` | Tabs: Summary, Fields, Noise floor, Calls, Not replayable |
| `↑` `↓`, `j` `k` | Move in the call list, or scroll |
| `PgUp` `PgDn`, space | A page |
| `g` `G`, `Home` `End` | First, last |
| `enter` | Open the call: its input, production's output beside each arm's |
| `esc`, `backspace` | Back |
| `n` `p` | Next and previous call, in an open call |
| `d` | Only calls that differ |
| `/` | Filter calls by text |
| `?` | Help |
| `q` | Quit |

The explorer uses the alternate screen and restores the terminal however it
ends: `q`, Ctrl-C, a signal or an error.

## Spend

The estimate prices each case at production's token counts (2000 input tokens
when they are not recorded) and an assumed output (the larger of production's
output and half the token cap, never above the cap), with a 25% margin. The
most a run can spend is every call using its full `max_tokens` on an input
bounded at one token per byte.

`run` refuses when the expected spend is over `--budget`, or when a model has
no price and `--allow-unpriced` is not set. While it runs:

- Each call reserves its upper bound before it is sent. A case starts only
  when all of its calls fit in the budget beside what is spent and still
  reserved, so a budget that runs out drops whole cases. A case that does not
  fit waits for calls in flight to settle; if it still does not fit, the run
  stops (`"stopped": "budget"`).
- A call's cost is what the endpoint reported, else the catalog price of the
  usage it returned, else its reservation (a reply with no usage, or a failure
  that may have reached the model). `costSource` in `run.json` says which. A
  local endpoint costs nothing (`"free"`).
- A call that cost more than it reserved stops the run at once
  (`"stopped": "reservation_exceeded"`).
- Ctrl-C stops sending, waits for the calls in flight, and writes what exists
  (`"stopped": "interrupted"`). A second Ctrl-C cancels the calls in flight.

Calls that were not sent are in `run.json` as `"status": "skipped"`, with the
reason.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Done. |
| 1 | Any other error, or every call failed. |
| 2 | Usage: a bad option, value or file. |
| 3 | No case in the sample can be replayed. |
| 4 | Refused: expected spend over the budget, unpriced models, or no confirmation. |
| 5 | Endpoint configuration, such as a missing key or base URL. |
| 6 | Interrupted, or stopped early (budget, or a call above its reservation). |

With `--json` every command prints one object with `ok`, `exitCode` and
`command`; a failure adds `error: { code, message }`.

## Development

```sh
npm run dev -- demo --out /tmp/replay-demo   # runs src/ directly
npm run build && node bin/metergraph-replay.js demo
UPDATE_GOLDEN=1 npm test                     # accepts changed report output in test/golden/
npm run build:demo                           # regenerates demo/traces.jsonl and demo/run.json
```
