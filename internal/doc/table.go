package doc

import (
	"sort"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// A table is rows of cells, each cell a run of paragraphs ended by a cell
// mark, each row ended by a paragraph that is only a mark and carries the
// row's definition: where its cells' edges are and which cells are
// merged ([MS-DOC] 2.9.320, TDefTableOperand).

// tableRow is one row as it was read.
type tableRow struct {
	cells  []string
	edges  []int16  // the positions of the cells' edges, one more than cells
	flags  []uint16 // of each cell: merged with the one before, or with the one above
	header bool
}

// Flags of a cell's definition.
const (
	tcFirstMerged = 0x0001 // the first of cells merged along the row
	tcMerged      = 0x0002 // merged into the cell before it
	tcVertMerge   = 0x0020 // one of cells merged down the column
	tcVertRestart = 0x0040 // the first of them
)

// tableBuilder gathers the rows of a table.
type tableBuilder struct {
	rows  []tableRow
	cur   []string // the cells of the row being read
	cell_ []byte   // the text of the cell being read
	any   bool     // the cell being read has text
	join  string   // what parts the next text from what the cell holds
	n     int      // cells in all
}

// text adds a paragraph's text to the cell being read.
func (t *tableBuilder) text(s string, ok bool) {
	if !ok || s == "" {
		return
	}
	if t.any {
		if t.join == "" {
			t.join = "\n"
		}
		t.cell_ = append(t.cell_, t.join...)
	}
	t.cell_ = append(t.cell_, s...)
	t.any, t.join = true, ""
}

// cell ends the cell being read.
func (t *tableBuilder) cell() {
	t.cur = append(t.cur, string(t.cell_))
	t.cell_, t.any, t.join = t.cell_[:0], false, ""
	t.n++
}

// row ends the row being read, with its definition.
func (t *tableBuilder) row(def []byte, header bool) {
	if t.any {
		t.cell() // text after the last cell mark: a cell of its own
	}
	r := tableRow{cells: t.cur, header: header}
	t.cur = nil
	// The definition: a length, the number of cells, the positions of
	// their edges, and twenty bytes for each cell, which begin with its
	// flags.
	if len(def) >= 3 {
		n := int(def[2])
		if b := def[3:]; len(b) >= 2*(n+1) {
			r.edges = make([]int16, n+1)
			for i := range r.edges {
				r.edges[i] = int16(le.Uint16(b[2*i:]))
			}
			b = b[2*(n+1):]
			r.flags = make([]uint16, n)
			for i := 0; i < n && 20*i+2 <= len(b); i++ {
				r.flags[i] = le.Uint16(b[20*i:])
			}
		}
	}
	t.rows = append(t.rows, r)
}

// block makes the table's block. ok is false for a table with no text.
func (t *tableBuilder) block(r *reader) (gofastcore.Block, bool) {
	if len(t.cur) > 0 || t.any {
		t.row(nil, false) // a row the table breaks off in
	}
	if len(t.rows) == 0 {
		return gofastcore.Block{}, false
	}
	// The columns of the table: every position at which some cell of
	// some row has an edge. A row with no definition, or one that does
	// not fit its cells, has its cells in the first columns.
	var edges []int16
	for i := range t.rows {
		row := &t.rows[i]
		if len(row.edges) != len(row.cells)+1 {
			row.edges, row.flags = nil, nil
			continue
		}
		edges = append(edges, row.edges...)
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i] < edges[j] })
	uniq := edges[:0]
	for i, e := range edges {
		if i == 0 || e != edges[i-1] {
			uniq = append(uniq, e)
		}
	}
	edges = uniq
	cols := max(len(edges)-1, 1)
	for i := range t.rows {
		if t.rows[i].edges == nil {
			cols = max(cols, len(t.rows[i].cells))
		}
	}
	col := func(e int16) int {
		return sort.Search(len(edges), func(i int) bool { return edges[i] >= e })
	}
	rows := len(t.rows)
	if cells := int64(rows) * int64(cols); cells > warn.MaxTableCells {
		r.lost.Add(warn.CapTableCells, 1)
		rows = max(warn.MaxTableCells/cols, 1)
	}
	fit := warn.FitRows(r.room, rows, cols)
	if fit == 0 {
		r.cut = true
		return gofastcore.Block{}, false
	}
	if fit < rows {
		r.cut = true
		rows = fit
	}
	tbl := &gofastcore.Table{Rows: make([][]string, rows)}
	flat := make([]string, rows*cols)
	any := false
	// The cell that a cell merged down the column continues, by column.
	above := make([]int, cols) // the index into merges plus one, or 0
	type span struct{ row, col, rows, cols int }
	var merges []span
	header := true
	var joined strings.Builder
	for i := 0; i < rows; i++ {
		row := &t.rows[i]
		tbl.Rows[i] = flat[i*cols : (i+1)*cols : (i+1)*cols]
		if header && row.header {
			tbl.HeaderRows = i + 1
		} else {
			header = false
		}
		last := -1 // the merge of the cell before, for cells merged along the row
		// The text of the cells merged into that cell, after its own: it
		// is put in the cell once, when the cells merged into it end.
		joined.Reset()
		join := func() {
			if joined.Len() > 0 {
				tbl.Rows[merges[last].row][merges[last].col] = joined.String()
				joined.Reset()
			}
		}
		for k, text := range row.cells {
			c, width := k, 1
			if row.edges != nil {
				c = col(row.edges[k])
				width = max(col(row.edges[k+1])-c, 1)
			}
			if c >= cols {
				continue
			}
			width = min(width, cols-c)
			var flags uint16
			if k < len(row.flags) {
				flags = row.flags[k]
			}
			switch {
			case flags&tcMerged != 0 && last >= 0:
				// Merged into the cell before it: that cell is wider.
				merges[last].cols += width
				if text != "" {
					if joined.Len() == 0 {
						joined.WriteString(tbl.Rows[merges[last].row][merges[last].col])
					}
					joined.WriteByte('\n')
					joined.WriteString(text)
				}
				for j := c; j < c+width; j++ {
					above[j] = 0
				}
				continue
			case flags&tcVertMerge != 0 && flags&tcVertRestart == 0 && above[c] > 0:
				// Merged into the cell above it: that cell is taller.
				m := &merges[above[c]-1]
				if m.row+m.rows == i {
					m.rows++
				}
				continue
			}
			join()
			tbl.Rows[i][c] = text
			any = any || text != ""
			last = -1
			for j := c; j < c+width; j++ {
				above[j] = 0
			}
			if width > 1 || flags&(tcFirstMerged|tcVertRestart) != 0 {
				merges = append(merges, span{i, c, 1, width})
				last = len(merges) - 1
				if flags&tcVertRestart != 0 {
					above[c] = len(merges)
				}
			}
		}
		join()
	}
	if !any {
		return gofastcore.Block{}, false
	}
	for _, m := range merges {
		if m.rows > 1 || m.cols > 1 {
			tbl.Merges = append(tbl.Merges, gofastcore.Merge{Row: m.row, Col: m.col, RowSpan: m.rows, ColSpan: m.cols})
		}
	}
	return gofastcore.Block{Kind: gofastcore.KindTable, Table: tbl}, true
}
