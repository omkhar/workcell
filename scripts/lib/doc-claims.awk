# Doc claim scanner over md-unfenced.awk output for the file doc. Prints
# "doc<TAB>path" per cited path for workcell-citools doc-claims to probe, and
# appends "doc<TAB>unanchored-claim<TAB>sentence" to the file claims per
# enforcement sentence with no path span within 2 lines of it. A sentence runs
# across line wraps inside one paragraph, list item, quote or table row. A code
# span can continue onto later lines of its paragraph, so each paragraph is
# scanned as one text with its lines joined by spaces; a blank line, which
# includes a blanked fence, ends it.
function span_path(s, p) {
  gsub(/^[[:space:]]+|[[:space:]]+$/, "", s)
  sub(/^\.\//, "", s)
  p = s
  sub(/[ :(].*$/, "", p)
  sub(/[.,;]+$/, "", p)
  if (p ~ /[*<>{}$]/) return ""
  # A path with a .. component that names a checked area anywhere is printed
  # whatever it starts with, so the probe reports it as escaping.
  if (p ~ /(^|\/)\.\.(\/|$)/ && p ~ /(^|\/)(scripts|internal|\.github\/workflows)\//) return p
  if (s !~ /^(scripts|internal|\.github\/workflows)\//) return ""
  return p
}
function claim(s) {
  return tolower(s) ~ /(^|[^a-z])(enforced by|enforces|rejects|refuses|blocks|denies|fails|requires|guarantees|prevents)([^a-z]|$)/
}
function anchored(first, last, j) {
  for (j = first - 2; j <= last + 2; j++) if (spans[j]) return 1
  return 0
}
# line_of returns the number of the paragraph line that holds offset o of the
# paragraph text; pat[k] is the offset where line k starts.
function line_of(o, k, l) {
  l = pfirst
  for (k = pfirst; k <= plast; k++) if (pat[k] <= o) l = k
  return l
}
# scan prints the cited path of each code span in the paragraph text s and
# counts it on the line where the span opens. A run of n backticks opens a span
# that only a later run of exactly n backticks closes, as in Markdown; an
# unmatched run is text. Outside a span, a backtick behind an odd number of
# backslashes is escaped text, and it and its backslash become spaces of the
# same length; inside a span a backslash is literal, so a closer is never
# escaped.
function scan(s, n, rest, off, at, c, pos, opened, pre, bs) {
  pos = 0
  while (match(s, /`+/)) {
    pre = substr(s, 1, RSTART - 1)
    bs = 0
    while (bs < length(pre) && substr(pre, length(pre) - bs, 1) == "\\") bs++
    if (bs % 2 == 1) {
      s = substr(s, 1, RSTART - 2) "  " substr(s, RSTART + 1)
      continue
    }
    n = RLENGTH
    opened = pos + RSTART
    rest = substr(s, RSTART + RLENGTH)
    pos += RSTART + RLENGTH - 1
    off = 0
    at = 0
    while (match(substr(rest, off + 1), /`+/)) {
      if (RLENGTH == n) {
        at = off + RSTART
        break
      }
      off += RSTART + RLENGTH - 1
    }
    if (at == 0) {
      s = rest
      continue
    }
    c = span_path(substr(rest, 1, at - 1))
    if (c != "") {
      spans[line_of(opened)]++
      print doc "\t" c
    }
    s = substr(rest, at + n)
    pos += at + n - 1
  }
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
    if (claim(s) && !anchored(first, last)) print doc "\tunanchored-claim\t" s >>claims
    rest = substr(rest, len + 1)
    start += len
  }
  text = ""
}
{ line[NR] = $0 }
/^[[:space:]]*$/ { scan(para); para = ""; next }
{
  if (para == "") pfirst = NR
  pat[NR] = length(para) + (para == "" ? 1 : 2)
  para = para (para == "" ? "" : " ") $0
  plast = NR
}
END {
  scan(para)
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
