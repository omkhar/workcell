# Prints each Markdown line, with fenced code blanked so line numbers stay put.
# A fence opens at most three spaces in, with no tab: three or more backticks,
# an optional info string and no other backtick, or three or more tildes. Only
# a line of the same character, at least as long and with no info string,
# closes it. Deeper indentation is an indented code block, so its text stays
# checked. An inline ```code``` span is text.
!fence && /^ ? ? ?(```+[^`]*|~~~+.*)$/ {
  text = $0; sub(/^ */, "", text); fence = substr(text, 1, 1)
  match(text, "^" fence "+"); size = RLENGTH; print ""; next
}
fence {
  text = $0; sub(/^ ? ? ?/, "", text); sub(/[ \t]+$/, "", text)
  long = length(text) >= size; gsub(fence, "", text)
  if (text == "" && long) fence = ""
  print ""; next
}
{ print }
