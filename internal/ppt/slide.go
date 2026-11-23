package ppt

import (
	"bytes"
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/cfb"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/label"
	"github.com/AOShei/go-fast-msbin/internal/media"
	"github.com/AOShei/go-fast-msbin/internal/officeart"
	"github.com/AOShei/go-fast-msbin/internal/stored"
	"github.com/AOShei/go-fast-msbin/internal/symbol"
	"github.com/AOShei/go-fast-msbin/internal/unit"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// The kinds of text a text header names ([MS-PPT] 2.13.33).
const (
	textTitle       = 0
	textBody        = 1
	textNotes       = 2
	textOther       = 4
	textCenterBody  = 5
	textCenterTitle = 6
	textHalfBody    = 7
	textQuarterBody = 8
)

// The kinds of placeholder ([MS-PPT] 2.13.20).
const (
	phMasterDate   = 7
	phMasterNumber = 8
	phMasterFooter = 9
	phMasterHeader = 10
	phNotesBody    = 12
	phTitle        = 13
	phBody         = 14
	phCenterTitle  = 15
	phSubTitle     = 16
	phVertTitle    = 17
	phVertBody     = 18
)

// rect is a rectangle on a slide, in points.
type rect struct{ x0, y0, x1, y1 float64 }

// para is one paragraph of a shape's text.
type para struct {
	text   string
	level  int
	bullet int8   // 1 the paragraph has a bullet, -1 it has none, 0 it does not say
	mark   string // the bullet's character, if the paragraph says
	font   int    // the bullet's font, by number; -1 where it does not say
	spans  []gofastcore.Span
	ext    int // which of the shape's automatic numberings is its
}

// item is what a shape puts on a slide.
type item struct {
	at          rect
	placed      bool
	placeholder int // the kind of placeholder, or -1
	textKind    int // the kind of its text, or -1
	paras       []para
	blip        int    // the picture it shows, from 1; 0 for none
	object      uint32 // the storage of the embedded object it shows, or 0
	alt         string
	hidden      bool
	rows        [][]string // of a table
	raw         []byte     // the records of its text
	fields      bool       // its text has fields, which each slide fills in
	autos       []auto     // the automatic numbers of its paragraphs, by the run that names them
}

// slideReader reads one slide.
type slideReader struct {
	d         *Deck
	ctx       context.Context
	room      *gofastcore.TextBudget
	ref       *slideRef
	lost      warn.Lost
	cut       bool
	removed   int
	unmapped  int // characters of equations that Unicode has nothing for
	sawImage  bool
	budget    bool
	programs  int
	damaged   []string // what could not be read
	texts     []int    // where each text of the slide begins in the outline
	indexed   bool
	inOutline bool // the text being read is one of the outline's
	number    int  // the slide's number, as the presentation counts
	hf        headFoot
	styles    map[int][]paraStyle // of the slide's master, by kind of text
}

// link is a range of a text that leads somewhere.
type link struct {
	from, to int // in characters of the text
	href     string
}

// charRun is what a run of the styles says of the fonts of its characters.
type charRun struct {
	count     int
	font, sym int // by number; -1 where the run does not say
	ext       int // which entry of the PowerPoint 2000 extension is its
}

// text reads a text out of the records that hold it: a header that says
// what kind it is, the characters in 16 bits or in bytes, the styles of
// its paragraphs and characters, what stands for the fields in it, and
// where it leads.
func (s *slideReader) text(recs []byte, it *item) {
	var units []uint16
	var style []byte
	var meta map[int]string
	var links []link
	href := ""
	have := false
	field := func(r officeart.Rec, v string) {
		if len(r.Body) >= 4 {
			if meta == nil {
				meta = map[int]string{}
			}
			meta[int(int32(le.Uint32(r.Body)))] = v
		}
	}
	for {
		r, rest, ok := officeart.Next(recs)
		if !ok {
			break
		}
		recs = rest
		switch r.Type {
		case rtTextHeaderAtom:
			if have {
				recs = nil // the next text
				break
			}
			if len(r.Body) >= 4 {
				it.textKind = int(le.Uint32(r.Body))
			}
		case rtTextCharsAtom:
			units = make([]uint16, len(r.Body)/2)
			for i := range units {
				units[i] = le.Uint16(r.Body[2*i:])
			}
			have = true
		case rtTextBytesAtom:
			units = make([]uint16, len(r.Body))
			for i, b := range r.Body {
				units[i] = uint16(b)
			}
			have = true
		case rtStyleTextPropAtom:
			style = r.Body
		case rtSlideNumberMC:
			field(r, strconv.Itoa(s.number))
		case rtFooterMC:
			field(r, s.hf.footer)
		case rtHeaderMC:
			field(r, s.hf.header)
		case rtGenericDateMC:
			field(r, s.hf.date)
		case rtDateTimeMC, rtRTFDateTimeMC:
			// The date of the day the slide is shown: the file holds none.
			field(r, "")
		case rtInteractiveInfo:
			href = s.action(r)
		case rtTxInteractiveInfo:
			if len(r.Body) >= 8 && href != "" && len(links) < 1<<12 {
				links = append(links, link{int(int32(le.Uint32(r.Body))), int(int32(le.Uint32(r.Body[4:]))), href})
			}
			href = ""
		case rtOutlineTextRef:
			// The text is in the outline of the presentation: the text
			// with this number among the slide's.
			// A text of the outline names no other.
			if len(r.Body) >= 4 && !s.inOutline {
				if seg := s.outline(int(int32(le.Uint32(r.Body)))); seg != nil {
					s.inOutline = true
					s.text(seg, it)
					s.inOutline = false
					it.autos = s.d.outline9[outlineKey{s.ref.id, it.textKind}]
					return
				}
			}
		}
		if recs == nil {
			break
		}
	}
	if !have {
		return
	}
	it.fields = it.fields || len(meta) > 0
	if s.d.opts == nil || !s.d.opts.Spans {
		links = nil
	}
	// The paragraphs: the text is cut at paragraph marks; each run of the
	// styles says the level of so many characters, and each run after
	// those the fonts of so many.
	levels, rest := paraStyles(style, len(units)+1)
	chars := charRuns(rest, len(units)+1)
	start := 0
	run, left := 0, 0
	if len(levels) > 0 {
		left = levels[0].count
	}
	cr, crLeft := 0, 0
	if len(chars) > 0 {
		crLeft = chars[0].count
	}
	var buf []byte
	var pos []int // where each character of the paragraph starts in buf
	for i := 0; i <= len(units); i++ {
		if i < len(units) && units[i] != 0x0D {
			continue
		}
		p := para{font: -1}
		if run < len(levels) {
			p.level, p.bullet, p.mark, p.font = levels[run].level, levels[run].bullet, levels[run].mark, levels[run].font
		}
		// The fonts the master gives the paragraph's level, for the runs
		// that name none.
		base, baseSym := -1, -1
		if m, ok := s.masterOf(it.textKind, it.placeholder, p.level); ok {
			base, baseSym = m.chFont, m.chSym
		}
		buf, pos = buf[:0], pos[:0]
		var high rune
		p.ext = -1
		for j := start; j < i; j++ {
			for crLeft == 0 && cr < len(chars) {
				if cr++; cr < len(chars) {
					crLeft = chars[cr].count
				}
			}
			font, sym := base, baseSym
			if cr < len(chars) {
				crLeft--
				if p.ext < 0 {
					p.ext = chars[cr].ext
				}
				if chars[cr].font >= 0 {
					font = chars[cr].font
				}
				if chars[cr].sym >= 0 {
					sym = chars[cr].sym
				}
			}
			if links != nil {
				pos = append(pos, len(buf))
			}
			if v, ok := meta[j]; ok {
				buf = append(buf, v...)
				high = 0
				continue
			}
			r := rune(units[j])
			switch {
			case r == 0x0B:
				buf = append(buf, '\n')
			case r == 0x09:
				buf = append(buf, '\t')
			case r < 0x20 || r == 0x7F || r >= 0x80 && r <= 0x9F:
			case r >= 0xD800 && r < 0xDC00:
				high = r
				continue
			case r >= 0xDC00 && r < 0xE000:
				if high != 0 {
					buf = utf8.AppendRune(buf, 0x10000+(high-0xD800)<<10+(r-0xDC00))
				}
			default:
				// A character of a symbol font is a code of that font.
				f := s.d.symbol(font)
				if r >= 0xF000 && s.d.symbol(sym) != symbol.None {
					f = s.d.symbol(sym)
				}
				if code, ok := symbol.Code(r); ok && f != symbol.None {
					if t, ok := f.Rune(code); ok {
						r = t
					} else {
						r = utf8.RuneError
					}
				}
				buf = utf8.AppendRune(buf, r)
			}
			high = 0
		}
		// The paragraph's mark is a character of the runs too.
		for crLeft == 0 && cr < len(chars) {
			if cr++; cr < len(chars) {
				crLeft = chars[cr].count
			}
		}
		if cr < len(chars) {
			crLeft--
		}
		text, removed := clean.String(buf, clean.Prose)
		s.removed += removed
		p.text = text
		if text != "" {
			if links != nil {
				// Where the text leads: the ranges, in bytes of the text
				// as it is after cleaning.
				if off := strings.Index(string(buf), text); off >= 0 {
					pos = append(pos, len(buf))
					for _, l := range links {
						from, to := max(l.from, start), min(l.to, i)
						if from >= to {
							continue
						}
						a, b := min(max(pos[from-start]-off, 0), len(text)), min(max(pos[to-start]-off, 0), len(text))
						for a < b && !utf8.RuneStart(text[a]) {
							a++
						}
						for b < len(text) && !utf8.RuneStart(text[b]) {
							b++
						}
						if a < b {
							p.spans = append(p.spans, gofastcore.Span{Start: a, End: b, Href: l.href})
						}
					}
				}
			}
			it.paras = append(it.paras, p)
		}
		// The characters of the paragraph, its mark among them, are taken
		// from the runs of the styles.
		for n := i - start + 1; n > 0 && run < len(levels); {
			k := min(n, left)
			n, left = n-k, left-k
			if left == 0 {
				run++
				if run < len(levels) {
					left = levels[run].count
				}
			}
		}
		start = i + 1
	}
}

// action reads what a click does (InteractiveInfoAtom, [MS-PPT] 2.8.14):
// it counts one that runs a macro or starts a program, and returns the
// address of one that follows a link.
func (s *slideReader) action(r officeart.Rec) string {
	href := ""
	officeart.Walk(r.Body, func(c officeart.Rec, _ int) bool {
		if c.Type == rtInteractiveInfoAtom && len(c.Body) >= 9 {
			switch c.Body[8] {
			case 1, 2:
				s.programs++
			case 4:
				href = s.d.links[le.Uint32(c.Body[4:])]
			}
		}
		return true
	})
	return href
}

// charRuns reads the character runs of a StyleTextPropAtom, which follow
// its paragraph runs: for each, how many characters, and the fonts its
// mask says follow.
func charRuns(b []byte, total int) []charRun {
	var out []charRun
	for covered := 0; covered < total && len(b) >= 8 && len(out) < 1<<16; {
		cr := charRun{count: int(le.Uint32(b))}
		if cr.count <= 0 {
			break
		}
		var ok bool
		if b, cr.font, cr.sym, cr.ext, ok = readCF(b[8:], le.Uint32(b[4:])); !ok {
			break
		}
		covered += cr.count
		out = append(out, cr)
	}
	return out
}

// outline returns the records of the text with the given number among
// those the outline keeps for the slide.
func (s *slideReader) outline(index int) []byte {
	if !s.indexed {
		// Where each text begins, found once for the slide.
		s.indexed = true
		for b := s.ref.outline; ; {
			r, rest, ok := officeart.Next(b)
			if !ok {
				break
			}
			if r.Type == rtTextHeaderAtom {
				s.texts = append(s.texts, len(s.ref.outline)-len(b))
			}
			b = rest
		}
	}
	if index < 0 || index >= len(s.texts) {
		return nil
	}
	return s.ref.outline[s.texts[index]:]
}

// paraStyle is what a run of the styles says of its paragraphs.
type paraStyle struct {
	count  int
	level  int
	bullet int8
	mark   string
	font   int // the bullet's font, by number; -1 where it does not say
	// The fonts of the characters, which a master's style gives.
	chFont, chSym int
}

// readPF reads the properties of paragraphs that a mask says follow
// ([MS-PPT] 2.9.20, TextPFException), in the order the format gives them,
// into ps, and returns what follows them.
func readPF(b []byte, mask uint32, ps *paraStyle) (rest []byte, ok bool) {
	ok = true
	take := func(n int) []byte {
		if !ok || len(b) < n {
			ok = false
			return nil
		}
		v := b[:n]
		b = b[n:]
		return v
	}
	field := func(bit uint32, n int) []byte {
		if mask&bit == 0 {
			return nil
		}
		return take(n)
	}
	if flags := field(0x000F, 2); flags != nil && mask&0x0001 != 0 {
		ps.bullet = -1
		if le.Uint16(flags)&0x0001 != 0 {
			ps.bullet = 1
		}
	}
	if ch := field(0x0080, 2); ch != nil {
		if r := rune(le.Uint16(ch)); r >= 0x20 && (r < 0xD800 || r > 0xDFFF) {
			ps.mark = string(r)
		}
	}
	if f := field(0x0010, 2); f != nil {
		ps.font = int(le.Uint16(f))
	}
	field(0x0040, 2) // the bullet's size
	field(0x0020, 4) // its colour
	field(0x0800, 2) // alignment
	field(0x1000, 2) // line spacing
	field(0x2000, 2) // space before
	field(0x4000, 2) // space after
	field(0x0100, 2) // left margin
	field(0x0400, 2) // indent
	field(0x8000, 2) // the size of a tab
	if tabs := field(0x100000, 2); tabs != nil {
		take(4 * int(le.Uint16(tabs)))
	}
	field(0x10000, 2)  // the alignment of fonts
	field(0xE0000, 2)  // wrapping
	field(0x200000, 2) // the direction of the text
	return b, ok
}

// readCF reads the properties of characters that a mask says follow
// ([MS-PPT] 2.9.14, TextCFException): the font, and the font of symbols,
// by number, -1 where it names none.
func readCF(b []byte, mask uint32) (rest []byte, font, sym, ext int, ok bool) {
	font, sym = -1, -1
	if mask&0xFFFF != 0 {
		if len(b) < 2 {
			return nil, -1, -1, 0, false
		}
		ext = int(le.Uint16(b)>>10) & 0xF // among the style flags
		b = b[2:]
	}
	for _, f := range []struct {
		bit  uint32
		size int
		to   *int
	}{{0x10000, 2, &font}, {0x200000, 2, nil}, {0x400000, 2, nil}, {0x800000, 2, &sym}, {0x20000, 2, nil}, {0x40000, 4, nil}, {0x80000, 2, nil}} {
		if mask&f.bit == 0 {
			continue
		}
		if len(b) < f.size {
			return nil, -1, -1, 0, false
		}
		if f.to != nil {
			*f.to = int(le.Uint16(b))
		}
		b = b[f.size:]
	}
	return b, font, sym, ext, true
}

// masterStyles reads the styles a master gives the levels of one kind of
// text (TxMasterStyleAtom, [MS-PPT] 2.9.35): for each level, the
// properties of its paragraphs and of its characters. A level takes from
// the level before it what it does not say itself, or from the same
// level of base, the styles of the kind of text this kind follows: the
// kinds of body text follow the body, the centred title the title. kind
// is the kind of text, which the record's instance gives.
func masterStyles(b []byte, kind int, base []paraStyle) []paraStyle {
	if len(b) < 2 {
		return base
	}
	n := int(le.Uint16(b))
	b = b[2:]
	var out []paraStyle
	for i := 0; i < n && i < 9; i++ {
		ps := paraStyle{font: -1, chFont: -1, chSym: -1}
		switch {
		case i < len(base):
			ps = base[i]
		case i > 0:
			ps = out[i-1]
		}
		if kind >= 5 {
			if len(b) < 2 {
				break
			}
			b = b[2:] // the level's number
		}
		if len(b) < 4 {
			break
		}
		mask := le.Uint32(b)
		var ok bool
		if b, ok = readPF(b[4:], mask, &ps); !ok || len(b) < 4 {
			break
		}
		var font, sym int
		if b, font, sym, _, ok = readCF(b[4:], le.Uint32(b)); !ok {
			break
		}
		if font >= 0 {
			ps.chFont = font
		}
		if sym >= 0 {
			ps.chSym = sym
		}
		out = append(out, ps)
	}
	return out
}

// paraStyles reads the paragraph runs of a StyleTextPropAtom ([MS-PPT]
// 2.9.44): for each, how many characters, their level, and the
// properties its mask says follow. total is the characters of the text
// and its end; the runs are read until they cover it. A run that does
// not fit ends the reading: what was read is used.
func paraStyles(b []byte, total int) (runs []paraStyle, rest []byte) {
	var out []paraStyle
	for covered := 0; covered < total && len(b) >= 10 && len(out) < 1<<16; {
		ps := paraStyle{count: int(le.Uint32(b)), level: int(le.Uint16(b[4:])), font: -1}
		mask := le.Uint32(b[6:])
		if ps.count <= 0 || ps.level > 8 {
			break
		}
		var ok bool
		if b, ok = readPF(b[10:], mask, &ps); !ok {
			return out, nil
		}
		covered += ps.count
		out = append(out, ps)
	}
	return out, b
}

// shapes reads the shapes of a drawing into items: each shape's place,
// its placeholder, its text, the picture it shows.
func (s *slideReader) shapes(drawing []byte) []item {
	var items []item
	// A group of shapes has a space of coordinates of its own: its
	// shapes say where they are in it, and the group where it is on the
	// slide.
	type group struct {
		inner, outer rect
		ok           bool
		table        bool
		first        int // the index of its first item
	}
	var groups []group
	var walk func(b []byte, depth int)
	walk = func(b []byte, depth int) {
		for {
			r, rest, ok := officeart.Next(b)
			if !ok {
				return
			}
			b = rest
			switch r.Type {
			case officeart.DgContainer:
				walk(r.Body, depth)
			case officeart.SpgrContainer:
				if depth >= officeart.MaxDepth {
					continue
				}
				groups = append(groups, group{first: len(items)})
				walk(r.Body, depth+1)
				g := groups[len(groups)-1]
				groups = groups[:len(groups)-1]
				if g.table && len(items) > g.first {
					// A table: the texts of its cells, by where they are.
					t := tableOf(items[g.first:])
					if t.rows != nil {
						items = append(items[:g.first], t)
					}
				}
			case officeart.SpContainer:
				if len(items) >= warn.MaxSlideShapes {
					s.lost.Add(warn.CapSlideShapes, 1)
					continue
				}
				it := item{placeholder: -1, textKind: -1}
				var props officeart.Props
				isGroup := false
				var own []auto // the automatic numbers of the shape's own text
				var child rect
				hasChild := false
				officeart.Walk(r.Body, func(c officeart.Rec, _ int) bool {
					switch c.Type {
					case 0xF009: // the group's own coordinates
						if len(c.Body) >= 16 && len(groups) > 0 {
							g := &groups[len(groups)-1]
							g.inner = rect{float64(int32(le.Uint32(c.Body))), float64(int32(le.Uint32(c.Body[4:]))),
								float64(int32(le.Uint32(c.Body[8:]))), float64(int32(le.Uint32(c.Body[12:])))}
							isGroup = true
						}
					case officeart.OPT, officeart.SecondaryOPT, officeart.TertiaryOPT:
						props.ReadProps(c.Inst, c.Body)
					case officeart.ClientAnchor:
						// Top, left, right and bottom, in units of 576 to
						// the inch.
						if len(c.Body) >= 8 {
							it.at = rect{float64(int16(le.Uint16(c.Body[2:]))) / 8, float64(int16(le.Uint16(c.Body))) / 8,
								float64(int16(le.Uint16(c.Body[4:]))) / 8, float64(int16(le.Uint16(c.Body[6:]))) / 8}
							it.placed = true
						}
					case officeart.ChildAnchor:
						if len(c.Body) >= 16 {
							child = rect{float64(int32(le.Uint32(c.Body))), float64(int32(le.Uint32(c.Body[4:]))),
								float64(int32(le.Uint32(c.Body[8:]))), float64(int32(le.Uint32(c.Body[12:])))}
							hasChild = true
						}
					case officeart.ClientData:
						officeart.Walk(c.Body, func(p officeart.Rec, _ int) bool {
							if p.Type == rtPlaceholderAtom && len(p.Body) >= 5 {
								it.placeholder = int(p.Body[4])
							}
							if p.Type == rtExObjRefAtom && len(p.Body) >= 4 {
								it.object = s.d.byRef[le.Uint32(p.Body)]
							}
							if p.Type == rtInteractiveInfo {
								s.action(p)
								return false
							}
							return true
						})
						binaryTags(c.Body, func(t officeart.Rec) {
							if t.Type == rtStyleTextProp9 && own == nil {
								own = autos(t.Body)
							}
						})
						return false
					case officeart.ClientTextbox:
						it.raw = c.Body
						s.text(c.Body, &it)
						return false
					}
					return true
				})
				if isGroup {
					// The group's own shape: where the group is, and
					// whether it is a table.
					g := &groups[len(groups)-1]
					g.outer, g.ok = it.at, it.placed
					if hasChild && len(groups) > 1 {
						if up := groups[len(groups)-2]; up.ok {
							g.outer, g.ok = mapRect(child, up.inner, up.outer), true
						}
					}
					if v, ok := props.Value(0x039F); ok && v&1 != 0 {
						g.table = true
					}
					continue
				}
				if hasChild && len(groups) > 0 {
					if g := groups[len(groups)-1]; g.ok {
						it.at, it.placed = mapRect(child, g.inner, g.outer), true
					}
				}
				if it.autos == nil {
					it.autos = own
				}
				if v, ok := props.Value(officeart.PropBlip); ok {
					it.blip = int(v)
				}
				it.hidden = props.Hidden()
				if alt := props.String(officeart.PropDescription); alt != "" {
					it.alt, _ = clean.String([]byte(alt), clean.Prose)
				}
				if word := props.String(officeart.PropUnicodeText); word != "" && len(it.paras) == 0 {
					text, _ := clean.String([]byte(word), clean.Prose)
					if text != "" {
						it.paras = []para{{text: text, font: -1}}
					}
				}
				// A text that is only fields has nothing to show until a
				// slide fills them in.
				if len(it.paras) > 0 || it.blip > 0 || it.fields || s.d.graphs[it.object] || s.d.equations[it.object] {
					items = append(items, it)
				}
			default:
				if r.Container() && depth < officeart.MaxDepth {
					walk(r.Body, depth+1)
				}
			}
		}
	}
	walk(drawing, 0)
	return items
}

// mapRect puts a rectangle given in a group's coordinates on the slide.
func mapRect(r, inner, outer rect) rect {
	w, h := inner.x1-inner.x0, inner.y1-inner.y0
	if w <= 0 || h <= 0 {
		return outer
	}
	sx, sy := (outer.x1-outer.x0)/w, (outer.y1-outer.y0)/h
	return rect{outer.x0 + (r.x0-inner.x0)*sx, outer.y0 + (r.y0-inner.y0)*sy, outer.x0 + (r.x1-inner.x0)*sx, outer.y0 + (r.y1-inner.y0)*sy}
}

// tableOf makes a table of the cells of a group that is one: their
// texts, placed by their left edges and their tops.
func tableOf(cells []item) item {
	var xs, ys []float64
	var at rect
	n := 0
	for _, c := range cells {
		if len(c.paras) == 0 || !c.placed {
			continue
		}
		xs, ys = append(xs, c.at.x0), append(ys, c.at.y0)
		if n == 0 {
			at = c.at
		} else {
			at = rect{min(at.x0, c.at.x0), min(at.y0, c.at.y0), max(at.x1, c.at.x1), max(at.y1, c.at.y1)}
		}
		n++
	}
	if n == 0 {
		return item{}
	}
	uniq := func(v []float64) []float64 {
		sort.Float64s(v)
		out := v[:0]
		for i, x := range v {
			if i == 0 || x-out[len(out)-1] > 0.5 {
				out = append(out, x)
			}
		}
		return out
	}
	xs, ys = uniq(xs), uniq(ys)
	if len(xs)*len(ys) > 1<<16 {
		return item{}
	}
	find := func(v []float64, x float64) int {
		i := sort.Search(len(v), func(i int) bool { return v[i] > x+0.5 })
		return max(i-1, 0)
	}
	rows := make([][]string, len(ys))
	for i := range rows {
		rows[i] = make([]string, len(xs))
	}
	for _, c := range cells {
		if len(c.paras) == 0 || !c.placed {
			continue
		}
		var parts []string
		for _, p := range c.paras {
			parts = append(parts, p.text)
		}
		cell := &rows[find(ys, c.at.y0)][find(xs, c.at.x0)]
		if *cell != "" {
			*cell += "\n"
		}
		*cell += strings.Join(parts, "\n")
	}
	return item{at: at, placed: true, placeholder: -1, textKind: -1, rows: rows}
}

// drawingOf returns the records of the drawing of a slide or a notes
// page.
func drawingOf(container officeart.Rec) (drawing []byte) {
	b := container.Body
	for {
		r, rest, ok := officeart.Next(b)
		if !ok {
			return drawing
		}
		if r.Type == rtPPDrawing {
			drawing = r.Body
		}
		b = rest
	}
}

// slide reads the slide with the given index.
func (d *Deck) slide(ctx context.Context, index int) *unit.Result {
	ref := &d.slides[index]
	s := &slideReader{d: d, ctx: ctx, room: gofastcore.TextBudgetFrom(ctx), ref: ref}
	part := gofastcore.Part{Kind: gofastcore.PartSlide, Number: index + 1, Width: d.width, Height: d.height}
	container, ok := d.object(ref.persist)
	if !ok || container.Type != rtSlide {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnPartFailed, "the presentation lists the slide but does not contain it")
		part.Finish()
		return &unit.Result{Part: part}
	}
	// The slide's own records: which notes page is its, and whether it
	// is hidden.
	notesID, masterID := uint32(0), uint32(0)
	hiddenSlide, title := false, false // title: the slide's layout is that of a title slide
	follows := false                   // the slide shows the shapes of its master
	s.number, s.hf = d.first+index, d.slideHF
	var comments []gofastcore.Block
	officeart.Walk(container.Body, func(r officeart.Rec, depth int) bool {
		switch r.Type {
		case rtSlideAtom:
			if len(r.Body) >= 20 {
				masterID, notesID = le.Uint32(r.Body[12:]), le.Uint32(r.Body[16:])
				title = le.Uint32(r.Body) == 0
			}
			if len(r.Body) >= 22 {
				follows = le.Uint16(r.Body[20:])&1 != 0
			}
		case rtHeadersFooters:
			s.hf = readHeadFoot(r)
			return false
		case rtSlideShowInfo:
			if len(r.Body) >= 12 {
				hiddenSlide = le.Uint16(r.Body[10:])&0x0004 != 0
			}
		case rtPPDrawing:
			return false
		case rtComment10:
			if b, ok := s.comment(r); ok {
				comments = append(comments, b)
			}
			return false
		}
		return true
	})
	if hiddenSlide {
		part.Display = gofastcore.DisplayHidden
	}
	m := d.masters[masterID]
	if m == nil {
		m = d.masters[0] // the first master
	}
	s.styles = m.styles
	items := s.shapes(drawingOf(container))
	// The order of reading: the title, then from the top down, and from
	// the left across.
	isTitle := func(it *item) bool {
		return it.placeholder == phTitle || it.placeholder == phCenterTitle || it.placeholder == phVertTitle
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := &items[i], &items[j]
		if ta, tb := isTitle(a), isTitle(b); ta != tb {
			return ta
		}
		if a.at.y0 != b.at.y0 {
			return a.at.y0 < b.at.y0
		}
		return a.at.x0 < b.at.x0
	})
	for i := range items {
		it := &items[i]
		if s.cut || ctx.Err() != nil {
			break
		}
		s.emit(&part, it, isTitle(it))
	}
	// What the slide's master puts on it: the text of its own, at the
	// head or the foot of the slide, and the date, the footer and the
	// slide's number where the slide is said to show them and has no
	// shapes of its own for them.
	if follows {
		for from := m; from != nil; from = from.parent {
			for i := range from.static {
				it := &from.static[i]
				if from != m && m.has[sameAs(it)] {
					// A title master often repeats what its master has, in
					// the same place; a reader sees it once.
					continue
				}
				if it.fields && it.raw != nil {
					// Its fields say what this slide puts in them. A
					// text that comes to nothing is paid for all the
					// same, so that a master cannot make every slide
					// read it for nothing without end.
					again := item{at: it.at, placed: it.placed, placeholder: -1, textKind: -1, hidden: it.hidden, blip: it.blip, alt: it.alt}
					s.text(it.raw, &again)
					if it = &again; len(it.paras) == 0 && !s.take(gofastcore.BlockCost) {
						break
					}
				}
				s.static(&part, it)
			}
			if !from.follow {
				break
			}
		}
	}
	if !(title && d.omitTitle) {
		own := map[int]bool{}
		for i := range items {
			own[items[i].placeholder] = true
		}
		for i := range m.foot {
			f := &m.foot[i]
			shown := map[int]uint16{phMasterDate: 0x01, phMasterNumber: 0x08, phMasterFooter: 0x20}[f.placeholder]
			if s.hf.flags&shown == 0 || own[f.placeholder] || f.raw == nil {
				continue
			}
			// The master's text for it, with what the slide puts in its
			// fields.
			it := item{at: f.at, placed: f.placed, placeholder: f.placeholder, textKind: -1, hidden: f.hidden}
			s.text(f.raw, &it)
			if len(it.paras) == 0 && !s.take(gofastcore.BlockCost) {
				break // as above: what comes to nothing is paid for
			}
			s.emit(&part, &it, false)
		}
	}
	// The speaker's notes: the texts of the slide's notes page, a
	// paragraph at a time.
	if at, ok := d.notes[notesID]; ok && notesID != 0 && !s.cut {
		if page, ok := d.object(at); ok && page.Type == rtNotes {
			was := s.hf
			s.hf = d.notesHF
			for _, it := range s.shapes(drawingOf(page)) {
				// The header, the footer, the date and the number of the
				// printed page are not notes.
				kind := gofastcore.KindNote
				switch it.placeholder {
				case phMasterHeader:
					kind = gofastcore.KindHeader
				case phMasterFooter, phMasterDate, phMasterNumber:
					kind = gofastcore.KindFooter
				}
				for _, p := range it.paras {
					if !s.take(gofastcore.BlockCost + len(p.text)) {
						break
					}
					b := gofastcore.Block{Kind: kind, Text: p.text, Spans: p.spans}
					if kind == gofastcore.KindNote {
						b.Attrs = map[string]string{gofastcore.AttrNoteType: gofastcore.NoteSpeaker}
					}
					if it.hidden {
						b.Display = gofastcore.DisplayHidden
					}
					part.Blocks = append(part.Blocks, b)
				}
			}
			s.hf = was
		}
	}
	for _, c := range comments {
		if s.take(gofastcore.BlockCost + len(c.Text)) {
			part.Blocks = append(part.Blocks, c)
		}
	}
	if s.cut {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, warn.TextCut("slide"))
	}
	if s.budget {
		part.Warn(gofastcore.SeverityError, gofastcore.WarnLimitExceeded, "the decompressed size limit was reached; pictures from there on are named and not read, and charts are not read")
	}
	capped := s.lost.Say(&part)
	hasText, hasImage := false, s.sawImage
	for i := range part.Blocks {
		if part.Blocks[i].Kind == gofastcore.KindImage {
			hasImage = true
		} else {
			hasText = true
		}
	}
	if !hasText {
		if hasImage {
			part.Warn(gofastcore.SeverityWarning, gofastcore.WarnPartImageOnly, "")
		} else {
			part.Warn(gofastcore.SeverityInfo, gofastcore.WarnPartNoText, "")
		}
	}
	if s.removed > 0 {
		part.Warn(gofastcore.SeverityInfo, gofastcore.WarnTextControlCharactersRemoved, strconv.Itoa(s.removed))
	}
	if s.unmapped > 0 {
		part.Warn(gofastcore.SeverityWarning, gofastcore.WarnTextUnmappedGlyphs, warn.UnmappedEquation(s.unmapped))
	}
	part.Finish()
	return &unit.Result{Part: part, Tally: unit.Tally{Limited: s.cut || s.budget || capped, Programs: s.programs, Damaged: s.damaged}}
}

