# Security Review, 2026-08-12

Repository: `omkhar/workcell`

Baseline commit: `a31af3ec99820f8da08099bc01354261b326d1d0`

Status: The working tree contains 59 fixes.
Two findings remain as explicit residuals.
Full validation passed.
No commit exists for this review.
The Result column shows the state of that working tree.
Each fix reaches the default branch in a separate reviewed pull request.

## Scope

The review covered host launch code, policy parsing, input staging, runtime
scripts, the Rust execution guard, GitHub workflows, and release publication.

The review also covered audit records, credentials, dependency locks, container
inputs, action pins, and repository-owned security validators.

The review used source analysis, focused race tests, static analyzers, dependency
scanners, Linux runtime probes, and peer review.

## Threat model

The primary attacker controls repository files and processes inside the runtime.
The attacker does not control the operator-owned Workcell state root.

The attacker can race mutable paths and call libc execution interfaces.
The attacker can also call the dynamically linked `syscall` interface.

The attacker cannot replace the dedicated VM and container boundary.
A fully static process does not load the execution interposer.

The release review assumes a compromised untrusted build step.
It does not assume compromise of every authorized release identity.

## Attack surface

| Boundary | Reviewed entry points |
|---|---|
| Host launcher | CLI arguments, workspace paths, Colima commands, DNS, and helper input |
| Host inputs | Policy files, documents, credentials, SSH files, copies, and direct mounts |
| Runtime setup | Entrypoint state, user mapping, package wrappers, broker requests, and environment data |
| Execution guard | Libc execution calls, spawn calls, raw syscall calls, descriptors, loaders, and scripts |
| Session evidence | Session records, audit logs, signatures, exports, and timelines |
| CI and release | Workflow refs, permissions, actions, artifacts, signatures, attestations, and publication |
| Supply chain | Go modules, Rust crates, container inputs, workflow actions, and release tools |

## Discovery records

| ID | Component | Primitive | Empirical evidence | Discovery disposition | Remediation result |
|---|---|---|---|---|---|
| F-001 | Execution guard | Missing dynamic syscall interposition | Linux raw-call probe | ready-for-validation | Fixed |
| F-002 | Execution guard | Mutable native execution variants | Linux API and descriptor matrix | ready-for-validation | Fixed |
| F-003 | Input staging | Source replacement race | Deterministic replacement test | ready-for-validation | Fixed |
| F-004 | Runtime setup | Untrusted state and environment | PATH and state mutation tests | ready-for-validation | Fixed |
| F-005 | Release workflow | Missing output verification | Workflow mutation tests | ready-for-validation | Fixed |
| F-006 | Audit processing | Path substitution and unbounded reads | Link and size tests | ready-for-validation | Fixed |
| F-007 | Credential parsing | Sensitive diagnostic content | Malformed credential fixtures | ready-for-validation | Fixed |
| F-008 | Privileged workflows | Non-main manual execution | YAML mutation tests | ready-for-validation | Fixed |
| F-009 | Resource handling | Unbounded input, output, lookup, and policy reads | Boundary-size tests | ready-for-validation | Fixed |
| F-010 | Native launcher | Ignored signal setup errors | Source proof and unit tests | ready-for-validation | Fixed |
| F-011 | Package broker | Helper descendants survived the deadline | Process-group timeout tests | ready-for-validation | Fixed |
| F-012 | Apple workspace copy | Path races and unsafe file types | Replacement and special-file tests | ready-for-validation | Fixed |
| F-013 | Session and host lifecycle | Untrusted record paths and incomplete timeout cleanup | Link, size, and timeout tests | ready-for-validation | Fixed |
| F-014 | Execution guard | Shebang, PATH, loader, spawn, and guard-propagation bypasses | Linux execution matrix and raw-call tests | ready-for-validation | Fixed |
| F-015 | Injection policy | Policy path replacement after validation | Descriptor race test | ready-for-validation | Fixed |
| F-016 | Package broker | Startup cancellation lost the helper process group | Delayed-start timeout test | ready-for-validation | Fixed |
| F-017 | Release workflow | Signed tag target was not bound before checkout and publication | Tag target and dispatch-payload mutation tests | ready-for-validation | Fixed |
| F-018 | Upstream publisher | Workflow path accepted lookalike paths | Exact workflow path mutation test | ready-for-validation | Fixed |
| F-019 | Package broker | Mutable broker state lacked a complete bounded root | Mount and quota tests | ready-for-validation | Fixed |
| F-020 | Release workflow | Release source was selected by a tag-triggered workflow ref | Dispatch and current-main mutation tests | ready-for-validation | Fixed |
| F-021 | Release controls | One tag ruleset allowed an administrator bypass for updates and deletion | Hosted-ruleset mutation tests | ready-for-validation | Fixed |
| F-022 | Execution guard | `env -S` options could remove the approved preload | Shebang option mutation tests | ready-for-validation | Fixed |
| F-023 | Input staging | Case-colliding destinations could merge or overwrite material | Destination collision tests | ready-for-validation | Fixed |
| F-024 | Release image | A named registry tag could move after digest signing | Named-tag verification mutation test | ready-for-validation | Fixed |
| F-025 | Package broker | Arbitrary frontend environment values could launch a pager or browser | Environment-value mutation tests and Debian apt-listchanges documentation | ready-for-validation | Fixed |
| F-026 | Injection manifest | Copy targets and SSH identity basenames could inject row separators | Manifest-field validation tests | ready-for-validation | Fixed |
| F-027 | Host containment and staging | Unicode normalization aliases could bypass lexical containment or destination collision checks | NFC/NFD and case-collision tests | ready-for-validation | Fixed |
| F-028 | Package broker | Shutdown returned before active handlers finished terminating helper groups | Broker shutdown and child-death tests | ready-for-validation | Fixed |
| F-029 | Release environment | The dispatch workflow used `main`, but the release environment allowed only `v*` deployment tags | Environment contract tests and policy fixtures | ready-for-validation | Fixed |
| F-030 | Hosted controls | The required-status ruleset allowed bypass actors | Hosted-ruleset mutation tests | ready-for-validation | Fixed |
| F-031 | Hosted controls | Review-rule bypass actors lacked an exact role and count constraint | Hosted-ruleset mutation tests | ready-for-validation | Fixed |
| F-032 | Release image | Output verification did not bind the image repository and `sha-<commit>` tag | Release-output mutation tests | ready-for-validation | Fixed |
| F-033 | Release tag verification | Tag checks accepted revision syntax and ambient repository identity | Tag-signature argument and API mutation tests | ready-for-validation | Fixed |
| F-034 | Release attestations | Mutable repository variables controlled whether the workflow created attestations | Release-workflow mutation tests | ready-for-validation | Fixed |
| F-035 | Hosted controls | Malformed bypass-actor data could pass as an empty list | Hosted-ruleset type mutation test | ready-for-validation | Fixed |
| F-036 | Hosted-control credential | The audit environment allowed unused tag deployments | Environment policy mutation test | ready-for-validation | Fixed |
| F-037 | Host containment and staging | Unicode compatibility aliases bypassed path identity checks | macOS Unicode alias tests | ready-for-validation | Fixed |
| F-038 | Hosted-controls audit | An ambient policy path could replace the reviewed hosted-controls policy | Policy-path mutation test | ready-for-validation | Fixed |
| F-039 | Release attestations | The visibility input was not bound exactly to the repository event | Workflow visibility mutation test | ready-for-validation | Fixed |
| F-040 | Release workflow | Workflow-run ranking could select the wrong run attempt | Workflow-run ranking mutation test | ready-for-validation | Fixed |
| F-041 | Hosted-controls audit | Malformed or incomplete paginated data could pass collection checks | Pagination mutation tests | ready-for-validation | Fixed |
| F-042 | Hosted controls | Required status contexts lacked a GitHub Actions source binding | Integration-ID mutation tests | ready-for-validation | Fixed |
| F-043 | Runtime manifest handling | Manifest reads lacked bounds and no-follow path controls | Limit and symlink race tests | ready-for-validation | Fixed |
| F-044 | Release privilege scope | Repository build and release assembly code share signer permissions | Workflow permission review | ready-for-validation | Residual |
| F-045 | Local release tag verification | Separate mutable tag reads, tag-name mismatch, and Git replacement objects could alter local verification | Tag race, tag-name, replacement-object, and publisher contract tests | ready-for-validation | Fixed |
| F-046 | Hosted-control credential | An ambient Go binary could execute while the audit token remained exported | Credential handoff and ambient-tool mutation tests | ready-for-validation | Fixed |
| F-047 | Runtime image build context | The Docker context excluded Go module files, the broker package, and broker command sources, so the runtime image could not build | Live invariant probe and pinned-input mutation tests | ready-for-validation | Fixed |
| F-048 | Runtime image guard propagation | Later Docker build steps lacked the exact preload environment after `/etc/ld.so.preload` activated the strict guard, so child execution failed | Live invariant probe and pinned-input mutation tests | ready-for-validation | Fixed |
| F-049 | Runtime startup | Sourced runtime libraries reassigned a read-only `PATH`, so the built container failed before launch | Focused structural test and full container-smoke run | ready-for-validation | Fixed |
| F-050 | Release publication | A spoofable entrypoint sentinel and ambient Go or token configuration could run code with publication authority | Ambient Go, persisted `GOFLAGS=-toolexec`, and verifier-token mutation tests | ready-for-validation | Fixed |
| F-051 | Runtime setup | The scrubbed apt-broker launch removed `LD_PRELOAD` after the guard became mandatory, so the runtime exited 126 | Focused launch-environment test | ready-for-validation | Fixed |
| F-052 | Hosted review controls | Default-branch review bypass role 5 can bypass the whole pull-request rule, including thread and code-owner review | Hosted-state review and policy comparison | ready-for-validation | Residual |
| F-053 | Runtime setup | The apt-broker launch did not create its trusted socket parent, so the broker failed before serving requests | Focused broker-root creation test and full container-smoke run | ready-for-validation | Fixed |
| F-054 | Runtime provider wrappers | Wrapper sanitizers reassigned a different `TRUSTED_PATH` after pinning `PATH` read-only, so launch aborted | Focused structural mutation test and full container-smoke run | ready-for-validation | Fixed |
| F-055 | Runtime provider binary | Codex archive extraction preserved uid/gid 1001, so the strict guard rejected the provider binary as non-root-owned | Metadata validation, ownership mutation test, and full container-smoke run | ready-for-validation | Fixed |
| F-056 | Package broker helper environment | The fixed helper environment omitted `LD_PRELOAD`, so the strict guard blocked `/usr/bin/id` before helper validation | Fixed-environment unit test and full container-smoke run | ready-for-validation | Fixed |
| F-057 | Container-smoke harness | Command substitutions could hide Bash syntax errors and skip late security checks; Perl `execveat` fixtures passed string flags and tested `EINVAL` | Full `./scripts/container-smoke.sh` run; mapped-user provider and numeric-flag tests | ready-for-validation | Fixed |
| F-058 | Runtime image bootstrap | The bootstrap policy omitted Go download and module hosts; the managed rebuild failed at nine 30-second curl attempts and then at `go mod download` | Focused control-plane, hardening-profile, pinned-input, injection, Docker lint, and Go tests | ready-for-validation | Fixed |
| F-059 | Policy mount serialization | A policy with no direct mounts serialized `null`, but the secure reader requires an array, so network-only launches failed closed | Focused extraction test and full assurance-dry-run scenario | ready-for-validation | Fixed |
| F-060 | Release build-input manifest | The manifest included runtime/container and adapters but omitted copied Go broker sources, `go.mod`, and `go.sum`, so it could omit image inputs | Unit coverage and `verify-build-input-manifest.sh` after all inputs entered the temporary index | ready-for-validation | Fixed |
| F-061 | Concurrent launch cleanup | Cleanup compared `darwin:`/`linux:` process records with legacy `ps` start strings, marked live injection bundles stale, and deleted staged inputs | Live-owner unit test and eight parallel auth-status/launch scenarios | ready-for-validation | Fixed |

