#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE_TAG="${WORKCELL_IMAGE_TAG:-workcell:smoke}"
DOCKER_CONTEXT_NAME="${WORKCELL_CONTAINER_SMOKE_DOCKER_CONTEXT:-}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

cleanup_workspace_scratch() {
  rm -rf \
    "${ROOT_DIR}/.workcell-provider-copy-tampered" \
    "${ROOT_DIR}/.workcell-provider-copy-aggressive" \
    "${ROOT_DIR}/.workcell-provider-copy-minimal" \
    "${ROOT_DIR}/.workcell-provider-copy-split" \
    "${ROOT_DIR}/.workcell-benign-marker-package"
  rm -f \
    "${ROOT_DIR}/.workcell-provider-copy-no-package.js"
  rm -rf \
    "${ROOT_DIR}/tmp/.workcell-"* \
    "${ROOT_DIR}/tmp/workcell-"*
}

select_docker_context() {
  if [[ -n "${DOCKER_CONTEXT_NAME}" ]]; then
    return
  fi

  if docker context inspect colima >/dev/null 2>&1; then
    DOCKER_CONTEXT_NAME="colima"
    return
  fi

  if docker context inspect default >/dev/null 2>&1; then
    DOCKER_CONTEXT_NAME="default"
  fi
}

docker_cmd() {
  if [[ -n "${DOCKER_CONTEXT_NAME}" ]]; then
    docker --context "${DOCKER_CONTEXT_NAME}" "$@"
  else
    docker "$@"
  fi
}

run_container() {
  local agent="$1"
  shift

  docker_cmd run --rm \
    --read-only \
    --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
    --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
    --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
    -e AGENT_NAME="${agent}" \
    -e AGENT_UI=cli \
    -e CODEX_PROFILE=strict \
    -e HOME=/state/agent-home \
    -e CODEX_HOME=/state/agent-home/.codex \
    -e TMPDIR=/state/tmp \
    -e WORKCELL_RUNTIME=1 \
    -e WORKSPACE=/workspace \
    -v "${ROOT_DIR}:/workspace" \
    --entrypoint "$1" \
    "${IMAGE_TAG}" "${@:2}"
}

run_entrypoint() {
  local agent="$1"
  shift

  docker_cmd run --rm \
    --read-only \
    --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
    --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
    --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
    -e AGENT_NAME="${agent}" \
    -e AGENT_UI=cli \
    -e CODEX_PROFILE=strict \
    -e HOME=/state/agent-home \
    -e CODEX_HOME=/state/agent-home/.codex \
    -e TMPDIR=/state/tmp \
    -e WORKCELL_RUNTIME=1 \
    -e WORKSPACE=/workspace \
    -v "${ROOT_DIR}:/workspace" \
    "${IMAGE_TAG}" "$@"
}

run_entrypoint_with_profile() {
  local agent="$1"
  local profile="$2"
  shift 2

  docker_cmd run --rm \
    --read-only \
    --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
    --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
    --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
    -e AGENT_NAME="${agent}" \
    -e AGENT_UI=cli \
    -e CODEX_PROFILE="${profile}" \
    -e WORKCELL_MODE="${profile}" \
    -e HOME=/state/agent-home \
    -e CODEX_HOME=/state/agent-home/.codex \
    -e TMPDIR=/state/tmp \
    -e WORKCELL_RUNTIME=1 \
    -e WORKSPACE=/workspace \
    -v "${ROOT_DIR}:/workspace" \
    "${IMAGE_TAG}" "$@"
}

require_tool docker
trap cleanup_workspace_scratch EXIT
cleanup_workspace_scratch
select_docker_context

BUILD_SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}"
SOURCE_DATE_EPOCH="${BUILD_SOURCE_DATE_EPOCH}" docker_cmd buildx build \
  --build-arg "BUILDKIT_MULTI_PLATFORM=1" \
  --build-arg "SOURCE_DATE_EPOCH=${BUILD_SOURCE_DATE_EPOCH}" \
  --provenance=false \
  --sbom=false \
  --load \
  -t "${IMAGE_TAG}" \
  -f "${ROOT_DIR}/runtime/container/Dockerfile" \
  "${ROOT_DIR}" >/dev/null

run_entrypoint codex codex --version >/dev/null
run_entrypoint_with_profile codex build codex --version >/dev/null

if run_entrypoint codex bash -lc true >/tmp/workcell-entrypoint-command.out 2>&1; then
  echo "expected Workcell entrypoint to reject non-provider commands by default" >&2
  exit 1
fi
grep -q "Workcell blocked non-provider command" /tmp/workcell-entrypoint-command.out

if run_entrypoint codex codex --search >/tmp/workcell-entrypoint-codex-search.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex web search outside breakglass" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-search.out

if run_entrypoint codex codex --dangerously-bypass-approvals-and-sandbox >/tmp/workcell-entrypoint-codex-danger.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex dangerous override outside breakglass" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-danger.out

if run_entrypoint codex codex -a never --version >/tmp/workcell-entrypoint-codex-approval.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex approval overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-approval.out

if run_entrypoint codex codex app-server >/tmp/workcell-entrypoint-codex-app-server.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex GUI subcommands on the CLI surface" >&2
  exit 1
fi
grep -q "Workcell blocked unsupported Codex CLI subcommand" /tmp/workcell-entrypoint-codex-app-server.out

if run_entrypoint codex codex --profile breakglass --version >/tmp/workcell-entrypoint-codex-profile.out 2>&1; then
  echo "expected Workcell entrypoint to reject operator-supplied Codex profiles" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-profile.out

if run_entrypoint codex codex --cd /state --version >/tmp/workcell-entrypoint-codex-cd.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex working-directory overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-cd.out

if run_entrypoint codex codex --config profile=breakglass --version >/tmp/workcell-entrypoint-codex-config-profile.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex profile config overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex config override" /tmp/workcell-entrypoint-codex-config-profile.out

if run_entrypoint codex codex --config sandbox_workspace_write.network_access=true --version >/tmp/workcell-entrypoint-codex-config-network.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex network_access config overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex config override" /tmp/workcell-entrypoint-codex-config-network.out

if run_entrypoint codex codex --config sandbox_workspace_write.writable_roots=/state --version >/tmp/workcell-entrypoint-codex-config-writable-roots.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex writable_roots config overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex config override" /tmp/workcell-entrypoint-codex-config-writable-roots.out

if run_entrypoint codex codex --config shell_environment_policy.set.BAD=1 --version >/tmp/workcell-entrypoint-codex-config-shell.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex shell environment overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex config override" /tmp/workcell-entrypoint-codex-config-shell.out

if run_entrypoint codex codex --add-dir=/tmp --version >/tmp/workcell-entrypoint-codex-add-dir.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex add-dir overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-add-dir.out

if run_entrypoint codex codex --remote=ssh://example.invalid/workcell --version >/tmp/workcell-entrypoint-codex-remote.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex remote overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-remote.out

run_entrypoint claude claude --version >/dev/null

if run_entrypoint claude claude --dangerously-skip-permissions >/tmp/workcell-entrypoint-claude-danger.out 2>&1; then
  echo "expected Workcell entrypoint to reject Claude dangerous override outside breakglass" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Claude override" /tmp/workcell-entrypoint-claude-danger.out

if run_entrypoint claude claude --allowedTools Read >/tmp/workcell-entrypoint-claude-allowed-tools.out 2>&1; then
  echo "expected Workcell entrypoint to reject Claude allowedTools overrides outside breakglass" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Claude override" /tmp/workcell-entrypoint-claude-allowed-tools.out

if run_entrypoint claude claude --add-dir=/state --version >/tmp/workcell-entrypoint-claude-add-dir.out 2>&1; then
  echo "expected Workcell entrypoint to reject Claude add-dir overrides outside breakglass" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Claude override" /tmp/workcell-entrypoint-claude-add-dir.out

if run_container codex bash -lc 'AGENT_NAME=claude WORKCELL_MODE=breakglass CODEX_PROFILE=breakglass /usr/local/bin/workcell-entrypoint claude --dangerously-skip-permissions' >/tmp/workcell-entrypoint-direct-claude-breakglass.out 2>&1; then
  echo "expected direct entrypoint Claude breakglass override to fail" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Claude override" /tmp/workcell-entrypoint-direct-claude-breakglass.out

if run_container codex bash -lc 'AGENT_NAME=codex WORKCELL_MODE=breakglass CODEX_PROFILE=breakglass /usr/local/bin/workcell-entrypoint codex --profile breakglass --version' >/tmp/workcell-entrypoint-direct-codex-breakglass.out 2>&1; then
  echo "expected direct entrypoint Codex breakglass profile override to fail" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-direct-codex-breakglass.out

