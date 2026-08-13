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

  if (
    set +x
    "$@"
  ) >"${stdout_path}" 2>"${stderr_path}"; then
    return 0
  fi

  return 1
}

expect_stderr_to_omit() {
  local stderr_path="$1"
  local marker="$2"

  if grep -Fq "${marker}" "${stderr_path}"; then
    echo "Gemini auth error leaked credential-file content" >&2
    exit 1
  fi
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
expect_auth_type 'GOOGLE_GENAI_USE_GCA="tr\u0075e"' 0 'oauth-personal'
expect_auth_type $'GOOGLE_GENAI_USE_GCA=true\nGEMINI_API_KEY=test-key' 0 'oauth-personal'
expect_auth_type $'GOOGLE_GENAI_USE_GCA="true" # comment\nGOOGLE_CLOUD_PROJECT=my-proj' 0 'oauth-personal'
expect_auth_type $'GOOGLE_GENAI_USE_VERTEXAI="true" # comment\nGOOGLE_CLOUD_PROJECT=my-proj\nGOOGLE_CLOUD_LOCATION="us-central1" # comment' 0 'vertex-ai'
expect_auth_type $'GOOGLE_GENAI_USE_VERTEXAI=true\nGOOGLE_API_KEY=vertex-key' 0 'vertex-ai'
expect_auth_type $'GEMINI_API_KEY=test-key\nGOOGLE_CLOUD_PROJECT=my-proj' 0 'gemini-api-key'
expect_auth_type '' 1 'oauth-personal'

printf 'GEMINI_API_KEY="decoded\\u002dkey"\n' >"${TMP_DIR}/quoted-escape.env"
if [[ "$(workcell_env_file_assignment_value "${TMP_DIR}/quoted-escape.env" "GEMINI_API_KEY")" != "decoded-key" ]]; then
  echo "Expected Gemini double-quoted escapes to decode before auth selection" >&2
  exit 1
fi

printf '%s\n' 'GEMINI_API_KEY="literal\\uD800"' >"${TMP_DIR}/literal-surrogate-escape.env"
if [[ "$(workcell_env_file_assignment_value "${TMP_DIR}/literal-surrogate-escape.env" "GEMINI_API_KEY")" != 'literal\uD800' ]]; then
  echo "Expected literal Gemini backslash Unicode escapes to remain valid" >&2
  exit 1
fi

printf '%s\n' 'GEMINI_API_KEY="literal\\u0000"' >"${TMP_DIR}/literal-nul-escape.env"
if [[ "$(workcell_env_file_assignment_value "${TMP_DIR}/literal-nul-escape.env" "GEMINI_API_KEY")" != 'literal\u0000' ]]; then
  echo "Expected literal Gemini backslash NUL escapes to remain valid" >&2
  exit 1
fi

printf '%s\n' 'GEMINI_API_KEY="literal\\/"' >"${TMP_DIR}/literal-slash-escape.env"
if [[ "$(workcell_env_file_assignment_value "${TMP_DIR}/literal-slash-escape.env" "GEMINI_API_KEY")" != 'literal\/' ]]; then
  echo "Expected literal Gemini backslash slash text to remain valid" >&2
  exit 1
fi

printf 'GOOGLE_API_KEY=test-key\n' >"${TMP_DIR}/google-api-key-only.env"
if workcell_gemini_selected_auth_type "${TMP_DIR}/google-api-key-only.env" "${TMP_DIR}/missing-oauth.json" >/dev/null 2>&1; then
  echo "Expected bare GOOGLE_API_KEY to stay unset until Gemini Vertex auth is explicitly selected" >&2
  exit 1
fi

if workcell_gemini_selected_auth_type "${TMP_DIR}/missing.env" "${TMP_DIR}/missing-oauth.json" >/dev/null 2>&1; then
  echo "Expected Gemini auth selection to stay unset when no credential material is present" >&2
  exit 1
fi

printf 'GOOGLE_GENAI_USE_GCA=invalid-boolean-secret\n' >"${TMP_DIR}/invalid-bool.env"
if expect_fatal_function_failure /tmp/gemini-invalid-bool.stdout /tmp/gemini-invalid-bool.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/invalid-bool.env"; then
  echo "Expected invalid Gemini auth booleans to be rejected" >&2
  exit 1
fi
grep -q 'Invalid boolean in Gemini auth env file' /tmp/gemini-invalid-bool.stderr
grep -q 'GOOGLE_GENAI_USE_GCA' /tmp/gemini-invalid-bool.stderr
expect_stderr_to_omit /tmp/gemini-invalid-bool.stderr 'invalid-boolean-secret'

printf 'GOOGLE_GENAI_USE_VERTEXAI raw-malformed-secret\n' >"${TMP_DIR}/malformed.env"
if expect_fatal_function_failure /tmp/gemini-malformed.stdout /tmp/gemini-malformed.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/malformed.env"; then
  echo "Expected malformed Gemini auth env syntax to be rejected" >&2
  exit 1
fi
grep -q 'Malformed Gemini auth env file' /tmp/gemini-malformed.stderr
grep -q 'line 1' /tmp/gemini-malformed.stderr
expect_stderr_to_omit /tmp/gemini-malformed.stderr 'raw-malformed-secret'

printf 'UNSUPPORTED_SECRET_KEY=unsupported-value-secret\n' >"${TMP_DIR}/unsupported.env"
if expect_fatal_function_failure /tmp/gemini-unsupported.stdout /tmp/gemini-unsupported.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/unsupported.env"; then
  echo "Expected unsupported Gemini auth env keys to be rejected" >&2
  exit 1
fi
grep -q 'Unsupported key in Gemini auth env file' /tmp/gemini-unsupported.stderr
grep -q 'line 1' /tmp/gemini-unsupported.stderr
expect_stderr_to_omit /tmp/gemini-unsupported.stderr 'UNSUPPORTED_SECRET_KEY'
expect_stderr_to_omit /tmp/gemini-unsupported.stderr 'unsupported-value-secret'

printf 'GEMINI_API_KEY=duplicate-one-secret\nGEMINI_API_KEY=duplicate-two-secret\n' >"${TMP_DIR}/duplicate-api-key.env"
if expect_fatal_function_failure /tmp/gemini-duplicate-api-key.stdout /tmp/gemini-duplicate-api-key.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/duplicate-api-key.env"; then
  echo "Expected duplicate GEMINI_API_KEY assignments to be rejected" >&2
  exit 1
fi
grep -q 'configures GEMINI_API_KEY more than once' /tmp/gemini-duplicate-api-key.stderr
grep -q 'line 2' /tmp/gemini-duplicate-api-key.stderr
expect_stderr_to_omit /tmp/gemini-duplicate-api-key.stderr 'duplicate-one-secret'
expect_stderr_to_omit /tmp/gemini-duplicate-api-key.stderr 'duplicate-two-secret'

printf 'GEMINI_API_KEY="unterminated-quote-secret\n' >"${TMP_DIR}/unterminated-quote.env"
if expect_fatal_function_failure /tmp/gemini-unterminated-quote.stdout /tmp/gemini-unterminated-quote.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/unterminated-quote.env"; then
  echo "Expected unterminated Gemini auth quotes to be rejected" >&2
  exit 1
fi
grep -q 'Malformed Gemini auth env file' /tmp/gemini-unterminated-quote.stderr
grep -q 'line 1' /tmp/gemini-unterminated-quote.stderr
expect_stderr_to_omit /tmp/gemini-unterminated-quote.stderr 'unterminated-quote-secret'

printf "GEMINI_API_KEY=trailing-quote-secret'\n" >"${TMP_DIR}/trailing-quote.env"
if expect_fatal_function_failure /tmp/gemini-trailing-quote.stdout /tmp/gemini-trailing-quote.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/trailing-quote.env"; then
  echo "Expected trailing Gemini auth quotes to be rejected" >&2
  exit 1
fi
grep -q 'Malformed Gemini auth env file' /tmp/gemini-trailing-quote.stderr
grep -q 'line 1' /tmp/gemini-trailing-quote.stderr
expect_stderr_to_omit /tmp/gemini-trailing-quote.stderr 'trailing-quote-secret'

printf 'GEMINI_API_KEY="invalid-double-escape-secret\\q"\n' >"${TMP_DIR}/invalid-escape.env"
if expect_fatal_function_failure /tmp/gemini-invalid-escape.stdout /tmp/gemini-invalid-escape.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/invalid-escape.env"; then
  echo "Expected invalid Gemini double-quoted escapes to be rejected" >&2
  exit 1
fi
grep -q 'Malformed Gemini auth env file' /tmp/gemini-invalid-escape.stderr
grep -q 'line 1' /tmp/gemini-invalid-escape.stderr
expect_stderr_to_omit /tmp/gemini-invalid-escape.stderr 'invalid-double-escape-secret'

printf '%s\n' 'GEMINI_API_KEY="json-only-slash-escape-secret\/"' >"${TMP_DIR}/json-only-slash-escape.env"
if expect_fatal_function_failure /tmp/gemini-json-only-slash-escape.stdout /tmp/gemini-json-only-slash-escape.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/json-only-slash-escape.env"; then
  echo "Expected JSON-only Gemini double-quoted slash escapes to be rejected" >&2
  exit 1
fi
grep -q 'Malformed Gemini auth env file' /tmp/gemini-json-only-slash-escape.stderr
grep -q 'line 1' /tmp/gemini-json-only-slash-escape.stderr
expect_stderr_to_omit /tmp/gemini-json-only-slash-escape.stderr 'json-only-slash-escape-secret'

printf 'GEMINI_API_KEY="go-only-hex-escape-secret\\x61"\n' >"${TMP_DIR}/go-only-escape.env"
if expect_fatal_function_failure /tmp/gemini-go-only-escape.stdout /tmp/gemini-go-only-escape.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/go-only-escape.env"; then
  echo "Expected Go-only Gemini double-quoted escapes to be rejected" >&2
  exit 1
fi
grep -q 'Malformed Gemini auth env file' /tmp/gemini-go-only-escape.stderr
grep -q 'line 1' /tmp/gemini-go-only-escape.stderr
expect_stderr_to_omit /tmp/gemini-go-only-escape.stderr 'go-only-hex-escape-secret'

printf 'GEMINI_API_KEY="unpaired-surrogate-secret\\uD800"\n' >"${TMP_DIR}/unpaired-surrogate.env"
if expect_fatal_function_failure /tmp/gemini-unpaired-surrogate.stdout /tmp/gemini-unpaired-surrogate.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/unpaired-surrogate.env"; then
  echo "Expected Gemini double-quoted surrogate escapes to be rejected" >&2
  exit 1
fi
grep -q 'Malformed Gemini auth env file' /tmp/gemini-unpaired-surrogate.stderr
grep -q 'line 1' /tmp/gemini-unpaired-surrogate.stderr
expect_stderr_to_omit /tmp/gemini-unpaired-surrogate.stderr 'unpaired-surrogate-secret'

printf 'GEMINI_API_KEY="low-surrogate-secret\\uDFFF"\n' >"${TMP_DIR}/low-surrogate.env"
if expect_fatal_function_failure /tmp/gemini-low-surrogate.stdout /tmp/gemini-low-surrogate.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/low-surrogate.env"; then
  echo "Expected Gemini low-surrogate escapes to be rejected" >&2
  exit 1
fi
grep -q 'Malformed Gemini auth env file' /tmp/gemini-low-surrogate.stderr
grep -q 'line 1' /tmp/gemini-low-surrogate.stderr
expect_stderr_to_omit /tmp/gemini-low-surrogate.stderr 'low-surrogate-secret'

printf 'GEMINI_API_KEY="escaped-ufffd\\uFFFD"\n' >"${TMP_DIR}/escaped-ufffd.env"
if ! workcell_validate_gemini_env_auth_config "${TMP_DIR}/escaped-ufffd.env" >/tmp/gemini-escaped-ufffd.stdout 2>/tmp/gemini-escaped-ufffd.stderr; then
  echo "Expected escaped Gemini U+FFFD to remain valid" >&2
  exit 1
fi

{
  printf 'GEMINI_API_KEY="invalid-byte-prefix-secret-'
  printf '\377'
  printf '%s\n' '-invalid-byte-suffix-secret"'
} >"${TMP_DIR}/invalid-utf8.env"
if expect_fatal_function_failure /tmp/gemini-invalid-utf8.stdout /tmp/gemini-invalid-utf8.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/invalid-utf8.env"; then
  echo "Expected Gemini env files with invalid UTF-8 to be rejected" >&2
  exit 1
fi
grep -q 'Malformed Gemini auth env file' /tmp/gemini-invalid-utf8.stderr
grep -q 'line 1' /tmp/gemini-invalid-utf8.stderr
expect_stderr_to_omit /tmp/gemini-invalid-utf8.stderr 'invalid-byte-prefix-secret'
expect_stderr_to_omit /tmp/gemini-invalid-utf8.stderr 'invalid-byte-suffix-secret'

printf 'GEMINI_API_KEY="embedded-quote-secret"suffix"\n' >"${TMP_DIR}/embedded-quote.env"
if expect_fatal_function_failure /tmp/gemini-embedded-quote.stdout /tmp/gemini-embedded-quote.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/embedded-quote.env"; then
  echo "Expected embedded Gemini double quotes to be rejected" >&2
  exit 1
fi
grep -q 'Malformed Gemini auth env file' /tmp/gemini-embedded-quote.stderr
grep -q 'line 1' /tmp/gemini-embedded-quote.stderr
expect_stderr_to_omit /tmp/gemini-embedded-quote.stderr 'embedded-quote-secret'

printf 'export\tGEMINI_API_KEY=export-tab-secret\n' >"${TMP_DIR}/export-tab.env"
if expect_fatal_function_failure /tmp/gemini-export-tab.stdout /tmp/gemini-export-tab.stderr \
  workcell_validate_gemini_env_auth_config "${TMP_DIR}/export-tab.env"; then
  echo "Expected tab-delimited export Gemini env syntax to be rejected" >&2
  exit 1
fi
grep -q 'Malformed Gemini auth env file' /tmp/gemini-export-tab.stderr
grep -q 'line 1' /tmp/gemini-export-tab.stderr
expect_stderr_to_omit /tmp/gemini-export-tab.stderr 'export-tab-secret'

printf 'GOOGLE_GENAI_USE_GCA=invalid-gca-fallback-secret\nGEMINI_API_KEY=valid-fallback-key\n' >"${TMP_DIR}/invalid-gca-fallback.env"
if expect_fatal_function_failure /tmp/gemini-invalid-gca-fallback.stdout /tmp/gemini-invalid-gca-fallback.stderr \
  workcell_gemini_selected_auth_type "${TMP_DIR}/invalid-gca-fallback.env" "${TMP_DIR}/missing-oauth.json"; then
  echo "Expected invalid Google Code Assist selector to block Gemini API key fallback" >&2
  exit 1
fi
grep -q 'Invalid boolean in Gemini auth env file' /tmp/gemini-invalid-gca-fallback.stderr
grep -q 'GOOGLE_GENAI_USE_GCA' /tmp/gemini-invalid-gca-fallback.stderr
expect_stderr_to_omit /tmp/gemini-invalid-gca-fallback.stderr 'invalid-gca-fallback-secret'
expect_stderr_to_omit /tmp/gemini-invalid-gca-fallback.stderr 'valid-fallback-key'

printf 'GOOGLE_GENAI_USE_GCA="tr\\u0000ue" # selector-nul-secret\nGEMINI_API_KEY=valid-nul-fallback-key\n' >"${TMP_DIR}/nul-selector-fallback.env"
if expect_fatal_function_failure /tmp/gemini-nul-selector-fallback.stdout /tmp/gemini-nul-selector-fallback.stderr \
  workcell_gemini_selected_auth_type "${TMP_DIR}/nul-selector-fallback.env" "${TMP_DIR}/missing-oauth.json"; then
  echo "Expected Gemini selector NUL escapes to block API key fallback" >&2
  exit 1
fi
grep -q 'Malformed Gemini auth env file' /tmp/gemini-nul-selector-fallback.stderr
grep -q 'line 1' /tmp/gemini-nul-selector-fallback.stderr
expect_stderr_to_omit /tmp/gemini-nul-selector-fallback.stderr 'selector-nul-secret'
expect_stderr_to_omit /tmp/gemini-nul-selector-fallback.stderr 'valid-nul-fallback-key'

{
  printf 'GOOGLE_GENAI_USE_GCA=tr'
  printf '\0'
  printf '%s\n' 'ue # raw-nul-selector-secret'
  printf '%s\n' 'GEMINI_API_KEY=valid-raw-nul-fallback-key'
} >"${TMP_DIR}/raw-nul-selector-fallback.env"
if expect_fatal_function_failure /tmp/gemini-raw-nul-selector-fallback.stdout /tmp/gemini-raw-nul-selector-fallback.stderr \
  workcell_gemini_selected_auth_type "${TMP_DIR}/raw-nul-selector-fallback.env" "${TMP_DIR}/missing-oauth.json"; then
  echo "Expected raw Gemini selector NUL bytes to block API key fallback" >&2
  exit 1
fi
grep -q 'Malformed Gemini auth env file' /tmp/gemini-raw-nul-selector-fallback.stderr
expect_stderr_to_omit /tmp/gemini-raw-nul-selector-fallback.stderr 'raw-nul-selector-secret'
expect_stderr_to_omit /tmp/gemini-raw-nul-selector-fallback.stderr 'valid-raw-nul-fallback-key'

printf 'GOOGLE_GENAI_USE_VERTEXAI=invalid-vertex-fallback-secret\nGEMINI_API_KEY=valid-fallback-key\n' >"${TMP_DIR}/invalid-vertex-fallback.env"
if expect_fatal_function_failure /tmp/gemini-invalid-vertex-fallback.stdout /tmp/gemini-invalid-vertex-fallback.stderr \
  workcell_gemini_selected_auth_type "${TMP_DIR}/invalid-vertex-fallback.env" "${TMP_DIR}/missing-oauth.json"; then
  echo "Expected invalid Vertex selector to block Gemini API key fallback" >&2
  exit 1
fi
grep -q 'Invalid boolean in Gemini auth env file' /tmp/gemini-invalid-vertex-fallback.stderr
grep -q 'GOOGLE_GENAI_USE_VERTEXAI' /tmp/gemini-invalid-vertex-fallback.stderr
expect_stderr_to_omit /tmp/gemini-invalid-vertex-fallback.stderr 'invalid-vertex-fallback-secret'
expect_stderr_to_omit /tmp/gemini-invalid-vertex-fallback.stderr 'valid-fallback-key'

printf 'GEMINI_API_KEY="   " # whitespace-only-api-secret\n' >"${TMP_DIR}/whitespace-api-key.env"
printf '{}\n' >"${TMP_DIR}/whitespace-api-key-oauth.json"
if expect_fatal_function_failure /tmp/gemini-whitespace-api-key.stdout /tmp/gemini-whitespace-api-key.stderr \
  workcell_gemini_selected_auth_type "${TMP_DIR}/whitespace-api-key.env" "${TMP_DIR}/whitespace-api-key-oauth.json"; then
  echo "Expected whitespace-only Gemini API keys to block OAuth fallback" >&2
  exit 1
fi
grep -q 'sets GEMINI_API_KEY but leaves it empty' /tmp/gemini-whitespace-api-key.stderr
expect_stderr_to_omit /tmp/gemini-whitespace-api-key.stderr 'whitespace-only-api-secret'

for unicode_whitespace_escape in '\u0085' '\u00A0' '\u2007' '\u202F'; do
  printf 'GEMINI_API_KEY="%s" # unicode-whitespace-api-secret\n' "${unicode_whitespace_escape}" >"${TMP_DIR}/unicode-whitespace-api-key.env"
  printf '{}\n' >"${TMP_DIR}/unicode-whitespace-api-key-oauth.json"
  if expect_fatal_function_failure /tmp/gemini-unicode-whitespace-api-key.stdout /tmp/gemini-unicode-whitespace-api-key.stderr \
    workcell_gemini_selected_auth_type "${TMP_DIR}/unicode-whitespace-api-key.env" "${TMP_DIR}/unicode-whitespace-api-key-oauth.json"; then
    echo "Expected Unicode whitespace-only Gemini API keys to block OAuth fallback" >&2
    exit 1
  fi
done
grep -q 'sets GEMINI_API_KEY but leaves it empty' /tmp/gemini-unicode-whitespace-api-key.stderr
expect_stderr_to_omit /tmp/gemini-unicode-whitespace-api-key.stderr 'unicode-whitespace-api-secret'

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