## Proof matrix

| Candidate | Positive case | Negative control | Stable result |
|---|---|---|---|
| F-001 | Mutable ELF through linked raw syscall | Trusted `/bin/true` | Mutable target blocks; trusted target runs |
| F-002 | Mutable paths, aliases, descriptors, and loaders | Trusted immutable executable and mutable script | Native targets block; allowed targets run |
| F-003 | Replace a validated source or directory child | Unchanged regular source | Replacement fails; regular source copies |
| F-004 | Spoof runtime state, helper path, or inherited tools | Root-owned state and fixed tools | Spoof fails; trusted state works |
| F-005 | Remove or corrupt one release proof | Complete signed fixture | Mutation fails; complete fixture passes |
| F-006 | Link or enlarge an audit log | Canonical bounded regular log | Unsafe log fails; canonical log passes |
| F-007 | Put sensitive text in malformed input | Valid supported assignments | Error omits text; valid input renders |
| F-008 | Remove or widen the main-ref guard | Exact main-ref guard | Mutation fails; guarded workflow passes |
| F-009 | Exceed input and line limits | Use exact accepted limits | Excess fails; exact limits pass |
| F-010 | Return a signal setup error | Install valid handlers | Failure stops; normal setup continues |
| F-011 | Ignore a helper descendant after timeout | Helper exits within the deadline | Descendants stop; timeout returns 124 |
| F-012 | Replace a workspace entry or add a special file | Stable regular workspace tree | Copy rejects unsafe changes; stable tree copies |
| F-013 | Link a session record or audit log, or ignore a timeout child | Owner-only canonical paths and a completed child | Unsafe paths fail; timeout cleanup reaches the full process group |
| F-014 | Use mutable shebang, PATH, loader, spawn, or raw syscall variants | Trusted native target with the approved preload | Mutable targets block; trusted targets retain the guard |
| F-015 | Replace a policy file or parent after validation | Stable operator-owned policy file | Replacement fails; the opened descriptor remains fixed |
| F-016 | Cancel or time out before helper PID publication | Helper publishes its PID before its deadline | The process group stops; no helper descendant survives |
| F-017 | Point a signed tag at a different commit | Tag target equals the payload commit and the current main workflow commit before checkout | The gate rejects either mismatch; the publisher repeats the tag-to-payload check |
| F-018 | Supply a successful run from a lookalike workflow path | Exact `.github/workflows/upstream-refresh.yml` path on `main` | The publisher rejects the run |
| F-019 | Fill broker state or submit invalid requests | 24 MiB `noexec` broker-root tmpfs, four concurrent requests, 512 KiB pending input, and 1 MiB output streams | Limits and request isolation hold |
| F-020 | Dispatch a release from a stale or untrusted workflow source | `repository_dispatch` runs the current `main` workflow and checks its signed tag payload | Source and payload mismatches fail |
| F-021 | Add an administrator bypass to tag updates or deletion | Creation and immutability rulesets use separate, exact controls | Hosted-control validation rejects the bypass |
| F-022 | Use `#!/usr/bin/env -S` to unset `LD_PRELOAD` | The guard rejects split-string and unset options | The mutable native launch remains blocked |
| F-023 | Submit names that differ only by case | Destination reservations use case-insensitive collision keys and exclusive creation | The second destination fails closed |
| F-024 | Move the named image tag after digest signing | The workflow verifies the named tag against the signed manifest digest | A moved tag fails before publication |
| F-025 | Set an apt frontend to `browser` or `pager` | The broker accepts only `none`, `text`, `true`, and `noninteractive` values | Unsafe frontend values fail before helper execution |
| F-026 | Add newline, U+001F, U+2028, or invalid UTF-8 to a manifest field | The renderer rejects unsafe fields and accepts a safe identity basename | Unsafe fields fail before row emission |
| F-027 | Use NFC and NFD aliases or case variants for one path | Shared NFC normalization and collision keys protect containment and destination reservations | Aliases compare as one path and the second destination fails |
| F-028 | Cancel the broker while a helper descendant ignores `SIGTERM` | Shutdown waits for handlers after process-group termination | The child is dead before `Serve` returns |
| F-029 | Dispatch a release while the environment allows only `v*` deployment tags | The release environment permits the exact `main` branch used by `repository_dispatch` | The contract accepts the release job |
| F-030 | Add a bypass actor to required status checks | The validator requires an empty bypass-actor list | The hosted-control check rejects the mutation |
| F-031 | Add two review bypass actors or use another role | The validator permits zero actors or one repository role 5 actor | The hosted-control check rejects the mutation |
| F-032 | Use a different image repository or omit the commit tag | Verification requires `ghcr.io/OWNER/REPO` and `sha-<commit>` binding | The release-output check fails closed |
| F-033 | Pass a revision-qualified tag or rely on ambient repository state | The script accepts one bounded release tag and an explicit repository | The tag check rejects ambiguous input |
| F-034 | Set the attestation opt-out repository variable before dispatch | Attestation steps and both verifier calls are unconditional | The release still requires attestations |
| F-035 | Return malformed bypass-actor data | The validator requires an explicit array | The hosted-control check fails closed |
| F-036 | Run an audit job from a tag | The audit environment accepts only `main` | The tag deployment cannot receive the audit credential |
| F-037 | Use APFS aliases such as `straße`/`STRASSE`, sigma variants, `ﬀ`/`ff`, or micro/Greek mu | Shared keys use NFC normalization and Unicode case folding | Aliases compare as one path and the second destination fails |
| F-038 | Set an ambient hosted-controls policy path | The script uses the versioned policy path | The audit rejects the override |
| F-039 | Remove or replace the repository visibility event value | The workflow binds `REPOSITORY_VISIBILITY` to `github.event.repository.visibility` | The attestation policy fails closed |
| F-040 | Rank a failed newer run against an older successful run | The selector ranks `.id` before `.run_attempt` | The release gate selects the newest workflow run |
| F-041 | Remove fields or change counts in paginated API pages | The aggregator requires objects, arrays, numeric counts, and exact totals | Malformed or incomplete collections fail |
| F-042 | Report a required context from another GitHub app | Every context requires integration ID 15368 | The hosted-control check rejects the source |
| F-043 | Supply oversized or symlinked runtime manifests | Reads use 1 MiB or 16 MiB limits and descriptor no-follow walks | Unsafe reads fail before parsing or rewrite |
| F-044 | Compromise a repository build step with signer permissions | Repository build and release assembly code still share `id-token: write` and `packages: write` | Permission separation remains pending |
| F-045 | Change the local tag ref during verification or use a mismatched tag object name | One captured tag object, one exact embedded tag name, and `--no-replace-objects` protect all identity reads | The verifier rejects each mutation |
| F-046 | Set `WORKCELL_GO_BIN` before the audit step | The wrapper removes exported tokens and sends one token line to exact `gh` calls | The ambient tool never runs, and other children receive no token |
| F-047 | Remove a broker input from `.dockerignore` | Exact context inclusions and broker `COPY` paths | The mutation test rejects the omission, and the live invariant probe passes |
| F-048 | Move `ENV LD_PRELOAD` after the broker build steps | The exact preload environment appears before every later `RUN` step | The mutation test rejects the late declaration, and the live invariant probe passes |
| F-049 | Source `home-control-plane.sh` or `runtime-user.sh` after `PATH` becomes read-only | Each library checks the exact path before `readonly PATH` | The structural test passes; full `./scripts/container-smoke.sh` passes |
| F-050 | Set the sanitized sentinel with `WORKCELL_GO_BIN`, persist `GOFLAGS=-toolexec`, or export a token to output tools | Exact environment validation, `env -i` re-exec, and token-scoped verifier calls | Mutation tests show that wrappers do not run and Cosign receives no token |
| F-051 | Start the apt broker from the scrubbed environment without the approved preload | `env -i` passes the exact `LD_PRELOAD` and `PATH` values | The focused test requires the preload and the launch remains guarded |
| F-052 | Use role 5 to bypass the default-branch pull-request ruleset | A no-bypass core ruleset protects pull requests, threads, and code-owner review; a separate approval ruleset limits bypass to missing independent approval | Hosted-state migration remains pending |
| F-053 | Start the broker before creating `/run/workcell/apt-broker` | The launch creates that parent with mode 0755 before it starts the broker | The focused test requires creation and mode; full `./scripts/container-smoke.sh` passes |
| F-054 | Run a provider or development wrapper after its sanitizer reassigns `PATH` | The exact `PATH` remains read-only and exported from entry | The structural test rejects later `TRUSTED_PATH` variables and assignments; full `./scripts/container-smoke.sh` passes |
| F-055 | Extract Codex with archive ownership and move it into the runtime | `install -o 0 -g 0 -m 0755` writes the binary, and the extracted source is removed | Metadata validation rejects ownership-preserving `mv`; full `./scripts/container-smoke.sh` passes |
| F-056 | Run the apt helper with the fixed environment but without the approved preload | `fixedEnvironment` includes the exact preload with the existing fixed values | The unit test requires the preload; full `./scripts/container-smoke.sh` passes |
| F-057 | Trigger a Bash syntax error in a late provider block or pass string flags to an `execveat` Perl fixture | Direct stdin scripts isolate command input; numeric flags reach the raw syscall; mapped-user tests cover provider policy | Full `./scripts/container-smoke.sh` passes and diagnostic traps report failures |
| F-058 | Rebuild the managed live-debug image with the omitted Go endpoints | The Dockerfile uses the pinned `https://dl.google.com/go/${go_archive}` endpoint and the exact three host ports: `dl.google.com:443`, `proxy.golang.org:443`, and `sum.golang.org:443` | Pinned-input and injection tests reject endpoint drift; focused control-plane, hardening-profile, Docker lint, and Go tests pass |
| F-059 | Extract a policy with no direct mounts | The serializer writes `[]`, and the secure reader accepts that array | The focused extraction test and full assurance-dry-run scenario pass |
| F-060 | Generate the release manifest without the new broker inputs | One `runtimeBuildContextPaths` helper includes `.dockerignore`, Go module files, adapters, runtime/container, the broker package, and both broker commands | Unit coverage checks the path set; `verify-build-input-manifest.sh` passes with all inputs in the temporary index |
| F-061 | Run cleanup while eight launches use staged inputs | Exported format-aware `launcher.ObserveProcessGeneration` reads `darwin:` and `linux:` records; hoststate cleanup uses it | The live-owner unit test passes; eight parallel auth-status/launch scenarios retain staged tokens and direct sources |

