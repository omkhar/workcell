#!/bin/bash -p
# Rebuilds the vendored Rust crates from crates.io against Cargo.lock and
# rejects any difference. Cargo checks a directory source only against the
# .cargo-checksum.json beside the code, so an edited crate with regenerated
# hashes builds. crates.io serves each .crate by its Cargo.lock checksum, so
# this comparison binds the committed bytes to the lock.
#
# Language-boundary justification (AGENTS.md): this is CI glue, not policy
# logic. It runs cargo, tar, find and diff in a scratch directory and compares
# the results; the few text checks guard the inputs to cargo vendor. It has no
# runtime or host policy, no state, and no Go tool to dispatch to, and the other
# validate-job checks are shell in the same way. A Go port would only re-invoke
# the same external commands.
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
# Cargo follows links in path dependencies too, so scan every source file that
# is copied for Cargo, not only vendor.
odd="$(find "${RUST_DIR}" \( -path "${RUST_DIR}/target" -o -path "${RUST_DIR}/.cargo" \) -prune -o ! -type f ! -type d -print)"
if [[ -n "${odd}" ]]; then
  echo "Rust tree contains a link or special file: ${odd}" >&2
  exit 1
fi

# Cargo follows links inside a Git dependency and copies their targets into the
# vendor output, so accept only crates.io sources. Cargo reads spellings that a
# line match misses, so allow only the exact form Cargo writes and reject every
# other line.
odd_lock="$(awk '
  /^$/ || /^#/ || /^version = [0-9]+$/ || /^\[\[package\]\]$/ { next }
  /^name = "[A-Za-z0-9_-]+"$/ || /^version = "[0-9A-Za-z.+-]+"$/ { next }
  /^source = "registry\+https:\/\/github.com\/rust-lang\/crates.io-index"$/ { next }
  /^checksum = "[0-9a-f]+"$/ || /^dependencies = \[$/ || /^\]$/ { next }
  /^ "[A-Za-z0-9_. +-]+",$/ { next }
  { print }
' "${RUST_DIR}/Cargo.lock")"
if [[ -n "${odd_lock}" ]]; then
  echo "Cargo.lock has a line this check does not accept: ${odd_lock}" >&2
  exit 1
fi

# Cargo reads every path dependency before it checks the lock, and a parse
# error prints file content. The root manifest has no path dependency, so allow
# only the crate's own target paths under src/. Comment lines are skipped. Any
# other line that uses a path or workspace key, or a backslash that could hide
# a key, fails closed.
odd_manifest="$(awk '
  /^[ \t]*#/ { next }
  /(^|[^A-Za-z0-9_-])(path|workspace)["'"'"']?[ \t]*[=.\]]/ || /\\/ {
    if ($0 !~ /^path = "src\/[A-Za-z0-9_\/.-]+\.rs"$/) print
  }
' "${RUST_DIR}/Cargo.toml")"
if [[ -n "${odd_manifest}" ]]; then
  echo "Cargo.toml has a path, workspace or escape this check does not accept: ${odd_manifest}" >&2
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

# Newer cargo adds a plain "$comment" member to .cargo-checksum.json. Cut that
# member from every such file in both trees, then compare the trees byte for
# byte. The only accepted difference is that member. A JSON parser would hide
# duplicate keys and formatting, so sed cuts the exact text instead.
# shellcheck disable=SC2016 # a sed expression; the shell must not expand it
comment_member='s/^\{"\$comment":"[[:alnum:] .,;:()\/_-]*",/{/; s/,"\$comment":"[[:alnum:] .,;:()\/_-]*"\}$/}/'
for tree in "${fresh}" "${committed}"; do
  find "${tree}" -name .cargo-checksum.json -exec sh -c '
    expr=$1
    shift
    for f; do sed -E "${expr}" "$f" >"$0" && cp "$0" "$f" || exit 1; done
  ' "${tmp}/norm.json" "${comment_member}" {} +
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
