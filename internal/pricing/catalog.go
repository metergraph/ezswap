// Package pricing holds the price catalog and cost function, and the run
// estimate built on them.
package pricing

// The price catalog and cost function.
//
// The catalog is metergraph-core's public prices.yaml, converted to JSON by
// cmd/build-prices. Models it lacks can be added at run time from
// Metergraph's Pricing API (lookup.go). Resolution follows metergraph-core:
// an alias is looked up by (provider, model) and is effective-dated and
// region-pinned; the price is the row effective at the call's time for the
// alias's model and channel; cache reads come out of the input count where the
// publisher reports them inside it; long-context and off-peak multipliers
// apply to every rate. A router model id ("creator/model") is looked up under
// its creator. Matches Metergraph's hosted figures (conformance/pricing.json).
//
// Reasoning tokens bill as output: they are inside the output count and the
// catalog has no separate rate for them.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/metergraph/ezswap/internal/types"
)

// ---------------------------------------------------------------------------
// Catalog document

type CatalogSource struct {
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Commit     string `json:"commit"`
	License    string `json:"license"`
}

type CatalogDocument struct {
	Source            CatalogSource  `json:"source"`
	Version           string         `json:"version"`
	Currency          string         `json:"currency"`
	PricingVerifiedAt *string        `json:"pricing_verified_at"`
	Models            []CatalogModel `json:"models"`
}

type CatalogModel struct {
	CanonicalID string         `json:"canonical_id"`
	Publisher   *string        `json:"publisher"`
	Aliases     []CatalogAlias `json:"aliases"`
	Prices      []CatalogPrice `json:"prices"`
}

type CatalogAlias struct {
	Provider      string         `json:"provider"`
	Alias         string         `json:"alias"`
	Channel       string         `json:"channel"`
	Rules         map[string]any `json:"rules,omitempty"`
	EffectiveFrom string         `json:"effective_from,omitempty"`
	EffectiveTo   string         `json:"effective_to,omitempty"`
	PriceRegion   []string       `json:"price_region,omitempty"`
}

type CatalogPrice struct {
	Channel             string         `json:"channel"`
	Region              string         `json:"region"`
	EffectiveFrom       string         `json:"effective_from"`
	EffectiveTo         string         `json:"effective_to,omitempty"`
	InputPerMtok        *string        `json:"input_per_mtok,omitempty"`
	OutputPerMtok       *string        `json:"output_per_mtok,omitempty"`
	CacheReadPerMtok    *string        `json:"cache_read_per_mtok,omitempty"`
	CacheWrite5mPerMtok *string        `json:"cache_write_5m_per_mtok,omitempty"`
	CacheWrite1hPerMtok *string        `json:"cache_write_1h_per_mtok,omitempty"`
	BatchInputPerMtok   *string        `json:"batch_input_per_mtok,omitempty"`
	BatchOutputPerMtok  *string        `json:"batch_output_per_mtok,omitempty"`
	Rules               map[string]any `json:"rules,omitempty"`
	SourceURL           string         `json:"source_url"`
}

// ---------------------------------------------------------------------------
// Resolution tables, as metergraph-core defines them

var providerAliases = map[string]string{
	"amazon-bedrock": "bedrock",
	"aws":            "bedrock",
	"aws-bedrock":    "bedrock",
	"gemini":         "google",
	"google-genai":   "google",
	"moonshot":       "moonshotai",
	"perplexity":     "perplexity-ai",
	"xai":            "x-ai",
}

// The loader indexes a bedrock alias under these spellings too.
var providerSynonyms = map[string][]string{"bedrock": {"aws-bedrock", "aws"}}

// Publishers that report cache reads inside the input count, on these channels.
var inputIncludesCacheReadPairs = map[string]bool{
	"openai\x00openai-api":       true,
	"google\x00google-api":       true,
	"google\x00google-vertex-ai": true,
	"deepseek\x00deepseek-api":   true,
	"xai\x00xai-api":             true,
}

// A gateway names models "creator/model" and is priced under the creator.
var routerProviders = map[string]bool{"gateway": true, "vercel": true, "vercel-ai-gateway": true}

const (
	bedrockChannel      = "aws-bedrock"
	bedrockGlobalPrefix = "global."
	globalRegion        = "global"
	millionPlaces       = 6
	costPlaces          = 8
)

