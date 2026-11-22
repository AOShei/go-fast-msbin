package xls

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/numfmt"
	"github.com/AOShei/go-fast-msbin/internal/officeart"
	"github.com/AOShei/go-fast-msbin/internal/ptg"
	"github.com/AOShei/go-fast-msbin/internal/stored"
	"github.com/AOShei/go-fast-msbin/internal/symbol"
	"github.com/AOShei/go-fast-msbin/internal/unit"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// Config is what the caller gives beyond the workbook stream.
type Config struct {
	// MaxCells bounds the cells kept from the workbook, all sheets
	// together.
	MaxCells int64
	// Stored reads into the stored part what the file stores besides
	// what this reader finds (the source of its macros), or is nil if
	// there is nothing.
	Stored func(ctx context.Context, part *stored.Part)
	// HasStored says Stored has something to read.
	HasStored bool
}

// sheetRef is a sheet as the workbook lists it.
type sheetRef struct {
	name   string
	pos    int  // where its records begin in the stream
	place  int  // its place among the BoundSheet records, which formulas name it by
	hidden bool // hidden, or hidden so that only a program can show it
	kind   byte // dtWorksheet and the like
}

// sheetPlace is where the records of a sheet are.
type sheetPlace struct {
	pos, size int
}

// The kinds of sheet a BoundSheet8 record names ([MS-XLS] 2.4.28).
const (
	dtWorksheet = 0x00 // a worksheet or a dialog sheet
	dtMacro     = 0x01
	dtChart     = 0x02
	dtVBA       = 0x06 // a module of the VBA project: not a sheet
)

// cellStyle is what a cell format means for the text of a cell.
type cellStyle struct {
	format  *numfmt.Format
	general bool        // the format is General
	symbol  symbol.Font // the cell's font, if it is a symbol font: its text is codes
}

var plainStyle = cellStyle{format: numfmt.General(), general: true}

// Book is a workbook opened for reading a sheet at a time. What the
// sheets share (the list of them, the cell formats, the shared strings)
// is read when it is opened, and does not change after.
type Book struct {
	data     []byte
	opts     *gofastcore.Options
	cfg      Config
	date1904 bool
	refs     []sheetRef
	styles   []cellStyle
	text     string   // the shared strings, one after another
	ends     []uint32 // where each ends in text
	// rich has, for the shared strings that change font partway, where
	// each change is and which symbol font the text is in from there, if
	// it is one. It is kept only for a workbook that has a symbol font.
	rich    map[uint32][]fontRun
	fonts   []symbol.Font
	removed int // control characters removed from shared strings

	// places is where the records of each sheet begin, and how many bytes
	// of them there are at most, in the order of the BoundSheet records.
	places []sheetPlace

	// codes are the number formats the workbook defines, by number.
	codes map[int]string

	// pictures is the store of pictures the shapes of the sheets show.
	pictures []officeart.StoreEntry

	// What formulas refer to outside themselves, and what the workbook
	// stores and shows on no sheet (formulas.go).
	formulas    ptg.Book
	names       []definedName
	external    int       // links to other workbooks and files
	unreadNames int       // defined names whose formulas are not read back
	macroSheets bool      // the workbook has sheets of macros
	lost        warn.Lost // what the bounds left out of the stored part
	damaged     []string  // what could not be read to its end
	docLost     warn.Lost

	cells atomic.Int64 // cells that may still be kept
}

// Open reads what the sheets of the workbook in data, a Workbook stream,
// share. It does not fail: what could not be read is reported by
// Summary, and a workbook that lists no sheets has one part that says
// so.
func Open(data []byte, opts *gofastcore.Options, cfg Config) *Book {
	b := &Book{data: data, opts: opts, cfg: cfg}
	b.globals()
	b.cells.Store(cfg.MaxCells)
	return b
}

// fontRun says that from the byte at of a string its text is in the
// symbol font, or in a font that is not one.
type fontRun struct {
	at   uint32
	font symbol.Font
}

// xf is a cell format: the numbers of its font and its number format.
type xf struct{ font, format uint16 }

