#!/usr/bin/env python3
from __future__ import annotations

import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
RUST_ROOT = REPO_ROOT / "runtime/container/rust"

MUTATIONS = [
    (
        'matches!(value, Some(candidate) if !candidate.is_empty() && !candidate.eq_ignore_ascii_case("strict"))',
        'matches!(value, Some(candidate) if !candidate.is_empty())',
        "strict-mode matcher",
    ),
    (
        "path == root",
        "false",
        "root-prefix matcher",
    ),
]


def prepare_temp_root() -> Path:
    temp_root = Path(tempfile.mkdtemp(prefix="workcell-rust-mutation."))
    shutil.copytree(RUST_ROOT, temp_root / "runtime/container/rust")
    return temp_root


def run_rust_tests(temp_root: Path) -> int:
    return subprocess.run(
        ["cargo", "test", "--locked", "--offline"],
        cwd=temp_root / "runtime/container/rust",
        check=False,
    ).returncode


def main() -> int:
    failures: list[str] = []
    for original, replacement, label in MUTATIONS:
        temp_root = prepare_temp_root()
        try:
            target = temp_root / "runtime/container/rust/src/lib.rs"
            content = target.read_text(encoding="utf-8")
            if original not in content:
                raise SystemExit(f"mutation anchor not found for {label}")
            target.write_text(content.replace(original, replacement, 1), encoding="utf-8")
            if run_rust_tests(temp_root) == 0:
                failures.append(label)
        finally:
            shutil.rmtree(temp_root, ignore_errors=True)

    if failures:
        raise SystemExit(
            "Rust mutation coverage did not catch: " + ", ".join(sorted(failures))
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
