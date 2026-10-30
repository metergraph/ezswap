package adapters

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/metergraph/replay/internal/types"
)

func TestDetectAdapterPicksEachFixturesFormat(t *testing.T) {
	first := func(name string) any {
		data, err := os.ReadFile(fixture(name))
		if err != nil {
			t.Fatal(err)
		}
		line, _, _ := strings.Cut(string(data), "\n")
		return j(t, line)
	}
	equal(t, detected(first("replay.jsonl")), "replay-jsonl")
	equal(t, detected(first("raw-pairs.jsonl")), "raw")
	equal(t, detected(first("helicone.jsonl")), "raw")
	equal(t, detected(first("litellm.jsonl")), "raw")
	equal(t, detected(first("langfuse.jsonl")), "langfuse")
	equal(t, detected(first("otel-spans.jsonl")), "otel")
	equal(t, detected(j(t, `{"hello": "world"}`)), nil)
}

func TestAnExplicitAdapterOverridesDetection(t *testing.T) {
	limit := 1
	calls := read(t, fixture("raw-pairs.jsonl"), ReadTracesOptions{Adapter: "raw", Limit: &limit})
	equal(t, len(calls), 1)
	_, err := ReadTraces(fixture("raw-pairs.jsonl"), ReadTracesOptions{Adapter: "nope"})
	if err == nil || !strings.Contains(err.Error(), "Unknown trace format") {
		t.Errorf("error %v", err)
	}
}

func TestAnUndetectableFileIsAnErrorThatNamesTheFormats(t *testing.T) {
	path := temp(t, "unknown.jsonl", `{"hello":"world"}`+"\n")
	_, err := ReadTraces(path, ReadTracesOptions{})
	if err == nil || !regexp.MustCompile(`replay-jsonl, raw, langfuse, otel|otel, langfuse, replay-jsonl, raw`).MatchString(err.Error()) {
		t.Errorf("error %v", err)
	}
}

func TestBlankLinesAreIgnoredAndBadLinesAreSkippedWithALineNumber(t *testing.T) {
	good := `{"request":{"messages":[{"role":"user","content":"hi"}]},"response":"hello"}`
	path := temp(t, "mixed.jsonl", good+"\n\nnot json\n"+good+"\r\n")
	skipped := []int{}
	calls := read(t, path, ReadTracesOptions{OnSkip: func(line int, _ string) { skipped = append(skipped, line) }})
	equal(t, len(calls), 2)
	equal(t, skipped, []int{3})
	equal(t, calls[0].ID, calls[1].ID, "id is a hash of the line")
	equal(t, str(calls[0].Response.Text), "hello")
}

func TestAnEmptyFileReadsAsNoCalls(t *testing.T) {
	equal(t, read(t, temp(t, "empty.jsonl", "\n  \n")), []types.CapturedCall{})
}

func TestAJSONArrayOfRawPairs(t *testing.T) {
	path := temp(t, "array.json", `[{"request":"Say hi","response":"hi"},{"request":"Say bye","response":"bye"}]`)
	calls := read(t, path)
	equal(t, len(calls), 2)
	if calls[0].ID == calls[1].ID {
		t.Errorf("ids should differ: %s", calls[0].ID)
	}
}
