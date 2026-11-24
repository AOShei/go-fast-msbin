package xls

import (
	"bytes"
	"context"
	"io"
	"math"
	"slices"
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
	// The workbook's stream: held whole in data, or read from src a part
	// at a time, each sheet its own records. size is its length.
	data     []byte
	src      io.ReaderAt
	size     int
	listed   bool // the sheets are known: what they share has been read
	opts     *gofastcore.Options
	cfg      Config
	date1904 bool
	// biff5 says the workbook is of Excel 5 or 95, whose strings are
	// bytes in the code page cp.
	biff5  bool
	cp     int
	refs   []sheetRef
	styles []cellStyle
	text   string   // the shared strings, one after another
	ends   []uint32 // where each ends in text
	// rich has, for the shared strings that change font partway, where
	// each change is and which symbol font the text is in from there, if
	// it is one. It is kept only for a workbook that has a symbol font.
	rich      map[uint32][]fontRun
	fonts     []symbol.Font
	removed   int // control characters removed from shared strings
	undecoded int // characters of the names of sheets and formats that their code page does not define

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
	b := &Book{data: data, size: len(data), opts: opts, cfg: cfg}
	b.globals()
	b.cells.Store(cfg.MaxCells)
	return b
}

// OpenStream is Open for a workbook stream that is not held in memory:
// size bytes of src, which must be readable throughout and from several
// goroutines at once. What the sheets share is read now; each sheet's
// records are read when the sheet is, and let go after. The document is
// the same as with Open.
func OpenStream(src io.ReaderAt, size int64, opts *gofastcore.Options, cfg Config) *Book {
	b := &Book{src: src, size: int(min(size, math.MaxInt32)), opts: opts, cfg: cfg}
	b.globals()
	b.cells.Store(cfg.MaxCells)
	return b
}

// The parts of a stream that is not held whole: the first of what the
// sheets share, and how much more is read at a time where records run on
// past what was read.
const (
	firstChunk = 256 << 10
	maxChunk   = 8 << 20
)