# shellcheck disable=SC2016
run_container codex bash -lc '
  test "$(id -u)" != 0
  test "$WORKCELL_RUNTIME" = "1"
  test "$TMPDIR" = "/state/tmp"
  mkdir -p "$TMPDIR"
  touch "$TMPDIR/workcell-tmpdir-ok"
  EXEC_TMP="$TMPDIR/workcell-exec"
  mkdir -p "$EXEC_TMP"
  codex --version | grep -q "codex-cli"
  LD_PRELOAD=/workspace/workcell-does-not-exist.so codex --version | grep -q "codex-cli"
  LD_PRELOAD=/workspace/workcell-does-not-exist.so claude --version >/dev/null
  LD_PRELOAD=/workspace/workcell-does-not-exist.so git --version | grep -q "git version"
  LD_PRELOAD=/workspace/workcell-does-not-exist.so node --version | grep -q "^v"
  test -f "$CODEX_HOME/config.toml"
  test -L "$CODEX_HOME/config.toml"
  test "$(readlink "$CODEX_HOME/config.toml")" = "/opt/workcell/adapters/codex/.codex/config.toml"
  codex features list >/dev/null
  if command -v python3 >/tmp/python-which.out 2>&1; then
    echo "expected runtime image to omit python3 from the operator PATH" >&2
    exit 1
  fi
  if command -v perl >/tmp/perl-which.out 2>&1; then
    echo "expected runtime image to omit perl from the operator PATH" >&2
    exit 1
  fi
  if find /usr/bin -maxdepth 1 -name "perl*" -print -quit | grep -q .; then
    echo "expected runtime image to remove raw Perl entrypoints" >&2
    exit 1
  fi
  cp /bin/true /tmp/workcell-noexec
  chmod 0700 /tmp/workcell-noexec
  if /tmp/workcell-noexec >/tmp/workcell-noexec.out 2>&1; then
    echo "expected /tmp to be mounted noexec" >&2
    exit 1
  fi
  grep -Eq "Permission denied|Operation not permitted" /tmp/workcell-noexec.out
  if codex --search >/tmp/codex-nested-search.out 2>&1; then
    echo "expected nested Codex invocation to reject unsafe overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Codex override" /tmp/codex-nested-search.out
  if codex --cd /state --version >/tmp/codex-nested-cd.out 2>&1; then
    echo "expected nested Codex invocation to reject working-directory overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Codex override" /tmp/codex-nested-cd.out
  if codex --config sandbox_workspace_write.writable_roots=/state --version >/tmp/codex-nested-config-writable-roots.out 2>&1; then
    echo "expected nested Codex invocation to reject writable_roots overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Codex config override" /tmp/codex-nested-config-writable-roots.out
  if WORKCELL_MODE=breakglass CODEX_PROFILE=breakglass codex --search >/tmp/codex-nested-breakglass.out 2>&1; then
    echo "expected nested Codex invocation to ignore caller-supplied breakglass env" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Codex override" /tmp/codex-nested-breakglass.out
  if codex -a never --version >/tmp/codex-nested-approval.out 2>&1; then
    echo "expected nested Codex invocation to reject approval overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Codex override" /tmp/codex-nested-approval.out
  if codex app-server >/tmp/codex-nested-app-server.out 2>&1; then
    echo "expected nested Codex invocation to reject GUI subcommands on the CLI surface" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsupported Codex CLI subcommand" /tmp/codex-nested-app-server.out
  rm -f "$CODEX_HOME/config.toml"
  printf "web_search = \"enabled\"\n" >"$CODEX_HOME/config.toml"
  codex --version >/dev/null
  test -L "$CODEX_HOME/config.toml"
  test "$(readlink "$CODEX_HOME/config.toml")" = "/opt/workcell/adapters/codex/.codex/config.toml"
  if /usr/local/libexec/workcell/real/codex --version >/tmp/codex-real-path.out 2>&1; then
    echo "expected direct real Codex payload execution to fail" >&2
    exit 1
  fi
  codex execpolicy check --rules /workspace/adapters/codex/.codex/rules/default.rules rm -rf build \
    | jq -e ".decision == \"forbidden\"" >/dev/null
  grep -q "Do not bypass git hooks with --no-verify or git commit -n from Workcell." \
    /workspace/adapters/codex/.codex/rules/default.rules
  grep -q "git commit --no-verify -m test" \
    /workspace/adapters/codex/.codex/rules/default.rules
  grep -q "git commit -n -m test" \
    /workspace/adapters/codex/.codex/rules/default.rules
  codex execpolicy check --rules /workspace/adapters/codex/.codex/rules/default.rules /usr/local/libexec/workcell/core/claude --dangerously-skip-permissions \
    | jq -e ".decision == \"forbidden\"" >/dev/null
  codex execpolicy check --rules /workspace/adapters/codex/.codex/rules/default.rules node /opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli.js --dangerously-skip-permissions \
    | jq -e ".decision == \"forbidden\"" >/dev/null
  cat <<'\''EOF'\'' >/tmp/workcell-bashenv.sh
touch /tmp/workcell-bashenv-ran
EOF
  rm -f /tmp/workcell-bashenv-ran
  BASH_ENV=/tmp/workcell-bashenv.sh node --version >/tmp/node-bashenv.out 2>&1
  test ! -e /tmp/workcell-bashenv-ran
  cat <<'\''EOF'\'' >/tmp/workcell-wrapper-bashenv.sh
exec env -u LD_PRELOAD /usr/local/libexec/workcell/real/codex --version
EOF
  if BASH_ENV=/tmp/workcell-wrapper-bashenv.sh bash /usr/local/libexec/workcell/provider-wrapper.sh >/tmp/provider-wrapper-bashenv.out 2>&1; then
    echo "expected explicit bash launch of provider wrapper with hostile BASH_ENV to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/provider-wrapper-bashenv.out
  cat <<'\''EOF'\'' >/tmp/workcell-node-wrapper-bashenv.sh
exec env -u LD_PRELOAD /usr/local/libexec/workcell/real/node --version
EOF
  if BASH_ENV=/tmp/workcell-node-wrapper-bashenv.sh bash /usr/local/libexec/workcell/node-wrapper.sh --version >/tmp/node-wrapper-bashenv.out 2>&1; then
    echo "expected explicit bash launch of node wrapper with hostile BASH_ENV to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/node-wrapper-bashenv.out
  set -- $(ldd /usr/local/libexec/workcell/real/node | grep -E "ld-linux|ld-musl" | head -n1)
  LOADER="$1"
  test -n "$LOADER"
  if env -u LD_PRELOAD "$LOADER" /usr/local/libexec/workcell/real/node --version >/tmp/node-loader-real.out 2>&1; then
    echo "expected direct loader invocation of the real Node payload to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/node-loader-real.out
  if env -u LD_PRELOAD "$LOADER" /usr/local/libexec/workcell/real/codex --version >/tmp/codex-loader-real.out 2>&1; then
    echo "expected direct loader invocation of the real Codex payload to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/codex-loader-real.out
  cp /bin/true "$EXEC_TMP/workcell-state-native"
  chmod 0700 "$EXEC_TMP/workcell-state-native"
  if "$EXEC_TMP/workcell-state-native" >/tmp/state-native.out 2>&1; then
    echo "expected strict profile to reject direct native executable launches from /state" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/state-native.out
  if env -u LD_PRELOAD "$LOADER" "$EXEC_TMP/workcell-state-native" >/tmp/state-native-loader.out 2>&1; then
    echo "expected strict profile to reject loader-mediated native executable launches from /state" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/state-native-loader.out
  if WORKCELL_MODE=breakglass "$EXEC_TMP/workcell-state-native" >/tmp/state-native-workcell-mode-bypass.out 2>&1; then
    echo "expected strict profile to ignore caller-supplied WORKCELL_MODE for mutable native execution" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/state-native-workcell-mode-bypass.out
  if CODEX_PROFILE=build "$EXEC_TMP/workcell-state-native" >/tmp/state-native-codex-profile-bypass.out 2>&1; then
    echo "expected strict profile to ignore caller-supplied CODEX_PROFILE for mutable native execution" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/state-native-codex-profile-bypass.out
  mkdir -p /workspace/tmp
  cp /bin/true /workspace/tmp/.workcell-native-helper
  chmod 0700 /workspace/tmp/.workcell-native-helper
  if /workspace/tmp/.workcell-native-helper >/tmp/workspace-native.out 2>&1; then
    echo "expected strict profile to reject direct native executable launches from /workspace" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/workspace-native.out
  cp /bin/true /workspace/tmp/.workcell-native-helper-deleted-fd
  chmod 0700 /workspace/tmp/.workcell-native-helper-deleted-fd
  exec 3</workspace/tmp/.workcell-native-helper-deleted-fd
  rm -f /workspace/tmp/.workcell-native-helper-deleted-fd
  if /proc/self/fd/3 >/tmp/workspace-native-deleted-fd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches from /workspace" >&2
    exec 3<&-
    exit 1
  fi
  if /dev/fd/3 >/tmp/workspace-native-deleted-devfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches via /dev/fd from /workspace" >&2
    exec 3<&-
    exit 1
  fi
  if /proc/self/fd/./3 >/tmp/workspace-native-deleted-dotfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches via normalized /proc/self/fd path from /workspace" >&2
    exec 3<&-
    exit 1
  fi
  if /proc/thread-self/fd/3 >/tmp/workspace-native-deleted-threadself.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches via /proc/thread-self/fd from /workspace" >&2
    exec 3<&-
    exit 1
  fi
  if /proc/self/task/"$BASHPID"/fd/3 >/tmp/workspace-native-deleted-taskfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches via /proc/self/task/<tid>/fd from /workspace" >&2
    exec 3<&-
    exit 1
  fi
  if /dev/stdin <&3 >/tmp/workspace-native-deleted-stdin.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches via /dev/stdin from /workspace" >&2
    exec 3<&-
    exit 1
  fi
  exec 3<&-
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/workspace-native-deleted-fd.out
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/workspace-native-deleted-devfd.out
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/workspace-native-deleted-dotfd.out
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/workspace-native-deleted-threadself.out
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/workspace-native-deleted-taskfd.out
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/workspace-native-deleted-stdin.out
  cp /bin/true /workspace/tmp/.workcell-native-helper-deleted-pidfd
  chmod 0700 /workspace/tmp/.workcell-native-helper-deleted-pidfd
  if (
    exec 5</workspace/tmp/.workcell-native-helper-deleted-pidfd
    rm -f /workspace/tmp/.workcell-native-helper-deleted-pidfd
    exec /proc/"$BASHPID"/fd/5
  ) >/tmp/workspace-native-deleted-pidfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches via /proc/\$\$/fd from /workspace" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/workspace-native-deleted-pidfd.out
  cp /bin/true /workspace/tmp/.workcell-native-helper-deleted-stdout
  chmod 0700 /workspace/tmp/.workcell-native-helper-deleted-stdout
  if (
    exec 5</workspace/tmp/.workcell-native-helper-deleted-stdout
    rm -f /workspace/tmp/.workcell-native-helper-deleted-stdout
    exec 1<&5
    exec /dev/stdout
  ) >/tmp/workspace-native-deleted-stdout.out 2>/tmp/workspace-native-deleted-stdout.err; then
    echo "expected strict profile to reject deleted-fd native executable launches via /dev/stdout from /workspace" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/workspace-native-deleted-stdout.err
  cp /bin/true /workspace/tmp/.workcell-native-helper-deleted-stderr
  chmod 0700 /workspace/tmp/.workcell-native-helper-deleted-stderr
  if (
    exec 5</workspace/tmp/.workcell-native-helper-deleted-stderr
    rm -f /workspace/tmp/.workcell-native-helper-deleted-stderr
    exec 2<&5
    exec /dev/stderr
  ) >/tmp/workspace-native-deleted-stderr.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches via /dev/stderr from /workspace" >&2
    exit 1
  fi
  if env -u LD_PRELOAD "$LOADER" /workspace/tmp/.workcell-native-helper >/tmp/workspace-native-loader.out 2>&1; then
    echo "expected strict profile to reject loader-mediated native executable launches from /workspace" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/workspace-native-loader.out
  cat >/workspace/tmp/.workcell-node-shebang <<EOF
