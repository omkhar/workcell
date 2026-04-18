#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="${ROOT_DIR}"
BASE_REF=""
HEAD_REF="HEAD"
readonly DEFAULT_MAX_FILES=25
readonly DEFAULT_MAX_LINES=1200
readonly DEFAULT_MAX_AREAS=8
readonly DEFAULT_MAX_BINARY_FILES=0
MAX_FILES="${DEFAULT_MAX_FILES}"
MAX_LINES="${DEFAULT_MAX_LINES}"
MAX_AREAS="${DEFAULT_MAX_AREAS}"
MAX_BINARY_FILES="${DEFAULT_MAX_BINARY_FILES}"
TRUSTED_HOST_PATH=""
HOST_GIT_BIN=""
REAL_HOME=""

build_trusted_host_path() {
  local dir=""
  local path=""

  for dir in /opt/homebrew/bin /usr/local/bin /usr/bin /bin /usr/sbin /sbin; do
    [[ -d "${dir}" ]] || continue
    if [[ -z "${path}" ]]; then
      path="${dir}"
    else
      path="${path}:${dir}"
    fi
  done
  printf '%s\n' "${path}"
}

resolve_fixed_host_tool() {
  local name="$1"
  shift
  local candidate=""

  for candidate in "$@"; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done

  echo "Missing trusted host tool: ${name}" >&2
  exit 1
}

run_clean_host_command_in_dir() {
  local dir="$1"
  shift

  [[ "$#" -gt 0 ]] || return 0
  if [[ ! -d "${dir}" ]]; then
    echo "Missing host working directory: ${dir}" >&2
    exit 2
  fi

  (
    cd "${dir}" &&
      env -i \
        PATH="${TRUSTED_HOST_PATH}" \
        HOME="${REAL_HOME}" \
        LC_ALL=C \
        LANG=C \
        "$@"
  )
}

workspace_is_git_worktree() {
  local workspace="$1"

  run_clean_host_command_in_dir "${workspace}" \
    "${HOST_GIT_BIN}" rev-parse --is-inside-work-tree >/dev/null 2>&1
}

workspace_git_filter_override_args() {
  local workspace="$1"
  local key=""

  workspace_is_git_worktree "${workspace}" || return 0
  while IFS= read -r key; do
    [[ -n "${key}" ]] || continue
    case "${key}" in
      filter.*.clean | filter.*.smudge | filter.*.process)
        printf -- '-c\0%s=\0' "${key}"
        ;;
      filter.*.required)
        printf -- '-c\0%s=false\0' "${key}"
        ;;
    esac
  done < <(
    run_clean_host_command_in_dir "${workspace}" env \
      GIT_CONFIG_NOSYSTEM=1 \
      GIT_CONFIG_SYSTEM=/dev/null \
      GIT_CONFIG_GLOBAL=/dev/null \
      "${HOST_GIT_BIN}" config --name-only --includes \
      --get-regexp '^filter\..*\.(clean|smudge|process|required)$' 2>/dev/null || true
  )
}

run_workspace_safe_git_command_in_dir() {
  local workspace="$1"
  shift
  local -a filter_overrides=()
  local -a git_command=()
  local override=""

  while IFS= read -r -d '' override; do
    filter_overrides+=("${override}")
  done < <(workspace_git_filter_override_args "${workspace}")

  git_command=(
    env
    GIT_CONFIG_NOSYSTEM=1
    GIT_CONFIG_SYSTEM=/dev/null
    GIT_CONFIG_GLOBAL=/dev/null
    "${HOST_GIT_BIN}"
    -c core.hooksPath=/dev/null
    -c core.fsmonitor=false
    -c diff.external=
    -c color.ui=false
  )
  if [[ ${#filter_overrides[@]} -gt 0 ]]; then
    git_command+=("${filter_overrides[@]}")
  fi
  git_command+=("$@")

  run_clean_host_command_in_dir "${workspace}" "${git_command[@]}"
}

usage() {
  cat <<'EOF'
Usage: check-pr-shape.sh [options]

Options:
  --repo-root PATH   Repository to inspect (default: script repo root)
  --base-ref REF     Base ref to compare against (default: origin/main or main)
  --head-ref REF     Head ref to compare (default: HEAD)
  --max-files N      Maximum changed files allowed (default: 25)
  --max-lines N      Maximum added+deleted lines allowed (default: 1200)
  --max-areas N      Maximum top-level areas allowed (default: 8)
  --max-binaries N   Maximum binary files allowed (default: 0)
  -h, --help         Show this help text
EOF
}

option_value_or_die() {
  local flag="$1"
  local value="${2:-}"

  if [[ -z "${value}" ]]; then
    echo "${flag} requires a value." >&2
    exit 2
  fi
  printf '%s\n' "${value}"
}

require_integer() {
  local label="$1"
  local value="$2"

  if [[ ! "${value}" =~ ^[0-9]+$ ]]; then
    echo "${label} must be a non-negative integer: ${value}" >&2
    exit 2
  fi
}

resolve_commit_or_die() {
  local repo_root="$1"
  local ref="$2"
  local commit=""

  commit="$(run_workspace_safe_git_command_in_dir "${repo_root}" rev-parse --verify --quiet "${ref}^{commit}" || true)"
  if [[ -z "${commit}" ]]; then
    echo "Unable to resolve commit for ref: ${ref}" >&2
    exit 2
  fi
  printf '%s\n' "${commit}"
}

default_base_ref() {
  local repo_root="$1"

  if run_workspace_safe_git_command_in_dir "${repo_root}" rev-parse --verify --quiet "refs/remotes/origin/main^{commit}" >/dev/null 2>&1; then
    printf '%s\n' "refs/remotes/origin/main"
    return 0
  fi
  if run_workspace_safe_git_command_in_dir "${repo_root}" rev-parse --verify --quiet "refs/heads/main^{commit}" >/dev/null 2>&1; then
    printf '%s\n' "refs/heads/main"
    return 0
  fi

  echo "Unable to determine a default base ref. Use --base-ref." >&2
  exit 2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-root)
      REPO_ROOT="$(option_value_or_die "$1" "${2-}")"
      shift 2
      ;;
    --base-ref)
      BASE_REF="$(option_value_or_die "$1" "${2-}")"
      shift 2
      ;;
    --head-ref)
      HEAD_REF="$(option_value_or_die "$1" "${2-}")"
      shift 2
      ;;
    --max-files)
      MAX_FILES="$(option_value_or_die "$1" "${2-}")"
      shift 2
      ;;
    --max-lines)
      MAX_LINES="$(option_value_or_die "$1" "${2-}")"
      shift 2
      ;;
    --max-areas)
      MAX_AREAS="$(option_value_or_die "$1" "${2-}")"
      shift 2
      ;;
    --max-binaries)
      MAX_BINARY_FILES="$(option_value_or_die "$1" "${2-}")"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unsupported check-pr-shape option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_integer "--max-files" "${MAX_FILES}"
