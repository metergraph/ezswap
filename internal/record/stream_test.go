package record

import (
	"bufio"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/metergraph/ezswap/internal/adapters"
	"github.com/metergraph/ezswap/internal/cases"
)

// streamCase is one provider's streaming call.
type streamCase struct {
	path        string
	contentType string
	request     string
}

var (
	chatStream      = streamCase{chatPath, "text/event-stream", `{"model":"gpt-4o-mini","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"Where is order A-1001 for ada@example.com?"}]}`}
	responsesStream = streamCase{"/v1/responses", "text/event-stream; charset=utf-8", `{"model":"gpt-5-mini","stream":true,"input":"What is the capital of France?"}`}
	anthropicStream = streamCase{"/v1/messages", "text/event-stream", `{"model":"claude-haiku-4-5","max_tokens":200,"stream":true,"messages":[{"role":"user","content":"What is the weather in Paris?"}]}`}
	geminiSSE       = streamCase{"/v1beta/models/gemini-2.0-flash:streamGenerateContent", "text/event-stream", `{"contents":[{"role":"user","parts":[{"text":"What is the capital of France?"}]}]}`}
	geminiArray     = streamCase{"/v1beta/models/gemini-2.0-flash:streamGenerateContent", "application/json; charset=UTF-8", geminiSSE.request}
)

// recordStream plays a fixture through a recorder as a stream, a few bytes
// at a time so that events arrive split across reads, and returns the
// stopped recorder.
func recordStream(t *testing.T, c streamCase, fixtureName string) *recorder {
	t.Helper()
	stream := fixture(t, fixtureName)
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", c.contentType)
		for start := 0; start < len(stream); start += 7 {
			w.Write(stream[start:min(start+7, len(stream))])
			w.(http.Flusher).Flush()
		}
	})
	rec := startRecorder(t, upstream.URL)
	query := ""
	if c == geminiSSE {
		query = "?alt=sse"
	}
	response, body := post(t, rec.url(c.path+query), c.request, map[string]string{"X-Ezswap-Part": "streamed"})
	if response.StatusCode != http.StatusOK || string(body) != string(stream) {
		t.Errorf("the app did not get the stream as sent: %d, %d bytes of %d", response.StatusCode, len(body), len(stream))
	}
	if got := response.Header.Get("Content-Type"); got != c.contentType {
		t.Errorf("Content-Type %q, want %q", got, c.contentType)
	}
	rec.stop()
	return rec
}

// rebuilt checks that a complete stream was recorded as the body in the
// expected fixture, and returns that body as the trace reader reads it.
func rebuilt(t *testing.T, c streamCase, streamFixture, expectedFixture string) adapters.ParsedResponse {
	t.Helper()
	rec := recordStream(t, c, streamFixture)
	lines := rec.lines()
	if len(lines) != 1 || rec.summary.Recorded != 1 || rec.summary.ByPart["streamed"] != 1 {
		t.Fatalf("%d lines, summary %+v", len(lines), rec.summary)
	}
	if got, want := string(lines[0].Response), compact(t, fixture(t, expectedFixture)); got != want {
		t.Errorf("rebuilt body:\n%s\nwant:\n%s", got, want)
	}
	if string(lines[0].Request) != c.request {
		t.Errorf("request: %s", lines[0].Request)
	}
	// The whole file, the way a run reads it.
	calls, err := adapters.ReadTraces(rec.file(), adapters.ReadTracesOptions{})
	if err != nil || len(calls) != 1 {
		t.Fatalf("ReadTraces: %d calls, %v", len(calls), err)
	}
	if prepared := cases.PrepareCase(calls[0], cases.PrepareCaseOptions{}); prepared.NotReplayable != nil {
		t.Errorf("not replayable: %s", *prepared.NotReplayable)
	}
	return adapters.ParseResponseBody(string(lines[0].Response))
}

