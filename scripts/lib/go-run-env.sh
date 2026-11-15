#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/lib/go-run-env.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}

default_go_cache_root() {
  if [[ -n "${WORKCELL_GO_CACHE_ROOT:-}" ]]; then
    printf '%s\n' "${WORKCELL_GO_CACHE_ROOT}"
    return 0
  fi
  if [[ -n "${HOME:-}" ]]; then
    case "$(uname -s 2>/dev/null || true)" in
      Darwin)
        printf '%s\n' "${HOME}/Library/Caches/workcell/go"
        return 0
        ;;
    esac
  fi
  if [[ -n "${XDG_CACHE_HOME:-}" ]]; then
    printf '%s\n' "${XDG_CACHE_HOME}/workcell/go"
    return 0
  fi
  if [[ -n "${HOME:-}" ]]; then
    printf '%s\n' "${HOME}/.cache/workcell/go"
    return 0
  fi
  local user_suffix="unknown"
  user_suffix="$(id -u 2>/dev/null || printf '%s' "unknown")"
  printf '%s\n' "${TMPDIR:-/tmp}/workcell-go-${user_suffix}"
}

# The cache lock (issue 812). workcell --gc removes a Workcell cache root, a
# directory named workcell under a cache home, so a writer and gc share a
# flock(2) lock on the file beside that root, ROOT.lock, which gc never
# removes. A writer opens the lock file on a descriptor of its own shell and
# takes a shared lock on it before it creates anything under the root. Every
# child of the shell inherits the descriptor, and flock(2) locks the open
# file, so the lock lasts until the shell and every child that keeps the
# descriptor, such as a go build left running, exit. gc takes an exclusive
# lock without waiting, skips a root it cannot lock, and holds the lock while
# it removes the root. scripts/lib/workcell-cache-lock.pl checks the file and
# takes the lock, since macOS has no flock(1). The memo below lists the locked
# paths and their descriptors. It is per shell and never exported: a new
# process takes its own lock, and a subshell uses its parent's. Sourcing this
# file again in the same shell keeps the memo, so release_all still finds
# every descriptor. A memo that is not a plain scalar of this shell, such as
# one from the environment, is reset. The first declare -p runs in the if
# condition, where bash ignores its status, so an unset memo trips no ERR
# trap of the caller: bash 3.2 runs an inherited ERR trap inside a command
# substitution even under an if.
if ! declare -p WORKCELL_CACHE_LOCK_FDS >/dev/null 2>&1 ||
  [[ "$(declare -p WORKCELL_CACHE_LOCK_FDS 2>/dev/null)" != "declare -- WORKCELL_CACHE_LOCK_FDS="* ]]; then
  unset WORKCELL_CACHE_LOCKS_SEEN WORKCELL_CACHE_LOCK_FDS
  WORKCELL_CACHE_LOCKS_SEEN=""
  WORKCELL_CACHE_LOCK_FDS=""
fi

# workcell_cache_lock_normal PATH prints PATH as an absolute path without
# empty or . components and without a trailing slash, so XDG_CACHE_HOME=~/.cache/
# and ~/.cache//workcell match. A relative PATH is taken from the working
# directory, where mkdir would create it.
workcell_cache_lock_normal() {
  local path="$1"

  [[ "${path}" == /* ]] || path="${PWD%/}/${path}"
  while [[ "${path}" == *//* ]]; do
    path="${path%%//*}/${path#*//}"
  done
  while [[ "${path}" == */./* ]]; do
    path="${path%%/./*}/${path#*/./}"
  done
  while [[ "${path}" == */. ]]; do
    path="${path%/.}"
  done
  while [[ "${path}" == */ && "${path}" != / ]]; do
    path="${path%/}"
  done
  printf '%s\n' "${path:-/}"
}

# workcell_cache_lock_physical PATH prints normal PATH with its longest
# existing prefix resolved through symlinks. The rest is taken as written,
# with .. dropping the component before it, since a missing directory is no
# symlink. It fails when the existing prefix cannot be resolved.
workcell_cache_lock_physical() {
  local head="$1"
  local rest=""
  local part=""
  local out=""

  while [[ "${head}" != / && ! -d "${head}" ]]; do
    rest="/${head##*/}${rest}"
    head="${head%/*}"
    head="${head:-/}"
  done
  out="$(CDPATH='' cd -P -- "${head}" 2>/dev/null && pwd -P)" || return 1
  out="${out%/}"
  while [[ -n "${rest}" ]]; do
    rest="${rest#/}"
    part="${rest%%/*}"
    if [[ "${rest}" == */* ]]; then
      rest="/${rest#*/}"
    else
      rest=""
    fi
    case "${part}" in
      "" | .) ;;
      ..) out="${out%/*}" ;;
      *) out="${out}/${part}" ;;
    esac
  done
  printf '%s\n' "${out:-/}"
}

