#!/bin/bash -p
# shellcheck shell=bash
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${HOME:-/tmp}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
unset BASH_ENV ENV
set -Eeuo pipefail

report_verify_invariants_failure() {
  local status="$1"
  local line="$2"
  local command="$3"
  local caller_frame=""

  if [[ "${FUNCNAME[1]:-}" == "rg" ]] || [[ "${VERIFY_INVARIANTS_EXPECTED_FAILURE:-0}" -eq 1 ]]; then
    return 0
  fi

  caller_frame="$(caller 0 2>/dev/null || true)"
  if [[ -n "${caller_frame}" ]]; then
    echo "verify-invariants failed with status ${status} at ${caller_frame}: ${command}" >&2
  else
    echo "verify-invariants failed with status ${status} at line ${line}: ${command}" >&2
  fi
}

trap 'report_verify_invariants_failure "$?" "${LINENO}" "${BASH_COMMAND}"' ERR

assert_output_did_not_start_colima() {
  local output_path="$1"
  local context="$2"

  if grep -Eq 'Starting managed Colima profile|starting colima' "${output_path}"; then
    echo "${context}" >&2
    cat "${output_path}" >&2
    exit 1
  fi
}

assert_output_contains() {
  local needle="$1"
  local output_path="$2"
  local context="$3"

  if ! grep -Fq -- "${needle}" "${output_path}"; then
    echo "${context}" >&2
    cat "${output_path}" >&2
    exit 1
  fi
}

assert_output_matches_regex() {
  local regex="$1"
  local output_path="$2"
  local context="$3"

  if ! grep -Eq -- "${regex}" "${output_path}"; then
    echo "${context}" >&2
    cat "${output_path}" >&2
    exit 1
  fi
}

script_supports_command_flag() {
  local script_help=""

  script_help="$(script --help 2>&1 || true)"
  grep -q -- ' -c, --command ' <<<"${script_help}"
}

run_typescript_probe_with_timeout() {
  local timeout_seconds="$1"
  local transcript_path="$2"
  shift 2
  local -a command_args=("$@")
  local command_string=""

  if script_supports_command_flag; then
    printf -v command_string '%q ' "${command_args[@]}"
    timeout "${timeout_seconds}" script -qef -c "${command_string% }" "${transcript_path}" </dev/null >/dev/null 2>&1
    return
  fi

  timeout "${timeout_seconds}" script -qeF "${transcript_path}" "${command_args[@]}" </dev/null >/dev/null 2>&1
}

free_bytes_for_path() {
  local target_path="$1"
  /bin/df -Pk "${target_path}" | awk 'NR==2 {print $4 * 1024}'
}

export PATH="${TRUSTED_HOST_PATH}"
require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_tool go
require_tool jq

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

go_verify_metadatautil() {
  run_go_in_repo "${ROOT_DIR}" run ./cmd/workcell-metadatautil "$@"
}

go_verify_hostutil() {
  run_go_in_repo "${ROOT_DIR}" run ./cmd/workcell-hostutil "$@"
}

HOST_GATE_SCRIPTS=(
  "${ROOT_DIR}/scripts/build-and-test.sh"
  "${ROOT_DIR}/scripts/check-pinned-inputs.sh"
  "${ROOT_DIR}/scripts/container-smoke.sh"
  "${ROOT_DIR}/scripts/generate-build-input-manifest.sh"
  "${ROOT_DIR}/scripts/generate-control-plane-manifest.sh"
  "${ROOT_DIR}/scripts/generate-builder-environment-manifest.sh"
  "${ROOT_DIR}/scripts/generate-release-checksums.sh"
  "${ROOT_DIR}/scripts/publish-provider-bump-pr.sh"
  "${ROOT_DIR}/scripts/update-upstream-pins.sh"
  "${ROOT_DIR}/scripts/update-provider-pins.sh"
  "${ROOT_DIR}/scripts/publish-github-release.sh"
  "${ROOT_DIR}/scripts/verify-build-input-manifest.sh"
  "${ROOT_DIR}/scripts/verify-control-plane-manifest.sh"
  "${ROOT_DIR}/scripts/verify-github-macos-release-test-runners.sh"
  "${ROOT_DIR}/scripts/verify-release-bundle.sh"
  "${ROOT_DIR}/scripts/verify-reproducible-build.sh"
  "${ROOT_DIR}/scripts/verify-upstream-codex-release.sh"
  "${ROOT_DIR}/scripts/verify-upstream-gemini-release.sh"
)
REPO_PRECOMMIT_HOOK="${ROOT_DIR}/.githooks/pre-commit"
if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "verify-invariants-entrypoint-ok"
  exit 0
fi

REAL_HOME="$(
  printf '%s\n' ~
)"
CODEX_VERIFY_HOME="$(mktemp -d)"
BARRIER_VERIFY_ROOT="$(mktemp -d)"
BROWSER_PROFILE_FIXTURE=""
COLIMA_PROFILE_FIXTURE=""
INSTALL_VERIFY_HOME="$(mktemp -d)"
ROOT_DRY_RUN_PROFILE_NAME="$(
  workspace="$(cd "${ROOT_DIR}" && pwd -P)"
  slug="$(printf '%s' "${workspace##*/}" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9]+/-/g; s/^-+|-+$//g; s/^$/workspace/' | cut -c1-10)"
  digest="$(go_verify_hostutil launcher workspace-cache-key "${workspace}" | cut -c1-8)"
  printf 'wcl-%s-%s\n' "${slug}" "${digest}"
)"
ROOT_DRY_RUN_PROFILE_DIR="${REAL_HOME}/.colima/${ROOT_DRY_RUN_PROFILE_NAME}"
ROOT_DRY_RUN_LIMA_DIR="${REAL_HOME}/.colima/_lima/colima-${ROOT_DRY_RUN_PROFILE_NAME}"
LIVE_DEBUG_PROFILE_NAME=""
LIVE_DETACHED_PROFILE_NAME=""
AUDIT_RESTORE_PROFILE_NAME=""
STRICT_REFRESH_PROFILE_NAME=""
STRICT_PREFLIGHT_PROFILE=""
DEBUG_LOG_PROFILE=""
TRANSCRIPT_LOG_PROFILE=""
BROKEN_DEBUG_POINTER_PROFILE=""
UNMANAGED_PROFILE_NAME=""
DETACHED_SESSION_ID=""
DETACHED_SESSION_WORKSPACE=""
DETACHED_SESSION_SOURCE_SENTINEL_PATH=""
VERIFY_INVARIANTS_CLEANUP_ACTIVE=0

delete_verify_colima_profile() {
  local profile_name="$1"

  [[ -n "${profile_name}" ]] || return 0
  if [[ -x /opt/homebrew/bin/colima ]]; then
    /opt/homebrew/bin/colima delete --profile "${profile_name}" --force >/dev/null 2>&1 || true
  elif [[ -x /usr/local/bin/colima ]]; then
    /usr/local/bin/colima delete --profile "${profile_name}" --force >/dev/null 2>&1 || true
  fi
  rm -rf \
    "${REAL_HOME}/.colima/${profile_name}" \
    "${REAL_HOME}/.colima/_lima/colima-${profile_name}" \
    "${REAL_HOME}/.colima/_lima/_disks/colima-${profile_name}"
}

cleanup_detached_session_runtime() {
  local session_parent=""

  if [[ -n "${DETACHED_SESSION_ID}" ]]; then
    "${ROOT_DIR}/scripts/workcell" \
      session stop \
      --id "${DETACHED_SESSION_ID}" \
      --force >/dev/null 2>&1 || true
  fi
  if [[ -n "${DETACHED_SESSION_WORKSPACE}" ]]; then
    session_parent="$(dirname "${DETACHED_SESSION_WORKSPACE}")"
    rm -rf "${session_parent}" 2>/dev/null || true
  fi
  if [[ -n "${DETACHED_SESSION_SOURCE_SENTINEL_PATH}" ]]; then
    rm -f "${DETACHED_SESSION_SOURCE_SENTINEL_PATH}" 2>/dev/null || true
  fi
}

file_mode_octal() {
  local path="$1"

  if stat -f '%Lp' "${path}" >/dev/null 2>&1; then
    stat -f '%Lp' "${path}"
  else
    stat -c '%a' "${path}"
  fi
}

extract_top_level_bash_function() {
  local source_file="$1"
  local function_name="$2"

  awk -v function_name="${function_name}" '
    $0 ~ "^" function_name "\\(\\) \\{" { capture = 1 }
    capture { print }
    capture && $0 == "}" { exit }
  ' "${source_file}"
}

make_tree_user_writable_safely() {
  local target_path="$1"

  [[ -e "${target_path}" || -L "${target_path}" ]] || return 0
  if [[ -L "${target_path}" ]]; then
    chmod -h u+w "${target_path}" 2>/dev/null || true
    return 0
  fi

  find -P "${target_path}" -type d -exec chmod u+w {} + 2>/dev/null || true
  find -P "${target_path}" -type f -exec chmod u+w {} + 2>/dev/null || true
  chmod u+w "${target_path}" 2>/dev/null || true
}

remove_tree_safely() {
  local target_path="$1"

  [[ -e "${target_path}" || -L "${target_path}" ]] || return 0
  make_tree_user_writable_safely "${target_path}"
  rm -rf "${target_path}"
}

run_safe_remove_self_test() {
  local test_root=""
  local managed_root=""
  local nested_dir=""
  local outside_root=""
  local outside_file=""
  local before_mode=""
  local after_mode=""

  test_root="$(mktemp -d "${TMPDIR:-/tmp}/workcell-verify-safe-remove.XXXXXX")"
  managed_root="${test_root}/managed-root"
  nested_dir="${managed_root}/nested"
  outside_root="${test_root}/outside"
  outside_file="${outside_root}/keep.txt"
  mkdir -p "${nested_dir}" "${outside_root}"
  printf 'outside\n' >"${outside_file}"
  chmod 0600 "${outside_file}"
  printf 'managed\n' >"${nested_dir}/readonly.txt"
  ln -s "${outside_file}" "${managed_root}/escape-link"
  chmod 0555 "${managed_root}" "${nested_dir}"
  chmod 0444 "${nested_dir}/readonly.txt"

  before_mode="$(file_mode_octal "${outside_file}")"
  remove_tree_safely "${managed_root}"
  after_mode="$(file_mode_octal "${outside_file}")"

  [[ ! -e "${managed_root}" ]] || {
    echo "Expected remove_tree_safely to remove the managed tree" >&2
    rm -rf "${test_root}"
    return 1
  }
  [[ -f "${outside_file}" ]] || {
    echo "Expected remove_tree_safely to leave external targets intact" >&2
    rm -rf "${test_root}"
    return 1
  }
  [[ "${before_mode}" == "${after_mode}" ]] || {
    echo "Expected remove_tree_safely to avoid chmodding symlink targets" >&2
    printf 'before=%s after=%s\n' "${before_mode}" "${after_mode}" >&2
    rm -rf "${test_root}"
    return 1
  }

  rm -rf "${test_root}"
}

if [[ "${1:-}" == "--self-safe-remove-probe" ]]; then
  run_safe_remove_self_test
  echo "verify-invariants-safe-remove-ok"
  exit 0
fi

cleanup() {
  [[ "${VERIFY_INVARIANTS_CLEANUP_ACTIVE}" -eq 0 ]] || return 0
  VERIFY_INVARIANTS_CLEANUP_ACTIVE=1
  trap - EXIT ERR
  set +e

  cleanup_detached_session_runtime
  delete_verify_colima_profile "${LIVE_DEBUG_PROFILE_NAME:-}"
  delete_verify_colima_profile "${LIVE_DETACHED_PROFILE_NAME:-}"
  delete_verify_colima_profile "${AUDIT_RESTORE_PROFILE_NAME:-}"
  delete_verify_colima_profile "${STRICT_REFRESH_PROFILE_NAME:-}"
  delete_verify_colima_profile "${STRICT_PREFLIGHT_PROFILE:-}"
  delete_verify_colima_profile "${DEBUG_LOG_PROFILE:-}"
  delete_verify_colima_profile "${TRANSCRIPT_LOG_PROFILE:-}"
  delete_verify_colima_profile "${BROKEN_DEBUG_POINTER_PROFILE:-}"
  delete_verify_colima_profile "${UNMANAGED_PROFILE_NAME:-}"
  remove_tree_safely "${CODEX_VERIFY_HOME}"
  remove_tree_safely "${BARRIER_VERIFY_ROOT}"
  remove_tree_safely "${INSTALL_VERIFY_HOME}"
  if [[ -n "${BROWSER_PROFILE_FIXTURE}" ]] && [[ -d "${BROWSER_PROFILE_FIXTURE}" ]]; then
    rmdir "${BROWSER_PROFILE_FIXTURE}" 2>/dev/null || true
  fi
  if [[ -n "${COLIMA_PROFILE_FIXTURE}" ]] && [[ -d "${COLIMA_PROFILE_FIXTURE}" ]]; then
    rm -rf "${COLIMA_PROFILE_FIXTURE}"
  fi
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
  local status=0

  if builtin type -P rg >/dev/null 2>&1; then
    set +E
    set +e
    command rg "$@"
    status=$?
    set -e
    set -E
    return "${status}"
  fi

  if [[ "${1:-}" == "-q" ]] && [[ "$#" -eq 3 ]]; then
    set +E
    set +e
    grep -Eq -- "$2" "$3"
    status=$?
    set -e
    set -E
    return "${status}"
  fi

  if [[ "${1:-}" == "-q" ]] && [[ "${2:-}" == "--" ]] && [[ "$#" -eq 4 ]]; then
    set +E
    set +e
    grep -Eq -- "$3" "$4"
    status=$?
    set -e
    set -E
    return "${status}"
  fi

  echo "rg fallback only supports 'rg -q PATTERN FILE' or 'rg -q -- PATTERN FILE'" >&2
  return 127
}

canonicalize_verify_tool_path() {
  local candidate="$1"
  go_verify_metadatautil canonicalize-path "${candidate}"
}

