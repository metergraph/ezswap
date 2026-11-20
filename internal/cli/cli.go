// Package cli is the command line: demo, estimate, run and report.
//
// Exit codes are stable (see the exit constants and docs/cli.md). With
// --json, stdout is one JSON object and nothing else.
//
// On an interactive
// terminal the spend confirmation, a run's progress and the report's
// explorer are drawn with Bubble Tea; otherwise the CLI prints plain text.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/colorprofile"
	xterm "github.com/charmbracelet/x/term"

	"github.com/metergraph/replay/internal/pricing"
	"github.com/metergraph/replay/internal/providers"
	"github.com/metergraph/replay/internal/report/term"
	"github.com/metergraph/replay/internal/run"
	"github.com/metergraph/replay/internal/types"
)

// Version is the tool's version: printed by --version and recorded in every
// run file. GoReleaser sets it with
// -X github.com/metergraph/replay/internal/cli.Version=...; a
// `go install ...@vX.Y.Z` build takes it from the module version instead.
var Version = "dev"

func init() {
	if Version != "dev" {
		return
	}
	if info, ok := debug.ReadBuildInfo(); ok && strings.HasPrefix(info.Main.Version, "v") {
		Version = strings.TrimPrefix(info.Main.Version, "v")
	}
}

// Exit codes.
const (
	ExitOK                = 0
	ExitError             = 1
	ExitUsage             = 2
	ExitNoReplayableCases = 3
	ExitRefused           = 4
	ExitProviderConfig    = 5
	ExitStoppedEarly      = 6
	// The explorer ended by a signal: 128 plus the signal's number.
	ExitSIGINT  = 130
	ExitSIGTERM = 143
)

// IO is everything the CLI touches outside itself. Tests replace it.
type IO struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	// Whether each stream is a terminal.
	StdoutTTY, StderrTTY, StdinTTY bool
	// The terminal's size; 0 reads COLUMNS (then 100) for the width and 24 rows.
	Columns, Rows int
	// The environment. Keys, base URLs and display settings are read from it.
	Env map[string]string
	// The HTTP client providers send with; nil is the providers' default.
	HTTPClient *http.Client
	// The clock for the run and for pricing; nil is time.Now.
	Now func() time.Time
	// Asks a yes or no question; nil asks on the terminal.
	Confirm func(question string) (bool, error)
	// Opens the explorer and says how it ended ("quit", "SIGINT" or
	// "SIGTERM"); nil opens it on the terminal.
	Explore func(run *types.RunFile, o ExploreOptions) (string, error)
	// Stops new calls when done; nil installs a handler: the first Ctrl-C.
	Signal context.Context
	// Cancels calls in flight when done; nil with Signal set means never,
	// else the second Ctrl-C.
	Hard context.Context
	// The directory paths are shown relative to; "" is the working directory.
	Cwd string
	// Metergraph's Pricing API, asked for models the bundled catalog cannot
	// price. "" never asks.
	PricingURL string
	// Where the Pricing API's answers are cached. "" caches nothing.
	CacheDir string
	// Where --judge asks its judge. "" is the AI Gateway.
	JudgeURL string
}

// ExploreOptions are what the explorer is opened with.
type ExploreOptions struct {
	// The run's path as shown in the status bar.
	Name    string
	Color   bool
	Profile colorprofile.Profile
	ASCII   bool
	Tracked []string
	Labels  bool
	// Calls a month for the projection; nil reads the run.
	MonthlyCalls *float64
	// The tab to open on.
	Tab int
}

// DefaultIO is the process's own streams, environment and terminal.
func DefaultIO() IO {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	o := IO{
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Stdin:     os.Stdin,
		StdoutTTY: xterm.IsTerminal(os.Stdout.Fd()),
		StderrTTY: xterm.IsTerminal(os.Stderr.Fd()),
		StdinTTY:  xterm.IsTerminal(os.Stdin.Fd()),
		Env:       env,
	}
	if v := env["METERGRAPH_REPLAY_NO_PRICE_LOOKUP"]; v == "" || v == "0" {
		o.PricingURL = pricing.DefaultPricingURL
		if url := env["METERGRAPH_PRICING_URL"]; url != "" {
			o.PricingURL = url
		}
	}
	if dir, err := os.UserCacheDir(); err == nil {
		o.CacheDir = filepath.Join(dir, "metergraph-replay")
	}
	if o.StdoutTTY {
		if w, h, err := xterm.GetSize(os.Stdout.Fd()); err == nil {
			o.Columns, o.Rows = w, h
		}
	}
	return o
}

