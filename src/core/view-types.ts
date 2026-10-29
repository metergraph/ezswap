// The experiment view: the JSON shape every statistic in core reads, built
// here from a run file. Keys are snake_case because the shape is shared with
// Metergraph's hosted product and pinned by the fixtures in conformance/.

/** What the statistics need from one output, taken from its full text. */
export interface Facts {
  json_ok: boolean;
  /** Sorted top-level keys of a JSON object output, else null. */
  keys: string[] | null;
  /** Output length in code points. */
  chars: number;
  /** Each tracked field's value, null when missing or too large to keep. */
  fields: Record<string, unknown>;
  /** The keys or fields were cut to stay within bounds. */
  truncated: boolean;
  keys_complete?: boolean;
}

/**
 * One call's result as the statistics read it: a treatment's or noise-floor
 * replay, or the control (production's recorded call). Every field is
 * optional and read with a default when absent.
 */
export interface Outcome {
  output?: string | null;
  /** An error code when the call failed; the call is then counted, not averaged in. */
  error?: string | null;
  cost_usd?: number | null;
  /** The part of cost_usd that was search fees, when known. */
  cost_search_usd?: number | null;
  search_units?: number | null;
  latency_ms?: number | null;
  output_tokens?: number | null;
  input_tokens?: number | null;
  cache_read_tokens?: number | null;
  /**
   * Whether input_tokens already counts the cached tokens. Absent means true
   * (the OpenAI convention replies follow); null means unknown.
   */
  input_includes_cache_read?: boolean | null;
  finish_reason?: string | null;
  refusal?: boolean | null;
  facts?: Facts | null;
  temperature_dropped?: boolean | null;
  response_format_dropped?: boolean | null;
  /** The stored output is a prefix of the reply. */
  output_truncated?: boolean | null;
}

/** A control as stored on a case: production's call. */
export interface ControlRecord extends Outcome {
  model?: string | null;
  provider?: string | null;
  settings?: Record<string, unknown> | null;
}

/** Built-in statistics for one arm or for the control. */
export interface Summary {
  cases: number;
  errors: number;
  cost_per_call_usd: number | null;
  model_cost_per_call_usd: number | null;
  search_cost_per_call_usd: number | null;
  latency_ms_p50: number | null;
  output_tokens_p50: number | null;
  output_chars_p50: number | null;
  json_parse_rate: number | null;
  same_keys_rate: number | null;
  same_keys_compared: number | null;
  empty_or_refused_rate: number | null;
  cut_off_rate: number | null;
  cached_input_share: number | null;
  cached_input_calls: number;
  temperature_dropped: number;
  response_format_dropped: number;
  temperature_ignored_with_reasoning?: number;
}

/** One case's result on one arm, as the case browser reads it. */
export interface ResultView {
  output: string | null;
  cost_usd: number | null;
  cost_search_usd: number | null;
  search_units: number | null;
  latency_ms: number | null;
  finish_reason: string | null;
  output_tokens: number | null;
  error: string | null;
  temperature_dropped: boolean;
  temperature_ignored_with_reasoning: boolean;
  response_format_dropped: boolean;
  output_truncated: boolean;
  /** Whether the provider flagged a refusal. Local only: the conformance fixtures do not carry it. */
  refusal: boolean;
}

export interface CaseControlView {
  output: string | null;
  cost_usd: number | null;
  cost_search_usd: number | null;
  latency_ms: number | null;
  finish_reason: string | null;
  output_tokens: number | null;
  output_truncated: boolean;
  /** Whether production's reply was flagged a refusal. Local only: the conformance fixtures do not carry it. */
  refusal: boolean;
}

export type CaseStatus = "replayed" | "not_replayable";

export interface CaseView {
  trace_id: string;
  /** The case's own id in the run file. */
  case_id: string;
  status: CaseStatus;
  reason: string | null;
  input_preview: string | null;
  limitations: string[];
  control: CaseControlView;
  arms: Record<string, ResultView>;
  /** The noise floor's two replays of production's model, when the run has a floor. */
  noise_floor?: { a1?: ResultView; a2?: ResultView };
}

export interface ArmView {
  /** The treatment's id in the run file ("t1", ...). */
  id: string;
  label: string;
  model: string;
  /** The endpoint the treatment ran through. */
  provider: string;
  settings: Record<string, unknown>;
  temperature_ignored_with_reasoning: boolean;
  web_search_fees_missing: boolean;
  summary: Summary;
  /** The control over exactly the cases this arm returned on. */
  control_summary: Summary;
}

export interface DifferenceSpread {
  comparison: "difference";
  pairs: number;
  median_abs_difference: number | null;
  p90_abs_difference: number | null;
  max_abs_difference: number | null;
  min_difference: number | null;
  max_difference: number | null;
  differences?: { trace_id: string; a1: number; a2: number; difference: number }[];
}

export interface MatchSpread {
  comparison: "match_rate";
  pairs: number;
  matched: number;
  match_rate: number | null;
  mismatches?: string[];
}

export interface OverlapSpread {
  comparison: "overlap";
  pairs: number;
  mean_overlap: number | null;
  min_overlap: number | null;
  overlaps?: { trace_id: string; overlap: number }[];
}

export interface NoComparison {
  comparison: null;
  pairs: number;
}

export type Spread = DifferenceSpread | MatchSpread | OverlapSpread | NoComparison;

export type FieldKind = "number" | "list" | "label" | "text" | "long_text" | "mixed" | "empty";

export type FloorField = Spread & {
  kind: FieldKind;
  missing: number;
  /** Each replay's raw values, aligned to the replayed cases; left out without cases. */
  a1?: unknown[];
  a2?: unknown[];
};

/** Built-in statistics the floor reads by their per-case difference. */
export type NoiseNumberStat = "cost_usd" | "latency_ms" | "output_tokens" | "output_chars";
/** Built-in statistics the floor reads by how often A1 and A2 agree. */
export type NoiseMatchStat = "json_parse" | "same_keys" | "empty_or_refused" | "cut_off" | "output";
export type NoiseStat = NoiseNumberStat | NoiseMatchStat;

/** Production's model against itself: A2 read against A1 on the same cases. */
export interface NoiseFloorView {
  replays: number | null;
  gateway_models: string[];
  /** Cases where both replays came back: what every spread is over. */
  cases: number;
  errors: { a1: number; a2: number };
  a1: Summary;
  a2: Summary;
  /** A2 as an arm against A1 as its control, over the paired cases. */
  summary: Summary;
  control_summary: Summary;
  spent_usd: number;
  stats: Record<NoiseNumberStat, DifferenceSpread> & Record<NoiseMatchStat, MatchSpread>;
  fields: Record<string, FloorField>;
}

export interface ExperimentView {
  /** Always "done": a run file is written when the run ends. `stopped` says why it ended early. */
  status: "done";
  stopped: null | "budget" | "interrupted" | "reservation_exceeded";
  sample: { size: number; cases: number; replayed: number; not_replayable: number };
  tracked_fields: string[];
  estimate_usd: number;
  budget_usd: number;
  spent_usd: number;
  created_at: string;
  control: {
    model: string | null;
    provider: string | null;
    settings_seen: Record<string, unknown[]>;
    models_seen: Record<string, number>;
    summary: Summary;
  };
  arms: ArmView[];
  /** Each tracked field's values per replayed case: `control` and one series per arm label. */
  fields: Record<string, Record<string, unknown[]>> | null;
  cases: CaseView[] | null;
  /** Always null: replay has no judge. */
  judge: null;
  noise_floor: NoiseFloorView | null;
  /** Always null: replay computes no similarity. */
  similarity: null;
}
