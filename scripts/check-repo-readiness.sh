#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO=""
BASE_BRANCH="main"
OUTPUT_FORMAT="text"
WATCH=0
TIMEOUT_SECONDS=2700
POLL_SECONDS=30
FETCH=1

BLOCKERS=()
REPO_READINESS="blocked"
LOCAL_STATUS=""
LOCAL_HEAD=""
LOCAL_BASE_OID=""
REMOTE_MAIN_SHA=""
LOCAL_BRANCHES=""
REMOTE_HEADS=""
OPEN_PRS=0
DEPENDABOT_OPEN=0
CODE_SCANNING_OPEN=0
SECRET_SCANNING_OPEN=0
CHECK_RUNS_TOTAL=0
CHECK_RUNS_INCOMPLETE=0
CHECK_RUNS_FAILING=0
ACTIVE_RUNS=0
TRACKER_ISSUE=""
TRACKER_STATE=""
TRACKER_CURRENT="false"

usage() {
  cat <<'EOF'
Usage: check-repo-readiness.sh [options]

Read-only host-side readiness gate for Workcell repository follow-up.

Options:
  --repo OWNER/REPO       GitHub repository (default: gh repo view)
  --base BRANCH           Base branch to audit (default: main)
  --format text|json      Output format (default: text)
  --watch                 Poll until ready or timeout
  --timeout DURATION      Watch timeout, e.g. 45m or 2700s (default: 45m)
  --poll DURATION         Watch poll interval (default: 30s)
  --no-fetch              Do not fetch/prune origin before local checks
  -h, --help              Show this help
EOF
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

parse_duration_seconds() {
  local value="$1"

  case "${value}" in
    *m)
      value="${value%m}"
      [[ "${value}" =~ ^[0-9]+$ ]] || {
        echo "Invalid duration: $1" >&2
        exit 2
      }
      printf '%s\n' "$((value * 60))"
      ;;
    *s)
      value="${value%s}"
      [[ "${value}" =~ ^[0-9]+$ ]] || {
        echo "Invalid duration: $1" >&2
        exit 2
      }
      printf '%s\n' "${value}"
      ;;
    *)
      [[ "${value}" =~ ^[0-9]+$ ]] || {
        echo "Invalid duration: $1" >&2
        exit 2
      }
      printf '%s\n' "${value}"
      ;;
  esac
}

