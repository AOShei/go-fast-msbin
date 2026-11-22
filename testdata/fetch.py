#!/usr/bin/env python3
"""Download the test corpus of go-fast-msbin.

The corpus is real Word, Excel and PowerPoint files in the binary formats of
Office 97 to 2003 (and older), from public sources. The files are not
committed; testdata/corpus.json lists each one by URL and SHA-256, and this
script downloads them into testdata/corpus/.

  python3 testdata/fetch.py            download what the manifest lists
  python3 testdata/fetch.py --rebuild  query the sources again and rewrite
                                       the manifest (changes what is tested;
                                       review the diff)

Standard library only. Sources:

  poi          The test files of Apache POI (Apache-2.0), at one commit. Many
               were attached to bug reports by users of Microsoft Office, and
               some are deliberately damaged, encrypted or hostile.
  tika         The test documents of Apache Tika (Apache-2.0), at one commit.
  libreoffice  The import test files of LibreOffice's Writer, Calc and
               Impress (MPL-2.0), at one commit. Its files that exploit old
               vulnerabilities (CVE-...) are kept scrambled there, with RC4
               under the key "CVE", so that virus scanners leave the
               repository alone. They are stored here unscrambled, marked
               "unscrambled" in the manifest, whose hash is of the file as
               stored.
  govuk        Files published on data.gov.uk under the Open Government
               Licence: what public bodies actually wrote with Office.
  govdocs      Govdocs1 (digitalcorpora.org): files collected from the web
               sites of the United States government and distributed for
               research without restriction. Ordinary documents, most of
               them written with Office 97 to 2003. They come in zip
               archives of a thousand files of every kind; only the members
               wanted are read from an archive, by byte range.

Every file belongs to one of two sets, decided by its hash when the manifest
is made: "dev", which development may look at, and "heldout" (one in five),
which is only ever scored. Nobody opens a held-out file to see why it scores
as it does.

A file that is no longer available, or whose content has changed, is reported
and skipped; tests skip corpus files that are absent.
"""
import concurrent.futures
import hashlib
import io
import json
import os
import sys
import threading
import urllib.parse
import urllib.request
import zipfile
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
MANIFEST = os.path.join(HERE, "corpus.json")
USER_AGENT = "corpus-fetch/0.1"
MAX_BYTES = 25 * 1024 * 1024

FORMATS = {
    "doc": "doc", "dot": "doc",
    "xls": "xls", "xlt": "xls", "xla": "xls",
    "ppt": "ppt", "pps": "ppt", "pot": "ppt",
}

# (name, repository, commit, licence, directories). The commits of POI and
# Tika are the ones go-fast-ooxml's corpus and soak set are pinned to.
GITHUB = [
    ("poi", "apache/poi", "f66927954a29cf1f5b86614fdf1816e6321c8aea", "Apache-2.0 (Apache POI test data)",
     ["test-data"]),
    ("tika", "apache/tika", "6ef8f501b1610fcb36d275332d2337e3113de74d", "Apache-2.0 (Apache Tika test documents)",
     [""]),
    ("libreoffice", "LibreOffice/core", "970e4f2194616485e9585b370f965cb61a34e096", "MPL-2.0 (LibreOffice import tests)",
     ["sw/qa", "sc/qa", "sd/qa"]),
]
GOVUK_API = "https://data.gov.uk/api/action/package_search"
GOVUK_WANT = {"DOC": 100, "XLS": 150, "PPT": 60}
GOVDOCS_URL = "https://digitalcorpora.s3.amazonaws.com/corpora/files/govdocs1/zipfiles/{:03d}.zip"
GOVDOCS_THREADS = range(0, 4)
GOVDOCS_LICENCE = "Govdocs1 (digitalcorpora.org): from United States government web sites, distributed for research without restriction"
COMPOUND = b"\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"


