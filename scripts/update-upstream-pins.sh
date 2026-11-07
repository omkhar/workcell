#!/bin/bash -p
workcell_upstream_pins_token_file_created=""
if [[ -n "${WORKCELL_GITHUB_API_TOKEN_FILE:-}" && "${WORKCELL_GITHUB_API_TOKEN_FILE}" != /* ]]; then
  workcell_upstream_pins_invocation_dir="$(pwd -P)" || {
    echo "Cannot resolve the updater invocation directory." >&2
    exit 1
  }
  export WORKCELL_GITHUB_API_TOKEN_FILE="${workcell_upstream_pins_invocation_dir}/${WORKCELL_GITHUB_API_TOKEN_FILE}"
  unset workcell_upstream_pins_invocation_dir
fi
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
DEBIAN_BOOTSTRAP_MANIFEST_PATH="${ROOT_DIR}/runtime/container/debian-bootstrap.env"
GO_MOD_PATH="${ROOT_DIR}/go.mod"
RUST_TOOLCHAIN_PATH="${ROOT_DIR}/runtime/container/rust/rust-toolchain.toml"
CARGO_MANIFEST_PATH="${ROOT_DIR}/runtime/container/rust/Cargo.toml"
VALIDATOR_IMAGE_SCRIPT_PATH="${ROOT_DIR}/scripts/ci/build-validator-image.sh"
CHECK_DEAD_CODE_SCRIPT_PATH="${ROOT_DIR}/scripts/check-dead-code.sh"
TOOL_PINS_POLICY_PATH="${ROOT_DIR}/policy/tool-pins.toml"

mode="summary"
toolchain_only=0
debian_snapshot_pin=""

cleanup() {
  [[ -z "${workcell_upstream_pins_token_file_created}" || "${workcell_upstream_pins_token_file_created}" != "${TMPDIR:-/tmp}"/workcell-github-token.* ]] || rm -f "${workcell_upstream_pins_token_file_created}"
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage: scripts/update-upstream-pins.sh [--apply | --check] [--toolchain-only] [--debian-snapshot STAMP]

Modes:
  --apply   Refresh pinned provider versions, Linux base images, toolchains, and
            release-build inputs to the newest reviewed upstream versions.
  --check   Exit non-zero when any eligible pinned upstream refresh is pending.

Options:
  --toolchain-only  Leave provider pins to scripts/update-provider-pins.sh.
                    Pending provider bumps do not count as changes.
  --debian-snapshot STAMP
                    Resolve only this Debian snapshot, for example
                    20261028T000000Z. The stamp must still fall inside the
                    lookback window. Use it to recreate an earlier candidate.

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
    --toolchain-only)
      toolchain_only=1
      shift
      ;;
    --debian-snapshot)
      debian_snapshot_pin="${2:-}"
      if [[ ! "${debian_snapshot_pin}" =~ ^[0-9]{8}T000000Z$ ]]; then
        echo "--debian-snapshot requires a YYYYMMDDT000000Z value." >&2
        exit 2
      fi
      shift 2
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

# Response-body download policy lives in workcell-citools. curl remains only
# for bodyless Debian Release probes, which need no curl size-cap capability.
# curl --retry retries only timeouts and HTTP 408, 429, 500, 502, 503 and 504.
CURL_HEAD_GUARDS=(--max-time 30 --connect-timeout 15 --retry 3 --retry-max-time 90)
DEBIAN_SNAPSHOT_LOOKBACK_DAYS="${WORKCELL_DEBIAN_SNAPSHOT_LOOKBACK_DAYS:-60}"
MAX_DEBIAN_SNAPSHOT_AGE_DAYS="${WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS:-60}"
# While snapshot.debian.org is down, keep the current snapshot pin only if it
# is at most this many days old. An older pin fails the refresh.
DEBIAN_SNAPSHOT_HOLD_MAX_AGE_DAYS="${WORKCELL_DEBIAN_SNAPSHOT_HOLD_MAX_AGE_DAYS:-7}"
# Internal status for an archive that did not answer (EX_TEMPFAIL).
readonly DEBIAN_ARCHIVE_UNAVAILABLE_STATUS=75

run_upstream_citools() {
  (
    cd "${ROOT_DIR}" || exit
    go run ./cmd/workcell-citools "$@"
  )
}

github_api_get() {
  run_upstream_citools github-api-get "$1"
}

upstream_get() {
  run_upstream_citools upstream-get "$@"
}

github_release_asset() {
  local release_json="$1"
  local repository="$2"
  local asset_name="$3"
  local asset_class="$4"

  (
    cd "${ROOT_DIR}" || exit
    printf '%s' "${release_json}" |
      go run ./cmd/workcell-citools github-release-asset "${repository}" "${asset_name}" "${asset_class}"
  )
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

docker_image_digest_if_available() {
  local image_ref="$1"
  local output=""
  local status=0
  local digest=""

  output="$(docker buildx imagetools inspect "${image_ref}" 2>&1)" || status=$?
  if [[ "${status}" -ne 0 ]]; then
    if grep -Eiq '(not found|manifest unknown)' <<<"${output}"; then
      return 1
    fi
    printf '%s\n' "${output}" >&2
    exit "${status}"
  fi
  digest="$(awk '/^Digest:/ {print $2; exit}' <<<"${output}")"
  if [[ -z "${digest}" ]]; then
    echo "Unable to resolve image digest for ${image_ref}" >&2
    exit 1
  fi
  printf '%s\n' "${digest}"
}

extract_dockerfile_arg() {
  (
    cd "${ROOT_DIR}" || exit
    go run ./cmd/workcell-citools extract-dockerfile-arg "$1" "$2"
  )
}

# extract_tool_pin prints one value from policy/tool-pins.toml, the single
# source of the CI and release tool pins. No workflow holds a copy, so a tool
# pin bump rewrites only that file.
extract_tool_pin() {
  local key="$1"
  awk -F'"' -v prefix="${key} = " '
    index($0, prefix) == 1 && NF == 3 {
      print $2
      found = 1
      exit
    }
    END {
      if (!found) {
        exit 3
      }
    }
  ' "${TOOL_PINS_POLICY_PATH}"
}

file_mode_octal() {
  local path="$1"

  if stat -f '%Lp' "${path}" >/dev/null 2>&1; then
    stat -f '%Lp' "${path}"
  else
    stat -c '%a' "${path}" # portability-exempt: the BSD stat form is tried first
  fi
}

# replace_tmp_over_file atomically replaces file with tmp while keeping the
# original file mode (mktemp files are 0600; a bare mv strips the executable
# bit from rewritten scripts and ships a spurious mode-only diff, while a
# write-through would leave the target truncated on an interrupted write).
replace_tmp_over_file() {
  local tmp="$1"
  local file="$2"

  chmod "$(file_mode_octal "${file}")" "${tmp}"
  mv "${tmp}" "${file}"
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
  replace_tmp_over_file "${tmp}" "${file}"
}

date_stamp_for_offset() {
  local offset="$1"
  if date -u -d '1970-01-01 UTC' +%Y%m%dT000000Z >/dev/null 2>&1; then
    if [[ "${offset}" == "0" ]]; then
      date -u +%Y%m%dT000000Z
    else
      date -u -d "-${offset} day" +%Y%m%dT000000Z # portability-exempt: the GNU date form is probed first
    fi
    return
  fi
  if [[ "${offset}" == "0" ]]; then
    date -u +%Y%m%dT000000Z
  else
    date -u -v-"${offset}"d +%Y%m%dT000000Z
  fi
}

resolve_debian_bootstrap_pins() {
  (
    cd "${ROOT_DIR}" || exit
    go run ./cmd/workcell-citools resolve-debian-bootstrap "$1"
  )
}

# debian_release_probe URL: status 0 when the Release file exists, 1 when the
# archive answers 404 or 410, DEBIAN_ARCHIVE_UNAVAILABLE_STATUS for a 429, a
# 5xx or a network failure after curl's retries, and 2 for any other result.
debian_release_probe() {
  local http_code=""
  local curl_status=0
  http_code="$(curl -q -sSI "${CURL_HEAD_GUARDS[@]}" -o /dev/null -w '%{http_code}' "$1")" || curl_status=$?
  case "${curl_status}" in
    0) ;;
    # Name or proxy lookup, connect, timeout, TLS handshake, empty reply,
    # send, receive and HTTP/2 or HTTP/3 stream failures. A TLS certificate
    # failure (60) or a curl usage error is not an outage.
    5 | 6 | 7 | 16 | 18 | 28 | 35 | 52 | 55 | 56 | 92 | 95)
      echo "snapshot.debian.org did not answer for $1 (curl exit ${curl_status})" >&2
      return "${DEBIAN_ARCHIVE_UNAVAILABLE_STATUS}"
      ;;
    *)
      echo "Debian Release probe failed for $1 (curl exit ${curl_status})" >&2
      return 2
      ;;
  esac
  case "${http_code}" in
    2?? | 3??) return 0 ;;
    404 | 410) return 1 ;;
    429 | 5??)
      echo "snapshot.debian.org answered HTTP ${http_code} for $1" >&2
      return "${DEBIAN_ARCHIVE_UNAVAILABLE_STATUS}"
      ;;
    *)
      echo "snapshot.debian.org answered unexpected HTTP ${http_code} for $1" >&2
      return 2
      ;;
  esac
}

# debian_snapshot_candidate STAMP: status 0 and the plan JSON on stdout when the
# stamp resolves, 1 when it is absent or unsuitable,
# DEBIAN_ARCHIVE_UNAVAILABLE_STATUS when the archive was down, and 2 when a
# Release probe failed for another reason.
debian_snapshot_candidate() {
  local stamp="$1"
  local release_url=""
  local status=0
  for release_url in \
    "https://snapshot.debian.org/archive/debian/${stamp}/dists/trixie/Release" \
    "https://snapshot.debian.org/archive/debian/${stamp}/dists/trixie-updates/Release" \
    "https://snapshot.debian.org/archive/debian-security/${stamp}/dists/trixie-security/Release"; do
    status=0
    debian_release_probe "${release_url}" || status=$?
    if [[ "${status}" -ne 0 ]]; then
      return "${status}"
    fi
  done
  # go run reports every failed exit as 1, so the resolver marks an outage
  # with an upstream-unavailable line on stderr.
  local error_path=""
  error_path="$(mktemp "${TMPDIR:-/tmp}/workcell-debian-resolve.XXXXXX")"
  status=0
  resolve_debian_bootstrap_pins "${stamp}" 2>"${error_path}" || status=$?
  cat "${error_path}" >&2
  if [[ "${status}" -ne 0 ]] && grep -q '^upstream-unavailable: ' "${error_path}"; then
    status="${DEBIAN_ARCHIVE_UNAVAILABLE_STATUS}"
  elif [[ "${status}" -ne 0 ]]; then
    status=1
  fi
  rm -f "${error_path}"
  return "${status}"
}

latest_debian_bootstrap_plan() {
  local stamp
  local offset
  local lookback_days="${DEBIAN_SNAPSHOT_LOOKBACK_DAYS:-60}"
  local max_age_days="${MAX_DEBIAN_SNAPSHOT_AGE_DAYS:-60}"
  local hold_max_age_days="${DEBIAN_SNAPSHOT_HOLD_MAX_AGE_DAYS:-7}"
  local resolution=""
  local resolution_error=""
  local resolution_error_path=""
  local status=0
  if [[ ! "${lookback_days}" =~ ^(0|[1-9][0-9]?)$ ]] || ((10#${lookback_days} > 60)); then
    echo "WORKCELL_DEBIAN_SNAPSHOT_LOOKBACK_DAYS must be an integer from 0 through 60" >&2
    return 2
  fi
  if [[ ! "${max_age_days}" =~ ^(0|[1-9][0-9]?)$ ]] || ((10#${max_age_days} > 60)); then
    echo "WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS must be an integer from 0 through 60" >&2
    return 2
  fi
  if [[ ! "${hold_max_age_days}" =~ ^(0|[1-9][0-9]?)$ ]] || ((10#${hold_max_age_days} > 60)); then
    echo "WORKCELL_DEBIAN_SNAPSHOT_HOLD_MAX_AGE_DAYS must be an integer from 0 through 60" >&2
    return 2
  fi
  # A hold never keeps a pin past the freshness bound.
  if ((10#${hold_max_age_days} > 10#${max_age_days})); then
    hold_max_age_days="${max_age_days}"
  fi
  if ((10#${lookback_days} > 10#${max_age_days})); then
    lookback_days="${max_age_days}"
  fi
  # Bootstrap packages intentionally come from trixie/main. A candidate is
  # eligible only when the same timestamp also exposes the updates and security
  # suites used by the subsequent apt install.
  resolution_error_path="$(mktemp "${TMPDIR:-/tmp}/workcell-debian-bootstrap.XXXXXX")"
  for offset in $(seq 0 "${lookback_days}"); do
    stamp="$(date_stamp_for_offset "${offset}")"
    # A pinned snapshot keeps the age bound but skips every other stamp.
    if [[ -n "${debian_snapshot_pin:-}" && "${stamp}" != "${debian_snapshot_pin}" ]]; then
      continue
    fi
    # Never walk back past the current pin: an older snapshot would roll back
    # Debian security updates. Keep the current pin.
    if [[ -z "${debian_snapshot_pin:-}" && "${stamp}" < "${current_debian_snapshot}" ]]; then
      rm -f "${resolution_error_path}"
      printf '{"keep_current":true}\n'
      return
    fi
    status=0
    resolution="$(debian_snapshot_candidate "${stamp}" 2>"${resolution_error_path}")" || status=$?
    if [[ "${status}" -eq 0 ]]; then
      rm -f "${resolution_error_path}"
      printf '%s\n' "${resolution}"
      return
    fi
    if [[ "${status}" -eq "${DEBIAN_ARCHIVE_UNAVAILABLE_STATUS}" ]]; then
      # An outage is not an unsuitable snapshot, so do not walk back. Keep a
      # fresh current pin and report the hold, so the other pins still refresh.
      resolution_error="$(<"${resolution_error_path}")"
      rm -f "${resolution_error_path}"
      printf '%s\n' "${resolution_error}" >&2
      if [[ -z "${debian_snapshot_pin:-}" && ! "${current_debian_snapshot}" < "$(date_stamp_for_offset "${hold_max_age_days}")" ]]; then
        printf '{"keep_current":true,"held":"snapshot.debian.org unavailable at %s"}\n' "${stamp}"
        return
      fi
      echo "snapshot.debian.org is unavailable and the Debian snapshot pin ${current_debian_snapshot} is older than ${hold_max_age_days} days or pinned; refusing to keep it" >&2
      exit 1
    fi
    if [[ "${status}" -ne 1 ]]; then
      cat "${resolution_error_path}" >&2
      rm -f "${resolution_error_path}"
      exit 1
    fi
  done
  resolution_error="$(<"${resolution_error_path}")"
  rm -f "${resolution_error_path}"
  if [[ -n "${debian_snapshot_pin:-}" ]]; then
    echo "Unable to resolve pinned Debian snapshot ${debian_snapshot_pin} within ${lookback_days} days" >&2
  fi
  echo "Unable to resolve a Debian snapshot within ${lookback_days} days for trixie/trixie-updates/trixie-security with HTTPS-fetched and byte-verified bootstrap packages" >&2
  if [[ -n "${resolution_error}" ]]; then
    printf '%s\n' "${resolution_error}" >&2
  fi
  exit 1
}

apply_debian_bootstrap_plan() {
  local plan="$1"
  local plan_path=""
  local status=0
  plan_path="$(mktemp "${TMPDIR:-/tmp}/workcell-debian-bootstrap-plan.XXXXXX")"
  printf '%s\n' "${plan}" >"${plan_path}"
  (
    cd "${ROOT_DIR}"
    go run ./cmd/workcell-citools apply-debian-bootstrap \
      "${plan_path}" \
      "${ROOT_DIR}"
  ) || status=$?
  rm -f "${plan_path}"
  return "${status}"
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
  upstream_get dockerhub-binfmt-tags |
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

latest_go_json="$(upstream_get go-releases | jq 'map(select(.stable == true)) | .[0]')"
target_go_toolchain="$(jq -r '.version | sub("^go"; "")' <<<"${latest_go_json}")"
target_go_language="$(semver_patch_zero "${target_go_toolchain}")"
target_go_sha_amd64="$(jq -r '.files[] | select(.os == "linux" and .arch == "amd64" and .kind == "archive") | .sha256' <<<"${latest_go_json}")"
target_go_sha_arm64="$(jq -r '.files[] | select(.os == "linux" and .arch == "arm64" and .kind == "archive") | .sha256' <<<"${latest_go_json}")"
x_tools_latest_json="$(upstream_get x-tools-latest)"
target_deadcode_version="$(jq -er '.Version' <<<"${x_tools_latest_json}")"
if [[ ! "${target_deadcode_version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "The x/tools release metadata has an invalid version: ${target_deadcode_version}" >&2
  exit 1
fi

rust_stable_toml="$(upstream_get rust-channel)"
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
rustup_stable_toml="$(upstream_get rustup-release)"
target_rustup_version="$(awk -F"'" '$1 == "version = " { print $2; exit }' <<<"${rustup_stable_toml}")"
target_rustup_sha_amd64="$(upstream_get rustup-checksum "${target_rustup_version}" x86_64-unknown-linux-gnu | awk '{print $1}')"
target_rustup_sha_arm64="$(upstream_get rustup-checksum "${target_rustup_version}" aarch64-unknown-linux-gnu | awk '{print $1}')"

hadolint_release_json="$(github_api_get 'https://api.github.com/repos/hadolint/hadolint/releases/latest')"
target_hadolint_version="$(jq -r '.tag_name' <<<"${hadolint_release_json}")"
hadolint_checksums="$(github_release_asset "${hadolint_release_json}" 'hadolint/hadolint' 'checksums.sha256' checksum)"
target_hadolint_sha_amd64="$(
  cd "${ROOT_DIR}" || exit
  printf '%s' "${hadolint_checksums}" | go run ./cmd/workcell-citools hadolint-manifest-checksum 'hadolint-linux-x86_64'
)"
target_hadolint_sha_arm64="$(
  cd "${ROOT_DIR}" || exit
  printf '%s' "${hadolint_checksums}" | go run ./cmd/workcell-citools hadolint-manifest-checksum 'hadolint-linux-arm64'
)"

buildx_release_json="$(github_api_get 'https://api.github.com/repos/docker/buildx/releases/latest')"
candidate_buildx_version="$(jq -r '.tag_name' <<<"${buildx_release_json}")"
buildx_install_catalog_response="$(github_api_get 'https://api.github.com/repos/docker/actions-toolkit/contents/.github/buildx-releases.json?ref=main')"

cosign_release_json="$(github_api_get 'https://api.github.com/repos/sigstore/cosign/releases/latest')"
target_cosign_version="$(jq -r '.tag_name' <<<"${cosign_release_json}")"

syft_release_json="$(github_api_get 'https://api.github.com/repos/anchore/syft/releases/latest')"
target_syft_version="$(jq -r '.tag_name' <<<"${syft_release_json}")"

actionlint_release_json="$(github_api_get 'https://api.github.com/repos/rhysd/actionlint/releases/latest')"
target_actionlint_version="$(jq -r '.tag_name | sub("^v"; "")' <<<"${actionlint_release_json}")"
target_actionlint_sha="$(
  github_release_asset "${actionlint_release_json}" 'rhysd/actionlint' "actionlint_${target_actionlint_version}_checksums.txt" checksum |
    awk '/actionlint_'"${target_actionlint_version}"'_linux_amd64\.tar\.gz$/ { print $1; exit }'
)"

zizmor_release_json="$(github_api_get 'https://api.github.com/repos/zizmorcore/zizmor/releases/latest')"
target_zizmor_version="$(jq -r '.tag_name | sub("^v"; "")' <<<"${zizmor_release_json}")"
target_zizmor_sha="$(
  github_release_asset "${zizmor_release_json}" 'zizmorcore/zizmor' 'zizmor-x86_64-unknown-linux-gnu.tar.gz' archive |
    shasum -a 256 |
    awk '{ print $1 }'
)"
current_runtime_base="$(extract_dockerfile_arg "${RUNTIME_DOCKERFILE_PATH}" NODE_BASE_IMAGE)"
current_validator_base="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" VALIDATOR_BASE_IMAGE)"
current_debian_bootstrap_manifest="$(
  cd "${ROOT_DIR}" || exit
  go run ./cmd/workcell-citools inspect-debian-bootstrap "${DEBIAN_BOOTSTRAP_MANIFEST_PATH}"
)"
current_debian_snapshot="$(jq -er '.snapshot' <<<"${current_debian_bootstrap_manifest}")"
current_debian_openssl_amd64_path="$(jq -er '.openssl_amd64_path' <<<"${current_debian_bootstrap_manifest}")"
current_debian_openssl_amd64_sha256="$(jq -er '.openssl_amd64_sha256' <<<"${current_debian_bootstrap_manifest}")"
current_debian_openssl_arm64_path="$(jq -er '.openssl_arm64_path' <<<"${current_debian_bootstrap_manifest}")"
current_debian_openssl_arm64_sha256="$(jq -er '.openssl_arm64_sha256' <<<"${current_debian_bootstrap_manifest}")"
current_debian_ca_path="$(jq -er '.ca_certificates_path' <<<"${current_debian_bootstrap_manifest}")"
current_debian_ca_sha256="$(jq -er '.ca_certificates_sha256' <<<"${current_debian_bootstrap_manifest}")"
current_go_toolchain="$(awk '/^toolchain / { sub(/^toolchain go/, "", $0); print; exit }' "${GO_MOD_PATH}")"
current_go_language="$(awk '/^go / { print $2; exit }' "${GO_MOD_PATH}")"
current_validator_go_version="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" GO_VERSION)"
current_runtime_go_version="$(extract_dockerfile_arg "${RUNTIME_DOCKERFILE_PATH}" GO_VERSION)"
current_runtime_go_sha_amd64="$(extract_dockerfile_arg "${RUNTIME_DOCKERFILE_PATH}" GO_LINUX_X86_64_SHA256)"
current_runtime_go_sha_arm64="$(extract_dockerfile_arg "${RUNTIME_DOCKERFILE_PATH}" GO_LINUX_ARM64_SHA256)"
current_go_sha_amd64="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" GO_LINUX_X86_64_SHA256)"
current_go_sha_arm64="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" GO_LINUX_ARM64_SHA256)"
current_deadcode_version="$(extract_dockerfile_arg "${VALIDATOR_DOCKERFILE_PATH}" DEADCODE_VERSION)"
current_deadcode_script_version="$(awk -F'"' '/^readonly DEADCODE_VERSION="v[0-9]+\.[0-9]+\.[0-9]+"$/ { print $2; exit }' "${CHECK_DEAD_CODE_SCRIPT_PATH}")"
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
current_buildkit_image="$(extract_tool_pin buildkit)"
current_buildx_version="$(extract_tool_pin buildx)"
target_buildx_version="$({
  cd "${ROOT_DIR}" || exit
  printf '%s' "${buildx_install_catalog_response}" |
    go run ./cmd/workcell-citools select-buildx-version "${current_buildx_version}" "${candidate_buildx_version}"
})"
unavailable_buildx_version=""
if [[ "${target_buildx_version}" != "${candidate_buildx_version}" ]]; then
  unavailable_buildx_version="${candidate_buildx_version}"
fi
current_validator_image_buildkit_fallback="$(awk -v prefix="BUILDKIT_IMAGE=\"\${WORKCELL_BUILDKIT_IMAGE:-" '
  index($0, prefix) == 1 {
    value = substr($0, length(prefix) + 1)
    sub(/}"$/, "", value)
    print value
    exit
  }' "${VALIDATOR_IMAGE_SCRIPT_PATH}")"
current_cosign_version="$(extract_tool_pin cosign)"
current_qemu_image="$(extract_tool_pin qemu)"
current_syft_version="$(extract_tool_pin syft)"
current_actionlint_version="$(extract_tool_pin actionlint_version)"
current_actionlint_sha="$(extract_tool_pin actionlint_sha256)"
current_zizmor_version="$(extract_tool_pin zizmor_version)"
current_zizmor_sha="$(extract_tool_pin zizmor_sha256)"

runtime_base_track="${current_runtime_base%@*}"
validator_base_track="${current_validator_base%@*}"
buildkit_track="${current_buildkit_image%@*}"
unavailable_rust_toolchain_image=""

target_runtime_base="${runtime_base_track}@$(docker_image_digest "${runtime_base_track}")"
target_validator_base="${validator_base_track}@$(docker_image_digest "${validator_base_track}")"
target_runtime_rust_toolchain_track="rust:${target_rust_version}-slim-trixie"
if target_runtime_rust_toolchain_digest="$(docker_image_digest_if_available "${target_runtime_rust_toolchain_track}")"; then
  target_runtime_rust_toolchain_image="${target_runtime_rust_toolchain_track}@${target_runtime_rust_toolchain_digest}"
else
  unavailable_rust_toolchain_image="${target_runtime_rust_toolchain_track}"
  target_rust_version="${current_rust_version}"
  target_cargo_rust_version="${current_cargo_rust_version}"
  target_runtime_rust_toolchain_image="${current_runtime_rust_toolchain_image}"
fi
# --apply records its Debian plan, and a --check in the same job reuses it.
# Thus snapshot.debian.org can recover or fail between the two runs, and the
# check still compares the tree with the plan that --apply wrote.
debian_plan_record="${WORKCELL_DEBIAN_PLAN_RECORD:-}"
if [[ -n "${debian_plan_record}" && "${debian_plan_record}" != /* ]]; then
  echo "WORKCELL_DEBIAN_PLAN_RECORD must be an absolute path" >&2
  exit 2
fi
if [[ "${mode}" == "check" && -n "${debian_plan_record}" ]]; then
  if [[ -L "${debian_plan_record}" || ! -f "${debian_plan_record}" ]] || (($(wc -c <"${debian_plan_record}") > 65536)); then
    echo "WORKCELL_DEBIAN_PLAN_RECORD ${debian_plan_record} is not a plan that --apply recorded" >&2
    exit 2
  fi
  target_debian_bootstrap_plan="$(<"${debian_plan_record}")"
  if ! jq -e '(type == "object") and ((.keep_current == true) or ((.snapshot | type == "string") and (.snapshot | test("^[0-9]{8}T000000Z$"))))' <<<"${target_debian_bootstrap_plan}" >/dev/null 2>&1; then
    echo "WORKCELL_DEBIAN_PLAN_RECORD ${debian_plan_record} does not hold a Debian bootstrap plan" >&2
    exit 2
  fi
else
  # Remove an older record first, so a failed plan cannot leave one for --check.
  if [[ "${mode}" == "apply" && -n "${debian_plan_record}" ]]; then
    rm -f "${debian_plan_record}"
  fi
  target_debian_bootstrap_plan="$(latest_debian_bootstrap_plan)"
  if [[ "${mode}" == "apply" && -n "${debian_plan_record}" ]]; then
    (umask 077 && printf '%s\n' "${target_debian_bootstrap_plan}" >"${debian_plan_record}")
  fi
fi
held_debian_snapshot=""
if jq -e '.keep_current == true' <<<"${target_debian_bootstrap_plan}" >/dev/null; then
  held_debian_snapshot="$(jq -r '.held // ""' <<<"${target_debian_bootstrap_plan}")"
  target_debian_snapshot="${current_debian_snapshot}"
  target_debian_openssl_amd64_path="${current_debian_openssl_amd64_path}"
  target_debian_openssl_amd64_sha256="${current_debian_openssl_amd64_sha256}"
  target_debian_openssl_arm64_path="${current_debian_openssl_arm64_path}"
  target_debian_openssl_arm64_sha256="${current_debian_openssl_arm64_sha256}"
  target_debian_ca_path="${current_debian_ca_path}"
  target_debian_ca_sha256="${current_debian_ca_sha256}"
else
  target_debian_snapshot="$(jq -er '.snapshot' <<<"${target_debian_bootstrap_plan}")"
  target_debian_openssl_amd64_path="$(jq -er '.openssl_amd64.filename' <<<"${target_debian_bootstrap_plan}")"
  target_debian_openssl_amd64_sha256="$(jq -er '.openssl_amd64.sha256' <<<"${target_debian_bootstrap_plan}")"
  target_debian_openssl_arm64_path="$(jq -er '.openssl_arm64.filename' <<<"${target_debian_bootstrap_plan}")"
  target_debian_openssl_arm64_sha256="$(jq -er '.openssl_arm64.sha256' <<<"${target_debian_bootstrap_plan}")"
  target_debian_ca_path="$(jq -er '.ca_certificates.filename' <<<"${target_debian_bootstrap_plan}")"
  target_debian_ca_sha256="$(jq -er '.ca_certificates.sha256' <<<"${target_debian_bootstrap_plan}")"
fi
target_buildkit_image="${buildkit_track}@$(docker_image_digest "${buildkit_track}")"
target_qemu_tag="$(latest_qemu_tag)"
target_qemu_image="tonistiigi/binfmt:${target_qemu_tag}@$(docker_image_digest "tonistiigi/binfmt:${target_qemu_tag}")"

provider_summary="$("${ROOT_DIR}/scripts/update-provider-pins.sh")"
provider_plan_status=0
provider_plan_json="$("${ROOT_DIR}/scripts/update-provider-pins.sh" --json)" || provider_plan_status=$?
if [[ "${provider_plan_status}" -ne 0 ]]; then
  echo "Unable to compute provider bump plan." >&2
  exit "${provider_plan_status}"
fi
if ! provider_has_changes="$(jq -ser '
  if length == 1 and (.[0] | type) == "object" and (.[0].has_changes | type) == "boolean" then
    if .[0].has_changes then 1 else 0 end
  else
    error("provider bump plan must set has_changes to a boolean")
  end
' <<<"${provider_plan_json}")"; then
  echo "Unable to read provider changes from the provider bump plan." >&2
  exit 1
fi

debian_has_changes=0
for current_target_pair in \
  "${current_debian_snapshot}|${target_debian_snapshot}" \
  "${current_debian_openssl_amd64_path}|${target_debian_openssl_amd64_path}" \
  "${current_debian_openssl_amd64_sha256}|${target_debian_openssl_amd64_sha256}" \
  "${current_debian_openssl_arm64_path}|${target_debian_openssl_arm64_path}" \
  "${current_debian_openssl_arm64_sha256}|${target_debian_openssl_arm64_sha256}" \
  "${current_debian_ca_path}|${target_debian_ca_path}" \
  "${current_debian_ca_sha256}|${target_debian_ca_sha256}"; do
  if [[ "${current_target_pair%%|*}" != "${current_target_pair#*|}" ]]; then
    debian_has_changes=1
    break
  fi
done

has_changes=0
for current_target_pair in \
  "${current_runtime_base}|${target_runtime_base}" \
  "${current_validator_base}|${target_validator_base}" \
  "${current_debian_snapshot}|${target_debian_snapshot}" \
  "${current_debian_openssl_amd64_path}|${target_debian_openssl_amd64_path}" \
  "${current_debian_openssl_amd64_sha256}|${target_debian_openssl_amd64_sha256}" \
  "${current_debian_openssl_arm64_path}|${target_debian_openssl_arm64_path}" \
  "${current_debian_openssl_arm64_sha256}|${target_debian_openssl_arm64_sha256}" \
  "${current_debian_ca_path}|${target_debian_ca_path}" \
  "${current_debian_ca_sha256}|${target_debian_ca_sha256}" \
  "${current_go_toolchain}|${target_go_toolchain}" \
  "${current_go_language}|${target_go_language}" \
  "${current_validator_go_version}|${target_go_toolchain}" \
  "${current_runtime_go_version}|${target_go_toolchain}" \
  "${current_runtime_go_sha_amd64}|${target_go_sha_amd64}" \
  "${current_runtime_go_sha_arm64}|${target_go_sha_arm64}" \
  "${current_go_sha_amd64}|${target_go_sha_amd64}" \
  "${current_go_sha_arm64}|${target_go_sha_arm64}" \
  "${current_deadcode_version}|${target_deadcode_version}" \
  "${current_deadcode_script_version}|${target_deadcode_version}" \
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
  "${current_validator_image_buildkit_fallback}|${target_buildkit_image}" \
  "${current_cosign_version}|${target_cosign_version}" \
  "${current_qemu_image}|${target_qemu_image}" \
  "${current_syft_version}|${target_syft_version}" \
  "${current_actionlint_version}|${target_actionlint_version}" \
  "${current_actionlint_sha}|${target_actionlint_sha}" \
  "${current_zizmor_version}|${target_zizmor_version}" \
  "${current_zizmor_sha}|${target_zizmor_sha}"; do
  current_value="${current_target_pair%%|*}"
  target_value="${current_target_pair#*|}"
  if [[ "${current_value}" != "${target_value}" ]]; then
    has_changes=1
    break
  fi
done
if [[ "${provider_has_changes}" -eq 1 && "${toolchain_only}" -eq 0 ]]; then
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
  print_summary_line "debian-snapshot" "${current_debian_snapshot}" "${target_debian_snapshot}"
  if [[ -n "${held_debian_snapshot}" ]]; then
    printf '  debian-snapshot-candidate: upstream-unavailable %s (held, current pin unchanged)\n' "${held_debian_snapshot}"
    echo "::warning::Debian snapshot refresh held: ${held_debian_snapshot}. The current pin stays."
  fi
  print_summary_line "debian-openssl-amd64" "${current_debian_openssl_amd64_path}" "${target_debian_openssl_amd64_path}"
  print_summary_line "debian-openssl-amd64-sha256" "${current_debian_openssl_amd64_sha256}" "${target_debian_openssl_amd64_sha256}"
  print_summary_line "debian-openssl-arm64" "${current_debian_openssl_arm64_path}" "${target_debian_openssl_arm64_path}"
  print_summary_line "debian-openssl-arm64-sha256" "${current_debian_openssl_arm64_sha256}" "${target_debian_openssl_arm64_sha256}"
  print_summary_line "debian-ca-certificates" "${current_debian_ca_path}" "${target_debian_ca_path}"
  print_summary_line "debian-ca-certificates-sha256" "${current_debian_ca_sha256}" "${target_debian_ca_sha256}"
  print_summary_line "go-toolchain" "${current_go_toolchain}" "${target_go_toolchain}"
  print_summary_line "go-language" "${current_go_language}" "${target_go_language}"
  print_summary_line "runtime-go" "${current_runtime_go_version}" "${target_go_toolchain}"
  print_summary_line "runtime-go-amd64-sha256" "${current_runtime_go_sha_amd64}" "${target_go_sha_amd64}"
  print_summary_line "runtime-go-arm64-sha256" "${current_runtime_go_sha_arm64}" "${target_go_sha_arm64}"
  print_summary_line "deadcode-validator" "${current_deadcode_version}" "${target_deadcode_version}"
  print_summary_line "deadcode-script" "${current_deadcode_script_version}" "${target_deadcode_version}"
  print_summary_line "rust-toolchain" "${current_rust_version}" "${target_rust_version}"
  print_summary_line "runtime-rust-image" "${current_runtime_rust_toolchain_image}" "${target_runtime_rust_toolchain_image}"
  if [[ -n "${unavailable_rust_toolchain_image}" ]]; then
    printf '  rust-toolchain-candidate: %s (waiting for official Docker image)\n' "${unavailable_rust_toolchain_image}"
  fi
  print_summary_line "rustup" "${current_rustup_version}" "${target_rustup_version}"
  print_summary_line "hadolint" "${current_hadolint_version}" "${target_hadolint_version}"
  print_summary_line "buildkit-image" "${current_buildkit_image}" "${target_buildkit_image}"
  print_summary_line "buildx-version" "${current_buildx_version}" "${target_buildx_version}"
  if [[ -n "${unavailable_buildx_version}" ]]; then
    printf '  buildx-candidate: %s (waiting for setup-buildx-action install catalog)\n' "${unavailable_buildx_version}"
  fi
  print_summary_line "cosign-version" "${current_cosign_version}" "${target_cosign_version}"
  print_summary_line "qemu-image" "${current_qemu_image}" "${target_qemu_image}"
  print_summary_line "syft-version" "${current_syft_version}" "${target_syft_version}"
  print_summary_line "actionlint-version" "${current_actionlint_version}" "${target_actionlint_version}"
  print_summary_line "zizmor-version" "${current_zizmor_version}" "${target_zizmor_version}"
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
  # A held release needs operator classification; do not hide it in a quiet run.
  held_lines="$(grep 'unclassified-cli-surface' <<<"${provider_summary}" || [[ $? -eq 1 ]])"
  if [[ -n "${held_lines}" ]]; then
    printf '%s\n' "${held_lines}"
    echo "::warning::Held Codex release needs command namespace classification."
  fi
  if [[ -n "${held_debian_snapshot}" ]]; then
    printf '  debian-snapshot-candidate: upstream-unavailable %s (held, current pin unchanged)\n' "${held_debian_snapshot}"
  fi
  exit 0
fi

if [[ "${debian_has_changes}" -eq 1 ]]; then
  apply_debian_bootstrap_plan "${target_debian_bootstrap_plan}"
fi

replace_line_with_prefix "${RUNTIME_DOCKERFILE_PATH}" 'ARG NODE_BASE_IMAGE=' "ARG NODE_BASE_IMAGE=${target_runtime_base}"
replace_line_with_prefix "${RUNTIME_DOCKERFILE_PATH}" 'ARG GO_VERSION=' "ARG GO_VERSION=${target_go_toolchain}"
replace_line_with_prefix "${RUNTIME_DOCKERFILE_PATH}" 'ARG GO_LINUX_X86_64_SHA256=' "ARG GO_LINUX_X86_64_SHA256=${target_go_sha_amd64}"
replace_line_with_prefix "${RUNTIME_DOCKERFILE_PATH}" 'ARG GO_LINUX_ARM64_SHA256=' "ARG GO_LINUX_ARM64_SHA256=${target_go_sha_arm64}"
replace_line_with_prefix "${RUNTIME_DOCKERFILE_PATH}" 'ARG RUST_VERSION=' "ARG RUST_VERSION=${target_rust_version}"
replace_line_with_prefix "${RUNTIME_DOCKERFILE_PATH}" 'ARG RUST_TOOLCHAIN_IMAGE=' "ARG RUST_TOOLCHAIN_IMAGE=${target_runtime_rust_toolchain_image}"

dockerfile_path="${VALIDATOR_DOCKERFILE_PATH}"
target_base="${target_validator_base}"
replace_line_with_prefix "${dockerfile_path}" 'ARG VALIDATOR_BASE_IMAGE=' "ARG VALIDATOR_BASE_IMAGE=${target_base}"
replace_line_with_prefix "${dockerfile_path}" 'ARG GO_VERSION=' "ARG GO_VERSION=${target_go_toolchain}"
replace_line_with_prefix "${dockerfile_path}" 'ARG GO_LINUX_X86_64_SHA256=' "ARG GO_LINUX_X86_64_SHA256=${target_go_sha_amd64}"
replace_line_with_prefix "${dockerfile_path}" 'ARG GO_LINUX_ARM64_SHA256=' "ARG GO_LINUX_ARM64_SHA256=${target_go_sha_arm64}"
replace_line_with_prefix "${dockerfile_path}" 'ARG DEADCODE_VERSION=' "ARG DEADCODE_VERSION=${target_deadcode_version}"
replace_line_with_prefix "${dockerfile_path}" 'ARG HADOLINT_VERSION=' "ARG HADOLINT_VERSION=${target_hadolint_version}"
replace_line_with_prefix "${dockerfile_path}" 'ARG HADOLINT_LINUX_X86_64_SHA256=' "ARG HADOLINT_LINUX_X86_64_SHA256=${target_hadolint_sha_amd64}"
replace_line_with_prefix "${dockerfile_path}" 'ARG HADOLINT_LINUX_ARM64_SHA256=' "ARG HADOLINT_LINUX_ARM64_SHA256=${target_hadolint_sha_arm64}"
replace_line_with_prefix "${dockerfile_path}" 'ARG RUST_VERSION=' "ARG RUST_VERSION=${target_rust_version}"
replace_line_with_prefix "${dockerfile_path}" 'ARG RUSTUP_VERSION=' "ARG RUSTUP_VERSION=${target_rustup_version}"
replace_line_with_prefix "${dockerfile_path}" 'ARG RUSTUP_INIT_LINUX_X86_64_SHA256=' "ARG RUSTUP_INIT_LINUX_X86_64_SHA256=${target_rustup_sha_amd64}"
replace_line_with_prefix "${dockerfile_path}" 'ARG RUSTUP_INIT_LINUX_ARM64_SHA256=' "ARG RUSTUP_INIT_LINUX_ARM64_SHA256=${target_rustup_sha_arm64}"

replace_line_with_prefix "${GO_MOD_PATH}" 'go ' "go ${target_go_language}"
replace_line_with_prefix "${GO_MOD_PATH}" 'toolchain go' "toolchain go${target_go_toolchain}"
replace_line_with_prefix "${CHECK_DEAD_CODE_SCRIPT_PATH}" 'readonly DEADCODE_VERSION=' "readonly DEADCODE_VERSION=\"${target_deadcode_version}\""
replace_line_with_prefix "${RUST_TOOLCHAIN_PATH}" 'channel = ' "channel = \"${target_rust_version}\""
replace_line_with_prefix "${CARGO_MANIFEST_PATH}" 'rust-version = ' "rust-version = \"${target_cargo_rust_version}\""

replace_line_with_prefix "${VALIDATOR_IMAGE_SCRIPT_PATH}" "BUILDKIT_IMAGE=\"\${WORKCELL_BUILDKIT_IMAGE:-" "BUILDKIT_IMAGE=\"\${WORKCELL_BUILDKIT_IMAGE:-${target_buildkit_image}}\""

# policy/tool-pins.toml is the single source of the CI and release tool pins:
# the workflows read it at run time, so a tool pin bump changes no workflow
# file. check-pinned-inputs (below) validates the new values.
replace_line_with_prefix "${TOOL_PINS_POLICY_PATH}" 'cosign = ' "cosign = \"${target_cosign_version}\""
replace_line_with_prefix "${TOOL_PINS_POLICY_PATH}" 'buildx = ' "buildx = \"${target_buildx_version}\""
replace_line_with_prefix "${TOOL_PINS_POLICY_PATH}" 'buildkit = ' "buildkit = \"${target_buildkit_image}\""
replace_line_with_prefix "${TOOL_PINS_POLICY_PATH}" 'qemu = ' "qemu = \"${target_qemu_image}\""
replace_line_with_prefix "${TOOL_PINS_POLICY_PATH}" 'syft = ' "syft = \"${target_syft_version}\""
replace_line_with_prefix "${TOOL_PINS_POLICY_PATH}" 'actionlint_version = ' "actionlint_version = \"${target_actionlint_version}\""
replace_line_with_prefix "${TOOL_PINS_POLICY_PATH}" 'actionlint_sha256 = ' "actionlint_sha256 = \"${target_actionlint_sha}\""
replace_line_with_prefix "${TOOL_PINS_POLICY_PATH}" 'zizmor_version = ' "zizmor_version = \"${target_zizmor_version}\""
replace_line_with_prefix "${TOOL_PINS_POLICY_PATH}" 'zizmor_sha256 = ' "zizmor_sha256 = \"${target_zizmor_sha}\""

if [[ "${toolchain_only}" -eq 0 ]]; then
  "${ROOT_DIR}/scripts/update-provider-pins.sh" --apply
fi
"${ROOT_DIR}/scripts/check-pinned-inputs.sh"

print_summary
