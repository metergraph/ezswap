# Command line

```sh
ezswap demo
ezswap demo --map
ezswap record
ezswap estimate traces.jsonl --map --models openai/gpt-5-mini
ezswap map traces.jsonl --models openai/gpt-5-mini
ezswap estimate traces.jsonl --models openai/gpt-5-mini,anthropic/claude-haiku-4.5
ezswap run traces.jsonl --models openai/gpt-5-mini,anthropic/claude-haiku-4.5 --budget 2.00
ezswap report ezswap-out/run.json
ezswap judge ezswap-out/run.json
```

Run with no command on an interactive terminal, `ezswap` walks
through three steps, and first asks which to start at:

1. **Record.** Where your app's calls go, then the record dashboard.
2. **Analyze.** The parts found in the traces, the models to try, cases
   per part and the judge, with the estimate updating as you choose.
3. **Map.** The spend confirmation, the progress and the map explorer.

Start at Analyze with a trace file you already have, or at Map to open a
finished `run.json`. Every step is also a command of its own, below.
Without a terminal it prints the help.

Installed from npm, prefix each with `npx` (`npx ezswap demo`).
The README lists every way to install it.

| Command | What it does |
|---|---|
| `demo` | Prints the report of bundled synthetic traces and recorded replies. No keys, no network. `--explore` opens the explorer instead. |
| `demo --map` | Prints the map of a bundled synthetic app with five parts. No keys, no network, no file written. |
| `record` | Runs a local proxy on `127.0.0.1` that forwards your app's model calls to one provider and appends each captured call to `<out>/traces.jsonl`. Takes no file. See [Recording](record.md). |
| `estimate <traces>` | Reads the traces, says how many cases can be replayed and why the rest cannot, and prices the run. Makes no provider call. |
| `estimate <traces> --map` | Lists the parts of the app (name, how it was named, cases, compared fields, expected cost) and prices a map. Makes no provider call. Writes the names file. |
| `map <traces>` | Estimates, asks before spending, replays a sample of every part on every model and twice on production's model, then writes `run.json` and prints the map. See [the sensitivity map](sensitivity-map.md). |
| `run <traces>` | Estimates, asks before spending, replays every case on every model plus the noise floor, then writes `run.json` and prints the report. |
| `judge <run.json>` | Judges the free-text parts of a run that `map` made, writes the verdicts into the same `run.json` and prints the map. Pairs already judged are not asked again. See [judged parts](sensitivity-map.md#judged-free-text-parts). |
| `report <run.json>` | Opens the interactive explorer on an existing run. Without an interactive terminal, or with `--no-interactive`, it prints the report instead. On a run that `map` made it prints the map, never the explorer. |

The report and the map live in the terminal. ezswap writes three files and
nothing else:

| File | Written by | Holds |
|---|---|---|
| `<out>/run.json` | `run`, `map`, `demo` (not `demo --map`), `judge` (rewrites it) | The sampled prompts and every model's output, and the judge's verdicts. |
| `<out>/traces.jsonl` | `record` | Each captured call's request and response bodies. Appended to. |
| `ezswap-parts.json`, beside the trace file | `estimate --map`, `map` | Part keys and names. |

`map` and `run` write the same `run.json`. Give one of them another `--out`
to keep both.

## Settings file

`estimate`, `map` and `run` read `ezswap.yaml` from the working directory
when it exists. Its keys are the option names, without the leading dashes,
and a flag given on the command line wins over the file:

```yaml
models:
  - anthropic/claude-haiku-5.5
  - openai/gpt-5.4-nano
endpoint: vercel
per-part: 40
judge: true
html: map.html
budget: 2.50
```

With that file, `ezswap map traces.jsonl` is the whole command.
The command says which settings came from the file:
`Settings from ezswap.yaml: budget, endpoint, html, judge, models, per-part.`

- Each command takes the keys it accepts and leaves the rest, so one file
  serves all three: `sample` is used by `run`, `per-part` by `map`.
- `models` takes a list or a comma-separated string. Repeatable options
  (`track`, `ignore`, `only`, `name`) take a list. Booleans take `true` or
  `false`.
- A key that is not an option name is an error, so a misspelling does not
  pass silently. `yes`, `json`, `map`, the display options and `record`'s
  options cannot be set in the file: spending is always confirmed on the
  command line.
- `--config FILE` reads another file; `--no-config` reads none. Other
  commands read no settings file.
- The file holds no keys: credentials stay in environment variables.

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

Options every replay command takes (`estimate`, `run`, `map`), and the
display options:

| Option | Default | |
|---|---|---|
| `--sample N` | 20 | The latest N replayable cases, one per trace id (max 200). Cases that cannot be replayed among them are kept, with the reason. Not for a map: use `--per-part`. |
| `--track PATH` | none | A JSON field to compare with production, such as `$.category`. Repeat it, up to 10. In a map, tracked fields come first among each part's compared fields. |
| `--budget USD` | 1.00, or 5.00 for a map | The most the run may spend. |
| `--monthly-calls N` | from the file | Calls a month the workload makes, for the monthly cost projection. Without it, the projection uses the file's calls a day when the file spans at least a day. |
| `--no-noise-floor` | off | Skip the two replays of production's model. A map made without them labels every part `not comparable`. |
| `--labels` | off | Within-noise badges in the explorer's tables. The printed report marks a figure beyond the noise floor with `*` either way. Not for `map`. |
| `--allow-no-search` | off | Replay calls that searched the web, without search. |
| `--allow-unpriced` | off | Run models the price catalog does not know. |
| `--no-price-lookup` | off | Do not ask Metergraph's Pricing API for models the bundled catalog cannot price. See [prices](#prices). |
| `--adapter NAME` | detected | `ezswap-jsonl`, `raw`, `langfuse` or `otel`. See [trace formats](trace-format.md). |
| `--html FILE` | none | `run`, `map`, `report` and `demo`: also write the result as one self-contained HTML page. See [the HTML page](#the-html-page). |
| `--out DIR` | `./ezswap-out` | Where `run.json` goes (`demo`, `run` and `map`), and `record`'s `traces.jsonl`. Not for `report` or `demo --map`, which write nothing. |
| `--explore` | off | `demo`: open the explorer instead of printing the report. Not for `demo --map`. |
| `--no-interactive` | off | Print instead of drawing: `report` prints the report instead of opening the explorer, and `run` shows plain progress. Also the default when `CI` is set. |
| `--explain` | off | Print the report's notes after it: how to read the `itself` column, how cost is estimated, why calls were not replayed. `report --explain` prints rather than opening the explorer. |
| `--ascii` | detected | Plain ASCII: no box drawing or block characters. On by default when stdout is not a terminal or `TERM` is `dumb`. |
| `--color`, `--no-color` | detected | Colour on or off. On by default in a terminal, unless `NO_COLOR` is set. |
| `--concurrency N` | 4 | Calls in flight at once. |
| `--yes` | off | Skip the spend confirmation. Without a terminal, `run` refuses without it. |
| `--json` | off | Print one JSON object on stdout and nothing else. |

### Map options

| Option | Default | |
|---|---|---|
| `--judge` | off | `map`: compare free-text parts with a judge model. `estimate --map`: show those parts as judged and price the judging. See [judged parts](sensitivity-map.md#judged-free-text-parts). |
| `--map` | off | `estimate`: list the parts and price a map. `demo`: print the bundled map. `report`: accepted on a run that `map` made (which prints the map either way), a usage error on any other run. |
| `--per-part N` | 20 | The latest N replayable cases of each part, one per trace id (10 to 200). |
| `--part-key KEY` | none | The key that holds the part name in your records. See [trace formats](trace-format.md#parts). |
| `--ignore PATH` | none | Leave a field out of the comparison, such as a generated id: `--ignore '$.request_id'`. Repeat it, or separate paths with commas. |
| `--only NAME` | all parts | Keep these parts, by name or key. Repeat it, or separate names with commas. |
| `--name KEY=NAME` | none | Rename a part for this command. `KEY` is a part's key or current name. Two parts given one name merge. Nothing is saved. Applied before `--only`. |

### Record options

| Option | Default | |
|---|---|---|
| `--upstream URL` | `https://api.openai.com` | Where calls are forwarded. `https`, or `http` to this machine. |
| `--port N` | 8787 | The port on `127.0.0.1` (0 to 65535). `0` picks a free one. |
| `--fresh` | off | Start an empty `traces.jsonl`. The previous file is renamed `traces-<date>-<time>.jsonl` beside it, never deleted. Without it, a recording appends to the file. |

### Which command takes which

An option given to a command that does not take it is a usage error (exit
2), so a slip such as `run --per-part 30` does not pass silently.

| Option | `estimate --map` | `map` | `report` (map run) | `demo --map` | `run`, `estimate` | `record` |
|---|---|---|---|---|---|---|
| `--map` | yes | no (always a map) | yes | yes | `estimate` only | no |
| `--per-part`, `--part-key`, `--ignore` | yes | yes | no | no | no | no |
| `--only`, `--name` | yes | yes | yes | no | no | no |
| `--judge` | yes | yes | no (use `judge`) | no | no | no |
| `--sample` | no | no | not used | not used | yes | no |
| `--upstream`, `--port` | no | no | no | no | no | yes |
| `--models`, `--budget` and the other replay options | yes | yes | not used | not used | yes | no |

`record` takes `--upstream`, `--port`, `--out` and `--json`, and no file.

## The map

`map` and `estimate --map` group calls into **parts**: places in your app
that call a model. [The sensitivity map](sensitivity-map.md) is the full
description. In short:

- A part is named by a `part` field in the trace file, by the
  `X-Ezswap-Part` header when recording, or by `--part-key`. A call with no
  name is grouped by a fingerprint of its system prompt, forced tool and
  response schema.
- Each part has a key: `name:<name>`, or `fp1:<hash>` for a fingerprint,
  with the default name `part-<first 6 characters>`.
- When the parts that would run include at least four fingerprint groups and
  more than half hold a single call, the commands refuse (exit 2, error code
  `parts_not_found`) and ask for names.

### The names file

`estimate --map` and `map` read `ezswap-parts.json` in the trace file's
directory and add any new key with its default name. They never change or
remove an entry.

```json
{
  "schema": "metergraph.replay-parts/v1",
  "parts": {
    "fp1:ecc195d1a7b4": "draft-reply",
    "name:classify-ticket": "classify-ticket"
  }
}
```

Edit a name to rename a part. Give two keys the same name to merge them.
`--name` does the same for one command and saves nothing. A names file that
is not valid JSON, or has another `schema`, is a usage error.

`report` does not read the names file. A map run keeps the names that were
in effect when it was made; `report --name` renames for one printing.

### Budget

- `--budget` defaults to 5.00 for `map` and `estimate --map`.
- An expected spend over the budget refuses before any provider call (exit
  4). The message suggests raising `--budget`, lowering `--per-part`, or
  keeping fewer parts with `--only`.
- Cases run part by part, largest part first. If the budget is reached
  partway, the run stops (exit 6, `"stopped": "budget"`) and parts are
  labelled from the cases that finished.

Everything under [Spend](#spend) applies to `map` as it does to `run`.

## The map explorer

On an interactive terminal, `map` and `report` on a map run print the map
and then open the map explorer:

- **The grid:** every model on every part, the first beyond-noise cell
  selected, with the selection described in a sentence. Arrow keys or
  `h j k l` move; `enter` opens a cell.
- **A cell:** its label and counts, the fields that changed, the notes, and
  the changed cases with what each request said. `a` shows every case;
  `enter` opens one.
- **A case:** the request, then the recorded reply, the model's reply and
  production's rerun, side by side on a wide terminal and stacked on a narrow
  one. Left and right step through the cases.
- `o` opens the HTML page in your browser: the `--html` file if you gave
  one, otherwise a fresh page in a private temporary folder. `esc` goes back,
  `?` shows the keys, `q` quits.

`record` on an interactive terminal shows a live dashboard: where to point
your app, calls per part, the latest calls and the time running. `q`, `Esc`
or Ctrl-C stops it, and the summary follows.

Pipes, CI (`CI` set), `--json` and `--no-interactive` keep the plain text
output.

## The report

`run` and `demo` print the report; `report <run.json>` explores it. The
report fits the terminal's width (40 to 140 columns, 100 when the width is
unknown). It answers first, from the top:

1. One line: how many of production's calls were replayed, the trace file,
   the run's date and what it spent (with the budget).
2. One table with a column for production, `itself` (production's model
   replayed against itself, the noise floor) and each model tried, labelled
   `A`, `B`, ...:
   - **COST**: per month, when the call volume is known, and per call.
   - **QUALITY**: agreement with production on each tracked field, then
     output signals such as JSON parses and cut off.
   - **SPEED**: median latency.

   A `*` marks a figure further from production than production is from
   itself.
3. **WHAT CHANGED**: the calls whose tracked fields changed, with the old and
   new values, up to eight, then how many more.
4. How many calls were not replayable, and the most common reason.
5. A footer: what `itself` means and the command to explore every call.

`--explain` adds NOTES after it: each model's settings and endpoint, how to
read `itself`, what `production` stands for in cost and latency, how the
monthly projection and cost per call were worked out, and how to fix each
reason calls were not replayed.

Colour marks which arm a figure belongs to (production, the treatments,
production against itself); it never marks a figure as good or bad.

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
bounded at one token per byte, priced at the full rate: an off-peak discount
in the catalog is not counted, since the call may be billed at peak.

`run` and `map` refuse when the expected spend is over `--budget`, or when a
model has no price and `--allow-unpriced` is not set. While one runs:

- Each call reserves its upper bound before it is sent. A case starts only
  when all of its calls fit in the budget beside what is spent and still
  reserved, so a budget that runs out drops whole cases. A case that does not
  fit waits for calls in flight to settle; if it still does not fit, the run
  stops (`"stopped": "budget"`).
- A call's cost is what the endpoint reported, else the catalog price of the
  usage it returned, else its reservation (a reply with no usage, or a failure
  that may have reached the model). `costSource` in `run.json` says which. A
  local endpoint costs nothing (`"free"`).
- A call that cost more than it reserved means the price catalog is wrong
  for that model on that endpoint, such as a gateway routing to a provider
  that charges more than the listed price. The run multiplies that model's
  later reservations by the overrun, with 25% headroom, and goes on within
  the budget. `run.json` lists the model under `repriced` with the factor,
  and the run's summary says so. The costs recorded are what the endpoint
  billed.
- The run stops at once (`"stopped": "reservation_exceeded"`) when a call
  costs more than 4 times its reservation, or when a model overruns again
  after its reservations were raised twice.
- Ctrl-C stops sending, waits for the calls in flight, and writes what exists
  (`"stopped": "interrupted"`). A second Ctrl-C cancels the calls in flight.

Calls that were not sent are in `run.json` as `"status": "skipped"`, with the
reason.

### Prices

ezswap bundles Metergraph's public price catalog (`internal/pricing/prices.json`,
built from metergraph-core's `prices.yaml`), so pricing works offline. When a
`creator/model` id you name is not in it, `estimate`, `run` and `map` ask
Metergraph's free [Pricing API](https://metergraph.dev/docs/reference/pricing-api)
for that id on the `vercel-ai-gateway` channel, and print which models it
priced:

```text
Prices from Metergraph's Pricing API: openai/gpt-6.1-terra. Only model names were sent. Turn this off with --no-price-lookup.
```

Only the model ids are sent. Answers, unknown models included, are cached for
a day in the user cache directory. `run.json` records the catalog as, for
example, `metergraph-core prices.yaml 2026-11-18 (28e57272a1ab) + Metergraph
Pricing API (1 model)`. A bare id (no `creator/`) is not looked up, because
the API needs the provider that billed it. If the API cannot be reached, the
models stay unpriced and ezswap says so.

| Setting | Effect |
|---|---|
| `--no-price-lookup` | No lookup for this command. |
| `EZSWAP_NO_PRICE_LOOKUP=1` | No lookup, ever. |
| `METERGRAPH_PRICING_URL` | Another Pricing API base URL, such as a self-hosted Metergraph. |

## The HTML page

`--html FILE` writes the result as one HTML file with its data, styles and
script inline. It opens from disk, works offline and makes no network
request (a content security policy refuses any), so it can be attached to a
pull request or sent as it is.

- For a map: a table of every model on every part, then, for the cell you
  choose, its counts, the fields compared, the notes, and each case with the
  request, the recorded reply, the model's reply and production's rerun side
  by side. Free-text parts show the replies side by side to read.
- For a plain run: the report's table, cost, latency, production against
  itself, and each case's replies.

The page holds the prompts and replies from the trace file, so it is written
readable by you only (mode 0600) and should go only where the traces may go.
With `report`, `--html` writes the page instead of opening the explorer.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Done. |
| 1 | Any other error, or every call failed. `record`: the port is in use, the upstream is not accepted, or the trace file cannot be written. |
| 2 | Usage: a bad option, value or file. Also an option the command does not take, `report --map` on a run that is not a map, a bad names file, and a file whose parts cannot be told apart (`parts_not_found`). |
| 3 | No case in the sample can be replayed. For a map: no case in any part. |
| 4 | Refused: expected spend over the budget, unpriced models, or no confirmation. |
| 5 | Endpoint configuration, such as a missing key or base URL. |
| 6 | Interrupted, or stopped early (budget, or a call above its reservation). |

With `--json` every command prints one object with `ok`, `exitCode` and
`command`; a failure adds `error: { code, message }`. `map`, `report` on a
map run and `demo --map` add a `parts` array, described in
[the sensitivity map](sensitivity-map.md#json). `record` prints its object
when it stops.

## Development

```sh
go run ./cmd/ezswap demo --out /tmp/replay-demo   # run from source
go test ./...
go test ./internal/report -update   # accepts changed report output in internal/report/testdata/golden
go run ./cmd/build-demo             # regenerates demo/traces.jsonl, demo/run.json, demo/map-traces.jsonl and demo/map-run.json
```

See [CONTRIBUTING.md](../CONTRIBUTING.md) for the rest.
