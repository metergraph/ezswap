package parts

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/metergraph/replay/internal/types"
)

func str(s string) *string { return &s }

func kase(id, system string, part *string) types.Case {
	return types.Case{ID: id, System: &system, Part: part}
}

func TestFingerprintPinned(t *testing.T) {
	// Computed once and committed: a change here changes every part key, so
	// it must come with a new FingerprintPrefix.
	got := Fingerprint(types.Case{System: str("Extract the order as JSON."), ResponseFormat: json.RawMessage(`{"type":"json_object"}`)})
	if got != "4c190b1a59de" {
		t.Fatalf("fingerprint %q", got)
	}
}

func TestFingerprintMasksDigitsAndUUIDs(t *testing.T) {
	a := Fingerprint(kase("a", "Today is 2026-11-18. Tenant 6f1c2a34-0b1d-4e5f-8a9b-0c1d2e3f4a5b. Reply in JSON.", nil))
	b := Fingerprint(kase("b", "Today is 2026-12-02. Tenant 0A1B2C3D-1111-2222-3333-444455556666. Reply in JSON.", nil))
	if a != b {
		t.Fatal("a date and a UUID in the prompt split the part")
	}
	if a == Fingerprint(kase("c", "Today is 2026-11-18. Reply in prose.", nil)) {
		t.Fatal("different prompts share a fingerprint")
	}
}

func TestFingerprintIgnoresModel(t *testing.T) {
	a, b := kase("a", "Classify.", nil), kase("b", "Classify.", nil)
	a.Model, b.Model = str("gpt-4.1-mini"), str("gpt-5-mini")
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("the model is part of the fingerprint")
	}
}

func TestFingerprintDiffersBySchemaAndTools(t *testing.T) {
	plain := kase("a", "Classify.", nil)
	schema := kase("b", "Classify.", nil)
	schema.ResponseFormat = json.RawMessage(`{"type": "json_object"}`)
	spaced := kase("c", "Classify.", nil)
	spaced.ResponseFormat = json.RawMessage(`{"type":"json_object"}`)
	tool := kase("d", "Classify.", nil)
	tool.ForcedTool = &types.ForcedTool{Name: "classify"}
	if Fingerprint(plain) == Fingerprint(schema) || Fingerprint(plain) == Fingerprint(tool) || Fingerprint(schema) == Fingerprint(tool) {
		t.Fatal("a schema or a forced tool does not change the fingerprint")
	}
	if Fingerprint(schema) != Fingerprint(spaced) {
		t.Fatal("whitespace in the schema changes the fingerprint")
	}
}

func TestMixedFileAssignsPerCase(t *testing.T) {
	reason := types.ContentTruncated
	cases := []types.Case{
		kase("1", "Classify.", str("classify")),
		kase("2", "Extract.", nil),
		kase("3", "Classify.", str("classify")),
		{ID: "4", NotReplayable: &reason, Part: str("classify")},
	}
	parts, notes := Assign(cases, nil)
	if len(parts) != 2 || len(notes) != 0 {
		t.Fatalf("parts %v notes %v", parts, notes)
	}
	if parts[0].Name != "classify" || parts[0].Source != SourceName || len(parts[0].Cases) != 2 {
		t.Fatalf("named part %+v", parts[0])
	}
	if parts[1].Source != SourceFingerprint || !strings.HasPrefix(parts[1].Name, "part-") || len(parts[1].Name) != 11 {
		t.Fatalf("fingerprinted part %+v", parts[1])
	}
	if cases[0].PartKey != "name:classify" || cases[1].PartKey != parts[1].Keys[0] || cases[1].PartName != parts[1].Name {
		t.Fatalf("cases not marked: %+v", cases[:2])
	}
	if cases[3].PartKey != "" {
		t.Fatal("a case that cannot be replayed was grouped")
	}
}

