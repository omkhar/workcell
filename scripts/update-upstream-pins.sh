#!/bin/bash -p
workcell_upstream_pins_token_file_created=""
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  unset WORKCELL_UPSTREAM_PINS_TOKEN_FILE_CREATED
  if [[ -z "${WORKCELL_GITHUB_API_TOKEN_FILE:-}" ]]; then
    workcell_upstream_pins_token="${WORKCELL_GITHUB_API_TOKEN:-${GITHUB_TOKEN:-${GH_TOKEN:-}}}"
    unset WORKCELL_GITHUB_API_TOKEN GITHUB_TOKEN GH_TOKEN
    if [[ -n "${workcell_upstream_pins_token}" ]]; then
      workcell_upstream_pins_token_file="$(umask 077 && /usr/bin/mktemp "${TMPDIR:-/tmp}/workcell-github-token.XXXXXX")"
      printf '%s' "${workcell_upstream_pins_token}" >"${workcell_upstream_pins_token_file}"
      export WORKCELL_GITHUB_API_TOKEN_FILE="${workcell_upstream_pins_token_file}"
      workcell_upstream_pins_token_file_created="${workcell_upstream_pins_token_file}"
      export WORKCELL_UPSTREAM_PINS_TOKEN_FILE_CREATED="${workcell_upstream_pins_token_file_created}"
    fi
  fi
  unset WORKCELL_GITHUB_API_TOKEN GITHUB_TOKEN GH_TOKEN workcell_upstream_pins_token
fi
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/trusted-entrypoint.sh"
workcell_upstream_pins_token_file_created="${WORKCELL_UPSTREAM_PINS_TOKEN_FILE_CREATED:-${workcell_upstream_pins_token_file_created}}"
unset WORKCELL_UPSTREAM_PINS_TOKEN_FILE_CREATED

if [[ -z "${WORKCELL_GITHUB_API_TOKEN_FILE:-}" ]]; then
  workcell_upstream_pins_token="${WORKCELL_GITHUB_API_TOKEN:-${GITHUB_TOKEN:-${GH_TOKEN:-}}}"
  if [[ -n "${workcell_upstream_pins_token}" ]]; then
    workcell_upstream_pins_token_file="$(umask 077 && mktemp "${TMPDIR:-/tmp}/workcell-github-token.XXXXXX")"
    printf '%s' "${workcell_upstream_pins_token}" >"${workcell_upstream_pins_token_file}"
    export WORKCELL_GITHUB_API_TOKEN_FILE="${workcell_upstream_pins_token_file}"
    workcell_upstream_pins_token_file_created="${workcell_upstream_pins_token_file}"
  fi
fi
unset WORKCELL_GITHUB_API_TOKEN GITHUB_TOKEN GH_TOKEN workcell_upstream_pins_token

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "update-upstream-pins-entrypoint-ok"
  [[ -z "${workcell_upstream_pins_token_file_created}" || "${workcell_upstream_pins_token_file_created}" != "${TMPDIR:-/tmp}"/workcell-github-token.* ]] || rm -f "${workcell_upstream_pins_token_file_created}"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUNTIME_DOCKERFILE_PATH="${ROOT_DIR}/runtime/container/Dockerfile"
VALIDATOR_DOCKERFILE_PATH="${ROOT_DIR}/tools/validator/Dockerfile"
GO_MOD_PATH="${ROOT_DIR}/go.mod"
RUST_TOOLCHAIN_PATH="${ROOT_DIR}/runtime/container/rust/rust-toolchain.toml"
CARGO_MANIFEST_PATH="${ROOT_DIR}/runtime/container/rust/Cargo.toml"
CI_WORKFLOW_PATH="${ROOT_DIR}/.github/workflows/ci.yml"
DOCS_WORKFLOW_PATH="${ROOT_DIR}/.github/workflows/docs.yml"
VALIDATOR_IMAGE_SCRIPT_PATH="${ROOT_DIR}/scripts/ci/build-validator-image.sh"
PIN_HYGIENE_WORKFLOW_PATH="${ROOT_DIR}/.github/workflows/pin-hygiene.yml"
RELEASE_WORKFLOW_PATH="${ROOT_DIR}/.github/workflows/release.yml"
SECURITY_WORKFLOW_PATH="${ROOT_DIR}/.github/workflows/security.yml"
UPSTREAM_REFRESH_WORKFLOW_PATH="${ROOT_DIR}/.github/workflows/upstream-refresh.yml"

mode="summary"

cleanup() {
  [[ -z "${workcell_upstream_pins_token_file_created}" || "${workcell_upstream_pins_token_file_created}" != "${TMPDIR:-/tmp}"/workcell-github-token.* ]] || rm -f "${workcell_upstream_pins_token_file_created}"
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage: scripts/update-upstream-pins.sh [--apply | --check]

Modes:
  --apply   Refresh pinned provider versions, Linux base images, toolchains, and
            release-build inputs to the newest reviewed upstream versions.
  --check   Exit non-zero when any eligible pinned upstream refresh is pending.

Without a mode flag, the script prints a human-readable summary.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --apply)
      mode="apply"
      shift
      ;;
    --check)
      mode="check"
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_tool awk
require_tool curl
require_tool docker
require_tool go
require_tool jq
require_tool mktemp
require_tool shasum