#!/usr/local/libexec/workcell/real/node
console.log("workcell shebang bypass");
EOF
  chmod 0700 /workspace/tmp/.workcell-node-shebang
  if /workspace/tmp/.workcell-node-shebang >/tmp/workspace-node-shebang.out 2>&1; then
    echo "expected strict profile to reject mutable shebang execution of the real Node payload" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-node-shebang.out
  cat >/workspace/tmp/.workcell-node-shebang-deleted-fd <<EOF
#!/usr/local/libexec/workcell/real/node
console.log("workcell deleted fd shebang bypass");
EOF
  chmod 0700 /workspace/tmp/.workcell-node-shebang-deleted-fd
  exec 4</workspace/tmp/.workcell-node-shebang-deleted-fd
  rm -f /workspace/tmp/.workcell-node-shebang-deleted-fd
  if /proc/self/fd/4 >/tmp/workspace-node-shebang-deleted-fd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution of the real Node payload" >&2
    exec 4<&-
    exit 1
  fi
  if /dev/fd/4 >/tmp/workspace-node-shebang-deleted-devfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via /dev/fd of the real Node payload" >&2
    exec 4<&-
    exit 1
  fi
  if /proc/self/fd/./4 >/tmp/workspace-node-shebang-deleted-dotfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via normalized /proc/self/fd path of the real Node payload" >&2
    exec 4<&-
    exit 1
  fi
  if /proc/thread-self/fd/4 >/tmp/workspace-node-shebang-deleted-threadself.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via /proc/thread-self/fd of the real Node payload" >&2
    exec 4<&-
    exit 1
  fi
  if /proc/self/task/"$BASHPID"/fd/4 >/tmp/workspace-node-shebang-deleted-taskfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via /proc/self/task/<tid>/fd of the real Node payload" >&2
    exec 4<&-
    exit 1
  fi
  if /dev/stdin <&4 >/tmp/workspace-node-shebang-deleted-stdin.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via /dev/stdin of the real Node payload" >&2
    exec 4<&-
    exit 1
  fi
  exec 4<&-
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-node-shebang-deleted-fd.out
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-node-shebang-deleted-devfd.out
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-node-shebang-deleted-dotfd.out
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-node-shebang-deleted-threadself.out
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-node-shebang-deleted-taskfd.out
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-node-shebang-deleted-stdin.out
  cat >/workspace/tmp/.workcell-node-shebang-deleted-pidfd <<EOF
#!/usr/local/libexec/workcell/real/node
console.log("workcell pid fd deleted shebang bypass");
EOF
  chmod 0700 /workspace/tmp/.workcell-node-shebang-deleted-pidfd
  if (
    exec 5</workspace/tmp/.workcell-node-shebang-deleted-pidfd
    rm -f /workspace/tmp/.workcell-node-shebang-deleted-pidfd
    exec /proc/"$BASHPID"/fd/5
  ) >/tmp/workspace-node-shebang-deleted-pidfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via /proc/\$\$/fd of the real Node payload" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-node-shebang-deleted-pidfd.out
  cat >/workspace/tmp/.workcell-node-shebang-deleted-stdout <<EOF
#!/usr/local/libexec/workcell/real/node
console.log("workcell stdout deleted shebang bypass");
EOF
  chmod 0700 /workspace/tmp/.workcell-node-shebang-deleted-stdout
  if (
    exec 5</workspace/tmp/.workcell-node-shebang-deleted-stdout
    rm -f /workspace/tmp/.workcell-node-shebang-deleted-stdout
    exec 1<&5
    exec /dev/stdout
  ) >/tmp/workspace-node-shebang-deleted-stdout.out 2>/tmp/workspace-node-shebang-deleted-stdout.err; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via /dev/stdout of the real Node payload" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-node-shebang-deleted-stdout.err
  cat >/workspace/tmp/.workcell-node-shebang-deleted-stderr <<EOF
#!/usr/local/libexec/workcell/real/node
console.log("workcell stderr deleted shebang bypass");
EOF
  chmod 0700 /workspace/tmp/.workcell-node-shebang-deleted-stderr
  if (
    exec 5</workspace/tmp/.workcell-node-shebang-deleted-stderr
    rm -f /workspace/tmp/.workcell-node-shebang-deleted-stderr
    exec 2<&5
    exec /dev/stderr
  ) >/tmp/workspace-node-shebang-deleted-stderr.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via /dev/stderr of the real Node payload" >&2
    exit 1
  fi
  cat >/workspace/tmp/.workcell-loader-node-shebang <<EOF
#!${LOADER} /usr/local/libexec/workcell/real/node
console.log("workcell loader shebang bypass");
EOF
  chmod 0700 /workspace/tmp/.workcell-loader-node-shebang
  if /workspace/tmp/.workcell-loader-node-shebang >/tmp/workspace-loader-node-shebang.out 2>&1; then
    echo "expected strict profile to reject mutable loader shebang execution of the real Node payload" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-loader-node-shebang.out
  cat >/workspace/tmp/.workcell-env-node-shebang <<EOF
#!/usr/bin/env -S /usr/local/libexec/workcell/real/node
console.log("workcell env shebang bypass");
EOF
  chmod 0700 /workspace/tmp/.workcell-env-node-shebang
  if /workspace/tmp/.workcell-env-node-shebang >/tmp/workspace-env-node-shebang.out 2>&1; then
    echo "expected strict profile to reject env -S shebang execution of the real Node payload" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-env-node-shebang.out
  cat >/workspace/tmp/.workcell-env-loader-node-shebang <<EOF
#!/usr/bin/env -S ${LOADER} /usr/local/libexec/workcell/real/node
console.log("workcell env loader shebang bypass");
EOF
  chmod 0700 /workspace/tmp/.workcell-env-loader-node-shebang
  if /workspace/tmp/.workcell-env-loader-node-shebang >/tmp/workspace-env-loader-node-shebang.out 2>&1; then
    echo "expected strict profile to reject env -S loader shebang execution of the real Node payload" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-env-loader-node-shebang.out
  cp /usr/local/libexec/workcell/real/node /workspace/tmp/node
  chmod 0700 /workspace/tmp/node
  cat >/workspace/tmp/.workcell-env-path-node-shebang <<EOF
#!/usr/bin/env -S PATH=/workspace/tmp node --version
EOF
  chmod 0700 /workspace/tmp/.workcell-env-path-node-shebang
  if /workspace/tmp/.workcell-env-path-node-shebang >/tmp/workspace-env-path-node-shebang.out 2>&1; then
    echo "expected strict profile to reject env -S PATH-rebound execution of a protected Node copy" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-env-path-node-shebang.out
  if env -i PATH=/workspace/tmp /usr/bin/env node --version >/tmp/env-path-node.out 2>&1; then
    echo "expected strict profile to reject env basename resolution to a protected Node copy" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/env-path-node.out
  cat <<'\''EOF'\'' >/workspace/tmp/workcell-child-envp-bypass.js
const fs = require("node:fs");
const { spawnSync } = require("node:child_process");

fs.copyFileSync("/usr/local/libexec/workcell/real/node", "/workspace/tmp/node");
fs.chmodSync("/workspace/tmp/node", 0o700);
fs.writeFileSync(
  "/workspace/tmp/shebang-bypass",
  "#!/usr/bin/env node\nconsole.log(\"bypass-ok\")\n",
  { mode: 0o700 },
);

