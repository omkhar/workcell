#!/bin/bash -p
# Stage the go.mod toolchain at a root-owned /usr/local/go on a GitHub-hosted
# runner. The runner image makes /opt, and with it the tool cache that its
# /usr/bin/go points into, world-writable, so resolve_exact_toolchain_go_bin in
# scripts/lib/go-run-env.sh refuses every go there. The checksum-verified
# GOTOOLCHAIN switch fetches the exact toolchain; the copy is then owned by
# root and writable by no one else. The runner VM is single-use, so the go
# that drives the switch is trusted for this one fetch only.
set -euo pipefail

ROOT_DIR="$(CDPATH='' cd -- "${BASH_SOURCE[0]%/*}/../.." && pwd -P)"
target=/usr/local/go
toolchain="$(/usr/bin/awk '$1 == "toolchain" { print $2; exit }' "${ROOT_DIR}/go.mod")"
[[ "${toolchain}" =~ ^go[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  echo "go.mod must declare one exact Go toolchain." >&2
  exit 1
}
if [[ -e "${target}" ]]; then
  echo "${target} already exists; refusing to replace it." >&2
  exit 1
fi
# require_toolchain GO ends the script unless GO runs and reports the
# go.mod toolchain.
require_toolchain() {
  local version=""
  if ! version="$(GOTOOLCHAIN=local "$1" env GOVERSION)"; then
    exit 1
  fi
  if [[ "${version}" != "${toolchain}" ]]; then
    echo "The toolchain at $1 is not ${toolchain}." >&2
    exit 1
  fi
}
if ! goroot="$(GOTOOLCHAIN="${toolchain}" go env GOROOT)"; then
  exit 1
fi
require_toolchain "${goroot}/bin/go"
# Build and check the tree in a directory beside the target that only root
# can enter, then rename it into place, so a partial copy is never found at
# the target. cp -a keeps the runner's ownership, so the directory opens to
# the runner (mktemp makes it 0700) only after the tree is root's and
# read-only, for the version check.
stage="$(sudo mktemp -d "${target%/*}/.go-stage.XXXXXX")"
sudo cp -a "${goroot}" "${stage}/go"
sudo chown -R root:root "${stage}/go"
sudo chmod -R go-w "${stage}/go"
sudo chmod 0755 "${stage}"
require_toolchain "${stage}/go/bin/go"
sudo sync
sudo mv -T "${stage}/go" "${target}"
sudo rmdir "${stage}"
sudo sync
require_toolchain "${target}/bin/go"
echo "Staged ${toolchain} at ${target}."
