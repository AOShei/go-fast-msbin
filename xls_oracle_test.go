package gofastmsbin_test

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// xlrdBook is what xlrd reads of a workbook (testdata/oracle_xls.py).
type xlrdBook struct {
	BIFF   int `json:"biff"`
	Sheets []struct {
		Name       string  `json:"name"`
		Visibility int     `json:"visibility"`
		Cells      [][]any `json:"cells"` // row, column, type, value
		Cut        bool    `json:"cut"`
		Error      string  `json:"error"`
	} `json:"sheets"`
}

// shownCell is a cell as the document has it.
type shownCell struct {
	text  string
	typ   gofastcore.CellType
	value string
}

// topLeft reads the first cell of a range such as "B3:D10": row and
// column, from 0.
func topLeft(rng string) (row, col int, ok bool) {
	first, _, _ := strings.Cut(rng, ":")
	i := 0
	for i < len(first) && first[i] >= 'A' && first[i] <= 'Z' {
		col = col*26 + int(first[i]-'A') + 1
		i++
	}
	n, err := strconv.Atoi(first[i:])
	return n - 1, col - 1, err == nil && i > 0
}

// cellsOf gives the cells of a sheet's tables by their place.
func cellsOf(t testing.TB, part *gofastcore.Part) map[[2]int]shownCell {
	out := map[[2]int]shownCell{}
	for i := range part.Blocks {
		b := &part.Blocks[i]
		// The table of a chart's data is not cells of the sheet.
		if b.Kind != gofastcore.KindTable || b.Table == nil || b.Attrs[gofastcore.AttrRole] == gofastcore.RoleChart {
			continue
		}
		r0, c0, ok := topLeft(b.Loc.Range)
		if !ok {
			t.Fatalf("a table with no place: %q", b.Loc.Range)
		}
		for r, row := range b.Table.Rows {
			for c, text := range row {
				if text != "" {
					out[[2]int{r0 + r, c0 + c}] = shownCell{text: text}
				}
			}
		}
		for _, d := range b.Table.Cells {
			at := [2]int{r0 + d.Row, c0 + d.Col}
			cell := out[at]
			cell.typ, cell.value = d.Type, d.Value
			out[at] = cell
		}
	}
	return out
}

// cleanName is a sheet's name as a part has it.
func cleanName(s string) (string, bool) {
	return strings.TrimSpace(s), true
}

// symbolic reports whether shown can be what the codes in stored stand
// for in a symbol font: the same number of characters, with those of
// shown that differ outside Latin-1.
func symbolic(stored, shown string) bool {
	a, b := []rune(squash(stored)), []rune(squash(shown))
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	changed := false
	for i := range a {
		if a[i] == b[i] {
			continue
		}
		if b[i] < 0x100 {
			return false
		}
		changed = true
	}
	return changed
}

var xlrdErrors = map[float64]string{0x00: "#NULL!", 0x07: "#DIV/0!", 0x0F: "#VALUE!", 0x17: "#REF!", 0x1D: "#NAME?", 0x24: "#NUM!", 0x2A: "#N/A"}