const result = spawnSync("/workspace/tmp/shebang-bypass", [], {
  encoding: "utf8",
  env: { PATH: "/workspace/tmp" },
});

if (
  result.status === 0 ||
  !result.stderr.includes(
    "Workcell blocked direct protected runtime execution",
  )
) {
  process.stderr.write(
    `unexpected child-envp shebang result: ${JSON.stringify(result)}\n`,
  );
  process.exit(1);
}

console.log("child-envp-shebang-blocked");
EOF
  node /workspace/tmp/workcell-child-envp-bypass.js >/tmp/workspace-child-envp-bypass.out 2>/tmp/workspace-child-envp-bypass.err
  grep -q "child-envp-shebang-blocked" /tmp/workspace-child-envp-bypass.out
  cat >/workspace/tmp/.workcell-env-shell-node-shebang <<EOF
#!/usr/bin/env -S /bin/sh -c /usr/local/libexec/workcell/real/node
EOF
  chmod 0700 /workspace/tmp/.workcell-env-shell-node-shebang
  if /workspace/tmp/.workcell-env-shell-node-shebang >/tmp/workspace-env-shell-node-shebang.out 2>&1; then
    echo "expected strict profile to reject env -S shell trampoline execution of the real Node payload" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-env-shell-node-shebang.out
  rm -f /workspace/tmp/.workcell-node-shebang /workspace/tmp/.workcell-loader-node-shebang
  rm -f /workspace/tmp/.workcell-env-node-shebang /workspace/tmp/.workcell-env-loader-node-shebang /workspace/tmp/.workcell-env-path-node-shebang /workspace/tmp/.workcell-env-shell-node-shebang
  rm -f /workspace/tmp/shebang-bypass /workspace/tmp/workcell-child-envp-bypass.js
  rm -f /workspace/tmp/node
  rm -f /workspace/tmp/.workcell-native-helper
  cp /usr/local/libexec/workcell/real/node "$EXEC_TMP/workcell-node-real-copy"
  chmod 0700 "$EXEC_TMP/workcell-node-real-copy"
  if "$EXEC_TMP/workcell-node-real-copy" --version >/tmp/node-real-copy.out 2>&1; then
    echo "expected renamed copy of the real Node payload to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/node-real-copy.out
  if env -u LD_PRELOAD "$LOADER" "$EXEC_TMP/workcell-node-real-copy" --version >/tmp/node-loader-copy.out 2>&1; then
    echo "expected loader invocation of a renamed real Node copy to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/node-loader-copy.out
  if node /opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli.js --dangerously-skip-permissions >/tmp/node-provider-claude.out 2>&1; then
    echo "expected Workcell node wrapper to reject direct Claude provider script execution" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct provider script execution via node." /tmp/node-provider-claude.out
  if node /opt/workcell/providers/node_modules/@google/gemini-cli/dist/index.js --yolo >/tmp/node-provider-gemini.out 2>&1; then
    echo "expected Workcell node wrapper to reject direct Gemini provider script execution" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct provider script execution via node." /tmp/node-provider-gemini.out
  if node /opt/workcell/providers/node_modules/@anthropic-ai/claude-code//cli.js --dangerously-skip-permissions >/tmp/node-provider-claude-alias.out 2>&1; then
    echo "expected Workcell node wrapper to reject canonicalized Claude provider path aliases" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct provider script execution via node." /tmp/node-provider-claude-alias.out
  ln -sf /opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli.js /tmp/workcell-claude-provider-link.js
  if node /tmp/workcell-claude-provider-link.js --dangerously-skip-permissions >/tmp/node-provider-claude-symlink.out 2>&1; then
    echo "expected Workcell node wrapper to reject symlinked Claude provider entrypoints" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct provider script execution via node." /tmp/node-provider-claude-symlink.out
  if node --import /tmp/workcell-claude-provider-link.js -e "" >/tmp/node-provider-claude-import.out 2>&1; then
    echo "expected Workcell node wrapper to reject symlinked provider imports" >&2
    exit 1
  fi
  grep -q "Workcell blocked dynamic Node code-loading option outside provider wrappers." /tmp/node-provider-claude-import.out
  if node -e '\''require("/opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli.js")'\'' >/tmp/node-provider-eval.out 2>&1; then
    echo "expected Workcell node wrapper to reject provider requires via node -e" >&2
    exit 1
  fi
  grep -q "Workcell blocked dynamic Node code-loading option outside provider wrappers." /tmp/node-provider-eval.out
  if node --require /opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli.js -e "" >/tmp/node-provider-require.out 2>&1; then
    echo "expected Workcell node wrapper to reject provider requires via node --require" >&2
    exit 1
  fi
  grep -q "Workcell blocked dynamic Node code-loading option outside provider wrappers." /tmp/node-provider-require.out
  if WORKCELL_ALLOW_PROVIDER_NODE=1 node /opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli.js --dangerously-skip-permissions >/tmp/node-provider-env.out 2>&1; then
    echo "expected Workcell node wrapper to ignore caller-supplied provider bypass env" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct provider script execution via node." /tmp/node-provider-env.out
  cp /bin/true /workspace/tmp/not-an-addon.node
  cat <<'\''EOF'\'' >/workspace/tmp/workcell-native-addon-require.js
try {
  require("/workspace/tmp/not-an-addon.node");
  console.error("expected Workcell to block native addon loading");
  process.exit(1);
} catch (error) {
  if (!String(error).includes("Workcell blocked native addon loading via public node.")) {
    throw error;
  }
  console.log("native-addon-load-blocked");
}
EOF
  node /workspace/tmp/workcell-native-addon-require.js >/tmp/node-native-addon.out 2>&1
  grep -q "native-addon-load-blocked" /tmp/node-native-addon.out
  cp -R /opt/workcell/providers /tmp/workcell-provider-copy
  if node /tmp/workcell-provider-copy/node_modules/@anthropic-ai/claude-code/cli.js --version >/tmp/node-provider-copy-claude.out 2>&1; then
    echo "expected copied Claude provider package execution via public node to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked provider package execution via public node." /tmp/node-provider-copy-claude.out
  if node /tmp/workcell-provider-copy/node_modules/@google/gemini-cli/dist/index.js --version >/tmp/node-provider-copy-gemini.out 2>&1; then
    echo "expected copied Gemini provider package execution via public node to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked provider package execution via public node." /tmp/node-provider-copy-gemini.out
  cat <<'\''EOF'\'' >/tmp/workcell-provider-import.mjs
await import("/tmp/workcell-provider-copy/node_modules/@anthropic-ai/claude-code/cli.js");
EOF
  if node /tmp/workcell-provider-import.mjs >/tmp/node-provider-copy-import.out 2>&1; then
    echo "expected imported copied provider package execution via public node to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked provider package execution via public node." /tmp/node-provider-copy-import.out
  cp -R /opt/workcell/providers/node_modules/@anthropic-ai/claude-code /tmp/workcell-provider-copy-tampered
  jq ".name = \"@workcell/not-claude\"" /tmp/workcell-provider-copy-tampered/package.json >/tmp/workcell-provider-copy-tampered/package.json.new
  mv /tmp/workcell-provider-copy-tampered/package.json.new /tmp/workcell-provider-copy-tampered/package.json
  printf "\n// tampered copy\n" >>/tmp/workcell-provider-copy-tampered/cli.js
  if node /tmp/workcell-provider-copy-tampered/cli.js --version >/tmp/node-provider-copy-tampered.out 2>&1; then
    echo "expected tampered copied Claude provider package execution via public node to fail" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked provider package execution via public node.|Workcell blocked public node execution outside the mounted workspace." /tmp/node-provider-copy-tampered.out
  rm -rf /workspace/.workcell-provider-copy-tampered
  cp -R /opt/workcell/providers/node_modules/@anthropic-ai/claude-code /workspace/.workcell-provider-copy-tampered
  jq ".name = \"@workcell/not-claude-workspace\"" /workspace/.workcell-provider-copy-tampered/package.json >/workspace/.workcell-provider-copy-tampered/package.json.new
  mv /workspace/.workcell-provider-copy-tampered/package.json.new /workspace/.workcell-provider-copy-tampered/package.json
  printf "\n// tampered workspace copy\n" >>/workspace/.workcell-provider-copy-tampered/cli.js
  if node /workspace/.workcell-provider-copy-tampered/cli.js --version >/tmp/node-provider-copy-workspace.out 2>&1; then
    echo "expected tampered workspace Claude provider copy execution via public node to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked provider package execution via public node." /tmp/node-provider-copy-workspace.out
  rm -rf /workspace/.workcell-provider-copy-tampered
  rm -rf /workspace/.workcell-provider-copy-aggressive
  cp -R /opt/workcell/providers/node_modules/@anthropic-ai/claude-code /workspace/.workcell-provider-copy-aggressive
  cat <<'\''EOF'\'' >/workspace/tmp/workcell-provider-copy-scrub.js
const fs = require("node:fs");
const path = require("node:path");

const packageRoot = process.argv[2];
const packageJsonPath = path.join(packageRoot, "package.json");
const packageJson = JSON.parse(fs.readFileSync(packageJsonPath, "utf8"));
packageJson.name = "@workcell/not-claude-workspace-aggressive";
fs.writeFileSync(packageJsonPath, JSON.stringify(packageJson, null, 2) + "\n");

for (const relativePath of ["README.md", "LICENSE.md", "sdk-tools.d.ts", "resvg.wasm"]) {
  fs.rmSync(path.join(packageRoot, relativePath), { force: true });
}

