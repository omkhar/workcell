#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=tests/scenarios/shared/test-docker-desktop-launch-smoke.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-docker-desktop-launch-scenario.XXXXXX")"

REAL_HOME="$(cd "${HOME}" && pwd -P)"
AUTH_ROOT="${TMP_DIR}/auth"
WORKSPACE="${TMP_DIR}/workspace"
TARGET_STATE_DIR=""
PROFILE_NAME=""

cleanup() {
  local status=$?
  local file=""
  local builder_name=""
  local builder_container_prefix=""
  local builder_resource=""
  local -a builder_names=()
  local -a builder_containers=()
  local -a builder_volumes=()

  if [[ "${status}" -ne 0 ]]; then
    for file in \
      inspect.stdout inspect.stderr \
      doctor.stdout doctor.stderr \
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
  if [[ -n "${PROFILE_NAME}" ]]; then
    builder_name="workcell-runtime-${PROFILE_NAME}-"
    builder_container_prefix="buildx_buildkit_${builder_name}"
    while IFS= read -r builder_resource; do builder_names+=("${builder_resource}"); done < <(docker --context desktop-linux buildx ls --format '{{.Name}}' 2>/dev/null | grep -E "^${builder_name}[a-f0-9]{32}$" || true)
    while IFS= read -r builder_resource; do builder_containers+=("${builder_resource}"); done < <(docker --context desktop-linux ps -a --format '{{.Names}}' 2>/dev/null | grep -E "^${builder_container_prefix}[a-f0-9]{32}0?$" || true)
    while IFS= read -r builder_resource; do builder_volumes+=("${builder_resource}"); done < <(docker --context desktop-linux volume ls --format '{{.Name}}' 2>/dev/null | grep -E "^${builder_container_prefix}[a-f0-9]{32}0?_state$" || true)
    for ((cleanup_attempt = 1; cleanup_attempt <= 5; cleanup_attempt++)); do
      for builder_resource in "${builder_names[@]}"; do
        docker --context desktop-linux buildx rm --force "${builder_resource}" >/dev/null 2>&1 || true
      done
      for builder_resource in "${builder_containers[@]}"; do
        docker --context desktop-linux rm -f "${builder_resource}" >/dev/null 2>&1 || true
      done
      for builder_resource in "${builder_volumes[@]}"; do
        docker --context desktop-linux volume rm "${builder_resource}" >/dev/null 2>&1 || true
      done
      [[ "${cleanup_attempt}" -eq 5 ]] || sleep 1
    done
  fi
  if [[ -n "${TARGET_STATE_DIR}" ]] && [[ "${TARGET_STATE_DIR}" == "${REAL_HOME}/.local/state/workcell/targets/local_compat/docker-desktop/"* ]]; then
    chmod -R u+w "${TARGET_STATE_DIR}" 2>/dev/null || true
    rm -rf "${TARGET_STATE_DIR}"
  fi
  chmod -R u+w "${TMP_DIR}" 2>/dev/null || true
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

inspect_target_state() {
  "${ROOT_DIR}/scripts/workcell" \
    --target docker-desktop \
    --agent codex \
    --inspect \
    --no-default-injection-policy \
    --workspace "${WORKSPACE}" \
    >"${TMP_DIR}/inspect.stdout" 2>"${TMP_DIR}/inspect.stderr"
}

doctor_target() {
  "${ROOT_DIR}/scripts/workcell" \
    --target docker-desktop \
    --agent codex \
    --doctor \
    --no-default-injection-policy \
    --workspace "${WORKSPACE}" \
    >"${TMP_DIR}/doctor.stdout" 2>"${TMP_DIR}/doctor.stderr"
}

prepare_runtime() {
  "${ROOT_DIR}/scripts/workcell" \
    --target docker-desktop \
    --prepare-only \
    --agent codex \
    --workspace "${WORKSPACE}" \
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
    --target docker-desktop \
    --agent "${agent}" \
    --workspace "${WORKSPACE}" \
    --injection-policy "${AUTH_ROOT}/policy.toml" \
    -- "${agent}" --version \
    >"${TMP_DIR}/${agent}.stdout" 2>"${TMP_DIR}/${agent}.stderr"
  status=$?
  set -e
  return "${status}"
}

inspect_target_state
PROFILE_NAME="$(sed -n 's/^profile=//p' "${TMP_DIR}/inspect.stdout")"
[[ -n "${PROFILE_NAME}" ]]
TARGET_STATE_DIR="$(sed -n 's/^target_state_dir=//p' "${TMP_DIR}/inspect.stdout")"
[[ -n "${TARGET_STATE_DIR}" ]]
[[ "${TARGET_STATE_DIR}" == "${REAL_HOME}/.local/state/workcell/targets/local_compat/docker-desktop/"* ]]
grep -q '^target_kind=local_compat$' "${TMP_DIR}/inspect.stdout"
grep -q '^target_provider=docker-desktop$' "${TMP_DIR}/inspect.stdout"
grep -q '^target_id=desktop-linux$' "${TMP_DIR}/inspect.stdout"
grep -q '^target_assurance_class=compat$' "${TMP_DIR}/inspect.stdout"
grep -q '^support_matrix_status=supported$' "${TMP_DIR}/inspect.stdout"
grep -q '^support_matrix_launch=allowed$' "${TMP_DIR}/inspect.stdout"
grep -q '^support_matrix_evidence=certification-only$' "${TMP_DIR}/inspect.stdout"

doctor_target
DOCTOR_RECOMMENDED_NEXT="$(sed -n 's/^doctor_recommended_next=//p' "${TMP_DIR}/doctor.stdout")"
grep -q '^doctor_missing_host_tools=none$' "${TMP_DIR}/doctor.stdout"
[[ "${DOCTOR_RECOMMENDED_NEXT}" == *"workcell"* ]]
[[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--prepare"* ]]
[[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--agent codex"* ]]
[[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--target docker-desktop"* ]]
[[ "${DOCTOR_RECOMMENDED_NEXT}" == *"--workspace "* ]]

prepare_runtime
grep -q '^target_kind=local_compat target_provider=docker-desktop target_id=desktop-linux target_assurance_class=compat runtime_api=docker workspace_transport=workspace-mount$' "${TMP_DIR}/prepare.stderr"
grep -q 'Prepared runtime image recorded for profile ' "${TMP_DIR}/prepare.stderr"
[[ -f "${TARGET_STATE_DIR}/workcell.image-ready" ]]
[[ -f "${TARGET_STATE_DIR}/workcell.managed" ]]
[[ ! -e "${TARGET_STATE_DIR}/workcell.builder-owned" ]]
BUILDER_PREFIX="buildx_buildkit_workcell-runtime-${PROFILE_NAME}"
{
  docker --context desktop-linux ps -a --format '{{.Names}}'
  docker --context desktop-linux volume ls --format '{{.Name}}'
} >"${TMP_DIR}/builder-inventory"
if grep -Eq "^${BUILDER_PREFIX}-[a-f0-9]{32}(0|0?_state)?$" "${TMP_DIR}/builder-inventory"; then
  echo "Runtime-image builder resources remain after prepare-only." >&2
  exit 1
fi

for agent in codex claude copilot gemini; do
  expected_version="$(expected_runtime_version "${agent}")"
  run_agent_version_smoke "${agent}"
  grep -q '^target_kind=local_compat target_provider=docker-desktop target_id=desktop-linux target_assurance_class=compat runtime_api=docker workspace_transport=workspace-mount$' "${TMP_DIR}/${agent}.stderr"
  grep -q '^execution_path=managed-tier1 audit_log=' "${TMP_DIR}/${agent}.stderr"
  cat "${TMP_DIR}/${agent}.stdout" "${TMP_DIR}/${agent}.stderr" >"${TMP_DIR}/${agent}.combined"
  grep -q '[^[:space:]]' "${TMP_DIR}/${agent}.combined"
  grep -Fq "${expected_version}" "${TMP_DIR}/${agent}.combined"
done

echo "Docker Desktop provider launch smoke scenario passed"
