// A made-up support desk that calls a model from four places, for trying
// `metergraph-replay record` and `map` end to end.
//
//	metergraph-replay record --upstream http://localhost:11434   # or the default, api.openai.com
//	go run ./examples/support-desk --base-url http://127.0.0.1:8787/v1 --model qwen2.5:3b
//
// Each call names its place in the app with the X-Replay-Part header, which
// the recorder writes to the trace file and never forwards. Every ticket,
// order and address is invented.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type part struct {
	name   string
	system string
	// Ask for a JSON object.
	json bool
	// Stream the reply, as a chat window would.
	stream bool
	inputs []string
}

var parts = []part{
	{
		name:   "classify-ticket",
		system: `Classify the support ticket. Reply with one JSON object and nothing else: {"category": "account" | "feature_request" | "billing" | "bug" | "outage", "priority": "low" | "medium" | "high" | "urgent"}`,
		json:   true,
		inputs: []string{
			"I was charged twice for the March invoice and need one payment refunded.",
			"After resetting my password the login link says it has expired. I've tried three times.",
			"Would love a dark theme for the dashboard, my eyes hurt at night.",
			"Every site in eu-west has been timing out since 09:00. Our checkout is down.",
			"The October invoice is missing our VAT number, finance can't book it.",
			"Uploads over 2 GB stop at 99 percent and never finish.",
			"Can you add SSO with Okta? Our security team requires it before renewal next month.",
			"How do I change the email address on my account? I left my old job.",
			"Your API returns 500 for every request with a date filter. Started an hour ago, all customers affected.",
			"Please cancel our plan at the end of this billing cycle.",
			"The export button produces an empty CSV when the report has more than 10,000 rows.",
			"Two people on my team can't see the shared folders I gave them access to.",
			"We were billed for 40 seats but only have 32 users.",
			"It would be nice if charts could be copied as images.",
			"Dashboard is completely blank for everyone in our org, nothing loads. We have a board meeting in 30 minutes.",
			"My 2FA phone was stolen and I'm locked out of the admin account.",
			"The mobile app crashes when I open a notification on Android 15.",
			"Is there a discount for nonprofits?",
			"Webhook deliveries are delayed by about 20 minutes since yesterday.",
			"I want to delete my account and all my data.",
			"Search results ignore the date range I pick.",
			"Our card was declined but the bank says there is no problem.",
			"Add keyboard shortcuts for switching projects, please.",
			"Payments from all our customers are failing at your checkout since 14:10. We are losing orders.",
		},
	},
	{
		name:   "extract-order",
		system: `Extract the order the customer is placing now from their message. Reply with one JSON object and nothing else: {"order_id": string, "sku": string, "quantity": number, "city": string}`,
		json:   true,
		inputs: []string{
			"Order EX-10401: please send 4 of item A-118 to our Leeds office. Thanks, purchasing@example.com",
			"Hi, we got 3 units of B-204 last month. For order EX-10408 we now need 5 more, delivered to Porto.",
			"PO EX-10415 - C-77 x 12, ship to the Tampere warehouse.",
			"Could you deliver two dozen A-12 to Graz? Reference EX-10422.",
			"Order EX-10429. Item D-310. Quantity: 1. Our Cork site, not the Dublin one.",
			"Re EX-10436: change the quantity of B-19 from 6 to 8 please, still going to Gdansk.",
			"Send ten of A-118 to Lyon under EX-10443. The B-204 we ordered last week can wait.",
			"EX-10450: 7 x C-77 for Leeds. (Ignore EX-10391, that one was delivered.)",
			"We need half a dozen D-310 in Porto, order number EX-10457.",
			"Order EX-10464 for Tampere: A-12, three boxes of 10 each.",
			"Please ship 15 units of B-19 to our Graz branch. Order EX-10471.",
			"EX-10478 - one B-204 to Cork, and please confirm the price.",
			"For EX-10485 we want 9 of A-118, to Gdansk. Last time we got 4.",
			"Order EX-10492: C-77, quantity 20, delivery to the Lyon depot.",
			"Hello! EX-10499 is for 2 x D-310, shipping to Leeds. Our Porto team ordered separately.",
			"Item A-12 x 30 to Porto please, ref EX-10506. Thanks!",
			"EX-10513: replace the damaged B-19 units with 4 new ones, ship to Tampere.",
			"Can we get 11 B-204 delivered in Graz? Order EX-10520.",
			"Order EX-10527: please send 6 C-77. Deliver to Cork. We cancelled the A-118 part.",
			"EX-10534 for Gdansk: D-310, quantity 5, plus nothing else this time.",
			"Need 8 units of A-12 shipped to Lyon, order ref EX-10541, urgent.",
			"Order EX-10548: B-19, 14 pieces, deliver to the Leeds office please.",
			"EX-10555: we'd like three B-204 and that's it, to Porto.",
			"Order EX-10562: 25 x C-77 to Tampere. Our usual order is 20.",
		},
	},
	{
		name: "refund-decision",
		system: `You decide refund requests for an online shop. Apply the policy exactly.
1. Physical item returned within 30 days of delivery: full refund if unopened; if opened, refund the price minus a 15% restocking fee.
2. Physical item returned 31 to 90 days after delivery: store credit for the full price, no refund.
3. Item damaged on arrival and reported within 90 days of delivery: full refund, no fee.
4. Digital download: full refund only if not downloaded and within 14 days of purchase; otherwise deny.
5. Subscription: refund the unused whole months of the current term.
Anything else: deny.
Reply with one JSON object and nothing else: {"decision": "refund" | "store_credit" | "deny", "amount": number (USD, 0 if none), "rule": number (1 to 5, 0 if none applies)}`,
		json: true,
		inputs: []string{
			"Unopened blender, $80.00, delivered 12 days ago. Customer wants their money back.",
			"Headphones, $120.00, opened and used, delivered 20 days ago. Customer changed their mind.",
			"Desk lamp, $45.00, unopened, delivered 47 days ago.",
			"Coffee grinder, $64.00, arrived with a cracked lid, reported 50 days after delivery.",
			"E-book, $14.99, bought 3 days ago, not downloaded yet.",
			"Video course, $199.00, bought 5 days ago, already downloaded.",
			"Annual subscription, $120.00 paid 4 months ago, customer wants to cancel now.",
			"Backpack, $95.00, opened, delivered exactly 30 days ago.",
			"Monitor, $310.00, unopened, delivered 91 days ago.",
			"Keyboard, $70.00, opened, delivered 31 days ago.",
			"Music album download, $9.99, bought 20 days ago, never downloaded.",
			"Monthly subscription, $15.00 paid 10 days ago for this month.",
			"Kettle, $38.00, arrived dented, reported 2 days after delivery, opened to check it.",
			"Phone case, $25.00, unopened, delivered 29 days ago.",
			"Running shoes, $140.00, worn twice, delivered 14 days ago.",
			"Two-year subscription, $240.00 paid 7 months ago, cancelling today.",
			"Software licence key, $59.00, bought 2 days ago, key not yet revealed or downloaded.",
			"Office chair, $260.00, opened and assembled, delivered 25 days ago.",
			"Board game, $42.00, missing pieces on arrival, reported 95 days after delivery.",
			"Water bottle, $18.00, opened, delivered 60 days ago.",
			"Annual subscription, $96.00 paid 11 months and 20 days ago.",
			"Gift card, $50.00, bought 1 day ago.",
			"Tent, $180.00, unopened, delivered 8 days ago, customer found it cheaper elsewhere.",
			"Printer, $150.00, stopped working after 3 weeks of use, delivered 22 days ago.",
		},
	},
	{
		name:   "draft-reply",
		system: "Draft a short, friendly reply to the customer from the support team. Include every fact from the note. Plain text, at most three sentences.",
		stream: true,
		inputs: []string{
			"Order EX-10401 leaves the warehouse tomorrow; tracking is emailed when it ships.",
			"Refund of $48.20 for order EX-10408 was sent today; it takes 5 to 7 business days to appear.",
			"The eu-west outage is fixed as of 10:42 UTC; we will publish a report by Friday.",
			"We can't refund the downloaded video course, but we can offer a 20% discount on the next one.",
			"Order EX-10429 is delayed by a week because the item is out of stock; the customer can cancel for free.",
			"The VAT number is now on the October invoice; a corrected PDF is attached.",
			"Dark theme is on our roadmap for the second quarter; we have added their vote.",
			"Their account email was changed; they must confirm the new address within 24 hours.",
			"The duplicate March charge of $29.00 was refunded; no action is needed from them.",
			"SSO with Okta is available on the Business plan, which costs $12 more per seat.",
			"The uploads bug is fixed for files up to 5 GB; larger files still need the desktop app.",
			"Their plan will cancel at the end of the cycle on 30 November; data is kept for 60 days.",
			"We replaced the dented kettle; the new one ships today and they can keep the old one.",
			"The seat count was corrected from 40 to 32; next invoice will be $96 lower.",
			"Webhook delays were caused by a queue backlog; deliveries are back to normal and missed events were resent.",
			"Their 2FA was reset after identity checks; they should set up a new device at next login.",
			"The Android crash is fixed in version 8.4.1, out today in the Play Store.",
			"We offer nonprofits 30% off annual plans; they need to send proof of status.",
			"Their data deletion request is scheduled; it completes in 30 days and cannot be undone.",
			"Order EX-10478 price is $54.00 including shipping; it ships Monday.",
			"Store credit of $45.00 was added to their account for the returned lamp; it never expires.",
			"The search date filter bug is confirmed; a fix is planned for next week's release.",
			"Their card was declined by our payment processor's fraud check; they can retry or use PayPal.",
			"The CSV export limit is now 50,000 rows; above that, use the API export.",
		},
	},
}

