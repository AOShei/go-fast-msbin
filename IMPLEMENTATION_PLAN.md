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
- **Not read**: form fields (their values are field results, and so are text; their drop-down entries are not given), equations (read since 2026-11-23: see "After Phase 9"), the single-change form of a fast-saved piece's property modifier, and for Word 6/95 everything said above.

### Phase 6: result (2026-11-22)

On branch `phase-6-powerpoint`: PowerPoint presentations of PowerPoint 97 to 2003.

- **The reader** (`internal/ppt`): the chain of edits from the Current User stream back to the first, and the persist directory each gives, so that the presentation is what the last edit left; the document's lists of slides, masters and notes pages; one `slide` part per slide with `Width`, `Height` and `Loc.BBox` on every block; slides read in parallel.
- **Text**: a slide's title and body are texts of the outline, which the document keeps with the list of slides; a text box holds its own. The kind of a block comes from the shape's placeholder (title, centred title: `heading` 1 and the part's name; subtitle: `heading` 2; date, footer, number: `footer`), as an `.pptx` file has it. Paragraphs, their levels, and their bullets from the paragraph or from the master's style for the kind of text and the level; the kinds of body text (centred, half, quarter) take what they do not say from the body's style. Bullets and characters of symbol fonts are translated by go-fast-ooxml's tables.
- **Numbered paragraphs**: the numbers are in what PowerPoint 2000 added to the format (a record of the document for the outline's texts, a record of the shape for its own), found through the run the paragraph's characters name. Counted by level, in the scheme the file gives.
- **Masters**: a master's own text and pictures are given with every slide that follows it, as `header` or `footer` by where they are on the slide, as go-fast-ooxml gives a layout's and a master's; the same thing twice in one place is given once. The date, the footer and the slide's number are given where the presentation or the slide says they are shown and the master has a place for them, in the master's words with the slide's values in its fields. Today's date is not in the file and is not given.
- **Tables** (a group of shapes marked as a table) by the places of the cells; **groups** with the places of their shapes worked out through the group's own coordinates; **pictures** from the Pictures stream; **notes** a paragraph at a time; **comments** with author and date; **links** as spans; an action that runs a macro or starts a program is `input.active_content`.
- **Charts**: a Microsoft Graph object keeps a workbook stream with the chart and a datasheet of the cells behind it. The datasheet is read by the Excel reader (`internal/xls`) and given as the chart's table at the place of the shape, on the slide, with the chart's type; the first row names what runs across and the first column what runs down, and each further row is a series.
- **Embedded objects** are records of the presentation's stream, each a compound file, often compressed: handed on as files (a workbook, a Word document, another presentation are read by this library). The VBA project is such a record too, and its modules are in the stored part.
- **Declared**: `hidden` for a slide left out of the show (the part) and a shape marked hidden. Nothing in this format answers to `no_ink`.
- **Repairs**: a file whose Current User stream does not lead to an edit is read from the last edit its stream holds (`input.repaired`). A file saved for PowerPoint 95 and 97 both is read from the storage that holds the PowerPoint 97 copy; a file with only the streams of PowerPoint 95 is refused as that generation.

**Measured:**

| | |
|---|---|
| Against the `.pptx` twin LibreOffice writes, read by go-fast-ooxml (`TestPptTwins`), 462 presentations | Without headers and footers: 54,845 blocks identical in kind, level, marker and text; 1,516 only here; 1,417 only in the twin. Words: recall 0.997, precision 0.976. By kind: 19,332 list items identical of about 20,200; 5,696 headings of 5,800; 26,261 paragraphs of 26,800; 3,317 notes of 3,700 |
| Headers and footers against the twin | 3,889 identical; 1,630 only here; 3,182 only in the twin. See below: most of the difference is how LibreOffice writes fields |
| Pictures per slide against the twin, 120 presentations | the same number on 3,026 of 3,104 slides |
| `large.ppt`, 1,500 slides | 0.007 s with all processors, 0.027 s with one (target 0.1 s) |
| Presentations read | 483 of the 510 in the corpus; the rest are PowerPoint 95 (3), encrypted (8, Phase 7), damaged containers (10) or encrypted packages of the newer format (6) |
| Microsoft Graph charts | 37 in the first 250 presentations, all read onto their slides |
| Fuzzing | `FuzzPresentation` 32 million executions. Two findings, fixed: a slide size below zero, and a text of the outline that named a text of the outline (endless recursion) |

What the differences from the twins are:

- **Headers and footers**: LibreOffice writes a master's fields as the words `<footer>`, `<number>` and `<date/time>`, and today's date as the day it converted the file; this library gives the footer and the number, and no date the file does not hold. It also gives the whole text of a number's place ("Page 5", or a number with a name after it) where LibreOffice keeps the number only.
- **Only here** (precision): notes and body text LibreOffice dropped when converting (the notes of one presentation are most of it), and the numbers of notes pages.
- **Only in the twin** (recall): equations, which LibreOffice converts to text and which are objects of the equation editor here (most of the 510 paragraphs only in the twin), and tables that are embedded Word or Excel objects.

Found on the way:

- **The strings of a property vector are not padded.** Office writes the strings of a vector (the titles of the parts of a document) one after another; reading each as padded to four bytes, as the description of the format reads, made the properties of most presentations and many workbooks look damaged (`msbin.properties_damaged`). Read as written, none of 60 published files says so.
- **A master's style for a kind of body text says only what differs from the body's.** Without that inheritance about 650 list items had a plain bullet in place of the master's.
- **What PowerPoint 2000 added is not where the rest is**: the document's part of it is inside the list of the document's other information, a shape's part inside the shape's own data, both wrapped in a record that is not marked as holding records.
- **Not read**: sound and video; equations; the animation of a slide; text of a master's placeholders (it is the prompt, "Click to edit", and not content); the text of earlier edits that the last one no longer points to (decided on 2026-11-22: left out of this release).

### Phase 7: result (2026-11-22)

On branch `phase-7-encryption`: encrypted files of the three formats, opened with `Options.Password`.

- **The schemes** (`internal/crypt`): XOR obfuscation (workbooks and Word documents), the RC4 encryption of Office 97, and RC4 with a key made by the CryptoAPI (all three formats; keys of 40 to 128 bits). The password is verified first, against the verifier the file keeps: `ErrPasswordRequired` with none, `ErrWrongPassword` with a wrong one. A header that cannot be read, or that names another algorithm than RC4, is `ErrUnsupported`.
- **Workbooks**: the record that says how follows the first; the data of the records is decrypted and their headers are not, nor the few records and the one field the format leaves as they are; with RC4 the key changes every 1,024 bytes of the stream. **Excel's own password** is tried unasked, and the warning says so.
- **Word documents**: the document's stream past its first 68 bytes (52 in Word 6/95), the table stream and the data stream; with RC4 a key to every 512 bytes.
- **Presentations**: every object of the stream, its header with it, with the key of its number; the object that says how is found through the last edit. The pictures, each field of each on its own with the first key.
- **How it is done**: the readers already hold their streams in memory, so a stream is decrypted in place once, when the file is opened, and the readers are not touched; sheets and slides are read in parallel as before. This is simpler than decrypting block by block under a stream interface, which the plan had foreseen, and costs no memory the readers did not already use. Decrypting a presentation is bounded by the size of its stream, whatever the lengths in it say.
- **`msbin.decrypted`** (info) names the scheme and whether the password given or Excel's opened the file.

**Measured:**

| | |
|---|---|
| Encrypted files of the corpus opened | 16 of 20: 12 with the passwords their test suites give (`TestEncryptedCorpus` checks the text of the 11 that are not held out, and a picture of an encrypted presentation), 4 workbooks with Excel's own password. The other 4 need passwords that are not known |
| Schemes verified on real files | XOR obfuscation (workbooks), RC4 (workbooks, Word), RC4 CryptoAPI (workbooks, Word, PowerPoint) |
| Fuzzing | `FuzzEncrypted`, 32 million executions, no finding |

- **Not verified on a real file: XOR obfuscation of a Word document.** The corpus has none, and neither LibreOffice nor Word since 97 writes one. It is tested against this project's own writer only, which was written from the same description. (Since 2026-11-23 it is checked against LibreOffice's reader: see "After Phase 9".)
- **The properties of an encrypted presentation** (added 2026-11-23) are read from the stream `EncryptedSummary`: each property set encrypted with a key of its own, and a list of them encrypted with the first key, as are the eight bytes that say where the list is. Checked against three files of the corpus written by PowerPoint.
- **Not read**: the properties of a Word document or workbook that encrypts them in the same way; no such file was found to check a reader against, and the encrypted Word documents and workbooks of the corpus keep their properties plain.
- msoffcrypto-tool was not needed as an oracle: the files with known passwords say in their text what they hold.

### Phase 8: result (2026-11-22)

On branch `phase-8-api-command`. Most of the API was written with Phase 1 (`Open`, `File`, `Parts`, `Part`, `Children`) and has carried every phase since; this phase closes it.

- **`File.Sheets`** lists a workbook's sheets (number, name, hidden) without reading them, as go-fast-ooxml's does. go-fast-ooxml's `File.Rows`, which reads a very large sheet in bands of rows, has no counterpart: a sheet of these formats has 65,536 rows at most, and the workbook's stream is held in memory whole.
- **The command's dispatcher**: a file embedded in a document is read by this library if it is a compound file of one of the three formats, and by go-fast-ooxml if it is a zip package or an encrypted Office Open XML package; which it is is decided from its first bytes. Anything else is listed with `child.unsupported`. The module now requires go-fast-ooxml v0.1.1, for the command only: `TestImportsStandardLibraryAndGoFastOnly` holds the library's packages to the standard library and go-fast-core.
- **The exported declarations** are held to `testdata/api.txt` (`TestAPISnapshot`, from go-fast-ooxml): 5 warning codes, 3 defaults, `Extract`, `ExtractFile`, `Extractor` with `MaxCells`, `File` with 11 methods, `Sheet`.
- **Examples** for a whole file, a part at a time, and a password; the package documentation says what the library does today.

### Phase 9: result (2026-11-22)

On branch `phase-9-hardening`. Nothing new is read; the release is made ready.

- **Soak** (`TestSoak`): every one of the 2,528 files of the corpus, the exploit files of LibreOffice's and POI's test suites among them, read four ways: one sheet or slide at a time with spans and cell details; eight at a time, the same; with a limit of 4,096 bytes of text; with the bytes of the pictures within a megabyte decoded. Every result is a valid document or one of the shared errors; the document is the same with one worker and with eight, byte for byte as JSON; no document holds more text than its limit; the test fails if the heap passes 3 GiB.
- **Worst cases bounded**, found by reading the PowerPoint reader for what a file can repeat: the check for a master holding the same thing twice was quadratic in its shapes (now a set); a slide's texts in the outline were searched from the start for every shape (now found once); a master's text that comes to nothing on a slide cost nothing, on every slide (now charged to the text the document may hold); a chart's file was read again for every shape that shows it without charge (now charged to the bytes the document may decode); the datasheet of a chart is cut to what a chart may hold. `TestPresentationWorstCases` holds two of them.
- **Allocation budgets** (`TestAllocationBudgets`): what one reading of each large file allocates, held to about a quarter over what was measured.
- **Readers reach nothing** (`TestReadersReachNothing`): the packages that read what a file holds import neither `os` nor `net` nor `os/exec`.
- **`SECURITY.md`**: what a file cannot do, every limit with its default, what the caller decides. **`CHANGELOG.md`** as the notes of the release. The placeholder reader of the early phases is gone.

**Measured:**

| | |
|---|---|
| Soak | 2,528 files, 2,427 documents, each read four ways in 25 to 38 s in all; 1,281 cut by the text limit; the slowest file 1.3 to 1.8 s for its four readings; heap peak 482 MiB |
| Files read with no password | 868 of 907 `.doc`, 1,076 of 1,111 `.xls` (calamine 1,064, xlrd 1,058), 483 of 510 `.ppt`: 2,427 of 2,528. The other 101: older generations (20), a generation the file does not state (16), not files of these formats (26), damaged containers (23), and encrypted files that need a password (16, of which 12 open with the passwords their test suites give) |
| `large.xls`, 1,966,080 cells | the library 0.10 to 0.12 s with all processors, 0.15 to 0.18 s with one (targets 0.22 s and 0.5 s; calamine 0.22 s, xlrd 1.09 s). 160 MiB allocated in 249 allocations |
| `large.doc`, 1.5 million words | 0.06 to 0.11 s (target 0.2 s; LibreOffice 8.1 s). 59 MiB in 41,800 allocations |
| `large.ppt`, 1,500 slides | 0.007 to 0.017 s, 0.027 to 0.061 s with one processor (target 0.1 s; LibreOffice 7.2 s). 41 MiB in 275,800 allocations |
| The command, writing JSON, as a process | `large.xls` 0.24 to 0.37 s, 285 to 395 MB at its peak; `large.doc` 0.13 to 0.23 s, 135 to 148 MB; `large.ppt` 0.05 to 0.09 s, 29 to 37 MB |
| Fuzz campaign, eight targets, four minutes each on eight workers | `FuzzExtract` 17 million executions, `FuzzWorkbook` 3.5 million, `FuzzWord` 13 million, `FuzzPresentation` 13 million, `FuzzCFB` 40 million, `FuzzRead` (properties) 64 million, `FuzzProject` (VBA) 61 million. `FuzzEncrypted` stopped after 235 seconds on the one finding, fixed: a ligature in a string of a defined name's formula reached the text of a block as it was. After the fix `FuzzEncrypted` ran 27 million more and `FuzzWorkbook` 5 million, with none |

The times are ranges because the machine was shared and busy (a load of 9 to 22 on 24 threads while they were taken); the lower ends are from the quieter runs.

- **The memory target for `large.xls` was not met at this point** (it is since 2026-11-23: see "After Phase 9"). It was 215 MB, calamine's peak as a process. The command peaks at 285 to 395 MB. The live heap is 105 to 130 MB (the workbook's stream, 38 MB, which is held whole; the shared strings; one sheet's cells and its table; the JSON of one part); the rest is the room Go's collector leaves, which a caller can set (`GOMEMLIMIT`, `GOGC`). Reading the stream in pieces instead of holding it would take about 38 MB off, and is a change to how all three readers read; it is not made for this release.
- **The time targets are met** by the library. The command, which also writes the document as JSON, takes two to three times as long as the library for the workbook.

**For go-fast-core's README** (proposed; not changed here, the file is another repository's):

