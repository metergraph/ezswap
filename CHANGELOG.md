# Changelog

All notable changes to this project are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
uses [semantic versioning](https://semver.org/). Until 1.0, a minor release
may change the command line or the `run.json` format.

## Unreleased

The first public release.

### The guided workflow

- `metergraph-replay` with no command, on an interactive terminal, walks
  through recording, planning (parts, models, cases per part and the judge,
  with a live estimate) and mapping, and ends in the map explorer.
- The map explorer: a grid of every model on every part, each cell's
  figures and changed cases, and each case's replies side by side. `o` opens
  the HTML page.
- `record` shows a live dashboard of calls per part on a terminal.

### The sensitivity map

- `map` groups an app's model calls into parts, replays a sample of each
  part on the models you name and twice on production's own model, and
  labels each part `beyond noise` only when a model changes more answers
  than a rerun of production does (a paired sign test at 5%).
- `estimate --map` lists the parts, what each is compared on and what the
  map will cost, before anything is spent.
- `--judge` compares free-text parts with a decision model
  (`typesafe-ai/jev` through the Vercel AI Gateway), asked in both orders.
  `judge <run.json>` does the same for a map already made. Off by default.
- With two or more models, the map starts with a summary of every model on
  every part, with the mean change in cost per call.
- The map says when replies were cut off at the output cap, and whether
  reasoning used it up.

### Settings

- `estimate`, `map` and `run` read `replay.yaml` from the working
  directory: the option names as keys, with flags winning. `--config FILE`
  and `--no-config` choose another file or none.

### Recording and reading traces

- `record` runs a local proxy on `127.0.0.1` that forwards an app's model
  calls to one provider and writes each call to `traces.jsonl`. Keys pass
  through and are never written. `--fresh` starts an empty file and keeps
  the previous one beside it.
- Reads Replay JSONL, raw provider request and response logs (including
  Helicone and LiteLLM), Langfuse observation exports and OpenTelemetry
  GenAI spans.

### Comparing models

- `run` replays the latest calls of a trace file on other models and
  reports cost, quality and latency against production and against
  production replayed twice (the noise floor). `report` opens an
  interactive explorer on the result.
- Any OpenAI-compatible endpoint: OpenRouter, OpenAI, Vercel AI Gateway,
  LiteLLM, Ollama, or a base URL.
- `--html FILE` writes any result as one self-contained page that makes no
  network request.

### Spend and pricing

- Every run is priced before it starts, asks before spending, reserves each
  call's maximum cost at the full rate and stops at `--budget`. A model
  billed above its catalog price gets larger reservations for the rest of
  the run instead of stopping it.
- A bundled price catalog covers several hundred models. Models it lacks are
  priced through Metergraph's free Pricing API, which receives model names
  only. `--no-price-lookup` turns it off.

### Distribution

- Prebuilt binaries for macOS, Linux and Windows (amd64 and arm64), a
  Homebrew cask, npm packages, and `go install`.
