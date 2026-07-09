# Safe-path Expectations

- Workcell launches the selected provider directly inside the bounded runtime
- there is no separate "start a container, then attach the agent" step
- `publish-pr` runs on the host so signed commits, signed-range verification,
  and GitHub publication stay outside the Tier 1 container, and it blocks
  unsigned publish ranges and over-broad branch diffs before push so published
  PRs stay reviewable; `main` is the only supported PR base by default, and
  non-`main` bases remain an explicit lower-assurance draft-only escape hatch
  with an explicit preflight warning that repo-owned PR checks are not expected
  for that base; reviewed, live-certified adapter support PRs may use the
  bounded `approved-large-certified-adapter` label plus
  `--approved-large-certified-adapter` publication flag when they cannot be
  split without invalidating certification evidence
- completed and aborted launches are recorded as durable host-side session
  records that you can inspect with `workcell session ...`
- `workcell session diff` compares the current workspace against the clean git
  base recorded at launch and fails closed when the launch started dirty, when
  no launch git base was recorded, or when the workspace is not a self-contained
  git worktree
- `--debug-log`, `--file-trace-log`, and `--audit-transcript` are explicit
  lower-assurance operator choices and are off by default

Useful operator flows:

For changes to this repository, publish main-based PRs through the repo wrapper
after fresh local parity evidence:

```bash
./scripts/pre-merge.sh --profile pr-parity
./scripts/repo-publish-pr.sh --workspace /path/to/repo --branch feature/name \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
```

`workcell publish-pr` is the lower-level host-side helper. Use it directly for
operator repositories that do not carry Workcell's repo-local parity wrapper,
or for the explicitly lower-assurance non-`main` draft path.

Use `--target colima|docker-desktop|aws-ec2-ssm|gcp-vm` to select the managed
runtime backend.

```bash
workcell --agent codex --prepare --workspace /path/to/repo
workcell --agent codex --prepare-only --workspace /path/to/repo
workcell --target docker-desktop --agent codex --workspace /path/to/repo
workcell --target aws-ec2-ssm --target-id i-1234567890abcdef0 --agent codex --workspace /path/to/repo --dry-run
workcell --target gcp-vm --target-id workcell-phase8-cert --agent codex --workspace /path/to/repo --dry-run
workcell --agent codex --mode development --workspace /path/to/repo -- bash -lc 'git status'
workcell session list
workcell session list --verbose
workcell session list --parallel
workcell session start --agent codex --workspace /path/to/repo
workcell session delete --id SESSION_ID
workcell session attach --id 20260408T120000Z-1a2b3c4d
workcell session send --id 20260408T120000Z-1a2b3c4d --message "continue with tests"
workcell session stop --id 20260408T120000Z-1a2b3c4d
workcell session show --id 20260408T120000Z-1a2b3c4d
workcell session show --id 20260408T120000Z-1a2b3c4d --text
workcell session logs --id 20260408T120000Z-1a2b3c4d --kind audit
workcell session timeline --id 20260408T120000Z-1a2b3c4d
workcell session diff --id 20260408T120000Z-1a2b3c4d
workcell session export --id 20260408T120000Z-1a2b3c4d --output /tmp/workcell-session.json
workcell session verify --id 20260408T120000Z-1a2b3c4d
workcell policy show
workcell policy diff
workcell why --agent codex --mode strict --credential codex_auth
workcell --agent codex --doctor --workspace /path/to/repo
workcell --agent codex --inspect --workspace /path/to/repo
workcell --agent codex --auth-status --workspace /path/to/repo
workcell --gc
./scripts/update-upstream-pins.sh --check
./scripts/publish-provider-bump-pr.sh
workcell --logs audit --colima-profile wcl-...
# Lower-level host publication helper for repositories without a repo wrapper.
workcell publish-pr --workspace /path/to/repo --branch feature/name \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
# Reviewed exception: live-certified adapter PRs that cannot be split.
workcell publish-pr --workspace /path/to/repo --branch feature/name \
  --approved-large-certified-adapter \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
# Lower-assurance exception: non-main bases stay draft-only.
workcell publish-pr --workspace /path/to/repo --branch feature/name \
  --base feature/review-stack --allow-non-main-base \
  --title-file /tmp/pr-title.txt \
  --body-file /tmp/pr-body.md \
  --commit-message-file /tmp/commit-message.txt
```

For the preview-only AWS and GCP remote VM broker paths and their certification
gates, see [aws-ec2-ssm-preview.md](aws-ec2-ssm-preview.md) and
[gcp-vm-preview.md](gcp-vm-preview.md).

`workcell session list --verbose` adds target, workspace transport, git branch,
and worktree columns without changing the default compact inventory view.
`workcell session list --parallel` groups sessions by their origin repository so
concurrent sessions launched against one repo (each started with
`--session-workspace isolated`, which clones a clean workspace into its own git
branch and container) render as a single parallel topology with each sibling's
isolated worktree, git branch, and container. Note that same-repo isolated
siblings share one Colima VM: the profile is derived from the original workspace
path, so they resolve to the same profile and VM (distinct clones, branches, and
containers, but a shared VM/kernel). Pass a distinct `--colima-profile` to place
a session on its own VM. The `--parallel` inventory emits one stable `key=value`
field per line (the same space-safe idiom as `workcell session show --text`), so
a parser recovers each value—including a workspace path containing spaces—as the
remainder of its line after the first `=`.
The 1.0 parallel-session isolation model is container-level: two same-repo
isolated sessions run as distinct containers sharing one Colima VM. The session
id deterministically derives two of the per-session boundaries—the isolated clone
path (`<repo>/.git/workcell-sessions/<session-id>/repo`) and the git branch
(`workcell/session-<session-id>`)—so two distinct session ids on one source repo
always resolve to distinct clone paths and distinct branches. The live container
name is NOT session-id-derived: it is assigned a random suffix at launch
(`workcell-<agent>-<mode>-repo-<random>`), which makes concurrent containers
distinct without depending on the session id.
Proven vs deferred, kept honest:

- CI-PROVEN (this PR, host-side, no Colima): for two distinct session ids on the
  same repo, the derivations produce distinct isolated clone paths and distinct
  branches, and clone-level write-invisibility holds (a write in clone A does not
  appear in clone B). The session-commands scenario runs the real clone/branch
  derivations directly and asserts both, needing only git—no live runtime.
- LOCAL-OPERATOR-CERTIFICATION (deferred, needs Apple Silicon + Colima): the LIVE
  two-container concurrent launch plus runtime cross-container non-interference (a
  write inside session A's running container is invisible inside session B's).
  This runtime container isolation is the boundary Workcell relies on generally,
  but this PR's automated evidence covers only the clone/branch layer; the live
  layer is certified locally because hosted CI has no Colima VM to launch
  containers in.

The shared VM/kernel is not proven distinct here; pass a distinct
`--colima-profile` to place a session on its own VM.
`workcell session show --text` renders stable key=value lines for the same
target-aware record, and `workcell session start|send|stop` emit stable
key=value summaries so host-side detached control stays scriptable.
`workcell --gc` removes stale Workcell-owned temp scratch, disposable
session-audit directories, broken latest-log pointers, and over-budget runtime
image cache entries without deleting durable session records. It also removes
stale regenerateable Workcell build cache entries.
