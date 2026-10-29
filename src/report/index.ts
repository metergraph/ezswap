// Report rendering: the single-file HTML report, the terminal table and the
// link into hosted Metergraph. Every figure and sentence comes from core.
import type { RunFile } from "../types.ts";
import { renderPage } from "./html.ts";
import { type LinkMedium, hostedLink as link } from "./link.ts";
import { renderTerminalTable } from "./table.ts";

export interface ReportOptions {
  /** Tracked JSON field paths to show; defaults to run.settings.trackedFields. */
  tracked?: string[];
  /** Show within-noise labels (opt-in). */
  labels?: boolean;
  /**
   * Calls a month to project a monthly cost from. Defaults to the rate in
   * run.input.traffic; without either, no monthly cost is projected.
   */
  monthlyCalls?: number | null;
}

/** One self-contained HTML document: inline CSS and JS, no network. */
export function renderHtml(run: RunFile, options: ReportOptions = {}): string {
  return renderPage(run, options);
}

export interface TableOptions extends ReportOptions {
  /** Terminal width in columns; default 100. */
  width?: number;
  /** Use ANSI colour. */
  color?: boolean;
  /** Plain ASCII only; defaults to true when stdout is not a terminal. */
  ascii?: boolean;
}

/** A plain-text summary table for the terminal. */
export function renderTable(run: RunFile, options: TableOptions = {}): string {
  return renderTerminalTable(run, options);
}

/** The "keep this running on live traffic" link, with UTM parameters. */
export function hostedLink(run: RunFile, medium: "report" | "cli" | "action" | "readme"): string {
  return link(run, medium);
}

export type { LinkMedium };
export { HOSTED_URL, type HostedSetup, decodeSetup, encodeSetup, hostedSetup } from "./link.ts";
export { NOISE_FLOOR_DOC, SETUP_COMMAND, type ReportModel, monthlyCalls, reportModel } from "./model.ts";
export { escapeHtml, scriptJson } from "./html.ts";
