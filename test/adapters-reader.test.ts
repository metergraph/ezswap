import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { detectAdapter, readTraces } from "../src/adapters/index.ts";

const fixture = (name: string) => fileURLToPath(new URL(`./fixtures/${name}`, import.meta.url));

async function temp(name: string, content: string): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), "replay-reader-"));
  const path = join(dir, name);
  await writeFile(path, content);
  return path;
}

test("detectAdapter picks each fixture's format", async () => {
  const first = async (name: string) => JSON.parse((await readFile(fixture(name), "utf8")).split("\n")[0] ?? "");
  assert.equal(detectAdapter(await first("replay.jsonl"))?.name, "replay-jsonl");
  assert.equal(detectAdapter(await first("raw-pairs.jsonl"))?.name, "raw");
  assert.equal(detectAdapter(await first("helicone.jsonl"))?.name, "raw");
  assert.equal(detectAdapter(await first("litellm.jsonl"))?.name, "raw");
  assert.equal(detectAdapter(await first("langfuse.jsonl"))?.name, "langfuse");
  assert.equal(detectAdapter(await first("otel-spans.jsonl"))?.name, "otel");
  assert.equal(detectAdapter({ hello: "world" }), null);
});

test("an explicit adapter overrides detection", async () => {
  const calls = await readTraces(fixture("raw-pairs.jsonl"), { adapter: "raw", limit: 1 });
  assert.equal(calls.length, 1);
  await assert.rejects(readTraces(fixture("raw-pairs.jsonl"), { adapter: "nope" }), /Unknown trace format/);
});

test("an undetectable file is an error that names the formats", async () => {
  const path = await temp("unknown.jsonl", '{"hello":"world"}\n');
  await assert.rejects(readTraces(path), /replay-jsonl, raw, langfuse, otel|otel, langfuse, replay-jsonl, raw/);
});

test("blank lines are ignored and bad lines are skipped with a line number", async () => {
  const good = '{"request":{"messages":[{"role":"user","content":"hi"}]},"response":"hello"}';
  const path = await temp("mixed.jsonl", `${good}\n\nnot json\n${good}\r\n`);
  const skipped: number[] = [];
  const calls = await readTraces(path, { onSkip: (line) => skipped.push(line) });
  assert.equal(calls.length, 2);
  assert.deepEqual(skipped, [3]);
  assert.equal(calls[0]?.id, calls[1]?.id, "id is a hash of the line");
  assert.equal(calls[0]?.response.text, "hello");
});

test("an empty file reads as no calls", async () => {
  assert.deepEqual(await readTraces(await temp("empty.jsonl", "\n  \n")), []);
});

test("a JSON array of raw pairs", async () => {
  const path = await temp(
    "array.json",
    JSON.stringify([
      { request: "Say hi", response: "hi" },
      { request: "Say bye", response: "bye" },
    ]),
  );
  const calls = await readTraces(path);
  assert.equal(calls.length, 2);
  assert.notEqual(calls[0]?.id, calls[1]?.id);
});
