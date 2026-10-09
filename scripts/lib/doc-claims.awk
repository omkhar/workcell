# Doc claim scanner over md-unfenced.awk output for the file doc. Prints
# "doc<TAB>path" per cited path for workcell-citools doc-claims to probe. A
# code span can continue onto later lines of its paragraph, so each paragraph
# is scanned as one text with its lines joined by spaces; a blank line, which
# includes a blanked fence, ends it.
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
# scan prints the cited path of each code span in s. A run of n backticks opens
# a span that only a later run of exactly n backticks closes, as in Markdown; an
# unmatched run is text.
function scan(s, n, rest, off, at, c) {
  while (match(s, /`+/)) {
    n = RLENGTH
    rest = substr(s, RSTART + RLENGTH)
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
    if (c != "") print doc "\t" c
    s = substr(rest, at + n)
  }
}
/^[[:space:]]*$/ { scan(para); para = ""; next }
{ para = para (para == "" ? "" : " ") $0 }
END { scan(para) }
