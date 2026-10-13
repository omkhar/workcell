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
goroot="$(GOTOOLCHAIN="${toolchain}" go env GOROOT)"
[[ "$(GOTOOLCHAIN=local "${goroot}/bin/go" env GOVERSION)" == "${toolchain}" ]] || {
  echo "The fetched toolchain at ${goroot} is not ${toolchain}." >&2
  exit 1
}
# Build and check the tree in a directory beside the target that only root
# can write, then rename it into place, so a partial copy is never found at
# the target. mktemp makes it 0700; the runner user must read it to check.
stage="$(sudo mktemp -d "${target%/*}/.go-stage.XXXXXX")"
sudo chmod 0755 "${stage}"
sudo cp -a "${goroot}" "${stage}/go"
sudo chown -R root:root "${stage}/go"
sudo chmod -R go-w "${stage}/go"
[[ "$(GOTOOLCHAIN=local "${stage}/go/bin/go" env GOVERSION)" == "${toolchain}" ]]
sudo sync
sudo mv -T "${stage}/go" "${target}"
sudo rmdir "${stage}"
sudo sync
[[ "$(GOTOOLCHAIN=local "${target}/bin/go" env GOVERSION)" == "${toolchain}" ]]
echo "Staged ${toolchain} at ${target}."
