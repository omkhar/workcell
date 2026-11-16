#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/ci/job-release-asset-acl.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"

# This bounded Bash bootstrap runs before a Go binary is available, so it cannot use a Go helper.
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

# Each Go binary path ends in /go. A command word spelled with that literal /go
# is a path the lane wiring check can read, never a builtin or a function.
probe_go_toolchain() {
  local candidate="$1"
  local requested_toolchain="$2"
  local actual_toolchain=""

  if actual_toolchain="$({
    GOENV=off GOFLAGS='' GOWORK=off GOTOOLCHAIN="${requested_toolchain}" \
      "${candidate%/go}/go" env GOVERSION 2>/dev/null
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
    bootstrap_candidates+=("${candidate}")
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

actual_toolchain="$("${go_bin%/go}/go" env GOVERSION)"
if [[ "${actual_toolchain}" != "${expected_toolchain}" ]]; then
  echo "Go toolchain ${actual_toolchain} does not match ${expected_toolchain}" >&2
  exit 1
fi
if [[ "$("${go_bin%/go}/go" env GOOS)" != "darwin" ]]; then
  echo "Release asset ACL proof requires Darwin" >&2
  exit 1
fi

cd "${ROOT_DIR}"
"${go_bin%/go}/go" test ./internal/host/release -run '^TestReleaseAssetACLDarwin$'
