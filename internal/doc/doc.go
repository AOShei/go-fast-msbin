package doc

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"unicode/utf16"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/stored"
	"github.com/AOShei/go-fast-msbin/internal/symbol"
	"github.com/AOShei/go-fast-msbin/internal/unit"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// Config is what the caller gives beyond the streams.
type Config struct {
	// Data returns the Data stream, which holds what is too large for the
	// other two: long lists of properties, pictures. It is asked for when
	// it is first needed.
	Data func() []byte
	// Stored reads into the stored part what the file stores besides what
	// this reader finds (the source of its macros), or is nil.
	Stored    func(ctx context.Context, part *stored.Part)
	HasStored bool
}

// Doc is a Word document opened for reading: what its text is made of
// (the pieces, the property pages, the styles, the lists, the notes) is
// read when it is opened, and does not change after.
type Doc struct {
	f        *fib
	stream   []byte // the WordDocument stream
	table    []byte // the table stream
	data     []byte // the Data stream, once asked for
	dataOnce sync.Once
	opts     *gofastcore.Options
	cfg      Config

	txt      text
	prcs     [][]byte // property modifiers of pieces, for a file saved fast
	papxBins bins
	chpxBins bins
	styles   *styleSheet
	fonts    []string
	symbols  []symbol.Font // of each font, if it is a symbol font
	lists    *numbering

	// The stories after the main text begin where the one before ends.
	ftnAt, hddAt, atnAt, ednAt int64

	ftn, edn  notes
	atn       notes
	atnAuthor []uint16 // of each comment, its author's number
	authors   []string // of comments
	revisers  []string // of tracked changes
	hdd       []int64  // where each header and footer begins, in its story
	bookmarks map[int64]string
	drawing   *drawing
	variables []variable
	fields    int // fields of the main text, for bounding

	damaged []string
}

// notes is the footnotes, the endnotes or the comments of a document:
// where each is referred to in the main text, and where its text is in
// its own story.
type notes struct {
	refs []int64
	auto []bool  // the reference is numbered by the program
	text []int64 // one more than notes
}

// dataStream returns the Data stream.
func (d *Doc) dataStream() []byte {
	d.dataOnce.Do(func() {
		if d.cfg.Data != nil {
			d.data = d.cfg.Data()
		}
	})
	return d.data
}

// plc reads the character positions of a PLC whose elements are size
// bytes each: n+1 positions, then n elements.
func plc(b []byte, size int) (cps []int64, data []byte) {
	if len(b) < 4 {
		return nil, nil
	}
	n := (len(b) - 4) / (4 + size)
	cps = make([]int64, n+1)
	for i := range cps {
		cps[i] = int64(le.Uint32(b[4*i:]))
	}
	return cps, b[4*(n+1):]
}

// maxNotes bounds the footnotes, endnotes and comments of a document, and
// its bookmarks.
const maxNotes = 1 << 20