# API response bodies are JSON/TOML; 200 MiB cap is well above realistic
# upstream sizes (e.g. the Rust channel TOML is ~13 MiB and growing,
# GitHub release lists are at most a few MiB) while still rejecting a
# multi-GB body from a misbehaving or compromised endpoint.
CURL_API_GUARDS=(--max-time 120 --connect-timeout 15 --max-filesize 209715200)
DEBIAN_SNAPSHOT_LOOKBACK_DAYS="${WORKCELL_DEBIAN_SNAPSHOT_LOOKBACK_DAYS:-${WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS:-45}}"

github_api_token() {
  local token="${WORKCELL_GITHUB_API_TOKEN:-${GITHUB_TOKEN:-${GH_TOKEN:-}}}"
  if [[ -z "${token}" && -n "${WORKCELL_GITHUB_API_TOKEN_FILE:-}" ]]; then
    if [[ ! -r "${WORKCELL_GITHUB_API_TOKEN_FILE}" ]]; then
      echo "GitHub API token file is not readable: ${WORKCELL_GITHUB_API_TOKEN_FILE}" >&2
      exit 1
    fi
    token="$(<"${WORKCELL_GITHUB_API_TOKEN_FILE}")"
  fi
  if [[ -n "${token}" && ("${token}" == *$'\n'* || "${token}" == *$'\r'*) ]]; then
    echo "GitHub API token must be a single line" >&2
    exit 1
  fi
  printf '%s' "${token}"
}

github_api_get() {
  local url="$1"
  local token
  token="$(github_api_token)"
  if [[ -n "${token}" ]]; then
    curl -fsSL "${CURL_API_GUARDS[@]}" --config - "${url}" <<EOF
header = "Accept: application/vnd.github+json"
header = "Authorization: Bearer ${token}"
EOF
    return
  fi
  curl -fsSL "${CURL_API_GUARDS[@]}" -H "Accept: application/vnd.github+json" "${url}"
}

dockerhub_api_get() {
  curl -fsSL "${CURL_API_GUARDS[@]}" "$1"
}

github_release_asset_url() {
  local release_json="$1"
  local asset_name="$2"
  local asset_url
  asset_url="$(jq -r --arg asset_name "${asset_name}" '.assets[] | select(.name == $asset_name) | .browser_download_url' <<<"${release_json}")"
  if [[ -z "${asset_url}" || "${asset_url}" == "null" ]]; then
    echo "Unable to resolve release asset ${asset_name}" >&2
    exit 1
  fi
  printf '%s\n' "${asset_url}"
}

github_release_asset_api_url() {
  local release_json="$1"
  local asset_name="$2"
  local asset_url
  asset_url="$(jq -r --arg asset_name "${asset_name}" '.assets[] | select(.name == $asset_name) | .url' <<<"${release_json}")"
  if [[ -z "${asset_url}" || "${asset_url}" == "null" ]]; then
    echo "Unable to resolve release asset ${asset_name}" >&2
    exit 1
  fi
  printf '%s\n' "${asset_url}"
}

github_release_asset_get() {
  local url="$1"
  local token
  local body_file=""
  local headers_file=""
  local location=""
  local status=""

  token="$(github_api_token)"
  if [[ -n "${token}" ]]; then
    headers_file="$(mktemp "${TMPDIR:-/tmp}/workcell-release-asset-headers.XXXXXX")"
    body_file="$(mktemp "${TMPDIR:-/tmp}/workcell-release-asset-body.XXXXXX")"
    trap 'rm -f "${headers_file}" "${body_file}"' RETURN

    status="$(
      curl -fsS "${CURL_CHECKSUM_GUARDS[@]}" \
        -D "${headers_file}" \
        -o "${body_file}" \
        -w '%{http_code}' \
        -H "Accept: application/octet-stream" \
        --config - \
        "${url}" <<EOF
header = "Authorization: Bearer ${token}"
EOF
    )"
    case "${status}" in
      200)
        cat "${body_file}"
        ;;
      3??)
        location="$(sed -n 's/^[Ll][Oo][Cc][Aa][Tt][Ii][Oo][Nn]:[[:space:]]*//p' "${headers_file}" | tail -n 1 | tr -d '\r')"
        if [[ -z "${location}" ]]; then
          echo "GitHub release asset redirect did not include a Location header: ${url}" >&2
          exit 1
        fi
        curl -fsSL "${CURL_CHECKSUM_GUARDS[@]}" "${location}"
        ;;
      *)
        echo "Unexpected GitHub release asset response ${status}: ${url}" >&2
        exit 1
        ;;
    esac
    rm -f "${headers_file}" "${body_file}"
    trap - RETURN
    return 0
  fi
  curl -fsSL "${CURL_CHECKSUM_GUARDS[@]}" -H "Accept: application/octet-stream" "${url}"
}

github_tag_commit_sha() {
  local repo="$1"
  local tag="$2"
  local ref_json object_sha object_type tag_json

  ref_json="$(github_api_get "https://api.github.com/repos/${repo}/git/ref/tags/${tag}")"
  object_sha="$(jq -r '.object.sha' <<<"${ref_json}")"
  object_type="$(jq -r '.object.type' <<<"${ref_json}")"
  case "${object_type}" in
    commit)
      printf '%s\n' "${object_sha}"
      ;;
    tag)
      tag_json="$(github_api_get "https://api.github.com/repos/${repo}/git/tags/${object_sha}")"
      jq -r '.object.sha' <<<"${tag_json}"
      ;;
    *)
      echo "Unable to resolve commit SHA for ${repo} tag ${tag}" >&2
      exit 1
      ;;
  esac
}

