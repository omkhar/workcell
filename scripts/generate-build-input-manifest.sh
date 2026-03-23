#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="${WORKCELL_BUILD_INPUT_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
OUTPUT_PATH="${1:-}"
DOCKERFILE_PATH="${ROOT_DIR}/runtime/container/Dockerfile"
PACKAGE_JSON_PATH="${ROOT_DIR}/runtime/container/providers/package.json"
PACKAGE_LOCK_PATH="${ROOT_DIR}/runtime/container/providers/package-lock.json"
BUILD_REF="${WORKCELL_BUILD_INPUT_REF:-$(git -C "${ROOT_DIR}" rev-parse HEAD 2>/dev/null || printf 'UNKNOWN')}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"
REQUIRE_TRACKED="${WORKCELL_BUILD_INPUT_REQUIRE_TRACKED:-0}"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

[[ -n "${OUTPUT_PATH}" ]] || {
  echo "usage: $0 OUTPUT_PATH" >&2
  exit 64
}

require_tool python3

python3 - "${DOCKERFILE_PATH}" "${PACKAGE_JSON_PATH}" "${PACKAGE_LOCK_PATH}" "${OUTPUT_PATH}" "${BUILD_REF}" "${SOURCE_DATE_EPOCH}" "${REQUIRE_TRACKED}" <<'PY'
import hashlib
import json
import pathlib
import re
import subprocess
import sys

dockerfile_path = pathlib.Path(sys.argv[1])
package_json_path = pathlib.Path(sys.argv[2])
package_lock_path = pathlib.Path(sys.argv[3])
output_path = pathlib.Path(sys.argv[4])
build_ref = sys.argv[5]
source_date_epoch = int(sys.argv[6])
require_tracked = sys.argv[7] == "1"
root_dir = dockerfile_path.parents[2]

dockerfile = dockerfile_path.read_text(encoding="utf-8")
package_json = json.loads(package_json_path.read_text(encoding="utf-8"))
package_lock = json.loads(package_lock_path.read_text(encoding="utf-8"))

node_base_image_match = re.search(r"^ARG NODE_BASE_IMAGE=(.+)$", dockerfile, re.MULTILINE)
debian_snapshot_match = re.search(r"^ARG DEBIAN_SNAPSHOT=(\d{8}T\d{6}Z)$", dockerfile, re.MULTILINE)
codex_version_match = re.search(r"^ARG CODEX_VERSION=(.+)$", dockerfile, re.MULTILINE)

if not node_base_image_match:
    raise SystemExit("Unable to extract NODE_BASE_IMAGE from Dockerfile")
if not debian_snapshot_match:
    raise SystemExit("Unable to extract DEBIAN_SNAPSHOT from Dockerfile")
if not codex_version_match:
    raise SystemExit("Unable to extract CODEX_VERSION from Dockerfile")

codex_assets = {}
for target_arch, codex_arch in (
    ("arm64", "aarch64-unknown-linux-gnu"),
    ("amd64", "x86_64-unknown-linux-gnu"),
):
    pattern = re.compile(
        rf"{re.escape(target_arch)}\)\s+\\\s*CODEX_ARCH=\"{re.escape(codex_arch)}\";\s+\\\s*CODEX_SHA256=\"([0-9a-f]{{64}})\";",
        re.MULTILINE,
    )
    match = pattern.search(dockerfile)
    if not match:
        raise SystemExit(f"Unable to extract CODEX_SHA256 for {target_arch}")
    codex_assets[target_arch] = {
        "arch": codex_arch,
        "sha256": match.group(1),
        "url": f"https://github.com/openai/codex/releases/download/rust-v{codex_version_match.group(1).strip()}/codex-{codex_arch}.tar.gz",
    }

dependencies = {}
for name in sorted(package_json.get("dependencies", {})):
    package_entry = package_lock.get("packages", {}).get(f"node_modules/{name}")
    if not package_entry:
        raise SystemExit(f"Missing pinned package entry for {name}")
    dependencies[name] = {
        "version": package_entry["version"],
        "resolved": package_entry["resolved"],
        "integrity": package_entry["integrity"],
    }

def walk_files(relative_root, *, exclude_parts=()):
    base = root_dir / relative_root
    for path in sorted(base.rglob("*")):
        if not path.is_file():
            continue
        relative_path = path.relative_to(root_dir)
        if any(part in relative_path.parts for part in exclude_parts):
            continue
        yield str(relative_path)

def walk_doc_files():
    for path in sorted(root_dir.rglob("*")):
        if not path.is_file():
            continue
        relative_path = path.relative_to(root_dir)
        if any(
            part in relative_path.parts
            for part in (".git", "dist", "tmp", "node_modules", "vendor", "target")
        ):
            continue
        if path.suffix not in (".md", ".txt", ".1"):
            continue
        yield str(relative_path)


