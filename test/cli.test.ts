import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { EXIT, HELP, main, type CliIo } from "../src/cli/main.ts";
import type { FetchLike } from "../src/providers/index.ts";
import { chatReply, tempDir, writeTraces } from "./run-helpers.ts";

const KEY = "sk-or-test-not-a-real-key-4242";
const OTHER_KEY = "sk-openai-test-not-a-real-key-7777";
const NOW = () => new Date("2026-10-28T12:00:00.000Z");

interface Captured {
  code: number;
  stdout: string;
  stderr: string;
  fetches: number;
  bodies: string[];
}

async function cli(argv: string[], io: Partial<CliIo> = {}, reply: () => Response = () => okResponse()): Promise<Captured> {
  let stdout = "";
  let stderr = "";
  let fetches = 0;
  const bodies: string[] = [];
  const fetch: FetchLike = async (_url, init) => {
    fetches++;
    bodies.push(String(init.body));
    return reply();
  };
  const code = await main(argv, {
    stdout: { write: (text: string) => (stdout += text) },
    stderr: { write: (text: string) => (stderr += text) },
    stdinIsTTY: false,
    env: { OPENROUTER_API_KEY: KEY },
    fetch,
    now: NOW,
    signal: new AbortController().signal,
    ...io,
  });
  return { code, stdout, stderr, fetches, bodies };
}

function okResponse(cost?: number): Response {
  const body = chatReply('{"category":"bug"}', cost === undefined ? {} : { cost });
  return new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
}

const MODELS = ["--models", "google/gemini-2.5-flash-lite"];

test("--help and --version exit 0; a bad option is a usage error", async () => {
  const help = await cli(["--help"]);
  assert.equal(help.code, EXIT.ok);
  assert.equal(help.stdout, HELP);
  for (const code of ["0", "1", "2", "3", "4", "5", "6"]) assert.match(HELP, new RegExp(`^  ${code}  `, "m"));
  assert.doesNotMatch(HELP, /—/);
  const version = await cli(["--version"]);
  assert.equal(version.code, EXIT.ok);
  assert.match(version.stdout, /^\d+\.\d+\.\d+\n$/);
  const bad = await cli(["run", "x.jsonl", "--frobnicate"]);
  assert.equal(bad.code, EXIT.usage);
  const none = await cli([]);
  assert.equal(none.code, EXIT.usage);
  const unknown = await cli(["fly"]);
  assert.equal(unknown.code, EXIT.usage);
});

test("estimate explains the run and makes no provider call", async () => {
  const file = writeTraces(tempDir(), 4, (n) => (n === 4 ? { truncated: true } : {}));
  const out = await cli(["estimate", file, ...MODELS]);
  assert.equal(out.code, EXIT.ok);
  assert.equal(out.fetches, 0);
  assert.match(out.stdout, /3 can be replayed, 1 cannot/);
  assert.match(out.stdout, /1 because the captured content was cut short/);
  assert.match(out.stdout, /Expected spend: about \$/);
  assert.match(out.stdout, /Most it can spend: \$/);
  assert.doesNotMatch(out.stdout, /—/);
});

test("estimate --json prints exactly one JSON object", async () => {
  const file = writeTraces(tempDir(), 3);
  const out = await cli(["estimate", file, ...MODELS, "--json", "--endpoint", "openai"], { env: {} });
  assert.equal(out.code, EXIT.ok);
  const lines = out.stdout.trim().split("\n");
  assert.equal(lines.length, 1);
  const json = JSON.parse(lines[0]!);
  assert.equal(json.ok, true);
  assert.equal(json.command, "estimate");
  assert.equal(json.estimate.replayable, 3);
  assert.deepEqual(json.notes, ["OPENAI_API_KEY is not set; run will need it."]);
  assert.equal(out.stderr, "");
});

test("no replayable cases exits 3", async () => {
  const file = writeTraces(tempDir(), 2, () => ({ truncated: true }));
  assert.equal((await cli(["estimate", file, ...MODELS])).code, EXIT.noReplayableCases);
  const run = await cli(["run", file, ...MODELS, "--yes", "--json"]);
  assert.equal(run.code, EXIT.noReplayableCases);
  assert.equal(JSON.parse(run.stdout).error.code, "no_replayable_cases");
  assert.equal(run.fetches, 0);
});

test("run refuses without a terminal and without --yes, and sends nothing", async () => {
  const file = writeTraces(tempDir(), 2);
  const out = await cli(["run", file, ...MODELS, "--out", tempDir()]);
  assert.equal(out.code, EXIT.refused);
  assert.equal(out.fetches, 0);
  assert.match(out.stderr, /--yes/);
});

test("run asks on a terminal, and a no sends nothing", async () => {
  const file = writeTraces(tempDir(), 2);
  let asked = "";
  const out = await cli(["run", file, ...MODELS, "--out", tempDir()], {
    stdinIsTTY: true,
    confirm: async (question) => {
      asked = question;
      return false;
    },
  });
  assert.match(asked, /^Spend up to \$/);
  assert.equal(out.code, EXIT.refused);
  assert.equal(out.fetches, 0);
});

