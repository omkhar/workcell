set -Eeuo pipefail
trap 'echo "Gemini auth selection harness failed at line ${LINENO}: ${BASH_COMMAND}" >&2' ERR
export PS4='+ gemini-harness:${LINENO}: '
set -x

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

# Provides the abort primitive that the home-control-plane.sh gemini functions
# (extracted and concatenated ahead of this harness by verify-invariants) call
# to reject invalid auth configs. Do not remove: those callers live in another
# file and bind to this definition via dynamic scope at harness runtime.
workcell_die() {
  printf '%s\n' "$*" >&2
  exit 1
}

expect_fatal_function_failure() {
  local stdout_path="$1"
  local stderr_path="$2"
  shift 2

  if ( "$@" ) >"${stdout_path}" 2>"${stderr_path}"; then
    return 0
  fi

  return 1
}

expect_auth_type() {
  local env_contents="$1"
  local oauth_present="$2"
  local expected="$3"
  local env_path="${TMP_DIR}/gemini.env"
  local oauth_path="${TMP_DIR}/oauth.json"
  local selected=""

  rm -f "${env_path}" "${oauth_path}"
  if [[ -n "${env_contents}" ]]; then
    printf '%s\n' "${env_contents}" >"${env_path}"
  fi
  if [[ "${oauth_present}" == "1" ]]; then
    printf '{}\n' >"${oauth_path}"
  fi

  selected="$(workcell_gemini_selected_auth_type "${env_path}" "${oauth_path}")"
  if [[ "${selected}" != "${expected}" ]]; then
    echo "Expected Gemini auth type ${expected}, got ${selected}" >&2
    exit 1
  fi
}

expect_auth_type 'GEMINI_API_KEY=test-key' 0 'gemini-api-key'
expect_auth_type ' export GEMINI_API_KEY = "quoted-key" # comment' 0 'gemini-api-key'
expect_auth_type $'GOOGLE_GENAI_USE_GCA=true\nGEMINI_API_KEY=test-key' 0 'oauth-personal'
expect_auth_type $'GOOGLE_GENAI_USE_GCA="true" # comment\nGOOGLE_CLOUD_PROJECT=my-proj' 0 'oauth-personal'
expect_auth_type $'GOOGLE_GENAI_USE_VERTEXAI="true" # comment\nGOOGLE_CLOUD_PROJECT=my-proj\nGOOGLE_CLOUD_LOCATION="us-central1" # comment' 0 'vertex-ai'
expect_auth_type $'GOOGLE_GENAI_USE_VERTEXAI=true\nGOOGLE_API_KEY=vertex-key' 0 'vertex-ai'
expect_auth_type $'GEMINI_API_KEY=test-key\nGOOGLE_CLOUD_PROJECT=my-proj' 0 'gemini-api-key'
expect_auth_type '' 1 'oauth-personal'

printf 'GOOGLE_API_KEY=test-key\n' >"${TMP_DIR}/google-api-key-only.env"
if workcell_gemini_selected_auth_type "${TMP_DIR}/google-api-key-only.env" "${TMP_DIR}/missing-oauth.json" >/dev/null 2>&1; then
  echo "Expected bare GOOGLE_API_KEY to stay unset until Gemini Vertex auth is explicitly selected" >&2
  exit 1
fi

if workcell_gemini_selected_auth_type "${TMP_DIR}/missing.env" "${TMP_DIR}/missing-oauth.json" >/dev/null 2>&1; then
  echo "Expected Gemini auth selection to stay unset when no credential material is present" >&2
  exit 1
fi

printf 'GOOGLE_GENAI_USE_GCA=maybe\n' >"${TMP_DIR}/invalid-bool.env"
if expect_fatal_function_failure /tmp/gemini-invalid-bool.stdout /tmp/gemini-invalid-bool.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/invalid-bool.env"; then
  echo "Expected invalid Gemini auth booleans to be rejected" >&2
  exit 1
fi
grep -q 'Invalid boolean in Gemini auth env file' /tmp/gemini-invalid-bool.stderr

printf 'GOOGLE_GENAI_USE_VERTEXAI true\n' >"${TMP_DIR}/malformed.env"
if expect_fatal_function_failure /tmp/gemini-malformed.stdout /tmp/gemini-malformed.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/malformed.env"; then
  echo "Expected malformed Gemini auth env syntax to be rejected" >&2
  exit 1
fi
grep -q 'Malformed Gemini auth env file' /tmp/gemini-malformed.stderr

