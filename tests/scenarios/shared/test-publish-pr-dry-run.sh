#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-publish-pr-scenario.XXXXXX")"

cleanup() {
  rm -f "${TRUSTED_GH_STUB:-}"
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

compute_worktree_tree_oid() {
  local repo_root="$1"
  local tmp_index=""
  local tree_oid=""

  tmp_index="$(mktemp "${TMPDIR:-/tmp}/workcell-publish-tree.XXXXXX")"
  rm -f "${tmp_index}"
  GIT_INDEX_FILE="${tmp_index}" git -C "${repo_root}" read-tree HEAD
  GIT_INDEX_FILE="${tmp_index}" git -C "${repo_root}" add -A
  tree_oid="$(GIT_INDEX_FILE="${tmp_index}" git -C "${repo_root}" write-tree)"
  rm -f "${tmp_index}"
  printf '%s\n' "${tree_oid}"
}

FIXTURE="${TMP_DIR}/publish-pr-fixture"
ORIGIN="${TMP_DIR}/publish-pr-origin.git"
SSH_SIGNING_KEY="${TMP_DIR}/signing_key"
ALLOWED_SIGNERS="${TMP_DIR}/allowed_signers"
UNTRUSTED_GH_STUB="${TMP_DIR}/gh-stub"
GH_LOG="${TMP_DIR}/gh.log"
HOOK_MARKER_DIR="${TMP_DIR}/hook-markers"
TITLE_FILE="${TMP_DIR}/pr-title.txt"
BODY_FILE="${TMP_DIR}/pr-body.md"
COMMIT_MESSAGE_FILE="${TMP_DIR}/commit-message.txt"
LIVE_TITLE_FILE="${TMP_DIR}/live-pr-title.txt"
LIVE_BODY_FILE="${TMP_DIR}/live-pr-body.md"
LIVE_COMMIT_MESSAGE_FILE="${TMP_DIR}/live-commit-message.txt"
TRUSTED_BIN_DIR=""
TRUSTED_GH_STUB=""

for candidate in /opt/homebrew/bin /usr/local/bin; do
  if [[ -d "${candidate}" ]] && [[ -w "${candidate}" ]]; then
    TRUSTED_BIN_DIR="${candidate}"
    break
  fi
done
if [[ -z "${TRUSTED_BIN_DIR}" ]]; then
  echo "Skipping publish-pr scenario: missing writable trusted host bin directory"
  exit 0
fi
TRUSTED_GH_STUB="${TRUSTED_BIN_DIR}/workcell-gh-scenario-${RANDOM}-$$"

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
git -C "${FIXTURE}" branch -M main
git -C "${FIXTURE}" push -q -u origin main >/dev/null

git -C "${FIXTURE}" switch -q -c feature/pr-shape-safe
printf 'shape gate\n' >"${FIXTURE}/shape-check.txt"
git -C "${FIXTURE}" add shape-check.txt
git -C "${FIXTURE}" commit -q -m "shape gate fixture"
MALICIOUS_HOME="${TMP_DIR}/malicious-home"
MALICIOUS_DIFF="${TMP_DIR}/malicious-diff.sh"
MALICIOUS_MARKER="${TMP_DIR}/malicious-diff.marker"
mkdir -p "${MALICIOUS_HOME}"
cat >"${MALICIOUS_DIFF}" <<EOF
#!/bin/sh
printf 'unexpected diff.external invocation\n' >"${MALICIOUS_MARKER}"
exit 99
EOF
chmod +x "${MALICIOUS_DIFF}"
cat >"${MALICIOUS_HOME}/.gitconfig" <<EOF
[diff]
	external = ${MALICIOUS_DIFF}
EOF
shape_check_output="$(
  HOME="${MALICIOUS_HOME}" "${ROOT_DIR}/scripts/check-pr-shape.sh" \
    --repo-root "${FIXTURE}" \
    --base-ref refs/remotes/origin/main \
    --head-ref HEAD \
    --max-files 25 \
    --max-lines 1200 \
    --max-areas 8 \
    --max-binaries 0
)"
grep -q '^PR shape check passed: files=1 ' <<<"${shape_check_output}"
test ! -e "${MALICIOUS_MARKER}"
git -C "${FIXTURE}" switch -q main
git -C "${FIXTURE}" reset -q --hard origin/main
git -C "${FIXTURE}" branch -D feature/pr-shape-safe >/dev/null

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
grep -q '^publish_base_mode=main$' <<<"${worktree_dry_run}"
grep -q '^publish_repo_owned_pr_checks_expected=1$' <<<"${worktree_dry_run}"
grep -q '^publish_draft=1$' <<<"${worktree_dry_run}"
grep -q -- ' -c core.hooksPath=/dev/null -C ' <<<"${worktree_dry_run}"
grep -q -- 'switch --no-guess -c feature/publish-scenario' <<<"${worktree_dry_run}"
grep -q -- ' add -A ' <<<"${worktree_dry_run}"
grep -q -- ' commit --no-verify -S -F ' <<<"${worktree_dry_run}"
worktree_fetch_line="$(grep -n -- ' fetch --no-tags --prune origin main' <<<"${worktree_dry_run}" | cut -d: -f1)"
worktree_shape_line="$(grep -n -- 'check-pr-shape\.sh --repo-root .* --base-ref refs/remotes/origin/main --head-ref HEAD --max-files 25 --max-lines 1200 --max-areas 8 --max-binaries 0' <<<"${worktree_dry_run}" | cut -d: -f1)"
test -n "${worktree_fetch_line}"
test -n "${worktree_shape_line}"
test "${worktree_fetch_line}" -lt "${worktree_shape_line}"
grep -q -- 'check-pr-shape\.sh --repo-root .* --base-ref refs/remotes/origin/main --head-ref HEAD --max-files 25 --max-lines 1200 --max-areas 8 --max-binaries 0' <<<"${worktree_dry_run}"
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
index_fetch_line="$(grep -n -- ' fetch --no-tags --prune origin main' <<<"${index_dry_run}" | cut -d: -f1)"
index_shape_line="$(grep -n -- 'check-pr-shape\.sh --repo-root .* --base-ref refs/remotes/origin/main --head-ref HEAD --max-files 25 --max-lines 1200 --max-areas 8 --max-binaries 0' <<<"${index_dry_run}" | cut -d: -f1)"
test -n "${index_fetch_line}"
test -n "${index_shape_line}"
test "${index_fetch_line}" -lt "${index_shape_line}"
grep -q -- 'check-pr-shape\.sh --repo-root .* --base-ref refs/remotes/origin/main --head-ref HEAD --max-files 25 --max-lines 1200 --max-areas 8 --max-binaries 0' <<<"${index_dry_run}"
if grep -q -- ' add -A ' <<<"${index_dry_run}"; then
  echo "publish-pr index snapshot should not auto-stage the worktree" >&2
  exit 1
