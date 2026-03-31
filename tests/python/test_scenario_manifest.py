from __future__ import annotations

import contextlib
import io
import json
import tempfile
import unittest
from pathlib import Path

from test_support import load_module


class ScenarioManifestTests(unittest.TestCase):
    def setUp(self) -> None:
        self.module = load_module("scripts/lib/scenario_manifest.py")

    def create_repo_root(self) -> tuple[tempfile.TemporaryDirectory[str], Path]:
        temp_dir = tempfile.TemporaryDirectory()
        root = Path(temp_dir.name)
        (root / "tests/scenarios/shared").mkdir(parents=True)
        return temp_dir, root

    def write_manifest(self, root: Path, payload: dict[str, object]) -> Path:
        manifest = root / "tests/scenarios/manifest.json"
        manifest.write_text(json.dumps(payload), encoding="utf-8")
        return manifest

    def test_load_scenarios_applies_defaults_for_secretless_entries(self) -> None:
        temp_dir, root = self.create_repo_root()
        self.addCleanup(temp_dir.cleanup)
        manifest = self.write_manifest(
            root,
            {
                "version": 1,
                "scenarios": [
                    {
                        "id": "shared/example",
                        "description": "Example scenario",
                        "providers": ["codex", "claude"],
                        "persona": "developer",
                        "test_file": "shared/test-example.sh",
                    }
                ],
            },
        )

        scenarios = self.module.load_scenarios(manifest)

        self.assertEqual(len(scenarios), 1)
        self.assertEqual(scenarios[0]["lane"], "secretless")
        self.assertEqual(scenarios[0]["platform"], "any")
        self.assertFalse(scenarios[0]["manual"])
        self.assertFalse(scenarios[0]["requires_credentials"])

    def test_load_scenarios_rejects_duplicate_ids_and_path_traversal(self) -> None:
        temp_dir, root = self.create_repo_root()
        self.addCleanup(temp_dir.cleanup)

        duplicate_manifest = self.write_manifest(
            root,
            {
                "version": 1,
                "scenarios": [
                    {
                        "id": "shared/example",
                        "description": "Example scenario",
                        "providers": ["codex"],
                        "persona": "developer",
                        "test_file": "shared/test-one.sh",
                    },
                    {
                        "id": "shared/example",
                        "description": "Duplicate id",
                        "providers": ["claude"],
                        "persona": "developer",
                        "test_file": "shared/test-two.sh",
                    },
                ],
            },
        )
        with self.assertRaises(SystemExit) as duplicate_exc:
            self.module.load_scenarios(duplicate_manifest)
        self.assertIn("scenario id", str(duplicate_exc.exception).lower())

        traversal_manifest = self.write_manifest(
            root,
            {
                "version": 1,
                "scenarios": [
                    {
                        "id": "shared/example",
                        "description": "Traversal",
                        "providers": ["codex"],
                        "persona": "developer",
                        "test_file": "../escape.sh",
                    }
                ],
            },
        )
        with self.assertRaises(SystemExit) as traversal_exc:
            self.module.load_scenarios(traversal_manifest)
        self.assertIn("without traversal", str(traversal_exc.exception))

    def test_verify_coverage_rejects_missing_and_orphaned_scripts(self) -> None:
        temp_dir, root = self.create_repo_root()
        self.addCleanup(temp_dir.cleanup)
        (root / "tests/scenarios/shared/test-orphan.sh").write_text("#!/bin/sh\n", encoding="utf-8")

        missing_manifest = self.write_manifest(
            root,
            {
                "version": 1,
                "scenarios": [
                    {
                        "id": "shared/missing",
                        "description": "Missing script",
                        "providers": ["codex"],
                        "persona": "developer",
                        "test_file": "shared/test-missing.sh",
                    }
                ],
            },
        )
        with self.assertRaises(SystemExit) as missing_exc:
            self.module.verify_coverage(root / "tests/scenarios", missing_manifest)
        self.assertIn("Missing test file", str(missing_exc.exception))

        (root / "tests/scenarios/shared/test-present.sh").write_text("#!/bin/sh\n", encoding="utf-8")
        orphan_manifest = self.write_manifest(
            root,
            {
                "version": 1,
                "scenarios": [
                    {
                        "id": "shared/present",
                        "description": "Present script",
                        "providers": ["codex"],
                        "persona": "developer",
                        "test_file": "shared/test-present.sh",
                    }
                ],
            },
        )
        with self.assertRaises(SystemExit) as orphan_exc:
            self.module.verify_coverage(root / "tests/scenarios", orphan_manifest)
        self.assertIn("missing from manifest", str(orphan_exc.exception))

    def test_main_lists_rows_and_verifies_coverage(self) -> None:
        temp_dir, root = self.create_repo_root()
        self.addCleanup(temp_dir.cleanup)
        (root / "tests/scenarios/shared/test-example.sh").write_text("#!/bin/sh\n", encoding="utf-8")
        manifest = self.write_manifest(
            root,
            {
                "version": 1,
                "scenarios": [
                    {
                        "id": "shared/example",
                        "description": "Example scenario",
                        "providers": ["codex", "gemini"],
                        "persona": "developer",
                        "test_file": "shared/test-example.sh",
                    }
                ],
            },
        )

        stdout = io.StringIO()
        with contextlib.redirect_stdout(stdout):
            self.assertEqual(self.module.main(["list-tsv", str(manifest)]), 0)
        self.assertEqual(stdout.getvalue().strip(), "shared/example\tshared/test-example.sh\t0\tsecretless\tany\t0")

        self.assertEqual(
            self.module.main(["verify-coverage", str(root / "tests/scenarios"), str(manifest)]),
            0,
        )


if __name__ == "__main__":
    unittest.main()
