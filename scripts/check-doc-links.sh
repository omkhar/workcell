#!/usr/bin/env -S BASH_ENV= ENV= bash
# Offline Markdown integrity check over tracked docs: fails on broken intra-repo
# relative links and on orphaned docs/ pages that nothing navigably links to. No
# network calls and no dependencies beyond git, awk, sed, and grep so it can run
# host-side in the docs CI lane. Kept bash-3.2 compatible (no mapfile, no
# associative arrays) because the host baseline is macOS /bin/bash 3.2; see
# scripts/lib/shellproto.sh.
#
# Scope (intentional limits, documented so they read as choices, not gaps):
# - Only space-free inline links of the form [text](target) are checked;
#   links inside fenced code blocks and inline `code` spans are skipped, and
#   reference-style definitions ([text][ref]) and inline-HTML links are not
#   validated.
# - Relative targets are resolved the way GitHub renders them: relative to the
#   linking file's own directory, and are required to stay inside the repo.
# - Heading-anchor (#fragment) validation is out of scope: reproducing GitHub's
#   slug algorithm offline is error-prone and would risk false failures on valid
#   links. Target existence is the robust, high-value core.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${ROOT_DIR}"

bt='`'
failures=0
note() {
  echo "check-doc-links: $*" >&2
  failures=$((failures + 1))
}

# Referrer index: one "linker<TAB>canonical-target" line per navigable link,
# keyed by resolved absolute path so distinct files that share a basename are
# never confused. A temp file keeps this bash-3.2 compatible.
link_records="$(mktemp "${TMPDIR:-/tmp}/check-doc-links.XXXXXX")"
trap 'rm -f "${link_records}"' EXIT

# Vendored and generated trees are not our docs; exclude the same paths the
# docs spelling job and .codespellrc skip. Test fixtures (testdata/) are also
# excluded: they simulate external content, not documentation.
excluded='^(runtime/container/rust/vendor|runtime/container/providers/node_modules|runtime/container/rust/target|dist|tmp)/|(^|/)testdata/'

# Capture the listing before the exclusion filter runs. `git ls-files | grep
# || true` erases Git's exit status twice over, so a failed or truncated
# listing would silently shrink this inventory and every check below would
# pass over the files it never read. `set -e` aborts on the capture instead.
# The `|| true` stays on the filter alone, where an empty result is a real
# outcome, and the inventory assertion rejects a vacuously empty result.
md_listing="$(git ls-files '*.md')"

md_files=()
while IFS= read -r mf; do
  [[ -n "${mf}" ]] && md_files+=("${mf}")
done < <(printf '%s\n' "${md_listing}" | grep -vE "${excluded}" || true)

if [[ "${#md_files[@]}" -eq 0 ]]; then
  echo "check-doc-links: markdown inventory is empty; refusing a vacuous pass" >&2
  exit 1
fi

# --- Broken relative-link check + referrer index ----------------------------
for f in "${md_files[@]}"; do
  dir="$(dirname "${f}")"
  while IFS= read -r target; do
    [[ -n "${target}" ]] || continue
    case "${target}" in
      http://* | https://* | mailto:* | tel:* | '#'*) continue ;;
    esac
    # Drop any #fragment; only the path portion is validated.
    path="${target%%#*}"
    [[ -n "${path}" ]] || continue
    resolved="${dir}/${path}"
    # GitHub resolves relative links against the linking file's directory only.
    if [[ ! -e "${resolved}" ]]; then
      note "broken link: ${f} -> ${target} (missing ${resolved})"
      continue
    fi
    # Canonicalize fully (resolving any trailing ..) and require the target to
    # stay within the repository checkout so a traversal such as ../../etc/passwd
    # or ../../.. cannot pass by matching a host path.
    # shellcheck disable=SC2312 # a failed cd yields a path outside ROOT_DIR, which the containment case below rejects
    if [[ -d "${resolved}" ]]; then
      canon="$(cd "${resolved}" && pwd -P)"
    else
      canon="$(cd "$(dirname "${resolved}")" && pwd -P)/$(basename "${resolved}")"
    fi
    case "${canon}" in
      "${ROOT_DIR}" | "${ROOT_DIR}"/*) : ;;
      *)
        note "link escapes repository: ${f} -> ${target}"
        continue
        ;;
    esac
    printf '%s\t%s\n' "${f}" "${canon}" >>"${link_records}"
  done < <(
    # Strip fenced code blocks, then inline code spans, so example markdown
    # (fenced or `inline`) is not treated as a navigable link.
    awk '/^[[:space:]]*```/{fence=!fence; next} !fence' "${f}" |
      sed -E "s/${bt}[^${bt}]*${bt}//g" |
      grep -oE '\]\([^) ]+\)' |
      sed -E 's/^\]\(//; s/\)$//' || true
  )
done

# --- Orphan docs/ check -----------------------------------------------------
# A docs/*.md page is an orphan when no other tracked markdown file navigably
# links to it. Matching is by canonical path, so a page is not treated as
# referenced merely because some unrelated file shares its basename.
# Capture the listing before the loop reads it. A process substitution hides a
# `git ls-files` failure, which would leave this orphan check iterating nothing
# and reporting a clean result over a docs tree it never enumerated.
docs_listing="$(git ls-files 'docs/*.md')"

while IFS= read -r doc; do
  [[ -n "${doc}" ]] || continue
  doc_canon="${ROOT_DIR}/${doc}"
  if awk -F'\t' -v t="${doc_canon}" -v self="${doc}" \
    '$2==t && $1!=self {found=1} END{exit found?0:1}' "${link_records}"; then
    : # navigably linked from another markdown file
  else
    note "orphan doc: ${doc} is linked from no other tracked markdown file"
  fi
done <<<"${docs_listing}"

if [[ "${failures}" -gt 0 ]]; then
  echo "check-doc-links: FAILED with ${failures} issue(s)" >&2
  exit 1
fi
echo "check-doc-links: OK (${#md_files[@]} markdown files; relative links and docs/ orphans clean)"