fi

set +e
missing_parity_output="$("${ROOT_DIR}/scripts/repo-publish-pr.sh" \
  --workspace "${FIXTURE}" \
  --branch feature/repo-wrapper-missing \
  --title "Missing parity evidence" \
  --commit-message "Missing parity evidence" \
  --dry-run 2>&1)"
missing_parity_rc=$?
set -e
test "${missing_parity_rc}" -eq 2
grep -q 'Missing local PR-parity evidence' <<<"${missing_parity_output}"

mkdir -p "$(git -C "${FIXTURE}" rev-parse --absolute-git-dir)/workcell-parity"
cat >"$(git -C "${FIXTURE}" rev-parse --absolute-git-dir)/workcell-parity/pr-parity.json" <<'EOF'
{
  "version": 1,
  "profile": "pr-parity",
  "base_branch": "main",
  "tree_oid": "deadbeef"
}
EOF
set +e
mismatched_parity_output="$("${ROOT_DIR}/scripts/repo-publish-pr.sh" \
  --workspace "${FIXTURE}" \
  --branch feature/repo-wrapper-mismatch \
  --title "Mismatched parity evidence" \
  --commit-message "Mismatched parity evidence" \
  --dry-run 2>&1)"
mismatched_parity_rc=$?
set -e
test "${mismatched_parity_rc}" -eq 2
grep -q 'Local PR-parity evidence does not match the tree being published' <<<"${mismatched_parity_output}"

