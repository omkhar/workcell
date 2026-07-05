# Extending Adapters: Worked Contributor Examples

Two step-by-step examples for the most common adapter-contributor tasks. Each
step names the file it touches and the security invariant or threat-model item
it must satisfy, so a change either preserves the boundary or fails a gate.

Read [invariants.md](invariants.md) and [threat-model.md](threat-model.md)
first. For the higher-level porting checklist see
[../workflows/adapter-porting.md](../workflows/adapter-porting.md). For the
per-adapter reference see [adapter-control-planes.md](adapter-control-planes.md)
and each adapter's `README.md` under [`../adapters/`](../adapters/README.md).

The per-adapter registry is data, not code: adding a credential type or a
provider is a table edit in `internal/adapters/data.go` plus the runtime seeding
and docs, not a new Go package (`internal/adapters/adapters.go`).

## Example 1: Add a credential type

Goal: add a new provider-native credential key (for an existing adapter) that an
operator can stage through injection policy. This walkthrough uses a
hypothetical `gemini_settings` key for the Gemini adapter as a concrete shape;
substitute your real key and target.

### Steps (add a credential type)

1. **Register the key and its mount path.** In `internal/adapters/data.go`, add
   the key to the provider's `credentialKeys` and add a
   `credentialContainerPaths[key]` entry under
   `/opt/workcell/host-inputs/credentials/`. Add the in-container destination to
   that provider's `reservedTargets`.
   - Invariant [§2 writes stay inside the intended workspace](invariants.md#2-writes-stay-inside-the-intended-workspace):
     the reserved target makes the destination Workcell-owned so a `[[copies]]`
     entry cannot clobber it (see the "no writes into Workcell-managed
     control-plane paths" limit in
     [injection-policy.md](injection-policy.md#deliberate-limits)).
   - Guardrail: `TestScopedCredentialKeysHaveContainerPaths` and
     `TestCredentialContainerPathsRootedAtHostInputs` in
     `internal/adapters/adapters_test.go` fail if the mount path is missing or
     not rooted at the host-inputs credentials dir.

2. **(Optional) Register a host resolver.** Only if the key is resolver-backed,
   add it to `allowedResolvers` in
   `internal/authresolve/resolve_credential_sources.go`, and add the matching
   `resolveCredential` and `ProbeResolverReadiness` cases in
   `resolve_provider_credentials.go` — the `allowedResolvers` entry only makes
   policy validation accept the name; without the resolution/probe cases, launch
   returns `unsupported credential resolver`. Resolvers materialize to
   ordinary files under the per-launch injection bundle host-side; they never
   pass Keychain or host-agent access into the runtime.
   - Invariant [§1 host secrets stay outside the default trust boundary](invariants.md#1-host-secrets-stay-outside-the-default-trust-boundary):
     resolver-backed auth is host-side preprocessing only
     ([injection-policy.md](injection-policy.md#provider-auth-maturity)).

3. **Confirm rendering picks up the key.** Rendering is table-driven off
   `adapters.CredentialContainerPaths()` in
   `internal/injection/render_credentials.go`, so a registered key flows without
   editing the renderer. It validates that direct credential sources live
   outside the mounted workspace and are owner-only.
   - Invariant [§1](invariants.md#1-host-secrets-stay-outside-the-default-trust-boundary):
     a source under the workspace is rejected so the original secret path is not
     also exposed through the workspace mount
     ([injection-policy.md](injection-policy.md#how-it-works)).

4. **Seed the credential into the provider home.** In
   `runtime/container/home-control-plane.sh`, copy the mounted credential into
   its provider-home target with
   `workcell_copy_manifest_credential_file <key> "${HOME}/<target>"` (the Gemini
   block near the other `gemini_*` seeds is the pattern). Validate the file shape
   if the provider requires it.
   - Invariant [§1](invariants.md#1-host-secrets-stay-outside-the-default-trust-boundary):
     the credential is copied from a read-only mount into session-local state and
     the direct mount is not persisted back into the baseline.

5. **Document the key.** Add a row to the credential-keys table and the
   provider-auth-maturity table in [injection-policy.md](injection-policy.md),
   update [adapter-control-planes.md](adapter-control-planes.md) if the managed
   file set changes, and add the key to the adapter's `README.md` auth section.
   - Also update the operator status surface: `print_injection_status` in
     `scripts/workcell` prints a hard-coded `supported_credential_keys=...` list
     when no policy is loaded, so a new key is accepted by the table-driven Go
     paths but missing from `workcell --auth-status` guidance until that list is
     updated too.
   - Threat-model note: an undocumented credential path is exactly the "trust
     widened without review" case CONTRIBUTING flags — keep the docs in the same
     change.

6. **Test.** Extend `internal/adapters/adapters_test.go` and the injection render
   tests, then run `go test ./internal/adapters/... ./internal/injection/...`.

> **The registration spans layers — grep to find them all.** A credential key is
> also referenced by the host auth-management surfaces: `workcell auth set`
> staging destinations (`canonicalCredentialDestinations` in
> `internal/authpolicy/staging.go`) and `--auth-status` ordering/summaries
> (`statusOrder`, `bootstrapSummaryForCredential` in `internal/authpolicy/`),
> plus the hard-coded `supported_credential_keys` list in `scripts/workcell`.
> These sites are intentionally spread across the validation, resolution,
> staging, and status layers, so grep the tree for an existing key (for example
> `codex_auth`) and mirror every hit — a key that lands only in the registry
> renders from hand-written TOML but fails `workcell auth set` or reports
> `provider_auth_mode=none`.

### Checklist touched (add a credential type)

- [§1 host secrets stay outside the boundary](invariants.md#1-host-secrets-stay-outside-the-default-trust-boundary)
  (steps 2, 3, 4)
- [§2 writes stay inside the intended workspace](invariants.md#2-writes-stay-inside-the-intended-workspace)
  (step 1: reserved target ownership)
- Injection-policy [deliberate limits](injection-policy.md#deliberate-limits):
  no arbitrary env-var secret injection, no `[[copies]]` into managed paths
  (steps 1, 3)

## Example 2: Extend an adapter (or add a new one)

Goal: change an adapter's provider-native surface — for instance add a blocked
unsafe flag, or promote a planned adapter such as `antigravity`.

### Steps (extend an adapter)

1. **Lock the shared boundary first.** Do not add host mounts, sockets, or
   ambient credential passthrough. Confirm the VM profile, inner container,
   narrow mount set, and network posture are unchanged
   ([../workflows/adapter-porting.md](../workflows/adapter-porting.md), step 1).
   - Invariant [§1](invariants.md#1-host-secrets-stay-outside-the-default-trust-boundary)
     and [§2](invariants.md#2-writes-stay-inside-the-intended-workspace).

2. **Register the provider (new adapter only).** Add the id to
   `internal/providerid/providerid.go` and the supported set, and add a row to
   `providers` in `internal/adapters/data.go` with its credential keys, container
   paths, and reserved targets. A directory without a registry row stays a
   fail-closed scaffold (see the `antigravity` README).
   - Also wire the host launcher and runtime dispatch: `scripts/workcell`
     validates the agent name against a fixed set (which also backs `--help`,
     `workcell why`, and `--auth-status`), and `runtime/container/entrypoint.sh`,
     the Rust core-launcher, and the Dockerfile core-wrapper symlinks map default
     launches and managed-wrapper routing for the same fixed
     `codex|claude|copilot|gemini` set. Until all of these accept the id,
     `workcell --agent <new>` fails before or during runtime prep (rejected as an
     `Unsupported agent/ui combination`, or not routed through the managed
     provider wrapper), so the registry edits alone are not enough.
   - Guardrail: `internal/providerid/providerid_test.go` asserts a planned
     provider stays out of the supported set until support lands.

3. **Add or extend unsafe-argument rejection.** Edit the provider's
   `reject_unsafe_<name>_args` in `runtime/container/provider-policy.sh` (and wire
   a new provider into `validate_command_args` and
   `runtime/container/provider-wrapper.sh`). Block provider-native flags and
   subcommands that widen trust. The only code-level exemption is
   `provider_policy_allows_breakglass`, but it is false inside
   `provider-wrapper.sh` (which always sets `WORKCELL_WRAPPER_CONTEXT=1`), so the
   wrapper re-check rejects these flags even in a `breakglass` session —
   `container-smoke.sh` asserts breakglass overrides still fail.
   - Invariant [§3 repo policy must not silently widen trust](invariants.md#3-repo-policy-must-not-silently-widen-trust)
     and [§5 destructive or trust-widening actions need defense in depth](invariants.md#5-destructive-or-trust-widening-actions-need-defense-in-depth).

4. **Seed the control plane.** Rebuild the provider home in
   `runtime/container/home-control-plane.sh` from the immutable baseline under
   `adapters/<name>/`, explicit injection inputs, and masked workspace imports.
   Mask any repo-local provider control-plane files the provider reads.
   - Invariant [§3](invariants.md#3-repo-policy-must-not-silently-widen-trust):
     repo content must not retake the control plane.

5. **Map autonomy and label lower-assurance paths.** Map
   `workcell --agent-autonomy` to the provider's native flags in the wrapper and
   record the mapping in
   [adapter-control-planes.md](adapter-control-planes.md#autonomy-mapping).
   Scrub provider telemetry env by default; any opt-in is lower assurance.
   - Invariant [§4 network posture is explicit](invariants.md#4-network-posture-is-explicit)
     and [§6 lower-assurance paths are labeled](invariants.md#6-lower-assurance-paths-are-labeled).

6. **Ship validation with the change.** Add or extend deterministic tests
   (`internal/adapters/adapters_test.go`, provider-policy coverage) and a
   scenario/smoke path. No adapter change is complete without matching invariant
   coverage ([../workflows/adapter-porting.md](../workflows/adapter-porting.md),
   step 6).
   - Promotion gate: before `provider-matrix.md` claims a supported Tier 1, run
     the live provider certification (`provider-e2e`) — deterministic tests and a
     smoke path are necessary but not sufficient. A supported-tier claim without
     that live-certification evidence is exactly what the release gate forbids.

7. **Update the docs in the same change.** Update
   [provider-matrix.md](provider-matrix.md),
   [adapter-control-planes.md](adapter-control-planes.md), and the adapter's
   `README.md`.

### Checklist touched (extend an adapter)

- [§1](invariants.md#1-host-secrets-stay-outside-the-default-trust-boundary),
  [§2](invariants.md#2-writes-stay-inside-the-intended-workspace) (step 1: no new
  host exposure)
- [§3 no silent trust widening](invariants.md#3-repo-policy-must-not-silently-widen-trust)
  (steps 3, 4)
- [§4 explicit network posture](invariants.md#4-network-posture-is-explicit) and
  [§6 labeled lower-assurance paths](invariants.md#6-lower-assurance-paths-are-labeled)
  (step 5)
- [§5 defense in depth](invariants.md#5-destructive-or-trust-widening-actions-need-defense-in-depth)
  (step 3: wrapper-side rejection is defense in depth, not the boundary)
