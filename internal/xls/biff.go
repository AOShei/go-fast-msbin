// Package xls reads Excel workbooks in the binary format of Excel 97 to
// 2003 (BIFF8, [MS-XLS]) and of Excel 5 and 95 (BIFF5): the Workbook
// stream, or the Book stream of the older format, a run of records.
package xls

import (
	"encoding/binary"
	"unicode/utf8"

	"github.com/AOShei/go-fast-msbin/internal/codepage"
)

// Record types that are read ([MS-XLS] 2.3).
const (
	recFormula         = 0x0006
	recFormula3        = 0x0206 // as Excel 3 and 4 numbered the record; some
	recFormula4        = 0x0406 // programs write it so in a later file
	recEOF             = 0x000A
	recExternSheet     = 0x0017
	recName            = 0x0018
	recNote            = 0x001C
	recDateMode        = 0x0022
	recExternName      = 0x0023
	recFont            = 0x0031
	recContinue        = 0x003C
	recCodePage        = 0x0042
	recObj             = 0x005D
	recColInfo         = 0x007D
	recWsBool          = 0x0081
	recBoundSheet      = 0x0085
	recMsoDrawingGroup = 0x00EB
	recMsoDrawing      = 0x00EC
	recSxView          = 0x00B0
	recMulRK           = 0x00BD
	recMulBlank        = 0x00BE
	recRString         = 0x00D6
	recXF              = 0x00E0
	recMergeCells      = 0x00E5
	recSST             = 0x00FC
	recLabelSST        = 0x00FD
	recSupBook         = 0x01AE
	recTxo             = 0x01B6
	recHLink           = 0x01B8
	recDV              = 0x01BE
	recDimensions      = 0x0200
	recNumber          = 0x0203
	recLabel           = 0x0204
	recBoolErr         = 0x0205
	recString          = 0x0207
	recRow             = 0x0208
	recArray           = 0x0221
	recTable           = 0x0236
	recRK              = 0x027E
	recFormat          = 0x041E
	recShrFmla         = 0x04BC
	recBOF             = 0x0809
)

var le = binary.LittleEndian

// cursor reads the body of a record, and on into the CONTINUE records
// that follow it where the body is longer than a record can be.
type cursor struct {
	data []byte // the stream, or the part of it the records are read from
	// more, where the data is a part of the stream, returns it with what
	// follows it in the stream added, or as it is at the stream's end.
	more  func(have []byte) []byte
	b     []byte // what is left of the record's body
	next  int    // where the next record starts
	short bool   // a record ran past the end of the stream

	// keepRuns asks for the formatting runs of the strings read, which
	// are then in runs after each.
	keepRuns bool
	runs     []run

	// cp is the code page of the strings of a workbook of Excel 5 or 95,
	// which are bytes with no flags before them; zero for a workbook of
	// Excel 97 and later. undecoded counts the characters the page does
	// not define.
	cp        int
	undecoded int
	tmp       []byte
}

// bytes reads a string of cch bytes in the cursor's code page.
func (c *cursor) bytes(dst []byte, cch int) ([]byte, bool) {
	c.tmp = c.tmp[:0]
	ok := true
	for cch > 0 {
		if len(c.b) == 0 {
			if !c.cont() || len(c.b) == 0 {
				ok = false
				break
			}
			continue
		}
		n := min(cch, len(c.b))
		c.tmp = append(c.tmp, c.b[:n]...)
		c.b = c.b[n:]
		cch -= n
	}
	text, undecoded := codepage.Decode(c.cp, c.tmp)
	c.undecoded += undecoded
	return append(dst, text...), ok
}

// record puts the cursor on the record at offset at, and returns its
// type. ok is false at the end of the stream.
func (c *cursor) record(at int) (typ uint16, ok bool) {
	c.need(at + 4)
	if at < 0 || at+4 > len(c.data) {
		c.b, c.next = nil, len(c.data)
		return 0, false
	}
	typ = le.Uint16(c.data[at:])
	end := at + 4 + int(le.Uint16(c.data[at+2:]))
	c.need(end)
	if end > len(c.data) {
		end, c.short = len(c.data), true
	}
	c.b, c.next = c.data[at+4:end], end
	return typ, true
}

// need sees to it that the data reaches to end, where the stream does.
func (c *cursor) need(end int) {
	for end > len(c.data) && c.more != nil {
		n := len(c.data)
		if c.data = c.more(c.data); len(c.data) == n {
			c.more = nil // the end of the stream
		}
	}
}

// peek returns the type of the record that follows, without moving to
// it.
func (c *cursor) peek() (typ uint16, ok bool) {
	c.need(c.next + 4)
	if c.next < 0 || c.next+4 > len(c.data) {
		return 0, false
	}
	return le.Uint16(c.data[c.next:]), true
}

// cont moves to the record that follows if it is a CONTINUE record.
func (c *cursor) cont() bool {
	if typ, ok := c.peek(); !ok || typ != recContinue {
		return false
	}
	c.record(c.next)
	return true
}

// skip passes over n bytes, into CONTINUE records where they go on.
func (c *cursor) skip(n int64) bool {
	for n > 0 {
		if len(c.b) == 0 {
			if !c.cont() {
				return false
			}
			continue
		}
		k := min(n, int64(len(c.b)))
		c.b = c.b[k:]
		n -= k
	}
	return true
}

