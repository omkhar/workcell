from __future__ import annotations

import json
import subprocess
import sys
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

    def test_load_policy_bundle_merges_includes_and_tracks_source_metadata(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            (root / "common.md").write_text("common\n", encoding="utf-8")
            (root / "auth.json").write_text("{}\n", encoding="utf-8")
            (root / "common.md").chmod(0o600)
            (root / "auth.json").chmod(0o600)
            (root / "shared.toml").write_text(
                '[documents]\ncommon = "common.md"\n',
                encoding="utf-8",
            )
            root_policy = root / "policy.toml"
            root_policy.write_text(
                'version = 1\nincludes = ["shared.toml"]\n[credentials]\ncodex_auth = "auth.json"\n',
                encoding="utf-8",
            )

            merged_policy, policy_sources = self.module.load_policy_bundle(root_policy)

            self.assertEqual(
                merged_policy["documents"]["common"],
                str((root / "common.md").resolve()),
            )
            self.assertEqual(merged_policy["credentials"]["codex_auth"], "auth.json")
            self.assertEqual(
                [entry["path"] for entry in policy_sources],
                ["shared.toml", "policy.toml"],
            )
            self.assertTrue(
                self.module.composite_policy_sha256(policy_sources).startswith("sha256:")
            )

    def test_policy_sha256_tracks_effective_injected_material(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "policy.toml"
            bundle_a = root / "bundle-a"
            bundle_b = root / "bundle-b"
            (root / "common.md").write_text("common\n", encoding="utf-8")
            (root / "auth.json").write_text('{"token":"one"}\n', encoding="utf-8")
            (root / "auth.json").chmod(0o600)
            policy_path.write_text(
                'version = 1\n[documents]\ncommon = "common.md"\n[credentials]\ncodex_auth = "auth.json"\n',
                encoding="utf-8",
            )

            subprocess.run(
                [
                    sys.executable,
                    str(Path(self.module.__file__).resolve()),
                    "--policy",
                    str(policy_path),
                    "--agent",
                    "codex",
                    "--mode",
                    "strict",
                    "--output-root",
                    str(bundle_a),
                ],
                check=True,
            )
            manifest_a = json.loads((bundle_a / "manifest.json").read_text(encoding="utf-8"))

            (root / "auth.json").write_text('{"token":"two"}\n', encoding="utf-8")
            subprocess.run(
                [
                    sys.executable,
                    str(Path(self.module.__file__).resolve()),
                    "--policy",
                    str(policy_path),
                    "--agent",
                    "codex",
                    "--mode",
                    "strict",
                    "--output-root",
                    str(bundle_b),
                ],
                check=True,
            )
            manifest_b = json.loads((bundle_b / "manifest.json").read_text(encoding="utf-8"))

            self.assertNotEqual(
                manifest_a["metadata"]["policy_sha256"],
                manifest_b["metadata"]["policy_sha256"],
            )

    def test_policy_metadata_override_preserves_original_entrypoint(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            credential_path = root / "auth.json"
            credential_path.write_text('{"token":"one"}\n', encoding="utf-8")
            credential_path.chmod(0o600)

            override_path = root / "policy-metadata.json"
            override_path.write_text(
                json.dumps(
                    {
                        "policy_entrypoint": "policy.toml",
                        "policy_sources": [
                            {
                                "path": "policy.toml",
                                "sha256": "sha256:original",
                            }
                        ],
                    },
                    sort_keys=True,
                )
                + "\n",
                encoding="utf-8",
            )

            manifests = []
            for name in ("resolved-a.toml", "resolved-b.toml"):
                policy_path = root / name
                bundle_root = root / f"bundle-{name}"
                policy_path.write_text(
                    f'version = 1\n[credentials.codex_auth]\nsource = "{credential_path}"\n',
                    encoding="utf-8",
                )
                subprocess.run(
                    [
                        sys.executable,
                        str(Path(self.module.__file__).resolve()),
                        "--policy",
                        str(policy_path),
                        "--policy-metadata",
                        str(override_path),
                        "--agent",
                        "codex",
                        "--mode",
                        "strict",
                        "--output-root",
                        str(bundle_root),
                    ],
                    check=True,
                )
                manifests.append(
                    json.loads((bundle_root / "manifest.json").read_text(encoding="utf-8"))
                )

            self.assertEqual(
                manifests[0]["metadata"]["policy_entrypoint"],
                "policy.toml",
            )
            self.assertEqual(
                manifests[0]["metadata"]["policy_sources"][0]["path"],
                "policy.toml",
            )
            self.assertEqual(
                manifests[0]["metadata"]["policy_sha256"],
                manifests[1]["metadata"]["policy_sha256"],
            )

    def test_load_policy_bundle_rejects_parent_escape_includes(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            nested = root / "nested"
            nested.mkdir()
            (root / "outside.toml").write_text('[documents]\ncommon = "common.md"\n', encoding="utf-8")
            (nested / "policy.toml").write_text(
                'version = 1\nincludes = ["../outside.toml"]\n',
                encoding="utf-8",
            )

            with self.assertRaises(SystemExit) as excinfo:
                self.module.load_policy_bundle(nested / "policy.toml")
            self.assertIn("must stay within", str(excinfo.exception))

    def test_load_policy_bundle_rejects_non_list_and_duplicate_includes(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            (root / "shared.toml").write_text('[documents]\ncommon = "common.md"\n', encoding="utf-8")
            (root / "common.md").write_text("common\n", encoding="utf-8")
            (root / "policy-invalid.toml").write_text(
                'version = 1\nincludes = "shared.toml"\n',
                encoding="utf-8",
            )
            (root / "policy-duplicate.toml").write_text(
                'version = 1\nincludes = ["shared.toml", "shared.toml"]\n',
                encoding="utf-8",
            )

            with self.assertRaises(SystemExit) as excinfo:
                self.module.load_policy_bundle(root / "policy-invalid.toml")
            self.assertIn("includes must be an array of strings", str(excinfo.exception))

            with self.assertRaises(SystemExit) as excinfo:
                self.module.load_policy_bundle(root / "policy-duplicate.toml")
            self.assertIn("includes the same file more than once", str(excinfo.exception))

    def test_load_policy_bundle_rebases_included_fragment_paths_to_fragment_directory(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            fragments = root / "fragments"
            fragments.mkdir()
            (fragments / "common.md").write_text("common\n", encoding="utf-8")
            (fragments / "copy.txt").write_text("copy\n", encoding="utf-8")
            (fragments / "auth.json").write_text("{}\n", encoding="utf-8")
            (fragments / "ssh_config").write_text("Host github.com\n", encoding="utf-8")
            (fragments / "id_workcell").write_text("private\n", encoding="utf-8")
            (fragments / "id_workcell").chmod(0o600)
            (fragments / "fragment.toml").write_text(
                '[documents]\n'
                'common = "common.md"\n'
                '[[copies]]\n'
                'source = "copy.txt"\n'
                'target = "/state/injected/copy.txt"\n'
                'classification = "public"\n'
                '[ssh]\n'
                'enabled = true\n'
                'config = "ssh_config"\n'
                'identities = ["id_workcell"]\n'
                '[credentials]\n'
                'codex_auth = "auth.json"\n',
                encoding="utf-8",
            )
            root_policy = root / "policy.toml"
            root_policy.write_text(
                'version = 1\nincludes = ["fragments/fragment.toml"]\n',
                encoding="utf-8",
            )

            merged_policy, _ = self.module.load_policy_bundle(root_policy)

            self.assertEqual(
                merged_policy["documents"]["common"],
                str((fragments / "common.md").resolve()),
            )
            self.assertEqual(
                merged_policy["copies"][0]["source"],
                str((fragments / "copy.txt").resolve()),
            )
            self.assertEqual(
                merged_policy["ssh"]["config"],
                str((fragments / "ssh_config").resolve()),
            )
            self.assertEqual(
                merged_policy["ssh"]["identities"][0],
                str((fragments / "id_workcell").resolve()),
            )
            self.assertEqual(
                merged_policy["credentials"]["codex_auth"],
                str((fragments / "auth.json").resolve()),
            )

    def test_rebase_policy_fragment_rebases_all_supported_path_fields(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            fragment_dir = root / "fragment"
            fragment_dir.mkdir()

            rebased = self.module.rebase_policy_fragment(
                {
                    "documents": {"common": "common.md"},
                    "copies": [{"source": "copy.txt", "target": "/state/injected/copy.txt"}, "literal"],
                    "ssh": {
                        "config": "ssh_config",
                        "known_hosts": "known_hosts",
                        "identities": ["id_workcell"],
                    },
                    "credentials": {
                        "codex_auth": "auth.json",
                        "github_hosts": {"source": "hosts.yml", "providers": ["codex"]},
                    },
                    "version": 1,
                },
                fragment_dir,
            )
            expected = lambda value: str(self.module.expand_host_path(value, fragment_dir))

            self.assertEqual(
                rebased["documents"]["common"],
                expected("common.md"),
            )
            self.assertEqual(
                rebased["copies"][0]["source"],
                expected("copy.txt"),
            )
            self.assertEqual(rebased["copies"][1], "literal")
            self.assertEqual(
                rebased["ssh"]["config"],
                expected("ssh_config"),
            )
            self.assertEqual(
                rebased["ssh"]["known_hosts"],
                expected("known_hosts"),
            )
            self.assertEqual(
                rebased["ssh"]["identities"][0],
                expected("id_workcell"),
            )
            self.assertEqual(
                rebased["credentials"]["codex_auth"],
                expected("auth.json"),
            )
            self.assertEqual(
                rebased["credentials"]["github_hosts"]["source"],
                expected("hosts.yml"),
            )

    def test_path_material_sha256_covers_file_directory_and_symlink_inputs(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            file_path = root / "file.txt"
            dir_path = root / "tree"
            symlink_path = root / "link.txt"
            file_path.write_text("file\n", encoding="utf-8")
            dir_path.mkdir()
            (dir_path / "nested.txt").write_text("nested\n", encoding="utf-8")
            symlink_path.symlink_to(file_path.name)

            file_digest = self.module.path_material_sha256(file_path)
            dir_digest = self.module.path_material_sha256(dir_path)
            symlink_digest = self.module.path_material_sha256(symlink_path)

            self.assertEqual(file_digest, self.module.path_material_sha256(file_path))
            self.assertEqual(dir_digest, self.module.path_material_sha256(dir_path))
            self.assertEqual(symlink_digest, self.module.path_material_sha256(symlink_path))
            self.assertNotEqual(file_digest, dir_digest)
            self.assertNotEqual(file_digest, symlink_digest)

    def test_effective_policy_sha256_tracks_documents_copies_credentials_and_ssh(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            output = root / "bundle"
            output.mkdir()
            (output / "documents").mkdir()
            (output / "copies").mkdir()
            (output / "documents/common.md").write_text("common\n", encoding="utf-8")
            (output / "copies/0").write_text("public\n", encoding="utf-8")
            secret_copy = root / "secret-copy.txt"
            credential = root / "auth.json"
            ssh_config = root / "ssh_config"
            known_hosts = root / "known_hosts"
            identity = root / "id_workcell"
            secret_copy.write_text("secret\n", encoding="utf-8")
            credential.write_text('{"token":"one"}\n', encoding="utf-8")
            ssh_config.write_text("Host github.com\n", encoding="utf-8")
            known_hosts.write_text("github.com ssh-ed25519 AAAA\n", encoding="utf-8")
            identity.write_text("private\n", encoding="utf-8")
            credential.chmod(0o600)
            ssh_config.chmod(0o600)
            identity.chmod(0o600)

            digest_before = self.module.effective_policy_sha256(
                [{"path": "policy.toml", "sha256": "sha256:policy"}],
                output,
                {"common": "documents/common.md"},
                [
                    {
                        "source": "copies/0",
                        "target": "/state/injected/public.txt",
                        "kind": "file",
                        "file_mode": "0644",
                        "dir_mode": "0755",
                        "classification": "public",
                    },
                    {
                        "source": {
                            "source": str(secret_copy),
                            "mount_path": "/opt/workcell/host-inputs/copies/1",
                        },
                        "target": "~/.config/workcell/token.txt",
                        "kind": "file",
                        "file_mode": "0600",
                        "dir_mode": "0700",
                        "classification": "secret",
                    },
                ],
                {
                    "codex_auth": {
                        "source": str(credential),
                        "mount_path": "/opt/workcell/host-inputs/credentials/codex-auth.json",
                    }
                },
                {
                    "config_assurance": "safe",
                    "config": {
                        "source": str(ssh_config),
                        "mount_path": "/opt/workcell/host-inputs/ssh/config",
                    },
                    "known_hosts": {
                        "source": str(known_hosts),
                        "mount_path": "/opt/workcell/host-inputs/ssh/known_hosts",
                    },
                    "identities": [
                        {
                            "source": str(identity),
                            "mount_path": "/opt/workcell/host-inputs/ssh/identity-0",
                            "target_name": "id_workcell",
                        }
                    ],
                },
            )

            secret_copy.write_text("secret-two\n", encoding="utf-8")
            digest_after = self.module.effective_policy_sha256(
                [{"path": "policy.toml", "sha256": "sha256:policy"}],
                output,
                {"common": "documents/common.md"},
                [
                    {
                        "source": "copies/0",
                        "target": "/state/injected/public.txt",
                        "kind": "file",
                        "file_mode": "0644",
                        "dir_mode": "0755",
                        "classification": "public",
                    },
                    {
                        "source": {
                            "source": str(secret_copy),
                            "mount_path": "/opt/workcell/host-inputs/copies/1",
                        },
                        "target": "~/.config/workcell/token.txt",
                        "kind": "file",
                        "file_mode": "0600",
                        "dir_mode": "0700",
                        "classification": "secret",
                    },
                ],
                {
                    "codex_auth": {
                        "source": str(credential),
                        "mount_path": "/opt/workcell/host-inputs/credentials/codex-auth.json",
                    }
                },
                {
                    "config_assurance": "safe",
                    "config": {
                        "source": str(ssh_config),
                        "mount_path": "/opt/workcell/host-inputs/ssh/config",
                    },
                    "known_hosts": {
                        "source": str(known_hosts),
                        "mount_path": "/opt/workcell/host-inputs/ssh/known_hosts",
                    },
                    "identities": [
                        {
                            "source": str(identity),
                            "mount_path": "/opt/workcell/host-inputs/ssh/identity-0",
                            "target_name": "id_workcell",
                        }
                    ],
                },
            )

            self.assertNotEqual(digest_before, digest_after)

    def test_reserved_targets_cover_managed_provider_state(self) -> None:
        for target in (
            "~/.mcp.json",
            "~/.gemini/projects.json",
            "~/.gemini/trustedFolders.json",
            "~/.claude/.claude.json",
            "~/.claude.json",
            "~/.claude/.credentials.json",
        ):
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
            env_file.write_text(
                'export GOOGLE_GENAI_USE_VERTEXAI=true\n'
                'GOOGLE_CLOUD_PROJECT=test-project\n'
                'GOOGLE_CLOUD_LOCATION="us-central1" # comment\n',
                encoding="utf-8",
            )
            env_file.chmod(0o600)

            rendered = self.module.render_credentials(
                {"credentials": {"gemini_env": "gemini.env"}},
                root,
                "gemini",
                "strict",
            )

            self.assertEqual(
                self.module.derive_credential_extra_endpoints(rendered),
                ["aiplatform.googleapis.com:443", "us-central1-aiplatform.googleapis.com:443"],
            )

    def test_derive_credential_extra_endpoints_adds_google_auth_endpoints_for_gca(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            env_file = root / "gemini.env"
            env_file.write_text("GOOGLE_GENAI_USE_GCA=true\n", encoding="utf-8")
            env_file.chmod(0o600)

            rendered = self.module.render_credentials(
                {"credentials": {"gemini_env": "gemini.env"}},
                root,
                "gemini",
                "strict",
            )

            self.assertEqual(
                self.module.derive_credential_extra_endpoints(rendered),
                [
                    "accounts.google.com:443",
                    "oauth2.googleapis.com:443",
                    "sts.googleapis.com:443",
                ],
            )

    def test_render_credentials_rejects_invalid_gemini_inputs(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            invalid_env = root / "gemini.env"
            invalid_env.write_text("GOOGLE_GENAI_USE_VERTEXAI true\n", encoding="utf-8")
            invalid_env.chmod(0o600)
            invalid_oauth = root / "gemini-oauth.json"
            invalid_oauth.write_text("[]\n", encoding="utf-8")
            invalid_oauth.chmod(0o600)
            invalid_adc = root / "gcloud-adc.json"
            invalid_adc.write_text("{}\n", encoding="utf-8")
            invalid_adc.chmod(0o600)
            invalid_projects = root / "gemini-projects.json"
            invalid_projects.write_text('{"projects":[]}\n', encoding="utf-8")
            invalid_projects.chmod(0o600)

            with self.assertRaises(SystemExit):
                self.module.render_credentials(
                    {"credentials": {"gemini_env": "gemini.env"}},
                    root,
                    "gemini",
                    "strict",
                )
            with self.assertRaises(SystemExit):
                self.module.render_credentials(
                    {"credentials": {"gemini_oauth": "gemini-oauth.json"}},
                    root,
                    "gemini",
                    "strict",
                )
            with self.assertRaises(SystemExit):
                self.module.render_credentials(
                    {"credentials": {"gcloud_adc": "gcloud-adc.json"}},
                    root,
                    "gemini",
                    "strict",
                )
            with self.assertRaises(SystemExit):
                self.module.render_credentials(
                    {"credentials": {"gemini_projects": "gemini-projects.json"}},
                    root,
                    "gemini",
                    "strict",
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
