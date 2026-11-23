package gofastmsbin_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	gofastmsbin "github.com/AOShei/go-fast-msbin"
	t "github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// The persist numbers and the numbers of the objects of a test
// presentation.
const (
	pptDoc    = 1
	pptMaster = 2
	masterID  = 0x80000000
)

// pptDeck is a presentation for a test: what its document holds besides
// its lists, its master's records, and its slides.
type pptDeck struct {
	doc     [][]byte          // records of the document, after its atom
	master  [][]byte          // records of the master: styles and its drawing
	slides  []pptSlide        // in order
	objects map[uint32][]byte // other objects, by persist number
	omit    bool              // title slides show no date, footer or number
}

// pptSlide is a slide of a test presentation.
type pptSlide struct {
	outline []byte   // the texts the outline keeps for it
	records [][]byte // the slide's records after its atom
	shapes  [][]byte
	layout  uint32 // 0 for a title slide
	notes   []byte // the drawing of its notes page, or nil
	alone   bool   // it does not show its master's shapes
}

// file writes the presentation.
func (d pptDeck) file() *t.PPTFile {
	var f t.PPTFile
	f.Edit(pptDoc, d.all())
	return &f
}

// all gives the objects of the presentation by their persist numbers:
// the document, the master, and for the slide with index i the slide
// under 10+2i and its notes page after it.
func (d pptDeck) all() map[uint32][]byte {
	objects := map[uint32][]byte{}
	for id, o := range d.objects {
		objects[id] = o
	}
	var slides, notes [][]byte
	for i, s := range d.slides {
		persist, id := uint32(10+2*i), uint32(256+i)
		slides = append(slides, t.PPTSlidePersist(persist, id), s.outline)
		notesID := uint32(0)
		if s.notes != nil {
			notesID = 512 + uint32(i)
			notes = append(notes, t.PPTSlidePersist(persist+1, notesID))
			objects[persist+1] = t.PPTContainer(t.PPTNotes, 0, t.PPTAtom(0x03F1, 0, t.U32(id, 0)...), s.notes)
		}
		layout := s.layout
		if i > 0 && layout == 0 {
			layout = 1
		}
		if i == 0 && s.layout != 0 {
			layout = s.layout
		}
		objects[persist] = t.PPTContainer(t.PPTSlide, 0, append([][]byte{t.PPTSlideAtom(layout, masterID, notesID, !s.alone)}, append(s.records, t.PPTShapes(s.shapes...))...)...)
	}
	doc := [][]byte{t.PPTDocumentAtom(5760, 4320, 1, d.omit)}
	doc = append(doc, d.doc...)
	doc = append(doc, t.PPTContainer(t.PPTSlideList, 1, t.PPTSlidePersist(pptMaster, masterID)))
	doc = append(doc, t.PPTContainer(t.PPTSlideList, 0, slides...))
	if notes != nil {
		doc = append(doc, t.PPTContainer(t.PPTSlideList, 2, notes...))
	}
	objects[pptDoc] = t.PPTContainer(t.PPTDocument, 0, doc...)
	master := d.master
	if master == nil {
		master = [][]byte{t.PPTShapes()}
	}
	objects[pptMaster] = t.PPTContainer(t.PPTMainMaster, 0, append([][]byte{t.PPTSlideAtom(1, 0, 0, false)}, master...)...)
	return objects
}

func (d pptDeck) bytes() []byte { return t.Compound(d.file().Streams()) }

// slideLines gives the blocks of a slide in a line each: kind, level and
// marker, what is declared of its display, and its text.
func slideLines(part *gofastcore.Part) []string {
	var out []string
	for _, b := range part.Blocks {
		line := fmt.Sprint(b.Kind)
		if b.Level > 0 {
			line += fmt.Sprint(b.Level)
		}
		if b.Marker != "" {
			line += "[" + b.Marker + "]"
		}
		if b.Display != 0 {
			line += " " + b.Display.String()
		}
		switch {
		case b.Table != nil:
			var rows []string
			for _, r := range b.Table.Rows {
				rows = append(rows, strings.Join(r, "|"))
			}
			line += " " + b.Attrs[gofastcore.AttrChart] + " " + strings.Join(rows, "/")
		case b.Image != nil:
			line += fmt.Sprintf(" %s %dx%d %q", b.Image.MIME, b.Image.Width, b.Image.Height, b.Text)
		default:
			line += " " + b.Text
		}
		for _, k := range []string{gofastcore.AttrNoteType, gofastcore.AttrAuthor, gofastcore.AttrDate} {
			if v := b.Attrs[k]; v != "" {
				line += " " + k + "=" + v
			}
		}
		out = append(out, line)
	}
	return out
}

func wantLines(tt *testing.T, what string, got, want []string) {
	tt.Helper()
	if !reflect.DeepEqual(got, want) {
		tt.Errorf("%s:\n got  %q\n want %q", what, got, want)
	}
}

