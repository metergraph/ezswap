// Package types holds the shared contracts. Every package builds against
// these, so a change here is a change to every package: keep it small and
// additive.
//
// Flow: an adapter reads a trace file into []CapturedCall; cases turns each
// into a Case (replayable or with a reason it is not); the runner sends a
// ReplayCall per case per arm through a Provider and records a Replay; the
// whole run is one RunFile (run.json); core reads a RunFile into the
// statistics view; report renders it.
//
// JSON field names match run.json as earlier versions (written in
// TypeScript) wrote it, so run files stay readable across versions. A nil
// pointer is JSON null.
package types

import (
	"context"
	"encoding/json"
)

// ---------------------------------------------------------------------------
// Captured calls (adapter output)

// CapturedCall is one production LLM call as an adapter read it, before any
// replay rule.
type CapturedCall struct {
	// Stable within the file: the source's own id, else a hash of the line.
	ID      string  `json:"id"`
	TraceID *string `json:"traceId"`
	// ISO 8601 start time, when the source has one.
	TS *string `json:"ts"`
	// Adapter that read it: "ezswap-jsonl" | "raw" | "langfuse" | "otel".
	Source string `json:"source"`
	// "openai" | "anthropic" | "google" | ... as captured, lower case.
	Provider *string `json:"provider"`
	Model    *string `json:"model"`
	// The request body in the provider's own shape (OpenAI chat or
	// Responses, Anthropic Messages, Gemini), or the replay JSONL request, as
	// decoded JSON. Objects may be ordered (see cases/jsval) so that schemas
	// and tool definitions keep production's key order; the value must have
	// a JSON form.
	Request  any              `json:"request"`
	Response CapturedResponse `json:"response"`
	// True when the source says or shows content was cut.
	Truncated bool `json:"truncated"`
	// Field names the source did not carry, e.g. ["tools", "response_format"].
	Missing []string `json:"missing"`
	// The name the developer gave the place in the app that made this call,
	// when the source carries one. Never guessed.
	Part *string `json:"part,omitempty"`
}

type CapturedResponse struct {
	// Assistant text; nil when the source carried none.
	Text *string `json:"text"`
	// Provider-shaped tool calls, when any. Objects may be ordered, as in
	// CapturedCall.Request.
	ToolCalls    []any   `json:"toolCalls"`
	FinishReason *string `json:"finishReason"`
	Refusal      bool    `json:"refusal"`
	Usage        *Usage  `json:"usage"`
	// What production paid for this call, when the source records it.
	CostUSD   *float64 `json:"costUsd"`
	LatencyMs *float64 `json:"latencyMs"`
}

type Usage struct {
	InputTokens  *float64 `json:"inputTokens"`
	OutputTokens *float64 `json:"outputTokens"`
	// Cache-read input tokens, whether or not InputTokens includes them.
	CachedInputTokens *float64 `json:"cachedInputTokens"`
	ReasoningTokens   *float64 `json:"reasoningTokens"`
}

// ---------------------------------------------------------------------------
// Cases (cases package output)

type Message struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content string `json:"content"`
}

type ForcedTool struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	// JSON Schema of the tool's arguments, as production sent it: raw JSON
	// so the provider receives the properties in production's order.
	Parameters json.RawMessage `json:"parameters"`
}

// CallParams are the production settings a replay can carry.
type CallParams struct {
	Temperature     *float64 `json:"temperature"`
	MaxTokens       *float64 `json:"maxTokens"`
	TopP            *float64 `json:"topP"`
	Stop            []string `json:"stop"`
	ReasoningEffort *string  `json:"reasoningEffort"`
}

type NotReplayableReason string

const (
	ContentUnavailable        NotReplayableReason = "content_unavailable"
	ContentTruncated          NotReplayableReason = "content_truncated"
	ToolHistory               NotReplayableReason = "tool_history"
	UnsupportedContent        NotReplayableReason = "unsupported_content"
	NoMessages                NotReplayableReason = "no_messages"
	LastTurnNotUser           NotReplayableReason = "last_turn_not_user"
	InputTooLarge             NotReplayableReason = "input_too_large"
	ToolDefinitionsNotOffered NotReplayableReason = "tool_definitions_not_offered"
	WebSearchNotReplayed      NotReplayableReason = "web_search_not_replayed"
	UnparseableRequest        NotReplayableReason = "unparseable_request"
)