const entrypointPath = path.join(packageRoot, "cli.js");
let entrypoint = fs.readFileSync(entrypointPath, "utf8");
for (const [from, to] of [
  ["Anthropic PBC. All rights reserved.", "Workcell scrubbed marker"],
  ["https://code.claude.com/docs/en/legal-and-compliance.", "https://example.invalid/workcell"],
  ["Want to see the unminified source? We\x27re hiring!", "Workcell scrubbed hiring marker"],
  ["dangerously-skip-permissions", "scrubbed-danger-flag"],
]) {
  entrypoint = entrypoint.split(from).join(to);
}
fs.writeFileSync(entrypointPath, entrypoint);
EOF
  node /workspace/tmp/workcell-provider-copy-scrub.js /workspace/.workcell-provider-copy-aggressive >/tmp/node-provider-copy-scrub.out 2>&1
  if node /workspace/.workcell-provider-copy-aggressive/cli.js --version >/tmp/node-provider-copy-aggressive.out 2>&1; then
    echo "expected aggressively scrubbed workspace Claude provider copy execution via public node to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked provider package execution via public node." /tmp/node-provider-copy-aggressive.out
  rm -rf /workspace/.workcell-provider-copy-aggressive
  rm -rf /workspace/.workcell-provider-copy-minimal
  mkdir -p /workspace/.workcell-provider-copy-minimal
  cp /opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli.js /workspace/.workcell-provider-copy-minimal/main.js
  cp /opt/workcell/providers/node_modules/@anthropic-ai/claude-code/package.json /workspace/.workcell-provider-copy-minimal/
  cat <<'\''EOF'\'' >/workspace/tmp/workcell-provider-copy-minimalize.js
const fs = require("node:fs");
const path = require("node:path");

const packageRoot = process.argv[2];
const packageJsonPath = path.join(packageRoot, "package.json");
const packageJson = JSON.parse(fs.readFileSync(packageJsonPath, "utf8"));
packageJson.name = "@workcell/not-claude-workspace-minimal";
packageJson.workcellTampered = true;
fs.writeFileSync(packageJsonPath, JSON.stringify(packageJson, null, 2) + "\n");

const entrypointPath = path.join(packageRoot, "main.js");
let entrypoint = fs.readFileSync(entrypointPath, "utf8");
for (const [from, to] of [
  ["Anthropic PBC. All rights reserved.", "Workcell scrubbed marker"],
  ["https://code.claude.com/docs/en/legal-and-compliance.", "https://example.invalid/workcell"],
  ["Want to see the unminified source? We\x27re hiring!", "Workcell scrubbed hiring marker"],
  ["dangerously-skip-permissions", "scrubbed-danger-flag"],
]) {
  entrypoint = entrypoint.split(from).join(to);
}
fs.writeFileSync(entrypointPath, entrypoint);
EOF
  node /workspace/tmp/workcell-provider-copy-minimalize.js /workspace/.workcell-provider-copy-minimal >/tmp/node-provider-copy-minimalize.out 2>&1
  if node /workspace/.workcell-provider-copy-minimal/main.js --version >/tmp/node-provider-copy-minimal.out 2>&1; then
    echo "expected minimized scrubbed renamed workspace Claude provider subset execution via public node to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked provider package execution via public node." /tmp/node-provider-copy-minimal.out
  rm -rf /workspace/.workcell-provider-copy-minimal
  rm -rf /workspace/.workcell-provider-copy-split
  mkdir -p /workspace/.workcell-provider-copy-split
  cp /opt/workcell/providers/node_modules/@anthropic-ai/claude-code/package.json /workspace/.workcell-provider-copy-split/
  cat <<'\''EOF'\'' >/workspace/tmp/workcell-provider-copy-split.js
const fs = require("node:fs");
const path = require("node:path");

const packageRoot = process.argv[2];
const sourceEntrypoint = "/opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli.js";
const packageJsonPath = path.join(packageRoot, "package.json");
const packageJson = JSON.parse(fs.readFileSync(packageJsonPath, "utf8"));
packageJson.name = "@workcell/not-claude-workspace-split";
packageJson.workcellTampered = true;
fs.writeFileSync(packageJsonPath, JSON.stringify(packageJson, null, 2) + "\n");

const tokenPattern = /[A-Za-z0-9_./:-]{12,}/g;
const sourceText = fs.readFileSync(sourceEntrypoint, "utf8");
const tokens = [...new Set(sourceText.match(tokenPattern) ?? [])].slice(0, 24);
if (tokens.length !== 24) {
  throw new Error(`expected at least 24 provider signature tokens, received ${tokens.length}`);
}

fs.writeFileSync(
  path.join(packageRoot, "main.js"),
  [
    "import \"./part-a.js\";",
    "import \"./part-b.js\";",
    "import \"./part-c.js\";",
    "console.log(\"workcell split provider smoke\");",
    "",
  ].join("\n"),
);

for (const [index, partTokens] of [
  [0, tokens.slice(0, 8)],
  [1, tokens.slice(8, 16)],
  [2, tokens.slice(16, 24)],
]) {
  fs.writeFileSync(
    path.join(packageRoot, `part-${String.fromCharCode(97 + index)}.js`),
    `export const tokenChunk${index} = ${JSON.stringify(partTokens.join(" "))};\n`,
  );
}
EOF
  node /workspace/tmp/workcell-provider-copy-split.js /workspace/.workcell-provider-copy-split >/tmp/node-provider-copy-splitize.out 2>&1
  if node /workspace/.workcell-provider-copy-split/main.js >/tmp/node-provider-copy-split.out 2>&1; then
    echo "expected split workspace Claude provider subset execution via public node to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked provider package execution via public node." /tmp/node-provider-copy-split.out
  rm -rf /workspace/.workcell-provider-copy-split
  cp /opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli.js /workspace/.workcell-provider-copy-no-package.js
  cat <<'\''EOF'\'' >/workspace/tmp/workcell-provider-copy-no-package.js
const fs = require("node:fs");

const entrypointPath = process.argv[2];
let entrypoint = fs.readFileSync(entrypointPath, "utf8");
for (const [from, to] of [
  ["Anthropic PBC. All rights reserved.", "Workcell scrubbed marker"],
  ["https://code.claude.com/docs/en/legal-and-compliance.", "https://example.invalid/workcell"],
  ["Want to see the unminified source? We\x27re hiring!", "Workcell scrubbed hiring marker"],
  ["dangerously-skip-permissions", "scrubbed-danger-flag"],
]) {
  entrypoint = entrypoint.split(from).join(to);
}
fs.writeFileSync(entrypointPath, entrypoint);
EOF
  node /workspace/tmp/workcell-provider-copy-no-package.js /workspace/.workcell-provider-copy-no-package.js >/tmp/node-provider-copy-no-packageize.out 2>&1
  if node /workspace/.workcell-provider-copy-no-package.js --version >/tmp/node-provider-copy-no-package.out 2>&1; then
    echo "expected package-less scrubbed Claude provider copy execution via public node to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked provider package execution via public node." /tmp/node-provider-copy-no-package.out
  rm -f /workspace/.workcell-provider-copy-no-package.js
  rm -rf /workspace/.workcell-provider-copy-no-package-split
  mkdir -p /workspace/.workcell-provider-copy-no-package-split
  cat <<'\''EOF'\'' >/workspace/tmp/workcell-provider-copy-no-package-split.js
const fs = require("node:fs");
const path = require("node:path");

const root = process.argv[2];
const sourceEntrypoint = "/opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli.js";
const tokenPattern = /[A-Za-z0-9_./:-]{12,}/g;
const sourceText = fs.readFileSync(sourceEntrypoint, "utf8");
const tokens = [...new Set(sourceText.match(tokenPattern) ?? [])].slice(0, 24);
if (tokens.length !== 24) {
  throw new Error(`expected at least 24 provider signature tokens, received ${tokens.length}`);
}

fs.writeFileSync(
  path.join(root, "main.js"),
  [
    "import \"./part-a.js\";",
    "import \"./part-b.js\";",
    "import \"./part-c.js\";",
    "console.log(\"workcell split no-package smoke\");",
    "",
  ].join("\n"),
);

for (const [index, partTokens] of [
  [0, tokens.slice(0, 8)],
  [1, tokens.slice(8, 16)],
  [2, tokens.slice(16, 24)],
]) {
  fs.writeFileSync(
    path.join(root, `part-${String.fromCharCode(97 + index)}.js`),
    `export const tokenChunk${index} = ${JSON.stringify(partTokens.join(" "))};\n`,
  );
}
EOF
  node /workspace/tmp/workcell-provider-copy-no-package-split.js /workspace/.workcell-provider-copy-no-package-split >/tmp/node-provider-copy-no-package-splitize.out 2>&1
  if node /workspace/.workcell-provider-copy-no-package-split/main.js >/tmp/node-provider-copy-no-package-split.out 2>&1; then
    echo "expected split package-less Claude provider subset execution via public node to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked provider package execution via public node." /tmp/node-provider-copy-no-package-split.out
  rm -rf /workspace/.workcell-provider-copy-no-package-split
  rm -rf /workspace/.workcell-benign-marker-package
  mkdir -p /workspace/.workcell-benign-marker-package
  cat <<'\''EOF'\'' >/workspace/.workcell-benign-marker-package/package.json
{
  "name": "@workcell/benign-marker-package",
  "version": "1.0.0",
  "type": "module"
}
EOF
  cat <<'\''EOF'\'' >/workspace/.workcell-benign-marker-package/script.js
