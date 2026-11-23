package gofastmsbin_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	t "github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// The cell formats of the test workbooks, by number: General; a date;
// two decimals; a format that shows nothing; text in Wingdings; percent;
// a format of text.
var styles = [][]byte{
	t.Font("Arial"), t.Font("Arial"), t.Font("Arial"), t.Font("Arial"), t.Font("Wingdings"), t.Font("Symbol"),
	t.Format(164, "0.00"), t.Format(165, ";;;"), t.Format(166, `"<"@">"`),
	t.XF(0, 0), t.XF(0, 14), t.XF(0, 164), t.XF(0, 165), t.XF(5, 0), t.XF(0, 9), t.XF(0, 166), t.XF(6, 0),
}

const (
	xfGeneral = iota
	xfDate
	xfTwo
	xfNothing
	xfWingdings
	xfPercent
	xfQuoted
	xfSymbol
)

func book(globals [][]byte, sheets ...t.Sheet) []byte {
	return t.BookFile(t.Workbook(append(append([][]byte(nil), styles...), globals...), sheets...))
}

// rows gives the rows of the first table of a part.
func rows(tb testing.TB, part *gofastcore.Part) [][]string {
	tb.Helper()
	for i := range part.Blocks {
		if part.Blocks[i].Kind == gofastcore.KindTable {
			return part.Blocks[i].Table.Rows
		}
	}
	tb.Fatalf("no table in part %d: %+v", part.Number, part.Warnings)
	return nil
}

func TestCellKinds(tt *testing.T) {
	sst := t.SST(t.Plain("shared", "  padded  ", "\ufb01ne", "")...)
	data := book([][]byte{sst}, t.Sheet{Name: "Data", Records: [][]byte{
		t.Dimensions(4, 8),
		t.LabelSST(0, 0, xfGeneral, 0), t.LabelSST(0, 1, xfGeneral, 1), t.LabelSST(0, 2, xfGeneral, 2), t.LabelSST(0, 3, xfGeneral, 3),
		t.LabelSST(0, 4, xfGeneral, 99), t.Label(0, 5, xfGeneral, "a label"), t.LabelSST(0, 6, xfQuoted, 0), t.Blank(0, 7, xfGeneral),
		t.RKInt(1, 0, xfGeneral, 42, false), t.RKInt(1, 1, xfGeneral, 1234, true), t.RKFloat(1, 2, xfGeneral, 0.5), t.Number(1, 3, xfGeneral, 1.0/3),
		t.MulRK(1, 4, xfGeneral, 7, -8, 9), t.Number(1, 7, xfGeneral, 1e20),
		t.Bool(2, 0, xfGeneral, true), t.Bool(2, 1, xfGeneral, false), t.Error(2, 2, xfGeneral, 0x07), t.Error(2, 3, xfGeneral, 0x2A),
		t.Number(2, 4, xfDate, 36526), t.Number(2, 5, xfTwo, 2.345), t.Number(2, 6, xfPercent, 0.25), t.Number(2, 7, xfDate, 36526.75),
		t.Formula(3, 0, xfGeneral, 3), t.FormulaString(3, 1, xfGeneral, "made"), t.FormulaBool(3, 2, xfGeneral, true),
		t.FormulaError(3, 3, xfGeneral, 0x0F), t.FormulaEmpty(3, 4, xfGeneral), t.Formula(3, 5, xfDate, 2),
	}})
	doc := mustExtract(tt, data, nil)
	if doc.Source.Format != "xls" || len(doc.Parts) != 1 || doc.Parts[0].Kind != gofastcore.PartSheet || doc.Parts[0].Name != "Data" {
		tt.Fatalf("%+v", doc.Parts)
	}
	want := [][]string{
		{"shared", "padded", "fine", "", "", "a label", "<shared>", ""},
		{"42", "12.34", "0.5", "0.333333333333333", "7", "-8", "9", "1E+20"},
		{"TRUE", "FALSE", "#DIV/0!", "#N/A", "2000-01-01", "2.35", "25%", "2000-01-01T18:00:00"},
		{"3", "made", "TRUE", "#VALUE!", "", "1900-01-02", "", ""},
	}
	if got := rows(tt, &doc.Parts[0]); !reflect.DeepEqual(got, want) {
		tt.Errorf("cells:\n%q\nwant\n%q", got, want)
	}
	b := doc.Parts[0].Blocks[0]
	if b.Loc.Range != "A1:H4" || b.Origin != gofastcore.OriginInferred || b.Table.Cells != nil {
		tt.Errorf("range %q, origin %v, %d details", b.Loc.Range, b.Origin, len(b.Table.Cells))
	}

	// With Options.Cells: the type of each value, and the stored value
	// where the text does not show it in full.
	doc = mustExtract(tt, data, &gofastcore.Options{Cells: true})
	details := map[string]gofastcore.CellDetail{}
	for _, d := range doc.Parts[0].Blocks[0].Table.Cells {
		details[fmt.Sprintf("%d,%d", d.Row, d.Col)] = d
	}
	for at, want := range map[string]gofastcore.CellDetail{
		"1,0": {Type: gofastcore.CellNumber}, "1,3": {Type: gofastcore.CellNumber, Value: "0.3333333333333333"},
		"2,0": {Type: gofastcore.CellBool}, "2,2": {Type: gofastcore.CellError},
		"2,4": {Type: gofastcore.CellDate, Value: "36526"}, "2,5": {Type: gofastcore.CellNumber, Value: "2.345"},
		"3,1": {Type: gofastcore.CellText}, "3,3": {Type: gofastcore.CellError},
	} {
		got := details[at]
		if got.Type != want.Type || got.Value != want.Value {
			tt.Errorf("detail %s: %+v, want %+v", at, got, want)
		}
	}
	if _, ok := details["0,0"]; ok {
		tt.Error("text that is shown as it is has a detail")
	}
}

