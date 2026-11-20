package cli

// The interactive map: after map, and report on a map run, an interactive
// terminal opens the map explorer once the text map is printed.

import (
	"context"
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
	// --html wrote the page during this command: o opens that file.
	// Otherwise o writes a fresh page into a new private folder, never into
	// the run file's folder, which may hold a file or link this command did
	// not write.
	pagePath, err := s.htmlPath()
	if err != nil {
		return 0, false, err
	}
	written := pagePath != ""
	if !written {
		pagePath = "map.html" // shown only; the page is written on demand
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
				if written {
					return mapview.OpenInBrowser(path)
				}
				fresh, err := privatePage(r, m)
				if err != nil {
					return err
				}
				return mapview.OpenInBrowser(fresh)
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

// privatePage writes the map's page into a new folder only this user can
// read, and returns its path. The folder is left for the browser to read;
// the system's temporary directory clears it.
func privatePage(r *types.RunFile, m *report.Map) (string, error) {
	text, err := page.MapPage(r, m)
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "metergraph-replay-page-")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "map.html")
	// O_EXCL in a folder just made: nothing else can be there to follow.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := file.Write(text); err != nil {
		file.Close()
		return "", err
	}
	return path, file.Close()
}
