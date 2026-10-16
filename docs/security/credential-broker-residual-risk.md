# Credential Broker Residual Risk

This record covers egress proxy sessions that use the `keychain-broker`
resolver. The brokered keys are the Claude API key, the Gemini API key and the
Codex API key. The agent holds only a `wcph-` placeholder. The egress proxy
sidecar swaps the placeholder for the real key on the wire.

[Injection policy](../injection-policy.md) describes the controls.
[Invariants](../invariants.md) names the gates for `phantom-no-real-secret` and
`session-ca-constrained`. This record lists the risks that stay after those
controls.

## Confused deputy through a brokered host

The agent cannot read the key, but it can use it. A request from the agent that
carries the placeholder in a brokered header gets the real key. The request can
use any path on the brokered host. For example, the agent can call any OpenAI
API with the user's Codex key.

The grant table in `internal/credbroker/server.go` limits each key to one host
and its headers. The broker allows 300 requests each minute for each session.
It records each request in its ledger, and the launcher audits the ledger as
`credential_broker_summary`.

Residual risk: the broker does not limit the API path or the request body on a
brokered host.

## Domain fronting on CDN hosts

The proxy admits a connection only when its SNI is on the session allowlist. For
a host that it does not terminate, the proxy cannot read the HTTP `Host` header
inside TLS. A CDN host on the allowlist can then carry traffic to another origin
on the same CDN.

For a brokered host, the proxy terminates TLS and sets the upstream `Host`
header to that host (`internal/egressproxy/terminate.go`). A fronted request
cannot take the key to another origin.

Residual risk: the agent can reach other origins behind an allowlisted CDN
host, without a brokered key.

## Prompt-channel exfiltration

The agent can put any data that it can read into a request to its provider. The
broker and the proxy do not inspect request bodies.

Residual risk: workspace data can leave through the provider API.

## Copilot token

Copilot is not brokered. The agent holds the GitHub token in
`COPILOT_GITHUB_TOKEN`, also under the egress proxy. Such a session reports
`credential_mode=direct`.

Residual risk: the agent can use and copy the GitHub token.

## OAuth tokens

OAuth flows stay direct: Claude login, Gemini OAuth and Codex ChatGPT login.
The agent holds those access and refresh tokens. Such a session reports
`credential_mode=direct`.

Residual risk: the agent can use and copy OAuth tokens.

## Session CA trust in child processes

The provider wrapper sets `NODE_EXTRA_CA_CERTS` and `CODEX_CA_CERTIFICATE`, and the
provider's child processes inherit them. Those processes also trust the session
CA. The CA can sign only for the brokered hosts, and its key stays in the
sidecar's memory (`internal/egressproxy/terminate.go`).

Residual risk: a process that the agent starts can trust a sidecar leaf for a
brokered host. That leaf gives it nothing that the placeholder does not give.

## Session lifetime

The session CA lives at most 24 hours (`MaxCALifetime` in
`internal/egressproxy/terminate.go`). A session that runs longer loses TLS to
the brokered host.

Residual risk: a session that runs longer than 24 hours needs a restart.
