// Package mtef reads the equations of the Equation Editor and of
// MathType, which the binary Office formats keep as embedded objects: a
// stream "Equation Native" that holds the equation in MTEF, a sequence of
// records for lines, characters, templates (a fraction, a root, a sum),
// piles and matrices.
//
// The equation is written in UnicodeMath, the linear form Word shows when
// an equation is switched to "linear" (Unicode Technical Note 28), as
// go-fast-ooxml writes the equations of the newer formats: a fraction is
// a/b, an exponent x^2, a square root √x, a sum ∑_(i=1)^n▒x_i. Every
// character the equation holds is kept, in order; what is added are the
// operators and brackets that say how the characters are laid out.
//
// Versions 2 and 3 of MTEF (the Equation Editor) and 5 (MathType 4 and
// later) are read. The rules of the linear form (when a part is put in
// parentheses, where a space ends a structure) are go-fast-ooxml's; see
// NOTICE.
package mtef

import (
	"encoding/binary"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AOShei/go-fast-msbin/internal/symbol"
)

// Stream is the name of the stream of an embedded object that holds an
// equation.
const Stream = "Equation Native"

// Bounds: the bytes of an equation that are read, and how deep its
// records may nest.
const (
	MaxBytes = 1 << 20
	maxDepth = 64
)

// The kinds of record.
const (
	recEnd = iota
	recLine
	recChar
	recTmpl
	recPile
	recMatrix
	recEmbell
	recRuler
	recFont // in version 5, the style of a font
	recSize
	recFull
	recSub
	recSub2
	recSym
	recSubSym
	recColor
	recColorDef
	recFontDef
	recEqnPrefs
	recEncodingDef
	recFuture = 100 // from here on a record says how long it is
)

// Options of a record: the high half of its first byte in versions 2 and
// 3, the byte after it in version 5.
const (
	optNudge      = 0x08
	optLineNull   = 0x01
	optRuler      = 0x02
	optLineSpace  = 0x04
	optCharEmbell = 0x02 // versions 2 and 3
	opt5Embell    = 0x01
	opt5Char8     = 0x04
	opt5Char16    = 0x10
	opt5NoCode    = 0x20
)

// Typefaces of a character.
const (
	faceText     = 1
	faceFunction = 2
	faceLCGreek  = 4
	faceUCGreek  = 5
	faceSymbol   = 6
	faceExtra    = 11
)

type node struct {
	kind  byte
	null  bool // a line that holds nothing
	r     rune
	face  int
	marks string // the embellishments of a character, as combining marks and primes
	sel   int    // a template's selector and variation
	vari  int
	cols  int // of a matrix
	kids  []node
}

type reader struct {
	b       []byte
	at      int
	version int
	bad     bool
}

// Native returns the MTEF of a stream "Equation Native": what follows
// the header the stream begins with, which says how long it is.
func Native(stream []byte) ([]byte, bool) {
	if len(stream) < 4 {
		return nil, false
	}
	head := int(binary.LittleEndian.Uint16(stream))
	if head < 28 || head >= len(stream) {
		return nil, false
	}
	return stream[head:], true
}

// Linear returns the equation in UnicodeMath, and the number of its
// characters that Unicode has nothing for (each is U+FFFD). ok is false
// for what is not MTEF of a version that is read, and for records that
// break off or do not follow one another as they should.
func Linear(b []byte) (text string, unmapped int, ok bool) {
	if len(b) > MaxBytes {
		b = b[:MaxBytes]
	}
	if len(b) < 5 {
		return "", 0, false
	}
	r := &reader{b: b, version: int(b[0])}
	switch r.version {
	case 2, 3:
		r.at = 5
	case 5:
		r.at = 5
		r.cstring() // the application's key
		r.skip(1)   // options of the equation
	default:
		return "", 0, false
	}
	top := r.list(0, false)
	var lines []string
	for i := range top {
		if s := r.render(&top[i]); s != "" {
			lines = append(lines, s)
		}
	}
	if r.bad {
		// Records that do not follow one another as they should: what
		// was made of them is not to be trusted.
		return "", 0, false
	}
	text = strings.Join(lines, "\n")
	return text, strings.Count(text, string(utf8.RuneError)), true
}

