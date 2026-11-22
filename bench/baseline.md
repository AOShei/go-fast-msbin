# Baselines

What the tools in use today make of the binary Office formats, measured before this
library reads any of them. Written by `bench/baseline.py report` from `bench/baseline.json`;
the method is in the script. One machine (a 12-core Ryzen 9 9900X), shared with other work,
so times are to be read as rough.

## The large files

| File | Size | Content |
|---|---|---|
| `large.doc` | 19.5 MB | LibreOffice, from go-fast-ooxml's large.docx (1.5 million words) |
| `large.ppt` | 7.6 MB | LibreOffice, from go-fast-ooxml's large.pptx (1,500 slides) |
| `large.xls` | 37.7 MB | 3 sheets of 65,536 rows by 10 columns: 1,966,080 cells |

| File | Tool | Time | Peak memory |
|---|---|---|---|
| `large.doc` | libreoffice, to txt:Text | 8.07 s | 910 MB |
| `large.ppt` | libreoffice, to pptx | 7.16 s | 1,050 MB |
| `large.xls` | calamine | 0.22 s | 215 MB |
| `large.xls` | libreoffice, to csv | 3.01 s | 907 MB |
| `large.xls` | xlrd | 1.09 s | 161 MB |

Each run is a process of its own, the time that of the whole process, the best of three
for the Python tools. LibreOffice's memory is that of its largest process.

## The corpus

2,528 files: 907 `.doc`, 510 `.ppt`, 1,111 `.xls`.

| Tool | Files | Read | Failed | Over 60 s | Time for the files read |
|---|---|---|---|---|---|
| xlrd | 1,111 `.xls` | 1,058 | 53 | 0 | 11.3 s |
| calamine | 1,111 `.xls` | 1,064 | 47 | 0 | 2.9 s |

A file counts as failed when the tool raises an error, whatever the reason: among the files
are damaged, encrypted and hostile ones, and ones that are not `.xls` files at all.

LibreOffice, on 40 files of each format drawn at random, a process for each file:

| Format | Converted to | Converted | Failed | Over 60 s | Median time a file |
|---|---|---|---|---|---|
| `.doc` | txt:Text | 40 | 0 | 0 | 2.17 s |
| `.xls` | csv | 38 | 2 | 0 | 2.07 s |
| `.ppt` | pptx | 39 | 1 | 0 | 2.17 s |