// A slide's title and body are texts of the outline; its text boxes hold
// their own. The body's paragraphs are list items with the bullets the
// master gives their level, unless they say otherwise.
func TestSlideText(tt *testing.T) {
	body := "First point\rA detail\rAnother\rNo bullet here\rOwn bullet"
	deck := pptDeck{
		doc: [][]byte{t.PPTContainer(t.PPTEnvironment, 0, t.PPTContainer(t.PPTFonts, 0, t.PPTFont(0, "Arial"), t.PPTFont(1, "Wingdings")))},
		master: [][]byte{
			// The body: a square from Wingdings at the first level, a dash
			// at the second.
			t.PPTMasterStyle(1, t.PPTParaRun{Bullet: 1, Char: 'n', Font: 1}, t.PPTParaRun{Bullet: 1, Char: '\u2013', Font: 0}),
			t.PPTShapes(),
		},
		slides: []pptSlide{{
			outline: t.Cat(
				t.PPTText(0, "The title\x0bon two lines"),
				t.PPTText(1, body, t.PPTStyle([]t.PPTParaRun{
					{Count: 12, Font: -1}, {Count: 9, Level: 1, Font: -1}, {Count: 8, Font: -1},
					{Count: 15, Bullet: -1, Font: -1}, {Count: 11, Bullet: 1, Char: '*', Font: 0},
				}, nil)),
			),
			shapes: [][]byte{
				t.PPTShape{Top: 1000, Left: 400, Right: 5000, Bottom: 3000, Placeholder: 14, OutlineRef: 2}.Bytes(),
				t.PPTShape{Top: 3200, Left: 400, Right: 2000, Bottom: 3600, Text: t.PPTTextBytes(4, "A box\rof two paragraphs")}.Bytes(),
				t.PPTShape{Top: 200, Left: 400, Right: 5000, Bottom: 800, Placeholder: 13, OutlineRef: 1}.Bytes(),
			},
			layout: 1,
		}, {
			// A title slide: a centred title and a subtitle.
			outline: t.Cat(t.PPTText(6, "Second"), t.PPTText(5, "A subtitle")),
			shapes: [][]byte{
				t.PPTShape{Top: 100, Left: 100, Right: 900, Bottom: 300, Placeholder: 15, OutlineRef: 1}.Bytes(),
				t.PPTShape{Top: 400, Left: 100, Right: 900, Bottom: 600, Placeholder: 16, OutlineRef: 2}.Bytes(),
			},
		}},
	}
	doc := mustExtract(tt, deck.bytes(), nil)
	if doc.Source.Format != "ppt" || len(doc.Parts) != 2 {
		tt.Fatalf("%s, %d parts", doc.Source.Format, len(doc.Parts))
	}
	p := &doc.Parts[0]
	if p.Kind != gofastcore.PartSlide || p.Number != 1 || p.Name != "The title on two lines" || p.Width != 720 || p.Height != 540 {
		tt.Errorf("part: %s %d %q %vx%v", p.Kind, p.Number, p.Name, p.Width, p.Height)
	}
	wantLines(tt, "slide 1", slideLines(p), []string{
		"heading1 The title\non two lines",
		"list_item1[\u25fc] First point",
		"list_item2[\u2013] A detail",
		"list_item1[\u25fc] Another",
		"paragraph No bullet here",
		"list_item1[*] Own bullet",
		"paragraph A box",
		"paragraph of two paragraphs",
	})
	if got := p.Blocks[1].Loc.BBox; got != (gofastcore.Rect{X0: 50, Y0: 125, X1: 625, Y1: 375}) {
		tt.Errorf("the body's place: %+v", got)
	}
	wantLines(tt, "slide 2", slideLines(&doc.Parts[1]), []string{"heading1 Second", "heading2 A subtitle"})
}

// Notes are the texts of the slide's notes page; a slide and a shape may
// be hidden; a comment has its author and its date.
func TestNotesHiddenAndComments(tt *testing.T) {
	deck := pptDeck{slides: []pptSlide{{
		outline: t.PPTText(0, "Shown"),
		shapes: [][]byte{
			t.PPTShape{Top: 100, Left: 100, Right: 900, Bottom: 300, Placeholder: 13, OutlineRef: 1}.Bytes(),
			t.PPTShape{Top: 400, Left: 100, Right: 900, Bottom: 600, Text: t.PPTText(4, "Not shown"),
				Props: []t.ArtProp{{ID: 0x03BF, Value: 0x00020002}}}.Bytes(),
		},
		records: [][]byte{t.PPTCommentOn("Ada", "Check this figure", 2004, 3, 9, 14, 30)},
		notes: t.PPTShapes(
			t.PPTShape{Top: 100, Left: 100, Right: 900, Bottom: 300, Placeholder: 11}.Bytes(),
			t.PPTShape{Top: 400, Left: 100, Right: 900, Bottom: 900, Placeholder: 12, Text: t.PPTText(2, "Say this first.\rThen this.")}.Bytes(),
		),
		layout: 1,
	}, {
		outline: t.PPTText(0, "Skipped in the show"),
		shapes:  [][]byte{t.PPTShape{Top: 100, Left: 100, Right: 900, Bottom: 300, Placeholder: 13, OutlineRef: 1}.Bytes()},
		records: [][]byte{t.PPTHidden()},
	}}}
	doc := mustExtract(tt, deck.bytes(), nil)
	wantLines(tt, "slide 1", slideLines(&doc.Parts[0]), []string{
		"heading1 Shown",
		"paragraph hidden Not shown",
		"note Say this first. type=speaker",
		"note Then this. type=speaker",
		"note Check this figure type=comment author=Ada date=2004-03-09T14:30:00",
	})
	if doc.Parts[0].Display != 0 || doc.Parts[1].Display != gofastcore.DisplayHidden {
		tt.Errorf("display: %v, %v", doc.Parts[0].Display, doc.Parts[1].Display)
	}
}

