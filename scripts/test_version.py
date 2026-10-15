#!/usr/bin/env python3
"""Exercise build labels with real, isolated Git checkouts and source archives."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]


class VersionTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="slakt version test ")
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        (self.root / "scripts").mkdir()
        (self.root / "cmd/slkt").mkdir(parents=True)
        shutil.copy(ROOT / "scripts/version.sh", self.root / "scripts/version.sh")
        (self.root / "cmd/slkt/VERSION").write_text("1.2.3\n")
        self.env = dict(os.environ)
        self.env.pop("VERSION", None)
        self.env.pop("GIT_DIR", None)
        self.env.pop("GIT_WORK_TREE", None)

    def label(self, **env):
        return subprocess.check_output(["bash", str(self.root / "scripts/version.sh")],
                                       cwd="/tmp", env=dict(self.env, **env), text=True).strip()

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.root), *args],
                                       env=self.env, text=True, stderr=subprocess.PIPE).strip()

    def test_archive_and_override(self):
        self.assertEqual(self.label(), "1.2.3-dev")
        self.assertEqual(self.label(VERSION="2.0.0-rc.1"), "2.0.0-rc.1")

    def test_checkout_tracks_commits_and_local_changes(self):
        self.git("init")
        self.git("add", ".")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com",
                 "-c", "commit.gpgsign=false", "commit", "-m", "Initial version")
        revision = self.git("rev-parse", "--short=12", "HEAD")
        self.assertEqual(self.label(), "1.2.3-dev+" + revision)
        (self.root / "cmd/slkt/VERSION").write_text("1.2.4\n")
        self.assertEqual(self.label(), "1.2.4-dev+" + revision + ".dirty")
        self.assertEqual(self.label(VERSION="1.2.4"), "1.2.4")
        self.git("add", ".")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com",
                 "-c", "commit.gpgsign=false", "commit", "-m", "Next release")
        updated = self.git("rev-parse", "--short=12", "HEAD")
        self.assertNotEqual(updated, revision)
        self.assertEqual(self.label(), "1.2.4-dev+" + updated)


if __name__ == "__main__":
    unittest.main()
