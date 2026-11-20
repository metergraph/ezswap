# Support desk example

A made-up support desk that calls a model from four places: it classifies a
ticket, extracts an order, decides a refund under a written policy, and
drafts a reply (streamed). Each place has 24 hand-written inputs. Use it to
try `record` and `map` end to end. Every ticket, order and address is
invented.

## With a local model (free)

This uses [Ollama](https://ollama.com) with two small models. Any two models
you have pulled will do.

Start the recorder in one terminal, forwarding to Ollama:

```sh
ezswap record --upstream http://localhost:11434
```

Run the app through it in another:

```sh
go run ./examples/support-desk --base-url http://127.0.0.1:8787/v1 --model qwen2.5:3b
```

Stop the recorder with Ctrl-C. Then list the parts and make the map, with
another model as the treatment:

```sh
ezswap estimate ezswap-out/traces.jsonl --map --models qwen2.5-coder:7b --endpoint ollama
ezswap map ezswap-out/traces.jsonl --models qwen2.5-coder:7b --endpoint ollama
```

## With a hosted provider

The recorder forwards to `https://api.openai.com` by default, and the app
sends `OPENAI_API_KEY` if it is set. The key passes through to the provider.
It is never written to the trace file or shown.

```sh
ezswap record
go run ./examples/support-desk --model gpt-4.1-mini
ezswap map ezswap-out/traces.jsonl --models openai/gpt-5-mini --endpoint openai
```

To go through [Vercel AI Gateway](https://vercel.com/docs/ai-gateway)
instead, forward the recorder there and give the app your gateway key. This
records the app on an expensive model and asks which places could move to
cheaper ones, with `--judge` comparing the free-text replies:

```sh
ezswap record --upstream https://ai-gateway.vercel.sh
OPENAI_API_KEY="$AI_GATEWAY_API_KEY" go run ./examples/support-desk --model anthropic/claude-sonnet-5.5
ezswap map ezswap-out/traces.jsonl --endpoint vercel --judge \
  --models anthropic/claude-haiku-5.5,openai/gpt-5.4-nano,openai/gpt-5.4-mini
```

Recording made 96 calls for about $0.25. The map made 400 replay calls and
200 judge calls for about $0.17. In two runs, Haiku 5.5 stayed within noise
on every part for 93% less per call, and GPT-5.4 nano changed the priority of
15 of 20 tickets.

`map` prints an estimate and asks before it spends anything. The app makes
24 calls a part by default (`--calls`), and the map samples the latest 20 of
each. At 20 cases, 4 or 5 changes against none on a rerun sits on the line
between beyond and within noise, so such a cell can flip between runs.

## What the app sends

Each call carries two headers, which the recorder writes to the trace file and
removes before forwarding:

| Header | Value |
|---|---|
| `X-Ezswap-Part` | The place in the app: `classify-ticket`, `extract-order`, `refund-decision` or `draft-reply`. |
| `X-Ezswap-Trace` | An id for the request, so the map samples one call per request. |