docker_image_digest() {
  local image_ref="$1"
  local digest
  digest="$(docker buildx imagetools inspect "${image_ref}" | awk '/^Digest:/ {print $2; exit}')"
  if [[ -z "${digest}" ]]; then
    echo "Unable to resolve image digest for ${image_ref}" >&2
    exit 1
  fi
  printf '%s\n' "${digest}"
}

extract_dockerfile_arg() {
  (
    cd "${ROOT_DIR}"
    go run ./cmd/workcell-citools extract-dockerfile-arg "$1" "$2"
  )
}

extract_yaml_scalar() {
  local file="$1"
  local key="$2"
  awk -v key="${key}" '$1 == key ":" { print $2; exit }' "${file}"
}

extract_workflow_env_value() {
  local file="$1"
  local key="$2"
  awk -v key="${key}:" '
    $1 == key {
      if (count == 0) {
        value = $2
      } else if ($2 != value) {
        printf "%s must use one reviewed value in %s; found both %s and %s\n", key, FILENAME, value, $2 > "/dev/stderr"
        failed = 1
        exit 4
      }
      count++
    }
    END {
      if (failed) {
        exit 4
      }
      if (count == 0) {
        exit 3
      }
      print value
    }
  ' "${file}"
}

replace_line_with_prefix() {
  local file="$1"
  local prefix="$2"
  local newline="$3"
  local tmp
  tmp="$(mktemp "${TMPDIR:-/tmp}/workcell-upstream-refresh.XXXXXX")"
  if ! awk -v prefix="${prefix}" -v newline="${newline}" '
    BEGIN { replaced = 0 }
    index($0, prefix) == 1 && replaced == 0 {
      print newline
      replaced = 1
      next
    }
    { print }
    END {
      if (replaced == 0) {
        exit 3
      }
    }
  ' "${file}" >"${tmp}"; then
    rm -f "${tmp}"
    echo "Unable to replace ${prefix} in ${file}" >&2
    exit 1
  fi
  mv "${tmp}" "${file}"
}

replace_all_lines_with_prefix() {
  local file="$1"
  local prefix="$2"
  local newline="$3"
  local tmp
  tmp="$(mktemp "${TMPDIR:-/tmp}/workcell-upstream-refresh.XXXXXX")"
  if ! awk -v prefix="${prefix}" -v newline="${newline}" '
    BEGIN { replaced = 0 }
    index($0, prefix) == 1 {
      print newline
      replaced++
      next
    }
    { print }
    END {
      if (replaced == 0) {
        exit 3
      }
    }
  ' "${file}" >"${tmp}"; then
    rm -f "${tmp}"
    echo "Failed to update ${prefix} in ${file}" >&2
    exit 1
  fi
  mv "${tmp}" "${file}"
}

replace_line_after_marker_with_prefix() {
  local file="$1"
  local marker="$2"
  local prefix="$3"
  local newline="$4"
  local tmp
  tmp="$(mktemp "${TMPDIR:-/tmp}/workcell-upstream-refresh.XXXXXX")"
  if ! awk -v marker="${marker}" -v prefix="${prefix}" -v newline="${newline}" '
    BEGIN { matched = 0; replaced = 0 }
    index($0, marker) > 0 && replaced == 0 {
      matched = 1
      print
      next
    }
    matched == 1 && index($0, prefix) == 1 && replaced == 0 {
      print newline
      replaced = 1
      matched = 0
      next
    }
    { print }
    END {
      if (replaced == 0) {
        exit 3
      }
    }
  ' "${file}" >"${tmp}"; then
    rm -f "${tmp}"
    echo "Unable to replace ${prefix} after ${marker} in ${file}" >&2
    exit 1
  fi
  mv "${tmp}" "${file}"
}

date_stamp_for_offset() {
  local offset="$1"
  if date -u -d '1970-01-01 UTC' +%Y%m%dT000000Z >/dev/null 2>&1; then
    if [[ "${offset}" == "0" ]]; then
      date -u +%Y%m%dT000000Z
    else
      date -u -d "-${offset} day" +%Y%m%dT000000Z
    fi
    return
  fi
  if [[ "${offset}" == "0" ]]; then
    date -u +%Y%m%dT000000Z
  else
    date -u -v-"${offset}"d +%Y%m%dT000000Z
  fi
}

latest_debian_snapshot() {
  local stamp
  local offset
  local lookback_days="${DEBIAN_SNAPSHOT_LOOKBACK_DAYS:-45}"
  for offset in $(seq 0 "${lookback_days}"); do
    stamp="$(date_stamp_for_offset "${offset}")"
    if curl -fsSI "https://snapshot.debian.org/archive/debian/${stamp}/dists/trixie/Release" >/dev/null &&
      curl -fsSI "https://snapshot.debian.org/archive/debian/${stamp}/dists/trixie-updates/Release" >/dev/null &&
      curl -fsSI "https://snapshot.debian.org/archive/debian-security/${stamp}/dists/trixie-security/Release" >/dev/null &&
      debian_snapshot_has_bootstrap_packages "${stamp}"; then
      printf '%s\n' "${stamp}"
      return
    fi
  done
  echo "Unable to resolve a Debian snapshot within ${lookback_days} days for trixie/trixie-updates/trixie-security with bootstrap packages" >&2
  exit 1
}

