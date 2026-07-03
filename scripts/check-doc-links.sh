#!/usr/bin/env -S BASH_ENV= ENV= bash
# Offline Markdown integrity check over tracked docs: fails on broken intra-repo
# relative links and on orphaned docs/ pages that nothing references. No network
# calls and no dependencies beyond git, awk, sed, and grep so it can run
# host-side in the docs CI lane.
#
# Scope (intentional limits, documented so they read as choices, not gaps):
# - Only space-free inline links of the form [text](target) are checked;
#   links inside fenced code blocks are skipped, and reference-style
#   definitions ([text][ref]) and inline-HTML links are not validated.
# - Relative targets are resolved the way GitHub renders them: relative to the
#   linking file's own directory only.
# - Heading-anchor (#fragment) validation is out of scope: reproducing GitHub's
#   slug algorithm offline (duplicate headings, punctuation, inline HTML) is
#   error-prone and would risk false failures on valid links. Target existence
#   is the robust, high-value core; anchor validation can be added later behind
#   its own evidence.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

failures=0
note() { echo "check-doc-links: $*" >&2; failures=$((failures + 1)); }

# Vendored and generated trees are not our docs; exclude the same paths the
# docs spelling job and .codespellrc skip.
excluded='^(runtime/container/rust/vendor|runtime/container/providers/node_modules|runtime/container/rust/target|dist|tmp)/'
mapfile -t md_files < <(git ls-files '*.md' | grep -vE "${excluded}" || true)

# Records, per link-target basename, the markdown files that navigably link it.
# Populated from fence-stripped content in the link loop so the orphan check
# below reuses the same view of who links whom (a link inside a code fence is
# not a navigable reference and must not keep a page out of the orphan set).
declare -A linked_by

# --- Broken relative-link check ---------------------------------------------
for f in "${md_files[@]}"; do
  dir="$(dirname "${f}")"
  while IFS= read -r target; do
    [[ -n "${target}" ]] || continue
    case "${target}" in
      http://*|https://*|mailto:*|tel:*|'#'*) continue ;;
    esac
    # Drop any #fragment; only the path portion is validated.
    path="${target%%#*}"
    [[ -n "${path}" ]] || continue
    # Record this navigable link (fence-stripped) for the orphan check.
    linked_by["$(basename "${path}")"]+=" ${f}"
    # GitHub resolves relative links against the linking file's directory only.
    if [[ ! -e "${dir}/${path}" ]]; then
      note "broken link: ${f} -> ${target} (missing ${dir}/${path})"
    fi
  done < <(
    # Strip fenced code blocks so example markdown inside ``` is not scanned.
    awk '/^[[:space:]]*```/{fence=!fence; next} !fence' "${f}" \
      | grep -oE '\]\([^) ]+\)' \
      | sed -E 's/^\]\(//; s/\)$//' || true
  )
done

# --- Orphan docs/ check -----------------------------------------------------
# A docs/*.md page is an orphan when no other tracked markdown file navigably
# links to it. A plain-text or code-span mention, or a link that only appears
# inside a fenced code sample, does not count: it does not let a reader reach
# the page. Membership is taken from linked_by, populated above from the same
# fence-stripped scan used for broken-link detection, so both checks agree on
# what a navigable link is. Index-style pages are linked widely, so this stays
# quiet on a healthy tree.
while IFS= read -r doc; do
  base="$(basename "${doc}")"
  others=0
  for referrer in ${linked_by["${base}"]:-}; do
    if [[ "${referrer}" != "${doc}" ]]; then
      others=1
      break
    fi
  done
  if [[ "${others}" -eq 0 ]]; then
    note "orphan doc: ${doc} is linked from no other tracked markdown file"
  fi
done < <(git ls-files 'docs/*.md')

if [[ "${failures}" -gt 0 ]]; then
  echo "check-doc-links: FAILED with ${failures} issue(s)" >&2
  exit 1
fi
echo "check-doc-links: OK (${#md_files[@]} markdown files; relative links and docs/ orphans clean)"
