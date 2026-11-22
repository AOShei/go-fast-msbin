package gofastmsbin_test

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin"
	"github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// twinSheet is a sheet of the .xlsx file LibreOffice made of a workbook
// (testdata/oracle_lo.py --twin): its name and the formulas of its cells.
type twinSheet struct {
	name     string
	formulas map[string]string
	shared   int // cells that use a shared formula written elsewhere
}

func attr(se xml.StartElement, name string) string {
	for _, a := range se.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// readTwin reads the sheets of an .xlsx file and the formulas in them.
func readTwin(file string) ([]twinSheet, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	open := func(name string) (io.ReadCloser, error) {
		for _, f := range zr.File {
			if f.Name == name {
				return f.Open()
			}
		}
		return nil, fmt.Errorf("no %s", name)
	}
	targets := map[string]string{}
	if rc, err := open("xl/_rels/workbook.xml.rels"); err == nil {
		d := xml.NewDecoder(rc)
		for {
			tok, err := d.Token()
			if err != nil {
				break
			}
			if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "Relationship" {
				targets[attr(se, "Id")] = attr(se, "Target")
			}
		}
		rc.Close()
	}
	rc, err := open("xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	var sheets []twinSheet
	var parts []string
	d := xml.NewDecoder(rc)
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "sheet" {
			sheets = append(sheets, twinSheet{name: attr(se, "name"), formulas: map[string]string{}})
			parts = append(parts, targets[attr(se, "id")])
		}
	}
	rc.Close()
	for i := range sheets {
		rc, err := open(path.Join("xl", parts[i]))
		if err != nil {
			continue
		}
		d := xml.NewDecoder(rc)
		cell := ""
		for {
			tok, err := d.Token()
			if err != nil {
				break
			}
			se, ok := tok.(xml.StartElement)
			if !ok {
				continue
			}
			switch se.Name.Local {
			case "c":
				cell = attr(se, "r")
			case "f":
				var text string
				if err := d.DecodeElement(&text, &se); err != nil {
					break
				}
				if text == "" {
					sheets[i].shared++
				} else {
					sheets[i].formulas[cell] = text
				}
			}
		}
		rc.Close()
	}
	return sheets, nil
}

// sameRange is a range of one cell, "A1:A1", which LibreOffice writes as
// the cell.
var sameRange = regexp.MustCompile(`(\$?[A-Z]+\$?[0-9]+):(\$?[A-Z]+\$?[0-9]+)`)

// sameFormula compares two formulas apart from what a program that
// rewrites a formula changes: spaces, the case of letters, and in
// LibreOffice's writing TRUE() for TRUE and a cell for a range of one
// cell.
func sameFormula(a, b string) bool {
	norm := func(s string) string {
		s = strings.ToUpper(strings.Join(strings.Fields(s), ""))
		s = strings.ReplaceAll(strings.ReplaceAll(s, "TRUE()", "TRUE"), "FALSE()", "FALSE")
		s = strings.ReplaceAll(s, "_XLUDF.", "")
		return sameRange.ReplaceAllStringFunc(s, func(m string) string {
			if first, second, _ := strings.Cut(m, ":"); first == second {
				return first
			}
			return m
		})
	}
	return norm(a) == norm(b)
}

// written reports whether a formula of LibreOffice's file is one it
// makes up for a value that is no formula: a truth value, an error.
func written(f string) bool {
	return f == "TRUE()" || f == "FALSE()" || f == "NA()" || strings.HasPrefix(f, "#")
}

// The formulas read back from a workbook are those LibreOffice writes
// when it saves the same workbook as .xlsx. Held-out files are counted
// and never named.
func TestFormulasAgainstTwins(t *testing.T) {
	if testing.Short() {
		t.Skip("a comparison over the whole corpus")
	}
	dir := filepath.Join("testdata", "corpus", "twins-xls")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no twins: run testdata/oracle_lo.py --twin")
	}
	entries, err := msbintest.Corpus("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var files, same, differ, onlyHere, onlyThere, unseen int
	shown := 0
	perFile := map[string]int{}
	note := func(e msbintest.Entry, format string, args ...any) {
		if !e.HeldOut() && shown < 80 && perFile[e.Path] < 3 {
			shown++
			perFile[e.Path]++
			t.Logf("%s: %s", filepath.Base(e.Path), fmt.Sprintf(format, args...))
		}
	}
	for _, e := range entries {
		if e.Format != "xls" {
			continue
		}
		twin, err := readTwin(filepath.Join(dir, e.SHA256+".xlsx"))
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		doc, err := gofastmsbin.ExtractFile(ctx, e.Path, &gofastcore.Options{Cells: true})
		cancel()
		if err != nil || doc.Source.Format != "xls" {
			continue
		}
		files++
		byName := map[string]*twinSheet{}
		for i := range twin {
			byName[strings.TrimSpace(twin[i].name)] = &twin[i]
		}
		for i := range doc.Parts {
			part := &doc.Parts[i]
			want := byName[part.Name]
			if part.Kind != gofastcore.PartSheet || want == nil {
				continue
			}
			if _, failed := warning(part.Warnings, gofastcore.WarnPartFailed); failed {
				continue
			}
			got := map[string]string{}
			for _, b := range part.Blocks {
				if b.Kind != gofastcore.KindTable {
					continue
				}
				r0, c0, _ := topLeft(b.Loc.Range)
				for _, d := range b.Table.Cells {
					if d.Formula != "" {
						got[cellRef(r0+d.Row, c0+d.Col)] = d.Formula
					}
				}
			}
			for cell, f := range got {
				w, ok := want.formulas[cell]
				switch {
				case !ok:
					onlyHere++
				case sameFormula(f, w):
					same++
				case strings.Contains(w, "#REF!") && !strings.Contains(f, "#REF!"):
					// What LibreOffice could not write: a macro sheet's
					// functions, a link it did not follow.
					onlyHere++
				default:
					differ++
					note(e, "sheet %q, %s: LibreOffice writes %q, here %q", part.Name, cell, w, f)
				}
			}
			cells := cellsOf(t, part)
			for cell, w := range want.formulas {
				if _, ok := got[cell]; ok || written(w) {
					continue
				}
				// A formula whose result shows nothing is in no table,
				// and so has no place for its formula.
				r, c, _ := topLeft(cell)
				if _, shown := cells[[2]int{r, c}]; !shown {
					unseen++
					continue
				}
				onlyThere++
				note(e, "sheet %q, %s: LibreOffice writes %q, here no formula", part.Name, cell, w)
			}
		}
	}
	t.Logf("%d workbooks: %d formulas are as LibreOffice writes them, %d differ, %d are only here, %d only in LibreOffice's file; %d more are of cells that show nothing",
		files, same, differ, onlyHere, onlyThere, unseen)
	if total := same + differ + onlyThere; total > 0 && float64(same)/float64(total) < 0.97 {
		t.Errorf("%d of %d formulas agree with LibreOffice's", same, total)
	}
}

// cellRef names a cell: column letters and row number.
func cellRef(row, col int) string {
	var letters []byte
	for c := col + 1; c > 0; c = (c - 1) / 26 {
		letters = append([]byte{byte('A' + (c-1)%26)}, letters...)
	}
	return fmt.Sprintf("%s%d", letters, row+1)
}
