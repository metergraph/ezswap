# Contributing

Thanks for helping. Issues and pull requests are welcome.

## Setup

Requires Node.js 22.6 or later. There are no runtime dependencies, and the
source runs directly with Node's type stripping, so there is no build step
while developing.

```sh
npm ci
npm run typecheck
npm test
npm run dev -- demo            # runs src/ directly
npm run build && node bin/metergraph-replay.js demo
```

- `UPDATE_GOLDEN=1 npm test` accepts changed report output in `test/golden/`.
  Review the diff before committing it.
- `npm run build:demo` regenerates `demo/traces.jsonl` and `demo/run.json`.

[AGENTS.md](AGENTS.md) describes the layout and the code conventions: erasable
TypeScript syntax only, `.ts` import extensions, `node:test`, and no em dashes
in user-facing copy.

## Conformance fixtures

The files in `conformance/` pin numbers and sentences that must match
Metergraph's hosted product. Do not edit their expected values. If a change
needs one to move, open an issue first, and a maintainer will regenerate it.

## Trace formats

New input formats are welcome. Each adapter in `src/adapters/` reads one
format into the shared call shape and must say what it could not read
(`truncated`, `missing`), so a call is declined rather than replayed from a
guess. Fixtures must be synthetic: use made-up content and `example.com`.

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
