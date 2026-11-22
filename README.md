# go-fast-msbin

Extracts the binary Office formats (Word `.doc`, Excel `.xls` and PowerPoint
`.ppt`, as Office 97 to 2003 wrote them) into the shared go-fast document
model, [`gofastcore.Document`](https://github.com/AOShei/go-fast-core).
Standard library and go-fast modules only.

**Status: in development, nothing released.** A file of any of the three
formats gives a document with its properties, the source of its macros and
its embedded files. The text is not read yet: the one part of the document
says so, with `part.failed`. Encrypted files are refused for now.
[`IMPLEMENTATION_PLAN.md`](IMPLEMENTATION_PLAN.md) has the plan, phase by
phase, and what each phase found.

| Format | Extensions | `Source.Format` | State |
|---|---|---|---|
| Word 97-2003, Word 6/95 | `.doc`, `.dot` | `doc` | properties, macros, embedded files; text not read yet |
| Excel 97-2003 (BIFF8), Excel 5/95 (BIFF5) | `.xls`, `.xlt`, `.xla` | `xls` | properties, macros, embedded files; cells not read yet |
| PowerPoint 97-2003 | `.ppt`, `.pps`, `.pot` | `ppt` | properties; slides, macros and embedded files not read yet |

What is read today:

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
| `internal/oleps` | Property sets: the document's properties |
| `internal/vba`, `internal/stored` | The source of VBA modules, and the stored part they go to |
| `internal/embed` | Embedded objects, handed on as files |
| `internal/unit`, `internal/warn` | What the readers of the three formats share: the stream they read from, a part's result, the wording of bounds and repeated warnings |
| `internal/cfb` | Compound files, the container of all three formats: sectors, allocation tables, directory, streams; writing one, and writing a storage of a file out as a file of its own |
| `internal/detect` | What a file is: format, generation, encryption |
| `internal/codepage`, `internal/numfmt`, `internal/symbol`, `internal/label`, `internal/clean` | Copied from the other go-fast libraries, for the readers to come: code pages, number formats, symbol fonts, list labels, the text conventions of the shared model |
| `internal/msbintest` | Test harness: a second writer of compound files, writers of property sets and VBA projects, the corpus manifest |
| `testdata/oracle_shared.py` | What olefile and olevba read of each corpus file, for `TestSharedAgainstOracles` |
| `testdata/corpus.json`, `testdata/fetch.py` | Manifest of 2,528 real files from the test suites of Apache POI, Apache Tika and LibreOffice, from data.gov.uk and from Govdocs1, and the script that downloads them. The files are not in the repository. One file in five is held out: scored, never looked at |
| `bench` | The baselines of the tools in use today (`baseline.md`) and the script that measures them |

## Running the tests

```sh
go test ./...
python3 testdata/fetch.py && go test -run TestCorpus -v .   # the corpus, and what it is made of
python -I testdata/oracle_shared.py testdata testdata/corpus/oracle-shared.jsonl   # needs olefile and oletools
go test -run TestSharedAgainstOracles -v .
go run ./cmd/gofastmsbin file.doc
```

Tests that need the corpus or the Python comparison tools skip themselves when
those are absent.

## Licence

MIT. The corpus files keep their own licences, recorded per file in
`testdata/corpus.json`.
