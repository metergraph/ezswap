package cli

// Telemetry: the one question the guided workflow asks, the `telemetry`
// command that changes the answer, and the event a command sends when it
// ends. See internal/telemetry and docs/telemetry.md.

import (
	"context"
	"runtime"
	"slices"
	"time"

	"github.com/metergraph/ezswap/internal/flow"
	"github.com/metergraph/ezswap/internal/pricing"
	"github.com/metergraph/ezswap/internal/run"
	"github.com/metergraph/ezswap/internal/telemetry"
)

// The commands that report. help, --version and telemetry itself do not.
var reported = []string{"guided", "demo", "estimate", "map", "run", "record", "report", "judge"}

const telemetryDoc = "https://github.com/metergraph/ezswap/blob/main/docs/telemetry.md"

func (s *session) now() time.Time {
	if s.io.Now != nil {
		return s.io.Now()
	}
	return time.Now()
}

// canReport is whether this build and machine have somewhere to send an
// event and somewhere to keep the answer.
func (s *session) canReport() bool {
	return s.io.TelemetryKey != "" && s.io.TelemetryURL != "" && s.io.ConfigDir != ""
}

// askTelemetry asks, once, whether to share anonymous usage. "No" is the
// highlighted answer. Quitting at the question answers nothing: quit is
// true, nothing is sent, and the next guided workflow asks again. It asks
// nothing when the build cannot report or the environment has said no.
func (s *session) askTelemetry(o flow.Options) (quit bool, err error) {
	if !s.canReport() || telemetry.OffBy(s.io.Env) != "" {
		return false, nil
	}
	if _, answered := telemetry.Load(s.io.ConfigDir); answered {
		return false, nil
	}
	menu, err := flow.RunMenu(flow.NewMenu("", "Share anonymous usage with ezswap's maintainers?", []string{
		"It is off unless you turn it on. If you do, each command reports that it ran: the command, how it ended, how many parts and cases it covered, and the names of public models compared.",
		"It never reports a prompt, a reply, a key, a file path, a part's name or a private model's name. Change your answer at any time with ezswap telemetry on or ezswap telemetry off. Every field: " + telemetryDoc,
	}, []flow.Item{
		{Title: "No, send nothing", Detail: "ezswap will not ask again."},
		{Title: "Yes, share anonymous usage", Detail: "Thank you. It shows which parts of ezswap are used."},
	}), o)
	if err != nil || menu.Cancel {
		return true, err
	}
	// A settings directory that cannot be written means no answer is kept
	// and nothing is sent; it is not a reason to stop the workflow.
	telemetry.Answer(s.io.ConfigDir, menu.Chosen == 1, s.now())
	return false, nil
}

// telemetryCommand is `ezswap telemetry [on|off]`: change the answer, or
// with no argument say what it is.
func (s *session) telemetryCommand(rest []string) (outcome, error) {
	if len(rest) > 1 || (len(rest) == 1 && rest[0] != "on" && rest[0] != "off") {
		return outcome{}, usagef("telemetry takes on, off or nothing.")
	}
	if len(rest) == 1 {
		if s.io.ConfigDir == "" {
			return outcome{}, &cliError{ExitError, "no_settings_directory", "This machine has no settings directory to keep the answer in, so telemetry stays off."}
		}
		if _, err := telemetry.Answer(s.io.ConfigDir, rest[0] == "on", s.now()); err != nil {
			return outcome{}, &cliError{ExitError, "settings_not_written", "The answer could not be saved, so telemetry stays off."}
		}
	}
	settings, _ := telemetry.Load(s.io.ConfigDir)
	offBy := telemetry.OffBy(s.io.Env)
	sending := settings.Enabled && s.canReport() && offBy == ""
	switch {
	case !settings.Enabled:
		s.say("Telemetry is off. ezswap sends nothing about how it is used.")
	case !s.canReport():
		s.say("Telemetry is on, but this build has nowhere to send it, so nothing is sent.")
	case offBy != "":
		s.say("Telemetry is on, but " + offBy + " is set, so nothing is sent.")
	default:
		s.say("Telemetry is on. Each command reports that it ran, with no prompt, reply, key, path or private name.")
	}
	s.say("Change it with ezswap telemetry on or ezswap telemetry off. Every field: " + telemetryDoc)
	return outcome{ExitOK, object{{"command", "telemetry"}, {"enabled", settings.Enabled}, {"sending", sending}}}, nil
}

// publicModel is a model's name if the catalog shipped inside ezswap can
// price it, else Other. The run's own catalog is not asked: the Pricing API,
// or a pricing service the person pointed ezswap at, may have taught it a
// private deployment's name.
func publicModel(model string) string {
	if model != "" && pricing.Bundled(model) {
		return model
	}
	return telemetry.Other
}

// notePlan records what a plan covers for the event.
func (s *session) notePlan(plan *run.RunPlan, prices *pricing.CatalogPricing) {
	s.event.Models = nil
	for _, t := range plan.Treatments {
		if name := publicModel(t.Model); !slices.Contains(s.event.Models, name) {
			s.event.Models = append(s.event.Models, name)
		}
	}
	if production := productionModel(plan.Cases, prices); production != "" {
		s.event.Production = publicModel(production)
	}
	s.event.Parts = len(plan.Parts)
	s.event.Cases = plan.Estimate.Replayable
	s.event.Judge = s.v.bool("judge")
}

// report sends the command's event if the person turned telemetry on. It
// waits at most telemetry.Timeout and says nothing, whatever happens.
func (s *session) reportUsage(command string, exitCode int, errorCode string, started time.Time) {
	if !slices.Contains(reported, command) || !s.canReport() || telemetry.OffBy(s.io.Env) != "" {
		return
	}
	settings, _ := telemetry.Load(s.io.ConfigDir)
	if !settings.Enabled {
		return
	}
	e := s.event
	e.Command, e.Version, e.OS, e.Arch = command, Version, runtime.GOOS, runtime.GOARCH
	e.ExitCode, e.Error = exitCode, errorCode
	e.Seconds = int(s.now().Sub(started).Seconds())
	e.Interactive = s.interactive()
	telemetry.Send(context.Background(), s.io.TelemetryURL, s.io.TelemetryKey, settings.ID, e, s.now())
}
