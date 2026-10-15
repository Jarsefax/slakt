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
        send(b"j")
        assert "Thread" in send(b"\r"), "thread did not open"
        assert "To #general / thread" in send(b"i"), "thread destination missing"
        send(b"Offline PTY thread smoke test. :thumbsup: :female-technologist::skin-tone-4:")
        sent = send(b"\x13")
        assert "Message sent" in sent, "demo thread send failed"
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
        send(b"?")
        send(b"q")
        assert process.wait(timeout=3) == 0, "TUI exited unsuccessfully"
        print("PASS: grouped sidebar/navigation, channel jump, thread send with emoji, search/result thread, DM send, resize, help, quit (offline PTY)")
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
