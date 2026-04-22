#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-session-scenario.XXXXXX")"
TMP_DIR="$(cd "${TMP_DIR}" && pwd -P)"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
REAL_HOME="$(resolve_workcell_real_home)"
PROFILE="wcl-session-scenario-$$"
SESSION_ONE="20260408T100000Z-11111111-$$"
SESSION_ATTACHED_LIVE="20260408T101500Z-12121212-$$"
SESSION_TWO="20260408T110000Z-22222222-$$"
SESSION_DELETE="20260408T113000Z-22666666-$$"
SESSION_DELETE_RUNNING="20260408T113500Z-22777777-$$"
SESSION_DIRTY="20260408T120000Z-33333333-$$"
SESSION_TAMPERED="20260408T140000Z-55555555-$$"
CLI_SESSION_FIXTURE_IMAGE="workcell-session-cli-fixture:test-$$"
CLI_SESSION_FIXTURE_BUILD_DIR="${TMP_DIR}/session-cli-fixture-build"
WORKCELL_FUNCTIONS_COPY="${ROOT_DIR}/scripts/.workcell-test-functions-$$"
HOST_DOCKER_BIN=""

resolve_host_docker_bin() {
  local candidate=""

  candidate="$(command -v docker 2>/dev/null || true)"
  if [[ -x "${candidate}" ]]; then
    printf '%s\n' "${candidate}"
    return 0
  fi
  for candidate in \
    /opt/homebrew/bin/docker \
    /usr/local/bin/docker \
    /Applications/Docker.app/Contents/Resources/bin/docker; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done
  return 1
}

cleanup() {
  if [[ -n "${CLI_SESSION_FIXTURE_IMAGE:-}" ]]; then
    if [[ -n "${HOST_DOCKER_BIN:-}" ]]; then
      "${HOST_DOCKER_BIN}" image rm -f "${CLI_SESSION_FIXTURE_IMAGE}" >/dev/null 2>&1 || true
    fi
  fi
  cleanup_workcell_trusted_docker_client
  rm -f "${WORKCELL_FUNCTIONS_COPY}"
  rm -rf "${REAL_HOME}/.colima/${PROFILE}"
  rm -rf "${XDG_STATE_HOME:-${REAL_HOME}/.local/state}/workcell/targets/local_vm/colima/${PROFILE}"
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

COLIMA_ROOT="${REAL_HOME}/.colima"
WORKCELL_STATE_ROOT="${XDG_STATE_HOME:-${REAL_HOME}/.local/state}/workcell"
TARGET_STATE_DIR="${WORKCELL_STATE_ROOT}/targets/local_vm/colima/${PROFILE}"
LEGACY_STATE_DIR="${COLIMA_ROOT}/${PROFILE}"
LEGACY_SESSIONS_DIR="${LEGACY_STATE_DIR}/sessions"
SESSIONS_DIR="${TARGET_STATE_DIR}/sessions"
AUDIT_LOG="${TARGET_STATE_DIR}/workcell.audit.log"
EXPORT_PATH="${TMP_DIR}/session-export.json"
WORKSPACE_A="${TMP_DIR}/workspace-a"
WORKSPACE_B="${TMP_DIR}/workspace-b"
DIFF_PATH="${TMP_DIR}/session-diff.txt"
TEXTCONV_MARKER="${WORKSPACE_A}/textconv-ran"
FILTER_MARKER="${WORKSPACE_A}/filter-ran"
FILTER_CONFIG="${WORKSPACE_A}/filter.inc"
TAMPERED_LOG_TARGET="${TMP_DIR}/tampered-log-target"
TAMPERED_DEBUG_LINK="${WORKSPACE_A}/tampered-debug.log"
SYMLINKED_POINTER_TARGET="${TMP_DIR}/symlinked-pointer-target"
DETACHED_START_WORKSPACE="${TMP_DIR}/detached-start-workspace"
DETACHED_SESSION="20260408T130000Z-44444444-$$"
DETACHED_STATE_DIR="${TMP_DIR}/detached-session-state"
DETACHED_STATE_FILE="${DETACHED_STATE_DIR}/session.env"
DETACHED_AUDIT_LOG="${DETACHED_STATE_DIR}/detached-session.audit.log"
DETACHED_DEBUG_LOG="${DETACHED_STATE_DIR}/detached-session.debug.log"
DETACHED_FILE_TRACE_LOG="${DETACHED_STATE_DIR}/detached-session.file-trace.log"
DETACHED_TRANSCRIPT_LOG="${DETACHED_STATE_DIR}/detached-session.transcript.log"
DETACHED_WORKSPACE="${WORKSPACE_A}/.git/session-sandboxes/${DETACHED_SESSION}"
DETACHED_BRANCH="workcell/session/${DETACHED_SESSION}"
DETACHED_BACKEND="${TMP_DIR}/detached-session-backend"

mkdir -p "${SESSIONS_DIR}" "${LEGACY_STATE_DIR}" "${WORKSPACE_A}" "${WORKSPACE_B}"
WORKSPACE_A="$(cd "${WORKSPACE_A}" && pwd -P)"
WORKSPACE_B="$(cd "${WORKSPACE_B}" && pwd -P)"

git -C "${WORKSPACE_A}" init >/dev/null
cat >"${WORKSPACE_A}/textconv.sh" <<EOF
#!/bin/sh
touch "${TEXTCONV_MARKER}"
cat "\$1"
EOF
chmod +x "${WORKSPACE_A}/textconv.sh"
cat >"${WORKSPACE_A}/filter.sh" <<EOF
#!/bin/sh
touch "${FILTER_MARKER}"
cat
EOF
chmod +x "${WORKSPACE_A}/filter.sh"
git -C "${WORKSPACE_A}" config diff.workcell.textconv "${WORKSPACE_A}/textconv.sh"
git -C "${WORKSPACE_A}" config extensions.worktreeConfig true
cat >"${FILTER_CONFIG}" <<EOF
[filter "workcell-test"]
	clean = ${WORKSPACE_A}/filter.sh
	smudge = cat
EOF
git -C "${WORKSPACE_A}" config --worktree include.path "${FILTER_CONFIG}"
printf '*.txt diff=workcell\n*.filter filter=workcell-test\n' >"${WORKSPACE_A}/.gitattributes"
printf 'base\n' >"${WORKSPACE_A}/tracked.txt"
printf 'seed\n' >"${WORKSPACE_A}/tracked.filter"
git -C "${WORKSPACE_A}" add .gitattributes tracked.txt tracked.filter
git -C "${WORKSPACE_A}" -c user.name='Workcell Test' -c user.email='workcell@example.com' commit -m 'initial' >/dev/null
rm -f "${FILTER_MARKER}"
GIT_BASE="$(git -C "${WORKSPACE_A}" rev-parse HEAD)"
GIT_BRANCH="$(git -C "${WORKSPACE_A}" branch --show-current)"
printf 'updated\n' >"${WORKSPACE_A}/tracked.txt"
printf 'new file\n' >"${WORKSPACE_A}/new.txt"
printf 'changed\n' >"${WORKSPACE_A}/tracked.filter"

cat >"${SESSIONS_DIR}/20260408T100000Z-11111111.json" <<EOF
{
  "version": 1,
  "session_id": "${SESSION_ONE}",
  "profile": "${PROFILE}",
  "agent": "codex",
  "mode": "strict",
  "status": "exited",
  "live_status": "stopped",
  "ui": "cli",
  "execution_path": "managed-tier1",
  "workspace": "${WORKSPACE_A}",
  "container_name": "workcell-session-one",
  "session_audit_dir": "${TMP_DIR}/session-audit.attached",
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

cat >"${SESSIONS_DIR}/20260408T101500Z-12121212.json" <<EOF
{
  "version": 1,
  "session_id": "${SESSION_ATTACHED_LIVE}",
  "profile": "${PROFILE}",
  "agent": "codex",
  "mode": "strict",
  "status": "running",
  "live_status": "running",
  "ui": "cli",
  "execution_path": "managed-tier1",
  "workspace": "${WORKSPACE_A}",
  "workspace_origin": "${WORKSPACE_A}",
  "worktree_path": "${WORKSPACE_A}",
  "container_name": "workcell-session-live-attached",
  "session_audit_dir": "${TMP_DIR}/session-audit.live-attached",
  "audit_log_path": "${AUDIT_LOG}",
  "started_at": "2026-04-08T10:15:00Z",
  "current_assurance": "managed-mutable",
  "initial_assurance": "managed-mutable",
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
  "live_status": "stopped",
  "ui": "cli",
  "execution_path": "lower-assurance-development",
  "workspace": "${WORKSPACE_B}",
  "workspace_origin": "${WORKSPACE_A}",
  "worktree_path": "${WORKSPACE_B}/.worktrees/${SESSION_TWO}",
  "container_name": "workcell-session-two",
  "monitor_pid": "4242",
  "session_audit_dir": "${TMP_DIR}/session-audit.4242",
  "audit_log_path": "${AUDIT_LOG}",
  "started_at": "2026-04-08T11:00:00Z",
  "finished_at": "2026-04-08T11:03:00Z",
  "exit_status": "17",
  "initial_assurance": "managed-mutable",
  "final_assurance": "managed-mutable",
  "workspace_control_plane": "masked"
}
EOF

cat >"${SESSIONS_DIR}/${SESSION_DELETE}.json" <<EOF
{
  "version": 1,
  "session_id": "${SESSION_DELETE}",
  "profile": "${PROFILE}",
  "agent": "codex",
  "mode": "strict",
  "status": "exited",
  "live_status": "stopped",
  "ui": "cli",
  "execution_path": "managed-tier1",
  "workspace": "${WORKSPACE_B}",
  "container_name": "workcell-session-delete",
  "audit_log_path": "${AUDIT_LOG}",
  "started_at": "2026-04-08T11:30:00Z",
  "finished_at": "2026-04-08T11:31:00Z",
  "exit_status": "0",
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
grep -q '^session_id[[:space:]]status[[:space:]]live_status[[:space:]]control[[:space:]]agent[[:space:]]mode[[:space:]]profile[[:space:]]started_at[[:space:]]assurance[[:space:]]workspace$' <<<"${list_output}"
grep -q $'^'"${SESSION_TWO}"$'\tfailed\tstopped\tdetached\tclaude\tdevelopment\t'"${PROFILE}"$'\t2026-04-08T11:00:00Z\tmanaged-mutable\t'"${WORKSPACE_A}"'$' <<<"${list_output}"
grep -q $'^'"${SESSION_ATTACHED_LIVE}"$'\trunning\trunning\tattached\tcodex\tstrict\t'"${PROFILE}"$'\t2026-04-08T10:15:00Z\tmanaged-mutable\t'"${WORKSPACE_A}"'$' <<<"${list_output}"
grep -q $'^'"${SESSION_ONE}"$'\texited\tstopped\tattached\tcodex\tstrict\t'"${PROFILE}"$'\t2026-04-08T10:00:00Z\tmanaged-mutable\t'"${WORKSPACE_A}"'$' <<<"${list_output}"

list_verbose_output="$("${ROOT_DIR}/scripts/workcell" session list --verbose --colima-profile "${PROFILE}")"
grep -q '^session_id[[:space:]]status[[:space:]]live_status[[:space:]]control[[:space:]]agent[[:space:]]mode[[:space:]]profile[[:space:]]target[[:space:]]target_assurance[[:space:]]workspace_transport[[:space:]]git_branch[[:space:]]worktree[[:space:]]started_at[[:space:]]assurance[[:space:]]workspace$' <<<"${list_verbose_output}"
grep -q $'^'"${SESSION_TWO}"$'\tfailed\tstopped\tdetached\tclaude\tdevelopment\t'"${PROFILE}"$'\tlocal_vm/colima/'"${PROFILE}"$'\tstrict\tisolated-worktree-mount\tnone\t'"${WORKSPACE_B}/.worktrees/${SESSION_TWO}"$'\t2026-04-08T11:00:00Z\tmanaged-mutable\t'"${WORKSPACE_A}"'$' <<<"${list_verbose_output}"

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
  grep -q 'session_id[[:space:]]status[[:space:]]live_status[[:space:]]control[[:space:]]agent[[:space:]]mode[[:space:]]profile[[:space:]]started_at[[:space:]]assurance[[:space:]]workspace' <<<"${tty_list_output}"
  if printf '%s' "${tty_list_output}" | LC_ALL=C grep -q $'\033\['; then
    echo "session list leaked terminal reset escapes on a host-only TTY path" >&2
    exit 1
  fi
fi

set +e
stop_attached_stderr="$("${ROOT_DIR}/scripts/workcell" session stop --id "${SESSION_ATTACHED_LIVE}" 2>&1 >/dev/null)"
stop_attached_status=$?
set -e
if [[ "${stop_attached_status}" -eq 0 ]]; then
  echo "session stop unexpectedly accepted an attached record" >&2
  exit 1
fi
grep -q "session stop only works for detached sessions started with 'workcell session start': ${SESSION_ATTACHED_LIVE}" <<<"${stop_attached_stderr}"
grep -q "Use 'workcell session list' to check the control column; attached records are not stoppable." <<<"${stop_attached_stderr}"

set +e
delete_stderr="$("${ROOT_DIR}/scripts/workcell" session delete --id "${SESSION_DELETE}" 2>&1 >/dev/null)"
delete_status=$?
set -e
if [[ "${delete_status}" -eq 0 ]]; then
  echo "session delete unexpectedly removed a session when container cleanup was unavailable" >&2
  exit 1
fi
grep -q "session delete could not confirm cleanup of the recorded session container: ${SESSION_DELETE}" <<<"${delete_stderr}"
grep -q "Retry while the profile Docker socket is available, or pass --record-only to remove only the session record." <<<"${delete_stderr}"
list_after_failed_delete="$("${ROOT_DIR}/scripts/workcell" session list --colima-profile "${PROFILE}")"
grep -q "${SESSION_DELETE}" <<<"${list_after_failed_delete}"

cat >"${SESSIONS_DIR}/${SESSION_DELETE_RUNNING}.json" <<EOF
{
  "version": 1,
  "session_id": "${SESSION_DELETE_RUNNING}",
  "profile": "${PROFILE}",
  "agent": "codex",
  "mode": "strict",
  "status": "running",
  "live_status": "running",
  "ui": "cli",
  "execution_path": "managed-tier1",
  "workspace": "${WORKSPACE_B}",
  "container_name": "workcell-session-running",
  "started_at": "2026-04-08T11:35:00Z",
  "initial_assurance": "managed-mutable",
  "current_assurance": "managed-mutable",
  "workspace_control_plane": "masked"
}
EOF
set +e
delete_running_stderr="$("${ROOT_DIR}/scripts/workcell" session delete --id "${SESSION_DELETE_RUNNING}" 2>&1 >/dev/null)"
delete_running_status=$?
set -e
if [[ "${delete_running_status}" -eq 0 ]]; then
  echo "session delete unexpectedly accepted a running session" >&2
  exit 1
fi
grep -q "session delete only works for exited, failed, or otherwise stopped sessions: ${SESSION_DELETE_RUNNING}" <<<"${delete_running_stderr}"
grep -q '^This session is still running\.$' <<<"${delete_running_stderr}"

list_json="$("${ROOT_DIR}/scripts/workcell" session list --json --workspace "${WORKSPACE_A}" --colima-profile "${PROFILE}")"
grep -q "\"session_id\": \"${SESSION_ONE}\"" <<<"${list_json}"
grep -q "\"session_id\": \"${SESSION_TWO}\"" <<<"${list_json}"
grep -q '"target_kind": "local_vm"' <<<"${list_json}"
grep -q '"target_provider": "colima"' <<<"${list_json}"
grep -q '"target_assurance_class": "strict"' <<<"${list_json}"

show_output="$("${ROOT_DIR}/scripts/workcell" session show --id "${SESSION_TWO}")"
grep -q "\"session_id\": \"${SESSION_TWO}\"" <<<"${show_output}"
grep -q '"status": "failed"' <<<"${show_output}"
grep -q '"target_kind": "local_vm"' <<<"${show_output}"
grep -q '"workspace_transport": "isolated-worktree-mount"' <<<"${show_output}"

show_text_output="$("${ROOT_DIR}/scripts/workcell" session show --id "${SESSION_TWO}" --text)"
grep -q '^target_summary=local_vm/colima/'"${PROFILE}"'$' <<<"${show_text_output}"
grep -q '^workspace_transport=isolated-worktree-mount$' <<<"${show_text_output}"
grep -q '^display_workspace='"${WORKSPACE_A}"'$' <<<"${show_text_output}"
grep -q '^display_worktree='"${WORKSPACE_B}/.worktrees/${SESSION_TWO}"'$' <<<"${show_text_output}"
grep -q '^display_git_branch=none$' <<<"${show_text_output}"
grep -q '^worktree_path='"${WORKSPACE_B}/.worktrees/${SESSION_TWO}"'$' <<<"${show_text_output}"
show_text_with_git="$("${ROOT_DIR}/scripts/workcell" session show --id "${SESSION_ONE}" --text)"
grep -q '^display_git_branch='"${GIT_BRANCH}"'$' <<<"${show_text_with_git}"
grep -q '^git_branch='"${GIT_BRANCH}"'$' <<<"${show_text_with_git}"

diff_stdout="$("${ROOT_DIR}/scripts/workcell" session diff --id "${SESSION_ONE}" --output "${DIFF_PATH}")"
grep -q "^session_diff=${DIFF_PATH}$" <<<"${diff_stdout}"
grep -q "^session_id=${SESSION_ONE}$" "${DIFF_PATH}"
grep -q "^git_branch=${GIT_BRANCH}$" "${DIFF_PATH}"
grep -q '^ M tracked.txt$' "${DIFF_PATH}"
grep -Fq '?? new.txt' "${DIFF_PATH}"
grep -q '^-base$' "${DIFF_PATH}"
grep -q '^+updated$' "${DIFF_PATH}"
test ! -e "${TEXTCONV_MARKER}"
test ! -e "${FILTER_MARKER}"

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

printf 'top-secret\n' >"${TAMPERED_LOG_TARGET}"
ln -s "${TAMPERED_LOG_TARGET}" "${TAMPERED_DEBUG_LINK}"
cat >"${SESSIONS_DIR}/20260408T140000Z-55555555.json" <<EOF
{
  "version": 1,
  "session_id": "${SESSION_TAMPERED}",
  "profile": "${PROFILE}",
  "agent": "codex",
  "mode": "development",
  "status": "exited",
  "ui": "cli",
  "execution_path": "managed-tier1",
  "workspace": "${WORKSPACE_A}",
  "debug_log_path": "${TAMPERED_DEBUG_LINK}",
  "started_at": "2026-04-08T14:00:00Z",
  "finished_at": "2026-04-08T14:01:00Z",
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

tampered_session_logs_output="$(
  "${ROOT_DIR}/scripts/workcell" session logs --id "${SESSION_TAMPERED}" --kind debug 2>&1 >/dev/null || true
)"
grep -q "Workcell blocked host output path after launch: ${TAMPERED_DEBUG_LINK}" <<<"${tampered_session_logs_output}"
if grep -q 'top-secret' <<<"${tampered_session_logs_output}"; then
  echo "session logs followed a tampered debug-log symlink" >&2
  exit 1
fi
printf '%s\n' "${TAMPERED_DEBUG_LINK}" >"${REAL_HOME}/.colima/${PROFILE}/workcell.latest-debug-log"
tampered_profile_logs_output="$(
  "${ROOT_DIR}/scripts/workcell" --logs debug --colima-profile "${PROFILE}" 2>&1 >/dev/null || true
)"
grep -q "Workcell blocked host output path after launch: ${TAMPERED_DEBUG_LINK}" <<<"${tampered_profile_logs_output}"
if grep -q 'top-secret' <<<"${tampered_profile_logs_output}"; then
  echo "profile debug log retrieval followed a tampered symlink" >&2
  exit 1
fi
printf '%s\n' "${AUDIT_LOG}" >"${SYMLINKED_POINTER_TARGET}"
rm -f "${REAL_HOME}/.colima/${PROFILE}/workcell.latest-transcript-log"
ln -s "${SYMLINKED_POINTER_TARGET}" "${REAL_HOME}/.colima/${PROFILE}/workcell.latest-transcript-log"
symlinked_pointer_output="$(
  "${ROOT_DIR}/scripts/workcell" --logs transcript --colima-profile "${PROFILE}" 2>&1 >/dev/null || true
)"
grep -q "Workcell blocked latest transcript pointer path after launch: ${REAL_HOME}/.colima/${PROFILE}/workcell.latest-transcript-log" <<<"${symlinked_pointer_output}"

mkdir -p "${DETACHED_START_WORKSPACE}"
git -C "${DETACHED_START_WORKSPACE}" init >/dev/null
printf 'seed\n' >"${DETACHED_START_WORKSPACE}/README.md"
git -C "${DETACHED_START_WORKSPACE}" add README.md
git -C "${DETACHED_START_WORKSPACE}" -c user.name='Workcell Test' -c user.email='workcell@example.com' commit -m 'initial' >/dev/null
detached_support_doctor_output="$(
  "${ROOT_DIR}/scripts/workcell" \
    --agent codex \
    --mode development \
    --workspace "${DETACHED_START_WORKSPACE}" \
    --no-default-injection-policy \
    --doctor
)"
detached_launch_blocked=0
if grep -q '^support_matrix_launch=blocked$' <<<"${detached_support_doctor_output}"; then
  detached_launch_blocked=1
