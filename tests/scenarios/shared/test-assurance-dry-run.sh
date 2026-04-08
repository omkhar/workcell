#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-assurance-scenario.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

HOME_DIR="${TMP_DIR}/home"
WORKSPACE="${TMP_DIR}/workspace"
mkdir -p "${HOME_DIR}/.config" "${WORKSPACE}"
git -C "${WORKSPACE}" init -q
printf 'scenario workspace\n' >"${WORKSPACE}/README.md"

run_dry_run() {
  local label="$1"
  shift
  HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
    "${ROOT_DIR}/scripts/workcell" \
    "$@" \
    --workspace "${WORKSPACE}" \
    --no-default-injection-policy \
    --dry-run \
    >"${TMP_DIR}/${label}.stdout" 2>"${TMP_DIR}/${label}.stderr"
}

run_dry_run_expect_failure() {
  local expected_rc="$1"
  local label="$2"
  shift 2

  set +e
  HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
    "${ROOT_DIR}/scripts/workcell" \
    "$@" \
    --workspace "${WORKSPACE}" \
    --no-default-injection-policy \
    --dry-run \
    >"${TMP_DIR}/${label}.stdout" 2>"${TMP_DIR}/${label}.stderr"
  local rc=$?
  set -e

  if [[ "${rc}" -ne "${expected_rc}" ]]; then
    echo "Unexpected exit code for ${label}: ${rc} (expected ${expected_rc})" >&2
    cat "${TMP_DIR}/${label}.stderr" >&2
    exit 1
  fi
}

for agent in codex claude gemini; do
  run_dry_run "prompt-${agent}" --agent "${agent}" --agent-autonomy prompt --agent-arg --version
  grep -q "^profile=.* mode=strict agent=${agent} " "${TMP_DIR}/prompt-${agent}.stderr"
  grep -q '^agent_autonomy=prompt$' "${TMP_DIR}/prompt-${agent}.stderr"
  grep -q '^autonomy_assurance=lower-assurance-prompt-autonomy$' "${TMP_DIR}/prompt-${agent}.stderr"
  grep -q '^workspace_control_plane=masked$' "${TMP_DIR}/prompt-${agent}.stderr"
  grep -q '^execution_path=managed-tier1 audit_log=' "${TMP_DIR}/prompt-${agent}.stderr"
done

grep -q '^codex_rules_mutability_effective_initial=session$' "${TMP_DIR}/prompt-codex.stderr"
grep -q '^codex_rules_mutability_effective_initial=not-applicable$' "${TMP_DIR}/prompt-claude.stderr"
grep -q '^codex_rules_mutability_effective_initial=not-applicable$' "${TMP_DIR}/prompt-gemini.stderr"

for agent in codex claude gemini; do
  run_dry_run "development-${agent}" --agent "${agent}" --mode development --agent-arg --version
  grep -q "^profile=.* mode=development agent=${agent} " "${TMP_DIR}/development-${agent}.stderr"
  grep -q '^workspace_control_plane=masked$' "${TMP_DIR}/development-${agent}.stderr"
  grep -q '^network_policy=allowlist ' "${TMP_DIR}/development-${agent}.stderr"
  grep -q '^execution_path=lower-assurance-development audit_log=' "${TMP_DIR}/development-${agent}.stderr"
  grep -q '^session_assurance_initial=managed-mutable$' "${TMP_DIR}/development-${agent}.stderr"
done

for agent in codex claude gemini; do
  HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
    "${ROOT_DIR}/scripts/workcell" \
    --agent "${agent}" \
    --mode development \
    --workspace "${WORKSPACE}" \
    --no-default-injection-policy \
    --dry-run \
    -- bash -lc true \
    >"${TMP_DIR}/development-command-${agent}.stdout" 2>"${TMP_DIR}/development-command-${agent}.stderr"
  grep -q "^profile=.* mode=development agent=${agent} " "${TMP_DIR}/development-command-${agent}.stderr"
  grep -q '^execution_path=lower-assurance-development audit_log=' "${TMP_DIR}/development-command-${agent}.stderr"
  grep -q -- ' bash -lc true ' "${TMP_DIR}/development-command-${agent}.stdout"
  if grep -q -- ' --entrypoint bash ' "${TMP_DIR}/development-command-${agent}.stdout"; then
    echo "development mode should keep managed command execution on the reviewed entrypoint" >&2
    exit 1
  fi
  if grep -q -- "workcell:local ${agent} bash -lc true " "${TMP_DIR}/development-command-${agent}.stdout"; then
    echo "development mode should forward explicit shell commands directly instead of prepending the provider binary" >&2
    exit 1
  fi