// A character of a symbol font is the character a reader sees; a field
// is what the slide puts in it; a link is a span of the text.
func TestSymbolsFieldsAndLinks(tt *testing.T) {
	text := "Go \uf0e0 there, slide * of the deck"
	deck := pptDeck{
		doc: [][]byte{
			t.PPTContainer(t.PPTEnvironment, 0, t.PPTContainer(t.PPTFonts, 0, t.PPTFont(0, "Arial"), t.PPTFont(1, "Wingdings"))),
			t.PPTContainer(t.PPTExObjList, 0, t.PPTHyperlink(7, "https://example.com/there")),
		},
		slides: []pptSlide{{}, {
			shapes: [][]byte{t.PPTShape{Top: 100, Left: 100, Right: 900, Bottom: 300, Text: t.PPTText(4, text,
				t.PPTStyle([]t.PPTParaRun{{Count: len([]rune(text)) + 1, Font: -1}},
					[]t.PPTCharRun{{Count: 3, Font: 0, Sym: -1}, {Count: 1, Font: 1, Sym: -1}, {Count: len([]rune(text)) - 3, Font: 0, Sym: -1}}),
				t.PPTField(0x0FD8, 18),
				t.PPTLink(7, 4, 5, 10),
				t.PPTLink(0, 2, 0, 2))}.Bytes()},
		}},
	}
	doc := mustExtract(tt, deck.bytes(), &gofastcore.Options{Spans: true})
	b := doc.Parts[1].Blocks[0]
	if b.Text != "Go \U0001f86a there, slide 2 of the deck" {
		tt.Errorf("text: %q", b.Text)
	}
	if len(b.Spans) != 1 || b.Spans[0].Href != "https://example.com/there" || b.Text[b.Spans[0].Start:b.Spans[0].End] != "there" {
		tt.Errorf("spans: %+v", b.Spans)
	}
	if w, ok := warning(doc.Warnings, gofastcore.WarnInputActiveContent); !ok || !strings.HasPrefix(w.Message, "1 action") {
		tt.Errorf("warnings: %+v", doc.Warnings)
	}
}

// What a master holds of its own is on every slide that follows it, at
// its head or its foot; the date, the footer and the number of the slide
// are shown where the presentation says, in the master's words.
func TestMasterTextAndFooters(tt *testing.T) {
	group, pictures := t.PPTPictures(t.PNG(4, 3))
	deck := pptDeck{
		doc: [][]byte{
			group,
			t.PPTContainer(t.PPTHeadFoot, 3, t.PPTHeadFootAtom(0x2D), t.PPTCString(0, "March 2004"), t.PPTCString(2, "Confidential")),
		},
		master: [][]byte{t.PPTShapes(
			t.PPTShape{Top: 100, Left: 100, Right: 2000, Bottom: 300, Text: t.PPTText(4, "The Company")}.Bytes(),
			t.PPTShape{Top: 100, Left: 100, Right: 2000, Bottom: 300, Text: t.PPTText(4, "The Company")}.Bytes(), // twice in one place
			t.PPTShape{Top: 150, Left: 5000, Right: 5600, Bottom: 450, Props: []t.ArtProp{{ID: 0x0104, Value: 1}}}.Bytes(),
			t.PPTShape{Top: 4000, Left: 100, Right: 1000, Bottom: 4200, Placeholder: 7, Text: t.PPTText(4, "*", t.PPTField(0x0FF8, 0))}.Bytes(),
			t.PPTShape{Top: 4000, Left: 2000, Right: 3500, Bottom: 4200, Placeholder: 9, Text: t.PPTText(4, "*", t.PPTField(0x0FFA, 0))}.Bytes(),
			t.PPTShape{Top: 4000, Left: 5000, Right: 5600, Bottom: 4200, Placeholder: 8, Text: t.PPTText(4, "Page *", t.PPTField(0x0FD8, 5))}.Bytes(),
			t.PPTShape{Top: 200, Left: 400, Right: 5000, Bottom: 800, Placeholder: 1, Text: t.PPTText(0, "Click to edit the title")}.Bytes(),
		)},
		omit: true,
		slides: []pptSlide{
			{outline: t.PPTText(6, "Opening"), shapes: [][]byte{t.PPTShape{Top: 100, Left: 100, Right: 900, Bottom: 300, Placeholder: 15, OutlineRef: 1}.Bytes()}},
			{outline: t.PPTText(0, "Figures"), shapes: [][]byte{t.PPTShape{Top: 100, Left: 100, Right: 900, Bottom: 300, Placeholder: 13, OutlineRef: 1}.Bytes()}},
			{outline: t.PPTText(0, "Alone"), alone: true, shapes: [][]byte{t.PPTShape{Top: 100, Left: 100, Right: 900, Bottom: 300, Placeholder: 13, OutlineRef: 1}.Bytes()},
				// The slide says for itself what it shows: its number only.
				records: [][]byte{t.PPTContainer(t.PPTHeadFoot, 0, t.PPTHeadFootAtom(0x08))}},
		},
	}
	streams := deck.file().Streams()
	streams["Pictures"] = pictures
	doc := mustExtract(tt, t.Compound(streams), nil)
	wantLines(tt, "the title slide", slideLines(&doc.Parts[0]), []string{
		"heading1 Opening", "header The Company", `image image/png 4x3 ""`,
	})
	wantLines(tt, "a slide", slideLines(&doc.Parts[1]), []string{
		"heading1 Figures", "header The Company", `image image/png 4x3 ""`, "footer March 2004", "footer Confidential", "footer Page 2",
	})
	wantLines(tt, "a slide that does not follow its master", slideLines(&doc.Parts[2]), []string{"heading1 Alone", "footer Page 3"})
	// Without pictures the picture is not given.
	doc = mustExtract(tt, t.Compound(streams), &gofastcore.Options{Images: gofastcore.ImagesNone})
	wantLines(tt, "no pictures", slideLines(&doc.Parts[0]), []string{"heading1 Opening", "header The Company"})
}

