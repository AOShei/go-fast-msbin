package doc

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/charset"
	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-msbin/internal/stored"
	"github.com/AOShei/go-fast-msbin/internal/unit"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// Word 6 and Word 95 keep a document in one stream: the text, one byte
// to a character in the code page of the document's language, and after
// it the structures that Word 97 moved to a stream of their own. What is
// read of such a document is its text, as paragraphs; its headings, by
// the built-in styles; its tables, by the marks that end their cells;
// its footnotes and endnotes; and the results of its fields. Properties
// that are changes to a style (lists, hidden text, tracked changes) are
// in a coding of their own, and are not read.

// Doc6 is a document of Word 6 or 95 opened for reading.
type Doc6 struct {
	stream []byte
	opts   *gofastcore.Options
	cfg    Config
	cp     int // the code page of its text
	pieces []piece
	bins   bins6
	sti    []uint16 // of each style, which built-in style it is
	// The lengths of the stories.
	ccpText, ccpFtn, ccpHdd, ccpAtn, ccpEdn int64
	ftn, edn                                notes
	damaged                                 []string
}

// bins6 is the bin table of paragraph properties of this version: page
// numbers in 16 bits.
type bins6 struct {
	fcs []uint32
	pns []uint16
}

// Places in the file information block of this version.
const (
	fib6FcMin     = 0x18
	fib6CcpText   = 0x34
	fib6Stshf     = 0x60
	fib6FndRef    = 0x68
	fib6FndTxt    = 0x70
	fib6BtePapx   = 0xC0
	fib6Fonts     = 0xD0
	fib6Clx       = 0x160
	fib6EndRef    = 0x1D2
	fib6EndTxt    = 0x1DA
	fib6MinLength = 0x1E2
)

// langCodePage gives the Windows code page of a language, by the low ten
// bits of its identifier.
func langCodePage(lid uint16) int {
	switch lid & 0x3FF {
	case 0x19, 0x02, 0x22, 0x23, 0x2F: // Russian, Bulgarian, Ukrainian, Belarusian, Macedonian
		return 1251
	case 0x05, 0x0E, 0x15, 0x18, 0x1A, 0x1B, 0x24: // Czech, Hungarian, Polish, Romanian, Croatian, Slovak, Slovenian
		return 1250
	case 0x08:
		return 1253
	case 0x1F:
		return 1254
	case 0x0D:
		return 1255
	case 0x01:
		return 1256
	case 0x25, 0x26, 0x27:
		return 1257
	case 0x1E:
		return 874
	case 0x11:
		return 932
	case 0x12:
		return 949
	case 0x04:
		if lid == 0x0404 || lid == 0x0C04 || lid == 0x1404 {
			return 950
		}
		return 936
	}
	return 1252
}

