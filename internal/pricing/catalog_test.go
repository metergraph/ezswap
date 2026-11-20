package pricing

import (
	"encoding/json"
	"math/big"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/metergraph/ezswap/internal/types"
)

type pricingFixture struct {
	Catalog struct {
		Commit  string `json:"commit"`
		Version string `json:"version"`
	} `json:"catalog"`
	Cases []struct {
		Name  string `json:"name"`
		Query struct {
			Model                  string   `json:"model"`
			InputTokens            float64  `json:"inputTokens"`
			OutputTokens           float64  `json:"outputTokens"`
			CachedInputTokens      *float64 `json:"cachedInputTokens"`
			InputIncludesCacheRead *bool    `json:"inputIncludesCacheRead"`
			At                     string   `json:"at"`
		} `json:"query"`
		Expected struct {
			CanonicalModel *string  `json:"canonicalModel"`
			PriceID        *string  `json:"priceId"`
			CostUSD        *string  `json:"costUsd"`
			Status         string   `json:"status"`
			Reasons        []string `json:"reasons"`
		} `json:"expected"`
	} `json:"cases"`
}

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatal(err)
	}
}

func mustLoad(t *testing.T, options LoadPricingOptions) *CatalogPricing {
	t.Helper()
	pricing, err := LoadPricing(options)
	if err != nil {
		t.Fatal(err)
	}
	return pricing
}

func str(s string) *string { return &s }

func strOrNil(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func costOrNil(p *float64) string {
	if p == nil {
		return "<nil>"
	}
	return strconv.FormatFloat(*p, 'g', -1, 64)
}

func TestConformanceFixtureMatchesBundledCatalog(t *testing.T) {
	var fixture pricingFixture
	readJSON(t, "../../conformance/pricing.json", &fixture)
	if fixture.Catalog.Commit != BundledCatalog.Source.Commit || fixture.Catalog.Version != BundledCatalog.Version {
		t.Fatalf("fixture catalog %+v, bundled %s %s", fixture.Catalog, BundledCatalog.Source.Commit, BundledCatalog.Version)
	}
	if len(fixture.Cases) <= 100 {
		t.Fatalf("only %d cases", len(fixture.Cases))
	}
}

func TestPricingConformance(t *testing.T) {
	var fixture pricingFixture
	readJSON(t, "../../conformance/pricing.json", &fixture)
	pricing := mustLoad(t, LoadPricingOptions{})
	for _, item := range fixture.Cases {
		t.Run(item.Name, func(t *testing.T) {
			q := item.Query
			quote := pricing.Quote(types.PriceQuery{
				Model:                  q.Model,
				InputTokens:            q.InputTokens,
				OutputTokens:           q.OutputTokens,
				CachedInputTokens:      q.CachedInputTokens,
				InputIncludesCacheRead: q.InputIncludesCacheRead,
				At:                     q.At,
			})
			want := item.Expected
			if strOrNil(quote.CanonicalModel) != strOrNil(want.CanonicalModel) {
				t.Errorf("canonicalModel %s, want %s", strOrNil(quote.CanonicalModel), strOrNil(want.CanonicalModel))
			}
			if strOrNil(quote.PriceID) != strOrNil(want.PriceID) {
				t.Errorf("priceId %s, want %s", strOrNil(quote.PriceID), strOrNil(want.PriceID))
			}
			if quote.Status != want.Status {
				t.Errorf("status %s, want %s", quote.Status, want.Status)
			}
			if !reflect.DeepEqual(quote.Reasons, want.Reasons) {
				t.Errorf("reasons %v, want %v", quote.Reasons, want.Reasons)
			}
			var wantCost *float64
			if want.CostUSD != nil {
				n, err := strconv.ParseFloat(*want.CostUSD, 64)
				if err != nil {
					t.Fatal(err)
				}
				wantCost = &n
			}
			if costOrNil(quote.CostUSD) != costOrNil(wantCost) {
				t.Errorf("costUsd %s, want %s", costOrNil(quote.CostUSD), costOrNil(wantCost))
			}
		})
	}
}

var base = types.PriceQuery{At: "2026-10-20T12:00:00Z"}

func query(model string, input, output float64) types.PriceQuery {
	q := base
	q.Model, q.InputTokens, q.OutputTokens = model, input, output
	return q
}

func TestCatalogVersionNamesSourceCommit(t *testing.T) {
	pricing := mustLoad(t, LoadPricingOptions{})
	if !regexp.MustCompile(`^metergraph-core prices\.yaml \d{4}-\d{2}-\d{2} \([0-9a-f]{12}\)$`).MatchString(pricing.CatalogVersion()) {
		t.Errorf("catalog version %q", pricing.CatalogVersion())
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(pricing.Source.Commit) {
		t.Errorf("commit %q", pricing.Source.Commit)
	}
	if pricing.Source.License != "Apache-2.0" {
		t.Errorf("license %q", pricing.Source.License)
	}
}

func TestPriceIsQuoteCostNilWhenUnpriced(t *testing.T) {
	pricing := mustLoad(t, LoadPricingOptions{})
	if got := pricing.Price(query("openai/gpt-6-luna", 100_000, 0)); got == nil || *got != 0.01 {
		t.Errorf("price %s, want 0.01", costOrNil(got))
	}
	if got := pricing.Price(query("example/unknown", 1, 1)); got != nil {
		t.Errorf("price %s, want nil", costOrNil(got))
	}
}

func TestKnowsGatewayIDAndBareAlias(t *testing.T) {
	pricing := mustLoad(t, LoadPricingOptions{})
	if !pricing.Knows("openai/gpt-6-luna") || !pricing.Knows("gpt-6-luna") || pricing.Knows("example/unknown") {
		t.Error("knows")
	}
}

func TestQueryWithoutTimePricesAtInjectedClock(t *testing.T) {
	before := mustLoad(t, LoadPricingOptions{Now: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }})
	after := mustLoad(t, LoadPricingOptions{Now: func() time.Time { return time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC) }})
	q := types.PriceQuery{Model: "gpt-6-luna", InputTokens: 1000, OutputTokens: 1000}
	if got := before.Price(q); got != nil {
		t.Errorf("before %s", costOrNil(got))
	}
	if got := after.Price(q); got == nil || *got != 0.0006 {
		t.Errorf("after %s", costOrNil(got))
	}
	if before.Knows("gpt-6-luna") {
		t.Error("before knows")
	}
}

