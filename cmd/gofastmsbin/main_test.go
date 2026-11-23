package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// whole is the JSON of the document read whole.
func whole(t testing.TB, e *gofastmsbin.Extractor, path string, o *gofastcore.Options, indent bool) ([]byte, error) {
	doc, err := e.ExtractFile(context.Background(), path, o)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(doc); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), nil
}

// streamed is the JSON the command writes a part at a time.
func streamed(e *gofastmsbin.Extractor, path string, o *gofastcore.Options, indent bool) ([]byte, error) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	_, _, err := streamFile(context.Background(), e, path, o, w, indent)
	w.Flush()
	return buf.Bytes(), err
}

// sameJSON checks one file under one set of options: the same bytes, or
// the same error and nothing written.
func sameJSON(t *testing.T, path string, o gofastcore.Options, indent bool) bool {
	t.Helper()
	e := &gofastmsbin.Extractor{}
	o.Child = func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, co *gofastcore.Options) (*gofastcore.Document, error) {
		return e.Extract(ctx, r, size, co)
	}
	o.Name = filepath.Base(path)
	a, errA := whole(t, e, path, &o, indent)
	b, errB := streamed(e, path, &o, indent)
	switch {
	case (errA == nil) != (errB == nil):
		t.Errorf("%s %+v: read whole: %v; a part at a time: %v", path, o.Limits, errA, errB)
		return false
	case errA != nil:
		if errA.Error() != errB.Error() || len(b) > 0 {
			t.Errorf("%s: errors %q and %q, %d bytes written", path, errA, errB, len(b))
			return false
		}
		return true
	case !bytes.Equal(a, b):
		i := 0
		for i < len(a) && i < len(b) && a[i] == b[i] {
			i++
		}
		from := max(i-80, 0)
		t.Errorf("%s (workers %d, limits %+v, indent %v): differ at byte %d of %d and %d:\nwhole:    %q\nstreamed: %q",
			path, o.Workers, o.Limits, indent, i, len(a), len(b), a[from:min(i+80, len(a))], b[from:min(i+80, len(b))])
		return false
	}
	return true
}

