// Build the bundled demo: synthetic support-ticket traces (demo/traces.jsonl)
// and a recorded run over them (demo/run.json), made with a deterministic
// fake provider so `ezswap demo` needs no keys and no network.
//
//	go run ./cmd/build-demo
//
// Every ticket, name and address is invented; addresses use example.com.
// The fake models differ the way real ones tend to: the cheaper model
// relabels a few tickets, the reasoning model writes more tokens and cuts one
// reply off at the token limit, and production's model replayed twice
// disagrees with itself on a couple of tickets (the noise floor).
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/metergraph/ezswap/internal/adapters"
	"github.com/metergraph/ezswap/internal/cases/jsval"
	"github.com/metergraph/ezswap/internal/jsnum"
	"github.com/metergraph/ezswap/internal/pricing"
	"github.com/metergraph/ezswap/internal/run"
	"github.com/metergraph/ezswap/internal/types"
)

var now = time.Date(2026, 10, 28, 16, 0, 0, 0, time.UTC)

var system = strings.Join([]string{
	"You triage support tickets for Example Cloud, a hosting product at example.com.",
	"Read the ticket and reply with one JSON object and nothing else:",
	`{"category": "billing" | "bug" | "account" | "feature_request" | "outage",`,
	` "priority": "low" | "medium" | "high" | "urgent",`,
	` "product": the product area named or implied (dashboard, api, billing, dns, storage, auth, email),`,
	` "customer_email": the sender's address or null,`,
	` "summary": one sentence of at most 20 words}`,
}, "\n")

type ticket struct {
	subject  string
	from     *string
	body     string
	category string
	priority string
	product  string
	summary  string
}

func from(address string) *string { return &address }

var tickets = []ticket{
	{"Charged twice for October", from("dana.reyes@example.com"), "Hi, my card statement shows two charges of $49 for the October plan. Can you refund the duplicate?", "billing", "medium", "billing", "Customer was charged twice for the October plan and wants the duplicate refunded."},
	{"API returns 500 on /deployments", from("ops@example.com"), "Since this morning every POST to /v2/deployments returns HTTP 500. Our CI pipeline is blocked.", "bug", "high", "api", "POST requests to the deployments endpoint fail with HTTP 500, blocking the customer's CI."},
	{"Can't log in after password reset", from("li.wei@example.com"), "I reset my password but the login page keeps saying the link expired. I have tried three times.", "account", "medium", "auth", "Password reset links show as expired, so the customer cannot log in."},
	{"Dark mode for the dashboard?", from("sam.okafor@example.com"), "Would love a dark theme in the dashboard. Staring at it late at night is rough.", "feature_request", "low", "dashboard", "Customer asks for a dark theme in the dashboard."},
	{"Sites down in eu-west", from("noc@example.com"), "All of our sites in eu-west are timing out. Status page says operational but nothing loads.", "outage", "urgent", "api", "Every site in eu-west is timing out although the status page shows operational."},
	{"Invoice needs our VAT number", from("accounts@example.com"), "Please reissue invoice INV-2207 with our VAT number EX123456 on it. Our auditors need it.", "billing", "low", "billing", "Customer needs invoice INV-2207 reissued with their VAT number."},
	{"DNS changes not propagating", from("maria.k@example.com"), "I updated the A record for shop.example.com six hours ago and it still resolves to the old IP.", "bug", "medium", "dns", "An A record change made six hours ago has not taken effect."},
	{"Add a teammate without a seat?", from("jo@example.com"), "Is there a way to give our accountant read-only access to invoices without paying for another seat?", "account", "low", "billing", "Customer wants read-only invoice access for an accountant without buying a seat."},
	{"Uploads fail over 2 GB", from("media-team@example.com"), "Uploading files larger than 2 GB to storage fails at 99 percent with a network error. Smaller files are fine.", "bug", "medium", "storage", "Storage uploads over 2 GB fail near completion with a network error."},
	{"Webhooks for failed payments", from("dev@example.com"), "We would like a webhook when a customer's payment fails so we can pause their account automatically.", "feature_request", "low", "api", "Customer requests a webhook for failed payments."},
	{"Account locked after 2FA phone change", from("priya.n@example.com"), "I got a new phone and lost my authenticator. Now I am locked out and our launch is tomorrow.", "account", "high", "auth", "Customer lost their authenticator with a phone change and is locked out before a launch."},
	{"Outbound email delayed", from("support@example.com"), "Transactional emails from our app are arriving 30 to 40 minutes late since yesterday afternoon.", "bug", "high", "email", "Transactional emails have been delayed by 30 to 40 minutes since yesterday."},
	{"Downgrade to the starter plan", from("hello@example.com"), "We are a smaller team now. How do I move from Pro to Starter at the end of this billing cycle?", "billing", "low", "billing", "Customer wants to downgrade from Pro to Starter at the end of the cycle."},
	{"Dashboard graphs blank in Safari", from("tom.b@example.com"), "The usage graphs on the dashboard render empty in Safari 19. Chrome shows them fine.", "bug", "low", "dashboard", "Dashboard usage graphs render blank in Safari but work in Chrome."},
	{"Whole API unreachable", from("sre@example.com"), "api.example.com is refusing connections from every region we test. This is a full outage for us.", "outage", "urgent", "api", "The API refuses connections from every tested region, a full outage for the customer."},
	{"Unexpected storage overage charge", from("finance@example.com"), "We were billed $310 in storage overage but our usage graph never went above the plan limit.", "billing", "medium", "storage", "Customer disputes a $310 storage overage that their usage graph does not show."},
	{"SSO with our identity provider", from("it-admin@example.com"), "Do you support SAML single sign-on? We need it before we can roll out to the whole company.", "feature_request", "medium", "auth", "Customer asks whether SAML single sign-on is supported before a company rollout."},
	{"Rate limit hit at low volume", from("backend@example.com"), "We get 429 responses at around 20 requests a minute, well under the documented limit of 600.", "bug", "high", "api", "Customer gets rate limited at 20 requests a minute, far below the documented limit."},
	{"Change the account owner", from("founder@example.com"), "Our previous CTO owns the account and has left. Please transfer ownership to me.", "account", "medium", "auth", "Customer asks to transfer account ownership from a departed CTO."},
	{"Export invoices as CSV", nil, "It would save us hours each month if we could export all invoices as one CSV file.", "feature_request", "low", "billing", "Customer asks for a CSV export of all invoices."},
}