// Open6 opens a document of Word 6 or 95 from its WordDocument stream.
func Open6(stream []byte, opts *gofastcore.Options, cfg Config) (*Doc6, error) {
	if len(stream) < fib6MinLength {
		return nil, errFIB
	}
	d := &Doc6{stream: stream, opts: opts, cfg: cfg}
	d.cp = langCodePage(le.Uint16(stream[6:]))
	long := func(at int) int64 { return max(int64(int32(le.Uint32(stream[at:]))), 0) }
	part := func(at int) []byte {
		fc, lcb := int64(le.Uint32(stream[at:])), int64(le.Uint32(stream[at+4:]))
		if lcb == 0 || fc+lcb > int64(len(stream)) {
			return nil
		}
		return stream[fc : fc+lcb]
	}
	// The fonts say which character set each is in. Text is decoded with
	// one code page for the whole document: where its fonts name one
	// character set other than the Western one, and only one, that set's;
	// otherwise that of the document's language.
	if b := part(fib6Fonts); len(b) > 2 {
		sets := map[byte]bool{}
		for b = b[2:]; len(b) > 6; {
			size := int(b[0]) + 1
			if size > len(b) {
				break
			}
			switch chs := b[4]; chs {
			case 0, 1, 2, 77, 255: // Western, the default, symbols, the Macintosh's, the console's
			default:
				sets[chs] = true
			}
			b = b[size:]
		}
		if len(sets) == 1 {
			for chs := range sets {
				if cp := charset.ForCharset(int(chs)); cp > 0 {
					if _, ok := charset.CodePage(cp); ok {
						d.cp = cp
					}
				}
			}
		}
	}
	d.ccpText, d.ccpFtn, d.ccpHdd = long(fib6CcpText), long(fib6CcpText+4), long(fib6CcpText+8)
	d.ccpAtn, d.ccpEdn = long(fib6CcpText+16), long(fib6CcpText+20)
	total := d.ccpText + d.ccpFtn + d.ccpHdd + long(fib6CcpText+12) + d.ccpAtn + d.ccpEdn + long(fib6CcpText+24) + long(fib6CcpText+28)
	fcMin := int64(le.Uint32(stream[fib6FcMin:]))
	if le.Uint16(stream[10:])&fibComplex != 0 {
		// Saved fast: a piece table, whose pieces are where they say.
		var damaged bool
		d.pieces, _, damaged = readPieces(part(fib6Clx), int64(len(stream)), true)
		if damaged || len(d.pieces) == 0 {
			d.damaged = append(d.damaged, "the piece table")
		}
	} else if fcMin < int64(len(stream)) {
		d.pieces = []piece{{cp: 0, n: min(total, int64(len(stream))-fcMin), fc: fcMin, narrow: true}}
	}
	// The pages of paragraph properties.
	if b := part(fib6BtePapx); len(b) >= 10 {
		n := (len(b) - 4) / 6
		d.bins.fcs, d.bins.pns = make([]uint32, n+1), make([]uint16, n)
		for i := range d.bins.fcs {
			d.bins.fcs[i] = le.Uint32(b[4*i:])
		}
		for i := range d.bins.pns {
			d.bins.pns[i] = le.Uint16(b[4*(n+1)+2*i:])
		}
	}
	// Of each style, which built-in style it is: the low twelve bits of
	// its first word.
	if b := part(fib6Stshf); len(b) >= 4 {
		if head := int(le.Uint16(b)); 2+head <= len(b) && head >= 4 {
			count := min(int(le.Uint16(b[2:])), maxStyles)
			b = b[2+head:]
			d.sti = make([]uint16, count)
			for i := range d.sti {
				d.sti[i] = 0x0FFF
			}
			for i := 0; i < count && len(b) >= 2; i++ {
				size := int(le.Uint16(b))
				b = b[2:]
				if size > len(b) {
					break
				}
				if size >= 2 {
					d.sti[i] = le.Uint16(b) & 0x0FFF
				}
				b = b[size:]
			}
		}
	}
	note := func(ref, txt int) notes {
		var n notes
		var data []byte
		n.refs, data = plc(part(ref), 2)
		if len(n.refs) > 0 {
			n.refs = n.refs[:len(n.refs)-1]
		}
		if len(n.refs) > maxNotes {
			n.refs = n.refs[:maxNotes]
		}
		n.auto = make([]bool, len(n.refs))
		for i := range n.auto {
			n.auto[i] = 2*i+2 > len(data) || le.Uint16(data[2*i:]) != 0
		}
		n.text, _ = plc(part(txt), 0)
		return n
	}
	d.ftn = note(fib6FndRef, fib6FndTxt)
	d.edn = note(fib6EndRef, fib6EndTxt)
	return d, nil
}

// istd returns the style of the paragraph whose last character is at the
// stream offset fc, or -1.
func (d *Doc6) istd(fc int64) int {
	t := &d.bins
	i := sort.Search(len(t.pns), func(i int) bool { return int64(t.fcs[i+1]) > fc })
	if i >= len(t.pns) || int64(t.fcs[i]) > fc {
		return -1
	}
	at := int64(t.pns[i]) * 512
	if at+512 > int64(len(d.stream)) {
		return -1
	}
	page := d.stream[at : at+512]
	n := int(page[511])
	if n == 0 || 4*(n+1)+7*n > 511 {
		return -1
	}
	j := sort.Search(n, func(j int) bool { return int64(le.Uint32(page[4*j+4:])) > fc })
	if j >= n || int64(le.Uint32(page[4*j:])) > fc {
		return -1
	}
	// The run's entry: where its properties are in the page, in pairs of
	// bytes. They begin with their length and the style.
	off := 2 * int(page[4*(n+1)+7*j])
	if off == 0 || off+2 > 511 {
		return 0
	}
	return int(page[off+1])
}

func (d *Doc6) Len() int {
	if d.cfg.HasStored {
		return 2
	}
	return 1
}

func (d *Doc6) Stored() int {
	if d.cfg.HasStored {
		return 1
	}
	return -1
}

func (d *Doc6) Noun() string  { return "parts" }
func (d *Doc6) Begin()        {}
func (d *Doc6) Limited() bool { return false }

func (d *Doc6) Summary(tallies []*unit.Tally) []gofastcore.Warning {
	return unit.Summarize(tallies, d.damaged)
}

// reader6 reads the stories of a document of this version.
type reader6 struct {
	d         *Doc6
	ctx       context.Context
	room      *gofastcore.TextBudget
	cut       bool
	undecoded int
	removed   int
	clamped   int
	codes     []bool // of each field being read, whether its code is: its result follows the code
	seenFtn   int
	seenEdn   int
	inNote    bool
}

