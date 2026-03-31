#!/usr/bin/env python3
"""Resolve host-side credential inputs into concrete files before rendering."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import shutil
import sys
import tempfile

SCRIPT_DIR = Path(__file__).resolve().parent
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

from policy_bundle import (  # noqa: E402
    AGENT_SCOPED_CREDENTIAL_KEYS,
    SHARED_CREDENTIAL_KEYS,
    SUPPORTED_AGENTS,
    SUPPORTED_MODES,
    die,
    logical_policy_path,
    load_policy_bundle,
    require_secret_file,
    rebase_policy_fragment,
    render_policy_toml,
    selected_for,
    validate_allowed_keys,
    validate_source_path,
)


RESOLVER_ALLOWED_KEYS = {"source", "resolver", "materialization", "providers", "modes"}
SUPPORTED_MATERIALIZATION = {"ephemeral", "persistent"}
ALLOWED_RESOLVERS = {
    "claude_auth": {"claude-macos-keychain"},
}
TEST_CLAUDE_EXPORT_ENV = "WORKCELL_TEST_CLAUDE_KEYCHAIN_EXPORT_FILE"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--policy", required=True)
    parser.add_argument("--agent", required=True, choices=sorted(SUPPORTED_AGENTS))
    parser.add_argument("--mode", required=True, choices=sorted(SUPPORTED_MODES))
    parser.add_argument(
        "--resolution-mode",
        required=True,
        choices=("launch", "metadata"),
    )
    parser.add_argument("--output-policy", required=True)
    parser.add_argument("--output-metadata", required=True)
    parser.add_argument("--output-root", required=True)
    return parser.parse_args()


def materialize_file(source: Path, destination: Path) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(dir=destination.parent, delete=False) as handle:
        temp_path = Path(handle.name)
    try:
        shutil.copyfile(source, temp_path)
        temp_path.chmod(0o600)
        temp_path.replace(destination)
        destination.chmod(0o600)
    finally:
        try:
            temp_path.unlink()
        except FileNotFoundError:
            pass


def write_placeholder(destination: Path, resolver: str) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(
        dir=destination.parent,
        delete=False,
        mode="w",
        encoding="utf-8",
    ) as handle:
        temp_path = Path(handle.name)
        handle.write(
            json.dumps(
                {
                    "workcell": "metadata-only",
                    "resolver": resolver,
                },
                sort_keys=True,
            )
            + "\n"
        )
    try:
        temp_path.chmod(0o600)
        temp_path.replace(destination)
        destination.chmod(0o600)
    finally:
        try:
            temp_path.unlink()
        except FileNotFoundError:
            pass


def atomic_write_text(destination: Path, content: str) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile(
        dir=destination.parent,
        delete=False,
        mode="w",
        encoding="utf-8",
    ) as handle:
        temp_path = Path(handle.name)
        handle.write(content)
    try:
        temp_path.chmod(0o600)
        temp_path.replace(destination)
        destination.chmod(0o600)
    finally:
        try:
            temp_path.unlink()
        except FileNotFoundError:
            pass


def resolve_claude_macos_keychain(
    destination: Path,
    resolution_mode: str,
) -> str:
    test_export_path = os.environ.get(TEST_CLAUDE_EXPORT_ENV, "")
    if test_export_path:
        source = require_secret_file(
            validate_source_path(
                test_export_path,
                TEST_CLAUDE_EXPORT_ENV,
                Path.cwd(),
            ),
            TEST_CLAUDE_EXPORT_ENV,
        )
        materialize_file(source, destination)
        return "resolved"
    if resolution_mode == "metadata":
        write_placeholder(destination, "claude-macos-keychain")
        return "configured-only"
    die(
        "Claude macOS login reuse is configured but no supported export path is "
        "available. Use claude_api_key or remove credentials.claude_auth."
    )


def resolve_credential(
    key: str,
    resolver_name: str,
    destination: Path,
    resolution_mode: str,
) -> str:
    if key == "claude_auth" and resolver_name == "claude-macos-keychain":
        return resolve_claude_macos_keychain(destination, resolution_mode)
    die(f"unsupported credential resolver: {resolver_name}")


def main() -> int:
    args = parse_args()
    policy_path = Path(args.policy).expanduser().resolve()
    output_policy_path = Path(args.output_policy).expanduser().resolve()
    output_metadata_path = Path(args.output_metadata).expanduser().resolve()
    output_root = Path(args.output_root).expanduser().resolve()

    policy, policy_sources = load_policy_bundle(policy_path)
    policy = rebase_policy_fragment(policy, policy_path.parent)
    credentials = policy.get("credentials", {})
    if credentials is None:
        credentials = {}
    if not isinstance(credentials, dict):
        die("credentials must be a TOML table")

    relevant_keys = SHARED_CREDENTIAL_KEYS | AGENT_SCOPED_CREDENTIAL_KEYS.get(args.agent, set())
    metadata = {
        "policy_entrypoint": logical_policy_path(policy_path, policy_path.parent),
        "policy_sources": policy_sources,
        "credential_input_kinds": {},
        "credential_resolvers": {},
        "credential_materialization": {},
        "credential_resolution_states": {},
    }

    for key in sorted(relevant_keys):
        raw = credentials.get(key)
        if raw is None:
            continue
        if not isinstance(raw, dict):
            metadata["credential_input_kinds"][key] = "source"
            metadata["credential_resolution_states"][key] = "source"
            continue

        validate_allowed_keys(raw, RESOLVER_ALLOWED_KEYS, f"credentials.{key}")
        resolver_name = raw.get("resolver")
        providers = raw.get("providers")
        modes = raw.get("modes")
        if not selected_for(
            providers,
            args.agent,
            f"credentials.{key}.providers",
            SUPPORTED_AGENTS,
        ):
            if resolver_name is not None:
                credentials.pop(key, None)
            continue
        if not selected_for(
            modes,
            args.mode,
            f"credentials.{key}.modes",
            SUPPORTED_MODES,
        ):
            if resolver_name is not None:
                credentials.pop(key, None)
            continue

        source_raw = raw.get("source")
        if source_raw is not None and resolver_name is not None:
            die(f"credentials.{key} must not declare both source and resolver")
        if source_raw is None and resolver_name is None:
            die(f"credentials.{key} must declare source or resolver")
        if resolver_name is None:
            metadata["credential_input_kinds"][key] = "source"
            metadata["credential_resolution_states"][key] = "source"
            continue
        if key not in ALLOWED_RESOLVERS or resolver_name not in ALLOWED_RESOLVERS[key]:
            die(f"credentials.{key}.resolver is unsupported: {resolver_name}")
        materialization = raw.get("materialization", "ephemeral")
        if materialization not in SUPPORTED_MATERIALIZATION:
            die(
                f"credentials.{key}.materialization must be one of: "
                f"{', '.join(sorted(SUPPORTED_MATERIALIZATION))}"
            )
        if materialization != "ephemeral":
            die(
                f"credentials.{key}.materialization must stay ephemeral for resolver-backed auth"
            )

        destination = output_root / "resolved" / "credentials" / f"{key}.json"
        state = resolve_credential(key, resolver_name, destination, args.resolution_mode)
        rewritten = dict(raw)
        rewritten.pop("resolver", None)
        rewritten.pop("materialization", None)
        rewritten["source"] = str(destination)
        credentials[key] = rewritten

        metadata["credential_input_kinds"][key] = "resolver"
        metadata["credential_resolvers"][key] = resolver_name
        metadata["credential_materialization"][key] = materialization
        metadata["credential_resolution_states"][key] = state

    atomic_write_text(output_policy_path, render_policy_toml(policy))
    atomic_write_text(
        output_metadata_path,
        json.dumps(metadata, sort_keys=True, indent=2) + "\n",
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
