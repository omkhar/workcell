#!/usr/bin/env -S -i PATH=/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin BASH_ENV= ENV= /bin/bash
# shellcheck shell=bash
set -euo pipefail

readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
export PATH="${TRUSTED_HOST_PATH}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOST_GATE_SCRIPTS=(
  "${ROOT_DIR}/scripts/check-pinned-inputs.sh"
  "${ROOT_DIR}/scripts/container-smoke.sh"
  "${ROOT_DIR}/scripts/generate-build-input-manifest.sh"
  "${ROOT_DIR}/scripts/generate-builder-environment-manifest.sh"
  "${ROOT_DIR}/scripts/generate-release-checksums.sh"
  "${ROOT_DIR}/scripts/publish-github-release.sh"
  "${ROOT_DIR}/scripts/verify-build-input-manifest.sh"
  "${ROOT_DIR}/scripts/verify-release-bundle.sh"
  "${ROOT_DIR}/scripts/verify-reproducible-build.sh"
  "${ROOT_DIR}/scripts/verify-upstream-codex-release.sh"
)
if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "verify-invariants-entrypoint-ok"
  exit 0
fi

REAL_HOME="$(
  if [[ -x /usr/bin/python3 ]]; then
    /usr/bin/python3 - <<'PY'
import os
import pwd
print(pwd.getpwuid(os.getuid()).pw_dir)
PY
  else
    python3 - <<'PY'
import os
import pwd
print(pwd.getpwuid(os.getuid()).pw_dir)
PY
  fi
)"
CODEX_VERIFY_HOME="$(mktemp -d)"
BARRIER_VERIFY_ROOT="$(mktemp -d)"
BROWSER_PROFILE_FIXTURE=""
COLIMA_PROFILE_FIXTURE=""

cleanup() {
  rm -rf "${CODEX_VERIFY_HOME}"
  rm -rf "${BARRIER_VERIFY_ROOT}"
  if [[ -n "${BROWSER_PROFILE_FIXTURE}" ]] && [[ -d "${BROWSER_PROFILE_FIXTURE}" ]]; then
    rmdir "${BROWSER_PROFILE_FIXTURE}" 2>/dev/null || true
  fi
  if [[ -n "${COLIMA_PROFILE_FIXTURE}" ]] && [[ -d "${COLIMA_PROFILE_FIXTURE}" ]]; then
    rm -rf "${COLIMA_PROFILE_FIXTURE}"
  fi
}

trap cleanup EXIT

check_file() {
  [[ -f "$1" ]] || {
    echo "Missing required file: $1" >&2
    exit 1
  }
}

rg() {
  if builtin type -P rg >/dev/null 2>&1; then
    command rg "$@"
    return
  fi

  if [[ "${1:-}" == "-q" ]] && [[ "$#" -eq 3 ]]; then
    grep -Eq -- "$2" "$3"
    return
  fi

  if [[ "${1:-}" == "-q" ]] && [[ "${2:-}" == "--" ]] && [[ "$#" -eq 4 ]]; then
    grep -Eq -- "$3" "$4"
    return
  fi

  echo "rg fallback only supports 'rg -q PATTERN FILE' or 'rg -q -- PATTERN FILE'" >&2
  return 127
}

for file in \
  "${ROOT_DIR}/adapters/codex/.codex/config.toml" \
  "${ROOT_DIR}/adapters/claude/.claude/settings.json" \
  "${ROOT_DIR}/adapters/gemini/.gemini/settings.json" \
  "${ROOT_DIR}/runtime/container/Dockerfile" \
  "${ROOT_DIR}/runtime/container/bin/git" \
  "${ROOT_DIR}/runtime/container/rust/Cargo.toml" \
  "${ROOT_DIR}/runtime/container/rust/src/lib.rs" \
  "${ROOT_DIR}/runtime/container/rust/src/bin/workcell-git-launcher.rs" \
  "${ROOT_DIR}/runtime/container/rust/src/bin/workcell-launcher.rs" \
  "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh" \
  "${ROOT_DIR}/scripts/workcell" \
  "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; do
  check_file "${file}"
done

if rg -q 'WORKCELL_TEST_HARNESS|WORKCELL_(GIT|COLIMA|DOCKER|RUBY)_BIN=' "${ROOT_DIR}/scripts/workcell"; then
  echo "Unexpected test-harness host tool override support remains in scripts/workcell" >&2
  exit 1
fi

if rg -q 'YAML\.load_file' "${ROOT_DIR}/scripts/workcell"; then
  echo "scripts/workcell still uses unsafe YAML.load_file parsing for managed profile validation" >&2
  exit 1
fi

if ! rg -q 'COLIMA_STATE_ROOT=' "${ROOT_DIR}/scripts/workcell" || ! rg -q 'COLIMA_HOME="\$\{COLIMA_STATE_ROOT\}"' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to pin Colima state operations to one COLIMA_HOME root" >&2
  exit 1
fi

if ! rg -q 'REAL_HOME=' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to derive the real host home independently of caller HOME" >&2
  exit 1
fi

if ! head -n 1 "${ROOT_DIR}/scripts/workcell" | grep -q '^#!/usr/bin/env -S -i PATH=.* BASH_ENV= ENV= /bin/bash$'; then
  echo "Expected scripts/workcell to use env -S -i with an absolute /bin/bash and cleared host environment" >&2
  exit 1
fi

if ! rg -q 'scrub_host_process_env' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to scrub hostile host process environment before host tool lookup" >&2
  exit 1
fi

if ! rg -q 'unset PERL5OPT PERL5LIB PERLLIB PERL_MB_OPT PERL_MM_OPT' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to scrub hostile Perl environment before host tool lookup" >&2
  exit 1
fi

if ! rg -q 'DYLD_\*' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to scrub DYLD_* variables before host tool lookup" >&2
  exit 1
fi

if rg -q 'shasum -a 256' "${ROOT_DIR}/scripts/workcell"; then
  echo "scripts/workcell still uses Perl-backed shasum for profile hashing" >&2
  exit 1
fi

if ! rg -q 'unset DOCKER_CONTEXT' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to scrub caller Docker context overrides before binding the managed daemon" >&2
  exit 1
fi

if ! rg -q 'unset DOCKER_CLI_PLUGIN_EXTRA_DIRS' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to scrub caller Docker CLI plugin overrides" >&2
  exit 1
fi

if ! rg -q 'source "\$\{ROOT_DIR\}/scripts/lib/trusted-docker-client\.sh"' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to source the trusted Docker client helper" >&2
  exit 1
fi

if ! rg -q 'setup_workcell_trusted_docker_client' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to seed a trusted Docker client state before host Docker use" >&2
  exit 1
fi

if rg -q 'DOCKER_CONFIG="\$\{REAL_HOME\}/\.docker"' "${ROOT_DIR}/scripts/workcell"; then
  echo "scripts/workcell still pins DOCKER_CONFIG to the real host home" >&2
  exit 1
fi

if ! rg -q 'buildx_cmd build' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to invoke buildx through the trusted absolute plugin path" >&2
  exit 1
fi

if ! rg -q -- '--self-docker-probe' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to expose a hidden self-docker probe for invariant testing" >&2
  exit 1
fi

if ! rg -q 'strict mode does not rebuild or cold-bootstrap the runtime image' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to reject explicit strict-mode image rebuild requests" >&2
  exit 1
fi

if ! rg -q 'run_clean_host_command "\$\{HOST_RUBY_BIN\}"' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to invoke host Ruby helpers under a scrubbed environment" >&2
  exit 1
