package gofastmsbin_test

import (
	"context"
	"encoding/binary"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// wordFile is the streams of a Word file of one paragraph.
func wordFile() map[string][]byte {
	return msbintest.WordStreams(msbintest.DocFile{Styles: docStyles, Main: []msbintest.DocPara{{Text: "A paragraph."}}})
}

// bookFile is the streams of a workbook with nothing in it.
func bookFile() map[string][]byte {
	return map[string][]byte{"Workbook": record(0x0809, u16s(0x0600, 0x0005, 0, 0, 0, 0, 0, 0)...)}
}

func with(base map[string][]byte, more map[string][]byte) map[string][]byte {
	for k, v := range more {
		base[k] = v
	}
	return base
}

func warning(ws []gofastcore.Warning, code string) (gofastcore.Warning, bool) {
	for _, w := range ws {
		if w.Code == code {
			return w, true
		}
	}
	return gofastcore.Warning{}, false
}

func mustExtract(t *testing.T, data []byte, opts *gofastcore.Options) *gofastcore.Document {
	t.Helper()
	doc, err := extract(data, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestMetadata(t *testing.T) {
	created := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	saved := time.Date(2003, 12, 31, 23, 59, 59, 0, time.UTC)
	summary := msbintest.PropertyStream(msbintest.Section{Format: msbintest.FmtSummary, CodePage: 1252, Props: []msbintest.Prop{
		{ID: 2, Value: "Budget  \x01report"},
		{ID: 3, Value: "Caf\xe9s"},
		{ID: 4, Value: "A. Author; B. Author"},
		{ID: 5, Value: "money, plans"},
		{ID: 6, Value: "A comment\r\nin two lines"},
		{ID: 7, Value: "Normal.dot"},
		{ID: 8, Value: "Last Person"},
		{ID: 9, Value: "42"},
		{ID: 10, Value: time.Time{}}, // editing time: not a date
		{ID: 12, Value: created},
		{ID: 13, Value: saved},
		{ID: 14, Value: int32(7)},
		{ID: 17, Value: msbintest.Raw{0x47, 0, 0, 0, 4, 0, 0, 0, 1, 2, 3, 4}}, // a thumbnail
		{ID: 18, Value: "Microsoft Word 9.0"},
	}})
	docSummary := msbintest.PropertyStream(
		msbintest.Section{Format: msbintest.FmtDocSummary, CodePage: 1252, Props: []msbintest.Prop{
			{ID: 2, Value: "Reports"},
			{ID: 14, Value: "The Manager"},
			{ID: 15, Value: "The Company"},
			{ID: 23, Value: int32(9<<16 | 2720)},
			{ID: 13, Value: msbintest.Strings{"Title", "Another"}},
		}},
		msbintest.Section{Format: msbintest.FmtUser, CodePage: 1252, Props: []msbintest.Prop{
			{ID: 2, Name: "Checked By", Value: "Someone"},
			{ID: 3, Name: "Copies", Value: int32(3)},
			{ID: 4, Name: "Final", Value: true},
			{ID: 5, Name: "Due", Value: created},
			{ID: 6, Name: "Ratio", Value: 1.5},
			{ID: 7, Name: "Wide", Value: msbintest.Wide("ŁĄ")},
			{ID: 0x01000002, Value: "bookmark"}, // what property 2 is linked to
		}},
	)
	doc := mustExtract(t, msbintest.Compound(with(wordFile(), map[string][]byte{
		"\x05SummaryInformation": summary, "\x05DocumentSummaryInformation": docSummary})), nil)
	m := doc.Metadata
	if m.Title != "Budget  report" || m.Subject != "Cafés" || len(m.Authors) != 1 || m.Authors[0] != "A. Author; B. Author" ||
		len(m.Keywords) != 1 || m.Keywords[0] != "money, plans" || m.Application != "Microsoft Word 9.0" ||
		!m.Created.Equal(created) || !m.Modified.Equal(saved) {
		t.Errorf("%+v", m)
	}
	for key, want := range map[string]string{
		"description": "A comment\nin two lines", "template": "Normal.dot", "last_modified_by": "Last Person", "revision": "42",
		"category": "Reports", "manager": "The Manager", "company": "The Company", "app_version": "9.2720",
		"custom.checked by": "Someone", "custom.copies": "3", "custom.final": "true", "custom.due": "2001-02-03T04:05:06Z",
		"custom.ratio": "1.5", "custom.wide": "ŁĄ",
	} {
		if m.Extra[key] != want {
			t.Errorf("%s: %q, want %q", key, m.Extra[key], want)
		}
	}
	if len(m.Extra) != 14 {
		t.Errorf("extra: %v", m.Extra)
	}
	if w, ok := warning(doc.Warnings, gofastmsbin.WarnControlCharsRemoved); !ok || w.Message != "1 in document properties" {
		t.Errorf("warnings: %+v", doc.Warnings)
	}
	if doc.Source.Format != "doc" || doc.Source.MIME != "application/msword" {
		t.Errorf("%+v", doc.Source)
	}
}

// Text of the properties is in the code page the set names.
func TestMetadataCodePages(t *testing.T) {
	for _, c := range []struct {
		cp          int
		title, want string
	}{
		{1251, "\xcf\xf0\xe8\xe2\xe5\xf2", "Привет"},
		{932, "\x93\xfa\x96\x7b", "日本"},
		{65001, "Zażółć", "Zażółć"},
		{10000, "\x8e", "é"},
	} {
		s := msbintest.PropertyStream(msbintest.Section{Format: msbintest.FmtSummary, CodePage: c.cp, Props: []msbintest.Prop{{ID: 2, Value: c.title}}})
		doc := mustExtract(t, msbintest.Compound(with(bookFile(), map[string][]byte{"\x05SummaryInformation": s})), nil)
		if doc.Metadata.Title != c.want {
			t.Errorf("code page %d: %q", c.cp, doc.Metadata.Title)
		}
	}
	// A code page with no table: what is not ASCII is U+FFFD, and counted.
	s := msbintest.PropertyStream(msbintest.Section{Format: msbintest.FmtSummary, CodePage: 1361, Props: []msbintest.Prop{{ID: 2, Value: "a\xcf\xf0"}}})
	doc := mustExtract(t, msbintest.Compound(with(bookFile(), map[string][]byte{"\x05SummaryInformation": s})), nil)
	if w, ok := warning(doc.Warnings, gofastcore.WarnTextUnmappedGlyphs); doc.Metadata.Title != "a\uFFFD\uFFFD" || !ok || !strings.HasPrefix(w.Message, "2 characters") {
		t.Errorf("%q, %+v", doc.Metadata.Title, doc.Warnings)
	}
}

// A damaged property stream gives what could be read of it, and a warning;
// it never costs the document.
func TestMetadataDamaged(t *testing.T) {
	good := msbintest.PropertyStream(msbintest.Section{Format: msbintest.FmtSummary, CodePage: 1252, Props: []msbintest.Prop{
		{ID: 2, Value: "The title"}, {ID: 4, Value: "The author"}}})
	doc := mustExtract(t, msbintest.Compound(with(wordFile(), map[string][]byte{"\x05SummaryInformation": good[:len(good)-6]})), nil)
	if w, ok := warning(doc.Warnings, gofastmsbin.WarnPropertiesDamaged); !ok || w.Message != "SummaryInformation" || doc.Metadata.Title != "The title" {
		t.Errorf("%+v, %+v", doc.Metadata, doc.Warnings)
	}
	// Every cut and every changed byte gives a valid document.
	for i := 0; i < len(good); i++ {
		for _, b := range []byte{0, 0xFF, good[i] + 1} {
			bad := append([]byte(nil), good...)
			bad[i] = b
			mustExtract(t, msbintest.Compound(with(wordFile(), map[string][]byte{"\x05SummaryInformation": bad})), nil)
		}
		mustExtract(t, msbintest.Compound(with(wordFile(), map[string][]byte{"\x05SummaryInformation": good[:i]})), nil)
	}
	// More custom properties than are kept, and a value longer than is kept.
	var props []msbintest.Prop
	for i := 0; i < 300; i++ {
		props = append(props, msbintest.Prop{ID: uint32(i + 2), Name: "p" + strings.Repeat("x", i), Value: int32(i)})
	}
	s := msbintest.PropertyStream(
		msbintest.Section{Format: msbintest.FmtDocSummary, CodePage: 1252, Props: []msbintest.Prop{{ID: 15, Value: strings.Repeat("c", 70000)}}},
		msbintest.Section{Format: msbintest.FmtUser, CodePage: 1252, Props: props})
	doc = mustExtract(t, msbintest.Compound(with(wordFile(), map[string][]byte{"\x05DocumentSummaryInformation": s})), nil)
	if n := len(doc.Metadata.Extra); n != 257 || len(doc.Metadata.Extra["company"]) != 64<<10 {
		t.Errorf("%d properties, company of %d bytes", n, len(doc.Metadata.Extra["company"]))
	}
	var said []string
	for _, w := range doc.Warnings {
		if w.Code == gofastcore.WarnLimitExceeded && w.Severity == gofastcore.SeverityError {
			said = append(said, w.Message)
		}
	}
	want := []string{
		"document property values longer than 65536 bytes were cut there (1 value, 4464 bytes left out)",
		"custom document properties past 256 were not read (44 properties)",
	}
	if strings.Join(said, "|") != strings.Join(want, "|") {
		t.Errorf("limits: %q", said)
	}
}

func TestMacros(t *testing.T) {
	code := "Attribute VB_Name = \"Module1\"\r\nSub Hello()\r\n    MsgBox \"Caf\xe9\"\r\nEnd Sub\r\n"
	for name, c := range map[string]struct {
		base   map[string][]byte
		prefix string
	}{
		"word":  {wordFile(), "Macros/"},
		"excel": {bookFile(), "_VBA_PROJECT_CUR/"},
		"other": {bookFile(), "Somewhere/"},
	} {
		streams := with(c.base, msbintest.VBAStreams(c.prefix, 1252,
			msbintest.VBAModule{Name: "Module1", Code: code}, msbintest.VBAModule{Name: "Empty", Code: ""},
			msbintest.VBAModule{Name: "ThisDocument", Code: "' nothing\r\n"}))
		doc := mustExtract(t, msbintest.Compound(streams), nil)
		if w, ok := warning(doc.Warnings, gofastcore.WarnInputActiveContent); !ok || w.Severity != gofastcore.SeverityInfo {
			t.Errorf("%s: %+v", name, doc.Warnings)
		}
		if len(doc.Parts) != 2 || doc.Parts[1].Kind != gofastcore.PartStored || doc.Parts[1].Number != 2 {
			t.Fatalf("%s: %d parts", name, len(doc.Parts))
		}
		blocks := doc.Parts[1].Blocks
		if len(blocks) != 2 {
			t.Fatalf("%s: %d blocks", name, len(blocks))
		}
		b := blocks[0]
		if b.Kind != gofastcore.KindCode || b.Text != "Attribute VB_Name = \"Module1\"\nSub Hello()\n    MsgBox \"Café\"\nEnd Sub" ||
			b.Attrs[gofastcore.AttrStored] != gofastcore.StoredMacro || b.Attrs[gofastcore.AttrName] != "Module1" || b.Attrs[gofastcore.AttrLanguage] != "vba" {
			t.Errorf("%s: %+v", name, b)
		}
		if blocks[1].Attrs[gofastcore.AttrName] != "ThisDocument" {
			t.Errorf("%s: %+v", name, blocks[1])
		}
		// The stored part comes with the whole document only.
		doc = mustExtract(t, msbintest.Compound(streams), &gofastcore.Options{Parts: gofastcore.PartRange{First: 1, Last: 1}})
		if len(doc.Parts) != 1 || doc.Parts[0].Kind == gofastcore.PartStored {
			t.Errorf("%s: a range of parts gives %d parts", name, len(doc.Parts))
		}
		// The limit on text cuts the code, and says so.
		doc = mustExtract(t, msbintest.Compound(streams), &gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: 40}})
		if w, ok := warning(doc.Parts[1].Warnings, gofastcore.WarnLimitExceeded); !ok || w.Severity != gofastcore.SeverityError {
			t.Errorf("%s: limit: %+v", name, doc.Parts[1].Warnings)
		}
	}
	// A damaged project is named, and the document is read.
	streams := with(wordFile(), msbintest.VBAStreams("Macros/", 1252, msbintest.VBAModule{Name: "M", Code: code}))
	streams["Macros/VBA/dir"] = []byte{9, 9, 9}
	doc := mustExtract(t, msbintest.Compound(streams), nil)
	if w, ok := warning(doc.Warnings, gofastmsbin.WarnPartDamaged); !ok || w.Message != "1 stream could not be read to its end (Macros/VBA)" {
		t.Errorf("%+v", doc.Warnings)
	}
}

// packaged is an "\x01Ole10Native" stream as the object packager writes
// it: the file, with its name and where it came from.
func packaged(label, path string, data []byte) []byte {
	le := binary.LittleEndian
	b := []byte{2, 0}
	b = append(append(b, label...), 0)
	b = append(append(b, path...), 0)
	b = append(b, 0, 0, 3, 0)
	b = le.AppendUint32(b, uint32(len(path)+1))
	b = append(append(b, path...), 0)
	b = le.AppendUint32(b, uint32(len(data)))
	b = append(b, data...)
	return append(le.AppendUint32(nil, uint32(len(b))), b...)
}

// self extracts embedded files of the three formats with this library,
// and says of any other what it was given.
func self(seen *[]string) gofastcore.ChildFunc {
	var child gofastcore.ChildFunc
	child = func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, opts *gofastcore.Options) (*gofastcore.Document, error) {
		*seen = append(*seen, name)
		o := *opts
		o.Child = child
		doc, err := gofastmsbin.Extract(ctx, r, size, &o)
		if err == nil {
			return doc, nil
		}
		data := make([]byte, size)
		r.ReadAt(data, 0)
		doc = gofastcore.New("txt")
		doc.Parts = []gofastcore.Part{{Kind: gofastcore.PartBody, Number: 1, Blocks: []gofastcore.Block{{Kind: gofastcore.KindParagraph, Text: strconv.Quote(string(data))}}}}
		return doc, nil
	}
	return child
}

