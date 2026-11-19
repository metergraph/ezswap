package record

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/metergraph/replay/internal/adapters"
	"github.com/metergraph/replay/internal/cases"
)

func TestForwardsBodyByteForByte(t *testing.T) {
	// Odd spacing, escapes and non-ASCII text: nothing may be re-encoded.
	request := "{\n  \"model\" : \"gpt-4o-mini\",\t\"messages\":[ {\"role\":\"user\",\"content\":\"caf\\u00e9 ü <b>&amp;</b>\"} ]\r\n}\n"
	response := "{ \"id\":\"chatcmpl-synthetic\",\n\"choices\":[{\"index\":0,\"message\":{\"role\":\"assistant\",\"content\":\"naïve <i>\"},\"finish_reason\":\"stop\"}] }\n\n"
	var got [][]byte
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, response)
	})
	rec := startRecorder(t, upstream.URL)
	for _, path := range []string{chatPath, "/v1/embeddings"} {
		_, body := post(t, rec.url(path), request, nil)
		if string(body) != response {
			t.Errorf("%s: the response changed on the way back:\n%q", path, body)
		}
	}
	rec.stop()
	if len(got) != 2 || string(got[0]) != request || string(got[1]) != request {
		t.Errorf("the request changed on the way up:\n%q", got)
	}
}

func TestUpstreamErrorReachesClient(t *testing.T) {
	const failure = `{"error":{"message":"Rate limit reached.","type":"rate_limit_error"}}`
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, failure)
	})
	rec := startRecorder(t, upstream.URL)
	response, body := post(t, rec.url(chatPath), chatRequest, nil)
	if response.StatusCode != http.StatusTooManyRequests || string(body) != failure || response.Header.Get("Retry-After") != "7" {
		t.Errorf("got %d %q, Retry-After %q", response.StatusCode, body, response.Header.Get("Retry-After"))
	}
}

func TestRedirectIsReturnedNotFollowed(t *testing.T) {
	var hits atomic.Int32
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
	})
	rec := startRecorder(t, upstream.URL)
	response, _ := post(t, rec.url(chatPath), chatRequest, nil)
	if response.StatusCode != http.StatusTemporaryRedirect || response.Header.Get("Location") != "/elsewhere" {
		t.Errorf("got %d, Location %q", response.StatusCode, response.Header.Get("Location"))
	}
	if hits.Load() != 1 {
		t.Errorf("the upstream was asked %d times, want 1: the recorder followed the redirect", hits.Load())
	}
	if summary := rec.stop(); summary.Skipped[skipErrorStatus] != 1 || summary.Recorded != 0 {
		t.Errorf("summary: %+v", summary)
	}
}

func TestRecordedFileIsReadByRawReaderAndReplayable(t *testing.T) {
	upstream := fakeUpstream(t, answer(chatResponse))
	rec := startRecorder(t, upstream.URL)
	post(t, rec.url(chatPath), chatRequest, map[string]string{"X-Replay-Part": "greeter", "X-Replay-Trace": "trace-1"})
	summary := rec.stop()
	if summary.Recorded != 1 || summary.ByPart["greeter"] != 1 || summary.File != rec.file() {
		t.Fatalf("summary: %+v", summary)
	}

	// The line itself: keys in order, one line, the bodies as they were sent.
	line := regexp.MustCompile(`^\{"id":"rec-[0-9a-f]{12}","ts":"[0-9T:-]+Z","latency_ms":[0-9]+,"provider":"openai","model":"gpt-4o-mini","part":"greeter","trace_id":"trace-1","request":(.*),"response":(.*)\}\n$`)
	match := line.FindStringSubmatch(rec.fileText())
	if match == nil {
		t.Fatalf("unexpected line:\n%s", rec.fileText())
	}
	if match[1] != chatRequest || match[2] != chatResponse {
		t.Errorf("bodies:\n%s\n%s", match[1], match[2])
	}
	if _, err := time.Parse(time.RFC3339, rec.lines()[0].TS); err != nil {
		t.Errorf("ts: %v", err)
	}

	// The way internal/run reads a trace file.
	calls, err := adapters.ReadTraces(rec.file(), adapters.ReadTracesOptions{
		OnSkip: func(line int, reason string) { t.Errorf("line %d skipped: %s", line, reason) },
	})
	if err != nil || len(calls) != 1 {
		t.Fatalf("ReadTraces: %d calls, %v", len(calls), err)
	}
	call := calls[0]
	if call.Source != "raw" || call.Provider == nil || *call.Provider != "openai" || call.Model == nil || *call.Model != "gpt-4o-mini" {
		t.Errorf("call: %+v", call)
	}
	if call.TraceID == nil || *call.TraceID != "trace-1" || call.TS == nil || call.Response.LatencyMs == nil {
		t.Errorf("trace id, time or latency missing: %+v", call)
	}
	prepared := cases.PrepareCase(call, cases.PrepareCaseOptions{})
	if prepared.NotReplayable != nil {
		t.Fatalf("not replayable: %s", *prepared.NotReplayable)
	}
	if len(prepared.Messages) != 1 || prepared.Messages[0].Content != "Say hello to example.com." {
		t.Errorf("messages: %+v", prepared.Messages)
	}
	if prepared.Production.Output == nil || *prepared.Production.Output != "Hello, example.com." {
		t.Errorf("production output: %v", prepared.Production.Output)
	}
}

