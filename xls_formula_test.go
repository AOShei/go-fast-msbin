package gofastmsbin_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	t "github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// formulas gives the formulas of the first table of a part by cell.
func formulas(part *gofastcore.Part) map[string]string {
	out := map[string]string{}
	for _, b := range part.Blocks {
		if b.Kind != gofastcore.KindTable {
			continue
		}
		r0, c0, _ := topLeft(b.Loc.Range)
		for _, d := range b.Table.Cells {
			if d.Formula != "" {
				out[fmt.Sprintf("%c%d", 'A'+c0+d.Col, r0+d.Row+1)] = d.Formula
			}
		}
	}
	return out
}

// The formulas of cells are read back as text with Options.Cells: each
// kind of operand, references to other sheets and other workbooks, names,
// functions, shared formulas and array formulas.
func TestFormulas(tt *testing.T) {
	links := [][]byte{
		t.SupBookSelf(3), t.SupBookFile("\x01C:\\other.xls", "Data", "More"), t.ExternName("Rate", 0), t.ExternName("Local", 1),
		t.SupBookAddin(), t.ExternName("EDATE", 0),
		// Sheet references: this workbook's second sheet, its first to
		// second, a deleted sheet, the other workbook's first sheet, the
		// workbook's names, the other workbook's names, the add-ins.
		t.ExternSheet([3]uint16{0, 1, 1}, [3]uint16{0, 0, 1}, [3]uint16{0, 0xFFFF, 0xFFFF}, [3]uint16{1, 0, 0}, [3]uint16{0, 0xFFFE, 0xFFFE},
			[3]uint16{1, 0xFFFE, 0xFFFE}, [3]uint16{2, 0xFFFE, 0xFFFE}),
		t.Name("Total", 0, -1, t.PtgArea3d(0, 0, 9, 0, 0)),
		t.Name("", 1, 6, t.PtgArea3d(0, 0, 4, 0, 2)), // Print_Area, local to the first sheet
		t.Name("Twice", 2, -1, t.Cat(t.PtgName(1), t.PtgInt(2), t.PtgOp(0x05))),
		t.Name("Nothing", 0, -1, nil),
	}
	f := func(row, col uint16, tokens ...[]byte) []byte {
		return t.FormulaTokens(row, col, xfGeneral, float64(row*10+col), t.Cat(tokens...))
	}
	arrayData := t.Cat([]byte{1}, t.U16(0), []byte{0x01}, t.F64(1.5), []byte{0x02}, t.Str("two"))
	sheet := [][]byte{
		f(0, 0, t.PtgInt(1), t.PtgNum(2.5), t.PtgOp(0x03)),
		f(0, 1, t.PtgRef(0, 0, true, true), t.PtgRef(4, 2, false, false), t.PtgOp(0x05), t.PtgOp(0x15)),
		f(0, 2, t.PtgArea(0, 9, 0, 0), t.PtgAttrSum()),
		f(0, 3, t.PtgStr(`say "hi"`), t.PtgStr("é"), t.PtgOp(0x08)),
		f(1, 0, t.PtgRef3d(0, 4, 1), t.PtgArea3d(1, 0, 1, 0, 1), t.PtgFuncVar(2, 4)),
		f(1, 1, t.PtgRef3d(2, 0, 0)),
		f(1, 2, t.PtgRef3d(3, 2, 2), t.PtgNameX(5, 1), t.PtgOp(0x03), t.PtgNameX(5, 2), t.PtgOp(0x03)),
		f(1, 3, t.PtgName(1), t.PtgNameX(4, 3), t.PtgOp(0x04)),
		f(2, 0, t.PtgBool(true), t.PtgInt(1), t.PtgInt(2), t.PtgFuncVar(3, 1)),
		f(2, 1, t.PtgNameX(6, 1), t.PtgInt(3), t.PtgInt(4), t.PtgFuncVar(3, 0xFF)),
		f(2, 2, t.PtgInt(9), t.PtgFunc(15)),
		f(2, 3, t.PtgArea(0, 0xFFFF, 1, 2), t.PtgArea(3, 5, 0, 0xFF), t.PtgFuncVar(2, 4)),
		// A shared formula over A4:A6, the cell above times two.
		f(3, 0, t.PtgExp(3, 0)), t.ShrFmla(3, 5, 0, 0, t.Cat(t.PtgRefN(-1, 0, true, true), t.PtgInt(2), t.PtgOp(0x05))),
		f(4, 0, t.PtgExp(3, 0)), f(5, 0, t.PtgExp(3, 0)),
		// An array formula over B4:B5.
		f(3, 1, t.PtgExp(3, 1)), t.ArrayFormula(3, 4, 1, 1, t.Cat(t.PtgArea(0, 1, 0, 0), t.PtgArray(), t.PtgOp(0x05)), arrayData...),
		f(4, 1, t.PtgExp(3, 1)),
		// A formula of text, shared.
		t.FormulaTokensString(3, 2, xfGeneral, "text", t.PtgExp(3, 2), t.ShrFmla(3, 3, 2, 2, t.Cat(t.PtgRefN(0, -2, true, true), t.PtgStr("!"), t.PtgOp(0x08)))),
		// One that cannot be read back: a function that does not exist.
		f(5, 2, t.PtgInt(1), t.PtgFunc(9999)),
	}
	data := book(links, t.Sheet{Name: "Main", Records: sheet}, t.Sheet{Name: "Second sheet"}, t.Sheet{Name: "Third"})
	doc := mustExtract(tt, data, &gofastcore.Options{Cells: true})
	got := formulas(&doc.Parts[0])
	want := map[string]string{
		"A1": "1+2.5", "B1": "(A1*$C$5)", "C1": "SUM($A$1:$A$10)", "D1": `"say ""hi"""&"é"`,
		"A2": "SUM('Second sheet'!$B$5,'Main:Second sheet'!$A$1:$B$2)", "B2": "#REF!$A$1",
		"C2": "[1]Data!$C$3+[1]!Rate+[1]Data!Local", "D2": "Total-Twice",
		"A3": "IF(TRUE,1,2)", "B3": "EDATE(3,4)", "C3": "SIN(9)", "D3": "SUM($B:$C,$4:$6)",
		"A4": "A3*2", "A5": "A4*2", "A6": "A5*2",
		"B4": "$A$1:$A$2*{1.5,\"two\"}",
		"C4": "A4&\"!\"",
	}
	for cell, f := range want {
		if got[cell] != f {
			tt.Errorf("%s: %q, want %q", cell, got[cell], f)
		}
	}
	if len(got) != len(want) {
		tt.Errorf("%d formulas, want %d: %v", len(got), len(want), got)
	}
	if w, ok := warning(doc.Warnings, gofastcore.WarnInputUnreadContent); !ok || w.Message != "1 formulas not read back" {
		tt.Errorf("%+v", doc.Warnings)
	}
	if w, ok := warning(doc.Warnings, gofastcore.WarnInputExternalReference); !ok || !strings.HasPrefix(w.Message, "1 link ") {
		tt.Errorf("%+v", doc.Warnings)
	}
	// Without Options.Cells no formula is read, and none is counted.
	plain := mustExtract(tt, data, nil)
	if _, ok := warning(plain.Warnings, gofastcore.WarnInputUnreadContent); ok || plain.Parts[0].Blocks[0].Table.Cells != nil {
		tt.Errorf("without Cells: %+v", plain.Warnings)
	}

	// The defined names are in the stored part, a name local to a sheet
	// with the sheet's name in front.
	stored := doc.Parts[len(doc.Parts)-1]
	if stored.Kind != gofastcore.PartStored || len(stored.Blocks) != 3 {
		tt.Fatalf("stored: %+v", stored)
	}
	for i, want := range []string{"Total: 'Second sheet'!$A$1:$A$10", "Main!_xlnm.Print_Area: 'Second sheet'!$A$1:$C$5", "Second sheet!Twice: Total*2"} {
		b := stored.Blocks[i]
		if b.Text != want || b.Attrs[gofastcore.AttrStored] != gofastcore.StoredDefinedName || !strings.HasPrefix(want, b.Attrs[gofastcore.AttrName]+": ") {
			tt.Errorf("name %d: %q (%v)", i, b.Text, b.Attrs)
		}
	}
}