func (r *reader) byte() int {
	if r.at >= len(r.b) {
		r.bad = true
		return 0
	}
	v := r.b[r.at]
	r.at++
	return int(v)
}

func (r *reader) u16() int {
	if r.at+2 > len(r.b) {
		r.bad, r.at = true, len(r.b)
		return 0
	}
	v := binary.LittleEndian.Uint16(r.b[r.at:])
	r.at += 2
	return int(v)
}

func (r *reader) skip(n int) {
	if n < 0 || r.at+n > len(r.b) {
		r.bad, r.at = true, len(r.b)
		return
	}
	r.at += n
}

// cstring skips a string that ends with a zero byte.
func (r *reader) cstring() {
	for r.at < len(r.b) {
		r.at++
		if r.b[r.at-1] == 0 {
			return
		}
	}
	r.bad = true
}

// uint reads an unsigned number of version 5: one byte, or 255 and two.
func (r *reader) uint() int {
	if v := r.byte(); v != 255 {
		return v
	}
	return r.u16()
}

// nudge skips how far a record is moved from its place.
func (r *reader) nudge() {
	dx, dy := r.byte(), r.byte()
	if dx == 128 && dy == 128 {
		r.skip(4)
	}
}

// ruler skips the tab stops of a line or a pile.
func (r *reader) ruler() {
	n := r.byte()
	r.skip(3 * n)
}

// nibbles skips a list of measures of the preferences of version 5: a
// count, then each as a unit and digits in half bytes, ended by 15; the
// list is filled up to a whole byte.
func (r *reader) nibbles() {
	count := r.byte()
	half := 0 // half bytes read
	next := func() int {
		if r.at >= len(r.b) {
			r.bad = true
			return 15
		}
		v := int(r.b[r.at])
		if half%2 == 0 {
			v >>= 4
		} else {
			v &= 15
			r.at++
		}
		half++
		return v
	}
	for ; count > 0 && !r.bad; count-- {
		next() // the unit
		for next() != 15 && !r.bad {
		}
	}
	if half%2 == 1 {
		r.at++
	}
}

// list reads records up to the one that ends them. The embellishments
// are kept where embell says the list is a character's own.
func (r *reader) list(depth int, embell bool) []node {
	var out []node
	if depth > maxDepth {
		r.bad = true
		return nil
	}
	for !r.bad && r.at < len(r.b) {
		tag := r.byte()
		kind, opts := tag, 0
		if r.version < 5 {
			kind, opts = tag&15, tag>>4
		}
		if kind == recEnd {
			return out
		}
		if r.version == 5 && kind >= recLine && kind <= recEmbell {
			opts = r.byte()
		}
		switch kind {
		case recLine:
			n := node{kind: recLine}
			if opts&optNudge != 0 {
				r.nudge()
			}
			if opts&optLineSpace != 0 {
				r.skip(2)
			}
			if opts&optRuler != 0 {
				r.byte() // the ruler's own first byte
				r.ruler()
			}
			if opts&optLineNull != 0 {
				n.null = true
			} else {
				n.kids = r.list(depth+1, false)
			}
			out = append(out, n)
		case recChar:
			out = append(out, r.char(opts, depth))
		case recTmpl:
			n := node{kind: recTmpl}
			if opts&optNudge != 0 {
				r.nudge()
			}
			n.sel = r.byte()
			n.vari = r.byte()
			if r.version == 5 && n.vari&0x80 != 0 {
				n.vari = n.vari&0x7F | r.byte()<<7
			}
			r.byte() // options of the template
			n.kids = r.list(depth+1, false)
			out = append(out, n)
		case recPile:
			n := node{kind: recPile}
			if opts&optNudge != 0 {
				r.nudge()
			}
			r.skip(2) // how its lines are aligned
			if opts&optRuler != 0 {
				r.byte()
				r.ruler()
			}
			n.kids = r.list(depth+1, false)
			out = append(out, n)
		case recMatrix:
			n := node{kind: recMatrix}
			if opts&optNudge != 0 {
				r.nudge()
			}
			r.skip(3) // alignment
			rows, cols := r.byte(), r.byte()
			n.cols = cols
			// The lines between the rows and between the columns, two
			// bits each.
			r.skip((2*(rows+1) + 7) / 8)
			r.skip((2*(cols+1) + 7) / 8)
			n.kids = r.list(depth+1, false)
			out = append(out, n)
		case recEmbell:
			if opts&optNudge != 0 {
				r.nudge()
			}
			// A record has one embellishment; in version 3 it has them one
			// after another, ended by a zero (seen in files; the published
			// description of the format gives one).
			for !r.bad {
				e := r.byte()
				if e != 0 && embell {
					out = append(out, node{kind: recEmbell, sel: e})
				}
				if e == 0 || r.version != 3 {
					break
				}
			}
		case recRuler:
			r.ruler()
		case recFont:
			if r.version == 5 {
				r.uint()
				r.skip(1)
			} else {
				r.skip(2)
				r.cstring()
			}
		case recSize:
			switch r.byte() {
			case 101:
				r.skip(2)
			case 100:
				r.skip(3)
			default:
				r.skip(1)
			}
		case recFull, recSub, recSub2, recSym, recSubSym:
		default:
			if r.version < 5 {
				r.bad = true
				return out
			}
			switch {
			case kind == recColor:
				r.uint()
			case kind == recColorDef:
				o := r.byte()
				if o&1 != 0 {
					r.skip(8) // four parts
				} else {
					r.skip(6)
				}
				if o&4 != 0 {
					r.cstring()
				}
			case kind == recFontDef:
				r.uint()
				r.cstring()
			case kind == recEqnPrefs:
				r.skip(1)
				r.nibbles() // sizes
				r.nibbles() // spaces
				for n := r.byte(); n > 0 && !r.bad; n-- {
					if r.byte() != 0 {
						r.skip(1)
					}
				}
			case kind == recEncodingDef:
				r.cstring()
			case kind >= recFuture:
				r.skip(r.uint())
			default:
				r.bad = true
				return out
			}
		}
	}
	// The records break off before the one that ends them.
	r.bad = true
	return out
}

