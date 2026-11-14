#!/bin/bash -p
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/repo-publish-pr.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "${workcell_entry_root}/scripts/lib/trusted-entrypoint.sh"
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
WORKSPACE="${PWD}"
BASE_BRANCH="main"
SNAPSHOT="worktree"
PUBLISH_BRANCH=""
ALLOW_PARITY_OVERRIDE=0
PARITY_OVERRIDE_REASON=""
HOST_GIT_BIN=""
HOST_JQ_BIN=""
PASSTHROUGH_ARGS=()

usage() {
  cat <<'EOF'
Usage: repo-publish-pr.sh [workcell publish-pr options]

Repo-local publication wrapper that requires matching local PR-parity evidence
for the tree being published before handing off to scripts/workcell publish-pr.

Options handled here:
  --workspace PATH
  --base BRANCH
  --snapshot index|worktree
  --allow-parity-override
  --parity-override-reason TEXT
  -h, --help

All other options are passed through to scripts/workcell publish-pr.
EOF
}

resolve_workspace() {
  local candidate="$1"
  if [[ -z "${candidate}" ]]; then
    printf '%s\n' "${PWD}"
    return 0
  fi
  if [[ ! -d "${candidate}" ]]; then
    echo "Workspace path does not exist: ${candidate}" >&2
    exit 2
  fi
  (
    cd "${candidate}" &&
      pwd -P
  )
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

compute_snapshot_tree() {
  local workspace="$1"
  local snapshot_mode="$2"
  local tmp_index=""
  local tree_oid=""

  case "${snapshot_mode}" in
    index)
      "${HOST_GIT_BIN}" -C "${workspace}" write-tree
      return 0
      ;;
    worktree) ;;
    *)
      echo "Unsupported publish snapshot: ${snapshot_mode}" >&2
      exit 2
      ;;
  esac

  tmp_index="$(mktemp "${TMPDIR:-/tmp}/workcell-publish-index.XXXXXX")"
  rm -f "${tmp_index}"
  trap 'rm -f "${tmp_index}"' RETURN
  GIT_INDEX_FILE="${tmp_index}" "${HOST_GIT_BIN}" -C "${workspace}" read-tree HEAD
  GIT_INDEX_FILE="${tmp_index}" "${HOST_GIT_BIN}" -C "${workspace}" add -A
  tree_oid="$(GIT_INDEX_FILE="${tmp_index}" "${HOST_GIT_BIN}" -C "${workspace}" write-tree)"
  rm -f "${tmp_index}"
  trap - RETURN
  printf '%s\n' "${tree_oid}"
}

compute_worktree_status_sha256() {
  local workspace="$1"

  "${HOST_GIT_BIN}" -C "${workspace}" status --short --untracked-files=all | shasum -a 256 | awk '{print $1}'
}

resolve_current_base_ref() {
  local workspace="$1"
  local base_branch="$2"

  if "${HOST_GIT_BIN}" -C "${workspace}" remote get-url origin >/dev/null 2>&1; then
    "${HOST_GIT_BIN}" -C "${workspace}" fetch --no-tags --prune origin "+refs/heads/${base_branch}:refs/remotes/origin/${base_branch}" >/dev/null
    if "${HOST_GIT_BIN}" -C "${workspace}" rev-parse --verify --quiet "refs/remotes/origin/${base_branch}" >/dev/null; then
      printf 'refs/remotes/origin/%s\n' "${base_branch}"
      return 0
    fi
  fi
  if "${HOST_GIT_BIN}" -C "${workspace}" rev-parse --verify --quiet "refs/heads/${base_branch}" >/dev/null; then
    printf 'refs/heads/%s\n' "${base_branch}"
    return 0
  fi

  echo "Could not resolve base branch for parity verification: ${base_branch}" >&2
  exit 2
}

