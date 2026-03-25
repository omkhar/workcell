from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from test_support import load_module


class RenderInjectionBundleTests(unittest.TestCase):
    def setUp(self) -> None:
        self.module = load_module("scripts/lib/render_injection_bundle.py")

    def test_render_credentials_supports_claude_and_gemini_state(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            (root / "claude-auth.json").write_text('{"token":"claude"}\n', encoding="utf-8")
            (root / "claude-mcp.json").write_text('{"mcpServers":{}}\n', encoding="utf-8")
            (root / "gemini-projects.json").write_text('{"projects":{}}\n', encoding="utf-8")
            (root / "claude-auth.json").chmod(0o600)
            (root / "claude-mcp.json").chmod(0o600)
            (root / "gemini-projects.json").chmod(0o600)

            policy = {
                "credentials": {
                    "claude_auth": "claude-auth.json",
                    "claude_mcp": "claude-mcp.json",
                    "gemini_projects": "gemini-projects.json",
                }
            }

            claude_rendered = self.module.render_credentials(policy, root, "claude", "strict")
            gemini_rendered = self.module.render_credentials(policy, root, "gemini", "strict")

            self.assertEqual(
                claude_rendered["claude_auth"]["mount_path"],
                "/opt/workcell/host-inputs/credentials/claude-auth.json",
            )
            self.assertEqual(
                claude_rendered["claude_mcp"]["mount_path"],
                "/opt/workcell/host-inputs/credentials/claude-mcp.json",
            )
            self.assertEqual(
                gemini_rendered["gemini_projects"]["mount_path"],
                "/opt/workcell/host-inputs/credentials/gemini-projects.json",
            )

    def test_reserved_targets_cover_managed_provider_state(self) -> None:
        for target in ("~/.mcp.json", "~/.gemini/projects.json", "~/.config/claude-code/auth.json"):
            with self.assertRaises(SystemExit):
                candidate = self.module.normalize_container_target(target)
                self.module.validate_container_target(candidate)

    def test_render_copies_preserves_public_bundle_and_secret_direct_mounts(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            output = root / "bundle"
            output.mkdir()
            (root / "public.txt").write_text("public\n", encoding="utf-8")
            (root / "secret.txt").write_text("secret\n", encoding="utf-8")
            (root / "secret.txt").chmod(0o600)

            policy = {
                "copies": [
                    {
                        "source": "public.txt",
                        "target": "/state/injected/public.txt",
                        "classification": "public",
                    },
                    {
                        "source": "secret.txt",
                        "target": "~/.config/workcell/token.txt",
                        "classification": "secret",
                    },
                ]
            }

            rendered = self.module.render_copies(policy, output, root, "codex", "strict")

            self.assertEqual(rendered[0]["source"], "copies/0")
            self.assertTrue((output / "copies/0").is_file())
            self.assertEqual(
                rendered[1]["source"]["mount_path"],
                "/opt/workcell/host-inputs/copies/1",
            )
            self.assertFalse((output / "copies/1").exists())

    def test_render_ssh_rejects_duplicate_identity_basenames(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            (root / "a").mkdir()
            (root / "b").mkdir()
            (root / "a/id_shared").write_text("a\n", encoding="utf-8")
            (root / "b/id_shared").write_text("b\n", encoding="utf-8")

            policy = {
                "ssh": {
                    "enabled": True,
                    "identities": ["a/id_shared", "b/id_shared"],
                }
            }

            with self.assertRaises(SystemExit):
                self.module.render_ssh(policy, root / "bundle", root, "codex", "strict")

    def test_render_documents_stages_common_and_agent_specific_files(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            output = root / "bundle"
            output.mkdir()
            (root / "common.md").write_text("common\n", encoding="utf-8")
            (root / "codex.md").write_text("codex\n", encoding="utf-8")

            rendered = self.module.render_documents(
                {
                    "documents": {
                        "common": "common.md",
                        "codex": "codex.md",
                    }
                },
                output,
                root,
            )

            self.assertEqual(rendered["common"], "documents/common.md")
            self.assertEqual(rendered["codex"], "documents/codex.md")
            self.assertEqual(
                (output / "documents/common.md").read_text(encoding="utf-8"),
                "common\n",
            )

    def test_render_copies_respects_provider_and_mode_selection(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            output = root / "bundle"
            output.mkdir()
            (root / "shared.txt").write_text("shared\n", encoding="utf-8")
            (root / "codex-only.txt").write_text("codex\n", encoding="utf-8")

            rendered = self.module.render_copies(
                {
                    "copies": [
                        {
                            "source": "shared.txt",
                            "target": "/state/injected/shared.txt",
                            "classification": "public",
                            "providers": ["claude"],
                        },
                        {
                            "source": "codex-only.txt",
                            "target": "/state/injected/codex-only.txt",
                            "classification": "public",
                            "providers": ["codex"],
                            "modes": ["strict"],
                        },
                    ]
                },
                output,
                root,
                "codex",
                "strict",
            )

            self.assertEqual(len(rendered), 1)
            self.assertEqual(rendered[0]["target"], "/state/injected/codex-only.txt")

    def test_parse_toml_subset_rejects_unsupported_tables(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            policy_path = Path(tmpdir) / "policy.toml"
            with self.assertRaises(SystemExit):
                self.module.parse_toml_subset("[unsupported]\nfoo = \"bar\"\n", policy_path)

    def test_selected_for_rejects_invalid_values(self) -> None:
        with self.assertRaises(SystemExit):
            self.module.selected_for(["codex", "invalid"], "codex", "providers", {"codex"})

    def test_render_ssh_respects_provider_filtering(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            output = root / "bundle"
            output.mkdir()
            (root / "id_agent").write_text("key\n", encoding="utf-8")
            (root / "id_agent").chmod(0o600)

            rendered = self.module.render_ssh(
                {
                    "ssh": {
                        "enabled": True,
                        "identities": ["id_agent"],
                        "providers": ["claude"],
                    }
                },
                output,
                root,
                "codex",
                "strict",
            )

            self.assertEqual(rendered, {})

    def test_render_credentials_respects_provider_and_mode_selection(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            auth = root / "github-hosts.yml"
            auth.write_text("github.com:\n", encoding="utf-8")
            auth.chmod(0o600)

            rendered = self.module.render_credentials(
                {
                    "credentials": {
                        "github_hosts": {
                            "source": "github-hosts.yml",
                            "providers": ["claude"],
                            "modes": ["build"],
                        }
                    }
                },
                root,
                "codex",
                "strict",
            )

            self.assertEqual(rendered, {})

    def test_render_credentials_accepts_legacy_scalar_github_auth_and_requires_providers_for_tables(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            auth = root / "github-hosts.yml"
            auth.write_text("github.com:\n", encoding="utf-8")
            auth.chmod(0o600)

            rendered = self.module.render_credentials(
                {"credentials": {"github_hosts": "github-hosts.yml"}},
                root,
                "codex",
                "strict",
            )
            self.assertEqual(
                rendered["github_hosts"]["mount_path"],
                "/opt/workcell/host-inputs/credentials/github-hosts.yml",
            )

            with self.assertRaises(SystemExit):
                self.module.render_credentials(
                    {
                        "credentials": {
                            "github_hosts": {
                                "source": "github-hosts.yml",
                            }
                        }
                    },
                    root,
                    "codex",
                    "strict",
                )

    def test_derive_credential_extra_endpoints_adds_vertex_region_from_gemini_env(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            env_file = root / "gemini.env"
            env_file.write_text('export GOOGLE_CLOUD_LOCATION="us-central1"\n', encoding="utf-8")
            env_file.chmod(0o600)

            rendered = self.module.render_credentials(
                {"credentials": {"gemini_env": "gemini.env"}},
                root,
                "gemini",
                "strict",
            )

            self.assertEqual(
                self.module.derive_credential_extra_endpoints(rendered),
                ["us-central1-aiplatform.googleapis.com:443"],
            )

    def test_render_ssh_rejects_unsafe_config_without_opt_in(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            output = root / "bundle"
            output.mkdir()
            config = root / "ssh-config"
            config.write_text("ProxyCommand nc %h %p\n", encoding="utf-8")
            config.chmod(0o600)

            with self.assertRaises(SystemExit):
                self.module.render_ssh(
                    {"ssh": {"enabled": True, "config": "ssh-config"}},
                    output,
                    root,
                    "codex",
                    "strict",
                )

    def test_render_ssh_rejects_other_exec_capable_directives_without_opt_in(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            output = root / "bundle"
            output.mkdir()
            config = root / "ssh-config"
            config.write_text("KnownHostsCommand /bin/true\n", encoding="utf-8")
            config.chmod(0o600)

            with self.assertRaises(SystemExit):
                self.module.render_ssh(
                    {"ssh": {"enabled": True, "config": "ssh-config"}},
                    output,
                    root,
                    "codex",
                    "strict",
                )

    def test_render_credentials_rejects_world_readable_files(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            auth = root / "auth.json"
            auth.write_text("{}\n", encoding="utf-8")
            auth.chmod(0o644)

            with self.assertRaises(SystemExit):
                self.module.render_credentials(
                    {"credentials": {"codex_auth": "auth.json"}},
                    root,
                    "codex",
                    "strict",
                )

    def test_render_credentials_rejects_symlink_sources(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            auth = root / "auth.json"
            auth.write_text("{}\n", encoding="utf-8")
            auth.chmod(0o600)
            link = root / "auth-link.json"
            link.symlink_to(auth)

            with self.assertRaises(SystemExit):
                self.module.render_credentials(
                    {"credentials": {"codex_auth": "auth-link.json"}},
                    root,
                    "codex",
                    "strict",
                )

    def test_render_credentials_rejects_symlinked_parent_directories(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            real_dir = root / "real"
            real_dir.mkdir()
            auth = real_dir / "auth.json"
            auth.write_text("{}\n", encoding="utf-8")
            auth.chmod(0o600)
            link_dir = root / "linked"
            link_dir.symlink_to(real_dir, target_is_directory=True)

            with self.assertRaises(SystemExit):
                self.module.render_credentials(
                    {"credentials": {"codex_auth": "linked/auth.json"}},
                    root,
                    "codex",
                    "strict",
                )

    def test_render_credentials_rejects_absolute_paths_with_symlinked_parents_under_policy_root(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            real_dir = root / "real"
            real_dir.mkdir()
            nested_dir = real_dir / "sub"
            nested_dir.mkdir()
            auth = nested_dir / "auth.json"
            auth.write_text("{}\n", encoding="utf-8")
            auth.chmod(0o600)
            link_dir = root / "linked"
            link_dir.symlink_to(real_dir, target_is_directory=True)

            with self.assertRaises(SystemExit):
                self.module.render_credentials(
                    {"credentials": {"codex_auth": str(link_dir / "sub/auth.json")}},
                    root,
                    "codex",
                    "strict",
                )

    def test_render_credentials_rejects_absolute_paths_with_symlinked_parents_outside_policy_root(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            base = Path(tmpdir)
            policy_root = base / "policy"
            policy_root.mkdir()
            outside_root = base / "outside"
            real_dir = outside_root / "real"
            real_dir.mkdir(parents=True)
            nested_dir = real_dir / "sub"
            nested_dir.mkdir()
            auth = nested_dir / "auth.json"
            auth.write_text("{}\n", encoding="utf-8")
            auth.chmod(0o600)
            link_dir = outside_root / "linked"
            link_dir.symlink_to(real_dir, target_is_directory=True)

            with self.assertRaises(SystemExit):
                self.module.render_credentials(
                    {"credentials": {"codex_auth": str(link_dir / "sub/auth.json")}},
                    policy_root,
                    "codex",
                    "strict",
                )

    def test_render_ssh_allows_standard_known_hosts_permissions(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            known_hosts = root / "known_hosts"
            known_hosts.write_text("github.com ssh-ed25519 AAAA\n", encoding="utf-8")
            known_hosts.chmod(0o644)

            rendered = self.module.render_ssh(
                {"ssh": {"enabled": True, "known_hosts": "known_hosts"}},
                root / "bundle",
                root,
                "codex",
                "strict",
            )

            self.assertEqual(
                rendered["known_hosts"]["mount_path"],
                "/opt/workcell/host-inputs/ssh/known_hosts",
            )

    def test_render_ssh_marks_safe_and_unsafe_config_assurance(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            output = root / "bundle"
            output.mkdir()
            config = root / "ssh-config"
            config.write_text("Host github.com\n  IdentityFile ~/.ssh/id_ed25519\n", encoding="utf-8")
            config.chmod(0o600)
            unsafe_config = root / "unsafe-ssh-config"
            unsafe_config.write_text("ProxyCommand nc %h %p\n", encoding="utf-8")
            unsafe_config.chmod(0o600)

            safe_rendered = self.module.render_ssh(
                {"ssh": {"enabled": True, "config": "ssh-config"}},
                output,
                root,
                "codex",
                "strict",
            )
            unsafe_rendered = self.module.render_ssh(
                {
                    "ssh": {
                        "enabled": True,
                        "config": "unsafe-ssh-config",
                        "allow_unsafe_config": True,
                    }
                },
                output,
                root,
                "codex",
                "strict",
            )

            self.assertEqual(safe_rendered["config_assurance"], "safe")
            self.assertEqual(
                unsafe_rendered["config_assurance"],
                "lower-assurance-unsafe-config",
            )


if __name__ == "__main__":
    unittest.main()
