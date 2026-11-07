#!/usr/bin/env -S BASH_ENV= ENV= bash
# Review a pull request head with the pinned Codex CLI and post the findings.
#
# The lane downloads the Codex release that runtime/container/Dockerfile pins,
# checks both tarballs against the pinned SHA-256 values, runs
# `codex exec review --base origin/<base>` on the checked-out head, and posts
# one PR comment that starts with the marker
# `<!-- workcell-codex-review:v1 {"head":"<sha>","findings":<n>} -->`.
# The job succeeds when a review completed, with or without findings: the
# marker carries the verdict, and the PR process decides what a finding needs.
# It fails closed when OPENAI_API_KEY is absent, a download or checksum fails,
# or Codex returns no result.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"
DOCKERFILE_PATH="${ROOT_DIR}/runtime/container/Dockerfile"
PR_NUMBER="${WORKCELL_PR_NUMBER:-}"
HEAD_SHA="${WORKCELL_PR_HEAD_SHA:-}"
BASE_BRANCH="${WORKCELL_PR_BASE_REF:-main}"
TOKEN_FILE="${WORKCELL_GITHUB_API_TOKEN_FILE:-}"
CODEX_ARCH="x86_64-unknown-linux-musl"
MAX_REVIEW_SECONDS="${WORKCELL_CODEX_REVIEW_TIMEOUT_SECONDS:-1500}"
WORK_DIR=""

cleanup() {
  if [[ -n "${WORK_DIR}" ]]; then
    rm -rf "${WORK_DIR}"
  fi
}
trap cleanup EXIT

require_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required tool: $1" >&2
    exit 1
  fi
}

require_value() {
  if [[ -z "$2" ]]; then
    echo "$1 is required." >&2
    exit 2
  fi
}

# extract_dockerfile_value COMMAND ARG runs the citools extractor COMMAND on
# the Dockerfile with ARG (an ARG name or a target architecture).
extract_dockerfile_value() {
  (cd "${ROOT_DIR}" && go run ./cmd/workcell-citools "$1" "${DOCKERFILE_PATH}" "$2")
}

download_asset() {
  curl --ipv4 -fsSL \
    --retry 5 \
    --retry-all-errors \
    --retry-delay 5 \
    --connect-timeout 20 \
    "$1" -o "$2"
}

# install_codex_asset NAME SHA256 installs the pinned release tarball NAME
# (codex or codex-code-mode-host) into WORK_DIR/bin after a checksum match.
install_codex_asset() {
  local name="$1"
  local sha256="$2"
  local tarball="${WORK_DIR}/${name}.tar.gz"
  download_asset "https://github.com/openai/codex/releases/download/rust-v${CODEX_VERSION}/${name}-${CODEX_ARCH}.tar.gz" "${tarball}"
  echo "${sha256}  ${tarball}" | sha256sum -c - >/dev/null # portability-exempt: require_tool sha256sum fails closed when it is absent
  tar -xzf "${tarball}" -C "${WORK_DIR}" "${name}-${CODEX_ARCH}"
  install -m 0755 "${WORK_DIR}/${name}-${CODEX_ARCH}" "${WORK_DIR}/bin/${name}"
}

require_tool curl
require_tool git
require_tool go
require_tool jq
require_tool sha256sum
require_tool tar
require_value WORKCELL_PR_NUMBER "${PR_NUMBER}"
require_value WORKCELL_PR_HEAD_SHA "${HEAD_SHA}"
require_value WORKCELL_GITHUB_API_TOKEN_FILE "${TOKEN_FILE}"
if [[ -z "${OPENAI_API_KEY:-}" ]]; then
  echo "OPENAI_API_KEY is absent: add it to the codex-review environment, or the Codex review lane cannot run." >&2
  exit 2
fi
[[ "${BASE_BRANCH}" =~ ^[A-Za-z0-9._/-]+$ ]] || {
  echo "Unsupported base branch name: ${BASE_BRANCH}" >&2
  exit 2
}

if [[ "$(git -C "${ROOT_DIR}" rev-parse HEAD)" != "${HEAD_SHA}" ]]; then
  echo "The checkout is not at the pull request head ${HEAD_SHA}." >&2
  exit 1
fi
git -C "${ROOT_DIR}" fetch --no-tags --prune origin "${BASE_BRANCH}"

# Codex refuses helper binaries under /tmp, so the work directory lives under
# the runner's temp root when one exists.
WORK_DIR="$(umask 077 && mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/workcell-codex-review.XXXXXX")"
mkdir -p "${WORK_DIR}/bin" "${WORK_DIR}/home/.codex"

