# shellcheck shell=bash
# Per-run temp/cache roots with an owner marker.  A lane creates its root with
# workcell_owned_root_create and removes it with workcell_owned_root_remove.
# Removal refuses any root whose owner.pid does not name the calling script, so
# a lane never deletes a root that another concurrent lane created.
#
# Residual risk, accepted: the owner check and the chmod/rm that follow it
# address the root by pathname, so a same-UID process that swaps the pathname
# between the check and the rm can steer the rm at an unchecked tree. The
# guard exists to stop the repo's own concurrent lanes from deleting each
# other's roots by accident, not to defend against a hostile same-UID process,
# which already owns every file these scripts can touch. Bash has no
# fd-relative, no-follow tree removal; move removal to a Go helper built on
# internal/rootio if a hostile same-UID process ever enters the threat model.

workcell_owned_root_create() {
  local base="$1" prefix="$2" root=""
  root="$(mktemp -d "${base%/}/${prefix}.XXXXXX")" || return
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
