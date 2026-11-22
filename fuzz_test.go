package gofastmsbin_test

import (
	"errors"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// checkOutcome holds the result of Extract to the contract: a document
// that passes Validate, or an error that wraps one of the shared errors,
// and never both.
func checkOutcome(t testing.TB, doc *gofastcore.Document, err error) {
	t.Helper()
	if err != nil {
		if doc != nil {
			t.Fatalf("a document and an error: %v", err)
		}
		for _, shared := range []error{gofastcore.ErrUnsupported, gofastcore.ErrMalformed, gofastcore.ErrLimitExceeded,
			gofastcore.ErrPasswordRequired, gofastcore.ErrWrongPassword, gofastcore.ErrNoPartsSelected} {
			if errors.Is(err, shared) {
				return
			}
		}
		t.Fatalf("an error of no shared kind: %v", err)
	}
	if doc == nil {
		t.Fatal("neither a document nor an error")
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("invalid document: %v", err)
	}
}

// FuzzExtract feeds arbitrary bytes to Extract. Whatever the input, the
// call must return a valid document or an error that wraps one of the
// shared errors; it must not panic, hang or exhaust memory.
func FuzzExtract(f *testing.F) {
	f.Add(msbintest.Compound(map[string][]byte{"WordDocument": fib(0xA5EC, 0x00C1, 0x0300), "1Table": u16s(1, 1)}))
	f.Add(msbintest.Compound(map[string][]byte{"Workbook": append(record(0x0809, u16s(0x0600, 5, 0, 0, 0, 0, 0, 0)...), record(0x002F, u16s(1, 4, 2)...)...)}))
	f.Add(msbintest.Compound(map[string][]byte{"PowerPoint Document": {1}, "Current User": currentUser(0xF3D1C4DF, 0x03F4)}))
	f.Add(msbintest.CompoundV4(map[string][]byte{"Book": record(0x0809, u16s(0x0500, 5, 0, 0)...), "a/b": make([]byte, 5000)}))
	f.Add([]byte(`{\rtf1 }`))
	// Properties, macros and embedded objects of every shape.
	shared := with(wordFile(), msbintest.VBAStreams("Macros/", 1252, msbintest.VBAModule{Name: "M", Code: "Sub A()\r\nEnd Sub\r\n"}))
	shared["\x05SummaryInformation"] = msbintest.PropertyStream(msbintest.Section{Format: msbintest.FmtSummary, CodePage: 1252,
		Props: []msbintest.Prop{{ID: 2, Value: "title"}, {ID: 12, Value: time.Unix(1e9, 0)}}})
	shared["\x05DocumentSummaryInformation"] = msbintest.PropertyStream(
		msbintest.Section{Format: msbintest.FmtDocSummary, CodePage: 1200, Props: []msbintest.Prop{{ID: 15, Value: msbintest.Wide("co")}}},
		msbintest.Section{Format: msbintest.FmtUser, CodePage: 932, Props: []msbintest.Prop{{ID: 2, Name: "n", Value: true}}})
	shared["ObjectPool/_1/\x01Ole10Native"] = packaged("a.txt", `C:\a.txt`, []byte("text"))
	shared["ObjectPool/_2/Package"] = []byte("PK\x03\x04")
	shared["ObjectPool/_3/Workbook"] = bookFile()["Workbook"]
	shared["ObjectPool/_3/MBD00000001/WordDocument"] = wordFile()["WordDocument"]
	shared["ObjectPool/_4/\x01Ole"] = []byte{1, 0, 0, 2, 1, 0, 0, 0}
	f.Add(msbintest.Compound(shared))
	// A workbook, with macros beside it.
	workbook := msbintest.VBAStreams("_VBA_PROJECT_CUR/", 1252, msbintest.VBAModule{Name: "M", Code: "Sub A()\r\nEnd Sub\r\n"})
	workbook["Workbook"] = msbintest.Workbook(append(append([][]byte(nil), styles...), msbintest.SST(msbintest.Plain("a", "b")...)),
		msbintest.Sheet{Name: "S", Records: [][]byte{msbintest.LabelSST(0, 0, xfGeneral, 0), msbintest.MulRK(0, 1, xfTwo, 1, 2), msbintest.Comment(0, 0, 1, "A", "note")}},
		msbintest.Sheet{Name: "T", State: 2, Records: [][]byte{msbintest.Number(0, 0, xfDate, 36526)}})
	f.Add(msbintest.Compound(workbook))
	// A Word document, and one of Word 95.
	f.Add(msbintest.Compound(msbintest.WordStreams(msbintest.DocFile{Styles: docStyles, Main: []msbintest.DocPara{
		{Text: "A heading", Style: 1}, {Text: "Text \x13 PAGE \x14 7\x15 and a note.\x02"}}, Footnotes: [][]msbintest.DocPara{{{Text: "\x02 note"}}}})))
	f.Add(msbintest.Compound(map[string][]byte{"WordDocument": msbintest.Word6Stream(0x0419, "\xcf\xf0\x02\ra\x07b\x07\x07\x13 PAGE \x14"+"7\x15\r", "\x02 n")}))
	// A workbook of Excel 95.
	f.Add(msbintest.Compound(map[string][]byte{"Book": msbintest.Workbook5(
		[][]byte{msbintest.CodePage(1251), msbintest.Font5("Arial"), msbintest.Format5(164, "0.0"), msbintest.XF5(0, 0), msbintest.XF5(0, 164)},
		msbintest.Sheet{Name: "S", Records: [][]byte{msbintest.Label5(0, 0, 0, "\xcf\xf0"), msbintest.Number(0, 1, 1, 2.25), msbintest.Note5(0, 0, "note"),
			msbintest.Formula5String(1, 0, 0, "x", msbintest.Cat(msbintest.PtgRef5(0, 0, true, true), msbintest.PtgStr5("x"), msbintest.PtgOp(8))),
			msbintest.FormulaTokens(1, 1, 0, 9, msbintest.Cat(msbintest.PtgArea5(0, 1, 0, 0), msbintest.PtgAttrSum(), msbintest.PtgRef3d5(0, 0, 4, 2), msbintest.PtgOp(3)))}})}))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		// An input that takes seconds is a bug: crash the worker, so that
		// the fuzzer keeps the input.
		timer := time.AfterFunc(5*time.Second, func() { panic("Extract took more than 5s") })
		defer timer.Stop()
		doc, err := extract(data, nil)
		checkOutcome(t, doc, err)
		// With a child extractor the embedded files are read too, each
		// by this library where it is of these formats.
		var seen []string
		doc, err = extract(data, &gofastcore.Options{Child: self(&seen), Images: gofastcore.ImagesData, Spans: true, Cells: true})
		checkOutcome(t, doc, err)
	})
}

