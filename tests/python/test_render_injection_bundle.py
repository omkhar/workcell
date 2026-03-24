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

            policy = {
                "credentials": {
                    "claude_auth": "claude-auth.json",
                    "claude_mcp": "claude-mcp.json",
                    "gemini_projects": "gemini-projects.json",
                }
            }

            claude_rendered = self.module.render_credentials(policy, root, "claude")
            gemini_rendered = self.module.render_credentials(policy, root, "gemini")

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


if __name__ == "__main__":
    unittest.main()