fi

run_detached_start_dry_run() {
  local workspace_mode="$1"
  local workspace_path="$2"
  shift 2

  WORKCELL_SESSION_WORKSPACE_MODE="${workspace_mode}" \
    "${ROOT_DIR}/scripts/workcell" \
    session start \
    --agent codex \
    --mode development \
    --workspace "${workspace_path}" \
    --no-default-injection-policy \
    --allow-arbitrary-command \
    --ack-arbitrary-command \
    "$@" \
    --dry-run \
    -- /bin/true
}

if [[ "${detached_launch_blocked}" -eq 1 ]]; then
  set +e
  detached_start_blocked_output="$(run_detached_start_dry_run direct "${DETACHED_START_WORKSPACE}" 2>&1 >/dev/null)"
  detached_start_blocked_status=$?
  set -e
  test "${detached_start_blocked_status}" -eq 2
  grep -q 'Workcell launch is not supported' <<<"${detached_start_blocked_output}"
  grep -q 'Supported launch hosts today remain Apple Silicon macOS' <<<"${detached_start_blocked_output}"
else
  detached_start_default_output="$(run_detached_start_dry_run direct "${DETACHED_START_WORKSPACE}")"
  grep -Fq -- "docker run --init -d -i -t" <<<"${detached_start_default_output}"
  grep -Eq -- "-v ${DETACHED_START_WORKSPACE}/\\.git/workcell-sessions/.+/repo:/workspace($| )" <<<"${detached_start_default_output}"
  grep -Fq -- "-e WORKCELL_DETACHED_STDIN_PATH=/state/tmp/workcell/session-stdin" <<<"${detached_start_default_output}"
  if grep -Fq -- "-v ${DETACHED_START_WORKSPACE}:/workspace" <<<"${detached_start_default_output}"; then
    echo "Detached session start honored an inherited workspace-mode env override" >&2
    exit 1
  fi
  if grep -Fq -- "--entrypoint /bin/true" <<<"${detached_start_default_output}"; then
    echo "Detached session start should keep arbitrary-command sessions on the managed entrypoint" >&2
    exit 1
  fi
  grep -Fq -- "workcell:local /bin/true" <<<"${detached_start_default_output}"
  if grep -Eq -- "-e WORKCELL_DETACHED_STDIN_PATH=/run/workcell/session-stdin" <<<"${detached_start_default_output}"; then
    echo "Detached session start kept the detached stdin FIFO under a path that blocks host command injection" >&2
    exit 1
  fi
fi
custom_state_home="${TMP_DIR}/detached-state-home"
if [[ "${detached_launch_blocked}" -eq 0 ]]; then
  detached_start_direct_output="$(run_detached_start_dry_run isolated "${DETACHED_START_WORKSPACE}" --session-workspace direct)"
  grep -Fq -- "-v ${DETACHED_START_WORKSPACE}:/workspace" <<<"${detached_start_direct_output}"

  NONGIT_DETACHED_WORKSPACE="${TMP_DIR}/detached-start-nongit"
  mkdir -p "${NONGIT_DETACHED_WORKSPACE}"
  set +e
  nongit_isolated_output="$(run_detached_start_dry_run isolated "${NONGIT_DETACHED_WORKSPACE}" --session-workspace isolated 2>&1 >/dev/null)"
  nongit_isolated_status=$?
  set -e
  test "${nongit_isolated_status}" -eq 2
  grep -q "Workspace is not a git worktree: ${NONGIT_DETACHED_WORKSPACE}" <<<"${nongit_isolated_output}"
  grep -q 'Detached session start with --session-workspace isolated requires a git workspace\.' <<<"${nongit_isolated_output}"
  grep -q 'Next step: rerun with --session-workspace direct if you want to use the current workspace in place\.' <<<"${nongit_isolated_output}"

  printf 'dirty\n' >>"${DETACHED_START_WORKSPACE}/README.md"
  set +e
  dirty_isolated_output="$(run_detached_start_dry_run isolated "${DETACHED_START_WORKSPACE}" --session-workspace isolated 2>&1 >/dev/null)"
  dirty_isolated_status=$?
  set -e
  test "${dirty_isolated_status}" -eq 2
  grep -q "session start requires a clean source workspace for isolated session cloning: ${DETACHED_START_WORKSPACE}" <<<"${dirty_isolated_output}"
  grep -q 'Next step: commit or stash the workspace changes, or rerun with --session-workspace direct to use the current workspace in place\.' <<<"${dirty_isolated_output}"

  LINKED_WORKTREE_ROOT="${TMP_DIR}/detached-linked-worktree"
  LINKED_WORKTREE_MAIN="${LINKED_WORKTREE_ROOT}/main"
  LINKED_WORKTREE_PATH="${LINKED_WORKTREE_ROOT}/linked"
  mkdir -p "${LINKED_WORKTREE_ROOT}"
  git init -q "${LINKED_WORKTREE_MAIN}"
  git -C "${LINKED_WORKTREE_MAIN}" config user.name "Workcell Test"
  git -C "${LINKED_WORKTREE_MAIN}" config user.email "workcell@example.com"
  printf 'tracked\n' >"${LINKED_WORKTREE_MAIN}/tracked.txt"
  git -C "${LINKED_WORKTREE_MAIN}" add tracked.txt
  git -C "${LINKED_WORKTREE_MAIN}" commit -q -m init
  git -C "${LINKED_WORKTREE_MAIN}" worktree add -q -b linked "${LINKED_WORKTREE_PATH}"
  set +e
  linked_isolated_output="$(run_detached_start_dry_run isolated "${LINKED_WORKTREE_PATH}" --session-workspace isolated 2>&1 >/dev/null)"
  linked_isolated_status=$?
  set -e
  test "${linked_isolated_status}" -eq 2
  grep -q "Refusing git workspace with admin state outside the mounted workspace: ${LINKED_WORKTREE_PATH}" <<<"${linked_isolated_output}"
  grep -q 'This workspace is a linked worktree: its .git file points to a .git directory outside the mounted path\.' <<<"${linked_isolated_output}"
  grep -q 'To use this workspace on the safe path, create a standard clone at the same location instead\.' <<<"${linked_isolated_output}"
  grep -q 'Alternatively, pass --mode breakglass --ack-breakglass to proceed with a linked worktree\.' <<<"${linked_isolated_output}"
fi

sed '/^if \[\[ \$# -gt 0 \]\]; then$/,$d' "${ROOT_DIR}/scripts/workcell" >"${WORKCELL_FUNCTIONS_COPY}"
state_path_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    COLIMA_PROFILE="$2"
    printf "target_state_dir=%s\n" "$(profile_target_state_dir "${COLIMA_PROFILE}")"
    printf "sessions_dir=%s\n" "$(profile_sessions_dir_path "${COLIMA_PROFILE}")"
    printf "audit_log=%s\n" "$(profile_audit_log_path "${COLIMA_PROFILE}")"
    printf "lock_dir=%s\n" "$(profile_lock_dir_path "${COLIMA_PROFILE}")"
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${PROFILE}"
)"
grep -q '^target_state_dir='"${TARGET_STATE_DIR}"'$' <<<"${state_path_output}"
grep -q '^sessions_dir='"${TARGET_STATE_DIR}/sessions"'$' <<<"${state_path_output}"
grep -q '^audit_log='"${TARGET_STATE_DIR}/workcell.audit.log"'$' <<<"${state_path_output}"
grep -q '^lock_dir='"${WORKCELL_STATE_ROOT}/locks/local_vm/colima/${PROFILE}.lock"'$' <<<"${state_path_output}"

if [[ "${detached_launch_blocked}" -eq 0 ]]; then
  detached_start_custom_state_output="$(
    XDG_STATE_HOME="${custom_state_home}" \
      /bin/bash "${ROOT_DIR}/scripts/workcell" \
      session start \
      --session-workspace direct \
      --agent codex \
      --mode development \
      --workspace "${DETACHED_START_WORKSPACE}" \
      --no-default-injection-policy \
      --allow-arbitrary-command \
      --ack-arbitrary-command \
      --dry-run \
      -- /bin/true 2>&1
  )"
  grep -Fq -- "audit_log=${custom_state_home}/workcell/targets/local_vm/colima/" <<<"${detached_start_custom_state_output}"
fi

mkdir -p "${LEGACY_SESSIONS_DIR}"
printf '{}' >"${LEGACY_SESSIONS_DIR}/${SESSION_ONE}.json"
legacy_record_path_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    COLIMA_PROFILE="$2"
    SESSION_ID="$3"
    printf "record_path=%s\n" "$(session_record_path_for "${COLIMA_PROFILE}" "${SESSION_ID}")"
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${PROFILE}" "${SESSION_ONE}"
)"
grep -q '^record_path='"${LEGACY_SESSIONS_DIR}/${SESSION_ONE}.json"'$' <<<"${legacy_record_path_output}"
rm -f "${LEGACY_SESSIONS_DIR}/${SESSION_ONE}.json"

rm -f "${LEGACY_STATE_DIR}/workcell.latest-transcript-log"
printf '%s\n' "${DETACHED_TRANSCRIPT_LOG}" >"${LEGACY_STATE_DIR}/workcell.latest-transcript-log"
legacy_pointer_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    COLIMA_PROFILE="$2"
    printf "pointer=%s\n" "$(read_latest_log_pointer "${COLIMA_PROFILE}" transcript)"
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${PROFILE}"
)"
grep -q '^pointer='"${DETACHED_TRANSCRIPT_LOG}"'$' <<<"${legacy_pointer_output}"
rm -f "${LEGACY_STATE_DIR}/workcell.latest-transcript-log"

legacy_audit_migration_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    FIXTURE_ROOT="$2"
    COLIMA_STATE_ROOT="${FIXTURE_ROOT}/colima"
    WORKCELL_STATE_ROOT="${FIXTURE_ROOT}/workcell"
    WORKCELL_TARGET_STATE_ROOT="${WORKCELL_STATE_ROOT}/targets"
    PROFILE_NAME="audit-migration-fixture"
    LEGACY_LOG="$(legacy_profile_audit_log_path "${PROFILE_NAME}")"
    TARGET_LOG="$(profile_audit_log_path "${PROFILE_NAME}")"
    mkdir -p "$(dirname "${LEGACY_LOG}")"
    printf "legacy-audit-line\n" >"${LEGACY_LOG}"
    stash_profile_audit_log "${PROFILE_NAME}"
    rm -f "${LEGACY_LOG}"
    restore_profile_audit_log "${PROFILE_NAME}"
    printf "target_log=%s\n" "${TARGET_LOG}"
    cat "${TARGET_LOG}"
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${TMP_DIR}/audit-migration-fixture"
)"
grep -q '^target_log='"${TMP_DIR}/audit-migration-fixture/workcell/targets/local_vm/colima/audit-migration-fixture/workcell.audit.log"'$' <<<"${legacy_audit_migration_output}"
grep -q '^legacy-audit-line$' <<<"${legacy_audit_migration_output}"

merged_audit_migration_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    FIXTURE_ROOT="$2"
    COLIMA_STATE_ROOT="${FIXTURE_ROOT}/colima"
    WORKCELL_STATE_ROOT="${FIXTURE_ROOT}/workcell"
    WORKCELL_TARGET_STATE_ROOT="${WORKCELL_STATE_ROOT}/targets"
    PROFILE_NAME="merged-audit-migration-fixture"
    LEGACY_LOG="$(legacy_profile_audit_log_path "${PROFILE_NAME}")"
    TARGET_LOG="$(profile_audit_log_path "${PROFILE_NAME}")"
    mkdir -p "$(dirname "${LEGACY_LOG}")" "$(dirname "${TARGET_LOG}")"
    printf "legacy-audit-line\n" >"${LEGACY_LOG}"
    printf "target-audit-line\n" >"${TARGET_LOG}"
    stash_profile_audit_log "${PROFILE_NAME}"
    restore_profile_audit_log "${PROFILE_NAME}"
    printf "target_log=%s\n" "${TARGET_LOG}"
    cat "${TARGET_LOG}"
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${TMP_DIR}/merged-audit-migration-fixture"
)"
grep -q '^target_log='"${TMP_DIR}/merged-audit-migration-fixture/workcell/targets/local_vm/colima/merged-audit-migration-fixture/workcell.audit.log"'$' <<<"${merged_audit_migration_output}"
grep -q '^legacy-audit-line$' <<<"${merged_audit_migration_output}"
grep -q '^target-audit-line$' <<<"${merged_audit_migration_output}"
[[ "$(grep -c '^target-audit-line$' <<<"${merged_audit_migration_output}")" -eq 1 ]] || {
  echo "audit migration duplicated target history during restore" >&2
  exit 1
}

monitor_env_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    XDG_STATE_HOME="$5"
    WORKCELL_STATE_ROOT="${XDG_STATE_HOME}/workcell"
    WORKCELL_TARGET_STATE_ROOT="${WORKCELL_STATE_ROOT}/targets"
    SESSION_AUDIT_DIR="$2"
    SESSION_RECORD_PATH="$3"
    SESSION_RECORD_WRITTEN=1
    SESSION_RECORD_FINALIZED=0
    SESSION_META_PROFILE="wcl-detached-fixture"
    SESSION_META_TARGET_KIND="local_compat"
    SESSION_META_TARGET_PROVIDER="docker-desktop"
    SESSION_META_TARGET_ID="desktop-linux"
    SESSION_META_TARGET_ASSURANCE_CLASS="compat"
    write_session_monitor_env_file "$4"
    cat "$4"
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${DETACHED_STATE_DIR}" "${SESSIONS_DIR}/${DETACHED_SESSION}.json" "${DETACHED_STATE_DIR}/session-monitor.env" "${TMP_DIR}/monitor-xdg-state"
)"
grep -q '^SESSION_RECORD_PATH=' <<<"${monitor_env_output}"
grep -q '^SESSION_RECORD_WRITTEN=1$' <<<"${monitor_env_output}"
grep -q '^SESSION_RECORD_FINALIZED=0$' <<<"${monitor_env_output}"
grep -q '^SESSION_META_PROFILE=wcl-detached-fixture$' <<<"${monitor_env_output}"
grep -q '^SESSION_META_TARGET_KIND=local_compat$' <<<"${monitor_env_output}"
grep -q '^SESSION_META_TARGET_PROVIDER=docker-desktop$' <<<"${monitor_env_output}"
grep -q '^SESSION_META_TARGET_ID=desktop-linux$' <<<"${monitor_env_output}"
grep -q '^SESSION_META_TARGET_ASSURANCE_CLASS=compat$' <<<"${monitor_env_output}"
grep -q '^SESSION_MONITOR_READY_PATH=' <<<"${monitor_env_output}"
grep -q '^XDG_STATE_HOME='"${TMP_DIR}/monitor-xdg-state"'$' <<<"${monitor_env_output}"
grep -q '^WORKCELL_STATE_ROOT='"${TMP_DIR}/monitor-xdg-state/workcell"'$' <<<"${monitor_env_output}"
grep -q '^WORKCELL_TARGET_STATE_ROOT='"${TMP_DIR}/monitor-xdg-state/workcell/targets"'$' <<<"${monitor_env_output}"

monitor_ready_probe_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    READY_PATH="$2"
    (
      sleep 0.2
      : >"${READY_PATH}"
      sleep 2
    ) &
    MONITOR_PID="$!"
    wait_for_session_monitor_ready "${MONITOR_PID}" "${READY_PATH}"
    kill "${MONITOR_PID}" >/dev/null 2>&1 || true
    wait "${MONITOR_PID}" >/dev/null 2>&1 || true
    printf "ready_success=1\n"
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${DETACHED_STATE_DIR}/session-monitor.ready"
)"
grep -q '^ready_success=1$' <<<"${monitor_ready_probe_output}"

set +e
bash -lc '
  set -euo pipefail
  source "$1"
  trap - EXIT
  READY_PATH="$2"
  (
    sleep 0.2
  ) &
  MONITOR_PID="$!"
  wait_for_session_monitor_ready "${MONITOR_PID}" "${READY_PATH}"
' _ "${WORKCELL_FUNCTIONS_COPY}" "${DETACHED_STATE_DIR}/session-monitor.missing.ready" >/dev/null 2>&1
monitor_ready_failure_status=$?
set -e
if [[ "${monitor_ready_failure_status}" -eq 0 ]]; then
  echo "wait_for_session_monitor_ready unexpectedly accepted a monitor that never reported ready" >&2
  exit 1
fi

