#!/usr/bin/env python3
"""Shared helpers for Workcell injection policy parsing and rendering."""

from __future__ import annotations

import ast
import hashlib
import os
from pathlib import Path
import stat
import sys
from typing import NoReturn


SUPPORTED_AGENTS = {"codex", "claude", "gemini"}
SUPPORTED_MODES = {"strict", "build", "breakglass"}
CREDENTIAL_KEYS = {
    "codex_auth",
    "claude_auth",
    "claude_api_key",
    "claude_mcp",
    "gemini_env",
    "gemini_oauth",
    "gemini_projects",
    "gcloud_adc",
    "github_hosts",
    "github_config",
}
AGENT_SCOPED_CREDENTIAL_KEYS = {
    "codex": {"codex_auth"},
    "claude": {"claude_api_key", "claude_auth", "claude_mcp"},
    "gemini": {"gemini_env", "gemini_oauth", "gemini_projects", "gcloud_adc"},
}
SHARED_CREDENTIAL_KEYS = {"github_hosts", "github_config"}
ALLOWED_ROOT_POLICY_KEYS = {
    "version",
    "includes",
    "documents",
    "ssh",
    "copies",
    "credentials",
}
SYSTEM_SYMLINK_ALLOWLIST = (
    {Path("/var"), Path("/tmp")} if sys.platform == "darwin" else set()
)


def die(message: str) -> NoReturn:
    raise SystemExit(message)


def expand_host_path(raw: str, base: Path) -> Path:
    expanded = Path(os.path.expanduser(raw))
    if not expanded.is_absolute():
        expanded = base / expanded
    return Path(os.path.abspath(os.fspath(expanded)))


def require_path_within(root: Path, candidate: Path, label: str) -> None:
    resolved_root = root.resolve()
    resolved_candidate = candidate.resolve()
    try:
        resolved_candidate.relative_to(resolved_root)
    except ValueError:
        die(f"{label} must stay within {resolved_root}: {resolved_candidate}")


def require_no_symlink_in_path_chain(path: Path, label: str) -> None:
    current = path
    while True:
        if current.is_symlink() and current not in SYSTEM_SYMLINK_ALLOWLIST:
            die(f"{label} must not be a symlink: {current}")
        if current.parent == current:
            return
        current = current.parent


def validate_source_path(raw: object, label: str, base: Path) -> Path:
    if not isinstance(raw, str) or not raw:
        die(f"{label} must be a non-empty string path")
    source = expand_host_path(raw, base)
    if not source.exists():
        die(f"{label} does not exist: {source}")
    require_no_symlink_in_path_chain(source, label)
    return source


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
    for value in values:
        if not isinstance(value, str):
            die(f"{label} values must be strings")
        if value not in allowed_values:
            die(f"{label} contains unsupported value: {value}")
    return current in values


def strip_comment(line: str) -> str:
    escaped = False
    quote_char = ""
    result = []

    for char in line:
        if escaped:
            result.append(char)
            escaped = False
            continue
        if char == "\\" and quote_char == '"':
            result.append(char)
            escaped = True
            continue
        if char in {'"', "'"}:
            if not quote_char:
                quote_char = char
            elif quote_char == char:
                quote_char = ""
            result.append(char)
            continue
        if char == "#" and not quote_char:
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
        f"{policy_path}:{lineno}: unsupported TOML value; use quoted strings, "
        "booleans, integers, or arrays of strings"
    )


def parse_toml_subset(content: str, policy_path: Path) -> dict:
    root: dict[str, object] = {}
    current: dict[str, object] = root
    seen_tables: set[str] = set()

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
            if table_name in seen_tables:
                die(f"{policy_path}:{lineno}: duplicate table [{table_name}]")
            seen_tables.add(table_name)
            if table_name.startswith("credentials."):
                credential_key = table_name.split(".", 1)[1]
                if credential_key not in CREDENTIAL_KEYS:
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
        if "." in key:
            die(
                f"{policy_path}:{lineno}: dotted TOML keys are not supported; "
                "use explicit [table] headers instead"
            )
        if key in current:
            die(f"{policy_path}:{lineno}: duplicate key: {key}")
        current[key] = parse_value(value, policy_path, lineno)

    return root


