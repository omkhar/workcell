# Security Policy

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

## Supported branch

Security fixes are applied to `main`. There are no long-lived release branches.

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
