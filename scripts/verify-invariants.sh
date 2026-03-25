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
INSTALL_VERIFY_HOME="$(mktemp -d)"
REMOTE_VALIDATE_CONFIG_ROOT="$(mktemp -d)"
LOCAL_REMOTE_CONFIG_PATH="${REMOTE_VALIDATE_CONFIG_ROOT}/remote-validate.env"
LEGACY_LOCAL_REMOTE_CONFIG_PATH="${ROOT_DIR}/.workcell.remote.local"
REPO_LOCAL_REMOTE_CONFIG_PATH="${ROOT_DIR}/tmp/verify-remote-validate-repo.env"
ROOT_DRY_RUN_PROFILE_NAME="$(
  python3 - "${ROOT_DIR}" <<'PY'
import hashlib
import pathlib
import re
import sys

workspace = pathlib.Path(sys.argv[1]).resolve()
slug = re.sub(r"[^a-z0-9]+", "-", workspace.name.lower()).strip("-")[:10] or "workspace"
digest = hashlib.sha256(str(workspace).encode("utf-8")).hexdigest()[:8]
print(f"workcell-{slug}-{digest}")
PY
)"
ROOT_DRY_RUN_PROFILE_DIR="${REAL_HOME}/.colima/${ROOT_DRY_RUN_PROFILE_NAME}"
ROOT_DRY_RUN_LIMA_DIR="${REAL_HOME}/.colima/_lima/colima-${ROOT_DRY_RUN_PROFILE_NAME}"

file_mode_octal() {
  local path="$1"

  if stat -f '%Lp' "${path}" >/dev/null 2>&1; then
    stat -f '%Lp' "${path}"
  else
    stat -c '%a' "${path}"
  fi
}

cleanup() {
  rm -rf "${CODEX_VERIFY_HOME}"
  rm -rf "${BARRIER_VERIFY_ROOT}"
  rm -rf "${INSTALL_VERIFY_HOME}"
  rm -rf "${REMOTE_VALIDATE_CONFIG_ROOT}"
  rm -f "${REPO_LOCAL_REMOTE_CONFIG_PATH}"
  if [[ -n "${BROWSER_PROFILE_FIXTURE}" ]] && [[ -d "${BROWSER_PROFILE_FIXTURE}" ]]; then
    rmdir "${BROWSER_PROFILE_FIXTURE}" 2>/dev/null || true
  fi
  if [[ -n "${COLIMA_PROFILE_FIXTURE}" ]] && [[ -d "${COLIMA_PROFILE_FIXTURE}" ]]; then
    rm -rf "${COLIMA_PROFILE_FIXTURE}"
  fi
  rm -f "${LEGACY_LOCAL_REMOTE_CONFIG_PATH}"
}

trap cleanup EXIT

if [[ -d "${ROOT_DRY_RUN_PROFILE_DIR}" ]] && [[ ! -f "${ROOT_DRY_RUN_PROFILE_DIR}/workcell.managed" ]]; then
  rm -rf "${ROOT_DRY_RUN_PROFILE_DIR}" "${ROOT_DRY_RUN_LIMA_DIR}"
fi

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
  "${ROOT_DIR}/runtime/container/runtime-user.sh" \
  "${ROOT_DIR}/runtime/container/rust/Cargo.toml" \
  "${ROOT_DIR}/runtime/container/rust/src/lib.rs" \
  "${ROOT_DIR}/runtime/container/rust/src/bin/workcell-git-launcher.rs" \
  "${ROOT_DIR}/runtime/container/rust/src/bin/workcell-launcher.rs" \
  "${ROOT_DIR}/scripts/lib/render_injection_bundle.py" \
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

if ! env -i HOME="${INSTALL_VERIFY_HOME}" PATH="${TRUSTED_HOST_PATH}" "${ROOT_DIR}/scripts/install.sh" >/tmp/workcell-install.out 2>&1; then
  echo "Expected scripts/install.sh to succeed in a clean temporary HOME" >&2
  cat /tmp/workcell-install.out >&2
  exit 1
fi

if ! "${INSTALL_VERIFY_HOME}/.local/bin/workcell" --help >/tmp/workcell-installed-help.out 2>&1; then
  echo "Expected installed ~/.local/bin/workcell symlink to resolve support files correctly" >&2
  cat /tmp/workcell-installed-help.out >&2
  exit 1
fi

if ! grep -q '^Usage: workcell' /tmp/workcell-installed-help.out; then
  echo "Expected installed ~/.local/bin/workcell --help to print usage" >&2
  exit 1
fi

if ! grep -q -- '--prepare' /tmp/workcell-installed-help.out; then
  echo "Expected installed ~/.local/bin/workcell --help to describe --prepare" >&2
  exit 1
fi

if ! grep -q -- '--prepare-only' /tmp/workcell-installed-help.out; then
  echo "Expected installed ~/.local/bin/workcell --help to describe --prepare-only" >&2
  exit 1
fi

if ! grep -q -- '--repair-profile' /tmp/workcell-installed-help.out; then
  echo "Expected installed ~/.local/bin/workcell --help to describe --repair-profile" >&2
  exit 1
fi

if ! grep -q 'Repair a conflicting unmanaged profile' /tmp/workcell-installed-help.out; then
  echo "Expected installed ~/.local/bin/workcell --help to describe unmanaged-profile repair accurately" >&2
  exit 1
fi

if ! grep -q -- '--agent-autonomy yolo|prompt' /tmp/workcell-installed-help.out; then
  echo "Expected installed ~/.local/bin/workcell --help to describe --agent-autonomy" >&2
  exit 1
fi

if ! grep -q -- '--agent-arg VALUE' /tmp/workcell-installed-help.out; then
  echo "Expected installed ~/.local/bin/workcell --help to describe --agent-arg" >&2
  exit 1
fi

if ! grep -q -- '--container-mutability ephemeral|readonly' /tmp/workcell-installed-help.out; then
  echo "Expected installed ~/.local/bin/workcell --help to describe --container-mutability" >&2
  exit 1
fi

if ! grep -q -- '--injection-policy PATH' /tmp/workcell-installed-help.out; then
  echo "Expected installed ~/.local/bin/workcell --help to describe --injection-policy" >&2
  exit 1
fi

if ! grep -q -- '--no-default-injection-policy' /tmp/workcell-installed-help.out; then
  echo "Expected installed ~/.local/bin/workcell --help to describe --no-default-injection-policy" >&2
  exit 1
fi

if ! grep -q 'Provider to run (required)' /tmp/workcell-installed-help.out; then
  echo "Expected installed ~/.local/bin/workcell --help to describe --agent as required" >&2
  exit 1
fi

INJECTION_POLICY_FIXTURE_ROOT="${BARRIER_VERIFY_ROOT}/injection-policy"
INJECTION_STATE_ROOT="${INJECTION_POLICY_FIXTURE_ROOT}/xdg-state"
mkdir -p "${INJECTION_POLICY_FIXTURE_ROOT}" "${INJECTION_STATE_ROOT}/workcell/tmp"
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/common.md"
# Common Workcell Instructions
EOF
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/codex.md"
# Codex Workcell Instructions
EOF
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/public.txt"
public fixture
EOF
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/secret.txt"
secret fixture
EOF
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/codex-auth.json"
{"test": "auth"}
EOF
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/claude-auth.json"
{"token": "claude-auth"}
EOF
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/claude-mcp.json"
{"mcpServers": {"stub": {"command": "echo", "args": ["ok"]}}}
EOF
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/gemini-projects.json"
{"projects":{"fixture":{"path":"/workspace"}}}
EOF
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/gh-hosts.yml"
github.com:
  oauth_token: test-token
  user: workcell
  git_protocol: ssh
EOF
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/ssh-config"
Host example
  HostName example.com
EOF
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/known_hosts"
example.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITestKey
EOF
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/id_test"
-----BEGIN OPENSSH PRIVATE KEY-----
test
-----END OPENSSH PRIVATE KEY-----
EOF
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/config"
not-an-identity
EOF
chmod 0600 \
  "${INJECTION_POLICY_FIXTURE_ROOT}/secret.txt" \
  "${INJECTION_POLICY_FIXTURE_ROOT}/codex-auth.json" \
  "${INJECTION_POLICY_FIXTURE_ROOT}/claude-auth.json" \
  "${INJECTION_POLICY_FIXTURE_ROOT}/claude-mcp.json" \
  "${INJECTION_POLICY_FIXTURE_ROOT}/gemini-projects.json" \
  "${INJECTION_POLICY_FIXTURE_ROOT}/gh-hosts.yml" \
  "${INJECTION_POLICY_FIXTURE_ROOT}/ssh-config" \
  "${INJECTION_POLICY_FIXTURE_ROOT}/known_hosts" \
  "${INJECTION_POLICY_FIXTURE_ROOT}/id_test" \
  "${INJECTION_POLICY_FIXTURE_ROOT}/config"
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/policy.toml"
version = 1

[documents]
common = "common.md"
codex = "codex.md"

[credentials]
codex_auth = "codex-auth.json"
claude_auth = "claude-auth.json"
claude_mcp = "claude-mcp.json"
gemini_projects = "gemini-projects.json"

[credentials.github_hosts]
source = "gh-hosts.yml"
providers = ["codex"]

[ssh]
enabled = true
config = "ssh-config"
known_hosts = "known_hosts"
identities = ["id_test"]
providers = ["codex"]
modes = ["strict", "build"]

[[copies]]
source = "public.txt"
target = "/state/injected/public.txt"
classification = "public"
providers = ["codex"]

[[copies]]
source = "secret.txt"
target = "~/.config/workcell/token.txt"
classification = "secret"
providers = ["codex"]
EOF

python3 "${ROOT_DIR}/scripts/lib/render_injection_bundle.py" \
  --policy "${INJECTION_POLICY_FIXTURE_ROOT}/policy.toml" \
  --agent codex \
  --mode strict \
  --output-root "${INJECTION_POLICY_FIXTURE_ROOT}/bundle" >/tmp/workcell-injection-manifest.out
python3 "${ROOT_DIR}/scripts/lib/render_injection_bundle.py" \
  --policy "${INJECTION_POLICY_FIXTURE_ROOT}/policy.toml" \
  --agent claude \
  --mode strict \
  --output-root "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-claude" >/dev/null
python3 "${ROOT_DIR}/scripts/lib/render_injection_bundle.py" \
  --policy "${INJECTION_POLICY_FIXTURE_ROOT}/policy.toml" \
  --agent gemini \
  --mode strict \
  --output-root "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-gemini" >/dev/null
python3 "${ROOT_DIR}/scripts/lib/extract_direct_mounts.py" \
  --manifest "${INJECTION_POLICY_FIXTURE_ROOT}/bundle/manifest.json" \
  --mount-spec "${INJECTION_POLICY_FIXTURE_ROOT}/bundle.mounts.json" >/dev/null

python3 - "${INJECTION_POLICY_FIXTURE_ROOT}/bundle/manifest.json" <<'PY'
import json
import pathlib
import sys

manifest = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
if manifest["documents"]["common"] != "documents/common.md":
    raise SystemExit("expected common document to be staged in the injection bundle")
if manifest["documents"]["codex"] != "documents/codex.md":
    raise SystemExit("expected codex document to be staged in the injection bundle")
targets = {entry["target"]: entry for entry in manifest["copies"]}
if "/state/injected/public.txt" not in targets:
    raise SystemExit("expected public injected file target in manifest")
if "/state/agent-home/.config/workcell/token.txt" not in targets:
    raise SystemExit("expected home-relative injected file target in manifest")
if targets["/state/injected/public.txt"]["source"] != "copies/0":
    raise SystemExit("expected public injected files to stay staged in the bundle")
