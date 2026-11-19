package judge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeJudge answers in the gateway's published shape. It says the replies
// differ when Reply 1 and Reply 2 hold different numbers, and leans towards
// "different" when the word "lean" comes first, to stand for position bias.
func fakeJudge(t *testing.T, status int) (*httptest.Server, *atomic.Int32, *atomic.Value) {
	t.Helper()
	var calls atomic.Int32
	var last atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		last.Store(string(body) + "\nauth=" + r.Header.Get("Authorization"))
		if r.URL.Path != "/decisions" {
			http.NotFound(w, r)
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			io.WriteString(w, `{"error":{"message":"Authentication failed","type":"authentication_error"}}`)
			return
		}
		var request struct {
			Input string `json:"input"`
		}
		json.Unmarshal(body, &request)
		one := between(request.Input, "Reply 1:\n", "\n\nReply 2:")
		two := request.Input[strings.Index(request.Input, "Reply 2:\n")+len("Reply 2:\n"):]
		p := 0.1
		if digits(one) != digits(two) {
			p = 0.9
		}
		if strings.HasPrefix(one, "lean") {
			p = 0.6
		}
		io.WriteString(w, `{"answers":[{"type":"choice","name":"same","choice":"L1","confidence":0.8,"probabilities":[{"value":"L1","probability":`+
			ftoa(1-p)+`},{"value":"L2","probability":`+ftoa(p)+`}]}],"model":"typesafe-ai/jev","provider_metadata":{"gateway":{"cost":"0.00002"}},"usage":{"input_tokens":300,"output_tokens":20}}`)
	}))
	t.Cleanup(server.Close)
	return server, &calls, &last
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	j := strings.Index(s, b)
	if i < 0 || j < i {
		return ""
	}
	return s[i+len(a) : j]
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func ftoa(f float64) string {
	data, _ := json.Marshal(f)
	return string(data)
}

func TestJudgeAsksBothOrders(t *testing.T) {
	server, calls, last := fakeJudge(t, http.StatusOK)
	pairs := []Pair{
		{Request: "When does order 1 ship?", A: "It ships on the 3rd.", B: "Your order goes out on the 3rd."},
		{Request: "When does order 1 ship?", A: "It ships on the 3rd.", B: "It ships on the 9th."},
		{Request: "When does order 1 ship?", A: "lean: ships on the 3rd.", B: "Ships on the 3rd."},
	}
	verdicts := Judge(context.Background(), pairs, Options{BaseURL: server.URL, APIKey: "test-key"})
	if calls.Load() != 6 {
		t.Fatalf("%d calls, want two per pair", calls.Load())
	}
	if v := verdicts[0]; v.Err != nil || v.Different() || v.Unsure {
		t.Errorf("same pair: %+v", v)
	}
	if v := verdicts[1]; v.Err != nil || !v.Different() || v.Unsure || v.CostUSD != 0.00004 {
		t.Errorf("different pair: %+v", v)
	}
	// 0.6 one way and 0.1 the other: the orders disagree, the mean is same.
	if v := verdicts[2]; !v.Unsure || v.Different() {
		t.Errorf("leaning pair: %+v", v)
	}
	body := last.Load().(string)
	for _, want := range []string{`"model":"typesafe-ai/jev"`, `"value":"L1"`, `"value":"L2"`, `"zeroDataRetention":true`, "auth=Bearer test-key"} {
		if !strings.Contains(body, want) {
			t.Errorf("request lacks %s", want)
		}
	}
	// The option names carry no meaning of their own.
	if strings.Contains(body, `"value":"same"`) || strings.Contains(body, `"value":"different"`) {
		t.Error("the choices are named for what they mean")
	}
}

func TestEmptyReplyIsDifferentWithoutAsking(t *testing.T) {
	server, calls, _ := fakeJudge(t, http.StatusOK)
	verdicts := Judge(context.Background(), []Pair{{Request: "q", A: "An answer.", B: "  "}, {Request: "q", A: "", B: ""}}, Options{BaseURL: server.URL})
	if calls.Load() != 0 || !verdicts[0].Different() || verdicts[1].Different() {
		t.Errorf("%d calls, %+v", calls.Load(), verdicts)
	}
}

func TestJudgeFailureIsPerPair(t *testing.T) {
	server, calls, _ := fakeJudge(t, http.StatusUnauthorized)
	verdicts := Judge(context.Background(), []Pair{{Request: "q", A: "a", B: "b"}}, Options{BaseURL: server.URL, APIKey: "secret-key"})
	err := verdicts[0].Err
	if err == nil || !strings.Contains(err.Error(), "HTTP 401: Authentication failed") {
		t.Fatalf("error %v", err)
	}
	if strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("the error names the key or the address: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("a 401 was retried: %d calls", calls.Load())
	}
}

func TestLongTextsAreCut(t *testing.T) {
	long := strings.Repeat("é", maxText+50)
	got := cut(long)
	if !strings.HasSuffix(got, " [cut]") || len([]rune(got)) != maxText+len(" [cut]") {
		t.Errorf("cut to %d characters", len([]rune(got)))
	}
	if cut("short") != "short" {
		t.Error("a short text was changed")
	}
}
