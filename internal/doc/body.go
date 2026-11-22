package doc

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/hidden"
	"github.com/AOShei/go-fast-msbin/internal/symbol"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// The characters that mean something other than themselves ([MS-DOC]
// 2.4.1 and after).
const (
	chPicture  = 0x01 // a picture or an object, where the run is special
	chNoteRef  = 0x02 // the number of a footnote or endnote, where special
	chAtnRef   = 0x05 // the mark of a comment, where special
	chCell     = 0x07 // the end of a table cell, or of a row
	chShape    = 0x08 // a drawn object, where special
	chTab      = 0x09
	chLine     = 0x0B // a line break inside a paragraph
	chPage     = 0x0C // a page break, or the end of a section
	chPara     = 0x0D
	chFieldBeg = 0x13 // where special: a field begins, its code follows
	chFieldSep = 0x14 // its result follows
	chFieldEnd = 0x15
	chNBHyphen = 0x1E
	chOptional = 0x1F // a hyphen shown only at the end of a line
	chSymbol   = 0x28 // where special and the run names a symbol: that symbol
)

// attrs is what a span says of its text.
type attrs struct {
	style   gofastcore.Style
	href    string
	display gofastcore.Display
}

// field is a field being read: its code, and once the code has ended,
// where its result leads if it is a link.
type field struct {
	code     []byte
	inResult bool
	href     string
}

// Bounds on fields.
const (
	maxFieldDepth = 64
	maxFieldCode  = 1 << 16
)

// deletion is text a tracked change removed.
type deletion struct {
	text []byte
	by   uint16
	when uint32
}

// reader reads the stories of a document into blocks.
type reader struct {
	d    *Doc
	ctx  context.Context
	room *gofastcore.TextBudget
	papx bins
	chpx bins
	num  *numState
	root *reader // the reader of the body, for one that reads a note

	lost     warn.Lost
	cut      bool
	clamped  int
	tracked  bool
	unmapped int
	removed  int
	unread   map[string]int

	// The paragraph being read.
	buf      []byte
	spans    []gofastcore.Span
	cur      attrs
	curStart int
	high     rune // the first half of a surrogate pair
	dels     []deletion
	fields   []field
	codes    []string // the codes of the fields that ended in it
	inCell   bool     // it is in a table cell: its text is charged with the table

	// The properties of the run read last, by what they were made from.
	lastStyle *style
	lastGrp   *byte
	lastLen   int
	lastPrm   uint16
	lastChp   chp

	ftnSeen, ednSeen int // notes numbered so far

	pics     []uint32     // pictures in the line of the paragraph being read: where each is in the Data stream
	boxDone  map[int]bool // the text boxes given
	sawImage bool         // the document has pictures that were not asked for
	budget   bool         // the limit on decoded bytes was reached
}

// body reads the main text, with the headers before it and the footers
// after.
func (r *reader) body() []gofastcore.Block {
	r.num = &numState{}
	headers, footers := r.headersFooters()
	out := append(headers, r.story(0, r.d.f.ccpText)...)
	// The text boxes that no shape anchored in the text holds: after the
	// text.
	if dr := r.d.drawing; dr != nil {
		base := r.d.ednAt + r.d.f.ccpEdn
		for i := 0; i+1 < len(dr.boxes) && !r.cut; i++ {
			lo, hi := base+dr.boxes[i], base+dr.boxes[i+1]
			if r.boxDone[i] || hi <= lo || hi > base+r.d.f.ccpTxbx {
				continue
			}
			c := r.child()
			out = append(out, c.story(lo, hi)...)
			r.merge(c)
		}
	}
	return append(out, footers...)
}

// child returns a reader for a story inside the one r reads: a note's
// text, a header.
func (r *reader) child() *reader {
	root := r
	if r.root != nil {
		root = r.root
	}
	return &reader{d: r.d, ctx: r.ctx, room: r.room, papx: r.papx, chpx: r.chpx, num: r.num, root: root}
}