// Numbered paragraphs have their numbers, which the extension of
// PowerPoint 2000 keeps: for a text of the outline with the document,
// for a shape's own text with the shape.
func TestNumberedParagraphs(tt *testing.T) {
	body := "One\rTwo\rAside\rThree"
	deck := pptDeck{
		doc: [][]byte{t.PPTContainer(t.PPTList, 0, t.PPTTags("___PPT9",
			t.PPTOutlineNumbers(256, 1, t.PPTAutoNumbers([3]int{1, -1, 0}, [3]int{0, -1, 0}))))},
		slides: []pptSlide{{
			outline: t.PPTText(1, body, t.PPTStyle([]t.PPTParaRun{{Count: 20, Font: -1}},
				[]t.PPTCharRun{{Count: 8, Font: -1, Sym: -1}, {Count: 6, Font: -1, Sym: -1, Ext: 1}, {Count: 6, Font: -1, Sym: -1}})),
			shapes: [][]byte{
				t.PPTShape{Top: 100, Left: 100, Right: 900, Bottom: 300, Placeholder: 14, OutlineRef: 1}.Bytes(),
				t.PPTShape{Top: 400, Left: 100, Right: 900, Bottom: 600, Text: t.PPTText(4, "alpha\rbeta"),
					Data: t.PPTTags("___PPT9", t.PPTAutoNumbers([3]int{1, 9, 3}))}.Bytes(),
			},
			layout: 1,
		}},
	}
	doc := mustExtract(tt, deck.bytes(), nil)
	wantLines(tt, "numbers", slideLines(&doc.Parts[0]), []string{
		"list_item1[1.] One", "list_item1[2.] Two", "list_item1[\u2022] Aside", "list_item1[1.] Three",
		"list_item1[c)] alpha", "list_item1[d)] beta",
	})
	if !doc.Parts[0].Blocks[0].Ordered || doc.Parts[0].Blocks[2].Ordered {
		tt.Errorf("ordered: %v %v", doc.Parts[0].Blocks[0].Ordered, doc.Parts[0].Blocks[2].Ordered)
	}
}

// A table is a group of shapes that says it is one: its cells are placed
// by where they are. A group's shapes say their places in the group's
// own coordinates.
func TestTablesAndGroups(tt *testing.T) {
	cell := func(x, y int32, text string) t.PPTShape {
		return t.PPTShape{Child: &[4]int32{x, y, x + 100, y + 50}, Text: t.PPTText(4, text)}
	}
	deck := pptDeck{slides: []pptSlide{{
		shapes: [][]byte{
			t.PPTGroup(
				t.PPTShape{Group: &[4]int32{0, 0, 200, 100}, Top: 800, Left: 800, Right: 2400, Bottom: 1600,
					Props: []t.ArtProp{{ID: 0x039F, Value: 1}}},
				cell(100, 50, "d"), cell(0, 0, "a"), cell(100, 0, "b"), cell(0, 50, "c"),
			),
			t.PPTGroup(
				t.PPTShape{Group: &[4]int32{0, 0, 1000, 1000}, Top: 2400, Left: 800, Right: 3200, Bottom: 4000},
				t.PPTShape{Child: &[4]int32{500, 500, 1000, 1000}, Text: t.PPTText(4, "in a group")},
			),
		},
		layout: 1,
	}}}
	doc := mustExtract(tt, deck.bytes(), nil)
	wantLines(tt, "table", slideLines(&doc.Parts[0]), []string{"table  a|b/c|d", "paragraph in a group"})
	if got := doc.Parts[0].Blocks[1].Loc.BBox; got != (gofastcore.Rect{X0: 250, Y0: 400, X1: 400, Y1: 500}) {
		tt.Errorf("the grouped shape's place: %+v", got)
	}
}

// The presentation is what its last edit left: an object a later edit
// wrote takes the place of the one before it.
func TestLaterEditsWin(tt *testing.T) {
	deck := pptDeck{slides: []pptSlide{{
		shapes: [][]byte{t.PPTShape{Top: 100, Left: 100, Right: 900, Bottom: 300, Text: t.PPTText(4, "as first saved")}.Bytes()},
		layout: 1,
	}}}
	f := deck.file()
	f.Edit(pptDoc, map[uint32][]byte{10: t.PPTContainer(t.PPTSlide, 0, t.PPTSlideAtom(1, masterID, 0, true),
		t.PPTShapes(t.PPTShape{Top: 100, Left: 100, Right: 900, Bottom: 300, Text: t.PPTText(4, "as saved again")}.Bytes()))})
	doc := mustExtract(tt, t.Compound(f.Streams()), nil)
	wantLines(tt, "after two edits", slideLines(&doc.Parts[0]), []string{"paragraph as saved again"})

	// A file whose Current User stream does not lead to the last edit is
	// read from the last edit its stream holds, and says so.
	streams := f.Streams()
	streams["Current User"] = []byte("someone")
	doc = mustExtract(tt, t.Compound(streams), nil)
	wantLines(tt, "no current user", slideLines(&doc.Parts[0]), []string{"paragraph as saved again"})
	if _, ok := warning(doc.Warnings, gofastcore.WarnInputRepaired); !ok {
		tt.Errorf("warnings: %+v", doc.Warnings)
	}

	// A presentation saved for PowerPoint 95 and 97 both keeps the one
	// that is read in a storage of its own.
	dual := map[string][]byte{"PowerPoint Document": {1, 2, 3}, "Text_Content": {1}, "PersistentStorage Directory": {1}}
	for name, data := range f.Streams() {
		dual["PP97_DUALSTORAGE/"+name] = data
	}
	doc = mustExtract(tt, t.Compound(dual), nil)
	wantLines(tt, "dual storage", slideLines(&doc.Parts[0]), []string{"paragraph as saved again"})
}

