package codepage

import "strings"

// Decode turns text in the code page cp into UTF-8. undecoded counts the
// characters the page does not define, and the bytes above 0x7F of a page
// that is not known: each is U+FFFD.
func Decode(cp int, b []byte) (s string, undecoded int) {
	ascii := true
	for _, c := range b {
		if c >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return string(b), 0
	}
	var sb strings.Builder
	sb.Grow(len(b) + len(b)/2)
	var d Decoder
	emit := func(r rune, ok bool) {
		if !ok {
			undecoded++
			r = Unmapped
		}
		sb.WriteRune(r)
	}
	for _, c := range b {
		d.Byte(cp, c, emit)
	}
	d.Flush(emit)
	return sb.String(), undecoded
}