// cut checks that a stream which stops before its final event writes no
// line.
func cut(t *testing.T, c streamCase, fixtureName string) {
	t.Helper()
	rec := recordStream(t, c, fixtureName)
	if rec.summary.Recorded != 0 || rec.summary.Skipped[skipStreamCut] != 1 || rec.fileText() != "" {
		t.Errorf("summary %+v, file %q", rec.summary, rec.fileText())
	}
	if events := rec.seen(); len(events) != 1 || events[0] != (Event{Path: c.path, Part: "streamed", Outcome: "stream cut"}) {
		t.Errorf("events: %+v", events)
	}
}

func usage(t *testing.T, parsed adapters.ParsedResponse, input, output float64) {
	t.Helper()
	if parsed.Usage == nil || parsed.Usage.InputTokens == nil || parsed.Usage.OutputTokens == nil ||
		*parsed.Usage.InputTokens != input || *parsed.Usage.OutputTokens != output {
		t.Errorf("usage: %+v, want %v in and %v out", parsed.Usage, input, output)
	}
}

func parsedText(parsed adapters.ParsedResponse) string {
	if parsed.Text == nil {
		return "<nil>"
	}
	return *parsed.Text
}

func finish(parsed adapters.ParsedResponse) string {
	if parsed.FinishReason == nil {
		return "<nil>"
	}
	return *parsed.FinishReason
}

func TestStreamRebuiltChatCompletions(t *testing.T) {
	parsed := rebuilt(t, chatStream, "chat.stream.sse", "chat.expected.json")
	if parsed.Shape != "openai_chat" || parsedText(parsed) != "Checking the order for ada@example.com." || finish(parsed) != "tool_calls" || len(parsed.ToolCalls) != 1 {
		t.Errorf("parsed: %+v", parsed)
	}
	if output := cases.SingleToolCallOutput(parsed.ToolCalls); output == nil || *output != `{"order_id":"A-1001"}` {
		t.Errorf("tool call arguments: %v", output)
	}
	usage(t, parsed, 21, 17)
}

func TestStreamCutChatCompletions(t *testing.T) {
	cut(t, chatStream, "chat.cut.sse")
}

func TestStreamRebuiltResponses(t *testing.T) {
	parsed := rebuilt(t, responsesStream, "responses.stream.sse", "responses.expected.json")
	if parsed.Shape != "openai_responses" || parsedText(parsed) != "The capital of France is Paris." || finish(parsed) != "completed" {
		t.Errorf("parsed: %+v", parsed)
	}
	usage(t, parsed, 14, 8)
}

func TestStreamCutResponses(t *testing.T) {
	cut(t, responsesStream, "responses.cut.sse")
}

func TestStreamRebuiltAnthropicMessages(t *testing.T) {
	parsed := rebuilt(t, anthropicStream, "anthropic.stream.sse", "anthropic.expected.json")
	if parsed.Shape != "anthropic" || parsedText(parsed) != "Let me look up the weather in Paris." || finish(parsed) != "tool_use" || len(parsed.ToolCalls) != 1 {
		t.Errorf("parsed: %+v", parsed)
	}
	if output := cases.SingleToolCallOutput(parsed.ToolCalls); output == nil || *output != `{"city":"Paris","unit":"celsius"}` {
		t.Errorf("tool call input: %v", output)
	}
	usage(t, parsed, 25, 42)
}

func TestStreamCutAnthropicMessages(t *testing.T) {
	cut(t, anthropicStream, "anthropic.cut.sse")
}

func TestStreamRebuiltGemini(t *testing.T) {
	for name, source := range map[string]struct {
		c       streamCase
		fixture string
	}{
		"SSE":        {geminiSSE, "gemini.stream.sse"},
		"JSON array": {geminiArray, "gemini.stream.json"},
	} {
		t.Run(name, func(t *testing.T) {
			parsed := rebuilt(t, source.c, source.fixture, "gemini.expected.json")
			if parsed.Shape != "gemini" || parsedText(parsed) != "The capital of France is Paris.\n Done." || finish(parsed) != "STOP" || len(parsed.ToolCalls) != 1 {
				t.Errorf("parsed: %+v", parsed)
			}
			usage(t, parsed, 9, 15)
		})
	}
}

