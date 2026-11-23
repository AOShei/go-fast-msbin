package gofastmsbin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// soakCeiling is the heap the soak may hold at any moment. A file that
// takes the library past it fails the test, before the machine notices.
const soakCeiling = 3 << 30

// TestSoak reads every file of the corpus, the hostile ones among them,
// under several sets of options. Whatever the file: a valid document or
// one of the shared errors; the same document however many sheets or
// slides are read at a time; no more text than the limit allows; no file
// that takes a minute; and a heap that stays under a ceiling.
func TestSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the whole corpus four times")
	}
	entries, err := msbintest.Corpus("testdata")
	if err != nil || len(entries) == 0 {
		t.Skip("no corpus: python3 testdata/fetch.py")
	}
	e := &gofastmsbin.Extractor{}
	child := func(ctx context.Context, name, mime string, r io.ReaderAt, size int64, o *gofastcore.Options) (*gofastcore.Document, error) {
		return e.Extract(ctx, r, size, o)
	}
	read := func(data []byte, o gofastcore.Options) (*gofastcore.Document, error) {
		o.Child = child
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return e.Extract(ctx, bytes.NewReader(data), int64(len(data)), &o)
	}
	const textLimit = 4096
	var files, documents, cut, encrypted int
	var slowest time.Duration
	var peak uint64
	var stats runtime.MemStats
	for i, entry := range entries {
		data, err := os.ReadFile(entry.Path)
		if err != nil {
			continue
		}
		files++
		start := time.Now()
		// One sheet or slide at a time, and eight: the same document.
		one, err1 := read(data, gofastcore.Options{Workers: 1, Spans: true, Cells: true})
		checkOutcome(t, one, err1)
		eight, err8 := read(data, gofastcore.Options{Workers: 8, Spans: true, Cells: true})
		checkOutcome(t, eight, err8)
		if (err1 == nil) != (err8 == nil) {
			t.Fatalf("file %d: with one worker %v, with eight %v", i, err1, err8)
		}
		if err1 == nil {
			documents++
			// The properties say a file was encrypted when it was, and
			// only then. No password is given here: what opens is what
			// Excel encrypted with its own.
			_, opened := warning(one.Warnings, gofastcore.WarnInputDecrypted)
			if one.Metadata.Encrypted != opened {
				t.Errorf("file %d: encrypted %v in its properties, decrypted %v", i, one.Metadata.Encrypted, opened)
			}
			if opened {
				encrypted++
			}
			a, _ := json.Marshal(one)
			b, _ := json.Marshal(eight)
			if !bytes.Equal(a, b) {
				t.Errorf("file %d: the document differs with the number of workers", i)
			}
		}
		// A small limit on the text: a valid document within it.
		small, err := read(data, gofastcore.Options{Limits: gofastcore.Limits{MaxTextBytes: textLimit}})
		checkOutcome(t, small, err)
		if small != nil {
			if n := textOf(small); n > textLimit {
				t.Errorf("file %d: %d bytes of text within a limit of %d", i, n, textLimit)
			}
			if truncated(small) {
				cut++
			}
		}
		// The bytes of the pictures, within a megabyte decoded.
		pictures, err := read(data, gofastcore.Options{Images: gofastcore.ImagesData, Limits: gofastcore.Limits{MaxDecodedBytes: 1 << 20}})
		checkOutcome(t, pictures, err)
		if took := time.Since(start); took > slowest {
			slowest = took
			if took > time.Minute {
				t.Errorf("file %d took %v", i, took)
			}
		}
		if i%25 == 0 {
			runtime.ReadMemStats(&stats)
			peak = max(peak, stats.HeapInuse)
			if stats.HeapInuse > soakCeiling {
				t.Fatalf("the heap is at %d MiB after file %d: over the ceiling of %d MiB", stats.HeapInuse>>20, i, soakCeiling>>20)
			}
		}
	}
	t.Logf("%d files, %d documents, each read four ways; %d encrypted with Excel's own password; %d cut by a limit of %d bytes of text; the slowest file took %v in all; the heap peaked at %d MiB",
		files, documents, encrypted, cut, textLimit, slowest.Round(time.Millisecond), peak>>20)
}

// textOf counts the text of a document and of the documents embedded in
// it: the text of its blocks and the cells of its tables.
func textOf(doc *gofastcore.Document) int {
	n := 0
	for i := range doc.Parts {
		for k := range doc.Parts[i].Blocks {
			b := &doc.Parts[i].Blocks[k]
			n += len(b.Text)
			if b.Table != nil {
				for _, row := range b.Table.Rows {
					for _, cell := range row {
						n += len(cell)
					}
				}
			}
		}
	}
	for _, c := range doc.Children {
		n += textOf(c)
	}
	return n
}

// truncated reports whether a limit cut the document or one embedded in
// it.
func truncated(doc *gofastcore.Document) bool {
	if _, ok := warning(doc.Warnings, gofastcore.WarnLimitExceeded); ok {
		return true
	}
	for i := range doc.Parts {
		if _, ok := warning(doc.Parts[i].Warnings, gofastcore.WarnLimitExceeded); ok {
			return true
		}
	}
	for _, c := range doc.Children {
		if truncated(c) {
			return true
		}
	}
	return false
}