detached_summary_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    SESSION_ID="detached-summary"
    SESSION_META_TARGET_KIND="local_vm"
    SESSION_META_TARGET_PROVIDER="colima"
    SESSION_META_TARGET_ID="wcl-detached-summary"
    SESSION_META_TARGET_ASSURANCE_CLASS="strict"
    SESSION_META_RUNTIME_API="docker"
    SESSION_META_WORKSPACE_TRANSPORT="workspace-mount"
    SESSION_META_WORKSPACE="/tmp/detached-summary"
    SESSION_META_WORKSPACE_ORIGIN="/tmp/detached-summary"
    SESSION_META_WORKTREE_PATH="/tmp/detached-summary"
    SESSION_META_CURRENT_ASSURANCE="managed-mutable"
    session_git_branch=""
    ensure_session_meta_git_branch "${session_git_branch}"
    printf "session_id=%s\n" "${SESSION_ID}"
    emit_loaded_session_control_summary
  ' _ "${WORKCELL_FUNCTIONS_COPY}"
)"
test "$(grep -c '^git_branch=' <<<"${detached_summary_output}")" -eq 1
grep -q '^display_git_branch=none$' <<<"${detached_summary_output}"
grep -q '^git_branch=none$' <<<"${detached_summary_output}"

detached_summary_runtime_branch_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    SESSION_ID="detached-summary-runtime"
    SESSION_META_TARGET_KIND="local_vm"
    SESSION_META_TARGET_PROVIDER="colima"
    SESSION_META_TARGET_ID="wcl-detached-summary-runtime"
    SESSION_META_TARGET_ASSURANCE_CLASS="strict"
    SESSION_META_RUNTIME_API="docker"
    SESSION_META_WORKSPACE_TRANSPORT="workspace-mount"
    SESSION_META_WORKSPACE="/tmp/detached-summary-runtime"
    SESSION_META_WORKSPACE_ORIGIN="/tmp/detached-summary-runtime"
    SESSION_META_WORKTREE_PATH="/tmp/detached-summary-runtime"
    SESSION_META_GIT_BRANCH="runtime-branch"
    SESSION_META_CURRENT_ASSURANCE="managed-mutable"
    session_git_branch="host-branch"
    ensure_session_meta_git_branch "${session_git_branch}"
    printf "session_id=%s\n" "${SESSION_ID}"
    emit_loaded_session_control_summary
  ' _ "${WORKCELL_FUNCTIONS_COPY}"
)"
test "$(grep -c '^git_branch=' <<<"${detached_summary_runtime_branch_output}")" -eq 1
grep -q '^display_git_branch=runtime-branch$' <<<"${detached_summary_runtime_branch_output}"
grep -q '^git_branch=runtime-branch$' <<<"${detached_summary_runtime_branch_output}"

monitor_wait_status_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    COLIMA_PROFILE="wcl-detached-fixture"
    CONTAINER_NAME="workcell-session-fixture"
    RECORD_FILE="$2"
    run_profile_docker_command() {
      local profile="$1"
      shift
      printf "%s|%s\n" "${profile}" "$*" >>"${RECORD_FILE}"
      case "$1" in
        wait)
          return 1
          ;;
        inspect)
          printf "0\n"
          ;;
        *)
          return 1
          ;;
      esac
    }
    printf "wait_status=%s\n" "$(session_monitor_wait_status)"
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${DETACHED_STATE_DIR}/session-monitor-wait.record"
)"
grep -q '^wait_status=0$' <<<"${monitor_wait_status_output}"
grep -q '^wcl-detached-fixture|wait workcell-session-fixture$' "${DETACHED_STATE_DIR}/session-monitor-wait.record"
grep -q '^wcl-detached-fixture|inspect --format {{.State.ExitCode}} workcell-session-fixture$' "${DETACHED_STATE_DIR}/session-monitor-wait.record"

monitor_provider_backfill_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    FIXTURE_ROOT="$2"
    RECORD_FILE="$3"
    PROFILE_NAME="wcl-detached-desktop-fixture"
    SESSION_ID="detached-desktop-provider"
    STATE_FILE="${FIXTURE_ROOT}/session-monitor.env"
    WORKCELL_STATE_ROOT="${FIXTURE_ROOT}/workcell"
    WORKCELL_TARGET_STATE_ROOT="${WORKCELL_STATE_ROOT}/targets"
    COLIMA_STATE_ROOT="${FIXTURE_ROOT}/colima"
    COLIMA_PROFILE="${PROFILE_NAME}"
    CONTAINER_NAME="workcell-session-desktop"
    SESSION_AUDIT_DIR="${FIXTURE_ROOT}/audit"
    mkdir -p "${WORKCELL_STATE_ROOT}/targets/local_compat/docker-desktop/${PROFILE_NAME}/sessions" "${SESSION_AUDIT_DIR}"
    cat >"${WORKCELL_STATE_ROOT}/targets/local_compat/docker-desktop/${PROFILE_NAME}/sessions/${SESSION_ID}.json" <<EOF_JSON
{
  "version": 1,
  "session_id": "${SESSION_ID}",
  "profile": "${PROFILE_NAME}",
  "target_kind": "local_compat",
  "target_provider": "docker-desktop",
  "target_id": "desktop-linux",
  "target_assurance_class": "compat",
  "runtime_api": "docker",
  "workspace_transport": "workspace-mount",
  "agent": "codex",
  "mode": "strict",
  "status": "running",
  "ui": "cli",
  "execution_path": "managed-tier1",
  "workspace": "${FIXTURE_ROOT}/workspace",
  "workspace_origin": "${FIXTURE_ROOT}/workspace",
  "workspace_root": "${FIXTURE_ROOT}/workspace",
  "worktree_path": "${FIXTURE_ROOT}/workspace",
  "container_name": "${CONTAINER_NAME}",
  "session_audit_dir": "${SESSION_AUDIT_DIR}",
  "live_status": "running",
  "started_at": "2026-04-22T14:00:00Z",
  "current_assurance": "managed-mutable",
  "workspace_control_plane": "masked"
}
EOF_JSON
    cat >"${STATE_FILE}" <<EOF_STATE
SESSION_ID=${SESSION_ID}
COLIMA_PROFILE=${PROFILE_NAME}
CONTAINER_NAME=${CONTAINER_NAME}
EXECUTION_PATH=managed-tier1
SESSION_AUDIT_DIR=${SESSION_AUDIT_DIR}
WORKCELL_STATE_ROOT=${WORKCELL_STATE_ROOT}
WORKCELL_TARGET_STATE_ROOT=${WORKCELL_TARGET_STATE_ROOT}
COLIMA_STATE_ROOT=${COLIMA_STATE_ROOT}
EOF_STATE
    resolve_host_tool() { printf "/usr/bin/true\n"; }
    sanitize_host_docker_env() { :; }
    capture_session_audit_state() { :; }
    capture_session_file_trace() { :; }
    finalize_session_audit() { :; }
    run_workcell_docker_client_command() {
      printf "%s\n" "$*" >>"${RECORD_FILE}"
      case "$*" in
        *" wait "*) return 1 ;;
        *" inspect --format {{.State.ExitCode}} "*) printf "0\n" ;;
        *" rm -f "*) return 0 ;;
      esac
      return 0
    }
    session_monitor_main --state-file "${STATE_FILE}"
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${DETACHED_STATE_DIR}/session-monitor-provider" "${DETACHED_STATE_DIR}/session-monitor-provider.record"
)"
grep -q '^env DOCKER_CONTEXT=desktop-linux /usr/bin/true wait workcell-session-desktop$' "${DETACHED_STATE_DIR}/session-monitor-provider.record"
grep -q '^env DOCKER_CONTEXT=desktop-linux /usr/bin/true inspect --format {{.State.ExitCode}} workcell-session-desktop$' "${DETACHED_STATE_DIR}/session-monitor-provider.record"
grep -q '^env DOCKER_CONTEXT=desktop-linux /usr/bin/true rm -f workcell-session-desktop$' "${DETACHED_STATE_DIR}/session-monitor-provider.record"

START_PROFILE_RETRY_RECORD="${DETACHED_STATE_DIR}/start-profile-retry.record"
start_profile_retry_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    RECORD_FILE="$2"
    DEBUG_LOG_PATH="$3"
    COLIMA_PROFILE="start-retry-fixture"
    PROFILE_WORKSPACE_ROOT="/tmp/workspace"
    WORKSPACE="/tmp/workspace"
    COLIMA_CPU="4"
    COLIMA_MEMORY="6"
    COLIMA_DISK="60"
    ATTEMPT=0
    revalidate_recorded_host_output_path() { printf "%s\n" "$1"; }
    maybe_reap_stale_profile_processes() { :; }
    reap_stale_profile_processes() {
      printf "reap\n" >>"${RECORD_FILE}"
    }
    refresh_managed_profile() {
      printf "refresh|%s\n" "$1" >>"${RECORD_FILE}"
    }
    run_command_with_debug_log() {
      local label="$1"
      shift
      ATTEMPT=$((ATTEMPT + 1))
      printf "run|%s|%s\n" "${ATTEMPT}" "${label}" >>"${RECORD_FILE}"
      if [[ "${ATTEMPT}" -eq 1 ]]; then
        cat >>"${DEBUG_LOG_PATH}" <<EOF
time="2026-04-16T09:46:40-04:00" level=fatal msg="did not receive an event with the "running" status"
time="2026-04-16T09:46:40-04:00" level=fatal msg="error starting docker: error at '"'"'starting'"'"': exit status 1"
EOF
        LAST_COMMAND_DEBUG_CAPTURE_PATH="${DEBUG_LOG_PATH}"
        return 1
      fi
      return 0
    }
    start_managed_profile
    printf "attempts=%s\n" "${ATTEMPT}"
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${START_PROFILE_RETRY_RECORD}" "${DETACHED_STATE_DIR}/start-profile-retry.debug.log"
)"
grep -q '^attempts=2$' <<<"${start_profile_retry_output}"
grep -q '^run|1|colima-start$' "${START_PROFILE_RETRY_RECORD}"
grep -q '^run|2|colima-start$' "${START_PROFILE_RETRY_RECORD}"
grep -q '^refresh|Refreshing managed Colima profile start-retry-fixture after Colima failed during Docker startup\.$' "${START_PROFILE_RETRY_RECORD}"

session_final_status_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    SESSION_AUDIT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-stop-marker.XXXXXX")"
    printf "stop_failed=%s\n" "$(session_final_status_for_exit 1 stopping)"
    touch "$(session_stop_request_marker_path "${SESSION_AUDIT_DIR}")"
    printf "stop_requested=%s\n" "$(session_final_status_for_exit 1 running)"
    rm -f "$(session_stop_request_marker_path "${SESSION_AUDIT_DIR}")"
    printf "run_failed=%s\n" "$(session_final_status_for_exit 1 running)"
    printf "run_exited=%s\n" "$(session_final_status_for_exit 0 running)"
  ' _ "${WORKCELL_FUNCTIONS_COPY}"
)"
grep -q '^stop_failed=exited$' <<<"${session_final_status_output}"
grep -q '^stop_requested=exited$' <<<"${session_final_status_output}"
grep -q '^run_failed=failed$' <<<"${session_final_status_output}"
grep -q '^run_exited=exited$' <<<"${session_final_status_output}"

session_monitor_pid_zero_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    SESSION_META_MONITOR_PID="0"
    kill() {
      printf "kill-called\n"
      return 0
    }
    if session_monitor_pid_is_live; then
      printf "live\n"
    else
      printf "dead\n"
    fi
  ' _ "${WORKCELL_FUNCTIONS_COPY}"
)"
grep -q '^dead$' <<<"${session_monitor_pid_zero_output}"
if grep -q '^kill-called$' <<<"${session_monitor_pid_zero_output}"; then
  echo "Detached session monitor liveness still probes kill -0 0" >&2
  exit 1
fi

session_monitor_missing_audit_dir_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    SESSION_META_MONITOR_PID="4242"
    kill() {
      printf "kill-called\n"
      return 0
    }
    if session_monitor_pid_is_live; then
      printf "live\n"
    else
      printf "dead\n"
    fi
  ' _ "${WORKCELL_FUNCTIONS_COPY}"
)"
grep -q '^dead$' <<<"${session_monitor_missing_audit_dir_output}"
if grep -q '^kill-called$' <<<"${session_monitor_missing_audit_dir_output}"; then
  echo "Detached session monitor liveness accepted a monitor without detached provenance" >&2
  exit 1
fi

detached_running_race_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    SESSION_ID="detached-fixture"
    SESSION_MONITOR_PID="4242"
    SESSION_RECORD_PATH="/tmp/detached-fixture.json"
    LOAD_COUNT=0
    session_assurance_initial() { printf "managed-mutable\n"; }
    load_session_runtime_metadata() {
      LOAD_COUNT=$((LOAD_COUNT + 1))
      if [[ "${LOAD_COUNT}" -eq 1 ]]; then
        SESSION_META_STATUS="running"
        SESSION_META_LIVE_STATUS="running"
      else
        SESSION_META_STATUS="exited"
        SESSION_META_LIVE_STATUS="stopped"
      fi
    }
    write_session_record() { return 1; }
    if mark_detached_session_running; then
      printf "result=ok\n"
    else
      printf "result=fail\n"
    fi
    printf "loads=%s\n" "${LOAD_COUNT}"
  ' _ "${WORKCELL_FUNCTIONS_COPY}"
)"
grep -q '^result=ok$' <<<"${detached_running_race_output}"
grep -q '^loads=2$' <<<"${detached_running_race_output}"

existing_file_trace_capture_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    FILE_TRACE_LOG_PATH="$2"
    COLIMA_PROFILE="wcl-detached-fixture"
    SESSION_FILE_TRACE_CONTAINER_FILE="/var/tmp/workcell-file-trace.log"
    printf "preexisting-watch-start\n" >"${FILE_TRACE_LOG_PATH}"
    revalidate_recorded_host_output_path() { printf "%s\n" "$1"; }
    run_profile_docker_command() { return 1; }
    capture_session_file_trace "workcell-session-fixture"
    cat "${FILE_TRACE_LOG_PATH}"
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${DETACHED_STATE_DIR}/preexisting.file-trace.log"
)"
grep -q '^preexisting-watch-start$' <<<"${existing_file_trace_capture_output}"
if grep -q 'host-collect-missing' <<<"${existing_file_trace_capture_output}"; then
  echo "Detached session file-trace fallback overwrote an already-populated host log" >&2
  exit 1
fi

SESSION_SEND_FAILURE_RECORD="${DETACHED_STATE_DIR}/session-send.failure.record"
SESSION_SEND_SUCCESS_RECORD="${DETACHED_STATE_DIR}/session-send.success.record"
SESSION_SEND_STOPPED_RECORD="${DETACHED_STATE_DIR}/session-send.stopped.record"
set +e
bash -lc '
  set -euo pipefail
  source "$1"
  trap - EXIT
  RECORD_FILE="$2"
  HOST_DOCKER_BIN="/bin/false"
  resolve_host_tool() { printf "/bin/false\n"; }
  sanitize_host_docker_env() { :; }
  session_monitor_pid_is_live() { return 0; }
  load_session_runtime_metadata() {
    SESSION_META_PROFILE="wcl-detached-fixture"
    SESSION_META_CONTAINER_NAME="workcell-session-fixture"
    SESSION_META_MONITOR_PID="4242"
    SESSION_META_STATUS="running"
    SESSION_META_LIVE_STATUS="running"
  }
  append_session_control_audit_record() {
    printf "audit|%s|%s|%s|%s\n" "$1" "$2" "$3" "$4" >>"${RECORD_FILE}"
  }
  run_profile_docker_command() {
    local profile="$1"
    shift
    printf "transport|%s|%s\n" "${profile}" "$*" >>"${RECORD_FILE}"
    case "$1" in
      inspect)
        printf "running\n"
        ;;
      exec)
        return 17
        ;;
      *)
        return 1
        ;;
    esac
  }
  session_send_main --id detached-fixture --message alpha
' _ "${WORKCELL_FUNCTIONS_COPY}" "${SESSION_SEND_FAILURE_RECORD}" >/dev/null 2>&1
session_send_failure_status=$?
set -e
if [[ "${session_send_failure_status}" -ne 17 ]]; then
  echo "Expected detached session send failure to preserve the transport error status" >&2
  exit 1
fi
grep -q '^transport|wcl-detached-fixture|exec --user ' "${SESSION_SEND_FAILURE_RECORD}"
if grep -q '^audit|' "${SESSION_SEND_FAILURE_RECORD}"; then
  echo "Detached session send wrote an audit record before transport delivery succeeded" >&2
  exit 1
fi
if grep -q '/proc/1/fd/0' "${SESSION_SEND_FAILURE_RECORD}"; then
  echo "Detached session send fell back to PID 1 stdin instead of failing closed" >&2
  exit 1
fi
if ! grep -q '/state/tmp/workcell/session-stdin' "${SESSION_SEND_FAILURE_RECORD}"; then
  echo "Detached session send stopped targeting the runtime-user-owned FIFO path" >&2
  exit 1
fi

session_send_success_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    RECORD_FILE="$2"
    COLIMA_STATE_ROOT="$3"
    PROFILE_DIR="${COLIMA_STATE_ROOT}/wcl-detached-fixture"
    RECORD_PATH="${PROFILE_DIR}/sessions/detached-fixture.json"
    SESSION_AUDIT_DIR="${PROFILE_DIR}/session-audit.detached-fixture"
    HOST_DOCKER_BIN="/bin/false"
    mkdir -p "${PROFILE_DIR}/sessions" "${SESSION_AUDIT_DIR}"
    cat >"${RECORD_PATH}" <<EOF_JSON
{
  "version": 1,
  "session_id": "detached-fixture",
  "profile": "wcl-detached-fixture",
  "agent": "codex",
  "mode": "strict",
  "status": "running",
  "live_status": "running",
  "workspace": "/tmp/detached-fixture-workspace",
  "container_name": "workcell-session-fixture",
  "monitor_pid": "4242",
  "session_audit_dir": "${SESSION_AUDIT_DIR}",
  "started_at": "2026-04-08T14:00:00Z",
  "current_assurance": "managed-mutable",
  "initial_assurance": "managed-mutable"
}
EOF_JSON
    resolve_host_tool() { printf "/bin/false\n"; }
    sanitize_host_docker_env() { :; }
    session_monitor_pid_is_live() { return 0; }
    load_session_runtime_metadata() {
      SESSION_META_PROFILE="wcl-detached-fixture"
      SESSION_META_TARGET_KIND="local_vm"
      SESSION_META_TARGET_PROVIDER="colima"
      SESSION_META_TARGET_ID="wcl-detached-fixture"
      SESSION_META_TARGET_ASSURANCE_CLASS="strict"
      SESSION_META_RUNTIME_API="docker"
      SESSION_META_WORKSPACE_TRANSPORT="workspace-mount"
      SESSION_META_WORKSPACE="/tmp/detached-fixture-workspace"
      SESSION_META_WORKSPACE_ORIGIN="/tmp/detached-fixture-workspace"
      SESSION_META_WORKTREE_PATH="/tmp/detached-fixture-workspace"
      SESSION_META_CONTAINER_NAME="workcell-session-fixture"
      SESSION_META_MONITOR_PID="4242"
      SESSION_META_STATUS="running"
      SESSION_META_LIVE_STATUS="running"
      SESSION_META_CURRENT_ASSURANCE="managed-mutable"
    }
    append_session_control_audit_record() {
      printf "audit|%s|%s|%s|%s\n" "$1" "$2" "$3" "$4" >>"${RECORD_FILE}"
    }
    run_profile_docker_command() {
      local profile="$1"
      shift
      printf "transport|%s|%s\n" "${profile}" "$*" >>"${RECORD_FILE}"
      case "$1" in
        inspect)
          printf "running\n"
          ;;
      esac
    }
    session_send_main --id detached-fixture --no-newline --message beta
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${SESSION_SEND_SUCCESS_RECORD}" "${DETACHED_STATE_DIR}/session-send.success.root"
)"
grep -q '^session_id=detached-fixture$' <<<"${session_send_success_output}"
grep -q '^sent_bytes=4$' <<<"${session_send_success_output}"
grep -q '^status=running$' <<<"${session_send_success_output}"
grep -q '^live_status=running$' <<<"${session_send_success_output}"
grep -q '^control_mode=detached$' <<<"${session_send_success_output}"
grep -q '^target_summary=local_vm/colima/wcl-detached-fixture$' <<<"${session_send_success_output}"
grep -q '^workspace_transport=workspace-mount$' <<<"${session_send_success_output}"
grep -q '^display_workspace=/tmp/detached-fixture-workspace$' <<<"${session_send_success_output}"
grep -q '^display_worktree=/tmp/detached-fixture-workspace$' <<<"${session_send_success_output}"
grep -q '^display_git_branch=none$' <<<"${session_send_success_output}"
grep -q '^assurance=managed-mutable$' <<<"${session_send_success_output}"
grep -q '^transport|wcl-detached-fixture|exec --user ' "${SESSION_SEND_SUCCESS_RECORD}"
if grep -q '/proc/1/fd/0' "${SESSION_SEND_SUCCESS_RECORD}"; then
  echo "Detached session send still contains the PID 1 stdin fallback on the success path" >&2
  exit 1
