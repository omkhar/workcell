# Remote VM Contract

## Authority and Status

[`policy/remote-vm-contract.json`](../policy/remote-vm-contract.json) defines
the `remote_vm` contract. Each remote VM provider must use this contract.

The contract does not create operator support. The host-support matrix gives
the support and launch status.

## Fixed Contract Fields

| Field | Required value |
|---|---|
| `target_kind` | `remote_vm` |
| `target_assurance_class` | `compat` |
| `support_boundary` | `preview-only` |
| `runtime_api` | `brokered` |
| `workspace_transport` | `remote-materialization` |
| `access_model` | `brokered` |

The policy artifact uses `target_provider=fake-remote`. A provider-specific
contract changes only this provider value. A separate access plan holds the
provider broker data.

## Deterministic Contract Evidence

The repository uses `remotevm.FakeTarget` for deterministic contract tests. The
AWS and GCP conformance fixtures also use this fake target.

The fake target copies the source workspace into its local target root. It
excludes `.git` and records each copied entry in `materialization.json`.

The fake target rejects destination names that collide after NFC normalization and Unicode case folding.
The copy stops with an error at the first limit that it exceeds:

- One file is larger than 256 MiB.
- All files together are larger than 2 GiB.
- The walk finds more than 100,000 entries.

The Apple container workspace copy uses the same limits.
It writes no content at a destination path it rejects for invalid UTF-8 data.

This process is contract evidence only. It does not copy data to AWS or GCP.
It does not start a Workcell remote session.

## Workspace Transfer Requirements

A live provider must copy the source workspace into its provider target root.
It must exclude `.git` and record each copied entry in
`materialization.json`.

Workcell must not use a live host mount for a remote workspace. The transfer
must be explicit and auditable.

## Brokered Workspace Transfer

`remotevm.BrokeredTarget` in `internal/remotevm/brokered_target.go`
implements the transfer as library code. No launcher path calls it. The
`policy/host-support-matrix.tsv` matrix still blocks launch of each remote
target.

The transfer has these steps:

1. The target copies the workspace into the host state root with the fake
   target copy rules and limits.
2. The target writes a tar of that copy in manifest order. Each header has
   owner 0, group 0, and modification time 0, so one workspace gives the same
   bytes on each run. Each file must still match its manifest SHA-256.
3. The target runs the provider broker CLI without a shell. The broker connects
   its standard input and output to the guest receiver port:
   - GCP: `gcloud compute start-iap-tunnel <vm> <port> --listen-on-stdin`.
   - AWS: `aws ssm start-session` with the `AWS-StartSSHSession` document and
     `portNumber=<port>`.
4. The target sends one header line with the materialization ID, the archive
   SHA-256, and the archive size. Then it sends the archive.
5. The guest receiver reads the archive into a spool file. It verifies the size
   and the SHA-256 before it unpacks any entry.
6. The receiver applies the copy rules again. It accepts only safe relative
   names, directories, regular files, and symlinks that stay in the workspace.
   `internal/remotevm/workspace_archive.go` rejects `.git`, name collisions,
   and an entry whose parent is not a directory from the same archive. It
   applies the same size and entry limits.
7. The receiver acknowledges the SHA-256 and the guest workspace path. The host
   accepts only an acknowledgment with the same SHA-256.
8. The host records the guest path and `archive_sha256` in
   `materialization.json`.

An error at any step stops the transfer. The receiver removes a partial
workspace. The host removes `materialization.json`. A transfer stops after 30
minutes. The broker then gets SIGTERM, and SIGKILL follows after 5 seconds.

The error includes the last 1 KiB of the broker standard error and a nonzero
broker exit status. `gcloud` can exit 0 after a failed IAP connection, so the
standard error can be the only cause.

## Brokered Bootstrap

`BrokeredTarget.BootstrapTarget` prepares the guest through the same broker
and receiver port. No launcher path calls it.

The bootstrap has these steps:

1. The caller renders the guest floor with the `guest-script` command of
   `colima-egress-allowlist.sh`, the one owner of the egress rules. This
   command prints the exact shell input that the Colima VM runs for the same
   endpoints, so the guest gets the same `DOCKER-USER` rules. The Go code does
   not build rules.
2. The target accepts only an image reference that is pinned by digest, as
   `internal/imageref` defines it. The release asset `workcell-image.digest`
   holds this reference for a release image.
3. The target writes `bootstrap.json` in the host state root. Then it sends one
   header line with the image reference, the floor SHA-256, and the floor size.
   Then it sends the floor script.
