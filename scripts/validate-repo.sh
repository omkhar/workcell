#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SKIP_HEAVY_HOST_SHELLCHECK="${WORKCELL_SKIP_HEAVY_HOST_SHELLCHECK:-0}"
VALIDATION_PROFILE="${WORKCELL_VALIDATE_REPO_PROFILE:-release-preflight}"

HOME="${HOME:-/tmp/workcell-home}"
XDG_CACHE_HOME="${XDG_CACHE_HOME:-${HOME}/.cache}"
GOCACHE="${GOCACHE:-${XDG_CACHE_HOME}/go-build}"
GOMODCACHE="${GOMODCACHE:-${XDG_CACHE_HOME}/go-mod}"
CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-${XDG_CACHE_HOME}/cargo-target}"
TMPDIR="${TMPDIR:-${HOME}/.tmp}"
export HOME XDG_CACHE_HOME GOCACHE GOMODCACHE CARGO_TARGET_DIR TMPDIR
mkdir -p "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"

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
require_tool markdownlint
require_tool yamllint
require_tool cargo
require_tool rustfmt
require_tool git
require_cargo_subcommand clippy

case "${VALIDATION_PROFILE}" in
  repo-core | pr-parity | release-preflight) ;;
  *)
    echo "Unsupported validate-repo profile: ${VALIDATION_PROFILE}" >&2
    exit 2
    ;;
esac

METADATAUTIL_BIN=""
BUILD_CACHE_DIR="${ROOT_DIR}/.workcell-build-cache"