// What a presentation holds that is not read is said, with how many:
// the sounds it keeps, and the videos and the sounds it plays from
// outside.
func TestPresentationUnreadContent(tt *testing.T) {
	sound := func() []byte { return t.PPTContainer(0x07E6, 0, t.PPTAtom(0x07E7, 0, make([]byte, 8)...)) }
	deck := pptDeck{
		doc: [][]byte{
			t.PPTContainer(0x07E4, 5, t.PPTAtom(0x07E5, 0, t.U32(2)...), sound(), sound()),
			t.PPTContainer(t.PPTExObjList, 0,
				t.PPTContainer(0x1006, 0, t.PPTContainer(0x1005, 0, t.PPTAtom(0x1004, 0, make([]byte, 8)...))),
				t.PPTContainer(0x1010, 0, t.PPTAtom(0x1004, 0, make([]byte, 8)...))),
		},
		slides: []pptSlide{{outline: t.PPTText(0, "A slide"), layout: 1,
			shapes: [][]byte{t.PPTShape{Top: 100, Left: 100, Right: 900, Bottom: 300, Placeholder: 13, OutlineRef: 1}.Bytes()}}},
	}
	doc := mustExtract(tt, deck.bytes(), nil)
	if w, ok := warning(doc.Warnings, gofastcore.WarnInputUnreadContent); !ok || w.Message != "1 video, 3 sounds" || w.Severity != gofastcore.SeverityWarning {
		tt.Errorf("warnings: %+v", doc.Warnings)
	}
	// A presentation with none says nothing of them.
	deck.doc = nil
	if _, ok := warning(mustExtract(tt, deck.bytes(), nil).Warnings, gofastcore.WarnInputUnreadContent); ok {
		tt.Error("a presentation with no sound or video says it has some")
	}
}

// A title of very many paragraphs is one heading, and joining them costs
// no more than their text: not a copy of the heading for every paragraph.
func TestPresentationLongTitle(tt *testing.T) {
	const n = 60000
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line " + strconv.Itoa(i) + " of a title, some forty bytes long"
	}
	deck := pptDeck{slides: []pptSlide{{
		outline: t.PPTText(0, strings.Join(lines, "\r")),
		shapes:  [][]byte{t.PPTShape{Top: 100, Left: 100, Right: 900, Bottom: 300, Placeholder: 13, OutlineRef: 1}.Bytes()},
		layout:  1,
	}}}
	data := deck.bytes()
	start := time.Now()
	doc := mustExtract(tt, data, nil)
	took := time.Since(start)
	b := doc.Parts[0].Blocks
	if len(b) != 1 || b[0].Kind != gofastcore.KindHeading || b[0].Text != strings.Join(lines, "\n") {
		tt.Fatalf("%d blocks; the first has %d bytes", len(b), len(b[0].Text))
	}
	limit := 2 * time.Second
	if underRace {
		limit = time.Minute
	}
	if took > limit {
		tt.Errorf("a title of %d paragraphs took %v", n, took)
	}
}

