from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


class ResolveCredentialSourcesTests(unittest.TestCase):
    def run_helper(
        self,
        policy_path: Path,
        agent: str,
        mode: str,
        resolution_mode: str,
        output_root: Path,
        extra_env: dict[str, str] | None = None,
        check: bool = True,
    ) -> subprocess.CompletedProcess[str]:
        output_policy = output_root / "resolved-policy.toml"
        output_metadata = output_root / "resolver-metadata.json"
        env = os.environ.copy()
        if extra_env:
            env.update(extra_env)
        return subprocess.run(
            [
                sys.executable,
                "scripts/lib/resolve_credential_sources.py",
                "--policy",
                str(policy_path),
                "--agent",
                agent,
                "--mode",
                mode,
                "--resolution-mode",
                resolution_mode,
                "--output-policy",
                str(output_policy),
                "--output-metadata",
                str(output_metadata),
                "--output-root",
                str(output_root),
            ],
            cwd=Path(__file__).resolve().parents[2],
            check=check,
            text=True,
            capture_output=True,
            env=env,
        )

    def test_metadata_mode_rewrites_claude_resolver_to_placeholder_source(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "policy.toml"
            policy_path.write_text(
                'version = 1\n[credentials.claude_auth]\nresolver = "claude-macos-keychain"\nmaterialization = "ephemeral"\n',
                encoding="utf-8",
            )

            self.run_helper(policy_path, "claude", "strict", "metadata", root)

            resolved_policy = (root / "resolved-policy.toml").read_text(encoding="utf-8")
            metadata = json.loads((root / "resolver-metadata.json").read_text(encoding="utf-8"))

            self.assertIn('source = ', resolved_policy)
            self.assertEqual(
                metadata["credential_resolvers"]["claude_auth"],
                "claude-macos-keychain",
            )
            self.assertEqual(
                metadata["credential_resolution_states"]["claude_auth"],
                "configured-only",
            )

    def test_launch_mode_fails_closed_without_supported_export_path(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "policy.toml"
            policy_path.write_text(
                'version = 1\n[credentials.claude_auth]\nresolver = "claude-macos-keychain"\nmaterialization = "ephemeral"\n',
                encoding="utf-8",
            )

            result = self.run_helper(
                policy_path,
                "claude",
                "strict",
                "launch",
                root,
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Claude macOS login reuse is configured", result.stderr)

    def test_launch_mode_accepts_test_export_file(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "policy.toml"
            export_path = root / "claude-export.json"
            export_path.write_text('{"token":"claude"}\n', encoding="utf-8")
            export_path.chmod(0o600)
            policy_path.write_text(
                'version = 1\n[credentials.claude_auth]\nresolver = "claude-macos-keychain"\nmaterialization = "ephemeral"\n',
                encoding="utf-8",
            )

            self.run_helper(
                policy_path,
                "claude",
                "strict",
                "launch",
                root,
                extra_env={"WORKCELL_TEST_CLAUDE_KEYCHAIN_EXPORT_FILE": str(export_path)},
            )

            metadata = json.loads((root / "resolver-metadata.json").read_text(encoding="utf-8"))
            resolved_policy = (root / "resolved-policy.toml").read_text(encoding="utf-8")

            self.assertEqual(
                metadata["credential_resolution_states"]["claude_auth"],
                "resolved",
            )
            self.assertIn("resolved/credentials/claude_auth.json", resolved_policy)

    def test_provider_filtered_resolver_entry_is_dropped_without_crashing(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "policy.toml"
            policy_path.write_text(
                'version = 1\n[credentials.claude_auth]\nresolver = "claude-macos-keychain"\nmaterialization = "ephemeral"\nproviders = ["codex"]\n',
                encoding="utf-8",
            )

            self.run_helper(policy_path, "claude", "strict", "metadata", root)

            resolved_policy = (root / "resolved-policy.toml").read_text(encoding="utf-8")
            metadata = json.loads((root / "resolver-metadata.json").read_text(encoding="utf-8"))

            self.assertNotIn("[credentials.claude_auth]", resolved_policy)
            self.assertEqual(metadata["credential_input_kinds"], {})


if __name__ == "__main__":
    unittest.main()