// The 1904 date system moves every date by four years and a day.
func TestDateSystem1904(tt *testing.T) {
	data := book([][]byte{t.Rec(0x0022, 1, 0)}, t.Sheet{Name: "S", Records: [][]byte{t.Number(0, 0, xfDate, 0), t.Number(0, 1, xfDate, 34519)}})
	if got := rows(tt, &mustExtract(tt, data, nil).Parts[0]); got[0][0] != "1904-01-01" || got[0][1] != "1998-07-05" {
		tt.Errorf("%q", got)
	}
}

// Shared strings: characters in 8 and in 16 bits, formatting runs and
// phonetic data to pass over, and strings that go on into CONTINUE
// records, where the width of the characters is said anew.
func TestSharedStrings(tt *testing.T) {
	strs := []t.SSTString{
		{Text: "plain"},
		{Text: "wide ✓ 日本"},
		{Text: "runs", Runs: [][2]uint16{{1, 1}, {2, 2}}},
		{Text: "phonetic", Phonetic: []byte{1, 2, 3, 4, 5, 6}},
		{Text: "both ✓", Runs: [][2]uint16{{0, 1}}, Phonetic: []byte{9, 9}},
		{Text: "astral 😀 pair"},
		{Text: "last"},
	}
	var cells [][]byte
	want := make([]string, len(strs))
	for i, s := range strs {
		cells = append(cells, t.LabelSST(0, uint16(i), xfGeneral, uint32(i)))
		want[i] = s.Text
	}
	data := book([][]byte{t.SST(strs...)}, t.Sheet{Name: "S", Records: cells})
	if got := rows(tt, &mustExtract(tt, data, nil).Parts[0]); !reflect.DeepEqual(got[0], want) {
		tt.Errorf("%q", got[0])
	}

	// The same strings, the record cut at every place a writer may cut
	// it: between strings, and inside the characters of one, where the
	// CONTINUE record begins with the flags of the characters that
	// follow.
	whole := t.SST(strs...)[4:] // the body
	// Where the characters of each string are in the body.
	type span struct {
		from, to int
		wide     bool
	}
	var spans []span
	at := 8
	for _, s := range strs {
		b := t.SST(s)[12:]
		head := 3
		if len(s.Runs) > 0 {
			head += 2
		}
		if len(s.Phonetic) > 0 {
			head += 4
		}
		wide := b[2]&1 != 0
		n := len([]rune(s.Text))
		if wide {
			n = (len(b) - head - 4*len(s.Runs) - len(s.Phonetic))
		}
		spans = append(spans, span{at + head, at + head + n, wide})
		at += len(b)
	}
	for cut := 9; cut < len(whole); cut++ {
		first, rest := whole[:cut], whole[cut:]
		inChars := false
		for _, sp := range spans {
			if cut >= sp.from && cut < sp.to {
				if sp.wide && (cut-sp.from)%2 != 0 {
					inChars = true
					first, rest = nil, nil // not between characters: no writer cuts here
					break
				}
				flag := byte(0)
				if sp.wide {
					flag = 1
				}
				rest = append([]byte{flag}, rest...)
				inChars = true
			}
		}
		if first == nil {
			continue
		}
		// A cut inside a string's header is not one a writer makes.
		header := false
		pos := 8
		for _, s := range strs {
			b := t.SST(s)[12:]
			head := 3
			if len(s.Runs) > 0 {
				head += 2
			}
			if len(s.Phonetic) > 0 {
				head += 4
			}
			if cut > pos && cut < pos+head {
				header = true
			}
			pos += len(b)
		}
		if header && !inChars {
			continue
		}
		data := book([][]byte{t.Cat(t.Rec(0x00FC, first...), t.Rec(0x003C, rest...))}, t.Sheet{Name: "S", Records: cells})
		if got := rows(tt, &mustExtract(tt, data, nil).Parts[0]); !reflect.DeepEqual(got[0], want) {
			tt.Fatalf("cut at %d: %q", cut, got[0])
		}
	}
}