var (
	bedrockChannels      = map[string]bool{bedrockChannel: true, "aws-bedrock-geo": true}
	bedrockVersionSuffix = regexp.MustCompile(`-v\d+(?::\d+)?$`)
)

type alias struct {
	canonicalID string
	channel     string
	rules       map[string]any
	publisher   *string
	from        *int64
	to          *int64
	priceRegion []string
}

type price struct {
	id        string
	modelID   string
	channel   string
	region    string
	input     *Decimal
	output    *Decimal
	cacheRead *Decimal
	rules     map[string]any
	from      int64
	to        *int64
	publisher *string
}

// Quote is a cost with the catalog's reasons.
type Quote struct {
	CanonicalModel *string `json:"canonicalModel"`
	PriceID        *string `json:"priceId"`
	// Rounded to 1e-8 USD, as metergraph-core rounds. Nil when unpriced.
	CostUSD *float64 `json:"costUsd"`
	Status  string   `json:"status"` // "priced" | "partial" | "unpriced"
	Reasons []string `json:"reasons"`
}

type LoadPricingOptions struct {
	// Deployment region for region-pinned rows. Default "global".
	Region string
	// A catalog document in the prices.json shape. Default: the bundled one.
	Document *CatalogDocument
	// Clock for queries without At.
	Now func() time.Time
}

// ---------------------------------------------------------------------------
// Dates

var (
	dateOnly   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	hasZone    = regexp.MustCompile(`(?i)(?:z|[+-]\d{2}:?\d{2})$`)
	isoInstant = regexp.MustCompile(
		`^([+-]\d{6}|\d{4})-(\d{2})-(\d{2})(?:[Tt](\d{2}):(\d{2})(?::(\d{2})(?:\.(\d+))?)?)?(?:([Zz])|([+-])(\d{2}):?(\d{2}))$`,
	)
)

// ParseInstant is milliseconds since the epoch; a value without a zone is UTC, as in Python.
func ParseInstant(value string) (int64, error) {
	text := jsTrim(value)
	var iso string
	switch {
	case dateOnly.MatchString(text):
		iso = text + "T00:00:00Z"
	case hasZone.MatchString(text):
		iso = text
	default:
		iso = strings.Replace(text, " ", "T", 1) + "Z"
	}
	ms, ok := dateParse(iso)
	if !ok {
		return 0, fmt.Errorf("not an ISO date or time: %s", value)
	}
	return ms, nil
}

// dateParse is Date.parse for the ISO forms parseInstant builds. Like V8, a
// day past the month's end rolls into the next month, and 24:00 is midnight
// of the next day.
func dateParse(text string) (int64, bool) {
	m := isoInstant.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	num := func(s string) int {
		n, _ := strconv.Atoi(s)
		return n
	}
	year, month, day := num(m[1]), num(m[2]), num(m[3])
	hour, minute, second, millis := num(m[4]), num(m[5]), num(m[6]), 0
	if m[7] != "" {
		fraction := (m[7] + "00")[:3]
		millis = num(fraction)
	}
	if m[1] == "-000000" || month < 1 || month > 12 || day < 1 || day > 31 {
		return 0, false
	}
	if minute > 59 || second > 59 || hour > 24 || (hour == 24 && (minute > 0 || second > 0 || millis > 0)) {
		return 0, false
	}
	offset := 0
	if m[8] == "" {
		oh, om := num(m[10]), num(m[11])
		if oh > 23 || om > 59 {
			return 0, false
		}
		offset = (oh*60 + om) * 60
		if m[9] == "-" {
			offset = -offset
		}
	}
	at := time.Date(year, time.Month(month), day, hour, minute, second, millis*int(time.Millisecond), time.UTC)
	return at.UnixMilli() - int64(offset)*1000, true
}

func mustInstant(value string) int64 {
	ms, err := ParseInstant(value)
	if err != nil {
		panic(err)
	}
	return ms
}

func windowsOverlap(a, b alias) bool {
	return (a.to == nil || b.from == nil || *b.from < *a.to) && (b.to == nil || a.from == nil || *a.from < *b.to)
}

func inEffect(from, to *int64, at int64) bool {
	return (from == nil || *from <= at) && (to == nil || at < *to)
}