// merge adds what a child found to its root.
func (r *reader) merge(c *reader) {
	r.cut = r.cut || c.cut
	r.clamped += c.clamped
	r.tracked = r.tracked || c.tracked
	r.unmapped += c.unmapped
	r.removed += c.removed
	r.lost.Merge(&c.lost)
}

// plain reads a story as text: its paragraphs on lines of their own.
func (r *reader) plain(from, to int64) string {
	c := r.child()
	blocks := c.story(from, to)
	r.merge(c)
	var sb strings.Builder
	for _, b := range blocks {
		if b.Text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(b.Text)
	}
	return sb.String()
}

// headersFooters reads the headers and footers, each once however many
// sections have it ([MS-DOC] 2.3.4): after six stories that are the lines
// above footnotes, each section has six: the header of even pages, of odd
// pages, the footers of both, and the header and footer of its first
// page.
func (r *reader) headersFooters() (headers, footers []gofastcore.Block) {
	hdd := r.d.hdd
	seen := map[string]bool{}
	for i := 6; i+1 < len(hdd); i++ {
		from, to := r.d.hddAt+hdd[i], r.d.hddAt+hdd[i+1]
		if to <= from || to > r.d.hddAt+r.d.f.ccpHdd {
			continue
		}
		kind := gofastcore.KindHeader
		if k := (i - 6) % 6; k == 2 || k == 3 || k == 5 {
			kind = gofastcore.KindFooter
		}
		c := r.child()
		add := func(b gofastcore.Block) {
			if b.Kind == gofastcore.KindNote {
				// The code of a field of a header is a note of its own.
				if kind == gofastcore.KindHeader {
					headers = append(headers, b)
				} else {
					footers = append(footers, b)
				}
				return
			}
			if b.Text == "" {
				return
			}
			key := kind.String() + "\x00" + b.Text
			if seen[key] {
				return
			}
			seen[key] = true
			b.Kind, b.Level, b.Marker, b.Ordered, b.Attrs = kind, 0, "", false, nil
			if kind == gofastcore.KindHeader {
				headers = append(headers, b)
			} else {
				footers = append(footers, b)
			}
		}
		for _, b := range c.story(from, to) {
			if b.Kind != gofastcore.KindTable {
				add(b)
				continue
			}
			// A table in a header: the paragraphs of its cells, each a
			// line of the header.
			for _, row := range b.Table.Rows {
				for _, cell := range row {
					for _, line := range strings.Split(cell, "\n") {
						add(gofastcore.Block{Kind: kind, Text: strings.TrimSpace(line)})
					}
				}
			}
		}
		r.merge(c)
	}
	return headers, footers
}

// paraEnd finds the character that ends the paragraph beginning at cp:
// its position, and the character. Where the story ends first, the
// position is limit and the character zero.
func (r *reader) paraEnd(cp, limit int64) (int64, uint16) {
	t := &r.d.txt
	for i := t.find(cp); i >= 0 && i < len(t.pieces) && cp < limit; i++ {
		p := &t.pieces[i]
		if cp < p.cp {
			cp = p.cp // a gap between pieces
		}
		if cp >= limit {
			break
		}
		end := min(p.end(), limit)
		if p.narrow {
			b := t.stream[p.fcOf(cp):p.fcOf(end)]
			for k, c := range b {
				if c == chPara || c == chCell || c == chPage {
					return cp + int64(k), uint16(c)
				}
			}
		} else {
			b := t.stream[p.fcOf(cp):p.fcOf(end)]
			for k := 0; k+1 < len(b); k += 2 {
				if b[k+1] == 0 && (b[k] == chPara || b[k] == chCell || b[k] == chPage) {
					return cp + int64(k/2), uint16(b[k])
				}
			}
		}
		cp = end
	}
	return limit, 0
}