console.log("dangerously-skip-permissions");
EOF
  if ! node /workspace/.workcell-benign-marker-package/script.js >/tmp/node-provider-marker-benign.out 2>&1; then
    cat /tmp/node-provider-marker-benign.out >&2
    echo "expected benign workspace package file containing a single provider marker to remain executable" >&2
    exit 1
  fi
  grep -q "dangerously-skip-permissions" /tmp/node-provider-marker-benign.out
  rm -rf /workspace/.workcell-benign-marker-package
  rm -f /workspace/tmp/workcell-provider-copy-scrub.js
  rm -f /workspace/tmp/workcell-provider-copy-minimalize.js
  rm -f /workspace/tmp/workcell-provider-copy-split.js
  rm -f /workspace/tmp/workcell-provider-copy-no-package.js
  rm -f /workspace/tmp/workcell-provider-copy-no-package-split.js
  rm -f /workspace/tmp/not-an-addon.node /workspace/tmp/workcell-native-addon-require.js
  cat <<'\''EOF'\'' >/tmp/workcell-node-public-preload.js
require("fs").writeFileSync("/tmp/workcell-node-public-preload-ran", "1")
process.exit(99)
EOF
  rm -f /tmp/workcell-node-public-preload-ran
  node --version >/tmp/node-public-baseline.out 2>&1
  if ! NODE_OPTIONS=--require=/tmp/workcell-node-public-preload.js node --version >/tmp/node-public-node-options.out 2>&1; then
    cat /tmp/node-public-node-options.out >&2
    echo "expected public node wrapper to ignore caller-supplied NODE_OPTIONS" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-node-public-preload-ran
  cat <<'\''EOF'\'' >/workspace/tmp/git
#!/bin/sh
printf '\''path-bypass-git\n'\''
EOF
  cat <<'\''EOF'\'' >/workspace/tmp/node
#!/bin/sh
printf '\''path-bypass-node\n'\''
EOF
  chmod 0700 /workspace/tmp/git /workspace/tmp/node
  cat <<'\''EOF'\'' >/workspace/tmp/workcell-path-sanitize.js
const { spawnSync } = require("node:child_process");

const git = spawnSync("git", ["--version"], { encoding: "utf8" });
const node = spawnSync("node", ["--version"], { encoding: "utf8" });

if (git.status !== 0 || node.status !== 0) {
  throw new Error(`expected trusted PATH child launches to succeed: ${git.status}/${node.status}`);
}
if (git.stdout.includes("path-bypass-git") || node.stdout.includes("path-bypass-node")) {
  throw new Error("expected Workcell wrappers to ignore caller-controlled PATH for child processes");
}
if (!git.stdout.includes("git version")) {
  throw new Error(`expected real git on PATH, received: ${git.stdout}`);
}
if (!node.stdout.trim().startsWith("v")) {
  throw new Error(`expected real node on PATH, received: ${node.stdout}`);
}

console.log("trusted-path-preserved");
EOF
  env PATH=/workspace/tmp:$PATH /usr/local/bin/node /workspace/tmp/workcell-path-sanitize.js >/tmp/node-path-sanitize.out 2>&1
  grep -q "trusted-path-preserved" /tmp/node-path-sanitize.out
  if grep -q "path-bypass-" /tmp/node-path-sanitize.out; then
    echo "expected public node wrapper to sanitize PATH for child processes" >&2
    exit 1
  fi
  rm -f /workspace/tmp/git /workspace/tmp/node /workspace/tmp/workcell-path-sanitize.js
  if printf '\''console.log("workcell")\n'\'' | node >/tmp/node-stdin.out 2>&1; then
    echo "expected public node wrapper to reject stdin-driven execution" >&2
    exit 1
  fi
  grep -q "Workcell blocked stdin-driven Node execution outside provider wrappers." /tmp/node-stdin.out
  if WORKSPACE=/ node /tmp/workcell-provider-copy-tampered/cli.js --version >/tmp/node-workspace-env.out 2>&1; then
    echo "expected public node wrapper to ignore caller-supplied WORKSPACE overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked public node execution outside the mounted workspace." /tmp/node-workspace-env.out
  cat <<'\''EOF'\'' >/tmp/workcell-node-preload.js
require("fs").writeFileSync("/tmp/workcell-node-preload-ran", "1")
process.exit(99)
EOF
  rm -f /tmp/workcell-node-preload-ran
  if ! NODE_OPTIONS=--require=/tmp/workcell-node-preload.js claude --version >/tmp/claude-node-options.out 2>&1; then
    cat /tmp/claude-node-options.out >&2
    echo "expected Claude provider launch to ignore caller-supplied NODE_OPTIONS" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-node-preload-ran
  rm -f /tmp/workcell-node-preload-ran
  if ! NODE_OPTIONS=--require=/tmp/workcell-node-preload.js gemini --version >/tmp/gemini-node-options.out 2>&1; then
    cat /tmp/gemini-node-options.out >&2
    echo "expected Gemini provider launch to ignore caller-supplied NODE_OPTIONS" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-node-preload-ran
  mkdir -p "$EXEC_TMP/git-guard" && cd "$EXEC_TMP/git-guard"
  git init -q
  git config user.name "Workcell Smoke"
  git config user.email "workcell-smoke@example.com"
  touch smoke.txt
  git add smoke.txt
  if git commit --no-verify -m smoke >/tmp/git-guard.out 2>&1; then
    echo "expected Workcell git guard to reject --no-verify" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard.out
  if /usr/bin/git commit -n -m smoke >/tmp/git-guard-short.out 2>&1; then
    echo "expected Workcell git guard to reject git commit -n" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-short.out
  rm -f /tmp/workcell-git-ssh-env-ran /tmp/workcell-git-ssh-helper-ran /tmp/workcell-git-ssh-config-ran
  cat <<EOF >"$EXEC_TMP/git-ssh-helper.sh"
#!/bin/sh
touch /tmp/workcell-git-ssh-helper-ran
exit 7
EOF
  chmod 0700 "$EXEC_TMP/git-ssh-helper.sh"
  cat <<EOF >"$EXEC_TMP/git-ssh-command.sh"
#!/bin/sh
touch /tmp/workcell-git-ssh-env-ran
exit 7
EOF
  chmod 0700 "$EXEC_TMP/git-ssh-command.sh"
  if GIT_SSH_COMMAND="$EXEC_TMP/git-ssh-command.sh" git ls-remote ssh://example.invalid/workcell.git >/tmp/git-ssh-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_SSH_COMMAND overrides" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-git-ssh-env-ran
  grep -q "Workcell blocked git control-plane override" /tmp/git-ssh-env.out
  if GIT_SSH="$EXEC_TMP/git-ssh-helper.sh" git ls-remote ssh://example.invalid/workcell.git >/tmp/git-ssh-helper.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_SSH overrides" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-git-ssh-helper-ran
  grep -q "Workcell blocked git control-plane override" /tmp/git-ssh-helper.out
  cat <<EOF >"$EXEC_TMP/git-ssh-config.sh"
#!/bin/sh
touch /tmp/workcell-git-ssh-config-ran
exit 7
EOF
  chmod 0700 "$EXEC_TMP/git-ssh-config.sh"
  if git -c core.sshCommand="$EXEC_TMP/git-ssh-config.sh" ls-remote ssh://example.invalid/workcell.git >/tmp/git-ssh-config.out 2>&1; then
    echo "expected Workcell git guard to reject core.sshCommand overrides" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-git-ssh-config-ran
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-ssh-config.out
  cat <<EOF >"$EXEC_TMP/git-credential-helper.sh"
#!/bin/sh
touch /tmp/workcell-git-cred-ran
printf "%s\n%s\n" "username=workcell" "password=secret"
EOF
  chmod 0700 "$EXEC_TMP/git-credential-helper.sh"
  rm -f /tmp/workcell-git-cred-ran
  if git -c credential.helper="!$EXEC_TMP/git-credential-helper.sh" credential fill >/tmp/git-credential-helper.out 2>&1 <<EOF
protocol=https
host=example.invalid

EOF
  then
    echo "expected Workcell git guard to reject credential.helper overrides" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-git-cred-ran
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-credential-helper.out
  cat <<EOF >"$EXEC_TMP/git-askpass.sh"
#!/bin/sh
touch /tmp/workcell-git-askpass-ran
printf "%s\n" "secret"
EOF
  chmod 0700 "$EXEC_TMP/git-askpass.sh"
  rm -f /tmp/workcell-git-askpass-ran
  if GIT_ASKPASS="$EXEC_TMP/git-askpass.sh" git credential fill >/tmp/git-askpass.out 2>&1 <<EOF
protocol=https
host=example.invalid
username=workcell

