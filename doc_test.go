package gofastmsbin_test

import (
	"context"
	"io"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-core/cfb/cfbtest"
	t "github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// The styles of the test documents, by number.
var docStyles = []t.DocStyle{
	{Name: "Normal", Base: 0x0FFF},
	{Name: "heading 1", Base: 0, Papx: t.Sprm(0x2640, 0)},
	{Name: "heading 2", Base: 0, Papx: t.Sprm(0x2640, 1)},
	{Name: "Title", Base: 0},
	{Name: "Quote", Base: 0},
	{Name: "Plain Text", Base: 0},
	{Name: "Strong", Base: 0x0FFF, Kind: 2, Chpx: t.Sprm(0x0835, 1)},
	{Name: "heading 8", Base: 0, Papx: t.Sprm(0x2640, 7)},
}

func wordFileOf(d t.DocFile) []byte {
	if d.Styles == nil {
		d.Styles = docStyles
	}
	return cfbtest.Compound(t.WordStreams(d))
}

// lines gives the blocks of a part as lines: kind, level, marker, text.
func lines(part *gofastcore.Part) []string {
	var out []string
	for i := range part.Blocks {
		out = append(out, blockKey(&part.Blocks[i]))
	}
	return out
}

func TestWordParagraphs(tt *testing.T) {
	for _, narrow := range []bool{false, true} {
		// Curly quotes do not fit in one byte as the test writer writes
		// them.
		last := "caf\u00e9 \u201cquoted\u201d"
		if narrow {
			last = "caf\u00e9 quoted"
		}
		doc := mustExtract(tt, wordFileOf(t.DocFile{Narrow: narrow, Main: []t.DocPara{
			{Text: "The title", Style: 3},
			{Text: "A heading", Style: 1},
			{Text: "  Text with a tab\tand a line\x0bbreak, a hard\x1ehyphen and a soft\x1fone.  "},
			{Text: ""},
			{Text: "A second heading", Style: 2},
			{Text: "Deep", Style: 7},
			{Text: "A quotation.", Style: 4},
			{Text: "code line 1", Style: 5},
			{Text: "code line 2", Style: 5},
			{Text: "An outline level of its own.", Papx: t.Sprm(0x2640, 2)},
			{Text: last},
		}}), nil)
		want := []string{
			"heading1 The title", "heading1 A heading",
			"paragraph Text with a tab and a line break, a hard\u2011hyphen and a soft\u00adone.",
			"heading2 A second heading", "heading6 Deep", "quote A quotation.", "code code line 1 code line 2",
			"heading3 An outline level of its own.", "paragraph " + last,
		}
		part := &doc.Parts[0]
		if got := lines(part); !reflect.DeepEqual(got, want) {
			tt.Errorf("narrow %v:\n%s\nwant\n%s", narrow, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		if doc.Source.Format != "doc" || part.Kind != gofastcore.PartBody {
			tt.Errorf("%+v", doc.Source)
		}
		if b := part.Blocks[0]; b.Attrs["style"] != "title" {
			tt.Errorf("the title: %+v", b.Attrs)
		}
		if b := part.Blocks[4]; b.Attrs[gofastcore.AttrLevel] != "8" {
			tt.Errorf("a heading of level 8: %+v", b.Attrs)
		}
		if _, ok := warning(part.Warnings, gofastcore.WarnHeadingLevelClamped); !ok {
			tt.Errorf("%+v", part.Warnings)
		}
	}
}

// Bold, italic and the like are spans with Options.Spans; hidden text is
// declared whether or not they are asked for.
func TestWordSpansAndHiddenText(tt *testing.T) {
	bold, italic, hidden := t.Sprm(0x0835, 1), t.Sprm(0x0836, 1), t.Sprm(0x083C, 1)
	data := wordFileOf(t.DocFile{Main: []t.DocPara{
		{Text: "plain bold italic hidden end", Runs: []t.DocRun{{Len: 6}, {Len: 4, Chpx: bold}, {Len: 1}, {Len: 6, Chpx: italic}, {Len: 1}, {Len: 6, Chpx: hidden}, {Len: 4}}},
		{Text: "all hidden", Runs: []t.DocRun{{Len: 10, Chpx: hidden}}},
		{Text: "styled strong", Runs: []t.DocRun{{Len: 7}, {Len: 6, Chpx: t.Sprm(0x4A30, 6, 0)}}},
	}})
	doc := mustExtract(tt, data, &gofastcore.Options{Spans: true})
	b := doc.Parts[0].Blocks
	want := []gofastcore.Span{{Start: 6, End: 10, Style: gofastcore.StyleBold}, {Start: 11, End: 17, Style: gofastcore.StyleItalic},
		{Start: 18, End: 24, Display: gofastcore.DisplayHidden}}
	if !reflect.DeepEqual(b[0].Spans, want) || b[0].Display != gofastcore.DisplayHidden {
		tt.Errorf("spans: %+v, display %v", b[0].Spans, b[0].Display)
	}
	if b[1].Display != gofastcore.DisplayHidden || b[1].Spans != nil {
		tt.Errorf("a hidden paragraph: %+v %v", b[1].Spans, b[1].Display)
	}
	if want := []gofastcore.Span{{Start: 7, End: 13, Style: gofastcore.StyleBold}}; !reflect.DeepEqual(b[2].Spans, want) {
		tt.Errorf("a character style: %+v", b[2].Spans)
	}
	// Without Options.Spans only what is declared about display.
	doc = mustExtract(tt, data, nil)
	if got := doc.Parts[0].Blocks[0].Spans; len(got) != 1 || got[0].Display != gofastcore.DisplayHidden {
		tt.Errorf("without spans: %+v", got)
	}
}

func TestWordTable(tt *testing.T) {
	in := t.Sprm(0x2416, 1)
	// The definition of a row: its length, the number of cells, the
	// positions of their edges, and twenty bytes for each cell.
	def := func(flags []uint16, edges ...uint16) []byte {
		body := t.Cat([]byte{byte(len(edges) - 1)}, t.U16(edges...))
		for i := 0; i < len(edges)-1; i++ {
			cell := make([]byte, 20)
			if i < len(flags) {
				cell[0], cell[1] = byte(flags[i]), byte(flags[i]>>8)
			}
			body = append(body, cell...)
		}
		return t.Cat(in, t.Sprm(0x2417, 1), t.Sprm(0xD608), t.U16(uint16(len(body)+1)), body)
	}
	doc := mustExtract(tt, wordFileOf(t.DocFile{Main: []t.DocPara{
		{Text: "Before the table."},
		{Text: "Name", Mark: 7, Papx: in}, {Text: "Value", Mark: 7, Papx: in}, {Text: "Note", Mark: 7, Papx: in},
		{Text: "", Mark: 7, Papx: t.Cat(def(nil, 0, 100, 200, 300), t.Sprm(0x3404, 1))},
		{Text: "wide cell", Mark: 7, Papx: in}, {Text: "first line", Papx: in}, {Text: "second line", Mark: 7, Papx: in},
		{Text: "", Mark: 7, Papx: def(nil, 0, 200, 300)},
		{Text: "tall", Mark: 7, Papx: in}, {Text: "b", Mark: 7, Papx: in}, {Text: "c", Mark: 7, Papx: in},
		{Text: "", Mark: 7, Papx: def([]uint16{0x0060}, 0, 100, 200, 300)},
		{Text: "", Mark: 7, Papx: in}, {Text: "e", Mark: 7, Papx: in}, {Text: "f", Mark: 7, Papx: in},
		{Text: "", Mark: 7, Papx: def([]uint16{0x0020}, 0, 100, 200, 300)},
		{Text: "After the table."},
	}}), nil)
	b := doc.Parts[0].Blocks
	if len(b) != 3 || b[1].Kind != gofastcore.KindTable {
		tt.Fatalf("%v", lines(&doc.Parts[0]))
	}
	want := [][]string{{"Name", "Value", "Note"}, {"wide cell", "", "first line\nsecond line"}, {"tall", "b", "c"}, {"", "e", "f"}}
	if !reflect.DeepEqual(b[1].Table.Rows, want) {
		tt.Errorf("rows: %q", b[1].Table.Rows)
	}
	if !reflect.DeepEqual(b[1].Table.Merges, []gofastcore.Merge{{Row: 1, Col: 0, RowSpan: 1, ColSpan: 2}, {Row: 2, Col: 0, RowSpan: 2, ColSpan: 1}}) || b[1].Table.HeaderRows != 1 {
		tt.Errorf("merges %+v, header rows %d", b[1].Table.Merges, b[1].Table.HeaderRows)
	}
}

// rowDef is the definition of a table's row: its length, the number of
// cells, the positions of their edges, and twenty bytes for each cell,
// which begin with the cell's flags.
func rowDef(flags []uint16, edges ...uint16) []byte {
	body := t.Cat([]byte{byte(len(edges) - 1)}, t.U16(edges...))
	for i := 0; i < len(edges)-1; i++ {
		cell := make([]byte, 20)
		if i < len(flags) {
			cell[0], cell[1] = byte(flags[i]), byte(flags[i]>>8)
		}
		body = append(body, cell...)
	}
	return t.Cat(t.Sprm(0x2416, 1), t.Sprm(0x2417, 1), t.Sprm(0xD608), t.U16(uint16(len(body)+1)), body)
}

// Cells merged along a row are one cell, with the text of each on a line
// of its own; a cell with no text adds no line.
func TestWordCellsMergedAlongARow(tt *testing.T) {
	in := t.Sprm(0x2416, 1)
	cell := func(s string) t.DocPara { return t.DocPara{Text: s, Mark: 7, Papx: in} }
	doc := mustExtract(tt, wordFileOf(t.DocFile{Main: []t.DocPara{
		cell("a"), cell("b"), cell(""), cell("d"), cell("e"),
		{Text: "", Mark: 7, Papx: rowDef([]uint16{1, 2, 2, 2, 0}, 0, 100, 200, 300, 400, 500)},
		// The first of the merged cells has no text; then two merges in one row.
		cell(""), cell("g"), cell("h"), cell("i"), cell("j"),
		{Text: "", Mark: 7, Papx: rowDef([]uint16{1, 2, 0, 1, 2}, 0, 100, 200, 300, 400, 500)},
		// A cell that says it is merged, with none before it to merge into.
		cell("k"), cell("l"), cell("m"), cell("n"), cell("o"),
		{Text: "", Mark: 7, Papx: rowDef([]uint16{2, 0, 0, 0, 0}, 0, 100, 200, 300, 400, 500)},
	}}), nil)
	b := doc.Parts[0].Blocks
	if len(b) != 1 || b[0].Kind != gofastcore.KindTable {
		tt.Fatalf("%v", lines(&doc.Parts[0]))
	}
	want := [][]string{{"a\nb\nd", "", "", "", "e"}, {"\ng", "", "h", "i\nj", ""}, {"k", "l", "m", "n", "o"}}
	if !reflect.DeepEqual(b[0].Table.Rows, want) {
		tt.Errorf("rows: %q", b[0].Table.Rows)
	}
	merges := []gofastcore.Merge{{Row: 0, Col: 0, RowSpan: 1, ColSpan: 4}, {Row: 1, Col: 0, RowSpan: 1, ColSpan: 2}, {Row: 1, Col: 3, RowSpan: 1, ColSpan: 2}}
	if !reflect.DeepEqual(b[0].Table.Merges, merges) {
		tt.Errorf("merges: %+v", b[0].Table.Merges)
	}
}

// A row of as many cells as a row can have, all merged into the first,
// costs no more than their text: not a copy of the cell for each.
func TestWordLongRowOfMergedCells(tt *testing.T) {
	const cells, size = 250, 64 << 10
	in := t.Sprm(0x2416, 1)
	text := strings.Repeat("seventeen bytes, ", size/17) + "end"
	paras := make([]t.DocPara, 0, cells+1)
	flags := make([]uint16, cells)
	edges := make([]uint16, cells+1)
	var want strings.Builder
	for i := 0; i < cells; i++ {
		paras = append(paras, t.DocPara{Text: text, Mark: 7, Papx: in})
		flags[i], edges[i+1] = 2, uint16(100*(i+1))
		if i > 0 {
			want.WriteByte('\n')
		}
		want.WriteString(text)
	}
	flags[0] = 1
	paras = append(paras, t.DocPara{Text: "", Mark: 7, Papx: rowDef(flags, edges...)})
	data := wordFileOf(t.DocFile{Main: paras})
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	doc := mustExtract(tt, data, nil)
	took := time.Since(start)
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	b := doc.Parts[0].Blocks
	if len(b) != 1 || b[0].Kind != gofastcore.KindTable || len(b[0].Table.Rows) != 1 || b[0].Table.Rows[0][0] != want.String() {
		tt.Fatalf("%d blocks; the cell's text is not the %d bytes of the cells", len(b), want.Len())
	}
	tt.Logf("%d cells of %d bytes took %v and allocated %d MB", cells, len(text), took, allocated>>20)
	// Joined by copying, the cell is allocated once for each cell merged
	// into it: 2,030 MB for these 16 MB of text, where joined once it is
	// 151 MB.
	if allocated > 512<<20 {
		tt.Errorf("a row of %d merged cells allocated %d MB", cells, allocated>>20)
	}
}

// A drop-down list of a form shows what was chosen, which is text; the
// entries to choose from are not read, and that is said, wherever the
// list is.
func TestWordFormDropDowns(tt *testing.T) {
	data := wordFileOf(t.DocFile{
		Main: []t.DocPara{
			{Text: "Colour: \x13 FORMDROPDOWN \x14Red\x15, size: \x13 FORMDROPDOWN \x14Large\x15.\x02"},
			{Text: "Name: \x13 FORMTEXT \x14Ada\x15."},
		},
		Footnotes: [][]t.DocPara{{{Text: "\x02 Or \x13 FORMDROPDOWN \x14Blue\x15."}}},
	})
	doc := mustExtract(tt, data, nil)
	if got := doc.Parts[0].Blocks[0].Text; got != "Colour: Red, size: Large." {
		tt.Errorf("the paragraph: %q", got)
	}
	if w, ok := warning(doc.Warnings, gofastcore.WarnInputUnreadContent); !ok || w.Message != "3 drop-down lists of form fields (their entries)" {
		tt.Errorf("warnings: %+v", doc.Warnings)
	}
	// A form with text fields only has nothing that is not read.
	plain := wordFileOf(t.DocFile{Main: []t.DocPara{{Text: "Name: \x13 FORMTEXT \x14Ada\x15."}}})
	if _, ok := warning(mustExtract(tt, plain, nil).Warnings, gofastcore.WarnInputUnreadContent); ok {
		tt.Error("a form of text fields says it has content that is not read")
	}
}

// A long run of paragraphs of code is one block, and joining them costs
// no more than their text: not a copy of the block for every paragraph.
func TestWordLongRunOfCode(tt *testing.T) {
	const n = 60000
	paras := make([]t.DocPara, 0, n+2)
	paras = append(paras, t.DocPara{Text: "Before."})
	var want strings.Builder
	for i := 0; i < n; i++ {
		line := "line " + strconv.Itoa(i) + " of the listing, some forty bytes long"
		paras = append(paras, t.DocPara{Text: line, Style: 5})
		if i > 0 {
			want.WriteByte('\n')
		}
		want.WriteString(line)
	}
	paras = append(paras, t.DocPara{Text: "After."})
	data := wordFileOf(t.DocFile{Main: paras})
	start := time.Now()
	doc := mustExtract(tt, data, nil)
	took := time.Since(start)
	b := doc.Parts[0].Blocks
	if len(b) != 3 || b[1].Kind != gofastcore.KindCode || b[1].Text != want.String() || b[2].Text != "After." {
		tt.Fatalf("%d blocks; the code is %d bytes, want %d", len(b), len(b[1].Text), want.Len())
	}
	// Joined by copying, this run takes more than twice the limit, and one
	// ten times as long a hundred times that.
	limit := 2 * time.Second
	if underRace {
		limit = time.Minute
	}
	if took > limit {
		tt.Errorf("a run of %d paragraphs of code took %v", n, took)
	}
}

// An equation of the Equation Editor is an object the text shows through
// a field; the mark between the field's code and its result names the
// object. The equation is text of its paragraph, in the linear form, and
// a paragraph that is an equation and nothing else is a formula. It is
// no embedded file then; an object that holds no equation stays one.
func TestWordEquations(tt *testing.T) {
	eq := func(s string) []byte {
		return t.EquationNative(t.MTEF3(t.EqLine(t.EqString(t.EqVariable, s), t.EqTmpl(15, 0, t.EqNull(), t.EqLine(t.EqString(t.EqNumber, "2"))))))
	}
	// A paragraph around a field whose mark names the object.
	para := func(before string, id uint32, after string) t.DocPara {
		code := "\x13 EMBED Equation.3 "
		object := t.Cat(t.Sprm(0x6A03, byte(id), byte(id>>8), byte(id>>16), byte(id>>24)), t.Sprm(0x080A, 1))
		picture := t.Cat(t.Sprm(0x0855, 1), t.Sprm(0x6A03, 0, 0, 0, 0))
		return t.DocPara{Text: before + code + "\x14\x01\x15" + after,
			Runs: []t.DocRun{{Len: len(before) + len(code)}, {Len: 1, Chpx: object}, {Len: 1, Chpx: picture}, {Len: 1 + len(after)}}}
	}
	streams := t.WordStreams(t.DocFile{Styles: docStyles, Main: []t.DocPara{
		para("The energy is ", 1001, ", he wrote."),
		para("", 1002, ""),
		para("And a sheet: ", 1003, "."),
		para("Again ", 1001, "."),
	}})
	streams["ObjectPool/_1001/Equation Native"] = eq("mc")
	streams["ObjectPool/_1002/Equation Native"] = eq("x")
	streams["ObjectPool/_1003/Workbook"] = t.Cat(t.BOF(0x0005), t.EOF())
	var handed []string
	opts := &gofastcore.Options{Child: func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
		handed = append(handed, name)
		return nil, gofastcore.ErrUnsupported
	}}
	doc := mustExtract(tt, cfbtest.Compound(streams), opts)
	wantLines(tt, "equations", lines(&doc.Parts[0]), []string{
		"paragraph The energy is mc^2, he wrote.", "note:field EMBED Equation.3",
		"formula x^2", "note:field EMBED Equation.3",
		"paragraph And a sheet: .", "note:field EMBED Equation.3",
		"paragraph Again mc^2.", "note:field EMBED Equation.3",
	})
	if got := doc.Parts[0].Blocks[2].Attrs[gofastcore.AttrNotation]; got != gofastcore.NotationUnicodeMath {
		tt.Errorf("the formula's notation: %q", got)
	}
	if len(doc.Children) != 1 || len(handed) != 1 || !strings.HasPrefix(handed[0], "_1003") {
		tt.Errorf("embedded files: %d, handed on %v", len(doc.Children), handed)
	} // An equation in a footnote, with a character Unicode has nothing
	// for: it is counted with those of the text.
	note := para("\x02 See ", 1004, ".")
	streams = t.WordStreams(t.DocFile{Styles: docStyles, Main: []t.DocPara{{Text: "A claim.\x02"}}, Footnotes: [][]t.DocPara{{note}}})
	streams["ObjectPool/_1004/Equation Native"] = t.EquationNative(t.MTEF3(t.EqLine(t.EqString(t.EqVariable, "a"), t.EqChar(t.EqSymbol, 0xE123))))
	doc = mustExtract(tt, cfbtest.Compound(streams), nil)
	wantLines(tt, "an equation in a footnote", lines(&doc.Parts[0]), []string{"paragraph A claim.", "footnote[1] See a\uFFFD. EMBED Equation.3"})
	if w, ok := warning(doc.Parts[0].Warnings, gofastcore.WarnTextUnmappedGlyphs); !ok || !strings.HasPrefix(w.Message, "1 characters of equations") {
		tt.Errorf("warnings: %+v", doc.Parts[0].Warnings)
	}
}

func TestWordFieldsFootnotesAndTrackedChanges(tt *testing.T) {
	del := t.Cat(t.Sprm(0x0800, 1), t.Sprm(0x4863, 0, 0), t.Sprm(0x6864, 0x40, 0x09, 0x41, 0x06)) // 2000-01-01 01:00... packed
	data := wordFileOf(t.DocFile{
		Main: []t.DocPara{
			{Text: "See \x13 HYPERLINK \"https://example.com/a\" \\l \"part\" \x14the site\x15 and page \x13 PAGE \x14 7\x15."},
			{Text: "A claim.\x02 And another.\x02"},
			{Text: "Kept removed kept.", Runs: []t.DocRun{{Len: 5}, {Len: 8, Chpx: del}, {Len: 5}}},
		},
		Footnotes: [][]t.DocPara{{{Text: "\x02 The first note."}}, {{Text: "\x02 The second,"}, {Text: "in two paragraphs."}}},
	})
	doc := mustExtract(tt, data, &gofastcore.Options{Spans: true})
	want := []string{
		"paragraph See the site and page 7.", "note:field PAGE",
		"paragraph A claim. And another.", "footnote[1] The first note.", "footnote[2] The second, in two paragraphs.",
		"paragraph Kept kept.", "note:deletion removed",
	}
	part := &doc.Parts[0]
	if got := lines(part); !reflect.DeepEqual(got, want) {
		tt.Errorf("%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if s := part.Blocks[0].Spans; len(s) != 1 || s[0].Href != "https://example.com/a#part" || part.Blocks[0].Text[s[0].Start:s[0].End] != "the site" {
		tt.Errorf("the link: %+v", s)
	}
	if b := part.Blocks[1]; b.Attrs[gofastcore.AttrStored] != gofastcore.StoredFieldCode {
		tt.Errorf("the field's code: %+v", b.Attrs)
	}
	if _, ok := warning(part.Warnings, gofastcore.WarnTextTrackedChanges); !ok {
		tt.Errorf("%+v", part.Warnings)
	}
}

// A document cut off at any byte, or with any byte changed, gives a valid
// document or a shared error.
func TestDamagedWordDocuments(tt *testing.T) {
	in := t.Sprm(0x2416, 1)
	streams := t.WordStreams(t.DocFile{Styles: docStyles, Main: []t.DocPara{
		{Text: "A heading", Style: 1}, {Text: "Text \x13 PAGE \x14 7\x15 and a note.\x02", Runs: []t.DocRun{{Len: 5, Chpx: t.Sprm(0x0835, 1)}, {Len: 23}}},
		{Text: "a", Mark: 7, Papx: in}, {Text: "b", Mark: 7, Papx: in}, {Text: "", Mark: 7, Papx: t.Cat(in, t.Sprm(0x2417, 1))},
	}, Footnotes: [][]t.DocPara{{{Text: "\x02 note"}}}})
	opts := &gofastcore.Options{Spans: true}
	for _, name := range []string{"WordDocument", "1Table"} {
		good := streams[name]
		step := 1
		if name == "WordDocument" {
			step = 3 // a larger stream, mostly zeros
		}
		for i := 0; i < len(good); i += step {
			for _, v := range []byte{0x00, 0xFF, good[i] ^ 0x41} {
				bad := append([]byte(nil), good...)
				bad[i] = v
				streams[name] = bad
				doc, err := extract(cfbtest.Compound(streams), opts)
				checkOutcome(tt, doc, err)
			}
			if i%7 == 0 {
				streams[name] = good[:i]
				doc, err := extract(cfbtest.Compound(streams), opts)
				checkOutcome(tt, doc, err)
			}
		}
		streams[name] = good
	}
}

// A document of Word 6 or 95: its text in the code page of its language,
// as paragraphs, tables by their cell marks, footnotes and the results of
// fields.
func TestWord95(tt *testing.T) {
	text := "\xcf\xf0\xe8\xe2\xe5\xf2, \xec\xe8\xf0.\x02\r" + // "Привет, мир." and a footnote
		"a\x07b\x07\x07c\x07d\x07\x07" +
		"Page \x13 PAGE \x14" + "7\x15 of \x13 NUMPAGES \x15" + "9.\r" +
		"An open \x13 mark, and text after it.\r" +
		"Last paragraph, with no mark"
	stream := t.Word6Stream(0x0419, text, "\x02 \xd1\xed\xee\xf1\xea\xe0.")
	doc := mustExtract(tt, cfbtest.Compound(map[string][]byte{"WordDocument": stream}), nil)
	want := []string{
		"paragraph Привет, мир.", "footnote[1] Сноска.",
		"table a|b/c|d", "paragraph Page 7 of 9.", "paragraph An open mark, and text after it.", "paragraph Last paragraph, with no mark",
	}
	if got := lines(&doc.Parts[0]); !reflect.DeepEqual(got, want) {
		tt.Errorf("%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if doc.Source.Format != "doc" {
		tt.Errorf("%+v", doc.Source)
	}
	// Cut off at any byte, or with any byte changed: a valid document or
	// a shared error.
	for i := 0; i < len(stream); i += 3 {
		bad := append([]byte(nil), stream...)
		bad[i] ^= 0x5A
		d, err := extract(cfbtest.Compound(map[string][]byte{"WordDocument": bad}), nil)
		checkOutcome(tt, d, err)
		d, err = extract(cfbtest.Compound(map[string][]byte{"WordDocument": stream[:i]}), nil)
		checkOutcome(tt, d, err)
	}
}
