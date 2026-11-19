package pricing

// Prices for models the bundled catalog does not know, from Metergraph's free
// Pricing API (POST /v1/pricing/lookup). Only model names and the channel are
// sent: never a trace, a prompt or a key. Answers are cached on disk for a day,
// unknown models included, so a run asks at most once a day per model.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultPricingURL is Metergraph's Pricing API.
const DefaultPricingURL = "https://app.metergraph.dev"

const (
	lookupPath       = "/v1/pricing/lookup"
	lookupCacheFile  = "pricing-lookup.json"
	lookupCacheTTL   = 24 * time.Hour
	lookupMaxBody    = 4 << 20
	lookupBatch      = 100
	lookupTimeout    = 10 * time.Second
	gatewayChannel   = "vercel-ai-gateway"
	lookupCacheShape = "metergraph-replay.pricing-lookup/v1"
)

type LookupOptions struct {
	// The API's base URL. Default DefaultPricingURL.
	BaseURL string
	// Nil: a client with a 10 second timeout.
	Client *http.Client
	// Where answers are cached. Empty: no cache.
	CacheDir string
}

// LookupResult says what a lookup found. Asked counts the models sent to the
// API; the rest came from the cache.
type LookupResult struct {
	Found   []string
	Missing []string
	Asked   int
}

type lookupRecord struct {
	Model   string            `json:"model"`
	Context map[string]string `json:"context,omitempty"`
}

type lookupPrice struct {
	ID string `json:"id"`
	CatalogPrice
	Currency string `json:"currency"`
}

type lookupAnswer struct {
	Status  string       `json:"status"`
	ModelID *string      `json:"model_id"`
	Price   *lookupPrice `json:"price"`
}

type cachedAnswer struct {
	FetchedAt string       `json:"fetchedAt"`
	Answer    lookupAnswer `json:"answer"`
}

type lookupCache struct {
	Schema  string                  `json:"schema"`
	Answers map[string]cachedAnswer `json:"answers"`
}

// lookupQuery is what the API is asked for a model as Replay prices it: a
// "creator/model" id on the gateway channel. A bare id is never asked: the
// API needs the provider that billed it, which Replay does not know.
func lookupQuery(model string) lookupRecord {
	return lookupRecord{Model: strings.ToLower(jsTrim(model)), Context: map[string]string{"channel": gatewayChannel}}
}

// LookUp prices the models this catalog cannot price, through the Pricing
// API, and adds what it finds to the catalog. A network or API failure leaves
// the catalog as it was and is returned; the models stay unpriced.
func (c *CatalogPricing) LookUp(ctx context.Context, models []string, options LookupOptions) (LookupResult, error) {
	var result LookupResult
	wanted := map[string]bool{}
	for _, model := range models {
		key := strings.ToLower(jsTrim(model))
		if strings.Contains(key, "/") && !c.Knows(key) {
			wanted[key] = true
		}
	}
	if len(wanted) == 0 {
		return result, nil
	}
	names := make([]string, 0, len(wanted))
	for name := range wanted {
		names = append(names, name)
	}
	sort.Strings(names)

	now := c.now()
	cache := readLookupCache(options.CacheDir)
	answers := map[string]lookupAnswer{}
	var ask []string
	for _, name := range names {
		if hit, ok := cache.Answers[name]; ok {
			if at, err := time.Parse(time.RFC3339, hit.FetchedAt); err == nil && now.Sub(at) >= 0 && now.Sub(at) < lookupCacheTTL {
				answers[name] = hit.Answer
				continue
			}
		}
		ask = append(ask, name)
	}
	var askErr error
	if len(ask) > 0 {
		fetched, err := fetchLookups(ctx, ask, options)
		if err != nil {
			askErr = err
		} else {
			result.Asked = len(ask)
			stamp := now.UTC().Format(time.RFC3339)
			for name, answer := range fetched {
				answers[name] = answer
				cache.Answers[name] = cachedAnswer{FetchedAt: stamp, Answer: answer}
			}
			writeLookupCache(options.CacheDir, cache)
		}
	}

	var added []CatalogModel
	for _, name := range names {
		answer, ok := answers[name]
		if model, priced := catalogModelOf(name, answer); ok && priced {
			added = append(added, model)
			result.Found = append(result.Found, name)
		} else {
			result.Missing = append(result.Missing, name)
		}
	}
	if len(added) > 0 {
		if err := c.extend(added); err != nil {
			return LookupResult{Missing: names}, err
		}
		// A model the answer names may still not price, such as a row
		// without the rates a call needs: count it as missing.
		found := result.Found[:0]
		for _, name := range result.Found {
			if c.Knows(name) {
				found = append(found, name)
			} else {
				result.Missing = append(result.Missing, name)
			}
		}
		result.Found = found
		sort.Strings(result.Missing)
	}
	return result, askErr
}

