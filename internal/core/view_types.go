package core

// The experiment view: the JSON shape every statistic in core reads, built
// here from a run file. Keys are snake_case because the shape is shared with
// Metergraph's hosted product and pinned by the fixtures in conformance/.
//
// A nil pointer is JSON null; a field the TypeScript version leaves out when
// it has nothing is `omitempty`.

import (
	"encoding/json"
)

// FieldKind is what a field's values are, which decides how arms are
// compared on it.
type FieldKind string

const (
	KindNumber   FieldKind = "number"
	KindList     FieldKind = "list"
	KindLabel    FieldKind = "label"
	KindText     FieldKind = "text"
	KindLongText FieldKind = "long_text"
	KindMixed    FieldKind = "mixed"
	KindEmpty    FieldKind = "empty"
)

// Summary is the built-in statistics for one arm or for the control.
type Summary struct {
	Cases                 int      `json:"cases"`
	Errors                int      `json:"errors"`
	CostPerCallUSD        *float64 `json:"cost_per_call_usd"`
	ModelCostPerCallUSD   *float64 `json:"model_cost_per_call_usd"`
	SearchCostPerCallUSD  *float64 `json:"search_cost_per_call_usd"`
	LatencyMsP50          *float64 `json:"latency_ms_p50"`
	OutputTokensP50       *float64 `json:"output_tokens_p50"`
	OutputCharsP50        *float64 `json:"output_chars_p50"`
	JSONParseRate         *float64 `json:"json_parse_rate"`
	SameKeysRate          *float64 `json:"same_keys_rate"`
	SameKeysCompared      *int     `json:"same_keys_compared"`
	EmptyOrRefusedRate    *float64 `json:"empty_or_refused_rate"`
	CutOffRate            *float64 `json:"cut_off_rate"`
	CachedInputShare      *float64 `json:"cached_input_share"`
	CachedInputCalls      int      `json:"cached_input_calls"`
	TemperatureDropped    int      `json:"temperature_dropped"`
	ResponseFormatDropped int      `json:"response_format_dropped"`
	// Set on an arm's summary only.
	TemperatureIgnoredWithReasoning *int `json:"temperature_ignored_with_reasoning,omitempty"`
}

// ResultView is one case's result on one arm, as the case browser reads it.
type ResultView struct {
	Output                          *string  `json:"output"`
	CostUSD                         *float64 `json:"cost_usd"`
	CostSearchUSD                   *float64 `json:"cost_search_usd"`
	SearchUnits                     *float64 `json:"search_units"`
	LatencyMs                       *float64 `json:"latency_ms"`
	FinishReason                    *string  `json:"finish_reason"`
	OutputTokens                    *float64 `json:"output_tokens"`
	Error                           *string  `json:"error"`
	TemperatureDropped              bool     `json:"temperature_dropped"`
	TemperatureIgnoredWithReasoning bool     `json:"temperature_ignored_with_reasoning"`
	ResponseFormatDropped           bool     `json:"response_format_dropped"`
	OutputTruncated                 bool     `json:"output_truncated"`
	// Whether the provider flagged a refusal. Local only: the conformance fixtures do not carry it.
	Refusal bool `json:"refusal"`
}

// CaseControlView is production's call on one case.
type CaseControlView struct {
	Output          *string  `json:"output"`
	CostUSD         *float64 `json:"cost_usd"`
	CostSearchUSD   *float64 `json:"cost_search_usd"`
	LatencyMs       *float64 `json:"latency_ms"`
	FinishReason    *string  `json:"finish_reason"`
	OutputTokens    *float64 `json:"output_tokens"`
	OutputTruncated bool     `json:"output_truncated"`
	// Whether production's reply was flagged a refusal. Local only: the conformance fixtures do not carry it.
	Refusal bool `json:"refusal"`
}

// CaseStatus is "replayed" or "not_replayable".
type CaseStatus = string

