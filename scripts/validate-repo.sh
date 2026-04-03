#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

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
require_tool python3
require_tool yamllint
require_tool cargo
require_tool rustfmt
require_tool git
require_cargo_subcommand clippy

mapfile -t python_files < <(
  find "${ROOT_DIR}/scripts/lib" "${ROOT_DIR}/tests/python" "${ROOT_DIR}/tests/mutation" \
    -type f -name '*.py' -print | sort
)

branding_scan() {
  local pattern="agent-boundary|Agent Boundary|agent boundary"
  git -C "${ROOT_DIR}" grep -nE "${pattern}" -- \
    . \
    ':(exclude)scripts/validate-repo.sh' \
    ':(exclude)dist/**' \
    ':(exclude)tmp/**'
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
  "${ROOT_DIR}/scripts/check-pinned-inputs.sh"
  "${ROOT_DIR}/scripts/workcell"
  "${ROOT_DIR}/scripts/check-workflows.sh"
  "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"
  "${ROOT_DIR}/scripts/container-smoke.sh"
  "${ROOT_DIR}/scripts/dev-quick-check.sh"
  "${ROOT_DIR}/scripts/dev-remote-validate.sh"
  "${ROOT_DIR}/scripts/generate-control-plane-manifest.sh"
  "${ROOT_DIR}/scripts/generate-builder-environment-manifest.sh"
  "${ROOT_DIR}/scripts/generate-release-checksums.sh"
  "${ROOT_DIR}/scripts/generate-build-input-manifest.sh"
  "${ROOT_DIR}/scripts/install.sh"
  "${ROOT_DIR}/scripts/uninstall.sh"
  "${ROOT_DIR}/scripts/pre-merge.sh"
  "${ROOT_DIR}/scripts/publish-github-release.sh"
  "${ROOT_DIR}/scripts/run-hosted-controls-audit.sh"
  "${ROOT_DIR}/scripts/run-mutation-tests.sh"
  "${ROOT_DIR}/scripts/verify-coverage.sh"
  "${ROOT_DIR}/scripts/verify-github-hosted-controls.sh"
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

for file in "${shell_files[@]}"; do
  shellcheck -x "${file}"
done
shfmt -ln=bash -i 2 -ci -d "${shell_files[@]}"

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

python3 -m py_compile "${python_files[@]}"
python3 -m unittest discover -s "${ROOT_DIR}/tests/python" -p 'test_*.py'

while IFS= read -r file; do
  python3 -m json.tool "${file}" >/dev/null
done < <(find "${ROOT_DIR}/adapters" "${ROOT_DIR}/.github" "${ROOT_DIR}/runtime/container/providers" "${ROOT_DIR}/tests/scenarios" \
  -path '*/node_modules' -prune -o \
  -type f -name '*.json' -print | sort)

python3 - "${ROOT_DIR}" <<'PY'
import pathlib
import tomllib
import sys

root = pathlib.Path(sys.argv[1])
for path in sorted(root.rglob("*.toml")):
    if ".git" in path.parts:
        continue
    if "node_modules" in path.parts:
        continue
    with path.open("rb") as handle:
        tomllib.load(handle)
PY

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
# root.rglob("*.toml") loop above, which scans all subdirectories.

# Check C: Credential pattern scan in tests/ and docs/examples/
python3 - "${ROOT_DIR}" <<'PY'
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
                print(f"Possible credential in {path}", file=__import__("sys").stderr)
                found += 1
if found:
    raise SystemExit(f"Found {found} possible credential(s) in tests/ or docs/examples/")
PY

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
  cargo check --locked --offline
  cargo test --locked --offline
)

"${ROOT_DIR}/scripts/run-mutation-tests.sh"
"${ROOT_DIR}/scripts/verify-coverage.sh"

# Check E: Scenario coverage and control-plane parity
"${ROOT_DIR}/scripts/run-scenario-tests.sh" --secretless-only
"${ROOT_DIR}/scripts/verify-scenario-coverage.sh"
"${ROOT_DIR}/scripts/verify-control-plane-parity.sh"

echo "Workcell repository validation passed."
