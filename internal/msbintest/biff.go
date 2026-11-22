package msbintest

import (
	"encoding/binary"
	"math"
	"strings"
	"unicode/utf16"
)

// This file writes workbook streams ([MS-XLS]) for the tests, record by
// record, on its own from the published description. The library's
// reading is checked against it, and against what other programs read of
// real files.

// Rec is one record: its type, the size of its body, and the body.
func Rec(typ uint16, body ...byte) []byte {
	b := binary.LittleEndian.AppendUint16(nil, typ)
	b = binary.LittleEndian.AppendUint16(b, uint16(len(body)))
	return append(b, body...)
}

// Cat joins byte slices.
func Cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// U16 and U32 write numbers as the format does.
func U16(v ...uint16) []byte {
	var b []byte
	for _, x := range v {
		b = binary.LittleEndian.AppendUint16(b, x)
	}
	return b
}

func U32(v ...uint32) []byte {
	var b []byte
	for _, x := range v {
		b = binary.LittleEndian.AppendUint32(b, x)
	}
	return b
}

// F64 writes a floating-point number.
func F64(v float64) []byte { return binary.LittleEndian.AppendUint64(nil, math.Float64bits(v)) }

// wideChars is the characters of s in 16 bits each.
func wideChars(s string) []byte {
	var b []byte
	for _, u := range utf16.Encode([]rune(s)) {
		b = binary.LittleEndian.AppendUint16(b, u)
	}
	return b
}

func narrow(s string) bool {
	for _, r := range s {
		if r > 0xFF {
			return false
		}
	}
	return true
}

// chars is the flags byte and the characters of s: one byte each where
// every character is below U+0100, sixteen bits each otherwise.
func chars(s string) (count int, body []byte) {
	if narrow(s) {
		b := []byte{0}
		for _, r := range s {
			b = append(b, byte(r))
		}
		return len(b) - 1, b
	}
	w := wideChars(s)
	return len(w) / 2, append([]byte{1}, w...)
}

// Str is a string with its length in 16 bits (XLUnicodeString).
func Str(s string) []byte {
	n, body := chars(s)
	return append(U16(uint16(n)), body...)
}

// ShortStr is a string with its length in 8 bits (ShortXLUnicodeString).
func ShortStr(s string) []byte {
	n, body := chars(s)
	return append([]byte{byte(n)}, body...)
}

// BOF begins a substream: 0x0005 the workbook's globals, 0x0010 a
// worksheet, 0x0020 a chart, 0x0040 a macro sheet.
func BOF(kind uint16) []byte {
	return Rec(0x0809, U16(0x0600, kind, 0x0DBB, 0x07CC, 0x00C1, 0, 0x0306, 0)...)
}

// EOF ends a substream.
func EOF() []byte { return Rec(0x000A) }

// Font is a font with the given name.
func Font(name string) []byte {
	return Rec(0x0031, Cat(U16(200, 0, 0x7FFF, 400, 0), []byte{0, 0, 0, 0}, ShortStr(name))...)
}

// Format is a number format with the given number.
func Format(id uint16, code string) []byte { return Rec(0x041E, Cat(U16(id), Str(code))...) }

// XF is a cell format: a font and a number format, by number.
func XF(font, format uint16) []byte {
	return Rec(0x00E0, Cat(U16(font, format, 0x0001, 0x0020), make([]byte, 12))...)
}

// SSTString is one string of the shared strings, with what it carries
// besides its characters.
type SSTString struct {
	Text string
	// Runs are formatting runs: pairs of a character position and a font
	// number.
	Runs [][2]uint16
	// Phonetic is bytes of phonetic data after the string.
	Phonetic []byte
	// Wide writes the characters in 16 bits each even where 8 would do.
	Wide bool
}

func (s SSTString) bytes() []byte {
	n, body := chars(s.Text)
	if s.Wide && body[0] == 0 {
		body = append([]byte{1}, wideChars(s.Text)...)
	}
	flags := body[0]
	var head []byte
	if len(s.Runs) > 0 {
		flags |= 0x08
		head = append(head, U16(uint16(len(s.Runs)))...)
	}
	if len(s.Phonetic) > 0 {
		flags |= 0x04
		head = append(head, U32(uint32(len(s.Phonetic)))...)
	}
	out := Cat(U16(uint16(n)), []byte{flags}, head, body[1:])
	for _, r := range s.Runs {
		out = append(out, U16(r[0], r[1])...)
	}
	return append(out, s.Phonetic...)
}

