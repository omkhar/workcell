# shellcheck shell=bash
resolve_workcell_real_home() {
  local python_bin

  if [[ -x /usr/bin/python3 ]]; then
    python_bin=/usr/bin/python3
  else
    python_bin=python3
  fi

  "${python_bin}" - <<'PY'
import os
import pwd

print(pwd.getpwuid(os.getuid()).pw_dir)
PY
}

copy_workcell_docker_state_tree() {
  local source_dir="$1"
  local destination_dir="$2"

  if [[ ! -d "${source_dir}" ]]; then
    return
  fi

  mkdir -p "${destination_dir}"
  cp -R "${source_dir}/." "${destination_dir}/"
}

select_workcell_trusted_buildx() {
  local candidate

  for candidate in \
    /Applications/Docker.app/Contents/Resources/cli-plugins/docker-buildx \
    /opt/homebrew/lib/docker/cli-plugins/docker-buildx \
    /usr/local/lib/docker/cli-plugins/docker-buildx \
    /usr/local/libexec/docker/cli-plugins/docker-buildx \
    /usr/lib/docker/cli-plugins/docker-buildx \
    /usr/libexec/docker/cli-plugins/docker-buildx; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done

  return 1
}

setup_workcell_trusted_docker_client() {
  local real_home

  real_home="$(resolve_workcell_real_home)"
  WORKCELL_DOCKER_SANDBOX_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-docker.XXXXXX")"
  WORKCELL_DOCKER_HOME="${WORKCELL_DOCKER_SANDBOX_ROOT}/home"
  WORKCELL_DOCKER_CONFIG="${WORKCELL_DOCKER_SANDBOX_ROOT}/config"
  mkdir -p "${WORKCELL_DOCKER_HOME}" "${WORKCELL_DOCKER_CONFIG}"

  copy_workcell_docker_state_tree "${real_home}/.docker/contexts" "${WORKCELL_DOCKER_CONFIG}/contexts"
  copy_workcell_docker_state_tree "${real_home}/.docker/buildx" "${WORKCELL_DOCKER_CONFIG}/buildx"
  rm -rf "${WORKCELL_DOCKER_CONFIG}/cli-plugins"

  export HOME="${WORKCELL_DOCKER_HOME}"
  export DOCKER_CONFIG="${WORKCELL_DOCKER_CONFIG}"
  unset DOCKER_CLI_PLUGIN_EXTRA_DIRS
}

cleanup_workcell_trusted_docker_client() {
  if [[ -n "${WORKCELL_DOCKER_SANDBOX_ROOT:-}" ]] && [[ -d "${WORKCELL_DOCKER_SANDBOX_ROOT}" ]]; then
    rm -rf "${WORKCELL_DOCKER_SANDBOX_ROOT}"
  fi
}

ensure_workcell_trusted_buildx() {
  if [[ -n "${WORKCELL_TRUSTED_BUILDX_BIN:-}" ]] && [[ -x "${WORKCELL_TRUSTED_BUILDX_BIN}" ]]; then
    return
  fi

  WORKCELL_TRUSTED_BUILDX_BIN="$(select_workcell_trusted_buildx)" || {
    echo "Missing trusted docker-buildx binary in a system plugin directory" >&2
    exit 1
  }
}

docker_context_exists() {
  local context_name="$1"
  docker context inspect "${context_name}" >/dev/null 2>&1
}

docker_context_is_healthy() {
  local context_name="$1"
  docker --context "${context_name}" info >/dev/null 2>&1
}

select_workcell_docker_context() {
  local explicit_error_prefix="$1"
  local missing_error_prefix="$2"
  shift 2

  local candidate=""
  local attempted=""

  if [[ "$#" -eq 0 ]]; then
    set -- colima default
  fi

  if [[ -n "${DOCKER_CONTEXT_NAME:-}" ]]; then
    if docker_context_exists "${DOCKER_CONTEXT_NAME}" && docker_context_is_healthy "${DOCKER_CONTEXT_NAME}"; then
      return 0
    fi

    echo "${explicit_error_prefix} '${DOCKER_CONTEXT_NAME}' is not healthy" >&2
    exit 1
  fi

  for candidate in "$@"; do
    if [[ -n "${attempted}" ]]; then
      attempted+=", "
    fi
    attempted+="${candidate}"

    if docker_context_exists "${candidate}" && docker_context_is_healthy "${candidate}"; then
      DOCKER_CONTEXT_NAME="${candidate}"
      return 0
    fi
  done

  echo "${missing_error_prefix} (tried: ${attempted})" >&2
  exit 1
}

