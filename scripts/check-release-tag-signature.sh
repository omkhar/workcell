#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

REPO_ROOT=""
TAG_REF=""
EXPECTED_COMMIT=""
EXPECTED_TAG_OBJECT=""
TRUSTED_HOST_PATH=""
HOST_GIT_BIN=""
REAL_HOME="${HOME:-/}"

build_trusted_host_path() {
  local dir=""
  local path=""

  for dir in /opt/homebrew/bin /usr/local/bin /usr/bin /bin /usr/sbin /sbin; do
    [[ -d "${dir}" ]] || continue
    if [[ -z "${path}" ]]; then
      path="${dir}"
    else
      path="${path}:${dir}"
    fi
  done
  printf '%s\n' "${path}"
}

resolve_fixed_host_tool() {
  local name="$1"
  shift
  local candidate=""

  for candidate in "$@"; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done

  echo "Missing trusted host tool: ${name}" >&2
  exit 1
}

run_clean_host_command_in_dir() {
  local dir="$1"
  local home="${REAL_HOME}"
  local -a env_args=()

  shift
  [[ "$#" -gt 0 ]] || return 0
  if [[ ! -d "${dir}" ]]; then
    echo "Missing host working directory: ${dir}" >&2
    exit 2
  fi
  if [[ ! -d "${home}" ]]; then
    home="/"
  fi

  env_args=(
    PATH="${TRUSTED_HOST_PATH}"
    HOME="${home}"
    LC_ALL=C
    LANG=C
  )

  [[ -n "${GNUPGHOME:-}" ]] && env_args+=("GNUPGHOME=${GNUPGHOME}")
  [[ -n "${GPG_TTY:-}" ]] && env_args+=("GPG_TTY=${GPG_TTY}")
  [[ -n "${XDG_CONFIG_HOME:-}" ]] && env_args+=("XDG_CONFIG_HOME=${XDG_CONFIG_HOME}")
  [[ -n "${XDG_STATE_HOME:-}" ]] && env_args+=("XDG_STATE_HOME=${XDG_STATE_HOME}")
  [[ -n "${XDG_CACHE_HOME:-}" ]] && env_args+=("XDG_CACHE_HOME=${XDG_CACHE_HOME}")
  [[ -n "${XDG_DATA_HOME:-}" ]] && env_args+=("XDG_DATA_HOME=${XDG_DATA_HOME}")
  [[ -n "${XDG_RUNTIME_DIR:-}" ]] && env_args+=("XDG_RUNTIME_DIR=${XDG_RUNTIME_DIR}")

  (
    cd "${dir}" &&
      env -i \
        "${env_args[@]}" \
        "$@"
  )
}

usage() {
  cat <<'EOF'
Usage: check-release-tag-signature.sh --repo-root PATH --tag TAG

Options:
  --repo-root PATH   Repository whose release tag should be verified
  --tag TAG          Tag name or ref to verify
  --expected-commit SHA      Require the tag to target this commit
  --expected-tag-object SHA  Require the tag ref to resolve to this tag object
  --git-bin PATH     Trusted Git executable to use
  --github-repo REPO Verify the tag through GitHub's tag-object verification API
  -h, --help         Show this help text
EOF
}

# require_expected_binding fails closed when the tag no longer resolves to the
# object and commit the caller already verified, so a tag moved between release
# phases cannot pass a later recheck.
require_expected_binding() {
  local tag_object="$1"
  local target_commit="$2"

  if [[ -n "${EXPECTED_TAG_OBJECT}" && "${tag_object}" != "${EXPECTED_TAG_OBJECT}" ]]; then
    echo "Release tag ${TAG_REF} resolves to object ${tag_object}, not the expected ${EXPECTED_TAG_OBJECT}." >&2
    exit 2
  fi
  if [[ -n "${EXPECTED_COMMIT}" && "${target_commit}" != "${EXPECTED_COMMIT}" ]]; then
    echo "Release tag ${TAG_REF} targets ${target_commit}, not the expected commit ${EXPECTED_COMMIT}." >&2
    exit 2
  fi
}

require_object_id() {
  local flag="$1"
  local value="$2"

  if [[ ! "${value}" =~ ^[0-9a-f]{40}$ ]]; then
    echo "${flag} requires a 40-character lowercase object ID." >&2
    exit 2
  fi
}

option_value_or_die() {
  local flag="$1"
  local value="${2:-}"

  if [[ -z "${value}" ]]; then
    echo "${flag} requires a value." >&2
    exit 2
  fi
  printf '%s\n' "${value}"
}

