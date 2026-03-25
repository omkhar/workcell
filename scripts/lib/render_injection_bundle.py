#!/usr/bin/env python3
"""Render a validated Workcell injection policy into a staged session bundle."""

from __future__ import annotations

import argparse
import ast
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import stat
import sys
from typing import NoReturn


SUPPORTED_AGENTS = {"codex", "claude", "gemini"}
SUPPORTED_MODES = {"strict", "build", "breakglass"}
SUPPORTED_CLASSIFICATIONS = {"public", "secret"}
RESERVED_SSH_FILENAMES = {"config", "known_hosts"}
RISKY_SSH_DIRECTIVES = {
    "identityagent",
    "include",
    "localcommand",
    "proxycommand",
}
SESSION_HOME_ROOT = PurePosixPath("/state/agent-home")
RUN_INJECTED_ROOT = PurePosixPath("/state/injected")
DIRECT_MOUNT_ROOT = PurePosixPath("/opt/workcell/host-inputs")
SYSTEM_SYMLINK_ALLOWLIST = (
    {Path("/var"), Path("/tmp")} if sys.platform == "darwin" else set()
)
RESERVED_TARGETS = (
    "/state/agent-home/.codex/AGENTS.md",
    "/state/agent-home/.codex/auth.json",
    "/state/agent-home/.codex/config.toml",
    "/state/agent-home/.codex/managed_config.toml",
    "/state/agent-home/.codex/requirements.toml",
    "/state/agent-home/.codex/agents",
    "/state/agent-home/.codex/rules",
    "/state/agent-home/.codex/mcp",
    "/state/agent-home/.claude/settings.json",
    "/state/agent-home/.claude/CLAUDE.md",
    "/state/agent-home/.claude/workcell",
    "/state/agent-home/.config/claude-code/auth.json",
    "/state/agent-home/.mcp.json",
    "/state/agent-home/.gemini/settings.json",
    "/state/agent-home/.gemini/GEMINI.md",
    "/state/agent-home/.gemini/.env",
    "/state/agent-home/.gemini/oauth_creds.json",
    "/state/agent-home/.gemini/projects.json",
    "/state/agent-home/.config/gcloud/application_default_credentials.json",
    "/state/agent-home/.config/gh/config.yml",
    "/state/agent-home/.config/gh/hosts.yml",
    "/state/agent-home/.ssh",
)

CREDENTIAL_CONTAINER_PATHS = {
    "codex_auth": f"{DIRECT_MOUNT_ROOT}/credentials/codex-auth.json",
    "claude_auth": f"{DIRECT_MOUNT_ROOT}/credentials/claude-auth.json",
    "claude_api_key": f"{DIRECT_MOUNT_ROOT}/credentials/claude-api-key.txt",
    "claude_mcp": f"{DIRECT_MOUNT_ROOT}/credentials/claude-mcp.json",
    "gemini_env": f"{DIRECT_MOUNT_ROOT}/credentials/gemini.env",
    "gemini_oauth": f"{DIRECT_MOUNT_ROOT}/credentials/gemini-oauth.json",
    "gemini_projects": f"{DIRECT_MOUNT_ROOT}/credentials/gemini-projects.json",
    "gcloud_adc": f"{DIRECT_MOUNT_ROOT}/credentials/gcloud-adc.json",
    "github_hosts": f"{DIRECT_MOUNT_ROOT}/credentials/github-hosts.yml",
    "github_config": f"{DIRECT_MOUNT_ROOT}/credentials/github-config.yml",
}

AGENT_SCOPED_CREDENTIAL_KEYS = {
    "codex": {"codex_auth"},
    "claude": {"claude_api_key", "claude_auth", "claude_mcp"},
    "gemini": {"gemini_env", "gemini_oauth", "gemini_projects", "gcloud_adc"},
}

SHARED_CREDENTIAL_KEYS = {"github_hosts", "github_config"}


def die(message: str) -> NoReturn:
    raise SystemExit(message)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--policy", required=True)
    parser.add_argument("--agent", required=True, choices=sorted(SUPPORTED_AGENTS))
    parser.add_argument("--mode", required=True, choices=sorted(SUPPORTED_MODES))
    parser.add_argument("--output-root", required=True)
    return parser.parse_args()