func TestEmbeddedObjects(t *testing.T) {
	inner := msbintest.PropertyStream(msbintest.Section{Format: msbintest.FmtSummary, CodePage: 1252, Props: []msbintest.Prop{{ID: 2, Value: "The inner workbook"}}})
	link := make([]byte, 20)
	binary.LittleEndian.PutUint32(link, 0x02000001)
	binary.LittleEndian.PutUint32(link[4:], 1)
	embedded := make([]byte, 20)
	binary.LittleEndian.PutUint32(embedded, 0x02000001)
	streams := with(wordFile(), map[string][]byte{
		"ObjectPool/_1000000001/\x01Ole10Native":          packaged("notes.txt", `C:\Users\someone\notes.txt`, []byte("the text of the notes")),
		"ObjectPool/_1000000001/\x01CompObj":              {1, 2, 3},
		"ObjectPool/_1000000002/Package":                  []byte("PK\x03\x04 a package"),
		"ObjectPool/_1000000003/Workbook":                 bookFile()["Workbook"],
		"ObjectPool/_1000000003/\x05SummaryInformation":   inner,
		"ObjectPool/_1000000003/\x01Ole":                  embedded,
		"ObjectPool/_1000000003/MBD00000001/WordDocument": wordFile()["WordDocument"],
		"ObjectPool/_1000000003/MBD00000001/1Table":       {},
		"ObjectPool/_1000000004/\x01Ole":                  link,
		"ObjectPool/_1000000005/CONTENTS":                 []byte("%PDF-1.4 a document"),
		"ObjectPool/_1000000006/\x01Ole10Native":          append([]byte{5, 0, 0, 0}, "raw!!"...),
		"ObjectPool/_1000000007/Equation Native":          {1, 2, 3, 4},
	})
	data := msbintest.Compound(streams)

	// With no child extractor every object is listed.
	doc := mustExtract(t, data, nil)
	var names []string
	for _, c := range doc.Children {
		if _, ok := warning(c.Warnings, gofastcore.WarnChildNotExtracted); !ok || len(c.Parts) != 0 {
			t.Errorf("%s: %+v", c.Source.Name, c.Warnings)
		}
		names = append(names, c.Source.Name)
	}
	if got := strings.Join(names, " "); got != "_1000000001 _1000000002 _1000000003.xls _1000000005 _1000000006 _1000000007" {
		t.Errorf("listed: %s", got)
	}
	if w, ok := warning(doc.Warnings, gofastcore.WarnInputExternalReference); !ok || !strings.HasPrefix(w.Message, "1 link ") {
		t.Errorf("%+v", doc.Warnings)
	}

	// With one, each is handed on as a file.
	var seen []string
	doc = mustExtract(t, data, &gofastcore.Options{Child: self(&seen)})
	if got := strings.Join(seen, " "); got != "notes.txt _1000000002 _1000000003.xls MBD00000001.doc _1000000005 _1000000006 _1000000007" {
		t.Errorf("handed on: %s", got)
	}
	if len(doc.Children) != 6 {
		t.Fatalf("%d children", len(doc.Children))
	}
	text := func(d *gofastcore.Document) string {
		if len(d.Parts) == 0 || len(d.Parts[0].Blocks) == 0 {
			return ""
		}
		return d.Parts[0].Blocks[0].Text
	}
	if got := text(doc.Children[0]); got != `"the text of the notes"` || doc.Children[0].Source.Name != "notes.txt" {
		t.Errorf("packaged file: %q", got)
	}
	if got := text(doc.Children[1]); got != `"PK\x03\x04 a package"` {
		t.Errorf("package: %q", got)
	}
	book := doc.Children[2]
	if book.Source.Format != "xls" || book.Metadata.Title != "The inner workbook" || book.Source.MIME != "application/vnd.ms-excel" {
		t.Errorf("workbook: %+v %+v", book.Source, book.Metadata)
	}
	if len(book.Children) != 1 || book.Children[0].Source.Format != "doc" {
		t.Errorf("the workbook's own object: %+v", book.Children)
	}
	if got := text(doc.Children[3]); got != `"%PDF-1.4 a document"` {
		t.Errorf("contents: %q", got)
	}
	if got := text(doc.Children[4]); got != `"raw!!"` {
		t.Errorf("native data: %q", got)
	}

	// The limit on decoded bytes is the document's and its objects'
	// together: the objects past it are listed, and the document says so.
	seen = nil
	doc = mustExtract(t, data, &gofastcore.Options{Child: self(&seen), Limits: gofastcore.Limits{MaxDecodedBytes: 150}})
	if len(doc.Children) != 6 || len(seen) != 2 {
		t.Errorf("limit: %d children, handed on %v", len(doc.Children), seen)
	}
	if _, ok := warning(doc.Children[5].Warnings, gofastcore.WarnChildNotExtracted); !ok {
		t.Errorf("limit: %+v", doc.Children[5].Warnings)
	}
	found := false
	for _, w := range doc.Warnings {
		found = found || w.Code == gofastcore.WarnLimitExceeded && w.Severity == gofastcore.SeverityError && strings.Contains(w.Message, "embedded files")
	}
	if !found {
		t.Errorf("limit: %+v", doc.Warnings)
	}

	// Nesting stops at the depth limit.
	seen = nil
	doc = mustExtract(t, data, &gofastcore.Options{Child: self(&seen), Limits: gofastcore.Limits{MaxDepth: 1}})
	if c := doc.Children[2]; len(c.Children) != 1 || len(c.Children[0].Parts) != 0 {
		t.Errorf("depth: %+v", c.Children)
	}
}

