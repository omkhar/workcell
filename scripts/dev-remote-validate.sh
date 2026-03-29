#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME=/tmp \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_REMOTE_VALIDATE_ALLOW_SHARED_DAEMON_HEAVY_CHECKS="${WORKCELL_REMOTE_VALIDATE_ALLOW_SHARED_DAEMON_HEAVY_CHECKS-}" \
    WORKCELL_REMOTE_VALIDATE_BASE_DIR="${WORKCELL_REMOTE_VALIDATE_BASE_DIR-}" \
    WORKCELL_REMOTE_VALIDATE_CONFIG_PATH="${WORKCELL_REMOTE_VALIDATE_CONFIG_PATH-}" \
    WORKCELL_REMOTE_VALIDATE_HOST="${WORKCELL_REMOTE_VALIDATE_HOST-}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REAL_HOME="$(
  /usr/bin/env -i PATH="${TRUSTED_HOST_PATH}" /usr/bin/python3 - <<'PY'
import os
import pwd

print(pwd.getpwuid(os.getuid()).pw_dir)
PY
)"
LEGACY_LOCAL_REMOTE_CONFIG_PATH="${ROOT_DIR}/.workcell.remote.local"
REMOTE_CONFIG_PATH="${WORKCELL_REMOTE_VALIDATE_CONFIG_PATH:-${REAL_HOME}/.config/workcell/remote-validate.env}"
REMOTE_HOST="${WORKCELL_REMOTE_VALIDATE_HOST:-}"
REMOTE_BASE_DIR="${WORKCELL_REMOTE_VALIDATE_BASE_DIR:-/tmp}"
ALLOW_SHARED_DAEMON_HEAVY_CHECKS="${WORKCELL_REMOTE_VALIDATE_ALLOW_SHARED_DAEMON_HEAVY_CHECKS:-0}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"
REMOTE_USE_SUDO=1
KEEP_REMOTE_DIR=0
DRY_RUN=0
SNAPSHOT_MODE="index"
INCLUDE_UNTRACKED=0
TMP_ROOT=""
SNAPSHOT_DIR=""
HELPER_CONTEXT_DIR=""
REMOTE_DIR=""
REMOTE_HOME=""
REMOTE_LOGIN_UID=""
REMOTE_LOGIN_GID=""
REMOTE_LOGIN_USER=""
REMOTE_VALIDATOR_IMAGE=""
declare -a CHECKS=()

