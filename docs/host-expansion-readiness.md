# Host Expansion Readiness

This page records the Phase 12 readiness gate for Linux and Windows host
promotion. It does not promote any new operator host.

Current support remains unchanged:

- Apple Silicon macOS is the reviewed operator-host launch path
- Linux `amd64` is a trusted validation-host lane, not an operator launch host
- Linux `arm64`, Raspberry Pi, Windows WSL2, and native Windows remain
  unsupported operator-host targets

## Support Tiers

| Tier | Meaning |
|---|---|
| `strict` | preserves the dedicated VM plus hardened container boundary and passes backend-specific invariant checks |
| `compat` | lower-assurance supported path with explicit diagnostics, rollback, and support-matrix rows |
| `preview` | limited rollout with explicit gates and no broad support claim |
| `certification candidate` | implementation under live certification before support promotion |
| `experimental` | investigation or prototype work with no support expectation |
| `unsupported` | blocked by default with fail-closed diagnostics |

`compat` is not strict parity. A host may move through `certification candidate`
or `preview` without becoming broadly supported.

## Candidate Scope

The roadmap sequences GitHub Copilot CLI Tier 1 provider parity before the
Linux host-expansion slice. Phase 13 remains the next runtime-target candidate
after that provider-parity work and may evaluate Linux `amd64` `local_compat`
as a narrow candidate. That evaluation must select one distro/runtime matrix
before implying Linux operator support. If that selected matrix cannot preserve
Workcell's support boundary, the phase must record the blocker instead of
substituting a different runtime silently.

Later host tracks remain separate:

- Linux `arm64` must identify package, runtime, kernel, cgroup, and live
  hardware prerequisites separately from Linux `amd64`
- Raspberry Pi must stay experimental until memory, disk I/O, SD-card
  reliability, kernel, and workload limits are documented and certified
- Windows WSL2 must document filesystem semantics, path translation, WSL/Docker
  integration, credential isolation, packaging, and endpoint controls
- native Windows must remain a separate investigation because its process,
  filesystem, credential, and endpoint-control model differs from WSL2

## Promotion Criteria

Any future host promotion must land atomically with:

- a canonical host-support matrix row for the exact host, architecture, target
  kind, provider, and assurance class
- fail-closed `doctor`, `inspect`, and launch diagnostics outside that row
- install, uninstall, upgrade, rollback, and support-bundle guidance
- deterministic repo-required tests for selection, unsupported combinations, and
  remediation text
- live certification evidence on real operator hosts when the support claim
  depends on host behavior
- documentation that distinguishes CI-proven, locally mirrored, and
  certification-only evidence

No Linux or Windows `strict` claim may land until an equivalent dedicated VM plus
container boundary is implemented and proven for that host family.

## Fail-Closed Behavior

Unsupported host combinations must remain blocked by default. Diagnostics should
show:

- selected host OS and architecture
- target kind, provider, and assurance class
- support-matrix status and reason
- whether evidence is repo-required, locally mirrored, certification-only, or
  absent
- the next supported action, such as using the current macOS path or running a
  validation-host lane

There must be no automatic fallback from a blocked host to a different backend.

## Quality Gate

Host expansion changes are high-risk because support language can outrun proof.
Every host-expansion change must remove vague support claims, duplicated matrix
logic, and untested remediation text before validation is considered complete.
