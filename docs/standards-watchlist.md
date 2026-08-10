# Standards Watchlist

The maintainer reviews this list each quarter. Review it sooner when a tracked
body publishes a new version.

- Last review: 2026-08-06.
- Next planned review: 2026-11-06.

## Watchlist

| Work | Status at review | Workcell effect | Next check |
|---|---|---|---|
| [Model Context Protocol 2026-07-28](https://modelcontextprotocol.io/specification/2026-07-28) | Current dated specification | MCP transport, authorization, and extension changes can affect injected configuration and runtime controls. | Check adapter support for the stateless protocol and authorization changes. |
| [OWASP Top 10 for Agentic Applications 2026](https://genai.owasp.org/resource/owasp-top-10-for-agentic-applications-for-2026/) | Published 2026 edition | Workcell maps its controls to ASI01 through ASI10. | Check for a new edition. If OWASP publishes one, update the mapping. |
| [IETF WIMSE documents](https://datatracker.ietf.org/wg/wimse/documents/) | Active working-group and individual drafts | Workload identity can affect future agent authentication and delegation. | Check architecture, credential, proof-token, and AI-agent drafts. |

## Notes

The MCP 2026-07-28 version changes the core protocol to stateless requests. It
also adds new authorization and extension rules.

The OWASP item is the source for the
[agentic application mapping](owasp-agentic-mapping.md). Update both documents
together when OWASP publishes a new edition.

WIMSE work is not a Workcell implementation commitment. The AI-agent identity
documents remain Internet-Drafts. Do not describe them as an IETF standard or
an approved agent identity protocol.