## Results

| ID | Severity | Area | Result |
|---|---|---|---|
| SR-01 | High | Raw execution syscalls | Fixed |
| SR-02 | High | Mutable native execution | Fixed |
| SR-03 | High | Host input staging | Fixed |
| SR-04 | Medium | Runtime state and package broker | Fixed |
| SR-05 | Medium | Release output verification | Fixed |
| SR-06 | Medium | Audit log reads and path trust | Fixed |
| SR-07 | Medium | Credential diagnostics | Fixed |
| SR-08 | Medium | Privileged manual workflows | Fixed |
| SR-09 | Medium | Resource limits | Fixed |
| SR-10 | Low | Runtime error handling | Fixed |
| SR-11 | Medium | Package broker process lifetime | Fixed |
| SR-12 | Medium | Apple workspace materialization | Fixed |
| SR-13 | Medium | Session and host lifecycle trust | Fixed |
| SR-14 | High | Execution guard variant and propagation controls | Fixed |
| SR-15 | Medium | Injection policy path replacement | Fixed |
| SR-16 | Medium | Package broker startup cancellation | Fixed |
| SR-17 | High | Release tag target binding | Fixed |
| SR-18 | Medium | Upstream publisher workflow identity | Fixed |
| SR-19 | Medium | Mutable package broker state and request bounds | Fixed |
| SR-20 | High | Release workflow source binding | Fixed |
| SR-21 | High | Release tag immutability ruleset | Fixed |
| SR-22 | High | Rust `env -S` preload bypass | Fixed |
| SR-23 | Medium | Case-colliding staging destinations | Fixed |
| SR-24 | Medium | Named image tag verification | Fixed |
| SR-25 | Medium | Package broker frontend environment values | Fixed |
| SR-26 | Medium | Injection manifest row integrity | Fixed |
| SR-27 | Medium | Unicode path normalization aliases | Fixed |
| SR-28 | Medium | Package broker shutdown cleanup | Fixed |
| SR-29 | High | Release environment and dispatch contract | Fixed |
| SR-30 | High | Required-status ruleset bypass actors | Fixed |
| SR-31 | Medium | Review-rule bypass actor shape | Fixed |
| SR-32 | Medium | Release image repository and commit tag binding | Fixed |
| SR-33 | Medium | Release tag input and repository identity binding | Fixed |
| SR-34 | High | Mutable release attestation decision | Fixed |
| SR-35 | Medium | Hosted-control bypass-actor parsing | Fixed |
| SR-36 | Medium | Hosted-control audit credential scope | Fixed |
| SR-37 | Medium | Unicode compatibility aliases in path identity checks | Fixed |
| SR-38 | High | Ambient hosted-controls policy override | Fixed |
| SR-39 | Medium | Release attestation visibility binding | Fixed |
| SR-40 | Medium | Release workflow-run ranking | Fixed |
| SR-41 | Medium | Hosted-control pagination integrity | Fixed |
| SR-42 | High | Required status source binding | Fixed |
| SR-43 | Medium | Runtime manifest bounds and path identity | Fixed |
| SR-44 | Medium | Repository build and release assembly privilege separation | Residual |
| SR-45 | Medium | Local release tag object verification | Fixed |
| SR-46 | High | Hosted-control audit credential execution | Fixed |
| SR-47 | Medium | Runtime image build context | Fixed |
| SR-48 | Medium | Runtime image guard propagation | Fixed |
| SR-49 | Medium | Runtime startup PATH initialization | Fixed |
| SR-50 | High | Release publication environment isolation | Fixed |
| SR-51 | Medium | Apt-broker guard environment propagation | Fixed |
| SR-52 | High | Default-branch review bypass scope | Residual |
| SR-53 | Medium | Apt-broker socket parent creation | Fixed |
| SR-54 | Medium | Runtime wrapper PATH propagation | Fixed |
| SR-55 | Medium | Codex provider binary ownership | Fixed |
| SR-56 | Medium | Package broker helper guard environment | Fixed |
| SR-57 | Medium | Container-smoke validation harness | Fixed |
| SR-58 | Medium | Runtime image bootstrap endpoint | Fixed |
| SR-59 | Low | Empty policy mount serialization | Fixed |
| SR-60 | Medium | Release build-input manifest coverage | Fixed |
| SR-61 | Medium | Concurrent launch cleanup | Fixed |

