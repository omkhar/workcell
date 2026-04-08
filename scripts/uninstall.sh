#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

readonly TRUSTED_HOST_PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
export PATH="${TRUSTED_HOST_PATH}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${WORKCELL_GO_BIN:-}"

DRY_RUN=0

usage() {
  cat <<'EOF'
Usage: ./scripts/uninstall.sh [--dry-run]

Remove Workcell-owned local install links and managed host state:
  - ~/.local/bin/workcell
  - ~/.local/share/man/man1/workcell.1
  - ~/.local/state/workcell
  - ~/.colima/workcell-* profiles, matching _lima dirs, matching _lima/_disks dirs, and Workcell locks
  - ~/Library/Caches/colima/workcell-host-inputs
  - ~/Library/Caches/colima/workcell-shadow
  - /tmp/workcell-docker.* and /tmp/workcell-*.log.*

Preserved on purpose:
  - ~/.config/workcell/*
  - user-specified debug log, file trace log, and transcript files
  - unrelated Colima profiles and caches
EOF
}

resolve_go_bin() {
  if [[ -n "${GO_BIN}" && -x "${GO_BIN}" ]]; then
    return 0
  fi
  if GO_BIN="$(command -v go 2>/dev/null)"; then
    return 0
  fi
  for candidate in \
    /opt/homebrew/bin/go \
    /usr/local/go/bin/go \
    /usr/local/bin/go \
    /usr/bin/go; do
    if [[ -x "${candidate}" ]]; then
      GO_BIN="${candidate}"
      return 0
    fi
  done
  echo "Missing required tool: go" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run)
      DRY_RUN=1
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unsupported uninstall option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

resolve_go_bin
REAL_HOME="$(cd "${ROOT_DIR}" && "${GO_BIN}" run ./cmd/workcell-hostutil path home)"
COLIMA_HOME="${REAL_HOME}/.colima"
INSTALL_PATH="${REAL_HOME}/.local/bin/workcell"
MAN_PATH="${REAL_HOME}/.local/share/man/man1/workcell.1"
STATE_ROOT="${REAL_HOME}/.local/state/workcell"
INJECTION_ROOT="${REAL_HOME}/Library/Caches/colima/workcell-host-inputs"
SHADOW_ROOT="${REAL_HOME}/Library/Caches/colima/workcell-shadow"

declare -a PROFILE_NAMES=()
declare -a TEMP_ROOTS=()
REMOVED_COUNT=0

append_unique_value() {
  local value="$1"
  local existing=""

  [[ -n "${value}" ]] || return 0
  if [[ ${#PROFILE_NAMES[@]} -gt 0 ]]; then
    for existing in "${PROFILE_NAMES[@]}"; do
      if [[ "${existing}" == "${value}" ]]; then
        return 0
      fi
    done
  fi
  PROFILE_NAMES+=("${value}")
}

append_unique_temp_root() {
  local value="$1"
  local existing=""

  [[ -n "${value}" ]] || return 0
  if [[ ${#TEMP_ROOTS[@]} -gt 0 ]]; then
    for existing in "${TEMP_ROOTS[@]}"; do
      if [[ "${existing}" == "${value}" ]]; then
        return 0
      fi
    done
  fi
  TEMP_ROOTS+=("${value}")
}

validate_profile_name() {
  local profile="$1"

  [[ "${profile}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] || return 1
  case "${profile}" in
    . | ..)
      return 1
      ;;
  esac
  return 0
}

log_action() {
  local action="$1"
  local target="$2"

  printf '%s %s\n' "${action}" "${target}"
}

prepare_directory_tree_for_removal() {
  local target="$1"

  [[ -d "${target}" ]] || return 0
  [[ ! -L "${target}" ]] || return 0

  if command -v chflags >/dev/null 2>&1; then
    chflags -R nouchg,noschg "${target}" 2>/dev/null || true
  fi
  find "${target}" -exec chmod u+rwx {} + 2>/dev/null || true
}

remove_path() {
  local target="$1"

  [[ -e "${target}" || -L "${target}" ]] || return 0
  if [[ "${DRY_RUN}" -eq 1 ]]; then
    log_action "Would remove" "${target}"
  else
    prepare_directory_tree_for_removal "${target}"
    if ! rm -rf "${target}" 2>/dev/null; then
      prepare_directory_tree_for_removal "${target}"
      rm -rf "${target}"
    fi
    log_action "Removed" "${target}"
  fi
  REMOVED_COUNT=$((REMOVED_COUNT + 1))
}

remove_path_best_effort() {
  local target="$1"

  [[ -e "${target}" || -L "${target}" ]] || return 0
  if [[ "${DRY_RUN}" -eq 1 ]]; then
    log_action "Would remove" "${target}"
    REMOVED_COUNT=$((REMOVED_COUNT + 1))
    return 0
  fi

  prepare_directory_tree_for_removal "${target}"
  if ! rm -rf "${target}" 2>/dev/null; then
    prepare_directory_tree_for_removal "${target}"
    if ! rm -rf "${target}" 2>/dev/null; then
      preserve_path "active temp root" "${target}"
      return 0
    fi
  fi

  log_action "Removed" "${target}"
  REMOVED_COUNT=$((REMOVED_COUNT + 1))
}

preserve_path() {
  local label="$1"
  local target="$2"

  printf 'Preserved %s %s\n' "${label}" "${target}"
}

remove_workcell_symlink() {
  local path="$1"
  local expected_suffix="$2"
  local label="$3"
  local target=""

  if [[ ! -L "${path}" ]]; then
    if [[ -e "${path}" ]]; then
      preserve_path "non-symlink ${label}" "${path}"
    fi
    return 0
  fi

  target="$(readlink "${path}" || true)"
  case "${target}" in
    */"${expected_suffix}")
      remove_path "${path}"
      ;;
    *)
      preserve_path "${label} symlink not owned by Workcell (${target})" "${path}"
      ;;
  esac
}

