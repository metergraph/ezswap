// Package judge asks a decision model whether two free-text replies say the
// same thing: the same facts, commitments and next steps, whatever the
// wording. The map uses it for parts whose answers are not JSON, where
// comparing fields is not possible.
//
// Each pair is asked twice, once in each order, because judges lean towards
// one position. The choices carry opaque names (L1, L2) rather than "same"
// and "different", since decision models have been reported to follow an
// option's name over its description. The request and both replies leave the
// machine: they go to the AI Gateway, which passes them to the judge's
// provider, asked not to retain them.
package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// DefaultModel is the decision model that judges.
	DefaultModel = "typesafe-ai/jev"
	// DefaultURL is the AI Gateway, which serves decision models.
	DefaultURL = "https://ai-gateway.vercel.sh/v1"
	// KeyEnv holds the AI Gateway key.
	KeyEnv = "AI_GATEWAY_API_KEY"
	// Question is what the judge is asked, as run.json records it.
	Question = "Do the two replies give the customer the same facts, commitments and next steps? Wording, tone, greetings, sign-offs and length do not matter."

	decisionsPath = "/decisions"
	// Each text is cut to this many characters so a pair fits the judge's
	// context (32k tokens) with room to spare.
	maxText    = 12000
	maxBody    = 1 << 20
	timeout    = 60 * time.Second
	retries    = 3
	sameValue  = "L1"
	diffValue  = "L2"
	sameText   = "The two replies give the customer the same facts, commitments and next steps."
	diffText   = "The two replies differ in a fact, a commitment, a promised action, or what the customer is asked to do."
	styleText  = "Wording, tone, greetings, sign-offs and length do not matter."
	concurrent = 8
)

// Pair is two replies to one request.
type Pair struct {
	// Request is the instructions and messages the replies answer.
	Request string
	A, B    string
}

// Verdict is the judge's answer for one pair.
type Verdict struct {
	// The mean probability, over both orders, that the replies differ.
	PDifferent float64
	// The two orders disagreed on which side of one half they fall.
	Unsure  bool
	CostUSD float64
	// Set when the judge could not be asked; the rest is then zero.
	Err error
}

// Different is the verdict the map counts.
func (v Verdict) Different() bool { return v.PDifferent > 0.5 }

type Options struct {
	// Default DefaultURL.
	BaseURL string
	// Default DefaultModel.
	Model  string
	APIKey string
	// Nil: a client with a 60 second timeout.
	Client *http.Client
	// Calls in flight at once. Default 8.
	Concurrency int
	// Called after each pair, from any goroutine.
	OnProgress func(done, total int)
}

