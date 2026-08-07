# GCP VM Preview

## Status

Workcell exposes a dry-run broker plan for `remote_vm/gcp-vm/compat`. The
host-support matrix marks this target `preview-only` and `blocked` on macOS
arm64.

Workcell blocks operator launch. Workcell does not implement live remote
launch. Live certification validates GCP IAP broker access and network posture
only. It does not validate a Workcell-managed remote session.

## Dry-Run Command

```bash
workcell \
  --target gcp-vm \
  --target-id workcell-phase8-cert \
  --agent codex \
  --workspace /path/to/repo \
  --dry-run
```

The plan reports `runtime_api=brokered` and `remote_broker=gcp-iap-ssh`. It also
reports `inbound_public_ssh=blocked` and `live_smoke=certification-only`.

## Required Tools and Infrastructure

Install `gcloud` at `/opt/homebrew/bin/gcloud`, `/usr/local/bin/gcloud`, or
`/usr/bin/gcloud`. Workcell also accepts a canonical path under the trusted
prefixes in the [Launcher Contract](launcher-contract.md#launcher-reference).

Workcell must not use a `gcloud` executable under `$HOME`. The host must also
have `jq`. Use `workcell --doctor` to confirm that Workcell can resolve
`gcloud`. The command does not check `jq`.

The target must be a Compute Engine VM without an external NAT IP. The project
and VM must permit IAP TCP forwarding.

The operator must have these permissions:

- `compute.instances.get`
- `compute.projects.get`
- `iap.tunnelInstances.accessViaIAP`
- `oslogin.users.getLoginProfile`

Host `gcloud` must have an SSH identity that can connect through IAP. Workcell
does not stage or approve this identity.

## Certification Scope

The certification script runs outside the Workcell runtime boundary. It uses
host Google Cloud credentials. Workcell does not stage or isolate these
credentials.

The target must run and have no external NAT IP. The direct IAP broker command
must succeed.

The script also checks Workcell diagnostics and the dry-run broker plan. It
does not copy a workspace or start a Workcell remote session.

## Certification Command

```bash
WORKCELL_GCP_VM_PROJECT=my-project \
WORKCELL_GCP_VM_ZONE=us-central1-a \
WORKCELL_GCP_VM_TARGET_ID=workcell-phase8-cert \
  bash ./tests/scenarios/shared/test-gcp-vm-launch-smoke.sh
```

Do not sign a commit that changes the GCP preview claim until live
certification succeeds.

## Rollback

Do not use `--target gcp-vm`. Select a supported target from the host-support
matrix.

Workcell does not provision or remove the cloud VM. If provider cleanup is
necessary, the operator must use Google Cloud tools.

After the operator selects `--target gcp-vm`, Workcell must not select Colima
or Docker Desktop automatically.

## Authoritative Sources

- [Remote VM Contract](remote-vm-contract.md)
- [Host-Support Matrix](../policy/host-support-matrix.tsv)
- [Validation Scenarios](validation-scenarios.md)
