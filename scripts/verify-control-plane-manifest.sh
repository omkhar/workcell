#!/bin/bash -p
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/verify-control-plane-manifest.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
# shellcheck source=scripts/lib/trusted-entrypoint.sh
source "${workcell_entry_root}/scripts/lib/trusted-entrypoint.sh"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "${workcell_entry_root}/scripts/verify-control-plane-manifest.sh" >/dev/null
  echo "verify-control-plane-manifest-entrypoint-ok"
  exit 0
fi

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

ROOT_DIR="${workcell_entry_root}"
COMMITTED_MANIFEST="${ROOT_DIR}/runtime/container/control-plane-manifest.json"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-control-plane.XXXXXX")"
NESTED_ROOT=""
SAFE_GIT_CONFIG="${TMP_ROOT}/safe-gitconfig"

cleanup() {
  rm -rf "${TMP_ROOT}"
  if [[ -n "${NESTED_ROOT}" ]]; then
    rm -rf "${NESTED_ROOT}"
  fi
}

trap cleanup EXIT

git config --file "${SAFE_GIT_CONFIG}" --add safe.directory "${ROOT_DIR}"
if [[ -d "${ROOT_DIR}/.git" ]]; then
  git config --file "${SAFE_GIT_CONFIG}" --add safe.directory "${ROOT_DIR}/.git"
fi

safe_git() {
  env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME=/tmp \
    LC_ALL=C \
    LANG=C \
    GIT_ATTR_NOSYSTEM=1 \
    GIT_CONFIG_NOSYSTEM=1 \
    GIT_CONFIG_SYSTEM=/dev/null \
    GIT_CONFIG_GLOBAL="${SAFE_GIT_CONFIG}" \
    git "$@"
}

copy_tracked_worktree() {
  local destination="$1"
  local path=""
  local source_path=""
  local destination_path=""

  mkdir -p "${destination}"
  while IFS= read -r -d '' path; do
    source_path="${ROOT_DIR}/${path}"
    destination_path="${destination}/${path}"
    if [[ ! -e "${source_path}" ]]; then
      continue
    fi
    mkdir -p "$(dirname "${destination_path}")"
    cp -pP "${source_path}" "${destination_path}"
  done < <(safe_git -C "${ROOT_DIR}" ls-files -z --cached --modified --others --exclude-standard --deduplicate)
}

"${ROOT_DIR}/scripts/generate-control-plane-manifest.sh" "${TMP_ROOT}/a.json"
"${ROOT_DIR}/scripts/generate-control-plane-manifest.sh" "${TMP_ROOT}/b.json"

digest_a="$(shasum -a 256 "${TMP_ROOT}/a.json" | awk '{print $1}')"
digest_b="$(shasum -a 256 "${TMP_ROOT}/b.json" | awk '{print $1}')"

if [[ "${digest_a}" != "${digest_b}" ]]; then
  echo "Non-deterministic control-plane manifest: ${digest_a} != ${digest_b}" >&2
  diff -u "${TMP_ROOT}/a.json" "${TMP_ROOT}/b.json" || true
  exit 1
fi

committed_digest="$(shasum -a 256 "${COMMITTED_MANIFEST}" | awk '{print $1}')"
if [[ "${digest_a}" != "${committed_digest}" ]]; then
  echo "Committed control-plane manifest diverged from the generated manifest: ${committed_digest} != ${digest_a}" >&2
  diff -u "${COMMITTED_MANIFEST}" "${TMP_ROOT}/a.json" || true
  exit 1
fi

(cd "${ROOT_DIR}" && "${GO_BIN}" run ./cmd/workcell-citools verify-control-plane-manifest "${TMP_ROOT}/a.json")

if safe_git -C "${ROOT_DIR}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  NESTED_ROOT="$(mktemp -d "${TMP_ROOT}/nested-control-plane.XXXXXX")"
  ARCHIVE_ROOT="${TMP_ROOT}/archived-source"
  copy_tracked_worktree "${ARCHIVE_ROOT}"
  copy_tracked_worktree "${NESTED_ROOT}"

  WORKCELL_CONTROL_PLANE_ROOT="${ARCHIVE_ROOT}" \
    "${ROOT_DIR}/scripts/generate-control-plane-manifest.sh" "${TMP_ROOT}/archive-outside.json"
  WORKCELL_CONTROL_PLANE_ROOT="${NESTED_ROOT}" \
    "${ROOT_DIR}/scripts/generate-control-plane-manifest.sh" "${TMP_ROOT}/archive-nested.json"
  (
    cd "${TMP_ROOT}"
    WORKCELL_CONTROL_PLANE_ROOT="${ARCHIVE_ROOT}" \
      "${ROOT_DIR}/scripts/generate-control-plane-manifest.sh" "archive-relative.json"
  )

  digest_archive_outside="$(shasum -a 256 "${TMP_ROOT}/archive-outside.json" | awk '{print $1}')"
  digest_archive_nested="$(shasum -a 256 "${TMP_ROOT}/archive-nested.json" | awk '{print $1}')"
  digest_archive_relative="$(shasum -a 256 "${TMP_ROOT}/archive-relative.json" | awk '{print $1}')"
  if [[ "${digest_a}" != "${digest_archive_outside}" ]]; then
    echo "Archived-source control-plane manifest diverged from the tracked working-tree manifest: ${digest_archive_outside} != ${digest_a}" >&2
    diff -u "${TMP_ROOT}/a.json" "${TMP_ROOT}/archive-outside.json" || true
    exit 1
  fi
  if [[ "${digest_archive_outside}" != "${digest_archive_nested}" ]]; then
    echo "Nested archived-source control-plane manifest diverged from standalone archived-source manifest: ${digest_archive_nested} != ${digest_archive_outside}" >&2
    diff -u "${TMP_ROOT}/archive-outside.json" "${TMP_ROOT}/archive-nested.json" || true
    exit 1
  fi
  if [[ "${digest_archive_outside}" != "${digest_archive_relative}" ]]; then
    echo "Relative-output archived-source control-plane manifest diverged from absolute-output archived-source manifest: ${digest_archive_relative} != ${digest_archive_outside}" >&2
    diff -u "${TMP_ROOT}/archive-outside.json" "${TMP_ROOT}/archive-relative.json" || true
    exit 1
  fi

  cp -R "${ARCHIVE_ROOT}" "${TMP_ROOT}/symlink-artifact"
  ln -sf "${TMP_ROOT}/a.json" "${TMP_ROOT}/symlink-artifact/scripts/workcell"
  SYMLINK_LOG="${TMP_ROOT}/symlink-out.log"
  if env \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${HOME:-/tmp}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_CONTROL_PLANE_ROOT="${TMP_ROOT}/symlink-artifact" \
    "${ROOT_DIR}/scripts/generate-control-plane-manifest.sh" "${TMP_ROOT}/symlink-out.json" \
    >"${SYMLINK_LOG}" 2>&1; then
    echo "Expected control-plane manifest generation to reject symlinked tracked artifacts" >&2
    exit 1
  fi
  if ! grep -q "must not be a symlink" "${SYMLINK_LOG}"; then
    echo "Expected symlinked tracked artifacts to fail with an explicit manifest error" >&2
    cat "${SYMLINK_LOG}" >&2
    exit 1
  fi
fi

echo "Workcell control-plane manifest verification passed."