remove_workcell_launcher_install() {
  local path="$1"

  if [[ -L "${path}" ]]; then
    remove_workcell_symlink "${path}" "scripts/workcell" "launcher"
    return 0
  fi

  if [[ ! -f "${path}" ]]; then
    return 0
  fi

  if grep -q '^# Workcell debug installer wrapper$' "${path}" 2>/dev/null; then
    remove_path "${path}"
  else
    preserve_path "non-Workcell launcher" "${path}"
  fi
}

resolve_host_tool_optional() {
  local name="$1"
  shift
  local candidate=""

  for candidate in "$@"; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done

  return 1
}

delete_managed_profile() {
  local profile="$1"
  local colima_bin="$2"
  local profile_root="${COLIMA_HOME}/${profile}"
  local lima_root="${COLIMA_HOME}/_lima/colima-${profile}"

  validate_profile_name "${profile}" || {
    preserve_path "unsafe profile name" "${profile}"
    return 0
  }

  if [[ -n "${colima_bin}" ]] &&
    { [[ -f "${profile_root}/colima.yaml" ]] || [[ -f "${lima_root}/lima.yaml" ]]; }; then
    if [[ "${DRY_RUN}" -eq 1 ]]; then
      log_action "Would run" "${colima_bin} delete --profile ${profile} --force"
    else
      env -i \
        PATH="${TRUSTED_HOST_PATH}" \
        HOME="${REAL_HOME}" \
        COLIMA_HOME="${COLIMA_HOME}" \
        "${colima_bin}" delete --profile "${profile}" --force >/dev/null 2>&1 || true
    fi
  fi

  remove_path "${profile_root}"
  remove_path "${lima_root}"
  remove_path "${COLIMA_HOME}/locks/${profile}.lock"
}

collect_profiles() {
  local candidate=""
  local name=""

  if [[ -d "${COLIMA_HOME}" ]]; then
    while IFS= read -r -d '' candidate; do
      name="$(basename "${candidate}")"
      case "${name}" in
        _lima | locks)
          continue
          ;;
      esac
      if [[ -f "${candidate}/workcell.managed" ]] || [[ "${name}" == workcell-* ]]; then
        append_unique_value "${name}"
      fi
    done < <(find "${COLIMA_HOME}" -mindepth 1 -maxdepth 1 -type d -print0 2>/dev/null)

    if [[ -d "${COLIMA_HOME}/_lima" ]]; then
      while IFS= read -r -d '' candidate; do
        name="$(basename "${candidate}")"
        case "${name}" in
          colima-workcell-*)
            append_unique_value "${name#colima-}"
            ;;
        esac
      done < <(find "${COLIMA_HOME}/_lima" -mindepth 1 -maxdepth 1 -type d -print0 2>/dev/null)
    fi

    if [[ -d "${COLIMA_HOME}/locks" ]]; then
      while IFS= read -r -d '' candidate; do
        name="$(basename "${candidate}")"
        case "${name}" in
          workcell-*.lock)
            append_unique_value "${name%.lock}"
            ;;
        esac
      done < <(find "${COLIMA_HOME}/locks" -mindepth 1 -maxdepth 1 -type d -print0 2>/dev/null)
    fi
  fi
}

cleanup_temp_root() {
  local temp_root="$1"
  local candidate=""
  local -a patterns=(
    "workcell-docker.*"
    "workcell-audit-log.*"
    "workcell-audit-merged.*"
    "workcell-*.log.*"
  )
  local pattern=""
  local nullglob_was_set=0

  [[ -d "${temp_root}" ]] || return 0

  if shopt -q nullglob; then
    nullglob_was_set=1
  fi
  shopt -s nullglob

  for pattern in "${patterns[@]}"; do
    for candidate in "${temp_root}"/${pattern}; do
      [[ -O "${candidate}" ]] || continue
      remove_path_best_effort "${candidate}"
    done
  done

  if [[ "${nullglob_was_set}" -eq 0 ]]; then
    shopt -u nullglob
  fi
}

COLIMA_BIN="$(resolve_host_tool_optional colima /opt/homebrew/bin/colima /usr/local/bin/colima || true)"

collect_profiles

if [[ ${#PROFILE_NAMES[@]} -gt 0 ]]; then
  for profile in "${PROFILE_NAMES[@]}"; do
    delete_managed_profile "${profile}" "${COLIMA_BIN}"
  done
fi

remove_workcell_launcher_install "${INSTALL_PATH}"
remove_workcell_symlink "${MAN_PATH}" "man/workcell.1" "man page"

remove_path "${STATE_ROOT}"
remove_path "${INJECTION_ROOT}"
remove_path "${SHADOW_ROOT}"

append_unique_temp_root "/tmp"
append_unique_temp_root "${TMPDIR:-}"
if [[ ${#TEMP_ROOTS[@]} -gt 0 ]]; then
  for temp_root in "${TEMP_ROOTS[@]}"; do
    cleanup_temp_root "${temp_root}"
  done
fi

if [[ "${REMOVED_COUNT}" -eq 0 ]]; then
  echo "No Workcell-owned install links or managed host state found."
else
  if [[ "${DRY_RUN}" -eq 1 ]]; then
    echo "Dry run completed. No files were removed."
  else
    printf 'Removed %d Workcell path(s).\n' "${REMOVED_COUNT}"
  fi
fi

echo "Preserved ~/.config/workcell and any user-specified debug/file-trace/transcript files."
