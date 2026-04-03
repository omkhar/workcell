#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${HOME:-/tmp}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_CONTROL_PLANE_ROOT="${WORKCELL_CONTROL_PLANE_ROOT-}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "generate-control-plane-manifest-entrypoint-ok"
  exit 0
fi

ROOT_DIR="${WORKCELL_CONTROL_PLANE_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
OUTPUT_PATH="${1:-}"

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

python3 - "${ROOT_DIR}" "${OUTPUT_PATH}" <<'PY'
import hashlib
import json
import pathlib
import sys

root_dir = pathlib.Path(sys.argv[1]).resolve()
output_path = pathlib.Path(sys.argv[2])

host_artifacts = [
    "scripts/workcell",
    "scripts/lib/extract_direct_mounts",
    "scripts/lib/render_injection_bundle",
    "scripts/lib/trusted-docker-client.sh",
]

runtime_artifacts = [
    ("adapter-baseline", "adapters/claude/.claude/settings.json", "/opt/workcell/adapters/claude/.claude/settings.json"),
    ("adapter-baseline", "adapters/claude/CLAUDE.md", "/opt/workcell/adapters/claude/CLAUDE.md"),
    ("adapter-baseline", "adapters/claude/hooks/guard-bash.sh", "/opt/workcell/adapters/claude/hooks/guard-bash.sh"),
    ("adapter-baseline", "adapters/claude/managed-settings.json", "/opt/workcell/adapters/claude/managed-settings.json"),
    ("adapter-baseline", "adapters/claude/mcp-template.json", "/opt/workcell/adapters/claude/mcp-template.json"),
    ("adapter-baseline", "adapters/codex/.codex/AGENTS.md", "/opt/workcell/adapters/codex/.codex/AGENTS.md"),
    ("adapter-baseline", "adapters/codex/.codex/agents/anthropic_claude_compat.md", "/opt/workcell/adapters/codex/.codex/agents/anthropic_claude_compat.md"),
    ("adapter-baseline", "adapters/codex/.codex/agents/apple_platform_boundary.md", "/opt/workcell/adapters/codex/.codex/agents/apple_platform_boundary.md"),
    ("adapter-baseline", "adapters/codex/.codex/agents/distinguished_security.md", "/opt/workcell/adapters/codex/.codex/agents/distinguished_security.md"),
    ("adapter-baseline", "adapters/codex/.codex/agents/openai_codex_platform.md", "/opt/workcell/adapters/codex/.codex/agents/openai_codex_platform.md"),
    ("adapter-baseline", "adapters/codex/.codex/config.toml", "/opt/workcell/adapters/codex/.codex/config.toml"),
    ("adapter-baseline", "adapters/codex/.codex/rules/default.rules", "/opt/workcell/adapters/codex/.codex/rules/default.rules"),
    ("adapter-baseline", "adapters/codex/managed_config.toml", "/opt/workcell/adapters/codex/managed_config.toml"),
    ("adapter-baseline", "adapters/codex/mcp/config.toml", "/opt/workcell/adapters/codex/mcp/config.toml"),
    ("adapter-baseline", "adapters/codex/requirements.toml", "/opt/workcell/adapters/codex/requirements.toml"),
    ("adapter-baseline", "adapters/gemini/.gemini/settings.json", "/opt/workcell/adapters/gemini/.gemini/settings.json"),
    ("adapter-baseline", "adapters/gemini/GEMINI.md", "/opt/workcell/adapters/gemini/GEMINI.md"),
    ("runtime-control-plane", "runtime/container/assurance.sh", "/usr/local/libexec/workcell/assurance.sh"),
    ("runtime-control-plane", "runtime/container/bin/apt-helper.sh", "/usr/local/libexec/workcell/apt-helper.sh"),
    ("runtime-control-plane", "runtime/container/bin/apt-wrapper.sh", "/usr/local/libexec/workcell/apt-wrapper.sh"),
    ("runtime-control-plane", "runtime/container/entrypoint.sh", "/usr/local/libexec/workcell/entrypoint.sh"),
    ("runtime-control-plane", "runtime/container/bin/git", "/usr/local/libexec/workcell/git-wrapper.sh"),
    ("runtime-control-plane", "runtime/container/home-control-plane.sh", "/usr/local/libexec/workcell/home-control-plane.sh"),
    ("runtime-control-plane", "runtime/container/bin/node", "/usr/local/libexec/workcell/node-wrapper.sh"),
    ("runtime-control-plane", "runtime/container/provider-policy.sh", "/usr/local/libexec/workcell/provider-policy.sh"),
    ("runtime-control-plane", "runtime/container/provider-wrapper.sh", "/usr/local/libexec/workcell/provider-wrapper.sh"),
    ("runtime-control-plane", "runtime/container/public-node-guard.mjs", "/usr/local/libexec/workcell/public-node-guard.mjs"),
    ("runtime-control-plane", "runtime/container/runtime-user.sh", "/usr/local/libexec/workcell/runtime-user.sh"),
    ("runtime-control-plane", "adapters/claude/managed-settings.json", "/etc/claude-code/managed-settings.json"),
]


def require_tracked_regular_file(path: pathlib.Path, repo_path: str) -> None:
    relative_parts = path.relative_to(root_dir).parts
    current = root_dir
    for part in relative_parts:
        current = current / part
        if current.is_symlink():
            raise SystemExit(f"Control-plane artifact must not be a symlink: {repo_path}")
    if not path.exists():
        raise SystemExit(f"Missing control-plane artifact: {repo_path}")
    if not path.is_file():
        raise SystemExit(f"Control-plane artifact must be a regular file: {repo_path}")


def digest(path: pathlib.Path, repo_path: str) -> str:
    require_tracked_regular_file(path, repo_path)
    return hashlib.sha256(path.read_bytes()).hexdigest()


rendered_host = []
for repo_path in host_artifacts:
    source_path = root_dir / repo_path
    rendered_host.append(
        {
            "repo_path": repo_path,
            "sha256": digest(source_path, repo_path),
        }
    )

rendered_runtime = []
for kind, repo_path, runtime_path in runtime_artifacts:
    source_path = root_dir / repo_path
    rendered_runtime.append(
        {
            "kind": kind,
            "repo_path": repo_path,
            "runtime_path": runtime_path,
            "sha256": digest(source_path, repo_path),
        }
    )

manifest = {
    "schema_version": 2,
    "host_artifacts": rendered_host,
    "runtime_artifacts": rendered_runtime,
}

output_path.parent.mkdir(parents=True, exist_ok=True)
output_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
