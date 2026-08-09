# Extend a Provider Adapter

Use this page for a new credential type or provider adapter. Read
[Invariants](invariants.md), [Threat Model](threat-model.md), and the
[Adapter Porting Workflow](../workflows/adapter-porting.md) first.

The provider registry is data in `internal/adapters/data.go`. A registry change
is only one part of an adapter change. Runtime dispatch, policy, seed logic,
validation, and documents must change together.

## Add a Credential Type

### 1. Register the credential

In `internal/adapters/data.go`:

1. Add the key to the provider credential list.
2. Add its mount path under `/opt/workcell/host-inputs/credentials/`.
3. Add its provider-home destination to the reserved targets.

The reserved target stops a general copy rule that tries to replace a Workcell
control file. See [Injection Policy explicit limits](injection-policy.md#explicit-limits).
Adapter tests must confirm that each key has a credential mount path. They must
confirm that the path is under the credential input root.

### 2. Add a resolver for a host-owned source

For a host resolver, add the name to the resolver allowlist. Then add both the
resolution and readiness cases. An allowlist value without those cases passes
the policy parser but stops at launch with an unsupported-resolver error.

A resolver must create a regular file in the per-launch host bundle. Do not
pass a keychain, agent, socket, or host credential store into the runtime.
See the [Provider Bootstrap Matrix](provider-bootstrap-matrix.md#current-matrix)
for the supported resolver states.

### 3. Check the renderer

The credential renderer uses the adapter registry. A registered key does not
need a new renderer branch. After the renderer creates the bundle, bundle
preparation must reject a direct source in the mounted workspace. It must also
require the documented owner-only source mode.
See the [Injection Policy security boundary](injection-policy.md#security-boundary).

### 4. Seed the provider home

In `runtime/container/home-control-plane.sh`, copy the mounted credential to its
reserved provider-home target. Validate the file shape when the provider has a
format requirement.

An optional or mode-filtered key must not stop provider-home creation when it is
absent. Follow the current guarded copy patterns. Do not add a bare copy call
that fails under `set -e` for an optional key.

### 5. Update all host surfaces

Find a current credential key. Use its occurrences to update each applicable
layer:

- Adapter registry and reserved targets.
- Destinations for staged authentication.
- Status order and bootstrap summaries.
- Resolver allowlist, resolution, and readiness.
- Launcher `supported_credential_keys` output for the no-policy state.
- Provider-home seed logic for the runtime.
- [Credential keys](injection-policy.md#credential-keys).
- [Provider Bootstrap Matrix](provider-bootstrap-matrix.md#current-matrix).
- [Adapter Control Planes](adapter-control-planes.md) and the adapter README.

A partial change can accept hand-written policy but fail `workcell auth set` or
report the wrong provider authentication mode.

### 6. Test the change

At minimum, extend adapter and injection tests. Run:

```sh
go test ./internal/adapters/... ./internal/injection/...
```

Also run each focused authentication or runtime test for the new key.

## Add or Extend a Provider

### 1. Preserve the shared boundary

Do not add an ambient host credential, broad mount, or host socket. Keep the
selected target, container controls, managed provider home, and explicit
network posture.

### 2. Register a new provider

For a new provider:

1. Define the identifier in `internal/providerid`. A planned identifier can stay
   outside `AllProviders` and fail closed during implementation.
2. Add the identifier to `CredentialMetadataProviders` when the adapter registry
   supplies its credential metadata. A planned provider can stay outside
   `AllProviders` during this step.
3. Add the adapter registry row with credential keys, paths, and reserved
   targets.
4. Add launcher validation and help output.
5. Add runtime entry-point and wrapper dispatch.
6. Add the provider binary to each applicable exec-guard and protected-runtime
   list for development mode.
7. Add the provider binary and wrapper links to the runtime image.
8. Add the provider allowlist, credential setup, and probe cases to
   `scripts/provider-e2e.sh`.
9. Add the identifier to `AllProviders` so that validation can select it.
10. Run the deterministic tests for the final supported-provider set.
11. Complete live provider certification before you sign the support-claim
   commit.

A directory and registry row do not make a provider supported. Until each
dispatch and protection layer accepts the identifier, the provider must fail
closed.

### 3. Reject unsafe provider options

Add or extend the provider checks in
`runtime/container/provider-policy.sh`. Reject flags and subcommands that can
change instructions, tools, MCP configuration, or permissions. Also reject
options that change sandbox posture or remote control outside Workcell policy.

Wire a new provider into both argument validation and the provider wrapper. The
wrapper checks the arguments again. Current provider wrappers do not accept an
unsafe-argument exception in a `breakglass` session.

### 4. Build the managed control plane

In non-breakglass modes, build the provider home from these sources:

- The immutable adapter baseline.
- Explicit staged inputs.
- Imported workspace instructions that the provider supports.

On the default safe path, mask each mutable repository control path that the
provider can read. Require explicit acknowledgement and label every supported
exception lower assurance. The operator must give that acknowledgement for each
supported exception. Add each baseline file to the source for the control-plane
manifest. Regenerate the manifest. Run
`scripts/verify-control-plane-manifest.sh`. A runtime prefix check examines only
files that the manifest already lists.

### 5. Map autonomy and network needs

Map `--agent-autonomy` to the provider's native surface. Add only fixed endpoint
literals that the supported provider path needs. Scrub environment values for
provider telemetry by default. Label each permitted lower-assurance option.

### 6. Add evidence before a support claim

Add deterministic adapter, policy, invariant, scenario, and smoke tests. Before
you sign a commit with a new Tier 1 support claim, complete the live
`provider-e2e` certification for that provider.

Repository tests do not replace a live provider certification. Keep the
provider unsupported until the implementation, evidence, support matrix, and
operator documents have the same claim.

### 7. Update the contract

Update these sources in the same change:

- [Provider Matrix](provider-matrix.md).
- [Adapter Control Planes](adapter-control-planes.md).
- The adapter README.
- Injection policy and bootstrap status when authentication changes.
- Operator contract and requirements when the public workflow changes.

Complete the Codex review loop before merge.
