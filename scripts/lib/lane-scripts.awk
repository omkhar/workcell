# Lane script lister: prints each scripts/*.sh path that a lane file runs as a
# command, one per line. The lane files are scripts/validate-repo.sh, the
# scripts/ci/job-*.sh scripts and the workflows. A path counts only as the
# first word of a command, so a comment, an echo argument, an array member, a
# case pattern, a continued argument or a longer name such as x.sh.bak does not.
# ponytail: a line reader, not a shell parser; a ; inside a quoted string, a
# heredoc body or an uncalled function still counts. The Go ShellInvocations
# owner stops at the first exit it cannot place (validate-repo.sh line 65), so
# it cannot list a whole lane yet; move this check there once it can.
FNR == 1 { array = 0; cont = 0 }
{
  s = $0
  was_cont = cont
  cont = (s ~ /\\[[:space:]]*$/)
  if (was_cont) next
  sub(/^[[:space:]]+/, "", s)
  if (array) {
    if (s ~ /^\)/) array = 0
    next
  }
  if (s ~ /^[A-Za-z_][A-Za-z0-9_]*\+?=\([[:space:]]*$/) {
    array = 1
    next
  }
  if (s ~ /^#/) next
  if (s ~ /^[^[:space:]()]+([[:space:]]*\|[[:space:]]*[^[:space:]()]+)*\)$/) next
  sub(/[[:space:]]#.*$/, "", s)
  sub(/^-[[:space:]]+run:[[:space:]]*/, "", s)
  sub(/^run:[[:space:]]*/, "", s)
  n = split(s, seg, /;|&&|\|\||\||\$\(|\(|`/)
  for (i = 1; i <= n; i++) {
    m = split(seg[i], w, /[[:space:]]+/)
    for (k = 1; k <= m; k++) {
      if (w[k] == "" || w[k] ~ /^-/ || w[k] ~ /^[A-Za-z_][A-Za-z0-9_]*=/) continue
      if (w[k] ~ /^(if|then|do|else|elif|while|until|!|exec|time|command|env|bash|sh)$/) continue
      break
    }
    if (k > m) continue
    p = w[k]
    gsub(/["']/, "", p)
    sub(/^\$\{?[A-Za-z_][A-Za-z0-9_]*\}?\//, "", p)
    sub(/^\.\//, "", p)
    if (p ~ /^scripts\/[A-Za-z0-9_.\/-]+\.sh$/) print p
  }
}