usage() {
  cat <<EOF
Usage: $(basename "$0") [options]

Stage a local Workcell snapshot to a remote amd64 builder, build a disposable
remote helper container there, and run the selected heavy validation inside
that container.

If ${REMOTE_CONFIG_PATH} exists, Workcell loads simple KEY=VALUE entries for
WORKCELL_REMOTE_VALIDATE_HOST, WORKCELL_REMOTE_VALIDATE_BASE_DIR, and
WORKCELL_REMOTE_VALIDATE_USE_SUDO from that host-local config before CLI
parsing. It also honors
WORKCELL_REMOTE_VALIDATE_ALLOW_SHARED_DAEMON_HEAVY_CHECKS=1 there when you
explicitly accept lower-assurance heavy checks on a shared remote Docker
daemon. Override the path with WORKCELL_REMOTE_VALIDATE_CONFIG_PATH or
--config <path>.

Options:
  --config <path>             Optional host-local config file to read before
                              CLI parsing
  --host <user@host>          Remote builder host
                              (required; may also be set via
                              WORKCELL_REMOTE_VALIDATE_HOST)
  --remote-base-dir <path>    Base directory for remote temp workspaces
                              (default: ${REMOTE_BASE_DIR})
  --source-date-epoch <sec>   SOURCE_DATE_EPOCH to use for remote checks
                              (default: git HEAD commit time)
  --snapshot <mode>           Snapshot source: head, index, or worktree
                              (default: ${SNAPSHOT_MODE})
  --include-untracked         Include untracked files with --snapshot worktree
  --no-remote-sudo            Do not use sudo for the remote workspace or
                              remote docker commands
  --check <name>              One or more checks to run. Valid values:
                              validate, smoke, repro, release-bundle
                              Default: validate
  --allow-shared-daemon-heavy-checks
                              Allow heavy remote checks that mount the shared
                              remote Docker daemon into the helper container.
                              This is lower-assurance and should be enabled
                              only on a reviewed builder.
  --keep-remote-dir           Preserve the remote temp directory after exit
  --dry-run                   Print the plan without staging or executing
  -h, --help                  Show this help
EOF
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_remote_host() {
  if [[ -z "${REMOTE_HOST}" ]]; then
    echo "Remote builder host is required. Pass --host <user@host> or set WORKCELL_REMOTE_VALIDATE_HOST." >&2
    exit 1
  fi
}

canonicalize_host_path() {
  local path="$1"

  /usr/bin/env -i PATH="${TRUSTED_HOST_PATH}" /usr/bin/python3 - "$path" <<'PY'
import os
import pathlib
import sys

print(pathlib.Path(sys.argv[1]).expanduser().resolve(strict=False))
PY
}

assert_supported_remote_config_path() {
  local candidate="$1"
  local canonical_candidate=""
  local canonical_root=""

  [[ -n "${candidate}" ]] || return 0
  canonical_candidate="$(canonicalize_host_path "${candidate}")"
  canonical_root="$(canonicalize_host_path "${ROOT_DIR}")"

  if [[ "${canonical_candidate}" == "${canonical_root}" ]] ||
    [[ "${canonical_candidate}" == "${canonical_root}"/* ]]; then
    echo "Remote builder config must live outside the repo checkout: ${canonical_candidate}" >&2
    exit 1
  fi
}

preparse_config_path() {
  local args=("$@")
  local i=0

  while [[ ${i} -lt ${#args[@]} ]]; do
    case "${args[${i}]}" in
      --config)
        if [[ $((i + 1)) -ge ${#args[@]} ]]; then
          echo "Option --config requires a value." >&2
          usage >&2
          exit 1
        fi
        REMOTE_CONFIG_PATH="${args[$((i + 1))]}"
        return 0
        ;;
      --config=*)
        REMOTE_CONFIG_PATH="${args[${i}]#--config=}"
        return 0
        ;;
    esac
    i=$((i + 1))
  done
}

load_local_remote_config() {
  local line=""
  local key=""
  local value=""

  if [[ -e "${LEGACY_LOCAL_REMOTE_CONFIG_PATH}" ]]; then
    echo "Legacy repo-local remote builder config is no longer supported: ${LEGACY_LOCAL_REMOTE_CONFIG_PATH}" >&2
    echo "Move it to ${REMOTE_CONFIG_PATH} or point WORKCELL_REMOTE_VALIDATE_CONFIG_PATH/--config at a host-local file." >&2
    exit 1
  fi

  assert_supported_remote_config_path "${REMOTE_CONFIG_PATH}"

  [[ -f "${REMOTE_CONFIG_PATH}" ]] || return 0
  ALLOW_SHARED_DAEMON_HEAVY_CHECKS=0

  while IFS= read -r line || [[ -n "${line}" ]]; do
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    [[ -z "${line}" ]] && continue
    [[ "${line}" == \#* ]] && continue
    if [[ "${line}" != *=* ]]; then
      echo "Unsupported entry in ${REMOTE_CONFIG_PATH}: ${line}" >&2
      exit 1
    fi
    key="${line%%=*}"
    value="${line#*=}"
    key="${key%"${key##*[![:space:]]}"}"
    value="${value#"${value%%[![:space:]]*}"}"
    value="${value%"${value##*[![:space:]]}"}"
    if [[ "${value}" == \"*\" && "${value}" == *\" ]]; then
      value="${value:1:${#value}-2}"
    elif [[ "${value}" == \'*\' && "${value}" == *\' ]]; then
      value="${value:1:${#value}-2}"
    fi
    case "${key}" in
      WORKCELL_REMOTE_VALIDATE_HOST)
        REMOTE_HOST="${value}"
        ;;
      WORKCELL_REMOTE_VALIDATE_BASE_DIR)
        REMOTE_BASE_DIR="${value}"
        ;;
      WORKCELL_REMOTE_VALIDATE_USE_SUDO)
        case "${value}" in
          0 | 1)
            REMOTE_USE_SUDO="${value}"
            ;;
          *)
            echo "WORKCELL_REMOTE_VALIDATE_USE_SUDO in ${REMOTE_CONFIG_PATH} must be 0 or 1." >&2
            exit 1
            ;;
        esac
        ;;
      WORKCELL_REMOTE_VALIDATE_ALLOW_SHARED_DAEMON_HEAVY_CHECKS)
        case "${value}" in
          0 | 1)
            ALLOW_SHARED_DAEMON_HEAVY_CHECKS="${value}"
            ;;
          *)
            echo "WORKCELL_REMOTE_VALIDATE_ALLOW_SHARED_DAEMON_HEAVY_CHECKS in ${REMOTE_CONFIG_PATH} must be 0 or 1." >&2
            exit 1
            ;;
        esac
        ;;
      *)
        echo "Unsupported key in ${REMOTE_CONFIG_PATH}: ${key}" >&2
        exit 1
        ;;
    esac
  done <"${REMOTE_CONFIG_PATH}"
}

add_check() {
  local check="$1"
  case "${check}" in
    validate | smoke | repro | release-bundle)
      CHECKS+=("${check}")
      ;;
    *)
      echo "Unsupported check: ${check}" >&2
      usage >&2
      exit 1
      ;;
  esac
}

preparse_config_path "$@"
load_local_remote_config

prepare_snapshot_dir() {
  local path_list_raw
  local path_list_filtered

  SNAPSHOT_DIR="${TMP_ROOT}/snapshot"
  mkdir -p "${SNAPSHOT_DIR}"

  case "${SNAPSHOT_MODE}" in
    head)
      git -C "${ROOT_DIR}" archive --format=tar HEAD | tar -C "${SNAPSHOT_DIR}" -xf -
      ;;
    index)
      git -C "${ROOT_DIR}" checkout-index --all --force --prefix="${SNAPSHOT_DIR}/"
      ;;
    worktree)
      path_list_raw="${TMP_ROOT}/paths.raw"
      path_list_filtered="${TMP_ROOT}/paths.filtered"

      if [[ "${INCLUDE_UNTRACKED}" == "1" ]]; then
        (
          cd "${ROOT_DIR}"
          git ls-files -z --cached --modified --others --exclude-standard --deduplicate >"${path_list_raw}"
        )
      else
        (
          cd "${ROOT_DIR}"
          git ls-files -z --cached --modified --deduplicate >"${path_list_raw}"
        )
      fi

      (
        cd "${ROOT_DIR}"
        while IFS= read -r -d '' path; do
          if [[ -e "${path}" || -L "${path}" ]]; then
            printf '%s\0' "${path}"
          fi
        done <"${path_list_raw}" >"${path_list_filtered}"
      )

      rsync --archive --from0 --files-from="${path_list_filtered}" "${ROOT_DIR}/" "${SNAPSHOT_DIR}/"
      ;;
    *)
      echo "Unsupported snapshot mode: ${SNAPSHOT_MODE}" >&2
      exit 1
      ;;
  esac
}

remote_cmd() {
  if [[ "${REMOTE_USE_SUDO}" == "1" ]]; then
    ssh "${REMOTE_HOST}" sudo -n "$@"
    return
  fi

  # Intentionally forward a vetted local argv vector through ssh unchanged.
  # shellcheck disable=SC2029
  ssh "${REMOTE_HOST}" "$@"
}

remote_bash() {
  if [[ "${REMOTE_USE_SUDO}" == "1" ]]; then
    ssh "${REMOTE_HOST}" sudo -n /bin/bash -s -- "$@"
    return
  fi

  ssh "${REMOTE_HOST}" /bin/bash -s -- "$@"
}

remote_docker() {
  if [[ "${REMOTE_USE_SUDO}" == "1" ]]; then
    ssh "${REMOTE_HOST}" sudo -n docker "$@"
    return
  fi

  # Intentionally forward a vetted local argv vector through ssh unchanged.
  # shellcheck disable=SC2029
  ssh "${REMOTE_HOST}" docker "$@"
}

cleanup() {
  rm -rf "${TMP_ROOT:-}"
  if [[ -n "${REMOTE_VALIDATOR_IMAGE}" ]]; then
    remote_docker image rm -f "${REMOTE_VALIDATOR_IMAGE}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${REMOTE_DIR}" ]] && [[ "${KEEP_REMOTE_DIR}" != "1" ]]; then
    remote_cmd rm -rf -- "${REMOTE_DIR}" >/dev/null 2>&1 || true
  fi
}

trap cleanup EXIT

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config)
      [[ $# -ge 2 ]] || {
        echo "Option --config requires a value." >&2
        usage >&2
        exit 1
      }
      REMOTE_CONFIG_PATH="$2"
      assert_supported_remote_config_path "${REMOTE_CONFIG_PATH}"
      load_local_remote_config
      shift 2
      ;;
    --config=*)
      REMOTE_CONFIG_PATH="${1#--config=}"
      assert_supported_remote_config_path "${REMOTE_CONFIG_PATH}"
      load_local_remote_config
      shift
      ;;
    --host)
      [[ $# -ge 2 ]] || {
        echo "Option --host requires a value." >&2
        usage >&2
        exit 1
      }
      REMOTE_HOST="$2"
      shift 2
      ;;
    --remote-base-dir)
      [[ $# -ge 2 ]] || {
        echo "Option --remote-base-dir requires a value." >&2
        usage >&2
        exit 1
      }
      REMOTE_BASE_DIR="$2"
      shift 2
      ;;
    --source-date-epoch)
      [[ $# -ge 2 ]] || {
        echo "Option --source-date-epoch requires a value." >&2
        usage >&2
        exit 1
      }
      SOURCE_DATE_EPOCH="$2"
      shift 2
      ;;
    --snapshot)
      [[ $# -ge 2 ]] || {
        echo "Option --snapshot requires a value." >&2
        usage >&2
        exit 1
      }
      SNAPSHOT_MODE="$2"
      shift 2
      ;;
    --include-untracked)
      INCLUDE_UNTRACKED=1
      shift
      ;;
    --no-remote-sudo)
      REMOTE_USE_SUDO=0
      shift
      ;;
    --check)
      [[ $# -ge 2 ]] || {
        echo "Option --check requires a value." >&2
        usage >&2
        exit 1
      }
      add_check "$2"
      shift 2
      ;;
    --allow-shared-daemon-heavy-checks)
      ALLOW_SHARED_DAEMON_HEAVY_CHECKS=1
      shift
      ;;
    --keep-remote-dir)
      KEEP_REMOTE_DIR=1
      shift
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

require_remote_host

if [[ "${#CHECKS[@]}" -eq 0 ]]; then
  CHECKS=(validate)
fi

if [[ "${SNAPSHOT_MODE}" != "worktree" ]] && [[ "${INCLUDE_UNTRACKED}" == "1" ]]; then
  echo "--include-untracked requires --snapshot worktree." >&2
  exit 1
fi

require_tool git
require_tool rsync
require_tool ssh
require_tool tar

if ! git -C "${ROOT_DIR}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "dev-remote-validate requires a git checkout." >&2
  exit 1
fi

TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-remote-validate.XXXXXX")"
prepare_snapshot_dir
HELPER_CONTEXT_DIR="${TMP_ROOT}/helper-context"
mkdir -p "${HELPER_CONTEXT_DIR}"
cp "${ROOT_DIR}/tools/remote-validator/Dockerfile" "${HELPER_CONTEXT_DIR}/Dockerfile"

echo "Remote host: ${REMOTE_HOST}"
echo "Remote base dir: ${REMOTE_BASE_DIR}"
echo "Remote sudo: ${REMOTE_USE_SUDO}"
echo "Remote config path: ${REMOTE_CONFIG_PATH}"
echo "Snapshot mode: ${SNAPSHOT_MODE}"
if [[ "${SNAPSHOT_MODE}" == "worktree" ]]; then
  echo "Include untracked: ${INCLUDE_UNTRACKED}"
fi
echo "Checks: ${CHECKS[*]}"
echo "Allow shared-daemon heavy checks: ${ALLOW_SHARED_DAEMON_HEAVY_CHECKS}"
echo "SOURCE_DATE_EPOCH: ${SOURCE_DATE_EPOCH}"

LIGHT_CHECKS=()
HEAVY_CHECKS=()
for check in "${CHECKS[@]}"; do
  case "${check}" in
    validate)
      LIGHT_CHECKS+=("${check}")
      ;;
    smoke | repro | release-bundle)
      HEAVY_CHECKS+=("${check}")
      ;;
  esac
done

if ((${#HEAVY_CHECKS[@]} > 0)) && [[ "${ALLOW_SHARED_DAEMON_HEAVY_CHECKS}" != "1" ]]; then
  echo "Heavy remote checks require --allow-shared-daemon-heavy-checks or WORKCELL_REMOTE_VALIDATE_ALLOW_SHARED_DAEMON_HEAVY_CHECKS=1 in the host-local config." >&2
  echo "Those checks mount the shared remote Docker daemon into the helper container and are lower-assurance on a shared builder." >&2
  exit 1
fi

if [[ "${DRY_RUN}" == "1" ]]; then
  echo "Dry run only; no files were staged to the remote builder."
  exit 0
fi

REMOTE_LOGIN_UID="$(ssh "${REMOTE_HOST}" id -u)"
REMOTE_LOGIN_GID="$(ssh "${REMOTE_HOST}" id -g)"
REMOTE_LOGIN_USER="$(ssh "${REMOTE_HOST}" id -un)"

remote_bash "${CHECKS[@]}" <<'EOF'
set -euo pipefail

need_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Remote builder is missing required tool: $1" >&2
    exit 1
  }
}

need_tool bash
need_tool docker
need_tool rsync

host_arch="$(uname -m)"
case "${host_arch}" in
  x86_64 | amd64) ;;
  *)
    echo "Remote builder must be amd64/x86_64, found: ${host_arch}" >&2
    exit 1
    ;;
esac

for check in "$@"; do
  case "${check}" in
    validate)
      ;;
    smoke | repro | release-bundle)
      ;;
    *)
      echo "Unknown remote check in prerequisite probe: ${check}" >&2
      exit 1
      ;;
  esac
done
EOF

remote_cmd mkdir -p -- "${REMOTE_BASE_DIR%/}"
REMOTE_DIR="$(remote_cmd mktemp -d "${REMOTE_BASE_DIR%/}/workcell-remote-validate.XXXXXX")"
REMOTE_HOME="${REMOTE_DIR}/home"
REMOTE_VALIDATOR_IMAGE="workcell-remote-validator:$(basename "${REMOTE_DIR}")"

echo "Remote workspace: ${REMOTE_DIR}"
echo "Remote helper image: ${REMOTE_VALIDATOR_IMAGE}"

if [[ "${REMOTE_USE_SUDO}" == "1" ]]; then
  remote_cmd install -d -m 0700 -o 0 -g 0 "${REMOTE_DIR}/repo"
  remote_cmd install -d -m 0700 -o 0 -g 0 "${REMOTE_DIR}/helper"
  remote_cmd install -d -m 0700 -o 0 -g 0 "${REMOTE_HOME}"
else
  remote_cmd mkdir -p -- "${REMOTE_DIR}/repo" "${REMOTE_DIR}/helper" "${REMOTE_HOME}"
  remote_cmd chmod 0700 "${REMOTE_DIR}" "${REMOTE_DIR}/repo" "${REMOTE_DIR}/helper" "${REMOTE_HOME}"
fi

rsync_args=(
  --archive
  "${SNAPSHOT_DIR}/"
  "${REMOTE_HOST}:${REMOTE_DIR}/repo/"
)
if [[ "${REMOTE_USE_SUDO}" == "1" ]]; then
  rsync_args=(--rsync-path="sudo -n rsync" --chown=0:0 "${rsync_args[@]}")
fi
rsync "${rsync_args[@]}"
if [[ "${REMOTE_USE_SUDO}" == "1" ]]; then
  remote_cmd install -d -m 0755 -o "${REMOTE_LOGIN_UID}" -g "${REMOTE_LOGIN_GID}" "${REMOTE_DIR}/repo/tmp"
else
  remote_cmd mkdir -p -- "${REMOTE_DIR}/repo/tmp"
  remote_cmd chown "${REMOTE_LOGIN_UID}:${REMOTE_LOGIN_GID}" "${REMOTE_DIR}/repo/tmp"
  remote_cmd chmod 0755 "${REMOTE_DIR}/repo/tmp"
fi

helper_rsync_args=(
  --archive
  "${HELPER_CONTEXT_DIR}/"
  "${REMOTE_HOST}:${REMOTE_DIR}/helper/"
)
if [[ "${REMOTE_USE_SUDO}" == "1" ]]; then
  helper_rsync_args=(--rsync-path="sudo -n rsync" --chown=0:0 "${helper_rsync_args[@]}")
fi
rsync "${helper_rsync_args[@]}"

remote_docker build \
  --pull=false \
  -t "${REMOTE_VALIDATOR_IMAGE}" \
  -f "${REMOTE_DIR}/helper/Dockerfile" \
  "${REMOTE_DIR}/helper" >/dev/null

run_remote_validator_container() {
  local needs_docker="$1"
  shift
  local -a selected_checks=("$@")
  local -a docker_args=(
    run --rm
    -i
    -e HOME=/tmp/workcell-home
    -e SSL_CERT_DIR=/etc/ssl/certs
    -e SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
    -e SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}"
    -e WORKCELL_TEST_HOST_GID="${REMOTE_LOGIN_GID}"
    -e WORKCELL_TEST_HOST_UID="${REMOTE_LOGIN_UID}"
    -e WORKCELL_TEST_HOST_USER="${REMOTE_LOGIN_USER}"
    -v /etc/ssl/certs:/etc/ssl/certs:ro
    -v "${REMOTE_DIR}/repo:/workspace"
  )

  if [[ "${needs_docker}" == "1" ]]; then
    docker_args+=(
      -e WORKCELL_CONTAINER_SMOKE_DOCKER_CONTEXT=default
      -e WORKCELL_DOCKER_HOST_HOME_ROOT="${REMOTE_HOME}"
      -e WORKCELL_DOCKER_HOST_WORKSPACE_ROOT="${REMOTE_DIR}/repo"
      -e WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC=1
      -e WORKCELL_REMOTE_BUILDKIT_LOCAL_CA=/host-local-ca
      -e WORKCELL_REMOTE_BUILDKIT_SSL_CERTS=/host-ssl-certs
      -e WORKCELL_REPRO_PLATFORMS=linux/amd64
      -e WORKCELL_RELEASE_BUNDLE_DOCKER_CONTEXT=default
      -e WORKCELL_REPRO_DOCKER_CONTEXT=default
      -v /etc/ssl/certs:/host-ssl-certs:ro
      -v /usr/local/share/ca-certificates:/host-local-ca:ro
      -v "${REMOTE_HOME}:/tmp/workcell-home"
      -v /var/run/docker.sock:/var/run/docker.sock
    )
  fi

  remote_docker "${docker_args[@]}" \
    --entrypoint /bin/bash \
    "${REMOTE_VALIDATOR_IMAGE}" \
    -s -- "${selected_checks[@]}" <<'EOF'
set -euo pipefail

REMOTE_BUILDER_NAME=""

cleanup_remote_builder() {
  if [[ -n "${REMOTE_BUILDER_NAME}" ]]; then
    docker buildx rm -f "${REMOTE_BUILDER_NAME}" >/dev/null 2>&1 || true
  fi
}

trap cleanup_remote_builder EXIT

cd /workspace

mkdir -p /workspace/tmp
chown "${WORKCELL_TEST_HOST_UID}:${WORKCELL_TEST_HOST_GID}" /workspace/tmp
chmod 0755 /workspace/tmp

rm -rf /workspace/.git
git init -q
git config user.name "Workcell Remote Validate"
git config user.email "workcell@example.invalid"
git add -A
if git diff --cached --quiet --exit-code; then
  echo "Remote snapshot is empty; nothing to validate." >&2
  exit 1
fi
GIT_AUTHOR_DATE="@${SOURCE_DATE_EPOCH} +0000" \
GIT_COMMITTER_DATE="@${SOURCE_DATE_EPOCH} +0000" \
git commit -q --no-gpg-sign -m "Workcell remote validation snapshot"

for check in "$@"; do
  case "${check}" in
    repro | release-bundle)
      if [[ -z "${REMOTE_BUILDER_NAME}" ]]; then
        REMOTE_BUILDER_NAME="workcell-remote-validate-$$"
        export BUILDX_BUILDER="${REMOTE_BUILDER_NAME}"
      fi
      ;;
  esac
done

for check in "$@"; do
  case "${check}" in
    validate)
      echo "[remote-container] validate-repo"
      ./scripts/validate-repo.sh
      ;;
    smoke)
      echo "[remote-container] container-smoke"
      ./scripts/container-smoke.sh
      ;;
    repro)
      echo "[remote-container] verify-reproducible-build"
      ./scripts/verify-reproducible-build.sh
      ;;
    release-bundle)
      echo "[remote-container] verify-release-bundle"
      ./scripts/verify-release-bundle.sh
      ;;
    *)
      echo "Unknown remote check: ${check}" >&2
      exit 1
      ;;
  esac
done
EOF
}

if ((${#LIGHT_CHECKS[@]} > 0)); then
  run_remote_validator_container 0 "${LIGHT_CHECKS[@]}"
fi

if ((${#HEAVY_CHECKS[@]} > 0)); then
  run_remote_validator_container 1 "${HEAVY_CHECKS[@]}"
fi

if [[ "${KEEP_REMOTE_DIR}" == "1" ]]; then
  echo "Remote validation finished. Preserved workspace: ${REMOTE_DIR}"
else
  echo "Remote validation finished."
fi
