# Security Invariants

These invariants define the safe path. Repository priorities apply only after
Workcell satisfies these constraints.

## 1. Host secrets stay outside the default trust boundary

The managed path does not pass these host resources to the runtime:

- home directories
- keychains and browser profiles
- Git credential helpers
- `docker.sock`
- SSH, GPG, and provider agent sockets
- provider state such as `~/.codex`, `~/.claude`, `~/.copilot`,
  `~/.config/github-copilot`, `~/.cache/github-copilot`, and `~/.gemini`

Reusable authentication enters a session only through reviewed injection-policy
inputs.

The Copilot adapter accepts only `copilot_github_token`. It does not use host
Copilot state, `GH_TOKEN`, `GITHUB_TOKEN`, a host keychain, or `gh auth token`.
Workcell gives the token only to the managed Copilot child through the reviewed
temporary handoff. It does not retain the token in mounted provider state or in
the entrypoint environment. See
[Onboarding and Authentication](onboarding-and-auth.md#copilot-token-handoff).

Workcell does not support Antigravity. A future adapter must not use host Google
account state, browser profiles, keychains, home directories, or provider
caches.

## 2. Writes stay inside the intended workspace

The selected workspace is the durable writable mount. Provider homes are
session-local runtime state.

Workcell uses these host staging roots:

- `<cache>/colima/workcell-host-inputs`
- `<cache>/colima/workcell-shadow`

On macOS, `<cache>` is `~/Library/Caches`. On Linux, `<cache>` is
`~/.cache`. The launcher runs with a clean environment, so it does not read
`XDG_CACHE_HOME`.

The managed Colima VM mounts these roots as read-only. These mounts do not give
the runtime durable write access outside the selected workspace. GitHub
publication remains a separate host action.

Invariant `snapshot-store-host-owned`: `workcell session snapshot` writes to
`<cache>/colima/workcell-snapshots`. No container mounts this store.
Workcell refuses a workspace that contains the store or is inside it.
`scripts/verify-invariants.sh` checks this with dry runs.

Workcell opens the store through no-follow descriptors. It runs git only while
the store path still names the opened directory. It fsyncs the new objects, the
ref, and each directory that gained an entry. It does this before it appends the
audit record.

`workcell session restore` fetches a snapshot by the object id in the signed
`session_snapshot` record, never by a store ref. A store ref that names
another commit does not change what restore applies. A commit that the store
does not hold, or a commit with another tree or parent, stops the restore.
`TestRestoreApplyUsesSignedObjectIDNotStoreRef` in
`internal/sessionctl/restore_test.go` moves the ref and checks this. Restore
runs git in a scratch directory, so it does not read the session clone `.git`.

Snapshots stay until the operator removes them. Workcell does not
prune the store. The command `workcell session delete` does not remove
snapshots. The manual page gives the removal commands.

## 2a. Host path reads go through the hardened primitives

A host path that an operator or a provider controls can change between the
check and the open. `internal/rootio` opens through a verified parent handle
and refuses to follow a symlink.

`scripts/check-hardened-fs.sh` rejects a reference to a raw `os` pathname call
in a trust-boundary package, `testdata` included. A function value counts, because it carries the
same authority as the direct call. `os.Lstat` stays permitted, because it does
not follow the final symlink.

`policy/hardened-fs-baseline.tsv` records the calls that the tree carries
today. A count that does not match its baseline fails
`internal/metadatautil/hardenedfs_check.go`. Replace a call
with the matching `internal/rootio` primitive, then lower the count in the same
change. New code states its
reason at the call with a `// hardened-fs-exempt: <reason>` comment.

The same script also rejects a narrower set of calls in all non-test Go under
`cmd/` and `internal/`, `testdata` included. Go builds a `testdata` package that
other code imports by path. The set is `os.ReadFile`, `os.WriteFile`, `os.Open`,
`os.OpenFile`, `os.Create`, `os.Stat`, `os.RemoveAll` and `filepath.Glob`.
Only `internal/rootio` is exempt. Each failure names the `internal/rootio`
replacement.

`policy/hardened-io-baseline.tsv` records the calls that the tree carries
today, with one reason per row. Each row lists one identity per call.
The identity hashes the enclosing function, the headers of the enclosing `if`,
`for`, `switch`, `select` and `case` statements, and the call text. A new call
needs a reviewed entry, even when it replaces another call in the same file or
moves out of a guarded branch.

`policy/testkit-exec-baseline.tsv` lists the test sites that run a script by
path without `execRetryETXTBSY`. A raw exec runs into ETXTBSY under load.
`internal/testkit/exec_ratchet_test.go` counts these sites. The count may only
go down. A stale-high count also fails, and so does a row above its value at
the merge base with `origin/main`.

## 2b. Workflow bodies reference real scripts and bounded lists

`scripts/check-workflows.sh` runs `check-workflow-refs`. The check fails when a
`run:` body calls a `./scripts` path that does not exist or that has a `..`
component. It fails when a `gh api` call has no `--paginate`. It fails when a
`gh list` call has no `--limit`.

The check also fails when an expansion hides a command word, a `gh` subcommand
or the text of an `eval`. It also fails on an `alias` definition, which can
rename any later command. It reads the program a shell runs from `-c`, a
heredoc or a here-string. It fails when it cannot spell that program, as in
`cat x | bash`. It fails when a `with:` key is not an input of the action.

The check fails when a `uses:` pin has no row in
`tests/fixtures/actions/inputs.tsv`. When a pin changes, replace its row. Write
the input names from the `action.yml` at the new commit, sorted and
comma-separated.

A testkit case runs each inline jq program against an empty array. A compile
error fails the test.

`policy/workflow-refs-baseline.tsv` records the hits that the tree carries
today. A new hit fails. A row with no hit also fails. Remove a row when you fix
its hit.

## 2c. Shell scripts must not hide a failed tool

Some idioms turn a failed tool into an empty answer, so a gate passes on nothing. They are a `<(` or `>(` process substitution, and a `$(...)`, `|| true`, `|| :` or stderr sent to `/dev/null` on `find`, `git`, `gh`, `docker` or `getent`. A `$(...)` that runs a variable, a quoted path or `eval` counts as one of those tools.

`scripts/check-shell-portability.sh` rejects each idiom in `scripts/` and `runtime/container/` unless the command, or the command after it, captures the status. A `$(...)` that is an argument of a command such as `local` or `[[` cannot be captured that way. A heredoc that the check cannot end fails the check. A command can state its reason with a `# fail-closed: <reason>` comment at its line end or alone on the line before. The comment covers only that command.

Bash never propagates the status of a process substitution, and a `# fail-closed:` comment does not cover one. Only `wait "$!"` after a `done < <(` loop or a `NAME_completed` sentinel captures it. No job in the loop may run in the background before that `wait`. For a sentinel, the producer ends with `&& printf '\0'`. The loop sets the flag to 1 only on that empty record. The next command fails unless the flag is 1.

`policy/shell-fail-open-baseline.tsv` records the hits that the tree carries today. A count that does not match its baseline fails `internal/metadatautil/shellfailopen_check.go`. Repair a hit, then lower the count in the same change.

## 2d. Validators anchor to decoded structure

A validator that reads content and then matches it with `strings.Contains`,
`bytes.Index` or `regexp` can match text in a comment or a string. Use
`ShellInvocations` or a closed decoder instead.

`scripts/check-validator-anchoring.sh` lists every function that matches text
this way. It scans `internal/adapters`, `internal/metadatautil`,
`internal/testkit` and `internal/workcellhardening`, test files included. It does not track where the text came from. A helper that
matches content from its caller is listed too. An import alias or a dot import
does not hide the package. A call through a function value such as
`contains := strings.Contains` counts.

`policy/validator-anchoring-baseline.tsv` holds one row per function: package,
function, an identity per match call, and the reason that the match is safe or
still debt. `internal/metadatautil/validator_anchoring.go` fails a function
with no row. It fails a row whose identities differ,
so a new or a swapped match in a listed function fails too. It fails a row whose
function no longer matches this way. Port a debt row, then delete it in the same
change.

## 3. Repo policy must not silently widen trust

Workcell masks repository control-plane files on the safe path. It imports only
reviewed inputs into provider homes.

The mask includes these files:

- `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, and `.mcp.json`
- `.github/mcp.json` and `.github/copilot-instructions.md`
- `.goosehints` at any depth, in the working tree or the Git index

The mask includes these directories:

- `.codex/`, `.claude/`, `.copilot/`, `.gemini/`, `.amp/`, and `.goose/`
- `.github/instructions/`, `.github/copilot/`, `.github/hooks/`,
  `.github/agents/`, and `.github/skills/`
- `.agents/skills/` and `.agents/plugins/`
- `.vscode/`, `.idea/`, `.cursor/`, and `.zed/`

The `.gemini/` mask does not copy a tracked `.gemini/.env` file. The Gemini
system settings at `/etc/gemini-cli/settings.json` have precedence over
workspace settings. They disable Gemini hooks and ignore the workspace `.env` file.

Workcell also masks Git execution-control paths for the workspace repository and
its submodules. These paths are `hooks`, `config`, `config.worktree`, and
`worktrees`.

Workcell also masks `workcell-sessions` in each Git directory of the workspace
with an empty read-only directory. That directory holds the isolated clone of
each session. The mask applies when the directory is absent, because Workcell
can make a clone there after the launch. Workcell makes the directory first.
Workcell refuses the workspace when `workcell-sessions` is a symlink or is not a
directory. The shadow setup in `scripts/workcell` adds this mask.

Workcell refuses a safe-path workspace in these cases. Each case can make Git
read config and hooks from a directory that the masks do not cover.

- A Git `commondir` file is beside a `HEAD` file or `HEAD` symlink. The scan
  skips `worktrees` and the top-level `refs`, `logs` and `objects`. A dangling
  `HEAD` symlink counts, because Git accepts one that names `refs/`.
- `.git/modules` is a symlink, or a symlink under `.git/modules` is not a
  `hooks`, `config`, `config.worktree`, `worktrees` or `HEAD` link.
- A top-level or module `hooks`, `config`, `config.worktree` or `worktrees`
  symlink dangles on the host. The launcher masks these links only when they resolve on the host.

The control names match in any letter case, because a case-insensitive volume
opens `CONFIG` as `config`. A `HEAD` symlink under `.git/modules` is allowed when
it resolves on the host.

These checks cover real `.git` directories. They do not cover a nested `.git` file
or symlink that points to an admin directory elsewhere.

Workcell also refuses the workspace when it cannot list the workspace, a Git
directory, or its modules. The checks run at launch. They do not stop a file that is created
after launch.

The Copilot adapter also masks Copilot settings, instructions, MCP files,
skills, and hooks. It disables custom instructions and blocks skill and dynamic
retrieval overrides.

A future Antigravity adapter must mask its settings, plugins, MCP files, hooks,
and instructions before it can claim support.

## 3a. Documentation claims resolve to gates

`scripts/check-doc-links.sh` reads every Markdown file. A code span that names
a path under `scripts/`, `internal/` or `.github/workflows/` must exist. The
check drops a leading `./` first. A path with a `..` or symlink component
fails, and the check does not look it up. Go probes each path through no-follow
descriptors.

`validate-repo.sh`, a CI job script or a workflow must run a cited
`scripts/*.sh` path as a command. Thus a doc cannot cite a gate that nothing
runs. The check reads every command that a lane can run, so a command after
`false &&` or after an `exit` counts. A comment, a heredoc or an array member
does not count. A workflow path starts from the working directory of its step,
and a `cd` before it moves that directory.

`internal/metadatautil/lane_scripts.go` reads the lanes. A lane fails the
check when it can change what a later command runs. An `alias` does this, and
so does a source of a path outside `${ROOT_DIR}`. A program name that the check
cannot read also fails. The check does not follow a symlink to a lane file.

A sentence that claims enforcement needs such a span within two lines, and
it can wrap across lines. `scripts/lib/doc-claims.awk` lists the trigger
words.

The baseline `policy/doc-claims-baseline.tsv` holds the hits that exist
today, one row per hit: path, rule, subject and reason. A new hit fails.
A row with no hit fails. `scripts/check-doc-links.sh` also fails on a row
that the file at the merge base does not hold. The one exception is a rule
that the baseline at the merge base does not name.

## 4. Network posture is explicit

`strict`, `development`, `build`, and `breakglass` are separate modes. Workcell
does not use a provider prompt as a network control.

The `strict` mode sets `NETWORK_POLICY=allowlist`. On Colima, Workcell applies a
fail-closed dual-stack firewall in the VM `DOCKER-USER` chain. It permits only
reviewed `host:port` entries and drops other egress. It stops the launch if it
cannot contain IPv6.

Only Colima applies the profile-wide allowlist. The last allowlist launch
replaces rules for all active containers in that profile. The replacement is
atomic for each address family. The old default-deny chain stays in force until
the complete new chain replaces it. Other targets use their own network
controls. The launch summary states the result in `egress_enforcement=`.

A strict Colima session routes its egress through a per-session proxy sidecar
by default. `--egress-proxy=off` keeps such a session on the profile-wide
allowlist alone, with the resolver of the VM, as before the proxy.

- `egress-proxy-default-strict-colima`: the proxy is on for the Colima target
  in strict mode unless the launch passes `--egress-proxy=off`. Every other
  target and mode runs without it. `resolve_egress_proxy` owns the decision.
  `TestEgressProxySetting` in `internal/testkit/egress_proxy_setting_test.go`
  enforces each target, mode and flag. The dry-run rows in
  `tests/scenarios/shared/test-assurance-dry-run.sh` enforce the launch
  summary and the agent route.

Two sections of `policy/hardening-profile.toml` gate the proxy route. The
`hardening-profile-conformance` check, which `scripts/verify-invariants.sh`
runs, enforces them:

- `egress-proxy-sole-route` (`[egress_proxy_sole_route]`): the agent joins only
  the internal network `wc-<session>` with `--dns 127.0.0.1`. Each allowlisted
  host maps to the proxy address, never to a resolved upstream address.
- `egress-proxy-hardening` (`[egress_proxy]`): the sidecar uses the agent
  conformance flags. It drops all capabilities, uses `no-new-privileges` and a
  read-only root, runs as a non-root user, and has PID and memory limits. It
  listens only on its internal-network address, never on `0.0.0.0`.

`broker-socket-sidecar-only`: a brokered key reaches only the sidecar. The
launcher sets the guest socket to the sidecar uid and mode `0600`, then reads
both back. A mismatch stops the launch. Only the sidecar mounts the socket, and
the agent holds a `wcph-` placeholder.

`internal/testkit/credential_broker_launch_test.go` enforces the owner and mode
check (`TestStartCredentialBrokerRollsBackEveryFailure`) and the sidecar-only
mount (`TestBrokerSocketAndTokenReachOnlyTheSidecar`). It also limits the test
keychain knob to an allowed harness.
`internal/metadatautil/egress_proxy_sidecar.go` pins the sidecar create and
`start_credential_broker`.

`phantom-no-real-secret`: a brokered key never reaches the agent container.
The agent environment, its processes, the injection bundle and the provider
home hold only the `wcph-` placeholder. The sidecar swaps it for the key on the
wire, and only for the brokered host and header.

`internal/injection/prepare_bundle_test.go` enforces that the bundle stages
only the placeholder (`TestPrepareBundleBrokersAPIKeys`).
`internal/testkit/credential_broker_launch_test.go` enforces that the agent
command gets neither the token nor the broker socket.
`internal/egressproxy/terminate_test.go` enforces the exact swap.

The live canary check is in `tests/scenarios/shared/test-egress-proxy-smoke.sh`.
It stores a random `sk-canary-` key in a throwaway keychain. A brokered Claude
run and a brokered Codex run then count the canary. They count it in the
environment, in each readable `/proc/*/environ`, in the bundle and in the
provider home. Each must count 0.

The real Codex binary must also reach OpenAI through the CA bundle. The
smoke is a certification scenario, so only the local certification lane of
`scripts/run-scenario-tests.sh` runs it. No CI or pre-merge lane runs it.

`session-ca-constrained`: the session CA that reaches the agent is a CA with
path length 0. Its critical DNS name constraints name only the brokered hosts,
and it excludes every IPv4 and IPv6 address. An X.509 DNS name constraint also
permits subdomains of a named host. The sidecar signs leaves only for the
exact terminate hosts, and the CA key stays in the sidecar's memory.

The CA's own validity period, from `NotBefore` to `NotAfter`, is at most 24
hours plus a one-minute backdate. It must be unexpired on the host clock. The
period check needs no clock, so skew between the host and the VM does not
refuse a valid CA.

`internal/injection/egress_ca.go` (`PublishEgressCA`) refuses any other CA from
the sidecar before the launcher publishes it, and
`internal/injection/egress_ca_test.go` enforces each refusal.

The CA bundle for `CODEX_CA_CERTIFICATE` holds the image system roots and this CA.
The launcher reads the roots from the read-only sidecar, not from the agent
container. `PublishEgressCA` accepts only PEM certificates there, and
`TestPublishEgressCAWritesTheRootsBundle` in
`internal/injection/egress_ca_test.go` enforces it.
`internal/egressproxy/terminate.go` owns the 24-hour bound for both the sidecar
and the publisher.

`broker-host-socket-private`: the host broker socket lives in a private
directory that the launcher makes with `mktemp -d`. In
`internal/credbroker/server.go`, `listen` refuses a socket directory that
another uid owns or can write, or that carries an extended ACL. Each ancestor
belongs to root or the user and carries no ACL. Only a sticky ancestor can
admit other writers. The walk uses no-follow opens. The socket gets mode `0600`.

`internal/credbroker/credbroker_test.go` covers a shared directory, an ACL, a
symlinked ancestor and an ancestor that another uid could replace.

The broker audits its use. The launcher appends `credential_broker_start` after
the broker starts and `credential_broker_summary` when it stops, before the
seal. `internal/testkit/credential_broker_launch_test.go` enforces the order on
the launcher, monitor and session stop paths.

`egress-deny-audited`: each denied proxy connection reaches the audit chain of
its session, under the session seal. When the proxy stops, the launcher appends bounded `egress_deny`
records and one `egress_summary` record before it seals the session. A failed
launch has no seal, so its cleanup appends them before the `aborted` record.
The OCSF export maps `egress_deny` to Network Activity (`class_uid` 4001).

`internal/testkit/egress_proxy_deny_log_test.go` enforces the records and
their order on each path. `internal/metadatautil/egress_deny_seal.go` parses
`scripts/workcell` and fails when any other code seals a session or stops a
session proxy with audit. The `hardening-profile-conformance` check runs it.

The injection policy can add or deny endpoints through `[network]`. It cannot
disable the default policy or change `NETWORK_POLICY`. See
[Network egress](injection-policy.md#network-rules).

The reviewed endpoint inventory is in
[outbound-endpoints.md](outbound-endpoints.md). The machine-readable source is
`policy/hardening-profile.toml`.

## 4a. Container hardening posture is captured and drift-checked

The runtime container uses these controls:

- drops all capabilities
- adds only `SETUID` and `SETGID` for the mutable-mode mapped-user step
- uses `no-new-privileges`
- uses a read-only root file system in `readonly` sessions
- uses hardened `nosuid`, `nodev`, and applicable `noexec` temporary mounts
- sets a process limit
- runs as a mapped nonroot user

`policy/hardening-profile.toml` records this posture. The
`hardening-profile-conformance` invariant checks the launcher. It also rejects
`--privileged` and `seccomp=unconfined`.

## 4b. Privileged package operations go through the broker socket

An ephemeral session can change packages. The mapped runtime user holds no
standing authority to do so. It holds no sudoers grant, and the real `sudo`
binary stays unreachable.

The only path is the apt broker:

- `workcell-apt-broker-server` starts as root before the mapped-user step. It
  creates a root-owned `0755` socket directory and binds a root-owned `0666`
  socket in it.
- The server accepts a connection only from the mapped uid, checked against the
  peer credentials the kernel reports, not against anything the caller sends.
- The server runs `apt-helper.sh` and nothing else. The helper validates the
  subcommand and every argument.
- Requests carry arguments and a fixed set of three environment names. The
  server rejects any other name and any unlisted value, and builds the helper
  environment itself.
- `sudo` in the container is a wrapper. For an unprivileged caller it hands the
  invocation to `workcell-apt-broker-client`, which admits the package helper
  and refuses every other command.

No shell broker, spool directory, or pid file takes part. The
`workcell-smoke-apt-broker-probe` invariant checks that the container smoke test
keeps proving this end to end.

## 5. Destructive or trust-widening actions need defense in depth

The runtime boundary is the primary control. Provider controls add defense in
depth:

- Codex requirements at `/etc/codex/requirements.toml`, and Codex rules
- Claude managed settings and Bash hook
- Gemini system settings at `/etc/gemini-cli/settings.json`, managed user
  settings, and trusted-folder seed

The session seed verifies each root-owned file against the control-plane
manifest before the provider starts.

These controls do not replace the runtime boundary.

## 6. Lower-assurance paths are labeled

Examples include:

- `--agent-autonomy prompt`
- `--cache-profile standard`
- `development`
- package changes in a mutable container
- `--allow-control-plane-vcs`
- `--allow-repo-mcp`
- `--allow-arbitrary-command`
- `--allow-uncertified-agent`
- `breakglass`
- host debug logs or transcript capture
- Copilot or future Antigravity telemetry and content capture

Workcell records these choices in launch or runtime state.

An adapter with the manifest tier `uncertified` runs only with
`--allow-uncertified-agent`. The session records
`session_assurance_initial=lower-assurance-uncertified-adapter`. Workcell runs
the `runtime-uncertified` image stage for it, so the runtime image of a
certified adapter does not include an uncertified agent. A provider option
passes only when it is on the manifest `[flags]` `allow` list and not on
`deny`. A word that names one of `[flags]` `other_commands` is refused, and
other words, such as a prompt, pass. The tests are in
`internal/adapters/uncertified_test.go`.

## 7. Autonomous runs remain auditable

The launcher keeps durable host session metadata. Full debug logs, file traces,
and transcripts require separate operator options.

## Profile expectations

| Profile | Expected posture |
|---|---|
| `strict` | Default provider path with reviewed mounts, explicit network controls, and control-plane masks |
| `strict --container-mutability readonly` | Strongest managed path with package-manager writes blocked |
| `development` | Managed interactive path with the same boundary and masks, non-provider commands, and more dependency endpoints |
| `build` | Image preparation path with broader build endpoints |
| `breakglass` | Explicit higher-trust path with dated acknowledgement |

## Non-goals

Workcell does not claim that:

- provider hooks or rules are the primary boundary
- host-native graphical applications are equal to Tier 1
- release provenance proves the local macOS runtime boundary
