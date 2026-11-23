// Package unit is what the readers of the three formats have in common
// when a document is read a part at a time: a Word document's one body,
// a workbook's sheets, a presentation's slides.
package unit

import (
	"context"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/AOShei/go-fast-core"
	"github.com/AOShei/go-fast-msbin/internal/clean"
	"github.com/AOShei/go-fast-msbin/internal/warn"
)

// Stream is a stream of the file as a reader sees it: the bytes of a
// stream of the compound file, decrypted where the file is encrypted.
// The readers take their input through it, so that what lies underneath
// (the compound file, a cipher over it) is not their concern.
type Stream interface {
	io.ReaderAt
	Size() int64
}

// ReadAll reads a whole stream, or its first limit bytes if it is longer;
// cut says it was.
func ReadAll(s Stream, limit int64) (data []byte, cut bool, err error) {
	n := s.Size()
	if n > limit {
		n, cut = limit, true
	}
	data = make([]byte, n)
	got, err := s.ReadAt(data, 0)
	if got == len(data) {
		err = nil
	}
	return data[:got], cut, err
}

// Result is what reading one part gives: the part, and what reading it
// found out about the document as a whole.
type Result struct {
	Part gofastcore.Part
	Tally
}

// Tally is what reading one part found out about the document as a
// whole. It is small, and is kept when the part itself has been handed
// on.
type Tally struct {
	// Unread counts content of kinds that are not read: dialog sheets,
	// formulas that could not be turned back into text.
	Unread map[string]int
	// Damaged names what could not be read to its end.
	Damaged []string
	// External counts references to things outside the file other than
	// hyperlinks; Programs, actions that start a program or run a macro.
	External, Programs int
	// Macros says the part is a sheet of macros.
	Macros bool
	// Limited says a limit cut the reading short, in this part or in
	// something it shares with others. What was read up to a limit
	// depends on what else was being read at the time, so a document
	// with a part so marked is read again, in order.
	Limited bool
	// Warnings are warnings about the document that the reader has made
	// itself.
	Warnings []gofastcore.Warning
}

// Source is a document opened by the reader of its format. All its
// methods are safe for concurrent use.
type Source interface {
	// Len returns the number of parts.
	Len() int
	// Stored returns the index of the stored part (gofastcore.PartStored),
	// which is the last, or -1 if the document has none.
	Stored() int
	// Noun names a part of this format, in the plural: "sheets".
	Noun() string
	// Begin starts a pass over the document: the limits count from here.
	Begin()
	// Read reads the part with the given index, from 0. It always returns
	// a part: one that could not be read says so in its warnings.
	Read(ctx context.Context, index int) *Result
	// Limited reports whether a limit has cut short anything the parts
	// share since the source was opened.
	Limited() bool
	// Summary turns what was found out while the parts were read into
	// warnings about the document. tallies are those of the parts read,
	// in the order of the parts.
	Summary(tallies []*Tally) []gofastcore.Warning
}

// Summarize turns what the tallies hold, and the names of what else could
// not be read, into the warnings every format gives for it: links to
// files outside, content of kinds that are not read, and what could not
// be read to its end. Actions that start a program (Programs) and macro
// sheets (Macros) are worded by the format that has them.
func Summarize(tallies []*Tally, damaged []string) []gofastcore.Warning {
	var out []gofastcore.Warning
	unread := map[string]int{}
	damaged = append([]string(nil), damaged...)
	for _, t := range tallies {
		for kind, n := range t.Unread {
			unread[kind] += n
		}
		damaged = append(damaged, t.Damaged...)
		out = append(out, t.Warnings...)
	}
	external := 0
	for _, t := range tallies {
		external += t.External
	}
	if external > 0 {
		msg := strconv.Itoa(external) + " links to files outside the document; nothing was fetched"
		if external == 1 {
			msg = "1 link to a file outside the document; nothing was fetched"
		}
		out = append(out, gofastcore.Warning{Code: gofastcore.WarnInputExternalReference, Severity: gofastcore.SeverityWarning, Message: msg})
	}
	if len(unread) > 0 {
		kinds := make([]string, 0, len(unread))
		for kind, n := range unread {
			kinds = append(kinds, strconv.Itoa(n)+" "+kind)
		}
		sort.Strings(kinds)
		out = append(out, gofastcore.Warning{Code: gofastcore.WarnInputUnreadContent, Severity: gofastcore.SeverityWarning, Message: strings.Join(kinds, ", ")})
	}
	sort.Strings(damaged)
	var names warn.Names
	for _, name := range damaged {
		names.AddOnce(clean.Label(name))
	}
	if msg := names.Message(warn.DamagedOne, warn.DamagedMany); msg != "" {
		out = append(out, gofastcore.Warning{Code: gofastcore.WarnInputStreamDamaged, Severity: gofastcore.SeverityWarning, Message: msg})
	}
	return out
}
