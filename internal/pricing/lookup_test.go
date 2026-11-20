package pricing

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/metergraph/replay/internal/types"
)

// A synthetic answer in the API's published shape.
const lookupPriced = `{"status":"priced","model_id":"examplelab/model-9","price":{"id":"examplelab/model-9:vercel-ai-gateway:global:2026-11-01","channel":"vercel-ai-gateway","region":"global","currency":"USD","input_per_mtok":"1.0","output_per_mtok":"4.0","cache_read_per_mtok":"0.1","cache_write_5m_per_mtok":null,"rules":{},"effective_from":"2026-11-01","effective_to":null,"source_url":"https://example.com/pricing"},"effective_at":"2026-11-19T12:00:00Z","currency":"USD","missing_dimensions":[],"warnings":[]}`

const lookupUnknown = `{"status":"unpriced","model_id":null,"price":null,"effective_at":"2026-11-19T12:00:00Z","currency":null,"missing_dimensions":[],"warnings":[{"code":"unknown_model","message":"not in the catalog"}]}`

type fakeAPI struct {
	server   *httptest.Server
	calls    atomic.Int32
	lastBody atomic.Value
}

func newFakeAPI(t *testing.T, status int) *fakeAPI {
	t.Helper()
	api := &fakeAPI{}
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		api.lastBody.Store(string(body))
		if r.Method != http.MethodPost || r.URL.Path != "/v1/pricing/lookup" {
			http.NotFound(w, r)
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		var request struct {
			Records []lookupRecord `json:"records"`
		}
		json.Unmarshal(body, &request)
		results := make([]string, len(request.Records))
		for i, record := range request.Records {
			results[i] = lookupUnknown
			if record.Model == "examplelab/model-9" {
				results[i] = lookupPriced
			}
		}
		io.WriteString(w, `{"results":[`+strings.Join(results, ",")+`]}`)
	}))
	t.Cleanup(api.server.Close)
	return api
}

func clockAt(at string) func() time.Time {
	moment, _ := time.Parse(time.RFC3339, at)
	return func() time.Time { return moment }
}

