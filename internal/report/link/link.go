// Package link builds the link into hosted Metergraph. Replay has no
// telemetry, so this link is the only way a run reaches the hosted product,
// and it carries only the run's setup: which models with which settings,
// which fields, how many calls. Never trace content, never keys, never
// endpoints. The setup rides in the URL fragment, which browsers do not send
// to the server. docs/hosted-handoff.md documents the format.
package link

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/metergraph/replay/internal/jsnum"
	"github.com/metergraph/replay/internal/types"
)

// HostedURL is the page the link opens.
const HostedURL = "https://www.metergraph.dev/replay"

// LinkMedium is the utm_medium: where the link was shown.
type LinkMedium string

const MediumCLI LinkMedium = "cli"

// HostedSettings is a treatment's settings in the fragment; nil is JSON null.
type HostedSettings struct {
	Temperature     *float64 `json:"temperature"`
	MaxTokens       *float64 `json:"maxTokens"`
	ReasoningEffort *string  `json:"reasoningEffort"`
}

// HostedTreatment is one treatment in the fragment.
type HostedTreatment struct {
	Model    string         `json:"model"`
	Settings HostedSettings `json:"settings"`
}

// HostedSetup is version 1 of the fragment payload.
type HostedSetup struct {
	V             int               `json:"v"`
	Treatments    []HostedTreatment `json:"treatments"`
	TrackedFields []string          `json:"trackedFields"`
	Sample        int               `json:"sample"`
}

// HostedSetupOf is the setup the fragment carries: built field by field so
// nothing else can slip in.
func HostedSetupOf(run *types.RunFile) HostedSetup {
	treatments := make([]HostedTreatment, 0, len(run.Treatments))
	for _, t := range run.Treatments {
		treatments = append(treatments, HostedTreatment{
			Model: t.Model,
			Settings: HostedSettings{
				Temperature:     copyFloat(t.Settings.Temperature),
				MaxTokens:       copyFloat(t.Settings.MaxTokens),
				ReasoningEffort: copyString(t.Settings.ReasoningEffort),
			},
		})
	}
	tracked := append([]string{}, run.Settings.TrackedFields...)
	return HostedSetup{V: 1, Treatments: treatments, TrackedFields: tracked, Sample: run.Settings.Sample}
}

func copyFloat(v *float64) *float64 {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

func copyString(v *string) *string {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

// marshal is JSON.stringify of the setup, byte for byte: the same key
// order, numbers as JavaScript prints them, and no HTML escaping (Go's
// encoding/json escapes <, >, &, U+2028 and U+2029, and prints some numbers
// differently, which would change the encoded fragment).
func marshal(s HostedSetup) []byte {
	var b strings.Builder
	b.WriteString(`{"v":`)
	b.WriteString(jsnum.String(float64(s.V)))
	b.WriteString(`,"treatments":[`)
	for i, t := range s.Treatments {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"model":`)
		writeString(&b, t.Model)
		b.WriteString(`,"settings":{"temperature":`)
		writeNumber(&b, t.Settings.Temperature)
		b.WriteString(`,"maxTokens":`)
		writeNumber(&b, t.Settings.MaxTokens)
		b.WriteString(`,"reasoningEffort":`)
		if t.Settings.ReasoningEffort == nil {
			b.WriteString("null")
		} else {
			writeString(&b, *t.Settings.ReasoningEffort)
		}
		b.WriteString("}}")
	}
	b.WriteString(`],"trackedFields":[`)
	for i, f := range s.TrackedFields {
		if i > 0 {
			b.WriteByte(',')
		}
		writeString(&b, f)
	}
	b.WriteString(`],"sample":`)
	b.WriteString(jsnum.String(float64(s.Sample)))
	b.WriteByte('}')
	return []byte(b.String())
}

func writeNumber(b *strings.Builder, v *float64) {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		// JSON.stringify writes a non-finite number as null.
		b.WriteString("null")
		return
	}
	b.WriteString(jsnum.String(*v))
}

// writeString is JSON.stringify's string quoting.
func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// EncodeSetup is base64url (RFC 4648 section 5) without padding.
func EncodeSetup(setup HostedSetup) string {
	return base64.RawURLEncoding.EncodeToString(marshal(setup))
}

// DecodeSetup reads a fragment ("#setup=...", "setup=..." or the bare
// encoding) back into a setup.
func DecodeSetup(fragment string) (HostedSetup, error) {
	value := strings.TrimPrefix(fragment, "#")
	encoded := strings.TrimPrefix(value, "setup=")
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(encoded, "="))
	if err != nil {
		return HostedSetup{}, err
	}
	var setup HostedSetup
	if err := json.Unmarshal(raw, &setup); err != nil {
		return HostedSetup{}, err
	}
	return setup, nil
}

// formEncode is URLSearchParams serialization (application/x-www-form-urlencoded):
// alphanumerics and *-._ as they are, space as +, every other byte %XX.
func formEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '*', c == '-', c == '.', c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// HostedLink is the "keep this running on live traffic" link, with UTM
// parameters and the run's setup in the fragment.
func HostedLink(run *types.RunFile, medium LinkMedium) string {
	campaign := run.Tool.Version
	if campaign == "" {
		campaign = "unknown"
	}
	query := "utm_source=" + formEncode("replay") +
		"&utm_medium=" + formEncode(string(medium)) +
		"&utm_campaign=" + formEncode(campaign)
	return HostedURL + "?" + query + "#setup=" + EncodeSetup(HostedSetupOf(run))
}
