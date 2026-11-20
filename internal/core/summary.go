package core

// Built-in statistics for one arm or for the control: cost, latency, output
// length, JSON parse rate, same top-level keys, empty or refused, cut off, and
// the share of input read from cache. Python semantics; matches Metergraph's
// hosted figures, which conformance/summarize.json pins.

import (
	"regexp"

	"github.com/metergraph/ezswap/internal/jsnum"
)

// CutOffReasons are the finish reasons that mean the reply hit its token limit.
var CutOffReasons = map[string]bool{"length": true, "max_tokens": true, "max_output_tokens": true}

// RefusalReasons are the finish reasons that mean the provider refused or filtered the reply.
var RefusalReasons = map[string]bool{"content_filter": true, "refusal": true, "safety": true}

// What is kept of an output's structure once the output itself is bounded.
const (
	MaxFactKeys       = 200
	MaxFactKeyChars   = 256
	MaxFactFieldBytes = 4 * 1024
	// MaxFactsBytes is a hard bound on the serialized facts of one output, whatever its shape.
	MaxFactsBytes = 32 * 1024
)

var pyFence = regexp.MustCompile("^(?s)```[A-Za-z0-9_-]*" + pyWSClass + "*\n(.*?)\n?```" + pyWSClass + "*$")

// ParseOutputJSON is parseOutputJson: an output parsed as JSON, accepting
// one surrounding Markdown code fence. NaN, Infinity and -Infinity are not
// JSON, so an output using them does not parse.
func ParseOutputJSON(output any) (any, bool) {
	s, ok := output.(string)
	if !ok {
		return nil, false
	}
	text := pyStrip(s)
	if text == "" {
		return nil, false
	}
	if m := pyFence.FindStringSubmatch(text); m != nil {
		text = pyStrip(m[1])
	}
	v, err := ParseJSON(text)
	if err != nil {
		return nil, false
	}
	return v, true
}

// ExtractField is extractField: a tracked field's value in one output, or
// nil when it does not parse or has no such field.
func ExtractField(output any, path string) any {
	parsed, ok := ParseOutputJSON(output)
	if !ok {
		return nil
	}
	found, ok := extract(parsed, mustParseJSONPath(path))
	if !ok {
		return nil
	}
	return found
}

// OutputFacts is outputFacts: what the statistics need from one output,
// taken from the full text, and bounded whatever the output holds: at most
// MaxFactKeys keys of at most MaxFactKeyChars each, and at most
// MaxFactsBytes serialized. Over that, the field values go first and then
// the keys; `truncated` says so. The result is the facts object, in the
// view's own keys (json_ok, keys, chars, fields, truncated, keys_complete).
func OutputFacts(output any, trackedFields []string) *Object {
	value, ok := ParseOutputJSON(output)
	isObject := ok && isPlainObject(value)
	fields := NewObject()
	if ok {
		for _, path := range trackedFields {
			found, there := extract(value, mustParseJSONPath(path))
			if !there || pyJSONBytes(found) > MaxFactFieldBytes {
				found = nil
			}
			fields.Set(path, found)
		}
	}
	var keys any
	truncated := false
	if isObject {
		names := pySorted(keysOf(value))
		truncated = len(names) > MaxFactKeys
		for _, name := range names {
			if codePointLength(name) > MaxFactKeyChars {
				truncated = true
			}
		}
		seen := map[string]bool{}
		var cut []string
		for _, name := range names {
			short := codePointSlice(name, MaxFactKeyChars)
			if !seen[short] {
				seen[short] = true
				cut = append(cut, short)
			}
		}
		cut = pySorted(cut)
		if len(cut) > MaxFactKeys {
			cut = cut[:MaxFactKeys]
		}
		kept := make([]any, len(cut))
		for i, k := range cut {
			kept[i] = k
		}
		keys = kept
	}
	text, _ := output.(string)
	facts := obj(
		"json_ok", ok,
		"keys", keys,
		"chars", float64(codePointLength(text)),
		"fields", fields,
		"truncated", truncated,
		"keys_complete", isObject && !truncated,
	)
	if pyJSONBytes(facts) > MaxFactsBytes {
		emptied := NewObject()
		for _, path := range fields.Keys() {
			emptied.Set(path, nil)
		}
		facts.Set("fields", emptied)
		facts.Set("truncated", true)
	}
	if pyJSONBytes(facts) > MaxFactsBytes {
		facts.Set("fields", NewObject())
		facts.Set("keys", nil)
		facts.Set("keys_complete", false)
	}
	return facts
}

