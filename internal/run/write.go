package run

// Write a run: run.json. The report is drawn in the terminal from it.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/metergraph/ezswap/internal/types"
)

type WrittenRun struct {
	RunPath string
}

// EncodeRun is run.json's text: JSON.stringify(run, null, 2) and a newline,
// as closely as encoding/json allows. Raw JSON (schemas) and ordered objects
// keep their own key order; nothing is HTML-escaped; U+2028 and U+2029 are
// written as themselves, as JSON.stringify does. Where the two still differ:
// a Go map (the estimate's perArm and notReplayable) comes out with sorted
// keys, and a run with no timed call leaves input.traffic out where earlier
// versions wrote "traffic": null (types marks it omitempty). Both read
// back the same.
func EncodeRun(run *types.RunFile) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(run); err != nil {
		return nil, err
	}
	return unescapeLineSeparators(out.Bytes()), nil
}

// unescapeLineSeparators turns encoding/json's   and   escapes back
// into the characters. A backslash only ever starts an escape in JSON text, so
// walking escape by escape never mistakes an escaped backslash for one.
func unescapeLineSeparators(text []byte) []byte {
	if !bytes.Contains(text, []byte(`\u202`)) {
		return text
	}
	out := make([]byte, 0, len(text))
	for i := 0; i < len(text); i++ {
		if text[i] != '\\' || i+1 >= len(text) {
			out = append(out, text[i])
			continue
		}
		if text[i+1] == 'u' && i+6 <= len(text) {
			switch string(text[i+2 : i+6]) {
			case "2028":
				out = append(out, " "...)
				i += 5
				continue
			case "2029":
				out = append(out, " "...)
				i += 5
				continue
			}
		}
		out = append(out, text[i], text[i+1])
		i++
	}
	return out
}

// WriteRun writes dir/run.json, creating dir. run.json holds no key:
// treatments name an endpoint preset or host only.
func WriteRun(dir string, run *types.RunFile) (WrittenRun, error) {
	out, err := filepath.Abs(dir)
	if err != nil {
		return WrittenRun{}, err
	}
	if err := os.MkdirAll(out, 0o777); err != nil {
		return WrittenRun{}, err
	}
	text, err := EncodeRun(run)
	if err != nil {
		return WrittenRun{}, err
	}
	runPath := filepath.Join(out, "run.json")
	if err := os.WriteFile(runPath, text, 0o666); err != nil {
		return WrittenRun{}, err
	}
	return WrittenRun{RunPath: runPath}, nil
}
