#!/bin/bash -p
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/publish-provider-bump-pr.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
workcell_provider_bump_token_file_created=""
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  unset WORKCELL_PROVIDER_BUMP_TOKEN_FILE_CREATED
  if [[ -z "${WORKCELL_GITHUB_API_TOKEN_FILE:-}" ]]; then
    workcell_provider_bump_token="${WORKCELL_GITHUB_API_TOKEN:-${GITHUB_TOKEN:-${GH_TOKEN:-}}}"
    unset WORKCELL_GITHUB_API_TOKEN GITHUB_TOKEN GH_TOKEN
    if [[ -n "${workcell_provider_bump_token}" ]]; then
      workcell_provider_bump_token_file="$(umask 077 && /usr/bin/mktemp "${TMPDIR:-/tmp}/workcell-github-token.XXXXXX")"
      printf '%s' "${workcell_provider_bump_token}" >"${workcell_provider_bump_token_file}"
      export WORKCELL_GITHUB_API_TOKEN_FILE="${workcell_provider_bump_token_file}"
      workcell_provider_bump_token_file_created="${workcell_provider_bump_token_file}"
      export WORKCELL_PROVIDER_BUMP_TOKEN_FILE_CREATED="${workcell_provider_bump_token_file_created}"
    fi
  fi
  unset WORKCELL_GITHUB_API_TOKEN GITHUB_TOKEN GH_TOKEN workcell_provider_bump_token
fi
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "${workcell_entry_root}/scripts/lib/trusted-entrypoint.sh"
workcell_provider_bump_token_file_created="${WORKCELL_PROVIDER_BUMP_TOKEN_FILE_CREATED:-${workcell_provider_bump_token_file_created}}"
unset WORKCELL_PROVIDER_BUMP_TOKEN_FILE_CREATED

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "${workcell_entry_root}/scripts/publish-provider-bump-pr.sh" >/dev/null
  echo "publish-provider-bump-pr-entrypoint-ok"
  [[ -z "${workcell_provider_bump_token_file_created}" || "${workcell_provider_bump_token_file_created}" != "${TMPDIR:-/tmp}"/workcell-github-token.* ]] || rm -f "${workcell_provider_bump_token_file_created}"
  exit 0
fi

ROOT_DIR="${workcell_entry_root}"
BASE_BRANCH="main"
ready_flag=1
now_override=""
base_ref=""

usage() {
  cat <<'EOF'
Usage: scripts/publish-provider-bump-pr.sh [--base BRANCH] [--draft] [--now RFC3339]

Creates a disposable worktree, updates pinned provider versions to the newest
stable releases older than the configured cool-off, validates the result, and
publishes a signed pull request through the repo-local parity-enforcing
publication wrapper.

Run this from a clean worktree. The disposable publication branch is created
from the latest available tip of the selected base branch, not the caller's
current feature branch HEAD.
EOF
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

while [[ $# -gt 0 ]]; do
  case "$1" in
    --base)
      BASE_BRANCH="${2:-}"
      if [[ -z "${BASE_BRANCH}" ]]; then
        echo "--base requires a branch name." >&2
        exit 2
      fi
      shift 2
      ;;
    --draft)
      ready_flag=0
      shift
      ;;
    --now)
      now_override="${2:-}"
      if [[ -z "${now_override}" ]]; then
        echo "--now requires an RFC3339 value." >&2
        exit 2
      fi
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
require_tool go
require_tool jq
require_tool mktemp

if [[ -n "$(git -C "${ROOT_DIR}" status --short)" ]]; then
  echo "scripts/publish-provider-bump-pr.sh requires a clean worktree." >&2
  echo "Commit, stash, or discard local changes first so the disposable PR worktree can be reviewed against the selected base branch cleanly." >&2
  exit 2
fi

base_ref="$(resolve_base_ref)"

worktree_root="$(mktemp -d "${TMPDIR:-/tmp}/workcell-provider-bump.XXXXXX")"
title_file="$(mktemp "${TMPDIR:-/tmp}/workcell-provider-bump-title.XXXXXX")"
body_file="$(mktemp "${TMPDIR:-/tmp}/workcell-provider-bump-body.XXXXXX")"
commit_file="$(mktemp "${TMPDIR:-/tmp}/workcell-provider-bump-commit.XXXXXX")"

cleanup() {
  git -C "${ROOT_DIR}" worktree remove --force "${worktree_root}" >/dev/null 2>&1 || true
  git -C "${ROOT_DIR}" worktree prune >/dev/null 2>&1 || true
  rm -f "${title_file}" "${body_file}" "${commit_file}"
  [[ -z "${workcell_provider_bump_token_file_created}" || "${workcell_provider_bump_token_file_created}" != "${TMPDIR:-/tmp}"/workcell-github-token.* ]] || rm -f "${workcell_provider_bump_token_file_created}"
}
trap cleanup EXIT

git -C "${ROOT_DIR}" worktree add --detach "${worktree_root}" "${base_ref}" >/dev/null

update_cmd=("${worktree_root}/scripts/update-provider-pins.sh" --apply)
if [[ -n "${now_override}" ]]; then
  update_cmd+=(--now "${now_override}")
fi
"${update_cmd[@]}"

if ! git -C "${worktree_root}" diff --quiet --exit-code; then
  :
else
  echo "No eligible stable provider pin updates found."
  exit 0
fi

branch_suffix="$(date -u +%Y%m%d%H%M%S)"
if [[ -n "${now_override}" ]]; then
  branch_suffix="$(printf '%s' "${now_override}" | tr -cd '0-9')"
fi
branch_name="codex/provider-bumps-${branch_suffix}"
"${worktree_root}/scripts/pre-merge.sh" --profile pr-parity --allow-dirty --publish-branch "${branch_name}"

codex_version="$(
  cd "${worktree_root}"
  go run ./cmd/workcell-citools extract-dockerfile-arg "${worktree_root}/runtime/container/Dockerfile" CODEX_VERSION
)"
claude_version="$(
  cd "${worktree_root}"
  go run ./cmd/workcell-citools extract-dockerfile-arg "${worktree_root}/runtime/container/Dockerfile" CLAUDE_VERSION
)"
copilot_version="$(
  cd "${worktree_root}"
  go run ./cmd/workcell-citools extract-dockerfile-arg "${worktree_root}/runtime/container/Dockerfile" COPILOT_VERSION
)"
gemini_version="$(jq -r '.dependencies["@google/gemini-cli"]' "${worktree_root}/runtime/container/providers/package.json")"

title="Bump stable provider pins"

printf '%s\n' "${title}" >"${title_file}"
cat >"${body_file}" <<EOF
## Summary

- Codex ${codex_version}
- Claude ${claude_version}
- Copilot ${copilot_version}
- Gemini ${gemini_version}

## Validation

- \`./scripts/pre-merge.sh --profile pr-parity --allow-dirty\`
EOF

cat >"${commit_file}" <<EOF
Bump stable provider pins

- Codex ${codex_version}
- Claude ${claude_version}
- Copilot ${copilot_version}
- Gemini ${gemini_version}
EOF

publish_cmd=(
  "${ROOT_DIR}/scripts/repo-publish-pr.sh"
  --workspace "${worktree_root}"
  --branch "${branch_name}"
  --base "${BASE_BRANCH}"
  --title-file "${title_file}"
  --body-file "${body_file}"
  --commit-message-file "${commit_file}"
)
if [[ "${ready_flag}" -eq 1 ]]; then
  publish_cmd+=(--ready)
fi
"${publish_cmd[@]}"
