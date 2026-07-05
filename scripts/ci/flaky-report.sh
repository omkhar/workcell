#!/usr/bin/env -S BASH_ENV= ENV= bash
# Weekly flaky-test report for the CI insights lane (.github/workflows/ci-insights.yml).
# Emits Markdown to stdout; the workflow appends it to the run's job summary.
#
# Two signals are combined:
#   1. Open issues labeled `flaky-test` — the human-curated tracked list. File a
#      `flaky-test` issue when a test/lane fails nondeterministically so it shows
#      up here until fixed and closed.
#   2. Workflow runs in the lookback window that needed a re-run (run_attempt > 1)
#      or concluded in failure on the default branch — an empirical flake signal
#      derived from CI history.
#
# Requires: gh (authenticated via GH_TOKEN) and jq. Read-only: actions:read for
# run history and issues read for the tracked list. Network calls are best-effort
# so a transient API hiccup degrades to a partial report rather than failing the
# scheduled lane.
set -euo pipefail

REPO="${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
DAYS="${WORKCELL_CI_INSIGHTS_DAYS:-7}"
# Literal backtick for Markdown code spans, kept out of single-quoted format
# strings so it is never mistaken for a command substitution (matches the
# convention in scripts/check-doc-links.sh).
bt='`'

if ! [[ "${DAYS}" =~ ^[1-9][0-9]*$ ]]; then
  echo "flaky-report: WORKCELL_CI_INSIGHTS_DAYS must be a positive integer, got '${DAYS}'" >&2
  exit 2
fi

since="$(date -u -d "${DAYS} days ago" +%Y-%m-%dT%H:%M:%SZ)"
generated="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

printf '## Flaky-test report (last %s days)\n\n' "${DAYS}"
printf '_Generated %s for %s%s%s._\n\n' "${generated}" "${bt}" "${REPO}" "${bt}"

# 1. Human-curated tracked flaky-test issues.
printf '### Tracked %sflaky-test%s issues\n\n' "${bt}" "${bt}"
issues="$(gh api --paginate \
  "repos/${REPO}/issues?state=open&labels=flaky-test&per_page=100" 2>/dev/null || echo '[]')"
issue_rows="$(jq -s -r '
  (add // [])
  | map(select(has("pull_request") | not))
  | sort_by(.updated_at) | reverse
  | .[]
  | "| [#\(.number)](\(.html_url)) | \(.title | gsub("[|]"; "\\|")) | \(.updated_at) |"
' <<<"${issues}" 2>/dev/null || true)"
if [[ -z "${issue_rows}" ]]; then
  printf 'No open issues labeled %sflaky-test%s. File one when a lane fails nondeterministically.\n\n' "${bt}" "${bt}"
else
  printf '| Issue | Title | Updated |\n|---|---|---|\n%s\n\n' "${issue_rows}"
fi

# 2. Empirical flake signal from workflow-run history.
printf '### Re-run / failed workflow runs\n\n'
runs="$(gh api --paginate --method GET \
  "repos/${REPO}/actions/runs" \
  -f "created=>${since}" \
  -f "per_page=100" \
  --jq '.workflow_runs[] | {name, head_branch, run_attempt, conclusion, event, html_url}' \
  2>/dev/null || true)"
run_rows="$(jq -s -r '
  map(select((.run_attempt // 1) > 1 or .conclusion == "failure"))
  | group_by(.name)
  | map({
      name: .[0].name,
      reruns: (map(select((.run_attempt // 1) > 1)) | length),
      failures: (map(select(.conclusion == "failure")) | length)
    })
  | sort_by(-(.reruns + .failures))
  | .[]
  | "| \(.name) | \(.reruns) | \(.failures) |"
' <<<"${runs}" 2>/dev/null || true)"
if [[ -z "${run_rows}" ]]; then
  printf 'No re-run or failed workflow runs in the window. \xE2\x9C\x93\n\n'
else
  printf '| Workflow | Re-runs (attempt > 1) | Failed runs |\n|---|---|---|\n%s\n\n' "${run_rows}"
  printf '_Re-runs and failures are candidates, not confirmed flakes: a re-run that then passed, or a failure on a red change, both surface here. Triage and, if nondeterministic, open a %sflaky-test%s issue._\n\n' "${bt}" "${bt}"
fi