def get(url, limit=MAX_BYTES):
    headers = {"User-Agent": USER_AGENT}
    token = os.environ.get("GITHUB_TOKEN")
    if token and url.startswith("https://api.github.com/"):
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(url, headers=headers)
    with urllib.request.urlopen(req, timeout=60) as resp:
        data = resp.read(limit + 1)
    if len(data) > limit:
        raise ValueError(f"larger than {limit} bytes")
    return data


def get_range(url, start, length):
    req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT, "Range": f"bytes={start}-{start + length - 1}"})
    with urllib.request.urlopen(req, timeout=120) as resp:
        if resp.status != 206:
            raise ValueError("the server does not answer with a byte range")
        return resp.read()


class Remote(io.RawIOBase):
    """A file at a URL, read by byte range: enough for zipfile to read an
    archive's directory without the archive."""

    def __init__(self, url):
        self.url, self.pos = url, 0
        req = urllib.request.Request(url, method="HEAD", headers={"User-Agent": USER_AGENT})
        with urllib.request.urlopen(req, timeout=60) as resp:
            self.size = int(resp.headers["Content-Length"])

    def seekable(self):
        return True

    def readable(self):
        return True

    def tell(self):
        return self.pos

    def seek(self, offset, whence=0):
        self.pos = offset + (0, self.pos, self.size)[whence]
        return self.pos

    def readinto(self, buf):
        n = min(len(buf), self.size - self.pos)
        if n <= 0:
            return 0
        data = get_range(self.url, self.pos, n)
        buf[:len(data)] = data
        self.pos += len(data)
        return len(data)


def member(url, info):
    """One member of a remote zip archive, from what the archive's directory
    says of it."""
    if info.file_size > MAX_BYTES or info.compress_type not in (zipfile.ZIP_STORED, zipfile.ZIP_DEFLATED):
        raise ValueError("too large, or compressed in a way that is not read")
    head = get_range(url, info.header_offset, 30)
    if head[:4] != b"PK\x03\x04":
        raise ValueError("no local header where the directory says")
    skip = 30 + int.from_bytes(head[26:28], "little") + int.from_bytes(head[28:30], "little")
    raw = get_range(url, info.header_offset + skip, info.compress_size) if info.compress_size else b""
    data = raw if info.compress_type == zipfile.ZIP_STORED else zlib.decompressobj(-15).decompress(raw, MAX_BYTES + 1)
    if len(data) != info.file_size or zlib.crc32(data) != info.CRC:
        raise ValueError("the member is not what the directory says")
    return data


def get_json(url):
    return json.loads(get(url, limit=256 * 1024 * 1024))


def rc4(key, data):
    s, j = list(range(256)), 0
    for i in range(256):
        j = (j + s[i] + key[i % len(key)]) & 255
        s[i], s[j] = s[j], s[i]
    out, i, j = bytearray(len(data)), 0, 0
    for n, b in enumerate(data):
        i = (i + 1) & 255
        j = (j + s[i]) & 255
        s[i], s[j] = s[j], s[i]
        out[n] = b ^ s[(s[i] + s[j]) & 255]
    return bytes(out)


def unscramble(data):
    """A file LibreOffice keeps scrambled, as it is; any other, None."""
    if rc4(b"CVE", data[:8]) == COMPOUND:
        return rc4(b"CVE", data)
    return None


def which_set(digest):
    """One file in five is held out, by its hash."""
    return "heldout" if int(digest[:8], 16) % 5 == 0 else "dev"


def entry(name, path, url, data, licence):
    ext = path.rsplit(".", 1)[-1].lower()
    digest = hashlib.sha256(data).hexdigest()
    return {"name": name, "format": FORMATS[ext], "set": which_set(digest), "path": path, "url": url,
            "sha256": digest, "size": len(data), "licence": licence}


def store(path, data):
    full = os.path.join(HERE, path)
    os.makedirs(os.path.dirname(full), exist_ok=True)
    with open(full, "wb") as f:
        f.write(data)


def parallel(fn, items, workers=8):
    with concurrent.futures.ThreadPoolExecutor(workers) as pool:
        return list(pool.map(fn, items))