// Text in a symbol font is codes of the font: the cell shows the
// characters they stand for. A string that changes font partway is in
// the cell's font up to the change.
func TestSymbolFonts(tt *testing.T) {
	sst := t.SST(
		t.SSTString{Text: "ü"},                                                   // a check mark in Wingdings
		t.SSTString{Text: "· point", Runs: [][2]uint16{{1, 0}}},                  // a bullet in Symbol, then Arial
		t.SSTString{Text: "mg and a", Runs: [][2]uint16{{0, 6}, {1, 0}, {7, 6}}}, // "m" and "a" in Symbol
	)
	data := book([][]byte{sst}, t.Sheet{Name: "S", Records: [][]byte{
		t.LabelSST(0, 0, xfWingdings, 0), t.LabelSST(0, 1, xfGeneral, 0),
		t.LabelSST(0, 2, xfSymbol, 1), t.LabelSST(0, 3, xfGeneral, 2), t.Label(0, 4, xfSymbol, "abg"),
	}})
	got := rows(tt, &mustExtract(tt, data, nil).Parts[0])
	if want := []string{"✓", "ü", "• point", "µg and α", "αβγ"}; !reflect.DeepEqual(got[0], want) {
		tt.Errorf("%q, want %q", got[0], want)
	}
}

func TestSheetsAndWhatTheyDeclare(tt *testing.T) {
	sst := t.SST(t.Plain("a", "b", "c", "d")...)
	cells := func() [][]byte {
		var out [][]byte
		for r := uint16(0); r < 4; r++ {
			for c := uint16(0); c < 4; c++ {
				out = append(out, t.RKInt(r, c, xfGeneral, int32(r*10+c), false))
			}
		}
		return out
	}
	shown := append([][]byte{t.Row(1, true), t.Row(9, true), t.ColInfo(2, 2, true), t.Number(0, 5, xfNothing, 5)}, cells()...)
	shown = append(shown, t.MergeCells([4]uint16{0, 1, 0, 1}, [4]uint16{3, 3, 2, 9}))
	data := book([][]byte{sst},
		t.Sheet{Name: "Shown", Records: shown},
		t.Sheet{Name: "Hidden", State: 1, Records: [][]byte{t.LabelSST(0, 0, xfGeneral, 0)}},
		t.Sheet{Name: "Very hidden", State: 2, Records: [][]byte{t.LabelSST(0, 0, xfGeneral, 1)}},
		t.Sheet{Name: "Module1", Kind: 6},
		t.Sheet{Name: "Chart", Kind: 2},
		t.Sheet{Name: "Macros", Kind: 1, Records: [][]byte{t.LabelSST(0, 0, xfGeneral, 2)}},
		t.Sheet{Name: "Dialog", Records: [][]byte{t.Rec(0x0081, 0x10, 0x00), t.LabelSST(0, 0, xfGeneral, 3)}},
		t.Sheet{Name: "Empty"},
	)
	doc := mustExtract(tt, data, nil)
	var names []string
	for _, p := range doc.Parts {
		names = append(names, p.Name)
	}
	// The last part is the stored part, which a workbook with a macro
	// sheet has.
	if got := strings.Join(names, ","); got != "Shown,Hidden,Very hidden,Chart,Macros,Dialog,Empty," || doc.Parts[7].Kind != gofastcore.PartStored {
		tt.Fatalf("parts: %s", got)
	}
	// The first sheet: a table, with the hidden row, the hidden column and
	// the merged cells, and beside it a cell whose format shows nothing.
	first := doc.Parts[0]
	if len(first.Blocks) != 2 || first.Blocks[0].Loc.Range != "A1:D4" || first.Blocks[1].Loc.Range != "F1:F1" {
		tt.Fatalf("%d blocks: %+v", len(first.Blocks), first.Blocks)
	}
	tbl := first.Blocks[0].Table
	if !reflect.DeepEqual(tbl.Merges, []gofastcore.Merge{{Row: 0, Col: 0, RowSpan: 2, ColSpan: 2}, {Row: 3, Col: 2, RowSpan: 1, ColSpan: 2}}) {
		tt.Errorf("merges: %+v", tbl.Merges)
	}
	if !reflect.DeepEqual(tbl.Display, []gofastcore.DisplayCells{
		{Row: 1, Col: 0, RowSpan: 1, ColSpan: 4, Display: gofastcore.DisplayHidden},
		{Row: 0, Col: 2, RowSpan: 4, ColSpan: 1, Display: gofastcore.DisplayHidden}}) {
		tt.Errorf("display: %+v", tbl.Display)
	}
	if b := first.Blocks[1]; b.Table.Rows[0][0] != "5" || b.Display != gofastcore.DisplayHidden {
		tt.Errorf("a value its format shows as nothing: %+v", b)
	}
	if first.Display != 0 || doc.Parts[1].Display != gofastcore.DisplayHidden || doc.Parts[2].Display != gofastcore.DisplayHidden {
		tt.Errorf("hidden sheets: %v %v %v", first.Display, doc.Parts[1].Display, doc.Parts[2].Display)
	}
	if b := doc.Parts[1].Blocks[0]; b.Display != gofastcore.DisplayHidden {
		tt.Errorf("the blocks of a hidden sheet: %v", b.Display)
	}
	// What is not read is said: a dialog sheet. A macro sheet's cells are
	// read, and it is active content.
	if len(doc.Parts[3].Blocks) != 0 || len(doc.Parts[5].Blocks) != 0 || len(doc.Parts[4].Blocks) != 1 {
		tt.Errorf("chart %d, macros %d, dialog %d blocks", len(doc.Parts[3].Blocks), len(doc.Parts[4].Blocks), len(doc.Parts[5].Blocks))
	}
	if w, ok := warning(doc.Warnings, gofastcore.WarnInputUnreadContent); !ok || w.Message != "1 dialog sheet" {
		tt.Errorf("%+v", doc.Warnings)
	}
	if w, ok := warning(doc.Warnings, gofastcore.WarnInputActiveContent); !ok || !strings.Contains(w.Message, "macro sheets") {
		tt.Errorf("%+v", doc.Warnings)
	}
	if _, ok := warning(doc.Parts[6].Warnings, gofastcore.WarnPartNoText); !ok {
		tt.Errorf("an empty sheet: %+v", doc.Parts[6].Warnings)
	}
}

