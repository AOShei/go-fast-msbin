# go-fast-msbin

Extracts the binary Office formats (Word `.doc`, Excel `.xls` and PowerPoint
`.ppt`, as Office 97 to 2003 wrote them) into the shared go-fast document
model, [`gofastcore.Document`](https://github.com/AOShei/go-fast-core).
Standard library and go-fast modules only.

**Status: in development, nothing released.** The library tells the three
formats apart, which generation of the program wrote a file and whether it is
encrypted, and reads none of them yet: every file is refused with an error
that wraps `gofastcore.ErrUnsupported` and says what the file is.
[`IMPLEMENTATION_PLAN.md`](IMPLEMENTATION_PLAN.md) has the plan, phase by
phase, and what each phase found.

| Format | Extensions | `Source.Format` | State |
|---|---|---|---|
| Word 97-2003, Word 6/95 | `.doc`, `.dot` | `doc` | identified, not read yet |
| Excel 97-2003 (BIFF8), Excel 5/95 (BIFF5) | `.xls`, `.xlt`, `.xla` | `xls` | identified, not read yet |
| PowerPoint 97-2003 | `.ppt`, `.pps`, `.pot` | `ppt` | identified, not read yet |

The format is decided from the file, never from its name: all three are
compound files, told apart by the streams they hold. Excel 2 to 4, Word 2 and
older and PowerPoint 95 and older are not to be read; neither is a file of
another format under one of these names (RTF, HTML, a zip package). Each is
refused with a message that says which it is.

## Layout

| Path | Content |
|---|---|
| `extract.go` | The public entry points: `Extract`, `ExtractFile`, `Extractor` |
| `internal/cfb` | Compound files, the container of all three formats: sectors, allocation tables, directory, streams; writing one, and writing a storage of a file out as a file of its own |
| `internal/detect` | What a file is: format, generation, encryption |
| `internal/codepage`, `internal/numfmt`, `internal/symbol`, `internal/label`, `internal/clean` | Copied from the other go-fast libraries, for the readers to come: code pages, number formats, symbol fonts, list labels, the text conventions of the shared model |
| `internal/msbintest` | Test harness: a second writer of compound files, the corpus manifest |
| `testdata/corpus.json`, `testdata/fetch.py` | Manifest of 2,528 real files from the test suites of Apache POI, Apache Tika and LibreOffice, from data.gov.uk and from Govdocs1, and the script that downloads them. The files are not in the repository. One file in five is held out: scored, never looked at |
| `bench` | The baselines of the tools in use today (`baseline.md`) and the script that measures them |

## Running the tests

```sh
go test ./...
python3 testdata/fetch.py && go test -run TestCorpus -v .   # the corpus, and what it is made of
```

Tests that need the corpus or the Python comparison tools skip themselves when
those are absent.

## Licence

MIT. The corpus files keep their own licences, recorded per file in
`testdata/corpus.json`.