- In the list of extractors: add `go-fast-msbin`.
- In "How each format maps": `doc (go-fast-msbin)` beside docx, `ppt (go-fast-msbin)` beside pptx, `xls (go-fast-msbin)` beside xlsx. The notes of those rows hold as they are: the binary formats are mapped as their successors are. `Loc.Anchor` in a `.doc` is the bookmark that starts the paragraph, as in rtf.
- After the table: "go-fast-msbin reads the templates and shows of the three binary formats, the workbooks of Excel 5 and 95 and the documents of Word 6 and 95, under the names doc, xls and ppt."

**Left for after the release**, as decided or found on the way:

- The text of earlier saves that a file no longer points to (Q9: to be reviewed after the release). **Decided by Andrew on 2026-11-23: dropped for now.** It is a known limitation, listed in the README, and is taken up again only if a user asks for it.
- Each library's owner decides whether its command routes embedded `.doc`, `.xls` and `.ppt` files here.
- For go-fast-ooxml's owner: it translates a whole cell by its font where the format has fonts by run; its number formats pick the negative section for a value that rounds to zero, and round the fifteenth digit from the binary value.
- Reading streams in pieces, for memory; the properties of encrypted presentations; equations; Word XOR obfuscation verified on a real file, if one is found. (Taken up on 2026-11-23: see "After Phase 9".)

