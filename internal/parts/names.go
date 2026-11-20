package parts

// The names file keeps renames and merges between commands. It holds part
// keys and names, nothing else.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	NamesFile   = "ezswap-parts.json"
	NamesSchema = "metergraph.replay-parts/v1"
)

type namesDocument struct {
	Schema string            `json:"schema"`
	Parts  map[string]string `json:"parts"`
}

// NamesPath is the names file beside a trace file.
func NamesPath(traceFile string) string {
	return filepath.Join(filepath.Dir(traceFile), NamesFile)
}

// ReadNames reads the names file. A missing file is an empty map.
func ReadNames(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, &UsageError{Message: fmt.Sprintf("Could not read the names file %s.", path)}
	}
	var document namesDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, &UsageError{Message: fmt.Sprintf("The names file %s is not valid JSON. Fix it or delete it.", path)}
	}
	if document.Schema != NamesSchema {
		return nil, &UsageError{Message: fmt.Sprintf("The names file %s does not have the schema %q. Fix it or delete it.", path, NamesSchema)}
	}
	if document.Parts == nil {
		document.Parts = map[string]string{}
	}
	return document.Parts, nil
}

// WriteNames adds the keys of parts that the file does not have yet, each
// with its default name, and writes the file. An entry already in the file is
// never changed or removed. It reports whether the file changed; the file is
// left alone when nothing is new.
func WriteNames(path string, existing map[string]string, parts []Part) (bool, error) {
	merged := map[string]string{}
	for key, name := range existing {
		merged[key] = name
	}
	added := false
	for _, part := range parts {
		for _, key := range part.Keys {
			if _, ok := merged[key]; !ok {
				merged[key] = DefaultName(key)
				added = true
			}
		}
	}
	if !added {
		return false, nil
	}
	// encoding/json writes map keys sorted.
	data, err := json.MarshalIndent(namesDocument{Schema: NamesSchema, Parts: merged}, "", "  ")
	if err != nil {
		return false, err
	}
	// Write beside the file and rename, so an interrupt cannot leave half a file.
	temp, err := os.CreateTemp(filepath.Dir(path), NamesFile+".*.tmp")
	if err != nil {
		return false, err
	}
	_, err = temp.Write(append(data, '\n'))
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(temp.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(temp.Name(), path)
	}
	if err != nil {
		os.Remove(temp.Name())
		return false, err
	}
	return true, nil
}
