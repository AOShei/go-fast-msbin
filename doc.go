// Package gofastmsbin extracts the binary Office formats into the shared
// go-fast document model, gofastcore.Document: Word documents (.doc),
// Excel workbooks (.xls) and PowerPoint presentations (.ppt) as Office 97
// to 2003 wrote them, with their templates and shows. Their
// gofastcore.Source.Format is doc, xls and ppt.
//
// The library is in development: it tells the three formats apart, their
// generations and whether a file is encrypted, and reads none of them
// yet. Every file is refused with an error that wraps
// gofastcore.ErrUnsupported and says what the file is. IMPLEMENTATION_PLAN.md
// says what is to come, phase by phase.
//
// The format is decided from the file, never from its name: all three are
// compound files, told apart by the streams they hold.
package gofastmsbin
