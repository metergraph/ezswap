// Package deps pins the module's third-party dependencies while the port is
// in progress, so parallel work never edits go.mod. Delete it once every
// dependency is imported for real.
package deps

import (
	_ "charm.land/bubbles/v2/key"
	_ "charm.land/bubbletea/v2"
	_ "charm.land/lipgloss/v2"
	_ "github.com/charmbracelet/x/ansi"
	_ "github.com/rivo/uniseg"
	_ "golang.org/x/text/collate"
)
