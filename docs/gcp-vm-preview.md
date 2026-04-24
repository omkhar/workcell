# GCP VM Preview

Workcell now exposes a preview-only `remote_vm/gcp-vm/compat` target selection
path. This is intentionally narrower than the reviewed local Colima boundary:

- repo-required support today is deterministic target selection, diagnostics,
  state-root routing, and shared remote-VM conformance reuse
- live GCP execution remains certification-only
- the reviewed live path must use brokered Google Cloud IAP access; inbound
  public SSH is out of bounds for the supported path

Use this page with:

- [docs/remote-vm-contract.md](remote-vm-contract.md)
- [docs/validation-scenarios.md](validation-scenarios.md)
- [policy/host-support-matrix.tsv](../policy/host-support-matrix.tsv)

## Canonical Preview Plan

Inspect the reviewed broker plan without attempting live remote execution:

```bash
workcell \
  --target gcp-vm \
  --target-id workcell-phase8-cert \
  --agent codex \
  --workspace /path/to/repo \
  --dry-run
```

The dry-run path emits:

- `target_kind=remote_vm`
- `target_provider=gcp-vm`
- `target_assurance_class=compat`
- `runtime_api=brokered`
- `workspace_transport=remote-materialization`
- `remote_access_model=brokered`
- `remote_broker=gcp-iap-ssh`
- `inbound_public_ssh=blocked`
- `live_smoke=certification-only`

`workcell --doctor` exposes the same support boundary through
`support_matrix_*` plus the required host tools.

## Host Prerequisites

The preview target expects these host-side tools and dependencies:

- `gcloud` installed in a reviewed host-tool location such as
  `/opt/homebrew/bin`, `/usr/local/bin`, `/opt/homebrew/share/google-cloud-sdk`,
  or `/usr/local/share/google-cloud-sdk`; Workcell does not trust a `gcloud`
  executable under `$HOME`
- a running Compute Engine VM without an external NAT IP
- IAP TCP forwarding enabled for the project and VM
- OS Login or another reviewed SSH identity path for `gcloud compute ssh`
- IAM permissions for `compute.instances.get`, `compute.projects.get`,
  `iap.tunnelInstances.accessViaIAP`, and `oslogin.users.getLoginProfile`

These are host prerequisites. They are not mounted into the reviewed local
runtime boundary.

## Live Certification Gate

Live GCP use is intentionally blocked on the default launch path until the
operator performs the separate certification run on reviewed infrastructure.

The certification lane must prove:

- the selected target is running and reachable through IAP
- no inbound public SSH is required
- remote workspace materialization stays explicit and host-auditable
- the brokered lifecycle and audit story match the shared `remote_vm`
  contract

Run the certification smoke with an already reviewed IAP-reachable Compute
Engine target:

```bash
WORKCELL_GCP_VM_PROJECT=my-project \
WORKCELL_GCP_VM_ZONE=us-central1-a \
WORKCELL_GCP_VM_TARGET_ID=workcell-phase8-cert \
  bash ./tests/scenarios/shared/test-gcp-vm-launch-smoke.sh
```

The smoke fails unless the target is running, has no external NAT IP, can run a
brokered IAP SSH command, and matches the Workcell preview broker plan.

Do not sign a commit that claims supported GCP preview delivery until that live
certification succeeds.

## Rollback

Rollback is explicit:

1. stop using `--target gcp-vm`
2. return to the reviewed `--target colima` path
3. remove any GCP preview target state under
   `~/.local/state/workcell/targets/remote_vm/gcp-vm/`

There is no silent fallback from the GCP preview target onto Colima or Docker
Desktop.
