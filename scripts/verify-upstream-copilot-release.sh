#!/bin/bash -p
workcell_copilot_release_token_file_created=""
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  unset WORKCELL_COPILOT_RELEASE_TOKEN_FILE_CREATED
  if [[ -z "${WORKCELL_GITHUB_API_TOKEN_FILE:-}" ]]; then
    workcell_copilot_release_token="${WORKCELL_GITHUB_API_TOKEN:-${GITHUB_TOKEN:-${GH_TOKEN:-}}}"
    unset WORKCELL_GITHUB_API_TOKEN GITHUB_TOKEN GH_TOKEN
    if [[ -n "${workcell_copilot_release_token}" ]]; then
      workcell_copilot_release_token_file="$(umask 077 && /usr/bin/mktemp "${TMPDIR:-/tmp}/workcell-github-token.XXXXXX")"
      printf '%s' "${workcell_copilot_release_token}" >"${workcell_copilot_release_token_file}"
      export WORKCELL_GITHUB_API_TOKEN_FILE="${workcell_copilot_release_token_file}"
      workcell_copilot_release_token_file_created="${workcell_copilot_release_token_file}"
      export WORKCELL_COPILOT_RELEASE_TOKEN_FILE_CREATED="${workcell_copilot_release_token_file_created}"
    fi
  fi
  unset WORKCELL_GITHUB_API_TOKEN GITHUB_TOKEN GH_TOKEN workcell_copilot_release_token
fi
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/trusted-entrypoint.sh"
workcell_copilot_release_token_file_created="${WORKCELL_COPILOT_RELEASE_TOKEN_FILE_CREATED:-${workcell_copilot_release_token_file_created}}"
unset WORKCELL_COPILOT_RELEASE_TOKEN_FILE_CREATED

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "verify-upstream-copilot-release-entrypoint-ok"
  [[ -z "${workcell_copilot_release_token_file_created}" || "${workcell_copilot_release_token_file_created}" != "${TMPDIR:-/tmp}"/workcell-github-token.* ]] || rm -f "${workcell_copilot_release_token_file_created}"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-copilot-release.XXXXXX")"
BUILD_INPUT_MANIFEST_PATH="${TMP_ROOT}/build-input.json"
RELEASE_METADATA_PATH="${TMP_ROOT}/release.json"

cleanup() {
  rm -rf "${TMP_ROOT}"
  [[ -z "${workcell_copilot_release_token_file_created}" || "${workcell_copilot_release_token_file_created}" != "${TMPDIR:-/tmp}"/workcell-github-token.* ]] || rm -f "${workcell_copilot_release_token_file_created}"
}

trap cleanup EXIT

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

github_api_get() {
  local url="$1"
  local output="$2"
  local token="${WORKCELL_GITHUB_API_TOKEN:-}"

  if [[ -z "${token}" && -n "${WORKCELL_GITHUB_API_TOKEN_FILE:-}" ]]; then
    if [[ ! -r "${WORKCELL_GITHUB_API_TOKEN_FILE}" ]]; then
      echo "GitHub API token file is not readable: ${WORKCELL_GITHUB_API_TOKEN_FILE}" >&2
      exit 1
    fi
    token="$(<"${WORKCELL_GITHUB_API_TOKEN_FILE}")"
  fi
  if [[ -z "${token}" ]]; then
    token="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
  fi

  if [[ -n "${token}" ]]; then
    if [[ "${token}" == *$'\n'* || "${token}" == *$'\r'* ]]; then
      echo "GitHub API token must be a single line" >&2
      exit 1
    fi
    curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --connect-timeout 20 --config - \
      "${url}" \
      -o "${output}" <<EOF
header = "Accept: application/vnd.github+json"
header = "Authorization: Bearer ${token}"
EOF
    return
  fi
  curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --connect-timeout 20 \
    -H "Accept: application/vnd.github+json" \
    "${url}" \
    -o "${output}"
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
  metadata_sha="$(jq -r --arg asset_name "${tarball_name}" '.assets[] | select(.name == $asset_name) | .digest | sub("^sha256:"; "")' "${RELEASE_METADATA_PATH}")"
  if [[ -z "${metadata_sha}" || "${metadata_sha}" == "null" ]]; then
    echo "GitHub Copilot CLI release v${COPILOT_VERSION} is missing ${tarball_name}" >&2
    exit 1
  fi
  if [[ "${metadata_sha}" != "${expected_sha}" ]]; then
    echo "Pinned ${target_arch} Copilot checksum does not match GitHub release metadata" >&2
    exit 1
  fi
  curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --connect-timeout 20 --speed-limit 1024 --speed-time 60 "${asset_root}/${tarball_name}" -o "${work_dir}/${tarball_name}"
  echo "${expected_sha}  ${work_dir}/${tarball_name}" | sha256sum -c - >/dev/null
  tar -tzf "${work_dir}/${tarball_name}" copilot >/dev/null
}

require_tool curl
require_tool go
require_tool jq
require_tool sha256sum
require_tool tar

"${ROOT_DIR}/scripts/generate-build-input-manifest.sh" "${BUILD_INPUT_MANIFEST_PATH}"
COPILOT_VERSION="$(jq -r '.runtime.copilot.version // empty' "${BUILD_INPUT_MANIFEST_PATH}")"
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
