package cli

// The option parser: node:util parseArgs in strict mode with positionals,
// which the TypeScript CLI used, down to its error messages. Long options
// take --name value or --name=value; booleans take no value; -y, -h and -v
// are short for --yes, --help and --version and may be grouped; everything
// after "--" is positional.

import (
	"fmt"
	"strings"
)

type optionKind int

const (
	boolOption optionKind = iota
	stringOption
	listOption // a string option that may repeat
)

var options = map[string]optionKind{
	"models":           stringOption,
	"endpoint":         stringOption,
	"floor-endpoint":   stringOption,
	"api-key-env":      stringOption,
	"sample":           stringOption,
	"track":            listOption,
	"temperature":      stringOption,
	"max-tokens":       stringOption,
	"reasoning-effort": stringOption,
	"budget":           stringOption,
	"monthly-calls":    stringOption,
	"no-noise-floor":   boolOption,
	"labels":           boolOption,
	"allow-no-search":  boolOption,
	"allow-unpriced":   boolOption,
	"adapter":          stringOption,
	"out":              stringOption,
	"json":             boolOption,
	"explore":          boolOption,
	"explain":          boolOption,
	"no-interactive":   boolOption,
	"ascii":            boolOption,
	"color":            boolOption,
	"no-color":         boolOption,
	"yes":              boolOption,
	"concurrency":      stringOption,
	"help":             boolOption,
	"version":          boolOption,
}

var shorts = map[byte]string{'y': "yes", 'h': "help", 'v': "version"}

// values are the parsed options.
type values struct {
	strings map[string]string
	lists   map[string][]string
	bools   map[string]bool
}

// str is a string option and whether it was given.
func (v values) str(name string) (string, bool) {
	s, ok := v.strings[name]
	return s, ok
}

func (v values) bool(name string) bool { return v.bools[name] }

func (v values) list(name string) []string { return v.lists[name] }

type parseError struct{ message string }

func (e *parseError) Error() string { return e.message }

func unknownOption(raw string) error {
	return &parseError{fmt.Sprintf(`Unknown option '%s'. To specify a positional argument starting with a '-', place it at the end of the command after '--', as in '-- "%s"`, raw, raw)}
}

func parseArgs(argv []string) (values, []string, error) {
	v := values{strings: map[string]string{}, lists: map[string][]string{}, bools: map[string]bool{}}
	var positionals []string
	set := func(name string, kind optionKind, value string) {
		switch kind {
		case listOption:
			v.lists[name] = append(v.lists[name], value)
		case stringOption:
			v.strings[name] = value
		default:
			v.bools[name] = true
		}
	}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case arg == "--":
			positionals = append(positionals, argv[i+1:]...)
			return v, positionals, nil
		case strings.HasPrefix(arg, "--"):
			name, inline, hasInline := strings.Cut(arg[2:], "=")
			kind, ok := options[name]
			if !ok {
				return v, nil, unknownOption("--" + name)
			}
			if kind == boolOption {
				if hasInline {
					return v, nil, &parseError{fmt.Sprintf("Option '--%s' does not take an argument", name)}
				}
				set(name, kind, "")
				continue
			}
			if hasInline {
				set(name, kind, inline)
				continue
			}
			if i+1 >= len(argv) {
				return v, nil, &parseError{fmt.Sprintf("Option '--%s <value>' argument missing", name)}
			}
			next := argv[i+1]
			if len(next) > 1 && next[0] == '-' {
				return v, nil, &parseError{fmt.Sprintf("Option '--%s' argument is ambiguous.\nDid you forget to specify the option argument for '--%s'?\nTo specify an option argument starting with a dash use '--%s=-XYZ'.", name, name, name)}
			}
			set(name, kind, next)
			i++
		case len(arg) > 1 && arg[0] == '-':
			for k := 1; k < len(arg); k++ {
				name, ok := shorts[arg[k]]
				if !ok {
					return v, nil, unknownOption("-" + string(arg[k]))
				}
				set(name, boolOption, "")
			}
		default:
			positionals = append(positionals, arg)
		}
	}
	return v, positionals, nil
}
