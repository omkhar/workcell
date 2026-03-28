# Adapter Porting Workflow

This workflow turns the provider matrix into an implementation sequence. It
assumes the secure boundary is a dedicated Colima VM profile plus a hardened
container inside it.

## Objectives

Preserve the useful parts of each provider's workflow surface without carrying
over provider-specific coupling into the shared boundary.

The output should optimize for:
1. Developer experience
2. Simplicity
3. Security invariant preservation
4. Performance
5. Idiomatic correctness

That optimization order only applies after the runtime boundary and invariants
are fixed. Do not trade them away for ergonomics.

## Operating Rules

1. Keep the policy layer and runtime boundary separate.
2. Prefer one clear mechanism over multiple overlapping ones.
3. Treat hooks and rules as guardrails, not as the boundary.
4. Keep high-autonomy execution inside the strongest available runtime boundary only.
5. Make every workflow reproducible from files in the repo.

## Migration Sequence

### 1. Lock the boundary

Start by implementing and documenting the Tier 1 runtime:

1. Dedicated Colima VM profile for the selected Workcell task.
2. Hardened container inside that VM.
3. Only the task workspace mounted.
4. No host home, no agent socket, no Docker socket, no keychain passthrough.
5. Network off by default.

Do not let the policy layer expand trust before this boundary exists.

### 2. Establish the generic core and adapter split

Create the shared core first, then the provider adapters:

1. shared invariants and threat model
2. one provider adapter directory per product
3. native config for each provider
4. explicit MCP allowlists or deny-by-default posture
5. provider-specific downgrade notes for GUI surfaces

### 3. Port workflows, not syntax

Rewrite useful provider workflows as generic bounded workflows:

1. review workflows become provider-specific review commands plus peer-review gates
2. fix workflows become bounded execution plus plan/implement/test/review steps
3. bootstrap/install becomes pinned adapter installation
4. hook-based logging becomes wrapper-script logging or Git-based auditing

### 4. Add subagent reviews

Use subagents as the peer-review ring before merging any parity change:

1. Architecture and portability review.
2. Security boundary review.
3. macOS and container boundary review.
4. Provider-native config consistency review.

No change should land until the subagent consensus is clear or the disagreement is documented.

### 5. Verify the invariants

Every implementation slice should be checked against the same tests:

1. Host secret reads are blocked.
2. Writes outside the workspace are blocked.
3. Unapproved MCP access is blocked.
4. Network egress is controlled.
5. Destructive shell patterns are blocked or escalated.
6. Workflow wrappers still work end-to-end.

## Review Gate

Before merging any doc or runtime change, ask:

1. Did this improve developer experience or just add indirection?
2. Is there a simpler way to express the same invariant?
3. Does the control survive prompt injection and agent drift?
4. Does the control live in the runtime boundary, not only in text?
5. Is the performance cost justified by the security gain?

If the answer to any of those is no, simplify before proceeding.