// Cells apart from one another are tables of their own, in reading
// order; a sheet too thinly filled is written a row at a time.
func TestRegions(tt *testing.T) {
	var recs [][]byte
	for r := uint16(0); r < 3; r++ {
		recs = append(recs, t.MulRK(r, 0, xfGeneral, 1, 2), t.MulRK(r, 5, xfGeneral, 3, 4, 5))
	}
	for r := uint16(10); r < 12; r++ {
		recs = append(recs, t.MulRK(r, 1, xfGeneral, 6, 7))
	}
	doc := mustExtract(tt, book(nil, t.Sheet{Name: "S", Records: recs}), nil)
	var ranges []string
	for _, b := range doc.Parts[0].Blocks {
		ranges = append(ranges, b.Loc.Range)
	}
	if got := strings.Join(ranges, " "); got != "A1:B3 F1:H3 B11:C12" {
		tt.Errorf("tables: %s", got)
	}
	// Cells written out of order are put in order, the first of two at
	// one place kept.
	doc = mustExtract(tt, book(nil, t.Sheet{Name: "S", Records: [][]byte{
		t.RKInt(1, 1, xfGeneral, 4, false), t.RKInt(0, 1, xfGeneral, 2, false), t.RKInt(0, 0, xfGeneral, 1, false),
		t.RKInt(1, 0, xfGeneral, 3, false), t.RKInt(0, 0, xfGeneral, 99, false),
	}}), nil)
	if got := rows(tt, &doc.Parts[0]); !reflect.DeepEqual(got, [][]string{{"1", "2"}, {"3", "4"}}) {
		tt.Errorf("out of order: %q", got)
	}
}

