#!/usr/bin/env python3
"""What xlrd reads of each .xls file of the corpus: the sheets and the stored
value of every cell.

    python -I testdata/oracle_xls.py testdata testdata/corpus/oracle-xls

Writes <hash>.json.gz for each file xlrd opens, and index.json saying for
every file whether it did. TestCellsAgainstXlrd compares the library with
them. Each file is read in a process of its own with a time limit. At most
MAX_CELLS cells of a file are written, in the order of the sheets.
"""
import concurrent.futures
import gzip
import json
import math
import os
import subprocess
import sys

MAX_CELLS = 300_000


def read(path, dest):
    import xlrd
    try:
        book = xlrd.open_workbook(path, on_demand=True)
    except Exception as e:
        return {"error": type(e).__name__}
    out = {"biff": book.biff_version, "date1904": bool(book.datemode), "sheets": []}
    left = MAX_CELLS
    for i in range(book.nsheets):
        try:
            sh = book.sheet_by_index(i)
        except Exception as e:
            out["sheets"].append({"error": type(e).__name__})
            continue
        cells = []
        for r in range(sh.nrows):
            if left <= 0:
                break
            types, values = sh.row_types(r), sh.row_values(r)
            for c, (t, v) in enumerate(zip(types, values)):
                if t in (0, 6) or (t == 1 and v == ""):
                    continue
                if isinstance(v, float) and (math.isnan(v) or math.isinf(v)):
                    v = None
                cells.append([r, c, t, v])
                left -= 1
        out["sheets"].append({"name": sh.name, "visibility": sh.visibility, "cells": cells, "cut": left <= 0})
        book.unload_sheet(i)
    with gzip.open(dest, "wt", encoding="utf-8") as f:
        json.dump(out, f)
    return {"ok": True, "biff": book.biff_version, "sheets": book.nsheets}


def main():
    if sys.argv[1] == "--one":
        print(json.dumps(read(sys.argv[2], sys.argv[3])))
        return
    testdata, dest = sys.argv[1], sys.argv[2]
    os.makedirs(dest, exist_ok=True)
    entries = [e for e in json.load(open(os.path.join(testdata, "corpus.json"))) if e["format"] == "xls"]
    index_path = os.path.join(dest, "index.json")
    index = json.load(open(index_path)) if os.path.exists(index_path) else {}
    todo = [e for e in entries if e["sha256"] not in index and os.path.exists(os.path.join(testdata, e["path"]))]

    def one(e):
        try:
            out = subprocess.run([sys.executable, "-I", os.path.abspath(__file__), "--one", os.path.join(testdata, e["path"]),
                                  os.path.join(dest, e["sha256"] + ".json.gz")], capture_output=True, timeout=120)
            res = json.loads(out.stdout.decode().strip().splitlines()[-1])
        except subprocess.TimeoutExpired:
            res = {"error": "timeout"}
        except Exception as ex:
            res = {"error": type(ex).__name__}
        return e["sha256"], res

    with concurrent.futures.ThreadPoolExecutor(12) as pool:
        for sha, res in pool.map(one, todo):
            index[sha] = res
    json.dump(index, open(index_path, "w"))


if __name__ == "__main__":
    main()
