package mtef

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
	"unicode/utf8"

	t "github.com/AOShei/go-fast-msbin/internal/msbintest"
)

func linear(tt *testing.T, what string, b []byte, want string) {
	tt.Helper()
	got, unmapped, ok := Linear(b)
	if !ok || got != want || unmapped != 0 {
		tt.Errorf("%s: %q (unmapped %d, ok %v), want %q", what, got, unmapped, ok, want)
	}
}

func v(s string) []byte  { return t.EqString(t.EqVariable, s) }
func n(s string) []byte  { return t.EqString(t.EqNumber, s) }
func op(s string) []byte { return t.EqString(t.EqSymbol, s) }

// An equation of the Equation Editor is written in the linear form: a
// fraction with a bar, a root, scripts, the sign of a sum with its
// limits; a part of more than one symbol is put in parentheses.
func TestLinear(tt *testing.T) {
	sup := func(s []byte) []byte { return t.EqTmpl(15, 0, t.EqNull(), t.EqLine(s)) }
	sub := func(s []byte) []byte { return t.EqTmpl(15, 1, t.EqLine(s), t.EqNull()) }
	linear(tt, "the roots of a quadratic", t.MTEF3(t.EqLine(
		v("x"), op("="),
		t.EqTmpl(14, 0,
			t.EqLine(op("−"), v("b"), op("±"), t.EqTmpl(13, 0, t.EqLine(v("b"), sup(n("2")), op("−"), n("4"), v("ac")), t.EqNull())),
			t.EqLine(n("2"), v("a"))),
	)), "x=(−b±√(b^2−4ac))/(2a)")
	linear(tt, "a sum with its limits", t.MTEF3(t.EqLine(
		t.EqTmpl(29, 1, t.EqLine(v("x"), sub(v("i"))), t.EqLine(v("i"), op("="), n("1")), t.EqLine(v("n")), t.EqChar(t.EqSymbol, '∑')),
	)), "∑_(i=1)^n▒(x_i)")
	linear(tt, "an integral with no limits", t.MTEF3(t.EqLine(
		t.EqTmpl(21, 0, t.EqLine(v("f"), v("dx")), t.EqNull(), t.EqNull(), t.EqChar(t.EqSymbol, '∫')),
	)), "∫▒(fdx)")
	linear(tt, "both scripts, and what follows them", t.MTEF3(t.EqLine(
		v("x"), t.EqTmpl(15, 2, t.EqLine(v("k")), t.EqLine(n("2"))), v("a"), sup(v("n")), op("+"), n("1"),
	)), "x_k^2 a^n+1")
	linear(tt, "roots of a degree", t.MTEF3(t.EqLine(
		t.EqTmpl(13, 1, t.EqLine(v("x")), t.EqLine(n("3"))), t.EqTmpl(13, 1, t.EqLine(v("x"), op("+"), n("1")), t.EqLine(v("n"))),
	)), "∛x√(n&x+1)")
	linear(tt, "a fraction on one line, bars", t.MTEF3(t.EqLine(
		t.EqTmpl(41, 0, t.EqLine(n("2")), t.EqLine(n("3"))), t.EqTmpl(17, 0, t.EqLine(v("ab"))), t.EqTmpl(16, 0, t.EqLine(v("c"))),
	)), "2∕3¯(ab)▁c")
	// The brackets of a fence are in its list as the pieces they are
	// drawn with; the selector says what they are.
	fence := func(sel, variation byte, s []byte) []byte {
		return t.EqTmpl(sel, variation, t.EqLine(s), t.EqChar(22, 0xF8EB), t.EqChar(22, 0xF8F6))
	}
	linear(tt, "fences", t.MTEF3(t.EqLine(
		fence(1, 0, v("a")), fence(3, 0, v("b")), fence(2, 1, v("c")), fence(4, 0, v("d")), fence(0, 0, v("e")), fence(7, 2, v("f")),
	)), "(a)[b]{c|d|⟨e⟩f⌉")
	linear(tt, "a matrix in brackets, and a pile", t.MTEF3(
		t.EqLine(fence(1, 0, t.EqMatrix(2, 2, t.EqLine(v("a")), t.EqLine(v("b")), t.EqLine(v("c")), t.EqLine(v("d"))))),
		t.EqPile(t.EqLine(v("x"), op("="), n("1")), t.EqLine(v("y"), op("="), n("2"))),
	), "(■(a&b@c&d))\n█(x=1@y=2)")
	linear(tt, "text, and the name of a function", t.MTEF3(t.EqLine(
		t.EqString(t.EqText, "if "), t.EqString(t.EqFunction, "sin"), v("θ"), op(">"), n("0"),
		t.EqString(t.EqFunction, "cos"), sup(n("2")), v("θ"),
	)), "\"if \"sin\u2061θ>0cos^2 θ")
	linear(tt, "embellishments", t.MTEF3(t.EqLine(
		t.EqChar(t.EqVariable, 'x', 2), t.EqChar(t.EqVariable, 'y', 5, 5), t.EqChar(t.EqVariable, 'v', 11), t.EqChar(t.EqVariable, 'z', 17),
	)), "x\u0307y′′v\u20D7z\u0305")
	linear(tt, "an operator the author wrote, with its limits", t.MTEF3(t.EqLine(
		t.EqTmpl(43, 2, t.EqNull(), t.EqLine(v("y"), op("="), n("1")), t.EqLine(n("34")), t.EqLine(v("Σ"))), v("y"),
		t.EqTmpl(42, 1, t.EqNull(), t.EqLine(v("fast")), t.EqNull(), t.EqLine(t.EqChar(t.EqVariable, 'p', 11))),
	)), "Σ_(y=1)^34 yp\u20D7_(fast)")
	linear(tt, "a limit, and a template with no form of its own", t.MTEF3(t.EqLine(
		t.EqTmpl(39, 0, t.EqLine(t.EqString(t.EqFunction, "lim")), t.EqLine(v("n"), op("→"), op("∞")), t.EqNull()), v("a"),
		t.EqTmpl(45, 0, t.EqLine(v("a")), t.EqLine(v("bc")), t.EqChar(t.EqSymbol, '⟨'), t.EqChar(t.EqSymbol, '|'), t.EqChar(t.EqSymbol, '⟩')),
	)), "lim┬(n→∞) aa(bc)⟨|⟩")
	// MathType's spaces, and a mark for alignment.
	linear(tt, "spaces", t.MTEF3(t.EqLine(v("a"), t.EqChar(24, 0xEB04), t.EqChar(24, 0xEB00), v("b"), t.EqChar(24, 0xEB01))), "a b")
	if got, unmapped, ok := Linear(t.MTEF3(t.EqLine(v("a"), t.EqChar(t.EqSymbol, 0xE123)))); !ok || got != "a�" || unmapped != 1 {
		tt.Errorf("a character Unicode has nothing for: %q, %d, %v", got, unmapped, ok)
	}
	// Records of size, of a font, a ruler and a nudge are read past.
	linear(tt, "what is not text", t.MTEF3(
		[]byte{9, 101, 12, 0}, []byte{9, 100, 1, 2, 0}, []byte{9, 3, 130}, []byte{8, 0x81, 0}, []byte("Times\x00"),
		t.Cat([]byte{0x21, 7, 1, 0, 10, 0}, v("a"), []byte{0x82, 0x83, 130, 131, 'b', 0, 0x82, 0x83, 128, 128, 1, 0, 2, 0, 'c', 0, 0}),
		[]byte{0x41, 5, 0}, v("d"), []byte{0},
	), "abc\nd")
}

