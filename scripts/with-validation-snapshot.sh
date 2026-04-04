#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

REPO_ROOT=""
SNAPSHOT_MODE=""
INCLUDE_UNTRACKED=0
KEEP_SNAPSHOT=0
SNAPSHOT_DIR=""
SNAPSHOT_PARENT=""

usage() {
  cat <<EOF
Usage: $(basename "$0") --mode head|index|worktree [options] -- command [args...]

Create a disposable git-backed snapshot of a repository, run a validation
command inside that snapshot, then discard the snapshot on exit unless
--keep-snapshot is set.

Options:
  --repo <path>                Repository to snapshot (default: current directory)
  --mode <head|index|worktree> Snapshot mode to materialize
  --include-untracked          Include untracked files with --mode worktree
  --keep-snapshot              Preserve the snapshot worktree after the command
  -h, --help                   Show this help
EOF
}

die() {
  echo "$*" >&2
  exit 2
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || die "Missing required tool: $1"
}

copy_path_into_snapshot() {
  local relative_path="$1"
  local source_path="${REPO_ROOT}/${relative_path}"
  local target_path="${SNAPSHOT_DIR}/${relative_path}"

  remove_path_from_snapshot "${relative_path}"
  if [[ ! -e "${source_path}" && ! -L "${source_path}" ]]; then
    return 0
  fi
  mkdir -p "$(dirname "${target_path}")"
  cp -pPR "${source_path}" "${target_path}"
}

remove_path_from_snapshot() {
  local relative_path="$1"
  local target_path="${SNAPSHOT_DIR}/${relative_path}"

  rm -rf "${target_path}"
}

