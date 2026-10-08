#!/usr/bin/env -S BASH_ENV= ENV= bash
# Offline Markdown integrity check over tracked docs: fails on broken intra-repo
# relative links and on orphaned docs/ pages that nothing navigably links to. No
# network calls and no dependencies beyond git, awk, sed, grep and Go (for the
# claim probe) so it can run host-side in the docs CI lane. Kept bash-3.2
# compatible (no mapfile, no associative arrays) because the host baseline is
# macOS /bin/bash 3.2; see scripts/lib/shellproto.sh.
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
# shellcheck source=scripts/lib/go-run-env.sh
source "${ROOT_DIR}/scripts/lib/go-run-env.sh"

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
    awk -f "${ROOT_DIR}/scripts/lib/md-unfenced.awk" "${f}" |
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

# --- Doc claim enforcement check ----------------------------------------------
# A code span naming scripts/..., internal/... or .github/workflows/... (a
# leading ./ is dropped) must exist. Hits that exist today sit in
# policy/doc-claims-baseline.tsv (PATH, RULE, SUBJECT, REASON), whose comment
# lines name every rule. A new hit fails. A baseline row with no hit fails too,
# and every row must exist at the merge base with origin/main (or main), so the
# set only shrinks. A rule the merge-base baseline does not name is new, so its
# rows may enter once. A path with a .. or symlink component fails.
# Override the baseline path with DOC_CLAIMS_BASELINE and the workcell-citools
# binary with DOC_CLAIMS_CITOOLS (tests only).
claims_baseline="${DOC_CLAIMS_BASELINE:-${ROOT_DIR}/policy/doc-claims-baseline.tsv}"
claim_hits="$(mktemp "${TMPDIR:-/tmp}/check-doc-claims.XXXXXX")"
claim_base="$(mktemp "${TMPDIR:-/tmp}/check-doc-claims.XXXXXX")"
claim_cited="$(mktemp "${TMPDIR:-/tmp}/check-doc-claims.XXXXXX")"
trap 'rm -f "${link_records}" "${claim_hits}" "${claim_base}" "${claim_cited}"' EXIT

# workcell-citools doc-claims probes each cited path through no-follow
# descriptors, since bash has no openat.
for f in "${md_files[@]}"; do
  awk -f "${ROOT_DIR}/scripts/lib/md-unfenced.awk" "${f}" |
    awk -v doc="${f}" -f "${ROOT_DIR}/scripts/lib/doc-claims.awk"
done >"${claim_cited}"
if [[ -n "${DOC_CLAIMS_CITOOLS:-}" ]]; then
  "${DOC_CLAIMS_CITOOLS}" doc-claims "${ROOT_DIR}" <"${claim_cited}" >"${claim_hits}"
else
  run_go_in_repo "${ROOT_DIR}" run ./cmd/workcell-citools doc-claims "${ROOT_DIR}" <"${claim_cited}" >"${claim_hits}"
fi

# A rule is declared when a comment line of the baseline text names it.
declares_rule() {
  printf '%s\n' "$1" | grep '^#' | grep -qE "(^|[^a-z-])$2([^a-z-]|$)"
}
claims_text="$(cat "${claims_baseline}")"
while IFS= read -r rule; do
  [[ -n "${rule}" ]] || continue
  declares_rule "${claims_text}" "${rule}" ||
    note "doc-claims rule ${rule} is not named in the comment lines of ${claims_baseline}"
done < <(cut -f2 "${claim_hits}" | sort -u)

grep -v '^#' "${claims_baseline}" | cut -f1-3 >"${claim_base}" || [[ $? -eq 1 ]]
sort -o "${claim_hits}" "${claim_hits}"
sort -o "${claim_base}" "${claim_base}"

# Ratchet: every row must exist in the baseline at the merge base, so a fixed
# hit cannot hand its row to a new one. Every lookup error fails closed. The
# one skip is a merge base without the baseline file (the change that adds it).
claims_base_ref=""
for ref in refs/remotes/origin/main refs/heads/main; do
  if git rev-parse --verify --quiet "${ref}^{commit}" >/dev/null; then
    claims_base_ref="${ref}"
    break
  fi
done
claims_file=policy/doc-claims-baseline.tsv
if [[ -z "${claims_base_ref}" ]]; then
  echo "check-doc-links: no origin/main or main ref; baseline merge-base ratchet skipped" >&2
else
  claims_merge_base="$(git merge-base HEAD "${claims_base_ref}")" || {
    echo "check-doc-links: no merge base between HEAD and ${claims_base_ref}" >&2
    exit 2
  }
  claims_listed="$(git ls-tree --name-only "${claims_merge_base}" -- "${claims_file}")" || {
    echo "check-doc-links: cannot read the tree at merge base ${claims_merge_base}" >&2
    exit 2
  }
  if [[ -z "${claims_listed}" ]]; then
    echo "check-doc-links: ${claims_file} is not at the merge base (this change adds it); baseline merge-base ratchet skipped" >&2
  else
    base_text="$(git show "${claims_merge_base}:${claims_file}")" || {
      echo "check-doc-links: cannot read ${claims_file} at merge base ${claims_merge_base}" >&2
      exit 2
    }
    base_rows="$(printf '%s\n' "${base_text}" | awk -F'\t' '!/^#/ { print $1 FS $2 FS $3 }' | sort)"
    claim_unbased="$(comm -23 "${claim_base}" <(printf '%s\n' "${base_rows}"))"
    while IFS= read -r row; do
      [[ -n "${row}" ]] || continue
      rule="${row#*$'\t'}"
      rule="${rule%%$'\t'*}"
      # A rule that the merge-base baseline does not name is new in this
      # change, so its rows have no base to match.
      declares_rule "${base_text}" "${rule}" || continue
      note "doc-claims baseline row is not at the merge base with ${claims_base_ref}; fix the hit instead: ${row//$'\t'/ | }"
    done <<<"${claim_unbased}"
  fi
fi
claim_new="$(comm -23 "${claim_hits}" "${claim_base}")"
claim_stale="$(comm -13 "${claim_hits}" "${claim_base}")"
while IFS= read -r row; do
  [[ -n "${row}" ]] || continue
  note "unbaselined doc claim hit: ${row//$'\t'/ | }"
done <<<"${claim_new}"
while IFS= read -r row; do
  [[ -n "${row}" ]] || continue
  note "stale doc-claims baseline row (fixed; delete it): ${row//$'\t'/ | }"
done <<<"${claim_stale}"

if [[ "${failures}" -gt 0 ]]; then
  echo "check-doc-links: FAILED with ${failures} issue(s)" >&2
  exit 1
fi
echo "check-doc-links: OK (${#md_files[@]} markdown files; relative links, docs/ orphans, and doc claims clean)"