fi

if ! rg -q 'env -i PATH="\$\{TRUSTED_HOST_PATH\}" "\$\{HOST_PYTHON3_BIN\}"' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to invoke the bootstrap Python helper under a scrubbed environment" >&2
  exit 1
fi

if rg -q 'set -- codex --cd ' "${ROOT_DIR}/runtime/container/entrypoint.sh"; then
  echo "runtime/container/entrypoint.sh still injects a blocked default Codex --cd override" >&2
  exit 1
fi

if rg -q 'command -v|type -P|which ' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "scripts/colima-egress-allowlist.sh still trusts PATH for executed host tools" >&2
  exit 1
fi

if ! rg -q 'REAL_HOME=' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "Expected scripts/colima-egress-allowlist.sh to derive the real host home independently of caller HOME" >&2
  exit 1
fi

if ! head -n 1 "${ROOT_DIR}/scripts/colima-egress-allowlist.sh" | grep -q '^#!/usr/bin/env -S -i PATH=.* BASH_ENV= ENV= /bin/bash$'; then
  echo "Expected scripts/colima-egress-allowlist.sh to use env -S -i with an absolute /bin/bash and cleared host environment" >&2
  exit 1
fi

if ! rg -q 'scrub_host_process_env' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "Expected scripts/colima-egress-allowlist.sh to scrub hostile host process environment before host tool lookup" >&2
  exit 1
fi

if ! rg -q 'unset PERL5OPT PERL5LIB PERLLIB PERL_MB_OPT PERL_MM_OPT' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "Expected scripts/colima-egress-allowlist.sh to scrub hostile Perl environment before host tool lookup" >&2
  exit 1
fi

if ! rg -q 'DYLD_\*' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "Expected scripts/colima-egress-allowlist.sh to scrub DYLD_* variables before host tool lookup" >&2
  exit 1
fi

if ! rg -q 'is_trusted_host_tool_path' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "Expected scripts/colima-egress-allowlist.sh to canonicalize and trust-check host tool paths" >&2
  exit 1
fi

if ! rg -q 'run_clean_host_command "\$\{PYTHON3_BIN\}"' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "Expected scripts/colima-egress-allowlist.sh to invoke host Python helpers under a scrubbed environment" >&2
  exit 1
fi

for script in "${HOST_GATE_SCRIPTS[@]}"; do
  if ! head -n 1 "${script}" | grep -q '^#!/bin/bash -p$'; then
    echo "Expected ${script} to use an absolute privileged Bash shebang before self-sanitizing its host entrypoint" >&2
    exit 1
  fi
  if ! rg -q 'WORKCELL_SANITIZED_ENTRYPOINT' "${script}"; then
    echo "Expected ${script} to self-sanitize its host entrypoint before running release or boundary checks" >&2
    exit 1
  fi
done

for script in \
  "${ROOT_DIR}/scripts/container-smoke.sh" \
  "${ROOT_DIR}/scripts/generate-builder-environment-manifest.sh" \
  "${ROOT_DIR}/scripts/verify-release-bundle.sh" \
  "${ROOT_DIR}/scripts/verify-reproducible-build.sh"; do
  if ! rg -q 'source "\$\{ROOT_DIR\}/scripts/lib/trusted-docker-client\.sh"' "${script}"; then
    echo "Expected ${script} to source the trusted Docker client helper" >&2
    exit 1
  fi
  if ! rg -q 'setup_workcell_trusted_docker_client' "${script}"; then
    echo "Expected ${script} to seed a trusted Docker client state before using Docker" >&2
    exit 1
  fi
  if ! rg -q 'HOME=/tmp' "${script}"; then
    echo "Expected ${script} to stop preserving caller HOME across its sanitized entrypoint re-exec" >&2
    exit 1
  fi
done

for script in \
  "${ROOT_DIR}/scripts/container-smoke.sh" \
  "${ROOT_DIR}/scripts/generate-builder-environment-manifest.sh" \
  "${ROOT_DIR}/scripts/verify-release-bundle.sh" \
  "${ROOT_DIR}/scripts/verify-reproducible-build.sh"; do
  if ! rg -q 'buildx_cmd ' "${script}"; then
    echo "Expected ${script} to invoke buildx through the trusted absolute plugin path" >&2
    exit 1
  fi
done

if ! rg -q 'COLIMA_HOME="\$\{colima_home\}"' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "Expected scripts/colima-egress-allowlist.sh to pin COLIMA_HOME while operating on Lima state" >&2
  exit 1
fi

if ! rg -q 'snapshot\.debian\.org:80' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell bootstrap endpoints to allow snapshot.debian.org" >&2
  exit 1
fi

if ! rg -q 'bootstrap_applied=%q bootstrap_endpoints=%q' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell audit records to include bootstrap network metadata" >&2
  exit 1
fi

if ! rg -q 'bootstrap_policy=allowlist endpoints=%s' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to announce temporary bootstrap network policy activation" >&2
  exit 1
fi

if ! rg -q 'net\.ipv6\.conf\.(all|default)\.disable_ipv6=1' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "Expected allowlist egress helper to disable IPv6 while the IPv4 allowlist is active" >&2
  exit 1
fi

HOST_BASH_ENV_PAYLOAD="${BARRIER_VERIFY_ROOT}/bashenv.sh"
HOST_BASH_ENV_MARKER="${BARRIER_VERIFY_ROOT}/bashenv-ran"
cat >"${HOST_BASH_ENV_PAYLOAD}" <<'EOF'
touch "${HOST_BASH_ENV_MARKER:?}"
EOF
if ! HOST_BASH_ENV_MARKER="${HOST_BASH_ENV_MARKER}" \
  BASH_ENV="${HOST_BASH_ENV_PAYLOAD}" \
  "${ROOT_DIR}/scripts/workcell" --help >/dev/null 2>&1; then
  echo "Expected scripts/workcell --help to succeed under a hostile BASH_ENV" >&2
  exit 1
fi
if [[ -e "${HOST_BASH_ENV_MARKER}" ]]; then
  echo "scripts/workcell executed hostile BASH_ENV content before launcher setup" >&2
  exit 1
fi

for script in "${HOST_GATE_SCRIPTS[@]}"; do
  gate_name="$(basename "${script}" .sh)"
  gate_marker="${BARRIER_VERIFY_ROOT}/${gate_name}-bashenv-ran"
  if ! HOST_BASH_ENV_MARKER="${gate_marker}" \
    BASH_ENV="${HOST_BASH_ENV_PAYLOAD}" \
    "${script}" --self-entrypoint-probe >/dev/null 2>&1; then
    echo "Expected ${script} self-entrypoint probe to succeed under a hostile BASH_ENV" >&2
    exit 1
  fi
  if [[ -e "${gate_marker}" ]]; then
    echo "${script} executed hostile BASH_ENV content before launcher setup" >&2
    exit 1
  fi
done

VERIFY_PATH_OVERRIDE_DIR="${BARRIER_VERIFY_ROOT}/verify-path-override-bin"
VERIFY_PATH_BASH_MARKER="${BARRIER_VERIFY_ROOT}/verify-path-bash-ran"
mkdir -p "${VERIFY_PATH_OVERRIDE_DIR}"
cat >"${VERIFY_PATH_OVERRIDE_DIR}/bash" <<'EOF'
#!/bin/sh
touch "${VERIFY_PATH_BASH_MARKER:?}"
exit 97
EOF
chmod 0755 "${VERIFY_PATH_OVERRIDE_DIR}/bash"
if ! VERIFY_PATH_BASH_MARKER="${VERIFY_PATH_BASH_MARKER}" \
  PATH="${VERIFY_PATH_OVERRIDE_DIR}:${PATH}" \
  "${ROOT_DIR}/scripts/verify-invariants.sh" --self-entrypoint-probe >/dev/null 2>&1; then
  echo "Expected scripts/verify-invariants.sh self-entrypoint probe to succeed under a hostile PATH" >&2
  exit 1