debian_snapshot_has_bootstrap_packages() {
  local stamp="$1"
  local base="https://snapshot.debian.org/archive/debian/${stamp}"
  local path

  for path in \
    "pool/main/o/openssl/openssl_3.5.5-1~deb13u1_amd64.deb" \
    "pool/main/o/openssl/openssl_3.5.5-1~deb13u1_arm64.deb" \
    "pool/main/c/ca-certificates/ca-certificates_20250419_all.deb"; do
    if ! curl -fsSI "${base}/${path}" >/dev/null 2>&1; then
      return 1
    fi
  done
}

semver_patch_zero() {
  local version="$1"
  IFS='.' read -r major minor _ <<<"${version}"
  printf '%s.%s.0\n' "${major}" "${minor}"
}

semver_major_minor() {
  local version="$1"
  IFS='.' read -r major minor _ <<<"${version}"
  printf '%s.%s\n' "${major}" "${minor}"
}

latest_qemu_tag() {
  dockerhub_api_get "https://hub.docker.com/v2/repositories/tonistiigi/binfmt/tags?page_size=100" |
    jq -r '
        [
          .results[].name
          | select(test("^qemu-v[0-9]+\\.[0-9]+\\.[0-9]+(?:-[0-9]+)?$"))
          | capture("^qemu-v(?<major>[0-9]+)\\.(?<minor>[0-9]+)\\.(?<patch>[0-9]+)(?:-(?<revision>[0-9]+))?$")
          | {
              tag: (
                "qemu-v" + .major + "." + .minor + "." + .patch +
                (if .revision then "-" + .revision else "" end)
              ),
              major: (.major | tonumber),
              minor: (.minor | tonumber),
              patch: (.patch | tonumber),
              revision: ((.revision // "0") | tonumber)
            }
        ]
        | sort_by(.major, .minor, .patch, .revision)
        | last
        | .tag
      '
}

latest_go_json="$(curl -fsSL "${CURL_API_GUARDS[@]}" 'https://go.dev/dl/?mode=json' | jq 'map(select(.stable == true)) | .[0]')"
target_go_toolchain="$(jq -r '.version | sub("^go"; "")' <<<"${latest_go_json}")"
target_go_language="$(semver_patch_zero "${target_go_toolchain}")"
target_go_sha_amd64="$(jq -r '.files[] | select(.os == "linux" and .arch == "amd64" and .kind == "archive") | .sha256' <<<"${latest_go_json}")"
target_go_sha_arm64="$(jq -r '.files[] | select(.os == "linux" and .arch == "arm64" and .kind == "archive") | .sha256' <<<"${latest_go_json}")"

rust_stable_toml="$(curl -fsSL "${CURL_API_GUARDS[@]}" 'https://static.rust-lang.org/dist/channel-rust-stable.toml')"
target_rust_version="$(
  awk -F'"' '
    /^\[pkg\.rust\]$/ {
      in_rust = 1
      next
    }
    /^\[/ {
      in_rust = 0
    }
    in_rust && $1 == "version = " {
      split($2, parts, " ")
      print parts[1]
      exit
    }
  ' <<<"${rust_stable_toml}"
)"
target_cargo_rust_version="$(semver_major_minor "${target_rust_version}")"
rustup_stable_toml="$(curl -fsSL "${CURL_API_GUARDS[@]}" 'https://static.rust-lang.org/rustup/release-stable.toml')"
target_rustup_version="$(awk -F"'" '$1 == "version = " { print $2; exit }' <<<"${rustup_stable_toml}")"
# SHA256 checksum bodies are at most ~80 bytes; cap the response so a
# misbehaving CDN or compromised release host cannot serve a multi-GB
# body that OOMs the maintainer's shell.  Mirrors the discipline applied
# to the zizmor download below.
CURL_CHECKSUM_GUARDS=(--max-time 60 --connect-timeout 15 --max-filesize 65536)
target_rustup_sha_amd64="$(curl -fsSL "${CURL_CHECKSUM_GUARDS[@]}" "https://static.rust-lang.org/rustup/archive/${target_rustup_version}/x86_64-unknown-linux-gnu/rustup-init.sha256" | awk '{print $1}')"
target_rustup_sha_arm64="$(curl -fsSL "${CURL_CHECKSUM_GUARDS[@]}" "https://static.rust-lang.org/rustup/archive/${target_rustup_version}/aarch64-unknown-linux-gnu/rustup-init.sha256" | awk '{print $1}')"

hadolint_release_json="$(github_api_get 'https://api.github.com/repos/hadolint/hadolint/releases/latest')"
target_hadolint_version="$(jq -r '.tag_name' <<<"${hadolint_release_json}")"
hadolint_sha_amd64_url="$(github_release_asset_url "${hadolint_release_json}" 'hadolint-linux-x86_64.sha256')"
hadolint_sha_arm64_url="$(github_release_asset_url "${hadolint_release_json}" 'hadolint-linux-arm64.sha256')"
target_hadolint_sha_amd64="$(
  curl -fsSL "${CURL_CHECKSUM_GUARDS[@]}" "${hadolint_sha_amd64_url}" | awk '{print $1}'
)"
target_hadolint_sha_arm64="$(
  curl -fsSL "${CURL_CHECKSUM_GUARDS[@]}" "${hadolint_sha_arm64_url}" | awk '{print $1}'
)"

buildx_release_json="$(github_api_get 'https://api.github.com/repos/docker/buildx/releases/latest')"
target_buildx_version="$(jq -r '.tag_name' <<<"${buildx_release_json}")"

cosign_release_json="$(github_api_get 'https://api.github.com/repos/sigstore/cosign/releases/latest')"
target_cosign_version="$(jq -r '.tag_name' <<<"${cosign_release_json}")"

