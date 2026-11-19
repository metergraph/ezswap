# Security policy

## Reporting a vulnerability

Please report security issues privately through
[GitHub private vulnerability reporting](https://github.com/metergraph/replay/security/advisories/new)
(the repository's **Security** tab, "Report a vulnerability"). Do not open a
public issue, and do not include real credentials, tokens, prompts or outputs
in a report.

## Supported versions

`metergraph-replay` is pre-1.0. Only the latest version receives fixes.

## Security properties

Replay reads trace files that can hold production prompts and outputs, and it
calls model endpoints with your keys. It is designed to keep these boundaries:

- **No telemetry.** Replay makes no network request except the model calls a
  `run` or `map` sends to the endpoints you name, the calls `record`
  forwards for your app to the one upstream you name, one price lookup, and
  with `map --judge` or `judge` the judge's calls. `demo` and `report` make
  none.
- **The price lookup sends model names only.** When `estimate`, `run` or
  `map` is given a `creator/model` id that the bundled price catalog cannot
  price, Replay asks Metergraph's Pricing API
  (`POST https://app.metergraph.dev/v1/pricing/lookup`) for those ids and the
  `vercel-ai-gateway` channel. Nothing from the trace file, no prompt, output
  or key, goes with it. Answers, unknown models included, are cached for a
  day in the user cache directory (`metergraph-replay/pricing-lookup.json`,
  mode 0600). `--no-price-lookup` or `METERGRAPH_REPLAY_NO_PRICE_LOOKUP=1`
  turns it off; `METERGRAPH_PRICING_URL` points it elsewhere. A failed lookup
  leaves the models unpriced and the run goes on.
- **The judge sees free-text replies, only when asked.** `map --judge` and
  `judge` send each judged case's system prompt, messages and two replies to
  the Vercel AI Gateway (`POST https://ai-gateway.vercel.sh/v1/decisions`)
  for `typesafe-ai/jev`, with zero data retention requested, using
  `AI_GATEWAY_API_KEY`. Without either, nothing is sent to a judge.
- **`record` listens on this machine only.** It binds `127.0.0.1` on one
  port (default 8787). It answers a request only when the request names
  `127.0.0.1:<port>` or `localhost:<port>` as its host and carries no
  `Origin` header, and refuses the rest with a 403, so a web page cannot
  reach it. It has no authentication of its own and holds no key: another
  program on the machine can send calls through it with a key of its own,
  and those calls are recorded too.
- **`record` forwards to one upstream.** Every request goes to the upstream
  you name (default `https://api.openai.com`), with its path, query string
  and headers as the app sent them, minus the two `X-Replay-*` headers. The
  upstream must be `https`, or `http` to a loopback host, and may not embed
  a user name. The standard proxy environment variables apply to forwarded
  calls.
- **`record` never writes or shows a header or a query string.** API keys
  travel in both. The only header values kept are `X-Replay-Part` and
  `X-Replay-Trace`, cleaned and cut to 64 characters. Error messages do not
  repeat the upstream URL.
- **`traces.jsonl` holds your prompts and responses.** `record` writes each
  captured call's request and response bodies as they were sent. It does not
  look inside a body, so a credential your app puts in a request body (a
  token for a remote tool server, say) is written to the file. The file is
  created with mode `0600` in a directory created with `0700`; an existing
  file keeps its mode. Keep it out of Git.
- **Keys stay in the environment.** API keys are read from environment
  variables only. They are never written to `run.json`, printed, or included
  in errors. A base URL that embeds a username or password is refused.
  `record` reads no key at all: your app's key passes through it in a header.
- **Spend is bounded.** `run` and `map` price the run first, ask before
  spending, and reserve each call's maximum cost before sending it, so they
  stop at `--budget`. A call billed above the catalog's price can take a
  run past the budget by its overrun; the run then reserves more for that
  model, and stops if the overrun is large or repeats. `record` spends nothing of its own; the calls it
  forwards are your app's.
- **Captured text cannot drive the terminal.** Prompts and outputs are cleaned
  before they are drawn: control characters, escape sequences and
  bidirectional overrides are shown as visible text, never sent to the
  terminal.
- **`run.json` holds your trace content.** It contains the sampled prompts and
  every model's output, so treat it like the trace file it came from.
- **The names file holds no content.** `replay-parts.json`, written beside
  the trace file by `estimate --map` and `map`, holds part keys and names
  only. A fingerprint key is a truncated hash of the system prompt, forced
  tool name and response schema. A part name is whatever you chose.
- **The printed map shows field values, not prompts.** Under WHAT CHANGED
  it prints the old and new value of a changed field, cleaned like all
  captured text. It prints no prompt and no whole reply.
- **Few dependencies, no install scripts.** The binary is statically built
  Go; its only third-party code is the Charm terminal libraries. The npm
  package is a small launcher that runs the binary from a per-platform
  package; it has no install script and downloads nothing.
