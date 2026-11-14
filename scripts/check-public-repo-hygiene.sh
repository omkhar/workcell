#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/check-public-repo-hygiene.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"

public_files=(
)

# Capture each inventory before the loop reads it. A process substitution hides
# a `find` failure, and this script only reports on the files it collects: an
# unread inventory would present as a clean public surface. `set -e` with
# `pipefail` now aborts on a failed or partial walk instead.
root_public_listing="$(
  find "${ROOT_DIR}" -maxdepth 1 -type f \
    \( -name '*.md' -o -name '*.toml' -o -name '*.cff' -o -name 'LICENSE' -o -name 'NOTICE' \) \
    -print | sort
)"

tree_public_listing="$(
  find \
    "${ROOT_DIR}/.agents" \
    "${ROOT_DIR}/docs" \
    "${ROOT_DIR}/man" \
    "${ROOT_DIR}/policy" \
    -type f \
    \( -name '*.md' -o -name '*.toml' -o -name '*.1' \) \
    -print | sort
)"

while IFS= read -r path; do
  [[ -n "${path}" ]] || continue
  public_files+=("${path}")
done <<<"${root_public_listing}"

while IFS= read -r path; do
  [[ -n "${path}" ]] || continue
  public_files+=("${path}")
done <<<"${tree_public_listing}"

check_public_surfaces() {
  local findings
  findings="$(
    awk '
      function is_example_placeholder(path) {
        return path ~ /^\/Users\/example($|[[:space:]]|\/|[][})>"'"'"'`.,:;!?])/ ||
          path ~ /^\/home\/example($|[[:space:]]|\/|[][})>"'"'"'`.,:;!?])/
      }

      function has_allowed_home_path_prefix(prefix,    last_char) {
        if (prefix == "") {
          return 1
        }

        last_char = substr(prefix, length(prefix), 1)
        if (last_char !~ /[[:alnum:]\/._~-]/) {
          return 1
        }

        return prefix ~ /(^|[^[:alnum:]+.\/-])file:\/\/([^[:space:]\/?#]+)?$/
      }

      function line_has_disallowed_home_path(line,    search_start, segment, absolute_start, prefix, path) {
        search_start = 1
        while (match(substr(line, search_start), /(\/Users\/[^[:space:]\/]+([[:space:]\/[:punct:]]|$)|\/home\/[^[:space:]\/]+([[:space:]\/[:punct:]]|$))/)) {
          segment = substr(line, search_start)
          absolute_start = search_start + RSTART - 1
          prefix = substr(line, 1, absolute_start - 1)
          path = substr(segment, RSTART, RLENGTH)
          if (has_allowed_home_path_prefix(prefix) && !is_example_placeholder(path)) {
            return 1
          }
          search_start = absolute_start + RLENGTH
        }
        return 0
      }

      line_has_disallowed_home_path($0) {
        printf "%s:%d:%s\n", FILENAME, FNR, $0
      }
    ' "${public_files[@]}" || true
  )"
  if [[ -n "${findings}" ]]; then
    echo "Public-facing repo files contain machine-specific absolute home paths:" >&2
    printf '%s\n' "${findings}" >&2
    return 1
  fi
}

check_repo_detritus() {
  local findings
  findings="$(
    find "${ROOT_DIR}" \
      \( -path "${ROOT_DIR}/.git" \
      -o -path "${ROOT_DIR}/runtime/container/providers/node_modules" \
      -o -path "${ROOT_DIR}/runtime/container/rust/vendor" \
      -o -path "${ROOT_DIR}/runtime/container/rust/target" \
      -o -path "${ROOT_DIR}/dist" \
      -o -path "${ROOT_DIR}/tmp" \) -prune -o \
      -type f \
      \( -name '.DS_Store' -o -name '*.orig' -o -name '*.rej' -o -name '*.bak' -o -name '*~' \) \
      -print | sort
  )"
  if [[ -n "${findings}" ]]; then
    echo "Repository detritus must be removed before validation passes:" >&2
    printf '%s\n' "${findings}" >&2
    return 1
  fi
}

check_public_surfaces
check_repo_detritus
echo "Public repo hygiene check passed."