# ---------------------------------------------------------------------- build

def subtree(repo, commit, directory):
    """The SHA of the tree of a directory at a commit."""
    sha = commit
    for part in [p for p in directory.split("/") if p]:
        tree = get_json(f"https://api.github.com/repos/{repo}/git/trees/{sha}")
        sha = next(t["sha"] for t in tree["tree"] if t["path"] == part and t["type"] == "tree")
    return sha


def build_github(name, repo, commit, licence, directories):
    wanted = []
    for directory in directories:
        tree = get_json(f"https://api.github.com/repos/{repo}/git/trees/{subtree(repo, commit, directory)}?recursive=1")
        if tree.get("truncated"):
            sys.exit(f"{name}: the listing of {directory or '/'} was truncated; narrow the directories")
        prefix = directory + "/" if directory else ""
        wanted += [prefix + t["path"] for t in tree["tree"]
                   if t["type"] == "blob" and t["path"].rsplit(".", 1)[-1].lower() in FORMATS
                   and 0 < t.get("size", 0) <= MAX_BYTES]
    print(f"{name}: commit {commit[:12]}, {len(wanted)} files", flush=True)
    licence = f"{licence}, commit {commit[:12]}"

    def one(p):
        url = f"https://raw.githubusercontent.com/{repo}/{commit}/" + urllib.parse.quote(p)
        try:
            data = get(url)
        except Exception as e:
            print(f"  skipped {p}: {e}", file=sys.stderr)
            return None
        path = f"corpus/{name}/" + p
        plain = unscramble(data) if name == "libreoffice" else None
        e = entry(f"{name}/" + p, path, url, plain or data, licence)
        if plain:
            e["unscrambled"] = True
        store(path, plain or data)
        return e

    return [e for e in parallel(one, wanted) if e]


def build_govuk():
    out = []
    for fmt, want in GOVUK_WANT.items():
        candidates, seen, start = [], set(), 0
        while len(candidates) < want * 3 and start < 2000:
            page = get_json(f"{GOVUK_API}?fq=res_format:{fmt}&rows=100&start={start}")["result"]["results"]
            if not page:
                break
            start += 100
            for pkg in page:
                if pkg.get("license_id") != "uk-ogl":
                    continue
                publisher = (pkg.get("organization") or {}).get("title", "")
                per_dataset = 0
                for res in pkg.get("resources", []):
                    url = res.get("url", "")
                    ext = urllib.parse.urlparse(url).path.rsplit(".", 1)[-1].lower()
                    if (res.get("format") or "").upper() != fmt or ext not in FORMATS or url in seen:
                        continue
                    seen.add(url)
                    candidates.append((url, ext, publisher))
                    per_dataset += 1
                    if per_dataset == 2:  # spread across publishers and datasets
                        break

        def one(c):
            url, ext, publisher = c
            try:
                data = get(url)
            except Exception:
                return None
            if data[:8] != COMPOUND:
                return None  # an error page, or a file of another format under this name
            digest = hashlib.sha256(data).hexdigest()
            path = f"corpus/govuk/{digest[:16]}.{ext}"
            store(path, data)
            licence = "Open Government Licence v3.0 (data.gov.uk" + (f"; publisher: {publisher}" if publisher else "") + ")"
            return entry(f"govuk/{digest[:16]}.{ext}", path, url, data, licence)

        got, hashes = [], set()
        for e in parallel(one, candidates):
            if e and e["sha256"] not in hashes and len(got) < want:
                hashes.add(e["sha256"])
                got.append(e)
        print(f"govuk: {fmt}: {len(got)} files from {len(candidates)} candidates", flush=True)
        out += got
    return out