// masterOf returns what the slide's master says of a level of a kind of
// text: of the kind the text's header names, or of the placeholder's.
func (s *slideReader) masterOf(kind, placeholder, level int) (paraStyle, bool) {
	if kind < 0 {
		switch placeholder {
		case phBody, phVertBody:
			kind = textBody
		default:
			kind = textOther
		}
	}
	styles := s.styles[kind]
	if styles == nil {
		switch kind {
		case textCenterBody, textHalfBody, textQuarterBody:
			styles = s.styles[textBody]
		case textCenterTitle:
			styles = s.styles[textTitle]
		}
	}
	if len(styles) == 0 {
		return paraStyle{}, false
	}
	return styles[min(max(level, 0), len(styles)-1)], true
}

// take takes what a block costs from what the document may hold.
func (s *slideReader) take(n int) bool {
	if s.cut || !s.room.Take(n) {
		s.cut = true
		return false
	}
	return true
}

// emit adds the blocks of one shape to the slide's part.
func (s *slideReader) emit(part *gofastcore.Part, it *item, title bool) {
	place := func(b *gofastcore.Block) {
		if it.placed {
			b.Loc.BBox = box(it.at)
		}
		if it.hidden {
			b.Display = gofastcore.DisplayHidden
		}
	}
	if it.rows != nil {
		cols := len(it.rows[0])
		size := 0
		for _, row := range it.rows {
			for _, c := range row {
				size += len(c)
			}
		}
		if fit := warn.FitRows(s.room, len(it.rows), cols); fit < len(it.rows) || !s.room.Take(size) {
			s.cut = true
			return
		}
		b := gofastcore.Block{Kind: gofastcore.KindTable, Table: &gofastcore.Table{Rows: it.rows}}
		place(&b)
		part.Blocks = append(part.Blocks, b)
		return
	}
	// What the shape's text is: a title, a subtitle, the date, the footer
	// or the number of the slide, or the body.
	kind, level := gofastcore.KindParagraph, uint8(0)
	body := false
	switch {
	case title:
		kind, level = gofastcore.KindHeading, 1
	case it.placeholder == phSubTitle:
		kind, level = gofastcore.KindHeading, 2
	case it.placeholder == phMasterDate || it.placeholder == phMasterNumber || it.placeholder == phMasterFooter:
		kind = gofastcore.KindFooter
	case it.placeholder == phMasterHeader:
		kind = gofastcore.KindHeader
	case it.placeholder == phBody || it.placeholder == phVertBody ||
		it.placeholder < 0 && (it.textKind == textBody || it.textKind == textCenterBody || it.textKind == textHalfBody || it.textKind == textQuarterBody):
		body = true
	}
	var count, scheme [9]int // the numbers of the numbered paragraphs, by level
	for i, p := range it.paras {
		if !s.take(gofastcore.BlockCost + len(p.text)) {
			return
		}
		b := gofastcore.Block{Kind: kind, Level: level, Text: p.text, Spans: p.spans}
		if kind == gofastcore.KindHeading && i > 0 {
			// A title of several paragraphs: one heading, on several lines.
			last := &part.Blocks[len(part.Blocks)-1]
			at := len(last.Text) + 1
			last.Text += "\n" + p.text
			for _, sp := range p.spans {
				sp.Start, sp.End = sp.Start+at, sp.End+at
				last.Spans = append(last.Spans, sp)
			}
			continue
		}
		if kind == gofastcore.KindParagraph {
			// What the paragraph does not say of its bullet, the master
			// says for its kind of text and its level; the body's
			// paragraphs have bullets where neither says.
			bullet, mark, font := p.bullet, p.mark, p.font
			if m, ok := s.masterOf(it.textKind, it.placeholder, p.level); ok {
				if bullet == 0 {
					bullet = m.bullet
				}
				if mark == "" {
					mark = m.mark
				}
				if font < 0 {
					font = m.font
				}
			}
			level := min(max(p.level, 0), 8)
			for l := level + 1; l < len(count); l++ {
				count[l] = 0
			}
			if a := p.auto(it); a.on && bullet >= 0 {
				// A numbered paragraph: the next number of its level.
				if count[level] == 0 || scheme[level] != a.scheme {
					count[level], scheme[level] = a.start, a.scheme
				} else {
					count[level]++
				}
				b.Kind, b.Level, b.Ordered = gofastcore.KindListItem, uint8(level)+1, true
				b.Marker = number(a.scheme, count[level])
			} else if count[level] = 0; bullet > 0 || body && bullet == 0 {
				b.Kind, b.Level = gofastcore.KindListItem, uint8(min(p.level, 8))+1
				if mark == "" {
					mark = "•"
				}
				name := ""
				if font >= 0 && font < len(s.d.fonts) {
					name = s.d.fonts[font]
				}
				b.Marker = label.Bullet(mark, name)
			}
		}
		place(&b)
		if title && part.Name == "" {
			part.Name = strings.ReplaceAll(p.text, "\n", " ")
		}
		part.Blocks = append(part.Blocks, b)
	}
	if title && len(part.Blocks) > 0 && part.Name != "" {
		// The part's name is the whole title.
		for i := range part.Blocks {
			if part.Blocks[i].Kind == gofastcore.KindHeading && part.Blocks[i].Level == 1 {
				part.Name = clean.Label(strings.Join(strings.Fields(part.Blocks[i].Text), " "))
				break
			}
		}
	}
	if s.d.graphs[it.object] && s.chart(part, it, place) {
		return // the picture of a chart that is read is the chart drawn
	}
	if s.d.equations[it.object] && s.equation(part, it, place) {
		return // and that of an equation the equation drawn
	}
	if it.blip > 0 {
		s.picture(part, it, place)
	}
}