overlay_head_state() {
  local entry=""
  local metadata=""
  local mode=""
  local obj_type=""
  local oid=""
  local tracked_path=""
  local dest=""
  local install_mode=""

  # Materialize HEAD's committed tree via cat-file to bypass smudge/clean
  # filters.  Uses git ls-tree which outputs: <mode> <type> <oid>\t<path>\0
  while IFS= read -r -d '' entry; do
    metadata="${entry%%$'\t'*}"
    tracked_path="${entry#*$'\t'}"
    mode="${metadata%% *}"
    local remainder="${metadata#* }"
    obj_type="${remainder%% *}"
    oid="${remainder##* }"

    [[ "${obj_type}" == "blob" ]] || continue

    case "${mode}" in
      100644) install_mode="0644" ;;
      100755) install_mode="0755" ;;
      120000)
        echo "with-validation-snapshot: skipping symlink in HEAD tree: ${tracked_path}" >&2
        continue
        ;;
      *)
        echo "with-validation-snapshot: skipping unsupported mode ${mode} in HEAD tree: ${tracked_path}" >&2
        continue
        ;;
    esac

    case "${tracked_path}" in
      '' | . | .. | ./* | /* | */../* | ../* | */..)
        echo "with-validation-snapshot: unsafe path in HEAD tree rejected: ${tracked_path}" >&2
        continue
        ;;
    esac

    if [[ ${#oid} -lt 40 ]] || [[ ! "${oid}" =~ ^[0-9a-f]+$ ]]; then
      echo "with-validation-snapshot: malformed OID in HEAD tree: ${oid}" >&2
      continue
    fi

    dest="${SNAPSHOT_DIR}/${tracked_path}"
    if [[ -d "${dest}" ]]; then
      rm -rf "${dest}"
    fi
    mkdir -p "$(dirname "${dest}")"
    if ! git -C "${REPO_ROOT}" cat-file blob "${oid}" >"${dest}"; then
      echo "with-validation-snapshot: failed to read blob ${oid} for path: ${tracked_path}" >&2
      rm -f "${dest}"
      continue
    fi
    chmod "${install_mode}" "${dest}" 2>/dev/null || true
  done < <(git -C "${REPO_ROOT}" ls-tree -r -z HEAD)
}

overlay_index_state() {
  local tracked_path=""
  local entry=""
  local metadata=""
  local mode=""
  local oid=""
  local remainder=""
  local stage=""
  local dest=""
  local install_mode=""

  while IFS= read -r -d '' tracked_path; do
    remove_path_from_snapshot "${tracked_path}"
  done < <(git -C "${SNAPSHOT_DIR}" ls-files -z)

  # Materialize each tracked blob via cat-file to bypass smudge/clean filters
  # that a malicious .gitattributes could use for code execution.  Only regular
  # file modes (100644, 100755) are materialized; symlinks and submodules are
  # intentionally skipped since the validation snapshot does not need them.
  while IFS= read -r -d '' entry; do
    metadata="${entry%%$'\t'*}"
    tracked_path="${entry#*$'\t'}"
    mode="${metadata%% *}"
    remainder="${metadata#* }"
    oid="${remainder%% *}"
    stage="${remainder##* }"
    [[ "${stage}" == "0" ]] || continue

    case "${mode}" in
      100644) install_mode="0644" ;;
      100755) install_mode="0755" ;;
      120000)
        echo "with-validation-snapshot: skipping symlink in git index: ${tracked_path}" >&2
        continue
        ;;
      *)
        echo "with-validation-snapshot: skipping unsupported mode ${mode} in git index: ${tracked_path}" >&2
        continue
        ;;
    esac

    # Reject paths that could escape SNAPSHOT_DIR
    case "${tracked_path}" in
      '' | . | .. | ./* | /* | */../* | ../* | */..)
        echo "with-validation-snapshot: unsafe path in git index rejected: ${tracked_path}" >&2
        continue
        ;;
    esac

    if [[ ${#oid} -lt 40 ]] || [[ ! "${oid}" =~ ^[0-9a-f]+$ ]]; then
      echo "with-validation-snapshot: malformed OID in git index: ${oid}" >&2
      continue
    fi

    dest="${SNAPSHOT_DIR}/${tracked_path}"
    if [[ -d "${dest}" ]]; then
      rm -rf "${dest}"
    fi
    mkdir -p "$(dirname "${dest}")"
    if ! git -C "${REPO_ROOT}" cat-file blob "${oid}" >"${dest}"; then
      echo "with-validation-snapshot: failed to read blob ${oid} for path: ${tracked_path}" >&2
      rm -f "${dest}"
      continue
    fi
    chmod "${install_mode}" "${dest}" 2>/dev/null || true
  done < <(git -C "${REPO_ROOT}" ls-files -z --stage)
}

overlay_worktree_state() {
  local modified_path=""
  local deleted_path=""
  local untracked_path=""

  while IFS= read -r -d '' deleted_path; do
    remove_path_from_snapshot "${deleted_path}"
  done < <(git -C "${REPO_ROOT}" ls-files -z --deleted)

  while IFS= read -r -d '' modified_path; do
    copy_path_into_snapshot "${modified_path}"
  done < <(git -C "${REPO_ROOT}" ls-files -z -m)

  if [[ "${INCLUDE_UNTRACKED}" -eq 1 ]]; then
    while IFS= read -r -d '' untracked_path; do
      copy_path_into_snapshot "${untracked_path}"
    done < <(git -C "${REPO_ROOT}" ls-files -z --others --exclude-standard)
  fi
}

cleanup() {
  if [[ -z "${SNAPSHOT_DIR}" ]] || [[ ! -d "${SNAPSHOT_DIR}" ]]; then
    return 0
  fi
  if [[ "${KEEP_SNAPSHOT}" -eq 1 ]]; then
    echo "Preserved validation snapshot at ${SNAPSHOT_DIR}" >&2
    return 0
  fi
  rm -rf "${SNAPSHOT_DIR}"
}

trap cleanup EXIT

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      [[ $# -ge 2 ]] || die "Option --repo requires a value."
      REPO_ROOT="$2"
      shift 2
      ;;
    --mode)
      [[ $# -ge 2 ]] || die "Option --mode requires a value."
      SNAPSHOT_MODE="$2"
      shift 2
      ;;
    --include-untracked)
      INCLUDE_UNTRACKED=1
      shift
      ;;
    --keep-snapshot)
      KEEP_SNAPSHOT=1
      shift
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    --)
      shift
      break
      ;;
    *)
      die "Unknown option: $1"
      ;;
  esac
done

[[ $# -gt 0 ]] || die "Pass the validation command after --."
[[ -n "${SNAPSHOT_MODE}" ]] || die "Option --mode is required."

case "${SNAPSHOT_MODE}" in
  head | index | worktree) ;;
  *)
    die "Unsupported snapshot mode: ${SNAPSHOT_MODE}"
    ;;
esac

if [[ -z "${REPO_ROOT}" ]]; then
  REPO_ROOT="$(pwd)"
fi
REPO_ROOT="$(cd "${REPO_ROOT}" && pwd)"

require_tool git
require_tool mktemp
require_tool cp

git -C "${REPO_ROOT}" rev-parse --is-inside-work-tree >/dev/null 2>&1 ||
  die "Repository is not inside a git worktree: ${REPO_ROOT}"
REPO_ROOT="$(git -C "${REPO_ROOT}" rev-parse --show-toplevel)"

SNAPSHOT_PARENT="${WORKCELL_VALIDATION_SNAPSHOT_PARENT:-$(dirname "${REPO_ROOT}")}"
SNAPSHOT_PARENT="$(cd "${SNAPSHOT_PARENT}" && pwd)" || die "Snapshot parent does not exist: ${SNAPSHOT_PARENT}"
SNAPSHOT_DIR="$(mktemp -d "${SNAPSHOT_PARENT}/workcell-validation-snapshot.XXXXXX")"
# Clone without checkout so repository-controlled smudge/clean filters in
# .gitattributes cannot execute during snapshot creation.  All modes
# materialize tracked content via cat-file blob in overlay_index_state.
git clone -q --no-hardlinks --no-checkout "${REPO_ROOT}" "${SNAPSHOT_DIR}"

case "${SNAPSHOT_MODE}" in
  head)
    overlay_head_state
    ;;
  index)
    overlay_index_state
    ;;
  worktree)
    overlay_index_state
    overlay_worktree_state
    ;;
esac

(
  cd "${SNAPSHOT_DIR}"
  WORKCELL_VALIDATION_SNAPSHOT_ACTIVE=1 \
    WORKCELL_VALIDATION_SNAPSHOT_DIR="${SNAPSHOT_DIR}" \
    "$@"
)
