package gofastmsbin_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/AOShei/go-fast-core"
	t "github.com/AOShei/go-fast-msbin/internal/msbintest"
)

// describe gives the blocks of a part in a line each: kind, place, what
// is declared of its display, and its text or its rows.
func describe(part *gofastcore.Part) []string {
	var out []string
	for _, b := range part.Blocks {
		line := fmt.Sprintf("%s %s", b.Kind, b.Loc.Range)
		if b.Display != 0 {
			line += " " + b.Display.String()
		}
		switch {
		case b.Table != nil:
			var rows []string
			for _, r := range b.Table.Rows {
				rows = append(rows, strings.Join(r, "|"))
			}
			line += " " + b.Attrs[gofastcore.AttrChart] + " " + strings.Join(rows, "/")
		case b.Image != nil:
			line += fmt.Sprintf(" %s %s %dx%d %q", b.Image.ID, b.Image.MIME, b.Image.Width, b.Image.Height, b.Text)
		default:
			line += " " + b.Text
		}
		out = append(out, line)
	}
	return out
}

// What floats over a sheet is read at the cell it is anchored to: text
// boxes, pictures and charts.
func TestShapesPicturesAndCharts(tt *testing.T) {
	links := [][]byte{t.SupBookSelf(2), t.ExternSheet([3]uint16{0, 0, 0}), t.DrawingGroup(t.PNG(3, 2), t.PNG(5, 4))}
	// A chart of two series whose data is cells of the sheet, and one of
	// a series that only the chart keeps.
	linked := t.Chart([][]byte{
		t.ChartSeries("Sales", t.PtgArea3d(0, 1, 3, 1, 1), t.PtgArea3d(0, 1, 3, 0, 0)),
		t.ChartSeries("Costs", t.PtgArea3d(0, 1, 3, 2, 2), t.PtgArea3d(0, 1, 3, 0, 0)),
		t.ChartType(0x1017, t.U16(0, 150, 0), true),
		t.ChartTitle("Money", 1), t.ChartTitle("Year", 3), t.ChartTitle("Thousands", 2),
	})
	kept := t.Chart([][]byte{
		t.ChartSeries("Kept", nil, nil),
		t.ChartType(0x1019, t.U16(0, 50, 0), false), // a pie with a hole
	}, t.ChartCache(1, t.Number(0, 0, 0, 1.5), t.Number(1, 0, 0, 2.5)), t.ChartCache(2, t.Label(0, 0, 0, "a"), t.Label(1, 0, 0, "b")))
	sheet := [][]byte{
		t.Label(0, 0, xfGeneral, "Year"), t.Label(0, 1, xfGeneral, "Sales"), t.Label(0, 2, xfGeneral, "Costs"),
		t.RKInt(1, 0, xfGeneral, 2001, false), t.Number(1, 1, xfTwo, 10), t.Number(1, 2, xfTwo, 4),
		t.RKInt(2, 0, xfGeneral, 2002, false), t.Number(2, 1, xfTwo, 12.5), t.Number(2, 2, xfTwo, 5),
		t.RKInt(3, 0, xfGeneral, 2003, false), t.Number(3, 1, xfTwo, 15), t.Number(3, 2, xfTwo, 6),
		// A text box at F2, with two paragraphs.
		t.MsoDrawing(t.ArtShape(1, 5, 1)), t.Obj(6, 1), t.TextObject("A text box\n\nsecond paragraph"),
		// A picture at F6, with alternative text; a hidden one at G6.
		t.MsoDrawing(t.ArtShape(2, 5, 5, t.ArtProp{ID: 0x0104, Value: 2}, t.ArtProp{ID: 0x0381, Text: "A red dot"})), t.Obj(8, 2),
		t.MsoDrawing(t.ArtShape(3, 6, 5, t.ArtProp{ID: 0x0104, Value: 1}, t.ArtProp{ID: 0x03BF, Value: 0x00020002})), t.Obj(8, 3),
		// The charts at F10 and F20.
		t.MsoDrawing(t.ArtShape(4, 5, 9)), t.Obj(5, 4), linked,
		t.MsoDrawing(t.ArtShape(5, 5, 19)), t.Obj(5, 5), kept,
		// WordArt at A30: its text is a property of the shape.
		t.MsoDrawing(t.ArtShape(6, 0, 29, t.ArtProp{ID: 0x00C0, Text: "Curved words"})), t.Obj(30, 6),
	}
	data := book(links, t.Sheet{Name: "Data", Records: sheet}, t.Sheet{Name: "A chart", Kind: 2, Records: [][]byte{}})
	doc := mustExtract(tt, data, nil)
	want := []string{
		"table A1:C4  Year|Sales|Costs/2001|10.00|4.00/2002|12.50|5.00/2003|15.00|6.00",
		"paragraph F2 A text box",
		"paragraph F2 second paragraph",
		`image F6 image2.png image/png 5x4 "A red dot"`,
		`image G6 hidden image1.png image/png 3x2 ""`,
		"caption F10 Money",
		"table F10 bar3d |Sales|Costs/2001|10.00|4.00/2002|12.50|5.00/2003|15.00|6.00",
		"table F20 doughnut |Kept/a|1.5/b|2.5",
		"paragraph A30 Curved words",
	}
	if got := describe(&doc.Parts[0]); !reflect.DeepEqual(got, want) {
		tt.Errorf("blocks:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, b := range doc.Parts[0].Blocks {
		if b.Kind == gofastcore.KindTable && b.Attrs[gofastcore.AttrRole] == gofastcore.RoleChart && b.Loc.Range == "F10" {
			if b.Attrs[gofastcore.AttrAxisX] != "Year" || b.Attrs[gofastcore.AttrAxisY] != "Thousands" || b.Table.HeaderRows != 1 {
				tt.Errorf("the chart's attributes: %v", b.Attrs)
			}
		}
		if b.Image != nil && b.Image.Data != nil {
			tt.Error("the bytes of a picture were given without being asked for")
		}
	}
	// With ImagesData the bytes of each picture; with ImagesNone no
	// picture, and the sheet says nothing of them.
	doc = mustExtract(tt, data, &gofastcore.Options{Images: gofastcore.ImagesData})
	n := 0
	for _, b := range doc.Parts[0].Blocks {
		if b.Image != nil {
			n++
			if !strings.HasPrefix(string(b.Image.Data), "\x89PNG") {
				tt.Errorf("%s: %d bytes that are not a PNG file", b.Image.ID, len(b.Image.Data))
			}
		}
	}
	doc = mustExtract(tt, data, &gofastcore.Options{Images: gofastcore.ImagesNone})
	for _, b := range doc.Parts[0].Blocks {
		if b.Image != nil {
			tt.Error("a picture with ImagesNone")
		}
	}
	if n != 2 {
		tt.Errorf("%d pictures with their bytes", n)
	}
}
