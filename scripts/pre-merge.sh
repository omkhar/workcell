#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VALIDATOR_DOCKERFILE="${ROOT_DIR}/tools/validator/Dockerfile"
VALIDATOR_IMAGE_DEFAULT_TAG="workcell-validator:local-premerge-$(cksum "${VALIDATOR_DOCKERFILE}" | awk '{print $1}')"
VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE:-${VALIDATOR_IMAGE_DEFAULT_TAG}}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"
LOCAL_SNAPSHOT_ACTIVE="${WORKCELL_PREMERGE_LOCAL_SNAPSHOT_ACTIVE:-0}"
REBUILD_VALIDATOR=0
RUN_RELEASE_BUNDLE=1
RUN_REPRO=1
ALLOW_DIRTY=0
LOCAL_SNAPSHOT_MODE=""
LOCAL_INCLUDE_UNTRACKED=0
LOCAL_KEEP_DIR=0
ORIGINAL_ARGS=("$@")

usage() {
  cat <<EOF
Usage: $(basename "$0") [options]

Run the standard local pre-merge verification stack.

Options:
  --allow-dirty             Run against the live worktree even when it is dirty
  --local-snapshot <mode>   Run the local validation stack from a disposable
                            snapshot: head, index, or worktree
  --local-include-untracked Include untracked files with
                            --local-snapshot worktree
  --keep-local-dir          Preserve the local snapshot directory after exit
  --rebuild-validator        Rebuild the local validator image before validation
  --skip-release-bundle      Skip verify-release-bundle.sh
  --skip-repro               Skip verify-reproducible-build.sh
  -h, --help                 Show this help
EOF
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

local_premerge_repro_platforms() {
  case "$(uname -m)" in
    arm64 | aarch64)
      printf 'linux/arm64\n'
      ;;
    x86_64 | amd64)
      printf 'linux/amd64\n'
      ;;
    *)
      printf '\n'
      ;;
  esac
}

require_clean_tree() {
  local status_output=""

  status_output="$(git -C "${ROOT_DIR}" status --short --untracked-files=all)"
  if [[ -n "${status_output}" ]]; then
    echo "pre-merge requires a clean worktree, including untracked files, by default." >&2
    echo "Commit or stash local changes first, or rerun with --allow-dirty when you intentionally want to validate the live worktree." >&2
    printf '%s\n' "${status_output}" >&2
    exit 2
  fi
}