if targets["/state/agent-home/.config/workcell/token.txt"]["source"]["mount_path"] != "/opt/workcell/host-inputs/copies/1":
    raise SystemExit("expected secret injected files to use the managed direct-mount path")
if "source" in targets["/state/agent-home/.config/workcell/token.txt"]["source"]:
    raise SystemExit("expected secret copy manifests to hide host source paths from the runtime")
if manifest["credentials"]["codex_auth"]["mount_path"] != "/opt/workcell/host-inputs/credentials/codex-auth.json":
    raise SystemExit("expected codex auth credential to use the managed credential mount path")
if "source" in manifest["credentials"]["codex_auth"]:
    raise SystemExit("expected credential manifests to hide host source paths from the runtime")
if manifest["credentials"]["github_hosts"]["mount_path"] != "/opt/workcell/host-inputs/credentials/github-hosts.yml":
    raise SystemExit("expected shared GitHub hosts credential to use the managed credential mount path")
if manifest["ssh"]["config"]["mount_path"] != "/opt/workcell/host-inputs/ssh/config":
    raise SystemExit("expected SSH config to use the managed direct-mount path")
if "source" in manifest["ssh"]["config"]:
    raise SystemExit("expected ssh manifests to hide host source paths from the runtime")
if manifest["ssh"]["identities"][0]["mount_path"] != "/opt/workcell/host-inputs/ssh/identity-0":
    raise SystemExit("expected SSH identities to use the managed direct-mount path")
if "source" in manifest["ssh"]["identities"][0]:
    raise SystemExit("expected ssh identity manifests to hide host source paths from the runtime")
if manifest["ssh"]["identities"][0]["target_name"] != "id_test":
    raise SystemExit("expected ssh identity target name to preserve the source basename")
PY

if [[ -e "${INJECTION_POLICY_FIXTURE_ROOT}/bundle/credentials/codex-auth.json" ]]; then
  echo "Expected credentials.* sources to mount directly from the host instead of being restaged into the bundle" >&2
  exit 1
fi

python3 - "${INJECTION_POLICY_FIXTURE_ROOT}/bundle.mounts.json" <<'PY'
import json
import pathlib
import sys

mounts = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
mount_paths = {entry["mount_path"] for entry in mounts}
expected = {
    "/opt/workcell/host-inputs/credentials/codex-auth.json",
    "/opt/workcell/host-inputs/credentials/github-hosts.yml",
    "/opt/workcell/host-inputs/copies/1",
    "/opt/workcell/host-inputs/ssh/config",
    "/opt/workcell/host-inputs/ssh/known_hosts",
    "/opt/workcell/host-inputs/ssh/identity-0",
}
if expected - mount_paths:
    raise SystemExit("expected direct-mount spec to preserve all secret input mount paths")
PY

if [[ -e "${INJECTION_POLICY_FIXTURE_ROOT}/bundle/ssh/config" ]]; then
  echo "Expected ssh.* sources to mount directly from the host instead of being restaged into the bundle" >&2
  exit 1
fi

python3 - "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-claude/manifest.json" "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-gemini/manifest.json" <<'PY'
import json
import pathlib
import sys

claude_manifest = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
gemini_manifest = json.loads(pathlib.Path(sys.argv[2]).read_text(encoding="utf-8"))

if claude_manifest["credentials"]["claude_auth"]["mount_path"] != "/opt/workcell/host-inputs/credentials/claude-auth.json":
    raise SystemExit("expected claude auth credential to use the managed credential mount path")
if claude_manifest["credentials"]["claude_mcp"]["mount_path"] != "/opt/workcell/host-inputs/credentials/claude-mcp.json":
    raise SystemExit("expected claude MCP credential to use the managed credential mount path")
if gemini_manifest["credentials"]["gemini_projects"]["mount_path"] != "/opt/workcell/host-inputs/credentials/gemini-projects.json":
    raise SystemExit("expected Gemini projects credential to use the managed credential mount path")
PY

INJECTION_DRY_RUN_OUTPUT="$("${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --workspace "${ROOT_DIR}" \
  --no-default-injection-policy \
  --injection-policy "${INJECTION_POLICY_FIXTURE_ROOT}/policy.toml" \
  --dry-run)"

if [[ "${INJECTION_DRY_RUN_OUTPUT}" != *'WORKCELL_INJECTION_MANIFEST=/opt/workcell/host-injections/manifest.json'* ]]; then
  echo "Expected workcell --dry-run to pass the staged injection manifest into the runtime" >&2
  exit 1
fi

if [[ "${INJECTION_DRY_RUN_OUTPUT}" != *'/opt/workcell/host-injections:ro'* ]]; then
  echo "Expected workcell --dry-run to mount the staged injection bundle read-only" >&2
  exit 1
fi

if [[ "${INJECTION_DRY_RUN_OUTPUT}" != *'/opt/workcell/host-inputs/credentials/codex-auth.json:ro'* ]]; then
  echo "Expected workcell --dry-run to mount validated credential sources directly into the runtime" >&2
  exit 1
fi

if [[ "${INJECTION_DRY_RUN_OUTPUT}" == *"${INJECTION_POLICY_FIXTURE_ROOT}/codex-auth.json"* ]]; then
  echo "Expected workcell --dry-run to redact host credential source paths" >&2
  exit 1
fi

if [[ "${INJECTION_DRY_RUN_OUTPUT}" != *'WORKCELL_CONTAINER_MUTABILITY=ephemeral'* ]]; then
  echo "Expected workcell --dry-run to default strict mode to ephemeral container mutability" >&2
  exit 1
fi

STALE_INJECTION_BUNDLE="${REAL_HOME}/.local/state/workcell/tmp/workcell-injections.verify-stale.$$"
STALE_INJECTION_SIDECAR="${STALE_INJECTION_BUNDLE}.mounts.json"
mkdir -p "$(dirname "${STALE_INJECTION_BUNDLE}")"
mkdir -p "${STALE_INJECTION_BUNDLE}"
printf '999999\n' >"${STALE_INJECTION_BUNDLE}/owner.pid"
printf 'stale-secret\n' >"${STALE_INJECTION_BUNDLE}/stale.txt"
printf '[{"source":"/tmp/stale-secret","mount_path":"/opt/workcell/host-inputs/credentials/stale"}]\n' >"${STALE_INJECTION_SIDECAR}"
touch -t 202001010000 "${STALE_INJECTION_BUNDLE}" "${STALE_INJECTION_BUNDLE}/owner.pid" "${STALE_INJECTION_BUNDLE}/stale.txt" "${STALE_INJECTION_SIDECAR}"
"${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --workspace "${ROOT_DIR}" \
  --no-default-injection-policy \
  --dry-run >/tmp/workcell-no-policy-dry-run.out

if [[ -e "${STALE_INJECTION_BUNDLE}" ]]; then
  echo "Expected startup cleanup to remove dead-owner injection bundles even when no injection policy is active" >&2
  exit 1
fi

if [[ -e "${STALE_INJECTION_SIDECAR}" ]]; then
  echo "Expected startup cleanup to remove stale direct-mount sidecars alongside dead-owner injection bundles" >&2
  exit 1
fi

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/bad-policy.toml"
version = 1

[[copies]]
source = "secret.txt"
target = "~/.codex/config.toml"
EOF

if python3 "${ROOT_DIR}/scripts/lib/render_injection_bundle.py" \
  --policy "${INJECTION_POLICY_FIXTURE_ROOT}/bad-policy.toml" \
  --agent codex \
  --mode strict \
  --output-root "${INJECTION_POLICY_FIXTURE_ROOT}/bad-bundle" >/tmp/workcell-injection-bad.out 2>&1; then
  echo "Expected injection policy renderer to reject reserved managed targets" >&2
  exit 1
fi

if ! grep -q 'Workcell-managed control-plane path' /tmp/workcell-injection-bad.out; then
  echo "Expected reserved-target injection failure to explain the control-plane collision" >&2
  cat /tmp/workcell-injection-bad.out >&2
  exit 1
fi

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/bad-keys.toml"
version = 1

[[copies]]
source = "secret.txt"
target = "~/.config/workcell/secret.txt"
provider = ["codex"]
classification = "secret"
EOF

if python3 "${ROOT_DIR}/scripts/lib/render_injection_bundle.py" \
  --policy "${INJECTION_POLICY_FIXTURE_ROOT}/bad-keys.toml" \
  --agent codex \
  --mode strict \
  --output-root "${INJECTION_POLICY_FIXTURE_ROOT}/bad-keys-bundle" >/tmp/workcell-injection-bad-keys.out 2>&1; then
  echo "Expected injection policy renderer to reject unknown keys that would otherwise broaden scope" >&2
  exit 1
fi

if ! grep -q 'unsupported keys: provider' /tmp/workcell-injection-bad-keys.out; then
  echo "Expected unknown-key rejection to call out the unexpected key name" >&2
  cat /tmp/workcell-injection-bad-keys.out >&2
  exit 1
fi

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/missing-classification.toml"
version = 1

[[copies]]
source = "secret.txt"
target = "~/.config/workcell/secret.txt"
EOF

if python3 "${ROOT_DIR}/scripts/lib/render_injection_bundle.py" \
  --policy "${INJECTION_POLICY_FIXTURE_ROOT}/missing-classification.toml" \
  --agent codex \
  --mode strict \
  --output-root "${INJECTION_POLICY_FIXTURE_ROOT}/missing-classification-bundle" >/tmp/workcell-injection-missing-classification.out 2>&1; then
  echo "Expected injection policy renderer to require explicit copy classification" >&2
  exit 1
fi

if ! grep -q 'copies.classification is required' /tmp/workcell-injection-missing-classification.out; then
  echo "Expected missing classification failure to explain the requirement" >&2
  cat /tmp/workcell-injection-missing-classification.out >&2
  exit 1
fi

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/bad-ssh.toml"
version = 1

[ssh]
enabled = true
identities = ["config"]
EOF

if python3 "${ROOT_DIR}/scripts/lib/render_injection_bundle.py" \
  --policy "${INJECTION_POLICY_FIXTURE_ROOT}/bad-ssh.toml" \
  --agent codex \
  --mode strict \
  --output-root "${INJECTION_POLICY_FIXTURE_ROOT}/bad-ssh-bundle" >/tmp/workcell-injection-bad-ssh.out 2>&1; then
  echo "Expected injection policy renderer to reject SSH identity names that collide with reserved files" >&2
  exit 1
fi

if ! grep -q 'reserved SSH file' /tmp/workcell-injection-bad-ssh.out; then
  echo "Expected SSH collision failure to explain the reserved filename" >&2
  cat /tmp/workcell-injection-bad-ssh.out >&2
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

if rg -q 'AGENT_NAME="\$\{AGENT_NAME:-codex\}"' "${ROOT_DIR}/runtime/container/entrypoint.sh"; then
  echo "runtime/container/entrypoint.sh still defaults AGENT_NAME to codex" >&2
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

if ! rg -q '"bootstrap_applied=\$\{BOOTSTRAP_APPLIED\}"' "${ROOT_DIR}/scripts/workcell" ||
  ! rg -q '"bootstrap_endpoints=\$\(\[\[ "\$\{BOOTSTRAP_APPLIED\}" -eq 1 \]\] && printf '\''%s'\'' "\$\{BOOTSTRAP_ENDPOINTS\}" \|\| printf '\'''\''\)"' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell audit records to include bootstrap network metadata" >&2
  exit 1
fi

if ! rg -q 'bootstrap_policy=allowlist endpoints=%s' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to announce temporary bootstrap network policy activation" >&2
  exit 1
fi