printf 'UNSUPPORTED_KEY=test\n' >"${TMP_DIR}/unsupported.env"
if expect_fatal_function_failure /tmp/gemini-unsupported.stdout /tmp/gemini-unsupported.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/unsupported.env"; then
  echo "Expected unsupported Gemini auth env keys to be rejected" >&2
  exit 1
fi
grep -q 'Unsupported key in Gemini auth env file' /tmp/gemini-unsupported.stderr

printf 'GEMINI_API_KEY=one\nGEMINI_API_KEY=two\n' >"${TMP_DIR}/duplicate-api-key.env"
if expect_fatal_function_failure /tmp/gemini-duplicate-api-key.stdout /tmp/gemini-duplicate-api-key.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/duplicate-api-key.env"; then
  echo "Expected duplicate GEMINI_API_KEY assignments to be rejected" >&2
  exit 1
fi
grep -q 'configures GEMINI_API_KEY more than once' /tmp/gemini-duplicate-api-key.stderr

printf ' export GOOGLE_GENAI_USE_GCA=true\nGOOGLE_GENAI_USE_GCA=false\n' >"${TMP_DIR}/duplicate-exported-bool.env"
if expect_fatal_function_failure /tmp/gemini-duplicate-exported-bool.stdout /tmp/gemini-duplicate-exported-bool.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/duplicate-exported-bool.env"; then
  echo "Expected duplicate exported Gemini auth selectors to be rejected" >&2
  exit 1
fi
grep -q 'configures GOOGLE_GENAI_USE_GCA more than once' /tmp/gemini-duplicate-exported-bool.stderr

printf 'GOOGLE_GENAI_USE_GCA=true\nGOOGLE_GENAI_USE_VERTEXAI=true\n' >"${TMP_DIR}/conflicting-selectors.env"
if expect_fatal_function_failure /tmp/gemini-conflicting.stdout /tmp/gemini-conflicting.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/conflicting-selectors.env"; then
  echo "Expected contradictory Gemini auth selectors to be rejected" >&2
  exit 1
fi
grep -q 'enables both GOOGLE_GENAI_USE_GCA and GOOGLE_GENAI_USE_VERTEXAI' /tmp/gemini-conflicting.stderr

printf 'GOOGLE_GENAI_USE_VERTEXAI=true\nGOOGLE_API_KEY=vertex-key\n' >"${TMP_DIR}/vertex-express.env"
if ! workcell_validate_gemini_env_auth_config "${TMP_DIR}/vertex-express.env" >/tmp/gemini-vertex-express.stdout 2>/tmp/gemini-vertex-express.stderr; then
  echo "Expected Gemini Vertex express-mode env config to validate" >&2
  cat /tmp/gemini-vertex-express.stderr >&2
  exit 1
fi

printf 'GOOGLE_API_KEY=vertex-key\n' >"${TMP_DIR}/google-api-key-only.env"
if expect_fatal_function_failure /tmp/gemini-google-api-key.stdout /tmp/gemini-google-api-key.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/google-api-key-only.env"; then
  echo "Expected bare GOOGLE_API_KEY to be rejected without GOOGLE_GENAI_USE_VERTEXAI=true" >&2
  exit 1
fi
grep -q 'sets GOOGLE_API_KEY without GOOGLE_GENAI_USE_VERTEXAI=true' /tmp/gemini-google-api-key.stderr

printf 'GOOGLE_CLOUD_LOCATION=us-central1\n' >"${TMP_DIR}/location-only.env"
if expect_fatal_function_failure /tmp/gemini-location-only.stdout /tmp/gemini-location-only.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/location-only.env"; then
  echo "Expected location-only Gemini env config to be rejected" >&2
  exit 1
fi
grep -q 'sets a Google Cloud location without a project' /tmp/gemini-location-only.stderr

printf 'GOOGLE_CLOUD_PROJECT=my-proj\n' >"${TMP_DIR}/project-only.env"
if expect_fatal_function_failure /tmp/gemini-project-only.stdout /tmp/gemini-project-only.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/project-only.env"; then
  echo "Expected project-only Gemini env config to be rejected" >&2
  exit 1
fi
grep -q 'does not configure a supported Gemini auth mode' /tmp/gemini-project-only.stderr

SETTINGS_PATH="${TMP_DIR}/settings.json"
cat >"${SETTINGS_PATH}" <<'JSON'
{"security":{"folderTrust":{"enabled":false}}}
JSON
workcell_set_gemini_selected_auth_type "${SETTINGS_PATH}" "gemini-api-key"
if ! jq -e '.security.auth.selectedType == "gemini-api-key"' "${SETTINGS_PATH}" >/dev/null; then
  echo "Gemini selected auth type should be persisted into the seeded settings" >&2
  exit 1