func TestRenameAndMerge(t *testing.T) {
	cases := []types.Case{kase("1", "Extract v1.", nil), kase("2", "Extract, second prompt.", nil), kase("3", "Classify.", str("classify"))}
	first, _ := Assign(cases, nil)
	names := map[string]string{}
	for _, part := range first {
		if part.Source == SourceFingerprint {
			names[part.Keys[0]] = "extract-order"
		}
	}
	names["name:classify"] = "classify-ticket"
	names["fp1:000000000000"] = "gone"
	names["name:empty"] = " "
	merged, notes := Assign(cases, names)
	if len(merged) != 2 {
		t.Fatalf("parts %+v", merged)
	}
	if merged[0].Name != "extract-order" || len(merged[0].Keys) != 2 || len(merged[0].Cases) != 2 {
		t.Fatalf("merge %+v", merged[0])
	}
	if merged[1].Name != "classify-ticket" {
		t.Fatalf("rename %+v", merged[1])
	}
	if len(notes) != 2 || !strings.Contains(notes[0], "fp1:000000000000") || !strings.Contains(notes[1], "name:empty") {
		t.Fatalf("notes %v", notes)
	}
}

func fingerprintCases(sizes ...int) []types.Case {
	var out []types.Case
	for g, size := range sizes {
		for i := 0; i < size; i++ {
			out = append(out, kase("c", strings.Repeat("x", g+1), nil))
		}
	}
	return out
}

func TestRefusesSingletonFingerprints(t *testing.T) {
	parts, _ := Assign(fingerprintCases(12, 9, 1, 1, 1), nil)
	if err := CheckFingerprints(parts); !errors.Is(err, ErrPartsNotFound) {
		t.Fatalf("5 groups with 3 single: %v", err)
	}
}

func TestDoesNotRefuseBelowFourGroups(t *testing.T) {
	parts, _ := Assign(fingerprintCases(1, 1, 1), nil)
	if err := CheckFingerprints(parts); err != nil {
		t.Fatal(err)
	}
	parts, _ = Assign(fingerprintCases(5, 5, 1, 1), nil)
	if err := CheckFingerprints(parts); err != nil {
		t.Fatalf("half single is not more than half: %v", err)
	}
}

func TestOnlyNamedPartsSkipFingerprintRefusal(t *testing.T) {
	cases := append(fingerprintCases(1, 1, 1, 1), kase("n", "Classify.", str("classify")))
	parts, _ := Assign(cases, nil)
	selected, err := Select(parts, []string{"classify"})
	if err != nil || len(selected) != 1 {
		t.Fatalf("%v %v", selected, err)
	}
	if err := CheckFingerprints(selected); err != nil {
		t.Fatal(err)
	}
	if err := CheckFingerprints(parts); !errors.Is(err, ErrPartsNotFound) {
		t.Fatal("all parts should refuse")
	}
}

func TestSelectAndRename(t *testing.T) {
	cases := []types.Case{kase("1", "Extract.", nil), kase("2", "Classify.", str("classify"))}
	parts, _ := Assign(cases, nil)
	var usage *UsageError
	if _, err := Select(parts, []string{"nope"}); !errors.As(err, &usage) {
		t.Fatalf("unknown --only: %v", err)
	}
	byKey, err := Select(parts, []string{"name:classify"})
	if err != nil || len(byKey) != 1 || byKey[0].Name != "classify" {
		t.Fatalf("select by key: %v %v", byKey, err)
	}
	if _, err := Rename(parts, []string{"nope=x"}); !errors.As(err, &usage) {
		t.Fatalf("unknown --name key: %v", err)
	}
	for _, bad := range []string{"classify", "=x", "classify="} {
		if _, err := Rename(parts, []string{bad}); !errors.As(err, &usage) {
			t.Fatalf("bad --name form %q: %v", bad, err)
		}
	}
	renamed, err := Rename(parts, []string{"classify=classify-ticket"})
	if err != nil || renamed["name:classify"] != "classify-ticket" {
		t.Fatalf("rename by current name: %v %v", renamed, err)
	}
}

func TestNamesFileAddsKeysAndKeepsEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), NamesFile)
	names, err := ReadNames(path)
	if err != nil || len(names) != 0 {
		t.Fatalf("a missing file is empty: %v %v", names, err)
	}
	cases := []types.Case{kase("1", "Extract.", nil), kase("2", "Classify.", str("classify"))}
	parts, _ := Assign(cases, names)
	changed, err := WriteNames(path, names, parts)
	if err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	written, _ := ReadNames(path)
	if len(written) != 2 || written["name:classify"] != "classify" {
		t.Fatalf("written %v", written)
	}
	written["name:classify"] = "classify-ticket"
	written["name:old"] = "kept"
	if changed, _ := WriteNames(path, written, parts); changed {
		t.Fatal("nothing new, but the file was written")
	}
	more, _ := Assign(append(cases, kase("3", "Route.", str("route"))), written)
	if changed, err := WriteNames(path, written, more); err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	final, _ := ReadNames(path)
	if final["name:classify"] != "classify-ticket" || final["name:old"] != "kept" || final["name:route"] != "route" {
		t.Fatalf("entries changed or lost: %v", final)
	}
	text, _ := os.ReadFile(path)
	if strings.Contains(string(text), "Extract.") || strings.Contains(string(text), "Classify.") {
		t.Fatal("the names file holds prompt text")
	}
}

func TestNamesFileInvalidIsUsageError(t *testing.T) {
	dir := t.TempDir()
	var usage *UsageError
	for name, text := range map[string]string{"not-json": "{", "schema": `{"schema":"other","parts":{}}`} {
		path := filepath.Join(dir, name+".json")
		os.WriteFile(path, []byte(text), 0o644)
		if _, err := ReadNames(path); !errors.As(err, &usage) || !strings.Contains(err.Error(), path) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestUnwritableDirectoryReportsAnError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the user cannot write")
	}
	dir := t.TempDir()
	os.Chmod(dir, 0o555)
	defer os.Chmod(dir, 0o755)
	parts, _ := Assign([]types.Case{kase("1", "Extract.", nil)}, nil)
	if changed, err := WriteNames(filepath.Join(dir, NamesFile), nil, parts); err == nil || changed {
		t.Fatal("expected an error")
	}
}

func out(values ...string) []*string {
	list := make([]*string, len(values))
	for i := range values {
		list[i] = &values[i]
	}
	return list
}

func TestComparedPaths(t *testing.T) {
	orders := out(
		`{"order":{"id":"EX-1","total":10.5},"sku":"A-1","note":"`+strings.Repeat("long ", 40)+`"}`,
		`{"order":{"id":"EX-2","total":3},"sku":"A-1","note":"`+strings.Repeat("text ", 40)+`"}`,
		"```json\n"+`{"order":{"id":"EX-3","total":7},"sku":"B-2"}`+"\n```",
		`{"order":{"id":"EX-4","total":1},"sku":"B-2","extra":true}`,
	)
	paths, reason := ComparedPaths(orders, nil, nil)
	if reason != "" || strings.Join(paths, " ") != "$.sku $.order.total $.order.id" {
		t.Fatalf("nested and id fields, label then number then text: %v %q", paths, reason)
	}
	paths, _ = ComparedPaths(orders, []string{"$.extra"}, []string{"$.order.id"})
	if strings.Join(paths, " ") != "$.extra $.sku $.order.total" {
		t.Fatalf("track first, ignore removed: %v", paths)
	}
	if _, reason := ComparedPaths(out("Hello there.", "Thanks for writing.", `{"a":1}`), nil, nil); reason != ReasonFreeText {
		t.Fatalf("free text: %q", reason)
	}
	long := strings.Repeat("words ", 40)
	if _, reason := ComparedPaths(out(`{"summary":"`+long+`"}`, `{"summary":"`+long+`x"}`), nil, nil); reason != ReasonNoField {
		t.Fatalf("no comparable field: %q", reason)
	}
	if _, reason := ComparedPaths(nil, nil, nil); reason != ReasonFreeText {
		t.Fatalf("no outputs: %q", reason)
	}
	wide := `{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7,"h":8,"i":9,"j":10,"k":11,"l":12}`
	if paths, _ := ComparedPaths(out(wide, wide), []string{"$.z"}, nil); len(paths) != MaxPaths || paths[0] != "$.z" {
		t.Fatalf("cap of %d with tracked first: %v", MaxPaths, paths)
	}
}