test("run refuses an expected spend over the budget, and unpriced models without --allow-unpriced", async () => {
  const file = writeTraces(tempDir(), 3);
  const over = await cli(["run", file, ...MODELS, "--budget", "0", "--yes", "--json"]);
  assert.equal(over.code, EXIT.refused);
  assert.equal(JSON.parse(over.stdout).error.code, "over_budget");
  const unpriced = await cli(["run", file, "--models", "example/unknown-model", "--yes", "--json"]);
  assert.equal(unpriced.code, EXIT.refused);
  assert.equal(JSON.parse(unpriced.stdout).error.code, "unpriced_models");
  assert.equal(over.fetches + unpriced.fetches, 0);
});

test("a missing key exits 5, names the variable, and never prints a key", async () => {
  const file = writeTraces(tempDir(), 2);
  const out = await cli(
    ["run", file, "--models", "openai:gpt-5-mini,google/gemini-2.5-flash-lite", "--yes"],
    { env: { OPENAI_API_KEY: OTHER_KEY } },
  );
  assert.equal(out.code, EXIT.providerConfig);
  assert.match(out.stderr, /OPENROUTER_API_KEY is not set/);
  assert.equal(out.fetches, 0);
  for (const text of [out.stdout, out.stderr]) {
    assert.ok(!text.includes(OTHER_KEY));
    assert.ok(!text.includes(KEY));
  }
  const json = await cli(["run", file, ...MODELS, "--yes", "--json"], { env: {} });
  assert.equal(json.code, EXIT.providerConfig);
  assert.equal(JSON.parse(json.stdout).error.code, "credential_missing");
});

test("run --yes replays, writes run.json without the key, and prints the report with the hosted link", async () => {
  const file = writeTraces(tempDir(), 2);
  const out = tempDir();
  const result = await cli(["run", file, ...MODELS, "--yes", "--out", out, "--track", "$.category"], {}, () => okResponse(0.00002));
  assert.equal(result.code, EXIT.ok, result.stderr);
  assert.equal(result.fetches, 6);
  const runText = readFileSync(join(out, "run.json"), "utf8");
  assert.ok(!runText.includes(KEY));
  assert.ok(!result.stdout.includes(KEY));
  const run = JSON.parse(runText);
  assert.equal(run.replays.length, 6);
  assert.ok(run.replays.every((r: { costSource: string }) => r.costSource === "endpoint"));
  assert.deepEqual(run.settings.trackedFields, ["$.category"]);
  assert.ok(!existsSync(join(out, "report.html")), "no HTML report");
  assert.match(result.stdout, /Keep this comparison running on live traffic/);
  assert.match(result.stdout, /^ https:\/\/www\.metergraph\.dev\/replay\?\S*utm_medium=cli/m);
  assert.match(result.stdout, /Explore every call:\s+metergraph-replay report \S*run\.json\s*$/);
  assert.match(result.stdout, /^[\x20-\x7e\n]*$/, "ASCII when stdout is not a terminal");
  // Production's settings travel with the replay.
  assert.equal(JSON.parse(result.bodies[0]!).temperature, 0.2);
});

test("run --temperature and --max-tokens apply to every treatment", async () => {
  const file = writeTraces(tempDir(), 1);
  const out = tempDir();
  const result = await cli(
    ["run", file, ...MODELS, "--temperature", "0.7", "--max-tokens", "64", "--no-noise-floor", "--yes", "--out", out, "--json"],
  );
  assert.equal(result.code, EXIT.ok);
  const body = JSON.parse(result.bodies[0]!);
  assert.equal(body.temperature, 0.7);
  assert.equal(body.max_tokens, 64);
  assert.equal(result.fetches, 1);
  const json = JSON.parse(result.stdout);
  assert.deepEqual(json.replays, { ok: 1, failed: 0, skipped: 0 });
});

test("a run stopped early by the budget exits 6", async () => {
  const file = writeTraces(tempDir(), 5);
  const out = tempDir();
  // The endpoint reports more than any reservation, so the first settled call stops the run.
  const result = await cli(
    ["run", file, ...MODELS, "--yes", "--out", out, "--json", "--concurrency", "1"],
    {},
    () => okResponse(0.5),
  );
  assert.equal(result.code, EXIT.stoppedEarly);
  const json = JSON.parse(result.stdout);
  assert.equal(json.stopped, "reservation_exceeded");
  assert.equal(result.fetches, 1);
  assert.ok(existsSync(join(out, "run.json")));
});