## Findings

### SR-01: Raw execution syscalls bypassed the guard

The Rust library defined `syscall` only through assembly.
Rust export filtering kept that symbol local in the shipped shared library.

Dynamically linked callers therefore resolved `syscall` from libc.
Raw `execve` and `execveat` calls could bypass the mutable ELF control.

The fix exports a public Rust `syscall` wrapper.
The wrapper forwards supported execution calls to the existing guard.

The image build now requires a defined global dynamic `syscall` symbol.
Container tests call raw execution syscalls on both supported Linux architectures.

### SR-02: Mutable native execution had variant bypasses

The strict guard trusted only two mutable roots.
It also depended on readable path content during several checks.

Aliases, execute-only files, anonymous descriptors, deleted descriptors, and
other writable runtime roots could avoid consistent classification.

A path replacement could also change script content after classification.
That race could replace an allowed script with native content.

The fix classifies immutable native files by ownership, mode, ACL access,
ancestor trust, path identity, and descriptor identity.

The guard treats all writable runtime roots as mutable.
It rejects untrusted ELF files through every intercepted execution family.

The guard copies allowed mutable scripts into bounded sealed memory files.
Execution then uses the stable sealed descriptor.

The guard covers `execl`, `execlp`, and `execle` through bounded C variadic
adapters. A glibc-versioned preload adapter also covers versioned libc callers.
It loads before the Rust guard so the guard's `RTLD_NEXT` lookups reach libc.
The guard pins relative and magic paths before execution.

