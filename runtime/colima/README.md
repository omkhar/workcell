# Colima Design

## Why Colima

On this machine, Colima backed by Apple Virtualization.Framework is the
strongest practical deployable boundary available today. There is no verified
Kata-class runtime in the active stack, so the VM is the hard wall.

## Boundary rules

- use a dedicated Colima profile per bounded workspace
- do not reuse the shared default developer profile when stronger isolation is
  required
- disable Colima's default home-directory mount by supplying an explicit mount
  list
- keep the task workspace as the only writable host mount
- enforce network policy at the VM layer instead of granting container network
  capabilities

## Operational stance

The wrapper derives a unique Colima profile name from the full target workspace
path by default, starts the VM with `--mount <workspace>:w`, and validates the
saved Lima config after startup. The secure path expects exactly one writable
host mount: the selected workspace. Operators can override the name, but the
secure path should not depend on the shared `default` profile.

## What stays out

- host home directories
- host auth and agent sockets
- keychain or browser-profile passthrough
- host Docker control sockets
- broad source-tree mounts unrelated to the task workspace
