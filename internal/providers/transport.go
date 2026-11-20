package providers

// One POST to a chat-completions endpoint. Retries only what cannot have been
// billed: a rate limit, an overloaded answer, a connection refused or a name
// that did not resolve. Anything that may have reached the model fails at
// once and is marked maybe billed.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/metergraph/ezswap/internal/jsnum"
)

const (
	RequestTimeoutMs = 120_000
	MaxResponseBytes = 4 * 1024 * 1024
	MaxAttempts      = 3
	// MaxRetryAfterMs is the longest Retry-After the client waits.
	MaxRetryAfterMs = 60_000
	errorBodyBytes  = 4096
)

type FailureCode string

const (
	CodeCredentialMissing         FailureCode = "credential_missing"
	CodeTemperatureUnsupported    FailureCode = "temperature_unsupported"
	CodeResponseFormatUnsupported FailureCode = "response_format_unsupported"
	CodeProviderRateLimited       FailureCode = "provider_rate_limited"
	CodeProviderOverloaded        FailureCode = "provider_overloaded"
	CodeProviderRefused           FailureCode = "provider_refused"
	CodeProviderUnavailable       FailureCode = "provider_unavailable"
	CodeProviderUnreachable       FailureCode = "provider_unreachable"
	CodeProviderTimeout           FailureCode = "provider_timeout"
	CodeProviderMalformed         FailureCode = "provider_malformed"
	CodeProviderError             FailureCode = "provider_error"
	CodeAborted                   FailureCode = "aborted"
)

// CallFailed is a request that returned no usable reply. The code is stable;
// the message is for people and never carries a key, a prompt or a reply body.
type CallFailed struct {
	Code    FailureCode
	Message string
	// The request may have reached the model although no usage came back.
	MaybeBilled bool
	// HTTP status, when the endpoint answered.
	Status *int
}

func (e *CallFailed) Error() string { return e.Message }

func callFailed(code FailureCode, message string, maybeBilled bool, status int) *CallFailed {
	failure := &CallFailed{Code: code, Message: message, MaybeBilled: maybeBilled}
	if status != 0 {
		failure.Status = &status
	}
	return failure
}

type PostOptions struct {
	Client  *http.Client
	Headers map[string]string
	Timeout time.Duration
	// Waits before a retry; returns early when ctx ends.
	Sleep func(ctx context.Context, ms float64)
	// Clock for latency, in milliseconds.
	Now func() float64
	// Called before each retry, so the caller can flag the call.
	OnRetry func()
}

type PostResult struct {
	Body map[string]any
	// The reply's bytes, for reading it in its own key order.
	Raw []byte
	// Start to end of the request that answered, not of earlier tries or waits.
	LatencyMs float64
}

// NeverSent says a connection was refused or a name did not resolve: nothing was sent.
func NeverSent(err error) bool {
	for err != nil {
		switch e := err.(type) {
		case interface{ Unwrap() []error }:
			// Grouped connection errors: nothing was sent only if every one was refused.
			errs := e.Unwrap()
			if len(errs) == 0 {
				return false
			}
			for _, inner := range errs {
				if !NeverSent(inner) {
					return false
				}
			}
			return true
		case syscall.Errno:
			return e == syscall.ECONNREFUSED
		case *net.DNSError:
			// ENOTFOUND or EAI_AGAIN: the name did not resolve.
			return e.IsNotFound || e.IsTemporary
		}
		err = errors.Unwrap(err)
	}
	return false
}

var decimalSeconds = regexp.MustCompile(`^\d+(\.\d+)?$`)