func call(client *http.Client, baseURL, model, key string, p part, i int) (string, error) {
	body := map[string]any{
		"model": model,
		"messages": []any{
			map[string]string{"role": "system", "content": p.system},
			map[string]string{"role": "user", "content": p.inputs[i]},
		},
		"temperature": 0.2,
		"max_tokens":  400,
	}
	if p.json {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	if p.stream {
		body["stream"] = true
	}
	data, _ := json.Marshal(body)
	request, err := http.NewRequest("POST", strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Replay-Part", p.name)
	request.Header.Set("X-Replay-Trace", fmt.Sprintf("%s-%d", p.name, i))
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		text, _ := io.ReadAll(io.LimitReader(response.Body, 300))
		return "", fmt.Errorf("status %d: %s", response.StatusCode, strings.TrimSpace(string(text)))
	}
	if !p.stream {
		var decoded struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil || len(decoded.Choices) == 0 {
			return "", fmt.Errorf("unreadable reply")
		}
		return decoded.Choices[0].Message.Content, nil
	}
	var text strings.Builder
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok || line == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(line), &chunk) == nil && len(chunk.Choices) > 0 {
			text.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	return text.String(), scanner.Err()
}

func main() {
	baseURL := flag.String("base-url", "http://127.0.0.1:8787/v1", "OpenAI-compatible base URL: the recorder's address")
	model := flag.String("model", "gpt-4.1-mini", "the model the app uses in production")
	count := flag.Int("calls", 24, "calls to make from each part (at most 24)")
	flag.Parse()
	key := os.Getenv("OPENAI_API_KEY")
	client := &http.Client{Timeout: 5 * time.Minute}
	failed := 0
	for i := 0; i < *count && i < 24; i++ {
		for _, p := range parts {
			reply, err := call(client, *baseURL, *model, key, p, i)
			if err != nil {
				failed++
				fmt.Printf("%-16s %2d  failed: %v\n", p.name, i+1, err)
				continue
			}
			reply = strings.Join(strings.Fields(reply), " ")
			if len(reply) > 70 {
				reply = reply[:70] + "..."
			}
			fmt.Printf("%-16s %2d  %s\n", p.name, i+1, reply)
		}
	}
	if failed > 0 {
		fmt.Printf("%d calls failed.\n", failed)
		os.Exit(1)
	}
}