json_array_from_values() {
  if (($# == 0)); then
    jq -n '[]'
    return 0
  fi
  printf '%s\n' "$@" | jq -R . | jq -s .
}

add_blocker() {
  BLOCKERS+=("$1")
}

alert_count() {
  local path="$1"

  gh api --paginate "${path}?state=open&per_page=100" | jq -s 'add | length'
}

active_run_count() {
  local status=""
  local runs_json="[]"
  local status_json=""

  for status in queued in_progress waiting requested pending; do
    status_json="$(gh run list --repo "${REPO}" --status "${status}" --limit 100 \
      --json databaseId,name,headBranch,headSha,status,conclusion,createdAt,url)"
    runs_json="$(jq -s 'add' <(printf '%s\n' "${runs_json}") <(printf '%s\n' "${status_json}"))"
  done

  jq 'unique_by(.databaseId) | length' <<<"${runs_json}"
}

collect_once() {
  local check_runs_json=""
  local tracker_count=0
  local tracker_body=""
  local unexpected_local_branches=""
  local unexpected_remote_heads=""

  BLOCKERS=()
  LOCAL_STATUS="$(git -C "${ROOT_DIR}" status --short --branch)"
  if [[ "${FETCH}" -eq 1 ]]; then
    git -C "${ROOT_DIR}" fetch origin --prune >/dev/null
  fi

  LOCAL_HEAD="$(git -C "${ROOT_DIR}" rev-parse HEAD)"
  LOCAL_BASE_OID="$(git -C "${ROOT_DIR}" rev-parse "refs/remotes/origin/${BASE_BRANCH}")"
  REMOTE_MAIN_SHA="$(gh api "repos/${REPO}/commits/${BASE_BRANCH}" --jq .sha)"
  LOCAL_BRANCHES="$(git -C "${ROOT_DIR}" branch --format='%(refname:short)')"
  REMOTE_HEADS="$(git -C "${ROOT_DIR}" ls-remote --heads origin)"

  if [[ "${LOCAL_STATUS}" != "## ${BASE_BRANCH}...origin/${BASE_BRANCH}" ]]; then
    add_blocker "local checkout is not clean on ${BASE_BRANCH} tracking origin/${BASE_BRANCH}"
  fi
  if [[ "${LOCAL_HEAD}" != "${REMOTE_MAIN_SHA}" || "${LOCAL_BASE_OID}" != "${REMOTE_MAIN_SHA}" ]]; then
    add_blocker "local ${BASE_BRANCH} and origin/${BASE_BRANCH} are not at hosted ${BASE_BRANCH} ${REMOTE_MAIN_SHA}"
  fi

  unexpected_local_branches="$(awk -v base="${BASE_BRANCH}" '$0 != base {print}' <<<"${LOCAL_BRANCHES}")"
  if [[ -n "${unexpected_local_branches}" ]]; then
    add_blocker "unexpected local branches remain"
  fi
  unexpected_remote_heads="$(awk -v base="refs/heads/${BASE_BRANCH}" '$2 != base {print}' <<<"${REMOTE_HEADS}")"
  if [[ -n "${unexpected_remote_heads}" ]]; then
    add_blocker "unexpected remote branches remain"
  fi

  OPEN_PRS="$(gh pr list --repo "${REPO}" --state open --limit 100 --json number --jq 'length')"
  [[ "${OPEN_PRS}" == "0" ]] || add_blocker "open pull requests remain"

  DEPENDABOT_OPEN="$(alert_count "repos/${REPO}/dependabot/alerts")"
  CODE_SCANNING_OPEN="$(alert_count "repos/${REPO}/code-scanning/alerts")"
  SECRET_SCANNING_OPEN="$(alert_count "repos/${REPO}/secret-scanning/alerts")"
  [[ "${DEPENDABOT_OPEN}" == "0" ]] || add_blocker "open Dependabot alerts remain"
  [[ "${CODE_SCANNING_OPEN}" == "0" ]] || add_blocker "open code scanning alerts remain"
  [[ "${SECRET_SCANNING_OPEN}" == "0" ]] || add_blocker "open secret scanning alerts remain"

  check_runs_json="$(gh api "repos/${REPO}/commits/${REMOTE_MAIN_SHA}/check-runs?per_page=100")"
  CHECK_RUNS_TOTAL="$(jq '.total_count' <<<"${check_runs_json}")"
  if [[ "${CHECK_RUNS_TOTAL}" -gt 100 ]]; then
    add_blocker "current main has more than 100 check runs; readiness script needs pagination"
  fi
  CHECK_RUNS_INCOMPLETE="$(jq '[.check_runs[] | select(.status != "completed")] | length' <<<"${check_runs_json}")"
  CHECK_RUNS_FAILING="$(jq '[.check_runs[] | select(.status == "completed" and (.conclusion | IN("success","skipped","neutral") | not))] | length' <<<"${check_runs_json}")"
  [[ "${CHECK_RUNS_INCOMPLETE}" == "0" ]] || add_blocker "current main has incomplete check runs"
  [[ "${CHECK_RUNS_FAILING}" == "0" ]] || add_blocker "current main has failing check runs"

  ACTIVE_RUNS="$(active_run_count)"
  [[ "${ACTIVE_RUNS}" == "0" ]] || add_blocker "queued, pending, waiting, requested, or in-progress workflow runs remain"

  tracker_count="$(gh issue list --repo "${REPO}" --state all --label upstream-refresh-candidate --limit 10 --json number --jq 'length')"
  if [[ "${tracker_count}" != "1" ]]; then
    TRACKER_ISSUE=""
    TRACKER_STATE=""
    TRACKER_CURRENT="false"
    add_blocker "expected exactly one upstream refresh tracker issue"
  else
    TRACKER_ISSUE="$(gh issue list --repo "${REPO}" --state all --label upstream-refresh-candidate --limit 10 --json number --jq '.[0].number')"
    tracker_json="$(gh issue view "${TRACKER_ISSUE}" --repo "${REPO}" --json state,body)"
    TRACKER_STATE="$(jq -r '.state' <<<"${tracker_json}")"
    tracker_body="$(jq -r '.body' <<<"${tracker_json}")"
    if grep -Fq "${REMOTE_MAIN_SHA}" <<<"${tracker_body}"; then
      TRACKER_CURRENT="true"
    else
      TRACKER_CURRENT="false"
      add_blocker "upstream refresh tracker does not reference current main"
    fi
  fi

  if ((${#BLOCKERS[@]} == 0)); then
    REPO_READINESS="ready"
  else
    REPO_READINESS="blocked"
  fi
}

emit_result() {
  local blockers_json=""

  case "${OUTPUT_FORMAT}" in
    text)
      printf 'repo_readiness=%s\n' "${REPO_READINESS}"
      printf 'repo=%s\n' "${REPO}"
      printf 'base=%s\n' "${BASE_BRANCH}"
      printf 'main_sha=%s\n' "${REMOTE_MAIN_SHA}"
      printf 'open_prs=%s\n' "${OPEN_PRS}"
      printf 'security_alerts=%s\n' "$((DEPENDABOT_OPEN + CODE_SCANNING_OPEN + SECRET_SCANNING_OPEN))"
      printf 'check_runs_total=%s\n' "${CHECK_RUNS_TOTAL}"
      printf 'check_runs_incomplete=%s\n' "${CHECK_RUNS_INCOMPLETE}"
      printf 'check_runs_failing=%s\n' "${CHECK_RUNS_FAILING}"
      printf 'active_runs=%s\n' "${ACTIVE_RUNS}"
      printf 'upstream_refresh_tracker=%s\n' "${TRACKER_CURRENT}"
      printf 'local_status=%s\n' "${LOCAL_STATUS}"
      if ((${#BLOCKERS[@]} > 0)); then
        printf 'blockers:\n'
        printf -- '- %s\n' "${BLOCKERS[@]}"
      fi
      ;;
    json)
      blockers_json="$(json_array_from_values "${BLOCKERS[@]}")"
      jq -n \
        --arg readiness "${REPO_READINESS}" \
        --arg repo "${REPO}" \
        --arg base "${BASE_BRANCH}" \
        --arg main_sha "${REMOTE_MAIN_SHA}" \
        --arg local_status "${LOCAL_STATUS}" \
        --arg tracker_issue "${TRACKER_ISSUE}" \
        --arg tracker_state "${TRACKER_STATE}" \
        --argjson open_prs "${OPEN_PRS}" \
        --argjson dependabot_open "${DEPENDABOT_OPEN}" \
        --argjson code_scanning_open "${CODE_SCANNING_OPEN}" \
        --argjson secret_scanning_open "${SECRET_SCANNING_OPEN}" \
        --argjson check_runs_total "${CHECK_RUNS_TOTAL}" \
        --argjson check_runs_incomplete "${CHECK_RUNS_INCOMPLETE}" \
        --argjson check_runs_failing "${CHECK_RUNS_FAILING}" \
        --argjson active_runs "${ACTIVE_RUNS}" \
        --argjson tracker_current "${TRACKER_CURRENT}" \
        --argjson blockers "${blockers_json}" \
        '{
          repo_readiness: $readiness,
          repo: $repo,
          base: $base,
          main_sha: $main_sha,
          open_prs: $open_prs,
          security_alerts: {
            dependabot: $dependabot_open,
            code_scanning: $code_scanning_open,
            secret_scanning: $secret_scanning_open
          },
          check_runs: {
            total: $check_runs_total,
            incomplete: $check_runs_incomplete,
            failing: $check_runs_failing
          },
          active_runs: $active_runs,
          upstream_refresh_tracker: {
            issue: $tracker_issue,
            state: $tracker_state,
            current: $tracker_current
          },
          local_status: $local_status,
          blockers: $blockers
        }'
      ;;
    *)
      echo "Unsupported output format: ${OUTPUT_FORMAT}" >&2
      exit 2
      ;;
  esac
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      REPO="${2:-}"
      [[ -n "${REPO}" ]] || {
        echo "--repo requires OWNER/REPO" >&2
        exit 2
      }
      shift 2
      ;;
    --base)
      BASE_BRANCH="${2:-}"
      [[ -n "${BASE_BRANCH}" ]] || {
        echo "--base requires a branch" >&2
        exit 2
      }
      shift 2
      ;;
    --format)
      OUTPUT_FORMAT="${2:-}"
      [[ -n "${OUTPUT_FORMAT}" ]] || {
        echo "--format requires text or json" >&2
        exit 2
      }
      shift 2
      ;;
    --watch)
      WATCH=1
      shift
      ;;
    --timeout)
      TIMEOUT_SECONDS="$(parse_duration_seconds "${2:-}")"
      shift 2
      ;;
    --poll)
      POLL_SECONDS="$(parse_duration_seconds "${2:-}")"
      shift 2
      ;;
    --no-fetch)
      FETCH=0
      shift
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

require_tool gh
require_tool git
require_tool jq
require_tool shasum

case "${OUTPUT_FORMAT}" in
  text | json) ;;
  *)
    echo "Unsupported output format: ${OUTPUT_FORMAT}" >&2
    exit 2
    ;;
esac

if [[ -z "${REPO}" ]]; then
  REPO="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
fi

if [[ "${BASE_BRANCH}" != "main" ]]; then
  echo "Only main-based repository readiness is supported by default." >&2
  exit 2
fi

deadline="$(($(date +%s) + TIMEOUT_SECONDS))"
while true; do
  collect_once
  if [[ "${REPO_READINESS}" == "ready" || "${WATCH}" -eq 0 ]]; then
    break
  fi
  if [[ "$(date +%s)" -ge "${deadline}" ]]; then
    break
  fi
  echo "repo_readiness=${REPO_READINESS}; sleeping ${POLL_SECONDS}s" >&2
  sleep "${POLL_SECONDS}"
done

emit_result
[[ "${REPO_READINESS}" == "ready" ]]
