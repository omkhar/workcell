from __future__ import annotations

import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from test_support import repo_root


class ValidationSnapshotTests(unittest.TestCase):
    def create_repo(self, root: Path) -> Path:
        repo = root / "fixture-repo"
        repo.mkdir()
        subprocess.run(["git", "init", "-q", str(repo)], check=True)
        subprocess.run(["git", "-C", str(repo), "config", "user.name", "Workcell Tests"], check=True)
        subprocess.run(
            ["git", "-C", str(repo), "config", "user.email", "workcell-tests@example.com"],
            check=True,
        )
        (repo / "README.md").write_text("fixture\n", encoding="utf-8")
        subprocess.run(["git", "-C", str(repo), "add", "README.md"], check=True)
        subprocess.run(["git", "-C", str(repo), "commit", "-q", "-m", "init"], check=True)
        return repo

    def run_snapshot(self, repo: Path, *, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
        script = repo_root() / "scripts/with-validation-snapshot.sh"
        return subprocess.run(
            [
                str(script),
                "--repo",
                str(repo),
                "--mode",
                "head",
                "--",
                sys.executable,
                "-c",
                "import os; print(os.getcwd()); print(os.environ['WORKCELL_VALIDATION_SNAPSHOT_DIR'])",
            ],
            check=False,
            capture_output=True,
            text=True,
            env=env,
        )

    def test_snapshot_defaults_to_repo_parent(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            temp_root = Path(tmpdir)
            repo = self.create_repo(temp_root)

            result = self.run_snapshot(repo)

            self.assertEqual(result.returncode, 0, result.stderr)
            cwd, snapshot_dir = result.stdout.strip().splitlines()
            self.assertEqual(Path(cwd), Path(snapshot_dir))
            self.assertEqual(Path(snapshot_dir).parent.resolve(), repo.parent.resolve())

    def test_snapshot_parent_override_is_honored(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            temp_root = Path(tmpdir)
            repo = self.create_repo(temp_root)
            override_parent = temp_root / "snapshots"
            override_parent.mkdir()
            env = os.environ.copy()
            env["WORKCELL_VALIDATION_SNAPSHOT_PARENT"] = str(override_parent)

            result = self.run_snapshot(repo, env=env)

            self.assertEqual(result.returncode, 0, result.stderr)
            _, snapshot_dir = result.stdout.strip().splitlines()
            self.assertEqual(Path(snapshot_dir).parent.resolve(), override_parent.resolve())


if __name__ == "__main__":
    unittest.main()
