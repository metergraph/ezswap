// An OpenAI-compatible chat-completions client for replays.
//
// One request per replay. The client retries only what cannot have been
// billed (transport.ts), resends once without a temperature the model refuses
// (and remembers that model), resends once without a response_format the
// endpoint refuses by name, and reads the reply into the replay's terms. Keys
// are read from the environment once and never appear in an error or output.

import type { Provider, ReplayCall, ReplayFlag, ReplyOutcome } from "../types.ts";
import { createLimiter } from "./limiter.ts";
import { buildPayload, parseReply } from "./payload.ts";
import { CallFailed, type FetchLike, REQUEST_TIMEOUT_MS, post } from "./transport.ts";

export { createLimiter, type Limiter } from "./limiter.ts";
export { buildPayload, forcedToolRequest, isCutOff, isRefusalReason, parseReply, readUsage } from "./payload.ts";
export { CallFailed, MAX_ATTEMPTS, MAX_RESPONSE_BYTES, MAX_RETRY_AFTER_MS, REQUEST_TIMEOUT_MS } from "./transport.ts";
export type { FailureCode, FetchLike } from "./transport.ts";

export const DEFAULT_CONCURRENCY = 4;

export type PresetName = "openai" | "openrouter" | "vercel" | "litellm" | "ollama";

export interface Preset {
  /** Base URL, or the environment variable that holds it. */
  baseUrl: string | null;
  baseUrlEnv: string | null;
  /** Environment variable holding the key; null for an endpoint without one. */
  apiKeyEnv: string | null;
  /** A model prefix this endpoint does not take, e.g. "openai/". */
  stripPrefix: string | null;
  maxTokensField: "max_tokens" | "max_completion_tokens";
  /** Request fields the preset adds. */
  extra: Record<string, unknown>;
  /** Runs on the user's machine and costs nothing. */
  free: boolean;
}

export const PRESETS: Record<PresetName, Preset> = {
  openai: {
    baseUrl: "https://api.openai.com/v1",
    baseUrlEnv: null,
    apiKeyEnv: "OPENAI_API_KEY",
    stripPrefix: "openai/",
    maxTokensField: "max_completion_tokens",
    extra: {},
    free: false,
  },
  openrouter: {
    baseUrl: "https://openrouter.ai/api/v1",
    baseUrlEnv: null,
    apiKeyEnv: "OPENROUTER_API_KEY",
    stripPrefix: null,
    maxTokensField: "max_tokens",
    // Ask OpenRouter to report what it charged in usage.cost.
    extra: { usage: { include: true } },
    free: false,
  },
  vercel: {
    baseUrl: "https://ai-gateway.vercel.sh/v1",
    baseUrlEnv: null,
    apiKeyEnv: "AI_GATEWAY_API_KEY",
    stripPrefix: null,
    maxTokensField: "max_tokens",
    extra: {},
    free: false,
  },
  litellm: {
    baseUrl: null,
    baseUrlEnv: "LITELLM_BASE_URL",
    apiKeyEnv: "LITELLM_API_KEY",
    stripPrefix: null,
    maxTokensField: "max_tokens",
    extra: {},
    free: false,
  },
  ollama: {
    baseUrl: "http://localhost:11434/v1",
    baseUrlEnv: null,
    apiKeyEnv: null,
    stripPrefix: null,
    maxTokensField: "max_tokens",
    extra: {},
    free: true,
  },
};

export function isPreset(name: string): name is PresetName {
  return Object.hasOwn(PRESETS, name);
}

/** A treatment's endpoint runs locally for free (the Ollama preset). */
export function isFreeEndpoint(endpoint: string): boolean {
  return isPreset(endpoint) && PRESETS[endpoint].free;
}

export interface ProviderOptions {
  preset?: PresetName;
  /** A custom endpoint's base URL, or an override of the preset's. */
  baseUrl?: string;
  /** Environment variable holding the key. Overrides the preset's; null for none. */
  apiKeyEnv?: string | null;
  fetch?: FetchLike;
  concurrency?: number;
  /** Where keys and base URLs are read. Default process.env. */
  env?: Record<string, string | undefined>;
  timeoutMs?: number;
  /** Treat a custom endpoint as free (a local vLLM server, say). */
  free?: boolean;
  /** For tests: waits between retries, and the clock for latency. */
  sleep?: (ms: number) => Promise<void>;
  now?: () => number;
}

export interface OpenAICompatibleProvider extends Provider {
  /** Base URL, without any credential. */
  readonly baseUrl: string;
  readonly free: boolean;
  /** Models that refused an explicit temperature, for this provider's lifetime. */
  readonly temperatureRefused: ReadonlySet<string>;
}

/** The endpoint is misconfigured: a missing key or base URL. Says which variable, never its value. */
export class ProviderConfigError extends Error {
  readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.name = "ProviderConfigError";
    this.code = code;
  }
}

function endpointName(preset: PresetName | undefined, baseUrl: string): string {
  if (preset) return preset;
  try {
    return new URL(baseUrl).host;
  } catch {
    return "custom";
  }
}

