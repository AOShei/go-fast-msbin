package xls

import (
	"bytes"
	"context"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/grid"
	"github.com/AOShei/go-fast-msbin/internal/ptg"
	"github.com/AOShei/go-fast-msbin/internal/stored"
	"github.com/AOShei/go-fast-msbin/internal/unit"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// What a workbook holds that is shown on no sheet: its defined names (a
// name can stand for a text that appears nowhere else), the formulas of
// its Excel 4 macro sheets and the code of its VBA project, which are
// programs. They are read as data, never evaluated, and given in the
// document's stored part (gofastcore.PartStored), with no display
// reason.

// unreadFormula is the kind under which formulas not read back are
// counted.
const unreadFormula = "formulas not read back"

// definedName is one name the workbook defines.
type definedName struct {
	name    string // with its sheet in front, for a name local to a sheet
	formula string
}

// The names Excel gives its own defined names, which the file holds as a
// number ([MS-XLS] 2.5.66); an .xlsx file writes them with this prefix.
var builtinNames = [...]string{"Consolidate_Area", "Auto_Open", "Auto_Close", "Extract", "Database", "Criteria", "Print_Area",
	"Print_Titles", "Recorder", "Data_Form", "Auto_Activate", "Auto_Deactivate", "Sheet_Title", "_FilterDatabase"}

// rawName is a defined name as its record gives it, before the formulas
// can be read back: they refer to one another.
type rawName struct {
	name        string
	sheet       int // the sheet it is local to, from 1; 0 for the workbook's
	rgce, extra []byte
}

// linkReader gathers what the formulas of a workbook refer to outside
// themselves while its globals are read.
type linkReader struct {
	sheets []string // of every BoundSheet record, in order
	links  []ptg.Link
	xti    []ptg.XTI
	names  []rawName
	books  int // links to other workbooks so far
	raw    []byte
}

// supBook reads a SupBook record: this workbook, the add-ins, or another
// file and its sheets ([MS-XLS] 2.4.271).
func (l *linkReader) supBook(b *Book, c *cursor) {
	if len(c.b) < 4 {
		return
	}
	if len(l.links) >= warn.MaxExternalLinks {
		b.docLost.Add(warn.CapExternalLinks, 1)
		l.links = append(l.links[:warn.MaxExternalLinks], ptg.Link{})[:warn.MaxExternalLinks]
		return
	}
	tabs, cch := int(le.Uint16(c.b)), le.Uint16(c.b[2:])
	switch cch {
	case 0x0401:
		l.links = append(l.links, ptg.Link{Kind: ptg.LinkSelf})
		return
	case 0x3A01:
		l.links = append(l.links, ptg.Link{Kind: ptg.LinkAddin})
		return
	}
	// Another file: its path, then the names of its sheets. The path of a
	// workbook is encoded, and begins with the mark of that; what does
	// not, and has the mark between an application and a topic, is a DDE
	// or OLE link, and what formulas take through it is not read back. A
	// path of one space is a link that is not used ([MS-XLS] 2.5.277).
	c.b = c.b[2:]
	path, ok := c.str(l.raw[:0])
	l.raw = path
	link := ptg.Link{Kind: ptg.LinkBook}
	switch {
	case string(path) == " ":
		l.links = append(l.links, ptg.Link{})
		return
	case len(path) > 0 && path[0] != 0x01 && bytes.IndexByte(path, 0x03) >= 0:
		link.Kind = ptg.LinkOther
	}
	b.external++
	if link.Kind == ptg.LinkBook {
		l.books++
		link.Book = l.books
		for i := 0; ok && i < tabs; i++ {
			if len(c.b) == 0 && !c.cont() {
				break
			}
			var name []byte
			name, ok = c.str(l.raw[:0])
			l.raw = name
			if len(link.Sheets) >= warn.MaxExternalNames {
				b.docLost.Add(warn.CapExternalSheets, 1)
				continue
			}
			text, _ := clean.String(name, clean.Prose)
			link.Sheets = append(link.Sheets, clean.Label(text))
		}
	}
	l.links = append(l.links, link)
}

// externName reads an ExternName record: a name the formulas take through
// the link before it ([MS-XLS] 2.4.105).
func (l *linkReader) externName(b *Book, c *cursor) {
	if len(l.links) == 0 || len(c.b) < 7 {
		return
	}
	link := &l.links[len(l.links)-1]
	if len(link.Names) >= warn.MaxExternalNames {
		b.docLost.Add(warn.CapExternalNames, 1)
		return
	}
	flags := le.Uint16(c.b)
	const ole = 0x0002 | 0x0004 | 0x0008 | 0x0010 // a DDE or OLE link's item
	n := ptg.ExternName{}
	if flags&ole == 0 && link.Kind != ptg.LinkOther {
		n.Sheet = int32(le.Uint16(c.b[2:]))
		c.b = c.b[6:]
		if name, ok := c.shortStr(l.raw[:0]); ok {
			text, _ := clean.String(name, clean.Prose)
			n.Name = clean.Label(text)
			l.raw = name
		}
		if link.Kind == ptg.LinkAddin {
			n.Sheet = 0
		}
	}
	// A name that is not read still takes its number.
	link.Names = append(link.Names, n)
}

// externSheet reads the ExternSheet record: the table that the 3-D
// references of formulas index, each entry a link and a range of its
// sheets ([MS-XLS] 2.4.104).
func (l *linkReader) externSheet(b *Book, body []byte) {
	if len(body) < 2 {
		return
	}
	n := int(le.Uint16(body))
	for body = body[2:]; n > 0 && len(body) >= 6; n, body = n-1, body[6:] {
		if len(l.xti) >= warn.MaxSheetRefs {
			b.docLost.Add(warn.CapSheetRefs, 1)
			continue
		}
		l.xti = append(l.xti, ptg.XTI{Link: int32(le.Uint16(body)), First: int32(int16(le.Uint16(body[2:]))), Last: int32(int16(le.Uint16(body[4:])))})
	}
}

// name reads a Lbl record: a defined name and its formula ([MS-XLS]
// 2.4.150).
func (l *linkReader) name(b *Book, c *cursor) {
	body := c.b
	if len(body) < 15 {
		l.names = append(l.names, rawName{})
		return
	}
	if len(l.names) >= warn.MaxNames {
		b.lost.Add(warn.CapNames, 1)
		return
	}
	const fBuiltin = 0x0020
	flags, cch, cce, sheet := le.Uint16(body), int(body[3]), int(le.Uint16(body[4:])), int(le.Uint16(body[8:]))
	// The name: a byte of flags and the characters.
	c.b = body[15:]
	raw, ok := c.chars(l.raw[:0], cch, body[14]&1 != 0)
	l.raw = raw
	n := rawName{sheet: sheet}
	if ok {
		if flags&fBuiltin != 0 && len(raw) == 1 && int(raw[0]) < len(builtinNames) {
			n.name = "_xlnm." + builtinNames[raw[0]]
		} else {
			text, _ := clean.String(raw, clean.Prose)
			n.name = b.lost.Name(text)
		}
		rest := c.b
		if cce <= len(rest) {
			n.rgce, n.extra = rest[:cce], rest[cce:]
		}
	}
	// A name that is not read still takes its number.
	l.names = append(l.names, n)
}

// finish makes of what was gathered the workbook's formula context, and
// reads the formulas of its names back.
func (l *linkReader) finish(b *Book) {
	f := &b.formulas
	f.Sheets, f.Links, f.XTI = l.sheets, l.links, l.xti
	f.Names = make([]string, len(l.names))
	for i, n := range l.names {
		f.Names[i] = n.name
	}
	for _, n := range l.names {
		if n.name == "" {
			continue
		}
		if len(n.rgce) == 0 {
			continue // a name with no formula: of a macro's procedure
		}
		text, ok := formulaText(n.rgce, n.extra, f, ptg.Place{})
		if !ok || text == "" {
			b.unreadNames++
			continue
		}
		name := n.name
		// A name local to a sheet has the sheet's name in front.
		if n.sheet >= 1 && n.sheet <= len(l.sheets) {
			name = l.sheets[n.sheet-1] + "!" + name
		}
		b.names = append(b.names, definedName{name: name, formula: text})
	}
}

// readStored reads the stored part: the defined names, the formulas of
// each macro sheet, and the code of the VBA project.
func (b *Book) readStored(ctx context.Context, index int) *unit.Result {
	p := stored.NewPart(ctx, index+1)
	p.Lost.Merge(&b.lost)
	for _, n := range b.names {
		if !p.Add(gofastcore.Block{Kind: gofastcore.KindParagraph, Text: n.name + ": " + n.formula,
			Attrs: map[string]string{gofastcore.AttrStored: gofastcore.StoredDefinedName, gofastcore.AttrName: n.name}}) {
			break
		}
	}
	unread := 0
	for _, ref := range b.refs {
		if p.Full() || ctx.Err() != nil {
			break
		}
		if ref.kind != dtMacro {
			continue
		}
		code, missed := b.macroCode(ref, p.Room().Left())
		unread += missed
		if code == "" {
			continue
		}
		p.Add(gofastcore.Block{Kind: gofastcore.KindCode, Text: code,
			Attrs: map[string]string{gofastcore.AttrStored: gofastcore.StoredMacro, gofastcore.AttrName: ref.name, gofastcore.AttrLanguage: "excel4"}})
	}
	if b.cfg.Stored != nil {
		b.cfg.Stored(ctx, p)
	}
	limited := p.Finish()
	res := &unit.Result{Part: p.Part, Tally: unit.Tally{Damaged: p.Damaged, Limited: limited}}
	if unread > 0 {
		res.Unread = map[string]int{unreadFormula: unread}
	}
	return res
}

// macroCode returns the formulas of a macro sheet, one cell to a line
// ("A1\t=ECHO(FALSE)"), as Excel shows them when it shows formulas, up to
// about room bytes, and how many of its formulas could not be read back.
func (b *Book) macroCode(ref sheetRef, room int64) (string, int) {
	var sb strings.Builder
	sh := &sheet{b: b}
	sh.c.data = b.data
	c := &sh.c
	missed := 0
	if typ, ok := c.record(ref.pos); !ok || typ != recBOF {
		return "", 0
	}
	depth := 0
	for int64(sb.Len()) < room {
		typ, ok := c.record(c.next)
		if !ok {
			break
		}
		switch {
		case typ == recBOF:
			depth++
		case typ == recEOF && depth > 0:
			depth--
		case typ == recEOF:
			return sb.String(), missed
		case depth > 0:
		case typ == recFormula || typ == recFormula3 || typ == recFormula4:
			if len(c.b) < 22 {
				continue
			}
			row, col := int32(le.Uint16(c.b)), int32(le.Uint16(c.b[2:]))
			text, state := sh.recordFormula(c.b, row, col)
			if state == formulaWaits {
				// The formula is shared: it follows.
				text, state = sh.sharedAfter(row, col)
			}
			if state != formulaRead {
				missed++
				continue
			}
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(grid.CellName(row, col))
			sb.WriteString("\t=")
			sb.WriteString(strings.ReplaceAll(text, "\n", " "))
		}
	}
	return sb.String(), missed
}