// --- deterministic noise ----------------------------------------------------

// hash is 32-bit FNV-1a over the UTF-16 code units, scaled to [0, 1).
func hash(text string) float64 {
	h := uint32(2166136261)
	for _, unit := range utf16.Encode([]rune(text)) {
		h = (h ^ uint32(unit)) * 16777619
	}
	return float64(h) / 4294967296
}

func between(seed string, low, high float64) float64 {
	return jsnum.Round(low + hash(seed)*(high-low))
}

func ticketText(t ticket) string {
	address := "(no address)"
	if t.from != nil {
		address = *t.from
	}
	return fmt.Sprintf("Subject: %s\nFrom: %s\n\n%s", t.subject, address, t.body)
}

// answer is production's reply to a ticket, with changes applied.
func answer(t ticket, change func(*ticket)) string {
	v := t
	if change != nil {
		change(&v)
	}
	out := jsval.NewObject()
	out.Set("category", v.category)
	out.Set("priority", v.priority)
	out.Set("product", v.product)
	if v.from != nil {
		out.Set("customer_email", *v.from)
	} else {
		out.Set("customer_email", nil)
	}
	out.Set("summary", v.summary)
	return jsval.Stringify(out)
}

func inputTokens(t ticket) float64 {
	return math.Ceil(float64(jsval.UTF16Len(system)+jsval.UTF16Len(ticketText(t)))/4) + 12
}

func outputTokens(text string) float64 {
	return math.Ceil(float64(jsval.UTF16Len(text)) / 4)
}

// object is an ordered JSON object from alternating keys and values.
func object(pairs ...any) *jsval.Object {
	out := jsval.NewObject()
	for i := 0; i < len(pairs); i += 2 {
		out.Set(pairs[i].(string), pairs[i+1])
	}
	return out
}

// --- the traces -------------------------------------------------------------

func request(content string) *jsval.Object {
	return object(
		"model", "gpt-4.1-mini",
		"system", system,
		"messages", []any{object("role", "user", "parts", []any{object("type", "text", "content", content)})},
		"response_format", object("type", "json_object"),
		"params", object("temperature", 0.2, "max_tokens", 300.0),
	)
}