export function createProvider(options: ProviderOptions): OpenAICompatibleProvider {
  const env = options.env ?? process.env;
  const preset = options.preset ? PRESETS[options.preset] : null;
  if (options.preset && !preset) throw new ProviderConfigError("unknown_preset", "unknown endpoint preset");

  const baseUrlEnv = preset?.baseUrlEnv ?? null;
  const baseUrl = (options.baseUrl ?? preset?.baseUrl ?? (baseUrlEnv ? env[baseUrlEnv]?.trim() : undefined) ?? "").replace(
    /\/+$/,
    "",
  );
  if (!baseUrl) {
    throw new ProviderConfigError(
      "base_url_missing",
      baseUrlEnv ? `${baseUrlEnv} is not set` : "a custom endpoint needs a base URL",
    );
  }
  let parsed: URL;
  try {
    parsed = new URL(baseUrl);
  } catch {
    throw new ProviderConfigError("base_url_invalid", "the endpoint base URL is not a valid URL");
  }
  if (parsed.username || parsed.password) {
    throw new ProviderConfigError("base_url_invalid", "put the key in an environment variable, not in the base URL");
  }

  const apiKeyEnv = options.apiKeyEnv !== undefined ? options.apiKeyEnv : (preset?.apiKeyEnv ?? null);
  let key: string | null = null;
  if (apiKeyEnv) {
    key = env[apiKeyEnv]?.trim() || null;
    if (!key) throw new ProviderConfigError("credential_missing", `${apiKeyEnv} is not set`);
  }

  const fetchImpl: FetchLike = options.fetch ?? ((input, init) => fetch(input, init));
  const limiter = createLimiter(options.concurrency ?? DEFAULT_CONCURRENCY);
  const sleep = options.sleep ?? ((ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms)));
  const now = options.now ?? (() => performance.now());
  const timeoutMs = options.timeoutMs ?? REQUEST_TIMEOUT_MS;
  const url = `${baseUrl}/chat/completions`;
  const headers: Record<string, string> = key ? { authorization: `Bearer ${key}` } : {};
  const refused = new Set<string>();
  const name = endpointName(options.preset, baseUrl);
  const free = options.free ?? preset?.free ?? false;

  const endpointModel = (model: string): string => {
    const prefix = preset?.stripPrefix;
    return prefix && model.toLowerCase().startsWith(prefix) ? model.slice(prefix.length) : model;
  };

  async function complete(call: ReplayCall, signal?: AbortSignal): Promise<ReplyOutcome> {
    const flags = new Set<ReplayFlag>();
    const send = async (withTemperature: boolean, withResponseFormat: boolean) => {
      const body = buildPayload(call, {
        temperature: withTemperature,
        responseFormat: withResponseFormat,
        model: endpointModel(call.model),
        maxTokensField: preset?.maxTokensField ?? "max_tokens",
        extra: preset?.extra ?? {},
      });
      return post(url, body, {
        fetch: fetchImpl,
        headers,
        timeoutMs,
        sleep,
        now,
        ...(signal ? { signal } : {}),
        onRetry: () => flags.add("retried"),
      });
    };

    // A temperature the model refused is dropped once and remembered.
    const sendWithFallback = async (withResponseFormat: boolean) => {
      if (call.temperature === null) return send(false, withResponseFormat);
      if (refused.has(call.model)) {
        flags.add("temperature_dropped");
        return send(false, withResponseFormat);
      }
      try {
        return await send(true, withResponseFormat);
      } catch (error) {
        if (!(error instanceof CallFailed) || error.code !== "temperature_unsupported") throw error;
        refused.add(call.model);
        flags.add("temperature_dropped");
        return send(false, withResponseFormat);
      }
    };

    try {
      let result;
      try {
        result = await sendWithFallback(true);
      } catch (error) {
        // A refused request was never billed. When the refusal names the
        // response format the call carried, try once without it.
        if (!(error instanceof CallFailed) || error.code !== "response_format_unsupported" || !call.responseFormat) {
          throw error;
        }
        flags.add("response_format_dropped");
        result = await sendWithFallback(false);
      }
      const body = result.body;
      const choices = body["choices"];
      if ((!Array.isArray(choices) || choices.length === 0) && body["error"] !== undefined) {
        // Some gateways answer 200 with an error object and no choice.
        throw new CallFailed("provider_error", "the endpoint returned an error instead of a reply", {
          maybeBilled: true,
        });
      }
      const reply = parseReply(body);
      return {
        status: "ok",
        output: reply.output,
        finishReason: reply.finishReason,
        refusal: reply.refusal,
        usage: reply.usage,
        reportedCostUsd: reply.reportedCostUsd,
        latencyMs: result.latencyMs,
        flags: [...flags],
        error: null,
      };
    } catch (error) {
      return failure(error, flags);
    }
  }

  return {
    name,
    baseUrl,
    free,
    temperatureRefused: refused,
    complete: (call, signal) => limiter.run(() => complete(call, signal)),
  };
}

function failure(error: unknown, flags: Set<ReplayFlag>): ReplyOutcome {
  let code: string = "provider_error";
  let message = "the call failed";
  if (error instanceof CallFailed) {
    code = error.code;
    message = error.message;
    // A refusal named a setting this call did not carry: an ordinary refusal.
    if (code === "temperature_unsupported" || code === "response_format_unsupported") code = "provider_refused";
    if (error.maybeBilled) flags.add("maybe_billed");
  } else {
    flags.add("maybe_billed");
  }
  return {
    status: "failed",
    output: null,
    finishReason: null,
    refusal: false,
    usage: null,
    reportedCostUsd: null,
    latencyMs: null,
    flags: [...flags],
    error: { code, message },
  };
}
