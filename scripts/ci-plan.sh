#!/usr/bin/env -S -uPOSIXLY_CORRECT -uPOSIX_PEDANTIC BASH_ENV= ENV= SHELLOPTS= BASHOPTS= BASH_COMPAT= BASH_XTRACEFD= FUNCNEST= LC_ALL=C bash -p
# shellcheck shell=bash
set -euo pipefail
set +C +f +v +x
shopt -u failglob nullglob nocaseglob nocasematch
unset GLOBIGNORE FUNCNEST
export LC_ALL=C

ROOT_DIR="$(CDPATH='' cd -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"
MANIFEST_PATH="${WORKCELL_WORKFLOW_LANE_MANIFEST_PATH:-${ROOT_DIR}/policy/workflow-lanes.json}"
PROFILE="pr-parity"
EVENT="pull_request"
BASE_BRANCH="main"
OUTPUT_FORMAT="text"
AUTO_CHANGED_FILES=1
PLANNER_INDEX_FILE=""
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
  local encoded="" value=""
  encoded="$(for value in "$@"; do printf '%s\0' "${value}"; done | jq -Rs 'split("\u0000")[:-1]')" ||
    git_plan_error 'Unable to encode planner values as JSON.\n'
  /usr/bin/cmp -s <(for value in "$@"; do printf '%s\0' "${value}"; done) \
    <(printf '%s' "${encoded}" | jq -rj '.[] + "\u0000"') ||
    git_plan_error 'Planner values must be valid UTF-8.\n'
  printf '%s\n' "${encoded}"
}
git_plan_error() {
  printf '%b' "$1" >&2
  exit 1
}
bootstrap_git_dir() {
  local work_tree="${ROOT_DIR}" git_dir="" status=0
  [[ ! -L "${work_tree}/.git" && -d "${work_tree}/.git" ]] || git_plan_error 'Planner repository metadata must be anchored by the script root .git directory.\n'
  git_dir="$(
    /usr/bin/env -i \
      "PATH=${PATH}" "HOME=${HOME:-/tmp}" "TMPDIR=${TMPDIR:-/tmp}" LC_ALL=C \
      GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_SYSTEM=/dev/null \
      GIT_NO_LAZY_FETCH=1 GIT_NO_REPLACE_OBJECTS=1 GIT_GRAFT_FILE=/dev/null GIT_TERMINAL_PROMPT=0 \
      git -C "${work_tree}" --no-pager \
      -c advice.graftFileDeprecated=false -c core.bare=false -c "core.worktree=${work_tree}" \
      rev-parse --absolute-git-dir
  )" || status=$?
  [[ "${status}" -eq 0 ]] || {
    printf 'Unable to resolve the planner repository metadata directory.\n' >&2
    return "${status}"
  }
  case "${git_dir}" in
    /*) ;;
    *) git_plan_error 'Planner repository metadata path is not absolute.\n' ;;
  esac
  [[ -d "${git_dir}" ]] || git_plan_error 'Planner repository metadata path is not a directory.\n'
  (
    cd -P "${git_dir}" && pwd -P
  )
}

# Git remains shell boundary glue here because changed paths must be known before
# the Go lane planner can run. Policy selection itself remains in workcell-citools.
planner_git_bound() {
  local git_dir="$1" work_tree="$2" attr_source="$3"
  shift 3
  /usr/bin/env -i \
    "PATH=${PATH}" "HOME=${HOME:-/tmp}" "TMPDIR=${TMPDIR:-/tmp}" LC_ALL=C GCM_INTERACTIVE=never \
    GIT_ATTR_GLOBAL=/dev/null GIT_ATTR_NOSYSTEM=1 "GIT_ATTR_SOURCE=${attr_source}" GIT_ATTR_SYSTEM=/dev/null \
    GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_SYSTEM=/dev/null GIT_GRAFT_FILE=/dev/null \
    "GIT_INDEX_FILE=${PLANNER_INDEX_FILE:-${git_dir}/index}" GIT_LITERAL_PATHSPECS=1 GIT_NO_LAZY_FETCH=1 \
    GIT_NO_REPLACE_OBJECTS=1 GIT_OPTIONAL_LOCKS=0 GIT_TERMINAL_PROMPT=0 \
    git -C "${work_tree}" \
    --git-dir="${git_dir}" --work-tree="${work_tree}" --no-pager \
    -c advice.graftFileDeprecated=false -c core.autocrlf=false -c core.askPass= \
    -c core.attributesFile=/dev/null -c core.bare=false -c core.excludesFile=/dev/null \
    -c core.fileMode=true -c core.fsmonitor=false -c core.hooksPath=/dev/null -c core.ignoreCase=false \
    -c core.ignoreStat=false -c core.protectHFS=true -c core.protectNTFS=true \
    -c core.splitIndex=false -c core.symlinks=true -c "core.worktree=${work_tree}" \
    -c credential.helper= -c credential.interactive=never -c diff.ignoreSubmodules=none -c index.sparse=false \
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
    0) ;;
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
  [[ "${status}" -eq 0 ]] || {
    printf 'Unable to plan changed files: present base ref %s could not be read.\n' "${ref}" >&2
    return "${status}"
  }
  validate_oid "${raw_oid}" || git_plan_error 'Unable to plan changed files: resident base ref returned a malformed object ID.\n'
  status=0
  commit_oid="$(planner_git rev-parse --verify --quiet "${raw_oid}^{commit}")" || status=$?
  [[ "${status}" -eq 0 ]] || {
    printf 'Unable to plan changed files: resident base ref %s does not resolve to a resident commit.\n' "${ref}" >&2
    return "${status}"
  }
  if ! validate_oid "${commit_oid}" || [[ "${#commit_oid}" -ne "${#raw_oid}" ]]; then
    git_plan_error 'Unable to plan changed files: resident base commit returned a malformed object ID.\n'
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
      printf 'Unable to plan changed files: neither the resident origin/%s ref nor local %s branch exists.\n' "${BASE_BRANCH}" "${BASE_BRANCH}" >&2
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
    [[ -n "${path}" ]] || git_plan_error 'Git returned an empty changed-file record.\n'
    CHANGED_FILES[${#CHANGED_FILES[@]}]="${path}"
    path=""
  done <"${output_file}" || return $?
  [[ -z "${path}" ]] || git_plan_error 'Git returned an incomplete changed-file record.\n'
}
reject_conversion_filters() {
  local tracked="" attributes="" path="" name="" value=""
  tracked="$(new_git_output_file)" || return $?
  attributes="$(new_git_output_file)" || return $?
  planner_git ls-files -z >"${tracked}" || return $?
  planner_git check-attr -z --all --stdin <"${tracked}" >"${attributes}" || return $?
  while IFS= read -r -d '' path <&3; do
    IFS= read -r -d '' name <&3 || git_plan_error 'Git returned an incomplete attribute record.\n'
    IFS= read -r -d '' value <&3 || git_plan_error 'Git returned an incomplete attribute record.\n'
    [[ -n "${path}" && -n "${name}" ]] || git_plan_error 'Git returned a malformed attribute record.\n'
    if [[ "${name}" == "filter" ]]; then
      printf 'Unable to plan changed files: effective pinned attributes select conversion filter %s for %s.\n' \
        "${value}" "${path}" >&2
      return 1
    fi
    path=""
  done 3<"${attributes}" || return $?
  [[ -z "${path}" ]] || git_plan_error 'Git returned an incomplete attribute record.\n'
}
reject_split_index() {
  local candidate="" shared="" index="${PLANNER_GIT_DIR}/index" shallow="" status=0
  [[ -f "${index}" && ! -L "${index}" ]] ||
    git_plan_error 'Unable to plan changed files: the repository index is not a regular file.\n'
  for candidate in "${PLANNER_GIT_DIR}"/*; do
    [[ -e "${candidate}" || -L "${candidate}" ]] || continue
    shared="${PLANNER_GIT_DIR}/sharedindex.${candidate##*.}"
    [[ ! -e "${shared}" && ! -L "${shared}" ]] ||
      git_plan_error 'Unable to plan changed files: split-index state is not accepted.\n'
  done
  shallow="$(planner_git rev-parse --path-format=absolute --git-path shallow)" || status=$?
  [[ "${status}" -eq 0 && "${shallow}" == /* ]] || git_plan_error 'Unable to resolve shallow-repository state.\n'
  [[ ! -e "${shallow}" && ! -L "${shallow}" ]] ||
    git_plan_error 'Unable to plan changed files: shallow repositories are not accepted.\n'
}
reject_hidden_index_entries() {
  local inventory="" entry="" tag=""
  inventory="$(new_git_output_file)" || return $?
  planner_git ls-files -v -z >"${inventory}" || return $?
  while IFS= read -r -d '' entry; do
    [[ "${entry:1:1}" == " " && -n "${entry:2}" ]] || git_plan_error 'Git returned a malformed tracked-index record.\n'
    tag="${entry:0:1}"
    case "${tag}" in
      S | [a-z])
        git_plan_error 'Unable to plan changed files: a tracked index entry uses a hidden-worktree flag.\n'
        ;;
      H | M | R | C | K) ;;
      *)
        git_plan_error 'Git returned a malformed tracked-index record.\n'
        ;;
    esac
    entry=""
  done <"${inventory}" || return $?
  [[ -z "${entry}" ]] || git_plan_error 'Git returned an incomplete tracked-index record.\n'
}
reject_unsafe_worktree_ancestry() {
  local remaining="$1" prefix="${ROOT_DIR}" component=""
  while [[ "${remaining}" == */* ]]; do
    component="${remaining%%/*}"
    remaining="${remaining#*/}"
    case "${component}" in
      '' | . | .. | .[gG][iI][tT]) git_plan_error 'Git returned an unsafe tracked-file path.\n' ;;
    esac
    prefix="${prefix}/${component}"
    [[ ! -L "${prefix}" && (! -e "${prefix}" || -d "${prefix}") ]] ||
      git_plan_error 'Unable to plan changed files: tracked-file ancestry is not a regular directory.\n'
  done
  case "${remaining}" in
    '' | . | .. | .[gG][iI][tT]) git_plan_error 'Git returned an unsafe tracked-file path.\n' ;;
  esac
}
prepare_planner_index_snapshot() {
  local inventory="" entry="" record="" metadata="" mode="" rest="" oid="" stage=""
  local subpath="" candidate="" hashes="" actual="" index=0
  local ancestry_paths=() indexed_paths=() indexed_oids=() gitlink_paths=() tracked_paths=() tracked_oids=()
  inventory="$(new_git_output_file)" || return $?
  planner_git ls-files --stage -z >"${inventory}" || return $?
  while IFS= read -r -d '' entry; do
    case "${entry}" in
      *$'\t'*) ;;
      *) git_plan_error 'Git returned a malformed staged-file record.\n' ;;
    esac
    record="${entry}"
    entry=""
    metadata="${record%%$'\t'*}"
    mode="${metadata%% *}"
    rest="${metadata#* }"
    oid="${rest%% *}"
    stage="${rest#* }"
    subpath="${record#*$'\t'}"
    case "${mode}" in
      100644 | 100755 | 120000 | 160000) ;;
      *) git_plan_error 'Git returned a malformed staged-file record.\n' ;;
    esac
    if [[ "${rest}" == "${metadata}" || "${stage}" == "${rest}" || "${stage}" == *' '* ]] ||
      ! validate_oid "${oid}"; then
      git_plan_error 'Git returned a malformed staged-file record.\n'
    fi
    case "${stage}" in
      0 | 1 | 2 | 3) ;;
      *) git_plan_error 'Git returned a malformed staged-file record.\n' ;;
    esac
    [[ -n "${subpath}" ]] || git_plan_error 'Git returned an empty staged-file path.\n'
    ancestry_paths[${#ancestry_paths[@]}]="${subpath}"
    if [[ "${mode}" == "160000" ]]; then
      gitlink_paths[${#gitlink_paths[@]}]="${subpath}"
    elif [[ "${stage}" == 0 && ("${mode}" == 100644 || "${mode}" == 100755) ]]; then
      indexed_paths[${#indexed_paths[@]}]="${subpath}"
      indexed_oids[${#indexed_oids[@]}]="${oid}"
    fi
  done <"${inventory}" || return $?
  [[ -z "${entry}" ]] || git_plan_error 'Git returned an incomplete staged-file record.\n'
  PLANNER_INDEX_FILE="${GIT_RUN_ROOT}/index"
  planner_git update-index -z --index-info <"${inventory}" || git_plan_error 'Unable to construct the planner index snapshot.\n'
  for subpath in "${ancestry_paths[@]}"; do reject_unsafe_worktree_ancestry "${subpath}"; done
  for ((index = 0; index < ${#gitlink_paths[@]}; index++)); do
    subpath="${gitlink_paths[${index}]}"
    candidate="${ROOT_DIR}/${subpath}"
    [[ ! -L "${candidate}" && ! -e "${candidate}" ]] || git_plan_error 'Unable to plan changed files: a gitlink worktree path is present.\n'
  done
  for ((index = 0; index < ${#indexed_paths[@]}; index++)); do
    subpath="${indexed_paths[${index}]}"
    candidate="${ROOT_DIR}/${subpath}"
    if [[ ! -L "${candidate}" && -f "${candidate}" ]]; then
      tracked_paths[${#tracked_paths[@]}]="${subpath}"
      tracked_oids[${#tracked_oids[@]}]="${indexed_oids[${index}]}"
    fi
  done
  index=0
  if ((${#tracked_paths[@]})); then
    hashes="$(new_git_output_file)" || return $?
    planner_git hash-object --no-filters -- "${tracked_paths[@]}" >"${hashes}" || return $?
    while IFS= read -r actual; do
      ((index < ${#tracked_paths[@]})) && validate_oid "${actual}" &&
        [[ "${#actual}" -eq "${#tracked_oids[${index}]}" ]] || git_plan_error 'Git returned malformed tracked-content hashes.\n'
      [[ "${actual}" == "${tracked_oids[${index}]}" ]] || CHANGED_FILES[${#CHANGED_FILES[@]}]="${tracked_paths[${index}]}"
      index=$((index + 1))
    done <"${hashes}" || return $?
    ((index == ${#tracked_paths[@]})) || git_plan_error 'Git returned incomplete tracked-content hashes.\n'
  fi
}
reject_mutable_ignore_files() {
  local changed_start="$1" inventory="" path="" index=0
  for ((index = changed_start; index < ${#CHANGED_FILES[@]}; index++)); do
    case "${CHANGED_FILES[${index}]}" in
      .[gG][iI][tT][iI][gG][nN][oO][rR][eE] | */.[gG][iI][tT][iI][gG][nN][oO][rR][eE])
        git_plan_error 'Unable to plan changed files: a worktree or index .gitignore differs from HEAD.\n'
        ;;
    esac
  done
  inventory="$(new_git_output_file)" || return $?
  planner_git ls-files --others -z >"${inventory}" || return $?
  while IFS= read -r -d '' path; do
    [[ -n "${path}" ]] || git_plan_error 'Git returned an empty untracked-file record.\n'
    case "${path}" in
      .[gG][iI][tT][iI][gG][nN][oO][rR][eE] | */.[gG][iI][tT][iI][gG][nN][oO][rR][eE])
        git_plan_error 'Unable to plan changed files: an untracked .gitignore could change ignore authority.\n'
        ;;
    esac
    path=""
  done <"${inventory}" || return $?
  [[ -z "${path}" ]] || git_plan_error 'Git returned an incomplete untracked-file record.\n'
}
collect_changed_files_from_git() {
  local base_oid="" mutable_ignore_start=0
  reject_split_index || return $?
  base_oid="$(resolve_base_oid)" || return $?
  PLANNER_ATTR_SOURCE="${base_oid}"
  base_oid="$(planner_git merge-base --all "${base_oid}" HEAD)" || return $?
  if ! validate_oid "${base_oid}" || [[ "${#base_oid}" -ne "${#PLANNER_ATTR_SOURCE}" ]]; then
    git_plan_error 'Unable to plan changed files: base and HEAD must have exactly one resident merge base.\n'
  fi
  reject_hidden_index_entries || return $?
  reject_conversion_filters || return $?
  collect_git_paths diff \
    --no-ext-diff --no-textconv --no-renames --ignore-submodules=none \
    --name-only -z "${base_oid}" HEAD || return $?
  mutable_ignore_start="${#CHANGED_FILES[@]}"
  prepare_planner_index_snapshot || return $?
  collect_git_paths diff \
    --no-ext-diff --no-textconv --no-renames --ignore-submodules=none \
    --name-only -z || return $?
  collect_git_paths diff --cached \
    --no-ext-diff --no-textconv --no-renames --ignore-submodules=none \
    --name-only -z || return $?
  reject_mutable_ignore_files "${mutable_ignore_start}" || return $?
  collect_git_paths ls-files --others --exclude-per-directory=.gitignore -z
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

json_array_from_values "${PROFILE}" "${EVENT}" "${BASE_BRANCH}" >/dev/null
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