func TestNoKeyInFileOrOutput(t *testing.T) {
	const key = "sk-synthetic-DO-NOT-LEAK-0123456789"
	headers := map[string]string{"Authorization": "Bearer " + key, "X-Api-Key": key, "X-Goog-Api-Key": key}
	var output []string // everything the recorder said, anywhere

	check := func(rec *recorder, paths ...string) {
		t.Helper()
		for _, path := range paths {
			response, body := post(t, rec.url(path+"?key="+key+"&alt=json"), chatRequest, headers)
			output = append(output, string(body), fmt.Sprint(response.Header))
		}
		rec.cancel()
		<-rec.done
		output = append(output, rec.fileText(), fmt.Sprintf("%+v", rec.seen()), fmt.Sprintf("%+v", rec.summary), fmt.Sprint(rec.err))
	}

	// An upstream that answers: the call is recorded.
	var sawKey atomic.Bool
	working := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") == key && r.Header.Get("Authorization") == "Bearer "+key {
			sawKey.Store(true)
		}
		answer(chatResponse)(w, r)
	})
	rec := startRecorder(t, working.URL)
	check(rec, chatPath, "/v1beta/models/gemini-2.0-flash:generateContent", "/v1/models")
	if !sawKey.Load() || rec.summary.Recorded != 2 {
		t.Fatalf("the key did not reach the upstream, or the calls were not recorded: %+v", rec.summary)
	}

	// An upstream that refuses the connection: the recorder writes the error.
	gone := httptest.NewServer(nil)
	goneURL := gone.URL
	gone.Close()
	rec = startRecorder(t, goneURL)
	response, body := post(t, rec.url(chatPath+"?key="+key), chatRequest, headers)
	if response.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "connection refused") {
		t.Errorf("got %d %q, want a 502 naming the kind of failure", response.StatusCode, body)
	}
	if strings.Contains(string(body), strings.TrimPrefix(goneURL, "http://")) {
		t.Errorf("the error names the upstream address: %q", body)
	}
	check(rec, chatPath, "/v1/models")

	// An upstream that drops the connection part way through its answer.
	dropping := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "1000")
		io.WriteString(w, `{"id":`)
		w.(http.Flusher).Flush()
		connection, _, _ := w.(http.Hijacker).Hijack()
		connection.Close()
	})
	rec = startRecorder(t, dropping.URL)
	request, _ := http.NewRequest(http.MethodPost, rec.url(chatPath+"?key="+key), strings.NewReader(chatRequest))
	request.Header.Set("Authorization", "Bearer "+key)
	if response, err := client.Do(request); err == nil {
		_, err = io.ReadAll(response.Body)
		response.Body.Close()
		output = append(output, fmt.Sprint(err))
	} else {
		output = append(output, err.Error())
	}
	rec.cancel()
	<-rec.done
	if rec.summary.Skipped[skipStreamCut] != 1 {
		t.Errorf("summary: %+v", rec.summary)
	}
	output = append(output, rec.fileText(), fmt.Sprintf("%+v", rec.seen()), fmt.Sprintf("%+v", rec.summary), fmt.Sprint(rec.err))

	// Errors about the upstream URL itself.
	for _, upstream := range []string{"http://api.example.com/?key=" + key, "https://user:" + key + "@api.example.com", "ftp://" + key + ".example.com", "https://api.example.com/%zz" + key} {
		err := Validate(Options{Upstream: upstream})
		if err == nil {
			t.Errorf("%q passed validation", strings.ReplaceAll(upstream, key, "KEY"))
			continue
		}
		output = append(output, err.Error())
		_, err = Run(context.Background(), Options{Upstream: upstream, OutDir: t.TempDir()})
		output = append(output, fmt.Sprint(err))
	}

	for _, text := range output {
		if strings.Contains(text, key) {
			t.Errorf("the key appears in:\n%s", strings.ReplaceAll(text, key, "<<KEY>>"))
		}
		if strings.Contains(text, "alt=json") || strings.Contains(text, "?key") {
			t.Errorf("a query string appears in:\n%s", strings.ReplaceAll(text, key, "<<KEY>>"))
		}
	}
}

