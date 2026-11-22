# Legacy Office library: proposed plan

A proposal for the go-fast library that reads Word, Excel and PowerPoint 97-2003 files (`.doc`, `.xls`, `.ppt`). Written 2026-11-22 from `LEGACY_OFFICE_HANDOFF.md`, core's README at v0.1.1, `COMMAND_FLAGS.md`, `CAP_WARNINGS_CONSISTENCY.md`, and the code and plans of go-fast-ooxml and go-fast-email. Once the name is decided and the repository exists, this becomes its `IMPLEMENTATION_PLAN.md`.

**No code is written.** No phase starts without a go-ahead.

## 1. Decisions

**Decided by Andrew, 2026-11-22:** Q1 (one module), Q2 (`go-fast-msbin`), Q3 (Excel, Word, PowerPoint), Q5 (one release), Q6 (permission to download a test corpus), Q7 (tools), Q8 (the command imports itself and go-fast-ooxml). Q4 agreed in principle: the aim is to cover most of the documents still in use, and the line is drawn where that stops; Phase 0 counts the versions found in the corpus to check that the line below is the right one. Q9: left out of the first release; to be reviewed after it. Go-ahead for Phase 0 given the same day.

| # | Question | Recommendation |
|---|---|---|
| Q1 | One module or three | **One.** The three formats share the compound file, property sets, encryption, VBA, code pages, the drawing layer (OfficeArt) and embedded objects, and each can embed the others. Same reasoning as ooxml's D1 |
| Q2 | Name | **`go-fast-msbin`**, package `gofastmsbin`, command `gofastmsbin` (Microsoft calls these the "binary file formats"). Warning codes of its own are under `msbin.` |
| Q3 | Order of the formats | **Excel, Word, PowerPoint.** `.xls` is the format production pipelines meet most, it reuses the most existing code (number formats, table finding, formula text), and it has the best oracles, so it proves the harness soonest. Word is the hardest of the three and benefits from the drawing layer being exercised first. ooxml's order was Word, Excel, PowerPoint |
| Q4 | Older versions | **Read:** Word 97-2003, Excel 97-2003 (BIFF8), PowerPoint 97-2003, first. Then Excel 5/95 (BIFF5/7: same container, close records, text in a code page) and Word 6/95 (text, paragraphs and tables; less structure), each in a late phase. **Refuse with `ErrUnsupported` and a message naming the version:** Excel 2 to 4 (BIFF2-4, not compound files), Word 2 and older, PowerPoint 95 and older |
| Q5 | Release | **One release, when all three formats are complete**, as ooxml's D3. The alternative is a v0.1.0 with Excel alone; I would not, because the README and the format list would change under users twice |
| Q6 | Test corpus | Permission to download, by URL and hash, never committed: the legacy files of Apache POI's test data (175 `.doc`, 422 `.xls`, 149 `.ppt` at the commit ooxml pins), Apache Tika's (57, 33, 23), LibreOffice's import tests, and a sample of `.doc`/`.xls`/`.ppt` from data.gov.uk. Split into a development set and a held-out set when the manifest is made; held-out files are scored and never opened |
| Q7 | Comparison tools | Add to `bench-env/.venv-ooxml`: `xlrd` (the Python `.xls` reader), `xlwt` (writes `.xls`: reproducible fixtures), `python-calamine` (the Rust reader: the speed to match). `olefile`, `oletools` and `msoffcrypto-tool` are already there. `antiword` and `catdoc` are not installed and need a system package: optional, Andrew's call. Apache Tika (Java) would be the strongest "anything else" comparison for `.doc` and `.ppt`: also Andrew's call |
| Q8 | What the command imports | **Itself and go-fast-ooxml**, so that a `.docx` embedded in a `.doc`, and a `.doc` embedded in a `.doc`, are both read. Other formats embedded in a legacy file are listed with `child.unsupported` |
| Q9 | Leftovers the file no longer refers to | A fast-saved Word file and an incrementally saved presentation keep text of earlier versions in bytes that nothing in the current document points to. **Not read in the first release**, and the README says so. Reading superseded PowerPoint edits is well defined and could be a later `stored` item; recovering unreferenced Word text is carving, which is a guess |
| Q10 | Core | No change to the model is foreseen. Core's README needs the three format names (`doc`, `xls`, `ppt`) in its mapping table and its list of extractors: a docs change, proposed when the library is near release |

## 2. Where the project stands

- Seven modules released on core v0.1.1 (schema 0.2); all working trees clean on `main` except go-fast-pdf, which is on its agent's `rust-bench` branch. pdf and ooxml are read, never edited.
- go-fast-ooxml refuses these files today with `ErrUnsupported` ("a legacy binary Office format"); go-fast-email's command and the ooxml and odf commands list an embedded one as `child.unsupported`.
- The workspace has no `.doc`, `.xls` or `.ppt` file. The corpus has to be fetched (Q6).
- Tools on this machine: LibreOffice 26.8 and pandoc are installed; `antiword`, `catdoc`, `xlrd` are not. Go is 1.27; CI tests 1.25 and stable.

### What can be copied, and what cannot

Libraries never import each other; code is copied into `internal/` with a note in NOTICE.

