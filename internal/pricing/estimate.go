package pricing

// What a run is expected to cost, and the most it can spend.
//
// The estimate matches Metergraph's hosted figures (conformance/estimate.json):
// each replayable case priced at
// production's input count (or 2000) and an assumed output (the larger of
// production's output, or 512, and half the cap the replay is sent with, never
// above that cap), times a 1.25 margin, rounded up to $0.0001 per arm. The
// noise floor adds two replays per case on production's model at production's
// settings.
//
// The upper bound is what each call reserves before it is sent: one token per
// UTF-8 byte of everything sent (system, messages, the response_format and the
// forced tool, as JSON), framing overhead per message, and the full max_tokens,
// since reasoning bills as output inside that cap. Production's own input
// count, from another tokenizer, is only ever a floor.

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/metergraph/replay/internal/jsnum"
	"github.com/metergraph/replay/internal/providers"
	"github.com/metergraph/replay/internal/types"
)

const (
	DefaultMaxTokens            = 4096
	FallbackInputTokens         = 2000
	FallbackOutputTokens        = 512
	EstimateMargin              = "1.25"
	NoiseReplays                = 2
	ReservationTokensPerMessage = 16
	ReservationBaseTokens       = 64
	estimatePlaces              = 4
)

type EstimateOptions struct {
	NoiseFloor bool
	// ISO time to price at. Empty: now.
	At string
	// A treatment that costs nothing. Default: the Ollama preset.
	IsFree func(treatment types.Treatment) bool
}

// orElse is `value || fallback` for an optional count: nil, 0 and NaN fall through.
func orElse(value *float64, fallback float64) float64 {
	if value == nil || *value == 0 || math.IsNaN(*value) {
		return fallback
	}
	return *value
}

// CapSent is the max_tokens a replay is sent with: the treatment's, else production's cap, else 4096.
func CapSent(treatmentMaxTokens, productionMaxTokens *float64) float64 {
	return orElse(treatmentMaxTokens, orElse(productionMaxTokens, DefaultMaxTokens))
}

// AssumedOutputTokens is the output an estimate assumes for one replay: the
// larger of production's output and half the cap, never above the cap. A
// treatment often writes more than production did (a reasoning model thinks,
// and that bills as output).
func AssumedOutputTokens(cap float64, productionOutputTokens *float64) float64 {
	control := orElse(productionOutputTokens, FallbackOutputTokens)
	return math.Min(math.Max(control, math.Floor(cap/2)), cap)
}

