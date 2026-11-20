// Package recommend suggests models to try for an objective: cost less than
// production's model, cost more, or be the newest. The models come from a
// list kept in this package; their prices come from the caller, which reads
// the price catalog.
package recommend

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

//go:embed recommendations.json
var bundled []byte

// Schema is the format of recommendations.json.
const Schema = "metergraph.ezswap-recommendations/v1"

// The objectives, in the order a screen offers them.
const (
	Cost    = "cost"
	Quality = "quality"
	Latest  = "latest"
)

// Objectives is every objective, in the order a screen offers them.
var Objectives = []string{Cost, Quality, Latest}

// Model is one model on the list.
type Model struct {
	// "creator/model", as the price catalog names it.
	ID        string `json:"id"`
	Publisher string `json:"publisher"`
	// The date the price catalog first carried the model, YYYY-MM-DD. It
	// stands in for the release date.
	FirstPriced string `json:"first_priced"`
}

// List is recommendations.json.
type List struct {
	Schema  string  `json:"schema"`
	Updated string  `json:"updated"`
	Models  []Model `json:"models"`
}

// Parse reads a recommendations document.
func Parse(data []byte) (List, error) {
	var l List
	if err := json.Unmarshal(data, &l); err != nil {
		return List{}, err
	}
	if l.Schema != Schema {
		return List{}, fmt.Errorf("recommendations: schema %q, want %q", l.Schema, Schema)
	}
	seen := map[string]bool{}
	for _, m := range l.Models {
		if m.ID == "" || m.Publisher == "" || len(m.FirstPriced) != len("2006-01-02") {
			return List{}, fmt.Errorf("recommendations: %q needs an id, a publisher and a first_priced date", m.ID)
		}
		if seen[m.ID] {
			return List{}, fmt.Errorf("recommendations: %q is listed twice", m.ID)
		}
		seen[m.ID] = true
	}
	return l, nil
}

// Bundled is the list built into this binary.
func Bundled() List {
	l, err := Parse(bundled)
	if err != nil {
		panic(err) // a test parses the bundled file
	}
	return l
}

// Price is what one typical call costs on a model, and whether the catalog
// can price it at all.
type Price func(model string) (usd float64, ok bool)

// Suggest is up to limit models from the list for an objective, the best
// first. production is the model the traces were recorded on; it is never
// suggested. A model the catalog cannot price is never suggested either,
// because a map refuses it.
//
//   - Cost: models that cost less than production, the cheapest first, at
//     most two from one publisher.
//   - Quality: models that cost more than production, the nearest in price
//     first, at most two from one publisher. Price stands in for strength.
//   - Latest: the newest model from each publisher, the newest first.
//
// When production has no price, cost and quality rank the whole list.
func Suggest(l List, objective, production string, price Price, limit int) []string {
	type priced struct {
		Model
		usd   float64
		order int
	}
	var candidates []priced
	for i, m := range l.Models {
		if m.ID == production {
			continue
		}
		usd, ok := price(m.ID)
		if !ok {
			continue
		}
		candidates = append(candidates, priced{m, usd, i})
	}
	base, known := price(production)
	perPublisher := 2
	switch objective {
	case Cost:
		if known {
			candidates = slices.DeleteFunc(candidates, func(c priced) bool { return c.usd >= base })
		}
		sort.SliceStable(candidates, func(a, b int) bool { return candidates[a].usd < candidates[b].usd })
	case Quality:
		if known {
			candidates = slices.DeleteFunc(candidates, func(c priced) bool { return c.usd <= base })
			sort.SliceStable(candidates, func(a, b int) bool { return candidates[a].usd < candidates[b].usd })
		} else {
			sort.SliceStable(candidates, func(a, b int) bool { return candidates[a].usd > candidates[b].usd })
		}
	case Latest:
		perPublisher = 1
		sort.SliceStable(candidates, func(a, b int) bool { return candidates[a].FirstPriced > candidates[b].FirstPriced })
	default:
		return nil
	}
	var out []string
	from := map[string]int{}
	for _, c := range candidates {
		if len(out) == limit {
			break
		}
		if from[c.Publisher] == perPublisher {
			continue
		}
		from[c.Publisher]++
		out = append(out, c.ID)
	}
	return out
}