func TestReasoningTokensBillAsOutput(t *testing.T) {
	pricing := mustLoad(t, LoadPricingOptions{})
	if got := pricing.Price(query("openai/gpt-6-sol", 100, 1000)); got == nil || *got != 0.0102 {
		t.Errorf("price %s", costOrNil(got))
	}
}

func TestResolveProductionFollowsHostedSpellings(t *testing.T) {
	pricing := mustLoad(t, LoadPricingOptions{})
	at := base.At
	cases := []struct {
		provider, model *string
		want            *string
	}{
		{str("anthropic"), str("claude-sonnet-5-5"), str("anthropic/claude-sonnet-5.5")},
		{str("openai"), str("gpt-6-luna"), str("openai/gpt-6-luna")},
		{str("vercel"), str("openai/gpt-6-sol"), str("openai/gpt-6-sol")},
		{str("openai"), str("openai/gpt-6-sol"), str("openai/gpt-6-sol")},
		{nil, str("anthropic/claude-sonnet-5.5"), str("anthropic/claude-sonnet-5.5")},
		{nil, str("gpt-6-luna"), str("openai/gpt-6-luna")},
		{str("example"), str("nothing"), nil},
		{str("openai"), nil, nil},
	}
	for _, c := range cases {
		if got := pricing.ResolveProduction(c.provider, c.model, at); strOrNil(got) != strOrNil(c.want) {
			t.Errorf("resolveProduction(%s, %s) = %s, want %s", strOrNil(c.provider), strOrNil(c.model), strOrNil(got), strOrNil(c.want))
		}
	}
}

