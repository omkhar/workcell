#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/ci/job-pr-shape.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
BASE_BRANCH="${WORKCELL_PR_BASE_REF:-main}"
BASE_REF=""
readonly CERTIFIED_ADAPTER_LABEL="approved-large-certified-adapter"
LABELS=()
MARGIN="1.0"

usage() {
  cat <<'EOF'
Usage: job-pr-shape.sh [--base BRANCH] [--label LABEL] [--margin F]

Run the shared PR shape gate against the selected base branch.
F scales the shape limits (default 1.0); see check-pr-shape.sh --help.
EOF
}

add_labels_from_json() {
  local raw_json="${1:-}"
  local label=""

  [[ -n "${raw_json}" ]] || return 0
  while IFS= read -r label; do
    [[ -n "${label}" ]] || continue
    LABELS+=("${label}")
  done < <(jq -r 'if type == "array" then .[]? else empty end' <<<"${raw_json}")
}

has_label() {
  local expected="$1"
  local label=""

  for label in "${LABELS[@]}"; do
    [[ "${label}" == "${expected}" ]] && return 0
  done
  return 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --base)
      BASE_BRANCH="${2:-}"
      [[ -n "${BASE_BRANCH}" ]] || {
        echo "--base requires a branch name" >&2
        exit 2
      }
      shift 2
      ;;
    --label)
      LABELS+=("${2:-}")
      [[ -n "${LABELS[-1]}" ]] || {
        echo "--label requires a value" >&2
        exit 2
      }
      shift 2
      ;;
    --margin)
      MARGIN="${2:-}"
      [[ -n "${MARGIN}" ]] || {
        echo "--margin requires a value" >&2
        exit 2
      }
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
  add_labels_from_json "${WORKCELL_PR_LABELS_JSON:-}"
fi

if git -C "${ROOT_DIR}" remote get-url origin >/dev/null 2>&1; then
  git -C "${ROOT_DIR}" fetch --no-tags --prune origin "${BASE_BRANCH}"
  BASE_REF="origin/${BASE_BRANCH}"
elif git -C "${ROOT_DIR}" rev-parse --verify --quiet "refs/heads/${BASE_BRANCH}" >/dev/null; then
  BASE_REF="${BASE_BRANCH}"
else
  echo "Unable to resolve base branch ${BASE_BRANCH} locally or from origin." >&2
  exit 2
fi

shape_args=(
  --base-ref "${BASE_REF}"
  --head-ref HEAD
  --max-files 25
  --max-lines 1200
  --max-areas 8
  --max-binaries 0
  --margin "${MARGIN}"
)
if has_label "${CERTIFIED_ADAPTER_LABEL}"; then
  shape_args+=(--allow-certified-adapter-shape)
fi

"${ROOT_DIR}/scripts/check-pr-shape.sh" "${shape_args[@]}"
