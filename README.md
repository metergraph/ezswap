# Metergraph Replay

Which parts of my app change when I change the model?

Metergraph Replay replays your recorded LLM calls on other models and shows
what changes. Its **sensitivity map** answers the question part by part: for
each place in your app that calls a model, how many answers change on the
new model, set against how many change when production's own model is simply
run again. That second figure is the **noise floor**. A part is flagged only
when the new model changes more than a rerun does.

Replay prices every run before it spends anything, and can also compare
models over a whole trace file for cost, latency and output differences.

Local, no account, your own keys. Replay sends no telemetry. The network
traffic is the provider calls you ask for (the calls a `run` or `map` sends,
and the calls `record` forwards for your app), plus one request to
Metergraph's free [Pricing API](https://metergraph.dev/docs/reference/pricing-api)
when a model you name is not in Replay's bundled price catalog. That request
carries the model names only, never a trace, prompt or key. The answer is
cached for a day. Turn it off with `--no-price-lookup` or
`METERGRAPH_REPLAY_NO_PRICE_LOOKUP=1`.

Replay writes three files, all on your machine: `run.json` (a finished run
or map), `traces.jsonl` (what `record` captured) and `replay-parts.json`
(part names, beside the trace file).

## Install

```sh
brew install metergraph/tap/metergraph-replay
```

or with Go 1.26 or later:

```sh
go install github.com/metergraph/replay/cmd/metergraph-replay@latest
```

or from npm, which installs the same native binary for your platform:

```sh
npx metergraph-replay demo
npm install -g metergraph-replay
```

