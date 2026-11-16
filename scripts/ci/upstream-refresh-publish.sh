#!/usr/bin/env -S BASH_ENV= ENV= bash
# Publish the upstream-refresh candidates as GitHub-signed PRs.
#
# Usage: upstream-refresh-publish.sh CANDIDATE_ROOT SCOPE_GUARD_RESULT TOOLCHAIN_GUARD_VERDICT TOOLCHAIN_REDERIVE_VERDICT AUDIT_FILE
#
# Run from a checkout of the candidate base_sha, with GH_TOKEN set to the
# upstream-refresh GitHub App token. CANDIDATE_ROOT holds a provider and a
# toolchain candidate directory, and either one can be absent. Each candidate
# is published alone from base_sha, so one cannot block or change the other.
# SCOPE_GUARD_RESULT is the guard result for the provider candidate. Only a
# provider candidate with a "passed" result gets auto-merge. Every other PR
# gets the needs-human-review label. The refresh job creates that label.
# TOOLCHAIN_GUARD_VERDICT is passed:<base sha>:<tree oid> when the toolchain
# scope guard passed. TOOLCHAIN_REDERIVE_VERDICT is rederived:<base sha>:<tree
# oid> when a separate job ran the toolchain updater from the base again and
# got the same tree. The guard checks that each changed line is a pin line; the
# re-derivation checks that the pins are the ones upstream publishes. The App
# publishes the toolchain candidate only when both passed, and only when both
# name the candidate's base and tree. A toolchain PR never gets auto-merge. A
# candidate that changes .github/workflows/ is not published, because the App
# has no Workflows permission. A person publishes a held candidate from a host.
# AUDIT_FILE receives the tracking-issue audit text.
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/ci/upstream-refresh-publish.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

