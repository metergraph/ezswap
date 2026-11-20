package cli

// The interactive map: after map, and report on a map run, an interactive
// terminal opens the map explorer once the text map is printed.

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/metergraph/replay/internal/mapview"
	"github.com/metergraph/replay/internal/report"
	"github.com/metergraph/replay/internal/report/page"
	"github.com/metergraph/replay/internal/types"
)

// exploreMap opens the map explorer when the terminal is interactive and
// returns the exit code it ends with; opened is false when it did not open.
func (s *session) exploreMap(r *types.RunFile, m *report.Map, runPath string) (code int, opened bool, err error) {
	if s.asJSON || !s.interactive() {
		return ExitOK, false, nil
	}
	d := s.display()
	pagePath, err := s.htmlPath()
	if err != nil {
		return 0, false, err
	}
	if pagePath == "" {
		// Written only if o asks for it.
		pagePath = filepath.Join(filepath.Dir(runPath), "map.html")
	}
	data := mapview.NewData(r, m, mapview.Options{HTMLPath: pagePath, Name: s.shownPath(runPath), ASCII: d.ascii})
	var reason string
	if s.io.ExploreMap != nil {
		reason, err = s.io.ExploreMap(data)
	} else {
		columns, rows := s.io.Columns, s.io.Rows
		if columns <= 0 {
			columns = 100
		}
		if rows <= 0 {
			rows = 30
		}
		reason, err = mapview.Run(context.Background(), data, mapview.RunOptions{
			Input: s.io.Stdin, Output: s.io.Stdout, Env: s.environ(), Profile: d.profile,
			Columns: columns, Rows: rows, Signals: true,
			Open: func(path string) error {
				if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
					text, err := page.MapPage(r, m)
					if err != nil {
						return err
					}
					// Owner-only: the page holds the prompts and replies.
					if err := os.WriteFile(path, text, 0o600); err != nil {
						return err
					}
				}
				return mapview.OpenInBrowser(path)
			},
		})
	}
	if err != nil {
		return 0, true, err
	}
	switch reason {
	case mapview.ReasonSIGINT:
		return ExitSIGINT, true, nil
	case mapview.ReasonSIGTERM:
		return ExitSIGTERM, true, nil
	}
	return ExitOK, true, nil
}
