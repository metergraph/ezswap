# Recording

`metergraph-replay record` captures your app's model calls into a trace file
that `map`, `run` and `estimate` can read. Use it when you have no trace
export yet.

```sh
metergraph-replay record
```

`record` starts a local reverse proxy. You point your app's model base URL
at it. The proxy forwards each call to the provider, hands the answer back
unchanged, and appends one line for each captured call to
`./replay-out/traces.jsonl`.

## Options

| Option | Default | |
|---|---|---|
| `--upstream URL` | `https://api.openai.com` | The one provider calls are forwarded to. It must be `https`, or `http` to this machine (`localhost`, `127.0.0.1`, `::1`). It may not carry a user name, a query string or a fragment. |
| `--port N` | 8787 | The port on `127.0.0.1`. `0` picks a free port, which `record` prints. |
| `--out DIR` | `./replay-out` | The trace file is `DIR/traces.jsonl`. |

`record` takes no trace file and none of the replay options (`--models`,
`--budget` and so on). It replays nothing.

For another provider, name its base URL:

```sh
metergraph-replay record --upstream https://api.anthropic.com
metergraph-replay record --upstream https://generativelanguage.googleapis.com
metergraph-replay record --upstream http://localhost:11434    # a local server
```

## Point your app at the recorder

When it starts, `record` prints the address to use. With the default port:

- OpenAI SDKs: `http://127.0.0.1:8787/v1`
- Anthropic and Google SDKs: `http://127.0.0.1:8787`

Your API key stays where it is. The app sends it as usual and the recorder
passes it through to the provider.

Two request headers label a call. The recorder reads them and removes them
before forwarding.

| Header | Written as | Use |
|---|---|---|
| `X-Replay-Part` | `part` | Names the place in your app the call comes from. This is what [the map](sensitivity-map.md) groups by. |
| `X-Replay-Trace` | `trace_id` | Groups the calls of one request or session. `map` and `run` sample one case per trace. |

Both values are cleaned before they are written: control characters are
made harmless, spaces at either end are trimmed, and anything past 64
characters is cut.

### OpenAI

Python:

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:8787/v1",
    default_headers={"X-Replay-Part": "classify-ticket"},
)
```

TypeScript:

```ts
import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "http://127.0.0.1:8787/v1",
  defaultHeaders: { "X-Replay-Part": "classify-ticket" },
});
```

### Anthropic

Start the recorder with `--upstream https://api.anthropic.com`.

Python:

```python
from anthropic import Anthropic

client = Anthropic(
    base_url="http://127.0.0.1:8787",
    default_headers={"X-Replay-Part": "classify-ticket"},
)
```

TypeScript:

```ts
import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic({
  baseURL: "http://127.0.0.1:8787",
  defaultHeaders: { "X-Replay-Part": "classify-ticket" },
});
```

### Google

Start the recorder with
`--upstream https://generativelanguage.googleapis.com`. Set the SDK's base
URL to `http://127.0.0.1:8787` and add the header through its HTTP options.
The SDK's own documentation names the settings for your version.

### One header per part

`default_headers` labels every call a client makes. An app with several
parts needs one client for each part, or the SDK's per-request header
option. Send `X-Replay-Trace` per request, because its value changes with
every request.

A call with no `X-Replay-Part` is still recorded. The map then groups it by
a fingerprint of its system prompt, forced tool and response schema.

## What is recorded

A request is a **model call** when it is a `POST` and its path ends in one
of:

| Path ending | API |
|---|---|
| `/chat/completions` | OpenAI Chat Completions |
| `/responses` | OpenAI Responses |
| `/v1/messages` | Anthropic Messages |
| `:generateContent` | Gemini |
| `:streamGenerateContent` | Gemini, streamed |

Every other request (listing models, embeddings, uploads) is forwarded and
not recorded.

