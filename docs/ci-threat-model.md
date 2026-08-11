# CI/CD threat model

This document describes threats to the Workcell GitHub Actions pipeline.
It covers builds, releases, credentials, and GitHub-hosted controls.

The runtime [threat model](threat-model.md) covers the local Workcell boundary.
The [provenance contract](provenance.md) covers consumer verification.
This document does not replace those documents.

The controls below come from the workflows, policy files, and validation scripts.
This document also identifies known gaps.

## Scope and assets

The pipeline protects these assets:

- the multi-platform runtime image in GHCR
- nine release-data assets and their nine Sigstore bundles
- GitHub attestations for eight build-provenance subjects
- the release workflow identity and the maintainer signing identity
- pinned actions, tools, images, and provider inputs
- branch, tag, environment, and immutable-release controls

The nine release-data assets are:

- the source bundle
- the Homebrew formula
- the image-digest file
- the build-input manifest
- the control-plane manifest
- the builder-environment manifest
- the source SBOM
- the image SBOM
- `SHA256SUMS`

The release also signs the runtime image in GHCR.

This document does not cover these subjects:

- local runtime isolation
- product-code correctness
- physical access to the maintainer host
- a complete compromise of GitHub, Fulcio, or Rekor

The pipeline trusts GitHub-hosted Actions, GitHub OIDC, GHCR, Fulcio, and Rekor.
A compromise of these services is a residual risk.

## Trust boundaries and runner tiers

### Runner tiers

All workflow jobs use GitHub-hosted runners.
The workflow files use these runner labels:

- `ubuntu-latest`
- `ubuntu-24.04-arm`
- `macos-15`
- `macos-26`

The repository does not use a self-hosted runner.
GitHub gives standard hosted jobs a new virtual machine.

Workcell pins the CI build matrix, macOS matrices, and arm64 release label.
The checks do not reject every possible `self-hosted` string.
Reviewers check all other `runs-on` values.

### Workflow authority

The repository has 14 workflows.
Each workflow starts with `permissions: {}`.
Jobs that need authority declare job permissions.
Other jobs inherit the empty workflow-level permission set.

Each checkout uses `persist-credentials: false`.
Each action uses a full commit SHA.
The action owner and repository must be in `policy/allowed-actions.toml`.

The release workflow has the main publication authority.
Its release job can write packages, artifact metadata, and attestations.
It can also request an OIDC token.
Its final publisher has `contents: write` for the repository.
The current publisher script uses this scope to create a release and upload assets.

The native arm64 release job can push an image by digest to GHCR.
Release scan jobs can upload SARIF data.

Other workflows also have write authority:

- CodeQL and security jobs can upload SARIF data.
- Scorecard can upload SARIF data and request an OIDC token.
- Upstream refresh has repository-wide `issues: write` authority.
- Upstream refresh also has `pull-requests: read` authority.

The refresh script can create its label and tracking issue.
It can also reopen or edit that issue.
It can also upload a review-only candidate artifact.
The job has no content-write or release-publication scope.

The release environment protects artifact construction and image publication.
The environment requires maintainer approval and does not permit administrator bypass.

The final publisher uses the `hosted-controls-audit` environment.
It refreshes the hosted-control proof before publication.

The release job uploads a current-run Actions artifact.
The final publisher downloads that current-run artifact and publishes its files.
The publisher trusts this handoff.
It does not verify the new signatures or attestations after the handoff.

The release publisher rejects extended ACLs on source and staging file handles.
The required Darwin lane checks the native macOS ACL interface on every PR and `main` push.
The Linux validator fixture checks POSIX ACL handling before a release can publish.
The release install matrix repeats the Darwin proof before it uses release artifacts.

### Untrusted pull requests

The PR workflows use the `pull_request` event.
GitHub reduces a fork token to read-only authority.
Fork code also gets no environment secret.

Trusted CodeQL and security jobs can request `security-events: write`.
This authority lets those jobs upload SARIF data.

The `approved-heavy-ci` label controls expensive PR jobs.
It controls CodeQL, mutation, platform reproducibility, and macOS installation checks.