def walk_repo_files():
    for path in sorted(root_dir.rglob("*")):
        if not path.is_file():
            continue
        relative_path = path.relative_to(root_dir)
        if any(
            part in relative_path.parts
            for part in (".git", "dist", "tmp", "node_modules", "target")
        ):
            continue
        yield str(relative_path)

def tracked_subset(paths):
    unique = sorted(set(paths))
    try:
        inside_proc = subprocess.run(
            ["git", "-C", str(root_dir), "rev-parse", "--is-inside-work-tree"],
            check=True,
            capture_output=True,
            text=True,
        )
    except (OSError, subprocess.CalledProcessError):
        return unique

    if inside_proc.stdout.strip() != "true":
        return unique

    toplevel_proc = subprocess.run(
        ["git", "-C", str(root_dir), "rev-parse", "--show-toplevel"],
        check=True,
        capture_output=True,
        text=True,
    )
    if pathlib.Path(toplevel_proc.stdout.strip()) != root_dir:
        return unique

    tracked = subprocess.run(
        ["git", "-C", str(root_dir), "ls-files", "--", *unique],
        check=True,
        capture_output=True,
        text=True,
    )
    tracked_paths = {line for line in tracked.stdout.splitlines() if line}
    omitted = [path for path in unique if path not in tracked_paths]
    if require_tracked and omitted:
        omitted_display = "\n".join(f"  - {path}" for path in omitted)
        raise SystemExit(
            "Release-critical inputs must be tracked before generating a verified "
            "build input manifest:\n"
            f"{omitted_display}"
        )
    return [path for path in unique if path in tracked_paths]


def tracked_repo_files():
    try:
        inside_proc = subprocess.run(
            ["git", "-C", str(root_dir), "rev-parse", "--is-inside-work-tree"],
            check=True,
            capture_output=True,
            text=True,
        )
    except (OSError, subprocess.CalledProcessError):
        return None

    if inside_proc.stdout.strip() != "true":
        return None

    toplevel_proc = subprocess.run(
        ["git", "-C", str(root_dir), "rev-parse", "--show-toplevel"],
        check=True,
        capture_output=True,
        text=True,
    )
    if pathlib.Path(toplevel_proc.stdout.strip()) != root_dir:
        return None

    tracked = subprocess.run(
        ["git", "-C", str(root_dir), "ls-files", "-z"],
        check=True,
        capture_output=True,
    )
    return sorted(
        path.decode("utf-8")
        for path in tracked.stdout.split(b"\0")
        if path
    )


def digest_map(paths):
    result = {}
    for relative_path in sorted(paths):
        candidate = root_dir / relative_path
        if not candidate.is_file():
            raise SystemExit(
                "Tracked release input is missing from the working tree; stage the "
                f"deletion or restore the file before generating a verified build "
                f"input manifest: {relative_path}"
            )
        result[relative_path] = hashlib.sha256(candidate.read_bytes()).hexdigest()
    return result


runtime_context_paths = tracked_subset(
    [
        ".dockerignore",
        *walk_files("adapters"),
        *walk_files("runtime/container", exclude_parts=("node_modules", "target")),
    ]
)
runtime_context_inputs = digest_map(runtime_context_paths)

tracked_files = tracked_repo_files()
excluded_prefixes = (
    "dist/",
    "tmp/",
    "runtime/container/providers/node_modules/",
    "runtime/container/rust/target/",
)
runtime_context_set = set(runtime_context_paths)
if tracked_files is None:
    verification_paths = [
        path
        for path in walk_repo_files()
        if path not in runtime_context_set
        and not path.startswith(excluded_prefixes)
    ]
else:
    verification_paths = [
        path
        for path in tracked_files
        if path not in runtime_context_set
        and not path.startswith(excluded_prefixes)
    ]

verification_inputs = digest_map(verification_paths)

manifest = {
    "schema_version": 1,
    "build": {
        "ref": build_ref,
        "source_date_epoch": source_date_epoch,
    },
    "runtime": {
        "dockerfile_sha256": hashlib.sha256(dockerfile_path.read_bytes()).hexdigest(),
        "node_base_image": node_base_image_match.group(1).strip(),
        "debian_snapshot": debian_snapshot_match.group(1),
        "codex": {
            "version": codex_version_match.group(1).strip(),
            "assets": codex_assets,
        },
        "providers": {
            "package_json_sha256": hashlib.sha256(package_json_path.read_bytes()).hexdigest(),
            "package_lock_sha256": hashlib.sha256(package_lock_path.read_bytes()).hexdigest(),
            "dependencies": dependencies,
        },
        "context_inputs": runtime_context_inputs,
    },
    "verification": {
        "inputs": verification_inputs,
    },
}

output_path.parent.mkdir(parents=True, exist_ok=True)
output_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
