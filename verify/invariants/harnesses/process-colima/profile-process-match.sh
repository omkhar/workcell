set -euo pipefail

HARNESS_BIN="$(mktemp -d)"
trap 'rm -rf "${HARNESS_BIN}"' EXIT

cat >"${HARNESS_BIN}/pgrep" <<'PGREP'
#!/bin/sh
printf '49909\n49991\n60000\n'
PGREP
cat >"${HARNESS_BIN}/ps" <<'PS'
#!/bin/sh
case "$2" in
  49909)
    printf '%s\n' '/opt/homebrew/bin/limactl hostagent --pidfile /Users/omkharanarasaratnam/.colima/_lima/colima-workcell-workcell-ac42b1dc/ha.pid --socket /Users/omkharanarasaratnam/.colima/_lima/colima-workcell-workcell-ac42b1dc/ha.sock --guestagent /opt/homebrew/share/lima/lima-guestagent.Linux-aarch64.gz colima-workcell-workcell-ac42b1dc'
    ;;
  49991)
    printf '%s\n' 'ssh: /Users/omkharanarasaratnam/.colima/_lima/colima-workcell-workcell-ac42b1dc/ssh.sock [mux]'
    ;;
  60000)
    printf '%s\n' '/opt/homebrew/bin/limactl hostagent --pidfile /Users/omkharanarasaratnam/.colima/_lima/colima-other/ha.pid --socket /Users/omkharanarasaratnam/.colima/_lima/colima-other/ha.sock colima-other'
    ;;
esac
PS
chmod +x "${HARNESS_BIN}/pgrep" "${HARNESS_BIN}/ps"

PATH="${HARNESS_BIN}:${PATH}"
matched="$(profile_process_pids workcell-workcell-ac42b1dc | tr '\n' ' ' | sed 's/[[:space:]]*$//')"
if [[ "${matched}" != "49909 49991" ]]; then
  echo "Expected profile_process_pids to return the stale hostagent and ssh mux, got: ${matched}" >&2
  exit 1
fi
