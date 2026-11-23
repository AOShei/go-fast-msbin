package xls

import (
	"github.com/AOShei/go-fast-msbin/internal/unit"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/grid"
	"github.com/AOShei/go-fast-msbin/internal/media"
	"github.com/AOShei/go-fast-msbin/internal/officeart"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// What floats over a sheet: text boxes and other shapes with text,
// pictures, charts. Each is a shape of the sheet's drawing; its records
// are an MsoDrawing record with the shape (its properties and the cell
// it is anchored to), an Obj record that says what kind of object it is,
// and then its text (TxO) or its chart (a substream of its own).

// shape is the shape whose records are being read.
type shape struct {
	row, col int32 // the cell it is anchored to
	props    officeart.Props
	kind     uint16 // of its Obj record
	id       uint16
	shown    bool // its picture has been given
}

// The kinds of object an Obj record names ([MS-XLS] 2.5.143).
const (
	objChart   = 0x05
	objPicture = 0x08
	objNote    = 0x19
)

// drawing reads an MsoDrawing record: a piece of the sheet's drawing,
// which holds one shape, or the group around the shapes and its first.
func (sh *sheet) drawing(b []byte) {
	officeart.Walk(b, func(r officeart.Rec, depth int) bool {
		switch r.Type {
		case officeart.Sp:
			// A shape begins: what the one before said is not its.
			sh.shape.props.Reset()
			sh.shape.shown = false
		case officeart.OPT, officeart.SecondaryOPT, officeart.TertiaryOPT:
			sh.shape.props.ReadProps(r.Inst, r.Body)
		case officeart.ClientAnchor:
			// Where the shape is on the sheet: flags, then the column and
			// row of its top left corner, each with an offset into the
			// cell ([MS-XLS] 2.5.193).
			if len(r.Body) >= 8 {
				sh.shape.col = min(int32(le.Uint16(r.Body[2:])), grid.MaxCols-1)
				sh.shape.row = int32(le.Uint16(r.Body[6:]))
			}
		}
		return true
	})
}

// object reads an Obj record: the kind of the object and its number.
func (sh *sheet) object(b []byte) {
	if len(b) < 8 || le.Uint16(b) != 0x0015 {
		return
	}
	sh.shape.kind, sh.shape.id = le.Uint16(b[4:]), le.Uint16(b[6:])
	sh.objNote, sh.objID = sh.shape.kind == objNote, sh.shape.id
	if sh.shape.kind == objNote {
		return
	}
	sh.picture()
	sh.wordArt()
}

// object5 reads an Obj record of Excel 5 or 95, which holds the object
// itself: its kind, its number and the cell its top left corner is in
// ([MS-XLS] of that version: a count, the kind, the number, flags, and
// the anchor).
func (sh *sheet) object5(b []byte) {
	if len(b) < 18 {
		return
	}
	sh.shape.props.Reset()
	sh.shape.kind, sh.shape.id = le.Uint16(b[4:]), le.Uint16(b[6:])
	sh.shape.col = min(int32(le.Uint16(b[10:])), grid.MaxCols-1)
	sh.shape.row = int32(le.Uint16(b[14:]))
	sh.objNote = false
	// A text box: its text is in the record itself, in a layout of that
	// version, and is not read.
	if sh.shape.kind == objText5 {
		sh.unread[unit.UnreadTextBoxes5]++
	}
}

// objText5 is the kind of an object of Excel 5 or 95 that is a text box.
const objText5 = 6

// place adds a block at the cell the current shape is anchored to. It
// reports false where there is no room for it.
func (sh *sheet) place(b gofastcore.Block, cost int) bool {
	if sh.shapesFull {
		return false
	}
	if !sh.room.Take(cost) {
		sh.shapesFull = true
		if sh.g.Cut == "" {
			sh.g.Cut = "the limit on the amount of text was reached"
		}
		return false
	}
	if sh.shape.props.Hidden() {
		b.Display = gofastcore.DisplayHidden
	}
	b.Loc.Range = grid.CellName(sh.shape.row, sh.shape.col)
	sh.floating = append(sh.floating, grid.Placed{Row: sh.shape.row, Col: sh.shape.col, Block: b})
	return true
}

// picture adds the picture the current shape shows, if it shows one.
func (sh *sheet) picture() {
	n, ok := sh.shape.props.Value(officeart.PropBlip)
	if !ok || n == 0 || sh.shape.shown {
		return
	}
	sh.shape.shown = true
	mode := gofastcore.ImagesMeta
	if sh.b.opts != nil {
		mode = sh.b.opts.Images
	}
	if mode == gofastcore.ImagesNone {
		sh.sawImage = true
		return
	}
	alt, removed := clean.String([]byte(sh.shape.props.String(officeart.PropDescription)), clean.Prose)
	sh.removed += removed
	var blip officeart.Rec
	if int64(n) <= int64(len(sh.b.pictures)) {
		blip = sh.b.pictures[n-1].Blip
	}
	img, err := media.Image(int(n), blip, mode, gofastcore.DecodeBudgetFrom(sh.ctx))
	if err != nil {
		sh.budget = true
	}
	sh.place(gofastcore.Block{Kind: gofastcore.KindImage, Text: alt, Image: img}, gofastcore.BlockCost+len(alt))
}

// wordArt adds the text of a shape that draws its text as a picture of
// letters, which the shape keeps as a property.
func (sh *sheet) wordArt() {
	text, removed := clean.String([]byte(sh.shape.props.String(officeart.PropUnicodeText)), clean.Prose)
	sh.removed += removed
	if text == "" {
		return
	}
	sh.paragraphs(text)
}

// paragraphs adds text of the current shape, a block for each of its
// paragraphs that has text.
func (sh *sheet) paragraphs(text string) {
	for _, para := range strings.Split(text, "\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		if !sh.place(gofastcore.Block{Kind: gofastcore.KindParagraph, Text: para}, gofastcore.BlockCost+len(para)) {
			return
		}
	}
}

// shapeText reads the text of a shape that is not a note (the TxO record
// and the CONTINUE records after it).
func (sh *sheet) shapeText(b []byte) {
	if len(b) < 14 {
		return
	}
	cch := int(le.Uint16(b[10:]))
	c := &sh.c
	if cch == 0 || !c.cont() || len(c.b) == 0 {
		return
	}
	if len(sh.floating) >= warn.MaxShapes {
		sh.lost.Add(warn.CapShapes, 1)
		return
	}
	wide := c.b[0]&1 != 0
	c.b = c.b[1:]
	raw, _ := c.chars(sh.raw[:0], cch, wide)
	sh.raw = raw
	text, removed := clean.String(raw, clean.Prose)
	sh.removed += removed
	sh.paragraphs(text)
}
