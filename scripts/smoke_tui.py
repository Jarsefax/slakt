#!/usr/bin/env python3
"""Linux PTY smoke test. Only runs --demo; never touches Slack or the keyring."""

import fcntl
import os
from pathlib import Path
import pty
import select
import signal
import struct
import subprocess
import sys
import termios
import time


def read_pty(master, seconds=0.25):
    chunk = bytearray()
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        ready, _, _ = select.select([master], [], [], 0.02)
        if ready:
            try:
                data = os.read(master, 65536)
            except OSError:
                break
            if not data:
                break
            chunk.extend(data)
    return chunk


def check_quit_paths(binary, environment):
    scenarios = [
        ("channel", [], b"q"),
        ("thread", [b"\r"], b"q"),
        ("sidebar", [b"\t"], b"q"),
        ("help", [b"?"], b"q"),
        ("grouped quit", [], b"qqq"),
        ("grouped navigation and quit", [], b"gqq"),
        ("rapid Esc q in composer", [b"i", b"draft"], b"\x1bq"),
        ("rapid Esc q in search", [b"/"], b"\x1bq"),
        ("rapid Esc q in jump", [b"\x0b"], b"\x1bq"),
        ("rapid Esc q in reaction", [b"r"], b"\x1bq"),
        ("after successful send", [b"i", b"draft", b"\x13"], b"q"),
        ("literal q in composer", [b"i", b"qqq"], b"\x03"),
        ("literal q in search", [b"/", b"qqq"], b"\x03"),
        ("literal q in jump", [b"\x0b", b"qqq"], b"\x03"),
        ("literal q in reaction", [b"r", b"qqq"], b"\x03"),
        ("grouped explicit text entry", [b"iq"], b"\x03"),
        ("pasted commands", [b"\x1b[200~iqqq\x1b[201~"], b"q"),
    ]
    for index, (label, setup, quit_keys) in enumerate(scenarios):
        master, slave = pty.openpty()
        width = 70 if index % 2 else 140
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, width, 0, 0))
        process = subprocess.Popen(
            [binary, "--demo"], stdin=slave, stdout=slave, stderr=slave,
            env=environment, start_new_session=True,
        )
        os.close(slave)
        try:
            initial = read_pty(master, 0.5).decode(errors="replace")
            assert "OFFLINE DEMO" in initial, f"{label}: initial frame missing"
            for data in setup:
                os.write(master, data)
                read_pty(master, 0.15)
                assert process.poll() is None, f"{label}: exited during setup/text entry"
            os.write(master, quit_keys)
            read_pty(master)
            assert process.wait(timeout=3) == 0, f"{label}: quit failed"
        finally:
            if process.poll() is None:
                process.terminate()
                process.wait(timeout=3)
            os.close(master)
    print(f"PASS: {len(scenarios)} quit/text-entry paths across wide and narrow terminals")


def main():
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 140, 0, 0))
    environment = dict(os.environ, TERM="xterm-256color", NO_COLOR="1")
    binary = sys.argv[1] if len(sys.argv) > 1 else "./bin/slkt"
    process = subprocess.Popen(
        [binary, "--demo"], stdin=slave, stdout=slave, stderr=slave,
        env=environment, start_new_session=True,
    )
    os.close(slave)
    transcript = bytearray()

    def read(seconds=0.25):
        chunk = read_pty(master, seconds)
        transcript.extend(chunk)
        return chunk.decode(errors="replace")

    def send(data):
        os.write(master, data)
        return read()

    try:
        initial = read(1)
        assert "OFFLINE DEMO" in initial, "initial demo frame missing"
        for section in ("Starred", "Channels", "Direct messages", "Group messages"):
            assert section in initial, f"sidebar section missing: {section}"
        send(b"\t")
        send(b"j")
        assert "#general" in send(b"\r"), "sidebar navigation selected a heading"
        send(b"\x0b")  # Ctrl+k
        send(b"general")
        send(b"\r")
        send(b"g")
        send(b"j")
        selected = send(b"j")
        assert "  3 ▎" in selected, "selected message did not show its 1-based position"
        assert "↳ Thread · 2 replies" in selected, "prominent thread indicator missing"
        assert "Thread" in send(b"\r"), "thread did not open"
        composing = send(b"i")
        assert "COMPOSE" in composing and "To #general / thread" in composing, "explicit compose mode or thread destination missing"
        send(b"Offline PTY thread smoke test. :thumbsup: :female-technologist::skin-tone-4:")
        sent = send(b"\x13")
        assert "Message sent" in sent, "demo thread send failed"
        assert "q quit" in sent, "successful thread send did not return to browsing"
        assert "👍" in sent and "👩🏽‍💻" in sent, "sent emoji did not render"
        send(b"\x1b")
        send(b"\x1b")
        send(b"/")
        send(b"deploy")
        assert "2 matches" in send(b"\r"), "search results missing"
        assert "Thread" in send(b"\r"), "search result thread did not open"
        send(b"\x1b")
        send(b"\x1b")
        send(b"\x0b")
        send(b"@alice")
        assert "@alice" in send(b"\r"), "DM did not open"
        send(b"i")
        send(b"Offline PTY DM smoke test.")
        assert "Message sent" in send(b"\x13"), "demo DM send failed"
        send(b"\x1b")
        fcntl.ioctl(master, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 70, 0, 0))
        process.send_signal(signal.SIGWINCH)
        read()
        assert "Keyboard guide" in send(b"?"), "help missing after resize"
        send(b"j")
        send(b"q")
        assert process.wait(timeout=3) == 0, "TUI exited unsuccessfully"
        print("PASS: grouped sidebar/navigation, hybrid message numbers, thread indicators, channel jump, thread send with emoji, search/result thread, DM send, resize, help, quit (offline PTY)")
        check_quit_paths(binary, environment)
    finally:
        if process.poll() is None:
            process.terminate()
            process.wait(timeout=3)
        os.close(master)
        log = Path("/tmp/slakt-tui-smoke.log")
        log.write_bytes(transcript)
        print(f"Demo terminal transcript: {log}")


if __name__ == "__main__":
    main()
