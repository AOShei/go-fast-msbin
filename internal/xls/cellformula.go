package xls

import (
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/ptg"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// The formulas of cells, read back as text with Options.Cells. A formula
// that cannot be read back is left out and counted: wrong text would be
// worse than none.

// formulaText reads a compiled formula back as text, in the shared
// model's form: valid UTF-8 with no control characters, which a string in
// a formula may hold.
func formulaText(rgce, extra []byte, book *ptg.Book, at ptg.Place) (string, bool) {
	text, ok := ptg.Formula(rgce, extra, book, at)
	if !ok {
		return "", false
	}
	if clean.Plain([]byte(text)) {
		return text, true
	}
	text, _ = clean.String([]byte(text), clean.Verbatim)
	return text, true
}

// blockFormula is a formula as the text of a block has it: the ligatures a
// string in it may hold are written out, as in all block text.
func blockFormula(text string) string {
	out, _ := clean.String([]byte(text), clean.Ligatures)
	return out
}

// sharedFormula is a formula several cells share: written once, with
// references relative to the cell that uses it ([MS-XLS] 2.4.260).
type sharedFormula struct {
	rgce, extra []byte
}

// detailRef names the detail of a cell whose formula follows it.
type detailRef struct {
	at    pos
	base  pos // the first cell of the range that shares the formula
	index int // of its detail, or -1 while the cell has none
}

// What reading a formula back came to.
const (
	formulaRead   = iota
	formulaWaits  // it is shared, and the shared formula follows
	formulaUnread // it cannot be read back
	formulaNone   // the cell has no formula of its own: part of an array, or a what-if table
)

// recordFormula reads the formula of a Formula record back, for the cell at
// row and col.
func (sh *sheet) recordFormula(b []byte, row, col int32) (string, int) {
	if len(b) < 22 {
		return "", formulaUnread
	}
	cce := int(le.Uint16(b[20:]))
	if cce > len(b)-22 {
		return "", formulaUnread
	}
	rgce, extra := b[22:22+cce], b[22+cce:]
	if exp, ok := ptg.ExpOf(rgce); ok {
		base := pos{exp.Row, exp.Col}
		f, known := sh.shared[base]
		switch {
		case known && f.rgce == nil:
			// An array formula: the cell that begins it has it.
			return "", formulaNone
		case known:
			text, ok := formulaText(f.rgce, f.extra, &sh.b.formulas, ptg.Place{Row: row, Col: col})
			if !ok {
				return "", formulaUnread
			}
			return text, formulaRead
		}
		sh.waiting = &detailRef{at: pos{row, col}, base: base, index: -1}
		return "", formulaWaits
	}
	if len(rgce) > 0 && rgce[0] == 0x02 {
		return "", formulaNone // a cell of a what-if table
	}
	text, ok := formulaText(rgce, extra, &sh.b.formulas, ptg.Place{Row: row, Col: col})
	if !ok {
		return "", formulaUnread
	}
	return text, formulaRead
}

// cellFormula sets the formula of the cell being read, if it can be read
// back.
func (sh *sheet) cellFormula(b []byte, row, col int32) {
	sh.waiting = nil
	text, state := sh.recordFormula(b, row, col)
	switch state {
	case formulaRead:
		sh.formula = text
	case formulaUnread:
		sh.unreadFormulas++
	}
}

// sharedFormula reads a ShrFmla record: the formula that the cell before
// it, and others of its range, share.
func (sh *sheet) sharedFormula(b []byte) {
	w := sh.waiting
	sh.waiting = nil
	if w == nil || len(b) < 10 {
		return
	}
	cce := int(le.Uint16(b[8:]))
	if cce > len(b)-10 {
		sh.unreadFormulas++
		return
	}
	if len(sh.shared) >= warn.MaxSharedFormulas {
		sh.lost.Add(warn.CapSharedFormulas, 1)
		return
	}
	if sh.shared == nil {
		sh.shared = map[pos]sharedFormula{}
	}
	f := sharedFormula{rgce: b[10 : 10+cce], extra: b[10+cce:]}
	sh.shared[w.base] = f
	sh.settle(w, f)
}

// arrayFormula reads an Array record: the formula of a range of cells
// that give its results together. The first cell has the formula, as an
// .xlsx file writes it; the others have none.
func (sh *sheet) arrayFormula(b []byte) {
	w := sh.waiting
	sh.waiting = nil
	if w == nil || len(b) < 14 {
		return
	}
	if sh.shared == nil {
		sh.shared = map[pos]sharedFormula{}
	}
	if len(sh.shared) < warn.MaxSharedFormulas {
		sh.shared[w.base] = sharedFormula{} // marks the range as an array's
	}
	if w.at != w.base {
		return
	}
	cce := int(le.Uint16(b[12:]))
	if cce > len(b)-14 {
		sh.unreadFormulas++
		return
	}
	sh.settle(w, sharedFormula{rgce: b[14 : 14+cce], extra: b[14+cce:]})
}

// settle gives the cell that was waiting its formula.
func (sh *sheet) settle(w *detailRef, f sharedFormula) {
	text, ok := formulaText(f.rgce, f.extra, &sh.b.formulas, ptg.Place{Row: w.at.row, Col: w.at.col})
	if !ok {
		sh.unreadFormulas++
		return
	}
	switch {
	case w.index >= 0 && w.index < len(sh.details) && sh.details[w.index].at == w.at:
		sh.details[w.index].formula = text
	case sh.strWait && sh.strAt == w.at:
		sh.strFormula = text // its result is a string, which follows
	}
}

// sharedAfter reads the formula that the record after the current one
// gives the cell at row and col, for a reading of formulas alone (a macro
// sheet's). The cursor is left on that record.
func (sh *sheet) sharedAfter(row, col int32) (string, int) {
	w := sh.waiting
	sh.waiting = nil
	c := &sh.c
	if w == nil || c.next+4 > len(c.data) {
		return "", formulaUnread
	}
	switch le.Uint16(c.data[c.next:]) {
	case recShrFmla:
		c.record(c.next)
		b := c.b
		if len(b) < 10 || int(le.Uint16(b[8:])) > len(b)-10 {
			return "", formulaUnread
		}
		cce := int(le.Uint16(b[8:]))
		f := sharedFormula{rgce: b[10 : 10+cce], extra: b[10+cce:]}
		if sh.shared == nil {
			sh.shared = map[pos]sharedFormula{}
		}
		if len(sh.shared) < warn.MaxSharedFormulas {
			sh.shared[w.base] = f
		}
		text, ok := formulaText(f.rgce, f.extra, &sh.b.formulas, ptg.Place{Row: row, Col: col})
		if !ok {
			return "", formulaUnread
		}
		return text, formulaRead
	case recArray:
		c.record(c.next)
		b := c.b
		if len(b) < 14 || int(le.Uint16(b[12:])) > len(b)-14 {
			return "", formulaUnread
		}
		cce := int(le.Uint16(b[12:]))
		if sh.shared == nil {
			sh.shared = map[pos]sharedFormula{}
		}
		if len(sh.shared) < warn.MaxSharedFormulas {
			sh.shared[w.base] = sharedFormula{}
		}
		text, ok := formulaText(b[14:14+cce], b[14+cce:], &sh.b.formulas, ptg.Place{Row: row, Col: col})
		if !ok {
			return "", formulaUnread
		}
		return text, formulaRead
	}
	return "", formulaNone
}
