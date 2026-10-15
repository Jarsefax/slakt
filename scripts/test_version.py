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
        for name in ("version.sh", "source-fingerprint.sh", "check-version.sh", "prepare-update.sh"):
            shutil.copy(ROOT / "scripts" / name, self.root / "scripts" / name)
        (self.root / "cmd/slkt/VERSION").write_text("1.2.3\n")
        (self.root / "cmd/slkt/main.go").write_text("package main\nfunc main() {}\n")
        (self.root / "internal").mkdir()
        for name in ("Makefile", "install.sh", "go.mod", "go.sum"):
            (self.root / name).write_text("fixture\n")
        self.env = dict(os.environ)
        self.env.pop("VERSION", None)
        self.env.pop("GIT_DIR", None)
        self.env.pop("GIT_WORK_TREE", None)

    def label(self, **env):
        return subprocess.check_output(["bash", str(self.root / "scripts/version.sh")],
                                       cwd="/tmp", env=dict(self.env, **env), text=True).strip()

    def fingerprint(self):
        return subprocess.check_output(["bash", str(self.root / "scripts/source-fingerprint.sh")],
                                       cwd="/tmp", env=self.env, text=True).strip()

    def prepare(self, bump="patch"):
        subprocess.run(["bash", str(self.root / "scripts/prepare-update.sh"), bump],
                       env=self.env, check=True, capture_output=True, text=True)

    def check(self, **env):
        return subprocess.run(["bash", str(self.root / "scripts/check-version.sh")],
                              env=dict(self.env, **env), capture_output=True, text=True)

    def commit(self):
        self.git("add", ".")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com",
                 "-c", "commit.gpgsign=false", "commit", "-m", "Update")

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.root), *args],
                                       env=self.env, text=True, stderr=subprocess.PIPE).strip()

    def test_archive_and_override(self):
        self.assertEqual(self.label(), "1.2.3-dev+src." + self.fingerprint()[:12])
        self.assertEqual(self.label(VERSION="2.0.0-rc.1"), "2.0.0-rc.1")

    def test_checkout_tracks_commits_and_local_changes(self):
        self.git("init")
        self.git("add", ".")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com",
                 "-c", "commit.gpgsign=false", "commit", "-m", "Initial version")
        revision = self.git("rev-parse", "--short=12", "HEAD")
        fingerprint = self.fingerprint()[:12]
        self.assertEqual(self.label(), "1.2.3-dev+" + revision + ".src." + fingerprint)
        (self.root / "cmd/slkt/VERSION").write_text("1.2.4\n")
        self.assertEqual(self.label(), "1.2.4-dev+" + revision + ".dirty.src." + fingerprint)
        self.assertEqual(self.label(VERSION="1.2.4"), "1.2.4")
        self.git("add", ".")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com",
                 "-c", "commit.gpgsign=false", "commit", "-m", "Next release")
        updated = self.git("rev-parse", "--short=12", "HEAD")
        self.assertNotEqual(updated, revision)
        self.assertEqual(self.label(), "1.2.4-dev+" + updated + ".src." + fingerprint)

    def test_missing_record_blocks_delivery(self):
        result = self.check()
        self.assertEqual(result.returncode, 2)
        self.assertIn("no source record", result.stderr)

    def test_release_target_checks_before_building_or_packaging(self):
        shutil.copy(ROOT / "Makefile", self.root / "Makefile")
        self.prepare()
        (self.root / "internal/new.go").write_text("package internal\n")
        result = subprocess.run(["make", "-C", str(self.root), "release", "GO=must-not-run"],
                                env=self.env, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("source changed", result.stderr)
        self.assertFalse((self.root / "dist").exists())

    def test_default_make_only_builds_without_preparing_a_version(self):
        shutil.copy(ROOT / "Makefile", self.root / "Makefile")
        result = subprocess.run(["make", "-n", "-C", str(self.root)],
                                env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("go build", result.stdout)
        self.assertNotIn("prepare-update.sh", result.stdout)
        self.assertEqual((self.root / "cmd/slkt/VERSION").read_text(), "1.2.3\n")

    def test_preparation_and_source_archives(self):
        self.prepare("minor")
        self.assertEqual((self.root / "cmd/slkt/VERSION").read_text(), "1.3.0\n")
        self.assertEqual(self.check().returncode, 0)
        self.assertEqual(self.check(VERSION="1.3.0-custom").returncode, 0)
        self.assertNotEqual(self.check(VERSION="1.2.3").returncode, 0)
        self.prepare()
        self.assertEqual((self.root / "cmd/slkt/VERSION").read_text(), "1.3.1\n")
        self.assertEqual(self.check().returncode, 0)

    def test_changed_untracked_or_dependency_inputs_block_reused_version(self):
        self.prepare()
        for relative in ("cmd/slkt/main.go", "internal/new.go", "go.mod", "scripts/version.sh"):
            with self.subTest(path=relative):
                path = self.root / relative
                previous = path.read_bytes() if path.exists() else None
                path.write_bytes((previous or b"") + b"changed\n")
                self.assertNotEqual(self.check().returncode, 0)
                if previous is None:
                    path.unlink()
                else:
                    path.write_bytes(previous)
                self.assertEqual(self.check().returncode, 0)

    def test_docs_tests_and_location_do_not_change_source_identity(self):
        self.prepare()
        fingerprint = self.fingerprint()
        (self.root / "README.md").write_text("Updated docs\n")
        (self.root / "cmd/slkt/main_test.go").write_text("package main\n")
        self.assertEqual(self.fingerprint(), fingerprint)
        self.assertEqual(self.check().returncode, 0)
        destination = self.root.parent / (self.root.name + " archive")
        self.addCleanup(shutil.rmtree, destination)
        shutil.copytree(self.root, destination)
        archived = subprocess.check_output(["bash", str(destination / "scripts/source-fingerprint.sh")],
                                           cwd="/tmp", env=self.env, text=True).strip()
        self.assertEqual(archived, fingerprint)

    def test_committed_changes_without_bump_still_fail(self):
        self.git("init")
        self.prepare()
        self.commit()
        self.assertEqual(self.check().returncode, 0)
        (self.root / "cmd/slkt/main.go").write_text("package main\nfunc main() { println(1) }\n")
        self.commit()
        self.assertNotEqual(self.check().returncode, 0)

    def test_record_rewrites_are_rejected_even_after_commit(self):
        self.git("init")
        self.prepare()
        self.commit()
        (self.root / "cmd/slkt/main.go").write_text("package main\nfunc main() { println(1) }\n")
        (self.root / "releases/1.2.4.sha256").write_text(self.fingerprint() + "\n")
        self.commit()
        result = self.check()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("immutable", result.stderr)

    def test_preparation_does_not_overwrite_an_existing_record(self):
        self.prepare()
        record = self.root / "releases/1.2.4.sha256"
        previous = record.read_bytes()
        (self.root / "cmd/slkt/VERSION").write_text("1.2.3\n")
        result = subprocess.run(["bash", str(self.root / "scripts/prepare-update.sh"), "patch"],
                                env=self.env, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(record.read_bytes(), previous)

    def test_version_regressions_are_rejected(self):
        self.git("init")
        self.prepare()
        self.commit()
        (self.root / "cmd/slkt/VERSION").write_text("1.2.3\n")
        (self.root / "releases/1.2.3.sha256").write_text(self.fingerprint() + "\n")
        result = self.check()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("must increase", result.stderr)


if __name__ == "__main__":
    unittest.main()