// object returns the compound file of the embedded object a shape
// shows. The file is charged to what the document may decode each time
// it is read, compressed or not: a presentation can show one object in
// as many shapes as it likes.
func (s *slideReader) object(it *item, what string) ([]byte, bool) {
	decoded := gofastcore.DecodeBudgetFrom(s.ctx)
	left := int64(1) << 40
	if decoded != nil {
		left = decoded.Left()
	}
	data, compressed, ok := s.d.storage(it.object, left)
	if ok && !decoded.Take(int64(len(data))) {
		s.budget = true
		return nil, false
	}
	if !ok {
		// A compressed file that does not expand within what is left is
		// over the limit; any other that cannot be read is damaged.
		if compressed && decoded != nil && decoded.Left() <= left {
			s.budget = true
		} else {
			s.damaged = append(s.damaged, what)
		}
		return nil, false
	}
	return data, true
}

// equation adds the equation a shape shows, in its linear form. It
// reports whether the equation was read; one that was not is a picture
// on the slide and a file among the embedded ones, as any other object.
func (s *slideReader) equation(part *gofastcore.Part, it *item, place func(*gofastcore.Block)) bool {
	if s.cut {
		return true
	}
	data, ok := s.object(it, "an equation")
	if !ok {
		return false
	}
	text, unmapped, ok := s.d.cfg.Equation(data)
	if !ok {
		return false
	}
	text, _ = clean.String([]byte(text), clean.Prose)
	if text == "" {
		return true // an equation with nothing in it
	}
	if !s.take(gofastcore.BlockCost + len(text)) {
		return true
	}
	s.unmapped += unmapped
	b := gofastcore.Block{Kind: gofastcore.KindFormula, Text: text, Attrs: map[string]string{gofastcore.AttrNotation: gofastcore.NotationUnicodeMath}}
	place(&b)
	part.Blocks = append(part.Blocks, b)
	return true
}

