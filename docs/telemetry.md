# Telemetry

`ezswap` can report anonymous usage to its maintainers. It is off until you
turn it on. Nothing is sent before you answer, and a build with no project
key never asks and never sends.

## Turning it on and off

The guided workflow (`ezswap` with no command, on a terminal) asks once.
"No" is the highlighted answer. Quitting at the question answers nothing:
nothing is sent, and it asks again next time.

```sh
ezswap telemetry        # say whether it is on
ezswap telemetry on
ezswap telemetry off
```

These keep it off whatever the answer was:

| Setting | Effect |
|---|---|
| `DO_NOT_TRACK=1` | Nothing is sent, and the question is not asked. |
| `EZSWAP_TELEMETRY=0` | The same. |
| `CI` set | The same. Nobody is there to have agreed. |

The answer is kept in `telemetry.json` in ezswap's settings directory
(`~/Library/Application Support/ezswap` on macOS, `~/.config/ezswap` on
Linux, `%AppData%\ezswap` on Windows). Deleting the file makes the guided
workflow ask again.

## What is sent

With telemetry on, each of `demo`, `estimate`, `map`, `run`, `record`,
`report`, `judge` and the guided workflow sends one event when it ends.
`--help`, `--version` and `telemetry` send none. This is every field:

| Field | Example | What it is |
|---|---|---|
| `command` | `map` | The command that ran. |
| `version` | `0.2.0` | ezswap's version. |
| `os`, `arch` | `darwin`, `arm64` | The operating system and processor family. |
| `exit_code` | `0` | The exit code. |
| `error` | `over_budget` | The error's code, one of a fixed set. Never its message. |
| `seconds` | `42` | Whole seconds the command took. |
| `interactive` | `true` | Whether it ran on an interactive terminal. |
| `start` | `analyze` | Guided workflow: the step it started at (`record`, `analyze` or `open`). |
| `objective` | `cost` | Guided workflow: the objective chosen (`cost`, `quality` or `latest`). |
| `models` | `["openai/gpt-6-luna", "other"]` | The models compared, by price catalog name. |
| `production` | `other` | The model the traces were recorded on, by price catalog name. |
| `parts` | `4` | How many parts of the app the map covered. |
| `cases` | `80` | How many recorded calls the plan replays. |
| `judge` | `true` | Whether the judge was on. |

A model is named only if ezswap's price catalog can price it. Anything
else, such as a private deployment or a fine-tune, is reported as `other`.

The event also carries a random install id, made when you turn telemetry
on. It is derived from nothing on your machine, and turning telemetry off
deletes it.

## What is never sent

Prompts, replies, keys, file names and paths, base URLs, part names, the
names of private models, error messages, your user name and your host name.
A test fails if an event carries a field that is not in the table above.

## Where it goes

Events are sent to [PostHog](https://posthog.com) at `us.i.posthog.com`.
Each asks PostHog to create no person profile and to work out no location
from the request's address. A command waits at most two seconds for its
event, and a failure to send is never shown and never changes an exit code.
