# Threat Model

## Assets

- the host's credentials, homes, keychains, and agent sockets
- the selected workspace and git history
- the reviewed runtime and adapter baselines
- release materials: image, source bundle, SBOMs, signatures, and manifests

## Trust boundaries

1. host OS
2. dedicated Colima VM
3. hardened runtime container
4. provider-native process inside the container
5. explicit injected material staged for the session
6. repository-hosted GitHub controls used for release and branch protection

## Main attacker models

### Malicious repository content

Untrusted workspace files try to change provider behavior, steal secrets, or
influence host publication flows.

### Malicious or compromised MCP server

An MCP server or related config attempts to widen network or filesystem access
from inside the session.

### Mis-scoped operator trust

An operator accidentally chooses a lower-assurance mode and assumes it is
equivalent to the default path.

### Supply-chain or release tampering

A workflow, hosted control, or release input changes in a way that weakens the
published provenance story.

## Primary abuse paths

- repo-local control-plane files replacing reviewed provider baselines
- host socket or credential passthrough through the runtime boundary
- uncontrolled egress on the managed path
- unsafe provider-native flag overrides
- unsigned or weakly attested release publication

## Controls

- dedicated VM plus container boundary
- explicit runtime profiles with separate assurance labels
- masking and re-seeding of provider control-plane files
- explicit injection-policy inputs instead of ambient host passthrough
- invariant tests and container smoke checks
- signed commits, branch rulesets, and hosted-control audits
- Sigstore-based release signing plus GitHub attestations in the canonical repo

## Residual risks

- the full macOS boundary is still a local or self-hosted exercise, not a
  GitHub-hosted guarantee
- `breakglass`, prompt autonomy, and other explicit downgrades remain
  operator-controlled trust decisions
- MCP servers remain extension points and need deliberate operator review
