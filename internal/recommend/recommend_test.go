package recommend

import (
	"slices"
	"strings"
	"testing"
)

var testList = List{Schema: Schema, Models: []Model{
	{ID: "a/large", Publisher: "a", FirstPriced: "2026-03-01"},
	{ID: "a/mid", Publisher: "a", FirstPriced: "2026-02-01"},
	{ID: "a/small", Publisher: "a", FirstPriced: "2026-01-01"},
	{ID: "a/tiny", Publisher: "a", FirstPriced: "2025-12-01"},
	{ID: "b/large", Publisher: "b", FirstPriced: "2026-04-01"},
	{ID: "b/small", Publisher: "b", FirstPriced: "2026-03-15"},
	{ID: "c/unpriced", Publisher: "c", FirstPriced: "2026-05-01"},
}}

var testPrices = map[string]float64{
	"a/large": 40, "a/mid": 10, "a/small": 2, "a/tiny": 1, "b/large": 30, "b/small": 3, "prod/own": 10,
}

func price(model string) (float64, bool) {
	usd, ok := testPrices[model]
	return usd, ok
}

func TestSuggest(t *testing.T) {
	for _, c := range []struct{ objective, production, want string }{
		// Cheaper than production, the cheapest first, two from a publisher.
		{Cost, "prod/own", "a/tiny a/small b/small"},
		// Production itself is never suggested, and a model at its price
		// is neither cheaper nor costlier.
		{Cost, "a/mid", "a/tiny a/small b/small"},
		{Quality, "a/mid", "b/large a/large"},
		{Quality, "a/large", ""},
		{Cost, "a/tiny", ""},
		// The newest from each publisher; the unpriced model is left out.
		{Latest, "prod/own", "b/large a/large"},
		{Latest, "b/large", "b/small a/large"},
		// Production without a price: the whole list is ranked.
		{Cost, "unknown/model", "a/tiny a/small b/small"},
		{Quality, "unknown/model", "a/large b/large a/mid"},
		{"speed", "prod/own", ""},
	} {
		got := strings.Join(Suggest(testList, c.objective, c.production, price, 3), " ")
		if got != c.want {
			t.Errorf("%s for %s: got %q, want %q", c.objective, c.production, got, c.want)
		}
	}
	if got := Suggest(testList, Cost, "prod/own", price, 1); !slices.Equal(got, []string{"a/tiny"}) {
		t.Errorf("limit 1: %v", got)
	}
}

func TestBundledListParses(t *testing.T) {
	l := Bundled()
	if len(l.Models) == 0 || l.Updated == "" {
		t.Fatalf("%+v", l)
	}
	publishers := map[string]bool{}
	for _, m := range l.Models {
		publishers[m.Publisher] = true
		if !strings.HasPrefix(m.ID, m.Publisher+"/") {
			t.Errorf("%s is not under its publisher %s", m.ID, m.Publisher)
		}
	}
	if len(publishers) < 2 {
		t.Errorf("one publisher only: %v", publishers)
	}
}

func TestParseRefusesABadList(t *testing.T) {
	for name, doc := range map[string]string{
		"schema":    `{"schema":"other/v1","models":[]}`,
		"no date":   `{"schema":"` + Schema + `","models":[{"id":"a/x","publisher":"a"}]}`,
		"duplicate": `{"schema":"` + Schema + `","models":[{"id":"a/x","publisher":"a","first_priced":"2026-01-01"},{"id":"a/x","publisher":"a","first_priced":"2026-01-01"}]}`,
		"not json":  `{`,
	} {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}