def expand_host_path(raw: str, base: Path) -> Path:
    expanded = Path(os.path.expanduser(raw))
    if not expanded.is_absolute():
        expanded = base / expanded
    return Path(os.path.abspath(os.fspath(expanded)))


def normalize_container_target(raw: str) -> PurePosixPath:
    if raw.startswith("~/"):
        candidate = SESSION_HOME_ROOT / raw[2:]
    else:
        candidate = PurePosixPath(raw)
    if not candidate.is_absolute():
        die(f"injection target must be absolute or use ~/ syntax: {raw}")
    if ".." in candidate.parts:
        die(f"injection target may not contain '..': {raw}")
    return candidate


def target_is_under(candidate: PurePosixPath, root: PurePosixPath) -> bool:
    if candidate == root:
        return True
    try:
        candidate.relative_to(root)
    except ValueError:
        return False
    return True


def target_is_reserved(candidate: PurePosixPath) -> bool:
    text = str(candidate)
    for reserved in RESERVED_TARGETS:
        if text == reserved or text.startswith(f"{reserved}/"):
            return True
    return False


def validate_container_target(candidate: PurePosixPath) -> str:
    if not (
        target_is_under(candidate, SESSION_HOME_ROOT)
        or target_is_under(candidate, RUN_INJECTED_ROOT)
    ):
        die(
            "injection target must stay under /state/agent-home or /state/injected: "
            f"{candidate}"
        )
    if target_is_reserved(candidate):
        die(
            "injection target collides with a Workcell-managed control-plane path: "
            f"{candidate}"
        )
    return str(candidate)


def classification_modes(classification: str, is_dir: bool) -> tuple[str, str]:
    if classification not in SUPPORTED_CLASSIFICATIONS:
        die(f"unsupported injection classification: {classification}")
    if classification == "secret":
        return ("0600", "0700") if not is_dir else ("0600", "0700")
    return ("0644", "0755") if not is_dir else ("0644", "0755")


def validate_allowed_keys(table: dict, allowed_keys: set[str], label: str) -> None:
    unknown = sorted(set(table) - allowed_keys)
    if unknown:
        die(f"{label} contains unsupported keys: {', '.join(unknown)}")


def selected_for(
    values: object, current: str, label: str, allowed_values: set[str]
) -> bool:
    if values is None:
        return True
    if not isinstance(values, list) or not values:
        die(f"{label} must be a non-empty array when specified")
    normalized = []
    for value in values:
        if not isinstance(value, str):
            die(f"{label} values must be strings")
        if value not in allowed_values:
            die(f"{label} contains unsupported value: {value}")
        normalized.append(value)
    return current in normalized


def ensure_no_symlinks_within(root: Path) -> None:
    for path in root.rglob("*"):
        if path.is_symlink():
            die(f"directory injections must not contain symlinks: {path}")


def copy_source(source: Path, destination: Path) -> str:
    if source.is_file():
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, destination)
        destination.chmod(0o600)
        return "file"
    if source.is_dir():
        ensure_no_symlinks_within(source)
        shutil.copytree(source, destination, dirs_exist_ok=False)
        for path in destination.rglob("*"):
            if path.is_dir():
                path.chmod(0o700)
            else:
                path.chmod(0o600)
        destination.chmod(0o700)
        return "dir"
    die(f"injection source must be a file or directory: {source}")


def stage_file(source: Path, output_root: Path, relpath: str) -> str:
    destination = output_root / relpath
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
    destination.chmod(0o600)
    return relpath


def direct_mount_entry(source: Path, mount_path: str) -> dict[str, str]:
    return {
        "source": str(source),
        "mount_path": mount_path,
    }


def validate_source_path(raw: object, label: str, base: Path) -> Path:
    if not isinstance(raw, str) or not raw:
        die(f"{label} must be a non-empty string path")
    source = expand_host_path(raw, base)
    if not source.exists():
        die(f"{label} does not exist: {source}")
    require_no_symlink_in_path_chain(source, label)
    return source


