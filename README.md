# Metergraph Replay

Would a cheaper model have worked on your real traffic?

Metergraph Replay replays your production LLM calls from a trace file on other
models, prices the run before it spends anything, and shows cost, latency and
output differences against what production returned. Every difference is read
against a **noise floor**: how much production's own model disagrees with
itself on the same calls.

Local, no account, your own keys. Nothing leaves your machine except the
provider calls you ask for, and Replay sends no telemetry.

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
metergraph-replay demo
```

`demo` prints the report of bundled synthetic traces with recorded replies. It
needs no keys and makes no network calls. Add `--explore` to open the
interactive explorer.

## Replay your own traces

```sh
metergraph-replay estimate traces.jsonl --models openai/gpt-5-mini,anthropic/claude-haiku-4.5
metergraph-replay run traces.jsonl --models openai/gpt-5-mini,anthropic/claude-haiku-4.5 --budget 2.00
metergraph-replay report replay-out/run.json
```

- `estimate` says how many calls can be replayed and prices the run. It makes
  no provider call.
- `run` estimates, asks before spending, replays every call on every model
  plus the noise floor, and stops at `--budget` (default $1.00).
- `report` opens the explorer on a finished run.

Models go to an OpenAI-compatible endpoint you bring: OpenRouter (the
default), OpenAI, Vercel AI Gateway, a LiteLLM proxy, Ollama, or any base
URL. Keys are read from environment variables and never written anywhere.

Replay reads traces in its own JSONL format, raw provider request and response
pairs (including Helicone and LiteLLM logs), Langfuse observation exports and
OpenTelemetry GenAI spans.

## The report

The report answers first: one table with cost, quality and speed for
production, production replayed against itself (`itself`, the noise floor)
and each model you tried, then the calls whose answers changed. This is
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
Keep this running on live traffic:
https://www.metergraph.dev/replay?utm_source=replay&utm_medium=cli&utm_campaign=0.0.0#setup=eyJ2IjoxLCJ0cmVhdG1lbnRzIjpbeyJtb2RlbCI6Imdvb2dsZS9nZW1pbmktMi41LWZsYXNoLWxpdGUiLCJzZXR0aW5ncyI6eyJ0ZW1wZXJhdHVyZSI6bnVsbCwibWF4VG9rZW5zIjpudWxsLCJyZWFzb25pbmdFZmZvcnQiOm51bGx9fSx7Im1vZGVsIjoib3BlbmFpL2dwdC01LjQtbmFubyIsInNldHRpbmdzIjp7InRlbXBlcmF0dXJlIjpudWxsLCJtYXhUb2tlbnMiOm51bGwsInJlYXNvbmluZ0VmZm9ydCI6bnVsbH19XSwidHJhY2tlZEZpZWxkcyI6WyIkLmNhdGVnb3J5IiwiJC5wcmlvcml0eSJdLCJzYW1wbGUiOjIwfQ
```

A `*` marks a figure further from production than production is from
itself. `--explain` prints notes on reading it: what each model ran with,
how cost was estimated, and why calls were not replayed. The explorer
(`report`, or `demo --explore`) adds tabs for the tracked fields, the noise
floor and every call side by side.

Statistics are presentation, never a verdict. Nothing in the report says a
model is good enough to switch to.

## Documentation

- [Command line](https://github.com/metergraph/replay/blob/main/docs/cli.md): commands, options, endpoints, spend and exit codes.
- [Trace formats](https://github.com/metergraph/replay/blob/main/docs/trace-format.md): what each input format must carry.
- [The noise floor](https://github.com/metergraph/replay/blob/main/docs/noise-floor.md): what it measures and how to read it.
- [The Metergraph link](https://github.com/metergraph/replay/blob/main/docs/hosted-handoff.md): what the link at the end of the report carries.

## Contributing and security

See
[CONTRIBUTING.md](https://github.com/metergraph/replay/blob/main/CONTRIBUTING.md).
Report vulnerabilities privately as described in
[SECURITY.md](https://github.com/metergraph/replay/blob/main/SECURITY.md).

Licensed under
[Apache-2.0](https://github.com/metergraph/replay/blob/main/LICENSE).
