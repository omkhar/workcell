#!/usr/bin/env python3
"""Manage Workcell-owned saved provider credentials."""

from __future__ import annotations

import argparse
import importlib.util
import json
import os
from pathlib import Path
import shutil
import tempfile
import tomllib
from typing import NoReturn


def load_render_helpers():
    helper_path = Path(__file__).resolve().with_name("render_injection_bundle.py")
    spec = importlib.util.spec_from_file_location("workcell_render_helpers", helper_path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"unable to load render helpers from {helper_path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


RENDER_HELPERS = load_render_helpers()

GOOGLE_OAUTH_EXTRA_ENDPOINTS = sorted(
    set(RENDER_HELPERS.GOOGLE_AUTH_ENDPOINTS) | {RENDER_HELPERS.VERTEX_ENDPOINT}
)

PERSISTABLE_KEYS: dict[str, dict[str, str]] = {
    "codex_auth": {
        "filename": "codex-auth.json",
        "label": "credentials.codex_auth",
    },
    "claude_api_key": {
        "filename": "claude-api-key.txt",
        "label": "credentials.claude_api_key",
    },
    "claude_auth": {
        "filename": "claude-auth.json",
        "label": "credentials.claude_auth",
    },
    "gemini_env": {
        "filename": "gemini.env",
        "label": "credentials.gemini_env",
    },
    "gemini_oauth": {
        "filename": "gemini-oauth.json",
        "label": "credentials.gemini_oauth",
    },
    "gcloud_adc": {
        "filename": "gcloud-adc.json",
        "label": "credentials.gcloud_adc",
    },
}

PROBEABLE_KEYS: dict[str, dict[str, str]] = dict(PERSISTABLE_KEYS)


def die(message: str) -> NoReturn:
    raise SystemExit(message)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)

    describe = subparsers.add_parser("describe")
    describe.add_argument("--key", required=True, choices=sorted(PROBEABLE_KEYS))
    describe.add_argument("--source", required=True)

    equivalent = subparsers.add_parser("equivalent")
    equivalent.add_argument("--key", required=True, choices=sorted(PROBEABLE_KEYS))
    equivalent.add_argument("--left", required=True)
    equivalent.add_argument("--right", required=True)

    persist = subparsers.add_parser("persist")
    persist.add_argument(
        "--entry",
        action="append",
        default=[],
        metavar="KEY=PATH",
        help="credential entry to persist; repeat for multi-file auth bundles",
    )
    persist.add_argument("--root-policy", required=True)
    persist.add_argument("--fragment", required=True)
    persist.add_argument("--credentials-root", required=True)

    return parser.parse_args()


def require_regular_file(path: Path, label: str) -> None:
    if path.is_symlink():
        die(f"{label} must not be a symlink: {path}")
    if not path.is_file():
        die(f"{label} must point at a file: {path}")


def validate_saved_credential(key: str, source: Path) -> dict[str, object]:
    label = PROBEABLE_KEYS[key]["label"]
    require_regular_file(source, label)
    RENDER_HELPERS.require_secret_owner_only(source, label)
    metadata: dict[str, object] = {
        "key": key,
        "filename": PROBEABLE_KEYS[key]["filename"],
        "extra_endpoints": [],
        "requires_gcloud_adc": False,
    }

    if key == "codex_auth":
        RENDER_HELPERS.validate_json_object_file(source, "credentials.codex_auth")
    elif key == "claude_auth":
        RENDER_HELPERS.validate_json_object_file(source, "credentials.claude_auth")
    elif key == "claude_api_key":
        if not source.read_text(encoding="utf-8").strip():
            die(f"credentials.claude_api_key must not be empty: {source}")
    elif key == "gemini_env":
        env_metadata = RENDER_HELPERS.validate_gemini_env_file(source)
        metadata["extra_endpoints"] = env_metadata["extra_endpoints"]
        metadata["selected_auth_type"] = env_metadata["selected_auth_type"]
        values = RENDER_HELPERS.parse_simple_env_file(source)
        google_api_key = values.get("GOOGLE_API_KEY", "").strip()
        metadata["requires_gcloud_adc"] = (
            env_metadata["selected_auth_type"] == "oauth-personal"
            or (
                env_metadata["selected_auth_type"] == "vertex-ai"
                and not google_api_key
            )
        )
    elif key == "gemini_oauth":
        RENDER_HELPERS.validate_json_object_file(source, "credentials.gemini_oauth")
        metadata["extra_endpoints"] = GOOGLE_OAUTH_EXTRA_ENDPOINTS
    elif key == "gcloud_adc":
        RENDER_HELPERS.validate_gcloud_adc_file(source, "credentials.gcloud_adc")
        metadata["extra_endpoints"] = GOOGLE_OAUTH_EXTRA_ENDPOINTS
    else:
        die(f"unsupported credential key: {key}")
    return metadata