func tinyCatalog() *CatalogDocument {
	example := "example"
	verified := "2026-01-01"
	return &CatalogDocument{
		Source:            CatalogSource{Repository: "https://example.com/catalog", Path: "prices.yaml", Commit: strings.Repeat("0", 40), License: "Apache-2.0"},
		Version:           "2026-01-01",
		Currency:          "USD",
		PricingVerifiedAt: &verified,
		Models: []CatalogModel{
			{
				CanonicalID: "example/model-a",
				Publisher:   &example,
				Aliases: []CatalogAlias{
					{Provider: "example", Alias: "example/model-a", Channel: "example-api"},
					{Provider: "unknown", Alias: "model-a", Channel: "example-api"},
					{Provider: "bedrock", Alias: "example.model-a", Channel: "aws-bedrock", PriceRegion: []string{"us-east-1"}},
					{Provider: "bedrock", Alias: "global.example.model-a", Channel: "aws-bedrock", PriceRegion: []string{"global"}},
					{Provider: "unknown", Alias: "reused", Channel: "example-api", EffectiveTo: "2026-06-01"},
				},
				Prices: []CatalogPrice{
					{Channel: "example-api", Region: "global", EffectiveFrom: "2026-01-01", InputPerMtok: str("1"), OutputPerMtok: str("2"), SourceURL: "https://example.com"},
					{Channel: "aws-bedrock", Region: "global", EffectiveFrom: "2026-01-01", InputPerMtok: str("3"), OutputPerMtok: str("4"), SourceURL: "https://example.com"},
					{Channel: "aws-bedrock", Region: "us-east-1", EffectiveFrom: "2026-01-01", InputPerMtok: str("5"), OutputPerMtok: str("6"), SourceURL: "https://example.com"},
				},
			},
			{
				CanonicalID: "example/model-b",
				Publisher:   &example,
				Aliases: []CatalogAlias{
					{Provider: "unknown", Alias: "reused", Channel: "example-api", EffectiveFrom: "2026-06-01"},
					{Provider: "example", Alias: "example/model-b", Channel: "example-api"},
				},
				Prices: []CatalogPrice{
					{Channel: "example-api", Region: "global", EffectiveFrom: "2026-01-01", OutputPerMtok: str("8"), SourceURL: "https://example.com"},
				},
			},
		},
	}
}

func TestReusedSpellingResolvesToModelAtTheTime(t *testing.T) {
	local := mustLoad(t, LoadPricingOptions{Document: tinyCatalog()})
	q := types.PriceQuery{Model: "reused", InputTokens: 0, OutputTokens: 1_000_000}
	q.At = "2026-05-31T23:59:59Z"
	if got := strOrNil(local.Quote(q).CanonicalModel); got != "example/model-a" {
		t.Errorf("before: %s", got)
	}
	q.At = "2026-06-01"
	if got := strOrNil(local.Quote(q).CanonicalModel); got != "example/model-b" {
		t.Errorf("after: %s", got)
	}
}

func TestMissingInputRateIsPartial(t *testing.T) {
	local := mustLoad(t, LoadPricingOptions{Document: tinyCatalog()})
	quote := local.Quote(types.PriceQuery{Model: "example/model-b", InputTokens: 10, OutputTokens: 10, At: "2026-02-01"})
	if quote.Status != "partial" || !reflect.DeepEqual(quote.Reasons, []string{"input_rate_unavailable"}) || costOrNil(quote.CostUSD) != "8e-05" {
		t.Errorf("quote %+v cost %s", quote, costOrNil(quote.CostUSD))
	}
}

func TestRegionPinnedAliasesPriceOnDeploymentRegion(t *testing.T) {
	q := types.PriceQuery{Model: "model-a", InputTokens: 1_000_000, OutputTokens: 0, At: "2026-02-01"}
	// Bare aliases go through "unknown"; reach the bedrock aliases through a document copy.
	document := tinyCatalog()
	document.Models[0].Aliases = append(document.Models[0].Aliases,
		CatalogAlias{Provider: "unknown", Alias: "regional", Channel: "aws-bedrock", PriceRegion: []string{"us-east-1"}},
	)
	east := mustLoad(t, LoadPricingOptions{Document: document, Region: "us-east-1"})
	global := mustLoad(t, LoadPricingOptions{Document: document})
	regional := q
	regional.Model = "regional"
	if got := east.Price(regional); got == nil || *got != 5 {
		t.Errorf("east %s", costOrNil(got))
	}
	if got := global.Price(regional); got != nil {
		t.Errorf("global regional %s", costOrNil(got))
	}
	if got := global.Price(q); got == nil || *got != 1 {
		t.Errorf("global %s", costOrNil(got))
	}
}