// catalogModelOf is the catalog entry an API answer adds: the price row, and
// one alias that resolves the name Replay asked for.
func catalogModelOf(name string, answer lookupAnswer) (CatalogModel, bool) {
	if answer.Status == "unpriced" || answer.Price == nil || answer.ModelID == nil || *answer.ModelID == "" {
		return CatalogModel{}, false
	}
	if answer.Price.Currency != "" && !strings.EqualFold(answer.Price.Currency, "USD") {
		return CatalogModel{}, false
	}
	canonical := *answer.ModelID
	publisher := canonical
	if slash := strings.Index(canonical, "/"); slash > 0 {
		publisher = canonical[:slash]
	}
	// Priced as Replay prices a gateway call: under the creator in the id.
	alias := CatalogAlias{Provider: name[:strings.Index(name, "/")], Alias: name, Channel: answer.Price.Channel}
	if answer.Price.Channel == gatewayChannel {
		// The gateway's OpenAI-shaped usage counts cache reads inside
		// prompt_tokens, as the bundled gateway aliases record.
		alias.Rules = map[string]any{"input_includes_cache_read": true}
	}
	price := answer.Price.CatalogPrice
	if price.Region == "" {
		price.Region = globalRegion
	}
	return CatalogModel{
		CanonicalID: canonical,
		Publisher:   &publisher,
		Aliases:     []CatalogAlias{alias},
		Prices:      []CatalogPrice{price},
	}, true
}

// extend rebuilds the catalog with these models added. A model the catalog
// already has keeps its rows; the answer's alias and price are added to it.
func (c *CatalogPricing) extend(models []CatalogModel) error {
	document := *c.document
	document.Models = append([]CatalogModel(nil), c.document.Models...)
	index := map[string]int{}
	for i, m := range document.Models {
		index[m.CanonicalID] = i
	}
	for _, m := range models {
		if i, ok := index[m.CanonicalID]; ok {
			existing := document.Models[i]
			existing.Aliases = append(append([]CatalogAlias(nil), existing.Aliases...), m.Aliases...)
			existing.Prices = append([]CatalogPrice(nil), existing.Prices...)
			for _, row := range m.Prices {
				if !hasRow(existing.Prices, row) {
					existing.Prices = append(existing.Prices, row)
				}
			}
			document.Models[i] = existing
			continue
		}
		index[m.CanonicalID] = len(document.Models)
		document.Models = append(document.Models, m)
	}
	s, err := newSnapshot(&document, c.region)
	if err != nil {
		return err
	}
	c.document, c.snapshot = &document, s
	c.looked += len(models)
	return nil
}

// hasRow says the catalog already has a row for this channel, region and date.
func hasRow(rows []CatalogPrice, row CatalogPrice) bool {
	for _, r := range rows {
		if r.Channel == row.Channel && strings.EqualFold(r.Region, row.Region) && r.EffectiveFrom == row.EffectiveFrom {
			return true
		}
	}
	return false
}

func fetchLookups(ctx context.Context, names []string, options LookupOptions) (map[string]lookupAnswer, error) {
	base := strings.TrimRight(options.BaseURL, "/")
	if base == "" {
		base = DefaultPricingURL
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: lookupTimeout}
	}
	out := map[string]lookupAnswer{}
	for start := 0; start < len(names); start += lookupBatch {
		batch := names[start:min(start+lookupBatch, len(names))]
		records := make([]lookupRecord, len(batch))
		for i, name := range batch {
			records[i] = lookupQuery(name)
		}
		body, _ := json.Marshal(map[string]any{"records": records})
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+lookupPath, bytes.NewReader(body))
		if err != nil {
			return nil, errors.New("the Pricing API address is not a URL")
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", "metergraph-replay")
		response, err := client.Do(request)
		if err != nil {
			return nil, errors.New("the Pricing API did not answer")
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, lookupMaxBody))
		response.Body.Close()
		if err != nil {
			return nil, errors.New("the Pricing API's answer was cut off")
		}
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("the Pricing API answered HTTP %d", response.StatusCode)
		}
		var decoded struct {
			Results []lookupAnswer `json:"results"`
		}
		if json.Unmarshal(data, &decoded) != nil || len(decoded.Results) != len(batch) {
			return nil, errors.New("the Pricing API's answer was not in the expected shape")
		}
		for i, name := range batch {
			out[name] = decoded.Results[i]
		}
	}
	return out, nil
}

func readLookupCache(dir string) lookupCache {
	empty := lookupCache{Schema: lookupCacheShape, Answers: map[string]cachedAnswer{}}
	if dir == "" {
		return empty
	}
	data, err := os.ReadFile(filepath.Join(dir, lookupCacheFile))
	if err != nil {
		return empty
	}
	var cache lookupCache
	if json.Unmarshal(data, &cache) != nil || cache.Schema != lookupCacheShape || cache.Answers == nil {
		return empty
	}
	return cache
}

// writeLookupCache replaces the cache file whole. A failure is ignored: the
// next run asks again.
func writeLookupCache(dir string, cache lookupCache) {
	if dir == "" {
		return
	}
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	data, err := json.MarshalIndent(cache, "", " ")
	if err != nil {
		return
	}
	temp, err := os.CreateTemp(dir, lookupCacheFile+".*")
	if err != nil {
		return
	}
	_, werr := temp.Write(append(data, '\n'))
	cerr := temp.Close()
	if werr != nil || cerr != nil || os.Rename(temp.Name(), filepath.Join(dir, lookupCacheFile)) != nil {
		os.Remove(temp.Name())
	}
}
