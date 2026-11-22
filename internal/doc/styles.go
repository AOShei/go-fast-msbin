package doc

import (
	"strings"
	"unicode/utf16"
)

// chp is the properties of a run of characters that are read ([MS-DOC]
// 2.6.1).
type chp struct {
	bold, italic, strike, vanish bool
	underline                    bool
	super, sub                   bool
	// spec says the run's characters are special: a picture, a footnote
	// reference, a field mark.
	spec bool
	// del and ins say the run was deleted, or inserted, with changes
	// tracked: by whom and when.
	del, ins         bool
	delBy, insBy     uint16
	delWhen, insWhen uint32
	// data, obj and ole2 are about the picture or object a special
	// character stands for.
	data, obj, ole2 bool
	pic             uint32 // where the picture or object is, if hasPic
	hasPic          bool
	font            uint16 // the font, by its number in the font table
	// A character of a symbol font: the font and the character's code.
	sym     bool
	symFont uint16
	symChar uint16
	istd    uint16 // the character style
}

// pap is the properties of a paragraph that are read ([MS-DOC] 2.6.2).
type pap struct {
	ilvl    uint8  // its level in its list
	ilfo    uint16 // its list, from 1; 0 for none
	outline uint8  // its outline level: 0 to 8 for a heading, 9 for body text
	// Tables: the paragraph is in a table, and how deep; it ends a row;
	// it ends a cell or a row of a table inside a table.
	inTable             bool
	itap                int32
	ttp                 bool
	innerCell, innerTtp bool
	table               []byte // the definition of the row's cells, on the paragraph that ends a row
	header              bool   // the row is a header row
}

// toggle reads an operand that turns a property on or off, or says it is
// as the style has it, or the opposite ([MS-DOC] 2.9.325, ToggleOperand).
func toggle(arg []byte, style bool) bool {
	switch arg[0] {
	case 0:
		return false
	case 1:
		return true
	case 0x80:
		return style
	case 0x81:
		return !style
	}
	return style
}

// apply makes the changes of a list to c. base is what the style has,
// for properties that are set relative to it.
func (c *chp) apply(grpprl []byte, base *chp) {
	eachSprm(grpprl, func(op uint16, arg []byte) bool {
		switch op {
		case sprmCFBold:
			c.bold = toggle(arg, base.bold)
		case sprmCFItalic:
			c.italic = toggle(arg, base.italic)
		case sprmCFStrike:
			c.strike = toggle(arg, base.strike)
		case sprmCFDStrike:
			c.strike = arg[0] != 0
		case sprmCFVanish:
			c.vanish = toggle(arg, base.vanish)
		case sprmCKul:
			c.underline = arg[0] != 0
		case sprmCIss:
			c.super, c.sub = arg[0] == 1, arg[0] == 2
		case sprmCFSpec:
			c.spec = arg[0] != 0
		case sprmCFRMarkDel:
			c.del = arg[0] != 0
		case sprmCFRMarkIns:
			c.ins = arg[0] != 0
		case sprmCIbstRMarkDe:
			c.delBy = le.Uint16(arg)
		case sprmCDttmRMarkDe:
			c.delWhen = le.Uint32(arg)
		case sprmCIbstRMark:
			c.insBy = le.Uint16(arg)
		case sprmCDttmRMark:
			c.insWhen = le.Uint32(arg)
		case sprmCFData:
			c.data = arg[0] != 0
		case sprmCFObj:
			c.obj = arg[0] != 0
		case sprmCFOle2:
			c.ole2 = arg[0] != 0
		case sprmCPicLocation:
			c.pic, c.hasPic = le.Uint32(arg), true
		case sprmCRgFtc0:
			c.font = le.Uint16(arg)
		case sprmCSymbol:
			// A length byte, the font and the character.
			if len(arg) >= 5 {
				c.sym, c.symFont, c.symChar = true, le.Uint16(arg[1:]), le.Uint16(arg[3:])
			} else if len(arg) == 4 {
				c.sym, c.symFont, c.symChar = true, le.Uint16(arg), le.Uint16(arg[2:])
			}
		case sprmCIstd:
			c.istd = le.Uint16(arg)
		}
		return true
	})
}

// apply makes the changes of a list to p.
func (p *pap) apply(grpprl []byte) {
	eachSprm(grpprl, func(op uint16, arg []byte) bool {
		switch op {
		case sprmPIlvl:
			p.ilvl = arg[0]
		case sprmPIlfo:
			p.ilfo = le.Uint16(arg)
		case sprmPOutLvl:
			p.outline = arg[0]
		case sprmPFInTable:
			p.inTable = arg[0] != 0
		case sprmPFTtp:
			p.ttp = arg[0] != 0
		case sprmPItap:
			p.itap = int32(le.Uint32(arg))
		case sprmPDtap:
			p.itap += int32(le.Uint32(arg))
		case sprmPFInnerTableCel:
			p.innerCell = arg[0] != 0
		case sprmPFInnerTtp:
			p.innerTtp = arg[0] != 0
		case sprmTDefTable:
			p.table = arg
		case sprmTTableHeader:
			p.header = arg[0] != 0
		}
		return true
	})
}

// What a style's name says it is.
type role uint8

const (
	roleNone role = iota
	roleTitle
	roleSubtitle
	roleQuote
	roleCode
)

