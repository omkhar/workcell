#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

expected_toolchain="$(awk '$1 == "toolchain" { print $2; exit }' "${ROOT_DIR}/go.mod")"
if [[ ! "${expected_toolchain}" =~ ^go[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "go.mod must declare an exact Go toolchain" >&2
  exit 1
fi

export GOENV=off
export GOFLAGS=
export GOWORK=off
export GOTOOLCHAIN=local

ambient_go_bin="$(command -v go 2>/dev/null || true)"
go_bin=""
if [[ -n "${ambient_go_bin}" ]] && [[ "$("${ambient_go_bin}" env GOVERSION)" == "${expected_toolchain}" ]]; then
  go_bin="${ambient_go_bin}"
fi
if [[ -z "${go_bin}" ]]; then
  toolcache_bin=""
  case "$(uname -m)" in
    arm64 | aarch64)
      toolcache_arch="arm64"
      ;;
    x86_64 | amd64)
      toolcache_arch="x64"
      ;;
    *)
      echo "Unsupported Go toolcache architecture: $(uname -m)" >&2
      exit 1
      ;;
  esac
  toolcache_root="${RUNNER_TOOL_CACHE:-${AGENT_TOOLSDIRECTORY:-}}"
  if [[ -n "${toolcache_root}" && -n "${toolcache_arch}" ]]; then
    toolcache_bin="${toolcache_root%/}/go/${expected_toolchain#go}/${toolcache_arch}/bin/go"
  fi
  if [[ -x "${toolcache_bin}" ]]; then
    go_bin="${toolcache_bin}"
  fi
fi
if [[ ! -x "${go_bin}" ]]; then
  if [[ -x "${ambient_go_bin}" ]]; then
    go_bin="${ambient_go_bin}"
    export GOTOOLCHAIN="${expected_toolchain}"
  else
    echo "Go toolchain ${expected_toolchain} is unavailable" >&2
    exit 1
  fi
fi

actual_toolchain="$("${go_bin}" env GOVERSION)"
if [[ "${actual_toolchain}" != "${expected_toolchain}" ]]; then
  echo "Go toolchain ${actual_toolchain} does not match ${expected_toolchain}" >&2
  exit 1
fi
if [[ "$("${go_bin}" env GOOS)" != "darwin" ]]; then
  echo "Release asset ACL proof requires Darwin" >&2
  exit 1
fi

cd "${ROOT_DIR}"
"${go_bin}" test ./internal/host/release -run '^TestReleaseAssetACLDarwin$'