### After Phase 9: what fell short, made good (2026-11-23)

Andrew asked for the shortfalls of the list above to be made good before the release. Each is a pull request of its own.

- **`Metadata.Encrypted`** is set on every decrypted document (core's rule of 2026-11-23, `CORE_TO_MSBIN_README_AND_ENCRYPTED.md`), Excel's own password included. The soak holds the field to the warning over the corpus.
- **The properties of an encrypted presentation**: see "Phase 7: result".
- **Equations** (`internal/mtef`). The Equation Editor and MathType keep an equation as an embedded object with a stream `Equation Native` in MTEF. Versions 2, 3 and 5 are read and written in UnicodeMath, by go-fast-ooxml's rules for the linear form.
  - *Word*: the mark between the code and the result of the object's field names the object (`sprmCPicLocation` with `sprmCFOle2`); the equation is text of the paragraph there, and its picture, the field's result, is left out. A paragraph that is an equation and nothing else is a `formula` block.
  - *PowerPoint*: an object said to be an equation (`ExOleObjAtom`, sub-type 6) is a `formula` block where the shape that shows it stands.
  - An equation that is read is not among the embedded files. One whose records break off or do not follow one another is refused whole, and stays a picture and a file.
  - **Found in files, against the published description**: in version 3 a record of embellishments holds several, ended by a zero (in version 5, one); the brackets in a fence's list are the pieces they are drawn with, in a typeface of their own, so the selector decides the brackets; the templates of an operator "with limits" (selectors 42 and 43 of version 3) keep the operator the author wrote in a fourth slot, and are used to put a script on a letter.
  - **Measured**: the corpus has 509 equations in 41 files (103 in 11 Word documents, 406 in 30 presentations; none in a workbook): MTEF 2 36, MTEF 3 433, MTEF 5 40. All 509 are read without a refusal; all 103 of the Word documents are placed in their text, and 401 of the 406 of the presentations (the other 5 are not said to be equations by their presentation, and stay files). Against the twins, by letters and digits (`TestEquationTwins`): in the 7 Word documents with equations on lines of their own, 1,433 of the twins' letters and digits were missing here and 7 are now; in the 30 presentations, 6,464 and now 329. 1,198 are only here in the presentations (101 before): LibreOffice does not turn every equation into text, those of MathType among them.
  - **Fuzzing** (`FuzzLinear`): one finding, a template with no form of its own wrote each of its parts twice, so that nesting doubled the work at every level; fixed, the input kept. 57 million executions after, with none.
  - **Not read**: equations in workbooks (the corpus has none to check against) and in Word 6/95 documents.

- **Memory** (the target that was not met). Measured first, on a quiet machine: the command peaked at 306 to 403 MB on `large.xls`. Three causes, in the order of what they cost:
  1. *The command encoded each part whole.* `json.Encoder` builds a value in memory before it writes it, and again to indent it: a sheet of 21 MB of JSON cost about 100 MB, three sheets 300 MB of garbage in a run that the collector had no time to take back. The command now writes a part a block at a time and a table a row at a time, the same bytes (`TestPartIsWrittenAsTheEncoderWritesIt`, and `TestStreamedJSONOverTheCorpus` over every file, indented and not).
  2. *The workbook's stream was held for as long as the file was open* (36.5 MB). Each sheet now reads its own records from the stream and lets them go (`xls.OpenStream`); an encrypted workbook, a short stream and one that cannot be read throughout are held as before. The document is the same for all 1,111 workbooks of the corpus, three ways each (`TestWorkbookRegions`); 913 of the 1,076 that open are read this way. Between sheets the library holds 5 MiB of `large.xls`, from 41.
  3. *Parts were read in batches*, a batch whole before any of it was handed on: with 24 workers, 96 parts. They are now handed on in order as each is read (`TestPartsInOrder`: order, a caller that stops, a context that ends, a limit among the parts, no worker left behind; also under the race detector). For `File.Parts` the sheets of a workbook are read ahead by no more than 32 MiB of records at a time, and 64 MiB begun and not handed on; `File.Document` reads with every worker, as before.

  | The command, writing JSON | before | after |
  |---|---|---|
  | `large.xls`, all processors | 0.16 to 0.18 s, 306 to 403 MB | 0.09 to 0.11 s, 156 to 160 MB |
  | `large.xls`, one worker | 0.22 s, 253 to 265 MB | 0.16 s, 93 to 97 MB |
  | `large.doc` | 0.10 s, 136 to 148 MB | 0.10 s, 69 to 72 MB |
  | `large.ppt` | 0.03 s, 35 to 39 MB | 0.03 s, 33 to 35 MB |

  The target of 215 MB (calamine's peak) is met. The library's own times are as they were (`large.xls` 0.052 s with all processors, 0.113 s with one). The figures of "Phase 9: result" were taken on a busy machine; these on a quiet one (load 1 to 1.5).
- **Word XOR obfuscation.** No file written by Word in this scheme was found: the corpus has none, and LibreOffice (26.8) no longer writes Word 6/95 files, the only ones it obfuscated. It still reads them. So the check is turned around (`TestEncryptedWordAgainstLibreOffice`, run with `GOFAST_MSBIN_LIBREOFFICE=1`; LibreOffice contained by `testdata/oracle_crypt.py`): the documents this project's writer obfuscates, of Word 97 and of Word 6, are opened by LibreOffice with their password, which gives exactly their text, accents included, and are refused with another password; the same for RC4. The reader here and LibreOffice's therefore agree on the key, the verifier, the array, the bytes left as they are, and where the obfuscated part begins (0x44 and 0x34). The obfuscation of Word 6/95 documents, which no test covered, now has one (`TestEncryptedWord6`). What stays unverified: a file written by Word itself.

- **Shared warning codes** (core v0.1.2, `CORE_TO_MSBIN_SHARED_CODES.md`, 2026-11-23). The six codes this library had of its own are core's now: `msbin.control_characters_removed` is `text.control_characters_removed`, `msbin.unread_content` `input.unread_content`, `msbin.part_damaged` and `msbin.properties_damaged` `input.stream_damaged` (for the properties the message names the stream), `msbin.region_split` `table.region_split`, `msbin.decrypted` `input.decrypted`. The five exported constants are gone; the library exports no warning code. Checked over the corpus with the command before and after: of 2,528 files, 2,438 give the same JSON but for these codes and the message of the properties, 90 are refused by both, none differs otherwise. The command requires go-fast-ooxml v0.2.0, which raises the same codes, so a `.docx`, `.xlsx` or `.pptx` embedded in a document warns as the document does.

### After v0.1.0: a sweep, and speed (2026-11-23)

**The sweep.** Every source file, the documents and the comments of the tests were read against the code by three reviewers, each finding checked before anything was changed: about forty statements corrected, three unused declarations removed, and two defects fixed (the count of unmapped characters of equations outside the main text; a warning's wording).

**Benchmarks**, new: `BenchmarkCorpus*` reads every file of one format one after another, as a batch of ordinary files is read (the large generated files are measured apart, as before). On a quiet machine, before any change:

| Batch | Files, size | All workers | One worker | Others |
|---|---|---|---|---|
| `.xls` | 1,111, 413 MB | 1.09 s | 1.28 to 1.37 s | calamine 2.9 s, xlrd 11.3 s (for the files each reads, in Python) |
| `.doc` | 907, 207 MB | 0.20 s | | LibreOffice: 2.2 s a file |
| `.ppt` | 510, 382 MB | 0.45 s | 0.48 to 0.49 s | LibreOffice: 2.2 s a file |

What the profiles showed, and what was done:

- **Presentations: 58% of the time went to expanding embedded objects that were only going to be listed** (no `Options.Child`). They are now expanded only as far as it takes to read their directory, which says what they are; an object that is handed on is expanded into room made beforehand from the size it states. A compound file's allocation table can lie anywhere in it, so about half of the bytes are still expanded. One file of the corpus changes: a damaged one, whose two truncated objects are now listed with the size they state and not the size that could be expanded.
- **Joining text by copying** (found in the Word profile, where one file of 0.4 MB took 22 ms): see the changelog. `TestWordLongRunOfCode` and `TestPresentationLongTitle` hold the two that a file can make arbitrarily long.
- **Workbooks**: the text of a sheet's cells was kept in pieces of 256 KiB from the first, which for a workbook of many small sheets was most of what it allocated (pieces now grow from 4 KiB); a sheet's records were read into two allocations (now one); the list of cells grows by what the rest of the sheet is expected to hold; the fifteen digits of a whole number or of one with up to four decimals are found without the general conversion, with the same digits (`TestShortDigitsAreTheLongDigits`, some fifty million numbers against the long way).

| Batch | All workers | One worker | Allocated |
|---|---|---|---|
| `.xls` | 0.91 s (was 1.09) | 1.12 s (was 1.28 to 1.37) | 2.96 GB (was 3.93) |
| `.doc` | 0.18 s (was 0.20) | | 0.42 GB (was 0.61) |
| `.ppt` | 0.30 s (was 0.45) | 0.36 s (was 0.48 to 0.49) | 0.84 GB (was 1.01) |

The large files are as they were (`large.xls` 0.05 s, `large.doc` 0.06 s, `large.ppt` 0.007 s). The documents are byte for byte the same for all 2,528 files under four settings, but for the one file above.

What is left, by the profile of the workbooks: placing cells into tables (23%, bound by memory), number formats (17%), clearing and copying large allocations (15%).

**Content that is not read, said** (2026-11-23, Andrew's decision after the sweep found it): only dialog sheets and formulas not read back raised `input.unread_content`, though the README listed more as not read. Now counted too: the sounds a presentation keeps (the records of its sound collection) and the videos and sounds it plays from outside (the movie and linked-audio records of its object list), the text boxes of an Excel 5/95 workbook (objects of kind 6), and the drop-down lists of a Word form (`FORMDROPDOWN` fields, in any story). In the corpus: sounds in 26 presentations, videos in 7, form drop-downs in 9 Word documents, Excel 5/95 text boxes in 1 workbook. Not counted: a pivot cache whose source is gone, form drop-downs in Word 6/95 documents, and the animation of slides, which is not content.

**A second round of speed** (2026-11-23, after Andrew asked for what the profile above left). Measured on the same machine, before and after, two runs each; the documents are byte for byte the same for every file of the corpus, through the command with `-cells` and through the library with objects only listed.

- **Workbooks.** Placing a cell in its table asked for the cell's entry (formula, type, link) twice, through an interface that returns the whole entry, whether or not the sheet has any entry at all; most have none. It is asked only of sheets that have one. This was most of the 23% the profile gave to placing cells. The digits of a fraction's numerator and denominator were made for every number, and are made for fraction formats; the scratch room of the cutter is as wide as the band it cuts, not as a sheet can be.
- **Presentations.** An object was opened whole to say what it is: all of its allocation table, its directory, its short streams. `cfb.OpenDirectory` reads the header, the directory, and of the table the sectors the directory's chain goes through (`TestDirectoryAlone`; the fuzz target holds it to `Open`: what opens whole opens this way and names the same). The object is expanded as far as its directory lies, which in the files of the corpus is often near the end: expanding is still 41% of the time of a batch. Handed on or listed, an object is named the same way, so one damaged outside its directory now has its format in its name; no file of the corpus changes.
- **Tried and dropped**: making room for fewer cells at first and leaving the rest to the estimate from what has been read (more allocated, 2.70 GB against 2.62, and no faster).
- **Word documents**: the profile is flat (the largest function 11%), and nothing was changed.

| Batch | All workers | One worker | Allocated |
|---|---|---|---|
| `.xls` | 0.77 s (was 0.88) | 0.92 to 0.93 s (was 1.09 to 1.10) | 2.72 GB (was 2.96) |
| `.doc` | 0.18 s (as it was) | | 0.42 GB |
| `.ppt` | 0.28 s (was 0.31 to 0.32) | 0.33 to 0.34 s (was 0.36 to 0.37) | 0.79 GB (was 0.83) |

The large files are as they were. What is left in workbooks, by the profile: number formats (26%, of which finding a number's digits is over a third) and clearing and copying memory (17%: the list of cells, a sheet's records, the tables). Counting a sheet's cells before reading them would make the list of cells exact; by estimate, not measured, the count costs about what it saves.

**The number-format engine follows LibreOffice** (2026-11-24; Andrew's decision, go-fast-ooxml PR #47 and v0.3.0, `OOXML_TO_MSBIN_NUMFMT_6B.md`). Of the two differences of "Phase 2: result":

- *The fifteenth digit*: brought over as it is in go-fast-ooxml v0.3.0 (`decimal.long`, `appendElapsed`, `TestFifteenthDigitAndZeroDurations`, and the table, now 6,972 pairs). Every cell of every workbook of the corpus compared before and after: 5,857 of 17,959,702 change, in 58 workbooks. 5,854 differ by a unit in the fifteenth digit. 3 are numbers below 1e-300, which the format keeps with fewer digits than fifteen: the shortest decimal has those and no more (`2.121995791E-314`, which was `2.12199579096527E-314`). That follows from the rule; what LibreOffice shows for such numbers is not in the table and was not checked. Through the command, the datasheets of charts in 5 presentations change the same way.
- *A negative value that rounds to zero*: nothing changed. go-fast-ooxml's table has LibreOffice using the negative section for a stored value down to -1e-15, as the engine does. The case of Phase 2 was seen against a twin, a file LibreOffice converted, and LibreOffice turns a formula's result within rounding error of zero into 0 when it recalculates: a different value, not a different format. The file of that case was not found again to confirm it held a formula.

The command requires go-fast-ooxml v0.3.0.

**Core's decompressor** (2026-11-24; go-fast-core v0.1.5, `CORE_TO_LIBS_SHARED_INFLATE.md`). The three places that used `compress/zlib` use `github.com/AOShei/go-fast-core/inflate`:

- an embedded object of a presentation that is handed on, and its macro storage: `inflate.Zlib`, in one pass into the room the record says it needs;
- an object that is only listed: an `inflate.Reader` over the deflate stream after the two bytes of the zlib header, read as far as the directory lies. The stream's checksum is not looked at there. It was looked at only when an object was read to its end, and then what had expanded was kept all the same, so nothing is read differently;
- a compressed metafile (`internal/officeart`): `inflate.Zlib`. It is expanded only when the pictures' bytes are asked for, so an ordinary run gains nothing there.

A stream whose header names a preset dictionary is refused, as `compress/zlib` refused it. The limits are held as before: one byte over the limit is asked for, to tell an object of exactly the limit from one over it.

Checked with the command over all 8,067 files of the corpus (JSON with `-cells -images data`, and Markdown with `-stored`): every output is byte for byte the same. The batch of presentations, on a machine other sessions were using (three runs each way; one of the runs after was disturbed and is left out):

| Batch | All workers | One worker | Allocated |
|---|---|---|---|
| `.ppt` | 0.20 to 0.22 s (was 0.29 to 0.32) | 0.26 to 0.28 s (was 0.35 to 0.37) | 0.78 GB (was 0.79) |

Workbooks and Word documents are as they were: nothing in them is expanded in an ordinary run. The CI step that holds the library to go-fast-core allows the packages of that module, not only its root.

**Released:** `v0.1.0` was tagged at `067181a` on 2026-11-23. What came after it, down to core's decompressor above, is `v0.1.1` (2026-11-24), tagged at the merge of the pull request that dated that section of the changelog.

**After v0.1.1: the last join by copying** (2026-11-24). A cell of a Word table merged into the cell before it in its row had its text added with `+=`, a copy of the cell for each. It is bounded, since a row's definition counts its cells in a byte, and so it was left when the unbounded joins were fixed. The text is now gathered and put in the cell once, when the cells merged into it end. A row of 250 merged cells of 64 KiB each: 0.13 s (was 0.23 s), 151 MB allocated (was 2,030 MB); `TestWordLongRowOfMergedCells` holds the allocation, `TestWordCellsMergedAlongARow` what such a row gives, which no test covered. The test writer now puts a row definition too long for a page of properties in the Data stream, as Word does (`sprmPHugePapx`), and no longer loops on one. The command's output over the corpus is byte for byte the same.

After the release, each library's owner decides whether its command routes embedded legacy files here (go-fast-email, go-fast-ooxml, go-fast-odf).

## 8. Risks

- **Word is the hard one.** Fast-saved files, property inheritance through three layers and the list labels are where readers go wrong. The parity test against the `.docx` twin is the main guard.
- **No Microsoft-written fixtures of our own.** Everything Office-made comes from other projects' test suites, which lean towards bug reports rather than ordinary documents. The data.gov.uk sample is the counterweight.
- **Specifications from memory.** The formats are read from the published [MS-DOC], [MS-XLS], [MS-PPT], [MS-ODRAW], [MS-OLEPS] and [MS-OFFCRYPTO]. Where a detail cannot be confirmed against a real file or an oracle, the reader refuses or counts rather than guesses, and the README says what was checked against what.
- **LibreOffice as oracle** hangs on some hostile files and once wrote 180 GB: it runs contained, always.
