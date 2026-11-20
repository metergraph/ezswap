// Small helpers every adapter shares.

package adapters

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/metergraph/ezswap/internal/cases/jsval"
	"github.com/metergraph/ezswap/internal/jsnum"
	"github.com/metergraph/ezswap/internal/report/term"
	"github.com/metergraph/ezswap/internal/types"
)

// Records reach an adapter as the reader parsed them: objects are
// *jsval.Object, so key order is the source's. The jsval accessors read
// plain maps as well, so an adapter can be handed either. A CapturedCall's
// Request and ToolCalls keep the ordered values, so a schema or a tool call's
// arguments reach run.json and the provider in production's key order.

// IsObject is `typeof value === "object" && value !== null && !Array.isArray(value)`.
func IsObject(value any) bool { return jsval.IsObject(value) }

func get(value any, key string) any { return jsval.Get(value, key) }

func has(value any, key string) bool { return jsval.Has(value, key) }

// Str is a non-empty string, or nil.
func Str(value any) *string {
	s, ok := value.(string)
	if !ok || s == "" {
		return nil
	}
	return &s
}

// Num is a finite number, also from a numeric string (OTLP sends int64 as a
// string), or nil.
func Num(value any) *float64 {
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil
		}
		return &v
	case string:
		if jsval.Trim(v) == "" {
			return nil
		}
		n := jsval.StringToNumber(v)
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil
		}
		return &n
	}
	return nil
}

// FirstNum is the first value that reads as a number.
func FirstNum(values ...any) *float64 {
	for _, value := range values {
		if n := Num(value); n != nil {
			return n
		}
	}
	return nil
}

// FirstStr is the first value that is a non-empty string.
func FirstStr(values ...any) *string {
	for _, value := range values {
		if s := Str(value); s != nil {
			return s
		}
	}
	return nil
}

// ParseJSON is JSON.parse for a string, objects ordered, with the string
// itself back when it does not parse. Anything else is returned as it is.
func ParseJSON(value any) any {
	s, ok := value.(string)
	if !ok {
		return value
	}
	parsed, err := jsval.Parse(s)
	if err != nil {
		return value
	}
	return parsed
}

// HashID is a short stable id for a record that has none of its own.
func HashID(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:16]
}

var numericString = regexp.MustCompile(`^\d+(\.\d+)?$`)

// IsoTime is an ISO 8601 time from an ISO string, epoch seconds, epoch
// milliseconds or epoch nanoseconds.
func IsoTime(value any) *string {
	if s, ok := value.(string); ok && s != "" && !numericString.MatchString(s) {
		ms, ok := parseDate(s)
		if !ok {
			return nil
		}
		iso := isoString(ms)
		return &iso
	}
	n := Num(value)
	if n == nil || *n <= 0 {
		return nil
	}
	ms, ok := timeClip(EpochMs(*n))
	if !ok {
		return nil
	}
	iso := isoString(ms)
	return &iso
}

// EpochMs is milliseconds since the epoch from seconds, milliseconds,
// microseconds or nanoseconds.
func EpochMs(n float64) float64 {
	if n > 1e17 {
		return n / 1e6 // nanoseconds
	}
	if n > 1e14 {
		return n / 1e3 // microseconds
	}
	if n > 1e11 {
		return n // milliseconds
	}
	return n * 1000 // seconds
}

// DurationMs is the wall time between two timestamps in any epoch unit or
// ISO form, or nil.
func DurationMs(start, end any) *float64 {
	a := timeMs(start)
	b := timeMs(end)
	if a == nil || b == nil || *b < *a {
		return nil
	}
	d := jsnum.Round((*b-*a)*1000) / 1000
	return &d
}

func timeMs(value any) *float64 {
	if s, ok := value.(string); ok && s != "" && !numericString.MatchString(s) {
		ms, ok := parseDate(s)
		if !ok {
			return nil
		}
		return &ms
	}
	n := Num(value)
	if n == nil || *n <= 0 {
		return nil
	}
	ms := EpochMs(*n)
	return &ms
}