// chart adds the blocks of the Microsoft Graph chart a shape shows. It
// reports whether the chart was read.
func (s *slideReader) chart(part *gofastcore.Part, it *item, place func(*gofastcore.Block)) bool {
	if s.cut {
		return true
	}
	data, ok := s.object(it, "a chart")
	if !ok {
		return false
	}
	blocks, cut, ok := s.d.cfg.Chart(s.ctx, data)
	if !ok {
		s.damaged = append(s.damaged, "a chart")
		return false
	}
	for i := range blocks {
		place(&blocks[i])
	}
	part.Blocks = append(part.Blocks, blocks...)
	s.cut = s.cut || cut
	return true
}

// headFoot is what a slide or a notes page is said to show at its head
// and its foot (HeadersFootersAtom, [MS-PPT] 2.4.15.2).
type headFoot struct {
	flags                uint16
	date, header, footer string
}

// readHeadFoot reads a HeadersFooters container.
func readHeadFoot(r officeart.Rec) headFoot {
	var hf headFoot
	officeart.Walk(r.Body, func(c officeart.Rec, _ int) bool {
		switch c.Type {
		case rtHeadersFootersAtom:
			if len(c.Body) >= 4 {
				hf.flags = le.Uint16(c.Body[2:])
			}
		case rtCString:
			v, _ := clean.String([]byte(wide(c.Body)), clean.Prose)
			switch c.Inst {
			case 0:
				hf.date = v
			case 1:
				hf.header = v
			case 2:
				hf.footer = v
			}
		}
		return true
	})
	return hf
}

