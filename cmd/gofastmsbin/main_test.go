package main

import (
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
	for _, e := range entries {
		sameJSON(t, e.Path, gofastcore.Options{}, false)
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
