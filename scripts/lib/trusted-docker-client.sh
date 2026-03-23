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

buildx_cmd() {
  ensure_workcell_trusted_buildx
  if [[ -n "${DOCKER_CONTEXT_NAME:-}" ]]; then
    DOCKER_CONTEXT="${DOCKER_CONTEXT_NAME}" "${WORKCELL_TRUSTED_BUILDX_BIN}" "$@"
  else
    "${WORKCELL_TRUSTED_BUILDX_BIN}" "$@"
  fi
}
