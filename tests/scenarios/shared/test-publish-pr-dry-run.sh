#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-publish-pr-scenario.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

FIXTURE="${TMP_DIR}/publish-pr-fixture"
ORIGIN="${TMP_DIR}/publish-pr-origin.git"
SSH_SIGNING_KEY="${TMP_DIR}/signing_key"
ALLOWED_SIGNERS="${TMP_DIR}/allowed_signers"
GH_STUB="${TMP_DIR}/gh-stub"
GH_LOG="${TMP_DIR}/gh.log"
HOOK_MARKER_DIR="${TMP_DIR}/hook-markers"
TITLE_FILE="${TMP_DIR}/pr-title.txt"
BODY_FILE="${TMP_DIR}/pr-body.md"
COMMIT_MESSAGE_FILE="${TMP_DIR}/commit-message.txt"
LIVE_TITLE_FILE="${TMP_DIR}/live-pr-title.txt"
LIVE_BODY_FILE="${TMP_DIR}/live-pr-body.md"
LIVE_COMMIT_MESSAGE_FILE="${TMP_DIR}/live-commit-message.txt"

mkdir -p "${FIXTURE}" "${HOOK_MARKER_DIR}"
git init -q --bare "${ORIGIN}"
git init -q "${FIXTURE}"
git -C "${FIXTURE}" config user.name "Workcell Scenario"
git -C "${FIXTURE}" config user.email "workcell-scenario@example.com"
ssh-keygen -q -t ed25519 -N '' -f "${SSH_SIGNING_KEY}" >/dev/null
printf 'workcell-scenario@example.com ' >"${ALLOWED_SIGNERS}"
cat "${SSH_SIGNING_KEY}.pub" >>"${ALLOWED_SIGNERS}"
git -C "${FIXTURE}" config gpg.format ssh
git -C "${FIXTURE}" config user.signingkey "${SSH_SIGNING_KEY}"
git -C "${FIXTURE}" config gpg.ssh.allowedSignersFile "${ALLOWED_SIGNERS}"
git -C "${FIXTURE}" config commit.gpgsign true
git -C "${FIXTURE}" remote add origin "${ORIGIN}"

printf 'base\n' >"${FIXTURE}/tracked.txt"
git -C "${FIXTURE}" add tracked.txt
git -C "${FIXTURE}" commit -q -m init

cat >"${FIXTURE}/.git/hooks/pre-commit" <<EOF
#!/bin/sh
printf 'pre-commit\n' >"${HOOK_MARKER_DIR}/pre-commit"
exit 1
EOF
cat >"${FIXTURE}/.git/hooks/pre-push" <<EOF
#!/bin/sh
printf 'pre-push\n' >"${HOOK_MARKER_DIR}/pre-push"
exit 1
EOF
chmod +x "${FIXTURE}/.git/hooks/pre-commit" "${FIXTURE}/.git/hooks/pre-push"

printf 'worktree\n' >"${FIXTURE}/tracked.txt"
cat >"${TITLE_FILE}" <<'EOF'
Scenario PR title
EOF
cat >"${BODY_FILE}" <<'EOF'
Scenario PR body
EOF
cat >"${COMMIT_MESSAGE_FILE}" <<'EOF'
Scenario publish-pr commit

- include staged workspace changes
EOF

