// The estimate and the run's outcome, in plain words for the terminal.

import type { NotReplayableReason, RunFile } from "../types.ts";
import type { RunPlan } from "../run/index.ts";

export const REASONS: Record<NotReplayableReason, string> = {
  content_unavailable: "the trace does not carry the request",
  content_truncated: "the captured content was cut short",
  tool_history: "the conversation includes earlier tool calls and results",
  unsupported_content: "the input has images, audio or files",
  no_messages: "the request has no messages",
  last_turn_not_user: "the last turn is not from the user",
  input_too_large: "the input is too large to replay",
  tool_definitions_not_offered: "production forced a tool choice the replay cannot reproduce",
  web_search_not_replayed: "production searched the web (pass --allow-no-search to replay without search)",
  unparseable_request: "the request could not be read",
};

/** Dollars: four places under a cent, two otherwise. */
export function usd(value: number): string {
  if (value === 0) return "$0.00";
  if (Math.abs(value) < 0.01) return `$${value.toFixed(4)}`;
  return `$${value.toFixed(2)}`;
}

function plural(n: number, one: string, many = `${one}s`): string {
  return `${n} ${n === 1 ? one : many}`;
}

export interface ExplainOptions {
  /** Where to send a missing price, shown with unpriced models. */
  pricingHelpUrl: string;
  allowUnpriced: boolean;
}

export function explainEstimate(plan: RunPlan, options: ExplainOptions): string {
  const lines: string[] = [];
  const { estimate, settings } = plan;
  const sampled = plan.cases.length;
  const notReplayable = sampled - estimate.replayable;
  lines.push(
    `Read ${plural(plan.file.calls, "call")} in ${plural(plan.file.traces, "trace")} from ${plan.input.file} (${plan.input.adapter} format).`,
  );
  if (plan.file.skippedLines > 0) {
    lines.push(`Skipped ${plural(plan.file.skippedLines, "line")} that could not be read.`);
  }
  lines.push(
    `Sampled the latest ${plural(sampled, "case")} (up to ${settings.sample} replayable, one per trace): ` +
      `${estimate.replayable} can be replayed${notReplayable > 0 ? `, ${notReplayable} cannot:` : "."}`,
  );
  const reasons = Object.entries(estimate.notReplayable) as [NotReplayableReason, number][];
  for (const [reason, count] of reasons.sort((a, b) => b[1] - a[1])) {
    lines.push(`  ${count} because ${REASONS[reason] ?? reason}`);
  }

  const floor = plan.floorEndpoint !== null;
  const perCase = plan.treatments.length + (floor ? 2 : 0);
  lines.push("");
  lines.push(
    `Each replayable case runs on ${plural(plan.treatments.length, "model")}` +
      (floor ? ", plus twice on production's model for the noise floor" : "") +
      `: ${plural(estimate.replayable * perCase, "call")} in all.`,
  );
  for (const t of plan.treatments) {
    const arm = estimate.perArm[t.id];
    const where = plan.freeEndpoints.includes(t.endpoint) ? "local, free" : `via ${t.endpoint}`;
    const cost = estimate.unpriced.includes(t.model)
      ? "no price in the catalog"
      : arm
        ? `about ${usd(arm.expectedUsd)}, at most ${usd(arm.upperBoundUsd)}`
        : "";
    lines.push(`  ${t.id}  ${t.label} (${where})${cost ? `: ${cost}` : ""}`);
  }
  if (floor) {
    const a1 = estimate.perArm["floor_a1"];
    const a2 = estimate.perArm["floor_a2"];
    const expected = (a1?.expectedUsd ?? 0) + (a2?.expectedUsd ?? 0);
    const upper = (a1?.upperBoundUsd ?? 0) + (a2?.upperBoundUsd ?? 0);
    const where = plan.freeEndpoints.includes(plan.floorEndpoint!) ? "local, free" : `via ${plan.floorEndpoint}`;
    lines.push(`  noise floor, production's model twice (${where}): about ${usd(expected)}, at most ${usd(upper)}`);
  }
  lines.push("");
  lines.push(`Expected spend: about ${usd(estimate.expectedUsd)} (production's token counts, with a 25% margin).`);
  lines.push(
    `Most it can spend: ${usd(estimate.upperBoundUsd)} (every call using its full max_tokens). ` +
      `Budget: ${usd(settings.budgetUsd)}.`,
  );
  if (estimate.expectedUsd > settings.budgetUsd) {
    lines.push(`The expected spend is over the budget, so run will refuse. Raise --budget or lower --sample.`);
  } else if (estimate.upperBoundUsd > settings.budgetUsd) {
    lines.push(
      `The run reserves each call's maximum before sending it and stops at the budget, so it may replay fewer cases.`,
    );
  }
  if (estimate.unpriced.length > 0) {
    lines.push("");
    lines.push(`No price for: ${estimate.unpriced.join(", ")}. Their calls are not in the estimate.`);
    lines.push(
      options.allowUnpriced
        ? `They run anyway (--allow-unpriced): spend the endpoint reports still counts toward the budget.`
        : `Run refuses them unless you pass --allow-unpriced. Add a price: ${options.pricingHelpUrl}`,
    );
  }
  return lines.join("\n");
}

export const STOP_REASONS: Record<NonNullable<RunFile["stopped"]>, string> = {
  budget: "the budget could not cover the next case",
  interrupted: "it was interrupted",
  reservation_exceeded: "a call cost more than it reserved, so no more calls were sent",
};

export function explainOutcome(run: RunFile): string {
  const counts = { ok: 0, failed: 0, skipped: 0 };
  const errors = new Map<string, number>();
  for (const replay of run.replays) {
    counts[replay.status]++;
    if (replay.status === "failed") {
      const code = replay.error?.code ?? "failed";
      errors.set(code, (errors.get(code) ?? 0) + 1);
    }
  }
  const lines = [
    `Replayed ${plural(counts.ok + counts.failed, "call")}: ${counts.ok} answered, ${counts.failed} failed` +
      (counts.skipped ? `, ${counts.skipped} not sent` : "") +
      `. Spent ${usd(run.spentUsd)} of ${usd(run.settings.budgetUsd)}.`,
  ];
  if (errors.size > 0) {
    lines.push(`Failures: ${[...errors].map(([code, n]) => `${code} (${n})`).join(", ")}.`);
  }
  if (run.stopped) lines.push(`Stopped early: ${STOP_REASONS[run.stopped]}.`);
  return lines.join("\n");
}
