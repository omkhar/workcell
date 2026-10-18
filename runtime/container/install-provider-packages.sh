#!/usr/bin/env -S BASH_ENV= ENV= bash
# install-provider-packages.sh DIR installs the pinned npm provider packages of
# DIR from its package-lock.json, with no install scripts. A failed install
# retries from an empty node_modules and a clean npm cache. Every path in DIR
# then gets the SOURCE_DATE_EPOCH mtime, so the layer is reproducible. The
# Dockerfile provider builder stages run it at build time only.
set -euo pipefail

dir="$1"
: "${SOURCE_DATE_EPOCH:?SOURCE_DATE_EPOCH is required}"
cd "${dir}"
for attempt in 1 2 3; do
  rm -rf node_modules
  if npm ci --omit=dev --ignore-scripts --no-audit --no-fund; then
    break
  fi
  if [[ "${attempt}" -eq 3 ]]; then
    exit 1
  fi
  npm cache clean --force >/dev/null 2>&1 || true
  sleep "$((attempt * 5))"
done
find "${dir}" -print0 | LC_ALL=C sort -z | xargs -0 touch -h -d "@${SOURCE_DATE_EPOCH}"