| Need | Source | State |
|---|---|---|
| Compound file reader | `go-fast-email/internal/cfb` | **Take email's copy**, not ooxml's: it adds a per-storage index, `List` for the entries of a storage, 262,144 directory entries, and reading on when the short streams are damaged (`input.repaired`). To add: writing a storage out as a file of its own (section 4.5) |
| Number formats | `go-fast-ooxml/internal/numfmt` | Copy unchanged. BIFF uses the same format strings and built-in ids. It already agrees with LibreOffice on 6,048 pairs |
| Formula text from compiled tokens | `go-fast-ooxml/internal/xlsb` (`formula.go`, `functions.go`) | The function and command tables are the same numbers: copy. The token layouts differ (16-bit rows, column flags, 3D references through EXTERNSHEET; BIFF5 differs again): the parser is adapted, not copied |
| Moving shared formulas | `go-fast-ooxml/internal/formula` | Not needed: BIFF stores a shared formula with relative tokens, resolved per cell |
| VBA source | `go-fast-ooxml/internal/vba` | Copy. It takes the bytes of a `vbaProject.bin`; here the project is a storage of the file itself (`Macros`, `_VBA_PROJECT_CUR`), so its entry point changes to a storage |
| Code pages | `go-fast-rtf/internal/codepage` | Copy: single-byte Windows pages, Mac Roman, 932/936/949/950. Needed for BIFF5, Word 6/95, property sets and VBA |
| Symbol fonts, list labels, text cleaning, display placement, grouped warnings | `go-fast-ooxml/internal/symbol`, `label`, `clean`, `hidden`, `warn` | Copy |
| Finding tables on a sheet | `go-fast-ooxml/internal/sml` | Copy the stage that turns a sheet's cells into table blocks (regions, `region_split`, row bands), so that a workbook saved both ways gives the same tables |
| Encryption | `go-fast-ooxml/internal/crypt` | **Nothing reusable.** It reads only the AES schemes of OOXML and refuses others. XOR obfuscation, RC4 and RC4 CryptoAPI are written new from [MS-OFFCRYPTO] 2.3.5 to 2.3.7; `crypto/rc4`, `md5` and `sha1` are in the standard library |
| Property sets (`\x05SummaryInformation`) | none | New, from [MS-OLEPS] |
| OfficeArt (shapes, pictures, text boxes) | none | New, from [MS-ODRAW]; shared by all three formats |
| Containing LibreOffice | `go-fast-ooxml/internal/ooxmltest/proc_unix.go` | Copy the pattern: own process group, group kill, file-size limit, cleanup |

## 3. Scope

| Family | Extensions | `Source.Format` | Identified by |
|---|---|---|---|
| Word | `.doc`, `.dot` | `doc` | a `WordDocument` stream |
| Excel | `.xls`, `.xlt`, `.xla` | `xls` | a `Workbook` stream (`Book` for Excel 5/95) |
| PowerPoint | `.ppt`, `.pps`, `.pot` | `ppt` | a `PowerPoint Document` stream |

The format is decided from the file, never from its name. A compound file that is none of the three (an encrypted OOXML package, an Outlook message, an installer) is `ErrUnsupported` with a message saying what it is. So is a file named `.doc` or `.xls` that is really RTF, HTML or XML: those belong to other libraries, and a dispatcher routes them.

Out of scope: rendering and pagination (a Word document is one `body` part); evaluating formulas; running or fetching anything; OCR.

"Production ready" means what ooxml's plan says it means: no crash or hang over corpus and fuzzing, every allocation the file drives bounded, output passing `Validate`, sentinel errors for what cannot be read, declarations reported and nothing judged, race-clean, identical output for any worker count, `go.mod` requiring only go-fast-core.

## 4. Mapping onto the shared model

The rule throughout: **the same document saved as `.docx` and `.doc` (or `.xlsx`/`.xls`, `.pptx`/`.ppt`) should give the same blocks**, as far as the older format holds the same facts. Where this plan is silent, the answer is "as go-fast-ooxml does".

### 4.1 All three

- **Metadata** from the two property-set streams: title, subject, authors, keywords, created, modified, application; last author, revision, company, manager, category and user-defined properties to `Extra`. Strings are decoded from the code page the set names.
- **Macros.** A VBA project is `input.active_content`, and each module a `code` block (`language` `vba`) in the stored part, `stored` `macro`.
- **Embedded objects** go to `opts.ExtractChild` (section 4.5). Links to other files are `input.external_reference`.
- **Pictures** are `image` blocks with alternative text; `ImagesData` gives the bytes. JPEG and PNG are passed through; metafiles and bitmaps the file compresses are delivered decompressed, within the decode budget.
- **Symbol fonts** are translated as in ooxml; a code with no character is U+FFFD and counted in `text.unmapped_glyphs`.
- **Fixed bounds** follow core's "Bounded input" and the shared wording: `limit.exceeded` at `error`, once per bound per part, with a count; repeating warnings grouped with the first eight.

### 4.2 Word

One `body` part. The text is read through the piece table, in 8-bit or UTF-16 pieces; paragraph and character properties come from the property pages, the style sheet, and the modifiers a fast save attaches to pieces.