// SST is the shared strings in one record.
func SST(strings ...SSTString) []byte {
	body := U32(uint32(len(strings)), uint32(len(strings)))
	for _, s := range strings {
		body = append(body, s.bytes()...)
	}
	return Rec(0x00FC, body...)
}

// Plain makes shared strings of texts.
func Plain(texts ...string) []SSTString {
	out := make([]SSTString, len(texts))
	for i, t := range texts {
		out[i] = SSTString{Text: t}
	}
	return out
}

// Cell records. Each takes a row, a column and a cell format.

func LabelSST(row, col, xf uint16, index uint32) []byte {
	return Rec(0x00FD, Cat(U16(row, col, xf), U32(index))...)
}

func Number(row, col, xf uint16, v float64) []byte {
	return Rec(0x0203, Cat(U16(row, col, xf), F64(v))...)
}

// RKInt is a whole number held in an RK record; hundredths divides it by
// a hundred.
func RKInt(row, col, xf uint16, v int32, hundredths bool) []byte {
	rk := uint32(v)<<2 | 2
	if hundredths {
		rk |= 1
	}
	return Rec(0x027E, Cat(U16(row, col, xf), U32(rk))...)
}

// RKFloat is a floating-point number whose low 34 bits are zero, held in
// an RK record.
func RKFloat(row, col, xf uint16, v float64) []byte {
	return Rec(0x027E, Cat(U16(row, col, xf), U32(uint32(math.Float64bits(v)>>32)&^3))...)
}

// MulRK is a run of whole numbers in one row, from the column col.
func MulRK(row, col, xf uint16, values ...int32) []byte {
	body := U16(row, col)
	for _, v := range values {
		body = append(body, Cat(U16(xf), U32(uint32(v)<<2|2))...)
	}
	return Rec(0x00BD, append(body, U16(col+uint16(len(values))-1)...)...)
}

func Bool(row, col, xf uint16, v bool) []byte {
	b := byte(0)
	if v {
		b = 1
	}
	return Rec(0x0205, Cat(U16(row, col, xf), []byte{b, 0})...)
}

func Error(row, col, xf uint16, code byte) []byte {
	return Rec(0x0205, Cat(U16(row, col, xf), []byte{code, 1})...)
}

func Label(row, col, xf uint16, text string) []byte {
	return Rec(0x0204, Cat(U16(row, col, xf), Str(text))...)
}

func Blank(row, col, xf uint16) []byte { return Rec(0x0201, U16(row, col, xf)...) }

// Formula is a formula whose result is a number. The formula itself is
// the constant 1.
func Formula(row, col, xf uint16, result float64) []byte {
	return formula(row, col, xf, F64(result))
}

func formula(row, col, xf uint16, result []byte) []byte {
	return Rec(0x0006, Cat(U16(row, col, xf), result, U16(0), U32(0), U16(3), []byte{0x1E, 1, 0})...)
}

// FormulaString is a formula whose result is text: the Formula record
// and the String record after it.
func FormulaString(row, col, xf uint16, text string) []byte {
	return Cat(formula(row, col, xf, []byte{0, 0, 0, 0, 0, 0, 0xFF, 0xFF}), Rec(0x0207, Str(text)...))
}

// FormulaBool, FormulaError and FormulaEmpty are formulas whose results
// are a truth value, an error and the empty string.
func FormulaBool(row, col, xf uint16, v bool) []byte {
	b := byte(0)
	if v {
		b = 1
	}
	return formula(row, col, xf, []byte{1, 0, b, 0, 0, 0, 0xFF, 0xFF})
}

func FormulaError(row, col, xf uint16, code byte) []byte {
	return formula(row, col, xf, []byte{2, 0, code, 0, 0, 0, 0xFF, 0xFF})
}

func FormulaEmpty(row, col, xf uint16) []byte {
	return formula(row, col, xf, []byte{3, 0, 0, 0, 0, 0, 0xFF, 0xFF})
}

// Row describes a row; hidden gives it no height.
func Row(row uint16, hidden bool) []byte {
	flags := uint16(0x0100)
	if hidden {
		flags |= 0x0020
	}
	return Rec(0x0208, U16(row, 0, 256, 255, 0, 0, flags, 15)...)
}

