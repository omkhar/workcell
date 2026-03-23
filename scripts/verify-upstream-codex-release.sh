#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCKERFILE_PATH="${ROOT_DIR}/runtime/container/Dockerfile"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-codex-release.XXXXXX")"
WORKFLOW_IDENTITY=""
CODEX_VERSION=""

cleanup() {
  rm -rf "${TMP_ROOT}"
}

trap cleanup EXIT

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

extract_codex_version() {
  python3 - "${DOCKERFILE_PATH}" <<'PY'
import pathlib
import re
import sys

text = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
match = re.search(r"^ARG CODEX_VERSION=(.+)$", text, re.MULTILINE)
if not match:
    raise SystemExit("Unable to extract CODEX_VERSION from Dockerfile")
print(match.group(1).strip())
PY
}

extract_codex_sha() {
  local target_arch="$1"
  python3 - "${DOCKERFILE_PATH}" "${target_arch}" <<'PY'
import pathlib
import re
import sys

text = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
target_arch = sys.argv[2]
pattern = re.compile(
    rf'{re.escape(target_arch)}\)\s+\\\s*CODEX_ARCH="[^"]+";\s+\\\s*CODEX_SHA256="([0-9a-f]{{64}})";',
    re.MULTILINE,
)
match = pattern.search(text)
if not match:
    raise SystemExit(f"Unable to extract CODEX_SHA256 for {target_arch}")
print(match.group(1))
PY
}

verify_asset() {
  local target_arch="$1"
  local codex_arch="$2"
  local codex_sha="$3"
  local bundle_name="codex-${codex_arch}.sigstore"
  local tarball_name="codex-${codex_arch}.tar.gz"
  local asset_root="https://github.com/openai/codex/releases/download/rust-v${CODEX_VERSION}"
  local work_dir="${TMP_ROOT}/${target_arch}"

  mkdir -p "${work_dir}"
  curl -fsSL "${asset_root}/${tarball_name}" -o "${work_dir}/${tarball_name}"
  curl -fsSL "${asset_root}/${bundle_name}" -o "${work_dir}/${bundle_name}"

  echo "${codex_sha}  ${work_dir}/${tarball_name}" | sha256sum -c - >/dev/null
  tar -xzf "${work_dir}/${tarball_name}" -C "${work_dir}"

  cosign verify-blob "${work_dir}/codex-${codex_arch}" \
    --bundle "${work_dir}/${bundle_name}" \
    --certificate-identity "${WORKFLOW_IDENTITY}" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com >/dev/null
}

require_tool cosign
require_tool curl
require_tool python3
require_tool sha256sum
require_tool tar

CODEX_VERSION="$(extract_codex_version)"
WORKFLOW_IDENTITY="https://github.com/openai/codex/.github/workflows/rust-release.yml@refs/tags/rust-v${CODEX_VERSION}"

verify_asset arm64 aarch64-unknown-linux-gnu "$(extract_codex_sha arm64)"
verify_asset amd64 x86_64-unknown-linux-gnu "$(extract_codex_sha amd64)"

echo "Workcell upstream Codex release verification passed."
