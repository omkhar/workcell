#!/usr/bin/env python3
"""Manage Workcell injection policy files for host-side auth commands."""

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
    CREDENTIAL_KEYS,
    SHARED_CREDENTIAL_KEYS,
    SUPPORTED_AGENTS,
    SUPPORTED_MODES,
    composite_policy_sha256,
    die,
    load_policy_bundle,
    load_raw_policy,
    require_no_symlink_in_path_chain,
    require_path_within,
    require_secret_file,
    selected_for,
    validate_allowed_keys,
    validate_source_path,
    write_policy_file,
)


CANONICAL_CREDENTIAL_DESTINATIONS = {
    "codex_auth": ("codex", "auth.json"),
    "claude_api_key": ("claude", "api-key.txt"),
    "claude_mcp": ("claude", "mcp.json"),
    "gemini_env": ("gemini", "gemini.env"),
    "gemini_oauth": ("gemini", "oauth_creds.json"),
    "gemini_projects": ("gemini", "projects.json"),
    "gcloud_adc": ("gemini", "gcloud-adc.json"),
    "github_hosts": ("shared", "github-hosts.yml"),
    "github_config": ("shared", "github-config.yml"),
}
ALLOWED_RESOLVERS = {
    "claude_auth": {"claude-macos-keychain"},
}
STATUS_ORDER = {
    "codex": ["codex_auth"],
    "claude": ["claude_api_key", "claude_auth"],
    "gemini": ["gemini_env", "gemini_oauth"],
}
ENTRY_ALLOWED_KEYS = {"source", "resolver", "materialization", "providers", "modes"}
MANAGED_ROOT_MARKER = ".workcell-managed-root"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)

    init_parser = subparsers.add_parser("init")
    init_parser.add_argument("--policy", required=True)
    init_parser.add_argument("--managed-root", required=True)

    set_parser = subparsers.add_parser("set")
    set_parser.add_argument("--policy", required=True)
    set_parser.add_argument("--managed-root", required=True)
    set_parser.add_argument("--agent", required=True, choices=sorted(SUPPORTED_AGENTS))
    set_parser.add_argument("--credential", required=True, choices=sorted(CREDENTIAL_KEYS))
    set_parser.add_argument("--source")
    set_parser.add_argument("--source-base")
    set_parser.add_argument("--resolver")
    set_parser.add_argument("--ack-host-resolver", action="store_true")

    unset_parser = subparsers.add_parser("unset")
    unset_parser.add_argument("--policy", required=True)
    unset_parser.add_argument("--managed-root", required=True)
    unset_parser.add_argument("--credential", required=True, choices=sorted(CREDENTIAL_KEYS))

    status_parser = subparsers.add_parser("status")
    status_parser.add_argument("--policy", required=True)
    status_parser.add_argument("--agent", choices=sorted(SUPPORTED_AGENTS))
    status_parser.add_argument("--mode", default="strict", choices=sorted(SUPPORTED_MODES))
    return parser.parse_args()


def ensure_directory(path: Path) -> None:
    path.mkdir(parents=True, exist_ok=True)
    path.chmod(0o700)


def validate_managed_path(managed_root: Path, path: Path, label: str) -> None:
    require_no_symlink_in_path_chain(path, label)
    require_path_within(managed_root, path, label)


def write_managed_root_marker(managed_root: Path) -> None:
    ensure_directory(managed_root)
    marker_path = managed_root / MANAGED_ROOT_MARKER
    with tempfile.NamedTemporaryFile(
        dir=managed_root,
        prefix=f"{MANAGED_ROOT_MARKER}.",
        suffix=".tmp",
        delete=False,
        mode="w",
        encoding="utf-8",
    ) as handle:
        temp_path = Path(handle.name)
        handle.write("managed_by=workcell\n")
    try:
        temp_path.chmod(0o600)
        temp_path.replace(marker_path)
        marker_path.chmod(0o600)
    finally:
        cleanup_staged_file(temp_path)


def is_workcell_managed_root(path: Path) -> bool:
    return (path / MANAGED_ROOT_MARKER).is_file()


def stage_file_copy(source: Path, destination: Path) -> Path:
    ensure_directory(destination.parent)
    with tempfile.NamedTemporaryFile(dir=destination.parent, delete=False) as handle:
        temp_path = Path(handle.name)
    try:
        shutil.copyfile(source, temp_path)
        temp_path.chmod(0o600)
    except Exception:
        cleanup_staged_file(temp_path)
        raise
    return temp_path