EOF
  then
    echo "expected Workcell git guard to reject GIT_ASKPASS overrides" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-git-askpass-ran
  grep -q "Workcell blocked git control-plane override" /tmp/git-askpass.out
  if /usr/local/libexec/workcell/core/git commit -n -m smoke >/tmp/git-guard-real.out 2>&1; then
    echo "expected Workcell git guard to reject direct hidden git execution" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-real.out
  if /usr/local/libexec/workcell/real/git status >/tmp/git-guard-real-payload.out 2>&1; then
    echo "expected direct real git payload execution to fail" >&2
    exit 1
  fi
  if ln /usr/local/libexec/workcell/core/git "$EXEC_TMP/git-hardlink" >/tmp/git-hardlink-link.out 2>&1; then
    if "$EXEC_TMP/git-hardlink" commit --no-verify -m smoke >/tmp/git-guard-hardlink.out 2>&1; then
      echo "expected Workcell git guard to reject hardlinked hidden git execution" >&2
      exit 1
    fi
    grep -q "Workcell blocked git hook bypass" /tmp/git-guard-hardlink.out
  else
    grep -Eiq "cross-device|operation not permitted|read-only" /tmp/git-hardlink-link.out
  fi
  ln -s /usr/local/libexec/workcell/core/git "$EXEC_TMP/git-symlink"
  if "$EXEC_TMP/git-symlink" commit -n -m smoke >/tmp/git-guard-symlink.out 2>&1; then
    echo "expected Workcell git guard to reject symlinked hidden git execution" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-symlink.out
  if ! cp /usr/local/libexec/workcell/core/git "$EXEC_TMP/git-copy" >/tmp/git-copy.out 2>&1; then
    echo "expected Workcell git trampoline to remain copyable for deterministic debugging" >&2
    exit 1
  fi
  if "$EXEC_TMP/git-copy" commit --no-verify -m smoke >/tmp/git-guard-copy.out 2>&1; then
    echo "expected copied Workcell git trampoline to reject hook bypasses" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-copy.out
  if git -c core.hooksPath=/dev/null commit -m smoke >/tmp/git-guard-hooks.out 2>&1; then
    echo "expected Workcell git guard to reject inline core.hooksPath override" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-hooks.out
  if git -c core.hookspath=/dev/null commit -m smoke >/tmp/git-guard-hooks-lower.out 2>&1; then
    echo "expected Workcell git guard to reject lowercase inline core.hookspath override" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-hooks-lower.out
  if GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.hooksPath GIT_CONFIG_VALUE_0=/dev/null git commit -m smoke >/tmp/git-guard-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_CONFIG_* hook override" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-env.out
  if GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=Core.HooksPath GIT_CONFIG_VALUE_0=/dev/null git commit -m smoke >/tmp/git-guard-env-mixed.out 2>&1; then
    echo "expected Workcell git guard to reject mixed-case GIT_CONFIG_* hook override" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-env-mixed.out
  printf "[core]\n  hooksPath = /dev/null\n" >"$EXEC_TMP/git-bypass.cfg"
  if git -c include.path="$EXEC_TMP/git-bypass.cfg" commit -m smoke >/tmp/git-guard-include.out 2>&1; then
    echo "expected Workcell git guard to reject inline include.path override" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-include.out
  if git -c includeIf.onbranch:main.path="$EXEC_TMP/git-bypass.cfg" commit -m smoke >/tmp/git-guard-includeif.out 2>&1; then
    echo "expected Workcell git guard to reject inline includeIf override" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-includeif.out
  if git -c core.worktree=/tmp commit -m smoke >/tmp/git-guard-worktree.out 2>&1; then
    echo "expected Workcell git guard to reject inline core.worktree override" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-worktree.out || grep -q "Workcell blocked git control-plane override" /tmp/git-guard-worktree.out
  if GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=include.path GIT_CONFIG_VALUE_0="$EXEC_TMP/git-bypass.cfg" git commit -m smoke >/tmp/git-guard-env-include.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_CONFIG_* include.path override" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-env-include.out
  if GIT_CONFIG_PARAMETERS="core.worktree=/tmp" git status >/tmp/git-guard-env-parameters-worktree.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_CONFIG_PARAMETERS core.worktree override" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-env-parameters-worktree.out
  if GIT_DIR="$EXEC_TMP/git-guard/.git" git status >/tmp/git-guard-git-dir-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_DIR overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-git-dir-env.out
  if GIT_EXEC_PATH="$EXEC_TMP/git-guard" git status >/tmp/git-guard-git-exec-path-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_EXEC_PATH overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-git-exec-path-env.out
  if GIT_EXTERNAL_DIFF="$EXEC_TMP/git-guard" git status >/tmp/git-guard-git-external-diff-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_EXTERNAL_DIFF overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-git-external-diff-env.out
  if GIT_PAGER=cat git status >/tmp/git-guard-git-pager-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_PAGER overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-git-pager-env.out
  if PAGER=cat git status >/tmp/git-guard-pager-env.out 2>&1; then
    echo "expected Workcell git guard to reject PAGER overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-pager-env.out
  if GIT_EDITOR=cat git status >/tmp/git-guard-git-editor-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_EDITOR overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-git-editor-env.out
  if GIT_SEQUENCE_EDITOR=cat git status >/tmp/git-guard-git-sequence-editor-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_SEQUENCE_EDITOR overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-git-sequence-editor-env.out
  if VISUAL=cat git status >/tmp/git-guard-visual-env.out 2>&1; then
    echo "expected Workcell git guard to reject VISUAL overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-visual-env.out
  if GIT_CONFIG_GLOBAL="$EXEC_TMP/git-bypass.cfg" git status >/tmp/git-guard-git-config-global-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_CONFIG_GLOBAL overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-git-config-global-env.out
  if git --exec-path="$EXEC_TMP/git-guard" status >/tmp/git-guard-exec-path-override.out 2>&1; then
    echo "expected Workcell git guard to reject --exec-path overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-exec-path-override.out
  if git --git-dir="$EXEC_TMP/git-guard/.git" --work-tree="$EXEC_TMP/git-guard" status >/tmp/git-guard-path-override.out 2>&1; then
    echo "expected Workcell git guard to reject git-dir/work-tree overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-path-override.out
  git config alias.ci "commit -n"
  if git ci -m smoke >/tmp/git-guard-alias.out 2>&1; then
    echo "expected Workcell git guard to reject alias-expanded git commit -n" >&2
    exit 1
  fi
  grep -q "Workcell blocked git alias bypass" /tmp/git-guard-alias.out
  git config --unset alias.ci
  git config alias.c "commit -n"
  git config alias.ci c
  if git ci -m smoke >/tmp/git-guard-alias-chain.out 2>&1; then
    echo "expected Workcell git guard to reject recursively expanded git commit -n aliases" >&2
    exit 1
  fi
  grep -q "Workcell blocked git alias bypass" /tmp/git-guard-alias-chain.out
  git config alias.ctab "$(printf "commit\\t-n")"
  if git ctab -m smoke >/tmp/git-guard-alias-tab.out 2>&1; then
    echo "expected Workcell git guard to reject tab-separated alias-expanded git commit -n" >&2
    exit 1
  fi
  grep -q "Workcell blocked git alias bypass" /tmp/git-guard-alias-tab.out
  git config alias.cquoted "commit \"-n\""
  if git cquoted -m smoke >/tmp/git-guard-alias-quoted.out 2>&1; then
    echo "expected Workcell git guard to reject quoted alias-expanded git commit -n" >&2
    exit 1
  fi
  grep -q "Workcell blocked git alias bypass" /tmp/git-guard-alias-quoted.out
  git config alias.execpath "--exec-path=$EXEC_TMP/git-guard status"
  if git execpath >/tmp/git-guard-alias-exec-path.out 2>&1; then
    echo "expected Workcell git guard to reject alias-expanded --exec-path" >&2
    exit 1
  fi
  grep -q "Workcell blocked git alias bypass" /tmp/git-guard-alias-exec-path.out
  git config alias.cshell "!git commit \\\\-n -m smoke"
  if git cshell >/tmp/git-guard-alias-shell-escaped.out 2>&1; then
    echo "expected Workcell git guard to reject shell alias git commit \\\\-n bypass" >&2
    exit 1
  fi
  grep -q "Workcell blocked git alias bypass" /tmp/git-guard-alias-shell-escaped.out
  git config alias.csubst "!git commit \$(printf -- -)\$(printf n) -m smoke"
  if git csubst >/tmp/git-guard-alias-shell-substitution.out 2>&1; then
    echo "expected Workcell git guard to reject shell alias substitution bypass" >&2
    exit 1
  fi
  grep -q "Workcell blocked git alias bypass" /tmp/git-guard-alias-shell-substitution.out
  set -- $(ldd /usr/local/libexec/workcell/real/git | grep -E "ld-linux|ld-musl" | head -n1)
  LOADER="$1"
  test -n "$LOADER"
  if "$LOADER" /usr/local/libexec/workcell/real/git commit --no-verify -m smoke >/tmp/git-guard-loader.out 2>&1; then
    echo "expected Workcell git guard to reject direct loader invocation" >&2
    exit 1
  fi
  grep -q "Workcell blocked git hook bypass" /tmp/git-guard-loader.out
  cp /usr/local/libexec/workcell/real/git "$EXEC_TMP/workcell-git-real-copy"
  chmod 0700 "$EXEC_TMP/workcell-git-real-copy"
  if "$EXEC_TMP/workcell-git-real-copy" status >/tmp/git-guard-real-copy.out 2>&1; then
    echo "expected renamed copy of the real git payload to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/git-guard-real-copy.out
  if "$LOADER" "$EXEC_TMP/workcell-git-real-copy" status >/tmp/git-guard-real-copy-loader.out 2>&1; then
    echo "expected loader invocation of a renamed real git copy to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/git-guard-real-copy-loader.out
  mkdir -p "$EXEC_TMP/git-global-guard" && cd "$EXEC_TMP/git-global-guard"
  git init -q
  git config user.name "Workcell Smoke"
  git config user.email "workcell-smoke@example.com"
  mkdir -p .git/hooks
  cat >.git/hooks/pre-commit <<'"'"'EOF'"'"'
