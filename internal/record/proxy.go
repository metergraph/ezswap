// Package record is a local recording proxy: it forwards LLM API calls to one
// upstream provider and appends one JSON line per captured call to a trace
// file the raw-pair reader (internal/adapters) can read.
//
// The recorder never writes or reports a header (other than the two cleaned
// X-Replay-* values) or a query string: some providers carry the API key
// there.
package record

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DefaultUpstream is the provider recorded when Options.Upstream is empty.
const DefaultUpstream = "https://api.openai.com"

// TraceFile is the name of the file written in Options.OutDir.
const TraceFile = "traces.jsonl"

const defaultGrace = 10 * time.Second

// The headers the app sets to label a call. They are read, cleaned and
// removed before the request goes upstream.
const (
	partHeader  = "X-Replay-Part"
	traceHeader = "X-Replay-Trace"
)

// ErrPortInUse is returned (wrapped) by Run when the port is taken.
var ErrPortInUse = errors.New("the port is already in use")

// Options configure one recorder.
type Options struct {
	// Upstream is the provider's base URL. Default "https://api.openai.com".
	// It must be https unless the host is a loopback address.
	Upstream string
	// Port to listen on, on 127.0.0.1. 0 picks a free port.
	Port int
	// OutDir is where the trace file goes: <OutDir>/traces.jsonl.
	OutDir string
	// Events, when set, is called once per finished model call, never
	// concurrently.
	Events func(Event)
	// Ready, when set, is called once the listener is bound, with
	// "127.0.0.1:PORT".
	Ready func(addr string)
	// Grace is how long a stop waits for calls in flight. Default 10s.
	Grace time.Duration
}

// Event describes one finished model call.
type Event struct {
	// The request path, without its query string.
	Path string
	// The cleaned X-Replay-Part value, or "".
	Part string
	// "recorded", or the reason the call was not recorded.
	Outcome string
}

// Summary is what a recorder did.
type Summary struct {
	File     string
	Recorded int
	// Recorded calls by part; "(unnamed)" for calls with no part header.
	ByPart map[string]int
	// Model calls not recorded, by reason.
	Skipped map[string]int
	// Requests that were not model calls: forwarded and not recorded.
	Forwarded int
}

// UnnamedPart is the Summary.ByPart key for calls with no part header.
const UnnamedPart = "(unnamed)"

// Validate checks the options without binding a port.
func Validate(o Options) error {
	if _, err := parseUpstream(o.Upstream); err != nil {
		return err
	}
	if o.Port < 0 || o.Port > 65535 {
		return errors.New("The port must be a number from 0 to 65535.")
	}
	return nil
}

// parseUpstream reads and checks the upstream URL. Its errors never repeat
// the URL, which may hold a credential.
func parseUpstream(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		raw = DefaultUpstream
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return nil, errors.New("The upstream must be a URL such as https://api.openai.com.")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("The upstream URL must not carry a user name, a query string or a fragment.")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !loopbackHost(u.Hostname()) {
			return nil, errors.New("The upstream must use https. Plain http is allowed only for a loopback host (localhost, 127.0.0.1, ::1).")
		}
	default:
		return nil, errors.New("The upstream must be an https URL.")
	}
	return u, nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func addrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	// Windows reports its own socket error, which does not match EADDRINUSE.
	text := err.Error()
	return strings.Contains(text, "address already in use") || strings.Contains(text, "Only one usage of each socket address")
}

// Run serves until ctx is done, then stops accepting requests, waits up to
// Grace for calls in flight and cuts whatever is still open. It returns the
// Summary and a nil error after such a stop.
func Run(ctx context.Context, o Options) (Summary, error) {
	if err := Validate(o); err != nil {
		return Summary{}, err
	}
	upstream, _ := parseUpstream(o.Upstream)

	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(o.Port)))
	if err != nil {
		if addrInUse(err) {
			return Summary{}, fmt.Errorf("port %d: %w", o.Port, ErrPortInUse)
		}
		return Summary{}, fmt.Errorf("could not listen on 127.0.0.1:%d: %w", o.Port, err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if loopbackHost(upstream.Hostname()) && upstream.Port() == strconv.Itoa(port) {
		listener.Close()
		return Summary{}, errors.New("the upstream is the recorder's own address, so every call would loop back into it")
	}

	out, err := openTraceFile(filepath.Join(o.OutDir, TraceFile), o.Events)
	if err != nil {
		listener.Close()
		return Summary{}, err
	}
	results := make(chan result)
	written := make(chan struct{})
	go func() {
		out.run(results)
		close(written)
	}()

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
		// The recorder asks for identity itself and passes bodies through as
		// they are; the transport must not negotiate or undo gzip.
		DisableCompression: true,
	}
	h := newHandler(upstream, port, transport, results)

	// Every request context descends from base, so cancelling it cuts the
	// upstream side of whatever is still open after the grace period.
	base, cutOpen := context.WithCancel(context.Background())
	defer cutOpen()
	server := &http.Server{
		Handler:           h,
		ErrorLog:          log.New(io.Discard, "", 0),
		BaseContext:       func(net.Listener) context.Context { return base },
		ReadHeaderTimeout: 30 * time.Second,
	}
	if o.Ready != nil {
		o.Ready(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-served:
	}

	grace := o.Grace
	if grace <= 0 {
		grace = defaultGrace
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), grace)
	server.Shutdown(waitCtx) // stops accepting; returns early when nothing is in flight
	cancel()
	cutOpen()
	server.Close()
	h.stop()
	close(results)
	<-written
	transport.CloseIdleConnections()

	summary, writeErr := out.close()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return summary, fmt.Errorf("the recorder stopped serving: %w", serveErr)
	}
	return summary, writeErr
}

