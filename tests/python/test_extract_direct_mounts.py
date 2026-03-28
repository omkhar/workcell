from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from test_support import load_module


class ExtractDirectMountsTests(unittest.TestCase):
    def setUp(self) -> None:
        self.module = load_module("scripts/lib/extract_direct_mounts.py")

    def test_main_sanitizes_manifest_and_sorts_mounts(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            manifest_path = root / "manifest.json"
            mount_spec_path = root / "mounts.json"
            manifest_path.write_text(
                json.dumps(
                    {
                        "credentials": {
                            "codex_auth": {
                                "source": "/host/auth.json",
                                "mount_path": "/opt/workcell/host-inputs/credentials/codex-auth.json",
                            }
                        },
                        "copies": [
                            {
                                "source": {
                                    "source": "/host/secret.txt",
                                    "mount_path": "/opt/workcell/host-inputs/copies/0",
                                },
                                "target": "/state/agent-home/.config/workcell/token.txt",
                            }
                        ],
                        "ssh": {
                            "config": {
                                "source": "/host/ssh-config",
                                "mount_path": "/opt/workcell/host-inputs/ssh/config",
                            }
                        },
                    }
                ),
                encoding="utf-8",
            )

            argv = [
                "extract_direct_mounts.py",
                "--manifest",
                str(manifest_path),
                "--mount-spec",
                str(mount_spec_path),
            ]
            with mock.patch.object(sys, "argv", argv):
                self.assertEqual(self.module.main(), 0)

            manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
            mounts = json.loads(mount_spec_path.read_text(encoding="utf-8"))

            self.assertNotIn("source", manifest["credentials"]["codex_auth"])
            self.assertNotIn("source", manifest["copies"][0]["source"])
            self.assertNotIn("source", manifest["ssh"]["config"])
            self.assertEqual(
                [entry["mount_path"] for entry in mounts],
                sorted(entry["mount_path"] for entry in mounts),
            )
            self.assertEqual(
                mounts[0]["mount_path"],
                "/opt/workcell/host-inputs/copies/0",
            )

    def test_require_direct_mount_rejects_missing_source(self) -> None:
        with self.assertRaises(SystemExit):
            self.module.require_direct_mount(
                {"mount_path": "/opt/workcell/host-inputs/credentials/codex-auth.json"},
                "credentials.codex_auth",
            )

    def test_main_leaves_plain_copy_sources_inline(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            manifest_path = root / "manifest.json"
            mount_spec_path = root / "mounts.json"
            manifest_path.write_text(
                json.dumps(
                    {
                        "copies": [
                            {
                                "source": "copies/0",
                                "target": "/state/injected/public.txt",
                            }
                        ]
                    }
                ),
                encoding="utf-8",
            )

            argv = [
                "extract_direct_mounts.py",
                "--manifest",
                str(manifest_path),
                "--mount-spec",
                str(mount_spec_path),
            ]
            with mock.patch.object(sys, "argv", argv):
                self.assertEqual(self.module.main(), 0)

            manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
            mounts = json.loads(mount_spec_path.read_text(encoding="utf-8"))

            self.assertEqual(manifest["copies"][0]["source"], "copies/0")
            self.assertEqual(mounts, [])


if __name__ == "__main__":
    unittest.main()
