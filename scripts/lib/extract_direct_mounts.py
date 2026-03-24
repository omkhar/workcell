#!/usr/bin/env python3
"""Split direct-mount metadata out of an injection manifest and sanitize it."""

from __future__ import annotations

import argparse
import json
from pathlib import Path


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", required=True)
    parser.add_argument("--mount-spec", required=True)
    return parser.parse_args()


def require_direct_mount(entry: dict, label: str) -> dict[str, str]:
    source = entry.pop("source", None)
    mount_path = entry.get("mount_path")
    if not isinstance(source, str) or not source:
        raise SystemExit(f"{label} is missing its host source path")
    if not isinstance(mount_path, str) or not mount_path:
        raise SystemExit(f"{label} is missing its direct mount path")
    return {"source": source, "mount_path": mount_path}


def main() -> int:
    args = parse_args()
    manifest_path = Path(args.manifest).expanduser().resolve()
    mount_spec_path = Path(args.mount_spec).expanduser().resolve()

    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    direct_mounts: list[dict[str, str]] = []

    for key, entry in manifest.get("credentials", {}).items():
        direct_mounts.append(require_direct_mount(entry, f"credentials.{key}"))

    for index, entry in enumerate(manifest.get("copies", [])):
        source = entry.get("source")
        if isinstance(source, dict):
            direct_mounts.append(
                require_direct_mount(source, f"copies[{index}].source")
            )

    ssh = manifest.get("ssh", {})
    for key in ("config", "known_hosts"):
        entry = ssh.get(key)
        if isinstance(entry, dict):
            direct_mounts.append(require_direct_mount(entry, f"ssh.{key}"))

    for index, entry in enumerate(ssh.get("identities", [])):
        if "mount_path" in entry:
            direct_mounts.append(
                require_direct_mount(entry, f"ssh.identities[{index}]")
            )

    manifest_path.write_text(
        json.dumps(manifest, sort_keys=True, indent=2) + "\n",
        encoding="utf-8",
    )
    manifest_path.chmod(0o600)

    mount_spec_path.write_text(
        json.dumps(sorted(direct_mounts, key=lambda item: item["mount_path"]), indent=2)
        + "\n",
        encoding="utf-8",
    )
    mount_spec_path.chmod(0o600)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
