#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

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
  printf '%s\n' "${root}" >"${TMP_DIR}/${name}.root"
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
trap 'kill "${pid_a}" "${pid_b}" 2>/dev/null || true; chmod -R u+w "${TMP_DIR}"; rm -rf "${TMP_DIR}"' EXIT
"${BASH_SOURCE[0]}" --lane a b "${TMP_DIR}" &
pid_a=$!
"${BASH_SOURCE[0]}" --lane b a "${TMP_DIR}" &
pid_b=$!
wait "${pid_a}"
wait "${pid_b}"
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