// take fills p with the bytes that follow, into CONTINUE records where
// they go on.
func (c *cursor) take(p []byte) bool {
	for len(p) > 0 {
		if len(c.b) == 0 {
			if !c.cont() {
				return false
			}
			continue
		}
		n := copy(p, c.b)
		p, c.b = p[n:], c.b[n:]
	}
	return true
}

// chars appends cch characters to dst as UTF-8. wide says they are 16
// bits each; otherwise each is one byte, the low half of a character
// below U+0100. Where the characters go on into a CONTINUE record, its
// first byte says anew which of the two they are ([MS-XLS] 2.5.293). ok
// is false where the stream ends before the characters do.
func (c *cursor) chars(dst []byte, cch int, wide bool) (out []byte, ok bool) {
	var high rune // the first half of a surrogate pair, waiting for the second
	flush := func() {
		if high != 0 {
			dst = append(dst, 0xEF, 0xBF, 0xBD)
			high = 0
		}
	}
	for cch > 0 {
		if len(c.b) == 0 || wide && len(c.b) == 1 {
			if !c.cont() || len(c.b) == 0 {
				flush()
				return dst, false
			}
			wide = c.b[0]&1 != 0
			c.b = c.b[1:]
			continue
		}
		if !wide {
			n := min(cch, len(c.b))
			flush()
			for _, ch := range c.b[:n] {
				if ch < 0x80 {
					dst = append(dst, ch)
				} else {
					dst = append(dst, 0xC0|ch>>6, 0x80|ch&0x3F)
				}
			}
			c.b = c.b[n:]
			cch -= n
			continue
		}
		n := min(cch, len(c.b)/2)
		for i := 0; i < n; i++ {
			u := rune(le.Uint16(c.b[2*i:]))
			switch {
			case u < 0x80:
				flush()
				dst = append(dst, byte(u))
			case u >= 0xD800 && u < 0xDC00:
				flush()
				high = u
			case u >= 0xDC00 && u < 0xE000:
				if high == 0 {
					dst = append(dst, 0xEF, 0xBF, 0xBD)
					continue
				}
				dst = utf8.AppendRune(dst, 0x10000+(high-0xD800)<<10+(u-0xDC00))
				high = 0
			default:
				flush()
				dst = utf8.AppendRune(dst, u)
			}
		}
		c.b = c.b[2*n:]
		cch -= n
	}
	flush()
	return dst, true
}

// str reads a string whose length is in 16 bits (XLUnicodeString,
// [MS-XLS] 2.5.294): the count of characters, a byte of flags, the
// characters.
func (c *cursor) str(dst []byte) ([]byte, bool) {
	c.runs = c.runs[:0]
	if c.cp != 0 {
		if len(c.b) < 2 {
			return dst, false
		}
		cch := int(le.Uint16(c.b))
		c.b = c.b[2:]
		return c.bytes(dst, cch)
	}
	if len(c.b) < 3 {
		return dst, len(c.b) == 2 && le.Uint16(c.b) == 0
	}
	cch, flags := int(le.Uint16(c.b)), c.b[2]
	c.b = c.b[3:]
	return c.rich(dst, cch, flags)
}

// shortStr reads a string whose length is in 8 bits (ShortXLUnicodeString,
// [MS-XLS] 2.5.240).
func (c *cursor) shortStr(dst []byte) ([]byte, bool) {
	c.runs = c.runs[:0]
	if c.cp != 0 {
		if len(c.b) < 1 {
			return dst, false
		}
		cch := int(c.b[0])
		c.b = c.b[1:]
		return c.bytes(dst, cch)
	}
	if len(c.b) < 2 {
		return dst, false
	}
	cch, flags := int(c.b[0]), c.b[1]
	c.b = c.b[2:]
	return c.rich(dst, cch, flags)
}

// run is a formatting run of a string: from the character at, counted in
// 16-bit units, the text is in the font with the number font.
type run struct{ at, font uint16 }

// rich reads the rest of a string after its count and flags: the counts
// of formatting runs and of the bytes of phonetic data, if the flags say
// it has them, the characters, and then those runs and that data. The
// phonetic data is passed over: it is another rendering of the same
// characters ([MS-XLS] 2.5.293). The runs are read into c.runs where
// c.keepRuns asks for them, and passed over otherwise.
func (c *cursor) rich(dst []byte, cch int, flags byte) ([]byte, bool) {
	const fHighByte, fExtSt, fRichSt = 0x01, 0x04, 0x08
	c.runs = c.runs[:0]
	var runs, ext int64
	if flags&fRichSt != 0 {
		if len(c.b) < 2 {
			return dst, false
		}
		runs = int64(le.Uint16(c.b))
		c.b = c.b[2:]
	}
	if flags&fExtSt != 0 {
		if len(c.b) < 4 {
			return dst, false
		}
		ext = int64(le.Uint32(c.b))
		c.b = c.b[4:]
	}
	dst, ok := c.chars(dst, cch, flags&fHighByte != 0)
	if ok && runs > 0 && c.keepRuns {
		for ; runs > 0; runs-- {
			var r [4]byte
			if !c.take(r[:]) {
				return dst, false
			}
			c.runs = append(c.runs, run{at: le.Uint16(r[:]), font: le.Uint16(r[2:])})
		}
	}
	if after := 4*runs + ext; ok && after > 0 {
		ok = c.skip(after)
	}
	return dst, ok
}
