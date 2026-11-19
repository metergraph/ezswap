// A made-up support desk that calls a model from three places, for trying
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
	input  func(i int) string
}

var (
	skus   = []string{"A-118", "B-204", "C-77", "A-12", "D-310", "B-19"}
	cities = []string{"Leeds", "Porto", "Tampere", "Graz", "Cork", "Gdansk"}
	issues = []string{
		"I was charged twice this month and need one payment refunded.",
		"Login fails with an expired link after a password reset.",
		"Please add a dark theme to the dashboard.",
		"Every site in eu-west is timing out since 09:00.",
		"The October invoice is missing our VAT number.",
		"Uploads over 2 GB fail at 99 percent.",
	}
)

var parts = []part{
	{
		name:   "classify-ticket",
		system: `Classify the support ticket. Reply with one JSON object and nothing else: {"category": "account" | "feature_request" | "billing" | "bug" | "outage", "priority": "low" | "medium" | "high" | "urgent"}`,
		json:   true,
		input: func(i int) string {
			return fmt.Sprintf("Ticket %d from customer-%d@example.com: %s", 3000+i, 200+i, issues[i%len(issues)])
		},
	},
	{
		name:   "extract-order",
		system: `Extract the order from the customer's message. Reply with one JSON object and nothing else: {"order_id": string, "sku": string, "quantity": number}`,
		json:   true,
		input: func(i int) string {
			return fmt.Sprintf("Order EX-%d: please send %d of item %s to our %s office. Thanks, purchasing@example.com",
				10400+i*7, 1+(i*5)%6, skus[(i*3)%len(skus)], cities[i%len(cities)])
		},
	},
	{
		name:   "draft-reply",
		system: "Draft a short, friendly reply to the customer. Plain text, at most three sentences.",
		stream: true,
		input: func(i int) string {
			return fmt.Sprintf("customer-%d@example.com asked when order EX-%d will ship. It leaves the warehouse tomorrow.", 500+i, 10400+i*7)
		},
	},
}

func call(client *http.Client, baseURL, model, key string, p part, i int) (string, error) {
	body := map[string]any{
		"model": model,
		"messages": []any{
			map[string]string{"role": "system", "content": p.system},
			map[string]string{"role": "user", "content": p.input(i)},
		},
		"temperature": 0.2,
		"max_tokens":  200,
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
	count := flag.Int("calls", 12, "calls to make from each part")
	flag.Parse()
	key := os.Getenv("OPENAI_API_KEY")
	client := &http.Client{Timeout: 5 * time.Minute}
	failed := 0
	for i := 0; i < *count; i++ {
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
