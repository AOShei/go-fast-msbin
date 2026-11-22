// Package gofastmsbin extracts the binary Office formats into the shared
// go-fast document model, gofastcore.Document: Word documents (.doc),
// Excel workbooks (.xls) and PowerPoint presentations (.ppt) as Office 97
// to 2003 wrote them, with their templates and shows, and the workbooks
// of Excel 5 and 95 and the documents of Word 6 and 95. Their
// gofastcore.Source.Format is doc, xls and ppt.
//
// Extract and ExtractFile read a whole file into a Document: a body part
// for a Word document, a sheet part for each sheet of a workbook with its
// cells as tables, a slide part for each slide of a presentation. Open
// gives a File, from which the parts are read one at a time (Parts,
// Part) or as a Document; a workbook's sheets and a presentation's
// slides are read in parallel, and the result does not depend on how
// many at a time.
//
// The properties of the file are its Metadata. The source of its VBA
// modules, its defined names and the like are in a part of kind stored,
// the last. The files embedded in it are its Children, read by the
// extractor given as Options.Child.
//
// An encrypted file is opened with Options.Password.
//
// What the file declares of the display of its content (a hidden sheet,
// row, slide or shape; hidden text) is reported on the block or the
// part, and nothing is judged: content is given whether or not it is
// shown.
//
// The format is decided from the file, never from its name: all three are
// compound files, told apart by the streams they hold. A file of an
// older generation (Excel 2 to 4, Word 2, PowerPoint 95) or of another
// format is refused with an error that says what it is.
//
// The library has no dependencies beyond the standard library and
// go-fast-core. The gofastmsbin command also uses go-fast-ooxml, to read
// the Office Open XML files embedded in a document.
package gofastmsbin
