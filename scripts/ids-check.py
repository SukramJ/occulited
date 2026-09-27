#!/usr/bin/env python3
"""No real device identifiers in the repository (B-24).

occulited is public, and its tests, fixtures, the UI stub and the docs need device identifiers
that look real enough for the parsers. They must be invented: a real SGTIN, serial or MAC names a
device somebody owns. This check finds every value of the shapes below in the tracked files and
fails on any that is not listed in scripts/ids-allow.txt, the list of the invented ones.

  - SGTINs: 24 hex digits beginning with 30 (3014F711A0…, 30150377DC…), also in groups of four
    as the UI writes them (3014-F711-A000-…), listed without the dashes
  - HmIP device addresses: 14 hex digits beginning with 00
  - BidCos serials: three letters ending in EQ, seven digits (JEQ0000001)
  - MAC addresses: six colon-separated hex pairs (a longer run such as a certificate
    fingerprint is not one)

A new test value goes into ids-allow.txt in the same commit, and it must be an invented one:
runs of zeros, counting digits, a letter pattern nobody reads as a real device's.

    python3 scripts/ids-check.py            # check the tracked files
    python3 scripts/ids-check.py --list     # print every value found, one per line, sorted
"""

import pathlib
import re
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
ALLOW = pathlib.Path(__file__).resolve().parent / "ids-allow.txt"

SHAPES = [
    ("SGTIN", re.compile(r"(?<![0-9A-Za-z])30[0-9A-Fa-f]{22}(?![0-9A-Za-z])")),
    ("SGTIN", re.compile(r"(?<![0-9A-Za-z-])30[0-9A-Fa-f]{2}(?:-[0-9A-Fa-f]{4}){5}(?![0-9A-Za-z-])")),
    ("HmIP address", re.compile(r"(?<![0-9A-Za-z])00[0-9A-Fa-f]{12}(?![0-9A-Za-z])")),
    ("BidCos serial", re.compile(r"(?<![0-9A-Za-z])[A-Z]EQ[0-9]{7}(?![0-9])")),
    ("MAC", re.compile(r"(?<![0-9A-Fa-f:])(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}(?![0-9A-Fa-f:])")),
]

# generated or third-party content: npm's lock files carry integrity hashes, nothing of ours
SKIP = re.compile(r"(^|/)package-lock\.json$")


def find(text):
    """Every (shape, value) in text, values upper-cased so the allow-list is case-free."""
    out = []
    for shape, rx in SHAPES:
        for m in rx.finditer(text):
            out.append((shape, m.group(0).replace("-", "").upper() if shape == "SGTIN" else m.group(0).upper()))
    return out


def load_allow(path=ALLOW):
    allow = set()
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.split("#", 1)[0].strip()
        if line:
            allow.add(line.upper())
    return allow


def tracked():
    out = subprocess.run(["git", "-C", str(ROOT), "ls-files", "-z"], capture_output=True, check=True).stdout
    return [p for p in out.decode().split("\0") if p and not SKIP.search(p)]


def scan(files):
    """{value: (shape, [file:line, …])} over the files, which are read as UTF-8 where they can be."""
    seen = {}
    for f in files:
        p = ROOT / f
        try:
            text = p.read_text(encoding="utf-8")
        except (UnicodeDecodeError, IsADirectoryError, FileNotFoundError):
            continue
        for n, line in enumerate(text.splitlines(), 1):
            for shape, value in find(line):
                seen.setdefault(value, (shape, []))[1].append(f"{f}:{n}")
    return seen


def main(argv):
    seen = scan(tracked())
    if "--list" in argv:
        for value in sorted(seen):
            print(value)
        return 0
    allow = load_allow()
    bad = {v: s for v, s in seen.items() if v not in allow}
    for value, (shape, where) in sorted(bad.items()):
        print(f"{shape} {value} is not in scripts/ids-allow.txt: {', '.join(where[:5])}")
    if bad:
        print(
            f"\n{len(bad)} identifier(s) not on the allow-list. Real device identifiers do not belong in "
            "this repository; replace them with invented ones and list those in scripts/ids-allow.txt.",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
