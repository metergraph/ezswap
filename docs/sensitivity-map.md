# The sensitivity map

Most apps call a model from more than one place: a classifier here, an
extractor there, a reply writer somewhere else. When you change the model,
some of those places change their answers and some do not. The map shows
which.

```sh
metergraph-replay map traces.jsonl --models openai/gpt-5-mini
```

For each part of the app, `map` replays a sample of recorded calls on the
new model and on production's own model, then counts how many answers
changed under each. A part is flagged only when the new model changes more
answers than a plain rerun of the old one does.

The map shows where outputs change. It does not say whether a change is
better or worse.

## Try it

```sh
metergraph-replay demo --map
```

This prints the map of a bundled synthetic support desk with five parts. It
needs no keys, makes no network request and writes no file. The output, with
`COLUMNS=80` and stdout not a terminal (so plain ASCII):

```text
A map of a synthetic support desk with five parts, recorded so it needs no keys and no network.
Make one from your own traces:
  metergraph-replay map traces.jsonl --models openai/gpt-5-mini

5 parts | map-traces.jsonl | 2026-10-20 to 2026-10-27 | 20 cases per part
spent $0.016 of $5.00

treatment A openai/gpt-5.4-nano

PART             LABEL           CHANGED   RERUN     COST/CALL  LATENCY
extract-order    beyond noise    9 of 20   1 of 20   -35%       -52%
route-to-team    within noise    4 of 20   3 of 20   -37%       -41%
classify-ticket  within noise    2 of 20   3 of 20   -36%       -33%
summarise-thread too few calls   6 paired, needs 10  -28%       -41%
draft-reply      not comparable  free text           -29%       -45%

WHAT CHANGED
extract-order
  sku       changed 4, rerun 1
  quantity  changed 6, rerun 0
  order_id  changed 1, rerun 0
  call-0003  quantity  5 -> 6
  call-0004  sku       "B-204" -> "B-204B"
  call-0006  quantity  2 -> 3
  call-0008  sku       "B-19" -> "B-19B"
  call-0010  quantity  4 -> 5
  call-0013  quantity  1 -> 2
  call-0015  sku       "C-77" -> "C-77B"
  call-0017  quantity  3 -> 4
  and 1 more
route-to-team
  team    changed 4, rerun 3
  call-0022  team    "support" -> "sales"
  call-0027  team    "support" -> "sales"
  call-0031  team    "billing" -> "support"
  call-0036  team    "billing" -> "support"

CHANGED: cases where the treatment's answer differs from the recorded one.
RERUN: the same for a fresh run of production's own model. A part is beyond
noise when CHANGED exceeds RERUN by a paired sign test at 5%.
This shows where outputs change. It does not say a change is better or worse.
```

