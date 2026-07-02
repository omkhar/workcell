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
COPILOT_HELP_MODE="${WORKCELL_COPILOT_RELEASE_HELP_MODE:-checksum}"
COPILOT_NATIVE_HELP_DONE=0
COPILOT_DOCKER_HELP_DONE=0
COPILOT_NATIVE_HELP_PLATFORM=""
COPILOT_DOCKER_HELP_PLATFORM=""
COPILOT_DOCKER_HELP_BINARY=""
COPILOT_DOCKER_HELP_LABEL=""

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
  github_get "${url}" "${output}" "application/vnd.github+json"
}

github_asset_get() {
  local url="$1"
  local output="$2"

  curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --connect-timeout 20 \
    -H "Accept: application/octet-stream" \
    "${url}" \
    -o "${output}"
}

github_get() {
  local url="$1"
  local output="$2"
  local accept="$3"
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
header = "Accept: ${accept}"
header = "Authorization: Bearer ${token}"
EOF
    return
  fi
  curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --connect-timeout 20 \
    -H "Accept: ${accept}" \
    "${url}" \
    -o "${output}"
}

discard_created_github_token_file() {
  [[ -z "${workcell_copilot_release_token_file_created}" || "${workcell_copilot_release_token_file_created}" != "${TMPDIR:-/tmp}"/workcell-github-token.* ]] || rm -f "${workcell_copilot_release_token_file_created}"
  workcell_copilot_release_token_file_created=""
}

scrub_github_token_env_for_child() {
  discard_created_github_token_file
  unset WORKCELL_GITHUB_API_TOKEN WORKCELL_GITHUB_API_TOKEN_FILE GITHUB_TOKEN GH_TOKEN
}

copilot_platform_for_linux_arch() {
  local arch="$1"

  case "${arch}" in
    x86_64 | amd64)
      printf '%s\n' "linux-x64"
      ;;
    aarch64 | arm64)
      printf '%s\n' "linux-arm64"
      ;;
    *)
      return 1
      ;;
  esac
}

detect_native_copilot_platform() {
  [[ "$(uname -s)" == "Linux" ]] || return 1
  copilot_platform_for_linux_arch "$(uname -m)"
}

detect_docker_copilot_platform() {
  local docker_info=""
  local os_type=""
  local arch=""

  command -v docker >/dev/null 2>&1 || return 1
  docker_info="$(docker info --format '{{.OSType}} {{.Architecture}}' 2>/dev/null)" || return 1
  os_type="${docker_info%% *}"
  arch="${docker_info#* }"
  [[ "${os_type}" == "linux" ]] || return 1
  copilot_platform_for_linux_arch "${arch}"
}

verify_copilot_help_flags_file() {
  local help_path="$1"
  local label="$2"
  local flag

  for flag in \
    --no-custom-instructions \
    --disable-builtin-mcps \
    --no-remote \
    --no-remote-export \
    --secret-env-vars \
    --available-tools; do
    if ! grep -Eq -- "(^|[^[:alnum:]_-])${flag}([^[:alnum:]_-]|$)" "${help_path}"; then
      echo "GitHub Copilot CLI v${COPILOT_VERSION} ${label} help is missing managed safety flag ${flag}" >&2
      exit 1
    fi
  done
}

run_native_copilot_help_probe() {
  local binary_path="$1"
  local label="$2"
  local help_path="${TMP_ROOT}/copilot-help-native.txt"
  local child_home="${TMP_ROOT}/copilot-native-home"

  mkdir -p "${child_home}"
  scrub_github_token_env_for_child
  if ! env -i \
    HOME="${child_home}" \
    PATH="/usr/bin:/bin" \
    TMPDIR="${TMP_ROOT}" \
    "${binary_path}" --help >"${help_path}" 2>&1; then
    echo "Failed to inspect GitHub Copilot CLI v${COPILOT_VERSION} ${label} help on the native Linux host" >&2
    exit 1
  fi
  verify_copilot_help_flags_file "${help_path}" "${label}"
  COPILOT_NATIVE_HELP_DONE=1
}

run_docker_copilot_help_probe() {
  local image="${WORKCELL_COPILOT_RELEASE_HELP_IMAGE:-${WORKCELL_IMAGE_TAG:-workcell:smoke}}"
  local binary_dir=""
  local help_path="${TMP_ROOT}/copilot-help-docker.txt"

  [[ -n "${COPILOT_DOCKER_HELP_BINARY}" ]] || return 1
  command -v docker >/dev/null 2>&1 || return 1
  docker image inspect "${image}" >/dev/null 2>&1 || return 1

  binary_dir="$(dirname "${COPILOT_DOCKER_HELP_BINARY}")"
  if ! docker run --rm \
    -v "${binary_dir}:/work:ro" \
    --entrypoint /work/copilot \
    "${image}" --help >"${help_path}" 2>&1; then
    echo "Failed to inspect GitHub Copilot CLI v${COPILOT_VERSION} ${COPILOT_DOCKER_HELP_LABEL} help inside Docker image ${image}" >&2
    return 1
  fi
  verify_copilot_help_flags_file "${help_path}" "${COPILOT_DOCKER_HELP_LABEL}"
  COPILOT_DOCKER_HELP_DONE=1
}

