#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
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

draft_ok_output="$("${ROOT_DIR}/scripts/publish-github-release.sh" --self-release-state-probe v9.9.9 true false)"
grep -Fx 'publish-github-release-state-ok' <<<"${draft_ok_output}" >/dev/null

set +e
published_mutable_output="$("${ROOT_DIR}/scripts/publish-github-release.sh" --self-release-state-probe v9.9.9 false false 2>&1)"
published_mutable_rc=$?
set -e
test "${published_mutable_rc}" -eq 1
grep -F 'GitHub release v9.9.9 is already published; Workcell only uploads assets to draft releases.' <<<"${published_mutable_output}" >/dev/null

set +e
published_immutable_output="$("${ROOT_DIR}/scripts/publish-github-release.sh" --self-release-state-probe v9.9.9 false true 2>&1)"
published_immutable_rc=$?
set -e
test "${published_immutable_rc}" -eq 1
grep -F 'GitHub release v9.9.9 is already published and immutable; asset uploads are no longer allowed.' <<<"${published_immutable_output}" >/dev/null

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

git_cmd -C "${FIXTURE}" -c tag.gpgSign=false tag v9.9.10
set +e
unsigned_tag_output="$(HOME="${HOST_HOME}" XDG_CONFIG_HOME="${HOST_XDG_CONFIG_HOME}" "${ROOT_DIR}/scripts/check-release-tag-signature.sh" \
  --repo-root "${FIXTURE}" \
  --tag v9.9.10 2>&1)"
unsigned_tag_rc=$?
set -e
test "${unsigned_tag_rc}" -eq 2
grep -F 'must be an annotated signed tag object' <<<"${unsigned_tag_output}" >/dev/null

echo "publish-github-release policy scenario passed"
