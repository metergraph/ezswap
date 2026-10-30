package explore

// The thin layer that owns the terminal: a Bubble Tea model over the pure
// reducer and frame, on the alternate screen. Keys go through Reduce, a
// resize through Resize, and View draws ScreenLines. Bubble Tea restores the
// terminal however the explorer ends (q, Ctrl-C, a signal, a panic).

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/metergraph/replay/internal/report/term"
)

// Reasons the explorer ended.
const (
	ReasonQuit    = "quit"
	ReasonSIGINT  = "SIGINT"
	ReasonSIGTERM = "SIGTERM"
)

// SignalMsg tells the explorer a signal arrived: it ends with the signal's name as its reason.
type SignalMsg struct{ Name string }

// Model is the explorer as a Bubble Tea model.
type Model struct {
	State  State
	Paint  term.Paint
	ASCII  bool
	Reason string
}

var _ tea.Model = Model{}

// NewModel is the explorer on a tab at a size.
func NewModel(data *Data, paint term.Paint, columns, rows, tab int) Model {
	return Model{State: NewState(data, columns, rows, tab), Paint: paint, ASCII: data.ASCII}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.State = Resize(m.State, msg.Width, msg.Height)
	case SignalMsg:
		m.Reason = msg.Name
		return m, tea.Quit
	case tea.KeyPressMsg:
		return m.press(KeyNames(msg)...)
	case tea.PasteMsg:
		if m.State.Typing {
			var keys []Key
			for _, r := range msg.Content {
				if r >= ' ' {
					keys = append(keys, string(r))
				}
			}
			return m.press(keys...)
		}
	}
	return m, nil
}

func (m Model) press(keys ...Key) (tea.Model, tea.Cmd) {
	for _, key := range keys {
		m.State = Reduce(m.State, key)
		if m.State.Quit {
			m.Reason = ReasonQuit
			return m, tea.Quit
		}
	}
	return m, nil
}

// Frame is the screen as text.
func (m Model) Frame() string {
	return Render(m.State, Screen{Columns: m.State.Columns, Rows: m.State.Rows, Paint: m.Paint, ASCII: m.ASCII})
}

func (m Model) View() tea.View {
	v := tea.NewView(m.Frame())
	v.AltScreen = true
	return v
}

// KeyNames are the reducer's names for a key press: none for a key it does not read.
func KeyNames(msg tea.KeyPressMsg) []Key {
	k := msg.Key()
	if k.Mod&tea.ModCtrl != 0 {
		switch k.Code {
		case 'c':
			return []Key{"ctrl-c"}
		case 'd':
			// Ctrl-D ends the explorer like q, as end of input does.
			return []Key{"q"}
		}
		return nil
	}
	switch k.Code {
	case tea.KeyUp, tea.KeyKpUp:
		return []Key{"up"}
	case tea.KeyDown, tea.KeyKpDown:
		return []Key{"down"}
	case tea.KeyLeft, tea.KeyKpLeft:
		return []Key{"left"}
	case tea.KeyRight, tea.KeyKpRight:
		return []Key{"right"}
	case tea.KeyPgUp, tea.KeyKpPgUp:
		return []Key{"pageup"}
	case tea.KeyPgDown, tea.KeyKpPgDown:
		return []Key{"pagedown"}
	case tea.KeyHome, tea.KeyKpHome:
		return []Key{"home"}
	case tea.KeyEnd, tea.KeyKpEnd:
		return []Key{"end"}
	case tea.KeyEnter, tea.KeyKpEnter:
		return []Key{"enter"}
	case tea.KeyEscape:
		return []Key{"escape"}
	case tea.KeyBackspace:
		return []Key{"backspace"}
	case tea.KeyTab:
		if k.Mod&tea.ModShift != 0 {
			return []Key{"left"}
		}
		return []Key{"tab"}
	case tea.KeySpace:
		return []Key{" "}
	}
	if k.Text == "" || k.Mod&tea.ModAlt != 0 {
		return nil
	}
	var keys []Key
	for _, r := range k.Text {
		if r >= ' ' && r != 0x7f {
			keys = append(keys, string(r))
		}
	}
	return keys
}

// RunOptions say where the explorer draws.
type RunOptions struct {
	Input  io.Reader
	Output io.Writer
	// The environment, in os.Environ form.
	Env     []string
	Profile colorprofile.Profile
	// The terminal's size before the first resize message.
	Columns, Rows int
	// The tab to open on.
	Tab int
	// Turn SIGINT and SIGTERM into the explorer's end, with the signal's name
	// as the reason. Off in tests.
	Signals bool
}

// Run opens the explorer and returns how it ended: ReasonQuit, ReasonSIGINT
// or ReasonSIGTERM.
func Run(ctx context.Context, data *Data, o RunOptions) (string, error) {
	model := NewModel(data, term.NewPaint(o.Profile), max(1, o.Columns), max(1, o.Rows), o.Tab)
	// Colour is decided by the paint; the renderer only keeps what it wrote.
	profile := max(o.Profile, colorprofile.Ascii)
	options := []tea.ProgramOption{
		tea.WithContext(ctx),
		tea.WithColorProfile(profile),
		tea.WithoutSignalHandler(),
	}
	if o.Input != nil {
		options = append(options, tea.WithInput(o.Input))
	}
	if o.Output != nil {
		options = append(options, tea.WithOutput(o.Output))
	}
	if o.Env != nil {
		options = append(options, tea.WithEnvironment(o.Env))
	}
	p := tea.NewProgram(model, options...)
	if o.Signals {
		stop := forwardSignals(p)
		defer stop()
	}
	final, err := p.Run()
	if err != nil {
		return "", err
	}
	reason := final.(Model).Reason
	if reason == "" {
		reason = ReasonQuit
	}
	return reason, nil
}

// forwardSignals sends SIGINT and SIGTERM to the program as a SignalMsg.
func forwardSignals(p *tea.Program) func() {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-ch:
				name := ReasonSIGINT
				if s == syscall.SIGTERM {
					name = ReasonSIGTERM
				}
				p.Send(SignalMsg{Name: name})
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}