CODEX_VERSION="$(extract_dockerfile_value extract-dockerfile-arg CODEX_VERSION)"
install_codex_asset codex "$(extract_dockerfile_value extract-codex-sha amd64)"
install_codex_asset codex-code-mode-host "$(extract_dockerfile_value extract-codex-code-mode-host-sha amd64)"
"${WORK_DIR}/bin/codex" --version

cat >"${WORK_DIR}/schema.json" <<'SCHEMA'
{
  "type": "object",
  "additionalProperties": false,
  "required": ["summary", "findings"],
  "properties": {
    "summary": {"type": "string"},
    "findings": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["severity", "title", "path", "line", "body"],
        "properties": {
          "severity": {"type": "string", "enum": ["P0", "P1", "P2", "P3"]},
          "title": {"type": "string"},
          "path": {"type": "string"},
          "line": {"type": "integer"},
          "body": {"type": "string"}
        }
      }
    }
  }
}
SCHEMA

# The review runs with a scrubbed environment: only the key, a private HOME
# and CODEX_HOME, and PATH reach the CLI. The GitHub token stays in its file.
review_status=0
(
  cd "${ROOT_DIR}" &&
    env -i \
      PATH="${PATH}" \
      HOME="${WORK_DIR}/home" \
      CODEX_HOME="${WORK_DIR}/home/.codex" \
      OPENAI_API_KEY="${OPENAI_API_KEY}" \
      timeout "${MAX_REVIEW_SECONDS}" "${WORK_DIR}/bin/codex" exec review \
      --ephemeral \
      --ignore-user-config \
      --ignore-rules \
      -c 'model_reasoning_effort="high"' \
      --base "origin/${BASE_BRANCH}" \
      --output-schema "${WORK_DIR}/schema.json" \
      -o "${WORK_DIR}/review.json" \
      >"${WORK_DIR}/review.log" 2>&1
) || review_status=$?
tail -n 40 "${WORK_DIR}/review.log"
if [[ "${review_status}" -ne 0 ]]; then
  echo "codex exec review exited ${review_status}." >&2
  exit 1
fi
if ! jq -e 'type == "object" and (.findings | type == "array") and (.summary | type == "string")' "${WORK_DIR}/review.json" >/dev/null; then
  echo "codex exec review returned no result that matches the review schema." >&2
  exit 1
fi

finding_count="$(jq -r '.findings | length' "${WORK_DIR}/review.json")"
short_head="${HEAD_SHA:0:10}"
{
  printf '<!-- workcell-codex-review:v1 {"head":"%s","findings":%s} -->\n\n' "${HEAD_SHA}" "${finding_count}"
  # shellcheck disable=SC2016 # the backticks are Markdown code spans, not substitutions
  printf '## Codex review of `%s`\n\n' "${short_head}"
  # shellcheck disable=SC2016 # the backticks are Markdown code spans, not substitutions
  printf 'Codex CLI %s, `codex exec review --base origin/%s`, head `%s`.\n\n' "${CODEX_VERSION}" "${BASE_BRANCH}" "${HEAD_SHA}"
  if [[ "${finding_count}" == "0" ]]; then
    printf '**No findings.**\n\n'
  else
    printf '**%s finding(s).** Fix each one, or reply with the disposition.\n\n' "${finding_count}"
    jq -r '.findings[] | "- **\(.severity)** `\(.path):\(.line)` \(.title)\n\n  \(.body | gsub("\n"; "\n  "))\n"' "${WORK_DIR}/review.json"
  fi
  printf '### Summary\n\n%s\n' "$(jq -r '.summary' "${WORK_DIR}/review.json")"
} >"${WORK_DIR}/comment.md"
cat "${WORK_DIR}/comment.md"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  cat "${WORK_DIR}/comment.md" >>"${GITHUB_STEP_SUMMARY}"
fi

jq -n --rawfile body "${WORK_DIR}/comment.md" '{body: $body}' >"${WORK_DIR}/comment.json"
GH_TOKEN="$(cat "${TOKEN_FILE}")" gh api \
  --method POST \
  "repos/${GITHUB_REPOSITORY}/issues/${PR_NUMBER}/comments" \
  --input "${WORK_DIR}/comment.json" >/dev/null
echo "Posted the Codex review of ${HEAD_SHA} with ${finding_count} finding(s) on #${PR_NUMBER}."