CodeQL, platform reproducibility, and macOS installation checks run on `main` pushes.
Release preflight runs the platform and installation checks again.
Mutation runs on approved PRs, schedules, and manual requests.
The release-preflight validation profile also runs the mutation gate.
A normal `main` push does not run the Mutation workflow.

The PR base policy uses `pull_request_target`.
It has no permissions, checkout, external action, or untrusted code execution.
It reads only the base branch and draft state.

### Cache boundary

CI and Mutation non-PR runs use `validator-main`.
Docs non-PR runs use `validator-docs-main`.
Each PR run uses its PR-specific scope.
PR runs can read `validator-main` but write only to their PR scope.
Scheduled and manual runs can write a minimal non-PR cache.

## Credentials and rotation

### Stored credential

The workflow files use one long-lived stored credential:

| Credential | Location | Purpose | Limit |
| --- | --- | --- | --- |
| `WORKCELL_HOSTED_CONTROLS_TOKEN` | `hosted-controls-audit` environment | Read repository administration metadata for hosted-control audits | The repository cannot inspect the token scopes. Read-only authority is a provisioning rule. |

This token does not trigger or publish a release.
The current script removes it before the publication call.

One final-publisher step receives both tokens.
Checked-out audit code uses the stored token before the step removes it.
A compromised audit script can use both tokens during this period.

Give this token read-only access.
Limit its access to repository administration metadata.
Rotate it every 90 days and after a possible exposure.

Use this rotation procedure:

1. Create a replacement token with the required read-only scopes.
2. Set an expiry of 90 days or less.
3. Update the `hosted-controls-audit` environment secret.
4. Run the Hosted controls workflow.
5. Revoke the old token after the workflow passes.
6. Record the rotation date.

Do not store a short-lived GitHub App installation token as this secret.
Mint an installation token during the workflow when you use an App.

### Ephemeral credentials

GitHub creates `GITHUB_TOKEN` for each job.
The token expires after the job.

CI, release, pin-hygiene, and upstream-refresh steps can use a temporary token file.
These steps use mode `0600`, remove environment copies, and delete the file on exit.

GitHub creates short-lived OIDC tokens for signing and attestations.
Workcell does not store a Cosign private key.

The maintainer signing key stays on the maintainer host.
Host publication checks commit signatures.
Release CI checks tag signatures.
Neither path creates a maintainer signature.

## Attestation and signing

### What is produced

Each successful tagged release publishes one signed multi-platform image.
It also uploads nine release-data assets and nine Sigstore bundles.

Cosign uses GitHub OIDC, a short-lived Fulcio certificate, and Rekor data.
The workflow does not use a `--key` option.

The canonical repository creates build-provenance attestations for eight subjects:

- the runtime image
- the source bundle
- the Homebrew formula
- the image-digest file
- the build-input manifest
- the control-plane manifest
- the builder-environment manifest
- `SHA256SUMS`

GitHub also attaches SBOM predicates to the image and source-bundle subjects.
The two SBOM files are not attestation subjects.
Cosign signs both SBOM files as release assets.

The amd64 release job rebuilds from the archived source bundle.
The native arm64 job builds from the checked-out signed tag.
The workflow combines both platform digests into one image index.

### Consumer verification

`scripts/install-release.sh` verifies a tagged release before extraction.
It verifies the Cosign signature for `SHA256SUMS`.
It then compares the bundle digest with the signed checksum.

The `--attestation` option also runs GitHub attestation verification.
The installer does not use the SBOM files as an installation gate.

An operator can select `--skip-verify` for an unverified installation.
This option also requires `--i-understand-unverified-install`.

The verified installer is the documented release-install path.
However, a user can select a local path that does not verify the release.
The installer also comes from a repository clone, not a signed standalone asset.

### Pipeline verification gap

The release workflow verifies inputs, the release-tag signature, and reproducibility.
It does not verify the new release signatures after it creates them.

The workflow does not run `cosign verify` on the new image or bundles.
It also does not run `gh attestation verify` on the new attestations.
This output-verification gap remains open.

### SLSA posture

Workcell claims SLSA v1.0 Build L2 for the eight build-provenance subjects.
It does not claim Build L2 for the two SBOM files or nine Sigstore bundles.
It does not claim Build L3.