func TestLookUpAddsWhatTheAPIPrices(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	c, err := LoadPricing(LoadPricingOptions{Now: clockAt("2026-11-19T12:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	if c.Knows("examplelab/model-9") {
		t.Fatal("the bundled catalog already knows the synthetic model")
	}
	before := c.CatalogVersion()
	result, err := c.LookUp(context.Background(), []string{"examplelab/model-9", "examplelab/nothing", "openai/gpt-6-sol"}, LookupOptions{BaseURL: api.server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(result.Found, ",") != "examplelab/model-9" || strings.Join(result.Missing, ",") != "examplelab/nothing" || result.Asked != 2 {
		t.Fatalf("result %+v", result)
	}
	// A model the catalog already prices is never sent.
	body := api.lastBody.Load().(string)
	if strings.Contains(body, "gpt-6-sol") || !strings.Contains(body, `"channel":"vercel-ai-gateway"`) {
		t.Errorf("request body %s", body)
	}
	// 1000 input tokens at $1/M and 500 output at $4/M.
	cost := c.Price(types.PriceQuery{Model: "examplelab/model-9", InputTokens: 1000, OutputTokens: 500})
	if cost == nil || *cost != 0.003 {
		t.Errorf("cost %v", cost)
	}
	if got := c.CatalogVersion(); got != before+" + Metergraph Pricing API (1 model)" {
		t.Errorf("version %q", got)
	}
}

func TestLookUpSendsNothingWhenAllPriced(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	c, _ := LoadPricing(LoadPricingOptions{})
	result, err := c.LookUp(context.Background(), []string{"openai/gpt-6-sol"}, LookupOptions{BaseURL: api.server.URL})
	if err != nil || api.calls.Load() != 0 || len(result.Found)+len(result.Missing) != 0 {
		t.Fatalf("%+v %v, %d calls", result, err, api.calls.Load())
	}
}

func TestLookUpCachesForADay(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	dir := t.TempDir()
	models := []string{"examplelab/model-9", "examplelab/nothing"}
	for _, at := range []string{"2026-11-19T12:00:00Z", "2026-11-20T11:00:00Z"} {
		c, _ := LoadPricing(LoadPricingOptions{Now: clockAt(at)})
		result, err := c.LookUp(context.Background(), models, LookupOptions{BaseURL: api.server.URL, CacheDir: dir})
		if err != nil || len(result.Found) != 1 {
			t.Fatalf("%s: %+v %v", at, result, err)
		}
	}
	if api.calls.Load() != 1 {
		t.Errorf("a second run within a day asked again: %d calls", api.calls.Load())
	}
	info, err := os.Stat(filepath.Join(dir, lookupCacheFile))
	// Windows has no Unix permission bits: Go reports every file as 0666 there.
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		t.Errorf("cache file %v %v", info, err)
	}
	c, _ := LoadPricing(LoadPricingOptions{Now: clockAt("2026-11-21T12:00:01Z")})
	if _, err := c.LookUp(context.Background(), models, LookupOptions{BaseURL: api.server.URL, CacheDir: dir}); err != nil {
		t.Fatal(err)
	}
	if api.calls.Load() != 2 {
		t.Errorf("a day-old answer was reused: %d calls", api.calls.Load())
	}
}

func TestLookUpFailureLeavesCatalogAlone(t *testing.T) {
	for name, base := range map[string]string{
		"error status": newFakeAPI(t, http.StatusServiceUnavailable).server.URL,
		"no server":    "http://127.0.0.1:1",
	} {
		c, _ := LoadPricing(LoadPricingOptions{})
		before := c.CatalogVersion()
		result, err := c.LookUp(context.Background(), []string{"examplelab/model-9"}, LookupOptions{BaseURL: base})
		if err == nil || len(result.Found) != 0 || strings.Join(result.Missing, ",") != "examplelab/model-9" {
			t.Errorf("%s: %+v %v", name, result, err)
		}
		if c.Knows("examplelab/model-9") || c.CatalogVersion() != before {
			t.Errorf("%s: the catalog changed", name)
		}
		if err != nil && strings.Contains(err.Error(), "127.0.0.1") {
			t.Errorf("%s: the error names the address: %v", name, err)
		}
	}
}

func TestLookUpRejectsOtherCurrencies(t *testing.T) {
	answer := lookupAnswer{}
	json.Unmarshal([]byte(strings.Replace(lookupPriced, `"currency":"USD","input`, `"currency":"EUR","input`, 1)), &answer)
	if _, ok := catalogModelOf("examplelab/model-9", answer); ok {
		t.Error("a EUR price was taken as USD")
	}
}

func TestLookUpAddsAnAliasToAModelTheCatalogHas(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The API's answer for a gateway id the bundled catalog files under
		// another provider: a row the catalog already has.
		answer := strings.NewReplacer("examplelab/model-9", "openai/gpt-6-sol", "2026-11-01", "2026-09-23", `"1.0"`, `"2"`, `"4.0"`, `"10"`).Replace(lookupPriced)
		io.WriteString(w, `{"results":[`+answer+`]}`)
	}))
	defer api.Close()
	c, _ := LoadPricing(LoadPricingOptions{})
	result, err := c.LookUp(context.Background(), []string{"examplegw/gpt-6-sol"}, LookupOptions{BaseURL: api.URL})
	if err != nil || strings.Join(result.Found, ",") != "examplegw/gpt-6-sol" {
		t.Fatalf("%+v %v", result, err)
	}
	direct := c.Price(types.PriceQuery{Model: "openai/gpt-6-sol", InputTokens: 1000, OutputTokens: 100})
	via := c.Price(types.PriceQuery{Model: "examplegw/gpt-6-sol", InputTokens: 1000, OutputTokens: 100})
	if direct == nil || via == nil || *direct != *via {
		t.Errorf("direct %v, via the new alias %v", costOrNil(direct), costOrNil(via))
	}
}

func TestLookUpNeverSendsBareIDs(t *testing.T) {
	api := newFakeAPI(t, http.StatusOK)
	c, _ := LoadPricing(LoadPricingOptions{})
	result, err := c.LookUp(context.Background(), []string{"made-up-model"}, LookupOptions{BaseURL: api.server.URL})
	if err != nil || api.calls.Load() != 0 || len(result.Found)+len(result.Missing) != 0 {
		t.Fatalf("%+v %v, %d calls", result, err, api.calls.Load())
	}
}
