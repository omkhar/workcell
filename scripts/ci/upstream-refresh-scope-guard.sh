#!/usr/bin/env -S BASH_ENV= ENV= bash
# Scope guard for automatic upstream-refresh merges.
#
# Usage: upstream-refresh-scope-guard.sh PATCH_FILE
#
# Exit 0 only when every change in the git patch is inside the agent-bump
# surface. Exit 1 lists each out-of-scope change. A patch that is out of scope
# is still a valid PR, but a human must review it.
set -euo pipefail

if [[ $# -ne 1 || ! -f "$1" ]]; then
  echo "Usage: $0 PATCH_FILE" >&2
  exit 2
fi

# Dockerfile lines may change only when they are provider pin ARG lines.
dockerfile_path="runtime/container/Dockerfile"
dockerfile_line_re='^[-+]ARG (CLAUDE|CODEX|COPILOT|GEMINI)_[A-Z0-9_]*(VERSION|SHA256)=[A-Za-z0-9._+:@/-]*$'
path_res='^runtime/container/providers[^/]*/package(-lock)?\.json$|^tests/fixtures/flags/[^/]+$|^tests/fixtures/codex-subcommands\.txt$|^runtime/container/control-plane-manifest\.json$'

# The patterns travel in the environment because awk -v would process backslashes.
DOCKERFILE_PATH="${dockerfile_path}" DOCKERFILE_LINE_RE="${dockerfile_line_re}" PATH_RES="${path_res}" awk '
  BEGIN { dockerfile = ENVIRON["DOCKERFILE_PATH"]; dockerfile_re = ENVIRON["DOCKERFILE_LINE_RE"]; path_re = ENVIRON["PATH_RES"] }
  function fail(msg) { print "out of scope: " msg > "/dev/stderr"; bad = 1 }
  /^diff --git / {
    in_hunk = 0; file = ""
    if (NF != 4 || substr($3, 1, 2) != "a/" || substr($4, 1, 2) != "b/" || substr($3, 3) != substr($4, 3)) {
      fail("unsupported diff header (rename, copy, or unusual path): " $0); next
    }
    file = substr($3, 3); seen = 1
    if (file != dockerfile && file !~ path_re) fail("path " file)
    next
  }
  in_hunk == 0 && /^(old mode|new mode|deleted file mode|rename |copy |similarity |dissimilarity )/ { fail(file ": " $0); next }
  in_hunk == 0 && /^new file mode / { if ($4 != "100644") fail(file ": " $0); next }
  in_hunk == 0 && /^(GIT binary patch|Binary files )/ { fail(file ": binary change"); next }
  in_hunk == 0 && /^index / { if (NF == 3 && $3 != "100644") fail(file ": " $0); next }
  /^@@ / { in_hunk = 1; next }
  in_hunk == 1 && file == dockerfile && /^[-+]/ && $0 !~ dockerfile_re { fail(file ": line " $0) }
  END { if (!seen) { print "out of scope: empty patch" > "/dev/stderr"; bad = 1 } exit bad }
' "$1"
echo "scope-guard: patch is inside the agent-bump surface"
