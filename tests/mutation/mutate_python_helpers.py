#!/usr/bin/env python3
from __future__ import annotations

import shutil
import subprocess
import sys
import tempfile
import os
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]

MUTATIONS = [
    (
        "scripts/lib/render_injection_bundle.py",
        'if target_is_reserved(candidate):',
        'if False and target_is_reserved(candidate):',
        "reserved target protection",
    ),
    (
        "scripts/lib/render_injection_bundle.py",
        '"claude_mcp": f"{DIRECT_MOUNT_ROOT}/credentials/claude-mcp.json",',
        '# removed claude_mcp mutation',
        "claude mcp credential support",
    ),
    (
        "scripts/lib/render_injection_bundle.py",
        "    if stat.S_IMODE(path_stat.st_mode) & 0o077:",
        "    if False and stat.S_IMODE(path_stat.st_mode) & 0o077:",
        "secret permission hygiene",
    ),
    (
        "scripts/lib/pty_transcript.py",
        '    if command[:1] == ["--"]:',
        '    if False and command[:1] == ["--"]:',
        "pty separator stripping",
    ),
    (
        "scripts/lib/extract_direct_mounts.py",
        'source = entry.pop("source", None)',
        'source = entry.get("source")',
        "manifest source stripping",
    ),
]


def prepare_temp_root() -> Path:
    temp_root = Path(tempfile.mkdtemp(prefix="workcell-python-mutation."))
    shutil.copytree(REPO_ROOT / "scripts", temp_root / "scripts")
    shutil.copytree(REPO_ROOT / "tests/python", temp_root / "tests/python")
    return temp_root


def run_unit_tests(temp_root: Path) -> int:
    env = os.environ.copy()
    env.update(
        {
            "WORKCELL_MUTATION_ROOT": str(temp_root),
            "PYTHONDONTWRITEBYTECODE": "1",
        }
    )
    return subprocess.run(
        [
            sys.executable,
            "-m",
            "unittest",
            "discover",
            "-s",
            str(temp_root / "tests/python"),
            "-p",
            "test_*.py",
        ],
        check=False,
        env=env,
        cwd=temp_root,
    ).returncode


def apply_mutation(temp_root: Path, relative_path: str, original: str, replacement: str) -> None:
    target = temp_root / relative_path
    content = target.read_text(encoding="utf-8")
    if original not in content:
        raise SystemExit(f"mutation anchor not found in {relative_path}: {original}")
    target.write_text(content.replace(original, replacement, 1), encoding="utf-8")


def main() -> int:
    failures: list[str] = []
    for relative_path, original, replacement, label in MUTATIONS:
        temp_root = prepare_temp_root()
        try:
            apply_mutation(temp_root, relative_path, original, replacement)
            if run_unit_tests(temp_root) == 0:
                failures.append(label)
        finally:
            shutil.rmtree(temp_root, ignore_errors=True)

    if failures:
        raise SystemExit(
            "Python mutation coverage did not catch: " + ", ".join(sorted(failures))
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
