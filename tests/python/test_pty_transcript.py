from __future__ import annotations

import os
import runpy
import signal
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from test_support import load_module


class PtyTranscriptTests(unittest.TestCase):
    def setUp(self) -> None:
        self.module = load_module("scripts/lib/pty_transcript.py")

    def test_main_requires_command_after_separator(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            log_path = Path(tmpdir) / "transcript.log"
            argv = ["pty_transcript.py", "--log", str(log_path), "--"]
            with mock.patch.object(sys, "argv", argv), mock.patch.object(
                sys.stdin, "isatty", return_value=True
            ), mock.patch.object(sys.stdout, "isatty", return_value=True):
                with self.assertRaises(SystemExit) as exc:
                    self.module.main()
            self.assertEqual(str(exc.exception), "pty_transcript.py requires a command after --")

    def test_main_requires_interactive_terminal(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            log_path = Path(tmpdir) / "transcript.log"
            argv = ["pty_transcript.py", "--log", str(log_path), "--", "echo", "hello"]
            with mock.patch.object(sys, "argv", argv), mock.patch.object(
                sys.stdin, "isatty", return_value=False
            ), mock.patch.object(sys.stdout, "isatty", return_value=True):
                with self.assertRaises(SystemExit) as exc:
                    self.module.main()
            self.assertEqual(str(exc.exception), "pty_transcript.py requires an interactive terminal")

    def test_main_strips_separator_records_transcript_and_returns_exit_code(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            log_path = Path(tmpdir) / "transcript.log"
            argv = ["pty_transcript.py", "--log", str(log_path), "--", "fake-agent", "--version"]

            def fake_spawn(command, master_read, stdin_read):
                self.assertEqual(command, ["fake-agent", "--version"])
                stdin_read(10)
                master_read(11)
                return 7 << 8

            with mock.patch.object(sys, "argv", argv), mock.patch.object(
                sys.stdin, "isatty", return_value=True
            ), mock.patch.object(sys.stdout, "isatty", return_value=True), mock.patch.object(
                self.module.pty, "spawn", side_effect=fake_spawn
            ), mock.patch.object(
                self.module.os, "read", side_effect=[b"user input\n", b"child output\n"]
            ):
                self.assertEqual(self.module.main(), 7)

            transcript = log_path.read_text(encoding="utf-8")
            self.assertIn("# workcell-transcript-v1 start=", transcript)
            self.assertIn("user input\nchild output\n", transcript)
            self.assertIn("# workcell-transcript-v1 end=", transcript)
            self.assertIn("wait_status=1792", transcript)
            self.assertIn("exit_code=7", transcript)
            self.assertEqual(stat_mode(log_path), 0o600)

    def test_main_translates_signaled_status(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            log_path = Path(tmpdir) / "transcript.log"
            argv = ["pty_transcript.py", "--log", str(log_path), "--", "fake-agent"]

            with mock.patch.object(sys, "argv", argv), mock.patch.object(
                sys.stdin, "isatty", return_value=True
            ), mock.patch.object(sys.stdout, "isatty", return_value=True), mock.patch.object(
                self.module.pty, "spawn", return_value=signal.SIGTERM
            ):
                self.assertEqual(self.module.main(), 128 + signal.SIGTERM)

    def test_main_returns_one_for_unexpected_wait_status_and_empty_reads(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            log_path = Path(tmpdir) / "transcript.log"
            argv = ["pty_transcript.py", "--log", str(log_path), "fake-agent"]

            def fake_spawn(command, master_read, stdin_read):
                self.assertEqual(command, ["fake-agent"])
                self.assertEqual(stdin_read(10), b"")
                self.assertEqual(master_read(11), b"")
                return 0x7F

            with mock.patch.object(sys, "argv", argv), mock.patch.object(
                sys.stdin, "isatty", return_value=True
            ), mock.patch.object(sys.stdout, "isatty", return_value=True), mock.patch.object(
                self.module.pty, "spawn", side_effect=fake_spawn
            ), mock.patch.object(self.module.os, "read", side_effect=[b"", b""]):
                self.assertEqual(self.module.main(), 1)

            transcript = log_path.read_text(encoding="utf-8")
            self.assertIn("# workcell-transcript-v1 start=", transcript)
            self.assertIn("# workcell-transcript-v1 end=", transcript)

    def test_module_main_entrypoint_raises_system_exit_with_return_code(self) -> None:
        script_path = Path(__file__).resolve().parents[2] / "scripts/lib/pty_transcript.py"
        argv = ["pty_transcript.py", "--log", "/tmp/workcell-transcript-entry.log", "fake-agent"]

        with mock.patch.object(sys, "argv", argv), mock.patch.object(
            sys.stdin, "isatty", return_value=True
        ), mock.patch.object(sys.stdout, "isatty", return_value=True), mock.patch(
            "pty.spawn", return_value=0
        ):
            with self.assertRaises(SystemExit) as exc:
                runpy.run_path(str(script_path), run_name="__main__")
        self.assertEqual(exc.exception.code, 0)


def stat_mode(path: Path) -> int:
    return os.stat(path).st_mode & 0o777


if __name__ == "__main__":
    unittest.main()