def install_staged_file(staged_path: Path, destination: Path) -> None:
    staged_path.replace(destination)
    destination.chmod(0o600)


def cleanup_staged_file(path: Path | None) -> None:
    if path is None:
        return
    try:
        path.unlink()
    except FileNotFoundError:
        pass


def stage_existing_file(path: Path | None) -> Path | None:
    if path is None or not path.exists():
        return None
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as handle:
        backup_path = Path(handle.name)
    cleanup_staged_file(backup_path)
    path.replace(backup_path)
    return backup_path


def restore_staged_file(staged_path: Path | None, destination: Path | None) -> None:
    if staged_path is None or destination is None:
        return
    staged_path.replace(destination)
    destination.chmod(0o600)


def canonical_destination_path(managed_root: Path, credential: str) -> Path:
    if credential not in CANONICAL_CREDENTIAL_DESTINATIONS:
        die(f"workcell auth set does not manage {credential} automatically")
    provider_dir, filename = CANONICAL_CREDENTIAL_DESTINATIONS[credential]
    return managed_root / provider_dir / filename


def load_mutable_policy(policy_path: Path) -> dict[str, object]:
    policy = load_raw_policy(policy_path)
    if "version" not in policy:
        policy["version"] = 1
    return policy


def ensure_credential_not_only_in_includes(
    policy_path: Path,
    credentials: dict[str, object],
    credential: str,
    command: str,
) -> None:
    if credential in credentials or not policy_path.exists():
        return
    merged_policy, _ = load_policy_bundle(policy_path)
    merged_credentials = merged_policy.get("credentials", {})
    if isinstance(merged_credentials, dict) and credential in merged_credentials:
        die(
            f"credentials.{credential} is declared by an included policy fragment; "
            f"update that fragment directly before using workcell auth {command}"
        )


def desired_selectors(
    credentials: dict[str, object],
    credential: str,
    agent: str,
) -> dict[str, object]:
    existing = credentials.get(credential)
    if not isinstance(existing, dict):
        if credential in SHARED_CREDENTIAL_KEYS:
            return {"providers": [agent]}
        return {}
    validate_allowed_keys(existing, ENTRY_ALLOWED_KEYS, f"credentials.{credential}")
    selectors: dict[str, object] = {}
    if "modes" in existing:
        selectors["modes"] = existing["modes"]
    if credential in SHARED_CREDENTIAL_KEYS:
        if "providers" in existing:
            selectors["providers"] = existing["providers"]
        else:
            selectors["providers"] = [agent]
    elif "providers" in existing:
        selectors["providers"] = existing["providers"]
    return selectors


def managed_source_path_for_entry(
    managed_root: Path,
    credential: str,
    existing_entry: object,
) -> Path | None:
    if credential not in CANONICAL_CREDENTIAL_DESTINATIONS:
        return None
    provider_dir, filename = CANONICAL_CREDENTIAL_DESTINATIONS[credential]
    candidate = canonical_destination_path(managed_root, credential)
    existing_source = None
    if isinstance(existing_entry, dict):
        existing_source = existing_entry.get("source")
    elif isinstance(existing_entry, str):
        existing_source = existing_entry
    if isinstance(existing_source, str) and existing_source == str(candidate):
        return candidate
    if not isinstance(existing_source, str):
        return None
    existing_path = Path(existing_source).expanduser()
    if existing_path.name != filename or existing_path.parent.name != provider_dir:
        return None
    root_candidate = existing_path.parent.parent
    if is_workcell_managed_root(root_candidate):
        return existing_path
    return None


def validate_agent_credential(agent: str, credential: str) -> None:
    allowed = AGENT_SCOPED_CREDENTIAL_KEYS.get(agent, set()) | SHARED_CREDENTIAL_KEYS
    if credential not in allowed:
        die(f"{credential} is not valid for agent {agent}")


def write_verified_policy(policy_path: Path, policy: dict[str, object]) -> None:
    ensure_directory(policy_path.parent)
    with tempfile.NamedTemporaryFile(
        dir=policy_path.parent,
        prefix=f"{policy_path.name}.",
        suffix=".tmp",
        delete=False,
    ) as handle:
        temp_path = Path(handle.name)
    try:
        write_policy_file(temp_path, policy)
        load_policy_bundle(temp_path)
        temp_path.replace(policy_path)
        policy_path.chmod(0o600)
    finally:
        try:
            temp_path.unlink()
        except FileNotFoundError:
            pass