def normalized_credential_value(key: str, source: Path) -> object:
    validate_saved_credential(key, source)

    if key in {"codex_auth", "claude_auth", "gemini_oauth", "gcloud_adc"}:
        return RENDER_HELPERS.validate_json_object_file(source, PROBEABLE_KEYS[key]["label"])
    if key == "claude_api_key":
        return source.read_text(encoding="utf-8").strip()
    if key == "gemini_env":
        return RENDER_HELPERS.parse_simple_env_file(source)
    die(f"unsupported credential key: {key}")


def equivalent_saved_credentials(key: str, left: Path, right: Path) -> bool:
    return normalized_credential_value(key, left) == normalized_credential_value(key, right)


def json_string(value: str) -> str:
    return json.dumps(value, ensure_ascii=True)


def json_string_array(values: list[str]) -> str:
    return "[" + ", ".join(json_string(value) for value in values) + "]"


def expand_absolute_path(raw: str) -> Path:
    expanded = Path(raw).expanduser()
    return Path(os.path.abspath(os.fspath(expanded)))


def ensure_parent_directory(path: Path) -> None:
    RENDER_HELPERS.require_no_symlink_in_path_chain(path.parent, str(path.parent))
    path.parent.mkdir(parents=True, exist_ok=True)
    path.parent.chmod(0o700)


def atomic_write_text(path: Path, content: str, mode: int) -> None:
    ensure_parent_directory(path)
    with tempfile.NamedTemporaryFile(
        "w",
        encoding="utf-8",
        dir=path.parent,
        prefix=f".{path.name}.tmp.",
        delete=False,
    ) as handle:
        handle.write(content)
        temp_path = Path(handle.name)
    os.chmod(temp_path, mode)
    os.replace(temp_path, path)
    os.chmod(path, mode)


def parse_toml_document(content: str, path: Path) -> dict[str, object]:
    try:
        parsed = tomllib.loads(content)
    except tomllib.TOMLDecodeError as exc:
        message = getattr(exc, "msg", str(exc))
        line_number = getattr(exc, "lineno", None)
        if isinstance(line_number, int):
            die(f"{path}:{line_number}: {message}")
        die(f"{path}: {message}")
    if not isinstance(parsed, dict):
        die(f"TOML document must be a table: {path}")
    return parsed


def strip_inline_comment(text: str) -> str:
    result: list[str] = []
    in_single = False
    in_double = False
    escape = False

    for char in text:
        if in_double:
            result.append(char)
            if escape:
                escape = False
            elif char == "\\":
                escape = True
            elif char == '"':
                in_double = False
            continue
        if in_single:
            result.append(char)
            if char == "'":
                in_single = False
            continue
        if char == '"':
            in_double = True
            result.append(char)
            continue
        if char == "'":
            in_single = True
            result.append(char)
            continue
        if char == "#":
            break
        result.append(char)
    return "".join(result)


def bracket_balance(text: str) -> int:
    balance = 0
    in_single = False
    in_double = False
    escape = False

    for char in strip_inline_comment(text):
        if in_double:
            if escape:
                escape = False
            elif char == "\\":
                escape = True
            elif char == '"':
                in_double = False
            continue
        if in_single:
            if char == "'":
                in_single = False
            continue
        if char == '"':
            in_double = True
            continue
        if char == "'":
            in_single = True
            continue
        if char == "[":
            balance += 1
        elif char == "]":
            balance -= 1
    return balance


def preamble_end_index(lines: list[str]) -> int:
    for index, line in enumerate(lines):
        stripped = strip_inline_comment(line).strip()
        if stripped.startswith("["):
            return index
    return len(lines)


def assignment_span(lines: list[str], key: str) -> tuple[int, int] | None:
    limit = preamble_end_index(lines)

    for index in range(limit):
        stripped = strip_inline_comment(lines[index]).lstrip()
        if "=" not in stripped:
            continue
        left, _sep, _right = stripped.partition("=")
        if left.strip() != key:
            continue
        balance = bracket_balance(lines[index])
        end = index + 1
        while balance > 0 and end < limit:
            balance += bracket_balance(lines[end])
            end += 1
        if balance > 0:
            die(f"{key} assignment is not closed before the first table header")
        return (index, end)
    return None