// RetryAfterMs reads Retry-After (seconds or an HTTP date) or
// retry-after-ms, capped; nil when absent. now is wall time in epoch
// milliseconds.
func RetryAfterMs(headers http.Header, now float64) *float64 {
	capped := func(ms float64) *float64 {
		v := math.Min(ms, MaxRetryAfterMs)
		return &v
	}
	if values, ok := headers[http.CanonicalHeaderKey("retry-after-ms")]; ok && len(values) > 0 {
		ms := strings.Join(values, ", ")
		if decimalSeconds.MatchString(jsTrim(ms)) {
			n, _ := strconv.ParseFloat(jsTrim(ms), 64)
			return capped(n)
		}
	}
	values, ok := headers[http.CanonicalHeaderKey("retry-after")]
	if !ok || len(values) == 0 {
		return nil
	}
	text := jsTrim(strings.Join(values, ", "))
	if decimalSeconds.MatchString(text) {
		n, _ := strconv.ParseFloat(text, 64)
		return capped(n * 1000)
	}
	date, ok := parseHTTPDate(text)
	if !ok {
		return nil
	}
	return capped(math.Max(0, float64(date)-now))
}

// parseHTTPDate stands in for Date.parse on a Retry-After date: the HTTP
// forms, then ISO 8601.
func parseHTTPDate(text string) (int64, bool) {
	if at, err := http.ParseTime(text); err == nil {
		return at.UnixMilli(), true
	}
	if at, err := time.Parse(time.RFC3339Nano, text); err == nil {
		return at.UnixMilli(), true
	}
	return 0, false
}

// readCapped reads at most limit bytes; the flag says the body was longer.
func readCapped(body io.Reader, limit int64) ([]byte, bool, error) {
	if body == nil {
		return []byte{}, false, nil
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return []byte{}, true, nil
	}
	return data, false, nil
}

func errorDetail(response *http.Response) string {
	data, _, err := readCapped(response.Body, errorBodyBytes*64)
	if err != nil {
		return ""
	}
	if len(data) > errorBodyBytes {
		data = data[:errorBodyBytes]
	}
	text := strings.ToValidUTF8(string(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))), "�")
	return strings.ToLower(text)
}

// namesTemperature is a 400 that names the temperature field: the model takes none.
func namesTemperature(detail string) bool {
	return strings.Contains(detail, "temperature")
}

var responseFormatPattern = regexp.MustCompile(`response_format|response format|responseformat|json_schema|json schema`)

// namesResponseFormat is a refusal that names the response format or its JSON schema.
func namesResponseFormat(detail string) bool {
	return responseFormatPattern.MatchString(detail)
}

// errDeadline is the cause a request's own timeout ends it with.
var errDeadline = errors.New("the request timed out")

// deadline is a context that ends with errDeadline after d. Unlike a
// caller's context it belongs to one attempt, so a hung request always ends
// as a timeout; clear releases it.
func deadline(parent context.Context, d time.Duration) (context.Context, func()) {
	return context.WithTimeoutCause(parent, d, errDeadline)
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errDeadline) {
		return true
	}
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

func timedOut(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), errDeadline)
}

func Post(ctx context.Context, url string, payload map[string]any, options PostOptions) (PostResult, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return PostResult{}, callFailed(CodeProviderError, "the call failed", true, 0)
	}
	client := options.Client
	if client == nil {
		client = DefaultClient()
	}
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return PostResult{}, callFailed(CodeAborted, "the call was cancelled before it was sent", false, 0)
		}
		result, retry, err := postOnce(ctx, client, url, data, options, attempt)
		if retry {
			continue
		}
		return result, err
	}
	return PostResult{}, callFailed(CodeProviderUnreachable, "no reply from the endpoint", false, 0)
}

