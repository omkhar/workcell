#!/usr/bin/env -S BASH_ENV= ENV= bash
set -uo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Log helpers
log_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
log_warning() { echo -e "${YELLOW}[WARNING]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

separator() {
  printf '=%.0s' {1..70}
  echo
}

# Error collection for end-of-run summary
FAILED_CHECKS=()
FAILED_OUTPUTS=()

# CLI flags
AUTO_FIX=false
LIST_ONLY=false
STRICT=false

usage() {
  echo "Usage: $(basename "$0") [OPTIONS]"
  echo ""
  echo "Options:"
  echo "  --auto-fix      Run markdownlint --fix before linting"
  echo "  --strict        Exit on first check failure"
  echo "  -l, --list      List files that would be checked, then exit"
  echo "  -h, --help      Show usage"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --auto-fix)
      AUTO_FIX=true
      shift
      ;;
    --strict)
      STRICT=true
      shift
      ;;
    -l | --list)
      LIST_ONLY=true
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      usage
      exit 1
      ;;
  esac
done

# ---------------------------------------------------------------------------
# Tool availability
# ---------------------------------------------------------------------------
# All tools are installed in the validator container image.  If a tool is
# missing, run: scripts/build-and-test.sh --install
require_tool() {
  local tool="$1"
  if command -v "${tool}" &>/dev/null; then
    return 0
  fi
  log_error "${tool} not found. Run: scripts/build-and-test.sh --install"
  exit 1
}

require_cargo_subcommand() {
  local sub="$1"
  if cargo "${sub}" --version &>/dev/null; then
    return 0
  fi
  log_error "Missing cargo subcommand: cargo ${sub}. Install the Rust toolchain."
  exit 1
}

# ---------------------------------------------------------------------------
# File discovery (find-based, git-agnostic)
# ---------------------------------------------------------------------------
# This script runs identically on a developer workstation and inside the CI
# validator container (which volume-mounts the repo without .git).  All file
# discovery uses find so there is exactly one code path.
#
# Standard prune list: .git, dist, tmp, vendor, node_modules, build artifacts.
# ---------------------------------------------------------------------------
_find_files() {
  local name_args=()
  local first=true
  for pat in "$@"; do
    if [[ "${first}" == "true" ]]; then
      first=false
    else
      name_args+=("-o")
    fi
    name_args+=("-name" "${pat}")
  done
  find "${ROOT_DIR}" \
    -path "${ROOT_DIR}/.git" -prune -o \
    -path "${ROOT_DIR}/dist" -prune -o \
    -path "${ROOT_DIR}/tmp" -prune -o \
    -path "*/node_modules" -prune -o \
    -path "*/vendor" -prune -o \
    -path "*/.venv" -prune -o \
    -path "*/__pycache__" -prune -o \
    -path "*/.pytest_cache" -prune -o \
    -path "*/target" -prune -o \
    -type f \( "${name_args[@]}" \) -print |
    sed "s|^${ROOT_DIR}/||" |
    sort
}

discover_sh_files() {
  _find_files '*.sh'
}

discover_md_files() {
  _find_files '*.md'
}

discover_py_files() {
  _find_files '*.py'
}

discover_py_test_files() {
  find "${ROOT_DIR}/tests" -type f -name '*.py' -print 2>/dev/null |
    sed "s|^${ROOT_DIR}/||" | sort
}

discover_cargo_tomls() {
  _find_files 'Cargo.toml'
}

discover_json_files() {
  local results=""
  for dir in adapters .github runtime/container/providers tests/scenarios; do
    if [[ -d "${ROOT_DIR}/${dir}" ]]; then
      results+="$(find "${ROOT_DIR}/${dir}" -path "*/node_modules" -prune -o -type f -name '*.json' -print 2>/dev/null | sed "s|^${ROOT_DIR}/||")"$'\n'
    fi
  done
  echo "${results}" | grep -v '^$' | sort
}

discover_toml_files() {
  _find_files '*.toml'
}

