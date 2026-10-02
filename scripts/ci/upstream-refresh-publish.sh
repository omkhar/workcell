#!/usr/bin/env -S BASH_ENV= ENV= bash
# Publish the upstream-refresh candidates as GitHub-signed PRs.
#
# Usage: upstream-refresh-publish.sh CANDIDATE_ROOT SCOPE_GUARD_RESULT AUDIT_FILE
#
# Run from a checkout of the candidate base_sha, with GH_TOKEN set to the
# upstream-refresh GitHub App token. CANDIDATE_ROOT holds a provider and a
# toolchain candidate directory, and either one can be absent. Each candidate
# is published alone from base_sha, so one cannot block or change the other.
# SCOPE_GUARD_RESULT is the guard result for the provider candidate. Only a
# provider candidate with a "passed" result gets auto-merge. Every other PR
# gets the needs-human-review label. The refresh job creates that label. A
# candidate that changes .github/workflows/ is not published, because the App
# has no Workflows permission. AUDIT_FILE receives the tracking-issue audit text.
set -euo pipefail

if [[ $# -eq 3 ]]; then
  : "${GITHUB_REPOSITORY:?}"
  : "${GITHUB_RUN_ID:?}"
  {
    echo "### Auto-publish ${GITHUB_RUN_ID}"
    echo
    echo "- run: ${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}"
  } >"$3"
  status=0
  for kind in provider toolchain; do
    [[ -e "$1/${kind}" ]] || continue
    # Start each candidate from the clean base checkout.
    git reset -q --hard HEAD
    "$0" "$1/${kind}" "$2" "$3" "${kind}" || status=1
  done
  exit "${status}"
fi
if [[ $# -ne 4 ]]; then
  echo "Usage: $0 CANDIDATE_ROOT SCOPE_GUARD_RESULT AUDIT_FILE" >&2
  exit 2
fi
candidate_dir="$1"
scope_result="$2"
audit_file="$3"
kind="$4"
metadata="${candidate_dir}/metadata.json"
patch="${candidate_dir}/patch"
: "${GH_TOKEN:?GH_TOKEN must hold the upstream-refresh App token}"
: "${GITHUB_REPOSITORY:?}"
: "${GITHUB_RUN_ID:?}"

die() {
  echo "upstream-refresh-publish: ${kind}: $*" >&2
  exit 1
}

case "${kind}" in
  provider | toolchain) ;;
  *) die "unknown candidate kind" ;;
esac

meta() { jq -r "$1 // \"\"" "${metadata}"; }

[[ -f "${metadata}" && -f "${patch}" ]] || die "candidate directory is incomplete"
[[ "$(meta .repository)" == "${GITHUB_REPOSITORY}" ]] || die "candidate repository mismatch"
[[ "$(meta .workflow)" == "upstream-refresh" ]] || die "candidate workflow mismatch"
[[ "$(meta .candidate)" == "${kind}" ]] || die "candidate kind mismatch"
[[ "$(meta .run_id)" == "${GITHUB_RUN_ID}" ]] || die "candidate run id mismatch"
[[ "$(meta .base_ref)" == "refs/heads/main" ]] || die "candidate base ref must be refs/heads/main"
base_sha="$(meta .base_sha)"
tree_oid="$(meta .tree_oid)"
[[ "$(shasum -a 256 "${patch}" | awk '{print $1}')" == "$(meta .patch_sha256)" ]] || die "candidate patch digest mismatch"
[[ "$(git rev-parse HEAD)" == "${base_sha}" ]] || die "checkout is not at candidate base ${base_sha}"

if [[ "${kind}" == provider ]]; then
  title="Refresh pinned upstreams (provider)"
else
  title="Refresh pinned upstreams (toolchain)"
fi
branch="codex/upstream-refresh-${kind}-${GITHUB_RUN_ID}"
# A legacy mixed refresh PR blocks both kinds.
existing_pr="$(gh pr list --repo "${GITHUB_REPOSITORY}" --state open --base main --limit 1000 --json title,url,headRefName,headRefOid,isCrossRepository \
  --jq 'map(select(.isCrossRepository | not) | select(.title == "Refresh pinned upstreams" or (.headRefName | test("^codex/upstream-refresh-([0-9]+$|'"${kind}"'-)")))) | .[0] // empty')"
# A PR from this run means an earlier attempt stopped after it opened the PR.
# Resume its disposition. The commit checks below still run against its head.
resume=0
if [[ -n "${existing_pr}" ]]; then
  if [[ "$(jq -r .headRefName <<<"${existing_pr}")" == "${branch}" ]]; then
    resume=1
    pr_url="$(jq -r .url <<<"${existing_pr}")"
    commit_oid="$(jq -r .headRefOid <<<"${existing_pr}")"
  else
    echo "- ${kind}: skipped, refresh PR already open: $(jq -r .url <<<"${existing_pr}")" >>"${audit_file}"
    exit 0
  fi
fi

# A newer main makes a new candidate stale. The next scheduled run rebuilds it.
# A resumed PR is already published, so staleness does not apply to it.
if [[ "${resume}" == 0 ]]; then
  remote_main="$(gh api "repos/${GITHUB_REPOSITORY}/git/ref/heads/main" --jq .object.sha)"
  if [[ "${remote_main}" != "${base_sha}" ]]; then
    echo "- ${kind}: skipped, candidate base ${base_sha} is stale against main ${remote_main}" >>"${audit_file}"
    echo "::notice::The ${kind} candidate is stale. The next run will rebuild it."
    exit 0
  fi
fi

# Apply the patch and prove it is the candidate tree.
git apply --index --binary "${patch}"
[[ "$(git write-tree)" == "${tree_oid}" ]] || die "applied tree does not match candidate tree ${tree_oid}"

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

# Reject symlinks, submodules, and any mode change. createCommitOnBranch
# writes regular files only. Capture each git listing and its exit status
# before the loop, so a git failure cannot look like an empty change set.
git diff --cached --raw --no-renames >"${work}/raw" || die "git diff --raw failed"
git diff --cached --name-status --no-renames -z >"${work}/name-status" || die "git diff --name-status failed"
while IFS=$' \t' read -r old_mode new_mode _ _ status_path; do
  old_mode="${old_mode#:}"
  for mode in "${old_mode}" "${new_mode}"; do
    case "${mode}" in
      000000 | 100644 | 100755) ;;
      *) die "unsupported file mode ${mode}: ${status_path}" ;;
    esac
  done
  if [[ "${old_mode}" != 000000 && "${new_mode}" != 000000 && "${old_mode}" != "${new_mode}" ]]; then
    die "mode change is not allowed: ${status_path}"
  fi
  if [[ "${old_mode}" == 000000 && "${new_mode}" == 100755 ]]; then
    die "new executable file is not allowed: ${status_path}"
  fi
