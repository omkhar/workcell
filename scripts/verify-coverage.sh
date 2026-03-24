#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PYTHON_MIN_COVERAGE="${WORKCELL_PYTHON_COVERAGE_MIN:-90}"
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

require_tool python3
require_tool cargo
coverage_tools_available() {
  python3 -m coverage --version >/dev/null 2>&1 || return 1
  LLVM_COV_BIN="$(resolve_llvm_tool llvm-cov)" || return 1
  LLVM_PROFDATA_BIN="$(resolve_llvm_tool llvm-profdata)" || return 1
}

if ! coverage_tools_available; then
  if [[ "${REQUIRE_SUPPORTED_COVERAGE}" == "1" ]]; then
    echo "Supported coverage tools are required but not fully available." >&2
    exit 1
  fi
  echo "Skipping supported coverage verification because coverage.py or LLVM coverage tools are not available locally." >&2
  exit 0
fi

TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-coverage.XXXXXX")"
cleanup() {
  rm -rf "${TMP_ROOT}"
}
trap cleanup EXIT

run_python_coverage() {
  local data_file="${TMP_ROOT}/python-coverage.data"
  local report_file="${TMP_ROOT}/python-coverage.json"

  (
    cd "${ROOT_DIR}"
    COVERAGE_FILE="${data_file}" python3 -m coverage run --branch \
      -m unittest discover -s tests/python -p 'test_*.py'
    COVERAGE_FILE="${data_file}" python3 -m coverage json \
      --pretty-print \
      --include="scripts/lib/*.py" \
      -o "${report_file}"
  )

  python3 - "${report_file}" "${PYTHON_MIN_COVERAGE}" <<'PY'
import json
import pathlib
import sys

report = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
minimum = float(sys.argv[2])
percent = float(report["totals"]["percent_covered"])
if percent < minimum:
    raise SystemExit(
        f"Python helper coverage is {percent:.2f}%, below the required {minimum:.2f}%"
    )
print(f"Python helper coverage: {percent:.2f}%")
PY
}

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

  python3 - "${message_file}" >"${TMP_ROOT}/rust-binaries.txt" <<'PY'
import json
import pathlib
import sys

message_path = pathlib.Path(sys.argv[1])
executables = []
for raw_line in message_path.read_text(encoding="utf-8").splitlines():
    if not raw_line.startswith("{"):
        continue
    message = json.loads(raw_line)
    if message.get("reason") != "compiler-artifact":
        continue
    executable = message.get("executable")
    target = message.get("target", {})
    if executable and target.get("kind") == ["bin"]:
        executables.append(executable)

if not executables:
    raise SystemExit("Unable to locate instrumented Rust test executables for coverage")

print("\n".join(sorted(set(executables))))
PY

  mapfile -t rust_binaries <"${TMP_ROOT}/rust-binaries.txt"
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

  python3 - "${export_file}" "${RUST_MIN_COVERAGE}" <<'PY'
import json
import pathlib
import sys

export = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
minimum = float(sys.argv[2])
data = export.get("data", [])
if not data:
    raise SystemExit("Rust coverage export did not contain summary data")
totals = data[0].get("totals", {})
lines = totals.get("lines", {})
percent = float(lines.get("percent", 0.0))
if percent < minimum:
    raise SystemExit(
        f"Rust launcher coverage is {percent:.2f}%, below the required {minimum:.2f}%"
    )
print(f"Rust launcher coverage: {percent:.2f}%")
PY
}

run_python_coverage
run_rust_launcher_coverage

echo "Workcell supported coverage verification passed."
