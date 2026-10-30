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
  `run` sends to the endpoints you name. `demo`, `estimate` and `report` make
  none.
- **Keys stay in the environment.** API keys are read from environment
  variables only. They are never written to `run.json`, printed, or included
  in errors. A base URL that embeds a username or password is refused.
- **Spend is bounded.** `run` prices the run first, asks before spending, and
  reserves each call's maximum cost before sending it, so it stops at
  `--budget`.
- **Captured text cannot drive the terminal.** Prompts and outputs are cleaned
  before they are drawn: control characters, escape sequences and
  bidirectional overrides are shown as visible text, never sent to the
  terminal.
- **The Metergraph link carries only the run's setup.** The link at the end of
  the report holds model names, settings, tracked field paths and the sample
  size, in the URL fragment. It never holds trace content, keys, endpoints,
  costs or statistics. See [the Metergraph link](docs/hosted-handoff.md).
- **`run.json` holds your trace content.** It contains the sampled prompts and
  every model's output, so treat it like the trace file it came from.
- **Few dependencies, no install scripts.** The binary is statically built
  Go; its only third-party code is the Charm terminal libraries. The npm
  package is a small launcher that runs the binary from a per-platform
  package; it has no install script and downloads nothing.