type Case struct {
	ID       string  `json:"id"`
	TraceID  *string `json:"traceId"`
	TS       *string `json:"ts"`
	Source   string  `json:"source"`
	Provider *string `json:"provider"`
	// Production's model as captured.
	Model *string `json:"model"`
	// Folded system and developer turns, or nil.
	System *string `json:"system"`
	// Text turns, same-role turns merged; the last one is from the user.
	Messages []Message `json:"messages"`
	// OpenAI chat `response_format`, translated from any captured shape, as
	// raw JSON in production's key order (models write fields in schema
	// order). Nil, written as null, when there is none.
	ResponseFormat json.RawMessage `json:"responseFormat"`
	ForcedTool     *ForcedTool     `json:"forcedTool"`
	Params         CallParams      `json:"params"`
	// Did production use web search on this call?
	WebSearch bool `json:"webSearch"`
	// Production's output: text, or a single tool call's JSON arguments.
	Production CaseOutput `json:"production"`
	// Short preview of the last user turn, for the case browser.
	InputPreview *string  `json:"inputPreview"`
	Fidelity     string   `json:"fidelity"` // "full" | "partial"
	Missing      []string `json:"missing"`
	// Nil when replayable.
	NotReplayable *NotReplayableReason `json:"notReplayable"`
	// The explicit part name, copied from the call.
	Part *string `json:"part,omitempty"`
	// Set by a map run on each replayable case: the part's key ("name:..." or
	// "fp1:...") and the name it had when the run was made.
	PartKey  string `json:"partKey,omitempty"`
	PartName string `json:"partName,omitempty"`
}

type CaseOutput struct {
	Output       *string  `json:"output"`
	FinishReason *string  `json:"finishReason"`
	Refusal      bool     `json:"refusal"`
	Usage        *Usage   `json:"usage"`
	CostUSD      *float64 `json:"costUsd"`
	LatencyMs    *float64 `json:"latencyMs"`
}

// ---------------------------------------------------------------------------
// Providers

// ReplayCall is one request the runner sends. Everything a provider needs,
// nothing else.
type ReplayCall struct {
	// Model id as the endpoint names it, e.g. "openai/gpt-5-mini".
	Model           string          `json:"model"`
	System          *string         `json:"system"`
	Messages        []Message       `json:"messages"`
	MaxTokens       float64         `json:"maxTokens"`
	Temperature     *float64        `json:"temperature"`
	TopP            *float64        `json:"topP"`
	Stop            []string        `json:"stop"`
	ReasoningEffort *string         `json:"reasoningEffort"`
	ResponseFormat  json.RawMessage `json:"responseFormat"`
	ForcedTool      *ForcedTool     `json:"forcedTool"`
}

type ReplayFlag string

const (
	FlagTemperatureDropped    ReplayFlag = "temperature_dropped"
	FlagResponseFormatDropped ReplayFlag = "response_format_dropped"
	FlagMaybeBilled           ReplayFlag = "maybe_billed"
	FlagRetried               ReplayFlag = "retried"
)

type ReplayError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ReplyOutcome struct {
	Status string `json:"status"` // "ok" | "failed"
	// Text, or the first tool call's arguments when the text is empty.
	Output       *string `json:"output"`
	FinishReason *string `json:"finishReason"`
	Refusal      bool    `json:"refusal"`
	Usage        *Usage  `json:"usage"`
	// Amount the endpoint says it charged, when it reports one.
	ReportedCostUSD *float64 `json:"reportedCostUsd"`
	// Wall time of the request that answered.
	LatencyMs *float64     `json:"latencyMs"`
	Flags     []ReplayFlag `json:"flags"`
	Error     *ReplayError `json:"error"`
}

type Provider interface {
	// Preset or endpoint name, shown in the report.
	Name() string
	Complete(ctx context.Context, call ReplayCall) ReplyOutcome
}

// ---------------------------------------------------------------------------
// Pricing

type PriceQuery struct {
	// "creator/model" or any alias the catalog knows.
	Model             string
	InputTokens       float64
	OutputTokens      float64
	CachedInputTokens *float64
	// Does InputTokens already include CachedInputTokens? Nil: provider rule.
	InputIncludesCacheRead *bool
	// ISO date the price applies at; empty means now.
	At string
	// Price at the full rate, ignoring off-peak discounts: for an upper
	// bound, which must hold whenever the call is billed.
	FullRate bool
}

type Pricing interface {
	// Catalog source commit or version, for the report footer.
	CatalogVersion() string
	// Nil when the catalog cannot price this model.
	Price(q PriceQuery) *float64
	// Can the catalog price this model at all?
	Knows(model string) bool
}

// ---------------------------------------------------------------------------
// The run (run.json)

type TreatmentSettings struct {
	Temperature     *float64 `json:"temperature"`
	MaxTokens       *float64 `json:"maxTokens"`
	ReasoningEffort *string  `json:"reasoningEffort"`
}

type Treatment struct {
	// "t1", "t2", ... in the order given.
	ID    string `json:"id"`
	Label string `json:"label"`
	Model string `json:"model"`
	// Endpoint preset or base URL host, never a key.
	Endpoint string            `json:"endpoint"`
	Settings TreatmentSettings `json:"settings"`
}

// Arm ids: a treatment id, or one of the two noise-floor replays. "control"
// never appears: production's recorded output is the control.
const (
	ArmFloorA1 = "floor_a1"
	ArmFloorA2 = "floor_a2"
)