func TestComments(tt *testing.T) {
	recs := [][]byte{t.RKInt(0, 0, xfGeneral, 1, false), t.RKInt(4, 2, xfGeneral, 2, false)}
	recs = append(recs, t.Comment(4, 2, 1, "Ann Author", "Ann Author:\ncheck this"), t.Comment(0, 0, 2, "Ünï", "wide ✓"),
		t.Rec(0x001C, t.Cat(t.U16(7, 7, 0, 99), t.Str("nobody"))...)) // a note whose object is missing
	doc := mustExtract(tt, book(nil, t.Sheet{Name: "S", Records: recs}), nil)
	var notes []gofastcore.Block
	for _, b := range doc.Parts[0].Blocks {
		if b.Kind == gofastcore.KindNote {
			notes = append(notes, b)
		}
	}
	if len(notes) != 2 {
		tt.Fatalf("%d notes: %+v", len(notes), doc.Parts[0].Blocks)
	}
	if n := notes[0]; n.Text != "wide ✓" || n.Attrs[gofastcore.AttrAuthor] != "Ünï" || n.Attrs[gofastcore.AttrNoteType] != gofastcore.NoteComment || n.Loc.Range != "A1" {
		tt.Errorf("%+v", n)
	}
	if n := notes[1]; n.Text != "Ann Author:\ncheck this" || n.Attrs[gofastcore.AttrAuthor] != "Ann Author" || n.Loc.Range != "C5" {
		tt.Errorf("%+v", n)
	}
	// A comment comes after the table at its cell.
	if b := doc.Parts[0].Blocks; b[0].Kind != gofastcore.KindTable || b[1].Kind != gofastcore.KindNote {
		tt.Errorf("order: %v %v", b[0].Kind, b[1].Kind)
	}
}

