package doc

import (
	"sort"
	"unicode/utf8"
)

// piece is a run of the document's text: its characters, by number, and
// where they are in the WordDocument stream ([MS-DOC] 2.8.35, 2.9.177).
type piece struct {
	cp     int64 // the first character
	n      int64 // how many
	fc     int64 // where they begin in the stream
	narrow bool  // one byte each, in the Windows code page; otherwise two, UTF-16
	prm    uint16
}

// end is the character after the last of the piece.
func (p *piece) end() int64 { return p.cp + p.n }

// fcOf is where the character cp of the piece is in the stream.
func (p *piece) fcOf(cp int64) int64 {
	if p.narrow {
		return p.fc + (cp - p.cp)
	}
	return p.fc + 2*(cp-p.cp)
}

// width is the bytes of a character of the piece.
func (p *piece) width() int64 {
	if p.narrow {
		return 1
	}
	return 2
}

// maxPieces bounds the pieces of a document. A document saved fast many
// times has thousands.
const maxPieces = 1 << 22

// readPieces reads the piece table out of the Clx ([MS-DOC] 2.9.38): a
// run of property modifiers, each kept for the pieces that name it, and
// then the table. size is the length of the WordDocument stream: a piece
// that lies outside it is cut to what is inside. bytes says the document
// is of Word 6 or 95, whose pieces are all one byte to a character.
func readPieces(clx []byte, size int64, bytes bool) (pieces []piece, prcs [][]byte, damaged bool) {
	for len(clx) > 0 {
		switch clx[0] {
		case 0x01: // property modifiers: a length and that many bytes
			if len(clx) < 3 {
				return nil, nil, true
			}
			n := int(le.Uint16(clx[1:]))
			if n > len(clx)-3 {
				return nil, nil, true
			}
			prcs = append(prcs, clx[3:3+n])
			clx = clx[3+n:]
			continue
		case 0x02:
		default:
			return nil, nil, true
		}
		// The table: a length, then one more character position than
		// pieces, then the pieces, eight bytes each.
		if len(clx) < 5 {
			return nil, nil, true
		}
		n := int64(le.Uint32(clx[1:]))
		body := clx[5:]
		if n > int64(len(body)) {
			n, damaged = int64(len(body)), true
		}
		body = body[:n]
		count := (len(body) - 4) / 12
		if count <= 0 {
			return nil, prcs, true
		}
		if count > maxPieces {
			count, damaged = maxPieces, true
		}
		cps, pcds := body, body[4*(count+1):]
		for i := 0; i < count; i++ {
			from, to := int64(le.Uint32(cps[4*i:])), int64(le.Uint32(cps[4*i+4:]))
			pcd := pcds[8*i:]
			fc := le.Uint32(pcd[2:])
			p := piece{cp: from, n: to - from, prm: le.Uint16(pcd[6:])}
			// A piece of one-byte characters says so with a bit, and
			// gives twice where it is.
			if bytes {
				// Word 6 and 95: the piece is bytes at the place it gives.
				p.narrow, p.fc = true, int64(fc)
			} else if fc&0x40000000 != 0 {
				p.narrow, p.fc = true, int64(fc&0x3FFFFFFF)/2
			} else {
				p.fc = int64(fc & 0x3FFFFFFF)
			}
			if p.n <= 0 || from < 0 {
				damaged = true
				continue
			}
			if last := len(pieces) - 1; last >= 0 && from < pieces[last].end() {
				damaged = true // the pieces are not in order
				continue
			}
			// What lies past the end of the stream is not there.
			if avail := (size - p.fc) / p.width(); avail < p.n {
				damaged = true
				if avail <= 0 {
					continue
				}
				p.n = avail
			}
			pieces = append(pieces, p)
		}
		return pieces, prcs, damaged
	}
	return nil, prcs, true
}

// text is the text of a document: its pieces over the WordDocument
// stream.
type text struct {
	stream []byte
	pieces []piece
}

// find returns the index of the piece that holds the character cp, or -1.
func (t *text) find(cp int64) int {
	i := sort.Search(len(t.pieces), func(i int) bool { return t.pieces[i].end() > cp })
	if i < len(t.pieces) && t.pieces[i].cp <= cp {
		return i
	}
	return -1
}

// unit returns the character at cp as its 16-bit code, and whether there
// is one.
func (t *text) unit(cp int64) (uint16, bool) {
	i := t.find(cp)
	if i < 0 {
		return 0, false
	}
	p := &t.pieces[i]
	at := p.fcOf(cp)
	if p.narrow {
		return uint16(cp1252[t.stream[at]]), true
	}
	return le.Uint16(t.stream[at:]), true
}

// cp1252 is what the bytes of a piece of one-byte characters stand for:
// the Windows Western code page, whose 0x80 to 0x9F are punctuation.
var cp1252 = func() (t [256]rune) {
	for i := range t {
		t[i] = rune(i)
	}
	for i, r := range [32]rune{0x20AC, 0x81, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x8D, 0x017D, 0x8F,
		0x90, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x9D, 0x017E, 0x0178} {
		t[0x80+i] = r
	}
	return t
}()

// appendRune appends a character given as a 16-bit code; half of a
// surrogate pair alone is U+FFFD.
func appendRune(dst []byte, r rune) []byte {
	if r < 0x80 {
		return append(dst, byte(r))
	}
	if r >= 0xD800 && r < 0xE000 {
		return append(dst, 0xEF, 0xBF, 0xBD)
	}
	return utf8.AppendRune(dst, r)
}