// FuzzWorkbook feeds arbitrary bytes to the Excel reader as a workbook
// stream, so that the fuzzer works on the records and not on the
// container around them.
func FuzzWorkbook(f *testing.F) {
	sst := msbintest.SST(msbintest.SSTString{Text: "one"}, msbintest.SSTString{Text: "two ✓", Runs: [][2]uint16{{1, 5}}},
		msbintest.SSTString{Text: "three", Phonetic: []byte{1, 2, 3}})
	recs := [][]byte{msbintest.Dimensions(3, 3), msbintest.Row(0, true), msbintest.ColInfo(1, 2, true),
		msbintest.LabelSST(0, 0, xfGeneral, 0), msbintest.LabelSST(0, 1, xfSymbol, 1), msbintest.MulRK(1, 0, xfTwo, 1, 2),
		msbintest.Number(1, 2, xfDate, 36526.5), msbintest.Bool(1, 3, xfGeneral, true), msbintest.Error(1, 4, xfGeneral, 7),
		msbintest.FormulaString(2, 0, xfQuoted, "text"), msbintest.Label(2, 1, xfWingdings, "abc"),
		msbintest.MergeCells([4]uint16{0, 1, 0, 0}), msbintest.HLinkURL([4]uint16{0, 0, 0, 0}, "https://example.com", "x"),
		msbintest.HLinkFile([4]uint16{1, 1, 0, 0}, 1, `a\b.xls`), msbintest.HLinkPlace([4]uint16{2, 2, 0, 0}, "S!A1"),
		msbintest.Comment(0, 0, 1, "A", "note")}
	globals := append(append([][]byte(nil), styles...), sst)
	f.Add(msbintest.Workbook(globals, msbintest.Sheet{Name: "S", Records: recs}, msbintest.Sheet{Name: "T", State: 1, Records: recs},
		msbintest.Sheet{Name: "C", Kind: 2}, msbintest.Sheet{Name: "M", Kind: 1, Records: recs}))
	f.Add(msbintest.Workbook(nil))
	// Formulas, names, links, lists, shapes, pictures and charts.
	m := msbintest.Cat
	links := [][]byte{msbintest.SupBookSelf(2), msbintest.SupBookFile("\x01C:\\other.xls", "Data"), msbintest.ExternName("Rate", 0),
		msbintest.ExternSheet([3]uint16{0, 0, 0}, [3]uint16{1, 0, 0}), msbintest.Name("Total", 0, -1, msbintest.PtgArea3d(0, 0, 9, 0, 0)),
		msbintest.Name("", 1, 6, msbintest.PtgArea3d(0, 0, 4, 0, 2)), msbintest.DrawingGroup(msbintest.PNG(2, 2))}
	chart := msbintest.Chart([][]byte{
		msbintest.ChartSeries("S", msbintest.PtgArea3d(0, 0, 2, 1, 1), msbintest.PtgArea3d(0, 0, 2, 0, 0)),
		msbintest.ChartType(0x1017, msbintest.U16(0, 150, 0), true), msbintest.ChartTitle("T", 1),
	}, msbintest.ChartCache(1, msbintest.Number(0, 0, 0, 1.5)), msbintest.ChartCache(2, msbintest.Label(0, 0, 0, "a")))
	rich := [][]byte{
		msbintest.MulRK(0, 0, xfGeneral, 1, 2), msbintest.MulRK(1, 0, xfGeneral, 3, 4),
		msbintest.FormulaTokens(2, 0, xfGeneral, 3, m(msbintest.PtgRef3d(0, 0, 0), msbintest.PtgName(1), msbintest.PtgOp(3), msbintest.PtgNameX(1, 1), msbintest.PtgOp(3))),
		msbintest.FormulaTokens(3, 0, xfGeneral, 3, msbintest.PtgExp(3, 0)),
		msbintest.ShrFmla(3, 4, 0, 0, m(msbintest.PtgRefN(-1, 0, true, true), msbintest.PtgInt(2), msbintest.PtgOp(5))),
		msbintest.FormulaTokens(4, 0, xfGeneral, 3, msbintest.PtgExp(3, 0)),
		msbintest.FormulaTokens(5, 0, xfGeneral, 3, msbintest.PtgExp(5, 0)),
		msbintest.ArrayFormula(5, 5, 0, 0, m(msbintest.PtgArea(0, 1, 0, 0), msbintest.PtgArray(), msbintest.PtgOp(5)),
			m([]byte{0}, msbintest.U16(0), []byte{2}, msbintest.Str("x"))...),
		msbintest.DVList([][4]uint16{{0, 1, 0, 0}}, []string{"a", "b"}, nil, false),
		msbintest.DVList([][4]uint16{{2, 2, 0, 0}}, nil, msbintest.PtgArea(0, 9, 5, 5), true),
		msbintest.MsoDrawing(msbintest.ArtShape(1, 5, 1)), msbintest.Obj(6, 1), msbintest.TextObject("box"),
		msbintest.MsoDrawing(msbintest.ArtShape(2, 5, 5, msbintest.ArtProp{ID: 0x0104, Value: 1}, msbintest.ArtProp{ID: 0x0381, Text: "alt"})), msbintest.Obj(8, 2),
		msbintest.MsoDrawing(msbintest.ArtShape(3, 5, 9)), msbintest.Obj(5, 3), chart,
	}
	f.Add(msbintest.Workbook(append(append([][]byte(nil), styles...), links...), msbintest.Sheet{Name: "S", Records: rich},
		msbintest.Sheet{Name: "C", Kind: 2, Records: [][]byte{chart[len(msbintest.BOF(0x20)) : len(chart)-4]}},
		msbintest.Sheet{Name: "M", Kind: 1, Records: rich[2:5]}))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		timer := time.AfterFunc(5*time.Second, func() { panic("Extract took more than 5s") })
		defer timer.Stop()
		file := msbintest.BookFile(data)
		doc, err := extract(file, &gofastcore.Options{Spans: true, Cells: true, Images: gofastcore.ImagesData})
		checkOutcome(t, doc, err)
		// The same document whatever the number of sheets read at a
		// time, also where a limit cuts it.
		for _, limits := range []gofastcore.Limits{{}, {MaxTextBytes: 300}} {
			one, err1 := extract(file, &gofastcore.Options{Workers: 1, Limits: limits})
			many, err2 := extract(file, &gofastcore.Options{Workers: 4, Limits: limits})
			checkOutcome(t, one, err1)
			if (err1 == nil) != (err2 == nil) {
				t.Fatalf("one worker: %v; four: %v", err1, err2)
			}
			if err1 == nil && one.Text() != many.Text() {
				t.Fatal("four workers give another text than one")
			}
		}
	})
}

