# Trace formats

`metergraph-replay` reads a trace file of production LLM calls. It accepts its
own format, described first, and three others it maps onto it: raw provider
request and response pairs (including Helicone exports and LiteLLM callback
logs), Langfuse observation exports, and OpenTelemetry GenAI spans.

A file is JSONL (one record per line), a JSON array of records, or one JSON
document (an OTLP export, say). The format is detected from the first record;
pass it explicitly when detection guesses wrong. Blank lines are ignored and a
line that is not JSON is skipped and reported with its line number.

Replay never replays a guess. A call that cannot be reproduced faithfully is
kept in the report with the reason it was not replayed (see
[Not replayable](#not-replayable)), and a call whose source did not carry
everything (tool definitions, say) is graded `partial`.

## Replay JSONL (`replay-jsonl`)

One call per line. Use it when your traces live somewhere the other adapters
do not read: write a small export script that emits this shape.

```json
{
  "schema": "metergraph.replay-trace/v1",
  "id": "call-0001",
  "trace_id": "trace-0001",
  "provider": "openai",
  "request": {
    "model": "gpt-4o-mini",
    "system": "You write short product blurbs for example.com.",
    "messages": [
      { "role": "user", "parts": [{ "type": "text", "content": "Write a blurb for a blue mug." }] }
    ],
    "tools": [],
    "tool_choice": null,
    "response_format": null,
    "params": { "temperature": 0.3, "max_tokens": 200, "top_p": 0.9, "stop": ["\n\n"], "seed": 7 }
  },
  "response": {
    "messages": [
      {
        "role": "assistant",
        "parts": [{ "type": "text", "content": "A calm blue mug for slow mornings." }],
        "finish_reason": "stop"
      }
    ],
    "usage": { "input_tokens": 31, "output_tokens": 9, "cached_input_tokens": 0 },
    "cost_usd": 0.0000101,
    "latency_ms": 812
  },
  "meta": {
    "source": "example-app",
    "ts": "2026-03-01T10:00:00Z",
    "truncated": false,
    "missing": []
  }
}
```

The record is detected by `schema`, or by a `meta` object beside a `request`
that has `messages`, `raw` or `params`.

### Top level

| Field | Type | Notes |
|---|---|---|
| `schema` | string | Optional. `"metergraph.replay-trace/v1"`. |
| `id` | string | Stable id for the call. When absent, a hash of the line is used. |
| `trace_id` | string | Optional. Groups calls from one request or session. |
| `part` | string | Optional. The place in your app that made the call. See [Parts](#parts). |
| `provider` | string | Provider that served the call: `openai`, `anthropic`, `google`, ... |
| `request` | object | The request. Null or absent: the call is reported as content unavailable. |
| `response` | object | What production returned. |
| `meta` | object | Where the record came from and how complete it is. |

### `request`

| Field | Type | Notes |
|---|---|---|
| `model` | string | Production's model as the provider named it. |
| `system` | string or parts | System prompt. A list of `{ "type": "text", "content": "..." }` parts also works. |
| `messages` | array | Turns in the [OpenTelemetry GenAI input messages](https://opentelemetry.io/docs/specs/semconv/gen-ai/) schema: `{ "role", "parts": [...] }`. Roles are `system`, `user`, `assistant` and `tool`. A part is `{ "type": "text", "content" }`, `{ "type": "tool_call", "id", "name", "arguments" }`, `{ "type": "tool_call_response", "id", "response" }`, or a media part (`blob`, `uri`, `file`). A plain `content` string or a list of OpenAI or Anthropic content blocks is also read. |
| `tools` | array | Tools offered, as OpenAI chat (`{type, function: {name, description, parameters}}`), OpenAI Responses or OpenTelemetry (`{type: "function", name, description, parameters}`) or Anthropic (`{name, description, input_schema}`) definitions. |
| `tool_choice` | string or object | As the provider spelled it: `"required"`, `{type: "function", function: {name}}`, `{type: "function", name}`, `{type: "tool", name}` or `{type: "any"}`. |
| `response_format` | object | OpenAI chat `response_format` (`json_object` or `json_schema`). |
| `params` | object | `temperature`, `max_tokens`, `top_p`, `stop` (string or list), `seed`, `reasoning_effort`. |
| `raw` | object | Optional verbatim provider request body. Used only when `messages` is absent. |

### `response`

| Field | Type | Notes |
|---|---|---|
| `messages` | array | OpenTelemetry GenAI output messages: `{ "role": "assistant", "parts": [...], "finish_reason" }`. |
| `text` | string | Assistant text, instead of or as well as `messages`. Wins when both are given. |
| `tool_calls` | array | Tool calls, `{ "id", "name", "arguments" }`. A single call's JSON arguments are production's output when there is no text. |
| `finish_reason` | string | As the provider reported it. |
| `refusal` | boolean | Production refused. |
| `usage` | object | `input_tokens`, `output_tokens`, `cached_input_tokens` (cache reads), `reasoning_tokens`. |
| `cost_usd` | number | What production paid, when known. |
| `latency_ms` | number | Production's wall time. |

### `meta`

| Field | Type | Notes |
|---|---|---|
| `source` | string | Free text: the system the record was exported from. |
| `ts` | string or number | Start time: ISO 8601, or epoch seconds, milliseconds or nanoseconds. |
| `truncated` | boolean | Set when any request content was cut. A truncated call is not replayed. |
| `missing` | string[] | Request fields the source did not carry, for example `["tools", "response_format"]`. Any entry grades the call `partial`. |

## Raw provider pairs (`raw`)

One record per call holding the provider's own request and response bodies:

```json
{ "id": "call-1", "ts": "2026-03-02T09:00:00Z", "latency_ms": 640, "cost_usd": 0.00002,
  "request": { "model": "gpt-4o-mini", "messages": [{ "role": "user", "content": "Hi" }] },
  "response": { "object": "chat.completion", "choices": [{ "message": { "role": "assistant", "content": "Hello" }, "finish_reason": "stop" }] } }
```

Requests may be OpenAI Chat Completions, OpenAI Responses, Anthropic Messages
or Gemini `generateContent` bodies; responses are read in the same four shapes.
Optional beside them: `id`, `trace_id`, `part`, `ts`, `provider`, `model`,
`latency_ms`, `cost_usd`, `truncated`. The provider is inferred from the body
shape when not given. `part` names the place in your app that made the call
(see [Parts](#parts)).

`metergraph-replay record` writes this format, one line for each call it
captures. See [Recording](record.md).

Two logging tools reduce to this shape and are read directly:

- **Helicone** request exports (for example from `@helicone/export`). Bodies
  are present only when exported with `--include-body`. Without them Helicone
  writes a placeholder body; such a call is reported as content unavailable,
  with its tokens, cost and latency still read. Fields read: `request_id`,
  `request_created_at`, `provider`, `model`, `request_body`, `response_body`,
  `prompt_tokens`, `completion_tokens`, `prompt_cache_read_tokens`,
  `reasoning_tokens`, `cost`, `delay_ms`, and the `Helicone-Session-Id`
  property as the trace id.
- **LiteLLM** `StandardLoggingPayload` records, as a custom callback or a
  logging integration writes them. The request is rebuilt from `messages` and
  `model_parameters`; `response` is the provider-format response. Also read:
  `id`, `trace_id`, `model`, `custom_llm_provider`, `response_cost`,
  `prompt_tokens`, `completion_tokens`, `startTime` and `endTime`. Content
  redacted by LiteLLM is reported as content unavailable.

## Langfuse observation export (`langfuse`)

The JSONL or JSON export of the observations table. Only `GENERATION`
observations are read. Fields read: `id`, `traceId`, `type`, `startTime`,
`endTime`, `model`, `modelParameters`, `input`, `output`, `usageDetails` (or
`usage`), `costDetails.total` (or `totalCost`, `calculatedTotalCost`).

- Use the batch export: it carries full input and output. Previews in the UI
  and API cut long values with a `...[truncated]` marker; those calls are not
  replayed.
- When `input` is a bare message list, Langfuse did not record the tools
  offered, so `tools` is marked missing and the call is graded `partial`.
- The provider is inferred from the model name.

## OpenTelemetry GenAI spans (`otel`)

An OTLP JSON export (`resourceSpans[].scopeSpans[].spans[]`, attributes as
`{key, value: {stringValue | intValue | doubleValue | boolValue | arrayValue}}`)
or one span per line, with attributes either in OTLP form or as a flat map.
Spans without GenAI attributes, and embeddings, tool and agent spans, are
skipped.

Read from each span:

| Attribute | Used as |
|---|---|
| `gen_ai.input.messages`, `gen_ai.system_instructions` | Request messages and system prompt. |
| `gen_ai.output.messages`, `gen_ai.response.finish_reasons` | Production's output and finish reason. |
| `gen_ai.tool.definitions` | Tools offered. |
| `gen_ai.request.model`, `.temperature`, `.max_tokens`, `.top_p`, `.stop_sequences`, `.seed` | Model and settings. |
| `gen_ai.provider.name` (or `gen_ai.system`) | Provider. |
| `gen_ai.usage.input_tokens`, `.output_tokens`, `.cache_read.input_tokens`, `.reasoning.output_tokens` | Usage. |
| `gen_ai.prompt.N.*`, `gen_ai.completion.N.*`, `llm.request.functions.N.*` | The older indexed form some instrumentations still write. |
| `gen_ai.request.structured_output_schema` | Structured output schema, where an instrumentation records it. |

- Content capture is opt-in in the GenAI conventions and off by default in
  most SDKs. A span without it has no request and is reported as content
  unavailable; its usage and latency are still read.
- There is no standard attribute for tool choice or for the structured output
  schema, so `tool_choice` (when tools were offered) and `response_format`
  (unless the schema attribute is present) are marked missing.
- A JSON attribute that does not parse was most likely cut at the exporter's
  attribute length limit; the call is marked truncated.
- Spans carry no cost.

## Parts

`map` and `estimate --map` group calls by **part**: the place in your app
that made the call. An ordinary `run` does not use parts.

A part name comes from one of these, and from nowhere else:

- A top-level `part` string in a Replay JSONL record or a raw-pair record.
  `record` writes it from the `X-Replay-Part` header.
- The key you name with `--part-key KEY`:

  | Format | Where `KEY` is read |
  |---|---|
  | Replay JSONL | Top-level `KEY`, then `KEY` in `meta`. Without either, `part`. |
  | Raw pairs | Top-level `KEY`. Without it, `part`. |
  | Helicone | `KEY` in `request_properties`, then in `properties`. |
  | LiteLLM | `KEY` in `metadata`. |
  | OpenTelemetry | The span or resource attribute `KEY`. With `--part-key span.name`, the span name. |
  | Langfuse | `KEY` in the observation's `metadata`. With `--part-key name`, the observation name. |

Span names and observation names are read only when you ask for them. Unless
you set them yourself they are instrumentation defaults, such as
`chat gpt-4o-mini`, and say nothing about the place in your app.

A name must be a string. Control characters are removed, spaces at either
end are trimmed, and a name longer than 64 characters is cut. A value of
another type is ignored.

A replayable call with no name is grouped by a fingerprint of its system
prompt, forced tool and response schema.
[The sensitivity map](sensitivity-map.md#parts) describes the fingerprint,
part keys and the names file.

## Not replayable

| Reason | When |
|---|---|
| `content_unavailable` | The source did not carry the request. |
| `content_truncated` | The source cut the request content. |
| `tool_history` | The conversation holds tool calls or tool results. |
| `unsupported_content` | A turn holds images, audio, files or other non-text parts. |
| `no_messages` | No text turns remain. |
| `last_turn_not_user` | The conversation ends with an assistant turn. |
| `input_too_large` | System prompt and messages exceed 64 KiB. |
| `tool_definitions_not_offered` | The request forced a tool call among several tools, or forced a tool it did not offer alone. |
| `web_search_not_replayed` | Production searched the web. A replay without search can be allowed explicitly. |
| `unparseable_request` | The request is in a shape replay cannot read. |

A request that offered tools but left the model free to answer in text is
replayed without them and graded `partial`. A map leaves such a call out
instead, and counts it in the estimate. A request that offered exactly one
tool and forced it is replayed with that tool forced, and production's output
is that call's JSON arguments.
