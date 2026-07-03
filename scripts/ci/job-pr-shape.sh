#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BASE_BRANCH="${WORKCELL_PR_BASE_REF:-main}"
BASE_REF=""
readonly CERTIFIED_ADAPTER_LABEL="approved-large-certified-adapter"
LABELS=()

usage() {
  cat <<'EOF'
Usage: job-pr-shape.sh [--base BRANCH] [--label LABEL]

Run the shared PR shape gate against the selected base branch.
EOF
}

add_labels_from_json() {
  local raw_json="${1:-}"
  local label=""

  [[ -n "${raw_json}" ]] || return 0
  while IFS= read -r label; do
    [[ -n "${label}" ]] || continue
    LABELS+=("${label}")
  done < <(jq -r 'if type == "array" then .[]? else empty end' <<<"${raw_json}")
}

has_label() {
  local expected="$1"
  local label=""

  for label in "${LABELS[@]}"; do
    [[ "${label}" == "${expected}" ]] && return 0
  done
  return 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --base)
      BASE_BRANCH="${2:-}"
      [[ -n "${BASE_BRANCH}" ]] || {
        echo "--base requires a branch name" >&2
        exit 2
      }
      shift 2
      ;;
    --label)
      LABELS+=("${2:-}")
      [[ -n "${LABELS[-1]}" ]] || {
        echo "--label requires a value" >&2
        exit 2
      }
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
  add_labels_from_json "${WORKCELL_PR_LABELS_JSON:-}"
fi

if git -C "${ROOT_DIR}" remote get-url origin >/dev/null 2>&1; then
  git -C "${ROOT_DIR}" fetch --no-tags --prune origin "${BASE_BRANCH}"
  BASE_REF="origin/${BASE_BRANCH}"
elif git -C "${ROOT_DIR}" rev-parse --verify --quiet "refs/heads/${BASE_BRANCH}" >/dev/null; then
  BASE_REF="${BASE_BRANCH}"
else
  echo "Unable to resolve base branch ${BASE_BRANCH} locally or from origin." >&2
  exit 2
fi

shape_args=(
  --base-ref "${BASE_REF}"
  --head-ref HEAD
  --max-files 25
  --max-lines 1200
  --max-areas 8
  --max-binaries 0
)
if has_label "${CERTIFIED_ADAPTER_LABEL}"; then
  shape_args+=(--allow-certified-adapter-shape)
fi

"${ROOT_DIR}/scripts/check-pr-shape.sh" "${shape_args[@]}"