// char reads a character and what embellishes it.
func (r *reader) char(opts, depth int) node {
	n := node{kind: recChar}
	if opts&optNudge != 0 {
		r.nudge()
	}
	embell := false
	code, font, coded := 0, -1, true
	switch r.version {
	case 5:
		n.face = r.uint() - 128
		if opts&opt5NoCode != 0 {
			coded = false
		} else {
			code = r.u16()
		}
		if opts&opt5Char8 != 0 {
			font = r.byte()
		}
		if opts&opt5Char16 != 0 {
			font = r.u16()
		}
		embell = opts&opt5Embell != 0
	case 3:
		n.face = r.byte() - 128
		code = r.u16()
		embell = opts&optCharEmbell != 0
	default:
		n.face = r.byte() - 128
		font, coded = r.byte(), false
		embell = opts&optCharEmbell != 0
	}
	switch {
	case coded:
		n.r = r.coded(code)
	default:
		n.r = r.placed(font, n.face)
	}
	if embell {
		var sb strings.Builder
		for _, e := range r.list(depth+1, true) {
			if e.kind == recEmbell {
				sb.WriteString(marks[e.sel])
			}
		}
		n.marks = sb.String()
	}
	return n
}

// coded gives the character of a code of MathType's own, which is
// Unicode but for the spaces and the symbols it keeps in the private
// use area.
func (r *reader) coded(code int) rune {
	c := rune(code)
	switch {
	case c == 0xEB00 || c == 0xEB01 || c < 0x20:
		return 0 // a mark for alignment, a space of no width
	case c >= 0xEB02 && c <= 0xEB0F:
		return ' '
	}
	if v, ok := private[c]; ok {
		return v
	}
	if c >= 0xE000 && c <= 0xF8FF || c >= 0xD800 && c <= 0xDFFF {
		return utf8.RuneError
	}
	return c
}

// placed gives the character at a place of a font, as version 2 has
// every character: of the Symbol font for the Greek and symbol
// typefaces, of the Equation Editor's own font of extras, or else of
// the Western code page.
func (r *reader) placed(at, face int) rune {
	if at < 0x20 {
		return 0
	}
	switch face {
	case faceLCGreek, faceUCGreek, faceSymbol:
		if v, ok := symbol.Symbol.Rune(byte(at)); ok {
			return v
		}
		return utf8.RuneError
	case faceExtra:
		if v, ok := extra[byte(at)]; ok {
			return v
		}
		return utf8.RuneError
	}
	if at < 0x80 || at >= 0xA0 && at <= 0xFF {
		return rune(at)
	}
	if v, ok := western[byte(at)]; ok {
		return v
	}
	return utf8.RuneError
}

