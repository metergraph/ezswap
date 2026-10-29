// Write a run: run.json. The report is drawn in the terminal from it.

import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import type { RunFile } from "../types.ts";

export interface WrittenRun {
  runPath: string;
}

/** run.json holds no key: treatments name an endpoint preset or host only. */
export async function writeRun(dir: string, run: RunFile): Promise<WrittenRun> {
  const out = resolve(dir);
  await mkdir(out, { recursive: true });
  const runPath = join(out, "run.json");
  await writeFile(runPath, `${JSON.stringify(run, null, 2)}\n`, "utf8");
  return { runPath };
}
