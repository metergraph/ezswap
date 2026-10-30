package run

// Endpoints and model specs: what `--endpoint`, `--floor-endpoint` and
// `--models` name, read into treatments and provider settings. Nothing here
// reads a key: a key stays in its environment variable until a provider
// is created.

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/metergraph/replay/internal/cases/jsval"
	"github.com/metergraph/replay/internal/providers"
	"github.com/metergraph/replay/internal/types"
)

const DefaultEndpoint = providers.PresetOpenRouter

type EndpointSpec struct {
	// The name a treatment records: the preset, or the base URL's host. Never a key.
	Key string
	// Empty for a custom endpoint.
	Preset providers.PresetName
	// A custom endpoint's base URL; empty for a preset.
	BaseURL string
	// Runs on this machine and costs nothing (Ollama, or a loopback URL).
	Free bool
}

// SpecError is an endpoint or model spec that does not read.
type SpecError struct{ Message string }

func (e *SpecError) Error() string { return e.Message }

func specError(format string, args ...any) *SpecError {
	return &SpecError{Message: fmt.Sprintf(format, args...)}
}

var loopback = map[string]bool{"localhost": true, "127.0.0.1": true, "[::1]": true, "::1": true, "0.0.0.0": true}

func presetList() string {
	names := make([]string, len(providers.PresetNames))
	for i, name := range providers.PresetNames {
		names[i] = string(name)
	}
	return strings.Join(names, ", ")
}

// ParseEndpoint reads a preset name or an http(s) base URL.
//
// This reads the URL with net/url and applies the parts of WHATWG's normalization that change the
// result here: a lower-case host and no default port.
func ParseEndpoint(spec string) (EndpointSpec, error) {
	text := jsval.Trim(spec)
	if providers.IsPreset(text) {
		name := providers.PresetName(text)
		return EndpointSpec{Key: text, Preset: name, Free: providers.Presets[name].Free}, nil
	}
	notEndpoint := specError(
		`"%s" is not an endpoint. Use a preset (%s) or an http(s) base URL.`, text, presetList(),
	)
	u, err := url.Parse(text)
	if err != nil || u.Scheme == "" {
		return EndpointSpec{}, notEndpoint
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return EndpointSpec{}, specError("The endpoint URL must start with http:// or https://.")
	}
	if u.Host == "" {
		// WHATWG refuses an http(s) URL without a host.
		return EndpointSpec{}, notEndpoint
	}
	if u.User != nil {
		return EndpointSpec{}, specError("Put the key in an environment variable, not in the endpoint URL.")
	}
	hostname := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	// url.host: the hostname (bracketed when IPv6) and a port that is not the default.
	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	if port != "" {
		host += ":" + port
	}
	pathname := u.EscapedPath()
	if pathname == "" {
		pathname = "/"
	}
	baseURL := strings.TrimRight(u.Scheme+"://"+host+pathname, "/")
	check := hostname
	if strings.HasSuffix(check, ".localhost") {
		check = "localhost"
	}
	return EndpointSpec{Key: host, BaseURL: baseURL, Free: loopback[check]}, nil
}

type ModelSpec struct {
	Model    string
	Endpoint EndpointSpec
	// Only the settings this spec set (the rest nil); the rest come from the command's flags.
	Settings types.TreatmentSettings
}

var settingKeys = map[string]bool{"temperature": true, "max_tokens": true, "reasoning_effort": true}

func parseSetting(spec, assignment string, into *types.TreatmentSettings) error {
	eq := strings.Index(assignment, "=")
	var name, value string
	if eq >= 0 {
		name = jsval.Trim(assignment[:eq])
		value = jsval.Trim(assignment[eq+1:])
	}
	if eq < 0 || !settingKeys[name] || value == "" {
		return specError(
			`Could not read "%s" in "%s". Settings are temperature=, max_tokens= and reasoning_effort=.`,
			assignment, spec,
		)
	}
	switch name {
	case "temperature":
		n, err := ParseTemperature(value)
		if err != nil {
			return err
		}
		into.Temperature = &n
	case "max_tokens":
		n, err := ParseMaxTokens(value)
		if err != nil {
			return err
		}
		into.MaxTokens = &n
	default:
		effort, err := ParseEffort(value)
		if err != nil {
			return err
		}
		into.ReasoningEffort = &effort
	}
	return nil
}