type Replay struct {
	CaseID       string   `json:"caseId"`
	Arm          string   `json:"arm"`
	Status       string   `json:"status"` // "ok" | "failed" | "skipped"
	Output       *string  `json:"output"`
	FinishReason *string  `json:"finishReason"`
	Refusal      bool     `json:"refusal"`
	Usage        *Usage   `json:"usage"`
	CostUSD      *float64 `json:"costUsd"`
	// "endpoint" | "catalog" | "reservation" | "free", or nil.
	CostSource *string      `json:"costSource"`
	LatencyMs  *float64     `json:"latencyMs"`
	Flags      []ReplayFlag `json:"flags"`
	Error      *ReplayError `json:"error"`
}

type ArmEstimate struct {
	ExpectedUSD   float64 `json:"expectedUsd"`
	UpperBoundUSD float64 `json:"upperBoundUsd"`
}

type Estimate struct {
	Replayable    int            `json:"replayable"`
	NotReplayable map[string]int `json:"notReplayable"`
	// Expected spend, with a x1.25 margin.
	ExpectedUSD float64 `json:"expectedUsd"`
	// Sum of per-call upper bounds: the most this run can spend.
	UpperBoundUSD float64                `json:"upperBoundUsd"`
	PerArm        map[string]ArmEstimate `json:"perArm"`
	Unpriced      []string               `json:"unpriced"`
}

type Traffic struct {
	Calls int     `json:"calls"`
	From  *string `json:"from"`
	To    *string `json:"to"`
}

type RunInput struct {
	File    string `json:"file"`
	Adapter string `json:"adapter"`
	// Optional: every call the input file held before sampling, and the
	// first and last timestamps among them. The report projects a monthly
	// cost from it (calls a day times 30); without it, no projection.
	Traffic *Traffic `json:"traffic,omitempty"`
}

type RunSettings struct {
	Sample        int      `json:"sample"`
	TrackedFields []string `json:"trackedFields"`
	NoiseFloor    bool     `json:"noiseFloor"`
	BudgetUSD     float64  `json:"budgetUsd"`
	// Calls a month the user said the workload makes, for the monthly cost
	// projection.
	MonthlyCalls *float64 `json:"monthlyCalls,omitempty"`
	// Set for a map run: cases sampled per part. Sample then holds the same
	// value.
	PerPart *int `json:"perPart,omitempty"`
	// Paths left out of a map's comparison.
	IgnoredFields []string `json:"ignoredFields,omitempty"`
}

type Tool struct {
	Name    string `json:"name"` // always "ezswap"
	Version string `json:"version"`
}

type Catalog struct {
	Version string `json:"version"`
}

const RunSchema = "metergraph.replay-run/v1"

type RunFile struct {
	Schema     string      `json:"schema"`
	CreatedAt  string      `json:"createdAt"`
	Tool       Tool        `json:"tool"`
	Input      RunInput    `json:"input"`
	Catalog    Catalog     `json:"catalog"`
	Settings   RunSettings `json:"settings"`
	Treatments []Treatment `json:"treatments"`
	// Every sampled case, replayable or not.
	Cases    []Case   `json:"cases"`
	Replays  []Replay `json:"replays"`
	Estimate Estimate `json:"estimate"`
	SpentUSD float64  `json:"spentUsd"`
	// Why the run ended early ("budget" | "interrupted" |
	// "reservation_exceeded"), or nil when it finished.
	Stopped *string `json:"stopped"`
	// What a judge said about free-text replies, when the map asked one.
	Judge *JudgeRecord `json:"judge,omitempty"`
	// Models whose calls cost more than the price catalog said, so the run
	// raised their later reservations.
	Repriced []Repricing `json:"repriced,omitempty"`
}

// Repricing is a model whose billed cost exceeded its reservation.
type Repricing struct {
	Model    string `json:"model"`
	Endpoint string `json:"endpoint"`
	// What its later reservations were multiplied by.
	Factor float64 `json:"factor"`
}

// JudgeRecord is a judge's verdicts on the free-text replies of a map run.
type JudgeRecord struct {
	Model    string  `json:"model"`
	Question string  `json:"question"`
	CostUSD  float64 `json:"costUsd"`
	// One per pair judged: a reply against the one it is compared with.
	Judgements []Judgement `json:"judgements"`
}

// Judgement is the verdict on one reply against another.
type Judgement struct {
	CaseID string `json:"caseId"`
	// The reply judged: a treatment id, ArmFloorA1 or ArmFloorA2.
	Arm string `json:"arm"`
	// What it is compared with: JudgedAgainstRecording or ArmFloorA1.
	Against string `json:"against"`
	// The mean probability, over both orders, that the two differ.
	PDifferent float64 `json:"pDifferent"`
	// The two orders fell on different sides of one half.
	Unsure bool `json:"unsure"`
}

// JudgedAgainstRecording is a Judgement against the recorded answer.
const JudgedAgainstRecording = "recording"