// inCode reports whether the code of a field is being read.
func (r *reader6) inCode() bool {
	for _, c := range r.codes {
		if c {
			return true
		}
	}
	return false
}

// Read reads the part with the given index.
func (d *Doc6) Read(ctx context.Context, index int) *unit.Result {
	if index == d.Stored() {
		part := stored.NewPart(ctx, index+1)
		if d.cfg.Stored != nil {
			d.cfg.Stored(ctx, part)
		}
		limited := part.Finish()
		return &unit.Result{Part: part.Part, Tally: unit.Tally{Damaged: part.Damaged, Limited: limited}}
	}
	r := &reader6{d: d, ctx: ctx, room: gofastcore.TextBudgetFrom(ctx)}
	part := gofastcore.Part{Kind: gofastcore.PartBody, Number: index + 1}
	part.Blocks = r.story(0, d.ccpText)
	if len(d.pieces) == 0 {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the document's text cannot be found")
	}
	if r.cut {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, warn.TextCut("document"))
	}
	if r.clamped > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnHeadingLevelClamped, strconv.Itoa(r.clamped)+" headings deeper than level 6 are given at level 6, with their level as an attribute")
	}
	if r.undecoded > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs, strconv.Itoa(r.undecoded)+" characters in code page "+strconv.Itoa(d.cp)+", which it does not define or which is not known here, are U+FFFD")
	}
	if r.removed > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnTextControlCharactersRemoved, strconv.Itoa(r.removed))
	}
	if len(part.Blocks) == 0 && !part.Has(gofastcore.WarnPartFailed) {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "")
	}
	part.Finish()
	return &unit.Result{Part: part, Tally: unit.Tally{Limited: r.cut}}
}

// find returns the piece that holds the character cp, or nil.
func (d *Doc6) find(cp int64) *piece {
	i := sort.Search(len(d.pieces), func(i int) bool { return d.pieces[i].end() > cp })
	if i < len(d.pieces) && d.pieces[i].cp <= cp {
		return &d.pieces[i]
	}
	return nil
}

// story reads the characters from cp up to limit into blocks.
func (r *reader6) story(cp, limit int64) []gofastcore.Block {
	d := r.d
	var out []gofastcore.Block
	var tbl *tableBuilder
	var pending []gofastcore.Block
	var raw []byte    // the bytes of the paragraph being read
	var code []byte   // the bytes of the code of the field being read
	var start = cp    // where it began
	lastCell := false // the paragraph before ended a cell
	flush := func() {
		if tbl != nil {
			if len(tbl.cur) > 0 || tbl.any {
				tbl.row(nil, false)
			}
			if b, ok := tbl.block6(r); ok {
				out = append(out, b)
			}
			tbl = nil
		}
		out = append(out, pending...)
		pending = nil
	}
	for n := 0; cp < limit && !r.cut; n++ {
		if n&4095 == 4095 && r.ctx.Err() != nil {
			break
		}
		p := d.find(cp)
		if p == nil {
			break
		}
		b := d.stream[p.fcOf(cp)]
		cp++
		switch {
		case b == chFieldBeg:
			if len(r.codes) < maxFieldDepth {
				r.codes = append(r.codes, true)
			}
			continue
		case b == chFieldSep:
			if n := len(r.codes); n > 0 {
				r.codes[n-1] = false // its result follows
			}
			continue
		case b == chFieldEnd:
			if n := len(r.codes); n > 0 {
				r.codes = r.codes[:n-1]
			}
			if !r.inCode() {
				code = code[:0]
			}
			continue
		case r.inCode() && b != chPara && b != chCell && b != chPage:
			// A field's code is not text of the paragraph. It is kept
			// until the field ends, in case it does not.
			if b >= 0x20 || b == chTab {
				code = append(code, b)
			}
			continue
		case b == chPara || b == chCell || b == chPage:
		case b == chLine:
			raw = append(raw, '\n')
			continue
		case b == chTab:
			raw = append(raw, '\t')
			continue
		case b == chNBHyphen:
			raw = append(raw, '-')
			continue
		case b < 0x20:
			continue // a mark of something read elsewhere, or of nothing that is text
		default:
			raw = append(raw, b)
			continue
		}
		// The paragraph ends here. A field whose code is still being read
		// does not end: a mark that begins a field and is followed by no
		// other is no field, and what came after it is text.
		if r.inCode() {
			raw = append(raw, code...)
			r.codes = r.codes[:0]
		}
		code = code[:0]
		text, undecoded := charset.DecodeCodePage(d.cp, raw)
		r.undecoded += undecoded
		text, removed := clean.String([]byte(text), clean.Prose)
		r.removed += removed
		raw = raw[:0]
		end := cp - 1
		if b == chCell {
			// A cell ends; a cell mark straight after another ends the
			// row.
			if tbl == nil {
				tbl = &tableBuilder{}
			}
			if lastCell && text == "" {
				tbl.row(nil, false)
				lastCell = false
			} else {
				if text != "" && r.room.Take(len(text)) {
					tbl.text(text, true)
				}
				tbl.cell()
				lastCell = true
			}
			pending = append(pending, r.notes(start, end)...)
			start = cp
			continue
		}
		lastCell = false
		if tbl != nil && len(tbl.cur) > 0 {
			// A paragraph inside a cell.
			if text != "" && r.room.Take(len(text)) {
				tbl.text(text, true)
			}
			pending = append(pending, r.notes(start, end)...)
			start = cp
			continue
		}
		flush()
		if text != "" {
			if !r.room.Take(gofastcore.BlockCost + len(text)) {
				r.cut = true
				break
			}
			blk := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: text}
			// The built-in styles 1 to 9 are the headings.
			if p := d.find(end); p != nil && !r.inNote {
				if istd := d.istd(p.fcOf(end)); istd >= 0 && istd < len(d.sti) {
					if sti := d.sti[istd]; sti >= 1 && sti <= 9 {
						blk.Kind, blk.Level = gofastcore.KindHeading, uint8(sti)
						if blk.Level > 6 {
							blk.Attrs = map[string]string{gofastcore.AttrLevel: strconv.Itoa(int(blk.Level))}
							blk.Level = 6
							r.clamped++
						}
					}
				}
			}
			out = append(out, blk)
		}
		out = append(out, r.notes(start, end)...)
		start = cp
	}
	// Text after the last paragraph mark.
	if len(raw) > 0 && !r.cut {
		text, undecoded := charset.DecodeCodePage(d.cp, raw)
		r.undecoded += undecoded
		text, _ = clean.String([]byte(text), clean.Prose)
		if text != "" && r.room.Take(gofastcore.BlockCost+len(text)) {
			flush()
			out = append(out, gofastcore.Block{Kind: gofastcore.KindParagraph, Text: text})
		}
	}
	flush()
	return out
}