def command_init(policy_path: Path, managed_root: Path) -> int:
    validate_managed_path(managed_root, managed_root, "managed_root")
    if not policy_path.exists():
        write_verified_policy(policy_path, {"version": 1})
    else:
        load_policy_bundle(policy_path)
    ensure_directory(managed_root)
    write_managed_root_marker(managed_root)
    for name in ("codex", "claude", "gemini", "shared"):
        validate_managed_path(managed_root, managed_root / name, f"managed_root/{name}")
        ensure_directory(managed_root / name)
    print(f"policy_path={policy_path}")
    print(f"managed_root={managed_root}")
    return 0


def command_set(
    policy_path: Path,
    managed_root: Path,
    agent: str,
    credential: str,
    source_raw: str | None,
    source_base_raw: str | None,
    resolver: str | None,
    ack_host_resolver: bool,
) -> int:
    validate_agent_credential(agent, credential)
    if bool(source_raw) == bool(resolver):
        die("Specify exactly one of --source or --resolver")
    validate_managed_path(managed_root, managed_root, "managed_root")

    policy = load_mutable_policy(policy_path)
    credentials = policy.setdefault("credentials", {})
    if not isinstance(credentials, dict):
        die("credentials must remain a TOML table")
    ensure_credential_not_only_in_includes(policy_path, credentials, credential, "set")
    existing_entry = credentials.get(credential)
    selectors = desired_selectors(credentials, credential, agent)

    if source_raw is not None:
        source_base = Path(source_base_raw).expanduser().resolve() if source_base_raw else Path.cwd()
        source = require_secret_file(
            validate_source_path(source_raw, f"credentials.{credential}", source_base),
            f"credentials.{credential}",
        )
        destination = canonical_destination_path(managed_root, credential)
        validate_managed_path(
            managed_root,
            destination,
            f"managed credential path for {credential}",
        )
        prior_managed_path = managed_source_path_for_entry(managed_root, credential, existing_entry)
        if prior_managed_path is not None and prior_managed_path != destination:
            require_no_symlink_in_path_chain(
                prior_managed_path,
                f"managed credential path for {credential}",
            )
        previous_destination = stage_existing_file(destination)
        prior_managed_backup = None
        if prior_managed_path is not None and prior_managed_path != destination:
            prior_managed_backup = stage_existing_file(prior_managed_path)
        staged_destination = None
        credentials[credential] = {
            **selectors,
            "source": str(destination),
        }
        try:
            staged_destination = stage_file_copy(source, destination)
            install_staged_file(staged_destination, destination)
            write_verified_policy(policy_path, policy)
            try:
                write_managed_root_marker(managed_root)
            except OSError:
                pass
        except BaseException:
            cleanup_staged_file(staged_destination)
            cleanup_staged_file(destination if destination.exists() else None)
            restore_staged_file(previous_destination, destination)
            restore_staged_file(prior_managed_backup, prior_managed_path)
            raise
        cleanup_staged_file(previous_destination)
        cleanup_staged_file(prior_managed_backup)
        print(f"policy_path={policy_path}")
        print(f"credential={credential}")
        print(f"source={source.resolve()}")
        print(f"managed_source={destination}")
        if isinstance(selectors.get("providers"), list):
            print(f"providers={','.join(selectors['providers'])}")
        if isinstance(selectors.get("modes"), list):
            print(f"modes={','.join(selectors['modes'])}")
        return 0

    if credential not in ALLOWED_RESOLVERS or resolver not in ALLOWED_RESOLVERS[credential]:
        die(f"{credential} does not support resolver {resolver}")
    if not ack_host_resolver:
        die("set --resolver requires --ack-host-resolver")
    managed_path = managed_source_path_for_entry(managed_root, credential, existing_entry)
    if managed_path is not None:
        require_no_symlink_in_path_chain(
            managed_path,
            f"managed credential path for {credential}",
        )
    staged_managed_path = stage_existing_file(managed_path)
    credentials[credential] = {
        **selectors,
        "resolver": resolver,
        "materialization": "ephemeral",
    }
    try:
        write_verified_policy(policy_path, policy)
    except BaseException:
        restore_staged_file(staged_managed_path, managed_path)
        raise
    cleanup_staged_file(staged_managed_path)
    print(f"policy_path={policy_path}")
    print(f"credential={credential}")
    print(f"resolver={resolver}")
    print("materialization=ephemeral")
    print("resolver_status=configured-fail-closed")
    if isinstance(selectors.get("providers"), list):
        print(f"providers={','.join(selectors['providers'])}")
    if isinstance(selectors.get("modes"), list):
        print(f"modes={','.join(selectors['modes'])}")
    return 0


