#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/shared/test-agent-launch-smoke.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-agent-launch-scenario.XXXXXX")"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
# workcell_colima: every colima run here sets the Colima cache root as the
# launcher does, so a restart keeps the staging root out of lima.yaml.
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/launcher/host-detect.sh"

REAL_HOME="$(resolve_workcell_real_home)"
PROFILE="wcl-als-$$"
AUTH_ROOT="${TMP_DIR}/auth"
WORKSPACE="${TMP_DIR}/workspace"

cleanup() {
  local status=$?
  local file=""

  if [[ "${status}" -ne 0 ]]; then
    for file in \
      prepare.stdout prepare.stderr \
      codex.stdout codex.stderr codex.combined \
      claude.stdout claude.stderr claude.combined \
      copilot.stdout copilot.stderr copilot.combined \
      gemini.stdout gemini.stderr gemini.combined; do
      [[ -f "${TMP_DIR}/${file}" ]] || continue
      printf -- '--- %s ---\n' "${file}" >&2
      cat "${TMP_DIR}/${file}" >&2
    done
  fi
  if command -v colima >/dev/null 2>&1; then
    workcell_colima colima "${REAL_HOME}/.colima" stop --profile "${PROFILE}" >/dev/null 2>&1 || true
    workcell_colima colima "${REAL_HOME}/.colima" delete --profile "${PROFILE}" --force >/dev/null 2>&1 || true
  fi
  rm -rf "${REAL_HOME}/.colima/${PROFILE}"
  rm -rf "${REAL_HOME}/.colima/_lima/colima-${PROFILE}"
  rm -rf "${REAL_HOME}/.colima/_lima/_disks/colima-${PROFILE}"
  rm -f "${REAL_HOME}/.colima/_store/colima-${PROFILE}.json"
  rm -rf "${TMP_DIR}"
  exit "${status}"
}
trap cleanup EXIT

mkdir -p "${AUTH_ROOT}" "${WORKSPACE}"
git -C "${WORKSPACE}" init -q
printf 'scenario workspace\n' >"${WORKSPACE}/README.md"

printf '{}\n' >"${AUTH_ROOT}/auth.json"
chmod 0600 "${AUTH_ROOT}/auth.json"
printf '{"token":"claude-auth"}\n' >"${AUTH_ROOT}/claude-auth.json"
chmod 0600 "${AUTH_ROOT}/claude-auth.json"
printf 'claude-key\n' >"${AUTH_ROOT}/claude-api-key.txt"
chmod 0600 "${AUTH_ROOT}/claude-api-key.txt"
printf 'copilot-token\n' >"${AUTH_ROOT}/copilot-github-token.txt"
chmod 0600 "${AUTH_ROOT}/copilot-github-token.txt"
cat >"${AUTH_ROOT}/gemini.env" <<'EOF'
GOOGLE_GENAI_USE_VERTEXAI=true
GOOGLE_API_KEY=verify-google-key
EOF
chmod 0600 "${AUTH_ROOT}/gemini.env"
# The launches below stage API keys from files, which a proxy session
# refuses, so they pass --egress-proxy=off. The egress-proxy smoke covers the
# proxy path.
cat >"${AUTH_ROOT}/policy.toml" <<'EOF'
version = 1

[credentials]
codex_auth = "auth.json"
claude_auth = "claude-auth.json"
claude_api_key = "claude-api-key.txt"
gemini_env = "gemini.env"

[credentials.copilot_github_token]
source = "copilot-github-token.txt"
EOF

prepare_runtime() {
  "${ROOT_DIR}/scripts/workcell" \
    --prepare-only \
    --egress-proxy=off \
    --agent codex \
    --workspace "${WORKSPACE}" \
    --colima-profile "${PROFILE}" \
    --injection-policy "${AUTH_ROOT}/policy.toml" \
    >"${TMP_DIR}/prepare.stdout" 2>"${TMP_DIR}/prepare.stderr"
}

