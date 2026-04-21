#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${HOME:-/tmp}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "publish-upstream-refresh-pr-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASE_BRANCH="main"
RUN_ID=""
ISSUE_NUMBER=""

usage() {
  cat <<'EOF'
Usage: scripts/publish-upstream-refresh-pr.sh --run-id RUN_ID [--issue-number NUMBER] [--base main]

Downloads an upstream-refresh candidate artifact from GitHub Actions, recreates
the refresh locally from the latest selected base branch, verifies the
candidate identity exactly, runs local PR-parity validation, and publishes a
signed draft PR through the repo-local parity-enforcing publication wrapper.

Run this from a clean worktree. The disposable publication branch is created
from the latest available tip of the selected base branch, not the caller's
current feature branch HEAD.
EOF
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

resolve_base_ref() {
  if git -C "${ROOT_DIR}" remote get-url origin >/dev/null 2>&1; then
    git -C "${ROOT_DIR}" fetch origin "${BASE_BRANCH}" >/dev/null
    if git -C "${ROOT_DIR}" rev-parse --verify --quiet "refs/remotes/origin/${BASE_BRANCH}" >/dev/null; then
      printf 'refs/remotes/origin/%s\n' "${BASE_BRANCH}"
      return 0
    fi
  fi

  if git -C "${ROOT_DIR}" rev-parse --verify --quiet "refs/heads/${BASE_BRANCH}" >/dev/null; then
    printf 'refs/heads/%s\n' "${BASE_BRANCH}"
    return 0
  fi

  echo "Unable to resolve base branch ${BASE_BRANCH} locally or from origin." >&2
  exit 2
}

compute_worktree_tree_oid() {
  local workspace="$1"
  local tmp_index=""
  local tree_oid=""

  tmp_index="$(mktemp "${TMPDIR:-/tmp}/workcell-upstream-refresh-index.XXXXXX")"
  rm -f "${tmp_index}"
  trap 'rm -f "${tmp_index}"' RETURN
  GIT_INDEX_FILE="${tmp_index}" git -C "${workspace}" read-tree HEAD
  GIT_INDEX_FILE="${tmp_index}" git -C "${workspace}" add -A
  tree_oid="$(GIT_INDEX_FILE="${tmp_index}" git -C "${workspace}" write-tree)"
  rm -f "${tmp_index}"
  trap - RETURN
  printf '%s\n' "${tree_oid}"
}

compute_patch_sha256() {
  local patch_path="$1"
  shasum -a 256 "${patch_path}" | awk '{print $1}'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --run-id)
      RUN_ID="${2:-}"
      [[ -n "${RUN_ID}" ]] || {
        echo "--run-id requires a value." >&2
        exit 2
      }
      shift 2
      ;;
    --issue-number)
      ISSUE_NUMBER="${2:-}"
      [[ -n "${ISSUE_NUMBER}" ]] || {
        echo "--issue-number requires a value." >&2
        exit 2
      }
      shift 2
      ;;
    --base)
      BASE_BRANCH="${2:-}"
      [[ -n "${BASE_BRANCH}" ]] || {
        echo "--base requires a branch name." >&2
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

require_tool git
require_tool gh
require_tool go
require_tool jq
require_tool mktemp
require_tool shasum

if [[ -z "${RUN_ID}" ]]; then
  echo "scripts/publish-upstream-refresh-pr.sh requires --run-id." >&2
  exit 2
fi
if [[ "${BASE_BRANCH}" != "main" ]]; then
  echo "scripts/publish-upstream-refresh-pr.sh only supports --base main." >&2
  exit 2
fi
if [[ -n "$(git -C "${ROOT_DIR}" status --short)" ]]; then
  echo "scripts/publish-upstream-refresh-pr.sh requires a clean worktree." >&2
  echo "Commit, stash, or discard local changes first so the disposable PR worktree can be reviewed against ${BASE_BRANCH} cleanly." >&2
  exit 2
fi

REPO="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
base_ref="$(resolve_base_ref)"
base_sha="$(git -C "${ROOT_DIR}" rev-parse "${base_ref}")"

run_json="$(gh api "repos/${REPO}/actions/runs/${RUN_ID}")"
run_conclusion="$(jq -r '.conclusion // ""' <<<"${run_json}")"
run_head_branch="$(jq -r '.head_branch // ""' <<<"${run_json}")"
run_head_sha="$(jq -r '.head_sha // ""' <<<"${run_json}")"
run_html_url="$(jq -r '.html_url // ""' <<<"${run_json}")"
run_path="$(jq -r '.path // ""' <<<"${run_json}")"
if [[ "${run_conclusion}" != "success" ]]; then
  echo "Workflow run ${RUN_ID} is not successful: ${run_conclusion:-unknown}." >&2
  exit 2
fi
if [[ "${run_head_branch}" != "${BASE_BRANCH}" ]]; then
  echo "Workflow run ${RUN_ID} must target ${BASE_BRANCH}, found ${run_head_branch:-unknown}." >&2
  exit 2
fi
if [[ "${run_path}" != ".github/workflows/upstream-refresh.yml"* ]]; then
  echo "Workflow run ${RUN_ID} does not come from the reviewed upstream-refresh workflow on ${BASE_BRANCH}." >&2
  exit 2
fi

artifact_root="$(mktemp -d "${TMPDIR:-/tmp}/workcell-upstream-refresh-artifact.XXXXXX")"
worktree_root="$(mktemp -d "${TMPDIR:-/tmp}/workcell-upstream-refresh.XXXXXX")"
title_file="$(mktemp "${TMPDIR:-/tmp}/workcell-upstream-refresh-title.XXXXXX")"
body_file="$(mktemp "${TMPDIR:-/tmp}/workcell-upstream-refresh-body.XXXXXX.md")"
commit_file="$(mktemp "${TMPDIR:-/tmp}/workcell-upstream-refresh-commit.XXXXXX.txt")"

cleanup() {
  git -C "${ROOT_DIR}" worktree remove --force "${worktree_root}" >/dev/null 2>&1 || true
  git -C "${ROOT_DIR}" worktree prune >/dev/null 2>&1 || true
  rm -rf "${artifact_root}"
  rm -f "${title_file}" "${body_file}" "${commit_file}" "${local_patch_path:-}"
}
trap cleanup EXIT

gh run download "${RUN_ID}" --repo "${REPO}" --name upstream-refresh-candidate --dir "${artifact_root}" >/dev/null

metadata_path="$(find "${artifact_root}" -type f -name metadata.json -print | head -n 1)"
patch_path="$(find "${artifact_root}" -type f -name patch -print | head -n 1)"
diffstat_path="$(find "${artifact_root}" -type f -name diffstat -print | head -n 1)"
if [[ -z "${metadata_path}" || -z "${patch_path}" || -z "${diffstat_path}" ]]; then
  echo "Run ${RUN_ID} does not contain a complete upstream-refresh candidate artifact." >&2
  exit 2
fi

candidate_repository="$(jq -r '.repository // ""' "${metadata_path}")"
candidate_workflow="$(jq -r '.workflow // ""' "${metadata_path}")"
candidate_run_id="$(jq -r '.run_id // ""' "${metadata_path}")"
candidate_base_ref="$(jq -r '.base_ref // ""' "${metadata_path}")"
candidate_base_sha="$(jq -r '.base_sha // ""' "${metadata_path}")"
candidate_patch_sha256="$(jq -r '.patch_sha256 // ""' "${metadata_path}")"
candidate_tree_oid="$(jq -r '.tree_oid // ""' "${metadata_path}")"
candidate_changed_files="$(jq -c '.changed_files // []' "${metadata_path}")"
if [[ "${candidate_repository}" != "${REPO}" || "${candidate_workflow}" != "upstream-refresh" || "${candidate_run_id}" != "${RUN_ID}" ]]; then
  echo "Run ${RUN_ID} candidate metadata does not match the requested repository or workflow." >&2
  exit 2
fi
if [[ "${candidate_base_ref}" != "refs/heads/${BASE_BRANCH}" ]]; then
  echo "Run ${RUN_ID} candidate base ref must be refs/heads/${BASE_BRANCH}, found ${candidate_base_ref:-unknown}." >&2
  exit 2
fi
if [[ "${candidate_base_sha}" != "${run_head_sha}" ]]; then
  echo "Run ${RUN_ID} candidate metadata base SHA does not match the workflow run head SHA." >&2
  exit 2
fi
if [[ "${candidate_base_sha}" != "${base_sha}" ]]; then
  echo "Run ${RUN_ID} candidate base SHA ${candidate_base_sha} is stale relative to ${BASE_BRANCH} tip ${base_sha}." >&2
  exit 2
fi

tracking_issue_json="$(
  gh issue list \
    --repo "${REPO}" \
    --state all \
    --label "upstream-refresh-candidate" \
    --limit 10 \
    --json number,url
)"
tracking_issue_count="$(jq 'length' <<<"${tracking_issue_json}")"
if [[ "${tracking_issue_count}" -gt 1 ]]; then
  echo "Expected at most one upstream-refresh-candidate issue, found ${tracking_issue_count}." >&2
  exit 2
