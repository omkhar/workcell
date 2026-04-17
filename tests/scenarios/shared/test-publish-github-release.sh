#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

draft_ok_output="$("${ROOT_DIR}/scripts/publish-github-release.sh" --self-release-state-probe v9.9.9 true false)"
grep -Fx 'publish-github-release-state-ok' <<<"${draft_ok_output}" >/dev/null

set +e
published_mutable_output="$("${ROOT_DIR}/scripts/publish-github-release.sh" --self-release-state-probe v9.9.9 false false 2>&1)"
published_mutable_rc=$?
set -e
test "${published_mutable_rc}" -eq 1
grep -F 'GitHub release v9.9.9 is already published; Workcell only uploads assets to draft releases.' <<<"${published_mutable_output}" >/dev/null

set +e
published_immutable_output="$("${ROOT_DIR}/scripts/publish-github-release.sh" --self-release-state-probe v9.9.9 false true 2>&1)"
published_immutable_rc=$?
set -e
test "${published_immutable_rc}" -eq 1
grep -F 'GitHub release v9.9.9 is already published and immutable; asset uploads are no longer allowed.' <<<"${published_immutable_output}" >/dev/null

echo "publish-github-release policy scenario passed"