// paraProps returns the properties of the paragraph that the character at
// cp ends, and its style.
func (r *reader) paraProps(cp int64) (pap, *style) {
	t := &r.d.txt
	p := pap{outline: 9}
	i := t.find(cp)
	if i < 0 {
		return p, nil
	}
	pc := &t.pieces[i]
	istd, grpprl := r.papx.papx(t.stream, pc.fcOf(cp))
	st := r.d.styles.get(istd)
	if st != nil {
		p = st.pap
		p.table = nil
	}
	// Changes too long for a page of properties are in the Data stream,
	// and the page says where ([MS-DOC] 2.6.2, sprmPHugePapx).
	eachSprm(grpprl, func(op uint16, arg []byte) bool {
		if op == sprmPHugePapx || op == sprmPHugePapx2 {
			data := r.d.dataStream()
			if at := int64(le.Uint32(arg)); at+2 <= int64(len(data)) {
				if n := int64(le.Uint16(data[at:])); at+2+n <= int64(len(data)) {
					grpprl = data[at+2 : at+2+n]
				}
			}
			return false
		}
		return true
	})
	p.apply(grpprl)
	if prc := r.modifier(pc.prm); prc != nil {
		p.apply(prc)
	}
	return p, st
}

// modifier returns the changes a piece of a file saved fast adds to the
// properties of its text, where it names a list of them.
func (r *reader) modifier(prm uint16) []byte {
	if prm&1 == 0 {
		return nil // one change, in a form of its own: not read
	}
	if i := int(prm >> 1); i < len(r.d.prcs) {
		return r.d.prcs[i]
	}
	return nil
}

// charProps returns the properties of a run of characters: its paragraph
// style's, then its character style's, then its own changes.
func (r *reader) charProps(st *style, grpprl []byte, prm uint16) chp {
	var first *byte
	if len(grpprl) > 0 {
		first = &grpprl[0]
	}
	if st == r.lastStyle && first == r.lastGrp && len(grpprl) == r.lastLen && prm == r.lastPrm && (st != nil || first != nil) {
		return r.lastChp
	}
	var base chp
	if st != nil {
		base = st.chp
	}
	c := base
	c.apply(grpprl, &base)
	if c.istd != 0 {
		// A character style: what it and the styles it is based on
		// change, under the run's own changes.
		var chain [8][]byte
		n := 0
		for cs, depth := r.d.styles.get(c.istd), 0; cs != nil && cs.kind == 2 && depth < len(chain); depth++ {
			chain[n] = cs.chpx
			n++
			if cs.base == noBase {
				break
			}
			cs = r.d.styles.get(cs.base)
		}
		if n > 0 {
			c = base
			for k := n - 1; k >= 0; k-- {
				c.apply(chain[k], &base)
			}
			c.apply(grpprl, &base)
		}
	}
	if prc := r.modifier(prm); prc != nil {
		c.apply(prc, &base)
	}
	r.lastStyle, r.lastGrp, r.lastLen, r.lastPrm, r.lastChp = st, first, len(grpprl), prm, c
	return c
}

// attr sets what the text from here on is to be marked with.
func (r *reader) attr(a attrs) {
	if a == r.cur {
		return
	}
	r.closeSpan()
	r.cur, r.curStart = a, len(r.buf)
}

func (r *reader) closeSpan() {
	if r.cur != (attrs{}) && len(r.buf) > r.curStart {
		r.spans = append(r.spans, gofastcore.Span{Start: r.curStart, End: len(r.buf), Style: r.cur.style, Href: r.cur.href, Display: r.cur.display})
	}
	r.curStart = len(r.buf)
}

// put appends a character to the paragraph's text.
func (r *reader) put(u rune) {
	if u >= 0xD800 && u < 0xDC00 {
		r.flushHigh()
		r.high = u
		return
	}
	if u >= 0xDC00 && u < 0xE000 {
		if r.high != 0 {
			r.buf = utf8.AppendRune(r.buf, 0x10000+(r.high-0xD800)<<10+(u-0xDC00))
			r.high = 0
			return
		}
		r.buf = append(r.buf, 0xEF, 0xBF, 0xBD)
		return
	}
	r.flushHigh()
	if u == 0x7F || u >= 0x80 && u <= 0x9F {
		r.removed++ // a control character, which is no text
		return
	}
	if u < 0x80 {
		r.buf = append(r.buf, byte(u))
		return
	}
	if u >= 0xFB00 && u <= 0xFB06 {
		if s := gofastcore.Ligature(u); s != "" {
			r.buf = append(r.buf, s...)
			return
		}
	}
	r.buf = utf8.AppendRune(r.buf, u)
}

