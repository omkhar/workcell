# OWASP Agentic Top 10 control mapping

This page maps the
[OWASP Top 10 for Agentic Applications (2026)](https://genai.owasp.org/resource/owasp-top-10-for-agentic-applications-for-2026/)
to shipped Workcell controls.
It is not a certification or a claim of conformance.

Workcell contains an agent in a runtime boundary.
It does not analyze the goals, prompts, memory, or output of the agent.
It also does not decide if agent behavior is safe.

This mapping applies first to the default supported target:

- macOS on arm64
- the `colima` target
- the `strict` mode
- the default `ephemeral` container mutability

The `docker-desktop` target is a supported compatibility path with lower assurance.
Remote VM targets are preview or validation paths.
The `apple-container` target is preview-only.
Workcell blocks operator launch for this target.
See [support tiers](support-tiers.md) for the current target matrix.

This page uses these verdicts:

- **Covered**: The runtime boundary directly addresses the category.
- **Partial**: The boundary addresses some parts of the category.
- **Out of scope**: Workcell does not provide the system that the category describes.

The word **covered** means containment, not content validation.
An operator can inject selected credentials into a session.
This action gives the agent the authority of those credentials.

The `ASInn:2026` identifiers are the audit cross-references.
Use the linked OWASP source for the official category text.

## Coverage summary

| Category | Verdict |
| --- | --- |
| ASI01:2026 Agent Goal Hijack | Partial |
| ASI02:2026 Tool Misuse and Exploitation | Partial |
| ASI03:2026 Agent Identity and Privilege Abuse | Partial (strong) |
| ASI04:2026 Agentic Supply Chain Vulnerabilities | Partial |
| ASI05:2026 Unexpected Code Execution | Covered (isolation) |
| ASI06:2026 Memory and Context Poisoning | Partial |
| ASI07:2026 Insecure Inter-Agent Communication | Out of scope |
| ASI08:2026 Cascading Failures | Partial (containment) |
| ASI09:2026 Human-Agent Trust Exploitation | Partial |
| ASI10:2026 Rogue Agents | Partial (contain and audit) |

## Per-category detail

### ASI01:2026 Agent Goal Hijack — Partial

This category covers instructions that change an agent goal or plan.

By default, Workcell masks the root control files for each provider in non-breakglass mode.
It also masks Git hooks and paths that can change Git configuration.
These controls stop workspace content from directly replacing the managed control plane.

The acknowledged `--allow-control-plane-vcs` path exposes selected paths read-only.
The acknowledged `--allow-repo-mcp` path exposes repository MCP files.
Both paths lower assurance.
`breakglass` exposes the live workspace control plane.

In non-breakglass modes, Workcell imports supported root instruction files.
This import applies to Codex, Claude, and Gemini.
It reads each import from the live regular file in the workspace.
It puts the import in the managed provider home.
Copilot does not import a workspace instruction file.
`breakglass` skips this import.

Workcell does not validate the meaning of an imported instruction.
It does not detect prompt injection in source files, issue text, tool output, or network data.
The runtime boundary only limits the effects of a successful hijack.

### ASI02:2026 Tool Misuse and Exploitation — Partial

This category covers unsafe use of tools that the agent can access.

The default Colima target puts the tools in a dedicated VM and a container.
The selected network profile controls outbound destinations.
On the default path, durable agent writes go to the workspace.
Workcell masks Git hooks and mutable Git configuration.

The default `ephemeral` container permits package changes inside the session.
The `readonly` container mode blocks package-manager writes and gives stronger assurance.
Provider rules and hooks are secondary controls.
They are not the runtime boundary.

Workcell does not limit tool-call rates.
It does not detect loops or unsafe tool sequences.
It contains tool misuse but does not identify it.

### ASI03:2026 Agent Identity and Privilege Abuse — Partial (strong)

This category covers misuse of identity and delegated authority.

The safe path does not pass ambient host credentials into the runtime.
It excludes host homes, keychains, credential helpers, provider homes, and agent sockets.
It also excludes `docker.sock` and provider authentication state.

Reusable credentials enter through an operator-owned injection policy.
The host copies selected credential sources into a staging area that the launcher owns.
Only the staged copy enters the runtime through a mount with read-only access.
Workcell rejects credential sources that are inside the workspace.
`workcell why` explains each credential decision.

The Copilot token path has a narrow exception.
It uses a temporary handoff mount with read-write access.
The runtime needs this access to remove the file.
The runtime moves the token to a transient file and removes the mounted file.
It then exports the token only to the Copilot child process that Workcell manages.

Explicit credential injection increases session authority.
Workcell does not reduce the privileges of an injected token.
The operator and the provider control those privileges.

A Gemini CLI launch can permit interactive authentication.
This path requires a TTY and no provider arguments.
Other managed Gemini operations that require authentication fail closed.
After authentication, Gemini keeps this state in the provider home for the session.

### ASI04:2026 Agentic Supply Chain Vulnerabilities — Partial

This category covers untrusted tools, MCP servers, schemas, and prompts.

Workcell denies repository MCP files by default in each non-breakglass mode.
The denied files are `.mcp.json` and `.github/mcp.json`.
Default denial replaces each present MCP file in the repository with an empty configuration.
An operator can use `--allow-repo-mcp` with a dated acknowledgement.

Workcell records that this exception has lower assurance.
The acknowledged path refuses symlinked MCP files.
It also refuses parent directories that are symlinks.

An injection policy can provide reviewed MCP configuration.
Workcell does not inspect the behavior of an MCP server.
The operator must review each server and its authority.

Workcell pins its provider inputs, actions, tools, and container bases.
Release controls include reproducible builds, SBOMs, signatures, and attestations.
These controls protect the Workcell supply chain.
They do not protect every tool that an agent can select at run time.

### ASI05:2026 Unexpected Code Execution — Covered (isolation)

This category covers code that runs without sufficient isolation.

The default strict target uses a dedicated Colima VM and a hardened container.
It does not mount `docker.sock` or host credential stores.
By default, Workcell masks control-plane files in the workspace.
It also masks Git hooks and paths that can change Git configuration.
Host staging roots are read-only, except for the narrow Copilot handoff.

This masking does not apply to `breakglass`.
`--allow-control-plane-vcs` exposes selected control-plane paths read-only.
`--allow-repo-mcp` exposes repository MCP files.
Both acknowledged exceptions lower assurance.

Invariant tests and container smoke tests verify configuration and container controls.
Local certification verifies this boundary on a live Colima target in strict mode.

The Docker Desktop target has the lower-assurance `compat` class.
It does not provide the dedicated VM boundary of the Colima target.
The Apple container target is preview-only.
Workcell blocks its operator launch.

Workcell does not validate code before it runs.
Code can change the workspace and other writable session-local state.
The default `ephemeral` container also permits root package changes.
The `readonly` mode gives the strongest container posture that Workcell manages.

GitHub publication is a separate host action on the normal path.
This separation does not remove authority that an operator explicitly injects.

### ASI06:2026 Memory and Context Poisoning — Partial

This category covers untrusted state that affects later reasoning.

Workcell builds each provider home from an immutable adapter baseline.
The default cache profile is `off`.
With the `off` profile, provider-home state does not persist between sessions.
The `standard` cache profile is an explicit lower-assurance choice.

The workspace is durable.
Root instruction files enter later Codex, Claude, and Gemini sessions automatically.
Nested instruction files remain visible in the workspace for their path scope.
Workcell does not validate the meaning of these files.

A poisoned workspace can therefore affect later sessions.
The provider-home rebuild does not remove this risk.
Workcell does not detect or sanitize poisoned context.

### ASI07:2026 Insecure Inter-Agent Communication — Out of scope

This category covers messages between agents, planners, and executors.

Workcell can run more than one detached session.
It does not connect those sessions through an inter-agent protocol.
It also does not authenticate or validate messages between agents.

A provider can implement its own subagent behavior.
That behavior is outside the Workcell control plane.
Workcell contains local provider processes.
Provider service traffic can cross the network boundary.
Workcell does not authenticate or validate provider-native subagent messages.

### ASI08:2026 Cascading Failures — Partial (containment)

This category covers a failure that spreads through connected systems.

The default strict target does not expose host credentials.
It does not expose the Docker socket or host publication authority.
The network profile also limits outbound destinations.

Explicit credential injection can give the agent access to other systems.
The effect depends on the authority of each injected credential.
The Docker Desktop target does not supply a VM boundary that Workcell manages.

Workcell does not manage an agent fleet.
It has no circuit breaker, service-health controller, or cross-session failure policy.
It limits access to some connected systems but does not manage a cascade.

### ASI09:2026 Human-Agent Trust Exploitation — Partial

This category covers unsafe human trust in agent statements or actions.

Workcell reports runtime posture from launcher and target state.
It records lower-assurance choices in session and audit records.
`workcell session diff` compares workspace changes with the clean Git base from launch.
The command requires a clean launch workspace and recorded Git metadata.
It refuses a linked worktree or an unavailable base commit.

`workcell session verify --id SESSION_ID` verifies the audit chain and its signed seal.
`workcell session timeline` shows the records for one session.
`workcell session export` produces JSON or OCSF-mapped JSON Lines evidence.
These commands give the operator evidence that is independent of agent statements.

The default agent autonomy is `yolo`.
Workcell does not require human approval for each agent action.
An operator must still review changes and publication evidence.

### ASI10:2026 Rogue Agents — Partial (contain and audit)

This category covers an agent that acts outside its intended objective.

On the default strict path, the agent cannot use ambient host secrets.
It also cannot use the credentials for host-side PR publication.
The VM and container limit its access to the host.

An operator can explicitly inject credentials into the session.
The agent can use the authority of those credentials.
Injected GitHub CLI state can permit HTTPS publication.
SSH material can permit publication only when policy also permits the remote SSH endpoint.
Workcell records the injection decision but does not prevent authorized use.

The host keeps the profile audit log outside the runtime boundary.
On chain-capable targets, each audit record has a record digest.
Each later record includes the prior record digest.

Workcell signs the final session head with a per-host ECDSA P-256 key.
`workcell session verify` recomputes the chain and verifies this host signature.
It fails closed if a record at or before the signed session head changes.
It also fails closed if the seal is invalid or missing.

`workcell session export` can produce a JSON bundle or an OCSF 1.3.0 stream.
The OCSF output applies the shared redaction rules.
`workcell --gc` does not delete durable session records.
An operator must delete these records separately.

The signed seal does not protect against host-root compromise.
A host-root attacker can read the signing key and can sign a changed chain.
The Apple container preview writes lifecycle records without signatures.
Verification fails closed for these records.
Workcell blocks operator launch for that target.

Workcell does not detect rogue intent or behavior.
It provides containment and evidence for later review.

## Sources

- [OWASP Top 10 for Agentic Applications for 2026](https://genai.owasp.org/resource/owasp-top-10-for-agentic-applications-for-2026/)
- [Workcell threat model](threat-model.md)
- [Workcell invariants](invariants.md)
- [Injection policy](injection-policy.md)
- [Support tiers](support-tiers.md)
- [Signed session audit records](signed-session-audit-records.md)
- [Enterprise evidence baseline](enterprise-evidence-baseline.md)
- [Provenance and signing](provenance.md)
