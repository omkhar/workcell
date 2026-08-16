set -euo pipefail

ROOT_DIR="__ROOT_DIR__"
TRUSTED_HOST_PATH="${PATH}"
PROFILE_STATUS_TMPDIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-colima-profile-status-harness.XXXXXX")"
trap 'rm -rf -- "${PROFILE_STATUS_TMPDIR}"' EXIT
export TMPDIR="${PROFILE_STATUS_TMPDIR}"

# Match scripts/workcell's scrubbed repo-root go_hostutil invocation.
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

go_hostutil() {
  local host_go_bin=""

  ensure_go_run_env
  host_go_bin="$(resolve_go_bin)"
  (
    cd "${ROOT_DIR}" &&
      env -i \
        PATH="${TRUSTED_HOST_PATH}" \
        HOME=/tmp \
        LC_ALL=C \
        LANG=C \
        GOPATH="${GOPATH}" \
        GOMODCACHE="${GOMODCACHE}" \
        GOCACHE="${GOCACHE}" \
        "${host_go_bin}" run ./cmd/workcell-hostutil "$@"
  )
}

assert_no_profile_status_temp_files() {
  local residue=""

  residue="$(find "${PROFILE_STATUS_TMPDIR}" -type f -name 'workcell-colima-profile-status.*' -print -quit)"
  if [[ -n "${residue}" ]]; then
    echo "Expected colima_profile_status to remove its temporary output: ${residue}" >&2
    exit 1
  fi
}

run_host_colima() {
  cat <<'JSON'
{"name":"default","status":"Running"}
{"name":"workcell-workcell-ac42b1dc","status":"Stopped"}
{"name":"workcell-other","status":"Running"}
JSON
}

reap_stale_profile_processes() {
  printf 'reaped:%s\n' "$1"
}

profile_process_pids() {
  case "$1" in
    workcell-stale)
      printf '%s\n' 49909
      ;;
    workcell-empty-list)
      printf '%s\n' 49919
      ;;
    workcell-parse-failure)
      printf '%s\n' 49991
      ;;
  esac
}

status="$(colima_profile_status workcell-workcell-ac42b1dc)"
if [[ "${status}" != "Stopped" ]]; then
  echo "Expected colima_profile_status to return Stopped for the matching profile, got: ${status}" >&2
  exit 1
fi
assert_no_profile_status_temp_files

status="$(colima_profile_status workcell-other)"
if [[ "${status}" != "Running" ]]; then
  echo "Expected colima_profile_status to return Running for the matching profile, got: ${status}" >&2
  exit 1
fi
assert_no_profile_status_temp_files

missing_status_rc=0
if colima_profile_status does-not-exist >"${PROFILE_STATUS_TMPDIR}/missing.out" 2>&1; then
  echo "Expected colima_profile_status to fail for a missing profile" >&2
  exit 1
else
  missing_status_rc=$?
fi
if ((missing_status_rc != 3)); then
  echo "Expected colima_profile_status to return exit status 3 for a missing profile, got: ${missing_status_rc}" >&2
  exit 1
fi
assert_no_profile_status_temp_files

reaped="$(maybe_reap_stale_profile_processes workcell-workcell-ac42b1dc)"
if [[ "${reaped}" != "reaped:workcell-workcell-ac42b1dc" ]]; then
  echo "Expected maybe_reap_stale_profile_processes to reap only explicit Stopped profiles, got: ${reaped}" >&2
  exit 1
fi

reap_stale_profile_processes() { return 23; }
if maybe_reap_stale_profile_processes workcell-workcell-ac42b1dc; then
  echo "Expected maybe_reap_stale_profile_processes to propagate reaper failure" >&2
  exit 1
elif [[ "$?" -ne 23 ]]; then
  echo "Expected maybe_reap_stale_profile_processes to preserve reaper status 23" >&2
  exit 1
fi

reap_stale_profile_processes() {
  printf 'reaped:%s\n' "$1"
}

if [[ -n "$(maybe_reap_stale_profile_processes workcell-other)" ]]; then
  echo "Expected maybe_reap_stale_profile_processes to ignore Running profiles" >&2
  exit 1
fi

reaped="$(maybe_reap_stale_profile_processes workcell-stale)"
if [[ "${reaped}" != "reaped:workcell-stale" ]]; then
  echo "Expected maybe_reap_stale_profile_processes to reap missing profiles that still have orphaned processes, got: ${reaped}" >&2
  exit 1
fi

run_host_colima() {
  return 0
}

reaped="$(maybe_reap_stale_profile_processes workcell-empty-list)"
if [[ "${reaped}" != "reaped:workcell-empty-list" ]]; then
  echo "Expected maybe_reap_stale_profile_processes to reap orphaned processes when colima list returns an empty profile set, got: ${reaped}" >&2
  exit 1
fi

run_host_colima() {
  printf '%s\n' '{not-json'
}

if [[ -n "$(maybe_reap_stale_profile_processes workcell-parse-failure)" ]]; then
  echo "Expected maybe_reap_stale_profile_processes to ignore parse failures instead of reaping live profiles blindly" >&2
  exit 1
fi
assert_no_profile_status_temp_files

FAILED_INVENTORY_STATUS=""
FAILED_INVENTORY_REAP_LOG="$(mktemp "${PROFILE_STATUS_TMPDIR}/reap-log.XXXXXX")"
run_host_colima() {
  printf '{"name":"workcell-fail-after-output","status":"%s"}\n' "${FAILED_INVENTORY_STATUS}"
  return 17
}
reap_stale_profile_processes() {
  printf 'reaped:%s\n' "$1" >>"${FAILED_INVENTORY_REAP_LOG}"
}

for FAILED_INVENTORY_STATUS in Running Stopped; do
  status=""
  status_rc=0
  if status="$(colima_profile_status workcell-fail-after-output)"; then
    echo "Expected failed ${FAILED_INVENTORY_STATUS} inventory to fail closed" >&2
    exit 1
  else
    status_rc=$?
  fi
  if ((status_rc != 1)); then
    echo "Expected failed ${FAILED_INVENTORY_STATUS} inventory status 1, got: ${status_rc}" >&2
    exit 1
  fi
  if [[ -n "${status}" ]]; then
    echo "Expected failed ${FAILED_INVENTORY_STATUS} inventory stdout to be empty, got: ${status}" >&2
    exit 1
  fi
  : >"${FAILED_INVENTORY_REAP_LOG}"
  maybe_reap_stale_profile_processes workcell-fail-after-output
  if [[ -s "${FAILED_INVENTORY_REAP_LOG}" ]]; then
    echo "Expected failed ${FAILED_INVENTORY_STATUS} inventory not to trigger reaping" >&2
    exit 1
  fi
  assert_no_profile_status_temp_files
done
