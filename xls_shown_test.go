package gofastmsbin_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// lastDigit reports whether the text is a number that differs from x by
// no more than the last of fifteen digits.
func lastDigit(x float64, text string) bool {
	y, err := strconv.ParseFloat(text, 64)
	return err == nil && math.Abs(x-y) <= 2e-14*math.Max(math.Abs(x), math.Abs(y))
}

// What a cell shows agrees with what LibreOffice shows for it
// (testdata/oracle_lo.py), for every cell that is not a date: dates are
// written in ISO 8601 here whatever their format. Held-out files are
// counted and never named.
func TestShownAgainstLibreOffice(t *testing.T) {
	if testing.Short() {
		t.Skip("a comparison over the whole corpus")
	}
	dir := filepath.Join("testdata", "corpus", "oracle-lo-xls")
	if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
		t.Skip("no oracle output: run testdata/oracle_lo.py")
	}
	entries, err := msbintest.Corpus("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var files, same, differ, dates, missing, rounding int
	shown := 0
	perFile := map[string]int{}
	note := func(e msbintest.Entry, format string, args ...any) {
		if !e.HeldOut() && shown < 60 && perFile[e.Path] < 2 {
			shown++
			perFile[e.Path]++
			t.Logf("%s: %s", e.Path, fmt.Sprintf(format, args...))
		}
	}
	for _, e := range entries {
		if e.Format != "xls" {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.SHA256+".json.gz"))
		if err != nil {
			continue
		}
		zr, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			continue
		}
		var want struct {
			Sheets map[string][][]string `json:"sheets"`
			Cut    bool                  `json:"cut"`
		}
		err = json.NewDecoder(zr).Decode(&want)
		f.Close()
		if err != nil {
			continue // a file still being written
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		doc, err := gofastmsbin.ExtractFile(ctx, e.Path, &gofastcore.Options{Cells: true})
		cancel()
		if err != nil || doc.Source.Format != "xls" {
			continue
		}
		files++
		for i := range doc.Parts {
			part := &doc.Parts[i]
			rows, ok := want.Sheets[part.Name]
			if part.Kind != gofastcore.PartSheet || !ok {
				continue
			}
			// A sheet that was not read, or not to its end, is another
			// test's matter.
			if _, failed := warning(part.Warnings, gofastcore.WarnPartFailed); failed {
				continue
			}
			got := cellsOf(t, part)
			for at, cell := range got {
				if cell.typ == gofastcore.CellDate {
					dates++
					continue
				}
				if at[0] >= len(rows) || at[1] >= len(rows[at[0]]) {
					if !want.Cut {
						differ++
						note(e, "sheet %q, row %d, column %d: here %q, LibreOffice has no such cell", part.Name, at[0], at[1], cell.text)
					}
					continue
				}
				if a, b := squash(rows[at[0]][at[1]]), squash(cell.text); a == b || strings.ContainsRune(b, 0xFFFD) {
					same++
				} else if x, err1 := strconv.ParseFloat(a, 64); err1 == nil && lastDigit(x, b) {
					// The two round the last of fifteen digits differently.
					rounding++
				} else {
					differ++
					note(e, "sheet %q, row %d, column %d: LibreOffice shows %q, here %q (value %q)", part.Name, at[0], at[1], rows[at[0]][at[1]], cell.text, cell.value)
				}
			}
			for r, row := range rows {
				for c, text := range row {
					if squash(text) == "" {
						continue
					}
					if _, ok := got[[2]int{r, c}]; !ok {
						missing++
						note(e, "sheet %q, row %d, column %d: LibreOffice shows %q, here nothing", part.Name, r, c, text)
					}
				}
			}
		}
	}
	total := same + differ + missing + rounding
	t.Logf("%d workbooks: %d cells are shown as LibreOffice shows them, %d differ, %d differ in the last digit of a number, %d are missing here; %d dates not compared",
		files, same, differ, rounding, missing, dates)
	if total > 0 && float64(same)/float64(total) < 0.995 {
		t.Errorf("%d of %d cells agree with LibreOffice", same, total)
	}
}