require_integer "--max-lines" "${MAX_LINES}"
require_integer "--max-areas" "${MAX_AREAS}"
require_integer "--max-binaries" "${MAX_BINARY_FILES}"
TRUSTED_HOST_PATH="$(build_trusted_host_path)"
HOST_GIT_BIN="$(resolve_fixed_host_tool git /opt/homebrew/bin/git /usr/local/bin/git /usr/bin/git /bin/git)"
REAL_HOME="${HOME:-/}"
if [[ ! -d "${REAL_HOME}" ]]; then
  REAL_HOME="/"
fi

REPO_ROOT="$(cd "${REPO_ROOT}" && pwd -P)"
if ! run_workspace_safe_git_command_in_dir "${REPO_ROOT}" rev-parse --show-toplevel >/dev/null 2>&1; then
  echo "check-pr-shape requires a git worktree: ${REPO_ROOT}" >&2
  exit 2
fi

if [[ -z "${BASE_REF}" ]]; then
  BASE_REF="$(default_base_ref "${REPO_ROOT}")"
fi

base_commit="$(resolve_commit_or_die "${REPO_ROOT}" "${BASE_REF}")"
head_commit="$(resolve_commit_or_die "${REPO_ROOT}" "${HEAD_REF}")"
merge_base="$(run_workspace_safe_git_command_in_dir "${REPO_ROOT}" merge-base "${base_commit}" "${head_commit}")"

changed_files=0
changed_lines=0
binary_files=0
changed_areas_text=""

while IFS=$'\t' read -r additions deletions path; do
  [[ -n "${path:-}" ]] || continue
  changed_files=$((changed_files + 1))
  if [[ "${additions}" == "-" || "${deletions}" == "-" ]]; then
    binary_files=$((binary_files + 1))
  else
    changed_lines=$((changed_lines + additions + deletions))
  fi
  if [[ "${path}" == */* ]]; then
    area="${path%%/*}"
  else
    area="repo-root"
  fi
  if ! grep -Fxq -- "${area}" <<<"${changed_areas_text}"; then
    changed_areas_text+="${area}"$'\n'
  fi
done < <(run_workspace_safe_git_command_in_dir "${REPO_ROOT}" diff --numstat --find-renames "${merge_base}..${head_commit}")

changed_area_count="$(printf '%s' "${changed_areas_text}" | grep -c . || true)"

if ((changed_files == 0)); then
  printf 'PR shape check passed: no committed diff between %s and %s.\n' "${BASE_REF}" "${HEAD_REF}"
  exit 0
fi

if ((changed_files > MAX_FILES || changed_lines > MAX_LINES || changed_area_count > MAX_AREAS || binary_files > MAX_BINARY_FILES)); then
  echo "PR shape check failed: the diff is too broad for a single reviewable PR." >&2
  printf '  base_ref=%s\n' "${BASE_REF}" >&2
  printf '  head_ref=%s\n' "${HEAD_REF}" >&2
  printf '  changed_files=%d (limit=%d)\n' "${changed_files}" "${MAX_FILES}" >&2
  printf '  changed_lines=%d (limit=%d)\n' "${changed_lines}" "${MAX_LINES}" >&2
  printf '  changed_areas=%d (limit=%d)\n' "${changed_area_count}" "${MAX_AREAS}" >&2
  printf '  binary_files=%d (limit=%d)\n' "${binary_files}" "${MAX_BINARY_FILES}" >&2
  printf '  areas=%s\n' "$(printf '%s' "${changed_areas_text}" | sort | paste -sd, -)" >&2
  echo "Split unrelated fixes, opportunistic cleanup, or separate reviewer-sized concerns before publishing." >&2
  exit 2
fi

printf 'PR shape check passed: files=%d lines=%d areas=%d binary_files=%d\n' \
  "${changed_files}" \
  "${changed_lines}" \
  "${changed_area_count}" \
  "${binary_files}"