def policy_sha256(policy_path: Path) -> str:
    return f"sha256:{hashlib.sha256(policy_path.read_bytes()).hexdigest()}"


def composite_policy_sha256(policy_sources: list[dict[str, str]]) -> str:
    canonical = repr(sorted(policy_sources, key=lambda entry: entry["path"])).encode("utf-8")
    return f"sha256:{hashlib.sha256(canonical).hexdigest()}"


def logical_policy_path(policy_path: Path, entrypoint_root: Path) -> str:
    relative_path = os.path.relpath(policy_path, entrypoint_root)
    return relative_path.replace(os.sep, "/")


def rebase_fragment_path(raw: object, fragment_dir: Path) -> object:
    if not isinstance(raw, str) or not raw:
        return raw
    return str(expand_host_path(raw, fragment_dir))


def rebase_policy_fragment(policy: dict[str, object], fragment_dir: Path) -> dict[str, object]:
    rebased: dict[str, object] = {}
    for key, value in policy.items():
        if key == "documents" and isinstance(value, dict):
            rebased[key] = {
                document_key: rebase_fragment_path(document_value, fragment_dir)
                for document_key, document_value in value.items()
            }
            continue
        if key == "copies" and isinstance(value, list):
            rebased_copies: list[object] = []
            for entry in value:
                if not isinstance(entry, dict):
                    rebased_copies.append(entry)
                    continue
                rebased_entry = dict(entry)
                if "source" in rebased_entry:
                    rebased_entry["source"] = rebase_fragment_path(
                        rebased_entry["source"], fragment_dir
                    )
                rebased_copies.append(rebased_entry)
            rebased[key] = rebased_copies
            continue
        if key == "ssh" and isinstance(value, dict):
            rebased_ssh = dict(value)
            for ssh_key in ("config", "known_hosts"):
                if ssh_key in rebased_ssh:
                    rebased_ssh[ssh_key] = rebase_fragment_path(rebased_ssh[ssh_key], fragment_dir)
            identities = rebased_ssh.get("identities")
            if isinstance(identities, list):
                rebased_ssh["identities"] = [
                    rebase_fragment_path(identity, fragment_dir) for identity in identities
                ]
            rebased[key] = rebased_ssh
            continue
        if key == "credentials" and isinstance(value, dict):
            rebased_credentials: dict[str, object] = {}
            for credential_key, credential_value in value.items():
                if isinstance(credential_value, dict):
                    rebased_credential = dict(credential_value)
                    if "source" in rebased_credential:
                        rebased_credential["source"] = rebase_fragment_path(
                            rebased_credential["source"], fragment_dir
                        )
                    rebased_credentials[credential_key] = rebased_credential
                else:
                    rebased_credentials[credential_key] = rebase_fragment_path(
                        credential_value, fragment_dir
                    )
            rebased[key] = rebased_credentials
            continue
        rebased[key] = value
    return rebased


def validate_policy_include(raw: object, label: str, base: Path, entrypoint_root: Path) -> Path:
    source = validate_source_path(raw, label, base)
    if not source.is_file():
        die(f"{label} must point at a file: {source}")
    resolved_source = source.resolve()
    require_path_within(entrypoint_root, resolved_source, label)
    return resolved_source