buildx_cmd() {
  ensure_workcell_trusted_buildx
  if [[ -n "${DOCKER_CONTEXT_NAME:-}" ]]; then
    DOCKER_CONTEXT="${DOCKER_CONTEXT_NAME}" "${WORKCELL_TRUSTED_BUILDX_BIN}" "$@"
  else
    "${WORKCELL_TRUSTED_BUILDX_BIN}" "$@"
  fi
}

prepare_workcell_buildkitd_config() {
  local config_path=""
  local cert_root=""
  local cert_bundle=""
  local cert_file=""
  local cert_name=""
  local -a ca_paths=()

  [[ -n "${WORKCELL_REMOTE_BUILDKIT_SSL_CERTS:-}" ]] || return 0
  [[ -d "${WORKCELL_REMOTE_BUILDKIT_SSL_CERTS}" ]] || return 0
  [[ -n "${WORKCELL_DOCKER_SANDBOX_ROOT:-}" ]] || return 0

  cert_root="${WORKCELL_DOCKER_SANDBOX_ROOT}/buildkit-certs/docker.io"
  config_path="${WORKCELL_DOCKER_SANDBOX_ROOT}/buildkitd.toml"
  mkdir -p "${cert_root}"

  cert_bundle="${WORKCELL_REMOTE_BUILDKIT_SSL_CERTS}/ca-certificates.crt"
  if [[ -f "${cert_bundle}" ]]; then
    cp "${cert_bundle}" "${cert_root}/ca-certificates.crt"
    ca_paths+=("${cert_root}/ca-certificates.crt")
  fi

  if [[ -n "${WORKCELL_REMOTE_BUILDKIT_LOCAL_CA:-}" ]] && [[ -d "${WORKCELL_REMOTE_BUILDKIT_LOCAL_CA}" ]]; then
    while IFS= read -r -d '' cert_file; do
      cert_name="$(basename "${cert_file}")"
      cp "${cert_file}" "${cert_root}/${cert_name}"
      ca_paths+=("${cert_root}/${cert_name}")
    done < <(find "${WORKCELL_REMOTE_BUILDKIT_LOCAL_CA}" -type f -name '*.crt' -print0 | sort -z)
  fi

  if [[ "${#ca_paths[@]}" -eq 0 ]]; then
    return 0
  fi

  {
    printf '[registry."docker.io"]\n'
    printf '  ca = ['
    local first=1
    for cert_file in "${ca_paths[@]}"; do
      if [[ "${first}" -eq 0 ]]; then
        printf ', '
      fi
      first=0
      printf '"%s"' "${cert_file}"
    done
    printf ']\n'
    printf '[registry."registry-1.docker.io"]\n'
    printf '  ca = ['
    first=1
    for cert_file in "${ca_paths[@]}"; do
      if [[ "${first}" -eq 0 ]]; then
        printf ', '
      fi
      first=0
      printf '"%s"' "${cert_file}"
    done
    printf ']\n'
  } >"${config_path}"

  printf '%s\n' "${config_path}"
}

ensure_workcell_selected_builder() {
  local builder_name="${BUILDX_BUILDER:-}"
  local buildkitd_config=""

  [[ -n "${builder_name}" ]] || return 0
  if buildx_cmd inspect "${builder_name}" >/dev/null 2>&1; then
    :
  else
    buildkitd_config="$(prepare_workcell_buildkitd_config || true)"
    if [[ -n "${buildkitd_config}" ]]; then
      buildx_cmd create \
        --driver docker-container \
        --buildkitd-config "${buildkitd_config}" \
        --name "${builder_name}" \
        --use >/dev/null
    else
      buildx_cmd create --driver docker-container --name "${builder_name}" --use >/dev/null
    fi
  fi

  buildx_cmd inspect "${builder_name}" --bootstrap >/dev/null
}

workcell_docker_host_path() {
  local path="$1"
  local workspace_root="${WORKCELL_DOCKER_HOST_WORKSPACE_ROOT:-}"
  local home_root="${WORKCELL_DOCKER_HOST_HOME_ROOT:-}"

  if [[ -n "${workspace_root}" ]] && [[ -n "${ROOT_DIR:-}" ]]; then
    case "${path}" in
      "${ROOT_DIR}")
        printf '%s\n' "${workspace_root}"
        return 0
        ;;
      "${ROOT_DIR}"/*)
        printf '%s%s\n' "${workspace_root}" "${path#"${ROOT_DIR}"}"
        return 0
        ;;
    esac
  fi

  if [[ -n "${home_root}" ]] && [[ -n "${HOME:-}" ]]; then
    case "${path}" in
      "${HOME}")
        printf '%s\n' "${home_root}"
        return 0
        ;;
      "${HOME}"/*)
        printf '%s%s\n' "${home_root}" "${path#"${HOME}"}"
        return 0
        ;;
    esac
  fi

  printf '%s\n' "${path}"
}