#!/usr/bin/env sh
echo "hook ran" >&2
exit 1
EOF
  chmod +x .git/hooks/pre-commit
  touch smoke.txt
  git add smoke.txt
  printf "[core]\n  hooksPath = /dev/null\n" >"$HOME/.gitconfig"
  if git commit -m smoke >/tmp/git-guard-global-config.out 2>&1; then
    echo "expected Workcell git wrapper to ignore writable global git config" >&2
    exit 1
  fi
  grep -Eq "hook ran|pre-commit" /tmp/git-guard-global-config.out
  mkdir -p "$HOME/.config/git"
  printf "[core]\n  hooksPath = /dev/null\n" >"$HOME/.config/git/config"
  if git commit -m smoke >/tmp/git-guard-xdg-config.out 2>&1; then
    echo "expected Workcell git wrapper to ignore writable XDG git config" >&2
    exit 1
  fi
  grep -Eq "hook ran|pre-commit" /tmp/git-guard-xdg-config.out
'

# shellcheck disable=SC2016
run_container claude bash -lc '
  claude --version 2>&1 | grep -q "Claude Code"
  test -f "$HOME/.claude/settings.json"
  test -L "$HOME/.claude/settings.json"
  test "$(readlink "$HOME/.claude/settings.json")" = "/opt/workcell/adapters/claude/.claude/settings.json"
  test -f "$HOME/.mcp.json"
  test -L "$HOME/.mcp.json"
  test -f /etc/claude-code/managed-settings.json
  jq -r ".disableBypassPermissionsMode" /etc/claude-code/managed-settings.json | grep -q "^disable$"
  jq -r ".hooks.PreToolUse[0].hooks[0].command" "$HOME/.claude/settings.json" | grep -q "guard-bash.sh"
  if /usr/local/libexec/workcell/real/node /opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli.js --version >/tmp/node-real-payload.out 2>&1; then
    echo "expected direct real node payload execution to fail" >&2
    exit 1
  fi
  if claude --dangerously-skip-permissions >/tmp/claude-nested-danger.out 2>&1; then
    echo "expected nested Claude invocation to reject unsafe overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Claude override" /tmp/claude-nested-danger.out
  if claude --add-dir=/state --version >/tmp/claude-nested-add-dir.out 2>&1; then
    echo "expected nested Claude invocation to reject add-dir overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Claude override" /tmp/claude-nested-add-dir.out
  if WORKCELL_MODE=breakglass claude --dangerously-skip-permissions >/tmp/claude-nested-breakglass.out 2>&1; then
    echo "expected nested Claude invocation to ignore caller-supplied breakglass env" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Claude override" /tmp/claude-nested-breakglass.out
  rm -f "$HOME/.claude/settings.json"
  printf "{\n  \"disableBypassPermissionsMode\": \"allow\"\n}\n" >"$HOME/.claude/settings.json"
  claude --version >/dev/null 2>&1
  test -L "$HOME/.claude/settings.json"
  test "$(readlink "$HOME/.claude/settings.json")" = "/opt/workcell/adapters/claude/.claude/settings.json"
  printf "%s" "{\"tool_input\":{\"command\":\"bash -lc '\''git commit -n -m smoke'\''\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-git.out 2>&1 && {
      echo "expected Claude guard hook to reject nested-shell git bypass" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-git.out
  printf "%s" "{\"tool_input\":{\"command\":\"claude --dangerously-skip-permissions\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-provider.out 2>&1 && {
      echo "expected Claude guard hook to reject nested Claude unsafe overrides" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-provider.out
  printf "%s" "{\"tool_input\":{\"command\":\"/usr/local/libexec/workcell/core/claude --dangerously-skip-permissions\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-provider-path.out 2>&1 && {
      echo "expected Claude guard hook to reject path-qualified nested provider launches" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-provider-path.out
  printf "%s" "{\"tool_input\":{\"command\":\"node /opt/workcell/providers/node_modules/@anthropic-ai/claude-code/cli.js --dangerously-skip-permissions\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-provider-script-path.out 2>&1 && {
      echo "expected Claude guard hook to reject nested provider script launches" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-provider-script-path.out
  printf "%b" "{\"tool_input\":{\"command\":\"c\\x24\\x27laude\\x27 --dangerously-skip-permissions\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-expansion.out 2>&1 && {
      echo "expected Claude guard hook to reject advanced shell expansion syntax" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-expansion.out
  printf "%s" "{\"tool_input\":{\"command\":\"c\\\\laude --dangerously-skip-permissions\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-backslash.out 2>&1 && {
      echo "expected Claude guard hook to reject backslash-obfuscated provider names" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-backslash.out
  jq -n --arg cmd "c'\''laude'\'' --dangerously-skip-permissions" "{\"tool_input\":{\"command\":\$cmd}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-quote-split.out 2>&1 && {
      echo "expected Claude guard hook to reject quote-split provider names" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-quote-split.out
  touch ./claude
  printf "%s" "{\"tool_input\":{\"command\":\"c* --dangerously-skip-permissions\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-glob.out 2>&1 && {
      echo "expected Claude guard hook to reject glob-expanded command names" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-glob.out
  rm -f ./claude
  cat >/tmp/claude-hook-positional.json <<'"'"'EOF'"'"'
{"tool_input":{"command":"set -- cl aude; \"$1$2\" --dangerously-skip-permissions"}}
EOF
  /opt/workcell/adapters/claude/hooks/guard-bash.sh </tmp/claude-hook-positional.json >/tmp/claude-hook-positional.out 2>&1 && {
      echo "expected Claude guard hook to reject positional-parameter provider reassembly" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-positional.out
  jq -n --arg cmd "printf foo\\ bar" "{\"tool_input\":{\"command\":\$cmd}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-safe-escape.out 2>&1 || {
      echo "expected Claude guard hook to allow ordinary shell escapes" >&2
      cat /tmp/claude-hook-safe-escape.out >&2
      exit 1
    }
  printf "%s" "{\"tool_input\":{\"command\":\"bash ./nested-script.sh\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-shell-script.out 2>&1 && {
      echo "expected Claude guard hook to reject nested shell script execution" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-shell-script.out
  printf "%s" "{\"tool_input\":{\"command\":\"source ./nested-script.sh\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-source-script.out 2>&1 && {
      echo "expected Claude guard hook to reject sourced shell scripts" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-source-script.out
  printf "%s" "{\"tool_input\":{\"command\":\"find . -type f | head -n 1\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-dot-arg.out 2>&1 || {
      echo "expected Claude guard hook to allow dot path arguments" >&2
      cat /tmp/claude-hook-dot-arg.out >&2
      exit 1
    }
  printf "%s" "{\"tool_input\":{\"command\":\"touch nested/.claude/settings.json\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-control-plane.out 2>&1 && {
      echo "expected Claude guard hook to reject control-plane path writes" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-control-plane.out
  printf "%s" "{\"tool_input\":{\"command\":\"cat ~/.claude/settings.json\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-home-control-plane.out 2>&1 && {
      echo "expected Claude guard hook to reject home control-plane access" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-home-control-plane.out
'

# shellcheck disable=SC2016
run_container gemini bash -lc '
  out="$(gemini --version 2>&1)"
  echo "$out"
  if echo "$out" | grep -q "Failed to save project registry"; then
    echo "unexpected Gemini project registry warning" >&2
    exit 1
  fi
  if echo "$out" | grep -q "There was an error saving your latest settings changes"; then
    echo "unexpected Gemini settings write warning" >&2
    exit 1
  fi
  echo "$out" | grep -Eq "([0-9]+\\.){2}[0-9]+"
  test -f "$HOME/.gemini/settings.json"
  test -f "$HOME/.gemini/GEMINI.md"
  test -f "$HOME/.gemini/projects.json"
  if gemini --yolo >/tmp/gemini-nested-yolo.out 2>&1; then
    echo "expected nested Gemini invocation to reject unsafe overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Gemini override" /tmp/gemini-nested-yolo.out
  if gemini --add-dir=/state --version >/tmp/gemini-nested-add-dir.out 2>&1; then
    echo "expected nested Gemini invocation to reject add-dir overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Gemini override" /tmp/gemini-nested-add-dir.out
  if WORKCELL_MODE=breakglass gemini --yolo >/tmp/gemini-nested-breakglass.out 2>&1; then
    echo "expected nested Gemini invocation to ignore caller-supplied breakglass env" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Gemini override" /tmp/gemini-nested-breakglass.out
  NODE_EXTRA_CA_CERTS=/workspace/does-not-exist.pem gemini --version >/tmp/gemini-extra-ca.out 2>&1
  if grep -qi "extra cert" /tmp/gemini-extra-ca.out; then
    echo "expected provider wrapper to scrub NODE_EXTRA_CA_CERTS" >&2
    cat /tmp/gemini-extra-ca.out >&2
    exit 1
  fi
  rm -rf /workspace/.gemini
  HOME=/workspace gemini --version >/dev/null 2>&1
  test ! -e /workspace/.gemini/settings.json
  test ! -e /workspace/.gemini/projects.json
  rm -f "$HOME/.gemini/settings.json"
  printf "{\n  \"general\": {\"disableAutoUpdate\": false}\n}\n" >"$HOME/.gemini/settings.json"
  gemini --version >/dev/null 2>&1
  jq -r ".general.enableAutoUpdate" "$HOME/.gemini/settings.json" | grep -q "^false$"
  jq -r ".general.enableAutoUpdateNotification" "$HOME/.gemini/settings.json" | grep -q "^false$"
'

echo "Workcell container smoke passed."
