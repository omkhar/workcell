#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(CDPATH='' cd -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"
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
  --base BRANCH               Base branch for resident diff planning (default: main)
  --label LABEL               Repeatable PR label input
  --changed-file PATH         Repeatable explicit changed-file input
  --no-auto-changed-files     Do not derive changed files from resident Git state
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

bootstrap_git_dir() {
  local work_tree="${1:-${ROOT_DIR}}" git_dir="" status=0
  git_dir="$(
    /usr/bin/env -i \
      "PATH=${PATH}" \
      "HOME=${HOME:-/tmp}" \
      "TMPDIR=${TMPDIR:-/tmp}" \
      LC_ALL=C \
      GIT_CONFIG_GLOBAL=/dev/null \
      GIT_CONFIG_NOSYSTEM=1 \
      GIT_CONFIG_SYSTEM=/dev/null \
      GIT_NO_LAZY_FETCH=1 \
      GIT_NO_REPLACE_OBJECTS=1 \
      GIT_GRAFT_FILE=/dev/null \
      GIT_TERMINAL_PROMPT=0 \
      git -C "${work_tree}" --no-pager \
      -c advice.graftFileDeprecated=false \
      -c core.bare=false \
      -c "core.worktree=${work_tree}" \
      rev-parse --absolute-git-dir
  )" || status=$?
  if [[ "${status}" -ne 0 ]]; then
    printf 'Unable to resolve the planner repository metadata directory.\n' >&2
    return "${status}"
  fi
  case "${git_dir}" in
    /*) ;;
    *)
      printf 'Planner repository metadata path is not absolute.\n' >&2
      return 1
      ;;
  esac
  if [[ ! -d "${git_dir}" ]]; then
    printf 'Planner repository metadata path is not a directory.\n' >&2
    return 1
  fi
  (
    cd -P "${git_dir}"
    pwd -P
  )
}

# Git remains shell boundary glue here because changed paths must be known before
# the Go lane planner can run. Policy selection itself remains in workcell-citools.
planner_git_bound() {
  local git_dir="$1" work_tree="$2" attr_source="$3"
  shift 3
  /usr/bin/env -i \
    "PATH=${PATH}" \
    "HOME=${HOME:-/tmp}" \
    "TMPDIR=${TMPDIR:-/tmp}" \
    LC_ALL=C \
    GCM_INTERACTIVE=never \
    GIT_ATTR_GLOBAL=/dev/null \
    GIT_ATTR_NOSYSTEM=1 \
    "GIT_ATTR_SOURCE=${attr_source}" \
    GIT_ATTR_SYSTEM=/dev/null \
    GIT_CONFIG_GLOBAL=/dev/null \
    GIT_CONFIG_NOSYSTEM=1 \
    GIT_CONFIG_SYSTEM=/dev/null \
    GIT_GRAFT_FILE=/dev/null \
    GIT_LITERAL_PATHSPECS=1 \
    GIT_NO_LAZY_FETCH=1 \
    GIT_NO_REPLACE_OBJECTS=1 \
    GIT_OPTIONAL_LOCKS=0 \
    GIT_TERMINAL_PROMPT=0 \
    git -C "${work_tree}" \
    --git-dir="${git_dir}" \
    --work-tree="${work_tree}" \
    --no-pager \
    -c advice.graftFileDeprecated=false \
    -c core.askPass= \
    -c core.attributesFile=/dev/null \
    -c core.bare=false \
    -c core.excludesFile=/dev/null \
    -c core.fsmonitor=false \
    -c core.hooksPath=/dev/null \
    -c "core.worktree=${work_tree}" \
    -c credential.helper= \
    -c credential.interactive=never \
    -c diff.ignoreSubmodules=none \
    "$@"
}

planner_git() {
  planner_git_bound "${PLANNER_GIT_DIR}" "${ROOT_DIR}" "${PLANNER_ATTR_SOURCE}" "$@"
}

validate_base_branch() {
  local status=0
  case "${BASE_BRANCH}" in
    -* | refs/*)
      printf 'Invalid --base branch name: %s\n' "${BASE_BRANCH}" >&2
      return 2
      ;;
  esac
  planner_git check-ref-format "refs/heads/${BASE_BRANCH}" >/dev/null 2>&1 || status=$?
  case "${status}" in
    0) return 0 ;;
    1)
      printf 'Invalid --base branch name: %s\n' "${BASE_BRANCH}" >&2
      return 2
      ;;
    *)
      printf 'Unable to validate the requested base branch.\n' >&2
      return "${status}"
      ;;
  esac
}

validate_oid() {
  local oid="$1"
  case "${oid}" in
    *[!0123456789abcdef]*) return 1 ;;
  esac
  [[ "${#oid}" -eq 40 || "${#oid}" -eq 64 ]]
}

ref_presence() {
  local ref="$1" status=0
  planner_git show-ref --exists "${ref}" >/dev/null 2>&1 || status=$?
  return "${status}"
}

resolve_present_ref() {
  local ref="$1" raw_oid="" commit_oid="" status=0
  raw_oid="$(planner_git show-ref --verify --hash "${ref}")" || status=$?
  if [[ "${status}" -ne 0 ]]; then
    printf 'Unable to plan changed files: present base ref %s could not be read.\n' "${ref}" >&2
    return "${status}"
  fi
  if ! validate_oid "${raw_oid}"; then
    printf 'Unable to plan changed files: resident base ref returned a malformed object ID.\n' >&2
    return 1
  fi
  status=0
  commit_oid="$(planner_git rev-parse --verify --quiet "${raw_oid}^{commit}")" || status=$?
  if [[ "${status}" -ne 0 ]]; then
    printf 'Unable to plan changed files: resident base ref %s does not resolve to a resident commit.\n' \
      "${ref}" >&2
    return "${status}"
  fi
  if ! validate_oid "${commit_oid}" || [[ "${#commit_oid}" -ne "${#raw_oid}" ]]; then
    printf 'Unable to plan changed files: resident base commit returned a malformed object ID.\n' >&2
    return 1
  fi
  printf '%s\n' "${commit_oid}"
}

resolve_base_oid() {
  local remote_ref="refs/remotes/origin/${BASE_BRANCH}"
  local local_ref="refs/heads/${BASE_BRANCH}"
  local local_oid="" status=0

  ref_presence "${remote_ref}" || status=$?
  case "${status}" in
    0)
      resolve_present_ref "${remote_ref}"
      return $?
      ;;
    2) ;;
    *)
      printf 'Unable to inspect resident base ref %s.\n' "${remote_ref}" >&2
      return "${status}"
      ;;
  esac

  status=0
  ref_presence "${local_ref}" || status=$?
  case "${status}" in
    0) local_oid="$(resolve_present_ref "${local_ref}")" || return $? ;;
    2)
      printf 'Unable to plan changed files: neither the resident origin/%s ref nor local %s branch exists.\n' \
        "${BASE_BRANCH}" "${BASE_BRANCH}" >&2
      return 1
      ;;
    *)
      printf 'Unable to inspect resident base ref %s.\n' "${local_ref}" >&2
      return "${status}"
      ;;
  esac

  # A remote ref may appear while local fallback is being resolved. Recheck it
  # before accepting the local OID; if it appeared, remote-first semantics win.
  status=0
  ref_presence "${remote_ref}" || status=$?
  case "${status}" in
    0) resolve_present_ref "${remote_ref}" ;;
    2) printf '%s\n' "${local_oid}" ;;
    *)
      printf 'Unable to recheck resident base ref %s.\n' "${remote_ref}" >&2
      return "${status}"
      ;;
  esac
}

new_git_output_file() {
  mktemp "${GIT_RUN_ROOT}/output.XXXXXX"
}

collect_git_paths() {
  local output_file="" path="" status=0
  output_file="$(new_git_output_file)" || return $?
  planner_git "$@" >"${output_file}" || status=$?
  [[ "${status}" -eq 0 ]] || return "${status}"
  while IFS= read -r -d '' path; do
    if [[ -z "${path}" ]]; then
      printf 'Git returned an empty changed-file record.\n' >&2
      return 1
    fi
    CHANGED_FILES[${#CHANGED_FILES[@]}]="${path}"
    path=""
  done <"${output_file}"
  if [[ -n "${path}" ]]; then
    printf 'Git returned an incomplete changed-file record.\n' >&2
    return 1
  fi
}

reject_conversion_filters_bound() {
  local git_dir="$1" work_tree="$2" attr_source="$3"
  local tracked="" attributes="" path="" name="" value=""
  tracked="$(new_git_output_file)" || return $?
  attributes="$(new_git_output_file)" || return $?
  planner_git_bound "${git_dir}" "${work_tree}" "${attr_source}" \
    ls-files -z >"${tracked}" || return $?
  planner_git_bound "${git_dir}" "${work_tree}" "${attr_source}" \
    check-attr -z --all --stdin <"${tracked}" >"${attributes}" || return $?
  while IFS= read -r -d '' path <&3; do
    IFS= read -r -d '' name <&3 || {
      printf 'Git returned an incomplete attribute record.\n' >&2
      return 1
    }
    IFS= read -r -d '' value <&3 || {
      printf 'Git returned an incomplete attribute record.\n' >&2
      return 1
    }
    if [[ -z "${path}" || -z "${name}" ]]; then
      printf 'Git returned a malformed attribute record.\n' >&2
      return 1
    fi
    if [[ "${name}" == "filter" ]]; then
      printf 'Unable to plan changed files: effective pinned attributes select conversion filter %s for %s.\n' \
        "${value}" "${path}" >&2
      return 1
    fi
    path=""
  done 3<"${attributes}"
  if [[ -n "${path}" ]]; then
    printf 'Git returned an incomplete attribute record.\n' >&2
    return 1
  fi
}

reject_conversion_filters() {
  reject_conversion_filters_bound \
    "${PLANNER_GIT_DIR}" "${ROOT_DIR}" "${PLANNER_ATTR_SOURCE}"
}

record_preflight_repository() {
  local git_dir="$1" work_tree="$2" index=0
  for ((index = 0; index < ${#PREFLIGHT_GIT_DIRS[@]}; index++)); do
    if [[ "${PREFLIGHT_GIT_DIRS[${index}]}" == "${git_dir}" ||
      "${PREFLIGHT_WORKTREES[${index}]}" == "${work_tree}" ]]; then
      printf 'Unable to plan changed files: populated submodule reuses repository authority.\n' >&2
      return 1
    fi
  done
  PREFLIGHT_GIT_DIRS[${#PREFLIGHT_GIT_DIRS[@]}]="${git_dir}"
  PREFLIGHT_WORKTREES[${#PREFLIGHT_WORKTREES[@]}]="${work_tree}"
}

preflight_populated_submodules() {
  local git_dir="$1" work_tree="$2" attr_source="$3"
  local inventory="" entry="" record="" metadata="" mode="" subpath="" previous=""
  local candidate="" subroot="" subgit="" subhead="" status=0
  inventory="$(new_git_output_file)" || return $?
  planner_git_bound "${git_dir}" "${work_tree}" "${attr_source}" \
    ls-files --stage -z >"${inventory}" || return $?
  while IFS= read -r -d '' entry; do
    case "${entry}" in
      *$'\t'*) ;;
      *)
        printf 'Git returned a malformed staged-file record.\n' >&2
        return 1
        ;;
    esac
    record="${entry}"
    entry=""
    metadata="${record%%$'\t'*}"
    mode="${metadata%% *}"
    subpath="${record#*$'\t'}"
    [[ "${mode}" == "160000" ]] || continue
    [[ -n "${subpath}" ]] || {
      printf 'Git returned an empty submodule path.\n' >&2
      return 1
    }
    [[ "${subpath}" != "${previous}" ]] || continue
    previous="${subpath}"
    candidate="${work_tree}/${subpath}"
    if [[ -L "${candidate}" ]]; then
      printf 'Unable to plan changed files: populated submodule path is a symlink: %s\n' \
        "${subpath}" >&2
      return 1
    fi
    [[ -d "${candidate}" && -e "${candidate}/.git" ]] || continue
    if [[ -L "${candidate}/.git" ||
      (! -f "${candidate}/.git" && ! -d "${candidate}/.git") ]]; then
      printf 'Unable to plan changed files: submodule metadata entry is not a regular file or directory: %s\n' \
        "${subpath}" >&2
      return 1
    fi
    subroot="$(CDPATH='' cd -- "${candidate}" && pwd -P)" || return $?
    if [[ "${subroot}" != "${candidate}" ]]; then
      printf 'Unable to plan changed files: populated submodule traverses symlinked ancestry: %s\n' \
        "${subpath}" >&2
      return 1
    fi
    subgit="$(bootstrap_git_dir "${subroot}")" || return $?
    record_preflight_repository "${subgit}" "${subroot}" || return $?
    status=0
    subhead="$(
      planner_git_bound "${subgit}" "${subroot}" HEAD \
        rev-parse --verify --quiet 'HEAD^{commit}'
    )" || status=$?
    if [[ "${status}" -ne 0 ]] || ! validate_oid "${subhead}"; then
      printf 'Unable to plan changed files: populated submodule HEAD is not a resident commit: %s\n' \
        "${subpath}" >&2
      [[ "${status}" -ne 0 ]] && return "${status}"
      return 1
    fi
    reject_conversion_filters_bound "${subgit}" "${subroot}" "${subhead}" || return $?
    preflight_populated_submodules "${subgit}" "${subroot}" "${subhead}" || return $?
  done <"${inventory}"
  if [[ -n "${entry}" ]]; then
    printf 'Git returned an incomplete staged-file record.\n' >&2
    return 1
  fi
}

collect_changed_files_from_git() {
  local base_oid=""
  base_oid="$(resolve_base_oid)" || return $?
  PLANNER_ATTR_SOURCE="${base_oid}"
  reject_conversion_filters || return $?
  PREFLIGHT_GIT_DIRS=("${PLANNER_GIT_DIR}")
  PREFLIGHT_WORKTREES=("${ROOT_DIR}")
  preflight_populated_submodules \
    "${PLANNER_GIT_DIR}" "${ROOT_DIR}" "${PLANNER_ATTR_SOURCE}" || return $?
  collect_git_paths diff \
    --no-ext-diff --no-textconv --no-renames --ignore-submodules=none \
    --name-only -z "${base_oid}...HEAD" || return $?
  collect_git_paths diff \
    --no-ext-diff --no-textconv --no-renames --ignore-submodules=none \
    --name-only -z || return $?
  collect_git_paths diff --cached \
    --no-ext-diff --no-textconv --no-renames --ignore-submodules=none \
    --name-only -z || return $?
  collect_git_paths ls-files --others --exclude-standard -z
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
      [[ -n "${2:-}" ]] || {
        echo "--label requires a value" >&2
        exit 2
      }
      LABELS[${#LABELS[@]}]="$2"
      shift 2
      ;;
    --changed-file)
      [[ -n "${2:-}" ]] || {
        echo "--changed-file requires a value" >&2
        exit 2
      }
      CHANGED_FILES[${#CHANGED_FILES[@]}]="$2"
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
  PLANNER_GIT_DIR="$(bootstrap_git_dir)"
  PLANNER_ATTR_SOURCE=HEAD
  validate_base_branch
  GIT_RUN_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-ci-plan-git.XXXXXX")"
  trap '/bin/rm -rf -- "${GIT_RUN_ROOT}"' EXIT
  git_status=0
  collect_changed_files_from_git || git_status=$?
  /bin/rm -rf -- "${GIT_RUN_ROOT}"
  trap - EXIT
  if [[ "${git_status}" -ne 0 ]]; then
    printf 'Unable to derive changed files from the resident repository state.\n' >&2
    exit "${git_status}"
  fi
fi

tmp_config="$(mktemp "${TMPDIR:-/tmp}/workcell-ci-plan.XXXXXX")"
trap 'rm -f "${tmp_config}"' EXIT

if ((${#LABELS[@]})); then
  labels_json="$(json_array_from_values "${LABELS[@]}")"
else
  labels_json="$(json_array_from_values)"
fi
if ((${#CHANGED_FILES[@]})); then
  changed_json="$(json_array_from_values "${CHANGED_FILES[@]}")"
else
  changed_json="$(json_array_from_values)"
fi
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
    go run ./cmd/workcell-citools plan-workflow-lanes "${MANIFEST_PATH}" "${tmp_config}"
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
          (.reason // .local_script // .github_only_reason // "")
        ] | @tsv)
      ] | .[]
    ' <<<"${plan_json}"
    ;;
  *)
    echo "Unsupported ci-plan output format: ${OUTPUT_FORMAT}" >&2
    exit 2
    ;;
esac
