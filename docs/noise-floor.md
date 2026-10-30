# The noise floor

A model asked the same question twice does not always give the same answer.
Before reading what a different model changed, it helps to know how much
production's own model changes when nothing else does. That is the noise
floor.

## What the floor is

With the noise floor on, every replayable case is sent to production's own
model twice more, at production's settings, through the same endpoint as the
treatments. The two replies are called A1 and A2. Nothing differs between
them except chance, so whatever separates A2 from A1 is the variation you
would see from production itself.

The floor is measured only on cases where both A1 and A2 came back. Every
figure it gives is over those paired cases.

## How the floor is shown

The floor sits beside each treatment's figure, in the same terms:

- a tracked field's sentence names the control's own gap, for example "Mini
  gives the same label as control on 10 of 12 cases; the control gives the
  same label as itself on 8 of 9."
- each built-in statistic shows A2 read against A1, for example "3% lower
  against itself" for latency.
- cost per call and latency are read against A1 rather than production's
  recorded call, because production's latency was timed by whatever logged
  it, and its cost reflects its own caching. A replay is timed and priced
  the same way for every column.

The figures are placed side by side and the reader compares them. The
printed report adds one mark: a `*` on a treatment's figure that is further
from production than production is from itself (beyond noise, below).

## Within-noise labels (opt-in)

With `--labels`, the explorer's tables also give each statistic and tracked
field one of these labels for each treatment:

| Label | Meaning |
|---|---|
| within noise | The treatment's gap from the control is no larger than the control's gap from itself. |
| beyond noise | The treatment's gap is larger. The label says which way it moved. |
| too few calls | Fewer than 10 paired cases, so neither gap is worth comparing. |
| not comparable | The field's values are of more than one type, or there are none. |

### Which cases are compared

A case counts when the treatment, the control and both floor replays all
have a value for the statistic or field. The treatment's gap and the floor's
gap are measured on exactly those cases, so the two are always over the same
inputs.

### What the treatment is compared with

- Cost per call and latency: the treatment against A1, and the floor is A2
  against A1.
- Everything else: the treatment against production's recorded output, and
  the floor is A2 against A1.

### How a gap is measured

| Kind | Examples | Gap |
|---|---|---|
| Numbers | cost, latency, output tokens, output length, a numeric field | The median, over the cases, of the size of the per-case difference. The direction is the sign of the median per-case difference: higher or lower. |
| Yes or no | JSON parses, empty or refused, cut off at the token limit | The share of cases where the two sides disagree. The direction says whether the treatment does it more often (higher) or less often (lower). |
| Same keys | the top-level keys of a JSON object | The share of cases where the key sets differ, over the cases where every side returned a whole JSON object. |
| Labels and text | a category, a short summary | The share of cases where the values differ, after trimming spaces at either end. The direction is "different": text has no order. |
| Lists | tags, extracted items | The mean, over the cases, of one minus the share of items the two lists have in common. |

A treatment is beyond noise only when its gap is strictly larger than the
floor's. A floor gap of zero means any disagreement at all is beyond noise.

## What a label does and does not mean

"Within noise" means that, on these cases, the treatment moved this one
statistic no more than production's model moves it by itself. It does not
mean the treatment is as good, that its outputs are correct, or that it is
safe to switch to. A treatment can be within noise on every figure shown and
still fail on something no figure measures.

"Beyond noise" means the difference is larger than chance alone produced on
these cases. It does not say whether the difference is better or worse, or
whether it matters for your product.

Both labels are read from a sample. With the default 20 cases, a floor that
happened to be quiet makes small differences look large, and one that
happened to be noisy hides them. Ten paired cases is the minimum for a label,
not a guarantee. Read the label beside the figures and the cases themselves,
never instead of them.