// notes returns the footnotes and endnotes referred to in the paragraph
// whose characters are cp0 up to end.
func (r *reader6) notes(cp0, end int64) []gofastcore.Block {
	if r.inNote {
		return nil
	}
	d := r.d
	var out []gofastcore.Block
	note := func(n *notes, base, length int64, seen *int, roman bool) {
		from, to := n.between(cp0, end+1)
		for i := from; i < to && i+1 < len(n.text); i++ {
			lo, hi := base+n.text[i], base+n.text[i+1]
			if hi > base+length || hi <= lo {
				continue
			}
			marker := ""
			if n.auto[i] {
				*seen++
				marker = strconv.Itoa(*seen)
				if roman {
					marker = strings.ToLower(formatNumber(*seen, "lowerRoman"))
				}
			}
			c := &reader6{d: d, ctx: r.ctx, room: r.room, inNote: true}
			var sb strings.Builder
			for _, b := range c.story(lo, hi) {
				if b.Text == "" {
					continue
				}
				if sb.Len() > 0 {
					sb.WriteByte('\n')
				}
				sb.WriteString(b.Text)
			}
			r.cut, r.undecoded, r.removed = r.cut || c.cut, r.undecoded+c.undecoded, r.removed+c.removed
			if text := strings.TrimFunc(sb.String(), unicode.IsSpace); text != "" {
				out = append(out, gofastcore.Block{Kind: gofastcore.KindFootnote, Text: text, Marker: marker})
			}
		}
	}
	note(&d.ftn, d.ccpText, d.ccpFtn, &r.seenFtn, false)
	note(&d.edn, d.ccpText+d.ccpFtn+d.ccpHdd+d.ccpAtn, d.ccpEdn, &r.seenEdn, true)
	return out
}

// block6 makes the block of a table of a document of this version: its
// rows, each with the cells it has.
func (t *tableBuilder) block6(r *reader6) (gofastcore.Block, bool) {
	cols := 0
	any := false
	for _, row := range t.rows {
		cols = max(cols, len(row.cells))
		for _, c := range row.cells {
			any = any || c != ""
		}
	}
	if !any || cols == 0 {
		return gofastcore.Block{}, false
	}
	rows := len(t.rows)
	if fit := warn.FitRows(r.room, rows, cols); fit < rows {
		r.cut = true
		if fit == 0 {
			return gofastcore.Block{}, false
		}
		rows = fit
	}
	tbl := &gofastcore.Table{Rows: make([][]string, rows)}
	for i := 0; i < rows; i++ {
		tbl.Rows[i] = make([]string, cols)
		copy(tbl.Rows[i], t.rows[i].cells)
	}
	return gofastcore.Block{Kind: gofastcore.KindTable, Table: tbl}, true
}
