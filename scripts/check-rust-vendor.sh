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

# Cargo follows links inside a Git dependency and copies their targets into the
# vendor output, so accept only crates.io sources from the lock file.
foreign="$(awk '/^source = / && $0 != "source = \"registry+https://github.com/rust-lang/crates.io-index\"" { print }' "${RUST_DIR}/Cargo.lock")"
if [[ -n "${foreign}" ]]; then
  echo "Cargo.lock names a source other than crates.io: ${foreign}" >&2
  exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT
fresh="${tmp}/vendor"
committed="${tmp}/committed"
# The committed .cargo/config.toml replaces crates-io with vendor/, so vendor
# from a copy that has no such override.
mkdir "${tmp}/src" "${committed}"
# Leave out rust-toolchain files too: a pull request could point one at a
# program to run, so cargo uses the host default toolchain.
tar -C "${RUST_DIR}" --exclude=./vendor --exclude=./.cargo --exclude=./target \
  --exclude=./rust-toolchain --exclude=./rust-toolchain.toml -cf - . | tar -C "${tmp}/src" -xf -
tar -C "${RUST_DIR}/vendor" -cf - . | tar -C "${committed}" -xf -
# A pull request controls the sources in Cargo.lock, so keep host Cargo, Git
# and SSH state out of the fetch: a scratch CARGO_HOME and no Git configuration.
(cd "${tmp}/src" && RUSTUP_HOME="${RUSTUP_HOME:-${HOME}/.rustup}" CARGO_HOME="${tmp}/cargo-home" \
  GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null GIT_CONFIG_NOSYSTEM=1 \
  GIT_ALLOW_PROTOCOL=https GIT_TERMINAL_PROMPT=0 cargo vendor --locked "${fresh}" >/dev/null)

# Newer cargo adds a "$comment" key to .cargo-checksum.json. Drop that key from
# every such file in both trees, then compare the trees byte for byte. The
# only accepted differences are that key and the JSON formatting of those files.
for tree in "${fresh}" "${committed}"; do
  find "${tree}" -name .cargo-checksum.json -exec sh -c '
    for f; do jq -S "del(.\"\$comment\")" "$f" >"$0" && cp "$0" "$f" || exit 1; done
  ' "${tmp}/norm.json" {} +
done
# diff ignores file modes, so compare the executable files as well.
executables() {
  (cd "$1" && find . -type f -perm -u+x | sort)
}
exec_fresh="$(executables "${fresh}")"
exec_committed="$(executables "${committed}")"
if ! diff -r "${fresh}" "${committed}" || [[ "${exec_fresh}" != "${exec_committed}" ]]; then
  echo "runtime/container/rust/vendor differs from crates.io for the pinned Cargo.lock" >&2
  exit 1
fi
echo "Rust vendor tree matches crates.io."