// globals reads the first substream of the workbook: from its BOF record
// to its EOF.
func (b *Book) globals() {
	c := cursor{data: b.data}
	var fonts []symbol.Font
	var xfs []xf
	codes := map[int]string{}
	var scratch []byte
	var links linkReader
	var group []byte // the records of the drawing group
	at := 0
	typ, ok := c.record(at)
	if !ok || typ != recBOF {
		b.damaged = append(b.damaged, "Workbook")
		return
	}
	ended := false
	for at = c.next; !ended; at = c.next {
		typ, ok := c.record(at)
		if !ok {
			break
		}
		body := c.b
		switch typ {
		case recEOF:
			ended = true
		case recDateMode:
			b.date1904 = len(body) >= 2 && le.Uint16(body) != 0
		case recFont:
			// The font's name follows fourteen bytes of its properties
			// ([MS-XLS] 2.4.122).
			font := symbol.None
			if len(body) > 16 {
				c.b = body[14:]
				if name, ok := c.shortStr(scratch[:0]); ok {
					font = symbol.ByName(string(name))
					scratch = name
				}
			}
			if len(fonts) < warn.MaxCellStyles {
				fonts = append(fonts, font)
			} else {
				b.docLost.Add(warn.CapCellStyles, 1)
			}
		case recFormat:
			if len(body) < 5 {
				continue
			}
			if len(codes) >= warn.MaxNumberFormats {
				b.docLost.Add(warn.CapNumberFormats, 1)
				continue
			}
			c.b = body[2:]
			if code, ok := c.str(scratch[:0]); ok {
				// Text a format writes into a cell is text like any other.
				codes[int(le.Uint16(body))], _ = clean.String(code, clean.Ligatures)
				scratch = code
			}
		case recXF:
			if len(body) < 4 {
				continue
			}
			if len(xfs) >= warn.MaxCellStyles {
				b.docLost.Add(warn.CapCellStyles, 1)
				continue
			}
			xfs = append(xfs, xf{font: le.Uint16(body), format: le.Uint16(body[2:])})
		case recBoundSheet:
			if len(body) < 8 {
				continue
			}
			ref := sheetRef{pos: int(le.Uint32(body)), hidden: body[4]&3 != 0, kind: body[5]}
			c.b = body[6:]
			name, _ := c.shortStr(scratch[:0])
			scratch = name
			// A formula names a sheet by its place in this list, the
			// modules of the VBA project among them.
			ref.place = len(b.places)
			if len(b.places) < warn.MaxSheets {
				b.places = append(b.places, sheetPlace{pos: ref.pos})
			}
			if len(links.sheets) < warn.MaxSheets {
				shown, _ := clean.String(name, clean.Prose)
				links.sheets = append(links.sheets, clean.Label(shown))
			}
			if ref.kind == dtVBA {
				continue // a module: its source is read with the VBA project
			}
			b.macroSheets = b.macroSheets || ref.kind == dtMacro
			if len(b.refs) >= warn.MaxSheets {
				b.docLost.Add(warn.CapSheets, 1)
				continue
			}
			text, removed := clean.String(name, clean.Prose)
			b.removed += removed
			ref.name = b.docLost.Label(text)
			b.refs = append(b.refs, ref)
		case recSST:
			b.fonts = fonts
			b.strings(&c)
		case recMsoDrawingGroup:
			// The drawing group, which holds the pictures: it goes on in
			// records of its own kind and in CONTINUE records.
			if b.opts == nil || b.opts.Images != gofastcore.ImagesNone {
				group = append(group, body...)
				for c.next+4 <= len(b.data) {
					if t := le.Uint16(b.data[c.next:]); t != recContinue && t != recMsoDrawingGroup {
						break
					}
					c.record(c.next)
					group = append(group, c.b...)
				}
			}
		case recSupBook:
			links.supBook(b, &c)
		case recExternName:
			links.externName(b, &c)
		case recExternSheet:
			links.externSheet(b, body)
		case recName:
			links.name(b, &c)
		}
	}
	if !ended || c.short {
		b.damaged = append(b.damaged, "Workbook")
	}

	formats := map[uint16]*numfmt.Format{}
	b.styles = make([]cellStyle, len(xfs))
	if len(group) > 0 {
		var past int
		b.pictures, past = officeart.Store(group, warn.MaxPictures)
		b.docLost.Add(warn.CapPictures, int64(past))
	}
	b.codes = codes
	// The records of a sheet end where those of the next begin.
	for i := range b.places {
		end := len(b.data)
		for _, other := range b.places {
			if other.pos > b.places[i].pos && other.pos < end {
				end = other.pos
			}
		}
		b.places[i].size = max(end-b.places[i].pos, 0)
	}
	links.finish(b)
	b.fonts = fonts
	for i, x := range xfs {
		f, ok := formats[x.format]
		if !ok {
			code, custom := codes[int(x.format)]
			if !custom {
				code = numfmt.Builtin(int(x.format))
			}
			f = numfmt.Parse(code)
			formats[x.format] = f
		}
		cs := cellStyle{format: f, general: f == numfmt.General(), symbol: b.font(x.font)}
		b.styles[i] = cs
	}
}