// marks are the embellishments of a character, by their numbers.
var marks = map[int]string{
	2: "\u0307", 3: "\u0308", 4: "\u20DB", 5: "′", 6: "″", 7: "‵", 8: "\u0303", 9: "\u0302", 10: "\u0338",
	11: "\u20D7", 12: "\u20D6", 13: "\u20E1", 14: "\u20D1", 15: "\u20D0", 16: "\u0336", 17: "\u0305", 18: "‴",
	19: "\u0311", 20: "\u0306", 24: "\u20DC", 25: "\u0323", 26: "\u0324", 29: "\u0332", 30: "\u0330",
	33: "\u20EF", 34: "\u20EE", 35: "\u034D",
}

// private are the symbols MathType keeps in the private use area that
// Unicode has characters for.
var private = map[rune]rune{
	0xE900: '⋯', 0xE901: '⋮', 0xE902: '⋰', 0xE903: '⋱',
	0xE932: '∶', 0xEA06: '≜', 0xEA07: '≝',
}

// extra are the characters of the Equation Editor's font of extras
// ("MT Extra") that equations use.
var extra = map[byte]rune{
	'K': '…', 'L': '⋯', 'M': '⋮', 'N': '⋰', 'O': '⋱', 'l': 'ℓ', 'h': 'ℏ', 'o': '∘', 'm': '∓',
	'&': '⌣', '(': '⌢', '$': '⇀', '#': '↼', '!': '←', '"': '→', '%': '↔',
	'a': '∡', 'b': '∢', 'c': '∠', 'f': '≜', 'p': '⊲', 'q': '⊳', 'd': '∥', 'I': '∎', 'P': '△', 'w': '□',
}

// western are the characters of the Western code page that are not
// Latin-1's.
var western = map[byte]rune{
	0x80: '€', 0x82: '‚', 0x83: 'ƒ', 0x84: '„', 0x85: '…', 0x86: '†', 0x87: '‡', 0x88: 'ˆ', 0x89: '‰',
	0x8A: 'Š', 0x8B: '‹', 0x8C: 'Œ', 0x8E: 'Ž', 0x91: '‘', 0x92: '’', 0x93: '“', 0x94: '”', 0x95: '•',
	0x96: '–', 0x97: '—', 0x98: '˜', 0x99: '™', 0x9A: 'š', 0x9B: '›', 0x9C: 'œ', 0x9E: 'ž', 0x9F: 'Ÿ',
}

// render writes a record in the linear form.
func (r *reader) render(n *node) string {
	switch n.kind {
	case recLine:
		return r.line(n.kids)
	case recPile:
		rows := make([]string, 0, len(n.kids))
		for i := range n.kids {
			rows = append(rows, r.render(&n.kids[i]))
		}
		if len(rows) == 1 {
			return rows[0]
		}
		return "█(" + strings.Join(rows, "@") + ")"
	case recMatrix:
		var sb strings.Builder
		sb.WriteString("■(")
		for i := range n.kids {
			if i > 0 {
				if n.cols > 0 && i%n.cols == 0 {
					sb.WriteByte('@')
				} else {
					sb.WriteByte('&')
				}
			}
			sb.WriteString(r.render(&n.kids[i]))
		}
		sb.WriteByte(')')
		return sb.String()
	case recTmpl:
		s, _ := r.template(n)
		return s
	case recChar:
		return r.line([]node{*n})
	}
	return ""
}

