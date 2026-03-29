#!/usr/bin/env -S BASH_ENV= ENV= bash

# shellcheck source=runtime/container/assurance.sh
source /usr/local/libexec/workcell/assurance.sh

workcell_managed_session_root_for_path() {
  local path="$1"

  case "${path}" in
    /state/agent-home | /state/agent-home/*)
      printf '/state/agent-home\n'
      ;;
    /state/injected | /state/injected/*)
      printf '/state/injected\n'
      ;;
    *)
      workcell_die "Workcell session target is outside the managed session roots: ${path}"
      ;;
  esac
}

workcell_assert_no_symlink_path_components() {
  local path="$1"
  local label="$2"
  local include_target="${3:-1}"
  local root=""
  local current=""

  root="$(workcell_managed_session_root_for_path "${path}")"
  if [[ "${include_target}" == "1" ]]; then
    current="${path}"
  else
    current="$(dirname "${path}")"
  fi
  while :; do
    if [[ -L "${current}" ]]; then
      workcell_die "Workcell refused ${label}: symlinked session path component ${current}"
    fi
    if [[ "${current}" == "${root}" ]]; then
      return 0
    fi
    current="$(dirname "${current}")"
  done
}

workcell_prepare_session_parent() {
  local target_path="$1"
  local label="$2"
  local parent_path=""

  workcell_assert_no_symlink_path_components "${target_path}" "${label}" 0
  parent_path="$(dirname "${target_path}")"
  mkdir -p "${parent_path}"
  workcell_assert_no_symlink_path_components "${target_path}" "${label}" 0
}

workcell_prepare_session_directory() {
  local target_path="$1"
  local label="$2"

  workcell_prepare_session_parent "${target_path}" "${label}"
  if [[ -e "${target_path}" ]] && [[ ! -d "${target_path}" ]]; then
    rm -rf "${target_path}"
  fi
  mkdir -p "${target_path}"
  workcell_assert_no_symlink_path_components "${target_path}" "${label}"
  workcell_file_trace_log_path_state "prepare-directory" "${target_path}" "label=$(printf '%q' "${label}")"
}

workcell_reset_session_target() {
  local target_path="$1"
  local label="$2"

  workcell_prepare_session_parent "${target_path}" "${label}"
  if [[ -L "${target_path}" ]]; then
    workcell_file_trace_log_path_state "reset-remove" "${target_path}" "label=$(printf '%q' "${label}")"
    rm -f "${target_path}"
  elif [[ -e "${target_path}" ]]; then
    workcell_file_trace_log_path_state "reset-remove" "${target_path}" "label=$(printf '%q' "${label}")"
    rm -rf "${target_path}"
  fi
  workcell_assert_no_symlink_path_components "${target_path}" "${label}"
  workcell_file_trace_emit \
    "event=reset-complete" \
    "path=$(printf '%q' "${target_path}")" \
    "label=$(printf '%q' "${label}")"
}

workcell_link_control_plane_path() {
  local source_path="$1"
  local target_path="$2"

  workcell_reset_session_target "${target_path}" "control-plane link"
  ln -s "${source_path}" "${target_path}"
  workcell_file_trace_log_path_state "link-control-plane" "${target_path}" "source=$(printf '%q' "${source_path}")"
}

workcell_copy_control_plane_tree() {
  local source_path="$1"
  local target_path="$2"
  local file_mode="$3"
  local dir_mode="$4"

  workcell_reset_session_target "${target_path}" "control-plane tree"
  mkdir -p "${target_path}"
  cp -R "${source_path}/." "${target_path}"
  find "${target_path}" -type d -exec chmod "${dir_mode}" {} +
  find "${target_path}" -type f -exec chmod "${file_mode}" {} +
  chmod "${dir_mode}" "${target_path}"
  workcell_file_trace_log_path_state "copy-control-plane-tree" "${target_path}" "source=$(printf '%q' "${source_path}")"
}

workcell_copy_control_plane_file() {
  local source_path="$1"
  local target_path="$2"
  local file_mode="$3"

  workcell_reset_session_target "${target_path}" "control-plane file"
  cp "${source_path}" "${target_path}"
  chmod "${file_mode}" "${target_path}"
  workcell_file_trace_log_path_state "copy-control-plane-file" "${target_path}" "source=$(printf '%q' "${source_path}")"
}

WORKCELL_FILE_TRACE_PATH="${WORKCELL_FILE_TRACE_PATH:-}"
WORKCELL_FILE_TRACE_INTERVAL_SEC="${WORKCELL_FILE_TRACE_INTERVAL_SEC:-1}"
WORKCELL_FILE_TRACE_WATCH_PID=""
WORKCELL_FILE_TRACE_WATCH_ROOT=""
WORKCELL_FILE_TRACE_WATCH_STATE_FILE=""

workcell_file_trace_enabled() {
  [[ -n "${WORKCELL_FILE_TRACE_PATH}" ]]
}

workcell_file_trace_emit() {
  local field=""

  workcell_file_trace_enabled || return 0
  mkdir -p "$(dirname "${WORKCELL_FILE_TRACE_PATH}")"
  touch "${WORKCELL_FILE_TRACE_PATH}"
  chmod 0600 "${WORKCELL_FILE_TRACE_PATH}" 2>/dev/null || true
  printf '%s' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >>"${WORKCELL_FILE_TRACE_PATH}"
  for field in "$@"; do
    printf '\t%s' "${field}" >>"${WORKCELL_FILE_TRACE_PATH}"
  done
  printf '\n' >>"${WORKCELL_FILE_TRACE_PATH}"
}

workcell_file_trace_path_is_sensitive() {
  local path="$1"
  local candidate=""
  local -a sensitive_exact_paths=(
    "${HOME}/.claude.json"
    "${HOME}/.mcp.json"
    "${HOME}/.npmrc"
    "${HOME}/.pypirc"
  )
  local -a sensitive_prefixes=(
    "${HOME}/.aws"
    "${HOME}/.azure"
    "${HOME}/.codex"
    "${HOME}/.claude"
    "${HOME}/.config/aws"
    "${HOME}/.config/azure"
    "${HOME}/.config/claude-code"
    "${HOME}/.config/docker"
    "${HOME}/.gemini"
    "${HOME}/.config/gcloud"
    "${HOME}/.config/gh"
    "${HOME}/.docker"
    "${HOME}/.gnupg"
    "${HOME}/.kube"
    "${HOME}/.npm"
    "${HOME}/.ssh"
    "${HOME}/.terraform.d"
  )

  for candidate in "${sensitive_exact_paths[@]}"; do
    if [[ "${path}" == "${candidate}" ]]; then
      return 0
    fi
  done

  for candidate in "${sensitive_prefixes[@]}"; do
    if [[ "${path}" == "${candidate}" ]] || [[ "${path}" == "${candidate}/"* ]]; then
      return 0
    fi
  done

  return 1
}

workcell_file_trace_snapshot_row() {
  local path="$1"
  local kind="missing"
  local mode="-"
  local size="-"
  local mtime="-"
  local target="-"

  workcell_file_trace_path_is_sensitive "${path}" && return 1

  if [[ -L "${path}" ]]; then
    kind="symlink"
    target="$(readlink "${path}" 2>/dev/null || true)"
  elif [[ -d "${path}" ]]; then
    kind="dir"
  elif [[ -f "${path}" ]]; then
    kind="file"
  elif [[ -e "${path}" ]]; then
    kind="other"
  fi

  if [[ "${kind}" != "missing" ]]; then
    mode="$(stat -c '%a' -- "${path}" 2>/dev/null || true)"
    size="$(stat -c '%s' -- "${path}" 2>/dev/null || true)"
    mtime="$(stat -c '%Y' -- "${path}" 2>/dev/null || true)"
  fi

  printf '%q\t%s\t%s\t%s\t%s\t%q\n' "${path}" "${kind}" "${mode:-?}" "${size:-?}" "${mtime:-?}" "${target}"
}

workcell_file_trace_collect_snapshot() {
  local root_path="$1"
  local output_path="$2"
  local candidate=""
  local row=""

  : >"${output_path}"
  [[ -d "${root_path}" ]] || return 0
  while IFS= read -r -d '' candidate; do
    row="$(workcell_file_trace_snapshot_row "${candidate}")" || continue
    printf '%s\n' "${row}" >>"${output_path}"
  done < <(find "${root_path}" -xdev \( -type d -o -type f -o -type l \) -print0 | LC_ALL=C sort -z)
}

workcell_file_trace_diff_snapshots() {
  local phase="$1"
  local before_path="$2"
  local after_path="$3"
  local path_key=""
  local state=""
  local -A before_map=()
  local -A after_map=()

  [[ -f "${before_path}" ]] || : >"${before_path}"
  [[ -f "${after_path}" ]] || : >"${after_path}"

  while IFS=$'\t' read -r path_key state; do
    [[ -n "${path_key}" ]] || continue
    before_map["${path_key}"]="${state}"
  done <"${before_path}"

  while IFS=$'\t' read -r path_key state; do
    [[ -n "${path_key}" ]] || continue
    after_map["${path_key}"]="${state}"
  done <"${after_path}"

  for path_key in "${!before_map[@]}"; do
    if [[ ! -v after_map["${path_key}"] ]]; then
      workcell_file_trace_emit \
        "event=delete" \
        "phase=${phase}" \
        "path=${path_key}" \
        "previous=${before_map[${path_key}]}"
    elif [[ "${before_map[${path_key}]}" != "${after_map[${path_key}]}" ]]; then
      workcell_file_trace_emit \
        "event=modify" \
        "phase=${phase}" \
        "path=${path_key}" \
        "previous=${before_map[${path_key}]}" \
        "current=${after_map[${path_key}]}"
    fi
  done

  for path_key in "${!after_map[@]}"; do
    if [[ ! -v before_map["${path_key}"] ]]; then
      workcell_file_trace_emit \
        "event=create" \
        "phase=${phase}" \
        "path=${path_key}" \
        "current=${after_map[${path_key}]}"
    fi
  done
}

workcell_file_trace_log_path_state() {
  local event="$1"
  local path="$2"
  local detail="${3:-}"
  local kind="missing"
  local mode="-"
  local size="-"
  local mtime="-"
  local sha256="-"
  local target="-"

  workcell_file_trace_enabled || return 0
  workcell_file_trace_path_is_sensitive "${path}" && return 0

  if [[ -L "${path}" ]]; then
    kind="symlink"
    target="$(readlink "${path}" 2>/dev/null || true)"
  elif [[ -d "${path}" ]]; then
    kind="dir"
  elif [[ -f "${path}" ]]; then
    kind="file"
  elif [[ -e "${path}" ]]; then
    kind="other"
  fi

  if [[ "${kind}" != "missing" ]]; then
    mode="$(stat -c '%a' -- "${path}" 2>/dev/null || true)"
    size="$(stat -c '%s' -- "${path}" 2>/dev/null || true)"
    mtime="$(stat -c '%Y' -- "${path}" 2>/dev/null || true)"
  fi
  if [[ "${kind}" == "file" ]]; then
    sha256="$(sha256sum "${path}" 2>/dev/null | awk '{print $1}' || true)"
    [[ -n "${sha256}" ]] || sha256="-"
  fi

  workcell_file_trace_emit \
    "event=${event}" \
    "path=$(printf '%q' "${path}")" \
    "kind=${kind}" \
    "mode=${mode:-?}" \
    "size=${size:-?}" \
    "mtime=${mtime:-?}" \
    "sha256=${sha256}" \
    "target=$(printf '%q' "${target}")" \
    "${detail}"
}

workcell_file_trace_watch_session_root() {
  local root_path="$1"
  local state_path="$2"
  local next_state=""

  while :; do
    sleep "${WORKCELL_FILE_TRACE_INTERVAL_SEC}"
    next_state="$(mktemp /tmp/workcell-file-trace.snapshot.XXXXXX)"
    workcell_file_trace_collect_snapshot "${root_path}" "${next_state}"
    workcell_file_trace_diff_snapshots "watch" "${state_path}" "${next_state}"
    mv "${next_state}" "${state_path}"
  done
}

workcell_file_trace_start_watcher() {
  local root_path="$1"

  workcell_file_trace_enabled || return 0
  [[ -n "${WORKCELL_FILE_TRACE_WATCH_PID}" ]] && return 0

  WORKCELL_FILE_TRACE_WATCH_ROOT="${root_path}"
  WORKCELL_FILE_TRACE_WATCH_STATE_FILE="$(mktemp /tmp/workcell-file-trace.state.XXXXXX)"
  workcell_file_trace_collect_snapshot "${root_path}" "${WORKCELL_FILE_TRACE_WATCH_STATE_FILE}"
  workcell_file_trace_emit \
    "event=watch-start" \
    "root=$(printf '%q' "${root_path}")" \
    "interval_sec=${WORKCELL_FILE_TRACE_INTERVAL_SEC}"
  workcell_file_trace_watch_session_root "${root_path}" "${WORKCELL_FILE_TRACE_WATCH_STATE_FILE}" &
  WORKCELL_FILE_TRACE_WATCH_PID="$!"
}

workcell_file_trace_stop_watcher() {
  local final_state=""

  workcell_file_trace_enabled || return 0
  [[ -n "${WORKCELL_FILE_TRACE_WATCH_PID}" ]] || return 0

  kill "${WORKCELL_FILE_TRACE_WATCH_PID}" >/dev/null 2>&1 || true
  wait "${WORKCELL_FILE_TRACE_WATCH_PID}" >/dev/null 2>&1 || true
  final_state="$(mktemp /tmp/workcell-file-trace.final.XXXXXX)"
  workcell_file_trace_collect_snapshot "${WORKCELL_FILE_TRACE_WATCH_ROOT}" "${final_state}"
  workcell_file_trace_diff_snapshots "final" "${WORKCELL_FILE_TRACE_WATCH_STATE_FILE}" "${final_state}"
  mv "${final_state}" "${WORKCELL_FILE_TRACE_WATCH_STATE_FILE}"
  workcell_file_trace_emit \
    "event=watch-stop" \
    "root=$(printf '%q' "${WORKCELL_FILE_TRACE_WATCH_ROOT}")"
  rm -f "${WORKCELL_FILE_TRACE_WATCH_STATE_FILE}"
  WORKCELL_FILE_TRACE_WATCH_PID=""
  WORKCELL_FILE_TRACE_WATCH_ROOT=""
  WORKCELL_FILE_TRACE_WATCH_STATE_FILE=""
}

WORKCELL_WORKSPACE_IMPORT_ROOT="${WORKCELL_WORKSPACE_IMPORT_ROOT:-/opt/workcell/workspace-control-plane}"
WORKCELL_CODEX_RULES_MUTABILITY="${WORKCELL_CODEX_RULES_MUTABILITY:-readonly}"
WORKCELL_CONTROL_PLANE_MANIFEST="${WORKCELL_CONTROL_PLANE_MANIFEST:-/usr/local/libexec/workcell/control-plane-manifest.json}"

workcell_session_assurance() {
  workcell_runtime_state_value WORKCELL_SESSION_ASSURANCE || true
}

workcell_current_agent_autonomy() {
  workcell_runtime_state_value WORKCELL_AGENT_AUTONOMY || printf '%s\n' "${WORKCELL_AGENT_AUTONOMY:-yolo}"
}

workcell_assert_session_regular_writable_file() {
  local target_path="$1"
  local label="$2"

  if [[ ! -f "${target_path}" ]]; then
    workcell_die "Workcell failed to seed ${label}: missing file ${target_path}"
  fi
  if [[ -L "${target_path}" ]]; then
    workcell_die "Workcell failed to seed ${label}: expected a session-local copy, not a symlink: ${target_path}"
  fi
  if [[ ! -w "${target_path}" ]]; then
    workcell_die "Workcell failed to seed ${label}: file is not writable: ${target_path}"
  fi
}

workcell_codex_rules_mutability() {
  case "${WORKCELL_CODEX_RULES_MUTABILITY:-readonly}" in
    readonly | session)
      printf '%s\n' "${WORKCELL_CODEX_RULES_MUTABILITY:-readonly}"
      ;;
    *)
      workcell_die "Unsupported Workcell Codex rules mutability: ${WORKCELL_CODEX_RULES_MUTABILITY}"
      ;;
  esac
}

workcell_codex_rules_promoted_for_session_assurance() {
  local configured_mutability=""
  local assurance=""

  configured_mutability="$(workcell_codex_rules_mutability)"
  assurance="$(workcell_session_assurance)"
  [[ "${configured_mutability}" == "readonly" ]] &&
    [[ "${assurance}" == "lower-assurance-package-mutation" ]]
}

workcell_codex_rules_promoted_for_prompt_autonomy() {
  local configured_mutability=""
  local autonomy=""

  configured_mutability="$(workcell_codex_rules_mutability)"
  autonomy="$(workcell_current_agent_autonomy)"
  [[ "${configured_mutability}" == "readonly" ]] &&
    [[ "${autonomy}" == "prompt" ]]
}

workcell_codex_rules_effective_reason() {
  local configured_mutability=""
  local autonomy=""
  local assurance=""

  configured_mutability="$(workcell_codex_rules_mutability)"
  autonomy="$(workcell_current_agent_autonomy)"
  assurance="$(workcell_session_assurance)"

  if [[ "${configured_mutability}" == "session" ]]; then
    printf 'operator-opt-in\n'
    return 0
  fi
  if [[ "${autonomy}" == "prompt" ]]; then
    printf 'prompt-autonomy\n'
    return 0
  fi
  if [[ "${assurance}" == "lower-assurance-package-mutation" ]]; then
    printf 'package-mutation\n'
    return 0
  fi

  printf 'managed-default\n'
}

workcell_current_effective_codex_rules_mutability() {
  workcell_effective_codex_rules_mutability \
    "$(workcell_codex_rules_mutability)" \
    "$(workcell_current_agent_autonomy)" \
    "$(workcell_session_assurance)"
}

workcell_manifest_active() {
  [[ -n "${WORKCELL_INJECTION_MANIFEST:-}" ]]
}

workcell_manifest_path() {
  printf '%s\n' "${WORKCELL_INJECTION_MANIFEST:-}"
}

workcell_manifest_root() {
  dirname "$(workcell_manifest_path)"
}

workcell_control_plane_manifest_path() {
  printf '%s\n' "${WORKCELL_CONTROL_PLANE_MANIFEST}"
}

workcell_ensure_control_plane_manifest() {
  if [[ ! -f "$(workcell_control_plane_manifest_path)" ]]; then
    workcell_die "Workcell control-plane manifest is missing: $(workcell_control_plane_manifest_path)"
  fi
}

workcell_control_plane_expected_sha() {
  local runtime_path="$1"

  workcell_ensure_control_plane_manifest
  jq -r --arg runtime_path "${runtime_path}" \
    '.runtime_artifacts[] | select(.runtime_path == $runtime_path) | .sha256' \
    "$(workcell_control_plane_manifest_path)" | head -n 1
}

workcell_verify_control_plane_parent_paths() {
  local runtime_path="$1"
  local current_path="/"
  local parent_path=""
  local component=""
  local -a path_components=()

  parent_path="$(dirname "${runtime_path}")"
  if [[ "${parent_path}" == "/" ]]; then
    return 0
  fi

  IFS='/' read -r -a path_components <<<"${parent_path#/}"
  for component in "${path_components[@]}"; do
    [[ -n "${component}" ]] || continue
    current_path="${current_path%/}/${component}"
    if [[ -L "${current_path}" ]]; then
      workcell_die "Workcell control-plane artifact parent must not be a symlink: ${current_path}"
    fi
  done
}

workcell_verify_control_plane_path() {
  local runtime_path="$1"
  local expected_sha=""
  local actual_sha=""

  expected_sha="$(workcell_control_plane_expected_sha "${runtime_path}")"
  if [[ -z "${expected_sha}" ]] || [[ "${expected_sha}" == "null" ]]; then
    workcell_die "Workcell control-plane manifest has no entry for ${runtime_path}"
  fi
  workcell_verify_control_plane_parent_paths "${runtime_path}"
  if [[ -L "${runtime_path}" ]]; then
    workcell_die "Workcell control-plane artifact must not be a symlink: ${runtime_path}"
  fi
  if [[ ! -f "${runtime_path}" ]]; then
    workcell_die "Workcell control-plane artifact is missing: ${runtime_path}"
  fi
  actual_sha="$(sha256sum "${runtime_path}" | awk '{print $1}')"
  if [[ "${actual_sha}" != "${expected_sha}" ]]; then
    workcell_die "Workcell control-plane manifest mismatch for ${runtime_path}: ${actual_sha} != ${expected_sha}"
  fi
}

workcell_verify_control_plane_prefix() {
  local runtime_prefix="$1"
  local runtime_path=""
  local matched=0

  workcell_ensure_control_plane_manifest
  while IFS= read -r runtime_path; do
    [[ -n "${runtime_path}" ]] || continue
    matched=1
    workcell_verify_control_plane_path "${runtime_path}"
  done < <(
    jq -r --arg runtime_prefix "${runtime_prefix}" \
      '.runtime_artifacts[] | select(.runtime_path | startswith($runtime_prefix)) | .runtime_path' \
      "$(workcell_control_plane_manifest_path)"
  )
  if [[ "${matched}" -eq 0 ]]; then
    workcell_die "Workcell control-plane manifest has no entries for prefix ${runtime_prefix}"
  fi
}

workcell_ensure_manifest() {
  if ! workcell_manifest_active; then
    return 1
  fi

  if [[ ! -f "$(workcell_manifest_path)" ]]; then
    workcell_die "Workcell injection manifest is missing: $(workcell_manifest_path)"
  fi
}

workcell_manifest_string() {
  local filter="$1"

  workcell_ensure_manifest || return 1
  jq -r "${filter}" "$(workcell_manifest_path)"
}

workcell_manifest_source_path() {
  local relative_path="$1"

  case "${relative_path}" in
    "" | /* | *".."*)
      workcell_die "Invalid Workcell injection source path: ${relative_path}"
      ;;
  esac

  printf '%s/%s\n' "$(workcell_manifest_root)" "${relative_path}"
}

workcell_validate_direct_mount_path() {
  local mount_path="$1"
  case "${mount_path}" in
    /opt/workcell/host-inputs/*) ;;
    *)
      workcell_die "Workcell direct input mount path is outside the managed host-input root: ${mount_path}"
      ;;
  esac
}

workcell_manifest_direct_mount_path() {
  local filter="$1"
  local mount_path=""

  workcell_ensure_manifest || return 1
  mount_path="$(jq -r "${filter}" "$(workcell_manifest_path)")"
  [[ -n "${mount_path}" ]] || return 0
  workcell_validate_direct_mount_path "${mount_path}"
  printf '%s\n' "${mount_path}"
}

workcell_resolve_manifest_input_path() {
  local source_ref="$1"
  local mount_path="$2"

  if [[ -n "${mount_path}" ]]; then
    workcell_validate_direct_mount_path "${mount_path}"
    printf '%s\n' "${mount_path}"
    return 0
  fi

  workcell_manifest_source_path "${source_ref}"
}

workcell_copy_manifest_credential_file() {
  local key="$1"
  local target_path="$2"
  local source_path=""

  source_path="$(workcell_manifest_direct_mount_path ".credentials[\"${key}\"].mount_path // empty" || true)"
  [[ -n "${source_path}" ]] || return 1

  workcell_reset_session_target "${target_path}" "credential copy"
  if [[ ! -f "${source_path}" ]]; then
    workcell_die "Workcell expected mounted credential file for ${key}: ${source_path}"
  fi
  cp "${source_path}" "${target_path}"
  chmod 0600 "${target_path}"
  workcell_file_trace_log_path_state "copy-credential" "${target_path}" "key=${key}"$'\t'"source=$(printf '%q' "${source_path}")"
  return 0
}

workcell_trim_leading_whitespace() {
  local value="$1"

  printf '%s' "${value#"${value%%[![:space:]]*}"}"
}

workcell_trim_trailing_whitespace() {
  local value="$1"

  printf '%s' "${value%"${value##*[![:space:]]}"}"
}

workcell_env_file_assignment_value() {
  local env_path="$1"
  local expected_key="$2"
  local line=""
  local parsed_key=""
  local value=""

  [[ -f "${env_path}" ]] || return 1

  while IFS= read -r line || [[ -n "${line}" ]]; do
    line="${line%$'\r'}"
    line="$(workcell_trim_leading_whitespace "${line}")"
    [[ -n "${line}" ]] || continue
    [[ "${line}" == \#* ]] && continue

    if [[ "${line}" == export[[:space:]]* ]]; then
      line="${line#export}"
      line="$(workcell_trim_leading_whitespace "${line}")"
    fi

    if [[ "${line}" =~ ^([A-Za-z_][A-Za-z0-9_]*)[[:space:]]*=[[:space:]]*(.*)$ ]]; then
      parsed_key="${BASH_REMATCH[1]}"
      value="${BASH_REMATCH[2]}"
      [[ "${parsed_key}" == "${expected_key}" ]] || continue

      value="$(workcell_trim_trailing_whitespace "$(workcell_trim_leading_whitespace "${value}")")"

      if [[ "${value}" =~ ^\"(.*)\"[[:space:]]*(#.*)?$ ]]; then
        value="${BASH_REMATCH[1]}"
      elif [[ "${value}" =~ ^\'(.*)\'[[:space:]]*(#.*)?$ ]]; then
        value="${BASH_REMATCH[1]}"
      else
        value="${value%%#*}"
        value="$(workcell_trim_trailing_whitespace "$(workcell_trim_leading_whitespace "${value}")")"
      fi

      printf '%s\n' "${value}"
      return 0
    fi
  done <"${env_path}"

  return 1
}

workcell_env_file_has_assignment_key() {
  local env_path="$1"
  local expected_key="$2"
  local line=""
  local parsed_key=""

  [[ -f "${env_path}" ]] || return 1

  while IFS= read -r line || [[ -n "${line}" ]]; do
    line="${line%$'\r'}"
    line="$(workcell_trim_leading_whitespace "${line}")"
    [[ -n "${line}" ]] || continue
    [[ "${line}" == \#* ]] && continue

    if [[ "${line}" == export[[:space:]]* ]]; then
      line="${line#export}"
      line="$(workcell_trim_leading_whitespace "${line}")"
    fi

    if [[ "${line}" =~ ^([A-Za-z_][A-Za-z0-9_]*)[[:space:]]*= ]]; then
      parsed_key="${BASH_REMATCH[1]}"
      [[ "${parsed_key}" == "${expected_key}" ]] || continue
      return 0
    fi
  done <"${env_path}"

  return 1
}

workcell_env_file_boolean_value() {
  local env_path="$1"
  local expected_key="$2"
  local value=""

  value="$(workcell_env_file_assignment_value "${env_path}" "${expected_key}" || true)"
  [[ -n "${value}" ]] || return 1
  value="$(printf '%s' "${value}" | tr '[:upper:]' '[:lower:]')"

  case "${value}" in
    true | false)
      printf '%s\n' "${value}"
      return 0
      ;;
    *)
      workcell_die "Invalid boolean in Gemini auth env file ${env_path}: ${expected_key}=${value}. Use true or false."
      ;;
  esac
}

workcell_gemini_env_has_project_config() {
  local env_path="$1"
  local env_value=""

  env_value="$(workcell_env_file_assignment_value "${env_path}" "GOOGLE_CLOUD_PROJECT" || true)"
  [[ -n "${env_value}" ]] && return 0
  env_value="$(workcell_env_file_assignment_value "${env_path}" "GOOGLE_CLOUD_PROJECT_ID" || true)"
  [[ -n "${env_value}" ]]
}

workcell_gemini_env_has_location_config() {
  local env_path="$1"
  local env_value=""

  env_value="$(workcell_env_file_assignment_value "${env_path}" "GOOGLE_CLOUD_LOCATION" || true)"
  [[ -n "${env_value}" ]] && return 0
  env_value="$(workcell_env_file_assignment_value "${env_path}" "GOOGLE_CLOUD_REGION" || true)"
  [[ -n "${env_value}" ]] && return 0
  env_value="$(workcell_env_file_assignment_value "${env_path}" "CLOUD_ML_REGION" || true)"
  [[ -n "${env_value}" ]] && return 0
  env_value="$(workcell_env_file_assignment_value "${env_path}" "VERTEX_LOCATION" || true)"
  [[ -n "${env_value}" ]] && return 0
  env_value="$(workcell_env_file_assignment_value "${env_path}" "VERTEX_AI_LOCATION" || true)"
  [[ -n "${env_value}" ]]
}

workcell_gemini_env_key_is_supported() {
  case "$1" in
    GEMINI_API_KEY | GOOGLE_API_KEY | GOOGLE_GENAI_USE_GCA | GOOGLE_GENAI_USE_VERTEXAI | GOOGLE_CLOUD_PROJECT | GOOGLE_CLOUD_PROJECT_ID | GOOGLE_CLOUD_LOCATION | GOOGLE_CLOUD_REGION | CLOUD_ML_REGION | VERTEX_LOCATION | VERTEX_AI_LOCATION)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

workcell_validate_gemini_env_file_syntax() {
  local env_path="$1"
  local line=""
  local parsed_key=""
  local value=""
  # Keep duplicate-key tracking compatible with host-side Bash 3.2 invariant runs.
  local seen_keys=$'\n'

  [[ -f "${env_path}" ]] || return 0

  while IFS= read -r line || [[ -n "${line}" ]]; do
    line="${line%$'\r'}"
    line="$(workcell_trim_leading_whitespace "${line}")"
    [[ -n "${line}" ]] || continue
    [[ "${line}" == \#* ]] && continue

    if [[ "${line}" == export[[:space:]]* ]]; then
      line="${line#export}"
      line="$(workcell_trim_leading_whitespace "${line}")"
    fi

    if ! [[ "${line}" =~ ^([A-Za-z_][A-Za-z0-9_]*)[[:space:]]*=[[:space:]]*(.*)$ ]]; then
      workcell_die "Malformed Gemini auth env file ${env_path}: ${line}. Use KEY=value assignments."
    fi

    parsed_key="${BASH_REMATCH[1]}"
    value="${BASH_REMATCH[2]}"
    case "${seen_keys}" in
      *$'\n'"${parsed_key}"$'\n'*)
        workcell_die "Gemini auth env file ${env_path} configures ${parsed_key} more than once."
        ;;
    esac
    seen_keys+="${parsed_key}"$'\n'

    if ! workcell_gemini_env_key_is_supported "${parsed_key}"; then
      workcell_die "Unsupported key in Gemini auth env file ${env_path}: ${parsed_key}."
    fi

    value="$(workcell_trim_trailing_whitespace "$(workcell_trim_leading_whitespace "${value}")")"
    if [[ "${value}" =~ ^\"(.*)\"[[:space:]]*(#.*)?$ ]]; then
      value="${BASH_REMATCH[1]}"
    elif [[ "${value}" =~ ^\'(.*)\'[[:space:]]*(#.*)?$ ]]; then
      value="${BASH_REMATCH[1]}"
    else
      value="${value%%#*}"
      value="$(workcell_trim_trailing_whitespace "$(workcell_trim_leading_whitespace "${value}")")"
    fi

    case "${parsed_key}" in
      GEMINI_API_KEY | GOOGLE_API_KEY | GOOGLE_CLOUD_PROJECT | GOOGLE_CLOUD_PROJECT_ID | GOOGLE_CLOUD_LOCATION | GOOGLE_CLOUD_REGION | CLOUD_ML_REGION | VERTEX_LOCATION | VERTEX_AI_LOCATION)
        if [[ -z "${value}" ]]; then
          workcell_die "Gemini auth env file ${env_path} sets ${parsed_key} but leaves it empty."
        fi
        ;;
    esac
  done <"${env_path}"
}

workcell_validate_gemini_env_auth_config() {
  local env_path="$1"
  local gca_value=""
  local vertex_value=""
  local gemini_api_key=""
  local google_api_key=""
  local has_project=0
  local has_location=0

  [[ -f "${env_path}" ]] || return 0

  workcell_validate_gemini_env_file_syntax "${env_path}"
  gca_value="$(workcell_env_file_boolean_value "${env_path}" "GOOGLE_GENAI_USE_GCA" || true)"
  vertex_value="$(workcell_env_file_boolean_value "${env_path}" "GOOGLE_GENAI_USE_VERTEXAI" || true)"
  gemini_api_key="$(workcell_env_file_assignment_value "${env_path}" "GEMINI_API_KEY" || true)"
  google_api_key="$(workcell_env_file_assignment_value "${env_path}" "GOOGLE_API_KEY" || true)"

  if [[ "${gca_value}" == "true" ]] && [[ "${vertex_value}" == "true" ]]; then
    workcell_die "Gemini auth env file ${env_path} enables both GOOGLE_GENAI_USE_GCA and GOOGLE_GENAI_USE_VERTEXAI. Choose exactly one auth selector."
  fi

  if workcell_gemini_env_has_project_config "${env_path}"; then
    has_project=1
  fi
  if workcell_gemini_env_has_location_config "${env_path}"; then
    has_location=1
  fi

  if [[ "${has_location}" == "1" ]] && [[ "${has_project}" != "1" ]]; then
    workcell_die "Gemini auth env file ${env_path} sets a Google Cloud location without a project."
  fi

  if [[ "${vertex_value}" == "true" ]]; then
    if [[ -n "${google_api_key}" ]] || { [[ "${has_project}" == "1" ]] && [[ "${has_location}" == "1" ]]; }; then
      return 0
    fi
    workcell_die "Gemini auth env file ${env_path} enables GOOGLE_GENAI_USE_VERTEXAI=true without either GOOGLE_API_KEY or both GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION."
  fi

  if [[ -n "${google_api_key}" ]]; then
    workcell_die "Gemini auth env file ${env_path} sets GOOGLE_API_KEY without GOOGLE_GENAI_USE_VERTEXAI=true."
  fi

  if [[ "${gca_value}" == "true" ]] || [[ -n "${gemini_api_key}" ]]; then
    return 0
  fi

  workcell_die "Gemini auth env file ${env_path} does not configure a supported Gemini auth mode. Use GEMINI_API_KEY, GOOGLE_GENAI_USE_GCA=true, or GOOGLE_GENAI_USE_VERTEXAI=true with GOOGLE_API_KEY or both GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION."
}

workcell_validate_json_object_file() {
  local json_path="$1"
  local label="$2"

  [[ -f "${json_path}" ]] || return 0

  if ! jq -e 'type == "object"' "${json_path}" >/dev/null 2>&1; then
    workcell_die "${label} must contain a JSON object: ${json_path}"
  fi
}

workcell_validate_gemini_oauth_config() {
  local oauth_path="$1"

  workcell_validate_json_object_file "${oauth_path}" "Gemini OAuth config"
}

workcell_validate_gcloud_adc_config() {
  local adc_path="$1"

  [[ -f "${adc_path}" ]] || return 0

  if ! jq -e 'type == "object" and (.type | type == "string") and (.type | length > 0)' "${adc_path}" >/dev/null 2>&1; then
    workcell_die "Google ADC config must contain a JSON object with a non-empty string type: ${adc_path}"
  fi
}

workcell_validate_gemini_projects_config() {
  local projects_path="$1"

  [[ -f "${projects_path}" ]] || return 0

  if ! jq -e 'type == "object" and (.projects | type == "object")' "${projects_path}" >/dev/null 2>&1; then
    workcell_die "Gemini projects config must contain a JSON object with an object-valued projects field: ${projects_path}"
  fi
}

workcell_render_gemini_trusted_folders() {
  local target_path="$1"
  local workspace_root="$2"
  local target_dir=""
  local target_name=""
  local rendered_path=""

  target_dir="$(dirname "${target_path}")"
  target_name="$(basename "${target_path}")"
  rendered_path="$(mktemp "${target_dir}/${target_name}.tmp.XXXXXX")"

  jq -n --arg workspace_root "${workspace_root}" \
    '{($workspace_root): "TRUST_FOLDER"}' >"${rendered_path}" || {
    rm -f "${rendered_path}"
    return 1
  }
  mv "${rendered_path}" "${target_path}"

  return 0
}

workcell_env_file_value_is_true() {
  local env_path="$1"
  local expected_key="$2"
  local value=""

  value="$(workcell_env_file_boolean_value "${env_path}" "${expected_key}" || true)"
  [[ "${value}" == "true" ]]
}

workcell_gemini_selected_auth_type_from_env_file() {
  local env_path="$1"
  local env_value=""

  [[ -f "${env_path}" ]] || return 1

  if workcell_env_file_value_is_true "${env_path}" "GOOGLE_GENAI_USE_GCA"; then
    printf 'oauth-personal\n'
    return 0
  fi
  if workcell_env_file_value_is_true "${env_path}" "GOOGLE_GENAI_USE_VERTEXAI"; then
    printf 'vertex-ai\n'
    return 0
  fi
  env_value="$(workcell_env_file_assignment_value "${env_path}" "GEMINI_API_KEY" || true)"
  if [[ -n "${env_value}" ]]; then
    printf 'gemini-api-key\n'
    return 0
  fi

  return 1
}

workcell_gemini_selected_auth_type() {
  local env_path="$1"
  local oauth_path="$2"
  local selected_auth_type=""

  selected_auth_type="$(workcell_gemini_selected_auth_type_from_env_file "${env_path}" || true)"
  if [[ -z "${selected_auth_type}" ]] && [[ -f "${oauth_path}" ]]; then
    selected_auth_type="oauth-personal"
  fi
  [[ -n "${selected_auth_type}" ]] || return 1
  printf '%s\n' "${selected_auth_type}"
}

workcell_set_gemini_selected_auth_type() {
  local settings_path="$1"
  local selected_auth_type="$2"
  local settings_dir=""
  local settings_name=""
  local rendered_path=""

  [[ -n "${selected_auth_type}" ]] || return 0
  settings_dir="$(dirname "${settings_path}")"
  settings_name="$(basename "${settings_path}")"
  rendered_path="$(mktemp "${settings_dir}/${settings_name}.tmp.XXXXXX")"
  jq --arg selected_auth_type "${selected_auth_type}" \
    '.security.auth.selectedType = $selected_auth_type' \
    "${settings_path}" >"${rendered_path}" || {
    rm -f "${rendered_path}"
    return 1
  }
  mv "${rendered_path}" "${settings_path}"
  chmod 0600 "${settings_path}"
}

workcell_set_gemini_folder_trust_enabled() {
  local settings_path="$1"
  local enabled="$2"
  local settings_dir=""
  local settings_name=""
  local rendered_path=""

  settings_dir="$(dirname "${settings_path}")"
  settings_name="$(basename "${settings_path}")"
  rendered_path="$(mktemp "${settings_dir}/${settings_name}.tmp.XXXXXX")"
  jq --argjson enabled "${enabled}" \
    '.security.folderTrust.enabled = $enabled' \
    "${settings_path}" >"${rendered_path}" || {
    rm -f "${rendered_path}"
    return 1
  }
  mv "${rendered_path}" "${settings_path}"
  chmod 0600 "${settings_path}"
}

workcell_workspace_import_path() {
  local relative_path="$1"
  local import_path="${WORKCELL_WORKSPACE_IMPORT_ROOT}/${relative_path}"

  [[ -f "${import_path}" ]] || return 1
  printf '%s\n' "${import_path}"
}

workcell_render_claude_settings() {
  local baseline_path="${ADAPTER_ROOT}/claude/.claude/settings.json"
  local target_path="${HOME}/.claude/settings.json"
  local api_key_source=""
  local helper_dir=""
  local helper_script=""

  api_key_source="$(workcell_manifest_direct_mount_path '.credentials["claude_api_key"].mount_path // empty' || true)"
  if [[ -z "${api_key_source}" ]]; then
    workcell_link_control_plane_path "${baseline_path}" "${target_path}"
    return 0
  fi

  helper_dir="${HOME}/.claude/workcell"
  helper_script="${helper_dir}/api-key-helper.sh"

  workcell_prepare_session_directory "${helper_dir}" "Claude helper directory"
  if [[ ! -f "${api_key_source}" ]]; then
    workcell_die "Workcell expected mounted credential file for claude_api_key: ${api_key_source}"
  fi
  workcell_reset_session_target "${helper_script}" "Claude helper script"
  printf '#!/bin/sh\nset -eu\ncat %s\n' "${api_key_source@Q}" >"${helper_script}"
  chmod 0700 "${helper_script}"
  workcell_file_trace_log_path_state "render-claude-helper" "${helper_script}" "source=$(printf '%q' "${api_key_source}")"
  workcell_reset_session_target "${target_path}" "Claude settings"
  jq --arg helper "${helper_script}" '.apiKeyHelper = $helper' "${baseline_path}" >"${target_path}"
  chmod 0600 "${target_path}"
  workcell_file_trace_log_path_state "render-claude-settings" "${target_path}" "baseline=$(printf '%q' "${baseline_path}")"
}

workcell_target_is_allowed() {
  local target_path="$1"

  case "${target_path}" in
    /state/agent-home | /state/agent-home/* | /state/injected | /state/injected/*) ;;
    *)
      return 1
      ;;
  esac

  case "${target_path}" in
    /state/agent-home/.codex/AGENTS.md | \
      /state/agent-home/.codex/auth.json | \
      /state/agent-home/.codex/config.toml | \
      /state/agent-home/.codex/managed_config.toml | \
      /state/agent-home/.codex/requirements.toml | \
      /state/agent-home/.claude/settings.json | \
      /state/agent-home/.claude/CLAUDE.md | \
      /state/agent-home/.claude/.claude.json | \
      /state/agent-home/.claude.json | \
      /state/agent-home/.claude/.credentials.json | \
      /state/agent-home/.claude/workcell | \
      /state/agent-home/.claude/workcell/* | \
      /state/agent-home/.config/claude-code/auth.json | \
      /state/agent-home/.mcp.json | \
      /state/agent-home/.gemini/settings.json | \
      /state/agent-home/.gemini/GEMINI.md | \
      /state/agent-home/.gemini/.env | \
      /state/agent-home/.gemini/oauth_creds.json | \
      /state/agent-home/.gemini/projects.json | \
      /state/agent-home/.gemini/trustedFolders.json | \
      /state/agent-home/.config/gcloud/application_default_credentials.json | \
      /state/agent-home/.config/gh/config.yml | \
      /state/agent-home/.config/gh/hosts.yml | \
      /state/agent-home/.ssh | \
      /state/agent-home/.ssh/* | \
      /state/agent-home/.codex/agents | \
      /state/agent-home/.codex/agents/* | \
      /state/agent-home/.codex/rules | \
      /state/agent-home/.codex/rules/* | \
      /state/agent-home/.codex/mcp | \
      /state/agent-home/.codex/mcp/*)
      return 1
      ;;
  esac

  return 0
}

workcell_copy_manifest_entry() {
  local source_path="$1"
  local target_path="$2"
  local kind="$3"
  local file_mode="$4"
  local dir_mode="$5"

  if ! workcell_target_is_allowed "${target_path}"; then
    workcell_die "Workcell injection target is not allowed: ${target_path}"
  fi

  case "${kind}" in
    file)
      workcell_reset_session_target "${target_path}" "injected copy"
      workcell_assert_no_symlink_path_components "${target_path}" "injected copy" 0
      cp "${source_path}" "${target_path}"
      chmod "${file_mode}" "${target_path}"
      workcell_file_trace_log_path_state "copy-injected-file" "${target_path}" "source=$(printf '%q' "${source_path}")"
      ;;
    dir)
      workcell_reset_session_target "${target_path}" "injected copy"
      workcell_assert_no_symlink_path_components "${target_path}" "injected copy" 0
      mkdir -p "${target_path}"
      cp -R "${source_path}/." "${target_path}"
      find "${target_path}" -type d -exec chmod "${dir_mode}" {} +
      find "${target_path}" -type f -exec chmod "${file_mode}" {} +
      chmod "${dir_mode}" "${target_path}"
      workcell_file_trace_log_path_state "copy-injected-dir" "${target_path}" "source=$(printf '%q' "${source_path}")"
      ;;
    *)
      workcell_die "Unsupported Workcell injection kind: ${kind}"
      ;;
  esac
}

workcell_render_provider_doc() {
  local baseline_path="$1"
  local target_path="$2"
  local provider_key="$3"
  local common_rel=""
  local provider_rel=""
  local workspace_common_doc=""
  local workspace_provider_doc=""

  if workcell_manifest_active; then
    common_rel="$(workcell_manifest_string '.documents.common // empty')"
    provider_rel="$(workcell_manifest_string ".documents.${provider_key} // empty")"
  fi

  case "${provider_key}" in
    codex)
      workspace_common_doc="$(workcell_workspace_import_path 'AGENTS.md' || true)"
      ;;
    claude)
      workspace_common_doc="$(workcell_workspace_import_path 'AGENTS.md' || true)"
      workspace_provider_doc="$(workcell_workspace_import_path 'CLAUDE.md' || true)"
      ;;
    gemini)
      workspace_common_doc="$(workcell_workspace_import_path 'AGENTS.md' || true)"
      workspace_provider_doc="$(workcell_workspace_import_path 'GEMINI.md' || true)"
      ;;
  esac

  if [[ -z "${workspace_common_doc}" ]] && [[ -z "${workspace_provider_doc}" ]] &&
    [[ -z "${common_rel}" ]] && [[ -z "${provider_rel}" ]]; then
    workcell_link_control_plane_path "${baseline_path}" "${target_path}"
    return 0
  fi

  workcell_reset_session_target "${target_path}" "provider document"
  {
    cat "${baseline_path}"
    if [[ -n "${workspace_common_doc}" ]]; then
      printf '\n\n<!-- Workcell imported workspace %s -->\n\n' "$(basename "${workspace_common_doc}")"
      cat "${workspace_common_doc}"
    fi
    if [[ -n "${workspace_provider_doc}" ]]; then
      printf '\n\n<!-- Workcell imported workspace %s -->\n\n' "$(basename "${workspace_provider_doc}")"
      cat "${workspace_provider_doc}"
    fi
    if [[ -n "${common_rel}" ]]; then
      printf '\n\n<!-- Workcell injected common instructions -->\n\n'
      cat "$(workcell_manifest_source_path "${common_rel}")"
    fi
    if [[ -n "${provider_rel}" ]]; then
      printf '\n\n<!-- Workcell injected %s instructions -->\n\n' "${provider_key}"
      cat "$(workcell_manifest_source_path "${provider_rel}")"
    fi
  } >"${target_path}"
  chmod 0444 "${target_path}"
  workcell_file_trace_log_path_state "render-provider-doc" "${target_path}" "provider=${provider_key}"$'\t'"baseline=$(printf '%q' "${baseline_path}")"
}

workcell_seed_codex_rules() {
  local baseline_rules="${ADAPTER_ROOT}/codex/.codex/rules"
  local rules_target="${CODEX_HOME}/rules"
  local default_rules_target="${rules_target}/default.rules"
  local rules_mutability=""

  rules_mutability="$(workcell_current_effective_codex_rules_mutability)"
  case "${rules_mutability}" in
    readonly)
      workcell_link_control_plane_path "${baseline_rules}" "${rules_target}"
      ;;
    session)
      if [[ ! -d "${rules_target}" ]] || [[ -L "${rules_target}" ]] || [[ ! -f "${default_rules_target}" ]]; then
        workcell_copy_control_plane_tree "${baseline_rules}" "${rules_target}" 0600 0700
      fi
      workcell_assert_session_regular_writable_file "${default_rules_target}" "Codex execpolicy session rules"
      ;;
  esac
}

workcell_apply_manifest_copies() {
  local entry_json=""
  local source_rel=""
  local mount_path=""
  local target_path=""
  local kind=""
  local file_mode=""
  local dir_mode=""

  workcell_ensure_manifest || return 0
  mkdir -p /state/injected
  chmod 0755 /state/injected 2>/dev/null || true

  while IFS= read -r entry_json; do
    source_rel="$(jq -r 'if (.source | type) == "object" then (.source.source // "") else .source end' <<<"${entry_json}")"
    mount_path="$(jq -r 'if (.source | type) == "object" then (.source.mount_path // "") else "" end' <<<"${entry_json}")"
    target_path="$(jq -r '.target' <<<"${entry_json}")"
    kind="$(jq -r '.kind' <<<"${entry_json}")"
    file_mode="$(jq -r '.file_mode' <<<"${entry_json}")"
    dir_mode="$(jq -r '.dir_mode' <<<"${entry_json}")"
    [[ -n "${source_rel}${mount_path}" ]] || continue
    workcell_copy_manifest_entry \
      "$(workcell_resolve_manifest_input_path "${source_rel}" "${mount_path}")" \
      "${target_path}" \
      "${kind}" \
      "${file_mode}" \
      "${dir_mode}"
  done < <(jq -c '.copies[]?' "$(workcell_manifest_path)")
}

workcell_apply_manifest_ssh() {
  local config_source=""
  local config_mount_path=""
  local known_hosts_source=""
  local known_hosts_mount_path=""
  local identity_source=""
  local identity_mount_path=""
  local identity_name=""

  workcell_ensure_manifest || return 0
  config_source="$(workcell_manifest_string 'if (.ssh.config | type) == "object" then (.ssh.config.source // empty) else (.ssh.config // empty) end')"
  config_mount_path="$(workcell_manifest_string 'if (.ssh.config | type) == "object" then (.ssh.config.mount_path // empty) else empty end')"
  known_hosts_source="$(workcell_manifest_string 'if (.ssh.known_hosts | type) == "object" then (.ssh.known_hosts.source // empty) else (.ssh.known_hosts // empty) end')"
  known_hosts_mount_path="$(workcell_manifest_string 'if (.ssh.known_hosts | type) == "object" then (.ssh.known_hosts.mount_path // empty) else empty end')"
  if [[ -z "${config_source}" ]] && [[ -z "${known_hosts_source}" ]] &&
    [[ "$(workcell_manifest_string '(.ssh.identities // []) | length')" == "0" ]]; then
    return 0
  fi

  workcell_prepare_session_directory "${HOME}/.ssh" "SSH home"
  chmod 0700 "${HOME}/.ssh"

  if [[ -n "${config_source}${config_mount_path}" ]]; then
    workcell_reset_session_target "${HOME}/.ssh/config" "SSH config"
    cp "$(workcell_resolve_manifest_input_path "${config_source}" "${config_mount_path}")" "${HOME}/.ssh/config"
    chmod 0600 "${HOME}/.ssh/config"
  fi

  if [[ -n "${known_hosts_source}${known_hosts_mount_path}" ]]; then
    workcell_reset_session_target "${HOME}/.ssh/known_hosts" "known_hosts"
    cp "$(workcell_resolve_manifest_input_path "${known_hosts_source}" "${known_hosts_mount_path}")" "${HOME}/.ssh/known_hosts"
    chmod 0600 "${HOME}/.ssh/known_hosts"
  fi

  while IFS= read -r entry_json; do
    identity_source="$(jq -r '.source // ""' <<<"${entry_json}")"
    identity_mount_path="$(jq -r '.mount_path // ""' <<<"${entry_json}")"
    identity_name="$(jq -r '.target_name' <<<"${entry_json}")"
    [[ -n "${identity_source}${identity_mount_path}" ]] || continue
    workcell_reset_session_target "${HOME}/.ssh/${identity_name}" "SSH identity"
    cp "$(workcell_resolve_manifest_input_path "${identity_source}" "${identity_mount_path}")" "${HOME}/.ssh/${identity_name}"
    chmod 0600 "${HOME}/.ssh/${identity_name}"
  done < <(jq -c '.ssh.identities[]?' "$(workcell_manifest_path)")
}

seed_codex_home() {
  workcell_verify_control_plane_prefix "${ADAPTER_ROOT}/codex/"
  workcell_prepare_session_directory "${CODEX_HOME}" "Codex home"
  workcell_prepare_session_directory "${CODEX_HOME}/mcp" "Codex MCP directory"
  workcell_render_provider_doc "${ADAPTER_ROOT}/codex/.codex/AGENTS.md" "${CODEX_HOME}/AGENTS.md" codex
  workcell_copy_control_plane_file "${ADAPTER_ROOT}/codex/.codex/config.toml" "${CODEX_HOME}/config.toml" 0600
  workcell_assert_session_regular_writable_file "${CODEX_HOME}/config.toml" "Codex config"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/managed_config.toml" "${CODEX_HOME}/managed_config.toml"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/requirements.toml" "${CODEX_HOME}/requirements.toml"
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/.codex/agents" "${CODEX_HOME}/agents"
  workcell_seed_codex_rules
  workcell_link_control_plane_path "${ADAPTER_ROOT}/codex/mcp/config.toml" "${CODEX_HOME}/mcp/config.toml"
  workcell_copy_manifest_credential_file codex_auth "${CODEX_HOME}/auth.json" || true
}

seed_claude_home() {
  workcell_verify_control_plane_prefix "${ADAPTER_ROOT}/claude/"
  workcell_verify_control_plane_path "/etc/claude-code/managed-settings.json"
  workcell_prepare_session_directory "${HOME}/.claude" "Claude home"
  workcell_render_claude_settings
  workcell_render_provider_doc "${ADAPTER_ROOT}/claude/CLAUDE.md" "${HOME}/.claude/CLAUDE.md" claude
  workcell_prepare_session_directory "${HOME}/.config/claude-code" "Claude auth directory"
  workcell_copy_manifest_credential_file claude_auth "${HOME}/.claude/.claude.json" || true
  workcell_copy_manifest_credential_file claude_auth "${HOME}/.claude.json" || true
  workcell_copy_manifest_credential_file claude_auth "${HOME}/.config/claude-code/auth.json" || true
  workcell_copy_manifest_credential_file claude_auth "${HOME}/.claude/.credentials.json" || true
  if ! workcell_copy_manifest_credential_file claude_mcp "${HOME}/.mcp.json"; then
    workcell_link_control_plane_path "${ADAPTER_ROOT}/claude/mcp-template.json" "${HOME}/.mcp.json"
  fi
}

seed_gemini_home() {
  local workspace_root="${WORKSPACE:-/workspace}"
  local selected_auth_type=""

  workcell_verify_control_plane_prefix "${ADAPTER_ROOT}/gemini/"
  workcell_prepare_session_directory "${HOME}/.gemini" "Gemini home"
  workcell_reset_session_target "${HOME}/.gemini/settings.json" "Gemini settings"
  cp "${ADAPTER_ROOT}/gemini/.gemini/settings.json" "${HOME}/.gemini/settings.json"
  chmod 0600 "${HOME}/.gemini/settings.json"
  if [[ "${WORKCELL_MODE:-strict}" == "breakglass" ]]; then
    workcell_set_gemini_folder_trust_enabled "${HOME}/.gemini/settings.json" true
    workcell_reset_session_target "${HOME}/.gemini/trustedFolders.json" "Gemini trusted folders"
    rm -f "${HOME}/.gemini/trustedFolders.json"
  else
    workcell_set_gemini_folder_trust_enabled "${HOME}/.gemini/settings.json" false
    workcell_reset_session_target "${HOME}/.gemini/trustedFolders.json" "Gemini trusted folders"
    workcell_render_gemini_trusted_folders "${HOME}/.gemini/trustedFolders.json" "${workspace_root}"
    chmod 0600 "${HOME}/.gemini/trustedFolders.json"
  fi
  workcell_render_provider_doc "${ADAPTER_ROOT}/gemini/GEMINI.md" "${HOME}/.gemini/GEMINI.md" gemini
  workcell_copy_manifest_credential_file gemini_env "${HOME}/.gemini/.env" || true
  workcell_validate_gemini_env_auth_config "${HOME}/.gemini/.env"
  workcell_copy_manifest_credential_file gemini_oauth "${HOME}/.gemini/oauth_creds.json" || true
  workcell_validate_gemini_oauth_config "${HOME}/.gemini/oauth_creds.json"
  workcell_prepare_session_directory "${HOME}/.config/gcloud" "Google ADC directory"
  workcell_copy_manifest_credential_file gcloud_adc "${HOME}/.config/gcloud/application_default_credentials.json" || true
  workcell_validate_gcloud_adc_config "${HOME}/.config/gcloud/application_default_credentials.json"
  selected_auth_type="$(workcell_gemini_selected_auth_type \
    "${HOME}/.gemini/.env" \
    "${HOME}/.gemini/oauth_creds.json" || true)"
  workcell_set_gemini_selected_auth_type "${HOME}/.gemini/settings.json" "${selected_auth_type}"
  if ! workcell_copy_manifest_credential_file gemini_projects "${HOME}/.gemini/projects.json"; then
    workcell_reset_session_target "${HOME}/.gemini/projects.json" "Gemini projects"
    printf '{\n  "projects": {}\n}\n' >"${HOME}/.gemini/projects.json"
    chmod 0600 "${HOME}/.gemini/projects.json"
  fi
  workcell_validate_gemini_projects_config "${HOME}/.gemini/projects.json"
}

workcell_seed_shared_credentials() {
  workcell_prepare_session_directory "${HOME}/.config/gh" "GitHub CLI config directory"
  workcell_copy_manifest_credential_file github_config "${HOME}/.config/gh/config.yml" || true
  workcell_copy_manifest_credential_file github_hosts "${HOME}/.config/gh/hosts.yml" || true
}

seed_agent_home() {
  case "$1" in
    codex)
      seed_codex_home
      ;;
    claude)
      seed_claude_home
      ;;
    gemini)
      seed_gemini_home
      ;;
    *)
      workcell_die "Unsupported agent: $1"
      ;;
  esac

  workcell_seed_shared_credentials
  workcell_apply_manifest_copies
  workcell_apply_manifest_ssh
}
