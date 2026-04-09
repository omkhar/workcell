#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${HOME:-/tmp}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "verify-github-macos-release-test-runners-entrypoint-ok"
  exit 0
fi

usage() {
  cat <<'EOF' >&2
Usage: verify-github-macos-release-test-runners.sh EXPECTED_LATEST EXPECTED_PREVIOUS
EOF
  exit 2
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

trim() {
  local value="${1:-}"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s\n' "${value}"
}

[[ $# -eq 2 ]] || usage

EXPECTED_LATEST="$1"
EXPECTED_PREVIOUS="$2"
RUNNER_IMAGES_README_URL="https://raw.githubusercontent.com/actions/runner-images/main/README.md"
GITHUB_HOSTED_RUNNERS_DOCS_URL="https://docs.github.com/en/actions/reference/runners/github-hosted-runners"

require_tool curl
require_tool grep
require_tool sort

runner_images_readme="$(curl -fsSL "${RUNNER_IMAGES_README_URL}")"
docs_page="$(curl -fsSL "${GITHUB_HOSTED_RUNNERS_DOCS_URL}")"

runner_rows=""

while IFS='|' read -r _ image arch labels readme_label _; do
  local_image_name=""

  image="$(trim "${image}")"
  image="${image%%<br>*}"
  arch="$(trim "${arch}")"
  labels="$(trim "${labels}")"
  readme_label="$(trim "${readme_label}")"

  [[ "${arch}" == "arm64" ]] || continue
  [[ "${image}" =~ ^macOS[[:space:]]+([0-9]+)[[:space:]]+Arm64$ ]] || continue

  version="${BASH_REMATCH[1]}"
  explicit_label="$(grep -o "macos-${version}" <<<"${labels}" | head -n 1 || true)"
  [[ -n "${explicit_label}" ]] || {
    echo "Unable to find explicit macOS arm64 workflow label for version ${version} in runner-images README" >&2
    exit 1
  }
  [[ "${readme_label}" =~ \[([[:alnum:]-]+)\] ]] || {
    echo "Unable to find image identifier for macOS arm64 version ${version} in runner-images README" >&2
    exit 1
  }
  local_image_name="${BASH_REMATCH[1]}"

  runner_rows+="${version}|${explicit_label}|${local_image_name}"$'\n'
done < <(printf '%s\n' "${runner_images_readme}" | grep -E '^\| macOS [0-9]+ Arm64')

sorted_rows="$(printf '%s' "${runner_rows}" | sed '/^$/d' | sort -t '|' -k1,1nr)"
actual_latest_row="$(printf '%s\n' "${sorted_rows}" | sed -n '1p')"
actual_previous_row="$(printf '%s\n' "${sorted_rows}" | sed -n '2p')"
if [[ -z "${actual_latest_row}" || -z "${actual_previous_row}" ]]; then
  echo "Expected at least two GitHub-hosted macOS arm64 runner versions in runner-images README" >&2
  exit 1
fi

IFS='|' read -r _ actual_latest actual_latest_image <<<"${actual_latest_row}"
IFS='|' read -r _ actual_previous actual_previous_image <<<"${actual_previous_row}"

if [[ "${EXPECTED_LATEST}" != "${actual_latest}" ]] || [[ "${EXPECTED_PREVIOUS}" != "${actual_previous}" ]]; then
  echo "GitHub-hosted macOS arm64 release test runner labels are stale." >&2
  echo "Expected from workflow: ${EXPECTED_LATEST}, ${EXPECTED_PREVIOUS}" >&2
  echo "Authoritative runner-images labels: ${actual_latest}, ${actual_previous}" >&2
  echo "Authoritative runner-images images: ${actual_latest_image}, ${actual_previous_image}" >&2
  exit 1
fi

for required in "${EXPECTED_LATEST}" "${EXPECTED_PREVIOUS}"; do
  if ! grep -F "${required}" <<<"${docs_page}" >/dev/null; then
    echo "GitHub Docs does not advertise expected macOS runner label ${required}" >&2
    exit 1
  fi
done

printf 'GitHub macOS arm64 release test runners verified from authoritative sources: %s (%s), %s (%s)\n' \
  "${actual_latest}" "${actual_latest_image}" \
  "${actual_previous}" "${actual_previous_image}"
