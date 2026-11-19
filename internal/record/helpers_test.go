package record

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Synthetic bodies in the providers' published shapes.
const (
	chatPath     = "/v1/chat/completions"
	chatRequest  = `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Say hello to example.com."}]}`
	chatResponse = `{"id":"chatcmpl-synthetic","object":"chat.completion","created":1772442000,"model":"gpt-4o-mini-2024-07-18","choices":[{"index":0,"message":{"role":"assistant","content":"Hello, example.com."},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17}}`
)

// recorder is a Run in progress.
type recorder struct {
	t      *testing.T
	addr   string
	dir    string
	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	events  []Event
	summary Summary
	err     error
}

// startRecorder runs a recorder in front of upstream and waits until it
// listens. It is stopped when the test ends, if the test did not stop it.
func startRecorder(t *testing.T, upstream string, adjust ...func(*Options)) *recorder {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &recorder{t: t, dir: t.TempDir(), cancel: cancel, done: make(chan struct{})}
	ready := make(chan string, 1)
	options := Options{
		Upstream: upstream,
		Ready:    func(addr string) { ready <- addr },
		Events: func(e Event) {
			r.mu.Lock()
			r.events = append(r.events, e)
			r.mu.Unlock()
		},
	}
	options.OutDir = r.dir
	for _, f := range adjust {
		f(&options)
	}
	r.dir = options.OutDir
	go func() {
		summary, err := Run(ctx, options)
		r.mu.Lock()
		r.summary, r.err = summary, err
		r.mu.Unlock()
		close(r.done)
	}()
	select {
	case r.addr = <-ready:
	case <-r.done:
		t.Fatalf("the recorder did not start: %v", r.err)
	case <-time.After(10 * time.Second):
		t.Fatal("the recorder did not start in time")
	}
	t.Cleanup(func() {
		cancel()
		<-r.done
	})
	return r
}

func (r *recorder) url(path string) string { return "http://" + r.addr + path }

// stop stops the recorder and returns its summary.
func (r *recorder) stop() Summary {
	r.t.Helper()
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(30 * time.Second):
		r.t.Fatal("the recorder did not stop")
	}
	if r.err != nil {
		r.t.Fatalf("Run: %v", r.err)
	}
	return r.summary
}

func (r *recorder) seen() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

func (r *recorder) file() string { return filepath.Join(r.dir, TraceFile) }

func (r *recorder) fileText() string {
	r.t.Helper()
	data, err := os.ReadFile(r.file())
	if err != nil {
		r.t.Fatal(err)
	}
	return string(data)
}

// lines are the file's lines, each decoded.
func (r *recorder) lines() []traceLine {
	r.t.Helper()
	var out []traceLine
	text := r.fileText()
	if text != "" && !strings.HasSuffix(text, "\n") {
		r.t.Fatalf("the file does not end with a newline: %q", text)
	}
	for _, raw := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if raw == "" {
			continue
		}
		var line traceLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			r.t.Fatalf("a line is not JSON: %v\n%s", err, raw)
		}
		out = append(out, line)
	}
	return out
}

// fakeUpstream is a local stand-in for a provider.
func fakeUpstream(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// answer is an upstream that answers every request with one JSON body.
func answer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}
}

// client neither follows redirects nor touches encodings, so a test sees
// what the recorder sent.
var client = &http.Client{
	Timeout:       30 * time.Second,
	Transport:     &http.Transport{DisableCompression: true},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// send makes one request and returns the response with its body read.
func send(t *testing.T, method, url, body string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		if name == "Host" {
			request.Host = value
			continue
		}
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}

func post(t *testing.T, url, body string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	return send(t, http.MethodPost, url, body, headers)
}

func compact(t *testing.T, source []byte) string {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, source); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
