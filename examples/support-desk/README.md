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

To go through [Vercel AI Gateway](https://vercel.com/docs/ai-gateway)
instead, forward the recorder there and give the app your gateway key:

```sh
metergraph-replay record --upstream https://ai-gateway.vercel.sh
OPENAI_API_KEY="$AI_GATEWAY_API_KEY" go run ./examples/support-desk --model openai/gpt-4.1-mini --calls 20
metergraph-replay map replay-out/traces.jsonl --models openai/gpt-4.1-nano --endpoint vercel
```

With 20 calls per part this made 180 replay calls and cost about one cent.

`map` prints an estimate and asks before it spends anything. With 12 calls a
part, a change in a few cases can fall short of the test; 20 is the default
sample per part.

## What the app sends

Each call carries two headers, which the recorder writes to the trace file and
removes before forwarding:

| Header | Value |
|---|---|
| `X-Replay-Part` | The place in the app: `classify-ticket`, `extract-order` or `draft-reply`. |
| `X-Replay-Trace` | An id for the request, so the map samples one call per request. |