cleanup() {
  if [[ -n "${METADATAUTIL_BIN}" && -e "${METADATAUTIL_BIN}" ]]; then
    rm -f "${METADATAUTIL_BIN}"
  fi
  rm -rf "${BUILD_CACHE_DIR}"
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
  "${ROOT_DIR}/.githooks/pre-commit"
  "${ROOT_DIR}/scripts/bootstrap-dev.sh"
  "${ROOT_DIR}/scripts/check-dead-code.sh"
  "${ROOT_DIR}/scripts/check-public-repo-hygiene.sh"
  "${ROOT_DIR}/scripts/check-pr-shape.sh"
  "${ROOT_DIR}/scripts/check-pinned-inputs.sh"
  "${ROOT_DIR}/scripts/build-and-test.sh"
  "${ROOT_DIR}/scripts/ci-plan.sh"
  "${ROOT_DIR}/scripts/workcell"
  "${ROOT_DIR}/scripts/check-workflows.sh"
  "${ROOT_DIR}/scripts/ci/build-validator-image.sh"
  "${ROOT_DIR}/scripts/ci/job-docs.sh"
  "${ROOT_DIR}/scripts/ci/job-pin-hygiene.sh"
  "${ROOT_DIR}/scripts/ci/job-pr-shape.sh"
  "${ROOT_DIR}/scripts/ci/job-validate.sh"
  "${ROOT_DIR}/scripts/ci/run-docs-in-validator.sh"
  "${ROOT_DIR}/scripts/ci/run-validate-in-validator.sh"
  "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"
  "${ROOT_DIR}/scripts/container-smoke.sh"
  "${ROOT_DIR}/scripts/dev-quick-check.sh"
  "${ROOT_DIR}/scripts/go-port-validate.sh"
  "${ROOT_DIR}/scripts/install-dev-tools.sh"
  "${ROOT_DIR}/scripts/lint-dockerfiles.sh"
  "${ROOT_DIR}/scripts/lib/extract_direct_mounts"
  "${ROOT_DIR}/scripts/lib/go-run-env.sh"
  "${ROOT_DIR}/scripts/lib/trusted-entrypoint.sh"
  "${ROOT_DIR}/scripts/lib/manage_injection_policy"
  "${ROOT_DIR}/scripts/lib/pty_transcript"
  "${ROOT_DIR}/scripts/lib/render_injection_bundle"
  "${ROOT_DIR}/scripts/lib/resolve_credential_sources"
  "${ROOT_DIR}/scripts/lib/scenario_manifest"
  "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
  "${ROOT_DIR}/scripts/generate-control-plane-manifest.sh"
  "${ROOT_DIR}/scripts/generate-builder-environment-manifest.sh"
  "${ROOT_DIR}/scripts/generate-release-checksums.sh"
  "${ROOT_DIR}/scripts/generate-homebrew-formula.sh"
  "${ROOT_DIR}/scripts/generate-build-input-manifest.sh"
  "${ROOT_DIR}/scripts/install.sh"
  "${ROOT_DIR}/scripts/install-workcell.sh"
  "${ROOT_DIR}/scripts/uninstall.sh"
  "${ROOT_DIR}/scripts/pre-merge.sh"
  "${ROOT_DIR}/scripts/provider-e2e.sh"
  "${ROOT_DIR}/scripts/publish-github-release.sh"
  "${ROOT_DIR}/scripts/publish-provider-bump-pr.sh"
  "${ROOT_DIR}/scripts/publish-upstream-refresh-pr.sh"
  "${ROOT_DIR}/scripts/run-hosted-controls-audit.sh"
  "${ROOT_DIR}/scripts/run-mutation-tests.sh"
  "${ROOT_DIR}/scripts/update-upstream-pins.sh"
  "${ROOT_DIR}/scripts/update-provider-pins.sh"
  "${ROOT_DIR}/scripts/verify-coverage.sh"
  "${ROOT_DIR}/scripts/verify-github-hosted-controls.sh"
  "${ROOT_DIR}/scripts/validate-repo.sh"
  "${ROOT_DIR}/scripts/verify-build-input-manifest.sh"
  "${ROOT_DIR}/scripts/verify-upstream-claude-release.sh"
  "${ROOT_DIR}/scripts/verify-control-plane-manifest.sh"
  "${ROOT_DIR}/scripts/verify-github-macos-release-test-runners.sh"
  "${ROOT_DIR}/scripts/verify-release-bundle.sh"
  "${ROOT_DIR}/scripts/verify-invariants.sh"
  "${ROOT_DIR}/scripts/verify-operator-contract.sh"
  "${ROOT_DIR}/scripts/verify-workflow-lanes.sh"
  "${ROOT_DIR}/scripts/verify-requirements-coverage.sh"
  "${ROOT_DIR}/scripts/verify-reproducible-build.sh"
  "${ROOT_DIR}/scripts/verify-upstream-codex-release.sh"
  "${ROOT_DIR}/scripts/verify-upstream-gemini-release.sh"
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
"${ROOT_DIR}/scripts/check-dead-code.sh"
"${ROOT_DIR}/scripts/check-public-repo-hygiene.sh"

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
"${ROOT_DIR}/scripts/verify-operator-contract.sh"
"${ROOT_DIR}/scripts/verify-requirements-coverage.sh"

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

markdown_files=()
while IFS= read -r -d '' file; do
  markdown_files+=("${file}")
done < <(find "${ROOT_DIR}" \
  -path "${ROOT_DIR}/.git" -prune -o \
  -path "${ROOT_DIR}/dist" -prune -o \
  -path "${ROOT_DIR}/tmp" -prune -o \
  -path "${ROOT_DIR}/.venv" -prune -o \
  -path "${ROOT_DIR}/runtime/container/providers/node_modules" -prune -o \
  -path "${ROOT_DIR}/runtime/container/rust/vendor" -prune -o \
  -path "${ROOT_DIR}/runtime/container/rust/target" -prune -o \
  -type f -name '*.md' -print0 | sort -z)

if command -v codespell >/dev/null 2>&1; then
  codespell --config "${ROOT_DIR}/.codespellrc" "${doc_files[@]}"
else
  echo "Skipping spelling checks because codespell is not installed locally." >&2
fi

if [[ "${#markdown_files[@]}" -gt 0 ]]; then
  markdownlint "${markdown_files[@]}"
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

(
  cd "${ROOT_DIR}/runtime/container/rust"
  cargo fmt --all --check
  cargo clippy --all-targets --locked --offline -- -D warnings
  cargo test --locked --offline
)

if [[ "${VALIDATION_PROFILE}" == "release-preflight" ]]; then
  "${ROOT_DIR}/scripts/run-mutation-tests.sh"
  "${ROOT_DIR}/scripts/verify-coverage.sh"
fi

# Pre-build hostutil so scenario tests skip `go run` overhead on every invocation
mkdir -p "${BUILD_CACHE_DIR}"
(cd "${ROOT_DIR}" && go build -buildvcs=false -o "${BUILD_CACHE_DIR}/hostutil" ./cmd/workcell-hostutil)

# Check E: deterministic repo-required scenarios plus control-plane parity
"${ROOT_DIR}/scripts/run-scenario-tests.sh" --repo-required
"${ROOT_DIR}/scripts/verify-scenario-coverage.sh"
"${ROOT_DIR}/scripts/verify-control-plane-parity.sh"

echo "Workcell repository validation passed."