// A workbook keeps its objects at its root.
func TestEmbeddedObjectsOfAWorkbook(t *testing.T) {
	streams := with(bookFile(), map[string][]byte{
		"MBD0001A2B3/WordDocument": wordFile()["WordDocument"],
		"MBD0001A2B3/1Table":       {},
		"LNK00000001/\x01Ole":      make([]byte, 20),
		"MBDsomething/else":        {1},
	})
	var seen []string
	doc := mustExtract(t, msbintest.Compound(streams), &gofastcore.Options{Child: self(&seen)})
	if len(doc.Children) != 1 || doc.Children[0].Source.Format != "doc" || strings.Join(seen, " ") != "MBD0001A2B3.doc" {
		t.Errorf("%v, %d children", seen, len(doc.Children))
	}
	if _, ok := warning(doc.Warnings, gofastcore.WarnInputExternalReference); !ok {
		t.Errorf("%+v", doc.Warnings)
	}
}

// The same file read through File a part at a time gives what Extract
// gives.
func TestFileAPartAtATime(t *testing.T) {
	streams := with(wordFile(), msbintest.VBAStreams("Macros/", 1252, msbintest.VBAModule{Name: "M", Code: "Sub A()\r\nEnd Sub\r\n"}))
	streams["ObjectPool/_1/Package"] = []byte("PK\x03\x04")
	data := msbintest.Compound(streams)
	whole := mustExtract(t, data, nil)
	f, err := gofastmsbin.OpenBytes(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Format() != "doc" || f.NumParts() != 2 {
		t.Fatalf("%s, %d parts", f.Format(), f.NumParts())
	}
	n := 0
	for part, err := range f.Parts(context.Background()) {
		if err != nil {
			t.Fatal(err)
		}
		if part.Kind != whole.Parts[n].Kind || len(part.Blocks) != len(whole.Parts[n].Blocks) {
			t.Errorf("part %d differs", n+1)
		}
		n++
	}
	children, err := f.Children(context.Background())
	if n != 2 || err != nil || len(children) != 1 {
		t.Errorf("%d parts, %d children, %v", n, len(children), err)
	}
	if len(f.Warnings()) != len(whole.Warnings) {
		t.Errorf("warnings: %+v, want %+v", f.Warnings(), whole.Warnings)
	}
	if _, err := f.Part(context.Background(), 3); err == nil {
		t.Error("part 3 of 2 is given")
	}
}
