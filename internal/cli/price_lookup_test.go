package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// A synthetic Pricing API that prices one made-up model.
func pricingAPI(t *testing.T, asked *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "Say hello") || strings.Contains(string(body), key) {
			t.Errorf("the lookup sent more than model names: %s", body)
		}
		answer := `{"status":"unpriced","model_id":null,"price":null}`
		if strings.Contains(string(body), "examplelab/model-9") {
			answer = `{"status":"priced","model_id":"examplelab/model-9","price":{"channel":"vercel-ai-gateway","region":"global","currency":"USD","input_per_mtok":"1.0","output_per_mtok":"4.0","effective_from":"2026-01-01","source_url":"https://example.com/pricing"}}`
		}
		io.WriteString(w, `{"results":[`+answer+`]}`)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestEstimateLooksUpUnpricedModels(t *testing.T) {
	var asked atomic.Int32
	api := pricingAPI(t, &asked)
	file := writeTraces(t, 2, nil)
	withAPI := func(o *IO) { o.PricingURL = api.URL; o.CacheDir = t.TempDir() }

	out := cli(t, args("estimate", file, "--models", "examplelab/model-9"), withAPI, nil)
	if out.code != ExitOK || asked.Load() != 1 {
		t.Fatalf("%d, %d lookups\n%s%s", out.code, asked.Load(), out.stdout, out.stderr)
	}
	match(t, `Prices from Metergraph's Pricing API: examplelab/model-9\. Only model names were sent\.`, out.stdout)
	if strings.Contains(out.stdout, "No price for") {
		t.Errorf("still unpriced:\n%s", out.stdout)
	}

	// Turned off: nothing is asked and the model stays unpriced.
	for name, change := range map[string]func(*IO){
		"--no-price-lookup": withAPI,
		"no URL":            func(o *IO) { o.PricingURL = "" },
	} {
		before := asked.Load()
		extra := []string{}
		if name == "--no-price-lookup" {
			extra = []string{"--no-price-lookup"}
		}
		out := cli(t, args("estimate", file, "--models", "examplelab/model-9", extra), change, nil)
		if asked.Load() != before || !strings.Contains(out.stdout, "No price for: examplelab/model-9") {
			t.Errorf("%s: asked %d times\n%s", name, asked.Load()-before, out.stdout)
		}
	}

	// A model the API does not know stays unpriced, and run still refuses it.
	out = cli(t, args("run", file, "--models", "examplelab/unknown", "--yes"), withAPI, nil)
	if out.code != ExitRefused || !strings.Contains(out.stderr, "examplelab/unknown") {
		t.Errorf("%d %s", out.code, out.stderr)
	}
}

func TestPriceLookupFailureIsANote(t *testing.T) {
	file := writeTraces(t, 2, nil)
	out := cli(t, args("estimate", file, "--models", "examplelab/model-9"), func(o *IO) { o.PricingURL = "http://127.0.0.1:1" }, nil)
	if out.code != ExitOK {
		t.Fatalf("%d %s", out.code, out.stderr)
	}
	match(t, `Could not look up prices for examplelab/model-9: the Pricing API did not answer\.`, out.stdout)
	match(t, `No price for: examplelab/model-9`, out.stdout)
}

func TestHTMLPage(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/map.html"
	out := cli(t, args("demo", "--map", "--html", path), nil, nil)
	if out.code != ExitOK || !strings.Contains(out.stdout, "share it only where the traces may go") {
		t.Fatalf("%d %s %s", out.code, out.stdout, out.stderr)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() < 1000 {
		t.Fatalf("%v %v", info, err)
	}
	if out := cli(t, args("estimate", writeTraces(t, 1, nil), models, "--html", path), nil, nil); out.code != ExitUsage {
		t.Errorf("estimate took --html: %d", out.code)
	}
	if out := cli(t, args("demo", "--map", "--html", ""), nil, nil); out.code != ExitUsage {
		t.Errorf("an empty --html passed: %d", out.code)
	}
}