// CaseFloor is the noise floor's two replays of production's model on one case.
type CaseFloor struct {
	A1 *ResultView `json:"a1,omitempty"`
	A2 *ResultView `json:"a2,omitempty"`
}

// CaseView is one sampled case.
type CaseView struct {
	TraceID string `json:"trace_id"`
	// The case's own id in the run file.
	CaseID       string                `json:"case_id"`
	Status       CaseStatus            `json:"status"`
	Reason       *string               `json:"reason"`
	InputPreview *string               `json:"input_preview"`
	Limitations  []string              `json:"limitations"`
	Control      CaseControlView       `json:"control"`
	Arms         map[string]ResultView `json:"arms"`
	// The noise floor's two replays of production's model, when the run has a floor.
	NoiseFloor *CaseFloor `json:"noise_floor,omitempty"`
}

// ArmView is one treatment.
type ArmView struct {
	// The treatment's id in the run file ("t1", ...).
	ID    string `json:"id"`
	Label string `json:"label"`
	Model string `json:"model"`
	// The endpoint the treatment ran through.
	Provider                        string  `json:"provider"`
	Settings                        *Object `json:"settings"`
	TemperatureIgnoredWithReasoning bool    `json:"temperature_ignored_with_reasoning"`
	WebSearchFeesMissing            bool    `json:"web_search_fees_missing"`
	Summary                         Summary `json:"summary"`
	// The control over exactly the cases this arm returned on.
	ControlSummary Summary `json:"control_summary"`
}

// Difference is one case's A2 minus A1 on a number.
type Difference struct {
	TraceID    any     `json:"trace_id"`
	A1         float64 `json:"a1"`
	A2         float64 `json:"a2"`
	Difference float64 `json:"difference"`
}

// OverlapItem is one case's item overlap between A1's list and A2's.
type OverlapItem struct {
	TraceID any     `json:"trace_id"`
	Overlap float64 `json:"overlap"`
}

// Spread is how far A2 is from A1 on one statistic or field. Comparison is
// "difference", "match_rate", "overlap", or "" (none; JSON null); only that
// comparison's fields are written. A per-case list that is nil is left out
// (a view built without cases).
type Spread struct {
	Comparison string
	Pairs      int

	// "difference"
	MedianAbsDifference *float64
	P90AbsDifference    *float64
	MaxAbsDifference    *float64
	MinDifference       *float64
	MaxDifference       *float64
	Differences         []Difference

	// "match_rate"
	Matched    int
	MatchRate  *float64
	Mismatches []any

	// "overlap"
	MeanOverlap *float64
	MinOverlap  *float64
	Overlaps    []OverlapItem
}

func (s *Spread) object() *Object {
	o := NewObject()
	if s.Comparison == "" {
		o.Set("comparison", nil)
	} else {
		o.Set("comparison", s.Comparison)
	}
	o.Set("pairs", float64(s.Pairs))
	switch s.Comparison {
	case "difference":
		o.Set("median_abs_difference", toLoose(s.MedianAbsDifference))
		o.Set("p90_abs_difference", toLoose(s.P90AbsDifference))
		o.Set("max_abs_difference", toLoose(s.MaxAbsDifference))
		o.Set("min_difference", toLoose(s.MinDifference))
		o.Set("max_difference", toLoose(s.MaxDifference))
		if s.Differences != nil {
			o.Set("differences", s.Differences)
		}
	case "match_rate":
		o.Set("matched", float64(s.Matched))
		o.Set("match_rate", toLoose(s.MatchRate))
		if s.Mismatches != nil {
			o.Set("mismatches", s.Mismatches)
		}
	case "overlap":
		o.Set("mean_overlap", toLoose(s.MeanOverlap))
		o.Set("min_overlap", toLoose(s.MinOverlap))
		if s.Overlaps != nil {
			o.Set("overlaps", s.Overlaps)
		}
	}
	return o
}

// stripCases drops the per-case lists.
func (s *Spread) stripCases() {
	s.Differences = nil
	s.Mismatches = nil
	s.Overlaps = nil
}