| In the file | In the model |
|---|---|
| Style Heading 1 to 9, or an outline level | `heading`, clamped to 6 with the real level in `Attrs` |
| A paragraph in a list (list tables and overrides) | `list_item` with `Level`, `Ordered` and the computed label in `Marker` |
| Table rows and cells, nested tables | `table` with merges; a nested table flattened into its cell, as ooxml |
| Footnotes and endnotes | `footnote` with `Marker`, after the paragraph that refers to it |
| Comments, with author and date | `note` `comment` |
| Text marked deleted by a tracked change | `note` `deletion`, with author and date; `text.tracked_changes` |
| Fields | the result as text; the code a `note` `field`, `stored` `field_code`; a `HYPERLINK` field also a link span |
| Headers and footers | `header`, `footer`, once per distinct content |
| Text boxes | blocks at the anchor paragraph |
| Form fields | the value as a block with the field's name; a drop-down's entries a `note` `choices` |
| Document variables | stored part, `document_variable` |
| Bookmarks | `Loc.Anchor`: the bookmark that starts the paragraph, as rtf |
| Bold, italic, underline, strike-through, super- and subscript, links | spans, with `Options.Spans` |

**Declared:** `hidden` for hidden text (the vanish property on the run or through its styles, with the toggle against the style that the format defines), for content anchored in hidden text, for a shape OfficeArt marks hidden, and for a first-page or even-page header or footer the section or document settings switch off. `no_ink`: the binary format appears to have no such declaration (the text fill of Word 2010 exists only in OOXML); to be confirmed in the phase.

### 4.3 Excel

One `sheet` part per worksheet, in workbook order, `Name` the sheet's name; sheets read in parallel.

- **Cells as displayed**, through the copied number-format engine; dates in ISO 8601 with the 1900 and 1904 systems; a formula gives its stored result. A sheet holds at most 65,536 rows by 256 columns, which bounds it by construction.
- **Tables** as ooxml: used regions as `inferred` tables with `Loc.Range`, merges, row bands for large ones.
- **Comments** as `note` `comment`; **drop-down lists** as `note` `choices`; **hyperlinks** as cell links; text in shapes as paragraphs; pictures as images at their anchor cell.
- **With `Options.Cells`:** the formula text read back from its tokens, and the type and stored value. A formula that cannot be read back is left out and counted, never guessed.
- **Stored part:** defined names (`defined_name`), Excel 4 macro sheets (`macro`, `language` `excel4`), VBA.
- **Charts** (the series and their cached values, as a `caption` and a `table` with `role` `chart`) and **pivot tables** (the cells they fill, with `role` `pivot`; caches whose source is gone as `pivot_cache`) come in the second Excel phase.

**Declared:** `hidden` for a hidden or very hidden sheet (part and blocks), hidden rows and columns, filtered rows, a value its format displays as nothing (`;;;`, other than zero), a shape marked hidden, a drop-down whose arrow is hidden. `no_ink`: shape text with no fill, if OfficeArt declares it in one property; to be confirmed.

### 4.4 PowerPoint

One `slide` part per slide in presentation order, with `Width` and `Height` in points and `loc.bbox` on every block; slides read in parallel. The current state of the file is found through the chain of edits and the persist directory (the last edit wins).

| On the slide | In the model |
|---|---|
| Title, centred title | `heading` 1 and the part's `Name` |
| Subtitle | `heading` 2 |
| Body text and text boxes | `list_item` with level and bullet or number in `Marker`, or `paragraph`; bullets inherited through the master's text styles |
| Table (a group of cell shapes the file marks as a table) | `table` |
| Picture | `image` |
| Date, footer and slide-number placeholders; the master's own text | `footer`, `header` |
| Speaker's notes | `note` `speaker` |
| Comments | `note` `comment`, with author and date |

Reading order is ooxml's rule (title, then top down, left to right), so the same deck reads the same. Charts are Microsoft Graph objects, which hold a small workbook: read with the Excel reader as a chart's table, in the PowerPoint phase if Excel's charts are done by then. Sound and video are counted, not read. An action that runs a program or macro is `input.active_content`.

**Declared:** `hidden` for a hidden slide (part, blocks and notes) and a shape or group marked hidden. `no_ink`: probably none; to be confirmed.

### 4.5 Embedded objects

Three shapes of embedding, all ending in `opts.ExtractChild`:

1. **A whole file in a stream:** a presentation's embedded objects (optionally zlib-compressed), an `Ole10Native` package, an OOXML `Package` stream. The bytes are handed on, counted against the decode budget.
2. **A storage inside the file:** Word's `ObjectPool` and Excel's `MBD…` storages hold an embedded document as a subtree, not as a file. It is written out as a compound file of its own, in memory within the decode budget, so that any dispatcher can read it. This needs a small compound-file writer, which the test harness needs anyway.
3. **A link:** listed with `input.external_reference`, nothing fetched.

Depth, text and decode budgets are shared with children through the context, as core describes.

### 4.6 Encryption

- **XOR obfuscation** (Word, Excel), **RC4** (40-bit) and **RC4 CryptoAPI** (all three), opened with `Options.Password`. The password is verified first: `ErrPasswordRequired` without one, `ErrWrongPassword` with a wrong one. The document carries an `msbin.decrypted` warning naming the scheme.
- **Excel's own password** (`VelvetSweatshop`) is tried without being asked for, as ooxml does: a protected workbook with no password to open is common among real `.xls` files.
- Streams are decrypted block by block as they are read (RC4 is re-keyed every 512 bytes in Word and 1,024 in Excel, per object in PowerPoint), so nothing is held decrypted and sheets and slides can still be read in parallel.
- These schemes have no rounds of hashing to bound, so there is no `MaxSpinCount`.
- Oracles: msoffcrypto-tool decrypts all three; LibreOffice writes encrypted fixtures.