func writeTraces(path string) error {
	prices, err := pricing.LoadPricing(pricing.LoadPricingOptions{Now: func() time.Time { return now }})
	if err != nil {
		return err
	}
	start, _ := adapters.DateParse("2026-10-27T08:00:00Z")
	stamp := func(k int) string { return adapters.ISOString(start + float64(k)*37*60_000) }
	var lines []string
	yes := true
	for i, t := range tickets {
		n := fmt.Sprintf("%04d", i+1)
		text := answer(t, nil)
		input, output := inputTokens(t), outputTokens(text)
		cost := prices.Price(types.PriceQuery{
			Model:                  "openai/gpt-4.1-mini",
			InputTokens:            input,
			OutputTokens:           output,
			CachedInputTokens:      new(float64),
			InputIncludesCacheRead: &yes,
		})
		var costValue any
		if cost != nil {
			costValue = *cost
		}
		lines = append(lines, jsval.Stringify(object(
			"schema", "metergraph.replay-trace/v1",
			"id", "call-"+n,
			"trace_id", "ticket-"+n,
			"provider", "openai",
			"request", request(ticketText(t)),
			"response", object(
				"text", text,
				"finish_reason", "stop",
				"usage", object("input_tokens", input, "output_tokens", output, "cached_input_tokens", 0.0),
				"cost_usd", costValue,
				"latency_ms", between(fmt.Sprintf("prod-%d", i), 690, 1340),
			),
			"meta", object("source", "example-helpdesk", "ts", stamp(i), "truncated", false, "missing", []any{}),
		)))
	}
	// Two calls the demo cannot replay, so the report shows why.
	late := func(k int) string { return stamp(len(tickets) + k) }
	lines = append(lines, jsval.Stringify(object(
		"schema", "metergraph.replay-trace/v1",
		"id", "call-0021",
		"trace_id", "ticket-0021",
		"provider", "openai",
		"request", request("Subject: Refund for annual plan\nFrom: ari@example.com\n\nWe paid for the annual plan in"),
		"response", object(
			"text", answer(tickets[0], func(v *ticket) {
				v.from = from("ari@example.com")
				v.summary = "Customer asks about a refund for the annual plan."
			}),
			"finish_reason", "stop",
		),
		"meta", object("source", "example-helpdesk", "ts", late(0), "truncated", true, "missing", []any{}),
	)))
	lines = append(lines, jsval.Stringify(object(
		"schema", "metergraph.replay-trace/v1",
		"id", "call-0022",
		"trace_id", "ticket-0022",
		"provider", "openai",
		"request", nil,
		"response", object("text", answer(tickets[6], nil), "finish_reason", "stop"),
		"meta", object("source", "example-helpdesk", "ts", late(1), "truncated", false, "missing", []any{"request"}),
	)))
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o666)
}

// --- the fake models --------------------------------------------------------

func ticketOf(call types.ReplayCall) int {
	last := ""
	if len(call.Messages) > 0 {
		last = call.Messages[len(call.Messages)-1].Content
	}
	for i, t := range tickets {
		if ticketText(t) == last {
			return i
		}
	}
	panic("unknown demo ticket")
}

type reply struct {
	text      string
	finish    string
	reasoning float64
	latency   float64
}

type behaviour func(i int, t ticket, call types.ReplayCall) reply

var (
	asksPattern  = regexp.MustCompile(`^Customer (asks|wants|requests) `)
	clausePatten = regexp.MustCompile(`, (blocking|so|a full) .*\.$`)
)

// A cheaper, faster model: terser summaries, and a few tickets labelled differently.
func flashLite(i int, t ticket, _ types.ReplayCall) reply {
	text := answer(t, func(v *ticket) {
		v.summary = clausePatten.ReplaceAllString(asksPattern.ReplaceAllString(t.summary, "Asks "), ".")
		switch i {
		case 3:
			v.category = "feature_request"
		case 6:
			v.priority = "high"
		case 11:
			v.category = "outage"
		case 15:
			v.category = "bug"
		case 17:
			v.priority = "urgent"
		case 10:
			v.priority = "urgent"
		case 18:
			v.category = "account"
		}
	})
	return reply{text: text, latency: between(fmt.Sprintf("lite-%d", i), 340, 560)}
}

// A small reasoning model: close to production, slower, more output tokens,
// and one reply cut off at the token limit.
func nano(i int, t ticket, call types.ReplayCall) reply {
	change := func(v *ticket) {
		if i == 8 {
			v.priority = "high"
		}
		if i == 16 {
			v.priority = "low"
		}
	}
	reasoning := between(fmt.Sprintf("nano-r-%d", i), 120, 230)
	if i == 13 {
		full := answer(t, change)
		cut := jsval.SliceUTF16(full, int(math.Floor(float64(jsval.UTF16Len(full))*0.55)))
		return reply{text: cut, finish: "length", reasoning: call.MaxTokens - outputTokens(cut), latency: between(fmt.Sprintf("nano-%d", i), 2600, 3100)}
	}
	return reply{text: answer(t, change), reasoning: reasoning, latency: between(fmt.Sprintf("nano-%d", i), 1500, 2300)}
}