func TestHyperlinks(tt *testing.T) {
	recs := [][]byte{t.MulRK(0, 0, xfGeneral, 1, 2, 3, 4), t.MulRK(1, 0, xfGeneral, 5, 6, 7, 8),
		t.HLinkURL([4]uint16{0, 0, 0, 0}, "https://example.com/a", ""),
		t.HLinkURL([4]uint16{0, 0, 1, 1}, "https://example.com/b", "part"),
		t.HLinkPlace([4]uint16{0, 0, 2, 2}, "Sheet2!A1"),
		t.HLinkFile([4]uint16{0, 0, 3, 3}, 2, `docs\other.xls`),
		t.HLinkURL([4]uint16{1, 1, 0, 3}, "mailto:someone@example.com", ""),
	}
	data := book(nil, t.Sheet{Name: "S", Records: recs})
	// Links are given with Options.Spans.
	if cells := mustExtract(tt, data, nil).Parts[0].Blocks[0].Table.Cells; cells != nil {
		tt.Errorf("links without Spans: %+v", cells)
	}
	doc := mustExtract(tt, data, &gofastcore.Options{Spans: true})
	got := map[string]string{}
	for _, d := range doc.Parts[0].Blocks[0].Table.Cells {
		got[fmt.Sprintf("%d,%d", d.Row, d.Col)] = d.Href
	}
	want := map[string]string{"0,0": "https://example.com/a", "0,1": "https://example.com/b#part", "0,2": "#Sheet2!A1",
		"0,3": `..\..\docs\other.xls`, "1,0": "mailto:someone@example.com", "1,1": "mailto:someone@example.com",
		"1,2": "mailto:someone@example.com", "1,3": "mailto:someone@example.com"}
	if !reflect.DeepEqual(got, want) {
		tt.Errorf("links: %v", got)
	}
}

// manySheets is a workbook of sheets of numbers and text.
func manySheets(sheets, rowsEach int) []byte {
	var strs []string
	for i := 0; i < 50; i++ {
		strs = append(strs, fmt.Sprintf("string number %d", i))
	}
	var list []t.Sheet
	for s := 0; s < sheets; s++ {
		var recs [][]byte
		for r := 0; r < rowsEach; r++ {
			recs = append(recs, t.LabelSST(uint16(r), 0, xfGeneral, uint32((s+r)%50)), t.MulRK(uint16(r), 1, xfGeneral, int32(r), int32(s)),
				t.Number(uint16(r), 3, xfTwo, float64(r)/7))
		}
		list = append(list, t.Sheet{Name: fmt.Sprintf("Sheet %d", s+1), Records: recs})
	}
	return book([][]byte{t.SST(t.Plain(strs...)...)}, list...)
}

// The limits cut a workbook at a place that does not depend on how many
// sheets are read at a time, and say so.
func TestWorkbookLimits(tt *testing.T) {
	data := manySheets(8, 40)
	jsonOf := func(doc *gofastcore.Document) string {
		var sb strings.Builder
		if err := doc.WriteText(&sb, nil); err != nil {
			tt.Fatal(err)
		}
		for _, p := range doc.Parts {
			fmt.Fprintf(&sb, "%d: %+v\n", p.Number, p.Warnings)
		}
		fmt.Fprintf(&sb, "%+v", doc.Warnings)
		return sb.String()
	}
	for _, c := range []struct {
		name string
		e    gofastmsbin.Extractor
		opts gofastcore.Options
		want string
	}{
		{"cells", gofastmsbin.Extractor{MaxCells: 500}, gofastcore.Options{}, "the limit on the number of cells was reached"},
		{"text", gofastmsbin.Extractor{}, gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 6000}}, "the limit on the amount of text was reached"},
	} {
		var first string
		for _, workers := range []int{1, 2, 8} {
			o := c.opts
			o.Workers = workers
			doc, err := c.e.Extract(context.Background(), strings.NewReader(string(data)), int64(len(data)), &o)
			if err != nil {
				tt.Fatal(err)
			}
			if err := doc.Validate(); err != nil {
				tt.Fatal(err)
			}
			got := jsonOf(doc)
			if !strings.Contains(got, c.want) {
				tt.Errorf("%s, %d workers: the limit is not said", c.name, workers)
			}
			if first == "" {
				first = got
			} else if got != first {
				tt.Errorf("%s: %d workers give another document than one", c.name, workers)
			}
		}
	}
	// With no limit met, the same document for any number of workers.
	one := mustExtract(tt, data, &gofastcore.Options{Workers: 1})
	many := mustExtract(tt, data, &gofastcore.Options{Workers: 8})
	if !reflect.DeepEqual(one, many) {
		tt.Error("8 workers give another document than one")
	}
	// A range of parts.
	doc := mustExtract(tt, data, &gofastcore.Options{Parts: gofastcore.PartRange{First: 3, Last: 4}})
	if len(doc.Parts) != 2 || doc.Parts[0].Name != "Sheet 3" || doc.Parts[1].Number != 4 {
		tt.Errorf("parts 3-4: %d parts", len(doc.Parts))
	}
}