// static adds the text of a master's own to a slide that follows it, at
// the head of the slide or at its foot, and the picture it shows.
func (s *slideReader) static(part *gofastcore.Part, it *item) {
	kind := gofastcore.KindFooter
	if it.placed && s.d.height > 0 && (it.at.y0+it.at.y1)/2 < s.d.height/2 {
		kind = gofastcore.KindHeader
	}
	for _, p := range it.paras {
		if !s.take(gofastcore.BlockCost + len(p.text)) {
			return
		}
		b := gofastcore.Block{Kind: kind, Text: p.text, Spans: p.spans}
		if it.placed {
			b.Loc.BBox = box(it.at)
		}
		if it.hidden {
			b.Display = gofastcore.DisplayHidden
		}
		part.Blocks = append(part.Blocks, b)
	}
	if it.blip > 0 {
		s.picture(part, it, func(b *gofastcore.Block) {
			if it.placed {
				b.Loc.BBox = box(it.at)
			}
			if it.hidden {
				b.Display = gofastcore.DisplayHidden
			}
		})
	}
}

// same is what makes two things of a master the same to a reader: the
// place, the picture, the kind of placeholder and the text.
type same struct {
	at          rect
	blip        int
	placeholder int
	text        string
}

// sameAs gives what an item is compared by.
func sameAs(it *item) same {
	var sb strings.Builder
	for i := range it.paras {
		sb.WriteString(it.paras[i].text)
		sb.WriteByte('\r')
	}
	return same{it.at, it.blip, it.placeholder, sb.String()}
}

