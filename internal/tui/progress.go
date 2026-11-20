package tui

// The live view of a run: calls done of the total, spend against the
// budget, each model's progress, failures, and the time since the start,
// with a spinner and a bar. Fed the run's events in order.
//
// Ctrl-C (a key here: the terminal is in raw mode) and SIGINT keep the
// run's own semantics: the first stops new calls and lets the calls in
// flight finish, the second cancels those too. The view never ends the
// program on a key; it ends when the run returns (DoneMsg).

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/metergraph/ezswap/internal/jsnum"
	"github.com/metergraph/ezswap/internal/report"
	"github.com/metergraph/ezswap/internal/report/term"
	"github.com/metergraph/ezswap/internal/run"
	"github.com/metergraph/ezswap/internal/types"
)

// USD is dollars: four places under a cent, two otherwise.
func USD(value float64) string {
	if value == 0 {
		return "$0.00"
	}
	if value > -0.01 && value < 0.01 {
		return "$" + jsnum.ToFixed(value, 4)
	}
	return "$" + jsnum.ToFixed(value, 2)
}

// StopReasons say why a run stopped early, to follow "Stopped early: ".
var StopReasons = map[string]string{
	run.StopBudget:              "the budget could not cover the next case",
	run.StopInterrupted:         "it was interrupted",
	run.StopReservationExceeded: "a call cost more than it reserved, so no more calls were sent",
}

// EventMsg is one event of the run.
type EventMsg struct{ Event run.Event }

// DoneMsg says the run returned: the view ends.
type DoneMsg struct{}

type tickMsg time.Time

// row is one model's progress.
type row struct {
	letter string
	hue    string
	label  string
	total  int
	done   int
	failed int
}

// Progress is the live view's model.
type Progress struct {
	Paint term.Paint
	ASCII bool
	// Width of the terminal.
	Width int

	rows []row
	// Arm id to its row: each treatment, and both floor replays to one row.
	armRow map[string]int

	total    int
	done     int
	spent    float64
	reserved float64
	budget   float64
	inFlight int
	errors   map[string]int
	codes    []string

	// stopping: the first Ctrl-C; cancelling: the second.
	stopping   bool
	cancelling bool
	// Why the run stopped, from its stopped event.
	stopped  string
	finished bool

	soft, hard context.CancelFunc
	now        func() time.Time
	start      time.Time
	elapsed    time.Duration

	spinner spinner.Model
	bar     progress.Model
}

var _ tea.Model = Progress{}

// ProgressOptions configure NewProgress.
type ProgressOptions struct {
	Paint term.Paint
	ASCII bool
	Width int
	// Stop new calls (the first Ctrl-C) and cancel the calls in flight (the second).
	Soft, Hard context.CancelFunc
	// The clock for the elapsed time; time.Now when nil.
	Now func() time.Time
}

// NewProgress is the view of a planned run before its first event.
func NewProgress(plan *run.RunPlan, o ProgressOptions) Progress {
	now := o.Now
	if now == nil {
		now = time.Now
	}
	m := Progress{
		Paint:  o.Paint,
		ASCII:  o.ASCII,
		Width:  o.Width,
		armRow: map[string]int{},
		errors: map[string]int{},
		budget: plan.Settings.BudgetUSD,
		soft:   o.Soft,
		hard:   o.Hard,
		now:    now,
		start:  now(),
	}
	if m.Width <= 0 {
		m.Width = 80
	}
	replayable := plan.Estimate.Replayable
	for i, t := range plan.Treatments {
		m.armRow[t.ID] = len(m.rows)
		m.rows = append(m.rows, row{letter: report.Letter(i), hue: term.TreatmentHues[i%len(term.TreatmentHues)], label: term.CleanLine(t.Label), total: replayable})
	}
	if plan.FloorEndpoint != "" {
		production := "production"
		for _, c := range plan.Cases {
			if c.NotReplayable == nil && c.Model != nil && *c.Model != "" {
				production = term.CleanLine(*c.Model)
				break
			}
		}
		for _, arm := range run.FloorArms {
			m.armRow[arm] = len(m.rows)
		}
		m.rows = append(m.rows, row{label: "itself (" + production + ", twice)", total: 2 * replayable})
	}
	for _, r := range m.rows {
		m.total += r.total
	}
	frames := spinner.MiniDot
	if m.ASCII {
		frames = spinner.Line
	}
	m.spinner = spinner.New(spinner.WithSpinner(frames), spinner.WithStyle(lipgloss.NewStyle()))
	full, empty := '█', '░'
	if m.ASCII {
		full, empty = '#', '-'
	}
	options := []progress.Option{progress.WithoutPercentage(), progress.WithFillCharacters(full, empty)}
	if o.Paint.Color {
		options = append(options, progress.WithColors(lipgloss.Color(term.Design.Green300)))
	}
	m.bar = progress.New(options...)
	if !o.Paint.Color {
		m.bar.FullColor, m.bar.EmptyColor = nil, nil
	}
	return m
}