if ! sed -n '/^validate_colima_profile()/,/^}/p' "${ROOT_DIR}/scripts/workcell" | grep -q 'validate_colima_profile_config'; then
  echo "Expected validate_colima_profile to re-check the managed Colima config before reusing a running profile" >&2
  exit 1
fi

if ! sed -n '/^git_alias_value_is_blocked()/,/^}/p' "${ROOT_DIR}/scripts/workcell" | grep -q 'git_commit_short_arg_is_no_verify'; then
  echo "Expected git_alias_value_is_blocked to reuse the precise short-option no-verify parser" >&2
  exit 1
fi

if ! sed -n '/^add_shadow_git_hooks_mount()/,/^}/p' "${ROOT_DIR}/scripts/workcell" | grep -Fq "copy_tree_without_symlinks"; then
  echo "Expected add_shadow_git_hooks_mount to avoid copying symlinked hook content into the readonly shadow" >&2
  exit 1
fi

if ! sed -n '/^add_shadow_git_config_mount()/,/^}/p' "${ROOT_DIR}/scripts/workcell" | grep -Fq "! -L \"\${source_path}\""; then
  echo "Expected add_shadow_git_config_mount to ignore symlinked git config files" >&2
  exit 1
fi

if ! grep -Fq "find \"\${workspace}\" -type d -name .git -prune -print0" "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected prepare_workspace_control_plane_shadow to enumerate only real .git directories" >&2
  exit 1
fi
python3 - "${ROOT_DIR}/scripts/workcell" <<'PY'
import pathlib
import sys

text = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
if 'find "${workspace}/${git_rel}/modules" \\' not in text:
    raise SystemExit("Expected prepare_workspace_control_plane_shadow to enumerate module git control-plane paths")
if '-type l \\) -name hooks' not in text:
    raise SystemExit("Expected prepare_workspace_control_plane_shadow to mask symlinked module hook directories as empty readonly mounts")
if '-type l \\) \\( -name config -o -name config.worktree \\)' not in text:
    raise SystemExit("Expected prepare_workspace_control_plane_shadow to mask symlinked module git config files as empty readonly mounts")
if '-type l \\) -name worktrees' not in text:
    raise SystemExit("Expected prepare_workspace_control_plane_shadow to mask symlinked module worktree directories as empty readonly mounts")
PY

if rg -q 'disable_ipv6=1' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "Workcell should not silently disable IPv6 as a fallback for allowlist enforcement" >&2
  exit 1
fi

if ! rg -q 'requires ip6tables support to enforce dual-stack allowlist egress policy' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "Expected allowlist egress helper to fail closed when dual-stack allowlist enforcement is unavailable" >&2
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

EGRESS_PLAN_OUTPUT="$("${ROOT_DIR}/scripts/colima-egress-allowlist.sh" plan default 'localhost:443')"
if ! echo "${EGRESS_PLAN_OUTPUT}" | grep -q 'iptables -A WORKCELL_EGRESS -p tcp -d 127\.0\.0\.1 --dport 443 -j ACCEPT'; then
  echo "Expected dual-stack egress plan to include the IPv4 localhost rule" >&2
  exit 1
fi
if ! echo "${EGRESS_PLAN_OUTPUT}" | grep -q 'ip6tables -A WORKCELL_EGRESS6 -p tcp -d ::1 --dport 443 -j ACCEPT'; then
  echo "Expected dual-stack egress plan to include the IPv6 localhost rule" >&2
  exit 1
fi
if ! echo "${EGRESS_PLAN_OUTPUT}" | grep -q 'ip6tables -A WORKCELL_EGRESS6 -j DROP'; then
  echo "Expected dual-stack egress plan to default-drop IPv6 traffic" >&2
  exit 1
fi
if echo "${EGRESS_PLAN_OUTPUT}" | grep -q 'disable_ipv6'; then
  echo "Dual-stack egress plan must not toggle kernel IPv6 disablement" >&2
  exit 1
fi

EGRESS_PLAN_IPV4_ONLY="$("${ROOT_DIR}/scripts/colima-egress-allowlist.sh" plan default '127.0.0.1:443')"
if ! echo "${EGRESS_PLAN_IPV4_ONLY}" | grep -q 'ip6tables -N WORKCELL_EGRESS6'; then
  echo "Expected IPv4-only allowlist plans to still install the IPv6 chain" >&2
  exit 1
fi
if ! echo "${EGRESS_PLAN_IPV4_ONLY}" | grep -q 'ip6tables -A WORKCELL_EGRESS6 -j DROP'; then
  echo "Expected IPv4-only allowlist plans to still default-drop IPv6 traffic" >&2
  exit 1
fi

EGRESS_PLAN_IPV6_LITERAL="$("${ROOT_DIR}/scripts/colima-egress-allowlist.sh" plan default '[::1]:443')"
if ! echo "${EGRESS_PLAN_IPV6_LITERAL}" | grep -q 'ip6tables -A WORKCELL_EGRESS6 -p tcp -d ::1 --dport 443 -j ACCEPT'; then
  echo "Expected bracketed IPv6 literal endpoints to produce an IPv6 allowlist rule" >&2
  exit 1
fi
if ! echo "${EGRESS_PLAN_IPV6_LITERAL}" | grep -q 'iptables -A WORKCELL_EGRESS -j DROP'; then
  echo "Expected bracketed IPv6 literal endpoints to still default-drop IPv4 traffic" >&2
  exit 1
fi
if ! rg -q 'run_in_vm "\$\(render_allowlist_apply_plan\)"' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "Expected dual-stack allowlist apply path to use the guarded apply plan" >&2
  exit 1
fi
if ! rg -q 'if ! type ip6tables >/dev/null 2>&1; then' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "Expected dual-stack allowlist apply plan to preflight ip6tables before rewriting rules" >&2
  exit 1
fi
if ! rg -q '^render_clear_plan\(\)' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "Expected dual-stack allowlist helper to render clear rules in the VM apply plan" >&2
  exit 1
fi
if ! sed -n '/^render_allowlist_apply_plan()/,/^}/p' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh" | grep -q 'render_clear_plan'; then
  echo "Expected dual-stack allowlist apply plan to include render_clear_plan" >&2
  exit 1
fi
if sed -n '/^render_allowlist_apply_plan()/,/^}/p' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh" | grep -q '^[[:space:]]*clear_rules$'; then
  echo "Expected dual-stack allowlist apply plan to avoid invoking clear_rules during render" >&2
  exit 1
fi
RUN_IN_VM_BLOCK="$(sed -n '/^run_in_vm()/,/^}/p' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh")"
if ! printf '%s\n' "${RUN_IN_VM_BLOCK}" | awk '
  /initialize_host_tools/ && !host_init { host_init = NR }
  /colima_home="\$\{COLIMA_HOME/ && !capture_home { capture_home = NR }
  /initialize_vm_tools/ && !vm_init { vm_init = NR }
  /set -euo pipefail/ && !vm_exec { vm_exec = NR }
  END { exit !(host_init && capture_home && vm_init && vm_exec && host_init < capture_home && vm_init < vm_exec) }
'; then
  echo "Expected run_in_vm to initialize host tools before the capture branch derives colima_home, and VM tools before real VM execution" >&2
  exit 1
fi
RUN_IN_VM_CAPTURE_DIR="$(mktemp -d)"
if ! "${ROOT_DIR}/scripts/colima-egress-allowlist.sh" \
  --test-run-in-vm-capture-dir "${RUN_IN_VM_CAPTURE_DIR}" \
  apply default '127.0.0.1:443 [::1]:443' >/dev/null 2>&1; then
  echo "Expected dual-stack allowlist apply path to succeed under the test VM capture hook" >&2
  exit 1
fi
if ! grep -q 'sudo iptables -A WORKCELL_EGRESS -p tcp -d 127.0.0.1 --dport 443 -j ACCEPT' "${RUN_IN_VM_CAPTURE_DIR}/apply-default.script"; then
  echo "Expected captured dual-stack apply script to include the IPv4 allowlist rule" >&2
  exit 1
fi
if ! grep -q 'sudo ip6tables -A WORKCELL_EGRESS6 -p tcp -d ::1 --dport 443 -j ACCEPT' "${RUN_IN_VM_CAPTURE_DIR}/apply-default.script"; then
  echo "Expected captured dual-stack apply script to include the IPv6 allowlist rule" >&2
  exit 1
fi
if ! grep -q "COLIMA_HOME=${REAL_HOME}/.colima" "${RUN_IN_VM_CAPTURE_DIR}/apply-default.env"; then
  echo "Expected captured dual-stack apply env to derive COLIMA_HOME from the real home directory" >&2
  exit 1
fi
if ! "${ROOT_DIR}/scripts/colima-egress-allowlist.sh" \
  --test-run-in-vm-capture-dir "${RUN_IN_VM_CAPTURE_DIR}" \
  clear default >/dev/null 2>&1; then
  echo "Expected dual-stack allowlist clear path to succeed under the test VM capture hook" >&2
  exit 1
fi
if ! grep -q 'sudo ip6tables -X WORKCELL_EGRESS6 2>/dev/null || true' "${RUN_IN_VM_CAPTURE_DIR}/clear-default.script"; then
  echo "Expected captured dual-stack clear script to remove the IPv6 chain" >&2
  exit 1
fi
if ! grep -q 'sudo iptables -X WORKCELL_EGRESS 2>/dev/null || true' "${RUN_IN_VM_CAPTURE_DIR}/clear-default.script"; then
  echo "Expected captured dual-stack clear script to remove the IPv4 chain" >&2
  exit 1
fi
rm -rf "${RUN_IN_VM_CAPTURE_DIR}"

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
  "${ROOT_DIR}/scripts/workcell" --agent codex --dry-run >/dev/null 2>&1; then
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
  --agent codex \
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

if "${ROOT_DIR}/scripts/workcell" --agent codex --mode strict --rebuild --dry-run >/tmp/workcell-strict-rebuild.out 2>&1; then
  echo "Expected strict mode to reject explicit --rebuild requests" >&2
  exit 1
fi
grep -q "strict mode does not rebuild or cold-bootstrap the runtime image" /tmp/workcell-strict-rebuild.out

if "${ROOT_DIR}/scripts/workcell" --agent codex --mode >/tmp/workcell-missing-mode.out 2>&1; then
  echo "Expected --mode without a value to fail cleanly" >&2
  exit 1
fi
grep -q "Option --mode requires a value." /tmp/workcell-missing-mode.out
grep -q '^Usage: workcell' /tmp/workcell-missing-mode.out

if "${ROOT_DIR}/scripts/workcell" --agent codex --workspace >/tmp/workcell-missing-workspace.out 2>&1; then
  echo "Expected --workspace without a value to fail cleanly" >&2
  exit 1
fi
grep -q "Option --workspace requires a value." /tmp/workcell-missing-workspace.out
grep -q '^Usage: workcell' /tmp/workcell-missing-workspace.out

if "${ROOT_DIR}/scripts/workcell" --agent codex --agent-autonomy >/tmp/workcell-missing-agent-autonomy.out 2>&1; then
  echo "Expected --agent-autonomy without a value to fail cleanly" >&2
  exit 1
fi
grep -q "Option --agent-autonomy requires a value." /tmp/workcell-missing-agent-autonomy.out
grep -q '^Usage: workcell' /tmp/workcell-missing-agent-autonomy.out

if "${ROOT_DIR}/scripts/workcell" --agent codex --agent-autonomy turbo --dry-run >/tmp/workcell-invalid-agent-autonomy.out 2>&1; then
  echo "Expected invalid --agent-autonomy values to fail cleanly" >&2
  exit 1
fi
grep -q "Unsupported agent autonomy mode: turbo" /tmp/workcell-invalid-agent-autonomy.out