// FactsOf is factsOf: stored facts when there are any, else worked out from
// the stored output. It takes an outcome object, an output string, or nil.
func FactsOf(outcome any) any {
	if isPlainObject(outcome) {
		stored, _ := get(outcome, "facts")
		if pyTruthy(stored) {
			return stored
		}
		output, _ := get(outcome, "output")
		return OutputFacts(output, nil)
	}
	if s, ok := outcome.(string); ok {
		return OutputFacts(s, nil)
	}
	return OutputFacts(nil, nil)
}

// FieldValue is fieldValue: one tracked field's value in one outcome, from
// its stored facts when they hold it.
func FieldValue(outcome any, path string) any {
	facts, _ := get(outcome, "facts")
	if pyTruthy(facts) {
		if fields, _ := get(facts, "fields"); has(fields, path) {
			v, _ := get(fields, path)
			return v
		}
	}
	output, _ := get(outcome, "output")
	return ExtractField(output, path)
}

// IsCutOff is isCutOff.
func IsCutOff(finishReason any) bool {
	s, ok := finishReason.(string)
	return ok && CutOffReasons[jsToLower(pyStrip(s))]
}

// IsEmptyOrRefused is isEmptyOrRefused: refused (by flag or finish reason),
// or no text at all.
func IsEmptyOrRefused(output, finishReason any, refusal bool) bool {
	if refusal {
		return true
	}
	if s, ok := finishReason.(string); ok && RefusalReasons[jsToLower(pyStrip(s))] {
		return true
	}
	if output == nil {
		return true
	}
	s, ok := output.(string)
	return ok && pyStrip(s) == ""
}

func rate(numerator, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}
	return ptr(pyRound(float64(numerator)/float64(denominator), 4))
}

// keysComplete is whether facts hold an object's whole key set.
func keysComplete(facts any) bool {
	if v, ok := get(facts, "keys_complete"); ok {
		return pyTruthy(v)
	}
	keys, _ := get(facts, "keys")
	truncated, _ := get(facts, "truncated")
	return keys != nil && !pyTruthy(truncated)
}

func tokenCount(value any) (float64, bool) {
	f, ok := finiteNum(value)
	if !ok || !jsnum.IsInteger(f) || f < 0 {
		return 0, false
	}
	return f, true
}

// CachedInput is cachedInput: [input tokens read from cache, all input
// tokens] for one call, or nil when it reported no cache count or its input
// total cannot be placed. A cache count above the input total cannot have
// been included in it.
func CachedInput(outcome any) *[2]float64 {
	cachedV, _ := get(outcome, "cache_read_tokens")
	reportedV, _ := get(outcome, "input_tokens")
	cached, ok1 := tokenCount(cachedV)
	reported, ok2 := tokenCount(reportedV)
	if !ok1 || !ok2 {
		return nil
	}
	if cached > reported {
		return &[2]float64{cached, reported + cached}
	}
	includes, present := get(outcome, "input_includes_cache_read")
	if !present {
		includes = true
	}
	if cached == 0 || includes == true {
		return &[2]float64{cached, reported}
	}
	if includes == false {
		return &[2]float64{cached, reported + cached}
	}
	return nil
}

// numbersAt is `outcomes.filter((o) => o[key] != null).map((o) => Number(o[key]))`.
func numbersAt(outcomes []any, key string) []float64 {
	var out []float64
	for _, o := range outcomes {
		if v, _ := get(o, key); v != nil {
			out = append(out, jsNumber(v))
		}
	}
	return out
}