// FuzzWord feeds arbitrary bytes to the Word reader as the two streams of
// a document, so that the fuzzer works on the structures and not on the
// container around them.
func FuzzWord(f *testing.F) {
	in := msbintest.Sprm(0x2416, 1)
	del := msbintest.Cat(msbintest.Sprm(0x0800, 1), msbintest.Sprm(0x4863, 0, 0))
	streams := msbintest.WordStreams(msbintest.DocFile{Styles: docStyles, Main: []msbintest.DocPara{
		{Text: "A heading", Style: 1},
		{Text: "Text \x13 HYPERLINK \"https://example.com\" \x14a link\x15 and \x13 PAGE \x14 7\x15 and a note.\x02",
			Runs: []msbintest.DocRun{{Len: 5, Chpx: msbintest.Sprm(0x0835, 1)}, {Len: 40, Chpx: msbintest.Sprm(0x083C, 1)}, {Len: 23, Chpx: del}}},
		{Text: "a", Mark: 7, Papx: in}, {Text: "b", Mark: 7, Papx: in}, {Text: "", Mark: 7, Papx: msbintest.Cat(in, msbintest.Sprm(0x2417, 1))},
		{Text: "code", Style: 5}, {Text: "styled", Runs: []msbintest.DocRun{{Len: 6, Chpx: msbintest.Sprm(0x4A30, 6, 0)}}},
	}, Footnotes: [][]msbintest.DocPara{{{Text: "\x02 note"}}}})
	f.Add(streams["WordDocument"], streams["1Table"])
	narrow := msbintest.WordStreams(msbintest.DocFile{Styles: docStyles, Narrow: true, Main: []msbintest.DocPara{{Text: "one byte each", Style: 2}}})
	f.Add(narrow["WordDocument"], narrow["1Table"])
	f.Fuzz(func(t *testing.T, word, table []byte) {
		if len(word)+len(table) > 1<<20 {
			return
		}
		timer := time.AfterFunc(5*time.Second, func() { panic("Extract took more than 5s") })
		defer timer.Stop()
		file := msbintest.Compound(map[string][]byte{"WordDocument": word, "1Table": table, "0Table": table, "Data": table})
		doc, err := extract(file, &gofastcore.Options{Spans: true, Images: gofastcore.ImagesData})
		checkOutcome(t, doc, err)
		plain, err2 := extract(file, &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 200}})
		checkOutcome(t, plain, err2)
	})
}