func (r *reader) flushHigh() {
	if r.high != 0 {
		r.buf = append(r.buf, 0xEF, 0xBF, 0xBD)
		r.high = 0
	}
}

// run reads the characters from cp up to end, which are in the piece p
// and have the properties c.
func (r *reader) run(cp, end int64, p *piece, c *chp) {
	stream := r.d.txt.stream
	var a attrs
	if r.d.opts != nil && r.d.opts.Spans {
		if c.bold {
			a.style |= gofastcore.StyleBold
		}
		if c.italic {
			a.style |= gofastcore.StyleItalic
		}
		if c.underline {
			a.style |= gofastcore.StyleUnderline
		}
		if c.strike {
			a.style |= gofastcore.StyleStrike
		}
		if c.super {
			a.style |= gofastcore.StyleSuper
		}
		if c.sub {
			a.style |= gofastcore.StyleSub
		}
	}
	if c.vanish {
		a.display = gofastcore.DisplayHidden
	}
	font := symbol.None
	if int(c.font) < len(r.d.symbols) {
		font = r.d.symbols[c.font]
	}
	if c.ins || c.del {
		r.tracked = true
	}
	// Most text is plain: one byte to a character, nothing special about
	// the run, no field's code being read.
	plain := p.narrow && !c.spec && !c.del && font == symbol.None && len(r.fields) == 0
	if plain {
		r.attr(a)
		r.flushHigh()
		for _, b := range stream[p.fcOf(cp):p.fcOf(end)] {
			switch {
			case b >= 0x20 && b < 0x7F:
				r.buf = append(r.buf, b)
			case b >= 0x80:
				if u := cp1252[b]; u > 0x9F {
					r.buf = utf8.AppendRune(r.buf, u)
				} else {
					r.removed++ // a byte the code page gives no character
				}
			case b == 0x7F:
				r.removed++
			case b == chTab:
				r.buf = append(r.buf, '\t')
			case b == chLine:
				r.buf = append(r.buf, '\n')
			case b == chNBHyphen:
				r.buf = append(r.buf, 0xE2, 0x80, 0x91) // U+2011, a hyphen that does not break
			case b == chOptional:
				r.buf = append(r.buf, 0xC2, 0xAD) // U+00AD, a soft hyphen
			}
		}
		return
	}
	for ; cp < end; cp++ {
		var u rune
		at := p.fcOf(cp)
		if p.narrow {
			u = cp1252[stream[at]]
		} else {
			u = rune(le.Uint16(stream[at:]))
		}
		// The marks of a field.
		if c.spec {
			switch u {
			case chFieldBeg:
				if len(r.fields) >= maxFieldDepth {
					r.lost.Add(warn.CapFieldDepth, 1)
				}
				r.fields = append(r.fields, field{})
				continue
			case chFieldSep:
				if n := len(r.fields); n > 0 && !r.fields[n-1].inResult {
					f := &r.fields[n-1]
					f.inResult = true
					f.href = hyperlink(f.code)
				}
				continue
			case chFieldEnd:
				if n := len(r.fields); n > 0 {
					f := r.fields[n-1]
					r.fields = r.fields[:n-1]
					// A link's code is where it leads, which the text it
					// shows carries as a link: it is no note. An .docx
					// file keeps a link as an element, not as a field.
					if code := strings.TrimSpace(string(f.code)); code != "" && n <= maxFieldDepth && hyperlink(f.code) == "" {
						r.codes = append(r.codes, code)
					}
				}
				continue
			}
		}
		// A field's code is not text of the paragraph.
		if n := len(r.fields); n > 0 {
			inCode := false
			href := ""
			for k := range r.fields {
				inCode = inCode || !r.fields[k].inResult
				if r.fields[k].href != "" {
					href = r.fields[k].href
				}
			}
			if inCode {
				f := &r.fields[n-1]
				for k := n - 1; k >= 0; k-- {
					if !r.fields[k].inResult {
						f = &r.fields[k]
						break
					}
				}
				if u >= 0x20 && !c.spec && u < 0xD800 {
					if len(f.code) < maxFieldCode {
						f.code = utf8.AppendRune(f.code, u)
					} else if len(f.code) == maxFieldCode {
						r.lost.Add(warn.CapFieldCode, 1)
						f.code = append(f.code, ' ')
					}
				}
				continue
			}
			if r.d.opts != nil && r.d.opts.Spans {
				a.href = href
			}
		}
		if c.spec {
			// A special character: a symbol stands for a character of a
			// symbol font; the others stand for what is read elsewhere (a
			// note, a picture, an object) or for nothing that is text.
			switch {
			case u == chSymbol && c.sym:
				r.attr(a)
				r.symbol(c.symFont, rune(c.symChar))
			case u == chPicture && c.hasPic && !c.data && len(r.pics) < 1<<12:
				r.pics = append(r.pics, c.pic)
			}
			continue
		}
		if c.del {
			// Text a tracked change removed: given beside the paragraph,
			// not in it.
			if n := len(r.dels); n == 0 || r.dels[n-1].by != c.delBy || r.dels[n-1].when != c.delWhen {
				r.dels = append(r.dels, deletion{by: c.delBy, when: c.delWhen})
			}
			d := &r.dels[len(r.dels)-1]
			switch {
			case u == chTab:
				d.text = append(d.text, '\t')
			case u == chLine:
				d.text = append(d.text, '\n')
			case u >= 0x20 && (u < 0xD800 || u >= 0xE000):
				d.text = utf8.AppendRune(d.text, u)
			}
			continue
		}
		r.attr(a)
		switch {
		case u >= 0x20:
			if font != symbol.None {
				r.symbolIn(font, u)
			} else {
				r.put(u)
			}
		case u == chTab:
			r.put('\t')
		case u == chLine:
			r.put('\n')
		case u == chNBHyphen:
			r.put(0x2011)
		case u == chOptional:
			r.put(0x00AD)
		}
	}
}

