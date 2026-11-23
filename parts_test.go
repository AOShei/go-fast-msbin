package gofastmsbin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	t "github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// bookOfSheets is a workbook of n sheets, each large enough that its
// stream is one of the file's own and not a short one.
func bookOfSheets(n int) []byte {
	sheets := make([]t.Sheet, n)
	for i := range sheets {
		var records [][]byte
		for row := 0; row < 40; row++ {
			records = append(records, t.Label(uint16(row), 0, xfGeneral, fmt.Sprintf("sheet %d, row %d", i+1, row+1)), t.Number(uint16(row), 1, xfGeneral, float64(i*100+row)))
		}
		sheets[i] = t.Sheet{Name: fmt.Sprintf("S%d", i+1), Records: records}
	}
	return t.BookFile(t.Workbook(styles, sheets...))
}

// The parts are handed on in their order as they are read, by however
// many workers: the same parts as the document read whole has. A caller
// that stops early, or whose context ends, is not left waiting, and
// leaves no worker behind.
func TestPartsInOrder(tt *testing.T) {
	data := bookOfSheets(60)
	whole := mustExtract(tt, data, nil)
	if len(whole.Parts) != 60 {
		tt.Fatalf("%d parts", len(whole.Parts))
	}
	before := runtime.NumGoroutine()
	for _, workers := range []int{1, 2, 3, 8, 64} {
		f, err := gofastmsbin.OpenBytes(data, &gofastcore.Options{Workers: workers})
		if err != nil {
			tt.Fatal(err)
		}
		n := 0
		for part, err := range f.Parts(context.Background()) {
			if err != nil {
				tt.Fatal(err)
			}
			got, _ := json.Marshal(part)
			want, _ := json.Marshal(whole.Parts[n])
			if string(got) != string(want) {
				tt.Fatalf("%d workers: part %d is not the document's", workers, n+1)
			}
			n++
		}
		if n != 60 {
			tt.Errorf("%d workers: %d parts", workers, n)
		}
		// Stopping after a few parts.
		n = 0
		for range f.Parts(context.Background()) {
			if n++; n == 3 {
				break
			}
		}
		// A context that ends while the parts are read: an error, or
		// all the parts if the reading was done first.
		ctx, cancel := context.WithCancel(context.Background())
		n = 0
		var last error
		for _, err := range f.Parts(ctx) {
			last = err
			if n++; n == 2 {
				cancel()
			}
		}
		cancel()
		if last == nil && n != 60 {
			tt.Errorf("%d workers: %d parts and no error after the context ended", workers, n)
		}
		// A limit met among the parts: the document is the same as with
		// one worker, read in order.
		limited := gofastcore.Options{Workers: workers, Limits: gofastcore.Limits{MaxTextBytes: 20000}}
		one := limited
		one.Workers = 1
		a, _ := json.Marshal(mustExtract(tt, data, &limited))
		b, _ := json.Marshal(mustExtract(tt, data, &one))
		if string(a) != string(b) {
			tt.Errorf("%d workers: within a limit the document is not the one read in order", workers)
		}
		f.Close()
	}
	// No worker is left behind.
	for i := 0; i < 100 && runtime.NumGoroutine() > before; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if now := runtime.NumGoroutine(); now > before {
		tt.Errorf("%d goroutines before, %d after", before, now)
	}
}