if "${ROOT_DIR}/scripts/workcell" --agent codex --agent-arg >/tmp/workcell-missing-agent-arg.out 2>&1; then
  echo "Expected --agent-arg without a value to fail cleanly" >&2
  exit 1
fi
grep -q "Option --agent-arg requires a value." /tmp/workcell-missing-agent-arg.out
grep -q '^Usage: workcell' /tmp/workcell-missing-agent-arg.out

if "${ROOT_DIR}/scripts/workcell" --dry-run >/tmp/workcell-missing-agent.out 2>&1; then
  echo "Expected workcell without --agent to fail cleanly" >&2
  exit 1
fi
grep -q "Option --agent is required." /tmp/workcell-missing-agent.out
grep -q '^Usage: workcell' /tmp/workcell-missing-agent.out

STRICT_PREFLIGHT_WORKSPACE="${BARRIER_VERIFY_ROOT}/strict-preflight-workspace"
mkdir -p "${STRICT_PREFLIGHT_WORKSPACE}"
printf '# marker\n' >"${STRICT_PREFLIGHT_WORKSPACE}/AGENTS.md"
STRICT_PREFLIGHT_PROFILE="workcell-preflight-$$"
rm -rf \
  "${REAL_HOME}/.colima/${STRICT_PREFLIGHT_PROFILE}" \
  "${REAL_HOME}/.colima/_lima/colima-${STRICT_PREFLIGHT_PROFILE}"
if "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}/missing" \
  --dry-run >/tmp/workcell-missing-workspace.out 2>&1; then
  echo "Expected nonexistent workspace resolution to fail with a Workcell-owned diagnostic" >&2
  exit 1
fi
grep -q "Workspace path does not exist" /tmp/workcell-missing-workspace.out
grep -q -- '--workspace' /tmp/workcell-missing-workspace.out
if "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --allow-nongit-workspace \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}" >/tmp/workcell-strict-preflight.out 2>&1; then
  echo "Expected strict mode without a prepared image marker to fail fast before launch" >&2
  exit 1
fi
grep -q "No prepared runtime image is recorded for strict mode" /tmp/workcell-strict-preflight.out
grep -q -- '--prepare' /tmp/workcell-strict-preflight.out
if grep -q "starting colima" /tmp/workcell-strict-preflight.out; then
  echo "Strict preflight should fail before Colima startup when the prepared image marker is absent" >&2
  exit 1
fi

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --no-default-injection-policy \
  --allow-nongit-workspace \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}" \
  --dry-run >/tmp/workcell-dry-run-no-image.out 2>&1; then
  echo "Expected strict dry-run to work without a prepared image marker" >&2
  cat /tmp/workcell-dry-run-no-image.out >&2
  exit 1
fi
grep -q 'docker run' /tmp/workcell-dry-run-no-image.out
grep -q 'cache_profile=off' /tmp/workcell-dry-run-no-image.out
grep -q 'cache_assurance=managed-no-persistent-cache' /tmp/workcell-dry-run-no-image.out

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --no-default-injection-policy \
  --allow-nongit-workspace \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}" \
  --inspect >/tmp/workcell-inspect.out 2>&1; then
  echo "Expected --inspect to succeed without launching the runtime" >&2
  exit 1
fi
grep -q '^profile='"${STRICT_PREFLIGHT_PROFILE}"'$' /tmp/workcell-inspect.out
grep -q '^workspace_status=marker-only$' /tmp/workcell-inspect.out
grep -q '^cache_profile=off$' /tmp/workcell-inspect.out
grep -q '^cache_assurance=managed-no-persistent-cache$' /tmp/workcell-inspect.out
grep -q '^injection_policy=none$' /tmp/workcell-inspect.out
if ! "${ROOT_DIR}/scripts/workcell" \
  inspect \
  --agent codex \
  --no-default-injection-policy \
  --allow-nongit-workspace \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}" >/tmp/workcell-inspect-subcommand.out 2>&1; then
  echo "Expected inspect subcommand alias to succeed without launching the runtime" >&2
  exit 1
fi
grep -q '^profile='"${STRICT_PREFLIGHT_PROFILE}"'$' /tmp/workcell-inspect-subcommand.out

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --no-default-injection-policy \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-inspect" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}-missing-inspect" \
  --inspect >/tmp/workcell-inspect-missing.out 2>&1; then
  echo "Expected --inspect to succeed even when the workspace is missing" >&2
  exit 1
fi
grep -q '^profile='"${STRICT_PREFLIGHT_PROFILE}-missing-inspect"'$' /tmp/workcell-inspect-missing.out
grep -Eq '^workspace=.*/missing-workspace-for-inspect$' /tmp/workcell-inspect-missing.out
grep -q '^workspace_status=missing$' /tmp/workcell-inspect-missing.out

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --no-default-injection-policy \
  --allow-nongit-workspace \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}" \
  --doctor >/tmp/workcell-doctor.out 2>&1; then
  echo "Expected --doctor to succeed without launching the runtime" >&2
  exit 1
fi
grep -q '^doctor_profile_state=absent$' /tmp/workcell-doctor.out
grep -q '^doctor_missing_host_tools=none$' /tmp/workcell-doctor.out
grep -q '^doctor_prepared_image=0$' /tmp/workcell-doctor.out
grep -q -- '--prepare' /tmp/workcell-doctor.out
if ! "${ROOT_DIR}/scripts/workcell" \
  doctor \
  --agent codex \
  --no-default-injection-policy \
  --allow-nongit-workspace \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}" >/tmp/workcell-doctor-subcommand.out 2>&1; then
  echo "Expected doctor subcommand alias to succeed without launching the runtime" >&2
  exit 1
fi
grep -q '^doctor_profile_state=absent$' /tmp/workcell-doctor-subcommand.out

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --no-default-injection-policy \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-doctor" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}-missing-doctor" \
  --doctor >/tmp/workcell-doctor-missing.out 2>&1; then
  echo "Expected --doctor to succeed even when the workspace is missing" >&2
  exit 1
fi
grep -q '^doctor_profile_state=absent$' /tmp/workcell-doctor-missing.out
grep -Eq '^workspace=.*/missing-workspace-for-doctor$' /tmp/workcell-doctor-missing.out
grep -q '^workspace_status=missing$' /tmp/workcell-doctor-missing.out
grep -q '^doctor_recommended_next=fix-workspace$' /tmp/workcell-doctor-missing.out

STALE_MARKER_PROFILE="${STRICT_PREFLIGHT_PROFILE}-stale"
STALE_MARKER_DIR="${REAL_HOME}/.colima/${STALE_MARKER_PROFILE}"
rm -rf "${STALE_MARKER_DIR}" "${REAL_HOME}/.colima/_lima/colima-${STALE_MARKER_PROFILE}"
mkdir -p "${STALE_MARKER_DIR}"
printf '%s\n' "${STRICT_PREFLIGHT_WORKSPACE}" >"${STALE_MARKER_DIR}/workcell.managed"
cat >"${STALE_MARKER_DIR}/workcell.image-ready" <<'EOF'
image_tag=workcell:local
image_id=sha256:stale
source_date_epoch=0
EOF
if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --no-default-injection-policy \
  --allow-nongit-workspace \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --colima-profile "${STALE_MARKER_PROFILE}" \
  --doctor >/tmp/workcell-doctor-stale.out 2>&1; then
  echo "Expected stale-marker --doctor to succeed without launching the runtime" >&2
  exit 1
fi
grep -q '^current_image_id=none$' /tmp/workcell-doctor-stale.out
grep -q '^doctor_prepared_image=0$' /tmp/workcell-doctor-stale.out
grep -q -- '--prepare' /tmp/workcell-doctor-stale.out
rm -rf "${STALE_MARKER_DIR}" "${REAL_HOME}/.colima/_lima/colima-${STALE_MARKER_PROFILE}"

DEBUG_LOG_CAPTURE="${BARRIER_VERIFY_ROOT}/debug/session.log"
DEBUG_LOG_PROFILE="${STRICT_PREFLIGHT_PROFILE}-logs"
rm -rf "$(dirname "${DEBUG_LOG_CAPTURE}")"
if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --allow-nongit-workspace \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}" \
  --debug-log "${DEBUG_LOG_CAPTURE}" \
  --dry-run >/tmp/workcell-debug-log.out 2>&1; then
  echo "Expected --debug-log dry-run to succeed" >&2
  exit 1
fi
test -f "${DEBUG_LOG_CAPTURE}"
test "$(file_mode_octal "${DEBUG_LOG_CAPTURE}")" = "600"
grep -q 'Workcell warning: full host-persisted debug log capture is enabled for this session:' /tmp/workcell-debug-log.out
grep -q 'execution_path=' "${DEBUG_LOG_CAPTURE}"
DEBUG_LOG_SYMLINK_TARGET="${BARRIER_VERIFY_ROOT}/debug/redirected.log"
DEBUG_LOG_SYMLINK="${BARRIER_VERIFY_ROOT}/debug/symlink.log"
rm -f "${DEBUG_LOG_SYMLINK_TARGET}" "${DEBUG_LOG_SYMLINK}"
printf 'seed\n' >"${DEBUG_LOG_SYMLINK_TARGET}"
ln -s "${DEBUG_LOG_SYMLINK_TARGET}" "${DEBUG_LOG_SYMLINK}"
if "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --allow-nongit-workspace \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}" \
  --debug-log "${DEBUG_LOG_SYMLINK}" \
  --dry-run >/tmp/workcell-debug-log-symlink.out 2>&1; then
  echo "Expected --debug-log to reject symlinked host output paths" >&2
  exit 1
fi
grep -q 'Refusing symlinked host output path component:' /tmp/workcell-debug-log-symlink.out
mkdir -p "${REAL_HOME}/.colima/${DEBUG_LOG_PROFILE}"
printf '%s\n' "${DEBUG_LOG_CAPTURE}" >"${REAL_HOME}/.colima/${DEBUG_LOG_PROFILE}/workcell.latest-debug-log"
if ! "${ROOT_DIR}/scripts/workcell" \
  --logs debug \
  --colima-profile "${DEBUG_LOG_PROFILE}" >/tmp/workcell-logs-debug.out 2>&1; then
  echo "Expected --logs debug to print the latest retained debug log" >&2
  exit 1
fi
grep -q 'execution_path=' /tmp/workcell-logs-debug.out

TRANSCRIPT_CAPTURE="${BARRIER_VERIFY_ROOT}/debug/session.transcript"
TRANSCRIPT_LOG_PROFILE="${STRICT_PREFLIGHT_PROFILE}-transcript-logs"
if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --allow-nongit-workspace \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}" \
  --audit-transcript "${TRANSCRIPT_CAPTURE}" \
  --dry-run >/tmp/workcell-transcript.out 2>&1; then
  echo "Expected --audit-transcript dry-run to succeed" >&2
  exit 1
fi
printf 'sample transcript line\n' >"${TRANSCRIPT_CAPTURE}"
mkdir -p "${REAL_HOME}/.colima/${TRANSCRIPT_LOG_PROFILE}"
printf '%s\n' "${TRANSCRIPT_CAPTURE}" >"${REAL_HOME}/.colima/${TRANSCRIPT_LOG_PROFILE}/workcell.latest-transcript-log"
if ! "${ROOT_DIR}/scripts/workcell" \
  --logs transcript \
  --colima-profile "${TRANSCRIPT_LOG_PROFILE}" >/tmp/workcell-logs-transcript.out 2>&1; then
  echo "Expected --logs transcript to print the latest retained transcript log" >&2
  exit 1