// A workbook cut off at any byte, or with any byte changed, gives a
// valid document or a shared error.
func TestDamagedWorkbooks(tt *testing.T) {
	sst := t.SST(t.SSTString{Text: "one"}, t.SSTString{Text: "two ✓", Runs: [][2]uint16{{1, 5}}})
	recs := [][]byte{t.Dimensions(3, 3), t.Row(0, true), t.LabelSST(0, 0, xfGeneral, 0), t.LabelSST(0, 1, xfSymbol, 1), t.MulRK(1, 0, xfTwo, 1, 2),
		t.FormulaString(2, 0, xfGeneral, "text"), t.MergeCells([4]uint16{0, 1, 0, 0}), t.HLinkURL([4]uint16{0, 0, 0, 0}, "https://example.com", "x")}
	recs = append(recs, t.Comment(0, 0, 1, "A", "note"))
	stream := t.Workbook(append(append([][]byte(nil), styles...), sst), t.Sheet{Name: "S", Records: recs}, t.Sheet{Name: "T", State: 1, Records: recs})
	opts := &gofastcore.Options{Spans: true, Cells: true}
	for i := 0; i < len(stream); i++ {
		doc, err := extract(t.BookFile(stream[:i]), opts)
		checkOutcome(tt, doc, err)
		for _, v := range []byte{0x00, 0xFF, stream[i] ^ 0x41} {
			bad := append([]byte(nil), stream...)
			bad[i] = v
			doc, err := extract(t.BookFile(bad), opts)
			checkOutcome(tt, doc, err)
		}
	}
	// A workbook that lists no sheets has one part, which says so.
	doc := mustExtract(tt, t.BookFile(t.Workbook(nil)), nil)
	if w, ok := warning(doc.Parts[0].Warnings, gofastcore.WarnPartFailed); len(doc.Parts) != 1 || !ok || !strings.Contains(w.Message, "lists no sheets") {
		tt.Errorf("%+v", doc.Parts)
	}
	// A sheet the workbook lists at a place where there is none.
	list := t.Rec(0x0085, t.Cat(t.U32(0x00FFFFFF), []byte{0, 0}, t.ShortStr("Lost"))...)
	doc = mustExtract(tt, t.BookFile(t.Cat(t.BOF(5), list, t.EOF())), nil)
	if w, ok := warning(doc.Parts[0].Warnings, gofastcore.WarnPartFailed); !ok || !strings.Contains(w.Message, "does not contain it") {
		tt.Errorf("%+v", doc.Parts[0].Warnings)
	}
}

// The macros of a workbook are in the stored part, after its sheets.
func TestWorkbookWithMacros(tt *testing.T) {
	streams := t.VBAStreams("_VBA_PROJECT_CUR/", 1252, t.VBAModule{Name: "Module1", Code: "Sub A()\r\nEnd Sub\r\n"})
	streams["Workbook"] = t.Workbook(styles, t.Sheet{Name: "One", Records: [][]byte{t.RKInt(0, 0, xfGeneral, 1, false)}},
		t.Sheet{Name: "Two", Records: [][]byte{t.RKInt(0, 0, xfGeneral, 2, false)}})
	doc := mustExtract(tt, t.Compound(streams), nil)
	if len(doc.Parts) != 3 || doc.Parts[2].Kind != gofastcore.PartStored || doc.Parts[2].Number != 3 || doc.Parts[2].Blocks[0].Kind != gofastcore.KindCode {
		tt.Fatalf("%d parts", len(doc.Parts))
	}
}

