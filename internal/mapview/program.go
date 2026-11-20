package mapview

// The thin layer that owns the terminal: a Bubble Tea model over the pure
// reducer and frame, on the alternate screen. Bubble Tea restores the
// terminal however the explorer ends.

import (
	"context"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/metergraph/ezswap/internal/explore"
	"github.com/metergraph/ezswap/internal/report/term"
)

// Reasons the explorer ended, as the run explorer names them.
const (
	ReasonQuit    = explore.ReasonQuit
	ReasonSIGINT  = explore.ReasonSIGINT
	ReasonSIGTERM = explore.ReasonSIGTERM
)

// Model is the map explorer as a Bubble Tea model.
type Model struct {
	State  State
	Paint  term.Paint
	ASCII  bool
	Reason string
	// Opens a file in the default browser; nil uses the system's opener.
	Open func(path string) error
}

var _ tea.Model = Model{}

// NewModel is the explorer at a size.
func NewModel(d *Data, paint term.Paint, columns, rows int) Model {
	return Model{State: NewState(d, columns, rows), Paint: paint, ASCII: d.ASCII}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.State = Resize(m.State, msg.Width, msg.Height)
	case explore.SignalMsg:
		m.Reason = msg.Name
		return m, tea.Quit
	case tea.KeyPressMsg:
		for _, key := range explore.KeyNames(msg) {
			m.State = Reduce(m.State, key)
			if m.State.Quit {
				m.Reason = ReasonQuit
				return m, tea.Quit
			}
		}
		if m.State.OpenPage {
			m.State.OpenPage = false
			open := m.Open
			if open == nil {
				open = OpenInBrowser
			}
			path := m.State.Data.HTMLPath
			return m, func() tea.Msg {
				open(path) // a browser that does not start leaves the explorer as it is
				return nil
			}
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

// OpenInBrowser opens a file with the system's default handler.
func OpenInBrowser(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

// RunOptions say where the explorer draws.
type RunOptions struct {
	Input   io.Reader
	Output  io.Writer
	Env     []string
	Profile colorprofile.Profile
	// The terminal's size before the first resize message.
	Columns, Rows int
	// Turn SIGINT and SIGTERM into the explorer's end. Off in tests.
	Signals bool
	// Opens the HTML page; nil opens Data.HTMLPath in the default browser.
	Open func(path string) error
}

// Run opens the explorer and returns how it ended.
func Run(ctx context.Context, d *Data, o RunOptions) (string, error) {
	model := NewModel(d, term.NewPaint(o.Profile), max(1, o.Columns), max(1, o.Rows))
	model.Open = o.Open
	options := []tea.ProgramOption{
		tea.WithContext(ctx),
		tea.WithColorProfile(max(o.Profile, colorprofile.Ascii)),
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
					p.Send(explore.SignalMsg{Name: name})
				case <-done:
					return
				}
			}
		}()
		defer func() { signal.Stop(ch); close(done) }()
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