// Production's model again: mostly the same answer, with the small
// self-disagreement that sets the noise floor.
func floor(replay int) behaviour {
	return func(i int, t ticket, _ types.ReplayCall) reply {
		text := answer(t, func(v *ticket) {
			if replay == 1 && i == 6 {
				v.priority = "high"
			}
			if replay == 2 && i == 12 {
				v.priority = "medium"
			}
			if replay == 2 && i == 17 {
				v.priority = "urgent"
			}
			if replay == 2 && i == 4 {
				v.summary = "Sites in eu-west time out while the status page reports everything operational."
			}
		})
		return reply{text: text, latency: between(fmt.Sprintf("floor%d-%d", replay, i), 650, 1300)}
	}
}

type fakeProvider struct {
	name string
	pick func(call types.ReplayCall) behaviour
}

func (p fakeProvider) Name() string { return p.name }

func (p fakeProvider) Complete(_ context.Context, call types.ReplayCall) types.ReplyOutcome {
	i := ticketOf(call)
	t := tickets[i]
	out := p.pick(call)(i, t, call)
	visible := outputTokens(out.text)
	input := jsnum.Round(inputTokens(t) * (0.96 + hash(fmt.Sprintf("%s-%d", call.Model, i))*0.08))
	finish := out.finish
	if finish == "" {
		finish = "stop"
	}
	var reasoningTokens *float64
	if out.reasoning != 0 {
		reasoningTokens = &out.reasoning
	}
	output := visible + out.reasoning
	cached := 0.0
	latency := out.latency
	return types.ReplyOutcome{
		Status:       "ok",
		Output:       &out.text,
		FinishReason: &finish,
		Usage:        &types.Usage{InputTokens: &input, OutputTokens: &output, CachedInputTokens: &cached, ReasoningTokens: reasoningTokens},
		LatencyMs:    &latency,
		Flags:        []types.ReplayFlag{},
	}
}

func writeRun(dir, traces string) (*types.RunFile, error) {
	prices, err := pricing.LoadPricing(pricing.LoadPricingOptions{Now: func() time.Time { return now }})
	if err != nil {
		return nil, err
	}
	noiseFloor := true
	budget := 1.0
	plan, err := run.PlanRun(run.PlanOptions{
		File:   traces,
		Sample: 20,
		Treatments: []run.TreatmentInput{
			{Model: "google/gemini-2.5-flash-lite", Endpoint: "openrouter"},
			{Model: "openai/gpt-5.4-nano", Endpoint: "openrouter"},
		},
		NoiseFloor:    &noiseFloor,
		FloorEndpoint: "openrouter",
		TrackedFields: []string{"$.category", "$.priority"},
		BudgetUSD:     &budget,
		Pricing:       prices,
		At:            adapters.ISOString(float64(now.UnixMilli())),
	})
	if err != nil {
		return nil, err
	}
	// The floor's two replays alternate on one endpoint; tell them apart by call order per ticket.
	var mu sync.Mutex
	floorSeen := map[int]int{}
	provider := fakeProvider{name: "openrouter", pick: func(call types.ReplayCall) behaviour {
		if call.Model == "google/gemini-2.5-flash-lite" {
			return flashLite
		}
		if call.Model == "openai/gpt-5.4-nano" {
			return nano
		}
		i := ticketOf(call)
		mu.Lock()
		defer mu.Unlock()
		floorSeen[i]++
		if floorSeen[i] == 1 {
			return floor(1)
		}
		return floor(2)
	}}
	// One call at a time, so the floor replays arrive in order.
	result, err := run.ExecuteRun(context.Background(), plan, run.ExecuteOptions{
		Providers:   map[string]types.Provider{"openrouter": provider},
		Pricing:     prices,
		Now:         func() time.Time { return now },
		Concurrency: 1,
		ToolVersion: "0.0.0",
	})
	if err != nil {
		return nil, err
	}
	// An assumed volume for the made-up helpdesk, so the demo shows a monthly projection.
	monthly := 250_000.0
	result.Settings.MonthlyCalls = &monthly
	if _, err := run.WriteRun(dir, result); err != nil {
		return nil, err
	}
	return result, nil
}

// build writes dir/traces.jsonl and dir/run.json.
func build(dir string) (*types.RunFile, error) {
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return nil, err
	}
	traces := filepath.Join(dir, "traces.jsonl")
	if err := writeTraces(traces); err != nil {
		return nil, err
	}
	return writeRun(dir, traces)
}

func main() {
	dir := flag.String("dir", "demo", "directory to write the demo's trace files and runs into")
	flag.Parse()
	result, err := build(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %d replays over %d cases; spent $%s.\n", len(result.Replays), len(result.Cases), jsnum.ToFixed(result.SpentUSD, 4))
	mapped, err := buildMap(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote the map run: %d replays over %d cases; spent $%s.\n", len(mapped.Replays), len(mapped.Cases), jsnum.ToFixed(mapped.SpentUSD, 4))
}
