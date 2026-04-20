#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME=/tmp \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_CONTAINER_SMOKE_DOCKER_CONTEXT="${WORKCELL_CONTAINER_SMOKE_DOCKER_CONTEXT-}" \
    WORKCELL_DOCKER_REAL_HOME="${WORKCELL_DOCKER_REAL_HOME-}" \
    WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC="${WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC-}" \
    WORKCELL_DOCKER_HOST_HOME_ROOT="${WORKCELL_DOCKER_HOST_HOME_ROOT-}" \
    WORKCELL_DOCKER_HOST_WORKSPACE_ROOT="${WORKCELL_DOCKER_HOST_WORKSPACE_ROOT-}" \
    WORKCELL_IMAGE_TAG="${WORKCELL_IMAGE_TAG-}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH-}" \
    WORKCELL_TEST_HOST_GID="${WORKCELL_TEST_HOST_GID-}" \
    WORKCELL_TEST_HOST_UID="${WORKCELL_TEST_HOST_UID-}" \
    WORKCELL_TEST_HOST_USER="${WORKCELL_TEST_HOST_USER-}" \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
trap 'echo "container-smoke failed at line ${LINENO}" >&2' ERR
export PATH="${TRUSTED_HOST_PATH}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${WORKCELL_GO_BIN:-}"
# shellcheck source=/dev/null
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
IMAGE_TAG="${WORKCELL_IMAGE_TAG:-workcell:smoke}"
DOCKER_CONTEXT_NAME="${WORKCELL_CONTAINER_SMOKE_DOCKER_CONTEXT:-}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"
HOST_UID="${WORKCELL_TEST_HOST_UID:-$(id -u)}"
HOST_GID="${WORKCELL_TEST_HOST_GID:-$(id -g)}"
HOST_USER="${WORKCELL_TEST_HOST_USER:-$(id -un)}"
SMOKE_WORKSPACE=""
INJECTION_FIXTURE_ROOT=""
INJECTION_BUNDLE_ROOT=""
WORKSPACE_IMPORT_ROOT=""
declare -a WORKSPACE_IMPORT_ARGS=()
declare -a RUNTIME_SECURITY_ARGS=()

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "container-smoke-entrypoint-ok"
  exit 0
fi

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

resolve_go_bin() {
  if [[ -n "${GO_BIN}" && -x "${GO_BIN}" ]]; then
    return 0
  fi
  if GO_BIN="$(command -v go 2>/dev/null)"; then
    return 0
  fi
  for candidate in \
    /opt/homebrew/bin/go \
    /usr/local/go/bin/go \
    /usr/local/bin/go \
    /usr/bin/go; do
    if [[ -x "${candidate}" ]]; then
      GO_BIN="${candidate}"
      return 0
    fi
  done
  echo "Missing required tool: go" >&2
  exit 1
}

go_runtimeutil() {
  resolve_go_bin
  (cd "${ROOT_DIR}" && "${GO_BIN}" run ./cmd/workcell-runtimeutil "$@")
}

container_smoke_die() {
  echo "$*" >&2
  return 1
}

host_symlink_component_is_allowed() {
  local candidate="$1"

  if [[ "$(uname -s)" == "Darwin" ]]; then
    case "${candidate}" in
      /var | /tmp)
        return 0
        ;;
    esac
  fi
  return 1
}

assert_host_path_components_not_symlinked() {
  local target_path="$1"
  local label="$2"
  local include_target="${3:-1}"
  local current=""
  local parent=""

  case "${target_path}" in
    /*) ;;
    *)
      container_smoke_die "container-smoke requires absolute paths for ${label}: ${target_path}"
      return 1
      ;;
  esac

  if [[ "${include_target}" == "1" ]]; then
    current="${target_path}"
  else
    current="$(dirname "${target_path}")"
  fi

  while :; do
    if [[ -L "${current}" ]]; then
      if host_symlink_component_is_allowed "${current}"; then
        if [[ "${current}" == "/" ]]; then
          return 0
        fi
        parent="$(dirname "${current}")"
        [[ "${parent}" != "${current}" ]] || return 0
        current="${parent}"
        continue
      fi
      container_smoke_die "container-smoke refuses ${label}: symlinked path component ${current}"
      return 1
    fi
    if [[ "${current}" == "/" ]]; then
      return 0
    fi
    parent="$(dirname "${current}")"
    [[ "${parent}" != "${current}" ]] || return 0
    current="${parent}"
  done
}

assert_host_tree_contains_no_symlinks() {
  local target_path="$1"
  local label="$2"
  local first_symlink=""

  [[ -d "${target_path}" ]] || return 0
  if IFS= read -r -d '' first_symlink < <(find -P "${target_path}" -type l -print0 2>/dev/null); then
    container_smoke_die "container-smoke refuses ${label}: symlinked tree entry ${first_symlink}"
    return 1
  fi
  return 0
}

align_path_for_mapped_runtime_user() {
  local target_path="$1"
  local file_mode="$2"
  local dir_mode="$3"
  local path=""

  [[ -e "${target_path}" || -L "${target_path}" ]] || return 0
  assert_host_path_components_not_symlinked "${target_path}" "mapped runtime ownership alignment" || return 1

  if [[ -L "${target_path}" ]]; then
    container_smoke_die "container-smoke refuses mapped runtime ownership alignment: symlink target ${target_path}"
    return 1
  fi

  if [[ -d "${target_path}" ]]; then
    assert_host_tree_contains_no_symlinks "${target_path}" "mapped runtime ownership alignment" || return 1
    while IFS= read -r -d '' path; do
      if [[ "$(id -u)" == "0" ]]; then
        chown "${HOST_UID}:${HOST_GID}" "${path}"
      fi
      if [[ -d "${path}" ]]; then
        chmod "${dir_mode}" "${path}"
      else
        chmod "${file_mode}" "${path}"
      fi
    done < <(find -P "${target_path}" \( -type d -o -type f \) -print0)
    return 0
  fi

  if [[ ! -f "${target_path}" ]]; then
    container_smoke_die "container-smoke refuses mapped runtime ownership alignment for unsupported path type: ${target_path}"
    return 1
  fi

  if [[ "$(id -u)" == "0" ]]; then
    chown "${HOST_UID}:${HOST_GID}" "${target_path}"
  fi
  chmod "${file_mode}" "${target_path}"
}

run_as_mapped_host_user() {
  if [[ "$(id -u)" == "0" ]] && [[ "${HOST_UID}" != "$(id -u)" || "${HOST_GID}" != "$(id -g)" ]]; then
    setpriv --reuid "${HOST_UID}" --regid "${HOST_GID}" --clear-groups "$@"
    return
  fi

  "$@"
}

remove_host_path_safely() {
  local target_path="$1"
  local label="$2"

  [[ -e "${target_path}" || -L "${target_path}" ]] || return 0
  assert_host_path_components_not_symlinked "${target_path}" "${label}" 0 || return 1
  if [[ -L "${target_path}" ]]; then
    rm -f "${target_path}"
    return 0
  fi
  assert_host_path_components_not_symlinked "${target_path}" "${label}" || return 1
  rm -rf "${target_path}"
}

cleanup_workspace_scratch() {
  local workspace_root="${1:-${SMOKE_WORKSPACE:-${ROOT_DIR}}}"
  local tmp_root="${workspace_root}/tmp"
  local entry=""

  assert_host_path_components_not_symlinked "${workspace_root}" "workspace scratch cleanup" || return 1
  remove_host_path_safely "${workspace_root}/.workcell-provider-copy-tampered" "workspace scratch cleanup"
  remove_host_path_safely "${workspace_root}/.workcell-provider-copy-aggressive" "workspace scratch cleanup"
  remove_host_path_safely "${workspace_root}/.workcell-provider-copy-minimal" "workspace scratch cleanup"
  remove_host_path_safely "${workspace_root}/.workcell-provider-copy-split" "workspace scratch cleanup"
  remove_host_path_safely "${workspace_root}/.workcell-benign-marker-package" "workspace scratch cleanup"
  remove_host_path_safely "${workspace_root}/.workcell-provider-copy-no-package.js" "workspace scratch cleanup"

  [[ -e "${tmp_root}" || -L "${tmp_root}" ]] || return 0
  assert_host_path_components_not_symlinked "${tmp_root}" "workspace scratch cleanup" || return 1
  while IFS= read -r -d '' entry; do
    remove_host_path_safely "${entry}" "workspace scratch cleanup"
  done < <(
    find -P "${tmp_root}" -mindepth 1 -maxdepth 1 \
      \( -name '.workcell-*' -o -name 'workcell-*' \) -print0
  )
}

validate_smoke_workspace_relative_path() {
  local relative_path="$1"

  case "${relative_path}" in
    '' | . | .. | ./* | /* | ../* | */../* | */..)
      container_smoke_die "container-smoke refuses unsafe smoke workspace path: ${relative_path}"
      return 1
      ;;
  esac
  return 0
}

copy_smoke_workspace_path() {
  local source_root="$1"
  local destination_root="$2"
  local relative_path="$3"
  local source_path="${source_root}/${relative_path}"
  local destination_path="${destination_root}/${relative_path}"

  validate_smoke_workspace_relative_path "${relative_path}" || return 1
  [[ -e "${source_path}" || -L "${source_path}" ]] || return 0
  assert_host_path_components_not_symlinked "${source_path}" "smoke workspace source path" || return 1

  if [[ -d "${source_path}" ]]; then
    mkdir -p "${destination_path}"
    return 0
  fi
  if [[ -f "${source_path}" ]]; then
    mkdir -p "$(dirname "${destination_path}")"
    cp -p "${source_path}" "${destination_path}"
    return 0
  fi

  container_smoke_die "container-smoke refuses unsupported smoke workspace source type: ${source_path}"
  return 1
}

stage_smoke_workspace_from_path_list() {
  local source_root="$1"
  local destination_root="$2"
  local path_list="$3"
  local relative_path=""

  while IFS= read -r -d '' relative_path; do
    copy_smoke_workspace_path "${source_root}" "${destination_root}" "${relative_path}" || return 1
  done <"${path_list}"
}

run_self_test_host_path_hardening() {
  local test_root=""
  local mutable_tree=""
  local outside_root=""
  local source_root=""
  local destination_root=""
  local path_list=""
  local workspace_root=""
  local bundle_source_root=""
  local bundle_destination_root=""
  local bundle_parent_link=""
  local output=""
  local failed=0

  test_root="$(mktemp -d "${TMPDIR:-/tmp}/workcell-container-smoke-selftest.XXXXXX")"
  mutable_tree="${test_root}/mutable-tree"
  outside_root="${test_root}/outside"
  mkdir -p "${mutable_tree}" "${outside_root}"
  printf 'sentinel\n' >"${outside_root}/keep.txt"
  ln -s "${outside_root}" "${mutable_tree}/escape-link"

  if output="$(align_path_for_mapped_runtime_user "${mutable_tree}" 0644 0755 2>&1)"; then
    echo "Expected align_path_for_mapped_runtime_user to reject a nested symlinked tree entry" >&2
    failed=1
  elif [[ "${output}" != *'symlinked tree entry'* ]]; then
    echo "Expected align_path_for_mapped_runtime_user rejection to mention the symlinked tree entry" >&2
    printf '%s\n' "${output}" >&2
    failed=1
  fi
  [[ -f "${outside_root}/keep.txt" ]] || {
    echo "align_path_for_mapped_runtime_user touched data outside the managed tree" >&2
    failed=1
  }

  source_root="${test_root}/source"
  destination_root="${test_root}/destination"
  path_list="${test_root}/paths.list"
  mkdir -p "${source_root}" "${destination_root}"
  ln -s "${outside_root}/keep.txt" "${source_root}/leak"
  printf 'leak\0' >"${path_list}"
  if output="$(stage_smoke_workspace_from_path_list "${source_root}" "${destination_root}" "${path_list}" 2>&1)"; then
    echo "Expected smoke workspace staging to reject symlinked source paths" >&2
    failed=1
  elif [[ "${output}" != *'symlinked path component'* ]]; then
    echo "Expected smoke workspace staging rejection to mention the blocked source path" >&2
    printf '%s\n' "${output}" >&2
    failed=1
  fi
  [[ ! -e "${destination_root}/leak" ]] || {
    echo "Smoke workspace staging copied a blocked symlink path" >&2
    failed=1
  }

  printf '../escape\0' >"${path_list}"
  if output="$(stage_smoke_workspace_from_path_list "${source_root}" "${destination_root}" "${path_list}" 2>&1)"; then
    echo "Expected smoke workspace staging to reject unsafe relative paths" >&2
    failed=1
  elif [[ "${output}" != *'unsafe smoke workspace path'* ]]; then
    echo "Expected smoke workspace staging rejection to mention the unsafe relative path" >&2
    printf '%s\n' "${output}" >&2
    failed=1
  fi

  workspace_root="${test_root}/workspace"
  mkdir -p "${workspace_root}"
  ln -s "${outside_root}" "${workspace_root}/tmp"
  if output="$(cleanup_workspace_scratch "${workspace_root}" 2>&1)"; then
    echo "Expected cleanup_workspace_scratch to reject a symlinked tmp root" >&2
    failed=1
  elif [[ "${output}" != *'symlinked path component'* ]]; then
    echo "Expected cleanup_workspace_scratch rejection to mention the symlinked tmp root" >&2
    printf '%s\n' "${output}" >&2
    failed=1
  fi
  [[ -f "${outside_root}/keep.txt" ]] || {
    echo "cleanup_workspace_scratch followed a symlinked tmp root" >&2
    failed=1
  }

  bundle_source_root="${test_root}/bundle-source"
  bundle_destination_root="${test_root}/bundle-destination"
  mkdir -p "${bundle_source_root}" "${test_root}/bundle-destination-parent"
  ln -s "${outside_root}/keep.txt" "${bundle_source_root}/leak"
  if output="$(clone_bundle_with_credential_override \
    "${bundle_source_root}" \
    "${bundle_destination_root}" \
    "github_hosts" \
    "/override" 2>&1)"; then
    echo "Expected bundle clone to reject a source tree with symlinked entries" >&2
    failed=1
  elif [[ "${output}" != *'symlinked tree entry'* ]]; then
    echo "Expected bundle clone rejection to mention the blocked source tree entry" >&2
    printf '%s\n' "${output}" >&2
    failed=1
  fi
  [[ ! -e "${bundle_destination_root}" ]] || {
    echo "bundle clone created a destination after rejecting a symlinked source tree" >&2
    failed=1
  }
  [[ -f "${outside_root}/keep.txt" ]] || {
    echo "bundle clone source rejection touched data outside the managed tree" >&2
    failed=1
  }

  rm -rf "${bundle_source_root}"
  mkdir -p "${bundle_source_root}"
  bundle_parent_link="${test_root}/bundle-destination-parent-link"
  mkdir -p "${outside_root}/bundle-existing-destination"
  ln -s "${outside_root}" "${bundle_parent_link}"
  if output="$(clone_bundle_with_credential_override \
    "${bundle_source_root}" \
    "${bundle_parent_link}/bundle-existing-destination" \
    "github_hosts" \
    "/override" 2>&1)"; then
    echo "Expected bundle clone to reject a destination with symlinked path components" >&2
    failed=1
  elif [[ "${output}" != *'symlinked path component'* ]]; then
    echo "Expected bundle clone rejection to mention the blocked destination path" >&2
    printf '%s\n' "${output}" >&2
    failed=1
  fi
  [[ -d "${outside_root}/bundle-existing-destination" ]] || {
    echo "bundle clone destination rejection removed data outside the managed tree" >&2
    failed=1
  }

  rm -rf "${test_root}"
  [[ "${failed}" == "0" ]] || return 1
  echo "container-smoke-host-path-hardening-ok"
}

prepare_smoke_workspace() {
  local path_list_raw=""
  local path_list_filtered=""

  mkdir -p "${ROOT_DIR}/tmp"
  align_path_for_mapped_runtime_user "${ROOT_DIR}/tmp" 0644 0755
  SMOKE_WORKSPACE="$(run_as_mapped_host_user mktemp -d "${ROOT_DIR}/tmp/workcell-smoke-workspace.XXXXXX")"

  if ! git -C "${ROOT_DIR}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "container-smoke requires a git checkout" >&2
    exit 1
  fi

  path_list_raw="$(mktemp "${ROOT_DIR}/tmp/workcell-smoke-paths-raw.XXXXXX")"
  path_list_filtered="$(mktemp "${ROOT_DIR}/tmp/workcell-smoke-paths-filtered.XXXXXX")"

  (
    cd "${ROOT_DIR}"
    git ls-files -z --cached --modified --others --exclude-standard --deduplicate >"${path_list_raw}"
  )

  (
    cd "${ROOT_DIR}"
    while IFS= read -r -d '' path; do
      if [[ -e "${path}" || -L "${path}" ]]; then
        printf '%s\0' "${path}"
      fi
    done <"${path_list_raw}" >"${path_list_filtered}"
  )

  (
    cd "${ROOT_DIR}"
    stage_smoke_workspace_from_path_list "${ROOT_DIR}" "${SMOKE_WORKSPACE}" "${path_list_filtered}"
  )

  rm -f "${path_list_raw}" "${path_list_filtered}"
  mkdir -p "${SMOKE_WORKSPACE}/tmp"
  # 1777 (sticky + world-writable) mirrors the container's /tmp posture so the
  # mapped runtime user can write here without elevated privileges.  The second
  # chmod re-applies 1777 after align_path_for_mapped_runtime_user, which resets
  # the workspace root to 0755 as part of UID alignment.
  chmod 1777 "${SMOKE_WORKSPACE}" "${SMOKE_WORKSPACE}/tmp"
  align_path_for_mapped_runtime_user "${SMOKE_WORKSPACE}" 0644 0755
  chmod 1777 "${SMOKE_WORKSPACE}/tmp"
}

cleanup() {
  cleanup_workcell_trusted_docker_client
  cleanup_workspace_scratch "${ROOT_DIR}" || true
  if [[ -n "${SMOKE_WORKSPACE}" ]]; then
    cleanup_workspace_scratch "${SMOKE_WORKSPACE}" || true
  fi
  if [[ -n "${INJECTION_FIXTURE_ROOT}" ]]; then
    remove_host_path_safely "${INJECTION_FIXTURE_ROOT}" "container-smoke cleanup" || true
  fi
  if [[ -n "${INJECTION_BUNDLE_ROOT}" ]]; then
    remove_host_path_safely "${INJECTION_BUNDLE_ROOT}" "container-smoke cleanup" || true
  fi
  if [[ -n "${WORKSPACE_IMPORT_ROOT}" ]]; then
    remove_host_path_safely "${WORKSPACE_IMPORT_ROOT}" "container-smoke cleanup" || true
  fi
  if [[ -n "${SMOKE_WORKSPACE}" ]]; then
    remove_host_path_safely "${SMOKE_WORKSPACE}" "container-smoke cleanup" || true
  fi
}

select_docker_context() {
  select_workcell_docker_context "Requested Docker context" "No healthy Docker context found" colima default
}

docker_cmd() {
  if [[ -n "${DOCKER_CONTEXT_NAME}" ]]; then
    docker --context "${DOCKER_CONTEXT_NAME}" "$@"
  else
    docker "$@"
  fi
}

populate_runtime_security_args() {
  local mutability="$1"

  RUNTIME_SECURITY_ARGS=(--cap-drop ALL)
  if [[ "${mutability}" == "ephemeral" ]]; then
    RUNTIME_SECURITY_ARGS+=(
      --cap-add SETUID
      --cap-add SETGID
    )
  fi
}

prepare_direct_mount_spec_for_bundle() {
  local bundle_root="$1"
  local mount_spec_path="${bundle_root}.mounts.json"

  "${ROOT_DIR}/scripts/lib/extract_direct_mounts" \
    --manifest "${bundle_root}/manifest.json" \
    --mount-spec "${mount_spec_path}" >/dev/null
  align_path_for_mapped_runtime_user "${bundle_root}" 0644 0755
  align_path_for_mapped_runtime_user "${mount_spec_path}" 0644 0755
}

clone_bundle_with_credential_override() {
  local source_bundle="$1"
  local bundle_root="$2"
  local credential_key="$3"
  local override_source="$4"

  assert_host_path_components_not_symlinked "${source_bundle}" "bundle clone source" || return 1
  assert_host_tree_contains_no_symlinks "${source_bundle}" "bundle clone source" || return 1
  remove_host_path_safely "${bundle_root}" "bundle clone destination" || return 1
  cp -R "${source_bundle}" "${bundle_root}"
  go_runtimeutil rewrite-bundle-credential-source \
    "${bundle_root}/manifest.json" \
    "${source_bundle}.mounts.json" \
    "${credential_key}" \
    "${override_source}"
  prepare_direct_mount_spec_for_bundle "${bundle_root}"
}

direct_mount_specs_for_bundle() {
  local bundle_root="$1"
  local mount_spec_path="${bundle_root}.mounts.json"
  [[ -f "${mount_spec_path}" ]] || return 0

  go_runtimeutil list-direct-mounts "${mount_spec_path}"
}

workspace_import_mounts() {
  if [[ -d "${WORKSPACE_IMPORT_ROOT}" ]] && find "${WORKSPACE_IMPORT_ROOT}" -type f -print -quit | grep -q .; then
    printf -- '%s\0' \
      -v \
      "$(workcell_docker_host_path "${WORKSPACE_IMPORT_ROOT}"):/opt/workcell/workspace-control-plane:ro"
  fi
}

populate_workspace_import_mounts() {
  local mount_spec=""

  WORKSPACE_IMPORT_ARGS=()
  while IFS= read -r -d '' mount_spec; do
    WORKSPACE_IMPORT_ARGS+=("${mount_spec}")
  done < <(workspace_import_mounts)
}

run_container() {
  local agent="$1"
  local docker_workspace=""
  shift

  docker_workspace="$(workcell_docker_host_path "${SMOKE_WORKSPACE}")"
  populate_workspace_import_mounts
  populate_runtime_security_args ephemeral

  docker_cmd run --rm \
    ${RUNTIME_SECURITY_ARGS[@]+"${RUNTIME_SECURITY_ARGS[@]}"} \
    --user 0:0 \
    --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
    --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
    --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
    -e AGENT_NAME="${agent}" \
    -e AGENT_UI=cli \
    -e WORKCELL_CONTAINER_MUTABILITY=ephemeral \
    -e WORKCELL_HOST_UID="${HOST_UID}" \
    -e WORKCELL_HOST_GID="${HOST_GID}" \
    -e WORKCELL_HOST_USER="${HOST_USER}" \
    -e CODEX_PROFILE=strict \
    -e HOME=/state/agent-home \
    -e CODEX_HOME=/state/agent-home/.codex \
    -e TMPDIR=/state/tmp \
    -e WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC="${WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC-}" \
    -e WORKCELL_RUNTIME=1 \
    -e WORKSPACE=/workspace \
    -e WORKCELL_WORKSPACE_IMPORT_ROOT=/opt/workcell/workspace-control-plane \
    -v "${docker_workspace}:/workspace" \
    ${WORKSPACE_IMPORT_ARGS[@]+"${WORKSPACE_IMPORT_ARGS[@]}"} \
    --entrypoint "$1" \
    "${IMAGE_TAG}" "${@:2}"
}

