# Adapter Porting Workflow

Use this workflow when you add or change a provider adapter.

## Goal

Keep one shared runtime boundary and one thin provider adapter. Use the native
provider control plane. Do not treat it as the security boundary.

## Work sequence

1. Confirm the supported target boundary and assurance status.
2. List each provider file that Workcell owns, masks, seeds, or links.
3. Use the smallest provider-native configuration surface.
4. Add the seed path for the provider home.
5. Block flags and paths that lower assurance without operator acknowledgment.
6. Add invariant, scenario, and adapter tests.
7. Run live certification for each new support claim.
8. Update the provider matrix and operator documents.
9. If host or target support changes, update the host-support matrix.
10. Complete adversarial and Codex review loops.

The seed path can use these ordered sources:

1. Immutable adapter baseline.
2. Supported repository instruction files.
3. Explicit injection-policy documents.

Do not let repository content replace immutable adapter controls.

## Required documentation

Update each document that the adapter change affects in the same change:

- `docs/provider-matrix.md`
- `docs/adapter-control-planes.md`
- Provider quickstart.
- Adapter README.
- Injection policy.
- Support and certification evidence.

## Review questions

1. Does the adapter preserve the runtime boundary?
2. Can repository content replace a trusted control?
3. Does each lower-assurance path have an explicit label?
4. Do tests exercise the shipped path?
5. Does live evidence support each new claim?
6. Did the change add unnecessary abstraction?
