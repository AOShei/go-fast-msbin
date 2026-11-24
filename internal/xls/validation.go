package xls

import (
	"sort"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/grid"
	"github.com/AOShei/go-fast-msbin/internal/ptg"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// choiceList is a drop-down list: the cells it is for and its values. It
// is a data validation of type list.
type choiceList struct {
	ranges []grid.Rect
	values []string // the values, where the file writes them out
	source string   // otherwise the reference or formula they come from
	arrow  bool     // the drop-down arrow is shown
}

// validation reads a Dv record: a rule for what cells may hold ([MS-XLS]
// 2.4.84). Only lists are kept: the other kinds of rule offer nothing to
// choose from.
func (sh *sheet) validation(b []byte) {
	if len(b) < 4 {
		return
	}
	const (
		typeList      = 3
		fStrLookup    = 0x00000080 // the list is written out, its values parted by zeros
		fSuppressComb = 0x00000200 // the arrow is not shown
	)
	flags := le.Uint32(b)
	if flags&0xF != typeList {
		return
	}
	if len(sh.lists) >= warn.MaxChoiceLists {
		sh.lost.Add(warn.CapChoiceLists, 1)
		return
	}
	c := &sh.c
	c.b = b[4:]
	// The titles and texts of the prompt and of the error message.
	for i := 0; i < 4; i++ {
		raw, ok := c.str(reuse(sh.raw))
		sh.raw = raw
		if !ok {
			return
		}
	}
	// The two formulas: each a length, two bytes not used, the tokens.
	formula := func() ([]byte, bool) {
		if len(c.b) < 4 {
			return nil, false
		}
		n := int(le.Uint16(c.b))
		if n > len(c.b)-4 {
			return nil, false
		}
		f := c.b[4 : 4+n]
		c.b = c.b[4+n:]
		return f, true
	}
	first, ok := formula()
	if !ok {
		return
	}
	if _, ok := formula(); !ok || len(c.b) < 2 {
		return
	}
	v := choiceList{arrow: flags&fSuppressComb == 0}
	refs := c.b[2:]
	for n := int(le.Uint16(c.b)); n > 0 && len(refs) >= 8; n, refs = n-1, refs[8:] {
		if sh.listRanges >= warn.MaxChoiceRanges {
			sh.lost.Add(warn.CapChoiceRanges, 1)
			continue
		}
		r := grid.Rect{R0: int32(le.Uint16(refs)), R1: int32(le.Uint16(refs[2:])), C0: int32(le.Uint16(refs[4:])), C1: int32(le.Uint16(refs[6:]))}
		if r.R0 <= r.R1 && r.C0 <= r.C1 && r.C1 < grid.MaxCols {
			v.ranges = append(v.ranges, r)
			sh.listRanges++
		}
	}
	if len(v.ranges) == 0 || len(first) == 0 {
		return
	}
	if flags&fStrLookup != 0 && first[0] == 0x17 {
		// One string, the values parted by zero characters.
		text, ok := ptg.Formula(first, nil, &sh.b.formulas, ptg.Place{})
		if !ok || len(text) < 2 {
			return
		}
		inner := strings.ReplaceAll(text[1:len(text)-1], `""`, `"`)
		v.values = strings.Split(inner, "\x00")
	} else {
		// A reference or a formula, relative to the first cell the list
		// is for.
		at := v.ranges[0]
		text, ok := formulaText(first, nil, &sh.b.formulas, ptg.Place{Row: at.R0, Col: at.C0})
		if !ok {
			sh.unreadFormulas++
			return
		}
		v.source = blockFormula(text)
	}
	sh.lists = append(sh.lists, v)
}

// choices returns the sheet's drop-down lists as note blocks, each at the
// first cell it is for. A list written out in the file gives its values,
// one per line; one taken from cells or worked out by a formula gives the
// formula, since those values are in the text where the cells are. A list
// whose arrow the file hides, or whose cells are all in hidden rows or
// columns, is declared hidden.
func (sh *sheet) choices() []grid.Placed {
	var out []grid.Placed
	for i := range sh.lists {
		v := &sh.lists[i]
		var sb strings.Builder
		if v.source != "" {
			text, removed := clean.String([]byte(v.source), clean.Prose)
			sh.removed += removed
			if text != "" {
				sb.WriteString("=" + text)
			}
		}
		for _, value := range v.values {
			text, removed := clean.String([]byte(value), clean.Prose)
			sh.removed += removed
			// A value is one line.
			text = strings.NewReplacer("\n", " ", "\t", " ").Replace(text)
			if text == "" {
				continue // an empty value offers nothing
			}
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(text)
		}
		if sb.Len() == 0 {
			continue
		}
		var where strings.Builder
		for k, r := range v.ranges {
			if k > 0 {
				where.WriteByte(' ')
			}
			if r.R0 == r.R1 && r.C0 == r.C1 {
				where.WriteString(grid.CellName(r.R0, r.C0))
			} else {
				where.WriteString(grid.RangeName(r))
			}
		}
		if !sh.room.Take(gofastcore.BlockCost + sb.Len() + where.Len()) {
			if sh.g.Cut == "" {
				sh.g.Cut = "the limit on the amount of text was reached"
			}
			break
		}
		b := gofastcore.Block{Kind: gofastcore.KindNote, Text: sb.String(), Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteChoices}}
		b.Loc.Range = where.String()
		if !v.arrow || sh.allHidden(v.ranges) {
			b.Display = gofastcore.DisplayHidden
		}
		first := v.ranges[0]
		for _, r := range v.ranges[1:] {
			if r.R0 < first.R0 || r.R0 == first.R0 && r.C0 < first.C0 {
				first = r
			}
		}
		out = append(out, grid.Placed{Row: first.R0, Col: first.C0, Block: b})
	}
	sh.lists = nil
	return out
}

// allHidden reports whether every cell of the ranges is in a hidden row or
// a hidden column. The hidden rows are in order with no row twice, and
// the hidden columns in order, as runs that do not touch.
func (sh *sheet) allHidden(ranges []grid.Rect) bool {
	rows, cols := sh.g.HiddenRows, sh.g.HiddenCols
	for _, r := range ranges {
		i := sort.Search(len(rows), func(i int) bool { return rows[i] >= r.R0 })
		j := i + int(r.R1-r.R0)
		if j < len(rows) && rows[i] == r.R0 && rows[j] == r.R1 {
			continue
		}
		k := sort.Search(len(cols), func(k int) bool { return cols[k].Hi >= r.C0 })
		if k < len(cols) && cols[k].Lo <= r.C0 && cols[k].Hi >= r.C1 {
			continue
		}
		return false
	}
	return true
}