fi
if [[ "${tracking_issue_count}" -eq 0 ]]; then
  echo "Missing the canonical upstream-refresh-candidate issue for run ${RUN_ID}." >&2
  exit 2
fi
tracking_issue_number="$(jq -r '.[0].number' <<<"${tracking_issue_json}")"
tracking_issue_url="$(jq -r '.[0].url' <<<"${tracking_issue_json}")"
if [[ -n "${ISSUE_NUMBER}" && "${ISSUE_NUMBER}" != "${tracking_issue_number}" ]]; then
  echo "Requested issue ${ISSUE_NUMBER} does not match the canonical upstream-refresh-candidate issue ${tracking_issue_number}." >&2
  exit 2
fi

existing_pr_url="$(
  gh pr list \
    --repo "${REPO}" \
    --state open \
    --base "${BASE_BRANCH}" \
    --json title,url,headRefName \
    --jq 'map(select(.title == "Refresh pinned upstreams" or (.headRefName | startswith("codex/upstream-refresh-")))) | .[0].url // ""'
)"
if [[ -n "${existing_pr_url}" ]]; then
  echo "Refusing to publish while an upstream refresh PR is already open: ${existing_pr_url}" >&2
  exit 2
fi

git -C "${ROOT_DIR}" worktree add --detach "${worktree_root}" "${base_ref}" >/dev/null

