# B6 Apple Silicon Boundary Lane Decision

## Decision

On 2026-07-09, the maintainer deferred the automated boundary lane for Apple
Silicon until after 1.0.

The project did not fund or operate a self-hosted runner. The only available
Apple Silicon system was the maintainer workstation.

The maintainer changed release criterion 6 for 1.0. It now requires local
operator certification of both supported launch targets:

- `macos/arm64/local_vm/colima/strict`
- `macos/arm64/local_compat/docker-desktop/compat`

The project completed those certifications before 1.0. The current release
keeps the same boundary evidence model.

## What the decision did not change

Both targets remain supported and launch-allowed on macOS in
`policy/host-support-matrix.tsv`.

The Apple `container` target is a different target. It remains preview-only and
has no operator launch path. Its evaluation does not certify Colima or Docker
Desktop.

## Hosted evidence limit

GitHub-hosted CI does not launch either supported runtime boundary on macOS.

| Hosted lane | Evidence | Limit |
|---|---|---|
| `container-smoke` on `ubuntu-latest` | Container controls and deterministic invariants | Not a macOS VM boundary |
| `trusted-linux-amd64-validator` | Repository, policy, and scenario validation | Validation-host-only target |
| `install-verification` on `macos-15` and `macos-26` | Install and uninstall behavior | No Colima or Docker Desktop VM launch |

Thus, local operator certification supplies the live boundary evidence. Hosted
CI supplies deterministic and install evidence.

## Options that the maintainer considered

### Option A: self-hosted Apple Silicon runner

If the project selects this option, the lane will run both supported macOS
targets on a schedule.

The lane will improve regression detection. It will also add runner
maintenance, availability, and security work.

The runner will be a lower-trust system. It must run one job. The workflow must
then replace it. The workflow must use trusted scheduled triggers and
`permissions: {}`. It must have no OIDC authority or environment secrets. It
must not hold release signing authority. A short-lived credential for scoped
runner registration is the only permitted repository credential.

### Option B: local certification

This option keeps live certification on an operator-controlled Apple Silicon
system. It does not add the attack surface of a self-hosted runner.

It has lower assurance than a continuous hosted lane. A boundary regression can
remain undetected until the next local certification.

## Recorded result

The maintainer selected Option B. The 1.0 readiness review records the criterion
change and the completed local certifications.

B6 remains a post-1.0 improvement. It is not a condition of the current support
claim.

## Future B6 exit gate

A future automated lane must meet these conditions:

1. It runs on a physical macOS host with Apple Silicon.
2. It launches both supported macOS targets.
3. It uses an isolated and replaceable runner.
4. It runs one job before replacement.
5. It uses trusted scheduled triggers and `permissions: {}`.
6. It has no OIDC authority or environment secrets.
7. It has no release signing authority.
8. It has no repository credential except scoped runner registration.
9. It records the exact commit, control tree, host, and result.
10. It updates the CI threat model and operator documentation.

See the [1.0 readiness review](1.0-readiness-review.md) for the release record.
See the [Roadmap](../ROADMAP.md) for the current B6 status.
