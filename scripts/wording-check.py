#!/usr/bin/env python3
"""Task 160: user-facing text says "the system" (de: "das System"), never "the box".
B-23: the German text addresses the user as "Sie", never with "du" (du, dein, dich, dir, euch, and
the du-imperatives such as "gib", "wähle", "bestätige" not followed by "Sie").

Checks four places for the word box/Box as its own word:

  - every t('...') / t("...") call argument across ui/src/**/*.svelte and *.ts
  - the i18n catalogue in ui/src/lib/i18n.svelte.ts (both the English key and the German
    "de:" value of every entry)
  - the starting page: ui/src/lib/bootbar.ts's own small {en, de} catalogue (bundled into
    it by ui/scripts/starting-page.mjs), ui/src/starting/template.html and the generated
    deploy/lighttpd/occulite-starting.html
  - occulited's own README.md

The du-check runs over the German strings only: the "de:" values of the i18n catalogue and of
bootbar.ts's catalogue. A {placeholder} such as {dir} is not a word of the text.

Code comments, test names and CSS (box-shadow, box-sizing, border-box, and other
`box-...`/`...-box` declarations) are not user-facing and are not in scope; a hyphen breaks the
\\b boundary that would otherwise make e.g. "box-shadow" match, so those are filtered explicitly.

    python3 scripts/wording-check.py
"""

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
UI_SRC = ROOT / "ui" / "src"

BOX_RE = re.compile(r"\bbox\b", re.IGNORECASE)
# CSS declarations/properties/selectors that legitimately contain "box": box-shadow, box-sizing,
# border-box, box-decoration-break and the like, a bare `.box` class selector or `class="box"` /
# `class="ol-bootresume-box"` attribute, none of it prose a user reads.
CSS_BOX_RE = re.compile(
    r"[a-z-]*-box\b|\bbox-[a-z-]+|\.box\b|class=\"[a-z0-9 -]*\bbox\b[a-z0-9 -]*\"",
    re.IGNORECASE,
)

# B-23: the informal forms. The pronouns are words of their own; the imperatives are the du-forms
# of the verbs a UI text asks with, and count only when "Sie" does not follow (the formal
# imperative is "Geben Sie", "Wählen Sie"). Words that are also nouns (Suche, Frage, Bitte, Stelle)
# are left out, and a status such as "Warten auf …" is written as an infinitive.
DU_PRONOUN_RE = re.compile(r"(?<![\w])(du|dein|deine|deinen|deinem|deiner|deines|dich|dir|euch|euer|eure|euren|eurem|eurer|eures)(?![\w])", re.IGNORECASE)
DU_IMPERATIVE_RE = re.compile(
    r"(?<![\w])(gib|nimm|wähle|prüfe|öffne|bestätige|vergleiche|trage|klicke|tippe|lies|sieh|gehe|lege|setze|starte|melde|"
    r"versuche|entferne|ändere|speichere|kopiere|drücke|halte|schalte|warte|verbinde|wechsle|verwende|nutze|achte|"
    r"schließe|lade|erstelle|füge|behalte|bewahre|notiere|entschlüssele|richte|hole)(?![\w])(?!\s+Sie\b)",
    re.IGNORECASE,
)
PLACEHOLDER_RE = re.compile(r"\{[A-Za-z_][A-Za-z0-9_]*\}")


def has_du_form(text):
    """True if the German `text` says du where the UI says Sie (B-23)."""
    stripped = PLACEHOLDER_RE.sub("", text)
    return bool(DU_PRONOUN_RE.search(stripped) or DU_IMPERATIVE_RE.search(stripped))


def has_bare_box(text):
    """True if `text` contains box/Box as its own word, outside a CSS box-* declaration."""
    stripped = CSS_BOX_RE.sub("", text)
    return bool(BOX_RE.search(stripped))


def scan_t_calls():
    violations = []
    for f in sorted(UI_SRC.rglob("*.svelte")) + sorted(UI_SRC.rglob("*.ts")):
        src = f.read_text(encoding="utf-8")
        for m in re.finditer(
            r"""(?<![A-Za-z0-9_$.])t\(\s*'((?:[^'\\]|\\.)*)'"""
            r"""|(?<![A-Za-z0-9_$.])t\(\s*"((?:[^"\\]|\\.)*)\""""
            r"""|(?<![A-Za-z0-9_$.])t\(\s*`((?:[^`\\]|\\.)*)`""",
            src,
        ):
            key = m.group(1) or m.group(2) or m.group(3)
            key = key.replace("\\'", "'").replace('\\"', '"')
            if has_bare_box(key):
                line = src.count("\n", 0, m.start()) + 1
                violations.append(f"{f.relative_to(ROOT)}:{line}: t({key[:100]!r})")
    return violations


