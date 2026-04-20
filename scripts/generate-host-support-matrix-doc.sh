#!/bin/bash -p
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MATRIX_PATH="${ROOT_DIR}/policy/host-support-matrix.tsv"
OUTPUT_PATH="${1:-${ROOT_DIR}/docs/host-support-matrix.md}"

emit_validation_lane_doc() {
  local lane="$1"

  case "${lane}" in
    trusted-linux-amd64-validator)
      cat <<'EOF'
- `trusted-linux-amd64-validator`:
  `./scripts/build-and-test.sh --docker`
  runs repo validation inside the pinned Linux validator container from a
  disposable snapshot. This is a reviewed validation-host bridge, not a
  supported Workcell launch host.
EOF
      ;;
  esac
}

{
  cat <<'EOF'
# Host Support Matrix

This file is generated from
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv). Do not
edit it directly.

The canonical matrix is keyed by `host_os`, `host_arch`, `target_kind`,
`target_provider`, and `target_assurance_class`. `workcell --inspect`,
`workcell --doctor`, and launch gating all derive their support-boundary output
from that same artifact.

## Validation-host bridge

EOF

  while IFS=$'\t' read -r host_os host_arch target_kind target_provider target_assurance_class status launch evidence validation_lane reason; do
    [[ -n "${host_os}" ]] || continue
    [[ "${host_os}" == \#* ]] && continue
    [[ "${host_os}" == "host_os" ]] && continue
    if [[ "${validation_lane}" != "none" ]]; then
      emit_validation_lane_doc "${validation_lane}"
    fi
  done <"${MATRIX_PATH}"

  cat <<'EOF'

## Matrix

| Host OS | Host arch | Target kind | Target provider | Assurance class | Support status | Launch | Evidence tier | Validation lane | Reason |
|---|---|---|---|---|---|---|---|---|---|
EOF

  while IFS=$'\t' read -r host_os host_arch target_kind target_provider target_assurance_class status launch evidence validation_lane reason; do
    [[ -n "${host_os}" ]] || continue
    [[ "${host_os}" == \#* ]] && continue
    [[ "${host_os}" == "host_os" ]] && continue
    printf "| \`%s\` | \`%s\` | \`%s\` | \`%s\` | \`%s\` | \`%s\` | \`%s\` | \`%s\` | \`%s\` | \`%s\` |\n" \
      "${host_os}" \
      "${host_arch}" \
      "${target_kind}" \
      "${target_provider}" \
      "${target_assurance_class}" \
      "${status}" \
      "${launch}" \
      "${evidence}" \
      "${validation_lane}" \
      "${reason}"
  done <"${MATRIX_PATH}"

  cat <<'EOF'

## Interpretation

- `supported` plus `launch=allowed` means the reviewed launch path is supported
  for that exact host and target combination.
- `validation-host-only` means the row is trusted only as a validation bridge.
  It bounds support claims and diagnostics, but Workcell launch remains blocked
  on that host.
- `unsupported` means the combination is outside the reviewed support boundary
  and launch must fail closed.
EOF
} >"${OUTPUT_PATH}"
