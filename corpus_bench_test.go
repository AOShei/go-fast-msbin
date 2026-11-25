package gofastmsbin_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
	"github.com/AOShei/go-fast-msbin/internal/xls"
)

// benchCorpus reads every file of one format of the corpus, one after
// another, as a batch of ordinary files is read.
func benchCorpus(b *testing.B, format string, workers int) {
	entries, err := msbintest.Corpus("testdata")
	if err != nil || len(entries) == 0 {
		b.Skip("no corpus: python3 testdata/fetch.py")
	}
	var files [][]byte
	var total int64
	for _, e := range entries {
		if e.Format != format {
			continue
		}
		if data, err := os.ReadFile(e.Path); err == nil {
			files = append(files, data)
			total += int64(len(data))
		}
	}
	b.SetBytes(total)
	b.ReportAllocs()
	b.ResetTimer()
	read := 0
	for i := 0; i < b.N; i++ {
		read = 0
		for _, data := range files {
			if _, err := gofastmsbin.Extract(context.Background(), bytes.NewReader(data), int64(len(data)), &gofastcore.Options{Workers: workers}); err == nil {
				read++
			}
		}
	}
	b.ReportMetric(float64(read), "read")
	b.ReportMetric(float64(len(files)), "files")
}

func BenchmarkCorpusXLS(b *testing.B)          { benchCorpus(b, "xls", 0) }
func BenchmarkCorpusXLSOneWorker(b *testing.B) { benchCorpus(b, "xls", 1) }

// The workbooks again with no cache of formatted numbers, for what the
// cache gives (xls.NumberCache).
func BenchmarkCorpusXLSNoNumberCache(b *testing.B) {
	defer func(n int) { xls.NumberCache = n }(xls.NumberCache)
	xls.NumberCache = 0
	benchCorpus(b, "xls", 0)
}

func BenchmarkCorpusXLSOneWorkerNoNumberCache(b *testing.B) {
	defer func(n int) { xls.NumberCache = n }(xls.NumberCache)
	xls.NumberCache = 0
	benchCorpus(b, "xls", 1)
}

func BenchmarkCorpusDOC(b *testing.B)          { benchCorpus(b, "doc", 0) }
func BenchmarkCorpusPPT(b *testing.B)          { benchCorpus(b, "ppt", 0) }
func BenchmarkCorpusPPTOneWorker(b *testing.B) { benchCorpus(b, "ppt", 1) }