// strings reads the shared strings: the SST record and the CONTINUE
// records after it ([MS-XLS] 2.4.265).
func (b *Book) strings(c *cursor) {
	if len(c.b) < 8 {
		return
	}
	// Each string takes three bytes at least, so the stream bounds how
	// many there can be whatever the record says.
	unique := min(int64(le.Uint32(c.b[4:])), int64(len(b.data)/3))
	c.b = c.b[8:]
	var all strings.Builder
	all.Grow(min(len(b.data)-c.next, 1<<20))
	ends := make([]uint32, 0, min(unique, 1<<16))
	var raw, tmp []byte
	// The fonts of a string's runs matter where one of them is a symbol
	// font, whose text is codes: only a workbook that has such a font
	// keeps them.
	for _, f := range b.fonts {
		c.keepRuns = c.keepRuns || f != symbol.None
	}
	for i := int64(0); i < unique; i++ {
		// A string begins in the record its header fits in: where fewer
		// than three bytes are left, it begins in the next.
		if len(c.b) < 3 && !c.cont() {
			break
		}
		var ok bool
		raw, ok = c.str(raw[:0])
		if len(c.runs) > 0 {
			b.richString(&all, uint32(i), raw, c.runs)
		} else if clean.Plain(raw) {
			all.Write(bytes.TrimSpace(raw))
		} else {
			var removed int
			tmp, removed = clean.Append(tmp[:0], raw, clean.Ligatures)
			b.removed += removed
			all.Write(bytes.TrimSpace(tmp))
		}
		ends = append(ends, uint32(all.Len()))
		if !ok {
			b.damaged = append(b.damaged, "Workbook (shared strings)")
			break
		}
	}
	c.keepRuns = false
	b.text, b.ends = all.String(), ends
}

// font returns the symbol font with the number a cell format or a run
// names it by. The number four is passed over, so that the fifth font is
// number five ([MS-XLS] 2.5.129).
func (b *Book) font(n uint16) symbol.Font {
	i := int(n)
	if i == 4 {
		return symbol.None
	}
	if i > 4 {
		i--
	}
	if i >= len(b.fonts) {
		return symbol.None
	}
	return b.fonts[i]
}

// richString writes a shared string that changes font partway, a piece
// at a time, and records where in what is written each font begins.
func (b *Book) richString(all *strings.Builder, index uint32, raw []byte, runs []run) {
	// Where each run begins in raw: a run counts 16-bit units, of which a
	// character outside the first plane is two.
	var out []fontRun
	var piece []byte
	start := all.Len()
	at, unit, next := 0, 0, 0
	flush := func(to int, font symbol.Font) {
		var removed int
		piece, removed = clean.Append(piece[:0], raw[at:to], clean.Ligatures)
		b.removed += removed
		all.Write(piece)
		at = to
		out = append(out, fontRun{at: uint32(all.Len() - start), font: font})
	}
	for i := 0; i < len(raw) && next < len(runs); {
		if unit >= int(runs[next].at) {
			flush(i, b.font(runs[next].font))
			next++
			continue
		}
		_, size := utf8.DecodeRune(raw[i:])
		if size == 4 {
			unit++
		}
		unit++
		i += size
	}
	var removed int
	piece, removed = clean.Append(piece[:0], raw[at:], clean.Ligatures)
	b.removed += removed
	all.Write(piece)
	// What is written is not trimmed, so that the places of the runs hold:
	// the cell is, when it is read. Only strings with a symbol font in
	// them, or that a cell in a symbol font may hold, are kept.
	if b.rich == nil {
		b.rich = map[uint32][]fontRun{}
	}
	b.rich[index] = out
}

