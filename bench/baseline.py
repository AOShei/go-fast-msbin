#!/usr/bin/env python3
"""Baselines for go-fast-msbin: what the tools in use today make of the
binary Office formats, before this library reads any of them.

  python bench/baseline.py generate   write the large files under
                                      testdata/corpus/generated/
  python bench/baseline.py corpus     run xlrd and calamine over every .xls of
                                      the corpus, and LibreOffice over a sample
                                      of each format
  python bench/baseline.py large      time and peak memory of each tool on the
                                      large files, each in a process of its own
  python bench/baseline.py report     write bench/baseline.md from the results

Run it with the Python of bench-env/.venv-ooxml, which has xlrd, xlwt and
python-calamine. The results are kept in bench/baseline.json, a section for
each step.

Held-out files of the corpus are run like the others. Only counts are kept:
no name of a held-out file, and nothing a tool says about one, is written.

LibreOffice is run contained: in a process group of its own, which is killed
as a whole when its time is up and when it returns, under a file-size limit,
with a profile of its own that is deleted afterwards.
"""
import collections
import datetime
import json
import os
import random
import resource
import shutil
import signal
import subprocess
import sys
import tempfile
import time

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
TESTDATA = os.path.join(ROOT, "testdata")
GENERATED = os.path.join(TESTDATA, "corpus", "generated")
RESULTS = os.path.join(HERE, "baseline.json")
OOXML_GENERATED = os.path.join(ROOT, "..", "go-fast-ooxml", "testdata", "corpus", "generated")
SAMPLE = 40  # files of each format given to LibreOffice
LO_TARGET = {"doc": "txt:Text", "xls": "csv", "ppt": "pptx"}


def load():
    if os.path.exists(RESULTS):
        with open(RESULTS, encoding="utf-8") as f:
            return json.load(f)
    return {}


def save(section, value):
    results = load()
    results[section] = value
    with open(RESULTS, "w", encoding="utf-8") as f:
        json.dump(results, f, indent=1, sort_keys=True)
        f.write("\n")


def manifest():
    with open(os.path.join(TESTDATA, "corpus.json"), encoding="utf-8") as f:
        entries = json.load(f)
    return [e for e in entries if os.path.exists(os.path.join(TESTDATA, e["path"]))]


# ------------------------------------------------------------------ containment

def run_contained(args, timeout, max_file_bytes=512 << 20):
    """Run a program so that it cannot outlive its time limit or fill the
    disk. Returns (seconds, peak resident bytes, status); status is "ok",
    "timeout" or "failed". The peak is the kernel's for the process and the
    children it waited for, which for LibreOffice's launcher is the program
    itself."""

    def limits():
        resource.setrlimit(resource.RLIMIT_FSIZE, (max_file_bytes, max_file_bytes))

    def killed(signum, frame):
        raise TimeoutError()

    start = time.perf_counter()
    proc = subprocess.Popen(args, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                            start_new_session=True, preexec_fn=limits)
    status, usage = "ok", None
    previous = signal.signal(signal.SIGALRM, killed)
    signal.alarm(int(timeout))
    try:
        _, code, usage = os.wait4(proc.pid, 0)
        if code != 0:
            status = "failed"
    except TimeoutError:
        status = "timeout"
    finally:
        signal.alarm(0)
        signal.signal(signal.SIGALRM, previous)
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        if usage is None:
            _, _, usage = os.wait4(proc.pid, 0)
        proc.returncode = 0  # reaped here, not by Popen
    took = time.perf_counter() - start
    return took, usage.ru_maxrss * 1024, status


def libreoffice(src, target, outdir, timeout):
    """Convert one file with LibreOffice, with a profile of its own."""
    with tempfile.TemporaryDirectory(prefix="gofastmsbin-lo-") as profile:
        took, peak, status = run_contained(
            ["soffice", "-env:UserInstallation=file://" + profile, "--headless", "--norestore",
             "--convert-to", target, "--outdir", outdir, src], timeout)
    if status == "ok" and not os.listdir(outdir):
        status = "failed"  # LibreOffice exits with 0 when it could not load the file
    return took, peak, status


# ------------------------------------------------------------------ generate

