#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/shared/test-home-control-plane-manifest.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-home-control-plane-manifest.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

source() {
  return 0
}

jq() {
  printf '1' >>"${JQ_COUNT_FILE}"
  printf '%s\n' "$*" >>"${JQ_LOG}"
  command jq "$@"
}

workcell_die() {
  printf '%s\n' "$*" >&2
  return 1
}

mkdir() {
  return 0
}

chmod() {
  return 0
}

cp() {
  printf '%s\t%s\n' "$1" "$2" >>"${SSH_LOG}"
  return 0
}

MANIFEST_ROOT="${TMP_DIR}/manifest-root"
DIRECT_INPUT_ROOT="/opt/workcell/host-inputs"
JQ_LOG="${TMP_DIR}/jq.log"
JQ_COUNT_FILE="${TMP_DIR}/jq.count"
COPIES_LOG="${TMP_DIR}/copies.log"
SSH_LOG="${TMP_DIR}/ssh.log"
MANIFEST_PATH="${MANIFEST_ROOT}/manifest.json"

command mkdir -p "${MANIFEST_ROOT}/ssh"

cat >"${MANIFEST_ROOT}/string-copy.txt" <<'EOF'
string copy source
EOF
cat >"${MANIFEST_ROOT}/object-copy.txt" <<'EOF'
object copy source
EOF
cat >"${MANIFEST_ROOT}/ssh-config.cfg" <<'EOF'
Host example
  User example
EOF
cat >"${MANIFEST_ROOT}/ssh-known_hosts" <<'EOF'
example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIexample
EOF
cat >"${MANIFEST_ROOT}/ssh/id_one" <<'EOF'
identity one
EOF
cat >"${MANIFEST_ROOT}/ssh/id_two" <<'EOF'
identity two
EOF

cat >"${MANIFEST_PATH}" <<EOF
{
  "copies": [
    {
      "source": "string-copy.txt",
      "target": "${TMP_DIR}/injected/string-copy.out",
      "kind": "file",
      "file_mode": "0640",
      "dir_mode": "0750"
    },
    {
      "source": {
        "source": "object-copy.txt"
      },
      "target": "${TMP_DIR}/injected/object-copy.out",
      "kind": "file",
      "file_mode": "0600",
      "dir_mode": "0700"
    },
    {
      "source": {
        "mount_path": "${DIRECT_INPUT_ROOT}/mounted-copy.txt"
      },
      "target": "${TMP_DIR}/injected/mounted-copy.out",
      "kind": "file",
      "file_mode": "0644",
      "dir_mode": "0755"
    }
  ],
  "ssh": {
    "config": {
      "mount_path": "${DIRECT_INPUT_ROOT}/ssh-config.cfg"
    },
    "known_hosts": "ssh-known_hosts",
    "identities": [
      {
        "source": "ssh/id_one",
        "target_name": "id_one"
      },
      {
        "source": "ssh/id_two",
        "target_name": "id_two"
      }
    ]
  }
}
EOF

HOME="${TMP_DIR}/session-home"
# shellcheck disable=SC2034
WORKCELL_INJECTION_MANIFEST="${MANIFEST_PATH}"

# shellcheck source=/dev/null
builtin source "${ROOT_DIR}/runtime/container/home-control-plane.sh"

workcell_target_is_allowed() {
  return 0
}

workcell_prepare_session_directory() {
  return 0
}

workcell_reset_session_target() {
  return 0
}

workcell_validate_direct_mount_path() {
  case "$1" in
    "${DIRECT_INPUT_ROOT}"/*) ;;
    *)
      workcell_die "unexpected direct mount path: $1"
      ;;
  esac
}

workcell_copy_manifest_entry() {
  printf '%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" "$5" >>"${COPIES_LOG}"
  return 0
}

workcell_apply_manifest_copies
workcell_apply_manifest_ssh

expected_copy_string="$(printf '%s\t%s\t%s\t%s\t%s' "${MANIFEST_ROOT}/string-copy.txt" "${TMP_DIR}/injected/string-copy.out" "file" "0640" "0750")"
expected_copy_object="$(printf '%s\t%s\t%s\t%s\t%s' "${MANIFEST_ROOT}/object-copy.txt" "${TMP_DIR}/injected/object-copy.out" "file" "0600" "0700")"
expected_copy_mount="$(printf '%s\t%s\t%s\t%s\t%s' "${DIRECT_INPUT_ROOT}/mounted-copy.txt" "${TMP_DIR}/injected/mounted-copy.out" "file" "0644" "0755")"
expected_ssh_config="$(printf '%s\t%s' "${DIRECT_INPUT_ROOT}/ssh-config.cfg" "${HOME}/.ssh/config")"
expected_ssh_known_hosts="$(printf '%s\t%s' "${MANIFEST_ROOT}/ssh-known_hosts" "${HOME}/.ssh/known_hosts")"
expected_ssh_identity_one="$(printf '%s\t%s' "${MANIFEST_ROOT}/ssh/id_one" "${HOME}/.ssh/id_one")"
expected_ssh_identity_two="$(printf '%s\t%s' "${MANIFEST_ROOT}/ssh/id_two" "${HOME}/.ssh/id_two")"

grep -Fx "${expected_copy_string}" "${COPIES_LOG}" >/dev/null
grep -Fx "${expected_copy_object}" "${COPIES_LOG}" >/dev/null
grep -Fx "${expected_copy_mount}" "${COPIES_LOG}" >/dev/null
grep -Fx "${expected_ssh_config}" "${SSH_LOG}" >/dev/null
grep -Fx "${expected_ssh_known_hosts}" "${SSH_LOG}" >/dev/null
grep -Fx "${expected_ssh_identity_one}" "${SSH_LOG}" >/dev/null
grep -Fx "${expected_ssh_identity_two}" "${SSH_LOG}" >/dev/null

test "$(wc -c <"${JQ_COUNT_FILE}")" -eq 3

echo "home-control-plane manifest parsing scenario passed"