# workcell_cache_lock_match PATH [HOME...] prints the lock file of the
# Workcell cache root in normal PATH, the root's path plus .lock. A cache root
# is a directory named workcell in a cache home: a directory named .cache or
# Caches, or one of the HOMEs. It fails for a PATH in no such root.
workcell_cache_lock_match() {
  local dir="$1"
  local parent=""
  local home=""
  shift

  while [[ -n "${dir}" && "${dir}" != / ]]; do
    parent="${dir%/*}"
    if [[ "${dir##*/}" == workcell ]]; then
      if [[ "${parent##*/}" == .cache || "${parent##*/}" == Caches ]]; then
        printf '%s.lock\n' "${dir}"
        return 0
      fi
      for home in "$@"; do
        if [[ -n "${home}" && "${parent:-/}" == "${home}" ]]; then
          printf '%s.lock\n' "${dir}"
          return 0
        fi
      done
    fi
    dir="${parent}"
  done
  return 1
}

# workcell_cache_lock_path DIR prints the lock file of the Workcell cache root
# that holds DIR. These are the roots that workcell --gc removes, however the
# home is spelled: a spelling with .. or through a symlinked alias of a cache
# home is compared by its resolved path, since perl and gc meet the same lock
# file through any spelling. It prints nothing for a DIR in no such root, and
# returns 2 when it cannot resolve DIR to tell.
workcell_cache_lock_path() {
  local dir=""
  local xdg=""
  local physical=""
  local home=""
  local -a homes=()

  dir="$(workcell_cache_lock_normal "$1")"
  if [[ -n "${XDG_CACHE_HOME:-}" ]]; then
    xdg="$(workcell_cache_lock_normal "${XDG_CACHE_HOME}")"
  fi
  if [[ "${dir}/" != */../* ]] && workcell_cache_lock_match "${dir}" "${xdg}"; then
    return 0
  fi
  physical="$(workcell_cache_lock_physical "${dir}")" || return 2
  for home in "${xdg}" "${HOME:+${HOME%/}/.cache}" "${HOME:+${HOME%/}/Library/Caches}"; do
    [[ -n "${home}" && -d "${home}" ]] || continue
    # A home that cannot be resolved cannot hold DIR's root, so skip it.
    home="$(workcell_cache_lock_physical "$(workcell_cache_lock_normal "${home}")")" || continue
    homes+=("${home}")
  done
  workcell_cache_lock_match "${physical}" "${xdg}" ${homes[@]+"${homes[@]}"} || true
}

# workcell_cache_lock_available succeeds when this host can run the lock
# helper. Without /usr/bin/perl or the helper, as in a partial copy of this
# library, no run can lock, so that is a lasting refusal (69).
workcell_cache_lock_available() {
  [[ -x /usr/bin/perl && -r "${GO_RUN_ENV_REPO_ROOT}/scripts/lib/workcell-cache-lock.pl" ]]
}

# workcell_cache_lock_perl runs the lock helper with a clean environment, so
# no PERL5OPT or PERL5LIB in the caller's environment loads code into it.
workcell_cache_lock_perl() {
  /usr/bin/env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin \
    /usr/bin/perl "${GO_RUN_ENV_REPO_ROOT}/scripts/lib/workcell-cache-lock.pl" "$@"
}

# workcell_cache_lock_take_shared LOCK opens LOCK on the first free
# descriptor from 20 up, which no Workcell script uses, and takes the shared
# lock on it. It returns the helper's status, such as 73 when the lock
# directory cannot be made, 69 without the helper, and 71 when no
# descriptor opens.
workcell_cache_lock_take_shared() {
  local lock="$1"
  local target=""
  local fd=20
  local rc=0

  workcell_cache_lock_available || return 69
  # prepare creates a missing cache home owner-only, since the helper refuses
  # one that other users can change, and a root run gives it to the user.
  target="$(workcell_cache_lock_perl prepare "${lock}")" || return "$?"
  [[ "${target}" == /* && "${target}" != *$'\n'* ]] || return 64
  while [[ -e "/dev/fd/${fd}" ]]; do
    fd=$((fd + 1))
    [[ "${fd}" -lt 100 ]] || return 71
  done
  # Bash 3.2, the macOS /bin/bash, has no {fd} redirection, so name it.
  eval "exec ${fd}<\"\${target}\"" || return 71
  workcell_cache_lock_perl lock-shared "${fd}" "${target}" || rc=$?
  if [[ "${rc}" -ne 0 ]]; then
    eval "exec ${fd}<&-"
    return "${rc}"
  fi
  WORKCELL_CACHE_LOCK_FDS+="${fd} "
}

# workcell_cache_lock_shared DIR takes a shared lock on the Workcell cache
# root that holds DIR for this shell and the children it starts. A lasting
# refusal, 69 (no flock(2) here) or 77 (an unsafe lock file), lets the run go
# on unlocked after one warning. That is safe: gc cannot lock that root
# either, so it keeps it. Any other failure, such as a failed fork, EMFILE or
# a full disk, is tried three times and then returns 1, since gc could lock
# the root later and remove it while this run writes it.
# A DIR that cannot be resolved to tell whether it is in a cache root also
# goes on after a warning. The memo keeps each DIR and lock to one try.
workcell_cache_lock_shared() {
  local lock=""
  local rc=0
  local tries=0

  case $'\n'"${WORKCELL_CACHE_LOCKS_SEEN}" in
    *$'\n'"dir:$1"$'\n'*) return 0 ;;
  esac
  lock="$(workcell_cache_lock_path "$1")" || rc=$?
  if [[ "${rc}" -ne 0 ]]; then
    echo "Workcell warning: cannot resolve $1 to find its Workcell cache root; it is in use without the cache lock" >&2
    WORKCELL_CACHE_LOCKS_SEEN+="dir:$1"$'\n'
    return 0
  fi
  if [[ -z "${lock}" ]]; then
    WORKCELL_CACHE_LOCKS_SEEN+="dir:$1"$'\n'
    return 0
  fi
  case $'\n'"${WORKCELL_CACHE_LOCKS_SEEN}" in
    *$'\n'"${lock}"$'\n'*) return 0 ;;
  esac
  while :; do
    rc=0
    workcell_cache_lock_take_shared "${lock}" || rc=$?
    case "${rc}" in
      0 | 69 | 77) break ;;
    esac
    tries=$((tries + 1))
    if [[ "${tries}" -ge 3 ]]; then
      echo "Workcell error: cannot take the cache lock on ${lock%.lock} (status ${rc}); stopping so workcell --gc cannot remove it in use" >&2
      return 1
    fi
    sleep 0.2
  done
  if [[ "${rc}" -ne 0 ]]; then
    echo "Workcell warning: ${lock%.lock} is in use without the cache lock; workcell --gc keeps it" >&2
  fi
  WORKCELL_CACHE_LOCKS_SEEN+="${lock}"$'\n'"dir:$1"$'\n'
}

# workcell_cache_lock_release_all closes this shell's lock descriptors, so
# workcell --gc can lock the roots that only it was using; a child that still
# runs keeps its copy and its lock. In a subshell it closes only that
# subshell's copies: run it there before starting a helper that outlives the
# run, such as a Colima VM, so the helper never holds the lock.
workcell_cache_lock_release_all() {
  local fd=""

  for fd in ${WORKCELL_CACHE_LOCK_FDS}; do
    [[ "${fd}" =~ ^[0-9]+$ ]] || continue
    eval "exec ${fd}<&-"
  done
  WORKCELL_CACHE_LOCKS_SEEN=""
  WORKCELL_CACHE_LOCK_FDS=""
}

# workcell_cache_lock_run_exclusive LOCK COMMAND [ARG...] runs COMMAND under
# an exclusive lock on LOCK. It returns 75 at once when a writer holds the
# lock or the lock file changed, 64, 69, 71, 73 or 77 when the lock cannot be
# taken safely, and COMMAND's status otherwise.
workcell_cache_lock_run_exclusive() {
  workcell_cache_lock_available || return 69
  workcell_cache_lock_perl run-exclusive "$@"
}

ensure_go_run_env() {
  local cache_root
  cache_root="$(default_go_cache_root)"
  local gopath="${GOPATH:-${cache_root}/gopath}"
  local gomodcache="${GOMODCACHE:-${cache_root}/mod-cache}"
  local gocache="${GOCACHE:-${cache_root}/build-cache}"
  local lock_dir=""

  # Lock before creating anything, so gc never removes a root as it starts.
  for lock_dir in "${cache_root}" "${gopath}" "${gomodcache}" "${gocache}"; do
    workcell_cache_lock_shared "${lock_dir}" || return 1
  done
  mkdir -p "${cache_root}"
  mkdir -p "${gopath}" "${gomodcache}" "${gocache}"
  chmod 0700 "${cache_root}" 2>/dev/null || true
  export GOPATH="${gopath}"
  export GOMODCACHE="${gomodcache}"
  export GOCACHE="${gocache}"
  export WORKCELL_GO_CACHE_ROOT="${cache_root}"
}

# The repository that holds this library; its content never supplies go.
GO_RUN_ENV_REPO_ROOT="${workcell_entry_root}"

# go_bin_path_trusted mirrors publish-pr's IsTrustedHostToolPath for go: the
# path is absolute, outside this repository and the caller's repo root, and,
# unless allow_any_prefix is 1, under a host-tool or Go-install prefix. The
# host-tool prefixes are the scripts/workcell is_trusted_host_tool_path list
# (internal/publishpr trustedHostToolPrefixes in Go) that hold go; the Go
# prefixes add the official, distro, and hosted-runner install roots.
go_bin_path_trusted() {
  local candidate="$1"
  local repo_root="$2"
  local allow_any_prefix="$3"
  local root=""

  [[ "${candidate}" == /* ]] || return 1
  for root in "${GO_RUN_ENV_REPO_ROOT}" "${repo_root}"; do
    [[ -n "${root}" ]] || continue
    case "${candidate}" in
      "${root}" | "${root}"/*) return 1 ;;
    esac
  done
  [[ "${allow_any_prefix}" == "1" ]] && return 0
  case "${candidate}" in
    /usr/bin/* | /bin/* | /usr/local/bin/* | /usr/local/go/* | \
      /usr/lib/go/* | /usr/lib/golang/* | /usr/lib/go-[0-9]*/* | \
      /opt/homebrew/bin/* | /opt/homebrew/Cellar/* | /usr/local/Cellar/* | \
      /home/linuxbrew/.linuxbrew/bin/* | /home/linuxbrew/.linuxbrew/Cellar/* | \
      /opt/hostedtoolcache/go/* | /Users/runner/hostedtoolcache/go/*)
      return 0
      ;;
  esac
  return 1
}

# go_bin_storage_private succeeds when no other user can replace the go at
# canonical path PATH: the file and each parent belong to root or this user,
# and none is group- or other-writable unless it is a root-owned sticky
# directory. find runs from a fixed system path; a failed find is a refusal.
#
# Language-boundary justification: the callers run go by its canonical path,
# so a swap between this check and the exec still runs the new file. Bash has
# no fexecve, and exec through /dev/fd breaks go, which finds GOROOT from its
# own path. Threat model: WORKCELL_GO_BIN may point outside the trusted
# prefixes, into a home or temporary directory. This check refuses storage
# that another user can write, so only root or this user can swap the go.
# Residual risk: a process of this user can still swap it; that process can
# already change this repository and the caller's environment.
go_bin_storage_private() {
  local path="$1"
  local find_bin=""
  local shared=""

  for find_bin in /usr/bin/find /bin/find; do
    [[ -x "${find_bin}" ]] && break
  done
  [[ -x "${find_bin}" ]] || return 1
  while :; do
    shared="$("${find_bin}" "${path}" -prune \( \( ! -user 0 ! -user "${EUID}" \) -o \
      \( \( -perm -0020 -o -perm -0002 \) ! \( -type d -user 0 -perm -1000 \) \) \) -print)" || return 1
    [[ -z "${shared}" ]] || return 1
    [[ "${path}" != / ]] || return 0
    path="${path%/*}"
    path="${path:-/}"
  done
}

# accept_go_bin prints the canonical go when both the raw and the canonical
# path pass go_bin_path_trusted. realpath runs from a fixed system path, and a
# failed canonicalization is a refusal. A go outside the trusted prefixes
# (allow_any_prefix 1) must also pass go_bin_storage_private.
accept_go_bin() {
  local candidate="$1"
  local repo_root="$2"
  local allow_any_prefix="$3"
  local realpath_bin=""
  local canonical=""
  local root_canonical=""

  [[ -n "${candidate}" && -f "${candidate}" && -x "${candidate}" ]] || return 1
  for realpath_bin in /usr/bin/realpath /bin/realpath; do
    [[ -x "${realpath_bin}" ]] && break
  done
  [[ -x "${realpath_bin}" ]] || return 1
  canonical="$("${realpath_bin}" "${candidate}" 2>/dev/null)" || return 1
  if [[ -n "${repo_root}" ]]; then
    root_canonical="$("${realpath_bin}" "${repo_root}" 2>/dev/null)" || return 1
  fi
  go_bin_path_trusted "${candidate}" "${repo_root}" "${allow_any_prefix}" &&
    go_bin_path_trusted "${canonical}" "${root_canonical}" "${allow_any_prefix}" ||
    return 1
  if [[ "${allow_any_prefix}" == "1" ]]; then
    go_bin_storage_private "${canonical}" || return 1
  fi
  printf '%s\n' "${canonical}"
}

# resolve_go_bin [REPO_ROOT] resolves go the way publish-pr resolves its host
# tools, so a workspace-planted go never runs with the caller's environment.
# WORKCELL_GO_BIN is the operator's explicit choice: it skips the prefix list
# (toolchain managers and test wrappers live elsewhere) but never the
# repository exclusion. Otherwise the first go on PATH, then the fixed
# candidates, must sit under a trusted prefix.
resolve_go_bin() {
  local repo_root="${1:-}"
  local candidate=""

  if [[ -n "${WORKCELL_GO_BIN:-}" && -x "${WORKCELL_GO_BIN}" ]]; then
    if ! accept_go_bin "${WORKCELL_GO_BIN}" "${repo_root}" 1; then
      echo "WORKCELL_GO_BIN must be an absolute go outside the repository that only root or this user can replace: ${WORKCELL_GO_BIN}" >&2
      exit 1
    fi
    return 0
  fi

  for candidate in \
    "$(command -v go 2>/dev/null || true)" \
    /opt/homebrew/bin/go \
    /usr/local/go/bin/go \
    /usr/local/bin/go \
    /usr/bin/go; do
    accept_go_bin "${candidate}" "${repo_root}" 0 && return 0
  done
  echo "Missing trusted host tool: go (set WORKCELL_GO_BIN to an absolute go outside the repository)" >&2
  exit 1
}

# resolve_exact_toolchain_go_bin ROOT prints a go that runs the exact toolchain
# ROOT/go.mod pins. Each candidate must pass accept_go_bin, the storage rule
# WORKCELL_GO_BIN must pass, so the resolver never hands on a go that the
# caller then refuses. A GitHub-hosted runner's tool cache sits under a
# world-writable /opt and never passes; scripts/ci/stage-trusted-go.sh stages
# the toolchain at a root-owned /usr/local/go there.
resolve_exact_toolchain_go_bin() {
  local repo_root="$1"
  local expected_toolchain=""
  local actual_toolchain=""
  local candidate=""
  local accepted=""
  local goroot=""
  local switched=""
  expected_toolchain="$(/usr/bin/awk '$1 == "toolchain" { print $2; exit }' "${repo_root}/go.mod")"
  [[ "${expected_toolchain}" =~ ^go[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
  for candidate in /opt/homebrew/bin/go /usr/local/go/bin/go /usr/local/bin/go /usr/bin/go; do
    accepted="$(accept_go_bin "${candidate}" "${repo_root}" 1)" || continue
    # Probe the toolchain the build will actually run under, not the version
    # this candidate happens to be, through the checksum-verified toolchain
    # switch every other lane uses. Callers then build with GOTOOLCHAIN=local,
    # so hand on the switched toolchain's own go, under the same storage rule.
    if ! goroot="$(GOTOOLCHAIN="${expected_toolchain}" "${accepted}" env GOROOT 2>/dev/null)"; then
      continue
    fi
    switched="$(accept_go_bin "${goroot}/bin/go" "${repo_root}" 1)" || continue
    if ! actual_toolchain="$(GOTOOLCHAIN=local "${switched}" env GOVERSION 2>/dev/null)"; then
      continue
    fi
    if [[ "${actual_toolchain}" == "${expected_toolchain}" ]]; then
      printf '%s\n' "${switched}"
      return 0
    fi
  done
  return 1
}

run_go_in_repo() {
  local repo_root="$1"
  shift

  ensure_go_run_env || return 1
  local go_bin
  go_bin="$(resolve_go_bin "${repo_root}")"
  (
    cd "${repo_root}" &&
      "${go_bin}" "$@"
  )
}

exec_go_run_in_repo() {
  local repo_root="$1"
  shift

  ensure_go_run_env || exit 1
  local go_bin
  go_bin="$(resolve_go_bin "${repo_root}")"
  cd "${repo_root}" || exit 1
  exec "${go_bin}" run "$@"
}

build_go_tool_in_repo() {
  local repo_root="$1"
  local output_path="$2"
  shift 2

  ensure_go_run_env || return 1
  local go_bin
  go_bin="$(resolve_go_bin "${repo_root}")"
  (
    cd "${repo_root}" &&
      "${go_bin}" build -buildvcs=false -o "${output_path}" "$@"
  )
}
