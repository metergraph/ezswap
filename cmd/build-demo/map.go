package main

// The bundled map demo: a made-up support desk with five places that call a
// model (demo/map-traces.jsonl), and a recorded map run over them
// (demo/map-run.json), made with a deterministic fake provider.
//
// Three parts carry a name in the trace file; two do not and are grouped by
// fingerprint, then named the way a developer would in the names file. The
// fake treatment changes many order extractions, a few routings (no more than
// production's own rerun does), and almost no classifications.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/metergraph/replay/internal/adapters"
	"github.com/metergraph/replay/internal/cases/jsval"
	"github.com/metergraph/replay/internal/parts"
	"github.com/metergraph/replay/internal/pricing"
	"github.com/metergraph/replay/internal/run"
	"github.com/metergraph/replay/internal/types"
)

const (
	mapProduction = "gpt-4.1-mini"
	mapTreatment  = "openai/gpt-5.4-nano"
)

// mapCall is one recorded call: what was asked and what production answered.
type mapCall struct {
	input, output string
}

// mapPart is one place in the made-up app.
type mapPart struct {
	// The name in the trace file, or "" for a part the developer did not name.
	name string
	// The name the builder gives a fingerprinted part, as a names file would.
	rename string
	system string
	json   bool
	calls  []mapCall
	// What each arm answers, given the call's index and production's answer.
	treatment, rerun1, rerun2 func(i int, c mapCall) string
}

func same(_ int, c mapCall) string { return c.output }

func jsonOf(pairs ...any) string { return jsval.Stringify(object(pairs...)) }

var (
	skus   = []string{"A-118", "B-204", "C-77", "A-12", "D-310", "B-19", "E-5", "C-240"}
	cities = []string{"Leeds", "Porto", "Tampere", "Graz", "Cork", "Gdansk", "Ghent", "Aarhus"}
	teams  = []string{"billing", "support", "sales", "engineering"}
)

func orderCalls() []mapCall {
	out := make([]mapCall, 20)
	for i := range out {
		id := fmt.Sprintf("EX-%d", 10400+i*7)
		sku := skus[(i*3)%len(skus)]
		quantity := float64(1 + (i*5)%6)
		out[i] = mapCall{
			input:  fmt.Sprintf("Order %s: please send %d of item %s to our %s office. Thanks, purchasing@example.com", id, int(quantity), sku, cities[i%len(cities)]),
			output: jsonOf("order_id", id, "sku", sku, "quantity", quantity),
		}
	}
	return out
}

func orderWith(c mapCall, i int, sku string, quantity float64) string {
	return jsonOf("order_id", fmt.Sprintf("EX-%d", 10400+i*7), "sku", sku, "quantity", quantity)
}

func routeCalls() []mapCall {
	asks := []string{
		"I was charged twice this month and need one payment refunded.",
		"The export button does nothing when I click it.",
		"Can someone walk us through pricing for 40 seats?",
		"Our webhook deliveries return a 502 from your side.",
		"Please update the billing address on our account.",
	}
	routed := []string{"billing", "support", "sales", "engineering", "billing"}
	out := make([]mapCall, 20)
	for i := range out {
		k := i % len(asks)
		out[i] = mapCall{
			input:  fmt.Sprintf("Message %d from customer-%d@example.com: %s", i+1, 200+i, asks[k]),
			output: jsonOf("team", routed[k], "urgent", k == 3),
		}
	}
	return out
}

func classifyCalls() []mapCall {
	asks := []string{
		"Login fails with an expired link after a password reset.",
		"Please add a dark theme to the dashboard.",
		"The invoice for October is missing our VAT number.",
		"Every site in eu-west is timing out.",
	}
	category := []string{"account", "feature_request", "billing", "outage"}
	priority := []string{"medium", "low", "low", "urgent"}
	out := make([]mapCall, 20)
	for i := range out {
		k := i % len(asks)
		out[i] = mapCall{
			input:  fmt.Sprintf("Ticket %d: %s", 3000+i, asks[k]),
			output: jsonOf("category", category[k], "priority", priority[k]),
		}
	}
	return out
}

