# shellcheck shell=bash
resolve_workcell_real_home() {
  local uid user home

  uid="$(id -u)"
  user="$(id -un)"

  if command -v getent >/dev/null 2>&1; then
    home="$(getent passwd "${uid}" | awk -F: 'NR==1 {print $6}')"
  fi
  if [[ -z "${home}" ]] && command -v dscl >/dev/null 2>&1; then
    home="$(dscl . -read "/Users/${user}" NFSHomeDirectory 2>/dev/null | awk '{print $2}')"
  fi
  if [[ -z "${home}" && -r /etc/passwd ]]; then
    home="$(awk -F: -v uid="${uid}" '$3 == uid {print $6; exit}' /etc/passwd)"
  fi

  if [[ -z "${home}" ]]; then
    echo "Unable to resolve real home directory for uid ${uid}" >&2
    return 1
  fi

  printf '%s\n' "${home}"
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

sanitize_workcell_docker_buildx_state() {
  local buildx_dir="$1"

  [[ -d "${buildx_dir}" ]] || return 0
  # Buildx refs cache prior local build roots and Dockerfile paths. Reusing
  # them inside a remapped Docker/Colima context can emit stale host-path cd
  # failures before the real build starts.
  rm -rf "${buildx_dir}/refs"
}

run_workcell_docker_client_command() {
  local safe_cwd="${WORKCELL_DOCKER_CLIENT_CWD:-${HOME:-/}}"

  [[ "$#" -gt 0 ]] || return 0
  if [[ ! -d "${safe_cwd}" ]]; then
    safe_cwd="/"
  fi

  (
    cd "${safe_cwd}" &&
      "$@"
  )
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
  sanitize_workcell_docker_buildx_state "${WORKCELL_DOCKER_CONFIG}/buildx"
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
  run_workcell_docker_client_command docker context inspect "${context_name}" >/dev/null 2>&1
}

docker_context_is_healthy() {
  local context_name="$1"
  run_workcell_docker_client_command docker --context "${context_name}" info >/dev/null 2>&1
}

docker_context_names() {
  run_workcell_docker_client_command docker context ls --format '{{.Name}}' 2>/dev/null || true
}

select_workcell_docker_context() {
  local explicit_error_prefix="$1"
  local missing_error_prefix="$2"
  shift 2

  local candidate=""
  local attempted=""
  local attempted_candidate=""
  local already_attempted=0

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

  while IFS= read -r candidate; do
    [[ -n "${candidate}" ]] || continue
    already_attempted=0
    for attempted_candidate in "$@"; do
      if [[ "${attempted_candidate}" == "${candidate}" ]]; then
        already_attempted=1
        break
      fi
    done
    if [[ "${already_attempted}" -eq 1 ]]; then
      continue
    fi
    if [[ -n "${attempted}" ]]; then
      attempted+=", "
    fi
    attempted+="${candidate}"
    if docker_context_exists "${candidate}" && docker_context_is_healthy "${candidate}"; then
      DOCKER_CONTEXT_NAME="${candidate}"
      return 0
    fi
  done < <(docker_context_names)

  echo "${missing_error_prefix} (tried: ${attempted})" >&2
  exit 1
}

buildx_cmd() {
  ensure_workcell_trusted_buildx
  if [[ -n "${DOCKER_CONTEXT_NAME:-}" ]]; then
    run_workcell_docker_client_command env DOCKER_CONTEXT="${DOCKER_CONTEXT_NAME}" "${WORKCELL_TRUSTED_BUILDX_BIN}" "$@"
  else
    run_workcell_docker_client_command "${WORKCELL_TRUSTED_BUILDX_BIN}" "$@"
  fi
}

buildx_expected_endpoints() {
  local context_endpoint=""

  if [[ -n "${DOCKER_CONTEXT_NAME:-}" ]]; then
    printf '%s\n' "${DOCKER_CONTEXT_NAME}"
    context_endpoint="$(run_workcell_docker_client_command \
      docker context inspect "${DOCKER_CONTEXT_NAME}" --format '{{.Endpoints.docker.Host}}' 2>/dev/null || true)"
    if [[ -n "${context_endpoint}" ]] && [[ "${context_endpoint}" != "${DOCKER_CONTEXT_NAME}" ]]; then
      printf '%s\n' "${context_endpoint}"
    fi
    return 0
  fi

  if [[ -n "${DOCKER_HOST:-}" ]]; then
    printf '%s\n' "${DOCKER_HOST}"
  fi
}

buildx_builder_matches_context() {
  local inspect_output_path="$1"
  shift
  local line=""
  local endpoint=""
  local expected_endpoint=""

  [[ "$#" -gt 0 ]] || return 0
  while IFS= read -r line; do
    case "${line}" in
      Endpoint:*)
        endpoint="${line#Endpoint: }"
        ;;
      *)
        continue
        ;;
    esac
    [[ -n "${endpoint}" ]] || continue
    for expected_endpoint in "$@"; do
      [[ -n "${expected_endpoint}" ]] || continue
      if [[ "${endpoint}" == "${expected_endpoint}" ]]; then
        return 0
      fi
    done
  done <"${inspect_output_path}"

  return 1
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
  local expected_endpoint=""
  local inspect_output=""
  local recreate_builder=0
  local -a expected_endpoints=()

  [[ -n "${builder_name}" ]] || return 0
  while IFS= read -r expected_endpoint; do
    [[ -n "${expected_endpoint}" ]] && expected_endpoints+=("${expected_endpoint}")
  done < <(buildx_expected_endpoints)
  inspect_output="$(mktemp "${TMPDIR:-/tmp}/workcell-buildx-inspect.XXXXXX")"
  if buildx_cmd inspect "${builder_name}" >"${inspect_output}" 2>&1; then
    if ! buildx_builder_matches_context "${inspect_output}" "${expected_endpoints[@]}"; then
      recreate_builder=1
    fi
  elif grep -Eq '^Name:|^Nodes:' "${inspect_output}"; then
    recreate_builder=1
  fi
  rm -f "${inspect_output}"

  if [[ "${recreate_builder}" -eq 1 ]]; then
    buildx_cmd rm --force "${builder_name}" >/dev/null 2>&1 || true
  fi

  if [[ "${recreate_builder}" -eq 1 ]] || ! buildx_cmd inspect "${builder_name}" >/dev/null 2>&1; then
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