def merge_policy_fragment(base: dict[str, object], addition: dict[str, object], source_path: Path) -> None:
    version = addition.get("version", 1)
    if version != 1:
        die(f"unsupported injection policy version: {version}")

    for table_name in ("documents", "ssh", "credentials"):
        table = addition.get(table_name)
        if table is None:
            continue
        if not isinstance(table, dict):
            die(f"injection policy fragment must keep {table_name} as a table: {source_path}")
        destination = base.setdefault(table_name, {})
        if not isinstance(destination, dict):
            die(f"injection policy merge corrupted {table_name}: {source_path}")
        for key, value in table.items():
            if key in destination:
                die(
                    "injection policy fragments declare the same setting more than once: "
                    f"{table_name}.{key} ({source_path})"
                )
            destination[key] = value

    copies = addition.get("copies")
    if copies is None:
        return
    if not isinstance(copies, list):
        die(f"injection policy fragment must keep copies as an array of tables: {source_path}")
    destination_copies = base.setdefault("copies", [])
    if not isinstance(destination_copies, list):
        die(f"injection policy merge corrupted copies: {source_path}")
    destination_copies.extend(copies)


def load_policy_bundle(
    policy_path: Path,
    *,
    entrypoint_root: Path | None = None,
    active_stack: tuple[Path, ...] | None = None,
    loaded_paths: set[Path] | None = None,
) -> tuple[dict, list[dict[str, str]]]:
    resolved_policy_path = policy_path.expanduser().resolve()
    entrypoint_root = (
        entrypoint_root.resolve() if entrypoint_root is not None else resolved_policy_path.parent
    )
    active_stack = active_stack or ()
    loaded_paths = loaded_paths if loaded_paths is not None else set()

    if resolved_policy_path in active_stack:
        cycle = " -> ".join(str(path) for path in (*active_stack, resolved_policy_path))
        die(f"injection policy include cycle detected: {cycle}")
    if resolved_policy_path in loaded_paths:
        die(f"injection policy includes the same file more than once: {resolved_policy_path}")
    loaded_paths.add(resolved_policy_path)

    loaded = parse_toml_subset(
        resolved_policy_path.read_text(encoding="utf-8"),
        resolved_policy_path,
    )
    if not isinstance(loaded, dict):
        die(f"injection policy must decode to a TOML table: {resolved_policy_path}")
    validate_allowed_keys(loaded, ALLOWED_ROOT_POLICY_KEYS, "root policy")

    version = loaded.get("version", 1)
    if version != 1:
        die(f"unsupported injection policy version: {version}")

    includes = loaded.get("includes", [])
    if includes is None:
        includes = []
    if not isinstance(includes, list):
        die("includes must be an array of strings when specified")

    merged: dict[str, object] = {"version": 1}
    policy_sources: list[dict[str, str]] = []
    next_stack = (*active_stack, resolved_policy_path)
    for index, include in enumerate(includes):
        include_path = validate_policy_include(
            include,
            f"includes[{index}]",
            resolved_policy_path.parent,
            entrypoint_root,
        )
        included_policy, included_sources = load_policy_bundle(
            include_path,
            entrypoint_root=entrypoint_root,
            active_stack=next_stack,
            loaded_paths=loaded_paths,
        )
        merge_policy_fragment(merged, included_policy, include_path)
        policy_sources.extend(included_sources)

    current_policy = dict(loaded)
    current_policy.pop("includes", None)
    if active_stack:
        current_policy = rebase_policy_fragment(current_policy, resolved_policy_path.parent)
    merge_policy_fragment(merged, current_policy, resolved_policy_path)
    policy_sources.append(
        {
            "path": logical_policy_path(resolved_policy_path, entrypoint_root),
            "sha256": policy_sha256(resolved_policy_path),
        }
    )
    return merged, policy_sources


def load_raw_policy(policy_path: Path) -> dict:
    if not policy_path.exists():
        return {"version": 1}
    loaded = parse_toml_subset(policy_path.read_text(encoding="utf-8"), policy_path)
    if not isinstance(loaded, dict):
        die(f"injection policy must decode to a TOML table: {policy_path}")
    validate_allowed_keys(loaded, ALLOWED_ROOT_POLICY_KEYS, "root policy")
    version = loaded.get("version", 1)
    if version != 1:
        die(f"unsupported injection policy version: {version}")
    if "version" not in loaded:
        loaded["version"] = 1
    return loaded