func (m Progress) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tick())
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Progress) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case EventMsg:
		m.apply(msg.Event)
	case DoneMsg:
		m.finished = true
		m.elapsed = m.now().Sub(m.start)
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
		if msg.Name == "SIGTERM" {
			m.cancel()
		} else {
			m.interrupt()
		}
	case tea.KeyPressMsg:
		if k := msg.Key(); k.Mod&tea.ModCtrl != 0 && k.Code == 'c' {
			m.interrupt()
		}
	}
	return m, nil
}

// interrupt is Ctrl-C: the first stops new calls, the second cancels the calls in flight.
func (m *Progress) interrupt() {
	if !m.stopping {
		m.stopping = true
		if m.soft != nil {
			m.soft()
		}
		return
	}
	m.cancel()
}

func (m *Progress) cancel() {
	m.stopping = true
	if !m.cancelling {
		m.cancelling = true
		if m.soft != nil {
			m.soft()
		}
		if m.hard != nil {
			m.hard()
		}
	}
}

func (m *Progress) apply(e run.Event) {
	p := e.Progress
	m.total, m.done = p.Total, p.Done
	m.spent, m.reserved, m.budget, m.inFlight = p.SpentUSD, p.ReservedUSD, p.BudgetUSD, p.InFlight
	switch e.Kind {
	case run.EventCallFinished:
		if i, ok := m.armRow[e.Arm]; ok {
			m.rows[i].done++
			if e.Replay != nil && e.Replay.Status == "failed" {
				m.rows[i].failed++
			}
		}
		if e.Replay != nil && e.Replay.Status == "failed" {
			code := "failed"
			if e.Replay.Error != nil && e.Replay.Error.Code != "" {
				code = term.CleanLine(e.Replay.Error.Code)
			}
			if m.errors[code] == 0 {
				m.codes = append(m.codes, code)
			}
			m.errors[code]++
		}
	case run.EventStopped:
		if e.Stopped != nil {
			m.stopped = *e.Stopped
		}
	case run.EventFinished:
		if e.Stopped != nil {
			m.stopped = *e.Stopped
		}
	}
}

func plural(n int, one string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %ss", n, one)
}

