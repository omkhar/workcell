#!/usr/bin/env -S BASH_ENV= ENV= bash
# Weekly CI cost-visibility report for the CI insights lane
# (.github/workflows/ci-insights.yml). Emits Markdown to stdout; the workflow
# appends it to the run's job summary.
#
# Aggregates workflow-run wall-clock over the lookback window per workflow so
# the most expensive lanes are visible at a glance. Wall-clock (updated_at minus
# created_at) is a proxy for spend: it includes queue time and reflects the
# serial critical path a change waits on, which is the quantity B8 targets.
#
# Requires: gh (authenticated via GH_TOKEN) and jq. Read-only: actions:read.
# Network calls are best-effort so a transient API hiccup degrades to a partial
# report rather than failing the scheduled lane.
set -euo pipefail

REPO="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
DAYS="${WORKCELL_CI_INSIGHTS_DAYS:-7}"
# Literal backtick for Markdown code spans, kept out of single-quoted format
# strings (matches scripts/check-doc-links.sh).
bt='`'

if ! [[ "${DAYS}" =~ ^[1-9][0-9]*$ ]]; then
  echo "cost-report: WORKCELL_CI_INSIGHTS_DAYS must be a positive integer, got '${DAYS}'" >&2
  exit 2
fi

since="$(date -u -d "${DAYS} days ago" +%Y-%m-%dT%H:%M:%SZ)"
generated="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

printf '## CI cost report (last %s days)\n\n' "${DAYS}"
printf '_Generated %s for %s%s%s. Wall-clock includes queue time._\n\n' "${generated}" "${bt}" "${REPO}" "${bt}"

runs="$(gh api --paginate --method GET \
  "repos/${REPO}/actions/runs" \
  -f "created=>${since}" \
  -f "per_page=100" \
  --jq '.workflow_runs[] | {name, created_at, updated_at}' \
  2>/dev/null || true)"

rows="$(jq -s -r '
  def hms($s): ($s | floor) as $t
    | "\(($t / 3600) | floor)h \((($t % 3600) / 60) | floor)m";
  map(. + {dur: (((.updated_at | fromdateiso8601) - (.created_at | fromdateiso8601)) | if . < 0 then 0 else . end)})
  | group_by(.name)
  | map({name: .[0].name, runs: length, total: (map(.dur) | add), avg: ((map(.dur) | add) / length)})
  | sort_by(-.total)
  | .[]
  | "| \(.name) | \(.runs) | \(hms(.total)) | \((.avg / 60) | floor)m |"
' <<<"${runs}" 2>/dev/null || true)"

if [[ -z "${rows}" ]]; then
  printf 'No workflow runs recorded in the window (or the runs API was unreachable).\n\n'
else
  printf '| Workflow | Runs | Total wall-clock | Avg / run |\n|---|---|---|---|\n%s\n\n' "${rows}"
fi