fi
if [[ -e "${VERIFY_PATH_BASH_MARKER}" ]]; then
  echo "scripts/verify-invariants.sh trusted caller PATH before launcher setup" >&2
  exit 1
fi

VERIFY_BASH_FUNC_MARKER="${BARRIER_VERIFY_ROOT}/verify-bash-func-ran"
if ! env \
  "BASH_FUNC_head%%=() { /usr/bin/touch '${VERIFY_BASH_FUNC_MARKER}'; }" \
  "${ROOT_DIR}/scripts/verify-invariants.sh" --self-entrypoint-probe >/dev/null 2>&1; then
  echo "Expected scripts/verify-invariants.sh self-entrypoint probe to succeed under a hostile imported Bash function" >&2
  exit 1
fi
if [[ -e "${VERIFY_BASH_FUNC_MARKER}" ]]; then
  echo "scripts/verify-invariants.sh imported hostile Bash functions before launcher setup" >&2
  exit 1
fi

for script in "${HOST_GATE_SCRIPTS[@]}"; do
  gate_name="$(basename "${script}" .sh)"
  gate_marker="${BARRIER_VERIFY_ROOT}/${gate_name}-bash-func-ran"
  if ! env \
    "BASH_FUNC_head%%=() { /usr/bin/touch '${gate_marker}'; }" \
    "${script}" --self-entrypoint-probe >/dev/null 2>&1; then
    echo "Expected ${script} self-entrypoint probe to succeed under a hostile imported Bash function" >&2
    exit 1
  fi
  if [[ -e "${gate_marker}" ]]; then
    echo "${script} imported hostile Bash functions before launcher setup" >&2
    exit 1
  fi
done

CONTAINER_SMOKE_BASH_ENV_MARKER="${BARRIER_VERIFY_ROOT}/container-smoke-bashenv-ran"
if ! HOST_BASH_ENV_MARKER="${CONTAINER_SMOKE_BASH_ENV_MARKER}" \
  BASH_ENV="${HOST_BASH_ENV_PAYLOAD}" \
  "${ROOT_DIR}/scripts/container-smoke.sh" --self-entrypoint-probe >/dev/null 2>&1; then
  echo "Expected scripts/container-smoke.sh self-entrypoint probe to succeed under a hostile BASH_ENV" >&2
  exit 1
fi
if [[ -e "${CONTAINER_SMOKE_BASH_ENV_MARKER}" ]]; then
  echo "scripts/container-smoke.sh executed hostile BASH_ENV content before launcher setup" >&2
  exit 1
fi

RELEASE_BUNDLE_BASH_ENV_MARKER="${BARRIER_VERIFY_ROOT}/verify-release-bundle-bashenv-ran"
if ! HOST_BASH_ENV_MARKER="${RELEASE_BUNDLE_BASH_ENV_MARKER}" \
  BASH_ENV="${HOST_BASH_ENV_PAYLOAD}" \
  "${ROOT_DIR}/scripts/verify-release-bundle.sh" --self-entrypoint-probe >/dev/null 2>&1; then
  echo "Expected scripts/verify-release-bundle.sh self-entrypoint probe to succeed under a hostile BASH_ENV" >&2
  exit 1
fi
if [[ -e "${RELEASE_BUNDLE_BASH_ENV_MARKER}" ]]; then
  echo "scripts/verify-release-bundle.sh executed hostile BASH_ENV content before launcher setup" >&2
  exit 1
fi

REPRO_BUILD_BASH_ENV_MARKER="${BARRIER_VERIFY_ROOT}/verify-reproducible-build-bashenv-ran"
if ! HOST_BASH_ENV_MARKER="${REPRO_BUILD_BASH_ENV_MARKER}" \
  BASH_ENV="${HOST_BASH_ENV_PAYLOAD}" \
  "${ROOT_DIR}/scripts/verify-reproducible-build.sh" --self-entrypoint-probe >/dev/null 2>&1; then
  echo "Expected scripts/verify-reproducible-build.sh self-entrypoint probe to succeed under a hostile BASH_ENV" >&2
  exit 1
fi
if [[ -e "${REPRO_BUILD_BASH_ENV_MARKER}" ]]; then
  echo "scripts/verify-reproducible-build.sh executed hostile BASH_ENV content before launcher setup" >&2
  exit 1
fi

CONTAINER_SMOKE_BASH_FUNC_MARKER="${BARRIER_VERIFY_ROOT}/container-smoke-bash-func-ran"
if ! env \
  "BASH_FUNC_head%%=() { /usr/bin/touch '${CONTAINER_SMOKE_BASH_FUNC_MARKER}'; }" \
  "${ROOT_DIR}/scripts/container-smoke.sh" --self-entrypoint-probe >/dev/null 2>&1; then
  echo "Expected scripts/container-smoke.sh self-entrypoint probe to succeed under a hostile imported Bash function" >&2
  exit 1
fi
if [[ -e "${CONTAINER_SMOKE_BASH_FUNC_MARKER}" ]]; then
  echo "scripts/container-smoke.sh imported hostile Bash functions before launcher setup" >&2
  exit 1
fi

RELEASE_BUNDLE_BASH_FUNC_MARKER="${BARRIER_VERIFY_ROOT}/verify-release-bundle-bash-func-ran"
if ! env \
  "BASH_FUNC_head%%=() { /usr/bin/touch '${RELEASE_BUNDLE_BASH_FUNC_MARKER}'; }" \
  "${ROOT_DIR}/scripts/verify-release-bundle.sh" --self-entrypoint-probe >/dev/null 2>&1; then
  echo "Expected scripts/verify-release-bundle.sh self-entrypoint probe to succeed under a hostile imported Bash function" >&2
  exit 1
fi
if [[ -e "${RELEASE_BUNDLE_BASH_FUNC_MARKER}" ]]; then
  echo "scripts/verify-release-bundle.sh imported hostile Bash functions before launcher setup" >&2
  exit 1
fi

REPRO_BUILD_BASH_FUNC_MARKER="${BARRIER_VERIFY_ROOT}/verify-reproducible-build-bash-func-ran"
if ! env \
  "BASH_FUNC_head%%=() { /usr/bin/touch '${REPRO_BUILD_BASH_FUNC_MARKER}'; }" \
  "${ROOT_DIR}/scripts/verify-reproducible-build.sh" --self-entrypoint-probe >/dev/null 2>&1; then
  echo "Expected scripts/verify-reproducible-build.sh self-entrypoint probe to succeed under a hostile imported Bash function" >&2
  exit 1
fi
if [[ -e "${REPRO_BUILD_BASH_FUNC_MARKER}" ]]; then
  echo "scripts/verify-reproducible-build.sh imported hostile Bash functions before launcher setup" >&2
  exit 1
fi

HOST_BASH_FUNC_MARKER="${BARRIER_VERIFY_ROOT}/bash-func-ran"
if ! env \
  "BASH_FUNC_compgen%%=() { /usr/bin/touch '${HOST_BASH_FUNC_MARKER}'; }" \
  "${ROOT_DIR}/scripts/workcell" --help >/dev/null 2>&1; then
  echo "Expected scripts/workcell --help to succeed under a hostile imported Bash function" >&2
  exit 1
