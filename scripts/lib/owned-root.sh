# shellcheck shell=bash
# Per-run temp/cache roots with an owner marker.  A lane creates its root with
# workcell_owned_root_create and removes it with workcell_owned_root_remove.
# Removal refuses any root whose owner.pid does not name the calling script, so
# a lane never deletes a root that another concurrent lane created.

workcell_owned_root_create() {
  local base="$1" prefix="$2" root=""
  root="$(mktemp -d "${base}/${prefix}.XXXXXX")" || return
  printf '%s\n' "$$" >"${root}/owner.pid" || return
  printf '%s\n' "${root}"
}

workcell_owned_root_check() {
  local root="$1" owner=""
  [[ -d "${root}" && ! -L "${root}" && -f "${root}/owner.pid" && ! -L "${root}/owner.pid" ]] || return 1
  owner="$(cat "${root}/owner.pid")" || return 1
  [[ "${owner}" == "$$" ]]
}

workcell_owned_root_remove() {
  local root="$1"
  [[ -n "${root}" ]] || return 0
  [[ -e "${root}" || -L "${root}" ]] || return 0
  workcell_owned_root_check "${root}" || {
    echo "Refusing to remove a root this run does not own: ${root}" >&2
    return 1
  }
  chmod -R u+w "${root}"
  rm -rf "${root}"
}
