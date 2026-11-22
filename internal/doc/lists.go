package doc

import (
	"strconv"
	"strings"
	"unicode/utf16"
)

// The definitions of lists ([MS-DOC] 2.9.131 and after): the list table
// (PlfLst), each list with one level or nine, and the overrides (PlfLfo),
// which paragraphs name by number and which say which list they use and
// what they change of it.

// Bounds on the lists of a document.
const (
	maxLists    = 1 << 14
	maxLabelLen = 64 // characters of a label's template
)

// nfcFormat names a number format code as the numbering engine knows it
// ([MS-OSHARED] 2.2.1.3, MSONFC); one it does not know is decimal.
func nfcFormat(nfc byte) string {
	switch nfc {
	case 0:
		return "decimal"
	case 1:
		return "upperRoman"
	case 2:
		return "lowerRoman"
	case 3:
		return "upperLetter"
	case 4:
		return "lowerLetter"
	case 5:
		return "ordinal"
	case 6:
		return "cardinalText"
	case 7:
		return "ordinalText"
	case 9:
		return "chicago"
	case 18:
		return "decimalEnclosedCircle"
	case 22:
		return "decimalZero"
	case 23:
		return "bullet"
	case 255:
		return "none"
	}
	return "decimal"
}

// readLevel reads one level of a list (LVL) at the start of b, and
// returns what follows it; ok is false where the level breaks off.
func (d *Doc) readLevel(b []byte, l *level) (rest []byte, ok bool) {
	if len(b) < 28 {
		return nil, false
	}
	l.set = true
	l.start = int(int32(le.Uint32(b)))
	l.format = nfcFormat(b[4])
	l.legal = b[5]&0x04 != 0
	l.restart = -1
	if b[5]&0x08 != 0 { // the level says after which level it starts again
		l.restart = int(b[26])
	}
	l.style = -1
	chpx, papx := int(b[24]), int(b[25])
	b = b[28:]
	if papx+chpx+2 > len(b) {
		return nil, false
	}
	// The font of the label decides what a bullet is.
	var c chp
	c.apply(b[papx:papx+chpx], &c)
	l.font = d.fontName(c.font)
	b = b[papx+chpx:]
	// The label's template: a count and that many characters; one below
	// nine stands for the number of that level.
	n := int(le.Uint16(b))
	b = b[2:]
	if 2*n > len(b) {
		return nil, false
	}
	var sb strings.Builder
	u := make([]uint16, 0, min(n, maxLabelLen))
	flush := func() {
		sb.WriteString(string(utf16.Decode(u)))
		u = u[:0]
	}
	for i := 0; i < n && i < maxLabelLen; i++ {
		ch := le.Uint16(b[2*i:])
		if ch < 9 && l.format != "bullet" {
			flush()
			sb.WriteString("%" + strconv.Itoa(int(ch)+1))
			continue
		}
		if ch == '%' && l.format != "bullet" {
			continue // would read as a level's number
		}
		u = append(u, ch)
	}
	flush()
	l.text = sb.String()
	return b[2*n:], true
}

// readLists reads the list table and the overrides into the numbering.
// The levels of the lists are not part of the list table: they follow it
// in the table stream, and after is what follows it.
func (d *Doc) readLists(lst, after, lfo []byte) *numbering {
	n := &numbering{abstracts: map[int]*abstractNum{}, nums: map[int]*num{}}
	if len(lst) < 2 {
		return n
	}
	count := min(int(le.Uint16(lst)), maxLists)
	if 2+28*count > len(lst) {
		count = (len(lst) - 2) / 28
	}
	heads, levels := lst[2:2+28*count], after
	if rest := lst[2+28*count:]; len(rest) > 0 {
		levels = rest // a table whose length counts its levels
	}
	byID := map[uint32]int{} // the place of each list, by its identifier
	for i := 0; i < count; i++ {
		h := heads[28*i:]
		a := &abstractNum{}
		n.abstracts[i] = a
		byID[le.Uint32(h)] = i
		depth := 9
		if h[26]&0x01 != 0 {
			depth = 1 // a list of one level
		}
		ok := true
		for k := 0; k < depth && ok; k++ {
			levels, ok = d.readLevel(levels, &a.levels[k])
			if !ok {
				a.levels[k] = level{}
			}
			// The paragraph style the level belongs to, if any.
			if istd := le.Uint16(h[8+2*k:]); ok && istd != noBase {
				a.levels[k].style = int(istd)
			}
		}
		if !ok {
			levels = nil
		}
	}
	if len(lfo) < 4 {
		return n
	}
	count = min(int(le.Uint32(lfo)), maxLists)
	if 4+16*count > len(lfo) {
		count = (len(lfo) - 4) / 16
	}
	heads, data := lfo[4:4+16*count], lfo[4+16*count:]
	for i := 0; i < count; i++ {
		h := heads[16*i:]
		inst := &num{abstract: -1}
		if a, ok := byID[le.Uint32(h)]; ok {
			inst.abstract = a
		}
		n.nums[i+1] = inst // a paragraph names its override from 1
		overrides := int(h[12])
		// What the override changes: four bytes, then for each level it
		// changes its number, flags, and the level's new definition where
		// the flags say there is one.
		if len(data) < 4 {
			data = nil
			continue
		}
		data = data[4:]
		for k := 0; k < overrides && k < maxLevels; k++ {
			if len(data) < 8 {
				data = nil
				break
			}
			start, flags := int(int32(le.Uint32(data))), le.Uint32(data[4:])
			data = data[8:]
			lvl := int(flags & 0xF)
			if lvl >= maxLevels {
				data = nil
				break
			}
			o := &inst.override[lvl]
			if flags&0x10 != 0 {
				o.start, o.hasStart = start, true
			}
			if flags&0x20 != 0 {
				var ok bool
				if data, ok = d.readLevel(data, &o.level); !ok {
					o.level = level{}
					data = nil
					break
				}
			}
		}
	}
	return n
}

// readFonts reads the names of the fonts (SttbfFfn, [MS-DOC] 2.9.275): a
// count, then for each font its length and its record, in which the name
// follows forty bytes of properties.
func readFonts(b []byte) []string {
	if len(b) < 4 {
		return nil
	}
	count := int(le.Uint16(b))
	b = b[4:]
	var out []string
	for i := 0; i < count && len(b) > 0 && len(out) < 1<<14; i++ {
		size := int(b[0])
		if 1+size > len(b) {
			break
		}
		ffn := b[1 : 1+size]
		b = b[1+size:]
		name := ""
		if len(ffn) > 39 {
			raw := ffn[39:]
			u := make([]uint16, 0, len(raw)/2)
			for k := 0; k+1 < len(raw); k += 2 {
				c := le.Uint16(raw[k:])
				if c == 0 {
					break
				}
				u = append(u, c)
			}
			name = string(utf16.Decode(u))
		}
		out = append(out, name)
	}
	return out
}

// fontName returns the name of the font with the given number.
func (d *Doc) fontName(i uint16) string {
	if int(i) < len(d.fonts) {
		return d.fonts[i]
	}
	return ""
}