def require_no_symlink(path: Path, label: str) -> None:
    if path.is_symlink():
        die(f"{label} must not be a symlink: {path}")


def require_no_symlink_in_path_chain(path: Path, label: str) -> None:
    current = path
    while True:
        if current.is_symlink() and current not in SYSTEM_SYMLINK_ALLOWLIST:
            die(f"{label} must not be a symlink: {current}")
        if current.parent == current:
            return
        current = current.parent


def require_secret_owner_only(path: Path, label: str) -> None:
    path_stat = path.lstat()
    expected_uid = os.getuid()
    if path_stat.st_uid != expected_uid:
        die(f"{label} must be owned by uid {expected_uid}: {path}")
    if stat.S_IMODE(path_stat.st_mode) & 0o077:
        die(f"{label} must not be group/world-accessible: {path}")


def validate_secret_file(source: Path, label: str) -> Path:
    require_no_symlink(source, label)
    if not source.is_file():
        die(f"{label} must point at a file: {source}")
    require_secret_owner_only(source, label)
    return source


def validate_secret_tree(source: Path, label: str) -> Path:
    require_no_symlink(source, label)
    if source.is_file():
        return validate_secret_file(source, label)
    if not source.is_dir():
        die(f"{label} must point at a file or directory: {source}")
    require_secret_owner_only(source, label)
    ensure_no_symlinks_within(source)
    for child in source.rglob("*"):
        require_no_symlink(child, label)
        require_secret_owner_only(child, label)
    return source


def validate_known_hosts_file(source: Path, label: str) -> Path:
    require_no_symlink(source, label)
    if not source.is_file():
        die(f"{label} must point at a file: {source}")
    path_stat = source.lstat()
    if stat.S_IMODE(path_stat.st_mode) & 0o022:
        die(f"{label} must not be group/world-writable: {source}")
    return source


def parse_ssh_directive(line: str) -> tuple[str, str] | None:
    stripped = line.strip()
    if not stripped or stripped.startswith("#"):
        return None
    parts = stripped.split(None, 1)
    directive = parts[0].lower()
    remainder = parts[1] if len(parts) > 1 else ""
    return directive, remainder


def validate_ssh_config_safety(source: Path, allow_unsafe: bool) -> None:
    if allow_unsafe:
        return
    for lineno, raw_line in enumerate(
        source.read_text(encoding="utf-8").splitlines(),
        start=1,
    ):
        parsed = parse_ssh_directive(raw_line)
        if parsed is None:
            continue
        directive, remainder = parsed
        if directive in RISKY_SSH_DIRECTIVES:
            die(
                f"ssh.config contains unsafe directive {directive!r} at line {lineno}; "
                "set ssh.allow_unsafe_config = true only when you explicitly accept lower assurance"
            )
        if directive == "match" and " exec " in f" {remainder.lower()} ":
            die(
                "ssh.config contains unsafe Match exec at line "
                f"{lineno}; set ssh.allow_unsafe_config = true only when you explicitly accept lower assurance"
            )


def policy_sha256(policy_path: Path) -> str:
    return f"sha256:{hashlib.sha256(policy_path.read_bytes()).hexdigest()}"


def load_policy(policy_path: Path) -> dict:
    loaded = parse_toml_subset(policy_path.read_text(encoding="utf-8"), policy_path)
    if not isinstance(loaded, dict):
        die(f"injection policy must decode to a TOML table: {policy_path}")
    validate_allowed_keys(
        loaded, {"version", "documents", "ssh", "copies", "credentials"}, "root policy"
    )
    version = loaded.get("version", 1)
    if version != 1:
        die(f"unsupported injection policy version: {version}")
    return loaded


def strip_comment(line: str) -> str:
    escaped = False
    in_string = False
    result = []

    for char in line:
        if escaped:
            result.append(char)
            escaped = False
            continue
        if char == "\\" and in_string:
            result.append(char)
            escaped = True
            continue
        if char == '"':
            in_string = not in_string
            result.append(char)
            continue
        if char == "#" and not in_string:
            break
        result.append(char)
    return "".join(result).strip()