done

run_dry_run_expect_failure 2 "breakglass-noack" --agent codex --mode breakglass
grep -q '^breakglass mode requires --ack-breakglass\.$' "${TMP_DIR}/breakglass-noack.stderr"

for agent in codex claude gemini; do
  run_dry_run "breakglass-${agent}" --agent "${agent}" --mode breakglass --ack-breakglass
  grep -q "^profile=.* mode=breakglass agent=${agent} " "${TMP_DIR}/breakglass-${agent}.stderr"
  grep -q '^workspace_control_plane=unmasked$' "${TMP_DIR}/breakglass-${agent}.stderr"
  grep -q '^network_policy=unrestricted ' "${TMP_DIR}/breakglass-${agent}.stderr"
  grep -q '^execution_path=lower-assurance-breakglass audit_log=' "${TMP_DIR}/breakglass-${agent}.stderr"
  grep -q '^autonomy_assurance=managed-yolo$' "${TMP_DIR}/breakglass-${agent}.stderr"
  grep -q '^session_assurance_initial=managed-mutable$' "${TMP_DIR}/breakglass-${agent}.stderr"
done

for agent in codex claude gemini; do
  run_dry_run "readonly-${agent}" --agent "${agent}" --container-mutability readonly
  grep -q "^profile=.* mode=strict agent=${agent} " "${TMP_DIR}/readonly-${agent}.stderr"
  grep -q '^container_resources=mutability=readonly ' "${TMP_DIR}/readonly-${agent}.stderr"
  grep -q '^container_assurance=managed-readonly$' "${TMP_DIR}/readonly-${agent}.stderr"
  grep -q '^session_assurance_initial=managed-readonly$' "${TMP_DIR}/readonly-${agent}.stderr"
  grep -q '^workspace_control_plane=masked$' "${TMP_DIR}/readonly-${agent}.stderr"
  grep -q -- '--read-only' "${TMP_DIR}/readonly-${agent}.stdout"
done

set +e
HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
  "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --allow-arbitrary-command \
  --workspace "${WORKSPACE}" \
  --no-default-injection-policy \
  --dry-run \
  -- bash -lc true \
  >"${TMP_DIR}/arbitrary-command-noack.stdout" 2>"${TMP_DIR}/arbitrary-command-noack.stderr"
arbitrary_noack_rc=$?
set -e
test "${arbitrary_noack_rc}" -eq 2
grep -q '^arbitrary command mode requires --ack-arbitrary-command\.$' "${TMP_DIR}/arbitrary-command-noack.stderr"

for agent in codex claude gemini; do
  HOME="${HOME_DIR}" XDG_CONFIG_HOME="${HOME_DIR}/.config" \
    "${ROOT_DIR}/scripts/workcell" \
    --agent "${agent}" \
    --allow-arbitrary-command \
    --ack-arbitrary-command \
    --workspace "${WORKSPACE}" \
    --no-default-injection-policy \
    --dry-run \
    -- bash -lc true \
    >"${TMP_DIR}/arbitrary-command-${agent}.stdout" 2>"${TMP_DIR}/arbitrary-command-${agent}.stderr"
  grep -q "^profile=.* mode=strict agent=${agent} " "${TMP_DIR}/arbitrary-command-${agent}.stderr"
  grep -q '^execution_path=lower-assurance-debug-command audit_log=' "${TMP_DIR}/arbitrary-command-${agent}.stderr"
  grep -q -- ' --entrypoint bash ' "${TMP_DIR}/arbitrary-command-${agent}.stdout"
done

run_dry_run_expect_failure 2 "control-plane-vcs-noack" --agent codex --allow-control-plane-vcs
grep -q '^control-plane VCS mode requires --ack-control-plane-vcs\.$' "${TMP_DIR}/control-plane-vcs-noack.stderr"

for agent in codex claude gemini; do
  run_dry_run "control-plane-vcs-${agent}" \
    --agent "${agent}" \
    --allow-control-plane-vcs \
    --ack-control-plane-vcs
  grep -q "^profile=.* mode=strict agent=${agent} " "${TMP_DIR}/control-plane-vcs-${agent}.stderr"
  grep -q '^session_assurance_initial=lower-assurance-control-plane-vcs$' "${TMP_DIR}/control-plane-vcs-${agent}.stderr"
  grep -q '^workspace_control_plane=readonly-vcs$' "${TMP_DIR}/control-plane-vcs-${agent}.stderr"
  grep -q '^execution_path=lower-assurance-control-plane-vcs audit_log=' "${TMP_DIR}/control-plane-vcs-${agent}.stderr"
done

echo "Assurance dry-run scenario passed"