func TestReplayHeadersRemovedAndKept(t *testing.T) {
	var seen http.Header
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		answer(chatResponse)(w, r)
	})
	rec := startRecorder(t, upstream.URL)
	post(t, rec.url(chatPath), chatRequest, map[string]string{
		"X-Replay-Part":    "summarize",
		"X-Replay-Trace":   "session-42",
		"Authorization":    "Bearer synthetic",
		"OpenAI-Beta":      "assistants=v2",
		"X-Forwarded-For":  "203.0.113.7",
		"X-Custom-Example": "kept",
	})
	rec.stop()
	if _, ok := seen["X-Replay-Part"]; ok {
		t.Error("X-Replay-Part went upstream")
	}
	if _, ok := seen["X-Replay-Trace"]; ok {
		t.Error("X-Replay-Trace went upstream")
	}
	for name, want := range map[string]string{
		"Authorization": "Bearer synthetic", "Openai-Beta": "assistants=v2", "X-Custom-Example": "kept",
		"X-Forwarded-For": "203.0.113.7", "Content-Type": "application/json",
	} {
		if got := seen.Get(name); got != want {
			t.Errorf("upstream saw %s: %q, want %q", name, got, want)
		}
	}
	lines := rec.lines()
	if len(lines) != 1 || lines[0].Part != "summarize" || lines[0].TraceID != "session-42" {
		t.Errorf("lines: %+v", lines)
	}
	if events := rec.seen(); len(events) != 1 || events[0] != (Event{Path: chatPath, Part: "summarize", Outcome: "recorded"}) {
		t.Errorf("events: %+v", events)
	}

	// Without the headers the keys are left out of the line.
	rec = startRecorder(t, upstream.URL)
	post(t, rec.url(chatPath), chatRequest, nil)
	summary := rec.stop()
	if text := rec.fileText(); strings.Contains(text, `"part"`) || strings.Contains(text, `"trace_id"`) {
		t.Errorf("empty keys written: %s", text)
	}
	if summary.ByPart[UnnamedPart] != 1 {
		t.Errorf("summary: %+v", summary)
	}
}

func TestPartValueCleanedAndCut(t *testing.T) {
	upstream := fakeUpstream(t, answer(chatResponse))
	rec := startRecorder(t, upstream.URL)
	post(t, rec.url(chatPath), chatRequest, map[string]string{
		"X-Replay-Part":  "  checkout\tsummary   " + strings.Repeat("x", 100),
		"X-Replay-Trace": strings.Repeat("é", 100),
	})
	summary := rec.stop()
	want := "checkout summary " + strings.Repeat("x", 47)
	lines := rec.lines()
	if len(lines) != 1 || lines[0].Part != want || summary.ByPart[want] != 1 {
		t.Errorf("part %q, summary %+v", lines[0].Part, summary)
	}
	if got := lines[0].TraceID; got != strings.Repeat("é", 64) {
		t.Errorf("trace id: %d characters", utf8.RuneCountInString(got))
	}

	// An HTTP client cannot send most control characters, but the cleaning
	// does not rely on that.
	for _, value := range []string{"a\x1b[2Jb\r\nc", "\x00\x07part\x7f", "  \t ", strings.Repeat(" y", 200)} {
		cleaned := cleanLabel(value)
		if utf8.RuneCountInString(cleaned) > maxLabel || cleaned != strings.TrimSpace(cleaned) {
			t.Errorf("%q cleaned to %q", value, cleaned)
		}
		for _, r := range cleaned {
			if r < 0x20 || r == 0x7f {
				t.Errorf("%q kept a control character: %q", value, cleaned)
			}
		}
	}
	if cleanLabel(" \t ") != "" {
		t.Error("a blank value should clean to nothing")
	}
}

func TestAcceptEncodingIsIdentity(t *testing.T) {
	var seen []string
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, strings.Join(r.Header.Values("Accept-Encoding"), "|"))
		answer(chatResponse)(w, r)
	})
	rec := startRecorder(t, upstream.URL)
	post(t, rec.url(chatPath), chatRequest, map[string]string{"Accept-Encoding": "gzip, deflate, br"})
	post(t, rec.url(chatPath), chatRequest, nil)
	send(t, http.MethodGet, rec.url("/v1/models"), "", map[string]string{"Accept-Encoding": "gzip"})
	rec.stop()
	if len(seen) != 3 || seen[0] != "identity" || seen[1] != "identity" || seen[2] != "identity" {
		t.Errorf("upstream saw Accept-Encoding %q", seen)
	}
}

func TestRejectsForeignHost(t *testing.T) {
	var hits atomic.Int32
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		answer(chatResponse)(w, r)
	})
	rec := startRecorder(t, upstream.URL)
	_, port, _ := net.SplitHostPort(rec.addr)
	for _, host := range []string{"attacker.example.com", "attacker.example.com:" + port, "127.0.0.1", "127.0.0.1:1", "localhost", "[::1]:" + port, "127.0.0.1." + port} {
		response, _ := post(t, rec.url(chatPath), chatRequest, map[string]string{"Host": host})
		if response.StatusCode != http.StatusForbidden {
			t.Errorf("Host %q: got %d, want 403", host, response.StatusCode)
		}
	}
	if hits.Load() != 0 {
		t.Errorf("%d refused requests reached the upstream", hits.Load())
	}
	for _, host := range []string{"127.0.0.1:" + port, "localhost:" + port} {
		response, _ := post(t, rec.url(chatPath), chatRequest, map[string]string{"Host": host})
		if response.StatusCode != http.StatusOK {
			t.Errorf("Host %q: got %d, want 200", host, response.StatusCode)
		}
	}
	if summary := rec.stop(); summary.Recorded != 2 || len(summary.Skipped) != 0 || summary.Forwarded != 0 {
		t.Errorf("summary: %+v", summary)
	}
}

