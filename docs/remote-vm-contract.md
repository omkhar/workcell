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
8. The host records the guest path in `materialization.json` and removes the
   `guest_transfer` field.

Before step 3, the host writes `archive_sha256` and
`"guest_transfer": "unconfirmed"` to `materialization.json`. The host syncs
that file and each directory that it creates for it. The host creates each
state directory with mode 0700, and it sets that mode on the materialization
directory.

An error at any step stops the transfer. The receiver removes a partial
workspace. A transfer stops after 30 minutes. The broker then gets SIGTERM,
and SIGKILL follows after 5 seconds.

The host does not remove `materialization.json` after an error. An error
before step 3 keeps the record of an earlier transfer with the same ID. An
error after that point keeps the unconfirmed record. The guest can then have
this workspace, an earlier workspace with the same ID, or no workspace. To
reconcile, send the same materialization ID again. An acknowledged transfer
replaces the guest copy and the record.

The error includes the last 1 KiB of the broker standard error and a nonzero
broker exit status. `gcloud` can exit 0 after a failed IAP connection, so the
standard error can be the only cause.

The other `ConformanceTarget` methods return an error. Remote bootstrap and
session control are later roadmap items. Workcell does not install the guest
receiver, and it does not provision cloud resources.

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
