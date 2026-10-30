# Contributing

Thanks for helping. Issues and pull requests are welcome.

## Setup

Requires Go (the version in `go.mod`).

```sh
go vet ./...
go test ./...                           # add -race as CI does
go run ./cmd/metergraph-replay demo     # run the tool from source
go build ./cmd/metergraph-replay        # or build the binary
```

- `go test ./internal/report -update` rewrites the report's golden files in
  `internal/report/testdata/golden`. Review the diff before committing it.
- `go run ./cmd/build-demo` regenerates `demo/traces.jsonl` and
  `demo/run.json`, which the binary embeds.
- `go run ./cmd/build-prices <clone of metergraph>` regenerates
  `internal/pricing/prices.json` from metergraph-core's `prices.yaml` at the
  clone's last commit to that file.
- Run `gofmt -l .` (it should print nothing) and `go mod tidy` before you
  push; CI checks both.

[AGENTS.md](AGENTS.md) describes the layout and the code conventions.

## Conformance fixtures

The files in `conformance/` pin numbers and sentences that must match
Metergraph's hosted product. Do not edit their expected values. If a change
needs one to move, open an issue first, and a maintainer will regenerate it.

## Trace formats

New input formats are welcome. Each adapter in `internal/adapters/` reads one
format into the shared call shape and must say what it could not read
(`truncated`, `missing`), so a call is declined rather than replayed from a
guess. Fixtures go in `internal/adapters/testdata/` and must be synthetic:
use made-up content and `example.com`.

## Releases

Maintainers release by pushing a tag such as `v0.2.0`. The release workflow
runs GoReleaser, which publishes the GitHub release with archives for macOS,
Linux and Windows (amd64 and arm64) and updates the Homebrew cask in
`metergraph/homebrew-tap`. It then builds one npm package per platform from
the same binaries plus the `metergraph-replay` launcher package, and
publishes them. A tag with a prerelease suffix (`v0.2.0-rc.1`) publishes to
npm's `next` tag.

The workflow needs two repository secrets: `HOMEBREW_TAP_TOKEN` (contents
write access to `metergraph/homebrew-tap`) and `NPM_TOKEN` (publish rights
on `metergraph-replay` and the `metergraph-replay-<os>-<cpu>` packages).
To try a release locally:

```sh
goreleaser release --snapshot --clean --skip=publish
node scripts/npm-packages.mjs 0.0.0-snapshot dist npm-out
```

## Before you open a pull request

This repository is public. Keep secrets, real prompts or outputs, customer
names and private links out of code, fixtures, commits and pull request text.
Run the publication check before pushing:

```sh
python3 scripts/check_publication.py origin/main
```

Report security issues privately, as described in [SECURITY.md](SECURITY.md),
not in a public issue. Everyone taking part is expected to follow the
[code of conduct](CODE_OF_CONDUCT.md).

By contributing, you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE).
