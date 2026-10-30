package core

// The noise floor: production's model replayed twice on the same cases (A1
// and A2), and how far the two replays differ on every built-in statistic and
// tracked field. Python semantics; matches Metergraph's hosted figures,
// which conformance/noise_floor.json pins. Presentation, never a verdict.

// NoiseReplays is NOISE_REPLAYS.
const NoiseReplays = 2

// What a field's values are, which decides how two sides are compared on it.
const (
	FieldLabelMaxDistinct = 12
	FieldShortTextMax     = 120
)

// FloorInput is what NoiseFloorView reads, as a loose object:
//
//	noise_floor     the floor's plan ({replays, gateway_models}), or null when the run has no floor
//	cases           [{position, trace_id, status, control}]
//	noise_results   [{position, replay, outcome}], the floor's replays by case and replay
//	tracked_fields  the tracked field paths
type FloorInput = any

// pyJSString is a value as JavaScript's String() writes it, so a label
// compares alike in the noise floor and the field comparisons.
func pyJSString(value any) string {
	switch value {
	case true:
		return "true"
	case false:
		return "false"
	}
	return pyStr(value)
}

// FieldKindPy is fieldKindPy: the field kind under Python semantics (string
// length in code points).
func FieldKindPy(values []any) FieldKind {
	var present []any
	for _, v := range values {
		if v != nil {
			present = append(present, v)
		}
	}
	if len(present) == 0 {
		return KindEmpty
	}
	every := func(test func(any) bool) bool {
		for _, v := range present {
			if !test(v) {
				return false
			}
		}
		return true
	}
	if every(func(v any) bool { _, ok := finiteNum(v); return ok }) {
		return KindNumber
	}
	if every(isArray) {
		return KindList
	}
	if every(func(v any) bool { _, ok := v.(bool); return ok }) {
		return KindLabel
	}
	if every(func(v any) bool {
		switch v.(type) {
		case string, bool:
			return true
		}
		return false
	}) {
		texts := make([]string, len(present))
		distinct := map[string]bool{}
		short, within := true, true
		for i, v := range present {
			texts[i] = pyJSString(v)
			distinct[texts[i]] = true
			n := codePointLength(texts[i])
			if n > 40 {
				short = false
			}
			if n > FieldShortTextMax {
				within = false
			}
		}
		if short && len(distinct) <= max(FieldLabelMaxDistinct, 2) && len(distinct) < len(present) {
			return KindLabel
		}
		if within {
			return KindText
		}
		return KindLongText
	}
	return KindMixed
}

func floorListItems(value []any) []string {
	out := make([]string, len(value))
	for i, item := range value {
		if isPlainObject(item) || isArray(item) {
			out[i] = pyJSONDumps(item, true, true)
		} else {
			out[i] = pyJSString(item)
		}
	}
	return out
}

// ItemOverlap is itemOverlap: shared items over all distinct items (1 when
// both lists are empty).
func ItemOverlap(a, b []any) float64 {
	left, leftOrder := stringSet(floorListItems(a))
	right, rightOrder := stringSet(floorListItems(b))
	if len(left) == 0 && len(right) == 0 {
		return 1
	}
	shared := 0
	for _, item := range leftOrder {
		if right[item] {
			shared++
		}
	}
	union := len(leftOrder)
	for _, item := range rightOrder {
		if !left[item] {
			union++
		}
	}
	return float64(shared) / float64(union)
}

type floorPair struct {
	traceID any
	a1, a2  any
}

// differenceSpread is A2 minus A1 per case, and the size of those differences.
func differenceSpread(pairs []floorPair) *Spread {
	differences := make([]Difference, len(pairs))
	sizes := make([]float64, len(pairs))
	signed := make([]float64, len(pairs))
	for i, p := range pairs {
		a1, a2 := p.a1.(float64), p.a2.(float64)
		differences[i] = Difference{p.traceID, a1, a2, a2 - a1}
		sizes[i] = abs(a2 - a1)
		signed[i] = a2 - a1
	}
	s := &Spread{
		Comparison:          "difference",
		Pairs:               len(pairs),
		MedianAbsDifference: pyQuantile(sizes, 0.5),
		P90AbsDifference:    pyQuantile(sizes, 0.9),
		Differences:         differences,
	}
	if len(sizes) > 0 {
		s.MaxAbsDifference = ptr(jsMax(sizes...))
		// The signed range of A2 minus A1: what a treatment's own shift is read against.
		s.MinDifference = ptr(jsMin(signed...))
		s.MaxDifference = ptr(jsMax(signed...))
	}
	return s
}

func abs(x float64) float64 {
	if x < 0 || (x == 0 && 1/x < 0) {
		return -x
	}
	return x
}

// matchSpread is how often A1 and A2 give the same value.
func matchSpread(pairs []floorPair) *Spread {
	matched := 0
	mismatches := []any{}
	for _, p := range pairs {
		if pyEqual(p.a1, p.a2) {
			matched++
		} else {
			mismatches = append(mismatches, p.traceID)
		}
	}
	s := &Spread{Comparison: "match_rate", Pairs: len(pairs), Matched: matched, Mismatches: mismatches}
	if len(pairs) > 0 {
		s.MatchRate = ptr(pyRound(float64(matched)/float64(len(pairs)), 4))
	}
	return s
}