// line writes the records of a line. Text that is not math is quoted;
// the name of a function is followed by the sign that applies it; and
// where a structure is followed by a letter, a digit or a bracket, a
// space ends it: x^k a^n, not x^ka^n, which is x to the power ka.
func (r *reader) line(kids []node) string {
	var sb strings.Builder
	structure := false
	for i := 0; i < len(kids); {
		n := &kids[i]
		piece, isStructure := "", false
		switch {
		case n.kind == recChar && n.face == faceText:
			var run strings.Builder
			for ; i < len(kids) && kids[i].kind == recChar && kids[i].face == faceText; i++ {
				if kids[i].r != 0 {
					run.WriteRune(kids[i].r)
				}
				run.WriteString(kids[i].marks)
			}
			if run.Len() > 0 {
				piece = `"` + run.String() + `"`
			}
		case n.kind == recChar && n.face == faceFunction:
			var run strings.Builder
			letters := true
			for ; i < len(kids) && kids[i].kind == recChar && kids[i].face == faceFunction; i++ {
				if kids[i].r != 0 {
					run.WriteRune(kids[i].r)
					letters = letters && unicode.IsLetter(kids[i].r)
				}
				run.WriteString(kids[i].marks)
			}
			piece = run.String()
			// The function is applied to what follows, unless that is a
			// script of its own.
			if letters && piece != "" && i < len(kids) && !script(&kids[i], r.version) {
				piece += "\u2061"
			}
		case n.kind == recChar:
			if n.r != 0 {
				piece = string(n.r)
			}
			piece += n.marks
			i++
		case n.kind == recTmpl:
			var fence bool
			piece, fence = r.template(n)
			isStructure = !fence
			i++
		default:
			piece = r.render(n)
			isStructure = true
			i++
		}
		if piece == "" {
			continue
		}
		if structure && joins(piece) {
			sb.WriteByte(' ')
		}
		sb.WriteString(piece)
		structure = isStructure
	}
	return sb.String()
}

// The templates, as this package names them whatever the version.
const (
	tFence = iota + 1
	tRoot
	tFraction
	tSlash
	tSub
	tSup
	tSubSup
	tScript // by its variation
	tLead   // scripts before what they belong to
	tUnder
	tOver
	tArrow
	tBig
	tLimit
	tBraceOver
	tBraceUnder
	tAccent
	tBox
	tOther
)

type shape struct {
	kind        int
	open, close string // of a fence
	sign        string // of a big operator, an accent, an arrow
}

var shapes3 = map[int]shape{
	0: {tFence, "⟨", "⟩", ""}, 1: {tFence, "(", ")", ""}, 2: {tFence, "{", "}", ""}, 3: {tFence, "[", "]", ""},
	4: {tFence, "|", "|", ""}, 5: {tFence, "‖", "‖", ""}, 6: {tFence, "⌊", "⌋", ""}, 7: {tFence, "⌈", "⌉", ""},
	8: {tFence, "[", "[", ""}, 9: {tFence, "]", "]", ""}, 10: {tFence, "]", "[", ""}, 11: {tFence, "[", ")", ""}, 12: {tFence, "(", "]", ""},
	13: {kind: tRoot}, 14: {kind: tFraction}, 15: {kind: tScript}, 16: {kind: tUnder}, 17: {kind: tOver},
	18: {kind: tArrow, sign: "←"}, 19: {kind: tArrow, sign: "→"}, 20: {kind: tArrow, sign: "↔"},
	21: {kind: tBig, sign: "∫"}, 22: {kind: tBig, sign: "∬"}, 23: {kind: tBig, sign: "∭"},
	24: {kind: tBig, sign: "∫"}, 25: {kind: tBig, sign: "∬"}, 26: {kind: tBig, sign: "∭"},
	27: {kind: tBraceOver}, 28: {kind: tBraceUnder},
	29: {kind: tBig, sign: "∑"}, 30: {kind: tBig, sign: "∑"}, 31: {kind: tBig, sign: "∏"}, 32: {kind: tBig, sign: "∏"},
	33: {kind: tBig, sign: "∐"}, 34: {kind: tBig, sign: "∐"}, 35: {kind: tBig, sign: "⋃"}, 36: {kind: tBig, sign: "⋃"},
	37: {kind: tBig, sign: "⋂"}, 38: {kind: tBig, sign: "⋂"}, 39: {kind: tLimit}, 41: {kind: tSlash},
	42: {kind: tBig}, 43: {kind: tBig}, 44: {kind: tLead},
}