discover_yaml_files() {
  local results=""
  for f in .github/dependency-review-config.yml .github/dependabot.yml; do
    [[ -f "${ROOT_DIR}/${f}" ]] && results+="${f}"$'\n'
  done
  if [[ -d "${ROOT_DIR}/.github/workflows" ]]; then
    results+="$(find "${ROOT_DIR}/.github/workflows" -type f \( -name '*.yml' -o -name '*.yaml' \) -print 2>/dev/null | sed "s|^${ROOT_DIR}/||")"$'\n'
  fi
  echo "${results}" | grep -v '^$' | sort
}

discover_doc_files() {
  _find_files '*.md' '*.txt' '*.1'
}

discover_shell_scripts_without_ext() {
  local known_scripts=(
    'runtime/container/bin/git'
    'runtime/container/bin/node'
    'scripts/workcell'
  )
  local results=""
  for f in "${known_scripts[@]}"; do
    [[ -f "${ROOT_DIR}/${f}" ]] && results+="${f}"$'\n'
  done
  echo "${results}" | grep -v '^$' | sort
}

# ---------------------------------------------------------------------------
# --list mode
# ---------------------------------------------------------------------------
if [[ "${LIST_ONLY}" == "true" ]]; then
  echo "Shell files (.sh):"
  discover_sh_files
  echo ""
  echo "Shell scripts (no extension):"
  discover_shell_scripts_without_ext
  echo ""
  echo "Markdown files (excluding vendor):"
  discover_md_files
  echo ""
  echo "Python files:"
  discover_py_files
  echo ""
  echo "Python test files:"
  discover_py_test_files
  echo ""
  echo "Rust workspaces (Cargo.toml, excluding vendor):"
  discover_cargo_tomls
  echo ""
  echo "JSON files (adapters, .github, providers, scenarios):"
  discover_json_files
  echo ""
  echo "TOML files (excluding vendor):"
  discover_toml_files
  echo ""
  echo "YAML files (.github config and workflows):"
  discover_yaml_files
  echo ""
  echo "Doc files (md, txt, man pages, excluding vendor):"
  discover_doc_files
  exit 0
fi

# ===================================================================
# LINTERS
# ===================================================================

check_shellcheck() {
  separator
  log_info "Running shellcheck..."

  require_tool shellcheck

  local files
  files=$(discover_sh_files)
  local extra_files
  extra_files=$(discover_shell_scripts_without_ext)
  if [[ -n "${extra_files}" ]]; then
    if [[ -n "${files}" ]]; then
      files="${files}"$'\n'"${extra_files}"
    else
      files="${extra_files}"
    fi
  fi

  if [[ -z "${files}" ]]; then
    log_warning "No shell files found"
    return 0
  fi

  local abs_files=()
  while IFS= read -r f; do
    abs_files+=("${ROOT_DIR}/${f}")
  done <<<"${files}"

  local exit_code=0
  local output
  output=$(shellcheck -x "${abs_files[@]}" 2>&1) || exit_code=$?

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "shellcheck failed"
    echo "${output}"
    FAILED_CHECKS+=("shellcheck")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "shellcheck passed"
  fi
  return ${exit_code}
}

check_shfmt() {
  separator
  log_info "Running shfmt..."

  require_tool shfmt

  local files
  files=$(discover_sh_files)
  local extra_files
  extra_files=$(discover_shell_scripts_without_ext)
  if [[ -n "${extra_files}" ]]; then
    if [[ -n "${files}" ]]; then
      files="${files}"$'\n'"${extra_files}"
    else
      files="${extra_files}"
    fi
  fi

  if [[ -z "${files}" ]]; then
    log_warning "No shell files found"
    return 0
  fi

  local abs_files=()
  while IFS= read -r f; do
    abs_files+=("${ROOT_DIR}/${f}")
  done <<<"${files}"

  local exit_code=0
  local output
  output=$(shfmt -ln=bash -i 2 -ci -d "${abs_files[@]}" 2>&1) || exit_code=$?

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "shfmt failed"
    echo "${output}"
    FAILED_CHECKS+=("shfmt")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "shfmt passed"
  fi
  return ${exit_code}
}