// UsageOf is a Usage from its parts, or nil when every part is nil.
func UsageOf(parts types.Usage) *types.Usage {
	if parts.InputTokens == nil && parts.OutputTokens == nil && parts.CachedInputTokens == nil && parts.ReasoningTokens == nil {
		return nil
	}
	usage := parts
	return &usage
}

// EmptyResponse is a CapturedResponse that carries nothing.
func EmptyResponse() types.CapturedResponse {
	return types.CapturedResponse{}
}

// NormalizeProvider is a lower-case provider name, with common aliases folded.
func NormalizeProvider(value any) *string {
	s := Str(value)
	if s == nil {
		return nil
	}
	name := jsval.ToLower(jsval.Trim(*s))
	if name == "" {
		return nil
	}
	switch name {
	case "gcp.gemini", "gcp.vertex_ai", "gemini", "vertex_ai", "vertex":
		name = "google"
	case "aws.bedrock":
		name = "bedrock"
	case "azure.ai.openai", "azure.ai.inference":
		name = "azure"
	}
	return &name
}

// AddMissing appends names not already in missing.
func AddMissing(missing *[]string, names ...string) {
	for _, name := range names {
		if !slices.Contains(*missing, name) {
			*missing = append(*missing, name)
		}
	}
}

// newCall is a CapturedCall with every field the adapter did not set empty.
func newCall(c types.CapturedCall) types.CapturedCall {
	if c.Missing == nil {
		c.Missing = []string{}
	}
	return c
}

// RecordContext is the context an adapter gets for each record.
type RecordContext struct {
	// 1-based line number, or array index plus one.
	Line int
	// The record's source text, for hashing an id.
	Raw string
	// The key the developer said holds the part name, or empty.
	PartKey string
}

// MaxPartName is the longest part name kept, in characters.
const MaxPartName = 64

var partSeparators = strings.NewReplacer(",", "-", "=", "-")

// CleanPart is a part name as read from a source: control characters
// removed, trimmed and cut to MaxPartName. Nil when the value is not a
// string or nothing is left.
func CleanPart(value any) *string {
	text, ok := value.(string)
	if !ok {
		return nil
	}
	// A comma or an equals sign would make the name impossible to give to
	// --only and --name, which use them as separators.
	cleaned := strings.TrimSpace(partSeparators.Replace(term.CleanLine(text)))
	if runes := []rune(cleaned); len(runes) > MaxPartName {
		cleaned = strings.TrimSpace(string(runes[:MaxPartName]))
	}
	if cleaned == "" {
		return nil
	}
	return &cleaned
}

// partFrom is the first part name found: the developer's key in each of the
// given containers, in order. Without a part key it is nil.
func partFrom(context RecordContext, containers ...any) *string {
	if context.PartKey == "" {
		return nil
	}
	for _, container := range containers {
		if !IsObject(container) {
			continue
		}
		if part := CleanPart(get(container, context.PartKey)); part != nil {
			return part
		}
	}
	return nil
}

// Adapter reads one trace format.
type Adapter interface {
	Name() string
	// Does this record look like this adapter's format?
	Detect(record any) bool
	// One record to zero or more calls (an OTLP export holds many spans).
	Read(record any, context RecordContext) []types.CapturedCall
}

type adapter struct {
	name   string
	detect func(record any) bool
	read   func(record any, context RecordContext) []types.CapturedCall
}

func (a adapter) Name() string           { return a.name }
func (a adapter) Detect(record any) bool { return a.detect(record) }
func (a adapter) Read(record any, context RecordContext) []types.CapturedCall {
	return a.read(record, context)
}

func ptr[T any](v T) *T { return &v }

// orDefault is `value ?? fallback` for pointers.
func orDefault[T any](value, fallback *T) *T {
	if value != nil {
		return value
	}
	return fallback
}

// nullishOr is `value ?? fallback`.
func nullishOr(value, fallback any) any {
	if jsval.Nullish(value) {
		return fallback
	}
	return value
}