def update_root_policy(root_policy: Path, include_path: Path) -> None:
    include_relative = os.path.relpath(include_path, root_policy.parent)
    include_relative = include_relative.replace(os.sep, "/")
    include_line = f"includes = {json_string_array([include_relative])}"

    if not root_policy.exists():
        atomic_write_text(
            root_policy,
            f"version = 1\n{include_line}\n",
            0o600,
        )
        return

    RENDER_HELPERS.require_no_symlink(root_policy, str(root_policy))
    current_text = root_policy.read_text(encoding="utf-8")
    parsed = parse_toml_document(current_text, root_policy)
    RENDER_HELPERS.validate_allowed_keys(
        parsed,
        RENDER_HELPERS.ALLOWED_ROOT_POLICY_KEYS,
        "root policy",
    )
    version = parsed.get("version", 1)
    if version != 1:
        die(f"unsupported injection policy version: {version}")

    includes = parsed.get("includes", [])
    if includes is None:
        includes = []
    if not isinstance(includes, list) or not all(isinstance(item, str) for item in includes):
        die(f"includes must be an array of strings in {root_policy}")
    if include_relative in includes:
        return
    includes = [*includes, include_relative]
    updated_line = f"includes = {json_string_array(includes)}"
    lines = current_text.splitlines(keepends=True)
    if not lines:
        lines = ["version = 1\n"]
    include_span = assignment_span(lines, "includes")

    if include_span is not None:
        start, end = include_span
        rendered_lines = [*lines[:start], f"{updated_line}\n", *lines[end:]]
    else:
        version_span = assignment_span(lines, "version")
        insert_at = preamble_end_index(lines)
        if version_span is not None:
            insert_at = version_span[1]
        rendered_lines = [*lines[:insert_at], f"{updated_line}\n", *lines[insert_at:]]

    rendered = "".join(rendered_lines)
    if not rendered.endswith("\n"):
        rendered += "\n"
    atomic_write_text(root_policy, rendered, 0o600)


def load_managed_fragment(fragment_path: Path) -> dict[str, object]:
    if not fragment_path.exists():
        return {"version": 1, "credentials": {}}

    RENDER_HELPERS.require_no_symlink(fragment_path, str(fragment_path))
    parsed = parse_toml_document(
        fragment_path.read_text(encoding="utf-8"),
        fragment_path,
    )
    RENDER_HELPERS.validate_allowed_keys(
        parsed,
        {"version", "credentials"},
        "saved credentials fragment",
    )
    version = parsed.get("version", 1)
    if version != 1:
        die(f"unsupported saved credentials fragment version: {version}")
    credentials = parsed.get("credentials", {})
    if credentials is None:
        credentials = {}
    if not isinstance(credentials, dict):
        die(f"saved credentials fragment credentials must be a table: {fragment_path}")
    for key, value in credentials.items():
        if key not in PERSISTABLE_KEYS:
            die(f"saved credentials fragment contains unsupported key: {key}")
        if not isinstance(value, str):
            die(f"saved credentials fragment {key} must be a string path")
    return {"version": 1, "credentials": dict(credentials)}


def render_managed_fragment(fragment: dict[str, object]) -> str:
    credentials = fragment.get("credentials", {})
    if not isinstance(credentials, dict):
        die("managed fragment credentials must be a table")

    lines = ["version = 1", "", "[credentials]"]
    for key in sorted(credentials):
        value = credentials[key]
        if not isinstance(value, str):
            die(f"managed credential {key} must be a string path")
        lines.append(f"{key} = {json_string(value)}")
    return "\n".join(lines) + "\n"


def copy_saved_credential(source: Path, destination: Path) -> None:
    ensure_parent_directory(destination)
    with source.open("rb") as input_handle:
        with tempfile.NamedTemporaryFile(
            "wb",
            dir=destination.parent,
            prefix=f".{destination.name}.tmp.",
            delete=False,
        ) as handle:
            shutil.copyfileobj(input_handle, handle)
            temp_path = Path(handle.name)
    os.chmod(temp_path, 0o600)
    os.replace(temp_path, destination)
    os.chmod(destination, 0o600)


def backup_existing_file(path: Path, mode: int) -> Path | None:
    if not path.exists():
        return None
    require_regular_file(path, str(path))
    with path.open("rb") as input_handle:
        with tempfile.NamedTemporaryFile(
            "wb",
            dir=path.parent,
            prefix=f".{path.name}.bak.",
            delete=False,
        ) as handle:
            shutil.copyfileobj(input_handle, handle)
            backup_path = Path(handle.name)
    os.chmod(backup_path, mode)
    return backup_path


def cleanup_backup(path: Path | None) -> None:
    if path is None:
        return
    path.unlink(missing_ok=True)


def restore_or_remove(path: Path, backup_path: Path | None, mode: int) -> None:
    if backup_path is None:
        path.unlink(missing_ok=True)
        return
    os.replace(backup_path, path)
    os.chmod(path, mode)


