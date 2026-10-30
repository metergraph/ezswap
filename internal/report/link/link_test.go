package link

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/metergraph/replay/internal/types"
)

// A synthetic run: 24 support-ticket calls at example.com, two treatments,
// a noise floor (the TypeScript tests' fixture).
func fixture(t *testing.T) *types.RunFile {
	t.Helper()
	raw, err := os.ReadFile("../../../test/fixtures/report-run.json")
	if err != nil {
		t.Fatal(err)
	}
	var run types.RunFile
	if err := json.Unmarshal(raw, &run); err != nil {
		t.Fatal(err)
	}
	return &run
}

const hostile = `</script><script>alert("x")</script><img src=x onerror="alert('y')"> &amp; ' " <!-- ` + " "

// Ported from test/report.test.ts: hostile strings in the places a run can
// carry them.
func hostileRun(t *testing.T) *types.RunFile {
	run := fixture(t)
	run.Input.File = "traces</title><script>alert(1)</script>.jsonl"
	run.Catalog.Version = hostile
	run.Treatments[0].Label = `Mini"><script>alert(2)</script>`
	h := hostile
	run.Cases[0].InputPreview = &h
	run.Cases[0].Production.Output = &h
	run.Treatments[0].Model = "openai/<b>mini</b>"
	return run
}

func TestTheHostedLinkCarriesUTMParametersAndOnlyTheSetupInTheFragment(t *testing.T) {
	run := hostileRun(t)
	run.Treatments[1].Endpoint = "https://gateway.example.com/v1"
	link := HostedLink(run, MediumCLI)
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != "https://www.metergraph.dev/replay" {
		t.Errorf("origin and path: %s", got)
	}
	var keys []string
	for _, kv := range strings.Split(u.RawQuery, "&") {
		k, _, _ := strings.Cut(kv, "=")
		keys = append(keys, k)
	}
	if !slices.Equal(keys, []string{"utm_source", "utm_medium", "utm_campaign"}) {
		t.Errorf("query keys: %v", keys)
	}
	q := u.Query()
	if q.Get("utm_source") != "replay" || q.Get("utm_medium") != "cli" || q.Get("utm_campaign") != run.Tool.Version {
		t.Errorf("query: %v", q)
	}
	hash := "#" + u.EscapedFragment()
	if !regexp.MustCompile(`^#setup=[A-Za-z0-9_-]+$`).MatchString(hash) {
		t.Errorf("fragment: %s", hash)
	}
	setup, err := DecodeSetup(hash)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(hash, "#setup="))
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	var top []string
	for k := range generic {
		top = append(top, k)
	}
	slices.Sort(top)
	if !slices.Equal(top, []string{"sample", "trackedFields", "treatments", "v"}) {
		t.Errorf("keys: %v", top)
	}
	for _, tr := range generic["treatments"].([]any) {
		var tk []string
		for k := range tr.(map[string]any) {
			tk = append(tk, k)
		}
		slices.Sort(tk)
		if !slices.Equal(tk, []string{"model", "settings"}) {
			t.Errorf("treatment keys: %v", tk)
		}
	}
	if setup.V != 1 || setup.Sample != run.Settings.Sample || !slices.Equal(setup.TrackedFields, run.Settings.TrackedFields) {
		t.Errorf("setup: %+v", setup)
	}
	var want []HostedTreatment
	for _, tr := range run.Treatments {
		want = append(want, HostedTreatment{Model: tr.Model, Settings: HostedSettings{tr.Settings.Temperature, tr.Settings.MaxTokens, tr.Settings.ReasoningEffort}})
	}
	if !reflect.DeepEqual(setup.Treatments, want) {
		t.Errorf("treatments: %+v", setup.Treatments)
	}
	decoded := string(raw)
	for _, leak := range []string{"gateway.example.com", "example.com", "alert", "Ticket", run.Treatments[0].Label, run.Input.File} {
		if strings.Contains(decoded, leak) || strings.Contains(link, leak) {
			t.Errorf("link carries %s", leak)
		}
	}
	for _, c := range run.Cases {
		if c.Production.Output != nil && *c.Production.Output != "" && strings.Contains(decoded, *c.Production.Output) {
			t.Errorf("link carries an output")
		}
	}
	if setup.Treatments[0].Model != "openai/<b>mini</b>" {
		t.Errorf("the model is carried as written: %q", setup.Treatments[0].Model)
	}
}

