#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"
LOCAL_SNAPSHOT_ACTIVE="${WORKCELL_PREMERGE_LOCAL_SNAPSHOT_ACTIVE:-0}"
PROFILE="${WORKCELL_PREMERGE_PROFILE:-pr-parity}"
PLANNER_EVENT="${WORKCELL_PREMERGE_EVENT:-pull_request}"
BASE_BRANCH="${WORKCELL_PREMERGE_BASE_BRANCH:-main}"
REBUILD_VALIDATOR=0
RUN_RELEASE_BUNDLE=1
RUN_REPRO=1
ALLOW_DIRTY=0
LOCAL_SNAPSHOT_MODE=""
LOCAL_INCLUDE_UNTRACKED=0
LOCAL_KEEP_DIR=0
ORIGINAL_ARGS=("$@")
LABELS=()

usage() {
  cat <<EOF
Usage: $(basename "$0") [options]

Run the Workcell local validation profiles.

Options:
  --profile repo-core|pr-parity|release-preflight
                            Validation profile (default: pr-parity)
  --event EVENT             Planner event for pr-parity (default: pull_request)
  --base BRANCH             Base branch for PR-parity planning (default: main)
  --label LABEL             Repeatable PR label input for planner selection
  --allow-dirty             Run against the live worktree even when it is dirty
  --local-snapshot <mode>   Run from a disposable snapshot: head, index, worktree
  --local-include-untracked Include untracked files with --local-snapshot worktree
  --keep-local-dir          Preserve the local snapshot directory after exit
  --rebuild-validator       Rebuild the local validator image before validation
  --skip-release-bundle     Skip verify-release-bundle.sh in shared validate runs
  --skip-repro              Skip verify-reproducible-build.sh when selected
  -h, --help                Show this help
EOF
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_clean_tree() {
  local status_output=""

  status_output="$(git -C "${ROOT_DIR}" status --short --untracked-files=all)"
  if [[ -n "${status_output}" ]]; then
    echo "pre-merge requires a clean worktree, including untracked files, by default." >&2
    echo "Commit or stash local changes first, or rerun with --allow-dirty when you intentionally want to validate the live worktree." >&2
    printf '%s\n' "${status_output}" >&2
    exit 2
  fi
}

default_local_snapshot_parent() {
  local raw_parent=""

  if [[ -n "${WORKCELL_VALIDATION_SNAPSHOT_PARENT:-}" ]]; then
    raw_parent="${WORKCELL_VALIDATION_SNAPSHOT_PARENT}"
  elif [[ -n "${XDG_CACHE_HOME:-}" ]]; then
    raw_parent="${XDG_CACHE_HOME}/workcell-validation-snapshots"
  elif [[ -n "${HOME:-}" ]]; then
    if [[ "${OSTYPE:-}" == darwin* ]]; then
      raw_parent="${HOME}/Library/Caches/workcell-validation-snapshots"
    else
      raw_parent="${HOME}/.cache/workcell-validation-snapshots"
    fi
  else
    raw_parent="$(dirname "${ROOT_DIR}")"
  fi

  case "${raw_parent}" in
    /*)
      printf '%s\n' "${raw_parent}"
      ;;
    *)
      printf '%s\n' "${ROOT_DIR}/${raw_parent}"
      ;;
  esac
}

run_from_local_snapshot() {
  local -a snapshot_cmd=()
  local snapshot_parent=""
  local status=0

  [[ -n "${LOCAL_SNAPSHOT_MODE}" ]] || return 0
  [[ "${LOCAL_SNAPSHOT_ACTIVE}" == "1" ]] && return 0

  echo "[pre-merge] local validation will run from snapshot (${LOCAL_SNAPSHOT_MODE})." >&2
  snapshot_parent="$(default_local_snapshot_parent)"
  mkdir -p "${snapshot_parent}"
  snapshot_cmd=(
    env
    "WORKCELL_VALIDATION_SNAPSHOT_PARENT=${snapshot_parent}"
    "${ROOT_DIR}/scripts/with-validation-snapshot.sh"
    --repo "${ROOT_DIR}"
    --mode "${LOCAL_SNAPSHOT_MODE}"
  )
  if [[ "${LOCAL_INCLUDE_UNTRACKED}" -eq 1 ]]; then
    snapshot_cmd+=(--include-untracked)
  fi
  if [[ "${LOCAL_KEEP_DIR}" -eq 1 ]]; then
    snapshot_cmd+=(--keep-snapshot)
  fi
  snapshot_cmd+=(
    --
    env
    WORKCELL_PREMERGE_LOCAL_SNAPSHOT_ACTIVE=1
    ./scripts/pre-merge.sh
    "${ORIGINAL_ARGS[@]}"
  )

  "${snapshot_cmd[@]}" || status=$?
  exit "${status}"
}

parity_evidence_dir() {
  local git_dir=""
  git_dir="$(git -C "${ROOT_DIR}" rev-parse --absolute-git-dir)"
  printf '%s\n' "${git_dir}/workcell-parity"
}

compute_publish_snapshot_tree() {
  local snapshot_mode="$1"
  local tmp_index=""
  local tree_oid=""

  case "${snapshot_mode}" in
    index)
      git -C "${ROOT_DIR}" write-tree
      return 0
      ;;
    worktree) ;;
    *)
      echo "Unsupported parity snapshot mode: ${snapshot_mode}" >&2
      exit 2
      ;;
  esac

  tmp_index="$(mktemp "${TMPDIR:-/tmp}/workcell-premerge-index.XXXXXX")"
  rm -f "${tmp_index}"
  trap 'rm -f "${tmp_index}"' RETURN
  GIT_INDEX_FILE="${tmp_index}" git -C "${ROOT_DIR}" read-tree HEAD
  GIT_INDEX_FILE="${tmp_index}" git -C "${ROOT_DIR}" add -A
  tree_oid="$(GIT_INDEX_FILE="${tmp_index}" git -C "${ROOT_DIR}" write-tree)"
  rm -f "${tmp_index}"
  trap - RETURN
  printf '%s\n' "${tree_oid}"
}

collect_selected_scripts() {
  local plan_json="$1"

  jq -r '
    .lanes
    | map(select(.status == "local"))
    | sort_by(.local_order, .local_script, .id)
    | unique_by(.local_script)
    | .[]
    | [.local_order, .local_script]
    | @tsv
  ' <<<"${plan_json}"
}

collect_selected_repro_platforms() {
  local plan_json="$1"

  jq -r '
    .lanes[]
    | select(.status == "local" and .local_script == "scripts/verify-reproducible-build.sh")
    | .matrix.platform // empty
  ' <<<"${plan_json}" | sort -u | paste -sd, -
}

write_pr_parity_evidence() {
  local plan_json="$1"
  local tree_oid=""
  local evidence_dir=""
  local evidence_path=""
  local tmp_path=""
  local labels_json="[]"

  tree_oid="$(compute_publish_snapshot_tree worktree)"
  evidence_dir="$(parity_evidence_dir)"
  evidence_path="${evidence_dir}/pr-parity.json"
  tmp_path="${evidence_path}.tmp"
  mkdir -p "${evidence_dir}"
  if ((${#LABELS[@]} > 0)); then
    labels_json="$(printf '%s\n' "${LABELS[@]}" | jq -R . | jq -s .)"
  fi

  jq -n \
    --arg profile "${PROFILE}" \
    --arg event "${PLANNER_EVENT}" \
    --arg base "${BASE_BRANCH}" \
    --arg tree_oid "${tree_oid}" \
    --arg generated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    --argjson labels "${labels_json}" \
    --argjson plan "${plan_json}" \
    '{
      version: 1,
      profile: $profile,
      event: $event,
      base_branch: $base,
      labels: $labels,
      tree_oid: $tree_oid,
      generated_at: $generated_at,
      plan: $plan
    }' >"${tmp_path}"
  mv "${tmp_path}" "${evidence_path}"
  echo "[pre-merge] wrote PR parity evidence to ${evidence_path}"
}

execute_plan() {
  local plan_json="$1"
  local script=""
  local repro_platforms=""

  while IFS=$'\t' read -r _ script; do
    [[ -n "${script}" ]] || continue
    case "${script}" in
      scripts/check-workflows.sh)
        echo "[pre-merge] workflow lint and policy analysis"
        "${ROOT_DIR}/${script}"
        ;;
      scripts/ci/job-pr-shape.sh)
        echo "[pre-merge] pull request shape"
        WORKCELL_PR_BASE_REF="${BASE_BRANCH}" "${ROOT_DIR}/${script}" --base "${BASE_BRANCH}"
        ;;
      scripts/ci/job-validate.sh)
        echo "[pre-merge] shared validate job (${PROFILE})"
        WORKCELL_REBUILD_VALIDATOR_IMAGE="${REBUILD_VALIDATOR}" \
          WORKCELL_CI_VALIDATE_PROFILE="${PROFILE}" \
          WORKCELL_CI_VALIDATE_SKIP_RELEASE_BUNDLE="$([[ "${RUN_RELEASE_BUNDLE}" -eq 0 ]] && printf '1' || printf '0')" \
          SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
          "${ROOT_DIR}/${script}" --profile "${PROFILE}"
        ;;
      scripts/ci/job-docs.sh)
        echo "[pre-merge] docs parity"
        WORKCELL_REBUILD_VALIDATOR_IMAGE="${REBUILD_VALIDATOR}" \
          "${ROOT_DIR}/${script}"
        ;;
      scripts/ci/job-pin-hygiene.sh)
        echo "[pre-merge] release pin hygiene"
        "${ROOT_DIR}/${script}"
        ;;
      scripts/container-smoke.sh)
        echo "[pre-merge] container smoke"
        "${ROOT_DIR}/${script}"
        ;;
      scripts/verify-reproducible-build.sh)
        if [[ "${RUN_REPRO}" -eq 0 ]]; then
          continue
        fi
        repro_platforms="$(collect_selected_repro_platforms "${plan_json}")"
        if [[ -n "${repro_platforms}" ]]; then
          echo "[pre-merge] runtime reproducibility (${repro_platforms})"
          WORKCELL_REPRO_PLATFORMS="${repro_platforms}" \
            SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
            "${ROOT_DIR}/${script}"
        fi
        ;;
      *)
        echo "Unsupported local parity script in plan: ${script}" >&2
        exit 2
        ;;
    esac
  done < <(collect_selected_scripts "${plan_json}")
}

build_plan_args() {
  local -n plan_args_ref=$1
  local label=""

  plan_args_ref=(
    --profile "${PROFILE}"
    --event "${PLANNER_EVENT}"
    --base "${BASE_BRANCH}"
  )
  for label in "${LABELS[@]}"; do
    plan_args_ref+=(--label "${label}")
  done
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile)
      PROFILE="${2:-}"
      [[ -n "${PROFILE}" ]] || {
        echo "--profile requires a value." >&2
        usage >&2
        exit 2
      }
      shift 2
      ;;
    --event)
      PLANNER_EVENT="${2:-}"
      [[ -n "${PLANNER_EVENT}" ]] || {
        echo "--event requires a value." >&2
        usage >&2
        exit 2
      }
      shift 2
      ;;
    --base)
      BASE_BRANCH="${2:-}"
      [[ -n "${BASE_BRANCH}" ]] || {
        echo "--base requires a value." >&2
        usage >&2
        exit 2
      }
      shift 2
      ;;
    --label)
      LABELS+=("${2:-}")
      [[ -n "${LABELS[-1]}" ]] || {
        echo "--label requires a value." >&2
        usage >&2
        exit 2
      }
      shift 2
      ;;
    --allow-dirty)
      ALLOW_DIRTY=1
      shift
      ;;
    --local-snapshot)
      LOCAL_SNAPSHOT_MODE="${2-}"
      [[ -n "${LOCAL_SNAPSHOT_MODE}" ]] || {
        echo "Option --local-snapshot requires a value." >&2
        usage >&2
        exit 2
      }
      shift 2
      ;;
    --local-include-untracked)
      LOCAL_INCLUDE_UNTRACKED=1
      shift
      ;;
    --keep-local-dir)
      LOCAL_KEEP_DIR=1
      shift
      ;;
    --rebuild-validator)
      REBUILD_VALIDATOR=1
      shift
      ;;
    --skip-release-bundle)
      RUN_RELEASE_BUNDLE=0
      shift
      ;;
    --skip-repro)
      RUN_REPRO=0
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

case "${PROFILE}" in
  repo-core | pr-parity | release-preflight) ;;
  *)
    echo "Unsupported pre-merge profile: ${PROFILE}" >&2
    exit 2
    ;;
esac

require_tool docker
require_tool git
require_tool jq
require_tool shellcheck

if [[ "${LOCAL_INCLUDE_UNTRACKED}" -eq 1 ]] && [[ "${LOCAL_SNAPSHOT_MODE}" != "worktree" ]]; then
  echo "--local-include-untracked requires --local-snapshot worktree." >&2
  exit 2
fi

run_from_local_snapshot

if [[ "${ALLOW_DIRTY}" -eq 0 ]] && [[ -z "${LOCAL_SNAPSHOT_MODE}" ]]; then
  require_clean_tree
fi

echo "[pre-merge] workflow lane plan (${PROFILE})"
plan_args=()
build_plan_args plan_args
plan_json="$("${ROOT_DIR}/scripts/ci-plan.sh" "${plan_args[@]}" --format json)"
echo "${plan_json}" | jq -r '
  [
    "STATUS\tLANE\tDETAIL",
    (.lanes[] | [
      .status,
      .id,
      (.local_script // .reason // .github_only_reason // "")
    ] | @tsv)
  ] | .[]
'

execute_plan "${plan_json}"

if [[ "${PROFILE}" == "pr-parity" ]]; then
  write_pr_parity_evidence "${plan_json}"
fi

echo "Workcell pre-merge validation passed."
