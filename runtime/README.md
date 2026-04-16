# Runtime Boundary

Tier 1 is a two-layer boundary:

1. a dedicated Colima VM profile on macOS
2. a hardened inner container inside that VM

The VM is the main local isolation boundary. The container provides a reviewed,
reproducible runtime for the supported providers.

## Goals

- keep the safe path one command away
- run the provider inside the boundary, not on the host
- mount only the selected workspace
- keep the container unprivileged by default, with a named nonroot `workcell`
  user in the shipped runtime and validator images
- enforce network posture at the VM layer
- block common control-plane escape hatches on the managed path

The host launcher still starts the managed runtime with explicit `--user 0:0`
only long enough for PID 1 to seed runtime state and then drop privileges.
Repo-mounted validator and release-helper paths instead run under explicit
caller UID/GID mappings with isolated writable home, cache, and tmp roots so
the mounted checkout never depends on ambient container-root defaults. When an
explicit caller UID has no passwd entry inside the image, the launcher
synthesizes an isolated writable home for that lane. The local
`scripts/build-and-test.sh --docker` validator snapshot uses that same
contract.

## Runtime profiles

- `strict`: default provider lane
- `development`: managed interactive development lane
- `build`: explicit build and image-preparation lane
- `breakglass`: explicit higher-trust lane

`strict` expects a prepared runtime image. Interactive repo work and dependency
egress belong to `development` or `build`.

## Main entrypoints

- `scripts/workcell`: host launcher and operator entrypoint
- `scripts/colima-egress-allowlist.sh`: VM-level network posture helper
- `scripts/container-smoke.sh`: direct container smoke coverage
- `scripts/verify-invariants.sh`: invariant checks
- `scripts/build-and-test.sh`: repo-wide validation and local check entry point
- `scripts/pre-merge.sh`: pinned validator-container pre-merge harness with optional disposable snapshots

## GUI status

CLI is the implemented Tier 1 path today. GUI or IDE surfaces are lower
assurance unless they become clients of the same bounded runtime.
