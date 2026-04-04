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
require_tool go
require_tool gofmt
require_tool cargo
require_tool rustfmt
require_cargo_subcommand clippy

shell_files=(
  "${ROOT_DIR}/scripts/dev-quick-check.sh"
  "${ROOT_DIR}/scripts/go-port-validate.sh"
  "${ROOT_DIR}/scripts/lint-dockerfiles.sh"
  "${ROOT_DIR}/scripts/workcell"
  "${ROOT_DIR}/scripts/dev-remote-validate.sh"
  "${ROOT_DIR}/scripts/lib/extract_direct_mounts"
  "${ROOT_DIR}/scripts/lib/manage_injection_policy"
  "${ROOT_DIR}/scripts/lib/pty_transcript"
  "${ROOT_DIR}/scripts/lib/render_injection_bundle"
  "${ROOT_DIR}/scripts/lib/resolve_credential_sources"
  "${ROOT_DIR}/scripts/lib/scenario_manifest"
  "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
  "${ROOT_DIR}/scripts/verify-go-python-parity.sh"
  "${ROOT_DIR}/runtime/container/bin/apt-helper.sh"
  "${ROOT_DIR}/runtime/container/bin/apt-wrapper.sh"
  "${ROOT_DIR}/runtime/container/home-control-plane.sh"
  "${ROOT_DIR}/runtime/container/provider-wrapper.sh"
  "${ROOT_DIR}/runtime/container/runtime-user.sh"
)

while IFS= read -r file; do
  shell_files+=("${file}")
done < <(find "${ROOT_DIR}/tests/scenarios" -type f -name 'test-*.sh' -print | sort)

shellcheck -x "${shell_files[@]}"
shfmt -ln=bash -i 2 -ci -d "${shell_files[@]}"
"${ROOT_DIR}/scripts/lint-dockerfiles.sh"
go_files=()
while IFS= read -r -d '' item; do
  go_files+=("${item}")
done < <(find "${ROOT_DIR}/cmd" "${ROOT_DIR}/internal" -type f -name '*.go' -print0 | sort -z)
if [[ "${#go_files[@]}" -gt 0 ]]; then
  if gofmt -l "${go_files[@]}" | grep -q .; then
    echo "Go files are not formatted with gofmt." >&2
    exit 1
  fi
fi
go vet ./...
go test ./...

(
  cd "${ROOT_DIR}/runtime/container/rust"
  cargo fmt --all --check
  cargo clippy --all-targets --locked --offline -- -D warnings
  cargo test --locked --offline
)

echo "Workcell quick validation passed."