"${worktree_root}/scripts/update-upstream-pins.sh" --apply
if git -C "${worktree_root}" diff --quiet --exit-code; then
  echo "Local upstream refresh produced no changes; candidate ${RUN_ID} is stale or mismatched." >&2
  exit 2
fi
"${worktree_root}/scripts/update-upstream-pins.sh" --check
"${worktree_root}/scripts/check-pinned-inputs.sh"

local_patch_path="$(mktemp "${TMPDIR:-/tmp}/workcell-upstream-refresh-patch.XXXXXX")"
git -C "${worktree_root}" diff --binary --full-index --patch --no-ext-diff --no-color >"${local_patch_path}"
local_patch_sha256="$(compute_patch_sha256 "${local_patch_path}")"
local_tree_oid="$(compute_worktree_tree_oid "${worktree_root}")"
local_changed_files="$(git -C "${worktree_root}" diff --name-only | jq -R . | jq -s -c .)"
if [[ "${local_patch_sha256}" != "${candidate_patch_sha256}" ]]; then
  echo "Candidate patch digest mismatch: ${local_patch_sha256} != ${candidate_patch_sha256}." >&2
  exit 2
fi
if [[ "${local_tree_oid}" != "${candidate_tree_oid}" ]]; then
  echo "Candidate tree OID mismatch: ${local_tree_oid} != ${candidate_tree_oid}." >&2
  exit 2
fi
if [[ "${local_changed_files}" != "${candidate_changed_files}" ]]; then
  echo "Candidate changed-file list mismatch." >&2
  exit 2
fi

"${worktree_root}/scripts/pre-merge.sh" --profile pr-parity --allow-dirty

title="Refresh pinned upstreams"
branch_name="codex/upstream-refresh-${RUN_ID}"

printf '%s\n' "${title}" >"${title_file}"
cat >"${body_file}" <<EOF
## Summary

- refresh provider pins and Linux base/toolchain inputs from reviewed upstream metadata
- refresh release-build helper versions, image digests, and workflow-managed install tools

## Candidate

- run: ${run_html_url}
- tracking issue: ${tracking_issue_url}
- base sha: \`${candidate_base_sha}\`
- patch sha256: \`${candidate_patch_sha256}\`
- tree oid: \`${candidate_tree_oid}\`

## Validation

- \`./scripts/update-upstream-pins.sh --check\`
- \`./scripts/check-pinned-inputs.sh\`
- \`./scripts/pre-merge.sh --profile pr-parity --allow-dirty\`

## Diffstat

\`\`\`
$(cat "${diffstat_path}")
\`\`\`
EOF

cat >"${commit_file}" <<'EOF'
Refresh pinned upstreams

- refresh provider pins and Linux base/toolchain inputs
- refresh release-build helper versions and image digests
EOF

"${ROOT_DIR}/scripts/repo-publish-pr.sh" \
  --workspace "${worktree_root}" \
  --branch "${branch_name}" \
  --base "${BASE_BRANCH}" \
  --title-file "${title_file}" \
  --body-file "${body_file}" \
  --commit-message-file "${commit_file}"
