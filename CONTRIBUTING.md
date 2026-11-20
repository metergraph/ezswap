# Contributing

Thanks for helping. Issues and pull requests are welcome.

## Setup

Requires Go (the version in `go.mod`).

```sh
go vet ./...
go test ./...                           # add -race as CI does
go run ./cmd/ezswap demo     # run the tool from source
go build ./cmd/ezswap        # or build the binary
```

- `go test ./internal/report -update` rewrites the report's golden files in
  `internal/report/testdata/golden`. Review the diff before committing it.
- `go run ./cmd/build-demo` regenerates the four files the binary embeds:
  `demo/traces.jsonl` and `demo/run.json` for `demo`, and
  `demo/map-traces.jsonl` and `demo/map-run.json` for `demo --map`. Run it
  after a change to the map's rules, so the bundled map still matches them.
- `estimate --map` and `map` write `ezswap-parts.json` beside the trace
  file. To try them on the demo traces, copy `demo/map-traces.jsonl` to a
  temporary directory first, and do not commit a `ezswap-parts.json`.
- `go run ./examples/support-desk --base-url http://127.0.0.1:8787/v1 --model MODEL`
  is a sample app for trying `record` and `map` end to end. Start
  `ezswap record` first.
- `go run ./cmd/build-prices <clone of metergraph>` regenerates
  `internal/pricing/prices.json` from metergraph-core's `prices.yaml` at the
  clone's last commit to that file.
  Then run
  `uv run --no-project --with pyyaml python scripts/build_pricing_conformance.py <same clone>`
  to recompute `conformance/pricing.json` with metergraph-core's own catalog
  code. Run it with `--check` on a clone at the previous catalog commit first:
  it should report 0 differences. Refresh both before each release.
- Run `gofmt -l .` (it should print nothing) and `go mod tidy` before you
  push; CI checks both.

[AGENTS.md](AGENTS.md) describes the layout and the code conventions.

## Conformance fixtures

The files in `conformance/` pin numbers and sentences that must match
Metergraph's hosted product. Do not edit their expected values. If a change
needs one to move, open an issue first, and a maintainer will regenerate it.

## The map and the recorder

- `internal/parts/` owns parts: the fingerprint, part keys, the names file
  and the choice of compared fields. The fingerprint's recipe is versioned
  in the key prefix (`fp1:`). Change the prefix when the recipe changes, or
  existing names files will name the wrong parts.
- The map's counts, labels and notes are in `internal/report/map.go` and the
  paired test in `internal/report/paired.go`. A change to any rule must also
  change [docs/sensitivity-map.md](docs/sensitivity-map.md).
- `internal/record/` is the recording proxy. It must never write or print a
  request header (other than the two cleaned `X-Ezswap-*` values) or a query
  string: providers carry API keys in both. Tests there use a local fake
  upstream and synthetic bodies.

## Trace formats

New input formats are welcome. Each adapter in `internal/adapters/` reads one
format into the shared call shape and must say what it could not read
(`truncated`, `missing`), so a call is declined rather than replayed from a
guess. An adapter reads a part name only from a `part` field or from the key
the developer names with `--part-key`, never from a default such as a span
name. Fixtures go in `internal/adapters/testdata/` and must be synthetic:
use made-up content and `example.com`.

## Releases

Maintainers release by pushing a tag such as `v0.2.0`. The release workflow
runs GoReleaser, which publishes the GitHub release with archives for macOS,
Linux and Windows (amd64 and arm64) and updates the Homebrew cask in
`metergraph/homebrew-tap`. It then builds one npm package per platform from
the same binaries plus the `ezswap` launcher package, and
publishes them. A tag with a prerelease suffix (`v0.2.0-rc.1`) publishes to
npm's `next` tag.

The workflow needs two repository secrets: `HOMEBREW_TAP_TOKEN` (contents
write access to `metergraph/homebrew-tap`) and `NPM_TOKEN` (publish rights
on `ezswap` and the `ezswap-<os>-<cpu>` packages).
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