The parsers read from a stream interface from the first phase, so that decryption slots in underneath without touching them.

## 5. Architecture

```
gofastmsbin (module root)   Extract, ExtractFile, Extractor; Open and File for a part at a time
internal/cfb              compound files (from go-fast-email), plus writing a storage as a file
internal/oleps            property sets
internal/crypt            XOR, RC4, RC4 CryptoAPI
internal/officeart        shapes, properties, anchors, pictures, text boxes: all three formats
internal/biff             records, CONTINUE joining, strings
internal/xls              the Excel reader
internal/ptg              compiled formulas back to text (BIFF8, BIFF5)
internal/numfmt           number formats (from go-fast-ooxml)
internal/doc              the Word reader: FIB, piece table, properties, styles, lists, stories
internal/ppt              the PowerPoint reader: edits, persist directory, slides, text, masters
internal/vba, codepage, symbol, label, clean, hidden, warn   copied
internal/oletest          file builders, contained oracles, scorers, manifest loader
cmd/gofastmsbin             the command
```

Settings only this library has are fields of `Extractor` (`MaxCells`, as ooxml). The command has core's five render flags through `AddRenderFlags`, the usual `-format`, `-parts`, `-images`, `-spans`, `-password`, `-workers` and limit flags, and writes JSON a part at a time.

## 6. Testing

| Layer | What |
|---|---|
| Built files | `internal/oletest` assembles a compound file from records in memory: one minimal file per behaviour, and the seeds of the fuzz targets |
| Generated fixtures | Known content written as `.xls` by `xlwt` (reproducible), and converted to `.doc`, `.xls` and `.ppt` by LibreOffice (contained), each with its expected document in the shared model's JSON |
| Corpus | The files of Q6, by URL and hash, skipped when absent. Files written by Microsoft Office matter most: LibreOffice-made fixtures cannot stand in for them |
| Differential | LibreOffice text and CSV-as-shown exports; `xlrd` and calamine for cells; `olevba` for macros; `olefile` for the container and property sets; msoffcrypto-tool for decryption |
| Parity with go-fast-ooxml | LibreOffice converts a legacy file to its OOXML twin; the `gofastooxml` command's JSON for the twin is compared block by block where the binary is present. No import: the dependency rule holds for tests too |
| Fuzz | One target per parser (container, property sets, records, tokens, piece table, property pages, OfficeArt, PowerPoint records, encryption headers, VBA) and one per format on the whole document, run with `-fuzzminimizetime 0s` and judged by the count of executions |
| Invariants on every input | No panic; `Validate` passes; JSON round-trips; same output for any `Workers`, also where a limit cuts; excluding declared content leaves the other text unchanged; the extraction packages import neither `net` nor `os` |

Test runs go under `systemd-run --user --scope -p MemoryMax=8G`, with a ceiling in the soak test itself. CI is ooxml's workflow: gofmt, vet, `-race` on three systems with Go 1.25 and stable, 32-bit, fuzz smoke, the dependency rule, `core.autocrlf false` before checkout.

**Targets** are set in Phase 0 from measured baselines, as ooxml's were. The intent: text and cell agreement with the oracles at ooxml's levels (word-level F1 0.99; 0.995 of cells); faster than every Python tool by a wide margin; `.xls` at least level with calamine. Compared by instructions per operation, since the machine is shared.

## 7. Phases

Sizes are relative: S (days), M (about a week), L (several weeks). Each phase is one branch and one PR against `main`. On 2026-11-22 Andrew gave the go-ahead for all phases ("only stop when my input or critical decisions are required") and accepted the speed targets of Phase 0, to be tested and adapted on the way.

| Phase | Delivers | Size |
|---|---|---|
| 0. Skeleton and baselines | Module, licence, NOTICE, CI, the copied packages; format detection with every file still `ErrUnsupported`; `oletest` with the compound-file writer; the corpus manifest and its held-out split; the tools of Q7 and their baseline speed, memory and accuracy; targets proposed | S |
| 1. Shared layer | Property sets to `Metadata`; VBA to the stored part; embedded objects found and handed on in all three shapes; the stream interface; a first command. A file of any of the three formats now gives a document with metadata, macros and children, and a part that says its content is not read yet | M |
| 2. Excel: cells | Records and strings, shared strings, styles and number formats, every cell record, sheets as parts in parallel, tables, merges, hidden sheets, rows and columns, dates, links, comments, defined names, limits | L |
| 3. Excel: the rest | Formula text and cell details, drop-down lists, shapes and pictures (OfficeArt begins here), charts, pivot tables, macro sheets, links to other workbooks; then Excel 5/95 | L |
| 4. Word: text and structure | FIB, piece table, stories, property pages, style sheet, headings, lists with labels, tables, footnotes and endnotes, comments, headers and footers, fields, bookmarks | L |
| 5. Word: the rest | Hidden text and spans, tracked changes, text boxes, pictures, form fields, document variables; then Word 6/95 | M |
| 6. PowerPoint | Edits and persist directory, slides in parallel, text and its types, placeholders and master inheritance, bullets, tables, notes, comments, pictures, places on the slide, reading order, hidden slides and shapes, Graph charts | L |
| 7. Encryption | The three schemes for the three formats, the default Excel password, encrypted children | M |
| 8. API and command | `Open`, `File`, `Parts`; the finished command and its child dispatcher; API snapshot | S |
| 9. Release hardening | Soak over the whole corpus, the fuzz campaign, allocation budgets and speed targets, the comparison table, README, SECURITY, CHANGELOG. Then Andrew tags | M |

