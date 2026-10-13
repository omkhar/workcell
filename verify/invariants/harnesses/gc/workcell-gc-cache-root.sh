# shellcheck shell=bash
# Harness body for cleanup_workcell_cache_root: verify-invariants.sh prepends
# the launcher functions it calls. A root whose entries are all old goes, a
# root with one recently changed entry stays, and a threshold of 0 removes it.
gc_root="$(mktemp -d "${TMPDIR:-/tmp}/workcell-gc-cache-root.XXXXXX")"
trap 'chmod -R u+w "${gc_root}" 2>/dev/null || true; rm -rf "${gc_root}"' EXIT

make_cache_root() {
  mkdir -p "$1/go/build-cache/ab"
  printf 'cached\n' >"$1/go/build-cache/ab/entry"
  touch -t 202001010000 "$1/go/build-cache/ab/entry" "$1/go/build-cache/ab" "$1/go/build-cache" "$1/go" "$1"
}

make_cache_root "${gc_root}/stale"
cleanup_workcell_cache_root "${gc_root}/stale" 60
[[ ! -e "${gc_root}/stale" ]]

make_cache_root "${gc_root}/in-use"
printf 'fresh\n' >"${gc_root}/in-use/go/build-cache/ab/fresh"
touch -t 202001010000 "${gc_root}/in-use/go/build-cache/ab" "${gc_root}/in-use/go/build-cache" "${gc_root}/in-use/go" "${gc_root}/in-use"
cleanup_workcell_cache_root "${gc_root}/in-use" 60
[[ -f "${gc_root}/in-use/go/build-cache/ab/fresh" ]]

cleanup_workcell_cache_root "${gc_root}/in-use" 0
[[ ! -e "${gc_root}/in-use" ]]
echo "gc cache-root harness passed"