def scan_i18n_table():
    violations = []
    path = UI_SRC / "lib" / "i18n.svelte.ts"
    src = path.read_text(encoding="utf-8")
    # entries: 'key' or "key" or bareKey, then a {de: '...'} or {\n de: '...' \n} block, possibly
    # with an en: field too (a handful of entries carry an explicit English override).
    for m in re.finditer(
        r"""(?m)^\s{4}(?:'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)"|([A-Za-z][A-Za-z0-9_]*))\s*:\s*\{""",
        src,
    ):
        key = m.group(1) or m.group(2) or m.group(3)
        key = key.replace("\\'", "'").replace('\\"', '"')
        block_start = m.end()
        # find this entry's closing brace: either on the same line ("...'},") or a following one
        same_line_end = src.find("},", block_start)
        newline_before_close = src.find("\n    },", block_start)
        close = same_line_end
        if newline_before_close != -1 and (close == -1 or newline_before_close < close):
            close = newline_before_close
        block = src[block_start:close] if close != -1 else src[block_start:block_start + 400]
        de_m = re.search(r"""de:\s*(?:'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)")""", block)
        de_val = ""
        if de_m:
            de_val = (de_m.group(1) or de_m.group(2)).replace("\\'", "'").replace('\\"', '"')
        line = src.count("\n", 0, m.start()) + 1
        if has_bare_box(key):
            violations.append(f"{path.relative_to(ROOT)}:{line}: key {key[:100]!r}")
        if de_val and has_bare_box(de_val):
            violations.append(f"{path.relative_to(ROOT)}:{line}: de {de_val[:100]!r}")
        if de_val and has_du_form(de_val):
            violations.append(f"{path.relative_to(ROOT)}:{line}: de says du, not Sie (B-23): {de_val[:100]!r}")
    return violations


def scan_bootbar_catalogue():
    violations = []
    path = UI_SRC / "lib" / "bootbar.ts"
    src = path.read_text(encoding="utf-8")
    for m in re.finditer(r"""en:\s*'((?:[^'\\]|\\.)*)'""", src):
        val = m.group(1).replace("\\'", "'")
        if has_bare_box(val):
            line = src.count("\n", 0, m.start()) + 1
            violations.append(f"{path.relative_to(ROOT)}:{line}: en {val[:100]!r}")
    for m in re.finditer(r"""de:\s*'((?:[^'\\]|\\.)*)'""", src):
        val = m.group(1).replace("\\'", "'")
        if has_bare_box(val):
            line = src.count("\n", 0, m.start()) + 1
            violations.append(f"{path.relative_to(ROOT)}:{line}: de {val[:100]!r}")
        if has_du_form(val):
            line = src.count("\n", 0, m.start()) + 1
            violations.append(f"{path.relative_to(ROOT)}:{line}: de says du, not Sie (B-23): {val[:100]!r}")
    return violations


def scan_plain_text_file(path):
    """Line-by-line box/Box check for a file with no quoting convention of its own (the
    starting page's static HTML, a README). An HTML comment (the starting page's own build
    note, naming placeholders such as "the box's hostname") is developer documentation, not
    something a user reads, so lines inside <!-- --> are skipped."""
    violations = []
    if not path.exists():
        return violations
    in_comment = False
    for i, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        text = line
        if in_comment:
            end = text.find("-->")
            if end == -1:
                continue
            text = text[end + 3:]
            in_comment = False
        start = text.find("<!--")
        if start != -1:
            end = text.find("-->", start)
            if end == -1:
                text = text[:start]
                in_comment = True
            else:
                text = text[:start] + text[end + 3:]
        if has_bare_box(text):
            violations.append(f"{path.relative_to(ROOT)}:{i}: {line.strip()[:140]}")
    return violations


def main():
    violations = []
    violations += scan_t_calls()
    violations += scan_i18n_table()
    violations += scan_bootbar_catalogue()
    violations += scan_plain_text_file(UI_SRC / "starting" / "template.html")
    violations += scan_plain_text_file(ROOT / "deploy" / "lighttpd" / "occulite-starting.html")
    violations += scan_plain_text_file(ROOT / "README.md")

    if violations:
        print("user-facing text says \"the box\" (task 160: it must say \"the system\") or \"du\" (B-23: it must say \"Sie\"):")
        for v in violations:
            print("   ", v)
        print(len(violations), "violation(s)")
        sys.exit(1)
    print("no user-facing \"box\" wording found (task 160), no \"du\" in German text (B-23)")


if __name__ == "__main__":
    main()