// Open reads what a document's text is made of from its WordDocument
// stream and its table stream. It does not fail: a document whose
// structures cannot be read gives a part that says so.
func Open(f *fib, stream, table []byte, opts *gofastcore.Options, cfg Config) *Doc {
	d := &Doc{f: f, stream: stream, table: table, opts: opts, cfg: cfg}
	d.txt.stream = stream
	var damaged bool
	d.txt.pieces, d.prcs, damaged = readPieces(f.part(table, fcClx), int64(len(stream)), false)
	if damaged || len(d.txt.pieces) == 0 {
		d.damaged = append(d.damaged, "the piece table")
	}
	d.papxBins = readBins(f.part(table, fcPlcfBtePapx))
	d.chpxBins = readBins(f.part(table, fcPlcfBteChpx))
	d.styles = readStyles(f.part(table, fcStshf))
	d.fonts = readFonts(f.part(table, fcSttbfFfn))
	d.symbols = make([]symbol.Font, len(d.fonts))
	for i, name := range d.fonts {
		d.symbols[i] = symbol.ByName(name)
	}
	var afterLists []byte
	if fcPlfLst < len(f.fc) {
		if end := int64(f.fc[fcPlfLst]) + int64(f.lcb[fcPlfLst]); f.lcb[fcPlfLst] > 0 && end <= int64(len(table)) {
			afterLists = table[end:]
		}
	}
	d.lists = d.readLists(f.part(table, fcPlfLst), afterLists, f.part(table, fcPlfLfo))

	d.ftnAt = f.ccpText
	d.hddAt = d.ftnAt + f.ccpFtn
	d.atnAt = d.hddAt + f.ccpHdd
	d.ednAt = d.atnAt + f.ccpAtn

	note := func(ref, txt, size int) notes {
		var n notes
		var data []byte
		n.refs, data = plc(f.part(table, ref), size)
		if len(n.refs) > 0 {
			n.refs = n.refs[:len(n.refs)-1]
		}
		if len(n.refs) > maxNotes {
			n.refs = n.refs[:maxNotes]
		}
		n.auto = make([]bool, len(n.refs))
		for i := range n.auto {
			n.auto[i] = size != 2 || 2*i+2 > len(data) || le.Uint16(data[2*i:]) != 0
		}
		n.text, _ = plc(f.part(table, txt), 0)
		return n
	}
	d.ftn = note(fcPlcffndRef, fcPlcffndTxt, 2)
	d.edn = note(fcPlcfendRef, fcPlcfendTxt, 2)
	// A comment's reference holds its author's initials and number
	// ([MS-DOC] 2.9.11, ATRDPre10: thirty bytes).
	{
		var data []byte
		d.atn.refs, data = plc(f.part(table, fcPlcfandRef), 30)
		if len(d.atn.refs) > 0 {
			d.atn.refs = d.atn.refs[:len(d.atn.refs)-1]
		}
		if len(d.atn.refs) > maxNotes {
			d.atn.refs = d.atn.refs[:maxNotes]
		}
		d.atnAuthor = make([]uint16, len(d.atn.refs))
		for i := range d.atnAuthor {
			if 30*i+22 <= len(data) {
				d.atnAuthor[i] = le.Uint16(data[30*i+20:])
			}
		}
		d.atn.text, _ = plc(f.part(table, fcPlcfandTxt), 0)
	}
	d.authors = xsts(f.part(table, fcGrpXstAtnOwner))
	d.revisers = sttb(f.part(table, fcSttbfRMark))
	d.hdd, _ = plc(f.part(table, fcPlcfHdd), 0)

	d.drawing = d.readDrawing()
	d.variables = readVariables(f.part(table, fcStwUser))

	// Bookmarks: their names, and where each begins.
	names := sttb(f.part(table, fcSttbfBkmk))
	starts, _ := plc(f.part(table, fcPlcfBkf), 4)
	d.bookmarks = map[int64]string{}
	for i, name := range names {
		if i >= len(starts) || len(d.bookmarks) >= maxNotes {
			break
		}
		if _, taken := d.bookmarks[starts[i]]; !taken && name != "" {
			d.bookmarks[starts[i]] = name
		}
	}
	return d
}

// sttb reads a table of strings (STTB, [MS-DOC] 2.2.4) of UTF-16 strings:
// a mark of that, a count, the length of the data beside each string, and
// the strings, each a count of characters and the characters.
func sttb(b []byte) []string {
	if len(b) < 6 || le.Uint16(b) != 0xFFFF {
		return nil
	}
	count, extra := int(le.Uint16(b[2:])), int(le.Uint16(b[4:]))
	b = b[6:]
	var out []string
	for i := 0; i < count && len(b) >= 2 && len(out) < maxNotes; i++ {
		n := int(le.Uint16(b))
		b = b[2:]
		if 2*n > len(b) {
			break
		}
		out = append(out, wide(b[:2*n]))
		b = b[2*n:]
		if extra > len(b) {
			break
		}
		b = b[extra:]
	}
	return out
}

// xsts reads strings that follow one another, each a count of characters
// and the characters: the authors of comments.
func xsts(b []byte) []string {
	var out []string
	for len(b) >= 2 && len(out) < maxNotes {
		n := int(le.Uint16(b))
		b = b[2:]
		if 2*n > len(b) {
			break
		}
		out = append(out, wide(b[:2*n]))
		b = b[2*n:]
	}
	return out
}

