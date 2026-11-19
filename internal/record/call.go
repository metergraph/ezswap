package record

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/metergraph/replay/internal/cases/jsval"
	"github.com/metergraph/replay/internal/report/term"
)

// maxBody is the most of one body the recorder keeps. A larger body is
// still forwarded in full; the call is just not recorded.
const maxBody = 8 << 20

// maxLabel is the longest part name or trace id kept, in characters.
const maxLabel = 64

// Outcomes: "recorded", or the reason a call was forwarded but not recorded.
const (
	outcomeRecorded = "recorded"
	skipErrorStatus = "error status"
	skipStreamCut   = "stream cut"
	skipTooLarge    = "too large"
	skipNotJSON     = "not JSON"
	skipCompressed  = "compressed"
	skipStoredState = "stored state"
	// Not a property of the call: the line could not be written to the file.
	skipWriteFailed = "write failed"
)

// shape is the API a model call was made to.
type shape int

const (
	shapeNone shape = iota
	shapeChat
	shapeResponses
	shapeAnthropic
	shapeGemini
	shapeGeminiStream
)

// classify says which API a request is a model call to, or shapeNone.
func classify(method, path string) shape {
	if method != http.MethodPost {
		return shapeNone
	}
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		return shapeChat
	case strings.HasSuffix(path, "/responses"):
		return shapeResponses
	case strings.HasSuffix(path, "/v1/messages"):
		return shapeAnthropic
	case strings.HasSuffix(path, ":generateContent"):
		return shapeGemini
	case strings.HasSuffix(path, ":streamGenerateContent"):
		return shapeGeminiStream
	}
	return shapeNone
}

func (s shape) provider() string {
	switch s {
	case shapeChat, shapeResponses:
		return "openai"
	case shapeAnthropic:
		return "anthropic"
	case shapeGemini, shapeGeminiStream:
		return "google"
	}
	return ""
}

// geminiModel is the model in a Gemini path: the segment after "models/"
// and before the colon, as in /v1beta/models/gemini-2.0-flash:generateContent.
func geminiModel(path string) string {
	start := strings.LastIndex(path, "models/")
	colon := strings.LastIndex(path, ":")
	if start < 0 || colon < start+len("models/") {
		return ""
	}
	return path[start+len("models/") : colon]
}

// cleanLabel makes a header value safe to write and print: control
// characters neutralized, one line, trimmed, at most maxLabel characters.
func cleanLabel(value string) string {
	cleaned := strings.TrimSpace(term.CleanLine(value))
	if runes := []rune(cleaned); len(runes) > maxLabel {
		cleaned = strings.TrimSpace(string(runes[:maxLabel]))
	}
	return cleaned
}

func identity(encoding string) bool {
	encoding = strings.TrimSpace(encoding)
	return encoding == "" || strings.EqualFold(encoding, "identity")
}

// buffer keeps up to maxBody bytes of a body. The request body is read by
// the transport's goroutine while the handler may already be finishing, so
// it is locked.
type buffer struct {
	mu     sync.Mutex
	data   []byte
	over   bool // the body was larger than maxBody; data was dropped
	closed bool
}

func (b *buffer) write(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.over || b.closed {
		return
	}
	if len(b.data)+len(p) > maxBody {
		b.over = true
		b.data = nil
		return
	}
	b.data = append(b.data, p...)
}

// take ends the capture and returns what was kept.
func (b *buffer) take() (data []byte, over bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	return b.data, b.over
}

// call is one model call being forwarded. Apart from the request buffer,
// its fields are touched by the handler's goroutine only: ReverseProxy
// copies the response body there.
type call struct {
	arrived time.Time
	path    string // no query string
	part    string
	trace   string
	shape   shape

	requestCompressed bool
	request           buffer

	status             int // 0 until the upstream answers
	responseCompressed bool
	response           buffer
	stream             bool
	events             *sseParser // nil for a JSON-array stream
	streamed           int        // bytes of an event stream seen so far
	streamOver         bool       // an event stream was larger than maxBody
	rebuild            assembler
	ended              bool      // the response body reached its end
	lastByte           time.Time // when the last response bytes arrived
}

func newCall(r *http.Request, kind shape) *call {
	return &call{
		arrived:           time.Now(),
		path:              r.URL.Path,
		part:              cleanLabel(r.Header.Get(partHeader)),
		trace:             cleanLabel(r.Header.Get(traceHeader)),
		shape:             kind,
		requestCompressed: !identity(r.Header.Get("Content-Encoding")),
	}
}