fi
grep -q 'sample transcript line' /tmp/workcell-logs-transcript.out
if ! "${ROOT_DIR}/scripts/workcell" \
  logs transcript \
  --colima-profile "${TRANSCRIPT_LOG_PROFILE}" >/tmp/workcell-logs-transcript-subcommand.out 2>&1; then
  echo "Expected logs subcommand alias to print the latest retained transcript log" >&2
  exit 1
fi
grep -q 'sample transcript line' /tmp/workcell-logs-transcript-subcommand.out
if ! "${ROOT_DIR}/scripts/workcell" logs --help >/tmp/workcell-logs-help.out 2>&1; then
  echo "Expected logs subcommand help to succeed" >&2
  exit 1
fi
grep -q 'Print the latest retained log of the selected type' /tmp/workcell-logs-help.out
if ! "${ROOT_DIR}/scripts/workcell" \
  --logs transcript \
  --colima-profile "${TRANSCRIPT_LOG_PROFILE}" \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-logs" >/tmp/workcell-logs-transcript-missing-workspace.out 2>&1; then
  echo "Expected --logs transcript to ignore a nonexistent workspace path" >&2
  exit 1
fi
grep -q 'sample transcript line' /tmp/workcell-logs-transcript-missing-workspace.out
rm -rf "${REAL_HOME}/.colima/${DEBUG_LOG_PROFILE}" "${REAL_HOME}/.colima/${TRANSCRIPT_LOG_PROFILE}"

AUTH_STATUS_ROOT="${BARRIER_VERIFY_ROOT}/auth-status"
mkdir -p "${AUTH_STATUS_ROOT}"
printf '{}\n' >"${AUTH_STATUS_ROOT}/auth.json"
chmod 0600 "${AUTH_STATUS_ROOT}/auth.json"
printf '{"token":"claude-auth"}\n' >"${AUTH_STATUS_ROOT}/claude-auth.json"
chmod 0600 "${AUTH_STATUS_ROOT}/claude-auth.json"
printf 'claude-key\n' >"${AUTH_STATUS_ROOT}/claude-api-key.txt"
chmod 0600 "${AUTH_STATUS_ROOT}/claude-api-key.txt"
printf 'GEMINI_API_KEY=verify-gemini-key\n' >"${AUTH_STATUS_ROOT}/gemini.env"
chmod 0600 "${AUTH_STATUS_ROOT}/gemini.env"
printf '{"type":"authorized_user"}\n' >"${AUTH_STATUS_ROOT}/gcloud-adc.json"
chmod 0600 "${AUTH_STATUS_ROOT}/gcloud-adc.json"
cat >"${AUTH_STATUS_ROOT}/hosts.yml" <<'EOF'
github.com:
  oauth_token: test-token
EOF
chmod 0600 "${AUTH_STATUS_ROOT}/hosts.yml"
cat >"${AUTH_STATUS_ROOT}/ssh-config" <<'EOF'
ProxyCommand nc %h %p
EOF
chmod 0600 "${AUTH_STATUS_ROOT}/ssh-config"
cat >"${AUTH_STATUS_ROOT}/policy.toml" <<'EOF'
version = 1
[credentials]
codex_auth = "auth.json"
claude_auth = "claude-auth.json"
claude_api_key = "claude-api-key.txt"
gemini_env = "gemini.env"
gcloud_adc = "gcloud-adc.json"
[credentials.github_hosts]
source = "hosts.yml"
providers = ["codex", "claude", "gemini"]
[ssh]
enabled = true
config = "ssh-config"
allow_unsafe_config = true
EOF
cat >"${AUTH_STATUS_ROOT}/gemini.env" <<'EOF'
GOOGLE_CLOUD_LOCATION=us-central1
EOF
chmod 0600 "${AUTH_STATUS_ROOT}/gemini.env"
if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${AUTH_STATUS_ROOT}/policy.toml" \
  --auth-status >/tmp/workcell-auth-status.out 2>&1; then
  echo "Expected --auth-status to succeed" >&2
  exit 1
fi
grep -Eq '^credential_keys=(codex_auth,github_hosts|github_hosts,codex_auth)$' /tmp/workcell-auth-status.out
grep -q '^provider_auth_mode=codex_auth$' /tmp/workcell-auth-status.out
grep -q '^provider_auth_modes=codex_auth$' /tmp/workcell-auth-status.out
grep -q '^shared_auth_modes=github_hosts$' /tmp/workcell-auth-status.out
grep -q '^github_auth_present=1$' /tmp/workcell-auth-status.out
grep -q '^ssh_injected=1$' /tmp/workcell-auth-status.out
grep -q '^ssh_config_assurance=lower-assurance-unsafe-config$' /tmp/workcell-auth-status.out
if ! "${ROOT_DIR}/scripts/workcell" \
  auth-status \
  --agent codex \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${AUTH_STATUS_ROOT}/policy.toml" >/tmp/workcell-auth-status-subcommand.out 2>&1; then
  echo "Expected auth-status subcommand alias to succeed" >&2
  exit 1
fi
grep -q '^provider_auth_mode=codex_auth$' /tmp/workcell-auth-status-subcommand.out
grep -q '^shared_auth_modes=github_hosts$' /tmp/workcell-auth-status-subcommand.out
if ! "${ROOT_DIR}/scripts/workcell" \
  --agent claude \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${AUTH_STATUS_ROOT}/policy.toml" \
  --auth-status >/tmp/workcell-auth-status-claude.out 2>&1; then
  echo "Expected Claude --auth-status to succeed" >&2
  exit 1
fi
grep -q '^provider_auth_mode=claude_api_key$' /tmp/workcell-auth-status-claude.out
grep -q '^provider_auth_modes=claude_api_key,claude_auth$' /tmp/workcell-auth-status-claude.out
grep -q '^shared_auth_modes=github_hosts$' /tmp/workcell-auth-status-claude.out
grep -q '^github_auth_present=1$' /tmp/workcell-auth-status-claude.out
if ! "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${AUTH_STATUS_ROOT}/policy.toml" \
  --auth-status >/tmp/workcell-auth-status-gemini.out 2>&1; then
  echo "Expected Gemini --auth-status to succeed" >&2
  exit 1
fi
grep -q '^provider_auth_mode=gemini_env$' /tmp/workcell-auth-status-gemini.out
grep -q '^provider_auth_modes=gemini_env,gcloud_adc$' /tmp/workcell-auth-status-gemini.out
grep -q '^shared_auth_modes=github_hosts$' /tmp/workcell-auth-status-gemini.out
grep -q '^github_auth_present=1$' /tmp/workcell-auth-status-gemini.out
if ! "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --workspace "${ROOT_DIR}" \
  --injection-policy "${AUTH_STATUS_ROOT}/policy.toml" \
  --dry-run >/tmp/workcell-gemini-network.stdout 2>/tmp/workcell-gemini-network.stderr; then
  echo "Expected Gemini dry-run with scoped auth policy to succeed" >&2
  exit 1
fi
grep -q 'accounts.google.com:443' /tmp/workcell-gemini-network.stderr
grep -q 'api.github.com:443' /tmp/workcell-gemini-network.stderr
grep -q 'us-central1-aiplatform.googleapis.com:443' /tmp/workcell-gemini-network.stderr
grep -q -- '--add-host accounts.google.com:' /tmp/workcell-gemini-network.stdout
grep -q -- '--add-host us-central1-aiplatform.googleapis.com:' /tmp/workcell-gemini-network.stdout

BROKEN_DEBUG_POINTER_PROFILE="${STRICT_PREFLIGHT_PROFILE}-broken-debug-pointer"
mkdir -p "${REAL_HOME}/.colima/${BROKEN_DEBUG_POINTER_PROFILE}"
printf '%s\n' "${BARRIER_VERIFY_ROOT}/missing-debug.log" >"${REAL_HOME}/.colima/${BROKEN_DEBUG_POINTER_PROFILE}/workcell.latest-debug-log"
if "${ROOT_DIR}/scripts/workcell" \
  --inspect \
  --agent codex \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --debug-log "${BARRIER_VERIFY_ROOT}/debug/nonlaunch.log" >/tmp/workcell-nonlaunch-debug-log.out 2>&1; then
  echo "Expected non-launch --inspect to reject --debug-log" >&2
  exit 1
fi
grep -q -- '--debug-log and --audit-transcript apply only to launched sessions.' /tmp/workcell-nonlaunch-debug-log.out

if ! "${ROOT_DIR}/scripts/workcell" --gc --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-gc" >/tmp/workcell-gc.out 2>&1; then
  echo "Expected --gc to succeed" >&2
  exit 1
fi
grep -q 'Cleaned stale Workcell injection, session-audit, and broken latest-log pointer state.' /tmp/workcell-gc.out
test ! -f "${REAL_HOME}/.colima/${BROKEN_DEBUG_POINTER_PROFILE}/workcell.latest-debug-log"
if ! "${ROOT_DIR}/scripts/workcell" gc --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-gc" >/tmp/workcell-gc-subcommand.out 2>&1; then
  echo "Expected gc subcommand alias to succeed" >&2
  exit 1
fi

PREMERGE_HARNESS_ROOT="${BARRIER_VERIFY_ROOT}/premerge-harness"
PREMERGE_FAKEBIN="${PREMERGE_HARNESS_ROOT}/fakebin"
PREMERGE_LOG="${PREMERGE_HARNESS_ROOT}/premerge.log"
rm -rf "${PREMERGE_HARNESS_ROOT}"
mkdir -p "${PREMERGE_HARNESS_ROOT}/scripts" "${PREMERGE_HARNESS_ROOT}/tools/validator" "${PREMERGE_FAKEBIN}"
install -m 0755 "${ROOT_DIR}/scripts/pre-merge.sh" "${PREMERGE_HARNESS_ROOT}/scripts/pre-merge.sh"
cat >"${PREMERGE_HARNESS_ROOT}/tools/validator/Dockerfile" <<'EOF'
FROM scratch
EOF
for stub in \
  check-pinned-inputs.sh \
  verify-upstream-codex-release.sh \
  check-workflows.sh \
  validate-repo.sh \
  verify-invariants.sh \
  container-smoke.sh \
  verify-release-bundle.sh \
  verify-reproducible-build.sh \
  dev-remote-validate.sh; do
  cat >"${PREMERGE_HARNESS_ROOT}/scripts/${stub}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s %s\n' "$(basename "$0")" "$*" >>"${PREMERGE_LOG}"
EOF
  chmod 0755 "${PREMERGE_HARNESS_ROOT}/scripts/${stub}"
done
cat >"${PREMERGE_FAKEBIN}/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1-}" == "-C" ]]; then
  shift 2
fi
case "${1-}" in
  status)
    printf '%s' "${WORKCELL_FAKE_GIT_STATUS_OUTPUT:-}"
    ;;
  log)
    printf '%s\n' "${WORKCELL_FAKE_GIT_EPOCH:-1700000000}"
    ;;
  *)
    echo "unexpected git invocation: $*" >&2
    exit 1
    ;;
esac
EOF
chmod 0755 "${PREMERGE_FAKEBIN}/git"
cat >"${PREMERGE_FAKEBIN}/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'docker %s\n' "$*" >>"${PREMERGE_LOG}"
if [[ "${1-}" == "image" && "${2-}" == "inspect" ]]; then
  exit 1
fi
exit 0
EOF
chmod 0755 "${PREMERGE_FAKEBIN}/docker"

if PATH="${PREMERGE_FAKEBIN}:${PATH}" \
  PREMERGE_LOG="${PREMERGE_LOG}" \
  WORKCELL_FAKE_GIT_STATUS_OUTPUT='?? stray.txt' \
  "${PREMERGE_HARNESS_ROOT}/scripts/pre-merge.sh" >/tmp/workcell-premerge-dirty.out 2>&1; then
  echo "Expected pre-merge to reject a dirty worktree by default" >&2
  exit 1
