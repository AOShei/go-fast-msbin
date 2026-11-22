// Package warn holds the warning codes this library raises beyond the ones
// every go-fast library shares (those are in gofastcore), and what the
// readers share in saying what a fixed bound left out. Each code states a
// fact about the file. They are documented where the public package
// declares them.
package warn

import (
	"errors"
	"strconv"
)

const (
	UnreadContent       = "msbin.unread_content"
	ControlCharsRemoved = "msbin.control_characters_removed"
	PropertiesDamaged   = "msbin.properties_damaged"
	PartDamaged         = "msbin.part_damaged"
)

// ErrText is what a reader ends with when the document has taken all the
// text it may hold (gofastcore.Limits.MaxTextBytes).
var ErrText = errors.New("the limit on the amount of text was reached")

// TextCut is the message of the limit.exceeded warning on a part cut off
// for that reason; what stands for "part" is the kind of part.
func TextCut(noun string) string {
	return "the limit on the amount of text was reached; the " + noun + " is cut off there"
}

// Unmapped is the message of gofastcore.WarnTextUnmappedGlyphs: n codes of
// a symbol font that have no character in Unicode.
func Unmapped(n int) string {
	return strconv.Itoa(n) + " characters of a symbol font that have no character in Unicode appear as U+FFFD"
}