done <"${work}/raw"
# The App has no Workflows permission, so a person publishes this candidate.
git diff --cached --name-only --no-renames -z >"${work}/names" || die "git diff --name-only failed"
workflow_path="$(tr '\0' '\n' <"${work}/names" | grep -m1 '^\.github/workflows/' || true)"
if [[ -n "${workflow_path}" ]]; then
  echo "- ${kind}: not published, it changes ${workflow_path}. Publish it from a host: \`./scripts/publish-upstream-refresh-pr.sh --run-id ${GITHUB_RUN_ID} --candidate ${kind}\`" >>"${audit_file}"
  echo "::notice::The ${kind} candidate changes .github/workflows/. Publish it from a host."
  exit 0
fi

# Build the createCommitOnBranch file changes.
: >"${work}/additions"
: >"${work}/deletions"
while IFS= read -r -d '' status && IFS= read -r -d '' path; do
  case "${status}" in
    D) jq -n --arg path "${path}" '{path: $path}' >>"${work}/deletions" ;;
    A | M)
      # Read the staged blob, not the workspace path, so a swapped file
      # cannot reach GitHub before the tree check.
      git cat-file blob ":${path}" >"${work}/blob" || die "cannot read staged blob: ${path}"
      base64 <"${work}/blob" | tr -d '\n' >"${work}/content"
      jq -n --arg path "${path}" --rawfile contents "${work}/content" '{path: $path, contents: $contents}' >>"${work}/additions"
      ;;
    *) die "unsupported change status ${status}: ${path}" ;;
  esac
done <"${work}/name-status"