verify_tool_path_is_trusted() {
  local candidate="$1"
  local workspace_root="${2:-}"
  local trusted_prefixes=(
    /usr/bin
    /bin
    /usr/sbin
    /sbin
    /usr/local/bin
    /usr/local/Cellar
    /opt/homebrew/bin
    /opt/homebrew/Cellar
    /Applications/Docker.app/Contents/Resources/bin
  )
  local prefix=""

  [[ "${candidate}" = /* ]] || return 1
  case "${candidate}" in
    "${ROOT_DIR}" | "${ROOT_DIR}"/*)
      return 1
      ;;
  esac
  if [[ -n "${workspace_root}" ]]; then
    case "${candidate}" in
      "${workspace_root}" | "${workspace_root}"/*)
        return 1
        ;;
    esac
  fi
  for prefix in "${trusted_prefixes[@]}"; do
    case "${candidate}" in
      "${prefix}" | "${prefix}"/*)
        return 0
        ;;
    esac
  done

  return 1
}

doctor_tool_is_available() {
  local workspace_root="$1"
  shift
  local name="$1"
  shift
  local candidate=""
  local canonical_candidate=""

  for candidate in "$@"; do
    canonical_candidate="$(canonicalize_verify_tool_path "${candidate}")"
    if [[ -x "${candidate}" ]] &&
      verify_tool_path_is_trusted "${candidate}" "${workspace_root}" &&
      verify_tool_path_is_trusted "${canonical_candidate}" "${workspace_root}"; then
      return 0
    fi
  done

  candidate="$(type -P "${name}" || true)"
  canonical_candidate="$(canonicalize_verify_tool_path "${candidate}")"
  if [[ -n "${candidate}" ]] &&
    verify_tool_path_is_trusted "${candidate}" "${workspace_root}" &&
    verify_tool_path_is_trusted "${canonical_candidate}" "${workspace_root}"; then
    return 0
  fi

  return 1
}

expected_doctor_missing_host_tools_csv_for_workspace() {
  local workspace_root="$1"
  local -a missing=()

  doctor_tool_is_available "${workspace_root}" colima /opt/homebrew/bin/colima /usr/local/bin/colima || missing+=(colima)
  doctor_tool_is_available "${workspace_root}" docker /opt/homebrew/bin/docker /usr/local/bin/docker /Applications/Docker.app/Contents/Resources/bin/docker || missing+=(docker)

  if ((${#missing[@]} == 0)); then
    printf 'none\n'
    return 0
  fi

  local IFS=','
  printf '%s\n' "${missing[*]}"
}

assert_doctor_missing_host_tools() {
  local file="$1"
  local expected="$2"

  if ! grep -q "^doctor_missing_host_tools=${expected}$" "${file}"; then
    echo "Expected ${file} to report doctor_missing_host_tools=${expected}" >&2
    cat "${file}" >&2
    exit 1
  fi
}

assert_doctor_next_for_prepare() {
  local file="$1"
  local expected_missing="$2"

  if [[ "${expected_missing}" == "none" ]]; then
    if ! grep -q -- '--prepare' "${file}"; then
      echo "Expected ${file} to recommend --prepare when required host tools are present" >&2
      cat "${file}" >&2
      exit 1
    fi
    return 0
  fi

  if ! grep -q '^doctor_recommended_next=install-host-tools$' "${file}"; then
    echo "Expected ${file} to recommend installing missing host tools before prepare" >&2
    cat "${file}" >&2
    exit 1
  fi
}

assert_doctor_next_for_missing_workspace() {
  local file="$1"
  local expected_missing="$2"

  if [[ "${expected_missing}" == "none" ]]; then
    if ! grep -q '^doctor_recommended_next=fix-workspace$' "${file}"; then
      echo "Expected ${file} to recommend fixing the missing workspace when required host tools are present" >&2
      cat "${file}" >&2
      exit 1
    fi
    return 0
  fi

  if ! grep -q '^doctor_recommended_next=install-host-tools$' "${file}"; then
    echo "Expected ${file} to prioritize installing missing host tools before fixing the workspace" >&2
    cat "${file}" >&2
    exit 1
  fi
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
  "${ROOT_DIR}/scripts/lib/render_injection_bundle" \
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

toml_section_assignments() {
  local file="$1"
  local section="$2"

  awk -v want="${section}" '
    function trim(value) {
      sub(/^[[:space:]]+/, "", value)
      sub(/[[:space:]]+$/, "", value)
      return value
    }

    function hex_value(ch) {
      if (ch >= "0" && ch <= "9") {
        return ch + 0
      }
      ch = tolower(ch)
      if (ch >= "a" && ch <= "f") {
        return index("abcdef", ch) + 9
      }
      return -1
    }

    function decode_toml_basic_string(value, i, ch, escaped, hex, code, digit, j) {
      escaped = ""

      for (i = 1; i <= length(value); i++) {
        ch = substr(value, i, 1)
        if (ch != "\\") {
          escaped = escaped ch
          continue
        }

        i++
        if (i > length(value)) {
          parse_error = 1
          return value
        }
        ch = substr(value, i, 1)

        if (ch == "b") {
          escaped = escaped sprintf("%c", 8)
        } else if (ch == "t") {
          escaped = escaped sprintf("%c", 9)
        } else if (ch == "n") {
          escaped = escaped sprintf("%c", 10)
        } else if (ch == "f") {
          escaped = escaped sprintf("%c", 12)
        } else if (ch == "r") {
          escaped = escaped sprintf("%c", 13)
        } else if (ch == "\"" || ch == "\\") {
          escaped = escaped ch
        } else if (ch == "u" || ch == "U") {
          hex = substr(value, i + 1, (ch == "u" ? 4 : 8))
          if (length(hex) != (ch == "u" ? 4 : 8)) {
            parse_error = 1
            return value
          }
          code = 0
          for (j = 1; j <= length(hex); j++) {
            digit = hex_value(substr(hex, j, 1))
            if (digit < 0) {
              parse_error = 1
              return value
            }
            code = (code * 16) + digit
          }
          escaped = escaped sprintf("%c", code)
          i += length(hex)
        } else {
          parse_error = 1
          return value
        }
      }

      return escaped
    }

    function normalize_toml_segment(value, first, last) {
      value = trim(value)
      first = substr(value, 1, 1)
      last = substr(value, length(value), 1)

      if (first == "\"" && last == "\"") {
        value = decode_toml_basic_string(substr(value, 2, length(value) - 2))
      } else if (first == "'"'"'" && last == "'"'"'") {
        value = substr(value, 2, length(value) - 2)
      }

      gsub(/\\/, "\\\\", value)
      gsub(/\./, "\\.", value)
      return value
    }

    function normalize_toml_name(value, i, ch, prev, quote, segment, normalized) {
      value = trim(value)
      quote = ""
      segment = ""
      normalized = ""

      for (i = 1; i <= length(value); i++) {
        ch = substr(value, i, 1)
        prev = (i > 1 ? substr(value, i - 1, 1) : "")

        if (quote != "") {
          segment = segment ch
          if (ch == quote && prev != "\\") {
            quote = ""
          }
          continue
        }

        if (ch == "\"" || ch == "'"'"'" ) {
          quote = ch
          segment = segment ch
          continue
        }

        if (ch == ".") {
          segment = normalize_toml_segment(segment)
          if (normalized != "") {
            normalized = normalized "."
          }
          normalized = normalized segment
          segment = ""
          continue
        }

        segment = segment ch
      }

      segment = normalize_toml_segment(segment)
      if (normalized != "") {
        normalized = normalized "."
      }
      normalized = normalized segment
      return normalized
    }

    BEGIN {
      parse_error = 0
      current = "__top__"
      if (want == "") {
        want = "__top__"
      } else {
        want = normalize_toml_name(want)
      }
    }

    {
      line = $0
      sub(/[[:space:]]+#.*$/, "", line)

      if (line ~ /^[[:space:]]*$/) {
        next
      }

      if (line ~ /^[[:space:]]*\[/) {
        current = line
        gsub(/^[[:space:]]*\[/, "", current)
        gsub(/\][[:space:]]*$/, "", current)
        current = normalize_toml_name(current)
        next
      }

      if (current != want) {
        next
      }

      if (line !~ /=/) {
        next
      }

      split(line, parts, "=")
      key = normalize_toml_name(parts[1])
      value = trim(substr(line, index(line, "=") + 1))
      print key "=" value
    }
    END {
      if (parse_error) {
        exit 2
      }
    }
  ' "${file}"
}

toml_section_names() {
  local file="$1"

  awk '
    function trim(value) {
      sub(/^[[:space:]]+/, "", value)
      sub(/[[:space:]]+$/, "", value)
      return value
    }

    function hex_value(ch) {
      if (ch >= "0" && ch <= "9") {
        return ch + 0
      }
      ch = tolower(ch)
      if (ch >= "a" && ch <= "f") {
        return index("abcdef", ch) + 9
      }
      return -1
    }

    function decode_toml_basic_string(value, i, ch, escaped, hex, code, digit, j) {
      escaped = ""

      for (i = 1; i <= length(value); i++) {
        ch = substr(value, i, 1)
        if (ch != "\\") {
          escaped = escaped ch
          continue
        }

        i++
        if (i > length(value)) {
          parse_error = 1
          return value
        }
        ch = substr(value, i, 1)

        if (ch == "b") {
          escaped = escaped sprintf("%c", 8)
        } else if (ch == "t") {
          escaped = escaped sprintf("%c", 9)
        } else if (ch == "n") {
          escaped = escaped sprintf("%c", 10)
        } else if (ch == "f") {
          escaped = escaped sprintf("%c", 12)
        } else if (ch == "r") {
          escaped = escaped sprintf("%c", 13)
        } else if (ch == "\"" || ch == "\\") {
          escaped = escaped ch
        } else if (ch == "u" || ch == "U") {
          hex = substr(value, i + 1, (ch == "u" ? 4 : 8))
          if (length(hex) != (ch == "u" ? 4 : 8)) {
            parse_error = 1
            return value
          }
          code = 0
          for (j = 1; j <= length(hex); j++) {
            digit = hex_value(substr(hex, j, 1))
            if (digit < 0) {
              parse_error = 1
              return value
            }
            code = (code * 16) + digit
          }
          escaped = escaped sprintf("%c", code)
          i += length(hex)
        } else {
          parse_error = 1
          return value
        }
      }

      return escaped
    }

    function normalize_toml_segment(value, first, last) {
      value = trim(value)
      first = substr(value, 1, 1)
      last = substr(value, length(value), 1)

      if (first == "\"" && last == "\"") {
        value = decode_toml_basic_string(substr(value, 2, length(value) - 2))
      } else if (first == "'"'"'" && last == "'"'"'") {
        value = substr(value, 2, length(value) - 2)
      }

      gsub(/\\/, "\\\\", value)
      gsub(/\./, "\\.", value)
      return value
    }

    function normalize_toml_name(value, i, ch, prev, quote, segment, normalized) {
      value = trim(value)
      quote = ""
      segment = ""
      normalized = ""

      for (i = 1; i <= length(value); i++) {
        ch = substr(value, i, 1)
        prev = (i > 1 ? substr(value, i - 1, 1) : "")

        if (quote != "") {
          segment = segment ch
          if (ch == quote && prev != "\\") {
            quote = ""
          }
          continue
        }

        if (ch == "\"" || ch == "'"'"'" ) {
          quote = ch
          segment = segment ch
          continue
        }

        if (ch == ".") {
          segment = normalize_toml_segment(segment)
          if (normalized != "") {
            normalized = normalized "."
          }
          normalized = normalized segment
          segment = ""
          continue
        }

        segment = segment ch
      }

      segment = normalize_toml_segment(segment)
      if (normalized != "") {
        normalized = normalized "."
      }
      normalized = normalized segment
      return normalized
    }

    BEGIN {
      parse_error = 0
    }

    {
      line = $0
      sub(/[[:space:]]+#.*$/, "", line)

      if (line !~ /^[[:space:]]*\[/) {
        next
      }

      section = line
      gsub(/^[[:space:]]*\[/, "", section)
      gsub(/\][[:space:]]*$/, "", section)
      print normalize_toml_name(section)
    }
    END {
      if (parse_error) {
        exit 2
      }
    }
  ' "${file}"
}

require_toml_assignment() {
  local file="$1"
  local section="$2"
  local key="$3"
  local expected="$4"
  local actual=""

  actual="$(
    toml_section_assignments "${file}" "${section}" | awk -F= -v want="${key}" '
      $1 == want {
        print substr($0, length($1) + 2)
        found = 1
        exit
      }
      END {
        if (!found) {
          exit 1
        }
      }
    '
  )" || {
    echo "Expected ${file} section [${section:-top-level}] to define ${key}" >&2
    return 1
  }

  if [[ "${actual}" != "${expected}" ]]; then
    echo "Expected ${file} section [${section:-top-level}] to set ${key}=${expected}, got ${actual}" >&2
    return 1
  fi
}

require_toml_key_absent() {
  local file="$1"
  local section="$2"
  local key="$3"
  local actual_keys=""

  actual_keys="$(toml_section_assignments "${file}" "${section}" | cut -d= -f1)" || return 1

  if printf '%s\n' "${actual_keys}" | grep -Fxq -- "${key}"; then
    echo "Expected ${file} section [${section:-top-level}] not to define ${key}" >&2
    return 1
  fi
}

require_toml_exact_keys() {
  local file="$1"
  local section="$2"
  local tmpdir=""
  local expected_keys=""
  local actual_keys=""
  shift 2

  tmpdir="$(mktemp -d)"
  expected_keys="${tmpdir}/expected"
  actual_keys="${tmpdir}/actual"

  printf '%s\n' "$@" | sort >"${expected_keys}"
  if ! toml_section_assignments "${file}" "${section}" | cut -d= -f1 | sort >"${actual_keys}"; then
    rm -rf "${tmpdir}"
    return 1
  fi

  if ! diff -u "${expected_keys}" "${actual_keys}" >/dev/null; then
    echo "Expected ${file} section [${section:-top-level}] to contain the exact reviewed key set" >&2
    diff -u "${expected_keys}" "${actual_keys}" >&2 || true
    rm -rf "${tmpdir}"
    return 1
  fi

  rm -rf "${tmpdir}"
}

require_toml_section_absent() {
  local file="$1"
  local section="$2"
  local sections=""

  sections="$(toml_section_names "${file}")" || return 1

  if printf '%s\n' "${sections}" | grep -Fxq -- "${section}"; then
    echo "Expected ${file} not to define [${section}]" >&2
    return 1
  fi
}

verify_codex_managed_config_invariants() {
  local file="$1"

  require_toml_assignment "${file}" "" "profile" '"strict"' || return 1
  require_toml_key_absent "${file}" "" "sandbox" || return 1
  require_toml_key_absent "${file}" "" "sandbox_mode" || return 1
  require_toml_key_absent "${file}" "" "sandbox_permissions" || return 1
  require_toml_key_absent "${file}" "" "approval_policy" || return 1

  require_toml_exact_keys "${file}" "sandbox_workspace_write" \
    "exclude_slash_tmp" \
    "exclude_tmpdir_env_var" \
    "network_access" || return 1
  require_toml_assignment "${file}" "sandbox_workspace_write" "exclude_slash_tmp" "true" || return 1
  require_toml_assignment "${file}" "sandbox_workspace_write" "exclude_tmpdir_env_var" "false" || return 1
  require_toml_assignment "${file}" "sandbox_workspace_write" "network_access" "false" || return 1

  require_toml_exact_keys "${file}" "features" "unified_exec" || return 1
  require_toml_assignment "${file}" "features" "unified_exec" "false" || return 1

  require_toml_exact_keys "${file}" "profiles.strict" \
    "approval_policy" \
    "sandbox_mode" \
    "web_search" || return 1
  require_toml_assignment "${file}" "profiles.strict" "sandbox_mode" '"workspace-write"' || return 1
  require_toml_assignment "${file}" "profiles.strict" "approval_policy" '"on-request"' || return 1
  require_toml_assignment "${file}" "profiles.strict" "web_search" '"disabled"' || return 1
  require_toml_section_absent "${file}" "profiles.strict.sandbox_workspace_write" || return 1

  require_toml_exact_keys "${file}" "profiles.development" \
    "approval_policy" \
    "sandbox_mode" \
    "web_search" || return 1
  require_toml_assignment "${file}" "profiles.development" "sandbox_mode" '"workspace-write"' || return 1
  require_toml_assignment "${file}" "profiles.development" "approval_policy" '"on-request"' || return 1
  require_toml_assignment "${file}" "profiles.development" "web_search" '"disabled"' || return 1
  require_toml_section_absent "${file}" "profiles.development.sandbox_workspace_write" || return 1

  require_toml_exact_keys "${file}" "profiles.build" \
    "approval_policy" \
    "sandbox_mode" \
    "web_search" || return 1
  require_toml_assignment "${file}" "profiles.build" "sandbox_mode" '"workspace-write"' || return 1
  require_toml_assignment "${file}" "profiles.build" "approval_policy" '"never"' || return 1
  require_toml_assignment "${file}" "profiles.build" "web_search" '"disabled"' || return 1
  require_toml_section_absent "${file}" "profiles.build.sandbox_workspace_write" || return 1

  require_toml_exact_keys "${file}" "profiles.breakglass" \
    "approval_policy" \
    "sandbox_mode" \
    "web_search" || return 1
  require_toml_assignment "${file}" "profiles.breakglass" "sandbox_mode" '"danger-full-access"' || return 1
  require_toml_assignment "${file}" "profiles.breakglass" "approval_policy" '"never"' || return 1
  require_toml_assignment "${file}" "profiles.breakglass" "web_search" '"disabled"' || return 1
}

assert_codex_managed_config_rejected() {
  local file="$1"
  local reason="$2"

  if verify_codex_managed_config_invariants "${file}" >/dev/null 2>&1; then
    echo "Expected Codex managed config invariant to reject ${reason}" >&2
    return 1
  fi
}

CODEX_CONFIG="${ROOT_DIR}/adapters/codex/.codex/config.toml"
CODEX_MANAGED_CONFIG="${ROOT_DIR}/adapters/codex/managed_config.toml"
verify_codex_managed_config_invariants "${CODEX_CONFIG}" || exit 1
verify_codex_managed_config_invariants "${CODEX_MANAGED_CONFIG}" || exit 1
require_toml_assignment \
  "${ROOT_DIR}/adapters/codex/requirements.toml" \
  "" \
  "allowed_sandbox_modes" \
  '["workspace-write", "danger-full-access"]' || {
  echo 'Expected adapters/codex/requirements.toml to allow workspace-write for managed sessions and danger-full-access only for breakglass' >&2
  exit 1
}

codex_managed_config_tmpdir="$(mktemp -d)"

quoted_key_config="${codex_managed_config_tmpdir}/quoted-key.toml"
awk '
  {
    print
    if ($0 == "profile = \"strict\"") {
      print "\"approval_policy\" = \"never\""
    }
  }
' "${CODEX_MANAGED_CONFIG}" >"${quoted_key_config}"
assert_codex_managed_config_rejected "${quoted_key_config}" 'quoted top-level approval_policy override' || exit 1

escaped_key_config="${codex_managed_config_tmpdir}/escaped-key.toml"
awk '
  {
    print
    if ($0 == "profile = \"strict\"") {
      print "\"approval\\u005fpolicy\" = \"never\""
    }
  }
' "${CODEX_MANAGED_CONFIG}" >"${escaped_key_config}"
assert_codex_managed_config_rejected "${escaped_key_config}" 'escaped top-level approval_policy override' || exit 1

spaced_section_config="${codex_managed_config_tmpdir}/spaced-section.toml"
cp "${CODEX_MANAGED_CONFIG}" "${spaced_section_config}"
printf '\n[ profiles.strict.sandbox_workspace_write ]\nnetwork_access = true\n' >>"${spaced_section_config}"
assert_codex_managed_config_rejected "${spaced_section_config}" 'whitespace-padded strict sandbox override section' || exit 1

quoted_segment_section_config="${codex_managed_config_tmpdir}/quoted-segment-section.toml"
cp "${CODEX_MANAGED_CONFIG}" "${quoted_segment_section_config}"
printf '\n[ "profiles" . "strict" . "sandbox_workspace_write" ]\nnetwork_access = true\n' >>"${quoted_segment_section_config}"
assert_codex_managed_config_rejected "${quoted_segment_section_config}" 'quoted strict segment sandbox override section' || exit 1

invalid_escape_key_config="${codex_managed_config_tmpdir}/invalid-escape-key.toml"
awk '
  {
    print
    if ($0 == "profile = \"strict\"") {
      print "\"approval\\u00ZZpolicy\" = \"never\""
    }
  }
' "${CODEX_MANAGED_CONFIG}" >"${invalid_escape_key_config}"
assert_codex_managed_config_rejected "${invalid_escape_key_config}" 'malformed escaped approval_policy override' || exit 1

literal_dot_section_config="${codex_managed_config_tmpdir}/literal-dot-section.toml"
cp "${CODEX_MANAGED_CONFIG}" "${literal_dot_section_config}"
printf '\n["profiles.strict.sandbox_workspace_write"]\nnetwork_access = true\n' >>"${literal_dot_section_config}"
if ! verify_codex_managed_config_invariants "${literal_dot_section_config}" >/dev/null 2>&1; then
  echo 'Expected quoted single-segment section names with literal dots to remain distinct from forbidden dotted paths' >&2
  exit 1
fi

rm -rf "${codex_managed_config_tmpdir}"

if ! sed -n '/^run_host_colima()/,/^}/p' "${ROOT_DIR}/scripts/workcell" | grep -Fq "HOME=\"\${REAL_HOME}\""; then
  echo "Expected run_host_colima to restore the real host HOME instead of the Docker client sandbox home" >&2
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

INSTALL_DEPS_VERIFY_BIN="${BARRIER_VERIFY_ROOT}/install-deps-bin"
INSTALL_DEPS_LOG="${BARRIER_VERIFY_ROOT}/install-deps-brew.log"
INSTALL_DEPS_VERIFY_HOME="$(mktemp -d "${BARRIER_VERIFY_ROOT}/install-deps-home.XXXXXX")"
INSTALL_NO_DEPS_VERIFY_HOME="$(mktemp -d "${BARRIER_VERIFY_ROOT}/install-no-deps-home.XXXXXX")"
INSTALL_DEPS_PATH="${INSTALL_DEPS_VERIFY_BIN}"
mkdir -p "${INSTALL_DEPS_VERIFY_BIN}"

for required_tool in basename bash cat chmod dirname ln mkdir rm; do
  required_tool_path="$(command -v "${required_tool}")"
  cat <<EOF >"${INSTALL_DEPS_VERIFY_BIN}/${required_tool}"
#!/bin/bash
set -euo pipefail
exec "${required_tool_path}" "\$@"
EOF
  chmod 0755 "${INSTALL_DEPS_VERIFY_BIN}/${required_tool}"
done

cat <<'EOF' >"${INSTALL_DEPS_VERIFY_BIN}/uname"
#!/bin/bash
set -euo pipefail
case "${1:-}" in
  -s)
    printf 'Darwin\n'
    ;;
  -m)
    printf 'arm64\n'
    ;;
  *)
    printf 'Darwin\n'
    ;;
esac
EOF
chmod 0755 "${INSTALL_DEPS_VERIFY_BIN}/uname"

cat <<'EOF' >"${INSTALL_DEPS_VERIFY_BIN}/dirname"
#!/bin/bash
set -euo pipefail
exec /usr/bin/dirname "$@"
EOF
chmod 0755 "${INSTALL_DEPS_VERIFY_BIN}/dirname"

cat <<'EOF' >"${INSTALL_DEPS_VERIFY_BIN}/basename"
#!/bin/bash
set -euo pipefail
exec /usr/bin/basename "$@"
EOF
chmod 0755 "${INSTALL_DEPS_VERIFY_BIN}/basename"

cat <<'EOF' >"${INSTALL_DEPS_VERIFY_BIN}/sysctl"
#!/bin/bash
set -euo pipefail
if [[ "${1:-}" == "-in" ]] && [[ "${2:-}" == "hw.optional.arm64" ]]; then
  printf '1\n'
  exit 0
fi
exit 1
EOF
chmod 0755 "${INSTALL_DEPS_VERIFY_BIN}/sysctl"

cat <<'EOF' >"${INSTALL_DEPS_VERIFY_BIN}/brew"
#!/bin/bash
set -euo pipefail
if [[ "${1:-}" != "install" ]]; then
  echo "Expected only brew install during installer dependency bootstrap" >&2
  exit 1
fi
shift
printf 'install %s\n' "$*" >"${INSTALL_DEPS_LOG}"
for pkg in "$@"; do
  cat <<'EOFAKE' >"${INSTALL_DEPS_FAKEBIN}/${pkg}"
#!/bin/bash
set -euo pipefail
exit 0
EOFAKE
  chmod 0755 "${INSTALL_DEPS_FAKEBIN}/${pkg}"
done
EOF
chmod 0755 "${INSTALL_DEPS_VERIFY_BIN}/brew"

if ! env -i \
  HOME="${INSTALL_DEPS_VERIFY_HOME}" \
  PATH="${INSTALL_DEPS_PATH}" \
  SHELL=/bin/zsh \
  INSTALL_DEPS_FAKEBIN="${INSTALL_DEPS_VERIFY_BIN}" \
  INSTALL_DEPS_LOG="${INSTALL_DEPS_LOG}" \
  "${ROOT_DIR}/scripts/install-workcell.sh" >/tmp/workcell-install-deps-bootstrap.out 2>&1; then
  echo "Expected scripts/install-workcell.sh to auto-install missing macOS host dependencies through Homebrew" >&2
  cat /tmp/workcell-install-deps-bootstrap.out >&2
  exit 1
fi

grep -q '^install colima docker gh git go$' "${INSTALL_DEPS_LOG}"
grep -q '^Installing required host packages via Homebrew: colima docker gh git go$' /tmp/workcell-install-deps-bootstrap.out
test -L "${INSTALL_DEPS_VERIFY_HOME}/.local/bin/workcell"
test -L "${INSTALL_DEPS_VERIFY_HOME}/.local/share/man/man1/workcell.1"
rm -f \
  "${INSTALL_DEPS_VERIFY_BIN}/colima" \
  "${INSTALL_DEPS_VERIFY_BIN}/docker" \
  "${INSTALL_DEPS_VERIFY_BIN}/gh" \
  "${INSTALL_DEPS_VERIFY_BIN}/git" \
  "${INSTALL_DEPS_VERIFY_BIN}/go"

if ! env -i \
  HOME="${INSTALL_NO_DEPS_VERIFY_HOME}" \
  PATH="${INSTALL_DEPS_PATH}" \
  SHELL=/bin/zsh \
  "${ROOT_DIR}/scripts/install-workcell.sh" --no-install-deps >/tmp/workcell-install-no-deps.out 2>&1; then
  echo "Expected scripts/install-workcell.sh --no-install-deps to install only the launcher and warn at the end" >&2
  cat /tmp/workcell-install-no-deps.out >&2
  exit 1
fi

grep -q 'Workcell warning: the launcher was installed without the full required host toolchain.' /tmp/workcell-install-no-deps.out
grep -q '^Missing required host packages: colima docker gh git go$' /tmp/workcell-install-no-deps.out
grep -q '^  brew install colima docker gh git go$' /tmp/workcell-install-no-deps.out
test -L "${INSTALL_NO_DEPS_VERIFY_HOME}/.local/bin/workcell"
test -L "${INSTALL_NO_DEPS_VERIFY_HOME}/.local/share/man/man1/workcell.1"

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

if ! grep -q -- '--no-spinner' /tmp/workcell-installed-help.out; then
  echo "Expected installed ~/.local/bin/workcell --help to describe --no-spinner" >&2
  exit 1
fi

if ! grep -q 'Provider to run (required)' /tmp/workcell-installed-help.out; then
  echo "Expected installed ~/.local/bin/workcell --help to describe --agent as required" >&2
  exit 1
fi

mkdir -p "${INSTALL_VERIFY_HOME}/.config/workcell"
printf 'version = 1\n' >"${INSTALL_VERIFY_HOME}/.config/workcell/injection-policy.toml"
mkdir -p \
  "${INSTALL_VERIFY_HOME}/.local/state/workcell/tmp" \
  "${INSTALL_VERIFY_HOME}/.colima/workcell-verify-profile" \
  "${INSTALL_VERIFY_HOME}/.colima/_lima/colima-workcell-verify-profile" \
  "${INSTALL_VERIFY_HOME}/.colima/locks/workcell-verify-profile.lock" \
  "${INSTALL_VERIFY_HOME}/Library/Caches/colima/workcell-host-inputs" \
  "${INSTALL_VERIFY_HOME}/Library/Caches/colima/workcell-shadow"
mkdir -p "${INSTALL_VERIFY_HOME}/Library/Caches/colima/workcell-shadow/shadow.readonly/git/.git/hooks"
printf '#!/bin/sh\n' >"${INSTALL_VERIFY_HOME}/Library/Caches/colima/workcell-shadow/shadow.readonly/git/.git/hooks/pre-commit"
chmod 0555 "${INSTALL_VERIFY_HOME}/Library/Caches/colima/workcell-shadow/shadow.readonly/git/.git/hooks"
chmod 0444 "${INSTALL_VERIFY_HOME}/Library/Caches/colima/workcell-shadow/shadow.readonly/git/.git/hooks/pre-commit"
printf '%s\n' "${ROOT_DIR}" >"${INSTALL_VERIFY_HOME}/.colima/workcell-verify-profile/workcell.managed"
printf 'image_tag=workcell:local\nimage_id=sha256:test\nsource_date_epoch=0\n' >"${INSTALL_VERIFY_HOME}/.colima/workcell-verify-profile/workcell.image-ready"
printf 'tmp\n' >"/tmp/workcell-uninstall-verify.log.$$"
mkdir -p "/tmp/workcell-docker.verify-uninstall.$$"

if ! env -i HOME="${INSTALL_VERIFY_HOME}" PATH="${TRUSTED_HOST_PATH}" "${ROOT_DIR}/scripts/uninstall.sh" >/tmp/workcell-uninstall.out 2>&1; then
  echo "Expected scripts/uninstall.sh to succeed in a clean temporary HOME" >&2
  cat /tmp/workcell-uninstall.out >&2
  exit 1
fi

test ! -e "${INSTALL_VERIFY_HOME}/.local/bin/workcell"
test ! -e "${INSTALL_VERIFY_HOME}/.local/share/man/man1/workcell.1"
test ! -e "${INSTALL_VERIFY_HOME}/.local/state/workcell"
test ! -e "${INSTALL_VERIFY_HOME}/.colima/workcell-verify-profile"
test ! -e "${INSTALL_VERIFY_HOME}/.colima/_lima/colima-workcell-verify-profile"
test ! -e "${INSTALL_VERIFY_HOME}/.colima/locks/workcell-verify-profile.lock"
test ! -e "${INSTALL_VERIFY_HOME}/Library/Caches/colima/workcell-host-inputs"
test ! -e "${INSTALL_VERIFY_HOME}/Library/Caches/colima/workcell-shadow"
test -e "${INSTALL_VERIFY_HOME}/.config/workcell/injection-policy.toml"
test ! -e "/tmp/workcell-uninstall-verify.log.$$"
test ! -e "/tmp/workcell-docker.verify-uninstall.$$"
grep -q 'Preserved ~/.config/workcell and any user-specified debug/file-trace/transcript files.' /tmp/workcell-uninstall.out
grep -q 'Preserved shared host packages installed outside Workcell.' /tmp/workcell-uninstall.out

if ! env -i HOME="${INSTALL_VERIFY_HOME}" PATH="${TRUSTED_HOST_PATH}" "${ROOT_DIR}/scripts/install.sh" --debug >/tmp/workcell-install-debug.out 2>&1; then
  echo "Expected scripts/install.sh --debug to succeed in a clean temporary HOME" >&2
  cat /tmp/workcell-install-debug.out >&2
  exit 1
fi

if [[ ! -f "${INSTALL_VERIFY_HOME}/.local/bin/workcell" ]] || [[ -L "${INSTALL_VERIFY_HOME}/.local/bin/workcell" ]]; then
  echo "Expected debug install to write a launcher wrapper script" >&2
  exit 1
fi

grep -q 'DEFAULT_DEBUG_LOG=' "${INSTALL_VERIFY_HOME}/.local/bin/workcell"
grep -q 'EXTRA_ARGS+=(--debug-log ' "${INSTALL_VERIFY_HOME}/.local/bin/workcell"
grep -q 'EXTRA_ARGS+=(--rebuild)' "${INSTALL_VERIFY_HOME}/.local/bin/workcell"

if ! "${INSTALL_VERIFY_HOME}/.local/bin/workcell" --help >/tmp/workcell-installed-debug-help.out 2>&1; then
  echo "Expected debug-installed ~/.local/bin/workcell wrapper to resolve support files correctly" >&2
  cat /tmp/workcell-installed-debug-help.out >&2
  exit 1
fi

if ! grep -q '^Usage: workcell' /tmp/workcell-installed-debug-help.out; then
  echo "Expected debug-installed ~/.local/bin/workcell --help to print usage" >&2
  exit 1
fi

if "${INSTALL_VERIFY_HOME}/.local/bin/workcell" \
  --agent codex \
  --workspace "${ROOT_DIR}" \
  --allow-control-plane-vcs \
  --ack-control-plane-vcs \
  --dry-run >/tmp/workcell-installed-debug-strict-dry-run.out 2>&1; then
  echo "Expected debug-installed ~/.local/bin/workcell strict dry-run to surface the injected --rebuild behavior" >&2
  exit 1
fi
grep -q 'strict mode requires --prepare when you explicitly request --rebuild.' /tmp/workcell-installed-debug-strict-dry-run.out

if ! "${INSTALL_VERIFY_HOME}/.local/bin/workcell" \
  --agent codex \
  --workspace "${ROOT_DIR}" \
  --mode build \
  --allow-control-plane-vcs \
  --ack-control-plane-vcs \
  --dry-run >/tmp/workcell-installed-debug-dry-run.out 2>&1; then
  echo "Expected debug-installed ~/.local/bin/workcell launch path to succeed through dry-run" >&2
  cat /tmp/workcell-installed-debug-dry-run.out >&2
  exit 1
fi
grep -q 'Workcell warning: host-persisted launcher debug stderr capture is enabled' /tmp/workcell-installed-debug-dry-run.out
grep -q "debug_log=${INSTALL_VERIFY_HOME}/.config/workcell/debug/latest-debug.log" /tmp/workcell-installed-debug-dry-run.out

if ! "${INSTALL_VERIFY_HOME}/.local/bin/workcell" \
  --auth-status \
  --agent codex \
  --workspace "${ROOT_DIR}" >/tmp/workcell-installed-debug-auth-status.out 2>&1; then
  echo "Expected debug-installed ~/.local/bin/workcell non-launch path to skip auto debug flags" >&2
  cat /tmp/workcell-installed-debug-auth-status.out >&2
  exit 1
fi
if grep -q -- '--debug-log, --file-trace-log, and --audit-transcript apply only to launched sessions.' /tmp/workcell-installed-debug-auth-status.out; then
  echo "Expected debug-installed ~/.local/bin/workcell to skip auto debug flags on non-launch paths" >&2
  cat /tmp/workcell-installed-debug-auth-status.out >&2
  exit 1
fi

if ! env -i HOME="${INSTALL_VERIFY_HOME}" PATH="${TRUSTED_HOST_PATH}" "${ROOT_DIR}/scripts/uninstall.sh" >/tmp/workcell-uninstall-debug.out 2>&1; then
  echo "Expected scripts/uninstall.sh to remove the debug installer wrapper cleanly" >&2
  cat /tmp/workcell-uninstall-debug.out >&2
  exit 1
fi

test ! -e "${INSTALL_VERIFY_HOME}/.local/bin/workcell"
test ! -e "${INSTALL_VERIFY_HOME}/.local/share/man/man1/workcell.1"
grep -q 'Preserved ~/.config/workcell and any user-specified debug/file-trace/transcript files.' /tmp/workcell-uninstall-debug.out
grep -q 'Preserved shared host packages installed outside Workcell.' /tmp/workcell-uninstall-debug.out

CUSTOM_DEBUG_DIR="${INSTALL_VERIFY_HOME}/custom-workcell-debug"
CUSTOM_DEBUG_DIR_REAL="$(go_verify_metadatautil canonicalize-path "${CUSTOM_DEBUG_DIR}")"
if ! env -i HOME="${INSTALL_VERIFY_HOME}" PATH="${TRUSTED_HOST_PATH}" "${ROOT_DIR}/scripts/install.sh" --debug --debug-dir "${CUSTOM_DEBUG_DIR}" >/tmp/workcell-install-custom-debug.out 2>&1; then
  echo "Expected scripts/install.sh --debug --debug-dir to succeed in a clean temporary HOME" >&2
  cat /tmp/workcell-install-custom-debug.out >&2
  exit 1
fi
if ! "${INSTALL_VERIFY_HOME}/.local/bin/workcell" \
  --agent codex \
  --workspace "${ROOT_DIR}" \
  --mode build \
  --allow-control-plane-vcs \
  --ack-control-plane-vcs \
  --dry-run >/tmp/workcell-installed-custom-debug-dry-run.out 2>&1; then
  echo "Expected debug-installed ~/.local/bin/workcell custom debug dir launch path to succeed through dry-run" >&2
  cat /tmp/workcell-installed-custom-debug-dry-run.out >&2
  exit 1
fi
grep -q "debug_log=${CUSTOM_DEBUG_DIR_REAL}/latest-debug.log" /tmp/workcell-installed-custom-debug-dry-run.out
if ! env -i HOME="${INSTALL_VERIFY_HOME}" PATH="${TRUSTED_HOST_PATH}" "${ROOT_DIR}/scripts/uninstall.sh" >/tmp/workcell-uninstall-custom-debug.out 2>&1; then
  echo "Expected scripts/uninstall.sh to remove the custom debug installer wrapper cleanly" >&2
  cat /tmp/workcell-uninstall-custom-debug.out >&2
  exit 1
fi
grep -q 'Preserved shared host packages installed outside Workcell.' /tmp/workcell-uninstall-custom-debug.out

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
modes = ["strict", "development", "build"]

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

"${ROOT_DIR}/scripts/lib/render_injection_bundle" \
  --policy "${INJECTION_POLICY_FIXTURE_ROOT}/policy.toml" \
  --agent codex \
  --mode strict \
  --output-root "${INJECTION_POLICY_FIXTURE_ROOT}/bundle" >/tmp/workcell-injection-manifest.out
"${ROOT_DIR}/scripts/lib/render_injection_bundle" \
  --policy "${INJECTION_POLICY_FIXTURE_ROOT}/policy.toml" \
  --agent claude \
  --mode strict \
  --output-root "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-claude" >/dev/null
"${ROOT_DIR}/scripts/lib/render_injection_bundle" \
  --policy "${INJECTION_POLICY_FIXTURE_ROOT}/policy.toml" \
  --agent gemini \
  --mode strict \
  --output-root "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-gemini" >/dev/null
"${ROOT_DIR}/scripts/lib/extract_direct_mounts" \
  --manifest "${INJECTION_POLICY_FIXTURE_ROOT}/bundle/manifest.json" \
  --mount-spec "${INJECTION_POLICY_FIXTURE_ROOT}/bundle.mounts.json" >/dev/null

manifest_path="${INJECTION_POLICY_FIXTURE_ROOT}/bundle/manifest.json"
[[ "$(jq -r '.documents.common' "${manifest_path}")" == "documents/common.md" ]]
[[ "$(jq -r '.documents.codex' "${manifest_path}")" == "documents/codex.md" ]]
[[ "$(jq -r '.copies[] | select(.target=="/state/injected/public.txt") | .source' "${manifest_path}")" == "copies/0" ]]
[[ "$(jq -r '.copies[] | select(.target=="/state/agent-home/.config/workcell/token.txt") | .source.mount_path' "${manifest_path}")" == "/opt/workcell/host-inputs/copies/1" ]]
[[ "$(jq -r '.copies[] | select(.target=="/state/agent-home/.config/workcell/token.txt") | has("source")' "${manifest_path}")" == "true" ]]
[[ "$(jq -r '.credentials.codex_auth.mount_path' "${manifest_path}")" == "/opt/workcell/host-inputs/credentials/codex-auth.json" ]]
[[ "$(jq -r '.credentials.codex_auth | has("source")' "${manifest_path}")" == "false" ]]
[[ "$(jq -r '.credentials.github_hosts.mount_path' "${manifest_path}")" == "/opt/workcell/host-inputs/credentials/github-hosts.yml" ]]
[[ "$(jq -r '.ssh.config.mount_path' "${manifest_path}")" == "/opt/workcell/host-inputs/ssh/config" ]]
[[ "$(jq -r '.ssh.config | has("source")' "${manifest_path}")" == "false" ]]
[[ "$(jq -r '.ssh.identities[0].mount_path' "${manifest_path}")" == "/opt/workcell/host-inputs/ssh/identity-0" ]]
[[ "$(jq -r '.ssh.identities[0] | has("source")' "${manifest_path}")" == "false" ]]
[[ "$(jq -r '.ssh.identities[0].target_name' "${manifest_path}")" == "id_test" ]]

if [[ -e "${INJECTION_POLICY_FIXTURE_ROOT}/bundle/credentials/codex-auth.json" ]]; then
  echo "Expected credentials.* sources to mount directly from the host instead of being restaged into the bundle" >&2
  exit 1
fi

actual_mount_paths=()
while IFS= read -r line; do
  [[ -n "${line}" ]] || continue
  actual_mount_paths+=("${line}")
done < <(jq -r '.[].mount_path' "${INJECTION_POLICY_FIXTURE_ROOT}/bundle.mounts.json" | sort -u)
expected_mount_paths=(
  "/opt/workcell/host-inputs/credentials/codex-auth.json"
  "/opt/workcell/host-inputs/credentials/github-hosts.yml"
  "/opt/workcell/host-inputs/copies/1"
  "/opt/workcell/host-inputs/ssh/config"
  "/opt/workcell/host-inputs/ssh/known_hosts"
  "/opt/workcell/host-inputs/ssh/identity-0"
)
for expected_mount_path in "${expected_mount_paths[@]}"; do
  if ! printf '%s\n' "${actual_mount_paths[@]}" | grep -Fxq "${expected_mount_path}"; then
    echo "expected direct-mount spec to preserve all secret input mount paths" >&2
    exit 1
  fi
done

if [[ -e "${INJECTION_POLICY_FIXTURE_ROOT}/bundle/ssh/config" ]]; then
  echo "Expected ssh.* sources to mount directly from the host instead of being restaged into the bundle" >&2
  exit 1
fi

[[ "$(jq -r '.credentials.claude_auth.mount_path' "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-claude/manifest.json")" == "/opt/workcell/host-inputs/credentials/claude-auth.json" ]]
[[ "$(jq -r '.credentials.claude_mcp.mount_path' "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-claude/manifest.json")" == "/opt/workcell/host-inputs/credentials/claude-mcp.json" ]]
[[ "$(jq -r '.credentials.gemini_projects.mount_path' "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-gemini/manifest.json")" == "/opt/workcell/host-inputs/credentials/gemini-projects.json" ]]

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/fragment-docs.toml"
[documents]
common = "common.md"
EOF

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/fragment-credentials.toml"
[credentials]
codex_auth = "codex-auth.json"
EOF

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/policy-with-includes.toml"
version = 1
includes = ["fragment-docs.toml", "fragment-credentials.toml"]

[credentials.github_hosts]
source = "gh-hosts.yml"
providers = ["codex"]
EOF

"${ROOT_DIR}/scripts/lib/render_injection_bundle" \
  --policy "${INJECTION_POLICY_FIXTURE_ROOT}/policy-with-includes.toml" \
  --agent codex \
  --mode strict \
  --output-root "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-includes" >/dev/null

[[ "$(jq -r '.documents.common' "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-includes/manifest.json")" == "documents/common.md" ]]
[[ "$(jq -r '.credentials.codex_auth.mount_path' "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-includes/manifest.json")" == "/opt/workcell/host-inputs/credentials/codex-auth.json" ]]
[[ "$(jq -r '.metadata.policy_sha256' "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-includes/manifest.json")" == sha256:* ]]
included_policy_source_names=()
while IFS= read -r line; do
  [[ -n "${line}" ]] || continue
  included_policy_source_names+=("${line}")
done < <(jq -r '.metadata.policy_sources[].path | split("/")[-1]' "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-includes/manifest.json")
if [[ "${included_policy_source_names[*]}" != "fragment-docs.toml fragment-credentials.toml policy-with-includes.toml" ]]; then
  echo "unexpected included policy source order: ${included_policy_source_names[*]}" >&2
  exit 1
fi
[[ "$(jq -r '.metadata.policy_entrypoint' "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-includes/manifest.json")" == "policy-with-includes.toml" ]]
if jq -r '.metadata.policy_sources[].path' "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-includes/manifest.json" | grep -Eq '^/'; then
  echo "policy source leaked an absolute host path" >&2
  exit 1
fi

mkdir -p "${INJECTION_POLICY_FIXTURE_ROOT}/fragments"
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/fragments/common-fragment.md"
fragment common
EOF
printf '{}\n' >"${INJECTION_POLICY_FIXTURE_ROOT}/fragments/fragment-auth.json"
chmod 0600 "${INJECTION_POLICY_FIXTURE_ROOT}/fragments/fragment-auth.json"
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/fragments/fragment-relative.toml"
[documents]
common = "common-fragment.md"

[credentials]
codex_auth = "fragment-auth.json"
EOF

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/policy-with-nested-includes.toml"
version = 1
includes = ["fragments/fragment-relative.toml"]
EOF

"${ROOT_DIR}/scripts/lib/render_injection_bundle" \
  --policy "${INJECTION_POLICY_FIXTURE_ROOT}/policy-with-nested-includes.toml" \
  --agent codex \
  --mode strict \
  --output-root "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-nested-includes" >/dev/null

[[ "$(jq -r '.documents.common' "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-nested-includes/manifest.json")" == "documents/common.md" ]]
cmp -s "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-nested-includes/documents/common.md" <(printf 'fragment common\n')
expected_auth="$(cd "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-nested-includes/../fragments" && pwd -P)/fragment-auth.json"
[[ "$(jq -r '.credentials.codex_auth.source' "${INJECTION_POLICY_FIXTURE_ROOT}/bundle-nested-includes/manifest.json")" == "${expected_auth}" ]]

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

"${ROOT_DIR}/scripts/verify-control-plane-manifest.sh"

if [[ "${INJECTION_DRY_RUN_OUTPUT}" == *"${INJECTION_POLICY_FIXTURE_ROOT}/codex-auth.json"* ]]; then
  echo "Expected workcell --dry-run to redact host credential source paths" >&2
  exit 1
fi

if [[ "${INJECTION_DRY_RUN_OUTPUT}" != *'WORKCELL_CONTAINER_MUTABILITY=ephemeral'* ]]; then
  echo "Expected workcell --dry-run to default strict mode to ephemeral container mutability" >&2
  exit 1
fi

STALE_INJECTION_BUNDLE="${REAL_HOME}/Library/Caches/colima/workcell-host-inputs/workcell-injections.verify-stale.$$"
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

if "${ROOT_DIR}/scripts/lib/render_injection_bundle" \
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

if "${ROOT_DIR}/scripts/lib/render_injection_bundle" \
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

if "${ROOT_DIR}/scripts/lib/render_injection_bundle" \
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

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/bad-duplicate-key.toml"
version = 1

[credentials]
gemini_env = "gemini.env"
gemini_env = "gemini.env"
EOF

if "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${INJECTION_POLICY_FIXTURE_ROOT}/bad-duplicate-key.toml" \
  --auth-status >/tmp/workcell-injection-bad-duplicate-key.out 2>&1; then
  echo "Expected workcell --auth-status to reject duplicate keys in injection policies" >&2
  exit 1
fi

if ! grep -q 'duplicate key: gemini_env' /tmp/workcell-injection-bad-duplicate-key.out; then
  echo "Expected duplicate-key rejection to name the repeated key" >&2
  cat /tmp/workcell-injection-bad-duplicate-key.out >&2
  exit 1
fi

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/bad-value.toml"
version = 1

[credentials]
gemini_env = gemini.env
EOF

if "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${INJECTION_POLICY_FIXTURE_ROOT}/bad-value.toml" \
  --auth-status >/tmp/workcell-injection-bad-value.out 2>&1; then
  echo "Expected workcell --auth-status to reject unsupported TOML values in injection policies" >&2
  exit 1
fi

if ! grep -q 'unsupported TOML value' /tmp/workcell-injection-bad-value.out; then
  echo "Expected invalid-value rejection to explain the supported TOML subset" >&2
  cat /tmp/workcell-injection-bad-value.out >&2
  exit 1
fi

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/duplicate-fragment.toml"
[credentials]
codex_auth = "codex-auth.json"
EOF

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/duplicate-fragment-root.toml"
version = 1
includes = ["duplicate-fragment.toml"]

[credentials]
codex_auth = "codex-auth.json"
EOF

if "${ROOT_DIR}/scripts/lib/render_injection_bundle" \
  --policy "${INJECTION_POLICY_FIXTURE_ROOT}/duplicate-fragment-root.toml" \
  --agent codex \
  --mode strict \
  --output-root "${INJECTION_POLICY_FIXTURE_ROOT}/duplicate-fragment-bundle" >/tmp/workcell-injection-duplicate-fragment.out 2>&1; then
  echo "Expected injection policy renderer to reject duplicate settings across fragments" >&2
  exit 1
fi

if ! grep -q 'credentials.codex_auth' /tmp/workcell-injection-duplicate-fragment.out; then
  echo "Expected duplicate-fragment rejection to name the duplicated setting" >&2
  cat /tmp/workcell-injection-duplicate-fragment.out >&2
  exit 1
fi

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/cycle-a.toml"
version = 1
includes = ["cycle-b.toml"]
EOF

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/cycle-b.toml"
includes = ["cycle-a.toml"]
EOF

if "${ROOT_DIR}/scripts/lib/render_injection_bundle" \
  --policy "${INJECTION_POLICY_FIXTURE_ROOT}/cycle-a.toml" \
  --agent codex \
  --mode strict \
  --output-root "${INJECTION_POLICY_FIXTURE_ROOT}/cycle-bundle" >/tmp/workcell-injection-cycle.out 2>&1; then
  echo "Expected injection policy renderer to reject include cycles" >&2
  exit 1
fi

if ! grep -q 'include cycle detected' /tmp/workcell-injection-cycle.out; then
  echo "Expected include-cycle rejection to explain the cycle" >&2
  cat /tmp/workcell-injection-cycle.out >&2
  exit 1
fi

mkdir -p "${INJECTION_POLICY_FIXTURE_ROOT}/subdir"
cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/outside.toml"
[documents]
common = "common.md"
EOF

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/subdir/escape-root.toml"
version = 1
includes = ["../outside.toml"]
EOF

if "${ROOT_DIR}/scripts/lib/render_injection_bundle" \
  --policy "${INJECTION_POLICY_FIXTURE_ROOT}/subdir/escape-root.toml" \
  --agent codex \
  --mode strict \
  --output-root "${INJECTION_POLICY_FIXTURE_ROOT}/escape-root-bundle" >/tmp/workcell-injection-escape-root.out 2>&1; then
  echo "Expected injection policy renderer to reject includes that escape the entrypoint root" >&2
  exit 1
fi

if ! grep -q 'must stay within' /tmp/workcell-injection-escape-root.out; then
  echo "Expected escaped-include rejection to explain the entrypoint root boundary" >&2
  cat /tmp/workcell-injection-escape-root.out >&2
  exit 1
fi

VALIDATION_SNAPSHOT_FIXTURE_ROOT="$(mktemp -d)"
git -C "${VALIDATION_SNAPSHOT_FIXTURE_ROOT}" init -q
git -C "${VALIDATION_SNAPSHOT_FIXTURE_ROOT}" config user.email "verify@example.com"
git -C "${VALIDATION_SNAPSHOT_FIXTURE_ROOT}" config user.name "Workcell Verify"
printf 'head\n' >"${VALIDATION_SNAPSHOT_FIXTURE_ROOT}/tracked.txt"
cat <<'EOF' >"${VALIDATION_SNAPSHOT_FIXTURE_ROOT}/snapshot-probe.sh"
#!/bin/bash
set -euo pipefail
printf 'tracked=%s\n' "$(cat tracked.txt)"
if [[ -e untracked.txt ]]; then
  printf 'untracked=%s\n' "$(cat untracked.txt)"
fi
EOF
chmod 0700 "${VALIDATION_SNAPSHOT_FIXTURE_ROOT}/snapshot-probe.sh"
git -C "${VALIDATION_SNAPSHOT_FIXTURE_ROOT}" add tracked.txt
git -C "${VALIDATION_SNAPSHOT_FIXTURE_ROOT}" add snapshot-probe.sh
git -C "${VALIDATION_SNAPSHOT_FIXTURE_ROOT}" commit -qm "initial"
printf 'index\n' >"${VALIDATION_SNAPSHOT_FIXTURE_ROOT}/tracked.txt"
git -C "${VALIDATION_SNAPSHOT_FIXTURE_ROOT}" add tracked.txt
printf 'worktree\n' >"${VALIDATION_SNAPSHOT_FIXTURE_ROOT}/tracked.txt"
printf 'untracked\n' >"${VALIDATION_SNAPSHOT_FIXTURE_ROOT}/untracked.txt"

SNAPSHOT_HEAD_OUTPUT="$("${ROOT_DIR}/scripts/with-validation-snapshot.sh" \
  --repo "${VALIDATION_SNAPSHOT_FIXTURE_ROOT}" \
  --mode head \
  -- ./snapshot-probe.sh)"
if [[ "${SNAPSHOT_HEAD_OUTPUT}" != *'tracked=head'* ]]; then
  echo "Expected head validation snapshot to preserve committed tracked content" >&2
  printf '%s\n' "${SNAPSHOT_HEAD_OUTPUT}" >&2
  exit 1
fi
if [[ "${SNAPSHOT_HEAD_OUTPUT}" == *'untracked='* ]]; then
  echo "Expected head validation snapshot to exclude untracked files" >&2
  printf '%s\n' "${SNAPSHOT_HEAD_OUTPUT}" >&2
  exit 1
fi

SNAPSHOT_INDEX_OUTPUT="$("${ROOT_DIR}/scripts/with-validation-snapshot.sh" \
  --repo "${VALIDATION_SNAPSHOT_FIXTURE_ROOT}" \
  --mode index \
  -- ./snapshot-probe.sh)"
if [[ "${SNAPSHOT_INDEX_OUTPUT}" != *'tracked=index'* ]]; then
  echo "Expected index validation snapshot to preserve staged tracked content" >&2
  printf '%s\n' "${SNAPSHOT_INDEX_OUTPUT}" >&2
  exit 1
fi
if [[ "${SNAPSHOT_INDEX_OUTPUT}" == *'untracked='* ]]; then
  echo "Expected index validation snapshot to exclude untracked files" >&2
  printf '%s\n' "${SNAPSHOT_INDEX_OUTPUT}" >&2
  exit 1
fi

VALIDATION_INDEX_TYPE_CHANGE_FILE_TO_DIR_ROOT="$(mktemp -d)"
git -C "${VALIDATION_INDEX_TYPE_CHANGE_FILE_TO_DIR_ROOT}" init -q
git -C "${VALIDATION_INDEX_TYPE_CHANGE_FILE_TO_DIR_ROOT}" config user.email "verify@example.com"
git -C "${VALIDATION_INDEX_TYPE_CHANGE_FILE_TO_DIR_ROOT}" config user.name "Workcell Verify"
cat <<'EOF' >"${VALIDATION_INDEX_TYPE_CHANGE_FILE_TO_DIR_ROOT}/snapshot-kind-probe.sh"
#!/bin/bash
set -euo pipefail
if [[ -d kind ]]; then
  printf 'kind=dir\n'
  printf 'payload=%s\n' "$(cat kind/payload.txt)"
else
  printf 'kind=file\n'
  printf 'payload=%s\n' "$(cat kind)"
fi
EOF
chmod 0700 "${VALIDATION_INDEX_TYPE_CHANGE_FILE_TO_DIR_ROOT}/snapshot-kind-probe.sh"
printf 'file\n' >"${VALIDATION_INDEX_TYPE_CHANGE_FILE_TO_DIR_ROOT}/kind"
git -C "${VALIDATION_INDEX_TYPE_CHANGE_FILE_TO_DIR_ROOT}" add kind snapshot-kind-probe.sh
git -C "${VALIDATION_INDEX_TYPE_CHANGE_FILE_TO_DIR_ROOT}" commit -qm "initial"
rm -f "${VALIDATION_INDEX_TYPE_CHANGE_FILE_TO_DIR_ROOT}/kind"
mkdir -p "${VALIDATION_INDEX_TYPE_CHANGE_FILE_TO_DIR_ROOT}/kind"
printf 'staged-nested\n' >"${VALIDATION_INDEX_TYPE_CHANGE_FILE_TO_DIR_ROOT}/kind/payload.txt"
git -C "${VALIDATION_INDEX_TYPE_CHANGE_FILE_TO_DIR_ROOT}" add -A kind

SNAPSHOT_INDEX_FILE_TO_DIR_OUTPUT="$("${ROOT_DIR}/scripts/with-validation-snapshot.sh" \
  --repo "${VALIDATION_INDEX_TYPE_CHANGE_FILE_TO_DIR_ROOT}" \
  --mode index \
  -- ./snapshot-kind-probe.sh)"
if [[ "${SNAPSHOT_INDEX_FILE_TO_DIR_OUTPUT}" != *'kind=dir'* ]] || [[ "${SNAPSHOT_INDEX_FILE_TO_DIR_OUTPUT}" != *'payload=staged-nested'* ]]; then
  echo "Expected index validation snapshot to preserve staged tracked file-to-directory type changes" >&2
  printf '%s\n' "${SNAPSHOT_INDEX_FILE_TO_DIR_OUTPUT}" >&2
  exit 1
fi

VALIDATION_INDEX_TYPE_CHANGE_DIR_TO_FILE_ROOT="$(mktemp -d)"
git -C "${VALIDATION_INDEX_TYPE_CHANGE_DIR_TO_FILE_ROOT}" init -q
git -C "${VALIDATION_INDEX_TYPE_CHANGE_DIR_TO_FILE_ROOT}" config user.email "verify@example.com"
git -C "${VALIDATION_INDEX_TYPE_CHANGE_DIR_TO_FILE_ROOT}" config user.name "Workcell Verify"
cat <<'EOF' >"${VALIDATION_INDEX_TYPE_CHANGE_DIR_TO_FILE_ROOT}/snapshot-kind-probe.sh"
#!/bin/bash
set -euo pipefail
if [[ -d kind ]]; then
  printf 'kind=dir\n'
  printf 'payload=%s\n' "$(cat kind/payload.txt)"
else
  printf 'kind=file\n'
  printf 'payload=%s\n' "$(cat kind)"
fi
EOF
chmod 0700 "${VALIDATION_INDEX_TYPE_CHANGE_DIR_TO_FILE_ROOT}/snapshot-kind-probe.sh"
mkdir -p "${VALIDATION_INDEX_TYPE_CHANGE_DIR_TO_FILE_ROOT}/kind"
printf 'nested\n' >"${VALIDATION_INDEX_TYPE_CHANGE_DIR_TO_FILE_ROOT}/kind/payload.txt"
git -C "${VALIDATION_INDEX_TYPE_CHANGE_DIR_TO_FILE_ROOT}" add kind/payload.txt snapshot-kind-probe.sh
git -C "${VALIDATION_INDEX_TYPE_CHANGE_DIR_TO_FILE_ROOT}" commit -qm "initial"
rm -rf "${VALIDATION_INDEX_TYPE_CHANGE_DIR_TO_FILE_ROOT}/kind"
printf 'staged-flattened\n' >"${VALIDATION_INDEX_TYPE_CHANGE_DIR_TO_FILE_ROOT}/kind"
git -C "${VALIDATION_INDEX_TYPE_CHANGE_DIR_TO_FILE_ROOT}" add -A kind

SNAPSHOT_INDEX_DIR_TO_FILE_OUTPUT="$("${ROOT_DIR}/scripts/with-validation-snapshot.sh" \
  --repo "${VALIDATION_INDEX_TYPE_CHANGE_DIR_TO_FILE_ROOT}" \
  --mode index \
  -- ./snapshot-kind-probe.sh)"
if [[ "${SNAPSHOT_INDEX_DIR_TO_FILE_OUTPUT}" != *'kind=file'* ]] || [[ "${SNAPSHOT_INDEX_DIR_TO_FILE_OUTPUT}" != *'payload=staged-flattened'* ]]; then
  echo "Expected index validation snapshot to preserve staged tracked directory-to-file type changes" >&2
  printf '%s\n' "${SNAPSHOT_INDEX_DIR_TO_FILE_OUTPUT}" >&2
  exit 1
fi

SNAPSHOT_WORKTREE_OUTPUT="$("${ROOT_DIR}/scripts/with-validation-snapshot.sh" \
  --repo "${VALIDATION_SNAPSHOT_FIXTURE_ROOT}" \
  --mode worktree \
  --include-untracked \
  -- ./snapshot-probe.sh)"
if [[ "${SNAPSHOT_WORKTREE_OUTPUT}" != *'tracked=worktree'* ]]; then
  echo "Expected worktree validation snapshot to preserve live tracked content" >&2
  printf '%s\n' "${SNAPSHOT_WORKTREE_OUTPUT}" >&2
  exit 1
fi
if [[ "${SNAPSHOT_WORKTREE_OUTPUT}" != *'untracked=untracked'* ]]; then
  echo "Expected worktree validation snapshot to include untracked files when requested" >&2
  printf '%s\n' "${SNAPSHOT_WORKTREE_OUTPUT}" >&2
  exit 1
fi

VALIDATION_TYPE_CHANGE_FILE_TO_DIR_ROOT="$(mktemp -d)"
git -C "${VALIDATION_TYPE_CHANGE_FILE_TO_DIR_ROOT}" init -q
git -C "${VALIDATION_TYPE_CHANGE_FILE_TO_DIR_ROOT}" config user.email "verify@example.com"
git -C "${VALIDATION_TYPE_CHANGE_FILE_TO_DIR_ROOT}" config user.name "Workcell Verify"
cat <<'EOF' >"${VALIDATION_TYPE_CHANGE_FILE_TO_DIR_ROOT}/snapshot-kind-probe.sh"
#!/bin/bash
set -euo pipefail
if [[ -d kind ]]; then
  printf 'kind=dir\n'
  printf 'payload=%s\n' "$(cat kind/payload.txt)"
else
  printf 'kind=file\n'
  printf 'payload=%s\n' "$(cat kind)"
fi
EOF
chmod 0700 "${VALIDATION_TYPE_CHANGE_FILE_TO_DIR_ROOT}/snapshot-kind-probe.sh"
printf 'file\n' >"${VALIDATION_TYPE_CHANGE_FILE_TO_DIR_ROOT}/kind"
git -C "${VALIDATION_TYPE_CHANGE_FILE_TO_DIR_ROOT}" add kind snapshot-kind-probe.sh
git -C "${VALIDATION_TYPE_CHANGE_FILE_TO_DIR_ROOT}" commit -qm "initial"
rm -f "${VALIDATION_TYPE_CHANGE_FILE_TO_DIR_ROOT}/kind"
mkdir -p "${VALIDATION_TYPE_CHANGE_FILE_TO_DIR_ROOT}/kind"
printf 'nested\n' >"${VALIDATION_TYPE_CHANGE_FILE_TO_DIR_ROOT}/kind/payload.txt"

SNAPSHOT_FILE_TO_DIR_OUTPUT="$("${ROOT_DIR}/scripts/with-validation-snapshot.sh" \
  --repo "${VALIDATION_TYPE_CHANGE_FILE_TO_DIR_ROOT}" \
  --mode worktree \
  --include-untracked \
  -- ./snapshot-kind-probe.sh)"
if [[ "${SNAPSHOT_FILE_TO_DIR_OUTPUT}" != *'kind=dir'* ]] || [[ "${SNAPSHOT_FILE_TO_DIR_OUTPUT}" != *'payload=nested'* ]]; then
  echo "Expected worktree validation snapshot to preserve tracked file-to-directory type changes" >&2
  printf '%s\n' "${SNAPSHOT_FILE_TO_DIR_OUTPUT}" >&2
  exit 1
fi

VALIDATION_TYPE_CHANGE_DIR_TO_FILE_ROOT="$(mktemp -d)"
git -C "${VALIDATION_TYPE_CHANGE_DIR_TO_FILE_ROOT}" init -q
git -C "${VALIDATION_TYPE_CHANGE_DIR_TO_FILE_ROOT}" config user.email "verify@example.com"
git -C "${VALIDATION_TYPE_CHANGE_DIR_TO_FILE_ROOT}" config user.name "Workcell Verify"
cat <<'EOF' >"${VALIDATION_TYPE_CHANGE_DIR_TO_FILE_ROOT}/snapshot-kind-probe.sh"
#!/bin/bash
set -euo pipefail
if [[ -d kind ]]; then
  printf 'kind=dir\n'
  printf 'payload=%s\n' "$(cat kind/payload.txt)"
else
  printf 'kind=file\n'
  printf 'payload=%s\n' "$(cat kind)"
fi
EOF
chmod 0700 "${VALIDATION_TYPE_CHANGE_DIR_TO_FILE_ROOT}/snapshot-kind-probe.sh"
mkdir -p "${VALIDATION_TYPE_CHANGE_DIR_TO_FILE_ROOT}/kind"
printf 'nested\n' >"${VALIDATION_TYPE_CHANGE_DIR_TO_FILE_ROOT}/kind/payload.txt"
git -C "${VALIDATION_TYPE_CHANGE_DIR_TO_FILE_ROOT}" add kind/payload.txt snapshot-kind-probe.sh
git -C "${VALIDATION_TYPE_CHANGE_DIR_TO_FILE_ROOT}" commit -qm "initial"
rm -rf "${VALIDATION_TYPE_CHANGE_DIR_TO_FILE_ROOT}/kind"
printf 'flattened\n' >"${VALIDATION_TYPE_CHANGE_DIR_TO_FILE_ROOT}/kind"

SNAPSHOT_DIR_TO_FILE_OUTPUT="$("${ROOT_DIR}/scripts/with-validation-snapshot.sh" \
  --repo "${VALIDATION_TYPE_CHANGE_DIR_TO_FILE_ROOT}" \
  --mode worktree \
  --include-untracked \
  -- ./snapshot-kind-probe.sh)"
if [[ "${SNAPSHOT_DIR_TO_FILE_OUTPUT}" != *'kind=file'* ]] || [[ "${SNAPSHOT_DIR_TO_FILE_OUTPUT}" != *'payload=flattened'* ]]; then
  echo "Expected worktree validation snapshot to preserve tracked directory-to-file type changes" >&2
  printf '%s\n' "${SNAPSHOT_DIR_TO_FILE_OUTPUT}" >&2
  exit 1
fi

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/bad-github-scope.toml"
version = 1

[credentials.github_hosts]
source = "gh-hosts.yml"
EOF

if "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${INJECTION_POLICY_FIXTURE_ROOT}/bad-github-scope.toml" \
  --auth-status >/tmp/workcell-injection-bad-github-scope.out 2>&1; then
  echo "Expected workcell --auth-status to reject unscoped shared GitHub credentials" >&2
  exit 1
fi

if ! grep -q 'providers is required' /tmp/workcell-injection-bad-github-scope.out; then
  echo "Expected shared GitHub credential rejection to require explicit providers scoping" >&2
  cat /tmp/workcell-injection-bad-github-scope.out >&2
  exit 1
fi

cat <<'EOF' >"${INJECTION_POLICY_FIXTURE_ROOT}/bad-ssh.toml"
version = 1

[ssh]
enabled = true
identities = ["config"]
EOF

if "${ROOT_DIR}/scripts/lib/render_injection_bundle" \
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

if ! rg -q -- '--self-staging-probe' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to expose a hidden staging probe for invariant testing" >&2
  exit 1
fi

if ! rg -q 'strict mode requires --prepare when you explicitly request --rebuild.' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to reject explicit strict-mode image rebuild requests" >&2
  exit 1
fi

if ! rg -q 'go_colimautil validate-profile-config' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to validate managed Colima config through the dedicated Go helper" >&2
  exit 1
fi

if ! rg -q 'go_colimautil validate-runtime-mounts' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to validate managed Lima mounts through the dedicated Go helper" >&2
  exit 1
fi

WORKCELL_COLIMA_TIMEOUT_HARNESS="${BARRIER_VERIFY_ROOT}/workcell-colima-timeout-harness.sh"
{
  printf 'set -euo pipefail\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" kill_process_tree_by_pid
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" terminate_process_tree_by_pid
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" run_host_colima_with_timeout
  printf '\n'
  cat <<'EOF'
run_host_colima() {
  sleep 60
}

start_epoch="$(date +%s)"
if run_host_colima_with_timeout 1 delete --profile timeout-fixture; then
  echo "Expected run_host_colima_with_timeout to time out for a hung colima command" >&2
  exit 1
else
  status=$?
fi
elapsed=$(( $(date +%s) - start_epoch ))
[[ "${status}" -eq 124 ]]
[[ "${elapsed}" -lt 15 ]]
EOF
} >"${WORKCELL_COLIMA_TIMEOUT_HARNESS}"
bash "${WORKCELL_COLIMA_TIMEOUT_HARNESS}"

WORKCELL_REFRESH_HARNESS="${BARRIER_VERIFY_ROOT}/workcell-refresh-harness.sh"
{
  printf 'set -euo pipefail\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" remove_profile_state_dirs
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" profile_state_dirs_exist
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" refresh_managed_profile
  printf '\n'
  cat <<'EOF'
ROOT="$(mktemp -d)"
COLIMA_PROFILE="refresh-fixture"
PROFILE_WAS_REFRESHED=0
PROFILE_PREEXISTED=1
PROFILE_MARKER_WORKSPACE="bound"
PROFILE_RUNNING=1

stash_profile_audit_log() { :; }
remember_profile_runtime_image_for_refresh() { :; }
reap_stale_profile_processes() { :; }
run_host_colima_with_timeout() { return 124; }
profile_dir() { printf '%s/profile-%s\n' "${ROOT}" "$1"; }
profile_lima_dir() { printf '%s/lima-%s\n' "${ROOT}" "$1"; }
profile_disk_dir() { printf '%s/disk-%s\n' "${ROOT}" "$1"; }
profile_process_pids() { return 1; }

PROFILE_DIR="$(profile_dir "${COLIMA_PROFILE}")"
mkdir -p "${PROFILE_DIR}" "$(profile_lima_dir "${COLIMA_PROFILE}")" "$(profile_disk_dir "${COLIMA_PROFILE}")"
refresh_managed_profile "refreshing fixture profile"
[[ ! -e "${PROFILE_DIR}" ]]
[[ ! -e "$(profile_lima_dir "${COLIMA_PROFILE}")" ]]
[[ ! -e "$(profile_disk_dir "${COLIMA_PROFILE}")" ]]
[[ "${PROFILE_WAS_REFRESHED}" -eq 1 ]]
[[ "${PROFILE_PREEXISTED}" -eq 0 ]]
[[ -z "${PROFILE_MARKER_WORKSPACE}" ]]
[[ "${PROFILE_RUNNING}" -eq 0 ]]
EOF
} >"${WORKCELL_REFRESH_HARNESS}"
bash "${WORKCELL_REFRESH_HARNESS}"

WORKCELL_START_RETRY_HARNESS="${BARRIER_VERIFY_ROOT}/workcell-start-retry-harness.sh"
{
  printf 'set -euo pipefail\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" start_managed_profile
  printf '\n'
  cat <<'EOF'
COLIMA_PROFILE="start-retry-fixture"
WORKSPACE="/tmp/workspace"
COLIMA_CPU=4
COLIMA_MEMORY=8
COLIMA_DISK=60
PROFILE_RUNNING=0
RUN_COUNT=0
REFRESH_COUNT=0

maybe_reap_stale_profile_processes() { :; }
reap_stale_profile_processes() { :; }
run_command_with_debug_log() {
  RUN_COUNT=$((RUN_COUNT + 1))
  if [[ "${RUN_COUNT}" -eq 1 ]]; then
    return 124
  fi
  return 0
}
refresh_managed_profile() {
  REFRESH_COUNT=$((REFRESH_COUNT + 1))
  return 0
}

start_managed_profile
[[ "${RUN_COUNT}" -eq 2 ]]
[[ "${REFRESH_COUNT}" -eq 1 ]]
[[ "${PROFILE_RUNNING}" -eq 1 ]]
EOF
} >"${WORKCELL_START_RETRY_HARNESS}"
bash "${WORKCELL_START_RETRY_HARNESS}"

WORKCELL_START_TIMEOUT_CLEANUP_HARNESS="${BARRIER_VERIFY_ROOT}/workcell-start-timeout-cleanup-harness.sh"
{
  printf 'set -euo pipefail\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" start_managed_profile
  printf '\n'
  cat <<'EOF'
COLIMA_PROFILE="start-timeout-cleanup-fixture"
WORKSPACE="/tmp/workspace"
COLIMA_CPU=4
COLIMA_MEMORY=8
COLIMA_DISK=60
PROFILE_RUNNING=0
RUN_COUNT=0
REFRESH_COUNT=0
FINAL_STATUS=0

maybe_reap_stale_profile_processes() { :; }
reap_stale_profile_processes() { :; }
run_command_with_debug_log() {
  RUN_COUNT=$((RUN_COUNT + 1))
  return 124
}
refresh_managed_profile() {
  REFRESH_COUNT=$((REFRESH_COUNT + 1))
  return 0
}

if start_managed_profile; then
  echo "Expected repeated Colima start timeouts to fail" >&2
  exit 1
else
  FINAL_STATUS=$?
fi
[[ "${RUN_COUNT}" -eq 2 ]]
[[ "${REFRESH_COUNT}" -eq 2 ]]
[[ "${FINAL_STATUS}" -eq 124 ]]
[[ "${PROFILE_RUNNING}" -eq 0 ]]
EOF
} >"${WORKCELL_START_TIMEOUT_CLEANUP_HARNESS}"
bash "${WORKCELL_START_TIMEOUT_CLEANUP_HARNESS}"

if ! rg -q 'run_clean_host_command_in_dir "\$\{ROOT_DIR\}" env' "${ROOT_DIR}/scripts/workcell" ||
  ! rg -q 'GOPATH="\$\{GOPATH\}"' "${ROOT_DIR}/scripts/workcell" ||
  ! rg -q 'GOMODCACHE="\$\{GOMODCACHE\}"' "${ROOT_DIR}/scripts/workcell" ||
  ! rg -q 'GOCACHE="\$\{GOCACHE\}"' "${ROOT_DIR}/scripts/workcell" ||
  ! rg -q '"\$\{HOST_GO_BIN\}" run ./cmd/workcell-hostutil "\$@"' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell to invoke the bootstrap Go helper from the repo root under a scrubbed environment with explicit Go caches" >&2
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

if ! rg -q "trap 'workcell_run_command_with_file_trace_signal INT' INT" "${ROOT_DIR}/runtime/container/entrypoint.sh" ||
  ! rg -q "trap 'workcell_run_command_with_file_trace_signal TERM' TERM" "${ROOT_DIR}/runtime/container/entrypoint.sh"; then
  echo "Expected runtime/container/entrypoint.sh to trap INT/TERM and finalize file-trace shutdown before exit" >&2
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

if ! rg -q 'run_clean_repo_command env' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh" ||
  ! rg -q 'GOPATH="\$\{GOPATH\}"' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh" ||
  ! rg -q 'GOMODCACHE="\$\{GOMODCACHE\}"' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh" ||
  ! rg -q 'GOCACHE="\$\{GOCACHE\}"' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh" ||
  ! rg -q '"\$\{GO_BIN\}" run ./cmd/workcell-runtimeutil "\$@"' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "Expected scripts/colima-egress-allowlist.sh to invoke Go runtime helpers under a scrubbed environment with explicit Go caches" >&2
  exit 1
fi

for script in "${HOST_GATE_SCRIPTS[@]}"; do
  if ! head -n 1 "${script}" | grep -q '^#!/bin/bash -p$'; then
    echo "Expected ${script} to use an absolute privileged Bash shebang before self-sanitizing its host entrypoint" >&2
    exit 1
  fi
  if ! rg -q 'WORKCELL_SANITIZED_ENTRYPOINT|trusted-entrypoint\.sh' "${script}"; then
    echo "Expected ${script} to self-sanitize its host entrypoint before running release or boundary checks" >&2
    exit 1
  fi
done

if [[ ! -x "${REPO_PRECOMMIT_HOOK}" ]]; then
  echo "Expected executable repo pre-commit hook: ${REPO_PRECOMMIT_HOOK}" >&2
  exit 1
fi
if ! rg -q 'scripts/update-upstream-pins\.sh" --check' "${REPO_PRECOMMIT_HOOK}"; then
  echo "Expected repo pre-commit hook to gate commits on pending pinned upstream updates" >&2
  exit 1
fi

PRECOMMIT_FIXTURE_ROOT="$(mktemp -d)"
mkdir -p "${PRECOMMIT_FIXTURE_ROOT}/.githooks" "${PRECOMMIT_FIXTURE_ROOT}/scripts"
install -m 0755 "${REPO_PRECOMMIT_HOOK}" "${PRECOMMIT_FIXTURE_ROOT}/.githooks/pre-commit"
cat >"${PRECOMMIT_FIXTURE_ROOT}/scripts/update-upstream-pins.sh" <<'EOF'
#!/bin/bash
if [[ "${1:-}" == "--check" ]]; then
  echo "Pinned upstream refresh summary:"
  echo "  buildx-version: v0.32.1 -> v0.33.0"
  exit 1
fi
exit 2
EOF
chmod 0755 "${PRECOMMIT_FIXTURE_ROOT}/scripts/update-upstream-pins.sh"
if HOME="${PRECOMMIT_FIXTURE_ROOT}" "${PRECOMMIT_FIXTURE_ROOT}/.githooks/pre-commit" >/tmp/workcell-precommit.out 2>&1; then
  echo "Expected repo pre-commit hook to block commits when pinned upstream updates are pending" >&2
  exit 1
fi
grep -q 'Pinned upstream updates are available' /tmp/workcell-precommit.out
grep -q 'update-upstream-pins.sh --apply' /tmp/workcell-precommit.out

cat >"${PRECOMMIT_FIXTURE_ROOT}/scripts/update-upstream-pins.sh" <<'EOF'
#!/bin/bash
if [[ "${1:-}" == "--check" ]]; then
  echo "Pinned upstream refresh summary:"
  exit 0
fi
exit 2
EOF
chmod 0755 "${PRECOMMIT_FIXTURE_ROOT}/scripts/update-upstream-pins.sh"
HOME="${PRECOMMIT_FIXTURE_ROOT}" "${PRECOMMIT_FIXTURE_ROOT}/.githooks/pre-commit" >/tmp/workcell-precommit-ok.out 2>&1
rm -rf "${PRECOMMIT_FIXTURE_ROOT}"

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

if ! rg -q 'BUILDX_BUILDER="workcell-release-' "${ROOT_DIR}/scripts/verify-release-bundle.sh"; then
  echo "Expected verify-release-bundle.sh to choose a deterministic context-scoped Buildx builder by default" >&2
  exit 1
fi

if ! rg -q 'buildx_expected_endpoints\(\)' "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"; then
  echo "Expected trusted-docker-client.sh to compute accepted Buildx endpoints from the active Docker context or host" >&2
  exit 1
fi

if ! rg -q 'docker context inspect "\$\{DOCKER_CONTEXT_NAME\}" --format' "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"; then
  echo "Expected trusted-docker-client.sh to resolve Docker context host URIs when validating existing Buildx builders" >&2
  exit 1
fi

if ! rg -q 'COLIMA_HOME="\$\{colima_home\}"' "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
  echo "Expected scripts/colima-egress-allowlist.sh to pin COLIMA_HOME while operating on Lima state" >&2
  exit 1
fi

if ! rg -q 'snapshot\.debian\.org:443' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell bootstrap endpoints to allow snapshot.debian.org" >&2
  exit 1
fi

if rg -q 'static\.rust-lang\.org:443' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell bootstrap endpoints to avoid unused static.rust-lang.org egress" >&2
  exit 1
fi

if ! rg -q 'docker-images-prod\.[^.]+\.r2\.cloudflarestorage\.com:443' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell bootstrap endpoints to allow Docker blob storage on Cloudflare R2" >&2
  exit 1
fi

if rg -q 'snapshot\.debian\.org:80' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected scripts/workcell bootstrap endpoints to avoid unused snapshot.debian.org:80 egress" >&2
  exit 1
fi

for dockerfile in \
  "${ROOT_DIR}/runtime/container/Dockerfile" \
  "${ROOT_DIR}/tools/validator/Dockerfile"; do
  if ! rg -q 'ca-certificates_20250419_all\.deb' "${dockerfile}"; then
    echo "Expected ${dockerfile} to pin a snapshot CA bundle bootstrap package before HTTPS apt" >&2
    exit 1
  fi
  if ! rg -q 'openssl_3\.5\.5-1~deb13u1_amd64\.deb' "${dockerfile}"; then
    echo "Expected ${dockerfile} to pin the amd64 snapshot OpenSSL bootstrap package before HTTPS apt" >&2
    exit 1
  fi
  if ! rg -q 'openssl_3\.5\.5-1~deb13u1_arm64\.deb' "${dockerfile}"; then
    echo "Expected ${dockerfile} to pin the arm64 snapshot OpenSSL bootstrap package before HTTPS apt" >&2
    exit 1
  fi
  if ! rg -q 'Acquire::Retries "5";' "${dockerfile}"; then
    echo "Expected ${dockerfile} to pin apt retry count for snapshot fetch resilience" >&2
    exit 1
  fi
  if ! rg -q 'Acquire::http::Timeout "30";' "${dockerfile}"; then
    echo "Expected ${dockerfile} to pin apt HTTP timeout for snapshot fetch resilience" >&2
    exit 1
  fi
  if ! rg -q 'Acquire::https::Timeout "30";' "${dockerfile}"; then
    echo "Expected ${dockerfile} to pin apt HTTPS timeout for snapshot fetch resilience" >&2
    exit 1
  fi
done

for dockerfile in \
  "${ROOT_DIR}/runtime/container/Dockerfile" \
  "${ROOT_DIR}/tools/validator/Dockerfile"; do
  if ! rg -q '^USER workcell$' "${dockerfile}"; then
    echo "Expected ${dockerfile} to default to the named unprivileged workcell user" >&2
    exit 1
  fi
done

validator_dockerfile="${ROOT_DIR}/tools/validator/Dockerfile"
for required in \
  'ENV HOME=/home/workcell' \
  'ENV XDG_CACHE_HOME=/home/workcell/.cache' \
  'ENV GOCACHE=/home/workcell/.cache/go-build' \
  'ENV GOMODCACHE=/home/workcell/.cache/go-mod' \
  'ENV CARGO_TARGET_DIR=/home/workcell/.cache/cargo-target' \
  'ENV TMPDIR=/home/workcell/.tmp'; do
  if ! grep -Fq "${required}" "${validator_dockerfile}"; then
    echo "Expected ${validator_dockerfile} to pin its default nonroot writable state under /home/workcell (${required})" >&2
    exit 1
  fi
done

if ! grep -Fq "CARGO_TARGET_DIR=\"\${CARGO_TARGET_DIR:-\${XDG_CACHE_HOME}/cargo-target}\"" "${ROOT_DIR}/scripts/validate-repo.sh"; then
  echo "Expected scripts/validate-repo.sh to externalize Cargo target writes away from the mounted workspace" >&2
  exit 1
fi

for caller in \
  "${ROOT_DIR}/.github/workflows/ci.yml" \
  "${ROOT_DIR}/.github/workflows/docs.yml" \
  "${ROOT_DIR}/.github/workflows/release.yml" \
  "${ROOT_DIR}/scripts/pre-merge.sh"; do
  for required in \
    "validator_uid=\"\$(id -u)\"" \
    "validator_gid=\"\$(id -g)\"" \
    "--user \"\${validator_uid}:\${validator_gid}\"" \
    "-e HOME=\"\${validator_home}\"" \
    "-e XDG_CACHE_HOME=\"\${validator_cache}\"" \
    "-e GOCACHE=\"\${validator_cache}/go-build\"" \
    "-e GOMODCACHE=\"\${validator_cache}/go-mod\"" \
    "-e CARGO_TARGET_DIR=\"\${validator_cache}/cargo-target\"" \
    "-e TMPDIR=\"\${validator_tmp}\"" \
    "mkdir -p \"\${HOME}\" \"\${XDG_CACHE_HOME}\" \"\${GOCACHE}\" \"\${GOMODCACHE}\" \"\${CARGO_TARGET_DIR}\" \"\${TMPDIR}\""; do
    if ! grep -Fq -- "${required}" "${caller}"; then
      echo "Expected ${caller} to launch validator work under an explicit caller UID/GID with isolated writable state (${required})" >&2
      exit 1
    fi
  done
done

for required in \
  "WORKCELL_BUILD_AND_TEST_VALIDATOR_UID=" \
  "WORKCELL_BUILD_AND_TEST_VALIDATOR_GID=" \
  "--user \"\${WORKCELL_BUILD_AND_TEST_VALIDATOR_UID}:\${WORKCELL_BUILD_AND_TEST_VALIDATOR_GID}\"" \
  "-e HOME=\"\${WORKCELL_BUILD_AND_TEST_VALIDATOR_HOME}\"" \
  "-e XDG_CACHE_HOME=\"\${WORKCELL_BUILD_AND_TEST_VALIDATOR_CACHE}\"" \
  "-e GOCACHE=\"\${WORKCELL_BUILD_AND_TEST_VALIDATOR_CACHE}/go-build\"" \
  "-e GOMODCACHE=\"\${WORKCELL_BUILD_AND_TEST_VALIDATOR_CACHE}/go-mod\"" \
  "-e CARGO_TARGET_DIR=\"\${WORKCELL_BUILD_AND_TEST_VALIDATOR_CACHE}/cargo-target\"" \
  "-e TMPDIR=\"\${WORKCELL_BUILD_AND_TEST_VALIDATOR_TMP}\"" \
  "mkdir -p \"\${HOME}\" \"\${XDG_CACHE_HOME}\" \"\${GOCACHE}\" \"\${GOMODCACHE}\" \"\${CARGO_TARGET_DIR}\" \"\${TMPDIR}\""; do
  if ! grep -Fq -- "${required}" "${ROOT_DIR}/scripts/build-and-test.sh"; then
    echo "Expected scripts/build-and-test.sh --docker to launch validator work under an explicit caller UID/GID with isolated writable state (${required})" >&2
    exit 1
  fi
done

if ! grep -Fq "fallback_home=\"\${fallback_parent%/}/workcell-home-\${uid}\"" "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"; then
  echo "Expected trusted-docker-client.sh to synthesize an isolated home for passwd-less caller UIDs" >&2
  exit 1
fi

for required in \
  "--user \"\${validator_uid}:\${validator_gid}\"" \
  "-e HOME=\"\${validator_home}\"" \
  "-e XDG_CACHE_HOME=\"\${validator_cache_root}\"" \
  "-e GOCACHE=\"\${validator_cache_root}/go-build\"" \
  "-e GOMODCACHE=\"\${validator_cache_root}/go-mod\"" \
  "-e CARGO_TARGET_DIR=\"\${validator_cache_root}/cargo-target\"" \
  "-e TMPDIR=\"\${validator_tmpdir}\"" \
  "mkdir -p \"\${HOME}\" \"\${XDG_CACHE_HOME}\" \"\${GOCACHE}\" \"\${GOMODCACHE}\" \"\${CARGO_TARGET_DIR}\" \"\${TMPDIR}\""; do
  if ! grep -Fq -- "${required}" "${ROOT_DIR}/scripts/verify-release-bundle.sh"; then
    echo "Expected scripts/verify-release-bundle.sh to build bundles in the validator under an explicit caller UID/GID with isolated writable state (${required})" >&2
    exit 1
  fi
done

if grep -Fq "\${ROOT_DIR}/tmp/workcell-build-input-nested" "${ROOT_DIR}/scripts/verify-build-input-manifest.sh"; then
  echo "Expected verify-build-input-manifest.sh nested-source checks to avoid writing under the mounted repo" >&2
  exit 1
fi

if grep -Fq "\${ROOT_DIR}/tmp/workcell-control-plane-nested" "${ROOT_DIR}/scripts/verify-control-plane-manifest.sh"; then
  echo "Expected verify-control-plane-manifest.sh nested-source checks to avoid writing under the mounted repo" >&2
  exit 1
fi

if grep -Fq "\${ROOT_DIR}/tmp/workcell-release-bundle" "${ROOT_DIR}/scripts/verify-release-bundle.sh"; then
  echo "Expected verify-release-bundle.sh temp roots to avoid writing under the mounted repo" >&2
  exit 1
fi

if grep -Fq "\${ROOT_DIR}/tmp/workcell-repro" "${ROOT_DIR}/scripts/verify-reproducible-build.sh"; then
  echo "Expected verify-reproducible-build.sh OCI exports to avoid writing under the mounted repo" >&2
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

if ! sed -n '/^resolve_existing_executable_or_die()/,/^}/p' "${ROOT_DIR}/scripts/workcell" | grep -q 'is_trusted_host_tool_path'; then
  echo "Expected resolve_existing_executable_or_die to reject untrusted host executable paths" >&2
  exit 1
fi

for _git_env_var in GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_INDEX_FILE; do
  # shellcheck disable=SC2016
  printf -v _git_env_literal '"${%s:-}"' "${_git_env_var}"
  if ! grep -Fq -- "${_git_env_literal}" "${ROOT_DIR}/runtime/container/bin/git"; then
    echo "Expected runtime/container/bin/git to block ${_git_env_var} to prevent object-store redirection" >&2
    exit 1
  fi
done

if ! sed -n '/^git_index_materialize_regular_file()/,/^}/p' "${ROOT_DIR}/scripts/workcell" | grep -q 'cat-file blob'; then
  echo "Expected git_index_materialize_regular_file to materialize tracked blobs without checkout-index" >&2
  exit 1
fi

if ! sed -n '/^git_index_materialize_regular_file()/,/^}/p' "${ROOT_DIR}/scripts/workcell" | grep -q 'failed to read tracked blob'; then
  echo "Expected git_index_materialize_regular_file to fail closed when a tracked control-plane blob is unreadable" >&2
  exit 1
fi

# shellcheck disable=SC2016
if ! sed -n '/^git_index_materialize_regular_file()/,/^}/p' "${ROOT_DIR}/scripts/workcell" | grep -Fq 'rm -f "${destination_path}"'; then
  echo "Expected git_index_materialize_regular_file to remove partially materialized files after blob read failures" >&2
  exit 1
fi

if ! sed -n '/^git_index_populate_shadow_dir()/,/^}/p' "${ROOT_DIR}/scripts/workcell" | grep -Fq '*/../*'; then
  echo "Expected git_index_populate_shadow_dir to reject unsafe index paths before shadow materialization" >&2
  exit 1
fi

if ! sed -n '/^sanitize_shadowed_git_config()/,/^}/p' "${ROOT_DIR}/scripts/workcell" | grep -q 'git_config_key_is_blocked'; then
  echo "Expected sanitize_shadowed_git_config to reuse the shared blocked git-config key matcher" >&2
  exit 1
fi

# shellcheck disable=SC2016
if ! grep -Fq -- '-path "${ROOT_DIR}/.venv" -prune -o' "${ROOT_DIR}/scripts/validate-repo.sh"; then
  echo "Expected validate-repo.sh to prune repo-local virtualenv content from documentation scans" >&2
  exit 1
fi

if ! grep -Fq "build -buildvcs=false -o \"\${output_path}\"" "${ROOT_DIR}/scripts/lib/go-run-env.sh"; then
  echo "Expected build_go_tool_in_repo to disable Go VCS stamping in untrusted repos" >&2
  exit 1
fi

go_cache_root_expected=""
case "$(uname -s)" in
  Darwin)
    go_cache_root_expected="${REAL_HOME}/Library/Caches/workcell/go"
    ;;
  *)
    go_cache_root_expected="${REAL_HOME}/.cache/workcell/go"
    ;;
esac
go_cache_root_actual="$(
  env -i \
    PATH="${PATH}" \
    HOME="${REAL_HOME}" \
    LC_ALL=C \
    LANG=C \
    bash --noprofile --norc -c "
      source \"${ROOT_DIR}/scripts/lib/go-run-env.sh\"
      ensure_go_run_env
      printf '%s\n' \"\${WORKCELL_GO_CACHE_ROOT}\"
    "
)"
if [[ "${go_cache_root_actual}" != "${go_cache_root_expected}" ]]; then
  echo "Expected ensure_go_run_env to default to a stable per-user cache root, got: ${go_cache_root_actual}" >&2
  exit 1
fi

if ! sed -n '/^validate_publish_base_name()/,/^}/p' "${ROOT_DIR}/scripts/workcell" | grep -q 'check-ref-format'; then
  echo "Expected validate_publish_base_name to validate the publish-pr --base branch name" >&2
  exit 1
fi

if ! sed -n '/^publish_pr_main()/,/^}/p' "${ROOT_DIR}/scripts/workcell" | grep -q 'core.hooksPath=/dev/null'; then
  echo "Expected publish_pr_main to disable repo hooks for host-side publish git commands" >&2
  exit 1
fi

if ! sed -n '/^publish_pr_main()/,/^}/p' "${ROOT_DIR}/scripts/workcell" | grep -q -- '--no-verify'; then
  echo "Expected publish_pr_main to bypass repo hooks explicitly on host-side commit and push" >&2
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
# shellcheck disable=SC1003,SC2016
for needle in \
  'find "${workspace}/${git_rel}/modules" \' \
  '-type l \) -name hooks' \
  '-type l \) \( -name config -o -name config.worktree \)' \
  '-type l \) -name worktrees'; do
  if ! grep -Fq -- "${needle}" "${ROOT_DIR}/scripts/workcell"; then
    echo "Expected prepare_workspace_control_plane_shadow to match snippet: ${needle}" >&2
    exit 1
  fi
done

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

RELEASE_CHECKSUMS_VERIFY_ROOT="${BARRIER_VERIFY_ROOT}/release-checksums"
mkdir -p "${RELEASE_CHECKSUMS_VERIFY_ROOT}"
printf 'alpha\n' >"${RELEASE_CHECKSUMS_VERIFY_ROOT}/asset-a.txt"
printf 'bravo\n' >"${RELEASE_CHECKSUMS_VERIFY_ROOT}/asset-b.txt"
"${ROOT_DIR}/scripts/generate-release-checksums.sh" \
  "${RELEASE_CHECKSUMS_VERIFY_ROOT}/SHA256SUMS" \
  "${RELEASE_CHECKSUMS_VERIFY_ROOT}/asset-a.txt" \
  "${RELEASE_CHECKSUMS_VERIFY_ROOT}/asset-b.txt"
if [[ ! -f "${RELEASE_CHECKSUMS_VERIFY_ROOT}/SHA256SUMS" ]]; then
  echo "Expected generate-release-checksums.sh to emit a checksum manifest for valid release assets" >&2
  exit 1
fi
if ! grep -q '  asset-a.txt$' "${RELEASE_CHECKSUMS_VERIFY_ROOT}/SHA256SUMS" ||
  ! grep -q '  asset-b.txt$' "${RELEASE_CHECKSUMS_VERIFY_ROOT}/SHA256SUMS"; then
  echo "Expected generate-release-checksums.sh to list every supplied release asset by basename" >&2
  cat "${RELEASE_CHECKSUMS_VERIFY_ROOT}/SHA256SUMS" >&2
  exit 1
fi
mkdir -p "${RELEASE_CHECKSUMS_VERIFY_ROOT}/dup-a" "${RELEASE_CHECKSUMS_VERIFY_ROOT}/dup-b"
printf 'charlie\n' >"${RELEASE_CHECKSUMS_VERIFY_ROOT}/dup-a/asset.txt"
printf 'delta\n' >"${RELEASE_CHECKSUMS_VERIFY_ROOT}/dup-b/asset.txt"
if "${ROOT_DIR}/scripts/generate-release-checksums.sh" \
  "${RELEASE_CHECKSUMS_VERIFY_ROOT}/SHA256SUMS-duplicate" \
  "${RELEASE_CHECKSUMS_VERIFY_ROOT}/dup-a/asset.txt" \
  "${RELEASE_CHECKSUMS_VERIFY_ROOT}/dup-b/asset.txt" \
  >/tmp/workcell-release-checksums-duplicate.out 2>&1; then
  echo "Expected generate-release-checksums.sh to reject duplicate release asset basenames" >&2
  exit 1
fi
if ! grep -q 'Duplicate release asset basename: asset.txt' /tmp/workcell-release-checksums-duplicate.out; then
  echo "Expected generate-release-checksums.sh duplicate-basename rejection to explain the conflicting asset" >&2
  cat /tmp/workcell-release-checksums-duplicate.out >&2
  exit 1
fi

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

while read -r env_name script; do
  output_file="/tmp/$(basename "${script}").bad-docker-context.out"
  if env "${env_name}=workcell-missing-context" "${script}" --self-docker-probe >/dev/null 2>"${output_file}"; then
    echo "Expected ${script} Docker probe to fail for an explicit unhealthy Docker context" >&2
    exit 1
  fi
  grep -q "Requested Docker context 'workcell-missing-context' is not healthy" "${output_file}"
done <<EOF
WORKCELL_CONTAINER_SMOKE_DOCKER_CONTEXT ${ROOT_DIR}/scripts/container-smoke.sh
WORKCELL_BUILDER_ENV_DOCKER_CONTEXT ${ROOT_DIR}/scripts/generate-builder-environment-manifest.sh
WORKCELL_RELEASE_BUNDLE_DOCKER_CONTEXT ${ROOT_DIR}/scripts/verify-release-bundle.sh
WORKCELL_REPRO_DOCKER_CONTEXT ${ROOT_DIR}/scripts/verify-reproducible-build.sh
EOF

BUILDER_RECREATE_PROBE_NAME="workcell-verify-builder-recreate-$$"
BUILDER_RECREATE_OUTPUT="/tmp/workcell-builder-recreate.out"
if ! (
  set -euo pipefail
  local_probe_docker_host=""
  source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
  setup_workcell_trusted_docker_client
  select_workcell_docker_context "Requested Docker context" "No healthy Docker context found" colima default
  PROBE_DOCKER_CONTEXT_NAME="${DOCKER_CONTEXT_NAME}"
  buildx_cmd rm --force "${BUILDER_RECREATE_PROBE_NAME}" >/dev/null 2>&1 || true
  buildx_cmd create --driver docker-container --name "${BUILDER_RECREATE_PROBE_NAME}" --use >/dev/null
  local_probe_docker_host="$(docker context inspect "${PROBE_DOCKER_CONTEXT_NAME}" --format '{{.Endpoints.docker.Host}}')"
  DOCKER_CONTEXT_NAME="" DOCKER_HOST="${local_probe_docker_host}" BUILDX_BUILDER="${BUILDER_RECREATE_PROBE_NAME}" \
    ensure_workcell_selected_builder
  DOCKER_CONTEXT_NAME="" DOCKER_HOST="${local_probe_docker_host}" \
    buildx_cmd inspect "${BUILDER_RECREATE_PROBE_NAME}" >"${BUILDER_RECREATE_OUTPUT}"
  while IFS= read -r line; do
    case "${line}" in
      "Endpoint: ${local_probe_docker_host}")
        matched=1
        break
        ;;
    esac
  done <"${BUILDER_RECREATE_OUTPUT}"
  [[ "${matched:-0}" -eq 1 ]]
  buildx_cmd rm --force "${BUILDER_RECREATE_PROBE_NAME}" >/dev/null 2>&1 || true
  cleanup_workcell_trusted_docker_client
); then
  echo "Expected trusted-docker-client.sh to recreate a stale Buildx builder when the active DOCKER_HOST endpoint differs" >&2
  cat "${BUILDER_RECREATE_OUTPUT}" >&2 || true
  exit 1
fi

BUILDER_ENDPOINT_MATCH_PROBE="/tmp/workcell-builder-match.out"
if ! (
  set -euo pipefail
  source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
  cat >"${BUILDER_ENDPOINT_MATCH_PROBE}" <<'EOF'
Name: test
Nodes:
Name: test0
Endpoint: colima
EOF
  buildx_builder_matches_context "${BUILDER_ENDPOINT_MATCH_PROBE}" colima unix:///tmp/workcell-docker.sock
  cat >"${BUILDER_ENDPOINT_MATCH_PROBE}" <<'EOF'
Name: test
Nodes:
Name: test0
Endpoint: unix:///tmp/workcell-docker.sock
EOF
  buildx_builder_matches_context "${BUILDER_ENDPOINT_MATCH_PROBE}" colima unix:///tmp/workcell-docker.sock
); then
  echo "Expected trusted-docker-client.sh to accept either a Docker context name or its resolved host URI when matching Buildx builders" >&2
  cat "${BUILDER_ENDPOINT_MATCH_PROBE}" >&2 || true
  exit 1
fi

DOCKER_CONTEXT_SELECTOR_FAKEBIN="${BARRIER_VERIFY_ROOT}/docker-context-selector-bin"
DOCKER_CONTEXT_SELECTOR_HARNESS="${BARRIER_VERIFY_ROOT}/docker-context-selector-harness.sh"
mkdir -p "${DOCKER_CONTEXT_SELECTOR_FAKEBIN}"
cat >"${DOCKER_CONTEXT_SELECTOR_FAKEBIN}/docker" <<'EOF'
#!/bin/sh
mode="${DOCKER_CONTEXT_SELECTOR_MODE:-default}"
case "$1 $2 $3" in
  "context inspect colima")
    exit 0
    ;;
  "context inspect default")
    exit 0
    ;;
  "context inspect sandbox")
    exit 0
    ;;
  "context ls --format")
    printf '%s\n' colima default sandbox
    exit 0
    ;;
  "--context colima info")
    exit 1
    ;;
  "--context default info")
    if [ "${mode}" = "fallback" ]; then
      exit 1
    fi
    exit 0
    ;;
  "--context sandbox info")
    exit 0
    ;;
esac
exit 1
EOF
chmod 0755 "${DOCKER_CONTEXT_SELECTOR_FAKEBIN}/docker"
cat >"${DOCKER_CONTEXT_SELECTOR_HARNESS}" <<'EOF'
set -euo pipefail

explicit_context_output="${BARRIER_VERIFY_ROOT}/docker-context-selector-explicit.out"
if PATH="${DOCKER_CONTEXT_SELECTOR_FAKEBIN}:${PATH}" \
  HOME=/tmp \
  ROOT_DIR="${ROOT_DIR}" \
  BARRIER_VERIFY_ROOT="${BARRIER_VERIFY_ROOT}" \
  /bin/bash -lc '
    set -euo pipefail
    source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
    export DOCKER_CONTEXT_NAME=colima
    select_workcell_docker_context \
      "Requested Docker context" \
      "No healthy Docker contexts" \
      colima default
  ' >/dev/null 2>"${explicit_context_output}"; then
  echo "Expected explicit unhealthy Docker context to fail selection" >&2
  exit 1
fi
grep -q "Requested Docker context 'colima' is not healthy" "${explicit_context_output}"

selected_context="$(
  PATH="${DOCKER_CONTEXT_SELECTOR_FAKEBIN}:${PATH}" \
  HOME=/tmp \
  ROOT_DIR="${ROOT_DIR}" \
  BARRIER_VERIFY_ROOT="${BARRIER_VERIFY_ROOT}" \
  /bin/bash -lc '
    set -euo pipefail
    source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
    unset DOCKER_CONTEXT_NAME
    select_workcell_docker_context \
      "Requested Docker context" \
      "No healthy Docker contexts" \
      colima default >/dev/null
    printf "%s\n" "${DOCKER_CONTEXT_NAME:-}"
  '
)"
if [[ "${selected_context}" != "default" ]]; then
  echo "Expected auto-selection to continue past unhealthy colima" >&2
  exit 1
fi

fallback_context="$(
  DOCKER_CONTEXT_SELECTOR_MODE=fallback \
    PATH="${DOCKER_CONTEXT_SELECTOR_FAKEBIN}:${PATH}" \
    HOME=/tmp \
    ROOT_DIR="${ROOT_DIR}" \
    BARRIER_VERIFY_ROOT="${BARRIER_VERIFY_ROOT}" \
    /bin/bash -lc '
      set -euo pipefail
      source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
      unset DOCKER_CONTEXT_NAME
      select_workcell_docker_context \
        "Requested Docker context" \
        "No healthy Docker contexts" \
        colima default >/dev/null
      printf "%s\n" "${DOCKER_CONTEXT_NAME:-}"
    '
)"
if [[ "${fallback_context}" != "sandbox" ]]; then
  echo "Expected auto-selection to fall back to a healthy listed Docker context outside the preferred set" >&2
  exit 1
fi
EOF
chmod 0755 "${DOCKER_CONTEXT_SELECTOR_HARNESS}"
DOCKER_CONTEXT_SELECTOR_PATH="${DOCKER_CONTEXT_SELECTOR_FAKEBIN}:${PATH}"
DOCKER_CONTEXT_SELECTOR_FAKEBIN="${DOCKER_CONTEXT_SELECTOR_FAKEBIN}" \
  PATH="${DOCKER_CONTEXT_SELECTOR_PATH}" ROOT_DIR="${ROOT_DIR}" BARRIER_VERIFY_ROOT="${BARRIER_VERIFY_ROOT}" \
  /bin/bash "${DOCKER_CONTEXT_SELECTOR_HARNESS}"

DOCKER_BUILDX_REF_SANITIZE_HARNESS="${BARRIER_VERIFY_ROOT}/docker-buildx-ref-sanitize.sh"
cat >"${DOCKER_BUILDX_REF_SANITIZE_HARNESS}" <<'EOF'
set -euo pipefail

buildx_root="${BARRIER_VERIFY_ROOT}/docker-buildx-ref-sanitize"
mkdir -p "${buildx_root}/refs/default/default"
printf '{"LocalPath":"/tmp/stale","DockerfilePath":"/tmp/stale/Dockerfile"}\n' >"${buildx_root}/refs/default/default/ref.json"
printf '{"Key":"default","Name":"","Global":false}\n' >"${buildx_root}/current"

ROOT_DIR="${ROOT_DIR}" BARRIER_VERIFY_ROOT="${BARRIER_VERIFY_ROOT}" HOME=/tmp /bin/bash -lc '
  set -euo pipefail
  source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
  sanitize_workcell_docker_buildx_state "'"${buildx_root}"'"
'

if [[ -e "${buildx_root}/refs/default/default/ref.json" ]]; then
  echo "Expected trusted Docker setup to drop stale buildx refs" >&2
  exit 1
fi
if [[ ! -f "${buildx_root}/current" ]]; then
  echo "Expected trusted Docker buildx sanitization to preserve current builder selection" >&2
  exit 1
fi
EOF
chmod 0755 "${DOCKER_BUILDX_REF_SANITIZE_HARNESS}"
ROOT_DIR="${ROOT_DIR}" BARRIER_VERIFY_ROOT="${BARRIER_VERIFY_ROOT}" /bin/bash "${DOCKER_BUILDX_REF_SANITIZE_HARNESS}"

DOCKER_CLIENT_CWD_HARNESS="${BARRIER_VERIFY_ROOT}/docker-client-cwd.sh"
cat >"${DOCKER_CLIENT_CWD_HARNESS}" <<'EOF'
set -euo pipefail

FAKE_DOCKER_BIN="${BARRIER_VERIFY_ROOT}/docker-client-cwd-bin"
WORKTREE_CWD="${BARRIER_VERIFY_ROOT}/missing-cwd"
mkdir -p "${FAKE_DOCKER_BIN}"
rm -rf "${WORKTREE_CWD}"

cat >"${FAKE_DOCKER_BIN}/docker" <<'EOS'
#!/bin/sh
printf '%s\n' "$PWD"
EOS
chmod 0755 "${FAKE_DOCKER_BIN}/docker"

ROOT_DIR="${ROOT_DIR}" PATH="${FAKE_DOCKER_BIN}:${PATH}" HOME=/tmp /bin/bash -lc '
  set -euo pipefail
  source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
  export HOME="${BARRIER_VERIFY_ROOT}/docker-client-home"
  mkdir -p "${HOME}"
  export WORKCELL_DOCKER_CLIENT_CWD="${HOME}"
  cd "'"${WORKTREE_CWD}"'" 2>/dev/null || true
  output="$(run_workcell_docker_client_command docker context inspect default)"
  if [[ "${output}" != "${HOME}" ]]; then
    echo "Expected Docker client commands to run from the configured safe cwd, got: ${output}" >&2
    exit 1
  fi
'
EOF
chmod 0755 "${DOCKER_CLIENT_CWD_HARNESS}"
ROOT_DIR="${ROOT_DIR}" BARRIER_VERIFY_ROOT="${BARRIER_VERIFY_ROOT}" /bin/bash "${DOCKER_CLIENT_CWD_HARNESS}"

DOCKER_CLIENT_EMPTY_ARGV_HARNESS="${BARRIER_VERIFY_ROOT}/docker-client-empty-argv.sh"
cat >"${DOCKER_CLIENT_EMPTY_ARGV_HARNESS}" <<'EOF'
set -euo pipefail

ROOT_DIR="${ROOT_DIR}" HOME=/tmp /bin/bash -lc '
  set -euo pipefail
  source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
  run_workcell_docker_client_command
'
EOF
chmod 0755 "${DOCKER_CLIENT_EMPTY_ARGV_HARNESS}"
ROOT_DIR="${ROOT_DIR}" /bin/bash "${DOCKER_CLIENT_EMPTY_ARGV_HARNESS}"

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

if rg -q 'chown -R "\$\{HOST_UID\}:\$\{HOST_GID\}" "\$\{target_path\}"' "${ROOT_DIR}/scripts/container-smoke.sh"; then
  echo "Expected scripts/container-smoke.sh to avoid raw recursive chown on host-managed paths" >&2
  exit 1
fi
if rg -q 'tar --null -T "\$\{path_list_filtered\}" -cf -' "${ROOT_DIR}/scripts/container-smoke.sh"; then
  echo "Expected scripts/container-smoke.sh to avoid tar-based smoke workspace staging" >&2
  exit 1
fi
if rg -q 'tar -xf -' "${ROOT_DIR}/scripts/container-smoke.sh"; then
  echo "Expected scripts/container-smoke.sh to avoid tar-based extraction for smoke workspace staging" >&2
  exit 1
fi

if ! "${ROOT_DIR}/scripts/container-smoke.sh" --self-test-host-path-hardening \
  >/tmp/workcell-container-smoke-host-path-hardening.out 2>&1; then
  echo "Expected scripts/container-smoke.sh host-path hardening self-test to pass" >&2
  cat /tmp/workcell-container-smoke-host-path-hardening.out >&2
  exit 1
fi
grep -q '^container-smoke-host-path-hardening-ok$' /tmp/workcell-container-smoke-host-path-hardening.out

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
grep -q "strict mode requires --prepare when you explicitly request --rebuild." /tmp/workcell-strict-rebuild.out

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

if "${ROOT_DIR}/scripts/workcell" --agent codex --allow-control-plane-vcs --dry-run >/tmp/workcell-missing-control-plane-vcs-ack.out 2>&1; then
  echo "Expected --allow-control-plane-vcs without acknowledgement to fail cleanly" >&2
  exit 1
fi
grep -q "control-plane VCS mode requires --ack-control-plane-vcs." /tmp/workcell-missing-control-plane-vcs-ack.out

if "${ROOT_DIR}/scripts/workcell" --dry-run >/tmp/workcell-missing-agent.out 2>&1; then
  echo "Expected workcell without --agent to fail cleanly" >&2
  exit 1
fi
grep -q "Option --agent is required." /tmp/workcell-missing-agent.out
grep -q '^Usage: workcell' /tmp/workcell-missing-agent.out

STRICT_PREFLIGHT_WORKSPACE="${BARRIER_VERIFY_ROOT}/strict-preflight-workspace"
mkdir -p "${STRICT_PREFLIGHT_WORKSPACE}"
printf '# marker\n' >"${STRICT_PREFLIGHT_WORKSPACE}/AGENTS.md"
MISSING_DOCTOR_WORKSPACE="${BARRIER_VERIFY_ROOT}/missing-workspace-for-doctor"
EXPECTED_STRICT_DOCTOR_MISSING_HOST_TOOLS="$(
  expected_doctor_missing_host_tools_csv_for_workspace "${STRICT_PREFLIGHT_WORKSPACE}"
)"
EXPECTED_MISSING_DOCTOR_MISSING_HOST_TOOLS="$(
  expected_doctor_missing_host_tools_csv_for_workspace "${MISSING_DOCTOR_WORKSPACE}"
)"
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
if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --allow-nongit-workspace \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}" \
  --doctor >/tmp/workcell-strict-preflight.out 2>&1; then
  echo "Expected strict-mode doctor to report the missing prepared image without launching the runtime" >&2
  exit 1
fi
grep -q '^doctor_prepared_image=0$' /tmp/workcell-strict-preflight.out
assert_doctor_missing_host_tools /tmp/workcell-strict-preflight.out "${EXPECTED_STRICT_DOCTOR_MISSING_HOST_TOOLS}"
assert_doctor_next_for_prepare /tmp/workcell-strict-preflight.out "${EXPECTED_STRICT_DOCTOR_MISSING_HOST_TOOLS}"

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
grep -q '^provider_native_sandbox_configured=disabled$' /tmp/workcell-inspect.out
grep -q '^provider_native_sandbox_effective=disabled$' /tmp/workcell-inspect.out
grep -q '^provider_native_sandbox_reason=workcell-pinned-off-due-to-bwrap-userns-incompatibility$' /tmp/workcell-inspect.out
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
  --agent claude \
  --no-default-injection-policy \
  --allow-nongit-workspace \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}-claude-inspect" \
  --inspect >/tmp/workcell-inspect-claude.out 2>&1; then
  echo "Expected Claude --inspect to succeed without launching the runtime" >&2
  exit 1
fi
grep -q '^provider_native_sandbox_configured=deferred$' /tmp/workcell-inspect-claude.out
grep -q '^provider_native_sandbox_effective=disabled$' /tmp/workcell-inspect-claude.out
grep -q '^provider_native_sandbox_reason=deferred-until-runtime-prereqs-and-validation$' /tmp/workcell-inspect-claude.out

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --no-default-injection-policy \
  --allow-nongit-workspace \
  --workspace "${STRICT_PREFLIGHT_WORKSPACE}" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}-gemini-inspect" \
  --inspect >/tmp/workcell-inspect-gemini.out 2>&1; then
  echo "Expected Gemini --inspect to succeed without launching the runtime" >&2
  exit 1
fi
grep -q '^provider_native_sandbox_configured=disabled$' /tmp/workcell-inspect-gemini.out
grep -q '^provider_native_sandbox_effective=disabled$' /tmp/workcell-inspect-gemini.out
grep -q '^provider_native_sandbox_reason=workcell-pinned-off-until-validated$' /tmp/workcell-inspect-gemini.out

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
assert_doctor_missing_host_tools /tmp/workcell-doctor.out "${EXPECTED_STRICT_DOCTOR_MISSING_HOST_TOOLS}"
grep -q '^doctor_prepared_image=0$' /tmp/workcell-doctor.out
assert_doctor_next_for_prepare /tmp/workcell-doctor.out "${EXPECTED_STRICT_DOCTOR_MISSING_HOST_TOOLS}"
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
assert_doctor_missing_host_tools /tmp/workcell-doctor-subcommand.out "${EXPECTED_STRICT_DOCTOR_MISSING_HOST_TOOLS}"
assert_doctor_next_for_prepare /tmp/workcell-doctor-subcommand.out "${EXPECTED_STRICT_DOCTOR_MISSING_HOST_TOOLS}"

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --no-default-injection-policy \
  --workspace "${MISSING_DOCTOR_WORKSPACE}" \
  --colima-profile "${STRICT_PREFLIGHT_PROFILE}-missing-doctor" \
  --doctor >/tmp/workcell-doctor-missing.out 2>&1; then
  echo "Expected --doctor to succeed even when the workspace is missing" >&2
  exit 1
fi
grep -q '^doctor_profile_state=absent$' /tmp/workcell-doctor-missing.out
assert_doctor_missing_host_tools /tmp/workcell-doctor-missing.out "${EXPECTED_MISSING_DOCTOR_MISSING_HOST_TOOLS}"
grep -Eq '^workspace=.*/missing-workspace-for-doctor$' /tmp/workcell-doctor-missing.out
grep -q '^workspace_status=missing$' /tmp/workcell-doctor-missing.out
assert_doctor_next_for_missing_workspace /tmp/workcell-doctor-missing.out "${EXPECTED_MISSING_DOCTOR_MISSING_HOST_TOOLS}"

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
assert_doctor_missing_host_tools /tmp/workcell-doctor-stale.out "${EXPECTED_STRICT_DOCTOR_MISSING_HOST_TOOLS}"
grep -q '^doctor_prepared_image=0$' /tmp/workcell-doctor-stale.out
assert_doctor_next_for_prepare /tmp/workcell-doctor-stale.out "${EXPECTED_STRICT_DOCTOR_MISSING_HOST_TOOLS}"
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
grep -q 'Workcell warning: host-persisted launcher debug stderr capture is enabled for this session:' /tmp/workcell-debug-log.out
grep -q 'execution_path=' "${DEBUG_LOG_CAPTURE}"
RUN_COMMAND_DEBUG_FAILURE_HARNESS="${BARRIER_VERIFY_ROOT}/debug/run-command-debug-failure.sh"
RUN_COMMAND_DEBUG_FAILURE_CAPTURE="${BARRIER_VERIFY_ROOT}/debug/run-command-debug-failure.log"
RUN_COMMAND_DEBUG_FAILURE_PERSISTED_LOG="${BARRIER_VERIFY_ROOT}/debug/run-command-debug-failure.persisted.log"
extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" run_command_with_debug_log >"${RUN_COMMAND_DEBUG_FAILURE_HARNESS}"
cat >>"${RUN_COMMAND_DEBUG_FAILURE_HARNESS}" <<EOF
set -euo pipefail
LOG_LEVEL=debug
DEBUG_LOG_PATH="${RUN_COMMAND_DEBUG_FAILURE_PERSISTED_LOG}"
COLIMA_PROFILE="${STRICT_PREFLIGHT_PROFILE}"
PREPARE_ONLY=0
AGENT=codex
BUILD_STATUS=0
exec > >(tee -a "${RUN_COMMAND_DEBUG_FAILURE_PERSISTED_LOG}") 2>&1
run_command_with_debug_log runtime-build /bin/bash -lc 'echo debug-pipeline-failure >&2; exit 23' || BUILD_STATUS=\$?
printf 'build_status=%s\n' "\${BUILD_STATUS}"
EOF
chmod +x "${RUN_COMMAND_DEBUG_FAILURE_HARNESS}"
if ! /bin/bash "${RUN_COMMAND_DEBUG_FAILURE_HARNESS}" >"${RUN_COMMAND_DEBUG_FAILURE_CAPTURE}" 2>&1; then
  echo "Expected debug-mode command failures to return control to the caller" >&2
  cat "${RUN_COMMAND_DEBUG_FAILURE_CAPTURE}" >&2
  exit 1
fi
grep -q '^build_status=23$' "${RUN_COMMAND_DEBUG_FAILURE_CAPTURE}"
grep -q 'Workcell runtime-build failed\.' "${RUN_COMMAND_DEBUG_FAILURE_CAPTURE}"
grep -q 'debug-pipeline-failure' "${RUN_COMMAND_DEBUG_FAILURE_CAPTURE}"
test "$(grep -c '^debug-pipeline-failure$' "${RUN_COMMAND_DEBUG_FAILURE_PERSISTED_LOG}")" = "1"
RUN_COMMAND_DEBUG_DUP_HARNESS="${BARRIER_VERIFY_ROOT}/debug/run-command-debug-dup.sh"
RUN_COMMAND_DEBUG_DUP_LOG="${BARRIER_VERIFY_ROOT}/debug/run-command-debug-dup.log"
extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" run_command_with_debug_log >"${RUN_COMMAND_DEBUG_DUP_HARNESS}"
cat >>"${RUN_COMMAND_DEBUG_DUP_HARNESS}" <<EOF
set -euo pipefail
LOG_LEVEL=debug
DEBUG_LOG_PATH="${RUN_COMMAND_DEBUG_DUP_LOG}"
COLIMA_PROFILE="${STRICT_PREFLIGHT_PROFILE}"
PREPARE_ONLY=0
AGENT=codex
exec > >(tee -a "${RUN_COMMAND_DEBUG_DUP_LOG}") 2>&1
run_command_with_debug_log runtime-build /bin/bash -lc 'echo workcell-debug-unique-line >&2'
EOF
chmod +x "${RUN_COMMAND_DEBUG_DUP_HARNESS}"
if ! /bin/bash "${RUN_COMMAND_DEBUG_DUP_HARNESS}" >/tmp/workcell-run-command-debug-dup.out 2>&1; then
  echo "Expected debug-mode log duplication harness to succeed" >&2
  cat /tmp/workcell-run-command-debug-dup.out >&2
  exit 1
fi
test "$(grep -c '^workcell-debug-unique-line$' "${RUN_COMMAND_DEBUG_DUP_LOG}")" = "1"
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
grep -q 'refusing symlinked host output path component:' /tmp/workcell-debug-log-symlink.out
mkdir -p "${REAL_HOME}/.colima/${DEBUG_LOG_PROFILE}"
printf '%s\n' "${DEBUG_LOG_CAPTURE}" >"${REAL_HOME}/.colima/${DEBUG_LOG_PROFILE}/workcell.latest-debug-log"
if ! "${ROOT_DIR}/scripts/workcell" \
  --logs debug \
  --colima-profile "${DEBUG_LOG_PROFILE}" >/tmp/workcell-logs-debug.out 2>&1; then
  echo "Expected --logs debug to print the latest retained debug log" >&2
  exit 1
fi
grep -q 'execution_path=' /tmp/workcell-logs-debug.out

FILE_TRACE_CAPTURE="${BARRIER_VERIFY_ROOT}/debug/session.file-trace.log"
rm -f "${FILE_TRACE_CAPTURE}"

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
printf '{"projects":{"verify":{"path":"/workspace"}}}\n' >"${AUTH_STATUS_ROOT}/gemini-projects.json"
chmod 0600 "${AUTH_STATUS_ROOT}/gemini-projects.json"
printf 'GOOGLE_GENAI_USE_VERTEXAI true\n' >"${AUTH_STATUS_ROOT}/gemini-invalid.env"
chmod 0600 "${AUTH_STATUS_ROOT}/gemini-invalid.env"
printf '[]\n' >"${AUTH_STATUS_ROOT}/gemini-invalid-oauth.json"
chmod 0600 "${AUTH_STATUS_ROOT}/gemini-invalid-oauth.json"
printf '{}\n' >"${AUTH_STATUS_ROOT}/gcloud-adc-invalid.json"
chmod 0600 "${AUTH_STATUS_ROOT}/gcloud-adc-invalid.json"
printf '{"projects":[]}\n' >"${AUTH_STATUS_ROOT}/gemini-projects-invalid.json"
chmod 0600 "${AUTH_STATUS_ROOT}/gemini-projects-invalid.json"
printf 'GOOGLE_GENAI_USE_GCA=true\n' >"${AUTH_STATUS_ROOT}/gemini-gca.env"
chmod 0600 "${AUTH_STATUS_ROOT}/gemini-gca.env"
cat >"${AUTH_STATUS_ROOT}/gemini-vertex-comment.env" <<'EOF'
GOOGLE_GENAI_USE_VERTEXAI=true
GOOGLE_CLOUD_PROJECT=verify-project
GOOGLE_CLOUD_LOCATION="us-central1" # comment
EOF
chmod 0600 "${AUTH_STATUS_ROOT}/gemini-vertex-comment.env"
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
gemini_projects = "gemini-projects.json"
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
GOOGLE_GENAI_USE_VERTEXAI=true
GOOGLE_API_KEY=verify-google-key
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
grep -q '^provider_auth_modes=gemini_env$' /tmp/workcell-auth-status-gemini.out
grep -q '^shared_auth_modes=github_hosts$' /tmp/workcell-auth-status-gemini.out
grep -q '^github_auth_present=1$' /tmp/workcell-auth-status-gemini.out

cat >"${AUTH_STATUS_ROOT}/adc-only.toml" <<'EOF'
version = 1

[credentials]
gcloud_adc = "gcloud-adc.json"
EOF
cat >"${AUTH_STATUS_ROOT}/invalid-gemini-env.toml" <<'EOF'
version = 1

[credentials]
gemini_env = "gemini-invalid.env"
EOF
cat >"${AUTH_STATUS_ROOT}/invalid-gemini-oauth.toml" <<'EOF'
version = 1

[credentials]
gemini_oauth = "gemini-invalid-oauth.json"
EOF
cat >"${AUTH_STATUS_ROOT}/invalid-gcloud-adc.toml" <<'EOF'
version = 1

[credentials]
gcloud_adc = "gcloud-adc-invalid.json"
EOF
cat >"${AUTH_STATUS_ROOT}/invalid-gemini-projects.toml" <<'EOF'
version = 1

[credentials]
gemini_projects = "gemini-projects-invalid.json"
EOF
cat >"${AUTH_STATUS_ROOT}/gca-only.toml" <<'EOF'
version = 1

[credentials]
gemini_env = "gemini-gca.env"
EOF
cat >"${AUTH_STATUS_ROOT}/vertex-comment.toml" <<'EOF'
version = 1

[credentials]
gemini_env = "gemini-vertex-comment.env"
EOF
if ! "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${AUTH_STATUS_ROOT}/adc-only.toml" \
  --auth-status >/tmp/workcell-auth-status-gemini-adc-only.out 2>&1; then
  echo "Expected Gemini --auth-status to succeed for supplemental gcloud_adc inputs" >&2
  exit 1
fi
grep -q '^provider_auth_mode=none$' /tmp/workcell-auth-status-gemini-adc-only.out
grep -q '^provider_auth_modes=none$' /tmp/workcell-auth-status-gemini-adc-only.out

if "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${AUTH_STATUS_ROOT}/invalid-gemini-env.toml" \
  --auth-status >/tmp/workcell-auth-status-gemini-invalid-env.out 2>&1; then
  echo "Expected Gemini --auth-status to reject malformed Gemini env auth" >&2
  exit 1
fi
grep -q 'malformed Gemini auth env file' /tmp/workcell-auth-status-gemini-invalid-env.out

if "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${AUTH_STATUS_ROOT}/invalid-gemini-oauth.toml" \
  --auth-status >/tmp/workcell-auth-status-gemini-invalid-oauth.out 2>&1; then
  echo "Expected Gemini --auth-status to reject malformed Gemini OAuth JSON" >&2
  exit 1
fi
grep -q 'credentials.gemini_oauth must contain a JSON object' /tmp/workcell-auth-status-gemini-invalid-oauth.out

if "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${AUTH_STATUS_ROOT}/invalid-gcloud-adc.toml" \
  --auth-status >/tmp/workcell-auth-status-gemini-invalid-adc.out 2>&1; then
  echo "Expected Gemini --auth-status to reject malformed Google ADC JSON" >&2
  exit 1
fi
grep -q 'credentials.gcloud_adc must contain a non-empty JSON string field: type' /tmp/workcell-auth-status-gemini-invalid-adc.out

if "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${AUTH_STATUS_ROOT}/invalid-gemini-projects.toml" \
  --auth-status >/tmp/workcell-auth-status-gemini-invalid-projects.out 2>&1; then
  echo "Expected Gemini --auth-status to reject malformed Gemini projects JSON" >&2
  exit 1
fi
grep -q 'credentials.gemini_projects must contain a JSON object with an object-valued projects field' /tmp/workcell-auth-status-gemini-invalid-projects.out

cat >"${AUTH_STATUS_ROOT}/invalid-dotted.toml" <<'EOF'
version = 1
credentials.gemini_env = "gemini.env"
EOF
if "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${AUTH_STATUS_ROOT}/invalid-dotted.toml" \
  --auth-status >/tmp/workcell-auth-status-invalid-dotted.out 2>&1; then
  echo "Expected workcell to reject dotted injection-policy keys through the CLI path" >&2
  exit 1
fi
grep -q 'dotted TOML keys are not supported' /tmp/workcell-auth-status-invalid-dotted.out

cat >"${AUTH_STATUS_ROOT}/invalid-credential-key.toml" <<'EOF'
version = 1
[credentials]
gemini = "gemini.env"
EOF
if "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${AUTH_STATUS_ROOT}/invalid-credential-key.toml" \
  --auth-status >/tmp/workcell-auth-status-invalid-credential-key.out 2>&1; then
  echo "Expected workcell to reject unsupported credential keys through the CLI path" >&2
  exit 1
fi
grep -q 'credentials contains unsupported keys: gemini' /tmp/workcell-auth-status-invalid-credential-key.out

cat >"${AUTH_STATUS_ROOT}/invalid-duplicate-table.toml" <<'EOF'
version = 1

[credentials]
gemini_env = "gemini.env"

[credentials]
gcloud_adc = "adc.json"
EOF
if "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${AUTH_STATUS_ROOT}/invalid-duplicate-table.toml" \
  --auth-status >/tmp/workcell-auth-status-invalid-duplicate-table.out 2>&1; then
  echo "Expected workcell to reject duplicate top-level tables through the CLI path" >&2
  exit 1
fi
grep -q 'duplicate table \[credentials\]' /tmp/workcell-auth-status-invalid-duplicate-table.out

cat >"${AUTH_STATUS_ROOT}/invalid-duplicate-shared-credential-table.toml" <<'EOF'
version = 1

[credentials.github_hosts]
source = "gh-hosts.yml"
providers = ["gemini"]

[credentials.github_hosts]
source = "gh-hosts.yml"
providers = ["codex"]
EOF
if "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --workspace "${BARRIER_VERIFY_ROOT}/missing-workspace-for-auth-status" \
  --injection-policy "${AUTH_STATUS_ROOT}/invalid-duplicate-shared-credential-table.toml" \
  --auth-status >/tmp/workcell-auth-status-invalid-duplicate-shared-credential-table.out 2>&1; then
  echo "Expected workcell to reject duplicate shared credential tables through the CLI path" >&2
  exit 1
fi
grep -q 'duplicate table \[credentials.github_hosts\]' /tmp/workcell-auth-status-invalid-duplicate-shared-credential-table.out

STAGING_PROBE_WORKSPACE="${AUTH_STATUS_ROOT}/staging-probe-workspace"
mkdir -p "${STAGING_PROBE_WORKSPACE}"
printf '# staging probe\n' >"${STAGING_PROBE_WORKSPACE}/AGENTS.md"
STAGING_PROBE_OUTPUT="$("${ROOT_DIR}/scripts/workcell" \
  --self-staging-probe \
  gemini \
  "${STAGING_PROBE_WORKSPACE}" \
  "${AUTH_STATUS_ROOT}/policy.toml" \
  strict)"
if [[ "${STAGING_PROBE_OUTPUT}" != *"injection_bundle_root=${REAL_HOME}/Library/Caches/colima/workcell-host-inputs/workcell-injections."* ]]; then
  echo "Expected staging probe to keep rendered injection bundles under the real Colima-visible cache root" >&2
  printf '%s\n' "${STAGING_PROBE_OUTPUT}" >&2
  exit 1
fi
if [[ "${STAGING_PROBE_OUTPUT}" != *"shadow_root=${REAL_HOME}/Library/Caches/colima/workcell-shadow/shadow."* ]]; then
  echo "Expected staging probe to keep workspace control-plane shadows under the real Colima-visible cache root" >&2
  printf '%s\n' "${STAGING_PROBE_OUTPUT}" >&2
  exit 1
fi
if [[ "${STAGING_PROBE_OUTPUT}" != *'/opt/workcell/host-inputs/credentials/gemini.env:ro'* ]]; then
  echo "Expected staging probe to include the rendered Gemini credential mount" >&2
  printf '%s\n' "${STAGING_PROBE_OUTPUT}" >&2
  exit 1
fi
if ! printf '%s\n' "${STAGING_PROBE_OUTPUT}" | grep -Eq "^direct_mount=${REAL_HOME}/Library/Caches/colima/workcell-host-inputs/workcell-injections\\.[^:]*/direct-mounts/[0-9a-f]{16}:/opt/workcell/host-inputs/credentials/gemini.env:ro$"; then
  echo "Expected staging probe to restage direct credential mounts under the injection bundle root" >&2
  printf '%s\n' "${STAGING_PROBE_OUTPUT}" >&2
  exit 1
fi
if printf '%s\n' "${STAGING_PROBE_OUTPUT}" | grep -Fq "direct_mount=${AUTH_STATUS_ROOT}/gemini.env:/opt/workcell/host-inputs/credentials/gemini.env:ro"; then
  echo "Expected staging probe to avoid binding the original host credential path directly into the runtime" >&2
  printf '%s\n' "${STAGING_PROBE_OUTPUT}" >&2
  exit 1
fi
if [[ "${STAGING_PROBE_OUTPUT}" != *'/workspace/AGENTS.md:ro'* ]]; then
  echo "Expected staging probe to include the masked workspace AGENTS.md mount" >&2
  printf '%s\n' "${STAGING_PROBE_OUTPUT}" >&2
  exit 1
fi
if [[ "${STAGING_PROBE_OUTPUT}" != *'/opt/workcell/workspace-control-plane:ro'* ]]; then
  echo "Expected staging probe to include the workspace import mount" >&2
  printf '%s\n' "${STAGING_PROBE_OUTPUT}" >&2
  exit 1
fi
if printf '%s\n' "${STAGING_PROBE_OUTPUT}" | grep -Eq '^(direct_mount|shadow_mount|workspace_import_mount)=/tmp/workcell-docker\.'; then
  echo "Expected staging probe mount sources to avoid the temporary Docker client sandbox home" >&2
  printf '%s\n' "${STAGING_PROBE_OUTPUT}" >&2
  exit 1
fi

GEMINI_AUTH_FAILURE_HARNESS="$(mktemp)"
{
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" csv_contains_value
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" provider_auth_modes
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" selected_provider_auth_mode
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" provider_auth_recovery_allowed
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" fail_fast_for_missing_gemini_auth
  cat <<'EOF'
AGENT=gemini
PREPARE_ONLY=0
ALLOW_ARBITRARY_COMMAND=0
DRY_RUN=0
INJECTION_POLICY=/tmp/no-gemini-policy.toml
WORKSPACE=/tmp/workcell-gemini-workspace
INJECTION_CREDENTIAL_KEYS=github_hosts
UI=cli
AGENT_ARGS=()
PROVIDER_ARGS=()

status=0
set -- gemini
if ( fail_fast_for_missing_gemini_auth "$@" ) >/tmp/workcell-gemini-auth-harness.stdout 2>/tmp/workcell-gemini-auth-harness.stderr; then
  echo "Expected Gemini missing-auth harness to fail fast" >&2
  exit 1
else
  status=$?
fi
if [[ "${status}" -ne 2 ]]; then
  echo "Expected Gemini missing-auth harness to exit 2, got ${status}" >&2
  exit 1
fi
grep -q 'Gemini auth is not configured for this session.' /tmp/workcell-gemini-auth-harness.stderr
grep -q 'Update /tmp/no-gemini-policy.toml to include credentials.gemini_env or credentials.gemini_oauth.' /tmp/workcell-gemini-auth-harness.stderr
grep -q 'credentials.gcloud_adc only supplements Gemini Vertex auth that is explicitly configured through credentials.gemini_env.' /tmp/workcell-gemini-auth-harness.stderr
grep -q '^Next step: workcell --auth-status --agent gemini --workspace /tmp/workcell-gemini-workspace$' /tmp/workcell-gemini-auth-harness.stderr

INJECTION_CREDENTIAL_KEYS=gcloud_adc
set -- gemini
if ( fail_fast_for_missing_gemini_auth "$@" ) >/tmp/workcell-gemini-auth-adc-only.stdout 2>/tmp/workcell-gemini-auth-adc-only.stderr; then
  echo "Expected bare gcloud_adc to remain insufficient for Gemini fail-fast" >&2
  exit 1
else
  status=$?
fi
if [[ "${status}" -ne 2 ]]; then
  echo "Expected bare gcloud_adc fail-fast to exit 2, got ${status}" >&2
  exit 1
fi
grep -q 'credentials.gcloud_adc only supplements Gemini Vertex auth' /tmp/workcell-gemini-auth-adc-only.stderr

set -- gemini --version
if ! ( fail_fast_for_missing_gemini_auth "$@" ) >/tmp/workcell-gemini-auth-version.stdout 2>/tmp/workcell-gemini-auth-version.stderr; then
  echo "Expected Gemini --version harness to bypass missing-auth fail-fast" >&2
  exit 1
fi
if [[ -s /tmp/workcell-gemini-auth-version.stderr ]]; then
  echo "Expected Gemini --version harness to stay silent" >&2
  exit 1
fi

DRY_RUN=1
set -- gemini
if ! ( fail_fast_for_missing_gemini_auth "$@" ) >/tmp/workcell-gemini-auth-dry-run.stdout 2>/tmp/workcell-gemini-auth-dry-run.stderr; then
  echo "Expected Gemini missing-auth harness to skip dry-run" >&2
  exit 1
fi
if [[ -s /tmp/workcell-gemini-auth-dry-run.stderr ]]; then
  echo "Expected Gemini missing-auth dry-run harness to stay silent" >&2
  exit 1
fi
EOF
} >"${GEMINI_AUTH_FAILURE_HARNESS}"
/bin/bash "${GEMINI_AUTH_FAILURE_HARNESS}"
rm -f "${GEMINI_AUTH_FAILURE_HARNESS}"
PROFILE_PROCESS_MATCH_HARNESS="$(mktemp)"
{
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" profile_process_pids
  cat <<'EOF'
set -euo pipefail

HARNESS_BIN="$(mktemp -d)"
trap 'rm -rf "${HARNESS_BIN}"' EXIT

cat >"${HARNESS_BIN}/pgrep" <<'PGREP'
#!/bin/sh
printf '49909\n49991\n60000\n'
PGREP
cat >"${HARNESS_BIN}/ps" <<'PS'
#!/bin/sh
case "$2" in
  49909)
    printf '%s\n' '/opt/homebrew/bin/limactl hostagent --pidfile /Users/omkharanarasaratnam/.colima/_lima/colima-workcell-workcell-ac42b1dc/ha.pid --socket /Users/omkharanarasaratnam/.colima/_lima/colima-workcell-workcell-ac42b1dc/ha.sock --guestagent /opt/homebrew/share/lima/lima-guestagent.Linux-aarch64.gz colima-workcell-workcell-ac42b1dc'
    ;;
  49991)
    printf '%s\n' 'ssh: /Users/omkharanarasaratnam/.colima/_lima/colima-workcell-workcell-ac42b1dc/ssh.sock [mux]'
    ;;
  60000)
    printf '%s\n' '/opt/homebrew/bin/limactl hostagent --pidfile /Users/omkharanarasaratnam/.colima/_lima/colima-other/ha.pid --socket /Users/omkharanarasaratnam/.colima/_lima/colima-other/ha.sock colima-other'
    ;;
esac
PS
chmod +x "${HARNESS_BIN}/pgrep" "${HARNESS_BIN}/ps"

PATH="${HARNESS_BIN}:${PATH}"
matched="$(profile_process_pids workcell-workcell-ac42b1dc | tr '\n' ' ' | sed 's/[[:space:]]*$//')"
if [[ "${matched}" != "49909 49991" ]]; then
  echo "Expected profile_process_pids to return the stale hostagent and ssh mux, got: ${matched}" >&2
  exit 1
fi
EOF
} >"${PROFILE_PROCESS_MATCH_HARNESS}"
/bin/bash "${PROFILE_PROCESS_MATCH_HARNESS}"
rm -f "${PROFILE_PROCESS_MATCH_HARNESS}"
COLIMA_PROFILE_STATUS_HARNESS="$(mktemp)"
{
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" colima_profile_status
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" maybe_reap_stale_profile_processes
  cat <<'EOF'
set -euo pipefail

ROOT_DIR="__ROOT_DIR__"
TRUSTED_HOST_PATH="${PATH}"

# Match scripts/workcell's scrubbed repo-root go_hostutil invocation.
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

go_hostutil() {
  local host_go_bin=""

  ensure_go_run_env
  host_go_bin="$(resolve_go_bin)"
  (
    cd "${ROOT_DIR}" &&
      env -i \
        PATH="${TRUSTED_HOST_PATH}" \
        HOME=/tmp \
        LC_ALL=C \
        LANG=C \
        GOPATH="${GOPATH}" \
        GOMODCACHE="${GOMODCACHE}" \
        GOCACHE="${GOCACHE}" \
        "${host_go_bin}" run ./cmd/workcell-hostutil "$@"
  )
}

run_host_colima() {
  cat <<'JSON'
{"name":"default","status":"Running"}
{"name":"workcell-workcell-ac42b1dc","status":"Stopped"}
{"name":"workcell-other","status":"Running"}
JSON
}

reap_stale_profile_processes() {
  printf 'reaped:%s\n' "$1"
}

profile_process_pids() {
  case "$1" in
    workcell-stale)
      printf '%s\n' 49909
      ;;
    workcell-empty-list)
      printf '%s\n' 49919
      ;;
    workcell-parse-failure)
      printf '%s\n' 49991
      ;;
  esac
}

