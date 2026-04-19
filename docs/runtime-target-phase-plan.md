# Runtime Target Deterministic Phase Plan

This document turns the runtime-target expansion program into deterministic
delivery phases that can be completed, reviewed, and verified atomically. It
complements:

- [ROADMAP.md](../ROADMAP.md) for high-level direction
- [docs/runtime-target-expansion-plan.md](runtime-target-expansion-plan.md) for
  the durable program model
- [docs/implement-first-delivery-plan.md](implement-first-delivery-plan.md) for
  the current active slice

Current repo status:

- Phase 0 is implemented in the validation substrate
- later phases remain planning targets until their code and evidence land

## Phase Completion Contract

Each phase is complete only when:

- code, docs, and support-boundary claims land in the same change set
- `./scripts/validate-repo.sh` is green without depending on live Colima,
  cloud state, or real provider credentials
- environment-dependent runtime proof stays in explicit certification lanes
  rather than the repo-required validation path
- the phase does not overstate host, backend, or assurance support beyond what
  the current evidence proves

## Phase 0: Validation substrate hardening

Goal:

- separate deterministic repo-required validation from local runtime or cloud
  certification smoke

Deliverables:

- scenario metadata distinguishes repo-required validation from certification
  smoke
- `./scripts/validate-repo.sh` runs only the repo-required scenario tier
- local runtime smoke remains available through an explicit certification lane
- docs explain which commands are repo-required versus certification-only

Complete when:

- repo validation no longer depends on live Colima or cloud state
- certification smoke still has an explicit, documented invocation

## Phase 1: Session platform completion and target taxonomy freeze

Goal:

- finish the current session-platform slice while freezing `target kind`,
  `assurance class`, and `workspace transport` as separate control-plane
  concepts

Deliverables:

- coherent session inventory, inspection, and detached control surfaces
- safer default worktree-per-session behavior where the current slice calls for
  it
- target-aware session rendering, diagnostics, and durable metadata

Complete when:

- the shipped session surface remains coherent under deterministic scenario and
  unit coverage
- target taxonomy is fixed in code, docs, and session records

## Phase 2: State-model decoupling and Colima driver extraction

Goal:

- remove program-level Colima shaping while preserving the current strict
  macOS behavior

Deliverables:

- session, audit, and lock state no longer depend on `.colima` or `profile`
  as the universal program model
- compatibility reads preserve existing durable records
- `colima` becomes an explicit driver on the new target model

Complete when:

- migration fixtures pass
- Colima parity is proven without user-visible regression

## Phase 3: Shared auth and bootstrap

Goal:

- move resolver coverage and provider bootstrap handoffs onto one reviewed
  host-owned auth path

Deliverables:

- broader resolver coverage
- explicit host-owned browser or setup handoffs where provider bootstrap still
  needs them
- shared diagnostics and explainability across launcher and operator tooling

Complete when:

- deterministic auth and bootstrap suites pass without live provider
  dependence

## Phase 4: Trusted validation hosts and host-compatibility matrix

Goal:

- define the validation-host bridge and support matrix before broader host
  claims

Deliverables:

- trusted `linux/amd64` validation-host lane
- support matrix expressed as `host OS x target kind x assurance class`
- backend-aware diagnostics that fail closed on unsupported combinations

Complete when:

- Linux and Windows support claims are limited to what the validation-host
  evidence and docs prove

## Phase 5: Remote VM control-plane contract

Goal:

- define the provider-neutral remote VM contract before any cloud-specific
  backend ships

Deliverables:

- explicit remote workspace materialization
- reviewed brokered-access model
- remote image/bootstrap contract and session/audit lifecycle

Complete when:

- deterministic remote-contract tests pass without real cloud dependence

## Phase 6: Docker Desktop compatibility backend

Goal:

- ship the first cross-platform `compat` target without blurring it into the
  current strict boundary

Deliverables:

- feature-flagged `docker-desktop` target
- explicit `compat` labeling in docs, diagnostics, and session metadata
- host-matrix certification evidence

Complete when:

- the repo keeps `docker-desktop` support clearly lower assurance than the
  current strict Colima path

## Phase 7: AWS remote VM backend

Goal:

- implement the remote VM contract on the first cloud provider

Deliverables:

- `aws-ec2-ssm` target
- audited lifecycle and brokered access
- explicit remote workspace materialization on the reviewed host-owned model

Complete when:

- deterministic adapter and contract suites pass
- live AWS smoke remains certification-only

## Phase 8: GCP remote VM backend

Goal:

- implement the same remote VM contract on a second provider

Deliverables:

- `gcp-vm` target with limited provider-specific delta
- parity on lifecycle, audit, and workspace materialization semantics

Complete when:

- deterministic adapter and contract suites pass
- live GCP smoke remains certification-only

## Phase 9: Later expansion decision gate

Goal:

- decide whether `azure-vm` or managed workstations become funded follow-on
  work

Deliverables:

- recorded decision, rationale, and demand/support evidence
- updated roadmap and program-plan references

Complete when:

- the next funded lane is explicit and the rejected paths are documented as
  deferred rather than implied
