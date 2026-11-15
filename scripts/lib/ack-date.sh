# shellcheck shell=bash
# Dated acknowledgements for the tests that run scripts/workcell with
# --ack-breakglass, --ack-arbitrary-command or --ack-repo-mcp. The launcher
# accepts such a date only when it is today's UTC date at the moment it checks
# the flag, and refuses any other date with exit status 2. A test that reads
# the date once and passes it later is therefore refused once the run crosses
# midnight UTC, so a test writes the placeholder @ACK_TODAY_UTC@ and runs the
# command through workcell_with_ack_today_utc, which reads the date at each use.

# workcell_ack_today_utc prints today's UTC date in the form an --ack-* flag
# takes, and fails when date gives anything else.
workcell_ack_today_utc() {
  local today=""

  today="$(date -u +%Y-%m-%d)" || return 1
  if [[ ! "${today}" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
    echo "date -u +%Y-%m-%d printed '${today}', not a YYYY-MM-DD date" >&2
    return 1
  fi
  printf '%s\n' "${today}"
}

# workcell_with_ack_today_utc COMMAND [ARG...] replaces each @ACK_TODAY_UTC@ in
# the arguments with today's UTC date, read just before the run, and runs the
# command. The launcher can still check the flag after midnight UTC when the
# run starts just before it, so when the command exits 2 and today's UTC date
# is no longer the date it was given, it runs once more with the new date and
# returns that run's status. Any other result is returned as is: a failure on
# an unchanged date, any other status, or a command with no placeholder. A
# rejection that does not depend on the date fails both runs.
#
# The rerun keys on exit status 2 alone, because the callers own their
# redirections and the helper cannot read the refusal's text. So a run that
# crosses midnight and exits 2 for another reason is run once more, and a
# check on the output can then see lines from both runs; a failure that
# repeats still fails. Run only a command that is safe to repeat and that does
# not read stdin, which the second run would find empty. A shell function runs here with
# errexit off, so pass one whose body is a single command; a function with
# several assertions would no longer stop at the first one that fails. Both
# runs write to the caller's redirections, the second after a note on stderr.
workcell_with_ack_today_utc() {
  # The locals carry a prefix because bash shows a caller's locals to the
  # functions it calls, so a wrapped function that sets status or ack would
  # otherwise change what this one returns.
  local _wcack_ack="" _wcack_today="" _wcack_arg=""
  local _wcack_has_placeholder=0
  local _wcack_status=0

  for _wcack_arg in "$@"; do
    [[ "${_wcack_arg}" != *@ACK_TODAY_UTC@* ]] || _wcack_has_placeholder=1
  done
  _wcack_ack="$(workcell_ack_today_utc)" || return 1
  if "${@//@ACK_TODAY_UTC@/${_wcack_ack}}"; then
    return 0
  else
    _wcack_status=$?
  fi
  [[ "${_wcack_status}" -eq 2 && "${_wcack_has_placeholder}" -eq 1 ]] || return "${_wcack_status}"
  _wcack_today="$(workcell_ack_today_utc)" || return 1
  [[ "${_wcack_today}" != "${_wcack_ack}" ]] || return "${_wcack_status}"
  echo "The UTC date moved from ${_wcack_ack} to ${_wcack_today} during the run; running it once more with ${_wcack_today}." >&2
  if "${@//@ACK_TODAY_UTC@/${_wcack_today}}"; then
    return 0
  else
    return "$?"
  fi
}
