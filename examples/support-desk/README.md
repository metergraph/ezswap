# Support desk example

A made-up support desk that calls a model from three places: it classifies a
ticket, extracts an order, and drafts a reply (streamed). Use it to try
`record` and `map` end to end. Every ticket, order and address is invented.

## With a local model (free)

This uses [Ollama](https://ollama.com) with two small models. Any two models
you have pulled will do.

Start the recorder in one terminal, forwarding to Ollama:

```sh
metergraph-replay record --upstream http://localhost:11434
```

Run the app through it in another:

```sh
go run ./examples/support-desk --base-url http://127.0.0.1:8787/v1 --model qwen2.5:3b
```

Stop the recorder with Ctrl-C. Then list the parts and make the map, with
another model as the treatment:

```sh
metergraph-replay estimate replay-out/traces.jsonl --map --models qwen2.5-coder:7b --endpoint ollama
metergraph-replay map replay-out/traces.jsonl --models qwen2.5-coder:7b --endpoint ollama
```

## With a hosted provider

The recorder forwards to `https://api.openai.com` by default, and the app
sends `OPENAI_API_KEY` if it is set. The key passes through to the provider.
It is never written to the trace file or shown.

```sh
metergraph-replay record
go run ./examples/support-desk --model gpt-4.1-mini
metergraph-replay map replay-out/traces.jsonl --models openai/gpt-5-mini --endpoint openai
```

`map` prints an estimate and asks before it spends anything.

## What the app sends

Each call carries two headers, which the recorder writes to the trace file and
removes before forwarding:

| Header | Value |
|---|---|
| `X-Replay-Part` | The place in the app: `classify-ticket`, `extract-order` or `draft-reply`. |
| `X-Replay-Trace` | An id for the request, so the map samples one call per request. |
