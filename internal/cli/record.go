package cli

// record: a local proxy that forwards an app's model calls to one provider
// and writes each captured call to a trace file.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/metergraph/ezswap/internal/adapters"
	"github.com/metergraph/ezswap/internal/cases"
	"github.com/metergraph/ezswap/internal/record"
	"github.com/metergraph/ezswap/internal/tui"
)

const (
	defaultUpstream   = "https://api.openai.com"
	defaultRecordPort = 8787
)

// insideGit is whether dir is inside a Git work tree, where a trace file
// could be committed by accident.
func insideGit(dir string) bool {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func (s *session) record() (outcome, error) {
	if err := s.checkOptions("record", false); err != nil {
		return outcome{}, err
	}
	upstream, given := s.v.str("upstream")
	if !given {
		upstream = defaultUpstream
	}
	portText, portGiven := s.v.str("port")
	port, err := intOption(portText, portGiven, "port", 0, 65535, defaultRecordPort)
	if err != nil {
		return outcome{}, err
	}
	out, given := s.v.str("out")
	if !given {
		out = "ezswap-out"
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return outcome{}, err
	}
	file := filepath.Join(out, "traces.jsonl")
	shown := s.shownPath(file)

	options := record.Options{Upstream: upstream, Port: port, OutDir: out}
	if err := record.Validate(options); err != nil {
		return outcome{}, &cliError{ExitError, "record_failed", sentence(err.Error())}
	}
	var setAside string
	if s.v.bool("fresh") {
		// A fresh recording starts from an empty file. The previous one is
		// renamed, never deleted: it holds calls that cannot be made again.
		if _, err := os.Stat(file); err == nil {
			now := time.Now
			if s.io.Now != nil {
				now = s.io.Now
			}
			setAside = filepath.Join(out, "traces-"+now().Format("20060102-150405")+".jsonl")
			if _, err := os.Stat(setAside); err == nil {
				return outcome{}, &cliError{ExitError, "record_failed", "Could not set the previous recording aside: " + s.shownPath(setAside) + " exists. Try again in a second."}
			}
			if err := os.Rename(file, setAside); err != nil {
				return outcome{}, &cliError{ExitError, "record_failed", "Could not set the previous recording aside: " + sentence(err.Error())}
			}
		}
	}
	// With --json, stdout carries one object at the end. The lines a person
	// needs while the recorder runs (above all the address, with --port 0)
	// go to stderr instead.
	tell := s.say
	if s.asJSON {
		tell = func(text string) {
			s.mu.Lock()
			defer s.mu.Unlock()
			io.WriteString(s.io.Stderr, text+"\n")
		}
	}
	options.Ready = func(address string) {
		if setAside != "" {
			tell("Started a fresh recording. The previous one is now " + s.shownPath(setAside) + ".")
		}
		tell("Recording model calls to " + shown)
		tell("Forwarding to " + upstream)
		tell("")
		tell("Point your app's model base URL here:")
		tell("  http://" + address + "/v1   (OpenAI SDKs)")
		tell("  http://" + address + "      (Anthropic and Google SDKs)")
		tell("Name the place a call comes from with the header X-Ezswap-Part.")
		if insideGit(out) {
			tell("Note: the trace file holds prompts and responses, and " + s.shownPath(out) + " is inside a Git repository. Keep it out of commits.")
		}
		tell("Press Ctrl-C to stop.")
		tell("")
	}
	options.Events = func(event record.Event) {
		part := event.Part
		if part == "" {
			part = "(unnamed)"
		}
		tell(fmt.Sprintf("  %-12s %-20s %s", event.Outcome, part, event.Path))
	}

	ctx := s.io.Signal
	if ctx == nil {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		// Let go of the signals once stopping has begun, so a second Ctrl-C
		// ends the process instead of waiting out the calls in flight.
		go func() {
			<-ctx.Done()
			stop()
		}()
	}
	var summary record.Summary
	if s.interactive() && !s.asJSON {
		// The live view: it stops the recorder itself on q or Ctrl-C.
		live, stopLive := context.WithCancel(ctx)
		defer stopLive()
		if insideGit(out) {
			tell("Note: the trace file holds prompts and responses, and " + s.shownPath(out) + " is inside a Git repository. Keep it out of commits.")
		}
		d := s.display()
		aside := ""
		if setAside != "" {
			aside = s.shownPath(setAside)
		}
		err = tui.RunRecording(tui.RunOptions{
			Input: s.io.Stdin, Output: s.io.Stderr, Env: s.environ(), Profile: d.profile,
			ASCII: d.ascii, Width: d.width, Signals: s.io.Signal == nil, Now: s.io.Now,
		}, tui.RecordingOptions{File: shown, Upstream: upstream, SetAside: aside, Stop: stopLive},
			func(onReady func(string), onEvent func(record.Event)) error {
				options.Ready, options.Events = onReady, onEvent
				var runErr error
				summary, runErr = record.Run(live, options)
				return runErr
			})
	} else {
		summary, err = record.Run(ctx, options)
	}
	if err != nil {
		if errors.Is(err, record.ErrPortInUse) {
			return outcome{}, &cliError{ExitError, "record_failed", fmt.Sprintf("Port %d is already in use. Pick another with --port, or --port 0 for a free one.", port)}
		}
		return outcome{}, &cliError{ExitError, "record_failed", sentence(err.Error())}
	}

	// Read the file back the way estimate does, so the count is of cases a map can use.
	replayable, notReplayable := 0, map[string]int{}
	if summary.Recorded > 0 {
		if calls, err := adapters.ReadTraces(file, adapters.ReadTracesOptions{}); err == nil {
			for _, call := range calls {
				kase := cases.PrepareCase(call, cases.PrepareCaseOptions{})
				if kase.NotReplayable == nil {
					replayable++
				} else {
					notReplayable[string(*kase.NotReplayable)]++
				}
			}
		}
	}
	s.say("")
	s.say(fmt.Sprintf("Recorded %s to %s.", plural(summary.Recorded, "call"), shown))
	for _, name := range sortedKeys(summary.ByPart) {
		s.say(fmt.Sprintf("  %-20s %d", name, summary.ByPart[name]))
	}
	if len(summary.Skipped) > 0 {
		s.say("Forwarded and not recorded:")
		for _, reason := range sortedKeys(summary.Skipped) {
			s.say(fmt.Sprintf("  %-20s %d", reason, summary.Skipped[reason]))
		}
	}
	if summary.Recorded > 0 {
		s.say(fmt.Sprintf("The file now holds %s a replay can reproduce.", plural(replayable, "case")))
		s.say("Next:")
		s.say("  ezswap estimate " + shown + " --map --models <list>")
	}
	return outcome{ExitOK, object{
		{"command", "record"},
		{"file", file},
		{"recorded", summary.Recorded},
		{"byPart", summary.ByPart},
		{"skipped", summary.Skipped},
		{"forwarded", summary.Forwarded},
		{"replayable", replayable},
		{"notReplayable", notReplayable},
	}}, nil
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// sentence is an error message as a line of output: capitalised, ending in a stop.
func sentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "The recorder failed."
	}
	text = strings.ToUpper(text[:1]) + text[1:]
	if !strings.HasSuffix(text, ".") {
		text += "."
	}
	return text
}