current_tree_oid="$(compute_worktree_tree_oid "${FIXTURE}")"
cat >"$(git -C "${FIXTURE}" rev-parse --absolute-git-dir)/workcell-parity/pr-parity.json" <<EOF
{
  "version": 1,
  "profile": "pr-parity",
  "base_branch": "main",
  "tree_oid": "${current_tree_oid}"
}
EOF
wrapper_dry_run="$("${ROOT_DIR}/scripts/repo-publish-pr.sh" \
  --workspace "${FIXTURE}" \
  --branch feature/repo-wrapper-ok \
  --title "Repo wrapper title" \
  --commit-message "Repo wrapper commit" \
  --dry-run)"
grep -q '^publish_branch=feature/repo-wrapper-ok$' <<<"${wrapper_dry_run}"
grep -q '^publish_base=main$' <<<"${wrapper_dry_run}"
grep -q '^publish_snapshot=worktree$' <<<"${wrapper_dry_run}"

rm -f "$(git -C "${FIXTURE}" rev-parse --absolute-git-dir)/workcell-parity/pr-parity.json"
override_dry_run="$("${ROOT_DIR}/scripts/repo-publish-pr.sh" \
  --workspace "${FIXTURE}" \
  --branch feature/repo-wrapper-override \
  --title "Repo wrapper override" \
  --commit-message "Repo wrapper override" \
  --allow-parity-override \
  --parity-override-reason "manual parity waiver for scenario" \
  --dry-run 2>&1)"
grep -q 'repo-publish-pr parity override: manual parity waiver for scenario' <<<"${override_dry_run}"
grep -q '^publish_branch=feature/repo-wrapper-override$' <<<"${override_dry_run}"

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

