#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

toolchain_version_is_newer() {
  local left="$1"
  local right="$2"
  local left_major left_minor left_patch
  local right_major right_minor right_patch

  IFS=. read -r left_major left_minor left_patch <<<"${left}"
  IFS=. read -r right_major right_minor right_patch <<<"${right}"
  if ((10#${left_major} != 10#${right_major})); then
    ((10#${left_major} > 10#${right_major}))
  elif ((10#${left_minor} != 10#${right_minor})); then
    ((10#${left_minor} > 10#${right_minor}))
  else
    ((10#${left_patch} > 10#${right_patch}))
  fi
}

expected_toolchain="$(awk '$1 == "toolchain" { print $2; exit }' "${ROOT_DIR}/go.mod")"
if [[ ! "${expected_toolchain}" =~ ^go[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "go.mod must declare an exact Go toolchain" >&2
  exit 1
fi

probe_go_toolchain() {
  local candidate="$1"
  local requested_toolchain="$2"
  local actual_toolchain=""

  if actual_toolchain="$({
    GOENV=off GOFLAGS='' GOWORK=off GOTOOLCHAIN="${requested_toolchain}" \
      "${candidate}" env GOVERSION 2>/dev/null
  })"; then
    [[ "${actual_toolchain}" == "${expected_toolchain}" ]]
  else
    return 1
  fi
}

export GOENV=off
export GOFLAGS=
export GOWORK=off
export GOTOOLCHAIN=local

ambient_go_bin="$(command -v go 2>/dev/null || true)"
go_bin=""
toolcache_bin=""
toolcache_arch=""
toolcache_root=""

if [[ -x "${ambient_go_bin}" ]]; then
  if probe_go_toolchain "${ambient_go_bin}" "${GOTOOLCHAIN}"; then
    go_bin="${ambient_go_bin}"
    export GOTOOLCHAIN=local
  fi
fi
if [[ -z "${go_bin}" ]]; then
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
    if probe_go_toolchain "${toolcache_bin}" local; then
      go_bin="${toolcache_bin}"
      export GOTOOLCHAIN=local
    fi
  fi
fi
if [[ -z "${go_bin}" && -x "${ambient_go_bin}" ]]; then
  if probe_go_toolchain "${ambient_go_bin}" "${expected_toolchain}"; then
    go_bin="${ambient_go_bin}"
    export GOTOOLCHAIN="${expected_toolchain}"
  fi
fi
if [[ -z "${go_bin}" && -n "${toolcache_root}" ]]; then
  bootstrap_candidates=()
  for candidate in "${toolcache_root%/}"/go/*/"${toolcache_arch}"/bin/go; do
    [[ -x "${candidate}" ]] || continue
    [[ "${candidate}" == "${toolcache_bin}" ]] && continue
    candidate_version="${candidate%/"${toolcache_arch}"/bin/go}"
    candidate_version="${candidate_version##*/}"
    [[ "${candidate_version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || continue

    insert_at="${#bootstrap_candidates[@]}"
    candidate_index=0
    if [[ ${#bootstrap_candidates[@]} -gt 0 ]]; then
      for existing in "${bootstrap_candidates[@]}"; do
        existing_version="${existing%/"${toolcache_arch}"/bin/go}"
        existing_version="${existing_version##*/}"
        if toolchain_version_is_newer "${candidate_version}" "${existing_version}" || {
          [[ "${candidate_version}" == "${existing_version}" ]] && [[ "${candidate}" < "${existing}" ]]
        }; then
          insert_at="${candidate_index}"
          break
        fi
        candidate_index=$((candidate_index + 1))
      done
    fi
    bootstrap_candidates[${#bootstrap_candidates[@]}]="${candidate}"
    candidate_index=${#bootstrap_candidates[@]}
    candidate_index=$((candidate_index - 1))
    while ((candidate_index > insert_at)); do
      bootstrap_candidates[candidate_index]="${bootstrap_candidates[candidate_index - 1]}"
      candidate_index=$((candidate_index - 1))
    done
    bootstrap_candidates[insert_at]="${candidate}"
  done

  bootstrap_attempts=0
  if [[ ${#bootstrap_candidates[@]} -gt 0 ]]; then
    for candidate in "${bootstrap_candidates[@]}"; do
      bootstrap_attempts=$((bootstrap_attempts + 1))
      ((bootstrap_attempts <= 8)) || break
      if probe_go_toolchain "${candidate}" "${expected_toolchain}"; then
        go_bin="${candidate}"
        export GOTOOLCHAIN="${expected_toolchain}"
        break
      fi
    done
  fi
fi
if [[ -z "${go_bin}" ]]; then
  echo "Go toolchain ${expected_toolchain} is unavailable" >&2
  exit 1
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