status="$(colima_profile_status workcell-workcell-ac42b1dc)"
if [[ "${status}" != "Stopped" ]]; then
  echo "Expected colima_profile_status to return Stopped for the matching profile, got: ${status}" >&2
  exit 1
fi

status="$(colima_profile_status workcell-other)"
if [[ "${status}" != "Running" ]]; then
  echo "Expected colima_profile_status to return Running for the matching profile, got: ${status}" >&2
  exit 1
fi

missing_status_rc=0
if colima_profile_status does-not-exist >/tmp/workcell-colima-profile-status-missing.out 2>&1; then
  echo "Expected colima_profile_status to fail for a missing profile" >&2
  exit 1
else
  missing_status_rc=$?
fi
if ((missing_status_rc != 3)); then
  echo "Expected colima_profile_status to return exit status 3 for a missing profile, got: ${missing_status_rc}" >&2
  exit 1
fi

reaped="$(maybe_reap_stale_profile_processes workcell-workcell-ac42b1dc)"
if [[ "${reaped}" != "reaped:workcell-workcell-ac42b1dc" ]]; then
  echo "Expected maybe_reap_stale_profile_processes to reap only explicit Stopped profiles, got: ${reaped}" >&2
  exit 1
fi

if [[ -n "$(maybe_reap_stale_profile_processes workcell-other)" ]]; then
  echo "Expected maybe_reap_stale_profile_processes to ignore Running profiles" >&2
  exit 1
