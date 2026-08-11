# Standards Watchlist

The maintainer reviews this list each quarter. Review it sooner when a tracked
body publishes a new version.

- Last full agent-standards review: 2026-08-06.
- Engineering-practice sources reviewed: 2026-08-11.
- Next planned review: 2026-11-06.

## Watchlist

| Work | Status at review | Workcell effect | Next check |
|---|---|---|---|
| [Model Context Protocol 2026-07-28](https://modelcontextprotocol.io/specification/2026-07-28) | Current dated specification | MCP transport, authorization, and extension changes can affect injected configuration and runtime controls. | Check adapter support for the stateless protocol and authorization changes. |
| [OWASP Top 10 for Agentic Applications 2026](https://genai.owasp.org/resource/owasp-top-10-for-agentic-applications-for-2026/) | Published 2026 edition | Workcell maps its controls to ASI01 through ASI10. | Check for a new edition. If OWASP publishes one, update the mapping. |
| [IETF WIMSE documents](https://datatracker.ietf.org/wg/wimse/documents/) | Active working-group and individual drafts | Workload identity can affect future agent authentication and delegation. | Check architecture, credential, proof-token, and AI-agent drafts. |
| [NIST SSDF 1.1](https://csrc.nist.gov/pubs/sp/800/218/final) | Final, published February 2022 | The engineering baseline maps secure development across the lifecycle. | Check for a final revision or supplement. |
| [NIST SP 800-160 Rev. 1](https://csrc.nist.gov/pubs/sp/800/160/v1/r1/final) | Final, published November 2022 | Trust objectives inform architecture, requirements, verification, and operations. | Check for a final revision. |
| [ISO/IEC 25010:2023](https://www.iso.org/standard/78176.html) | Published second edition | Relevant product-quality characteristics inform requirements, tests, acceptance, and measures. | Check its systematic-review status. |
| [SLSA v1.2](https://slsa.dev/spec/v1.2/) | Approved specification | Build and Source tracks affect provenance claims and source-control assessment. | Reassess exact Workcell track and level claims after a new approved version. |
| [GitHub Actions secure use](https://docs.github.com/en/actions/reference/security/secure-use) | Current official guidance | Token permissions, untrusted checkout, action identity, and secret handling affect workflow controls. | Check after a material Actions security change. |
| [DORA metrics](https://dora.dev/guides/dora-metrics/) | Current five-measure guidance, updated January 2026 | Repository trends can guide delivery improvement after Workcell defines deployments and recovery. | Check definitions before changing a delivery measure. |
| [Google Engineering Practices](https://google.github.io/eng-practices/review/reviewer/standard.html) | Current official guidance | Small changes, technical evidence, and code-health improvement inform review practice. | Check for material review-policy changes. |
| [Go guidance](https://go.dev/doc/comment) and [Rust API Guidelines](https://rust-lang.github.io/api-guidelines/) | Current official project guidance | Language conventions inform APIs, documentation, and tests. | Check after a supported language or toolchain change. |
| [OpenSSF Best Practices Badge](https://openssf.org/projects/best-practices-badge/) | Current voluntary self-certification program | The checklist can support open-source hygiene. It does not prove Workcell isolation. | Check criteria before starting the deferred badge assessment. |

## Notes

The MCP 2026-07-28 version changes the core protocol to stateless requests. It
also adds new authorization and extension rules.

The OWASP item is the source for the
[agentic application mapping](owasp-agentic-mapping.md). Update both documents
together when OWASP publishes a new edition.

WIMSE work is not a Workcell implementation commitment. The AI-agent identity
documents remain Internet-Drafts. Do not describe them as an IETF standard or
an approved agent identity protocol.
