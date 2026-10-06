# Doc claim scanner over md-unfenced.awk output for the file doc. Prints
# "PATH<TAB>doc<TAB>path" per cited path and "CLAIM<TAB>doc<TAB>sentence" per
# enforcement sentence with no path span within 2 lines of it. A sentence runs
# across line wraps inside one paragraph, list item, quote or table row.
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
function claim(s) {
  return tolower(s) ~ /(^|[^a-z])(enforced by|enforces|rejects|refuses|blocks|denies|fails|requires|guarantees|prevents)([^a-z]|$)/
}
function anchored(first, last, j) {
  for (j = first - 2; j <= last + 2; j++) if (spans[j]) return 1
  return 0
}
# Splits the block that ends before line upto into sentences and reports each
# unanchored claim. at[k] is the block offset where block line k starts.
function flush(upto, rest, start, len, k, first, last, s) {
  rest = text
  start = 1
  while (rest != "") {
    if (match(rest, /[.!?]+[[:space:]]+/)) {
      len = RSTART + RLENGTH - 1
    } else {
      len = length(rest)
    }
    s = substr(rest, 1, len)
    first = block_start
    last = block_start
    for (k = block_start; k < upto; k++) {
      if (at[k] <= start) first = k
      if (at[k] <= start + len - 1) last = k
    }
    gsub(/[[:space:]]+/, " ", s)
    sub(/^ /, "", s)
    sub(/ $/, "", s)
    if (claim(s) && !anchored(first, last)) print "CLAIM\t" doc "\t" s
    rest = substr(rest, len + 1)
    start += len
  }
  text = ""
}
{
  line[NR] = $0
  s = $0
  while (match(s, /`[^`]+`/)) {
    c = span_path(substr(s, RSTART + 1, RLENGTH - 2))
    s = substr(s, RSTART + RLENGTH)
    if (c != "") {
      spans[NR]++
      print "PATH\t" doc "\t" c
    }
  }
}
END {
  for (i = 1; i <= NR + 1; i++) {
    if (i > NR || line[i] ~ /^[[:space:]]*$/ || line[i] ~ /^[[:space:]]*([-*+>|#]|[0-9]+[.)])/) {
      if (text != "") flush(i)
      if (i > NR || line[i] ~ /^[[:space:]]*$/) continue
    }
    if (text == "") block_start = i
    at[i] = length(text) + 1
    text = text line[i] " "
  }
}
