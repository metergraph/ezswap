# Metergraph Replay

Would a cheaper model have worked on your real traffic?

Metergraph Replay replays your production LLM calls from a trace file on other
models, prices the run before it spends anything, and shows cost, latency and
output differences against what production returned. Every difference is read
against a **noise floor**: how much production's own model disagrees with
itself on the same calls.

Local, no account, your own keys. Nothing leaves your machine except the
provider calls you ask for, and Replay sends no telemetry.

## Try it

Requires Node.js 22.6 or later. Replay is not on npm yet, so run it from
source:

```sh
git clone https://github.com/metergraph/replay.git
cd replay
npm ci
npm run dev -- demo
```

`demo` prints the report of bundled synthetic traces with recorded replies. It
needs no keys and makes no network calls. Add `--explore` to open the
interactive explorer.

Once the package is published, the same commands are:

```sh
npx metergraph-replay demo
npx metergraph-replay report replay-out/run.json   # explore every call
```

## Replay your own traces

```sh
npm run dev -- estimate traces.jsonl --models openai/gpt-5-mini,anthropic/claude-haiku-4.5
npm run dev -- run traces.jsonl --models openai/gpt-5-mini,anthropic/claude-haiku-4.5 --budget 2.00
npm run dev -- report replay-out/run.json
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

The report lives in the terminal: cost and a monthly projection, output
signals, tracked JSON fields and latency per model, each beside production's
own variation, then the calls that differ. The explorer adds every call side
by side.

Statistics are presentation, never a verdict. Nothing in the report says a
model is good enough to switch to.

## Documentation

- [Command line](docs/cli.md): commands, options, endpoints, spend and exit codes.
- [Trace formats](docs/trace-format.md): what each input format must carry.
- [The noise floor](docs/noise-floor.md): what it measures and how to read it.
- [The Metergraph link](docs/hosted-handoff.md): what the link at the end of the report carries.

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md). Report vulnerabilities privately as
described in [SECURITY.md](SECURITY.md).

Licensed under [Apache-2.0](LICENSE).