func TestRejectsOrigin(t *testing.T) {
	var hits atomic.Int32
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		answer(chatResponse)(w, r)
	})
	rec := startRecorder(t, upstream.URL)
	for _, origin := range []string{"https://attacker.example.com", "http://" + rec.addr, "null"} {
		for _, method := range []string{http.MethodPost, http.MethodOptions, http.MethodGet} {
			response, _ := send(t, method, rec.url(chatPath), chatRequest, map[string]string{"Origin": origin})
			if response.StatusCode != http.StatusForbidden {
				t.Errorf("%s with Origin %q: got %d, want 403", method, origin, response.StatusCode)
			}
			if response.Header.Get("Access-Control-Allow-Origin") != "" {
				t.Error("the refusal carries a CORS header")
			}
		}
	}
	if hits.Load() != 0 {
		t.Errorf("%d refused requests reached the upstream", hits.Load())
	}
}

func TestRejectsPlainHTTPUpstream(t *testing.T) {
	for _, upstream := range []string{
		"http://api.example.com",
		"http://api.example.com:8080/v1",
		"http://192.0.2.10",
		"http://localhost.example.com",
		"http://127.0.0.1.example.com",
		"ftp://127.0.0.1",
		"api.example.com",
		"https://",
		"https://user:secret@api.example.com",
		"https://api.example.com?x=1",
	} {
		if err := Validate(Options{Upstream: upstream}); err == nil {
			t.Errorf("Validate accepted %q", upstream)
		}
		if _, err := Run(context.Background(), Options{Upstream: upstream, OutDir: t.TempDir()}); err == nil {
			t.Errorf("Run accepted %q", upstream)
		}
	}
	if err := Validate(Options{Port: 70000}); err == nil {
		t.Error("Validate accepted port 70000")
	}
}

func TestAllowsLoopbackHTTP(t *testing.T) {
	for _, upstream := range []string{
		"", // the default
		"https://api.openai.com",
		"https://api.example.com/v1",
		"http://127.0.0.1:8080",
		"http://127.8.9.10",
		"http://localhost:11434",
		"http://LOCALHOST:11434/v1",
		"http://[::1]:8080",
	} {
		if err := Validate(Options{Upstream: upstream}); err != nil {
			t.Errorf("Validate refused %q: %v", upstream, err)
		}
	}
}

func TestPortInUse(t *testing.T) {
	taken, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	dir := t.TempDir()
	_, err = Run(context.Background(), Options{Upstream: "http://127.0.0.1:1", Port: taken.Addr().(*net.TCPAddr).Port, OutDir: dir})
	if !errors.Is(err, ErrPortInUse) {
		t.Fatalf("got %v, want ErrPortInUse", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, TraceFile)); !os.IsNotExist(statErr) {
		t.Error("a trace file was created although the recorder never started")
	}
}