cleanup_branch() {
  [[ "${resume}" == 1 ]] || gh api -X DELETE "repos/${GITHUB_REPOSITORY}/git/refs/heads/${branch}" >/dev/null 2>&1 || true
}
fail() {
  cleanup_branch
  die "$*"
}
if [[ "${resume}" == 0 ]]; then
  if lookup="$(gh api "repos/${GITHUB_REPOSITORY}/git/ref/heads/${branch}" 2>&1)"; then
    gh api -X DELETE "repos/${GITHUB_REPOSITORY}/git/refs/heads/${branch}" >/dev/null
  elif [[ "${lookup}" != *"HTTP 404"* ]]; then
    die "cannot look up branch ${branch}: ${lookup}"
  fi
  gh api "repos/${GITHUB_REPOSITORY}/git/refs" -f "ref=refs/heads/${branch}" -f "sha=${base_sha}" >/dev/null
  headline="^F ${title} (upstream maintenance; auto-publish)"
  jq -n \
    --arg repo "${GITHUB_REPOSITORY}" --arg branch "${branch}" --arg base "${base_sha}" --arg headline "${headline}" \
    --arg body "Apply the exact upstream ${kind} refresh candidate from run ${GITHUB_RUN_ID}." \
    --slurpfile additions "${work}/additions" --slurpfile deletions "${work}/deletions" \
    '{
      query: "mutation($input: CreateCommitOnBranchInput!) { createCommitOnBranch(input: $input) { commit { oid } } }",
      variables: {input: {
        branch: {repositoryNameWithOwner: $repo, branchName: $branch},
        message: {headline: $headline, body: $body},
        expectedHeadOid: $base,
        fileChanges: {additions: $additions, deletions: $deletions}
      }}
    }' >"${work}/request.json"
  commit_oid="$(gh api graphql --input "${work}/request.json" --jq .data.createCommitOnBranch.commit.oid)" || fail "createCommitOnBranch failed"
  [[ -n "${commit_oid}" && "${commit_oid}" != null ]] || fail "createCommitOnBranch returned no commit"
fi

commit_json="$(gh api "repos/${GITHUB_REPOSITORY}/git/commits/${commit_oid}")"
[[ "$(jq -r .tree.sha <<<"${commit_json}")" == "${tree_oid}" ]] || fail "GitHub commit tree does not match candidate tree ${tree_oid}"
[[ "$(jq -r '.parents | map(.sha) | join(" ")' <<<"${commit_json}")" == "${base_sha}" ]] || fail "GitHub commit parent is not candidate base ${base_sha}"
[[ "$(jq -r .verification.verified <<<"${commit_json}")" == true ]] || fail "GitHub did not sign commit ${commit_oid}"

body_file="${work}/pr-body.md"
cat >"${body_file}" <<EOF
## Summary

- apply the exact upstream ${kind} refresh candidate from the reviewed workflow
- the commit is signed by GitHub and its tree equals the candidate tree

## Candidate

- run: $(meta .run_url)
- base sha: \`${base_sha}\`
- patch sha256: \`$(meta .patch_sha256)\`
- tree oid: \`${tree_oid}\`
- candidate: ${kind}
- scope guard: ${scope_result}

## Diffstat

\`\`\`
$(cat "${candidate_dir}/diffstat")
\`\`\`

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
if [[ "${resume}" == 0 ]]; then
  pr_url="$(gh pr create --repo "${GITHUB_REPOSITORY}" --base main --head "${branch}" --title "${title}" --body-file "${body_file}")" || fail "gh pr create failed"
fi

# The scope guard checks only the provider candidate.
if [[ "${kind}" == provider && "${scope_result}" == passed ]]; then
  # Enable auto-merge only for the commit that passed the scope guard and tree check.
  gh pr merge --repo "${GITHUB_REPOSITORY}" --auto --merge --match-head-commit "${commit_oid}" "${pr_url}"
  merge_line="auto-merge enabled (scope guard passed)"
else
  gh pr edit "${pr_url}" --repo "${GITHUB_REPOSITORY}" --add-label needs-human-review >/dev/null
  merge_line="labeled needs-human-review (scope guard did not pass, or a toolchain candidate)"
fi

cooloff_hours="$(awk -F= '/^cooloff_hours[[:space:]]*=/ {gsub(/[[:space:]]/, "", $2); print $2}' policy/provider-bumps.toml)"
{
  echo "- ${kind} PR: ${pr_url}"
  echo "- commit: \`${commit_oid}\` (GitHub-verified, tree matches candidate)"
  echo "- merge: ${merge_line}"
  echo "- cool-off: ${cooloff_hours}h policy; the candidate selects only releases older than the cutoff"
  echo "- provenance: the ${kind} updater --apply and --check passed in the refresh job, including the verify-upstream-*-release.sh checks"
  echo "- versions:"
  grep -E '^\+ARG (CLAUDE|CODEX|COPILOT|GEMINI)_[A-Z0-9_]*VERSION=' "${patch}" | sed -e 's/^+ARG /  - /' || true
  if [[ "${kind}" == provider && -s "${candidate_dir}/../provider-summary" ]]; then
    echo
    echo '```'
    cat "${candidate_dir}/../provider-summary"
    echo '```'
  fi
} >>"${audit_file}"
echo "${merge_line}: ${pr_url}"