// postOnce is one attempt. retry says to try again after the wait it took.
func postOnce(ctx context.Context, client *http.Client, url string, data []byte, options PostOptions, attempt int) (PostResult, bool, error) {
	attemptCtx, clear := deadline(ctx, options.Timeout)
	defer clear()
	started := options.Now()
	response, err := send(attemptCtx, client, url, data, options.Headers)
	if err != nil {
		if NeverSent(err) {
			if attempt < MaxAttempts {
				if options.OnRetry != nil {
					options.OnRetry()
				}
				options.Sleep(ctx, float64(2000*attempt))
				return PostResult{}, true, nil
			}
			return PostResult{}, false, callFailed(CodeProviderUnreachable, "the endpoint could not be reached", false, 0)
		}
		if ctx.Err() != nil {
			return PostResult{}, false, callFailed(CodeAborted, "the call was cancelled while in flight", true, 0)
		}
		if isTimeout(err) || timedOut(attemptCtx) {
			return PostResult{}, false, callFailed(CodeProviderTimeout, "timed out waiting for the endpoint", true, 0)
		}
		return PostResult{}, false, callFailed(CodeProviderUnreachable, "the connection failed while the request was in flight", true, 0)
	}
	defer response.Body.Close()

	status := response.StatusCode
	if status < 200 || status >= 300 {
		if status == 429 || status == 529 {
			wait := RetryAfterMs(response.Header, float64(time.Now().UnixMilli()))
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, errorBodyBytes))
			if attempt < MaxAttempts {
				if options.OnRetry != nil {
					options.OnRetry()
				}
				ms := float64(2000 * attempt)
				if wait != nil {
					ms = *wait
				}
				options.Sleep(ctx, ms)
				return PostResult{}, true, nil
			}
			if status == 429 {
				return PostResult{}, false, callFailed(CodeProviderRateLimited, "the endpoint answered HTTP 429", false, status)
			}
			return PostResult{}, false, callFailed(CodeProviderOverloaded, "the endpoint answered HTTP 529 (overloaded)", false, status)
		}
		if status >= 400 && status < 500 {
			detail := errorDetail(response)
			if status == 400 && namesTemperature(detail) {
				return PostResult{}, false, callFailed(CodeTemperatureUnsupported, "the endpoint refused the temperature setting", false, status)
			}
			if status != 401 && status != 403 && namesResponseFormat(detail) {
				return PostResult{}, false, callFailed(CodeResponseFormatUnsupported, "the endpoint refused the response format", false, status)
			}
			return PostResult{}, false, callFailed(CodeProviderRefused, fmt.Sprintf("the endpoint answered HTTP %d", status), false, status)
		}
		return PostResult{}, false, callFailed(CodeProviderUnavailable, fmt.Sprintf("the endpoint answered HTTP %d", status), true, status)
	}

	raw, over, err := readCapped(response.Body, MaxResponseBytes)
	if err != nil {
		if isTimeout(err) || timedOut(attemptCtx) {
			return PostResult{}, false, callFailed(CodeProviderTimeout, "timed out reading the endpoint's reply", true, status)
		}
		return PostResult{}, false, callFailed(CodeProviderUnreachable, "the reply was cut off", true, status)
	}
	if over {
		return PostResult{}, false, callFailed(CodeProviderMalformed, "the endpoint's reply exceeds the size limit", true, status)
	}
	// TextDecoder drops a byte order mark; fatal: true refuses bad UTF-8.
	text := bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	var body any
	if !utf8.Valid(text) || json.Unmarshal(text, &body) != nil {
		return PostResult{}, false, callFailed(CodeProviderMalformed, "the endpoint's reply is not JSON", true, status)
	}
	object, ok := body.(map[string]any)
	if !ok || object == nil {
		return PostResult{}, false, callFailed(CodeProviderMalformed, "the endpoint's reply is not an object", true, status)
	}
	latency := math.Max(0, jsnum.Round(options.Now()-started))
	return PostResult{Body: object, Raw: text, LatencyMs: latency}, false, nil
}

func send(ctx context.Context, client *http.Client, url string, data []byte, headers map[string]string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	request.Header.Set("content-type", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	return client.Do(request)
}

// DefaultClient is the client a provider uses without one given: no client
// timeout (each attempt carries its own deadline) and, as Node's fetch, no
// proxy from the environment.
func DefaultClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{Transport: transport}
}
