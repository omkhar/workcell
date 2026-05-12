#!/bin/bash
set -euo pipefail
if [[ "${1:-}" != "install" ]]; then
  echo "Expected only brew install during installer dependency bootstrap" >&2
  exit 1
fi
shift
printf 'install %s\n' "$*" >"${INSTALL_DEPS_LOG}"
for pkg in "$@"; do
  cat <<'EOFAKE' >"${INSTALL_DEPS_FAKEBIN}/${pkg}"
#!/bin/bash
set -euo pipefail
exit 0
EOFAKE
  chmod 0755 "${INSTALL_DEPS_FAKEBIN}/${pkg}"
done