default_local_snapshot_parent() {
  local raw_parent=""

  if [[ -n "${WORKCELL_VALIDATION_SNAPSHOT_PARENT:-}" ]]; then
    raw_parent="${WORKCELL_VALIDATION_SNAPSHOT_PARENT}"
  elif [[ -n "${XDG_CACHE_HOME:-}" ]]; then
    raw_parent="${XDG_CACHE_HOME}/workcell-validation-snapshots"
  elif [[ -n "${HOME:-}" ]]; then
    if [[ "${OSTYPE:-}" == darwin* ]]; then
      raw_parent="${HOME}/Library/Caches/workcell-validation-snapshots"
    else
      raw_parent="${HOME}/.cache/workcell-validation-snapshots"
    fi
  else
    # Keep local validation snapshots outside Workcell-managed Colima caches so
    # host-invariant cleanup tests cannot race with the validation workspace,
    # while still landing under a user cache path that Docker can bind-mount.
    raw_parent="$(dirname "${ROOT_DIR}")"
  fi

  case "${raw_parent}" in
    /*)
      printf '%s\n' "${raw_parent}"
      ;;
    *)
      printf '%s\n' "${ROOT_DIR}/${raw_parent}"
      ;;
  esac
}

run_from_local_snapshot() {
  local -a snapshot_cmd=()
  local snapshot_parent=""
  local status=0

  [[ -n "${LOCAL_SNAPSHOT_MODE}" ]] || return 0
  [[ "${LOCAL_SNAPSHOT_ACTIVE}" == "1" ]] && return 0

  echo "[pre-merge] local validation will run from snapshot (${LOCAL_SNAPSHOT_MODE})." >&2
  snapshot_parent="$(default_local_snapshot_parent)"
  mkdir -p "${snapshot_parent}"
  snapshot_cmd=(
    env
    "WORKCELL_VALIDATION_SNAPSHOT_PARENT=${snapshot_parent}"
    "${ROOT_DIR}/scripts/with-validation-snapshot.sh"
    --repo "${ROOT_DIR}"
    --mode "${LOCAL_SNAPSHOT_MODE}"
  )
  if [[ "${LOCAL_INCLUDE_UNTRACKED}" -eq 1 ]]; then
    snapshot_cmd+=(--include-untracked)
  fi
  if [[ "${LOCAL_KEEP_DIR}" -eq 1 ]]; then
    snapshot_cmd+=(--keep-snapshot)
  fi
  snapshot_cmd+=(
    --
    env
    WORKCELL_PREMERGE_LOCAL_SNAPSHOT_ACTIVE=1
    ./scripts/pre-merge.sh
    "${ORIGINAL_ARGS[@]}"
  )

  "${snapshot_cmd[@]}" || status=$?
  exit "${status}"
}

build_validator_image() {
  if [[ "${REBUILD_VALIDATOR}" -eq 0 ]] && docker image inspect "${VALIDATOR_IMAGE}" >/dev/null 2>&1; then
    return 0
  fi

  docker build \
    -f "${ROOT_DIR}/tools/validator/Dockerfile" \
    -t "${VALIDATOR_IMAGE}" \
    "${ROOT_DIR}"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --allow-dirty)
      ALLOW_DIRTY=1
      shift
      ;;
    --local-snapshot)
      LOCAL_SNAPSHOT_MODE="${2-}"
      [[ -n "${LOCAL_SNAPSHOT_MODE}" ]] || {
        echo "Option --local-snapshot requires a value." >&2
        usage >&2
        exit 2
      }
      shift 2
      ;;
    --local-include-untracked)
      LOCAL_INCLUDE_UNTRACKED=1
      shift
      ;;
    --keep-local-dir)
      LOCAL_KEEP_DIR=1
      shift
      ;;
    --rebuild-validator)
      REBUILD_VALIDATOR=1
      shift
      ;;
    --skip-release-bundle)
      RUN_RELEASE_BUNDLE=0
      shift
      ;;
    --skip-repro)
      RUN_REPRO=0
      shift
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

require_tool docker
require_tool git
require_tool shellcheck

if [[ "${LOCAL_INCLUDE_UNTRACKED}" -eq 1 ]] && [[ "${LOCAL_SNAPSHOT_MODE}" != "worktree" ]]; then
  echo "--local-include-untracked requires --local-snapshot worktree." >&2
  exit 2
fi

run_from_local_snapshot

if [[ "${ALLOW_DIRTY}" -eq 0 ]]; then
  if [[ -z "${LOCAL_SNAPSHOT_MODE}" ]]; then
    require_clean_tree
  fi
fi

echo "[pre-merge] pinned-input policy"
"${ROOT_DIR}/scripts/check-pinned-inputs.sh"

echo "[pre-merge] GitHub macOS release test runner verification"
"${ROOT_DIR}/scripts/verify-github-macos-release-test-runners.sh" macos-26 macos-15

echo "[pre-merge] upstream Codex release verification"
"${ROOT_DIR}/scripts/verify-upstream-codex-release.sh"

echo "[pre-merge] upstream Claude release verification"
"${ROOT_DIR}/scripts/verify-upstream-claude-release.sh"

echo "[pre-merge] upstream Gemini release verification"
"${ROOT_DIR}/scripts/verify-upstream-gemini-release.sh"

echo "[pre-merge] pinned upstream refresh check"
"${ROOT_DIR}/scripts/update-upstream-pins.sh" --check

echo "[pre-merge] building validator image"
build_validator_image

echo "[pre-merge] workflow lint and policy analysis"
"${ROOT_DIR}/scripts/check-workflows.sh"

heavy_shellcheck_targets=()
for file in \
  "${ROOT_DIR}/scripts/workcell" \
  "${ROOT_DIR}/scripts/verify-invariants.sh"; do
  if [[ -f "${file}" ]]; then
    heavy_shellcheck_targets+=("${file}")
  fi
done
if ((${#heavy_shellcheck_targets[@]} > 0)); then
  echo "[pre-merge] host shellcheck for large shell harnesses"
  shellcheck -x "${heavy_shellcheck_targets[@]}"
fi

echo "[pre-merge] repository validation in validator container"
validator_uid="$(id -u)"
validator_gid="$(id -g)"
validator_home="/tmp/workcell-home-${validator_uid}"
validator_cache="${validator_home}/.cache"
validator_tmp="${validator_home}/.tmp"
docker run --rm \
  --user "${validator_uid}:${validator_gid}" \
  --entrypoint /bin/bash \
  -e WORKCELL_SKIP_HEAVY_HOST_SHELLCHECK=1 \
  -e HOME="${validator_home}" \
  -e XDG_CACHE_HOME="${validator_cache}" \
  -e GOCACHE="${validator_cache}/go-build" \
  -e GOMODCACHE="${validator_cache}/go-mod" \
  -e CARGO_TARGET_DIR="${validator_cache}/cargo-target" \
  -e TMPDIR="${validator_tmp}" \
  -v "${ROOT_DIR}:/workspace" \
  -w /workspace \
  "${VALIDATOR_IMAGE}" \
  -lc '
    set -euo pipefail
    mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"
    ./scripts/validate-repo.sh
  '

echo "[pre-merge] host launcher invariants"
"${ROOT_DIR}/scripts/verify-invariants.sh"

echo "[pre-merge] host publish-pr scenario"
"${ROOT_DIR}/tests/scenarios/shared/test-publish-pr-dry-run.sh"

echo "[pre-merge] container smoke"
"${ROOT_DIR}/scripts/container-smoke.sh"

if [[ "${RUN_RELEASE_BUNDLE}" -eq 1 ]]; then
  echo "[pre-merge] release bundle reproducibility"
  WORKCELL_VALIDATOR_IMAGE="${VALIDATOR_IMAGE}" \
    SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
    "${ROOT_DIR}/scripts/verify-release-bundle.sh"
fi

if [[ "${RUN_REPRO}" -eq 1 ]]; then
  PREMERGE_REPRO_PLATFORMS="${WORKCELL_PREMERGE_REPRO_PLATFORMS:-$(local_premerge_repro_platforms)}"
  if [[ -n "${PREMERGE_REPRO_PLATFORMS}" ]]; then
    echo "[pre-merge] runtime reproducibility (${PREMERGE_REPRO_PLATFORMS})"
    WORKCELL_REPRO_PLATFORMS="${PREMERGE_REPRO_PLATFORMS}" \
      SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
      "${ROOT_DIR}/scripts/verify-reproducible-build.sh"
  else
    echo "[pre-merge] runtime reproducibility"
    SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
      "${ROOT_DIR}/scripts/verify-reproducible-build.sh"
  fi
fi

echo "Workcell pre-merge validation passed."