test("an interrupted run writes what exists and exits 6", async () => {
  const file = writeTraces(tempDir(), 3);
  const out = tempDir();
  const stop = new AbortController();
  const result = await cli(
    ["run", file, ...MODELS, "--yes", "--out", out, "--json", "--concurrency", "1"],
    { signal: stop.signal },
    () => {
      stop.abort();
      return okResponse();
    },
  );
  assert.equal(result.code, EXIT.stoppedEarly);
  assert.equal(JSON.parse(result.stdout).stopped, "interrupted");
  const run = JSON.parse(readFileSync(join(out, "run.json"), "utf8"));
  assert.equal(run.replays.filter((r: { status: string }) => r.status === "ok").length, 1);
});

test("demo needs no key and no network, and report draws a run in the terminal", async () => {
  const out = tempDir();
  const demo = await cli(["demo", "--out", out, "--json"], { env: {} });
  assert.equal(demo.code, EXIT.ok);
  assert.equal(demo.fetches, 0);
  const json = JSON.parse(demo.stdout);
  assert.ok(existsSync(json.runFile));
  assert.ok(!existsSync(join(out, "report.html")));
  const run = JSON.parse(readFileSync(json.runFile, "utf8"));
  assert.equal(run.schema, "metergraph.replay-run/v1");
  assert.ok(run.replays.length > 0);

  const text = await cli(["demo", "--out", out], { env: {} });
  assert.match(text.stdout, /metergraph replay/);
  assert.match(text.stdout, /Explore every call:\s+metergraph-replay report \S*run\.json/);
  assert.doesNotMatch(text.stdout, /\x1b/, "no colour without a terminal");

  const report = await cli(["report", json.runFile]);
  assert.equal(report.code, EXIT.ok);
  assert.match(report.stdout, /\+- Cost -/, "not interactive: the static report, in ASCII");
  const missing = await cli(["report", join(out, "nope.json")]);
  assert.equal(missing.code, EXIT.usage);
  const withOut = await cli(["report", json.runFile, "--out", out]);
  assert.equal(withOut.code, EXIT.usage);
});

test("report opens the explorer on an interactive terminal, and --no-interactive prints instead", async () => {
  const out = tempDir();
  const demo = JSON.parse((await cli(["demo", "--out", out, "--json"], { env: {} })).stdout);
  const opened: { name: string; color: boolean; ascii: boolean }[] = [];
  const tty = {
    stdout: { write: () => true, isTTY: true, columns: 100 },
    stdinIsTTY: true,
    env: {},
    explore: async (_run: unknown, options: { name: string; color: boolean; ascii: boolean }) => {
      opened.push(options);
      return "quit" as const;
    },
  };
  assert.equal((await cli(["report", demo.runFile], tty)).code, EXIT.ok);
  assert.equal(opened.length, 1);
  assert.match(opened[0]!.name, /run\.json$/);
  assert.equal(opened[0]!.color, true);
  assert.equal(opened[0]!.ascii, false);
  assert.equal((await cli(["report", demo.runFile, "--no-color", "--ascii"], tty)).code, EXIT.ok);
  assert.deepEqual([opened[1]!.color, opened[1]!.ascii], [false, true]);
  assert.equal((await cli(["report", demo.runFile], { ...tty, env: { NO_COLOR: "1" } })).code, EXIT.ok);
  assert.equal(opened[2]!.color, false, "NO_COLOR turns colour off");

  let printed = "";
  const noTty = { ...tty, stdout: { write: (t: string) => ((printed += t), true), isTTY: true, columns: 100 } };
  assert.equal((await cli(["report", demo.runFile, "--no-interactive"], noTty)).code, EXIT.ok);
  assert.equal(opened.length, 3, "--no-interactive never opens the explorer");
  assert.match(printed.replace(/\x1b\[[0-9;]*m/g, ""), /╭─ Cost/);
  assert.match(printed, /\x1b\[/, "colour on a terminal");

  assert.equal((await cli(["demo", "--out", out, "--explore"], tty)).code, EXIT.ok);
  assert.equal(opened.length, 4, "demo --explore opens the explorer");
  const ended = await cli(["report", demo.runFile], { ...tty, explore: async () => "SIGTERM" as const });
  assert.equal(ended.code, 143);
});

test("a missing trace file is a usage error", async () => {
  const out = await cli(["estimate", join(tempDir(), "missing.jsonl"), ...MODELS, "--json"]);
  assert.equal(out.code, EXIT.usage);
  assert.equal(JSON.parse(out.stdout).error.code, "file_not_found");
});

test("--monthly-calls must be a whole number", async () => {
  const { main } = await import("../src/cli/main.ts");
  const out: string[] = [];
  const err: string[] = [];
  const io = {
    stdout: { write: (s: string) => (out.push(s), true), isTTY: false },
    stderr: { write: (s: string) => (err.push(s), true), isTTY: false },
    env: {},
  };
  const code = await main(["demo", "--monthly-calls", "lots", "--out", join(tmpdir(), "replay-cli-monthly")], io as never);
  assert.equal(code, 2);
  assert.match(err.join(""), /--monthly-calls must be a whole number/);
});