// symbol appends a character of the symbol font with the given number.
func (r *reader) symbol(fontNumber uint16, code rune) {
	font := symbol.None
	if int(fontNumber) < len(r.d.symbols) {
		font = r.d.symbols[fontNumber]
	}
	if font == symbol.None {
		// A font that is not one of the symbol fonts known here: the
		// character is as likely a letter of it.
		if code >= 0xF000 && code <= 0xF0FF {
			code -= 0xF000
		}
		if code >= 0x20 {
			r.put(code)
		}
		return
	}
	r.symbolIn(font, code)
}

// symbolIn appends the character a code of a symbol font stands for.
func (r *reader) symbolIn(font symbol.Font, code rune) {
	if code >= 0xF000 && code <= 0xF0FF {
		code -= 0xF000
	}
	if code > 0xFF {
		r.put(code) // not a code of the font: a character of its own
		return
	}
	if ch, ok := font.Rune(byte(code)); ok {
		r.put(ch)
		return
	}
	r.unmapped++
	r.put(0xFFFD)
}

// hyperlink returns where a field leads if its code is that of a link:
// HYPERLINK "address" \l "place".
func hyperlink(code []byte) string {
	s := strings.TrimSpace(string(code))
	if len(s) < 9 || !strings.EqualFold(s[:9], "HYPERLINK") {
		return ""
	}
	s = s[9:]
	// The arguments: quoted strings, and switches that begin with a
	// backslash; the switch \l is followed by a place in the target.
	var target, place string
	for len(s) > 0 {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			break
		}
		var word string
		if s[0] == '"' {
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				word, s = s[1:], ""
			} else {
				word, s = s[1:1+end], s[2+end:]
			}
		} else {
			end := strings.IndexAny(s, " \t")
			if end < 0 {
				word, s = s, ""
			} else {
				word, s = s[:end], s[end:]
			}
			if strings.HasPrefix(word, `\`) {
				if strings.EqualFold(word, `\l`) {
					s = strings.TrimLeft(s, " \t")
					if strings.HasPrefix(s, `"`) {
						if end := strings.IndexByte(s[1:], '"'); end >= 0 {
							place, s = s[1:1+end], s[2+end:]
						} else {
							place, s = s[1:], ""
						}
					} else if end := strings.IndexAny(s, " \t"); end >= 0 {
						place, s = s[:end], s[end:]
					} else {
						place, s = s, ""
					}
				}
				continue
			}
		}
		if target == "" {
			target = strings.ReplaceAll(word, `\\`, `\`)
		}
	}
	if place != "" {
		target += "#" + place
	}
	return clean.Address(target)
}

// dttm reads a date and time as Word packs it into 32 bits ([MS-DOC]
// 2.9.52): minutes, hours, the day of the month, the month, the years
// since 1900, the day of the week. It is local time with no zone; it is
// given as written, in ISO 8601 with no zone. Zero is no date.
func dttm(v uint32) string {
	if v == 0 {
		return ""
	}
	minute, hour, day := int(v&0x3F), int(v>>6&0x1F), int(v>>11&0x1F)
	month, year := int(v>>16&0xF), 1900+int(v>>20&0x1FF)
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 {
		return ""
	}
	return time.Date(year, time.Month(month), day, hour, minute, 0, 0, time.UTC).Format("2006-01-02T15:04:05")
}

// after returns the blocks that follow the paragraph just read, whose
// characters were cp0 up to end: the text its tracked changes removed,
// the codes of its fields, and the notes it refers to.
func (r *reader) after(cp0, end int64) []gofastcore.Block {
	var out []gofastcore.Block
	add := func(b gofastcore.Block) {
		if b.Text == "" {
			return
		}
		if !r.room.Take(gofastcore.BlockCost + len(b.Text)) {
			r.cut = true
			return
		}
		out = append(out, b)
	}
	for _, d := range r.dels {
		text, removed := clean.String(d.text, clean.Prose)
		r.removed += removed
		b := gofastcore.Block{Kind: gofastcore.KindNote, Text: text, Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteDeletion}}
		if int(d.by) < len(r.d.revisers) {
			if who := name(r.d.revisers[d.by]); who != "" {
				b.Attrs[gofastcore.AttrAuthor] = who
			}
		}
		if when := dttm(d.when); when != "" {
			b.Attrs[gofastcore.AttrDate] = when
		}
		add(b)
	}
	r.dels = r.dels[:0]
	for _, code := range r.codes {
		code, removed := clean.String([]byte(code), clean.Prose)
		r.removed += removed
		add(gofastcore.Block{Kind: gofastcore.KindNote, Text: code,
			Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteField, gofastcore.AttrStored: gofastcore.StoredFieldCode}})
	}
	r.codes = r.codes[:0]
	// Pictures in the line of the text.
	for _, at := range r.pics {
		if b, ok := r.inline(at); ok {
			out = append(out, b)
		}
	}
	r.pics = r.pics[:0]
	if r.root != nil {
		return out // notes are referred to from the main text only
	}
	out = append(out, r.anchored(cp0, end)...)
	d := r.d
	// Footnotes and endnotes: numbered in the order they are referred to,
	// those the program numbers.
	note := func(n *notes, base, length int64, seen *int, roman bool) {
		from, to := n.between(cp0, end+1)
		for i := from; i < to && i+1 < len(n.text); i++ {
			lo, hi := base+n.text[i], base+n.text[i+1]
			if hi > base+length || hi <= lo {
				continue
			}
			// A note the program numbers has its number; one with a mark
			// of its author's begins with that mark, in its own text.
			marker := ""
			if n.auto[i] {
				*seen++
				marker = strconv.Itoa(*seen)
				if roman {
					marker = strings.ToLower(formatNumber(*seen, "lowerRoman"))
				}
			}
			add(gofastcore.Block{Kind: gofastcore.KindFootnote, Text: r.plain(lo, hi), Marker: marker})
		}
	}
	note(&d.ftn, d.ftnAt, d.f.ccpFtn, &r.ftnSeen, false)
	note(&d.edn, d.ednAt, d.f.ccpEdn, &r.ednSeen, true)
	from, to := d.atn.between(cp0, end+1)
	for i := from; i < to && i+1 < len(d.atn.text); i++ {
		lo, hi := d.atnAt+d.atn.text[i], d.atnAt+d.atn.text[i+1]
		if hi > d.atnAt+d.f.ccpAtn || hi <= lo {
			continue
		}
		b := gofastcore.Block{Kind: gofastcore.KindNote, Text: r.plain(lo, hi), Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteComment}}
		if who := int(d.atnAuthor[i]); who < len(d.authors) {
			if author := name(d.authors[who]); author != "" {
				b.Attrs[gofastcore.AttrAuthor] = author
			}
		}
		add(b)
	}
	return out
}

// story reads the characters from cp up to limit into blocks.
func (r *reader) story(cp, limit int64) []gofastcore.Block {
	var out []gofastcore.Block
	var tbl *tableBuilder
	var pending []gofastcore.Block // what follows paragraphs of a table, given after it
	t := &r.d.txt
	flush := func() {
		if tbl != nil {
			if b, ok := tbl.block(r); ok {
				out = append(out, b)
			}
			tbl = nil
		}
		out = append(out, pending...)
		pending = nil
	}
	for n := 0; cp < limit && !r.cut; n++ {
		if n&255 == 255 && r.ctx.Err() != nil {
			break
		}
		if t.find(cp) < 0 {
			// Past the pieces, or in a gap between two: the next piece.
			next := limit
			for i := range t.pieces {
				if t.pieces[i].cp > cp {
					next = t.pieces[i].cp
					break
				}
			}
			if next >= limit {
				break
			}
			cp = next
			continue
		}
		end, mark := r.paraEnd(cp, limit)
		p, st := r.paraProps(min(end, limit-1))
		inTable := p.inTable || p.itap > 0
		r.inCell = inTable
		r.chars(cp, end, st)
		b, ok := r.finish(&p, st, cp, end)
		notes := r.after(cp, end)
		switch {
		case inTable:
			if tbl == nil {
				tbl = &tableBuilder{}
			}
			switch {
			case p.itap > 1:
				// A table inside a cell: its text is the cell's, its cells
				// parted by tabs and its rows by line breaks.
				if !p.innerTtp {
					tbl.text(b.Text, ok)
					if p.innerCell {
						tbl.join = "\t"
					}
				}
			case p.ttp:
				tbl.row(p.table, p.header)
			default:
				tbl.text(b.Text, ok)
				if mark == chCell {
					tbl.cell()
				}
			}
			pending = append(pending, notes...)
		default:
			flush()
			if ok {
				r.emit(&out, b)
			}
			out = append(out, notes...)
		}
		cp = end + 1
	}
	flush()
	return out
}

// emit appends a block. Consecutive paragraphs of code are one block.
func (r *reader) emit(out *[]gofastcore.Block, b gofastcore.Block) {
	if n := len(*out); n > 0 && b.Kind == gofastcore.KindCode && (*out)[n-1].Kind == gofastcore.KindCode &&
		b.Display == 0 && (*out)[n-1].Display == 0 {
		last := &(*out)[n-1]
		at := len(last.Text) + 1
		last.Text += "\n" + b.Text
		for _, span := range b.Spans {
			span.Start += at
			span.End += at
			last.Spans = append(last.Spans, span)
		}
		return
	}
	*out = append(*out, b)
}

// chars reads the characters of a paragraph, from cp up to end, into the
// paragraph's text.
func (r *reader) chars(cp, end int64, st *style) {
	t := &r.d.txt
	r.buf, r.spans = r.buf[:0], r.spans[:0]
	r.cur, r.curStart, r.high = attrs{}, 0, 0
	for cp < end {
		i := t.find(cp)
		if i < 0 {
			break
		}
		p := &t.pieces[i]
		fc := p.fcOf(cp)
		grpprl, endFC := r.chpx.chpx(t.stream, fc)
		runEnd := min(p.cp+(endFC-p.fc)/p.width(), p.end(), end)
		if runEnd <= cp {
			runEnd = cp + 1
		}
		c := r.charProps(st, grpprl, p.prm)
		r.run(cp, runEnd, p, &c)
		cp = runEnd
	}
	r.flushHigh()
	r.closeSpan()
}

// finish makes the block of the paragraph just read. ok is false for a
// paragraph with no text, which is spacing.
func (r *reader) finish(p *pap, st *style, cp0, end int64) (gofastcore.Block, bool) {
	raw := r.buf
	// Space at the ends is not text of the paragraph.
	left := bytes.TrimLeftFunc(raw, unicode.IsSpace)
	from := len(raw) - len(left)
	to := from + len(bytes.TrimRightFunc(left, unicode.IsSpace))
	listed := p.ilfo > 0 && p.ilfo != 0x07FF
	if from >= to {
		// An empty paragraph is spacing. A numbered one still counts in
		// its list, as it does in Word.
		if listed {
			r.label(p, st)
		}
		return gofastcore.Block{}, false
	}
	// Every paragraph is counted, wherever it stands. A paragraph the
	// document has no room for ends the reading; what was read is kept.
	cost := to - from
	if !r.inCell {
		cost += gofastcore.BlockCost // a cell's paragraphs are charged with its table
	}
	if !r.room.Take(cost) {
		r.cut = true
		return gofastcore.Block{}, false
	}
	b := gofastcore.Block{Kind: gofastcore.KindParagraph, Text: string(raw[from:to])}
	b.Spans, b.Display = hidden.Settle(r.spans, from, to, raw)
	// The bookmark that starts the paragraph is its anchor.
	if mark, ok := r.d.bookmarks[cp0]; ok && r.root == nil {
		b.Loc.Anchor = r.lost.Label(name(mark))
	}
	var marker string
	var ordered, numbered bool
	if listed {
		marker, ordered, numbered = r.label(p, st)
	}
	kind, outline, nested := roleNone, int8(-1), uint8(0)
	if st != nil {
		kind, outline, nested = st.role, st.outline, st.nested
	}
	if p.outline < 9 {
		outline = int8(p.outline)
	}
	switch {
	case kind == roleTitle:
		b.Kind, b.Level = gofastcore.KindHeading, 1
		b.Attrs = map[string]string{"style": "title"}
	case kind == roleSubtitle:
		b.Kind, b.Level = gofastcore.KindHeading, 2
		b.Attrs = map[string]string{"style": "subtitle"}
	case outline >= 0 && outline < 9:
		b.Kind, b.Level = gofastcore.KindHeading, uint8(outline)+1
		if b.Level > 6 {
			// The model has six heading levels; Word has nine.
			b.Attrs = map[string]string{gofastcore.AttrLevel: strconv.Itoa(int(b.Level))}
			r.clamped++
			b.Level = 6
		}
		b.Marker = marker // a numbered heading keeps its number
	case numbered:
		b.Kind, b.Ordered, b.Marker = gofastcore.KindListItem, ordered, marker
		b.Level = 1
		switch {
		case p.ilvl > 0:
			b.Level = p.ilvl + 1
		case nested > 0:
			b.Level = nested
		}
	case kind == roleQuote:
		b.Kind, b.Level = gofastcore.KindQuote, 1
	case kind == roleCode:
		b.Kind = gofastcore.KindCode
	}
	return b, true
}

// label advances the paragraph's list and returns its label.
func (r *reader) label(p *pap, st *style) (marker string, ordered, ok bool) {
	level := int(p.ilvl)
	if level >= maxLevels {
		level = maxLevels - 1
	}
	return r.num.next(r.d.lists, int(p.ilfo), level)
}