[Reading the map](#reading-the-map) walks through it.

## Parts

A **part** is a place in your app that calls a model. Replay names a part
only when you named it. There are three ways.

1. **A `part` field in the trace file.** A top-level `part` string in a
   Replay JSONL record or a raw-pair record.
2. **The `X-Replay-Part` header when recording.** `metergraph-replay record`
   writes the header's value as the `part` of each call. See
   [Recording](record.md).
3. **`--part-key KEY`**, when your records already carry a name under
   another key:

   | Format | Where `KEY` is read |
   |---|---|
   | Replay JSONL | Top-level `KEY`, then `KEY` in `meta`. |
   | Raw pairs | Top-level `KEY`. |
   | Helicone | `KEY` in `request_properties`, then in `properties`. |
   | LiteLLM | `KEY` in `metadata`. |
   | OpenTelemetry | The span or resource attribute `KEY`. With `--part-key span.name`, the span name. |
   | Langfuse | `KEY` in the observation's `metadata`. With `--part-key name`, the observation name. |

   For Replay JSONL and raw pairs, a record without `KEY` falls back to its
   `part` field. Helicone, LiteLLM, OpenTelemetry and Langfuse records are
   named only through `--part-key`.

A name must be a string. Control characters are removed, spaces at either
end are trimmed, and a name longer than 64 characters is cut.

### Calls with no name

A replayable call with no name is grouped by a **fingerprint**: 12
hexadecimal characters of a SHA-256 hash over three things that stay fixed
across calls from one place.

- The system prompt, with UUIDs and runs of digits masked, so a date or an id
  in the prompt does not split a part.
- The name of the forced tool, when the call forced one.
- The response schema.

The model is not in the fingerprint, so a part that is partway through a
model change stays one part.

Every part has a **key**. A named part has the key `name:<name>`. A
fingerprinted part has the key `fp1:<hash>` and the default name
`part-<first 6 characters of the hash>`. A default name never holds prompt
text.

### When fingerprints do not work

A system prompt that holds per-request text (a customer's name, a pasted
document) gives almost every call its own fingerprint. `map` and
`estimate --map` refuse such a file instead of printing a map of one-call
parts. The rule: among the parts that would run, there are at least four
fingerprint groups and more than half of them hold a single call. The
command exits with code 2 (error code `parts_not_found` with `--json`) and
asks you to name the parts in one of the three ways above.

### The names file

`estimate --map` and `map` keep part names in `replay-parts.json`, in the
same directory as the trace file:

```json
{
  "schema": "metergraph.replay-parts/v1",
  "parts": {
    "fp1:178780517574": "part-178780",
    "fp1:ecc195d1a7b4": "part-ecc195",
    "name:classify-ticket": "classify-ticket",
    "name:extract-order": "extract-order",
    "name:route-to-team": "route-to-team"
  }
}
```

- Both commands read it, then add any key it does not have yet, with the
  default name. They never change or remove an entry.
- **Rename** a part by editing its name.
- **Merge** two parts by giving two keys the same name.
- The file holds keys and names only. No prompt, no output.
- An entry for a key the trace file does not have is ignored, with a note.
  An empty name is ignored too.
- A file that is not valid JSON, or has another `schema`, is a usage error
  (exit 2). Fix it or delete it.
- If the directory cannot be written, the command carries on without the
  file.

Two options change names for one command and save nothing:

- `--name KEY=NAME` renames a part. `KEY` is a part's key or its current
  name. Repeat the option for more parts. Two parts given one name merge.
- `--only NAME` keeps some parts. It takes a name or a key, repeated or
  comma-separated. A name that matches no part is a usage error that lists
  the parts.

`--name` applies first, so `--only` must use the new name (or the key).

## List the parts before spending

```sh
metergraph-replay estimate traces.jsonl --map --models openai/gpt-5-mini
```

`estimate --map` lists the parts and prices the map. It makes no provider
call and needs no key. It does write the names file. This is the bundled
demo trace file, `demo/map-traces.jsonl`:

```text
Read 86 calls in 86 traces from map-traces.jsonl (replay-jsonl format).

5 parts | map-traces.jsonl | 1 treatment | 20 cases per part

PART             FROM        CASES  COMPARED                 EXPECTED
classify-ticket  name           20  2 fields                    $0.02
extract-order    name           20  3 fields                    $0.02
part-ecc195      fingerprint    20  no: free text               $0.02
route-to-team    name           20  2 fields                    $0.02
part-178780      fingerprint     6  no: under 10 cases        $0.0057

258 calls in all: each case runs on 1 model, plus twice on production's model.
Expected $0.08, at most $0.14. Budget $5.00.
Names file: replay-parts.json (updated). Edit a name there to rename a part; give two parts one name to merge them.
Note: OPENROUTER_API_KEY is not set; map will need it.
```

| Column | Meaning |
|---|---|
| `PART` | The part's name. |
| `FROM` | How it was named: `name`, `fingerprint`, or `mixed` for a merge of both. |
| `CASES` | Cases that will be replayed. |
| `COMPARED` | How many fields the part will be compared on, or why it will get no label. |
| `EXPECTED` | Expected spend for this part. |

Two parts of the demo file have no name, so they show as `part-ecc195` and
`part-178780`. The demo map above was made after renaming them to
`draft-reply` and `summarise-thread`.

## What a map run does

### Which calls are sampled

`--per-part N` (default 20, from 10 to 200) takes the latest N replayable
cases of each part, one per trace id. Calls are ordered by time when every
call has a time, and by their order in the file otherwise. `map` does not
take `--sample`.

Two kinds of call are left out before sampling. The estimate counts them and
gives the reason.

- Calls a replay cannot reproduce. The reasons are listed under "Not
  replayable" in [trace formats](trace-format.md#not-replayable).
- Calls where production offered tools that a replay would not offer. An
  ordinary `run` replays these without the tools and grades them `partial`.
  A map leaves them out, because a missing tool changes answers for a reason
  that has nothing to do with the model.

### What is sent

Every sampled case is replayed once on each treatment (each model in
`--models`), and twice on production's own model at production's settings.
The two reruns of production's model are the noise floor. With one treatment
and 20 cases a part costs 60 calls.

`--no-noise-floor` skips the two reruns. Without them no part can be
labelled: every part reads `not comparable` with the reason `no noise floor`.

### Which fields are compared

Each part is compared on up to 10 JSON fields. They are chosen from
production's recorded outputs alone, so the choice is known before any
spending and does not depend on what a treatment returns.

1. `--track PATH` fields come first.
2. Then the fields Replay suggests: labels, numbers, lists, and short text
   of at most 120 characters. A field qualifies when it has a value in at
   least half of the part's recorded outputs.
3. `--ignore PATH` removes a field, such as a generated id or a timestamp
   that differs on every call: `--ignore '$.request_id'`. Repeat it or
   separate paths with commas.

A part with no field to compare is `not comparable`:

- `free text`: fewer than half of its recorded outputs parse as JSON.
- `no comparable field`: the outputs are JSON, and no field qualifies.

### How two answers are compared

Each case is compared at the compared fields where its recorded answer has a
value. At one field:

- Types must match. The text `"5"` is not the number `5`.
- Text is compared after trimming spaces at either end.
- Numbers must be exactly equal.
- Lists are compared in order, item by item.
- A JSON `null` reads as no value.
- A value on one side only is a difference. No value on both sides is the
  same.

A reply that is not JSON has no value at any field, so it counts as changed.
So does a refusal, and a reply cut off at the token limit.

A case whose recorded answer has no value at any compared field is left out,
with a note.

### The counts

The counts are over **paired cases**: cases where both the treatment and
production's first rerun answered.

| Count | Meaning |
|---|---|
| `CHANGED` | Cases where the treatment's answer differs from the recorded answer at any compared field. |
| `RERUN` | Cases where a fresh run of production's own model differs from the recorded answer. |
| `itself` | Cases where production's two reruns differ from each other. In `--json` only. |

### The label

Each part gets one label for each treatment. The first rule that applies
wins.

| Label | When |
|---|---|
| `not comparable` | No field to compare (`free text`, `no comparable field`), or the run had no noise floor. |
| `too few calls` | Fewer than 10 paired cases. |
| `beyond noise` | The paired test below passes. |
| `within noise` | Anything else. |

The test is a one-sided paired sign test at the 5% level. It looks only at
the cases where the two counts disagree:

- `b`: cases that changed under the treatment and not under the rerun.
- `c`: cases that changed under the rerun and not under the treatment.

If the treatment behaved like production, a case would be as likely to land
in `b` as in `c`. The test passes when `b` is larger than `c` and the chance
of `b` or more out of `b + c` at even odds is at most 5%.

| `b` against `c` | Result |
|---|---|
| 5 against 0, 7 against 1, 9 against 2, 10 against 3 | passes: `beyond noise` |
| 4 against 0, 6 against 1, 4 against 3 | fails: `within noise` |

Both counts are measured against the same recording. An old recording
raises `CHANGED` and `RERUN` together, so it is not blamed on the treatment.

This rule is not the one behind the "within noise" and "beyond noise" labels
of the ordinary report (`run`, `--labels`). Those compare one statistic or
one field at a time: a treatment is beyond noise when its gap from
production is strictly larger than production's gap from itself, with no
statistical test. See [the noise floor](noise-floor.md). The map labels a
whole part, counts a case as changed when any compared field changed, and
applies the paired test.

### Cost and latency

`COST/CALL` is the treatment's mean cost per call and `LATENCY` its median
latency, each as a change from production's first rerun, over the cases
where both answered. `-35%` means the treatment cost 35% less per call. A
figure that cannot be worked out prints as `—` (`-` in ASCII).

### Notes

A treatment's block ends with notes when they apply:

- **A noisy baseline.** `RERUN` is above half the paired cases. The note
  names the field that differs most often on a rerun and suggests
  `--ignore` for it. A treatment can hardly exceed a baseline that high.
- **A stale recording.** Production's own model no longer reproduces its
  recording, while its two reruns agree with each other. The recording may
  be old, or a setting was not replayed. This uses the same paired test, on
  `RERUN` against `itself`.
- **One field stands out.** In a `within noise` part, a single field passes
  the test alone while other fields raise the part's baseline.
- **Left-out cases.** How many cases had no value at any compared field.
- **Failed calls.** How many treatment calls failed.

## Reading the map

Take the demo map above. It has one treatment, `openai/gpt-5.4-nano`, and
five parts. Rows are ordered by label (`beyond noise` first), then by
`CHANGED`.

- **`extract-order` is beyond noise.** The treatment changed 9 of 20
  answers. A rerun of production's model changed 1. Nine cases changed under
  the treatment alone and one under the rerun alone, and 9 against 1 passes
  the test. This is the part to look at.
- **`route-to-team` is within noise.** 4 of 20 changed, but a rerun changed
  3 of 20. Production's model is that unsteady here by itself. 4 against 3
  fails the test.
- **`classify-ticket` is within noise.** The treatment changed fewer answers
  (2) than the rerun did (3).
- **`summarise-thread` has too few calls.** The file holds 6 cases for it,
  and a label needs 10 paired cases.
- **`draft-reply` is not comparable.** Its outputs are free text, so there
  is no field to compare.

Cost and latency are shown for every part, labelled or not.

`WHAT CHANGED` lists a part when it is beyond noise, or when it is within
noise and `CHANGED` is above `RERUN`. Each block gives the counts for each
field, then up to eight changed cases: the case id, the first field that
changed, and the recorded value and the new one. For `extract-order`,
`quantity` changed in 6 cases and never on a rerun, and `sku` changed in 4
against 1. No prompt and no whole reply is printed.

What the map does not tell you: whether `quantity` going from 5 to 6 is a
fix or a regression. Open the cases to find out.

## Spend

`--budget` defaults to $5.00 for `map` (`run` defaults to $1.00). The spend
rules are those of `run`, described under "Spend" in
[the command line reference](cli.md#spend).

- `map` prints the estimate, then asks before spending. `--yes` skips the
  question, and is required without a terminal.
- When the expected spend is over the budget, `map` refuses before any
  provider call (exit 4). Raise `--budget`, lower `--per-part`, or keep
  fewer parts with `--only`.
- Cases run part by part, largest part first. If the budget is reached
  partway, the run stops (exit 6) and parts are labelled from the cases that
  finished. The parts that did not finish usually read `too few calls`.

## Printing a map again

`map` writes `run.json` to `--out` (default `./replay-out`), the same file
`run` writes. Pass `--out` to keep a map and an ordinary run side by side.

```sh
metergraph-replay report replay-out/run.json
metergraph-replay report replay-out/run.json --only extract-order
metergraph-replay report replay-out/run.json --name part-ecc195=draft-reply
```

- `report` on a run that `map` made prints the map. It never opens the
  explorer.
- `--only` and `--name` work on `report`. The names in `run.json` are the
  ones in effect when the map was made, so a later edit to the names file
  does not reach an old run. Use `--name`.
- `report --map` on an ordinary run is a usage error (exit 2).
- `--per-part`, `--part-key` and `--ignore` are set when the map is made.
  `report` refuses them.

### JSON

With `--json`, `map`, `report` and `demo --map` add a `parts` array. Each
part has `keys`, `name`, `source` (`name`, `fingerprint` or `mixed`),
`sampled`, `leftOut`, `paths` and one entry in `treatments` for each
treatment:

| Key | Meaning |
|---|---|
| `label`, `model` | The treatment's letter and model. |
| `paired`, `failed` | Paired cases, and treatment calls that failed. |
| `changed`, `rerun`, `itself` | The three counts. |
| `onlyTreatment`, `onlyRerun` | `b` and `c` of the paired test. |
| `result` | `within_noise`, `beyond_noise`, `too_few_calls` or `not_comparable`. |
| `reason` | Why, for `too_few_calls` and `not_comparable`. Otherwise `null`. |
| `costPerCallChange`, `latencyChange` | Relative changes (`-0.35` is 35% lower), or `null`. |
| `pathCounts` | `changed` and `rerun` for each compared field. |
| `changes` | Each changed case: `caseId`, `path`, `from`, `to`. |
| `notes` | The notes, as sentences. |

`estimate --map --json` has its own `parts` array (`keys`, `name`, `source`,
`available`, `sampled`, `paths`, `compared`, `reason`, `expectedUsd`), plus
`leftOut` and `namesFile`.

## Limits

- **The 5% level is a choice, and it applies to each part separately.** With
  12 parts and a treatment identical to production, at least one part reads
  `beyond noise` in up to 46 maps out of 100.
- **Small real changes read `within noise`.** At 20 cases, a part whose
  rerun never differs needs 5 changed cases to pass. A model that changes 2
  answers in 20 looks the same as chance.
- **It shows where outputs change, not whether a change is better or
  worse.** A treatment that fixes production's mistakes also reads
  `beyond noise`.
- **Response schemas are read in OpenAI's shape only** (`response_format`,
  or the Responses API `text.format`). A part recorded from another provider
  with a schema is replayed without the schema. That raises `CHANGED` and
  `RERUN` together.
- **Free-text parts are not compared.** The map compares JSON fields.
- **Calls that offered tools are left out**, unless the call forced its one
  tool. A part made mostly of such calls can end up with too few cases.
- **A label is read from a sample.** Ten paired cases is the minimum for a
  label, not a guarantee. Read the label beside the counts and the cases.
