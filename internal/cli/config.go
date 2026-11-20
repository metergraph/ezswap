package cli

// The settings file: replay.yaml in the working directory, or --config FILE.
// Its keys are the command line's option names; a flag on the command line
// always wins. estimate, map and run read it, each taking the keys it
// accepts, so one file serves all three.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ConfigFile is the settings file read from the working directory.
const ConfigFile = "replay.yaml"

const maxConfigBytes = 64 << 10

// Settings shared by estimate, map and run.
var configShared = []string{
	"models", "endpoint", "floor-endpoint", "api-key-env", "track", "temperature", "max-tokens",
	"reasoning-effort", "budget", "monthly-calls", "no-noise-floor", "allow-no-search",
	"allow-unpriced", "no-price-lookup", "adapter", "concurrency",
}

// Settings for a map, in map and estimate --map.
var configMap = []string{"per-part", "part-key", "ignore", "only", "name", "judge"}

// configKeys is the settings a command takes from the file. The rest of the
// file is left for the other commands.
func configKeys(command string, mapMode bool) []string {
	keys := append([]string(nil), configShared...)
	switch {
	case command == "map":
		keys = append(append(keys, configMap...), "out", "html")
	case command == "run":
		keys = append(keys, "sample", "labels", "out", "html")
	case command == "estimate" && mapMode:
		keys = append(keys, configMap...)
	case command == "estimate":
		keys = append(keys, "sample")
	default:
		return nil
	}
	return keys
}

// configNever is options the file may not set: each is a choice for the
// moment a command runs. --yes above all: spending is confirmed by hand.
var configNever = map[string]string{
	"yes":            "confirm spending on the command line",
	"json":           "choose the output on the command line",
	"config":         "it names the file itself",
	"no-config":      "it names the file itself",
	"map":            "pass --map on the command line",
	"help":           "",
	"version":        "",
	"explore":        "",
	"explain":        "",
	"no-interactive": "",
	"ascii":          "",
	"color":          "",
	"no-color":       "",
	"upstream":       "record takes no settings file",
	"port":           "record takes no settings file",
	"fresh":          "record takes no settings file",
}

// applyConfig merges the settings file into the parsed options for this
// command, under any flag given. It returns the keys it took, in order.
func (s *session) applyConfig(command string) ([]string, string, error) {
	path, named := s.v.str("config")
	keys := configKeys(command, s.v.bool("map"))
	if keys == nil {
		if named || s.v.bool("no-config") {
			return nil, "", usagef("--config and --no-config are for estimate, map and run.")
		}
		return nil, "", nil
	}
	if s.v.bool("no-config") {
		if named {
			return nil, "", usagef("Pass --config FILE or --no-config, not both.")
		}
		return nil, "", nil
	}
	if !named {
		path = filepath.Join(s.io.Cwd, ConfigFile)
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
	}
	shown := s.shownPath(path)
	file, err := os.Open(path)
	if err != nil {
		return nil, "", usagef("Could not read " + shown + ".")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	file.Close()
	if err != nil {
		return nil, "", usagef("Could not read " + shown + ".")
	}
	if len(data) > maxConfigBytes {
		return nil, "", usagef(shown + " is larger than 64 KB, which no settings file needs.")
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, "", usagef(shown + " is not valid YAML: " + yamlProblem(err) + ".")
	}
	names := make([]string, 0, len(doc))
	for name := range doc {
		names = append(names, name)
	}
	sort.Strings(names)
	var taken []string
	for _, name := range names {
		kind, known := options[name]
		if !known {
			return nil, "", usagef(fmt.Sprintf("%s: %q is not a setting. Its keys are the option names, such as models or per-part.", shown, name))
		}
		if why, never := configNever[name]; never {
			message := fmt.Sprintf("%s: %s cannot be set in a settings file", shown, name)
			if why != "" {
				message += "; " + why
			}
			return nil, "", usagef(message + ".")
		}
		// Every value is checked, even one a flag overrides or another
		// command takes: a broken file is reported where it is broken.
		value := doc[name]
		var on bool
		var items []string
		var text string
		switch kind {
		case boolOption:
			b, ok := value.(bool)
			if !ok {
				return nil, "", usagef(fmt.Sprintf("%s: %s takes true or false.", shown, name))
			}
			on = b
		case listOption:
			list, err := configList(value)
			if err != nil {
				return nil, "", usagef(fmt.Sprintf("%s: %s %s.", shown, name, err))
			}
			items = list
		default:
			t, err := configString(name, value)
			if err != nil {
				return nil, "", usagef(fmt.Sprintf("%s: %s %s.", shown, name, err))
			}
			text = t
		}
		if !slices.Contains(keys, name) {
			continue // for another command
		}
		switch kind {
		case boolOption:
			if on && !s.v.bools[name] {
				s.v.bools[name] = true
				taken = append(taken, name)
			}
		case listOption:
			if _, given := s.v.lists[name]; !given {
				s.v.lists[name] = items
				taken = append(taken, name)
			}
		default:
			if _, given := s.v.strings[name]; !given {
				s.v.strings[name] = text
				taken = append(taken, name)
			}
		}
	}
	return taken, shown, nil
}

// configString is a string option's value: text, a number, or for models a
// list of names.
func configString(name string, value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case int:
		return strconv.Itoa(v), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case []any:
		if name == "models" {
			items, err := configList(v)
			if err != nil {
				return "", err
			}
			return strings.Join(items, ","), nil
		}
	}
	return "", errors.New("takes a single value")
}

// configList is a repeatable option's values: one text or a list of texts.
func configList(value any) ([]string, error) {
	switch v := value.(type) {
	case string:
		return []string{v}, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text, ok := item.(string)
			if !ok {
				return nil, errors.New("takes a list of text")
			}
			out = append(out, text)
		}
		return out, nil
	}
	return nil, errors.New("takes text or a list of text")
}

// yamlProblem is the parser's message without its "yaml: " prefix.
func yamlProblem(err error) string {
	return strings.TrimPrefix(strings.TrimSuffix(err.Error(), "."), "yaml: ")
}