fi
first_send_record="$(sed -n '1p' "${SESSION_SEND_SUCCESS_RECORD}")"
second_send_record="$(sed -n '2p' "${SESSION_SEND_SUCCESS_RECORD}")"
third_send_record="$(sed -n '3p' "${SESSION_SEND_SUCCESS_RECORD}")"
if [[ "${first_send_record}" != transport\|*inspect* ]]; then
  echo "Detached session send did not preflight the live detached container before delivery" >&2
  exit 1
fi
if [[ "${second_send_record}" != transport\|*exec* ]]; then
  echo "Detached session send did not deliver the payload before auditing it" >&2
  exit 1
fi
if [[ "${third_send_record}" != audit\|* ]]; then
  echo "Detached session send did not append the audit record after delivery" >&2
  exit 1
fi

set +e
bash -lc '
  set -euo pipefail
  source "$1"
  trap - EXIT
  RECORD_FILE="$2"
  HOST_DOCKER_BIN="/bin/false"
  resolve_host_tool() { printf "/bin/false\n"; }
  sanitize_host_docker_env() { :; }
  load_session_runtime_metadata() {
    SESSION_META_PROFILE="wcl-detached-fixture"
    SESSION_META_CONTAINER_NAME="workcell-session-fixture"
    SESSION_META_MONITOR_PID="4242"
    SESSION_META_STATUS="stopped"
    SESSION_META_LIVE_STATUS="stopped"
  }
  append_session_control_audit_record() {
    printf "audit|%s|%s|%s|%s\n" "$1" "$2" "$3" "$4" >>"${RECORD_FILE}"
  }
  run_profile_docker_command() {
    printf "unexpected-transport\n" >>"${RECORD_FILE}"
    return 0
  }
  session_send_main --id detached-fixture --message gamma
' _ "${WORKCELL_FUNCTIONS_COPY}" "${SESSION_SEND_STOPPED_RECORD}" >/dev/null 2>&1
session_send_stopped_status=$?
set -e
if [[ "${session_send_stopped_status}" -eq 0 ]]; then
  echo "Detached session send unexpectedly steered a stopped session record" >&2
  exit 1
fi
if [[ -f "${SESSION_SEND_STOPPED_RECORD}" ]] && grep -q . "${SESSION_SEND_STOPPED_RECORD}"; then
  echo "Detached session send touched transport or audit state for a stopped record" >&2
  exit 1
fi

SESSION_SEND_UNATTACHED_RECORD="${DETACHED_STATE_DIR}/session-send.unattached.record"
set +e
bash -lc '
  set -euo pipefail
  source "$1"
  trap - EXIT
  RECORD_FILE="$2"
  HOST_DOCKER_BIN="/bin/false"
  resolve_host_tool() { printf "/bin/false\n"; }
  sanitize_host_docker_env() { :; }
  load_session_runtime_metadata() {
    SESSION_META_PROFILE="wcl-detached-fixture"
    SESSION_META_CONTAINER_NAME="workcell-session-fixture"
    SESSION_META_MONITOR_PID=""
    SESSION_META_STATUS="running"
    SESSION_META_LIVE_STATUS="running"
  }
  append_session_control_audit_record() {
    printf "audit|%s|%s|%s|%s\n" "$1" "$2" "$3" "$4" >>"${RECORD_FILE}"
  }
  run_profile_docker_command() {
    printf "unexpected-transport\n" >>"${RECORD_FILE}"
    return 0
  }
  session_send_main --id detached-fixture --message delta
' _ "${WORKCELL_FUNCTIONS_COPY}" "${SESSION_SEND_UNATTACHED_RECORD}" >/dev/null 2>&1
session_send_unattached_status=$?
set -e
if [[ "${session_send_unattached_status}" -eq 0 ]]; then
  echo "Detached session send unexpectedly steered a session without detached host provenance" >&2
  exit 1
fi
if [[ -f "${SESSION_SEND_UNATTACHED_RECORD}" ]] && grep -q . "${SESSION_SEND_UNATTACHED_RECORD}"; then
  echo "Detached session send touched transport or audit state without detached host provenance" >&2
  exit 1
fi

SESSION_SEND_DEAD_MONITOR_RECORD="${DETACHED_STATE_DIR}/session-send.dead-monitor.record"
set +e
bash -lc '
  set -euo pipefail
  source "$1"
  trap - EXIT
  RECORD_FILE="$2"
  HOST_DOCKER_BIN="/bin/false"
  resolve_host_tool() { printf "/bin/false\n"; }
  sanitize_host_docker_env() { :; }
  session_monitor_pid_is_live() { return 1; }
  load_session_runtime_metadata() {
    SESSION_META_PROFILE="wcl-detached-fixture"
    SESSION_META_CONTAINER_NAME="workcell-session-fixture"
    SESSION_META_MONITOR_PID="4242"
    SESSION_META_STATUS="running"
    SESSION_META_LIVE_STATUS="running"
  }
  append_session_control_audit_record() {
    printf "audit|%s|%s|%s|%s\n" "$1" "$2" "$3" "$4" >>"${RECORD_FILE}"
  }
  run_profile_docker_command() {
    printf "unexpected-transport\n" >>"${RECORD_FILE}"
    return 0
  }
  session_send_main --id detached-fixture --message epsilon
' _ "${WORKCELL_FUNCTIONS_COPY}" "${SESSION_SEND_DEAD_MONITOR_RECORD}" >/dev/null 2>&1
session_send_dead_monitor_status=$?
set -e
if [[ "${session_send_dead_monitor_status}" -eq 0 ]]; then
  echo "Detached session send unexpectedly steered a session with a dead detached monitor" >&2
  exit 1
fi
if [[ -f "${SESSION_SEND_DEAD_MONITOR_RECORD}" ]] && grep -q . "${SESSION_SEND_DEAD_MONITOR_RECORD}"; then
  echo "Detached session send touched transport or audit state with a dead detached monitor" >&2
  exit 1
fi

SESSION_ATTACH_STOPPED_RECORD="${DETACHED_STATE_DIR}/session-attach.stopped.record"
set +e
bash -lc '
  set -euo pipefail
  source "$1"
  trap - EXIT
  RECORD_FILE="$2"
  HOST_DOCKER_BIN="/bin/false"
  resolve_host_tool() { printf "/bin/false\n"; }
  sanitize_host_docker_env() { :; }
  load_session_runtime_metadata() {
    SESSION_META_PROFILE="wcl-detached-fixture"
    SESSION_META_CONTAINER_NAME="workcell-session-fixture"
    SESSION_META_MONITOR_PID="4242"
    SESSION_META_STATUS="running"
    SESSION_META_LIVE_STATUS="running"
  }
  append_session_control_audit_record() {
    printf "audit|%s|%s|%s|%s\n" "$1" "$2" "$3" "$4" >>"${RECORD_FILE}"
  }
  run_profile_docker_command() {
    local profile="$1"
    shift
    printf "transport|%s|%s\n" "${profile}" "$*" >>"${RECORD_FILE}"
    case "$1" in
      inspect)
        printf "stopped\n"
        ;;
      *)
        return 1
        ;;
    esac
  }
  session_attach_main --id detached-fixture
' _ "${WORKCELL_FUNCTIONS_COPY}" "${SESSION_ATTACH_STOPPED_RECORD}" >/dev/null 2>&1
session_attach_stopped_status=$?
set -e
if [[ "${session_attach_stopped_status}" -eq 0 ]]; then
  echo "Detached session attach unexpectedly attached a stopped session" >&2
  exit 1
fi
if [[ -f "${SESSION_ATTACH_STOPPED_RECORD}" ]] && grep -q '^audit|' "${SESSION_ATTACH_STOPPED_RECORD}"; then
  echo "Detached session attach wrote an audit record before confirming a live container" >&2
  exit 1
fi
if [[ -f "${SESSION_ATTACH_STOPPED_RECORD}" ]] && grep -q 'attach ' "${SESSION_ATTACH_STOPPED_RECORD}"; then
  echo "Detached session attach attempted a transport attach after live-state preflight failed" >&2
  exit 1
fi

SESSION_ATTACH_FAILURE_RECORD="${DETACHED_STATE_DIR}/session-attach.failure.record"
set +e
bash -lc '
  set -euo pipefail
  source "$1"
  trap - EXIT
  RECORD_FILE="$2"
  HOST_DOCKER_BIN="/bin/false"
  resolve_host_tool() { printf "/bin/false\n"; }
  sanitize_host_docker_env() { :; }
  session_monitor_pid_is_live() { return 0; }
  load_session_runtime_metadata() {
    SESSION_META_PROFILE="wcl-detached-fixture"
    SESSION_META_CONTAINER_NAME="workcell-session-fixture"
    SESSION_META_MONITOR_PID="4242"
    SESSION_META_STATUS="running"
    SESSION_META_LIVE_STATUS="running"
  }
  append_session_control_audit_record() {
    printf "audit|%s|%s|%s|%s\n" "$1" "$2" "$3" "$4" >>"${RECORD_FILE}"
  }
  run_profile_docker_command() {
    local profile="$1"
    shift
    printf "transport|%s|%s\n" "${profile}" "$*" >>"${RECORD_FILE}"
    case "$1" in
      inspect)
        printf "running\n"
        ;;
      attach)
        return 23
        ;;
      *)
        return 1
        ;;
    esac
  }
  session_attach_main --id detached-fixture --no-stdin
' _ "${WORKCELL_FUNCTIONS_COPY}" "${SESSION_ATTACH_FAILURE_RECORD}" >/dev/null 2>&1
session_attach_failure_status=$?
set -e
if [[ "${session_attach_failure_status}" -ne 23 ]]; then
  echo "Detached session attach did not preserve the attach transport error" >&2
  exit 1
fi
grep -q '^audit|wcl-detached-fixture|detached-fixture|attach-attempt|' "${SESSION_ATTACH_FAILURE_RECORD}"
if grep -Eq '^audit\|wcl-detached-fixture\|detached-fixture\|attach\|' "${SESSION_ATTACH_FAILURE_RECORD}"; then
  echo "Detached session attach recorded a successful attach event after transport failure" >&2
  exit 1
fi

SESSION_STOP_STOPPED_RECORD="${DETACHED_STATE_DIR}/session-stop.stopped.record"
set +e
bash -lc '
  set -euo pipefail
  source "$1"
  trap - EXIT
  RECORD_FILE="$2"
  HOST_DOCKER_BIN="/bin/false"
  resolve_host_tool() { printf "/bin/false\n"; }
  sanitize_host_docker_env() { :; }
  load_session_runtime_metadata() {
    SESSION_META_PROFILE="wcl-detached-fixture"
    SESSION_META_CONTAINER_NAME="workcell-session-fixture"
    SESSION_META_MONITOR_PID="4242"
    SESSION_META_STATUS="running"
    SESSION_META_LIVE_STATUS="running"
  }
  append_session_control_audit_record() {
    printf "audit|%s|%s|%s|%s\n" "$1" "$2" "$3" "$4" >>"${RECORD_FILE}"
  }
  write_session_record() {
    printf "record|%s|%s\n" "$1" "$2" >>"${RECORD_FILE}"
  }
  run_profile_docker_command() {
    local profile="$1"
    shift
    printf "transport|%s|%s\n" "${profile}" "$*" >>"${RECORD_FILE}"
    case "$1" in
      inspect)
        printf "stopped\n"
        ;;
      *)
        return 1
        ;;
    esac
  }
  session_stop_main --id detached-fixture
' _ "${WORKCELL_FUNCTIONS_COPY}" "${SESSION_STOP_STOPPED_RECORD}" >/dev/null 2>&1
session_stop_stopped_status=$?
set -e
if [[ "${session_stop_stopped_status}" -eq 0 ]]; then
  echo "Detached session stop unexpectedly signaled a stopped session" >&2
  exit 1
fi
if [[ -f "${SESSION_STOP_STOPPED_RECORD}" ]] && grep -Eq '^(audit|record)\|' "${SESSION_STOP_STOPPED_RECORD}"; then
  echo "Detached session stop mutated audit or record state before confirming a live container" >&2
  exit 1
fi

SESSION_STOP_DEAD_MONITOR_RECORD="${DETACHED_STATE_DIR}/session-stop.dead-monitor.record"
SESSION_STOP_DEAD_MONITOR_AUDIT_DIR="${DETACHED_STATE_DIR}/session-stop.dead-monitor.audit"
mkdir -p "${SESSION_STOP_DEAD_MONITOR_AUDIT_DIR}"
stop_dead_monitor_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    RECORD_FILE="$2"
    AUDIT_DIR="$3"
    HOST_DOCKER_BIN="/bin/false"
    LOAD_COUNT=0
    resolve_host_tool() { printf "/bin/false\n"; }
    sanitize_host_docker_env() { :; }
    resolve_host_output_candidate() { printf "%s\n" "$1"; }
    exit() { return "${1:-0}"; }
    session_monitor_pid_is_live() { return 1; }
    session_container_exit_code() { printf "1\n"; }
    load_session_runtime_metadata() {
      LOAD_COUNT=$((LOAD_COUNT + 1))
      SESSION_META_PROFILE="wcl-detached-fixture"
      SESSION_META_CONTAINER_NAME="workcell-session-fixture"
      SESSION_META_MONITOR_PID="4242"
      SESSION_META_STATUS="running"
      SESSION_META_LIVE_STATUS="running"
      SESSION_META_CURRENT_ASSURANCE="managed-mutable"
      SESSION_META_SESSION_AUDIT_DIR="${AUDIT_DIR}"
    }
    append_session_control_audit_record() {
      printf "audit|%s|%s|%s|%s\n" "$1" "$2" "$3" "$4" >>"${RECORD_FILE}"
    }
    write_session_record() {
      printf "record|%s|%s|%s|%s\n" "$1" "$2" "$3" "$4" >>"${RECORD_FILE}"
    }
    run_profile_docker_command() {
      local profile="$1"
      shift
      printf "transport|%s|%s\n" "${profile}" "$*" >>"${RECORD_FILE}"
      case "$1" in
        inspect)
          printf "running\n"
          ;;
        stop)
          return 0
          ;;
        *)
          return 1
          ;;
      esac
    }
    session_stop_main --id detached-fixture
    if [[ -e "$(session_stop_request_marker_path "${AUDIT_DIR}")" ]]; then
      printf "marker=present\n"
    else
      printf "marker=cleared\n"
    fi
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${SESSION_STOP_DEAD_MONITOR_RECORD}" "${SESSION_STOP_DEAD_MONITOR_AUDIT_DIR}"
)"
grep -q '^session_id=detached-fixture$' <<<"${stop_dead_monitor_output}"
grep -q '^stop_requested=1$' <<<"${stop_dead_monitor_output}"
grep -q '^marker=cleared$' <<<"${stop_dead_monitor_output}"
grep -q '^audit|wcl-detached-fixture|detached-fixture|stop-request|' "${SESSION_STOP_DEAD_MONITOR_RECORD}"
grep -q '^audit|wcl-detached-fixture|detached-fixture|exit|source=host-stop-fallback' "${SESSION_STOP_DEAD_MONITOR_RECORD}"
grep -q '^record|.*/detached-fixture\.json|status=exited|live_status=stopped|observed_at=' "${SESSION_STOP_DEAD_MONITOR_RECORD}"

SESSION_STOP_ALREADY_STOPPED_RECORD="${DETACHED_STATE_DIR}/session-stop.already-stopped.record"
SESSION_STOP_ALREADY_STOPPED_AUDIT_DIR="${DETACHED_STATE_DIR}/session-stop.already-stopped.audit"
mkdir -p "${SESSION_STOP_ALREADY_STOPPED_AUDIT_DIR}"
stop_already_stopped_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    RECORD_FILE="$2"
    AUDIT_DIR="$3"
    HOST_DOCKER_BIN="/bin/false"
    resolve_host_tool() { printf "/bin/false\n"; }
    sanitize_host_docker_env() { :; }
    resolve_host_output_candidate() { printf "%s\n" "$1"; }
    exit() { return "${1:-0}"; }
    session_monitor_pid_is_live() { return 1; }
    session_container_exit_code() { printf "1\n"; }
    load_session_runtime_metadata() {
      SESSION_META_PROFILE="wcl-detached-fixture"
      SESSION_META_CONTAINER_NAME="workcell-session-fixture"
      SESSION_META_MONITOR_PID="4242"
      SESSION_META_STATUS="stopping"
      SESSION_META_LIVE_STATUS="stopping"
      SESSION_META_CURRENT_ASSURANCE="managed-mutable"
      SESSION_META_SESSION_AUDIT_DIR="${AUDIT_DIR}"
    }
    append_session_control_audit_record() {
      printf "audit|%s|%s|%s|%s\n" "$1" "$2" "$3" "$4" >>"${RECORD_FILE}"
    }
    write_session_record() {
      printf "record|%s|%s|%s|%s\n" "$1" "$2" "$3" "$4" >>"${RECORD_FILE}"
    }
    run_profile_docker_command() {
      local profile="$1"
      shift
      printf "transport|%s|%s\n" "${profile}" "$*" >>"${RECORD_FILE}"
      case "$1" in
        inspect)
          printf "stopped\n"
          ;;
        *)
          return 1
          ;;
      esac
    }
    session_stop_main --id detached-fixture
    if [[ -e "$(session_stop_request_marker_path "${AUDIT_DIR}")" ]]; then
      printf "marker=present\n"
    else
      printf "marker=cleared\n"
    fi
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${SESSION_STOP_ALREADY_STOPPED_RECORD}" "${SESSION_STOP_ALREADY_STOPPED_AUDIT_DIR}"
)"
grep -q '^session_id=detached-fixture$' <<<"${stop_already_stopped_output}"
grep -q '^stop_requested=1$' <<<"${stop_already_stopped_output}"
grep -q '^marker=cleared$' <<<"${stop_already_stopped_output}"
grep -q '^audit|wcl-detached-fixture|detached-fixture|stop-request|' "${SESSION_STOP_ALREADY_STOPPED_RECORD}"
grep -q '^audit|wcl-detached-fixture|detached-fixture|exit|source=host-stop-fallback' "${SESSION_STOP_ALREADY_STOPPED_RECORD}"
grep -q '^record|.*/detached-fixture\.json|status=exited|live_status=stopped|observed_at=' "${SESSION_STOP_ALREADY_STOPPED_RECORD}"
if grep -q '^transport|wcl-detached-fixture|stop ' "${SESSION_STOP_ALREADY_STOPPED_RECORD}"; then
  echo "Detached session stop tried to stop an already-stopped container during repair" >&2
  exit 1