if [[ $# -eq 5 ]]; then
  : "${GITHUB_REPOSITORY:?}"
  : "${GITHUB_RUN_ID:?}"
  {
    echo "### Auto-publish ${GITHUB_RUN_ID}"
    echo
    echo "- run: ${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}"
  } >"$5"
  status=0
  for kind in provider toolchain; do
    [[ -e "$1/${kind}" ]] || continue
    # Start each candidate from the clean base checkout.
    git reset -q --hard HEAD
    "${workcell_entry_root}/scripts/ci/upstream-refresh-publish.sh" --candidate "$1/${kind}" "$2" "$3" "$4" "$5" "${kind}" || status=1
  done
  exit "${status}"
fi
if [[ $# -ne 7 || "$1" != --candidate ]]; then
  echo "Usage: $0 CANDIDATE_ROOT SCOPE_GUARD_RESULT TOOLCHAIN_GUARD_VERDICT TOOLCHAIN_REDERIVE_VERDICT AUDIT_FILE" >&2
  exit 2
fi
candidate_dir="$2"
scope_result="$3"
toolchain_verdict="$4"
rederive_verdict="$5"
audit_file="$6"
kind="$7"
metadata="${candidate_dir}/metadata.json"
patch="${candidate_dir}/patch"
: "${GH_TOKEN:?GH_TOKEN must hold the upstream-refresh App token}"
: "${GITHUB_REPOSITORY:?}"
: "${GITHUB_RUN_ID:?}"
host_command="./scripts/publish-upstream-refresh-pr.sh --run-id ${GITHUB_RUN_ID} --candidate ${kind}"

die() {
  echo "upstream-refresh-publish: ${kind}: $*" >&2
  exit 1
}

case "${kind}" in
  provider | toolchain) ;;
  *) die "unknown candidate kind" ;;
esac

# Each toolchain verdict names the base and the tree it checked. Without both
# verdicts the App does not publish the toolchain candidate. This hold comes
# before any GitHub call. Each verdict covers only the toolchain kind.
guard_base=""
guard_tree=""
rederive_base=""
rederive_tree=""
if [[ "${kind}" == toolchain ]]; then
  if [[ ! "${toolchain_verdict}" =~ ^passed:([0-9a-f]{40}):([0-9a-f]{40})$ ]]; then
    echo "- toolchain: not published by the App, the toolchain scope guard did not pass. Review the candidate, then publish it from a host: \`${host_command}\`" >>"${audit_file}"
    echo "::notice::The toolchain scope guard did not pass, so the App does not publish the toolchain candidate."
    exit 0
  fi
  guard_base="${BASH_REMATCH[1]}"
  guard_tree="${BASH_REMATCH[2]}"
  if [[ ! "${rederive_verdict}" =~ ^rederived:([0-9a-f]{40}):([0-9a-f]{40})$ ]]; then
    echo "- toolchain: not published by the App, the rederive job did not get the same tree from the base. Upstream may have moved since the refresh. Review the candidate, then publish it from a host: \`${host_command}\`" >>"${audit_file}"
    echo "::notice::The toolchain candidate was not re-derived, so the App does not publish it."
    exit 0
  fi
  rederive_base="${BASH_REMATCH[1]}"
  rederive_tree="${BASH_REMATCH[2]}"
  scope_result="passed on tree ${guard_tree}"
fi

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
if [[ "${kind}" == toolchain ]]; then
  # The tree check below then binds the published commit to the checked tree.
  [[ "${guard_base}" == "${base_sha}" ]] || die "the toolchain scope guard checked base ${guard_base}, not candidate base ${base_sha}"
  [[ "${guard_tree}" == "${tree_oid}" ]] || die "the toolchain scope guard checked tree ${guard_tree}, not candidate tree ${tree_oid}"
  [[ "${rederive_base}" == "${base_sha}" ]] || die "the rederive job used base ${rederive_base}, not candidate base ${base_sha}"
  [[ "${rederive_tree}" == "${tree_oid}" ]] || die "the rederive job got tree ${rederive_tree}, not candidate tree ${tree_oid}"
fi

if [[ "${kind}" == provider ]]; then
  title="Refresh pinned upstreams (provider)"
else
  title="Refresh pinned upstreams (toolchain)"
fi
branch="codex/upstream-refresh-${kind}-${GITHUB_RUN_ID}"
# A legacy mixed refresh PR blocks both kinds.
existing_pr="$(gh pr list --repo "${GITHUB_REPOSITORY}" --state open --base main --limit 1000 --json title,url,headRefName,headRefOid,isCrossRepository |
  jq -L "${workcell_entry_root}/scripts/ci" -c --arg kind "${kind}" 'include "upstream-refresh-overlap"; overlap($kind) // empty')"
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
  echo "- ${kind}: not published, it changes ${workflow_path}. Publish it from a host: \`${host_command}\`" >>"${audit_file}"
  echo "::notice::The ${kind} candidate changes .github/workflows/. Publish it from a host."
  exit 0
fi

# A toolchain PR lists each pin line it changes, old then new, so a reviewer
# sees every version and digest. The guard allowed only pin lines, so the
# list is the whole change.
: >"${work}/pins"
if [[ "${kind}" == toolchain ]]; then
  git diff --cached --no-renames --no-color --no-ext-diff -U0 >"${work}/pin-diff" || die "git diff of the pins failed"
  awk '/^diff --git /{header=1; next} header && /^\+\+\+ b\//{print substr($0, 7); next} /^@@/{header=0; next} !header && /^[-+]/{print}' "${work}/pin-diff" >"${work}/pins"
  [[ -s "${work}/pins" ]] || die "the toolchain candidate changes no pin line"
fi
pins_section=""
if [[ -s "${work}/pins" ]]; then
  pins_section="## Pins

\`\`\`diff
$(cat "${work}/pins")
\`\`\`
"
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

# The body takes no text from the artifact that the checks above did not
# cover: the diffstat comes from the applied tree and the run URL from the
# workflow context.
git diff --cached --stat --no-color >"${work}/diffstat" || die "git diff --stat failed"
run_url="${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}"
rederive_line=""
if [[ "${kind}" == toolchain ]]; then
  rederive_line="- re-derived: the rederive job got tree ${rederive_tree} from base ${rederive_base}
"
fi
body_file="${work}/pr-body.md"
cat >"${body_file}" <<EOF
## Summary

- apply the exact upstream ${kind} refresh candidate from the reviewed workflow
- the commit is signed by GitHub and its tree equals the candidate tree

## Candidate

- run: ${run_url}
- base sha: \`${base_sha}\`
- patch sha256: \`$(meta .patch_sha256)\`
- tree oid: \`${tree_oid}\`
- candidate: ${kind}
- scope guard: ${scope_result}
${rederive_line}
${pins_section}## Diffstat

\`\`\`
$(cat "${work}/diffstat")
\`\`\`

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
if [[ "${resume}" == 0 ]]; then
  pr_url="$(gh pr create --repo "${GITHUB_REPOSITORY}" --base main --head "${branch}" --title "${title}" --body-file "${body_file}")" || fail "gh pr create failed"
fi

# Only a provider candidate that passed its guard gets auto-merge. A toolchain
# PR always waits for a person, even when its guard passed.
if [[ "${kind}" == provider && "${scope_result}" == passed ]]; then
  # Enable auto-merge only for the commit that passed the scope guard and tree check.
  # The head match applies at arming time only; a later writer push is an accepted risk.
  gh pr merge --repo "${GITHUB_REPOSITORY}" --auto --merge --match-head-commit "${commit_oid}" "${pr_url}"
  merge_line="auto-merge enabled (scope guard passed)"
else
  gh pr edit "${pr_url}" --repo "${GITHUB_REPOSITORY}" --add-label needs-human-review >/dev/null
  if [[ "${kind}" == toolchain ]]; then
    merge_line="labeled needs-human-review, no auto-merge (toolchain scope guard passed on this tree)"
  else
    merge_line="labeled needs-human-review (scope guard did not pass)"
  fi
fi

cooloff_hours="$(awk -F= '/^cooloff_hours[[:space:]]*=/ {gsub(/[[:space:]]/, "", $2); print $2}' policy/provider-bumps.toml)"
{
  echo "- ${kind} PR: ${pr_url}"
  echo "- commit: \`${commit_oid}\` (GitHub-verified, tree matches candidate)"
  echo "- merge: ${merge_line}"
  if [[ "${kind}" == provider ]]; then
    echo "- cool-off: ${cooloff_hours}h policy; the candidate selects only releases older than the cutoff"
  fi
  if [[ "${kind}" == provider ]]; then
    echo "- provenance: the provider updater --apply and --check passed in the refresh job, including the verify-upstream-*-release.sh checks"
  else
    echo "- provenance: the rederive job, on its own runner with a read-only token, ran the toolchain updater from the base with the candidate's Debian snapshot and got this tree. The toolchain scope guard allowed each changed line."
  fi
  if [[ "${kind}" == provider ]]; then
    echo "- versions:"
    grep -E '^\+ARG (CLAUDE|CODEX|COPILOT|GEMINI)_[A-Z0-9_]*VERSION=' "${patch}" | sed -e 's/^+ARG /  - /' || true
  else
    echo "- pins, each old line then its new line:"
    echo
    echo '```diff'
    cat "${work}/pins"
    echo '```'
  fi
  if [[ "${kind}" == provider && -s "${candidate_dir}/../provider-summary" ]]; then
    echo
    echo '```'
    cat "${candidate_dir}/../provider-summary"
    echo '```'
  fi
} >>"${audit_file}"
echo "${merge_line}: ${pr_url}"