// style is one style of the style sheet ([MS-DOC] 2.9.260).
type style struct {
	set      bool
	kind     uint8 // 1 a paragraph style, 2 a character style
	base     uint16
	name     string
	papx     []byte // its changes to a paragraph's properties
	chpx     []byte // and to its characters'
	resolved bool
	pap      pap
	chp      chp
	role     role
	outline  int8  // the heading level, from 0; -1 for none
	nested   uint8 // the list level one of Word's list styles states, or 0
}

// noBase is the number a style gives for its base when it has none.
const noBase = 0x0FFF

// maxStyles bounds the styles of a style sheet; Word's own limit is 4,079.
const maxStyles = 1 << 12

// styleSheet is the styles of a document, by number.
type styleSheet struct {
	styles []style
}

// readStyles reads the style sheet (STSH, [MS-DOC] 2.9.271).
func readStyles(b []byte) *styleSheet {
	st := &styleSheet{}
	if len(b) < 4 {
		return st
	}
	// The header: its length, then the number of styles and the length of
	// the fixed part of each.
	head := int(le.Uint16(b))
	if head < 4 || 2+head > len(b) {
		return st
	}
	count, fixed := int(le.Uint16(b[2:])), int(le.Uint16(b[4:]))
	b = b[2+head:]
	count = min(count, maxStyles)
	st.styles = make([]style, count)
	for i := 0; i < count && len(b) >= 2; i++ {
		size := int(le.Uint16(b))
		b = b[2:]
		if size > len(b) {
			break
		}
		std := b[:size]
		b = b[size:]
		if size < fixed || fixed < 8 {
			continue // an empty slot
		}
		s := &st.styles[i]
		s.set = true
		s.kind = uint8(le.Uint16(std[2:]) & 0xF)
		s.base = le.Uint16(std[2:]) >> 4
		upx := int(le.Uint16(std[4:]) & 0xF)
		rest := std[fixed:]
		// The name: a count of characters, the characters, a terminator.
		if len(rest) < 2 {
			continue
		}
		n := int(le.Uint16(rest))
		if 2+2*n+2 > len(rest) {
			continue
		}
		u := make([]uint16, n)
		for k := range u {
			u[k] = le.Uint16(rest[2+2*k:])
		}
		s.name = string(utf16.Decode(u))
		rest = rest[2+2*n+2:]
		at := size - len(rest) // where the changes begin, which is even
		next := func() []byte {
			if at%2 != 0 && len(rest) > 0 {
				rest, at = rest[1:], at+1
			}
			if len(rest) < 2 {
				return nil
			}
			n := int(le.Uint16(rest))
			if 2+n > len(rest) {
				n = len(rest) - 2
			}
			out := rest[2 : 2+n]
			rest, at = rest[2+n:], at+2+n
			return out
		}
		switch s.kind {
		case 1: // a paragraph style: its paragraph changes, after its own number, and its character changes
			if upx >= 1 {
				if p := next(); len(p) >= 2 {
					s.papx = p[2:]
				}
			}
			if upx >= 2 {
				s.chpx = next()
			}
		case 2:
			if upx >= 1 {
				s.chpx = next()
			}
		}
	}
	return st
}

// get returns the style with the given number, with what it inherits, or
// nil.
func (st *styleSheet) get(istd uint16) *style {
	if int(istd) >= len(st.styles) || !st.styles[istd].set {
		return nil
	}
	s := &st.styles[istd]
	st.resolve(s, 0)
	return s
}

// resolve fills in what a style takes from the styles it is based on.
func (st *styleSheet) resolve(s *style, depth int) {
	if s.resolved {
		return
	}
	s.resolved = true
	s.pap.outline = 9
	s.role, s.outline = classify(s.name)
	s.nested = nestedList(s.name)
	var parent *style
	if int(s.base) < len(st.styles) && s.base != noBase && depth < 32 && st.styles[s.base].set {
		parent = &st.styles[s.base]
		st.resolve(parent, depth+1)
		s.pap, s.chp = parent.pap, parent.chp
		s.pap.table = nil
		if s.role == roleNone {
			s.role = parent.role
		}
	}
	base := s.chp
	s.pap.apply(s.papx)
	s.chp.apply(s.chpx, &base)
	if s.outline < 0 {
		if s.pap.outline < 9 {
			s.outline = int8(s.pap.outline)
		} else if parent != nil {
			s.outline = parent.outline
		}
	}
}

// classify reads what a style's name says. Word stores the names of its
// built-in styles in English whatever the language of the interface, so
// "heading 2" is a second-level heading in every Word file.
func classify(name string) (role, int8) {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "title":
		return roleTitle, -1
	case "subtitle":
		return roleSubtitle, -1
	case "quote", "intense quote", "quotations", "block text":
		return roleQuote, -1
	case "html preformatted", "preformatted text", "source code", "code", "macro text", "plain text":
		return roleCode, -1
	}
	if strings.HasPrefix(n, "heading ") && len(n) == len("heading ")+1 {
		if d := n[len(n)-1]; d >= '1' && d <= '9' {
			return roleNone, int8(d - '1')
		}
	}
	return roleNone, -1
}

// nestedList returns the list level that one of Word's built-in list style
// names states ("List Bullet 3" is 3), or 0.
func nestedList(name string) uint8 {
	n := strings.ToLower(strings.TrimSpace(name))
	if len(n) < 6 || !strings.HasPrefix(n, "list") {
		return 0
	}
	d := n[len(n)-1]
	if d < '2' || d > '5' || n[len(n)-2] != ' ' {
		return 0
	}
	switch strings.TrimSpace(n[4 : len(n)-2]) {
	case "", "bullet", "number", "continue":
		return d - '0'
	}
	return 0
}