check_markdownlint() {
  separator
  log_info "Running markdownlint..."

  require_tool markdownlint

  local files
  files=$(discover_md_files)
  if [[ -z "${files}" ]]; then
    log_warning "No markdown files found"
    return 0
  fi

  local abs_files=()
  while IFS= read -r f; do
    abs_files+=("${ROOT_DIR}/${f}")
  done <<<"${files}"

  # Auto-fix pass only when explicitly requested
  if [[ "${AUTO_FIX}" == "true" ]]; then
    log_info "Running markdownlint --fix..."
    markdownlint --config "${ROOT_DIR}/.markdownlint.json" --fix "${abs_files[@]}" 2>/dev/null || true
  fi

  local exit_code=0
  local output
  output=$(markdownlint --config "${ROOT_DIR}/.markdownlint.json" "${abs_files[@]}" 2>&1) || exit_code=$?

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "markdownlint failed"
    echo "${output}"
    FAILED_CHECKS+=("markdownlint")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "markdownlint passed"
  fi
  return ${exit_code}
}

check_yamllint() {
  separator
  log_info "Running yamllint..."

  require_tool yamllint

  local files
  files=$(discover_yaml_files)
  if [[ -z "${files}" ]]; then
    log_warning "No YAML files found"
    return 0
  fi

  local abs_files=()
  while IFS= read -r f; do
    abs_files+=("${ROOT_DIR}/${f}")
  done <<<"${files}"

  local exit_code=0
  local output
  output=$(yamllint -d "{extends: default, rules: {comments: disable, document-start: disable, line-length: disable, truthy: disable}}" \
    "${abs_files[@]}" 2>&1) || exit_code=$?

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "yamllint failed"
    echo "${output}"
    FAILED_CHECKS+=("yamllint")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "yamllint passed"
  fi
  return ${exit_code}
}

check_codespell() {
  separator
  log_info "Running codespell..."

  require_tool codespell

  local files
  files=$(discover_doc_files)
  if [[ -z "${files}" ]]; then
    log_warning "No doc files found"
    return 0
  fi

  local abs_files=()
  while IFS= read -r f; do
    abs_files+=("${ROOT_DIR}/${f}")
  done <<<"${files}"

  local exit_code=0
  local output
  output=$(codespell --config "${ROOT_DIR}/.codespellrc" "${abs_files[@]}" 2>&1) || exit_code=$?

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "codespell failed"
    echo "${output}"
    FAILED_CHECKS+=("codespell")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "codespell passed"
  fi
  return ${exit_code}
}

# ===================================================================
# COMPILE / BUILD CHECKS
# ===================================================================

check_python_compile() {
  separator
  log_info "Running Python compile check..."

  require_tool python3

  local files
  files=$(discover_py_files)
  if [[ -z "${files}" ]]; then
    log_warning "No Python files found"
    return 0
  fi

  local abs_files=()
  while IFS= read -r f; do
    abs_files+=("${ROOT_DIR}/${f}")
  done <<<"${files}"

  local exit_code=0
  local output=""
  local check_output
  for f in "${abs_files[@]}"; do
    check_output=$(python3 -B -m py_compile "${f}" 2>&1) || {
      exit_code=$?
      output+="${check_output}"$'\n'
    }
  done

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "Python compile check failed"
    echo "${output}"
    FAILED_CHECKS+=("python-compile")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "Python compile check passed"
  fi
  return ${exit_code}
}

check_json_validation() {
  separator
  log_info "Running JSON validation..."

  require_tool python3

  local files
  files=$(discover_json_files)
  if [[ -z "${files}" ]]; then
    log_warning "No JSON files found"
    return 0
  fi

  local exit_code=0
  local output=""
  while IFS= read -r f; do
    if ! python3 -m json.tool "${ROOT_DIR}/${f}" >/dev/null 2>&1; then
      exit_code=1
      output+="Invalid JSON: ${f}\n"
    fi
  done <<<"${files}"

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "JSON validation failed"
    echo -e "${output}"
    FAILED_CHECKS+=("json-validation")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "JSON validation passed"
  fi
  return ${exit_code}
}

