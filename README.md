# go-fast-msbin

Extracts the binary Office formats (Word `.doc`, Excel `.xls` and PowerPoint
`.ppt`, as Office 97 to 2003 wrote them) into the shared go-fast document
model, [`gofastcore.Document`](https://github.com/AOShei/go-fast-core).
Standard library and go-fast modules only.

**Status: in development, nothing released.** Excel workbooks are read, of
Excel 97 to 2003 and of Excel 5 and 95. Of a Word document and a
presentation the properties, the macros and the embedded files are read, and
the text not yet: the one part of the document says so, with `part.failed`.
Encrypted files are refused for now.
[`IMPLEMENTATION_PLAN.md`](IMPLEMENTATION_PLAN.md) has the plan, phase by
phase, and what each phase found.

| Format | Extensions | `Source.Format` | State |
|---|---|---|---|
| Excel 97-2003 (BIFF8), Excel 5/95 (BIFF5) | `.xls`, `.xlt`, `.xla` | `xls` | read |
| Word 97-2003, Word 6/95 | `.doc`, `.dot` | `doc` | properties, macros, embedded files; text not read yet |
| PowerPoint 97-2003 | `.ppt`, `.pps`, `.pot` | `ppt` | properties; slides, macros and embedded files not read yet |

What is read today:

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
  linked object is `input.external_reference`; nothing is fetched.
- **Damaged containers**: a wrong byte-order mark, a directory whose chain of
  sectors breaks and lost short streams are read past, with `input.repaired`.

The format is decided from the file, never from its name: all three are
compound files, told apart by the streams they hold. Excel 2 to 4, Word 2 and
older and PowerPoint 95 and older are not to be read; neither is a file of
another format under one of these names (RTF, HTML, a zip package). Each is
refused with a message that says which it is.

## Layout

| Path | Content |
|---|---|
| `extract.go`, `file.go` | The public entry points: `Extract`, `ExtractFile`, `Extractor`; `Open` and `File`, for a part at a time |
| `cmd/gofastmsbin` | The command: JSON, Markdown or text for files and directories |
| `internal/xls` | The Excel reader: records, shared strings, cell formats, sheets, formulas, lists, shapes, charts |
| `internal/ptg` | Compiled formulas back to text |
| `internal/officeart`, `internal/media` | The drawing layer the three formats share: shapes, their properties, pictures |
| `internal/chart` | A chart's data as a caption and a table |
| `internal/grid` | The cells of a sheet into table blocks, as go-fast-ooxml does it |
| `internal/oleps` | Property sets: the document's properties |
| `internal/vba`, `internal/stored` | The source of VBA modules, and the stored part they go to |
| `internal/embed` | Embedded objects, handed on as files |
| `internal/unit`, `internal/warn` | What the readers of the three formats share: the stream they read from, a part's result, the wording of bounds and repeated warnings |
| `internal/cfb` | Compound files, the container of all three formats: sectors, allocation tables, directory, streams; writing one, and writing a storage of a file out as a file of its own |
| `internal/detect` | What a file is: format, generation, encryption |
| `internal/codepage`, `internal/numfmt`, `internal/symbol`, `internal/label`, `internal/clean` | Copied from the other go-fast libraries, for the readers to come: code pages, number formats, symbol fonts, list labels, the text conventions of the shared model |
| `internal/msbintest` | Test harness: a second writer of compound files, writers of property sets and VBA projects, the corpus manifest |
| `testdata/oracle_shared.py`, `oracle_xls.py`, `oracle_lo.py` | What olefile and olevba read of each corpus file, what xlrd reads of each workbook and what LibreOffice shows of it, for the tests that compare the library with them |
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
go test -run 'TestCellsAgainstXlrd|TestShownAgainstLibreOffice|TestTwins|TestFormulasAgainstTwins' -v .
go test -run '^$' -bench Large .                                               # needs bench/baseline.py generate
go run ./cmd/gofastmsbin file.doc
```

Tests that need the corpus or the Python comparison tools skip themselves when
those are absent.

## Licence

MIT. The corpus files keep their own licences, recorded per file in
`testdata/corpus.json`.
