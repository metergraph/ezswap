// Write a run: run.json and the single-file HTML report beside it.

import { mkdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { renderHtml, type ReportOptions } from "../report/index.ts";
import type { RunFile } from "../types.ts";

export interface WrittenRun {
  runPath: string;
  reportPath: string;
}

/** run.json holds no key: treatments name an endpoint preset or host only. */
export async function writeRun(dir: string, run: RunFile, options: ReportOptions = {}): Promise<WrittenRun> {
  const out = resolve(dir);
  await mkdir(out, { recursive: true });
  const runPath = join(out, "run.json");
  const reportPath = join(out, "report.html");
  await writeFile(runPath, `${JSON.stringify(run, null, 2)}\n`, "utf8");
  await writeFile(reportPath, renderHtml(run, options), "utf8");
  return { runPath, reportPath };
}

/** Render report.html for an existing run into `dir`. */
export async function writeReport(dir: string, run: RunFile, options: ReportOptions = {}): Promise<string> {
  const out = resolve(dir);
  await mkdir(out, { recursive: true });
  const reportPath = join(out, "report.html");
  await writeFile(reportPath, renderHtml(run, options), "utf8");
  return reportPath;
}