// ColInfo describes the columns first to last; hidden hides them.
func ColInfo(first, last uint16, hidden bool) []byte {
	flags := uint16(0)
	if hidden {
		flags = 1
	}
	return Rec(0x007D, U16(first, last, 2304, 15, flags, 0)...)
}

// MergeCells merges ranges, each a first and last row and a first and
// last column.
func MergeCells(ranges ...[4]uint16) []byte {
	body := U16(uint16(len(ranges)))
	for _, r := range ranges {
		body = append(body, U16(r[0], r[1], r[2], r[3])...)
	}
	return Rec(0x00E5, body...)
}

// Dimensions says which rows and columns the cells lie in.
func Dimensions(rows uint32, cols uint16) []byte {
	return Rec(0x0200, Cat(U32(0, rows), U16(0, cols, 0))...)
}

// Comment is a note on a cell, with the drawing object that holds its
// text: the Obj and TxO records, the text and its formatting, and the
// Note record.
func Comment(row, col, id uint16, author, text string) []byte {
	n, body := chars(text)
	obj := Rec(0x005D, Cat(U16(0x0015, 0x0012, 0x0019, id, 0x4011), make([]byte, 12), U16(0, 0))...)
	txo := Rec(0x01B6, Cat(U16(0x0212, 0), make([]byte, 6), U16(uint16(n), 16), make([]byte, 4))...)
	return Cat(obj, txo, Rec(0x003C, body...), Rec(0x003C, make([]byte, 16)...),
		Rec(0x001C, Cat(U16(row, col, 0, id), Str(author), []byte{0})...))
}

// hyperlink strings: a count of 16-bit units, the terminator among them.
func hlinkString(s string) []byte {
	w := append(wideChars(s), 0, 0)
	return append(U32(uint32(len(w)/2)), w...)
}

var hlinkClass = []byte{0xD0, 0xC9, 0xEA, 0x79, 0xF9, 0xBA, 0xCE, 0x11, 0x8C, 0x82, 0x00, 0xAA, 0x00, 0x4B, 0xA9, 0x0B}

// HLinkURL links the cells of a range to an address, and to a place in
// what is there if location is not empty.
func HLinkURL(rng [4]uint16, url, location string) []byte {
	flags := uint32(0x03)
	w := append(wideChars(url), 0, 0)
	body := Cat(U16(rng[0], rng[1], rng[2], rng[3]), hlinkClass, U32(2), nil)
	rest := Cat([]byte{0xE0, 0xC9, 0xEA, 0x79, 0xF9, 0xBA, 0xCE, 0x11, 0x8C, 0x82, 0x00, 0xAA, 0x00, 0x4B, 0xA9, 0x0B}, U32(uint32(len(w))), w)
	if location != "" {
		flags |= 0x08
		rest = append(rest, hlinkString(location)...)
	}
	return Rec(0x01B8, Cat(body, U32(flags), rest)...)
}

// HLinkPlace links the cells of a range to a place in the workbook.
func HLinkPlace(rng [4]uint16, location string) []byte {
	return Rec(0x01B8, Cat(U16(rng[0], rng[1], rng[2], rng[3]), hlinkClass, U32(2), U32(0x08), hlinkString(location))...)
}

// HLinkFile links the cells of a range to a file, up folders above.
func HLinkFile(rng [4]uint16, up uint16, path string) []byte {
	ansi := append([]byte(path), 0)
	file := Cat([]byte{0x03, 0x03, 0, 0, 0, 0, 0, 0, 0xC0, 0, 0, 0, 0, 0, 0, 0x46}, U16(up), U32(uint32(len(ansi))), ansi,
		U16(0xFFFF, 0xDEAD), make([]byte, 20), U32(0))
	return Rec(0x01B8, Cat(U16(rng[0], rng[1], rng[2], rng[3]), hlinkClass, U32(2), U32(0x01), file)...)
}

// Sheet is a sheet of a workbook written by Workbook.
type Sheet struct {
	Name string
	// State is 0 for a sheet that is shown, 1 for a hidden one and 2 for
	// one only a program can show.
	State byte
	// Kind is 0 for a worksheet, 1 for a macro sheet, 2 for a chart and 6
	// for a module of the VBA project.
	Kind byte
	// Records are the sheet's records between its BOF and its EOF.
	Records [][]byte
}