// ---------------------------------------------------------------------------
// Rules

func NormalizeProvider(provider string) string {
	key := strings.ToLower(jsTrim(provider))
	if folded, ok := providerAliases[key]; ok {
		return folded
	}
	return key
}

// CountsCacheReadInInput says whether cache reads come out of billable input for this row.
func CountsCacheReadInInput(publisher, channel *string, rules map[string]any) bool {
	if stated, ok := rules["input_includes_cache_read"]; ok && stated != nil {
		return truthy(stated)
	}
	if publisher == nil || channel == nil {
		return false
	}
	return inputIncludesCacheReadPairs[strings.ToLower(jsTrim(*publisher))+"\x00"+strings.ToLower(jsTrim(*channel))]
}

// tokens is a whole, non-negative count, or nil.
func tokens(value any) *float64 {
	switch value.(type) {
	case nil, bool:
		return nil
	}
	number := jsToNumber(value)
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return nil
	}
	whole := math.Trunc(number)
	if whole >= 0 {
		return &whole
	}
	return nil
}

func record(value any) map[string]any {
	if m, ok := value.(map[string]any); ok && m != nil {
		return m
	}
	return map[string]any{}
}

// offPeakMultiplier is the discount a row's provider applies outside its published peak hours.
func offPeakMultiplier(rules map[string]any, at int64) Decimal {
	discount := record(rules["off_peak_discount"])
	windows, _ := discount["peak_hours_utc"].([]any)
	multiplier := Dec(discount["multiplier"])
	if len(windows) == 0 || multiplier == nil {
		return One
	}
	moment := time.UnixMilli(at).UTC()
	weekday := (int(moment.Weekday()) + 6) % 7 // Monday 0, as Python's weekday()
	if truthy(discount["peak_weekdays_only"]) && weekday >= 5 {
		return *multiplier
	}
	hour := float64(moment.Hour())
	for _, item := range windows {
		window, ok := item.([]any)
		if !ok || len(window) != 2 {
			continue
		}
		start := tokens(window[0])
		end := tokens(window[1])
		if start == nil || end == nil {
			continue
		}
		if *start <= hour && hour < *end {
			return One
		}
	}
	return *multiplier
}

type fallbackID struct {
	id            string
	droppedGlobal bool
}

func bedrockFallbackIDs(modelKey string) []fallbackID {
	unversioned := bedrockVersionSuffix.ReplaceAllString(modelKey, "")
	var candidates []fallbackID
	if unversioned != modelKey {
		candidates = append(candidates, fallbackID{unversioned, false})
	}
	if strings.HasPrefix(modelKey, bedrockGlobalPrefix) {
		for _, name := range unique([]string{modelKey, unversioned}) {
			bare := strings.TrimPrefix(name, bedrockGlobalPrefix)
			if bare != "" {
				candidates = append(candidates, fallbackID{bare, true})
			}
		}
	}
	return candidates
}

func crossesPriceRegion(a alias) bool {
	return len(a.priceRegion) > 0 && !(len(a.priceRegion) == 1 && a.priceRegion[0] == globalRegion)
}

