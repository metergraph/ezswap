package tui

// The live view of a recording: where to point the app, calls captured per
// part with a bar, the latest calls, and how long it has run. Fed the
// recorder's events in order. q, Esc or Ctrl-C (keys here: the terminal is
// in raw mode) and SIGINT or SIGTERM stop the recorder; the view ends when
// the recorder returns (DoneMsg), and the CLI prints the summary.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/metergraph/replay/internal/record"
	"github.com/metergraph/replay/internal/report/term"
)

// RecordReadyMsg says the recorder listens at an address.
type RecordReadyMsg struct{ Address string }

// RecordEventMsg is one call the recorder handled.
type RecordEventMsg struct{ Event record.Event }

const recentCalls = 6

type recentCall struct {
	at    time.Time
	event record.Event
}

// Recording is the live view's model.
type Recording struct {
	Paint term.Paint
	ASCII bool
	Width int

	file, upstream, setAside string
	address                  string
	start                    time.Time
	now                      func() time.Time
	elapsed                  time.Duration
	byPart                   map[string]int
	skipped                  map[string]int
	recorded                 int
	recent                   []recentCall
	stop                     context.CancelFunc
	stopping                 bool
	finished                 bool
	spinner                  spinner.Model
}

// RecordingOptions configure NewRecording.
type RecordingOptions struct {
	Paint term.Paint
	ASCII bool
	Width int
	// The trace file and the upstream, as shown.
	File, Upstream string
	// The previous recording --fresh set aside, as shown, or "".
	SetAside string
	// Stops the recorder.
	Stop context.CancelFunc
	Now  func() time.Time
}

// NewRecording is the view before the recorder listens.
func NewRecording(o RecordingOptions) Recording {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	if o.ASCII {
		s.Spinner = spinner.Line
	}
	return Recording{
		Paint: o.Paint, ASCII: o.ASCII, Width: o.Width,
		file: term.CleanLine(o.File), upstream: term.CleanLine(o.Upstream), setAside: term.CleanLine(o.SetAside),
		start: now(), now: now, byPart: map[string]int{}, skipped: map[string]int{},
		stop: o.Stop, spinner: s,
	}
}

func (m Recording) Init() tea.Cmd { return tea.Batch(tick(), m.spinner.Tick) }

func (m Recording) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case RecordReadyMsg:
		m.address = term.CleanLine(msg.Address)
	case RecordEventMsg:
		m.apply(msg.Event)
	case DoneMsg:
		m.finished = true
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.Width = msg.Width
	case tickMsg:
		m.elapsed = m.now().Sub(m.start)
		return m, tick()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case SignalMsg:
		m.halt()
	case tea.KeyPressMsg:
		k := msg.Key()
		if (k.Mod&tea.ModCtrl != 0 && k.Code == 'c') || k.Code == tea.KeyEscape || k.Text == "q" {
			m.halt()
		}
	}
	return m, nil
}

// halt asks the recorder to stop: calls in flight finish first.
func (m *Recording) halt() {
	if !m.stopping {
		m.stopping = true
		if m.stop != nil {
			m.stop()
		}
	}
}

func (m *Recording) apply(e record.Event) {
	if e.Outcome == "recorded" {
		m.recorded++
		part := e.Part
		if part == "" {
			part = "(unnamed)"
		}
		m.byPart[part]++
	} else {
		m.skipped[e.Outcome]++
	}
	m.recent = append(m.recent, recentCall{at: m.now(), event: e})
	if len(m.recent) > recentCalls {
		m.recent = m.recent[len(m.recent)-recentCalls:]
	}
}

func (m Recording) g(fancy, plain string) string {
	if m.ASCII {
		return plain
	}
	return fancy
}

