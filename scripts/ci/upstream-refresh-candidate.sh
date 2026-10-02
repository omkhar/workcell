#!/usr/bin/env -S BASH_ENV= ENV= bash
# Capture the working-tree refresh as one upstream-refresh candidate.
#
# Usage: upstream-refresh-candidate.sh KIND OUT_ROOT
#
# KIND is "provider" or "toolchain". Run from the repository root after the
# updater for KIND applied its changes. When the tree changed, the script
# writes OUT_ROOT/KIND/{patch,diffstat,metadata.json}. It then restores the
# tree to HEAD, so the next candidate starts from the same base and the two
# candidates never depend on each other.
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "Usage: $0 KIND OUT_ROOT" >&2
  exit 2
fi
kind="$1"
case "${kind}" in
  provider | toolchain) ;;
  *)
    echo "upstream-refresh-candidate: unknown candidate kind: ${kind}" >&2
    exit 2
    ;;
esac
candidate_dir="$2/${kind}"
: "${GITHUB_REPOSITORY:?}"
: "${GITHUB_RUN_ID:?}"
: "${GITHUB_REF:?}"

if git diff --quiet --exit-code; then
  echo "No ${kind} refresh changes."
  exit 0
fi
mkdir -p "${candidate_dir}"

git diff --binary --full-index --patch --no-ext-diff --no-color >"${candidate_dir}/patch"
git diff --stat --no-color >"${candidate_dir}/diffstat"

tmp_index="$(mktemp "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/upstream-refresh-index.XXXXXX")"
rm -f "${tmp_index}"
trap 'rm -f "${tmp_index}"' EXIT
GIT_INDEX_FILE="${tmp_index}" git read-tree HEAD
GIT_INDEX_FILE="${tmp_index}" git add -A
tree_oid="$(GIT_INDEX_FILE="${tmp_index}" git write-tree)"

patch_sha256="$(shasum -a 256 "${candidate_dir}/patch" | awk '{print $1}')"
changed_files_json="$(git diff --name-only | jq -R . | jq -s .)"
jq -n \
  --arg repository "${GITHUB_REPOSITORY}" \
  --arg workflow "upstream-refresh" \
  --arg run_url "${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}" \
  --arg artifact_name "upstream-refresh-candidate" \
  --arg candidate "${kind}" \
  --arg base_ref "${GITHUB_REF}" \
  --arg base_sha "$(git rev-parse HEAD)" \
  --arg patch_sha256 "${patch_sha256}" \
  --arg tree_oid "${tree_oid}" \
  --argjson changed_files "${changed_files_json}" \
  '{
    version: 1,
    repository: $repository,
    workflow: $workflow,
    run_id: (env.GITHUB_RUN_ID | tonumber),
    run_url: $run_url,
    artifact_name: $artifact_name,
    candidate: $candidate,
    base_ref: $base_ref,
    base_sha: $base_sha,
    patch_sha256: $patch_sha256,
    tree_oid: $tree_oid,
    changed_files: $changed_files
  }' >"${candidate_dir}/metadata.json"

git reset -q --hard HEAD
git clean -fdq
echo "Wrote the ${kind} candidate to ${candidate_dir}."