check_toml_validation() {
  separator
  log_info "Running TOML validation..."

  require_tool python3

  local files
  files=$(discover_toml_files)
  if [[ -z "${files}" ]]; then
    log_warning "No TOML files found"
    return 0
  fi

  local abs_files=()
  while IFS= read -r f; do
    abs_files+=("${ROOT_DIR}/${f}")
  done <<<"${files}"

  local exit_code=0
  local output
  output=$(
    python3 - "${abs_files[@]}" <<'PY' 2>&1
import pathlib
import tomllib
import sys

errors = []
for arg in sys.argv[1:]:
    path = pathlib.Path(arg)
    try:
        with path.open("rb") as handle:
            tomllib.load(handle)
    except Exception as exc:
        errors.append(f"{path}: {exc}")
if errors:
    for e in errors:
        print(e, file=sys.stderr)
    sys.exit(1)
PY
  ) || exit_code=$?

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "TOML validation failed"
    echo "${output}"
    FAILED_CHECKS+=("toml-validation")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "TOML validation passed"
  fi
  return ${exit_code}
}

check_rust_fmt() {
  separator
  log_info "Running Rust fmt check..."

  require_tool cargo

  local cargo_files
  cargo_files=$(discover_cargo_tomls)
  if [[ -z "${cargo_files}" ]]; then
    log_warning "No Cargo.toml files found"
    return 0
  fi

  local exit_code=0
  local output=""

  while IFS= read -r manifest; do
    local manifest_dir
    manifest_dir="${ROOT_DIR}/$(dirname "${manifest}")"
    local check_output
    check_output=$(cd "${manifest_dir}" && RUSTFLAGS="-D warnings" cargo fmt --all --check 2>&1) || {
      exit_code=1
      output+="cargo fmt --check (${manifest}):\n${check_output}\n\n"
      log_error "  cargo fmt --check failed for ${manifest}"
    }
  done <<<"${cargo_files}"

  if [[ ${exit_code} -ne 0 ]]; then
    echo -e "${output}"
    FAILED_CHECKS+=("rust-fmt")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "Rust fmt check passed"
  fi
  return ${exit_code}
}

check_rust_clippy() {
  separator
  log_info "Running Rust clippy..."

  require_tool cargo

  require_cargo_subcommand clippy

  local cargo_files
  cargo_files=$(discover_cargo_tomls)
  if [[ -z "${cargo_files}" ]]; then
    log_warning "No Cargo.toml files found"
    return 0
  fi

  local exit_code=0
  local output=""

  while IFS= read -r manifest; do
    local manifest_dir
    manifest_dir="${ROOT_DIR}/$(dirname "${manifest}")"
    local check_output
    check_output=$(cd "${manifest_dir}" && RUSTFLAGS="-D warnings" cargo clippy \
      --all-targets --all-features --locked --offline \
      -- -D warnings 2>&1) || {
      exit_code=1
      output+="cargo clippy (${manifest}):\n${check_output}\n\n"
      log_error "  cargo clippy failed for ${manifest}"
    }
  done <<<"${cargo_files}"

  if [[ ${exit_code} -ne 0 ]]; then
    echo -e "${output}"
    FAILED_CHECKS+=("rust-clippy")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "Rust clippy passed"
  fi
  return ${exit_code}
}

# ===================================================================
# TESTS
# ===================================================================

check_python_tests() {
  separator
  log_info "Running Python tests..."

  local test_files
  test_files=$(discover_py_test_files)
  if [[ -z "${test_files}" ]]; then
    log_warning "No Python test files found"
    return 0
  fi

  local exit_code=0
  local output
  output=$(python3 -B -m pytest "${ROOT_DIR}/tests/" -v -p no:cacheprovider 2>&1) || exit_code=$?

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "Python tests failed"
    echo "${output}"
    FAILED_CHECKS+=("python-tests")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "Python tests passed"
  fi
  return ${exit_code}
}

