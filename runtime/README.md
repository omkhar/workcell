# Runtime Boundary

The strict local target uses two isolation layers:

1. A dedicated Colima VM profile on macOS.
2. A hardened runtime container in that VM.

The VM is the main local boundary. The container supplies a reproducible
provider runtime.

Docker Desktop is a supported compatibility target on Apple Silicon macOS. It
does not provide a dedicated Workcell VM or Workcell egress enforcement.

## Runtime goals

- Keep the safe path available through one command.
- Run the provider inside the runtime boundary.
- Mount only the selected workspace and the approved persistent-cache paths as
  durable host data.
- Use only narrow, approved credential handoffs.
- Run provider processes with the mapped host UID and GID.
- Block common control-plane escape paths.

Mutable sessions start PID 1 as root to prepare runtime state. PID 1 then changes
to the mapped UID and GID before it starts the provider. Read-only sessions start
PID 1 with the mapped UID and GID.

Validator and release-helper containers use the caller UID and GID. They use
separate writable home, cache, and temporary directories.

## Runtime profiles

| Profile | Use |
|---|---|
| `strict` | Default provider session from the prepared image |
| `development` | Managed interactive development |
| `build` | Image preparation and build work |
| `breakglass` | Explicit higher-trust work with lower assurance |

Strict mode prepares a missing or stale image. Development and build profiles
permit the dependency access that their documented plans require.

## Main entry points

- `scripts/workcell` provides the operator CLI and host launcher.
- `scripts/colima-egress-allowlist.sh` manages profile-wide Colima rules.
- `scripts/container-smoke.sh` runs direct container checks.
- `scripts/verify-invariants.sh` verifies security invariants.
- `scripts/build-and-test.sh` runs repository validation.
- `scripts/pre-merge.sh` runs the pinned pre-merge validator.

## Interface scope

The supported Tier 1 interface is the CLI. GUI and IDE paths have lower
assurance unless they use the same runtime boundary.
