# Single owner of the open-refresh-PR overlap rule. Input: the JSON from
# `gh pr list --json title,url,headRefName,isCrossRepository`. A fork PR never
# counts. A legacy mixed PR blocks both kinds. A PR of the same kind blocks it.
def overlap($kind):
  map(select(.isCrossRepository | not)
      | select(.title == "Refresh pinned upstreams"
               or (.headRefName | test("^codex/upstream-refresh-([0-9]+$|" + $kind + "-)"))))
  | .[0];
