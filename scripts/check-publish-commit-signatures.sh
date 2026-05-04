#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

REPO_ROOT=""
BASE_REF=""
HEAD_REF="HEAD"
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
Usage: check-publish-commit-signatures.sh --repo-root PATH --base-ref REF [--head-ref REF]

Options:
  --repo-root PATH   Repository whose publish range should be verified
  --base-ref REF     Base ref for the published commit range
  --head-ref REF     Head ref for the published commit range (default: HEAD)
  --git-bin PATH     Trusted Git executable to use
  -h, --help         Show this help text
EOF
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

resolve_commit_or_die() {
  local repo_root="$1"
  local ref="$2"
  local commit=""

  commit="$(run_clean_host_command_in_dir "${repo_root}" "${HOST_GIT_BIN}" rev-parse --verify --quiet "${ref}^{commit}" || true)"
  if [[ -z "${commit}" ]]; then
    echo "Unable to resolve commit for ref: ${ref}" >&2
    exit 2
  fi
  printf '%s\n' "${commit}"
}

resolve_object_dir_or_die() {
  local repo_root="$1"
  local object_path=""

  object_path="$(run_clean_host_command_in_dir "${repo_root}" "${HOST_GIT_BIN}" rev-parse --git-path objects)"
  if [[ "${object_path}" != /* ]]; then
    object_path="${repo_root}/${object_path}"
  fi
  if [[ ! -d "${object_path}" ]]; then
    echo "Unable to resolve git object directory for ${repo_root}." >&2
    exit 2
  fi
  (
    cd "${object_path}" &&
      pwd -P
  )
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo-root)
      REPO_ROOT="$(option_value_or_die "$1" "${2-}")"
      shift 2
      ;;
    --base-ref)
      BASE_REF="$(option_value_or_die "$1" "${2-}")"
      shift 2
      ;;
    --head-ref)
      HEAD_REF="$(option_value_or_die "$1" "${2-}")"
      shift 2
      ;;
    --git-bin)
      HOST_GIT_BIN="$(option_value_or_die "$1" "${2-}")"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unsupported check-publish-commit-signatures option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ -z "${REPO_ROOT}" ]]; then
  echo "check-publish-commit-signatures requires --repo-root." >&2
  exit 2
fi
if [[ -z "${BASE_REF}" ]]; then
  echo "check-publish-commit-signatures requires --base-ref." >&2
  exit 2
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
  echo "check-publish-commit-signatures requires a git worktree: ${REPO_ROOT}" >&2
  exit 2
fi

base_commit="$(resolve_commit_or_die "${REPO_ROOT}" "${BASE_REF}")"
head_commit="$(resolve_commit_or_die "${REPO_ROOT}" "${HEAD_REF}")"
object_dir="$(resolve_object_dir_or_die "${REPO_ROOT}")"
object_format="$(run_clean_host_command_in_dir "${REPO_ROOT}" "${HOST_GIT_BIN}" --no-replace-objects rev-parse --show-object-format)"
verify_root="$(mktemp -d "${TMPDIR:-/tmp}/workcell-publish-signatures.XXXXXX")"
verify_log="${verify_root}/verify-commit.err"
commit_count=0
verification_git_dir=""

cleanup() {
  rm -rf "${verify_root}"
}
trap cleanup EXIT

run_clean_host_command_in_dir "${verify_root}" "${HOST_GIT_BIN}" init -q --object-format="${object_format}" "${verify_root}"
verification_git_dir="${verify_root}/.git"
mkdir -p "${verification_git_dir}/objects/info"
printf '%s\n' "${object_dir}" >"${verification_git_dir}/objects/info/alternates"

while IFS= read -r commit; do
  [[ -n "${commit}" ]] || continue
  commit_count=$((commit_count + 1))
  if ! run_clean_host_command_in_dir "${verify_root}" "${HOST_GIT_BIN}" --no-replace-objects --git-dir "${verification_git_dir}" verify-commit "${commit}" >/dev/null 2>"${verify_log}"; then
    echo "publish-pr requires verifiable signed commits; unable to verify commit ${commit} in ${BASE_REF}..${HEAD_REF}." >&2
    if [[ -s "${verify_log}" ]]; then
      sed 's/^/  /' "${verify_log}" >&2
    fi
    exit 2
  fi
done < <(run_clean_host_command_in_dir "${REPO_ROOT}" "${HOST_GIT_BIN}" --no-replace-objects rev-list --reverse "${base_commit}..${head_commit}")

if ((commit_count == 0)); then
  echo "publish-pr found no commits ahead of ${BASE_REF}: ${HEAD_REF}" >&2
  exit 2
fi

printf 'Publish commit signature check passed: commits=%d\n' "${commit_count}"