fi
if [[ -e "${HOST_BASH_FUNC_MARKER}" ]]; then
  echo "scripts/workcell imported hostile Bash functions before launcher setup" >&2
  exit 1
fi

if env \
  "BASH_FUNC_compgen%%=() { /usr/bin/touch '${HOST_BASH_FUNC_MARKER}'; }" \
  "${ROOT_DIR}/scripts/colima-egress-allowlist.sh" noop default >/dev/null 2>&1; then
  echo "Expected scripts/colima-egress-allowlist.sh noop default to fail" >&2
  exit 1
fi
if [[ -e "${HOST_BASH_FUNC_MARKER}" ]]; then
  echo "scripts/colima-egress-allowlist.sh imported hostile Bash functions before launcher setup" >&2
  exit 1
fi

HOST_PATH_OVERRIDE_DIR="${BARRIER_VERIFY_ROOT}/path-override-bin"
HOST_PATH_BASH_MARKER="${BARRIER_VERIFY_ROOT}/path-bash-ran"
HOST_PATH_DIRNAME_MARKER="${BARRIER_VERIFY_ROOT}/path-dirname-ran"
mkdir -p "${HOST_PATH_OVERRIDE_DIR}"
cat >"${HOST_PATH_OVERRIDE_DIR}/bash" <<'EOF'
#!/bin/sh
touch "${HOST_PATH_BASH_MARKER:?}"
exit 99
EOF
cat >"${HOST_PATH_OVERRIDE_DIR}/dirname" <<'EOF'
#!/bin/sh
touch "${HOST_PATH_DIRNAME_MARKER:?}"
exit 99
EOF
chmod 0755 "${HOST_PATH_OVERRIDE_DIR}/bash" "${HOST_PATH_OVERRIDE_DIR}/dirname"
if ! HOST_PATH_BASH_MARKER="${HOST_PATH_BASH_MARKER}" \
  HOST_PATH_DIRNAME_MARKER="${HOST_PATH_DIRNAME_MARKER}" \
  PATH="${HOST_PATH_OVERRIDE_DIR}:${PATH}" \
  "${ROOT_DIR}/scripts/workcell" --help >/dev/null 2>&1; then
  echo "Expected scripts/workcell --help to succeed under a hostile PATH" >&2
  exit 1
fi
if [[ -e "${HOST_PATH_BASH_MARKER}" ]] || [[ -e "${HOST_PATH_DIRNAME_MARKER}" ]]; then
  echo "scripts/workcell trusted caller PATH before establishing the host boundary" >&2
  exit 1
fi

HOST_BASH_OVERRIDE_DIR="${BARRIER_VERIFY_ROOT}/bash-override-bin"
HOST_BASH_OVERRIDE_MARKER="${BARRIER_VERIFY_ROOT}/bash-override-ran"
mkdir -p "${HOST_BASH_OVERRIDE_DIR}"
cat >"${HOST_BASH_OVERRIDE_DIR}/bash" <<'EOF'
#!/bin/sh
touch "${HOST_BASH_OVERRIDE_MARKER:?}"
exit 97
EOF
chmod 0755 "${HOST_BASH_OVERRIDE_DIR}/bash"
for script in "${HOST_GATE_SCRIPTS[@]}"; do
  if ! HOST_BASH_OVERRIDE_MARKER="${HOST_BASH_OVERRIDE_MARKER}" \
    PATH="${HOST_BASH_OVERRIDE_DIR}:${PATH}" \
    "${script}" --self-entrypoint-probe >/dev/null 2>&1; then
    echo "Expected ${script} self-entrypoint probe to succeed under a hostile bash on PATH" >&2
    exit 1
  fi
  if [[ -e "${HOST_BASH_OVERRIDE_MARKER}" ]]; then
    echo "${script} trusted caller PATH while selecting its interpreter" >&2
    exit 1
  fi
done

for script in "${HOST_GATE_SCRIPTS[@]}"; do
  gate_name="$(basename "${script}" .sh)"
  gate_path_override_dir="${BARRIER_VERIFY_ROOT}/${gate_name}-path-override-bin"
  gate_path_marker="${BARRIER_VERIFY_ROOT}/${gate_name}-path-ran"
  mkdir -p "${gate_path_override_dir}"
  cat >"${gate_path_override_dir}/head" <<EOF
#!/bin/sh
touch "${gate_path_marker:?}"
exit 99
EOF
  chmod 0755 "${gate_path_override_dir}/head"
  if ! PATH="${gate_path_override_dir}:${PATH}" \
    "${script}" --self-entrypoint-probe >/dev/null 2>&1; then
    echo "Expected ${script} self-entrypoint probe to succeed under a hostile PATH" >&2
    exit 1
  fi
  if [[ -e "${gate_path_marker}" ]]; then
    echo "${script} trusted caller PATH before launcher setup" >&2
    exit 1
  fi
done

HOST_DOCKER_PLUGIN_HOME="${BARRIER_VERIFY_ROOT}/docker-plugin-home"
HOST_DOCKER_PLUGIN_DIR="${HOST_DOCKER_PLUGIN_HOME}/.docker/cli-plugins"
mkdir -p "${HOST_DOCKER_PLUGIN_DIR}"
cat >"${HOST_DOCKER_PLUGIN_DIR}/docker-buildx" <<'EOF'
#!/bin/sh
touch "${WORKCELL_DOCKER_PLUGIN_MARKER:?}"
exit 97
EOF
chmod 0755 "${HOST_DOCKER_PLUGIN_DIR}/docker-buildx"
WORKCELL_DOCKER_PLUGIN_MARKER="${BARRIER_VERIFY_ROOT}/workcell-docker-plugin-ran"
if ! WORKCELL_DOCKER_PLUGIN_MARKER="${WORKCELL_DOCKER_PLUGIN_MARKER}" \
  HOME="${HOST_DOCKER_PLUGIN_HOME}" \
  "${ROOT_DIR}/scripts/workcell" --self-docker-probe >/dev/null 2>&1; then
  echo "Expected scripts/workcell Docker probe to succeed under a hostile HOME docker-buildx plugin" >&2
  exit 1
fi
if [[ -e "${WORKCELL_DOCKER_PLUGIN_MARKER}" ]]; then
  echo "scripts/workcell executed a caller-controlled docker-buildx plugin before trusted Docker client setup" >&2
  exit 1
fi
for script in \
  "${ROOT_DIR}/scripts/container-smoke.sh" \
  "${ROOT_DIR}/scripts/generate-builder-environment-manifest.sh" \
  "${ROOT_DIR}/scripts/verify-release-bundle.sh" \
  "${ROOT_DIR}/scripts/verify-reproducible-build.sh"; do
  gate_name="$(basename "${script}" .sh)"
  gate_marker="${BARRIER_VERIFY_ROOT}/${gate_name}-docker-plugin-ran"
  if ! WORKCELL_DOCKER_PLUGIN_MARKER="${gate_marker}" \
    HOME="${HOST_DOCKER_PLUGIN_HOME}" \
    "${script}" --self-docker-probe >/dev/null 2>&1; then
    echo "Expected ${script} Docker probe to succeed under a hostile HOME docker-buildx plugin" >&2
    exit 1
  fi
  if [[ -e "${gate_marker}" ]]; then
    echo "${script} executed a caller-controlled docker-buildx plugin before trusted Docker client setup" >&2
    exit 1
  fi
done

