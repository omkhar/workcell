# Apple `container` backend evaluation (C1)

This page records the roadmap **C1** evaluation of Apple's `container` runtime as
a `local_vm/apple-container` target for Workcell, and the go/no-go promotion
decision. Per the roadmap, the 1.0 gate is the *recorded decision*, not a shipped
backend: Colima stays the reviewed default, and Apple `container` is
support-invisible (preview-only, launch-blocked) until the full support-matrix
launch and certification gates pass.

The spike lives in [`internal/applecontainer`](../internal/applecontainer); the
support-matrix row is in [`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv).

## Context

Apple's Containerization framework reached 1.0.0 (June 2026): one lightweight VM
per container on Virtualization.framework, a frozen 1.0 API, Apple Silicon only,
macOS 26 baseline. The hypothesis was that per-session VM isolation would be a
stronger and lighter boundary than one shared Colima VM.

## Methodology

Two independent validation modes, mirroring the existing `remotevm` target
pattern:

1. **Deterministic conformance harness.** A local-VM `Contract` (its own type —
   Apple `container` is local and direct, so it does not reuse `remotevm`'s
   remote/brokered contract) plus a deterministic `AppleContainerTarget` driven
   through the session lifecycle (`workspace_materialized` → `bootstrap_ready` →
   `session_started` → `session_finished`). Runs on any host in CI.
2. **Live local exercise.** A `ProbeAppleContainer` helper that shells out to the
   real `container` CLI on a macOS 26 host, measures warm boot latency, and reads
   per-VM isolation properties. Skips (sentinel `ErrAppleContainerUnavailable`)
   when the CLI is absent or the service is stopped, so non-macOS-26/CI hosts are
   unaffected.

Evaluation host: macOS 26.5.1 (Darwin 25.5.0), Apple Silicon, Apple `container`
1.0.0, kata guest kernel 6.18.15.

## Results

### Conformance

`AppleContainerTarget` passes the local-VM session-lifecycle contract
(materialize → bootstrap → start → finish, all four required audit events, and
the `running` → `exited` status transition). The contract's `Validate()`
fail-closes on remote/brokered values, so a local target cannot be
mis-declared as remote.

### Live boundary properties (macOS 26 host)

| Property | Apple `container` (observed) |
|---|---|
| Warm boot latency | **sub-second** — 843 ms fastest, ~857 ms median (idle host, 3 samples) |
| Guest kernel | own Linux kernel **6.18.15** (host is Darwin 25.5.0) |
| Hostname / init | unique per-run UUID hostname; own pid 1 |
| Network | own VM NIC `eth0` on `192.168.64.0/24` (not a shared bridge) |
| Root filesystem | own ext4 block device (`/dev/vd*`) |

Every isolation property confirms the model: **one lightweight VM per
container**, each with its own kernel, network stack, and block device.

### Boundary comparison versus Colima

| | Apple `container` | Colima (current default) |
|---|---|---|
| VM model | **one VM per container/session** | **one shared VM** for all sessions |
| Kernel isolation | per-session kernel | all sessions share one kernel |
| Cold start | sub-second per container | tens of seconds for the shared VM's first boot |
| Host support | Apple Silicon + macOS 26 only | macOS (reviewed), Linux amd64 (validator) |
| API maturity | frozen 1.0 (new) | mature |

Apple `container` provides a strictly stronger session boundary (per-session VM
vs shared VM) at a lighter cost (sub-second boot), validating the hypothesis.

## Caveats

- **Apple Silicon + macOS 26 only.** Nothing below macOS 26; `RequireMacOS26()`
  fail-closes on lower versions, non-macOS, and non-Apple-Silicon (Intel/amd64).
- **1.0 API is new** (June 2026) — less field-hardened than Colima.
- **First-run setup** requires a kata guest kernel
  (`container system kernel set --recommended`) and a running system service.
- **Boot latency is CPU-contention-sensitive**: sub-second on an idle host, but
  2–7 s under a saturated host. The always-run probe test therefore asserts
  isolation plus a robust lightweight-VM ceiling (an order of magnitude under
  Colima cold boot) and logs the real median; the literal sub-second assertion
  is an opt-in serial test (`WORKCELL_APPLECONTAINER_STRICT_BOOT=1`).

## Fail-closed behavior

`RequireMacOS26()` (`internal/applecontainer/guard.go`) refuses the
`apple-container` target on macOS below 26 and on non-macOS, so the backend
cannot be selected where its per-session-VM guarantees do not hold.

## Decision: go (preview-only), Colima stays the default for 1.0

**Recorded go/no-go: GO on the evaluation; promotion deferred.** Apple
`container` is validated as a superior local boundary on macOS 26, and is
recorded in the support matrix as `local_vm/apple-container`,
`per-session-vm`, **`preview-only` / `blocked`** — support-invisible, evaluation
only. Colima remains the reviewed default local backend (including on macOS below
26, where Apple `container` is unavailable).

Promotion to a `supported` / `launch-allowed` backend is **deferred beyond
1.0**, gated on: a real-boundary certification lane on an Apple-Silicon macOS 26
runner (roadmap B6), the full host-support-matrix launch gates, and broader
field validation of the 1.0 API. The 1.0 deliverable is this recorded evaluation
and decision, plus the conformance spike and fail-closed guard — not a shipped
backend.