// shared returns the shared string with the given number, or "" for a
// number the table does not have.
func (b *Book) shared(i uint32) string {
	if int64(i) >= int64(len(b.ends)) {
		return ""
	}
	start := uint32(0)
	if i > 0 {
		start = b.ends[i-1]
	}
	return b.text[start:b.ends[i]]
}

// style returns the cell format with the given number. One the file does
// not define is the plain style.
func (b *Book) style(i uint16) *cellStyle {
	if int(i) >= len(b.styles) {
		return &plainStyle
	}
	return &b.styles[i]
}

// SheetInfo is a sheet as the workbook lists it.
type SheetInfo struct {
	Name   string
	Hidden bool
}

// Sheets lists the sheets in the order of the workbook.
func (b *Book) Sheets() []SheetInfo {
	out := make([]SheetInfo, len(b.refs))
	for i, ref := range b.refs {
		out[i] = SheetInfo{Name: ref.name, Hidden: ref.hidden}
	}
	return out
}

// Len returns the number of parts: the sheets, one part for a workbook
// with none, and the stored part if there is one.
func (b *Book) Len() int {
	if i := b.Stored(); i >= 0 {
		return i + 1
	}
	return max(len(b.refs), 1)
}

// Stored returns the index of the stored part, or -1 if there is none.
func (b *Book) Stored() int {
	if !b.cfg.HasStored && len(b.names) == 0 && !b.macroSheets {
		return -1
	}
	return max(len(b.refs), 1)
}

// Noun names the parts of a workbook.
func (b *Book) Noun() string { return "sheets" }

// Begin starts a pass over the workbook: the limits count from here.
func (b *Book) Begin() { b.cells.Store(b.cfg.MaxCells) }

// Limited reports whether a limit cut short what the sheets share:
// nothing they share is bounded by a limit the caller sets.
func (b *Book) Limited() bool { return false }

// Read reads the sheet with the given index into a part.
func (b *Book) Read(ctx context.Context, index int) *unit.Result {
	if index == b.Stored() {
		return b.readStored(ctx, index)
	}
	if len(b.refs) == 0 {
		// A workbook with no sheets, or whose list of them could not be
		// read. A document has at least one part, and this one says why
		// it is empty.
		res := &unit.Result{Part: gofastcore.Part{Kind: gofastcore.PartSheet, Number: 1}}
		res.Part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the workbook lists no sheets")
		res.Part.Finish()
		return res
	}
	return b.sheet(ctx, index).result()
}

// Summary turns what was found while the sheets were read into warnings
// about the document.
func (b *Book) Summary(tallies []*unit.Tally) []gofastcore.Warning {
	out := b.docLost.Warnings()
	macros := false
	for _, t := range tallies {
		macros = macros || t.Macros
	}
	if macros {
		out = append(out, gofastcore.Warning{Code: gofastcore.WarnInputActiveContent, Severity: gofastcore.SeverityInfo,
			Message: "macro sheets (Excel 4.0 macros); what their cells show is read, the macros are not"})
	}
	// The links to other workbooks are the document's, whichever sheets
	// were read.
	if b.external > 0 {
		tallies = append(tallies[:len(tallies):len(tallies)], &unit.Tally{External: b.external})
	}
	if b.unreadNames > 0 {
		tallies = append(tallies[:len(tallies):len(tallies)], &unit.Tally{Unread: map[string]int{unreadFormula: b.unreadNames}})
	}
	out = append(out, unit.Summarize(tallies, b.damaged)...)
	if b.removed > 0 {
		out = append(out, gofastcore.Warning{Code: warn.ControlCharsRemoved, Severity: gofastcore.SeverityInfo,
			Message: strconv.Itoa(b.removed) + " in shared strings and sheet names"})
	}
	return out
}
