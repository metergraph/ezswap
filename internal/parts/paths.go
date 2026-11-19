package parts

import (
	"slices"

	"github.com/metergraph/replay/internal/core"
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
// come first; then Replay's suggested fields (labels, numbers, lists, short
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
	if len(paths) == 0 {
		return nil, ReasonNoField
	}
	return paths, ""
}