def command_unset(policy_path: Path, managed_root: Path, credential: str) -> int:
    validate_managed_path(managed_root, managed_root, "managed_root")
    policy = load_mutable_policy(policy_path)
    credentials = policy.get("credentials")
    ensure_credential_not_only_in_includes(
        policy_path,
        credentials if isinstance(credentials, dict) else {},
        credential,
        "unset",
    )
    existing_entry = credentials.get(credential) if isinstance(credentials, dict) else None
    if not isinstance(credentials, dict) or credential not in credentials:
        print(f"policy_path={policy_path}")
        print(f"credential={credential}")
        print("removed=0")
        return 0
    del credentials[credential]
    if not credentials:
        policy.pop("credentials", None)
    managed_path = managed_source_path_for_entry(managed_root, credential, existing_entry)
    if managed_path is not None:
        require_no_symlink_in_path_chain(
            managed_path,
            f"managed credential path for {credential}",
        )
    staged_managed_path = stage_existing_file(managed_path)
    try:
        write_verified_policy(policy_path, policy)
    except BaseException:
        restore_staged_file(staged_managed_path, managed_path)
        raise
    cleanup_staged_file(staged_managed_path)
    print(f"policy_path={policy_path}")
    print(f"credential={credential}")
    print("removed=1")
    return 0


def selected_credentials(policy: dict, agent: str | None, mode: str) -> dict[str, object]:
    credentials = policy.get("credentials", {})
    if not isinstance(credentials, dict):
        return {}
    if agent is None:
        selected: dict[str, object] = {}
        for key, raw in credentials.items():
            if isinstance(raw, dict):
                validate_status_credential_entry(key, raw)
                validate_selector_values(
                    raw.get("providers"),
                    f"credentials.{key}.providers",
                    SUPPORTED_AGENTS,
                )
                if not selected_for(
                    raw.get("modes"),
                    mode,
                    f"credentials.{key}.modes",
                    SUPPORTED_MODES,
                ):
                    continue
            selected[key] = raw
        return selected

    relevant = SHARED_CREDENTIAL_KEYS | AGENT_SCOPED_CREDENTIAL_KEYS.get(agent, set())
    selected: dict[str, object] = {}
    for key in sorted(relevant):
        raw = credentials.get(key)
        if raw is None:
            continue
        if isinstance(raw, dict):
            validate_status_credential_entry(key, raw)
            if not selected_for(
                raw.get("providers"),
                agent,
                f"credentials.{key}.providers",
                SUPPORTED_AGENTS,
            ):
                continue
            if not selected_for(
                raw.get("modes"),
                mode,
                f"credentials.{key}.modes",
                SUPPORTED_MODES,
            ):
                continue
        selected[key] = raw
    return selected


def credential_input_kind(raw: object) -> str:
    if isinstance(raw, dict) and raw.get("resolver") is not None:
        return "resolver"
    return "source"


def validate_status_credential_entry(key: str, raw: object) -> None:
    if not isinstance(raw, dict):
        return
    validate_allowed_keys(raw, ENTRY_ALLOWED_KEYS, f"credentials.{key}")
    source_raw = raw.get("source")
    resolver = raw.get("resolver")
    providers = raw.get("providers")
    materialization = raw.get("materialization")
    if key in SHARED_CREDENTIAL_KEYS and providers is None:
        die(
            f"credentials.{key}.providers is required so shared GitHub credentials "
            "stay least-privilege"
        )
    if source_raw is not None and resolver is not None:
        die(f"credentials.{key} must not declare both source and resolver")
    if resolver is None:
        if materialization is not None:
            die(
                f"credentials.{key}.materialization is only valid for resolver-backed auth"
            )
        if source_raw is None:
            die(f"credentials.{key} must declare source or resolver")
        return
    if key not in ALLOWED_RESOLVERS or resolver not in ALLOWED_RESOLVERS[key]:
        die(f"credentials.{key}.resolver is unsupported: {resolver}")
    if materialization not in (None, "ephemeral"):
        die(
            f"credentials.{key}.materialization must stay ephemeral for resolver-backed auth"
        )