// A macro sheet's cells are read as a sheet, and its formulas are in the
// stored part as code.
func TestMacroSheet(tt *testing.T) {
	f := func(row, col uint16, tokens ...[]byte) []byte {
		return t.FormulaTokens(row, col, xfGeneral, 1, t.Cat(tokens...))
	}
	macro := [][]byte{
		f(0, 0, t.PtgBool(false), t.PtgFuncVar(1, 87)),                      // ECHO(FALSE)
		f(1, 0, t.PtgStr("calc.exe"), t.PtgFuncVar(1, 110)),                 // EXEC("calc.exe")
		f(2, 0, t.PtgStr("done"), t.PtgInt(2), t.PtgFuncVar(2, 0x8000|118)), // ALERT("done",2): a command
		f(3, 0, t.PtgFuncVar(0, 54)),                                        // HALT()
	}
	data := book(nil, t.Sheet{Name: "Data", Records: [][]byte{t.RKInt(0, 0, xfGeneral, 1, false)}}, t.Sheet{Name: "Macro1", Kind: 1, Records: macro})
	doc := mustExtract(tt, data, nil)
	if len(doc.Parts) != 3 || doc.Parts[2].Kind != gofastcore.PartStored {
		tt.Fatalf("%d parts", len(doc.Parts))
	}
	b := doc.Parts[2].Blocks[0]
	if b.Kind != gofastcore.KindCode || b.Attrs[gofastcore.AttrLanguage] != "excel4" || b.Attrs[gofastcore.AttrName] != "Macro1" ||
		b.Attrs[gofastcore.AttrStored] != gofastcore.StoredMacro {
		tt.Errorf("%+v", b)
	}
	lines := strings.Split(b.Text, "\n")
	if len(lines) != 4 || lines[0] != "A1\t=ECHO(FALSE)" || lines[1] != "A2\t=EXEC(\"calc.exe\")" || lines[2] != "A3\t=ALERT(\"done\",2)" || lines[3] != "A4\t=HALT()" {
		tt.Errorf("code: %q", b.Text)
	}
}