// Frame is the live view as text.
func (m Recording) Frame() string {
	p := m.Paint
	width := m.Width
	if width <= 0 {
		width = 80
	}
	width = max(40, width)
	var lines []string
	title := p.Brand(m.g("▁▃▅▇", "::")) + " " + p.Bold("replay record")
	if m.stopping {
		title += "  " + p.Muted("stopping: the calls in flight finish first")
	} else {
		title += "  " + p.Muted(m.spinner.View()+" recording "+formatElapsed(m.elapsed))
	}
	lines = append(lines, title)
	if m.setAside != "" {
		lines = append(lines, p.Muted("Started fresh. The previous recording is now "+m.setAside+"."))
	}
	lines = append(lines, p.Muted("Writing ")+m.file+p.Muted(", forwarding to ")+m.upstream, "")
	if m.address == "" {
		lines = append(lines, p.Muted("Starting..."))
	} else {
		lines = append(lines,
			"Point your app's model base URL at",
			"  "+p.Bold("http://"+m.address+"/v1")+p.Muted("   OpenAI SDKs"),
			"  "+p.Bold("http://"+m.address)+p.Muted("      Anthropic and Google SDKs"),
			p.Muted("and name each call site with the header X-Replay-Part."),
		)
	}
	lines = append(lines, "")
	if m.recorded == 0 && len(m.skipped) == 0 {
		lines = append(lines, p.Muted("Waiting for the first call."))
	} else {
		names := make([]string, 0, len(m.byPart))
		most, nameW := 0, 8
		for name, n := range m.byPart {
			names = append(names, name)
			most = max(most, n)
			nameW = max(nameW, term.Width(term.CleanLine(name)))
		}
		sort.Strings(names)
		nameW = min(nameW, 24)
		barW := max(4, min(30, width-nameW-12))
		lines = append(lines, p.Muted(term.PadEnd("PART", nameW)+"  CALLS"))
		for _, name := range names {
			n := m.byPart[name]
			bar := p.Bar(term.Hbar(float64(n), float64(max(1, most)), barW, m.ASCII))
			lines = append(lines, term.PadEnd(term.Truncate(term.CleanLine(name), nameW, m.g("…", "~")), nameW)+"  "+term.PadStart(fmt.Sprint(n), 5)+"  "+bar)
		}
		summary := plural(m.recorded, "call") + " recorded"
		if len(m.skipped) > 0 {
			var parts []string
			for _, reason := range sortedReasons(m.skipped) {
				parts = append(parts, fmt.Sprintf("%d %s", m.skipped[reason], term.CleanLine(reason)))
			}
			summary += p.Muted(", not recorded: " + strings.Join(parts, ", "))
		}
		lines = append(lines, "", summary, "", p.Muted("LATEST"))
		for i := len(m.recent) - 1; i >= 0; i-- {
			r := m.recent[i]
			part := r.event.Part
			if part == "" {
				part = "(unnamed)"
			}
			outcome := term.PadEnd(term.CleanLine(r.event.Outcome), 12)
			if r.event.Outcome == "recorded" {
				outcome = p.Hue("#7cc9a0", outcome)
			} else {
				outcome = p.Hue("#f08a60", outcome)
			}
			lines = append(lines, p.Muted(r.at.Format("15:04:05"))+"  "+outcome+" "+term.PadEnd(term.Truncate(term.CleanLine(part), nameWidth(m), m.g("…", "~")), nameWidth(m))+"  "+p.Muted(term.CleanLine(r.event.Path)))
		}
	}
	lines = append(lines, "", p.Muted("q or Ctrl-C stops recording. The summary follows."))
	for i, l := range lines {
		if m.ASCII {
			l = term.ToAscii(l)
		}
		lines[i] = term.Fit(l, width, term.AlignLeft, m.g("…", "~"))
	}
	return strings.Join(lines, "\n")
}

func nameWidth(m Recording) int {
	w := 8
	for name := range m.byPart {
		w = max(w, term.Width(term.CleanLine(name)))
	}
	return min(w, 24)
}

func sortedReasons(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func (m Recording) View() tea.View {
	if m.finished {
		return tea.NewView("")
	}
	return tea.NewView(m.Frame() + "\n")
}

// Recorded is the calls recorded so far, for tests.
func (m Recording) Recorded() int { return m.recorded }

// Stopping is whether the view asked the recorder to stop.
func (m Recording) Stopping() bool { return m.stopping }

// RunRecording runs the recorder behind the live view. execute starts the
// recorder with the callbacks it is given and returns when it stops; stop
// is how the view stops it.
func RunRecording(o RunOptions, view RecordingOptions, execute func(onReady func(string), onEvent func(record.Event)) error) error {
	view.Paint, view.ASCII, view.Width = term.NewPaint(o.Profile), o.ASCII, o.Width
	if view.Now == nil {
		view.Now = o.Now
	}
	p := tea.NewProgram(NewRecording(view), programOptions(o.Input, o.Output, o.Env, o.Profile)...)
	if o.Signals {
		stop := ForwardSignals(p)
		defer stop()
	}
	done := make(chan error, 1)
	go func() {
		err := execute(
			func(address string) { p.Send(RecordReadyMsg{Address: address}) },
			func(e record.Event) { p.Send(RecordEventMsg{Event: e}) },
		)
		done <- err
		p.Send(DoneMsg{})
	}()
	// When the view cannot draw, the recorder goes on without it.
	_, _ = p.Run()
	return <-done
}
