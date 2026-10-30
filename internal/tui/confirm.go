// Package tui holds the CLI's small Bubble Tea views: the spend
// confirmation and the live progress of a run. Each is a plain model that
// tests drive through Update; Ask and the run's program wire them to the
// terminal.
package tui

import (
	"io"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/metergraph/replay/internal/report/term"
)

// SignalMsg is SIGINT or SIGTERM, forwarded to a view (Bubble Tea's own
// signal handler is off, so a signal never ends a program behind the
// view's back).
type SignalMsg struct{ Name string }

// Confirm asks a yes or no question; no is the default. y answers yes;
// n, enter, esc, Ctrl-C and Ctrl-D answer no.
type Confirm struct {
	Question string
	Paint    term.Paint
	// Answered says the question has its answer, in Yes.
	Answered bool
	Yes      bool
}

var _ tea.Model = Confirm{}

func (m Confirm) Init() tea.Cmd { return nil }

func (m Confirm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.Answered {
		return m, nil
	}
	switch msg := msg.(type) {
	case SignalMsg:
		return m.answer(false)
	case tea.KeyPressMsg:
		k := msg.Key()
		if k.Mod&tea.ModCtrl != 0 {
			if k.Code == 'c' || k.Code == 'd' {
				return m.answer(false)
			}
			return m, nil
		}
		switch k.Code {
		case tea.KeyEnter, tea.KeyKpEnter, tea.KeyEscape:
			return m.answer(false)
		}
		switch k.Text {
		case "y", "Y":
			return m.answer(true)
		case "n", "N":
			return m.answer(false)
		}
	}
	return m, nil
}

func (m Confirm) answer(yes bool) (tea.Model, tea.Cmd) {
	m.Answered, m.Yes = true, yes
	return m, tea.Quit
}

func (m Confirm) View() tea.View {
	text := m.Question
	if m.Answered {
		answer := "no"
		if m.Yes {
			answer = "yes"
		}
		text += m.Paint.Bold(answer) + "\n"
	}
	return tea.NewView(text)
}

// AskOptions say where to ask.
type AskOptions struct {
	Input  io.Reader
	Output io.Writer
	// In os.Environ form.
	Env     []string
	Profile colorprofile.Profile
	// Forward SIGINT and SIGTERM (as a no). Off in tests.
	Signals bool
}

// Ask shows the question on the terminal and waits for y or n.
func Ask(question string, o AskOptions) (bool, error) {
	model := Confirm{Question: question, Paint: term.NewPaint(o.Profile)}
	p := tea.NewProgram(model, programOptions(o.Input, o.Output, o.Env, o.Profile)...)
	if o.Signals {
		stop := ForwardSignals(p)
		defer stop()
	}
	final, err := p.Run()
	if err != nil {
		return false, err
	}
	answer := final.(Confirm)
	return answer.Answered && answer.Yes, nil
}

func programOptions(in io.Reader, out io.Writer, env []string, profile colorprofile.Profile) []tea.ProgramOption {
	options := []tea.ProgramOption{
		tea.WithoutSignalHandler(),
		// Colour is decided by the paint; the renderer keeps what it is given.
		tea.WithColorProfile(max(profile, colorprofile.Ascii)),
	}
	if in != nil {
		options = append(options, tea.WithInput(in))
	}
	if out != nil {
		options = append(options, tea.WithOutput(out))
	}
	if env != nil {
		options = append(options, tea.WithEnvironment(env))
	}
	return options
}
