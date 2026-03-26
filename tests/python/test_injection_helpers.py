from __future__ import annotations

import json
import os
import sys
import tempfile
import unittest
from pathlib import Path, PurePosixPath
from unittest import mock

from test_support import load_module


class RenderInjectionHelperTests(unittest.TestCase):
    def setUp(self) -> None:
        self.module = load_module("scripts/lib/render_injection_bundle.py")
        self.extract_module = load_module("scripts/lib/extract_direct_mounts.py")

    def test_strip_comment_preserves_quoted_hashes(self) -> None:
        self.assertEqual(
            self.module.strip_comment('value = "keep # hash" # remove'),
            'value = "keep # hash"',
        )
        self.assertEqual(
            self.module.strip_comment(r'value = "escaped \"#\" hash" # remove'),
            r'value = "escaped \"#\" hash"',
        )

    def test_parse_value_supports_supported_scalar_types(self) -> None:
        policy = Path("/tmp/policy.toml")
        self.assertTrue(self.module.parse_value("true", policy, 1))
        self.assertFalse(self.module.parse_value("false", policy, 2))
        self.assertEqual(self.module.parse_value('"text"', policy, 3), "text")
        self.assertEqual(self.module.parse_value('["a", "b"]', policy, 4), ["a", "b"])
        self.assertEqual(self.module.parse_value("42", policy, 5), 42)

    def test_parse_value_rejects_unsupported_types(self) -> None:
        policy = Path("/tmp/policy.toml")
        with self.assertRaises(SystemExit):
            self.module.parse_value("1.5", policy, 1)
        with self.assertRaises(SystemExit):
            self.module.parse_value("[1, 2]", policy, 2)
        with self.assertRaises(SystemExit):
            self.module.parse_value("", policy, 3)

    def test_parse_toml_subset_parses_supported_tables(self) -> None:
        policy = Path("/tmp/policy.toml")
        parsed = self.module.parse_toml_subset(
            """
            version = 1
            [documents]
            common = "common.md"
            [credentials]
            codex_auth = "auth.json"
            [ssh]
            enabled = true
            identities = ["id_ed25519"]
            [[copies]]
            source = "file.txt"
            target = "/state/injected/file.txt"
            classification = "public"
            """,
            policy,
        )
        self.assertEqual(parsed["version"], 1)
        self.assertEqual(parsed["documents"]["common"], "common.md")
        self.assertEqual(parsed["credentials"]["codex_auth"], "auth.json")
        self.assertTrue(parsed["ssh"]["enabled"])
        self.assertEqual(len(parsed["copies"]), 1)

    def test_parse_toml_subset_parses_scoped_credential_table(self) -> None:
        policy = Path("/tmp/policy.toml")
        parsed = self.module.parse_toml_subset(
            """
            [credentials.codex_auth]
            source = "auth.json"
            providers = ["codex"]
            modes = ["strict"]
            """,
            policy,
        )
        self.assertEqual(parsed["credentials"]["codex_auth"]["source"], "auth.json")
        self.assertEqual(parsed["credentials"]["codex_auth"]["providers"], ["codex"])

    def test_parse_toml_subset_rejects_invalid_lines(self) -> None:
        policy = Path("/tmp/policy.toml")
        with self.assertRaises(SystemExit):
            self.module.parse_toml_subset("[[ssh]]\nfoo = \"bar\"\n", policy)
        with self.assertRaises(SystemExit):
            self.module.parse_toml_subset("= \"bar\"\n", policy)
        with self.assertRaises(SystemExit):
            self.module.parse_toml_subset("nope\n", policy)

    def test_path_and_target_validation_helpers(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            base = Path(tmpdir)
            relative = self.module.expand_host_path("child/file.txt", base)
            self.assertEqual(
                relative,
                Path(os.path.abspath(os.fspath(base / "child/file.txt"))),
            )
            target = base / "target.txt"
            target.write_text("target\n", encoding="utf-8")
            link = base / "link.txt"
            link.symlink_to(target)
            self.assertEqual(self.module.expand_host_path("link.txt", base), link)
            candidate = self.module.normalize_container_target("~/test.txt")
            self.assertEqual(
                candidate,
                PurePosixPath("/state/agent-home/test.txt"),
            )
            self.assertTrue(
                self.module.target_is_under(
                    PurePosixPath("/state/agent-home/.config"),
                    PurePosixPath("/state/agent-home"),
                )
            )
            self.assertTrue(
                self.module.target_is_under(
                    PurePosixPath("/state/agent-home"),
                    PurePosixPath("/state/agent-home"),
                )
            )
            self.assertTrue(
                self.module.target_is_reserved(
                    PurePosixPath("/state/agent-home/.codex/rules/default.rules")
                )
            )
            self.assertEqual(
                self.module.validate_container_target(
                    PurePosixPath("/state/injected/notes.txt")
                ),
                "/state/injected/notes.txt",
            )
            with self.assertRaises(SystemExit):
                self.module.normalize_container_target("relative.txt")
            with self.assertRaises(SystemExit):
                self.module.normalize_container_target("/state/agent-home/../escape")
            with self.assertRaises(SystemExit):
                self.module.validate_container_target(PurePosixPath("/tmp/outside"))

    def test_selection_and_key_validation_helpers(self) -> None:
        self.assertTrue(
            self.module.selected_for(None, "codex", "providers", {"codex", "claude"})
        )
        self.assertTrue(
            self.module.selected_for(["codex"], "codex", "providers", {"codex"})
        )
        with self.assertRaises(SystemExit):
            self.module.selected_for([], "codex", "providers", {"codex"})
        with self.assertRaises(SystemExit):
            self.module.selected_for(["invalid"], "codex", "providers", {"codex"})
        with self.assertRaises(SystemExit):
            self.module.selected_for(["codex", 7], "codex", "providers", {"codex"})
        self.module.validate_allowed_keys({"a": 1}, {"a"}, "table")
        with self.assertRaises(SystemExit):
            self.module.validate_allowed_keys({"a": 1, "b": 2}, {"a"}, "table")

    def test_copy_source_and_stage_file_handle_files_and_directories(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            source_file = root / "source.txt"
            source_file.write_text("hello\n", encoding="utf-8")
            source_file.chmod(0o600)
            staged_file = root / "bundle/file.txt"
            self.assertEqual(self.module.copy_source(source_file, staged_file), "file")
            self.assertEqual(staged_file.read_text(encoding="utf-8"), "hello\n")
            self.assertEqual(oct(staged_file.stat().st_mode & 0o777), "0o600")

            source_dir = root / "dir-source"
            source_dir.mkdir()
            source_dir.chmod(0o700)
            (source_dir / "nested.txt").write_text("nested\n", encoding="utf-8")
            (source_dir / "nested.txt").chmod(0o600)
            staged_dir = root / "bundle/dir"
            self.assertEqual(self.module.copy_source(source_dir, staged_dir), "dir")
            self.assertTrue((staged_dir / "nested.txt").is_file())
            self.assertEqual(oct(staged_dir.stat().st_mode & 0o777), "0o700")

            output_root = root / "stage"
            output_root.mkdir()
            relpath = self.module.stage_file(source_file, output_root, "documents/common.md")
            self.assertEqual(relpath, "documents/common.md")
            self.assertTrue((output_root / relpath).is_file())

    def test_copy_source_rejects_symlinks_and_missing_sources(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            source_dir = root / "dir-source"
            source_dir.mkdir()
            (source_dir / "target.txt").write_text("target\n", encoding="utf-8")
            (source_dir / "link.txt").symlink_to(source_dir / "target.txt")
            with self.assertRaises(SystemExit):
                self.module.ensure_no_symlinks_within(source_dir)
            with self.assertRaises(SystemExit):
                self.module.copy_source(root / "missing.txt", root / "dest")

    def test_validate_source_path_and_load_policy(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "policy.toml"
            source = root / "common.md"
            source.write_text("common\n", encoding="utf-8")
            source.chmod(0o600)
            self.assertEqual(
                self.module.expand_host_path(str(source), root),
                Path(os.path.abspath(os.fspath(source))),
            )
            policy_path.write_text(
                'version = 1\n[documents]\ncommon = "common.md"\n',
                encoding="utf-8",
            )
            loaded = self.module.load_policy(policy_path)
            self.assertEqual(loaded["documents"]["common"], "common.md")
            self.assertEqual(
                self.module.validate_source_path("common.md", "documents.common", root),
                Path(os.path.abspath(os.fspath(source))),
            )

            policy_path.write_text('version = 2\n', encoding="utf-8")
            with self.assertRaises(SystemExit):
                self.module.load_policy(policy_path)
            with self.assertRaises(SystemExit):
                self.module.validate_source_path("", "documents.common", root)
            with self.assertRaises(SystemExit):
                self.module.validate_source_path("missing.md", "documents.common", root)

    def test_render_documents_rejects_non_file_sources(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            output = root / "bundle"
            output.mkdir()
            (root / "dir").mkdir()
            with self.assertRaises(SystemExit):
                self.module.render_documents(
                    {"documents": {"common": "dir"}},
                    output,
                    root,
                )

    def test_optional_sections_accept_none(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            output = root / "bundle"
            output.mkdir()
            self.assertEqual(self.module.render_documents({"documents": None}, output, root), {})
            self.assertEqual(
                self.module.render_copies({"copies": None}, output, root, "codex", "strict"),
                [],
            )
            self.assertEqual(
                self.module.render_ssh({"ssh": None}, output, root, "codex", "strict"),
                {},
            )
            self.assertEqual(
                self.module.render_credentials(
                    {"credentials": None}, root, "codex", "strict"
                ),
                {},
            )

    def test_render_copies_supports_public_and_secret_directories(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            output = root / "bundle"
            output.mkdir()
            public_dir = root / "public-dir"
            public_dir.mkdir()
            (public_dir / "note.txt").write_text("note\n", encoding="utf-8")
            secret_dir = root / "secret-dir"
            secret_dir.mkdir()
            secret_dir.chmod(0o700)
            (secret_dir / "token.txt").write_text("token\n", encoding="utf-8")
            (secret_dir / "token.txt").chmod(0o600)

            rendered = self.module.render_copies(
                {
                    "copies": [
                        {
                            "source": "public-dir",
                            "target": "/state/injected/public-dir",
                            "classification": "public",
                        },
                        {
                            "source": "secret-dir",
                            "target": "/state/agent-home/.config/workcell/secrets",
                            "classification": "secret",
                        },
                    ]
                },
                output,
                root,
                "codex",
                "strict",
            )

            self.assertEqual(rendered[0]["source"], "copies/0")
            self.assertEqual(rendered[0]["kind"], "dir")
            self.assertEqual(
                rendered[1]["source"]["mount_path"],
                "/opt/workcell/host-inputs/copies/1",
            )
            self.assertEqual(rendered[1]["kind"], "dir")

    def test_render_copies_rejects_invalid_entries(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            output = root / "bundle"
            output.mkdir()
            (root / "file.txt").write_text("data\n", encoding="utf-8")
            (root / "file.txt").chmod(0o600)

            with self.assertRaises(SystemExit):
                self.module.render_copies(
                    {"copies": [{"source": "file.txt", "target": "/state/injected/file.txt"}]},
                    output,
                    root,
                    "codex",
                    "strict",
                )
            with self.assertRaises(SystemExit):
                self.module.render_copies(
                    {"copies": ["not-a-table"]},
                    output,
                    root,
                    "codex",
                    "strict",
                )
            with self.assertRaises(SystemExit):
                self.module.classification_modes("invalid", is_dir=False)

    def test_render_ssh_supports_full_configuration(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            output = root / "bundle"
            output.mkdir()
            (root / "config").write_text("Host *\n", encoding="utf-8")
            (root / "known_hosts").write_text("host key\n", encoding="utf-8")
            (root / "id_a").write_text("key a\n", encoding="utf-8")
            (root / "id_b").write_text("key b\n", encoding="utf-8")
            (root / "config").chmod(0o600)
            (root / "known_hosts").chmod(0o600)
            (root / "id_a").chmod(0o600)
            (root / "id_b").chmod(0o600)

            rendered = self.module.render_ssh(
                {
                    "ssh": {
                        "enabled": True,
                        "config": "config",
                        "known_hosts": "known_hosts",
                        "identities": ["id_a", "id_b"],
                    }
                },
                output,
                root,
                "codex",
                "strict",
            )

            self.assertEqual(
                rendered["config"]["mount_path"],
                "/opt/workcell/host-inputs/ssh/config",
            )
            self.assertEqual(len(rendered["identities"]), 2)
            self.assertEqual(rendered["identities"][0]["target_name"], "id_a")

    def test_render_ssh_rejects_invalid_configuration(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            output = root / "bundle"
            output.mkdir()
            (root / "config-dir").mkdir()
            with self.assertRaises(SystemExit):
                self.module.render_ssh(
                    {"ssh": {"enabled": "yes"}},
                    output,
                    root,
                    "codex",
                    "strict",
                )

            (root / "config").write_text("key\n", encoding="utf-8")
            with self.assertRaises(SystemExit):
                self.module.render_ssh(
                    {
                        "ssh": {
                            "enabled": True,
                            "identities": ["config"],
                        }
                    },
                    output,
                    root,
                    "codex",
                    "strict",
                )

            with self.assertRaises(SystemExit):
                self.module.render_ssh(
                    {"ssh": {"enabled": True, "providers": ["invalid"]}},
                    output,
                    root,
                    "codex",
                    "strict",
                )

            (root / "unsafe").write_text("Host *\nProxyCommand nope\n", encoding="utf-8")
            (root / "unsafe").chmod(0o600)
            self.module.validate_ssh_config_safety(root / "unsafe", allow_unsafe=True)
            self.assertIsNone(self.module.parse_ssh_directive("   "))
            self.assertIsNone(self.module.parse_ssh_directive("# comment"))

            # New risky directives should also be blocked
            for directive, content in [
                ("ForwardAgent", "Host *\nForwardAgent yes\n"),
                ("SendEnv", "Host *\nSendEnv LC_ALL\n"),
                ("ControlPath", "Host *\nControlPath /tmp/ssh-%r@%h:%p\n"),
                ("UserKnownHostsFile", "Host *\nUserKnownHostsFile /dev/null\n"),
            ]:
                risky_file = root / f"risky_{directive.lower()}"
                risky_file.write_text(content, encoding="utf-8")
                risky_file.chmod(0o600)
                with self.assertRaises(SystemExit):
                    self.module.validate_ssh_config_safety(risky_file, allow_unsafe=False)
                # Verify allow_unsafe bypasses the block
                self.module.validate_ssh_config_safety(risky_file, allow_unsafe=True)

    def test_render_credentials_rejects_invalid_tables_and_paths(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            (root / "dir").mkdir()
            with self.assertRaises(SystemExit):
                self.module.render_credentials({"credentials": []}, root, "codex", "strict")
            with self.assertRaises(SystemExit):
                self.module.render_credentials(
                    {"credentials": {"codex_auth": "dir"}}, root, "codex", "strict"
                )

    def test_main_writes_manifest_for_supported_policy(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            output = root / "bundle"
            policy_path = root / "policy.toml"
            (root / "common.md").write_text("common\n", encoding="utf-8")
            (root / "auth.json").write_text('{"token":"abc"}\n', encoding="utf-8")
            (root / "auth.json").chmod(0o600)
            policy_path.write_text(
                """
                version = 1
                [documents]
                common = "common.md"
                [credentials]
                codex_auth = "auth.json"
                """,
                encoding="utf-8",
            )
            argv = [
                "render_injection_bundle.py",
                "--policy",
                str(policy_path),
                "--agent",
                "codex",
                "--mode",
                "strict",
                "--output-root",
                str(output),
            ]
            with mock.patch.object(sys, "argv", argv):
                self.assertEqual(self.module.main(), 0)

            manifest = json.loads((output / "manifest.json").read_text(encoding="utf-8"))
            self.assertEqual(manifest["version"], 1)
            self.assertEqual(manifest["documents"]["common"], "documents/common.md")
            self.assertIn("codex_auth", manifest["credentials"])

    def test_extract_direct_mounts_covers_ssh_identities_and_argparse(self) -> None:
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
                        "ssh": {
                            "identities": [
                                {
                                    "source": "/host/id_a",
                                    "mount_path": "/opt/workcell/host-inputs/ssh/identity-0",
                                    "target_name": "id_a",
                                }
                            ]
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
                self.assertEqual(self.extract_module.parse_args().manifest, str(manifest_path))
            with mock.patch.object(sys, "argv", argv):
                self.assertEqual(self.extract_module.main(), 0)

            manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
            mounts = json.loads(mount_spec_path.read_text(encoding="utf-8"))
            self.assertNotIn("source", manifest["credentials"]["codex_auth"])
            self.assertNotIn("source", manifest["ssh"]["identities"][0])
            self.assertEqual(len(mounts), 2)

    def test_extract_direct_mounts_rejects_missing_mount_path(self) -> None:
        with self.assertRaises(SystemExit):
            self.extract_module.require_direct_mount(
                {"source": "/host/auth.json"},
                "credentials.codex_auth",
            )


if __name__ == "__main__":
    unittest.main()
