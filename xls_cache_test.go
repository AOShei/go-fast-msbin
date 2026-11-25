package gofastmsbin_test

import (
	"bytes"
	"fmt"
	"testing"

	gofastcore "github.com/AOShei/go-fast-core"
	t "github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// TestWorkbookNumbersRememberedAreTheirOwn reads workbooks whose sheets
// repeat their numbers, more of them than the table of remembered
// numbers starts with, one workbook after another. The second gives the
// same numbers another format under the same style, and each number
// must be shown by the format of its own workbook and sheet: what is
// remembered of one sheet says nothing of the next.
func TestWorkbookNumbersRememberedAreTheirOwn(tt *testing.T) {
	const rowsPerSheet, distinct = 600, 150
	sheet := func(name string, scale float64) t.Sheet {
		var recs [][]byte
		for r := 0; r < rowsPerSheet; r++ {
			v := float64(r%distinct) * scale
			recs = append(recs, t.Number(uint16(r), 0, xfTwo, v), t.Number(uint16(r), 1, xfPercent, v))
		}
		return t.Sheet{Name: name, Records: recs}
	}
	for round := 0; round < 3; round++ {
		for _, code := range []string{"0.00", "0.0", `0.000" kg"`} {
			globals := append([][]byte(nil), styles...)
			found := false
			for i, rec := range globals {
				if bytes.Equal(rec, t.Format(164, "0.00")) {
					globals[i], found = t.Format(164, code), true
				}
			}
			if !found {
				tt.Fatal("the styles of the tests have no format 164")
			}
			data := t.BookFile(t.Workbook(globals, sheet("A", 1.5), sheet("B", 0.25)))
			doc := mustExtract(tt, data, &gofastcore.Options{})
			if len(doc.Parts) != 2 {
				tt.Fatalf("%q: %d parts", code, len(doc.Parts))
			}
			for p, scale := range []float64{1.5, 0.25} {
				got := rows(tt, &doc.Parts[p])
				if len(got) != rowsPerSheet {
					tt.Fatalf("%q, sheet %d: %d rows", code, p+1, len(got))
				}
				for r, row := range got {
					v := float64(r%distinct) * scale
					var want string
					switch code {
					case "0.00":
						want = fmt.Sprintf("%.2f", v)
					case "0.0":
						// A quarter is shown to one place: halves round
						// away from zero, as the format does.
						want = fmt.Sprintf("%.1f", float64(int64(v*10+0.5))/10)
					default:
						want = fmt.Sprintf("%.3f kg", v)
					}
					percent := fmt.Sprintf("%d%%", int64(v*100+0.5))
					if row[0] != want || row[1] != percent {
						tt.Fatalf("%q, sheet %d, row %d: %q %q, want %q %q", code, p+1, r+1, row[0], row[1], want, percent)
					}
				}
			}
		}
	}
}
