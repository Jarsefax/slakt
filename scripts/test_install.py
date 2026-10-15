#!/usr/bin/env python3
"""Installer lifecycle tests with isolated fake engines; never use real images."""

import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time
import unittest


INSTALLER = Path(__file__).resolve().parents[1] / "install.sh"
IMAGE_ID = "sha256:" + "a" * 64

FAKE_TOOL = r'''#!/usr/bin/env python3
import io, json, os, pathlib, signal, sys, tarfile, time
root = pathlib.Path(os.environ["SLKT_TEST_ROOT"])
tool = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
with (root / "events").open("a") as log:
    log.write(json.dumps([tool, args]) + "\n")
state_path = root / "state.json"
state = json.loads(state_path.read_text())
def save():
    temporary = root / ("state." + str(os.getpid()))
    temporary.write_text(json.dumps(state))
    temporary.replace(state_path)
def artifact(version):
    return ("#!/bin/sh\nprintf '%s\\n' 'slkt version " + version + "'\n").encode()
def block(marker):
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    (root / marker).touch()
    while True: time.sleep(0.1)
if tool == "fake-go":
    if args[0] == "env":
        print("linux" if args[1] == "GOHOSTOS" else "amd64")
        sys.exit(0)
    target = pathlib.Path(args[args.index("-o") + 1])
    target.write_bytes(artifact(args[args.index("-ldflags") + 1].split("main.version=", 1)[1]))
    sys.exit(int(os.environ.get("SLKT_TEST_BUILD_EXIT", "0")))
if args[0] == "info":
    sys.exit(0 if os.environ.get("SLKT_TEST_" + tool.upper(), "1") == "1" else 1)
if args[:2] == ["image", "inspect"]:
    if not state["image"]: sys.exit(1)
    print(state["id"])
elif args[:2] == ["image", "ls"]:
    print("\n".join(state["before"]))
elif args[0] == "pull":
    state["image"] = True
    save()
    if os.environ.get("SLKT_TEST_BLOCK") == "pull": block("pulling")
    sys.exit(int(os.environ.get("SLKT_TEST_PULL_EXIT", "0")))
elif args[0] == "run":
    assert "--rm" in args and "--pull=never" in args
    assert "CGO_ENABLED=0" in args and "GOOS=linux" in args
    assert not any(a in args for a in ("-v", "--volume", "--mount", "--privileged"))
    source = sys.stdin.buffer.read()
    with tarfile.open(fileobj=io.BytesIO(source)) as archive:
        assert "go.mod" in archive.getnames() and "cmd/slkt/main.go" in archive.getnames()
        assert not any(n.startswith(".git") for n in archive.getnames())
    state["container"] = True
    state["owner"] = args[args.index("--label") + 1].split("=", 1)[1]
    if os.environ.get("SLKT_TEST_FOREIGN_OWNER"): state["owner"] = "another-run"
    state["name"] = args[args.index("--name") + 1]
    pathlib.Path(args[args.index("--cidfile") + 1]).write_text("c" * 64)
    save()
    if os.environ.get("SLKT_TEST_BLOCK") == "run": block("running")
    status = int(os.environ.get("SLKT_TEST_BUILD_EXIT", "0"))
    if status: sys.stdout.buffer.write(b"partial binary"); sys.exit(status)
    if os.environ.get("SLKT_TEST_BAD_BINARY"): sys.stdout.buffer.write(b"#!/bin/sh\nexit 9\n")
    else: sys.stdout.buffer.write(artifact(args[-1]))
    state["container"] = False  # --rm on normal completion
    save()
elif args[:2] == ["container", "inspect"]:
    if not state["container"]: sys.exit(1)
    print(state["owner"])
elif args[:2] == ["container", "rm"]:
    assert args[-1] == state["name"] and "--force" in args
    if os.environ.get("SLKT_TEST_CONTAINER_CLEANUP_FAIL"): sys.exit(125)
    state["container"] = False
    save()
elif args[:2] == ["image", "rm"]:
    assert "--force" not in args and "--no-prune" in args and args[-1] == state["id"]
    if os.environ.get("SLKT_TEST_IMAGE_CLEANUP_FAIL") or state["container"]: sys.exit(2)
    state["image"] = False
    save()
else:
    raise AssertionError("Unexpected engine command: " + repr(args))
'''


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="slakt install test ")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        tools = self.root / "tools"
        tools.mkdir()
        for name in ("podman", "docker", "fake-go"):
            path = tools / name
            path.write_text(FAKE_TOOL)
            path.chmod(0o755)
        self.env = dict(os.environ, PATH=str(tools) + os.pathsep + os.environ["PATH"],
                        GO="slakt-test-nonexistent-go", SLKT_TEST_ROOT=str(self.root), VERSION="installer-test")
        self.destination = self.root / "bin with spaces"
        self.destination.mkdir()
        self.binary = self.destination / "slkt"
        self.binary.write_text("old installed binary\n")
        self.state = {"image": False, "before": [], "id": IMAGE_ID, "container": False, "owner": "", "name": ""}
        self.save_state()

    def save_state(self):
        (self.root / "state.json").write_text(json.dumps(self.state))

    def current_state(self):
        return json.loads((self.root / "state.json").read_text())

    def events(self):
        return [json.loads(line) for line in (self.root / "events").read_text().splitlines()]

    def commands(self, prefix):
        return [(tool, args) for tool, args in self.events() if args[:len(prefix)] == prefix]

    def run_installer(self, **environment):
        result = subprocess.run([str(INSTALLER), str(self.destination)], cwd="/tmp",
                                env=dict(self.env, **environment), capture_output=True, text=True, timeout=20)
        self.assertFalse(list(self.destination.glob(".slkt-install.*")), result.stderr)
        return result

    def assert_old_binary(self):
        self.assertEqual(self.binary.read_text(), "old installed binary\n")

    def assert_installed(self, result):
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(os.access(self.binary, os.X_OK))
        self.assertEqual(subprocess.check_output([str(self.binary), "--version"], text=True).strip(),
                         "slkt version installer-test")

    def test_local_go_preferred(self):
        self.assert_installed(self.run_installer(GO="fake-go"))
        self.assertTrue(all(tool == "fake-go" for tool, _ in self.events()))

    def test_default_version_matches_local_and_container_builds(self):
        self.env.pop("VERSION")
        expected = subprocess.check_output(
            ["bash", str(INSTALLER.parent / "scripts/version.sh")], env=self.env, text=True).strip()
        base = (INSTALLER.parent / "cmd/slkt/VERSION").read_text().strip()
        self.assertTrue(expected.startswith(base + "-dev"), expected)
        for go in ("fake-go", "slakt-test-nonexistent-go"):
            with self.subTest(go=go):
                result = self.run_installer(GO=go)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(subprocess.check_output([str(self.binary), "--version"], text=True).strip(),
                                 "slkt version " + expected)

    def test_invalid_version_keeps_existing_installation(self):
        result = self.run_installer(VERSION="invalid version")
        self.assertEqual(result.returncode, 2)
        self.assertIn("VERSION may contain only", result.stderr)
        self.assert_old_binary()

    def test_local_build_failure_does_not_fall_back(self):
        self.assertEqual(self.run_installer(GO="fake-go", SLKT_TEST_BUILD_EXIT="23").returncode, 23)
        self.assert_old_binary()
        self.assertTrue(all(tool == "fake-go" for tool, _ in self.events()))

    def test_podman_build_and_new_image_cleanup(self):
        self.assert_installed(self.run_installer())
        self.assertEqual(self.commands(["run"])[0][0], "podman")
        self.assertEqual(len(self.commands(["image", "rm"])), 1)
        self.assertFalse(self.current_state()["image"])

    def test_docker_fallback(self):
        self.assert_installed(self.run_installer(SLKT_TEST_PODMAN="0"))
        self.assertEqual(self.commands(["run"])[0][0], "docker")

    def test_no_usable_runtime(self):
        result = self.run_installer(SLKT_TEST_PODMAN="0", SLKT_TEST_DOCKER="0")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("no usable Podman or Docker", result.stderr)
        self.assert_old_binary()

    def test_cached_image_retained(self):
        self.state["image"] = True
        self.save_state()
        self.assert_installed(self.run_installer())
        self.assertFalse(self.commands(["pull"]))
        self.assertFalse(self.commands(["image", "rm"]))

    def test_image_cached_under_another_tag_retained(self):
        self.state["before"] = [IMAGE_ID.removeprefix("sha256:")]
        self.save_state()
        self.assert_installed(self.run_installer())
        self.assertEqual(len(self.commands(["pull"])), 1)
        self.assertFalse(self.commands(["image", "rm"]))

    def test_failed_build_cleans_container_and_image(self):
        self.assertEqual(self.run_installer(SLKT_TEST_BUILD_EXIT="23").returncode, 23)
        self.assert_old_binary()
        self.assertFalse(self.current_state()["container"])
        self.assertFalse(self.current_state()["image"])
        self.assertEqual(len(self.commands(["container", "rm"])), 1)

    def test_failed_pull_cleans_image_if_it_finished(self):
        self.assertEqual(self.run_installer(SLKT_TEST_PULL_EXIT="24").returncode, 24)
        self.assert_old_binary()
        self.assertFalse(self.current_state()["image"])

    def test_invalid_built_binary_keeps_existing_installation(self):
        self.assertEqual(self.run_installer(SLKT_TEST_BAD_BINARY="1").returncode, 9)
        self.assert_old_binary()
        self.assertFalse(self.current_state()["image"])

    def test_shared_image_retained_with_warning(self):
        result = self.run_installer(SLKT_TEST_IMAGE_CLEANUP_FAIL="1")
        self.assert_installed(result)
        self.assertIn("Cleanup warning", result.stderr)
        self.assertTrue(self.current_state()["image"])

    def test_cleanup_failure_preserves_original_exit_status(self):
        result = self.run_installer(SLKT_TEST_BUILD_EXIT="23", SLKT_TEST_IMAGE_CLEANUP_FAIL="1", SLKT_TEST_CONTAINER_CLEANUP_FAIL="1")
        self.assertEqual(result.returncode, 23)
        self.assertIn("Cleanup warning", result.stderr)
        self.assert_old_binary()

    def test_cleanup_does_not_remove_another_runs_container(self):
        result = self.run_installer(SLKT_TEST_BUILD_EXIT="23", SLKT_TEST_FOREIGN_OWNER="1")
        self.assertEqual(result.returncode, 23)
        self.assert_old_binary()
        self.assertFalse(self.commands(["container", "rm"]))
        self.assertIn("different owner label", result.stderr)
        self.assertTrue(self.current_state()["container"])

    def test_interrupt_and_termination_cleanup(self):
        for sig, status, block in ((signal.SIGINT, 130, "run"), (signal.SIGTERM, 143, "run"),
                                   (signal.SIGTERM, 143, "pull")):
            with self.subTest(signal=sig, phase=block):
                self.state.update(image=False, container=False)
                self.save_state()
                marker = self.root / ("running" if block == "run" else "pulling")
                marker.unlink(missing_ok=True)
                process = subprocess.Popen([str(INSTALLER), str(self.destination)], cwd="/tmp",
                                           env=dict(self.env, SLKT_TEST_BLOCK=block), stdin=subprocess.DEVNULL,
                                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
                try:
                    deadline = time.monotonic() + 10
                    while not marker.exists() and process.poll() is None and time.monotonic() < deadline:
                        time.sleep(0.02)
                    self.assertTrue(marker.exists(), "build/pull did not start")
                    process.send_signal(sig)
                    _, stderr = process.communicate(timeout=10)
                    self.assertEqual(process.returncode, status, stderr)
                    self.assert_old_binary()
                    self.assertFalse(self.current_state()["container"])
                    self.assertFalse(self.current_state()["image"])
                    self.assertFalse(list(self.destination.glob(".slkt-install.*")))
                finally:
                    if process.poll() is None:
                        process.kill()
                        process.communicate(timeout=5)


if __name__ == "__main__":
    unittest.main()
