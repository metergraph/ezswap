# Metergraph Replay

Would a cheaper model have worked on your real traffic?

Metergraph Replay replays your production LLM calls from a trace file on other
models, prices the run before it spends anything, and shows cost, latency and
output differences against what production returned. Every difference is read
against a **noise floor**: how much production's own model disagrees with
itself on the same calls.

Local, no account, your own keys. Under construction.

```sh
npx metergraph-replay demo
```

Licensed under Apache-2.0.
