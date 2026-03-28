#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-publish-pr-scenario.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

FIXTURE="${TMP_DIR}/publish-pr-fixture"
mkdir -p "${FIXTURE}"
git init -q "${FIXTURE}"
git -C "${FIXTURE}" config user.name "Workcell Scenario"
git -C "${FIXTURE}" config user.email "workcell-scenario@example.com"
git -C "${FIXTURE}" remote add origin https://github.com/example/workcell-publish-fixture.git

printf 'base\n' >"${FIXTURE}/tracked.txt"
git -C "${FIXTURE}" add tracked.txt
git -C "${FIXTURE}" commit -q -m init

printf 'worktree\n' >"${FIXTURE}/tracked.txt"
cat >"${FIXTURE}/pr-title.txt" <<'EOF'
Scenario PR title
EOF
cat >"${FIXTURE}/pr-body.md" <<'EOF'
Scenario PR body
EOF
cat >"${FIXTURE}/commit-message.txt" <<'EOF'
Scenario publish-pr commit

- include staged workspace changes
EOF

worktree_dry_run="$("${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${FIXTURE}" \
  --branch feature/publish-scenario \
  --title-file "${FIXTURE}/pr-title.txt" \
  --body-file "${FIXTURE}/pr-body.md" \
  --commit-message-file "${FIXTURE}/commit-message.txt" \
  --snapshot worktree \
  --dry-run)"

grep -q '^publish_snapshot=worktree$' <<<"${worktree_dry_run}"
grep -q '^publish_branch=feature/publish-scenario$' <<<"${worktree_dry_run}"
grep -q '^publish_base=main$' <<<"${worktree_dry_run}"
grep -q '^publish_draft=1$' <<<"${worktree_dry_run}"
grep -q -- 'switch -c feature/publish-scenario' <<<"${worktree_dry_run}"
grep -q -- ' add -A ' <<<"${worktree_dry_run}"
grep -q -- ' commit -S -F ' <<<"${worktree_dry_run}"
grep -q -- ' push -u origin feature/publish-scenario ' <<<"${worktree_dry_run}"
grep -q -- 'gh pr create --base main --head feature/publish-scenario --title Scenario\\ PR\\ title --draft --body-file' <<<"${worktree_dry_run}"

git -C "${FIXTURE}" add tracked.txt
index_dry_run="$("${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${FIXTURE}" \
  --branch feature/publish-index \
  --title "Index scenario title" \
  --commit-message "Index scenario commit" \
  --snapshot index \
  --dry-run)"

grep -q '^publish_snapshot=index$' <<<"${index_dry_run}"
grep -q '^publish_branch=feature/publish-index$' <<<"${index_dry_run}"
grep -q -- 'switch -c feature/publish-index' <<<"${index_dry_run}"
grep -q -- ' commit -S -F ' <<<"${index_dry_run}"
if grep -q -- ' add -A ' <<<"${index_dry_run}"; then
  echo "publish-pr index snapshot should not auto-stage the worktree" >&2
  exit 1
fi

echo "Publish-pr scenario passed"
