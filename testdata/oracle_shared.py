#!/usr/bin/env python3
"""What olefile and olevba read of each corpus file: the document properties
and the source of the VBA modules.

    python -I testdata/oracle_shared.py testdata testdata/corpus/oracle-shared.jsonl

Writes one JSON object a line, keyed by the file's hash. The Go test
TestSharedAgainstOracles compares the library with it. Each file is read in
a process of its own with a time limit of a minute: these tools are run on
damaged and hostile files, and some never return.
"""
import concurrent.futures
import json
import os
import subprocess
import sys

FIELDS = {
    "title": "title", "subject": "subject", "author": "author", "keywords": "keywords",
    "comments": "description", "template": "template", "last_saved_by": "last_modified_by",
    "revision_number": "revision", "creating_application": "application",
    "category": "category", "manager": "manager", "company": "company",
}
CODECS = {65001: "utf-8", 1200: "utf-16-le", 10000: "mac_roman", 932: "cp932", 936: "gbk", 949: "cp949", 950: "cp950"}


def text(value, codepage):
    if isinstance(value, bytes):
        codec = CODECS.get(codepage, "cp%d" % codepage if codepage else "cp1252")
        try:
            value = value.decode(codec, errors="replace")
        except LookupError:
            return None
    if not isinstance(value, str):
        return None
    return value.split("\x00")[0]


def read(path):
    import olefile
    out = {"meta": None, "vba": None}
    try:
        ole = olefile.OleFileIO(path)
    except Exception as e:  # not a compound file, or one olefile refuses
        out["error"] = type(e).__name__
        return out
    try:
        meta = ole.get_metadata()
        got = {}
        for field, key in FIELDS.items():
            cp = meta.codepage if field in olefile.OleMetadata.SUMMARY_ATTRIBS else meta.codepage_doc
            v = text(getattr(meta, field, None), (cp or 1252) & 0xFFFF)
            if v is not None and v.strip():
                got[key] = v
        for field, key in (("create_time", "created"), ("last_saved_time", "modified")):
            v = getattr(meta, field, None)
            if v is not None and hasattr(v, "isoformat") and v.year > 1601:
                got[key] = v.replace(microsecond=0).isoformat() + "Z"
        out["meta"] = got
    except Exception as e:
        out["meta_error"] = type(e).__name__
    try:
        from oletools.olevba import VBA_Parser
        parser = VBA_Parser(path)
        mods = []
        if parser.detect_vba_macros():
            for _, stream, name, code in parser.extract_macros():
                if isinstance(code, bytes):
                    code = code.decode("utf-8", errors="replace")
                mods.append({"stream": stream, "name": name, "code": code})
        out["vba"] = mods
        parser.close()
    except Exception as e:
        out["vba_error"] = type(e).__name__
    return out


def main():
    if sys.argv[1] == "--one":
        # One file, in this process: the parent bounds its time.
        print(json.dumps(read(sys.argv[2])))
        return
    testdata, dest = sys.argv[1], sys.argv[2]
    entries = json.load(open(os.path.join(testdata, "corpus.json")))
    done = set()
    if os.path.exists(dest):
        for line in open(dest):
            done.add(json.loads(line)["sha256"])
    todo = [e for e in entries if e["sha256"] not in done and os.path.exists(os.path.join(testdata, e["path"]))]

    def one(e):
        try:
            out = subprocess.run([sys.executable, "-I", os.path.abspath(__file__), "--one", os.path.join(testdata, e["path"])],
                                 capture_output=True, timeout=60)
            res = json.loads(out.stdout.decode().strip().splitlines()[-1])
        except subprocess.TimeoutExpired:
            res = {"error": "timeout"}
        except Exception as ex:
            res = {"error": type(ex).__name__}
        res["sha256"] = e["sha256"]
        return res

    with open(dest, "a") as f, concurrent.futures.ThreadPoolExecutor(12) as pool:
        for res in pool.map(one, todo):
            f.write(json.dumps(res) + "\n")
            f.flush()


if __name__ == "__main__":
    main()