func TestOverlappingAliasWindowsRefusedAtLoad(t *testing.T) {
	document := tinyCatalog()
	document.Models[1].Aliases = append(document.Models[1].Aliases, CatalogAlias{Provider: "unknown", Alias: "model-a", Channel: "example-api"})
	_, err := LoadPricing(LoadPricingOptions{Document: document})
	if err == nil || !strings.Contains(err.Error(), "overlapping alias windows") {
		t.Errorf("err %v", err)
	}
}

func TestCacheReadRule(t *testing.T) {
	cases := []struct {
		publisher, channel *string
		rules              map[string]any
		want               bool
	}{
		{str("anthropic"), str("anthropic-api"), map[string]any{}, false},
		{str("openai"), str("openai-api"), map[string]any{}, true},
		{str("OpenAI"), str(" OPENAI-API "), map[string]any{}, true},
		{str("anthropic"), str("vercel-ai-gateway"), map[string]any{"input_includes_cache_read": true}, true},
		{str("openai"), str("openai-api"), map[string]any{"input_includes_cache_read": false}, false},
		{nil, str("openai-api"), map[string]any{}, false},
	}
	for _, c := range cases {
		if got := CountsCacheReadInInput(c.publisher, c.channel, c.rules); got != c.want {
			t.Errorf("CountsCacheReadInInput(%s, %s, %v) = %v", strOrNil(c.publisher), strOrNil(c.channel), c.rules, got)
		}
	}
}

func TestTimesWithoutZoneAreUTC(t *testing.T) {
	cases := map[string]time.Time{
		"2026-10-01":                time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		"2026-10-01T08:30:00":       time.Date(2026, 10, 1, 8, 30, 0, 0, time.UTC),
		"2026-10-01T08:30:00+02:00": time.Date(2026, 10, 1, 6, 30, 0, 0, time.UTC),
	}
	for text, want := range cases {
		got, err := ParseInstant(text)
		if err != nil || got != want.UnixMilli() {
			t.Errorf("ParseInstant(%q) = %d, %v", text, got, err)
		}
	}
	if _, err := ParseInstant("yesterday"); err == nil {
		t.Error("yesterday parsed")
	}
}

func TestFreePricing(t *testing.T) {
	free := FreePricing()
	if !free.Knows("llama3.2") {
		t.Error("knows")
	}
	if got := free.Price(types.PriceQuery{Model: "llama3.2", InputTokens: 10, OutputTokens: 10}); got == nil || *got != 0 {
		t.Errorf("price %s", costOrNil(got))
	}
}

func TestDecimalRounding(t *testing.T) {
	check := func(name string, got *big.Int, want int64) {
		if got.Cmp(big.NewInt(want)) != 0 {
			t.Errorf("%s = %s, want %d", name, got, want)
		}
	}
	check("half up 0.000000005", UnitsHalfUp(*Dec("0.000000005"), 8), 1)
	check("half up 0.0000000049", UnitsHalfUp(*Dec("0.0000000049"), 8), 0)
	check("half up -0.000000005", UnitsHalfUp(*Dec("-0.000000005"), 8), -1)
	check("up 0.00010001", UnitsUp(*Dec("0.00010001"), 4), 2)
	check("up 0.0001", UnitsUp(*Dec("0.0001"), 4), 1)
	if d := Dec("1e-7"); d == nil || d.N.Int64() != 1 || d.S != 7 {
		t.Errorf("1e-7: %+v", d)
	}
	if d := Dec("2.5E3"); d == nil || d.N.Int64() != 2500 || d.S != 0 {
		t.Errorf("2.5E3: %+v", d)
	}
	if Dec("abc") != nil || Dec(true) != nil || Dec(nanValue()) != nil {
		t.Error("non-decimals parsed")
	}
}

func nanValue() float64 {
	zero := 0.0
	return zero / zero
}
