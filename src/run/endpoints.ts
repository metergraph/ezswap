// Endpoints and model specs: what `--endpoint`, `--floor-endpoint` and
// `--models` name, read into treatments and provider settings. Nothing here
// reads a key: a key stays in its environment variable until a provider
// is created.

import { PRESETS, isPreset, type PresetName } from "../providers/index.ts";
import type { TreatmentSettings } from "../types.ts";

export const DEFAULT_ENDPOINT: PresetName = "openrouter";

export interface EndpointSpec {
  /** The name a treatment records: the preset, or the base URL's host. Never a key. */
  key: string;
  preset: PresetName | null;
  /** A custom endpoint's base URL. */
  baseUrl: string | null;
  /** Runs on this machine and costs nothing (Ollama, or a loopback URL). */
  free: boolean;
}

export class SpecError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SpecError";
  }
}

const LOOPBACK = new Set(["localhost", "127.0.0.1", "[::1]", "::1", "0.0.0.0"]);

/** A preset name or an http(s) base URL. */
export function parseEndpoint(spec: string): EndpointSpec {
  const text = spec.trim();
  if (isPreset(text)) return { key: text, preset: text, baseUrl: null, free: PRESETS[text].free };
  let url: URL;
  try {
    url = new URL(text);
  } catch {
    throw new SpecError(
      `"${text}" is not an endpoint. Use a preset (${Object.keys(PRESETS).join(", ")}) or an http(s) base URL.`,
    );
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    throw new SpecError(`The endpoint URL must start with http:// or https://.`);
  }
  if (url.username || url.password) {
    throw new SpecError("Put the key in an environment variable, not in the endpoint URL.");
  }
  const baseUrl = `${url.origin}${url.pathname}`.replace(/\/+$/, "");
  const host = url.hostname.endsWith(".localhost") ? "localhost" : url.hostname;
  return { key: url.host, preset: null, baseUrl, free: LOOPBACK.has(host) };
}

export interface ModelSpec {
  model: string;
  endpoint: EndpointSpec;
  /** Only the settings this spec set; the rest come from the command's flags. */
  settings: Partial<TreatmentSettings>;
}

const SETTING_KEYS = new Set(["temperature", "max_tokens", "reasoning_effort"]);

function parseSetting(spec: string, assignment: string, into: Partial<TreatmentSettings>): void {
  const eq = assignment.indexOf("=");
  const name = assignment.slice(0, eq).trim();
  const value = assignment.slice(eq + 1).trim();
  if (eq < 0 || !SETTING_KEYS.has(name) || !value) {
    throw new SpecError(
      `Could not read "${assignment}" in "${spec}". Settings are temperature=, max_tokens= and reasoning_effort=.`,
    );
  }
  if (name === "temperature") into.temperature = parseTemperature(value);
  else if (name === "max_tokens") into.maxTokens = parseMaxTokens(value);
  else into.reasoningEffort = parseEffort(value);
}

export function parseTemperature(value: string): number {
  const n = Number(value);
  if (!/^\d+(\.\d+)?$/.test(value.trim()) || !Number.isFinite(n) || n < 0 || n > 2) {
    throw new SpecError(`Temperature must be a number from 0 to 2, not "${value}".`);
  }
  return n;
}

export function parseMaxTokens(value: string): number {
  const n = Number(value);
  if (!/^\d+$/.test(value.trim()) || n < 1 || n > 200_000) {
    throw new SpecError(`Max tokens must be a whole number from 1 to 200000, not "${value}".`);
  }
  return n;
}

const EFFORTS = new Set(["none", "minimal", "low", "medium", "high", "xhigh"]);

export function parseEffort(value: string): string {
  const effort = value.trim().toLowerCase();
  if (!EFFORTS.has(effort)) throw new SpecError(`Reasoning effort must be one of ${[...EFFORTS].join(", ")}.`);
  return effort;
}

/**
 * Split a `--models` list. A comma also separates per-model settings
 * (`model@temperature=0.2,max_tokens=500`), so a piece that is only a
 * setting belongs to the model before it.
 */
function splitModels(list: string): string[] {
  const pieces = list.split(",").map((piece) => piece.trim()).filter(Boolean);
  const specs: string[] = [];
  for (const piece of pieces) {
    const last = specs.length - 1;
    if (last >= 0 && specs[last]!.includes("@") && /^[a-z_]+=/.test(piece)) specs[last] += `,${piece}`;
    else specs.push(piece);
  }
  return specs;
}

/**
 * Read `--models`. A model may name its endpoint preset first
 * (`openai:gpt-5-mini`); a bare model uses the default endpoint. On the
 * OpenAI preset a bare id is read as an OpenAI model, so it prices.
 */
export function parseModels(list: string, defaultEndpoint: EndpointSpec): ModelSpec[] {
  const specs = splitModels(list);
  if (specs.length === 0) throw new SpecError("--models names no model.");
  return specs.map((spec) => {
    let rest = spec;
    let endpoint = defaultEndpoint;
    const colon = rest.indexOf(":");
    if (colon > 0 && isPreset(rest.slice(0, colon))) {
      endpoint = parseEndpoint(rest.slice(0, colon));
      rest = rest.slice(colon + 1);
    }
    const settings: Partial<TreatmentSettings> = {};
    const at = rest.indexOf("@");
    if (at >= 0) {
      for (const assignment of rest.slice(at + 1).split(",")) parseSetting(spec, assignment, settings);
      rest = rest.slice(0, at);
    }
    let model = rest.trim();
    if (!model || /\s/.test(model)) throw new SpecError(`"${spec}" does not name a model.`);
    if (endpoint.preset === "openai" && !model.includes("/")) model = `openai/${model}`;
    return { model, endpoint, settings };
  });
}