4. The guest receiver verifies the size and the SHA-256 before it runs a
   command. It runs each tool without a shell, with the same timeout, SIGTERM,
   and SIGKILL rules as the broker:
   - `bash -s --` reads the floor script. The floor applies first, so it is in
     force before Workcell pulls or starts an image.
   - `docker pull --quiet <image>` pulls the image by digest.
   - `docker image inspect` must show that digest in `RepoDigests`.
5. The receiver acknowledges the image reference and the floor SHA-256. The
   host accepts only an acknowledgment with the same values. Then it records
   `egress_floor_sha256` in `bootstrap.json`.

An error at any step stops the bootstrap. An error after the host writes
`bootstrap.json` removes it. The error includes the step, the tool exit status, and the last 1 KiB of the tool
standard error.

These items are operator preconditions. Workcell does not verify them yet:

- The VM has no external IP address.
- The VPC has no NAT, except the proxy path. It has no inbound rule, except the
  IAP or SSM path to the receiver port.
- The guest receiver runs as a user that can run Docker and `sudo` without a
  password.
- The guest has `getent` from glibc. The floor script resolves hostname
  endpoints with `getent`. Without it, a hostname endpoint stops the floor
  with an error. Container-Optimized OS does not have `getent`.

Workcell does not install the guest receiver, and it does not provision cloud
resources.

## Brokered Session Control

`BrokeredTarget.StartSession` and `BrokeredTarget.FinishSession` control the
session container through the same broker and receiver port. No launcher path
calls them.

The caller gives `StartSession` the docker run argv for the guest. The
launcher must render this argv, because `scripts/workcell` is the one owner of
the hardened run flags. That renderer does not exist yet. Without an argv,
`StartSession` returns an error that names the gap, and the Go code does not
copy the run flags.

Session start has these steps:

1. `internal/remotevm/brokered_target.go` refuses the request before the
   broker starts if one of these conditions is true:
   - The bootstrap result records no `egress_floor_sha256`.
   - The argv does not start with `run -d`.
   - The argv does not run the image that the bootstrap pulled by digest.
   - The argv has no valid `--name` before the image.
   - The session already has a record.
2. The host sends one header line with the session ID, the image reference,
   the argv size, and the argv SHA-256. Then it sends the argv words, each
   with a NUL terminator.
3. The guest receiver in `internal/remotevm/guest_session.go` verifies the
   size and the SHA-256 and checks the argv again. Then it runs `docker` with
   the argv and no shell. If `docker run` fails, the receiver removes a
   container that the run created.
4. The receiver acknowledges the session ID and the container name. If the
   host gets no matching acknowledgment, it asks the guest to remove the
   container, because a lost acknowledgment can hide a running container.
5. The host writes the session record with `status=running` and the container
   name. Then it appends the `workspace_materialized`, `bootstrap_ready`, and
   `session_started` records to the target audit log. If one of these writes
   fails, `internal/remotevm/brokered_target.go` removes the container and the
   record.

Session finish has these steps:

1. A session whose record has `status=exited` returns that record. The host
   does not start the broker.
2. The guest receiver runs `docker container stop --time 10`, then
   `docker container rm --force`. The finish succeeds only when
   `docker container ls --all` does not list the container. A container that
   is already gone gives the result `absent`, so a repeated teardown succeeds.
3. After the acknowledgment, the host writes the final record and appends the
   `session_finished` record. A failure before this step leaves the record
   with `status=running`, and the caller can try again.

The host appends each audit record with `hoststate.AppendAuditRecord`. It
writes the line format and the `record_digest` chain of the launcher audit
writer, under the same lock. Thus `internal/host/auditseal` can sign and
verify the remote session chain. The exit status comes from the caller. The
guest does not report it yet.

The egress proxy sidecar is not started on the guest. The launcher uses the
sidecar only for Colima in strict mode.

## Session and Audit Requirements

The contract requires `bootstrap.json`, session records, and a target audit
log. It requires these audit events:

- `workspace_materialized`
- `bootstrap_ready`
- `session_started`
- `session_finished`

The fake target tests `status=running`, `status=exited`, and
`assurance=compat-preview-brokered`. These are deterministic contract results,
not live provider results.

## Provider Conformance Requirements

Each provider must implement `remotevm.ConformanceTarget`. Each provider must
pass `remotevm.RunConformance`.

A provider must not define a separate contract suite. A provider must limit
additions to its provider-specific broker and bootstrap data.

AWS and GCP do not implement live Workcell remote execution. Their preview
documents define tools, broker tests, certification scope, and rollback:

- [AWS EC2 SSM Preview](aws-ec2-ssm-preview.md)
- [GCP VM Preview](gcp-vm-preview.md)
