#!/usr/bin/env python3
"""D-12: every user-facing string exists in German.

Extracts every t(...) key from ui/src/**/*.svelte and compares it with the catalogue in
ui/src/lib/i18n.svelte.ts. Exits non-zero and names the keys when one is missing.

    python3 scripts/i18n-check.py

The lookahead in the t( pattern matters: without it, get(...) and post(...) match too, and every
route string in the UI looks like an untranslated key.
"""

import pathlib
import re
import sys

root = pathlib.Path(__file__).resolve().parent.parent / "ui" / "src"
cat = (root / "lib" / "i18n.svelte.ts").read_text()

# catalogue keys: a bare identifier, 'single quoted' or "double quoted", followed by ": {"
keys = set()
for m in re.finditer(
    r"""(?m)^\s{4}(?:'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)"|([A-Za-z][A-Za-z0-9_]*))\s*:\s*\{""",
    cat,
):
    k = m.group(1) or m.group(2) or m.group(3)
    keys.add(k.replace("\\'", "'").replace('\\"', '"'))

used = set()
for f in sorted(root.rglob("*.svelte")):
    src = f.read_text()
    for m in re.finditer(
        r"""(?<![A-Za-z0-9_$.])t\(\s*'((?:[^'\\]|\\.)*)'"""
        r"""|(?<![A-Za-z0-9_$.])t\(\s*"((?:[^"\\]|\\.)*)\"""",
        src,
    ):
        k = m.group(1) or m.group(2)
        used.add(k.replace("\\'", "'").replace('\\"', '"'))

missing = sorted(k for k in used if k not in keys)
print("catalogue keys: %d, t() keys used: %d" % (len(keys), len(used)))
if missing:
    print("MISSING a German translation (%d):" % len(missing))
    for k in missing:
        print("   ", k[:110])
    sys.exit(1)
print("every t() key has a catalogue entry (D-12)")