func unhex(tt *testing.T, s string) []byte {
	tt.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		tt.Fatal(err)
	}
	return b
}

// Equations as files hold them, in the three versions that are read.
func TestLinearOfFiles(tt *testing.T) {
	// Version 3, the Equation Editor: vectors with scripts, one of them
	// written with the template of an operator.
	linear(tt, "version 3", unhex(tt, "03 01 01 03 0a 0a 01 32 83 7a 00 06 0e 00 00 02 86 3d 00 03 2a 01 00 11 0b 01 12 83 66 00 12 83 61 00 12 83 73 00 12 83 74 00 00 11 0a 01 32 83 70 00 06 0e 00 00 00 00 02 86 12 22 32 83 70 00 06 0e 00 00 03 0f 01 00 0b 01 12 83 62 00 12 83 65 00 12 83 61 00 12 83 6d 00 00 11 00 00 00"),
		"z\u20D1=p\u20D1_(fast)−p\u20D1_(beam)")
	// Version 2: a character is its place in a font, and the brackets of
	// a ceiling are pieces of the Symbol font.
	linear(tt, "version 2", unhex(tt, "02 00 01 0a 00 0a 01 12 83 4f 03 01 00 00 01 12 83 54 03 07 00 00 01 03 0e 00 00 01 12 83 44 00 01 12 83 46 00 00 00 02 96 e9 02 96 f9 00 02 82 28 02 88 32 03 0f 00 00 0b 11 01 12 83 46 00 00 0a 02 86 2d 02 88 31 02 82 29 00 02 96 28 02 96 29 00 00 00"),
		"O(T⌈D/F⌉(2^F−1))")
	// Version 5, MathType: preferences, fonts and colours before the
	// equation; an embellished character; a subscript.
	v5 := t.Cat(
		[]byte{5, 1, 0, 6, 0}, []byte("DSMT6\x00"), []byte{0},
		[]byte{18, 0, 2, 0x21, 0x2F, 0x00, 0xA5, 0xF0, 1, 0x21, 0xF0, 2, 0, 1, 2}, // preferences: two sizes, a space, two styles
		[]byte{19}, []byte("WinAllBasicCodePages\x00"), []byte{17, 0}, []byte("Times New Roman\x00"),
		[]byte{8, 1, 0}, []byte{16, 0, 0, 0, 0, 0, 0, 0}, []byte{15, 0}, []byte{200, 2, 9, 9}, // a style, a colour, and a record of a later version
		[]byte{10, 1, 0},
		[]byte{2, 1, 0x83, 0x71, 0, 6, 0, 0x0B, 0},                                                             // q with an arrow
		[]byte{2, 4, 0x86, 0x3D, 0, 0x3D},                                                                      // = and its place in a font
		[]byte{2, 0, 0x84, 0xB4, 0x03},                                                                         // δ
		[]byte{3, 0, 27, 0, 0, 1, 0}, []byte{2, 0, 0x83, 'i', 0, 2, 0, 0x83, 'r', 0, 0}, []byte{0x01, 0x01, 0}, // a subscript: its slot, and one that is empty
		[]byte{3, 0, 1, 3, 0, 1, 0, 2, 0, 0x88, '1', 0, 0, 2, 0, 0x96, '(', 0, 2, 0, 0x96, ')', 0, 0},
		[]byte{0, 0},
	)
	linear(tt, "version 5", v5, "q\u20D7=δ_(ir) (1)")
}

