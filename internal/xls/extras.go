package xls

import (
	"unicode/utf16"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/charset"
	"github.com/AOShei/go-fast-core/clean"
	"github.com/AOShei/go-fast-msbin/internal/grid"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// textObject reads the text of a drawing object (the TxO record and the
// CONTINUE records after it, [MS-XLS] 2.4.329). The text of a note is
// kept under the object's number, for the Note record that names it.
func (sh *sheet) textObject(b []byte) {
	if !sh.objNote || len(b) < 14 {
		return
	}
	sh.objNote = false
	cch := int(le.Uint16(b[10:]))
	c := &sh.c
	if cch == 0 || !c.cont() || len(c.b) == 0 {
		return
	}
	if len(sh.noteText) >= warn.MaxComments {
		return // counted when its note is met
	}
	wide := c.b[0]&1 != 0
	c.b = c.b[1:]
	raw, _ := c.chars(reuse(sh.raw), cch, wide)
	sh.raw = raw
	if sh.noteText == nil {
		sh.noteText = map[uint16]string{}
	}
	text, removed := clean.String(raw, clean.Prose)
	sh.removed += removed
	sh.noteText[sh.objID] = text
}

// note reads a Note record: the cell a comment is on, the object that
// holds its text and its author ([MS-XLS] 2.4.179).
func (sh *sheet) note(b []byte) {
	if len(b) < 8 {
		return
	}
	if len(sh.notes) >= warn.MaxComments {
		sh.lost.Add(warn.CapComments, 1)
		return
	}
	n := comment{row: int32(le.Uint16(b)), col: int32(le.Uint16(b[2:])), obj: le.Uint16(b[6:])}
	c := &sh.c
	c.b = b[8:]
	if raw, ok := c.str(reuse(sh.raw)); ok {
		author, removed := clean.String(raw, clean.Prose)
		sh.removed += removed
		n.author = sh.lost.Name(author)
		sh.raw = raw
	}
	sh.notes = append(sh.notes, n)
}

// note5 reads a Note record of Excel 5 or 95: the cell a comment is on,
// the length of its text and the text, which goes on in Note records
// with a row of 0xFFFF where it is longer than a record.
func (sh *sheet) note5(b []byte) {
	if len(b) < 6 {
		return
	}
	row, length := le.Uint16(b), int(le.Uint16(b[4:]))
	c := &sh.c
	c.b = b[6:]
	raw, _ := c.bytes(reuse(sh.raw), min(length, len(c.b)))
	sh.raw = raw
	text, removed := clean.String(raw, clean.Prose)
	sh.removed += removed
	if row == 0xFFFF {
		// More of the text of the note before.
		if n := len(sh.notes); n > 0 && sh.noteText != nil {
			sh.noteText[sh.notes[n-1].obj] += text
		}
		return
	}
	if len(sh.notes) >= warn.MaxComments {
		sh.lost.Add(warn.CapComments, 1)
		return
	}
	// The text is kept as that of an object numbered by the note's place.
	n := comment{row: int32(row), col: int32(le.Uint16(b[2:])), obj: uint16(len(sh.notes))}
	if sh.noteText == nil {
		sh.noteText = map[uint16]string{}
	}
	sh.noteText[n.obj] = text
	sh.notes = append(sh.notes, n)
}

// comments returns the comments of the sheet as notes at their cells.
func (sh *sheet) comments() []grid.Placed {
	var out []grid.Placed
	for _, n := range sh.notes {
		text := sh.noteText[n.obj]
		if text == "" || n.col >= grid.MaxCols {
			continue
		}
		if !sh.room.Take(gofastcore.BlockCost + len(text)) {
			if sh.g.Cut == "" {
				sh.g.Cut = "the limit on the amount of text was reached"
			}
			break
		}
		b := gofastcore.Block{Kind: gofastcore.KindNote, Text: text, Attrs: map[string]string{gofastcore.AttrNoteType: gofastcore.NoteComment}}
		if n.author != "" {
			b.Attrs[gofastcore.AttrAuthor] = n.author
		}
		b.Loc.Range = grid.CellName(n.row, n.col)
		out = append(out, grid.Placed{Row: n.row, Col: n.col, Block: b})
	}
	return out
}

// The class identifiers of the two kinds of target a hyperlink has
// ([MS-OSHARED] 2.3.7.2), as they stand in the file.
var (
	urlMoniker  = [16]byte{0xE0, 0xC9, 0xEA, 0x79, 0xF9, 0xBA, 0xCE, 0x11, 0x8C, 0x82, 0x00, 0xAA, 0x00, 0x4B, 0xA9, 0x0B}
	fileMoniker = [16]byte{0x03, 0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}
)

// hyperlink reads an HLink record: a range of cells and where they link
// to, an address outside the file or a place in the workbook ([MS-XLS]
// 2.4.140, [MS-OSHARED] 2.3.7.1).
func (sh *sheet) hyperlink(b []byte) {
	if len(b) < 32 {
		return
	}
	if len(sh.links)+len(sh.linkAt) >= warn.MaxLinks {
		sh.lost.Add(warn.CapLinks, 1)
		return
	}
	at := grid.Rect{R0: int32(le.Uint16(b)), R1: int32(le.Uint16(b[2:])), C0: int32(le.Uint16(b[4:])), C1: int32(le.Uint16(b[6:]))}
	if at.R0 > at.R1 || at.C0 > at.C1 {
		return
	}
	const (
		hasMoniker     = 0x001
		hasLocation    = 0x008
		hasDisplayName = 0x010
		hasFrameName   = 0x080
		monikerAsStr   = 0x100
	)
	flags := le.Uint32(b[28:])
	p := b[32:]
	// A string of the hyperlink: a count of 16-bit units, the terminator
	// among them, and the units.
	str := func() (string, bool) {
		if len(p) < 4 {
			return "", false
		}
		n := int64(le.Uint32(p))
		if n*2 > int64(len(p)-4) {
			return "", false
		}
		s := wide(p[4 : 4+n*2])
		p = p[4+n*2:]
		return s, true
	}
	if flags&hasDisplayName != 0 {
		if _, ok := str(); !ok {
			return
		}
	}
	if flags&hasFrameName != 0 {
		if _, ok := str(); !ok {
			return
		}
	}
	target := ""
	switch {
	case flags&hasMoniker != 0 && flags&monikerAsStr != 0:
		s, ok := str()
		if !ok {
			return
		}
		target = s
	case flags&hasMoniker != 0:
		if len(p) < 16 {
			return
		}
		var class [16]byte
		copy(class[:], p)
		p = p[16:]
		switch class {
		case urlMoniker:
			// The length of what follows in bytes, and the address,
			// which ends at its terminator.
			if len(p) < 4 {
				return
			}
			n := int64(le.Uint32(p))
			if n > int64(len(p)-4) {
				return
			}
			target = wide(p[4 : 4+n])
			p = p[4+n:]
		case fileMoniker:
			// How many folders up, the path in a code page, and after
			// some fixed fields the path in Unicode if it needs it.
			if len(p) < 6 {
				return
			}
			up, n := int(le.Uint16(p)), int64(le.Uint32(p[2:]))
			if n > int64(len(p)-6) {
				return
			}
			ansi := p[6 : 6+n]
			p = p[6+n:]
			if len(p) < 28 {
				return
			}
			size := int64(le.Uint32(p[24:]))
			p = p[28:]
			path := ""
			if size >= 6 && size <= int64(len(p)) {
				path = wide(p[6:size])
				p = p[size:]
			} else {
				for i, ch := range ansi {
					if ch == 0 {
						ansi = ansi[:i]
						break
					}
				}
				path, _ = charset.DecodeCodePage(1252, ansi)
			}
			for ; up > 0 && up < 64; up-- {
				path = `..\` + path
			}
			target = path
		default:
			return // a target of a kind that is not read
		}
	}
	if flags&hasLocation != 0 {
		if loc, ok := str(); ok && loc != "" {
			target += "#" + loc
		}
	}
	href := sh.lost.Address(target)
	if href == "" {
		return
	}
	if n := int64(at.R1-at.R0+1) * int64(at.C1-at.C0+1); n > linkSpread {
		sh.links = append(sh.links, link{at, href})
		return
	}
	if sh.linkAt == nil {
		sh.linkAt = map[pos]string{}
	}
	// A cell given two links has the later: the one a file adds after
	// another is the one it means.
	for r := at.R0; r <= at.R1; r++ {
		for c := at.C0; c <= at.C1; c++ {
			sh.linkAt[pos{r, c}] = href
		}
	}
}

// wide reads UTF-16 text up to its terminator.
func wide(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := le.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}

// hrefOf returns where the cell at p links to, or "".
func (sh *sheet) hrefOf(p pos) string {
	if h, ok := sh.linkAt[p]; ok {
		return h
	}
	for i := len(sh.links) - 1; i >= 0; i-- {
		if l := &sh.links[i]; p.row >= l.at.R0 && p.row <= l.at.R1 && p.col >= l.at.C0 && p.col <= l.at.C1 {
			return l.href
		}
	}
	return ""
}