// Workbook returns a workbook stream: the globals (its BOF, the given
// records, the list of sheets, its EOF) and the sheets.
func Workbook(globals [][]byte, sheets ...Sheet) []byte {
	head := Cat(BOF(0x0005), Cat(globals...))
	size := len(head) + len(EOF())
	for _, s := range sheets {
		size += 4 + 6 + len(ShortStr(s.Name))
	}
	var bodies [][]byte
	for _, s := range sheets {
		kind := uint16(0x0010)
		switch s.Kind {
		case 1:
			kind = 0x0040
		case 2:
			kind = 0x0020
		case 6:
			kind = 0x0006
		}
		bodies = append(bodies, Cat(BOF(kind), Cat(s.Records...), EOF()))
	}
	out := head
	at := size
	for i, s := range sheets {
		out = append(out, Rec(0x0085, Cat(U32(uint32(at)), []byte{s.State, s.Kind}, ShortStr(s.Name))...)...)
		at += len(bodies[i])
	}
	out = append(out, EOF()...)
	return append(out, Cat(bodies...)...)
}

// BookFile returns a compound file holding the workbook stream.
func BookFile(stream []byte) []byte {
	return Compound(map[string][]byte{"Workbook": stream})
}

// Tokens of compiled formulas ([MS-XLS] 2.5.198), for FormulaTokens and
// the records that hold formulas. A reference's flags say which of its
// row and column are relative.

func PtgInt(v uint16) []byte { return append([]byte{0x1E}, U16(v)...) }

func PtgNum(v float64) []byte { return append([]byte{0x1F}, F64(v)...) }

func PtgStr(s string) []byte { return append([]byte{0x17}, ShortStr(s)...) }

func PtgBool(v bool) []byte {
	if v {
		return []byte{0x1D, 1}
	}
	return []byte{0x1D, 0}
}

// PtgOp is an operator: 0x03 add, 0x04 subtract, 0x05 multiply, 0x06
// divide, 0x08 concatenate, 0x15 parentheses and so on.
func PtgOp(code byte) []byte { return []byte{code} }

func colFlags(col uint16, rowRel, colRel bool) uint16 {
	if rowRel {
		col |= 0x8000
	}
	if colRel {
		col |= 0x4000
	}
	return col
}

// PtgRef is a reference to a cell.
func PtgRef(row, col uint16, rowRel, colRel bool) []byte {
	return append([]byte{0x44}, U16(row, colFlags(col, rowRel, colRel))...)
}

// PtgArea is a reference to a range, both corners absolute.
func PtgArea(r0, r1, c0, c1 uint16) []byte {
	return append([]byte{0x25}, U16(r0, r1, c0, c1)...)
}

// PtgRefN is a reference of a shared formula or a name: its relative
// parts are offsets from the cell that uses it.
func PtgRefN(row int16, col int8, rowRel, colRel bool) []byte {
	return append([]byte{0x4C}, U16(uint16(row), colFlags(uint16(uint8(col)), rowRel, colRel))...)
}

// PtgRef3d is a reference to a cell of the sheets an entry of the
// ExternSheet record names.
func PtgRef3d(xti, row, col uint16) []byte {
	return append([]byte{0x5A}, U16(xti, row, col)...)
}

// PtgArea3d is a reference to a range of such sheets.
func PtgArea3d(xti, r0, r1, c0, c1 uint16) []byte {
	return append([]byte{0x3B}, U16(xti, r0, r1, c0, c1)...)
}

// PtgName is a defined name, by its number from 1.
func PtgName(index uint32) []byte { return append([]byte{0x43}, U32(index)...) }

// PtgNameX is a name taken through a link.
func PtgNameX(xti uint16, index uint32) []byte {
	return Cat([]byte{0x59}, U16(xti), U32(index))
}

// PtgFunc is a function with a fixed number of arguments, by its number.
func PtgFunc(id uint16) []byte { return append([]byte{0x41}, U16(id)...) }

// PtgFuncVar is a function called with n arguments.
func PtgFuncVar(n byte, id uint16) []byte { return Cat([]byte{0x42, n}, U16(id)) }

// PtgAttrSum is SUM with one argument.
func PtgAttrSum() []byte { return []byte{0x19, 0x10, 0, 0} }

// PtgExp refers to the shared or array formula whose range begins at row
// and col.
func PtgExp(row, col uint16) []byte { return append([]byte{0x01}, U16(row, col)...) }

// PtgArray is an array constant; its values follow the tokens.
func PtgArray() []byte { return append([]byte{0x60}, make([]byte, 7)...) }

