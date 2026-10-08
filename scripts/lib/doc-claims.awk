# Doc claim scanner over md-unfenced.awk output for the file doc. Prints
# "doc<TAB>path" per cited path for workcell-citools doc-claims to probe.
function span_path(s, p) {
  gsub(/^[[:space:]]+|[[:space:]]+$/, "", s)
  sub(/^\.\//, "", s)
  if (s !~ /^(scripts|internal|\.github\/workflows)\//) return ""
  p = s
  sub(/[ :(].*$/, "", p)
  sub(/[.,;]+$/, "", p)
  if (p ~ /[*<>{}$]/) return ""
  return p
}
{
  s = $0
  while (match(s, /`[^`]+`/)) {
    c = span_path(substr(s, RSTART + 1, RLENGTH - 2))
    s = substr(s, RSTART + RLENGTH)
    if (c != "") print doc "\t" c
  }
}