The guard is defense in depth for dynamically linked processes.
It does not stop static processes, inline syscalls, direct `dlopen`, executable
memory mappings, or every libc-internal spawn implementation.
The container and VM boundary remain the primary isolation controls.

### SR-03: Input staging had source replacement races

Document and public-copy staging validated a source path before reading it.
A workspace process could replace that path before the read.

The replacement could redirect staging to another host-readable file.
Directory traversal had the same race for child entries.

The fix opens every source component with `O_NOFOLLOW`.
It traverses directories from opened parent descriptors.

The copy rejects symbolic links, named pipes, sockets, and other special files.
Regression tests replace validated sources and directory children.

### SR-04: Runtime state and package broker trust was incomplete

Runtime scripts accepted state without complete ownership and mode checks.
Package wrappers also consulted caller-controlled environment state.

The privileged package broker inherited more environment than it required.
That inheritance included a test-only helper override.

The fix accepts only root-owned regular state files in a trusted directory.
Package wrappers use a fixed tool path before they read runtime state.

The production broker now starts through an empty environment.
It receives the exact mapped peer UID and uses fixed helper and tool paths.
It no longer receives a configurable broker root.

### SR-05: The release workflow did not verify new outputs

The release workflow produced signatures and attestations before publication.
It did not verify those new outputs in the same workflow.

The fix adds an independent read-only verification job.
The final publisher repeats the checks before publication.

The verifier checks the exact asset inventory, checksums, nine Sigstore bundles,
the image signature, and all ten GitHub attestations.

Every proof binds to the tag commit and release workflow identity.
The verifier rejects attestations from self-hosted runners.

### SR-06: Audit log reads trusted paths and sizes too broadly

Session exports trusted the audit path stored in a session record.
They also read complete audit logs without a size limit.

The fix derives the canonical audit path from the trusted session location.
It rejects symbolic links, hard links, and non-regular files.

The shared reader streams records with a 1 MiB line limit.
It also enforces a 64 MiB total log limit.

### SR-07: Credential diagnostics could reveal input data

Gemini environment errors included malformed lines, keys, or values.
Those fields can contain credential material.

The fix reports only the file, line number, and error class.
It also validates environment variable names before further processing.

### SR-08: Privileged manual workflows accepted other branches

Two manually triggered workflows could run from a non-`main` ref.
Those jobs use protected environments or privileged repository controls.

The jobs now require the exact `refs/heads/main` ref.
YAML-aware validators preserve this requirement.

### SR-09: Host and runtime inputs had avoidable resource risks

Several host helper commands read standard input without a limit.
DNS resolution also had no explicit deadline.

Injection readers and the package broker also accepted unbounded data.
The Rust guard accepted unbounded execution arguments.

The fixes limit helper input to 4 MiB.
DNS lookup now has a five-second timeout.

Injection and policy inputs have file, tree, and entry limits.
Package requests, helper lifetime, and output now have explicit limits.
The execution guard bounds paths, arguments, and environments.

The policy reader parses bytes from one bounded open file.
It calculates the recorded digest from the same bytes that it parses.
This removes a parse-versus-provenance content race.

The Colima launcher now requires an absolute executable path.
This change prevents caller-selected path resolution.

### SR-10: Runtime launch errors were ignored

The native launcher ignored failures while it installed signal handlers.
It could continue without the documented forwarding behavior.

The launcher now stops with a mapped error code.
It reports the failed signal setup.

### SR-11: The package broker did not bound helper descendants

The broker bounded request data but did not reliably stop helper descendants at
the deadline.

The fix starts each helper in a dedicated process group.
It sends `SIGTERM`, waits for the grace period, and then sends `SIGKILL`.
It returns status `124` when the helper deadline expires.

### SR-12: Apple workspace copies lacked complete file and race controls

The preview copier accepted a broader file set and did not bound all materialized
content before it copied workspace entries.

The fix opens each source from a trusted parent descriptor.
It rejects special files and escaping symbolic links.
It limits each file, total content, and entry count.
It checks the completed symbolic-link graph before publication.

### SR-13: Session paths and host timeout cleanup trusted mutable state

Session records and audit reads accepted paths from mutable record content.
Colima timeout cleanup also needed process-group escalation.

The fix derives audit paths from the trusted session location.
It reads session records through owner-only, descriptor-anchored paths.
It reads audit logs through descriptor-anchored paths with bounded reads.
The Colima helper now sends `SIGTERM` and then `SIGKILL` to the child process group.

### SR-14: Execution guard variants could bypass native execution controls

The guard did not consistently classify mutable shebang interpreters or repeated
`PATH` searches. Loader options and loader environment values could also select
mutable code. Spawn file actions could alter a sealed descriptor. Child calls,
including raw syscall calls, could omit the approved guard preload.

The fix parses direct and `env` shebang forms. It rejects `env -S`, split-string,
and preload-unset options. It resolves `PATH` once with bounded segments and
uses the selected path for execution. It rejects mutable loader options, unsafe
loader environment values, and file actions for sealed snapshots. Strict child
execution retains the exact guard preload, and the raw `execve` and `execveat`
syscall paths use the same checks.

The guard also bounds path segments and the complete `PATH` value. Regression
tests cover shebang forms, loader options, loader environment values, spawn
file actions, bounded searches, guard propagation, and raw syscall execution.

### SR-15: Policy path validation allowed a replacement race

The policy reader validated a path before it opened the file. A workspace or
operator process could replace a parent or leaf with a symbolic link after
validation.

The fix opens each parent directory and the leaf with descriptor-anchored,
no-follow operations. The reader parses and hashes bytes from that open
descriptor. A regression test swaps a parent after validation and confirms the
reader does not follow the replacement.

### SR-16: Broker startup cancellation could lose the helper group

The package broker checked cancellation before it had the helper process-group
PID. A forked `setsid` child could then survive the cancellation or timeout.

The fix makes the launcher publish the process-group PID before monitoring
cancellation and the deadline. A delayed-start regression test proves that a
timeout stops the complete helper group and returns status `124`.

The control plane intentionally uses the direct path in `apt-wrapper.sh`.
That path is outside the unprivileged attacker capability and is not a bypass finding.

### SR-17: Release dispatch did not bind the tag target at the trusted gates

The release workflow now runs from `main` for `repository_dispatch`.
The dispatch payload carries the release tag and its 40-hex release commit.
The workflow needed a trusted tag check before release-source checkout.
The publisher also needed a second check before publication.

The fix verifies an annotated signed tag before checkout.
The trusted gate requires the tag target to equal the payload commit.
It also requires the payload commit to equal the current `main` workflow commit.
The publisher repeats the tag-to-payload check before publication.
New releases use the `main` workflow identity and workflow SHA.
The `v1.0.2` release and earlier releases retain the tag workflow identity.
The later check remains a defense-in-depth control.

### SR-18: Upstream publisher accepted a lookalike workflow path

The publisher used a prefix comparison for the workflow path.
A lookalike path could therefore pass the source-workflow check.