func TestGeminiModelFromPath(t *testing.T) {
	const response = `{"candidates":[{"content":{"parts":[{"text":"Paris."}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":2,"totalTokenCount":9},"modelVersion":"gemini-2.0-flash"}`
	const request = `{"contents":[{"role":"user","parts":[{"text":"What is the capital of France?"}]}]}`
	upstream := fakeUpstream(t, answer(response))
	rec := startRecorder(t, upstream.URL)
	post(t, rec.url("/v1beta/models/gemini-2.0-flash:generateContent"), request, nil)
	post(t, rec.url("/v1/projects/example/locations/global/publishers/google/models/gemini-2.5-pro:generateContent"), request, nil)
	rec.stop()
	lines := rec.lines()
	if len(lines) != 2 {
		t.Fatalf("%d lines", len(lines))
	}
	for i, want := range []string{"gemini-2.0-flash", "gemini-2.5-pro"} {
		if lines[i].Provider != "google" || lines[i].Model != want {
			t.Errorf("line %d: provider %q, model %q", i, lines[i].Provider, lines[i].Model)
		}
	}
	calls, err := adapters.ReadTraces(rec.file(), adapters.ReadTracesOptions{})
	if err != nil || len(calls) != 2 || calls[0].Response.Text == nil || *calls[0].Response.Text != "Paris." {
		t.Errorf("ReadTraces: %+v, %v", calls, err)
	}
	if calls[0].Model == nil || *calls[0].Model != "gemini-2.0-flash" {
		t.Errorf("the reader's model: %v", calls[0].Model)
	}

	for path, want := range map[string]string{
		"/v1beta/models/gemini-2.0-flash:streamGenerateContent": "gemini-2.0-flash",
		"/models/m:generateContent":                             "m",
		"/v1beta/tunedModels/example:generateContent":           "",
		"/v1beta/models/:generateContent":                       "",
	} {
		if got := geminiModel(path); got != want {
			t.Errorf("geminiModel(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestQueryIsForwardedNotWritten(t *testing.T) {
	const query = "key=synthetic-query-key&alt=json&note=a%20b+c&empty=&%zz"
	var seen []string
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+"?"+r.URL.RawQuery)
		answer(`{"candidates":[{"content":{"parts":[{"text":"Paris."}],"role":"model"},"finishReason":"STOP","index":0}]}`)(w, r)
	})
	rec := startRecorder(t, upstream.URL)
	const path = "/v1beta/models/gemini-2.0-flash:generateContent"
	post(t, rec.url(path+"?"+query), `{"contents":[{"role":"user","parts":[{"text":"Capital of France?"}]}]}`, nil)
	send(t, http.MethodGet, rec.url("/v1beta/models?"+query), "", nil)
	summary := rec.stop()
	if len(seen) != 2 || seen[0] != path+"?"+query || seen[1] != "/v1beta/models?"+query {
		t.Errorf("upstream saw %q", seen)
	}
	if summary.Recorded != 1 {
		t.Fatalf("summary: %+v", summary)
	}
	events := rec.seen()
	if len(events) != 1 || events[0].Path != path {
		t.Errorf("events: %+v", events)
	}
	for _, text := range []string{rec.fileText(), fmt.Sprintf("%+v", events), fmt.Sprintf("%+v", summary)} {
		if strings.Contains(text, "synthetic-query-key") || strings.Contains(text, "alt=json") {
			t.Errorf("the query string appears in:\n%s", text)
		}
	}
}

func TestUpstreamPathPrefixIsKept(t *testing.T) {
	var seen string
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Host + r.URL.Path
		answer(chatResponse)(w, r)
	})
	rec := startRecorder(t, upstream.URL+"/api/")
	post(t, rec.url(chatPath), chatRequest, nil)
	rec.stop()
	if want := strings.TrimPrefix(upstream.URL, "http://") + "/api" + chatPath; seen != want {
		t.Errorf("upstream saw %q, want %q", seen, want)
	}
}

func TestErrorStatusNotRecorded(t *testing.T) {
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		status, _ := strconv.Atoi(r.Header.Get("X-Answer-Status"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusNotModified {
			io.WriteString(w, chatResponse)
		}
	})
	rec := startRecorder(t, upstream.URL)
	statuses := []int{400, 401, 404, 429, 500, 503, 304}
	for _, status := range statuses {
		response, _ := post(t, rec.url(chatPath), chatRequest, map[string]string{"X-Replay-Part": "p", "X-Answer-Status": strconv.Itoa(status)})
		if response.StatusCode != status {
			t.Errorf("got %d, want %d", response.StatusCode, status)
		}
	}
	summary := rec.stop()
	if summary.Recorded != 0 || summary.Skipped[skipErrorStatus] != len(statuses) || rec.fileText() != "" {
		t.Errorf("summary: %+v, file %q", summary, rec.fileText())
	}
	for _, event := range rec.seen() {
		if event != (Event{Path: chatPath, Part: "p", Outcome: "error status"}) {
			t.Errorf("event: %+v", event)
		}
	}
}

func TestOtherPathsNotRecorded(t *testing.T) {
	var hits atomic.Int32
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		answer(chatResponse)(w, r)
	})
	rec := startRecorder(t, upstream.URL)
	requests := [][2]string{
		{http.MethodGet, "/v1/models"},
		{http.MethodPost, "/v1/embeddings"},
		{http.MethodPost, "/v1/messages/count_tokens"},
		{http.MethodPost, "/v1/messages/batches"},
		{http.MethodPost, "/v2/messages"},
		{http.MethodPost, "/v1/threads/thread_1/messages"},
		{http.MethodGet, chatPath},
		{http.MethodGet, "/v1/responses"},
		{http.MethodPost, "/v1/responses/resp_1/cancel"},
		{http.MethodPost, "/v1beta/models/gemini-2.0-flash:countTokens"},
		{http.MethodDelete, "/v1/files/file_1"},
	}
	for _, request := range requests {
		body := ""
		if request[0] == http.MethodPost {
			body = chatRequest
		}
		if response, _ := send(t, request[0], rec.url(request[1]), body, nil); response.StatusCode != http.StatusOK {
			t.Errorf("%s %s: got %d", request[0], request[1], response.StatusCode)
		}
	}
	summary := rec.stop()
	if int(hits.Load()) != len(requests) || summary.Forwarded != len(requests) {
		t.Errorf("%d reached the upstream, %d counted, want %d", hits.Load(), summary.Forwarded, len(requests))
	}
	if summary.Recorded != 0 || len(summary.Skipped) != 0 || rec.fileText() != "" || len(rec.seen()) != 0 {
		t.Errorf("summary: %+v, file %q, events %+v", summary, rec.fileText(), rec.seen())
	}
}