expected_runtime_version() {
  local agent="$1"

  case "${agent}" in
    codex)
      sed -n 's/^ARG CODEX_VERSION=//p' "${ROOT_DIR}/runtime/container/Dockerfile"
      ;;
    claude)
      sed -n 's/^ARG CLAUDE_VERSION=//p' "${ROOT_DIR}/runtime/container/Dockerfile"
      ;;
    copilot)
      sed -n 's/^ARG COPILOT_VERSION=//p' "${ROOT_DIR}/runtime/container/Dockerfile"
      ;;
    gemini)
      jq -r '.dependencies["@google/gemini-cli"]' "${ROOT_DIR}/runtime/container/providers/package.json"
      ;;
    *)
      return 1
      ;;
  esac
}

run_agent_version_smoke() {
  local agent="$1"
  local status=0

  set +e
  perl -e 'alarm shift @ARGV; exec @ARGV' 120 \
    "${ROOT_DIR}/scripts/workcell" \
    --egress-proxy=off \
    --agent "${agent}" \
    --workspace "${WORKSPACE}" \
    --colima-profile "${PROFILE}" \
    --injection-policy "${AUTH_ROOT}/policy.toml" \
    -- "${agent}" --version \
    >"${TMP_DIR}/${agent}.stdout" 2>"${TMP_DIR}/${agent}.stderr"
  status=$?
  set -e
  return "${status}"
}

prepare_runtime
grep -q "^profile=${PROFILE} mode=strict agent=codex " "${TMP_DIR}/prepare.stderr"
grep -q '^target_kind=local_vm target_provider=colima target_id='"${PROFILE}"' target_assurance_class=strict runtime_api=docker workspace_transport=workspace-mount$' "${TMP_DIR}/prepare.stderr"
grep -q "Prepared runtime image recorded for profile ${PROFILE}. No session launched because --prepare-only was requested." "${TMP_DIR}/prepare.stderr"
"${ROOT_DIR}/scripts/workcell" \
  --inspect \
  --agent codex \
  --no-default-injection-policy \
  --workspace "${WORKSPACE}" \
  --colima-profile "${PROFILE}" >"${TMP_DIR}/inspect.stdout"
TARGET_STATE_DIR="$(sed -n 's/^target_state_dir=//p' "${TMP_DIR}/inspect.stdout")"
[[ -n "${TARGET_STATE_DIR}" ]]
[[ ! -e "${TARGET_STATE_DIR}/workcell.builder-owned" ]]
BUILDER_PREFIX="buildx_buildkit_workcell-runtime-${PROFILE}"
workcell_colima colima "${REAL_HOME}/.colima" start --profile "${PROFILE}" >/dev/null
{
  env DOCKER_HOST="unix://${REAL_HOME}/.colima/${PROFILE}/docker.sock" docker ps -a --format '{{.Names}}'
  env DOCKER_HOST="unix://${REAL_HOME}/.colima/${PROFILE}/docker.sock" docker volume ls --format '{{.Name}}'
} >"${TMP_DIR}/builder-inventory"
if grep -Eq "^${BUILDER_PREFIX}-[a-f0-9]{32}(0|0?_state)?$" "${TMP_DIR}/builder-inventory"; then
  echo "Runtime-image builder resources remain after prepare-only." >&2
  exit 1
fi

for agent in codex claude copilot gemini; do
  expected_version="$(expected_runtime_version "${agent}")"
  run_agent_version_smoke "${agent}"
  grep -q "^profile=${PROFILE} mode=strict agent=${agent} " "${TMP_DIR}/${agent}.stderr"
  grep -q '^target_kind=local_vm target_provider=colima target_id='"${PROFILE}"' target_assurance_class=strict runtime_api=docker workspace_transport=workspace-mount$' "${TMP_DIR}/${agent}.stderr"
  grep -q '^execution_path=managed-tier1 audit_log=' "${TMP_DIR}/${agent}.stderr"
  cat "${TMP_DIR}/${agent}.stdout" "${TMP_DIR}/${agent}.stderr" >"${TMP_DIR}/${agent}.combined"
  grep -q '[^[:space:]]' "${TMP_DIR}/${agent}.combined"
  grep -Fq "${expected_version}" "${TMP_DIR}/${agent}.combined"
done

echo "Provider launch smoke scenario passed"