The fix requires the exact path `.github/workflows/upstream-refresh.yml`.
The publisher also requires the `main` branch before it downloads the candidate.

### SR-19: Mutable package broker state lacked a complete bounded root

Mutable sessions needed a bounded, non-executable root for all broker state.
The broker also needed explicit pending-input, output, and request-isolation controls.

The broker now serves one Unix socket at `/run/workcell/apt-broker/socket`.
The trusted launcher passes the mapped runtime UID, and the server accepts only that peer UID.
The broker uses a 24 MiB `noexec` root, accepts four concurrent requests,
limits each request to 512 KiB, and limits each output stream to 1 MiB.
It rejects invalid requests and isolates each request failure.

### SR-20: Release dispatch did not bind its workflow source

The tag-triggered release workflow selected source from a tag event context.
That context did not bind the workflow file to the current `main` commit.

The fix uses `repository_dispatch` from the host release procedure.
The workflow runs from `main` and requires a tag and a 40-hex commit payload.
The trusted gate checks the signed tag target against that payload.
It also checks the payload against the current workflow commit.

### SR-21: Release tag rules allowed an administrator immutability bypass

The hosted release-tag rules permitted an administrator bypass for updates and deletion.
That bypass could move a signed release tag after the local signing gate.

The policy now separates tag creation from tag immutability.
The creation ruleset allows the documented administrator role.
The immutability ruleset blocks tag updates and deletion without a bypass actor.
The validator rejects a combined ruleset or an immutability bypass.

### SR-22: `env -S` could remove the execution guard preload

The execution guard accepted split-string shebang options without validating each option.
An attacker could use `--unset=LD_PRELOAD` or an equivalent short option.

The guard now rejects `-S`, `--split-string`, `--unset=`, and attached `-u` options.
The regression tests cover the long and short preload-removal forms.

### SR-23: Case-colliding destinations could merge staged material

Input staging tracked destination names with case-sensitive keys.
Two names that differ only by case could therefore target one case-insensitive path.

The fix reserves destination names with case-insensitive keys.
It creates directories and files with exclusive operations.
It rejects non-empty destinations before materialization.
This prevents merge and overwrite behavior on case-insensitive volumes.

### SR-24: A named image tag could move after digest signing

The release workflow signed an image digest and later published its named tag.
A registry tag could move between those operations.

The verifier now checks the named tag and requires its manifest digest to equal the signed digest.
The publisher repeats this check before release publication.
The check detects tag movement before publication but cannot prevent a later registry rewrite.

### SR-25: Package broker frontend values could launch interactive commands