fi

SESSION_STOP_ALREADY_STOPPED_RUNNING_RECORD="${DETACHED_STATE_DIR}/session-stop.already-stopped-running.record"
SESSION_STOP_ALREADY_STOPPED_RUNNING_AUDIT_DIR="${DETACHED_STATE_DIR}/session-stop.already-stopped-running.audit"
mkdir -p "${SESSION_STOP_ALREADY_STOPPED_RUNNING_AUDIT_DIR}"
stop_already_stopped_running_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    RECORD_FILE="$2"
    AUDIT_DIR="$3"
    HOST_DOCKER_BIN="/bin/false"
    resolve_host_tool() { printf "/bin/false\n"; }
    sanitize_host_docker_env() { :; }
    resolve_host_output_candidate() { printf "%s\n" "$1"; }
    exit() { return "${1:-0}"; }
    session_monitor_pid_is_live() { return 0; }
    session_container_exit_code() { printf "1\n"; }
    load_session_runtime_metadata() {
      SESSION_META_PROFILE="wcl-detached-fixture"
      SESSION_META_CONTAINER_NAME="workcell-session-fixture"
      SESSION_META_MONITOR_PID="4242"
      SESSION_META_STATUS="running"
      SESSION_META_LIVE_STATUS="running"
      SESSION_META_CURRENT_ASSURANCE="managed-mutable"
      SESSION_META_SESSION_AUDIT_DIR="${AUDIT_DIR}"
    }
    append_session_control_audit_record() {
      printf "audit|%s|%s|%s|%s\n" "$1" "$2" "$3" "$4" >>"${RECORD_FILE}"
    }
    write_session_record() {
      printf "record|%s|%s|%s|%s\n" "$1" "$2" "$3" "$4" >>"${RECORD_FILE}"
    }
    run_profile_docker_command() {
      local profile="$1"
      shift
      printf "transport|%s|%s\n" "${profile}" "$*" >>"${RECORD_FILE}"
      case "$1" in
        inspect)
          printf "stopped\n"
          ;;
        *)
          return 1
          ;;
      esac
    }
    session_stop_main --id detached-fixture
    if [[ -e "$(session_stop_request_marker_path "${AUDIT_DIR}")" ]]; then
      printf "marker=present\n"
    else
      printf "marker=cleared\n"
    fi
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${SESSION_STOP_ALREADY_STOPPED_RUNNING_RECORD}" "${SESSION_STOP_ALREADY_STOPPED_RUNNING_AUDIT_DIR}"
)"
grep -q '^session_id=detached-fixture$' <<<"${stop_already_stopped_running_output}"
grep -q '^stop_requested=1$' <<<"${stop_already_stopped_running_output}"
grep -q '^marker=cleared$' <<<"${stop_already_stopped_running_output}"
if grep -q '^audit|' "${SESSION_STOP_ALREADY_STOPPED_RUNNING_RECORD}"; then
  echo "Detached session stop should not append fallback audit records when the monitor is still live" >&2
  exit 1
fi
if grep -q '^transport|wcl-detached-fixture|stop ' "${SESSION_STOP_ALREADY_STOPPED_RUNNING_RECORD}"; then
  echo "Detached session stop tried to stop an already-stopped running-status container while the monitor was still live" >&2
  exit 1
fi

SESSION_DELETE_CLEANUP_RECORD="${DETACHED_STATE_DIR}/session-delete.cleanup.record"
SESSION_DELETE_CLEANUP_ROOT="${DETACHED_STATE_DIR}/session-delete.cleanup.root"
session_delete_cleanup_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    COLIMA_STATE_ROOT="$2"
    RECORD_FILE="$3"
    PROFILE_DIR="${COLIMA_STATE_ROOT}/wcl-detached-fixture"
    RECORD_PATH="${PROFILE_DIR}/sessions/detached-fixture.json"
    DEBUG_LOG="${PROFILE_DIR}/detached.debug.log"
    FILE_TRACE_LOG="${PROFILE_DIR}/detached.file-trace.log"
    TRANSCRIPT_LOG="${PROFILE_DIR}/detached.transcript.log"
    SESSION_AUDIT_DIR="${PROFILE_DIR}/session-audit.detached-fixture"
    mkdir -p "${PROFILE_DIR}/sessions"
    : >"${PROFILE_DIR}/docker.sock"
    mkdir -p "${SESSION_AUDIT_DIR}"
    cat >"${RECORD_PATH}" <<EOF_JSON
{
  "version": 1,
  "session_id": "detached-fixture",
  "profile": "wcl-detached-fixture",
  "agent": "codex",
  "mode": "strict",
  "status": "exited",
  "live_status": "stopped",
  "workspace": "/tmp/detached-fixture-workspace",
  "container_name": "workcell-session-fixture",
  "started_at": "2026-04-08T14:00:00Z"
}
EOF_JSON
    printf "debug\n" >"${DEBUG_LOG}"
    printf "trace\n" >"${FILE_TRACE_LOG}"
    printf "transcript\n" >"${TRANSCRIPT_LOG}"
    HOST_DOCKER_BIN="/bin/false"
    resolve_host_tool() { printf "/bin/false\n"; }
    sanitize_host_docker_env() { :; }
    revalidate_recorded_host_output_path() { printf "%s\n" "$1"; }
    load_session_runtime_metadata() {
      SESSION_META_PROFILE="wcl-detached-fixture"
      SESSION_META_CONTAINER_NAME="workcell-session-fixture"
      SESSION_META_STATUS="exited"
      SESSION_META_LIVE_STATUS="stopped"
      SESSION_META_DEBUG_LOG_PATH="${DEBUG_LOG}"
      SESSION_META_FILE_TRACE_LOG_PATH="${FILE_TRACE_LOG}"
      SESSION_META_SESSION_AUDIT_DIR="${SESSION_AUDIT_DIR}"
      SESSION_META_TRANSCRIPT_LOG_PATH="${TRANSCRIPT_LOG}"
    }
    run_profile_docker_command() {
      local profile="$1"
      shift
      printf "transport|%s|%s\n" "${profile}" "$*" >>"${RECORD_FILE}"
      case "$1" in
        inspect)
          printf "stopped\n"
          ;;
        rm)
          return 0
          ;;
        *)
          return 1
          ;;
      esac
    }
    session_delete_main --id detached-fixture
    test ! -e "${RECORD_PATH}"
    test ! -e "${DEBUG_LOG}"
    test ! -e "${FILE_TRACE_LOG}"
    test ! -e "${SESSION_AUDIT_DIR}"
    test ! -e "${TRANSCRIPT_LOG}"
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${SESSION_DELETE_CLEANUP_ROOT}" "${SESSION_DELETE_CLEANUP_RECORD}"
)"
grep -q '^session_id=detached-fixture$' <<<"${session_delete_cleanup_output}"
grep -q '^deleted=1$' <<<"${session_delete_cleanup_output}"
grep -q '^removed=record,container,session_audit_dir,debug_log,file_trace_log,transcript_log$' <<<"${session_delete_cleanup_output}"
grep -q '^kept=none$' <<<"${session_delete_cleanup_output}"
grep -q '^missing=none$' <<<"${session_delete_cleanup_output}"
grep -q '^unavailable=none$' <<<"${session_delete_cleanup_output}"
grep -q '^transport|wcl-detached-fixture|inspect --format ' "${SESSION_DELETE_CLEANUP_RECORD}"
grep -q '^transport|wcl-detached-fixture|rm -f workcell-session-fixture$' "${SESSION_DELETE_CLEANUP_RECORD}"

SESSION_DELETE_RECORD_ONLY_RECORD="${DETACHED_STATE_DIR}/session-delete.record-only.record"
SESSION_DELETE_RECORD_ONLY_ROOT="${DETACHED_STATE_DIR}/session-delete.record-only.root"
session_delete_record_only_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    COLIMA_STATE_ROOT="$2"
    RECORD_FILE="$3"
    PROFILE_DIR="${COLIMA_STATE_ROOT}/wcl-detached-fixture"
    RECORD_PATH="${PROFILE_DIR}/sessions/detached-fixture.json"
    DEBUG_LOG="${PROFILE_DIR}/detached.debug.log"
    FILE_TRACE_LOG="${PROFILE_DIR}/detached.file-trace.log"
    TRANSCRIPT_LOG="${PROFILE_DIR}/detached.transcript.log"
    SESSION_AUDIT_DIR="${PROFILE_DIR}/session-audit.detached-fixture"
    mkdir -p "${PROFILE_DIR}/sessions"
    : >"${PROFILE_DIR}/docker.sock"
    mkdir -p "${SESSION_AUDIT_DIR}"
    cat >"${RECORD_PATH}" <<EOF_JSON
{
  "version": 1,
  "session_id": "detached-fixture",
  "profile": "wcl-detached-fixture",
  "agent": "codex",
  "mode": "strict",
  "status": "exited",
  "live_status": "stopped",
  "workspace": "/tmp/detached-fixture-workspace",
  "container_name": "workcell-session-fixture",
  "started_at": "2026-04-08T14:00:00Z"
}
EOF_JSON
    printf "debug\n" >"${DEBUG_LOG}"
    printf "trace\n" >"${FILE_TRACE_LOG}"
    printf "transcript\n" >"${TRANSCRIPT_LOG}"
    HOST_DOCKER_BIN="/bin/false"
    resolve_host_tool() { printf "/bin/false\n"; }
    sanitize_host_docker_env() { :; }
    revalidate_recorded_host_output_path() { printf "%s\n" "$1"; }
    load_session_runtime_metadata() {
      SESSION_META_PROFILE="wcl-detached-fixture"
      SESSION_META_CONTAINER_NAME="workcell-session-fixture"
      SESSION_META_STATUS="exited"
      SESSION_META_LIVE_STATUS="stopped"
      SESSION_META_DEBUG_LOG_PATH="${DEBUG_LOG}"
      SESSION_META_FILE_TRACE_LOG_PATH="${FILE_TRACE_LOG}"
      SESSION_META_SESSION_AUDIT_DIR="${SESSION_AUDIT_DIR}"
      SESSION_META_TRANSCRIPT_LOG_PATH="${TRANSCRIPT_LOG}"
    }
    run_profile_docker_command() {
      local profile="$1"
      shift
      printf "transport|%s|%s\n" "${profile}" "$*" >>"${RECORD_FILE}"
      case "$1" in
        inspect)
          printf "stopped\n"
          ;;
        *)
          return 1
          ;;
      esac
    }
    session_delete_main --id detached-fixture --record-only
    test ! -e "${RECORD_PATH}"
    test -e "${DEBUG_LOG}"
    test -e "${FILE_TRACE_LOG}"
    test -e "${SESSION_AUDIT_DIR}"
    test -e "${TRANSCRIPT_LOG}"
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${SESSION_DELETE_RECORD_ONLY_ROOT}" "${SESSION_DELETE_RECORD_ONLY_RECORD}"
)"
grep -q '^deleted=1$' <<<"${session_delete_record_only_output}"
grep -q '^record_only=1$' <<<"${session_delete_record_only_output}"
grep -q '^removed=record$' <<<"${session_delete_record_only_output}"
grep -q '^kept=container,session_audit_dir,debug_log,file_trace_log,transcript_log$' <<<"${session_delete_record_only_output}"
if grep -q '^transport|wcl-detached-fixture|rm -f ' "${SESSION_DELETE_RECORD_ONLY_RECORD}"; then
  echo "session delete --record-only unexpectedly removed a container" >&2
  exit 1
fi

SESSION_DELETE_DRY_RUN_RECORD="${DETACHED_STATE_DIR}/session-delete.dry-run.record"
SESSION_DELETE_DRY_RUN_ROOT="${DETACHED_STATE_DIR}/session-delete.dry-run.root"
session_delete_dry_run_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    COLIMA_STATE_ROOT="$2"
    RECORD_FILE="$3"
    PROFILE_DIR="${COLIMA_STATE_ROOT}/wcl-detached-fixture"
    RECORD_PATH="${PROFILE_DIR}/sessions/detached-fixture.json"
    DEBUG_LOG="${PROFILE_DIR}/detached.debug.log"
    FILE_TRACE_LOG="${PROFILE_DIR}/detached.file-trace.log"
    TRANSCRIPT_LOG="${PROFILE_DIR}/detached.transcript.log"
    SESSION_AUDIT_DIR="${PROFILE_DIR}/session-audit.detached-fixture"
    mkdir -p "${PROFILE_DIR}/sessions"
    : >"${PROFILE_DIR}/docker.sock"
    mkdir -p "${SESSION_AUDIT_DIR}"
    cat >"${RECORD_PATH}" <<EOF_JSON
{
  "version": 1,
  "session_id": "detached-fixture",
  "profile": "wcl-detached-fixture",
  "agent": "codex",
  "mode": "strict",
  "status": "failed",
  "live_status": "stopped",
  "workspace": "/tmp/detached-fixture-workspace",
  "container_name": "workcell-session-fixture",
  "started_at": "2026-04-08T14:00:00Z"
}
EOF_JSON
    printf "debug\n" >"${DEBUG_LOG}"
    printf "trace\n" >"${FILE_TRACE_LOG}"
    printf "transcript\n" >"${TRANSCRIPT_LOG}"
    HOST_DOCKER_BIN="/bin/false"
    resolve_host_tool() { printf "/bin/false\n"; }
    sanitize_host_docker_env() { :; }
    revalidate_recorded_host_output_path() { printf "%s\n" "$1"; }
    load_session_runtime_metadata() {
      SESSION_META_PROFILE="wcl-detached-fixture"
      SESSION_META_CONTAINER_NAME="workcell-session-fixture"
      SESSION_META_STATUS="failed"
      SESSION_META_LIVE_STATUS="stopped"
      SESSION_META_DEBUG_LOG_PATH="${DEBUG_LOG}"
      SESSION_META_FILE_TRACE_LOG_PATH="${FILE_TRACE_LOG}"
      SESSION_META_SESSION_AUDIT_DIR="${SESSION_AUDIT_DIR}"
      SESSION_META_TRANSCRIPT_LOG_PATH="${TRANSCRIPT_LOG}"
    }
    run_profile_docker_command() {
      local profile="$1"
      shift
      printf "transport|%s|%s\n" "${profile}" "$*" >>"${RECORD_FILE}"
      case "$1" in
        inspect)
          printf "stopped\n"
          ;;
        *)
          return 1
          ;;
      esac
    }
    session_delete_main --id detached-fixture --dry-run
    test -e "${RECORD_PATH}"
    test -e "${DEBUG_LOG}"
    test -e "${FILE_TRACE_LOG}"
    test -e "${SESSION_AUDIT_DIR}"
    test -e "${TRANSCRIPT_LOG}"
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${SESSION_DELETE_DRY_RUN_ROOT}" "${SESSION_DELETE_DRY_RUN_RECORD}"
)"
grep -q '^deleted=0$' <<<"${session_delete_dry_run_output}"
grep -q '^dry_run=1$' <<<"${session_delete_dry_run_output}"
grep -q '^would_remove=record,container,session_audit_dir,debug_log,file_trace_log,transcript_log$' <<<"${session_delete_dry_run_output}"
if grep -q '^transport|wcl-detached-fixture|rm -f ' "${SESSION_DELETE_DRY_RUN_RECORD}"; then
  echo "session delete --dry-run unexpectedly removed a container" >&2
  exit 1
fi

SESSION_DELETE_LIVE_CONTAINER_RECORD="${DETACHED_STATE_DIR}/session-delete.live-container.record"
set +e
bash -lc '
  set -euo pipefail
  source "$1"
  trap - EXIT
  COLIMA_STATE_ROOT="$2"
  RECORD_FILE="$3"
  PROFILE_DIR="${COLIMA_STATE_ROOT}/wcl-detached-fixture"
  RECORD_PATH="${PROFILE_DIR}/sessions/detached-fixture.json"
  DEBUG_LOG="${PROFILE_DIR}/detached.debug.log"
  mkdir -p "${PROFILE_DIR}/sessions"
  : >"${PROFILE_DIR}/docker.sock"
  cat >"${RECORD_PATH}" <<EOF_JSON
{
  "version": 1,
  "session_id": "detached-fixture",
  "profile": "wcl-detached-fixture",
  "agent": "codex",
  "mode": "strict",
  "status": "exited",
  "live_status": "stopped",
  "workspace": "/tmp/detached-fixture-workspace",
  "container_name": "workcell-session-fixture",
  "started_at": "2026-04-08T14:00:00Z"
}
EOF_JSON
  printf "debug\n" >"${DEBUG_LOG}"
  HOST_DOCKER_BIN="/bin/false"
  resolve_host_tool() { printf "/bin/false\n"; }
  sanitize_host_docker_env() { :; }
  revalidate_recorded_host_output_path() { printf "%s\n" "$1"; }
  load_session_runtime_metadata() {
    SESSION_META_PROFILE="wcl-detached-fixture"
    SESSION_META_CONTAINER_NAME="workcell-session-fixture"
    SESSION_META_MONITOR_PID="4242"
    SESSION_META_STATUS="exited"
    SESSION_META_LIVE_STATUS="stopped"
    SESSION_META_DEBUG_LOG_PATH="${DEBUG_LOG}"
  }
  run_profile_docker_command() {
    local profile="$1"
    shift
    printf "transport|%s|%s\n" "${profile}" "$*" >>"${RECORD_FILE}"
    case "$1" in
      inspect)
        printf "running\n"
        ;;
      *)
        return 1
        ;;
    esac
  }
  session_delete_main --id detached-fixture
' _ "${WORKCELL_FUNCTIONS_COPY}" "${DETACHED_STATE_DIR}/session-delete.live-container.root" "${SESSION_DELETE_LIVE_CONTAINER_RECORD}" >/dev/null 2>&1
session_delete_live_container_status=$?
set -e
if [[ "${session_delete_live_container_status}" -eq 0 ]]; then
  echo "session delete unexpectedly accepted a live container cleanup" >&2
  exit 1
fi
if grep -q '^transport|wcl-detached-fixture|rm -f ' "${SESSION_DELETE_LIVE_CONTAINER_RECORD}"; then
  echo "session delete tried to remove a running container" >&2
  exit 1
