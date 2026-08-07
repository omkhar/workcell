# Apple `container` Evaluation Record

## Status

Workcell completed the C1 evaluation of Apple `container`. Workcell approved
the technical evaluation and deferred operator promotion.

The host-support matrix marks the target `preview-only` and `blocked`. The
Workcell CLI does not expose `apple-container` as a target. Colima remains the
default target.

## Test Environment

The test host used macOS 26.5.1, Apple Silicon, and Apple `container` 1.0.0.
The guest used Linux kernel 6.18.15.

## Test Methods

Repository tests exercise the local-VM contract and lifecycle. The tests use
the deterministic `AppleContainerTarget` in
[`internal/applecontainer`](../internal/applecontainer).

The live probe calls `RequireMacOS26()` before it invokes the Apple CLI. The
probe measures start time and reads VM isolation properties.

## Observed Results

Three idle-host samples had a median warm start of approximately 857 ms. The
fastest sample was 843 ms. On a busy host, the start time was two to seven
seconds.

The inspected container had a Linux kernel and hostname that differed from the
host. It had a `192.168.64.x` network address and an ext4 root on a `/dev/vd*`
block device. These observations match the per-container VM model.

The probe observed VM separation from the host. It did not compare two
containers that ran at the same time. This result does not establish a stronger
assurance claim for Workcell or operator support.

## Limits

The evaluation used one Apple Silicon host. The idle-host measurement used
three samples. This result does not create a general performance claim.

The evaluation probe uses the macOS 26 guard. The Workcell launcher does not
use this guard because it has no `apple-container` operator target.

The deterministic target writes lifecycle audit records without a signed
digest chain. Session verification fails closed for this target.

## Decision

The technical evaluation result was `GO`. Workcell deferred operator
promotion. The target stays `preview-only` and `blocked`.

A promotion change must add CLI selection and update the exact matrix row. It
must add diagnostics, rollback, tests, and live certification. The change must
also address audit signatures. It must satisfy the controls in
[Invariants](invariants.md).

Use
[`policy/host-support-matrix.tsv`](../policy/host-support-matrix.tsv) for the
support decision. Use [Runtime Target Phase Record](runtime-target-phase-plan.md)
for the program status.
