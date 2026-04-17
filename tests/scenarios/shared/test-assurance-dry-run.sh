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

run_tty_dry_run() {
  local label="$1"
  shift
  local tty_home_dir="${HOME_DIR}"
  local tty_config_home="${tty_home_dir}/.config"

  HOME_DIR="${tty_home_dir}" XDG_CONFIG_HOME="${tty_config_home}" ROOT_DIR="${ROOT_DIR}" \
    WORKSPACE="${WORKSPACE}" TMP_DIR="${TMP_DIR}" LABEL="${label}" \
    python3 - "$@" <<'PY'
import os
import pty
import subprocess
import sys

label = os.environ["LABEL"]
root_dir = os.environ["ROOT_DIR"]
workspace = os.environ["WORKSPACE"]
tmp_dir = os.environ["TMP_DIR"]
home_dir = os.environ["HOME_DIR"]

cmd = [
    os.path.join(root_dir, "scripts/workcell"),
    *sys.argv[1:],
    "--workspace",
    workspace,
    "--no-default-injection-policy",
    "--dry-run",
]

master_fd, slave_fd = pty.openpty()
proc = subprocess.Popen(
    cmd,
    cwd=root_dir,
    stdin=slave_fd,
    stdout=slave_fd,
    stderr=slave_fd,
    env={
        **os.environ,
        "HOME": home_dir,
        "XDG_CONFIG_HOME": os.path.join(home_dir, ".config"),
    },
)
os.close(slave_fd)
chunks = []
try:
    while True:
        try:
            data = os.read(master_fd, 65536)
        except OSError:
            break
        if not data:
            break
        chunks.append(data)
finally:
    os.close(master_fd)

rc = proc.wait()
output_path = os.path.join(tmp_dir, f"{label}.tty")
with open(output_path, "wb") as fh:
    fh.write(b"".join(chunks))
if rc != 0:
    sys.stderr.write(f"TTY dry-run failed for {label}: rc={rc}\n")
    sys.stderr.write(b"".join(chunks).decode("utf-8", "replace"))
    sys.exit(rc)
PY
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
  if grep -q -- ' --entrypoint bash ' "${TMP_DIR}/arbitrary-command-${agent}.stdout"; then
    echo "arbitrary command mode should stay on the managed runtime entrypoint" >&2
    exit 1
  fi
  grep -q -- '-e WORKCELL_ALLOW_ARBITRARY_COMMAND=1' "${TMP_DIR}/arbitrary-command-${agent}.stdout"
  grep -q -- 'workcell:local bash -lc true ' "${TMP_DIR}/arbitrary-command-${agent}.stdout"
done

run_tty_dry_run "interactive-codex" --agent codex --agent-arg --version
grep -q '^docker run -it ' "${TMP_DIR}/interactive-codex.tty"
if grep -q -- ' docker run --init -it ' "${TMP_DIR}/interactive-codex.tty"; then
  echo "Interactive attached launch should not route SIGWINCH through docker-init" >&2
  exit 1
fi

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