def parse_value(raw: str, policy_path: Path, lineno: int) -> object:
    value = raw.strip()
    if not value:
        die(f"{policy_path}:{lineno}: expected a value")
    if value in ("true", "false"):
        return value == "true"
    if value.startswith('"') and value.endswith('"'):
        return ast.literal_eval(value)
    if value.startswith("[") and value.endswith("]"):
        parsed = ast.literal_eval(value)
        if not isinstance(parsed, list):
            die(f"{policy_path}:{lineno}: expected an array value")
        if not all(isinstance(item, str) for item in parsed):
            die(f"{policy_path}:{lineno}: only arrays of strings are supported")
        return parsed
    if value.isdigit():
        return int(value)
    die(
        f"{policy_path}:{lineno}: unsupported TOML value; use quoted strings, booleans, integers, or arrays of strings"
    )


def parse_toml_subset(content: str, policy_path: Path) -> dict:
    root: dict[str, object] = {}
    current: dict[str, object] = root

    for lineno, raw_line in enumerate(content.splitlines(), start=1):
        line = strip_comment(raw_line)
        if not line:
            continue

        if line.startswith("[[") and line.endswith("]]"):
            table_name = line[2:-2].strip()
            if table_name != "copies":
                die(f"{policy_path}:{lineno}: unsupported array-of-table [{table_name}]")
            copies = root.setdefault("copies", [])
            if not isinstance(copies, list):
                die(f"{policy_path}:{lineno}: copies must remain an array of tables")
            entry: dict[str, object] = {}
            copies.append(entry)
            current = entry
            continue

        if line.startswith("[") and line.endswith("]"):
            table_name = line[1:-1].strip()
            if table_name.startswith("credentials."):
                credential_key = table_name.split(".", 1)[1]
                if credential_key not in CREDENTIAL_CONTAINER_PATHS:
                    die(
                        f"{policy_path}:{lineno}: unsupported credentials table "
                        f"[{table_name}]"
                    )
                credentials = root.setdefault("credentials", {})
                if not isinstance(credentials, dict):
                    die(f"{policy_path}:{lineno}: credentials must remain a table")
                entry = credentials.setdefault(credential_key, {})
                if not isinstance(entry, dict):
                    die(
                        f"{policy_path}:{lineno}: credentials.{credential_key} must remain a table"
                    )
                current = entry
                continue
            if table_name not in {"documents", "ssh", "credentials"}:
                die(f"{policy_path}:{lineno}: unsupported table [{table_name}]")
            table = root.setdefault(table_name, {})
            if not isinstance(table, dict):
                die(f"{policy_path}:{lineno}: {table_name} must remain a table")
            current = table
            continue

        if "=" not in line:
            die(f"{policy_path}:{lineno}: expected key = value")

        key, value = line.split("=", 1)
        key = key.strip()
        if not key:
            die(f"{policy_path}:{lineno}: empty key")
        current[key] = parse_value(value, policy_path, lineno)

    return root


def render_documents(policy: dict, output_root: Path, policy_dir: Path) -> dict:
    documents = policy.get("documents", {})
    if documents is None:
        return {}
    if not isinstance(documents, dict):
        die("documents must be a TOML table")
    validate_allowed_keys(documents, {"common", "codex", "claude", "gemini"}, "documents")

    rendered: dict[str, str] = {}
    for key, relpath in (
        ("common", "documents/common.md"),
        ("codex", "documents/codex.md"),
        ("claude", "documents/claude.md"),
        ("gemini", "documents/gemini.md"),
    ):
        raw = documents.get(key)
        if raw is None:
            continue
        source = validate_source_path(raw, f"documents.{key}", policy_dir)
        if not source.is_file():
            die(f"documents.{key} must point at a file: {source}")
        rendered[key] = stage_file(source, output_root, relpath)
    return rendered