fi

reaped="$(maybe_reap_stale_profile_processes workcell-stale)"
if [[ "${reaped}" != "reaped:workcell-stale" ]]; then
  echo "Expected maybe_reap_stale_profile_processes to reap missing profiles that still have orphaned processes, got: ${reaped}" >&2
  exit 1
fi

run_host_colima() {
  return 0
}

reaped="$(maybe_reap_stale_profile_processes workcell-empty-list)"
if [[ "${reaped}" != "reaped:workcell-empty-list" ]]; then
  echo "Expected maybe_reap_stale_profile_processes to reap orphaned processes when colima list returns an empty profile set, got: ${reaped}" >&2
  exit 1
fi

run_host_colima() {
  printf '%s\n' '{not-json'
}

if [[ -n "$(maybe_reap_stale_profile_processes workcell-parse-failure)" ]]; then
  echo "Expected maybe_reap_stale_profile_processes to ignore parse failures instead of reaping live profiles blindly" >&2
  exit 1
fi
EOF
} | sed "s|__ROOT_DIR__|${ROOT_DIR}|g" >"${COLIMA_PROFILE_STATUS_HARNESS}"
/bin/bash "${COLIMA_PROFILE_STATUS_HARNESS}"
rm -f "${COLIMA_PROFILE_STATUS_HARNESS}"
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
grep -q 'aiplatform.googleapis.com:443' /tmp/workcell-gemini-network.stderr
grep -q -- '--add-host accounts.google.com:' /tmp/workcell-gemini-network.stdout
grep -q -- '--add-host aiplatform.googleapis.com:' /tmp/workcell-gemini-network.stdout
if ! "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --workspace "${ROOT_DIR}" \
  --injection-policy "${AUTH_STATUS_ROOT}/gca-only.toml" \
  --dry-run >/tmp/workcell-gemini-gca-network.stdout 2>/tmp/workcell-gemini-gca-network.stderr; then
  echo "Expected Gemini dry-run with GOOGLE_GENAI_USE_GCA=true auth to succeed" >&2
  exit 1