set +e
unsupported_base_output="$("${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${FIXTURE}" \
  --branch feature/non-main-base \
  --base feature/review-stack \
  --title "Unsupported non-main base" \
  --commit-message "Unsupported non-main base" \
  --dry-run 2>&1)"
unsupported_base_rc=$?
set -e
test "${unsupported_base_rc}" -eq 2
grep -q 'publish-pr only supports --base main by default' <<<"${unsupported_base_output}"

allowed_non_main_dry_run="$("${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${FIXTURE}" \
  --branch feature/non-main-base \
  --base feature/review-stack \
  --allow-non-main-base \
  --title "Lower assurance non-main base" \
  --commit-message "Lower assurance non-main base" \
  --ready \
  --dry-run 2>&1)"
grep -q '^publish_base=feature/review-stack$' <<<"${allowed_non_main_dry_run}"
grep -q '^publish_base_mode=lower-assurance-non-main$' <<<"${allowed_non_main_dry_run}"
grep -q '^publish_repo_owned_pr_checks_expected=0$' <<<"${allowed_non_main_dry_run}"
grep -q '^publish_draft=1$' <<<"${allowed_non_main_dry_run}"
grep -q 'publish-pr preflight: repo-owned PR checks are not expected for --base feature/review-stack' <<<"${allowed_non_main_dry_run}"
grep -q 'normal main-based PR validation and merge gating do not apply to that PR shape' <<<"${allowed_non_main_dry_run}"
grep -q -- "gh pr create --base feature/review-stack --head feature/non-main-base --title Lower\\\\ assurance\\\\ non-main\\\\ base --draft --body ''" <<<"${allowed_non_main_dry_run}"

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
cat >"${UNTRUSTED_GH_STUB}" <<EOF
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "\$*" >"${GH_LOG}"
printf 'https://example.invalid/pr/123\n'
EOF
chmod +x "${UNTRUSTED_GH_STUB}"

set +e
untrusted_gh_output="$("${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${FIXTURE}" \
  --branch feature/publish-live \
  --gh-bin "${UNTRUSTED_GH_STUB}" \
  --title-file "${LIVE_TITLE_FILE}" \
  --body-file "${LIVE_BODY_FILE}" \
  --commit-message-file "${LIVE_COMMIT_MESSAGE_FILE}" \
  --dry-run 2>&1)"
untrusted_gh_rc=$?
set -e
test "${untrusted_gh_rc}" -eq 2
grep -q 'gh-bin must point to a trusted host executable path' <<<"${untrusted_gh_output}"

set +e
host_gh_output="$(HOST_GH_BIN="${UNTRUSTED_GH_STUB}" bash "${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${FIXTURE}" \
  --branch feature/publish-live \
  --title-file "${LIVE_TITLE_FILE}" \
  --body-file "${LIVE_BODY_FILE}" \
  --commit-message-file "${LIVE_COMMIT_MESSAGE_FILE}" \
  --dry-run 2>&1)"
host_gh_rc=$?
set -e
test "${host_gh_rc}" -eq 2
grep -q 'HOST_GH_BIN must point to a trusted host executable path' <<<"${host_gh_output}"

cat >"${TRUSTED_GH_STUB}" <<EOF
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "\$*" >"${GH_LOG}"
printf 'https://example.invalid/pr/123\n'
EOF
chmod +x "${TRUSTED_GH_STUB}"

publish_output="$(
  "${ROOT_DIR}/scripts/workcell" publish-pr \
    --workspace "${FIXTURE}" \
    --branch feature/publish-live \
    --gh-bin "${TRUSTED_GH_STUB}" \
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

git -C "${FIXTURE}" switch -C main >/dev/null
git -C "${FIXTURE}" reset -q --hard origin/main
for index in $(seq 1 26); do
  printf 'broad %02d\n' "${index}" >"${FIXTURE}/broad-${index}.txt"
done

set +e
broad_output="$("${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${FIXTURE}" \
  --branch feature/publish-too-broad \
  --gh-bin "${TRUSTED_GH_STUB}" \
  --title "Broad scenario title" \
  --body "Broad scenario body" \
  --commit-message "Broad scenario commit" \
  2>&1)"
broad_rc=$?
set -e
test "${broad_rc}" -eq 2
grep -q 'PR shape check failed' <<<"${broad_output}"
if git --git-dir="${ORIGIN}" show-ref --verify --quiet refs/heads/feature/publish-too-broad; then
  echo "publish-pr should not push an over-broad branch" >&2
  exit 1
fi

git -C "${FIXTURE}" switch -C main >/dev/null
git -C "${FIXTURE}" reset -q --hard origin/main
printf '\x00\x01binary\n' >"${FIXTURE}/artifact.bin"

set +e
binary_output="$("${ROOT_DIR}/scripts/workcell" publish-pr \
  --workspace "${FIXTURE}" \
  --branch feature/publish-binary \
  --gh-bin "${TRUSTED_GH_STUB}" \
  --title "Binary scenario title" \
  --body "Binary scenario body" \
  --commit-message "Binary scenario commit" \
  2>&1)"
binary_rc=$?
set -e
test "${binary_rc}" -eq 2
grep -q 'PR shape check failed' <<<"${binary_output}"
grep -q 'binary_files=1 (limit=0)' <<<"${binary_output}"
if git --git-dir="${ORIGIN}" show-ref --verify --quiet refs/heads/feature/publish-binary; then
  echo "publish-pr should not push a binary-only branch" >&2
  exit 1
fi

echo "Publish-pr scenario passed"