type callKey struct{}

// handler answers every request: refuse what is not local, forward the
// rest, and capture the model calls.
type handler struct {
	port    int
	proxy   *httputil.ReverseProxy
	results chan<- result

	mu      sync.Mutex
	stopped bool
	active  sync.WaitGroup
}

func newHandler(upstream *url.URL, port int, transport http.RoundTripper, results chan<- result) *handler {
	h := &handler{port: port, results: results}
	h.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Upstream URL + incoming path + incoming query.
			pr.SetURL(upstream)
			// ReverseProxy removes query parameters it cannot parse; the
			// query goes upstream exactly as the app wrote it.
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.Host = upstream.Host
			pr.Out.Header.Del(partHeader)
			pr.Out.Header.Del(traceHeader)
			pr.Out.Header.Set("Accept-Encoding", "identity")
			// ReverseProxy drops these before Rewrite runs; every header
			// other than the ones above goes upstream as the app sent it.
			for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
				if values, ok := pr.In.Header[name]; ok {
					pr.Out.Header[name] = append([]string(nil), values...)
				}
			}
		},
		Transport:      transport,
		FlushInterval:  -1, // streams reach the app as they arrive
		ErrorLog:       log.New(io.Discard, "", 0),
		ModifyResponse: h.capture,
		ErrorHandler:   upstreamFailed,
	}
	return h
}

// begin counts a request in, unless the recorder has stopped.
func (h *handler) begin() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return false
	}
	h.active.Add(1)
	return true
}

// stop refuses new requests and waits for the ones in progress to return.
func (h *handler) stop() {
	h.mu.Lock()
	h.stopped = true
	h.mu.Unlock()
	h.active.Wait()
}

// local reports whether a request was addressed to this recorder by a local
// program. A browser page that reaches the port through another name (DNS
// rebinding) or sends a cross-site request carries a foreign Host or an
// Origin header.
func (h *handler) local(r *http.Request) bool {
	if _, ok := r.Header["Origin"]; ok {
		return false
	}
	port := strconv.Itoa(h.port)
	if r.Host == "127.0.0.1:"+port || strings.EqualFold(r.Host, "localhost:"+port) {
		return true
	}
	// Clients leave the port out of Host when it is 80.
	return h.port == 80 && (r.Host == "127.0.0.1" || strings.EqualFold(r.Host, "localhost"))
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.local(r) {
		http.Error(w, "This recorder only answers requests sent to 127.0.0.1 or localhost on its own port, without an Origin header.", http.StatusForbidden)
		return
	}
	if !h.begin() {
		http.Error(w, "The recorder is stopping.", http.StatusServiceUnavailable)
		return
	}
	defer h.active.Done()

	kind := classify(r.Method, r.URL.Path)
	if kind == shapeNone {
		h.results <- result{forwarded: true}
		h.proxy.ServeHTTP(w, r)
		return
	}
	c := newCall(r, kind)
	if c.requestCompressed {
		// Not captured, so not buffered either.
	} else if r.Body != nil && r.Body != http.NoBody {
		r.Body = &teeBody{body: r.Body, read: c.request.write}
	}
	// ReverseProxy aborts the handler with a panic when the response is cut
	// part way, so the call is finished in a defer.
	defer func() { h.results <- c.finish() }()
	h.proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callKey{}, c)))
}

// capture is the proxy's ModifyResponse: it tees a model call's response
// body into the call. It never changes the response.
func (h *handler) capture(resp *http.Response) error {
	c, _ := resp.Request.Context().Value(callKey{}).(*call)
	if c == nil {
		return nil
	}
	if c.begin(resp) {
		resp.Body = &teeBody{body: resp.Body, read: c.responseBytes, end: c.responseEnded}
	}
	return nil
}

// teeBody passes a body through untouched and hands every chunk read to the
// call.
type teeBody struct {
	body io.ReadCloser
	read func([]byte)
	end  func() // called once, when the body reports its end
}

func (t *teeBody) Read(p []byte) (int, error) {
	n, err := t.body.Read(p)
	if n > 0 {
		t.read(p[:n])
	}
	if err == io.EOF && t.end != nil {
		t.end()
		t.end = nil
	}
	return n, err
}

func (t *teeBody) Close() error { return t.body.Close() }

// upstreamFailed answers 502 with the kind of failure only. The error text
// itself holds the upstream URL, query string included, so it goes nowhere.
func upstreamFailed(w http.ResponseWriter, _ *http.Request, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	fmt.Fprintf(w, `{"error":{"type":"replay_record_upstream_error","message":"The recorder could not get an answer from the upstream: %s."}}`+"\n", errorKind(err))
}

// errorKind names a transport failure without any of its detail.
func errorKind(err error) string {
	var dnsErr *net.DNSError
	var netErr net.Error
	var certErr *tls.CertificateVerificationError
	var recordErr tls.RecordHeaderError
	text := err.Error()
	switch {
	case errors.Is(err, context.Canceled):
		return "request canceled"
	case errors.As(err, &dnsErr):
		return "dns lookup failed"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case errors.Is(err, syscall.ECONNREFUSED), strings.Contains(text, "connection refused"), strings.Contains(text, "actively refused"):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection closed"
	case errors.As(err, &certErr), errors.As(err, &recordErr):
		return "tls error"
	}
	return "upstream error"
}
