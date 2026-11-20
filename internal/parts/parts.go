// Package parts groups cases by the place in the app that made the call.
//
// A part is named only when the developer named it: a "part" field in the
// trace file, the recorder's X-Ezswap-Part header, or the key given with
// --part-key. A replayable case with no name is grouped by a fingerprint of
// what stays fixed across calls from one place: the system prompt, the forced
// tool and the response schema.
package parts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/metergraph/ezswap/internal/types"
)

const (
	// NamePrefix starts the key of an explicitly named part.
	NamePrefix = "name:"
	// FingerprintPrefix starts the key of a fingerprinted part. The 1 is the
	// version of the recipe: change it when Fingerprint changes.
	FingerprintPrefix = "fp1:"

	SourceName        = "name"
	SourceFingerprint = "fingerprint"
	SourceMixed       = "mixed"
)

var (
	uuidPattern  = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	digitPattern = regexp.MustCompile(`[0-9]+`)
)

// Fingerprint is 12 hexadecimal characters that are the same for calls made
// from one place: a hash of the system prompt (with UUIDs and digit runs
// masked, so a date or an id in the prompt does not split a part), the forced
// tool's name and the response schema. The model is left out, so a part that
// is partway through a model migration stays one part.
func Fingerprint(kase types.Case) string {
	system := ""
	if kase.System != nil {
		system = *kase.System
	}
	system = uuidPattern.ReplaceAllString(system, "U")
	system = digitPattern.ReplaceAllString(system, "0")
	tools := []string{}
	if kase.ForcedTool != nil {
		tools = append(tools, kase.ForcedTool.Name)
	}
	schema := ""
	if len(kase.ResponseFormat) > 0 && string(kase.ResponseFormat) != "null" {
		var compact bytes.Buffer
		if err := json.Compact(&compact, kase.ResponseFormat); err == nil {
			schema = compact.String()
		} else {
			schema = string(kase.ResponseFormat)
		}
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode([]any{system, tools, schema})
	sum := sha256.Sum256(bytes.TrimRight(encoded.Bytes(), "\n"))
	return hex.EncodeToString(sum[:])[:12]
}

// KeyOf is the part key of a replayable case: its explicit name, else its
// fingerprint.
func KeyOf(kase types.Case) string {
	if kase.Part != nil && *kase.Part != "" {
		return NamePrefix + *kase.Part
	}
	return FingerprintPrefix + Fingerprint(kase)
}

// DefaultName is the name a key has until the developer renames it. It never
// holds prompt text.
func DefaultName(key string) string {
	if name, ok := strings.CutPrefix(key, NamePrefix); ok {
		return name
	}
	hash := strings.TrimPrefix(key, FingerprintPrefix)
	if len(hash) > 6 {
		hash = hash[:6]
	}
	return "part-" + hash
}

// Part is one group of cases.
type Part struct {
	// Every key merged into this part, sorted.
	Keys []string
	Name string
	// SourceName, SourceFingerprint, or SourceMixed for a merge of both.
	Source string
	// Indexes into the cases given to Assign, in their order.
	Cases []int
	// How many of the cases each key holds.
	KeyCounts map[string]int
}

// Assign groups replayable cases into parts and writes each case's PartKey
// and PartName. names maps a key to the name the developer chose: two keys
// with one name become one part. Cases that are not replayable are left
// alone. Parts come largest first, then by name. The notes say which entries
// of names matched no key or were empty.
func Assign(cases []types.Case, names map[string]string) ([]Part, []string) {
	byName := map[string]*Part{}
	var order []string
	seenKeys := map[string]bool{}
	for i := range cases {
		kase := &cases[i]
		if kase.NotReplayable != nil {
			continue
		}
		key := KeyOf(*kase)
		seenKeys[key] = true
		name := strings.TrimSpace(names[key])
		if name == "" {
			name = DefaultName(key)
		}
		kase.PartKey, kase.PartName = key, name
		part, ok := byName[name]
		if !ok {
			part = &Part{Name: name, KeyCounts: map[string]int{}}
			byName[name] = part
			order = append(order, name)
		}
		part.Cases = append(part.Cases, i)
		part.KeyCounts[key]++
	}
	out := make([]Part, 0, len(order))
	for _, name := range order {
		part := byName[name]
		named, fingerprinted := false, false
		for key := range part.KeyCounts {
			part.Keys = append(part.Keys, key)
			if strings.HasPrefix(key, NamePrefix) {
				named = true
			} else {
				fingerprinted = true
			}
		}
		sort.Strings(part.Keys)
		switch {
		case named && fingerprinted:
			part.Source = SourceMixed
		case named:
			part.Source = SourceName
		default:
			part.Source = SourceFingerprint
		}
		out = append(out, *part)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if len(out[a].Cases) != len(out[b].Cases) {
			return len(out[a].Cases) > len(out[b].Cases)
		}
		return out[a].Name < out[b].Name
	})
	var notes []string
	var unknown, empty []string
	for key, name := range names {
		if strings.TrimSpace(name) == "" {
			empty = append(empty, key)
		} else if !seenKeys[key] {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	sort.Strings(empty)
	if len(unknown) > 0 {
		notes = append(notes, fmt.Sprintf("Names for parts this file does not have were ignored: %s.", strings.Join(unknown, ", ")))
	}
	if len(empty) > 0 {
		notes = append(notes, fmt.Sprintf("Empty names were ignored: %s.", strings.Join(empty, ", ")))
	}
	return out, notes
}

// UsageError is a part option the developer got wrong.
type UsageError struct{ Message string }

func (e *UsageError) Error() string { return e.Message }

// ErrPartsNotFound is the refusal to map a file whose calls do not fall into
// parts.
var ErrPartsNotFound = errors.New("ezswap could not tell the parts of this app apart: most calls have a system prompt of their own. " +
	"Name the parts: add a \"part\" field to each record, send the X-Ezswap-Part header when recording, or pass --part-key.")

// find is the index of the part with this name, or holding this key.
func find(parts []Part, value string) int {
	for i, part := range parts {
		if part.Name == value || slices.Contains(part.Keys, value) {
			return i
		}
	}
	return -1
}

// Select keeps the parts named in only (a name or a key), in the order of
// parts. With no only it keeps them all.
func Select(parts []Part, only []string) ([]Part, error) {
	if len(only) == 0 {
		return parts, nil
	}
	keep := map[int]bool{}
	for _, value := range only {
		i := find(parts, strings.TrimSpace(value))
		if i < 0 {
			return nil, &UsageError{Message: fmt.Sprintf("--only %s matches no part. The parts are: %s.", value, nameList(parts))}
		}
		keep[i] = true
	}
	var out []Part
	for i, part := range parts {
		if keep[i] {
			out = append(out, part)
		}
	}
	return out, nil
}

func nameList(parts []Part) string {
	names := make([]string, len(parts))
	for i, part := range parts {
		names[i] = part.Name
	}
	return strings.Join(names, ", ")
}

// CheckFingerprints refuses when the fingerprint did not find parts: among
// the parts that would run there are at least four fingerprint groups and
// more than half of them hold a single case. That is what a system prompt
// with per-request text looks like.
func CheckFingerprints(parts []Part) error {
	groups, single := 0, 0
	for _, part := range parts {
		for key, count := range part.KeyCounts {
			if !strings.HasPrefix(key, FingerprintPrefix) {
				continue
			}
			groups++
			if count == 1 {
				single++
			}
		}
	}
	if groups >= 4 && single*2 > groups {
		return ErrPartsNotFound
	}
	return nil
}

// Rename resolves --name flags ("KEY=NAME", where KEY is a part key or a
// part's current name) into names by key.
func Rename(parts []Part, flags []string) (map[string]string, error) {
	out := map[string]string{}
	for _, flag := range flags {
		target, name, ok := strings.Cut(flag, "=")
		target, name = strings.TrimSpace(target), strings.TrimSpace(name)
		if !ok || target == "" || name == "" {
			return nil, &UsageError{Message: fmt.Sprintf("--name takes KEY=NAME, not %q.", flag)}
		}
		i := find(parts, target)
		if i < 0 {
			return nil, &UsageError{Message: fmt.Sprintf("--name %s matches no part. The parts are: %s.", target, nameList(parts))}
		}
		if slices.Contains(parts[i].Keys, target) {
			out[target] = name
			continue
		}
		for _, key := range parts[i].Keys {
			out[key] = name
		}
	}
	return out, nil
}
