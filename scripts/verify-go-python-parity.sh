#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat >&2 <<'EOF'
usage: verify-go-python-parity.sh --name NAME --python-cmd CMD --go-cmd CMD [--compare-root LEFT:RIGHT]...

Runs two command strings, compares exit code/stdout/stderr, and optionally compares
matching directory trees. Commands run from the repository root.
EOF
  exit 64
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

name=""
python_cmd=""
go_cmd=""
compare_roots=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --name)
      name="${2:-}"
      shift 2
      ;;
    --python-cmd)
      python_cmd="${2:-}"
      shift 2
      ;;
    --go-cmd)
      go_cmd="${2:-}"
      shift 2
      ;;
    --compare-root)
      compare_roots+=("${2:-}")
      shift 2
      ;;
    --help | -h)
      usage
      ;;
    *)
      usage
      ;;
  esac
done

[[ -n "${name}" && -n "${python_cmd}" && -n "${go_cmd}" ]] || usage

require_tool go
require_tool bash

TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-go-python-parity.XXXXXX")"
cleanup() {
  rm -rf "${TMP_ROOT}"
}
trap cleanup EXIT

run_command() {
  local command="$1"
  local stdout_file="$2"
  local stderr_file="$3"
  local status_file="$4"

  local status=0
  if (
    cd "${ROOT_DIR}"
    bash -lc "${command}"
  ) >"${stdout_file}" 2>"${stderr_file}"; then
    status=0
  else
    status=$?
  fi
  printf '%s\n' "${status}" >"${status_file}"
}

compare_trees() {
  local left_root="$1"
  local right_root="$2"
  (
    cd "${ROOT_DIR}"
    go run ./cmd/workcell-treecompare "${left_root}" "${right_root}"
  )
}

python_stdout="${TMP_ROOT}/python.stdout"
python_stderr="${TMP_ROOT}/python.stderr"
python_status="${TMP_ROOT}/python.status"
go_stdout="${TMP_ROOT}/go.stdout"
go_stderr="${TMP_ROOT}/go.stderr"
go_status="${TMP_ROOT}/go.status"

run_command "${python_cmd}" "${python_stdout}" "${python_stderr}" "${python_status}"
run_command "${go_cmd}" "${go_stdout}" "${go_stderr}" "${go_status}"

if ! cmp -s "${python_status}" "${go_status}"; then
  echo "${name}: exit code mismatch" >&2
  echo "python=$(<"${python_status}")" >&2
  echo "go=$(<"${go_status}")" >&2
  exit 1
fi

if ! cmp -s "${python_stdout}" "${go_stdout}"; then
  echo "${name}: stdout mismatch" >&2
  exit 1
fi

if ! cmp -s "${python_stderr}" "${go_stderr}"; then
  echo "${name}: stderr mismatch" >&2
  exit 1
fi

for pair in "${compare_roots[@]}"; do
  left_root="${pair%%:*}"
  right_root="${pair#*:}"
  if [[ -z "${left_root}" || -z "${right_root}" || "${left_root}" == "${right_root}" ]]; then
    echo "${name}: invalid compare-root entry: ${pair}" >&2
    exit 1
  fi
  compare_trees "${left_root}" "${right_root}"
done

echo "${name}: parity passed"