// A workbook of Excel 5 or 95 keeps its strings as bytes in a code page.
func TestExcel95(tt *testing.T) {
	globals := [][]byte{t.CodePage(1251), t.Font5("Arial"), t.Format5(164, "0.0"), t.XF5(0, 0), t.XF5(0, 164), t.XF5(0, 14)}
	sheet := [][]byte{
		t.Label5(0, 0, 0, "\xcf\xf0\xe8\xe2\xe5\xf2"), t.Label5(0, 1, 0, "plain"), t.Number(0, 2, 1, 2.25), t.Number(0, 3, 2, 36526),
		t.RKInt(1, 0, 0, 7, false), t.Bool(1, 1, 0, true),
		t.Formula5String(1, 2, 0, "\xe4\xe0", t.Cat(t.PtgRef5(0, 0, true, true), t.PtgStr5("\xe4\xe0"), t.PtgOp(0x08))),
		t.FormulaTokens(1, 3, 0, 9, t.Cat(t.PtgArea5(0, 1, 0, 0), t.PtgAttrSum(), t.PtgRef3d5(1, 1, 4, 2), t.PtgOp(0x03))),
		t.Note5(0, 0, "a note \xe8"), t.Row(1, true),
	}
	data := t.Compound(map[string][]byte{"Book": t.Workbook5(globals, t.Sheet{Name: "\xcb\xe8\xf1\xf2", Records: sheet}, t.Sheet{Name: "Two", State: 1})})
	doc := mustExtract(tt, data, &gofastcore.Options{Cells: true})
	if doc.Source.Format != "xls" || len(doc.Parts) != 2 || doc.Parts[0].Name != "Лист" || doc.Parts[1].Display != gofastcore.DisplayHidden {
		tt.Fatalf("%+v", doc.Parts)
	}
	want := [][]string{{"Привет", "plain", "2.3", "2000-01-01"}, {"7", "TRUE", "да", "9"}}
	if got := rows(tt, &doc.Parts[0]); !reflect.DeepEqual(got, want) {
		tt.Errorf("cells: %q", got)
	}
	if got := formulas(&doc.Parts[0]); got["C2"] != `A1&"да"` || got["D2"] != "SUM($A$1:$A$2)+Two!$C$5" {
		tt.Errorf("formulas: %v", got)
	}
	blocks := doc.Parts[0].Blocks
	if n := blocks[len(blocks)-1]; n.Kind != gofastcore.KindNote || n.Text != "a note и" || n.Loc.Range != "A1" {
		tt.Errorf("the note: %+v", n)
	}
	if d := blocks[0].Table.Display; len(d) != 1 || d[0].Row != 1 {
		tt.Errorf("the hidden row: %+v", d)
	}
}

// A pivot table's cells are a table of their own, with its name.
func TestPivotTable(tt *testing.T) {
	var recs [][]byte
	for r := uint16(0); r < 6; r++ {
		recs = append(recs, t.MulRK(r, 0, xfGeneral, int32(r), int32(r)+1, int32(r)+2, int32(r)+3))
	}
	recs = append(recs, t.SxView([4]uint16{2, 4, 1, 2}, 3, "PivotTable1"))
	doc := mustExtract(tt, book(nil, t.Sheet{Name: "S", Records: recs}), nil)
	var pivot *gofastcore.Block
	for i, b := range doc.Parts[0].Blocks {
		if b.Attrs[gofastcore.AttrRole] == gofastcore.RolePivot {
			pivot = &doc.Parts[0].Blocks[i]
		}
	}
	if pivot == nil || pivot.Loc.Range != "B3:C5" || pivot.Attrs[gofastcore.AttrName] != "PivotTable1" || pivot.Table.HeaderRows != 1 ||
		pivot.Origin != gofastcore.OriginNative || !reflect.DeepEqual(pivot.Table.Rows, [][]string{{"3", "4"}, {"4", "5"}, {"5", "6"}}) {
		tt.Errorf("%+v", pivot)
	}
}