// overlapSpread is the item overlap between A1's list and A2's, per case.
func overlapSpread(pairs []floorPair) *Spread {
	overlaps := make([]OverlapItem, len(pairs))
	scores := make([]float64, len(pairs))
	for i, p := range pairs {
		o := pyRound(ItemOverlap(p.a1.([]any), p.a2.([]any)), 4)
		overlaps[i] = OverlapItem{p.traceID, o}
		scores[i] = o
	}
	s := &Spread{Comparison: "overlap", Pairs: len(pairs), Overlaps: overlaps}
	if len(scores) > 0 {
		s.MeanOverlap = ptr(pyRound(pySum(scores)/float64(len(scores)), 4))
		s.MinOverlap = ptr(jsMin(scores...))
	}
	return s
}

// ControlOutcome is controlOutcome: the control as an outcome. Production's
// refusal flag is kept, though the conformance fixtures do not carry it.
func ControlOutcome(control any) *Object {
	c := toLoose(control)
	field := func(key string) any { v, _ := get(c, key); return v }
	refusal, _ := get(c, "refusal")
	return obj(
		"output", field("output"),
		"cost_usd", field("cost_usd"),
		"cost_search_usd", field("cost_search_usd"),
		"latency_ms", field("latency_ms"),
		"finish_reason", field("finish_reason"),
		"output_tokens", field("output_tokens"),
		"input_tokens", field("input_tokens"),
		"cache_read_tokens", field("cache_read_tokens"),
		"input_includes_cache_read", field("input_includes_cache_read"),
		"facts", field("facts"),
		"refusal", pyTruthy(refusal),
		"error", nil,
	)
}

// StatOf is statOf: one built-in statistic of one output, or nil when it has none.
func StatOf(outcome any, key string) any {
	switch key {
	case "cost_usd", "latency_ms", "output_tokens":
		v, _ := get(outcome, key)
		if _, ok := num(v); ok {
			return v
		}
		return nil
	}
	facts := FactsOf(outcome)
	switch key {
	case "output_chars":
		v, _ := get(facts, "chars")
		return v
	case "json_parse":
		v, _ := get(facts, "json_ok")
		return pyTruthy(v)
	case "same_keys":
		if !keysComplete(facts) {
			return nil
		}
		var keys []string
		for _, k := range list(at(facts, "keys")) {
			keys = append(keys, jsString(k))
		}
		sorted := pySorted(keys)
		out := make([]any, len(sorted))
		for i, k := range sorted {
			out[i] = k
		}
		return out
	case "empty_or_refused":
		output, _ := get(outcome, "output")
		finish, _ := get(outcome, "finish_reason")
		refusal, _ := get(outcome, "refusal")
		return IsEmptyOrRefused(output, finish, pyTruthy(refusal))
	case "cut_off":
		finish, _ := get(outcome, "finish_reason")
		return IsCutOff(finish)
	}
	// A stored output cut at the bound is not the whole text.
	output, _ := get(outcome, "output")
	if truncated, _ := get(outcome, "output_truncated"); pyTruthy(truncated) || output == nil {
		return nil
	}
	return pyStrip(jsString(output))
}

