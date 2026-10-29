# Command line

```sh
npx metergraph-replay demo
npx metergraph-replay estimate traces.jsonl --models openai/gpt-5-mini,anthropic/claude-haiku-4.5
npx metergraph-replay run traces.jsonl --models openai/gpt-5-mini,anthropic/claude-haiku-4.5 --budget 2.00
npx metergraph-replay report replay-out/run.json
```

| Command | What it does |
|---|---|
| `demo` | Renders a report from bundled synthetic traces and recorded replies. No keys, no network. |
| `estimate <traces>` | Reads the traces, says how many cases can be replayed and why the rest cannot, and prices the run. Makes no provider call. |
| `run <traces>` | Estimates, asks before spending, replays every case on every model plus the noise floor, then writes `run.json` and `report.html` and prints a table. |
| `report <run.json>` | Renders `report.html` again from an existing run. |

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
| `--no-noise-floor` | off | Skip the two replays of production's model. |
| `--labels` | off | Within-noise labels in the report. |
| `--allow-no-search` | off | Replay calls that searched the web, without search. |
| `--allow-unpriced` | off | Run models the price catalog does not know. |
| `--adapter NAME` | detected | `replay-jsonl`, `raw`, `langfuse` or `otel`. See [trace formats](trace-format.md). |
| `--out DIR` | `./replay-out` | Where `run.json` and `report.html` go. For `report`, next to the run file. |
| `--concurrency N` | 4 | Calls in flight at once. |
| `--yes` | off | Skip the spend confirmation. Without a terminal, `run` refuses without it. |
| `--json` | off | Print one JSON object on stdout and nothing else. |

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
npm run build:demo                           # regenerates demo/traces.jsonl and demo/run.json
```
