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

  commit="$(git -C "${repo_root}" rev-parse --verify --quiet "${ref}^{commit}" || true)"
  if [[ -z "${commit}" ]]; then
    echo "Unable to resolve commit for ref: ${ref}" >&2
    exit 2
  fi
  printf '%s\n' "${commit}"
}

default_base_ref() {
  local repo_root="$1"

  if git -C "${repo_root}" rev-parse --verify --quiet "refs/remotes/origin/main^{commit}" >/dev/null 2>&1; then
    printf '%s\n' "refs/remotes/origin/main"
    return 0
  fi
  if git -C "${repo_root}" rev-parse --verify --quiet "refs/heads/main^{commit}" >/dev/null 2>&1; then
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

REPO_ROOT="$(cd "${REPO_ROOT}" && pwd -P)"
if ! git -C "${REPO_ROOT}" rev-parse --show-toplevel >/dev/null 2>&1; then
  echo "check-pr-shape requires a git worktree: ${REPO_ROOT}" >&2
  exit 2
fi

if [[ -z "${BASE_REF}" ]]; then
  BASE_REF="$(default_base_ref "${REPO_ROOT}")"
fi

base_commit="$(resolve_commit_or_die "${REPO_ROOT}" "${BASE_REF}")"
head_commit="$(resolve_commit_or_die "${REPO_ROOT}" "${HEAD_REF}")"
merge_base="$(git -C "${REPO_ROOT}" merge-base "${base_commit}" "${head_commit}")"

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
done < <(git -C "${REPO_ROOT}" diff --numstat --find-renames "${merge_base}..${head_commit}")

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