fi
grep -q 'clean worktree, including untracked files' /tmp/workcell-premerge-dirty.out

: >"${PREMERGE_LOG}"
if ! PATH="${PREMERGE_FAKEBIN}:${PATH}" \
  PREMERGE_LOG="${PREMERGE_LOG}" \
  WORKCELL_FAKE_GIT_STATUS_OUTPUT=$' M README.md\n?? stray.txt\n' \
  "${PREMERGE_HARNESS_ROOT}/scripts/pre-merge.sh" \
  --allow-dirty \
  --remote >/tmp/workcell-premerge-allow-dirty.out 2>&1; then
  echo "Expected --allow-dirty --remote pre-merge harness to succeed" >&2
  cat /tmp/workcell-premerge-allow-dirty.out >&2
  exit 1
fi
grep -q 'remote validation will use --remote-snapshot worktree --include-untracked' /tmp/workcell-premerge-allow-dirty.out
grep -q 'dev-remote-validate.sh --snapshot worktree --include-untracked --check validate' "${PREMERGE_LOG}"

: >"${PREMERGE_LOG}"
if ! PATH="${PREMERGE_FAKEBIN}:${PATH}" \
  PREMERGE_LOG="${PREMERGE_LOG}" \
  WORKCELL_FAKE_GIT_STATUS_OUTPUT=$' M README.md\n?? stray.txt\n' \
  "${PREMERGE_HARNESS_ROOT}/scripts/pre-merge.sh" \
  --allow-dirty \
  --remote \
  --remote-snapshot index >/tmp/workcell-premerge-remote-index.out 2>&1; then
  echo "Expected explicit remote snapshot pre-merge harness to succeed" >&2
  cat /tmp/workcell-premerge-remote-index.out >&2
  exit 1
fi
grep -q 'warning: --allow-dirty validates the live worktree locally, but remote validation will use --remote-snapshot index.' /tmp/workcell-premerge-remote-index.out
grep -q 'dev-remote-validate.sh --snapshot index --check validate' "${PREMERGE_LOG}"

: >"${PREMERGE_LOG}"
if ! PATH="${PREMERGE_FAKEBIN}:${PATH}" \
  PREMERGE_LOG="${PREMERGE_LOG}" \
  WORKCELL_FAKE_GIT_STATUS_OUTPUT=$' M README.md\n?? stray.txt\n' \
  "${PREMERGE_HARNESS_ROOT}/scripts/pre-merge.sh" \
  --allow-dirty \
  --remote \
  --remote-snapshot worktree >/tmp/workcell-premerge-remote-worktree.out 2>&1; then
  echo "Expected explicit worktree remote snapshot pre-merge harness to succeed" >&2
  cat /tmp/workcell-premerge-remote-worktree.out >&2
  exit 1
fi
grep -q 'local validation sees untracked files, but remote worktree validation will exclude them without --include-untracked.' /tmp/workcell-premerge-remote-worktree.out

: >"${PREMERGE_LOG}"
if ! PATH="${PREMERGE_FAKEBIN}:${PATH}" \
  PREMERGE_LOG="${PREMERGE_LOG}" \
  WORKCELL_FAKE_GIT_STATUS_OUTPUT=$' M README.md\n?? stray.txt\n' \
  "${PREMERGE_HARNESS_ROOT}/scripts/pre-merge.sh" \
  --allow-dirty \
  --remote-heavy >/tmp/workcell-premerge-remote-heavy.out 2>&1; then
  echo "Expected explicit heavy remote pre-merge harness to succeed" >&2
  cat /tmp/workcell-premerge-remote-heavy.out >&2
  exit 1
fi
grep -q 'dev-remote-validate.sh --snapshot worktree --include-untracked --check validate --allow-shared-daemon-heavy-checks --check smoke --check repro --check release-bundle' "${PREMERGE_LOG}"

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --prepare \
  --allow-nongit-workspace \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}" \
  --dry-run >/tmp/workcell-prepare-dry-run.out 2>&1; then
  echo "Expected --prepare dry-run to continue working" >&2
  cat /tmp/workcell-prepare-dry-run.out >&2
  exit 1
fi
grep -q 'docker run' /tmp/workcell-prepare-dry-run.out

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --prepare-only \
  --allow-nongit-workspace \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}" \
  --dry-run >/tmp/workcell-prepare-only-dry-run.out 2>&1; then
  echo "Expected --prepare-only dry-run to succeed" >&2
  cat /tmp/workcell-prepare-only-dry-run.out >&2
  exit 1
fi
grep -q '^prepare_only=1 no_session_launch=1$' /tmp/workcell-prepare-only-dry-run.out

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --mode strict \
  --dry-run >/tmp/workcell-default-autonomy-dry-run.stdout 2>/tmp/workcell-default-autonomy-dry-run.stderr; then
  echo "Expected default autonomy dry-run to succeed" >&2
  exit 1
fi
grep -q 'agent_autonomy=yolo' /tmp/workcell-default-autonomy-dry-run.stderr
grep -q 'container_assurance=managed-mutable' /tmp/workcell-default-autonomy-dry-run.stderr
grep -q 'autonomy_assurance=managed-yolo' /tmp/workcell-default-autonomy-dry-run.stderr
grep -q 'codex_rules_mutability_configured=readonly' /tmp/workcell-default-autonomy-dry-run.stderr
grep -q 'codex_rules_assurance_configured=managed-immutable-rules' /tmp/workcell-default-autonomy-dry-run.stderr
grep -q 'codex_rules_mutability_effective_initial=readonly' /tmp/workcell-default-autonomy-dry-run.stderr
grep -q 'codex_rules_assurance_effective_initial=managed-immutable-rules' /tmp/workcell-default-autonomy-dry-run.stderr
grep -q 'session_assurance_initial=managed-mutable' /tmp/workcell-default-autonomy-dry-run.stderr
grep -q 'WORKCELL_AGENT_AUTONOMY=yolo' /tmp/workcell-default-autonomy-dry-run.stdout
grep -q 'WORKCELL_CODEX_RULES_MUTABILITY=readonly' /tmp/workcell-default-autonomy-dry-run.stdout
grep -q -- '--cap-drop ALL' /tmp/workcell-default-autonomy-dry-run.stdout
grep -q -- '--cap-add SETUID' /tmp/workcell-default-autonomy-dry-run.stdout
grep -q -- '--cap-add SETGID' /tmp/workcell-default-autonomy-dry-run.stdout

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --agent-autonomy prompt \
  --agent-arg --version \
  --dry-run >/tmp/workcell-prompt-autonomy-dry-run.stdout 2>/tmp/workcell-prompt-autonomy-dry-run.stderr; then
  echo "Expected prompt autonomy dry-run with --agent-arg to succeed" >&2
  cat /tmp/workcell-prompt-autonomy-dry-run.stderr >&2
  exit 1
fi
grep -q 'agent_autonomy=prompt' /tmp/workcell-prompt-autonomy-dry-run.stderr
grep -q 'container_assurance=managed-mutable' /tmp/workcell-prompt-autonomy-dry-run.stderr
grep -q 'autonomy_assurance=lower-assurance-prompt-autonomy' /tmp/workcell-prompt-autonomy-dry-run.stderr
grep -q 'codex_rules_mutability_configured=readonly' /tmp/workcell-prompt-autonomy-dry-run.stderr
grep -q 'codex_rules_assurance_configured=managed-immutable-rules' /tmp/workcell-prompt-autonomy-dry-run.stderr
grep -q 'codex_rules_mutability_effective_initial=session' /tmp/workcell-prompt-autonomy-dry-run.stderr
grep -q 'codex_rules_assurance_effective_initial=lower-assurance-session-rules' /tmp/workcell-prompt-autonomy-dry-run.stderr
grep -q 'session_assurance_initial=managed-mutable' /tmp/workcell-prompt-autonomy-dry-run.stderr
grep -q 'WORKCELL_AGENT_AUTONOMY=prompt' /tmp/workcell-prompt-autonomy-dry-run.stdout
grep -q 'workcell:local codex --version' /tmp/workcell-prompt-autonomy-dry-run.stdout

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --codex-rules-mutability session \
  --agent-arg --version \
  --dry-run >/tmp/workcell-codex-session-rules-dry-run.stdout 2>/tmp/workcell-codex-session-rules-dry-run.stderr; then
  echo "Expected session Codex rules mutability dry-run to succeed" >&2
  cat /tmp/workcell-codex-session-rules-dry-run.stderr >&2
  exit 1
fi
grep -q 'codex_rules_mutability_configured=session' /tmp/workcell-codex-session-rules-dry-run.stderr
grep -q 'codex_rules_assurance_configured=lower-assurance-session-rules' /tmp/workcell-codex-session-rules-dry-run.stderr
grep -q 'codex_rules_mutability_effective_initial=session' /tmp/workcell-codex-session-rules-dry-run.stderr
grep -q 'codex_rules_assurance_effective_initial=lower-assurance-session-rules' /tmp/workcell-codex-session-rules-dry-run.stderr
grep -q 'WORKCELL_CODEX_RULES_MUTABILITY=session' /tmp/workcell-codex-session-rules-dry-run.stdout

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent claude \
  --agent-arg --version \
  --dry-run >/tmp/workcell-claude-agent-arg-dry-run.stdout 2>/tmp/workcell-claude-agent-arg-dry-run.stderr; then
  echo "Expected Claude --agent-arg dry-run to succeed" >&2
  cat /tmp/workcell-claude-agent-arg-dry-run.stderr >&2
  exit 1
fi
grep -q 'agent_autonomy=yolo' /tmp/workcell-claude-agent-arg-dry-run.stderr
grep -q 'workcell:local claude --version' /tmp/workcell-claude-agent-arg-dry-run.stdout

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --agent-arg --version \
  --dry-run >/tmp/workcell-gemini-agent-arg-dry-run.stdout 2>/tmp/workcell-gemini-agent-arg-dry-run.stderr; then
  echo "Expected Gemini --agent-arg dry-run to succeed" >&2
  cat /tmp/workcell-gemini-agent-arg-dry-run.stderr >&2
  exit 1
fi
grep -q 'agent_autonomy=yolo' /tmp/workcell-gemini-agent-arg-dry-run.stderr
grep -q 'workcell:local gemini --version' /tmp/workcell-gemini-agent-arg-dry-run.stdout

DRY_RUN_OUTPUT="$("${ROOT_DIR}/scripts/workcell" --agent codex --mode strict --dry-run 2>/dev/null)"
SECOND_DRY_RUN_OUTPUT="$("${ROOT_DIR}/scripts/workcell" --agent codex --mode strict --dry-run 2>/dev/null)"
DRY_RUN_CONTAINER_NAME="$(printf '%s\n' "${DRY_RUN_OUTPUT}" | sed -n 's/.*--name \([^ ]*\).*/\1/p' | head -n1)"
SECOND_DRY_RUN_CONTAINER_NAME="$(printf '%s\n' "${SECOND_DRY_RUN_OUTPUT}" | sed -n 's/.*--name \([^ ]*\).*/\1/p' | head -n1)"
if [[ -z "${DRY_RUN_CONTAINER_NAME}" ]] || [[ -z "${SECOND_DRY_RUN_CONTAINER_NAME}" ]]; then
  echo "Expected workcell --dry-run to expose a managed container name" >&2
  exit 1
fi
if [[ "${DRY_RUN_CONTAINER_NAME}" == "${SECOND_DRY_RUN_CONTAINER_NAME}" ]]; then
  echo "Expected repeated workcell --dry-run launches to use unique container names per session" >&2
  exit 1
fi

