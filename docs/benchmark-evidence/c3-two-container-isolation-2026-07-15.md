# C3 — two-session isolation evidence

Auditable evidence for the C3 isolation run recorded in
[`../1.0-readiness-review-draft.md`](../1.0-readiness-review-draft.md) §6 Platform
row. Two same-repo detached sessions were started with `--session-workspace
isolated` on `macos/arm64/local_vm/colima/strict` (profile `wcl-workcell-006e49ec`),
then `session show` was captured for each.

> **Not a concurrent run.** The two sessions started **33 s apart** (19:36:40 vs
> 19:37:13, below) and each taskless `codex` session exits within seconds, so
> session A had already exited before session B started — they did **not** overlap
> in time. This evidence therefore shows that the isolation *scheme* assigns
> **distinct** containers/worktrees/branches to two sessions, but it does **not**
> establish a live **concurrent** two-container run. This was the historical
> 2026-07-15 conclusion. The 2026-08-03 certification below closes that
> evidence gap with overlapping keepalive sessions.

## Invocation

```sh
export REPO="$HOME/src/workcell"   # a clean git worktree
./scripts/workcell session start --agent codex --target colima --workspace "$REPO" --session-workspace isolated
./scripts/workcell session start --agent codex --target colima --workspace "$REPO" --session-workspace isolated
./scripts/workcell session list    # -> the two session_ids below
for id in <ID1> <ID2>; do ./scripts/workcell session show --id "$id"; done
```

## Captured `session show` fields (verbatim; home path generalized to `$HOME`)

Session A — `20260715T193632Z-f4226b4e`:

```text
"session_id":            "20260715T193632Z-f4226b4e"
"target_provider":       "colima"
"target_assurance_class":"strict"
"status":                "failed"
"workspace_root":        "$HOME/src/workcell"
"worktree_path":         "$HOME/src/workcell/.git/workcell-sessions/20260715T193632Z-f4226b4e/repo"
"git_branch":            "workcell/session-20260715T193632Z-f4226b4e"
"container_name":        "workcell-codex-strict-repo-557c91b5"
"started_at":            "2026-07-15T19:36:40Z"
```

Session B — `20260715T193705Z-2ae8e294`:

```text
"session_id":            "20260715T193705Z-2ae8e294"
"target_provider":       "colima"
"target_assurance_class":"strict"
"status":                "failed"
"workspace_root":        "$HOME/src/workcell"
"worktree_path":         "$HOME/src/workcell/.git/workcell-sessions/20260715T193705Z-2ae8e294/repo"
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
- This preliminary capture is **structural** evidence only. It does **not**
  perform the runtime
  non-interference check required by
  [`../safe-path-expectations.md`](../safe-path-expectations.md). The certified
  2026-08-03 run below performs that check.

## Certified concurrent run (2026-08-03)

This local-operator certification ran on
`macos/arm64/local_vm/colima/strict`. It is bound to signed activation commit
`a26b750f8d8c7957fc15a3f5c164c2df28f25b46` and its exact control-plane tree
`726f485a609b88edcee7ee5ad88692d7aafdd501`. The certification was performed
before merge. This evidence record is not itself a shipped-status claim; verify
that the activation commit is reachable from `main` before treating it as
shipped.

The exact-tree run used the new maintainer entrypoint:

```sh
./scripts/certify-c3-parallel-sessions.sh \
  --workspace /path/to/clean/workload \
  --precommit-control-tree 726f485a609b88edcee7ee5ad88692d7aafdd501
```

The certifier used clean workload commit
`cc6bc6f8310d1f65ef03007024d4ec2e3f57fe7b` and recorded:

```text
date (UTC): 2026-08-03T18:17:15Z
Workcell launcher SHA-256: e414864af41a89527582c891bb777f513816b2cccbfd63a48106e0f8fab0126d
Docker client SHA-256: 49d98ab806e8678cd6341b09dad6389e5bcd8a46513de7651053bee3d8366e8d
target: local_vm/colima/strict
profile: wcl-c3-2305439552
session A: 20260803T181448Z-82c58950
container A: workcell-codex-strict-repo-e90ea0c8
branch A: workcell/session-20260803T181448Z-82c58950
session B: 20260803T181648Z-57e2dd25
container B: workcell-codex-strict-repo-8b984979
branch B: workcell/session-20260803T181648Z-57e2dd25
```

The exact worktree paths were certifier-owned isolated paths under the temporary
workload clone. They are intentionally generalized here rather than publishing
the maintainer host's private temporary-directory prefix.

The certifier proved all of the following in one run:

- sessions A and B overlapped and had distinct containers, isolated worktrees,
  and branches;
- a marker written through container A's `/workspace` was present in A's
  recorded host worktree and absent from both container B and B's host
  worktree; the symmetric B-to-A check also passed;
- each container's `/workspace` matched its recorded host worktree;
- session A stopped independently while session B remained running;
- both session records, containers, and isolated worktrees were removed; and
- the certifier-owned Colima profile, target state, and runtime-image-cache
  entries were removed.

An independent post-run audit then found no exact profile process or Colima
inventory entry, no exact Colima/profile/target/cache path, and no state-root
name matching either session or the profile. It also confirmed the certified
control-plane tree was unchanged.

Scope limitation: the sessions used Workcell's explicitly acknowledged
arbitrary-command keepalive path. This certifies the parallel strict runtime
boundaries and lifecycle behavior; it does not certify provider interaction.