run_container_stdin() {
  local agent="$1"
  local docker_workspace=""
  shift

  docker_workspace="$(workcell_docker_host_path "${SMOKE_WORKSPACE}")"
  populate_workspace_import_mounts
  populate_runtime_security_args ephemeral

  docker_cmd run --rm -i \
    ${RUNTIME_SECURITY_ARGS[@]+"${RUNTIME_SECURITY_ARGS[@]}"} \
    --user 0:0 \
    --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
    --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
    --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
    -e AGENT_NAME="${agent}" \
    -e AGENT_UI=cli \
    -e WORKCELL_CONTAINER_MUTABILITY=ephemeral \
    -e WORKCELL_HOST_UID="${HOST_UID}" \
    -e WORKCELL_HOST_GID="${HOST_GID}" \
    -e WORKCELL_HOST_USER="${HOST_USER}" \
    -e CODEX_PROFILE=strict \
    -e HOME=/state/agent-home \
    -e CODEX_HOME=/state/agent-home/.codex \
    -e TMPDIR=/state/tmp \
    -e WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC="${WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC-}" \
    -e WORKCELL_RUNTIME=1 \
    -e WORKSPACE=/workspace \
    -e WORKCELL_WORKSPACE_IMPORT_ROOT=/opt/workcell/workspace-control-plane \
    -v "${docker_workspace}:/workspace" \
    ${WORKSPACE_IMPORT_ARGS[@]+"${WORKSPACE_IMPORT_ARGS[@]}"} \
    --entrypoint "$1" \
    "${IMAGE_TAG}" "${@:2}"
}

run_container_with_mutability() {
  local agent="$1"
  local mutability="$2"
  local docker_workspace=""
  shift 2

  docker_workspace="$(workcell_docker_host_path "${SMOKE_WORKSPACE}")"
  populate_workspace_import_mounts
  populate_runtime_security_args "${mutability}"

  docker_cmd run --rm \
    ${RUNTIME_SECURITY_ARGS[@]+"${RUNTIME_SECURITY_ARGS[@]}"} \
    --user 0:0 \
    --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
    --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
    --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
    -e AGENT_NAME="${agent}" \
    -e AGENT_UI=cli \
    -e WORKCELL_CONTAINER_MUTABILITY="${mutability}" \
    -e WORKCELL_HOST_UID="${HOST_UID}" \
    -e WORKCELL_HOST_GID="${HOST_GID}" \
    -e WORKCELL_HOST_USER="${HOST_USER}" \
    -e CODEX_PROFILE=strict \
    -e HOME=/state/agent-home \
    -e CODEX_HOME=/state/agent-home/.codex \
    -e TMPDIR=/state/tmp \
    -e WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC="${WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC-}" \
    -e WORKCELL_RUNTIME=1 \
    -e WORKSPACE=/workspace \
    -e WORKCELL_WORKSPACE_IMPORT_ROOT=/opt/workcell/workspace-control-plane \
    -v "${docker_workspace}:/workspace" \
    ${WORKSPACE_IMPORT_ARGS[@]+"${WORKSPACE_IMPORT_ARGS[@]}"} \
    --entrypoint "$1" \
    "${IMAGE_TAG}" "${@:2}"
}

run_entrypoint() {
  local agent="$1"
  local docker_workspace=""
  shift

  docker_workspace="$(workcell_docker_host_path "${SMOKE_WORKSPACE}")"
  populate_workspace_import_mounts
  populate_runtime_security_args ephemeral

  docker_cmd run --rm \
    ${RUNTIME_SECURITY_ARGS[@]+"${RUNTIME_SECURITY_ARGS[@]}"} \
    --user 0:0 \
    --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
    --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
    --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
    -e AGENT_NAME="${agent}" \
    -e AGENT_UI=cli \
    -e WORKCELL_CONTAINER_MUTABILITY=ephemeral \
    -e WORKCELL_HOST_UID="${HOST_UID}" \
    -e WORKCELL_HOST_GID="${HOST_GID}" \
    -e WORKCELL_HOST_USER="${HOST_USER}" \
    -e CODEX_PROFILE=strict \
    -e HOME=/state/agent-home \
    -e CODEX_HOME=/state/agent-home/.codex \
    -e TMPDIR=/state/tmp \
    -e WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC="${WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC-}" \
    -e WORKCELL_RUNTIME=1 \
    -e WORKSPACE=/workspace \
    -e WORKCELL_WORKSPACE_IMPORT_ROOT=/opt/workcell/workspace-control-plane \
    -v "${docker_workspace}:/workspace" \
    ${WORKSPACE_IMPORT_ARGS[@]+"${WORKSPACE_IMPORT_ARGS[@]}"} \
    "${IMAGE_TAG}" "$@"
}

run_entrypoint_with_profile() {
  local agent="$1"
  local profile="$2"
  local docker_workspace=""
  shift 2

  docker_workspace="$(workcell_docker_host_path "${SMOKE_WORKSPACE}")"
  populate_workspace_import_mounts
  populate_runtime_security_args ephemeral

  docker_cmd run --rm \
    ${RUNTIME_SECURITY_ARGS[@]+"${RUNTIME_SECURITY_ARGS[@]}"} \
    --user 0:0 \
    --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
    --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
    --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
    -e AGENT_NAME="${agent}" \
    -e AGENT_UI=cli \
    -e WORKCELL_CONTAINER_MUTABILITY=ephemeral \
    -e WORKCELL_HOST_UID="${HOST_UID}" \
    -e WORKCELL_HOST_GID="${HOST_GID}" \
    -e WORKCELL_HOST_USER="${HOST_USER}" \
    -e CODEX_PROFILE="${profile}" \
    -e WORKCELL_MODE="${profile}" \
    -e HOME=/state/agent-home \
    -e CODEX_HOME=/state/agent-home/.codex \
    -e TMPDIR=/state/tmp \
    -e WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC="${WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC-}" \
    -e WORKCELL_RUNTIME=1 \
    -e WORKSPACE=/workspace \
    -e WORKCELL_WORKSPACE_IMPORT_ROOT=/opt/workcell/workspace-control-plane \
    -v "${docker_workspace}:/workspace" \
    ${WORKSPACE_IMPORT_ARGS[@]+"${WORKSPACE_IMPORT_ARGS[@]}"} \
    "${IMAGE_TAG}" "$@"
}

run_entrypoint_with_init_profile() {
  local agent="$1"
  local profile="$2"
  local docker_workspace=""
  shift 2

  docker_workspace="$(workcell_docker_host_path "${SMOKE_WORKSPACE}")"
  populate_workspace_import_mounts
  populate_runtime_security_args ephemeral

  docker_cmd run --rm \
    --init \
    ${RUNTIME_SECURITY_ARGS[@]+"${RUNTIME_SECURITY_ARGS[@]}"} \
    --user 0:0 \
    --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
    --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
    --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
    -e AGENT_NAME="${agent}" \
    -e AGENT_UI=cli \
    -e WORKCELL_CONTAINER_MUTABILITY=ephemeral \
    -e WORKCELL_HOST_UID="${HOST_UID}" \
    -e WORKCELL_HOST_GID="${HOST_GID}" \
    -e WORKCELL_HOST_USER="${HOST_USER}" \
    -e CODEX_PROFILE="${profile}" \
    -e WORKCELL_MODE="${profile}" \
    -e HOME=/state/agent-home \
    -e CODEX_HOME=/state/agent-home/.codex \
    -e TMPDIR=/state/tmp \
    -e WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC="${WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC-}" \
    -e WORKCELL_RUNTIME=1 \
    -e WORKSPACE=/workspace \
    -e WORKCELL_WORKSPACE_IMPORT_ROOT=/opt/workcell/workspace-control-plane \
    -v "${docker_workspace}:/workspace" \
    ${WORKSPACE_IMPORT_ARGS[@]+"${WORKSPACE_IMPORT_ARGS[@]}"} \
    "${IMAGE_TAG}" "$@"
}

run_entrypoint_with_autonomy() {
  local agent="$1"
  local autonomy="$2"
  local docker_workspace=""
  shift 2

  docker_workspace="$(workcell_docker_host_path "${SMOKE_WORKSPACE}")"
  populate_workspace_import_mounts
  populate_runtime_security_args ephemeral

  docker_cmd run --rm \
    ${RUNTIME_SECURITY_ARGS[@]+"${RUNTIME_SECURITY_ARGS[@]}"} \
    --user 0:0 \
    --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
    --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
    --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
    -e AGENT_NAME="${agent}" \
    -e AGENT_UI=cli \
    -e WORKCELL_CONTAINER_MUTABILITY=ephemeral \
    -e WORKCELL_HOST_UID="${HOST_UID}" \
    -e WORKCELL_HOST_GID="${HOST_GID}" \
    -e WORKCELL_HOST_USER="${HOST_USER}" \
    -e CODEX_PROFILE=strict \
    -e WORKCELL_AGENT_AUTONOMY="${autonomy}" \
    -e HOME=/state/agent-home \
    -e CODEX_HOME=/state/agent-home/.codex \
    -e TMPDIR=/state/tmp \
    -e WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC="${WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC-}" \
    -e WORKCELL_RUNTIME=1 \
    -e WORKSPACE=/workspace \
    -e WORKCELL_WORKSPACE_IMPORT_ROOT=/opt/workcell/workspace-control-plane \
    -v "${docker_workspace}:/workspace" \
    ${WORKSPACE_IMPORT_ARGS[@]+"${WORKSPACE_IMPORT_ARGS[@]}"} \
    "${IMAGE_TAG}" "$@"
}

run_entrypoint_with_autonomy_and_bind() {
  local agent="$1"
  local autonomy="$2"
  local bind_source="$3"
  local bind_target="$4"
  local docker_workspace=""
  local docker_bind_source=""
  shift 4

  docker_workspace="$(workcell_docker_host_path "${SMOKE_WORKSPACE}")"
  docker_bind_source="$(workcell_docker_host_path "${bind_source}")"
  populate_workspace_import_mounts
  populate_runtime_security_args ephemeral

  docker_cmd run --rm \
    ${RUNTIME_SECURITY_ARGS[@]+"${RUNTIME_SECURITY_ARGS[@]}"} \
    --user 0:0 \
    --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
    --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
    --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
    -e AGENT_NAME="${agent}" \
    -e AGENT_UI=cli \
    -e WORKCELL_CONTAINER_MUTABILITY=ephemeral \
    -e WORKCELL_HOST_UID="${HOST_UID}" \
    -e WORKCELL_HOST_GID="${HOST_GID}" \
    -e WORKCELL_HOST_USER="${HOST_USER}" \
    -e CODEX_PROFILE=strict \
    -e WORKCELL_AGENT_AUTONOMY="${autonomy}" \
    -e HOME=/state/agent-home \
    -e CODEX_HOME=/state/agent-home/.codex \
    -e TMPDIR=/state/tmp \
    -e WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC="${WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC-}" \
    -e WORKCELL_RUNTIME=1 \
    -e WORKSPACE=/workspace \
    -e WORKCELL_WORKSPACE_IMPORT_ROOT=/opt/workcell/workspace-control-plane \
    -v "${docker_workspace}:/workspace" \
    ${WORKSPACE_IMPORT_ARGS[@]+"${WORKSPACE_IMPORT_ARGS[@]}"} \
    -v "${docker_bind_source}:${bind_target}:ro" \
    "${IMAGE_TAG}" "$@"
}

run_entrypoint_with_injection_bundle() {
  local agent="$1"
  local bundle_root="$2"
  shift 2
  local -a credential_mounts=()
  local docker_workspace=""
  local docker_bundle_root=""
  local host_source=""
  local mount_path=""

  while IFS=$'\t' read -r host_source mount_path; do
    [[ -n "${host_source}" ]] || continue
    credential_mounts+=(-v "$(workcell_docker_host_path "${host_source}"):${mount_path}:ro")
  done < <(direct_mount_specs_for_bundle "${bundle_root}")

  docker_workspace="$(workcell_docker_host_path "${SMOKE_WORKSPACE}")"
  docker_bundle_root="$(workcell_docker_host_path "${bundle_root}")"
  populate_workspace_import_mounts
  populate_runtime_security_args ephemeral

  docker_cmd run --rm \
    ${RUNTIME_SECURITY_ARGS[@]+"${RUNTIME_SECURITY_ARGS[@]}"} \
    --user 0:0 \
    --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
    --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
    --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
    -e AGENT_NAME="${agent}" \
    -e AGENT_UI=cli \
    -e WORKCELL_CONTAINER_MUTABILITY=ephemeral \
    -e WORKCELL_HOST_UID="${HOST_UID}" \
    -e WORKCELL_HOST_GID="${HOST_GID}" \
    -e WORKCELL_HOST_USER="${HOST_USER}" \
    -e CODEX_PROFILE=strict \
    -e HOME=/state/agent-home \
    -e CODEX_HOME=/state/agent-home/.codex \
    -e TMPDIR=/state/tmp \
    -e WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC="${WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC-}" \
    -e WORKCELL_RUNTIME=1 \
    -e WORKSPACE=/workspace \
    -e WORKCELL_INJECTION_MANIFEST=/opt/workcell/host-injections/manifest.json \
    -e WORKCELL_WORKSPACE_IMPORT_ROOT=/opt/workcell/workspace-control-plane \
    -v "${docker_workspace}:/workspace" \
    ${credential_mounts[@]+"${credential_mounts[@]}"} \
    ${WORKSPACE_IMPORT_ARGS[@]+"${WORKSPACE_IMPORT_ARGS[@]}"} \
    -v "${docker_bundle_root}:/opt/workcell/host-injections:ro" \
    "${IMAGE_TAG}" "$@"
}

run_container_with_injection_bundle() {
  local agent="$1"
  local bundle_root="$2"
  shift 2
  local -a credential_mounts=()
  local docker_workspace=""
  local docker_bundle_root=""
  local host_source=""
  local mount_path=""

  while IFS=$'\t' read -r host_source mount_path; do
    [[ -n "${host_source}" ]] || continue
    credential_mounts+=(-v "$(workcell_docker_host_path "${host_source}"):${mount_path}:ro")
  done < <(direct_mount_specs_for_bundle "${bundle_root}")

  docker_workspace="$(workcell_docker_host_path "${SMOKE_WORKSPACE}")"
  docker_bundle_root="$(workcell_docker_host_path "${bundle_root}")"
  populate_workspace_import_mounts
  populate_runtime_security_args ephemeral

  docker_cmd run --rm \
    ${RUNTIME_SECURITY_ARGS[@]+"${RUNTIME_SECURITY_ARGS[@]}"} \
    --user 0:0 \
    --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
    --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
    --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
    -e AGENT_NAME="${agent}" \
    -e AGENT_UI=cli \
    -e WORKCELL_CONTAINER_MUTABILITY=ephemeral \
    -e WORKCELL_HOST_UID="${HOST_UID}" \
    -e WORKCELL_HOST_GID="${HOST_GID}" \
    -e WORKCELL_HOST_USER="${HOST_USER}" \
    -e CODEX_PROFILE=strict \
    -e HOME=/state/agent-home \
    -e CODEX_HOME=/state/agent-home/.codex \
    -e TMPDIR=/state/tmp \
    -e WORKCELL_RUNTIME=1 \
    -e WORKSPACE=/workspace \
    -e WORKCELL_INJECTION_MANIFEST=/opt/workcell/host-injections/manifest.json \
    -e WORKCELL_WORKSPACE_IMPORT_ROOT=/opt/workcell/workspace-control-plane \
    -v "${docker_workspace}:/workspace" \
    ${credential_mounts[@]+"${credential_mounts[@]}"} \
    ${WORKSPACE_IMPORT_ARGS[@]+"${WORKSPACE_IMPORT_ARGS[@]}"} \
    -v "${docker_bundle_root}:/opt/workcell/host-injections:ro" \
    --entrypoint "$1" \
    "${IMAGE_TAG}" "${@:2}"
}

run_container_with_injection_bundle_stdin() {
  local agent="$1"
  local bundle_root="$2"
  shift 2
  local -a credential_mounts=()
  local docker_workspace=""
  local docker_bundle_root=""
  local host_source=""
  local mount_path=""

  while IFS=$'\t' read -r host_source mount_path; do
    [[ -n "${host_source}" ]] || continue
    credential_mounts+=(-v "$(workcell_docker_host_path "${host_source}"):${mount_path}:ro")
  done < <(direct_mount_specs_for_bundle "${bundle_root}")

  docker_workspace="$(workcell_docker_host_path "${SMOKE_WORKSPACE}")"
  docker_bundle_root="$(workcell_docker_host_path "${bundle_root}")"
  populate_workspace_import_mounts
  populate_runtime_security_args ephemeral

  docker_cmd run --rm -i \
    ${RUNTIME_SECURITY_ARGS[@]+"${RUNTIME_SECURITY_ARGS[@]}"} \
    --user 0:0 \
    --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
    --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
    --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
    -e AGENT_NAME="${agent}" \
    -e AGENT_UI=cli \
    -e WORKCELL_CONTAINER_MUTABILITY=ephemeral \
    -e WORKCELL_HOST_UID="${HOST_UID}" \
    -e WORKCELL_HOST_GID="${HOST_GID}" \
    -e WORKCELL_HOST_USER="${HOST_USER}" \
    -e CODEX_PROFILE=strict \
    -e HOME=/state/agent-home \
    -e CODEX_HOME=/state/agent-home/.codex \
    -e TMPDIR=/state/tmp \
    -e WORKCELL_RUNTIME=1 \
    -e WORKSPACE=/workspace \
    -e WORKCELL_INJECTION_MANIFEST=/opt/workcell/host-injections/manifest.json \
    -e WORKCELL_WORKSPACE_IMPORT_ROOT=/opt/workcell/workspace-control-plane \
    -v "${docker_workspace}:/workspace" \
    ${credential_mounts[@]+"${credential_mounts[@]}"} \
    ${WORKSPACE_IMPORT_ARGS[@]+"${WORKSPACE_IMPORT_ARGS[@]}"} \
    -v "${docker_bundle_root}:/opt/workcell/host-injections:ro" \
    --entrypoint "$1" \
    "${IMAGE_TAG}" "${@:2}"
}

if [[ "${1:-}" == "--self-docker-probe" ]]; then
  require_tool docker
  setup_workcell_trusted_docker_client
  if [[ -n "${DOCKER_CONTEXT_NAME:-}" ]]; then
    select_docker_context
  fi
  buildx_cmd version >/dev/null
  echo "container-smoke-docker-probe-ok"
  exit 0
fi

if [[ "${1:-}" == "--self-test-host-path-hardening" ]]; then
  run_self_test_host_path_hardening
  exit 0
fi

require_tool docker
trap cleanup EXIT
cleanup_workspace_scratch "${ROOT_DIR}"
prepare_smoke_workspace
setup_workcell_trusted_docker_client
select_docker_context

cat <<'EOF' >"${SMOKE_WORKSPACE}/AGENTS.md"
# Workspace AGENTS Instructions
EOF
cat <<'EOF' >"${SMOKE_WORKSPACE}/CLAUDE.md"
# Workspace Claude Instructions
EOF
cat <<'EOF' >"${SMOKE_WORKSPACE}/GEMINI.md"
# Workspace Gemini Instructions
EOF
mkdir -p "${SMOKE_WORKSPACE}/nested"
cat <<'EOF' >"${SMOKE_WORKSPACE}/nested/AGENTS.md"
# Nested Workspace AGENTS Instructions
EOF
cat <<'EOF' >"${SMOKE_WORKSPACE}/nested/CLAUDE.md"
# Nested Workspace Claude Instructions
EOF
cat <<'EOF' >"${SMOKE_WORKSPACE}/nested/GEMINI.md"
# Nested Workspace Gemini Instructions
EOF
align_path_for_mapped_runtime_user "${SMOKE_WORKSPACE}/AGENTS.md" 0644 0755
align_path_for_mapped_runtime_user "${SMOKE_WORKSPACE}/CLAUDE.md" 0644 0755
align_path_for_mapped_runtime_user "${SMOKE_WORKSPACE}/GEMINI.md" 0644 0755
align_path_for_mapped_runtime_user "${SMOKE_WORKSPACE}/nested/AGENTS.md" 0644 0755
align_path_for_mapped_runtime_user "${SMOKE_WORKSPACE}/nested/CLAUDE.md" 0644 0755
align_path_for_mapped_runtime_user "${SMOKE_WORKSPACE}/nested/GEMINI.md" 0644 0755

WORKSPACE_IMPORT_ROOT="$(mktemp -d "${ROOT_DIR}/tmp/workcell-import-fixtures.XXXXXX")"
cat <<'EOF' >"${WORKSPACE_IMPORT_ROOT}/AGENTS.md"
<!-- Workcell imported workspace AGENTS.md -->

# Workspace AGENTS Instructions
EOF
cat <<'EOF' >"${WORKSPACE_IMPORT_ROOT}/CLAUDE.md"
<!-- Workcell imported workspace CLAUDE.md -->

# Workspace Claude Instructions
EOF
cat <<'EOF' >"${WORKSPACE_IMPORT_ROOT}/GEMINI.md"
<!-- Workcell imported workspace GEMINI.md -->

# Workspace Gemini Instructions
EOF
align_path_for_mapped_runtime_user "${WORKSPACE_IMPORT_ROOT}" 0644 0755

BUILD_SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}"
SOURCE_DATE_EPOCH="${BUILD_SOURCE_DATE_EPOCH}" buildx_cmd build \
  --build-arg "SOURCE_DATE_EPOCH=${BUILD_SOURCE_DATE_EPOCH}" \
  --provenance=false \
  --sbom=false \
  --load \
  -t "${IMAGE_TAG}" \
  -f "${ROOT_DIR}/runtime/container/Dockerfile" \
  "${ROOT_DIR}" >/dev/null

