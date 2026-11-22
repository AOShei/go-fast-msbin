#!/usr/bin/env python3
"""What LibreOffice makes of each .xls file of the corpus.

    python3 testdata/oracle_lo.py testdata testdata/corpus/oracle-lo-xls
    python3 testdata/oracle_lo.py testdata testdata/corpus/twins-xls --twin

Without --twin, every sheet is saved as CSV with the cells as shown, and
<hash>.json.gz ({"sheets": {name: rows}}) is written for each file
LibreOffice converts, with index.json; TestShownAgainstLibreOffice compares
the library with them.

With --twin, each file is saved as .xlsx, <hash>.xlsx: the same workbook in
the newer format, which go-fast-ooxml reads. TestTwins compares what the
two libraries make of the pair.

LibreOffice is contained: a process group of its own that is killed as a
group at the time limit, a limit on the size of any file it writes, a
profile and an output directory of its own, both removed afterwards.
"""
import concurrent.futures
import csv
import gzip
import json
import os
import resource
import shutil
import signal
import subprocess
import sys
import tempfile

TIMEOUT = 120
MAX_FILE = 256 << 20
MAX_CELLS = 400_000
FILTER = 'csv:Text - txt - csv (StarCalc):44,34,76,1,,0,false,true,true,false,false,-1'


def limits():
    resource.setrlimit(resource.RLIMIT_FSIZE, (MAX_FILE, MAX_FILE))


def convert(src, dest, twin=False):
    work = tempfile.mkdtemp(prefix="gofastmsbin-lo-")
    try:
        profile, out = os.path.join(work, "profile"), os.path.join(work, "out")
        os.mkdir(out)
        copy = os.path.join(work, "f.xls")
        shutil.copyfile(src, copy)
        proc = subprocess.Popen(["soffice", "-env:UserInstallation=file://" + profile, "--headless", "--norestore",
                                 "--convert-to", "xlsx" if twin else FILTER, "--outdir", out, copy],
                                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True, preexec_fn=limits)
        status = "ok"
        try:
            proc.wait(TIMEOUT)
        except subprocess.TimeoutExpired:
            status = "timeout"
        finally:
            try:
                os.killpg(proc.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            proc.wait()
        if status != "ok":
            return {"error": status}
        if twin:
            made = os.path.join(out, "f.xlsx")
            if not os.path.exists(made):
                return {"error": "failed"}
            shutil.move(made, dest)
            return {"ok": True}
        sheets, left = {}, MAX_CELLS
        csv.field_size_limit(1 << 30)
        for name in sorted(os.listdir(out)):
            if not (name.startswith("f-") and name.endswith(".csv")):
                continue
            rows = []
            with open(os.path.join(out, name), newline="", encoding="utf-8", errors="replace") as f:
                for row in csv.reader(f):
                    if left <= 0:
                        break
                    left -= len(row)
                    rows.append(row)
            sheets[name[2:-4]] = rows
        if not sheets:
            return {"error": "failed"}
        with gzip.open(dest, "wt", encoding="utf-8") as f:
            json.dump({"sheets": sheets, "cut": left <= 0}, f)
        return {"ok": True, "sheets": len(sheets)}
    finally:
        shutil.rmtree(work, ignore_errors=True)


def main():
    testdata, dest = sys.argv[1], sys.argv[2]
    twin = "--twin" in sys.argv[3:]
    os.makedirs(dest, exist_ok=True)
    entries = [e for e in json.load(open(os.path.join(testdata, "corpus.json"))) if e["format"] == "xls"]
    index_path = os.path.join(dest, "index.json")
    index = json.load(open(index_path)) if os.path.exists(index_path) else {}
    todo = [e for e in entries if e["sha256"] not in index and os.path.exists(os.path.join(testdata, e["path"]))]

    def one(e):
        try:
            return e["sha256"], convert(os.path.join(testdata, e["path"]), os.path.join(dest, e["sha256"] + (".xlsx" if twin else ".json.gz")), twin)
        except Exception as ex:
            return e["sha256"], {"error": type(ex).__name__}

    done = 0
    with concurrent.futures.ThreadPoolExecutor(6) as pool:
        for sha, res in pool.map(one, todo):
            index[sha] = res
            done += 1
            if done % 50 == 0:
                json.dump(index, open(index_path, "w"))
    json.dump(index, open(index_path, "w"))


if __name__ == "__main__":
    main()