func clock(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// Frame is the view as text.
func (m Progress) Frame() string {
	p := m.Paint
	w := max(20, m.Width)
	var lines []string

	verb := "Replaying"
	switch {
	case m.finished:
		verb = "Replayed"
	case m.cancelling:
		verb = "Cancelling"
	case m.stopping || m.stopped != "":
		verb = "Stopping"
	}
	spin := m.spinner.View()
	if m.finished {
		spin = " "
	}
	share := 0.0
	if m.total > 0 {
		share = float64(m.done) / float64(m.total)
	}
	head := spin + " " + p.Bold(verb) + "  " + fmt.Sprintf("%d of %s", m.done, plural(m.total, "call"))
	tail := "  " + fmt.Sprintf("%3d%%", int(share*100)) + "  " + p.Muted(clock(m.elapsed))
	bar := m.bar
	bar.SetWidth(max(10, min(40, w-term.Width(head)-term.Width(tail)-2)))
	lines = append(lines, head+"  "+bar.ViewAs(share)+tail)

	money := "  Spent " + USD(m.spent) + " of " + USD(m.budget)
	if m.reserved > 0 {
		money += ", " + USD(m.reserved) + " reserved"
	}
	if m.inFlight > 0 {
		money += ", " + plural(m.inFlight, "call") + " in flight"
	}
	lines = append(lines, money)

	labelW := 0
	for _, r := range m.rows {
		labelW = max(labelW, term.Width(r.label)+len(r.letter)+1)
	}
	labelW = min(labelW, max(12, w-24))
	for _, r := range m.rows {
		name := r.label
		if r.letter != "" {
			name = p.Hue(r.hue, p.Bold(r.letter)) + " " + p.Hue(r.hue, r.label)
		} else {
			name = p.Floor(name)
		}
		line := "  " + term.Fit(name, labelW, term.AlignLeft, m.ell()) + "  " + fmt.Sprintf("%d of %d", r.done, r.total)
		if r.failed > 0 {
			line += "  " + p.Emph(fmt.Sprintf("%d failed", r.failed))
		}
		lines = append(lines, line)
	}

	if len(m.codes) > 0 {
		parts := make([]string, len(m.codes))
		for i, code := range m.codes {
			parts[i] = fmt.Sprintf("%s (%d)", code, m.errors[code])
		}
		lines = append(lines, "  "+p.Emph("Failures: "+strings.Join(parts, ", ")+"."))
	}

	var note string
	switch {
	case m.finished:
	case m.cancelling:
		note = "Quitting: cancelling the calls in flight."
	case m.stopping:
		note = "Stopping: finishing the calls in flight, then writing what exists. Press Ctrl-C again to quit now."
	case m.stopped != "":
		note = "Stopped early: " + StopReasons[m.stopped] + ". Finishing the calls in flight."
	default:
		note = "Ctrl-C stops new calls and keeps what is done."
	}
	if note != "" {
		for _, l := range term.Wrap(note, w-2) {
			lines = append(lines, "  "+p.Muted(l))
		}
	}
	for i, l := range lines {
		lines[i] = term.Truncate(l, w, m.ell())
		if m.ASCII {
			lines[i] = term.ToAscii(lines[i])
		}
	}
	return strings.Join(lines, "\n")
}

func (m Progress) ell() string {
	if m.ASCII {
		return "~"
	}
	return term.Ellipsis
}

func (m Progress) View() tea.View {
	if m.finished {
		// The CLI prints the outcome next; leave nothing behind.
		return tea.NewView("")
	}
	return tea.NewView(m.Frame() + "\n")
}

// RunOptions say where the live view draws.
type RunOptions struct {
	Input  io.Reader
	Output io.Writer
	// In os.Environ form.
	Env     []string
	Profile colorprofile.Profile
	ASCII   bool
	Width   int
	// Forward SIGINT and SIGTERM to the view. Off in tests.
	Signals bool
	Now     func() time.Time
}

// RunWithProgress executes the plan behind the live view. execute runs the
// plan with the stop contexts and the event callback it is given. The
// first Ctrl-C cancels soft, the second hard.
func RunWithProgress(plan *run.RunPlan, o RunOptions, soft, hard context.CancelFunc, execute func(onEvent func(run.Event)) (*types.RunFile, error)) (*types.RunFile, error) {
	model := NewProgress(plan, ProgressOptions{Paint: term.NewPaint(o.Profile), ASCII: o.ASCII, Width: o.Width, Soft: soft, Hard: hard, Now: o.Now})
	p := tea.NewProgram(model, programOptions(o.Input, o.Output, o.Env, o.Profile)...)
	if o.Signals {
		stop := ForwardSignals(p)
		defer stop()
	}
	type result struct {
		run *types.RunFile
		err error
	}
	done := make(chan result, 1)
	go func() {
		r, err := execute(func(e run.Event) { p.Send(EventMsg{Event: e}) })
		done <- result{r, err}
		p.Send(DoneMsg{})
	}()
	// When the view cannot draw, the run goes on without it.
	_, _ = p.Run()
	r := <-done
	return r.run, r.err
}

// Rows are each model's label, done and total, for tests.
func (m Progress) Rows() [][3]string {
	out := make([][3]string, len(m.rows))
	for i, r := range m.rows {
		out[i] = [3]string{r.label, fmt.Sprint(r.done), fmt.Sprint(r.total)}
	}
	return out
}

// Stopping and Cancelling say how far an interrupt has gone.
func (m Progress) Stopping() bool   { return m.stopping }
func (m Progress) Cancelling() bool { return m.cancelling }
