#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/verify-coverage.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
RUST_MIN_COVERAGE=90
GO_CITOOLS_MIN_COVERAGE=56
REQUIRE_SUPPORTED_COVERAGE="${WORKCELL_REQUIRE_SUPPORTED_COVERAGE:-0}"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

resolve_llvm_tool() {
  local tool_name="$1"

  if command -v "${tool_name}" >/dev/null 2>&1; then
    command -v "${tool_name}"
    return 0
  fi

  if command -v xcrun >/dev/null 2>&1; then
    xcrun --find "${tool_name}"
    return 0
  fi

  return 1
}

require_tool cargo
require_tool go
coverage_tools_available() {
  LLVM_COV_BIN="$(resolve_llvm_tool llvm-cov)" || return 1
  LLVM_PROFDATA_BIN="$(resolve_llvm_tool llvm-profdata)" || return 1
}

if ! coverage_tools_available; then
  if [[ "${REQUIRE_SUPPORTED_COVERAGE}" == "1" ]]; then
    echo "Supported coverage tools are required but not fully available." >&2
    exit 1
  fi
  echo "Skipping supported coverage verification because LLVM coverage tools are not available locally." >&2
  exit 0
fi

TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-coverage.XXXXXX")"
cleanup() {
  rm -rf "${TMP_ROOT}"
}
trap cleanup EXIT

run_rust_launcher_coverage() {
  local rust_dir="${ROOT_DIR}/runtime/container/rust"
  local target_dir="${TMP_ROOT}/rust-target"
  local message_file="${TMP_ROOT}/rust-messages.json"
  local profdata_file="${TMP_ROOT}/rust.profdata"
  local export_file="${TMP_ROOT}/rust-coverage.json"

  (
    cd "${rust_dir}"
    CARGO_INCREMENTAL=0 \
      CARGO_TARGET_DIR="${target_dir}" \
      RUSTFLAGS="-C instrument-coverage" \
      LLVM_PROFILE_FILE="${TMP_ROOT}/workcell-%p-%m.profraw" \
      cargo test --locked --offline --bins --no-run --message-format=json \
      >"${message_file}"

    CARGO_INCREMENTAL=0 \
      CARGO_TARGET_DIR="${target_dir}" \
      RUSTFLAGS="-C instrument-coverage" \
      LLVM_PROFILE_FILE="${TMP_ROOT}/workcell-%p-%m.profraw" \
      cargo test --locked --offline --bins
  )

  (cd "${ROOT_DIR}" && go run ./cmd/workcell-citools coverage-executables "${message_file}") >"${TMP_ROOT}/rust-binaries.txt"

  rust_binaries=()
  while IFS= read -r line; do
    rust_binaries+=("${line}")
  done <"${TMP_ROOT}/rust-binaries.txt"
  if [[ "${#rust_binaries[@]}" -eq 0 ]]; then
    echo "Unable to locate instrumented Rust test executables for coverage." >&2
    exit 1
  fi

  "${LLVM_PROFDATA_BIN}" merge -sparse "${TMP_ROOT}"/workcell-*.profraw -o "${profdata_file}"
  "${LLVM_COV_BIN}" export \
    --summary-only \
    --instr-profile="${profdata_file}" \
    --ignore-filename-regex='(/.cargo/registry|/rustc/|/src/lib.rs$)' \
    "${rust_binaries[@]}" >"${export_file}"

  (cd "${ROOT_DIR}" && go run ./cmd/workcell-citools coverage-percent "${export_file}" "${RUST_MIN_COVERAGE}" "Rust launcher coverage")
}

run_go_citools_coverage() {
  local cover_file="${TMP_ROOT}/go-metadatautil.cover"
  local summary=""

  (
    cd "${ROOT_DIR}"
    go test -coverprofile="${cover_file}" ./internal/metadatautil >/dev/null
  )

  summary="$(go tool cover -func="${cover_file}" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
  if [[ -z "${summary}" ]]; then
    echo "Unable to determine Go citools coverage." >&2
    exit 1
  fi

  if ! awk -v got="${summary}" -v min="${GO_CITOOLS_MIN_COVERAGE}" 'BEGIN { exit !(got + 0 >= min + 0) }'; then
    echo "Go citools coverage ${summary}% is below ${GO_CITOOLS_MIN_COVERAGE}%." >&2
    exit 1
  fi
}

run_rust_launcher_coverage
run_go_citools_coverage

echo "Workcell supported coverage verification passed."