// begin looks at the upstream's answer and reports whether its body should
// be captured.
func (c *call) begin(resp *http.Response) bool {
	c.status = resp.StatusCode
	c.lastByte = time.Now()
	if c.status < 200 || c.status > 299 || c.requestCompressed {
		return false
	}
	if !identity(resp.Header.Get("Content-Encoding")) {
		c.responseCompressed = true
		return false
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch {
	case mediaType == "text/event-stream":
		c.stream = true
		c.rebuild = newAssembler(c.shape)
		c.events = &sseParser{emit: c.rebuild.event}
	case c.shape == shapeGeminiStream:
		// Without alt=sse Gemini streams one JSON array of responses. It is
		// buffered and read when the body ends.
		c.stream = true
		c.rebuild = newAssembler(c.shape)
	}
	return true
}

func (c *call) responseBytes(p []byte) {
	c.lastByte = time.Now()
	if c.events == nil {
		c.response.write(p)
		return
	}
	// An event stream is parsed as it passes; only its size is counted.
	c.streamed += len(p)
	if c.streamed > maxBody {
		c.streamOver = true
		return
	}
	c.events.feed(p)
}

func (c *call) responseEnded() { c.ended = true }

// finish decides what became of the call and builds its line.
func (c *call) finish() result {
	line, outcome := c.line()
	return result{
		event: Event{Path: term.CleanLine(c.path), Part: c.part, Outcome: outcome},
		line:  line,
	}
}

func (c *call) line() ([]byte, string) {
	request, requestOver := c.request.take()
	response, responseOver := c.response.take()
	switch {
	case c.status < 200 || c.status > 299:
		// Includes the upstream that never answered (the app got a 502).
		return nil, skipErrorStatus
	case c.requestCompressed || c.responseCompressed:
		return nil, skipCompressed
	case requestOver || responseOver || c.streamOver:
		return nil, skipTooLarge
	}
	if c.stream {
		if c.events == nil {
			if reason := feedArray(c.rebuild, response); reason != "" {
				return nil, reason
			}
		}
		body, reason := c.rebuild.body(c.ended)
		if reason != "" {
			return nil, reason
		}
		response = []byte(jsval.Stringify(body))
	} else if !c.ended {
		// The body stopped before its end: the upstream or the app went
		// away, or the recorder was stopped.
		return nil, skipStreamCut
	}

	var requestLine, responseLine bytes.Buffer
	if json.Compact(&requestLine, request) != nil || json.Compact(&responseLine, response) != nil {
		return nil, skipNotJSON
	}
	// Only an object has fields to read; any other JSON value is written as
	// it is and left to the reader.
	var fields map[string]json.RawMessage
	json.Unmarshal(request, &fields)
	if previous, ok := fields["previous_response_id"]; ok && string(bytes.TrimSpace(previous)) != "null" {
		// The request continues a conversation the provider stores: it
		// cannot be replayed from this call alone.
		return nil, skipStoredState
	}
	var model string
	json.Unmarshal(fields["model"], &model)
	if c.shape == shapeGemini || c.shape == shapeGeminiStream {
		if fromPath := geminiModel(c.path); fromPath != "" {
			model = fromPath
		}
	}

	last := c.lastByte
	if last.IsZero() {
		last = time.Now()
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(traceLine{
		ID:        newID(),
		TS:        c.arrived.UTC().Format(time.RFC3339),
		LatencyMs: max(last.Sub(c.arrived).Milliseconds(), 0),
		Provider:  c.shape.provider(),
		Model:     model,
		Part:      c.part,
		TraceID:   c.trace,
		Request:   requestLine.Bytes(),
		Response:  responseLine.Bytes(),
	})
	if err != nil {
		return nil, skipNotJSON
	}
	return oneLine(out.Bytes()), outcomeRecorded
}

// traceLine is one line of the trace file, keys in this order.
type traceLine struct {
	ID        string          `json:"id"`
	TS        string          `json:"ts"`
	LatencyMs int64           `json:"latency_ms"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Part      string          `json:"part,omitempty"`
	TraceID   string          `json:"trace_id,omitempty"`
	Request   json.RawMessage `json:"request"`
	Response  json.RawMessage `json:"response"`
}

// oneLine escapes the two Unicode line separators. JSON allows them raw
// inside a string, but the trace reader splits lines on them. In valid JSON
// they can only occur inside a string, where the escape means the same.
func oneLine(line []byte) []byte {
	line = bytes.ReplaceAll(line, []byte{0xE2, 0x80, 0xA8}, []byte("\\u2028"))
	return bytes.ReplaceAll(line, []byte{0xE2, 0x80, 0xA9}, []byte("\\u2029"))
}

func newID() string {
	var random [6]byte
	rand.Read(random[:]) // crypto/rand.Read does not fail
	return "rec-" + hex.EncodeToString(random[:])
}