// The links the TypeScript hostedLink wrote for the same runs.
func TestTheHostedLinkIsByteForByteTheTypeScripts(t *testing.T) {
	run := fixture(t)
	eq(t, HostedLink(run, MediumCLI), "https://www.metergraph.dev/replay?utm_source=replay&utm_medium=cli&utm_campaign=0.1.0#setup=eyJ2IjoxLCJ0cmVhdG1lbnRzIjpbeyJtb2RlbCI6Im9wZW5haS9ncHQtNS1taW5pIiwic2V0dGluZ3MiOnsidGVtcGVyYXR1cmUiOm51bGwsIm1heFRva2VucyI6bnVsbCwicmVhc29uaW5nRWZmb3J0IjpudWxsfX0seyJtb2RlbCI6Imdvb2dsZS9nZW1pbmktMy44LWZsYXNoIiwic2V0dGluZ3MiOnsidGVtcGVyYXR1cmUiOjAsIm1heFRva2VucyI6MTAyNCwicmVhc29uaW5nRWZmb3J0IjoibG93In19XSwidHJhY2tlZEZpZWxkcyI6WyIkLmNhdGVnb3J5IiwiJC5wcmlvcml0eSIsIiQudGFncyJdLCJzYW1wbGUiOjI0fQ")

	// Strings JSON.stringify and Go's encoding/json quote differently, numbers
	// they print differently, a NaN, and a version URLSearchParams escapes.
	temp, maxTokens, effort, nan := 1e-7, 1e21, "high", math.NaN()
	run.Treatments[0].Model = "openai/<b>mini</b> &   \"q\" \\ é 🙂\u0001"
	run.Treatments[0].Settings = types.TreatmentSettings{Temperature: &temp, MaxTokens: &maxTokens, ReasoningEffort: &effort}
	run.Treatments[1].Settings = types.TreatmentSettings{Temperature: &nan}
	run.Tool.Version = "0.1.0 beta~*"
	run.Settings.TrackedFields = []string{"a.b", "<x>"}
	eq(t, HostedLink(run, MediumCLI), "https://www.metergraph.dev/replay?utm_source=replay&utm_medium=cli&utm_campaign=0.1.0+beta%7E*#setup=eyJ2IjoxLCJ0cmVhdG1lbnRzIjpbeyJtb2RlbCI6Im9wZW5haS88Yj5taW5pPC9iPiAmIOKAqCBcInFcIiBcXCDDqSDwn5mCXHUwMDAxIiwic2V0dGluZ3MiOnsidGVtcGVyYXR1cmUiOjFlLTcsIm1heFRva2VucyI6MWUrMjEsInJlYXNvbmluZ0VmZm9ydCI6ImhpZ2gifX0seyJtb2RlbCI6Imdvb2dsZS9nZW1pbmktMy44LWZsYXNoIiwic2V0dGluZ3MiOnsidGVtcGVyYXR1cmUiOm51bGwsIm1heFRva2VucyI6bnVsbCwicmVhc29uaW5nRWZmb3J0IjpudWxsfX1dLCJ0cmFja2VkRmllbGRzIjpbImEuYiIsIjx4PiJdLCJzYW1wbGUiOjI0fQ")

	// No version, no tracked fields, no treatments: still a well-formed setup.
	run.Tool.Version = ""
	run.Settings.TrackedFields = nil
	run.Treatments = nil
	eq(t, HostedLink(run, MediumCLI), "https://www.metergraph.dev/replay?utm_source=replay&utm_medium=cli&utm_campaign=unknown#setup=eyJ2IjoxLCJ0cmVhdG1lbnRzIjpbXSwidHJhY2tlZEZpZWxkcyI6W10sInNhbXBsZSI6MjR9")
}

func TestDecodeSetupReadsEveryFragmentForm(t *testing.T) {
	setup := HostedSetupOf(fixture(t))
	enc := EncodeSetup(setup)
	for _, f := range []string{"#setup=" + enc, "setup=" + enc, enc, enc + "=="} {
		got, err := DecodeSetup(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		eq(t, got, setup)
	}
	if _, err := DecodeSetup("#setup=!!!"); err == nil {
		t.Error("garbage should not decode")
	}
}

func eq[T any](t *testing.T, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v\nwant %#v", got, want)
	}
}
