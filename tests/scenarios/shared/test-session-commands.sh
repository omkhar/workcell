#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-session-scenario.XXXXXX")"
TMP_DIR="$(cd "${TMP_DIR}" && pwd -P)"
REAL_HOME="$(cd "${ROOT_DIR}" && go run ./cmd/workcell-hostutil path home)"
PROFILE="wcl-session-scenario-$$"
SESSION_ONE="20260408T100000Z-11111111-$$"
SESSION_TWO="20260408T110000Z-22222222-$$"
SESSION_DIRTY="20260408T120000Z-33333333-$$"

cleanup() {
  rm -rf "${REAL_HOME}/.colima/${PROFILE}"
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

COLIMA_ROOT="${REAL_HOME}/.colima"
SESSIONS_DIR="${COLIMA_ROOT}/${PROFILE}/sessions"
AUDIT_LOG="${COLIMA_ROOT}/${PROFILE}/workcell.audit.log"
EXPORT_PATH="${TMP_DIR}/session-export.json"
WORKSPACE_A="${TMP_DIR}/workspace-a"
WORKSPACE_B="${TMP_DIR}/workspace-b"
DIFF_PATH="${TMP_DIR}/session-diff.txt"
TEXTCONV_MARKER="${WORKSPACE_A}/textconv-ran"

mkdir -p "${SESSIONS_DIR}" "${WORKSPACE_A}" "${WORKSPACE_B}"
WORKSPACE_A="$(cd "${WORKSPACE_A}" && pwd -P)"
WORKSPACE_B="$(cd "${WORKSPACE_B}" && pwd -P)"

git -C "${WORKSPACE_A}" init >/dev/null
cat >"${WORKSPACE_A}/textconv.sh" <<EOF
#!/bin/sh
touch "${TEXTCONV_MARKER}"
cat "\$1"
EOF
chmod +x "${WORKSPACE_A}/textconv.sh"
git -C "${WORKSPACE_A}" config diff.workcell.textconv "${WORKSPACE_A}/textconv.sh"
printf '*.txt diff=workcell\n' >"${WORKSPACE_A}/.gitattributes"
printf 'base\n' >"${WORKSPACE_A}/tracked.txt"
git -C "${WORKSPACE_A}" add .gitattributes tracked.txt
git -C "${WORKSPACE_A}" -c user.name='Workcell Test' -c user.email='workcell@example.com' commit -m 'initial' >/dev/null
GIT_BASE="$(git -C "${WORKSPACE_A}" rev-parse HEAD)"
GIT_BRANCH="$(git -C "${WORKSPACE_A}" branch --show-current)"
printf 'updated\n' >"${WORKSPACE_A}/tracked.txt"
printf 'new file\n' >"${WORKSPACE_A}/new.txt"

cat >"${SESSIONS_DIR}/20260408T100000Z-11111111.json" <<EOF
{
  "version": 1,
  "session_id": "${SESSION_ONE}",
  "profile": "${PROFILE}",
  "agent": "codex",
  "mode": "strict",
  "status": "exited",
  "ui": "cli",
  "execution_path": "managed-tier1",
  "workspace": "${WORKSPACE_A}",
  "git_branch": "${GIT_BRANCH}",
  "git_head": "${GIT_BASE}",
  "git_base": "${GIT_BASE}",
  "audit_log_path": "${AUDIT_LOG}",
  "started_at": "2026-04-08T10:00:00Z",
  "finished_at": "2026-04-08T10:05:00Z",
  "exit_status": "0",
  "initial_assurance": "managed-mutable",
  "final_assurance": "managed-mutable",
  "workspace_control_plane": "masked"
}
EOF

cat >"${SESSIONS_DIR}/20260408T110000Z-22222222.json" <<EOF
{
  "version": 1,
  "session_id": "${SESSION_TWO}",
  "profile": "${PROFILE}",
  "agent": "claude",
  "mode": "development",
  "status": "failed",
  "ui": "cli",
  "execution_path": "lower-assurance-development",
  "workspace": "${WORKSPACE_B}",
  "audit_log_path": "${AUDIT_LOG}",
  "started_at": "2026-04-08T11:00:00Z",
  "finished_at": "2026-04-08T11:03:00Z",
  "exit_status": "17",
  "initial_assurance": "managed-mutable",
  "final_assurance": "managed-mutable",
  "workspace_control_plane": "masked"
}
EOF

cat >"${AUDIT_LOG}" <<EOF
timestamp=2026-04-08T10:00:00Z event=launch session_id=${SESSION_ONE} record_digest=aaa
timestamp=2026-04-08T11:00:00Z event=launch session_id=${SESSION_TWO} record_digest=bbb
timestamp=2026-04-08T11:03:00Z event=exit session_id=${SESSION_TWO} record_digest=ccc
EOF

list_output="$("${ROOT_DIR}/scripts/workcell" session list --colima-profile "${PROFILE}")"
grep -q '^session_id[[:space:]]status[[:space:]]agent[[:space:]]mode[[:space:]]profile[[:space:]]started_at[[:space:]]assurance[[:space:]]workspace$' <<<"${list_output}"
grep -q $'^'"${SESSION_TWO}"$'\tfailed\tclaude\tdevelopment\t'"${PROFILE}"$'\t2026-04-08T11:00:00Z\tmanaged-mutable\t'"${WORKSPACE_B}"'$' <<<"${list_output}"
grep -q $'^'"${SESSION_ONE}"$'\texited\tcodex\tstrict\t'"${PROFILE}"$'\t2026-04-08T10:00:00Z\tmanaged-mutable\t'"${WORKSPACE_A}"'$' <<<"${list_output}"

if command -v script >/dev/null 2>&1; then
  tty_list_cmd=("${ROOT_DIR}/scripts/workcell" session list --colima-profile "${PROFILE}")
  if script_help="$(script --help 2>&1 || true)" && grep -q -- ' -c, --command ' <<<"${script_help}"; then
    printf -v tty_list_shell_cmd '%q ' "${tty_list_cmd[@]}"
    tty_list_output="$(
      script -q /dev/null -c "${tty_list_shell_cmd% }" 2>/dev/null | tr -d '\r\004\010'
    )"
  else
    tty_list_output="$(
      script -q /dev/null "${tty_list_cmd[@]}" 2>/dev/null | tr -d '\r\004\010'
    )"
  fi
  grep -q 'session_id[[:space:]]status[[:space:]]agent[[:space:]]mode[[:space:]]profile[[:space:]]started_at[[:space:]]assurance[[:space:]]workspace' <<<"${tty_list_output}"
  if printf '%s' "${tty_list_output}" | LC_ALL=C grep -q $'\033\['; then
    echo "session list leaked terminal reset escapes on a host-only TTY path" >&2
    exit 1
  fi