check_rust_tests() {
  separator
  log_info "Running Rust tests..."

  require_tool cargo

  local cargo_files
  cargo_files=$(discover_cargo_tomls)
  if [[ -z "${cargo_files}" ]]; then
    log_warning "No Cargo.toml files found"
    return 0
  fi

  local exit_code=0
  local output=""

  while IFS= read -r manifest; do
    local manifest_dir
    manifest_dir="${ROOT_DIR}/$(dirname "${manifest}")"
    log_info "  Testing ${manifest}"
    local check_output
    check_output=$(cd "${manifest_dir}" && RUSTFLAGS="-D warnings" cargo test --locked --offline 2>&1) || {
      exit_code=1
      output+="cargo test (${manifest}):\n${check_output}\n\n"
      log_error "  cargo test failed for ${manifest}"
    }
  done <<<"${cargo_files}"

  if [[ ${exit_code} -ne 0 ]]; then
    echo -e "${output}"
    FAILED_CHECKS+=("rust-tests")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "Rust tests passed"
  fi
  return ${exit_code}
}

check_mutation_tests() {
  separator
  log_info "Running mutation tests..."

  local script="${ROOT_DIR}/scripts/run-mutation-tests.sh"
  if [[ ! -x "${script}" ]]; then
    log_warning "scripts/run-mutation-tests.sh not found or not executable. Skipping."
    return 0
  fi

  local exit_code=0
  local output
  output=$("${script}" 2>&1) || exit_code=$?

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "Mutation tests failed"
    echo "${output}"
    FAILED_CHECKS+=("mutation-tests")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "Mutation tests passed"
  fi
  return ${exit_code}
}

# ===================================================================
# VERIFICATION (coverage, scenarios, invariants, manifests)
# ===================================================================

run_verification_script() {
  local name="$1"
  local script="$2"
  shift 2

  separator
  log_info "Running ${name}..."

  if [[ ! -x "${script}" ]]; then
    log_warning "${script} not found or not executable. Skipping."
    return 0
  fi

  local exit_code=0
  local output
  output=$("${script}" "$@" 2>&1) || exit_code=$?

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "${name} failed"
    echo "${output}"
    FAILED_CHECKS+=("${name}")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "${name} passed"
  fi
  return ${exit_code}
}

check_coverage() {
  run_verification_script "coverage-verification" "${ROOT_DIR}/scripts/verify-coverage.sh"
}

check_scenario_coverage() {
  run_verification_script "scenario-coverage" "${ROOT_DIR}/scripts/verify-scenario-coverage.sh"
}

check_control_plane_parity() {
  run_verification_script "control-plane-parity" "${ROOT_DIR}/scripts/verify-control-plane-parity.sh"
}

check_build_input_manifest() {
  run_verification_script "build-input-manifest" "${ROOT_DIR}/scripts/verify-build-input-manifest.sh"
}

check_control_plane_manifest() {
  run_verification_script "control-plane-manifest" "${ROOT_DIR}/scripts/verify-control-plane-manifest.sh"
}

# ===================================================================
# REPO HYGIENE
# ===================================================================

check_executable_permissions() {
  separator
  log_info "Checking executable permissions on shell files..."

  local files
  files=$(discover_sh_files)
  local extra_files
  extra_files=$(discover_shell_scripts_without_ext)
  if [[ -n "${extra_files}" ]]; then
    if [[ -n "${files}" ]]; then
      files="${files}"$'\n'"${extra_files}"
    else
      files="${extra_files}"
    fi
  fi

  if [[ -z "${files}" ]]; then
    log_warning "No shell files found"
    return 0
  fi

  local exit_code=0
  local output=""
  while IFS= read -r f; do
    if [[ ! -x "${ROOT_DIR}/${f}" ]]; then
      exit_code=1
      output+="Expected executable script: ${f}\n"
    fi
  done <<<"${files}"

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "Executable permission check failed"
    echo -e "${output}"
    FAILED_CHECKS+=("executable-permissions")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "Executable permissions OK"
  fi
  return ${exit_code}
}

