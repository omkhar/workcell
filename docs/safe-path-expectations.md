# Safe-path Expectations

The safe path has these properties:

- Workcell starts the selected provider inside the bounded runtime. You do not
  start a container and then attach the provider.
- `publish-pr` runs on the host. It enforces signed ranges, the pull request
  shape gate, the `main` base, and explicit exceptions. Thus, GitHub publication
  stays outside the Tier 1 container.
- Completed and aborted launches produce durable host session records.
- `workcell session diff` compares the current workspace with the clean Git
  base at launch. It stops if the launch was dirty, had no recorded Git base,
  or did not use a self-contained Git worktree.
- Debug logs, file traces, and audit transcripts are off by default. Their
  options are explicit lower-assurance choices.

## Publish a Workcell pull request

For this repository, first create fresh `pr-parity` evidence. The
`--allow-dirty` option validates live worktree changes before the wrapper
creates their signed commit. Then use the repository wrapper:

```bash
./scripts/pre-merge.sh --profile pr-parity --allow-dirty
./scripts/repo-publish-pr.sh --workspace /path/to/repo --branch feature/name \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
```

`workcell publish-pr` is the lower-level host-side helper. Use it directly for
operator repositories that do not carry Workcell's repo-local parity wrapper,
or for the explicitly lower-assurance non-`main` draft path.

```bash
workcell publish-pr --workspace /path/to/repo --branch feature/name \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
```

After a verified push, publication can reuse an open pull request with the same
repository, base, and branch. It preserves the title, body, labels, and draft
state. It stops if that state violates the base or shape gate. `origin` must
have exactly one push destination. GitHub commands use a credential-free
repository selector from that destination.

For a certified adapter exception in this repository, first create parity
evidence with the matching label. Then publish through the repository wrapper:

```bash
# Certified adapter change that cannot keep valid evidence after a split.
./scripts/pre-merge.sh --profile pr-parity \
  --allow-dirty --label approved-large-certified-adapter
./scripts/repo-publish-pr.sh --workspace /path/to/repo --branch feature/name \
  --approved-large-certified-adapter \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
```

Use the non-`main` exception only for lower-assurance review stacks:

```bash
# Lower-assurance review stack. The pull request must stay draft.
workcell publish-pr --workspace /path/to/repo --branch feature/name \
  --base feature/review-stack --allow-non-main-base \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
```

## Common operator commands

During a suspected incident, do not use `workcell session delete` or
`workcell --gc`. These commands can remove evidence. First, preserve the
evidence as specified in [incident-response.md](incident-response.md).

```bash
workcell --agent codex --prepare --workspace /path/to/repo
workcell --agent codex --prepare-only --workspace /path/to/repo
workcell --target docker-desktop --agent codex --workspace /path/to/repo
workcell --target aws-ec2-ssm --target-id i-1234567890abcdef0 \
  --agent codex --workspace /path/to/repo --dry-run
workcell --target gcp-vm --target-id workcell-phase8-cert \
  --agent codex --workspace /path/to/repo --dry-run
workcell --agent codex --mode development --workspace /path/to/repo \
  -- bash -lc 'git status'
workcell --agent codex --doctor --workspace /path/to/repo
workcell --agent codex --inspect --workspace /path/to/repo
workcell --agent codex --auth-status --workspace /path/to/repo
workcell policy show
workcell policy diff
workcell why --agent codex --mode strict --credential codex_auth
workcell --gc
```

AWS EC2 SSM and GCP VM are preview-only broker paths. Workcell blocks operator
launch. See [aws-ec2-ssm-preview.md](aws-ec2-ssm-preview.md) and
[gcp-vm-preview.md](gcp-vm-preview.md).

## Session commands

For deletion, always inspect the `--dry-run` result before you run the command
without `--dry-run`.

```bash
workcell session start --agent codex --workspace /path/to/repo
workcell session list
workcell session list --verbose
workcell session list --parallel
workcell session attach --id SESSION_ID
workcell session send --id SESSION_ID --message "continue with tests"
workcell session stop --id SESSION_ID
workcell session show --id SESSION_ID
workcell session show --id SESSION_ID --text
workcell session logs --id SESSION_ID --kind audit
workcell session timeline --id SESSION_ID
workcell session diff --id SESSION_ID
workcell session export --id SESSION_ID --output /tmp/workcell-session.json
workcell session verify --id SESSION_ID
workcell session delete --id SESSION_ID --dry-run
workcell session delete --id SESSION_ID
```

`workcell session delete` requires a session with terminal status. It refuses a
running session or container. It removes the durable session record and only
the recorded session-owned artifacts. It does not rewrite the shared profile
audit log.

`workcell session list --verbose` adds target, workspace transport, Git branch,
and worktree fields. `workcell session list --parallel` groups sessions by
origin repository. It prints one stable `key=value` field per line.

## Parallel-session boundary

Use `--session-workspace isolated` to create a clean clone and branch for one
session. Two isolated sessions for one repository have different clone paths,
branches, and containers.

By default, these sessions share one Colima VM and kernel. Their profile comes
from the original workspace path. Use a different `--colima-profile` for each
session when each session needs a different VM.

The session ID defines these values:

- clone path: `<repo>/.git/workcell-sessions/<session-id>/repo`
- branch: `workcell/session-<session-id>`

Workcell gives the container name a random suffix. The session ID does not
define the name.

Repository tests prove distinct clone paths, distinct branches, and clone-level
write invisibility without Colima. Local certification on 2026-08-03 proved two
simultaneous containers, cross-container write isolation, independent stop
behavior, and bounded cleanup. This evidence applies only to signed commit
`a26b750f` and exact tree `726f485a609b88edcee7ee5ad88692d7aafdd501`.
The commit is reachable from `main`. See
[the C3 evidence record](benchmark-evidence/c3-two-container-isolation-2026-07-15.md).

Hosted CI does not prove the shared-VM runtime boundary.

## Garbage collection

`workcell --gc` removes stale Workcell scratch data, stale transient audit
directories, broken latest-log links, excess image-cache entries, and stale
regenerable build-cache entries. It does not delete durable session records.
