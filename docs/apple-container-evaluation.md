# Apple `container` Evaluation Record

## Status

Workcell completed the C1 evaluation of Apple `container`. Workcell approved
the technical evaluation and deferred operator promotion.

The host-support matrix marks the target `preview-only` and `blocked`.
`--target apple-container` prints a reviewed `--dry-run` plan and a `--doctor`
boundary on Apple Silicon macOS 27 or later. A live launch exits with status 2
(`fail_for_unsupported_launch_target` in `scripts/workcell`). Colima remains
the default target.

## Test Environment

The test host used macOS 26.5.1, Apple Silicon, and Apple `container` 1.0.0.
The guest used Linux kernel 6.18.15.

## Test Methods

Repository tests exercise the local-VM contract and lifecycle. The tests use
the deterministic `AppleContainerTarget` in
[`internal/applecontainer`](../internal/applecontainer).

The live probe calls `RequireMacOS27()` before it invokes the Apple CLI. The
probe measures start time and reads VM isolation properties.

## Observed Results

Three idle-host samples had a median warm start of approximately 857 ms. The
fastest sample was 843 ms. A busy host produced results from two to seven
seconds.

The inspected container had a Linux kernel and hostname that differed from the
host. It had a `192.168.64.x` network address and an ext4 root on a `/dev/vd*`
block device. These observations match the per-container VM model.

The probe observed VM separation from the host. It did not compare two
concurrent containers. This result does not establish a stronger assurance
claim for Workcell or operator support.

## Limits

The evaluation used one Apple Silicon host. The idle-host measurement used
three samples. This result does not create a general performance claim.

The evaluation ran under a macOS 26 guard. The guard in
`internal/applecontainer/guard.go` now requires macOS 27. The launcher runs
the same guard before an `apple-container` dry-run plan and in `--doctor`
(`tests/scenarios/shared/test-apple-container-dry-run.sh`).

The deterministic target writes lifecycle audit records with the launcher
digest chain, so the A5 signer can seal them
(`internal/host/auditseal/applecontainer_test.go`).

## Workspace Materialization Safety

The deterministic target pins a trusted, pre-existing `StateRoot` before it writes target state.
It rejects a source workspace that overlaps `StateRoot`.
Each materialization ID is create-once, and native exclusive publication does not replace an existing materialization.
The target prepares a private `0700` stage under `.materialization-staging`.
After stage creation, a preparation failure leaves the stage in place without pathname cleanup.
An operator can inspect and remove the stage after they understand the failure.

The conformance check reads workspace trees through stable descriptors.
It does not create mirror copies.
Persisted manifests use the same 64 MiB limit as workspace publication.
The manifest reader rejects noncanonical JSON and trailing data.

## Decision

The technical evaluation result was `GO`. Workcell deferred operator
promotion. The target stays `preview-only` and `blocked`.

A promotion change must add the live launch, connect the egress proxy, and
update the exact matrix row. It must add rollback and live certification. It
must satisfy the controls in
[Invariants](invariants.md).

Use
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv) for the
support decision. Use [Runtime Target Phase Record](runtime-target-phase-plan.md)
for the program status.
