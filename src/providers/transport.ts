// One POST to a chat-completions endpoint. Retries only what cannot have been
// billed: a rate limit, an overloaded answer, a connection refused or a name
// that did not resolve. Anything that may have reached the model fails at
// once and is marked maybe billed.

export const REQUEST_TIMEOUT_MS = 120_000;
export const MAX_RESPONSE_BYTES = 4 * 1024 * 1024;
export const MAX_ATTEMPTS = 3;
/** The longest Retry-After the client waits. */
export const MAX_RETRY_AFTER_MS = 60_000;
const ERROR_BODY_BYTES = 4096;

export type FetchLike = (input: string, init: RequestInit) => Promise<Response>;

export type FailureCode =
  | "credential_missing"
  | "temperature_unsupported"
  | "response_format_unsupported"
  | "provider_rate_limited"
  | "provider_overloaded"
  | "provider_refused"
  | "provider_unavailable"
  | "provider_unreachable"
  | "provider_timeout"
  | "provider_malformed"
  | "provider_error"
  | "aborted";

/**
 * A request that returned no usable reply. The code is stable; the message is
 * for people and never carries a key, a prompt or a reply body.
 */
export class CallFailed extends Error {
  readonly code: FailureCode;
  /** The request may have reached the model although no usage came back. */
  readonly maybeBilled: boolean;
  /** HTTP status, when the endpoint answered. */
  readonly status: number | null;

  constructor(code: FailureCode, message: string, options: { maybeBilled?: boolean; status?: number | null } = {}) {
    super(message);
    this.name = "CallFailed";
    this.code = code;
    this.maybeBilled = options.maybeBilled ?? false;
    this.status = options.status ?? null;
  }
}

export interface PostOptions {
  fetch: FetchLike;
  headers: Record<string, string>;
  timeoutMs: number;
  sleep: (ms: number) => Promise<void>;
  now: () => number;
  signal?: AbortSignal;
  /** Called before each retry, so the caller can flag the call. */
  onRetry?: () => void;
}

export interface PostResult {
  body: Record<string, unknown>;
  /** Start to end of the request that answered, not of earlier tries or waits. */
  latencyMs: number;
}

const NEVER_SENT_CODES = new Set(["ECONNREFUSED", "ENOTFOUND", "EAI_AGAIN"]);

/** A connection refused or a name that did not resolve: nothing was sent. */
export function neverSent(error: unknown): boolean {
  const cause = (error as { cause?: unknown } | null)?.cause ?? error;
  const code = (cause as { code?: unknown } | null)?.code;
  if (typeof code === "string" && NEVER_SENT_CODES.has(code)) return true;
  const errors = (cause as { errors?: unknown } | null)?.errors;
  return (
    Array.isArray(errors) &&
    errors.length > 0 &&
    errors.every((inner) => NEVER_SENT_CODES.has(String((inner as { code?: unknown } | null)?.code)))
  );
}

/** Retry-After (seconds or an HTTP date) or retry-after-ms, capped; null when absent. */
export function retryAfterMs(headers: Headers, now: number): number | null {
  const ms = headers.get("retry-after-ms");
  if (ms !== null && /^\d+(\.\d+)?$/.test(ms.trim())) return Math.min(Number(ms), MAX_RETRY_AFTER_MS);
  const value = headers.get("retry-after");
  if (value === null) return null;
  const text = value.trim();
  if (/^\d+(\.\d+)?$/.test(text)) return Math.min(Number(text) * 1000, MAX_RETRY_AFTER_MS);
  const date = Date.parse(text);
  if (Number.isNaN(date)) return null;
  return Math.min(Math.max(0, date - now), MAX_RETRY_AFTER_MS);
}

/** Read at most `limit` bytes; the flag says the body was longer. */
async function readCapped(response: Response, limit: number): Promise<{ bytes: Uint8Array; over: boolean }> {
  if (response.body === null) return { bytes: new Uint8Array(0), over: false };
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      chunks.push(value);
      size += value.byteLength;
      if (size > limit) {
        await reader.cancel().catch(() => {});
        return { bytes: new Uint8Array(0), over: true };
      }
    }
  } finally {
    reader.releaseLock();
  }
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return { bytes, over: false };
}

async function errorDetail(response: Response): Promise<string> {
  try {
    const { bytes } = await readCapped(response, ERROR_BODY_BYTES * 64);
    return new TextDecoder("utf-8", { fatal: false }).decode(bytes.subarray(0, ERROR_BODY_BYTES)).toLowerCase();
  } catch {
    return "";
  }
}

/** A 400 that names the temperature field: the model takes none. */
function namesTemperature(detail: string): boolean {
  return detail.includes("temperature");
}

