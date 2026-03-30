# Adapter Porting Workflow

Use this checklist when adding or refactoring a provider adapter.

## Goal

Preserve the shared Workcell boundary while mapping into the provider's native
control plane. Do not blur the boundary and the adapter into one layer.

## Design order

Optimize in this order:

1. Developer experience
2. Simplicity
3. Security invariant preservation
4. Performance
5. Idiomatic correctness

That order only applies after the runtime boundary is fixed.

## Porting sequence

### 1. Lock the shared boundary first

Before touching provider specifics, confirm that the shared runtime still
guarantees:

- dedicated VM profile
- hardened inner container
- narrow mount set
- explicit network posture
- no host home, sockets, or ambient credential passthrough

### 2. Define the native control plane

Document exactly which provider files Workcell owns, seeds, links, or masks.
Keep the list small and auditable.

### 3. Keep the adapter thin

Use the provider's native config surfaces where possible. Do not invent a fake
universal provider config layer.

### 4. Add the runtime seeding path

Update the home-control-plane logic so the provider-facing home is rebuilt from:

- the immutable adapter baseline
- imported workspace docs
- explicit injection-policy input

### 5. Add deny-list and downgrade logic

Block provider-native flags or workflows that would silently widen trust, and
document any lower-assurance paths explicitly.

### 6. Add validation with the adapter

No adapter change is complete without matching invariant or smoke coverage.

### 7. Update the docs in the same change

At minimum, update:

- `docs/provider-matrix.md`
- `docs/adapter-control-planes.md`
- provider-specific quickstart or adapter README material if behavior changed

## Review questions

Before merging an adapter change, ask:

1. Did this improve the workflow, or just add indirection?
2. Is the provider config being mistaken for the primary boundary?
3. Can repo content retake the control plane after this change?
4. Are any lower-assurance paths clearly labeled?
5. Is the new behavior covered by validation?
