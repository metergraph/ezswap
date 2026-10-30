// Package explore is the interactive explorer for a run: five tabs (the
// summary, the tracked fields, the noise floor, every call and the calls that
// could not be replayed) and the detail of one call, drawn in the terminal
// with Bubble Tea.
//
// The state and its reducer (state.go),
// what each tab shows (content.go) and the frame (screen.go) are pure: the
// same state and size always draw the same lines. program.go is the thin
// Bubble Tea layer that owns the terminal. The Summary tab shows
// report.RenderSummary, the block the printed report is built around.
package explore

import (
	"sync"

	"github.com/metergraph/replay/internal/report"
	"github.com/metergraph/replay/internal/report/link"
	"github.com/metergraph/replay/internal/report/term"
	"github.com/metergraph/replay/internal/types"
)

// Options say what to build. Tracked, Labels and MonthlyCalls are as for
// report.Options.
type Options struct {
	Tracked      []string
	Labels       bool
	MonthlyCalls *float64
	// Draw in plain ASCII.
	ASCII bool
	// The run's name for the status bar, such as replay-out/run.json.
	Name string
}

// Data is what the explorer shows, prepared once per run.
type Data struct {
	// The model as built, for report.RenderSummary (which cleans what it draws).
	Model *report.Model
	// The model with every string cleaned for the terminal (and made ASCII
	// when asked), for the explorer's own panels.
	Clean *report.Model
	// The hosted link for the Summary footer.
	Link string
	// The run's name for the status bar.
	Name string
	// Drawn in ASCII.
	ASCII bool
	// report.Caveats, shown under ? with the keys.
	Caveats []string

	mu    sync.Mutex
	cache map[string][]string
}

// NewData builds the model and everything the tabs draw from.
func NewData(run *types.RunFile, options Options) (*Data, error) {
	m, err := report.Build(run, report.Options{Tracked: options.Tracked, Labels: options.Labels, MonthlyCalls: options.MonthlyCalls})
	if err != nil {
		return nil, err
	}
	name := options.Name
	if name == "" {
		name = "run.json"
	}
	caveats := report.Caveats(m)
	if options.ASCII {
		for i, c := range caveats {
			caveats[i] = term.ToAscii(c)
		}
	}
	return &Data{
		Model:   m,
		Clean:   term.CleanDeep(m, options.ASCII),
		Link:    link.HostedLink(run, link.MediumCLI),
		Name:    term.CleanLine(name),
		ASCII:   options.ASCII,
		Caveats: caveats,
		cache:   map[string][]string{},
	}, nil
}

func (d *Data) cached(key string, build func() []string) []string {
	d.mu.Lock()
	hit, ok := d.cache[key]
	d.mu.Unlock()
	if ok {
		return hit
	}
	lines := build()
	d.mu.Lock()
	d.cache[key] = lines
	d.mu.Unlock()
	return lines
}
