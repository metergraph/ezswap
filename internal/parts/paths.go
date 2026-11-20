package parts

import (
	"regexp"
	"slices"

	"github.com/metergraph/ezswap/internal/core"
)

const (
	// MaxPaths is the most paths a part is compared on.
	MaxPaths = 10
	// MinCases is the fewest paired cases a part needs for a label.
	MinCases = 10

	ReasonFreeText = "free text"
	ReasonNoField  = "no comparable field"
	ReasonTooFew   = "under 10 cases"
)

// ComparedPaths is the paths one part is compared on, chosen from
// production's recorded outputs alone, so the choice is known before any
// spending and does not depend on what a treatment returns. Tracked paths
// come first; then ezswap's suggested fields (labels, numbers, lists, short
// text, each present in at least half the outputs); ignored paths are left
// out; at most MaxPaths are kept. With no path the reason says why:
// ReasonFreeText when fewer than half the outputs parse as JSON, else
// ReasonNoField.
func ComparedPaths(controlOutputs []*string, tracked, ignored []string) ([]string, string) {
	items := make([]any, len(controlOutputs))
	parsed := 0
	for i, output := range controlOutputs {
		var value any
		if output != nil {
			value = *output
			if core.ParseOutput(value).OK {
				parsed++
			}
		}
		items[i] = map[string]any{"control": map[string]any{"output": value}}
	}
	if len(items) == 0 || parsed*2 < len(items) {
		return nil, ReasonFreeText
	}
	var paths []string
	add := func(path string) {
		if len(paths) < MaxPaths && !slices.Contains(paths, path) && !slices.Contains(ignored, path) {
			paths = append(paths, path)
		}
	}
	for _, path := range tracked {
		add(path)
	}
	// Ask for enough that ignored and tracked paths do not crowd out the rest.
	for _, path := range core.SuggestedFields(items, MaxPaths+len(ignored)+len(tracked)) {
		add(path)
	}
	// A top-level key the recordings answer with an explicit null still
	// counts: "none" is an answer, and a model that fills one in (an id it
	// guessed, say) has changed it.
	for _, key := range answeredKeys(controlOutputs, (len(items)+1)/2) {
		add("$." + key)
	}
	if len(paths) == 0 {
		return nil, ReasonNoField
	}
	return paths, ""
}

var plainKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// answeredKeys is the top-level keys at least half of the outputs hold,
// null or not, that some output holds as null and whose other values are
// labels, numbers or short text: fields the suggestions pass over because
// most outputs answer "none".
func answeredKeys(outputs []*string, half int) []string {
	type seen struct {
		count  int
		null   bool
		values []any
	}
	keys := map[string]*seen{}
	var order []string
	for _, output := range outputs {
		if output == nil {
			continue
		}
		parsed := core.ParseOutput(*output)
		object, ok := parsed.Value.(*core.Object)
		if !parsed.OK || !ok {
			continue
		}
		for _, key := range object.Keys() {
			if !plainKey.MatchString(key) {
				continue
			}
			value, _ := object.Get(key)
			s := keys[key]
			if s == nil {
				s = &seen{}
				keys[key] = s
				order = append(order, key)
			}
			s.count++
			if value == nil {
				s.null = true
			} else {
				s.values = append(s.values, value)
			}
		}
	}
	var out []string
	for _, key := range order {
		s := keys[key]
		if s.count < half || !s.null {
			continue
		}
		if len(s.values) > 0 {
			switch core.FieldKindOf(s.values) {
			case core.KindLabel, core.KindNumber, core.KindText:
			default:
				continue
			}
		}
		out = append(out, key)
	}
	return out
}