def validate_selector_values(values: object, label: str, allowed_values: set[str]) -> None:
    if values is None:
        return
    if not isinstance(values, list) or not values:
        die(f"{label} must be a non-empty array when specified")
    for value in values:
        if not isinstance(value, str):
            die(f"{label} values must be strings")
        if value not in allowed_values:
            die(f"{label} contains unsupported value: {value}")


def validate_status_credential_source(key: str, raw: object, policy_base: Path) -> None:
    source_raw = raw
    if isinstance(raw, dict):
        source_raw = raw.get("source")
    if source_raw is None:
        return
    source = validate_source_path(source_raw, f"credentials.{key}", policy_base)
    require_secret_file(source, f"credentials.{key}")


def render_map(value: dict[str, str]) -> str:
    if not value:
        return "none"
    return ",".join(f"{key}:{value[key]}" for key in sorted(value))


def render_modes(keys: list[str]) -> str:
    return ",".join(keys) if keys else "none"


def command_status(policy_path: Path, agent: str | None, mode: str) -> int:
    if not policy_path.exists():
        print("injection_policy=none")
        print(f"default_injection_policy_path={policy_path}")
        print("credential_keys=none")
        print("credential_input_kinds=none")
        print("credential_resolvers=none")
        print("credential_materialization=none")
        print("credential_resolution_states=none")
        if agent is not None:
            print("provider_auth_mode=none")
            print("provider_auth_modes=none")
            print("shared_auth_modes=none")
            print("github_auth_present=0")
        return 0

    policy, policy_sources = load_policy_bundle(policy_path)
    selected = selected_credentials(policy, agent, mode)
    for key, raw in selected.items():
        validate_status_credential_source(key, raw, policy_path.parent)
    input_kinds = {key: credential_input_kind(raw) for key, raw in selected.items()}
    resolvers = {
        key: raw["resolver"]
        for key, raw in selected.items()
        if isinstance(raw, dict) and isinstance(raw.get("resolver"), str)
    }
    materialization = {
        key: raw["materialization"]
        for key, raw in selected.items()
        if isinstance(raw, dict) and isinstance(raw.get("materialization"), str)
    }
    resolution_states = {
        key: ("configured-only" if input_kinds[key] == "resolver" else "source")
        for key in selected
    }

    print(f"policy_source_sha256={composite_policy_sha256(policy_sources)}")
    print(f"credential_keys={render_modes(sorted(selected))}")
    print(f"credential_input_kinds={render_map(input_kinds)}")
    print(f"credential_resolvers={render_map(resolvers)}")
    print(f"credential_materialization={render_map(materialization)}")
    print(f"credential_resolution_states={render_map(resolution_states)}")

    if agent is not None:
        provider_auth_modes = [
            key
            for key in STATUS_ORDER.get(agent, [])
            if key in selected and resolution_states.get(key) != "configured-only"
        ]
        shared_auth_modes = [
            key
            for key in ("github_hosts", "github_config")
            if key in selected and resolution_states.get(key) != "configured-only"
        ]
        provider_auth_mode = provider_auth_modes[0] if provider_auth_modes else "none"
        print(f"provider_auth_mode={provider_auth_mode}")
        print(f"provider_auth_modes={render_modes(provider_auth_modes)}")
        print(f"shared_auth_modes={render_modes(shared_auth_modes)}")
        print(f"github_auth_present={1 if shared_auth_modes else 0}")
    return 0


def main() -> int:
    args = parse_args()
    policy_path = Path(args.policy).expanduser().resolve()

    if args.command == "init":
        managed_root = Path(args.managed_root).expanduser().resolve()
        return command_init(policy_path, managed_root)
    if args.command == "set":
        managed_root = Path(args.managed_root).expanduser().resolve()
        return command_set(
            policy_path,
            managed_root,
            args.agent,
            args.credential,
            args.source,
            args.source_base,
            args.resolver,
            args.ack_host_resolver,
        )
    if args.command == "unset":
        managed_root = Path(args.managed_root).expanduser().resolve()
        return command_unset(policy_path, managed_root, args.credential)
    if args.command == "status":
        return command_status(policy_path, args.agent, args.mode)
    die(f"unsupported command: {args.command}")


if __name__ == "__main__":
    raise SystemExit(main())
