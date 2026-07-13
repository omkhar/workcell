# Security Policy

## Supported Versions

Workcell operates in single-maintainer release mode. Security fixes are applied
to `main` (there are no long-lived release branches) and shipped only in the
**latest released version**; there are no backports to earlier tags. A fix is
delivered as the next release cut from `main` (patch release, or the next
release candidate while the line is pre-1.0), so any release older than the
newest one stops receiving fixes as soon as a newer release exists — including
earlier release candidates, which are superseded in place.

| Version | Security fixes |
| --- | --- |
| Latest release | Yes |
| Any earlier release / superseded pre-release | No — upgrade to the latest |

Always verify a release before installing it: `scripts/install-release.sh`
checks the release's cosign signature and digest fail-closed before any bundle
code runs (see [`docs/install-lifecycle.md`](docs/install-lifecycle.md)).

## Signing key

Release tags and commits are GPG-signed by the maintainer. Confirm any key you
import against this fingerprint before trusting a signature:

```text
Omkhar Arasaratnam <omkhar@gmail.com>
ed25519  9055 4248 C4F7 CC08 6BB7  45D0 DA5A 8E9F 536C 42FD
```

When you obtain the installer from a checkout of a release tag (the verified
install path in [`docs/getting-started.md`](docs/getting-started.md)), verify
that tag's signature against this key **before** running any code from it:

```bash
# import the maintainer key (e.g. from a keyserver) and confirm the fingerprint above, then:
git tag -v vX.Y.Z
```

This authenticates the installer source; the release *artifacts* are
independently verified with keyless cosign signatures (no long-lived signing
key), documented in [`docs/provenance.md`](docs/provenance.md).

## Reporting

Do not disclose new sandbox escapes, credential leaks, signing bypasses, or
boundary-preservation bugs in a public issue.

Use [GitHub Private Vulnerability Reporting][pvr] to open a security advisory
for this repository. If that channel is unavailable, contact the repository
owner privately through GitHub first.

[pvr]: https://github.com/omkhar/workcell/security/advisories/new

## Response

We aim to acknowledge reports within **5 business days** and to provide an
initial assessment within **10 business days**. Critical issues (sandbox
escapes, secret exposure) will be prioritised. You will be credited in the
advisory unless you request otherwise.

## In scope

High-priority reports include:

- reads of host secrets outside the documented boundary
- writes outside the intended workspace without explicit `breakglass`
- unmanaged host socket or credential passthrough
- silent trust widening through repo content or workflow changes
- release signing, SBOM, or provenance regressions

## Out of scope

The following are not in scope for this policy:

- social engineering attacks
- physical access attacks
- bugs in third-party providers (Codex, Claude, Gemini) that do not
  involve a Workcell boundary violation
- issues reproducible only in unsupported configurations (non-macOS host,
  `breakglass` mode used as intended)

## Container git-config blocklist

The set of `git-config` keys that the in-container git wrapper and the
in-container LD_PRELOAD exec guard refuse to honor (e.g. `core.askpass`,
`core.hookspath`, `credential.*.helper`, `includeif.*.path`,
`pager.*`) is the canonical control-plane denylist for git-mediated
bypasses.  The single source of truth is
[`policy/git-config-blocklist.toml`](policy/git-config-blocklist.toml);
`scripts/verify-invariants.sh` enforces parity between the TOML and
the three enforcement points (`scripts/workcell`,
`runtime/container/bin/git`, `runtime/container/rust/src/lib.rs`).
Adding a key requires editing the TOML and updating each enforcer in
the same PR.

## Operator incident response

If you are an operator responding to a suspected runtime boundary breach — an
agent escaping the session, host credential exposure, or workspace tampering —
follow the [operator boundary-incident response runbook](docs/incident-response.md)
to contain, preserve evidence, collect a redacted support bundle, and escalate
through the reporting channel above.

## CI/CD threat model

The [CI/CD threat model](docs/ci-threat-model.md) covers the build and release
pipeline: runner trust tiers, secrets handling and rotation, attestation
verification assumptions, and the signing-compromise incident response runbook.

## Disclosure

We follow a coordinated disclosure model. Please allow a reasonable fix window
before public disclosure. We will notify you when a fix is ready and work with
you on timing.

## Past security reviews

Closed-finding evidence and assurance artifacts from past reviews live under
[docs/security/](docs/security/):

- [2026-04-24 validation summary](docs/security/security-findings-2026-04-24-validation.md)
- [2026-04-24 PoC matrix](docs/security/security-findings-2026-04-24-poc-matrix.md)
- [2026-04-24 mutation results](docs/security/security-findings-2026-04-24-mutation-results.md)
- [2026-04-24 verification CSV](docs/security/security-findings-2026-04-24-verification.csv)
