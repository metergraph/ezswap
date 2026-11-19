package page

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/metergraph/replay/demo"
	"github.com/metergraph/replay/internal/report"
)

func payload(t *testing.T, page []byte) map[string]any {
	t.Helper()
	m := regexp.MustCompile(`(?s)<script type="application/json" id="replay-data">(.*?)</script>`).FindSubmatch(page)
	if m == nil {
		t.Fatal("no data block")
	}
	var data map[string]any
	if err := json.Unmarshal(m[1], &data); err != nil {
		t.Fatalf("data block: %v", err)
	}
	return data
}

func TestMapPageHoldsEveryPartAndCase(t *testing.T) {
	run, err := demo.MapRun()
	if err != nil {
		t.Fatal(err)
	}
	m, err := report.BuildMap(run, report.MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := MapPage(run, m)
	if err != nil {
		t.Fatal(err)
	}
	data := payload(t, page)
	if data["kind"] != "map" || len(data["parts"].([]any)) != len(m.Parts) {
		t.Fatalf("kind %v, %d parts", data["kind"], len(data["parts"].([]any)))
	}
	if n := len(data["cases"].([]any)); n == 0 {
		t.Fatal("no cases")
	}
	order := data["summary"].(map[string]any)["order"].([]any)
	if len(order) != len(m.Parts) {
		t.Errorf("summary order %v", order)
	}
}

func TestPageCannotBeBrokenOutOfAndLoadsNothing(t *testing.T) {
	run, err := demo.MapRun()
	if err != nil {
		t.Fatal(err)
	}
	hostile := `</script><script>alert(1)</script><img src="https://example.com/x.png">`
	run.Cases[0].Production.Output = &hostile
	m, err := report.BuildMap(run, report.MapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := MapPage(run, m)
	if err != nil {
		t.Fatal(err)
	}
	text := string(page)
	if strings.Count(text, "</script>") != 2 || strings.Contains(text, "alert(1)</script>") {
		t.Error("a reply closed the data block")
	}
	found := false
	for _, c := range payload(t, page)["cases"].([]any) {
		if out, _ := c.(map[string]any)["recorded"].(map[string]any)["output"].(string); out == hostile {
			found = true
		}
	}
	if !found {
		t.Error("the reply did not survive the round trip")
	}
	if !strings.Contains(text, `default-src 'none'`) {
		t.Error("no content security policy")
	}
	// Nothing outside the data block points elsewhere.
	outside := regexp.MustCompile(`(?s)<script type="application/json" id="replay-data">.*?</script>`).ReplaceAllString(text, "")
	if regexp.MustCompile(`(?i)(src|href)\s*=\s*["']?(https?:)?//`).MatchString(outside) {
		t.Error("the page references an external resource")
	}
}

func TestRunPage(t *testing.T) {
	run, err := demo.Run()
	if err != nil {
		t.Fatal(err)
	}
	model, err := report.Build(run, report.Options{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := RunPage(model)
	if err != nil {
		t.Fatal(err)
	}
	data := payload(t, page)
	if data["kind"] != "run" || data["model"].(map[string]any)["headline"] != model.Headline {
		t.Errorf("kind %v", data["kind"])
	}
}