def parse_persist_entry(raw: str) -> tuple[str, Path]:
    if "=" not in raw:
        die(f"persist entry must use KEY=PATH format: {raw}")
    key, raw_source = raw.split("=", 1)
    if key not in PERSISTABLE_KEYS:
        die(f"unsupported persist entry key: {key}")
    if not raw_source:
        die(f"persist entry must include a source path: {raw}")
    return (key, expand_absolute_path(raw_source))


def persist_saved_credentials(
    entries: list[tuple[str, Path]],
    root_policy: Path,
    fragment_path: Path,
    credentials_root: Path,
) -> dict[str, object]:
    if not entries:
        die("persist requires at least one --entry")

    RENDER_HELPERS.require_no_symlink_in_path_chain(root_policy.parent, str(root_policy.parent))
    if root_policy.exists():
        RENDER_HELPERS.require_no_symlink(root_policy, str(root_policy))
    RENDER_HELPERS.require_no_symlink_in_path_chain(fragment_path.parent, str(fragment_path.parent))
    if fragment_path.exists():
        RENDER_HELPERS.require_no_symlink(fragment_path, str(fragment_path))

    validated_entries: list[tuple[str, Path, str]] = []
    for key, source in entries:
        metadata = validate_saved_credential(key, source)
        filename = metadata["filename"]
        if not isinstance(filename, str):
            die(f"saved credential filename must be a string for {key}")
        validated_entries.append((key, source, filename))

    RENDER_HELPERS.require_no_symlink_in_path_chain(credentials_root, str(credentials_root))
    credentials_root.mkdir(parents=True, exist_ok=True)
    credentials_root.chmod(0o700)

    fragment = load_managed_fragment(fragment_path)
    credentials = fragment["credentials"]
    if not isinstance(credentials, dict):
        die("saved credentials fragment credentials must be a table")

    fragment_backup = backup_existing_file(fragment_path, 0o600)

    saved_paths: dict[str, str] = {}
    credential_backups: list[tuple[Path, Path | None]] = []
    try:
        for key, source, filename in validated_entries:
            saved_credential_path = credentials_root / filename
            credential_backups.append(
                (saved_credential_path, backup_existing_file(saved_credential_path, 0o600))
            )
            copy_saved_credential(source, saved_credential_path)
            credentials[key] = str(saved_credential_path)
            saved_paths[key] = str(saved_credential_path)

        atomic_write_text(fragment_path, render_managed_fragment(fragment), 0o600)
        update_root_policy(root_policy, fragment_path)
    except BaseException:
        restore_or_remove(fragment_path, fragment_backup, 0o600)
        for path, backup_path in credential_backups:
            restore_or_remove(path, backup_path, 0o600)
            cleanup_backup(backup_path)
        raise

    cleanup_backup(fragment_backup)
    for _path, backup_path in credential_backups:
        cleanup_backup(backup_path)

    return {
        "root_policy": str(root_policy),
        "fragment": str(fragment_path),
        "credential_paths": saved_paths,
    }


def persist_saved_credential(
    key: str,
    source: Path,
    root_policy: Path,
    fragment_path: Path,
    credentials_root: Path,
) -> dict[str, str]:
    result = persist_saved_credentials(
        [(key, source)],
        root_policy,
        fragment_path,
        credentials_root,
    )
    credential_paths = result["credential_paths"]
    if not isinstance(credential_paths, dict):
        die("persist result must contain credential_paths")
    credential_path = credential_paths.get(key)
    if not isinstance(credential_path, str):
        die(f"persist result must contain saved path for {key}")
    return {
        "root_policy": str(result["root_policy"]),
        "fragment": str(result["fragment"]),
        "credential_path": credential_path,
    }


def main() -> int:
    args = parse_args()

    if args.command == "describe":
        source = expand_absolute_path(args.source)
        print(json.dumps(validate_saved_credential(args.key, source), sort_keys=True))
        return 0

    if args.command == "equivalent":
        left = expand_absolute_path(args.left)
        right = expand_absolute_path(args.right)
        print("1" if equivalent_saved_credentials(args.key, left, right) else "0")
        return 0

    if args.command == "persist":
        result = persist_saved_credentials(
            [parse_persist_entry(raw) for raw in args.entry],
            expand_absolute_path(args.root_policy),
            expand_absolute_path(args.fragment),
            expand_absolute_path(args.credentials_root),
        )
        print(json.dumps(result, sort_keys=True))
        return 0

    die(f"unsupported command: {args.command}")


if __name__ == "__main__":
    raise SystemExit(main())