func firstNonNil[T any](values ...*T) *T {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

// TreatmentCall is the call a treatment sends for one case.
func TreatmentCall(item types.Case, treatment types.Treatment) types.ReplayCall {
	return types.ReplayCall{
		Model:           treatment.Model,
		System:          item.System,
		Messages:        item.Messages,
		MaxTokens:       CapSent(treatment.Settings.MaxTokens, item.Params.MaxTokens),
		Temperature:     firstNonNil(treatment.Settings.Temperature, item.Params.Temperature),
		TopP:            item.Params.TopP,
		Stop:            item.Params.Stop,
		ReasoningEffort: firstNonNil(treatment.Settings.ReasoningEffort, item.Params.ReasoningEffort),
		ResponseFormat:  item.ResponseFormat,
		ForcedTool:      item.ForcedTool,
	}
}

// FloorCall is a noise-floor call: production's model at production's settings.
func FloorCall(item types.Case, model string) types.ReplayCall {
	return types.ReplayCall{
		Model:           model,
		System:          item.System,
		Messages:        item.Messages,
		MaxTokens:       CapSent(nil, item.Params.MaxTokens),
		Temperature:     item.Params.Temperature,
		TopP:            item.Params.TopP,
		Stop:            item.Params.Stop,
		ReasoningEffort: item.Params.ReasoningEffort,
		ResponseFormat:  item.ResponseFormat,
		ForcedTool:      item.ForcedTool,
	}
}

// ProductionResolver is a Pricing that can resolve production's model, as
// CatalogPricing does.
type ProductionResolver interface {
	ResolveProduction(provider, model *string, at string) *string
}

// FloorModel is the model id the noise floor replays production on: the
// catalog's canonical id when it can resolve production's model, else the
// model as captured (with its provider as creator when it has no slash). Nil
// without a model. An empty at means now.
func FloorModel(item types.Case, pricing types.Pricing, at string) *string {
	if resolver, ok := pricing.(ProductionResolver); ok {
		if resolved := resolver.ResolveProduction(item.Provider, item.Model, at); resolved != nil {
			return resolved
		}
	}
	if item.Model == nil {
		return nil
	}
	model := jsTrim(*item.Model)
	if model == "" {
		return nil
	}
	if strings.Contains(model, "/") || item.Provider == nil || *item.Provider == "" {
		return &model
	}
	out := strings.ToLower(jsTrim(*item.Provider)) + "/" + model
	return &out
}

// ---------------------------------------------------------------------------
// JSON measured as Python's json.dumps writes it by default (", " and ": "
// separators, non-ASCII escaped), so a bound matches Metergraph's hosted
// figures (conformance/estimate.json).

func pyString(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, code := range utf16.Encode([]rune(value)) {
		switch code {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		default:
			if code < 0x20 || code > 0x7e {
				if code == 0x7f {
					out.WriteByte(0x7f)
				} else {
					fmt.Fprintf(&out, `\u%04x`, code)
				}
			} else {
				out.WriteByte(byte(code))
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}

func pyNumber(value float64) string {
	if math.IsInf(value, 0) || math.IsNaN(value) {
		switch {
		case value > 0:
			return "Infinity"
		case value < 0:
			return "-Infinity"
		}
		return "NaN"
	}
	if jsnum.IsInteger(value) && math.Abs(value) < 1e16 {
		return jsnum.String(value)
	}
	magnitude := math.Abs(value)
	if magnitude >= 1e16 || magnitude < 1e-4 {
		mantissa, exponent, found := strings.Cut(jsnum.ToExponentialShortest(value), "e")
		if !found {
			exponent = "0"
		}
		sign := "+"
		if strings.HasPrefix(exponent, "-") {
			sign = "-"
		}
		digits := strings.TrimLeft(exponent, "+-")
		if len(digits) < 2 {
			digits = strings.Repeat("0", 2-len(digits)) + digits
		}
		return mantissa + "e" + sign + digits
	}
	return jsnum.String(value)
}

// PythonJSON is value as Python's json.dumps writes it by default. Raw JSON
// (json.RawMessage) keeps its own key order, as the TypeScript's parsed
// object does. A Go map has no key order, so its keys come out sorted; the
// bound measures only the length, which order does not change.
func PythonJSON(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case bool:
		if v {
			return "true"
		}
		return "false"
	case float64:
		return pyNumber(v)
	case int:
		return pyNumber(float64(v))
	case string:
		return pyString(v)
	case []any:
		if v == nil {
			return "null"
		}
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = PythonJSON(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []string:
		if v == nil {
			return "null"
		}
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = pyString(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case json.RawMessage:
		// Raw JSON, read as JSON.parse reads it: key order and duplicate keys
		// as the source has them.
		if len(strings.TrimSpace(string(v))) == 0 {
			return "null"
		}
		decoded, err := providers.DecodeOrdered(v)
		if err != nil {
			return pyString(string(v))
		}
		return PythonJSON(decoded)
	case *providers.OrderedObject:
		if v == nil {
			return "null"
		}
		keys := v.JSKeys()
		parts := make([]string, len(keys))
		for i, key := range keys {
			parts[i] = pyString(key) + ": " + PythonJSON(v.Values[key])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case map[string]any:
		if v == nil {
			return "null"
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, key := range keys {
			parts[i] = pyString(key) + ": " + PythonJSON(v[key])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		// Any other Go value: as its JSON decodes.
		data, err := json.Marshal(v)
		if err != nil {
			return pyString(fmt.Sprint(v))
		}
		var decoded any
		if json.Unmarshal(data, &decoded) != nil {
			return pyString(fmt.Sprint(v))
		}
		return PythonJSON(decoded)
	}
}

// ReservationInputTokens is a true upper bound on the input tokens of the
// request this call sends.
func ReservationInputTokens(call types.ReplayCall, productionInputTokens *float64) float64 {
	bytes := 0
	if call.System != nil {
		bytes += len(*call.System)
	}
	for _, message := range call.Messages {
		bytes += len(message.Content)
	}
	if providers.HasJSON(call.ResponseFormat) {
		bytes += len(PythonJSON(call.ResponseFormat))
	}
	if call.ForcedTool != nil {
		forced := providers.ForcedToolRequest(*call.ForcedTool)
		bytes += len(PythonJSON(map[string]any{"tool": forced.Tool, "tool_choice": forced.ToolChoice}))
	}
	bound := float64(bytes + ReservationTokensPerMessage*(len(call.Messages)+1) + ReservationBaseTokens)
	production := 0.0
	if productionInputTokens != nil {
		production = *productionInputTokens
	}
	return math.Max(bound, production)
}

// UpperBoundUSD is what one call reserves before it is sent: the input bound
// and the full max_tokens, priced. Nil when the catalog cannot price the
// model; such a call cannot be budgeted. An empty at means now.
func UpperBoundUSD(call types.ReplayCall, pricing types.Pricing, productionInputTokens *float64, at string) *float64 {
	return pricing.Price(types.PriceQuery{
		Model:        call.Model,
		InputTokens:  ReservationInputTokens(call, productionInputTokens),
		OutputTokens: call.MaxTokens,
		At:           at,
	})
}

// ---------------------------------------------------------------------------
// The run's estimate

var margin = *Dec(EstimateMargin)

// withMargin is costs in 1e-8 units, times the margin, rounded up to $0.0001, in 1e-8 units.
func withMargin(units *big.Int) *big.Int {
	scaled := Mul(Decimal{N: units, S: costPlaces}, margin)
	return new(big.Int).Mul(UnitsUp(scaled, estimatePlaces), pow10(costPlaces-estimatePlaces))
}

func usd(units *big.Int) float64 {
	return UnitsToNumber(units, costPlaces)
}

func EstimateRun(cases []types.Case, treatments []types.Treatment, options EstimateOptions, pricing types.Pricing) types.Estimate {
	isFree := options.IsFree
	if isFree == nil {
		isFree = func(treatment types.Treatment) bool { return providers.IsFreeEndpoint(treatment.Endpoint) }
	}
	at := options.At
	var replayable []types.Case
	notReplayable := map[string]int{}
	for _, item := range cases {
		if item.NotReplayable == nil {
			replayable = append(replayable, item)
		} else {
			notReplayable[string(*item.NotReplayable)]++
		}
	}
	unpriced := map[string]bool{}
	perArm := map[string]types.ArmEstimate{}
	expected := new(big.Int)
	upper := new(big.Int)

	priceUnits := func(model string, inputTokens, outputTokens float64) *big.Int {
		cost := pricing.Price(types.PriceQuery{Model: model, InputTokens: inputTokens, OutputTokens: outputTokens, At: at})
		if cost == nil {
			return nil
		}
		return NumberToUnits(*cost, costPlaces)
	}
	boundUnits := func(call types.ReplayCall, productionInput *float64) *big.Int {
		bound := UpperBoundUSD(call, pricing, productionInput, at)
		if bound == nil {
			return nil
		}
		return NumberToUnits(*bound, costPlaces)
	}
	inputOf := func(usage *types.Usage) *float64 {
		if usage == nil {
			return nil
		}
		return usage.InputTokens
	}
	outputOf := func(usage *types.Usage) *float64 {
		if usage == nil {
			return nil
		}
		return usage.OutputTokens
	}

	for _, treatment := range treatments {
		armExpected := new(big.Int)
		armUpper := new(big.Int)
		if !isFree(treatment) {
			for _, item := range replayable {
				usage := item.Production.Usage
				call := TreatmentCall(item, treatment)
				cost := priceUnits(
					treatment.Model,
					orElse(inputOf(usage), FallbackInputTokens),
					AssumedOutputTokens(call.MaxTokens, outputOf(usage)),
				)
				bound := boundUnits(call, inputOf(usage))
				if cost == nil || bound == nil {
					unpriced[treatment.Model] = true
					continue
				}
				armExpected.Add(armExpected, cost)
				armUpper.Add(armUpper, bound)
			}
			armExpected = withMargin(armExpected)
		}
		perArm[treatment.ID] = types.ArmEstimate{ExpectedUSD: usd(armExpected), UpperBoundUSD: usd(armUpper)}
		expected.Add(expected, armExpected)
		upper.Add(upper, armUpper)
	}

	if options.NoiseFloor {
		floorExpected := new(big.Int)
		floorUpper := new(big.Int)
		for _, item := range replayable {
			model := FloorModel(item, pricing, at)
			if model == nil {
				unpriced["(production model not recorded)"] = true
				continue
			}
			usage := item.Production.Usage
			call := FloorCall(item, *model)
			cost := priceUnits(
				*model,
				orElse(inputOf(usage), FallbackInputTokens),
				AssumedOutputTokens(call.MaxTokens, outputOf(usage)),
			)
			bound := boundUnits(call, inputOf(usage))
			if cost == nil || bound == nil {
				unpriced[*model] = true
				continue
			}
			floorExpected.Add(floorExpected, new(big.Int).Mul(cost, big.NewInt(NoiseReplays)))
			floorUpper.Add(floorUpper, bound)
		}
		// The floor share: both replays, with one margin and one rounding.
		share := withMargin(floorExpected)
		for _, arm := range []string{types.ArmFloorA1, types.ArmFloorA2} {
			perArm[arm] = types.ArmEstimate{ExpectedUSD: usd(share) / NoiseReplays, UpperBoundUSD: usd(floorUpper)}
		}
		expected.Add(expected, share)
		upper.Add(upper, new(big.Int).Mul(floorUpper, big.NewInt(NoiseReplays)))
	}

	names := make([]string, 0, len(unpriced))
	for name := range unpriced {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return utf16Less(names[i], names[j]) })
	return types.Estimate{
		Replayable:    len(replayable),
		NotReplayable: notReplayable,
		ExpectedUSD:   usd(expected),
		UpperBoundUSD: usd(upper),
		PerArm:        perArm,
		Unpriced:      names,
	}
}
