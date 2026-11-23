#!/usr/bin/env python3
"""Opens Office files with a password through LibreOffice and prints the
text of each, as JSON: {"name": text or null}. null is a file LibreOffice
did not open (a wrong password among the reasons).

    python3 testdata/oracle_crypt.py PROFILE_DIR FILE=PASSWORD ...

LibreOffice is run contained: a profile of its own, a session of its own,
a bound on the size of any file it writes, a time limit, and its whole
process group killed at the end, whatever happens.
"""
import json
import os
import resource
import signal
import subprocess
import sys
import time

import uno
from com.sun.star.beans import PropertyValue

MAX_FILE = 64 << 20
TIME_LIMIT = 120


def limits():
    resource.setrlimit(resource.RLIMIT_FSIZE, (MAX_FILE, MAX_FILE))


def prop(name, value):
    p = PropertyValue()
    p.Name, p.Value = name, value
    return p


def main():
    profile, specs = sys.argv[1], sys.argv[2:]
    pipe = "gofastmsbincrypt%d" % os.getpid()
    proc = subprocess.Popen(
        ["soffice", "-env:UserInstallation=file://" + profile, "--headless", "--norestore", "--nologo",
         "--accept=pipe,name=%s;urp;" % pipe],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True, preexec_fn=limits)

    def stop(*_):
        raise TimeoutError("LibreOffice took too long")

    signal.signal(signal.SIGALRM, stop)
    signal.signal(signal.SIGTERM, stop)
    signal.alarm(TIME_LIMIT)
    out = {}
    try:
        local = uno.getComponentContext()
        resolver = local.ServiceManager.createInstanceWithContext("com.sun.star.bridge.UnoUrlResolver", local)
        ctx, deadline = None, time.time() + 60
        while ctx is None and time.time() < deadline:
            try:
                ctx = resolver.resolve("uno:pipe,name=%s;urp;StarOffice.ComponentContext" % pipe)
            except Exception:
                time.sleep(0.5)
        if ctx is None:
            sys.exit("LibreOffice did not start")
        desktop = ctx.ServiceManager.createInstanceWithContext("com.sun.star.frame.Desktop", ctx)
        for spec in specs:
            path, _, password = spec.partition("=")
            props = [prop("Hidden", True), prop("ReadOnly", True)]
            if password:
                props.append(prop("Password", password))
            name = os.path.basename(path) + "=" + password
            out[name] = None
            try:
                doc = desktop.loadComponentFromURL("file://" + os.path.abspath(path), "_blank", 0, tuple(props))
            except Exception:
                continue
            if doc is None:
                continue
            try:
                out[name] = doc.Text.String
            finally:
                doc.close(True)
    finally:
        signal.alarm(0)
        try:
            os.killpg(proc.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    json.dump(out, sys.stdout)


if __name__ == "__main__":
    main()