fi
grep -q 'accounts.google.com:443' /tmp/workcell-gemini-gca-network.stderr
grep -q 'oauth2.googleapis.com:443' /tmp/workcell-gemini-gca-network.stderr
grep -q 'sts.googleapis.com:443' /tmp/workcell-gemini-gca-network.stderr
grep -q -- '--add-host accounts.google.com:' /tmp/workcell-gemini-gca-network.stdout
grep -q -- '--add-host oauth2.googleapis.com:' /tmp/workcell-gemini-gca-network.stdout
grep -q -- '--add-host sts.googleapis.com:' /tmp/workcell-gemini-gca-network.stdout
if ! "${ROOT_DIR}/scripts/workcell" \
  --agent gemini \
  --workspace "${ROOT_DIR}" \
  --injection-policy "${AUTH_STATUS_ROOT}/vertex-comment.toml" \
  --dry-run >/tmp/workcell-gemini-vertex-comment.stdout 2>/tmp/workcell-gemini-vertex-comment.stderr; then
  echo "Expected Gemini dry-run with commented Vertex location auth to succeed" >&2
  exit 1
fi
grep -q 'aiplatform.googleapis.com:443' /tmp/workcell-gemini-vertex-comment.stderr
grep -q 'us-central1-aiplatform.googleapis.com:443' /tmp/workcell-gemini-vertex-comment.stderr
grep -q -- '--add-host aiplatform.googleapis.com:' /tmp/workcell-gemini-vertex-comment.stdout
grep -q -- '--add-host us-central1-aiplatform.googleapis.com:' /tmp/workcell-gemini-vertex-comment.stdout

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
grep -q -- '--debug-log, --file-trace-log, and --audit-transcript apply only to launched sessions.' /tmp/workcell-nonlaunch-debug-log.out

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
PREMERGE_DEFAULT_HOME="${PREMERGE_HARNESS_ROOT}/home"
if [[ "${OSTYPE:-}" == darwin* ]]; then
  PREMERGE_DEFAULT_SNAPSHOT_PARENT="${PREMERGE_DEFAULT_HOME}/Library/Caches/workcell-validation-snapshots"
else
  PREMERGE_DEFAULT_SNAPSHOT_PARENT="${PREMERGE_DEFAULT_HOME}/.cache/workcell-validation-snapshots"
fi
rm -rf "${PREMERGE_HARNESS_ROOT}"
mkdir -p \
  "${PREMERGE_HARNESS_ROOT}/scripts" \
  "${PREMERGE_HARNESS_ROOT}/tests/scenarios/shared" \
  "${PREMERGE_HARNESS_ROOT}/tools/validator" \
  "${PREMERGE_FAKEBIN}" \
  "${PREMERGE_DEFAULT_HOME}"
install -m 0755 "${ROOT_DIR}/scripts/pre-merge.sh" "${PREMERGE_HARNESS_ROOT}/scripts/pre-merge.sh"
cat >"${PREMERGE_HARNESS_ROOT}/scripts/with-validation-snapshot.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'with-validation-snapshot.sh %s\n' "$*" >>"${PREMERGE_LOG}"
printf 'WORKCELL_VALIDATION_SNAPSHOT_PARENT=%s\n' "${WORKCELL_VALIDATION_SNAPSHOT_PARENT-}" >>"${PREMERGE_LOG}"
while [[ $# -gt 0 ]]; do
  if [[ "$1" == "--" ]]; then
    shift
    break
  fi
  shift
done
cd "${WORKCELL_FAKE_GIT_ROOT}"
"$@"
EOF
chmod 0755 "${PREMERGE_HARNESS_ROOT}/scripts/with-validation-snapshot.sh"
cat >"${PREMERGE_HARNESS_ROOT}/tools/validator/Dockerfile" <<'EOF'
FROM scratch
EOF
for stub in \
  check-pinned-inputs.sh \
  update-upstream-pins.sh \
  update-provider-pins.sh \
  verify-github-macos-release-test-runners.sh \
  verify-upstream-codex-release.sh \
  verify-upstream-claude-release.sh \
  verify-upstream-gemini-release.sh \
  check-workflows.sh \
  validate-repo.sh \
  verify-invariants.sh \
  container-smoke.sh \
  verify-release-bundle.sh \
  verify-reproducible-build.sh; do
  cat >"${PREMERGE_HARNESS_ROOT}/scripts/${stub}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s %s\n' "$(basename "$0")" "$*" >>"${PREMERGE_LOG}"
EOF
  chmod 0755 "${PREMERGE_HARNESS_ROOT}/scripts/${stub}"
done
cat >"${PREMERGE_HARNESS_ROOT}/scripts/verify-reproducible-build.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s %s\n' "$(basename "$0")" "$*" >>"${PREMERGE_LOG}"
printf 'verify-reproducible-build.sh env WORKCELL_REPRO_PLATFORMS=%s\n' "${WORKCELL_REPRO_PLATFORMS-}" >>"${PREMERGE_LOG}"
EOF
chmod 0755 "${PREMERGE_HARNESS_ROOT}/scripts/verify-reproducible-build.sh"
cat >"${PREMERGE_HARNESS_ROOT}/tests/scenarios/shared/test-publish-pr-dry-run.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s %s\n' "$(basename "$0")" "$*" >>"${PREMERGE_LOG}"
EOF
chmod 0755 "${PREMERGE_HARNESS_ROOT}/tests/scenarios/shared/test-publish-pr-dry-run.sh"
cat >"${PREMERGE_FAKEBIN}/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'git %s\n' "$*" >>"${PREMERGE_LOG}"
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
  archive)
    tar -C "${WORKCELL_FAKE_GIT_ROOT}" -cf - scripts tests tools
    ;;
  checkout-index)
    prefix=""
    for arg in "$@"; do
      case "${arg}" in
        --prefix=*)
          prefix="${arg#--prefix=}"
          ;;
      esac
    done
    [[ -n "${prefix}" ]] || {
      echo "missing checkout-index prefix" >&2
      exit 1
    }
    mkdir -p "${prefix}"
    cp -R "${WORKCELL_FAKE_GIT_ROOT}/scripts" "${prefix}/scripts"
    mkdir -p "${prefix}/tests/scenarios"
    cp -R "${WORKCELL_FAKE_GIT_ROOT}/tests/scenarios/shared" "${prefix}/tests/scenarios/shared"
    mkdir -p "${prefix}/tools"
    cp -R "${WORKCELL_FAKE_GIT_ROOT}/tools/validator" "${prefix}/tools/validator"
    ;;
  ls-files)
    (
      cd "${WORKCELL_FAKE_GIT_ROOT}"
      find scripts tests tools -type f -print0 | LC_ALL=C sort -z
    )
    ;;
  init|config|add|commit)
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
  WORKCELL_FAKE_GIT_ROOT="${PREMERGE_HARNESS_ROOT}" \
  WORKCELL_FAKE_GIT_STATUS_OUTPUT='?? stray.txt' \
  "${PREMERGE_HARNESS_ROOT}/scripts/pre-merge.sh" >/tmp/workcell-premerge-dirty.out 2>&1; then
  echo "Expected pre-merge to reject a dirty worktree by default" >&2
  exit 1
fi
grep -q 'clean worktree, including untracked files' /tmp/workcell-premerge-dirty.out

if PATH="${PREMERGE_FAKEBIN}:${PATH}" \
  PREMERGE_LOG="${PREMERGE_LOG}" \
  WORKCELL_FAKE_GIT_ROOT="${PREMERGE_HARNESS_ROOT}" \
  WORKCELL_FAKE_GIT_STATUS_OUTPUT='?? stray.txt' \
  "${PREMERGE_HARNESS_ROOT}/scripts/pre-merge.sh" \
  --local-include-untracked >/tmp/workcell-premerge-local-include.out 2>&1; then
  echo "Expected --local-include-untracked without --local-snapshot worktree to fail" >&2
  exit 1
fi
grep -q -- '--local-include-untracked requires --local-snapshot worktree.' /tmp/workcell-premerge-local-include.out

: >"${PREMERGE_LOG}"
if ! PATH="${PREMERGE_FAKEBIN}:${PATH}" \
  HOME="${PREMERGE_DEFAULT_HOME}" \
  XDG_CACHE_HOME='' \
  PREMERGE_LOG="${PREMERGE_LOG}" \
  WORKCELL_FAKE_GIT_ROOT="${PREMERGE_HARNESS_ROOT}" \
  WORKCELL_FAKE_GIT_STATUS_OUTPUT=$' M README.md\n?? stray.txt\n' \
  WORKCELL_VALIDATION_SNAPSHOT_PARENT='' \
  "${PREMERGE_HARNESS_ROOT}/scripts/pre-merge.sh" \
  --local-snapshot head >/tmp/workcell-premerge-local-snapshot.out 2>&1; then
  echo "Expected --local-snapshot head pre-merge harness to succeed on a dirty worktree" >&2
  cat /tmp/workcell-premerge-local-snapshot.out >&2
  exit 1
fi
grep -q 'local validation will run from snapshot (head).' /tmp/workcell-premerge-local-snapshot.out
grep -q "WORKCELL_VALIDATION_SNAPSHOT_PARENT=${PREMERGE_DEFAULT_SNAPSHOT_PARENT}" "${PREMERGE_LOG}"
grep -q 'check-pinned-inputs.sh ' "${PREMERGE_LOG}"
grep -q 'verify-github-macos-release-test-runners.sh macos-26 macos-15' "${PREMERGE_LOG}"
grep -q 'update-upstream-pins.sh --check' "${PREMERGE_LOG}"

: >"${PREMERGE_LOG}"
if PATH="${PREMERGE_FAKEBIN}:${PATH}" \
  PREMERGE_LOG="${PREMERGE_LOG}" \
  WORKCELL_FAKE_GIT_ROOT="${PREMERGE_HARNESS_ROOT}" \
  WORKCELL_FAKE_GIT_STATUS_OUTPUT=$' M README.md\n?? stray.txt\n' \
  "${PREMERGE_HARNESS_ROOT}/scripts/pre-merge.sh" \
  --allow-dirty \
  --remote >/tmp/workcell-premerge-remote-removed.out 2>&1; then
  echo "Expected removed --remote pre-merge flag to be rejected" >&2
  exit 1
fi
grep -q 'Unknown option: --remote' /tmp/workcell-premerge-remote-removed.out

: >"${PREMERGE_LOG}"
if PATH="${PREMERGE_FAKEBIN}:${PATH}" \
  PREMERGE_LOG="${PREMERGE_LOG}" \
  WORKCELL_FAKE_GIT_ROOT="${PREMERGE_HARNESS_ROOT}" \
  WORKCELL_FAKE_GIT_STATUS_OUTPUT=$' M README.md\n?? stray.txt\n' \
  "${PREMERGE_HARNESS_ROOT}/scripts/pre-merge.sh" \
  --allow-dirty \
  --remote-heavy >/tmp/workcell-premerge-remote-heavy-removed.out 2>&1; then
  echo "Expected removed --remote-heavy pre-merge flag to be rejected" >&2
  exit 1
fi
grep -q 'Unknown option: --remote-heavy' /tmp/workcell-premerge-remote-heavy-removed.out

: >"${PREMERGE_LOG}"
if ! PATH="${PREMERGE_FAKEBIN}:${PATH}" \
  PREMERGE_LOG="${PREMERGE_LOG}" \
  WORKCELL_FAKE_GIT_ROOT="${PREMERGE_HARNESS_ROOT}" \
  WORKCELL_FAKE_GIT_STATUS_OUTPUT=$' M README.md\n?? stray.txt\n' \
  WORKCELL_VALIDATION_SNAPSHOT_PARENT='relative-snapshots' \
  WORKCELL_PREMERGE_REPRO_PLATFORMS='linux/arm64' \
  "${PREMERGE_HARNESS_ROOT}/scripts/pre-merge.sh" \
  --local-snapshot worktree \
  --local-include-untracked >/tmp/workcell-premerge-local-snapshot.out 2>&1; then
  echo "Expected local snapshot pre-merge harness to succeed" >&2
  cat /tmp/workcell-premerge-local-snapshot.out >&2
  exit 1
fi
grep -q 'local validation will run from snapshot (worktree).' /tmp/workcell-premerge-local-snapshot.out
grep -q 'with-validation-snapshot.sh --repo ' "${PREMERGE_LOG}"
grep -q "WORKCELL_VALIDATION_SNAPSHOT_PARENT=${PREMERGE_HARNESS_ROOT}/relative-snapshots" "${PREMERGE_LOG}"
grep -q -- '--mode worktree --include-untracked -- env WORKCELL_PREMERGE_LOCAL_SNAPSHOT_ACTIVE=1 ./scripts/pre-merge.sh --local-snapshot worktree --local-include-untracked' "${PREMERGE_LOG}"
grep -q 'check-pinned-inputs.sh ' "${PREMERGE_LOG}"
grep -q 'verify-github-macos-release-test-runners.sh macos-26 macos-15' "${PREMERGE_LOG}"
grep -q 'update-upstream-pins.sh --check' "${PREMERGE_LOG}"
grep -q 'test-publish-pr-dry-run.sh ' "${PREMERGE_LOG}"
grep -q 'verify-reproducible-build.sh ' "${PREMERGE_LOG}"
grep -q 'verify-reproducible-build.sh env WORKCELL_REPRO_PLATFORMS=linux/arm64' "${PREMERGE_LOG}"
grep -q 'local_premerge_repro_platforms()' "${ROOT_DIR}/scripts/pre-merge.sh"
grep -q 'WORKCELL_PREMERGE_REPRO_PLATFORMS' "${ROOT_DIR}/scripts/pre-merge.sh"
grep -Fq "WORKCELL_REPRO_PLATFORMS=\"\${PREMERGE_REPRO_PLATFORMS}\"" "${ROOT_DIR}/scripts/pre-merge.sh"

FILE_TRACE_SENSITIVITY_HARNESS="$(mktemp)"
cat >"${FILE_TRACE_SENSITIVITY_HARNESS}" <<EOF
#!/usr/bin/env bash
set -euo pipefail
HOME="\$1"
$(extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_file_trace_path_is_sensitive)
workcell_file_trace_path_is_sensitive "\${HOME}/.aws/credentials"
workcell_file_trace_path_is_sensitive "\${HOME}/.docker/config.json"
workcell_file_trace_path_is_sensitive "\${HOME}/.gnupg/pubring.kbx"
workcell_file_trace_path_is_sensitive "\${HOME}/.kube/config"
if workcell_file_trace_path_is_sensitive "\${HOME}/.cache/claude-cli-nodejs/log.json"; then
  echo "Expected file trace sensitivity filter to keep benign cache paths visible" >&2
  exit 1
fi
EOF
chmod 0755 "${FILE_TRACE_SENSITIVITY_HARNESS}"
"${FILE_TRACE_SENSITIVITY_HARNESS}" "${INSTALL_VERIFY_HOME}"
rm -f "${FILE_TRACE_SENSITIVITY_HARNESS}"