check_branding() {
  separator
  log_info "Scanning for stale branding references..."

  local pattern="agent-boundary|Agent Boundary|agent boundary"
  local exit_code=0
  local output=""
  local rc=0

  if command -v rg &>/dev/null; then
    output=$(rg -n "${pattern}" "${ROOT_DIR}" \
      -g '!**/.git/**' \
      -g '!build-and-test.sh' \
      -g '!scripts/validate-repo.sh' \
      -g '!dist/**' \
      -g '!tmp/**' 2>&1) || rc=$?
    if [[ ${rc} -eq 0 ]]; then
      exit_code=1
    elif [[ ${rc} -ge 2 ]]; then
      exit_code=1
      output="rg error (exit ${rc}): ${output}"
    fi
  else
    output=$(grep -RInE "${pattern}" "${ROOT_DIR}" \
      --exclude-dir=.git \
      --exclude-dir=dist \
      --exclude-dir=tmp \
      --exclude=build-and-test.sh \
      --exclude=validate-repo.sh 2>&1) || rc=$?
    if [[ ${rc} -eq 0 ]]; then
      exit_code=1
    elif [[ ${rc} -ge 2 ]]; then
      exit_code=1
      output="grep error (exit ${rc}): ${output}"
    fi
  fi

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "Found stale pre-rename branding"
    echo "${output}"
    FAILED_CHECKS+=("branding-scan")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "No stale branding found"
  fi
  return ${exit_code}
}

check_credentials() {
  separator
  log_info "Scanning for credential patterns..."

  if ! command -v python3 &>/dev/null; then
    log_error "python3 not found. Credential scan cannot be skipped safely."
    FAILED_CHECKS+=("credential-scan")
    FAILED_OUTPUTS+=("python3 not installed; credential scan requires python3")
    return 1
  fi

  local exit_code=0
  local output
  output=$(
    python3 - "${ROOT_DIR}" <<'PY' 2>&1
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
patterns = [
    re.compile(r'sk-[A-Za-z0-9]{40,}'),
    re.compile(r'AIza[A-Za-z0-9\-_]{35}'),
    re.compile(r'ya29\.[A-Za-z0-9\-_]+'),
]
scan_dirs = [root / "tests", root / "docs" / "examples"]
found = 0
for scan_dir in scan_dirs:
    if not scan_dir.exists():
        continue
    for path in sorted(scan_dir.rglob("*")):
        if not path.is_file():
            continue
        if ".git" in path.parts:
            continue
        try:
            text = path.read_text(encoding="utf-8", errors="ignore")
        except OSError:
            continue
        for pattern in patterns:
            if pattern.search(text):
                print(f"Possible credential in {path}", file=sys.stderr)
                found += 1
if found:
    raise SystemExit(f"Found {found} possible credential(s) in tests/ or docs/examples/")
PY
  ) || exit_code=$?

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "Credential scan failed"
    echo "${output}"
    FAILED_CHECKS+=("credential-scan")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "No credentials found"
  fi
  return ${exit_code}
}

check_manpage() {
  separator
  log_info "Validating manpage..."

  local manpage="${ROOT_DIR}/man/workcell.1"
  if [[ ! -f "${manpage}" ]]; then
    log_warning "man/workcell.1 not found. Skipping."
    return 0
  fi

  local exit_code=0
  local output

  if command -v mandoc &>/dev/null; then
    output=$(mandoc -Tlint "${manpage}" 2>&1) || exit_code=$?
  elif command -v nroff &>/dev/null; then
    nroff -man "${manpage}" >/dev/null 2>&1 || exit_code=$?
  else
    log_error "Neither mandoc nor nroff found. Run: scripts/build-and-test.sh --install"
    exit 1
  fi

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "Manpage validation failed"
    echo "${output}"
    FAILED_CHECKS+=("manpage")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "Manpage validation passed"
  fi
  return ${exit_code}
}

check_adapter_scratch_state() {
  separator
  log_info "Checking adapter scratch state..."

  local exit_code=0
  local output=""
  for scratch_dir in \
    "${ROOT_DIR}/adapters/codex/.codex/memories" \
    "${ROOT_DIR}/adapters/codex/.codex/tmp"; do
    if find "${scratch_dir}" -mindepth 1 -print -quit 2>/dev/null | grep -q .; then
      exit_code=1
      output+="Unexpected adapter scratch state present: ${scratch_dir}\n"
    fi
  done

  if [[ ${exit_code} -ne 0 ]]; then
    log_error "Adapter scratch state check failed"
    echo -e "${output}"
    FAILED_CHECKS+=("adapter-scratch-state")
    FAILED_OUTPUTS+=("${output}")
  else
    log_success "Adapter scratch state clean"
  fi
  return ${exit_code}
}