var shapes5 = map[int]shape{
	0: {tFence, "⟨", "⟩", ""}, 1: {tFence, "(", ")", ""}, 2: {tFence, "{", "}", ""}, 3: {tFence, "[", "]", ""},
	4: {tFence, "|", "|", ""}, 5: {tFence, "‖", "‖", ""}, 6: {tFence, "⌊", "⌋", ""}, 7: {tFence, "⌈", "⌉", ""},
	8: {tFence, "[", "[", ""}, 9: {tFence, "", "", ""},
	10: {kind: tRoot}, 11: {kind: tFraction}, 12: {kind: tUnder}, 13: {kind: tOver}, 14: {kind: tArrow, sign: "→"},
	15: {kind: tBig, sign: "∫"}, 16: {kind: tBig, sign: "∑"}, 17: {kind: tBig, sign: "∏"}, 18: {kind: tBig, sign: "∐"},
	19: {kind: tBig, sign: "⋃"}, 20: {kind: tBig, sign: "⋂"}, 21: {kind: tBig}, 22: {kind: tBig}, 23: {kind: tLimit},
	24: {kind: tBraceOver}, 25: {kind: tBraceOver}, 27: {kind: tSub}, 28: {kind: tSup}, 29: {kind: tSubSup},
	31: {kind: tAccent, sign: "\u20D7"}, 32: {kind: tAccent, sign: "\u0303"}, 33: {kind: tAccent, sign: "\u0302"},
	34: {kind: tAccent, sign: "\u0311"}, 37: {kind: tBox},
}

func (r *reader) shape(n *node) shape {
	var s shape
	var ok bool
	if r.version == 5 {
		s, ok = shapes5[n.sel]
	} else {
		s, ok = shapes3[n.sel]
	}
	if !ok {
		return shape{kind: tOther}
	}
	return s
}

// script reports whether a record is a template of scripts, which go
// with what is before them.
func script(n *node, version int) bool {
	if n.kind != recTmpl {
		return false
	}
	if version == 5 {
		return n.sel >= 27 && n.sel <= 29
	}
	return n.sel == 15
}

// template writes a template: its slots are the lines, piles and
// matrices of its list, and its signs (the brackets of a fence, the sign
// of a sum) the characters. fence says the result is closed by brackets
// of its own.
func (r *reader) template(n *node) (out string, fence bool) {
	// Each record of the list is written once, however the template is
	// then put together: templates nest.
	var slots, signs []string
	pieces := make([]string, len(n.kids))
	for i := range n.kids {
		k := &n.kids[i]
		switch k.kind {
		case recChar:
			if k.r != 0 {
				pieces[i] = string(k.r) + k.marks
			}
			if k.r != 0 && k.r != ' ' {
				signs = append(signs, pieces[i])
			}
		case recLine, recPile, recMatrix, recTmpl:
			pieces[i] = r.render(k)
			slots = append(slots, pieces[i])
		}
	}
	slot := func(i int) string {
		if i < len(slots) {
			return slots[i]
		}
		return ""
	}
	s := r.shape(n)
	switch s.kind {
	case tFence:
		open, closer := s.open, s.close
		// The brackets in the list are the pieces they are drawn with,
		// not characters to read; the selector says what they are, and
		// the variation which of the two there is.
		left, right := true, true
		if r.version == 5 {
			left, right = n.vari&1 != 0, n.vari&2 != 0
			if !left && !right {
				left, right = true, true
			}
		} else {
			left, right = n.vari != 2, n.vari != 1
		}
		if !left {
			open = ""
		}
		if !right {
			closer = ""
		}
		return open + strings.Join(slots, "│") + closer, left && right
	case tRoot:
		e, deg := slot(0), slot(1)
		switch deg {
		case "":
			return "√" + group(e), false
		case "3":
			return "∛" + group(e), false
		case "4":
			return "∜" + group(e), false
		}
		return "√(" + deg + "&" + e + ")", false
	case tFraction:
		return group(slot(0)) + "/" + group(slot(1)), false
	case tSlash:
		return group(slot(0)) + "∕" + group(slot(1)), false
	case tSub, tSup, tSubSup, tScript, tLead:
		sub, sup := slot(0), slot(1)
		if len(slots) == 1 {
			lone := s.kind == tSup || s.kind == tScript && n.vari == 0
			if lone {
				sub, sup = "", slot(0)
			}
		}
		if s.kind == tSub {
			sup = ""
		}
		if s.kind == tSup && len(slots) > 1 {
			sub = ""
		}
		out := ""
		if sub != "" {
			out += "_" + group(sub)
		}
		if sup != "" {
			out += "^" + group(sup)
		}
		return out, false
	case tUnder:
		return "▁" + group(slot(0)), false
	case tOver:
		return "¯" + group(slot(0)), false
	case tArrow:
		sign := s.sign
		if len(signs) > 0 && known(signs[0]) {
			sign = signs[0]
		}
		out := sign
		if top := slot(0); top != "" {
			out += "┴" + group(top)
		}
		if bottom := slot(1); bottom != "" {
			out += "┬" + group(bottom)
		}
		return out, false
	case tBig:
		sign := s.sign
		if len(signs) > 0 && known(signs[0]) {
			sign = signs[0]
		}
		if sign == "" {
			// An operator the author wrote: it is the last slot.
			sign = group(slot(3))
		}
		out := sign
		if sub := slot(1); sub != "" {
			out += "_" + group(sub)
		}
		if sup := slot(2); sup != "" {
			out += "^" + group(sup)
		}
		if of := slot(0); of != "" {
			out += "▒" + group(of)
		}
		return out, false
	case tLimit:
		out := name(slot(0))
		if under := slot(1); under != "" {
			out += "┬" + group(under)
		}
		if over := slot(2); over != "" {
			out += "┴" + group(over)
		}
		return out, false
	case tBraceOver, tBraceUnder:
		sign, side := "⏞", "┴"
		if s.kind == tBraceUnder || r.version == 5 && n.vari&1 == 0 {
			sign, side = "⏟", "┬"
		}
		out := sign + group(slot(0))
		if label := slot(1); label != "" {
			out += side + group(label)
		}
		return out, false
	case tAccent:
		return group(slot(0)) + s.sign, false
	case tBox:
		return "▭" + group(slot(0)), false
	}
	// A template this package has no form for: its slots and signs, in
	// their order.
	var sb strings.Builder
	for i := range n.kids {
		piece := pieces[i]
		if n.kids[i].kind != recChar && piece != "" {
			piece = group(piece)
		}
		sb.WriteString(piece)
	}
	return sb.String(), false
}

