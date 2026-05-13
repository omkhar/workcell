#!/bin/bash
set -euo pipefail
if [[ -d kind ]]; then
  printf 'kind=dir\n'
  printf 'payload=%s\n' "$(cat kind/payload.txt)"
else
  printf 'kind=file\n'
  printf 'payload=%s\n' "$(cat kind)"
fi