// An equation of the Equation Editor is read with the slide that shows
// it, in the linear form, where its picture stands; it is no embedded
// file then. One that cannot be read stays a picture and a file.
func TestPresentationEquations(tt *testing.T) {
	eq := t.MTEF3(t.EqLine(t.EqString(t.EqVariable, "E"), t.EqString(t.EqSymbol, "="), t.EqString(t.EqVariable, "mc"),
		t.EqTmpl(15, 0, t.EqNull(), t.EqLine(t.EqString(t.EqNumber, "2")))))
	strange := t.MTEF3(t.EqLine(t.EqString(t.EqVariable, "a"), t.EqChar(t.EqSymbol, 0xE123)))
	object := func(mtef []byte) []byte {
		return t.Compound(map[string][]byte{"Equation Native": t.EquationNative(mtef), "\x01CompObj": make([]byte, 40)})
	}
	group, _ := t.PPTPictures(t.PNG(4, 3))
	picture := []t.ArtProp{{ID: 0x0104, Value: 1}}
	deck := pptDeck{
		doc: [][]byte{
			group,
			t.PPTContainer(t.PPTExObjList, 0, t.PPTExObj(1, 6, 40), t.PPTExObj(2, 6, 41), t.PPTExObj(3, 6, 42)),
		},
		objects: map[uint32][]byte{40: t.PPTStorage(object(eq), true), 41: t.PPTStorage(object(eq[:len(eq)-4]), false), 42: t.PPTStorage(object(strange), true)},
		slides: []pptSlide{{
			shapes: [][]byte{
				t.PPTShape{Top: 800, Left: 800, Right: 4000, Bottom: 1600, Data: t.PPTExObjRef(1), Props: picture}.Bytes(),
				t.PPTShape{Top: 2400, Left: 800, Right: 4000, Bottom: 3200, Data: t.PPTExObjRef(2), Props: picture}.Bytes(),
				t.PPTShape{Top: 3300, Left: 800, Right: 4000, Bottom: 4000, Data: t.PPTExObjRef(3), Props: picture}.Bytes(),
			},
			layout: 1,
		}},
	}
	var handed []string
	opts := &gofastcore.Options{Child: func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
		handed = append(handed, name)
		return nil, gofastcore.ErrUnsupported
	}}
	doc := mustExtract(tt, deck.bytes(), opts)
	wantLines(tt, "equations", slideLines(&doc.Parts[0]), []string{"formula E=mc^2", `image  0x0 ""`, "formula a\uFFFD"})
	b := doc.Parts[0].Blocks[0]
	if b.Attrs[gofastcore.AttrNotation] != gofastcore.NotationUnicodeMath || b.Loc.BBox != (gofastcore.Rect{X0: 100, Y0: 100, X1: 500, Y1: 200}) {
		tt.Errorf("the equation: %+v, at %+v", b.Attrs, b.Loc.BBox)
	}
	if len(doc.Children) != 1 || len(handed) != 1 || handed[0] != "object41" {
		tt.Errorf("embedded files: %d, handed on %v", len(doc.Children), handed)
	}
	if w, ok := warning(doc.Parts[0].Warnings, gofastcore.WarnTextUnmappedGlyphs); !ok || !strings.HasPrefix(w.Message, "1 characters of equations") {
		tt.Errorf("warnings: %+v", doc.Parts[0].Warnings)
	}
	// Within a limit on what may be expanded, whatever is left of it:
	// a valid document, the equations over it not read, and said.
	for _, limit := range []int64{1, 100, 1500, 1600, 3000, 3200, 5000} {
		for _, o := range []*gofastcore.Options{{}, {Child: opts.Child}} {
			o.Limits.MaxDecodedBytes = limit
			tight, err := extract(deck.bytes(), o)
			checkOutcome(tt, tight, err)
			if tight == nil {
				continue
			}
			_, onSlide := warning(tight.Parts[0].Warnings, gofastcore.WarnLimitExceeded)
			_, onDoc := warning(tight.Warnings, gofastcore.WarnLimitExceeded)
			if limit < 1500 && !onSlide && !onDoc {
				tt.Errorf("a limit of %d bytes expanded: nothing says it was met", limit)
			}
		}
	}
	// A limit on the text holds for equations as for any text.
	small := mustExtract(tt, deck.bytes(), &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: gofastcore.BlockCost + 3}})
	if n := len(small.Parts[0].Blocks); n != 0 {
		tt.Errorf("%d blocks within a limit below the first equation", n)
	}
	if _, ok := warning(small.Parts[0].Warnings, gofastcore.WarnLimitExceeded); !ok {
		tt.Errorf("warnings: %+v", small.Parts[0].Warnings)
	}
}

// A Microsoft Graph chart is read with the slide that shows it, from its
// datasheet; other embedded objects are documents of their own; the
// modules of the VBA project are in the stored part.
func TestPresentationObjects(tt *testing.T) {
	label := func(row, col uint16, text string) []byte {
		return t.Rec(0x0004, t.Cat(t.U16(row, col), []byte{0, 0, 0, byte(len(text)), 0}, []byte(text))...)
	}
	number := func(row, col uint16, v float64) []byte {
		return t.Rec(0x0003, t.Cat(t.U16(row, col), []byte{0, 0, 0}, t.F64(v))...)
	}
	graph := t.BookFile(t.Workbook(nil, t.Sheet{Name: "Graph", Kind: 2, Records: [][]byte{
		label(0, 1, "2001"), label(0, 2, "2002"),
		label(1, 0, "East"), number(1, 1, 10), number(1, 2, 12.5),
		label(2, 0, "West"), number(2, 1, 7), number(2, 2, 8),
		t.Rec(0x1002, make([]byte, 16)...), t.Rec(0x1033),
		t.ChartType(0x1017, t.U16(0, 150, 0), false),
		t.Rec(0x1034),
	}}))
	book := t.Compound(map[string][]byte{"Workbook": t.Cat(t.BOF(0x0005), t.EOF())})
	project := t.Compound(t.VBAStreams("", 1252, t.VBAModule{Name: "Module1", Code: "Sub Hello()\r\nEnd Sub\r\n"}))
	deck := pptDeck{
		doc: [][]byte{
			t.PPTContainer(t.PPTExObjList, 0, t.PPTExObj(1, 4, 40), t.PPTExObj(2, 3, 41)),
			t.PPTVbaInfo(42),
		},
		objects: map[uint32][]byte{40: t.PPTStorage(graph, true), 41: t.PPTStorage(book, false), 42: t.PPTStorage(project, true)},
		slides: []pptSlide{{
			shapes: [][]byte{
				t.PPTShape{Top: 800, Left: 800, Right: 4000, Bottom: 3200, Data: t.PPTExObjRef(1)}.Bytes(),
				t.PPTShape{Top: 3300, Left: 800, Right: 4000, Bottom: 4000, Data: t.PPTExObjRef(2)}.Bytes(),
			},
			layout: 1,
		}},
	}
	doc := mustExtract(tt, deck.bytes(), nil)
	if len(doc.Parts) != 2 || doc.Parts[1].Kind != gofastcore.PartStored {
		tt.Fatalf("%d parts", len(doc.Parts))
	}
	wantLines(tt, "chart", slideLines(&doc.Parts[0]), []string{"table bar |East|West/2001|10|7/2002|12.5|8"})
	if got := doc.Parts[0].Blocks[0].Loc.BBox; got != (gofastcore.Rect{X0: 100, Y0: 100, X1: 500, Y1: 400}) {
		tt.Errorf("the chart's place: %+v", got)
	}
	if got := doc.Parts[1].Blocks; len(got) == 0 || !strings.Contains(got[len(got)-1].Text, "Sub Hello()") {
		tt.Errorf("the stored part: %+v", got)
	}
	if _, ok := warning(doc.Warnings, gofastcore.WarnInputActiveContent); !ok {
		tt.Errorf("warnings: %+v", doc.Warnings)
	}
	if len(doc.Children) != 1 || doc.Children[0].Source.Name != "object41.xls" || doc.Children[0].Source.MIME != "application/vnd.ms-excel" {
		tt.Fatalf("children: %+v", doc.Children)
	}
}