CONTAINER_SMOKE_PATH_OVERRIDE_DIR="${BARRIER_VERIFY_ROOT}/container-smoke-path-override-bin"
CONTAINER_SMOKE_PATH_MARKER="${BARRIER_VERIFY_ROOT}/container-smoke-path-ran"
mkdir -p "${CONTAINER_SMOKE_PATH_OVERRIDE_DIR}"
cat >"${CONTAINER_SMOKE_PATH_OVERRIDE_DIR}/head" <<EOF
#!/bin/sh
touch "${CONTAINER_SMOKE_PATH_MARKER:?}"
exit 99
EOF
chmod 0755 "${CONTAINER_SMOKE_PATH_OVERRIDE_DIR}/head"
if ! CONTAINER_SMOKE_PATH_MARKER="${CONTAINER_SMOKE_PATH_MARKER}" \
  PATH="${CONTAINER_SMOKE_PATH_OVERRIDE_DIR}:${PATH}" \
  "${ROOT_DIR}/scripts/container-smoke.sh" --self-entrypoint-probe >/dev/null 2>&1; then
  echo "Expected scripts/container-smoke.sh self-entrypoint probe to succeed under a hostile PATH" >&2
  exit 1
fi
if [[ -e "${CONTAINER_SMOKE_PATH_MARKER}" ]]; then
  echo "scripts/container-smoke.sh trusted caller PATH before launcher setup" >&2
  exit 1
fi

RELEASE_BUNDLE_PATH_OVERRIDE_DIR="${BARRIER_VERIFY_ROOT}/verify-release-bundle-path-override-bin"
RELEASE_BUNDLE_PATH_MARKER="${BARRIER_VERIFY_ROOT}/verify-release-bundle-path-ran"
mkdir -p "${RELEASE_BUNDLE_PATH_OVERRIDE_DIR}"
cat >"${RELEASE_BUNDLE_PATH_OVERRIDE_DIR}/head" <<EOF
#!/bin/sh
touch "${RELEASE_BUNDLE_PATH_MARKER:?}"
exit 99
EOF
chmod 0755 "${RELEASE_BUNDLE_PATH_OVERRIDE_DIR}/head"
if ! RELEASE_BUNDLE_PATH_MARKER="${RELEASE_BUNDLE_PATH_MARKER}" \
  PATH="${RELEASE_BUNDLE_PATH_OVERRIDE_DIR}:${PATH}" \
  "${ROOT_DIR}/scripts/verify-release-bundle.sh" --self-entrypoint-probe >/dev/null 2>&1; then
  echo "Expected scripts/verify-release-bundle.sh self-entrypoint probe to succeed under a hostile PATH" >&2
  exit 1
fi
if [[ -e "${RELEASE_BUNDLE_PATH_MARKER}" ]]; then
  echo "scripts/verify-release-bundle.sh trusted caller PATH before launcher setup" >&2
  exit 1
fi

REPRO_BUILD_PATH_OVERRIDE_DIR="${BARRIER_VERIFY_ROOT}/verify-reproducible-build-path-override-bin"
REPRO_BUILD_PATH_MARKER="${BARRIER_VERIFY_ROOT}/verify-reproducible-build-path-ran"
mkdir -p "${REPRO_BUILD_PATH_OVERRIDE_DIR}"
cat >"${REPRO_BUILD_PATH_OVERRIDE_DIR}/head" <<EOF
#!/bin/sh
touch "${REPRO_BUILD_PATH_MARKER:?}"
exit 99
EOF
chmod 0755 "${REPRO_BUILD_PATH_OVERRIDE_DIR}/head"
if ! REPRO_BUILD_PATH_MARKER="${REPRO_BUILD_PATH_MARKER}" \
  PATH="${REPRO_BUILD_PATH_OVERRIDE_DIR}:${PATH}" \
  "${ROOT_DIR}/scripts/verify-reproducible-build.sh" --self-entrypoint-probe >/dev/null 2>&1; then
  echo "Expected scripts/verify-reproducible-build.sh self-entrypoint probe to succeed under a hostile PATH" >&2
  exit 1
fi
if [[ -e "${REPRO_BUILD_PATH_MARKER}" ]]; then
  echo "scripts/verify-reproducible-build.sh trusted caller PATH before launcher setup" >&2
  exit 1
fi

if PATH="${HOST_PATH_OVERRIDE_DIR}:${PATH}" "${ROOT_DIR}/scripts/colima-egress-allowlist.sh" >/dev/null 2>&1; then
  echo "Expected scripts/colima-egress-allowlist.sh without arguments to fail under a hostile PATH" >&2
  exit 1
fi
if [[ -e "${HOST_PATH_BASH_MARKER}" ]] || [[ -e "${HOST_PATH_DIRNAME_MARKER}" ]]; then
  echo "scripts/colima-egress-allowlist.sh trusted caller PATH before argument validation" >&2
  exit 1
fi

HOST_PYTHON_INJECT_DIR="${BARRIER_VERIFY_ROOT}/python-inject"
HOST_PYTHON_MARKER="${BARRIER_VERIFY_ROOT}/pythonpath-ran"
mkdir -p "${HOST_PYTHON_INJECT_DIR}"
cat >"${HOST_PYTHON_INJECT_DIR}/sitecustomize.py" <<'EOF'
import os
with open(os.environ["HOST_PYTHON_MARKER"], "w", encoding="utf-8") as handle:
    handle.write("ran\n")
EOF
if ! HOST_PYTHON_MARKER="${HOST_PYTHON_MARKER}" \
  PYTHONPATH="${HOST_PYTHON_INJECT_DIR}" \
  "${ROOT_DIR}/scripts/workcell" --help >/dev/null 2>&1; then
  echo "Expected scripts/workcell --help to succeed under a hostile PYTHONPATH" >&2
  exit 1
fi
if [[ -e "${HOST_PYTHON_MARKER}" ]]; then
  echo "scripts/workcell executed hostile Python import hooks before launcher setup" >&2
  exit 1
fi

HOST_PERL_INJECT_DIR="${BARRIER_VERIFY_ROOT}/perl-inject"
HOST_PERL_MARKER="${BARRIER_VERIFY_ROOT}/perl-ran"
mkdir -p "${HOST_PERL_INJECT_DIR}"
cat >"${HOST_PERL_INJECT_DIR}/WorkcellMarker.pm" <<'EOF'
package WorkcellMarker;

BEGIN {
  open my $fh, '>', $ENV{WORKCELL_PERL_MARKER} or die "marker: $!";
  print {$fh} "ran\n";
  close $fh;
}

1;
EOF
if ! WORKCELL_PERL_MARKER="${HOST_PERL_MARKER}" \
  PERL5OPT=-MWorkcellMarker \
  PERL5LIB="${HOST_PERL_INJECT_DIR}" \
  "${ROOT_DIR}/scripts/workcell" --dry-run >/dev/null 2>&1; then
  echo "Expected scripts/workcell --dry-run to succeed under a hostile Perl environment" >&2
  exit 1
fi
if [[ -e "${HOST_PERL_MARKER}" ]]; then
  echo "scripts/workcell executed hostile Perl hooks before launcher setup" >&2
  exit 1
fi

if [[ "$(uname -s)" == "Darwin" ]] && command -v clang >/dev/null 2>&1; then
  HOST_DYLD_SOURCE="${BARRIER_VERIFY_ROOT}/dyld-marker.c"
  HOST_DYLD_LIB="${BARRIER_VERIFY_ROOT}/libworkcell-marker.dylib"
  HOST_DYLD_MARKER="${BARRIER_VERIFY_ROOT}/dyld-ran"
  cat >"${HOST_DYLD_SOURCE}" <<'EOF'