// at puts a cursor on the stream for the records that begin at pos, and
// returns where they begin in the cursor's data. A stream held whole is
// the data. Of one that is not, the data is the records from pos to
// where the next sheet's begin, or the first of the stream where the
// sheets are not yet known; it grows if the records run on, so that what
// is read is what would be read of the whole stream.
func (b *Book) at(c *cursor, pos int) int {
	if b.src == nil {
		c.data = b.data
		return pos
	}
	c.data = nil
	if pos < 0 || pos >= b.size {
		return 0
	}
	end := b.size
	if !b.listed {
		end = min(pos+firstChunk, b.size)
	}
	for _, other := range b.refs {
		if other.pos > pos && other.pos < end {
			end = other.pos
		}
	}
	read := func(have []byte, n int) []byte {
		from := pos + len(have)
		n = min(n, b.size-from)
		if n <= 0 {
			return have
		}
		// Room for it, and no copy of what is not yet read.
		at := len(have)
		have = slices.Grow(have, n)[:at+n]
		got, _ := b.src.ReadAt(have[at:], int64(from))
		return have[:at+max(got, 0)]
	}
	c.data = read(nil, end-pos)
	c.more = func(have []byte) []byte {
		return read(have, min(max(len(have), firstChunk), maxChunk))
	}
	return 0
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
	defer func() { b.listed = true }()
	var c cursor
	start := b.at(&c, 0)
	var fonts []symbol.Font
	var xfs []xf
	codes := map[int]string{}
	var scratch []byte
	var links linkReader
	var group []byte // the records of the drawing group
	at := start
	typ, ok := c.record(at)
	if !ok || typ != recBOF {
		b.damaged = append(b.damaged, "Workbook")
		return
	}
	// The version's high byte says which generation wrote the workbook.
	if len(c.b) >= 2 && le.Uint16(c.b)>>8 == 5 {
		b.biff5, b.cp = true, 1252
		c.cp = b.cp
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
		case recCodePage:
			// The code page of the workbook's strings, for a workbook
			// that keeps them in one ([MS-XLS] 2.4.52): two values stand
			// for the Macintosh's and for Windows'.
			if b.biff5 && len(body) >= 2 {
				switch cp := int(le.Uint16(body)); cp {
				case 0x8000:
					b.cp = 10000
				case 0x8001:
					b.cp = 1252
				default:
					b.cp = cp
				}
				c.cp = b.cp
			}
		case recFont:
			// The font's name follows fourteen bytes of its properties
			// ([MS-XLS] 2.4.122).
			font := symbol.None
			if len(body) > 16 {
				c.b = body[14:]
				if name, ok := c.shortStr(reuse(scratch)); ok {
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
			read := c.str
			if b.biff5 {
				read = c.shortStr // its length is in a byte
			}
			if code, ok := read(reuse(scratch)); ok {
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
			name, _ := c.shortStr(reuse(scratch))
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
				for {
					if t, ok := c.peek(); !ok || t != recContinue && t != recMsoDrawingGroup {
						break
					}
					c.record(c.next)
					group = append(group, c.b...)
				}
			}
		case recSupBook:
			links.supBook(b, &c)
		case recExternName:
			if !b.biff5 {
				links.externName(b, &c)
			}
		case recExternSheet:
			if b.biff5 {
				// One record for each sheet formulas name outside this
				// one; an encoded path is another file's.
				if len(body) >= 2 && body[1] == 0x01 {
					b.external++
				}
			} else {
				links.externSheet(b, body)
			}
		case recName:
			links.name(b, &c)
		}
	}
	if !ended || c.short {
		b.damaged = append(b.damaged, "Workbook")
	}
	b.undecoded = c.undecoded
	b.formulas.BIFF5, b.formulas.CodePage = b.biff5, b.cp

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
		end := b.size
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
	unique := min(int64(le.Uint32(c.b[4:])), int64(b.size/3))
	c.b = c.b[8:]
	var all strings.Builder
	all.Grow(min(b.size-c.next, 1<<20))
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
		raw, ok = c.str(reuse(raw))
		if len(c.runs) > 0 {
			b.richString(&all, uint32(i), raw, c.runs)
		} else if clean.Plain(raw) {
			all.Write(bytes.TrimSpace(raw))
		} else {
			var removed int
			tmp, removed = clean.Append(reuse(tmp), raw, clean.Ligatures)
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
		piece, removed = clean.Append(reuse(piece), raw[at:to], clean.Ligatures)
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
	piece, removed = clean.Append(reuse(piece), raw[at:], clean.Ligatures)
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

// Weight returns what the sheet with the given index weighs for the
// reading of several at a time: the bytes of its records. The cells of
// a sheet take several times that in memory while it is read.
func (b *Book) Weight(index int) int64 {
	if index < 0 || index >= len(b.refs) {
		return 0
	}
	ref := b.refs[index]
	end := b.size
	for _, other := range b.refs {
		if other.pos > ref.pos && other.pos < end {
			end = other.pos
		}
	}
	return int64(max(end-ref.pos, 0))
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
	if b.undecoded > 0 {
		out = append(out, gofastcore.Warning{Code: gofastcore.WarnTextUnmappedGlyphs, Severity: gofastcore.SeverityWarning,
			Message: undecodedMessage(b.undecoded, b.cp)})
	}
	if b.removed > 0 {
		out = append(out, gofastcore.Warning{Code: gofastcore.WarnTextControlCharactersRemoved, Severity: gofastcore.SeverityInfo,
			Message: strconv.Itoa(b.removed) + " in shared strings and sheet names"})
	}
	return out
}

// undecodedMessage says how many characters a code page did not give.
func undecodedMessage(n, cp int) string {
	return strconv.Itoa(n) + " characters in code page " + strconv.Itoa(cp) + ", which it does not define or which is not known here, are U+FFFD"
}
