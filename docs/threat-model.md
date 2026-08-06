# Runtime Boundary Threat Model

This threat model covers the supported local Workcell launch targets. It also
states the limits of preview targets and host-owned controls.

For build and release threats, see the [CI/CD threat model](ci-threat-model.md).

## Support boundary

| Target | Status | Runtime boundary | Network control |
|---|---|---|---|
| Colima `local_vm` | Supported strict target | Dedicated profile VM and runtime container | Workcell profile-wide allowlist |
| Docker Desktop `local_compat` | Supported compatibility target | Docker Desktop and runtime container | No Workcell allowlist |
| AWS and GCP `remote_vm` | Preview-only and launch-blocked | Dry-run broker plan only | Provider design only |
| Apple container | Preview-only with no operator launch path | Evaluation code only | No operator-session control |

## Assets

| Asset | Required protection |
|---|---|
| Host credentials, homes, keychains, and sockets | The safe path must not expose them. |
| Injected credentials | Only the selected session path can receive staged material. |
| Workspace and Git history | The runtime must limit writes and control-plane influence. |
| Runtime and adapter baselines | Workspace content must not replace reviewed controls. |
| Durable session state | Agent code must not own the authoritative record. |
| Audit log and seal | Offline changes must cause verification failure in the signed region. |
| Host signing key | Runtime code and reports must not receive the private key. |

Release assets belong to the separate CI/CD threat model.

## Attacker capabilities

| Actor | Capability |
|---|---|
| Malicious repository | Supplies instructions, source, hooks, configuration, and executable files. |
| Compromised MCP server | Returns hostile content and tries to widen runtime access. |
| Compromised provider service | Returns hostile output through an allowed provider connection. |
| Malicious provider output | Causes the agent to use tools or change files outside the approved scope. |
| Operator error | Selects a lower-assurance mode or an endpoint set that permits unnecessary access. |
| Same-user host malware | Reads or changes files that the operator account can access. |
| Host-root attacker | Controls host state and can use the session signing key. |

The safe-path controls address repository and runtime attackers. They do not
protect a compromised operator account or host root.

## Trust boundaries

| Boundary | Trusted side | Untrusted side | Important limit |
|---|---|---|---|
| Operator host to Colima | Host launcher and state | Dedicated profile VM | One profile shares egress rules across its containers. |
| Operator host to Docker Desktop | Host launcher and state | Compatibility container | Docker Desktop is not a dedicated Workcell VM. |
| Runtime to provider process | Runtime controls | Provider CLI and model output | Workcell does not isolate processes in one session from each other. |
| Host to injected material | Host validator and staging | Mounted or copied session input | A crash can leave staged plaintext until cleanup. |
| Host to workspace | Host launcher | Repository content | The safe path masks mutable control files and Git paths. |
| Host to durable audit state | Host appender and signer | Sandboxed agent | Host root can rewrite and sign state. |

## Abuse paths and controls

| Abuse path | Control owner | Protected asset | Blocked action | Evidence |
|---|---|---|---|---|
| Repository replaces provider controls | Host launcher and adapter | Runtime baseline | Use mutable repository control files as trusted baseline | Invariant tests and adapter tests |
| Repository uses mutable native executables | Rust syscall shim | Runtime boundary | Execute protected native paths from mutable state | Rust tests and container validation |
| Session receives a host socket or home | Host launcher | Host credentials and control plane | Mount forbidden host paths on the safe path | Dry-run invariant checks |
| Session changes Git hooks or configuration | Host launcher and Rust shim | Workspace and Git history | Use mutable Git control paths or unsafe Git overrides | Git-policy tests and invariant checks |
| Session sends unrestricted Colima traffic | Colima egress helper | Host and network boundary | Reach an IP and port outside the profile allowlist | Egress helper tests and launch summary |
| Policy disables Colima enforcement | Host launcher | Network boundary | Change `NETWORK_POLICY` through injection policy | Injection parser tests |
| Agent changes durable session history | Host audit appender and verifier | Audit log and seal | Change chained records without verification failure | Audit-seal tests and `session verify` |
| Agent reads the signing key | Host launcher | Host signing key | Mount the private key into the runtime | Mount-source checks and support-bundle tests |

Versioned runtime profiles can add fixed endpoints. Operator policy can also add
endpoints. Both inputs broaden the allowed set.

## Residual risks

| Risk | Result |
|---|---|
| Colima rules are profile-wide. | The last launch controls all active containers in that profile. |
| A Colima `breakglass` launch clears the profile rules. | Existing strict containers lose Workcell egress enforcement. |
| Colima rule replacement is not atomic. | A setup failure can leave active profile containers without default-deny rules. |
| A policy change keeps established connections. | A connection can continue after the new endpoint set removes its destination. |
| Allowed host names resolve to shared IP addresses. | Another host on the same IP can remain reachable. |
| Docker Desktop has no dedicated Workcell VM. | It provides lower isolation than the strict target. |
| Docker Desktop has no Workcell allowlist. | Host or Docker Desktop controls determine egress. |
| MCP servers and provider output remain untrusted. | The agent can act on hostile content within its granted tools. |
| Audit signing uses a host key. | Host root can rewrite a chain and create a new valid seal. |
| Initial legacy audit records have no chain. | Signed verification does not protect that initial prefix. |
| Explicit lower-assurance modes remain available. | The operator accepts their stated downgrade. |

Do not run concurrent sessions with different network policies in one Colima
profile.

## Exclusions

This model does not claim protection against these events:

- A host-root compromise.
- Same-user malware that can read operator-owned state.
- A compromised provider service outside the Workcell runtime.
- Security properties for a launch-blocked preview target.
- Isolation between processes in one runtime container.

The [unsafe-code checklist](unsafe-code-audit-checklist.md) records each Rust
unsafe class, its invariant, and its required review.

See the [OWASP Agentic mapping](owasp-agentic-mapping.md) for the application
risk mapping.