func summaryCalls() []mapCall {
	out := make([]mapCall, 6)
	for i := range out {
		out[i] = mapCall{
			input: fmt.Sprintf("Thread %d: the customer reported slow uploads, support asked for a file size, the customer replied 3 GB, engineering confirmed a limit of 2 GB.", 70+i),
			output: jsonOf(
				"summary", fmt.Sprintf("Thread %d: uploads over 2 GB fail because of a size limit that engineering confirmed; the customer was sending 3 GB files and needs a workaround.", 70+i),
				"action_items", []any{"Tell the customer about the 2 GB limit", "Open a request to raise the limit"},
			),
		}
	}
	return out
}

func draftCalls() []mapCall {
	out := make([]mapCall, 20)
	for i := range out {
		out[i] = mapCall{
			input:  fmt.Sprintf("Draft a reply to customer-%d@example.com, who asked when order EX-%d will ship.", 500+i, 10400+i*7),
			output: fmt.Sprintf("Hello, thanks for writing in. Order EX-%d is packed and leaves our warehouse tomorrow. You will get a tracking link by email.", 10400+i*7),
		}
	}
	return out
}

func swap(list []string, value string) string {
	for i, item := range list {
		if item == value {
			return list[(i+1)%len(list)]
		}
	}
	return list[0]
}

// field reads one string field of a JSON answer the builder wrote.
func field(output, key string) string {
	parsed, err := jsval.Parse(output)
	if err != nil {
		return ""
	}
	text, _ := jsval.Get(parsed, key).(string)
	return text
}

func in(i int, set ...int) bool {
	for _, n := range set {
		if n == i {
			return true
		}
	}
	return false
}

var mapParts = []mapPart{
	{
		name:   "extract-order",
		system: "Extract the order from the customer's message. Reply with one JSON object and nothing else: {\"order_id\": string, \"sku\": string, \"quantity\": number}",
		json:   true,
		calls:  orderCalls(),
		treatment: func(i int, c mapCall) string {
			sku := skus[(i*3)%len(skus)]
			quantity := float64(1 + (i*5)%6)
			switch {
			case in(i, 2, 5, 9, 12, 16):
				return orderWith(c, i, sku, quantity+1)
			case in(i, 3, 7, 14):
				return orderWith(c, i, sku+"B", quantity)
			case i == 18:
				return fmt.Sprintf("The order is EX-%d, for %d units of %s.", 10400+i*7, int(quantity), sku)
			}
			return c.output
		},
		rerun1: func(i int, c mapCall) string {
			if i == 11 {
				return orderWith(c, i, strings.ToLower(skus[(i*3)%len(skus)]), float64(1+(i*5)%6))
			}
			return c.output
		},
		rerun2: same,
	},
	{
		name:   "route-to-team",
		system: "Route the customer's message to a team. Reply with one JSON object and nothing else: {\"team\": \"billing\" | \"support\" | \"sales\" | \"engineering\", \"urgent\": boolean}",
		json:   true,
		calls:  routeCalls(),
		treatment: func(i int, c mapCall) string {
			if in(i, 1, 6, 10, 15) {
				return jsonOf("team", swap(teams, field(c.output, "team")), "urgent", i%5 == 3)
			}
			return c.output
		},
		rerun1: func(i int, c mapCall) string {
			if in(i, 4, 8, 17) {
				return jsonOf("team", swap(teams, field(c.output, "team")), "urgent", i%5 == 3)
			}
			return c.output
		},
		rerun2: same,
	},
	{
		name:   "classify-ticket",
		system: "Classify the support ticket. Reply with one JSON object and nothing else: {\"category\": \"account\" | \"feature_request\" | \"billing\" | \"outage\", \"priority\": \"low\" | \"medium\" | \"high\" | \"urgent\"}",
		json:   true,
		calls:  classifyCalls(),
		treatment: func(i int, c mapCall) string {
			if in(i, 3, 13) {
				return jsonOf("category", field(c.output, "category"), "priority", "high")
			}
			return c.output
		},
		rerun1: func(i int, c mapCall) string {
			if in(i, 5, 9, 19) {
				return jsonOf("category", field(c.output, "category"), "priority", "medium")
			}
			return c.output
		},
		rerun2: func(i int, c mapCall) string {
			if i == 5 {
				return jsonOf("category", field(c.output, "category"), "priority", "medium")
			}
			return c.output
		},
	},
	{
		rename:    "summarise-thread",
		system:    "Summarise the support thread for a handover. Reply with one JSON object and nothing else: {\"summary\": string, \"action_items\": string[]}",
		json:      true,
		calls:     summaryCalls(),
		treatment: same, rerun1: same, rerun2: same,
	},
	{
		rename: "draft-reply",
		system: "Draft a short, friendly reply to the customer. Plain text, no greeting line, at most three sentences.",
		calls:  draftCalls(),
		treatment: func(i int, c mapCall) string {
			return strings.Replace(c.output, "Hello, thanks for writing in.", "Thanks for getting in touch.", 1)
		},
		rerun1: same,
		rerun2: func(i int, c mapCall) string {
			if i%4 == 0 {
				return strings.Replace(c.output, "leaves our warehouse tomorrow", "ships tomorrow", 1)
			}
			return c.output
		},
	},
}

