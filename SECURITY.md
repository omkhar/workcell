# Security Policy

## Reporting

Do not open a public issue for a new sandbox escape, secret-exposure path,
credential leak, signing bypass, or boundary-preservation bug.

Use GitHub Private Vulnerability Reporting or a GitHub security advisory for
this repository. If that is unavailable, contact the repository owner privately
through GitHub before disclosing details.

## Scope

High-priority issues include:

- reads of host secrets outside the documented trust boundary
- writes outside the intended workspace without explicit `breakglass`
- ambient network access in `strict`
- unmanaged host socket or credential passthrough
- release signing, SBOM, or provenance regressions
- workflow changes that widen trust unexpectedly

## Supported branch

- `main`
