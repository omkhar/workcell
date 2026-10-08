# Doc claim scanner over md-unfenced.awk output for the file doc. Prints
# "PATH<TAB>doc<TAB>path" per cited path, and "ESCAPE<TAB>doc<TAB>path" per path
# with a .. component, which may leave the repository and anchors nothing.
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
    if (c ~ /(^|\/)\.\.(\/|$)/) {
      print "ESCAPE\t" doc "\t" c
    } else if (c != "") {
      print "PATH\t" doc "\t" c
    }
  }
}
