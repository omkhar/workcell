# Prints each Markdown line, with fenced code blanked so line numbers stay put.
# A fence line is three or more backticks, an optional info string and no other
# backtick. An inline ```code``` span at column zero is text, not a fence.
/^[[:space:]]*```+[^`]*$/ { fence = !fence; print ""; next }
fence { print ""; next }
{ print }
