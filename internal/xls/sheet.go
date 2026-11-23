package xls

import (
	"bytes"
	"context"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/grid"
	"github.com/AOShei/go-fast-msbin/internal/numfmt"
	"github.com/AOShei/go-fast-msbin/internal/symbol"
	"github.com/AOShei/go-fast-msbin/internal/unit"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// arena holds the text of a sheet's cells in pieces, so that a cell
// costs no allocation of its own. A piece is never moved once text has
// been cut from it. The pieces grow from small to large: most sheets are
// small, and a large piece for each would be most of what a workbook of
// many small sheets allocates.
type arena struct {
	b    strings.Builder
	next int // the size of the next piece
}

const (
	arenaFirst = 4 << 10
	arenaPiece = 256 << 10
)

func (a *arena) put(p []byte) string {
	if len(p) == 0 {
		return ""
	}
	if a.b.Cap()-a.b.Len() < len(p) {
		a.next = min(max(2*a.next, arenaFirst), arenaPiece)
		a.b = strings.Builder{}
		a.b.Grow(max(a.next, len(p)))
	}
	start := a.b.Len()
	a.b.Write(p)
	return a.b.String()[start:]
}

// pos is the place of a cell on its sheet.
type pos struct{ row, col int32 }

func before(a, b pos) bool { return a.row < b.row || a.row == b.row && a.col < b.col }

// detail is what a cell holds besides its text, before its table is made.
type detail struct {
	at             pos
	formula, value string
	typ            gofastcore.CellType
}

// comment is a note on a cell: where it is, who wrote it, and the number
// of the drawing object that holds its text.
type comment struct {
	row, col int32
	author   string
	obj      uint16
}

// sheet reads one sheet. Each sheet is read by one goroutine and shares
// nothing that changes with the others, apart from the workbook's
// budgets.
type sheet struct {
	b     *Book
	ctx   context.Context
	part  gofastcore.Part
	g     grid.Sheet
	c     cursor
	size  int // the bytes of the sheet's records, at most
	start int // where its records begin in the cursor's data

	lost   warn.Lost
	capped bool
	text   arena
	room   *gofastcore.TextBudget
	raw    []byte // a string being read
	out    []byte // a value being formatted
	sym    []byte // text being translated from a symbol font

	// The row being read: the cells of a row are charged to the budgets
	// together.
	rowAt          int32
	rowFirst       int   // the index of its first cell
	rowBytes       int64 // the text of its cells
	pending        int64 // text of the cells read and not yet in tables
	last           pos   // the place of the last cell kept
	removed        int   // control characters taken out
	unmapped       int   // codes of symbol fonts with no character
	isDate         bool  // the value just formatted was shown as a date
	unread         map[string]int
	unreadFormulas int  // formulas that could not be read back
	macros         bool // the sheet is a sheet of macros
	cutOff         bool // the records break off before their end
	notSheet       bool // the workbook lists the sheet and does not hold it
	cancelled      bool

	// A formula whose result is a string: the string follows in a record
	// of its own.
	strAt      pos
	strStyle   *cellStyle
	strWait    bool
	strFormula string

	// The formula of the cell being read, with Options.Cells, and the
	// shared formulas of the sheet (cellformula.go).
	formula string
	shared  map[pos]sharedFormula
	waiting *detailRef // the cell whose formula is the shared one that follows

	details         []detail
	detailsUnsorted bool
	linkAt          map[pos]string
	links           []link

	// What floats over the sheet (drawing.go, chart.go).
	shape      shape
	floating   []grid.Placed
	shapesFull bool  // there is no room for more of it
	charts     int   // the charts read
	lookedUp   int64 // bytes of sheets read over again for their data

	// A reading for the cells of some ranges only (chart.go).
	only     []grid.Rect
	found    map[pos]string
	index    int  // the sheet's place among the BoundSheet records
	sawImage bool // the sheet has pictures that were not asked for
	budget   bool // the limit on decoded bytes was reached

	// The pivot tables the sheet declares.
	declared []grid.Declared

	// Drop-down lists (validation.go).
	lists      []choiceList
	listRanges int // the ranges of all of them

	// Comments: the notes, and the text of the drawing objects they
	// name.
	notes    []comment
	noteText map[uint16]string
	objID    uint16
	objNote  bool
}

// link is a hyperlink of a range of cells.
type link struct {
	at   grid.Rect
	href string
}

// A link over a range of more cells than this is kept as a range and
// looked up, not spread over its cells.
const linkSpread = 1 << 12

// A cell takes fourteen bytes of a sheet's records at least, six in a
// run of numbers; room is made at once for no more than two million.
const (
	minCellBytes = 14
	maxReserve   = 2 << 20
)

var (
	textTrue  = "TRUE"
	textFalse = "FALSE"
)

// errorText is what a cell shows for an error value ([MS-XLS] 2.5.11).
func errorText(code byte) string {
	switch code {
	case 0x00:
		return "#NULL!"
	case 0x07:
		return "#DIV/0!"
	case 0x0F:
		return "#VALUE!"
	case 0x17:
		return "#REF!"
	case 0x1D:
		return "#NAME?"
	case 0x24:
		return "#NUM!"
	case 0x2A:
		return "#N/A"
	case 0x2B:
		return "#GETTING_DATA"
	}
	return "#ERR" + strconv.Itoa(int(code))
}

// sheet reads the sheet with the given index.
func (b *Book) sheet(ctx context.Context, index int) *sheet {
	ref := b.refs[index]
	sh := &sheet{b: b, ctx: ctx, unread: map[string]int{}, rowAt: -1, last: pos{-1, -1}, index: ref.place}
	sh.c.cp = b.cp
	start := b.at(&sh.c, ref.pos)
	sh.room = gofastcore.TextBudgetFrom(ctx)
	sh.g.Room = sh.room
	part := &sh.part
	part.Kind, part.Number, part.Name = gofastcore.PartSheet, index+1, ref.name
	if ref.hidden {
		part.Display = gofastcore.DisplayHidden
	}
	switch ref.kind {
	case dtChart:
		// A sheet that is a chart: its blocks have no place on a sheet
		// of cells.
		if typ, ok := sh.c.record(start); !ok || typ != recBOF {
			sh.notSheet = true
		} else {
			sh.c.record(start)
			sh.chartBlocks(sh.readChart(), 0, 0, false, false)
		}
	default:
		sh.macros = ref.kind == dtMacro
		// The sheet's records end where those of the next begin.
		end := b.size
		for _, other := range b.refs {
			if other.pos > ref.pos && other.pos < end {
				end = other.pos
			}
		}
		sh.size = max(end-ref.pos, 0)
		sh.read(start)
	}
	if sh.notSheet {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the workbook lists the sheet but does not contain it")
	} else {
		sh.blocks()
	}
	sh.finish()
	return sh
}

// result is what reading the sheet gave.
func (sh *sheet) result() *unit.Result {
	if sh.unreadFormulas > 0 {
		sh.unread[unreadFormula] += sh.unreadFormulas
		sh.unreadFormulas = 0
	}
	return &unit.Result{Part: sh.part, Tally: unit.Tally{
		Unread: sh.unread, Macros: sh.macros,
		Limited: sh.capped || sh.budget || sh.g.Cut != "",
	}}
}

// read reads the records of the sheet, from its BOF record at offset at
// to its EOF.
func (sh *sheet) read(at int) {
	c := &sh.c
	sh.start = at
	typ, ok := c.record(at)
	if !ok || typ != recBOF {
		sh.notSheet = true
		return
	}
	depth := 0 // inside the records of a chart placed on the sheet
	for n := 0; ; n++ {
		typ, ok := c.record(c.next)
		if !ok {
			sh.cutOff = true
			break
		}
		if n&4095 == 4095 && sh.ctx.Err() != nil {
			sh.cancelled = true
			break
		}
		if depth > 0 {
			switch typ {
			case recBOF:
				depth++
			case recEOF:
				depth--
			}
			continue
		}
		if typ == recEOF {
			break
		}
		if !sh.record(typ) {
			break
		}
	}
	if c.short {
		sh.cutOff = true
	}
	sh.endRow()
}

// record reads one record of the sheet. It reports false where reading
// is to stop: a limit was reached, or the sheet is not one of cells.
func (sh *sheet) record(typ uint16) bool {
	c := &sh.c
	b := c.b
	// A cell's record begins with its row, its column and its format.
	cell := func(need int) (row, col int32, style *cellStyle, ok bool) {
		if len(b) < need {
			return 0, 0, nil, false
		}
		return int32(le.Uint16(b)), int32(le.Uint16(b[2:])), sh.b.style(le.Uint16(b[4:])), true
	}
	if sh.only != nil {
		// A reading for cells: nothing else of the sheet is wanted.
		switch typ {
		case recMsoDrawing, recObj, recTxo, recNote, recDV, recHLink, recMergeCells, recRow, recColInfo, recDimensions, recWsBool, recSxView:
			return true
		}
	}
	switch typ {
	case recLabelSST:
		if row, col, style, ok := cell(10); ok {
			return sh.sharedStr(row, col, style, le.Uint32(b[6:]))
		}
	case recRK:
		if row, col, style, ok := cell(10); ok {
			return sh.number(row, col, style, rk(le.Uint32(b[6:])))
		}
	case recNumber:
		if row, col, style, ok := cell(14); ok {
			return sh.number(row, col, style, math.Float64frombits(le.Uint64(b[6:])))
		}
	case recMulRK:
		// A row, the first column, a format and a value for each cell,
		// and the last column.
		if len(b) < 12 {
			break
		}
		row, col := int32(le.Uint16(b)), int32(le.Uint16(b[2:]))
		for cells := b[4 : len(b)-2]; len(cells) >= 6; cells, col = cells[6:], col+1 {
			if !sh.number(row, col, sh.b.style(le.Uint16(cells)), rk(le.Uint32(cells[2:]))) {
				return false
			}
		}
	case recBoolErr:
		if row, col, _, ok := cell(8); ok {
			text, kind := textFalse, gofastcore.CellBool
			switch {
			case b[7] != 0:
				text, kind = errorText(b[6]), gofastcore.CellError
			case b[6] != 0:
				text = textTrue
			}
			return sh.add(row, col, text, 0, kind, "")
		}
	case recFormula, recFormula3, recFormula4:
		// The result is a number, or, where its last two bytes are all
		// ones, a string that follows, a truth value, an error or the
		// empty string ([MS-XLS] 2.5.133).
		row, col, style, ok := cell(14)
		if !ok {
			break
		}
		sh.strWait = false
		sh.formula = ""
		if sh.b.opts != nil && sh.b.opts.Cells {
			sh.cellFormula(b, row, col)
		}
		res := b[6:14]
		if le.Uint16(res[6:]) != 0xFFFF {
			return sh.number(row, col, style, math.Float64frombits(le.Uint64(res)))
		}
		switch res[0] {
		case 0:
			sh.strAt, sh.strStyle, sh.strWait, sh.strFormula = pos{row, col}, style, true, sh.formula
			sh.formula = ""
		case 1:
			text := textFalse
			if res[2] != 0 {
				text = textTrue
			}
			return sh.add(row, col, text, 0, gofastcore.CellBool, "")
		case 2:
			return sh.add(row, col, errorText(res[2]), 0, gofastcore.CellError, "")
		}
	case recString:
		if !sh.strWait {
			break
		}
		sh.strWait = false
		raw, _ := c.str(sh.raw[:0])
		sh.raw = raw
		sh.formula = sh.strFormula
		return sh.str(sh.strAt.row, sh.strAt.col, sh.strStyle, sh.cellText(raw), gofastcore.CellText)
	case recShrFmla:
		sh.sharedFormula(b)
	case recArray:
		sh.arrayFormula(b)
	case recLabel, recRString:
		if row, col, style, ok := cell(9); ok {
			c.b = b[6:]
			raw, _ := c.str(sh.raw[:0])
			sh.raw = raw
			return sh.str(row, col, style, sh.cellText(raw), gofastcore.CellUnset)
		}
	case recRow:
		// A row of no height is hidden ([MS-XLS] 2.4.221, fDyZero).
		if len(b) >= 16 && le.Uint16(b[12:])&0x0020 != 0 {
			sh.g.HiddenRows = append(sh.g.HiddenRows, int32(le.Uint16(b)))
		}
	case recColInfo:
		if len(b) >= 10 && le.Uint16(b[8:])&0x0001 != 0 {
			lo, hi := int32(le.Uint16(b)), int32(le.Uint16(b[2:]))
			if lo <= hi {
				sh.g.HiddenCols = append(sh.g.HiddenCols, grid.Span{Lo: lo, Hi: min(hi, grid.MaxCols-1)})
			}
		}
	case recMergeCells:
		if len(b) < 2 {
			break
		}
		refs := b[2:]
		for n := int(le.Uint16(b)); n > 0 && len(refs) >= 8; n, refs = n-1, refs[8:] {
			if len(sh.g.Merges) >= warn.MaxMerges {
				sh.lost.Add(warn.CapMerges, 1)
				continue
			}
			r := grid.Rect{R0: int32(le.Uint16(refs)), R1: int32(le.Uint16(refs[2:])), C0: int32(le.Uint16(refs[4:])), C1: int32(le.Uint16(refs[6:]))}
			if r.R0 <= r.R1 && r.C0 <= r.C1 {
				sh.g.Merges = append(sh.g.Merges, r)
			}
		}
	case recDimensions:
		// The rows and columns the sheet's cells lie in ([MS-XLS]
		// 2.4.90): room is made for its cells at once, as many as that
		// area and the bytes of the sheet's records can hold, so that
		// the list of them is not grown and copied as it fills.
		if len(b) >= 10 && len(sh.g.Cells) == 0 {
			rows := int64(le.Uint16(b[2:])) - int64(le.Uint16(b))
			cols := int64(le.Uint16(b[6:])) - int64(le.Uint16(b[4:]))
			if len(b) >= 12 {
				// As Excel 97 and later write it, with rows in 32 bits.
				rows = int64(le.Uint32(b[4:])) - int64(le.Uint32(b))
				cols = int64(le.Uint16(b[10:])) - int64(le.Uint16(b[8:]))
			}
			if n := min(rows*cols, int64(sh.size/minCellBytes), maxReserve); rows > 0 && cols > 0 && n > 0 {
				sh.g.Cells = make([]grid.Cell, 0, n)
			}
		}
	case recWsBool:
		// A dialog sheet is a worksheet marked as one: what is on it is
		// controls, not cells ([MS-XLS] 2.4.351, fDialog).
		if len(b) >= 2 && le.Uint16(b)&0x0010 != 0 {
			sh.unread["dialog sheet"]++
			return false
		}
	case recBOF:
		// The records of a chart placed on the sheet, after the object
		// that places it.
		if sh.only != nil {
			// A reading for cells: the chart's records are passed over.
			for depth := 1; depth > 0; {
				typ, ok := c.record(c.next)
				if !ok {
					return false
				}
				switch typ {
				case recBOF:
					depth++
				case recEOF:
					depth--
				}
			}
			break
		}
		ch := sh.readChart()
		if ch == nil {
			return false
		}
		sh.chartBlocks(ch, sh.shape.row, sh.shape.col, true, sh.shape.props.Hidden())
	case recHLink:
		if sh.b.opts != nil && sh.b.opts.Spans {
			sh.hyperlink(b)
		}
	case recMsoDrawing:
		sh.drawing(b)
	case recObj:
		if sh.b.biff5 {
			sh.object5(b)
		} else {
			sh.object(b)
		}
	case recTxo:
		if sh.objNote {
			sh.textObject(b)
		} else {
			sh.shapeText(b)
		}
	case recNote:
		if sh.b.biff5 {
			sh.note5(b)
		} else {
			sh.note(b)
		}
	case recDV:
		sh.validation(b)
	case recSxView:
		// A pivot table: the cells it fills, the first row of its values
		// and its name ([MS-XLS] 2.4.313). Its cells are cells of the
		// sheet; the table says which, and that they are one table.
		if len(b) < 44 {
			break
		}
		if len(sh.declared) >= warn.MaxPivotTables {
			sh.lost.Add(warn.CapPivotTables, 1)
			break
		}
		t := grid.Declared{Role: gofastcore.RolePivot,
			At: grid.Rect{R0: int32(le.Uint16(b)), R1: int32(le.Uint16(b[2:])), C0: int32(le.Uint16(b[4:])), C1: int32(le.Uint16(b[6:]))}}
		if t.At.R0 > t.At.R1 || t.At.C0 > t.At.C1 || t.At.C1 >= grid.MaxCols {
			break
		}
		// The rows above the first row of values are its headers: the
		// labels of the column fields.
		if first := int32(le.Uint16(b[10:])); first > t.At.R0 {
			t.HeaderRows = int(min(first-t.At.R0, t.At.R1-t.At.R0+1))
		}
		if cch := int(le.Uint16(b[40:])); cch > 0 && len(b) > 44 {
			var raw []byte
			if sh.b.biff5 {
				c.b = b[44:]
				raw, _ = c.bytes(sh.raw[:0], cch)
			} else {
				c.b = b[45:]
				raw, _ = c.chars(sh.raw[:0], cch, b[44]&1 != 0)
			}
			sh.raw = raw
			name, removed := clean.String(raw, clean.Prose)
			sh.removed += removed
			t.Name = sh.lost.Name(name)
		}
		sh.declared = append(sh.declared, t)
	}
	return true
}

// rk is the number an RK value stands for ([MS-XLS] 2.5.217): thirty bits
// of an integer or of the high end of a floating-point number, and two
// flags, one of which divides by a hundred.
func rk(v uint32) float64 {
	var f float64
	if v&2 != 0 {
		f = float64(int32(v) >> 2)
	} else {
		f = math.Float64frombits(uint64(v&^3) << 32)
	}
	if v&1 != 0 {
		f /= 100
	}
	return f
}

// cellText puts text read from a record into the shared model's form:
// valid UTF-8, no control characters, ligatures expanded, no space at
// the ends.
func (sh *sheet) cellText(raw []byte) string {
	if clean.Plain(raw) {
		return sh.text.put(bytes.TrimSpace(raw))
	}
	out, removed := clean.Append(sh.out[:0], raw, clean.Ligatures)
	sh.out = out
	sh.removed += removed
	return sh.text.put(bytes.TrimSpace(out))
}

// str adds a cell of text, as its format shows it. kind is CellText for
// the result of a formula, and unset for text the cell holds.
func (sh *sheet) str(row, col int32, style *cellStyle, text string, kind gofastcore.CellType) bool {
	if text == "" {
		return true
	}
	var hidden gofastcore.Display
	if style.symbol != symbol.None {
		var missing int
		sh.sym, missing = style.symbol.Translate(sh.sym[:0], []byte(text))
		sh.unmapped += missing
		text = sh.text.put(sh.sym)
	}
	if !style.format.PlainText() {
		out, result := style.format.Text(sh.out[:0], text)
		sh.out = out
		text, hidden = sh.text.put(bytes.TrimSpace(out)), blank(result)
	}
	return sh.add(row, col, text, hidden, kind, "")
}

// sharedStr adds a cell that holds a shared string.
func (sh *sheet) sharedStr(row, col int32, style *cellStyle, index uint32) bool {
	text := sh.b.shared(index)
	runs, rich := sh.b.rich[index]
	if !rich {
		return sh.str(row, col, style, text, gofastcore.CellUnset)
	}
	// A string that changes font partway: the cell's font up to the first
	// change, and each run's font from there. A piece in a symbol font is
	// codes of that font.
	out := sh.sym[:0]
	at, font := 0, style.symbol
	for i := 0; i <= len(runs); i++ {
		end := len(text)
		if i < len(runs) {
			end = min(int(runs[i].at), len(text))
		}
		if piece := text[at:end]; font != symbol.None {
			var missing int
			out, missing = font.Translate(out, []byte(piece))
			sh.unmapped += missing
		} else {
			out = append(out, piece...)
		}
		at = end
		if i < len(runs) {
			font = runs[i].font
		}
	}
	sh.sym = out
	plain := *style
	plain.symbol = symbol.None
	return sh.str(row, col, &plain, sh.text.put(bytes.TrimSpace(out)), gofastcore.CellUnset)
}

// blank is the reason a value that its format displays as nothing cannot
// be seen.
func blank(r numfmt.Result) gofastcore.Display {
	if r == numfmt.Blank {
		return gofastcore.DisplayHidden
	}
	return 0
}

// number adds a cell that holds a number, as its format shows it.
func (sh *sheet) number(row, col int32, style *cellStyle, v float64) bool {
	var hidden gofastcore.Display
	out := sh.out[:0]
	kind := gofastcore.CellNumber
	if style.general {
		// Most numbers are whole and short, and General shows them as
		// their digits.
		if i := int64(v); float64(i) == v && i > -1e11 && i < 1e11 && (i != 0 || !math.Signbit(v)) {
			out = strconv.AppendInt(out, i, 10)
		} else {
			out = numfmt.AppendGeneral(out, v)
		}
	} else {
		var result numfmt.Result
		out, result = style.format.Number(out, v, sh.b.date1904)
		hidden = blank(result)
		if result == numfmt.Date {
			kind = gofastcore.CellDate
		}
	}
	sh.out = out
	if len(out) == 0 {
		return true
	}
	value := ""
	if sh.b.opts != nil && sh.b.opts.Cells {
		// The stored value, where the text does not show it in full.
		var buf [32]byte
		if full := strconv.AppendFloat(buf[:0], v, 'g', -1, 64); string(full) != string(out) {
			value = string(full)
		}
	}
	return sh.add(row, col, sh.text.put(out), hidden, kind, value)
}

// room4cells makes room for the cells still to come, where the room made
// at the start was not enough: as many as the rest of the sheet's records
// would hold at the size the cells read so far have had, so that the
// list is not copied again and again as it grows.
func (sh *sheet) room4cells() {
	n := len(sh.g.Cells)
	more := n/4 + 16
	if read := sh.c.next - sh.start; n > 0 && read > 0 && sh.size > read {
		rest := int64(sh.size-read) * int64(n) / int64(read)
		more = int(min(max(rest+rest/16, int64(more)), maxReserve))
	}
	sh.g.Cells = slices.Grow(sh.g.Cells, more)
}

// add keeps a cell. It reports false, having kept neither the cell nor
// the row before its own, where that row met a limit.
func (sh *sheet) add(row, col int32, text string, hidden gofastcore.Display, kind gofastcore.CellType, value string) bool {
	if text == "" {
		return true
	}
	if sh.only != nil {
		// A reading for the cells of some ranges: a chart's data.
		for _, r := range sh.only {
			if row >= r.R0 && row <= r.R1 && col >= r.C0 && col <= r.C1 {
				if len(sh.found) < warn.MaxChartTotal {
					sh.found[pos{row, col}] = strings.Clone(text)
				}
				break
			}
		}
		return true
	}
	if col >= grid.MaxCols {
		sh.lost.Add(warn.CapSheetCols, 1)
		return true
	}
	if row != sh.rowAt {
		if !sh.endRow() {
			return false
		}
		sh.rowAt, sh.rowFirst = row, len(sh.g.Cells)
	}
	at := pos{row, col}
	if !before(sh.last, at) {
		sh.g.Unsorted = true
	}
	sh.last = at
	sh.rowBytes += int64(len(text))
	if len(sh.g.Cells) == cap(sh.g.Cells) {
		sh.room4cells()
	}
	sh.g.Cells = append(sh.g.Cells, grid.Cell{Text: text, Row: row, Col: col, Hidden: hidden})
	if kind != gofastcore.CellUnset && sh.b.opts != nil && sh.b.opts.Cells {
		if n := len(sh.details); n > 0 && !before(sh.details[n-1].at, at) {
			sh.detailsUnsorted = true
		}
		sh.details = append(sh.details, detail{at: at, typ: kind, value: value, formula: sh.formula})
		if sh.waiting != nil && sh.waiting.at == at {
			sh.waiting.index = len(sh.details) - 1
		}
	}
	sh.formula = ""
	return true
}

// endRow charges the cells of the row just read to the document's
// budgets. It reports false, having dropped them, where there is no room.
func (sh *sheet) endRow() bool {
	n := len(sh.g.Cells) - sh.rowFirst
	bytes := sh.rowBytes
	sh.rowBytes = 0
	if sh.rowAt < 0 || n <= 0 {
		return true
	}
	drop := func(why string) bool {
		sh.g.Cells = sh.g.Cells[:sh.rowFirst]
		for len(sh.details) > 0 && sh.details[len(sh.details)-1].at.row == sh.rowAt {
			sh.details = sh.details[:len(sh.details)-1]
		}
		sh.g.Cut = why
		sh.rowAt = -1
		return false
	}
	if sh.b.cells.Add(-int64(n)) < 0 {
		return drop("the limit on the number of cells was reached")
	}
	// The text is taken from the budget when the tables are made, with
	// what their blocks and cells take; reading stops here once there
	// could be no room for it.
	if sh.pending += bytes; sh.pending > sh.room.Left() {
		return drop("the limit on the amount of text was reached")
	}
	sh.rowFirst = len(sh.g.Cells)
	return true
}

// Detail gives what a cell holds besides its text (grid.Details).
func (sh *sheet) Detail(c *grid.Cell, row, col int) (gofastcore.CellDetail, bool) {
	out := gofastcore.CellDetail{Row: row, Col: col}
	at := pos{c.Row, c.Col}
	if sh.linkAt != nil || sh.links != nil {
		out.Href = sh.hrefOf(at)
	}
	if i := sort.Search(len(sh.details), func(i int) bool { return !before(sh.details[i].at, at) }); i < len(sh.details) && sh.details[i].at == at {
		d := &sh.details[i]
		out.Formula, out.Type, out.Value = d.formula, d.typ, d.value
	}
	return out, out.Href != "" || out.Formula != "" || out.Type != gofastcore.CellUnset
}

// blocks turns what was read into the part's blocks: the cells as
// tables, and the comments at their cells.
func (sh *sheet) blocks() {
	g := &sh.g
	if len(g.HiddenRows) > 1 {
		sort.Slice(g.HiddenRows, func(i, j int) bool { return g.HiddenRows[i] < g.HiddenRows[j] })
		rows := g.HiddenRows[:0]
		for i, r := range g.HiddenRows {
			if i == 0 || r != g.HiddenRows[i-1] {
				rows = append(rows, r)
			}
		}
		g.HiddenRows = rows
	}
	g.Order()
	if sh.detailsUnsorted {
		sort.SliceStable(sh.details, func(i, j int) bool { return before(sh.details[i].at, sh.details[j].at) })
		kept := sh.details[:0]
		for i := range sh.details {
			if n := len(kept); n > 0 && kept[n-1].at == sh.details[i].at {
				continue
			}
			kept = append(kept, sh.details[i])
		}
		sh.details = kept
	}
	if len(sh.details) > 0 || sh.linkAt != nil || sh.links != nil {
		g.Details = sh
	}
	blocks := g.Regions(sh.declared)
	blocks = append(blocks, sh.comments()...)
	blocks = append(blocks, sh.choices()...)
	blocks = append(blocks, sh.floating...)
	sh.floating = nil
	sh.part.Blocks = grid.Arrange(blocks)
	// The tables hold the text now.
	g.Cells, sh.details, sh.raw, sh.out = nil, nil, nil, nil
}

// finish completes the part: what limits cut it short, what it lacks,
// what was found while its cells were read, and which of its text is
// hidden.
func (sh *sheet) finish() {
	part := &sh.part
	if sh.cutOff && !sh.notSheet {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the sheet is cut off: its records break off before their end")
	}
	if sh.budget {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the decompressed size limit was reached; pictures from there on are named and not read")
	}
	if sh.g.Cut != "" {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, sh.g.Cut+"; the sheet is cut off there")
	}
	if sh.lost.Say(part) {
		sh.capped = true
	}
	hasText, hasImage := false, sh.sawImage
	for i := range part.Blocks {
		if part.Blocks[i].Kind == gofastcore.KindImage {
			hasImage = true
		} else {
			hasText = true
		}
	}
	if !hasText && !part.Has(gofastcore.WarnPartFailed) {
		if hasImage {
			part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartImageOnly, "")
		} else {
			part.Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "")
		}
	}
	if sh.g.Split > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnTableRegionSplit, strconv.Itoa(sh.g.Split)+" areas too thinly filled to be tables were written a row at a time")
	}
	if sh.removed > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnTextControlCharactersRemoved, strconv.Itoa(sh.removed))
	}
	if sh.unmapped > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs, warn.Unmapped(sh.unmapped))
	}
	if sh.c.undecoded > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs, undecodedMessage(sh.c.undecoded, sh.b.cp))
	}
	part.Finish()
}
