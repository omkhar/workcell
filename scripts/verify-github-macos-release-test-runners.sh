#!/bin/bash -p
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/verify-github-macos-release-test-runners.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "${workcell_entry_root}/scripts/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "${workcell_entry_root}/scripts/verify-github-macos-release-test-runners.sh" >/dev/null
  echo "verify-github-macos-release-test-runners-entrypoint-ok"
  exit 0
fi

usage() {
  cat <<'EOF' >&2
Usage: verify-github-macos-release-test-runners.sh EXPECTED_LATEST EXPECTED_PREVIOUS
EOF
  exit 2
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

runner_images_readme="$(curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --connect-timeout 20 \
  "${RUNNER_IMAGES_README_URL}")"
docs_page="$(curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --connect-timeout 20 \
  "${GITHUB_HOSTED_RUNNERS_DOCS_URL}")"

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