#include <stdio.h>
#include <stdlib.h>

__attribute__((constructor))
static void write_marker(void) {
  const char *path = getenv("WORKCELL_DYLD_MARKER");
  FILE *handle;

  if (path == NULL) {
    return;
  }

  handle = fopen(path, "w");
  if (handle == NULL) {
    return;
  }

  fputs("ran\n", handle);
  fclose(handle);
}
EOF
  clang -dynamiclib -o "${HOST_DYLD_LIB}" "${HOST_DYLD_SOURCE}"
  if ! WORKCELL_DYLD_MARKER="${HOST_DYLD_MARKER}" \
    DYLD_INSERT_LIBRARIES="${HOST_DYLD_LIB}" \
    DYLD_FORCE_FLAT_NAMESPACE=1 \
    "${ROOT_DIR}/scripts/workcell" --help >/dev/null 2>&1; then
    echo "Expected scripts/workcell --help to succeed under hostile DYLD injection" >&2
    exit 1
  fi
  if [[ -e "${HOST_DYLD_MARKER}" ]]; then
    echo "scripts/workcell honored hostile DYLD injection before launcher setup" >&2
    exit 1
  fi
  if WORKCELL_DYLD_MARKER="${HOST_DYLD_MARKER}" \
    DYLD_INSERT_LIBRARIES="${HOST_DYLD_LIB}" \
    DYLD_FORCE_FLAT_NAMESPACE=1 \
    "${ROOT_DIR}/scripts/colima-egress-allowlist.sh" noop default >/tmp/workcell-dyld-colima.out 2>&1; then
    echo "Expected scripts/colima-egress-allowlist.sh noop default to fail" >&2
    exit 1
  fi
  if [[ -e "${HOST_DYLD_MARKER}" ]]; then
    echo "scripts/colima-egress-allowlist.sh honored hostile DYLD injection before launcher setup" >&2
    exit 1
  fi
fi

MODE_TRAVERSAL_WORKSPACE="${BARRIER_VERIFY_ROOT}/mode-traversal-workspace"
MODE_TRAVERSAL_ENV="${ROOT_DIR}/tmp/workcell-mode-traversal.env"
MODE_TRAVERSAL_MARKER="${BARRIER_VERIFY_ROOT}/mode-traversal-ran"
mkdir -p "${MODE_TRAVERSAL_WORKSPACE}" "${ROOT_DIR}/tmp"
printf '# marker\n' >"${MODE_TRAVERSAL_WORKSPACE}/AGENTS.md"
cat >"${MODE_TRAVERSAL_ENV}" <<'EOF'
touch "${MODE_TRAVERSAL_MARKER:?}"
EOF
if MODE_TRAVERSAL_MARKER="${MODE_TRAVERSAL_MARKER}" \
  "${ROOT_DIR}/scripts/workcell" \
  --mode ../../tmp/workcell-mode-traversal \
  --allow-nongit-workspace \
  --workspace "${MODE_TRAVERSAL_WORKSPACE}" \
  --dry-run >/tmp/workcell-mode-traversal.out 2>&1; then
  echo "Expected unsupported --mode traversal input to fail" >&2
  exit 1
fi
if [[ -e "${MODE_TRAVERSAL_MARKER}" ]]; then
  echo "scripts/workcell sourced a caller-controlled mode profile path before validation" >&2
  exit 1
fi
grep -q "Unsupported mode" /tmp/workcell-mode-traversal.out
rm -f "${MODE_TRAVERSAL_ENV}"

if "${ROOT_DIR}/scripts/workcell" --mode strict --rebuild --dry-run >/tmp/workcell-strict-rebuild.out 2>&1; then
  echo "Expected strict mode to reject explicit --rebuild requests" >&2
  exit 1
fi
grep -q "strict mode does not rebuild or cold-bootstrap the runtime image" /tmp/workcell-strict-rebuild.out

DRY_RUN_OUTPUT="$("${ROOT_DIR}/scripts/workcell" --agent codex --mode strict --dry-run 2>/dev/null)"

MASK_VERIFY_WORKSPACE="${BARRIER_VERIFY_ROOT}/mask-workspace"
mkdir -p "${MASK_VERIFY_WORKSPACE}/nested/.claude"
git init -q "${MASK_VERIFY_WORKSPACE}"
printf '# root agent marker\n' >"${MASK_VERIFY_WORKSPACE}/AGENTS.md"
mkdir -p "${MASK_VERIFY_WORKSPACE}/.codex"
printf 'profile = "strict"\n' >"${MASK_VERIFY_WORKSPACE}/.codex/config.toml"
printf '# nested agent marker\n' >"${MASK_VERIFY_WORKSPACE}/nested/AGENTS.md"
printf '{\n  "masked": true\n}\n' >"${MASK_VERIFY_WORKSPACE}/nested/.claude/settings.json"
mkdir -p "${MASK_VERIFY_WORKSPACE}/symlink-targets/.codex"
printf '# symlinked agent marker\n' >"${MASK_VERIFY_WORKSPACE}/symlink-targets/AGENTS.md"
printf 'profile = "strict"\n' >"${MASK_VERIFY_WORKSPACE}/symlink-targets/.codex/config.toml"
mkdir -p "${MASK_VERIFY_WORKSPACE}/symlinked"
ln -s "${MASK_VERIFY_WORKSPACE}/symlink-targets/AGENTS.md" "${MASK_VERIFY_WORKSPACE}/symlinked/AGENTS.md"
ln -s "${MASK_VERIFY_WORKSPACE}/symlink-targets/.codex" "${MASK_VERIFY_WORKSPACE}/symlinked/.codex"
git init -q "${MASK_VERIFY_WORKSPACE}/.alt"
MASK_DRY_RUN_OUTPUT="$("${ROOT_DIR}/scripts/workcell" --agent codex --mode strict --workspace "${MASK_VERIFY_WORKSPACE}" --dry-run 2>/dev/null)"

for forbidden in "docker.sock" "SSH_AUTH_SOCK" "/.ssh" "/.aws" "Library/Keychains" ".gnupg"; do
  if echo "${DRY_RUN_OUTPUT}" | grep -q "${forbidden}"; then
    echo "Unexpected host exposure in dry-run output: ${forbidden}" >&2
    exit 1
  fi
done

for required in "--user" "HOME=/state/agent-home" "CODEX_HOME=/state/agent-home/.codex" "TMPDIR=/state/tmp" "WORKCELL_RUNTIME=1" "--tmpfs /tmp:nosuid" "noexec" "--tmpfs /state:"; do
  if ! echo "${DRY_RUN_OUTPUT}" | grep -q -- "${required}"; then
    echo "Missing runtime control in dry-run output: ${required}" >&2
    exit 1
  fi
done

for required in "/workspace/AGENTS.md:ro" "/workspace/.codex:ro" "/workspace/.git/config:ro"; do
  if ! echo "${MASK_DRY_RUN_OUTPUT}" | grep -q -- "${required}"; then
    echo "Missing workspace control-plane masking mount in dry-run output: ${required}" >&2
    exit 1
  fi
done

for required in "/workspace/nested/AGENTS.md:ro" "/workspace/nested/.claude:ro" "/workspace/.alt/.git/config:ro"; do
  if ! echo "${MASK_DRY_RUN_OUTPUT}" | grep -q -- "${required}"; then
    echo "Missing nested workspace control-plane masking mount in dry-run output: ${required}" >&2
    exit 1
  fi
