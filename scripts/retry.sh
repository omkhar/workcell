#!/usr/bin/env -S BASH_ENV= ENV= bash
# Bounded-retry wrapper for transient network / package-fetch steps in CI.
#
# Runs the given command and, if it fails, retries it up to 3 times with a
# 5-second sleep between attempts, doubling the delay after each failure so a
# brief registry/network hiccup does not fail an otherwise-green lane.
#
# Only wrap network-flaky, idempotent commands (toolchain and package fetches,
# artifact transfers). Never wrap deterministic logic whose failure is a real
# signal (linters, tests, lock-file checks): retrying those only wastes CI time
# and still fails after the last attempt without hiding anything.
#
# Usage: scripts/retry.sh <command> [args...]
set -euo pipefail

if [[ "$#" -eq 0 ]]; then
  echo "scripts/retry.sh: a command to run is required" >&2
  exit 2
fi

attempts=3
delay=5

attempt=1
while true; do
  # Capture the command's real exit status: an `if cmd; then ...; fi` with no
  # matching branch returns 0, which would mask a genuine failure.
  status=0
  "$@" || status="$?"
  if [[ "${status}" -eq 0 ]]; then
    exit 0
  fi
  if [[ "${attempt}" -ge "${attempts}" ]]; then
    echo "scripts/retry.sh: command failed after ${attempts} attempt(s) (exit ${status}): $*" >&2
    exit "${status}"
  fi
  echo "scripts/retry.sh: attempt ${attempt}/${attempts} failed (exit ${status}); retrying in ${delay}s: $*" >&2
  sleep "${delay}"
  attempt=$((attempt + 1))
  delay=$((delay * 2))
done