def generate():
    os.makedirs(GENERATED, exist_ok=True)
    out = {}
    path = os.path.join(GENERATED, "large.xls")
    if not os.path.exists(path):
        import xlwt
        rng = random.Random(1)
        words = ["alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india", "juliett",
                 "kilo", "lima", "mike", "november", "oscar", "papa", "quebec", "romeo", "sierra", "tango"]
        book = xlwt.Workbook()
        money = xlwt.easyxf(num_format_str="#,##0.00")
        percent = xlwt.easyxf(num_format_str="0.0%")
        date = xlwt.easyxf(num_format_str="yyyy-mm-dd")
        rows, cols = 65536, 10
        for s in range(3):  # 3 sheets of 65,536 rows by 10 columns: 1,966,080 cells
            sheet = book.add_sheet(f"Sheet{s + 1}")
            for r in range(rows):
                row = sheet.row(r)
                row.write(0, r + 1)
                row.write(1, rng.random() * 100000, money)
                row.write(2, rng.random(), percent)
                row.write(3, datetime.date(2000, 1, 1) + datetime.timedelta(days=rng.randrange(9000)), date)
                row.write(4, words[rng.randrange(20)])
                row.write(5, f"{words[rng.randrange(20)]} {words[rng.randrange(20)]} {r}")
                row.write(6, rng.randrange(1000000))
                row.write(7, rng.random() < 0.5)
                row.write(8, words[rng.randrange(20)] + " " + words[rng.randrange(20)])
                row.write(9, rng.random() * 1e-3)
                if r % 1000 == 999:
                    sheet.flush_row_data()
        book.save(path)
    out["large.xls"] = {"bytes": os.path.getsize(path), "written by": "xlwt",
                        "content": "3 sheets of 65,536 rows by 10 columns: 1,966,080 cells"}
    for name, source, target, content in (
            ("large.doc", "large.docx", "doc:MS Word 97", "go-fast-ooxml's large.docx (1.5 million words)"),
            ("large.ppt", "large.pptx", "ppt:MS PowerPoint 97", "go-fast-ooxml's large.pptx (1,500 slides)")):
        path = os.path.join(GENERATED, name)
        src = os.path.join(OOXML_GENERATED, source)
        if not os.path.exists(path):
            if not os.path.exists(src):
                print(f"{name}: {source} is not there; skipped")
                continue
            with tempfile.TemporaryDirectory(prefix="gofastmsbin-gen-") as tmp:
                took, _, status = libreoffice(src, target, tmp, timeout=1800)
                made = os.path.join(tmp, name)
                if status != "ok" or not os.path.exists(made):
                    print(f"{name}: LibreOffice {status} after {took:.0f} s; skipped")
                    continue
                shutil.move(made, path)
        out[name] = {"bytes": os.path.getsize(path), "written by": "LibreOffice, from " + content}
    for name, v in out.items():
        print(name, v)
    save("generated", out)


# ------------------------------------------------------------------ tools

def read_xlrd(path):
    import xlrd
    book = xlrd.open_workbook(path)
    cells = 0
    for sheet in book.sheets():
        for r in range(sheet.nrows):
            cells += sum(1 for c in sheet.row_values(r) if c != "")
    return cells


def read_calamine(path):
    import python_calamine
    book = python_calamine.CalamineWorkbook.from_path(path)
    cells = 0
    for name in book.sheet_names:
        for row in book.get_sheet_by_name(name).to_python():
            cells += sum(1 for c in row if c != "")
    return cells


TOOLS = {"xlrd": read_xlrd, "calamine": read_calamine}


class TimedOut(Exception):
    pass


def alarm(signum, frame):
    raise TimedOut()


