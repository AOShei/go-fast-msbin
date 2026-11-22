package msbintest

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"unicode/utf16"
)

// This file writes records of the drawing layer ([MS-ODRAW]) and of
// charts for the tests, on its own from the published descriptions.

// Art is one record of a drawing: a version and an instance, a type, and
// what follows.
func Art(ver uint8, inst uint16, typ uint16, body ...byte) []byte {
	b := binary.LittleEndian.AppendUint16(nil, uint16(ver)|inst<<4)
	b = binary.LittleEndian.AppendUint16(b, typ)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(body)))
	return append(b, body...)
}

// ArtContainer is a record that holds records.
func ArtContainer(typ uint16, children ...[]byte) []byte {
	return Art(0xF, 0, typ, Cat(children...)...)
}

// ArtProp is a property of a shape: a number, or text.
type ArtProp struct {
	ID    uint16
	Value uint32
	Text  string // for a property that is text; Value is then not used
}

// ArtProps is a shape's table of properties.
func ArtProps(props ...ArtProp) []byte {
	var table, data []byte
	for _, p := range props {
		id, v := p.ID, p.Value
		if p.Text != "" {
			var text []byte
			for _, u := range append(utf16.Encode([]rune(p.Text)), 0) {
				text = binary.LittleEndian.AppendUint16(text, u)
			}
			id, v = id|0x8000, uint32(len(text))
			data = append(data, text...)
		}
		table = append(table, Cat(U16(id), U32(v))...)
	}
	return Art(3, uint16(len(props)), 0xF00B, append(table, data...)...)
}

// ArtShape is a shape of a sheet's drawing: its properties, and the cell
// its top left corner is in.
func ArtShape(id uint32, col, row uint16, props ...ArtProp) []byte {
	return ArtContainer(0xF004,
		Art(2, 1, 0xF00A, Cat(U32(id), U32(0x0A00))...),
		ArtProps(props...),
		Art(0, 0, 0xF010, U16(0, col, 0, row, 0, col+2, 0, row+3, 0)...),
		Art(0, 0, 0xF011))
}

// MsoDrawing is a record of a sheet's drawing; Obj says what kind of
// object the shape before it is, and its number.
func MsoDrawing(art []byte) []byte { return Rec(0x00EC, art...) }

func Obj(kind, id uint16) []byte {
	return Rec(0x005D, Cat(U16(0x0015, 0x0012, kind, id, 0x6011), make([]byte, 12), U16(0, 0))...)
}

// TextObject is the text of the shape before it.
func TextObject(text string) []byte {
	n, body := chars(text)
	return Cat(Rec(0x01B6, Cat(U16(0x0212, 0), make([]byte, 6), U16(uint16(n), 16), make([]byte, 4))...),
		Rec(0x003C, body...), Rec(0x003C, make([]byte, 16)...))
}

// PNG is a picture of the given size, as a PNG file.
func PNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// DrawingGroup is the workbook's drawing group with its store of
// pictures, each a PNG file.
func DrawingGroup(pictures ...[]byte) []byte {
	var entries [][]byte
	for _, p := range pictures {
		blip := Art(0, 0x6E0, 0xF01E, Cat(make([]byte, 16), []byte{0xFF}, p)...)
		head := Cat([]byte{6, 6}, make([]byte, 16), U16(0xFF), U32(uint32(len(blip))), U32(1), U32(0), []byte{0, 0, 0, 0})
		entries = append(entries, Art(2, 6, 0xF007, Cat(head, blip)...))
	}
	return Rec(0x00EB, ArtContainer(0xF000, Art(0, 0, 0xF006, make([]byte, 16)...), ArtContainer(0xF001, entries...))...)
}

// Records of a chart. ChartSeries is one series: its name, kept as text,
// and the formulas its values and categories are taken from (nil for
// none).
func ChartSeries(name string, values, cats []byte) []byte {
	ai := func(id byte, rgce []byte) []byte {
		kind := byte(2)
		if rgce == nil {
			kind = 1
		}
		return Rec(0x1051, Cat([]byte{id, kind}, U16(0, 0, uint16(len(rgce))), rgce)...)
	}
	text := Rec(0x100D, Cat(U16(0), ShortStr(name))...)
	return Cat(Rec(0x1003, U16(1, 1, 0, 0, 1, 0)...), Rec(0x1033), ai(0, nil), text, ai(1, values), ai(2, cats), ai(3, nil), Rec(0x1034))
}

// ChartTitle is a text of the chart: link is 1 for the chart's title, 2
// for the title of the value axis and 3 for that of the category axis.
func ChartTitle(text string, link uint16) []byte {
	return Cat(Rec(0x1025, make([]byte, 32)...), Rec(0x1033), Rec(0x1051, Cat([]byte{0, 1}, U16(0, 0, 0))...),
		Rec(0x100D, Cat(U16(0), ShortStr(text))...), Rec(0x1027, U16(link, 0, 0)...), Rec(0x1034))
}

// ChartType is a group of series drawn as one type: typ is the record of
// the type (0x1017 a bar chart, 0x1018 a line chart, 0x1019 a pie) with
// its body; threeD draws it in three dimensions.
func ChartType(typ uint16, body []byte, threeD bool) []byte {
	out := Cat(Rec(0x1014, make([]byte, 20)...), Rec(0x1033), Rec(typ, body...))
	if threeD {
		out = append(out, Rec(0x103A, make([]byte, 14)...)...)
	}
	return append(out, Rec(0x1034)...)
}

// ChartCache is the values a chart keeps: kind is 1 for values and 2 for
// categories; each value is for a point (its row) of a series (its
// column), a number or text.
func ChartCache(kind uint16, cells ...[]byte) []byte {
	return Cat(Rec(0x1065, U16(kind)...), Cat(cells...))
}

// Chart is a chart's substream: its BOF, the records between the
// chart's Begin and End, what follows them, and its EOF.
func Chart(inside [][]byte, after ...[]byte) []byte {
	return Cat(BOF(0x0020), Rec(0x1002, make([]byte, 16)...), Rec(0x1033), Cat(inside...), Rec(0x1034), Cat(after...), EOF())
}