if PATH="${PREMERGE_FAKEBIN}:${PATH}" \
  PREMERGE_LOG="${PREMERGE_LOG}" \
  WORKCELL_FAKE_GIT_ROOT="${PREMERGE_HARNESS_ROOT}" \
  "${PREMERGE_HARNESS_ROOT}/scripts/pre-merge.sh" \
  --local-snapshot head \
  --local-include-untracked >/tmp/workcell-premerge-local-snapshot-invalid.out 2>&1; then
  echo "Expected --local-include-untracked without worktree snapshot to fail" >&2
  exit 1
fi
grep -q -- '--local-include-untracked requires --local-snapshot worktree.' /tmp/workcell-premerge-local-snapshot-invalid.out

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
SECRET_DRY_RUN_OUTPUT="$(
  AWS_SECRET_ACCESS_KEY='verify-aws-secret' \
    GITHUB_TOKEN='verify-gh-token' \
    SSH_AUTH_SOCK='/tmp/workcell-secret-sock' \
    "${ROOT_DIR}/scripts/workcell" \
    --agent codex \
    --mode strict \
    --workspace "${MASK_VERIFY_WORKSPACE}" \
    --dry-run 2>/dev/null
)"

for forbidden in "docker.sock" "SSH_AUTH_SOCK" "/.ssh" "/.aws" "Library/Keychains" ".gnupg"; do
  if echo "${DRY_RUN_OUTPUT}" | grep -q "${forbidden}"; then
    echo "Unexpected host exposure in dry-run output: ${forbidden}" >&2
    exit 1
  fi
done

for forbidden in "verify-aws-secret" "verify-gh-token" "/tmp/workcell-secret-sock"; do
  if echo "${SECRET_DRY_RUN_OUTPUT}" | grep -Fq -- "${forbidden}"; then
    echo "Unexpected host secret forwarding in dry-run output: ${forbidden}" >&2
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

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --mode strict \
  --workspace "${MASK_VERIFY_WORKSPACE}" \
  --allow-control-plane-vcs \
  --ack-control-plane-vcs \
  --dry-run >/tmp/workcell-control-plane-vcs.stdout 2>/tmp/workcell-control-plane-vcs.stderr; then
  echo "Expected acknowledged control-plane VCS dry-run to succeed" >&2
  cat /tmp/workcell-control-plane-vcs.stderr >&2
  exit 1
fi
grep -q 'session_assurance_initial=lower-assurance-control-plane-vcs' /tmp/workcell-control-plane-vcs.stderr
grep -q 'workspace_control_plane=readonly-vcs' /tmp/workcell-control-plane-vcs.stderr
grep -q 'execution_path=lower-assurance-control-plane-vcs' /tmp/workcell-control-plane-vcs.stderr
grep -q 'WORKCELL_ALLOW_CONTROL_PLANE_VCS=1' /tmp/workcell-control-plane-vcs.stdout
grep -q -- "${MASK_VERIFY_WORKSPACE}/AGENTS.md:/workspace/AGENTS.md:ro" /tmp/workcell-control-plane-vcs.stdout

PUBLISH_PR_FIXTURE="${BARRIER_VERIFY_ROOT}/publish-pr-fixture"
mkdir -p "${PUBLISH_PR_FIXTURE}"
git init -q "${PUBLISH_PR_FIXTURE}"
git -C "${PUBLISH_PR_FIXTURE}" config user.name "Workcell Verify"
git -C "${PUBLISH_PR_FIXTURE}" config user.email "workcell-verify@example.com"
git -C "${PUBLISH_PR_FIXTURE}" remote add origin https://github.com/example/workcell-publish-fixture.git
printf 'base\n' >"${PUBLISH_PR_FIXTURE}/tracked.txt"
git -C "${PUBLISH_PR_FIXTURE}" add tracked.txt
git -C "${PUBLISH_PR_FIXTURE}" commit -q -m init
printf 'worktree\n' >"${PUBLISH_PR_FIXTURE}/tracked.txt"
cat <<'EOF' >"${PUBLISH_PR_FIXTURE}/pr-title.txt"
Verify PR title
EOF
cat <<'EOF' >"${PUBLISH_PR_FIXTURE}/pr-body.md"
Verify PR body
EOF
cat <<'EOF' >"${PUBLISH_PR_FIXTURE}/commit-message.txt"
Verify publish-pr commit

- include staged workspace changes
EOF
PUBLISH_PR_DRY_RUN="$("${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${PUBLISH_PR_FIXTURE}" \
  --branch feature/publish-fixture \
  --title-file "${PUBLISH_PR_FIXTURE}/pr-title.txt" \
  --body-file "${PUBLISH_PR_FIXTURE}/pr-body.md" \
  --commit-message-file "${PUBLISH_PR_FIXTURE}/commit-message.txt" \
  --snapshot worktree \
  --dry-run)"
grep -q '^publish_snapshot=worktree$' <<<"${PUBLISH_PR_DRY_RUN}"
grep -q '^publish_branch=feature/publish-fixture$' <<<"${PUBLISH_PR_DRY_RUN}"
grep -q -- ' -c core.hooksPath=/dev/null -C ' <<<"${PUBLISH_PR_DRY_RUN}"
grep -q -- 'switch --no-guess -c feature/publish-fixture' <<<"${PUBLISH_PR_DRY_RUN}"
grep -q -- ' add -A ' <<<"${PUBLISH_PR_DRY_RUN}"
grep -q -- ' commit --no-verify -S -F ' <<<"${PUBLISH_PR_DRY_RUN}"
grep -q -- ' push --no-verify -u origin feature/publish-fixture ' <<<"${PUBLISH_PR_DRY_RUN}"
grep -q -- 'gh pr create --base main --head feature/publish-fixture --title Verify\\ PR\\ title --draft --body-file' <<<"${PUBLISH_PR_DRY_RUN}"

git -C "${PUBLISH_PR_FIXTURE}" add tracked.txt
PUBLISH_PR_INDEX_DRY_RUN="$("${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${PUBLISH_PR_FIXTURE}" \
  --branch feature/publish-index \
  --title "Index publish title" \
  --commit-message "Index publish commit" \
  --snapshot index \
  --dry-run)"
grep -q '^publish_snapshot=index$' <<<"${PUBLISH_PR_INDEX_DRY_RUN}"
if grep -q -- ' add -A ' <<<"${PUBLISH_PR_INDEX_DRY_RUN}"; then
  echo "publish-pr index snapshot should not auto-stage the worktree" >&2
  exit 1
fi
grep -q -- ' -c core.hooksPath=/dev/null -C ' <<<"${PUBLISH_PR_INDEX_DRY_RUN}"
grep -q -- 'switch --no-guess -c feature/publish-index' <<<"${PUBLISH_PR_INDEX_DRY_RUN}"
grep -q -- ' commit --no-verify -S -F ' <<<"${PUBLISH_PR_INDEX_DRY_RUN}"

if "${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${PUBLISH_PR_FIXTURE}" \
  --branch main \
  --title "Bad branch" \
  --commit-message "Bad branch commit" \
  --dry-run >/tmp/workcell-publish-pr-main.out 2>&1; then
  echo "Expected publish-pr to reject the default branch" >&2
  exit 1
fi
grep -q 'publish-pr refuses the default branch' /tmp/workcell-publish-pr-main.out

if "${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${PUBLISH_PR_FIXTURE}" \
  --branch topic.lock \
  --title "Bad branch format" \
  --commit-message "Bad branch format commit" \
  --dry-run >/tmp/workcell-publish-pr-invalid-branch.out 2>&1; then
  echo "Expected publish-pr to reject an invalid branch name" >&2
  exit 1
fi
grep -q 'Invalid publish branch name: topic.lock' /tmp/workcell-publish-pr-invalid-branch.out

if "${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${PUBLISH_PR_FIXTURE}" \
  --branch feature/publish-invalid-base \
  --base topic.lock \
  --title "Bad base branch format" \
  --commit-message "Bad base branch format commit" \
  --dry-run >/tmp/workcell-publish-pr-invalid-base.out 2>&1; then
  echo "Expected publish-pr to reject an invalid base branch name" >&2
  exit 1
fi
grep -q 'Invalid publish base branch name: topic.lock' /tmp/workcell-publish-pr-invalid-base.out

cat <<'EOF' >"${PUBLISH_PR_FIXTURE}/gh-untrusted"
#!/usr/bin/env bash
printf 'https://example.invalid/pr/123\n'
EOF
chmod +x "${PUBLISH_PR_FIXTURE}/gh-untrusted"
if "${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${PUBLISH_PR_FIXTURE}" \
  --branch feature/publish-untrusted-gh \
  --gh-bin "${PUBLISH_PR_FIXTURE}/gh-untrusted" \
  --title "Untrusted gh" \
  --commit-message "Untrusted gh commit" \
  --dry-run >/tmp/workcell-publish-pr-untrusted-gh.out 2>&1; then
  echo "Expected publish-pr to reject an untrusted --gh-bin path" >&2
  exit 1
fi
grep -q 'gh-bin must point to a trusted host executable path' /tmp/workcell-publish-pr-untrusted-gh.out

if HOST_GH_BIN="${PUBLISH_PR_FIXTURE}/gh-untrusted" bash "${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${PUBLISH_PR_FIXTURE}" \
  --branch feature/publish-untrusted-host-gh \
  --title "Untrusted host gh" \
  --commit-message "Untrusted host gh commit" \
  --dry-run >/tmp/workcell-publish-pr-untrusted-host-gh.out 2>&1; then
  echo "Expected publish-pr to reject an untrusted HOST_GH_BIN path" >&2
  exit 1
fi
grep -q 'HOST_GH_BIN must point to a trusted host executable path' /tmp/workcell-publish-pr-untrusted-host-gh.out

git -C "${PUBLISH_PR_FIXTURE}" reset -q --hard HEAD
git -C "${PUBLISH_PR_FIXTURE}" clean -fdq
if "${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${PUBLISH_PR_FIXTURE}" \
  --branch feature/publish-noop \
  --title "No changes" \
  --commit-message "No changes commit" \
  --snapshot worktree \
  --dry-run >/tmp/workcell-publish-pr-noop.out 2>&1; then
  echo "Expected publish-pr to reject an empty worktree snapshot" >&2
  exit 1
fi
grep -q 'publish-pr found no workspace changes to publish' /tmp/workcell-publish-pr-noop.out

SHADOW_GIT_CONFIG_HARNESS="$(mktemp)"
{
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" lower_ascii
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" git_config_key_is_blocked
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" git_alias_value_is_blocked
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" git_commit_short_arg_is_no_verify
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/scripts/workcell" sanitize_shadowed_git_config
  cat <<'EOF'
set -Eeuo pipefail
HOST_GIT_BIN="$(command -v git)"
sanitize_shadowed_git_config "$1"
EOF
} >"${SHADOW_GIT_CONFIG_HARNESS}"
chmod +x "${SHADOW_GIT_CONFIG_HARNESS}"
SHADOW_GIT_CONFIG_FIXTURE="${BARRIER_VERIFY_ROOT}/shadowed-git-config"
cat <<'EOF' >"${SHADOW_GIT_CONFIG_FIXTURE}"
[core]
  askPass = /tmp/askpass
  editor = /tmp/editor
  fsMonitor = /tmp/fsmonitor
  hooksPath = /tmp/hooks
  pager = /tmp/pager
  sshCommand = ssh -F /tmp/config
  worktree = /tmp/worktree
[credential]
  helper = store
[credential "https://example.invalid"]
  helper = cache
[diff]
  external = /tmp/diff
[include]
  path = /tmp/include
[includeIf "gitdir:/tmp/"]
  path = /tmp/includeif
[pager]
  log = /tmp/pager-log
[sequence]
  editor = /tmp/sequence-editor
[alias]
  bad = -c core.fsmonitor=/tmp/fsmonitor status
  good = status
[safe]
  keep = value
EOF
"${SHADOW_GIT_CONFIG_HARNESS}" "${SHADOW_GIT_CONFIG_FIXTURE}"
for blocked_key in \
  core.askpass \
  core.editor \
  core.fsmonitor \
  core.hookspath \
  core.pager \
  core.sshcommand \
  core.worktree \
  credential.helper \
  diff.external \
  include.path \
  pager.log \
  sequence.editor \
  alias.bad; do
  if git config --file "${SHADOW_GIT_CONFIG_FIXTURE}" --get-all "${blocked_key}" >/dev/null 2>&1; then
    echo "Expected sanitize_shadowed_git_config to strip ${blocked_key}" >&2
    exit 1
  fi
done
if git config --file "${SHADOW_GIT_CONFIG_FIXTURE}" --name-only --get-regexp '^credential\..*\.helper$' >/dev/null 2>&1; then
  echo "Expected sanitize_shadowed_git_config to strip credential.*.helper entries" >&2
  exit 1
fi
if git config --file "${SHADOW_GIT_CONFIG_FIXTURE}" --name-only --get-regexp '^includeif\..*\.path$' >/dev/null 2>&1; then
  echo "Expected sanitize_shadowed_git_config to strip includeIf.*.path entries" >&2
  exit 1
fi
[[ "$(git config --file "${SHADOW_GIT_CONFIG_FIXTURE}" --get alias.good)" == "status" ]]
[[ "$(git config --file "${SHADOW_GIT_CONFIG_FIXTURE}" --get safe.keep)" == "value" ]]

BUILDVCS_FIXTURE="${BARRIER_VERIFY_ROOT}/buildvcs-fixture"
mkdir -p "${BUILDVCS_FIXTURE}"
git init -q "${BUILDVCS_FIXTURE}"
cat <<'EOF' >"${BUILDVCS_FIXTURE}/go.mod"
module example.com/workcell/buildvcsfixture

go 1.25.0
EOF
cat <<'EOF' >"${BUILDVCS_FIXTURE}/main.go"
package main

func main() {}
EOF
BUILDVCS_MARKER="${BUILDVCS_FIXTURE}/fsmonitor.log"
cat >"${BUILDVCS_FIXTURE}/fsmonitor.sh" <<EOF
#!/usr/bin/env bash
set -euo pipefail
printf 'fsmonitor-invoked\n' >>"${BUILDVCS_MARKER}"
exit 0
EOF
chmod +x "${BUILDVCS_FIXTURE}/fsmonitor.sh"
BUILDVCS_OUTPUT="${BUILDVCS_FIXTURE}/fixture-bin"
git -C "${BUILDVCS_FIXTURE}" config core.fsmonitor "${BUILDVCS_FIXTURE}/fsmonitor.sh"
build_go_tool_in_repo "${BUILDVCS_FIXTURE}" "${BUILDVCS_OUTPUT}" .
[[ -x "${BUILDVCS_OUTPUT}" ]]
if [[ -e "${BUILDVCS_MARKER}" ]]; then
  echo "Expected build_go_tool_in_repo to avoid repo-controlled fsmonitor execution" >&2
  exit 1
fi

MASK_SNAPSHOT_WORKSPACE="${BARRIER_VERIFY_ROOT}/mask-snapshot-workspace"
mkdir -p "${MASK_SNAPSHOT_WORKSPACE}/.claude"
git init -q "${MASK_SNAPSHOT_WORKSPACE}"
git -C "${MASK_SNAPSHOT_WORKSPACE}" config user.name "Workcell Verify"
git -C "${MASK_SNAPSHOT_WORKSPACE}" config user.email "workcell-verify@example.com"
cat <<'EOF' >"${MASK_SNAPSHOT_WORKSPACE}/AGENTS.md"
# committed instructions
EOF
cat <<'EOF' >"${MASK_SNAPSHOT_WORKSPACE}/.claude/settings.json"
{"tracked": true}
EOF
git -C "${MASK_SNAPSHOT_WORKSPACE}" add AGENTS.md .claude/settings.json
git -C "${MASK_SNAPSHOT_WORKSPACE}" commit -q -m init
cat <<'EOF' >"${MASK_SNAPSHOT_WORKSPACE}/AGENTS.md"
# modified instructions
EOF
rm -f "${MASK_SNAPSHOT_WORKSPACE}/.claude/settings.json"
MASK_SNAPSHOT_OUTPUT="$("${ROOT_DIR}/scripts/workcell" \
  --self-staging-probe \
  codex \
  "${MASK_SNAPSHOT_WORKSPACE}" \
  "${AUTH_STATUS_ROOT}/policy.toml" \
  strict \
  0 \
  1)"
MASK_SNAPSHOT_ROOT="$(printf '%s\n' "${MASK_SNAPSHOT_OUTPUT}" | sed -n 's/^shadow_root=//p' | head -n1)"
if [[ -z "${MASK_SNAPSHOT_ROOT}" ]] || [[ ! -d "${MASK_SNAPSHOT_ROOT}" ]]; then
  echo "Expected staging probe to expose a shadow root for tracked control-plane snapshots" >&2
  printf '%s\n' "${MASK_SNAPSHOT_OUTPUT}" >&2
  exit 1
fi
grep -q '^# committed instructions$' "${MASK_SNAPSHOT_ROOT}/files/AGENTS.md"
if grep -q '^# modified instructions$' "${MASK_SNAPSHOT_ROOT}/files/AGENTS.md"; then
  echo "Expected masked root control files to reflect the git index snapshot, not modified workspace contents" >&2
  exit 1
fi
grep -q '"tracked": true' "${MASK_SNAPSHOT_ROOT}/dirs/.claude/settings.json"
remove_tree_safely "${MASK_SNAPSHOT_ROOT}"

CONFLICT_SHADOW_REPO="${BARRIER_VERIFY_ROOT}/conflict-shadow-repo"
git init -q "${CONFLICT_SHADOW_REPO}"
git -C "${CONFLICT_SHADOW_REPO}" config user.name "Workcell Verify"
git -C "${CONFLICT_SHADOW_REPO}" config user.email "workcell-verify@example.com"
mkdir -p "${CONFLICT_SHADOW_REPO}/.claude"
cat <<'EOF' >"${CONFLICT_SHADOW_REPO}/.claude/settings.json"
{"value":"base"}
EOF
git -C "${CONFLICT_SHADOW_REPO}" add .claude/settings.json
git -C "${CONFLICT_SHADOW_REPO}" commit -q -m init
git -C "${CONFLICT_SHADOW_REPO}" checkout -q -b other
cat <<'EOF' >"${CONFLICT_SHADOW_REPO}/.claude/settings.json"
{"value":"other"}
EOF
git -C "${CONFLICT_SHADOW_REPO}" commit -q -am other
git -C "${CONFLICT_SHADOW_REPO}" checkout -q master
cat <<'EOF' >"${CONFLICT_SHADOW_REPO}/.claude/settings.json"
{"value":"master"}
EOF
git -C "${CONFLICT_SHADOW_REPO}" commit -q -am master
if git -C "${CONFLICT_SHADOW_REPO}" merge other >/tmp/workcell-conflict-shadow-merge.out 2>&1; then
  echo "Expected conflict-shadow fixture merge to leave unresolved index stages" >&2
  exit 1
fi
CONFLICT_SHADOW_OUTPUT="$("${ROOT_DIR}/scripts/workcell" \
  --self-staging-probe \
  codex \
  "${CONFLICT_SHADOW_REPO}" \
  "${AUTH_STATUS_ROOT}/policy.toml" \
  strict \
  0 \
  1)"
CONFLICT_SHADOW_ROOT="$(printf '%s\n' "${CONFLICT_SHADOW_OUTPUT}" | sed -n 's/^shadow_root=//p' | head -n1)"
if [[ -z "${CONFLICT_SHADOW_ROOT}" ]] || [[ ! -d "${CONFLICT_SHADOW_ROOT}" ]]; then
  echo "Expected conflicted shadow staging probe to expose a shadow root" >&2
  printf '%s\n' "${CONFLICT_SHADOW_OUTPUT}" >&2
  exit 1
fi
if [[ -e "${CONFLICT_SHADOW_ROOT}/dirs/.claude/settings.json" ]]; then
  echo "Expected unresolved git index entries to be excluded from the control-plane shadow without leaking stage 1/2/3 blobs" >&2
  cat "${CONFLICT_SHADOW_ROOT}/dirs/.claude/settings.json" >&2
  exit 1
fi
remove_tree_safely "${CONFLICT_SHADOW_ROOT}"

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

if echo "${ARBITRARY_DRY_RUN_OUTPUT}" | grep -q -- '--entrypoint bash'; then
  echo "Expected arbitrary command path to stay on the managed container entrypoint" >&2
  exit 1
fi
if ! echo "${ARBITRARY_DRY_RUN_OUTPUT}" | grep -q -- '-e WORKCELL_ALLOW_ARBITRARY_COMMAND=1'; then
  echo "Expected arbitrary command path to declare explicit lower-assurance runtime handling" >&2
  exit 1
fi
if ! echo "${ARBITRARY_DRY_RUN_OUTPUT}" | grep -q -- 'workcell:local bash -lc true '; then
  echo "Expected arbitrary command path to preserve the explicit runtime command arguments" >&2
  exit 1
fi

if ! "${ROOT_DIR}/scripts/workcell" \
  --agent codex \
  --mode development \
  --dry-run \
  -- bash -lc true \
  >/tmp/workcell-development-command.stdout 2>/tmp/workcell-development-command.stderr; then
  echo "Expected managed development command dry-run to succeed" >&2
  cat /tmp/workcell-development-command.stderr >&2
  exit 1
fi
grep -q 'profile=.* mode=development agent=codex ' /tmp/workcell-development-command.stderr
grep -q 'execution_path=lower-assurance-development' /tmp/workcell-development-command.stderr
grep -q -- ' bash -lc true ' /tmp/workcell-development-command.stdout
if grep -q -- '--entrypoint bash' /tmp/workcell-development-command.stdout; then
  echo "Development command dry-run should stay on the managed entrypoint" >&2
  exit 1
fi
if grep -q -- 'workcell:local codex bash -lc true ' /tmp/workcell-development-command.stdout; then
  echo "Development command dry-run should not prepend the provider binary to explicit shell commands" >&2
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
  if [[ "$(free_bytes_for_path "${ROOT_DIR}")" -lt $((5 * 1024 * 1024 * 1024)) ]]; then
    echo "Cannot run live-debug audit verification on Darwin: host filesystem has less than 5 GiB free." >&2
    exit 1
  else
    LIVE_DEBUG_PROFILE_NAME="workcell-live-debug-$$"
    LIVE_DETACHED_PROFILE_NAME="wcl-live-det-$$"
    delete_verify_colima_profile "${LIVE_DEBUG_PROFILE_NAME}"
    delete_verify_colima_profile "${LIVE_DETACHED_PROFILE_NAME}"
    LIVE_DEBUG_LOG="${BARRIER_VERIFY_ROOT}/debug/live-debug.log"
    LIVE_DEBUG_PREPARE_OUT="${BARRIER_VERIFY_ROOT}/debug/live-debug.prepare.out"
    LIVE_DEBUG_REFRESH_OUT="${BARRIER_VERIFY_ROOT}/debug/live-debug.refresh.out"
    LIVE_DEBUG_FILE_TRACE_OUT="${BARRIER_VERIFY_ROOT}/debug/live-debug.file-trace.out"
    LIVE_DEBUG_LOGS_FILE_TRACE_OUT="${BARRIER_VERIFY_ROOT}/debug/live-debug.logs-file-trace.out"
    LIVE_DEBUG_INSPECT_FILE_TRACE_OUT="${BARRIER_VERIFY_ROOT}/debug/live-debug.inspect-file-trace.out"
    LIVE_DEBUG_LOGS_DEBUG_OUT="${BARRIER_VERIFY_ROOT}/debug/live-debug.logs-debug.out"
    AUDIT_SESSION_LOG="${BARRIER_VERIFY_ROOT}/debug/live-debug.audit-session.log"
    if ! "${ROOT_DIR}/scripts/workcell" \
      --agent codex \
      --prepare-only \
      --rebuild \
      --workspace "${ROOT_DIR}" \
      --vm-memory 6 \
      --injection-policy "${AUTH_STATUS_ROOT}/policy.toml" \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
      --debug-log "${LIVE_DEBUG_LOG}" >"${LIVE_DEBUG_PREPARE_OUT}" 2>&1; then
      echo "Expected audit verification prepare run to seed a managed image" >&2
      cat "${LIVE_DEBUG_PREPARE_OUT}" >&2
      exit 1
    fi
    assert_output_matches_regex 'Starting managed Colima profile|starting colima' "${LIVE_DEBUG_LOG}" \
      "Expected audit verification prepare run debug log to capture managed Colima startup"
    assert_output_matches_regex 'Preparing the runtime image for profile|runtime-build|runtime-builder' "${LIVE_DEBUG_LOG}" \
      "Expected audit verification prepare run debug log to capture runtime image preparation"
    if ! "${ROOT_DIR}/scripts/workcell" \
      --agent codex \
      --workspace "${ROOT_DIR}" \
      --vm-memory 6 \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
      --file-trace-log "${FILE_TRACE_CAPTURE}" \
      --agent-arg --version >"${LIVE_DEBUG_FILE_TRACE_OUT}" 2>&1; then
      echo "Expected launched session with --file-trace-log to succeed" >&2
      cat "${LIVE_DEBUG_FILE_TRACE_OUT}" >&2
      exit 1
    fi
    test -s "${FILE_TRACE_CAPTURE}"
    grep -q 'event=provider-launch' "${FILE_TRACE_CAPTURE}"
    grep -q 'event=watch-start' "${FILE_TRACE_CAPTURE}"
    grep -q 'event=provider-exit' "${FILE_TRACE_CAPTURE}"
    if ! "${ROOT_DIR}/scripts/workcell" \
      --logs file-trace \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" >"${LIVE_DEBUG_LOGS_FILE_TRACE_OUT}" 2>&1; then
      echo "Expected --logs file-trace to print the latest retained file trace log" >&2
      exit 1
    fi
    grep -q 'event=provider-launch' "${LIVE_DEBUG_LOGS_FILE_TRACE_OUT}"
    if ! "${ROOT_DIR}/scripts/workcell" \
      --inspect \
      --agent codex \
      --workspace "${ROOT_DIR}" \
      --vm-memory 6 \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" >"${LIVE_DEBUG_INSPECT_FILE_TRACE_OUT}" 2>&1; then
      echo "Expected --inspect to surface the latest retained file trace log" >&2
      cat "${LIVE_DEBUG_INSPECT_FILE_TRACE_OUT}" >&2
      exit 1
    fi
    grep -q "latest_file_trace_log=${FILE_TRACE_CAPTURE}" "${LIVE_DEBUG_INSPECT_FILE_TRACE_OUT}"
    if ! "${ROOT_DIR}/scripts/workcell" \
      --logs debug \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" >"${LIVE_DEBUG_LOGS_DEBUG_OUT}" 2>&1; then
      echo "Expected successful prepare run to persist the latest debug-log pointer" >&2
      exit 1
    fi
    assert_output_matches_regex 'Starting managed Colima profile|starting colima' "${LIVE_DEBUG_LOGS_DEBUG_OUT}" \
      "Expected workcell logs debug to print the retained managed Colima startup log"
    for agent in codex claude gemini; do
      if ! GIT_PAGER=cat PAGER=cat \
        "${ROOT_DIR}/scripts/workcell" \
        --agent "${agent}" \
        --mode development \
        --workspace "${ROOT_DIR}" \
        --vm-memory 6 \
        --no-default-injection-policy \
        --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
        -- bash -lc 'git -c safe.directory=/workspace status --short >/tmp/workcell-development-shell.out && printf "WORKCELL_DEVELOPMENT_SHELL_OK\n"' \
        >"${BARRIER_VERIFY_ROOT}/debug/live-development-shell-${agent}.out" 2>&1; then
        echo "Expected managed development shell command to succeed for ${agent} even with inherited host pager env" >&2
        cat "${BARRIER_VERIFY_ROOT}/debug/live-development-shell-${agent}.out" >&2
        exit 1
      fi
      grep -q '^WORKCELL_DEVELOPMENT_SHELL_OK$' "${BARRIER_VERIFY_ROOT}/debug/live-development-shell-${agent}.out"
      if [[ "${agent}" == "codex" ]] &&
        grep -Eq 'Preparing the runtime image for profile|runtime-build|429 Too Many Requests' \
          "${BARRIER_VERIFY_ROOT}/debug/live-development-shell-${agent}.out"; then
        echo "Expected refreshed managed development shell to reuse the prepared runtime image without rebuilding" >&2
        cat "${BARRIER_VERIFY_ROOT}/debug/live-development-shell-${agent}.out" >&2
        exit 1
      fi
    done
    if ! GIT_PAGER=cat PAGER=cat \
      "${ROOT_DIR}/scripts/workcell" \
      --agent codex \
      --mode development \
      --workspace "${ROOT_DIR}" \
      --vm-memory 7 \
      --no-default-injection-policy \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
      -- bash -lc 'git -c safe.directory=/workspace status --short >/tmp/workcell-development-shell-refresh.out && printf "WORKCELL_DEVELOPMENT_REFRESH_OK\n"' \
      >"${LIVE_DEBUG_REFRESH_OUT}" 2>&1; then
      echo "Expected managed development shell refresh lane to succeed after the reviewed VM resources changed" >&2
      cat "${LIVE_DEBUG_REFRESH_OUT}" >&2
      exit 1
    fi
    grep -q '^WORKCELL_DEVELOPMENT_REFRESH_OK$' "${LIVE_DEBUG_REFRESH_OUT}"
    grep -q "Refreshing managed Colima profile ${LIVE_DEBUG_PROFILE_NAME} to apply the requested reviewed VM resources." "${LIVE_DEBUG_REFRESH_OUT}"
    grep -q "Restored the prepared runtime image from cache for profile ${LIVE_DEBUG_PROFILE_NAME}." "${LIVE_DEBUG_REFRESH_OUT}"
    if grep -Eq 'Preparing the runtime image for profile|runtime-build|429 Too Many Requests' "${LIVE_DEBUG_REFRESH_OUT}"; then
      echo "Expected refreshed managed development shell to restore the prepared runtime image from cache without rebuilding" >&2
      cat "${LIVE_DEBUG_REFRESH_OUT}" >&2
      exit 1
    fi
    DETACHED_SESSION_START_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.start.out"
    DETACHED_SESSION_SHOW_RUNNING_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.show-running.out"
    DETACHED_SESSION_ATTACH_TYPESCRIPT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.attach.typescript"
    DETACHED_SESSION_SEND_ALPHA_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.send-alpha.out"
    DETACHED_SESSION_SEND_BETA_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.send-beta.out"
    DETACHED_SESSION_DIFF_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.diff.out"
    DETACHED_SESSION_STOP_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.stop.out"
    DETACHED_SESSION_SHOW_STOPPED_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.show-stopped.out"
    DETACHED_SESSION_TIMELINE_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.timeline.out"
    DETACHED_SESSION_LOGS_AUDIT_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.logs-audit.out"
    DETACHED_SESSION_LOGS_DEBUG_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.logs-debug.out"
    DETACHED_SESSION_LOGS_FILE_TRACE_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.logs-file-trace.out"
    DETACHED_SESSION_LOGS_TRANSCRIPT_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.logs-transcript.out"
    DETACHED_SESSION_LIST_OUT="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.list.out"
    DETACHED_SESSION_DEBUG_LOG="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.debug.log"
    DETACHED_SESSION_FILE_TRACE_LOG="${BARRIER_VERIFY_ROOT}/debug/live-detached-session.file-trace.log"
    DETACHED_SESSION_SOURCE_WORKSPACE="${BARRIER_VERIFY_ROOT}/debug/live-detached-session-source"
    DETACHED_SESSION_SOURCE_SENTINEL_REL=".workcell-detached-session-sentinel-$$.log"
    DETACHED_SESSION_HOST_GIT_BIN="$(command -v git)"
    rm -rf "${DETACHED_SESSION_SOURCE_WORKSPACE}"
    env -i \
      PATH="${TRUSTED_HOST_PATH}" \
      HOME="${REAL_HOME}" \
      LC_ALL=C \
      LANG=C \
      "${DETACHED_SESSION_HOST_GIT_BIN}" clone --quiet --no-hardlinks "${ROOT_DIR}" "${DETACHED_SESSION_SOURCE_WORKSPACE}"
    DETACHED_SESSION_SOURCE_WORKSPACE="$(cd "${DETACHED_SESSION_SOURCE_WORKSPACE}" && pwd -P)"
    DETACHED_SESSION_SOURCE_SENTINEL_PATH="${DETACHED_SESSION_SOURCE_WORKSPACE}/${DETACHED_SESSION_SOURCE_SENTINEL_REL}"
    if [[ -e "${DETACHED_SESSION_SOURCE_SENTINEL_PATH}" ]]; then
      echo "Detached session source sentinel already exists in the source workspace: ${DETACHED_SESSION_SOURCE_SENTINEL_PATH}" >&2
      exit 1
    fi
    DETACHED_SESSION_WORKER_COMMAND="$(
      cat <<'EOF'
set -euo pipefail
WORKER_SENTINEL_REL="${1:?missing detached-session sentinel path}"
: >"/workspace/${WORKER_SENTINEL_REL}"
printf 'SESSION_READY\n'
printf 'SESSION_READY\n' >>"/workspace/${WORKER_SENTINEL_REL}"
test -r /workspace/AGENTS.md
test ! -w /workspace/AGENTS.md
test -r /workspace/.git/config
test ! -w /workspace/.git/config
test -d /opt/workcell/host-inputs
[[ -z "$(find /opt/workcell/host-inputs -mindepth 1 -print -quit)" ]]
printf 'SESSION_MASKS_OK\n'
printf 'SESSION_MASKS_OK\n' >>"/workspace/${WORKER_SENTINEL_REL}"
trap 'printf "SESSION_STOPPING\n"; exit 0' TERM INT
while IFS= read -r line; do
  printf 'SESSION_RECV:%s\n' "${line}"
  printf 'SESSION_RECV:%s\n' "${line}" >>"/workspace/${WORKER_SENTINEL_REL}"
done
EOF
    )"
    if ! "${ROOT_DIR}/scripts/workcell" \
      session start \
      --agent codex \
      --mode development \
      --workspace "${DETACHED_SESSION_SOURCE_WORKSPACE}" \
      --session-workspace isolated \
      --no-default-injection-policy \
      --colima-profile "${LIVE_DETACHED_PROFILE_NAME}" \
      --debug-log "${DETACHED_SESSION_DEBUG_LOG}" \
      --file-trace-log "${DETACHED_SESSION_FILE_TRACE_LOG}" \
      --allow-arbitrary-command \
      --ack-arbitrary-command \
      -- /bin/bash -lc "${DETACHED_SESSION_WORKER_COMMAND}" -- "${DETACHED_SESSION_SOURCE_SENTINEL_REL}" >"${DETACHED_SESSION_START_OUT}" 2>&1; then
      echo "Expected detached session start to succeed against the live runtime" >&2
      cat "${DETACHED_SESSION_START_OUT}" >&2
      exit 1
    fi
    DETACHED_SESSION_ID="$(sed -n 's/^session_id=//p' "${DETACHED_SESSION_START_OUT}" | head -n1)"
    DETACHED_SESSION_WORKSPACE="$(sed -n 's/^workspace=//p' "${DETACHED_SESSION_START_OUT}" | head -n1)"
    [[ -n "${DETACHED_SESSION_ID}" ]] || {
      echo "Detached session start did not report a session_id" >&2
      cat "${DETACHED_SESSION_START_OUT}" >&2
      exit 1
    }
    [[ -n "${DETACHED_SESSION_WORKSPACE}" ]] || {
      echo "Detached session start did not report a workspace path" >&2
      cat "${DETACHED_SESSION_START_OUT}" >&2
      exit 1
    }
    DETACHED_SESSION_MONITOR_PID="$(sed -n 's/^monitor_pid=//p' "${DETACHED_SESSION_START_OUT}" | head -n1)"
    [[ -n "${DETACHED_SESSION_MONITOR_PID}" ]] || {
      echo "Detached session start did not report a monitor_pid" >&2
      cat "${DETACHED_SESSION_START_OUT}" >&2
      exit 1
    }
    grep -q '^status=running$' "${DETACHED_SESSION_START_OUT}"
    grep -q '^live_status=running$' "${DETACHED_SESSION_START_OUT}"
    grep -q '^control_mode=detached$' "${DETACHED_SESSION_START_OUT}"
    grep -q "^workspace_origin=${DETACHED_SESSION_SOURCE_WORKSPACE}$" "${DETACHED_SESSION_START_OUT}"
    grep -q "^workspace_root=${DETACHED_SESSION_SOURCE_WORKSPACE}$" "${DETACHED_SESSION_START_OUT}"
    if ! kill -0 "${DETACHED_SESSION_MONITOR_PID}" >/dev/null 2>&1; then
      echo "Detached session reported a dead monitor_pid immediately after start: ${DETACHED_SESSION_MONITOR_PID}" >&2
      cat "${DETACHED_SESSION_START_OUT}" >&2
      exit 1
    fi
    case "${DETACHED_SESSION_WORKSPACE}" in
      "${DETACHED_SESSION_SOURCE_WORKSPACE}/.git/workcell-sessions/"*"/repo") ;;
      *)
        echo "Detached session workspace did not stay under the repo git-admin area: ${DETACHED_SESSION_WORKSPACE}" >&2
        exit 1
        ;;
    esac
    test -d "${DETACHED_SESSION_WORKSPACE}"
    if ! "${ROOT_DIR}/scripts/workcell" session show --id "${DETACHED_SESSION_ID}" >"${DETACHED_SESSION_SHOW_RUNNING_OUT}" 2>&1; then
      echo "Expected session show to succeed for a running detached session" >&2
      cat "${DETACHED_SESSION_SHOW_RUNNING_OUT}" >&2
      exit 1
    fi
    grep -q "\"session_id\": \"${DETACHED_SESSION_ID}\"" "${DETACHED_SESSION_SHOW_RUNNING_OUT}"
    grep -q '"status": "running"' "${DETACHED_SESSION_SHOW_RUNNING_OUT}"
    grep -q '"live_status": "running"' "${DETACHED_SESSION_SHOW_RUNNING_OUT}"
    grep -q "\"workspace_origin\": \"${DETACHED_SESSION_SOURCE_WORKSPACE}\"" "${DETACHED_SESSION_SHOW_RUNNING_OUT}"
    grep -q "\"workspace_root\": \"${DETACHED_SESSION_SOURCE_WORKSPACE}\"" "${DETACHED_SESSION_SHOW_RUNNING_OUT}"
    grep -q "\"worktree_path\": \"${DETACHED_SESSION_WORKSPACE}\"" "${DETACHED_SESSION_SHOW_RUNNING_OUT}"
    grep -q "\"monitor_pid\": \"${DETACHED_SESSION_MONITOR_PID}\"" "${DETACHED_SESSION_SHOW_RUNNING_OUT}"
    DETACHED_SESSION_AUDIT_DIR="$(jq -r '.session_audit_dir // empty' "${DETACHED_SESSION_SHOW_RUNNING_OUT}")"
    [[ -n "${DETACHED_SESSION_AUDIT_DIR}" ]] || {
      echo "Detached session show output did not report session_audit_dir" >&2
      cat "${DETACHED_SESSION_SHOW_RUNNING_OUT}" >&2
      exit 1
    }
    DETACHED_SESSION_MONITOR_STATE_FILE="${DETACHED_SESSION_AUDIT_DIR}/session-monitor.env"
    DETACHED_SESSION_MONITOR_COMMAND="$(ps -o command= -p "${DETACHED_SESSION_MONITOR_PID}" 2>/dev/null | head -n1 || true)"
    case "${DETACHED_SESSION_MONITOR_COMMAND}" in
      *"${ROOT_DIR}/scripts/workcell"*' session monitor --state-file '*"${DETACHED_SESSION_MONITOR_STATE_FILE}") ;;
      *)
        echo "Detached session monitor pid did not match the expected monitor command: ${DETACHED_SESSION_MONITOR_PID}" >&2
        printf '%s\n' "${DETACHED_SESSION_MONITOR_COMMAND}" >&2
        exit 1
        ;;
    esac
    DETACHED_SESSION_SENTINEL_PATH="${DETACHED_SESSION_WORKSPACE}/${DETACHED_SESSION_SOURCE_SENTINEL_REL}"
    for _ in $(seq 1 90); do
      if [[ -f "${DETACHED_SESSION_SENTINEL_PATH}" ]] &&
        grep -q '^SESSION_READY$' "${DETACHED_SESSION_SENTINEL_PATH}" &&
        grep -q '^SESSION_MASKS_OK$' "${DETACHED_SESSION_SENTINEL_PATH}"; then
        break
      fi
      sleep 2
    done
    if [[ ! -f "${DETACHED_SESSION_SENTINEL_PATH}" ]]; then
      echo "Detached session sentinel did not appear in the isolated workspace: ${DETACHED_SESSION_SENTINEL_PATH}" >&2
      cat "${DETACHED_SESSION_START_OUT}" >&2
      exit 1
    fi
    grep -q '^SESSION_READY$' "${DETACHED_SESSION_SENTINEL_PATH}"
    grep -q '^SESSION_MASKS_OK$' "${DETACHED_SESSION_SENTINEL_PATH}"
    DETACHED_ATTACH_STATUS=0
    (
      VERIFY_INVARIANTS_EXPECTED_FAILURE=1
      run_typescript_probe_with_timeout 10 \
        "${DETACHED_SESSION_ATTACH_TYPESCRIPT}" \
        "${ROOT_DIR}/scripts/workcell" \
        session attach \
        --id "${DETACHED_SESSION_ID}" \
        --no-stdin
    ) &
    DETACHED_ATTACH_PID=$!
    sleep 1
    if ! "${ROOT_DIR}/scripts/workcell" session send --id "${DETACHED_SESSION_ID}" --message alpha >"${DETACHED_SESSION_SEND_ALPHA_OUT}" 2>&1; then
      echo "Expected detached session send(alpha) to succeed" >&2
      cat "${DETACHED_SESSION_SEND_ALPHA_OUT}" >&2
      exit 1
    fi
    if ! "${ROOT_DIR}/scripts/workcell" session send --id "${DETACHED_SESSION_ID}" --message beta >"${DETACHED_SESSION_SEND_BETA_OUT}" 2>&1; then
      echo "Expected detached session send(beta) to succeed" >&2
      cat "${DETACHED_SESSION_SEND_BETA_OUT}" >&2
      exit 1
    fi
    grep -q "^session_id=${DETACHED_SESSION_ID}$" "${DETACHED_SESSION_SEND_ALPHA_OUT}"
    grep -q '^sent_bytes=6$' "${DETACHED_SESSION_SEND_ALPHA_OUT}"
    grep -q '^sent_bytes=5$' "${DETACHED_SESSION_SEND_BETA_OUT}"
    if wait "${DETACHED_ATTACH_PID}"; then
      DETACHED_ATTACH_STATUS=0
    else
      DETACHED_ATTACH_STATUS=$?
    fi
    if [[ "${DETACHED_ATTACH_STATUS}" != "0" ]] && [[ "${DETACHED_ATTACH_STATUS}" != "124" ]]; then
      echo "Expected detached session attach to stream live output or timeout cleanly" >&2
      cat "${DETACHED_SESSION_ATTACH_TYPESCRIPT}" >&2 || true
      exit 1
    fi
    grep -q 'SESSION_RECV:alpha' "${DETACHED_SESSION_ATTACH_TYPESCRIPT}"
    grep -q 'SESSION_RECV:beta' "${DETACHED_SESSION_ATTACH_TYPESCRIPT}"
    DETACHED_SESSION_GIT_DIR="$(git -C "${DETACHED_SESSION_WORKSPACE}" rev-parse --absolute-git-dir)"
    SOURCE_GIT_DIR="$(git -C "${DETACHED_SESSION_SOURCE_WORKSPACE}" rev-parse --absolute-git-dir)"
    if [[ "${DETACHED_SESSION_GIT_DIR}" != "${DETACHED_SESSION_WORKSPACE}/.git" ]]; then
      echo "Detached session clone did not keep a self-contained git dir: ${DETACHED_SESSION_GIT_DIR}" >&2
      exit 1
    fi
    if [[ "${DETACHED_SESSION_GIT_DIR}" == "${SOURCE_GIT_DIR}" ]]; then
      echo "Detached session clone unexpectedly reused the source workspace git admin directory" >&2
      exit 1
    fi
    for _ in $(seq 1 90); do
      if [[ -f "${DETACHED_SESSION_SENTINEL_PATH}" ]] &&
        grep -q '^SESSION_RECV:alpha$' "${DETACHED_SESSION_SENTINEL_PATH}" &&
        grep -q '^SESSION_RECV:beta$' "${DETACHED_SESSION_SENTINEL_PATH}"; then
        break
      fi
      sleep 2
    done
    grep -q '^SESSION_RECV:alpha$' "${DETACHED_SESSION_SENTINEL_PATH}"
    grep -q '^SESSION_RECV:beta$' "${DETACHED_SESSION_SENTINEL_PATH}"
    if [[ -e "${DETACHED_SESSION_SOURCE_SENTINEL_PATH}" ]]; then
      echo "Detached session wrote into the source workspace instead of the isolated clone: ${DETACHED_SESSION_SOURCE_SENTINEL_PATH}" >&2
      exit 1
    fi
    if ! "${ROOT_DIR}/scripts/workcell" session diff --id "${DETACHED_SESSION_ID}" >"${DETACHED_SESSION_DIFF_OUT}" 2>&1; then
      echo "Expected detached session diff to succeed for the isolated workspace clone" >&2
      cat "${DETACHED_SESSION_DIFF_OUT}" >&2
      exit 1
    fi
    grep -q "^session_id=${DETACHED_SESSION_ID}$" "${DETACHED_SESSION_DIFF_OUT}"
    grep -q "^?? ${DETACHED_SESSION_SOURCE_SENTINEL_REL}$" "${DETACHED_SESSION_DIFF_OUT}"
    if ! "${ROOT_DIR}/scripts/workcell" session stop --id "${DETACHED_SESSION_ID}" >"${DETACHED_SESSION_STOP_OUT}" 2>&1; then
      echo "Expected detached session stop to succeed" >&2
      cat "${DETACHED_SESSION_STOP_OUT}" >&2
      exit 1
    fi
    grep -q "^session_id=${DETACHED_SESSION_ID}$" "${DETACHED_SESSION_STOP_OUT}"
    grep -q '^stop_requested=1$' "${DETACHED_SESSION_STOP_OUT}"
    for _ in 1 2 3 4 5 6 7 8 9 10; do
      if "${ROOT_DIR}/scripts/workcell" session show --id "${DETACHED_SESSION_ID}" >"${DETACHED_SESSION_SHOW_STOPPED_OUT}" 2>&1 &&
        grep -q '"status": "exited"' "${DETACHED_SESSION_SHOW_STOPPED_OUT}" &&
        grep -q '"live_status": "stopped"' "${DETACHED_SESSION_SHOW_STOPPED_OUT}"; then
        break
      fi
      sleep 1
    done
    grep -q "\"session_id\": \"${DETACHED_SESSION_ID}\"" "${DETACHED_SESSION_SHOW_STOPPED_OUT}"
    grep -q '"status": "exited"' "${DETACHED_SESSION_SHOW_STOPPED_OUT}"
    grep -q '"live_status": "stopped"' "${DETACHED_SESSION_SHOW_STOPPED_OUT}"
    grep -q '"current_assurance": "managed-mutable"' "${DETACHED_SESSION_SHOW_STOPPED_OUT}"
    grep -q '"final_assurance": "managed-mutable"' "${DETACHED_SESSION_SHOW_STOPPED_OUT}"
    DETACHED_SESSION_MONITOR_COMMAND="$(ps -o command= -p "${DETACHED_SESSION_MONITOR_PID}" 2>/dev/null | head -n1 || true)"
    case "${DETACHED_SESSION_MONITOR_COMMAND}" in
      *"${ROOT_DIR}/scripts/workcell"*' session monitor --state-file '*"${DETACHED_SESSION_MONITOR_STATE_FILE}")
        echo "Detached session monitor remained alive after session finalization: ${DETACHED_SESSION_MONITOR_PID}" >&2
        cat "${DETACHED_SESSION_SHOW_STOPPED_OUT}" >&2
        exit 1
        ;;
    esac
    test ! -e "${DETACHED_SESSION_AUDIT_DIR}"
    test ! -e "${DETACHED_SESSION_MONITOR_STATE_FILE}"
    if ! "${ROOT_DIR}/scripts/workcell" session timeline --id "${DETACHED_SESSION_ID}" >"${DETACHED_SESSION_TIMELINE_OUT}" 2>&1; then
      echo "Expected detached session timeline to succeed" >&2
      cat "${DETACHED_SESSION_TIMELINE_OUT}" >&2
      exit 1
    fi
    grep -q "event=launch session_id=${DETACHED_SESSION_ID}" "${DETACHED_SESSION_TIMELINE_OUT}"
    grep -q "event=attach-attempt session_id=${DETACHED_SESSION_ID}" "${DETACHED_SESSION_TIMELINE_OUT}"
    test "$(grep -c "event=command session_id=${DETACHED_SESSION_ID}" "${DETACHED_SESSION_TIMELINE_OUT}")" = "2"
    grep -q "event=stop-request session_id=${DETACHED_SESSION_ID}" "${DETACHED_SESSION_TIMELINE_OUT}"
    grep -q "event=exit session_id=${DETACHED_SESSION_ID}" "${DETACHED_SESSION_TIMELINE_OUT}"
    if ! "${ROOT_DIR}/scripts/workcell" session logs --id "${DETACHED_SESSION_ID}" --kind audit >"${DETACHED_SESSION_LOGS_AUDIT_OUT}" 2>&1; then
      echo "Expected detached session audit log retrieval to succeed" >&2
      cat "${DETACHED_SESSION_LOGS_AUDIT_OUT}" >&2
      exit 1
    fi
    grep -q "event=launch session_id=${DETACHED_SESSION_ID}" "${DETACHED_SESSION_LOGS_AUDIT_OUT}"
    grep -q "event=exit session_id=${DETACHED_SESSION_ID}" "${DETACHED_SESSION_LOGS_AUDIT_OUT}"
    if ! "${ROOT_DIR}/scripts/workcell" session logs --id "${DETACHED_SESSION_ID}" --kind debug >"${DETACHED_SESSION_LOGS_DEBUG_OUT}" 2>&1; then
      echo "Expected detached session debug log retrieval to succeed" >&2
      cat "${DETACHED_SESSION_LOGS_DEBUG_OUT}" >&2
      exit 1
    fi
    grep -q "profile=${LIVE_DETACHED_PROFILE_NAME}" "${DETACHED_SESSION_LOGS_DEBUG_OUT}"
    if ! "${ROOT_DIR}/scripts/workcell" session logs --id "${DETACHED_SESSION_ID}" --kind file-trace >"${DETACHED_SESSION_LOGS_FILE_TRACE_OUT}" 2>&1; then
      echo "Expected detached session file-trace retrieval to succeed" >&2
      cat "${DETACHED_SESSION_LOGS_FILE_TRACE_OUT}" >&2
      exit 1
    fi
    if ! grep -Eq 'event=watch-start|event=host-collect-missing' "${DETACHED_SESSION_LOGS_FILE_TRACE_OUT}"; then
      echo "Expected detached session file-trace retrieval to include watcher activity or an explicit host collection fallback" >&2
      cat "${DETACHED_SESSION_LOGS_FILE_TRACE_OUT}" >&2
      exit 1
    fi
    if "${ROOT_DIR}/scripts/workcell" session logs --id "${DETACHED_SESSION_ID}" --kind transcript >"${DETACHED_SESSION_LOGS_TRANSCRIPT_OUT}" 2>&1; then
      echo "Expected detached session transcript retrieval to fail when transcript capture is not enabled" >&2
      exit 1
    fi
    grep -q "No transcript log is recorded for session ${DETACHED_SESSION_ID}" "${DETACHED_SESSION_LOGS_TRANSCRIPT_OUT}"
    if ! "${ROOT_DIR}/scripts/workcell" session list --json --workspace "${DETACHED_SESSION_WORKSPACE}" --colima-profile "${LIVE_DETACHED_PROFILE_NAME}" >"${DETACHED_SESSION_LIST_OUT}" 2>&1; then
      echo "Expected detached session list --json to include the isolated workspace session" >&2
      cat "${DETACHED_SESSION_LIST_OUT}" >&2
      exit 1
    fi
    grep -q "\"session_id\": \"${DETACHED_SESSION_ID}\"" "${DETACHED_SESSION_LIST_OUT}"
    grep -q "\"workspace\": \"${DETACHED_SESSION_WORKSPACE}\"" "${DETACHED_SESSION_LIST_OUT}"
    grep -q '"status": "exited"' "${DETACHED_SESSION_LIST_OUT}"
    cleanup_detached_session_runtime
    DETACHED_SESSION_ID=""
    DETACHED_SESSION_WORKSPACE=""
    DETACHED_SESSION_SOURCE_SENTINEL_PATH=""
    AUDIT_LOG="${REAL_HOME}/.colima/${LIVE_DEBUG_PROFILE_NAME}/workcell.audit.log"
    PACKAGE_MUTATION_AUDIT_COMMAND="$(
      cat <<'EOF'
