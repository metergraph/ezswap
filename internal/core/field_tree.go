package core

// The fields a run could track, as the JSON tree the outputs share: objects
// as branches, the values a field can track as leaves. Matches Metergraph's
// hosted figures (conformance/page.json).

import (
	"slices"
	"strconv"
)

// OpenAllMax is OPEN_ALL_MAX: a tree with this many fields or fewer opens
// whole; a larger one opens only toward what is tracked.
const OpenAllMax = 15

const sampleMax = 40

func stepPath(prefix string, token PageToken) string {
	switch token.Kind {
	case TokenEach:
		return prefix + "[]"
	case TokenIndex:
		return prefix + "[" + jsNumberText(token.Index) + "]"
	}
	if isIdentifier(token.Key) {
		return prefix + "." + token.Key
	}
	return prefix + "[" + jsonQuote(token.Key) + "]"
}

func stepLabel(token PageToken) string {
	switch token.Kind {
	case TokenEach:
		return "[ ]"
	case TokenIndex:
		return "[" + jsNumberText(token.Index) + "]"
	}
	return token.Key
}

func jsNumberText(f float64) string { return jsString(f) }

// controlOutputs is production's own outputs that parse, in case order.
func controlOutputs(cases []any) []any {
	var out []any
	for _, c := range cases {
		if p := ParseOutput(at(c, "control", "output")); p.OK {
			out = append(out, p.Value)
		}
	}
	return out
}

// ParsedOutputCount is parsedOutputCount: how many outputs parsed as JSON at
// all, control and arms together.
func ParsedOutputCount(cases any) int {
	n := 0
	for _, c := range list(toLoose(cases)) {
		for _, output := range caseOutputs(c) {
			if ParseOutput(output).OK {
				n++
			}
		}
	}
	return n
}

// SamplePreview is samplePreview: a field's first value in production's
// outputs, as a short preview. present false is JavaScript's undefined.
func SamplePreview(value any, present bool) string {
	if !present {
		return ""
	}
	switch v := value.(type) {
	case []any:
		word := "items"
		if len(v) == 1 {
			word = "item"
		}
		return "[" + strconv.Itoa(len(v)) + " " + word + "]"
	case *Object, map[string]any:
		return "{ }"
	}
	var text string
	if s, ok := value.(string); ok {
		text = jsonQuote(s)
	} else {
		text = jsString(value)
	}
	if utf16Len(text) > sampleMax {
		return utf16Slice(text, sampleMax-2) + "…\""
	}
	return text
}

// FieldLeaf is what a tree leaf carries: how many outputs had the field, out
// of how many parsed, its kind over production's values and a preview of one.
type FieldLeaf struct {
	Path   string    `json:"path"`
	Count  int       `json:"count"`
	Total  int       `json:"total"`
	Kind   FieldKind `json:"kind"`
	Sample string    `json:"sample"`
}

// FieldNode is one node of the field tree.
type FieldNode struct {
	Path     string       `json:"path"`
	Key      string       `json:"key"`
	Children []*FieldNode `json:"children"`
	Leaf     *FieldLeaf   `json:"leaf"`
}

// FieldTree is fieldTree: the tree, nodes under one root, children in the
// order their paths first appear (most common first).
func FieldTree(cases any, tracked []string) *FieldNode {
	items := list(toLoose(cases))
	paths := PickerPaths(items, tracked)
	controls := controlOutputs(items)
	total := ParsedOutputCount(items)
	root := &FieldNode{Path: "$", Key: "output", Children: []*FieldNode{}}
	index := map[string]*FieldNode{"$": root}
	for _, p := range paths {
		tokens, ok := ParsePath(p.Path)
		if !ok || len(tokens) == 0 {
			continue
		}
		node := root
		prefix := "$"
		for _, token := range tokens {
			prefix = stepPath(prefix, token)
			next, ok := index[prefix]
			if !ok {
				next = &FieldNode{Path: prefix, Key: stepLabel(token), Children: []*FieldNode{}}
				index[prefix] = next
				node.Children = append(node.Children, next)
			}
			node = next
		}
		values := make([]any, len(controls))
		var first any
		found := false
		for i, value := range controls {
			v, there := ValueAt(value, p.Path)
			values[i] = v
			if there && !found {
				first, found = v, true
			}
		}
		node.Leaf = &FieldLeaf{Path: p.Path, Count: p.Count, Total: total, Kind: FieldKindOf(values), Sample: SamplePreview(first, found)}
	}
	return root
}

// LeafPaths is leafPaths: every leaf path at or under a node, in tree order.
func LeafPaths(node *FieldNode) []string {
	var out []string
	if node.Leaf != nil {
		out = append(out, node.Leaf.Path)
	}
	for _, child := range node.Children {
		out = append(out, LeafPaths(child)...)
	}
	return out
}

// BranchState is a branch's state against the tracked set: "all", "some" or "none".
type BranchState struct {
	State string `json:"state"`
	On    int    `json:"on"`
	Total int    `json:"total"`
}

// BranchStateOf is branchState.
func BranchStateOf(node *FieldNode, tracked []string) BranchState {
	leaves := LeafPaths(node)
	on := 0
	for _, p := range leaves {
		if slices.Contains(tracked, p) {
			on++
		}
	}
	state := "some"
	if on == 0 {
		state = "none"
	} else if on == len(leaves) {
		state = "all"
	}
	return BranchState{state, on, len(leaves)}
}

// ToggleBranch is toggleBranch: track everything under a branch, or untrack
// it when all of it is tracked. Order is kept.
func ToggleBranch(node *FieldNode, tracked []string) []string {
	leaves := LeafPaths(node)
	all := true
	for _, p := range leaves {
		if !slices.Contains(tracked, p) {
			all = false
		}
	}
	out := []string{}
	if all {
		for _, p := range tracked {
			if !slices.Contains(leaves, p) {
				out = append(out, p)
			}
		}
		return out
	}
	out = append(out, tracked...)
	for _, p := range leaves {
		if !slices.Contains(tracked, p) {
			out = append(out, p)
		}
	}
	return out
}

// InitialOpen is initialOpen: which branches start open, all for a small
// tree, else those leading to a focused field (the root always), in the
// order a walk of the tree meets them.
func InitialOpen(root *FieldNode, focus []string) []string {
	open := []string{"$"}
	seen := map[string]bool{"$": true}
	all := LeafPaths(root)
	var walk func(node *FieldNode)
	walk = func(node *FieldNode) {
		if len(node.Children) == 0 {
			return
		}
		leaves := LeafPaths(node)
		focused := false
		for _, p := range leaves {
			if slices.Contains(focus, p) {
				focused = true
			}
		}
		if (len(all) <= OpenAllMax || focused) && !seen[node.Path] {
			seen[node.Path] = true
			open = append(open, node.Path)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return open
}