fi

SESSION_LIFECYCLE_RECORD="${DETACHED_STATE_DIR}/session-lifecycle.record"
SESSION_LIFECYCLE_ROOT="${DETACHED_STATE_DIR}/session-lifecycle.root"
SESSION_LIFECYCLE_AUDIT_LOG="${SESSION_LIFECYCLE_ROOT}/wcl-detached-lifecycle/workcell.audit.log"
session_lifecycle_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    COLIMA_STATE_ROOT="$2"
    RECORD_FILE="$3"
    WORKSPACE_PATH="$4"
    PROFILE_DIR="${COLIMA_STATE_ROOT}/wcl-detached-lifecycle"
    SESSION_ID="detached-lifecycle"
    RECORD_PATH="${PROFILE_DIR}/sessions/${SESSION_ID}.json"
    AUDIT_LOG="${PROFILE_DIR}/workcell.audit.log"
    DEBUG_LOG="${PROFILE_DIR}/detached.debug.log"
    FILE_TRACE_LOG="${PROFILE_DIR}/detached.file-trace.log"
    TRANSCRIPT_LOG="${PROFILE_DIR}/detached.transcript.log"
    SESSION_AUDIT_DIR="${PROFILE_DIR}/session-audit.${SESSION_ID}"
    CONTAINER_STATE_FILE="${PROFILE_DIR}/container.state"
    mkdir -p "${PROFILE_DIR}/sessions" "${SESSION_AUDIT_DIR}"
    : >"${PROFILE_DIR}/docker.sock"
    printf "running\n" >"${CONTAINER_STATE_FILE}"
    printf "debug\n" >"${DEBUG_LOG}"
    printf "trace\n" >"${FILE_TRACE_LOG}"
    printf "transcript\n" >"${TRANSCRIPT_LOG}"
    cat >"${RECORD_PATH}" <<EOF_JSON
{
  "version": 1,
  "session_id": "${SESSION_ID}",
  "profile": "wcl-detached-lifecycle",
  "agent": "codex",
  "mode": "strict",
  "status": "running",
  "live_status": "running",
  "ui": "cli",
  "execution_path": "managed-tier1",
  "workspace": "${WORKSPACE_PATH}",
  "workspace_origin": "${WORKSPACE_PATH}",
  "worktree_path": "${WORKSPACE_PATH}",
  "container_name": "workcell-session-fixture",
  "monitor_pid": "4242",
  "session_audit_dir": "${SESSION_AUDIT_DIR}",
  "audit_log_path": "${AUDIT_LOG}",
  "debug_log_path": "${DEBUG_LOG}",
  "file_trace_log_path": "${FILE_TRACE_LOG}",
  "transcript_log_path": "${TRANSCRIPT_LOG}",
  "started_at": "2026-04-08T15:00:00Z",
  "current_assurance": "managed-mutable",
  "initial_assurance": "managed-mutable",
  "workspace_control_plane": "masked"
}
EOF_JSON
    HOST_DOCKER_BIN="/bin/false"
    resolve_host_tool() { printf "/bin/false\n"; }
    sanitize_host_docker_env() { :; }
    revalidate_recorded_host_output_path() { printf "%s\n" "$1"; }
    exit() { return "${1:-0}"; }
    session_monitor_pid_is_live() {
      [[ "$(<"${CONTAINER_STATE_FILE}")" == "running" ]]
    }
    session_container_exit_code() { printf "0\n"; }
    run_profile_docker_command() {
      local profile="$1"
      shift
      printf "transport|%s|%s\n" "${profile}" "$*" >>"${RECORD_FILE}"
      case "$1" in
        inspect)
          printf "%s\n" "$(<"${CONTAINER_STATE_FILE}")"
          ;;
        attach)
          printf "attached\n"
          ;;
        exec)
          return 0
          ;;
        stop)
          printf "stopped\n" >"${CONTAINER_STATE_FILE}"
          ;;
        rm)
          printf "removed\n" >"${CONTAINER_STATE_FILE}"
          ;;
        *)
          return 1
          ;;
      esac
    }

    attach_output="$(session_attach_main --id "${SESSION_ID}" --no-stdin)"
    send_output="$(session_send_main --id "${SESSION_ID}" --message resume)"
    stop_output="$(session_stop_main --id "${SESSION_ID}")"
    delete_output="$(session_delete_main --id "${SESSION_ID}")"

    printf "attach_output=%s\n" "${attach_output}"
    printf "send_output=%s\n" "$(tr "\n" "|" <<<"${send_output}")"
    printf "stop_output=%s\n" "$(tr "\n" "|" <<<"${stop_output}")"
    printf "delete_output=%s\n" "$(tr "\n" "|" <<<"${delete_output}")"

    test ! -e "${RECORD_PATH}"
    test ! -e "${DEBUG_LOG}"
    test ! -e "${FILE_TRACE_LOG}"
    test ! -e "${TRANSCRIPT_LOG}"
    test ! -e "${SESSION_AUDIT_DIR}"
  ' _ "${WORKCELL_FUNCTIONS_COPY}" "${SESSION_LIFECYCLE_ROOT}" "${SESSION_LIFECYCLE_RECORD}" "${WORKSPACE_A}"
)"
grep -q '^attach_output=attached$' <<<"${session_lifecycle_output}"
grep -q '^send_output=session_id=detached-lifecycle|sent_bytes=7|status=running|live_status=running|control_mode=detached|target_kind=local_vm|target_provider=colima|target_id=wcl-detached-lifecycle|target_summary=local_vm/colima/wcl-detached-lifecycle|target_assurance_class=strict|runtime_api=docker|workspace_transport=workspace-mount|workspace='"${WORKSPACE_A}"'|display_workspace='"${WORKSPACE_A}"'|workspace_origin='"${WORKSPACE_A}"'|display_worktree='"${WORKSPACE_A}"'|worktree_path='"${WORKSPACE_A}"'|display_git_branch=none|git_branch=|assurance=managed-mutable|$' <<<"${session_lifecycle_output}"
grep -q '^stop_output=session_id=detached-lifecycle|stop_requested=1|status=exited|live_status=stopped|control_mode=detached|target_kind=local_vm|target_provider=colima|target_id=wcl-detached-lifecycle|target_summary=local_vm/colima/wcl-detached-lifecycle|target_assurance_class=strict|runtime_api=docker|workspace_transport=workspace-mount|workspace='"${WORKSPACE_A}"'|display_workspace='"${WORKSPACE_A}"'|workspace_origin='"${WORKSPACE_A}"'|display_worktree='"${WORKSPACE_A}"'|worktree_path='"${WORKSPACE_A}"'|display_git_branch=none|git_branch=|assurance=managed-mutable|$' <<<"${session_lifecycle_output}"
grep -q '^delete_output=session_id=detached-lifecycle|deleted=1|record_only=0|dry_run=0|removed=record,container,session_audit_dir,debug_log,file_trace_log,transcript_log|kept=none|missing=none|unavailable=none|$' <<<"${session_lifecycle_output}"
test -f "${SESSION_LIFECYCLE_AUDIT_LOG}"
grep -q '^transport|wcl-detached-lifecycle|attach --no-stdin workcell-session-fixture$' "${SESSION_LIFECYCLE_RECORD}"
grep -q '^transport|wcl-detached-lifecycle|exec --user ' "${SESSION_LIFECYCLE_RECORD}"
grep -q '/state/tmp/workcell/session-stdin' "${SESSION_LIFECYCLE_RECORD}"
grep -q '^transport|wcl-detached-lifecycle|stop workcell-session-fixture$' "${SESSION_LIFECYCLE_RECORD}"
grep -q '^transport|wcl-detached-lifecycle|rm -f workcell-session-fixture$' "${SESSION_LIFECYCLE_RECORD}"
grep -q 'event=attach-attempt session_id=detached-lifecycle' "${SESSION_LIFECYCLE_AUDIT_LOG}"
grep -q 'event=attach session_id=detached-lifecycle' "${SESSION_LIFECYCLE_AUDIT_LOG}"
grep -q 'event=command session_id=detached-lifecycle source=host-cli command=session-send argv=resume' "${SESSION_LIFECYCLE_AUDIT_LOG}"
grep -q 'event=stop-request session_id=detached-lifecycle' "${SESSION_LIFECYCLE_AUDIT_LOG}"
grep -q 'event=exit session_id=detached-lifecycle source=host-stop-fallback exit_status=0 final_assurance=managed-mutable' "${SESSION_LIFECYCLE_AUDIT_LOG}"

HOST_DOCKER_BIN="$(resolve_host_docker_bin || true)"
if [[ -n "${HOST_DOCKER_BIN}" ]]; then
  docker_context_candidate=""
  setup_workcell_trusted_docker_client
  export WORKCELL_DOCKER_CLIENT_CWD="${ROOT_DIR}"
  unset DOCKER_HOST
  unset DOCKER_CONTEXT
  DOCKER_CONTEXT_NAME=""

  for docker_context_candidate in colima default desktop-linux; do
    if docker_context_exists "${docker_context_candidate}" && docker_context_is_healthy "${docker_context_candidate}"; then
      DOCKER_CONTEXT_NAME="${docker_context_candidate}"
      break
    fi
  done
  if [[ -z "${DOCKER_CONTEXT_NAME}" ]]; then
    while IFS= read -r docker_context_candidate; do
      [[ -n "${docker_context_candidate}" ]] || continue
      if docker_context_exists "${docker_context_candidate}" && docker_context_is_healthy "${docker_context_candidate}"; then
        DOCKER_CONTEXT_NAME="${docker_context_candidate}"
        break
      fi
    done < <(docker_context_names)
  fi

  if [[ -n "${DOCKER_CONTEXT_NAME}" ]]; then
    export DOCKER_CONTEXT="${DOCKER_CONTEXT_NAME}"

    docker_server_arch="$(run_workcell_docker_client_command "${HOST_DOCKER_BIN}" version --format '{{.Server.Arch}}')"
    case "${docker_server_arch}" in
      amd64 | arm64)
        fixture_goarch="${docker_server_arch}"
        ;;
      x86_64)
        fixture_goarch="amd64"
        ;;
      aarch64)
        fixture_goarch="arm64"
        ;;
      *)
        echo "Unsupported Docker server architecture for CLI session fixture image: ${docker_server_arch}" >&2
        exit 1
        ;;
    esac
    mkdir -p "${CLI_SESSION_FIXTURE_BUILD_DIR}"
    CGO_ENABLED=0 GOOS=linux GOARCH="${fixture_goarch}" \
      go build -trimpath -ldflags='-s -w' \
      -o "${CLI_SESSION_FIXTURE_BUILD_DIR}/session-cli-fixture" \
      "${ROOT_DIR}/tests/fixtures/session-cli"
    cat >"${CLI_SESSION_FIXTURE_BUILD_DIR}/Dockerfile" <<'EOF'