// auto returns the automatic numbering of a paragraph of a shape.
func (p *para) auto(it *item) auto {
	// A paragraph whose characters name no entry has the first.
	if ext := max(p.ext, 0); ext < len(it.autos) {
		return it.autos[ext]
	}
	return auto{}
}

// number writes a number the way a scheme of automatic numbering says
// ([MS-PPT] 2.13.36).
func number(scheme, n int) string {
	if n < 1 {
		n = 1
	}
	v := strconv.Itoa(n)
	switch scheme {
	case 0, 8, 9:
		v = strings.ToLower(label.Letters(n))
	case 1, 10, 11:
		v = label.Letters(n)
	case 4, 5, 6:
		v = strings.ToLower(label.Roman(n))
	case 7, 14, 15:
		v = label.Roman(n)
	}
	switch scheme {
	case 2, 5, 9, 11, 15:
		return v + ")"
	case 4, 8, 10, 12, 14:
		return "(" + v + ")"
	case 13:
		return v
	}
	return v + "."
}

func round(v float64) float64 { return math.Round(v*100) / 100 }

// box gives the place of a shape as a rectangle of the part: a shape
// that is flipped says its edges the other way round.
func box(at rect) gofastcore.Rect {
	return gofastcore.Rect{X0: round(min(at.x0, at.x1)), Y0: round(min(at.y0, at.y1)), X1: round(max(at.x0, at.x1)), Y1: round(max(at.y0, at.y1))}
}