syft_release_json="$(github_api_get 'https://api.github.com/repos/anchore/syft/releases/latest')"
target_syft_version="$(jq -r '.tag_name' <<<"${syft_release_json}")"

actionlint_release_json="$(github_api_get 'https://api.github.com/repos/rhysd/actionlint/releases/latest')"
target_actionlint_version="$(jq -r '.tag_name | sub("^v"; "")' <<<"${actionlint_release_json}")"
actionlint_checksums_url="$(github_release_asset_api_url "${actionlint_release_json}" "actionlint_${target_actionlint_version}_checksums.txt")"
target_actionlint_sha="$(
  github_release_asset_get "${actionlint_checksums_url}" |
    awk '/actionlint_'"${target_actionlint_version}"'_linux_amd64\.tar\.gz$/ { print $1; exit }'
)"

zizmor_release_json="$(github_api_get 'https://api.github.com/repos/zizmorcore/zizmor/releases/latest')"
target_zizmor_version="$(jq -r '.tag_name | sub("^v"; "")' <<<"${zizmor_release_json}")"
target_zizmor_url="$(github_release_asset_url "${zizmor_release_json}" 'zizmor-x86_64-unknown-linux-gnu.tar.gz')"
target_zizmor_sha="$(
  curl -fsSL \
    --max-time 60 \
    --connect-timeout 15 \
    --max-filesize 209715200 \
    "${target_zizmor_url}" |
    shasum -a 256 |
    awk '{ print $1 }'
)"
current_runtime_base="$(extract_dockerfile_arg "${RUNTIME_DOCKERFILE_PATH}" NODE_BASE_IMAGE)"
current_validator_base="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" VALIDATOR_BASE_IMAGE)"
current_runtime_snapshot="$(extract_dockerfile_arg "${RUNTIME_DOCKERFILE_PATH}" DEBIAN_SNAPSHOT)"
current_validator_snapshot="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" DEBIAN_SNAPSHOT)"
current_go_toolchain="$(awk '/^toolchain / { sub(/^toolchain go/, "", $0); print; exit }' "${GO_MOD_PATH}")"
current_go_language="$(awk '/^go / { print $2; exit }' "${GO_MOD_PATH}")"
current_validator_go_version="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" GO_VERSION)"
current_go_sha_amd64="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" GO_LINUX_X86_64_SHA256)"
current_go_sha_arm64="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" GO_LINUX_ARM64_SHA256)"
current_rust_version="$(extract_dockerfile_arg "${RUNTIME_DOCKERFILE_PATH}" RUST_VERSION)"
current_runtime_rust_toolchain_image="$(extract_dockerfile_arg "${RUNTIME_DOCKERFILE_PATH}" RUST_TOOLCHAIN_IMAGE)"
current_rust_toolchain_channel="$(awk -F'"' '/^channel = / { print $2; exit }' "${RUST_TOOLCHAIN_PATH}")"
current_cargo_rust_version="$(awk -F'"' '/^rust-version = / { print $2; exit }' "${CARGO_MANIFEST_PATH}")"
current_rustup_version="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" RUSTUP_VERSION)"
current_rustup_sha_amd64="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" RUSTUP_INIT_LINUX_X86_64_SHA256)"
current_rustup_sha_arm64="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" RUSTUP_INIT_LINUX_ARM64_SHA256)"
current_hadolint_version="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" HADOLINT_VERSION)"
current_hadolint_sha_amd64="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" HADOLINT_LINUX_X86_64_SHA256)"
current_hadolint_sha_arm64="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" HADOLINT_LINUX_ARM64_SHA256)"
current_buildkit_image="$(extract_yaml_scalar "${CI_WORKFLOW_PATH}" WORKCELL_BUILDKIT_IMAGE)"
current_buildx_version="$(extract_yaml_scalar "${CI_WORKFLOW_PATH}" WORKCELL_BUILDX_VERSION)"
current_docs_buildkit_image="$(extract_yaml_scalar "${DOCS_WORKFLOW_PATH}" WORKCELL_BUILDKIT_IMAGE)"
current_docs_buildx_version="$(extract_yaml_scalar "${DOCS_WORKFLOW_PATH}" WORKCELL_BUILDX_VERSION)"
current_validator_image_buildkit_fallback="$(awk -v prefix="BUILDKIT_IMAGE=\"\${WORKCELL_BUILDKIT_IMAGE:-" '
  index($0, prefix) == 1 {
    value = substr($0, length(prefix) + 1)
    sub(/}"$/, "", value)
    print value
    exit
  }' "${VALIDATOR_IMAGE_SCRIPT_PATH}")"
current_cosign_version="$(extract_yaml_scalar "${CI_WORKFLOW_PATH}" WORKCELL_COSIGN_VERSION)"
current_upstream_refresh_cosign_version="$(extract_yaml_scalar "${UPSTREAM_REFRESH_WORKFLOW_PATH}" WORKCELL_COSIGN_VERSION)"
current_qemu_image="$(extract_yaml_scalar "${CI_WORKFLOW_PATH}" WORKCELL_QEMU_IMAGE)"
current_syft_version="$(extract_yaml_scalar "${RELEASE_WORKFLOW_PATH}" WORKCELL_SYFT_VERSION)"
current_actionlint_version="$(extract_workflow_env_value "${SECURITY_WORKFLOW_PATH}" ACTIONLINT_VERSION)"
current_actionlint_sha="$(extract_workflow_env_value "${SECURITY_WORKFLOW_PATH}" ACTIONLINT_SHA256)"
current_zizmor_version="$(extract_workflow_env_value "${SECURITY_WORKFLOW_PATH}" ZIZMOR_VERSION)"
current_zizmor_sha="$(extract_workflow_env_value "${SECURITY_WORKFLOW_PATH}" ZIZMOR_SHA256)"
current_release_zizmor_version="$(extract_workflow_env_value "${RELEASE_WORKFLOW_PATH}" ZIZMOR_VERSION)"
current_release_zizmor_sha="$(extract_workflow_env_value "${RELEASE_WORKFLOW_PATH}" ZIZMOR_SHA256)"

