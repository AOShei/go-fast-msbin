package msbintest

import (
	"encoding/binary"
	"unicode/utf16"
)

// This file writes Word documents ([MS-DOC]) for the tests, on its own
// from the published description: the file information block, the text,
// the pages of properties, the style sheet and the piece table.

// Sprm is one change to a property: its code and its operand.
func Sprm(op uint16, arg ...byte) []byte {
	return append(binary.LittleEndian.AppendUint16(nil, op), arg...)
}

// DocRun is a run of characters of a paragraph: how many, and their
// changes to the properties.
type DocRun struct {
	Len  int
	Chpx []byte
}

// DocPara is a paragraph: its text, the character that ends it (a
// paragraph mark if zero; 0x07 for the end of a cell or of a row), its
// style, its changes to the style, and its runs (one run of all its text
// if nil). The characters 0x02 (a note's number), 0x13, 0x14 and 0x15
// (the marks of a field) are made special.
type DocPara struct {
	Text  string
	Mark  byte
	Style uint16
	Papx  []byte
	Runs  []DocRun
}

// DocStyle is a style: its name, the style it is based on (0x0FFF for
// none), and its changes to a paragraph's and its characters' properties.
// Kind is 1 for a paragraph style (the default) and 2 for a character
// style.
type DocStyle struct {
	Name string
	Base uint16
	Kind uint16
	Papx []byte
	Chpx []byte
}

// DocFile is a Word document: its styles by number, its main text, and
// the texts of its footnotes, each referred to by a character 0x02 in
// the main text, in order.
type DocFile struct {
	Styles    []DocStyle
	Main      []DocPara
	Footnotes [][]DocPara
	// Narrow writes the text one byte to a character, which every
	// character of it must fit.
	Narrow bool
	// Reserve leaves so many bytes free at the start of the table
	// stream.
	Reserve int
}

const (
	docTextAt = 0x0800 // where the text begins in the WordDocument stream
	docPage   = 512
)

type docRunAt struct {
	end  int // the character after its last, counted over all stories
	data []byte
}

