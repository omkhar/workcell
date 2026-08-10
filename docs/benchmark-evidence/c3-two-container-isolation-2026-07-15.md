# C3 Two-Session Isolation Evidence

This record contains a preliminary capture from 2026-07-15 and a certified run
from 2026-08-03.

## Preliminary capture

The first capture started two isolated detached sessions on
`macos/arm64/local_vm/colima/strict`.

The sessions started 33 seconds apart. They did not overlap. The capture shows
different container, worktree, and branch identifiers. It does not show
concurrent isolation.

### Preliminary command

```sh
export REPO="$HOME/src/workcell"   # a clean git worktree
./scripts/workcell session start --agent codex --target colima --workspace "$REPO" --session-workspace isolated
./scripts/workcell session start --agent codex --target colima --workspace "$REPO" --session-workspace isolated
./scripts/workcell session list    # -> the two session_ids below
for id in <ID1> <ID2>; do ./scripts/workcell session show --id "$id"; done
```

### Captured fields

These normalized fields preserve the captured values. They do not preserve the
command output format.

Session A:

```text
session_id=20260715T193632Z-f4226b4e
target_provider=colima
target_assurance_class=strict
status=failed
workspace_root=$HOME/src/workcell
worktree_path=$HOME/src/workcell/.git/workcell-sessions/20260715T193632Z-f4226b4e/repo
git_branch=workcell/session-20260715T193632Z-f4226b4e
container_name=workcell-codex-strict-repo-557c91b5
started_at=2026-07-15T19:36:40Z
```

Session B:

```text
session_id=20260715T193705Z-2ae8e294
target_provider=colima
target_assurance_class=strict
status=failed
workspace_root=$HOME/src/workcell
worktree_path=$HOME/src/workcell/.git/workcell-sessions/20260715T193705Z-2ae8e294/repo
git_branch=workcell/session-20260715T193705Z-2ae8e294
container_name=workcell-codex-strict-repo-0e6b0d64
started_at=2026-07-15T19:37:13Z
```

In this capture, a detached Codex session without a task exited with a failure
status. The result did not change the resource identifiers that Workcell
assigned at session start.

## Certified concurrent run

The maintainer ran the C3 certifier on 2026-08-03. The target was
`macos/arm64/local_vm/colima/strict`.

The evidence binds to these Git objects:

| Item | Value |
|---|---|
| Signed activation commit | `a26b750f8d8c7957fc15a3f5c164c2df28f25b46` |
| Control-plane tree | `726f485a609b88edcee7ee5ad88692d7aafdd501` |
| Clean workload commit | `cc6bc6f8310d1f65ef03007024d4ec2e3f57fe7b` |

The activation commit is reachable from `main`.

### Command

```sh
./scripts/certify-c3-parallel-sessions.sh \
  --workspace /path/to/clean/workload \
  --precommit-control-tree 726f485a609b88edcee7ee5ad88692d7aafdd501
```

### Recorded result

```text
date_utc=2026-08-03T18:17:15Z
launcher_sha256=e414864af41a89527582c891bb777f513816b2cccbfd63a48106e0f8fab0126d
docker_client_sha256=49d98ab806e8678cd6341b09dad6389e5bcd8a46513de7651053bee3d8366e8d
target=local_vm/colima/strict
profile=wcl-c3-2305439552
session_a=20260803T181448Z-82c58950
container_a=workcell-codex-strict-repo-e90ea0c8
branch_a=workcell/session-20260803T181448Z-82c58950
session_b=20260803T181648Z-57e2dd25
container_b=workcell-codex-strict-repo-8b984979
branch_b=workcell/session-20260803T181648Z-57e2dd25
```

The certifier proved these properties:

- Both sessions ran at the same time.
- Each session had a different container, worktree, and branch.
- A marker from session A did not occur in session B.
- A marker from session B did not occur in session A.
- Each marker occurred in its own session and host worktree.
- Each container workspace matched its recorded host worktree.
- Session A stopped while session B continued to run.
- Cleanup removed both session records, containers, and isolated worktrees.
- Cleanup removed the certifier Colima profile, target state, and image cache.
- The control-plane tree did not change during certification.

An independent audit found no process or inventory entry for the exact profile.
It found no Colima, profile, target, or cache path for that profile. It found no
state-root name for either session or the profile. The audit also confirmed
that the certified control-plane tree did not change.

## Limit

The sessions used the acknowledged keepalive path for arbitrary commands. The run
certifies parallel runtime boundaries and lifecycle behavior. It does not
certify provider interaction. Both containers shared one Colima VM and kernel.
This run does not certify VM-level separation.

See the [safe-path expectations](../safe-path-expectations.md) for the C3
requirement.