MASK_VERIFY_WORKSPACE="${BARRIER_VERIFY_ROOT}/mask-workspace"
mkdir -p "${MASK_VERIFY_WORKSPACE}/nested/.claude"
git init -q "${MASK_VERIFY_WORKSPACE}"
printf '# root agent marker\n' >"${MASK_VERIFY_WORKSPACE}/AGENTS.md"
mkdir -p "${MASK_VERIFY_WORKSPACE}/.codex"
printf 'profile = "strict"\n' >"${MASK_VERIFY_WORKSPACE}/.codex/config.toml"
printf '# nested agent marker\n' >"${MASK_VERIFY_WORKSPACE}/nested/AGENTS.md"
printf '{\n  "masked": true\n}\n' >"${MASK_VERIFY_WORKSPACE}/nested/.claude/settings.json"
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

for required in "/workspace/nested/.claude:ro" "/workspace/.alt/.git/config:ro"; do
  if ! echo "${MASK_DRY_RUN_OUTPUT}" | grep -q -- "${required}"; then
    echo "Missing nested workspace control-plane masking mount in dry-run output: ${required}" >&2
    exit 1
  fi
done

if echo "${MASK_DRY_RUN_OUTPUT}" | grep -q -- "/workspace/nested/AGENTS.md:ro"; then
  echo "Nested AGENTS.md should remain visible in the workspace for path-scoped agent instructions" >&2
  exit 1
fi

mkdir -p "${MASK_VERIFY_WORKSPACE}/symlinked"
ln -s "${REAL_HOME}/.ssh/config" "${MASK_VERIFY_WORKSPACE}/symlinked/GEMINI.md"
if "${ROOT_DIR}/scripts/workcell" --agent gemini --mode strict --workspace "${MASK_VERIFY_WORKSPACE}" --dry-run >/tmp/workcell-symlinked-doc.out 2>&1; then
  echo "Expected symlinked workspace control docs to be rejected" >&2
  exit 1
fi
grep -q 'Workcell refuses symlinked workspace control files' /tmp/workcell-symlinked-doc.out

SHADOW_SYMLINK_REPO="${BARRIER_VERIFY_ROOT}/shadow-symlink-repo"
git init -q "${SHADOW_SYMLINK_REPO}"
git -C "${SHADOW_SYMLINK_REPO}" config user.name "Workcell Verify"
git -C "${SHADOW_SYMLINK_REPO}" config user.email "workcell-verify@example.com"
touch "${SHADOW_SYMLINK_REPO}/tracked.txt"
git -C "${SHADOW_SYMLINK_REPO}" add tracked.txt
git -C "${SHADOW_SYMLINK_REPO}" commit -q -m init
mkdir -p "${SHADOW_SYMLINK_REPO}/.git/hooks"
mkdir -p "${SHADOW_SYMLINK_REPO}/external-hooks-dir" "${SHADOW_SYMLINK_REPO}/external-worktrees"
printf '#!/bin/sh\nexit 0\n' >"${SHADOW_SYMLINK_REPO}/external-hook.sh"
chmod 0755 "${SHADOW_SYMLINK_REPO}/external-hook.sh"
printf '[core]\nrepositoryformatversion = 0\n' >"${SHADOW_SYMLINK_REPO}/external-config"
ln -sf "${SHADOW_SYMLINK_REPO}/external-hook.sh" "${SHADOW_SYMLINK_REPO}/.git/hooks/post-commit"
mkdir -p "${SHADOW_SYMLINK_REPO}/.git/modules/demo"
ln -sf "${SHADOW_SYMLINK_REPO}/external-config" "${SHADOW_SYMLINK_REPO}/.git/modules/demo/config"
ln -sf "${SHADOW_SYMLINK_REPO}/external-hooks-dir" "${SHADOW_SYMLINK_REPO}/.git/modules/demo/hooks"
ln -sf "${SHADOW_SYMLINK_REPO}/external-worktrees" "${SHADOW_SYMLINK_REPO}/.git/modules/demo/worktrees"
SHADOW_SYMLINK_DRY_RUN_OUTPUT="$("${ROOT_DIR}/scripts/workcell" --agent codex --mode strict --workspace "${SHADOW_SYMLINK_REPO}" --dry-run 2>/dev/null)"
for required in \
  "/workspace/.git/hooks:ro" \
  "/workspace/.git/modules/demo/config:ro" \
  "/workspace/.git/modules/demo/hooks:ro" \
  "/workspace/.git/modules/demo/worktrees:ro"; do
  if ! echo "${SHADOW_SYMLINK_DRY_RUN_OUTPUT}" | grep -q -- "${required}"; then
    echo "Expected symlinked git control-plane entry to be masked by a readonly shadow mount: ${required}" >&2
    exit 1
  fi
done

for forbidden in "github.com:443" "api.github.com:443" "objects.githubusercontent.com:443" "raw.githubusercontent.com:443"; do
  if echo "${DRY_RUN_OUTPUT}" | grep -q "${forbidden}"; then
    echo "Unexpected strict-mode egress allowance in dry-run output: ${forbidden}" >&2
    exit 1
  fi
done

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --mode strict \
  --container-mutability readonly \
  --container-cpu 2 \
  --container-memory 3g \
  --vm-cpu 4 \
  --vm-memory 8 \
  --vm-disk 80 \
  --dry-run >/tmp/workcell-resource-tunables.stdout 2>/tmp/workcell-resource-tunables.stderr; then
  echo "Expected resource-tunable dry-run to succeed" >&2
  cat /tmp/workcell-resource-tunables.stderr >&2
  exit 1
fi
grep -q 'vm_resources=cpu=4 memory_gib=8 disk_gib=80' /tmp/workcell-resource-tunables.stderr
grep -q 'container_resources=mutability=readonly cpu=2 memory=3g' /tmp/workcell-resource-tunables.stderr
grep -q 'container_assurance=managed-readonly' /tmp/workcell-resource-tunables.stderr
grep -q 'autonomy_assurance=managed-yolo' /tmp/workcell-resource-tunables.stderr
grep -q 'session_assurance_initial=managed-readonly' /tmp/workcell-resource-tunables.stderr
grep -q 'WORKCELL_CONTAINER_MUTABILITY=readonly' /tmp/workcell-resource-tunables.stdout
grep -q -- '--cpus 2' /tmp/workcell-resource-tunables.stdout
grep -q -- '--memory 3g' /tmp/workcell-resource-tunables.stdout
grep -q -- '--cap-drop ALL' /tmp/workcell-resource-tunables.stdout
if grep -q -- '--cap-add SETUID' /tmp/workcell-resource-tunables.stdout; then
  echo "Readonly dry-run should not add mutable-session handoff capabilities" >&2
  exit 1
fi
if grep -q -- '--cap-add SETGID' /tmp/workcell-resource-tunables.stdout; then
  echo "Readonly dry-run should not add mutable-session handoff capabilities" >&2
  exit 1
fi

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

ARBITRARY_DRY_RUN_OUTPUT="$("${ROOT_DIR}/scripts/workcell" --agent codex --prepare --allow-arbitrary-command --ack-arbitrary-command --dry-run -- bash -lc true 2>/dev/null)"
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
  "${ROOT_DIR}/scripts/colima-egress-allowlist.sh" plan default 'example.com:443; touch /tmp/workcell-egress-pwned' \
  >/tmp/workcell-egress-invalid.out 2>&1; then
  echo "Expected invalid egress endpoint rejection" >&2
  exit 1
fi
if ! grep -q "Invalid endpoint" /tmp/workcell-egress-invalid.out; then
  echo "Expected explicit invalid-endpoint validation failure" >&2
  exit 1
fi
if [[ -e /tmp/workcell-egress-pwned ]]; then
  echo "Invalid egress endpoint survived validation and reached the shell" >&2
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
if ! "${ROOT_DIR}/scripts/workcell" --agent codex --prepare --allow-nongit-workspace --workspace "${NONGIT_WORKSPACE}" --dry-run >/dev/null 2>&1; then
  echo "Expected marker-based non-git workspace to succeed with explicit opt-in" >&2
  exit 1
fi
for agent in claude gemini; do
  if ! "${ROOT_DIR}/scripts/workcell" --agent "${agent}" --prepare --allow-nongit-workspace --workspace "${NONGIT_WORKSPACE}" --dry-run >/dev/null 2>&1; then
    echo "Expected marker-based non-git workspace prepare dry-run to succeed for ${agent}" >&2
    exit 1
  fi
done

if [[ "$(uname -s)" == "Darwin" ]] &&
  host_tool_exists /opt/homebrew/bin/colima /usr/local/bin/colima &&
  host_tool_exists /opt/homebrew/bin/docker /usr/local/bin/docker /Applications/Docker.app/Contents/Resources/bin/docker; then
  LIVE_DEBUG_PROFILE_NAME="workcell-live-debug-$$"
  LIVE_DEBUG_LOG="${BARRIER_VERIFY_ROOT}/debug/live-debug.log"
  if ! "${ROOT_DIR}/scripts/workcell" \
    --agent codex \
    --prepare \
    --rebuild \
    --workspace "${ROOT_DIR}" \
    --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
    --debug-log "${LIVE_DEBUG_LOG}" \
    --agent-arg --version >/tmp/workcell-audit-prepare.out 2>&1; then
    echo "Expected audit verification prepare run to seed a managed image" >&2
    cat /tmp/workcell-audit-prepare.out >&2
    exit 1
  fi
  grep -q 'starting colima' "${LIVE_DEBUG_LOG}"
  grep -q 'runtime-builder' "${LIVE_DEBUG_LOG}"
  if ! "${ROOT_DIR}/scripts/workcell" \
    --logs debug \
    --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" >/tmp/workcell-live-logs-debug.out 2>&1; then
    echo "Expected successful prepare run to persist the latest debug-log pointer" >&2
    exit 1
  fi
  grep -q 'starting colima' /tmp/workcell-live-logs-debug.out
  AUDIT_LOG="$(sed -n 's/.*audit_log=\([^ ]*\).*/\1/p' /tmp/workcell-audit-prepare.out | head -n1)"
  if [[ -z "${AUDIT_LOG}" ]]; then
    echo "Expected audit verification prepare run to report an audit log path" >&2
    exit 1
  fi
  AUDIT_BASE_LINES=0
  if [[ -f "${AUDIT_LOG}" ]]; then
    AUDIT_BASE_LINES="$(wc -l <"${AUDIT_LOG}")"
  fi
  if ! "${ROOT_DIR}/scripts/workcell" \
    --agent codex \
    --mode build \
    --workspace "${ROOT_DIR}" \
    --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
    --allow-arbitrary-command \
    --ack-arbitrary-command \
    -- /bin/bash -lc 'sudo -n /usr/local/libexec/workcell/apt-helper.sh apt-get update >/dev/null && sudo -n /usr/local/libexec/workcell/apt-helper.sh apt-get install -y --no-install-recommends make >/dev/null'; then
    echo "Expected package-mutation audit verification run to succeed" >&2
    exit 1
  fi
  tail -n "+$((AUDIT_BASE_LINES + 1))" "${AUDIT_LOG}" >/tmp/workcell-audit-session.log
  grep -q 'event=launch' /tmp/workcell-audit-session.log
  grep -q 'record_digest=' /tmp/workcell-audit-session.log
  grep -q 'execution_path=lower-assurance-debug-command' /tmp/workcell-audit-session.log
  grep -q 'event=assurance-change' /tmp/workcell-audit-session.log
  grep -q 'reason=package-mutation' /tmp/workcell-audit-session.log
  grep -q 'session_assurance_final=lower-assurance-package-mutation' /tmp/workcell-audit-session.log
  grep -q 'event=exit' /tmp/workcell-audit-session.log
  grep -q 'package_mutation_downgraded=1' /tmp/workcell-audit-session.log
  if [[ -x /opt/homebrew/bin/colima ]]; then
    /opt/homebrew/bin/colima delete --profile "${LIVE_DEBUG_PROFILE_NAME}" --force >/dev/null 2>&1 || true
  else
    /usr/local/bin/colima delete --profile "${LIVE_DEBUG_PROFILE_NAME}" --force >/dev/null 2>&1 || true
  fi
  rm -rf "${REAL_HOME}/.colima/${LIVE_DEBUG_PROFILE_NAME}" "${REAL_HOME}/.colima/_lima/colima-${LIVE_DEBUG_PROFILE_NAME}"
  AUDIT_RESTORE_PROFILE_NAME="workcell-audit-restore-$$"
  AUDIT_RESTORE_DIR="${REAL_HOME}/.colima/${AUDIT_RESTORE_PROFILE_NAME}"
  AUDIT_RESTORE_LOG="${AUDIT_RESTORE_DIR}/workcell.audit.log"
  mkdir -p "${AUDIT_RESTORE_DIR}"
  printf '%s\n' "${NONGIT_WORKSPACE}" >"${AUDIT_RESTORE_DIR}/workcell.managed"
  cat >"${AUDIT_RESTORE_DIR}/colima.yaml" <<'EOF'
