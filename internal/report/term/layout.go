package term

// Column layout: fit cells into a width, dropping optional columns (highest
// Drop first) and giving what is left to one flexible column.

import (
	"math"
	"sort"
	"strings"
)

// ColumnSpec describes one column.
type ColumnSpec struct {
	ID    string
	Align Align // empty is left
	// Fixed columns size to their widest cell, capped here; nil is no cap.
	Max *int
	// At least this wide; nil is 0 for a fixed column and 4 for the flexible one.
	Min *int
	// Optional columns drop first, highest number first; 0 never drops.
	Drop int
	// The one column that takes the space left, between Min and Max.
	Flex bool
}

// Cols is a pointer to n, for ColumnSpec.Min and Max.
func Cols(n int) *int { return &n }

// Column is a column with the width the layout gave it.
type Column struct {
	ColumnSpec
	Width int
}

// Layout is the columns kept and the gap between them.
type Layout struct {
	Columns []Column
	Gap     int
}

// LayoutColumns decides column widths for these rows within available
// columns. gap is optional (2 by default).
func LayoutColumns(specs []ColumnSpec, rows []map[string]string, available int, gap ...int) Layout {
	g := 2
	if len(gap) > 0 {
		g = gap[0]
	}
	natural := func(spec ColumnSpec) int {
		widest := 0
		for _, r := range rows {
			widest = max(widest, Width(r[spec.ID]))
		}
		lo := 0
		if spec.Min != nil {
			lo = *spec.Min
		}
		w := max(lo, widest)
		if spec.Max != nil {
			w = min(*spec.Max, w)
		}
		return w
	}
	flexMin := func(spec ColumnSpec) int {
		if spec.Min != nil {
			return *spec.Min
		}
		return 4
	}
	// Indices into specs, so a column is removed by identity as the TypeScript did.
	var active []int
	for i, s := range specs {
		keep := s.Flex
		for _, r := range rows {
			if r[s.ID] != "" {
				keep = true
			}
		}
		if keep {
			active = append(active, i)
		}
	}
	gaps := func(n int) int { return g * max(0, n-1) }
	need := func(set []int) int {
		sum := 0
		for _, i := range set {
			if specs[i].Flex {
				sum += flexMin(specs[i])
			} else {
				sum += natural(specs[i])
			}
		}
		return sum + gaps(len(set))
	}
	for need(active) > available {
		var droppable []int
		for _, i := range active {
			if specs[i].Drop > 0 {
				droppable = append(droppable, i)
			}
		}
		if len(droppable) == 0 {
			break
		}
		sort.SliceStable(droppable, func(a, b int) bool { return specs[droppable[a]].Drop > specs[droppable[b]].Drop })
		next := active[:0:0]
		for _, i := range active {
			if i != droppable[0] {
				next = append(next, i)
			}
		}
		active = next
	}
	fixed := gaps(len(active))
	for _, i := range active {
		if !specs[i].Flex {
			fixed += natural(specs[i])
		}
	}
	spare := max(0, available-fixed)
	columns := make([]Column, 0, len(active))
	for _, i := range active {
		s := specs[i]
		if !s.Flex {
			columns = append(columns, Column{ColumnSpec: s, Width: natural(s)})
			continue
		}
		hi := spare
		if s.Max != nil {
			hi = *s.Max
		}
		w := max(flexMin(s), min(hi, spare))
		spare -= w
		columns = append(columns, Column{ColumnSpec: s, Width: w})
	}
	// Still too wide (a very narrow terminal): narrow the widest fixed columns.
	total := gaps(len(columns))
	for _, c := range columns {
		total += c.Width
	}
	for total > available {
		widest := -1
		best := math.MinInt
		for i, c := range columns {
			if c.Width > best {
				widest, best = i, c.Width
			}
		}
		if widest < 0 || columns[widest].Width <= 1 {
			break
		}
		columns[widest].Width--
		total--
	}
	return Layout{Columns: columns, Gap: g}
}

// RenderRow renders one row with a layout. Cells may carry ANSI styles. The
// optional ellipsis is as for Truncate.
func RenderRow(l Layout, cells map[string]string, ellipsis ...string) string {
	parts := make([]string, len(l.Columns))
	for i, c := range l.Columns {
		parts[i] = Fit(cells[c.ID], c.Width, c.Align, ellipsisOr(ellipsis))
	}
	return strings.TrimRight(strings.Join(parts, strings.Repeat(" ", l.Gap)), " ")
}

// Has reports whether the layout kept column id.
func Has(l Layout, id string) bool {
	for _, c := range l.Columns {
		if c.ID == id {
			return true
		}
	}
	return false
}

// WidthOf is column id's width, 0 when it was dropped.
func WidthOf(l Layout, id string) int {
	for _, c := range l.Columns {
		if c.ID == id {
			return c.Width
		}
	}
	return 0
}

// OffsetOf is the columns from the left edge to where column id starts.
func OffsetOf(l Layout, id string) int {
	x := 0
	for _, c := range l.Columns {
		if c.ID == id {
			return x
		}
		x += c.Width + l.Gap
	}
	return x
}