mkdir -p "${ROOT_DIR}/tmp"
align_path_for_mapped_runtime_user "${ROOT_DIR}/tmp" 0644 0755
INJECTION_FIXTURE_ROOT="$(run_as_mapped_host_user mktemp -d "${ROOT_DIR}/tmp/workcell-injection-fixtures.XXXXXX")"
INJECTION_BUNDLE_ROOT="$(run_as_mapped_host_user mktemp -d "${ROOT_DIR}/tmp/workcell-injection-bundle.XXXXXX")"
mkdir -p "${INJECTION_FIXTURE_ROOT}"
align_path_for_mapped_runtime_user "${INJECTION_BUNDLE_ROOT}" 0644 0755
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/common.md"
# Common Smoke Instructions
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/codex.md"
# Codex Smoke Instructions
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/claude.md"
# Claude Smoke Instructions
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gemini.md"
# Gemini Smoke Instructions
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/public.txt"
injected-public
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/secret.txt"
injected-secret
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/codex-auth.json"
{"test": "auth"}
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/claude-auth.json"
{"token": "claude-auth"}
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/claude-mcp.json"
{"mcpServers": {"stub": {"command": "echo", "args": ["ok"]}}}
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gh-hosts.yml"
github.com:
  oauth_token: smoke-token
  user: workcell
  git_protocol: ssh
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/claude-api-key.txt"
claude-smoke-key
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gemini.env"
GEMINI_API_KEY=smoke-gemini-key
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gemini-invalid-bool.env"
GOOGLE_GENAI_USE_GCA=maybe
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gemini-conflicting.env"
GOOGLE_GENAI_USE_GCA=true
GOOGLE_GENAI_USE_VERTEXAI=true
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gemini-partial-vertex.env"
GOOGLE_CLOUD_PROJECT=smoke-project
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gemini-google-api-key-only.env"
GOOGLE_API_KEY=smoke-google-key
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gemini-malformed.env"
GOOGLE_GENAI_USE_VERTEXAI true
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gemini-vertex.env"
GOOGLE_GENAI_USE_VERTEXAI=true
GOOGLE_CLOUD_PROJECT=smoke-project
GOOGLE_CLOUD_LOCATION=us-central1
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gemini-vertex-express.env"
GOOGLE_GENAI_USE_VERTEXAI=true
GOOGLE_API_KEY=smoke-google-key
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gcloud-adc.json"
{"type":"authorized_user","client_id":"smoke-client","client_secret":"smoke-secret","refresh_token":"smoke-refresh"}
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gcloud-adc-invalid.json"
{}
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gemini-oauth.json"
{"token":"smoke-gemini-oauth"}
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gemini-invalid-oauth.json"
[]
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gemini-projects.json"
{"projects":{"smoke":{"path":"/workspace"}}}
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/gemini-projects-invalid.json"
{"projects":[]}
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/ssh-config"
Host smoke
  HostName smoke.example
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/known_hosts"
smoke.example ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAISmokeKey
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/id_smoke"
-----BEGIN OPENSSH PRIVATE KEY-----
smoke
-----END OPENSSH PRIVATE KEY-----
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/policy.toml"
version = 1

[documents]
common = "common.md"
codex = "codex.md"
claude = "claude.md"
gemini = "gemini.md"

[credentials]
codex_auth = "codex-auth.json"
claude_auth = "claude-auth.json"
claude_api_key = "claude-api-key.txt"
claude_mcp = "claude-mcp.json"
gemini_env = "gemini.env"
gemini_projects = "gemini-projects.json"

[credentials.github_hosts]
source = "gh-hosts.yml"
providers = ["codex", "claude", "gemini"]

[ssh]
enabled = true
config = "ssh-config"
known_hosts = "known_hosts"
identities = ["id_smoke"]
providers = ["codex"]

[[copies]]
source = "public.txt"
target = "/state/injected/public.txt"
classification = "public"
providers = ["codex"]

[[copies]]
source = "secret.txt"
target = "~/.config/workcell/token.txt"
classification = "secret"
providers = ["codex"]
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/policy-gemini-invalid-bool.toml"
version = 1

[credentials]
gemini_env = "gemini-invalid-bool.env"
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/policy-gemini-conflicting.toml"
version = 1

[credentials]
gemini_env = "gemini-conflicting.env"
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/policy-gemini-partial-vertex.toml"
version = 1

[credentials]
gemini_env = "gemini-partial-vertex.env"
gcloud_adc = "gcloud-adc.json"
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/policy-gemini-google-api-key-only-oauth.toml"
version = 1

[credentials]
gemini_env = "gemini-google-api-key-only.env"
gemini_oauth = "gemini-oauth.json"
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/policy-gemini-project-only-oauth.toml"
version = 1

[credentials]
gemini_env = "gemini-partial-vertex.env"
gemini_oauth = "gemini-oauth.json"
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/policy-gemini-malformed.toml"
version = 1

[credentials]
gemini_env = "gemini-malformed.env"
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/policy-gemini-gcloud-adc.toml"
version = 1

[credentials]
gemini_env = "gemini-vertex.env"
gcloud_adc = "gcloud-adc.json"
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/policy-gemini-vertex-express.toml"
version = 1

[credentials]
gemini_env = "gemini-vertex-express.env"
EOF
cat <<'EOF' >"${INJECTION_FIXTURE_ROOT}/policy-gemini-env-plus-oauth.toml"
version = 1

[credentials]
gemini_env = "gemini-vertex-express.env"
gemini_oauth = "gemini-oauth.json"
EOF

align_path_for_mapped_runtime_user "${INJECTION_FIXTURE_ROOT}" 0644 0755
chmod 0600 \
  "${INJECTION_FIXTURE_ROOT}/secret.txt" \
  "${INJECTION_FIXTURE_ROOT}/codex-auth.json" \
  "${INJECTION_FIXTURE_ROOT}/claude-auth.json" \
  "${INJECTION_FIXTURE_ROOT}/claude-mcp.json" \
  "${INJECTION_FIXTURE_ROOT}/gh-hosts.yml" \
  "${INJECTION_FIXTURE_ROOT}/claude-api-key.txt" \
  "${INJECTION_FIXTURE_ROOT}/gemini.env" \
  "${INJECTION_FIXTURE_ROOT}/gemini-invalid-bool.env" \
  "${INJECTION_FIXTURE_ROOT}/gemini-conflicting.env" \
  "${INJECTION_FIXTURE_ROOT}/gemini-partial-vertex.env" \
  "${INJECTION_FIXTURE_ROOT}/gemini-google-api-key-only.env" \
  "${INJECTION_FIXTURE_ROOT}/gemini-malformed.env" \
  "${INJECTION_FIXTURE_ROOT}/gemini-vertex.env" \
  "${INJECTION_FIXTURE_ROOT}/gemini-vertex-express.env" \
  "${INJECTION_FIXTURE_ROOT}/gcloud-adc.json" \
  "${INJECTION_FIXTURE_ROOT}/gcloud-adc-invalid.json" \
  "${INJECTION_FIXTURE_ROOT}/gemini-oauth.json" \
  "${INJECTION_FIXTURE_ROOT}/gemini-invalid-oauth.json" \
  "${INJECTION_FIXTURE_ROOT}/gemini-projects.json" \
  "${INJECTION_FIXTURE_ROOT}/gemini-projects-invalid.json" \
  "${INJECTION_FIXTURE_ROOT}/ssh-config" \
  "${INJECTION_FIXTURE_ROOT}/id_smoke"

run_as_mapped_host_user "${ROOT_DIR}/scripts/lib/render_injection_bundle" \
  --policy "${INJECTION_FIXTURE_ROOT}/policy.toml" \
  --agent codex \
  --mode strict \
  --output-root "${INJECTION_BUNDLE_ROOT}/codex" >/dev/null
prepare_direct_mount_spec_for_bundle "${INJECTION_BUNDLE_ROOT}/codex"

run_as_mapped_host_user "${ROOT_DIR}/scripts/lib/render_injection_bundle" \
  --policy "${INJECTION_FIXTURE_ROOT}/policy.toml" \
  --agent claude \
  --mode strict \
  --output-root "${INJECTION_BUNDLE_ROOT}/claude" >/dev/null
prepare_direct_mount_spec_for_bundle "${INJECTION_BUNDLE_ROOT}/claude"

run_as_mapped_host_user "${ROOT_DIR}/scripts/lib/render_injection_bundle" \
  --policy "${INJECTION_FIXTURE_ROOT}/policy.toml" \
  --agent gemini \
  --mode strict \
  --output-root "${INJECTION_BUNDLE_ROOT}/gemini" >/dev/null
prepare_direct_mount_spec_for_bundle "${INJECTION_BUNDLE_ROOT}/gemini"

run_as_mapped_host_user "${ROOT_DIR}/scripts/lib/render_injection_bundle" \
  --policy "${INJECTION_FIXTURE_ROOT}/policy-gemini-gcloud-adc.toml" \
  --agent gemini \
  --mode strict \
  --output-root "${INJECTION_BUNDLE_ROOT}/gemini-gcloud-adc" >/dev/null
prepare_direct_mount_spec_for_bundle "${INJECTION_BUNDLE_ROOT}/gemini-gcloud-adc"

run_as_mapped_host_user "${ROOT_DIR}/scripts/lib/render_injection_bundle" \
  --policy "${INJECTION_FIXTURE_ROOT}/policy-gemini-vertex-express.toml" \
  --agent gemini \
  --mode strict \
  --output-root "${INJECTION_BUNDLE_ROOT}/gemini-vertex-express" >/dev/null
prepare_direct_mount_spec_for_bundle "${INJECTION_BUNDLE_ROOT}/gemini-vertex-express"

run_as_mapped_host_user "${ROOT_DIR}/scripts/lib/render_injection_bundle" \
  --policy "${INJECTION_FIXTURE_ROOT}/policy-gemini-env-plus-oauth.toml" \
  --agent gemini \
  --mode strict \
  --output-root "${INJECTION_BUNDLE_ROOT}/gemini-env-plus-oauth" >/dev/null
prepare_direct_mount_spec_for_bundle "${INJECTION_BUNDLE_ROOT}/gemini-env-plus-oauth"

clone_bundle_with_credential_override \
  "${INJECTION_BUNDLE_ROOT}/gemini-vertex-express" \
  "${INJECTION_BUNDLE_ROOT}/gemini-invalid-bool" \
  gemini_env \
  "${INJECTION_FIXTURE_ROOT}/gemini-invalid-bool.env"
clone_bundle_with_credential_override \
  "${INJECTION_BUNDLE_ROOT}/gemini-vertex-express" \
  "${INJECTION_BUNDLE_ROOT}/gemini-conflicting" \
  gemini_env \
  "${INJECTION_FIXTURE_ROOT}/gemini-conflicting.env"
clone_bundle_with_credential_override \
  "${INJECTION_BUNDLE_ROOT}/gemini-gcloud-adc" \
  "${INJECTION_BUNDLE_ROOT}/gemini-partial-vertex" \
  gemini_env \
  "${INJECTION_FIXTURE_ROOT}/gemini-partial-vertex.env"
clone_bundle_with_credential_override \
  "${INJECTION_BUNDLE_ROOT}/gemini-env-plus-oauth" \
  "${INJECTION_BUNDLE_ROOT}/gemini-google-api-key-only-oauth" \
  gemini_env \
  "${INJECTION_FIXTURE_ROOT}/gemini-google-api-key-only.env"
clone_bundle_with_credential_override \
  "${INJECTION_BUNDLE_ROOT}/gemini-env-plus-oauth" \
  "${INJECTION_BUNDLE_ROOT}/gemini-project-only-oauth" \
  gemini_env \
  "${INJECTION_FIXTURE_ROOT}/gemini-partial-vertex.env"
clone_bundle_with_credential_override \
  "${INJECTION_BUNDLE_ROOT}/gemini-vertex-express" \
  "${INJECTION_BUNDLE_ROOT}/gemini-malformed" \
  gemini_env \
  "${INJECTION_FIXTURE_ROOT}/gemini-malformed.env"
clone_bundle_with_credential_override \
  "${INJECTION_BUNDLE_ROOT}/gemini-env-plus-oauth" \
  "${INJECTION_BUNDLE_ROOT}/gemini-invalid-oauth" \
  gemini_oauth \
  "${INJECTION_FIXTURE_ROOT}/gemini-invalid-oauth.json"
clone_bundle_with_credential_override \
  "${INJECTION_BUNDLE_ROOT}/gemini-gcloud-adc" \
  "${INJECTION_BUNDLE_ROOT}/gemini-invalid-adc" \
  gcloud_adc \
  "${INJECTION_FIXTURE_ROOT}/gcloud-adc-invalid.json"
clone_bundle_with_credential_override \
  "${INJECTION_BUNDLE_ROOT}/gemini" \
  "${INJECTION_BUNDLE_ROOT}/gemini-invalid-projects" \
  gemini_projects \
  "${INJECTION_FIXTURE_ROOT}/gemini-projects-invalid.json"

run_entrypoint codex codex --version >/dev/null
run_entrypoint_with_profile codex build codex --version >/dev/null

run_container_with_injection_bundle_stdin codex "${INJECTION_BUNDLE_ROOT}/codex" bash -s <<'SCRIPT'
set -euo pipefail
CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
/usr/local/bin/workcell-entrypoint codex --version >/dev/null
setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -s <<'INNER'
set -euo pipefail
CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
grep -q "Common Smoke Instructions" "$CODEX_HOME/AGENTS.md"
grep -q "Codex Smoke Instructions" "$CODEX_HOME/AGENTS.md"
grep -q "\"test\": \"auth\"" "$CODEX_HOME/auth.json"
grep -q "github.com:" "$HOME/.config/gh/hosts.yml"
grep -q "injected-public" /state/injected/public.txt
test "$(stat -c "%a" /state/injected/public.txt)" = "644"
grep -q "injected-secret" "$HOME/.config/workcell/token.txt"
test "$(stat -c "%a" "$HOME/.config/workcell/token.txt")" = "600"
grep -q "smoke.example" "$HOME/.ssh/config"
test "$(stat -c "%a" "$HOME/.ssh")" = "700"
test "$(stat -c "%a" "$HOME/.ssh/id_smoke")" = "600"
test -L "$CODEX_HOME/rules"
if bash -c 'printf "\n# session-marker\n" >>"$1"' bash "$CODEX_HOME/rules/default.rules" \
  2>/tmp/workcell-codex-rules-write.err; then
  echo "expected default Codex execpolicy rules to stay immutable on the managed path" >&2
  exit 1
fi
grep -Eq "Permission denied|Read-only file system" /tmp/workcell-codex-rules-write.err
if sudo -n id >/tmp/codex-sudo-id.out 2>&1; then
  echo "expected unrestricted sudo to stay blocked for the runtime user" >&2
  exit 1
fi
if /usr/local/libexec/workcell/real/sudo -n id >/tmp/codex-real-sudo-id.out 2>&1; then
  echo "expected direct access to the relocated real sudo binary to stay blocked" >&2
  exit 1
fi
grep -Eq "no new privileges|Permission denied" /tmp/codex-real-sudo-id.out
if sudo -n --preserve-env=PATH /usr/local/libexec/workcell/apt-helper.sh apt-get --help \
  >/tmp/codex-sudo-preserve-path.out 2>/tmp/codex-sudo-preserve-path.err; then
  echo "expected sudo preserve-env to stay constrained on the apt broker path" >&2
  exit 1
fi
grep -q "blocked unsupported preserved environment variable: PATH" /tmp/codex-sudo-preserve-path.err
slow_apt_helper=/tmp/workcell-slow-apt-helper.sh
slow_apt_broker_root=/tmp/workcell-apt-broker
cat >"${slow_apt_helper}" <<'INNER'
#!/usr/bin/env bash
set -euo pipefail
sleep 11
printf 'slow-apt-helper-ok\n'
INNER
chmod +x "${slow_apt_helper}"
rm -rf "${slow_apt_broker_root}"
WORKCELL_APT_BROKER_ROOT="${slow_apt_broker_root}" \
  WORKCELL_APT_HELPER="${slow_apt_helper}" \
  WORKCELL_APT_BROKER_SLEEP_SECONDS=0.05 \
  /bin/bash /usr/local/libexec/workcell/apt-broker.sh >/dev/null 2>&1 &
slow_apt_broker_pid=$!
trap 'kill "${slow_apt_broker_pid}" >/dev/null 2>&1 || true; wait "${slow_apt_broker_pid}" >/dev/null 2>&1 || true' EXIT
for _ in $(seq 1 100); do
  [[ -f "${slow_apt_broker_root}/pid" ]] && break
  sleep 0.1
done
if [[ ! -f "${slow_apt_broker_root}/pid" ]]; then
  echo "expected slow apt broker fixture to publish its pid file" >&2
  exit 1
fi
if ! WORKCELL_APT_BROKER_ROOT="${slow_apt_broker_root}" \
  WORKCELL_APT_BROKER_WAIT_INTERVAL_SECONDS=0.05 \
  sudo -n /usr/local/libexec/workcell/apt-helper.sh apt-get update \
  >/tmp/codex-sudo-slow-apt.out 2>/tmp/codex-sudo-slow-apt.err; then
  echo "expected sudo-wrapper to wait for a slow apt broker request by default" >&2
  cat /tmp/codex-sudo-slow-apt.out >&2 || true
  cat /tmp/codex-sudo-slow-apt.err >&2 || true
  exit 1
fi
grep -q "slow-apt-helper-ok" /tmp/codex-sudo-slow-apt.out
if grep -q "Workcell apt broker timed out." /tmp/codex-sudo-slow-apt.err; then
  echo "expected default apt broker waits to avoid timing out slow requests" >&2
  cat /tmp/codex-sudo-slow-apt.err >&2
  exit 1
fi
trap - EXIT
kill "${slow_apt_broker_pid}" >/dev/null 2>&1 || true
wait "${slow_apt_broker_pid}" >/dev/null 2>&1 || true
apt-get --help >/dev/null
codex --version >/dev/null
mkdir -p /workspace/exfil
rm -rf "$HOME/.config/workcell"
ln -s /workspace/exfil "$HOME/.config/workcell"
if codex --version >/tmp/codex-injected-copy-symlink.out 2>&1; then
  echo "expected nested Codex launch to reject symlinked injected-copy parents" >&2
  exit 1
fi
grep -q "symlinked session path component" /tmp/codex-injected-copy-symlink.out
test ! -e /workspace/exfil/token.txt
INNER
SCRIPT

run_container_with_injection_bundle_stdin claude "${INJECTION_BUNDLE_ROOT}/claude" bash -s <<'SCRIPT'
set -euo pipefail
/usr/local/bin/workcell-entrypoint claude --version >/dev/null
setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -s <<'INNER'
set -euo pipefail
test -f /usr/local/libexec/workcell/control-plane-manifest.json
jq -e '.runtime_artifacts[] | select(.runtime_path == "/usr/local/libexec/workcell/home-control-plane.sh")' \
  /usr/local/libexec/workcell/control-plane-manifest.json >/dev/null
grep -q "Common Smoke Instructions" "$HOME/.claude/CLAUDE.md"
grep -q "Claude Smoke Instructions" "$HOME/.claude/CLAUDE.md"
grep -q "Workspace AGENTS Instructions" "$HOME/.claude/CLAUDE.md"
grep -q "Workspace Claude Instructions" "$HOME/.claude/CLAUDE.md"
grep -q "\"apiKeyHelper\"" "$HOME/.claude/settings.json"
helper_path="$(jq -r '.apiKeyHelper' "$HOME/.claude/settings.json")"
test ! -L "$HOME/.claude/settings.json"
test -x "$helper_path"
grep -q "/opt/workcell/host-inputs/credentials/claude-api-key.txt" "$helper_path"
test ! -e "$HOME/.claude/workcell/claude-api-key"
test "$("$helper_path")" = "claude-smoke-key"
grep -q "\"token\": \"claude-auth\"" "$HOME/.claude/.claude.json"
test ! -L "$HOME/.claude/.claude.json"
grep -q "\"token\": \"claude-auth\"" "$HOME/.claude.json"
test ! -L "$HOME/.claude.json"
grep -q "\"token\": \"claude-auth\"" "$HOME/.claude/.credentials.json"
test ! -L "$HOME/.claude/.credentials.json"
grep -q "\"token\": \"claude-auth\"" "$HOME/.config/claude-code/auth.json"
test ! -L "$HOME/.config/claude-code/auth.json"
test ! -L "$HOME/.mcp.json"
grep -q "\"stub\"" "$HOME/.mcp.json"
grep -q "github.com:" "$HOME/.config/gh/hosts.yml"
rm -f "$HOME/.claude/settings.json" "$HOME/.claude/.claude.json" "$HOME/.claude.json" "$HOME/.claude/.credentials.json" "$HOME/.mcp.json"
claude --version >/dev/null
grep -q "\"apiKeyHelper\"" "$HOME/.claude/settings.json"
helper_path="$(jq -r '.apiKeyHelper' "$HOME/.claude/settings.json")"
test -x "$helper_path"
grep -q "/opt/workcell/host-inputs/credentials/claude-api-key.txt" "$helper_path"
test ! -e "$HOME/.claude/workcell/claude-api-key"
test "$("$helper_path")" = "claude-smoke-key"
grep -q "\"token\": \"claude-auth\"" "$HOME/.claude/.claude.json"
grep -q "\"token\": \"claude-auth\"" "$HOME/.claude.json"
grep -q "\"token\": \"claude-auth\"" "$HOME/.claude/.credentials.json"
grep -q "\"stub\"" "$HOME/.mcp.json"
INNER
SCRIPT

run_container_with_injection_bundle_stdin gemini "${INJECTION_BUNDLE_ROOT}/gemini" bash -s <<'SCRIPT'
  set -euo pipefail
  /usr/local/bin/workcell-entrypoint gemini --version >/dev/null
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    grep -q "Common Smoke Instructions" "$HOME/.gemini/GEMINI.md"
    grep -q "Gemini Smoke Instructions" "$HOME/.gemini/GEMINI.md"
    grep -q "Workspace AGENTS Instructions" "$HOME/.gemini/GEMINI.md"
    grep -q "Workspace Gemini Instructions" "$HOME/.gemini/GEMINI.md"
    grep -q "GEMINI_API_KEY=smoke-gemini-key" "$HOME/.gemini/.env"
    jq -r ".security.auth.selectedType" "$HOME/.gemini/settings.json" | grep -q "^gemini-api-key$"
    jq -r ".security.folderTrust.enabled" "$HOME/.gemini/settings.json" | grep -q "^false$"
    jq -r ".tools.sandbox" "$HOME/.gemini/settings.json" | grep -q "^false$"
    jq -e --arg workspace "/workspace" ". == {(\$workspace): \"TRUST_FOLDER\"}" "$HOME/.gemini/trustedFolders.json" >/dev/null
    grep -q "\"smoke\"" "$HOME/.gemini/projects.json"
    grep -q "github.com:" "$HOME/.config/gh/hosts.yml"
    mkdir -p /workspace/exfil
    rm -f "$HOME/.gemini/settings.json.tmp" "$HOME/.gemini/trustedFolders.json.tmp"
    ln -s /workspace/exfil/settings-clobber "$HOME/.gemini/settings.json.tmp"
    ln -s /workspace/exfil/trusted-clobber "$HOME/.gemini/trustedFolders.json.tmp"
    gemini --version >/dev/null 2>&1
    test ! -e /workspace/exfil/settings-clobber
    test ! -e /workspace/exfil/trusted-clobber
    script_supports_command_flag() {
      local script_help=""
      script_help="$(script --help 2>&1 || true)"
      grep -q -- " -c, --command " <<<"${script_help}"
    }
    run_typescript_probe_with_timeout() {
      local timeout_seconds="${1:?missing timeout}"
      local transcript_path="${2:?missing transcript path}"
      shift 2
      local -a command_args=("$@")
      local command_string=""
      if script_supports_command_flag; then
        printf -v command_string "%q " "${command_args[@]}"
        timeout "${timeout_seconds}" script -qef -c "${command_string% }" "${transcript_path}" </dev/null >/dev/null 2>&1
        return
      fi
      timeout "${timeout_seconds}" script -qeF "${transcript_path}" "${command_args[@]}" </dev/null >/dev/null 2>&1
    }
    interactive_status=0
    if run_typescript_probe_with_timeout 20 /tmp/workcell-gemini-interactive.typescript gemini; then
      interactive_status=0
    else
      interactive_status=$?
    fi
    if [[ "${interactive_status}" != "0" ]] && [[ "${interactive_status}" != "124" ]]; then
      echo "Gemini interactive startup probe failed; transcript follows:" >&2
      tail -n 80 /tmp/workcell-gemini-interactive.typescript >&2 || true
      exit 1
    fi
    grep -q "Gemini CLI v" /tmp/workcell-gemini-interactive.typescript
    if grep -q "Do you trust the files in this folder\\?" /tmp/workcell-gemini-interactive.typescript; then
      echo "expected seeded Gemini trustedFolders.json to suppress the trust prompt" >&2
      exit 1
    fi
    if grep -q "Gemini CLI is restarting to apply the trust changes" /tmp/workcell-gemini-interactive.typescript; then
      echo "expected Gemini startup not to restart when the workspace is already trusted" >&2
      exit 1
    fi
    if grep -q "Failed to relaunch the CLI process" /tmp/workcell-gemini-interactive.typescript; then
      echo "expected Gemini startup not to hit the relaunch failure path" >&2
      exit 1
    fi
  '
