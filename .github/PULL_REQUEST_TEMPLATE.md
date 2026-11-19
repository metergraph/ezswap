## What this changes

<!-- What a user sees differently, and why. -->

## How it was checked

<!-- Tests added or run; for output changes, the before and after. -->

## Checklist

- [ ] `gofmt -l .` prints nothing, and `go vet ./...` and `go test -race ./...` pass.
- [ ] A change to a map rule also changes `docs/sensitivity-map.md`.
- [ ] Fixtures and examples are synthetic: no real prompts, replies, keys or customer names.
- [ ] `python3 scripts/check_publication.py origin/main` passes.