done

for required in "/workspace/symlinked/AGENTS.md:ro" "/workspace/symlinked/.codex:ro"; do
  if ! echo "${MASK_DRY_RUN_OUTPUT}" | grep -q -- "${required}"; then
    echo "Missing symlinked workspace control-plane masking mount in dry-run output: ${required}" >&2
    exit 1
  fi
done

for forbidden in "github.com:443" "api.github.com:443" "objects.githubusercontent.com:443" "raw.githubusercontent.com:443"; do
  if echo "${DRY_RUN_OUTPUT}" | grep -q "${forbidden}"; then
    echo "Unexpected strict-mode egress allowance in dry-run output: ${forbidden}" >&2
    exit 1
  fi
done

if "${ROOT_DIR}/scripts/workcell" --agent codex --workspace "${REAL_HOME}" --dry-run >/dev/null 2>&1; then
  echo "Expected broad workspace rejection for ${REAL_HOME}" >&2
  exit 1
fi

if "${ROOT_DIR}/scripts/workcell" --agent codex --mode breakglass --dry-run >/dev/null 2>&1; then
  echo "Expected breakglass acknowledgement requirement" >&2
  exit 1
fi

if ! "${ROOT_DIR}/scripts/workcell" --agent codex --mode breakglass --ack-breakglass --dry-run >/dev/null 2>&1; then
  echo "Expected acknowledged breakglass dry-run to succeed" >&2
  exit 1
fi

if "${ROOT_DIR}/scripts/workcell" --agent codex --allow-arbitrary-command --dry-run >/dev/null 2>&1; then
  echo "Expected arbitrary command acknowledgement requirement" >&2
  exit 1
fi

ARBITRARY_DRY_RUN_OUTPUT="$("${ROOT_DIR}/scripts/workcell" --agent codex --allow-arbitrary-command --ack-arbitrary-command --dry-run -- bash -lc true 2>/dev/null)"
if [[ -z "${ARBITRARY_DRY_RUN_OUTPUT}" ]]; then
  echo "Expected acknowledged arbitrary command dry-run to succeed" >&2
  exit 1
fi

if ! echo "${ARBITRARY_DRY_RUN_OUTPUT}" | grep -q -- '--entrypoint bash'; then
  echo "Expected arbitrary command path to bypass the managed container entrypoint" >&2
  exit 1
fi

if "${ROOT_DIR}/scripts/workcell" --agent codex --colima-profile ../../Library/Caches/colima-evil --dry-run >/dev/null 2>&1; then
  echo "Expected invalid Colima profile name rejection" >&2
  exit 1
fi

FAKE_VM_BIN="${BARRIER_VERIFY_ROOT}/fake-vm-bin"
mkdir -p "${FAKE_VM_BIN}"
cat >"${FAKE_VM_BIN}/colima" <<'EOF'
#!/usr/bin/env sh
exit 0
EOF
cat >"${FAKE_VM_BIN}/limactl" <<'EOF'
#!/usr/bin/env sh
touch "${WORKCELL_FAKE_LIMACTL_MARKER:?}"
cat >/dev/null
exit 0
EOF
chmod 0755 "${FAKE_VM_BIN}/colima" "${FAKE_VM_BIN}/limactl"
rm -f /tmp/workcell-egress-pwned
if PATH="${FAKE_VM_BIN}:${PATH}" WORKCELL_FAKE_LIMACTL_MARKER="${BARRIER_VERIFY_ROOT}/fake-limactl-ran" \
  "${ROOT_DIR}/scripts/colima-egress-allowlist.sh" apply default 'example.com:443; touch /tmp/workcell-egress-pwned' \
  >/tmp/workcell-egress-invalid.out 2>&1; then
  echo "Expected invalid egress endpoint rejection" >&2
  exit 1
fi
if ! grep -q "Invalid endpoint" /tmp/workcell-egress-invalid.out; then
  echo "Expected explicit invalid-endpoint validation failure" >&2
  exit 1
fi
if [[ -e "${BARRIER_VERIFY_ROOT}/fake-limactl-ran" ]]; then
  echo "Invalid egress endpoint reached limactl execution" >&2
  exit 1
fi
if [[ -e /tmp/workcell-egress-pwned ]]; then
  echo "Invalid egress endpoint payload escaped validation" >&2
  exit 1
fi

if [[ -d "${REAL_HOME}/.ssh" ]] && "${ROOT_DIR}/scripts/workcell" --agent codex --allow-nongit-workspace --workspace "${REAL_HOME}/.ssh" --dry-run >/dev/null 2>&1; then
  echo "Expected sensitive workspace rejection for ${REAL_HOME}/.ssh" >&2
  exit 1
fi

if [[ -d "${REAL_HOME}/.config" ]] && "${ROOT_DIR}/scripts/workcell" --agent codex --allow-nongit-workspace --workspace "${REAL_HOME}/.config" --dry-run >/dev/null 2>&1; then
  echo "Expected sensitive workspace rejection for ${REAL_HOME}/.config" >&2
  exit 1
fi

if [[ -d "${REAL_HOME}/Library/Application Support" ]]; then
  if "${ROOT_DIR}/scripts/workcell" --agent codex --allow-nongit-workspace --workspace "${REAL_HOME}/Library/Application Support" --dry-run >/dev/null 2>&1; then
    echo "Expected sensitive workspace rejection for ${REAL_HOME}/Library/Application Support" >&2
    exit 1
  fi
  BROWSER_PROFILE_FIXTURE="${REAL_HOME}/Library/Application Support/Google/Chrome/WorkcellVerifyBrowserProfile"
  mkdir -p "${BROWSER_PROFILE_FIXTURE}"
  if "${ROOT_DIR}/scripts/workcell" --agent codex --allow-nongit-workspace --workspace "${BROWSER_PROFILE_FIXTURE}" --dry-run >/dev/null 2>&1; then
    echo "Expected browser-profile workspace rejection for ${BROWSER_PROFILE_FIXTURE}" >&2
    exit 1
  fi
fi

host_tool_exists() {
  local candidate
  for candidate in "$@"; do
    if [[ -x "${candidate}" ]]; then
      return 0
    fi
  done
  return 1
}

if [[ -d "${REAL_HOME}/Library/Application Support" ]]; then
  if HOME="${BARRIER_VERIFY_ROOT}/fake-home" \
    "${ROOT_DIR}/scripts/workcell" \
    --agent codex \
    --allow-nongit-workspace \
    --workspace "${REAL_HOME}/Library/Application Support" \
    --dry-run >/dev/null 2>&1; then
    echo "Expected scripts/workcell to reject sensitive real-home mounts even when caller HOME is overridden" >&2
    exit 1
  fi
fi

NONGIT_WORKSPACE="${BARRIER_VERIFY_ROOT}/nongit-workspace"
mkdir -p "${NONGIT_WORKSPACE}"
NONGIT_WORKSPACE="$(cd "${NONGIT_WORKSPACE}" && pwd -P)"
if "${ROOT_DIR}/scripts/workcell" --agent codex --workspace "${NONGIT_WORKSPACE}" --dry-run >/dev/null 2>&1; then
  echo "Expected non-git workspace rejection without explicit opt-in" >&2
  exit 1
fi
printf '# marker\n' >"${NONGIT_WORKSPACE}/AGENTS.md"
if ! "${ROOT_DIR}/scripts/workcell" --agent codex --allow-nongit-workspace --workspace "${NONGIT_WORKSPACE}" --dry-run >/dev/null 2>&1; then
  echo "Expected marker-based non-git workspace to succeed with explicit opt-in" >&2
  exit 1
