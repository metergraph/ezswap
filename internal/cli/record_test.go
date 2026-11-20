package cli

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// recordSession starts `record` against a fake upstream and returns the
// recorder's address, a stop function that waits for the exit code, and the
// captured output.
func recordSession(t *testing.T, extra ...string) (address string, stop func() int, stdout *lockedBuffer, out string) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			io.WriteString(w, chatReply(`{"category":"bug"}`, nil))
			return
		}
		io.WriteString(w, `{"data":[]}`)
	}))
	t.Cleanup(upstream.Close)
	out = t.TempDir()
	stdout = &lockedBuffer{}
	stderr := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- Main(append([]string{"record", "--upstream", upstream.URL, "--port", "0", "--out", out}, extra...),
			IO{Stdout: stdout, Stderr: stderr, Signal: ctx, Cwd: out})
	}()
	pattern := regexp.MustCompile(`http://(127\.0\.0\.1:\d+)/v1`)
	deadline := time.Now().Add(5 * time.Second)
	for address == "" && time.Now().Before(deadline) {
		if m := pattern.FindStringSubmatch(stdout.String() + stderr.String()); m != nil {
			address = m[1]
			break
		}
		select {
		case code := <-done:
			t.Fatalf("record exited early with %d: %s", code, stdout.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if address == "" {
		t.Fatal("record did not say where it listens")
	}
	stop = func() int {
		cancel()
		select {
		case code := <-done:
			return code
		case <-time.After(15 * time.Second):
			t.Fatal("record did not stop")
			return -1
		}
	}
	return address, stop, stdout, out
}

func post(t *testing.T, url, part, body string) {
	t.Helper()
	request, _ := http.NewRequest("POST", url, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+otherKey)
	if part != "" {
		request.Header.Set("X-Ezswap-Part", part)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("status %d", response.StatusCode)
	}
}

func TestRecordWritesATraceFileAMapCanRead(t *testing.T) {
	address, stop, stdout, out := recordSession(t)
	body := func(n int) string {
		return `{"model":"gpt-4.1-mini","messages":[{"role":"system","content":"Label the ticket as JSON."},{"role":"user","content":"Ticket ` + strconv.Itoa(n) + `"}],"response_format":{"type":"json_object"}}`
	}
	for n := 0; n < 10; n++ {
		post(t, "http://"+address+"/v1/chat/completions?api_key="+otherKey, "classify", body(n))
	}
	post(t, "http://"+address+"/v1/chat/completions", "", body(99))
	response, err := http.Get("http://" + address + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if code := stop(); code != ExitOK {
		t.Fatalf("exit %d: %s", code, stdout.String())
	}
	text := stdout.String()
	match(t, `Recording model calls to ezswap-out|Recording model calls to \S*traces\.jsonl`, text)
	match(t, `recorded +classify +/v1/chat/completions`, text)
	match(t, `recorded +\(unnamed\) +/v1/chat/completions`, text)
	match(t, `Recorded 11 calls to \S*traces\.jsonl\.`, text)
	match(t, `classify +10`, text)
	match(t, `The file now holds 11 cases a replay can reproduce\.`, text)
	match(t, `ezswap estimate \S*traces\.jsonl --map --models <list>`, text)
	file := filepath.Join(out, "traces.jsonl")
	written, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, otherKey) || strings.Contains(string(written), otherKey) || strings.Contains(text, "api_key") {
		t.Fatal("the key or the query string leaked")
	}
	if strings.Contains(text, "—") {
		t.Fatal("an em dash in the output")
	}

	// The recorded file goes straight into a map estimate, parts named by the header.
	estimate := cli(t, args("estimate", file, "--map", models), nil, nil)
	if estimate.code != ExitOK {
		t.Fatalf("%d %s", estimate.code, estimate.stderr)
	}
	match(t, `classify +name +10 +1 field`, estimate.stdout)
	match(t, `part-[0-9a-f]{6} +fingerprint +1 +no: under 10 cases`, estimate.stdout)
}

func TestRecordJSONPrintsOneObject(t *testing.T) {
	address, stop, stdout, _ := recordSession(t, "--json")
	post(t, "http://"+address+"/v1/chat/completions", "classify", `{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"Hi"}]}`)
	if code := stop(); code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	summary := decode(t, stdout.String())
	if summary["command"] != "record" || summary["recorded"] != 1.0 || summary["replayable"] != 1.0 ||
		summary["byPart"].(map[string]any)["classify"] != 1.0 {
		t.Fatalf("%v", summary)
	}
}

func TestRecordPortInUseAndUpstreamRules(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	busy := cli(t, args("record", "--port", port, "--out", t.TempDir(), "--json"), nil, nil)
	if busy.code != ExitError || errorCode(t, busy.stdout) != "record_failed" {
		t.Fatalf("%d %s", busy.code, busy.stdout)
	}
	text := cli(t, args("record", "--port", port, "--out", t.TempDir()), nil, nil)
	match(t, `Port `+port+` is already in use\. Pick another with --port`, text.stderr)
	plain := cli(t, args("record", "--upstream", "http://example.com", "--out", t.TempDir(), "--json"), nil, nil)
	if plain.code != ExitError || errorCode(t, plain.stdout) != "record_failed" {
		t.Fatalf("a plain http upstream that is not this machine: %d %s", plain.code, plain.stdout)
	}
	if bad := cli(t, args("record", "--port", "70000"), nil, nil); bad.code != ExitUsage {
		t.Fatalf("--port out of range: %d", bad.code)
	}
	if file := cli(t, args("record", "traces.jsonl"), nil, nil); file.code != ExitUsage {
		t.Fatalf("record takes no file: %d", file.code)
	}
}

func TestRecordFreshSetsThePreviousRecordingAside(t *testing.T) {
	dir := t.TempDir()
	body := `{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"Ticket 1"}]}`
	session := func(extra ...string) (string, int) {
		address, stop, stdout, _ := recordSession(t, append([]string{"--out", dir}, extra...)...)
		post(t, "http://"+address+"/v1/chat/completions", "classify", body)
		code := stop()
		return stdout.String(), code
	}
	lines := func() int {
		data, err := os.ReadFile(filepath.Join(dir, "traces.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(data), "\n")
	}
	if _, code := session(); code != ExitOK || lines() != 1 {
		t.Fatalf("first session: %d, %d lines", code, lines())
	}
	// Without --fresh the file grows.
	if _, code := session(); code != ExitOK || lines() != 2 {
		t.Fatalf("second session: %d, %d lines", code, lines())
	}
	text, code := session("--fresh")
	if code != ExitOK || lines() != 1 {
		t.Fatalf("fresh session: %d, %d lines", code, lines())
	}
	match(t, `Started a fresh recording\. The previous one is now \S*traces-\d{8}-\d{6}\.jsonl\.`, text)
	aside, _ := filepath.Glob(filepath.Join(dir, "traces-*.jsonl"))
	if len(aside) != 1 {
		t.Fatalf("set aside: %v", aside)
	}
	if data, _ := os.ReadFile(aside[0]); strings.Count(string(data), "\n") != 2 {
		t.Errorf("the previous recording was not kept whole: %q", data)
	}
	// --fresh is for record only.
	if out := cli(t, args("estimate", filepath.Join(dir, "traces.jsonl"), models, "--fresh"), nil, nil); out.code != ExitUsage || !strings.Contains(out.stderr, "--fresh is for record") {
		t.Errorf("estimate --fresh: %d %s", out.code, out.stderr)
	}
}