func mapTokens(text string) float64 { return outputTokens(text) + 8 }

func writeMapTraces(path string) error {
	prices, err := pricing.LoadPricing(pricing.LoadPricingOptions{Now: func() time.Time { return now }})
	if err != nil {
		return err
	}
	start, _ := adapters.DateParse("2026-10-20T09:00:00Z")
	yes := true
	var lines []string
	n := 0
	for p, part := range mapParts {
		for i, c := range part.calls {
			n++
			input, output := mapTokens(part.system+c.input), mapTokens(c.output)
			cost := prices.Price(types.PriceQuery{
				Model: "openai/" + mapProduction, InputTokens: input, OutputTokens: output,
				CachedInputTokens: new(float64), InputIncludesCacheRead: &yes,
			})
			var costValue any
			if cost != nil {
				costValue = *cost
			}
			request := object(
				"model", mapProduction,
				"system", part.system,
				"messages", []any{object("role", "user", "parts", []any{object("type", "text", "content", c.input)})},
			)
			if part.json {
				request.Set("response_format", object("type", "json_object"))
			}
			request.Set("params", object("temperature", 0.2, "max_tokens", 300.0))
			record := object(
				"schema", "metergraph.replay-trace/v1",
				"id", fmt.Sprintf("call-%04d", n),
				"trace_id", fmt.Sprintf("req-%04d", n),
				"provider", "openai",
			)
			if part.name != "" {
				record.Set("part", part.name)
			}
			record.Set("request", request)
			record.Set("response", object(
				"text", c.output,
				"finish_reason", "stop",
				"usage", object("input_tokens", input, "output_tokens", output, "cached_input_tokens", 0.0),
				"cost_usd", costValue,
				"latency_ms", between(fmt.Sprintf("map-prod-%d-%d", p, i), 640, 1280),
			))
			// The calls of the five parts interleave over a week, as they would in an app.
			ts := adapters.ISOString(start + float64(i*5+p)*107*60_000)
			record.Set("meta", object("source", "example-support-desk", "ts", ts, "truncated", false, "missing", []any{}))
			lines = append(lines, jsval.Stringify(record))
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o666)
}

// mapLookup finds the part and call a replayed request belongs to.
func mapLookup(call types.ReplayCall) (int, int) {
	last := ""
	if len(call.Messages) > 0 {
		last = call.Messages[len(call.Messages)-1].Content
	}
	for p, part := range mapParts {
		for i, c := range part.calls {
			if c.input == last {
				return p, i
			}
		}
	}
	panic("unknown map demo call")
}

type mapProvider struct {
	mu   sync.Mutex
	seen map[string]int
}

func (*mapProvider) Name() string { return "openrouter" }

func (m *mapProvider) Complete(_ context.Context, call types.ReplayCall) types.ReplyOutcome {
	p, i := mapLookup(call)
	part := mapParts[p]
	c := part.calls[i]
	var text string
	var latency float64
	if call.Model == mapTreatment {
		text = part.treatment(i, c)
		latency = between(fmt.Sprintf("map-t-%d-%d", p, i), 380, 790)
	} else {
		// The floor's two replays share a model: tell them apart by call order.
		m.mu.Lock()
		key := fmt.Sprintf("%d-%d", p, i)
		m.seen[key]++
		first := m.seen[key] == 1
		m.mu.Unlock()
		if first {
			text = part.rerun1(i, c)
		} else {
			text = part.rerun2(i, c)
		}
		latency = between(fmt.Sprintf("map-f-%d-%d-%v", p, i, first), 640, 1280)
	}
	input, output, cached := mapTokens(part.system+c.input), mapTokens(text), 0.0
	finish := "stop"
	return types.ReplyOutcome{
		Status:       "ok",
		Output:       &text,
		FinishReason: &finish,
		Usage:        &types.Usage{InputTokens: &input, OutputTokens: &output, CachedInputTokens: &cached},
		LatencyMs:    &latency,
		Flags:        []types.ReplayFlag{},
	}
}

func writeMapRun(path, traces string) (*types.RunFile, error) {
	prices, err := pricing.LoadPricing(pricing.LoadPricingOptions{Now: func() time.Time { return now }})
	if err != nil {
		return nil, err
	}
	noiseFloor := true
	options := run.PlanOptions{
		File:          traces,
		Map:           true,
		PerPart:       20,
		Treatments:    []run.TreatmentInput{{Model: mapTreatment, Endpoint: "openrouter"}},
		NoiseFloor:    &noiseFloor,
		FloorEndpoint: "openrouter",
		Pricing:       prices,
		At:            adapters.ISOString(float64(now.UnixMilli())),
	}
	// Plan once to learn the fingerprint keys, then name those parts the way
	// a developer would by editing the names file.
	first, err := run.PlanRun(options)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, kase := range first.Cases {
		if !strings.HasPrefix(kase.PartKey, parts.FingerprintPrefix) {
			continue
		}
		p, _ := mapLookup(types.ReplayCall{Messages: kase.Messages})
		names[kase.PartKey] = mapParts[p].rename
	}
	options.Names = names
	plan, err := run.PlanRun(options)
	if err != nil {
		return nil, err
	}
	result, err := run.ExecuteRun(context.Background(), plan, run.ExecuteOptions{
		Providers:   map[string]types.Provider{"openrouter": &mapProvider{seen: map[string]int{}}},
		Pricing:     prices,
		Now:         func() time.Time { return now },
		Concurrency: 1,
		ToolVersion: "0.0.0",
	})
	if err != nil {
		return nil, err
	}
	text, err := run.EncodeRun(result)
	if err != nil {
		return nil, err
	}
	return result, os.WriteFile(path, text, 0o666)
}

// buildMap writes dir/map-traces.jsonl and dir/map-run.json.
func buildMap(dir string) (*types.RunFile, error) {
	traces := filepath.Join(dir, "map-traces.jsonl")
	if err := writeMapTraces(traces); err != nil {
		return nil, err
	}
	result, err := writeMapRun(filepath.Join(dir, "map-run.json"), traces)
	if err != nil {
		return nil, err
	}
	// The run records the file's base name; nothing else of the path is kept.
	return result, nil
}
