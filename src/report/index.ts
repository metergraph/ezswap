// Report rendering in the terminal: the static report, the interactive
// explorer and the link into hosted Metergraph. Every figure and sentence
// comes from core, through model.ts.
import type { RunFile } from "../types.ts";
import { type LinkMedium, hostedLink as link } from "./link.ts";
import { type TerminalOptions, renderReport as render } from "./terminal.ts";

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

/** The static terminal report: width-aware panels, Unicode or ASCII, colour optional. */
export function renderReport(run: RunFile, options: TerminalOptions = {}): string {
  return render(run, options);
}

/** The "keep this running on live traffic" link, with UTM parameters. */
export function hostedLink(run: RunFile, medium: LinkMedium): string {
  return link(run, medium);
}

export type { LinkMedium, TerminalOptions };
export { HOSTED_URL, type HostedSetup, decodeSetup, encodeSetup, hostedSetup } from "./link.ts";
export { NOISE_FLOOR_DOC, SETUP_COMMAND, type ReportModel, monthlyCalls, reportModel } from "./model.ts";
export { MAX_WIDTH, MIN_WIDTH } from "./terminal.ts";
export { type ExplorerIo, type ExplorerResult, runExplorer } from "./explore/run.ts";
export { type ExplorerState, initialState, explorerData, parseKeys, reduce, resize } from "./explore/state.ts";
export { renderScreen } from "./explore/screen.ts";
