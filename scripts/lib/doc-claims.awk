# Doc claim scanner over md-unfenced.awk output for the file doc. Prints
# "doc<TAB>path" per cited path for workcell-citools doc-claims to probe, and
# appends "doc<TAB>unanchored-claim<TAB>sentence" to the file claims per
# enforcement sentence with no path span within 2 lines of it. A sentence runs
# across line wraps inside one paragraph, list item, quote or table row. A code
# span can continue onto later lines of its block, so each block is scanned as
# one text with its lines joined by spaces; a blank line, which includes a
# blanked fence, ends it, and so does each line that starts_block names.
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
# opens returns the length of the backtick run that starts a code span at the
# head of s, the rest of line i: a later run of the same length in the rest of
# the line or of its block closes it. It returns 0 when the run closes nothing,
# so the run is text.
function opens(i, s, n, j, t) {
  t = s
  for (j = i; ; j++) {
    if (j > i) {
      if (j > NR || raw[j] ~ /^[[:space:]]*$/ || starts_block(raw[j], raw[j - 1], raw[j + 1])) return 0
      t = raw[j]
    }
    while (match(t, /`+/)) {
      if (RLENGTH == n) return n
      t = substr(t, RSTART + RLENGTH)
    }
  }
}
# starts_block reports whether line s starts a Markdown block, so a code span
# open on the line before it, prev, never closes on it: an ATX heading, an HTML
# comment, a bullet item, an ordered item that starts at 1 or follows a list
# line, a table row whose next line, nxt, is a delimiter row or that follows a
# table row, the first line of a blockquote, or the line after a heading, a
# setext underline or a thematic break. CommonMark lets no other ordered item
# or table interrupt a paragraph, so a wrapped line that starts with 10) or |
# stays in the span.
function starts_block(s, prev, nxt) {
  if (s ~ /^ ? ? ?<!--/ || s ~ /^ ? ? ?#+([ \t]|$)/ || s ~ /^ ? ? ?[-*+]([ \t]|$)/ || s ~ /^ ? ? ?0*1[.)]([ \t]|$)/) return 1
  if (s ~ /^ ? ? ?[0-9]+[.)]([ \t]|$)/ && (prev ~ /^ ? ? ?([-*+]|[0-9]+[.)])([ \t]|$)/ || prev ~ /^[ \t]/)) return 1
  if (s ~ /^[[:space:]]*\|/ && (prev ~ /^[[:space:]]*\|/ || nxt ~ /\|/ && nxt ~ /^[[:space:]]*\|?([[:space:]]*:?-+:?[[:space:]]*\|?)+[[:space:]]*$/)) return 1
  if (s ~ /^ ? ? ?>/ && prev !~ /^ ? ? ?>/) return 1
  return prev ~ /^ ? ? ?#+([ \t]|$)/ || prev ~ /^ ? ? ?(=+|-+)[ \t]*$/ || prev ~ /^ ? ? ?[-*_][ \t]*[-*_][ \t]*[-*_][-*_ \t]*$/
}
# Each line is kept, and every pass runs at the end, because a code span needs
# the lines after its opener.
{ raw[NR] = $0 }
END {
  # An HTML comment is hidden from readers, so it can neither anchor a claim
  # nor make one. It is cut here, in this scanner only, because md-unfenced.awk
  # also feeds the link check, which keeps checking links inside comments. Each
  # line stays, so line numbers hold; a comment that never closes hides the
  # rest. A line that is empty only because a comment spans it is skip[], not
  # a blank line, and the text after a closer is glue[] to the text before the
  # opener, as rendered, so a comment inside a word does not split the word. A
  # <!-- inside a code span is code, as Markdown renders it, so a span that
  # opens first is copied whole; tick is the length of a span still open at
  # the end of a line, and a block boundary ends it.
  hidden = 0
  tick = 0
  for (i = 1; i <= NR; i++) {
    s = raw[i]
    out = ""
    glue[i] = hidden
    wasopen = hidden
    if (s ~ /^[[:space:]]*$/ || i > 1 && starts_block(s, raw[i - 1], raw[i + 1])) tick = 0
    while (s != "") {
      if (hidden) {
        c = index(s, "-->")
        if (c == 0) break
        s = substr(s, c + 3)
        hidden = 0
        continue
      }
      if (tick) {
        closed = 0
        t = s
        o = 0
        while (match(t, /`+/)) {
          if (RLENGTH == tick) {
            closed = o + RSTART + RLENGTH - 1
            break
          }
          o += RSTART + RLENGTH - 1
          t = substr(s, o + 1)
        }
        if (!closed) {
          out = out s
          break
        }
        out = out substr(s, 1, closed)
        s = substr(s, closed + 1)
        tick = 0
        continue
      }
      c = index(s, "<!--")
      b = 0
      t = s
      o = 0
      while (match(t, /`+/)) {
        bs = 0
        pre = substr(s, 1, o + RSTART - 1)
        while (bs < length(pre) && substr(pre, length(pre) - bs, 1) == "\\") bs++
        if (bs % 2 == 0) {
          b = o + RSTART
          n = RLENGTH
          break
        }
        o += RSTART
        t = substr(s, o + 1)
      }
      if (b && (c == 0 || b < c)) {
        out = out substr(s, 1, b + n - 1)
        s = substr(s, b + n)
        tick = opens(i, s, n)
        continue
      }
      if (c) {
        out = out substr(s, 1, c - 1)
        s = substr(s, c + 4)
        hidden = 1
        continue
      }
      out = out s
      break
    }
    line[i] = out
    skip[i] = out ~ /^[[:space:]]*$/ && (wasopen || hidden)
  }
  # A code span may continue onto later lines of its block, so each block is
  # scanned as one text with its lines joined by spaces. A blank line, which
  # includes a blanked fence, ends a block, and so does each line that
  # starts_block names.
  para = ""
  for (i = 1; i <= NR; i++) {
    if (skip[i] && para == "") continue
    if (skip[i]) {
      pat[i] = length(para) + 1
      plast = i
      continue
    }
    if (line[i] ~ /^[[:space:]]*$/) {
      scan(para)
      para = ""
      continue
    }
    if (para != "" && i > 1 && starts_block(line[i], line[i - 1], line[i + 1])) {
      scan(para)
      para = ""
    }
    if (para == "") pfirst = i
    sep = (para == "" || glue[i]) ? "" : " "
    pat[i] = length(para) + length(sep) + 1
    para = para sep line[i]
    plast = i
  }
  scan(para)
  for (i = 1; i <= NR + 1; i++) {
    if (skip[i]) {
      at[i] = length(text) + 1
      continue
    }
    if (i > NR || line[i] ~ /^[[:space:]]*$/ || line[i] ~ /^[[:space:]]*([-*+>|#]|[0-9]+[.)])/) {
      if (text != "") flush(i)
      if (i > NR || line[i] ~ /^[[:space:]]*$/) continue
    }
    if (text == "") block_start = i
    if (glue[i]) sub(/ $/, "", text)
    at[i] = length(text) + 1
    text = text line[i] " "
  }
}
