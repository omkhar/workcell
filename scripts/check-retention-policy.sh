#!/usr/bin/env -S BASH_ENV= ENV= bash
# Assert that the artifact retention values documented in
# docs/retention-policy.md match the retention-days actually configured in the
# GitHub Actions workflows, so the policy and the workflow config cannot drift.
# A workflow may legitimately use more than one retention value (for example a
# long-lived evidence artifact and a short-lived redundant one); the doc lists
# the full set per workflow and this check compares the sets exactly.
# Kept bash-3.2 compatible (no mapfile, no associative arrays).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "${ROOT_DIR}"

DOC="docs/retention-policy.md"
failures=0
note() { echo "check-retention-policy: $*" >&2; failures=$((failures + 1)); }

[[ -f "${DOC}" ]] || { note "missing ${DOC}"; exit 1; }

# Normalize a whitespace/comma-separated list of integers to a sorted
# multiset (duplicates preserved), single-space-joined, e.g. "90, 7 90"
# -> "7 90 90". Multiplicity is kept so that reducing one of several equal-value
# uploads to a different value is still detected.
normalize_set() {
  printf '%s\n' "$1" | tr ',' ' ' | tr ' ' '\n' | grep -E '^[0-9]+$' | sort -n \
    | tr '\n' ' ' | sed 's/ *$//' || true
}

# Actual retention-days values for a workflow, as a sorted multiset. Anchored to
# the YAML key position so a "# retention-days:" comment is not counted, and
# duplicates are preserved so per-upload changes are not masked.
workflow_set() {
  grep -oE '^[[:space:]]*retention-days:[[:space:]]*[0-9]+' "$1" 2>/dev/null \
    | grep -oE '[0-9]+' | sort -n | tr '\n' ' ' | sed 's/ *$//' || true
}

# Documented rows between the machine-checked markers, tolerant of column
# padding: "<workflow><TAB><comma/space separated values>".
doc_rows="$(
  awk '/<!-- retention-policy:begin -->/{f=1;next} /<!-- retention-policy:end -->/{f=0} f' "${DOC}" \
    | grep -E '^\|[[:space:]]*[a-z0-9_.-]+\.yml[[:space:]]*\|[[:space:]]*[0-9]+([[:space:]]*,[[:space:]]*[0-9]+)*[[:space:]]*\|' \
    | sed -E 's/^\|[[:space:]]*([a-z0-9_.-]+\.yml)[[:space:]]*\|[[:space:]]*([0-9][0-9, ]*)[[:space:]]*\|.*/\1\t\2/' || true
)"
[[ -n "${doc_rows}" ]] || { note "no retention rows found in ${DOC} (are the markers present?)"; exit 1; }

# 1. Every documented workflow's actual retention set must equal the doc set.
while IFS="$(printf '\t')" read -r wf vals; do
  [[ -n "${wf}" ]] || continue
  wf_path=".github/workflows/${wf}"
  if [[ ! -f "${wf_path}" ]]; then
    note "documented workflow not found: ${wf}"
    continue
  fi
  doc_set="$(normalize_set "${vals}")"
  actual_set="$(workflow_set "${wf_path}")"
  if [[ -z "${actual_set}" ]]; then
    note "${wf} is documented (retention ${doc_set}) but sets no retention-days"
  elif [[ "${actual_set}" != "${doc_set}" ]]; then
    note "retention drift in ${wf}: documented {${doc_set}}, workflow has {${actual_set}}"
  fi
done <<EOF
${doc_rows}
EOF

# 2. Every workflow that sets retention-days must be documented.
for wf_path in .github/workflows/*.yml; do
  grep -qE '^[[:space:]]*retention-days:' "${wf_path}" || continue
  wf="$(basename "${wf_path}")"
  if ! printf '%s\n' "${doc_rows}" | grep -qE "^${wf}$(printf '\t')"; then
    note "workflow ${wf} sets retention-days but is not in the ${DOC} table"
  fi
done

# 3. Every actions/upload-artifact step must set an explicit retention-days, so
# no uploaded artifact silently inherits the repository default. Counting keys
# per workflow catches an upload added without its own retention-days.
for wf_path in .github/workflows/*.yml; do
  uploads="$(grep -cE '^[[:space:]]*uses:[[:space:]]*actions/upload-artifact@' "${wf_path}" || true)"
  [[ "${uploads}" -gt 0 ]] || continue
  retentions="$(grep -cE '^[[:space:]]*retention-days:' "${wf_path}" || true)"
  if [[ "${retentions}" -lt "${uploads}" ]]; then
    note "$(basename "${wf_path}") has ${uploads} upload-artifact step(s) but only ${retentions} retention-days setting(s); every upload must set explicit retention-days"
  fi
done

if [[ "${failures}" -gt 0 ]]; then
  echo "check-retention-policy: FAILED with ${failures} issue(s)" >&2
  exit 1
fi
echo "check-retention-policy: OK (retention policy matches workflow configuration)"