// What a presentation may hold is bounded, and a slide the limit cuts
// says so; a presentation with nothing in it that can be found has one
// part that says so.
func TestPresentationLimitsAndDamage(tt *testing.T) {
	var slides []pptSlide
	for i := 0; i < 40; i++ {
		slides = append(slides, pptSlide{layout: 1, shapes: [][]byte{
			t.PPTShape{Top: 100, Left: 100, Right: 900, Bottom: 300, Text: t.PPTText(4, strings.Repeat("words and more words ", 20))}.Bytes()}})
	}
	data := pptDeck{slides: slides}.bytes()
	e := gofastmsbin.Extractor{}
	doc, err := extract(data, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 4000}})
	if err != nil {
		tt.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		tt.Fatal(err)
	}
	cut := false
	for _, p := range doc.Parts {
		if _, ok := warning(p.Warnings, gofastcore.WarnLimitExceeded); ok {
			cut = true
		}
	}
	if !cut {
		tt.Errorf("no slide says the limit was reached: %+v", doc.Warnings)
	}
	size := 0
	for _, p := range doc.Parts {
		for _, b := range p.Blocks {
			size += len(b.Text)
		}
	}
	if size > 4000 {
		tt.Errorf("%d bytes of text within a limit of 4000", size)
	}
	// A cancelled context ends the reading.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Extract(ctx, bytes.NewReader(data), int64(len(data)), nil); err == nil {
		tt.Error("no error for a cancelled context")
	}
	// The stream cut in the middle of the document: nothing is found.
	streams := pptDeck{slides: slides[:1]}.file().Streams()
	streams["PowerPoint Document"] = streams["PowerPoint Document"][:40]
	doc = mustExtract(tt, t.Compound(streams), nil)
	if _, ok := warning(doc.Parts[0].Warnings, gofastcore.WarnPartFailed); !ok || len(doc.Parts) != 1 {
		tt.Errorf("parts: %+v", doc.Parts)
	}
}

// richDeck is a presentation with something of most of what is read, for
// the fuzzer to start from.
func richDeck() pptDeck {
	group, _ := t.PPTPictures(t.PNG(4, 3))
	body := "One\rTwo \uf0e0 there\rThree *"
	return pptDeck{
		doc: [][]byte{
			t.PPTContainer(t.PPTEnvironment, 0, t.PPTContainer(t.PPTFonts, 0, t.PPTFont(0, "Arial"), t.PPTFont(1, "Wingdings"))),
			group,
			t.PPTContainer(t.PPTHeadFoot, 3, t.PPTHeadFootAtom(0x2D), t.PPTCString(0, "March 2004"), t.PPTCString(2, "Confidential")),
			t.PPTContainer(t.PPTExObjList, 0, t.PPTHyperlink(7, "https://example.com/there"), t.PPTExObj(1, 4, 40)),
			t.PPTContainer(t.PPTList, 0, t.PPTTags("___PPT9", t.PPTOutlineNumbers(256, 1, t.PPTAutoNumbers([3]int{1, 3, 1}, [3]int{0, -1, 0})))),
		},
		master: [][]byte{
			t.PPTMasterStyle(1, t.PPTParaRun{Bullet: 1, Char: 'n', Font: 1}, t.PPTParaRun{Bullet: 1, Char: '\u2013', Font: 0}),
			t.PPTShapes(
				t.PPTShape{Top: 100, Left: 100, Right: 2000, Bottom: 300, Text: t.PPTText(4, "The Company")}.Bytes(),
				t.PPTShape{Top: 150, Left: 5000, Right: 5600, Bottom: 450, Props: []t.ArtProp{{ID: 0x0104, Value: 1}}}.Bytes(),
				t.PPTShape{Top: 4000, Left: 2000, Right: 3500, Bottom: 4200, Placeholder: 9, Text: t.PPTText(4, "*", t.PPTField(0x0FFA, 0))}.Bytes(),
				t.PPTShape{Top: 4000, Left: 5000, Right: 5600, Bottom: 4200, Placeholder: 8, Text: t.PPTText(4, "Page *", t.PPTField(0x0FD8, 5))}.Bytes(),
			),
		},
		slides: []pptSlide{{
			outline: t.Cat(t.PPTText(0, "A title"), t.PPTText(1, body, t.PPTStyle(
				[]t.PPTParaRun{{Count: 4, Font: -1}, {Count: 12, Level: 1, Font: -1}, {Count: 8, Bullet: 1, Char: '*', Font: 0}},
				[]t.PPTCharRun{{Count: 8, Font: 0, Sym: -1}, {Count: 1, Font: 1, Sym: -1}, {Count: 15, Font: 0, Sym: -1, Ext: 1}}),
				t.PPTField(0x0FD8, 22), t.PPTLink(7, 4, 4, 7))),
			shapes: [][]byte{
				t.PPTShape{Top: 200, Left: 400, Right: 5000, Bottom: 800, Placeholder: 13, OutlineRef: 1}.Bytes(),
				t.PPTShape{Top: 1000, Left: 400, Right: 5000, Bottom: 3000, Placeholder: 14, OutlineRef: 2}.Bytes(),
				t.PPTShape{Top: 3200, Left: 400, Right: 2000, Bottom: 3600, Text: t.PPTTextBytes(4, "A box\rof two paragraphs"),
					Data: t.PPTTags("___PPT9", t.PPTAutoNumbers([3]int{1, 9, 3})), Props: []t.ArtProp{{ID: 0x03BF, Value: 0x00020002}}}.Bytes(),
				t.PPTShape{Top: 3300, Left: 3000, Right: 4000, Bottom: 4000, Data: t.PPTExObjRef(1), Props: []t.ArtProp{{ID: 0x0104, Value: 1}, {ID: 0x0381, Text: "a chart"}}}.Bytes(),
				t.PPTGroup(
					t.PPTShape{Group: &[4]int32{0, 0, 200, 100}, Top: 800, Left: 800, Right: 2400, Bottom: 1600, Props: []t.ArtProp{{ID: 0x039F, Value: 1}}},
					t.PPTShape{Child: &[4]int32{0, 0, 100, 50}, Text: t.PPTText(4, "a")},
					t.PPTShape{Child: &[4]int32{100, 0, 200, 50}, Text: t.PPTText(4, "b")},
				),
			},
			records: [][]byte{t.PPTCommentOn("Ada", "Check this figure", 2004, 3, 9, 14, 30), t.PPTHidden()},
			notes:   t.PPTShapes(t.PPTShape{Top: 400, Left: 100, Right: 900, Bottom: 900, Placeholder: 12, Text: t.PPTText(2, "Say this first.\rThen this.")}.Bytes()),
			layout:  1,
		}},
	}
}

