package msbintest

import "encoding/binary"

// This file writes equations as the Equation Editor keeps them: MTEF of
// version 3, in the stream "Equation Native" of an embedded object. It
// is written from the description of the format, apart from the reader.

// EquationNative is the stream of an equation's object: a header of 28
// bytes that says how long the equation is, and the equation.
func EquationNative(mtef []byte) []byte {
	head := make([]byte, 28)
	binary.LittleEndian.PutUint16(head, 28)
	binary.LittleEndian.PutUint32(head[2:], 0x00020000)
	binary.LittleEndian.PutUint32(head[8:], uint32(len(mtef)))
	return append(head, mtef...)
}

// MTEF3 is an equation of version 3: its header, the size of what
// follows, the records, and the end.
func MTEF3(records ...[]byte) []byte {
	return Cat([]byte{3, 1, 1, 3, 0, 0x0A}, Cat(records...), []byte{0})
}

// Typefaces of a character of an equation.
const (
	EqText     = 1
	EqFunction = 2
	EqVariable = 3
	EqSymbol   = 6
	EqNumber   = 8
)

// EqLine is a line: its records, and the end.
func EqLine(records ...[]byte) []byte { return Cat([]byte{1}, Cat(records...), []byte{0}) }

// EqNull is a line that holds nothing.
func EqNull() []byte { return []byte{0x11} }

// EqChar is a character in a typeface, with its embellishments if any.
func EqChar(face byte, code uint16, embell ...byte) []byte {
	c := Cat([]byte{2, 0x80 + face}, U16(code))
	if len(embell) > 0 {
		// One record of embellishments, ended by a zero, and the end of
		// the list.
		c[0] |= 0x20
		c = Cat(c, []byte{6}, embell, []byte{0, 0})
	}
	return c
}

// EqString writes the characters of a string in one typeface.
func EqString(face byte, s string) []byte {
	var out []byte
	for _, r := range s {
		out = append(out, EqChar(face, uint16(r))...)
	}
	return out
}

// EqTmpl is a template: its selector, its variation, and its slots and
// signs.
func EqTmpl(selector, variation byte, records ...[]byte) []byte {
	return Cat([]byte{3, selector, variation, 0}, Cat(records...), []byte{0})
}

// EqPile is lines one above another.
func EqPile(lines ...[]byte) []byte { return Cat([]byte{4, 1, 1}, Cat(lines...), []byte{0}) }

// EqMatrix is a matrix of the given size: a line for each cell, by rows.
func EqMatrix(rows, cols byte, cells ...[]byte) []byte {
	parts := func(n byte) []byte { return make([]byte, (2*(int(n)+1)+7)/8) }
	return Cat([]byte{5, 0, 1, 1, rows, cols}, parts(rows), parts(cols), Cat(cells...), []byte{0})
}
