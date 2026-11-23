package gofastmsbin_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
)

// large is one of the generated files of bench/baseline.py.
func large(b *testing.B, name string) []byte {
	b.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "corpus", "generated", name))
	if err != nil {
		b.Skip("the generated files are absent: python3 bench/baseline.py generate")
	}
	return data
}

func benchLarge(b *testing.B, name string, workers int) {
	data := large(b, name)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := gofastmsbin.OpenBytes(data, &gofastcore.Options{Workers: workers})
		if err != nil {
			b.Fatal(err)
		}
		if _, err := f.Document(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLargeXLS(b *testing.B)          { benchLarge(b, "large.xls", 0) }
func BenchmarkLargeXLSOneWorker(b *testing.B) { benchLarge(b, "large.xls", 1) }

func BenchmarkLargeDOC(b *testing.B) { benchLarge(b, "large.doc", 0) }

func BenchmarkLargePPT(b *testing.B)          { benchLarge(b, "large.ppt", 0) }
func BenchmarkLargePPTOneWorker(b *testing.B) { benchLarge(b, "large.ppt", 1) }

// TestAllocationBudgets holds what reading the large files allocates to a
// budget, so that a change that makes the library allocate much more is
// seen: the bytes allocated and the number of allocations for one
// reading, one sheet or slide at a time. The budgets are a quarter to a
// half over what was measured when they were set; the allocations of the
// workbook, which are few, have room for several times as many.
func TestAllocationBudgets(t *testing.T) {
	if testing.Short() || underRace {
		t.Skip("reads the large generated files, without the race detector")
	}
	for _, c := range []struct {
		name          string
		bytes, allocs uint64
	}{
		{"large.xls", 210 << 20, 2_000},  // 1,966,080 cells: 173 MB in 270 allocations
		{"large.doc", 80 << 20, 60_000},  // 1.5 million words: 62 MB in 41,800
		{"large.ppt", 55 << 20, 350_000}, // 1,500 slides: 43 MB in 275,700
	} {
		data, err := os.ReadFile(filepath.Join("testdata", "corpus", "generated", c.name))
		if err != nil {
			t.Skip("the generated files are absent: python3 bench/baseline.py generate")
		}
		read := func() {
			f, err := gofastmsbin.OpenBytes(data, &gofastcore.Options{Workers: 1})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Document(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		read() // what the first reading sets up once is not counted
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		read()
		runtime.ReadMemStats(&after)
		bytes, allocs := after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs
		t.Logf("%s: %d MiB in %d allocations (budget %d MiB, %d)", c.name, bytes>>20, allocs, c.bytes>>20, c.allocs)
		if bytes > c.bytes || allocs > c.allocs {
			t.Errorf("%s: %d bytes in %d allocations, over the budget of %d bytes in %d", c.name, bytes, allocs, c.bytes, c.allocs)
		}
	}
}
