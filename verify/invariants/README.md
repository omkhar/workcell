# Invariant Test Plan

The invariant suite answers one question: does the current implementation still
match the documented security and control-plane contract?

## Minimum checks

The suite should keep verifying that:

1. the provider launches inside the container, not on the host
2. the runtime receives only the reviewed host mounts
3. host auth state and sockets stay out by default
4. the selected workspace is the only writable host mount
5. network posture is applied for the managed modes
6. `breakglass` is explicit and visibly different
7. provider control-plane files are present and loadable
8. unsafe broad workspaces are rejected

## Negative checks

The suite should fail if it finds:

- `docker.sock` passthrough
- host home passthrough
- host provider-state passthrough
- SSH or GPG agent socket passthrough
- missing egress controls on the managed path
- silent defaulting into `breakglass`
