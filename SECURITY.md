# Security Policy

## Reporting

Do not disclose new sandbox escapes, credential leaks, signing bypasses, or
boundary-preservation bugs in a public issue.

Use GitHub Private Vulnerability Reporting or a GitHub security advisory for
this repository. If that is unavailable, contact the repository owner privately
through GitHub first.

## In scope

High-priority reports include:

- reads of host secrets outside the documented boundary
- writes outside the intended workspace without explicit `breakglass`
- unmanaged host socket or credential passthrough
- silent trust widening through repo content or workflow changes
- release signing, SBOM, or provenance regressions

## Supported branch

Security fixes are expected on `main`.