FROM scratch
COPY session-cli-fixture /session-cli-fixture
COPY session-cli-fixture /bin/sh
EOF
    run_workcell_docker_client_command \
      "${HOST_DOCKER_BIN}" build -t "${CLI_SESSION_FIXTURE_IMAGE}" "${CLI_SESSION_FIXTURE_BUILD_DIR}" >/dev/null

    cli_attach_output="$(
      bash -lc '
    set -euo pipefail
    ROOT_DIR="$1"
    REAL_HOME="$2"
    CLI_SESSION_FIXTURE_IMAGE="$3"
    HOST_DOCKER_BIN="$4"
    PROFILE="wcl-cli-att-$$"
    SESSION_ID="cli-att-$$"
    PROFILE_DIR="${REAL_HOME}/.colima/${PROFILE}"
    SESSIONS_DIR="${PROFILE_DIR}/sessions"
    AUDIT_DIR="${PROFILE_DIR}/session-audit.${SESSION_ID}"
    AUDIT_LOG="${PROFILE_DIR}/workcell.audit.log"
    STATE_FILE="${AUDIT_DIR}/session-monitor.env"
    WORKSPACE="$(mktemp -d "${REAL_HOME}/.colima/workcell-cli-attach-workspace.XXXXXX")"
    CONTAINER_NAME="workcell-cli-attach-$$"
    MONITOR_PID=""
    cleanup() {
      if [[ -n "${MONITOR_PID}" ]]; then
        kill "${MONITOR_PID}" >/dev/null 2>&1 || true
        wait "${MONITOR_PID}" >/dev/null 2>&1 || true
      fi
      "${HOST_DOCKER_BIN}" rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
      rm -rf "${PROFILE_DIR}" "${WORKSPACE}"
    }
    trap cleanup EXIT

    docker_host="$("${HOST_DOCKER_BIN}" context inspect "$("${HOST_DOCKER_BIN}" context show)" --format '"'"'{{ (index .Endpoints "docker").Host }}'"'"')"
    [[ "${docker_host}" == unix://* ]] || {
      echo "Expected a unix Docker host for CLI session attach integration, got: ${docker_host}" >&2
      exit 1
    }

    mkdir -p "${SESSIONS_DIR}" "${AUDIT_DIR}"
    ln -s "${docker_host#unix://}" "${PROFILE_DIR}/docker.sock"
    "${HOST_DOCKER_BIN}" run --pull=never -d --name "${CONTAINER_NAME}" "${CLI_SESSION_FIXTURE_IMAGE}" /session-cli-fixture attach >/dev/null

    monitor_cmd="${ROOT_DIR}/scripts/workcell session monitor --state-file ${STATE_FILE}"
    bash -c '"'"'exec -a "$0" sleep 30'"'"' "${monitor_cmd}" &
    MONITOR_PID="$!"

    cat >"${SESSIONS_DIR}/${SESSION_ID}.json" <<EOF_JSON
{
  "version": 1,
  "session_id": "${SESSION_ID}",
  "profile": "${PROFILE}",
  "agent": "codex",
  "mode": "strict",
  "status": "running",
  "live_status": "running",
  "ui": "cli",
  "execution_path": "managed-tier1",
  "workspace": "${WORKSPACE}",
  "workspace_origin": "${WORKSPACE}",
  "worktree_path": "${WORKSPACE}/.git/workcell-sessions/${SESSION_ID}/repo",
  "container_name": "${CONTAINER_NAME}",
  "monitor_pid": "${MONITOR_PID}",
  "session_audit_dir": "${AUDIT_DIR}",
  "audit_log_path": "${AUDIT_LOG}",
  "started_at": "2026-04-08T15:30:00Z",
  "current_assurance": "managed-mutable",
  "initial_assurance": "managed-mutable",
  "workspace_control_plane": "masked"
}
EOF_JSON

    attach_output="$("${ROOT_DIR}/scripts/workcell" session attach --id "${SESSION_ID}" --no-stdin | tr -d "\r")"
    grep -q "event=attach-attempt session_id=${SESSION_ID}" "${AUDIT_LOG}"
    grep -q "event=attach session_id=${SESSION_ID}" "${AUDIT_LOG}"
    printf "attach_cli_output=%s\n" "${attach_output}"
  ' _ "${ROOT_DIR}" "${REAL_HOME}" "${CLI_SESSION_FIXTURE_IMAGE}" "${HOST_DOCKER_BIN}"
    )"
    grep -q '^attach_cli_output=attached-from-container$' <<<"${cli_attach_output}"

    cli_lifecycle_output="$(
      bash -lc '
    set -euo pipefail
    ROOT_DIR="$1"
    REAL_HOME="$2"
    CLI_SESSION_FIXTURE_IMAGE="$3"
    HOST_DOCKER_BIN="$4"
    PROFILE="wcl-cli-life-$$"
    SESSION_ID="cli-life-$$"
    PROFILE_DIR="${REAL_HOME}/.colima/${PROFILE}"
    SESSIONS_DIR="${PROFILE_DIR}/sessions"
    AUDIT_DIR="${PROFILE_DIR}/session-audit.${SESSION_ID}"
    AUDIT_LOG="${PROFILE_DIR}/workcell.audit.log"
    DEBUG_LOG="${PROFILE_DIR}/${SESSION_ID}.debug.log"
    FILE_TRACE_LOG="${PROFILE_DIR}/${SESSION_ID}.file-trace.log"
    TRANSCRIPT_LOG="${PROFILE_DIR}/${SESSION_ID}.transcript.log"
    STATE_FILE="${AUDIT_DIR}/session-monitor.env"
    EXPORT_PATH="${PROFILE_DIR}/${SESSION_ID}.export.json"
    WORKSPACE="$(mktemp -d "${REAL_HOME}/.colima/workcell-cli-lifecycle-workspace.XXXXXX")"
    FIXTURE_DIR="${PROFILE_DIR}/fixture"
    CONTAINER_NAME="workcell-cli-lifecycle-$$"
    MONITOR_PID=""
    cleanup() {
      if [[ -n "${MONITOR_PID}" ]]; then
        kill "${MONITOR_PID}" >/dev/null 2>&1 || true
        wait "${MONITOR_PID}" >/dev/null 2>&1 || true
      fi
      "${HOST_DOCKER_BIN}" rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
      rm -rf "${PROFILE_DIR}" "${WORKSPACE}"
    }
    trap cleanup EXIT

    docker_host="$("${HOST_DOCKER_BIN}" context inspect "$("${HOST_DOCKER_BIN}" context show)" --format '"'"'{{ (index .Endpoints "docker").Host }}'"'"')"
    [[ "${docker_host}" == unix://* ]] || {
      echo "Expected a unix Docker host for CLI session lifecycle integration, got: ${docker_host}" >&2
      exit 1
    }

    mkdir -p "${SESSIONS_DIR}" "${AUDIT_DIR}" "${FIXTURE_DIR}"
    : >"${DEBUG_LOG}"
    : >"${FILE_TRACE_LOG}"
    : >"${TRANSCRIPT_LOG}"
    ln -s "${docker_host#unix://}" "${PROFILE_DIR}/docker.sock"
    "${HOST_DOCKER_BIN}" run --pull=never -d --name "${CONTAINER_NAME}" -v "${FIXTURE_DIR}:/fixture" "${CLI_SESSION_FIXTURE_IMAGE}" /session-cli-fixture lifecycle /fixture/stdin.log >/dev/null

    monitor_cmd="${ROOT_DIR}/scripts/workcell session monitor --state-file ${STATE_FILE}"
    bash -c '"'"'exec -a "$0" sleep 30'"'"' "${monitor_cmd}" &
    MONITOR_PID="$!"

    cat >"${SESSIONS_DIR}/${SESSION_ID}.json" <<EOF_JSON
{
  "version": 1,
  "session_id": "${SESSION_ID}",
  "profile": "${PROFILE}",
  "agent": "codex",
  "mode": "strict",
  "status": "running",
  "live_status": "running",
  "ui": "cli",
  "execution_path": "managed-tier1",
  "workspace": "${WORKSPACE}",
  "workspace_origin": "${WORKSPACE}",
  "worktree_path": "${WORKSPACE}/.git/workcell-sessions/${SESSION_ID}/repo",
  "container_name": "${CONTAINER_NAME}",
  "monitor_pid": "${MONITOR_PID}",
  "session_audit_dir": "${AUDIT_DIR}",
  "audit_log_path": "${AUDIT_LOG}",
  "debug_log_path": "${DEBUG_LOG}",
  "file_trace_log_path": "${FILE_TRACE_LOG}",
  "transcript_log_path": "${TRANSCRIPT_LOG}",
  "started_at": "2026-04-08T16:00:00Z",
  "current_assurance": "managed-mutable",
  "initial_assurance": "managed-mutable",
  "workspace_control_plane": "masked"
}
EOF_JSON

    list_running="$("${ROOT_DIR}/scripts/workcell" session list --json --colima-profile "${PROFILE}")"
    show_running="$("${ROOT_DIR}/scripts/workcell" session show --id "${SESSION_ID}")"
    grep -q "\"session_id\": \"${SESSION_ID}\"" <<<"${list_running}"
    grep -q "\"status\": \"running\"" <<<"${show_running}"
    grep -q "\"live_status\": \"running\"" <<<"${show_running}"

    send_output="$("${ROOT_DIR}/scripts/workcell" session send --id "${SESSION_ID}" --message resume)"
    set +e
    running_delete_stderr="$("${ROOT_DIR}/scripts/workcell" session delete --id "${SESSION_ID}" 2>&1 >/dev/null)"
    running_delete_rc="$?"
    set -e
    [[ "${running_delete_rc}" -ne 0 ]] || {
      echo "session delete unexpectedly accepted a live detached session on the public CLI" >&2
      exit 1
    }
    grep -q "session delete only works for exited, failed, or otherwise stopped sessions: ${SESSION_ID}" <<<"${running_delete_stderr}"
    grep -q "For detached sessions, run: workcell session stop --id ${SESSION_ID}" <<<"${running_delete_stderr}"
    sleep 1
    stdin_payload="$(python3 -c '"'"'import pathlib,sys; print(pathlib.Path(sys.argv[1]).read_text().rstrip("\\n"))'"'"' "${FIXTURE_DIR}/stdin.log")"
    audit_log_running="$("${ROOT_DIR}/scripts/workcell" session logs --id "${SESSION_ID}" --kind audit)"
    timeline_running="$("${ROOT_DIR}/scripts/workcell" session timeline --id "${SESSION_ID}")"
    grep -q "event=command session_id=${SESSION_ID} source=host-cli command=session-send argv=resume" <<<"${audit_log_running}"
    grep -q "event=command session_id=${SESSION_ID}" <<<"${timeline_running}"
    kill "${MONITOR_PID}" >/dev/null 2>&1 || true
    wait "${MONITOR_PID}" >/dev/null 2>&1 || true
    MONITOR_PID=""

    stop_output="$("${ROOT_DIR}/scripts/workcell" session stop --id "${SESSION_ID}")"
    python3 - "${SESSIONS_DIR}/${SESSION_ID}.json" <<'"'"'PY'"'"'
import json
import sys

record = json.load(open(sys.argv[1]))
status = record.get("status")
live_status = record.get("live_status")
if status != "exited" or live_status != "stopped":
    raise SystemExit(f"unexpected record state: {status} {live_status}")
PY
    printf "debug-log\n" >"${DEBUG_LOG}"
    printf "file-trace-log\n" >"${FILE_TRACE_LOG}"
    printf "transcript-log\n" >"${TRANSCRIPT_LOG}"
    list_stopped="$("${ROOT_DIR}/scripts/workcell" session list --json --colima-profile "${PROFILE}")"
    show_stopped="$("${ROOT_DIR}/scripts/workcell" session show --id "${SESSION_ID}")"
    audit_log_stopped="$("${ROOT_DIR}/scripts/workcell" session logs --id "${SESSION_ID}" --kind audit)"
    debug_log_output="$("${ROOT_DIR}/scripts/workcell" session logs --id "${SESSION_ID}" --kind debug)"
    file_trace_log_output="$("${ROOT_DIR}/scripts/workcell" session logs --id "${SESSION_ID}" --kind file-trace)"
    transcript_log_output="$("${ROOT_DIR}/scripts/workcell" session logs --id "${SESSION_ID}" --kind transcript)"
    timeline_stopped="$("${ROOT_DIR}/scripts/workcell" session timeline --id "${SESSION_ID}")"
    export_output="$("${ROOT_DIR}/scripts/workcell" session export --id "${SESSION_ID}" --output "${EXPORT_PATH}")"
    grep -q "\"session_id\": \"${SESSION_ID}\"" <<<"${list_stopped}"
    grep -q "\"live_status\": \"stopped\"" <<<"${show_stopped}"
    grep -q "\"status\": \"exited\"" <<<"${show_stopped}"
    grep -q "event=stop-request session_id=${SESSION_ID}" <<<"${audit_log_stopped}"
    grep -q "event=exit session_id=${SESSION_ID}" <<<"${audit_log_stopped}"
    grep -q "^debug-log$" <<<"${debug_log_output}"
    grep -q "^file-trace-log$" <<<"${file_trace_log_output}"
    grep -q "^transcript-log$" <<<"${transcript_log_output}"
    grep -q "event=stop-request session_id=${SESSION_ID}" <<<"${timeline_stopped}"
    grep -q "event=exit session_id=${SESSION_ID}" <<<"${timeline_stopped}"
    grep -q "^session_export=${EXPORT_PATH}$" <<<"${export_output}"
    grep -q "\"session_id\": \"${SESSION_ID}\"" "${EXPORT_PATH}"
    grep -q "\"audit_records\"" "${EXPORT_PATH}"

    set +e
    dead_send_stderr="$("${ROOT_DIR}/scripts/workcell" session send --id "${SESSION_ID}" --message after 2>&1 >/dev/null)"
    dead_send_rc="$?"
    set -e
    [[ "${dead_send_rc}" -ne 0 ]] || {
      echo "session send unexpectedly accepted a stopped detached session on the public CLI" >&2
      exit 1
    }
    grep -q "session send requires a detached session that is still running: ${SESSION_ID}" <<<"${dead_send_stderr}"

    set +e
    dead_attach_stderr="$("${ROOT_DIR}/scripts/workcell" session attach --id "${SESSION_ID}" --no-stdin 2>&1 >/dev/null)"
    dead_attach_rc="$?"
    set -e
    [[ "${dead_attach_rc}" -ne 0 ]] || {
      echo "session attach unexpectedly accepted a stopped detached session on the public CLI" >&2
      exit 1
    }
    grep -q "session attach requires a detached session that is still running: ${SESSION_ID}" <<<"${dead_attach_stderr}"

    delete_output="$("${ROOT_DIR}/scripts/workcell" session delete --id "${SESSION_ID}")"
    grep -q "event=command session_id=${SESSION_ID} source=host-cli command=session-send argv=resume" "${AUDIT_LOG}"
    grep -q "event=stop-request session_id=${SESSION_ID}" "${AUDIT_LOG}"
    grep -q "event=exit session_id=${SESSION_ID} source=host-stop-fallback exit_status=" "${AUDIT_LOG}"
    grep -q "final_assurance=managed-mutable" "${AUDIT_LOG}"
    test ! -e "${SESSIONS_DIR}/${SESSION_ID}.json"
    test ! -e "${DEBUG_LOG}"
    test ! -e "${FILE_TRACE_LOG}"
    test ! -e "${TRANSCRIPT_LOG}"
    test ! -e "${AUDIT_DIR}"
    if "${HOST_DOCKER_BIN}" inspect "${CONTAINER_NAME}" >/dev/null 2>&1; then
      echo "session delete left the recorded detached container behind on the public CLI" >&2
      exit 1
    fi

    printf "cli_send_output=%s\n" "$(tr "\n" "|" <<<"${send_output}")"
    printf "cli_stdin_payload=%s\n" "${stdin_payload}"
    printf "cli_stop_output=%s\n" "$(tr "\n" "|" <<<"${stop_output}")"
    printf "cli_delete_output=%s\n" "$(tr "\n" "|" <<<"${delete_output}")"
  ' _ "${ROOT_DIR}" "${REAL_HOME}" "${CLI_SESSION_FIXTURE_IMAGE}" "${HOST_DOCKER_BIN}"
    )"
    grep -q '^cli_stdin_payload=resume$' <<<"${cli_lifecycle_output}"
    grep -Eq '^cli_send_output=session_id=cli-life-[0-9]+[|]sent_bytes=7[|]status=running[|]live_status=running[|]control_mode=detached[|]target_kind=local_vm[|]target_provider=colima[|]target_id=wcl-cli-life-[0-9]+[|]target_summary=local_vm/colima/wcl-cli-life-[0-9]+[|]target_assurance_class=strict[|]runtime_api=docker[|]workspace_transport=isolated-worktree-mount[|]workspace=.*/workcell-cli-lifecycle-workspace\.[^|]+[|]display_workspace=.*/workcell-cli-lifecycle-workspace\.[^|]+[|]workspace_origin=.*/workcell-cli-lifecycle-workspace\.[^|]+[|]display_worktree=.*/workcell-cli-lifecycle-workspace\.[^|]+/.git/workcell-sessions/cli-life-[0-9]+/repo[|]worktree_path=.*/workcell-cli-lifecycle-workspace\.[^|]+/.git/workcell-sessions/cli-life-[0-9]+/repo[|]display_git_branch=none[|]git_branch=[|]assurance=managed-mutable[|]$' <<<"${cli_lifecycle_output}"
    grep -Eq '^cli_stop_output=session_id=cli-life-[0-9]+[|]stop_requested=1[|]status=exited[|]live_status=stopped[|]control_mode=detached[|]target_kind=local_vm[|]target_provider=colima[|]target_id=wcl-cli-life-[0-9]+[|]target_summary=local_vm/colima/wcl-cli-life-[0-9]+[|]target_assurance_class=strict[|]runtime_api=docker[|]workspace_transport=isolated-worktree-mount[|]workspace=.*/workcell-cli-lifecycle-workspace\.[^|]+[|]display_workspace=.*/workcell-cli-lifecycle-workspace\.[^|]+[|]workspace_origin=.*/workcell-cli-lifecycle-workspace\.[^|]+[|]display_worktree=.*/workcell-cli-lifecycle-workspace\.[^|]+/.git/workcell-sessions/cli-life-[0-9]+/repo[|]worktree_path=.*/workcell-cli-lifecycle-workspace\.[^|]+/.git/workcell-sessions/cli-life-[0-9]+/repo[|]display_git_branch=none[|]git_branch=[|]assurance=managed-mutable[|]$' <<<"${cli_lifecycle_output}"
    grep -q '^cli_delete_output=session_id=cli-life-[0-9]\+|deleted=1|record_only=0|dry_run=0|removed=record,container,session_audit_dir,debug_log,file_trace_log,transcript_log|kept=none|missing=none|unavailable=none|$' <<<"${cli_lifecycle_output}"
  else
    echo "Skipping public CLI detached workload integration: no healthy Docker context available" >&2
  fi
else
  echo "Skipping public CLI detached workload integration: docker CLI unavailable on PATH" >&2
fi

TAMPERED_SESSION_AUDIT_TARGET="${TMP_DIR}/tampered-session-audit-target"
printf 'keep-me\n' >"${TAMPERED_SESSION_AUDIT_TARGET}"
cat >"${SESSIONS_DIR}/20260408T141500Z-56565656.json" <<EOF
{
  "version": 1,
  "session_id": "20260408T141500Z-56565656",
  "profile": "${PROFILE}",
  "agent": "codex",
  "mode": "strict",
  "status": "exited",
  "live_status": "stopped",
  "ui": "cli",
  "execution_path": "managed-tier1",
  "workspace": "${WORKSPACE_A}",
  "session_audit_dir": "${TAMPERED_SESSION_AUDIT_TARGET}",
  "started_at": "2026-04-08T14:15:00Z",
  "finished_at": "2026-04-08T14:16:00Z",
  "exit_status": "0",
  "initial_assurance": "managed-mutable",
  "final_assurance": "managed-mutable",
  "workspace_control_plane": "masked"
}
EOF
tampered_session_delete_output="$(
  "${ROOT_DIR}/scripts/workcell" session delete --id 20260408T141500Z-56565656 2>&1
)"
grep -q 'session delete refused recorded session_audit_dir path after launch' <<<"${tampered_session_delete_output}"
grep -q '^deleted=1$' <<<"${tampered_session_delete_output}"
grep -q '^removed=record$' <<<"${tampered_session_delete_output}"
grep -q '^missing=none$' <<<"${tampered_session_delete_output}"
grep -q 'keep-me' "${TAMPERED_SESSION_AUDIT_TARGET}"

RUNTIME_IMAGE_CACHE_EXPORT_SOURCE="${TMP_DIR}/runtime-image-cache-export.tar"
RUNTIME_IMAGE_CACHE_RECORD="${TMP_DIR}/runtime-image-cache.record"
printf 'cached-runtime-image\n' >"${RUNTIME_IMAGE_CACHE_EXPORT_SOURCE}"
runtime_image_cache_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    COLIMA_STATE_ROOT="$2"
    COLIMA_PROFILE="wcl-cache-fixture"
    IMAGE_TAG="workcell:local"
    SOURCE_DATE_EPOCH="1234567890"
    HOST_DOCKER_BIN="/bin/false"
    CACHE_RECORD="$3"
    EXPORT_SOURCE="$4"
    SAVE_COUNT=0
    LOAD_COUNT=0
    MARKER_COUNT=0
    CURRENT_PROFILE_IMAGE_ID="sha256:cached-runtime-image"
    LOADED_PROFILE_IMAGE_ID=""
    validate_colima_profile_name() { :; }
    run_profile_docker_command() {
      local profile="$1"
      shift
      case "$1 $2" in
        "image save")
          SAVE_COUNT=$((SAVE_COUNT + 1))
          cp "${EXPORT_SOURCE}" "$4"
          ;;
        "image load")
          LOAD_COUNT=$((LOAD_COUNT + 1))
          test -f "$4"
          LOADED_PROFILE_IMAGE_ID="sha256:cached-runtime-image"
          ;;
        *)
          return 1
          ;;
      esac
      printf "%s|%s\n" "${profile}" "$*" >>"${CACHE_RECORD}"
    }
    current_profile_image_id() {
      if [[ -n "${LOADED_PROFILE_IMAGE_ID}" ]]; then
        printf "%s\n" "${LOADED_PROFILE_IMAGE_ID}"
      else
        printf "%s\n" "${CURRENT_PROFILE_IMAGE_ID}"
      fi
    }
    write_profile_image_marker() {
      MARKER_COUNT=$((MARKER_COUNT + 1))
      printf "marker=%s\n" "$2" >>"${CACHE_RECORD}"
    }

    rm -f \
      "$(profile_runtime_image_cache_path "${COLIMA_PROFILE}")" \
      "$(profile_runtime_image_cache_metadata_path "${COLIMA_PROFILE}")"
    cache_profile_runtime_image "${COLIMA_PROFILE}" "sha256:cached-runtime-image"
    CURRENT_PROFILE_IMAGE_ID=""
    if restore_profile_runtime_image_cache "${COLIMA_PROFILE}" "sha256:cached-runtime-image" "0"; then
      echo "restore with a stale source epoch unexpectedly succeeded" >&2
      exit 1
    fi
    restore_profile_runtime_image_cache "${COLIMA_PROFILE}" "sha256:cached-runtime-image" "1234567890"

    printf "save_count=%s\n" "${SAVE_COUNT}"
    printf "load_count=%s\n" "${LOAD_COUNT}"
    printf "marker_count=%s\n" "${MARKER_COUNT}"
    printf "cache_path=%s\n" "$(profile_runtime_image_cache_path "${COLIMA_PROFILE}")"
    printf "cache_metadata=%s\n" "$(profile_runtime_image_cache_metadata_path "${COLIMA_PROFILE}")"
    printf "cache_image_id=%s\n" "$(profile_runtime_image_cache_value "${COLIMA_PROFILE}" image_id)"
    printf "cache_epoch=%s\n" "$(profile_runtime_image_cache_value "${COLIMA_PROFILE}" source_date_epoch)"
  ' _ \
    "${WORKCELL_FUNCTIONS_COPY}" \
    "${COLIMA_ROOT}" \
    "${RUNTIME_IMAGE_CACHE_RECORD}" \
    "${RUNTIME_IMAGE_CACHE_EXPORT_SOURCE}"
)"
grep -q '^save_count=1$' <<<"${runtime_image_cache_output}"
grep -q '^load_count=1$' <<<"${runtime_image_cache_output}"
grep -q '^marker_count=1$' <<<"${runtime_image_cache_output}"
grep -q '^cache_image_id=sha256:cached-runtime-image$' <<<"${runtime_image_cache_output}"
grep -q '^cache_epoch=1234567890$' <<<"${runtime_image_cache_output}"
grep -q '^wcl-cache-fixture|image save -o ' "${RUNTIME_IMAGE_CACHE_RECORD}"
grep -q '^wcl-cache-fixture|image load -i ' "${RUNTIME_IMAGE_CACHE_RECORD}"
grep -q '^marker=sha256:cached-runtime-image$' "${RUNTIME_IMAGE_CACHE_RECORD}"

DETACHED_CAPTURE_STATE_FILE="${DETACHED_STATE_DIR}/captured-session-assurance"
DETACHED_CAPTURE_FILE_TRACE_LOG="${DETACHED_STATE_DIR}/captured-session.file-trace.log"
DETACHED_CAPTURE_AUDIT_SOURCE="${DETACHED_STATE_DIR}/container-session-assurance"
DETACHED_CAPTURE_FILE_TRACE_SOURCE="${DETACHED_STATE_DIR}/container-session.file-trace.log"
DETACHED_CAPTURE_RECORD="${DETACHED_STATE_DIR}/capture-record.log"
printf 'lower-assurance-package-mutation\n' >"${DETACHED_CAPTURE_AUDIT_SOURCE}"
printf 'event=watch-start\n' >"${DETACHED_CAPTURE_FILE_TRACE_SOURCE}"
capture_probe_output="$(
  bash -lc '
    set -euo pipefail
    source "$1"
    trap - EXIT
    COLIMA_PROFILE="wcl-detached-fixture"
    HOST_DOCKER_BIN="/bin/false"
    SESSION_AUDIT_STATE_FILE="$2"
    SESSION_AUDIT_CONTAINER_FILE="/var/lib/workcell/session-assurance"
    FILE_TRACE_LOG_PATH="$3"
    SESSION_FILE_TRACE_CONTAINER_FILE="/var/tmp/workcell-file-trace.log"
    STUB_AUDIT_SOURCE="$4"
    STUB_FILE_TRACE_SOURCE="$5"
    STUB_RECORD="$6"
    revalidate_recorded_host_output_path() { printf "%s\n" "$1"; }
    run_profile_docker_command() {
      local profile="$1"
      shift
      printf "%s|%s|%s|%s\n" "${profile}" "$1" "$2" "$3" >>"${STUB_RECORD}"
      case "$2" in
        *:/var/lib/workcell/session-assurance|*:/run/workcell/session-assurance)
          cp "${STUB_AUDIT_SOURCE}" "$3"
          ;;
        *:/var/tmp/workcell-file-trace.log)
          cp "${STUB_FILE_TRACE_SOURCE}" "$3"
          ;;
        *)
          return 1
          ;;
      esac
    }
    run_workcell_docker_client_command() {
      echo "ambient-docker-client" >>"${STUB_RECORD}"
      return 99
    }
    capture_session_audit_state "workcell-session-fixture"
    capture_session_file_trace "workcell-session-fixture"
    printf "audit=%s\n" "$(cat "${SESSION_AUDIT_STATE_FILE}")"
    printf "trace=%s\n" "$(cat "${FILE_TRACE_LOG_PATH}")"
  ' _ \
    "${WORKCELL_FUNCTIONS_COPY}" \
    "${DETACHED_CAPTURE_STATE_FILE}" \
    "${DETACHED_CAPTURE_FILE_TRACE_LOG}" \
    "${DETACHED_CAPTURE_AUDIT_SOURCE}" \
    "${DETACHED_CAPTURE_FILE_TRACE_SOURCE}" \
    "${DETACHED_CAPTURE_RECORD}"
)"
grep -q '^audit=lower-assurance-package-mutation$' <<<"${capture_probe_output}"
grep -q '^trace=event=watch-start$' <<<"${capture_probe_output}"
grep -q '^wcl-detached-fixture|cp|workcell-session-fixture:/var/lib/workcell/session-assurance|' "${DETACHED_CAPTURE_RECORD}"
grep -q '^wcl-detached-fixture|cp|workcell-session-fixture:/var/tmp/workcell-file-trace.log|' "${DETACHED_CAPTURE_RECORD}"
if grep -q 'ambient-docker-client' "${DETACHED_CAPTURE_RECORD}"; then
  echo "Detached artifact capture unexpectedly used the ambient host docker client" >&2
  exit 1