def corpus():
    entries = manifest()
    out = {"files": collections.Counter(e["format"] for e in entries)}
    signal.signal(signal.SIGALRM, alarm)
    xls = [e for e in entries if e["format"] == "xls"]
    for tool, read in TOOLS.items():
        read_ok = failed = timeouts = cells = 0
        seconds = 0.0
        errors = collections.Counter()
        for e in xls:
            start = time.perf_counter()
            signal.alarm(60)
            try:
                cells += read(os.path.join(TESTDATA, e["path"]))
                read_ok += 1
                seconds += time.perf_counter() - start
            except TimedOut:
                timeouts += 1
            except BaseException as err:  # calamine raises a Rust panic as BaseException
                if isinstance(err, KeyboardInterrupt):
                    raise
                failed += 1
                errors[type(err).__name__] += 1
            finally:
                signal.alarm(0)
        out[tool] = {"files": len(xls), "read": read_ok, "failed": failed, "over 60 s": timeouts,
                     "seconds for the files read": round(seconds, 2), "cells": cells,
                     "errors by type": dict(errors.most_common())}
        print(tool, out[tool], flush=True)
    # LibreOffice on a sample of each format: about a second a file.
    rng = random.Random(1)
    for fmt, target in LO_TARGET.items():
        sample = rng.sample(sorted((e for e in entries if e["format"] == fmt), key=lambda e: e["sha256"]), SAMPLE)
        counts, seconds = collections.Counter(), []
        for e in sample:
            with tempfile.TemporaryDirectory(prefix="gofastmsbin-out-") as tmp:
                took, _, status = libreoffice(os.path.join(TESTDATA, e["path"]), target, tmp, timeout=60)
            counts[status] += 1
            if status == "ok":
                seconds.append(took)
        seconds.sort()
        out["libreoffice " + fmt] = {"files": SAMPLE, "converted to": target, **counts,
                                     "median seconds a file": round(seconds[len(seconds) // 2], 2) if seconds else None,
                                     "megabytes": round(sum(e["size"] for e in sample) / 1e6, 1)}
        print("libreoffice", fmt, out["libreoffice " + fmt], flush=True)
    save("corpus", out)


def one(tool, path):
    """Read one file with one tool, in this process: for the large files."""
    print(TOOLS[tool](path))


def large():
    out = {}
    for name, fmt in (("large.xls", "xls"), ("large.doc", "doc"), ("large.ppt", "ppt")):
        path = os.path.join(GENERATED, name)
        if not os.path.exists(path):
            continue
        runs = {}
        if fmt == "xls":
            for tool in TOOLS:
                best = None
                for _ in range(3):
                    took, peak, status = run_contained([sys.executable, "-I", os.path.abspath(__file__), "one", tool, path], 900)
                    if status == "ok" and (best is None or took < best[0]):
                        best = (took, peak)
                runs[tool] = {"seconds": round(best[0], 2), "peak MB": round(best[1] / 1e6)} if best else {"status": "failed"}
        with tempfile.TemporaryDirectory(prefix="gofastmsbin-out-") as tmp:
            took, peak, status = libreoffice(path, LO_TARGET[fmt], tmp, timeout=1800)
        runs["libreoffice"] = {"seconds": round(took, 2), "peak MB": round(peak / 1e6), "status": status,
                               "converted to": LO_TARGET[fmt]}
        out[name] = runs
        print(name, runs, flush=True)
    save("large", out)


# ------------------------------------------------------------------ report

def report():
    r = load()
    lines = ["# Baselines", "",
             "What the tools in use today make of the binary Office formats, measured before this",
             "library reads any of them. Written by `bench/baseline.py report` from `bench/baseline.json`;",
             "the method is in the script. One machine (a 12-core Ryzen 9 9900X), shared with other work,",
             "so times are to be read as rough.", ""]
    if "generated" in r:
        lines += ["## The large files", "", "| File | Size | Content |", "|---|---|---|"]
        for name, v in sorted(r["generated"].items()):
            lines.append(f"| `{name}` | {v['bytes'] / 1e6:.1f} MB | {v.get('content', v['written by'])} |")
        lines.append("")
    if "large" in r:
        lines += ["| File | Tool | Time | Peak memory |", "|---|---|---|---|"]
        for name, runs in sorted(r["large"].items()):
            for tool, v in runs.items():
                if "seconds" not in v:
                    lines.append(f"| `{name}` | {tool} | failed | |")
                    continue
                what = tool + (f", to {v['converted to']}" if "converted to" in v else "")
                state = "" if v.get("status", "ok") == "ok" else f" ({v['status']})"
                lines.append(f"| `{name}` | {what} | {v['seconds']:.2f} s{state} | {v['peak MB']:,} MB |")
        lines += ["", "Each run is a process of its own, the time that of the whole process, the best of three",
                  "for the Python tools. LibreOffice's memory is that of its largest process.", ""]
    if "corpus" in r:
        c = r["corpus"]
        lines += ["## The corpus", "",
                  f"{sum(c['files'].values()):,} files: " + ", ".join(f"{n:,} `.{f}`" for f, n in sorted(c["files"].items())) + ".", "",
                  "| Tool | Files | Read | Failed | Over 60 s | Time for the files read |", "|---|---|---|---|---|---|"]
        for tool in TOOLS:
            v = c[tool]
            lines.append(f"| {tool} | {v['files']:,} `.xls` | {v['read']:,} | {v['failed']:,} | {v['over 60 s']:,} | {v['seconds for the files read']:.1f} s |")
        lines += ["", "A file counts as failed when the tool raises an error, whatever the reason: among the files",
                  "are damaged, encrypted and hostile ones, and ones that are not `.xls` files at all.", "",
                  f"LibreOffice, on {SAMPLE} files of each format drawn at random, a process for each file:", "",
                  "| Format | Converted to | Converted | Failed | Over 60 s | Median time a file |", "|---|---|---|---|---|---|"]
        for fmt in LO_TARGET:
            v = c["libreoffice " + fmt]
            lines.append(f"| `.{fmt}` | {v['converted to']} | {v.get('ok', 0)} | {v.get('failed', 0)} | {v.get('timeout', 0)} | {v['median seconds a file']} s |")
        lines.append("")
    with open(os.path.join(HERE, "baseline.md"), "w", encoding="utf-8") as f:
        f.write("\n".join(lines))
    print("\n".join(lines))


if __name__ == "__main__":
    step = sys.argv[1] if len(sys.argv) > 1 else ""
    if step == "one":
        one(sys.argv[2], sys.argv[3])
    elif step in ("generate", "corpus", "large", "report"):
        globals()[step]()
    else:
        sys.exit(__doc__)