// sample writes a Word file with properties, macros and embedded files,
// and returns its path.
func sample(t *testing.T, dir, name string) string {
	t.Helper()
	fib := make([]byte, 68)
	copy(fib, []byte{0xEC, 0xA5, 0xC1, 0x00})
	fib[11] = 0x02
	streams := msbintest.VBAStreams("Macros/", 1252, msbintest.VBAModule{Name: "Module1", Code: "Sub A()\r\n  MsgBox \"<b>\"\r\nEnd Sub\r\n"})
	streams["WordDocument"], streams["1Table"] = fib, []byte{}
	streams["\x05SummaryInformation"] = msbintest.PropertyStream(msbintest.Section{Format: msbintest.FmtSummary, CodePage: 1252,
		Props: []msbintest.Prop{{ID: 2, Value: "A title & more"}}})
	streams["ObjectPool/_1/WordDocument"], streams["ObjectPool/_1/1Table"] = fib, []byte{}
	streams["ObjectPool/_2/Package"] = []byte("PK\x03\x04")
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, msbintest.Compound(streams), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The JSON written a part at a time is, byte for byte, that of the
// document read whole, under every setting that changes what is read.
func TestStreamedJSONIsTheDocument(t *testing.T) {
	path := sample(t, t.TempDir(), "a.doc")
	for _, o := range []gofastcore.Options{
		{},
		{Workers: 1},
		{Parts: gofastcore.PartRange{First: 1, Last: 1}},
		{Parts: gofastcore.PartRange{First: 5, Last: 6}},
		{Limits: gofastcore.Limits{MaxTextBytes: 20}},
		{Limits: gofastcore.Limits{MaxDecodedBytes: 10}},
		{Limits: gofastcore.Limits{MaxDepth: 1}},
		{Limits: gofastcore.Limits{MaxParts: 1}},
	} {
		for _, indent := range []bool{false, true} {
			sameJSON(t, path, o, indent)
		}
	}
}

func TestStreamedJSONOverTheCorpus(t *testing.T) {
	entries, err := msbintest.Corpus(filepath.Join("..", "..", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Skip("the corpus is not downloaded")
	}
	if testing.Short() {
		entries = entries[:min(len(entries), 100)]
	}
	for i, e := range entries {
		// Indented or not, with the cells' details or without, with all
		// the workers or one: each file one way, all the ways over the
		// corpus.
		o := gofastcore.Options{Cells: i%3 == 1}
		if i%5 == 2 {
			o.Workers = 1
		}
		sameJSON(t, e.Path, o, i%2 == 1)
	}
}

// A part is written a block at a time and a table a row at a time, and
// is byte for byte what the encoder writes for it: whatever the cells
// hold, with rows and tables that are empty or absent, and with a text
// that has in it what marks the place of the blocks or of the rows.
func TestPartIsWrittenAsTheEncoderWritesIt(t *testing.T) {
	rows := [][]string{
		{"plain", "with \"quotes\"", `a\b`, "two\nlines", "a\ttab"},
		{"café", "日本語", "line\u2028separator", "paragraph\u2029separator", "<b>&amp;</b>"},
		{"not \xff UTF-8", "\x01control", "", "\"rows\": []", `"blocks":[]`},
		nil,
		{},
		{"one"},
	}
	parts := []gofastcore.Part{
		{Kind: gofastcore.PartSheet, Number: 1, Name: `"blocks": []`, Blocks: []gofastcore.Block{
			{Kind: gofastcore.KindParagraph, Text: "before\n      \"rows\": []\n    \"blocks\": []", Attrs: map[string]string{"rows": "[]"}},
			{Kind: gofastcore.KindTable, Table: &gofastcore.Table{Rows: rows, HeaderRows: 1, Merges: []gofastcore.Merge{{Row: 0, Col: 0, RowSpan: 1, ColSpan: 2}},
				Cells: []gofastcore.CellDetail{{Row: 0, Col: 1, Formula: "=A1"}}}, Loc: gofastcore.Location{Range: "A1:E6"}},
			{Kind: gofastcore.KindTable, Table: &gofastcore.Table{Rows: [][]string{}}},
			{Kind: gofastcore.KindTable, Table: &gofastcore.Table{}},
			{Kind: gofastcore.KindTable, Table: &gofastcore.Table{Rows: [][]string{{"x"}}}, Display: gofastcore.DisplayHidden},
		}, Warnings: []gofastcore.Warning{{Code: "part.no_text", Severity: gofastcore.SeverityInfo, Message: `"blocks": []`}}},
		{Kind: gofastcore.PartBody, Number: 2, Blocks: []gofastcore.Block{}},
		{Kind: gofastcore.PartBody, Number: 3},
		{Kind: gofastcore.PartSlide, Number: 4, Width: 720, Height: 540, Blocks: []gofastcore.Block{{Kind: gofastcore.KindHeading, Level: 1, Text: "A title"}}},
	}
	for _, indent := range []bool{false, true} {
		for depth := 0; depth < 4; depth++ {
			for i := range parts {
				var want bytes.Buffer
				enc := json.NewEncoder(&want)
				enc.SetEscapeHTML(false)
				if indent {
					enc.SetIndent(strings.Repeat("  ", depth), "  ")
				}
				if err := enc.Encode(&parts[i]); err != nil {
					t.Fatal(err)
				}
				var got bytes.Buffer
				w := bufio.NewWriter(&got)
				if err := newPartWriter(w, indent).part(&parts[i], depth); err != nil {
					t.Fatal(err)
				}
				w.Flush()
				if !bytes.Equal(got.Bytes(), bytes.TrimSuffix(want.Bytes(), []byte("\n"))) {
					t.Errorf("part %d, indent %v, depth %d:\n got %s\nwant %s", parts[i].Number, indent, depth, got.Bytes(), want.Bytes())
				}
			}
		}
	}
}

func TestCommand(t *testing.T) {
	dir := t.TempDir()
	a, b := sample(t, dir, "a.doc"), sample(t, dir, "b.dot")
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not an Office file"), 0o600)
	os.WriteFile(filepath.Join(dir, "~$a.doc"), []byte("a lock file"), 0o600)
	call := func(args ...string) (int, string, string) {
		var out, errs bytes.Buffer
		status := run(context.Background(), args, &out, &errs)
		return status, out.String(), errs.String()
	}
	// One file: indented JSON, a valid document.
	status, out, errs := call("-quiet", a)
	var doc gofastcore.Document
	if status != 0 || errs != "" || json.Unmarshal([]byte(out), &doc) != nil || doc.Validate() != nil || !strings.Contains(out, "\n  ") {
		t.Fatalf("status %d, %q, %.200s", status, errs, out)
	}
	if doc.Metadata.Title != "A title & more" || len(doc.Children) != 2 || doc.Children[0].Source.Format != "doc" || len(doc.Parts) != 2 {
		t.Errorf("%+v", doc.Metadata)
	}
	if !strings.Contains(out, `"A title & more"`) || !strings.Contains(out, `<b>`) {
		t.Error("HTML characters are escaped in the JSON")
	}
	// A directory: the Office files in it, each on a line.
	status, out, errs = call("-quiet", dir)
	if lines := strings.Count(out, "\n"); status != 0 || lines != 2 || errs != "" {
		t.Errorf("directory: status %d, %d lines, %q", status, lines, errs)
	}
	// Markdown and text.
	for _, format := range []string{"markdown", "text"} {
		status, out, _ = call("-format", format, "-stored", a, b)
		if status != 0 || !strings.Contains(out, "Sub A()") || !strings.Contains(out, "a.doc") {
			t.Errorf("%s: status %d, %q", format, status, out)
		}
	}
	// A file that is not one of the formats fails, and the others are read.
	status, out, errs = call(a, filepath.Join(dir, "notes.txt"))
	if status != 1 || strings.Count(out, "\n") != 1 || !strings.Contains(errs, "not a compound file") {
		t.Errorf("status %d, %q", status, errs)
	}
	for _, bad := range [][]string{{}, {"-format", "xml", a}, {"-parts", "0", a}, {"-images", "all", a}, {"-nonsense"}} {
		if status, _, _ := call(bad...); status != 2 {
			t.Errorf("%v: status %d", bad, status)
		}
	}
	if status, out, _ := call("-version"); status != 0 || !strings.HasPrefix(out, "gofastmsbin ") {
		t.Errorf("version: %d %q", status, out)
	}
	// -o writes to a file.
	dest := filepath.Join(dir, "out.json")
	if status, out, _ := call("-o", dest, a); status != 0 || out != "" {
		t.Errorf("-o: %d %q", status, out)
	}
	if data, err := os.ReadFile(dest); err != nil || json.Unmarshal(data, &doc) != nil {
		t.Errorf("-o: %v", err)
	}
}

// docx is a small Word document in the Office Open XML format, written
// here as the zip package it is.
func docx(t *testing.T, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`,
	} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The command reads the files embedded in a document: one of the binary
// formats with this library, an Office Open XML file with go-fast-ooxml,
// told apart by their first bytes; anything else is listed.
func TestCommandReadsEmbeddedFiles(t *testing.T) {
	inner := msbintest.WordStreams(msbintest.DocFile{Main: []msbintest.DocPara{{Text: "In the binary format."}}})
	streams := msbintest.WordStreams(msbintest.DocFile{Main: []msbintest.DocPara{{Text: "The outer document."}}})
	streams["ObjectPool/_1000000001/Package"] = docx(t, "In the newer format.")
	streams["ObjectPool/_1000000002/WordDocument"] = inner["WordDocument"]
	streams["ObjectPool/_1000000002/1Table"] = inner["1Table"]
	streams["ObjectPool/_1000000003/CONTENTS"] = []byte("%PDF-1.4 not an Office file")
	path := filepath.Join(t.TempDir(), "outer.doc")
	if err := os.WriteFile(path, msbintest.Compound(streams), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if status := run(context.Background(), []string{"-quiet", path}, &out, &errs); status != 0 {
		t.Fatalf("status %d: %s", status, errs.String())
	}
	var doc gofastcore.Document
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Children) != 3 {
		t.Fatalf("%d children", len(doc.Children))
	}
	text := func(d *gofastcore.Document) string {
		if len(d.Parts) == 0 || len(d.Parts[0].Blocks) == 0 {
			return ""
		}
		return d.Parts[0].Blocks[0].Text
	}
	if c := doc.Children[0]; c.Source.Format != "docx" || text(c) != "In the newer format." {
		t.Errorf("the Office Open XML file: %+v %q %+v", c.Source, text(c), c.Warnings)
	}
	if c := doc.Children[1]; c.Source.Format != "doc" || text(c) != "In the binary format." {
		t.Errorf("the binary file: %+v %q %+v", c.Source, text(c), c.Warnings)
	}
	if c := doc.Children[2]; len(c.Parts) != 0 || len(c.Warnings) == 0 || c.Warnings[0].Code != gofastcore.WarnChildUnsupported {
		t.Errorf("the other file: %+v %+v", c.Source, c.Warnings)
	}
}
