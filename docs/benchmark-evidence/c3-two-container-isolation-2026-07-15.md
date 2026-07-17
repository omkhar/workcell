# C3 — live two-container isolation, raw capture (2026-07-15)

Auditable evidence for the C3 live two-container run recorded in
[`../1.0-readiness-review-draft.md`](../1.0-readiness-review-draft.md) §6 Platform
row. Two concurrent same-repo detached sessions were started with
`--session-workspace isolated` on `macos/arm64/local_vm/colima/strict` (profile
`wcl-workcell-006e49ec`), then `session show` was captured for each.

## Invocation

```sh
export REPO="$HOME/src/workcell"   # a clean git worktree
./scripts/workcell session start --agent codex --target colima --workspace "$REPO" --session-workspace isolated
./scripts/workcell session start --agent codex --target colima --workspace "$REPO" --session-workspace isolated
./scripts/workcell session list    # -> the two session_ids below
for id in <ID1> <ID2>; do ./scripts/workcell session show --id "$id"; done
```

## Captured `session show` fields (verbatim)

Session A — `20260715T193632Z-f4226b4e`:

```
"session_id":            "20260715T193632Z-f4226b4e"
"target_provider":       "colima"
"target_assurance_class":"strict"
"status":                "failed"
"workspace_root":        "/Users/omkharanarasaratnam/src/workcell"
"worktree_path":         "/Users/omkharanarasaratnam/src/workcell/.git/workcell-sessions/20260715T193632Z-f4226b4e/repo"
"git_branch":            "workcell/session-20260715T193632Z-f4226b4e"
"container_name":        "workcell-codex-strict-repo-557c91b5"
"started_at":            "2026-07-15T19:36:40Z"
```

Session B — `20260715T193705Z-2ae8e294`:

```
"session_id":            "20260715T193705Z-2ae8e294"
"target_provider":       "colima"
"target_assurance_class":"strict"
"status":                "failed"
"workspace_root":        "/Users/omkharanarasaratnam/src/workcell"
"worktree_path":         "/Users/omkharanarasaratnam/src/workcell/.git/workcell-sessions/20260715T193705Z-2ae8e294/repo"
"git_branch":            "workcell/session-20260715T193705Z-2ae8e294"
"container_name":        "workcell-codex-strict-repo-0e6b0d64"
"started_at":            "2026-07-15T19:37:13Z"
```

## What this establishes — and what it does not

- **Distinct** `container_name`, `worktree_path`, and `git_branch` under one shared
  `workspace_root` → **structural** worktree-per-agent isolation on the strict path.
- `status: failed` is expected here: a detached `codex` started with no task exits
  non-zero within seconds. It does not affect the isolation attributes above, which
  are assigned at session-start time.
- This is **structural** evidence only. It does **not** perform the runtime
  non-interference check required by
  [`../safe-path-expectations.md`](../safe-path-expectations.md) — that a write
  inside session A's running container is not visible inside session B — which
  remains to be done before criterion 3 is signed off.
