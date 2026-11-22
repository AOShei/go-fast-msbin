// Package chart turns the data of a chart into blocks, as go-fast-ooxml
// does: the names of its series, its categories and its values, as the
// chart's cache holds them, given as a table, with the chart's title as a
// caption before it and its type and the titles of its axes as
// attributes (gofastcore.RoleChart).
//
// The cache is what the chart was drawn from when it was saved; the
// sheet it was taken from may say something else now, and is not
// consulted.
package chart

import (
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// Limits on what a chart may make the reader hold. The text the chart
// becomes is bounded by the document's text budget; these bound the work
// done before it is charged.
const (
	MaxSeries = warn.MaxChartSeries
	MaxPoints = warn.MaxChartPoints // points of one series
	MaxTitle  = warn.MaxChartTitle  // bytes of a title, of a series' name
	// The points of all the series of a chart, and the cells of all its
	// tables: what is held before any of it is charged to the budget.
	maxTotal = warn.MaxChartTotal
)

// Chart is what a chart says.
type Chart struct {
	// Kinds are the types of the chart's plots, lowercase as an .xlsx
	// file names them ("bar", "line", "pie"), in order, each once.
	Kinds []string
	// Title is the chart's title as shown; AxisX and AxisY are the titles
	// of its horizontal and vertical axes.
	Title, AxisX, AxisY string
	// Series are the chart's series, in order.
	Series []Series
}

// Series is one series of a chart: its name, its categories and its
// values, as shown.
type Series struct {
	Name string
	Cats []string
	// Vals are the sets of values: one for most kinds; a bubble chart's
	// sizes are a second.
	Vals   [][]string
	Hidden bool
}

// Kind adds a type of plot to the chart, once.
func (c *Chart) Kind(k string) {
	for _, have := range c.Kinds {
		if have == k {
			return
		}
	}
	c.Kinds = append(c.Kinds, k)
}

// Table is a table of a chart's data: a header row with the names of the
// series, a column of categories, and a column of values for each series.
type Table struct {
	Rows [][]string
	// Filtered: the file declares the series filtered out of the chart.
	Filtered bool
}

func sameCats(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// tables groups the series by their categories: series that share them
// share a table. Series filtered out of the chart go to tables of their
// own, after the others.
func tables(list []Series, cut *bool) []Table {
	var out []Table
	for _, hidden := range []bool{false, true} {
		done := make([]bool, len(list))
		for i := range list {
			if done[i] || list[i].Hidden != hidden {
				continue
			}
			group := []*Series{&list[i]}
			for j := i + 1; j < len(list); j++ {
				if !done[j] && list[j].Hidden == hidden && sameCats(list[j].Cats, list[i].Cats) {
					group = append(group, &list[j])
					done[j] = true
				}
			}
			out = append(out, table(group, hidden, cut))
		}
	}
	return out
}

func table(group []*Series, hidden bool, cut *bool) Table {
	cats := group[0].Cats
	levels := 0
	if len(cats) > 0 {
		levels = 1
	}
	n := len(cats)
	cols := levels
	for _, s := range group {
		for k := range s.Vals {
			n = max(n, len(s.Vals[k]))
			cols++
		}
	}
	if cols > 0 && (n+1)*cols > maxTotal {
		n, *cut = maxTotal/cols-1, true
	}
	rows := make([][]string, 0, n+1)
	head := make([]string, cols)
	c := levels
	for _, s := range group {
		for range s.Vals {
			head[c] = s.Name
			c++
		}
	}
	rows = append(rows, head)
	for i := 0; i < n; i++ {
		row := make([]string, cols)
		if levels > 0 && i < len(cats) {
			row[0] = cats[i]
		}
		c := levels
		for _, s := range group {
			for k := range s.Vals {
				if v := s.Vals[k]; i < len(v) {
					row[c] = v[i]
				}
				c++
			}
		}
		rows = append(rows, row)
	}
	return Table{Rows: rows, Filtered: hidden}
}

// Blocks returns the chart as blocks: a caption with its title, if it has
// one, and a table block for each of its tables, with name (the name the
// source gives the chart, if any), the chart's type and its axes' titles.
// What they hold is taken from budget: the caption and each table are
// blocks, each cell takes gofastcore.CellCost, and the text its bytes. A
// table there is no room for in full keeps the rows that fit; cut reports
// that the budget ran out. taken is what was taken.
func (c *Chart) Blocks(budget *gofastcore.TextBudget, name string) (blocks []gofastcore.Block, taken int64, cut bool) {
	list := tables(c.Series, &cut)
	if c.Title != "" {
		n := gofastcore.BlockCost + len(c.Title)
		if !budget.Take(n) {
			return nil, taken, true
		}
		taken += int64(n)
		blocks = append(blocks, gofastcore.Block{Kind: gofastcore.KindCaption, Text: c.Title})
	}
	attrs := map[string]string{gofastcore.AttrRole: gofastcore.RoleChart}
	if len(c.Kinds) > 0 {
		attrs[gofastcore.AttrChart] = strings.Join(c.Kinds, " ")
	}
	if c.AxisX != "" {
		attrs[gofastcore.AttrAxisX] = c.AxisX
	}
	if c.AxisY != "" {
		attrs[gofastcore.AttrAxisY] = c.AxisY
	}
	if name != "" {
		attrs[gofastcore.AttrName] = name
	}
	// The attributes are text the file decides, given with every table.
	said := 0
	for _, v := range attrs {
		said += len(v)
	}
	for k := range list {
		t := &list[k]
		cols := 0
		if len(t.Rows) > 0 {
			cols = len(t.Rows[0])
		}
		if cols == 0 {
			continue
		}
		fit := warn.FitRows(budget, len(t.Rows), cols)
		if fit == 0 {
			return blocks, taken, true
		}
		taken += int64(gofastcore.BlockCost + gofastcore.CellCost*fit*cols)
		rows := t.Rows[:fit]
		cut = fit < len(t.Rows)
		for i, row := range rows {
			n := 0
			for _, cell := range row {
				n += len(cell)
			}
			if !budget.Take(n) {
				rows, cut = rows[:i], true
				break
			}
			taken += int64(n)
		}
		if len(rows) == 0 {
			return blocks, taken, true
		}
		if budget.Take(said) {
			taken += int64(said)
		} else {
			cut = true
		}
		b := gofastcore.Block{Kind: gofastcore.KindTable, Table: &gofastcore.Table{Rows: rows, HeaderRows: 1}, Attrs: make(map[string]string, len(attrs))}
		for key, v := range attrs {
			b.Attrs[key] = v
		}
		if t.Filtered {
			b.Display = gofastcore.DisplayHidden
		}
		blocks = append(blocks, b)
		if cut {
			return blocks, taken, true
		}
	}
	return blocks, taken, false
}