// Run is the CLI on the process: it returns the exit code.
func Run(args []string) int {
	return Main(args, DefaultIO())
}

type usageError struct{ message string }

func (e *usageError) Error() string { return e.message }

func usagef(message string) error { return &usageError{message} }

type cliError struct {
	exitCode int
	code     string
	message  string
}

func (e *cliError) Error() string { return e.message }

func configError(err *providers.ProviderConfigError) *cliError {
	hint := ""
	switch err.Code {
	case "credential_missing":
		hint = " Export it in your shell, or choose another endpoint with --endpoint."
	case "base_url_missing":
		hint = " Export it, or pass a base URL with --endpoint."
	}
	return &cliError{ExitProviderConfig, err.Code, err.Message + "." + hint}
}

// field is one key of a JSON object written in order.
type field struct {
	key   string
	value any
}

// object is a JSON object that keeps its key order, as JSON.stringify does.
type object []field

func (o object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(f.key)
		b.Write(key)
		b.WriteByte(':')
		value, err := encodeJSON(f.value)
		if err != nil {
			return nil, err
		}
		b.Write(value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// encodeJSON is compact JSON without HTML escaping.
func encodeJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

type outcome struct {
	exitCode int
	json     object
}

// session is one invocation: the IO, the parsed options and how to write.
type session struct {
	io     IO
	v      values
	asJSON bool
	mu     sync.Mutex
}

// say writes a line to stdout, except with --json.
func (s *session) say(text string) {
	if s.asJSON {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	io.WriteString(s.io.Stdout, text+"\n")
}

func (s *session) env(name string) string { return s.io.Env[name] }

// environ is the environment in os.Environ form.
func (s *session) environ() []string {
	out := make([]string, 0, len(s.io.Env))
	for k, v := range s.io.Env {
		out = append(out, k+"="+v)
	}
	slices.Sort(out)
	return out
}

// Main runs the CLI with args (without the program name) and returns the exit code.
func Main(args []string, o IO) int {
	if o.Stdout == nil {
		o.Stdout = io.Discard
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	if o.Stdin == nil {
		o.Stdin = strings.NewReader("")
	}
	if o.Env == nil {
		o.Env = map[string]string{}
	}
	s := &session{io: o, asJSON: slices.Contains(args, "--json")}
	command := ""
	hasCommand := false
	emit := func(exitCode int, fields object) int {
		if s.asJSON {
			out := append(object{{"ok", exitCode == ExitOK}, {"exitCode", exitCode}}, fields...)
			text, err := encodeJSON(out)
			if err == nil {
				io.WriteString(o.Stdout, string(text)+"\n")
			}
		}
		return exitCode
	}

	result, err := func() (outcome, error) {
		v, positionals, err := parseArgs(args)
		if err != nil {
			return outcome{}, usagef(err.Error())
		}
		s.v = v
		if v.bool("version") {
			s.say(Version)
			return outcome{ExitOK, object{{"command", "version"}, {"version", Version}}}, nil
		}
		if len(positionals) > 0 {
			command, hasCommand = positionals[0], true
		}
		if v.bool("help") || !hasCommand || command == "help" {
			if !s.asJSON {
				io.WriteString(o.Stdout, Help)
			}
			code := ExitOK
			if !hasCommand && !v.bool("help") {
				code = ExitUsage
			}
			return outcome{code, object{{"command", "help"}}}, nil
		}
		rest := positionals[1:]
		taken, configPath, err := s.applyConfig(command)
		if err != nil {
			return outcome{}, err
		}
		if len(taken) > 0 {
			s.say("Settings from " + configPath + ": " + strings.Join(taken, ", ") + ".")
		}
		needFile := func(what string) (string, error) {
			if len(rest) != 1 {
				return "", usagef(command + " takes one " + what + ".")
			}
			return rest[0], nil
		}
		switch command {
		case "demo":
			if len(rest) > 0 {
				return outcome{}, usagef("demo takes no file.")
			}
			return s.demo()
		case "estimate":
			file, err := needFile("trace file")
			if err != nil {
				return outcome{}, err
			}
			return s.estimate(file)
		case "run":
			file, err := needFile("trace file")
			if err != nil {
				return outcome{}, err
			}
			return s.run(file, false)
		case "map":
			file, err := needFile("trace file")
			if err != nil {
				return outcome{}, err
			}
			return s.run(file, true)
		case "record":
			if len(rest) > 0 {
				return outcome{}, usagef("record takes no file.")
			}
			return s.record()
		case "report":
			file, err := needFile("run.json")
			if err != nil {
				return outcome{}, err
			}
			return s.report(file)
		case "judge":
			file, err := needFile("run.json")
			if err != nil {
				return outcome{}, err
			}
			return s.judgeCommand(file)
		}
		return outcome{}, usagef(`Unknown command "` + command + `". Run metergraph-replay --help.`)
	}()
	if err == nil {
		return emit(result.exitCode, result.json)
	}

	exitCode, code, message := ExitError, "error", "Something went wrong."
	var (
		ce   *cliError
		ue   *usageError
		se   *run.SpecError
		pe   *run.PlanError
		pce  *providers.ProviderConfigError
		path *fs.PathError
	)
	switch {
	case errors.As(err, &ce):
		exitCode, code, message = ce.exitCode, ce.code, ce.message
	case errors.As(err, &ue), errors.As(err, &se), errors.As(err, &pe):
		exitCode, code, message = ExitUsage, "usage", err.Error()
	case errors.As(err, &pce):
		c := configError(pce)
		exitCode, code, message = c.exitCode, c.code, c.message
	case errors.Is(err, fs.ErrNotExist) && errors.As(err, &path):
		exitCode, code, message = ExitUsage, "file_not_found", "No such file: "+path.Path+"."
	case err.Error() != "":
		message = err.Error()
	}
	if !s.asJSON {
		text := "metergraph-replay: " + message + "\n"
		if exitCode == ExitUsage {
			text += "Run metergraph-replay --help for usage.\n"
		}
		io.WriteString(o.Stderr, text)
	}
	var commandValue any
	if hasCommand {
		commandValue = command
	}
	return emit(exitCode, object{{"command", commandValue}, {"error", object{{"code", code}, {"message", message}}}})
}

// ---------------------------------------------------------------------------
// Display

// display is how the report is drawn on stdout.
type display struct {
	width   int
	profile colorprofile.Profile
	paint   term.Paint
	ascii   bool
}

func (s *session) colorMode() term.ColorMode {
	switch {
	case s.v.bool("no-color"):
		return term.ColorNever
	case s.v.bool("color"):
		return term.ColorAlways
	}
	return term.ColorAuto
}

func (s *session) dumb() bool { return s.env("TERM") == "dumb" }

// display is the width, colour and character set for the report on stdout.
func (s *session) display() display {
	tty := s.io.StdoutTTY
	profile := term.DetectProfile(tty, s.environ(), s.colorMode())
	width := s.io.Columns
	if width <= 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(s.env("COLUMNS"))); err == nil && n > 0 {
			width = n
		}
	}
	if width <= 0 {
		width = 100
	}
	return display{
		width:   width,
		profile: profile,
		paint:   term.NewPaint(profile),
		ascii:   s.v.bool("ascii") || !tty || s.dumb(),
	}
}

// interactive: a terminal on both ends that can draw, and nothing asking for plain output.
func (s *session) interactive() bool {
	return s.io.StdinTTY && s.io.StdoutTTY && !s.dumb() && !s.v.bool("no-interactive") && s.env("CI") == ""
}

// shownPath is a path as the user would type it: relative to here when it is below here.
func (s *session) shownPath(path string) string {
	cwd := s.io.Cwd
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return path
		}
	}
	return relativeBelow(cwd, path)
}