// picture adds the picture a shape shows.
func (s *slideReader) picture(part *gofastcore.Part, it *item, place func(*gofastcore.Block)) {
	mode := gofastcore.ImagesMeta
	if s.d.opts != nil {
		mode = s.d.opts.Images
	}
	if mode == gofastcore.ImagesNone {
		s.sawImage = true
		return
	}
	if !s.take(gofastcore.BlockCost + len(it.alt)) {
		return
	}
	var blip officeart.Rec
	if it.blip <= len(s.d.store) {
		e := s.d.store[it.blip-1]
		blip = e.Blip
		// A picture the store does not hold is in the Pictures stream, at
		// the place its entry gives.
		if blip.Type == 0 && int64(e.Offset)+8 <= int64(len(s.d.cfg.Pictures)) {
			if rec, _, ok := officeart.Next(s.d.cfg.Pictures[e.Offset:]); ok {
				blip = rec
			}
		}
	}
	img, err := media.Image(it.blip, blip, mode, gofastcore.DecodeBudgetFrom(s.ctx))
	if err != nil {
		s.budget = true
	}
	b := gofastcore.Block{Kind: gofastcore.KindImage, Text: it.alt, Image: img}
	place(&b)
	part.Blocks = append(part.Blocks, b)
}

// comment reads a comment on a slide: its author, its text and when it
// was written ([MS-PPT] 2.5.23).
func (s *slideReader) comment(r officeart.Rec) (gofastcore.Block, bool) {
	var author, text, date string
	officeart.Walk(r.Body, func(c officeart.Rec, _ int) bool {
		switch c.Type {
		case rtCString:
			v := wide(c.Body)
			switch c.Inst {
			case 0:
				author = v
			case 1:
				text = v
			}
		case rtComment10Atom:
			// A number, then the time: year, month, day of the week, day,
			// hour, minute, second and thousandths.
			if len(c.Body) >= 20 {
				t := c.Body[4:]
				y, mo, da := int(le.Uint16(t)), int(le.Uint16(t[2:])), int(le.Uint16(t[6:]))
				h, mi, se := int(le.Uint16(t[8:])), int(le.Uint16(t[10:])), int(le.Uint16(t[12:]))
				if y >= 1601 && y < 10000 && mo >= 1 && mo <= 12 && da >= 1 && da <= 31 && h < 24 && mi < 60 && se < 60 {
					date = pad(y, 4) + "-" + pad(mo, 2) + "-" + pad(da, 2) + "T" + pad(h, 2) + ":" + pad(mi, 2) + ":" + pad(se, 2)
				}
			}
		}
		return true
	})
	text, removed := clean.String([]byte(text), clean.Prose)
	s.removed += removed
	if text == "" {
		return gofastcore.Block{}, false
	}
	b := gofastcore.Block{Kind: gofastcore.KindNote, Text: text, Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteComment}}
	if a, _ := clean.String([]byte(author), clean.Prose); a != "" {
		b.Attrs[gofastcore.AttrAuthor] = s.lost.Name(a)
	}
	if date != "" {
		b.Attrs[gofastcore.AttrDate] = date
	}
	return b, true
}

