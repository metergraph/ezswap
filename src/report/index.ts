// Report rendering. Placeholder signatures that the runner and CLI build
// against; the report module replaces the bodies.
import type { RunFile } from "../types.ts";

export interface ReportOptions {
  /** Tracked JSON field paths to show; defaults to run.settings.trackedFields. */
  tracked?: string[];
  /** Show within-noise labels (opt-in). */
  labels?: boolean;
}

/** One self-contained HTML document: inline CSS and JS, no network. */
export function renderHtml(run: RunFile, options: ReportOptions = {}): string {
  void options;
  return `<!doctype html><title>Metergraph Replay</title><p>${run.treatments.length} treatments</p>`;
}

export interface TableOptions extends ReportOptions {
  /** Terminal width in columns; default 100. */
  width?: number;
  /** Use ANSI colour. */
  color?: boolean;
}

/** A plain-text summary table for the terminal. */
export function renderTable(run: RunFile, options: TableOptions = {}): string {
  void options;
  return `${run.treatments.length} treatments\n`;
}

/** The "keep this running on live traffic" link, with UTM parameters. */
export function hostedLink(run: RunFile, medium: "report" | "cli" | "action" | "readme"): string {
  void run;
  return `https://www.metergraph.dev/replay?utm_source=replay&utm_medium=${medium}`;
}