func TestOversizeBodyNotRecorded(t *testing.T) {
	big := strings.Repeat("a", maxBody)
	bigRequest := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"` + big + `"}]}`
	bigResponse := `{"id":"chatcmpl-synthetic","choices":[{"index":0,"message":{"role":"assistant","content":"` + big + `"},"finish_reason":"stop"}]}`
	var sums [][32]byte
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sums = append(sums, sha256.Sum256(body))
		w.Header().Set("Content-Type", "application/json")
		if len(body) > maxBody {
			io.WriteString(w, chatResponse)
		} else {
			io.WriteString(w, bigResponse)
		}
	})
	rec := startRecorder(t, upstream.URL)
	if _, body := post(t, rec.url(chatPath), bigRequest, nil); string(body) != chatResponse {
		t.Error("the answer to the large request changed")
	}
	if _, body := post(t, rec.url(chatPath), chatRequest, nil); string(body) != bigResponse {
		t.Errorf("the large response did not arrive whole: %d bytes, want %d", len(body), len(bigResponse))
	}
	summary := rec.stop()
	if len(sums) != 2 || sums[0] != sha256.Sum256([]byte(bigRequest)) {
		t.Error("the large request did not arrive whole")
	}
	if summary.Recorded != 0 || summary.Skipped[skipTooLarge] != 2 || rec.fileText() != "" {
		t.Errorf("summary: %+v, %d bytes written", summary, len(rec.fileText()))
	}
}

func TestOversizeStreamNotRecorded(t *testing.T) {
	stream := string(fixture(t, "chat.stream.sse"))
	padding := ": " + strings.Repeat("p", 64*1024) + "\n\n" // an SSE comment
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		for sent := 0; sent <= maxBody; sent += len(padding) {
			io.WriteString(w, padding)
		}
		io.WriteString(w, stream)
	})
	rec := startRecorder(t, upstream.URL)
	if _, body := post(t, rec.url(chatPath), chatRequest, nil); !strings.HasSuffix(string(body), stream) || len(body) <= maxBody {
		t.Errorf("the stream did not arrive whole: %d bytes", len(body))
	}
	if summary := rec.stop(); summary.Skipped[skipTooLarge] != 1 || rec.fileText() != "" {
		t.Errorf("summary: %+v", summary)
	}
}

func TestNonJSONBodyNotRecorded(t *testing.T) {
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasPrefix(r.URL.Path, "/html/"):
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<html>Service page</html>")
		case strings.HasPrefix(r.URL.Path, "/empty/"):
		default:
			w.Header().Set("Content-Type", "application/json")
			if strings.HasPrefix(string(body), "{") {
				io.WriteString(w, `{"truncated":`)
			} else {
				io.WriteString(w, chatResponse)
			}
		}
	})
	rec := startRecorder(t, upstream.URL)
	post(t, rec.url(chatPath), "model=gpt-4o-mini&prompt=hello", nil) // the request is not JSON
	post(t, rec.url(chatPath), "", nil)                               // no request body
	post(t, rec.url(chatPath), chatRequest+"}", nil)                  // trailing text
	if _, body := post(t, rec.url("/html"+chatPath), chatRequest, nil); string(body) != "<html>Service page</html>" {
		t.Errorf("the response changed: %q", body)
	}
	post(t, rec.url("/empty"+chatPath), chatRequest, nil)
	post(t, rec.url(chatPath), chatRequest, nil) // the response stops part way through a value
	summary := rec.stop()
	if summary.Recorded != 0 || summary.Skipped[skipNotJSON] != 6 || rec.fileText() != "" {
		t.Errorf("summary: %+v, file %q", summary, rec.fileText())
	}
}

func TestCompressedBodyNotRecorded(t *testing.T) {
	var zipped bytes.Buffer
	zip := gzip.NewWriter(&zipped)
	io.WriteString(zip, chatResponse)
	zip.Close()
	var requestBodies [][]byte
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, body)
		if r.Header.Get("Content-Encoding") == "gzip" {
			answer(chatResponse)(w, r)
			return
		}
		// A provider that compresses although asked for identity.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(zipped.Bytes())
	})
	rec := startRecorder(t, upstream.URL)
	response, body := post(t, rec.url(chatPath), chatRequest, nil)
	if response.Header.Get("Content-Encoding") != "gzip" || !bytes.Equal(body, zipped.Bytes()) {
		t.Errorf("the compressed response changed: %q, %d bytes", response.Header.Get("Content-Encoding"), len(body))
	}
	if _, body := post(t, rec.url(chatPath), zipped.String(), map[string]string{"Content-Encoding": "gzip"}); string(body) != chatResponse {
		t.Errorf("the answer to the compressed request changed: %q", body)
	}
	summary := rec.stop()
	if len(requestBodies) != 2 || !bytes.Equal(requestBodies[1], zipped.Bytes()) {
		t.Error("the compressed request did not arrive as sent")
	}
	if summary.Recorded != 0 || summary.Skipped[skipCompressed] != 2 || rec.fileText() != "" {
		t.Errorf("summary: %+v", summary)
	}

	// "identity" is not a compression.
	rec = startRecorder(t, fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "identity")
		answer(chatResponse)(w, r)
	}).URL)
	post(t, rec.url(chatPath), chatRequest, map[string]string{"Content-Encoding": "identity"})
	if summary := rec.stop(); summary.Recorded != 1 {
		t.Errorf("summary: %+v", summary)
	}
}

func TestStoredStateNotRecorded(t *testing.T) {
	const response = `{"id":"resp_synthetic2","object":"response","created_at":1772442060,"status":"completed","model":"gpt-5-mini","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"4"}]}]}`
	upstream := fakeUpstream(t, answer(response))
	rec := startRecorder(t, upstream.URL)
	post(t, rec.url("/v1/responses"), `{"model":"gpt-5-mini","input":"And doubled?","previous_response_id":"resp_synthetic1"}`, nil)
	post(t, rec.url("/v1/responses"), `{"model":"gpt-5-mini","input":"What is 2+2?","previous_response_id":null}`, nil)
	post(t, rec.url("/v1/responses"), `{"model":"gpt-5-mini","input":"What is 2+2?"}`, nil)
	summary := rec.stop()
	if summary.Recorded != 2 || summary.Skipped[skipStoredState] != 1 || len(rec.lines()) != 2 {
		t.Errorf("summary: %+v", summary)
	}
	if strings.Contains(rec.fileText(), "resp_synthetic1") {
		t.Error("the call that continues a stored conversation was written")
	}
}

func TestConcurrentCallsWriteWholeLines(t *testing.T) {
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Content string }
		}
		json.NewDecoder(r.Body).Decode(&request)
		// The answer repeats the prompt, so a line holds a matching pair.
		reply, _ := json.Marshal(request.Messages[0].Content)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"chatcmpl-synthetic","choices":[{"index":0,"message":{"role":"assistant","content":%s},"finish_reason":"stop"}]}`, reply)
	})
	rec := startRecorder(t, upstream.URL)
	const calls = 48
	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			content := strconv.Itoa(i) + ":" + strings.Repeat(string(rune('a'+i%26)), 1000+i*4000)
			body := fmt.Sprintf(`{"model":"gpt-4o-mini","messages":[{"role":"user","content":%q}]}`, content)
			request, _ := http.NewRequest(http.MethodPost, rec.url(chatPath), strings.NewReader(body))
			request.Header.Set("X-Replay-Part", "part-"+strconv.Itoa(i%3))
			response, err := client.Do(request)
			if err != nil {
				t.Error(err)
				return
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}()
	}
	wg.Wait()
	summary := rec.stop()
	lines := rec.lines() // fails on a line that is not whole JSON
	if len(lines) != calls || summary.Recorded != calls || len(rec.seen()) != calls {
		t.Fatalf("%d lines, %d recorded, %d events, want %d", len(lines), summary.Recorded, len(rec.seen()), calls)
	}
	if summary.ByPart["part-0"] != calls/3 || summary.ByPart["part-1"] != calls/3 || summary.ByPart["part-2"] != calls/3 {
		t.Errorf("by part: %+v", summary.ByPart)
	}
	ids := map[string]bool{}
	for _, line := range lines {
		var request struct {
			Messages []struct{ Content string }
		}
		var response struct {
			Choices []struct{ Message struct{ Content string } }
		}
		if json.Unmarshal(line.Request, &request) != nil || json.Unmarshal(line.Response, &response) != nil {
			t.Fatal("a body could not be read")
		}
		if request.Messages[0].Content != response.Choices[0].Message.Content {
			t.Error("a line pairs one call's request with another call's response")
		}
		ids[line.ID] = true
	}
	if len(ids) != calls {
		t.Errorf("%d distinct ids for %d calls", len(ids), calls)
	}
}

func TestAppendAfterPartialLine(t *testing.T) {
	upstream := fakeUpstream(t, answer(chatResponse))
	dir := t.TempDir()
	path := filepath.Join(dir, TraceFile)
	const partial = `{"id":"rec-000000000000","ts":"2026-03-02T09:00:00Z","requ`
	if err := os.WriteFile(path, []byte(partial), 0o600); err != nil {
		t.Fatal(err)
	}
	inDir := func(o *Options) { o.OutDir = dir }
	rec := startRecorder(t, upstream.URL, inDir)
	post(t, rec.url(chatPath), chatRequest, nil)
	rec.stop()
	// A second run finds a file that ends with a newline and adds none.
	rec = startRecorder(t, upstream.URL, inDir)
	post(t, rec.url(chatPath), chatRequest, nil)
	rec.stop()

	lines := strings.Split(rec.fileText(), "\n")
	if len(lines) != 4 || lines[0] != partial || lines[3] != "" {
		t.Fatalf("unexpected file:\n%s", rec.fileText())
	}
	for _, line := range lines[1:3] {
		if !json.Valid([]byte(line)) || !strings.HasPrefix(line, `{"id":"rec-`) {
			t.Errorf("not a whole line: %s", line)
		}
	}
}