// MarshalJSON writes the spread in the view's shape.
func (s Spread) MarshalJSON() ([]byte, error) { return s.object().MarshalJSON() }

// FloorFieldSpread is one tracked field on the noise floor: its kind, how
// many cases miss a value on either replay, the spread, and each replay's
// raw values aligned to the replayed cases (nil without cases).
type FloorFieldSpread struct {
	Spread
	Kind    FieldKind
	Missing int
	A1      []any
	A2      []any
}

// MarshalJSON writes the field in the view's shape.
func (f FloorFieldSpread) MarshalJSON() ([]byte, error) {
	o := obj("kind", string(f.Kind), "missing", float64(f.Missing))
	s := f.Spread.object()
	for _, k := range s.Keys() {
		v, _ := s.Get(k)
		o.Set(k, v)
	}
	if f.A1 != nil {
		o.Set("a1", f.A1)
	}
	if f.A2 != nil {
		o.Set("a2", f.A2)
	}
	return o.MarshalJSON()
}

// Built-in statistics the floor reads by their per-case difference.
var NoiseNumberStats = []string{"cost_usd", "latency_ms", "output_tokens", "output_chars"}

// Built-in statistics the floor reads by how often A1 and A2 agree.
var NoiseMatchStats = []string{"json_parse", "same_keys", "empty_or_refused", "cut_off", "output"}

// FloorErrors counts each replay's errors.
type FloorErrors struct {
	A1 int `json:"a1"`
	A2 int `json:"a2"`
}

// NoiseFloor is production's model against itself: A2 read against A1 on
// the same cases (the TypeScript NoiseFloorView type).
type NoiseFloor struct {
	Replays       *float64 `json:"replays"`
	GatewayModels []string `json:"gateway_models"`
	// Cases where both replays came back: what every spread is over.
	Cases  int         `json:"cases"`
	Errors FloorErrors `json:"errors"`
	A1     Summary     `json:"a1"`
	A2     Summary     `json:"a2"`
	// A2 as an arm against A1 as its control, over the paired cases.
	Summary        Summary                      `json:"summary"`
	ControlSummary Summary                      `json:"control_summary"`
	SpentUSD       float64                      `json:"spent_usd"`
	Stats          map[string]*Spread           `json:"stats"`
	Fields         map[string]*FloorFieldSpread `json:"fields"`
}

// Sample counts a run's cases.
type Sample struct {
	Size          int `json:"size"`
	Cases         int `json:"cases"`
	Replayed      int `json:"replayed"`
	NotReplayable int `json:"not_replayable"`
}

// ControlView is the control column: production's model and its summary.
type ControlView struct {
	Model        *string `json:"model"`
	Provider     *string `json:"provider"`
	SettingsSeen *Object `json:"settings_seen"`
	ModelsSeen   *Object `json:"models_seen"`
	Summary      Summary `json:"summary"`
}

// ExperimentView is the view of one run.
type ExperimentView struct {
	// Always "done": a run file is written when the run ends. Stopped says why it ended early.
	Status        string      `json:"status"`
	Stopped       *string     `json:"stopped"`
	Sample        Sample      `json:"sample"`
	TrackedFields []string    `json:"tracked_fields"`
	EstimateUSD   float64     `json:"estimate_usd"`
	BudgetUSD     float64     `json:"budget_usd"`
	SpentUSD      float64     `json:"spent_usd"`
	CreatedAt     string      `json:"created_at"`
	Control       ControlView `json:"control"`
	Arms          []ArmView   `json:"arms"`
	// Each tracked field's values per replayed case: "control" and one series per arm label.
	Fields map[string]map[string][]any `json:"fields"`
	Cases  []CaseView                  `json:"cases"`
	// Always null: replay has no judge.
	Judge      any         `json:"judge"`
	NoiseFloor *NoiseFloor `json:"noise_floor"`
	// Always null: replay computes no similarity.
	Similarity any `json:"similarity"`
}

var _ json.Marshaler = Spread{}