func pad(v, width int) string {
	s := strconv.Itoa(v)
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// wide reads UTF-16 text.
func wide(b []byte) string {
	var out []byte
	var high rune
	for i := 0; i+1 < len(b); i += 2 {
		r := rune(le.Uint16(b[i:]))
		switch {
		case r >= 0xD800 && r < 0xDC00:
			high = r
			continue
		case r >= 0xDC00 && r < 0xE000:
			if high != 0 {
				out = utf8.AppendRune(out, 0x10000+(high-0xD800)<<10+(r-0xDC00))
			}
		default:
			out = utf8.AppendRune(out, r)
		}
		high = 0
	}
	return string(out)
}

// macros reads the modules of the presentation's VBA project, which is a
// compound file kept in an object of its own, into the stored part.
func (d *Deck) macros(ctx context.Context, part *stored.Part) {
	decoded := gofastcore.DecodeBudgetFrom(ctx)
	left := int64(1) << 40
	if decoded != nil {
		left = decoded.Left()
	}
	data, compressed, ok := d.storage(d.vba, left)
	if !ok {
		part.Damaged = append(part.Damaged, "the VBA project")
		return
	}
	if compressed && !decoded.Take(int64(len(data))) {
		return
	}
	f, err := cfb.Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		part.Damaged = append(part.Damaged, "the VBA project")
		return
	}
	stored.Macros(ctx, f, stored.Projects(f), part)
}
