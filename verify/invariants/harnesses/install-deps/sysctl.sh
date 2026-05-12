#!/bin/bash
set -euo pipefail
if [[ "${1:-}" == "-in" ]] && [[ "${2:-}" == "hw.optional.arm64" ]]; then
  printf '1\n'
  exit 0
fi
exit 1