fi

list_json="$("${ROOT_DIR}/scripts/workcell" session list --json --workspace "${WORKSPACE_A}" --colima-profile "${PROFILE}")"
grep -q "\"session_id\": \"${SESSION_ONE}\"" <<<"${list_json}"
if grep -q "\"session_id\": \"${SESSION_TWO}\"" <<<"${list_json}"; then
  echo "session list --workspace returned an unexpected session" >&2
  exit 1
fi

show_output="$("${ROOT_DIR}/scripts/workcell" session show --id "${SESSION_TWO}")"
grep -q "\"session_id\": \"${SESSION_TWO}\"" <<<"${show_output}"
grep -q '"status": "failed"' <<<"${show_output}"

diff_stdout="$("${ROOT_DIR}/scripts/workcell" session diff --id "${SESSION_ONE}" --output "${DIFF_PATH}")"
grep -q "^session_diff=${DIFF_PATH}$" <<<"${diff_stdout}"
grep -q "^session_id=${SESSION_ONE}$" "${DIFF_PATH}"
grep -q "^git_branch=${GIT_BRANCH}$" "${DIFF_PATH}"
grep -q '^ M tracked.txt$' "${DIFF_PATH}"
grep -Fq '?? new.txt' "${DIFF_PATH}"
grep -q '^-base$' "${DIFF_PATH}"
grep -q '^+updated$' "${DIFF_PATH}"
test ! -e "${TEXTCONV_MARKER}"

cat >"${SESSIONS_DIR}/20260408T120000Z-33333333.json" <<EOF
{
  "version": 1,
  "session_id": "${SESSION_DIRTY}",
  "profile": "${PROFILE}",
  "agent": "codex",
  "mode": "strict",
  "status": "exited",
  "ui": "cli",
  "execution_path": "managed-tier1",
  "workspace": "${WORKSPACE_A}",
  "git_branch": "${GIT_BRANCH}",
  "git_head": "${GIT_BASE}",
  "audit_log_path": "${AUDIT_LOG}",
  "started_at": "2026-04-08T12:00:00Z",
  "finished_at": "2026-04-08T12:05:00Z",
  "exit_status": "0",
  "initial_assurance": "managed-mutable",
  "final_assurance": "managed-mutable",
  "workspace_control_plane": "masked"
}
EOF

dirty_diff_output="$(
  "${ROOT_DIR}/scripts/workcell" session diff --id "${SESSION_DIRTY}" 2>&1 >/dev/null || true
)"
grep -q 'session diff requires a clean git launch baseline' <<<"${dirty_diff_output}"

export_stdout="$("${ROOT_DIR}/scripts/workcell" session export --id "${SESSION_TWO}" --output "${EXPORT_PATH}")"
grep -q "^session_export=${EXPORT_PATH}$" <<<"${export_stdout}"
grep -q "\"session_id\": \"${SESSION_TWO}\"" "${EXPORT_PATH}"
grep -q '"audit_records": \[' "${EXPORT_PATH}"
grep -q 'record_digest=ccc' "${EXPORT_PATH}"

missing_output="$(
  "${ROOT_DIR}/scripts/workcell" session show --id missing-session 2>&1 >/dev/null || true
)"
grep -q 'file does not exist' <<<"${missing_output}"