// Judge asks about every pair and returns the verdicts in order. A pair the
// judge could not be asked about carries the error; the others are kept.
func Judge(ctx context.Context, pairs []Pair, options Options) []Verdict {
	verdicts := make([]Verdict, len(pairs))
	workers := options.Concurrency
	if workers <= 0 {
		workers = concurrent
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				verdicts[i] = judgeOne(ctx, pairs[i], options)
				if options.OnProgress != nil {
					mu.Lock()
					done++
					n := done
					mu.Unlock()
					options.OnProgress(n, len(pairs))
				}
			}
		}()
	}
	for i := range pairs {
		if ctx.Err() != nil {
			verdicts[i] = Verdict{Err: ctx.Err()}
			continue
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return verdicts
}

func judgeOne(ctx context.Context, pair Pair, options Options) Verdict {
	// A reply with no text differs from one with text; the judge is not asked.
	if a, b := strings.TrimSpace(pair.A), strings.TrimSpace(pair.B); a == "" || b == "" {
		if a == b {
			return Verdict{PDifferent: 0}
		}
		return Verdict{PDifferent: 1}
	}
	ab, costAB, err := ask(ctx, input(pair.Request, pair.A, pair.B), options)
	if err != nil {
		return Verdict{Err: err}
	}
	ba, costBA, err := ask(ctx, input(pair.Request, pair.B, pair.A), options)
	if err != nil {
		return Verdict{Err: err}
	}
	return Verdict{PDifferent: (ab + ba) / 2, Unsure: (ab > 0.5) != (ba > 0.5), CostUSD: costAB + costBA}
}

func input(request, first, second string) string {
	return "The request:\n" + cut(request) + "\n\nReply 1:\n" + cut(first) + "\n\nReply 2:\n" + cut(second)
}

// cut shortens a text to maxText characters, on a character boundary.
func cut(text string) string {
	if utf8.RuneCountInString(text) <= maxText {
		return text
	}
	n := 0
	for i := range text {
		if n == maxText {
			return text[:i] + " [cut]"
		}
		n++
	}
	return text
}

type question struct {
	Type         string   `json:"type"`
	Name         string   `json:"name"`
	Instructions string   `json:"instructions"`
	Choices      []choice `json:"choices"`
}

type choice struct {
	Value       string `json:"value"`
	Description string `json:"description"`
}

var questions = []question{{
	Type: "choice", Name: "same",
	Instructions: "Compare Reply 1 and Reply 2 as answers to the request. " + styleText,
	Choices:      []choice{{sameValue, sameText}, {diffValue, diffText}},
}}

type answer struct {
	Answers []struct {
		Type          string `json:"type"`
		Name          string `json:"name"`
		Probabilities []struct {
			Value       string  `json:"value"`
			Probability float64 `json:"probability"`
		} `json:"probabilities"`
	} `json:"answers"`
	ProviderMetadata struct {
		Gateway struct {
			Cost string `json:"cost"`
		} `json:"gateway"`
	} `json:"provider_metadata"`
}

// ask returns the probability that Reply 1 and Reply 2 differ, and the cost.
func ask(ctx context.Context, text string, options Options) (float64, float64, error) {
	base := strings.TrimRight(options.BaseURL, "/")
	if base == "" {
		base = DefaultURL
	}
	model := options.Model
	if model == "" {
		model = DefaultModel
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	body, _ := json.Marshal(map[string]any{
		"model": model, "input": text, "questions": questions,
		"providerOptions": map[string]any{"gateway": map[string]any{"zeroDataRetention": true}},
	})
	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return 0, 0, ctx.Err()
			case <-time.After(time.Duration(1<<(attempt-1)) * time.Second):
			}
		}
		p, cost, retry, err := askOnce(ctx, client, base, options.APIKey, body)
		if err == nil {
			return p, cost, nil
		}
		lastErr = err
		if !retry {
			break
		}
	}
	return 0, 0, lastErr
}

func askOnce(ctx context.Context, client *http.Client, base, key string, body []byte) (float64, float64, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+decisionsPath, bytes.NewReader(body))
	if err != nil {
		return 0, 0, false, errors.New("the judge's address is not a URL")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "ezswap")
	if key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return 0, 0, false, ctx.Err()
		}
		return 0, 0, true, errors.New("the judge did not answer")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBody))
	response.Body.Close()
	if err != nil {
		return 0, 0, true, errors.New("the judge's answer was cut off")
	}
	if response.StatusCode != http.StatusOK {
		retry := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return 0, 0, retry, fmt.Errorf("the judge answered HTTP %d%s", response.StatusCode, gatewayMessage(data))
	}
	var decoded answer
	if json.Unmarshal(data, &decoded) != nil || len(decoded.Answers) == 0 {
		return 0, 0, false, errors.New("the judge's answer was not in the expected shape")
	}
	p, found := -1.0, false
	for _, a := range decoded.Answers[0].Probabilities {
		if a.Value == diffValue {
			p, found = a.Probability, true
		}
	}
	if !found || p < 0 || p > 1 {
		return 0, 0, false, errors.New("the judge's answer had no probability for the choices")
	}
	cost, _ := strconv.ParseFloat(decoded.ProviderMetadata.Gateway.Cost, 64)
	return p, cost, false, nil
}

// gatewayMessage is the gateway's own error message, if it sent one.
func gatewayMessage(data []byte) string {
	var e struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) != nil {
		return ""
	}
	message := e.Error.Message
	if message == "" {
		message = e.Message
	}
	if message == "" {
		return ""
	}
	if utf8.RuneCountInString(message) > 200 {
		message = string([]rune(message)[:200])
	}
	return ": " + message
}
