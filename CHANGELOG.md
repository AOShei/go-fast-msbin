# Changelog

What changes for someone who uses the library. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). Before 1.0 a
minor version may change the API.

## [Unreleased]

The first release, to be 0.1.0. Requires `go-fast-core` v0.1.1; the command
also requires `go-fast-ooxml` v0.1.1. `IMPLEMENTATION_PLAN.md` has what each
phase of the work found and measured.

### Added

- **Excel workbooks** (`.xls`: Excel 97-2003, and Excel 5 and 95). One
  `sheet` part per sheet; cells as shown, through their number formats;
  tables found as go-fast-ooxml finds them; merged cells, comments,
  hyperlinks; formulas as text, cell types and stored values with
  `Options.Cells`; drop-down lists, text boxes, pictures, charts, pivot
  tables; defined names and macro sheets in the stored part; links to other
  workbooks reported. Sheets are read in parallel.
- **Word documents** (`.doc`: Word 97-2003). One `body` part: headings, list
  items with their labels, tables with merged cells, footnotes, endnotes,
  comments, tracked deletions, fields, links, headers and footers,
  bookmarks, text boxes, pictures, document variables; spans with
  `Options.Spans`. Of Word 6 and 95 documents: text, headings, tables and
  footnotes.
- **PowerPoint presentations** (`.ppt`: PowerPoint 97-2003). One `slide`
  part per slide with its size and the place of every block: titles,
  bulleted and numbered text by the master's styles, text boxes, tables,
  pictures, notes, comments, links; what a master puts on every slide;
  Microsoft Graph charts from their datasheets. Slides are read in parallel.
- **Properties** as `Metadata`; **VBA modules** as code in the stored part,
  never run; **embedded files** as `Children`, through `Options.Child`.
- **Encrypted files**, with `Options.Password`: XOR obfuscation, RC4 and RC4
  CryptoAPI. `ErrPasswordRequired`, `ErrWrongPassword`; the password Excel
  uses when none is set is tried unasked; the warning `msbin.decrypted`.
- **What a file declares of display**: `hidden` for a hidden sheet, row,
  column, slide or shape, hidden text, and a cell its format shows as
  nothing.
- **Damaged files** read as far as they are sound, with `input.repaired`: a
  wrong byte-order mark, a broken directory chain, a stream that breaks off,
  a presentation whose Current User stream leads nowhere.
- **`Open` and `File`**: the parts one at a time (`Parts`, `Part`),
  `Sheets`, `Children`, `Metadata`, `Warnings`.
- **The `gofastmsbin` command**: JSON, Markdown or text for files and
  directories; embedded files of the binary formats and of Office Open XML
  are read too.
- **Limits**: `Options.Limits` (input, decoded bytes, text, depth) and
  `Extractor.MaxCells`; fixed bounds reported with `limit.exceeded`.
  `SECURITY.md` lists them.

### Not read

- Excel 2 to 4, Word 2 and older, and PowerPoint 95 and older: refused, with
  an error that says which.
- The text of earlier saves that the file as it stands no longer points to.
- Equations (listed as embedded files), sound and video, form-field
  drop-down entries in Word, the pivot caches of sources that are gone, the
  text boxes of Excel 5/95, and in Word 6/95 documents lists, hidden text
  and tracked changes.
- The properties of an encrypted presentation.