worktree_dry_run="$("${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${FIXTURE}" \
  --branch feature/publish-scenario \
  --title-file "${TITLE_FILE}" \
  --body-file "${BODY_FILE}" \
  --commit-message-file "${COMMIT_MESSAGE_FILE}" \
  --snapshot worktree \
  --dry-run)"

grep -q '^publish_snapshot=worktree$' <<<"${worktree_dry_run}"
grep -q '^publish_branch=feature/publish-scenario$' <<<"${worktree_dry_run}"
grep -q '^publish_base=main$' <<<"${worktree_dry_run}"
grep -q '^publish_draft=1$' <<<"${worktree_dry_run}"
grep -q -- ' -c core.hooksPath=/dev/null -C ' <<<"${worktree_dry_run}"
grep -q -- 'switch --no-guess -c feature/publish-scenario' <<<"${worktree_dry_run}"
grep -q -- ' add -A ' <<<"${worktree_dry_run}"
grep -q -- ' commit --no-verify -S -F ' <<<"${worktree_dry_run}"
grep -q -- ' push --no-verify -u origin feature/publish-scenario ' <<<"${worktree_dry_run}"
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
grep -q -- ' -c core.hooksPath=/dev/null -C ' <<<"${index_dry_run}"
grep -q -- 'switch --no-guess -c feature/publish-index' <<<"${index_dry_run}"
grep -q -- ' commit --no-verify -S -F ' <<<"${index_dry_run}"
if grep -q -- ' add -A ' <<<"${index_dry_run}"; then
  echo "publish-pr index snapshot should not auto-stage the worktree" >&2
  exit 1
fi

set +e
default_branch_output="$("${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${FIXTURE}" \
  --branch main \
  --title "Refuse default branch" \
  --commit-message "Refuse default branch" \
  --dry-run 2>&1)"
default_branch_rc=$?
set -e
test "${default_branch_rc}" -eq 2
grep -q 'publish-pr refuses the default branch' <<<"${default_branch_output}"

no_remote_fixture="${TMP_DIR}/publish-pr-no-remote"
git init -q "${no_remote_fixture}"
set +e
missing_remote_output="$("${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${no_remote_fixture}" \
  --branch feature/missing-origin \
  --title "Missing origin" \
  --commit-message "Missing origin" \
  --dry-run 2>&1)"
missing_remote_rc=$?
set -e
test "${missing_remote_rc}" -eq 2
grep -q 'publish-pr requires an origin remote' <<<"${missing_remote_output}"

git -C "${FIXTURE}" reset tracked.txt >/dev/null
printf 'live publish\n' >"${FIXTURE}/tracked.txt"
cat >"${LIVE_TITLE_FILE}" <<'EOF'
Live scenario title
EOF
cat >"${LIVE_BODY_FILE}" <<'EOF'
Live scenario body
EOF
cat >"${LIVE_COMMIT_MESSAGE_FILE}" <<'EOF'
Live scenario commit
EOF
cat >"${GH_STUB}" <<EOF
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "\$*" >"${GH_LOG}"
printf 'https://example.invalid/pr/123\n'
EOF
chmod +x "${GH_STUB}"

publish_output="$(
  HOST_GH_BIN="${GH_STUB}" bash "${ROOT_DIR}/scripts/workcell" publish-pr \
    --workspace "${FIXTURE}" \
    --branch feature/publish-live \
    --title-file "${LIVE_TITLE_FILE}" \
    --body-file "${LIVE_BODY_FILE}" \
    --commit-message-file "${LIVE_COMMIT_MESSAGE_FILE}" \
    2>"${TMP_DIR}/publish-live.stderr"
)"
grep -q '^publish_branch=feature/publish-live$' <<<"${publish_output}"
grep -q '^publish_base=main$' <<<"${publish_output}"
grep -q '^publish_pr_url=https://example.invalid/pr/123$' <<<"${publish_output}"
grep -q '^publish_snapshot=worktree$' <<<"${publish_output}"
grep -q '^pr create --base main --head feature/publish-live --title Live scenario title --draft --body-file ' "${GH_LOG}"

publish_head="$(git -C "${FIXTURE}" rev-parse HEAD)"
remote_head="$(git --git-dir="${ORIGIN}" rev-parse refs/heads/feature/publish-live)"
test "${publish_head}" = "${remote_head}"
git -C "${FIXTURE}" verify-commit "${publish_head}" >/dev/null 2>&1
test ! -e "${HOOK_MARKER_DIR}/pre-commit"
test ! -e "${HOOK_MARKER_DIR}/pre-push"

echo "Publish-pr scenario passed"
