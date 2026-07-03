#!/usr/bin/env -S BASH_ENV= ENV= bash
# Assert that the support-matrix metadata field names documented in
# docs/diagnostics-and-support-matrix.md exactly match the field names emitted
# by BOTH the Go emitter (internal/host/supportmatrix/supportmatrix.go
# MetadataLines) and the shell emitter (scripts/workcell
# print_support_matrix_state). Binding the doc to both emitters keeps the
# operator-facing diagnostics guide aligned with the code and also fails if the
# two emitters expose different field-name sets.
#
# Scope is deliberately field NAMES only: emission order, emitted values, and
# tier-word coverage in the prose are not checked here.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

GO_FILE="${ROOT_DIR}/internal/host/supportmatrix/supportmatrix.go"
SH_FILE="${ROOT_DIR}/scripts/workcell"
DOC_FILE="${ROOT_DIR}/docs/diagnostics-and-support-matrix.md"

fail() {
  echo "check-doc-support-matrix-fields: $*" >&2
  exit 1
}

for f in "${GO_FILE}" "${SH_FILE}" "${DOC_FILE}"; do
  [[ -f "${f}" ]] || fail "missing required file: ${f#"${ROOT_DIR}/"}"
done

# Field names emitted by Go MetadataLines(), scoped to that function body.
go_fields="$(
  awk '/^func MetadataLines\(/{f=1} f{print} f&&/^}/{exit}' "${GO_FILE}" \
    | grep -oE '"[a-z0-9_]+=%s"' \
    | sed -E 's/"([a-z0-9_]+)=%s"/\1/' \
    | sort -u
)"

# Field names printed by the shell print_support_matrix_state(), scoped to it.
sh_fields="$(
  awk '/^print_support_matrix_state\(\) \{/{f=1} f{print} f&&/^}/{exit}' "${SH_FILE}" \
    | grep -oE "'[a-z0-9_]+=%s" \
    | sed -E "s/'([a-z0-9_]+)=%s/\1/" \
    | sort -u
)"

# Field names documented between the machine-checked markers in the doc. Only
# the first table cell of each row is the field-name column; backticked value
# examples in the meaning column are intentionally ignored. The backtick is
# sourced from a variable so the extraction patterns stay in double quotes.
bt='`'
doc_fields="$(
  awk '/<!-- support-matrix-fields:begin -->/{f=1;next} /<!-- support-matrix-fields:end -->/{f=0} f' "${DOC_FILE}" \
    | grep -oE "^\\| ${bt}[a-z0-9_]+${bt}" \
    | sed -E "s/^\\| ${bt}([a-z0-9_]+)${bt}/\\1/" \
    | sort -u
)"

[[ -n "${go_fields}" ]] || fail "extracted no fields from Go MetadataLines()"
[[ -n "${sh_fields}" ]] || fail "extracted no fields from shell print_support_matrix_state()"
[[ -n "${doc_fields}" ]] || fail "extracted no fields from ${DOC_FILE#"${ROOT_DIR}/"} (are the support-matrix-fields markers present?)"

report_diff() {
  local label_a="$1" set_a="$2" label_b="$3" set_b="$4"
  local only_a only_b
  only_a="$(comm -23 <(printf '%s\n' "${set_a}") <(printf '%s\n' "${set_b}") | tr '\n' ' ')"
  only_b="$(comm -13 <(printf '%s\n' "${set_a}") <(printf '%s\n' "${set_b}") | tr '\n' ' ')"
  [[ -n "${only_a// }" ]] && echo "  only in ${label_a}: ${only_a}" >&2
  [[ -n "${only_b// }" ]] && echo "  only in ${label_b}: ${only_b}" >&2
}

if [[ "${go_fields}" != "${sh_fields}" ]]; then
  echo "check-doc-support-matrix-fields: Go and shell emitters disagree" >&2
  report_diff "Go" "${go_fields}" "shell" "${sh_fields}"
  exit 1
fi

if [[ "${doc_fields}" != "${go_fields}" ]]; then
  echo "check-doc-support-matrix-fields: documented fields differ from the emitted fields" >&2
  report_diff "doc" "${doc_fields}" "code" "${go_fields}"
  exit 1
fi

field_count="$(printf '%s\n' "${go_fields}" | grep -c .)"
echo "check-doc-support-matrix-fields: OK (${field_count} field names match across Go emitter, shell emitter, and doc)"
