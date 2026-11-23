package gofastmsbin

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// A workbook's stream is not held in memory where it can be read
// throughout: each sheet reads its own records of it. The document is
// the same as with the whole stream in memory, for every workbook of the
// corpus, with one sheet read at a time and with eight, and within a
// small limit on the text.
func TestWorkbookRegions(t *testing.T) {
	if testing.Short() {
		t.Skip("reads every workbook of the corpus six times")
	}
	entries, err := msbintest.Corpus("testdata")
	if err != nil || len(entries) == 0 {
		t.Skip("no corpus: python3 testdata/fetch.py")
	}
	defer func() { wholeBooks = false }()
	read := func(data []byte, whole bool, o gofastcore.Options) ([]byte, error) {
		wholeBooks = whole
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		doc, err := Extract(ctx, bytes.NewReader(data), int64(len(data)), &o)
		if err != nil {
			return nil, err
		}
		out, err := json.Marshal(doc)
		return out, err
	}
	files, documents, streamed := 0, 0, 0
	for i, e := range entries {
		if e.Format != "xls" {
			continue
		}
		data, err := os.ReadFile(e.Path)
		if err != nil {
			continue
		}
		files++
		for _, o := range []gofastcore.Options{
			{Workers: 1, Spans: true, Cells: true},
			{Workers: 8, Spans: true, Cells: true},
			{Limits: gofastcore.Limits{MaxTextBytes: 4096}},
		} {
			whole, err1 := read(data, true, o)
			parts, err2 := read(data, false, o)
			if (err1 == nil) != (err2 == nil) {
				t.Fatalf("file %d: with the stream in memory %v, a sheet at a time %v", i, err1, err2)
			}
			if !bytes.Equal(whole, parts) {
				t.Errorf("file %d: the document differs with the way the stream is read (workers %d, text limit %d)", i, o.Workers, o.Limits.MaxTextBytes)
			}
		}
		wholeBooks = false
		if f, err := OpenBytes(data, nil); err == nil {
			documents++
			if f.bookStream != nil {
				streamed++
			}
			f.Close()
		}
	}
	t.Logf("%d workbooks, %d opened, %d of them read a sheet at a time", files, documents, streamed)
	if streamed*2 < documents {
		t.Errorf("only %d of %d workbooks are read a sheet at a time", streamed, documents)
	}
}