// known reports whether a sign is a character Unicode has.
func known(s string) bool { return s != "" && !strings.ContainsRune(s, utf8.RuneError) }

// joins reports whether text that starts so would be read as part of a
// structure before it.
func joins(piece string) bool {
	r, _ := utf8.DecodeRuneInString(piece)
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '(' || r == '[' || r == '{'
}

// group returns an argument as it is written after an operator: as it
// is if it is one symbol, a number or already in brackets, else in
// parentheses.
func group(x string) string {
	if single(x) {
		return x
	}
	return "(" + x + ")"
}

// name is group for what may be the name of a function (lim, max), which
// UnicodeMath reads as one thing.
func name(x string) string {
	for _, r := range x {
		if !unicode.IsLetter(r) {
			return group(x)
		}
	}
	return x
}

// single reports whether x reads as one thing after an operator.
func single(x string) bool {
	if x == "" || bracketed(x) {
		return true
	}
	symbols, digits := 0, true
	for _, r := range x {
		if unicode.Is(unicode.Mn, r) {
			continue // a combining mark goes with what it follows
		}
		symbols++
		if !unicode.IsDigit(r) && r != '.' {
			digits = false
		}
	}
	return symbols == 1 || digits
}

// bracketed reports whether x is wholly inside one pair of brackets.
func bracketed(x string) bool {
	open, size := utf8.DecodeRuneInString(x)
	closer, ok := pairs[open]
	if !ok {
		return false
	}
	last, lastSize := utf8.DecodeLastRuneInString(x)
	if last != closer || len(x) == size {
		return false
	}
	// The opening bracket must close at the very end.
	depth := 0
	inner := x[:len(x)-lastSize]
	for _, r := range inner {
		switch r {
		case open:
			depth++
		case closer:
			depth--
			if depth == 0 {
				return false
			}
		}
	}
	return depth == 1
}

var pairs = map[rune]rune{'(': ')', '[': ']', '{': '}', '〖': '〗', '⟨': '⟩'}
