# Changelog

## Unreleased

In development; nothing is released. `IMPLEMENTATION_PLAN.md` has the phases
and what each found.

- Phase 0: the module, the packages copied from the other go-fast libraries,
  a writer of compound files, the telling apart of formats, generations and
  encryption, the test corpus and the baselines of today's tools. Every file
  is still refused with `ErrUnsupported`.
- Phase 1: what the three formats share. A file gives a document with its
  properties (`Metadata`), the source of its VBA modules (stored part) and
  its embedded files (`Children`); the text is not read yet, and the part
  says so. `Open` and `File` read a part at a time; the `gofastmsbin`
  command prints JSON, Markdown or text. A compound file with a wrong
  byte-order mark or a broken directory chain is read as far as it is sound.
- Phase 2: Excel workbooks (BIFF8). Cells as shown, through their number
  formats; tables found as go-fast-ooxml finds them; merged cells, comments,
  hyperlinks, hidden sheets, rows and columns; cell types and stored values
  with `Options.Cells`; sheets read in parallel; `Extractor.MaxCells`.
- Phase 3: the rest of Excel. Formulas as text, defined names, macro sheets,
  links to other workbooks, drop-down lists, text boxes, pictures, charts,
  pivot tables, and workbooks of Excel 5 and 95. The command has `-cells`.
- Phases 4 and 5: Word documents. Text and structure (headings, lists with
  labels, tables, footnotes, endnotes, comments, fields, headers and
  footers, bookmarks), hidden text and spans, tracked deletions, text boxes,
  pictures, document variables; and the text, headings, tables and footnotes
  of Word 6/95 documents.
