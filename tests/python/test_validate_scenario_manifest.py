from __future__ import annotations

import io
import json
import os
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from test_support import load_module, repo_root


class ValidateScenarioManifestTests(unittest.TestCase):
    def setUp(self) -> None:
        self.module = load_module("scripts/lib/scenario_manifest.py")
        self.root = repo_root()

    def write_script(self, scenario_root: Path, relative_path: str, body: str = "#!/bin/sh\nexit 0\n") -> None:
        path = scenario_root / relative_path
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(body, encoding="utf-8")
        path.chmod(path.stat().st_mode | stat.S_IXUSR)

    def write_manifest(self, scenario_root: Path, scenarios: list[dict[str, object]]) -> Path:
        manifest_path = scenario_root / "manifest.json"
        manifest_path.write_text(
            json.dumps({"version": 1, "scenarios": scenarios}, indent=2) + "\n",
            encoding="utf-8",
        )
        return manifest_path

    def test_validate_manifest_emits_runner_records_for_valid_manifest(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            scenario_root = Path(tmpdir)
            self.write_script(scenario_root, "shared/test-secretless.sh")
            manifest_path = self.write_manifest(
                scenario_root,
                [
                    {
                        "id": "shared/secretless",
                        "description": "Secretless fixture",
                        "lane": "secretless",
                        "platform": "any",
                        "manual": False,
                        "providers": ["codex"],
                        "persona": "developer",
                        "test_file": "shared/test-secretless.sh",
                        "requires_credentials": False,
                    }
                ],
            )

            records = self.module.load_scenarios(manifest_path)
            self.assertEqual(records[0]["test_file"], "shared/test-secretless.sh")

            argv = [
                "scenario_manifest.py",
                "list-tsv",
                str(manifest_path),
            ]
            with mock.patch.object(sys, "argv", argv), mock.patch("sys.stdout", new_callable=io.StringIO) as stdout:
                self.assertEqual(self.module.main(), 0)
            self.assertEqual(
                stdout.getvalue().strip(),
                "shared/secretless\tshared/test-secretless.sh\t0\tsecretless\tany\t0",
            )

    def test_validate_manifest_rejects_invalid_shapes(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            scenario_root = Path(tmpdir)
            self.write_script(scenario_root, "shared/test-secretless.sh")
            self.write_script(scenario_root, "shared/test-other.sh")

            invalid_scenarios = [
                (
                    [
                        {
                            "id": "shared/duplicate",
                            "description": "One",
                            "lane": "secretless",
                            "platform": "any",
                            "manual": False,
                            "providers": ["codex"],
                            "persona": "developer",
                            "test_file": "shared/test-secretless.sh",
                            "requires_credentials": False,
                        },
                        {
                            "id": "shared/duplicate",
                            "description": "Two",
                            "lane": "secretless",
                            "platform": "any",
                            "manual": False,
                            "providers": ["codex"],
                            "persona": "developer",
                            "test_file": "shared/test-other.sh",
                            "requires_credentials": False,
                        },
                    ],
                    "(?i)duplicate scenario id",
                ),
                (
                    [
                        {
                            "id": "shared/invalid-lane",
                            "description": "Bad lane",
                            "lane": "manual-only",
                            "platform": "any",
                            "manual": False,
                            "providers": ["codex"],
                            "persona": "developer",
                            "test_file": "shared/test-secretless.sh",
                            "requires_credentials": False,
                        }
                    ],
                    "must be one of",
                ),
                (
                    [
                        {
                            "id": "shared/secretless-creds",
                            "description": "Contradictory lane",
                            "lane": "secretless",
                            "platform": "any",
                            "manual": False,
                            "providers": ["codex"],
                            "persona": "developer",
                            "test_file": "shared/test-secretless.sh",
                            "requires_credentials": True,
                        }
                    ],
                    "cannot be secretless and require credentials",
                ),
                (
                    [
                        {
                            "id": "shared/bad-provider",
                            "description": "Bad provider",
                            "lane": "secretless",
                            "platform": "any",
                            "manual": False,
                            "providers": ["unknown"],
                            "persona": "developer",
                            "test_file": "shared/test-secretless.sh",
                            "requires_credentials": False,
                        }
                    ],
                    "must be one of",
                ),
            ]

            for scenarios, expected in invalid_scenarios:
                with self.subTest(expected=expected):
                    manifest_path = self.write_manifest(scenario_root, scenarios)
                    with self.assertRaisesRegex(SystemExit, expected):
                        self.module.load_scenarios(manifest_path)

    def test_verify_scenario_coverage_rejects_orphan_scripts(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            scenario_root = Path(tmpdir)
            self.write_script(scenario_root, "shared/test-secretless.sh")
            self.write_script(scenario_root, "shared/test-orphan.sh")
            manifest_path = self.write_manifest(
                scenario_root,
                [
                    {
                        "id": "shared/secretless",
                        "description": "Secretless fixture",
                        "lane": "secretless",
                        "platform": "any",
                        "manual": False,
                        "providers": ["codex"],
                        "persona": "developer",
                        "test_file": "shared/test-secretless.sh",
                        "requires_credentials": False,
                    }
                ],
            )
            env = os.environ.copy()
            env["WORKCELL_SCENARIO_ROOT"] = str(scenario_root)
            env["WORKCELL_SCENARIO_MANIFEST"] = str(manifest_path)

            result = subprocess.run(
                [str(self.root / "scripts/verify-scenario-coverage.sh")],
                cwd=self.root,
                check=False,
                capture_output=True,
                text=True,
                env=env,
            )

            self.assertEqual(result.returncode, 1)
            self.assertIn("Scenario scripts missing from manifest", result.stderr)
            self.assertIn("shared/test-orphan.sh", result.stderr)

    def test_run_scenario_tests_secretless_only_skips_non_secretless_entries(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            scenario_root = Path(tmpdir)
            self.write_script(
                scenario_root,
                "shared/test-secretless.sh",
                "#!/bin/sh\nset -eu\nprintf 'secretless-ran\\n'\n",
            )
            self.write_script(
                scenario_root,
                "shared/test-provider.sh",
                "#!/bin/sh\nset -eu\necho provider-ran >&2\nexit 1\n",
            )
            self.write_script(
                scenario_root,
                "shared/test-manual.sh",
                "#!/bin/sh\nset -eu\necho manual-ran >&2\nexit 1\n",
            )
            manifest_path = self.write_manifest(
                scenario_root,
                [
                    {
                        "id": "shared/secretless",
                        "description": "Secretless fixture",
                        "lane": "secretless",
                        "platform": "any",
                        "manual": False,
                        "providers": ["codex"],
                        "persona": "developer",
                        "test_file": "shared/test-secretless.sh",
                        "requires_credentials": False,
                    },
                    {
                        "id": "shared/provider",
                        "description": "Provider fixture",
                        "lane": "provider-e2e",
                        "platform": "any",
                        "manual": False,
                        "providers": ["codex"],
                        "persona": "developer",
                        "test_file": "shared/test-provider.sh",
                        "requires_credentials": True,
                    },
                    {
                        "id": "shared/manual",
                        "description": "Manual fixture",
                        "lane": "secretless",
                        "platform": "any",
                        "manual": True,
                        "providers": ["codex"],
                        "persona": "developer",
                        "test_file": "shared/test-manual.sh",
                        "requires_credentials": False,
                    },
                ],
            )
            env = os.environ.copy()
            env["WORKCELL_SCENARIO_ROOT"] = str(scenario_root)
            env["WORKCELL_SCENARIO_MANIFEST"] = str(manifest_path)

            result = subprocess.run(
                [str(self.root / "scripts/run-scenario-tests.sh"), "--secretless-only"],
                cwd=self.root,
                check=False,
                capture_output=True,
                text=True,
                env=env,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("secretless-ran", result.stdout)
            self.assertIn("PASS shared/secretless", result.stdout)
            self.assertIn("SKIP shared/provider (lane provider-e2e)", result.stdout)
            self.assertIn("SKIP shared/manual (manual lane)", result.stdout)


if __name__ == "__main__":
    unittest.main()