cpu: 4
memory: 8
disk: 60
runtime: docker
vmType: vz
mountType: virtiofs
EOF
  printf 'timestamp=test event=launch workspace=%q\n' "${NONGIT_WORKSPACE}" >"${AUDIT_RESTORE_LOG}"
  if "${ROOT_DIR}/scripts/workcell" \
    --test-fail-after-profile-refresh \
    --agent codex \
    --prepare \
    --allow-nongit-workspace \
    --workspace "${NONGIT_WORKSPACE}" \
    --colima-profile "${AUDIT_RESTORE_PROFILE_NAME}" \
    --agent-arg --version >/tmp/workcell-audit-restore.out 2>&1; then
    echo "Expected managed-profile refresh test hook to fail after stashing the audit log" >&2
    exit 1
  fi
  grep -q 'Workcell test hook: forcing failure after managed profile refresh.' /tmp/workcell-audit-restore.out
  grep -q 'timestamp=test event=launch' "${AUDIT_RESTORE_LOG}"
  if [[ -x /opt/homebrew/bin/colima ]]; then
    /opt/homebrew/bin/colima delete --profile "${AUDIT_RESTORE_PROFILE_NAME}" --force >/dev/null 2>&1 || true
  else
    /usr/local/bin/colima delete --profile "${AUDIT_RESTORE_PROFILE_NAME}" --force >/dev/null 2>&1 || true
  fi
  rm -rf "${REAL_HOME}/.colima/${AUDIT_RESTORE_PROFILE_NAME}" "${REAL_HOME}/.colima/_lima/colima-${AUDIT_RESTORE_PROFILE_NAME}"
fi

UNMANAGED_PROFILE_NAME="workcell-unmanaged-verify-$$"
mkdir -p "${REAL_HOME}/.colima/${UNMANAGED_PROFILE_NAME}"
if "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --allow-nongit-workspace \
  --workspace "${NONGIT_WORKSPACE}" \
  --colima-profile "${UNMANAGED_PROFILE_NAME}" >/tmp/workcell-unmanaged-profile.out 2>&1; then
  echo "Expected unmanaged Colima profile reuse to fail" >&2
  exit 1
fi
grep -q "Refusing to reuse unmanaged Colima profile" /tmp/workcell-unmanaged-profile.out
grep -q -- '--repair-profile' /tmp/workcell-unmanaged-profile.out
grep -q "colima delete --profile" /tmp/workcell-unmanaged-profile.out

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --repair-profile \
  --allow-nongit-workspace \
  --workspace "${NONGIT_WORKSPACE}" \
  --colima-profile "${UNMANAGED_PROFILE_NAME}" \
  --dry-run >/tmp/workcell-repair-profile-dry-run.out 2>&1; then
  echo "Expected --repair-profile dry-run to explain the repair action and continue on strict without an extra --prepare flag" >&2
  cat /tmp/workcell-repair-profile-dry-run.out >&2
  exit 1
fi
grep -q 'repair_action=delete_unmanaged_profile' /tmp/workcell-repair-profile-dry-run.out
grep -q 'docker run' /tmp/workcell-repair-profile-dry-run.out
for agent in claude gemini; do
  if ! "${ROOT_DIR}/scripts/workcell" \
    --agent "${agent}" \
    --repair-profile \
    --allow-nongit-workspace \
    --workspace "${NONGIT_WORKSPACE}" \
    --colima-profile "${UNMANAGED_PROFILE_NAME}" \
    --dry-run >/tmp/workcell-repair-profile-${agent}-dry-run.out 2>&1; then
    echo "Expected --repair-profile dry-run to succeed for ${agent}" >&2
    cat /tmp/workcell-repair-profile-${agent}-dry-run.out >&2
    exit 1
  fi
  grep -q 'repair_action=delete_unmanaged_profile' /tmp/workcell-repair-profile-${agent}-dry-run.out
  grep -q 'docker run' /tmp/workcell-repair-profile-${agent}-dry-run.out
done
rm -rf "${REAL_HOME}/.colima/${UNMANAGED_PROFILE_NAME}" "${REAL_HOME}/.colima/_lima/colima-${UNMANAGED_PROFILE_NAME}"

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
  printf 'image_tag=workcell:local\nimage_id=sha256:test\nsource_date_epoch=0\n' >"${COLIMA_PROFILE_FIXTURE}/workcell.image-ready"
  cat >"${COLIMA_PROFILE_FIXTURE}/colima.yaml" <<'EOF'
vmType: qemu
mountType: virtiofs
runtime: docker
EOF
  cat >"${RUBYOPT_PAYLOAD}" <<'EOF'
File.write(ENV.fetch("RUBYOPT_MARKER"), "ran\n")
EOF
  RUBYOPT_MARKER="${RUBYOPT_MARKER}" \
    RUBYOPT="-r${RUBYOPT_PAYLOAD}" \
    "${ROOT_DIR}/scripts/workcell" \
    --agent codex \
    --allow-nongit-workspace \
    --workspace "${NONGIT_WORKSPACE}" \
    --colima-profile "${RUBY_PROFILE_NAME}" >/tmp/workcell-rubyopt.out 2>&1 || true
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

cat <<'EOF' >"${LOCAL_REMOTE_CONFIG_PATH}"
WORKCELL_REMOTE_VALIDATE_HOST=builder@example.internal
WORKCELL_REMOTE_VALIDATE_BASE_DIR=/var/tmp/workcell
WORKCELL_REMOTE_VALIDATE_USE_SUDO=0
EOF
if ! WORKCELL_REMOTE_VALIDATE_CONFIG_PATH="${LOCAL_REMOTE_CONFIG_PATH}" \
  "${ROOT_DIR}/scripts/dev-remote-validate.sh" --check validate --dry-run >/tmp/workcell-remote-config.out 2>&1; then
  echo "Expected host-local remote builder config to be accepted" >&2
  cat /tmp/workcell-remote-config.out >&2
  exit 1
fi
grep -q 'Remote host: builder@example.internal' /tmp/workcell-remote-config.out
grep -q 'Remote base dir: /var/tmp/workcell' /tmp/workcell-remote-config.out
grep -q "Remote config path: ${LOCAL_REMOTE_CONFIG_PATH}" /tmp/workcell-remote-config.out
if ! "${ROOT_DIR}/scripts/dev-remote-validate.sh" --config "${LOCAL_REMOTE_CONFIG_PATH}" --check validate --dry-run >/tmp/workcell-remote-config-cli.out 2>&1; then
  echo "Expected --config host-local remote builder config to be accepted" >&2
  cat /tmp/workcell-remote-config-cli.out >&2
  exit 1
fi
grep -q 'Remote host: builder@example.internal' /tmp/workcell-remote-config-cli.out
grep -q "Remote config path: ${LOCAL_REMOTE_CONFIG_PATH}" /tmp/workcell-remote-config-cli.out

if "${ROOT_DIR}/scripts/dev-remote-validate.sh" --config "${LOCAL_REMOTE_CONFIG_PATH}" --check smoke --dry-run >/tmp/workcell-remote-heavy-no-ack.out 2>&1; then
  echo "Expected heavy remote validation without an explicit shared-daemon acknowledgement to be rejected" >&2
  exit 1
fi
grep -q 'Heavy remote checks require --allow-shared-daemon-heavy-checks' /tmp/workcell-remote-heavy-no-ack.out

cat <<'EOF' >"${LOCAL_REMOTE_CONFIG_PATH}"
WORKCELL_REMOTE_VALIDATE_HOST=builder@example.internal
WORKCELL_REMOTE_VALIDATE_BASE_DIR=/var/tmp/workcell
WORKCELL_REMOTE_VALIDATE_USE_SUDO=0
WORKCELL_REMOTE_VALIDATE_ALLOW_SHARED_DAEMON_HEAVY_CHECKS=1
EOF
if ! "${ROOT_DIR}/scripts/dev-remote-validate.sh" --config "${LOCAL_REMOTE_CONFIG_PATH}" --check smoke --dry-run >/tmp/workcell-remote-heavy-ack.out 2>&1; then
  echo "Expected heavy remote validation with an explicit shared-daemon acknowledgement to be accepted" >&2
  cat /tmp/workcell-remote-heavy-ack.out >&2
  exit 1
fi
grep -q 'Allow shared-daemon heavy checks: 1' /tmp/workcell-remote-heavy-ack.out

cat <<'EOF' >"${LEGACY_LOCAL_REMOTE_CONFIG_PATH}"
WORKCELL_REMOTE_VALIDATE_HOST=builder@example.internal
EOF
if "${ROOT_DIR}/scripts/dev-remote-validate.sh" --check validate --dry-run >/tmp/workcell-remote-config-legacy.out 2>&1; then
  echo "Expected legacy repo-local remote builder config to be rejected" >&2
  exit 1
fi
grep -q 'Legacy repo-local remote builder config is no longer supported' /tmp/workcell-remote-config-legacy.out
rm -f "${LEGACY_LOCAL_REMOTE_CONFIG_PATH}"

cat <<'EOF' >"${REPO_LOCAL_REMOTE_CONFIG_PATH}"
WORKCELL_REMOTE_VALIDATE_HOST=builder@example.internal
EOF
if WORKCELL_REMOTE_VALIDATE_CONFIG_PATH="${REPO_LOCAL_REMOTE_CONFIG_PATH}" \
  "${ROOT_DIR}/scripts/dev-remote-validate.sh" --check validate --dry-run >/tmp/workcell-remote-config-repo-env.out 2>&1; then
  echo "Expected repo-local remote builder config override via environment to be rejected" >&2
  exit 1
fi
grep -q 'Remote builder config must live outside the repo checkout' /tmp/workcell-remote-config-repo-env.out
if "${ROOT_DIR}/scripts/dev-remote-validate.sh" --config "${REPO_LOCAL_REMOTE_CONFIG_PATH}" --check validate --dry-run >/tmp/workcell-remote-config-repo-cli.out 2>&1; then
  echo "Expected repo-local remote builder config override via --config to be rejected" >&2
  exit 1
fi
grep -q 'Remote builder config must live outside the repo checkout' /tmp/workcell-remote-config-repo-cli.out

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

if claude_managed.get("disableBypassPermissionsMode") != "allow":
    raise SystemExit("Claude managed settings must allow bypass-permissions mode under the external Workcell boundary")

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