verify_asset() {
  local target_arch="$1"
  local platform="$2"
  local expected_sha="$3"
  local tarball_name="copilot-${platform}.tar.gz"
  local work_dir="${TMP_ROOT}/${target_arch}"
  local metadata_sha=""
  local asset_url=""

  mkdir -p "${work_dir}"
  metadata_sha="$(jq -r --arg asset_name "${tarball_name}" '.assets[] | select(.name == $asset_name) | .digest | sub("^sha256:"; "")' "${RELEASE_METADATA_PATH}")"
  asset_url="$(jq -r --arg asset_name "${tarball_name}" '.assets[] | select(.name == $asset_name) | .url // empty' "${RELEASE_METADATA_PATH}")"
  if [[ -z "${metadata_sha}" || "${metadata_sha}" == "null" ]]; then
    echo "GitHub Copilot CLI release v${COPILOT_VERSION} is missing ${tarball_name}" >&2
    exit 1
  fi
  if [[ -z "${asset_url}" || "${asset_url}" == "null" ]]; then
    echo "GitHub Copilot CLI release v${COPILOT_VERSION} asset ${tarball_name} is missing its API download URL" >&2
    exit 1
  fi
  if [[ "${metadata_sha}" != "${expected_sha}" ]]; then
    echo "Pinned ${target_arch} Copilot checksum does not match GitHub release metadata" >&2
    exit 1
  fi
  github_asset_get "${asset_url}" "${work_dir}/${tarball_name}"
  echo "${expected_sha}  ${work_dir}/${tarball_name}" | sha256sum -c - >/dev/null
  tar -tzf "${work_dir}/${tarball_name}" copilot >/dev/null
  [[ "${COPILOT_HELP_MODE}" == "checksum" ]] && return 0
  tar -xzf "${work_dir}/${tarball_name}" -C "${work_dir}" copilot
  if [[ "${COPILOT_HELP_MODE}" != "docker" && "${platform}" == "${COPILOT_NATIVE_HELP_PLATFORM}" && "${COPILOT_NATIVE_HELP_DONE}" != "1" ]]; then
    run_native_copilot_help_probe "${work_dir}/copilot" "${platform}"
  fi
  if [[ "${platform}" == "${COPILOT_DOCKER_HELP_PLATFORM}" ]]; then
    COPILOT_DOCKER_HELP_BINARY="${work_dir}/copilot"
    COPILOT_DOCKER_HELP_LABEL="${platform}"
  fi
}

require_tool curl
require_tool go
require_tool jq
require_tool sha256sum
require_tool tar

case "${COPILOT_HELP_MODE}" in
  auto | native | docker | checksum) ;;
  *)
    echo "Unsupported WORKCELL_COPILOT_RELEASE_HELP_MODE: ${COPILOT_HELP_MODE}" >&2
    exit 2
    ;;
esac

COPILOT_NATIVE_HELP_PLATFORM="$(detect_native_copilot_platform || true)"
COPILOT_DOCKER_HELP_PLATFORM="$(detect_docker_copilot_platform || true)"

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

case "${COPILOT_HELP_MODE}" in
  auto)
    if [[ "${COPILOT_NATIVE_HELP_DONE}" != "1" && "${COPILOT_DOCKER_HELP_DONE}" != "1" ]]; then
      if ! run_docker_copilot_help_probe; then
        echo "Copilot release help verification requires a Linux host or a local Workcell image matching the Docker engine architecture." >&2
        echo "Run ./scripts/container-smoke.sh first, or set WORKCELL_COPILOT_RELEASE_HELP_IMAGE to a local Workcell runtime image." >&2
        exit 1
      fi
    fi
    ;;
  native)
    if [[ -z "${COPILOT_NATIVE_HELP_PLATFORM}" || "${COPILOT_NATIVE_HELP_DONE}" != "1" ]]; then
      echo "Copilot release native help verification requires a Linux host matching a pinned Copilot asset architecture." >&2
      exit 1
    fi
    ;;
  docker)
    if ! run_docker_copilot_help_probe; then
      echo "Copilot release Docker help verification requires a local Workcell image matching the Docker engine architecture." >&2
      echo "Run ./scripts/container-smoke.sh first, or set WORKCELL_COPILOT_RELEASE_HELP_IMAGE to a local Workcell runtime image." >&2
      exit 1
    fi
    ;;
  checksum) ;;
esac

echo "Workcell upstream Copilot release verification passed."
