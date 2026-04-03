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
- keep the container unprivileged
- enforce network posture at the VM layer
- block common control-plane escape hatches on the managed path

## Runtime profiles

- `strict`: default developer lane
- `build`: explicit build and image-preparation lane
- `breakglass`: explicit higher-trust lane

`strict` expects a prepared runtime image. Image creation and rebuild belong to
`build`.

## Main entrypoints

- `scripts/workcell`: host launcher and operator entrypoint
- `scripts/colima-egress-allowlist.sh`: VM-level network posture helper
- `scripts/container-smoke.sh`: direct container smoke coverage
- `scripts/verify-invariants.sh`: invariant checks
- `./build_and_test.sh`: repo-wide validation and local check entry point

## GUI status

CLI is the implemented Tier 1 path today. GUI or IDE surfaces are lower
assurance unless they become clients of the same bounded runtime.
