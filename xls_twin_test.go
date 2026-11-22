package gofastmsbin_test

import (
	"archive/zip"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
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

var wholeRow = regexp.MustCompile(`\$A([0-9]+):\$IV([0-9]+)`)

// sameFormula compares two formulas apart from what a program that
// rewrites a formula changes: spaces, the case of letters, and in
// LibreOffice's writing TRUE() for TRUE and a cell for a range of one
// cell.
func sameFormula(a, b string) bool {
	norm := func(s string) string {
		s = strings.ToUpper(strings.Join(strings.Fields(s), ""))
		s = strings.ReplaceAll(strings.ReplaceAll(s, "TRUE()", "TRUE"), "FALSE()", "FALSE")
		s = strings.ReplaceAll(s, "_XLUDF.", "")
		// LibreOffice writes 0 for an argument left out, NA() for the
		// error value, and a whole row as its first to its last column.
		s = strings.ReplaceAll(strings.ReplaceAll(s, ",0,", ",,"), ",0)", ",)")
		s = strings.ReplaceAll(s, "NA()", "#N/A")
		s = wholeRow.ReplaceAllString(s, "$1:$2")
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
	// What differs is LibreOffice's rewriting: a reference to a deleted
	// sheet without its cell, TABLE() for a what-if table, its own names
	// for some functions, another form of exponent.
	if total := same + differ + onlyThere; total > 0 && float64(same)/float64(total) < 0.95 {
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

// ooxmlCommand builds go-fast-ooxml's command from the directory beside
// this one, and returns its path; the test is skipped where that
// directory is absent. The libraries do not import one another, in
// tests either: the command's JSON is what is compared.
func ooxmlCommand(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "go-fast-ooxml")
	if _, err := os.Stat(filepath.Join(src, "cmd", "gofastooxml")); err != nil {
		t.Skip("go-fast-ooxml is not beside this module")
	}
	bin := filepath.Join(t.TempDir(), "gofastooxml")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/gofastooxml")
	cmd.Dir = src
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("go-fast-ooxml's command does not build: %v\n%s", err, out)
	}
	return bin
}

// A workbook and the .xlsx file LibreOffice makes of it give the same
// sheets, the same tables in the same places and the same notes, read by
// this library and by go-fast-ooxml. Held-out files are counted and never
// named.
func TestTwins(t *testing.T) {
	if testing.Short() {
		t.Skip("a comparison over the whole corpus")
	}
	dir := filepath.Join("testdata", "corpus", "twins-xls")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no twins: run testdata/oracle_lo.py --twin")
	}
	bin := ooxmlCommand(t)
	entries, err := msbintest.Corpus("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var files, sheets, sheetsMissing, same, differ, onlyHere, onlyThere int
	var tables, tablesSame, notes, notesSame, chartsHere, chartsThere, chartsSame int
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
		twin := filepath.Join(dir, e.SHA256+".xlsx")
		if _, err := os.Stat(twin); e.Format != "xls" || err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		doc, err := gofastmsbin.ExtractFile(ctx, e.Path, nil)
		if err != nil || doc.Source.Format != "xls" {
			cancel()
			continue
		}
		out, err := exec.CommandContext(ctx, bin, "-quiet", twin).Output()
		cancel()
		var other gofastcore.Document
		if err != nil || json.Unmarshal(out, &other) != nil {
			continue
		}
		files++
		byName := map[string]*gofastcore.Part{}
		for i := range other.Parts {
			if other.Parts[i].Kind == gofastcore.PartSheet {
				byName[other.Parts[i].Name] = &other.Parts[i]
			}
		}
		for i := range doc.Parts {
			part := &doc.Parts[i]
			if part.Kind != gofastcore.PartSheet {
				continue
			}
			if _, failed := warning(part.Warnings, gofastcore.WarnPartFailed); failed {
				continue
			}
			want := byName[part.Name]
			if want == nil {
				sheetsMissing++
				note(e, "sheet %q is not in the twin", part.Name)
				continue
			}
			sheets++
			got, there := cellsOf(t, part), cellsOf(t, want)
			for at, cell := range got {
				w, ok := there[at]
				switch {
				case !ok:
					onlyHere++
					note(e, "sheet %q, %s: here %q, the twin has nothing", part.Name, cellRef(at[0], at[1]), cell.text)
				case squash(w.text) == squash(cell.text):
					same++
				default:
					differ++
					note(e, "sheet %q, %s: the twin shows %q, here %q", part.Name, cellRef(at[0], at[1]), w.text, cell.text)
				}
			}
			for at, w := range there {
				if _, ok := got[at]; !ok {
					onlyThere++
					note(e, "sheet %q, %s: the twin shows %q, here nothing", part.Name, cellRef(at[0], at[1]), w.text)
				}
			}
			// The charts: the same tables of data, with the same types.
			charts := func(p *gofastcore.Part) map[string]int {
				out := map[string]int{}
				for _, b := range p.Blocks {
					if b.Kind == gofastcore.KindTable && b.Attrs[gofastcore.AttrRole] == gofastcore.RoleChart {
						var sb strings.Builder
						sb.WriteString(b.Attrs[gofastcore.AttrChart])
						for _, row := range b.Table.Rows {
							sb.WriteString("\n" + squash(strings.Join(row, "|")))
						}
						out[sb.String()]++
					}
				}
				return out
			}
			c1, c2 := charts(part), charts(want)
			for text, n := range c1 {
				chartsHere += n
				chartsSame += min(n, c2[text])
				if c2[text] == 0 {
					note(e, "sheet %q: the chart %.200q is not in the twin", part.Name, text)
				}
			}
			for text, n := range c2 {
				chartsThere += n
				if c1[text] == 0 {
					note(e, "sheet %q: the twin's chart %.200q is not here", part.Name, text)
				}
			}
			// The tables: the same ranges. The notes: the same texts at
			// the same places.
			shape := func(p *gofastcore.Part) (ranges, texts map[string]bool) {
				ranges, texts = map[string]bool{}, map[string]bool{}
				for _, b := range p.Blocks {
					switch b.Kind {
					case gofastcore.KindTable:
						if b.Attrs[gofastcore.AttrRole] != gofastcore.RoleChart {
							ranges[b.Loc.Range] = true
						}
					case gofastcore.KindNote:
						texts[b.Attrs[gofastcore.AttrNoteType]+" "+b.Loc.Range+" "+squash(b.Text)] = true
					}
				}
				return ranges, texts
			}
			r1, n1 := shape(part)
			r2, n2 := shape(want)
			for r := range r1 {
				tables++
				if r2[r] {
					tablesSame++
				}
			}
			for n := range n1 {
				notes++
				if n2[n] {
					notesSame++
				} else {
					note(e, "sheet %q: note %q is not in the twin", part.Name, n)
				}
			}
			for n := range n2 {
				if !n1[n] {
					notes++
					note(e, "sheet %q: the twin's note %q is not here", part.Name, n)
				}
			}
		}
	}
	t.Logf("%d workbooks, %d sheets (%d not in the twin): %d cells the same, %d differ, %d only here, %d only in the twin; %d of %d tables have the same range; %d of %d notes are the same; %d charts here and %d in the twins, %d the same",
		files, sheets, sheetsMissing, same, differ, onlyHere, onlyThere, tablesSame, tables, notesSame, notes, chartsHere, chartsThere, chartsSame)
	if total := same + differ + onlyHere + onlyThere; total > 0 && float64(same)/float64(total) < 0.98 {
		t.Errorf("%d of %d cells are the same in the twin", same, total)
	}
}
