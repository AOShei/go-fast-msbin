// Package gofastmsbin extracts the binary Office formats into the shared
// go-fast document model, gofastcore.Document: Word documents (.doc),
// Excel workbooks (.xls) and PowerPoint presentations (.ppt) as Office 97
// to 2003 wrote them, with their templates and shows. Their
// gofastcore.Source.Format is doc, xls and ppt.
//
// The library is in development. A file of any of the three formats gives
// a document with its properties, the source of its macros (in the stored
// part) and its embedded files (Children); the text is not read yet, and
// the document's part says so with part.failed. IMPLEMENTATION_PLAN.md
// says what is to come, phase by phase.
//
// The format is decided from the file, never from its name: all three are
// compound files, told apart by the streams they hold.
package gofastmsbin