def quote_string(value: str) -> str:
    return json_quote(value)


def json_quote(value: str) -> str:
    escaped = value.replace("\\", "\\\\").replace('"', '\\"')
    escaped = escaped.replace("\n", "\\n")
    return f'"{escaped}"'


def render_toml_value(value: object) -> str:
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, int):
        return str(value)
    if isinstance(value, str):
        return json_quote(value)
    if isinstance(value, list):
        if not all(isinstance(item, str) for item in value):
            die("only arrays of strings are supported in rendered policy output")
        return "[" + ", ".join(json_quote(item) for item in value) + "]"
    die(f"unsupported TOML value type: {type(value).__name__}")


def render_policy_toml(policy: dict[str, object]) -> str:
    lines: list[str] = []
    version = policy.get("version", 1)
    lines.append(f"version = {render_toml_value(version)}")

    includes = policy.get("includes")
    if isinstance(includes, list) and includes:
        lines.append(f"includes = {render_toml_value(includes)}")

    documents = policy.get("documents")
    if isinstance(documents, dict) and documents:
        lines.append("")
        lines.append("[documents]")
        for key in ("common", "codex", "claude", "gemini"):
            if key in documents:
                lines.append(f"{key} = {render_toml_value(documents[key])}")

    credentials = policy.get("credentials")
    if isinstance(credentials, dict) and credentials:
        scalar_entries = {
            key: value for key, value in credentials.items() if not isinstance(value, dict)
        }
        if scalar_entries:
            lines.append("")
            lines.append("[credentials]")
            for key in sorted(scalar_entries):
                lines.append(f"{key} = {render_toml_value(scalar_entries[key])}")
        for key in sorted(credentials):
            value = credentials[key]
            if not isinstance(value, dict):
                continue
            lines.append("")
            lines.append(f"[credentials.{key}]")
            for field in sorted(value):
                lines.append(f"{field} = {render_toml_value(value[field])}")

    ssh = policy.get("ssh")
    if isinstance(ssh, dict) and ssh:
        lines.append("")
        lines.append("[ssh]")
        ordered = ("enabled", "config", "known_hosts", "identities", "providers", "modes", "allow_unsafe_config")
        for key in ordered:
            if key in ssh:
                lines.append(f"{key} = {render_toml_value(ssh[key])}")
        for key in sorted(set(ssh) - set(ordered)):
            lines.append(f"{key} = {render_toml_value(ssh[key])}")

    copies = policy.get("copies")
    if isinstance(copies, list) and copies:
        for entry in copies:
            if not isinstance(entry, dict):
                die("copies entries must be TOML tables when rendering policy output")
            lines.append("")
            lines.append("[[copies]]")
            for key in ("source", "target", "classification", "providers", "modes"):
                if key in entry:
                    lines.append(f"{key} = {render_toml_value(entry[key])}")
            for key in sorted(set(entry) - {"source", "target", "classification", "providers", "modes"}):
                lines.append(f"{key} = {render_toml_value(entry[key])}")

    return "\n".join(lines) + "\n"


def write_policy_file(policy_path: Path, policy: dict[str, object]) -> None:
    policy_path.parent.mkdir(parents=True, exist_ok=True)
    policy_path.write_text(render_policy_toml(policy), encoding="utf-8")
    policy_path.chmod(0o600)


def require_secret_file(source: Path, label: str) -> Path:
    if source.is_symlink():
        die(f"{label} must not be a symlink: {source}")
    if not source.is_file():
        die(f"{label} must point at a file: {source}")
    path_stat = source.lstat()
    if path_stat.st_uid != os.getuid():
        die(f"{label} must be owned by uid {os.getuid()}: {source}")
    if stat.S_IMODE(path_stat.st_mode) & 0o077:
        die(f"{label} must not be group/world-accessible: {source}")
    return source
