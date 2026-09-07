#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail
printf 'tracked=%s\n' "$(cat tracked.txt)"
if [[ -e untracked.txt ]]; then
  printf 'untracked=%s\n' "$(cat untracked.txt)"
fi
