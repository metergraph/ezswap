// The link into hosted Metergraph. Replay has no telemetry, so this link is
// the only way a run reaches the hosted product, and it carries only the
// run's setup: which models with which settings, which fields, how many
// calls. Never trace content, never keys, never endpoints. The setup rides
// in the URL fragment, which browsers do not send to the server.
// docs/hosted-handoff.md documents the format.

import type { RunFile } from "../types.ts";

export const HOSTED_URL = "https://www.metergraph.dev/replay";

export type LinkMedium = "cli" | "action" | "readme";

/** Version 1 of the fragment payload. */
export interface HostedSetup {
  v: 1;
  treatments: {
    model: string;
    settings: { temperature: number | null; maxTokens: number | null; reasoningEffort: string | null };
  }[];
  trackedFields: string[];
  sample: number;
}

/** The setup the fragment carries: built field by field so nothing else can slip in. */
export function hostedSetup(run: RunFile): HostedSetup {
  return {
    v: 1,
    treatments: run.treatments.map((t) => ({
      model: String(t.model),
      settings: {
        temperature: typeof t.settings?.temperature === "number" ? t.settings.temperature : null,
        maxTokens: typeof t.settings?.maxTokens === "number" ? t.settings.maxTokens : null,
        reasoningEffort: typeof t.settings?.reasoningEffort === "string" ? t.settings.reasoningEffort : null,
      },
    })),
    trackedFields: (run.settings?.trackedFields ?? []).map(String),
    sample: Number(run.settings?.sample) || 0,
  };
}

/** base64url (RFC 4648 section 5) without padding. */
export function encodeSetup(setup: HostedSetup): string {
  return Buffer.from(JSON.stringify(setup), "utf8").toString("base64url");
}

export function decodeSetup(fragment: string): HostedSetup {
  const value = fragment.startsWith("#") ? fragment.slice(1) : fragment;
  const encoded = value.startsWith("setup=") ? value.slice("setup=".length) : value;
  return JSON.parse(Buffer.from(encoded, "base64url").toString("utf8")) as HostedSetup;
}

/** The "keep this running on live traffic" link, with UTM parameters and the run's setup in the fragment. */
export function hostedLink(run: RunFile, medium: LinkMedium): string {
  const query = new URLSearchParams({
    utm_source: "replay",
    utm_medium: medium,
    utm_campaign: run.tool?.version || "unknown",
  });
  return `${HOSTED_URL}?${query.toString()}#setup=${encodeSetup(hostedSetup(run))}`;
}
