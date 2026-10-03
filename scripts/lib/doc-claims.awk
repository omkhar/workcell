# Doc claim scanner: prints "PATH<TAB>doc<TAB>path" per cited path and
# "CLAIM<TAB>doc<TAB>line" per claim with no path span within 2 lines.
function trim_subject(t) {
  if (length(t) <= 100) return t
  t = substr(t, 1, 100)
  sub(/[[:space:]]+[^[:space:]]*$/, "", t)
  return t
}
function span_path(s, p) {
  if (s !~ /^(scripts|internal|\.github\/workflows)\//) return ""
  p = s
  sub(/[ :(].*$/, "", p)
  sub(/[.,;]+$/, "", p)
  if (p ~ /[*<>{}$]/) return ""
  return p
}
function spans_in(s, t, n, c) {
  n = 0
  while (match(s, /`[^`]+`/)) {
    t = substr(s, RSTART + 1, RLENGTH - 2)
    s = substr(s, RSTART + RLENGTH)
    c = span_path(t)
    if (c != "") n++
  }
  return n
}
/^[[:space:]]*```/ { fence = !fence; fenced[FNR] = 1; line[FNR] = $0; next }
{ fenced[FNR] = fence; line[FNR] = $0 }
END {
  for (i = 1; i <= FNR; i++) {
    if (fenced[i]) continue
    s = line[i]
    while (match(s, /`[^`]+`/)) {
      t = substr(s, RSTART + 1, RLENGTH - 2)
      s = substr(s, RSTART + RLENGTH)
      c = span_path(t)
      if (c != "") print "PATH\t" FILENAME "\t" c
    }
    if (tolower(line[i]) ~ /enforced by|rejects|refuses/) {
      ok = 0
      for (j = i - 2; j <= i + 2; j++) if (j >= 1 && j <= FNR && !fenced[j] && spans_in(line[j]) > 0) ok = 1
      if (!ok) {
        t = line[i]
        gsub(/[\t]+/, " ", t)
        sub(/^[[:space:]]+/, "", t)
        print "CLAIM\t" FILENAME "\t" trim_subject(t)
      }
    }
  }
}