Prebuilt archives for macOS, Linux and Windows (amd64 and arm64) are on the
[releases page](https://github.com/metergraph/replay/releases).

## Try it

```sh
metergraph-replay demo --map
```

`demo --map` prints the map of a bundled synthetic support desk with five
parts. It needs no keys, makes no network request and writes no file. This
is its output with `COLUMNS=80` and stdout not a terminal (so plain ASCII):

```text
A map of a synthetic support desk with five parts, recorded so it needs no keys and no network.
Make one from your own traces:
  metergraph-replay map traces.jsonl --models openai/gpt-5-mini

5 parts | map-traces.jsonl | 2026-10-20 to 2026-10-27 | 20 cases per part
spent $0.016 of $5.00

treatment A openai/gpt-5.4-nano

PART             LABEL           CHANGED   RERUN     COST/CALL  LATENCY
extract-order    beyond noise    9 of 20   1 of 20   -35%       -52%
route-to-team    within noise    4 of 20   3 of 20   -37%       -41%
classify-ticket  within noise    2 of 20   3 of 20   -36%       -33%
summarise-thread too few calls   6 paired, needs 10  -28%       -41%
draft-reply      not comparable  free text           -29%       -45%

WHAT CHANGED
extract-order
  sku       changed 4, rerun 1
  quantity  changed 6, rerun 0
  order_id  changed 1, rerun 0
  call-0003  quantity  5 -> 6
  call-0004  sku       "B-204" -> "B-204B"
  call-0006  quantity  2 -> 3
  call-0008  sku       "B-19" -> "B-19B"
  call-0010  quantity  4 -> 5
  call-0013  quantity  1 -> 2
  call-0015  sku       "C-77" -> "C-77B"
  call-0017  quantity  3 -> 4
  and 1 more
route-to-team
  team    changed 4, rerun 3
  call-0022  team    "support" -> "sales"
  call-0027  team    "support" -> "sales"
  call-0031  team    "billing" -> "support"
  call-0036  team    "billing" -> "support"

CHANGED: cases where the treatment's answer differs from the recorded one.
RERUN: the same for a fresh run of production's own model. A part is beyond
noise when CHANGED exceeds RERUN by a paired sign test at 5%.
This shows where outputs change. It does not say a change is better or worse.
```

How to read it:

- **`CHANGED`**: cases where the new model's answer differs from the
  recorded one.
- **`RERUN`**: cases where a fresh run of production's own model differs
  from the recorded one.
- **`beyond noise`**: `CHANGED` exceeds `RERUN` by a paired sign test at 5%.
  Here the new model changes order extraction (9 of 20 against 1 of 20) and
  leaves routing and classification about as steady as production is by
  itself.
- **`COST/CALL`** and **`LATENCY`**: the new model against a rerun of
  production's.

The map shows where outputs change. It does not say a change is better or
worse. [The sensitivity map](https://github.com/metergraph/replay/blob/main/docs/sensitivity-map.md)
explains every rule and the limits of the label.

`metergraph-replay demo` (without `--map`) prints the whole-file report
described [below](#the-report), and `--explore` opens the interactive
explorer.

## Map your own app

A **part** is a place in your app that calls a model. You name it. Replay
never guesses a name from your prompts.

### 1. Get a trace file

If you already export traces, use them: Replay reads its own JSONL format,
raw provider request and response pairs (including Helicone and LiteLLM
logs), Langfuse observation exports and OpenTelemetry GenAI spans. Name the
parts with a `part` field, or point `--part-key` at a key your records
already carry.

If you do not, record one:

```sh
metergraph-replay record
```

`record` runs a local proxy on `127.0.0.1:8787`. Point your app's model base
URL at it (`http://127.0.0.1:8787/v1` for OpenAI SDKs) and send the header
`X-Replay-Part` with each call to name the part. The proxy forwards every
call to one provider (`--upstream`, default `https://api.openai.com`) and
appends each captured call to `replay-out/traces.jsonl`. Your API key passes
through and is never written or shown. The trace file holds prompts and
responses, so keep it out of Git. See
[Recording](https://github.com/metergraph/replay/blob/main/docs/record.md)
for SDK settings and limits.

### 2. List the parts and the price

```sh
metergraph-replay estimate replay-out/traces.jsonl --map --models openai/gpt-5-mini
```

`estimate --map` lists each part, how it was named, how many cases it has,
which fields it will be compared on and what the map will cost. It makes no
provider call. It writes `replay-parts.json` beside the trace file: edit a
name there to rename a part, or give two parts one name to merge them.

### 3. Make the map

```sh
metergraph-replay map replay-out/traces.jsonl --models openai/gpt-5-mini
```

`map` asks before spending, replays the latest 20 cases of each part
(`--per-part`) on each model and twice on production's own model, stops at
`--budget` (default $5.00), writes `run.json` and prints the map. Print it
again with `metergraph-replay report replay-out/run.json`.

Calls with no part name are grouped by a fingerprint of their system prompt,
forced tool and response schema. Parts whose outputs are free text are
priced and timed but not compared: the map compares JSON fields.

## Compare models over a whole trace file

`run` answers a different question: over the latest calls in the file,
whatever part they came from, how do cost, latency and outputs differ?

```sh
metergraph-replay estimate traces.jsonl --models openai/gpt-5-mini,anthropic/claude-haiku-4.5
metergraph-replay run traces.jsonl --models openai/gpt-5-mini,anthropic/claude-haiku-4.5 --budget 2.00
metergraph-replay report replay-out/run.json
```

- `estimate` says how many calls can be replayed and prices the run. It makes
  no provider call.
- `run` estimates, asks before spending, replays every call on every model
  plus the noise floor, and stops at `--budget` (default $1.00).
- `report` opens the explorer on a finished run. On a run that `map` made,
  it prints the map instead.

Models go to an OpenAI-compatible endpoint you bring: OpenRouter (the
default), OpenAI, Vercel AI Gateway, a LiteLLM proxy, Ollama, or any base
URL. Keys are read from environment variables and never written anywhere.

## The report

`run` and `demo` print the report. It answers first: one table with cost,
quality and speed for production, production replayed against itself
(`itself`, the noise floor) and each model you tried, then the calls whose
answers changed. This is
`metergraph-replay demo` at 80 columns, without colour:

```text
20 of 22 gpt-4.1-mini calls replayed · traces.jsonl · 28 Oct 2026 · spent $0.013

                   production    itself  A gemini-2.5-flash-l…   B gpt-5.4-nano
COST
  per month            $35.47    $35.51                  $8.54*          $87.05*
  per call           $0.00014  $0.00014              $0.000034*        $0.00035*
QUALITY
  category agrees           —      100%                    90%*            100%
  priority agrees           —       85%                    85%              89%
  JSON parses            100%      100%                   100%              95%*
  cut off                  0%        0%                     0%               5%*
SPEED
  median latency       798 ms     1.0 s                 418 ms            1.8 s*
* further from production than production is from itself

WHAT CHANGED
  ticket-0007  A  priority  medium → high
  ticket-0009  B  priority  medium → high
  ticket-0011  A  priority  high → urgent
  ticket-0012  A  category  bug → outage
  ticket-0016  A  category  billing → bug
  ticket-0017  B  priority  medium → low
  ticket-0018  A  priority  high → urgent

2 calls not replayable, most often: content cut short (1).

itself: gpt-4.1-mini replayed again, the noise floor. Notes: run with --explain.
Explore every call: metergraph-replay report replay-out/run.json
```

A `*` marks a figure further from production than production is from
itself. `--explain` prints notes on reading it: what each model ran with,
how cost was estimated, and why calls were not replayed. The explorer
(`report`, or `demo --explore`) adds tabs for the tracked fields, the noise
floor and every call side by side.

The `*` here and the map's `beyond noise` follow different rules. The report
marks one figure at a time when its gap from production is larger than
production's gap from itself. The map labels a whole part with a paired
test. [The noise floor](https://github.com/metergraph/replay/blob/main/docs/noise-floor.md)
and [the sensitivity map](https://github.com/metergraph/replay/blob/main/docs/sensitivity-map.md)
describe each.

Statistics are presentation, never a verdict. Nothing in the report or the
map says a model is good enough to switch to.

Metergraph runs this comparison continuously on live traffic: https://www.metergraph.dev

## Documentation

- [The sensitivity map](https://github.com/metergraph/replay/blob/main/docs/sensitivity-map.md): parts, what is compared, the label's rule and its limits.
- [Recording](https://github.com/metergraph/replay/blob/main/docs/record.md): the local proxy, SDK settings, what is written and its limits.
- [Command line](https://github.com/metergraph/replay/blob/main/docs/cli.md): commands, options, endpoints, spend and exit codes.
- [Trace formats](https://github.com/metergraph/replay/blob/main/docs/trace-format.md): what each input format must carry, and how parts are named.
- [The noise floor](https://github.com/metergraph/replay/blob/main/docs/noise-floor.md): what it measures in the whole-file report and how to read it.

## Contributing and security

See
[CONTRIBUTING.md](https://github.com/metergraph/replay/blob/main/CONTRIBUTING.md).
Report vulnerabilities privately as described in
[SECURITY.md](https://github.com/metergraph/replay/blob/main/SECURITY.md).

Licensed under
[Apache-2.0](https://github.com/metergraph/replay/blob/main/LICENSE).