SCRIPT

run_container_with_injection_bundle gemini "${INJECTION_BUNDLE_ROOT}/gemini-invalid-bool" bash -lc '
  set -euo pipefail
  if /usr/local/bin/workcell-entrypoint gemini --version >/tmp/gemini-invalid-bool.out 2>&1; then
    echo "expected Gemini invalid boolean env config to fail fast" >&2
    exit 1
  fi
  grep -q "Invalid boolean in Gemini auth env file" /tmp/gemini-invalid-bool.out
'

run_container_with_injection_bundle gemini "${INJECTION_BUNDLE_ROOT}/gemini-conflicting" bash -lc '
  set -euo pipefail
  if /usr/local/bin/workcell-entrypoint gemini --version >/tmp/gemini-conflicting.out 2>&1; then
    echo "expected Gemini conflicting auth selectors to fail fast" >&2
    exit 1
  fi
  grep -q "enables both GOOGLE_GENAI_USE_GCA and GOOGLE_GENAI_USE_VERTEXAI" /tmp/gemini-conflicting.out
'

run_container_with_injection_bundle gemini "${INJECTION_BUNDLE_ROOT}/gemini-partial-vertex" bash -lc '
  set -euo pipefail
  if /usr/local/bin/workcell-entrypoint gemini --version >/tmp/gemini-partial-vertex.out 2>&1; then
    echo "expected Gemini partial Vertex config to fail fast" >&2
    exit 1
  fi
  grep -q "does not configure a supported Gemini auth mode" /tmp/gemini-partial-vertex.out
'

run_container_with_injection_bundle gemini "${INJECTION_BUNDLE_ROOT}/gemini-google-api-key-only-oauth" bash -lc '
  set -euo pipefail
  if /usr/local/bin/workcell-entrypoint gemini --version >/tmp/gemini-google-api-key-only-oauth.out 2>&1; then
    echo "expected Gemini GOOGLE_API_KEY config without explicit Vertex selection to fail fast" >&2
    exit 1
  fi
  grep -q "sets GOOGLE_API_KEY without GOOGLE_GENAI_USE_VERTEXAI=true" /tmp/gemini-google-api-key-only-oauth.out
'

run_container_with_injection_bundle gemini "${INJECTION_BUNDLE_ROOT}/gemini-project-only-oauth" bash -lc '
  set -euo pipefail
  if /usr/local/bin/workcell-entrypoint gemini --version >/tmp/gemini-project-only-oauth.out 2>&1; then
    echo "expected project-only Gemini env config to remain invalid even when gemini_oauth is present" >&2
    exit 1
  fi
  grep -q "does not configure a supported Gemini auth mode" /tmp/gemini-project-only-oauth.out
'

run_container_with_injection_bundle gemini "${INJECTION_BUNDLE_ROOT}/gemini-malformed" bash -lc '
  set -euo pipefail
  if /usr/local/bin/workcell-entrypoint gemini --version >/tmp/gemini-malformed.out 2>&1; then
    echo "expected malformed Gemini env config to fail fast" >&2
    exit 1
  fi
  grep -q "Malformed Gemini auth env file" /tmp/gemini-malformed.out
'

run_container_with_injection_bundle_stdin gemini "${INJECTION_BUNDLE_ROOT}/gemini-gcloud-adc" bash -s <<'SCRIPT'
  set -euo pipefail
  /usr/local/bin/workcell-entrypoint gemini --version >/dev/null
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    jq -r ".security.auth.selectedType" "$HOME/.gemini/settings.json" | grep -q "^vertex-ai$"
    grep -q "GOOGLE_CLOUD_PROJECT=smoke-project" "$HOME/.gemini/.env"
    grep -q "\"authorized_user\"" "$HOME/.config/gcloud/application_default_credentials.json"
  '
SCRIPT

run_container_with_injection_bundle gemini "${INJECTION_BUNDLE_ROOT}/gemini-invalid-oauth" bash -lc '
  set -euo pipefail
  if /usr/local/bin/workcell-entrypoint gemini --version >/tmp/gemini-invalid-oauth.out 2>&1; then
    echo "expected malformed Gemini OAuth JSON to fail fast" >&2
    exit 1
  fi
  grep -q "Gemini OAuth config must contain a JSON object" /tmp/gemini-invalid-oauth.out
'

run_container_with_injection_bundle gemini "${INJECTION_BUNDLE_ROOT}/gemini-invalid-adc" bash -lc '
  set -euo pipefail
  if /usr/local/bin/workcell-entrypoint gemini --version >/tmp/gemini-invalid-adc.out 2>&1; then
    echo "expected malformed Google ADC JSON to fail fast" >&2
    exit 1
  fi
  grep -q "Google ADC config must contain a JSON object with a non-empty string type" /tmp/gemini-invalid-adc.out
'

run_container_with_injection_bundle gemini "${INJECTION_BUNDLE_ROOT}/gemini-invalid-projects" bash -lc '
  set -euo pipefail
  if /usr/local/bin/workcell-entrypoint gemini --version >/tmp/gemini-invalid-projects.out 2>&1; then
    echo "expected malformed Gemini projects JSON to fail fast" >&2
    exit 1
  fi
  grep -q "Gemini projects config must contain a JSON object with an object-valued projects field" /tmp/gemini-invalid-projects.out
'

run_container_with_injection_bundle_stdin gemini "${INJECTION_BUNDLE_ROOT}/gemini-vertex-express" bash -s <<'SCRIPT'
  set -euo pipefail
  /usr/local/bin/workcell-entrypoint gemini --version >/dev/null
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    jq -r ".security.auth.selectedType" "$HOME/.gemini/settings.json" | grep -q "^vertex-ai$"
    grep -q "GOOGLE_GENAI_USE_VERTEXAI=true" "$HOME/.gemini/.env"
    grep -q "GOOGLE_API_KEY=smoke-google-key" "$HOME/.gemini/.env"
  '
SCRIPT

WORKSPACE_IMPORT_ROOT_FALLBACK="$(mktemp -d "${ROOT_DIR}/tmp/workcell-import-fallback.XXXXXX")"
cat <<'EOF' >"${WORKSPACE_IMPORT_ROOT_FALLBACK}/AGENTS.md"
<!-- Workcell imported workspace AGENTS.md -->

# Workspace AGENTS Instructions
EOF
align_path_for_mapped_runtime_user "${WORKSPACE_IMPORT_ROOT_FALLBACK}" 0644 0755
ORIGINAL_WORKSPACE_IMPORT_ROOT="${WORKSPACE_IMPORT_ROOT}"
WORKSPACE_IMPORT_ROOT="${WORKSPACE_IMPORT_ROOT_FALLBACK}"

run_container_stdin claude bash -s <<'SCRIPT'
  set -euo pipefail
  /usr/local/bin/workcell-entrypoint claude --version >/dev/null
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    grep -q "Workspace AGENTS Instructions" "$HOME/.claude/CLAUDE.md"
  '
SCRIPT

run_container_stdin gemini bash -s <<'SCRIPT'
  set -euo pipefail
  /usr/local/bin/workcell-entrypoint gemini --version >/dev/null
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    grep -q "Workspace AGENTS Instructions" "$HOME/.gemini/GEMINI.md"
  '
SCRIPT

WORKSPACE_IMPORT_ROOT="${ORIGINAL_WORKSPACE_IMPORT_ROOT}"

if [[ "$(uname -s)" == "Linux" ]] && [[ -x /bin/echo ]]; then
  CODEX_YOLO_ARGS="$(
    run_entrypoint_with_autonomy_and_bind \
      codex \
      yolo \
      /bin/echo \
      /usr/local/libexec/workcell/real/codex \
      codex --version
  )"
  if [[ "${CODEX_YOLO_ARGS}" != "--profile strict --ask-for-approval never --version" ]]; then
    echo "unexpected Codex yolo argv: ${CODEX_YOLO_ARGS}" >&2
    exit 1
  fi

  cat <<'EOF' >"${ROOT_DIR}/tmp/workcell-codex-env-check.sh"
#!/bin/sh
printf '{"claude_config":"%s","aws":"%s","gh":"%s","ssh":"%s"}\n' \
  "${CLAUDE_CONFIG_DIR-}" \
  "${AWS_SECRET_ACCESS_KEY-}" \
  "${GITHUB_TOKEN-}" \
  "${SSH_AUTH_SOCK-}"
EOF
  align_path_for_mapped_runtime_user "${ROOT_DIR}/tmp/workcell-codex-env-check.sh" 0755 0755

  CODEX_SECRET_ENV="$(
    AWS_SECRET_ACCESS_KEY='verify-aws-secret' \
      GITHUB_TOKEN='verify-gh-token' \
      SSH_AUTH_SOCK='/tmp/workcell-secret-sock' \
      run_entrypoint_with_autonomy_and_bind \
      codex \
      yolo \
      "${ROOT_DIR}/tmp/workcell-codex-env-check.sh" \
      /usr/local/libexec/workcell/real/codex \
      codex --version
  )"
  if [[ "${CODEX_SECRET_ENV}" != '{"claude_config":"","aws":"","gh":"","ssh":""}' ]]; then
    echo "unexpected Codex provider environment exposure: ${CODEX_SECRET_ENV}" >&2
    exit 1
  fi

  CODEX_PROMPT_ARGS="$(
    run_entrypoint_with_autonomy_and_bind \
      codex \
      prompt \
      /bin/echo \
      /usr/local/libexec/workcell/real/codex \
      codex --version
  )"
  if [[ "${CODEX_PROMPT_ARGS}" != "--profile strict --ask-for-approval on-request --version" ]]; then
    echo "unexpected Codex prompt argv: ${CODEX_PROMPT_ARGS}" >&2
    exit 1
  fi

  cat <<'EOF' >"${ROOT_DIR}/tmp/workcell-claude-runtime-check.sh"
#!/bin/sh
printf '{"config":"%s","autoupdater":"%s","aws":"%s","gh":"%s","ssh":"%s","args":"%s"}\n' \
  "${CLAUDE_CONFIG_DIR-}" \
  "${DISABLE_AUTOUPDATER-}" \
  "${AWS_SECRET_ACCESS_KEY-}" \
  "${GITHUB_TOKEN-}" \
  "${SSH_AUTH_SOCK-}" \
  "$*"
EOF
  align_path_for_mapped_runtime_user "${ROOT_DIR}/tmp/workcell-claude-runtime-check.sh" 0755 0755

  CLAUDE_YOLO_RUNTIME="$(
    AWS_SECRET_ACCESS_KEY='verify-aws-secret' \
      GITHUB_TOKEN='verify-gh-token' \
      SSH_AUTH_SOCK='/tmp/workcell-secret-sock' \
      run_entrypoint_with_autonomy_and_bind \
      claude \
      yolo \
      "${ROOT_DIR}/tmp/workcell-claude-runtime-check.sh" \
      /usr/local/libexec/workcell/real/claude \
      claude --version
  )"
  if [[ "${CLAUDE_YOLO_RUNTIME}" != '{"config":"/state/agent-home/.claude","autoupdater":"1","aws":"","gh":"","ssh":"","args":"--permission-mode bypassPermissions --version"}' ]]; then
    echo "unexpected Claude yolo env/argv: ${CLAUDE_YOLO_RUNTIME}" >&2
    exit 1
  fi

  CLAUDE_AUTH_STATUS_ARGS="$(
    run_entrypoint_with_autonomy_and_bind \
      claude \
      yolo \
      /bin/echo \
      /usr/local/libexec/workcell/real/claude \
      claude auth status --text
  )"
  if [[ "${CLAUDE_AUTH_STATUS_ARGS}" != "--permission-mode bypassPermissions auth status --text" ]]; then
    echo "unexpected Claude auth-status argv: ${CLAUDE_AUTH_STATUS_ARGS}" >&2
    exit 1
  fi

  CLAUDE_PROMPT_ARGS="$(
    run_entrypoint_with_autonomy_and_bind \
      claude \
      prompt \
      /bin/echo \
      /usr/local/libexec/workcell/real/claude \
      claude --version
  )"
  if [[ "${CLAUDE_PROMPT_ARGS}" != "--permission-mode default --version" ]]; then
    echo "unexpected Claude prompt argv: ${CLAUDE_PROMPT_ARGS}" >&2
    exit 1
  fi

  GEMINI_YOLO_ARGS="$(
    run_entrypoint_with_autonomy_and_bind \
      gemini \
      yolo \
      /bin/echo \
      /usr/local/libexec/workcell/real/node \
      gemini --version
  )"
  if [[ "${GEMINI_YOLO_ARGS}" != "/opt/workcell/providers/node_modules/@google/gemini-cli/bundle/gemini.js --approval-mode yolo --version" ]]; then
    echo "unexpected Gemini yolo argv: ${GEMINI_YOLO_ARGS}" >&2
    exit 1
  fi

  cat <<'EOF' >"${ROOT_DIR}/tmp/workcell-gemini-node-env.sh"
#!/bin/sh
printf '{"claude_config":"%s","relaunch":"%s","gemini_sandbox":"%s","sandbox":"%s","sandbox_flags":"%s","sandbox_set_uid_gid":"%s","seatbelt":"%s","sandbox_mounts":"%s","sandbox_env":"%s","aws":"%s","gh":"%s","ssh":"%s","args":"%s"}\n' \
  "${CLAUDE_CONFIG_DIR-}" \
  "${GEMINI_CLI_NO_RELAUNCH-}" \
  "${GEMINI_SANDBOX-}" \
  "${SANDBOX-}" \
  "${SANDBOX_FLAGS-}" \
  "${SANDBOX_SET_UID_GID-}" \
  "${SEATBELT_PROFILE-}" \
  "${SANDBOX_MOUNTS-}" \
  "${SANDBOX_ENV-}" \
  "${AWS_SECRET_ACCESS_KEY-}" \
  "${GITHUB_TOKEN-}" \
  "${SSH_AUTH_SOCK-}" \
  "$*"
EOF
  align_path_for_mapped_runtime_user "${ROOT_DIR}/tmp/workcell-gemini-node-env.sh" 0755 0755

  GEMINI_NO_RELAUNCH_ENV="$(
    GEMINI_SANDBOX='docker' \
      SANDBOX='host-sandbox' \
      SANDBOX_FLAGS='--privileged' \
      SANDBOX_SET_UID_GID='1' \
      SEATBELT_PROFILE='permissive-open' \
      SANDBOX_MOUNTS='/tmp:/sandbox:rw' \
      SANDBOX_ENV='FOO=bar' \
      AWS_SECRET_ACCESS_KEY='verify-aws-secret' \
      GITHUB_TOKEN='verify-gh-token' \
      SSH_AUTH_SOCK='/tmp/workcell-secret-sock' \
      run_entrypoint_with_autonomy_and_bind \
      gemini \
      yolo \
      "${ROOT_DIR}/tmp/workcell-gemini-node-env.sh" \
      /usr/local/libexec/workcell/real/node \
      gemini --version
  )"
  if [[ "${GEMINI_NO_RELAUNCH_ENV}" != '{"claude_config":"","relaunch":"1","gemini_sandbox":"false","sandbox":"","sandbox_flags":"","sandbox_set_uid_gid":"","seatbelt":"","sandbox_mounts":"","sandbox_env":"","aws":"","gh":"","ssh":"","args":"/opt/workcell/providers/node_modules/@google/gemini-cli/bundle/gemini.js --approval-mode yolo --version"}' ]]; then
    echo "unexpected Gemini relaunch env/argv: ${GEMINI_NO_RELAUNCH_ENV}" >&2
    exit 1
  fi

  GEMINI_PROMPT_ARGS="$(
    run_entrypoint_with_autonomy_and_bind \
      gemini \
      prompt \
      /bin/echo \
      /usr/local/libexec/workcell/real/node \
      gemini --version
  )"
  if [[ "${GEMINI_PROMPT_ARGS}" != "/opt/workcell/providers/node_modules/@google/gemini-cli/bundle/gemini.js --approval-mode default --version" ]]; then
    echo "unexpected Gemini prompt argv: ${GEMINI_PROMPT_ARGS}" >&2
    exit 1
  fi

  GEMINI_PROMPT_ENV="$(
    GEMINI_SANDBOX='docker' \
      SANDBOX='host-sandbox' \
      SANDBOX_FLAGS='--privileged' \
      SANDBOX_SET_UID_GID='1' \
      SEATBELT_PROFILE='permissive-open' \
      SANDBOX_MOUNTS='/tmp:/sandbox:rw' \
      SANDBOX_ENV='FOO=bar' \
      run_entrypoint_with_autonomy_and_bind \
      gemini \
      prompt \
      "${ROOT_DIR}/tmp/workcell-gemini-node-env.sh" \
      /usr/local/libexec/workcell/real/node \
      gemini --version
  )"
  if [[ "${GEMINI_PROMPT_ENV}" != '{"claude_config":"","relaunch":"1","gemini_sandbox":"false","sandbox":"","sandbox_flags":"","sandbox_set_uid_gid":"","seatbelt":"","sandbox_mounts":"","sandbox_env":"","aws":"","gh":"","ssh":"","args":"/opt/workcell/providers/node_modules/@google/gemini-cli/bundle/gemini.js --approval-mode default --version"}' ]]; then
    echo "unexpected Gemini prompt env/argv: ${GEMINI_PROMPT_ENV}" >&2
    exit 1
  fi
fi

if [[ "$(docker_cmd run --rm --entrypoint /usr/bin/id "${IMAGE_TAG}" -u)" == "0" ]]; then
  echo "expected runtime image default user to remain unprivileged" >&2
  exit 1
fi

docker_cmd run --rm \
  -e AGENT_NAME=codex \
  -e AGENT_UI=cli \
  -e CODEX_PROFILE=strict \
  -e WORKCELL_MODE=strict \
  "${IMAGE_TAG}" codex --version >/dev/null

populate_runtime_security_args ephemeral
if docker_cmd run --rm \
  ${RUNTIME_SECURITY_ARGS[@]+"${RUNTIME_SECURITY_ARGS[@]}"} \
  --user 0:0 \
  --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
  --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
  --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
  -e AGENT_UI=cli \
  -e WORKCELL_CONTAINER_MUTABILITY=ephemeral \
  -e WORKCELL_HOST_UID="${HOST_UID}" \
  -e WORKCELL_HOST_GID="${HOST_GID}" \
  -e WORKCELL_HOST_USER="${HOST_USER}" \
  -e CODEX_PROFILE=strict \
  -e HOME=/state/agent-home \
  -e CODEX_HOME=/state/agent-home/.codex \
  -e TMPDIR=/state/tmp \
  -e WORKCELL_RUNTIME=1 \
  -e WORKSPACE=/workspace \
  -v "$(workcell_docker_host_path "${SMOKE_WORKSPACE}"):/workspace" \
  "${IMAGE_TAG}" codex --version >/tmp/workcell-entrypoint-missing-agent.out 2>&1; then
  echo "expected Workcell entrypoint to reject a missing AGENT_NAME instead of defaulting to codex" >&2
  exit 1
fi
grep -q "Workcell requires AGENT_NAME to be set explicitly" /tmp/workcell-entrypoint-missing-agent.out

if run_entrypoint codex bash -lc true >/tmp/workcell-entrypoint-command.out 2>&1; then
  echo "expected Workcell entrypoint to reject non-provider commands by default" >&2
  exit 1
fi
grep -q "Workcell blocked non-provider command" /tmp/workcell-entrypoint-command.out

for agent in codex claude gemini; do
  if ! run_entrypoint_with_profile "${agent}" development bash -lc "printf '%s\n' development-mode-ok" \
    >/tmp/workcell-entrypoint-development-${agent}.out 2>&1; then
    echo "expected Workcell entrypoint development mode to allow managed non-provider commands for ${agent}" >&2
    cat /tmp/workcell-entrypoint-development-${agent}.out >&2
    exit 1
  fi
  grep -q '^development-mode-ok$' /tmp/workcell-entrypoint-development-${agent}.out
done

if ! run_entrypoint_with_profile codex development bash -lc '
  set -euo pipefail
  git --version >/dev/null
  sed -n "1p" /workspace/README.md >/tmp/workcell-development-readme.out
  test -s /tmp/workcell-development-readme.out
  ls /workspace | grep -q "^README.md$"
  find /workspace -maxdepth 1 -name README.md | grep -q "^/workspace/README.md$"
' >/tmp/workcell-entrypoint-development-readonly.out 2>&1; then
  echo "expected development mode to support managed readonly workspace inspection commands" >&2
  cat /tmp/workcell-entrypoint-development-readonly.out >&2
  exit 1
fi

if run_entrypoint codex codex --search >/tmp/workcell-entrypoint-codex-search.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex web search outside breakglass" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-search.out

if run_entrypoint codex codex --dangerously-bypass-approvals-and-sandbox >/tmp/workcell-entrypoint-codex-danger.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex dangerous override outside breakglass" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-danger.out

if run_entrypoint codex codex -a never --version >/tmp/workcell-entrypoint-codex-approval.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex approval overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-approval.out

if run_entrypoint codex codex --full-auto --version >/tmp/workcell-entrypoint-codex-full-auto.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex full-auto overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-full-auto.out

if run_entrypoint codex codex app-server >/tmp/workcell-entrypoint-codex-app-server.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex GUI subcommands on the CLI surface" >&2
  exit 1
fi
grep -q "Workcell blocked unsupported Codex CLI subcommand" /tmp/workcell-entrypoint-codex-app-server.out

if run_entrypoint codex codex --profile breakglass --version >/tmp/workcell-entrypoint-codex-profile.out 2>&1; then
  echo "expected Workcell entrypoint to reject operator-supplied Codex profiles" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-profile.out

if run_entrypoint codex codex --cd /state --version >/tmp/workcell-entrypoint-codex-cd.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex working-directory overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-cd.out

if run_entrypoint codex codex --config profile=breakglass --version >/tmp/workcell-entrypoint-codex-config-profile.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex profile config overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex config override" /tmp/workcell-entrypoint-codex-config-profile.out

if run_entrypoint codex codex --config sandbox_workspace_write.network_access=true --version >/tmp/workcell-entrypoint-codex-config-network.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex network_access config overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex config override" /tmp/workcell-entrypoint-codex-config-network.out

if run_entrypoint codex codex --config sandbox_workspace_write.writable_roots=/state --version >/tmp/workcell-entrypoint-codex-config-writable-roots.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex writable_roots config overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex config override" /tmp/workcell-entrypoint-codex-config-writable-roots.out