Each recorded call is one line in the raw-pair format that
[trace formats](trace-format.md#raw-provider-pairs-raw) describes. A line
has this shape (a made-up call):

```json
{"id":"rec-3f9a1c0b7d2e","ts":"2026-03-02T09:00:00Z","latency_ms":640,"provider":"openai","model":"gpt-4o-mini","part":"classify-ticket","trace_id":"ticket-3001","request":{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hi"}]},"response":{"object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"Hello"},"finish_reason":"stop"}]}}
```

| Key | Value |
|---|---|
| `id` | `rec-` and 12 random hexadecimal characters. |
| `ts` | When the request arrived, in UTC. |
| `latency_ms` | From the request's arrival to the last byte of the response. |
| `provider` | `openai`, `anthropic` or `google`, from the path. |
| `model` | The request body's `model`. For Gemini, the model in the path. |
| `part`, `trace_id` | The two headers, when sent. |
| `request`, `response` | The provider's request and response bodies. |

### Calls forwarded but not recorded

Some model calls are forwarded as usual and left out of the file. `record`
prints the reason beside each one and totals them when it stops.

| Reason | When |
|---|---|
| `error status` | The provider did not answer with a 2xx status. This includes a provider that could not be reached. |
| `compressed` | The request body was compressed, or the provider compressed its answer. The recorder asks the provider for an uncompressed answer, so the second case is rare. |
| `too large` | A request or response body over 8 MiB. It is still forwarded in full. |
| `stream cut` | A response that stopped before its end: the provider or the app went away, or the recorder was stopped. |
| `not JSON` | A body, or a stream event, that is not JSON. |
| `stored state` | The request carries a `previous_response_id`. It continues a conversation the provider stores, so it cannot be replayed from this call alone. |
| `write failed` | The line could not be written to the file. |

### Streamed answers

A streamed answer reaches your app untouched, piece by piece as it arrives.
From the events as they pass, the recorder rebuilds the body the provider
would have sent without streaming, and writes that.

| API | Rebuilt from |
|---|---|
| Chat Completions | The chunks up to `data: [DONE]`: text, refusal, tool calls, finish reason, and token usage when the stream carries it. |
| Responses | The response inside the `response.completed` event. |
| Anthropic Messages | `message_start`, the content blocks and their deltas (text, thinking, tool input), `message_delta`, up to `message_stop`. |
| Gemini | Every piece of each candidate, joined. Both stream forms are read: server-sent events and one JSON array. The stream must report a finish reason. |

A stream that does not reach its final event is not recorded (`stream cut`).

## While it runs, and when it stops

While it runs, `record` prints one line for each model call: `recorded` or
the reason it was not, the part name (`(unnamed)` without one), and the
request path without its query string.

Stop it with Ctrl-C. The recorder stops accepting requests, waits up to 10
seconds for calls in flight, and cuts whatever is still open. A call cut
this way is not recorded. Then it prints:

- how many calls it recorded, and how many for each part,
- the calls it forwarded and did not record, by reason,
- how many cases in the file a replay can reproduce,
- the next command: `metergraph-replay estimate <file> --map --models <list>`.

The file is appended to, never truncated. Run `record` again with the same
`--out` to add calls. Lines are written whole, in the order calls finish.

`record` exits with 0 after a stop. A port already in use, an upstream it
will not accept, or a trace file it cannot open exits with 1. With `--json`
it prints one object when it stops: `file`, `recorded`, `byPart`, `skipped`,
`forwarded`, `replayable` and `notReplayable`.

## What the recorder keeps to itself

- **It listens on `127.0.0.1` only.** Other machines cannot reach it.
- **It refuses requests that do not look local.** A request must name
  `127.0.0.1:<port>` or `localhost:<port>` as its host and carry no `Origin`
  header. Anything else gets a 403. This stops a web page from reaching the
  port.
- **No header is written or shown**, except the two cleaned `X-Replay-*`
  values. Your API key is in a header. It goes to the provider and nowhere
  else.
- **No query string is written or shown.** Some providers carry the key
  there. The query goes to the provider exactly as the app wrote it.
- **Errors do not repeat the upstream URL.** When the provider cannot be
  reached, the app gets a 502 that names the kind of failure only (a timeout,
  a refused connection).
- **The trace file is created with mode `0600`** and a new output directory
  with `0700`, so only your user can read them. An existing file keeps the
  mode it has.

Every header other than the two `X-Replay-*` ones goes to the provider as
the app sent it, with one exception: the recorder sets `Accept-Encoding` to
`identity` so it can read the answer. If the standard proxy environment
variables (`HTTPS_PROXY` and friends) are set, forwarded calls go through
that proxy.

## Limits

- **One provider per recorder.** Every request goes to the one `--upstream`.
  An app that calls two providers needs two recorders on two ports. Give
  each its own `--out`, or they share a trace file.
- **An app in a container cannot reach the host's `127.0.0.1`.** The
  recorder listens on the loopback address of the machine it runs on. Run
  the recorder inside the same container or network namespace as the app.
- **Browser code is refused.** A browser sends an `Origin` header. Record
  from your server, not from a page.
- **The trace file holds prompts and responses.** Treat it like production
  data and keep it out of Git. `record` warns when the output directory is
  inside a Git repository.
- **Request bodies are written as sent.** The recorder never writes a header
  or a query string, but it does not look inside a body. A credential your
  app puts in a request body (a token for a remote tool server, say) is
  written to the file.
- **Recorded test traffic is not production traffic.** A map of calls you
  made by hand shows how the models differ on those calls. Record real use,
  or a replay of real inputs, before you rely on it.
- **A conversation the provider stores is not recorded** (`stored state`),
  and neither is a call over 8 MiB.

## A sample app

`examples/support-desk` is a made-up support desk that calls a model from
three parts (`classify-ticket`, `extract-order`, `draft-reply`) and sends
both headers. `draft-reply` streams. Every ticket, order and address in it
is invented.

```sh
metergraph-replay record
# in another terminal, from a clone of this repository:
go run ./examples/support-desk --base-url http://127.0.0.1:8787/v1 --model gpt-4.1-mini
```

It reads `OPENAI_API_KEY` from the environment and makes 12 calls from each
part (`--calls` changes that). Then stop the recorder and list the parts:

```sh
metergraph-replay estimate replay-out/traces.jsonl --map --models openai/gpt-5-mini
```