func TestLineSeparatorsStayOnOneLine(t *testing.T) {
	// JSON allows U+2028 and U+2029 raw in a string; the trace reader ends a
	// line at them.
	request := "{\"model\":\"gpt-4o-mini\",\n\"messages\":[{\"role\":\"user\",\"content\":\"one\u2028two\u2029three\"}]}"
	upstream := fakeUpstream(t, answer(chatResponse))
	rec := startRecorder(t, upstream.URL)
	post(t, rec.url(chatPath), request, nil)
	rec.stop()
	calls, err := adapters.ReadTraces(rec.file(), adapters.ReadTracesOptions{OnSkip: func(int, string) { t.Error("a line was skipped") }})
	if err != nil || len(calls) != 1 {
		t.Fatalf("ReadTraces: %d calls, %v", len(calls), err)
	}
	prepared := cases.PrepareCase(calls[0], cases.PrepareCaseOptions{})
	if len(prepared.Messages) != 1 || prepared.Messages[0].Content != "one\u2028two\u2029three" {
		t.Errorf("messages: %+q", prepared.Messages)
	}
}

func TestFileModeOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no owner-only file mode")
	}
	upstream := fakeUpstream(t, answer(chatResponse))
	dir := filepath.Join(t.TempDir(), "new", "recordings")
	rec := startRecorder(t, upstream.URL, func(o *Options) { o.OutDir = dir })
	post(t, rec.url(chatPath), chatRequest, nil)
	rec.stop()
	for path, want := range map[string]os.FileMode{rec.file(): 0o600, dir: 0o700, filepath.Dir(dir): 0o700} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s has mode %o, want %o", filepath.Base(path), got, want)
		}
	}
}

