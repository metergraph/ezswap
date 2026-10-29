# The Metergraph link

Replay sends no telemetry. The only way a local run reaches Metergraph is a
link the user chooses to open: the "Keep this comparison running on live
traffic" box at the end of the terminal report (printed by `run`, `demo` and
`report`). It is built by `hostedLink(run, "cli")` in `src/report/link.ts`.
Metergraph's hosted product can read the setup it carries.

## The link

```
https://www.metergraph.dev/replay?utm_source=replay&utm_medium=cli&utm_campaign=<version>#setup=<payload>
```

| Part | Value |
|---|---|
| `utm_source` | always `replay` |
| `utm_medium` | always `cli` |
| `utm_campaign` | the version of metergraph-replay that made the run, from `run.tool.version` |
| `#setup=` | the run's setup, described below |

The setup is in the fragment, never the query string. Browsers do not send a
fragment to the server, so it stays out of request logs and analytics; only
the page's own script reads it.

## The setup payload

`<payload>` is base64url (RFC 4648 section 5, no padding) of UTF-8 JSON:

```json
{
  "v": 1,
  "treatments": [
    { "model": "openai/gpt-5-mini", "settings": { "temperature": null, "maxTokens": null, "reasoningEffort": null } },
    { "model": "google/gemini-3.8-flash", "settings": { "temperature": 0, "maxTokens": 1024, "reasoningEffort": "low" } }
  ],
  "trackedFields": ["$.category", "$.priority", "$.tags"],
  "sample": 24
}
```

| Field | Type | Meaning |
|---|---|---|
| `v` | `1` | Payload version. A reader that does not know the version ignores the payload. |
| `treatments[].model` | string | The model id as the run's endpoint named it, for example `openai/gpt-5-mini`. |
| `treatments[].settings.temperature` | number or null | The treatment's temperature; null means production's. |
| `treatments[].settings.maxTokens` | number or null | The treatment's output token limit; null means production's. |
| `treatments[].settings.reasoningEffort` | string or null | The treatment's reasoning effort; null means production's. |
| `trackedFields` | string[] | JSONPath expressions of the output fields the run compared, for example `$.category`. |
| `sample` | number | How many calls the run sampled. |

Treatments keep the run's order.

## What the payload never carries

The payload is built field by field from the list above, so nothing else can
reach it. In particular it never carries:

- prompts, outputs, input previews or any other trace content
- API keys, endpoint URLs or endpoint names
- treatment labels, file names, costs, timings or statistics

## Decoding it

`decodeSetup(fragment)` in `src/report/link.ts` decodes the payload:

```ts
import { decodeSetup } from "./src/report/link.ts";

const setup = decodeSetup(location.hash); // accepts "#setup=...", "setup=..." or the bare payload
```

A browser without `Buffer` decodes it with:

```js
const b64 = location.hash.replace(/^#setup=/, "").replace(/-/g, "+").replace(/_/g, "/");
const setup = JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))));
```