if run_entrypoint codex codex --config shell_environment_policy.set.BAD=1 --version >/tmp/workcell-entrypoint-codex-config-shell.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex shell environment overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex config override" /tmp/workcell-entrypoint-codex-config-shell.out

if run_entrypoint codex codex --add-dir=/tmp --version >/tmp/workcell-entrypoint-codex-add-dir.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex add-dir overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-add-dir.out

if run_entrypoint codex codex --enable danger-mode --version >/tmp/workcell-entrypoint-codex-enable.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex feature enables" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-enable.out

if run_entrypoint codex codex --disable safe-guards --version >/tmp/workcell-entrypoint-codex-disable.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex feature disables" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-disable.out

if run_entrypoint codex codex --remote=ssh://example.invalid/workcell --version >/tmp/workcell-entrypoint-codex-remote.out 2>&1; then
  echo "expected Workcell entrypoint to reject Codex remote overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-codex-remote.out

run_entrypoint claude claude --version >/dev/null
run_entrypoint_with_init_profile codex build codex --version >/dev/null
run_entrypoint gemini gemini --version >/dev/null

run_container_stdin codex bash -s <<'SCRIPT'
  set -euo pipefail
  CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
  /usr/local/bin/workcell-entrypoint codex --version >/dev/null
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
    grep -q "Workspace AGENTS Instructions" "$CODEX_HOME/AGENTS.md"
    if grep -q "Nested Workspace AGENTS Instructions" "$CODEX_HOME/AGENTS.md"; then
      echo "expected nested AGENTS.md instructions to remain path-scoped in the workspace" >&2
      exit 1
    fi
    grep -q "Nested Workspace AGENTS Instructions" /workspace/nested/AGENTS.md
  '
SCRIPT

run_container_stdin claude bash -s <<'SCRIPT'
  set -euo pipefail
  AGENT_NAME=claude /usr/local/bin/workcell-entrypoint claude --version >/dev/null
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    grep -q "Workspace AGENTS Instructions" "$HOME/.claude/CLAUDE.md"
    grep -q "Workspace Claude Instructions" "$HOME/.claude/CLAUDE.md"
    if grep -q "Nested Workspace Claude Instructions" "$HOME/.claude/CLAUDE.md"; then
      echo "expected nested CLAUDE.md instructions to remain path-scoped in the workspace" >&2
      exit 1
    fi
    if grep -q "Nested Workspace AGENTS Instructions" "$HOME/.claude/CLAUDE.md"; then
      echo "expected nested AGENTS.md instructions to remain path-scoped in the workspace" >&2
      exit 1
    fi
    grep -q "Nested Workspace Claude Instructions" /workspace/nested/CLAUDE.md
  '
SCRIPT

run_container_stdin gemini bash -s <<'SCRIPT'
  set -euo pipefail
  AGENT_NAME=gemini /usr/local/bin/workcell-entrypoint gemini --version >/dev/null
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    grep -q "Workspace AGENTS Instructions" "$HOME/.gemini/GEMINI.md"
    grep -q "Workspace Gemini Instructions" "$HOME/.gemini/GEMINI.md"
    if grep -q "Nested Workspace Gemini Instructions" "$HOME/.gemini/GEMINI.md"; then
      echo "expected nested GEMINI.md instructions to remain path-scoped in the workspace" >&2
      exit 1
    fi
    if grep -q "Nested Workspace AGENTS Instructions" "$HOME/.gemini/GEMINI.md"; then
      echo "expected nested AGENTS.md instructions to remain path-scoped in the workspace" >&2
      exit 1
    fi
    grep -q "Nested Workspace Gemini Instructions" /workspace/nested/GEMINI.md
  '
SCRIPT

run_container_stdin codex bash -s <<'SCRIPT'
  set -euo pipefail
  CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
  AGENT_NAME=codex WORKCELL_AGENT_AUTONOMY=yolo WORKCELL_CODEX_RULES_MUTABILITY=session /usr/local/bin/workcell-entrypoint codex --version >/dev/null
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
    test ! -L "$CODEX_HOME/rules"
    printf "\n# yolo-session-marker\n" >>"$CODEX_HOME/rules/default.rules"
    AGENT_NAME=codex WORKCELL_AGENT_AUTONOMY=yolo WORKCELL_CODEX_RULES_MUTABILITY=session /usr/local/bin/workcell-entrypoint codex --version >/dev/null
    grep -q "^# yolo-session-marker$" "$CODEX_HOME/rules/default.rules"
  '
SCRIPT

run_container_stdin codex bash -s <<'SCRIPT'
  set -euo pipefail
  CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
  AGENT_NAME=codex WORKCELL_AGENT_AUTONOMY=prompt /usr/local/bin/workcell-entrypoint codex --version >/dev/null
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
    test ! -L "$CODEX_HOME/rules"
    printf "\n# prompt-session-marker\n" >>"$CODEX_HOME/rules/default.rules"
    AGENT_NAME=codex WORKCELL_AGENT_AUTONOMY=prompt /usr/local/bin/workcell-entrypoint codex --version >/dev/null
    grep -q "^# prompt-session-marker$" "$CODEX_HOME/rules/default.rules"
  '
SCRIPT

run_container_stdin codex bash -s <<'SCRIPT'
  set -euo pipefail
  cat <<EOF >/tmp/workcell-mutable-runtime-check.sh
set -euo pipefail
test "\$(id -u)" = "${WORKCELL_HOST_UID}"
test "\$(id -g)" = "${WORKCELL_HOST_GID}"
if sudo -n true >/tmp/workcell-mutable-sudo.out 2>&1; then
  echo "expected mutable runtime user to keep unrestricted sudo blocked" >&2
  exit 1
fi
apt-get update >/dev/null
apt-get install -y --no-install-recommends make >/tmp/workcell-apt-install.out 2>/tmp/workcell-apt-install.err
grep -q "downgrades in-container control-plane assurance until this session exits" /tmp/workcell-apt-install.err
grep -q "this session is now lower-assurance until the container exits" /tmp/workcell-apt-install.err
command -v make >/dev/null
grep -q "^CapEff:[[:space:]]*0000000000000000$" /proc/self/status
codex --version >/tmp/workcell-post-apt-codex.out 2>&1
grep -q "session previously ran package-manager mutations as root" /tmp/workcell-post-apt-codex.out
grep -q "forced session-local Codex execpolicy rules" /tmp/workcell-post-apt-codex.out
test ! -L "$CODEX_HOME/rules"
test -w "$CODEX_HOME/rules/default.rules"
claude --version >/tmp/workcell-post-apt-claude.out 2>&1
grep -q "session previously ran package-manager mutations as root" /tmp/workcell-post-apt-claude.out
gemini --version >/tmp/workcell-post-apt-gemini.out 2>&1
grep -q "session previously ran package-manager mutations as root" /tmp/workcell-post-apt-gemini.out
EOF
  source /usr/local/libexec/workcell/runtime-user.sh
  if ! workcell_should_reexec_as_runtime_user; then
    echo "expected mutable runtime default to re-exec as the mapped host user" >&2
    exit 1
  fi
  workcell_reexec_as_runtime_user /tmp/workcell-mutable-runtime-check.sh
SCRIPT

if run_container codex bash -lc '
  set -euo pipefail
  cat <<EOF >/tmp/workcell-unsafe-apt-check.sh
set -euo pipefail
if apt-get -o APT::Update::Pre-Invoke::=/bin/true update >/tmp/workcell-unsafe-apt-hook.out 2>&1; then
  echo "expected apt-get hook override to stay blocked" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe apt-get argument: -o" /tmp/workcell-unsafe-apt-hook.out
touch /tmp/workcell-local.deb
if apt-get install -y /tmp/workcell-local.deb >/tmp/workcell-unsafe-apt-local.out 2>&1; then
  echo "expected local package installs to stay blocked" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe apt-get argument: /tmp/workcell-local.deb" /tmp/workcell-unsafe-apt-local.out
EOF
  source /usr/local/libexec/workcell/runtime-user.sh
  workcell_reexec_as_runtime_user /tmp/workcell-unsafe-apt-check.sh
' >/tmp/workcell-unsafe-apt-run.out 2>&1; then
  :
else
  cat /tmp/workcell-unsafe-apt-run.out >&2
  exit 1
fi

if run_container_with_mutability codex readonly bash -lc '
  set -euo pipefail
  source /usr/local/libexec/workcell/runtime-user.sh
  if workcell_should_reexec_as_runtime_user; then
    echo "expected readonly runtime not to auto-reexec as the mapped host user" >&2
    exit 1
  fi
  apt-get update
' >/tmp/workcell-readonly-mutability.out 2>&1; then
  echo "expected readonly mutability to block package-manager writes" >&2
  exit 1
fi
grep -q 'Workcell blocked apt-get: readonly container mutability is active.' /tmp/workcell-readonly-mutability.out
grep -q 'Relaunch with --container-mutability ephemeral to allow ephemeral package-manager writes.' /tmp/workcell-readonly-mutability.out

run_container codex bash -s <<'SCRIPT'
set -euo pipefail
source /usr/local/libexec/workcell/runtime-user.sh
printf "regexXuser::20000:0:99999:7:::\n" >>/etc/shadow
workcell_append_shadow_entry "regex.user"
test "$(grep -c "^regex\\.user:" /etc/shadow)" = "1"
SCRIPT

if run_entrypoint claude claude --dangerously-skip-permissions >/tmp/workcell-entrypoint-claude-danger.out 2>&1; then
  echo "expected Workcell entrypoint to reject Claude dangerous override outside breakglass" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Claude override" /tmp/workcell-entrypoint-claude-danger.out

if run_entrypoint claude claude --allowedTools Read >/tmp/workcell-entrypoint-claude-allowed-tools.out 2>&1; then
  echo "expected Workcell entrypoint to reject Claude allowedTools overrides outside breakglass" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Claude override" /tmp/workcell-entrypoint-claude-allowed-tools.out

if run_entrypoint claude claude --add-dir=/state --version >/tmp/workcell-entrypoint-claude-add-dir.out 2>&1; then
  echo "expected Workcell entrypoint to reject Claude add-dir overrides outside breakglass" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Claude override" /tmp/workcell-entrypoint-claude-add-dir.out

if run_entrypoint claude claude --permission-mode default --version >/tmp/workcell-entrypoint-claude-permission-mode.out 2>&1; then
  echo "expected Workcell entrypoint to reject Claude autonomy overrides outside host policy" >&2
  exit 1
fi
grep -q "Workcell blocked Claude autonomy override" /tmp/workcell-entrypoint-claude-permission-mode.out

if run_entrypoint claude claude update >/tmp/workcell-entrypoint-claude-update.out 2>&1; then
  echo "expected Workcell entrypoint to reject Claude update outside the pinned-image model" >&2
  exit 1
fi
grep -q "Workcell blocked Claude lifecycle command: update" /tmp/workcell-entrypoint-claude-update.out

if run_entrypoint claude claude install >/tmp/workcell-entrypoint-claude-install.out 2>&1; then
  echo "expected Workcell entrypoint to reject Claude install outside the pinned-image model" >&2
  exit 1
fi
grep -q "Workcell blocked Claude lifecycle command: install" /tmp/workcell-entrypoint-claude-install.out

if run_entrypoint gemini gemini --approval-mode default --version >/tmp/workcell-entrypoint-gemini-approval-mode.out 2>&1; then
  echo "expected Workcell entrypoint to reject Gemini autonomy overrides outside host policy" >&2
  exit 1
fi
grep -q "Workcell blocked Gemini autonomy override" /tmp/workcell-entrypoint-gemini-approval-mode.out

if run_entrypoint gemini gemini --bypassPermissions --version >/tmp/workcell-entrypoint-gemini-bypass-camel.out 2>&1; then
  echo "expected Workcell entrypoint to reject Gemini bypassPermissions-style overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Gemini override" /tmp/workcell-entrypoint-gemini-bypass-camel.out

if run_entrypoint gemini gemini --bypass-permissions --version >/tmp/workcell-entrypoint-gemini-bypass-kebab.out 2>&1; then
  echo "expected Workcell entrypoint to reject Gemini bypass-permissions-style overrides" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Gemini override" /tmp/workcell-entrypoint-gemini-bypass-kebab.out

if run_entrypoint gemini gemini -y --version >/tmp/workcell-entrypoint-gemini-yolo-short.out 2>&1; then
  echo "expected Workcell entrypoint to reject Gemini short yolo overrides outside host policy" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Gemini override" /tmp/workcell-entrypoint-gemini-yolo-short.out

if run_container codex bash -lc 'AGENT_NAME=claude WORKCELL_MODE=breakglass CODEX_PROFILE=breakglass /usr/local/bin/workcell-entrypoint claude --dangerously-skip-permissions' >/tmp/workcell-entrypoint-direct-claude-breakglass.out 2>&1; then
  echo "expected direct entrypoint Claude breakglass override to fail" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Claude override" /tmp/workcell-entrypoint-direct-claude-breakglass.out

if run_container codex bash -lc 'AGENT_NAME=codex WORKCELL_MODE=breakglass CODEX_PROFILE=breakglass /usr/local/bin/workcell-entrypoint codex --profile breakglass --version' >/tmp/workcell-entrypoint-direct-codex-breakglass.out 2>&1; then
  echo "expected direct entrypoint Codex breakglass profile override to fail" >&2
  exit 1
fi
grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-direct-codex-breakglass.out

populate_runtime_security_args ephemeral
if docker_cmd run --rm \
  --init \
  ${RUNTIME_SECURITY_ARGS[@]+"${RUNTIME_SECURITY_ARGS[@]}"} \
  --user 0:0 \
  --tmpfs "/tmp:nosuid,nodev,noexec,size=1g,mode=1777" \
  --tmpfs "/run:nosuid,nodev,size=64m,mode=755" \
  --tmpfs "/state:exec,nosuid,nodev,size=1g,mode=1777" \
  -e AGENT_NAME=codex \
  -e AGENT_UI=cli \
  -e WORKCELL_CONTAINER_MUTABILITY=ephemeral \
  -e WORKCELL_HOST_UID="${HOST_UID}" \
  -e WORKCELL_HOST_GID="${HOST_GID}" \
  -e WORKCELL_HOST_USER="${HOST_USER}" \
  -e CODEX_PROFILE=build \
  -e WORKCELL_MODE=build \
  -e HOME=/state/agent-home \
  -e CODEX_HOME=/state/agent-home/.codex \
  -e TMPDIR=/state/tmp \
  -e WORKCELL_RUNTIME=1 \
  -e WORKSPACE=/workspace \
  -v "$(workcell_docker_host_path "${SMOKE_WORKSPACE}"):/workspace" \
  --entrypoint bash \
  "${IMAGE_TAG}" -lc '
    codex --version | grep -q "codex-cli"
    if AGENT_NAME=codex WORKCELL_MODE=breakglass CODEX_PROFILE=breakglass /usr/local/bin/workcell-entrypoint codex --profile breakglass --version >/tmp/workcell-entrypoint-init-nested.out 2>&1; then
      echo "expected nested init-path entrypoint replay to fail" >&2
      exit 1
    fi
    grep -q "Workcell blocked unsafe Codex override" /tmp/workcell-entrypoint-init-nested.out
    if AGENT_NAME=codex AGENT_UI=gui WORKCELL_MODE=build CODEX_PROFILE=build /usr/local/bin/workcell-entrypoint >/tmp/workcell-entrypoint-init-gui-replay.out 2>&1; then
      echo "expected nested init-path GUI replay to fail" >&2
      exit 1
    fi
    grep -q "Workcell blocked non-PID1 breakglass request" /tmp/workcell-entrypoint-init-gui-replay.out
  ' >/tmp/workcell-entrypoint-init-nested-run.out 2>&1; then
  :
else
  cat /tmp/workcell-entrypoint-init-nested-run.out >&2
  exit 1
fi

run_container_stdin codex bash -s <<'SCRIPT'
  set -euo pipefail
  test -f /usr/local/libexec/workcell/control-plane-manifest.json
  jq -e ".schema_version == 2" /usr/local/libexec/workcell/control-plane-manifest.json >/dev/null
  printf "\n# tampered during smoke\n" >>/opt/workcell/adapters/codex/.codex/config.toml
  if /usr/local/bin/workcell-entrypoint codex --version >/tmp/workcell-control-plane-tamper.out 2>&1; then
    echo "expected tampered Codex adapter baseline to fail control-plane verification" >&2
    exit 1
  fi
  grep -q "Workcell control-plane manifest mismatch for /opt/workcell/adapters/codex/.codex/config.toml" /tmp/workcell-control-plane-tamper.out
SCRIPT

run_container claude bash -lc '
  set -euo pipefail
  if (printf "\n# tampered during smoke\n" >>/etc/claude-code/managed-settings.json) \
      >/tmp/workcell-control-plane-claude-write.out 2>&1; then
    if /usr/local/bin/workcell-entrypoint claude --version >/tmp/workcell-control-plane-claude-tamper.out 2>&1; then
      echo "expected tampered Claude managed settings to fail control-plane verification" >&2
      exit 1
    fi
    grep -q "Workcell control-plane manifest mismatch for /etc/claude-code/managed-settings.json" \
      /tmp/workcell-control-plane-claude-tamper.out
  else
    grep -Eq "Permission denied|Read-only file system" /tmp/workcell-control-plane-claude-write.out
  fi
'

run_container_stdin claude bash -s <<'SCRIPT'
  set -euo pipefail
  rm -f /etc/claude-code/managed-settings.json
  ln -s /opt/workcell/adapters/claude/managed-settings.json /etc/claude-code/managed-settings.json
  if /usr/local/bin/workcell-entrypoint claude --version >/tmp/workcell-control-plane-claude-symlink.out 2>&1; then
    echo "expected symlinked Claude managed settings to fail control-plane verification" >&2
    exit 1
  fi
  grep -q "Workcell control-plane artifact must not be a symlink: /etc/claude-code/managed-settings.json" \
    /tmp/workcell-control-plane-claude-symlink.out
SCRIPT

run_container codex bash -lc '
  set -euo pipefail
  mkdir -p /tmp/workcell-control-plane-codex-parent
  cp /opt/workcell/adapters/codex/.codex/config.toml /tmp/workcell-control-plane-codex-parent/config.toml
  rm -rf /opt/workcell/adapters/codex/.codex
  ln -s /tmp/workcell-control-plane-codex-parent /opt/workcell/adapters/codex/.codex
  if /usr/local/bin/workcell-entrypoint codex --version >/tmp/workcell-control-plane-codex-parent-symlink.out 2>&1; then
    echo "expected symlinked Codex control-plane parent to fail verification" >&2
    exit 1
  fi
  grep -q "Workcell control-plane artifact parent must not be a symlink: /opt/workcell/adapters/codex/.codex" \
    /tmp/workcell-control-plane-codex-parent-symlink.out
'

run_container gemini bash -lc '
  set -euo pipefail
  printf "\n# tampered during smoke\n" >>/opt/workcell/adapters/gemini/.gemini/settings.json
  if /usr/local/bin/workcell-entrypoint gemini --version >/tmp/workcell-control-plane-gemini-tamper.out 2>&1; then
    echo "expected tampered Gemini adapter baseline to fail control-plane verification" >&2
    exit 1
  fi
  grep -q "Workcell control-plane manifest mismatch for /opt/workcell/adapters/gemini/.gemini/settings.json" /tmp/workcell-control-plane-gemini-tamper.out
'

