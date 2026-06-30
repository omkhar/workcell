#!/bin/bash -p
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "verify-upstream-copilot-release-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-copilot-release.XXXXXX")"
BUILD_INPUT_MANIFEST_PATH="${TMP_ROOT}/build-input.json"
RELEASE_METADATA_PATH="${TMP_ROOT}/release.json"
COPILOT_VERSION=""

cleanup() {
  rm -rf "${TMP_ROOT}"
}

trap cleanup EXIT

generate_build_input_manifest() {
  "${ROOT_DIR}/scripts/generate-build-input-manifest.sh" "${BUILD_INPUT_MANIFEST_PATH}"
}

extract_copilot_version() {
  jq -r '.runtime.copilot.version // empty' "${BUILD_INPUT_MANIFEST_PATH}"
}

extract_copilot_sha() {
  local target_arch="$1"
  local value=""

  value="$(jq -r --arg target_arch "${target_arch}" '.runtime.copilot.assets[$target_arch].sha256 // empty' "${BUILD_INPUT_MANIFEST_PATH}")"
  if [[ ! "${value}" =~ ^[0-9a-f]{64}$ ]]; then
    echo "Build input manifest is missing a valid ${target_arch} Copilot checksum" >&2
    exit 1
  fi
  printf '%s\n' "${value}"
}

download_large_asset() {
  local url="$1"
  local output="$2"

  curl -fsSL \
    --retry 5 \
    --retry-all-errors \
    --retry-delay 5 \
    --connect-timeout 20 \
    --speed-limit 1024 \
    --speed-time 60 \
    "${url}" -o "${output}"
}

github_api_get() {
  local url="$1"
  local output="$2"
  local token="${WORKCELL_GITHUB_API_TOKEN:-${GITHUB_TOKEN:-${GH_TOKEN:-}}}"

  if [[ -n "${token}" ]]; then
    curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --connect-timeout 20 \
      -H "Accept: application/vnd.github+json" \
      --oauth2-bearer "${token}" \
      "${url}" \
      -o "${output}"
    return
  fi
  curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --connect-timeout 20 \
    -H "Accept: application/vnd.github+json" \
    "${url}" \
    -o "${output}"
}

release_asset_digest() {
  local asset_name="$1"

  jq -r --arg asset_name "${asset_name}" '
    .assets[]
    | select(.name == $asset_name)
    | .digest
    | sub("^sha256:"; "")
  ' "${RELEASE_METADATA_PATH}"
}

verify_asset() {
  local target_arch="$1"
  local platform="$2"
  local expected_sha="$3"
  local tarball_name="copilot-${platform}.tar.gz"
  local asset_root="https://github.com/github/copilot-cli/releases/download/v${COPILOT_VERSION}"
  local work_dir="${TMP_ROOT}/${target_arch}"
  local metadata_sha=""

  mkdir -p "${work_dir}"
  metadata_sha="$(release_asset_digest "${tarball_name}")"
  if [[ -z "${metadata_sha}" || "${metadata_sha}" == "null" ]]; then
    echo "GitHub Copilot CLI release v${COPILOT_VERSION} is missing ${tarball_name}" >&2
    exit 1
  fi
  if [[ "${metadata_sha}" != "${expected_sha}" ]]; then
    echo "Pinned ${target_arch} Copilot checksum does not match GitHub release metadata" >&2
    exit 1
  fi
  download_large_asset "${asset_root}/${tarball_name}" "${work_dir}/${tarball_name}"
  echo "${expected_sha}  ${work_dir}/${tarball_name}" | sha256sum -c - >/dev/null
  tar -tzf "${work_dir}/${tarball_name}" copilot >/dev/null
}

require_tool curl
require_tool go
require_tool jq
require_tool sha256sum
require_tool tar

generate_build_input_manifest
COPILOT_VERSION="$(extract_copilot_version)"
if [[ -z "${COPILOT_VERSION}" ]]; then
  echo "Build input manifest is missing the Copilot version" >&2
  exit 1
fi
github_api_get "https://api.github.com/repos/github/copilot-cli/releases/tags/v${COPILOT_VERSION}" "${RELEASE_METADATA_PATH}"

if [[ "$(jq -r '.tag_name' "${RELEASE_METADATA_PATH}")" != "v${COPILOT_VERSION}" ]]; then
  echo "GitHub Copilot CLI release metadata does not match v${COPILOT_VERSION}" >&2
  exit 1
fi
if [[ "$(jq -r '.prerelease' "${RELEASE_METADATA_PATH}")" != "false" ]]; then
  echo "GitHub Copilot CLI v${COPILOT_VERSION} must be a non-prerelease release" >&2
  exit 1
fi

verify_asset arm64 linux-arm64 "$(extract_copilot_sha arm64)"
verify_asset amd64 linux-x64 "$(extract_copilot_sha amd64)"

echo "Workcell upstream Copilot release verification passed."
