#!/bin/bash
set -euo pipefail
printf 'tracked=%s\n' "$(cat tracked.txt)"
if [[ -e untracked.txt ]]; then
  printf 'untracked=%s\n' "$(cat untracked.txt)"
fi
