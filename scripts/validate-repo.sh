#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SKIP_HEAVY_HOST_SHELLCHECK="${WORKCELL_SKIP_HEAVY_HOST_SHELLCHECK:-0}"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_cargo_subcommand() {
  cargo "$1" --version >/dev/null 2>&1 || {
    echo "Missing required cargo subcommand: cargo $1" >&2
    exit 1
  }
}

require_tool shellcheck
require_tool shfmt
require_tool go
require_tool gofmt
require_tool yamllint
require_tool cargo
require_tool rustfmt
require_tool git
require_cargo_subcommand clippy

METADATAUTIL_BIN=""

cleanup() {
  if [[ -n "${METADATAUTIL_BIN}" && -e "${METADATAUTIL_BIN}" ]]; then
    rm -f "${METADATAUTIL_BIN}"
  fi
}

build_metadatautil() {
  if [[ -n "${METADATAUTIL_BIN}" ]]; then
    return 0
  fi
  METADATAUTIL_BIN="$(mktemp "${TMPDIR:-/tmp}/workcell-metadatautil.XXXXXX")"
  (cd "${ROOT_DIR}" && go build -buildvcs=false -o "${METADATAUTIL_BIN}" ./cmd/workcell-metadatautil)
}

run_metadatautil() {
  build_metadatautil
  "${METADATAUTIL_BIN}" "$@"
}

trap cleanup EXIT

python_files=()
while IFS= read -r file; do
  python_files+=("${file}")
done < <(
  find "${ROOT_DIR}" \
    -path "${ROOT_DIR}/.git" -prune -o \
    -path "${ROOT_DIR}/.venv" -prune -o \
    -path "${ROOT_DIR}/dist" -prune -o \
    -path "${ROOT_DIR}/tmp" -prune -o \
    -path "${ROOT_DIR}/runtime/container/providers/node_modules" -prune -o \
    -type f -name '*.py' -print | sort
)

branding_scan() {
  local pattern="agent-boundary|Agent Boundary|agent boundary"

  if command -v rg >/dev/null 2>&1; then
    rg -n "${pattern}" "${ROOT_DIR}" \
      -g '!**/.git/**' \
      -g '!scripts/validate-repo.sh' \
      -g '!dist/**' \
      -g '!tmp/**'
    return
  fi

  grep -RInE "${pattern}" "${ROOT_DIR}" \
    --exclude-dir=.git \
    --exclude-dir=dist \
    --exclude-dir=tmp \
    --exclude=validate-repo.sh
}

validate_manpage() {
  if command -v mandoc >/dev/null 2>&1; then
    mandoc -Tlint "${ROOT_DIR}/man/workcell.1" >/dev/null
    return
  fi

  if command -v nroff >/dev/null 2>&1; then
    nroff -man "${ROOT_DIR}/man/workcell.1" >/dev/null
    return
  fi

  echo "Missing required tool: mandoc or nroff" >&2
  exit 1
}