def render_copies(
    policy: dict,
    output_root: Path,
    policy_dir: Path,
    agent: str,
    mode: str,
) -> list[dict]:
    copies = policy.get("copies", [])
    if copies is None:
        return []
    if not isinstance(copies, list):
        die("copies must be a TOML array of tables")

    rendered: list[dict] = []
    copy_index = 0
    for entry in copies:
        if not isinstance(entry, dict):
            die("each copies entry must be a table")
        validate_allowed_keys(
            entry,
            {"source", "target", "classification", "providers", "modes"},
            "copies entry",
        )
        if not selected_for(
            entry.get("providers"), agent, "copies.providers", SUPPORTED_AGENTS
        ):
            continue
        if not selected_for(entry.get("modes"), mode, "copies.modes", SUPPORTED_MODES):
            continue
        source = validate_source_path(entry.get("source"), "copies.source", policy_dir)
        target = validate_container_target(
            normalize_container_target(str(entry.get("target", "")))
        )
        if "classification" not in entry:
            die("copies.classification is required")
        classification = entry.get("classification")
        if not isinstance(classification, str):
            die("copies.classification must be a string")
        relpath = f"copies/{copy_index}"
        mount_path = f"{DIRECT_MOUNT_ROOT}/copies/{copy_index}"
        copy_index += 1
        if source.is_dir():
            ensure_no_symlinks_within(source)
        staged_kind = "dir" if source.is_dir() else "file"
        file_mode, dir_mode = classification_modes(classification, is_dir=(staged_kind == "dir"))
        rendered_source: dict[str, str] | str
        if classification == "secret":
            validate_secret_tree(source, "copies.source")
            rendered_source = direct_mount_entry(source, mount_path)
        else:
            staged_kind = copy_source(source, output_root / relpath)
            rendered_source = relpath
        rendered.append(
            {
                "source": rendered_source,
                "target": target,
                "kind": staged_kind,
                "file_mode": file_mode,
                "dir_mode": dir_mode,
                "classification": classification,
            }
        )
    return rendered


def render_ssh(
    policy: dict,
    output_root: Path,
    policy_dir: Path,
    agent: str,
    mode: str,
) -> dict:
    ssh = policy.get("ssh", {})
    if ssh is None:
        return {}
    if not isinstance(ssh, dict):
        die("ssh must be a TOML table")
    validate_allowed_keys(
        ssh,
        {
            "enabled",
            "config",
            "known_hosts",
            "identities",
            "providers",
            "modes",
            "allow_unsafe_config",
        },
        "ssh",
    )

    enabled = ssh.get("enabled")
    has_material = any(key in ssh for key in ("config", "known_hosts", "identities"))
    if enabled is False or (enabled is None and not has_material):
        return {}
    if enabled is not None and not isinstance(enabled, bool):
        die("ssh.enabled must be a boolean when specified")
    if not selected_for(ssh.get("providers"), agent, "ssh.providers", SUPPORTED_AGENTS):
        return {}
    if not selected_for(ssh.get("modes"), mode, "ssh.modes", SUPPORTED_MODES):
        return {}

    rendered: dict[str, object] = {"identities": []}
    allow_unsafe_config = ssh.get("allow_unsafe_config", False)
    if not isinstance(allow_unsafe_config, bool):
        die("ssh.allow_unsafe_config must be a boolean when specified")
    config = ssh.get("config")
    if config is None:
        rendered["config_assurance"] = "no-config"
    elif allow_unsafe_config:
        rendered["config_assurance"] = "lower-assurance-unsafe-config"
    else:
        rendered["config_assurance"] = "safe"
    if config is not None:
        source = validate_secret_file(
            validate_source_path(config, "ssh.config", policy_dir),
            "ssh.config",
        )
        validate_ssh_config_safety(source, allow_unsafe_config)
        rendered["config"] = direct_mount_entry(source, f"{DIRECT_MOUNT_ROOT}/ssh/config")

    known_hosts = ssh.get("known_hosts")
    if known_hosts is not None:
        source = validate_known_hosts_file(
            validate_source_path(known_hosts, "ssh.known_hosts", policy_dir),
            "ssh.known_hosts",
        )
        rendered["known_hosts"] = direct_mount_entry(
            source, f"{DIRECT_MOUNT_ROOT}/ssh/known_hosts"
        )

    identities = ssh.get("identities", [])
    if identities is None:
        identities = []
    if not isinstance(identities, list):
        die("ssh.identities must be an array of paths")
    rendered_identities: list[dict[str, str]] = []
    seen_identity_targets: set[str] = set()
    for index, raw in enumerate(identities):
        source = validate_secret_file(
            validate_source_path(raw, f"ssh.identities[{index}]", policy_dir),
            f"ssh.identities[{index}]",
        )
        if source.name in RESERVED_SSH_FILENAMES:
            die(
                f"ssh.identities[{index}] basename collides with a reserved SSH file: {source.name}"
            )
        if source.name in seen_identity_targets:
            die(
                f"ssh.identities contains duplicate target basename: {source.name}"
            )
        seen_identity_targets.add(source.name)
        rendered_identities.append(
            {
                "source": str(source),
                "mount_path": f"{DIRECT_MOUNT_ROOT}/ssh/identity-{index}",
                "target_name": source.name,
            }
        )
    rendered["identities"] = rendered_identities
    return rendered


