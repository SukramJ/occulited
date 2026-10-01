#!/usr/bin/env python3
"""Tests for scripts/wording-check.py: the "box" words (task 160), the du-forms (B-23) and
"Netzschlüssel" (task 305).

    python3 scripts/test_wording_check.py
"""

import importlib.util
import pathlib
import unittest

_spec = importlib.util.spec_from_file_location(
    "wording_check", pathlib.Path(__file__).resolve().parent / "wording-check.py"
)
wc = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(wc)


class BoxTest(unittest.TestCase):
    def test_box(self):
        for text, want in [
            ("Reboot the box", True),
            ("Die Box startet neu", True),
            ("box-shadow: none", False),
            ("border-box", False),
            ("Reboot the system", False),
            ("a checkbox", False),
        ]:
            with self.subTest(text=text):
                self.assertEqual(wc.has_bare_box(text), want)


class DuTest(unittest.TestCase):
    def test_du_forms(self):
        # the welcome page's and the other strings B-23 found, as they were
        for text in [
            "Dieses System verbindet sich nur mit dem Internet, wenn du es darum bittest.",
            "Installieren bleibt deine Entscheidung.",
            "gib ihre Adresse ein: die Namen kommen jetzt herüber.",
            "Gib der Platine eine feste Adresse.",
            "Angewendet. Bestätige innerhalb von {s} Sekunden.",
            "Öffne die neue Adresse und bestätige von dort:",
            "ein Tippfehler sperrt dich so nicht aus",
            "Vergleiche seinen Fingerabdruck mit dem, den dein Anbieter anzeigt",
            "Du meldest dich noch einmal an.",
            "Warte auf Bestätigung",
            "Das gehört euch.",
        ]:
            with self.subTest(text=text):
                self.assertTrue(wc.has_du_form(text))

    def test_sie_forms(self):
        for text in [
            "Dieses System verbindet sich nur mit dem Internet, wenn Sie es darum bitten.",
            "Installieren bleibt Ihre Entscheidung.",
            "Geben Sie ihre Adresse ein.",
            "Angewendet. Bestätigen Sie innerhalb von {s} Sekunden.",
            "Wählen Sie das Modul; Sie brauchen es später.",
            # a placeholder is not a word of the text
            "Die Kopien gehen nach {dir} auf den USB-Stick.",
            # nouns that look like imperatives
            "Suche läuft …",
            "Bitte ändern Sie jetzt Ihr Passwort.",
            "an dieser Stelle",
            "Letzte Suche: {time}",
            # words that only contain a pronoun
            "{name} deinstallieren?",
            "Direktverknüpfungen",
            "Warten auf Bestätigung — noch {s} s",
        ]:
            with self.subTest(text=text):
                self.assertFalse(wc.has_du_form(text))


class NetzTest(unittest.TestCase):
    def test_netzschluessel(self):
        for text, want in [
            ("HmIP-Geräte teilen sich einen Netzschlüssel.", True),
            ("Der HmIP-Netzschlüssel", True),
            ("des Netzschlüssels", True),
            ("Netzschlüssel", True),
            ("HmIP-Geräte teilen sich einen Netzwerkschlüssel.", False),
            ("Der HmIP-Netzwerkschlüssel", False),
            ("Sicherheitsschlüssel des Netzes", False),
        ]:
            with self.subTest(text=text):
                self.assertEqual(wc.has_old_netz(text), want)


class TreeTest(unittest.TestCase):
    def test_the_tree_is_clean(self):
        self.assertEqual(wc.scan_i18n_table(), [])
        self.assertEqual(wc.scan_bootbar_catalogue(), [])
        self.assertEqual(wc.scan_t_calls(), [])


if __name__ == "__main__":
    unittest.main()
