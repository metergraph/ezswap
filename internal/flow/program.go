package flow

// The Bubble Tea layer for the flow's screens, on the alternate screen.

import (
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/metergraph/ezswap/internal/explore"
	"github.com/metergraph/ezswap/internal/report/term"
)

// Options say where a screen draws.
type Options struct {
	Input   io.Reader
	Output  io.Writer
	Env     []string
	Profile colorprofile.Profile
	ASCII   bool
	Width   int
	Height  int
}

func (o Options) program(model tea.Model) *tea.Program {
	options := []tea.ProgramOption{tea.WithoutSignalHandler(), tea.WithColorProfile(max(o.Profile, colorprofile.Ascii))}
	if o.Input != nil {
		options = append(options, tea.WithInput(o.Input))
	}
	if o.Output != nil {
		options = append(options, tea.WithOutput(o.Output))
	}
	if o.Env != nil {
		options = append(options, tea.WithEnvironment(o.Env))
	}
	return tea.NewProgram(model, options...)
}

func view(lines []string, height int) tea.View {
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	return v
}

// menuModel runs a Menu.
type menuModel struct {
	m      Menu
	paint  term.Paint
	ascii  bool
	height int
}

func (mm menuModel) Init() tea.Cmd { return nil }

func (mm menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		mm.m.Width, mm.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		for _, key := range explore.KeyNames(msg) {
			mm.m = ReduceMenu(mm.m, key)
			if mm.m.Cancel || mm.m.Chosen >= 0 {
				return mm, tea.Quit
			}
		}
	case tea.PasteMsg:
		if mm.m.Editing {
			mm.m.Value += strings.TrimSpace(msg.Content)
		}
	}
	return mm, nil
}

func (mm menuModel) View() tea.View {
	return view(MenuFrame(mm.m, mm.paint, mm.ascii), mm.height)
}

// RunMenu shows a menu and returns it once an item is chosen or the menu is
// cancelled.
func RunMenu(m Menu, o Options) (Menu, error) {
	m.Width = o.Width
	final, err := o.program(menuModel{m: m, paint: term.NewPaint(o.Profile), ascii: o.ASCII, height: o.Height}).Run()
	if err != nil {
		return m, err
	}
	return final.(menuModel).m, nil
}

// previewMsg is an estimate, for the revision of the choice it was asked for.
type previewMsg struct {
	revision int
	preview  Preview
}

// planModel runs a Plan, working out the estimate off the screen's thread.
type planModel struct {
	p        Plan
	paint    term.Paint
	ascii    bool
	estimate func(Choice) Preview
}

func (pm planModel) ask() tea.Cmd {
	revision, choice, estimate := pm.p.Revision, pm.p.Choice, pm.estimate
	choice.Models = append([]string(nil), choice.Models...)
	choice.Parts = append([]string(nil), choice.Parts...)
	return func() tea.Msg { return previewMsg{revision: revision, preview: estimate(choice)} }
}

func (pm planModel) Init() tea.Cmd { return pm.ask() }

func (pm planModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		pm.p.Width, pm.p.Height = msg.Width, msg.Height
	case previewMsg:
		if msg.revision == pm.p.Revision {
			pm.p.Preview, pm.p.Pending = msg.preview, false
		}
	case tea.KeyPressMsg:
		var changed bool
		for _, key := range explore.KeyNames(msg) {
			var c bool
			pm.p, c = ReducePlan(pm.p, key)
			changed = changed || c
			if pm.p.Cancel || pm.p.Done {
				return pm, tea.Quit
			}
		}
		if changed {
			pm.p.Revision++
			pm.p.Pending = true
			return pm, pm.ask()
		}
	case tea.PasteMsg:
		if pm.p.Typing {
			pm.p.Draft += strings.TrimSpace(msg.Content)
		}
	}
	return pm, nil
}

func (pm planModel) View() tea.View {
	return view(PlanFrame(pm.p, pm.paint, pm.ascii), pm.p.Height)
}

// RunPlan shows the plan screen, asking estimate for each choice, and
// returns the plan once it is run or cancelled.
func RunPlan(p Plan, estimate func(Choice) Preview, o Options) (Plan, error) {
	p.Width, p.Height, p.Pending = o.Width, o.Height, true
	final, err := o.program(planModel{p: p, paint: term.NewPaint(o.Profile), ascii: o.ASCII, estimate: estimate}).Run()
	if err != nil {
		return p, err
	}
	return final.(planModel).p, nil
}
