#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST_PATH="${WORKCELL_WORKFLOW_LANE_MANIFEST_PATH:-${ROOT_DIR}/policy/workflow-lanes.json}"
PROFILE="pr-parity"
EVENT="pull_request"
BASE_BRANCH="main"
OUTPUT_FORMAT="text"
AUTO_CHANGED_FILES=1
LABELS=()
CHANGED_FILES=()

usage() {
  cat <<'EOF'
Usage: ci-plan.sh [options]

Options:
  --profile repo-core|pr-parity|release-preflight
  --event EVENT               Planner event for pr-parity (default: pull_request)
  --base BRANCH               Base branch for diff planning (default: main)
  --label LABEL               Repeatable PR label input
  --changed-file PATH         Repeatable explicit changed-file input
  --no-auto-changed-files     Do not derive changed files from git
  --format text|json          Output format (default: text)
  -h, --help                  Show this help
EOF
}

json_array_from_values() {
  if (($# == 0)); then
    jq -n '[]'
    return 0
  fi
  printf '%s\n' "$@" | jq -R . | jq -s .
}

resolve_base_ref() {
  if git -C "${ROOT_DIR}" remote get-url origin >/dev/null 2>&1; then
    git -C "${ROOT_DIR}" fetch --no-tags --prune origin "${BASE_BRANCH}" >/dev/null 2>&1 || true
    if git -C "${ROOT_DIR}" rev-parse --verify --quiet "refs/remotes/origin/${BASE_BRANCH}" >/dev/null; then
      printf 'refs/remotes/origin/%s\n' "${BASE_BRANCH}"
      return 0
    fi
  fi
  if git -C "${ROOT_DIR}" rev-parse --verify --quiet "refs/heads/${BASE_BRANCH}" >/dev/null; then
    printf 'refs/heads/%s\n' "${BASE_BRANCH}"
    return 0
  fi
  printf ''
}

collect_changed_files_from_git() {
  local base_ref=""

  base_ref="$(resolve_base_ref)"
  if [[ -n "${base_ref}" ]]; then
    git -C "${ROOT_DIR}" diff --name-only "${base_ref}...HEAD" || true
  fi
  git -C "${ROOT_DIR}" diff --name-only || true
  git -C "${ROOT_DIR}" diff --cached --name-only || true
  git -C "${ROOT_DIR}" ls-files --others --exclude-standard || true
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile)
      PROFILE="${2:-}"
      [[ -n "${PROFILE}" ]] || {
        echo "--profile requires a value" >&2
        exit 2
      }
      shift 2
      ;;
    --event)
      EVENT="${2:-}"
      [[ -n "${EVENT}" ]] || {
        echo "--event requires a value" >&2
        exit 2
      }
      shift 2
      ;;
    --base)
      BASE_BRANCH="${2:-}"
      [[ -n "${BASE_BRANCH}" ]] || {
        echo "--base requires a value" >&2
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
    --changed-file)
      CHANGED_FILES+=("${2:-}")
      [[ -n "${CHANGED_FILES[-1]}" ]] || {
        echo "--changed-file requires a value" >&2
        exit 2
      }
      AUTO_CHANGED_FILES=0
      shift 2
      ;;
    --no-auto-changed-files)
      AUTO_CHANGED_FILES=0
      shift
      ;;
    --format)
      OUTPUT_FORMAT="${2:-}"
      [[ -n "${OUTPUT_FORMAT}" ]] || {
        echo "--format requires a value" >&2
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

if [[ "${AUTO_CHANGED_FILES}" -eq 1 ]]; then
  while IFS= read -r path; do
    [[ -n "${path}" ]] || continue
    CHANGED_FILES+=("${path}")
  done < <(collect_changed_files_from_git)
fi

tmp_config="$(mktemp "${TMPDIR:-/tmp}/workcell-ci-plan.XXXXXX.json")"
trap 'rm -f "${tmp_config}"' EXIT

labels_json="$(json_array_from_values "${LABELS[@]}")"
changed_json="$(json_array_from_values "${CHANGED_FILES[@]}")"
jq -n \
  --arg profile "${PROFILE}" \
  --arg event "${EVENT}" \
  --arg base "${BASE_BRANCH}" \
  --argjson labels "${labels_json}" \
  --argjson changed_files "${changed_json}" \
  '{
    profile: $profile,
    event: $event,
    base_branch: $base,
    labels: $labels,
    changed_files: $changed_files
  }' >"${tmp_config}"

plan_json="$(
  cd "${ROOT_DIR}" &&
    go run ./cmd/workcell-metadatautil plan-workflow-lanes "${MANIFEST_PATH}" "${tmp_config}"
)"

case "${OUTPUT_FORMAT}" in
  json)
    printf '%s\n' "${plan_json}"
    ;;
  text)
    jq -r '
      [
        "STATUS\tLANE\tDETAIL",
        (.lanes[] | [
          .status,
          .id,
          (.local_script // .reason // .github_only_reason // "")
        ] | @tsv)
      ] | .[]
    ' <<<"${plan_json}"
    ;;
  *)
    echo "Unsupported ci-plan output format: ${OUTPUT_FORMAT}" >&2
    exit 2
    ;;
esac