runtime_base_track="${current_runtime_base%@*}"
validator_base_track="${current_validator_base%@*}"
buildkit_track="${current_buildkit_image%@*}"

target_runtime_base="${runtime_base_track}@$(docker_image_digest "${runtime_base_track}")"
target_validator_base="${validator_base_track}@$(docker_image_digest "${validator_base_track}")"
target_runtime_rust_toolchain_image="rust:${target_rust_version}-slim-trixie@$(docker_image_digest "rust:${target_rust_version}-slim-trixie")"
target_debian_snapshot="$(latest_debian_snapshot)"
target_buildkit_image="${buildkit_track}@$(docker_image_digest "${buildkit_track}")"
target_qemu_tag="$(latest_qemu_tag)"
target_qemu_image="tonistiigi/binfmt:${target_qemu_tag}@$(docker_image_digest "tonistiigi/binfmt:${target_qemu_tag}")"

provider_summary="$("${ROOT_DIR}/scripts/update-provider-pins.sh")"
provider_check_status=0
"${ROOT_DIR}/scripts/update-provider-pins.sh" --check >/dev/null 2>&1 || provider_check_status=$?
if [[ "${provider_check_status}" -ne 0 && "${provider_check_status}" -ne 1 ]]; then
  echo "Unable to compute provider bump status." >&2
  exit "${provider_check_status}"
fi
provider_has_changes=0
if [[ "${provider_check_status}" -eq 1 ]]; then
  provider_has_changes=1
fi

has_changes=0
for current_target_pair in \
  "${current_runtime_base}|${target_runtime_base}" \
  "${current_validator_base}|${target_validator_base}" \
  "${current_runtime_snapshot}|${target_debian_snapshot}" \
  "${current_validator_snapshot}|${target_debian_snapshot}" \
  "${current_go_toolchain}|${target_go_toolchain}" \
  "${current_go_language}|${target_go_language}" \
  "${current_validator_go_version}|${target_go_toolchain}" \
  "${current_go_sha_amd64}|${target_go_sha_amd64}" \
  "${current_go_sha_arm64}|${target_go_sha_arm64}" \
  "${current_rust_version}|${target_rust_version}" \
  "${current_runtime_rust_toolchain_image}|${target_runtime_rust_toolchain_image}" \
  "${current_rust_toolchain_channel}|${target_rust_version}" \
  "${current_cargo_rust_version}|${target_cargo_rust_version}" \
  "${current_rustup_version}|${target_rustup_version}" \
  "${current_rustup_sha_amd64}|${target_rustup_sha_amd64}" \
  "${current_rustup_sha_arm64}|${target_rustup_sha_arm64}" \
  "${current_hadolint_version}|${target_hadolint_version}" \
  "${current_hadolint_sha_amd64}|${target_hadolint_sha_amd64}" \
  "${current_hadolint_sha_arm64}|${target_hadolint_sha_arm64}" \
  "${current_buildkit_image}|${target_buildkit_image}" \
  "${current_buildx_version}|${target_buildx_version}" \
  "${current_docs_buildkit_image}|${target_buildkit_image}" \
  "${current_docs_buildx_version}|${target_buildx_version}" \
  "${current_validator_image_buildkit_fallback}|${target_buildkit_image}" \
  "${current_cosign_version}|${target_cosign_version}" \
  "${current_upstream_refresh_cosign_version}|${target_cosign_version}" \
  "${current_qemu_image}|${target_qemu_image}" \
  "${current_syft_version}|${target_syft_version}" \
  "${current_actionlint_version}|${target_actionlint_version}" \
  "${current_actionlint_sha}|${target_actionlint_sha}" \
  "${current_zizmor_version}|${target_zizmor_version}" \
  "${current_zizmor_sha}|${target_zizmor_sha}" \
  "${current_release_zizmor_version}|${target_zizmor_version}" \
  "${current_release_zizmor_sha}|${target_zizmor_sha}"; do
  current_value="${current_target_pair%%|*}"
  target_value="${current_target_pair#*|}"
  if [[ "${current_value}" != "${target_value}" ]]; then
    has_changes=1
    break
  fi
done
if [[ "${provider_has_changes}" -eq 1 ]]; then
  has_changes=1
fi

print_summary_line() {
  local label="$1"
  local current_value="$2"
  local target_value="$3"
  if [[ "${current_value}" == "${target_value}" ]]; then
    printf '  %s: %s (up to date)\n' "${label}" "${current_value}"
    return
  fi
  printf '  %s: %s -> %s\n' "${label}" "${current_value}" "${target_value}"
}

