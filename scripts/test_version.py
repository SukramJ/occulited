#!/usr/bin/env python3
"""Tests for scripts/version.sh: occulited's version is its commit; the image's v* tags are not (occulited task 16).

    python3 scripts/test_version.py
"""

import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).resolve().parent / "version.sh"


def git(repo, *args):
    env = dict(os.environ, GIT_AUTHOR_NAME="t", GIT_AUTHOR_EMAIL="t@example.org", GIT_COMMITTER_NAME="t",
               GIT_COMMITTER_EMAIL="t@example.org", GIT_CONFIG_GLOBAL="/dev/null", GIT_CONFIG_NOSYSTEM="1")
    return subprocess.run(["git", "-C", str(repo), *args], check=True, capture_output=True, text=True, env=env).stdout.strip()


class VersionTest(unittest.TestCase):
    def setUp(self):
        self.dir = pathlib.Path(tempfile.mkdtemp())
        (self.dir / "scripts").mkdir()
        shutil.copy(SCRIPT, self.dir / "scripts" / "version.sh")
        (self.dir / "f").write_text("1\n")

    def tearDown(self):
        shutil.rmtree(self.dir)

    def run_script(self):
        out = subprocess.run(["sh", str(self.dir / "scripts" / "version.sh")], check=True, capture_output=True, text=True)
        return out.stdout.split("\n")[0].split(" ")

    def commit(self, text):
        (self.dir / "f").write_text(text)
        git(self.dir, "add", "-A")
        git(self.dir, "commit", "-qm", text)
        return git(self.dir, "rev-parse", "HEAD")

    def test_outside_git_is_dev_without_commit(self):
        self.assertEqual(self.run_script(), ["dev", ""])

    def test_versions(self):
        git(self.dir, "init", "-q")
        first = self.commit("1\n")
        self.assertEqual(self.run_script(), [first, first], "the commit")
        (self.dir / "f").write_text("changed\n")
        self.assertEqual(self.run_script(), [first + "-dirty", first], "an uncommitted tree")
        git(self.dir, "checkout", "-q", "--", "f")

        git(self.dir, "tag", "-a", "-m", "occulited 1.0.0-dev.40", "v1.0.0-dev.40")
        self.assertEqual(self.run_script(), [first, first], "a round's tag names the commit, it is not the version")

        head = self.commit("2\n")
        self.assertNotEqual(first, head)
        self.assertEqual(self.run_script(), [head, head], "a commit after the tag")
        (self.dir / "f").write_text("changed\n")
        self.assertEqual(self.run_script(), [head + "-dirty", head], "a hot build")


if __name__ == "__main__":
    unittest.main()