// What a hostile presentation can make the reader do is bounded by the
// limits on what a document may hold, not by what the file repeats: a
// master's text that comes to nothing on every slide, and one chart
// shown by thousands of shapes.
func TestPresentationWorstCases(tt *testing.T) {
	// A master with a thousand texts that are a field with nothing to put
	// in it, and a thousand slides that follow it.
	var texts [][]byte
	for i := 0; i < 1000; i++ {
		texts = append(texts, t.PPTShape{Top: int16(i + 1), Left: 100, Right: 900, Bottom: int16(i + 2), Text: t.PPTText(4, "*", t.PPTField(0x0FF7, 0))}.Bytes())
	}
	slides := make([]pptSlide, 1000)
	for i := range slides {
		slides[i] = pptSlide{layout: 1}
	}
	data := pptDeck{master: [][]byte{t.PPTShapes(texts...)}, slides: slides}.bytes()
	start := time.Now()
	doc, err := extract(data, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 1 << 20}})
	if err != nil {
		tt.Fatal(err)
	}
	cut := 0
	for i := range doc.Parts {
		if _, ok := warning(doc.Parts[i].Warnings, gofastcore.WarnLimitExceeded); ok {
			cut++
		}
	}
	if cut == 0 || time.Since(start) > 20*time.Second {
		tt.Errorf("a master's empty texts: %d slides cut, %v", cut, time.Since(start))
	}

	// One chart, shown by three thousand shapes: each showing is charged
	// to what the document may decode.
	graph := t.BookFile(t.Workbook(nil, t.Sheet{Name: "Graph", Kind: 2, Records: [][]byte{
		t.Rec(0x0004, t.Cat(t.U16(0, 1), []byte{0, 0, 0, 1, 0}, []byte("a"))...),
		t.Rec(0x0003, t.Cat(t.U16(1, 1), []byte{0, 0, 0}, t.F64(1))...),
		t.Rec(0x1002, make([]byte, 16)...), t.Rec(0x1033), t.Rec(0x1034),
	}}))
	var shapes [][]byte
	for i := 0; i < 3000; i++ {
		shapes = append(shapes, t.PPTShape{Top: 800, Left: 800, Right: 4000, Bottom: 3200, Data: t.PPTExObjRef(1)}.Bytes())
	}
	deck := pptDeck{
		doc:     [][]byte{t.PPTContainer(t.PPTExObjList, 0, t.PPTExObj(1, 4, 40))},
		objects: map[uint32][]byte{40: t.PPTStorage(graph, false)},
		slides:  []pptSlide{{layout: 1, shapes: shapes}},
	}
	start = time.Now()
	doc, err = extract(deck.bytes(), &gofastcore.Options{Limits: gofastcore.Limits{MaxDecodedBytes: 1 << 20}})
	if err != nil {
		tt.Fatal(err)
	}
	charts := 0
	for _, b := range doc.Parts[0].Blocks {
		if b.Attrs[gofastcore.AttrRole] == gofastcore.RoleChart {
			charts++
		}
	}
	_, limited := warning(doc.Parts[0].Warnings, gofastcore.WarnLimitExceeded)
	if charts == 0 || charts >= 3000 || !limited || time.Since(start) > 20*time.Second {
		tt.Errorf("one chart in 3000 shapes: %d read, limited %v, %v; warnings %+v", charts, limited, time.Since(start), doc.Parts[0].Warnings)
	}
}