shell_files=(
  "${ROOT_DIR}/install.sh"
  "${ROOT_DIR}/scripts/check-pinned-inputs.sh"
  "${ROOT_DIR}/scripts/build-and-test.sh"
  "${ROOT_DIR}/scripts/workcell"
  "${ROOT_DIR}/scripts/check-workflows.sh"
  "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"
  "${ROOT_DIR}/scripts/container-smoke.sh"
  "${ROOT_DIR}/scripts/dev-quick-check.sh"
  "${ROOT_DIR}/scripts/go-port-validate.sh"
  "${ROOT_DIR}/scripts/install-dev-tools.sh"
  "${ROOT_DIR}/scripts/lint-dockerfiles.sh"
  "${ROOT_DIR}/scripts/dev-remote-validate.sh"
  "${ROOT_DIR}/scripts/lib/extract_direct_mounts"
  "${ROOT_DIR}/scripts/lib/manage_injection_policy"
  "${ROOT_DIR}/scripts/lib/pty_transcript"
  "${ROOT_DIR}/scripts/lib/render_injection_bundle"
  "${ROOT_DIR}/scripts/lib/resolve_credential_sources"
  "${ROOT_DIR}/scripts/lib/scenario_manifest"
  "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
  "${ROOT_DIR}/scripts/generate-control-plane-manifest.sh"
  "${ROOT_DIR}/scripts/generate-builder-environment-manifest.sh"
  "${ROOT_DIR}/scripts/generate-release-checksums.sh"
  "${ROOT_DIR}/scripts/generate-build-input-manifest.sh"
  "${ROOT_DIR}/scripts/install-workcell.sh"
  "${ROOT_DIR}/scripts/uninstall.sh"
  "${ROOT_DIR}/scripts/pre-merge.sh"
  "${ROOT_DIR}/scripts/publish-github-release.sh"
  "${ROOT_DIR}/scripts/run-hosted-controls-audit.sh"
  "${ROOT_DIR}/scripts/run-mutation-tests.sh"
  "${ROOT_DIR}/scripts/verify-coverage.sh"
  "${ROOT_DIR}/scripts/verify-github-hosted-controls.sh"
  "${ROOT_DIR}/scripts/verify-go-python-parity.sh"
  "${ROOT_DIR}/scripts/validate-repo.sh"
  "${ROOT_DIR}/scripts/verify-build-input-manifest.sh"
  "${ROOT_DIR}/scripts/verify-upstream-claude-release.sh"
  "${ROOT_DIR}/scripts/verify-control-plane-manifest.sh"
  "${ROOT_DIR}/scripts/verify-release-bundle.sh"
  "${ROOT_DIR}/scripts/verify-invariants.sh"
  "${ROOT_DIR}/scripts/verify-reproducible-build.sh"
  "${ROOT_DIR}/scripts/verify-upstream-codex-release.sh"
  "${ROOT_DIR}/scripts/with-validation-snapshot.sh"
  "${ROOT_DIR}/adapters/claude/hooks/guard-bash.sh"
  "${ROOT_DIR}/runtime/container/entrypoint.sh"
  "${ROOT_DIR}/runtime/container/bin/apt-helper.sh"
  "${ROOT_DIR}/runtime/container/bin/apt-wrapper.sh"
  "${ROOT_DIR}/runtime/container/assurance.sh"
  "${ROOT_DIR}/runtime/container/development-wrapper.sh"
  "${ROOT_DIR}/runtime/container/bin/git"
  "${ROOT_DIR}/runtime/container/bin/node"
  "${ROOT_DIR}/runtime/container/home-control-plane.sh"
  "${ROOT_DIR}/runtime/container/provider-policy.sh"
  "${ROOT_DIR}/runtime/container/provider-wrapper.sh"
  "${ROOT_DIR}/runtime/container/runtime-user.sh"
  "${ROOT_DIR}/scripts/run-scenario-tests.sh"
  "${ROOT_DIR}/scripts/verify-scenario-coverage.sh"
  "${ROOT_DIR}/scripts/verify-control-plane-parity.sh"
)

while IFS= read -r file; do
  shell_files+=("${file}")
done < <(find "${ROOT_DIR}/tests/scenarios" -type f -name 'test-*.sh' -print | sort)

should_skip_shellcheck_file() {
  local file="$1"

  [[ "${SKIP_HEAVY_HOST_SHELLCHECK}" == "1" ]] || return 1
  case "${file}" in
    "${ROOT_DIR}/scripts/workcell" | "${ROOT_DIR}/scripts/verify-invariants.sh")
      return 0
      ;;
  esac
  return 1
}

for file in "${shell_files[@]}"; do
  if should_skip_shellcheck_file "${file}"; then
    continue
  fi
  shellcheck -x "${file}"
done
shfmt -ln=bash -i 2 -ci -d "${shell_files[@]}"
"${ROOT_DIR}/scripts/lint-dockerfiles.sh"

for file in "${shell_files[@]}"; do
  if [[ ! -x "${file}" ]]; then
    echo "Expected executable script: ${file}" >&2
    exit 1
  fi
done

for scratch_dir in \
  "${ROOT_DIR}/adapters/codex/.codex/memories" \
  "${ROOT_DIR}/adapters/codex/.codex/tmp"; do
  if find "${scratch_dir}" -mindepth 1 -print -quit 2>/dev/null | grep -q .; then
    echo "Unexpected adapter scratch state present: ${scratch_dir}" >&2
    exit 1
  fi
done

if [[ "${#python_files[@]}" -gt 0 ]]; then
  echo "Unexpected Python source files remain in scripts/lib:" >&2
  printf '  %s\n' "${python_files[@]}" >&2
  exit 1
fi
go_files=()
while IFS= read -r -d '' path; do
  go_files+=("${path}")
done < <(find "${ROOT_DIR}/cmd" "${ROOT_DIR}/internal" -type f -name '*.go' -print0 | sort -z)
if [[ "${#go_files[@]}" -gt 0 ]]; then
  if gofmt -l "${go_files[@]}" | grep -q .; then
    echo "Go files are not formatted with gofmt." >&2
    exit 1
  fi
