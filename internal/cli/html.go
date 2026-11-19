package cli

import (
	"os"
	"path/filepath"

	"github.com/metergraph/replay/internal/report"
	"github.com/metergraph/replay/internal/report/page"
	"github.com/metergraph/replay/internal/types"
)

// htmlPath is the --html path, or "" when the option is not given.
func (s *session) htmlPath() (string, error) {
	path, given := s.v.str("html")
	if !given {
		return "", nil
	}
	if path == "" {
		return "", usagef("--html needs a file name, such as --html report.html.")
	}
	return filepath.Abs(path)
}

// writePage writes the run as a self-contained HTML page to the --html
// path: the map when m is set, else the report. It returns the JSON field to
// add, or nil when --html was not given.
func (s *session) writePage(r *types.RunFile, m *report.Map, tracked []string) (object, error) {
	path, err := s.htmlPath()
	if err != nil || path == "" {
		return nil, err
	}
	var data []byte
	if m != nil {
		data, err = page.MapPage(r, m)
	} else {
		var ro report.Options
		if ro, err = s.reportOptions(tracked); err != nil {
			return nil, err
		}
		var model *report.Model
		if model, err = report.Build(r, ro); err != nil {
			return nil, err
		}
		data, err = page.RunPage(model)
	}
	if err != nil {
		return nil, err
	}
	// Owner-only: the page holds the prompts and replies.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, err
	}
	s.say("Wrote " + s.shownPath(path) + ". Open it in a browser. " + page.Warning)
	return object{{"htmlFile", path}}, nil
}
