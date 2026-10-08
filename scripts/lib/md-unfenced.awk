# Prints each Markdown line, with fenced code blanked so line numbers stay put.
# A fence line has at most three leading spaces and no tab, then three or more
# backticks, an optional info string and no other backtick, or three or more
# tildes. Deeper indentation is an indented code block, so its text stays
# checked; a list fence indented four or more spaces is read as text too. Only
# the opening character closes a fence. An inline ```code``` span is text.
/^ ? ? ?```+[^`]*$/ && fence != "~" { fence = fence ? "" : "`"; print ""; next }
/^ ? ? ?~~~+/ && fence != "`" { fence = fence ? "" : "~"; print ""; next }
fence { print ""; next }
{ print }