fi
go vet ./...
go test ./...

json_files=()
while IFS= read -r path; do
  [[ -n "${path}" ]] || continue
  json_files+=("${path}")
done < <(
  find "${ROOT_DIR}/adapters" "${ROOT_DIR}/.github" "${ROOT_DIR}/runtime/container/providers" "${ROOT_DIR}/tests/scenarios" \
    -path '*/node_modules' -prune -o \
    -type f -name '*.json' -print | sort
)
if [[ "${#json_files[@]}" -gt 0 ]]; then
  run_metadatautil validate-json "${json_files[@]}"
fi

toml_files=()
while IFS= read -r path; do
  [[ -n "${path}" ]] || continue
  toml_files+=("${path}")
done < <(
  find "${ROOT_DIR}" \
    -path "${ROOT_DIR}/.git" -prune -o \
    -path "${ROOT_DIR}/dist" -prune -o \
    -path "${ROOT_DIR}/tmp" -prune -o \
    -path "${ROOT_DIR}/runtime/container/providers/node_modules" -prune -o \
    -type f -name '*.toml' -print | sort
)
if [[ "${#toml_files[@]}" -gt 0 ]]; then
  run_metadatautil validate-toml "${toml_files[@]}"
fi

yamllint -d "{extends: default, rules: {comments: disable, document-start: disable, line-length: disable, truthy: disable}}" \
  "${ROOT_DIR}/.github/dependency-review-config.yml" \
  "${ROOT_DIR}/.github/dependabot.yml" \
  "${ROOT_DIR}/.github/workflows"

"${ROOT_DIR}/scripts/verify-build-input-manifest.sh"
"${ROOT_DIR}/scripts/verify-control-plane-manifest.sh"

doc_files=()
while IFS= read -r -d '' file; do
  doc_files+=("${file}")
done < <(find "${ROOT_DIR}" \
  -path "${ROOT_DIR}/.git" -prune -o \
  -path "${ROOT_DIR}/dist" -prune -o \
  -path "${ROOT_DIR}/tmp" -prune -o \
  -path "${ROOT_DIR}/.venv" -prune -o \
  -path "${ROOT_DIR}/runtime/container/providers/node_modules" -prune -o \
  -path "${ROOT_DIR}/runtime/container/rust/vendor" -prune -o \
  -path "${ROOT_DIR}/runtime/container/rust/target" -prune -o \
  -type f \( -name '*.md' -o -name '*.txt' -o -name '*.1' \) -print0 | sort -z)

if command -v codespell >/dev/null 2>&1; then
  codespell --config "${ROOT_DIR}/.codespellrc" "${doc_files[@]}"
else
  echo "Skipping spelling checks because codespell is not installed locally." >&2
fi

validate_manpage

# Check A: docs/examples/ must exist and be non-empty
if [[ ! -d "${ROOT_DIR}/docs/examples" ]] || ! find "${ROOT_DIR}/docs/examples" -type f -print -quit | grep -q .; then
  echo "docs/examples/ must exist and be non-empty" >&2
  exit 1
fi

# Check B: TOML validation for docs/examples/ is already covered by the
# validate-toml pass above, which scans the repository tree.

# Check C: Credential pattern scan in tests/ and docs/examples/
run_metadatautil scan-credential-patterns "${ROOT_DIR}"

if branding_scan; then
  echo "Found stale pre-rename branding." >&2
  exit 1
fi

if [[ -e "${ROOT_DIR}/.workcell.remote.local" ]]; then
  echo "Legacy repo-local remote builder config must not exist: ${ROOT_DIR}/.workcell.remote.local" >&2
  exit 1
fi

(
  cd "${ROOT_DIR}/runtime/container/rust"
  cargo fmt --all --check
  cargo clippy --all-targets --locked --offline -- -D warnings
  cargo test --locked --offline
)

"${ROOT_DIR}/scripts/run-mutation-tests.sh"
"${ROOT_DIR}/scripts/verify-coverage.sh"

# Check E: Scenario coverage and control-plane parity
"${ROOT_DIR}/scripts/run-scenario-tests.sh" --secretless-only
"${ROOT_DIR}/scripts/verify-scenario-coverage.sh"
"${ROOT_DIR}/scripts/verify-control-plane-parity.sh"

echo "Workcell repository validation passed."