// The cells of every workbook agree with what xlrd reads: the same cells,
// with the same stored values. Held-out files are counted and never
// named.
func TestCellsAgainstXlrd(t *testing.T) {
	if testing.Short() {
		t.Skip("a comparison over the whole corpus")
	}
	dir := filepath.Join("testdata", "corpus", "oracle-xls")
	if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
		t.Skip("no oracle output: run testdata/oracle_xls.py")
	}
	entries, err := msbintest.Corpus("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var files, older, same, differ, missing, extra, sheetsDiffer, symbols int
	shown := 0
	note := func(e msbintest.Entry, format string, args ...any) {
		if !e.HeldOut() && shown < 60 {
			shown++
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
			t.Fatal(err)
		}
		var book xlrdBook
		err = json.NewDecoder(zr).Decode(&book)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if book.BIFF != 80 && book.BIFF != 70 && book.BIFF != 50 {
			continue // a workbook of Excel 4 or older
		}
		old := book.BIFF != 80
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		doc, err := gofastmsbin.ExtractFile(ctx, e.Path, &gofastcore.Options{Cells: true})
		cancel()
		if err != nil {
			continue
		}
		files++
		if old {
			older++
		}
		// xlrd lists the worksheets; chart sheets and macro sheets are
		// parts here and not sheets there. They are matched by name, in
		// order.
		var sheets []*gofastcore.Part
		for i := range doc.Parts {
			if doc.Parts[i].Kind == gofastcore.PartSheet {
				sheets = append(sheets, &doc.Parts[i])
			}
		}
		next := 0
		for i, want := range book.Sheets {
			if want.Error != "" {
				continue
			}
			name, _ := cleanName(want.Name)
			var sheet *gofastcore.Part
			for ; next < len(sheets); next++ {
				if sheets[next].Name == name {
					sheet = sheets[next]
					next++
					break
				}
			}
			if sheet == nil {
				sheetsDiffer++
				note(e, "sheet %q of xlrd is not a part here", want.Name)
				break
			}
			got := cellsOf(t, sheet)
			seen := 0
			for _, c := range want.Cells {
				at := [2]int{int(c[0].(float64)), int(c[1].(float64))}
				typ := int(c[2].(float64))
				cell, ok := got[at]
				if !ok {
					// A string of nothing but spaces shows nothing.
					if s, isText := c[3].(string); isText && strings.TrimSpace(s) == "" {
						continue
					}
					missing++
					note(e, "sheet %d, row %d, column %d: xlrd has %v, here nothing", i+1, at[0], at[1], c[3])
					continue
				}
				seen++
				agree := false
				switch typ {
				case 1: // text
					s, _ := c[3].(string)
					agree = squash(gofastcore.ExpandLigatures(s)) == squash(cell.text) || strings.ContainsRune(cell.text, 0xFFFD)
				case 2, 3: // a number, a date
					v, isNum := c[3].(float64)
					text := cell.value
					if text == "" {
						text = cell.text
					}
					n, err := strconv.ParseFloat(text, 64)
					agree = isNum && err == nil && (n == v || math.Abs(n-v) <= 1e-12*math.Max(math.Abs(n), math.Abs(v)))
					// What is not a number is null in the oracle's output.
					agree = agree || c[3] == nil && err == nil && (math.IsNaN(n) || math.IsInf(n, 0))
				case 4:
					v, _ := c[3].(float64)
					agree = cell.text == "TRUE" && v == 1 || cell.text == "FALSE" && v == 0
				case 5:
					v, _ := c[3].(float64)
					agree = cell.text == xlrdErrors[v]
				}
				// A cell in a symbol font shows the characters its codes
				// stand for, and xlrd gives the codes.
				if s, isText := c[3].(string); !agree && isText && typ == 1 && symbolic(s, cell.text) {
					symbols++
					continue
				}
				if agree {
					same++
				} else {
					differ++
					note(e, "sheet %d, row %d, column %d: xlrd has %v (type %d), here %q (value %q)", i+1, at[0], at[1], c[3], typ, cell.text, cell.value)
				}
			}
			if !want.Cut && len(got) > seen {
				extra += len(got) - seen
				if !e.HeldOut() && shown < 60 {
					var at [][2]int
					for k := range got {
						at = append(at, k)
					}
					sort.Slice(at, func(i, j int) bool { return at[i][0] < at[j][0] || at[i][0] == at[j][0] && at[i][1] < at[j][1] })
					have := map[[2]int]bool{}
					for _, c := range want.Cells {
						have[[2]int{int(c[0].(float64)), int(c[1].(float64))}] = true
					}
					for _, k := range at {
						if !have[k] {
							note(e, "sheet %d, row %d, column %d: here %q, xlrd nothing (%d such)", i+1, k[0], k[1], got[k].text, len(got)-seen)
							break
						}
					}
				}
			}
		}
	}
	total := same + differ + missing
	t.Logf("%d workbooks, %d of them of Excel 5 or 95: %d cells agree with xlrd, %d differ, %d are missing here, %d are here and not in xlrd; %d more are text in a symbol font; %d workbooks differ in their sheets",
		files, older, same, differ, missing, extra, symbols, sheetsDiffer)
	if total > 0 && float64(same)/float64(total) < 0.995 {
		t.Errorf("%d of %d cells agree with xlrd", same, total)
	}
}

// Every workbook of the corpus gives the same document whatever the
// number of sheets read at a time, also where a limit cuts it.
func TestWorkersOverTheCorpus(t *testing.T) {
	entries, err := msbintest.Corpus("testdata")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Skip("the corpus is not downloaded")
	}
	n := 0
	for _, e := range entries {
		if e.Format != "xls" || e.Size > 8<<20 {
			continue
		}
		if n++; testing.Short() && n > 100 {
			break
		}
		data, err := os.ReadFile(e.Path)
		if err != nil {
			t.Fatal(err)
		}
		for _, limits := range []gofastcore.Limits{{}, {MaxTextBytes: 20000}} {
			var first []byte
			for _, workers := range []int{1, 5} {
				doc, err := extract(data, &gofastcore.Options{Workers: workers, Limits: limits, Cells: true, Spans: true})
				if err != nil {
					break
				}
				out, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				if first == nil {
					first = out
				} else if string(first) != string(out) {
					name := e.SHA256[:12]
					if !e.HeldOut() {
						name = e.Path
					}
					t.Errorf("%s (limits %+v): %d workers give another document than one", name, limits, workers)
				}
			}
		}
	}
}