// WordStreams returns the streams of the document: WordDocument and
// 1Table.
func WordStreams(d DocFile) map[string][]byte {
	le := binary.LittleEndian
	var units []uint16
	var paras, runs []docRunAt
	width := 2
	if d.Narrow {
		width = 1
	}
	story := func(list []DocPara) {
		for _, p := range list {
			text := utf16.Encode([]rune(p.Text))
			start := len(units)
			units = append(units, text...)
			mark := p.Mark
			if mark == 0 {
				mark = 0x0D
			}
			units = append(units, uint16(mark))
			paras = append(paras, docRunAt{len(units), append(U16(p.Style), p.Papx...)})
			// The runs: as given, with the special characters apart.
			given := p.Runs
			if given == nil {
				given = []DocRun{{Len: len(text)}}
			}
			at := start
			for _, run := range given {
				for k := 0; k < run.Len; {
					special := func(u uint16) bool { return u == 0x02 || u == 0x13 || u == 0x14 || u == 0x15 }
					j := k
					if special(text[at-start+k]) {
						j = k + 1
						runs = append(runs, docRunAt{at + j, append(append([]byte(nil), run.Chpx...), Sprm(0x0855, 1)...)})
					} else {
						for j < run.Len && !special(text[at-start+j]) {
							j++
						}
						runs = append(runs, docRunAt{at + j, run.Chpx})
					}
					k = j
				}
				at += run.Len
			}
			runs = append(runs, docRunAt{len(units), nil}) // the mark
		}
	}
	story(d.Main)
	ccpText := len(units)
	var noteStarts []int
	for _, note := range d.Footnotes {
		noteStarts = append(noteStarts, len(units)-ccpText)
		story(note)
	}
	ccpFtn := len(units) - ccpText
	if ccpFtn > 0 {
		noteStarts = append(noteStarts, ccpFtn, ccpFtn+1) // the end of the last, and of the story
	}
	// Where the footnotes are referred to.
	var refs []int
	for i := 0; i < ccpText; i++ {
		if units[i] == 0x02 {
			refs = append(refs, i)
		}
	}

	word := make([]byte, docTextAt)
	for _, u := range units {
		if d.Narrow {
			word = append(word, byte(u))
		} else {
			word = le.AppendUint16(word, u)
		}
	}
	fcOf := func(cp int) uint32 { return uint32(docTextAt + width*cp) }
	for len(word)%docPage != 0 {
		word = append(word, 0)
	}
	// Pages of properties: as many runs to a page as fit.
	pages := func(list []docRunAt, papx bool) (bte []byte) {
		var fcs, pns []uint32
		start := 0
		for i := 0; i < len(list); {
			page := make([]byte, docPage)
			entry := 1 // bytes of an entry beside its position
			if papx {
				entry = 13
			}
			top := docPage - 1
			n := 0
			var rgfc, rgb []byte
			rgfc = le.AppendUint32(rgfc, fcOf(start))
			for i+n < len(list) {
				data := list[i+n].data
				var stored []byte
				if papx {
					if len(data)%2 == 0 {
						stored = append([]byte{0, byte(len(data) / 2)}, data...) // a length of pairs after a zero
					} else {
						stored = append([]byte{byte((len(data) + 1) / 2)}, data...)
					}
				} else if len(data) > 0 {
					stored = append([]byte{byte(len(data))}, data...)
				}
				at := 0
				if len(stored) > 0 {
					at = (top - len(stored)) &^ 1
				}
				if 4*(n+2)+entry*(n+1) > min(at, top)-1 && n > 0 || len(stored) > 0 && at < 4*(n+2)+entry*(n+1) {
					break
				}
				if len(stored) > 0 {
					copy(page[at:], stored)
					top = at
				}
				b := make([]byte, entry)
				b[0] = byte(at / 2)
				rgb = append(rgb, b...)
				rgfc = le.AppendUint32(rgfc, fcOf(list[i+n].end))
				n++
			}
			copy(page, rgfc)
			copy(page[len(rgfc):], rgb)
			page[docPage-1] = byte(n)
			fcs = append(fcs, fcOf(start))
			pns = append(pns, uint32(len(word)/docPage))
			word = append(word, page...)
			start = list[i+n-1].end
			i += n
		}
		fcs = append(fcs, fcOf(start))
		return append(U32(fcs...), U32(pns...)...)
	}
	btePapx := pages(paras, true)
	bteChpx := pages(runs, false)

	// The table stream.
	// What an encrypted document says of its encryption is at the start
	// of the table stream; room is left for it.
	table := make([]byte, d.Reserve)
	fc, lcb := make([]uint32, 93), make([]uint32, 93)
	put := func(index int, data []byte) {
		if len(data) == 0 {
			return
		}
		fc[index], lcb[index] = uint32(len(table)), uint32(len(data))
		table = append(table, data...)
	}
	// The style sheet.
	stsh := Cat(U16(18), U16(uint16(len(d.Styles)), 10, 1, 0, 0, 0, 0, 0, 0))
	for _, s := range d.Styles {
		if s.Name == "" {
			stsh = append(stsh, U16(0)...)
			continue
		}
		kind := s.Kind
		if kind == 0 {
			kind = 1
		}
		name := utf16.Encode([]rune(s.Name))
		std := Cat(U16(0x0FFE, kind|s.Base<<4, 0, 0, 0), U16(uint16(len(name))), U16(name...), U16(0))
		upx := func(data []byte) {
			if len(std)%2 != 0 {
				std = append(std, 0)
			}
			std = append(std, append(U16(uint16(len(data))), data...)...)
		}
		if kind == 1 {
			le.PutUint16(std[4:], 2) // two lists of changes
			upx(append(U16(0), s.Papx...))
			upx(s.Chpx)
		} else {
			le.PutUint16(std[4:], 1)
			upx(s.Chpx)
		}
		stsh = append(stsh, append(U16(uint16(len(std))), std...)...)
	}
	put(1, stsh)
	if len(refs) > 0 {
		ref := U32()
		for _, r := range refs {
			ref = append(ref, U32(uint32(r))...)
		}
		ref = append(ref, U32(uint32(ccpText))...)
		for range refs {
			ref = append(ref, U16(1)...) // numbered by the program
		}
		put(2, ref)
		var txt []byte
		for _, s := range noteStarts {
			txt = append(txt, U32(uint32(s))...)
		}
		put(3, txt)
	}
	put(12, bteChpx)
	put(13, btePapx)
	// The piece table: one piece.
	pcd := uint32(docTextAt)
	if d.Narrow {
		pcd = pcd*2 | 0x40000000
	}
	plcpcd := Cat(U32(0, uint32(len(units))), U16(0), U32(pcd), U16(0))
	put(33, Cat([]byte{2}, U32(uint32(len(plcpcd))), plcpcd))

	// The file information block.
	fib := Cat(U16(0xA5EC, 0x00C1, 0, 0x0409, 0, 0x0200, 0x00BF, 0), make([]byte, 16), U16(14), make([]byte, 28), U16(22))
	lw := make([]uint32, 22)
	lw[3], lw[4] = uint32(ccpText), uint32(ccpFtn)
	fib = append(fib, U32(lw...)...)
	fib = append(fib, U16(93)...)
	for i := range fc {
		fib = append(fib, U32(fc[i], lcb[i])...)
	}
	copy(word, fib)
	return map[string][]byte{"WordDocument": word, "1Table": table}
}

// Word6Stream returns the WordDocument stream of a document of Word 6 or
// 95: a file information block of that version and the text, bytes in
// the code page of the language lid. The text holds its own marks: 0x0D
// ends a paragraph, 0x07 a cell, and a second 0x07 the row; 0x13, 0x14
// and 0x15 are the marks of a field. footnotes are the texts of
// footnotes referred to by the characters 0x02 of the text, in order.
func Word6Stream(lid uint16, text string, footnotes ...string) []byte {
	le := binary.LittleEndian
	const at = 0x400
	b := make([]byte, at)
	le.PutUint16(b, 0xA5DC)
	le.PutUint16(b[2:], 104)
	le.PutUint16(b[6:], lid)
	le.PutUint32(b[0x18:], at)
	le.PutUint32(b[0x34:], uint32(len(text)))
	body := []byte(text)
	var refs, starts []uint32
	for i := range body {
		if body[i] == 0x02 {
			refs = append(refs, uint32(i))
		}
	}
	notes := 0
	for _, f := range footnotes {
		starts = append(starts, uint32(notes))
		body = append(body, f...)
		body = append(body, 0x0D)
		notes += len(f) + 1
	}
	le.PutUint32(b[0x38:], uint32(notes))
	b = append(b, body...)
	if len(refs) > 0 {
		ref := append(U32(refs...), U32(uint32(len(text)))...)
		for range refs {
			ref = append(ref, U16(1)...)
		}
		le.PutUint32(b[0x68:], uint32(len(b)))
		le.PutUint32(b[0x6C:], uint32(len(ref)))
		b = append(b, ref...)
		txt := append(U32(starts...), U32(uint32(notes), uint32(notes+1))...)
		le.PutUint32(b[0x70:], uint32(len(b)))
		le.PutUint32(b[0x74:], uint32(len(txt)))
		b = append(b, txt...)
	}
	return b
}