The package broker accepted arbitrary values for its three preserved environment names.
An attacker could select a browser or pager frontend through `apt-listchanges`.
The [Debian apt-listchanges manual](https://manpages.debian.org/bookworm/apt-listchanges/apt-listchanges.1.en.html) documents these frontends.

The fix accepts only `DEBIAN_FRONTEND=noninteractive`.
It accepts only `DEBCONF_NONINTERACTIVE_SEEN=true`.
It accepts only `APT_LISTCHANGES_FRONTEND=none` or `text`.
Invalid values fail before the privileged helper starts.

### SR-26: Injection manifest fields could corrupt row framing

Copy targets and SSH identity basenames enter a jq-to-shell row protocol.
Newlines, ASCII control characters, and Unicode line separators could split or alter rows.
Invalid UTF-8 could also produce inconsistent field data.

The renderer now rejects invalid UTF-8, control characters, and Unicode line and paragraph separators.
It applies this check before it creates the injection manifest.
Tests cover newline, U+001F, U+2028, invalid UTF-8, and a safe identity basename.

### SR-27: Unicode aliases could bypass path identity checks

NFC and NFD names can represent the same file on normalization-sensitive filesystems.
Lexical containment and destination collision checks did not share one identity rule.

The fix uses shared NFC normalization for host containment checks.
It uses shared normalization and case-folding keys for injection and Apple destinations.
Tests cover NFC and NFD aliases and case-colliding destinations.

### SR-28: Broker shutdown returned before helper cleanup completed

Broker shutdown closed its listener but did not wait for active request handlers.
A helper descendant could therefore survive after `Serve` returned.

The fix tracks active handlers and waits for them during context cancellation.
Each handler terminates the complete helper process group before it returns.
Tests confirm that the helper child is dead before broker shutdown returns.

### SR-29: Release environment rules did not match dispatch execution

The release workflow runs from `refs/heads/main` for `repository_dispatch`.
The release environment contract previously allowed only `v*` deployment tags.
The mismatch prevented the release job from entering its protected environment.

The fix requires the release environment to allow the exact `main` branch.
Validators, policy fixtures, and public release instructions use the same contract.

### SR-30: Required-status rules allowed bypass actors

The hosted-control validator did not reject bypass actors on required status checks.
An actor could therefore bypass required CI status protection.

The fix requires an empty bypass-actor list for the default-branch status ruleset.
Mutation tests reject any required-status bypass actor.

### SR-31: Review-rule bypass actors lacked exact constraints

The hosted-control validator checked the review bypass type but not its count or role.
An extra actor or another repository role could weaken the review gate.

The fix allows zero actors or one repository role 5 actor with pull-request bypass mode.
Mutation tests reject extra actors and incorrect role IDs.

### SR-32: Image verification lacked repository and commit-tag binding

Release output verification checked the signed image digest but did not require the canonical repository or commit tag.
An unrelated image or a moved commit tag could pass the output gate.

The fix requires `ghcr.io/OWNER/REPO` to match the release repository.
It verifies both the release tag and `sha-<commit>` tag against the signed digest.

### SR-33: Release tag checks accepted ambiguous input

The tag-signature script accepted revision syntax and could use ambient repository identity.
Revision expressions could select a different object than the intended release tag.

The fix accepts one bounded canonical release tag.
It requires an explicit safe `OWNER/REPO` value for GitHub verification.
It uses explicit remote and tag references for local verification.

### SR-34: Mutable repository variables controlled release attestations

The workflow read a mutable repository variable to decide whether it created attestations.
An administrator could change that variable before dispatch and restore it before hosted-control auditing.

The fix removes `RELEASE_NO_ATTEST` from the workflow and hosted policy.
All attestation steps and both verifier calls are unconditional.
A fixed guard requires `github.event.repository.visibility` to equal `public`.
Validators and mutation tests reject mutable attestation decisions.

### SR-35: Malformed bypass-actor data failed open

The validator treated a failed bypass-list type assertion as an empty list.
Malformed hosted data could therefore look like an explicit no-bypass policy.

The fix requires each bypass list to be an explicit array.
A mutation test proves that malformed data fails closed.

### SR-36: The audit environment allowed tag deployments

The audit environment allowed `v*` tags and `main`.
No current audit credential consumer runs from a tag.

The extra tag policy increased the credential exposure surface.
The fix permits only the exact `main` branch.
Policy and hosted-state mutation tests reject tag deployment rules.

### SR-37: Unicode compatibility aliases bypassed path identity checks

APFS treats some Unicode spellings as equivalent.
Simple lowercase keys missed `straße`/`STRASSE`, Greek sigma variants, `ﬀ`/`ff`, and micro/Greek mu.

The fix applies NFC normalization and Unicode case folding through `cases.Fold()`.
Path containment and destination collision checks use the shared key.
Focused macOS tests cover these aliases and fail on path or destination reuse.

### SR-38: Hosted-controls audit accepted an ambient policy override

The audit script accepted a policy path from the caller environment.
An attacker with job control could substitute a weaker hosted-controls policy.

The fix uses the versioned policy path inside the repository.
Validators and mutation tests reject the ambient override.

### SR-39: Release attestation visibility was not bound exactly

The attestation policy did not bind its visibility input to the repository event.
A missing or mutable value could stop the release or change its support decision.

The fix binds `REPOSITORY_VISIBILITY` to `github.event.repository.visibility`.
The workflow validator rejects other visibility sources and mutable conditions.

### SR-40: Workflow-run ranking could select the wrong run

The release gate grouped workflow runs by path before it selected one run.
Ranking run attempts before run IDs could select the wrong run identity.

The fix ranks the run ID before `run_attempt`.
The workflow mutation test proves that the selector chooses the newest run.

### SR-41: Hosted-controls pagination checks failed open

The audit script converted missing page fields to empty arrays and zero counts.
Malformed or incomplete hosted API data could therefore look complete.
The ruleset summary request also used one default-size page.
Later active rulesets could remain outside the audit input.

The fix requires each page to contain an object, an array, and an exact count.
Ruleset summaries use strict array pagination.
Mutation tests reject missing fields, wrong types, invalid counts, and incomplete collections.

### SR-42: Required status checks lacked a source binding

The hosted-control validator checked status context names but not their source application.
Another GitHub application could publish a matching context and satisfy the release gate.

The policy and validator require integration ID 15368 for every required context.
Mutation tests reject a different or missing integration ID.

### SR-43: Runtime manifest reads lacked bounds and path identity controls

Runtime manifest readers used unbounded reads and ordinary path opens.
Symlinks could redirect direct-mount or bundle manifests, and workspace validation resolved mutable paths.

The fix limits direct-mount data to 1 MiB and bundle manifests to 16 MiB.
Descriptor-anchored no-follow parent and leaf walks protect reads.
The rewrite checks parent identity before reading, and credential validation keeps the recorded provenance path.
Focused race tests and `go vet` cover these paths.

### SR-44: Repository build and release assembly share signer permissions

Repository build and release assembly code run in a job with `id-token: write` and `packages: write`.
Install verification runs in a separate job without OIDC permissions.
A compromised build step could therefore access signer identity or registry mutation authority.

No permission split exists between repository build and release assembly in this working tree.
Split repository build from release assembly in an OIDC-free job.
Give only the minimal signer job OIDC and package permissions.

### SR-45: Local tag verification did not bind one immutable tag object

The local verifier read the tag object and peeled commit through separate mutable ref lookups.
An attacker could change the tag between those reads.

The verifier did not compare the embedded tag name with the requested name.
Its local `rev-parse` calls also allowed Git replacement objects.

The later `verify-tag --no-replace-objects` call could not repair those earlier choices.
Both local tag scripts capture one tag object before they read the tag name and peeled commit.
All identity reads disable replacement objects.
Race, tag-name, replacement-object, and publisher contract tests reject each mutation.

### SR-46: Hosted-control audit credentials reached ambient tools

The audit verifier trusted an executable path from ambient `WORKCELL_GO_BIN`.
The wrapper also left the hosted-control token exported to the verifier process.
A prior workflow step could persist the override and execute code with the audit credential.

The wrapper removes all exported token names before it starts the verifier.
It sends one bounded token line through standard input.
The verifier rejects ambient Go paths and selects one exact toolchain path.
Only exact `gh` calls receive the token.
The compiled validator runs without token variables.
Behavior and contract mutations cover the complete handoff.

### SR-47: Runtime image context omitted broker build inputs

`.dockerignore` excluded `go.mod`, `go.sum`, `internal/aptbroker`, and the apt-broker command sources.
The Docker build then lacked files required by its broker build step.
The runtime image could therefore fail before it was produced.

The fix adds exact Docker context inclusions and broker `COPY` checks.
Pinned-input mutation tests reject missing inputs.
The live invariant probe supplied positive proof.

### SR-48: Later image build steps lost the execution guard environment

`/etc/ld.so.preload` activated strict child checks during the builder stage.
Later Docker `RUN` steps did not inherit the exact `LD_PRELOAD` value.
Child commands then failed because the execution guard required its approved preload.

The fix sets the exact `LD_PRELOAD` environment before later build steps.
The pinned-input mutation test rejects a late declaration.
The live invariant probe supplied positive proof.

### SR-49: Sourced runtime libraries reassigned a read-only PATH

The entrypoint made `PATH` read-only before it sourced `home-control-plane.sh` and `runtime-user.sh`.
Those libraries assigned the same `PATH` value again.
Bash rejected the assignment, and the built container failed before launch.

The fix checks the exact path before it assigns `PATH` and makes it read-only.
The focused structural test checks both sourced libraries.
Full `./scripts/container-smoke.sh` passed after the fix.

### SR-50: Release publication tools accepted ambient execution controls

The publisher accepted `WORKCELL_SANITIZED_ENTRYPOINT=1` when extra environment names remained.
It then sourced `go-run-env.sh`, which could use ambient `WORKCELL_GO_BIN`.
Go could also read user configuration with `GOFLAGS=-toolexec` while `GITHUB_TOKEN` remained exported.
A previous workflow step could therefore run attacker code with publication authority.

The fix accepts the sentinel only with an exact environment-name set.
Otherwise it re-executes through `env -i`.
It unsets the sentinel and sets `CGO_ENABLED=0`, `GOENV=off`, `GOTOOLCHAIN=local`, and `GOWORK=off`.

The output verifier also kept `GITHUB_TOKEN` exported while it ran Cosign, checksum, and file tools.
The fix captures and validates the token, then unsets `GITHUB_TOKEN` and `GH_TOKEN`.
Cosign runs without token variables, and each exact `gh` call receives only `GH_TOKEN`.
Focused tests show that wrappers do not run and tools receive only their required token.

### SR-51: Scrubbed apt-broker launch removed the required preload

The execution guard became mandatory after the runtime image installed `/etc/ld.so.preload`.
The scrubbed apt-broker launch then removed `LD_PRELOAD` from its `env -i` environment.
The broker process therefore exited with status 126 before it could serve requests.

The fix passes the exact approved `LD_PRELOAD` and `PATH` values through `env -i`.
The focused launch-environment test requires both values.

### SR-52: Default-branch review bypass covers the whole pull-request rule

The default-branch hosted review ruleset gives repository role 5 a bypass for the whole pull-request rule.
That bypass also removes required thread resolution and code-owner review.
This conflicts with `AGENTS.md`, which permits bypass only when independent approval is missing.

The required migration is not present in this working tree.
Create a no-bypass core ruleset for pull requests, thread resolution, and code-owner review.
Create a separate approval ruleset with a role 5 bypass only for missing independent approval.
Apply and verify the hosted-state rollout before closing this residual.

### SR-53: Apt-broker launch omitted its socket parent

The apt-broker server requires `/run/workcell/apt-broker` as its trusted socket parent.
Startup passed the broker command and then waited for a socket that could not exist.
The live smoke reached that wait and failed before the runtime could launch.

The fix creates the broker root before launch and sets mode 0755.
The focused test requires both the creation command and the mode.
Full `./scripts/container-smoke.sh` passed after the fix.

### SR-54: Runtime wrapper sanitizers reassigned a read-only PATH

The provider and development wrappers pinned `PATH` as read-only at entry.
Their sanitizers then assigned a different `TRUSTED_PATH` value.
Bash rejected the assignment, so provider and development launches aborted.

The fix removes the duplicate variables and assignments.
Each wrapper keeps the exact `PATH` pinned and exported at entry.
The focused structural test rejects later reassignment.
Full `./scripts/container-smoke.sh` passed after the fix.

### SR-55: Codex archive ownership failed the strict guard

The Codex tar archive stored its payload with uid/gid 1001.
The Dockerfile moved that file into the runtime without changing ownership.
The strict guard rejected the provider binary because it was not root-owned.

The fix installs the payload with uid 0, gid 0, and mode 0755.
It removes the extracted source after installation.
Metadata validation and the mutation test reject ownership-preserving `mv`.
Full `./scripts/container-smoke.sh` passed after the fix.

### SR-56: Package broker helper environment omitted the guard preload

The broker used a fixed helper environment with `HOME`, `PATH`, and `LC_ALL`.
It omitted `LD_PRELOAD` after the strict guard became mandatory.
The guard then blocked `/usr/bin/id` before helper validation could run.

The fix adds only the exact approved preload to `fixedEnvironment`.
The existing fixed environment values remain unchanged.
The unit test requires the preload.
Full `./scripts/container-smoke.sh` passed after the fix.

### SR-57: Container-smoke checks could fail open

Late provider blocks ran inside command substitutions.
A Bash syntax error could terminate a substitution while the outer script continued.
Late security checks could therefore be skipped.

Raw `execveat` Perl fixtures also passed string-typed flags.
Perl passed a pointer instead of the intended numeric flags.
The fixture then tested `EINVAL`, not the intended flag behavior.

The fix sends each large provider block through direct standard input with isolated command input.
It stages the mapped-user Codex script.
It passes numeric flags to raw `execveat` Perl fixtures.
It runs provider policy tests as the mapped user.
It accepts only exact fail-closed message alternatives.

Diagnostic traps now report syntax and command failures.
Full `./scripts/container-smoke.sh` passed after these fixes.

### SR-58: Live-debug rebuild missed Go bootstrap endpoints

The managed live-debug rebuild downloaded Go from `go.dev`.
The `bootstrap_endpoints` allowlist omitted that host.
Nine curl attempts timed out after 30 seconds.
The direct download then succeeded.
The rebuild failed at `go mod download` because `proxy.golang.org` was outside bootstrap egress.

The fix downloads the pinned archive from `https://dl.google.com/go/${go_archive}`.
The exact bootstrap set now contains `dl.google.com:443`, `proxy.golang.org:443`, and `sum.golang.org:443`.
Pinned-input mutation tests reject endpoint changes.
Focused control-plane, hardening-profile, pinned-input, injection, Docker lint, and Go tests pass.

### SR-59: Empty policy mounts serialized as null

A policy with no direct mounts serialized its mount specification as JSON `null`.
The secure reader requires an array.
Network-only policy launches therefore failed closed.

The serializer now initializes the mount slice as empty.
It writes raw `[]` for a policy with no direct mounts.
The focused extraction test and full assurance-dry-run scenario pass.

### SR-60: Release manifest omitted Go broker inputs

The release build-input manifest listed `runtime/container` and `adapters`.
It omitted `internal/aptbroker`, both broker commands, `go.mod`, and `go.sum`.
A release manifest could therefore omit inputs that the image used.

The fix uses one `runtimeBuildContextPaths` helper.
It includes `.dockerignore`, `go.mod`, `go.sum`, `adapters`, `runtime/container`, `internal/aptbroker`, and both broker commands.
Unit coverage checks the path set.
`verify-build-input-manifest.sh` passes after all new inputs enter the temporary index.

### SR-61: Launch cleanup deleted live staged inputs

Concurrent Workcell launch cleanup compared new `darwin:` and `linux:` process-generation records with legacy `ps` start strings.
It marked live injection bundles as stale.
It deleted staged tokens and direct sources.
Eight parallel auth-status/launch scenarios failed before the fix.

The launcher now exports format-aware `launcher.ObserveProcessGeneration`.
Hoststate cleanup uses this function.
New live-owner unit coverage passes.
Eight parallel auth-status/launch scenarios pass after the fix.

## Residual risks

The execution interposer remains defense in depth.
A fully static process can issue later execution syscalls without interposition.

The dedicated VM and container boundary remains the primary isolation control.
Kernel `noexec` flags protect selected temporary mounts.

An authorized release job can still sign false outputs.
A second trusted identity or maintainer review must detect that case.

The named image check cannot prevent a later registry tag rewrite.
Consumers should pull by the verified digest and use registry immutability where available.
See the [GitHub Container registry documentation](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).

The Rust guard caches resolver and policy state in `OnceLock` values.
A multithreaded process can inherit a lock held by a vanished thread after `fork`.
The interposer has no fork-child lock reset path.
Treat post-fork execution as residual risk and keep strict child paths single-threaded.

## Validation

Focused contract, race, and vet checks passed on 2026-08-12.
Full `./scripts/container-smoke.sh` passed after the harness fixes.
Focused control-plane, hardening-profile, pinned-input, Docker lint, and Go tests passed.
The complete Go race suite and `go vet ./...` passed.
`./scripts/validate-repo.sh` passed with 18 scenarios, zero failures, and five certification skips.
`./scripts/verify-invariants.sh` passed its live rebuild, session, package, profile, and control-plane checks.

Dependency and secret scans found no current findings in govulncheck, OSV, cargo audit, or Trivy.
Semgrep findings matched audited unsafe syscalls, Git SHA-1 object IDs, generic JSON validation, and constant-format benchmark output.
Trivy Docker findings were false positives or style items for snapshot/update RUN statements and interactive images without health endpoints.

## Discovery gates

Ready for validation:

- F-001 through F-061 have empirical evidence or a documented source proof.

Not ready:

- None.

Retracted or downgraded:

- Scanner-only path findings were retracted when descriptor tests disproved them.
- Expected dynamic command calls remained informational after threat-model review.

Simplification gate:

- The review used focused harnesses instead of a new test framework.
- The fixes reuse existing descriptor and validation helpers.
- The review removed speculative controls that had no demonstrated attack path.

Public scrub gate:

- The review covered reports, commands, fixtures, scanner output, and runtime logs.
- The public record omits secrets, private hosts, raw logs, and unnecessary exploit steps.
- No private operational detail remains in this document.
