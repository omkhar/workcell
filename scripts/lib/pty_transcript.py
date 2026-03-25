#!/usr/bin/env python3
"""Capture a full interactive transcript for a child command via PTY."""

from __future__ import annotations

import argparse
import datetime as dt
import os
import pty
import sys
from pathlib import Path


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--log", required=True)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    command = args.command
    if command[:1] == ["--"]:
        command = command[1:]
    if not command:
        raise SystemExit("pty_transcript.py requires a command after --")
    if not sys.stdin.isatty() or not sys.stdout.isatty():
        raise SystemExit("pty_transcript.py requires an interactive terminal")

    log_path = Path(args.log).expanduser().resolve()
    log_path.parent.mkdir(parents=True, exist_ok=True)
    with log_path.open("wb") as handle:
        os.chmod(log_path, 0o600)
        started_at = dt.datetime.now(dt.timezone.utc).isoformat()
        header = f"# workcell-transcript-v1 start={started_at}\n".encode("utf-8")
        handle.write(header)
        handle.flush()

        def stdin_read(fd: int) -> bytes:
            data = os.read(fd, 1024)
            if data:
                handle.write(data)
                handle.flush()
            return data

        def master_read(fd: int) -> bytes:
            data = os.read(fd, 1024)
            if data:
                handle.write(data)
                handle.flush()
            return data

        status = pty.spawn(command, master_read=master_read, stdin_read=stdin_read)
        exit_code = 1
        if os.WIFEXITED(status):
            exit_code = os.WEXITSTATUS(status)
        elif os.WIFSIGNALED(status):
            exit_code = 128 + os.WTERMSIG(status)
        finished_at = dt.datetime.now(dt.timezone.utc).isoformat()
        footer = (
            f"\n# workcell-transcript-v1 end={finished_at} "
            f"wait_status={status} exit_code={exit_code}\n"
        ).encode("utf-8")
        handle.write(footer)
        handle.flush()

    return exit_code


if __name__ == "__main__":
    raise SystemExit(main())
