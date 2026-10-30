package adapters

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/metergraph/replay/internal/cases/jsval"
)

// encode is encoding/json output without HTML escaping, as run.json is written.
func encode(t *testing.T, value any) string {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func TestRequestAndToolCallsKeepSourceKeyOrder(t *testing.T) {
	line := `{"schema":"metergraph.replay-trace/v1","id":"o-1",` +
		`"request":{"params":{"top_p":0.9,"temperature":0.3},"model":"m","messages":[{"role":"user","content":"<x> & y"}],` +
		`"tools":[{"name":"f","input_schema":{"type":"object","properties":{"z":{},"a":{}}}}],"tool_choice":{"type":"any"},"raw":{"ignored":true}},` +
		`"response":{"tool_calls":[{"name":"f","input":{"z":1,"a":[{"y":2,"b":3}],"1":0}}]},"meta":{}}`
	calls := read(t, temp(t, "order.jsonl", line+"\n"))
	c := calls[0]
	wantRequest := `{"params":{"top_p":0.9,"temperature":0.3},"model":"m","messages":[{"role":"user","content":"<x> & y"}],` +
		`"tools":[{"name":"f","input_schema":{"type":"object","properties":{"z":{},"a":{}}}}],"tool_choice":{"type":"any"}}`
	equal(t, jsval.Stringify(c.Request), wantRequest, "request")
	equal(t, encode(t, c.Request), wantRequest, "request through encoding/json")
	equal(t, encode(t, c.Response.ToolCalls), `[{"name":"f","input":{"1":0,"z":1,"a":[{"y":2,"b":3}]}}]`, "tool calls")

	kase := prepare(c)
	equal(t, str(kase.Production.Output), `{"1":0,"z":1,"a":[{"y":2,"b":3}]}`, "production output")
	equal(t, string(kase.ForcedTool.Parameters), `{"type":"object","properties":{"z":{},"a":{}}}`, "forced tool parameters")
}

func TestLangfuseRequestKeepsModelParametersThenInputOrder(t *testing.T) {
	line := `{"id":"l-1","traceId":"t","type":"GENERATION","model":"gpt-4o",` +
		`"modelParameters":{"max_tokens":"10","temperature":"0.5"},` +
		`"input":{"tools":[],"messages":[{"role":"user","content":"hi"}]},"output":{"z":1,"a":2}}`
	c := read(t, temp(t, "lf.jsonl", line+"\n"))[0]
	equal(t, jsval.Stringify(c.Request), `{"model":"gpt-4o","max_tokens":10,"temperature":0.5,"tools":[],"messages":[{"role":"user","content":"hi"}]}`)
	equal(t, str(c.Response.Text), `{"z":1,"a":2}`, "a structured answer's JSON keeps its order")
}

func TestOtelRequestAndSchemaKeepSourceKeyOrder(t *testing.T) {
	span := `{"spanId":"s","attributes":{"gen_ai.request.model":"m",` +
		`"gen_ai.input.messages":"[{\"role\":\"user\",\"parts\":[{\"type\":\"text\",\"content\":\"q\"}]}]",` +
		`"gen_ai.request.structured_output_schema":"{\"type\":\"object\",\"properties\":{\"z\":{},\"a\":{}}}"}}`
	c := read(t, temp(t, "span.jsonl", span+"\n"))[0]
	equal(t, string(prepare(c).ResponseFormat), `{"type":"json_schema","json_schema":{"name":"output","schema":{"type":"object","properties":{"z":{},"a":{}}}}}`)
	if !strings.HasPrefix(jsval.Stringify(c.Request), `{"model":"m","messages":[{"role":"user","parts":[{"type":"text","content":"q"}]}],"params":{`) {
		t.Errorf("request %s", jsval.Stringify(c.Request))
	}
}