run_container codex bash -lc "$(
  cat <<'SCRIPT'
  set -euo pipefail
  /usr/local/bin/workcell-entrypoint codex --version >/dev/null
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
    test "$(id -u)" != 0
    test "$WORKCELL_RUNTIME" = "1"
    test "$TMPDIR" = "/state/tmp"
    mkdir -p "$TMPDIR"
    touch "$TMPDIR/workcell-tmpdir-ok"
    test -x /usr/bin/bwrap
    test ! -u /usr/bin/bwrap
    assert_codex_stderr_clean() {
      local stderr_path="$1"
      if grep -Eq "Codex could not find system bubblewrap|Failed to save model migration prompt preference|Failed to save model for profile|failed to persist config.toml" "$stderr_path"; then
        echo "expected Codex startup to avoid bubblewrap/config persistence warnings" >&2
        cat "$stderr_path" >&2
        exit 1
      fi
    }
    assert_codex_feature_value() {
      local expected_value="$1"
      if grep -Eq "^\[profiles\.strict\.features\]$" "$CODEX_HOME/config.toml"; then
        grep -q "^unified_exec = ${expected_value}$" "$CODEX_HOME/config.toml"
        return 0
      fi
      grep -Eq "^\[features\]$" "$CODEX_HOME/config.toml"
      grep -q "^unified_exec = ${expected_value}$" "$CODEX_HOME/config.toml"
    }
    EXEC_TMP="$TMPDIR/workcell-exec"
    mkdir -p "$EXEC_TMP"
    codex --version >/tmp/codex-version.out 2>/tmp/codex-version.err
    grep -q "codex-cli" /tmp/codex-version.out
    assert_codex_stderr_clean /tmp/codex-version.err
    LD_PRELOAD=/workspace/workcell-does-not-exist.so codex --version | grep -q "codex-cli"
    LD_PRELOAD=/workspace/workcell-does-not-exist.so claude --version >/dev/null
    LD_PRELOAD=/workspace/workcell-does-not-exist.so git --version | grep -q "git version"
    LD_PRELOAD=/workspace/workcell-does-not-exist.so node --version | grep -q "^v"
    test -f "$CODEX_HOME/config.toml"
    test ! -L "$CODEX_HOME/config.toml"
    test -w "$CODEX_HOME/config.toml"
    cmp "$CODEX_HOME/config.toml" /opt/workcell/adapters/codex/.codex/config.toml
    codex features list >/tmp/codex-features.out 2>/tmp/codex-features.err
    assert_codex_stderr_clean /tmp/codex-features.err
    grep -Eq "^unified_exec[[:space:]]+stable[[:space:]]+false$" /tmp/codex-features.out
    if command -v python3 >/tmp/python-which.out 2>&1; then
      echo "expected runtime image to omit python3 from the operator PATH" >&2
      exit 1
    fi
    command -v perl >/tmp/perl-which.out 2>&1
    grep -q "^/usr/bin/perl$" /tmp/perl-which.out
    if command -v perlbug >/tmp/perlbug-which.out 2>&1; then
      echo "expected runtime image to omit auxiliary Perl tooling from the operator PATH" >&2
      exit 1
    fi
    if command -v perldoc >/tmp/perldoc-which.out 2>&1; then
      echo "expected runtime image to omit auxiliary Perl tooling from the operator PATH" >&2
      exit 1
    fi
    if command -v perlthanks >/tmp/perlthanks-which.out 2>&1; then
      echo "expected runtime image to omit auxiliary Perl tooling from the operator PATH" >&2
      exit 1
    fi
    cp /bin/true /tmp/workcell-noexec
    chmod 0700 /tmp/workcell-noexec
    if /tmp/workcell-noexec >/tmp/workcell-noexec.out 2>&1; then
      echo "expected /tmp to be mounted noexec" >&2
      exit 1
    fi
    grep -Eq "Permission denied|Operation not permitted" /tmp/workcell-noexec.out
    if codex --search >/tmp/codex-nested-search.out 2>&1; then
      echo "expected nested Codex invocation to reject unsafe overrides" >&2
      exit 1
    fi
    grep -q "Workcell blocked unsafe Codex override" /tmp/codex-nested-search.out
    if codex --cd /state --version >/tmp/codex-nested-cd.out 2>&1; then
      echo "expected nested Codex invocation to reject working-directory overrides" >&2
      exit 1
    fi
    grep -q "Workcell blocked unsafe Codex override" /tmp/codex-nested-cd.out
    if codex --config sandbox_workspace_write.writable_roots=/state --version >/tmp/codex-nested-config-writable-roots.out 2>&1; then
      echo "expected nested Codex invocation to reject writable_roots overrides" >&2
      exit 1
    fi
    grep -q "Workcell blocked unsafe Codex config override" /tmp/codex-nested-config-writable-roots.out
    if WORKCELL_MODE=breakglass CODEX_PROFILE=breakglass codex --search >/tmp/codex-nested-breakglass.out 2>&1; then
      echo "expected nested Codex invocation to ignore caller-supplied breakglass env" >&2
      exit 1
    fi
    grep -q "Workcell blocked unsafe Codex override" /tmp/codex-nested-breakglass.out
    if codex -a never --version >/tmp/codex-nested-approval.out 2>&1; then
      echo "expected nested Codex invocation to reject approval overrides" >&2
      exit 1
    fi
    grep -q "Workcell blocked unsafe Codex override" /tmp/codex-nested-approval.out
    if codex --full-auto --version >/tmp/codex-nested-full-auto.out 2>&1; then
      echo "expected nested Codex invocation to reject full-auto overrides" >&2
      exit 1
    fi
    grep -q "Workcell blocked unsafe Codex override" /tmp/codex-nested-full-auto.out
    if codex --enable danger-mode --version >/tmp/codex-nested-enable.out 2>&1; then
      echo "expected nested Codex invocation to reject feature enables" >&2
      exit 1
    fi
    grep -q "Workcell blocked unsafe Codex override" /tmp/codex-nested-enable.out
    if codex --disable safe-guards --version >/tmp/codex-nested-disable.out 2>&1; then
      echo "expected nested Codex invocation to reject feature disables" >&2
      exit 1
    fi
    grep -q "Workcell blocked unsafe Codex override" /tmp/codex-nested-disable.out
    if codex app-server >/tmp/codex-nested-app-server.out 2>&1; then
      echo "expected nested Codex invocation to reject GUI subcommands on the CLI surface" >&2
      exit 1
    fi
    grep -q "Workcell blocked unsupported Codex CLI subcommand" /tmp/codex-nested-app-server.out
    rm -f "$CODEX_HOME/config.toml"
    printf "web_search = \"enabled\"\n" >"$CODEX_HOME/config.toml"
    codex --version >/tmp/codex-version-after-tamper.out 2>/tmp/codex-version-after-tamper.err
    grep -q "codex-cli" /tmp/codex-version-after-tamper.out
    assert_codex_stderr_clean /tmp/codex-version-after-tamper.err
    test ! -L "$CODEX_HOME/config.toml"
    test -w "$CODEX_HOME/config.toml"
    cmp "$CODEX_HOME/config.toml" /opt/workcell/adapters/codex/.codex/config.toml
    codex features disable unified_exec >/tmp/codex-features-disable.out 2>/tmp/codex-features-disable.err
    assert_codex_stderr_clean /tmp/codex-features-disable.err
    test ! -L "$CODEX_HOME/config.toml"
    test -w "$CODEX_HOME/config.toml"
    assert_codex_feature_value false
    codex features enable unified_exec >/tmp/codex-features-enable.out 2>/tmp/codex-features-enable.err
    assert_codex_stderr_clean /tmp/codex-features-enable.err
    assert_codex_feature_value true
  '
  if /usr/local/libexec/workcell/real/codex --version >/tmp/codex-real-path.out 2>&1; then
    echo "expected direct real Codex payload execution to fail" >&2
    exit 1
  fi
  if /usr/local/libexec/workcell/real/claude --version >/tmp/claude-real-path.out 2>&1; then
    echo "expected direct real Claude payload execution to fail" >&2
    exit 1
  fi
  EXEC_TMP="/state/workcell-exec"
  mkdir -p "$EXEC_TMP"
  chmod 0777 "$EXEC_TMP"
  codex execpolicy check --rules /opt/workcell/adapters/codex/.codex/rules/default.rules rm -rf build \
    | jq -e ".decision == \"forbidden\"" >/dev/null
  codex execpolicy check --rules /opt/workcell/adapters/codex/.codex/rules/default.rules git push origin feature \
    | jq -e ".decision == \"prompt\"" >/dev/null
  codex execpolicy check --rules /opt/workcell/adapters/codex/.codex/rules/default.rules git push origin main --force \
    | jq -e ".decision == \"forbidden\"" >/dev/null
  codex execpolicy check --rules /opt/workcell/adapters/codex/.codex/rules/default.rules git commit --no-verify \
    | jq -e ".decision == \"forbidden\"" >/dev/null
  codex execpolicy check --rules /opt/workcell/adapters/codex/.codex/rules/default.rules /usr/bin/git push --no-verify origin feature \
    | jq -e ".decision == \"forbidden\"" >/dev/null
  codex execpolicy check --rules /opt/workcell/adapters/codex/.codex/rules/default.rules /usr/local/libexec/workcell/core/claude --dangerously-skip-permissions \
    | jq -e ".decision == \"forbidden\"" >/dev/null
  codex execpolicy check --rules /opt/workcell/adapters/codex/.codex/rules/default.rules /usr/local/libexec/workcell/real/claude --dangerously-skip-permissions \
    | jq -e ".decision == \"forbidden\"" >/dev/null
  grep -q "Do not bypass git hooks with --no-verify or git commit -n from Workcell." \
    /opt/workcell/adapters/codex/.codex/rules/default.rules
  grep -q "git commit --no-verify -m test" \
    /opt/workcell/adapters/codex/.codex/rules/default.rules
  grep -q "git commit -n -m test" \
    /opt/workcell/adapters/codex/.codex/rules/default.rules
  cat <<'EOF' >/tmp/workcell-bashenv.sh
touch /tmp/workcell-bashenv-ran
EOF
  rm -f /tmp/workcell-bashenv-ran
  BASH_ENV=/tmp/workcell-bashenv.sh node --version >/tmp/node-bashenv.out 2>&1
  test ! -e /tmp/workcell-bashenv-ran
  cat <<'EOF' >/tmp/workcell-wrapper-bashenv.sh
exec env -u LD_PRELOAD /usr/local/libexec/workcell/real/codex --version
EOF
  if BASH_ENV=/tmp/workcell-wrapper-bashenv.sh bash /usr/local/libexec/workcell/provider-wrapper.sh >/tmp/provider-wrapper-bashenv.out 2>&1; then
    echo "expected explicit bash launch of provider wrapper with hostile BASH_ENV to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/provider-wrapper-bashenv.out
  cat <<'EOF' >/tmp/workcell-development-wrapper-bashenv.sh
exec env -u LD_PRELOAD /usr/local/libexec/workcell/real/codex --version
EOF
  if BASH_ENV=/tmp/workcell-development-wrapper-bashenv.sh bash /usr/local/libexec/workcell/development-wrapper.sh >/tmp/development-wrapper-bashenv.out 2>&1; then
    echo "expected explicit bash launch of development wrapper with hostile BASH_ENV to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/development-wrapper-bashenv.out
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    mkdir -p /workspace/tmp
    ln -sf /usr/local/libexec/workcell/real/codex /workspace/tmp/workcell-renamed-codex
  '
  if AGENT_NAME=codex \
    WORKCELL_MODE=development \
    CODEX_PROFILE=development \
    HOME=/state/agent-home \
    CODEX_HOME=/state/agent-home/.codex \
    TMPDIR=/state/tmp \
    /usr/local/libexec/workcell/development-wrapper.sh \
    /workspace/tmp/workcell-renamed-codex --version >/tmp/development-wrapper-renamed-path.out 2>&1; then
    echo "expected development wrapper to reject renamed symlinks to protected runtimes" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/development-wrapper-renamed-path.out
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups rm -f /workspace/tmp/workcell-renamed-codex
  cat <<'EOF' >/tmp/workcell-node-wrapper-bashenv.sh
exec env -u LD_PRELOAD /usr/local/libexec/workcell/real/node --version
EOF
  if BASH_ENV=/tmp/workcell-node-wrapper-bashenv.sh bash /usr/local/libexec/workcell/node-wrapper.sh --version >/tmp/node-wrapper-bashenv.out 2>&1; then
    echo "expected explicit bash launch of node wrapper with hostile BASH_ENV to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/node-wrapper-bashenv.out
  LOADER="$(
    for candidate in \
      /lib64/ld-linux-x86-64.so.2 \
      /lib/ld-linux-aarch64.so.1 \
      /lib/ld-linux-armhf.so.3 \
      /lib/ld-musl-*.so.1 \
      /lib64/ld-musl-*.so.1; do
      if [ -x "$candidate" ]; then
        printf "%s\n" "$candidate"
        break
      fi
    done
  )"
  test -n "$LOADER"
  if env -u LD_PRELOAD "$LOADER" /usr/local/libexec/workcell/real/node --version >/tmp/node-loader-real.out 2>&1; then
    echo "expected direct loader invocation of the real Node payload to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/node-loader-real.out
  if env -u LD_PRELOAD "$LOADER" /usr/local/libexec/workcell/real/codex --version >/tmp/codex-loader-real.out 2>&1; then
    echo "expected direct loader invocation of the real Codex payload to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/codex-loader-real.out
  if env -u LD_PRELOAD "$LOADER" /usr/local/libexec/workcell/real/claude --version >/tmp/claude-loader-real.out 2>&1; then
    echo "expected direct loader invocation of the real Claude payload to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/claude-loader-real.out
  cp /bin/true "$EXEC_TMP/workcell-state-native"
  chmod 0700 "$EXEC_TMP/workcell-state-native"
  if "$EXEC_TMP/workcell-state-native" >/tmp/state-native.out 2>&1; then
    echo "expected strict profile to reject direct native executable launches from /state" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/state-native.out
  if env -u LD_PRELOAD "$LOADER" "$EXEC_TMP/workcell-state-native" >/tmp/state-native-loader.out 2>&1; then
    echo "expected strict profile to reject loader-mediated native executable launches from /state" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/state-native-loader.out
  if WORKCELL_MODE=breakglass "$EXEC_TMP/workcell-state-native" >/tmp/state-native-workcell-mode-bypass.out 2>&1; then
    echo "expected strict profile to ignore caller-supplied WORKCELL_MODE for mutable native execution" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/state-native-workcell-mode-bypass.out
  if CODEX_PROFILE=build "$EXEC_TMP/workcell-state-native" >/tmp/state-native-codex-profile-bypass.out 2>&1; then
    echo "expected strict profile to ignore caller-supplied CODEX_PROFILE for mutable native execution" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/state-native-codex-profile-bypass.out
  if [[ "${WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC:-0}" != "1" ]] && [[ ! -w /workspace ]]; then
    echo "Workcell note: skipping workspace mutable execution smoke because /workspace is not writable for the runtime user." >&2
    WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC=1
  fi
  if [[ "${WORKCELL_CONTAINER_SMOKE_SKIP_WORKSPACE_MUTABLE_EXEC:-0}" != "1" ]]; then
  workspace_exec_scratch=/workspace/.workcell-exec-scratch
  rm -rf "${workspace_exec_scratch}"
  mkdir -p "${workspace_exec_scratch}"
  workspace_provider_scratch="${workspace_exec_scratch}/workcell-provider-scratch"
  workspace_provider_tampered="${workspace_provider_scratch}/tampered"
  workspace_provider_aggressive="${workspace_provider_scratch}/aggressive"
  workspace_provider_minimal="${workspace_provider_scratch}/minimal"
  workspace_provider_split="${workspace_provider_scratch}/split"
  workspace_provider_no_package="${workspace_provider_scratch}/no-package.js"
  workspace_provider_no_package_split="${workspace_provider_scratch}/no-package-split"
  workspace_provider_benign_marker="${workspace_provider_scratch}/benign-marker-package"
  rm -rf "${workspace_provider_scratch}"
  mkdir -p "${workspace_provider_scratch}"
  cp /bin/true "${workspace_exec_scratch}/.workcell-native-helper"
  chmod 0700 "${workspace_exec_scratch}/.workcell-native-helper"
  if "${workspace_exec_scratch}/.workcell-native-helper" >/tmp/workspace-native.out 2>&1; then
    echo "expected strict profile to reject direct native executable launches from /workspace" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/workspace-native.out
  cp /bin/true "${workspace_exec_scratch}/.workcell-native-helper-deleted-fd"
  chmod 0700 "${workspace_exec_scratch}/.workcell-native-helper-deleted-fd"
  exec 3<"${workspace_exec_scratch}/.workcell-native-helper-deleted-fd"
  rm -f "${workspace_exec_scratch}/.workcell-native-helper-deleted-fd"
  if /proc/self/fd/3 >/tmp/workspace-native-deleted-fd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches from /workspace" >&2
    exec 3<&-
    exit 1
  fi
  if /dev/fd/3 >/tmp/workspace-native-deleted-devfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches via /dev/fd from /workspace" >&2
    exec 3<&-
    exit 1
  fi
  if /proc/self/fd/./3 >/tmp/workspace-native-deleted-dotfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches via normalized /proc/self/fd path from /workspace" >&2
    exec 3<&-
    exit 1
  fi
  if /proc/thread-self/fd/3 >/tmp/workspace-native-deleted-threadself.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches via /proc/thread-self/fd from /workspace" >&2
    exec 3<&-
    exit 1
  fi
  if /proc/self/task/"$BASHPID"/fd/3 >/tmp/workspace-native-deleted-taskfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches via /proc/self/task/<tid>/fd from /workspace" >&2
    exec 3<&-
    exit 1
  fi
  if /dev/stdin <&3 >/tmp/workspace-native-deleted-stdin.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches via /dev/stdin from /workspace" >&2
    exec 3<&-
    exit 1
  fi
  exec 3<&-
  grep -Eq "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile\\.|cannot execute: required file not found|No such file or directory" /tmp/workspace-native-deleted-fd.out
  grep -Eq "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile\\.|cannot execute: required file not found|No such file or directory" /tmp/workspace-native-deleted-devfd.out
  grep -Eq "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile\\.|cannot execute: required file not found|No such file or directory" /tmp/workspace-native-deleted-dotfd.out
  grep -Eq "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile\\.|cannot execute: required file not found|No such file or directory" /tmp/workspace-native-deleted-threadself.out
  grep -Eq "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile\\.|cannot execute: required file not found|No such file or directory" /tmp/workspace-native-deleted-taskfd.out
  grep -Eq "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile\\.|cannot execute: required file not found|No such file or directory" /tmp/workspace-native-deleted-stdin.out
  cp /bin/true "${workspace_exec_scratch}/.workcell-native-helper-deleted-pidfd"
  chmod 0700 "${workspace_exec_scratch}/.workcell-native-helper-deleted-pidfd"
  if (
    exec 5<"${workspace_exec_scratch}/.workcell-native-helper-deleted-pidfd"
    rm -f "${workspace_exec_scratch}/.workcell-native-helper-deleted-pidfd"
    exec /proc/"$BASHPID"/fd/5
  ) >/tmp/workspace-native-deleted-pidfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches via /proc/\$\$/fd from /workspace" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile\\.|cannot execute: required file not found|No such file or directory" /tmp/workspace-native-deleted-pidfd.out
  cp /bin/true "${workspace_exec_scratch}/.workcell-native-helper-deleted-stdout"
  chmod 0700 "${workspace_exec_scratch}/.workcell-native-helper-deleted-stdout"
  if (
    exec 5<"${workspace_exec_scratch}/.workcell-native-helper-deleted-stdout"
    rm -f "${workspace_exec_scratch}/.workcell-native-helper-deleted-stdout"
    exec 1<&5
    exec /dev/stdout
  ) >/tmp/workspace-native-deleted-stdout.out 2>/tmp/workspace-native-deleted-stdout.err; then
    echo "expected strict profile to reject deleted-fd native executable launches via /dev/stdout from /workspace" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile\\.|cannot execute: required file not found|No such file or directory" /tmp/workspace-native-deleted-stdout.err
  cp /bin/true "${workspace_exec_scratch}/.workcell-native-helper-deleted-stderr"
  chmod 0700 "${workspace_exec_scratch}/.workcell-native-helper-deleted-stderr"
  if (
    exec 5<"${workspace_exec_scratch}/.workcell-native-helper-deleted-stderr"
    rm -f "${workspace_exec_scratch}/.workcell-native-helper-deleted-stderr"
    exec 2<&5
    exec /dev/stderr
  ) >/tmp/workspace-native-deleted-stderr.out 2>&1; then
    echo "expected strict profile to reject deleted-fd native executable launches via /dev/stderr from /workspace" >&2
    exit 1
  fi
  if env -u LD_PRELOAD "$LOADER" "${workspace_exec_scratch}/.workcell-native-helper" >/tmp/workspace-native-loader.out 2>&1; then
    echo "expected strict profile to reject loader-mediated native executable launches from /workspace" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/workspace-native-loader.out
  cat >"${workspace_exec_scratch}/.workcell-node-shebang" <<EOF
#!/usr/local/libexec/workcell/real/node
console.log("workcell shebang bypass");
EOF
  chmod 0700 "${workspace_exec_scratch}/.workcell-node-shebang"
  if "${workspace_exec_scratch}/.workcell-node-shebang" >/tmp/workspace-node-shebang.out 2>&1; then
    echo "expected strict profile to reject mutable shebang execution of the real Node payload" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-node-shebang.out
  cat >"${workspace_exec_scratch}/.workcell-node-shebang-deleted-fd" <<EOF
#!/usr/local/libexec/workcell/real/node
console.log("workcell deleted fd shebang bypass");
EOF
  chmod 0700 "${workspace_exec_scratch}/.workcell-node-shebang-deleted-fd"
  exec 4<"${workspace_exec_scratch}/.workcell-node-shebang-deleted-fd"
  rm -f "${workspace_exec_scratch}/.workcell-node-shebang-deleted-fd"
  if /proc/self/fd/4 >/tmp/workspace-node-shebang-deleted-fd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution of the real Node payload" >&2
    exec 4<&-
    exit 1
  fi
  if /dev/fd/4 >/tmp/workspace-node-shebang-deleted-devfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via /dev/fd of the real Node payload" >&2
    exec 4<&-
    exit 1
  fi
  if /proc/self/fd/./4 >/tmp/workspace-node-shebang-deleted-dotfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via normalized /proc/self/fd path of the real Node payload" >&2
    exec 4<&-
    exit 1
  fi
  if /proc/thread-self/fd/4 >/tmp/workspace-node-shebang-deleted-threadself.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via /proc/thread-self/fd of the real Node payload" >&2
    exec 4<&-
    exit 1
  fi
  if /proc/self/task/"$BASHPID"/fd/4 >/tmp/workspace-node-shebang-deleted-taskfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via /proc/self/task/<tid>/fd of the real Node payload" >&2
    exec 4<&-
    exit 1
  fi
  if /dev/stdin <&4 >/tmp/workspace-node-shebang-deleted-stdin.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via /dev/stdin of the real Node payload" >&2
    exec 4<&-
    exit 1
  fi
  exec 4<&-
  grep -Eq "Workcell blocked direct protected runtime execution|cannot execute: required file not found|No such file or directory" /tmp/workspace-node-shebang-deleted-fd.out
  grep -Eq "Workcell blocked direct protected runtime execution|cannot execute: required file not found|No such file or directory" /tmp/workspace-node-shebang-deleted-devfd.out
  grep -Eq "Workcell blocked direct protected runtime execution|cannot execute: required file not found|No such file or directory" /tmp/workspace-node-shebang-deleted-dotfd.out
  grep -Eq "Workcell blocked direct protected runtime execution|cannot execute: required file not found|No such file or directory" /tmp/workspace-node-shebang-deleted-threadself.out
  grep -Eq "Workcell blocked direct protected runtime execution|cannot execute: required file not found|No such file or directory" /tmp/workspace-node-shebang-deleted-taskfd.out
  grep -Eq "Workcell blocked direct protected runtime execution|cannot execute: required file not found|No such file or directory" /tmp/workspace-node-shebang-deleted-stdin.out
  cat >"${workspace_exec_scratch}/.workcell-node-shebang-deleted-pidfd" <<EOF
#!/usr/local/libexec/workcell/real/node
console.log("workcell pid fd deleted shebang bypass");
EOF
  chmod 0700 "${workspace_exec_scratch}/.workcell-node-shebang-deleted-pidfd"
  if (
    exec 5<"${workspace_exec_scratch}/.workcell-node-shebang-deleted-pidfd"
    rm -f "${workspace_exec_scratch}/.workcell-node-shebang-deleted-pidfd"
    exec /proc/"$BASHPID"/fd/5
  ) >/tmp/workspace-node-shebang-deleted-pidfd.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via /proc/\$\$/fd of the real Node payload" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked direct protected runtime execution|cannot execute: required file not found|No such file or directory" /tmp/workspace-node-shebang-deleted-pidfd.out
  cat >"${workspace_exec_scratch}/.workcell-node-shebang-deleted-stdout" <<EOF
#!/usr/local/libexec/workcell/real/node
console.log("workcell stdout deleted shebang bypass");
EOF
  chmod 0700 "${workspace_exec_scratch}/.workcell-node-shebang-deleted-stdout"
  if (
    exec 5<"${workspace_exec_scratch}/.workcell-node-shebang-deleted-stdout"
    rm -f "${workspace_exec_scratch}/.workcell-node-shebang-deleted-stdout"
    exec 1<&5
    exec /dev/stdout
  ) >/tmp/workspace-node-shebang-deleted-stdout.out 2>/tmp/workspace-node-shebang-deleted-stdout.err; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via /dev/stdout of the real Node payload" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked direct protected runtime execution|cannot execute: required file not found|No such file or directory" /tmp/workspace-node-shebang-deleted-stdout.err
  cat >"${workspace_exec_scratch}/.workcell-node-shebang-deleted-stderr" <<EOF
#!/usr/local/libexec/workcell/real/node
console.log("workcell stderr deleted shebang bypass");
EOF
  chmod 0700 "${workspace_exec_scratch}/.workcell-node-shebang-deleted-stderr"
  if (
    exec 5<"${workspace_exec_scratch}/.workcell-node-shebang-deleted-stderr"
    rm -f "${workspace_exec_scratch}/.workcell-node-shebang-deleted-stderr"
    exec 2<&5
    exec /dev/stderr
  ) >/tmp/workspace-node-shebang-deleted-stderr.out 2>&1; then
    echo "expected strict profile to reject deleted-fd mutable shebang execution via /dev/stderr of the real Node payload" >&2
    exit 1
  fi
  cat >"${workspace_exec_scratch}/.workcell-loader-node-shebang" <<EOF
#!${LOADER} /usr/local/libexec/workcell/real/node
console.log("workcell loader shebang bypass");
EOF
  chmod 0700 "${workspace_exec_scratch}/.workcell-loader-node-shebang"
  if "${workspace_exec_scratch}/.workcell-loader-node-shebang" >/tmp/workspace-loader-node-shebang.out 2>&1; then
    echo "expected strict profile to reject mutable loader shebang execution of the real Node payload" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-loader-node-shebang.out
  cat >"${workspace_exec_scratch}/.workcell-env-node-shebang" <<EOF
