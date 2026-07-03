# OWASP Agentic Top 10 Control Mapping

This page maps the
[OWASP Top 10 for Agentic Applications (2026)](https://genai.owasp.org/resource/owasp-top-10-for-agentic-applications-for-2026/)
(ASI01–ASI10) to Workcell's actual controls. It is a posture map for
reviewers, not a certification, an audit result, or a claim of conformance.

Workcell is a runtime boundary, not an agent-safety layer. It does not prevent
prompt injection, goal hijack, or context poisoning inside the agent — it
contains their blast radius by keeping host credentials, host state, and
publication authority outside the boundary the agent runs in. Verdicts here are
deliberately conservative:

- **Covered** — the boundary structurally addresses the category (isolation,
  not validation).
- **Partial** — the boundary addresses some vectors while others remain open.
- **Out of scope** — a single-provider local runtime does not implement the
  surface the category describes (for example, inter-agent messaging).

Every mechanism cited links to a control documented in this repository
([threat model](threat-model.md), [invariants](invariants.md),
[injection policy](injection-policy.md), and the release posture in the
[README](../README.md)); anything not documented here is not claimed. This
mapping describes the default `strict` safe path — lower-assurance lanes
(`development`, `breakglass`, `--cache-profile standard`, `--agent-autonomy
prompt` changes) shift several verdicts and are called out where they matter.
See [support tiers](support-tiers.md) for the assurance vocabulary.

Category titles follow the OWASP source; the `ASInn:2026` identifiers are the
stable cross-reference for audits and keyword crosswalks.

## Coverage summary

| Category | Verdict |
|---|---|
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

Manipulation of agent goals or plans through direct or indirect instruction
injection. Workcell masks and re-seeds the repo control plane
(`AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `.mcp.json`,
`.github/copilot-instructions.md`, provider and IDE directories, and git
`hooks`/`config`) so workspace content cannot silently take over the provider
control plane; workspace instructions enter only as reviewed imports, and the
VM-plus-container boundary contains the consequences.

Honest limit: the runtime cannot prevent in-content prompt injection from
steering the agent while it runs. It removes one hijack vector (control-plane
takeover) and contains the blast radius.

### ASI02:2026 Tool Misuse and Exploitation — Partial

Unsafe tool chaining, loops, or excessive invocations despite valid
permissions. Writes are confined to the selected workspace; the VM applies a
reviewed egress posture and explicit network profiles; `--container-mutability
readonly` blocks package-manager writes; git execution-control paths are
masked; provider-side guardrails (Codex rules, the Claude bash hook, Gemini
managed settings) are explicitly labeled secondary defenses, not the boundary.

Gap: there is no rate limiting, loop detection, or per-tool-call policy. Misuse
within the boundary is bounded, not detected.

### ASI03:2026 Agent Identity and Privilege Abuse — Partial (strong)

Delegated authority, ambiguous identity, and trust assumptions leading to
unauthorized actions. This is Workcell's core design: no ambient host
passthrough of home directories, keychains, git credential helpers,
`docker.sock`, SSH/GPG/provider agent sockets, or provider-home state.
Credentials enter only through the operator-owned injection policy, staged
host-side and mounted read-only; the Copilot token handoff re-execs without the
token in its environment and unlinks the handoff file; credential sources under
the workspace are rejected; `workcell why` explains each credential decision.

Gap: Workcell does not down-scope the injected provider token itself. The
token's own privileges remain an operator and provider decision.

### ASI04:2026 Agentic Supply Chain Vulnerabilities — Partial

Compromise of external tools, MCP servers, schemas, or prompts the agent
dynamically trusts. Repo-local `.mcp.json` and `.github/mcp.json` are masked on
the safe path, so the workspace cannot inject MCP servers — MCP config enters
only through the reviewed injection policy. For Workcell's own supply chain:
pinned upstream provider verification, reproducible builds, keyless
Sigstore/Cosign signing of the image, source bundle, SBOMs and manifests,
GitHub attestations, and hosted-control audits.

Gap: the threat model records MCP servers as operator-reviewed extension
points. Workcell does not vet MCP server behavior.

### ASI05:2026 Unexpected Code Execution — Covered (isolation)

Agent-generated or agent-triggered code executing without validation or
isolation. This is the primary purpose of the product: a dedicated Colima VM
plus a hardened container as the execution boundary, durable writes limited to
the workspace mount, host staging roots mounted read-only, no `docker.sock`,
`readonly` container mutability as the strongest lane, git hook/config masking,
and invariant plus container-smoke tests.

Caveat: "covered" means isolated, not validated. Code still runs with full
effect inside the workspace; the host-side signed `publish-pr` gate protects
what leaves it. The default lane is `--container-mutability ephemeral`, under
which package-manager mutations run as root and in-container control-plane
integrity is explicitly lower-assurance; `readonly` is the strongest
(non-default) lane. The VM-plus-container isolation boundary holds either way —
"covered" is about isolation of execution, not the integrity of state inside
it.

### ASI06:2026 Memory and Context Poisoning — Partial

Injection or leakage of agent memory or contextual state that influences future
reasoning. Provider homes are session-local and rebuilt each launch from
immutable adapter baselines plus reviewed imports, so poisoned state does not
persist across sessions on the default path; `--cache-profile off` is the
default and `standard` is an explicitly labeled lower-assurance choice.

Gap: there is no in-session detection or sanitization of poisoned context from
workspace content.

### ASI07:2026 Insecure Inter-Agent Communication — Out of scope

Manipulation of messages between agents, planners, and executors. Workcell runs
a single provider-native process per session inside the boundary and implements
no multi-agent messaging layer to secure. Any subagent traffic internal to a
provider CLI stays inside the container boundary and outside Workcell's control
plane.

### ASI08:2026 Cascading Failures — Partial (containment)

Small agent failures propagating through connected systems. The boundary
structurally cuts the main propagation paths from a session into the host and
org: no host credentials to pivot with unless the operator explicitly injects
them, no docker socket, and no use of the host-side signed `publish-pr` flow
(which blocks unsigned ranges and over-broad diffs) from inside the container.

Gap: Workcell does not orchestrate agent fleets and has no circuit-breaker or
health mechanisms. It limits a cascade's reach; it does not manage cascades.

### ASI09:2026 Human-Agent Trust Exploitation — Partial

Exploiting human over-reliance through misleading explanations or authority
framing. Workcell does not rely on provider prompts to describe network posture
after the fact — posture claims come from the runtime, not the agent;
lower-assurance choices are recorded in launch and runtime state rather than
implied equivalent; the support-tier vocabulary prevents overclaiming; and
`workcell session diff` compares actual workspace changes against the recorded
clean git base and fails closed, so review rests on ground truth rather than
the agent's narrative.

Honest limits: `--agent-autonomy yolo` is the default (prompt-approval is the
opt-out), and Workcell adds no in-loop human approval of individual agent
actions.

### ASI10:2026 Rogue Agents — Partial (contain and audit)

Agents acting beyond intended objectives through goal drift, collusion, or
emergent behavior. Containment is identical to ASI05 and ASI03 — on the safe
path a rogue agent cannot reach host secrets, use host-side publication
authority, or escape the workspace. The important qualifier: publication is
blocked only for host-side authority; if the operator explicitly injects
publishing credentials (for example `github_hosts`/`github_config` or SSH
material — an explicit credential-injection decision available on the strict
path, not a lower-assurance lane), an agent can publish from inside the
container using that injected authority. Containment adds host-side
auditability: the profile audit log lives on the host, outside the contained
agent's reach, and each record includes a chained digest of the prior record;
durable host-side session records survive `--gc`. `workcell session timeline`
prints the audit-log entries for a session, and `workcell session export`
emits a JSON bundle of the session record plus its audit records.

Gap: the chained digests provide the basis for order and corruption
verification, but Workcell does not yet ship a verifier that checks the chain,
and the chain is not externally anchored — so the audit log is evidence to
review, not proof of tamper-evidence against a host-level attacker. There is
no behavioral monitoring or rogue-behavior detection; discovery of a contained
rogue agent is post-hoc via audit evidence.

## Sources

- [OWASP Top 10 for Agentic Applications for 2026](https://genai.owasp.org/resource/owasp-top-10-for-agentic-applications-for-2026/)
- [Workcell threat model](threat-model.md), [invariants](invariants.md),
  [injection policy](injection-policy.md), and
  [enterprise evidence baseline](enterprise-evidence-baseline.md)
