# Security

This library is written to read files from people who are not trusted: an
upload, an attachment, a crawl. This page says what it promises about such
files, what it leaves to the caller, and how to report a problem.

## Reporting a vulnerability

Use GitHub's private reporting for this repository (the **Security** tab,
**Report a vulnerability**). Please do not open a public issue for something
that lets a file crash the library, exhaust memory, or read outside the
input. A file that reproduces the problem is the most useful thing to send.

Any of the following is a bug to report, whatever the file:

- a panic, a stack overflow, or a call that does not return;
- memory or time beyond what the limits below allow;
- a result that is neither a valid document nor one of the shared errors;
- output that differs with the number of workers;
- text the file declares hidden that comes out without that reason, where
  the README lists the declaration for the format.

## What a file cannot do

| | |
|---|---|
| **Run anything** | Macros are never run, formulas never evaluated, fields never updated. Their presence is reported (`input.active_content`); the code of VBA projects and macro sheets, and the codes of fields, are read as text and marked as stored data |
| **Reach anything** | No file other than the input is opened and no network connection is made. Links to other workbooks and linked objects are reported (`input.external_reference`), never followed. The packages that read file content do not import `os`, `net` or `os/exec`, and a test fails if one starts to |
| **Crash the caller** | No panic on any input: every offset, length and count taken from a file is checked, every chain of sectors, edits and records is followed with a bound, and every list a file can lengthen has one |
| **Write what it likes into the output** | Text is valid UTF-8 with control characters removed. Names taken from the file (sheets, authors, fonts, error messages) are cleaned and cut to a length before they appear in a warning or a field |
| **Lose what it declares about display** | A sheet, row, column, slide, shape or text the file declares hidden, in the ways the README lists, keeps that reason. Nothing is judged from appearance, so content can be out of sight with no reason; see below |
| **Be read past its damage unnoticed** | A compound file with a wrong byte-order mark or a broken chain, a stream that breaks off, a presentation whose Current User stream leads nowhere: each is read as far as it is sound, and says so (`input.repaired`, `msbin.part_damaged`) |

## Limits

Every cost a file can impose is bounded by a limit with a default. A limit
that is reached either refuses the file (`ErrLimitExceeded`) or cuts the
document there with a `limit.exceeded` warning; which, is in the table.

| Limit | Default | Where | When reached |
|---|---|---|---|
| Size of the file | 1 GiB | `Options.Limits.MaxInputBytes` | Refused before anything is read |
| Bytes expanded, by the document and the files embedded in it together: macro code, compressed pictures and objects, embedded files written out, a chart's file each time a shape shows it | 2 GiB | `Options.Limits.MaxDecodedBytes` | What is being expanded is left out and said; embedded files after that are listed, not read |
| Text the document holds, with the files embedded in it | 256 MiB | `Options.Limits.MaxTextBytes` | The part being read is cut off; embedded files after that are listed, not read |
| Cells kept from a workbook | 20 million | `Extractor.MaxCells` | The sheet is cut off |
| Depth of embedded documents | 4 | `Options.Limits.MaxDepth` | Listed, not opened |
| Entries of the compound file's directory | 262,144 | fixed | Refused |
| Sheets of a workbook; slides of a presentation | 65,536 each | fixed | Those past it are not read, and said |
| Shapes of a slide | 16,384 | fixed | Those past it are not read, and said |
| Cells of one table of a Word document | 4 million | fixed | The table is cut there, and said |
| A formula turned back into text | 32 KiB | fixed | The formula is not read back, and counted in `msbin.unread_content` |
| Points of a chart; of all the charts of a sheet together | 1 million; 2 million | fixed | Cut there, and said |
| Embedded objects | 4,096, to a depth of 64 storages | fixed | Those past it are not listed, and said |
| VBA projects; modules of a project | 16; 4,096 | fixed | Those past it are not read, and said |
| Nesting of fields in a Word document; of groups of shapes | 64 | fixed | What is deeper is read as text, or not read |
| An equation: its bytes; the nesting of its parts | 1 MiB; 64 | fixed | The equation is not read as text: it stays a picture and an embedded file |

The time a file takes follows from these: reading is linear in the size of
the file and in the text it gives, and what a file repeats (a master's text
on every slide, a chart shown by many shapes) is charged to the limits each
time.

## Encrypted files

A password is checked against what the file keeps for that before anything
is decrypted with it. The three schemes of these formats (XOR obfuscation,
RC4 with a key of 40 bits, RC4 with a CryptoAPI key) are weak, and one of
them is not encryption at all: the library reads them because documents
kept in them exist, and nothing should be taken as protected because it was
saved in one. Checking a password costs a few hashes; there is no count of
rounds for a file to raise.

## What the caller decides

- **What is shown.** The library reports what a file declares about the
  display of its content, and does not judge. Text in white on white, in a
  size too small to read, or under a picture comes out as text. A caller
  for whom that matters (a pipeline that feeds a model with documents from
  strangers) has to treat extracted text as untrusted input.
- **Embedded files.** They are read only by the extractor the caller gives
  as `Options.Child`, within the same limits; without one they are listed.
- **Memory.** The library holds the stream it reads (a workbook's, a
  document's, a presentation's) in memory, and the document it builds. For
  the largest files of the test corpus that is 100 to 150 MB of live heap.
  `Options.Limits.MaxInputBytes` bounds the first, `MaxTextBytes` and
  `Extractor.MaxCells` the second.

## How this is tested

Eight fuzz targets run in CI and were run for the release (the counts are in
`IMPLEMENTATION_PLAN.md`). A soak test reads all 2,528 files of the corpus,
the exploit files of LibreOffice's and POI's test suites among them, four
ways each: every result a valid document or a shared error, the same
document with one worker and with eight, no more text than the limit, a heap
under a ceiling. Worst-case tests hold the costs above.
