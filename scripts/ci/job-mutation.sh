#!/usr/bin/env -S BASH_ENV= ENV= bash
# Build the validator image and run the mutation-score gate inside it, surfacing
# the score in the GitHub job summary. Mirrors the build+run portion of
# job-validate.sh without the release-bundle machinery.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

echo "[ci/mutation] validator image build"
VALIDATOR_IMAGE="$("${ROOT_DIR}/scripts/ci/build-validator-image.sh")"
export WORKCELL_VALIDATOR_IMAGE="${VALIDATOR_IMAGE}"

echo "[ci/mutation] mutation-score gate in validator"
# Stream the harness output live (so a hung mutant hitting the job timeout still
# leaves logs) while capturing it to a file for the score summary.
mutation_log="${RUNNER_TEMP:-/tmp}/workcell-mutation.log"
set +e
"${ROOT_DIR}/scripts/ci/run-mutation-in-validator.sh" 2>&1 | tee "${mutation_log}"
mutation_status=${PIPESTATUS[0]}
set -e

if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  score_line="$(grep -E '^mutation score:' "${mutation_log}" | tail -1 || true)"
  {
    echo "## Mutation score"
    echo
    echo '```'
    echo "${score_line:-(score not captured; see job log)}"
    echo '```'
  } >>"${GITHUB_STEP_SUMMARY}"
fi

exit "${mutation_status}"
