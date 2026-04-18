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
  local path_prefix_regex
  local path_regex

  path_prefix_regex='(^|[^[:alnum:]/._~-])'
  path_regex="${path_prefix_regex}(/Users/[^[:space:]/]+([/[:punct:]]|$)|/home/[^[:space:]/]+([/[:punct:]]|$))"
  findings="$(
    if command -v rg >/dev/null 2>&1; then
      rg -n "${path_regex}" "${public_files[@]}" || true
    else
      grep -HnE "${path_regex}" "${public_files[@]}" || true
    fi |
      grep -vE "${path_prefix_regex}/Users/example(/|$)" |
      grep -vE "${path_prefix_regex}/home/example(/|$)" || true
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
