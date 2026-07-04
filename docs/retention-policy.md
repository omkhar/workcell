# CI and Release Artifact Retention Policy

This page records how long each GitHub Actions workflow keeps its uploaded
artifacts and why, and how to verify a release after those artifacts expire.

The machine-enforced source of truth is
[`policy/retention-policy.json`](../policy/retention-policy.json), which binds
each artifact name to its required `retention-days`. The
`workcell-citools check-retention-policy` validator (run by
`scripts/check-workflows.sh` in both local `pre-merge`/`pr-parity` and the
`security.yml` lane) asserts that every `actions/upload-artifact` step sets an
explicit `retention-days` (so no artifact silently inherits the repository
default) and that each uploaded artifact's retention matches the policy exactly.
Binding per artifact name — rather than per workflow — means moving a retention
value from one artifact to another is still caught. The table below mirrors that
policy for readability.

## Retention by artifact

| Workflow | Artifact | retention-days |
|---|---|---|
| ci.yml | workcell-ci-install-candidate | 7 |
| fuzz.yml | fuzz-reproducers | 14 |
| release.yml | workcell-release-preflight | 90 |
| release.yml | workcell-release-install-candidate | 90 |
| release.yml | workcell-release-artifacts | 7 |
| security.yml | zizmor-sarif | 5 |
| scorecard.yml | scorecard-sarif | 5 |
| upstream-refresh.yml | upstream-refresh-candidate | 7 |

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
- **`fuzz.yml` — 14 days.** The `fuzz-reproducers` artifact is uploaded only
  when a scheduled fuzz run finds a crash, and it carries the exact failing
  inputs (`testdata/fuzz/<Target>/<hash>`) needed to reproduce and fix the
  defect. Fourteen days spans the weekly cadence with margin, so a crash from
  one run is still retrievable for triage after the next run; the durable copy
  is the reproducer once committed as a regression seed (see
  [fuzzing.md](fuzzing.md)).
- **`security.yml` — 5 days.** The `zizmor` audit job itself is the enforcement
  gate: it fails the workflow on any finding. The `zizmor-sarif` upload is a
  short-lived supplementary export of that run and is **not** ingested into
  GitHub code scanning, so there is no durable copy after it expires — 5 days is
  enough to inspect a specific run's SARIF; the durable signal is the pass/fail
  gate and the workflow logs.
- **`scorecard.yml` — 5 days.** The Scorecard SARIF is uploaded to GitHub code
  scanning (`github/codeql-action/upload-sarif`), which is the authoritative,
  durable copy; the 5-day `scorecard-sarif` artifact is only a short-lived
  convenience copy of the same data.
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