// unique keeps the first of each value, in order, as [...new Set(values)].
func unique(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The snapshot

type usage struct {
	inputTokens            *float64
	outputTokens           *float64
	cachedInputTokens      float64
	inputIncludesCacheRead *bool
	fullRate               bool
}

type snapshot struct {
	aliases map[string][]alias
	prices  map[string][]*price
	region  string
}

func newSnapshot(document *CatalogDocument, region string) (*snapshot, error) {
	s := &snapshot{aliases: map[string][]alias{}, prices: map[string][]*price{}, region: strings.ToLower(jsTrim(region))}
	instant := func(value string) (*int64, error) {
		if value == "" {
			return nil, nil
		}
		ms, err := ParseInstant(value)
		if err != nil {
			return nil, err
		}
		return &ms, nil
	}
	var priceKeys []string
	for _, entry := range document.Models {
		canonical := entry.CanonicalID
		var publisher *string
		if entry.Publisher != nil && *entry.Publisher != "" {
			p := strings.ToLower(jsTrim(*entry.Publisher))
			publisher = &p
		}
		for _, raw := range entry.Aliases {
			provider := strings.ToLower(raw.Provider)
			name := strings.ToLower(raw.Alias)
			from, err := instant(raw.EffectiveFrom)
			if err != nil {
				return nil, err
			}
			to, err := instant(raw.EffectiveTo)
			if err != nil {
				return nil, err
			}
			regions := make([]string, 0, len(raw.PriceRegion))
			for _, r := range raw.PriceRegion {
				regions = append(regions, strings.ToLower(jsTrim(r)))
			}
			a := alias{
				canonicalID: canonical,
				channel:     raw.Channel,
				rules:       record(raw.Rules),
				publisher:   publisher,
				from:        from,
				to:          to,
				priceRegion: unique(regions),
			}
			for _, key := range append([]string{provider}, providerSynonyms[provider]...) {
				id := key + "\x00" + name
				list := s.aliases[id]
				for _, other := range list {
					if windowsOverlap(a, other) {
						return nil, fmt.Errorf("%s: overlapping alias windows for %s/%s", canonical, key, name)
					}
				}
				s.aliases[id] = append(list, a)
			}
		}
		for _, raw := range entry.Prices {
			from, err := ParseInstant(raw.EffectiveFrom)
			if err != nil {
				return nil, err
			}
			to, err := instant(raw.EffectiveTo)
			if err != nil {
				return nil, err
			}
			region := raw.Region
			if region == "" {
				region = globalRegion
			}
			p := &price{
				id:        fmt.Sprintf("%s:%s:%s:%s", canonical, raw.Channel, raw.Region, isoDate(from)),
				modelID:   canonical,
				channel:   raw.Channel,
				region:    region,
				input:     decPtr(raw.InputPerMtok),
				output:    decPtr(raw.OutputPerMtok),
				cacheRead: decPtr(raw.CacheReadPerMtok),
				rules:     record(raw.Rules),
				from:      from,
				to:        to,
				publisher: publisher,
			}
			id := canonical + "\x00" + raw.Channel
			if _, ok := s.prices[id]; !ok {
				priceKeys = append(priceKeys, id)
			}
			s.prices[id] = append(s.prices[id], p)
		}
	}
	for _, id := range priceKeys {
		list := s.prices[id]
		sort.SliceStable(list, func(i, j int) bool { return list[i].from > list[j].from })
	}
	return s, nil
}

// isoDate is new Date(ms).toISOString().slice(0, 10).
func isoDate(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02")
}

func decPtr(value *string) *Decimal {
	if value == nil {
		return nil
	}
	return Dec(*value)
}

func (s *snapshot) aliasFor(candidates []alias, at int64) *alias {
	var effective []alias
	for _, a := range candidates {
		if inEffect(a.from, a.to, at) {
			effective = append(effective, a)
		}
	}
	for _, region := range unique([]string{s.region, globalRegion}) {
		for i := range effective {
			if contains(effective[i].priceRegion, region) {
				return &effective[i]
			}
		}
	}
	for i := range effective {
		if len(effective[i].priceRegion) == 0 {
			return &effective[i]
		}
	}
	return nil
}

func (s *snapshot) priceRegions(a *alias) []string {
	if len(a.priceRegion) == 0 {
		return unique([]string{s.region, "*", globalRegion})
	}
	for _, region := range []string{s.region, globalRegion} {
		if contains(a.priceRegion, region) {
			return []string{region}
		}
	}
	return nil
}

func (s *snapshot) priceFor(a *alias, at int64) *price {
	candidates := s.prices[a.canonicalID+"\x00"+a.channel]
	for _, region := range s.priceRegions(a) {
		for _, p := range candidates {
			if strings.ToLower(p.region) != region {
				continue
			}
			if inEffect(&p.from, p.to, at) {
				return p
			}
		}
	}
	return nil
}

// cost is metergraph-core's CatalogSnapshot.cost for an already-normalized
// provider key: alias, bedrock fallbacks, effective price, then the token cost.
func (s *snapshot) cost(providerKey, modelKey string, at int64, u usage) Quote {
	candidates := s.aliases[providerKey+"\x00"+modelKey]
	aliasKnown := len(candidates) > 0
	a := s.aliasFor(candidates, at)
	if a == nil && providerKey == "bedrock" {
		for _, fallback := range bedrockFallbackIDs(modelKey) {
			candidates = s.aliases[providerKey+"\x00"+fallback.id]
			aliasKnown = aliasKnown || len(candidates) > 0
			found := s.aliasFor(candidates, at)
			if found == nil || !bedrockChannels[found.channel] {
				continue
			}
			if fallback.droppedGlobal && (found.channel != bedrockChannel || crossesPriceRegion(*found)) {
				continue
			}
			a = found
			break
		}
	}
	if a == nil {
		reason := "unknown_model"
		if aliasKnown {
			reason = "no_effective_alias"
		}
		return unpriced(nil, reason)
	}
	p := s.priceFor(a, at)
	if p == nil {
		return unpriced(&a.canonicalID, "no_effective_price")
	}
	rules := make(map[string]any, len(p.rules)+len(a.rules))
	for k, v := range p.rules {
		rules[k] = v
	}
	for k, v := range a.rules {
		rules[k] = v
	}
	return priceTokens(a, p, rules, at, u)
}

func unpriced(canonicalModel *string, reason string) Quote {
	var model *string
	if canonicalModel != nil {
		m := *canonicalModel
		model = &m
	}
	return Quote{CanonicalModel: model, Status: "unpriced", Reasons: []string{reason}}
}

func priceTokens(a *alias, p *price, rules map[string]any, at int64, u usage) Quote {
	reasons := []string{}
	var inputCount, outputCount float64
	if count := tokensOf(u.inputTokens); count == nil {
		reasons = append(reasons, "missing_input_tokens")
	} else {
		inputCount = *count
	}
	if count := tokensOf(u.outputTokens); count == nil {
		reasons = append(reasons, "missing_output_tokens")
	} else {
		outputCount = *count
	}
	cacheRead := 0.0
	if count := tokens(u.cachedInputTokens); count != nil {
		cacheRead = *count
	}

	rowIncludes := CountsCacheReadInInput(p.publisher, &p.channel, rules)
	// A caller that knows how its count was taken restates it in the row's
	// convention, so the row's own arithmetic (and its long-context
	// threshold) applies as metergraph-core applies it.
	if u.inputIncludesCacheRead != nil && *u.inputIncludesCacheRead != rowIncludes {
		if *u.inputIncludesCacheRead {
			inputCount = math.Max(0, inputCount-cacheRead)
		} else {
			inputCount = inputCount + cacheRead
		}
	}

	deducted := 0.0
	if rowIncludes {
		if cacheRead > inputCount {
			reasons = append(reasons, "cache_read_exceeds_input")
			deducted = inputCount
		} else {
			deducted += cacheRead
		}
	}
	// Replays record no cache writes, so input_includes_cache_write deducts nothing.
	billableInput := inputCount - deducted

	inputMultiplier := One
	outputMultiplier := One
	longContext := record(rules["long_context"])
	threshold := tokens(longContext["threshold"])
	if threshold != nil && inputCount > *threshold {
		inputMultiplier = nonZero(Dec(longContext["input_multiplier"]))
		outputMultiplier = nonZero(Dec(longContext["output_multiplier"]))
	}
	if !u.fullRate {
		offPeak := offPeakMultiplier(rules, at)
		inputMultiplier = Mul(inputMultiplier, offPeak)
		outputMultiplier = Mul(outputMultiplier, offPeak)
	}

	total := Zero
	if p.input == nil {
		if billableInput != 0 {
			reasons = append(reasons, "input_rate_unavailable")
		}
	} else {
		total = Add(total, Shift(Mul(Mul(Int(billableInput), *p.input), inputMultiplier), millionPlaces))
	}
	if p.output == nil {
		if outputCount != 0 {
			reasons = append(reasons, "output_rate_unavailable")
		}
	} else {
		total = Add(total, Shift(Mul(Mul(Int(outputCount), *p.output), outputMultiplier), millionPlaces))
	}
	if cacheRead != 0 {
		if p.cacheRead == nil {
			reasons = append(reasons, "cache_read_rate_unavailable")
		} else {
			total = Add(total, Shift(Mul(Mul(Int(cacheRead), *p.cacheRead), inputMultiplier), millionPlaces))
		}
	}
	if truthy(rules["uncaptured_fees"]) {
		reasons = append(reasons, "uncaptured_fees")
	}

	distinct := unique(reasons)
	status := "priced"
	if len(distinct) > 0 {
		status = "partial"
	}
	canonical := a.canonicalID
	priceID := p.id
	cost := UnitsToNumber(UnitsHalfUp(total, costPlaces), costPlaces)
	return Quote{CanonicalModel: &canonical, PriceID: &priceID, CostUSD: &cost, Status: status, Reasons: distinct}
}

func tokensOf(value *float64) *float64 {
	if value == nil {
		return nil
	}
	return tokens(*value)
}

// nonZero is Python's `_decimal(x) or Decimal("1")`: a zero or missing multiplier is one.
func nonZero(value *Decimal) Decimal {
	if value == nil || value.N.Sign() == 0 {
		return One
	}
	return *value
}

// ---------------------------------------------------------------------------
// The public surface

//go:embed prices.json
var pricesJSON []byte

// BundledCatalog is prices.json as shipped.
var BundledCatalog = func() CatalogDocument {
	var document CatalogDocument
	if err := json.Unmarshal(pricesJSON, &document); err != nil {
		panic("pricing: the bundled prices.json does not parse: " + err.Error())
	}
	return document
}()

// CatalogVersion is the bundled catalog's version for the report footer: commit and date.
func CatalogVersion(document *CatalogDocument) string {
	if document == nil {
		document = &BundledCatalog
	}
	commit := document.Source.Commit
	if len(commit) > 12 {
		commit = commit[:12]
	}
	return fmt.Sprintf("metergraph-core prices.yaml %s (%s)", document.Version, commit)
}

// CatalogPricing is types.Pricing over a catalog, plus the quote with its
// reasons and the resolution of production's model.
//
// A query whose At is not an ISO date or time panics.
type CatalogPricing struct {
	// Source repository, file and commit of the catalog.
	Source   CatalogSource
	version  string
	snapshot *snapshot
	now      func() time.Time
	// The document and region the snapshot was built from, so a lookup can
	// add models to it.
	document *CatalogDocument
	region   string
	// Models added from the Pricing API.
	looked int
}

var _ types.Pricing = (*CatalogPricing)(nil)

func LoadPricing(options LoadPricingOptions) (*CatalogPricing, error) {
	document := options.Document
	if document == nil {
		document = &BundledCatalog
	}
	region := options.Region
	if region == "" {
		region = globalRegion
	}
	s, err := newSnapshot(document, region)
	if err != nil {
		return nil, err
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &CatalogPricing{Source: document.Source, version: CatalogVersion(document), snapshot: s, now: now, document: document, region: region}, nil
}

func (c *CatalogPricing) instant(at string) int64 {
	if at == "" {
		return c.now().UnixMilli()
	}
	return mustInstant(at)
}

// lookup: a router provider resolves to the creator in "creator/model",
// spelled as written; then the creator's folded spelling, which
// metergraph-core indexes some creators under.
func (c *CatalogPricing) lookup(provider, model string, at int64, u usage) Quote {
	providerKey := NormalizeProvider(provider)
	modelKey := strings.ToLower(jsTrim(model))
	var attempts []string
	if routerProviders[providerKey] {
		if slash := strings.Index(modelKey, "/"); slash > 0 {
			providerKey = modelKey[:slash]
		}
		attempts = append(attempts, providerKey, NormalizeProvider(providerKey))
	} else {
		attempts = append(attempts, providerKey)
	}
	quote := unpriced(nil, "unknown_model")
	for _, key := range unique(attempts) {
		quote = c.snapshot.cost(key, modelKey, at, u)
		if len(quote.Reasons) == 0 || quote.Reasons[0] != "unknown_model" {
			break
		}
	}
	return quote
}

// quoteModel prices a replay's model: "creator/model" through the gateway
// rule, or a bare id under the catalog's provider-less aliases.
func (c *CatalogPricing) quoteModel(model string, at int64, u usage) Quote {
	provider := "unknown"
	if strings.Contains(model, "/") {
		provider = "gateway"
	}
	return c.lookup(provider, model, at, u)
}

var probe = usage{inputTokens: ptr(1.0), outputTokens: ptr(1.0)}

func ptr[T any](v T) *T { return &v }

func (c *CatalogPricing) priceable(provider, model string, at int64) *string {
	quote := c.lookup(provider, model, at, probe)
	if quote.Status != "unpriced" && quote.CostUSD != nil {
		return quote.CanonicalModel
	}
	return nil
}

// Quote is the cost with the catalog's reasons, for reports and conformance.
func (c *CatalogPricing) Quote(query types.PriceQuery) Quote {
	cached := 0.0
	if query.CachedInputTokens != nil {
		cached = *query.CachedInputTokens
	}
	return c.quoteModel(query.Model, c.instant(query.At), usage{
		inputTokens:            ptr(query.InputTokens),
		outputTokens:           ptr(query.OutputTokens),
		cachedInputTokens:      cached,
		inputIncludesCacheRead: query.InputIncludesCacheRead,
		fullRate:               query.FullRate,
	})
}

// CatalogVersion names the catalog, and says when the Pricing API added models.
func (c *CatalogPricing) CatalogVersion() string {
	if c.looked == 0 {
		return c.version
	}
	return fmt.Sprintf("%s + Metergraph Pricing API (%d %s)", c.version, c.looked, map[bool]string{true: "model", false: "models"}[c.looked == 1])
}

func (c *CatalogPricing) Price(query types.PriceQuery) *float64 { return c.Quote(query).CostUSD }

func (c *CatalogPricing) Knows(model string) bool {
	return c.quoteModel(model, c.instant(""), probe).Status != "unpriced"
}

// ResolveProduction is the canonical "creator/model" id a production call's
// model resolves to, trying the captured provider with the bare model, then
// the provider and model as captured, or nil when the catalog cannot price
// it. The noise floor replays and prices this id. An empty at means now.
func (c *CatalogPricing) ResolveProduction(provider, model *string, at string) *string {
	when := c.instant(at)
	raw := ""
	if model != nil {
		raw = jsTrim(*model)
	}
	if raw == "" {
		return nil
	}
	// Production's model for the floor resolves to the first that prices:
	// the captured provider (a router folded to the creator) with the bare
	// model, then the provider and model as captured.
	providerKey := ""
	if provider != nil {
		providerKey = strings.ToLower(jsTrim(*provider))
	}
	slash := strings.Index(raw, "/")
	bare := raw
	if slash >= 0 && raw[slash+1:] != "" {
		bare = raw[slash+1:]
	}
	captured := normalizeCaptured(providerKey)
	if (routerProviders[captured] || captured == "") && slash > 0 {
		captured = strings.ToLower(raw[:slash])
	}
	attempts := [][2]string{{captured, bare}}
	if providerKey != "" {
		attempts = append(attempts, [2]string{providerKey, raw})
	}
	for _, attempt := range attempts {
		candidateProvider, candidateModel := attempt[0], attempt[1]
		if candidateProvider == "" || candidateModel == "" {
			continue
		}
		var candidates [][2]string
		if strings.Contains(candidateModel, "/") {
			candidates = append(candidates, [2]string{"gateway", candidateModel})
		}
		candidates = append(candidates, [2]string{candidateProvider, candidateModel})
		if !strings.Contains(candidateModel, "/") {
			candidates = append(candidates, [2]string{"gateway", candidateProvider + "/" + candidateModel})
		}
		for _, candidate := range candidates {
			if canonical := c.priceable(candidate[0], candidate[1], when); canonical != nil {
				return canonical
			}
		}
	}
	// A capture without a provider: the catalog's provider-less aliases.
	if providerKey == "" && slash < 0 {
		return c.priceable("unknown", raw, when)
	}
	return nil
}

func normalizeCaptured(provider string) string {
	folded := map[string]string{"amazon-bedrock": "bedrock", "aws": "bedrock", "aws-bedrock": "bedrock"}
	if f, ok := folded[provider]; ok {
		return f
	}
	return provider
}

// freePricing is pricing for a local endpoint (Ollama and similar): every
// model is known and costs nothing.
type freePricing struct{}

func (freePricing) CatalogVersion() string          { return "local (free)" }
func (freePricing) Price(types.PriceQuery) *float64 { return ptr(0.0) }
func (freePricing) Knows(string) bool               { return true }

// FreePricing is pricing for a local endpoint (Ollama and similar): every
// model is known and costs nothing.
func FreePricing() types.Pricing { return freePricing{} }