GitHub-hosted jobs create authentic platform provenance.
Build and attestation steps still share one job and its OIDC authority.
A compromised build step can give a false digest to the attestation step.

The build is reproducible, pinned, and network-dependent.
It is not hermetic.

## Threats and mitigations

Residual risk describes the risk after the current controls.

| ID | Threat | Current control | Residual risk |
| --- | --- | --- | --- |
| 1 | A poisoned action runs in a privileged job. | Full-SHA pins, the action allowlist, and pin checks restrict the action set. Zizmor checks other workflow risks. | Low. A reviewed pinned commit can still contain malicious code. |
| 2 | A poisoned build dependency enters an image. | Workcell pins snapshots, provider digests, base images, tools, and Rust vendor data. | Medium. `apt` and `npm ci` still use network data. |
| 3 | Fork code steals a secret. | GitHub makes fork tokens read-only. Fork code cannot use environment secrets. | Low. |
| 4 | A runner steals authority or changes output. | GitHub-hosted ephemeral jobs, narrow tokens, and disabled checkout credentials reduce exposure. | Medium. Jobs do not restrict network egress. |
| 5 | An attacker compromises a signing identity. | Cosign is keyless. The maintainer key stays outside CI. Releases are immutable. | Medium. Keyless signing removes stored Cosign keys, but it does not stop workflow-identity or maintainer-key misuse. |
| 6 | A false artifact gets authentic provenance. | GitHub OIDC binds provenance to the release workflow. Consumers pin that identity. | Medium. Build and provenance authority share a job. |
| 7 | Fork code poisons a trusted cache. | PR runs write only to PR-specific cache scopes. They can read `validator-main`. | Low. Non-PR runs can write `validator-main`. |
| 8 | A consumer installs an unverified artifact. | The documented release installer verifies Cosign data and the bundle digest before extraction. | Medium. Other installation paths remain available. |
| 9 | A malicious change reaches a release. | Signed commits, signed tags, required checks, environment approval, and immutable releases protect publication. | Medium. One maintainer signs and approves releases. |
| 10 | A large or hidden change avoids review. | The PR shape gate limits files, lines, areas, and binary changes. | Low. A reviewed exception can raise the limits. |
| 11 | Script logging exposes the stored token. | The token is environment-scoped and the workflows do not print it. | Low. Read-only scope remains a provisioning rule. |

## Signing-compromise incident response

Immutable releases preserve the incident record.
Do not delete a published tag or release.
Do not replace a published release.
Do not move a published tag.

### Maintainer-key evidence preservation

Use this procedure before key revocation or recovery:

1. Preserve Workcell session evidence with the [incident-response procedure](incident-response.md) when a session is affected.
2. Create a new owner-only evidence directory on detached operator-controlled storage.
3. Use read-only collection methods where available.
4. Do not copy the private key, GnuPG home, agent sockets, or credential files.
5. Preserve available host security, authentication, process, software-installation, shell-history, and malware-alert records from the suspected period.
6. Record each collection source, unavailable record, failure, and UTC collection time.
7. Record the compromised key fingerprint and suspected compromise time in the evidence directory.
8. Export the compromised GnuPG public key to the evidence directory.
9. Attach the storage read-only to a separate trusted system.
10. Record each artifact source and SHA-256 digest before inspection.
11. Keep raw host records private.
12. Apply the [private-report redaction rules](incident-response.md#7-private-report) before sharing any evidence.
13. Treat affected-host output as observed evidence, not independent integrity proof.

### Compromise: release workflow identity

Use this procedure if the release workflow identity signs unauthorized content:

1. Disable the Release workflow.
2. Block publication of new remote tags.
3. Cancel each queued or active Release workflow run.
4. Confirm that no queued or active Release workflow run remains.
5. Keep the release-environment reviewer rule enabled.
6. Record the affected tags, image digests, assets, and Rekor entries.
7. Revoke credentials that the attacker used.
8. Rotate exposed credentials.
9. Publish a security advisory that identifies the affected digests.
10. Patch `main` through the normal PR, review, and validation process.
11. Select a new patch version.
12. Create the signed tag.
13. Verify the signed tag.
14. Enable the Release workflow.
15. Permit publication of the selected tag.
16. Push the signed tag.
17. Follow the Release workflow to completion.
18. Name the safe replacement version in the advisory.

Do not edit the affected immutable release.
Do not reuse its tag.

### Compromise: maintainer signing key

Use this procedure after a maintainer-key compromise:

1. Disconnect the affected host from all networks.
2. Use a separate trusted system for steps 3 through 7.
3. Disable the Release workflow.
4. Block publication of new remote tags.
5. Cancel each queued or active Release workflow run.
6. Confirm that no queued or active Release workflow run remains.
7. Keep the release-environment reviewer rule enabled.
8. Complete the [maintainer-key evidence procedure](#maintainer-key-evidence-preservation).
9. Use a separate trusted system for all revocation, recovery, and release steps.
10. Publish the key revocation data.
11. Remove the key from the GitHub account.
12. Revoke each other credential accessible from the affected host at its source.
13. Record each revocation time.
14. Rotate each revoked credential.
15. Create a new signing key.
16. Register the new key.
17. Audit commits and tags from the affected period.
18. Publish an advisory for each affected release.
19. Patch `main` through the normal PR process.
20. Select a new patch version.
21. Use the new key to create the signed tag.
22. Verify the signed tag.
23. Enable the Release workflow.
24. Permit publication of the selected tag.
25. Push the signed tag.
26. Follow the Release workflow to completion.

Do not re-sign an existing tag.
Do not remove the immutable audit record.

### Compromise of a trust service

Fulcio, Rekor, and GitHub OIDC are trusted services.
Follow the service incident instructions after a compromise.

Treat releases from the affected period as suspect.
Publish an advisory.
Create a new patch release after the trust service is safe.

### Post-incident work

Write a post-incident report.
Link the report from the [security policy](../SECURITY.md).
Add a workflow or policy check when a repository control can prevent the event.
Run the hosted-control audit again.

## Known gaps and future work

1. **Consumer verification is not mandatory.**
   G3 is complete through lifecycle scenarios, hosted checks, and published `v1.0.2` evidence.
   The verified installer checks Cosign data, checksums, and optional GitHub attestations.
   However, local and manual installation paths can omit this verification.
   The acknowledged `--skip-verify` option also omits verification.
   Workcell also does not publish the installer as a signed standalone asset.

2. **Workcell does not meet SLSA Build L3.**
   Build steps and provenance authority share a job.
   A trusted builder must separate these authorities.

3. **The build is not hermetic.**
   Image builds use network package sources.
   The amd64 job builds from the archived source bundle.
   The separate arm64 job builds from the checked-out signed tag.

4. **CI runners do not have egress restrictions.**
   A compromised workflow step can use the runner network.

5. **No global rule rejects self-hosted runners.**
   Pin checks cover the CI build matrix, macOS matrices, and arm64 release label.
   Reviewers check all other `runs-on` values.

6. **The repository has one maintainer.**
   The same maintainer can sign a tag and approve the release environment.
   Workcell does not claim independent approval or separation of duties.

7. **The code can omit attestations.**
   `WORKCELL_RELEASE_NO_ATTEST=true` disables GitHub attestations.
   Hosted policy pins this value to `false` for the canonical repository.
   Cosign signatures remain mandatory.

8. **The release workflow does not verify its new outputs.**
   It creates signatures and attestations but does not verify them in the same run.
   Add independent post-production verification to close this gap.

## References

- Runtime [threat model](threat-model.md)
- [OWASP agentic mapping](owasp-agentic-mapping.md)
- [Provenance, signatures, and SBOMs](provenance.md)
- [Release posture](release-posture.md)
- [Release runbook](releasing.md)
- [GitHub workflow design](github-workflows.md)
- [Artifact retention policy](retention-policy.md)
- [`policy/github-hosted-controls.toml`](../policy/github-hosted-controls.toml)
- [`policy/tool-pins.toml`](../policy/tool-pins.toml)
- [`policy/allowed-actions.toml`](../policy/allowed-actions.toml)
- [Security policy](../SECURITY.md)