func wide(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = le.Uint16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

// Len returns the number of parts: the body, and the stored part if
// there is one.
func (d *Doc) Len() int {
	if d.Stored() >= 0 {
		return 2
	}
	return 1
}

// Stored returns the index of the stored part, or -1.
func (d *Doc) Stored() int {
	if d.cfg.HasStored || len(d.variables) > 0 {
		return 1
	}
	return -1
}

// variable is a document variable: a name and a value a program stored
// in the document for its fields and macros.
type variable struct{ name, value string }

// maxVariables bounds the document variables read.
const maxVariables = 1 << 14

// readVariables reads the document variables (StwUser, [MS-DOC] 2.9.298):
// a table of their names, each with four bytes beside it, and then their
// values in the same order, each a count of characters and the
// characters.
func readVariables(b []byte) []variable {
	if len(b) < 6 || le.Uint16(b) != 0xFFFF {
		return nil
	}
	count, extra := int(le.Uint16(b[2:])), int(le.Uint16(b[4:]))
	b = b[6:]
	var out []variable
	for i := 0; i < count && len(b) >= 2 && len(out) < maxVariables; i++ {
		n := int(le.Uint16(b))
		b = b[2:]
		if 2*n+extra > len(b) {
			return nil
		}
		out = append(out, variable{name: wide(b[:2*n])})
		b = b[2*n+extra:]
	}
	for i := range out {
		if len(b) < 2 {
			break
		}
		n := int(le.Uint16(b))
		b = b[2:]
		if 2*n > len(b) {
			break
		}
		out[i].value = wide(b[:2*n])
		b = b[2*n:]
	}
	return out
}

// Noun names the parts of a Word document.
func (d *Doc) Noun() string { return "parts" }

// Begin starts a pass over the document.
func (d *Doc) Begin() {}

// Limited reports whether a limit cut short what the parts share.
func (d *Doc) Limited() bool { return false }

// Read reads the part with the given index.
func (d *Doc) Read(ctx context.Context, index int) *unit.Result {
	if index == d.Stored() {
		part := stored.NewPart(ctx, index+1)
		for _, v := range d.variables {
			name, _ := clean.String([]byte(v.name), clean.Prose)
			value, _ := clean.String([]byte(v.value), clean.Prose)
			name = part.Lost.Name(name)
			if name == "" {
				continue
			}
			if !part.Add(gofastcore.Block{Kind: gofastcore.KindParagraph, Text: name + ": " + value,
				Attrs: map[string]string{gofastcore.AttrStored: gofastcore.StoredDocumentVariable, gofastcore.AttrName: name}}) {
				break
			}
		}
		if d.cfg.Stored != nil {
			d.cfg.Stored(ctx, part)
		}
		limited := part.Finish()
		return &unit.Result{Part: part.Part, Tally: unit.Tally{Damaged: part.Damaged, Limited: limited}}
	}
	r := &reader{d: d, ctx: ctx, room: gofastcore.TextBudgetFrom(ctx)}
	r.papx, r.chpx = d.papxBins, d.chpxBins // each reading has its own place in the pages
	part := gofastcore.Part{Kind: gofastcore.PartBody, Number: index + 1}
	part.Blocks = r.body()
	if len(d.txt.pieces) == 0 {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the document's text cannot be found: its piece table cannot be read")
	}
	if r.cut {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, warn.TextCut("document"))
	}
	if r.budget {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the decompressed size limit was reached; pictures from there on are named and not read")
	}
	limited := r.cut || r.budget
	if r.lost.Say(&part) {
		limited = true
	}
	if r.clamped > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnHeadingLevelClamped, strconv.Itoa(r.clamped)+" headings deeper than level 6 are given at level 6, with their level as an attribute")
	}
	if r.tracked {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnTextTrackedChanges, "the document has tracked changes; inserted text is in the text, and deleted text is given as notes")
	}
	if r.unmapped > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs, warn.Unmapped(r.unmapped))
	}
	if r.removed > 0 {
		part.Warn(gofastcore.SeverityInfo, warn.ControlCharsRemoved, strconv.Itoa(r.removed))
	}
	hasText, hasImage := false, r.sawImage
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
	part.Finish()
	return &unit.Result{Part: part, Tally: unit.Tally{Limited: limited, Unread: r.unread}}
}

// Summary turns what was found while the document was read into warnings
// about it.
func (d *Doc) Summary(tallies []*unit.Tally) []gofastcore.Warning {
	return unit.Summarize(tallies, d.damaged)
}

// OpenStreams reads the file information block and opens the document.
// The error is for a WordDocument stream too short to hold one.
func OpenStreams(stream []byte, table func(name string) []byte, opts *gofastcore.Options, cfg Config) (*Doc, error) {
	f, err := readFIB(stream)
	if err != nil {
		return nil, err
	}
	return Open(f, stream, table(f.table), opts, cfg), nil
}

// between returns the notes referred to at character positions from lo up
// to hi, as indexes into refs.
func (n *notes) between(lo, hi int64) (from, to int) {
	from = sort.Search(len(n.refs), func(i int) bool { return n.refs[i] >= lo })
	to = sort.Search(len(n.refs), func(i int) bool { return n.refs[i] >= hi })
	return from, to
}

// name is clean.Label for a name the file gives.
func name(s string) string {
	t, _ := clean.String([]byte(s), clean.Prose)
	return clean.Label(t)
}