### Phase 0: result (2026-11-22)

Done, on branch `phase-0-skeleton`. Every file is still refused with `ErrUnsupported`, with a message that says what it is.

- **Module and gates.** `gofmt`, `go vet`, `go test -race`, the 32-bit tests, vet for seven other systems and the dependency rule pass locally. CI is written (`.github/workflows/ci.yml`) and not yet run: the GitHub repository does not exist.
- **Copied:** `cfb` (go-fast-email's), `codepage` (go-fast-rtf's), `numfmt`, `symbol`, `label`, `clean` (go-fast-ooxml's), each with its tests. `vba`, `hidden` and `warn` come with the phases that use them, since they need changes.
- **Compound files are written as well as read.** `cfb.Write` makes a file from a tree of storages and streams; `cfb.Export` writes a storage of a file out as a file of its own, within a byte limit, with the storage's class identifier. olefile reads what `Write` writes. The reader is tested against two writers (this one and the copied test builder), and the fuzz target now exports every storage it finds.
- **Detection** (`internal/detect`): format from the streams; generation from the Word FIB, the workbook's BOF record and the presentation's Current User stream; encryption scheme from the FIB flags and table stream, FILEPASS, and the Current User token.
- **Corpus:** 2,528 files, 1.0 GB, in `testdata/corpus.json`; 515 held out. Sources: Apache POI (737), LibreOffice's import tests (761), Apache Tika (91), data.gov.uk (164) and Govdocs1 (775). Govdocs1 was added because data.gov.uk gave only 10 `.doc` and 4 `.ppt`: it is a research corpus of files from United States government web sites, read from its zip archives by byte range.
- **Tools:** `xlrd`, `xlwt` and `python-calamine` added to `bench-env/.venv-ooxml`; no existing pin changed.

**What the corpus is made of** (`go test -run TestCorpus -v .`). Among the 939 files public bodies published:

| Format | Office 97-2003 | The generation before | Older | Encrypted | Other |
|---|---|---|---|---|---|
| `.doc` | 330 | 14 (Word 6/95) | 0 | 0 | 1 is a presentation |
| `.xls` | 312 | 10 (Excel 5/95) | 0 | 0 | |
| `.ppt` | 269 | 0 | 0 | 2 | 1 damaged |

This supports the line of Q4: Office 97-2003 is 96 to 100 % of each format, the generation before it 3 to 4 % of Word and Excel files, and nothing older appears. The test suites add the rest: 8 Word 2 files, 9 Excel 2 to 4, 1 PowerPoint 95, 18 encrypted files across the three schemes, and about 70 that are damaged or not what their names say.

**Baselines** (`bench/baseline.md`):

| | Today | Proposed target |
|---|---|---|
| `large.xls`, 1,966,080 cells | calamine (Rust) 0.22 s, 215 MB; xlrd 1.09 s; LibreOffice 3.0 s | at most 0.22 s with all workers, at most 0.5 s with one; at most 215 MB. calamine and xlrd give raw values; this library also formats every number |
| `large.doc`, 1.5 million words | LibreOffice 8.1 s, 910 MB | at most 0.2 s |
| `large.ppt`, 1,500 slides | LibreOffice 7.2 s, 1,050 MB | at most 0.1 s |
| The 1,111 `.xls` of the corpus | calamine reads 1,064 in 2.9 s (3 of its failures are panics); xlrd reads 1,058 in 11.3 s | every file a document or a shared error; at least as many read as calamine |
| One ordinary file | LibreOffice: about 2.1 s a file, whatever the format | |

No tool for `.doc` or `.ppt` other than LibreOffice is installed (`antiword` and `catdoc` need a system package; Tika needs Java), so those two targets are set against ooxml's times for the same content (0.08 s and 0.02 s), not against a rival.

Found on the way:

- **LibreOffice keeps its exploit files scrambled** (RC4 under the key "CVE"), so that virus scanners leave its repository alone: 64 of its files looked like noise. `fetch.py` unscrambles them; they are the most hostile inputs of the corpus.
- **The container reader is stricter than olefile.** Of 26 development files it refuses as damaged, olefile opens 12: a directory chain that runs off the file, a wrong byte-order mark, a directory with no root entry. Phase 1 decides how much of that to read with `input.repaired`.
- **The fuzzers ran** 18 million executions on the container and 16 million on `Extract` without a finding.
- The accuracy targets wait for the first reader: there is nothing to score yet.

### Phase 1: result (2026-11-22)

Done, on branch `phase-1-shared`. A file of any of the three formats gives a document: its properties, the source of its macros, its embedded files, and one part that says the text is not read yet (`part.failed`). Encrypted files are refused until Phase 7.

- **Properties** (`internal/oleps`, `meta.go`): both property-set streams, every value type, text in the code page the set names (and UTF-16), the dictionary of custom properties. Keys in `Extra` are go-fast-ooxml's (`description`, `last_modified_by`, `revision`, `template`, `category`, `manager`, `company`, `app_version`, `content_status`, `custom.<name>`). Bounds: 64 KiB a value, 256 custom properties, 4,096 properties a set.
- **Macros** (`internal/vba`, `internal/stored`): a project is found in `Macros` (Word), `_VBA_PROJECT_CUR` (Excel) or any storage at the root; each module is a `code` block in the stored part.
- **Embedded files** (`internal/embed`): Word's `ObjectPool` storages and a workbook's `MBD…` storages. A file in a stream (`\x01Ole10Native`, `Package`, `CONTENTS`) is handed on as it is, a packaged file under its own name; any other storage is written out as a compound file. A linked object is `input.external_reference`. What is handed on counts against the decode budget, which the document and its children share.
- **The stream interface** (`unit.Stream`) is what the readers take their input through; the cipher of Phase 7 goes under it.
- **`Open` and `File`** read a part at a time, and the **command** writes JSON a part at a time, byte for byte what the whole document gives (checked over the corpus). Both arrived here rather than in Phase 8, because the reading of parts in order and in parallel was copied whole from go-fast-ooxml; Phase 8 finishes and documents them.
- **Damaged containers.** A wrong byte-order mark and a directory whose chain of sectors breaks or loops are read past, with `input.repaired`: 12 of the 26 development files that were refused are now read, one of them a published presentation. The 14 still refused have no root entry, no sound directory sector, or a header that contradicts itself.

**Measured** over the corpus:

| | |
|---|---|
| Properties against olefile | 99.7 % of the values of 14 fields agree (14,712 of 14,750); the differences are in fuzzed files, whose streams break off, and in dates in the year 1601, which olefile leaves out |
| Macro modules against olevba | 839 of 843 given; the four are modules of damaged projects and p-code that olevba disassembles |
| Corpus | 2,528 files: every one a valid document or a shared error; 1,030 with a title, 145 with macros (840 modules), 90 with embedded files (575 files, 131 of them documents of these formats), 18 repaired |

Found on the way:

- **Word for the Macintosh** writes the identifier of a property set with its fields in the other byte order, and its text in Mac Roman. Both are read.
- **A presentation's macros and embedded files** are not storages of the file: they are in records of the `PowerPoint Document` stream, compressed. They come with Phase 6.

### Phase 2: result (2026-11-22)

Done, on branch `phase-2-excel-cells`. A workbook of Excel 97 to 2003 is read: one `sheet` part per sheet, its cells as tables.

- **The reader** (`internal/xls`): the globals (date system, fonts, number formats, cell formats, the list of sheets, the shared strings with their CONTINUE records), and for each sheet every cell record (shared and inline strings, numbers in their three encodings, truth values, errors, formulas with results of each kind), rows and columns the sheet hides, merged cells, comments, hyperlinks. The workbook stream is read into memory once; the sheets are read from it in parallel.
- **Tables** (`internal/grid`) are found by go-fast-ooxml's code, made a package: the same regions, the same splitting of sparse areas and of tables over a million cells, the same order.
- **As shown**: numbers through the copied number-format engine, dates in ISO 8601 in both date systems, text through the text section of its format. Text in a symbol font is translated; a string that changes font partway is translated run by run.
- **With `Options.Cells`**: the type of each value, and the stored value where the text does not show it in full. Formula text comes in Phase 3. **With `Options.Spans`**: hyperlinks, to an address, a file or a place in the workbook.
- **Declared**: `hidden` for a hidden or very hidden sheet, hidden rows and columns, and a value its format shows as nothing.
- **Limits**: `Extractor.MaxCells` (20 million by default) and `Limits.MaxTextBytes` cut a workbook at the same place whatever the number of workers; a sheet holds at most 65,536 rows.
- Chart sheets and dialog sheets are counted as `msbin.unread_content`; a macro sheet's cells are read and it is `input.active_content`; the modules of the VBA project that the workbook lists as sheets are not parts.

**Measured:**

| | |
|---|---|
| Cells against xlrd, 1,047 workbooks | 12,148,649 agree; 10 differ; 205 are zeros their format shows as nothing; 520 are text in a symbol font, which xlrd gives as codes |
| Cells as shown against LibreOffice, 1,071 workbooks | 12,828,242 of 12,867,728 agree (99.69 %); dates are not compared. LibreOffice recalculates formulas on loading, which is most of the rest |
| `large.xls`, 1,966,080 cells | 0.08 s with all workers, 0.15 s with one (target 0.22 s and 0.5 s; calamine 0.22 s, xlrd 1.09 s, LibreOffice 3.0 s) |
| Memory for `large.xls` | 168 MB allocated in all by `Extract`; the command peaks at 155 MB writing text and 313 MB writing JSON, where the one sheet is encoded in one piece (target 215 MB: met for the library, not for the command's JSON) |
| Workbooks read | 1,055 of the 1,111 in the corpus; 16 more are Excel 5/95 (Phase 3) and 9 are encrypted (Phase 7). calamine reads 1,064 |
| Same output for 1 and 5 workers | every workbook of the corpus, with and without a limit |
| Fuzzing | `FuzzWorkbook` 9 million executions and `FuzzExtract` 15 million more, no finding |

Found on the way:

- **Symbol fonts in part of a cell.** A cell whose font is Symbol for its bullet and Arial for its words is common in published workbooks. Translating the whole cell by the cell's font turned the words into Greek; the fonts of the runs are now kept for workbooks that have a symbol font. go-fast-ooxml translates a whole cell by its font, and may have the same fault with `.xlsx`: for its owner to check.
- **The number-format engine and LibreOffice differ in two ways**, in code this library copies from go-fast-ooxml unchanged: a value that rounds to zero is shown through the negative section here and through the zero section there (`(0.00)` for -4.5e-13); and the last of fifteen digits of a General number is rounded from the binary value here and from its shortest decimal there (6,054 cells of 12.9 million). Both are for the engine's owner; nothing is changed here, so that the two libraries keep agreeing.
- **A formula record under its older number** (0x0206, 0x0406) appears in workbooks of this generation written by other programs, and is read.
- **Defined names** move to Phase 3: a name's value is a formula, and reading formulas back as text is that phase's.

### Phase 3: result (2026-11-22)

Done, on branch `phase-3-excel-rest`. The rest of what a workbook holds, and workbooks of Excel 5 and 95.

- **Formula text** (`internal/ptg`): go-fast-ooxml's parser for the formulas of `.xlsb` files, with the token layouts of this format and of Excel 5/95. With `Options.Cells` a cell gives its formula, shared and array formulas among them. A formula that cannot be read back is left out and counted (`msbin.unread_content`). A function newer than Excel 97, which the file keeps as a call by name (`_xlfn.SUMIFS`), is written as an `.xlsx` file writes it; numbers are written to fifteen digits, as Excel shows them.
- **Stored part**: defined names (`defined_name`, a name local to a sheet with the sheet in front, Excel's own names as `_xlnm.…`), and the formulas of each macro sheet as code (`macro`, `language` `excel4`), before the VBA modules.
- **Links to other workbooks** are `input.external_reference`; formulas through them are written `[1]Sheet!A1`.
- **Drop-down lists** are `note` `choices` at their cells: the values, or the formula they come from; hidden where the arrow is, or every cell.
- **The drawing layer** (`internal/officeart`, shared by the three formats): records, a shape's properties, the store of pictures with bitmaps passed through and compressed metafiles expanded within the decode budget. On a sheet: text boxes and WordArt as paragraphs, pictures as `image` blocks with alternative text, each at the cell it is anchored to; a shape marked hidden is `hidden`.
- **Charts** (`internal/chart`, go-fast-ooxml's table of a chart's data): type, titles of the chart and its axes, the series. A chart of this format often keeps no copy of its data: where a series names the cells it plots, those cells are read, as they show; otherwise what the chart keeps. A chart sheet is a part with the chart's blocks.
- **Pivot tables** are tables with `role` `pivot`, their name and header rows, over the cells they fill.
- **Excel 5/95** (BIFF5): strings in the workbook's code page, its own name, note and object records, its formula tokens. Read by the same reader.

**Measured:**

| | |
|---|---|
| Cells against xlrd, 1,063 workbooks (16 of Excel 5/95) | 12,180,746 agree; 10 differ |
| Cells as shown against LibreOffice | 12,860,182 of 12,899,668 agree (99.69 %) |
| Against the `.xlsx` twin LibreOffice writes, read by go-fast-ooxml (`TestTwins`), 1,072 workbooks | 17,892,191 of 17,929,209 cells the same (99.79 %); 105,639 of 105,690 tables have the same range; 2,035 of 2,190 notes the same; 385 charts here, 252 in the twins, 161 identical |
| Formulas against the twins | 286,115 the same; 7,913 differ, by LibreOffice's rewriting (a deleted sheet's reference without its cell, `TABLE()` for a what-if table, its own names for some functions) |
| Workbooks read | 1,072 of 1,111 (calamine 1,064); the rest are Excel 2 to 4, encrypted (Phase 7), damaged or not workbooks |
| `large.xls` | 0.05 s with all workers, 0.10 s with one, on a machine with nothing else running (Phase 2 measured 0.08 s and 0.15 s beside other work) |
| Fuzzing | `FuzzWorkbook` 14 million executions, `FuzzExtract` 23 million. Two findings, fixed: a cell name for a column past three letters, and control characters in a formula's string |

Found on the way:

- **go-fast-ooxml translates a whole cell by its font**, and the twins show it: a cell with a Symbol bullet before its words comes out in Greek letters there (`TestTwins`, published files among them). Reported in Phase 2; confirmed here.
- **LibreOffice's files are not Excel's.** It writes `TRUE()` for TRUE, 0 for an argument left out, a cell for a range of one cell, and recalculates. The comparisons allow for what could be told apart; the rest lowers the figures above without being a difference in reading.
- **Not read**: the records of a pivot cache whose source is gone (`pivot_cache`), the text of text boxes of Excel 5/95 (kept in the object's own record), several levels of categories of a chart (the innermost is given), a picture kept outside the file. Counted or absent, never guessed.

### Phases 4 and 5: result (2026-11-22)

Done together, on branch `phase-4-word`: Word documents, of Word 97 to 2003 and of Word 6 and 95.

- **The reader** (`internal/doc`): the file information block; the piece table, with pieces of one byte or two to a character; the pages of paragraph and character properties and the lists of changes they hold; the style sheet with what each style inherits; the font table. One `body` part.
- **Structure**, by go-fast-ooxml's rules: headings from the built-in heading styles and from outline levels (clamped to six, with the level as an attribute), titles and subtitles, quotations, code (consecutive paragraphs one block), list items with their level and their label. The labels are counted by go-fast-ooxml's numbering engine, fed from this format's list tables.
- **Tables**: rows and cells from the cell and row marks; the grid from the edges of the cells of every row; cells merged along a row and down a column; header rows; a table inside a cell flattened into it. A row's definition too long for a page of properties is read from the Data stream.
- **Notes**: footnotes and endnotes with their numbers, after the paragraph that refers to them; comments with their authors; text a tracked change removed as `note` `deletion` with author and date, and `text.tracked_changes`.
- **Fields**: the result is text; the code is a `note` `field` (`stored` `field_code`); a `HYPERLINK` field is a link span and no note, as an `.docx` file has it.
- **Headers and footers** once per distinct content; **bookmarks** as `Loc.Anchor`; **spans** for bold, italic, underline, strike-through, super- and subscript with `Options.Spans`.
- **Declared**: `hidden` for hidden text (the vanish property, on the run or through its styles) and for a shape marked hidden. No declaration of this format answers to `no_ink`.
- **Text boxes** at the paragraph their shape is anchored in; **pictures**, in the line of the text (from the Data stream) and in shapes (from the store of the drawing), as `image` blocks. Symbol fonts are translated, by the run's font and for characters inserted as symbols.
- **Document variables** in the stored part (`document_variable`).
- **Word 6 and 95** (`word6.go`): the text, in one code page for the document; headings by the built-in styles; tables by their cell marks; footnotes and endnotes; the results of fields. The changes to a style of that version are in a coding of their own and are not read: no lists, no hidden text, no tracked changes, no spans.

**Measured:**

| | |
|---|---|
| Against the `.docx` twin LibreOffice writes, read by go-fast-ooxml (`TestDocTwins`), 846 documents | 99,388 blocks identical in kind, level, label and text; 10,178 only here, 15,573 only in the twin; words: precision 0.988, recall 0.985. By kind: 11,084 list items identical of about 11,800; 5,658 headings of about 6,000; 1,899 footnotes of about 2,100; 1,224 tables of about 1,650 |
| Words against LibreOffice's plain text, 841 documents | precision 0.983, recall 0.983, F1 0.983 (target 0.99: not met by this measure; see below) |
| `large.doc`, 1.5 million words | 0.06 s (target 0.2 s; LibreOffice 8.1 s) |
| Documents read | 867 of the 907 in the corpus; the rest are Word 2, encrypted (Phase 7), damaged or not Word files. One published document has no piece table that can be read |
| Document variables against the twins | 80 of 81 the same |
| Fuzzing | `FuzzWord` 60 million executions, `FuzzExtract` 23 million more. Three findings, fixed: control characters in a link's address, a negative slice at a gap between pieces, and control characters in text of two bytes to a character |

Why the plain-text figure is below the target, and what it does not say:

- **LibreOffice's text leaves out what this library reads**: the text of text boxes, footnotes, and headers and footers. Footnotes and headers are left out of the comparison; text boxes cannot be told apart, and count against precision. Documents whose text is all in text boxes score zero.
- **One held-out document of Word 6/95** is read in the wrong code page: about a third of the words that differ. A document of that version is decoded with one code page (that of its fonts where they agree on one, otherwise of its language); a document that mixes two is not read right.
- **The twins are the better measure**, since both sides are read into the same model, and there the differences are mostly LibreOffice's: it recalculates page numbers in footers, rewrites field codes, renames the Title style, and merges or splits some tables.

Found on the way:

- **The levels of the lists are not in the list table.** The table's length covers the lists; the levels follow it in the stream. Reading them from inside the table gave no list at all: the twin comparison showed 10,933 list items missing at once.
- **A row's definition is often too long for its page of properties**, and is then in the Data stream with a pointer on the page. Without it a table broke into pieces at every such row.
- **Not read**: form fields (their values are field results, and so are text; their drop-down entries are not given), equations (objects of the equation editor: listed as embedded files), the single-change form of a fast-saved piece's property modifier, and for Word 6/95 everything said above.

After the release, each library's owner decides whether its command routes embedded legacy files here (go-fast-email, go-fast-ooxml, go-fast-odf).

## 8. Risks

- **Word is the hard one.** Fast-saved files, property inheritance through three layers and the list labels are where readers go wrong. The parity test against the `.docx` twin is the main guard.
- **No Microsoft-written fixtures of our own.** Everything Office-made comes from other projects' test suites, which lean towards bug reports rather than ordinary documents. The data.gov.uk sample is the counterweight.
- **Specifications from memory.** The formats are read from the published [MS-DOC], [MS-XLS], [MS-PPT], [MS-ODRAW], [MS-OLEPS] and [MS-OFFCRYPTO]. Where a detail cannot be confirmed against a real file or an oracle, the reader refuses or counts rather than guesses, and the README says what was checked against what.
- **LibreOffice as oracle** hangs on some hostile files and once wrote 180 GB: it runs contained, always.