#!/usr/bin/env -S /usr/local/libexec/workcell/real/node
console.log("workcell env shebang bypass");
EOF
  chmod 0700 "${workspace_exec_scratch}/.workcell-env-node-shebang"
  if "${workspace_exec_scratch}/.workcell-env-node-shebang" >/tmp/workspace-env-node-shebang.out 2>&1; then
    echo "expected strict profile to reject env -S shebang execution of the real Node payload" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-env-node-shebang.out
  cat >"${workspace_exec_scratch}/.workcell-env-loader-node-shebang" <<EOF
#!/usr/bin/env -S ${LOADER} /usr/local/libexec/workcell/real/node
console.log("workcell env loader shebang bypass");
EOF
  chmod 0700 "${workspace_exec_scratch}/.workcell-env-loader-node-shebang"
  if "${workspace_exec_scratch}/.workcell-env-loader-node-shebang" >/tmp/workspace-env-loader-node-shebang.out 2>&1; then
    echo "expected strict profile to reject env -S loader shebang execution of the real Node payload" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-env-loader-node-shebang.out
  cp /usr/local/libexec/workcell/real/node "${workspace_exec_scratch}/node"
  chmod 0700 "${workspace_exec_scratch}/node"
  cat >"${workspace_exec_scratch}/.workcell-env-path-node-shebang" <<EOF
#!/usr/bin/env -S PATH=${workspace_exec_scratch} node --version
EOF
  chmod 0700 "${workspace_exec_scratch}/.workcell-env-path-node-shebang"
  if "${workspace_exec_scratch}/.workcell-env-path-node-shebang" >/tmp/workspace-env-path-node-shebang.out 2>&1; then
    echo "expected strict profile to reject env -S PATH-rebound execution of a protected Node copy" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-env-path-node-shebang.out
  if env -i PATH="${workspace_exec_scratch}" /usr/bin/env node --version >/tmp/env-path-node.out 2>&1; then
    echo "expected strict profile to reject env basename resolution to a protected Node copy" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/env-path-node.out
  cat <<'EOF' >"${workspace_exec_scratch}/workcell-child-envp-bypass.js"
const fs = require("node:fs");
const { spawnSync } = require("node:child_process");
const scratch = process.env.WORKCELL_EXEC_SCRATCH;

fs.copyFileSync("/usr/local/libexec/workcell/real/node", `${scratch}/node`);
fs.chmodSync(`${scratch}/node`, 0o700);
fs.writeFileSync(
  `${scratch}/shebang-bypass`,
  `#!/usr/bin/env -S PATH=${scratch} node --version\n`,
  { mode: 0o700 },
);

const result = spawnSync(`${scratch}/shebang-bypass`, [], {
  encoding: "utf8",
  env: { PATH: scratch },
});

const blockedByRuntime =
  typeof result.stderr === "string" &&
  result.stderr.includes("Workcell blocked direct protected runtime execution");
const blockedByExec =
  result.error &&
  (result.error.code === "EPERM" || result.error.code === "ENOENT");

if (
  result.status === 0 ||
  !(blockedByRuntime || blockedByExec)
) {
  process.stderr.write(
    `unexpected child-envp shebang result: ${JSON.stringify(result)}\n`,
  );
  process.exit(1);
}

console.log("child-envp-shebang-blocked");
EOF
  WORKCELL_EXEC_SCRATCH="${workspace_exec_scratch}" node "${workspace_exec_scratch}/workcell-child-envp-bypass.js" >/tmp/workspace-child-envp-bypass.out 2>/tmp/workspace-child-envp-bypass.err
  grep -q "child-envp-shebang-blocked" /tmp/workspace-child-envp-bypass.out
  cat >"${workspace_exec_scratch}/.workcell-env-shell-node-shebang" <<EOF
#!/usr/bin/env -S /bin/sh -c /usr/local/libexec/workcell/real/node
EOF
  chmod 0700 "${workspace_exec_scratch}/.workcell-env-shell-node-shebang"
  if "${workspace_exec_scratch}/.workcell-env-shell-node-shebang" >/tmp/workspace-env-shell-node-shebang.out 2>&1; then
    echo "expected strict profile to reject env -S shell trampoline execution of the real Node payload" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/workspace-env-shell-node-shebang.out
  rm -f "${workspace_exec_scratch}/.workcell-node-shebang" "${workspace_exec_scratch}/.workcell-loader-node-shebang"
  rm -f "${workspace_exec_scratch}/.workcell-env-node-shebang" "${workspace_exec_scratch}/.workcell-env-loader-node-shebang" "${workspace_exec_scratch}/.workcell-env-path-node-shebang" "${workspace_exec_scratch}/.workcell-env-shell-node-shebang"
  rm -f "${workspace_exec_scratch}/shebang-bypass" "${workspace_exec_scratch}/workcell-child-envp-bypass.js"
  rm -f "${workspace_exec_scratch}/node"
  rm -f "${workspace_exec_scratch}/.workcell-native-helper"
	cp /usr/local/libexec/workcell/real/node "$EXEC_TMP/workcell-node-real-copy"
	chmod 0700 "$EXEC_TMP/workcell-node-real-copy"
	if "$EXEC_TMP/workcell-node-real-copy" --version >/tmp/node-real-copy.out 2>&1; then
		echo "expected renamed copy of the real Node payload to fail" >&2
		exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/node-real-copy.out
	if env -u LD_PRELOAD "$LOADER" "$EXEC_TMP/workcell-node-real-copy" --version >/tmp/node-loader-copy.out 2>&1; then
		echo "expected loader invocation of a renamed real Node copy to fail" >&2
		exit 1
	fi
	grep -q "Workcell blocked direct protected runtime execution" /tmp/node-loader-copy.out
	cp /usr/local/libexec/workcell/real/claude "$EXEC_TMP/workcell-claude-real-copy"
	chmod 0700 "$EXEC_TMP/workcell-claude-real-copy"
	if "$EXEC_TMP/workcell-claude-real-copy" --version >/tmp/claude-real-copy.out 2>&1; then
		echo "expected renamed copy of the real Claude payload to fail" >&2
		exit 1
	fi
	grep -q "Workcell blocked direct protected runtime execution" /tmp/claude-real-copy.out
	if env -u LD_PRELOAD "$LOADER" "$EXEC_TMP/workcell-claude-real-copy" --version >/tmp/claude-loader-copy.out 2>&1; then
		echo "expected loader invocation of a renamed real Claude copy to fail" >&2
		exit 1
	fi
	grep -q "Workcell blocked direct protected runtime execution" /tmp/claude-loader-copy.out
	if node /opt/workcell/providers/node_modules/@google/gemini-cli/bundle/gemini.js --yolo >/tmp/node-provider-gemini.out 2>&1; then
		echo "expected Workcell node wrapper to reject direct Gemini provider script execution" >&2
		exit 1
	fi
  grep -q "Workcell blocked direct provider script execution via node." /tmp/node-provider-gemini.out
  if WORKCELL_ALLOW_PROVIDER_NODE=1 node /opt/workcell/providers/node_modules/@google/gemini-cli/bundle/gemini.js --yolo >/tmp/node-provider-env.out 2>&1; then
    echo "expected Workcell node wrapper to ignore caller-supplied provider bypass env" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct provider script execution via node." /tmp/node-provider-env.out
  cp /bin/true "${workspace_exec_scratch}/not-an-addon.node"
  cat <<'EOF' >"${workspace_exec_scratch}/workcell-native-addon-require.js"
const scratch = process.env.WORKCELL_EXEC_SCRATCH;

try {
  require(`${scratch}/not-an-addon.node`);
  console.error("expected Workcell to block native addon loading");
  process.exit(1);
} catch (error) {
  if (!String(error).includes("Workcell blocked native addon loading via public node.")) {
    throw error;
  }
  console.log("native-addon-load-blocked");
}
EOF
  WORKCELL_EXEC_SCRATCH="${workspace_exec_scratch}" node "${workspace_exec_scratch}/workcell-native-addon-require.js" >/tmp/node-native-addon.out 2>&1
  grep -q "native-addon-load-blocked" /tmp/node-native-addon.out
  cp -R /opt/workcell/providers /tmp/workcell-provider-copy
  if node /tmp/workcell-provider-copy/node_modules/@google/gemini-cli/bundle/gemini.js --version >/tmp/node-provider-copy-gemini.out 2>&1; then
    echo "expected copied Gemini provider package execution via public node to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked provider package execution via public node." /tmp/node-provider-copy-gemini.out
  cat <<'EOF' >/tmp/workcell-provider-import.mjs
await import("/tmp/workcell-provider-copy/node_modules/@google/gemini-cli/bundle/gemini.js");
EOF
  if node /tmp/workcell-provider-import.mjs >/tmp/node-provider-copy-import.out 2>&1; then
    echo "expected imported copied provider package execution via public node to fail" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked provider package execution via public node.|Workcell blocked public node execution outside the mounted workspace." /tmp/node-provider-copy-import.out
  rm -f "${workspace_exec_scratch}/not-an-addon.node" "${workspace_exec_scratch}/workcell-native-addon-require.js"
  cat <<'EOF' >/tmp/workcell-node-public-preload.js
require("fs").writeFileSync("/tmp/workcell-node-public-preload-ran", "1")
process.exit(99)
EOF
  rm -f /tmp/workcell-node-public-preload-ran
  node --version >/tmp/node-public-baseline.out 2>&1
  if ! NODE_OPTIONS=--require=/tmp/workcell-node-public-preload.js node --version >/tmp/node-public-node-options.out 2>&1; then
    cat /tmp/node-public-node-options.out >&2
    echo "expected public node wrapper to ignore caller-supplied NODE_OPTIONS" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-node-public-preload-ran
  cat <<'EOF' >"${workspace_exec_scratch}/workcell-node-env-check.js"
console.log(JSON.stringify({
  nodeExtra: process.env.NODE_EXTRA_CA_CERTS ?? "",
  sslFile: process.env.SSL_CERT_FILE ?? "",
  sslDir: process.env.SSL_CERT_DIR ?? "",
}));
EOF
  NODE_EXTRA_CA_CERTS=/workspace/does-not-exist.pem \
    SSL_CERT_FILE=/workspace/does-not-exist.pem \
    SSL_CERT_DIR=/workspace/does-not-exist.d \
    node "${workspace_exec_scratch}/workcell-node-env-check.js" >/tmp/node-public-env-check.out 2>&1
  grep -Fq "{\"nodeExtra\":\"\",\"sslFile\":\"\",\"sslDir\":\"\"}" /tmp/node-public-env-check.out
  if node --env-file-if-exists=/workspace/does-not-exist.env --version >/tmp/node-public-env-file-if-exists.out 2>&1; then
    echo "expected public node wrapper to reject --env-file-if-exists" >&2
    exit 1
  fi
  grep -q "Workcell blocked dynamic Node code-loading option outside provider wrappers." /tmp/node-public-env-file-if-exists.out
  if node --experimental-config-file=/workspace/does-not-exist.json --version >/tmp/node-public-config-file.out 2>&1; then
    echo "expected public node wrapper to reject --experimental-config-file" >&2
    exit 1
  fi
  grep -q "Workcell blocked dynamic Node code-loading option outside provider wrappers." /tmp/node-public-config-file.out
  if node --experimental-default-config-file=/workspace/does-not-exist.json --version >/tmp/node-public-default-config-file.out 2>&1; then
    echo "expected public node wrapper to reject --experimental-default-config-file" >&2
    exit 1
  fi
  grep -q "Workcell blocked dynamic Node code-loading option outside provider wrappers." /tmp/node-public-default-config-file.out
  cat <<'EOF' >"${workspace_exec_scratch}/git"
#!/bin/sh
printf 'path-bypass-git\n'
EOF
  cat <<'EOF' >"${workspace_exec_scratch}/node"
#!/bin/sh
printf 'path-bypass-node\n'
EOF
  chmod 0700 "${workspace_exec_scratch}/git" "${workspace_exec_scratch}/node"
  cat <<'EOF' >"${workspace_exec_scratch}/workcell-path-sanitize.js"
const { spawnSync } = require("node:child_process");

const git = spawnSync("git", ["--version"], { encoding: "utf8" });
const node = spawnSync("node", ["--version"], { encoding: "utf8" });

if (git.status !== 0 || node.status !== 0) {
  throw new Error(`expected trusted PATH child launches to succeed: ${git.status}/${node.status}`);
}
if (git.stdout.includes("path-bypass-git") || node.stdout.includes("path-bypass-node")) {
  throw new Error("expected Workcell wrappers to ignore caller-controlled PATH for child processes");
}
if (!git.stdout.includes("git version")) {
  throw new Error(`expected real git on PATH, received: ${git.stdout}`);
}
if (!node.stdout.trim().startsWith("v")) {
  throw new Error(`expected real node on PATH, received: ${node.stdout}`);
}

console.log("trusted-path-preserved");
EOF
  env PATH="${workspace_exec_scratch}:$PATH" /usr/local/bin/node "${workspace_exec_scratch}/workcell-path-sanitize.js" >/tmp/node-path-sanitize.out 2>&1
  grep -q "trusted-path-preserved" /tmp/node-path-sanitize.out
  if grep -q "path-bypass-" /tmp/node-path-sanitize.out; then
    echo "expected public node wrapper to sanitize PATH for child processes" >&2
    exit 1
  fi
  rm -f "${workspace_exec_scratch}/git" "${workspace_exec_scratch}/node" "${workspace_exec_scratch}/workcell-path-sanitize.js"
  rm -rf "${workspace_exec_scratch}"
  else
    echo "Skipping nested workspace mutable-exec smoke checks for the remote validator bind-mount path." >&2
  fi
  if printf 'console.log("workcell")\n' | node >/tmp/node-stdin.out 2>&1; then
    echo "expected public node wrapper to reject stdin-driven execution" >&2
    exit 1
  fi
  grep -q "Workcell blocked stdin-driven Node execution outside provider wrappers." /tmp/node-stdin.out
  if WORKSPACE=/ node /tmp/workcell-provider-copy-tampered/cli.js --version >/tmp/node-workspace-env.out 2>&1; then
    echo "expected public node wrapper to ignore caller-supplied WORKSPACE overrides" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked public node execution outside the mounted workspace.|Workcell blocked provider package execution via public node." /tmp/node-workspace-env.out
  cat <<'EOF' >/tmp/workcell-node-preload.js
require("fs").writeFileSync("/tmp/workcell-node-preload-ran", "1")
process.exit(99)
EOF
  rm -f /tmp/workcell-node-preload-ran
  if ! NODE_OPTIONS=--require=/tmp/workcell-node-preload.js claude --version >/tmp/claude-node-options.out 2>&1; then
    cat /tmp/claude-node-options.out >&2
    echo "expected Claude provider launch to ignore caller-supplied NODE_OPTIONS" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-node-preload-ran
  rm -f /tmp/workcell-node-preload-ran
  if ! NODE_OPTIONS=--require=/tmp/workcell-node-preload.js gemini --version >/tmp/gemini-node-options.out 2>&1; then
    cat /tmp/gemini-node-options.out >&2
    echo "expected Gemini provider launch to ignore caller-supplied NODE_OPTIONS" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-node-preload-ran
  mkdir -p "$EXEC_TMP/git-guard" && cd "$EXEC_TMP/git-guard"
  git init -q
  git config user.name "Workcell Smoke"
  git config user.email "workcell-smoke@example.com"
  touch smoke.txt
  git add smoke.txt
  if git commit --no-verify -m smoke >/tmp/git-guard.out 2>&1; then
    echo "expected Workcell git guard to reject --no-verify" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard.out
  if /usr/bin/git commit -n -m smoke >/tmp/git-guard-short.out 2>&1; then
    echo "expected Workcell git guard to reject git commit -n" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-short.out
  if /usr/bin/git commit -nm smoke >/tmp/git-guard-short-combined.out 2>&1; then
    echo "expected Workcell git guard to reject combined short-option git commit -nm" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-short-combined.out
  mkdir -p "$EXEC_TMP/git-guard-allow" && cd "$EXEC_TMP/git-guard-allow"
  git init -q
  git config user.name "Workcell Smoke"
  git config user.email "workcell-smoke@example.com"
  if ! /usr/bin/git commit --allow-empty -mnote >/tmp/git-guard-allow-message.out 2>&1; then
    cat /tmp/git-guard-allow-message.out >&2
    echo "expected Workcell git guard to allow git commit -mnote" >&2
    exit 1
  fi
  if ! /usr/bin/git commit -uno --allow-empty -m note >/tmp/git-guard-allow-u.out 2>&1; then
    cat /tmp/git-guard-allow-u.out >&2
    echo "expected Workcell git guard to allow git commit -uno" >&2
    exit 1
  fi
  rm -f /tmp/workcell-git-ssh-env-ran /tmp/workcell-git-ssh-helper-ran /tmp/workcell-git-ssh-config-ran
  cat <<EOF >"$EXEC_TMP/git-ssh-helper.sh"
#!/bin/sh
touch /tmp/workcell-git-ssh-helper-ran
exit 7
EOF
  chmod 0700 "$EXEC_TMP/git-ssh-helper.sh"
  cat <<EOF >"$EXEC_TMP/git-ssh-command.sh"
#!/bin/sh
touch /tmp/workcell-git-ssh-env-ran
exit 7
EOF
  chmod 0700 "$EXEC_TMP/git-ssh-command.sh"
  if GIT_SSH_COMMAND="$EXEC_TMP/git-ssh-command.sh" git ls-remote ssh://example.invalid/workcell.git >/tmp/git-ssh-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_SSH_COMMAND overrides" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-git-ssh-env-ran
  grep -q "Workcell blocked git control-plane override" /tmp/git-ssh-env.out
  if GIT_SSH="$EXEC_TMP/git-ssh-helper.sh" git ls-remote ssh://example.invalid/workcell.git >/tmp/git-ssh-helper.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_SSH overrides" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-git-ssh-helper-ran
  grep -q "Workcell blocked git control-plane override" /tmp/git-ssh-helper.out
  cat <<EOF >"$EXEC_TMP/git-ssh-config.sh"
#!/bin/sh
touch /tmp/workcell-git-ssh-config-ran
exit 7
EOF
  chmod 0700 "$EXEC_TMP/git-ssh-config.sh"
  if git -c core.sshCommand="$EXEC_TMP/git-ssh-config.sh" ls-remote ssh://example.invalid/workcell.git >/tmp/git-ssh-config.out 2>&1; then
    echo "expected Workcell git guard to reject core.sshCommand overrides" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-git-ssh-config-ran
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-ssh-config.out
  cat <<EOF >"$EXEC_TMP/git-credential-helper.sh"
#!/bin/sh
touch /tmp/workcell-git-cred-ran
printf "%s\n%s\n" "username=workcell" "password=secret"
EOF
  chmod 0700 "$EXEC_TMP/git-credential-helper.sh"
  rm -f /tmp/workcell-git-cred-ran
  if git -c credential.helper="!$EXEC_TMP/git-credential-helper.sh" credential fill >/tmp/git-credential-helper.out 2>&1 <<EOF
protocol=https
host=example.invalid

EOF
  then
    echo "expected Workcell git guard to reject credential.helper overrides" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-git-cred-ran
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-credential-helper.out
  cat <<EOF >"$EXEC_TMP/git-askpass.sh"
#!/bin/sh
touch /tmp/workcell-git-askpass-ran
printf "%s\n" "secret"
EOF
  chmod 0700 "$EXEC_TMP/git-askpass.sh"
  rm -f /tmp/workcell-git-askpass-ran
  if GIT_ASKPASS="$EXEC_TMP/git-askpass.sh" git credential fill >/tmp/git-askpass.out 2>&1 <<EOF
protocol=https
host=example.invalid
username=workcell

