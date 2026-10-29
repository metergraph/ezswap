// Shared contracts. Every module builds against these, so a change here is a
// change to every module: keep it small and additive.
//
// Flow: an adapter reads a trace file into CapturedCall[]; cases turns each
// into a Case (replayable or with a reason it is not); the runner sends a
// ReplayCall per case per arm through a Provider and records a Replay; the
// whole run is one RunFile (run.json); core reads a RunFile into the
// statistics view; report renders it.

// ---------------------------------------------------------------------------
// Captured calls (adapter output)

/** One production LLM call as an adapter read it, before any replay rule. */
export interface CapturedCall {
  /** Stable within the file: the source's own id, else a hash of the line. */
  id: string;
  traceId: string | null;
  /** ISO 8601 start time, when the source has one. */
  ts: string | null;
  /** Adapter that read it: "replay-jsonl" | "raw" | "langfuse" | "otel". */
  source: string;
  /** "openai" | "anthropic" | "google" | ... as captured, lower case. */
  provider: string | null;
  model: string | null;
  /**
   * The request body in the provider's own shape (OpenAI chat or Responses,
   * Anthropic Messages, Gemini), or the replay JSONL request. cases parses it.
   */
  request: unknown;
  response: CapturedResponse;
  /** True when the source says or shows content was cut. */
  truncated: boolean;
  /** Field names the source did not carry, e.g. ["tools", "response_format"]. */
  missing: string[];
}

export interface CapturedResponse {
  /** Assistant text; null when the source carried none. */
  text: string | null;
  /** Provider-shaped tool calls, when any. */
  toolCalls: unknown[] | null;
  finishReason: string | null;
  refusal: boolean;
  usage: Usage | null;
  /** What production paid for this call, when the source records it. */
  costUsd: number | null;
  latencyMs: number | null;
}

export interface Usage {
  inputTokens: number | null;
  outputTokens: number | null;
  /** Cache-read input tokens, whether or not inputTokens includes them. */
  cachedInputTokens: number | null;
  reasoningTokens: number | null;
}

// ---------------------------------------------------------------------------
// Cases (cases module output)

export interface Message {
  role: "user" | "assistant";
  content: string;
}

export interface ForcedTool {
  name: string;
  description: string | null;
  /** JSON Schema of the tool's arguments. */
  parameters: Record<string, unknown>;
}

/** Production settings a replay can carry. */
export interface CallParams {
  temperature: number | null;
  maxTokens: number | null;
  topP: number | null;
  stop: string[] | null;
  reasoningEffort: string | null;
}

export type NotReplayableReason =
  | "content_unavailable"
  | "content_truncated"
  | "tool_history"
  | "unsupported_content"
  | "no_messages"
  | "last_turn_not_user"
  | "input_too_large"
  | "tool_definitions_not_offered"
  | "web_search_not_replayed"
  | "unparseable_request";

export interface Case {
  id: string;
  traceId: string | null;
  ts: string | null;
  source: string;
  provider: string | null;
  /** Production's model as captured. */
  model: string | null;
  /** Folded system and developer turns, or null. */
  system: string | null;
  /** Text turns, same-role turns merged; the last one is from the user. */
  messages: Message[];
  /** OpenAI chat `response_format`, translated from any captured shape. */
  responseFormat: Record<string, unknown> | null;
  forcedTool: ForcedTool | null;
  params: CallParams;
  /** Did production use web search on this call? */
  webSearch: boolean;
  /** Production's output: text, or a single tool call's JSON arguments. */
  production: CaseOutput;
  /** Short preview of the last user turn, for the case browser. */
  inputPreview: string | null;
  fidelity: "full" | "partial";
  missing: string[];
  /** Null when replayable. */
  notReplayable: NotReplayableReason | null;
}

export interface CaseOutput {
  output: string | null;
  finishReason: string | null;
  refusal: boolean;
  usage: Usage | null;
  costUsd: number | null;
  latencyMs: number | null;
}

// ---------------------------------------------------------------------------
// Providers

