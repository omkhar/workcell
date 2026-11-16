#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/shared/test-lane-owned-roots.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"

# wait_for_file polls for PATH for at most 10 s so a peer that fails before it
# publishes a marker fails this lane instead of hanging the scenario.
wait_for_file() {
  local path="$1" i=0
  until [[ -e "${path}" ]]; do
    ((++i < 200)) || {
      echo "timed out waiting for ${path}" >&2
      return 1
    }
    sleep 0.05
  done
}

# Ownership is the pid of the lane script, so each lane is its own process.
# Two lanes run at once. Each creates its owned root, waits for the other to
# create one, tries to remove the other's root, then removes its own.
lane() {
  local name="$1" peer="$2" root="" other=""
  # shellcheck source=scripts/lib/owned-root.sh
  source "${ROOT_DIR}/scripts/lib/owned-root.sh"
  root="$(workcell_owned_root_create "${TMP_DIR}" "lane-${name}")"
  # Publish the path with a rename so the peer never reads a partial file.
  printf '%s\n' "${root}" >"${TMP_DIR}/${name}.root.tmp"
  mv "${TMP_DIR}/${name}.root.tmp" "${TMP_DIR}/${name}.root"
  wait_for_file "${TMP_DIR}/${peer}.root"
  other="$(cat "${TMP_DIR}/${peer}.root")"
  if workcell_owned_root_remove "${other}" 2>"${TMP_DIR}/${name}.refusal"; then
    echo "lane ${name} removed a root it does not own" >&2
    return 1
  fi
  grep -q "does not own" "${TMP_DIR}/${name}.refusal"
  [[ -d "${other}" ]] || {
    echo "lane ${name} lost the peer root" >&2
    return 1
  }
  : >"${TMP_DIR}/${name}.refused"
  wait_for_file "${TMP_DIR}/${peer}.refused"
  workcell_owned_root_remove "${root}"
}

if [[ "${1:-}" == "--lane" ]]; then
  TMP_DIR="$4"
  lane "$2" "$3"
  exit 0
fi

TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-lane-owned-roots.XXXXXX")"
pid_a="" pid_b=""
# Only an outstanding lane is signaled: a reaped pid is cleared below so a
# reused pid number is never killed, and the trap waits so no lane outlives it.
stop_outstanding_lanes() {
  local pid
  for pid in "${pid_a}" "${pid_b}"; do
    [[ -z "${pid}" ]] || {
      kill "${pid}" 2>/dev/null || true
      wait "${pid}" 2>/dev/null || true
    }
  done
}
trap 'stop_outstanding_lanes; chmod -R u+w "${TMP_DIR}"; rm -rf "${TMP_DIR}"' EXIT
"${workcell_entry_root}/tests/scenarios/shared/test-lane-owned-roots.sh" --lane a b "${TMP_DIR}" &
pid_a=$!
"${workcell_entry_root}/tests/scenarios/shared/test-lane-owned-roots.sh" --lane b a "${TMP_DIR}" &
pid_b=$!
# Clear each pid before its wait status can end the script, so the EXIT trap
# never signals a pid that wait already reaped.
lane_status=0
wait "${pid_a}" || lane_status=$?
pid_a=""
[[ "${lane_status}" -eq 0 ]]
wait "${pid_b}" || lane_status=$?
pid_b=""
[[ "${lane_status}" -eq 0 ]]
for name in a b; do
  [[ ! -e "$(cat "${TMP_DIR}/${name}.root")" ]] || {
    echo "lane ${name} did not remove its own root" >&2
    exit 1
  }
done

# Negative control: an unmarked root and a root marked by another pid are refused.
source "${ROOT_DIR}/scripts/lib/owned-root.sh"
mkdir "${TMP_DIR}/unmarked"
if workcell_owned_root_remove "${TMP_DIR}/unmarked" 2>/dev/null; then
  echo "removed an unmarked root" >&2
  exit 1
fi
[[ -d "${TMP_DIR}/unmarked" ]]
mkdir "${TMP_DIR}/foreign"
printf '1\n' >"${TMP_DIR}/foreign/owner.pid"
if workcell_owned_root_remove "${TMP_DIR}/foreign" 2>/dev/null; then
  echo "removed a foreign-owned root" >&2
  exit 1
fi
[[ -d "${TMP_DIR}/foreign" ]]

# A base with a trailing slash (macOS TMPDIR) must not yield a double slash,
# because launcher output echoes paths verbatim and verify-invariants greps them.
slash_root="$(workcell_owned_root_create "${TMP_DIR}/" slash)"
[[ "${slash_root}" == "${TMP_DIR}"/slash.* ]] || {
  echo "owned root kept a double slash: ${slash_root}" >&2
  exit 1
}
workcell_owned_root_remove "${slash_root}"

echo "lane-owned-roots-ok"