var (
	decimalPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
	wholePattern   = regexp.MustCompile(`^[0-9]+$`)
)

func ParseTemperature(value string) (float64, error) {
	n := jsval.StringToNumber(value)
	if !decimalPattern.MatchString(jsval.Trim(value)) || !finite(n) || n < 0 || n > 2 {
		return 0, specError(`Temperature must be a number from 0 to 2, not "%s".`, value)
	}
	return n, nil
}

func ParseMaxTokens(value string) (float64, error) {
	n := jsval.StringToNumber(value)
	if !wholePattern.MatchString(jsval.Trim(value)) || n < 1 || n > 200_000 {
		return 0, specError(`Max tokens must be a whole number from 1 to 200000, not "%s".`, value)
	}
	return n, nil
}

var efforts = []string{"none", "minimal", "low", "medium", "high", "xhigh"}

func ParseEffort(value string) (string, error) {
	effort := jsval.ToLower(jsval.Trim(value))
	for _, known := range efforts {
		if effort == known {
			return effort, nil
		}
	}
	return "", specError("Reasoning effort must be one of %s.", strings.Join(efforts, ", "))
}

var settingPiece = regexp.MustCompile(`^[a-z_]+=`)

// splitModels splits a `--models` list. A comma also separates per-model
// settings (`model@temperature=0.2,max_tokens=500`), so a piece that is only a
// setting belongs to the model before it.
func splitModels(list string) []string {
	var specs []string
	for _, piece := range strings.Split(list, ",") {
		piece = jsval.Trim(piece)
		if piece == "" {
			continue
		}
		last := len(specs) - 1
		if last >= 0 && strings.Contains(specs[last], "@") && settingPiece.MatchString(piece) {
			specs[last] += "," + piece
		} else {
			specs = append(specs, piece)
		}
	}
	return specs
}

// ParseModels reads `--models`. A model may name its endpoint preset first
// (`openai:gpt-5-mini`); a bare model uses the default endpoint. On the
// OpenAI preset a bare id is read as an OpenAI model, so it prices.
func ParseModels(list string, defaultEndpoint EndpointSpec) ([]ModelSpec, error) {
	specs := splitModels(list)
	if len(specs) == 0 {
		return nil, specError("--models names no model.")
	}
	out := make([]ModelSpec, 0, len(specs))
	for _, spec := range specs {
		rest := spec
		endpoint := defaultEndpoint
		if colon := strings.Index(rest, ":"); colon > 0 && providers.IsPreset(rest[:colon]) {
			parsed, err := ParseEndpoint(rest[:colon])
			if err != nil {
				return nil, err
			}
			endpoint = parsed
			rest = rest[colon+1:]
		}
		var settings types.TreatmentSettings
		if at := strings.Index(rest, "@"); at >= 0 {
			for _, assignment := range strings.Split(rest[at+1:], ",") {
				if err := parseSetting(spec, assignment, &settings); err != nil {
					return nil, err
				}
			}
			rest = rest[:at]
		}
		model := jsval.Trim(rest)
		if model == "" || strings.IndexFunc(model, jsval.IsSpace) >= 0 {
			return nil, specError(`"%s" does not name a model.`, spec)
		}
		if endpoint.Preset == providers.PresetOpenAI && !strings.Contains(model, "/") {
			model = "openai/" + model
		}
		out = append(out, ModelSpec{Model: model, Endpoint: endpoint, Settings: settings})
	}
	return out, nil
}