for attempt in 1 2 3; do
  # Remove a package baked into the runtime image so the mutation audit does
  # not depend on live Debian snapshot availability.
  if sudo -n /usr/local/libexec/workcell/apt-helper.sh apt-get remove -y unzip >/dev/null; then
    exit 0
  fi
  if [[ "${attempt}" -eq 3 ]]; then
    exit 1
  fi
  sleep "$((attempt * 5))"
done
EOF
    )"
    if ! "${ROOT_DIR}/scripts/workcell" \
      --agent codex \
      --mode build \
      --workspace "${ROOT_DIR}" \
      --injection-policy "${AUTH_STATUS_ROOT}/policy.toml" \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
      --allow-arbitrary-command \
      --ack-arbitrary-command \
      -- /bin/bash -lc "${PACKAGE_MUTATION_AUDIT_COMMAND}"; then
      echo "Expected package-mutation audit verification run to succeed" >&2
      exit 1
    fi
    cp "${AUDIT_LOG}" "${AUDIT_SESSION_LOG}"
    grep -q 'event=launch' "${AUDIT_SESSION_LOG}"
    grep -q 'record_digest=' "${AUDIT_SESSION_LOG}"
    grep -q 'execution_path=lower-assurance-debug-command' "${AUDIT_SESSION_LOG}"
    grep -q 'provider_native_sandbox_configured=disabled' "${AUDIT_SESSION_LOG}"
    grep -q 'provider_native_sandbox_effective=disabled' "${AUDIT_SESSION_LOG}"
    grep -q 'provider_native_sandbox_reason=workcell-pinned-off-due-to-bwrap-userns-incompatibility' "${AUDIT_SESSION_LOG}"
    grep -q 'event=assurance-change' "${AUDIT_SESSION_LOG}"
    grep -q 'reason=package-mutation' "${AUDIT_SESSION_LOG}"
    grep -q 'session_assurance_final=lower-assurance-package-mutation' "${AUDIT_SESSION_LOG}"
    grep -q 'event=exit' "${AUDIT_SESSION_LOG}"
    grep -q 'package_mutation_downgraded=1' "${AUDIT_SESSION_LOG}"
    PACKAGE_MUTATION_FAILURE_COMMAND="$(
      cat <<'EOF'
if sudo -n /usr/local/libexec/workcell/apt-helper.sh apt-get install -y workcell-package-that-must-not-exist-verify-fixture \
  >/tmp/workcell-package-mutation-failure.out 2>/tmp/workcell-package-mutation-failure.err; then
  echo "Expected apt-helper to propagate package-manager failure status" >&2
  cat /tmp/workcell-package-mutation-failure.out >&2 || true
  cat /tmp/workcell-package-mutation-failure.err >&2 || true
  exit 1
fi
codex --version >/tmp/workcell-package-mutation-post-failure.out 2>&1
grep -q "session previously ran package-manager mutations as root" /tmp/workcell-package-mutation-post-failure.out
EOF
    )"
    if ! "${ROOT_DIR}/scripts/workcell" \
      --agent codex \
      --mode build \
      --workspace "${ROOT_DIR}" \
      --injection-policy "${AUTH_STATUS_ROOT}/policy.toml" \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
      --allow-arbitrary-command \
      --ack-arbitrary-command \
      -- /bin/bash -lc "${PACKAGE_MUTATION_FAILURE_COMMAND}"; then
      echo "Expected package-mutation failure propagation verification run to succeed" >&2
      exit 1
    fi
    APT_BROKER_FIXTURE_ROOT="${BARRIER_VERIFY_ROOT}/workcell-apt-broker-fixture"
    APT_BROKER_HELPER="${APT_BROKER_FIXTURE_ROOT}/slow-apt-helper.sh"
    APT_BROKER_RUNTIME_ROOT="${APT_BROKER_FIXTURE_ROOT}/runtime"
    APT_BROKER_STDOUT="${APT_BROKER_FIXTURE_ROOT}/sudo-wrapper.out"
    APT_BROKER_STDERR="${APT_BROKER_FIXTURE_ROOT}/sudo-wrapper.err"
    rm -rf "${APT_BROKER_FIXTURE_ROOT}"
    mkdir -p "${APT_BROKER_FIXTURE_ROOT}"
    cat >"${APT_BROKER_HELPER}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
sleep 11
printf 'slow-apt-helper-ok\n'
EOF
    chmod +x "${APT_BROKER_HELPER}"
    WORKCELL_APT_BROKER_ROOT="${APT_BROKER_RUNTIME_ROOT}" \
      WORKCELL_APT_HELPER="${APT_BROKER_HELPER}" \
      WORKCELL_APT_BROKER_SLEEP_SECONDS=0.05 \
      /bin/bash "${ROOT_DIR}/runtime/container/apt-broker.sh" >/dev/null 2>&1 &
    APT_BROKER_PID=$!
    for _ in $(seq 1 100); do
      if [[ -f "${APT_BROKER_RUNTIME_ROOT}/pid" ]]; then
        break
      fi
      sleep 0.1
    done
    if [[ ! -f "${APT_BROKER_RUNTIME_ROOT}/pid" ]]; then
      echo "Expected apt broker fixture to publish its pid file" >&2
      kill "${APT_BROKER_PID}" >/dev/null 2>&1 || true
      wait "${APT_BROKER_PID}" >/dev/null 2>&1 || true
      exit 1
    fi
    if ! WORKCELL_APT_BROKER_ROOT="${APT_BROKER_RUNTIME_ROOT}" \
      WORKCELL_APT_BROKER_WAIT_INTERVAL_SECONDS=0.05 \
      /bin/bash "${ROOT_DIR}/runtime/container/bin/sudo-wrapper.sh" \
      -n /usr/local/libexec/workcell/apt-helper.sh apt-get update \
      >"${APT_BROKER_STDOUT}" 2>"${APT_BROKER_STDERR}"; then
      echo "Expected sudo-wrapper to wait for a slow apt broker request by default" >&2
      cat "${APT_BROKER_STDOUT}" >&2 || true
      cat "${APT_BROKER_STDERR}" >&2 || true
      kill "${APT_BROKER_PID}" >/dev/null 2>&1 || true
      wait "${APT_BROKER_PID}" >/dev/null 2>&1 || true
      exit 1
    fi
    grep -q 'slow-apt-helper-ok' "${APT_BROKER_STDOUT}"
    if grep -q 'Workcell apt broker timed out.' "${APT_BROKER_STDERR}"; then
      echo "Expected sudo-wrapper default apt broker waits to avoid timing out slow requests" >&2
      cat "${APT_BROKER_STDERR}" >&2
      kill "${APT_BROKER_PID}" >/dev/null 2>&1 || true
      wait "${APT_BROKER_PID}" >/dev/null 2>&1 || true
      exit 1
    fi
    kill "${APT_BROKER_PID}" >/dev/null 2>&1 || true
    wait "${APT_BROKER_PID}" >/dev/null 2>&1 || true
    if ! "${ROOT_DIR}/scripts/workcell" \
      --agent codex \
      --mode build \
      --workspace "${ROOT_DIR}" \
      --injection-policy "${AUTH_STATUS_ROOT}/policy.toml" \
      --colima-profile "${LIVE_DEBUG_PROFILE_NAME}" \
      --allow-arbitrary-command \
      --ack-arbitrary-command \
      -- /bin/bash -lc 'test -f /opt/workcell/host-injections/manifest.json && grep -q "Repository Working Agreement" /workspace/AGENTS.md && test ! -d /workspace/AGENTS.md'; then
      echo "Expected live launcher run to stage an injection manifest and mount the tracked workspace AGENTS.md snapshot as a file" >&2
      exit 1
    fi
    delete_verify_colima_profile "${LIVE_DEBUG_PROFILE_NAME}"
    delete_verify_colima_profile "${LIVE_DETACHED_PROFILE_NAME}"
    AUDIT_RESTORE_PROFILE_NAME="workcell-audit-restore-$$"
    AUDIT_RESTORE_DIR="${REAL_HOME}/.colima/${AUDIT_RESTORE_PROFILE_NAME}"
    AUDIT_RESTORE_LIMA_DIR="${REAL_HOME}/.colima/_lima/colima-${AUDIT_RESTORE_PROFILE_NAME}"
    AUDIT_RESTORE_LOG="${AUDIT_RESTORE_DIR}/workcell.audit.log"
    mkdir -p "${AUDIT_RESTORE_DIR}" "${AUDIT_RESTORE_LIMA_DIR}"
    printf '%s\n' "${NONGIT_WORKSPACE}" >"${AUDIT_RESTORE_DIR}/workcell.managed"
    cat >"${AUDIT_RESTORE_LIMA_DIR}/lima.yaml" <<'EOF'
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
    delete_verify_colima_profile "${AUDIT_RESTORE_PROFILE_NAME}"

    STRICT_REFRESH_PROFILE_NAME="workcell-strict-refresh-$$"
    delete_verify_colima_profile "${STRICT_REFRESH_PROFILE_NAME}"
    STRICT_REFRESH_DIR="${REAL_HOME}/.colima/${STRICT_REFRESH_PROFILE_NAME}"
    mkdir -p "${STRICT_REFRESH_DIR}"
    printf '%s\n' "${NONGIT_WORKSPACE}" >"${STRICT_REFRESH_DIR}/workcell.managed"
    cat >"${STRICT_REFRESH_DIR}/colima.yaml" <<'EOF'
cpu: 4
memory: 7
disk: 60
runtime: docker
vmType: vz
mountType: virtiofs
EOF
    cat >"${STRICT_REFRESH_DIR}/workcell.image-ready" <<'EOF'
image_tag=workcell:local
image_id=sha256:strict-refresh-fixture
source_date_epoch=0
EOF
    printf 'timestamp=test event=launch workspace=%q\n' "${NONGIT_WORKSPACE}" >"${STRICT_REFRESH_DIR}/workcell.audit.log"
    VERIFY_INVARIANTS_EXPECTED_FAILURE=1
    set +e
    "${ROOT_DIR}/scripts/workcell" \
      --test-fail-after-profile-refresh \
      --agent codex \
      --allow-nongit-workspace \
      --workspace "${NONGIT_WORKSPACE}" \
      --colima-profile "${STRICT_REFRESH_PROFILE_NAME}" \
      --agent-arg --version >/tmp/workcell-strict-refresh-preflight.out 2>&1
    strict_refresh_status=$?
    set -e
    VERIFY_INVARIANTS_EXPECTED_FAILURE=0
    if [[ "${strict_refresh_status}" -ne 88 ]]; then
      echo "Expected strict-mode refresh launch without --prepare to reach the post-refresh test hook, got ${strict_refresh_status}" >&2
      cat /tmp/workcell-strict-refresh-preflight.out >&2
      exit 1
    fi
    grep -q "Refreshing managed Colima profile ${STRICT_REFRESH_PROFILE_NAME} to apply the requested reviewed VM resources." /tmp/workcell-strict-refresh-preflight.out
    grep -q "No prepared runtime image is recorded for strict mode on profile ${STRICT_REFRESH_PROFILE_NAME}." /tmp/workcell-strict-refresh-preflight.out
    grep -q "Workcell will seed or refresh the prepared runtime image automatically before launching codex in strict mode." /tmp/workcell-strict-refresh-preflight.out
    assert_output_did_not_start_colima \
      /tmp/workcell-strict-refresh-preflight.out \
      "Strict-mode refresh launch should still stop at the post-refresh hook before Colima startup"
    grep -q 'Workcell test hook: forcing failure after managed profile refresh.' /tmp/workcell-strict-refresh-preflight.out
    VERIFY_INVARIANTS_EXPECTED_FAILURE=1
    set +e
    "${ROOT_DIR}/scripts/workcell" \
      --test-fail-after-profile-refresh \
      --agent codex \
      --prepare \
      --allow-nongit-workspace \
      --workspace "${NONGIT_WORKSPACE}" \
      --colima-profile "${STRICT_REFRESH_PROFILE_NAME}" \
      --agent-arg --version >/tmp/workcell-strict-refresh-prepare.out 2>&1
    strict_refresh_prepare_status=$?
    set -e
    VERIFY_INVARIANTS_EXPECTED_FAILURE=0
    if [[ "${strict_refresh_prepare_status}" -ne 2 ]]; then
      echo "Expected follow-up strict prepare to stop on unmanaged-profile safety preflight, got ${strict_refresh_prepare_status}" >&2
      cat /tmp/workcell-strict-refresh-prepare.out >&2
      exit 1
    fi
    grep -q 'Refusing to reuse unmanaged Colima profile' /tmp/workcell-strict-refresh-prepare.out
    grep -q -- '--repair-profile' /tmp/workcell-strict-refresh-prepare.out
    delete_verify_colima_profile "${STRICT_REFRESH_PROFILE_NAME}"
  fi
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
rm -rf \
  "${REAL_HOME}/.colima/${UNMANAGED_PROFILE_NAME}" \
  "${REAL_HOME}/.colima/_lima/colima-${UNMANAGED_PROFILE_NAME}" \
  "${REAL_HOME}/.colima/_lima/_disks/colima-${UNMANAGED_PROFILE_NAME}"

if [[ "$(uname -s)" == "Darwin" ]] &&
  host_tool_exists /opt/homebrew/bin/colima /usr/local/bin/colima &&
  host_tool_exists /opt/homebrew/bin/docker /usr/local/bin/docker /Applications/Docker.app/Contents/Resources/bin/docker; then
  GOFLAGS_PROFILE_NAME="workcell-goflags-verify-$$"
  COLIMA_PROFILE_FIXTURE="${REAL_HOME}/.colima/${GOFLAGS_PROFILE_NAME}"
  mkdir -p "${COLIMA_PROFILE_FIXTURE}"
  printf '%s\n' "${NONGIT_WORKSPACE}" >"${COLIMA_PROFILE_FIXTURE}/workcell.managed"
  printf 'image_tag=workcell:local\nimage_id=sha256:test\nsource_date_epoch=0\n' >"${COLIMA_PROFILE_FIXTURE}/workcell.image-ready"
  cat >"${COLIMA_PROFILE_FIXTURE}/colima.yaml" <<'EOF'
vmType: qemu
mountType: virtiofs
runtime: docker
EOF
  GOFLAGS="-modfile=${BARRIER_VERIFY_ROOT}/missing-go.mod" \
    "${ROOT_DIR}/scripts/workcell" \
    --agent codex \
    --allow-nongit-workspace \
    --workspace "${NONGIT_WORKSPACE}" \
    --colima-profile "${GOFLAGS_PROFILE_NAME}" >/tmp/workcell-goflags.out 2>&1 || true
  if grep -q 'missing-go.mod' /tmp/workcell-goflags.out; then
    echo "scripts/workcell honored hostile GOFLAGS before validating managed Colima profiles" >&2
    exit 1
  fi
  if ! grep -Eiq "unexpected configured Colima mounts|unexpected Colima vmType" /tmp/workcell-goflags.out; then
    echo "Expected managed Colima profile validation failure output for hostile GOFLAGS fixture" >&2
    cat /tmp/workcell-goflags.out >&2
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
if "${ROOT_DIR}/scripts/workcell" --agent codex --workspace "${WORKTREE_LINKED}" --dry-run >/tmp/workcell-linked-worktree.out 2>&1; then
  echo "Expected linked git worktree with external admin state to be rejected" >&2
  exit 1
fi
grep -q 'This workspace is a linked worktree' /tmp/workcell-linked-worktree.out
grep -q 'create a standard clone at the same location instead' /tmp/workcell-linked-worktree.out
grep -q 'pass --mode breakglass --ack-breakglass to proceed with a linked worktree' /tmp/workcell-linked-worktree.out

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

if ! WORKCELL_E2E_CODEX_AUTH_JSON='{"token":"codex-smoke"}' \
  WORKCELL_E2E_GITHUB_HOSTS_YML=$'github.com:\n  user: smoke\n' \
  "${ROOT_DIR}/scripts/provider-e2e.sh" \
  --agent codex \
  --workspace "${ROOT_DIR}" \
  --dry-run >/tmp/workcell-provider-e2e-codex.out 2>&1; then
  echo "Expected provider-e2e codex dry-run to succeed with generated env credentials" >&2
  cat /tmp/workcell-provider-e2e-codex.out >&2
  exit 1
fi
grep -q '^provider_e2e_agent=codex$' /tmp/workcell-provider-e2e-codex.out
grep -q '^provider_e2e_injection_source=generated-env$' /tmp/workcell-provider-e2e-codex.out
grep -q '^provider_e2e_steps=auth-status,prepare-only,development-shell,live-probe$' /tmp/workcell-provider-e2e-codex.out
grep -q 'codex_auth' /tmp/workcell-provider-e2e-codex.out
if grep -q 'github_hosts' /tmp/workcell-provider-e2e-codex.out; then
  echo "Expected provider-e2e codex dry-run to omit unrelated shared GitHub credentials" >&2
  exit 1