fi

if [[ "$(uname -s)" == "Darwin" ]] &&
  host_tool_exists /opt/homebrew/bin/colima /usr/local/bin/colima &&
  host_tool_exists /opt/homebrew/bin/docker /usr/local/bin/docker /Applications/Docker.app/Contents/Resources/bin/docker &&
  host_tool_exists /usr/bin/ruby /opt/homebrew/bin/ruby /usr/local/bin/ruby; then
  RUBYOPT_MARKER="${BARRIER_VERIFY_ROOT}/rubyopt-ran"
  RUBYOPT_PAYLOAD="${BARRIER_VERIFY_ROOT}/rubyopt-payload.rb"
  RUBY_PROFILE_NAME="workcell-ruby-verify-$$"
  COLIMA_PROFILE_FIXTURE="${REAL_HOME}/.colima/${RUBY_PROFILE_NAME}"
  mkdir -p "${COLIMA_PROFILE_FIXTURE}"
  printf '%s\n' "${NONGIT_WORKSPACE}" >"${COLIMA_PROFILE_FIXTURE}/workcell.managed"
  cat >"${COLIMA_PROFILE_FIXTURE}/colima.yaml" <<'EOF'
vmType: qemu
mountType: virtiofs
runtime: docker
EOF
  cat >"${RUBYOPT_PAYLOAD}" <<'EOF'
File.write(ENV.fetch("RUBYOPT_MARKER"), "ran\n")
EOF
  if RUBYOPT_MARKER="${RUBYOPT_MARKER}" \
    RUBYOPT="-r${RUBYOPT_PAYLOAD}" \
    "${ROOT_DIR}/scripts/workcell" \
    --agent codex \
    --allow-nongit-workspace \
    --workspace "${NONGIT_WORKSPACE}" \
    --colima-profile "${RUBY_PROFILE_NAME}" >/tmp/workcell-rubyopt.out 2>&1; then
    echo "Expected invalid managed Colima profile validation to fail" >&2
    exit 1
  fi
  if [[ -e "${RUBYOPT_MARKER}" ]]; then
    echo "scripts/workcell executed hostile Ruby preload hooks before validating managed Colima profiles" >&2
    exit 1
  fi
  if ! grep -Eq "Unexpected configured Colima mounts|Unexpected Colima vmType" /tmp/workcell-rubyopt.out; then
    echo "Expected managed Colima profile validation failure output for hostile Ruby preload fixture" >&2
    cat /tmp/workcell-rubyopt.out >&2
    exit 1
  fi
fi

WORKTREE_ROOT="${BARRIER_VERIFY_ROOT}/worktree-root"
WORKTREE_MAIN="${WORKTREE_ROOT}/main"
WORKTREE_LINKED="${WORKTREE_ROOT}/linked"
mkdir -p "${WORKTREE_ROOT}"
git init -q "${WORKTREE_MAIN}"
git -C "${WORKTREE_MAIN}" config user.name "Workcell Verify"
git -C "${WORKTREE_MAIN}" config user.email "workcell-verify@example.com"
touch "${WORKTREE_MAIN}/tracked.txt"
git -C "${WORKTREE_MAIN}" add tracked.txt
git -C "${WORKTREE_MAIN}" commit -q -m init
git -C "${WORKTREE_MAIN}" worktree add -q -b linked "${WORKTREE_LINKED}"
if "${ROOT_DIR}/scripts/workcell" --agent codex --workspace "${WORKTREE_LINKED}" --dry-run >/dev/null 2>&1; then
  echo "Expected linked git worktree with external admin state to be rejected" >&2
  exit 1
fi

REDIRECTED_ROOT="${BARRIER_VERIFY_ROOT}/redirected-root"
REDIRECTED_REPO="${REDIRECTED_ROOT}/repo"
REDIRECTED_WORKTREE="${REDIRECTED_ROOT}/outside"
mkdir -p "${REDIRECTED_WORKTREE}"
git init -q "${REDIRECTED_REPO}"
git --git-dir "${REDIRECTED_REPO}/.git" config core.worktree "${REDIRECTED_WORKTREE}"
if "${ROOT_DIR}/scripts/workcell" --agent codex --workspace "${REDIRECTED_REPO}" --dry-run >/dev/null 2>&1; then
  echo "Expected redirected core.worktree repo to be rejected" >&2
  exit 1
fi

cp -R "${ROOT_DIR}/adapters/codex/.codex/." "${CODEX_VERIFY_HOME}/"
if command -v codex >/dev/null 2>&1; then
  CODEX_HOME="${CODEX_VERIFY_HOME}" codex features list >/dev/null
  codex execpolicy check --rules "${ROOT_DIR}/adapters/codex/.codex/rules/default.rules" rm -rf build | jq -e '.decision == "forbidden"' >/dev/null
  codex execpolicy check --rules "${ROOT_DIR}/adapters/codex/.codex/rules/default.rules" git push origin feature | jq -e '.decision == "prompt"' >/dev/null
  codex execpolicy check --rules "${ROOT_DIR}/adapters/codex/.codex/rules/default.rules" git push origin main --force | jq -e '.decision == "forbidden"' >/dev/null
  codex execpolicy check --rules "${ROOT_DIR}/adapters/codex/.codex/rules/default.rules" git commit --no-verify | jq -e '.decision == "forbidden"' >/dev/null
  codex execpolicy check --rules "${ROOT_DIR}/adapters/codex/.codex/rules/default.rules" /usr/bin/git push --no-verify origin feature | jq -e '.decision == "forbidden"' >/dev/null
else
  echo "Skipping host Codex CLI policy checks because codex is not installed; container smoke covers provider policy behavior." >&2
fi
python3 - "${ROOT_DIR}" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
claude_settings = json.loads((root / "adapters/claude/.claude/settings.json").read_text(encoding="utf-8"))
claude_managed = json.loads((root / "adapters/claude/managed-settings.json").read_text(encoding="utf-8"))
gemini_settings = json.loads((root / "adapters/gemini/.gemini/settings.json").read_text(encoding="utf-8"))

for label, settings in (("claude", claude_settings), ("claude managed", claude_managed)):
    if settings.get("enableAllProjectMcpServers") is not False:
        raise SystemExit(f"{label} settings must disable auto-enabled project MCP servers")
    guard = (
        settings.get("hooks", {})
        .get("PreToolUse", [{}])[0]
        .get("hooks", [{}])[0]
        .get("command")
    )
    if guard != "/opt/workcell/adapters/claude/hooks/guard-bash.sh":
        raise SystemExit(f"{label} settings must use the managed guard-bash.sh hook")

if claude_managed.get("disableBypassPermissionsMode") != "disable":
    raise SystemExit("Claude managed settings must disable bypass-permissions mode")

if gemini_settings.get("tools", {}).get("allowed") != []:
    raise SystemExit("Gemini adapter must not seed allowed tools")
if gemini_settings.get("mcp", {}).get("allowed") != []:
    raise SystemExit("Gemini adapter must not seed allowed MCP servers")
if gemini_settings.get("tools", {}).get("shell", {}).get("enableInteractiveShell") is not False:
    raise SystemExit("Gemini adapter must disable interactive shell mode")
if not isinstance(gemini_settings.get("advanced", {}).get("excludedEnvVars"), list):
    raise SystemExit("Gemini adapter must exclude sensitive environment variables")
PY

echo "Workcell invariant verification passed."