/** A refusal that names the response format or its JSON schema. */
function namesResponseFormat(detail: string): boolean {
  return /response_format|response format|responseformat|json_schema|json schema/.test(detail);
}

/**
 * A signal that aborts with a TimeoutError after `ms`. Unlike
 * AbortSignal.timeout, its timer keeps the process alive until it fires or
 * is cleared, so a hung request always ends as a timeout.
 */
function deadline(ms: number): { signal: AbortSignal; clear: () => void } {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(new DOMException("The request timed out.", "TimeoutError")), ms);
  return { signal: controller.signal, clear: () => clearTimeout(timer) };
}

function isTimeout(error: unknown): boolean {
  const name = (error as { name?: unknown } | null)?.name;
  return name === "TimeoutError";
}

export async function post(url: string, payload: Record<string, unknown>, options: PostOptions): Promise<PostResult> {
  const data = JSON.stringify(payload);
  for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt += 1) {
    if (options.signal?.aborted) throw new CallFailed("aborted", "the call was cancelled before it was sent");
    const limit = deadline(options.timeoutMs);
    try {
      const timeout = limit.signal;
      const signal = options.signal ? AbortSignal.any([options.signal, timeout]) : timeout;
      const started = options.now();
      let response: Response;
      try {
        response = await options.fetch(url, {
          method: "POST",
          headers: { "content-type": "application/json", ...options.headers },
          body: data,
          signal,
        });
      } catch (error) {
        if (neverSent(error)) {
          if (attempt < MAX_ATTEMPTS) {
            options.onRetry?.();
            await options.sleep(2000 * attempt);
            continue;
          }
          throw new CallFailed("provider_unreachable", "the endpoint could not be reached");
        }
        if (options.signal?.aborted) {
          throw new CallFailed("aborted", "the call was cancelled while in flight", { maybeBilled: true });
        }
        if (isTimeout(error) || timeout.aborted) {
          throw new CallFailed("provider_timeout", "timed out waiting for the endpoint", { maybeBilled: true });
        }
        throw new CallFailed("provider_unreachable", "the connection failed while the request was in flight", {
          maybeBilled: true,
        });
      }

      const status = response.status;
      if (status < 200 || status >= 300) {
        if (status === 429 || status === 529) {
          const wait = retryAfterMs(response.headers, Date.now());
          await response.body?.cancel().catch(() => {});
          if (attempt < MAX_ATTEMPTS) {
            options.onRetry?.();
            await options.sleep(wait ?? 2000 * attempt);
            continue;
          }
          throw status === 429
            ? new CallFailed("provider_rate_limited", "the endpoint answered HTTP 429", { status })
            : new CallFailed("provider_overloaded", "the endpoint answered HTTP 529 (overloaded)", { status });
        }
        if (status >= 400 && status < 500) {
          const detail = await errorDetail(response);
          if (status === 400 && namesTemperature(detail)) {
            throw new CallFailed("temperature_unsupported", "the endpoint refused the temperature setting", { status });
          }
          if (status !== 401 && status !== 403 && namesResponseFormat(detail)) {
            throw new CallFailed("response_format_unsupported", "the endpoint refused the response format", { status });
          }
          throw new CallFailed("provider_refused", `the endpoint answered HTTP ${status}`, { status });
        }
        await response.body?.cancel().catch(() => {});
        throw new CallFailed("provider_unavailable", `the endpoint answered HTTP ${status}`, {
          status,
          maybeBilled: true,
        });
      }

      let raw: { bytes: Uint8Array; over: boolean };
      try {
        raw = await readCapped(response, MAX_RESPONSE_BYTES);
      } catch (error) {
        if (isTimeout(error) || timeout.aborted) {
          throw new CallFailed("provider_timeout", "timed out reading the endpoint's reply", { maybeBilled: true, status });
        }
        throw new CallFailed("provider_unreachable", "the reply was cut off", { maybeBilled: true, status });
      }
      if (raw.over) {
        throw new CallFailed("provider_malformed", "the endpoint's reply exceeds the size limit", {
          maybeBilled: true,
          status,
        });
      }
      let body: unknown;
      try {
        body = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw.bytes));
      } catch {
        throw new CallFailed("provider_malformed", "the endpoint's reply is not JSON", { maybeBilled: true, status });
      }
      if (body === null || typeof body !== "object" || Array.isArray(body)) {
        throw new CallFailed("provider_malformed", "the endpoint's reply is not an object", { maybeBilled: true, status });
      }
      return { body: body as Record<string, unknown>, latencyMs: Math.max(0, Math.round(options.now() - started)) };
    } finally {
      limit.clear();
    }
  }
  throw new CallFailed("provider_unreachable", "no reply from the endpoint");
}