// FormulaTokens is a formula with the given tokens and the data after
// them, whose result is a number.
func FormulaTokens(row, col, xf uint16, result float64, rgce []byte, extra ...byte) []byte {
	return Rec(0x0006, Cat(U16(row, col, xf), F64(result), U16(0), U32(0), U16(uint16(len(rgce))), rgce, extra)...)
}

// FormulaTokensString is such a formula whose result is text.
func FormulaTokensString(row, col, xf uint16, text string, rgce []byte, between ...[]byte) []byte {
	head := Rec(0x0006, Cat(U16(row, col, xf), []byte{0, 0, 0, 0, 0, 0, 0xFF, 0xFF}, U16(0), U32(0), U16(uint16(len(rgce))), rgce)...)
	return Cat(head, Cat(between...), Rec(0x0207, Str(text)...))
}

// ShrFmla is the formula the cells of a range share.
func ShrFmla(r0, r1 uint16, c0, c1 byte, rgce []byte) []byte {
	return Rec(0x04BC, Cat(U16(r0, r1), []byte{c0, c1, 0, 0}, U16(uint16(len(rgce))), rgce)...)
}

// ArrayFormula is the formula of a range of cells that give its results
// together.
func ArrayFormula(r0, r1 uint16, c0, c1 byte, rgce []byte, extra ...byte) []byte {
	return Rec(0x0221, Cat(U16(r0, r1), []byte{c0, c1}, U16(0), U32(0), U16(uint16(len(rgce))), rgce, extra)...)
}

// SupBookSelf is the link that stands for the workbook itself.
func SupBookSelf(sheets uint16) []byte { return Rec(0x01AE, U16(sheets, 0x0401)...) }

// SupBookAddin is the link that stands for the add-ins.
func SupBookAddin() []byte { return Rec(0x01AE, U16(1, 0x3A01)...) }

// SupBookFile is a link to another file and its sheets.
func SupBookFile(path string, sheets ...string) []byte {
	body := Cat(U16(uint16(len(sheets))), Str(path))
	for _, s := range sheets {
		body = append(body, Str(s)...)
	}
	return Rec(0x01AE, body...)
}

// ExternName is a name the formulas take through the link before it;
// sheet is the sheet of that file it is local to, from 1, or 0.
func ExternName(name string, sheet uint16) []byte {
	return Rec(0x0023, Cat(U16(0, sheet, 0), ShortStr(name), U16(0))...)
}

// ExternSheet is the table of sheet references: for each, a link by its
// number and the first and last of its sheets.
func ExternSheet(refs ...[3]uint16) []byte {
	body := U16(uint16(len(refs)))
	for _, r := range refs {
		body = append(body, U16(r[0], r[1], r[2])...)
	}
	return Rec(0x0017, body...)
}

// Name is a defined name: local to the sheet with the number sheet, from
// 1, or the workbook's if sheet is 0. builtin is the number of one of
// Excel's own names, or negative.
func Name(name string, sheet uint16, builtin int, rgce []byte, extra ...byte) []byte {
	flags := uint16(0)
	_, chars := chars(name)
	count := len([]rune(name))
	if builtin >= 0 {
		flags, chars, count = 0x0020, []byte{0, byte(builtin)}, 1
	}
	return Rec(0x0018, Cat(U16(flags), []byte{0, byte(count)}, U16(uint16(len(rgce)), 0, sheet), make([]byte, 4), chars, rgce, extra)...)
}

// DVList is a drop-down list for the cells of the ranges (a data
// validation of type list): its values written out, or, if values is
// nil, the formula they come from. hideArrow hides the arrow.
func DVList(ranges [][4]uint16, values []string, formula []byte, hideArrow bool) []byte {
	flags := uint32(3) | 0x100
	if values != nil {
		flags |= 0x80
		n, body := chars(strings.Join(values, "\x00"))
		formula = Cat([]byte{0x17, byte(n)}, body)
	}
	if hideArrow {
		flags |= 0x200
	}
	empty := Cat(U16(1), []byte{0, 0})
	body := Cat(U32(flags), empty, empty, empty, empty, U16(uint16(len(formula)), 0), formula, U16(0, 0), U16(uint16(len(ranges))))
	for _, r := range ranges {
		body = append(body, U16(r[0], r[1], r[2], r[3])...)
	}
	return Rec(0x01BE, body...)
}
