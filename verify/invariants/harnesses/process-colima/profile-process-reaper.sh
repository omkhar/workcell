set -euo pipefail

go_hostutil() {
  if [[ "$*" != "helper reap-colima-profile-processes workcell-test" ]]; then
    echo "Unexpected profile reaper invocation: $*" >&2
    return 1
  fi
  printf '%s\n' routed
}

result="$(reap_stale_profile_processes workcell-test)"
if [[ "${result}" != "routed" ]]; then
  echo "Expected stale profile cleanup to route through workcell-hostutil, got: ${result}" >&2
  exit 1
fi