print_summary() {
  echo "Pinned upstream refresh summary:"
  print_summary_line "runtime-base" "${current_runtime_base}" "${target_runtime_base}"
  print_summary_line "validator-base" "${current_validator_base}" "${target_validator_base}"
  print_summary_line "debian-snapshot" "${current_runtime_snapshot}" "${target_debian_snapshot}"
  print_summary_line "go-toolchain" "${current_go_toolchain}" "${target_go_toolchain}"
  print_summary_line "go-language" "${current_go_language}" "${target_go_language}"
  print_summary_line "rust-toolchain" "${current_rust_version}" "${target_rust_version}"
  print_summary_line "runtime-rust-image" "${current_runtime_rust_toolchain_image}" "${target_runtime_rust_toolchain_image}"
  print_summary_line "rustup" "${current_rustup_version}" "${target_rustup_version}"
  print_summary_line "hadolint" "${current_hadolint_version}" "${target_hadolint_version}"
  print_summary_line "buildkit-image" "${current_buildkit_image}" "${target_buildkit_image}"
  print_summary_line "buildx-version" "${current_buildx_version}" "${target_buildx_version}"
  print_summary_line "cosign-version" "${current_cosign_version}" "${target_cosign_version}"
  print_summary_line "upstream-refresh-cosign-version" "${current_upstream_refresh_cosign_version}" "${target_cosign_version}"
  print_summary_line "qemu-image" "${current_qemu_image}" "${target_qemu_image}"
  print_summary_line "syft-version" "${current_syft_version}" "${target_syft_version}"
  print_summary_line "actionlint-version" "${current_actionlint_version}" "${target_actionlint_version}"
  print_summary_line "zizmor-version" "${current_zizmor_version}" "${target_zizmor_version}"
  print_summary_line "release-zizmor-version" "${current_release_zizmor_version}" "${target_zizmor_version}"
  print_summary_line "release-zizmor-sha" "${current_release_zizmor_sha}" "${target_zizmor_sha}"
  printf '%s\n' "${provider_summary}"
}

if [[ "${mode}" == "summary" ]]; then
  print_summary
  exit 0
fi

if [[ "${mode}" == "check" ]]; then
  print_summary
  if [[ "${has_changes}" -eq 1 ]]; then
    exit 1
  fi
  exit 0
fi

if [[ "${has_changes}" -eq 0 ]]; then
  echo "No pinned upstream updates found."
  exit 0
fi

replace_line_with_prefix "${RUNTIME_DOCKERFILE_PATH}" 'ARG NODE_BASE_IMAGE=' "ARG NODE_BASE_IMAGE=${target_runtime_base}"
replace_line_with_prefix "${RUNTIME_DOCKERFILE_PATH}" 'ARG DEBIAN_SNAPSHOT=' "ARG DEBIAN_SNAPSHOT=${target_debian_snapshot}"
replace_line_with_prefix "${RUNTIME_DOCKERFILE_PATH}" 'ARG RUST_VERSION=' "ARG RUST_VERSION=${target_rust_version}"
replace_line_with_prefix "${RUNTIME_DOCKERFILE_PATH}" 'ARG RUST_TOOLCHAIN_IMAGE=' "ARG RUST_TOOLCHAIN_IMAGE=${target_runtime_rust_toolchain_image}"

dockerfile_path="${VALIDATOR_DOCKERFILE_PATH}"
target_base="${target_validator_base}"
replace_line_with_prefix "${dockerfile_path}" 'ARG VALIDATOR_BASE_IMAGE=' "ARG VALIDATOR_BASE_IMAGE=${target_base}"
replace_line_with_prefix "${dockerfile_path}" 'ARG DEBIAN_SNAPSHOT=' "ARG DEBIAN_SNAPSHOT=${target_debian_snapshot}"
replace_line_with_prefix "${dockerfile_path}" 'ARG GO_VERSION=' "ARG GO_VERSION=${target_go_toolchain}"
replace_line_with_prefix "${dockerfile_path}" 'ARG GO_LINUX_X86_64_SHA256=' "ARG GO_LINUX_X86_64_SHA256=${target_go_sha_amd64}"
replace_line_with_prefix "${dockerfile_path}" 'ARG GO_LINUX_ARM64_SHA256=' "ARG GO_LINUX_ARM64_SHA256=${target_go_sha_arm64}"
replace_line_with_prefix "${dockerfile_path}" 'ARG HADOLINT_VERSION=' "ARG HADOLINT_VERSION=${target_hadolint_version}"
replace_line_with_prefix "${dockerfile_path}" 'ARG HADOLINT_LINUX_X86_64_SHA256=' "ARG HADOLINT_LINUX_X86_64_SHA256=${target_hadolint_sha_amd64}"
replace_line_with_prefix "${dockerfile_path}" 'ARG HADOLINT_LINUX_ARM64_SHA256=' "ARG HADOLINT_LINUX_ARM64_SHA256=${target_hadolint_sha_arm64}"
replace_line_with_prefix "${dockerfile_path}" 'ARG RUST_VERSION=' "ARG RUST_VERSION=${target_rust_version}"
replace_line_with_prefix "${dockerfile_path}" 'ARG RUSTUP_VERSION=' "ARG RUSTUP_VERSION=${target_rustup_version}"
replace_line_with_prefix "${dockerfile_path}" 'ARG RUSTUP_INIT_LINUX_X86_64_SHA256=' "ARG RUSTUP_INIT_LINUX_X86_64_SHA256=${target_rustup_sha_amd64}"
replace_line_with_prefix "${dockerfile_path}" 'ARG RUSTUP_INIT_LINUX_ARM64_SHA256=' "ARG RUSTUP_INIT_LINUX_ARM64_SHA256=${target_rustup_sha_arm64}"

