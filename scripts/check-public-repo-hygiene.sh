#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

public_files=(
)

while IFS= read -r path; do
  [[ -n "${path}" ]] || continue
  public_files+=("${path}")
done < <(
  find "${ROOT_DIR}" -maxdepth 1 -type f \
    \( -name '*.md' -o -name '*.toml' -o -name '*.cff' -o -name 'LICENSE' -o -name 'NOTICE' \) \
    -print | sort
)

while IFS= read -r path; do
  [[ -n "${path}" ]] || continue
  public_files+=("${path}")
done < <(
  find \
    "${ROOT_DIR}/.agents" \
    "${ROOT_DIR}/docs" \
    "${ROOT_DIR}/man" \
    "${ROOT_DIR}/policy" \
    -type f \
    \( -name '*.md' -o -name '*.toml' -o -name '*.1' \) \
    -print | sort
)

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

        return prefix ~ /(^|[^[:alnum:]+.-])file:\/\/([^[:space:]\/?#]+)?$/
      }

      function line_has_disallowed_home_path(line,    rest, prefix, path) {
        rest = line
        while (match(rest, /(\/Users\/[^[:space:]\/]+([[:space:]\/[:punct:]]|$)|\/home\/[^[:space:]\/]+([[:space:]\/[:punct:]]|$))/)) {
          prefix = substr(rest, 1, RSTART - 1)
          path = substr(rest, RSTART, RLENGTH)
          if (has_allowed_home_path_prefix(prefix) && !is_example_placeholder(path)) {
            return 1
          }
          rest = substr(rest, RSTART + RLENGTH)
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