fi

mkdir -p "${DETACHED_STATE_DIR}" "$(dirname "${DETACHED_WORKSPACE}")"
cat >"${DETACHED_DEBUG_LOG}" <<EOF
debug-log: detached session observability fixture
EOF
cat >"${DETACHED_FILE_TRACE_LOG}" <<EOF
file-trace: detached session observability fixture
EOF
cat >"${DETACHED_TRANSCRIPT_LOG}" <<EOF
transcript: detached session observability fixture
EOF
cat >"${DETACHED_BACKEND}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

cmd="${1:-}"
shift || true

STATE_FILE="${SESSION_BACKEND_STATE_FILE:?}"
AUDIT_LOG="${SESSION_BACKEND_AUDIT_LOG:?}"
SESSION_ID="${SESSION_BACKEND_SESSION_ID:?}"
WORKSPACE_PATH="${SESSION_BACKEND_WORKSPACE_PATH:?}"
DEBUG_LOG_PATH="${SESSION_BACKEND_DEBUG_LOG_PATH:?}"
FILE_TRACE_LOG_PATH="${SESSION_BACKEND_FILE_TRACE_LOG_PATH:?}"
TRANSCRIPT_LOG_PATH="${SESSION_BACKEND_TRANSCRIPT_LOG_PATH:?}"

write_state() {
  local status="$1"
  local live_status="$2"
  local timeline_seq="$3"
  local finished_at="${4:-}"
  local exit_status="${5:-}"
  local final_assurance="${6:-}"

  {
    printf 'status=%q\n' "${status}"
    printf 'live_status=%q\n' "${live_status}"
    printf 'timeline_seq=%q\n' "${timeline_seq}"
    printf 'finished_at=%q\n' "${finished_at}"
    printf 'exit_status=%q\n' "${exit_status}"
    printf 'final_assurance=%q\n' "${final_assurance}"
  } >"${STATE_FILE}"
}

load_state() {
  # shellcheck disable=SC1090
  source "${STATE_FILE}"
}

append_audit_line() {
  printf '%s\n' "$*" >>"${AUDIT_LOG}"
}

case "${cmd}" in
  start)
    mkdir -p "$(dirname "${STATE_FILE}")" "$(dirname "${AUDIT_LOG}")" "$(dirname "${WORKSPACE_PATH}")"
    write_state "running" "running" 1
    append_audit_line "timestamp=2026-04-08T13:00:00Z event=launch session_id=${SESSION_ID} control_mode=detached live_status=running workspace=${WORKSPACE_PATH} git_branch=${SESSION_BACKEND_GIT_BRANCH:-unknown} debug_log_path=${DEBUG_LOG_PATH} file_trace_log_path=${FILE_TRACE_LOG_PATH} transcript_log_path=${TRANSCRIPT_LOG_PATH}"
    cat <<EOF_START
session_id=${SESSION_ID}
status=running
live_status=running
control_mode=detached
workspace=${WORKSPACE_PATH}
git_branch=${SESSION_BACKEND_GIT_BRANCH:-unknown}
debug_log_path=${DEBUG_LOG_PATH}
file_trace_log_path=${FILE_TRACE_LOG_PATH}
transcript_log_path=${TRANSCRIPT_LOG_PATH}
EOF_START
    ;;
  attach)
    load_state
    cat <<EOF_ATTACH
session_id=${SESSION_ID}
status=${status}
live_status=${live_status}
control_mode=detached
workspace=${WORKSPACE_PATH}
git_branch=${SESSION_BACKEND_GIT_BRANCH:-unknown}
transcript_log_path=${TRANSCRIPT_LOG_PATH}
EOF_ATTACH
    ;;
  send)
    load_state
    command_text="${1:-}"
    shift || true
    timeline_seq=$((timeline_seq + 1))
    write_state "${status}" "${live_status}" "${timeline_seq}"
    append_audit_line "timestamp=2026-04-08T13:01:00Z event=command session_id=${SESSION_ID} timeline_seq=${timeline_seq} command=${command_text} argv=${command_text} source=attach"
    printf 'sent=%s\n' "${command_text}"
    printf 'timeline_seq=%s\n' "${timeline_seq}"
    ;;
  stop)
    load_state
    timeline_seq=$((timeline_seq + 1))
    write_state "exited" "stopped" "${timeline_seq}" "2026-04-08T13:02:00Z" "0" "managed-mutable"
    append_audit_line "timestamp=2026-04-08T13:02:00Z event=exit session_id=${SESSION_ID} timeline_seq=${timeline_seq} exit_status=0 final_assurance=managed-mutable"
    cat <<EOF_STOP
session_id=${SESSION_ID}
status=exited
live_status=stopped
finished_at=2026-04-08T13:02:00Z
exit_status=0
final_assurance=managed-mutable
EOF_STOP
    ;;
  show)
    load_state
    cat <<EOF_SHOW
{
  "session_id": "${SESSION_ID}",
  "status": "${status}",
  "live_status": "${live_status}",
  "control_mode": "detached",
  "workspace": "${WORKSPACE_PATH}",
  "git_branch": "${SESSION_BACKEND_GIT_BRANCH:-unknown}",
  "initial_assurance": "managed-mutable",
  "current_assurance": "managed-mutable",
  "final_assurance": "${final_assurance}",
  "audit_log_path": "${AUDIT_LOG}",
  "debug_log_path": "${DEBUG_LOG_PATH}",
  "file_trace_log_path": "${FILE_TRACE_LOG_PATH}",
  "transcript_log_path": "${TRANSCRIPT_LOG_PATH}",
  "timeline_seq": ${timeline_seq}
}
EOF_SHOW
    ;;
  export)
    load_state
    {
      echo '{'
      printf '  "session": {\n'
      printf '    "session_id": "%s",\n' "${SESSION_ID}"
      printf '    "status": "%s",\n' "${status}"
      printf '    "live_status": "%s",\n' "${live_status}"
      printf '    "control_mode": "detached",\n'
      printf '    "workspace": "%s",\n' "${WORKSPACE_PATH}"
      printf '    "git_branch": "%s",\n' "${SESSION_BACKEND_GIT_BRANCH:-unknown}"
      printf '    "audit_log_path": "%s",\n' "${AUDIT_LOG}"
      printf '    "debug_log_path": "%s",\n' "${DEBUG_LOG_PATH}"
      printf '    "file_trace_log_path": "%s",\n' "${FILE_TRACE_LOG_PATH}"
      printf '    "transcript_log_path": "%s"\n' "${TRANSCRIPT_LOG_PATH}"
      printf '  },\n'
      echo '  "audit_records": ['
      first=1
      while IFS= read -r line; do
        [[ -z "${line}" ]] && continue
        case "${line}" in
          *"session_id=${SESSION_ID}"*)
            if [[ "${first}" -eq 0 ]]; then
              echo ','
            fi
            first=0
            printf '    "%s"' "${line}"
            ;;
        esac
      done <"${AUDIT_LOG}"
      if [[ "${first}" -eq 0 ]]; then
        echo
      fi
      echo '  ]'
      echo '}'
    }
    ;;
  *)
    echo "unknown session backend command: ${cmd}" >&2
    exit 2
    ;;
esac
EOF
chmod +x "${DETACHED_BACKEND}"

start_output="$(
  SESSION_BACKEND_STATE_FILE="${DETACHED_STATE_FILE}" \
    SESSION_BACKEND_AUDIT_LOG="${DETACHED_AUDIT_LOG}" \
    SESSION_BACKEND_SESSION_ID="${DETACHED_SESSION}" \
    SESSION_BACKEND_WORKSPACE_PATH="${DETACHED_WORKSPACE}" \
    SESSION_BACKEND_GIT_BRANCH="${DETACHED_BRANCH}" \
    SESSION_BACKEND_DEBUG_LOG_PATH="${DETACHED_DEBUG_LOG}" \
    SESSION_BACKEND_FILE_TRACE_LOG_PATH="${DETACHED_FILE_TRACE_LOG}" \
    SESSION_BACKEND_TRANSCRIPT_LOG_PATH="${DETACHED_TRANSCRIPT_LOG}" \
    "${DETACHED_BACKEND}" start
)"
grep -q "^session_id=${DETACHED_SESSION}$" <<<"${start_output}"
grep -q '^status=running$' <<<"${start_output}"
grep -q '^live_status=running$' <<<"${start_output}"
grep -q "^control_mode=detached$" <<<"${start_output}"
grep -q "^workspace=${DETACHED_WORKSPACE}$" <<<"${start_output}"
grep -q "^git_branch=${DETACHED_BRANCH}$" <<<"${start_output}"
grep -q "^debug_log_path=${DETACHED_DEBUG_LOG}$" <<<"${start_output}"
grep -q "^file_trace_log_path=${DETACHED_FILE_TRACE_LOG}$" <<<"${start_output}"
grep -q "^transcript_log_path=${DETACHED_TRANSCRIPT_LOG}$" <<<"${start_output}"

attach_output="$(
  SESSION_BACKEND_STATE_FILE="${DETACHED_STATE_FILE}" \
    SESSION_BACKEND_AUDIT_LOG="${DETACHED_AUDIT_LOG}" \
    SESSION_BACKEND_SESSION_ID="${DETACHED_SESSION}" \
    SESSION_BACKEND_WORKSPACE_PATH="${DETACHED_WORKSPACE}" \
    SESSION_BACKEND_GIT_BRANCH="${DETACHED_BRANCH}" \
    SESSION_BACKEND_DEBUG_LOG_PATH="${DETACHED_DEBUG_LOG}" \
    SESSION_BACKEND_FILE_TRACE_LOG_PATH="${DETACHED_FILE_TRACE_LOG}" \
    SESSION_BACKEND_TRANSCRIPT_LOG_PATH="${DETACHED_TRANSCRIPT_LOG}" \
    "${DETACHED_BACKEND}" attach
)"
grep -q "^session_id=${DETACHED_SESSION}$" <<<"${attach_output}"
grep -q '^status=running$' <<<"${attach_output}"
grep -q '^live_status=running$' <<<"${attach_output}"
grep -q "^workspace=${DETACHED_WORKSPACE}$" <<<"${attach_output}"
grep -q "^git_branch=${DETACHED_BRANCH}$" <<<"${attach_output}"
grep -q "^transcript_log_path=${DETACHED_TRANSCRIPT_LOG}$" <<<"${attach_output}"

send_output="$(
  SESSION_BACKEND_STATE_FILE="${DETACHED_STATE_FILE}" \
    SESSION_BACKEND_AUDIT_LOG="${DETACHED_AUDIT_LOG}" \
    SESSION_BACKEND_SESSION_ID="${DETACHED_SESSION}" \
    SESSION_BACKEND_WORKSPACE_PATH="${DETACHED_WORKSPACE}" \
    SESSION_BACKEND_GIT_BRANCH="${DETACHED_BRANCH}" \
    SESSION_BACKEND_DEBUG_LOG_PATH="${DETACHED_DEBUG_LOG}" \
    SESSION_BACKEND_FILE_TRACE_LOG_PATH="${DETACHED_FILE_TRACE_LOG}" \
    SESSION_BACKEND_TRANSCRIPT_LOG_PATH="${DETACHED_TRANSCRIPT_LOG}" \
    "${DETACHED_BACKEND}" send "plan-next-step"
)"
grep -q '^sent=plan-next-step$' <<<"${send_output}"
grep -q '^timeline_seq=2$' <<<"${send_output}"
grep -q "event=command session_id=${DETACHED_SESSION} timeline_seq=2 command=plan-next-step argv=plan-next-step source=attach" "${DETACHED_AUDIT_LOG}"

show_output="$(
  SESSION_BACKEND_STATE_FILE="${DETACHED_STATE_FILE}" \
    SESSION_BACKEND_AUDIT_LOG="${DETACHED_AUDIT_LOG}" \
    SESSION_BACKEND_SESSION_ID="${DETACHED_SESSION}" \
    SESSION_BACKEND_WORKSPACE_PATH="${DETACHED_WORKSPACE}" \
    SESSION_BACKEND_GIT_BRANCH="${DETACHED_BRANCH}" \
    SESSION_BACKEND_DEBUG_LOG_PATH="${DETACHED_DEBUG_LOG}" \
    SESSION_BACKEND_FILE_TRACE_LOG_PATH="${DETACHED_FILE_TRACE_LOG}" \
    SESSION_BACKEND_TRANSCRIPT_LOG_PATH="${DETACHED_TRANSCRIPT_LOG}" \
    "${DETACHED_BACKEND}" show
)"
grep -q "^  \"session_id\": \"${DETACHED_SESSION}\"" <<<"${show_output}"
grep -q '^  "status": "running",$' <<<"${show_output}"
grep -q '^  "live_status": "running",$' <<<"${show_output}"
grep -q '^  "control_mode": "detached",$' <<<"${show_output}"
grep -q "^  \"workspace\": \"${DETACHED_WORKSPACE}\",$" <<<"${show_output}"
grep -q "^  \"git_branch\": \"${DETACHED_BRANCH}\",$" <<<"${show_output}"
grep -q "^  \"audit_log_path\": \"${DETACHED_AUDIT_LOG}\",$" <<<"${show_output}"
grep -q "^  \"debug_log_path\": \"${DETACHED_DEBUG_LOG}\",$" <<<"${show_output}"
grep -q "^  \"file_trace_log_path\": \"${DETACHED_FILE_TRACE_LOG}\",$" <<<"${show_output}"
grep -q "^  \"transcript_log_path\": \"${DETACHED_TRANSCRIPT_LOG}\",$" <<<"${show_output}"

stop_output="$(
  SESSION_BACKEND_STATE_FILE="${DETACHED_STATE_FILE}" \
    SESSION_BACKEND_AUDIT_LOG="${DETACHED_AUDIT_LOG}" \
    SESSION_BACKEND_SESSION_ID="${DETACHED_SESSION}" \
    SESSION_BACKEND_WORKSPACE_PATH="${DETACHED_WORKSPACE}" \
    SESSION_BACKEND_GIT_BRANCH="${DETACHED_BRANCH}" \
    SESSION_BACKEND_DEBUG_LOG_PATH="${DETACHED_DEBUG_LOG}" \
    SESSION_BACKEND_FILE_TRACE_LOG_PATH="${DETACHED_FILE_TRACE_LOG}" \
    SESSION_BACKEND_TRANSCRIPT_LOG_PATH="${DETACHED_TRANSCRIPT_LOG}" \
    "${DETACHED_BACKEND}" stop
)"
grep -q "^session_id=${DETACHED_SESSION}$" <<<"${stop_output}"
grep -q '^status=exited$' <<<"${stop_output}"
grep -q '^live_status=stopped$' <<<"${stop_output}"
grep -q '^exit_status=0$' <<<"${stop_output}"
grep -q '^final_assurance=managed-mutable$' <<<"${stop_output}"

export_fixture="$(
  SESSION_BACKEND_STATE_FILE="${DETACHED_STATE_FILE}" \
    SESSION_BACKEND_AUDIT_LOG="${DETACHED_AUDIT_LOG}" \
    SESSION_BACKEND_SESSION_ID="${DETACHED_SESSION}" \
    SESSION_BACKEND_WORKSPACE_PATH="${DETACHED_WORKSPACE}" \
    SESSION_BACKEND_GIT_BRANCH="${DETACHED_BRANCH}" \
    SESSION_BACKEND_DEBUG_LOG_PATH="${DETACHED_DEBUG_LOG}" \
    SESSION_BACKEND_FILE_TRACE_LOG_PATH="${DETACHED_FILE_TRACE_LOG}" \
    SESSION_BACKEND_TRANSCRIPT_LOG_PATH="${DETACHED_TRANSCRIPT_LOG}" \
    "${DETACHED_BACKEND}" export
)"
grep -q "^    \"session_id\": \"${DETACHED_SESSION}\"" <<<"${export_fixture}"
grep -q '^    "status": "exited",$' <<<"${export_fixture}"
grep -q '^    "live_status": "stopped",$' <<<"${export_fixture}"
grep -q '^    "control_mode": "detached",$' <<<"${export_fixture}"
grep -q "^    \"workspace\": \"${DETACHED_WORKSPACE}\",$" <<<"${export_fixture}"
grep -q "^    \"git_branch\": \"${DETACHED_BRANCH}\",$" <<<"${export_fixture}"
grep -q "^    \"audit_log_path\": \"${DETACHED_AUDIT_LOG}\",$" <<<"${export_fixture}"
grep -q '^  "audit_records": \[$' <<<"${export_fixture}"
grep -Fq "workspace=${WORKSPACE_A}/.git/" <<<"${start_output}"
grep -q "event=launch session_id=${DETACHED_SESSION}" "${DETACHED_AUDIT_LOG}"
grep -q "event=exit session_id=${DETACHED_SESSION} timeline_seq=3 exit_status=0 final_assurance=managed-mutable" "${DETACHED_AUDIT_LOG}"
grep -q "event=command session_id=${DETACHED_SESSION} timeline_seq=2 command=plan-next-step argv=plan-next-step source=attach" "${DETACHED_AUDIT_LOG}"
grep -q 'debug-log: detached session observability fixture' "${DETACHED_DEBUG_LOG}"
grep -q 'file-trace: detached session observability fixture' "${DETACHED_FILE_TRACE_LOG}"
grep -q 'transcript: detached session observability fixture' "${DETACHED_TRANSCRIPT_LOG}"

missing_output="$(
  "${ROOT_DIR}/scripts/workcell" session show --id missing-session 2>&1 >/dev/null || true
)"
grep -q 'file does not exist' <<<"${missing_output}"
