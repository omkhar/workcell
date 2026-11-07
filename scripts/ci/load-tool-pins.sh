#!/usr/bin/env -S BASH_ENV= ENV= bash
# Load the reviewed CI and release tool pins from policy/tool-pins.toml.
#
# Usage: load-tool-pins.sh [POLICY_PATH]
#
# The file is the single source of the tool pins that the workflows use. A
# workflow job runs this script as its `pins` step and binds each value with
# `${{ steps.pins.outputs.<key> }}`, so a pin bump changes only the policy file.
#
# The parse fails closed. The file may hold only blank lines, `#` comment lines,
# one `[tool_pins]` header and `key = "value"` lines. Each key must come from the
# fixed table below, exactly once, and its value must match the key's format.
# Any other line, key or value stops the script before it writes an output, so
# a consuming step never runs with a partial or unchecked pin set.
# check-pinned-inputs runs this script and requires its output to equal the Go
# parse of the same file.
set -euo pipefail
export LC_ALL=C

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
POLICY_PATH="${1:-${ROOT_DIR}/policy/tool-pins.toml}"

if [[ $# -gt 1 ]]; then
  echo "Usage: $0 [POLICY_PATH]" >&2
  exit 2
fi

die() {
  echo "load-tool-pins: ${POLICY_PATH}: $*" >&2
  exit 1
}

[[ -n "${GITHUB_OUTPUT:-}" ]] || die "GITHUB_OUTPUT must name the step output file"
[[ -f "${POLICY_PATH}" && ! -L "${POLICY_PATH}" ]] || die "must be a regular file"

# The fixed key table, in output order, and each key's value format.
readonly PIN_KEYS=(
  cosign
  buildx
  buildkit
  qemu
  syft
  actionlint_version
  actionlint_sha256
  zizmor_version
  zizmor_sha256
  oras_version
  oras_linux_amd64_sha256
)
# These are the formats internal/metadatautil/pinnedinputs_toolpins.go checks,
# so the loader and the Go parse accept the same values. A release tag keeps
# its leading v; the actionlint, zizmor and ORAS versions have none. An image
# names its one reviewed repository with no registry host or other prefix.
# Version parts have no leading zero, so one version has one spelling.
readonly RELEASE_TAG_RE='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
readonly BARE_VERSION_RE='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
readonly SHA256_RE='^[0-9a-f]{64}$'
readonly IMAGE_TAG_DIGEST_RE=':[A-Za-z0-9_][A-Za-z0-9_.-]*@sha256:[0-9a-f]{64}$'
readonly BUILDKIT_RE="^moby/buildkit${IMAGE_TAG_DIGEST_RE}"
readonly QEMU_RE="^tonistiigi/binfmt${IMAGE_TAG_DIGEST_RE}"
readonly HEADER_RE='^\[tool_pins\]$'
readonly COMMENT_RE='^#'
readonly BLANK_RE='^ *$'
readonly ENTRY_RE='^([a-z0-9_]+) = "([^"]*)"$'

key_format() {
  case "$1" in
    buildkit) printf '%s\n' "${BUILDKIT_RE}" ;;
    qemu) printf '%s\n' "${QEMU_RE}" ;;
    cosign | buildx | syft) printf '%s\n' "${RELEASE_TAG_RE}" ;;
    actionlint_version | zizmor_version | oras_version) printf '%s\n' "${BARE_VERSION_RE}" ;;
    actionlint_sha256 | zizmor_sha256 | oras_linux_amd64_sha256) printf '%s\n' "${SHA256_RE}" ;;
    *) return 1 ;;
  esac
}

key_index() {
  local index=0
  local key
  for key in "${PIN_KEYS[@]}"; do
    if [[ "${key}" == "$1" ]]; then
      printf '%s\n' "${index}"
      return 0
    fi
    index=$((index + 1))
  done
  return 1
}

values=()
seen_header=0
line_number=0
bytes_read=0
while :; do
  if IFS= read -r line; then
    newline=1
  elif [[ -n "${line}" ]]; then
    newline=0
  else
    break
  fi
  line_number=$((line_number + 1))
  # Count each line and its newline. A NUL byte, which read drops, leaves the
  # total short of the file size and fails the size check after the loop.
  bytes_read=$((bytes_read + ${#line} + newline))
  if [[ "${line}" =~ ${BLANK_RE} || "${line}" =~ ${COMMENT_RE} ]]; then
    continue
  fi
  if [[ "${line}" =~ ${HEADER_RE} ]]; then
    [[ "${seen_header}" -eq 0 ]] || die "line ${line_number}: duplicate [tool_pins] header"
    seen_header=1
    continue
  fi
  if [[ ! "${line}" =~ ${ENTRY_RE} ]]; then
    die "line ${line_number}: expected a blank line, a comment, [tool_pins] or key = \"value\""
  fi
  key="${BASH_REMATCH[1]}"
  value="${BASH_REMATCH[2]}"
  [[ "${seen_header}" -eq 1 ]] || die "line ${line_number}: ${key} appears before the [tool_pins] header"
  index="$(key_index "${key}")" || die "line ${line_number}: unknown key ${key}"
  [[ -z "${values[index]+set}" ]] || die "line ${line_number}: duplicate key ${key}"
  format="$(key_format "${key}")" || die "line ${line_number}: no format for key ${key}"
  [[ "${value}" =~ ${format} ]] || die "line ${line_number}: ${key} value does not match ${format}"
  values[index]="${value}"
  [[ "${newline}" -eq 1 ]] || break
done <"${POLICY_PATH}"

file_bytes="$(wc -c <"${POLICY_PATH}")"
file_bytes="${file_bytes//[!0-9]/}"
[[ "${bytes_read}" -eq "${file_bytes}" ]] || die "holds bytes that are not part of a line (such as NUL)"
[[ "${seen_header}" -eq 1 ]] || die "must define the [tool_pins] header"

output=""
index=0
for key in "${PIN_KEYS[@]}"; do
  [[ -n "${values[index]+set}" ]] || die "missing key ${key}"
  output+="${key}=${values[index]}"$'\n'
  index=$((index + 1))
done

printf '%s' "${output}" >>"${GITHUB_OUTPUT}"
