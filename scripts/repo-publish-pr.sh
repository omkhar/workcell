#!/bin/bash -p
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "$(cd "$(/usr/bin/dirname "${BASH_SOURCE[0]}")" && /bin/pwd)/lib/trusted-entrypoint.sh"
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKSPACE="${PWD}"
BASE_BRANCH="main"
SNAPSHOT="worktree"
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

verify_pr_parity_evidence() {
  local workspace="$1"
  local snapshot_mode="$2"
  local base_branch="$3"
  local evidence_dir=""
  local evidence_path=""
  local expected_tree=""

  evidence_dir="$("${HOST_GIT_BIN}" -C "${workspace}" rev-parse --absolute-git-dir)/workcell-parity"
  evidence_path="${evidence_dir}/pr-parity.json"
  if [[ ! -f "${evidence_path}" ]]; then
    echo "Missing local PR-parity evidence for ${workspace}." >&2
    echo "Run ./scripts/pre-merge.sh --profile pr-parity first, or use --allow-parity-override with a reason." >&2
    exit 2
  fi
  expected_tree="$(compute_snapshot_tree "${workspace}" "${snapshot_mode}")"

  "${HOST_JQ_BIN}" -e \
    --arg base "${base_branch}" \
    --arg tree_oid "${expected_tree}" \
    '
      .version == 1 and
      .profile == "pr-parity" and
      .base_branch == $base and
      .tree_oid == $tree_oid
    ' "${evidence_path}" >/dev/null || {
    echo "Local PR-parity evidence does not match the tree being published." >&2
    echo "Expected base=${base_branch} tree_oid=${expected_tree}." >&2
    echo "Re-run ./scripts/pre-merge.sh --profile pr-parity before publishing, or use --allow-parity-override with a reason." >&2
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
  verify_pr_parity_evidence "${WORKSPACE}" "${SNAPSHOT}" "${BASE_BRANCH}"
fi

exec "${ROOT_DIR}/scripts/workcell" publish-pr --workspace "${WORKSPACE}" "${PASSTHROUGH_ARGS[@]}"