fi
if ! jq -e '.security.folderTrust.enabled == false' "${SETTINGS_PATH}" >/dev/null; then
  echo "Gemini selected auth type update should preserve existing settings" >&2
  exit 1
fi
workcell_set_gemini_folder_trust_enabled "${SETTINGS_PATH}" true
if ! jq -e '.security.folderTrust.enabled == true' "${SETTINGS_PATH}" >/dev/null; then
  echo "Gemini folder-trust helper should restore trust prompts for breakglass sessions" >&2
  exit 1
fi
workcell_set_gemini_folder_trust_enabled "${SETTINGS_PATH}" false

TRUSTED_FOLDERS_PATH="${TMP_DIR}/trustedFolders.json"
TRUSTED_WORKSPACE=$'/workspace/quoted"path\\segment'
workcell_render_gemini_trusted_folders "${TRUSTED_FOLDERS_PATH}" "${TRUSTED_WORKSPACE}"
if [[ "$(jq -S -c '.' "${TRUSTED_FOLDERS_PATH}")" != "$(jq -S -c -n --arg path "${TRUSTED_WORKSPACE}" '{($path): "TRUST_FOLDER"}')" ]]; then
  echo "Expected trustedFolders.json to preserve the exact workspace path" >&2
  exit 1
fi

printf '{"projects":[]}\n' >"${TMP_DIR}/invalid-projects.json"
if expect_fatal_function_failure /tmp/gemini-invalid-projects.stdout /tmp/gemini-invalid-projects.stderr \
  workcell_validate_gemini_projects_config "${TMP_DIR}/invalid-projects.json"; then
  echo "Expected invalid Gemini projects config to be rejected" >&2
  exit 1
fi
grep -q 'Gemini projects config must contain a JSON object with an object-valued projects field' /tmp/gemini-invalid-projects.stderr

printf '{"projects":{}}\n' >"${TMP_DIR}/valid-projects.json"
if ! workcell_validate_gemini_projects_config "${TMP_DIR}/valid-projects.json" >/tmp/gemini-valid-projects.stdout 2>/tmp/gemini-valid-projects.stderr; then
  echo "Expected valid Gemini projects config to be accepted" >&2
  cat /tmp/gemini-valid-projects.stderr >&2
  exit 1
fi

if workcell_target_is_allowed '/state/agent-home/.gemini/trustedFolders.json'; then
  echo "Expected runtime manifest guard to reserve Gemini trustedFolders.json" >&2
  exit 1
fi
if workcell_target_is_allowed '/state/agent-home/.claude/settings.json'; then
  echo "Expected runtime manifest guard to reserve Claude settings.json" >&2
  exit 1
fi
if workcell_target_is_allowed '/state/agent-home/.claude/.credentials.json'; then
  echo "Expected runtime manifest guard to reserve injected Claude credentials" >&2
  exit 1
fi
if workcell_target_is_allowed '/state/agent-home/.claude/.claude.json'; then
  echo "Expected runtime manifest guard to reserve injected Claude session config" >&2
  exit 1
fi
if workcell_target_is_allowed '/state/agent-home/.claude.json'; then
  echo "Expected runtime manifest guard to reserve injected Claude global config" >&2
  exit 1
fi
if workcell_target_is_allowed '/state/agent-home/.config/claude-code/auth.json'; then
  echo "Expected runtime manifest guard to reserve injected Claude auth.json" >&2
  exit 1
fi
if workcell_target_is_allowed '/state/agent-home/.gemini/settings.json'; then
  echo "Expected runtime manifest guard to reserve Gemini settings.json" >&2
  exit 1
fi
if workcell_target_is_allowed '/state/agent-home/.ssh/config'; then
  echo "Expected runtime manifest guard to reserve seeded SSH config paths" >&2
  exit 1
fi
if ! workcell_target_is_allowed '/state/agent-home/workcell-benign-note.txt'; then
  echo "Expected runtime manifest guard to allow benign session-local targets under /state/agent-home" >&2
  exit 1
fi
if ! workcell_target_is_allowed '/state/injected/documents/org-policy.md'; then
  echo "Expected runtime manifest guard to allow staged injected documents under /state/injected" >&2
  exit 1
fi
if workcell_target_is_allowed '/workspace/not-allowed.txt'; then
  echo "Expected runtime manifest guard to reject targets outside managed session roots" >&2
  exit 1
fi