EOF
  then
    echo "expected Workcell git guard to reject GIT_ASKPASS overrides" >&2
    exit 1
  fi
  test ! -e /tmp/workcell-git-askpass-ran
  grep -q "Workcell blocked git control-plane override" /tmp/git-askpass.out
  if /usr/local/libexec/workcell/core/git commit -n -m smoke >/tmp/git-guard-real.out 2>&1; then
    echo "expected Workcell git guard to reject direct hidden git execution" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-real.out
  if /usr/local/libexec/workcell/core/git commit -nm smoke >/tmp/git-guard-real-combined.out 2>&1; then
    echo "expected Workcell git guard to reject direct hidden git execution with combined short options" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-real-combined.out
  if /usr/local/libexec/workcell/real/git status >/tmp/git-guard-real-payload.out 2>&1; then
    echo "expected direct real git payload execution to fail" >&2
    exit 1
  fi
  if ln /usr/local/libexec/workcell/core/git "$EXEC_TMP/git-hardlink" >/tmp/git-hardlink-link.out 2>&1; then
    if "$EXEC_TMP/git-hardlink" commit --no-verify -m smoke >/tmp/git-guard-hardlink.out 2>&1; then
      echo "expected Workcell git guard to reject hardlinked hidden git execution" >&2
      exit 1
    fi
    grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-hardlink.out
  else
    grep -Eiq "cross-device|operation not permitted|read-only" /tmp/git-hardlink-link.out
  fi
  ln -s /usr/local/libexec/workcell/core/git "$EXEC_TMP/git-symlink"
  if "$EXEC_TMP/git-symlink" commit -n -m smoke >/tmp/git-guard-symlink.out 2>&1; then
    echo "expected Workcell git guard to reject symlinked hidden git execution" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-symlink.out
  if ! cp /usr/local/libexec/workcell/core/git "$EXEC_TMP/git-copy" >/tmp/git-copy.out 2>&1; then
    echo "expected Workcell git trampoline to remain copyable for deterministic debugging" >&2
    exit 1
  fi
  if "$EXEC_TMP/git-copy" commit --no-verify -m smoke >/tmp/git-guard-copy.out 2>&1; then
    echo "expected copied Workcell git trampoline under mutable state to be blocked before execution" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile." /tmp/git-guard-copy.out
  if git -c core.hooksPath=/dev/null commit -m smoke >/tmp/git-guard-hooks.out 2>&1; then
    echo "expected Workcell git guard to reject inline core.hooksPath override" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-hooks.out
  if git -c core.hookspath=/dev/null commit -m smoke >/tmp/git-guard-hooks-lower.out 2>&1; then
    echo "expected Workcell git guard to reject lowercase inline core.hookspath override" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-hooks-lower.out
  if git -c core.fsmonitor=/tmp/workcell-fsmonitor status >/tmp/git-guard-fsmonitor.out 2>&1; then
    echo "expected Workcell git guard to reject inline core.fsmonitor override" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-fsmonitor.out
  if GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.hooksPath GIT_CONFIG_VALUE_0=/dev/null git commit -m smoke >/tmp/git-guard-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_CONFIG_* hook override" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-env.out
  if GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.fsmonitor GIT_CONFIG_VALUE_0=/tmp/workcell-fsmonitor git status >/tmp/git-guard-env-fsmonitor.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_CONFIG_* fsmonitor override" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-env-fsmonitor.out
  if GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=Core.HooksPath GIT_CONFIG_VALUE_0=/dev/null git commit -m smoke >/tmp/git-guard-env-mixed.out 2>&1; then
    echo "expected Workcell git guard to reject mixed-case GIT_CONFIG_* hook override" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-env-mixed.out
  printf "[core]\n  hooksPath = /dev/null\n" >"$EXEC_TMP/git-bypass.cfg"
  if git -c include.path="$EXEC_TMP/git-bypass.cfg" commit -m smoke >/tmp/git-guard-include.out 2>&1; then
    echo "expected Workcell git guard to reject inline include.path override" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-include.out
  if git -c includeIf.onbranch:main.path="$EXEC_TMP/git-bypass.cfg" commit -m smoke >/tmp/git-guard-includeif.out 2>&1; then
    echo "expected Workcell git guard to reject inline includeIf override" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-includeif.out
  if git -c core.worktree=/tmp commit -m smoke >/tmp/git-guard-worktree.out 2>&1; then
    echo "expected Workcell git guard to reject inline core.worktree override" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-worktree.out
  if GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=include.path GIT_CONFIG_VALUE_0="$EXEC_TMP/git-bypass.cfg" git commit -m smoke >/tmp/git-guard-env-include.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_CONFIG_* include.path override" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override" /tmp/git-guard-env-include.out
  if GIT_CONFIG_PARAMETERS="core.worktree=/tmp" git status >/tmp/git-guard-env-parameters-worktree.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_CONFIG_PARAMETERS core.worktree override" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-env-parameters-worktree.out
  if GIT_DIR="$EXEC_TMP/git-guard/.git" git status >/tmp/git-guard-git-dir-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_DIR overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-git-dir-env.out
  if GIT_EXEC_PATH="$EXEC_TMP/git-guard" git status >/tmp/git-guard-git-exec-path-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_EXEC_PATH overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-git-exec-path-env.out
  if GIT_EXTERNAL_DIFF="$EXEC_TMP/git-guard" git status >/tmp/git-guard-git-external-diff-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_EXTERNAL_DIFF overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-git-external-diff-env.out
  if GIT_PAGER=cat git status >/tmp/git-guard-git-pager-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_PAGER overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-git-pager-env.out
  if PAGER=cat git status >/tmp/git-guard-pager-env.out 2>&1; then
    echo "expected Workcell git guard to reject PAGER overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-pager-env.out
  if GIT_EDITOR=cat git status >/tmp/git-guard-git-editor-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_EDITOR overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-git-editor-env.out
  if GIT_SEQUENCE_EDITOR=cat git status >/tmp/git-guard-git-sequence-editor-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_SEQUENCE_EDITOR overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-git-sequence-editor-env.out
  if VISUAL=cat git status >/tmp/git-guard-visual-env.out 2>&1; then
    echo "expected Workcell git guard to reject VISUAL overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-visual-env.out
  if ! AGENT_NAME=codex \
    WORKCELL_MODE=development \
    CODEX_PROFILE=development \
    GIT_CONFIG_COUNT=1 \
    GIT_CONFIG_KEY_0=core.pager \
    GIT_CONFIG_VALUE_0=cat \
    GIT_PAGER=cat \
    PAGER=cat \
    GIT_EDITOR=cat \
    EDITOR=cat \
    VISUAL=cat \
    SSH_ASKPASS="$EXEC_TMP/git-askpass.sh" \
    GIT_ASKPASS="$EXEC_TMP/git-askpass.sh" \
    /usr/local/libexec/workcell/development-wrapper.sh \
    /bin/bash -lc "git --version >/tmp/git-development-wrapper-scrub.out"; then
    cat /tmp/git-development-wrapper-scrub.out >&2
    echo "expected development wrapper to scrub inherited git control-plane env before launching git" >&2
    exit 1
  fi
  if GIT_CONFIG_GLOBAL="$EXEC_TMP/git-bypass.cfg" git status >/tmp/git-guard-git-config-global-env.out 2>&1; then
    echo "expected Workcell git guard to reject GIT_CONFIG_GLOBAL overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-git-config-global-env.out
  if git --exec-path="$EXEC_TMP/git-guard" status >/tmp/git-guard-exec-path-override.out 2>&1; then
    echo "expected Workcell git guard to reject --exec-path overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-exec-path-override.out
  if git --git-dir="$EXEC_TMP/git-guard/.git" --work-tree="$EXEC_TMP/git-guard" status >/tmp/git-guard-path-override.out 2>&1; then
    echo "expected Workcell git guard to reject git-dir/work-tree overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-path-override.out
  git config alias.ci "commit -n"
  if git ci -m smoke >/tmp/git-guard-alias.out 2>&1; then
    echo "expected Workcell git guard to reject alias-expanded git commit -n" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git alias bypass|Workcell blocked git control-plane override|Workcell blocked direct protected runtime execution" /tmp/git-guard-alias.out
  git config --unset alias.ci
  git config alias.c "commit -n"
  git config alias.ci c
  if git ci -m smoke >/tmp/git-guard-alias-chain.out 2>&1; then
    echo "expected Workcell git guard to reject recursively expanded git commit -n aliases" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git alias bypass|Workcell blocked git control-plane override|Workcell blocked direct protected runtime execution" /tmp/git-guard-alias-chain.out
  git config alias.ctab "$(printf "commit\\t-n")"
  if git ctab -m smoke >/tmp/git-guard-alias-tab.out 2>&1; then
    echo "expected Workcell git guard to reject tab-separated alias-expanded git commit -n" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git alias bypass|Workcell blocked git control-plane override|Workcell blocked direct protected runtime execution" /tmp/git-guard-alias-tab.out
  git config alias.cquoted "commit \"-n\""
  if git cquoted -m smoke >/tmp/git-guard-alias-quoted.out 2>&1; then
    echo "expected Workcell git guard to reject quoted alias-expanded git commit -n" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git alias bypass|Workcell blocked git control-plane override|Workcell blocked direct protected runtime execution" /tmp/git-guard-alias-quoted.out
  git config alias.cbundle "commit -nm"
  if git cbundle smoke >/tmp/git-guard-alias-combined.out 2>&1; then
    echo "expected Workcell git guard to reject alias-expanded combined git commit -nm" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git alias bypass|Workcell blocked git control-plane override|Workcell blocked direct protected runtime execution" /tmp/git-guard-alias-combined.out
  if git config alias.execpath "--exec-path=$EXEC_TMP/git-guard status" >/tmp/git-guard-alias-exec-path-define.out 2>&1; then
    echo "expected Workcell git guard to reject defining an alias with --exec-path" >&2
    exit 1
  fi
  grep -q "Workcell blocked git control-plane override" /tmp/git-guard-alias-exec-path-define.out
  if git config alias.cshell "!git commit \\\\-n -m smoke" >/tmp/git-guard-alias-shell-escaped-define.out 2>&1; then
    if git cshell >/tmp/git-guard-alias-shell-escaped.out 2>&1; then
      echo "expected Workcell git guard to reject shell alias git commit \\\\-n bypass" >&2
      exit 1
    fi
    grep -Eq "Workcell blocked git alias bypass|Workcell blocked git control-plane override|Workcell blocked direct protected runtime execution" /tmp/git-guard-alias-shell-escaped.out
  else
    grep -q "Workcell blocked git control-plane override" /tmp/git-guard-alias-shell-escaped-define.out
  fi
  if git config alias.csubst "!git commit \$(printf -- -)\$(printf n) -m smoke" >/tmp/git-guard-alias-shell-substitution-define.out 2>&1; then
    if git csubst >/tmp/git-guard-alias-shell-substitution.out 2>&1; then
      echo "expected Workcell git guard to reject shell alias substitution bypass" >&2
      exit 1
    fi
    grep -Eq "Workcell blocked git alias bypass|Workcell blocked git control-plane override|Workcell blocked direct protected runtime execution" /tmp/git-guard-alias-shell-substitution.out
  else
    grep -q "Workcell blocked git control-plane override" /tmp/git-guard-alias-shell-substitution-define.out
  fi
  LOADER="$(
    for candidate in \
      /lib64/ld-linux-x86-64.so.2 \
      /lib/ld-linux-aarch64.so.1 \
      /lib/ld-linux-armhf.so.3 \
      /lib/ld-musl-*.so.1 \
      /lib64/ld-musl-*.so.1; do
      if [ -x "$candidate" ]; then
        printf "%s\n" "$candidate"
        break
      fi
    done
  )"
  test -n "$LOADER"
  if "$LOADER" /usr/local/libexec/workcell/real/git commit --no-verify -m smoke >/tmp/git-guard-loader.out 2>&1; then
    echo "expected Workcell git guard to reject direct loader invocation" >&2
    exit 1
  fi
  grep -Eq "Workcell blocked git hook bypass|Workcell blocked git control-plane override|Workcell blocked direct protected runtime execution" /tmp/git-guard-loader.out
  cp /usr/local/libexec/workcell/real/git "$EXEC_TMP/workcell-git-real-copy"
  chmod 0700 "$EXEC_TMP/workcell-git-real-copy"
  if "$EXEC_TMP/workcell-git-real-copy" status >/tmp/git-guard-real-copy.out 2>&1; then
    echo "expected renamed copy of the real git payload to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/git-guard-real-copy.out
  if "$LOADER" "$EXEC_TMP/workcell-git-real-copy" status >/tmp/git-guard-real-copy-loader.out 2>&1; then
    echo "expected loader invocation of a renamed real git copy to fail" >&2
    exit 1
  fi
  grep -q "Workcell blocked direct protected runtime execution" /tmp/git-guard-real-copy-loader.out
  mkdir -p "$EXEC_TMP/git-global-guard" && cd "$EXEC_TMP/git-global-guard"
  git init -q
  git config user.name "Workcell Smoke"
  git config user.email "workcell-smoke@example.com"
  mkdir -p .git/hooks
  cat >.git/hooks/pre-commit <<'EOF'
#!/usr/bin/env sh
echo "hook ran" >&2
exit 1
EOF
  chmod +x .git/hooks/pre-commit
  touch smoke.txt
  git add smoke.txt
  GLOBAL_HOME="$EXEC_TMP/git-global-home"
  mkdir -p "$GLOBAL_HOME"
  printf "[core]\n  hooksPath = /dev/null\n" >"$GLOBAL_HOME/.gitconfig"
  if HOME="$GLOBAL_HOME" git commit -m smoke >/tmp/git-guard-global-config.out 2>&1; then
    echo "expected Workcell git wrapper to ignore writable global git config" >&2
    exit 1
  fi
  grep -Eq "hook ran|pre-commit" /tmp/git-guard-global-config.out
  XDG_CONFIG_HOME="$EXEC_TMP/git-xdg-home"
  mkdir -p "$XDG_CONFIG_HOME/git"
  printf "[core]\n  hooksPath = /dev/null\n" >"$XDG_CONFIG_HOME/git/config"
  if XDG_CONFIG_HOME="$XDG_CONFIG_HOME" git commit -m smoke >/tmp/git-guard-xdg-config.out 2>&1; then
    echo "expected Workcell git wrapper to ignore writable XDG git config" >&2
    exit 1
  fi
  grep -Eq "hook ran|pre-commit" /tmp/git-guard-xdg-config.out
SCRIPT
)"

run_container claude bash -lc "$(
  cat <<'SCRIPT'
  /usr/local/bin/workcell-entrypoint claude --version >/dev/null
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    claude --version 2>&1 | grep -q "Claude Code"
    test -f "$HOME/.claude/settings.json"
    test -L "$HOME/.claude/settings.json"
    test "$(readlink "$HOME/.claude/settings.json")" = "/opt/workcell/adapters/claude/.claude/settings.json"
    test -f "$HOME/.mcp.json"
    test -L "$HOME/.mcp.json"
    test -f /etc/claude-code/managed-settings.json
    jq -r ".disableBypassPermissionsMode" /etc/claude-code/managed-settings.json | grep -q "^allow$"
    jq -r ".hooks.PreToolUse[0].hooks[0].command" "$HOME/.claude/settings.json" | grep -q "guard-bash.sh"
  '
  if claude --dangerously-skip-permissions >/tmp/claude-nested-danger.out 2>&1; then
    echo "expected nested Claude invocation to reject unsafe overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Claude override" /tmp/claude-nested-danger.out
  if claude --add-dir=/state --version >/tmp/claude-nested-add-dir.out 2>&1; then
    echo "expected nested Claude invocation to reject add-dir overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Claude override" /tmp/claude-nested-add-dir.out
  if claude --permission-mode default --version >/tmp/claude-nested-permission-mode.out 2>&1; then
    echo "expected nested Claude invocation to reject autonomy overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked Claude autonomy override" /tmp/claude-nested-permission-mode.out
  if claude update >/tmp/claude-nested-update.out 2>&1; then
    echo "expected nested Claude invocation to reject lifecycle updates" >&2
    exit 1
  fi
  grep -q "Workcell blocked Claude lifecycle command: update" /tmp/claude-nested-update.out
  if claude install >/tmp/claude-nested-install.out 2>&1; then
    echo "expected nested Claude invocation to reject lifecycle installs" >&2
    exit 1
  fi
  grep -q "Workcell blocked Claude lifecycle command: install" /tmp/claude-nested-install.out
  if WORKCELL_MODE=breakglass claude --dangerously-skip-permissions >/tmp/claude-nested-breakglass.out 2>&1; then
    echo "expected nested Claude invocation to ignore caller-supplied breakglass env" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Claude override" /tmp/claude-nested-breakglass.out
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    if (
      cat <<'\''EOF'\'' >"$HOME/.claude/settings.json"
{
  "disableBypassPermissionsMode": "allow"
}
EOF
    ) >/tmp/claude-managed-settings-overwrite.out 2>&1; then
      echo "expected managed Claude settings to remain protected" >&2
      exit 1
    fi
    claude --version >/dev/null 2>&1
    test -L "$HOME/.claude/settings.json"
    test "$(readlink "$HOME/.claude/settings.json")" = "/opt/workcell/adapters/claude/.claude/settings.json"
  '
  jq -n --arg cmd "bash -lc 'git commit -n -m smoke'" "{\"tool_input\":{\"command\":\$cmd}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-git.out 2>&1 && {
      echo "expected Claude guard hook to reject nested-shell git bypass" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-git.out
  printf "%s" "{\"tool_input\":{\"command\":\"claude --dangerously-skip-permissions\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-provider.out 2>&1 && {
      echo "expected Claude guard hook to reject nested Claude unsafe overrides" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-provider.out
  printf "%s" "{\"tool_input\":{\"command\":\"/usr/local/libexec/workcell/core/claude --dangerously-skip-permissions\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-provider-path.out 2>&1 && {
      echo "expected Claude guard hook to reject path-qualified nested provider launches" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-provider-path.out
  printf "%s" "{\"tool_input\":{\"command\":\"/usr/local/libexec/workcell/real/claude --dangerously-skip-permissions\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-provider-script-path.out 2>&1 && {
      echo "expected Claude guard hook to reject direct native Claude launches" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-provider-script-path.out
  printf "%b" "{\"tool_input\":{\"command\":\"c\\x24\\x27laude\\x27 --dangerously-skip-permissions\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-expansion.out 2>&1 && {
      echo "expected Claude guard hook to reject advanced shell expansion syntax" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-expansion.out
  printf "%s" "{\"tool_input\":{\"command\":\"c\\\\laude --dangerously-skip-permissions\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-backslash.out 2>&1 && {
      echo "expected Claude guard hook to reject backslash-obfuscated provider names" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-backslash.out
  jq -n --arg cmd "c'\''laude'\'' --dangerously-skip-permissions" "{\"tool_input\":{\"command\":\$cmd}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-quote-split.out 2>&1 && {
      echo "expected Claude guard hook to reject quote-split provider names" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-quote-split.out
  touch ./claude
  printf "%s" "{\"tool_input\":{\"command\":\"c* --dangerously-skip-permissions\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-glob.out 2>&1 && {
      echo "expected Claude guard hook to reject glob-expanded command names" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-glob.out
  rm -f ./claude
  cat >/tmp/claude-hook-positional.json <<'EOF'
{"tool_input":{"command":"set -- cl aude; \"$1$2\" --dangerously-skip-permissions"}}
EOF
  /opt/workcell/adapters/claude/hooks/guard-bash.sh </tmp/claude-hook-positional.json >/tmp/claude-hook-positional.out 2>&1 && {
      echo "expected Claude guard hook to reject positional-parameter provider reassembly" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-positional.out
  jq -n --arg cmd "printf foo\\ bar" "{\"tool_input\":{\"command\":\$cmd}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-safe-escape.out 2>&1 || {
      echo "expected Claude guard hook to allow ordinary shell escapes" >&2
      cat /tmp/claude-hook-safe-escape.out >&2
      exit 1
    }
  printf "%s" "{\"tool_input\":{\"command\":\"bash ./nested-script.sh\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-shell-script.out 2>&1 && {
      echo "expected Claude guard hook to reject nested shell script execution" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-shell-script.out
  printf "%s" "{\"tool_input\":{\"command\":\"source ./nested-script.sh\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-source-script.out 2>&1 && {
      echo "expected Claude guard hook to reject sourced shell scripts" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-source-script.out
  printf "%s" "{\"tool_input\":{\"command\":\"find . -type f | head -n 1\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-dot-arg.out 2>&1 || {
      echo "expected Claude guard hook to allow dot path arguments" >&2
      cat /tmp/claude-hook-dot-arg.out >&2
      exit 1
    }
  printf "%s" "{\"tool_input\":{\"command\":\"touch nested/.claude/settings.json\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-control-plane.out 2>&1 && {
      echo "expected Claude guard hook to reject control-plane path writes" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-control-plane.out
  printf "%s" "{\"tool_input\":{\"command\":\"git add AGENTS.md\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-control-plane-git-default.out 2>&1 && {
      echo "expected Claude guard hook to reject control-plane git staging without explicit opt-in" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-control-plane-git-default.out
  printf "%s" "{\"tool_input\":{\"command\":\"git add AGENTS.md\"}}" \
    | WORKCELL_ALLOW_CONTROL_PLANE_VCS=1 /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-control-plane-git-allowed.out 2>&1 || {
      echo "expected Claude guard hook to allow acknowledged control-plane git staging" >&2
      cat /tmp/claude-hook-control-plane-git-allowed.out >&2
      exit 1
    }
  printf "%s" "{\"tool_input\":{\"command\":\"cat AGENTS.md\"}}" \
    | WORKCELL_ALLOW_CONTROL_PLANE_VCS=1 /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-control-plane-read-still-blocked.out 2>&1 && {
      echo "expected Claude guard hook to keep plain control-plane reads blocked even with Git opt-in" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-control-plane-read-still-blocked.out
  printf "%s" "{\"tool_input\":{\"command\":\"git add AGENTS.md && cat AGENTS.md\"}}" \
    | WORKCELL_ALLOW_CONTROL_PLANE_VCS=1 /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-control-plane-compound.out 2>&1 && {
      echo "expected Claude guard hook to reject compound control-plane commands" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-control-plane-compound.out
  printf "%s" "{\"tool_input\":{\"command\":\"cat ~/.claude/settings.json\"}}" \
    | /opt/workcell/adapters/claude/hooks/guard-bash.sh >/tmp/claude-hook-home-control-plane.out 2>&1 && {
      echo "expected Claude guard hook to reject home control-plane access" >&2
      exit 1
    }
  grep -q "BLOCKED:" /tmp/claude-hook-home-control-plane.out
SCRIPT
)"

run_container gemini bash -lc "$(
  cat <<'SCRIPT'
  /usr/local/bin/workcell-entrypoint gemini --version >/dev/null
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    out="$(gemini --version 2>&1)"
    echo "$out"
    if echo "$out" | grep -q "Failed to save project registry"; then
      echo "unexpected Gemini project registry warning" >&2
      exit 1
    fi
    if echo "$out" | grep -q "There was an error saving your latest settings changes"; then
      echo "unexpected Gemini settings write warning" >&2
      exit 1
    fi
    echo "$out" | grep -Eq "([0-9]+\\.){2}[0-9]+"
    test -f "$HOME/.gemini/settings.json"
    test -f "$HOME/.gemini/GEMINI.md"
    test -f "$HOME/.gemini/projects.json"
  '
  if gemini --yolo >/tmp/gemini-nested-yolo.out 2>&1; then
    echo "expected nested Gemini invocation to reject unsafe overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Gemini override" /tmp/gemini-nested-yolo.out
  if gemini -y >/tmp/gemini-nested-yolo-short.out 2>&1; then
    echo "expected nested Gemini invocation to reject short yolo overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Gemini override" /tmp/gemini-nested-yolo-short.out
  if gemini --bypassPermissions --version >/tmp/gemini-nested-bypass-camel.out 2>&1; then
    echo "expected nested Gemini invocation to reject bypassPermissions-style overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Gemini override" /tmp/gemini-nested-bypass-camel.out
  if gemini --bypass-permissions --version >/tmp/gemini-nested-bypass-dashed.out 2>&1; then
    echo "expected nested Gemini invocation to reject bypass-permissions-style overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Gemini override" /tmp/gemini-nested-bypass-dashed.out
  if gemini --add-dir=/state --version >/tmp/gemini-nested-add-dir.out 2>&1; then
    echo "expected nested Gemini invocation to reject add-dir overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Gemini override" /tmp/gemini-nested-add-dir.out
  if gemini --approval-mode default --version >/tmp/gemini-nested-approval-mode.out 2>&1; then
    echo "expected nested Gemini invocation to reject autonomy overrides" >&2
    exit 1
  fi
  grep -q "Workcell blocked Gemini autonomy override" /tmp/gemini-nested-approval-mode.out
  if WORKCELL_MODE=breakglass gemini --yolo >/tmp/gemini-nested-breakglass.out 2>&1; then
    echo "expected nested Gemini invocation to ignore caller-supplied breakglass env" >&2
    exit 1
  fi
  grep -q "Workcell blocked unsafe Gemini override" /tmp/gemini-nested-breakglass.out
  NODE_EXTRA_CA_CERTS=/workspace/does-not-exist.pem gemini --version >/tmp/gemini-extra-ca.out 2>&1
  if grep -qi "extra cert" /tmp/gemini-extra-ca.out; then
    echo "expected provider wrapper to scrub NODE_EXTRA_CA_CERTS" >&2
    cat /tmp/gemini-extra-ca.out >&2
    exit 1
  fi
  rm -rf /workspace/.gemini
  HOME=/workspace gemini --version >/dev/null 2>&1
  test ! -e /workspace/.gemini/settings.json
  test ! -e /workspace/.gemini/projects.json
  setpriv --reuid "$WORKCELL_HOST_UID" --regid "$WORKCELL_HOST_GID" --init-groups bash -lc '
    set -euo pipefail
    jq -r ".general.enableAutoUpdate" "$HOME/.gemini/settings.json" | grep -q "^false$"
    jq -r ".general.enableAutoUpdateNotification" "$HOME/.gemini/settings.json" | grep -q "^false$"
    gemini --version >/dev/null 2>&1
    jq -r ".general.enableAutoUpdate" "$HOME/.gemini/settings.json" | grep -q "^false$"
    jq -r ".general.enableAutoUpdateNotification" "$HOME/.gemini/settings.json" | grep -q "^false$"
  '
SCRIPT
)"

echo "Workcell container smoke passed."
