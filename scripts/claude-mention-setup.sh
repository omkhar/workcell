#!/usr/bin/env bash
# One-time setup for the @claude mention workflow, run by the repository owner
# from the workcell clone on branch claude/claude-mention:
#
#   bash scripts/claude-mention-setup.sh
#
# It checks the pinned action against the v1 tag, runs the workflow checks,
# creates the claude-mention environment (deployments from main only), stores
# the Claude token there, then removes this script from the branch, re-signs
# the branch with your key and pushes it. The script never lands on main.
set -euo pipefail

# workcell's checks refuse a path that runs through a symlink, so work from
# the physical path of the clone.
cd -P -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

repo=omkhar/workcell
branch=claude/claude-mention
environment=claude-mention
self=scripts/claude-mention-setup.sh

fail() {
  printf '%s\n' "$*" >&2
  exit 1
}

for tool in git gh go claude; do
  command -v "${tool}" >/dev/null 2>&1 || fail "${tool} is not on PATH."
done
gh auth status >/dev/null 2>&1 || fail "gh is not signed in. Run: gh auth login"

current="$(git symbolic-ref --short HEAD)"
[[ "${current}" == "${branch}" ]] || fail "Switch to ${branch} first."
if ! git diff --quiet || ! git diff --cached --quiet; then
  fail "This clone has uncommitted changes. Commit or discard them first."
fi
git fetch --quiet origin
local_head="$(git rev-parse HEAD)"
remote_head="$(git rev-parse "origin/${branch}")"
[[ "${local_head}" == "${remote_head}" ]] || fail "Run: git pull --ff-only"

echo "1/5 Checking the pinned claude-code-action against its v1 tag"
pinned="$(grep -oE 'anthropics/claude-code-action@[0-9a-f]{40}' .github/workflows/claude-mention.yml | cut -d@ -f2)"
v1="$(git ls-remote https://github.com/anthropics/claude-code-action refs/tags/v1 'refs/tags/v1^{}' | tail -n 1 | cut -f1)"
[[ "${pinned}" =~ ^[0-9a-f]{40}$ ]] || fail "Could not read the pinned action commit from the workflow."
[[ "${pinned}" == "${v1}" ]] || fail "v1 now points at ${v1}, but the branch pins ${pinned}. Stopping; ask Claude to repin."
echo "    pinned ${pinned} matches v1"

echo "2/5 Running the workflow checks"
go run ./cmd/workcell-citools check-workflow-refs .
go test -count=1 -run ClaudeMention ./internal/metadatautil/

echo "3/5 Creating the ${environment} environment, limited to main"
gh api --silent -X PUT "repos/${repo}/environments/${environment}" --input - <<'JSON'
{"deployment_branch_policy": {"protected_branches": false, "custom_branch_policies": true}}
JSON
if ! gh api --paginate "repos/${repo}/environments/${environment}/deployment-branch-policies" \
  --jq '.branch_policies[] | select(.type == "branch") | .name' | grep -qx main; then
  gh api --silent -X POST "repos/${repo}/environments/${environment}/deployment-branch-policies" -f name=main -f type=branch
fi

echo "4/5 Storing the Claude token in the environment"
echo "    claude setup-token signs you in and prints a token; copy it, then paste it at the next prompt."
claude setup-token
gh secret set CLAUDE_CODE_OAUTH_TOKEN --env "${environment}" -R "${repo}"

echo "5/5 Removing this script, re-signing the branch and pushing"
trap 'echo "Push step failed. To retry: git reset --hard origin/${branch} && bash ${self}" >&2' ERR
git rm --quiet -- "${self}"
git commit --quiet -S -m ".D Remove the one-time Claude mention setup script (it set up the environment and secret and re-signed this branch; it never lands on main)"
git rebase --quiet -S --force-rebase "origin/main"
git push --force-with-lease="${branch}:origin/${branch}" origin "${branch}"
trap - ERR
echo "Done. The branch is signed and pushed; open the PR or tell Claude it is pushed."