/** One request the runner sends. Everything a provider needs, nothing else. */
export interface ReplayCall {
  /** Model id as the endpoint names it, e.g. "openai/gpt-5-mini". */
  model: string;
  system: string | null;
  messages: Message[];
  maxTokens: number;
  temperature: number | null;
  topP: number | null;
  stop: string[] | null;
  reasoningEffort: string | null;
  responseFormat: Record<string, unknown> | null;
  forcedTool: ForcedTool | null;
}

export type ReplayFlag =
  | "temperature_dropped"
  | "response_format_dropped"
  | "maybe_billed"
  | "retried";

export interface ReplyOutcome {
  status: "ok" | "failed";
  /** Text, or the first tool call's arguments when the text is empty. */
  output: string | null;
  finishReason: string | null;
  refusal: boolean;
  usage: Usage | null;
  /** Amount the endpoint says it charged, when it reports one. */
  reportedCostUsd: number | null;
  /** Wall time of the request that answered. */
  latencyMs: number | null;
  flags: ReplayFlag[];
  error: { code: string; message: string } | null;
}

export interface Provider {
  /** Preset or endpoint name, shown in the report. */
  readonly name: string;
  complete(call: ReplayCall, signal?: AbortSignal): Promise<ReplyOutcome>;
}

// ---------------------------------------------------------------------------
// Pricing

export interface PriceQuery {
  /** "creator/model" or any alias the catalog knows. */
  model: string;
  inputTokens: number;
  outputTokens: number;
  cachedInputTokens: number | null;
  /** Does inputTokens already include cachedInputTokens? Null: provider rule. */
  inputIncludesCacheRead: boolean | null;
  /** ISO date the price applies at; default now. */
  at?: string;
}

export interface Pricing {
  /** Catalog source commit or version, for the report footer. */
  readonly catalogVersion: string;
  /** Null when the catalog cannot price this model. */
  price(query: PriceQuery): number | null;
  /** Can the catalog price this model at all? */
  knows(model: string): boolean;
}

// ---------------------------------------------------------------------------
// The run (run.json)

export interface TreatmentSettings {
  temperature: number | null;
  maxTokens: number | null;
  reasoningEffort: string | null;
}

export interface Treatment {
  /** "t1", "t2", ... in the order given. */
  id: string;
  label: string;
  model: string;
  /** Endpoint preset or base URL host, never a key. */
  endpoint: string;
  settings: TreatmentSettings;
}

/** "control" never appears: production's recorded output is the control. */
export type ArmId = string | "floor_a1" | "floor_a2";

export interface Replay {
  caseId: string;
  arm: ArmId;
  status: "ok" | "failed" | "skipped";
  output: string | null;
  finishReason: string | null;
  refusal: boolean;
  usage: Usage | null;
  costUsd: number | null;
  costSource: "endpoint" | "catalog" | "reservation" | "free" | null;
  latencyMs: number | null;
  flags: ReplayFlag[];
  error: { code: string; message: string } | null;
}

export interface Estimate {
  replayable: number;
  notReplayable: Partial<Record<NotReplayableReason, number>>;
  /** Expected spend, with a ×1.25 margin. */
  expectedUsd: number;
  /** Sum of per-call upper bounds: the most this run can spend. */
  upperBoundUsd: number;
  perArm: Record<ArmId, { expectedUsd: number; upperBoundUsd: number }>;
  unpriced: string[];
}

export interface RunFile {
  schema: "metergraph.replay-run/v1";
  createdAt: string;
  tool: { name: "metergraph-replay"; version: string };
  input: {
    file: string;
    adapter: string;
    /**
     * Optional: every call the input file held before sampling, and the
     * first and last timestamps among them. The report projects a monthly
     * cost from it (calls a day times 30); without it, no projection.
     */
    traffic?: { calls: number; from: string | null; to: string | null } | null;
  };
  catalog: { version: string };
  settings: {
    sample: number;
    trackedFields: string[];
    noiseFloor: boolean;
    budgetUsd: number;
    /** Calls a month the user said the workload makes, for the monthly cost projection. */
    monthlyCalls?: number | null;
  };
  treatments: Treatment[];
  /** Every sampled case, replayable or not. */
  cases: Case[];
  replays: Replay[];
  estimate: Estimate;
  spentUsd: number;
  /** Why the run ended early, or null when it finished. */
  stopped: null | "budget" | "interrupted" | "reservation_exceeded";
}