verify_pr_parity_evidence() {
  local workspace="$1"
  local snapshot_mode="$2"
  local base_branch="$3"
  local publish_branch="$4"
  local evidence_dir=""
  local evidence_path=""
  local expected_tree=""
  local expected_head=""
  local expected_status_sha256=""
  local base_ref=""
  local base_oid=""

  evidence_dir="$("${HOST_GIT_BIN}" -C "${workspace}" rev-parse --absolute-git-dir)/workcell-parity"
  evidence_path="${evidence_dir}/pr-parity.json"
  if [[ ! -f "${evidence_path}" ]]; then
    echo "Missing local PR-parity evidence for ${workspace}." >&2
    echo "Run ./scripts/pre-merge.sh --profile pr-parity first, or use --allow-parity-override with a reason." >&2
    exit 2
  fi
  base_ref="$(resolve_current_base_ref "${workspace}" "${base_branch}")"
  base_oid="$("${HOST_GIT_BIN}" -C "${workspace}" rev-parse --verify "${base_ref}")"
  expected_tree="$(compute_snapshot_tree "${workspace}" "${snapshot_mode}")"
  expected_head="$("${HOST_GIT_BIN}" -C "${workspace}" rev-parse HEAD)"
  expected_status_sha256="$(compute_worktree_status_sha256 "${workspace}")"

  # shellcheck disable=SC2016
  "${HOST_JQ_BIN}" -e \
    --arg base "${base_branch}" \
    --arg publish_branch "${publish_branch}" \
    --arg base_ref "${base_ref}" \
    --arg base_oid "${base_oid}" \
    --arg head_oid "${expected_head}" \
    --arg snapshot "${snapshot_mode}" \
    --arg tree_oid "${expected_tree}" \
    --arg status_sha256 "${expected_status_sha256}" \
    '
      .version == 1 and
      .profile == "pr-parity" and
      .base_branch == $base and
      .publish_branch == $publish_branch and
      .base_ref == $base_ref and
      .base_oid == $base_oid and
      .head_oid == $head_oid and
      .snapshot == $snapshot and
      .tree_oid == $tree_oid and
      .status_sha256 == $status_sha256
    ' "${evidence_path}" >/dev/null || {
    echo "Local PR-parity evidence does not match the tree being published." >&2
    echo "Expected base=${base_branch} publish_branch=${publish_branch} base_oid=${base_oid} head_oid=${expected_head} snapshot=${snapshot_mode} tree_oid=${expected_tree}." >&2
    echo "Re-run ./scripts/pre-merge.sh --profile pr-parity --publish-branch ${publish_branch} before publishing, or use --allow-parity-override with a reason." >&2
    exit 2
  }
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --workspace)
      WORKSPACE="$(resolve_workspace "${2:-}")"
      shift 2
      ;;
    --base)
      BASE_BRANCH="${2:-}"
      [[ -n "${BASE_BRANCH}" ]] || {
        echo "--base requires a branch name" >&2
        exit 2
      }
      PASSTHROUGH_ARGS+=("$1" "$BASE_BRANCH")
      shift 2
      ;;
    --branch)
      # Pre-merge sized the PR shape margin for this branch.
      PUBLISH_BRANCH="${2-}"
      PASSTHROUGH_ARGS+=("${@:1:2}")
      shift "$(($# > 1 ? 2 : 1))"
      ;;
    --snapshot)
      SNAPSHOT="${2:-}"
      [[ -n "${SNAPSHOT}" ]] || {
        echo "--snapshot requires a value" >&2
        exit 2
      }
      PASSTHROUGH_ARGS+=("$1" "$SNAPSHOT")
      shift 2
      ;;
    --allow-parity-override)
      ALLOW_PARITY_OVERRIDE=1
      shift
      ;;
    --parity-override-reason)
      PARITY_OVERRIDE_REASON="${2:-}"
      [[ -n "${PARITY_OVERRIDE_REASON}" ]] || {
        echo "--parity-override-reason requires a value" >&2
        exit 2
      }
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      PASSTHROUGH_ARGS+=("$1")
      shift
      ;;
  esac
done

HOST_GIT_BIN="$(resolve_fixed_host_tool git /opt/homebrew/bin/git /usr/local/bin/git /usr/bin/git /bin/git)"
HOST_JQ_BIN="$(resolve_fixed_host_tool jq /opt/homebrew/bin/jq /usr/local/bin/jq /usr/bin/jq /bin/jq)"
WORKSPACE="$(resolve_workspace "${WORKSPACE}")"

if [[ "${ALLOW_PARITY_OVERRIDE}" -eq 1 ]]; then
  if [[ -z "${PARITY_OVERRIDE_REASON}" ]]; then
    echo "--allow-parity-override requires --parity-override-reason." >&2
    exit 2
  fi
  echo "repo-publish-pr parity override: ${PARITY_OVERRIDE_REASON}" >&2
elif [[ "${BASE_BRANCH}" == "main" ]]; then
  verify_pr_parity_evidence "${WORKSPACE}" "${SNAPSHOT}" "${BASE_BRANCH}" "${PUBLISH_BRANCH}"
fi

if [[ "${#PASSTHROUGH_ARGS[@]}" -eq 0 ]]; then
  exec "${ROOT_DIR}/scripts/workcell" publish-pr --workspace "${WORKSPACE}"
fi

exec "${ROOT_DIR}/scripts/workcell" publish-pr --workspace "${WORKSPACE}" "${PASSTHROUGH_ARGS[@]}"
