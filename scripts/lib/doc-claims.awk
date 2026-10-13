# Doc claim scanner over md-unfenced.awk output for the file doc. Prints
# "doc<TAB>path" per cited path for workcell-citools doc-claims to probe. A
# code span can continue onto later lines of its paragraph, so each paragraph
# is scanned as one text with its lines joined by spaces; a blank line, which
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
# scan prints the cited path of each code span in s. A run of n backticks opens
# a span that only a later run of exactly n backticks closes, as in Markdown; an
# unmatched run is text. Outside a span, a backtick behind an odd number of
# backslashes is escaped text, and it and its backslash become spaces of the
# same length; inside a span a backslash is literal, so a closer is never
# escaped.
function scan(s, n, rest, off, at, c, pre, bs) {
  while (match(s, /`+/)) {
    pre = substr(s, 1, RSTART - 1)
    bs = 0
    while (bs < length(pre) && substr(pre, length(pre) - bs, 1) == "\\") bs++
    if (bs % 2 == 1) {
      s = substr(s, 1, RSTART - 2) "  " substr(s, RSTART + 1)
      continue
    }
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
