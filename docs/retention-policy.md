# CI and Release Artifact Retention Policy

This page records how long each GitHub Actions workflow keeps its uploaded
artifacts and why, and how to verify a release after those artifacts expire.
The retention values below are enforced against the workflows by
`scripts/check-retention-policy.sh`, which also requires every
`actions/upload-artifact` step to set an explicit `retention-days` (so no
artifact silently inherits the repository default), so the documented policy
and the workflow configuration cannot drift apart.

## Retention by workflow

<!-- retention-policy:begin -->
| Workflow | retention-days |
|---|---|
| ci.yml | 7 |
| release.yml | 7, 90 |
| security.yml | 5 |
| scorecard.yml | 5 |
| upstream-refresh.yml | 7 |
<!-- retention-policy:end -->

## Rationale

- **`release.yml` — 90 and 7 days.** The release-preflight and
  release-install-candidate artifacts are the buyer- and incident-facing
  evidence for a tagged release (build inputs, install candidates, manifests);
  they are kept **90 days** to give incident response and post-release audits a
  usable window. The `workcell-release-artifacts` upload is kept at **7 days**
  because every file in it is also published as a permanent GitHub Release
  asset in the next step, so a long-lived workflow-artifact copy would be pure
  redundant storage.
- **`ci.yml` — 7 days.** The CI install candidate is a transient
  per-PR/per-push build; it is only useful while the change is in flight, and
  the durable release evidence is produced by `release.yml`.
- **`security.yml` — 5 days** and **`scorecard.yml` — 5 days.** These upload
  SARIF backups. The authoritative copies are ingested into GitHub code
  scanning and the Scorecard dashboard; the artifact is a short-lived
  convenience copy, not the system of record.
- **`upstream-refresh.yml` — 7 days.** The `upstream-refresh-candidate` bundle
  (`patch`, `diffstat`, `metadata.json`) is an advisory operator signal for a
  reviewed upstream-pin refresh, not integrity evidence; the authoritative
  refresh PR is created later on the host. Seven days covers the review window
  for a candidate before it is regenerated.

## Verifying a release after artifacts expire

Uploaded workflow artifacts are not the durable provenance record. After they
expire, a release can still be verified from permanent sources:

- **GitHub attestations.** When the reviewed hosted controls enable them,
  release artifacts carry GitHub-native build provenance attestations. Verify a
  downloaded artifact with `gh attestation verify <file> --repo omkhar/workcell`
  (or the equivalent API), independent of whether the workflow artifact still
  exists.
- **Sigstore / Rekor transparency log.** The image, source bundle, Homebrew
  formula asset, published image digest, checksums, build-input manifest,
  control-plane manifest, builder-environment manifest, and both SBOMs are
  signed with keyless Sigstore/Cosign. Those signatures are recorded in the
  public Rekor transparency log, which is permanent — `cosign verify` and Rekor
  lookups remain available long after any workflow artifact has aged out.

See [provenance.md](provenance.md) and [github-workflows.md](github-workflows.md)
for the full signing and attestation surface.