verify_github_tag_signature() {
  local repo="$1"
  local tag_ref="$2"
  local ref_json=""
  local tag_json=""
  local object_type=""
  local object_sha=""
  local verified=""
  local reason=""
  local target_type=""
  local target_sha=""

  command -v gh >/dev/null 2>&1 || {
    echo "Missing trusted host tool: gh" >&2
    exit 1
  }

  tag_ref="${tag_ref#refs/tags/}"
  ref_json="$(gh api "repos/${repo}/git/ref/tags/${tag_ref}")"
  object_type="$(jq -r '.object.type // empty' <<<"${ref_json}")"
  object_sha="$(jq -r '.object.sha // empty' <<<"${ref_json}")"
  if [[ "${object_type}" != "tag" || -z "${object_sha}" ]]; then
    echo "Release tag ${tag_ref} on ${repo} must be an annotated signed tag object." >&2
    exit 2
  fi

  tag_json="$(gh api "repos/${repo}/git/tags/${object_sha}")"
  verified="$(jq -r '.verification.verified // false' <<<"${tag_json}")"
  reason="$(jq -r '.verification.reason // "unknown"' <<<"${tag_json}")"
  if [[ "${verified}" != "true" || "${reason}" != "valid" ]]; then
    echo "release workflow requires a GitHub-verified signed tag; ${tag_ref} verification reason is ${reason}." >&2
    exit 2
  fi

  target_type="$(jq -r '.object.type // empty' <<<"${tag_json}")"
  target_sha="$(jq -r '.object.sha // empty' <<<"${tag_json}")"
  if [[ "${target_type}" != "commit" ]]; then
    echo "Release tag ${tag_ref} on ${repo} must directly target a commit." >&2
    exit 2
  fi
  require_expected_binding "${object_sha}" "${target_sha}"

  printf 'Release tag signature check passed: tag=%s verifier=github\n' "${tag_ref}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-root)
      REPO_ROOT="$(option_value_or_die "$1" "${2-}")"
      shift 2
      ;;
    --tag)
      TAG_REF="$(option_value_or_die "$1" "${2-}")"
      shift 2
      ;;
    --expected-commit)
      EXPECTED_COMMIT="$(option_value_or_die "$1" "${2-}")"
      require_object_id "$1" "${EXPECTED_COMMIT}"
      shift 2
      ;;
    --expected-tag-object)
      EXPECTED_TAG_OBJECT="$(option_value_or_die "$1" "${2-}")"
      require_object_id "$1" "${EXPECTED_TAG_OBJECT}"
      shift 2
      ;;
    --git-bin)
      HOST_GIT_BIN="$(option_value_or_die "$1" "${2-}")"
      shift 2
      ;;
    --github-repo)
      GITHUB_REPOSITORY="$(option_value_or_die "$1" "${2-}")"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unsupported check-release-tag-signature option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "${REPO_ROOT}" ]]; then
  echo "check-release-tag-signature requires --repo-root." >&2
  exit 2
fi
if [[ -z "${TAG_REF}" ]]; then
  echo "check-release-tag-signature requires --tag." >&2
  exit 2
fi

if [[ -n "${GITHUB_REPOSITORY:-}" ]]; then
  verify_github_tag_signature "${GITHUB_REPOSITORY}" "${TAG_REF}"
  exit 0
fi

TRUSTED_HOST_PATH="$(build_trusted_host_path)"
if [[ -n "${HOST_GIT_BIN}" ]]; then
  if [[ "${HOST_GIT_BIN}" != /* ]] || [[ ! -x "${HOST_GIT_BIN}" ]]; then
    echo "--git-bin must point to an absolute executable path: ${HOST_GIT_BIN}" >&2
    exit 2
  fi
else
  HOST_GIT_BIN="$(resolve_fixed_host_tool git /opt/homebrew/bin/git /usr/local/bin/git /usr/bin/git /bin/git)"
fi
if [[ ! -d "${REAL_HOME}" ]]; then
  REAL_HOME="/"
fi

REPO_ROOT="$(cd "${REPO_ROOT}" && pwd -P)"
if ! run_clean_host_command_in_dir "${REPO_ROOT}" "${HOST_GIT_BIN}" rev-parse --show-toplevel >/dev/null 2>&1; then
  echo "check-release-tag-signature requires a git worktree: ${REPO_ROOT}" >&2
  exit 2
fi

tag_object="$(run_clean_host_command_in_dir "${REPO_ROOT}" "${HOST_GIT_BIN}" rev-parse --verify --quiet "${TAG_REF}^{tag}" || true)"
if [[ -z "${tag_object}" ]]; then
  echo "Release tag ${TAG_REF} must be an annotated signed tag object." >&2
  exit 2
fi

verify_log="$(mktemp "${TMPDIR:-/tmp}/workcell-release-tag.XXXXXX")"

cleanup() {
  rm -f "${verify_log}"
}
trap cleanup EXIT

tag_target="$(run_clean_host_command_in_dir "${REPO_ROOT}" "${HOST_GIT_BIN}" --no-replace-objects cat-file tag "${tag_object}" |
  /usr/bin/awk '
    $0 == "" { exit }
    index($0, "object ") == 1 { object_count++; object = substr($0, 8) }
    index($0, "type ") == 1 { type_count++; object_type = substr($0, 6) }
    END {
      if (object_count != 1 || type_count != 1 || object_type != "commit") exit 2
      print object
    }
  ')" || {
  echo "Release tag ${TAG_REF} must directly target one commit with canonical annotated tag headers." >&2
  exit 2
}
require_expected_binding "${tag_object}" "${tag_target}"

if ! run_clean_host_command_in_dir "${REPO_ROOT}" "${HOST_GIT_BIN}" --no-replace-objects verify-tag "${tag_object}" >/dev/null 2>"${verify_log}"; then
  echo "release workflow requires a verifiable signed tag; unable to verify ${TAG_REF}." >&2
  if [[ -s "${verify_log}" ]]; then
    sed 's/^/  /' "${verify_log}" >&2
  fi
  exit 2
fi

printf 'Release tag signature check passed: tag=%s\n' "${TAG_REF}"
