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

export PATH="${HOME}/.cargo/bin:${PATH}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUST_DIR="${ROOT_DIR}/runtime/container/rust"

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
fresh="${tmp}/vendor"
# The committed .cargo/config.toml replaces crates-io with vendor/, so vendor
# from a copy that has no such override.
mkdir "${tmp}/src"
tar -C "${RUST_DIR}" --exclude=./vendor --exclude=./.cargo --exclude=./target -cf - . | tar -C "${tmp}/src" -xf -
(cd "${tmp}/src" && cargo vendor --locked "${fresh}" >/dev/null)

# Newer cargo adds a "$comment" key to .cargo-checksum.json, so compare that
# file without it and every other file byte for byte. The exclusion matches
# that name at any depth, so compare the ones at the vendor root and below a
# crate root apart.
nested_checksums() {
  (cd "$1" && {
    find . -maxdepth 1 -name .cargo-checksum.json -exec shasum -a 256 {} +
    find . -mindepth 3 -name .cargo-checksum.json -exec shasum -a 256 {} +
  } | sort -k2)
}
mismatch=0
# diff follows symlinks, so reject them before it runs. Assignments stop the
# script under set -e when a scan fails, so an unreadable tree cannot pass.
links="$(find "${RUST_DIR}/vendor" -type l)"
if [[ -n "${links}" ]]; then
  echo "vendor tree contains a symbolic link" >&2
  exit 1
fi
diff -r --exclude=.cargo-checksum.json "${fresh}" "${RUST_DIR}/vendor" || mismatch=1
nested_fresh="$(nested_checksums "${fresh}")"
nested_committed="$(nested_checksums "${RUST_DIR}/vendor")"
[[ "${nested_fresh}" == "${nested_committed}" ]] || mismatch=1
for sum in "${fresh}"/*/.cargo-checksum.json; do
  crate="$(basename "$(dirname "${sum}")")"
  want="$(jq -S 'del(."$comment")' "${sum}")" || want=""
  have="$(jq -S 'del(."$comment")' "${RUST_DIR}/vendor/${crate}/.cargo-checksum.json")" || have=""
  if [[ -z "${want}" || "${want}" != "${have}" ]]; then
    echo "checksum file differs or is unreadable: ${crate}" >&2
    mismatch=1
  fi
done
if [[ "${mismatch}" -ne 0 ]]; then
  echo "runtime/container/rust/vendor differs from crates.io for the pinned Cargo.lock" >&2
  exit 1
fi
echo "Rust vendor tree matches crates.io."
