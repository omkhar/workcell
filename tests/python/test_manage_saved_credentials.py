from __future__ import annotations

import io
import json
import stat
import sys
import tempfile
from pathlib import Path
import unittest
from unittest import mock
from types import SimpleNamespace

from test_support import load_module


class ManageSavedCredentialsTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.module = load_module("scripts/lib/manage_saved_credentials.py")

    def test_describe_codex_auth_returns_expected_metadata(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            source = root / "auth.json"
            source.write_text('{"token":"abc"}\n', encoding="utf-8")

            metadata = self.module.validate_saved_credential("codex_auth", source)

            self.assertEqual(metadata["key"], "codex_auth")
            self.assertEqual(metadata["filename"], "codex-auth.json")
            self.assertEqual(metadata["extra_endpoints"], [])

    def test_describe_gemini_env_surfaces_extra_endpoints(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            source = root / "gemini.env"
            source.write_text("GOOGLE_GENAI_USE_GCA=true\n", encoding="utf-8")

            metadata = self.module.validate_saved_credential("gemini_env", source)

            self.assertEqual(metadata["key"], "gemini_env")
            self.assertIn("accounts.google.com:443", metadata["extra_endpoints"])
            self.assertIn("oauth2.googleapis.com:443", metadata["extra_endpoints"])
            self.assertEqual(metadata["selected_auth_type"], "oauth-personal")
            self.assertTrue(metadata["requires_gcloud_adc"])

    def test_persist_creates_root_fragment_and_saved_credential(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            source = root / "auth.json"
            source.write_text('{"token":"abc"}\n', encoding="utf-8")
            root_policy = root / "injection-policy.toml"
            fragment = root / "injection-policy.d" / "saved-credentials.toml"
            credentials_root = root / "credentials"

            result = self.module.persist_saved_credential(
                "codex_auth",
                source,
                root_policy,
                fragment,
                credentials_root,
            )

            self.assertEqual(result["root_policy"], str(root_policy))
            self.assertEqual(result["fragment"], str(fragment))
            saved_path = Path(result["credential_path"])
            self.assertTrue(saved_path.is_file())
            self.assertEqual(saved_path.read_text(encoding="utf-8"), '{"token":"abc"}\n')
            self.assertEqual(stat.S_IMODE(saved_path.stat().st_mode), 0o600)

            root_policy_text = root_policy.read_text(encoding="utf-8")
            self.assertIn('includes = ["injection-policy.d/saved-credentials.toml"]', root_policy_text)
            self.assertTrue(root_policy_text.startswith("version = 1\n"))

            fragment_text = fragment.read_text(encoding="utf-8")
            self.assertIn("[credentials]\n", fragment_text)
            self.assertIn(f'codex_auth = "{saved_path}"', fragment_text)

    def test_persist_updates_existing_root_policy_without_dropping_other_tables(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            source = root / "auth.json"
            source.write_text('{"token":"abc"}\n', encoding="utf-8")
            root_policy = root / "injection-policy.toml"
            root_policy.write_text(
                'version = 1\n\n[documents]\ncommon = "/tmp/common.md"\n',
                encoding="utf-8",
            )
            fragment = root / "injection-policy.d" / "saved-credentials.toml"
            credentials_root = root / "credentials"

            self.module.persist_saved_credential(
                "codex_auth",
                source,
                root_policy,
                fragment,
                credentials_root,
            )

            root_policy_text = root_policy.read_text(encoding="utf-8")
            self.assertIn('includes = ["injection-policy.d/saved-credentials.toml"]', root_policy_text)
            self.assertIn('[documents]\ncommon = "/tmp/common.md"\n', root_policy_text)

    def test_persist_preserves_existing_includes_and_fragment_entries(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            codex_source = root / "codex-auth.json"
            codex_source.write_text('{"token":"codex"}\n', encoding="utf-8")
            claude_source = root / "claude-auth.json"
            claude_source.write_text('{"token":"claude"}\n', encoding="utf-8")
            root_policy = root / "injection-policy.toml"
            root_policy.write_text(
                'version = 1\nincludes = ["shared.toml"]\n',
                encoding="utf-8",
            )
            fragment = root / "injection-policy.d" / "saved-credentials.toml"
            fragment.parent.mkdir(parents=True)
            fragment.write_text(
                'version = 1\n\n[credentials]\nclaude_auth = "/tmp/existing-claude-auth.json"\n',
                encoding="utf-8",
            )
            credentials_root = root / "credentials"

            self.module.persist_saved_credential(
                "codex_auth",
                codex_source,
                root_policy,
                fragment,
                credentials_root,
            )
            self.module.persist_saved_credential(
                "claude_auth",
                claude_source,
                root_policy,
                fragment,
                credentials_root,
            )

            root_policy_text = root_policy.read_text(encoding="utf-8")
            self.assertIn('includes = ["shared.toml", "injection-policy.d/saved-credentials.toml"]', root_policy_text)

            parsed_fragment = self.module.load_managed_fragment(fragment)
            credentials = parsed_fragment["credentials"]
            self.assertIsInstance(credentials, dict)
            self.assertEqual(
                set(credentials),
                {"claude_auth", "codex_auth"},
            )
            self.assertEqual(
                Path(credentials["codex_auth"]).read_text(encoding="utf-8"),
                '{"token":"codex"}\n',
            )
            self.assertEqual(
                Path(credentials["claude_auth"]).read_text(encoding="utf-8"),
                '{"token":"claude"}\n',
            )

    def test_persist_saved_credentials_writes_multi_file_gemini_bundle(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            env_source = root / "gemini.env"
            env_source.write_text("GOOGLE_GENAI_USE_GCA=true\n", encoding="utf-8")
            adc_source = root / "gcloud-adc.json"
            adc_source.write_text('{"type":"authorized_user"}\n', encoding="utf-8")
            root_policy = root / "injection-policy.toml"
            fragment = root / "injection-policy.d" / "saved-credentials.toml"
            credentials_root = root / "credentials"

            result = self.module.persist_saved_credentials(
                [
                    ("gemini_env", env_source),
                    ("gcloud_adc", adc_source),
                ],
                root_policy,
                fragment,
                credentials_root,
            )

            credential_paths = result["credential_paths"]
            self.assertEqual(
                set(credential_paths),
                {"gemini_env", "gcloud_adc"},
            )
            self.assertEqual(
                Path(credential_paths["gemini_env"]).read_text(encoding="utf-8"),
                "GOOGLE_GENAI_USE_GCA=true\n",
            )
            self.assertEqual(
                Path(credential_paths["gcloud_adc"]).read_text(encoding="utf-8"),
                '{"type":"authorized_user"}\n',
            )

            parsed_fragment = self.module.load_managed_fragment(fragment)
            credentials = parsed_fragment["credentials"]
            self.assertEqual(
                set(credentials),
                {"gemini_env", "gcloud_adc"},
            )

    def test_persist_updates_multiline_includes_without_dropping_preamble(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            source = root / "auth.json"
            source.write_text('{"token":"abc"}\n', encoding="utf-8")
            root_policy = root / "injection-policy.toml"
            root_policy.write_text(
                'version = 1\n# keep me\nincludes = [\n  "shared.toml",\n]\n\n[documents]\ncommon = "/tmp/common.md"\n',
                encoding="utf-8",
            )
            fragment = root / "injection-policy.d" / "saved-credentials.toml"
            credentials_root = root / "credentials"

            self.module.persist_saved_credential(
                "codex_auth",
                source,
                root_policy,
                fragment,
                credentials_root,
            )

            root_policy_text = root_policy.read_text(encoding="utf-8")
            self.assertIn("# keep me\n", root_policy_text)
            self.assertIn(
                'includes = ["shared.toml", "injection-policy.d/saved-credentials.toml"]',
                root_policy_text,
            )
            self.assertIn('[documents]\ncommon = "/tmp/common.md"\n', root_policy_text)

    def test_describe_rejects_non_file_sources(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            source = root / "missing.json"

            with self.assertRaises(SystemExit):
                self.module.validate_saved_credential("codex_auth", source)

    def test_describe_rejects_empty_claude_api_key(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            source = root / "claude-api-key.txt"
            source.write_text("\n", encoding="utf-8")

            with self.assertRaises(SystemExit):
                self.module.validate_saved_credential("claude_api_key", source)

    def test_describe_gemini_oauth_and_gcloud_adc_metadata(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            oauth_source = root / "oauth.json"
            oauth_source.write_text('{"refresh_token":"abc"}\n', encoding="utf-8")
            adc_source = root / "adc.json"
            adc_source.write_text('{"type":"authorized_user"}\n', encoding="utf-8")

            oauth_metadata = self.module.validate_saved_credential("gemini_oauth", oauth_source)
            adc_metadata = self.module.validate_saved_credential("gcloud_adc", adc_source)

            self.assertIn("aiplatform.googleapis.com:443", oauth_metadata["extra_endpoints"])
            self.assertIn("oauth2.googleapis.com:443", adc_metadata["extra_endpoints"])

    def test_parse_toml_document_rejects_invalid_toml(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            path = root / "policy.toml"

            with self.assertRaises(SystemExit):
                self.module.parse_toml_document('version = "unterminated', path)

    def test_strip_inline_comment_and_assignment_helpers_handle_quotes(self) -> None:
        line = 'includes = ["shared.toml", "#literal"] # comment\n'
        stripped = self.module.strip_inline_comment(line)

        self.assertEqual(stripped.rstrip(), 'includes = ["shared.toml", "#literal"]')
        self.assertEqual(self.module.bracket_balance(stripped), 0)
        self.assertEqual(
            self.module.assignment_span(
                [
                    "# preamble\n",
                    'includes = ["shared.toml", "#literal"] # comment\n',
                    "\n",
                    "[documents]\n",
                ],
                "includes",
            ),
            (1, 2),
        )

    def test_assignment_span_rejects_unclosed_array_before_table(self) -> None:
        with self.assertRaises(SystemExit):
            self.module.assignment_span(
                [
                    "includes = [\n",
                    '  "shared.toml"\n',
                    "[documents]\n",
                ],
                "includes",
            )

    def test_update_root_policy_handles_empty_existing_file(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            root_policy = root / "injection-policy.toml"
            root_policy.write_text("", encoding="utf-8")
            fragment = root / "injection-policy.d" / "saved-credentials.toml"

            self.module.update_root_policy(root_policy, fragment)

            self.assertEqual(
                root_policy.read_text(encoding="utf-8"),
                'version = 1\nincludes = ["injection-policy.d/saved-credentials.toml"]\n',
            )

    def test_ensure_parent_directory_rejects_symlinked_parent_chain(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            real_parent = root / "real"
            real_parent.mkdir()
            redirected = root / "redirected"
            redirected.symlink_to(real_parent, target_is_directory=True)

            with self.assertRaises(SystemExit):
                self.module.ensure_parent_directory(redirected / "child" / "file.txt")

    def test_update_root_policy_is_noop_when_include_already_present(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            root_policy = root / "injection-policy.toml"
            original = 'version = 1\nincludes = ["injection-policy.d/saved-credentials.toml"]\n'
            root_policy.write_text(original, encoding="utf-8")
            fragment = root / "injection-policy.d" / "saved-credentials.toml"

            self.module.update_root_policy(root_policy, fragment)

            self.assertEqual(root_policy.read_text(encoding="utf-8"), original)

    def test_update_root_policy_rejects_unsupported_version(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            root_policy = root / "injection-policy.toml"
            root_policy.write_text("version = 2\n", encoding="utf-8")
            fragment = root / "injection-policy.d" / "saved-credentials.toml"

            with self.assertRaises(SystemExit):
                self.module.update_root_policy(root_policy, fragment)

    def test_load_managed_fragment_rejects_invalid_shape(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            fragment = root / "saved-credentials.toml"
            fragment.write_text("version = 2\n", encoding="utf-8")

            with self.assertRaises(SystemExit):
                self.module.load_managed_fragment(fragment)

            fragment.write_text(
                'version = 1\n\n[credentials]\nunsupported = "/tmp/value"\n',
                encoding="utf-8",
            )
            with self.assertRaises(SystemExit):
                self.module.load_managed_fragment(fragment)

    def test_render_managed_fragment_rejects_invalid_values(self) -> None:
        with self.assertRaises(SystemExit):
            self.module.render_managed_fragment({"credentials": []})

        with self.assertRaises(SystemExit):
            self.module.render_managed_fragment({"credentials": {"codex_auth": 123}})

    def test_parse_persist_entry_rejects_invalid_inputs(self) -> None:
        with self.assertRaises(SystemExit):
            self.module.parse_persist_entry("codex_auth")

        with self.assertRaises(SystemExit):
            self.module.parse_persist_entry("unsupported=/tmp/value")

        with self.assertRaises(SystemExit):
            self.module.parse_persist_entry("codex_auth=")

    def test_parse_persist_entry_preserves_symlink_path(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            target = root / "auth.json"
            target.write_text('{"token":"abc"}\n', encoding="utf-8")
            source = root / "auth-link.json"
            source.symlink_to(target)

            key, path = self.module.parse_persist_entry(f"codex_auth={source}")

            self.assertEqual(key, "codex_auth")
            self.assertTrue(path.is_symlink())

    def test_persist_saved_credentials_rejects_empty_entry_list(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            with self.assertRaises(SystemExit):
                self.module.persist_saved_credentials(
                    [],
                    root / "policy.toml",
                    root / "saved-credentials.toml",
                    root / "credentials",
                )

    def test_persist_saved_credentials_rolls_back_when_root_update_fails(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            source = root / "auth.json"
            source.write_text('{"token":"abc"}\n', encoding="utf-8")
            root_policy = root / "injection-policy.toml"
            fragment = root / "saved-credentials.toml"
            credentials_root = root / "credentials"

            with mock.patch.object(self.module, "update_root_policy", side_effect=SystemExit("boom")):
                with self.assertRaises(SystemExit):
                    self.module.persist_saved_credentials(
                        [("codex_auth", source)],
                        root_policy,
                        fragment,
                        credentials_root,
                    )

            self.assertFalse(fragment.exists())
            self.assertFalse((credentials_root / "codex-auth.json").exists())

    def test_persist_saved_credentials_rolls_back_bundle_when_fragment_already_included(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            codex_source = root / "auth.json"
            codex_source.write_text('{"token":"abc"}\n', encoding="utf-8")
            adc_source = root / "adc.json"
            adc_source.write_text('{"type":"authorized_user"}\n', encoding="utf-8")
            root_policy = root / "injection-policy.toml"
            root_policy.write_text('version = 1\nincludes = ["saved-credentials.toml"]\n', encoding="utf-8")
            fragment = root / "saved-credentials.toml"
            fragment.write_text("version = 1\n\n[credentials]\n", encoding="utf-8")
            credentials_root = root / "credentials"

            original_copy = self.module.copy_saved_credential
            calls = 0

            def flaky_copy(source: Path, destination: Path) -> None:
                nonlocal calls
                calls += 1
                if calls == 2:
                    raise SystemExit("boom")
                original_copy(source, destination)

            with mock.patch.object(self.module, "copy_saved_credential", side_effect=flaky_copy):
                with self.assertRaises(SystemExit):
                    self.module.persist_saved_credentials(
                        [("codex_auth", codex_source), ("gcloud_adc", adc_source)],
                        root_policy,
                        fragment,
                        credentials_root,
                    )

            self.assertEqual(
                fragment.read_text(encoding="utf-8"),
                "version = 1\n\n[credentials]\n",
            )
            self.assertFalse((credentials_root / "codex-auth.json").exists())
            self.assertFalse((credentials_root / "gcloud-adc.json").exists())

    def test_persist_saved_credentials_rejects_symlinked_root_policy_before_writes(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            source = root / "auth.json"
            source.write_text('{"token":"abc"}\n', encoding="utf-8")
            real_root_policy = root / "real-policy.toml"
            real_root_policy.write_text('version = 1\nincludes = ["saved-credentials.toml"]\n', encoding="utf-8")
            root_policy = root / "policy-link.toml"
            root_policy.symlink_to(real_root_policy)
            fragment = root / "saved-credentials.toml"
            credentials_root = root / "credentials"

            with self.assertRaises(SystemExit):
                self.module.persist_saved_credentials(
                    [("codex_auth", source)],
                    root_policy,
                    fragment,
                    credentials_root,
                )

            self.assertFalse(fragment.exists())
            self.assertFalse((credentials_root / "codex-auth.json").exists())

    def test_persist_saved_credentials_rejects_symlinked_root_policy_parent_before_writes(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            source = root / "auth.json"
            source.write_text('{"token":"abc"}\n', encoding="utf-8")
            real_config_dir = root / "real-config"
            real_config_dir.mkdir()
            config_dir = root / "config-link"
            config_dir.symlink_to(real_config_dir, target_is_directory=True)
            root_policy = config_dir / "injection-policy.toml"
            fragment = root / "saved-credentials.toml"
            credentials_root = root / "credentials"

            with self.assertRaises(SystemExit):
                self.module.persist_saved_credentials(
                    [("codex_auth", source)],
                    root_policy,
                    fragment,
                    credentials_root,
                )

            self.assertFalse((real_config_dir / "injection-policy.toml").exists())
            self.assertFalse((credentials_root / "codex-auth.json").exists())

    def test_persist_saved_credentials_rejects_symlinked_fragment_parent_before_writes(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            source = root / "auth.json"
            source.write_text('{"token":"abc"}\n', encoding="utf-8")
            real_fragment_dir = root / "real-fragments"
            real_fragment_dir.mkdir()
            fragment_dir = root / "fragment-link"
            fragment_dir.symlink_to(real_fragment_dir, target_is_directory=True)
            fragment = fragment_dir / "saved-credentials.toml"
            credentials_root = root / "credentials"

            with self.assertRaises(SystemExit):
                self.module.persist_saved_credentials(
                    [("codex_auth", source)],
                    root / "policy.toml",
                    fragment,
                    credentials_root,
                )

            self.assertFalse((real_fragment_dir / "saved-credentials.toml").exists())
            self.assertFalse((credentials_root / "codex-auth.json").exists())

    def test_persist_saved_credential_wrapper_requires_saved_path(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            source = root / "auth.json"
            source.write_text('{"token":"abc"}\n', encoding="utf-8")

            with mock.patch.object(
                self.module,
                "persist_saved_credentials",
                return_value={"root_policy": "a", "fragment": "b", "credential_paths": {}},
            ):
                with self.assertRaises(SystemExit):
                    self.module.persist_saved_credential(
                        "codex_auth",
                        source,
                        root / "policy.toml",
                        root / "saved-credentials.toml",
                        root / "credentials",
                    )

    def test_parse_args_supports_describe_and_persist(self) -> None:
        with mock.patch.object(sys, "argv", ["manage_saved_credentials.py", "describe", "--key", "codex_auth", "--source", "/tmp/auth.json"]):
            args = self.module.parse_args()
        self.assertEqual(args.command, "describe")
        self.assertEqual(args.key, "codex_auth")

        with mock.patch.object(
            sys,
            "argv",
            [
                "manage_saved_credentials.py",
                "persist",
                "--entry",
                "codex_auth=/tmp/auth.json",
                "--root-policy",
                "/tmp/policy.toml",
                "--fragment",
                "/tmp/fragment.toml",
                "--credentials-root",
                "/tmp/credentials",
            ],
        ):
            args = self.module.parse_args()
        self.assertEqual(args.command, "persist")
        self.assertEqual(args.entry, ["codex_auth=/tmp/auth.json"])

    def test_main_describe_and_persist_paths(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            source = root / "auth.json"
            source.write_text('{"token":"abc"}\n', encoding="utf-8")
            root_policy = root / "injection-policy.toml"
            fragment = root / "saved-credentials.toml"
            credentials_root = root / "credentials"

            with mock.patch.object(
                sys,
                "argv",
                [
                    "manage_saved_credentials.py",
                    "describe",
                    "--key",
                    "codex_auth",
                    "--source",
                    str(source),
                ],
            ):
                stdout = io.StringIO()
                with mock.patch("sys.stdout", stdout):
                    self.assertEqual(self.module.main(), 0)
                payload = json.loads(stdout.getvalue())
                self.assertEqual(payload["key"], "codex_auth")

            with mock.patch.object(
                sys,
                "argv",
                [
                    "manage_saved_credentials.py",
                    "persist",
                    "--entry",
                    f"codex_auth={source}",
                    "--root-policy",
                    str(root_policy),
                    "--fragment",
                    str(fragment),
                    "--credentials-root",
                    str(credentials_root),
                ],
            ):
                stdout = io.StringIO()
                with mock.patch("sys.stdout", stdout):
                    self.assertEqual(self.module.main(), 0)
                payload = json.loads(stdout.getvalue())
                self.assertIn("codex_auth", payload["credential_paths"])

    def test_main_rejects_symlinked_describe_source(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            target = root / "auth.json"
            target.write_text('{"token":"abc"}\n', encoding="utf-8")
            source = root / "auth-link.json"
            source.symlink_to(target)

            with mock.patch.object(
                sys,
                "argv",
                [
                    "manage_saved_credentials.py",
                    "describe",
                    "--key",
                    "codex_auth",
                    "--source",
                    str(source),
                ],
            ):
                with self.assertRaises(SystemExit):
                    self.module.main()

    def test_main_rejects_stubbed_unsupported_command(self) -> None:
        with mock.patch.object(
            self.module,
            "parse_args",
            return_value=SimpleNamespace(command="unsupported"),
        ):
            with self.assertRaises(SystemExit):
                self.module.main()


if __name__ == "__main__":
    unittest.main()
