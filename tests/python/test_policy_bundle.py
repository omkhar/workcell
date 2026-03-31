from __future__ import annotations

import os
import tempfile
import unittest
from unittest import mock
from pathlib import Path

from test_support import load_module


class PolicyBundleTests(unittest.TestCase):
    def setUp(self) -> None:
        self.module = load_module("scripts/lib/policy_bundle.py")

    def test_load_policy_bundle_merges_includes_and_tracks_sources(self) -> None:
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

    def test_render_policy_toml_round_trips_resolver_entry(self) -> None:
        policy = {
            "version": 1,
            "credentials": {
                "codex_auth": {"source": "/tmp/codex-auth.json"},
                "claude_auth": {
                    "resolver": "claude-macos-keychain",
                    "materialization": "ephemeral",
                },
            },
        }

        rendered = self.module.render_policy_toml(policy)
        reparsed = self.module.parse_toml_subset(rendered, Path("/tmp/policy.toml"))

        self.assertEqual(reparsed["credentials"]["codex_auth"]["source"], "/tmp/codex-auth.json")
        self.assertEqual(
            reparsed["credentials"]["claude_auth"]["resolver"],
            "claude-macos-keychain",
        )
        self.assertEqual(
            reparsed["credentials"]["claude_auth"]["materialization"],
            "ephemeral",
        )

    def test_validation_helpers_reject_invalid_inputs(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            inside = root / "inside.txt"
            inside.write_text("ok\n", encoding="utf-8")
            inside.chmod(0o600)
            outside = root.parent / "outside.txt"
            outside.write_text("outside\n", encoding="utf-8")
            expected_inside = self.module.expand_host_path("inside.txt", root)

            self.assertEqual(
                expected_inside,
                Path(os.path.abspath(os.fspath(inside))),
            )
            self.module.require_path_within(root, expected_inside, "inside")
            with self.assertRaises(SystemExit):
                self.module.require_path_within(root, outside, "outside")

            self.assertEqual(
                self.module.validate_source_path("inside.txt", "inside", root),
                expected_inside,
            )
            with self.assertRaises(SystemExit):
                self.module.validate_source_path("", "empty", root)
            with self.assertRaises(SystemExit):
                self.module.validate_source_path("missing.txt", "missing", root)

            symlink_path = root / "link.txt"
            symlink_path.symlink_to(inside)
            with self.assertRaises(SystemExit):
                self.module.require_no_symlink_in_path_chain(symlink_path, "link")
            with self.assertRaises(SystemExit):
                self.module.require_secret_file(symlink_path, "link")

            world_readable = root / "world.txt"
            world_readable.write_text("secret\n", encoding="utf-8")
            world_readable.chmod(0o644)
            with self.assertRaises(SystemExit):
                self.module.require_secret_file(world_readable, "world")

    def test_selection_comment_and_value_helpers_cover_error_paths(self) -> None:
        self.assertTrue(
            self.module.selected_for(None, "codex", "providers", self.module.SUPPORTED_AGENTS)
        )
        self.assertFalse(
            self.module.selected_for(
                ["claude"],
                "codex",
                "providers",
                self.module.SUPPORTED_AGENTS,
            )
        )
        for invalid in ([], ["codex", 1], ["unknown"]):
            with self.assertRaises(SystemExit):
                self.module.selected_for(
                    invalid,
                    "codex",
                    "providers",
                    self.module.SUPPORTED_AGENTS,
                )

        self.assertEqual(
            self.module.strip_comment('value = "a # b" # tail'),
            'value = "a # b"',
        )
        self.assertEqual(
            self.module.strip_comment(r'value = "a \" # b" # tail'),
            r'value = "a \" # b"',
        )

        policy_path = Path("/tmp/policy.toml")
        self.assertTrue(self.module.parse_value("true", policy_path, 1))
        self.assertEqual(self.module.parse_value('"value"', policy_path, 1), "value")
        self.assertEqual(self.module.parse_value('["codex"]', policy_path, 1), ["codex"])
        self.assertEqual(self.module.parse_value("7", policy_path, 1), 7)
        for raw in ("", "[1]", "{bad = true}"):
            with self.assertRaises(SystemExit):
                self.module.parse_value(raw, policy_path, 1)

    def test_parse_toml_subset_rejects_invalid_structures(self) -> None:
        policy_path = Path("/tmp/policy.toml")
        invalid_cases = [
            "[[unknown]]\n",
            "[documents]\n[documents]\n",
            "[credentials.unknown]\nsource = \"/tmp/x\"\n",
            "[unknown]\nvalue = 1\n",
            "invalid-line\n",
            ' = "value"\n',
            'a.b = "value"\n',
            'value = "one"\nvalue = "two"\n',
            '[credentials]\ncodex_auth = "/tmp/x"\n[credentials.codex_auth]\nsource = "/tmp/y"\n',
        ]
        for content in invalid_cases:
            with self.subTest(content=content):
                with self.assertRaises(SystemExit):
                    self.module.parse_toml_subset(content, policy_path)

    def test_parse_toml_subset_covers_copies_and_type_corruption_guards(self) -> None:
        policy_path = Path("/tmp/policy.toml")

        parsed = self.module.parse_toml_subset(
            '[[copies]]\nsource = "/tmp/source.txt"\ntarget = "/state/injected/source.txt"\n',
            policy_path,
        )
        self.assertEqual(
            parsed["copies"],
            [{"source": "/tmp/source.txt", "target": "/state/injected/source.txt"}],
        )

        invalid_cases = [
            'copies = "bad"\n[[copies]]\nsource = "/tmp/source.txt"\n',
            'credentials = "bad"\n[credentials.codex_auth]\nsource = "/tmp/auth.json"\n',
            'documents = "bad"\n[documents]\ncommon = "/tmp/common.md"\n',
        ]
        for content in invalid_cases:
            with self.subTest(content=content):
                with self.assertRaises(SystemExit):
                    self.module.parse_toml_subset(content, policy_path)

    def test_rebase_merge_and_load_helpers_cover_nested_and_error_paths(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            fragment_dir = root / "fragment"
            fragment_dir.mkdir()
            (fragment_dir / "doc.md").write_text("doc\n", encoding="utf-8")
            (fragment_dir / "copy.txt").write_text("copy\n", encoding="utf-8")
            (fragment_dir / "id_key").write_text("key\n", encoding="utf-8")
            (fragment_dir / "auth.json").write_text("{}\n", encoding="utf-8")
            rebased = self.module.rebase_policy_fragment(
                {
                    "documents": {"common": "doc.md"},
                    "copies": [{"source": "copy.txt", "target": "/state/injected/copy.txt"}, "literal"],
                    "ssh": {
                        "config": "ssh_config",
                        "known_hosts": "known_hosts",
                        "identities": ["id_key"],
                    },
                    "credentials": {
                        "codex_auth": "auth.json",
                        "github_hosts": {"source": "hosts.yml", "providers": ["codex"]},
                    },
                    "version": 1,
                },
                fragment_dir,
            )
            expected = lambda relative: str(
                self.module.expand_host_path(relative, fragment_dir)
            )
            self.assertEqual(
                rebased["documents"]["common"],
                expected("doc.md"),
            )
            self.assertEqual(
                rebased["copies"][1],
                "literal",
            )
            self.assertEqual(
                rebased["ssh"]["identities"][0],
                expected("id_key"),
            )
            self.assertEqual(
                rebased["credentials"]["github_hosts"]["source"],
                expected("hosts.yml"),
            )

            for addition in (
                {"version": 2},
                {"documents": []},
                {"copies": {}},
            ):
                with self.assertRaises(SystemExit):
                    self.module.merge_policy_fragment({}, addition, root / "bad.toml")

            with self.assertRaises(SystemExit):
                self.module.merge_policy_fragment(
                    {"documents": {"common": "a"}},
                    {"documents": {"common": "b"}},
                    root / "dup.toml",
                )

            (root / "doc.md").write_text("doc\n", encoding="utf-8")
            (root / "shared.toml").write_text(
                '[documents]\ncommon = "doc.md"\n',
                encoding="utf-8",
            )
            (root / "policy.toml").write_text(
                'version = 1\nincludes = ["shared.toml"]\n',
                encoding="utf-8",
            )
            merged, sources = self.module.load_policy_bundle(root / "policy.toml")
            self.assertEqual(
                merged["documents"]["common"],
                str((root / "doc.md").resolve()),
            )
            self.assertEqual(
                [entry["path"] for entry in sources],
                ["shared.toml", "policy.toml"],
            )

            (root / "cycle-a.toml").write_text('version = 1\nincludes = ["cycle-b.toml"]\n', encoding="utf-8")
            (root / "cycle-b.toml").write_text('version = 1\nincludes = ["cycle-a.toml"]\n', encoding="utf-8")
            with self.assertRaises(SystemExit):
                self.module.load_policy_bundle(root / "cycle-a.toml")

            (root / "dup-policy.toml").write_text(
                'version = 1\nincludes = ["shared.toml", "shared.toml"]\n',
                encoding="utf-8",
            )
            with self.assertRaises(SystemExit):
                self.module.load_policy_bundle(root / "dup-policy.toml")

            self.assertEqual(
                self.module.load_raw_policy(root / "missing.toml"),
                {"version": 1},
            )

    def test_helper_guards_cover_versions_includes_and_corrupted_merge_state(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            self.module.validate_allowed_keys({"version": 1}, {"version"}, "policy")
            with self.assertRaises(SystemExit):
                self.module.validate_allowed_keys({"unexpected": True}, {"version"}, "policy")

            self.assertTrue(
                self.module.composite_policy_sha256(
                    [
                        {"path": "b.toml", "sha256": "sha256:b"},
                        {"path": "a.toml", "sha256": "sha256:a"},
                    ]
                ).startswith("sha256:")
            )
            self.assertEqual(self.module.rebase_fragment_path("", root), "")
            marker = object()
            self.assertIs(self.module.rebase_fragment_path(marker, root), marker)

            include_dir = root / "fragment"
            include_dir.mkdir()
            with self.assertRaises(SystemExit):
                self.module.validate_policy_include("fragment", "include", root, root)

            with self.assertRaises(SystemExit):
                self.module.merge_policy_fragment(
                    {"documents": []},
                    {"documents": {"common": "/tmp/common.md"}},
                    root / "broken-docs.toml",
                )
            with self.assertRaises(SystemExit):
                self.module.merge_policy_fragment(
                    {"copies": {}},
                    {"copies": [{"source": "/tmp/source.txt"}]},
                    root / "broken-copies.toml",
                )

            version_2 = root / "version-2.toml"
            version_2.write_text("version = 2\n", encoding="utf-8")
            with self.assertRaises(SystemExit):
                self.module.load_policy_bundle(version_2)

            include_string = root / "include-string.toml"
            include_string.write_text(
                'version = 1\nincludes = "shared.toml"\n',
                encoding="utf-8",
            )
            with self.assertRaises(SystemExit):
                self.module.load_policy_bundle(include_string)

            with mock.patch.object(self.module, "parse_toml_subset", return_value=[]):
                with self.assertRaises(SystemExit):
                    self.module.load_policy_bundle(version_2)

            with mock.patch.object(
                self.module,
                "parse_toml_subset",
                return_value={"version": 1, "includes": None},
            ):
                merged, sources = self.module.load_policy_bundle(version_2)
            self.assertEqual(merged, {"version": 1})
            self.assertEqual(len(sources), 1)

            raw_policy = root / "raw-policy.toml"
            raw_policy.write_text('[documents]\ncommon = "/tmp/common.md"\n', encoding="utf-8")
            loaded_raw = self.module.load_raw_policy(raw_policy)
            self.assertEqual(loaded_raw["version"], 1)
            self.assertEqual(loaded_raw["documents"]["common"], "/tmp/common.md")

            raw_version_2 = root / "raw-version-2.toml"
            raw_version_2.write_text("version = 2\n", encoding="utf-8")
            with self.assertRaises(SystemExit):
                self.module.load_raw_policy(raw_version_2)

            with mock.patch.object(self.module, "parse_toml_subset", return_value=[]):
                with self.assertRaises(SystemExit):
                    self.module.load_raw_policy(raw_policy)

    def test_render_helpers_cover_supported_and_invalid_shapes(self) -> None:
        self.assertEqual(self.module.quote_string("line\nbreak"), '"line\\nbreak"')
        self.assertEqual(self.module.render_toml_value(False), "false")
        self.assertEqual(self.module.render_toml_value(3), "3")
        self.assertEqual(self.module.render_toml_value("value"), '"value"')
        self.assertEqual(
            self.module.render_toml_value(["codex", "claude"]),
            '["codex", "claude"]',
        )
        with self.assertRaises(SystemExit):
            self.module.render_toml_value([1])
        with self.assertRaises(SystemExit):
            self.module.render_toml_value({"bad": "value"})

        rendered = self.module.render_policy_toml(
            {
                "version": 1,
                "includes": ["shared.toml"],
                "documents": {"common": "/tmp/common.md"},
                "credentials": {
                    "codex_auth": "/tmp/auth.json",
                    "github_hosts": {"source": "/tmp/hosts.yml", "providers": ["codex"]},
                },
                "ssh": {
                    "enabled": True,
                    "config": "/tmp/config",
                    "known_hosts": "/tmp/known_hosts",
                    "identities": ["/tmp/id_key"],
                },
                "copies": [
                    {
                        "source": "/tmp/source.txt",
                        "target": "/state/injected/source.txt",
                        "classification": "public",
                    }
                ],
            }
        )
        self.assertIn('includes = ["shared.toml"]', rendered)
        self.assertIn("[documents]", rendered)
        self.assertIn("[credentials]", rendered)
        self.assertIn("[credentials.github_hosts]", rendered)
        self.assertIn("[ssh]", rendered)
        self.assertIn("[[copies]]", rendered)

        rendered_with_extras = self.module.render_policy_toml(
            {
                "version": 1,
                "ssh": {"enabled": True, "proxy_jump": "bastion"},
                "copies": [
                    {
                        "source": "/tmp/source.txt",
                        "target": "/state/injected/source.txt",
                        "classification": "public",
                        "note": "extra",
                    }
                ],
            }
        )
        self.assertIn('proxy_jump = "bastion"', rendered_with_extras)
        self.assertIn('note = "extra"', rendered_with_extras)

        with self.assertRaises(SystemExit):
            self.module.render_policy_toml({"version": 1, "copies": ["bad"]})

        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            policy_path = root / "policy.toml"
            self.module.write_policy_file(policy_path, {"version": 1})
            self.assertEqual(policy_path.read_text(encoding="utf-8"), "version = 1\n")
            self.assertEqual(policy_path.stat().st_mode & 0o777, 0o600)

    def test_require_secret_file_rejects_non_files_and_wrong_owners(self) -> None:
        with tempfile.TemporaryDirectory() as tmpdir:
            root = Path(tmpdir)
            directory = root / "directory"
            directory.mkdir()
            with self.assertRaises(SystemExit):
                self.module.require_secret_file(directory, "directory")

            owned = root / "owned.txt"
            owned.write_text("secret\n", encoding="utf-8")
            owned.chmod(0o600)
            with mock.patch.object(
                self.module.os,
                "getuid",
                return_value=os.getuid() + 1,
            ):
                with self.assertRaises(SystemExit):
                    self.module.require_secret_file(owned, "owned")


if __name__ == "__main__":
    unittest.main()
