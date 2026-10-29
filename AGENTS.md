# Working in metergraph-replay

## Public repository rules

This repository will be public. Before publishing a branch, commit, issue,
pull request, comment, release, screenshot, fixture, or log:

- Write a self-contained public description. Keep private tracker IDs, links,
  discussion, and customer context out of branch names, commit messages,
  issue or PR text, code comments, and release notes.
- Remove secrets, tokens, non-public customer or prospect names and domains,
  tenant or workspace IDs, captured prompts or responses, private paths, and
  cloud account or resource IDs. Use synthetic examples and `example.com`.
- Fixtures and demo data are synthetic only.
- Run `python3 scripts/check_publication.py origin/main` before pushing.

## What this is

A local command-line tool: read LLM call traces from a file, replay each call
on other models through an OpenAI-compatible endpoint the user brings, and
report cost, latency and output statistics against production's recorded
output and against a noise floor (production's model replayed twice against
itself). No account, no telemetry; nothing leaves the machine except the
provider calls the user asked for.

## Layout

| Path | Owns |
|---|---|
| `src/types.ts` | Shared contracts. Every module builds against them. |
| `src/core/` | Pure statistics over a `RunFile`: per-arm summary, noise floor, tracked-field comparisons, sentences, spreads, bootstrap. No I/O. |
| `src/cases/` | Request parsing and replayability rules: `CapturedCall` to `Case`. |
| `src/adapters/` | Trace file readers: replay JSONL, raw provider logs, Langfuse export, OTel GenAI. |
| `src/providers/` | OpenAI-compatible chat client: retries, fallbacks, usage normalization. |
| `src/pricing/` | Price catalog (generated JSON) and cost function. |
| `src/run/` | Estimate, budget, replay orchestration, writing `run.json`. |
| `src/report/` | The terminal report (`terminal.ts`, primitives in `term/`), the interactive explorer (`explore/`) and the hosted link. `model.ts` holds every figure and sentence. |
| `src/cli/` | Commands: `demo`, `estimate`, `run`, `report`. |
| `conformance/` | Fixtures that pin numbers shared with the hosted Metergraph product. Never edit expected values by hand. |
| `demo/` | Synthetic traces and recorded outputs for `demo`. |

## Conventions

- TypeScript, Node 22.6+, run directly with `--experimental-strip-types`:
  only erasable syntax (no `enum`, `namespace` or parameter properties).
  Relative imports use the `.ts` extension.
- No runtime dependencies unless there is no reasonable alternative. Ask first.
- Tests use `node:test` and live in `test/`, one file per module
  (`test/core-*.test.ts`, `test/cases.test.ts`, ...). `npm test` and
  `npm run typecheck` must pass.
- User-facing copy: no em dashes. Use a full stop, comma, colon or brackets.
  A bare `—` as a missing-value marker in a table cell is fine.
- Statistics are presentation, not a verdict. Nothing says a model is good
  enough to switch to.
