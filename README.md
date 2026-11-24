# go-fast-msbin

Extracts the binary Office formats (Word `.doc`, Excel `.xls` and PowerPoint
`.ppt`, as Office 97 to 2003 wrote them) into the shared go-fast document
model, [`gofastcore.Document`](https://github.com/AOShei/go-fast-core).
Standard library and go-fast modules only.

**Status: v0.1.2.** The three
formats are read, encrypted files with their password.
[`SECURITY.md`](SECURITY.md) says what a hostile file cannot do and lists the
limits; [`IMPLEMENTATION_PLAN.md`](IMPLEMENTATION_PLAN.md) has what each phase
of the work found and measured; [`CHANGELOG.md`](CHANGELOG.md) what the
release holds.

| Format | Extensions | `Source.Format` | State |
|---|---|---|---|
| Excel 97-2003 (BIFF8), Excel 5/95 (BIFF5) | `.xls`, `.xlt`, `.xla` | `xls` | read |
| Word 97-2003 | `.doc`, `.dot` | `doc` | read |
| Word 6/95 | `.doc` | `doc` | text, headings, tables, footnotes; no lists, hidden text or tracked changes |
| PowerPoint 97-2003 | `.ppt`, `.pps`, `.pot` | `ppt` | read |

What is read:

- **Workbooks**: one `sheet` part per sheet, in the workbook's order, named
  as the sheet is. Cells are given as they are shown, through their number
  formats, with dates in ISO 8601; the cells of a sheet make `table` blocks
  with their place (`Loc.Range`), found as go-fast-ooxml finds them, so that
  a workbook saved in either format gives the same tables. Merged cells,
  comments (`note`, `comment`) and, with `Options.Spans`, hyperlinks are
  read; with `Options.Cells` the type of each value and the stored value
  where the text does not show it in full. Text in a symbol font is
  translated, run by run. Declared: `hidden` for a hidden sheet, hidden rows
  and columns, and a value its format shows as nothing. Sheets are read in
  parallel; the result does not depend on how many at a time.
  Beyond cells: formulas as text (`Options.Cells`), drop-down lists (`note`,
  `choices`), text boxes, pictures (`image`), charts (a `caption` and a
  `table` with `role` `chart`, as go-fast-ooxml gives them), pivot tables
  (`role` `pivot`). The stored part has the defined names and the formulas
  of macro sheets. Links to other workbooks are `input.external_reference`.
  Not read: the records of a pivot cache whose source is gone, and the text
  boxes of Excel 5/95.
- **Word documents**: one `body` part. Headings, list items with their
  labels, quotations and code, by go-fast-ooxml's rules; tables with merged
  cells and header rows; footnotes and endnotes with their numbers;
  comments; tracked deletions as notes; the results of fields as text and
  their codes as notes; links; headers and footers once each; bookmarks as
  anchors; text boxes at their anchors; pictures; document variables in the
  stored part. With `Options.Spans`: bold, italic, underline, strike-through,
  super- and subscript. Declared: `hidden` for hidden text and hidden shapes.
- **Presentations**: one `slide` part per slide, in the order of the show,
  with the size of a slide in points and the place of every block on it
  (`Loc.BBox`). The title is a `heading` and the part's name; the body's
  paragraphs are list items with their level and their bullet or number, as
  the slide's master and the paragraph say; text boxes are paragraphs; a
  table is a `table`; pictures are `image` blocks; speaker's notes and
  comments are `note` blocks. What a master puts on every slide that follows
  it (its own text and pictures, and the date, the footer and the number of
  the slide where the presentation says they are shown) is given with each
  slide, as `header` and `footer`. The order is go-fast-ooxml's: the title,
  then from the top down and from the left across. A Microsoft Graph chart is
  read with the slide that shows it, from its datasheet, as a chart's table.
  With `Options.Spans`, links. Declared: `hidden` for a slide left out of the
  show and for a hidden shape. The presentation is what its last edit left
  of it; slides are read in parallel. A file saved for PowerPoint 95 and 97
  both is read from the PowerPoint 97 copy it holds. Not read: sound and
  video, and tables that are embedded Word or Excel objects (read as
  embedded files).
- **Equations** of the Equation Editor and of MathType, in Word documents
  and presentations, in UnicodeMath, the linear form go-fast-ooxml writes
  the equations of the newer formats in: `x=(−b±√(b^2−4ac))/(2a)`,
  `∑_(i=1)^n▒(x_i)`, `■(a&b@c&d)`. Every character of the equation is kept,
  in order. In a Word document an equation is text of its paragraph, and a
  paragraph that is an equation and nothing else is a `formula` block with
  `notation` `unicodemath`; on a slide an equation is a `formula` block
  where its picture stands. An equation that is read is not listed among
  the embedded files; one that cannot be read stays a picture and an
  embedded file. A character Unicode has nothing for is U+FFFD, with
  `text.unmapped_glyphs`. Not read: equations in workbooks and in Word 6/95
  documents (embedded files, as before).
- **Properties** (`Metadata`), from the two property-set streams: title,
  subject, author, keywords, dates, application; comments, template, last
  author, revision, category, manager, company and the program's version in
  `Extra`, under the keys go-fast-ooxml uses; the properties the author
  defined as `custom.<name>`. Text is decoded from the code page the stream
  names.
- **Macros**: a VBA project is `input.active_content`, and the source of each
  module is a `code` block in the stored part (`stored` `macro`, `language`
  `vba`). Nothing is run.
- **Embedded files** (`Children`, through `Options.Child`): a file wrapped by
  the object packager, an Office Open XML package, the content another
  program stored, and a document held as a storage of the file (a workbook in
  a Word document), which is written out as a compound file of its own. A
  linked object is `input.external_reference`; nothing is fetched. Without
  `Options.Child` the files are listed, with their names, kinds and sizes. A
  presentation keeps its embedded files compressed, and one that is only
  listed is expanded no further than it takes to say what it is: its size is
  then the size it states, which for a file whose compressed data breaks off
  is more than could be expanded.
- **Encrypted files**, with `Options.Password`: XOR obfuscation, the RC4
  encryption of Office 97 and RC4 with a CryptoAPI key, for the three
  formats. The password is checked against what the file keeps for that
  before anything is decrypted: without one the error wraps
  `ErrPasswordRequired`, with a wrong one `ErrWrongPassword`. A workbook
  encrypted with the password Excel uses when none was set (a protected
  workbook with no password to open it) is read without being given one. The
  document's `Metadata.Encrypted` is set, whoever's password opened the
  file, and it has the warning `input.decrypted`, which names the scheme and
  says which password that was. These
  schemes are weak: they are read because files kept in them exist. Each
  is checked against files other programs wrote, but for the XOR obfuscation
  of Word documents, of which none was found: there, LibreOffice opens what
  this project's test writer obfuscates, with the same password. The
  properties of an encrypted presentation, which are kept encrypted in a
  stream of their own, are read from it. Not read: the properties of a
  workbook or Word document that encrypts them in the same way (none was
  found to check a reader against); such a file has the properties its
  plain streams hold.
- **Damaged containers**: a wrong byte-order mark, a directory whose chain of
  sectors breaks and lost short streams are read past, with `input.repaired`.

The format is decided from the file, never from its name: all three are
compound files, told apart by the streams they hold. Excel 2 to 4, Word 2 and
older and PowerPoint 95 and older are not read; neither is a file of
another format under one of these names (RTF, HTML, a zip package). Each is
refused with a message that says which it is.

## Known limitations

- **The text of earlier saves is not read.** A Word document saved with
  "fast save" and a presentation saved several times can still hold, in
  bytes the document as it stands no longer points to, text that was later
  deleted or replaced. The library reads the document as its last save left
  it and gives none of that text, and nothing in the output says whether a
  file holds any. This was decided, not overlooked: it will be looked at
  again if someone needs it.
- What each format's entry above lists as "Not read". Where the library can
  tell that a file holds such content it says so with
  `input.unread_content` (see below).

## Warnings

Every warning has one of the codes all go-fast libraries share (go-fast-core's
README, "Warnings"); this library has none of its own. Besides the ones about
parts, limits and embedded documents it raises:

| Code | Here |
|---|---|
| `input.repaired` | The compound file, or a stream of it, is damaged and was read as far as it could be; a presentation's chain of edits was found by search |
| `input.stream_damaged` | A stream other than the main one could not be read to its end, and what was read of it is used: the properties (the message names the stream), a VBA project, an embedded object, a chart |
| `input.unread_content` | Content of a kind that is not read; the message says what and how many: dialog sheets, formulas that cannot be turned back into text, the text boxes of Excel 5/95, the sounds and videos of a presentation, the drop-down lists of a Word form (their entries; what is chosen is text). Not counted: a pivot cache whose source is gone, and form drop-downs in Word 6/95 documents |
| `input.decrypted` | The file was encrypted and has been decrypted; `Metadata.Encrypted` is set. The message names the scheme and which password opened it |
| `input.active_content` | A VBA project, macro sheets, or an action that starts a program; nothing is run |
| `input.external_reference` | A linked object or file; nothing is fetched |
| `text.unmapped_glyphs` | Characters that Unicode, the code page or a symbol font has nothing for |
| `text.control_characters_removed` | Control characters with no meaning as text were removed; the message says how many |
| `text.tracked_changes` | A Word document has tracked changes |
| `structure.heading_level_clamped` | Headings deeper than six levels |
| `table.region_split` | Areas of a sheet too thinly filled to be tables are given as cells |

## Using it

```go
doc, err := gofastmsbin.ExtractFile(ctx, "report.doc", nil)          // a whole file

f, err := gofastmsbin.Open("ledger.xls", &gofastcore.Options{Password: "sesame"})
defer f.Close()
for _, sheet := range f.Sheets() { ... }                             // listed without being read
for part, err := range f.Parts(ctx) { ... }                          // a sheet or a slide at a time
```

`Extract` and `ExtractFile` give a `gofastcore.Document`. `Open` gives a
`File`: its `Metadata`, its parts one at a time (`Parts`, `Part`) or as a
`Document`, its embedded files (`Children`), and for a workbook its `Sheets`.
`Extractor.MaxCells` bounds the cells kept from a workbook. The exported
declarations are held to `testdata/api.txt` by a test.

The command prints JSON (the document, a line for each file when there are
several), Markdown or text:

```sh
go install github.com/AOShei/go-fast-msbin/cmd/gofastmsbin@latest
gofastmsbin report.doc                    # JSON
gofastmsbin -format markdown slides.ppt
gofastmsbin -password sesame -cells ledger.xls
gofastmsbin -quiet archive/               # every .doc, .xls and .ppt file below a directory
```

It reads the files embedded in a document too: those of the binary formats
with this library, and Office Open XML files (`.docx`, `.xlsx`, `.pptx`) with
[go-fast-ooxml](https://github.com/AOShei/go-fast-ooxml), told apart by their
first bytes. The library itself imports the standard library and go-fast-core
only; a program that uses it decides what reads embedded files, through
`Options.Child`.

## Layout

| Path | Content |
|---|---|
| `extract.go`, `file.go` | The public entry points: `Extract`, `ExtractFile`, `Extractor`; `Open` and `File`, for a part at a time |
| `cmd/gofastmsbin` | The command: JSON, Markdown or text for files and directories; embedded Office Open XML files read with go-fast-ooxml |
| `internal/xls` | The Excel reader: records, shared strings, cell formats, sheets, formulas, lists, shapes, charts |
| `internal/doc` | The Word reader: pieces, properties, styles, lists, tables, notes, fields, text boxes, pictures; Word 6/95 |
| `internal/ppt` | The PowerPoint reader: edits and the persist directory, masters and their text styles, slides, shapes, text, notes, comments |
| `internal/hidden` | Spans and what is declared about the display of text, settled on a block |
| `internal/ptg` | Compiled formulas back to text |
| `internal/officeart`, `internal/media` | The drawing layer the three formats share: shapes, their properties, pictures |
| `internal/chart` | A chart's data as a caption and a table |
| `internal/grid` | The cells of a sheet into table blocks, as go-fast-ooxml does it |
| `internal/crypt` | The three schemes of encryption: a password checked, and the keys it gives |
| `internal/oleps` | Property sets: the document's properties |
| `internal/vba`, `internal/stored` | The source of VBA modules, and the stored part they go to |
| `internal/embed` | Embedded objects, handed on as files |
| `internal/mtef` | Equations of the Equation Editor and MathType (MTEF 2, 3 and 5), written in UnicodeMath |
| `internal/unit`, `internal/warn` | What the readers of the three formats share: the stream they read from, a part's result, the wording of bounds and repeated warnings |
| `internal/cfb` | Compound files, the container of all three formats: sectors, allocation tables, directory, streams; writing one, and writing a storage of a file out as a file of its own |
| `internal/detect` | What a file is: format, generation, encryption |
| `internal/codepage`, `internal/numfmt`, `internal/symbol`, `internal/label`, `internal/clean` | Copied from the other go-fast libraries: code pages, number formats, symbol fonts, list labels, the text conventions of the shared model |
| `internal/msbintest` | Test harness: a second writer of compound files; writers of property sets, VBA projects, workbooks, Word documents and presentations, and of the three schemes of encryption; the corpus manifest |
| `testdata/oracle_shared.py`, `oracle_xls.py`, `oracle_lo.py`, `oracle_crypt.py` | What olefile and olevba read of each corpus file, what xlrd reads of each workbook and what LibreOffice shows of it, for the tests that compare the library with them; and LibreOffice opening, with a password, what the test writer encrypts |
| `testdata/corpus.json`, `testdata/fetch.py` | Manifest of 2,528 real files from the test suites of Apache POI, Apache Tika and LibreOffice, from data.gov.uk and from Govdocs1, and the script that downloads them. The files are not in the repository. One file in five is held out: scored, never looked at |
| `bench` | The baselines of the tools in use today (`baseline.md`) and the script that measures them |

## Running the tests

```sh
go test ./...
python3 testdata/fetch.py && go test -run TestCorpus -v .   # the corpus, and what it is made of
python -I testdata/oracle_shared.py testdata testdata/corpus/oracle-shared.jsonl   # needs olefile and oletools
go test -run TestSharedAgainstOracles -v .
python -I testdata/oracle_xls.py testdata testdata/corpus/oracle-xls           # needs xlrd
python3 testdata/oracle_lo.py testdata testdata/corpus/oracle-lo-xls          # needs LibreOffice; contained
python3 testdata/oracle_lo.py testdata testdata/corpus/twins-xls --twin       # each workbook as .xlsx
python3 testdata/oracle_lo.py testdata testdata/corpus/twins-doc --twin --format doc
python3 testdata/oracle_lo.py testdata testdata/corpus/oracle-lo-doc --text --format doc
go test -run 'TestDocTwins|TestDocTextAgainstLibreOffice' -v .
python3 testdata/oracle_lo.py testdata testdata/corpus/twins-ppt --twin --format ppt
go test -run 'TestPptTwins|TestEquationTwins' -v .
go test -run 'TestCellsAgainstXlrd|TestShownAgainstLibreOffice|TestTwins|TestFormulasAgainstTwins' -v .
go test -run '^$' -bench Large .                                               # needs bench/baseline.py generate
go test -run '^$' -bench Corpus .                                              # every file of a format, as a batch
go test -run 'TestSoak|TestAllocationBudgets' -v .                             # the whole corpus four ways; what the large files allocate
go test -run TestWorkbookRegions -v .                                          # every workbook read both ways: its stream in memory, a sheet at a time
go run ./cmd/gofastmsbin file.doc
GOFAST_MSBIN_LIBREOFFICE=1 go test -run TestEncryptedWordAgainstLibreOffice . # LibreOffice opens what the test writer encrypts; contained
```

Tests that need the corpus or the Python comparison tools skip themselves when
those are absent.

## Licence

MIT. The corpus files keep their own licences, recorded per file in
`testdata/corpus.json`.
