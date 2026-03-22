# Runtime Boundary

Tier 1 uses two layers:

1. a dedicated Colima VM profile on macOS
2. a hardened Docker container inside that VM

The VM is the strongest practical local boundary available on this host. The
container keeps the environment reproducible and usable across multiple agent
providers.

## Design goals

- keep the safe path one command away
- keep the selected agent inside the boundary, not on the host
- keep the VM mount set to the selected workspace only
- keep the container unprivileged
- push network enforcement to the VM layer

## Profiles

- `strict`: allowlisted service access only
- `build`: broader registry allowlist for dependency installation and builds
- `breakglass`: unrestricted network plus the provider's highest-trust mode
  where one exists; Codex maps this to `danger-full-access`

## GUI status

CLI is the only implemented Tier 1 path today. GUI support is not claimed until
the GUI is wired as a client to the same bounded runtime.

## Main entrypoints

- `scripts/workcell`: start the VM, build the runtime image, apply the selected
  network mode, and launch the selected agent inside the container
- `scripts/colima-egress-allowlist.sh`: apply or clear VM-level egress rules
- `scripts/verify-invariants.sh`: run basic regression checks against the
  current runtime assumptions