// What is not an equation that can be read is refused: nothing is made
// of records that break off or do not follow one another.
func TestLinearRefuses(tt *testing.T) {
	whole := t.MTEF3(t.EqLine(v("x"), t.EqTmpl(14, 0, t.EqLine(v("a")), t.EqLine(v("b")))))
	for _, b := range [][]byte{nil, {3, 1, 1}, {4, 1, 1, 3, 0, 1, 0, 0}, {1, 1, 1, 3, 0}, whole[:len(whole)-9], t.MTEF3([]byte{0x0F})} {
		if got, _, ok := Linear(b); ok || got != "" {
			tt.Errorf("% x: %q, %v", b, got, ok)
		}
	}
	// Nested deeper than is read.
	deep := []byte{3, 1, 1, 3, 0}
	for i := 0; i < 100000; i++ {
		deep = append(deep, 1, 3, 14, 0, 0)
	}
	if _, _, ok := Linear(deep); ok {
		tt.Error("records nested 200,000 deep are read")
	}
	// An equation with nothing in it is one, and empty.
	if got, _, ok := Linear(t.MTEF3(t.EqLine())); !ok || got != "" {
		tt.Errorf("an empty equation: %q, %v", got, ok)
	}
	// The stream: a header that says how long it is.
	if b, ok := Native(t.EquationNative(whole)); !ok || !bytes.Equal(b, whole) {
		tt.Error("the stream of an equation")
	}
	for _, b := range [][]byte{nil, {28, 0}, make([]byte, 28), t.EquationNative(nil)} {
		if _, ok := Native(b); ok {
			tt.Errorf("a stream of % x is taken for an equation's", b)
		}
	}
}

// FuzzLinear: whatever the bytes, the reader returns, with text that is
// valid and no longer than a few times what it was given.
func FuzzLinear(f *testing.F) {
	f.Add(t.MTEF3(t.EqLine(v("x"), t.EqTmpl(14, 0, t.EqLine(v("a")), t.EqLine(v("b"))), t.EqChar(t.EqVariable, 'y', 5))))
	f.Add(t.MTEF3(t.EqPile(t.EqLine(t.EqMatrix(1, 2, t.EqLine(v("a")), t.EqLine(v("b")))), t.EqLine(t.EqTmpl(29, 0, t.EqLine(v("s")), t.EqNull(), t.EqNull())))))
	f.Add([]byte{5, 1, 0, 6, 0, 'D', 0, 0, 18, 0, 1, 0x21, 0xF0, 0, 0, 1, 0, 2, 1, 0x83, 0x71, 0, 6, 0, 11, 0, 3, 0, 27, 0, 0, 1, 0, 2, 0, 0x83, 'i', 0, 0, 0, 0, 0})
	f.Add([]byte{2, 0, 1, 10, 0, 10, 1, 0x12, 0x83, 0x4F, 2, 0x86, 0x2D, 0, 0})
	f.Fuzz(func(tt *testing.T, b []byte) {
		text, unmapped, ok := Linear(b)
		if !ok && (text != "" || unmapped != 0) {
			tt.Fatalf("refused, with %q and %d", text, unmapped)
		}
		if !utf8.ValidString(text) {
			tt.Fatalf("text that is not valid: %q", text)
		}
		if len(text) > 16*len(b)+16 {
			tt.Fatalf("%d bytes of text from %d", len(text), len(b))
		}
	})
}
