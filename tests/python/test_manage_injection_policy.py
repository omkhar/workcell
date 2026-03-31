from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


class ManageInjectionPolicyTests(unittest.TestCase):
    def run_helper(
        self,
        *args: str,
        check: bool = True,
    ) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [
                sys.executable,
                "scripts/lib/manage_injection_policy.py",
                *args,
            ],
            cwd=Path(__file__).resolve().parents[2],
            check=check,
            text=True,
            capture_output=True,
        )

    def test_init_set_status_and_unset_round_trip(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            managed_root = root / "credentials"
            source_path = root / "auth.json"
            source_path.write_text("{}\n", encoding="utf-8")
            source_path.chmod(0o600)

            self.run_helper(
                "init",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
            )
            self.run_helper(
                "set",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
                "--agent",
                "codex",
                "--credential",
                "codex_auth",
                "--source",
                str(source_path),
            )
            status = self.run_helper(
                "status",
                "--policy",
                str(policy_path),
                "--agent",
                "codex",
            )

            self.assertIn("credential_keys=codex_auth", status.stdout)
            self.assertIn("credential_input_kinds=codex_auth:source", status.stdout)
            self.assertIn("provider_auth_mode=codex_auth", status.stdout)
            self.assertTrue((managed_root / "codex" / "auth.json").is_file())

            unset = self.run_helper(
                "unset",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
                "--credential",
                "codex_auth",
            )
            self.assertIn("removed=1", unset.stdout)
            self.assertFalse((managed_root / "codex" / "auth.json").exists())

    def test_set_resolver_records_ephemeral_materialization(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            managed_root = root / "credentials"

            self.run_helper(
                "init",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
            )
            self.run_helper(
                "set",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
                "--agent",
                "claude",
                "--credential",
                "claude_auth",
                "--resolver",
                "claude-macos-keychain",
                "--ack-host-resolver",
            )
            status = self.run_helper(
                "status",
                "--policy",
                str(policy_path),
                "--agent",
                "claude",
            )

            self.assertIn("credential_input_kinds=claude_auth:resolver", status.stdout)
            self.assertIn("credential_resolvers=claude_auth:claude-macos-keychain", status.stdout)
            self.assertIn("credential_materialization=claude_auth:ephemeral", status.stdout)
            self.assertIn("credential_resolution_states=claude_auth:configured-only", status.stdout)
            self.assertIn("provider_auth_mode=none", status.stdout)
            self.assertIn("provider_auth_modes=none", status.stdout)

    def test_shared_credentials_are_scoped_to_the_requested_agent(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            managed_root = root / "credentials"
            hosts_path = root / "hosts.yml"
            hosts_path.write_text("github.com:\n", encoding="utf-8")
            hosts_path.chmod(0o600)

            self.run_helper(
                "init",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
            )
            self.run_helper(
                "set",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
                "--agent",
                "codex",
                "--credential",
                "github_hosts",
                "--source",
                str(hosts_path),
            )

            codex_status = self.run_helper(
                "status",
                "--policy",
                str(policy_path),
                "--agent",
                "codex",
            )
            claude_status = self.run_helper(
                "status",
                "--policy",
                str(policy_path),
                "--agent",
                "claude",
            )

            self.assertIn("shared_auth_modes=github_hosts", codex_status.stdout)
            self.assertIn("credential_keys=none", claude_status.stdout)
            self.assertIn("shared_auth_modes=none", claude_status.stdout)

    def test_set_preserves_existing_selectors(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            managed_root = root / "credentials"
            source_path = root / "auth.json"
            source_path.write_text("{}\n", encoding="utf-8")
            source_path.chmod(0o600)
            policy_path.write_text(
                'version = 1\n[credentials.codex_auth]\nsource = "/tmp/original.json"\nproviders = ["codex"]\nmodes = ["strict"]\n',
                encoding="utf-8",
            )

            self.run_helper(
                "set",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
                "--agent",
                "codex",
                "--credential",
                "codex_auth",
                "--source",
                str(source_path),
            )
            rendered = policy_path.read_text(encoding="utf-8")

            self.assertIn('providers = ["codex"]', rendered)
            self.assertIn('modes = ["strict"]', rendered)

    def test_set_preserves_existing_shared_github_scope(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            managed_root = root / "credentials"
            hosts_path = root / "hosts.yml"
            hosts_path.write_text("github.com:\n", encoding="utf-8")
            hosts_path.chmod(0o600)
            policy_path.write_text(
                'version = 1\n[credentials.github_hosts]\nsource = "/tmp/original-hosts.yml"\nproviders = ["codex", "claude"]\n',
                encoding="utf-8",
            )

            self.run_helper(
                "set",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
                "--agent",
                "codex",
                "--credential",
                "github_hosts",
                "--source",
                str(hosts_path),
            )
            rendered = policy_path.read_text(encoding="utf-8")

            self.assertIn('providers = ["codex", "claude"]', rendered)

    def test_status_rejects_conflicting_source_and_resolver(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            policy_path.write_text(
                'version = 1\n[credentials.claude_auth]\nsource = "/tmp/auth.json"\nresolver = "claude-macos-keychain"\nmaterialization = "ephemeral"\n',
                encoding="utf-8",
            )

            status = self.run_helper(
                "status",
                "--policy",
                str(policy_path),
                "--agent",
                "claude",
                check=False,
            )

            self.assertNotEqual(status.returncode, 0)
            self.assertIn(
                "credentials.claude_auth must not declare both source and resolver",
                status.stderr,
            )

    def test_status_rejects_conflicting_source_and_resolver_without_agent_filter(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            policy_path.write_text(
                'version = 1\n[credentials.claude_auth]\nsource = "/tmp/auth.json"\nresolver = "claude-macos-keychain"\nmaterialization = "ephemeral"\n',
                encoding="utf-8",
            )

            status = self.run_helper(
                "status",
                "--policy",
                str(policy_path),
                check=False,
            )

            self.assertNotEqual(status.returncode, 0)
            self.assertIn(
                "credentials.claude_auth must not declare both source and resolver",
                status.stderr,
            )

    def test_agentless_status_rejects_unsupported_resolver(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            policy_path.write_text(
                'version = 1\n[credentials.claude_auth]\nresolver = "bogus"\nmaterialization = "ephemeral"\n',
                encoding="utf-8",
            )

            status = self.run_helper(
                "status",
                "--policy",
                str(policy_path),
                check=False,
            )

            self.assertNotEqual(status.returncode, 0)
            self.assertIn(
                "credentials.claude_auth.resolver is unsupported: bogus",
                status.stderr,
            )

    def test_agentless_status_validates_selector_values(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            policy_path.write_text(
                'version = 1\n[credentials.codex_auth]\nsource = "/tmp/auth.json"\nproviders = ["bogus"]\n',
                encoding="utf-8",
            )

            status = self.run_helper(
                "status",
                "--policy",
                str(policy_path),
                check=False,
            )

            self.assertNotEqual(status.returncode, 0)
            self.assertIn(
                "credentials.codex_auth.providers contains unsupported value: bogus",
                status.stderr,
            )

    def test_status_rejects_shared_github_credentials_without_providers(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            policy_path.write_text(
                'version = 1\n[credentials.github_hosts]\nsource = "/tmp/hosts.yml"\n',
                encoding="utf-8",
            )

            status = self.run_helper(
                "status",
                "--policy",
                str(policy_path),
                "--agent",
                "codex",
                check=False,
            )

            self.assertNotEqual(status.returncode, 0)
            self.assertIn(
                "credentials.github_hosts.providers is required so shared GitHub credentials stay least-privilege",
                status.stderr,
            )

    def test_set_rejects_credential_declared_in_included_fragment(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            fragment_path = root / "fragment.toml"
            managed_root = root / "credentials"
            source_path = root / "auth.json"
            source_path.write_text("{}\n", encoding="utf-8")
            source_path.chmod(0o600)
            fragment_path.write_text(
                'version = 1\n[credentials.codex_auth]\nsource = "/tmp/original.json"\n',
                encoding="utf-8",
            )
            policy_path.write_text(
                'version = 1\nincludes = ["fragment.toml"]\n',
                encoding="utf-8",
            )

            result = self.run_helper(
                "set",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
                "--agent",
                "codex",
                "--credential",
                "codex_auth",
                "--source",
                str(source_path),
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("declared by an included policy fragment", result.stderr)

    def test_unset_rejects_credential_declared_in_included_fragment(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            fragment_path = root / "fragment.toml"
            managed_root = root / "credentials"
            fragment_path.write_text(
                'version = 1\n[credentials.codex_auth]\nsource = "/tmp/original.json"\n',
                encoding="utf-8",
            )
            policy_path.write_text(
                'version = 1\nincludes = ["fragment.toml"]\n',
                encoding="utf-8",
            )

            result = self.run_helper(
                "unset",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
                "--credential",
                "codex_auth",
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("declared by an included policy fragment", result.stderr)

    def test_set_source_rolls_back_managed_copy_when_policy_write_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            managed_root = root / "credentials"
            source_path = root / "auth.json"
            source_path.write_text('{"token":"next"}\n', encoding="utf-8")
            source_path.chmod(0o600)
            policy_path.write_text(
                'version = 1\nunexpected = "value"\n[credentials.codex_auth]\nsource = "/tmp/original.json"\n',
                encoding="utf-8",
            )

            result = self.run_helper(
                "set",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
                "--agent",
                "codex",
                "--credential",
                "codex_auth",
                "--source",
                str(source_path),
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertFalse((managed_root / "codex" / "auth.json").exists())
            self.assertFalse((managed_root / ".workcell-managed-root").exists())

    def test_status_rejects_missing_managed_source_file(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            managed_root = root / "credentials"
            source_path = root / "auth.json"
            source_path.write_text("{}\n", encoding="utf-8")
            source_path.chmod(0o600)

            self.run_helper(
                "init",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
            )
            self.run_helper(
                "set",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
                "--agent",
                "codex",
                "--credential",
                "codex_auth",
                "--source",
                str(source_path),
            )
            (managed_root / "codex" / "auth.json").unlink()

            status = self.run_helper(
                "status",
                "--policy",
                str(policy_path),
                "--agent",
                "codex",
                check=False,
            )

            self.assertNotEqual(status.returncode, 0)
            self.assertIn("does not exist", status.stderr)

    def test_status_without_policy_reports_resolution_states_none(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "missing-policy.toml"

            status = self.run_helper(
                "status",
                "--policy",
                str(policy_path),
            )

            self.assertIn("credential_resolution_states=none", status.stdout)

    def test_set_rejects_symlinked_managed_root_destination(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            managed_root = root / "credentials"
            managed_root.mkdir()
            escape_root = root / "escape"
            escape_root.mkdir()
            (managed_root / "codex").symlink_to(escape_root, target_is_directory=True)
            source_path = root / "auth.json"
            source_path.write_text("{}\n", encoding="utf-8")
            source_path.chmod(0o600)

            result = self.run_helper(
                "set",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
                "--agent",
                "codex",
                "--credential",
                "codex_auth",
                "--source",
                str(source_path),
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("must not be a symlink", result.stderr)
            self.assertFalse((escape_root / "auth.json").exists())

    def test_set_and_unset_remove_old_managed_copy_after_managed_root_migration(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            old_managed_root = root / "old-credentials"
            new_managed_root = root / "new-credentials"
            first_source = root / "first-auth.json"
            second_source = root / "second-auth.json"
            first_source.write_text('{"token":"old"}\n', encoding="utf-8")
            second_source.write_text('{"token":"new"}\n', encoding="utf-8")
            first_source.chmod(0o600)
            second_source.chmod(0o600)

            self.run_helper(
                "init",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(old_managed_root),
            )
            self.run_helper(
                "set",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(old_managed_root),
                "--agent",
                "codex",
                "--credential",
                "codex_auth",
                "--source",
                str(first_source),
            )
            self.assertTrue((old_managed_root / "codex" / "auth.json").is_file())

            self.run_helper(
                "init",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(new_managed_root),
            )
            self.run_helper(
                "set",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(new_managed_root),
                "--agent",
                "codex",
                "--credential",
                "codex_auth",
                "--source",
                str(second_source),
            )
            self.assertFalse((old_managed_root / "codex" / "auth.json").exists())
            self.assertTrue((new_managed_root / "codex" / "auth.json").is_file())

            self.run_helper(
                "unset",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(new_managed_root),
                "--credential",
                "codex_auth",
            )
            self.assertFalse((new_managed_root / "codex" / "auth.json").exists())

    def test_agentless_status_respects_mode_filter(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            policy_path.write_text(
                'version = 1\n[credentials.codex_auth]\nsource = "/tmp/auth.json"\nmodes = ["strict"]\n',
                encoding="utf-8",
            )

            status = self.run_helper(
                "status",
                "--policy",
                str(policy_path),
                "--mode",
                "build",
            )

            self.assertIn("credential_keys=none", status.stdout)

    def test_init_failure_does_not_create_managed_root_marker(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            managed_root = root / "credentials"
            policy_path.write_text('version = 1\nunexpected = "value"\n', encoding="utf-8")

            result = self.run_helper(
                "init",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
                check=False,
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertFalse((managed_root / ".workcell-managed-root").exists())

    def test_set_source_accepts_relative_paths_with_source_base(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "injection-policy.toml"
            managed_root = root / "credentials"
            source_path = root / "auth.json"
            source_path.write_text("{}\n", encoding="utf-8")
            source_path.chmod(0o600)

            self.run_helper(
                "init",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
            )
            self.run_helper(
                "set",
                "--policy",
                str(policy_path),
                "--managed-root",
                str(managed_root),
                "--agent",
                "codex",
                "--credential",
                "codex_auth",
                "--source",
                "./auth.json",
                "--source-base",
                str(root),
            )

            self.assertTrue((managed_root / "codex" / "auth.json").is_file())


if __name__ == "__main__":
    unittest.main()
