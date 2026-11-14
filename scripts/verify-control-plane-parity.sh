#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/verify-control-plane-parity.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  echo "verify-control-plane-parity-entrypoint-ok"
  exit 0
fi

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

resolve_go_bin() {
  local candidate

  if candidate="$(command -v go 2>/dev/null)"; then
    printf '%s\n' "${candidate}"
    return 0
  fi

  for candidate in \
    /opt/homebrew/bin/go \
    /usr/local/go/bin/go \
    /usr/local/bin/go \
    /usr/bin/go; do
    if [[ -x "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done

  echo "Missing required tool: go" >&2
  exit 1
}

GO_BIN="$(resolve_go_bin)"

MANIFEST="${ROOT_DIR}/runtime/container/control-plane-manifest.json"
CONTROL_PLANE="${ROOT_DIR}/runtime/container/home-control-plane.sh"
ENTRYPOINT="${ROOT_DIR}/runtime/container/entrypoint.sh"

missing=0

while IFS=$'\t' read -r requirement_type label value; do
  [[ -n "${requirement_type}" ]] || continue

  case "${requirement_type}" in
    prefix)
      expected_call="workcell_verify_control_plane_prefix \"\${ADAPTER_ROOT}/${label}/\""
      if ! grep -Fq "${expected_call}" "${CONTROL_PLANE}"; then
        echo "missing control-plane verification prefix for ${label}: ${expected_call}" >&2
        missing=$((missing + 1))
      fi
      ;;
    path)
      expected_call="workcell_verify_control_plane_path \"${value}\""
      if ! grep -Fq "${expected_call}" "${CONTROL_PLANE}" &&
        ! grep -Fq "${expected_call}" "${ENTRYPOINT}"; then
        echo "missing control-plane verification path for ${label}: ${expected_call}" >&2
        missing=$((missing + 1))
      fi
      ;;
    *)
      echo "unsupported parity requirement type: ${requirement_type}" >&2
      missing=$((missing + 1))
      ;;
  esac
done < <(
  cd "${ROOT_DIR}" && "${GO_BIN}" run ./cmd/workcell-citools verify-control-plane-parity "${MANIFEST}"
)

if [[ "${missing}" -gt 0 ]]; then
  echo "${missing} control-plane verification gap(s) found in home-control-plane.sh." >&2
  exit 1
fi

echo "Control plane parity verification passed"
