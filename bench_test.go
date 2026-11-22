package gofastmsbin_test

import (
	"context"
	"os"
	"path/filepath"
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