// NoiseFloorView is noiseFloorView: how far production's model differs from
// itself on the same cases, the spread between its two replays (A1 and A2)
// for every built-in statistic and tracked field, over the replayed cases
// where both replies came back. Nil without a noise floor. includeCases
// false keeps the aggregates and leaves out every per-case list.
func NoiseFloorView(input FloorInput, includeCases bool) *NoiseFloor {
	input = toLoose(input)
	plan := at(input, "noise_floor")
	if !pyTruthy(plan) {
		return nil
	}
	noise := map[string]any{}
	for _, r := range list(at(input, "noise_results")) {
		position, _ := get(r, "position")
		replay, _ := get(r, "replay")
		key := jsString(position) + ":" + jsString(replay)
		if _, ok := noise[key]; !ok {
			noise[key], _ = get(r, "outcome")
		}
	}
	atReplay := func(c any, replay int) (any, bool) {
		position, _ := get(c, "position")
		o, ok := noise[jsString(position)+":"+jsString(float64(replay))]
		return o, ok
	}
	failed := func(o any, present bool) bool {
		if !present || !pyTruthy(o) {
			return true
		}
		e, _ := get(o, "error")
		return pyTruthy(e)
	}

	var replayed []any
	for _, c := range list(at(input, "cases")) {
		if status, _ := get(c, "status"); status == "replayed" {
			replayed = append(replayed, c)
		}
	}
	a1, a2 := []any{}, []any{}
	controlsA1, controlsA2 := []any{}, []any{}
	type both struct {
		traceID any
		x, y    any
	}
	var pairs []both
	for _, c := range replayed {
		x, okX := atReplay(c, 1)
		y, okY := atReplay(c, 2)
		control, _ := get(c, "control")
		if okX {
			a1 = append(a1, x)
			controlsA1 = append(controlsA1, ControlOutcome(control))
		}
		if okY {
			a2 = append(a2, y)
			controlsA2 = append(controlsA2, ControlOutcome(control))
		}
		if !failed(x, okX) && !failed(y, okY) {
			traceID, _ := get(c, "trace_id")
			pairs = append(pairs, both{traceID, x, y})
		}
	}

	stats := map[string]*Spread{}
	for _, key := range NoiseNumberStats {
		var kept []floorPair
		for _, p := range pairs {
			x, y := StatOf(p.x, key), StatOf(p.y, key)
			if x != nil && y != nil {
				kept = append(kept, floorPair{p.traceID, jsNumber(x), jsNumber(y)})
			}
		}
		stats[key] = differenceSpread(kept)
	}
	for _, key := range NoiseMatchStats {
		var kept []floorPair
		for _, p := range pairs {
			x, y := StatOf(p.x, key), StatOf(p.y, key)
			if x != nil && y != nil {
				kept = append(kept, floorPair{p.traceID, x, y})
			}
		}
		stats[key] = matchSpread(kept)
	}

	fields := map[string]*FloorFieldSpread{}
	for _, p := range list(at(input, "tracked_fields")) {
		path := jsString(p)
		values := make([]floorPair, len(pairs))
		var all []any
		for _, c := range replayed {
			control, _ := get(c, "control")
			all = append(all, FieldValue(ControlOutcome(control), path))
		}
		for i, pair := range pairs {
			values[i] = floorPair{pair.traceID, FieldValue(pair.x, path), FieldValue(pair.y, path)}
			all = append(all, values[i].a1, values[i].a2)
		}
		kind := FieldKindPy(all)
		var present []floorPair
		for _, v := range values {
			if v.a1 != nil && v.a2 != nil {
				present = append(present, v)
			}
		}
		var spread *Spread
		switch kind {
		case KindNumber:
			var numeric []floorPair
			for _, v := range present {
				_, okX := num(v.a1)
				_, okY := num(v.a2)
				if okX && okY {
					numeric = append(numeric, v)
				}
			}
			spread = differenceSpread(numeric)
		case KindList:
			var lists []floorPair
			for _, v := range present {
				if isArray(v.a1) && isArray(v.a2) {
					lists = append(lists, v)
				}
			}
			spread = overlapSpread(lists)
		case KindLabel, KindText, KindLongText:
			texts := make([]floorPair, len(present))
			for i, v := range present {
				texts[i] = floorPair{v.traceID, pyStrip(pyJSString(v.a1)), pyStrip(pyJSString(v.a2))}
			}
			spread = matchSpread(texts)
		default:
			spread = &Spread{Pairs: len(present)}
		}
		// Each replay's raw values, aligned to the replayed cases; null where that replay has none.
		f := &FloorFieldSpread{Spread: *spread, Kind: kind, Missing: len(values) - len(present), A1: []any{}, A2: []any{}}
		for _, c := range replayed {
			x, _ := atReplay(c, 1)
			y, _ := atReplay(c, 2)
			f.A1 = append(f.A1, FieldValue(x, path))
			f.A2 = append(f.A2, FieldValue(y, path))
		}
		fields[path] = f
	}
	if !includeCases {
		for _, s := range stats {
			s.stripCases()
		}
		for _, f := range fields {
			f.stripCases()
			f.A1, f.A2 = nil, nil
		}
	}

	var costs []float64
	for _, o := range append(append([]any{}, a1...), a2...) {
		if cost, _ := get(o, "cost_usd"); pyTruthy(cost) {
			costs = append(costs, jsNumber(cost))
		} else {
			costs = append(costs, 0)
		}
	}
	replays, present := get(plan, "replays")
	if !present {
		replays = float64(NoiseReplays)
	}
	var models []string
	seen := map[string]bool{}
	for _, m := range list(at(plan, "gateway_models")) {
		s := jsString(m)
		if !seen[s] {
			seen[s] = true
			models = append(models, s)
		}
	}
	errorsIn := func(outcomes []any) int {
		n := 0
		for _, o := range outcomes {
			if e, _ := get(o, "error"); pyTruthy(e) {
				n++
			}
		}
		return n
	}
	ys := make([]any, len(pairs))
	xs := make([]any, len(pairs))
	for i, p := range pairs {
		xs[i], ys[i] = p.x, p.y
	}
	return &NoiseFloor{
		Replays:        numPtr(replays),
		GatewayModels:  pySorted(models),
		Cases:          len(pairs),
		Errors:         FloorErrors{errorsIn(a1), errorsIn(a2)},
		A1:             Summarize(a1, controlsA1),
		A2:             Summarize(a2, controlsA2),
		Summary:        Summarize(ys, xs),
		ControlSummary: Summarize(xs, nil),
		SpentUSD:       pyRound(pySum(costs), 8),
		Stats:          stats,
		Fields:         fields,
	}
}