def build_govdocs():
    out = []
    for thread in GOVDOCS_THREADS:
        url = GOVDOCS_URL.format(thread)
        with zipfile.ZipFile(io.BufferedReader(Remote(url), 1 << 20)) as z:
            wanted = [i for i in z.infolist()
                      if i.filename.rsplit(".", 1)[-1].lower() in FORMATS and 0 < i.file_size <= MAX_BYTES]

        def one(info):
            try:
                data = member(url, info)
            except Exception as e:
                print(f"  skipped {info.filename}: {e}", file=sys.stderr)
                return None
            if data[:8] != COMPOUND:
                return None  # a file of another format under this name
            path = "corpus/govdocs/" + info.filename
            store(path, data)
            e = entry("govdocs/" + info.filename, path, url, data, GOVDOCS_LICENCE)
            e["member"] = info.filename
            return e

        got = [e for e in parallel(one, wanted) if e]
        print(f"govdocs: {url.rsplit('/', 1)[-1]}: {len(got)} files of {len(wanted)} named as these formats", flush=True)
        out += got
    return out


def rebuild():
    manifest = []
    for source in GITHUB:
        manifest += build_github(*source)
    manifest += build_govuk()
    manifest += build_govdocs()
    # The same file from two sources is kept once, under the first name.
    manifest.sort(key=lambda e: e["name"])
    seen, unique = set(), []
    for e in manifest:
        if e["sha256"] not in seen:
            seen.add(e["sha256"])
            unique.append(e)
    kept = {e["path"] for e in unique}
    for base, _, files in os.walk(os.path.join(HERE, "corpus")):
        for f in files:
            rel = os.path.relpath(os.path.join(base, f), HERE).replace(os.sep, "/")
            if rel not in kept:
                os.remove(os.path.join(base, f))
    with open(MANIFEST, "w", encoding="utf-8") as f:
        json.dump(unique, f, ensure_ascii=False, indent=1)
        f.write("\n")
    summary(unique)


# ---------------------------------------------------------------------- fetch

def fetch():
    with open(MANIFEST, encoding="utf-8") as f:
        manifest = json.load(f)
    directories, lock = {}, threading.Lock()

    def directory(url):
        """The members of a remote zip archive by name, read once."""
        with lock:
            if url not in directories:
                with zipfile.ZipFile(io.BufferedReader(Remote(url), 1 << 20)) as z:
                    directories[url] = {i.filename: i for i in z.infolist()}
            return directories[url]

    def one(e):
        full = os.path.join(HERE, e["path"])
        if os.path.exists(full):
            with open(full, "rb") as f:
                if hashlib.sha256(f.read()).hexdigest() == e["sha256"]:
                    return "present"
        try:
            if "member" in e:
                data = member(e["url"], directory(e["url"])[e["member"]])
            else:
                data = get(e["url"])
        except Exception as err:
            print(f"  unavailable: {e['name']}: {err}", file=sys.stderr)
            return "unavailable"
        if e.get("unscrambled"):
            data = unscramble(data) or data
        if hashlib.sha256(data).hexdigest() != e["sha256"]:
            print(f"  changed at the source, skipped: {e['name']}", file=sys.stderr)
            return "changed"
        store(e["path"], data)
        return "downloaded"

    results = parallel(one, manifest)
    for status in ("present", "downloaded", "unavailable", "changed"):
        print(f"{status}: {results.count(status)}")


def summary(manifest):
    by = {}
    for e in manifest:
        key = (e["name"].split("/")[0], e["format"])
        n, held, size = by.get(key, (0, 0, 0))
        by[key] = (n + 1, held + (e["set"] == "heldout"), size + e["size"])
    for (source, fmt), (n, held, size) in sorted(by.items()):
        print(f"{source:11} {fmt}: {n:5} files ({held:4} held out), {size / 1e6:7.1f} MB")
    print(f"total: {len(manifest)} files ({sum(e['set'] == 'heldout' for e in manifest)} held out), "
          f"{sum(e['size'] for e in manifest) / 1e6:.1f} MB")


if __name__ == "__main__":
    if "--rebuild" in sys.argv:
        rebuild()
    else:
        fetch()