// A drop-down list is a note at the first cell it is for: its values, or
// the formula they come from.
func TestDropDownLists(tt *testing.T) {
	recs := [][]byte{
		t.MulRK(0, 0, xfGeneral, 1, 2, 3), t.MulRK(1, 0, xfGeneral, 4, 5, 6), t.Row(5, true), t.Row(6, true),
		t.DVList([][4]uint16{{1, 1, 1, 1}, {0, 0, 0, 0}}, []string{"yes", "no", "", `say "maybe"`}, nil, false),
		t.DVList([][4]uint16{{0, 1, 2, 2}}, nil, t.PtgArea(0, 9, 5, 5), false),
		t.DVList([][4]uint16{{3, 3, 0, 0}}, nil, t.PtgRefN(0, 4, true, true), true), // the arrow is hidden
		t.DVList([][4]uint16{{5, 6, 0, 3}}, []string{"a", "b"}, nil, false),         // every cell in a hidden row
		// A rule that is no list: whole numbers.
		t.Rec(0x01BE, t.Cat(t.U32(1), t.U16(1), []byte{0, 0}, t.U16(1), []byte{0, 0}, t.U16(1), []byte{0, 0}, t.U16(1), []byte{0, 0},
			t.U16(3, 0), t.PtgInt(1), t.U16(3, 0), t.PtgInt(9), t.U16(1, 0, 0, 0, 0))...),
	}
	doc := mustExtract(tt, book(nil, t.Sheet{Name: "S", Records: recs}), nil)
	type list struct {
		text, at string
		hidden   bool
	}
	var got []list
	for _, b := range doc.Parts[0].Blocks {
		if b.Kind == gofastcore.KindNote && b.Attrs[gofastcore.AttrNoteType] == gofastcore.NoteChoices {
			got = append(got, list{b.Text, b.Loc.Range, b.Display == gofastcore.DisplayHidden})
		}
	}
	want := []list{
		{"yes\nno\nsay \"maybe\"", "B2 A1", false},
		{"=$F$1:$F$10", "C1:C2", false},
		{"=E4", "A4", true},
		{"a\nb", "A6:D7", true},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		tt.Errorf("lists:\n%+v\nwant\n%+v", got, want)
	}
}
