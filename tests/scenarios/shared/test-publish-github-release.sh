#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/shared/test-publish-github-release.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-release-scenario.XXXXXX")"
FIXTURE="${TMP_DIR}/repo"
ALLOWED_SIGNERS="${TMP_DIR}/allowed_signers"
SSH_VERIFY_PROGRAM="${TMP_DIR}/ssh-verify"
HOST_XDG_CONFIG_HOME="${TMP_DIR}/xdg-config"
HOST_HOME="${TMP_DIR}/home"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

entrypoint_output="$("${ROOT_DIR}/scripts/publish-github-release.sh" --self-entrypoint-probe)"
grep -Fx 'publish-github-release-entrypoint-ok' <<<"${entrypoint_output}" >/dev/null

mkdir -p "${FIXTURE}" "${HOST_HOME}" "${HOST_XDG_CONFIG_HOME}/git"
export HOME="${HOST_HOME}"
export USER="workcell-scenario"
export LOGNAME="workcell-scenario"
git_cmd() {
  HOME="${HOST_HOME}" XDG_CONFIG_HOME="${HOST_XDG_CONFIG_HOME}" git "$@"
}

git_cmd init -q "${FIXTURE}"
git_cmd -C "${FIXTURE}" config user.name "Workcell Scenario"
git_cmd -C "${FIXTURE}" config user.email "workcell-scenario@example.com"
printf 'workcell-scenario@example.com namespaces="git" ' >"${ALLOWED_SIGNERS}"
printf '%s\n' 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA' >>"${ALLOWED_SIGNERS}"
cat >"${SSH_VERIFY_PROGRAM}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

case " $* " in
  *' -Y find-principals '*)
    printf '%s\n' 'workcell-scenario@example.com'
    ;;
  *' -Y verify '*)
    printf '%s\n' 'Good "git" signature for workcell-scenario@example.com with ED25519 key SHA256:workcell-scenario'
    ;;
  *)
    echo "unexpected ssh verification invocation: $*" >&2
    exit 1
    ;;
esac
EOF
chmod 0755 "${SSH_VERIFY_PROGRAM}"
cat >"${HOST_XDG_CONFIG_HOME}/git/config" <<EOF
[gpg]
	format = ssh
[gpg "ssh"]
	allowedSignersFile = ${ALLOWED_SIGNERS}
	program = ${SSH_VERIFY_PROGRAM}
EOF
git_cmd -C "${FIXTURE}" config gpg.format ssh
git_cmd -C "${FIXTURE}" config gpg.ssh.allowedSignersFile "${ALLOWED_SIGNERS}"
git_cmd -C "${FIXTURE}" config gpg.ssh.program "${SSH_VERIFY_PROGRAM}"

printf 'release\n' >"${FIXTURE}/tracked.txt"
git_cmd -C "${FIXTURE}" add tracked.txt
git_cmd -C "${FIXTURE}" -c commit.gpgsign=false commit -q -m init
commit_sha="$(git_cmd -C "${FIXTURE}" rev-parse HEAD)"
cat >"${TMP_DIR}/signed-tag.txt" <<EOF
object ${commit_sha}
type commit
tag v9.9.9
tagger Workcell Scenario <workcell-scenario@example.com> 0 +0000

v9.9.9
-----BEGIN SSH SIGNATURE-----
U1NIU0lHAAAAAQAAADMAAAALc3NoLWVkMjU1MTkAAAAgAAAAAAAAAAAAAAAAAAAAAAAA
AAAAAAAAAAAAAAAAAAAAAAAABnNoYTUxMgAAAAAAAAAGc2lnbmVyAAAAAAAAAAAAAAA=
-----END SSH SIGNATURE-----
EOF
tag_sha="$(git_cmd -C "${FIXTURE}" mktag <"${TMP_DIR}/signed-tag.txt")"
git_cmd -C "${FIXTURE}" update-ref refs/tags/v9.9.9 "${tag_sha}"
HOME="${HOST_HOME}" XDG_CONFIG_HOME="${HOST_XDG_CONFIG_HOME}" "${ROOT_DIR}/scripts/check-release-tag-signature.sh" \
  --repo-root "${FIXTURE}" \
  --tag v9.9.9 >/dev/null

# The expected binding must accept the verified pair and reject a moved tag.
HOME="${HOST_HOME}" XDG_CONFIG_HOME="${HOST_XDG_CONFIG_HOME}" "${ROOT_DIR}/scripts/check-release-tag-signature.sh" \
  --repo-root "${FIXTURE}" \
  --tag v9.9.9 \
  --expected-commit "${commit_sha}" \
  --expected-tag-object "${tag_sha}" >/dev/null

other_sha="$(printf '%040d' 0)"
for binding in "--expected-commit ${other_sha}" "--expected-tag-object ${other_sha}"; do
  set +e
  # shellcheck disable=SC2086 # Each binding is a fixed flag/value pair.
  binding_output="$(HOME="${HOST_HOME}" XDG_CONFIG_HOME="${HOST_XDG_CONFIG_HOME}" "${ROOT_DIR}/scripts/check-release-tag-signature.sh" \
    --repo-root "${FIXTURE}" \
    --tag v9.9.9 ${binding} 2>&1)"
  binding_rc=$?
  set -e
  test "${binding_rc}" -eq 2
  grep -F 'not the expected' <<<"${binding_output}" >/dev/null
done

set +e
malformed_output="$(HOME="${HOST_HOME}" XDG_CONFIG_HOME="${HOST_XDG_CONFIG_HOME}" "${ROOT_DIR}/scripts/check-release-tag-signature.sh" \
  --repo-root "${FIXTURE}" \
  --tag v9.9.9 --expected-commit "$(printf '%s' "${commit_sha}" | tr '[:lower:]' '[:upper:]')" 2>&1)"
malformed_rc=$?
set -e
test "${malformed_rc}" -eq 2
grep -F 'requires a 40-character lowercase object ID' <<<"${malformed_output}" >/dev/null

git_cmd -C "${FIXTURE}" -c tag.gpgSign=false tag v9.9.10
set +e
unsigned_tag_output="$(HOME="${HOST_HOME}" XDG_CONFIG_HOME="${HOST_XDG_CONFIG_HOME}" "${ROOT_DIR}/scripts/check-release-tag-signature.sh" \
  --repo-root "${FIXTURE}" \
  --tag v9.9.10 2>&1)"
unsigned_tag_rc=$?
set -e
test "${unsigned_tag_rc}" -eq 2
grep -F 'must be an annotated signed tag object' <<<"${unsigned_tag_output}" >/dev/null

echo "publish-github-release entrypoint scenario passed"
