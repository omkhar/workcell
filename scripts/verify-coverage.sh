#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUST_MIN_COVERAGE="${WORKCELL_RUST_COVERAGE_MIN:-90}"
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

  (cd "${ROOT_DIR}" && go run ./cmd/workcell-metadatautil coverage-executables "${message_file}") >"${TMP_ROOT}/rust-binaries.txt"

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

  (cd "${ROOT_DIR}" && go run ./cmd/workcell-metadatautil coverage-percent "${export_file}" "${RUST_MIN_COVERAGE}" "Rust launcher coverage")
}

run_rust_launcher_coverage

echo "Workcell supported coverage verification passed."
