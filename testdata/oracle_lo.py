#!/usr/bin/env python3
"""What LibreOffice makes of each file of one format of the corpus.

    python3 testdata/oracle_lo.py testdata testdata/corpus/oracle-lo-xls
    python3 testdata/oracle_lo.py testdata testdata/corpus/twins-xls --twin
    python3 testdata/oracle_lo.py testdata testdata/corpus/twins-doc --twin --format doc
    python3 testdata/oracle_lo.py testdata testdata/corpus/oracle-lo-doc --text --format doc
    python3 testdata/oracle_lo.py testdata testdata/corpus/twins-ppt --twin --format ppt

Without --twin, every sheet is saved as CSV with the cells as shown, and
<hash>.json.gz ({"sheets": {name: rows}}) is written for each file
LibreOffice converts, with index.json; TestShownAgainstLibreOffice compares
the library with them.

With --twin, each file is saved in the newer format (<hash>.xlsx, .docx
or .pptx), which go-fast-ooxml reads; TestTwins and its like compare what
the two libraries make of the pair. With --text, a Word document is saved
as plain text (<hash>.txt).

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


TWIN = {"xls": "xlsx", "doc": "docx", "ppt": "pptx"}


def convert(src, dest, twin=False, fmt="xls", text=False):
    work = tempfile.mkdtemp(prefix="gofastmsbin-lo-")
    try:
        profile, out = os.path.join(work, "profile"), os.path.join(work, "out")
        os.mkdir(out)
        copy = os.path.join(work, "f." + fmt)
        shutil.copyfile(src, copy)
        proc = subprocess.Popen(["soffice", "-env:UserInstallation=file://" + profile, "--headless", "--norestore",
                                 "--convert-to", TWIN[fmt] if twin else ("txt:Text (encoded):UTF8" if text else FILTER),
                                 "--outdir", out, copy],
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
        if twin or text:
            made = os.path.join(out, "f." + (TWIN[fmt] if twin else "txt"))
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
    twin, text = "--twin" in sys.argv[3:], "--text" in sys.argv[3:]
    fmt = sys.argv[sys.argv.index("--format") + 1] if "--format" in sys.argv else "xls"
    os.makedirs(dest, exist_ok=True)
    entries = [e for e in json.load(open(os.path.join(testdata, "corpus.json"))) if e["format"] == fmt]
    index_path = os.path.join(dest, "index.json")
    index = json.load(open(index_path)) if os.path.exists(index_path) else {}
    todo = [e for e in entries if e["sha256"] not in index and os.path.exists(os.path.join(testdata, e["path"]))]

    def one(e):
        try:
            ext = "." + TWIN[fmt] if twin else (".txt" if text else ".json.gz")
            return e["sha256"], convert(os.path.join(testdata, e["path"]), os.path.join(dest, e["sha256"] + ext), twin, fmt, text)
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