def render_credentials(
    policy: dict,
    policy_dir: Path,
    agent: str,
    mode: str,
) -> dict:
    credentials = policy.get("credentials", {})
    if credentials is None:
        return {}
    if not isinstance(credentials, dict):
        die("credentials must be a TOML table")
    validate_allowed_keys(credentials, set(CREDENTIAL_CONTAINER_PATHS), "credentials")

    relevant_keys = SHARED_CREDENTIAL_KEYS | AGENT_SCOPED_CREDENTIAL_KEYS.get(agent, set())
    rendered: dict[str, dict[str, str]] = {}

    for key in sorted(relevant_keys):
        raw = credentials.get(key)
        if raw is None:
            continue
        providers = None
        modes = None
        source_raw = raw
        if isinstance(raw, dict):
            validate_allowed_keys(
                raw,
                {"source", "providers", "modes"},
                f"credentials.{key}",
            )
            source_raw = raw.get("source")
            providers = raw.get("providers")
            modes = raw.get("modes")
        elif not isinstance(raw, str):
            die(f"credentials.{key} must be a string path or a table")
        if not selected_for(
            providers,
            agent,
            f"credentials.{key}.providers",
            SUPPORTED_AGENTS,
        ):
            continue
        if not selected_for(
            modes,
            mode,
            f"credentials.{key}.modes",
            SUPPORTED_MODES,
        ):
            continue
        source = validate_secret_file(
            validate_source_path(source_raw, f"credentials.{key}", policy_dir),
            f"credentials.{key}",
        )
        rendered[key] = {
            "source": str(source),
            "mount_path": CREDENTIAL_CONTAINER_PATHS[key],
        }

    return rendered


def main() -> int:
    args = parse_args()
    policy_path = Path(args.policy).expanduser().resolve()
    output_root = Path(args.output_root).expanduser().resolve()
    output_root.mkdir(parents=True, exist_ok=True)
    output_root.chmod(0o700)

    policy = load_policy(policy_path)
    rendered_documents = render_documents(policy, output_root, policy_path.parent)
    rendered_copies = render_copies(
        policy, output_root, policy_path.parent, args.agent, args.mode
    )
    rendered_credentials = render_credentials(
        policy, policy_path.parent, args.agent, args.mode
    )
    rendered_ssh = render_ssh(policy, output_root, policy_path.parent, args.agent, args.mode)
    manifest = {
        "version": 1,
        "metadata": {
            "policy_sha256": policy_sha256(policy_path),
            "credential_keys": sorted(rendered_credentials),
            "secret_copy_targets": sorted(
                entry["target"]
                for entry in rendered_copies
                if entry.get("classification") == "secret"
            ),
            "ssh_enabled": bool(rendered_ssh),
            "ssh_config_assurance": rendered_ssh.get("config_assurance", "off")
            if rendered_ssh
            else "off",
        },
        "documents": rendered_documents,
        "copies": rendered_copies,
        "credentials": rendered_credentials,
        "ssh": rendered_ssh,
    }

    manifest_path = output_root / "manifest.json"
    manifest_path.write_text(json.dumps(manifest, sort_keys=True, indent=2) + "\n", encoding="utf-8")
    manifest_path.chmod(0o600)
    print(manifest_path)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