replace_line_with_prefix "${GO_MOD_PATH}" 'go ' "go ${target_go_language}"
replace_line_with_prefix "${GO_MOD_PATH}" 'toolchain go' "toolchain go${target_go_toolchain}"
replace_line_with_prefix "${RUST_TOOLCHAIN_PATH}" 'channel = ' "channel = \"${target_rust_version}\""
replace_line_with_prefix "${CARGO_MANIFEST_PATH}" 'rust-version = ' "rust-version = \"${target_cargo_rust_version}\""

replace_line_with_prefix "${CI_WORKFLOW_PATH}" '  WORKCELL_BUILDKIT_IMAGE:' "  WORKCELL_BUILDKIT_IMAGE: ${target_buildkit_image}"
replace_line_with_prefix "${CI_WORKFLOW_PATH}" '  WORKCELL_BUILDX_VERSION:' "  WORKCELL_BUILDX_VERSION: ${target_buildx_version}"
replace_line_with_prefix "${CI_WORKFLOW_PATH}" '  WORKCELL_COSIGN_VERSION:' "  WORKCELL_COSIGN_VERSION: ${target_cosign_version}"
replace_line_with_prefix "${CI_WORKFLOW_PATH}" '  WORKCELL_QEMU_IMAGE:' "  WORKCELL_QEMU_IMAGE: ${target_qemu_image}"

replace_line_with_prefix "${DOCS_WORKFLOW_PATH}" '  WORKCELL_BUILDKIT_IMAGE:' "  WORKCELL_BUILDKIT_IMAGE: ${target_buildkit_image}"
replace_line_with_prefix "${DOCS_WORKFLOW_PATH}" '  WORKCELL_BUILDX_VERSION:' "  WORKCELL_BUILDX_VERSION: ${target_buildx_version}"
replace_line_with_prefix "${VALIDATOR_IMAGE_SCRIPT_PATH}" "BUILDKIT_IMAGE=\"\${WORKCELL_BUILDKIT_IMAGE:-" "BUILDKIT_IMAGE=\"\${WORKCELL_BUILDKIT_IMAGE:-${target_buildkit_image}}\""

replace_line_with_prefix "${PIN_HYGIENE_WORKFLOW_PATH}" '  WORKCELL_COSIGN_VERSION:' "  WORKCELL_COSIGN_VERSION: ${target_cosign_version}"
replace_line_with_prefix "${UPSTREAM_REFRESH_WORKFLOW_PATH}" '  WORKCELL_COSIGN_VERSION:' "  WORKCELL_COSIGN_VERSION: ${target_cosign_version}"
replace_line_with_prefix "${RELEASE_WORKFLOW_PATH}" '  WORKCELL_BUILDKIT_IMAGE:' "  WORKCELL_BUILDKIT_IMAGE: ${target_buildkit_image}"
replace_line_with_prefix "${RELEASE_WORKFLOW_PATH}" '  WORKCELL_BUILDX_VERSION:' "  WORKCELL_BUILDX_VERSION: ${target_buildx_version}"
replace_line_with_prefix "${RELEASE_WORKFLOW_PATH}" '  WORKCELL_COSIGN_VERSION:' "  WORKCELL_COSIGN_VERSION: ${target_cosign_version}"
replace_line_with_prefix "${RELEASE_WORKFLOW_PATH}" '  WORKCELL_QEMU_IMAGE:' "  WORKCELL_QEMU_IMAGE: ${target_qemu_image}"
replace_line_with_prefix "${RELEASE_WORKFLOW_PATH}" '  WORKCELL_SYFT_VERSION:' "  WORKCELL_SYFT_VERSION: ${target_syft_version}"
replace_line_with_prefix "${RELEASE_WORKFLOW_PATH}" '          ACTIONLINT_SHA256:' "          ACTIONLINT_SHA256: ${target_actionlint_sha}"
replace_line_with_prefix "${RELEASE_WORKFLOW_PATH}" '          ACTIONLINT_VERSION:' "          ACTIONLINT_VERSION: ${target_actionlint_version}"
replace_line_with_prefix "${RELEASE_WORKFLOW_PATH}" '          ZIZMOR_SHA256:' "          ZIZMOR_SHA256: ${target_zizmor_sha}"
replace_line_with_prefix "${RELEASE_WORKFLOW_PATH}" '          ZIZMOR_VERSION:' "          ZIZMOR_VERSION: ${target_zizmor_version}"

replace_line_with_prefix "${SECURITY_WORKFLOW_PATH}" '          ACTIONLINT_SHA256:' "          ACTIONLINT_SHA256: ${target_actionlint_sha}"
replace_line_with_prefix "${SECURITY_WORKFLOW_PATH}" '          ACTIONLINT_VERSION:' "          ACTIONLINT_VERSION: ${target_actionlint_version}"
replace_all_lines_with_prefix "${SECURITY_WORKFLOW_PATH}" '          ZIZMOR_SHA256:' "          ZIZMOR_SHA256: ${target_zizmor_sha}"
replace_all_lines_with_prefix "${SECURITY_WORKFLOW_PATH}" '          ZIZMOR_VERSION:' "          ZIZMOR_VERSION: ${target_zizmor_version}"

"${ROOT_DIR}/scripts/update-provider-pins.sh" --apply
"${ROOT_DIR}/scripts/check-pinned-inputs.sh"

print_summary