func TestStreamCutGemini(t *testing.T) {
	t.Run("SSE", func(t *testing.T) { cut(t, geminiSSE, "gemini.cut.sse") })
	t.Run("JSON array", func(t *testing.T) { cut(t, geminiArray, "gemini.cut.json") })
}

func TestStreamReachesClientInChunks(t *testing.T) {
	stream := string(fixture(t, "chat.stream.sse"))
	first, rest, _ := strings.Cut(stream, "\n\n")
	first += "\n\n"
	release := make(chan struct{})
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, first)
		w.(http.Flusher).Flush()
		select { // the rest is held back until the app has the first event
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, rest)
	})
	rec := startRecorder(t, upstream.URL)
	// The client's 30 second timeout fails the test if the first event is
	// held until the stream ends.
	response, err := client.Post(rec.url(chatPath), "application/json", strings.NewReader(chatStream.request))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	got := ""
	for !strings.HasSuffix(got, "\n\n") {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("reading the first event: %v", err)
		}
		got += line
	}
	if got != first {
		t.Fatalf("first event %q, want %q", got, first)
	}
	close(release)
	remainder, err := io.ReadAll(reader)
	if err != nil || string(remainder) != rest {
		t.Errorf("the rest of the stream: %v, %q", err, remainder)
	}
	if summary := rec.stop(); summary.Recorded != 1 {
		t.Errorf("summary: %+v", summary)
	}
}

func TestStreamEventThatIsNotJSON(t *testing.T) {
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: <html>\n\ndata: [DONE]\n\n")
	})
	rec := startRecorder(t, upstream.URL)
	post(t, rec.url(chatPath), chatStream.request, nil)
	if summary := rec.stop(); summary.Skipped[skipNotJSON] != 1 || rec.fileText() != "" {
		t.Errorf("summary: %+v", summary)
	}
}

// A stream event of nothing but opening brackets must be refused, not parsed:
// the parser recurses, and deep enough nesting would overflow the stack.
func TestDeeplyNestedEventIsRefused(t *testing.T) {
	if _, err := parseBounded(strings.Repeat("[", 2_000_000)); err == nil {
		t.Fatal("unterminated nesting parsed")
	}
	if _, err := parseBounded(strings.Repeat("[", 200_000) + strings.Repeat("]", 200_000)); err == nil {
		t.Fatal("nesting 200000 deep parsed")
	}
	if _, err := parseBounded(`{"a":[1,{"b":2}]}`); err != nil {
		t.Fatal(err)
	}
}

// An upstream may start its answer before it has read the whole request.
// The proxy must keep sending the request body and keep streaming the
// answer: an HTTP/1 server that closed the request body once the answer
// began would fail the upload and cut the stream.
func TestStreamSurvivesAnswerBeforeRequestIsRead(t *testing.T) {
	stream := fixture(t, "chat.stream.sse")
	first, rest, _ := strings.Cut(string(stream), "\n\n")
	first += "\n\n"
	upstream := fakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		http.NewResponseController(w).EnableFullDuplex()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, first)
		w.(http.Flusher).Flush()
		io.Copy(io.Discard, r.Body) // the upload finishes only now
		io.WriteString(w, rest)
	})
	rec := startRecorder(t, upstream.URL)
	// A request large enough that it is still being sent when the answer starts.
	padding := strings.Repeat("x", 4<<20)
	request := `{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"` + padding + `"}]}`
	response, body := post(t, rec.url(chatPath), request, map[string]string{"X-Ezswap-Part": "streamed"})
	if response.StatusCode != http.StatusOK || string(body) != string(stream) {
		t.Fatalf("the app got %d and %d bytes of %d", response.StatusCode, len(body), len(stream))
	}
	if summary := rec.stop(); summary.Recorded != 1 {
		t.Errorf("summary %+v", summary)
	}
}
