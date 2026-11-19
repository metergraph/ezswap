package adapters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func partsOf(t *testing.T, text, partKey string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "traces.jsonl")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	calls, err := ReadTraces(path, ReadTracesOptions{PartKey: partKey})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(calls))
	for i, call := range calls {
		out[i] = "<nil>"
		if call.Part != nil {
			out[i] = *call.Part
		}
	}
	return out
}

func expect(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("parts %q, want %q", got, want)
	}
}

const rawPair = `"request":{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hi"}]},"response":{"choices":[{"message":{"role":"assistant","content":"Hello"}}]}`

func TestPartReadFromReplayJSONL(t *testing.T) {
	record := `{"schema":"metergraph.replay-trace/v1","id":"a","part":"classify","request":{"model":"m","messages":[]},"response":{"text":"x"},"meta":{"feature":"from-meta"}}`
	expect(t, partsOf(t, record+"\n", ""), "classify")
	expect(t, partsOf(t, record+"\n", "feature"), "from-meta")
	expect(t, partsOf(t, strings.Replace(record, `"part":"classify",`, "", 1)+"\n", ""), "<nil>")
}

func TestPartReadFromRawTopLevel(t *testing.T) {
	expect(t, partsOf(t, `{"id":"a","part":"extract-order",`+rawPair+"}\n", ""), "extract-order")
	expect(t, partsOf(t, `{"id":"a",`+rawPair+"}\n", ""), "<nil>")
	expect(t, partsOf(t, `{"id":"a","part":7,`+rawPair+"}\n", ""), "<nil>")
}

func TestPartKeyRawTopLevel(t *testing.T) {
	expect(t, partsOf(t, `{"id":"a","feature":"route","part":"other",`+rawPair+"}\n", "feature"), "route")
	expect(t, partsOf(t, `{"id":"a","part":"other",`+rawPair+"}\n", "feature"), "other")
}

func TestPartNameCleanedAndCut(t *testing.T) {
	long := strings.Repeat("x", 80)
	got := partsOf(t, `{"id":"a","part":"  extract\u001b[31m\norder  ",`+rawPair+"}\n"+`{"id":"b","part":"`+long+`",`+rawPair+"}\n"+`{"id":"c","part":"   ",`+rawPair+"}\n", "")
	if strings.ContainsAny(got[0], "\x1b\n") || !strings.HasPrefix(got[0], "extract") || !strings.HasSuffix(got[0], "order") {
		t.Fatalf("control characters kept: %q", got[0])
	}
	if len(got[1]) != MaxPartName {
		t.Fatalf("cut to %d: %d", MaxPartName, len(got[1]))
	}
	if got[2] != "<nil>" {
		t.Fatalf("a blank name is no name: %q", got[2])
	}
}

func TestPartKeyHeliconeProperty(t *testing.T) {
	record := `{"request_id":"h1","response_status":200,"model":"gpt-4o-mini","request_body":{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hi"}]},"response_body":{"choices":[{"message":{"role":"assistant","content":"Hello"}}]},"request_properties":{"Helicone-Property-Feature":"summarise"}}`
	expect(t, partsOf(t, record+"\n", ""), "<nil>")
	expect(t, partsOf(t, record+"\n", "Helicone-Property-Feature"), "summarise")
	expect(t, partsOf(t, strings.Replace(record, "request_properties", "properties", 1)+"\n", "Helicone-Property-Feature"), "summarise")
}

func TestPartKeyLiteLLMMetadata(t *testing.T) {
	record := `{"id":"l1","call_type":"acompletion","custom_llm_provider":"openai","model":"gpt-4o-mini","model_parameters":{},"messages":[{"role":"user","content":"Hi"}],"response":{"choices":[{"message":{"role":"assistant","content":"Hello"}}]},"metadata":{"feature":"translate"}}`
	expect(t, partsOf(t, record+"\n", ""), "<nil>")
	expect(t, partsOf(t, record+"\n", "feature"), "translate")
}

const span = `{"traceId":"t1","spanId":"s1","name":"chat gpt-4o-mini","attributes":[{"key":"gen_ai.operation.name","value":{"stringValue":"chat"}},{"key":"gen_ai.request.model","value":{"stringValue":"gpt-4o-mini"}},{"key":"app.feature","value":{"stringValue":"draft-reply"}},{"key":"gen_ai.input.messages","value":{"stringValue":"[{\"role\":\"user\",\"parts\":[{\"type\":\"text\",\"content\":\"Hi\"}]}]"}}]}`

func TestSpanNameIgnoredWithoutPartKey(t *testing.T) {
	expect(t, partsOf(t, span+"\n", ""), "<nil>")
}

func TestPartKeySpanNameAndAttribute(t *testing.T) {
	expect(t, partsOf(t, span+"\n", "span.name"), "chat gpt-4o-mini")
	expect(t, partsOf(t, span+"\n", "app.feature"), "draft-reply")
	expect(t, partsOf(t, span+"\n", "missing"), "<nil>")
}

func TestPartKeyLangfuseMetadata(t *testing.T) {
	record := `{"id":"lf1","traceId":"t1","type":"GENERATION","name":"OpenAI-generation","model":"gpt-4o-mini","input":[{"role":"user","content":"Hi"}],"output":"Hello","metadata":{"feature":"classify"}}`
	expect(t, partsOf(t, record+"\n", ""), "<nil>")
	expect(t, partsOf(t, record+"\n", "feature"), "classify")
	expect(t, partsOf(t, record+"\n", "name"), "OpenAI-generation")
	expect(t, partsOf(t, strings.Replace(record, `"metadata":{"feature":"classify"}`, `"metadata":"{\"feature\":\"from-string\"}"`, 1)+"\n", "feature"), "from-string")
}