func TestStopWaitsForCallsInFlight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-release
		answer(chatResponse)(w, r)
	})
	rec := startRecorder(t, upstream.URL)
	answered := make(chan string, 1)
	go func() {
		response, err := client.Post(rec.url(chatPath), "application/json", strings.NewReader(chatRequest))
		if err != nil {
			answered <- err.Error()
			return
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		answered <- string(body)
	}()
	<-started
	rec.cancel()
	select {
	case <-rec.done:
		t.Fatal("the recorder stopped while a call was in flight")
	case <-time.After(200 * time.Millisecond):
	}
	// It no longer accepts requests while it waits.
	if connection, err := net.DialTimeout("tcp", rec.addr, time.Second); err == nil {
		connection.Close()
		t.Error("the recorder still accepts connections after the stop")
	}
	close(release)
	if body := <-answered; body != chatResponse {
		t.Errorf("the call in flight got %q", body)
	}
	if summary := rec.stop(); summary.Recorded != 1 || len(rec.lines()) != 1 {
		t.Errorf("summary: %+v", summary)
	}
}

func TestStopCutsOpenStreamAfterGrace(t *testing.T) {
	stream := fixture(t, "chat.stream.sse")
	started := make(chan struct{})
	quit := make(chan struct{})
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write(stream[:len(stream)/2])
		w.(http.Flusher).Flush()
		close(started)
		select { // a stream that never finishes on its own
		case <-r.Context().Done():
		case <-quit:
		}
	})
	t.Cleanup(func() { close(quit) })
	rec := startRecorder(t, upstream.URL, func(o *Options) { o.Grace = 100 * time.Millisecond })
	cut := make(chan error, 1)
	go func() {
		response, err := client.Post(rec.url(chatPath), "application/json", strings.NewReader(chatRequest))
		if err == nil {
			_, err = io.ReadAll(response.Body)
			response.Body.Close()
		}
		cut <- err
	}()
	<-started
	begin := time.Now()
	summary := rec.stop()
	if waited := time.Since(begin); waited < 100*time.Millisecond || waited > 5*time.Second {
		t.Errorf("the stop took %s, want about the grace period", waited)
	}
	if err := <-cut; err == nil {
		t.Error("the app's stream ended cleanly although it was cut")
	}
	if summary.Recorded != 0 || summary.Skipped[skipStreamCut] != 1 || rec.fileText() != "" {
		t.Errorf("summary: %+v", summary)
	}
}

func TestErrorKindNamesNoDetail(t *testing.T) {
	for want, err := range map[string]error{
		"dns lookup failed": &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "api.example.com"}},
		"timeout":           context.DeadlineExceeded,
		"request canceled":  context.Canceled,
		"connection closed": io.ErrUnexpectedEOF,
		"upstream error":    errors.New("Post \"https://api.example.com/v1?key=synthetic\": something else"),
	} {
		if got := errorKind(err); got != want {
			t.Errorf("errorKind(%v) = %q, want %q", err, got, want)
		}
	}
}