// Summarize is summarize: built-in statistics for one arm (or the control).
// `controls` are the control of the same cases, aligned, for the same-keys
// comparison; nil means none (a non-nil empty slice is an empty set of
// controls). Rates are over the calls that returned (errors are counted, not
// averaged in); same_keys_rate is over the cases whose control output is a
// JSON object; cached_input_share is over the calls that reported a cache
// count, weighted by their input tokens, and nil when none did, never zero.
// Outcomes and controls are loose outcome objects (a control may also be an
// output string). It panics when controls are not aligned with outcomes.
func Summarize(outcomes []any, controls []any) Summary {
	var succeeded []any
	for _, o := range outcomes {
		if e, _ := get(o, "error"); !pyTruthy(e) {
			succeeded = append(succeeded, o)
		}
	}
	costs := numbersAt(succeeded, "cost_usd")
	fees := numbersAt(succeeded, "cost_search_usd")
	var modelCosts []float64
	for _, o := range succeeded {
		cost, _ := get(o, "cost_usd")
		fee, _ := get(o, "cost_search_usd")
		if cost != nil && fee != nil {
			modelCosts = append(modelCosts, jsNumber(cost)-jsNumber(fee))
		}
	}
	latencies := numbersAt(succeeded, "latency_ms")
	tokens := numbersAt(succeeded, "output_tokens")
	facts := make([]any, len(succeeded))
	for i, o := range succeeded {
		facts[i] = FactsOf(o)
	}
	var cache [][2]float64
	for _, o := range succeeded {
		if pair := CachedInput(o); pair != nil {
			cache = append(cache, *pair)
		}
	}
	cacheInputTotal := 0.0
	cacheRead := 0.0
	for _, pair := range cache {
		cacheInputTotal += pair[1]
	}
	for _, pair := range cache {
		cacheRead += pair[0]
	}

	sameKeys, comparable := 0, 0
	if controls != nil {
		if len(controls) != len(outcomes) {
			panic("summarize: outcomes and controls must be aligned")
		}
		for i, outcome := range outcomes {
			controlFacts := FactsOf(controls[i])
			if e, _ := get(outcome, "error"); !keysComplete(controlFacts) || pyTruthy(e) {
				continue
			}
			outcomeFacts := FactsOf(outcome)
			keys, _ := get(outcomeFacts, "keys")
			truncated, _ := get(outcomeFacts, "truncated")
			// An object whose key set was cut: not comparable, and not a mismatch.
			if keys != nil && !keysComplete(outcomeFacts) {
				continue
			}
			// Its keys were too large to keep at all.
			if keys == nil && pyTruthy(truncated) {
				continue
			}
			comparable++
			controlKeys, _ := get(controlFacts, "keys")
			if keys != nil && sameSet(list(keys), list(controlKeys)) {
				sameKeys++
			}
		}
	}

	mean8 := func(values []float64) *float64 {
		if len(values) == 0 {
			return nil
		}
		return ptr(pyRound(pySum(values)/float64(len(values)), 8))
	}
	chars := make([]float64, len(facts))
	jsonOK := 0
	for i, f := range facts {
		c, _ := get(f, "chars")
		chars[i] = jsNumber(c)
		if ok, _ := get(f, "json_ok"); truthy(ok) {
			jsonOK++
		}
	}
	emptyOrRefused, cutOff := 0, 0
	for _, o := range succeeded {
		output, _ := get(o, "output")
		finish, _ := get(o, "finish_reason")
		refusal, _ := get(o, "refusal")
		if IsEmptyOrRefused(output, finish, pyTruthy(refusal)) {
			emptyOrRefused++
		}
		if IsCutOff(finish) {
			cutOff++
		}
	}
	temperatureDropped, responseFormatDropped := 0, 0
	for _, o := range outcomes {
		if v, _ := get(o, "temperature_dropped"); pyTruthy(v) {
			temperatureDropped++
		}
		if v, _ := get(o, "response_format_dropped"); pyTruthy(v) {
			responseFormatDropped++
		}
	}
	s := Summary{
		Cases:                 len(outcomes),
		Errors:                len(outcomes) - len(succeeded),
		CostPerCallUSD:        mean8(costs),
		ModelCostPerCallUSD:   mean8(modelCosts),
		SearchCostPerCallUSD:  mean8(fees),
		LatencyMsP50:          pyMedian(latencies),
		OutputTokensP50:       pyMedian(tokens),
		OutputCharsP50:        pyMedian(chars),
		JSONParseRate:         rate(jsonOK, len(succeeded)),
		EmptyOrRefusedRate:    rate(emptyOrRefused, len(succeeded)),
		CutOffRate:            rate(cutOff, len(succeeded)),
		CachedInputCalls:      len(cache),
		TemperatureDropped:    temperatureDropped,
		ResponseFormatDropped: responseFormatDropped,
	}
	if controls != nil {
		s.SameKeysRate = rate(sameKeys, comparable)
		s.SameKeysCompared = ptr(comparable)
	}
	if cacheInputTotal != 0 && !isNaN(cacheInputTotal) {
		s.CachedInputShare = ptr(pyRound(cacheRead/cacheInputTotal, 4))
	}
	return s
}

func isNaN(f float64) bool { return f != f }

func sameSet(a, b []any) bool {
	left := map[string]bool{}
	for _, k := range a {
		left[jsString(k)] = true
	}
	right := map[string]bool{}
	for _, k := range b {
		right[jsString(k)] = true
	}
	if len(left) != len(right) {
		return false
	}
	for k := range left {
		if !right[k] {
			return false
		}
	}
	return true
}
