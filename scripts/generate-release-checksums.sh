#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

usage() {
  cat <<'EOF' >&2
Usage: generate-release-checksums.sh OUTPUT FILE...
EOF
  exit 2
}

[[ $# -ge 2 ]] || usage

output_path="$1"
shift

seen_basenames=()

for path in "$@"; do
  [[ -f "${path}" ]] || {
    echo "Missing release asset: ${path}" >&2
    exit 1
  }

  base_name="$(basename "${path}")"
  for seen_base in "${seen_basenames[@]}"; do
    if [[ "${seen_base}" == "${base_name}" ]]; then
      echo "Duplicate release asset basename: ${base_name}" >&2
      exit 1
    fi
  done
  seen_basenames+=("${base_name}")
done

tmp_output="$(mktemp "${TMPDIR:-/tmp}/workcell-sha256sums.XXXXXX")"

cleanup() {
  rm -f "${tmp_output}"
}

trap cleanup EXIT

printf '%s\n' "$@" | LC_ALL=C sort | while IFS= read -r path; do
  base_name="$(basename "${path}")"
  digest="$(sha256sum "${path}" | awk '{print $1}')"
  printf '%s  %s\n' "${digest}" "${base_name}"
done >"${tmp_output}"

mv "${tmp_output}" "${output_path}"
trap - EXIT