check_legacy_config() {
  separator
  log_info "Checking for legacy config files..."

  if [[ -e "${ROOT_DIR}/.workcell.remote.local" ]]; then
    local msg="Legacy repo-local remote builder config must not exist: .workcell.remote.local"
    log_error "${msg}"
    FAILED_CHECKS+=("legacy-config")
    FAILED_OUTPUTS+=("${msg}")
    return 1
  fi

  log_success "No legacy config files found"
  return 0
}

check_docs_examples_exist() {
  separator
  log_info "Checking docs/examples/ exists and is non-empty..."

  if [[ ! -d "${ROOT_DIR}/docs/examples" ]]; then
    local msg="docs/examples/ directory must exist"
    log_error "${msg}"
    FAILED_CHECKS+=("docs-examples")
    FAILED_OUTPUTS+=("${msg}")
    return 1
  fi

  if ! find "${ROOT_DIR}/docs/examples" -type f -print -quit | grep -q .; then
    local msg="docs/examples/ must contain at least one file"
    log_error "${msg}"
    FAILED_CHECKS+=("docs-examples")
    FAILED_OUTPUTS+=("${msg}")
    return 1
  fi

  log_success "docs/examples/ OK"
  return 0
}

# ===================================================================
# Run all checks
# ===================================================================

# In --strict mode, exit immediately on the first failure.
# Otherwise, collect failures and report at the end.
strict_gate() {
  if [[ "${STRICT}" == "true" ]] && [[ ${#FAILED_CHECKS[@]} -gt 0 ]]; then
    local last_index=$((${#FAILED_CHECKS[@]} - 1))
    log_error "Aborting (--strict): ${FAILED_CHECKS[$last_index]} failed"
    exit 1
  fi
}

# --- Linters ---
check_shellcheck || true
strict_gate
check_shfmt || true
strict_gate
check_markdownlint || true
strict_gate
check_yamllint || true
strict_gate
check_codespell || true
strict_gate

# --- Compile / build checks ---
check_python_compile || true
strict_gate
check_json_validation || true
strict_gate
check_toml_validation || true
strict_gate
check_rust_fmt || true
strict_gate
check_rust_clippy || true
strict_gate

# --- Tests ---
check_python_tests || true
strict_gate
check_rust_tests || true
strict_gate
check_mutation_tests || true
strict_gate

# --- Verification ---
# Scenario tests require the container exec guard (libworkcell_exec_guard.so)
# and only pass inside the runtime container.  Run via pre-merge.sh instead.
# check_scenario_tests || true
# strict_gate
check_coverage || true
strict_gate
check_scenario_coverage || true
strict_gate
check_control_plane_parity || true
strict_gate
check_build_input_manifest || true
strict_gate
check_control_plane_manifest || true
strict_gate

# --- Repo hygiene ---
check_executable_permissions || true
strict_gate
check_branding || true
strict_gate
check_credentials || true
strict_gate
check_manpage || true
strict_gate
check_adapter_scratch_state || true
strict_gate
check_legacy_config || true
strict_gate
check_docs_examples_exist || true
strict_gate

# ===================================================================
# Error replay summary
# ===================================================================

separator
if [[ ${#FAILED_CHECKS[@]} -eq 0 ]]; then
  log_success "All checks passed!"
  exit 0
else
  log_error "Failed checks (${#FAILED_CHECKS[@]}): ${FAILED_CHECKS[*]}"
  echo ""
  for i in "${!FAILED_CHECKS[@]}"; do
    log_error "--- ${FAILED_CHECKS[$i]} ---"
    echo -e "${FAILED_OUTPUTS[$i]}"
    echo ""
  done
  exit 1
fi