fi
grep -q 'provider_e2e_auth_status_cmd=.*--auth-status' /tmp/workcell-provider-e2e-codex.out
grep -q 'provider_e2e_prepare_only_cmd=.*--prepare-only' /tmp/workcell-provider-e2e-codex.out
grep -q 'provider_e2e_shell_probe_cmd=.*--mode development' /tmp/workcell-provider-e2e-codex.out
grep -q 'provider_e2e_shell_probe_cmd=.*-- bash -lc' /tmp/workcell-provider-e2e-codex.out
grep -q 'provider_e2e_shell_probe_cmd=.*WORKCELL_PROVIDER_E2E_SHELL_OK' /tmp/workcell-provider-e2e-codex.out
grep -q 'provider_e2e_probe_cmd=.*--agent-arg exec' /tmp/workcell-provider-e2e-codex.out
grep -q 'provider_e2e_probe_cmd=.*--agent-arg --json' /tmp/workcell-provider-e2e-codex.out
grep -q 'provider_e2e_probe_cmd=.*WORKCELL_PROVIDER_E2E_OK' /tmp/workcell-provider-e2e-codex.out

if ! WORKCELL_E2E_CLAUDE_API_KEY='smoke-api-key' \
  "${ROOT_DIR}/scripts/provider-e2e.sh" \
  --agent claude \
  --workspace "${ROOT_DIR}" \
  --dry-run >/tmp/workcell-provider-e2e-claude.out 2>&1; then
  echo "Expected provider-e2e claude dry-run to succeed with generated env credentials" >&2
  cat /tmp/workcell-provider-e2e-claude.out >&2
  exit 1
fi
grep -q '^provider_e2e_agent=claude$' /tmp/workcell-provider-e2e-claude.out
grep -q '^provider_e2e_injection_source=generated-env$' /tmp/workcell-provider-e2e-claude.out
grep -q 'claude_api_key' /tmp/workcell-provider-e2e-claude.out
grep -q 'provider_e2e_shell_probe_cmd=.*--mode development' /tmp/workcell-provider-e2e-claude.out
grep -q 'provider_e2e_shell_probe_cmd=.*-- bash -lc' /tmp/workcell-provider-e2e-claude.out
grep -q 'provider_e2e_shell_probe_cmd=.*WORKCELL_PROVIDER_E2E_SHELL_OK' /tmp/workcell-provider-e2e-claude.out
grep -q 'provider_e2e_probe_cmd=.*--agent-arg -p' /tmp/workcell-provider-e2e-claude.out
grep -q 'provider_e2e_probe_cmd=.*--agent-arg json' /tmp/workcell-provider-e2e-claude.out
grep -q 'provider_e2e_probe_cmd=.*--agent-arg --no-session-persistence' /tmp/workcell-provider-e2e-claude.out
grep -q 'provider_e2e_probe_cmd=.*WORKCELL_PROVIDER_E2E_OK' /tmp/workcell-provider-e2e-claude.out
if grep -q 'github_hosts' /tmp/workcell-provider-e2e-claude.out; then
  echo "Expected provider-e2e claude dry-run to omit unrelated shared GitHub credentials" >&2
  exit 1
fi

if ! WORKCELL_E2E_GEMINI_ENV=$'GOOGLE_GENAI_USE_VERTEXAI=true\nGOOGLE_CLOUD_PROJECT=smoke-project\nGOOGLE_CLOUD_LOCATION=us-central1\n' \
  WORKCELL_E2E_GCLOUD_ADC_JSON='{"type":"authorized_user","client_id":"smoke-client","client_secret":"smoke-secret","refresh_token":"smoke-refresh"}' \
  "${ROOT_DIR}/scripts/provider-e2e.sh" \
  --agent gemini \
  --workspace "${ROOT_DIR}" \
  --dry-run >/tmp/workcell-provider-e2e-gemini.out 2>&1; then
  echo "Expected provider-e2e gemini dry-run to succeed with generated env credentials" >&2
  cat /tmp/workcell-provider-e2e-gemini.out >&2
  exit 1
fi
grep -q '^provider_e2e_agent=gemini$' /tmp/workcell-provider-e2e-gemini.out
grep -q '^provider_e2e_injection_source=generated-env$' /tmp/workcell-provider-e2e-gemini.out
grep -q 'gemini_env' /tmp/workcell-provider-e2e-gemini.out
grep -q 'gcloud_adc' /tmp/workcell-provider-e2e-gemini.out
grep -q 'provider_e2e_shell_probe_cmd=.*--mode development' /tmp/workcell-provider-e2e-gemini.out
grep -q 'provider_e2e_shell_probe_cmd=.*-- bash -lc' /tmp/workcell-provider-e2e-gemini.out
grep -q 'provider_e2e_shell_probe_cmd=.*WORKCELL_PROVIDER_E2E_SHELL_OK' /tmp/workcell-provider-e2e-gemini.out
grep -q 'provider_e2e_probe_cmd=.*--agent-arg -p' /tmp/workcell-provider-e2e-gemini.out
grep -q 'provider_e2e_probe_cmd=.*--agent-arg json' /tmp/workcell-provider-e2e-gemini.out
grep -q 'provider_e2e_probe_cmd=.*WORKCELL_PROVIDER_E2E_OK' /tmp/workcell-provider-e2e-gemini.out

FAKE_PROVIDER_E2E_WORKCELL_OK="${BARRIER_VERIFY_ROOT}/provider-e2e-fake-workcell-ok.sh"
cat >"${FAKE_PROVIDER_E2E_WORKCELL_OK}" <<'EOF_FAKE_PROVIDER_E2E_OK'
#!/bin/bash
set -euo pipefail

for arg in "$@"; do
  if [[ "${arg}" == "--auth-status" ]]; then
    cat <<'STATUS'
credential_keys=codex_auth
provider_auth_mode=codex_auth
provider_auth_modes=codex_auth
shared_auth_modes=none
github_auth_present=0
ssh_injected=0
ssh_config_assurance=off
secret_copy_targets=none
STATUS
    exit 0
  fi
done

if [[ "${1:-}" == "--prepare-only" ]]; then
  exit 0
fi

if [[ " $* " == *" --mode development "* ]]; then
  case " $* " in
    *" -- bash -lc "* ) ;;
    * )
      echo "missing codex development-shell args: $*" >&2
      exit 96
      ;;
  esac
  case " $* " in
    *" WORKCELL_PROVIDER_E2E_SHELL_OK "* ) ;;
    * )
      echo "missing codex development-shell token: $*" >&2
      exit 95
      ;;
  esac
  printf 'WORKCELL_PROVIDER_E2E_SHELL_OK\n'
  exit 0
fi

case " $* " in
  *" --agent-arg exec "* ) ;;
  * )
    echo "missing codex exec probe args: $*" >&2
    exit 98
    ;;
esac
case " $* " in
  *" --agent-arg --json "* ) ;;
  * )
    echo "missing codex json probe args: $*" >&2
    exit 97
    ;;
esac

printf '{"type":"item.completed","item":{"type":"agent_message","text":"WORKCELL_PROVIDER_E2E_OK"}}\n'
EOF_FAKE_PROVIDER_E2E_OK
chmod +x "${FAKE_PROVIDER_E2E_WORKCELL_OK}"

if ! WORKCELL_PROVIDER_E2E_WORKCELL_SCRIPT="${FAKE_PROVIDER_E2E_WORKCELL_OK}" \
  WORKCELL_E2E_CODEX_AUTH_JSON='{"token":"codex-smoke"}' \
  "${ROOT_DIR}/scripts/provider-e2e.sh" \
  --agent codex \
  --workspace "${ROOT_DIR}" \
  --require-injection >/tmp/workcell-provider-e2e-live-probe.out 2>&1; then
  echo "Expected provider-e2e live probe to succeed against the fake Workcell shim" >&2
  cat /tmp/workcell-provider-e2e-live-probe.out >&2
  exit 1
fi
grep -q '\[provider-e2e\] auth-status (codex)' /tmp/workcell-provider-e2e-live-probe.out
grep -q '\[provider-e2e\] prepare-only (codex)' /tmp/workcell-provider-e2e-live-probe.out
grep -q '\[provider-e2e\] development-shell (codex)' /tmp/workcell-provider-e2e-live-probe.out
grep -q '^WORKCELL_PROVIDER_E2E_SHELL_OK$' /tmp/workcell-provider-e2e-live-probe.out
grep -q '\[provider-e2e\] live-probe (codex)' /tmp/workcell-provider-e2e-live-probe.out
grep -q '"text":"WORKCELL_PROVIDER_E2E_OK"' /tmp/workcell-provider-e2e-live-probe.out

FAKE_PROVIDER_E2E_WORKCELL_CLAUDE="${BARRIER_VERIFY_ROOT}/provider-e2e-fake-workcell-claude.sh"
cat >"${FAKE_PROVIDER_E2E_WORKCELL_CLAUDE}" <<'EOF_FAKE_PROVIDER_E2E_CLAUDE'
#!/bin/bash
set -euo pipefail

for arg in "$@"; do
  if [[ "${arg}" == "--auth-status" ]]; then
    cat <<'STATUS'
credential_keys=claude_api_key
provider_auth_mode=claude_api_key
provider_auth_modes=claude_api_key
shared_auth_modes=none
github_auth_present=0
ssh_injected=0
ssh_config_assurance=off
secret_copy_targets=none
STATUS
    exit 0
  fi
done

if [[ "${1:-}" == "--prepare-only" ]]; then
  exit 0
fi

if [[ " $* " == *" --mode development "* ]]; then
  case " $* " in
    *" -- bash -lc "* ) ;;
    * )
      echo "missing claude development-shell args: $*" >&2
      exit 88
      ;;
  esac
  case " $* " in
    *" WORKCELL_PROVIDER_E2E_SHELL_OK "* ) ;;
    * )
      echo "missing claude development-shell token: $*" >&2
      exit 87
      ;;
  esac
  printf 'WORKCELL_PROVIDER_E2E_SHELL_OK\n'
  exit 0
fi

case " $* " in
  *" --agent-arg -p "* ) ;;
  * )
    echo "missing claude prompt probe args: $*" >&2
    exit 94
    ;;
esac
case " $* " in
  *" --agent-arg --output-format "* ) ;;
  * )
    echo "missing claude output-format probe args: $*" >&2
    exit 93
    ;;
esac
case " $* " in
  *" --agent-arg --no-session-persistence "* ) ;;
  * )
    echo "missing claude session-persistence probe args: $*" >&2
    exit 92
    ;;
esac
case " $* " in
  *" --agent-arg --max-budget-usd "* ) ;;
  * )
    echo "missing claude budget probe args: $*" >&2
    exit 91
    ;;
esac
case " $* " in
  *" --agent-arg --tools "* ) ;;
  * )
    echo "missing claude tools probe args: $*" >&2
    exit 90
    ;;
esac
have_empty_tools_value=0
fake_provider_e2e_args=("$@")
for ((i = 0; i <= ${#fake_provider_e2e_args[@]} - 4; i++)); do
  if [[ "${fake_provider_e2e_args[i]}" == "--agent-arg" && "${fake_provider_e2e_args[i + 1]}" == "--tools" && "${fake_provider_e2e_args[i + 2]}" == "--agent-arg" && -z "${fake_provider_e2e_args[i + 3]}" ]]; then
    have_empty_tools_value=1
    break
  fi
done
if [[ "${have_empty_tools_value}" -ne 1 ]]; then
  echo "missing claude empty tools probe value: $*" >&2
  exit 89
fi

printf '{"result":"WORKCELL_PROVIDER_E2E_OK"}\n'
EOF_FAKE_PROVIDER_E2E_CLAUDE
chmod +x "${FAKE_PROVIDER_E2E_WORKCELL_CLAUDE}"

if ! WORKCELL_PROVIDER_E2E_WORKCELL_SCRIPT="${FAKE_PROVIDER_E2E_WORKCELL_CLAUDE}" \
  WORKCELL_E2E_CLAUDE_API_KEY='smoke-api-key' \
  "${ROOT_DIR}/scripts/provider-e2e.sh" \
  --agent claude \
  --workspace "${ROOT_DIR}" \
  --require-injection >/tmp/workcell-provider-e2e-live-probe-claude.out 2>&1; then
  echo "Expected provider-e2e Claude probe to succeed against the fake Workcell shim" >&2
  cat /tmp/workcell-provider-e2e-live-probe-claude.out >&2
  exit 1
fi
grep -q '\[provider-e2e\] auth-status (claude)' /tmp/workcell-provider-e2e-live-probe-claude.out
grep -q '\[provider-e2e\] prepare-only (claude)' /tmp/workcell-provider-e2e-live-probe-claude.out
grep -q '\[provider-e2e\] development-shell (claude)' /tmp/workcell-provider-e2e-live-probe-claude.out
grep -q '^WORKCELL_PROVIDER_E2E_SHELL_OK$' /tmp/workcell-provider-e2e-live-probe-claude.out
grep -q '\[provider-e2e\] live-probe (claude)' /tmp/workcell-provider-e2e-live-probe-claude.out
grep -q '"result":"WORKCELL_PROVIDER_E2E_OK"' /tmp/workcell-provider-e2e-live-probe-claude.out

FAKE_PROVIDER_E2E_WORKCELL_GEMINI="${BARRIER_VERIFY_ROOT}/provider-e2e-fake-workcell-gemini.sh"
cat >"${FAKE_PROVIDER_E2E_WORKCELL_GEMINI}" <<'EOF_FAKE_PROVIDER_E2E_GEMINI'
#!/bin/bash
set -euo pipefail

for arg in "$@"; do
  if [[ "${arg}" == "--auth-status" ]]; then
    cat <<'STATUS'
credential_keys=gemini_env
provider_auth_mode=gemini_env
provider_auth_modes=gemini_env
shared_auth_modes=none
github_auth_present=0
ssh_injected=0
ssh_config_assurance=off
secret_copy_targets=none
STATUS
    exit 0
  fi
done

if [[ "${1:-}" == "--prepare-only" ]]; then
  exit 0
fi

if [[ " $* " == *" --mode development "* ]]; then
  case " $* " in
    *" -- bash -lc "* ) ;;
    * )
      echo "missing gemini development-shell args: $*" >&2
      exit 94
      ;;
  esac
  case " $* " in
    *" WORKCELL_PROVIDER_E2E_SHELL_OK "* ) ;;
    * )
      echo "missing gemini development-shell token: $*" >&2
      exit 93
      ;;
  esac
  printf 'WORKCELL_PROVIDER_E2E_SHELL_OK\n'
  exit 0
fi

case " $* " in
  *" --agent-arg -p "* ) ;;
  * )
    echo "missing gemini prompt probe args: $*" >&2
    exit 96
    ;;
esac
case " $* " in
  *" --agent-arg --output-format "* ) ;;
  * )
    echo "missing gemini output-format probe args: $*" >&2
    exit 95
    ;;
esac

printf '{\n  "response": "WORKCELL_PROVIDER_E2E_OK"\n}\n'
EOF_FAKE_PROVIDER_E2E_GEMINI
chmod +x "${FAKE_PROVIDER_E2E_WORKCELL_GEMINI}"

if ! WORKCELL_PROVIDER_E2E_WORKCELL_SCRIPT="${FAKE_PROVIDER_E2E_WORKCELL_GEMINI}" \
  WORKCELL_E2E_GEMINI_ENV=$'GOOGLE_GENAI_USE_VERTEXAI=true\nGOOGLE_CLOUD_PROJECT=smoke-project\nGOOGLE_CLOUD_LOCATION=us-central1\n' \
  "${ROOT_DIR}/scripts/provider-e2e.sh" \
  --agent gemini \
  --workspace "${ROOT_DIR}" \
  --require-injection >/tmp/workcell-provider-e2e-live-probe-gemini.out 2>&1; then
  echo "Expected provider-e2e Gemini probe to succeed against the fake Workcell shim" >&2
  cat /tmp/workcell-provider-e2e-live-probe-gemini.out >&2
  exit 1
fi
grep -q '\[provider-e2e\] auth-status (gemini)' /tmp/workcell-provider-e2e-live-probe-gemini.out
grep -q '\[provider-e2e\] prepare-only (gemini)' /tmp/workcell-provider-e2e-live-probe-gemini.out
grep -q '\[provider-e2e\] development-shell (gemini)' /tmp/workcell-provider-e2e-live-probe-gemini.out
grep -q '^WORKCELL_PROVIDER_E2E_SHELL_OK$' /tmp/workcell-provider-e2e-live-probe-gemini.out
grep -q '\[provider-e2e\] live-probe (gemini)' /tmp/workcell-provider-e2e-live-probe-gemini.out
grep -q '"response": "WORKCELL_PROVIDER_E2E_OK"' /tmp/workcell-provider-e2e-live-probe-gemini.out

FAKE_PROVIDER_E2E_WORKCELL_NONE="${BARRIER_VERIFY_ROOT}/provider-e2e-fake-workcell-none.sh"
cat >"${FAKE_PROVIDER_E2E_WORKCELL_NONE}" <<'EOF_FAKE_PROVIDER_E2E_NONE'
#!/bin/bash
set -euo pipefail

for arg in "$@"; do
  if [[ "${arg}" == "--auth-status" ]]; then
    printf 'provider_auth_mode=none\n'
    exit 0
  fi
done

echo "unexpected fake provider-e2e workcell invocation: $*" >&2
exit 99
EOF_FAKE_PROVIDER_E2E_NONE
chmod +x "${FAKE_PROVIDER_E2E_WORKCELL_NONE}"

if WORKCELL_PROVIDER_E2E_WORKCELL_SCRIPT="${FAKE_PROVIDER_E2E_WORKCELL_NONE}" \
  WORKCELL_E2E_CODEX_AUTH_JSON='{"token":"codex-smoke"}' \
  "${ROOT_DIR}/scripts/provider-e2e.sh" \
  --agent codex \
  --workspace "${ROOT_DIR}" >/tmp/workcell-provider-e2e-auth-guard.out 2>&1; then
  echo "Expected provider-e2e auth guard to fail when injected credentials are not recognized" >&2
  exit 1
fi
grep -q 'Workcell did not detect provider auth for codex' /tmp/workcell-provider-e2e-auth-guard.out

if WORKCELL_PROVIDER_E2E_WORKCELL_SCRIPT="${FAKE_PROVIDER_E2E_WORKCELL_NONE}" \
  WORKCELL_E2E_CLAUDE_API_KEY='smoke-api-key' \
  "${ROOT_DIR}/scripts/provider-e2e.sh" \
  --agent claude \
  --workspace "${ROOT_DIR}" >/tmp/workcell-provider-e2e-auth-guard-claude.out 2>&1; then
  echo "Expected provider-e2e auth guard to fail for Claude when injected credentials are not recognized" >&2
  exit 1
fi
grep -q 'Workcell did not detect provider auth for claude' /tmp/workcell-provider-e2e-auth-guard-claude.out

if "${ROOT_DIR}/scripts/provider-e2e.sh" \
  --agent claude \
  --workspace "${ROOT_DIR}" \
  --require-injection \
  --dry-run >/tmp/workcell-provider-e2e-missing-injection.out 2>&1; then
  echo "Expected provider-e2e require-injection mode to fail without explicit or generated credentials" >&2
  exit 1
fi
grep -q 'No injection policy is available' /tmp/workcell-provider-e2e-missing-injection.out

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
for settings_path in \
  "${ROOT_DIR}/adapters/claude/.claude/settings.json" \
  "${ROOT_DIR}/adapters/claude/managed-settings.json"; do
  if ! jq -e '.enableAllProjectMcpServers == false' "${settings_path}" >/dev/null; then
    echo "$(basename "${settings_path}") settings must disable auto-enabled project MCP servers" >&2
    exit 1
  fi
  if ! jq -e '.hooks.PreToolUse[0].hooks[0].command == "/opt/workcell/adapters/claude/hooks/guard-bash.sh"' "${settings_path}" >/dev/null; then
    echo "$(basename "${settings_path}") settings must use the managed guard-bash.sh hook" >&2
    exit 1
  fi
done
if ! jq -e '.disableBypassPermissionsMode == "allow"' "${ROOT_DIR}/adapters/claude/managed-settings.json" >/dev/null; then
  echo "Claude managed settings must allow bypass-permissions mode under the external Workcell boundary" >&2
  exit 1
fi
if ! jq -e '.tools.allowed == []' "${ROOT_DIR}/adapters/gemini/.gemini/settings.json" >/dev/null; then
  echo "Gemini adapter must not seed allowed tools" >&2
  exit 1
fi
if ! jq -e '.mcp.allowed == []' "${ROOT_DIR}/adapters/gemini/.gemini/settings.json" >/dev/null; then
  echo "Gemini adapter must not seed allowed MCP servers" >&2
  exit 1
fi
if jq -e '.security.auth.selectedType' "${ROOT_DIR}/adapters/gemini/.gemini/settings.json" >/dev/null 2>&1; then
  echo "Gemini adapter baseline must not hardcode a selected auth type" >&2
  exit 1
fi
if ! jq -e '.security.folderTrust.enabled == false' "${ROOT_DIR}/adapters/gemini/.gemini/settings.json" >/dev/null; then
  echo "Gemini adapter must disable Gemini folder trust inside the managed runtime" >&2
  exit 1
fi
if ! jq -e '.tools.shell.enableInteractiveShell == false' "${ROOT_DIR}/adapters/gemini/.gemini/settings.json" >/dev/null; then
  echo "Gemini adapter must disable interactive shell mode" >&2
  exit 1
fi
if ! jq -e '.advanced.excludedEnvVars | type == "array"' "${ROOT_DIR}/adapters/gemini/.gemini/settings.json" >/dev/null; then
  echo "Gemini adapter must exclude sensitive environment variables" >&2
  exit 1
fi

GEMINI_AUTH_SELECTION_HARNESS="$(mktemp)"
GEMINI_AUTH_SELECTION_STDOUT="$(mktemp)"
GEMINI_AUTH_SELECTION_STDERR="$(mktemp)"
{
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_trim_leading_whitespace
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_trim_trailing_whitespace
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_env_file_assignment_value
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_gemini_env_key_is_supported
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_validate_gemini_env_file_syntax
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_env_file_boolean_value
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_env_file_value_is_true
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_gemini_env_has_project_config
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_gemini_env_has_location_config
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_validate_gemini_env_auth_config
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_validate_json_object_file
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_validate_gemini_projects_config
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_gemini_selected_auth_type_from_env_file
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_gemini_selected_auth_type
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_set_gemini_selected_auth_type
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_set_gemini_folder_trust_enabled
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_render_gemini_trusted_folders
  printf '\n'
  extract_top_level_bash_function "${ROOT_DIR}/runtime/container/home-control-plane.sh" workcell_target_is_allowed
  cat <<'EOF'
set -Eeuo pipefail
trap 'echo "Gemini auth selection harness failed at line ${LINENO}: ${BASH_COMMAND}" >&2' ERR
export PS4='+ gemini-harness:${LINENO}: '
set -x

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

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
EOF
} >"${GEMINI_AUTH_SELECTION_HARNESS}"
/bin/bash "${GEMINI_AUTH_SELECTION_HARNESS}" >"${GEMINI_AUTH_SELECTION_STDOUT}" 2>"${GEMINI_AUTH_SELECTION_STDERR}" || {
  echo "Gemini auth selection harness stdout (tail):" >&2
  tail -n 200 "${GEMINI_AUTH_SELECTION_STDOUT}" >&2 || true
  echo "Gemini auth selection harness stderr (tail):" >&2
  tail -n 200 "${GEMINI_AUTH_SELECTION_STDERR}" >&2 || true
  exit 1
}
rm -f "${GEMINI_AUTH_SELECTION_HARNESS}"
rm -f "${GEMINI_AUTH_SELECTION_STDOUT}" "${GEMINI_AUTH_SELECTION_STDERR}"

if ! rg -q 'trustedFolders\.json' "${ROOT_DIR}/runtime/container/home-control-plane.sh"; then
  echo "Expected Gemini home seeding to provision trustedFolders.json" >&2
  exit 1
fi
if ! grep -Fq "workcell_reset_session_target \"\${HOME}/.gemini/settings.json\" \"Gemini settings\"" "${ROOT_DIR}/runtime/container/home-control-plane.sh"; then
  echo "Expected Gemini home seeding to reset settings.json through workcell_reset_session_target" >&2
  exit 1
fi
if ! grep -Fq "workcell_set_gemini_tool_sandbox \"\${HOME}/.gemini/settings.json\" false" "${ROOT_DIR}/runtime/container/home-control-plane.sh"; then
  echo "Expected Gemini home seeding to pin the nested sandbox setting explicitly" >&2
  exit 1
fi
if ! grep -Fq "workcell_copy_manifest_credential_file claude_auth \"\${HOME}/.claude/.credentials.json\" || true" "${ROOT_DIR}/runtime/container/home-control-plane.sh"; then
  echo "Expected Claude home seeding to copy auth into .claude/.credentials.json" >&2
  exit 1
fi
if ! grep -Fq "workcell_copy_manifest_credential_file claude_auth \"\${HOME}/.claude/.claude.json\" || true" "${ROOT_DIR}/runtime/container/home-control-plane.sh"; then
  echo "Expected Claude home seeding to copy auth into .claude/.claude.json" >&2
  exit 1
fi
if ! grep -Fq "workcell_copy_manifest_credential_file claude_auth \"\${HOME}/.claude.json\" || true" "${ROOT_DIR}/runtime/container/home-control-plane.sh"; then
  echo "Expected Claude home seeding to copy auth into .claude.json" >&2
  exit 1
fi
if ! grep -Fq 'unset CLAUDE_CONFIG_DIR' "${ROOT_DIR}/runtime/container/provider-wrapper.sh"; then
  echo "Expected provider wrapper to discard caller-supplied CLAUDE_CONFIG_DIR" >&2
  exit 1
fi
if grep -Fq 'export HOME CODEX_HOME CLAUDE_CONFIG_DIR TMPDIR WORKCELL_MODE CODEX_PROFILE WORKCELL_AGENT_AUTONOMY WORKCELL_CONTAINER_MUTABILITY' "${ROOT_DIR}/runtime/container/provider-wrapper.sh"; then
  echo "Provider wrapper should not export CLAUDE_CONFIG_DIR for non-Claude launches" >&2
  exit 1
fi
if ! grep -Fq 'unset DISABLE_AUTOUPDATER' "${ROOT_DIR}/runtime/container/provider-wrapper.sh"; then
  echo "Expected provider wrapper to discard caller-supplied DISABLE_AUTOUPDATER" >&2
  exit 1
fi
for gemini_sandbox_env in \
  'unset GEMINI_SANDBOX' \
  'unset GEMINI_SANDBOX_IMAGE' \
  'unset GEMINI_SANDBOX_IMAGE_DEFAULT' \
  'unset GEMINI_SANDBOX_PROXY_COMMAND' \
  'unset BUILD_SANDBOX' \
  'unset SANDBOX' \
  'unset SANDBOX_FLAGS' \
  'unset SANDBOX_MOUNTS' \
  'unset SANDBOX_ENV' \
  'unset SANDBOX_PORTS' \
  'unset SANDBOX_SET_UID_GID' \
  'unset SEATBELT_PROFILE'; do
  if ! grep -Fq -- "${gemini_sandbox_env}" "${ROOT_DIR}/runtime/container/provider-wrapper.sh"; then
    echo "Expected provider wrapper to scrub Gemini sandbox env knob: ${gemini_sandbox_env}" >&2
    exit 1
  fi
done
if ! grep -Fq "DISABLE_AUTOUPDATER=1 CLAUDE_CONFIG_DIR=\"\${HOME}/.claude\" exec /usr/local/libexec/workcell/real/claude \\" "${ROOT_DIR}/runtime/container/provider-wrapper.sh"; then
  echo "Expected provider wrapper to launch the pinned native Claude binary with managed env" >&2
  exit 1
fi
if ! grep -Fq "GEMINI_CLI_NO_RELAUNCH=1 GEMINI_SANDBOX=false exec /usr/local/libexec/workcell/real/node \\" "${ROOT_DIR}/runtime/container/provider-wrapper.sh"; then
  echo "Expected provider wrapper to pin Gemini native sandbox off on the managed path" >&2
  exit 1
fi
if ! grep -Fq "Workcell blocked Claude lifecycle command: \${arg}" "${ROOT_DIR}/runtime/container/provider-policy.sh"; then
  echo "Expected provider policy to reject native Claude lifecycle commands that bypass the pinned image" >&2
  exit 1
fi
if ! grep -Fq '/usr/local/libexec/workcell/real/claude' "${ROOT_DIR}/adapters/codex/managed_config.toml"; then
  echo "Expected Codex managed rules to block the native Claude binary path" >&2
  exit 1
fi
if grep -Fq '@anthropic-ai/claude-code/cli.js' "${ROOT_DIR}/adapters/codex/managed_config.toml"; then
  echo "Codex managed rules should not reference the removed Claude npm entrypoint" >&2
  exit 1
fi
if ! grep -Fq '/usr/local/libexec/workcell/real/claude' "${ROOT_DIR}/adapters/claude/hooks/guard-bash.sh"; then
  echo "Expected Claude Bash guard to block direct native Claude binary launches" >&2
  exit 1
fi
if grep -Fq '@anthropic-ai/claude-code/cli.js' "${ROOT_DIR}/adapters/claude/hooks/guard-bash.sh"; then
  echo "Claude Bash guard should not reference the removed Claude npm entrypoint" >&2
  exit 1
fi

if ! awk '
  $0 == "  acquire_profile_lock \"${COLIMA_PROFILE}\"" { seen_lock = 1; next }
  seen_lock && $0 == "  # Another launch may have created or repaired the profile while we waited." { seen_comment = 1; next }
  seen_lock && seen_comment && $0 == "  refresh_profile_state \"${COLIMA_PROFILE}\"" { found = 1; exit }
  END { exit(found ? 0 : 1) }
' "${ROOT_DIR}/scripts/workcell"; then
  echo "Expected workcell to refresh profile state immediately after taking the profile lock" >&2
  exit 1
fi

if ! sed -n '/^acquire_profile_lock()/,/^}/p' "${ROOT_DIR}/scripts/workcell" | awk '
  $0 == "  while true; do" && state == 0 { state = 1; next }
  $0 == "    if ! acquired_state=\"$(go_hostutil launcher acquire-profile-lock \"${lock_dir}\" \"$$\")\"; then" && state == 1 { state = 2; next }
  $0 == "      echo \"Failed to create managed runtime lock for profile ${profile}.\" >&2" && state == 2 { state = 3; next }
  $0 == "      return 1" && state == 3 { state = 4; next }
  $0 == "    fi" && state == 4 { state = 5; next }
  $0 == "    if [[ \"${acquired_state}\" == \"1\" ]]; then" && state == 5 { state = 6; next }
  $0 == "      PROFILE_LOCK_DIR=\"${lock_dir}\"" && state == 6 { state = 7; next }
  $0 == "      return 0" && state == 7 { state = 8; next }
  $0 == "    fi" && state == 8 { state = 9; next }
  $0 == "    if ! stale_state=\"$(profile_lock_is_stale \"${lock_dir}\")\"; then" && state == 9 { state = 10; next }
  $0 == "      echo \"Failed to inspect managed runtime lock state for profile ${profile}.\" >&2" && state == 10 { state = 11; next }
  $0 == "      return 1" && state == 11 { state = 12; next }
  $0 == "    fi" && state == 12 { state = 13; next }
  $0 == "    if [[ \"${stale_state}\" == \"1\" ]]; then" && state == 13 { state = 14; exit }
  END { exit(state == 14 ? 0 : 1) }
'; then
  echo "Expected workcell to acquire profile locks atomically and fail fast when lock state cannot be inspected" >&2
  exit 1
fi

# shellcheck disable=SC2016
for needle in \
  'workspace_runtime_probe_path()' \
  'validate_colima_runtime_workspace_view()' \
  'validate_colima_runtime_workspace_view "${profile}" "${workspace}"' \
  'Refreshing managed Colima profile ${COLIMA_PROFILE} because the running VM is not exposing the expected workspace contents.' \
  'Refreshing managed Colima profile ${COLIMA_PROFILE} because the started VM did not expose the expected workspace view.'; do
  if ! grep -Fq -- "${needle}" "${ROOT_DIR}/scripts/workcell"; then
    echo "Expected workcell mount-view validation snippet missing: ${needle}" >&2
    exit 1
  fi
done

# shellcheck disable=SC2016
for needle in 'cd "${home}" &&' 'cd / &&' 'LIMA_WORKDIR=/'; do
  if ! grep -Fq -- "${needle}" "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"; then
    echo "Expected egress helper safe-cwd snippet missing: ${needle}" >&2
    exit 1
  fi
done

for token in '--inspect' 'print_inspect_state' 'provider_native_sandbox_configured' 'provider_native_sandbox_effective' 'provider_native_sandbox_reason' 'codex' 'claude' 'gemini'; do
  if ! grep -Fq -- "${token}" "${ROOT_DIR}/scripts/workcell"; then
    echo "Expected workcell to contain --inspect contract token: ${token}" >&2
    exit 1
  fi
done

for field in workspace network_policy session_assurance_initial provider_native_sandbox_configured provider_native_sandbox_effective provider_native_sandbox_reason codex claude gemini; do
  if ! grep -Fq -- "${field}" "${ROOT_DIR}/scripts/workcell" && ! grep -Fq -- "${field}" "${ROOT_DIR}/runtime/container/assurance.sh"; then
    echo "Expected audit log field referenced in control scripts: ${field}" >&2
    exit 1
  fi
done

if [[ ! -d "${ROOT_DIR}/docs/examples" ]]; then
  echo "docs/examples/ must exist" >&2
  exit 1
fi

if [[ ! -f "${ROOT_DIR}/tests/scenarios/manifest.json" ]]; then
  echo "tests/scenarios/manifest.json must exist" >&2
  exit 1
fi

for scenario_script in \
  "${ROOT_DIR}/scripts/run-scenario-tests.sh" \
  "${ROOT_DIR}/scripts/verify-scenario-coverage.sh" \
  "${ROOT_DIR}/scripts/verify-control-plane-parity.sh"; do
  if [[ ! -x "${scenario_script}" ]]; then
    echo "Expected executable scenario script: ${scenario_script}" >&2
    exit 1
  fi
done

if ! jq -e '[.hooks.PreToolUse[]?.hooks[0].command? // empty] | any(type == "string" and endswith("guard-bash.sh"))' "${ROOT_DIR}/adapters/claude/managed-settings.json" >/dev/null; then
  echo "guard-bash.sh hook must be registered in managed-settings.json PreToolUse" >&2
  exit 1
fi

for scenario_script_basename in \
  "run-scenario-tests.sh" \
  "verify-scenario-coverage.sh" \
  "verify-control-plane-parity.sh"; do
  if ! grep -Fq -- "${scenario_script_basename}" "${ROOT_DIR}/scripts/validate-repo.sh"; then
    echo "validate-repo.sh must reference ${scenario_script_basename}" >&2
    exit 1
  fi
done

echo "Workcell invariant verification passed."
