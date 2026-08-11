#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

expected_toolchain="$(awk '$1 == "toolchain" { print $2; exit }' "${ROOT_DIR}/go.mod")"
if [[ ! "${expected_toolchain}" =~ ^go[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "go.mod must declare an exact Go toolchain" >&2
  exit 1
fi

export GOTOOLCHAIN=local
actual_toolchain="$(go env GOVERSION)"
if [[ "${actual_toolchain}" != "${expected_toolchain}" ]]; then
  echo "Go toolchain ${actual_toolchain} does not match ${expected_toolchain}" >&2
  exit 1
fi
if [[ "$(go env GOOS)" != "darwin" ]]; then
  echo "Release asset ACL proof requires Darwin" >&2
  exit 1
fi

cd "${ROOT_DIR}"
go test ./internal/host/release -run '^TestReleaseAssetACLDarwin$'
