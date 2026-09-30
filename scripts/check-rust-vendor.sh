#!/bin/bash -p
# Rebuilds the vendored Rust crates from crates.io against Cargo.lock and
# rejects any difference. Cargo checks a directory source only against the
# .cargo-checksum.json beside the code, so an edited crate with regenerated
# hashes builds. crates.io serves each .crate by its Cargo.lock checksum, so
# this comparison binds the committed bytes to the lock.
# shellcheck source=scripts/lib/trusted-entrypoint.sh
# shellcheck disable=SC2312 # repo-wide bootstrap; the path is the running script's own directory
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "check-rust-vendor-entrypoint-ok"
  exit 0
fi

# Residual risk: cargo comes from the caller's HOME (as in build-and-test.sh),
# and the link checks below do not stop a same-user process that races them.
# This check guards the committed tree against a pull request, not the host
# user against themselves.
export PATH="${HOME}/.cargo/bin:${PATH}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUST_DIR="${ROOT_DIR}/runtime/container/rust"

# Reject links before any read, so the check binds the committed tree and not a
# link target. Assignments stop the script under set -e when a scan fails.
for part in runtime runtime/container runtime/container/rust runtime/container/rust/vendor; do
  if [[ -L "${ROOT_DIR}/${part}" ]]; then
    echo "${part} is a symbolic link" >&2
    exit 1
  fi
done
odd="$(find "${RUST_DIR}/vendor" ! -type f ! -type d)"
if [[ -n "${odd}" ]]; then
  echo "vendor tree contains a link or special file: ${odd}" >&2
  exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
fresh="${tmp}/vendor"
committed="${tmp}/committed"
# The committed .cargo/config.toml replaces crates-io with vendor/, so vendor
# from a copy that has no such override.
mkdir "${tmp}/src" "${committed}"
tar -C "${RUST_DIR}" --exclude=./vendor --exclude=./.cargo --exclude=./target -cf - . | tar -C "${tmp}/src" -xf -
tar -C "${RUST_DIR}/vendor" -cf - . | tar -C "${committed}" -xf -
(cd "${tmp}/src" && cargo vendor --locked "${fresh}" >/dev/null)

# Newer cargo adds a "$comment" key to .cargo-checksum.json. Drop that key from
# every such file in both trees, then compare the trees byte for byte. A
# difference in that one key is the only difference the check accepts.
for tree in "${fresh}" "${committed}"; do
  find "${tree}" -name .cargo-checksum.json -exec sh -c '
    for f; do jq -S "del(.\"\$comment\")" "$f" >"$0" && cp "$0" "$f" || exit 1; done
  ' "${tmp}/norm.json" {} +
done
if ! diff -r "${fresh}" "${committed}"; then
  echo "runtime/container/rust/vendor differs from crates.io for the pinned Cargo.lock" >&2
  exit 1
fi
echo "Rust vendor tree matches crates.io."
